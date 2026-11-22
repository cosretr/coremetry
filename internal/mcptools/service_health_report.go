package mcptools

// service_health_report.go — v0.10.2454 (operatör 2026-11-22): servis sağlığı
// sorusu ("X servisi nasıl", "sağlığı", "ne durumda") cevabı ŞU bölümleri, bu
// sırayla ve her biri KAYNAĞIYLA taşır:
//
//  1. metrics          — örneklenmemiş RED (VictoriaMetrics, Overview metrik
//                        kipinin okuması; tesisat rotaları dışlı) + önceki
//                        eş-uzunluk pencereye fark.
//  2. trace_errors     — saklanan span'ler içindeki hata payı (ÖRNEKLENMİŞ:
//                        upstream tüm hataları + %5'i saklar → gerçek oran
//                        DEĞİL) + hatalı trace sayısı + önceki pencereye eğilim.
//  3. error_operations — hata sayısına göre ilk 5 operasyon: hata %, baskın
//                        sınıf (error.type / HTTP / gRPC / DB), örnek trace.
//  4. technical_errors — error.type / HTTP durum / gRPC durum / DB dağılımı +
//                        hatalı aşağı hedefler (VM istemci histogramı önce,
//                        topology_edges_5m ikinci).
//  5. exceptions       — exception_groups ilk 5 (tür + mesaj, sayı, ilk/son
//                        görülme, yeni / tekrarlayan).
//  6. messaging        — servisin kendi topic satırları (messaging_caller_summary_5m)
//                        + Kafka istemci metriği ailesi + consumer lag (VM);
//                        lag serisi yoksa "ölçülmüyor".
//  +  context          — açık problem, anomali sayısı, son deploy.
//
// Okunamayan bölüm `unread`, kaynağı olmayan `missing`, okunup boş çıkan
// `none` — üçü ayrı; hiçbir değer tahmin edilmez. Okumalar PARALEL, her biri
// kendi bütçesinde (hrReadBudget); rapor 60 sn önbellekte (anahtar: servis,
// ortam, pencere başı ve sonu). Montaj SAF (assembleHealthReport).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

// Bölüm durumları.
const (
	HRStatusOK      = "ok"
	HRStatusNone    = "none"    // okundu, kayıt yok
	HRStatusMissing = "missing" // kaynak yok / yapılandırılmamış
	HRStatusUnread  = "unread"  // okuma başarısız (okunamadı ≠ yok)
)

// HRSectionOrder — cevabın bölüm sırası (operatör kuralı; JSON alan sırasıyla aynı).
var HRSectionOrder = []string{"metrics", "trace_errors", "error_operations", "technical_errors", "exceptions", "messaging", "context"}

const (
	hrReadBudget   = 8 * time.Second
	hrCacheTTL     = 60 * time.Second
	hrMetricMdp    = 30
	hrOpsTop       = 5
	hrTechTop      = 5
	hrExcTop       = 5
	hrTopicsMax    = 6
	hrDownMax      = 5
	hrAnomalyLimit = 50
	hrDeployMin    = 6 * time.Hour
	hrFlowRows     = 40
	hrEdgesMax     = 15
)

