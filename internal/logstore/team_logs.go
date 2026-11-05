package logstore

// team_logs.go — v0.10.1473 (operatör 2026-10-23: "Elastic loglarında da
// takım bilgisi var; 'takımımın servisleri nasıl' derken logları da buna göre
// inceleyebilir, ve Elastic'e çok yük getirmeden son logları kontrol edip
// CoSRE dönebilir.").
//
// TeamRecentLogs — bir takımın son penceredeki HATA loglarının sınırlı özeti:
// en çok hata üreten ≤5 servis, örneklemden ≤5 mesaj deseni, en yeni 3 satır.
// Takım belge alanı operatör yapılandırmasıdır (ESFieldMap.Team, Settings →
// Elasticsearch); boşsa özellik KAPALI ve ES'e HİÇ istek gitmez.
//
// ES maliyet sözleşmesi (CLAUDE.md ES-maliyet disiplini, v0.8.3 olayı):
//   - TEK _search, size:0 + iki agg (terms by_service + top_hits örneklemi);
//     sayfa / PIT / scroll yok.
//   - pencere sorguda (range filtre bağlamında), varsayılan 15 dk, tavan 60 dk;
//   - takım yüklemi exact-term (keyword alt-alanı + exists-korumalı çıplak
//     alan, exactTermsBothShapes sözleşmesi) — metin üstünde wildcard YOK;
//   - seviye yüklemi şerit histogramının ERROR bandı (severityBandClause):
//     düşük kardinaliteli seviye alanlarında önek, iki yüzey aynı "hata"yı sayar;
//   - yumuşak `timeout` 3 s, shard başına `terminate_after`, track_total_hits
//     tavanlı (10k), request_cache açık (çağıran pencereyi dakikaya oturtur);
//   - desenler örneklemden (≤teamLogsSampleSize satır, NormalizeSignature —
//     /logs Desenler ile aynı imza) — tam tarama ASLA (INCIDENTS: Drain
//     templating sample-based).
// CH backend'i "unsupported" döner: CH logs tablosunda takım etiketi
// yapılandırılmış bir rol değil (çağıran tek satırla dürüstçe söyler).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

const (
	// TeamLogsDefaultWindow — pencere verilmezse son 15 dk.
	TeamLogsDefaultWindow = 15 * time.Minute
	// TeamLogsMaxWindow — pencere tavanı (daha geniş istek kırpılır).
	TeamLogsMaxWindow = 60 * time.Minute
	// TeamLogsMaxFields — virgülle yazılabilecek takım alanı tavanı.
	TeamLogsMaxFields = 3
	// TeamLogsMaxValues — sorguya giren takım değeri (yazım) tavanı.
	TeamLogsMaxValues = 10

	teamLogsTopServices    = 5
	teamLogsTopPatterns    = 5
	teamLogsSampleLines    = 3
	teamLogsSampleSize     = 50
	teamLogsTerminateAfter = 50000
	teamLogsTrackTotalCap  = 10000
	teamLogsSoftTimeout    = "3s"
	teamLogsLineMax        = 200
	teamLogsPatternMax     = 160
)

var (
	// ErrTeamLogsDisabled — takım alanı yapılandırılmamış (özellik kapalı).
	ErrTeamLogsDisabled = errors.New("team log field not configured")
	// ErrTeamLogsUnsupported — backend takım son-log kontrolünü desteklemiyor.
	ErrTeamLogsUnsupported = errors.New("team recent-log check is Elasticsearch-only")
)

// TeamLogsQuery — TeamRecentLogs girdisi. Values takım etiketinin olası
// yazımları (OR); ilk TeamLogsMaxValues tanesi kullanılır.
type TeamLogsQuery struct {
	Values   []string
	From, To time.Time
}

// TeamLogPattern — örneklemdeki bir mesaj deseni.
type TeamLogPattern struct {
	Template string `json:"template"`
	Count    int    `json:"count"`
	Service  string `json:"service,omitempty"` // desende en sık servis
	Sample   string `json:"sample"`            // kırpılmış tek satır
}

