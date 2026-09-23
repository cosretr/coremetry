package oracle

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// enrich.go — Oracle Aşama 3 dilim E (v0.10.898; spec Onay 2026-09-23,
// audit §7 Aşama 3 "enrich.go"): dış tarayıcının OnEvidence kancası —
// açılışta hemen, sürerken 5 dk'da bir, tik başına ≤20 (external.go).
// Kanıt: (op, kod, kanal) satırları oracle_error_log'dan (PK öneki
// source_id+time; FINAL; ≤500), trace listesi (≤50, en yeni önce) →
// SpanSummariesForTraces (±5 dk), dağılımlar (external_code, error_type,
// instance, host, channel, task — top 10 + sayım), özne kaynağı (trace'ten /
// öğrenilmiş / bilinmiyor). Hipotez anchor=problem; sentezleyici bu anchor'ı
// atlar (ruleID öneki), yani kanıt üzerine yazılmaz; mevcut hipotezin
// aday/skor alanları korunur (ilk yazan kazanır), yalnız Deep tazelenir.

const (
	enrichRowLimit   = 500
	enrichTraceLimit = 50
	enrichTopN       = 10
	enrichPad        = 5 * time.Minute
	enrichBudget     = 8 * time.Second
)

// enrichStore — chstore.Store'un dört metodu (testte sahte).
type enrichStore interface {
	OracleErrorsByKey(ctx context.Context, sourceID, op, code, channel string, from, to time.Time, limit int) ([]chstore.OracleErrorRow, error)
	SpanSummariesForTraces(ctx context.Context, ids []string, from, to time.Time) ([]chstore.TraceSpanSummary, error)
	GetHypothesis(ctx context.Context, anchorKind, anchorID string) (*chstore.RootCauseHypothesis, error)
	UpsertHypothesis(ctx context.Context, h chstore.RootCauseHypothesis) error
}

// Enricher — OnEvidence gövdesi.
type Enricher struct {
	store    enrichStore
	subjects *SubjectResolver
	now      func() time.Time
	mu       sync.Mutex
	last     map[string]EnrichStats // problem id → son tur
}

// EnrichStats — teşhis.
type EnrichStats struct {
	At     int64  `json:"at"`
	Rows   int    `json:"rows"`
	Traces int    `json:"traces"`
	Error  string `json:"error,omitempty"`
}

func NewEnricher(store enrichStore, subjects *SubjectResolver) *Enricher {
	return &Enricher{store: store, subjects: subjects, now: time.Now, last: map[string]EnrichStats{}}
}

// distributions — SAF: alan başına top-N değer + sayım (boş değer sayılmaz).
func distributions(rows []chstore.OracleErrorRow, topN int) map[string][]chstore.ValueCount {
	fields := []struct {
		name string
		get  func(r chstore.OracleErrorRow) string
	}{
		{"external_code", func(r chstore.OracleErrorRow) string { return r.ExternalCode }},
		{"error_type", func(r chstore.OracleErrorRow) string { return r.ErrorType }},
		{"instance", func(r chstore.OracleErrorRow) string { return r.InstanceID }},
		{"host", func(r chstore.OracleErrorRow) string { return r.HostName }},
		{"channel", func(r chstore.OracleErrorRow) string { return r.ChannelCode }},
		{"task", func(r chstore.OracleErrorRow) string { return r.TaskCode }},
	}
	out := map[string][]chstore.ValueCount{}
	for _, f := range fields {
		counts := map[string]int{}
		for _, r := range rows {
			if v := strings.TrimSpace(f.get(r)); v != "" {
				counts[v] += r.EffectiveWeight() // v0.10.904 — sayaçla aynı birim (hata, satır değil)
			}
		}
		if len(counts) == 0 {
			continue
		}
		list := make([]chstore.ValueCount, 0, len(counts))
		for v, n := range counts {
			list = append(list, chstore.ValueCount{Value: v, Count: n})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Count != list[j].Count {
				return list[i].Count > list[j].Count
			}
			return list[i].Value < list[j].Value
		})
		if len(list) > topN {
			list = list[:topN]
		}
		out[f.name] = list
	}
	return out
}

// weightedErrors — SAF (v0.10.904): satırların temsil ettiği hata sayısı.
// Tablo kipinde satır sayısına eşittir; özel SQL kipinde (Adet) sayaçla aynı.
func weightedErrors(rows []chstore.OracleErrorRow) int {
	n := 0
	for _, r := range rows {
		n += r.EffectiveWeight()
	}
	return n
}