// KafkaFamilyInfo — servisin Kafka istemci metriği ailesi (api
// /api/services/{name}/kafka-family çözücüsü). State: metric | spans | unknown.
type KafkaFamilyInfo struct {
	State   string `json:"state"`
	Family  string `json:"family,omitempty"`
	Produce bool   `json:"produce,omitempty"`
	Consume bool   `json:"consume,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// KafkaFamilyFunc — api dikişi (nil → aile bilinmiyor, "missing").
type KafkaFamilyFunc func(ctx context.Context, service string, at time.Time) (KafkaFamilyInfo, error)

// ── rapor şekli ────────────────────────────────────────────────────────

type HRMetricVals struct {
	RPS      *float64 `json:"rps,omitempty"`
	ErrorPct *float64 `json:"error_pct,omitempty"`
	P95Ms    *float64 `json:"p95_ms,omitempty"`
	P99Ms    *float64 `json:"p99_ms,omitempty"`
}

type HRMetricDelta struct {
	RPSPct  *float64 `json:"rps_pct,omitempty"`
	ErrorPP *float64 `json:"error_pp,omitempty"`
	P95Pct  *float64 `json:"p95_pct,omitempty"`
	P99Pct  *float64 `json:"p99_pct,omitempty"`
}

type HRMetrics struct {
	Status        string         `json:"status"`
	Source        string         `json:"source"`
	InfraExcluded bool           `json:"infra_routes_excluded,omitempty"`
	Current       *HRMetricVals  `json:"current,omitempty"`
	Prior         *HRMetricVals  `json:"prior,omitempty"`
	Delta         *HRMetricDelta `json:"delta,omitempty"`
	Note          string         `json:"note,omitempty"`
}

type HRTraceErrors struct {
	Status             string   `json:"status"`
	Source             string   `json:"source"`
	StoredSpans        uint64   `json:"stored_spans"`
	ErrorSpans         uint64   `json:"error_spans"`
	ErrorSharePct      float64  `json:"error_share_pct"`
	PriorErrorSpans    *uint64  `json:"prior_error_spans,omitempty"`
	PriorErrorSharePct *float64 `json:"prior_error_share_pct,omitempty"`
	Trend              string   `json:"trend,omitempty"`
	ErrorTraces        *uint64  `json:"error_traces,omitempty"`
	ErrorTracesWindowS int      `json:"error_traces_window_s,omitempty"`
	Note               string   `json:"note"`
}

type HROp struct {
	Operation   string   `json:"operation"`
	Errors      uint64   `json:"errors"`
	ErrorPct    *float64 `json:"error_pct,omitempty"`
	Dominant    string   `json:"dominant"`
	DominantPct float64  `json:"dominant_pct"`
	SampleTrace string   `json:"sample_trace_id,omitempty"`
}

type HROps struct {
	Status string `json:"status"`
	Source string `json:"source"`
	Rows   []HROp `json:"rows,omitempty"`
	Note   string `json:"note,omitempty"`
}

type HRCount struct {
	Value    string  `json:"value"`
	Errors   uint64  `json:"errors"`
	SharePct float64 `json:"share_pct"`
}

type HRDown struct {
	Target   string  `json:"target"`
	Kind     string  `json:"kind,omitempty"`
	Source   string  `json:"source"` // vm | topology
	ErrorPct float64 `json:"error_pct"`
	Errors   uint64  `json:"errors,omitempty"`
	Dominant string  `json:"dominant,omitempty"`
}

type HRTech struct {
	Status           string    `json:"status"`
	Source           string    `json:"source"`
	ErrorType        []HRCount `json:"error_type,omitempty"`
	HTTPStatus       []HRCount `json:"http_status,omitempty"`
	GRPCStatus       []HRCount `json:"grpc_status,omitempty"`
	DB               []HRCount `json:"db,omitempty"`
	Downstream       []HRDown  `json:"downstream_errors,omitempty"`
	DownstreamStatus string    `json:"downstream_status"`
	Note             string    `json:"note,omitempty"`
}

type HRExc struct {
	Type        string `json:"type"`
	Message     string `json:"message,omitempty"`
	Occurrences uint64 `json:"occurrences"`
	FirstSeen   string `json:"first_seen"`
	LastSeen    string `json:"last_seen"`
	Kind        string `json:"kind"` // new | recurring
	State       string `json:"state,omitempty"`
}

type HRExceptions struct {
	Status  string  `json:"status"`
	Source  string  `json:"source"`
	Groups  []HRExc `json:"groups,omitempty"`
	HasMore bool    `json:"has_more,omitempty"`
	Note    string  `json:"note,omitempty"`
}

type HRTopic struct {
	System      string  `json:"system"`
	Destination string  `json:"destination"`
	Role        string  `json:"role"`
	Spans       uint64  `json:"spans"`
	ErrorPct    float64 `json:"error_pct"`
	P95Ms       float64 `json:"p95_ms"`
}

type HRMessaging struct {
	Status    string           `json:"status"`
	Source    string           `json:"source"`
	Family    *KafkaFamilyInfo `json:"kafka_metric_family,omitempty"`
	Topics    []HRTopic        `json:"topics,omitempty"`
	Lag       []mfLag          `json:"lag,omitempty"`
	LagStatus string           `json:"lag_status"`
	Note      string           `json:"note,omitempty"`
}

type HRDeploy struct {
	Version string `json:"version"`
	At      string `json:"at"`
	Source  string `json:"source,omitempty"`
}

type HRContext struct {
	OpenProblems     *uint64   `json:"open_problems,omitempty"`
	Anomalies        *int      `json:"anomalies,omitempty"`
	AnomaliesHasMore bool      `json:"anomalies_has_more,omitempty"`
	LastDeploy       *HRDeploy `json:"last_deploy,omitempty"`
	DeployStatus     string    `json:"deploy_status"`
	Unread           []string  `json:"unread,omitempty"`
	Missing          []string  `json:"missing,omitempty"`
}

// ServiceHealthReport — get_service_health `report` alanı. Alan sırası = HRSectionOrder.
type ServiceHealthReport struct {
	Order       []string      `json:"order"`
	WindowS     int           `json:"window_s"`
	To          string        `json:"to"`
	Env         string        `json:"env,omitempty"`
	Metrics     HRMetrics     `json:"metrics"`
	TraceErrors HRTraceErrors `json:"trace_errors"`
	ErrorOps    HROps         `json:"error_operations"`
	Technical   HRTech        `json:"technical_errors"`
	Exceptions  HRExceptions  `json:"exceptions"`
	Messaging   HRMessaging   `json:"messaging"`
	Context     HRContext     `json:"context"`
}

// HasUnread — herhangi bir okuma başarısız mı (önbelleğe girmez).
func (r ServiceHealthReport) HasUnread() bool {
	return r.Metrics.Status == HRStatusUnread || r.TraceErrors.Status == HRStatusUnread || r.ErrorOps.Status == HRStatusUnread ||
		r.Technical.Status == HRStatusUnread || r.Exceptions.Status == HRStatusUnread || r.Messaging.Status == HRStatusUnread ||
		len(r.Context.Unread) > 0
}

// ── okumalar ───────────────────────────────────────────────────────────

// hrRead — tek okumanın sonucu. Missing: okuyucu yok (nil).
type hrRead[T any] struct {
	V       T
	Err     error
	Missing bool
}

func (r hrRead[T]) failed() bool { return r.Missing || r.Err != nil }

// hrSources — okuma dikişleri (servis / ortam kapanışta; testte sahte). nil = missing.
type hrSources struct {
	metricRED   func(ctx context.Context, from, to time.Time) (MetricREDResult, error)
	spanRED     func(ctx context.Context, from, to time.Time) (chstore.ServiceSummary, error)
	errProfile  func(ctx context.Context, from, to time.Time) (chstore.ErrorProfile, error)
	opTotals    func(ctx context.Context, names []string, from, to time.Time) (map[string]chstore.OpTotal, error)
	exceptions  func(ctx context.Context, from, to time.Time) ([]chstore.ExceptionGroup, error)
	downstream  func(ctx context.Context, from, to time.Time) (runtimesig.DownstreamReport, error)
	edges       func(ctx context.Context, from, to time.Time) ([]chstore.DownstreamEdge, error)
	msgFlow     func(ctx context.Context, from, to time.Time) ([]chstore.MessagingFlowRow, error)
	kafkaFamily func(ctx context.Context, at time.Time) (KafkaFamilyInfo, error)
	lag         func(ctx context.Context, from, to time.Time) ([]chstore.SpanMetricSeries, error)
	problems    func(ctx context.Context) (uint64, error)
	anomalies   func(ctx context.Context, from time.Time) ([]chstore.AnomalyEvent, error)
	deploys     func(ctx context.Context, from, to time.Time) ([]chstore.Deploy, bool, error)
}

// hrRaw — paralel okumaların ham sonuçları (assembleHealthReport girdisi).
type hrRaw struct {
	Service, Env  string
	From, To      time.Time
	MetricCur     hrRead[MetricREDResult]
	MetricPrior   hrRead[MetricREDResult]
	SpanCur       hrRead[chstore.ServiceSummary]
	SpanPrior     hrRead[chstore.ServiceSummary]
	Profile       hrRead[chstore.ErrorProfile]
	OpTotals      hrRead[map[string]chstore.OpTotal]
	Exceptions    hrRead[[]chstore.ExceptionGroup]
	Downstream    hrRead[runtimesig.DownstreamReport]
	Edges         hrRead[[]chstore.DownstreamEdge]
	Flow          hrRead[[]chstore.MessagingFlowRow]
	KafkaFamily   hrRead[KafkaFamilyInfo]
	Lag           hrRead[[]chstore.SpanMetricSeries]
	Problems      hrRead[uint64]
	Anomalies     hrRead[[]chstore.AnomalyEvent]
	Deploys       hrRead[[]chstore.Deploy]
	DeployCovered bool
}

func hrGo[T any](ctx context.Context, wg *sync.WaitGroup, dst *hrRead[T], f func(context.Context) (T, error)) {
	if f == nil {
		dst.Missing = true
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, cancel := context.WithTimeout(ctx, hrReadBudget)
		defer cancel()
		dst.V, dst.Err = f(c)
	}()
}

// collectHealthRaw — okumaları paralel koşturur (goroutine emit etmez).
func collectHealthRaw(ctx context.Context, src hrSources, service, env string, from, to time.Time) hrRaw {
	raw := hrRaw{Service: service, Env: env, From: from, To: to}
	w := to.Sub(from)
	pFrom, pTo := from.Add(-w), from
	var wg sync.WaitGroup
	if src.metricRED != nil {
		hrGo(ctx, &wg, &raw.MetricCur, func(c context.Context) (MetricREDResult, error) { return src.metricRED(c, from, to) })
		hrGo(ctx, &wg, &raw.MetricPrior, func(c context.Context) (MetricREDResult, error) { return src.metricRED(c, pFrom, pTo) })
	} else {
		raw.MetricCur.Missing, raw.MetricPrior.Missing = true, true
	}
	if src.spanRED != nil {
		hrGo(ctx, &wg, &raw.SpanCur, func(c context.Context) (chstore.ServiceSummary, error) { return src.spanRED(c, from, to) })
		hrGo(ctx, &wg, &raw.SpanPrior, func(c context.Context) (chstore.ServiceSummary, error) { return src.spanRED(c, pFrom, pTo) })
	} else {
		raw.SpanCur.Missing, raw.SpanPrior.Missing = true, true
	}
	// Profil + operasyon toplamları ardışık (toplamlar profilin ilk adlarını ister).
	if src.errProfile != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, hrReadBudget)
			raw.Profile.V, raw.Profile.Err = src.errProfile(c, from, to)
			cancel()
			if raw.Profile.Err != nil {
				return
			}
			if src.opTotals == nil {
				raw.OpTotals.Missing = true
				return
			}
			names := hrTopOpNames(raw.Profile.V.Rows, hrOpsTop)
			c, cancel = context.WithTimeout(ctx, hrReadBudget)
			defer cancel()
			// Profil penceresi (≤1 sa kırpılmış olabilir) ile AYNI pencere.
			raw.OpTotals.V, raw.OpTotals.Err = src.opTotals(c, names, raw.Profile.V.From, raw.Profile.V.To)
		}()
	} else {
		raw.Profile.Missing, raw.OpTotals.Missing = true, true
	}
	hrGo(ctx, &wg, &raw.Exceptions, bindFT(src.exceptions, from, to))
	hrGo(ctx, &wg, &raw.Downstream, bindFT(src.downstream, from, to))
	hrGo(ctx, &wg, &raw.Edges, bindFT(src.edges, from, to))
	hrGo(ctx, &wg, &raw.Flow, bindFT(src.msgFlow, from, to))
	if src.kafkaFamily != nil {
		hrGo(ctx, &wg, &raw.KafkaFamily, func(c context.Context) (KafkaFamilyInfo, error) { return src.kafkaFamily(c, to) })
	} else {
		raw.KafkaFamily.Missing = true
	}
	lagFrom := from
	if to.Sub(lagFrom) > mfLagWindow {
		lagFrom = to.Add(-mfLagWindow)
	}
	hrGo(ctx, &wg, &raw.Lag, bindFT(src.lag, lagFrom, to))
	hrGo(ctx, &wg, &raw.Problems, src.problems)
	if src.anomalies != nil {
		hrGo(ctx, &wg, &raw.Anomalies, func(c context.Context) ([]chstore.AnomalyEvent, error) { return src.anomalies(c, from) })
	} else {
		raw.Anomalies.Missing = true
	}
	if src.deploys != nil {
		dFrom := from
		if to.Sub(dFrom) < hrDeployMin {
			dFrom = to.Add(-hrDeployMin)
		}
		hrGo(ctx, &wg, &raw.Deploys, func(c context.Context) ([]chstore.Deploy, error) {
			ds, cov, err := src.deploys(c, dFrom, to)
			raw.DeployCovered = cov
			return ds, err
		})
	} else {
		raw.Deploys.Missing = true
	}
	wg.Wait()
	return raw
}

// bindFT — (ctx, from, to) okuyucusunu (ctx) biçimine bağlar; nil → nil.
func bindFT[T any](f func(context.Context, time.Time, time.Time) (T, error), from, to time.Time) func(context.Context) (T, error) {
	if f == nil {
		return nil
	}
	return func(c context.Context) (T, error) { return f(c, from, to) }
}

// ── SAF montaj ─────────────────────────────────────────────────────────

func f64p(v float64) *float64 { x := r2(v); return &x }

// hrSeriesMean — SAF: serilerin sonlu noktalarının ortalaması; sum=true →
// seri ortalamalarının TOPLAMI (rate), aksi ortalaması. ok=false: nokta yok.
func hrSeriesMean(ser []chstore.SpanMetricSeries, sum bool) (float64, bool) {
	var acc float64
	n := 0
	for _, s := range ser {
		var t float64
		k := 0
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			t += p.Value
			k++
		}
		if k == 0 {
			continue
		}
		acc += t / float64(k)
		n++
	}
	if n == 0 {
		return 0, false
	}
	if !sum {
		acc /= float64(n)
	}
	return acc, true
}

// hrMetricVals — SAF: VM sonucu → pencere ortalamaları (bilinmeyen alan nil).
func hrMetricVals(res MetricREDResult) (*HRMetricVals, bool) {
	rps, ok := hrSeriesMean(res.Series["rate"], true)
	if !ok {
		return nil, false
	}
	v := &HRMetricVals{RPS: f64p(rps)}
	if !res.ErrorsUnknown {
		if e, ok := hrSeriesMean(res.Series["error_rate"], false); ok {
			v.ErrorPct = f64p(e)
		}
	}
	if res.LatencyUnitKnown {
		if p, ok := hrSeriesMean(res.Series["p95"], false); ok {
			v.P95Ms = f64p(p)
		}
		if p, ok := hrSeriesMean(res.Series["p99"], false); ok {
			v.P99Ms = f64p(p)
		}
	}
	return v, true
}

func hrRel(cur, base *float64) *float64 {
	if cur == nil || base == nil {
		return nil
	}
	return relPct(*cur, *base)
}

func assembleMetrics(raw hrRaw) HRMetrics {
	m := HRMetrics{Source: "VictoriaMetrics (örneklenmemiş metrik RED)"}
	cur := raw.MetricCur
	switch {
	case cur.Missing:
		m.Status, m.Note = HRStatusMissing, "metrik okuyucusu yapılandırılmamış — örneklenmemiş RED yok"
		return m
	case cur.Err != nil:
		m.Status, m.Note = HRStatusUnread, "metrik okuması başarısız (okunamadı ≠ trafik yok): "+cur.Err.Error()
		return m
	}
	res := cur.V
	if res.Source != "" && res.Source != "vm" {
		m.Source = "metrik kaynağı " + res.Source + " (örneklenmemiş)"
	}
	if res.Metric != "" {
		m.Source += " · " + res.Metric
	}
	m.InfraExcluded = res.InfraExcluded
	vals, ok := hrMetricVals(res)
	if !res.Found || !ok {
		m.Status = HRStatusMissing
		m.Note = "servis için HTTP/gRPC RED metriği yok"
		if res.Found {
			m.Note = res.Metric + " bu pencerede seri döndürmedi"
		}
		if len(res.Tried) > 0 {
			m.Note += " (denenen: " + strings.Join(hrCap(res.Tried, 3), ", ") + ")"
		}
		return m
	}
	m.Status, m.Current = HRStatusOK, vals
	var notes []string
	if res.ErrorsUnknown {
		notes = append(notes, "durum etiketi yok → hata oranı bilinmiyor (0 değil)")
	}
	if !res.LatencyUnitKnown {
		notes = append(notes, "süre birimi bilinmiyor → p95/p99 verilmedi")
	}
	if raw.Env != "" && !res.EnvApplied {
		notes = append(notes, "ortam süzgeci metrikte uygulanamadı — tüm ortamlar")
	}
	notes = append(notes, "değerler pencere ortalaması")
	if pr := raw.MetricPrior; !pr.failed() {
		if pv, ok := hrMetricVals(pr.V); ok && pr.V.Found {
			m.Prior = pv
			d := &HRMetricDelta{RPSPct: hrRel(vals.RPS, pv.RPS), P95Pct: hrRel(vals.P95Ms, pv.P95Ms), P99Pct: hrRel(vals.P99Ms, pv.P99Ms)}
			if vals.ErrorPct != nil && pv.ErrorPct != nil {
				d.ErrorPP = f64p(*vals.ErrorPct - *pv.ErrorPct)
			}
			m.Delta = d
		}
	} else if pr.Err != nil {
		notes = append(notes, "önceki pencere okunamadı — fark yok")
	}
	if m.Prior == nil && raw.MetricPrior.Err == nil {
		notes = append(notes, "önceki pencerede metrik yok — fark yok")
	}
	m.Note = strings.Join(notes, "; ")
	return m
}

// hrTrend — SAF: hata payı eğilimi (≥1 puan VE ≥%25 göreli değişim).
func hrTrend(cur, prior float64, priorSpans uint64) string {
	if priorSpans == 0 {
		return "önceki pencerede veri yok"
	}
	d := cur - prior
	switch {
	case d >= 1 && cur >= prior*1.25:
		return "artıyor"
	case d <= -1 && cur <= prior*0.8:
		return "azalıyor"
	}
	return "yatay"
}

func assembleTraceErrors(raw hrRaw) HRTraceErrors {
	t := HRTraceErrors{Source: "service_summary_5m (örneklenmiş span)",
		Note: "örneklenmiş: upstream tüm hataları + %5'ini saklar — bu, saklanan span'ler içindeki hata payıdır, gerçek hata oranı DEĞİL (gerçek oran: metrics)"}
	if raw.Env != "" {
		t.Source = "spans (örneklenmiş, ortam " + raw.Env + ")"
	}
	cur := raw.SpanCur
	switch {
	case cur.Missing:
		t.Status = HRStatusMissing
		return t
	case cur.Err != nil:
		t.Status, t.Note = HRStatusUnread, "span özeti okunamadı: "+cur.Err.Error()
		return t
	}
	t.StoredSpans, t.ErrorSpans = cur.V.SpanCount, cur.V.ErrorCount
	t.ErrorSharePct = errPct(cur.V.ErrorCount, cur.V.SpanCount)
	t.Status = HRStatusOK
	if cur.V.SpanCount == 0 {
		t.Status = HRStatusNone
	}
	if pr := raw.SpanPrior; !pr.failed() {
		pe, ps := pr.V.ErrorCount, errPct(pr.V.ErrorCount, pr.V.SpanCount)
		t.PriorErrorSpans, t.PriorErrorSharePct = &pe, &ps
		t.Trend = hrTrend(t.ErrorSharePct, ps, pr.V.SpanCount)
	}
	if p := raw.Profile; !p.failed() {
		et := p.V.ErrorTraces
		t.ErrorTraces = &et
		t.ErrorTracesWindowS = int(p.V.To.Sub(p.V.From).Seconds())
	}
	return t
}

// hrClassLabel — SAF: satırın hata sınıfı (error.type · HTTP · gRPC · DB).
func hrClassLabel(r chstore.ErrorProfileRow) string {
	var parts []string
	if r.ErrorType != "" {
		parts = append(parts, "error.type="+r.ErrorType)
	}
	if r.HTTPStatus > 0 {
		parts = append(parts, "http="+strconv.Itoa(int(r.HTTPStatus)))
	}
	if r.GRPCCode != "" {
		parts = append(parts, "grpc="+r.GRPCCode)
	}
	if r.DBSystem != "" {
		parts = append(parts, "db="+r.DBSystem)
	}
	if len(parts) == 0 {
		if r.Message != "" {
			m := r.Message
			if len(m) > 60 {
				m = m[:60]
			}
			return "status=error (" + m + ")"
		}
		return "status=error"
	}
	return strings.Join(parts, " · ")
}

// hrOpAgg — operasyon başına hata toplamı + baskın sınıf.
type hrOpAgg struct {
	name   string
	errors uint64
	dom    chstore.ErrorProfileRow
}

// hrOpAggs — SAF: profil satırları → hata sayısına göre sıralı operasyonlar.
func hrOpAggs(rows []chstore.ErrorProfileRow) []hrOpAgg {
	idx := map[string]int{}
	var out []hrOpAgg
	for _, r := range rows {
		i, ok := idx[r.Operation]
		if !ok {
			i = len(out)
			idx[r.Operation] = i
			out = append(out, hrOpAgg{name: r.Operation})
		}
		out[i].errors += r.Errors
		if r.Errors > out[i].dom.Errors {
			out[i].dom = r
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].errors != out[j].errors {
			return out[i].errors > out[j].errors
		}
		return out[i].name < out[j].name
	})
	return out
}

func hrTopOpNames(rows []chstore.ErrorProfileRow, n int) []string {
	aggs := hrOpAggs(rows)
	var out []string
	for i := 0; i < len(aggs) && i < n; i++ {
		out = append(out, aggs[i].name)
	}
	return out
}

func hrProfileStatus(p hrRead[chstore.ErrorProfile]) (string, string) {
	switch {
	case p.Missing:
		return HRStatusMissing, "hata profili okuyucusu yok"
	case p.Err != nil:
		return HRStatusUnread, "hatalı span profili okunamadı: " + p.Err.Error()
	}
	return "", ""
}

func hrProfileSource(p chstore.ErrorProfile) string {
	s := fmt.Sprintf("spans — yalnız hatalı span'ler (hatalar örneklenmez), son %s", fmtWindowTR(int(p.To.Sub(p.From).Seconds())))
	if p.Clamped {
		s += " (pencere 1 sa'e kırpıldı)"
	}
	return s
}

func assembleErrorOps(raw hrRaw) HROps {
	o := HROps{}
	if st, note := hrProfileStatus(raw.Profile); st != "" {
		o.Status, o.Note, o.Source = st, note, "spans (hatalı span'ler)"
		return o
	}
	p := raw.Profile.V
	o.Source = hrProfileSource(p) + "; hata % = operation_summary_5m saklanan span'leri (örneklenmiş)"
	aggs := hrOpAggs(p.Rows)
	if len(aggs) == 0 {
		o.Status, o.Note = HRStatusNone, "pencerede hatalı span yok"
		return o
	}
	o.Status = HRStatusOK
	var notes []string
	tot := raw.OpTotals
	switch {
	case raw.Env != "":
		notes = append(notes, "operasyon toplamlarında ortam boyutu yok — hata % verilmedi")
	case tot.Missing:
		notes = append(notes, "operasyon toplamı okuyucusu yok — hata % verilmedi")
	case tot.Err != nil:
		notes = append(notes, "operasyon toplamları okunamadı — hata % verilmedi")
	}
	for i := 0; i < len(aggs) && i < hrOpsTop; i++ {
		a := aggs[i]
		row := HROp{Operation: a.name, Errors: a.errors, Dominant: hrClassLabel(a.dom), SampleTrace: a.dom.Sample}
		if a.errors > 0 {
			row.DominantPct = errPct(a.dom.Errors, a.errors)
		}
		if raw.Env == "" && !tot.failed() {
			if t, ok := tot.V[a.name]; ok && t.Calls > 0 {
				row.ErrorPct = f64p(errPct(t.Errors, t.Calls))
			}
		}
		o.Rows = append(o.Rows, row)
	}
	if p.Truncated {
		notes = append(notes, "grup tavanı doldu — sayılar alt sınır")
	}
	o.Note = strings.Join(notes, "; ")
	return o
}

// hrCounts — SAF: anahtar → hata toplamı, payı; çoktan aza, ilk n.
func hrCounts(m map[string]uint64, total uint64, n int) []HRCount {
	out := make([]HRCount, 0, len(m))
	for k, v := range m {
		out = append(out, HRCount{Value: k, Errors: v, SharePct: errPct(v, total)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Errors != out[j].Errors {
			return out[i].Errors > out[j].Errors
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func assembleTechnical(raw hrRaw) HRTech {
	t := HRTech{}
	if st, note := hrProfileStatus(raw.Profile); st != "" {
		t.Status, t.Note, t.Source = st, note, "spans (hatalı span'ler)"
	} else {
		p := raw.Profile.V
		t.Source = hrProfileSource(p)
		et, hs, gs, db := map[string]uint64{}, map[string]uint64{}, map[string]uint64{}, map[string]uint64{}
		var total uint64
		for _, r := range p.Rows {
			total += r.Errors
			if r.ErrorType != "" {
				et[r.ErrorType] += r.Errors
			}
			if r.HTTPStatus > 0 {
				hs[strconv.Itoa(int(r.HTTPStatus))] += r.Errors
			}
			if r.GRPCCode != "" {
				gs[r.GRPCCode] += r.Errors
			}
			if r.DBSystem != "" {
				db[r.DBSystem] += r.Errors
			}
		}
		if p.TotalErrors > total {
			total = p.TotalErrors
		}
		t.ErrorType, t.HTTPStatus, t.GRPCStatus, t.DB = hrCounts(et, total, hrTechTop), hrCounts(hs, total, hrTechTop), hrCounts(gs, total, hrTechTop), hrCounts(db, total, hrTechTop)
		t.Status = HRStatusOK
		if total == 0 {
			t.Status, t.Note = HRStatusNone, "pencerede hatalı span yok"
		} else {
			t.Note = "pay = servisin tüm hatalı span'leri içinde; etiketi olmayan hatalar kırılımda yok"
		}
	}
	// Aşağı hedefler: VM (örneklenmemiş) önce, topology ikinci.
	var dsNotes []string
	vmOK := false
	switch d := raw.Downstream; {
	case d.Missing:
		dsNotes = append(dsNotes, "VM istemci histogramı yok")
	case d.Err != nil:
		dsNotes = append(dsNotes, "VM istemci histogramı okunamadı")
	default:
		vmOK = true
		ts := append([]runtimesig.TargetRED(nil), d.V.Targets...)
		sort.SliceStable(ts, func(i, j int) bool { return ts[i].ErrorPct > ts[j].ErrorPct })
		for _, x := range ts {
			if x.ErrorPct <= 0 || len(t.Downstream) >= hrDownMax {
				continue
			}
			tgt := x.Address
			if x.Service != "" {
				tgt = x.Service
			}
			t.Downstream = append(t.Downstream, HRDown{Target: tgt, Source: "vm", ErrorPct: r2(x.ErrorPct), Dominant: x.DominantError})
		}
	}
	switch e := raw.Edges; {
	case e.Missing:
		dsNotes = append(dsNotes, "topology okuyucusu yok")
	case e.Err != nil:
		dsNotes = append(dsNotes, "topology_edges_5m okunamadı")
	default:
		es := append([]chstore.DownstreamEdge(nil), e.V...)
		sort.SliceStable(es, func(i, j int) bool { return es[i].Errors > es[j].Errors })
		seen := map[string]bool{}
		for _, x := range t.Downstream {
			seen[x.Target] = true
		}
		for _, x := range es {
			if x.Errors == 0 || seen[x.Target] || len(t.Downstream) >= hrDownMax*2 {
				continue
			}
			t.Downstream = append(t.Downstream, HRDown{Target: x.Target, Kind: x.Kind, Source: "topology", ErrorPct: errPct(x.Errors, x.Calls), Errors: x.Errors})
		}
	}
	switch {
	case raw.Downstream.failed() && raw.Edges.failed():
		t.DownstreamStatus = "okunamadı: " + strings.Join(dsNotes, "; ")
	case len(t.Downstream) == 0:
		t.DownstreamStatus = "hatalı aşağı hedef yok"
	default:
		t.DownstreamStatus = "vm = örneklenmemiş hata %; topology = saklanan span'ler (örneklenmiş)"
		if !vmOK {
			t.DownstreamStatus += "; " + strings.Join(dsNotes, "; ")
		}
	}
	return t
}

func nsISO(ns int64) string {
	if ns <= 0 {
		return ""
	}
	return time.Unix(0, ns).UTC().Format(time.RFC3339)
}

func assembleExceptions(raw hrRaw) HRExceptions {
	x := HRExceptions{Source: "exception_groups (occurrences = grubun toplamı)"}
	e := raw.Exceptions
	switch {
	case e.Missing:
		x.Status, x.Note = HRStatusMissing, "exception okuyucusu yok"
		return x
	case e.Err != nil:
		x.Status, x.Note = HRStatusUnread, "exception_groups okunamadı: "+e.Err.Error()
		return x
	case len(e.V) == 0:
		x.Status, x.Note = HRStatusNone, "pencerede etkin exception grubu yok"
		return x
	}
	x.Status = HRStatusOK
	for i, g := range e.V {
		if i >= hrExcTop {
			x.HasMore = true
			break
		}
		msg := g.Message
		if len(msg) > 120 {
			msg = msg[:120]
		}
		kind := "recurring"
		if g.FirstSeen >= raw.From.UnixNano() {
			kind = "new"
		}
		x.Groups = append(x.Groups, HRExc{Type: g.Type, Message: msg, Occurrences: g.Occurrences,
			FirstSeen: nsISO(g.FirstSeen), LastSeen: nsISO(g.LastSeen), Kind: kind, State: g.State})
	}
	if raw.Env != "" {
		x.Note = "exception gruplarında ortam boyutu yok — tüm ortamlar"
	}
	return x
}

func assembleMessaging(raw hrRaw) HRMessaging {
	m := HRMessaging{Source: "messaging_caller_summary_5m (örneklenmiş span) + Kafka istemci metrikleri (VictoriaMetrics)"}
	if !raw.KafkaFamily.failed() {
		f := raw.KafkaFamily.V
		m.Family = &f
	}
	consumer := m.Family != nil && m.Family.Consume
	flow := raw.Flow
	if !flow.failed() {
		for _, r := range flow.V {
			if r.Service != raw.Service {
				continue
			}
			if strings.EqualFold(r.Role, "consumer") {
				consumer = true
			}
			if len(m.Topics) < hrTopicsMax {
				m.Topics = append(m.Topics, HRTopic{System: r.System, Destination: r.Destination, Role: strings.ToLower(r.Role),
					Spans: r.Spans, ErrorPct: errPct(r.Errors, r.Spans), P95Ms: r2(r.P95Ms)})
			}
		}
	}
	// Lag — yalnız VM serisinden; yoksa "ölçülmüyor" (değer asla yazılmaz).
	switch l := raw.Lag; {
	case l.Missing:
		m.LagStatus = "ölçülmüyor (metrik kaynağı yok)"
	case l.Err != nil:
		m.LagStatus = "okunamadı: " + l.Err.Error()
	default:
		m.Lag = lagRows(raw.Service, l.V)
		switch {
		case len(m.Lag) > 0:
			m.LagStatus = "ölçülüyor (" + hcKafkaLagMetric + ", kayıt)"
		case consumer:
			m.LagStatus = "ölçülmüyor (" + hcKafkaLagMetric + " serisi yok)"
		default:
			m.LagStatus = "ölçülmüyor (tüketici rolü görülmedi)"
		}
	}
	hasMetric := m.Family != nil && m.Family.State == "metric"
	switch {
	case flow.Err != nil && !hasMetric && len(m.Lag) == 0:
		m.Status, m.Note = HRStatusUnread, "messaging MV okunamadı: "+flow.Err.Error()
	case flow.Missing && raw.KafkaFamily.Missing && raw.Lag.Missing:
		m.Status, m.Note = HRStatusMissing, "messaging okuyucusu yok"
	case len(m.Topics) == 0 && !hasMetric && len(m.Lag) == 0:
		m.Status, m.Note = HRStatusNone, "pencerede bu servisin Kafka/MQ span'i ya da Kafka istemci metriği yok"
		if raw.KafkaFamily.Err != nil {
			m.Note += "; Kafka metrik ailesi okunamadı"
		}
	default:
		m.Status = HRStatusOK
		var notes []string
		if flow.Err != nil {
			notes = append(notes, "messaging MV okunamadı")
		}
		if hasMetric && len(m.Topics) == 0 {
			notes = append(notes, "Kafka istemci metriği var, pencerede messaging span'i yok")
		}
		notes = append(notes, "üretici p95 = yayın süresi, tüketici p95 = işleme süresi; hata % saklanan span'ler")
		m.Note = strings.Join(notes, "; ")
	}
	return m
}

func assembleContext(raw hrRaw) HRContext {
	c := HRContext{}
	note := func(name string, missing bool, err error) {
		if missing {
			c.Missing = append(c.Missing, name)
		} else if err != nil {
			c.Unread = append(c.Unread, name)
		}
	}
	if p := raw.Problems; !p.failed() {
		v := p.V
		c.OpenProblems = &v
	}
	note("open_problems", raw.Problems.Missing, raw.Problems.Err)
	if a := raw.Anomalies; !a.failed() {
		n := len(a.V)
		c.Anomalies = &n
		c.AnomaliesHasMore = n >= hrAnomalyLimit
	}
	note("anomalies", raw.Anomalies.Missing, raw.Anomalies.Err)
	d := raw.Deploys
	note("deploys", d.Missing, d.Err)
	switch {
	case d.Missing:
		c.DeployStatus = "deploy okuyucusu yok"
	case d.Err != nil:
		c.DeployStatus = "okunamadı"
	case len(d.V) == 0 && !raw.DeployCovered:
		c.DeployStatus = "deploy MV'si pencereyi kapsamıyor — bilinmiyor"
	case len(d.V) == 0:
		c.DeployStatus = "son 6 sa'te ya da pencerede deploy yok"
	default:
		last := d.V[0]
		for _, x := range d.V[1:] {
			if x.TimeUnixNs > last.TimeUnixNs {
				last = x
			}
		}
		c.LastDeploy = &HRDeploy{Version: last.Version, At: nsISO(last.TimeUnixNs), Source: last.Source}
		c.DeployStatus = "service_version_5m + deploy olayları"
	}
	return c
}

// assembleHealthReport — SAF: ham okumalar → rapor (bölüm sırası sabit).
func assembleHealthReport(raw hrRaw) ServiceHealthReport {
	return ServiceHealthReport{
		Order:       HRSectionOrder,
		WindowS:     int(raw.To.Sub(raw.From).Seconds()),
		To:          raw.To.UTC().Format(time.RFC3339),
		Env:         raw.Env,
		Metrics:     assembleMetrics(raw),
		TraceErrors: assembleTraceErrors(raw),
		ErrorOps:    assembleErrorOps(raw),
		Technical:   assembleTechnical(raw),
		Exceptions:  assembleExceptions(raw),
		Messaging:   assembleMessaging(raw),
		Context:     assembleContext(raw),
	}
}

func hrCap(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func fmtWindowTR(sec int) string {
	switch {
	case sec >= 3600 && sec%3600 == 0:
		return fmt.Sprintf("%d sa", sec/3600)
	case sec >= 60:
		return fmt.Sprintf("%d dk", sec/60)
	}
	return fmt.Sprintf("%d sn", sec)
}

// ── Deps bağlama + önbellek ───────────────────────────────────────────

// hrSourcesFrom — Deps → okuma dikişleri (servis / ortam bağlı).
func hrSourcesFrom(d Deps, service, env string) hrSources {
	var s hrSources
	if d.ServiceMetricRED != nil {
		s.metricRED = func(ctx context.Context, from, to time.Time) (MetricREDResult, error) {
			return d.ServiceMetricRED(ctx, service, env, from, to, hrMetricMdp)
		}
	}
	if d.KafkaFamily != nil {
		s.kafkaFamily = func(ctx context.Context, at time.Time) (KafkaFamilyInfo, error) {
			return d.KafkaFamily(ctx, service, at)
		}
	}
	if d.DownstreamRED != nil {
		s.downstream = func(ctx context.Context, from, to time.Time) (runtimesig.DownstreamReport, error) {
			return d.DownstreamRED(ctx, service, from, to)
		}
	}
	if d.Metrics != nil {
		s.lag = func(ctx context.Context, from, to time.Time) ([]chstore.SpanMetricSeries, error) {
			f, err := hcKafkaLagFilter(service, from, to)
			if err != nil {
				return nil, err
			}
			return d.Metrics.QueryMetric(ctx, f)
		}
	}
	st := d.Store
	if st == nil {
		return s
	}
	s.spanRED = func(ctx context.Context, from, to time.Time) (chstore.ServiceSummary, error) {
		rows, _, err := readServicesIn(ctx, d, from, to, []string{service}, env, 1)
		if err != nil || len(rows) == 0 {
			return chstore.ServiceSummary{Name: service}, err
		}
		return rows[0], nil
	}
	s.errProfile = func(ctx context.Context, from, to time.Time) (chstore.ErrorProfile, error) {
		return st.ServiceErrorProfile(ctx, service, env, from, to)
	}
	s.opTotals = func(ctx context.Context, names []string, from, to time.Time) (map[string]chstore.OpTotal, error) {
		return st.ServiceOpTotals(ctx, service, names, from, to)
	}
	s.exceptions = func(ctx context.Context, from, to time.Time) ([]chstore.ExceptionGroup, error) {
		return st.ListExceptionGroups(ctx, chstore.ExceptionGroupFilter{Service: service, ActiveFromNs: from.UnixNano(), ActiveToNs: to.UnixNano(),
			Sort: "occurrences", Dir: "desc", Limit: hrExcTop + 1})
	}
	s.edges = func(ctx context.Context, from, to time.Time) ([]chstore.DownstreamEdge, error) {
		return st.DownstreamEdges(ctx, service, from, to, hrEdgesMax)
	}
	s.msgFlow = func(ctx context.Context, from, to time.Time) ([]chstore.MessagingFlowRow, error) {
		return st.MessagingFlow(ctx, "", service, from, to, hrFlowRows)
	}
	s.problems = func(ctx context.Context) (uint64, error) {
		return st.CountProblems(ctx, chstore.ProblemFilter{Status: "open", Service: service, Env: env})
	}
	s.anomalies = func(ctx context.Context, from time.Time) ([]chstore.AnomalyEvent, error) {
		return st.ListAnomalyFeed(ctx, anomalyToolFilter(from.UnixNano(), hrAnomalyLimit, service, envScope{}))
	}
	s.deploys = func(ctx context.Context, from, to time.Time) ([]chstore.Deploy, bool, error) {
		return st.BaselineDeploys(ctx, service, from, to)
	}
	return s
}

// HealthReportCacheKey — SAF: TÜM girdiler (servis, ortam, pencere başı / sonu).
func HealthReportCacheKey(service, env string, from, to time.Time) string {
	return fmt.Sprintf("mcp:svc_health_report:v1:%s:%d:%d", fnvHex(service, env), from.Unix(), to.Unix())
}

// HealthReportWindow — SAF: pencere sonu dakikaya oturur (önbellek isabeti;
// rapor beyan ettiği pencereyi okur).
func HealthReportWindow(from, to time.Time) (time.Time, time.Time) {
	w := to.Sub(from)
	to = to.Truncate(time.Minute)
	return to.Add(-w), to
}

// ReadServiceHealthReport — get_service_health raporu (60 sn önbellek; okunamayan
// bölüm varsa önbelleğe girmez). Guided sağlık demeti de bunu çağırır.
func ReadServiceHealthReport(ctx context.Context, d Deps, service, env string, from, to time.Time) (ServiceHealthReport, error) {
	from, to = HealthReportWindow(from, to)
	run := func(c context.Context) ServiceHealthReport {
		return assembleHealthReport(collectHealthRaw(c, hrSourcesFrom(d, service, env), service, env, from, to))
	}
	if d.Memo == nil {
		return run(ctx), nil
	}
	body, err := d.Memo(ctx, HealthReportCacheKey(service, env, from, to), hrCacheTTL, func(c context.Context) (any, bool, error) {
		r := run(c)
		return r, !r.HasUnread(), nil
	})
	if err != nil {
		return ServiceHealthReport{}, err
	}
	var rep ServiceHealthReport
	if err := json.Unmarshal(body, &rep); err != nil {
		return ServiceHealthReport{}, err
	}
	return rep, nil
}