// TeamLogLine — en yeni örnek satırlardan biri (kırpılmış, tek satır).
type TeamLogLine struct {
	TimestampNs int64  `json:"ts"`
	Service     string `json:"service,omitempty"`
	Line        string `json:"line"`
	TraceID     string `json:"traceId,omitempty"`
}

// TeamLogsResult — TeamRecentLogs cevabı. Dürüstlük zarfı: Partial (yumuşak
// zaman aşımı / düşen shard), TerminatedEarly (shard başına tavan doldu —
// sayılar alt sınır), CountIsLowerBound (≥10k).
type TeamLogsResult struct {
	Fields            []string            `json:"fields"`
	Values            []string            `json:"values"`
	FromNs            int64               `json:"fromNs"`
	ToNs              int64               `json:"toNs"`
	ErrorCount        int64               `json:"errorCount"`
	CountIsLowerBound bool                `json:"countIsLowerBound,omitempty"`
	Services          []PatternServiceHit `json:"services,omitempty"`
	// ServicesFromSample — servis alanı terms agg'de boş kaldı; liste
	// örneklemden (mapHit'in servis yedek zinciri) sayıldı.
	ServicesFromSample bool             `json:"servicesFromSample,omitempty"`
	Patterns           []TeamLogPattern `json:"patterns,omitempty"`
	Sampled            int              `json:"sampled"`
	Lines              []TeamLogLine    `json:"lines,omitempty"`
	Partial            bool             `json:"partial,omitempty"`
	TerminatedEarly    bool             `json:"terminatedEarly,omitempty"`
}

// ParseTeamFields — SAF: ayar metni → takım alanları. Virgülle ayrılmış,
// kırpılmış, tekilleştirilmiş; `.keyword` soneki atılır (iki biçim de
// exactTermsBothShapes ile zaten sorulur). Boş metin → nil, nil (kapalı).
// Reddedilen: boşluk / wildcard / sorgu dili karakteri taşıyan ad, tavan
// üstü alan sayısı — yanlış yazım sessiz boş sonuç değil 400 olsun.
func ParseTeamFields(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		f := strings.TrimSuffix(strings.TrimSpace(part), ".keyword")
		if f == "" {
			continue
		}
		if len(f) > 256 {
			return nil, fmt.Errorf("team field %q is too long", f[:32]+"…")
		}
		for _, r := range f {
			if unicode.IsSpace(r) || strings.ContainsRune(`*?"':\()[]{}<>!^~/|&=+`, r) {
				return nil, fmt.Errorf("team field %q: a document field path only (no spaces, wildcards or query syntax)", f)
			}
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) > TeamLogsMaxFields {
		return nil, fmt.Errorf("at most %d team fields (comma-separated), got %d", TeamLogsMaxFields, len(out))
	}
	return out, nil
}

// TeamLogsWindow — SAF: pencere kıstırması. to sıfır → now; from sıfır ya da
// to'dan sonra → to − varsayılan; genişlik tavanı aşarsa from = to − tavan.
func TeamLogsWindow(from, to, now time.Time) (time.Time, time.Time) {
	if to.IsZero() {
		to = now
	}
	if from.IsZero() || !from.Before(to) {
		from = to.Add(-TeamLogsDefaultWindow)
	}
	if to.Sub(from) > TeamLogsMaxWindow {
		from = to.Add(-TeamLogsMaxWindow)
	}
	return from, to
}

// teamLogValues — SAF: değerler kırpılır, tekilleştirilir, tavanlanır (sıra
// korunur: çağıranın tercih ettiği yazım önce).
func teamLogValues(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= TeamLogsMaxValues {
			break
		}
	}
	return out
}

// teamValueClause — SAF: alan(lar) × değer(ler) exact eşleşmesi. Alan başına
// iki biçim (exactTermsBothShapes'in çoğul ikizi): `.keyword` alt-alanında
// `terms` + exists-korumalı çıplak alanda `terms` (ECS/yönetilen eşlemede
// alan doğrudan keyword). Hepsi tek bool.should altında; yanlış dal eşleşmez.
func teamValueClause(fields, values []string) map[string]any {
	should := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		should = append(should,
			map[string]any{"terms": map[string]any{f + ".keyword": values}},
			map[string]any{"bool": map[string]any{
				"must":     []any{map[string]any{"terms": map[string]any{f: values}}},
				"must_not": []any{map[string]any{"exists": map[string]any{"field": f + ".keyword"}}},
			}},
		)
	}
	return map[string]any{"bool": map[string]any{"should": should, "minimum_should_match": 1}}
}