// traceIDsNewestFirst — SAF: tekrarsız, en yeni satır önce, ≤max.
func traceIDsNewestFirst(rows []chstore.OracleErrorRow, max int) []string {
	type tt struct {
		id string
		at time.Time
	}
	latest := map[string]time.Time{}
	for _, r := range rows {
		if r.TraceID == "" {
			continue
		}
		if r.Time.After(latest[r.TraceID]) {
			latest[r.TraceID] = r.Time
		}
	}
	list := make([]tt, 0, len(latest))
	for id, at := range latest {
		list = append(list, tt{id, at})
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].at.Equal(list[j].at) {
			return list[i].at.After(list[j].at)
		}
		return list[i].id < list[j].id
	})
	out := make([]string, 0, min(max, len(list)))
	for i := 0; i < len(list) && i < max; i++ {
		out = append(out, list[i].id)
	}
	return out
}

// OnEvidence — ExternalTarget.OnEvidence gövdesi. Soft-fail; kanıt hatası
// Problem'i etkilemez.
func (e *Enricher) OnEvidence(ctx context.Context, ev anomaly.ExternalEvent) {
	if e == nil || e.store == nil || ev.Problem.ID == "" || len(ev.Values) < 3 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, enrichBudget)
	defer cancel()
	now := e.now()
	op, code, channel := ev.Values[0], ev.Values[1], ev.Values[2]
	st := EnrichStats{At: now.Unix()}
	rows, err := e.store.OracleErrorsByKey(ctx, ev.Target.SourceID, op, code, channel, ev.From, ev.To, enrichRowLimit)
	if err != nil {
		st.Error = "satırlar: " + err.Error()
		e.record(ev.Problem.ID, st)
		log.Printf("[oracle/enrich] %s: %s", ev.Problem.RuleID, st.Error)
		return
	}
	st.Rows = len(rows)
	ids := traceIDsNewestFirst(rows, enrichTraceLimit)
	st.Traces = len(ids)
	var summaries []chstore.TraceSpanSummary
	if len(ids) > 0 {
		summaries, err = e.store.SpanSummariesForTraces(ctx, ids, ev.From.Add(-enrichPad), ev.To.Add(enrichPad))
		if err != nil {
			summaries = nil
			st.Error = "span özeti: " + err.Error()
		}
	}
	labels := map[string]string{}
	for i, k := range ev.Target.GroupBy {
		if i < len(ev.Values) {
			labels[k] = ev.Values[i]
		}
	}
	ext := &chstore.ExternalMetricEvidence{
		Source: ev.Target.SourceName, Query: ev.Target.Query, Labels: labels,
		Current: ev.Current, Median: ev.Median, MAD: ev.MAD, Z: ev.Z,
		WindowFromNs: ev.From.UnixNano(), WindowToNs: ev.To.UnixNano(),
		Rows: len(rows), SpanSummary: summaries, UpdatedNs: now.UnixNano(),
		Distributions: distributions(rows, enrichTopN),
		Errors:        weightedErrors(rows),
	}
	if e.subjects != nil {
		if res, ok := e.subjects.LastResolution(ev.Target.SourceID, op); ok {
			ext.SubjectSource, ext.SubjectNote = res.Source, res.Note
			if ext.SubjectSource == "" {
				ext.SubjectSource = "unknown"
			}
		}
	}
	if len(rows) >= enrichRowLimit {
		ext.Notes = append(ext.Notes, "satır tavanı: ilk 500 satır")
	}
	if ext.SubjectNote != "" {
		ext.Notes = append(ext.Notes, "özne: "+ext.SubjectNote)
	}
	// Mevcut hipotez varsa aday/skor alanları KORUNUR (ilk yazan kazanır);
	// yalnız Deep tazelenir.
	h := chstore.RootCauseHypothesis{AnchorKind: "problem", AnchorID: ev.Problem.ID, Service: ev.Problem.Service, Candidates: []chstore.ScoredCause{}}
	if prev, err := e.store.GetHypothesis(ctx, "problem", ev.Problem.ID); err == nil && prev != nil {
		h = *prev
	}
	if h.Deep == nil {
		h.Deep = &chstore.DeepEvidence{}
	}
	h.Deep.External, h.Deep.TraceIDs = ext, ids
	h.ComputedAt = now.UnixNano()
	if h.Service == "" {
		h.Service = ev.Problem.Service
	}
	if err := e.store.UpsertHypothesis(ctx, h); err != nil {
		st.Error = "hipotez yazımı: " + err.Error()
		log.Printf("[oracle/enrich] %s: %s", ev.Problem.RuleID, st.Error)
	}
	e.record(ev.Problem.ID, st)
}

func (e *Enricher) record(id string, st EnrichStats) {
	e.mu.Lock()
	e.last[id] = st
	if len(e.last) > 5000 {
		for k := range e.last {
			delete(e.last, k)
			break
		}
	}
	e.mu.Unlock()
}

// Stats — problem id → son kanıt turu (teşhis).
func (e *Enricher) Stats(problemID string) (EnrichStats, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.last[problemID]
	return s, ok
}