// errorBand — şerit histogramının ERROR bandı (severityBands[0] adla aranır:
// sıra değişse de yanlış bant seçilmesin).
func errorBand() severityBandDef {
	for _, b := range severityBands {
		if b.Name == "ERROR" {
			return b
		}
	}
	return severityBands[0]
}

// teamLogsSource — SAF: top_hits örnekleminin dar _source'u: gövde, zaman,
// seviye (leanSourceFields) + servis yedek zinciri + trace alanları. Tam
// _source değil — 50 doküman × tüm alanlar gereksiz yük.
func teamLogsSource(fields ESFieldMap) []string {
	cands := []string{fields.Body, fields.Timestamp, fields.SeverityTx, fields.SeverityNo,
		fields.Service, "service.name", "kubernetes.container_name", "kubernetes.container.name",
		"service_name", "serviceName", "ServiceName",
		fields.TraceID, "trace.id", "trace_id", "traceId", "TraceId",
		"message", "Body", "body", "log.message", "log",
		"level", "log.level", "severity_text", "severity"}
	seen := map[string]bool{}
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// buildTeamLogsBody — SAF: TEK _search gövdesi (maliyet bekçileri pinli,
// team_logs_test.go). svcAggField boş → by_service agg yok (servisler
// örneklemden sayılır).
func buildTeamLogsBody(fields ESFieldMap, teamFields, values []string, svcAggField string, from, to time.Time) map[string]any {
	filter := []any{
		map[string]any{"range": map[string]any{fields.Timestamp: map[string]any{
			"gte": from.UTC().Format(time.RFC3339Nano),
			"lte": to.UTC().Format(time.RFC3339Nano),
		}}},
		teamValueClause(teamFields, values),
		severityBandClause(errorBand(), severityCandidateKeywordFields(fields.SeverityTx), fields.SeverityNo, fields.Body),
	}
	aggs := map[string]any{
		"newest": map[string]any{
			"top_hits": map[string]any{
				"size":    teamLogsSampleSize,
				"_source": teamLogsSource(fields),
				"sort": []any{map[string]any{fields.Timestamp: map[string]any{
					"order": "desc", "unmapped_type": "date",
				}}},
			},
		},
	}
	if svcAggField != "" {
		aggs["by_service"] = map[string]any{
			"terms": map[string]any{
				"field":      svcAggField,
				"size":       teamLogsTopServices,
				"shard_size": teamLogsTopServices * 10,
			},
		}
	}
	return map[string]any{
		"size":             0,
		"track_total_hits": teamLogsTrackTotalCap,
		"timeout":          teamLogsSoftTimeout,
		"terminate_after":  teamLogsTerminateAfter,
		"query":            map[string]any{"bool": map[string]any{"filter": filter}},
		"aggs":             aggs,
	}
}

// TrimLogLine — SAF: çok satırlı / uzun gövdeyi tek satıra indirir (boşluk
// sıkıştırma) ve max rune'da "…" ile keser. Redaksiyon değil, kısaltma.
func TrimLogLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// teamLogsFromRecords — SAF: örneklem (en yeni önce) → desenler (imza,
// sayı azalan, eşitlikte en yeni önce), ilk teamLogsSampleLines satır, ve
// servis sayımı (terms agg boş kalırsa yedek).
func teamLogsFromRecords(recs []*LogRecord) (pats []TeamLogPattern, lines []TeamLogLine, svcs []PatternServiceHit) {
	type acc struct {
		p     TeamLogPattern
		last  int64
		order int
		svcs  map[string]int
	}
	groups := map[string]*acc{}
	svcCount := map[string]uint64{}
	for i, rec := range recs {
		if rec == nil {
			continue
		}
		if rec.ServiceName != "" {
			svcCount[rec.ServiceName]++
		}
		if len(lines) < teamLogsSampleLines {
			lines = append(lines, TeamLogLine{
				TimestampNs: rec.Timestamp, Service: rec.ServiceName,
				Line: TrimLogLine(rec.Body, teamLogsLineMax), TraceID: rec.TraceID,
			})
		}
		sig := NormalizeSignature(rec.Body)
		if sig == "" {
			continue
		}
		a, ok := groups[sig]
		if !ok {
			a = &acc{p: TeamLogPattern{Template: TrimLogLine(sig, teamLogsPatternMax), Sample: TrimLogLine(rec.Body, teamLogsLineMax)},
				last: rec.Timestamp, order: i, svcs: map[string]int{}}
			groups[sig] = a
		}
		a.p.Count++
		if rec.ServiceName != "" {
			a.svcs[rec.ServiceName]++
		}
	}
	all := make([]*acc, 0, len(groups))
	for _, a := range groups {
		best, bestN := "", 0
		for s, n := range a.svcs {
			if n > bestN || (n == bestN && s < best) {
				best, bestN = s, n
			}
		}
		a.p.Service = best
		all = append(all, a)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].p.Count != all[j].p.Count {
			return all[i].p.Count > all[j].p.Count
		}
		return all[i].order < all[j].order // örneklem en yeni önce → ilk görülen en yeni
	})
	for i := 0; i < len(all) && i < teamLogsTopPatterns; i++ {
		pats = append(pats, all[i].p)
	}
	for s, n := range svcCount {
		svcs = append(svcs, PatternServiceHit{Service: s, Count: n})
	}
	sort.Slice(svcs, func(i, j int) bool {
		if svcs[i].Count != svcs[j].Count {
			return svcs[i].Count > svcs[j].Count
		}
		return svcs[i].Service < svcs[j].Service
	})
	if len(svcs) > teamLogsTopServices {
		svcs = svcs[:teamLogsTopServices]
	}
	return pats, lines, svcs
}

// TeamRecentLogs — ES: tek sınırlı _search (dosya başlığı). Takım alanı
// yapılandırılmamışsa ErrTeamLogsDisabled, ağ çağrısı YOK; değer yoksa
// hata (boş IN listesi = tüm filo — sessiz kapsam patlaması).
func (s *ESStore) TeamRecentLogs(ctx context.Context, q TeamLogsQuery) (*TeamLogsResult, error) {
	teamFields, err := ParseTeamFields(s.fields.Team)
	if err != nil {
		return nil, err
	}
	if len(teamFields) == 0 {
		return nil, ErrTeamLogsDisabled
	}
	values := teamLogValues(q.Values)
	if len(values) == 0 {
		return nil, errors.New("team recent logs: no team value")
	}
	from, to := TeamLogsWindow(q.From, q.To, time.Now())
	idx := s.queryIndices(ctx, Filter{From: from, To: to})
	body, err := json.Marshal(buildTeamLogsBody(s.fields, teamFields, values,
		s.termsAggField(ctx, s.fields.Service), from, to))
	if err != nil {
		return nil, err
	}
	tru := true
	req := esapi.SearchRequest{
		Index:             idx,
		Body:              bytes.NewReader(body),
		AllowNoIndices:    &tru,
		IgnoreUnavailable: &tru,
		RequestCache:      &tru,
	}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return nil, s.recordQueryError("team recent logs", idx, body, 0, fmt.Errorf("ES team recent logs: %w", err))
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, s.recordQueryError("team recent logs", idx, body, res.StatusCode,
			parseESError("team recent logs", res, s.cfg.Index))
	}
	var decoded struct {
		esSearchEnvelope
		TerminatedEarly bool `json:"terminated_early"`
		Hits            struct {
			Total esTotal `json:"total"`
		} `json:"hits"`
		Aggregations struct {
			ByService struct {
				Buckets []struct {
					Key      any   `json:"key"`
					DocCount int64 `json:"doc_count"`
				} `json:"buckets"`
			} `json:"by_service"`
			Newest struct {
				Hits struct {
					Hits []struct {
						ID     string         `json:"_id"`
						Source map[string]any `json:"_source"`
					} `json:"hits"`
				} `json:"hits"`
			} `json:"newest"`
		} `json:"aggregations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode ES team recent logs: %w", err)
	}
	if d := decoded.describe(); d != "" {
		log.Printf("[logstore-es] PARTIAL team recent logs (%s) — counts are a subset", d)
	}
	out := &TeamLogsResult{
		Fields: teamFields, Values: values,
		FromNs: from.UnixNano(), ToNs: to.UnixNano(),
		ErrorCount:        int64(decoded.Hits.Total.Value),
		CountIsLowerBound: decoded.Hits.Total.isLowerBound() || decoded.TerminatedEarly,
		Partial:           decoded.partial(),
		TerminatedEarly:   decoded.TerminatedEarly,
	}
	recs := make([]*LogRecord, 0, len(decoded.Aggregations.Newest.Hits.Hits))
	for _, h := range decoded.Aggregations.Newest.Hits.Hits {
		if h.Source == nil {
			continue
		}
		recs = append(recs, s.mapHit(h.ID, h.Source, nil, "", ""))
	}
	out.Sampled = len(recs)
	var sampleSvcs []PatternServiceHit
	out.Patterns, out.Lines, sampleSvcs = teamLogsFromRecords(recs)
	for _, b := range decoded.Aggregations.ByService.Buckets {
		k := strings.TrimSpace(fmt.Sprintf("%v", b.Key))
		if k == "" || b.DocCount <= 0 {
			continue
		}
		out.Services = append(out.Services, PatternServiceHit{Service: k, Count: uint64(b.DocCount)})
	}
	if len(out.Services) == 0 && len(sampleSvcs) > 0 {
		out.Services, out.ServicesFromSample = sampleSvcs, true
	}
	return out, nil
}

// TeamRecentLogs — CH: desteklenmiyor (CH logs tablosunda takım etiketi
// yapılandırılmış bir rol değil). Sorgu YOK; çağıran tek satırla söyler.
func (s *CHStore) TeamRecentLogs(ctx context.Context, q TeamLogsQuery) (*TeamLogsResult, error) {
	_, _ = ctx, q
	return nil, ErrTeamLogsUnsupported
}

// TeamRecentLogs — canlı backend'e iletim.
func (s *Switchable) TeamRecentLogs(ctx context.Context, q TeamLogsQuery) (*TeamLogsResult, error) {
	return s.Current().TeamRecentLogs(ctx, q)
}

// ── /logs `team=` süzgeci (v0.10.1477) ──────────────────────────────────────
//
// TeamRecentLogs'un yan tümcesi (teamValueClause) /logs listesine, histogramına,
// alan istatistiklerine ve desen örneklemesine de uygulanır: Filter.TeamValues
// (API katmanının çözdüğü ham yazımlar) × yapılandırılmış takım alanları.
// İki yüzey aynı "takımın logları"nı görür — CoSRE kanıt çipi ile /logs
// arasında ayrışma yok.

// TeamFilterValues — SAF: Filter'ın takım değerleri (kırpılmış, tekil,
// ≤TeamLogsMaxValues). TeamValues boş ama Team doluysa [Team] — boş IN listesi
// "tüm filo" demek olurdu (sessiz kapsam patlaması). Takım istenmemişse nil.
func TeamFilterValues(f Filter) []string {
	vals := teamLogValues(f.TeamValues)
	if len(vals) == 0 {
		if t := strings.TrimSpace(f.Team); t != "" {
			vals = []string{t}
		}
	}
	return vals
}

// esTeamFilter — SAF: ES filtre yan tümcesi + uygulandı mı. Takım istenmemişse
// (nil, true). Alan yapılandırılmamış / geçersizse (nil, false): çağıran
// yan tümce EKLEMEZ, Page.UnappliedFilters "team" der.
func esTeamFilter(f Filter, teamField string) (map[string]any, bool) {
	vals := TeamFilterValues(f)
	if len(vals) == 0 {
		return nil, true
	}
	fields, err := ParseTeamFields(teamField)
	if err != nil || len(fields) == 0 {
		return nil, false
	}
	return teamValueClause(fields, vals), true
}
