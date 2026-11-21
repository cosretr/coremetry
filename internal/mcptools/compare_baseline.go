package mcptools

// compare_baseline.go — CoSRE faz D (görseller, operatör onaylı plan
// 2026-11-20): "ne değişti / neden yavaşladı" sorusunun DETERMİNİSTİK, ucuz
// cevabı. Bir servis (isteğe bağlı tek endpoint) için inceleme penceresini bir
// taban pencereyle (önceki dönem / dün aynı saat / geçen hafta aynı saat)
// kıyaslar ve ne değiştiğini döndürür:
//
//	red              iki pencerenin RED'i + farklar (istek/s, hata %, p50/p95/p99)
//	changed          sayılardan TÜRETİLMİŞ kısa bulgu satırları (model yazmaz)
//	top_endpoints    en çok değişen ≤5 endpoint (p95 / hata / trafik; yeni / kayıp)
//	new_error_types  taban bittikten SONRA ilk görülen istisna tipleri (≤5)
//	deploys          taban başından pencere sonuna sürüm değişimleri (≤5)
//	dependencies     çağrılan bağımlılıkların ortalama gecikme / hata farkı (≤5)
//
// OKUMA: yalnız ön-agregatlar (chstore/compare_baseline.go) — prod ölçeğinde
// (~2.3 milyar span/gün) her soruda koşabilsin; ham spans hiçbir dalda yok.
// compare_periods'un (giriş span'i popülasyonu, env ayrımı, ham yedek) ucuz
// ve geniş kardeşi; env ayrımı gerekiyorsa o.
//
// GÖRSEL: sonuç `data.blocks` taşır (bileşik kanıt aracı, compositeEvidenceTools)
// — kıyas tablosu + öne çıkan sinyalin satır içi grafiği (inceleme penceresi
// gölgeli; previous'ta taban penceresi soluk gölge, dün/geçen hafta kıyasında
// taban kesikli çizgi). Grafiğin her noktası AYNI okumanın kovalarıdır; model
// sayı yazmaz, blok sunucuda kurulur.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// CompareBaselineName — aracın adı.
const CompareBaselineName = "compare_baseline"

const (
	cbDefaultRangeS = 3600
	cbMinRangeS     = 300
	cbMaxRangeS     = 86400
	cbTopN          = 5
	cbMinCalls      = 20 // endpoint / bağımlılık kıyası için pencere başına en az çağrı
	cbChartPoints   = 60
	cbCacheTTL      = 60 * time.Second
	cbMsgRunes      = 120
	cbHopChanges    = "baseline"
	cbHopChart      = "baseline_chart"
)

// cbSteps — grafik kovası adayları (300'ün katı, 86400'ü böler: dün/geçen
// hafta kaydırması kova sınırına denk gelir).
var cbSteps = []int{300, 600, 900, 1800, 3600}

// baselineReader — üç okuma; *chstore.Store karşılar, test sahte verir.
type baselineReader interface {
	ReadBaseline(ctx context.Context, q chstore.BaselineQuery) (chstore.BaselineRaw, map[string]error, error)
	BaselineDeploys(ctx context.Context, service string, from, to time.Time) ([]chstore.Deploy, bool, error)
	ListExceptionGroups(ctx context.Context, f chstore.ExceptionGroupFilter) ([]chstore.ExceptionGroup, error)
}

type compareBaselineArgs struct {
	Service   string `json:"service"`
	Operation string `json:"operation,omitempty"`
	RangeS    int    `json:"range_s,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// cbPlan — SAF: argüman + pencere sonu → okuma planı.
func cbPlan(a compareBaselineArgs, end time.Time) (chstore.BaselineQuery, string, error) {
	svc := strings.TrimSpace(a.Service)
	if svc == "" {
		return chstore.BaselineQuery{}, "", fmt.Errorf("service zorunlu (list_services'teki tam ad)")
	}
	rs := a.RangeS
	if rs <= 0 {
		rs = cbDefaultRangeS
	}
	rs = min(max(rs, cbMinRangeS), cbMaxRangeS)
	rs = (rs + 299) / 300 * 300 // tam 5 dk kovaları
	kind := strings.ToLower(strings.TrimSpace(a.Reference))
	if kind == "" {
		kind = "previous"
	}
	switch kind {
	case "previous", "day_before", "week_before":
	default:
		return chstore.BaselineQuery{}, "", fmt.Errorf("reference geçersiz: previous | day_before | week_before (gelen %q)", a.Reference)
	}
	// Yalnız TAMAMLANMIŞ 5 dk kovaları: yarım son kova trafiği düşük gösterirdi.
	to := end.UTC().Truncate(5 * time.Minute)
	length := time.Duration(rs) * time.Second
	cur := chstore.PeriodWindow{From: to.Add(-length), To: to}
	ref, err := ReferenceWindow(kind, cur)
	if err != nil {
		return chstore.BaselineQuery{}, "", err
	}
	q := chstore.BaselineQuery{Service: svc, Operation: strings.TrimSpace(a.Operation), Cur: cur, Ref: ref,
		ViewFrom: cur.From.Add(-length), StepS: cbStep(2 * length)}
	if kind != "previous" {
		q.Overlay = cur.From.Sub(ref.From)
	}
	return q, kind, nil
}

// cbStep — SAF: grafik aralığı → ≤ cbChartPoints kovalı en küçük adım.
func cbStep(span time.Duration) int {
	for _, s := range cbSteps {
		if int(span.Seconds())/s <= cbChartPoints {
			return s
		}
	}
	return cbSteps[len(cbSteps)-1]
}

// ── sonuç şekli ─────────────────────────────────────────────────────────

type cbWindow struct {
	FromISO string `json:"from_iso"`
	ToISO   string `json:"to_iso"`
	Kind    string `json:"kind,omitempty"`
}

type cbRED struct {
	Calls        uint64  `json:"calls"`
	RatePerS     float64 `json:"rate_per_s"`
	ErrorRatePct float64 `json:"error_rate_pct"`
	P50Ms        float64 `json:"p50_ms"`
	P95Ms        float64 `json:"p95_ms"`
	P99Ms        float64 `json:"p99_ms"`
}

type cbDelta struct {
	RatePct      *float64 `json:"rate_pct"`
	ErrorRatePP  float64  `json:"error_rate_pp"`
	P95Pct       *float64 `json:"p95_pct"`
	P99Pct       *float64 `json:"p99_pct"`
	P95DeltaMs   float64  `json:"p95_delta_ms"`
	ErrorsChange int64    `json:"errors_change"`
}

type cbREDView struct {
	Current  cbRED   `json:"current"`
	Baseline cbRED   `json:"baseline"`
	Delta    cbDelta `json:"delta"`
}

type cbOpView struct {
	Endpoint       string   `json:"endpoint"`
	Change         string   `json:"change"` // latency | errors | traffic | new | gone
	Calls          uint64   `json:"calls"`
	BaseCalls      uint64   `json:"baseline_calls"`
	P95Ms          float64  `json:"p95_ms"`
	BaseP95Ms      float64  `json:"baseline_p95_ms"`
	ErrorRatePct   float64  `json:"error_rate_pct"`
	BaseErrRatePct float64  `json:"baseline_error_rate_pct"`
	P95Pct         *float64 `json:"p95_pct,omitempty"`
	score          float64
}

type cbErrView struct {
	Type         string `json:"type"`
	Message      string `json:"message,omitempty"`
	FirstSeenISO string `json:"first_seen_iso"`
	Occurrences  uint64 `json:"occurrences"`
	Fingerprint  string `json:"fingerprint"`
}

type cbDeployView struct {
	Version string `json:"version"`
	AtISO   string `json:"at_iso"`
	Between bool   `json:"between"` // taban bittikten sonra, pencere sonundan önce
	Source  string `json:"source,omitempty"`
}

type cbDepView struct {
	Target         string   `json:"target"`
	Kind           string   `json:"kind"`
	Change         string   `json:"change"` // latency | errors | new | gone
	AvgMs          float64  `json:"avg_ms"`
	BaseAvgMs      float64  `json:"baseline_avg_ms"`
	AvgPct         *float64 `json:"avg_pct,omitempty"`
	Calls          uint64   `json:"calls"`
	BaseCalls      uint64   `json:"baseline_calls"`
	ErrorRatePct   float64  `json:"error_rate_pct"`
	BaseErrRatePct float64  `json:"baseline_error_rate_pct"`
	score          float64
}

type cbData struct {
	Blocks []pivotBlock `json:"blocks"`
}

// BaselineResult — zarf; alan sırası = JSON sırası (veri EN SONDA: model
// görünümü kırpılırsa önce grafik noktaları düşer).
type BaselineResult struct {
	Tool          string             `json:"tool"`
	OK            bool               `json:"ok"`
	Partial       bool               `json:"partial"`
	Empty         bool               `json:"empty,omitempty"`
	Message       string             `json:"message,omitempty"`
	Error         *cosreError        `json:"error,omitempty"`
	Request       map[string]any     `json:"request"`
	Window        cbWindow           `json:"window"`
	Reference     cbWindow           `json:"reference"`
	RED           *cbREDView         `json:"red,omitempty"`
	Changed       []string           `json:"changed"`
	TopEndpoints  []cbOpView         `json:"top_endpoints,omitempty"`
	NewErrorTypes []cbErrView        `json:"new_error_types,omitempty"`
	Deploys       []cbDeployView     `json:"deploys,omitempty"`
	Dependencies  []cbDepView        `json:"dependencies,omitempty"`
	ChartSignal   string             `json:"chart_signal,omitempty"`
	Unread        []string           `json:"unread,omitempty"`
	Notes         []string           `json:"notes"`
	Evidence      *cosreEvidence     `json:"evidence,omitempty"`
	Evidences     []cosreHopEvidence `json:"evidences,omitempty"`
	Data          cbData             `json:"data"`
}

// cbInput — SAF kurucunun girdisi (okumaların ham sonuçları).
type cbInput struct {
	Q           chstore.BaselineQuery
	Kind        string
	Raw         chstore.BaselineRaw
	ReadErrs    map[string]error
	Deploys     []chstore.Deploy
	DeploysMV   bool
	DeploysErr  error
	Exceptions  []chstore.ExceptionGroup
	ExcErr      error
	DurationsMs map[string]int64
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func r2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}

// relPct — SAF: göreli değişim %; taban 0 → nil (sonsuz/tanımsız).
func relPct(cur, base float64) *float64 {
	if base <= 0 {
		return nil
	}
	v := r2((cur - base) / base * 100)
	return &v
}

func errPct(errs, calls uint64) float64 {
	if calls == 0 {
		return 0
	}
	return r2(float64(errs) / float64(calls) * 100)
}

func cbREDOf(r chstore.BaselineRED, w chstore.PeriodWindow) cbRED {
	return cbRED{Calls: r.Calls, RatePerS: r2(float64(r.Calls) / w.To.Sub(w.From).Seconds()),
		ErrorRatePct: errPct(r.Errors, r.Calls), P50Ms: r2(r.P50Ms), P95Ms: r2(r.P95Ms), P99Ms: r2(r.P99Ms)}
}

func absf(v *float64) float64 {
	if v == nil {
		return 0
	}
	return math.Abs(*v)
}

// cbKindTR — taban türünün Türkçe adı.
func cbKindTR(kind string) string {
	switch kind {
	case "day_before":
		return "dün aynı saat"
	case "week_before":
		return "geçen hafta aynı saat"
	}
	return "önceki dönem"
}

// buildBaselineResult — SAF: ham okumalar → zarf + bloklar. Bütün sayılar
// okumalardan; yorum yok, sıralama ve eşikler sabit.
func buildBaselineResult(in cbInput) BaselineResult {
	q := in.Q
	res := BaselineResult{Tool: CompareBaselineName, OK: true, Changed: []string{}, Notes: []string{}, Data: cbData{Blocks: []pivotBlock{}},
		Request: map[string]any{"service": q.Service, "reference": in.Kind, "range_s": int(q.Cur.To.Sub(q.Cur.From).Seconds())},
		Window:  cbWindow{FromISO: iso(q.Cur.From), ToISO: iso(q.Cur.To)}, Reference: cbWindow{FromISO: iso(q.Ref.From), ToISO: iso(q.Ref.To), Kind: in.Kind}}
	if q.Operation != "" {
		res.Request["operation"] = q.Operation
	}
	raw := in.Raw
	scope := q.Service
	if q.Operation != "" {
		scope += " · " + q.Operation
	}
	if raw.Cur.Calls == 0 && raw.Ref.Calls == 0 {
		res.Empty = true
		res.Message = fmt.Sprintf("%s için iki pencerede de span yok — adı list_services / list_operations ile doğrula (veri yok ≠ hata yok).", scope)
		return res
	}
	cur, base := cbREDOf(raw.Cur, q.Cur), cbREDOf(raw.Ref, q.Ref)
	d := cbDelta{RatePct: relPct(cur.RatePerS, base.RatePerS), ErrorRatePP: r2(cur.ErrorRatePct - base.ErrorRatePct),
		P95Pct: relPct(cur.P95Ms, base.P95Ms), P99Pct: relPct(cur.P99Ms, base.P99Ms), P95DeltaMs: r2(cur.P95Ms - base.P95Ms),
		ErrorsChange: int64(raw.Cur.Errors) - int64(raw.Ref.Errors)}
	res.RED = &cbREDView{Current: cur, Baseline: base, Delta: d}
	ref := cbKindTR(in.Kind)

	// Bulgu satırları (eşikler sabit; sayılar okumadan).
	if raw.Ref.Calls == 0 {
		res.Changed = append(res.Changed, fmt.Sprintf("tabanda (%s) trafik yok — kıyas yapılamadı, yalnız şimdiki değerler geçerli", ref))
	} else {
		if d.P95Pct != nil && math.Abs(*d.P95Pct) >= 20 && math.Abs(d.P95DeltaMs) >= 5 {
			res.Changed = append(res.Changed, fmt.Sprintf("p95 %s → %s ms (%+.0f%%; taban: %s)", fmtMs(base.P95Ms), fmtMs(cur.P95Ms), *d.P95Pct, ref))
		}
		if math.Abs(d.ErrorRatePP) >= 0.5 && (cur.ErrorRatePct >= 1 || base.ErrorRatePct >= 1) {
			res.Changed = append(res.Changed, fmt.Sprintf("hata oranı %%%.2f → %%%.2f (%+.2f puan)", base.ErrorRatePct, cur.ErrorRatePct, d.ErrorRatePP))
		}
		if d.RatePct != nil && math.Abs(*d.RatePct) >= 30 {
			res.Changed = append(res.Changed, fmt.Sprintf("trafik %.2f → %.2f istek/s (%+.0f%%)", base.RatePerS, cur.RatePerS, *d.RatePct))
		}
	}

	// Endpoint'ler (yalnız servis kapsamında).
	if q.Operation == "" {
		res.TopEndpoints = cbTopOps(raw.Ops, q)
		if raw.OpsTruncated {
			res.Notes = append(res.Notes, "endpoint okuması satır tavanına dayandı — en yoğun endpoint'ler kıyaslandı.")
		}
	}
	res.Dependencies = cbTopDeps(raw.Edges)
	if raw.EdgesTruncated {
		res.Notes = append(res.Notes, "bağımlılık okuması satır tavanına dayandı — en yoğun hedefler kıyaslandı.")
	}
	for _, o := range res.TopEndpoints {
		if o.Change == "latency" && o.P95Pct != nil && *o.P95Pct >= 50 {
			res.Changed = append(res.Changed, fmt.Sprintf("en çok yavaşlayan endpoint: %s (p95 %s → %s ms)", o.Endpoint, fmtMs(o.BaseP95Ms), fmtMs(o.P95Ms)))
			break
		}
	}
	for _, dep := range res.Dependencies {
		if dep.Change == "latency" && dep.AvgPct != nil && *dep.AvgPct >= 50 {
			res.Changed = append(res.Changed, fmt.Sprintf("bağımlılık yavaşladı: %s (ort. %s → %s ms)", dep.Target, fmtMs(dep.BaseAvgMs), fmtMs(dep.AvgMs)))
			break
		}
	}

	// Yeni hata tipleri: taban bittikten SONRA ilk görülen gruplar.
	if in.ExcErr != nil {
		res.Unread = append(res.Unread, "new_error_types")
	} else {
		res.NewErrorTypes = cbNewErrors(in.Exceptions, q)
		if len(res.NewErrorTypes) > 0 {
			res.Changed = append(res.Changed, fmt.Sprintf("%d yeni hata tipi (ilk: %s)", len(res.NewErrorTypes), res.NewErrorTypes[0].Type))
		}
	}
	// Deploy'lar.
	if in.DeploysErr != nil {
		res.Unread = append(res.Unread, "deploys")
	} else {
		res.Deploys = cbDeploys(in.Deploys, q)
		for _, dp := range res.Deploys {
			if dp.Between {
				res.Changed = append(res.Changed, fmt.Sprintf("arada deploy: %s (%s)", dp.Version, dp.AtISO))
				break
			}
		}
		if !in.DeploysMV {
			res.Notes = append(res.Notes, "sürüm MV'si (service_version_5m) bu geriye bakışı henüz kapsamıyor — çıkarımlı deploy okunmadı; yalnız deploy olayları listelendi.")
		}
	}
	for _, k := range []string{"series", "ops", "edges"} {
		if in.ReadErrs[k] != nil {
			res.Unread = append(res.Unread, map[string]string{"series": "chart", "ops": "top_endpoints", "edges": "dependencies"}[k])
		}
	}
	if len(res.Unread) > 0 {
		res.Partial = true
		res.Notes = append(res.Notes, "okunamayan bölümler: "+strings.Join(res.Unread, ", ")+" — okunamadı ≠ değişiklik yok.")
	}
	if len(res.Changed) == 0 {
		res.Changed = append(res.Changed, fmt.Sprintf("taban (%s) ile kıyasta eşiği aşan değişiklik yok (p95 ±%%20, hata ±0.5 puan, trafik ±%%30)", ref))
	}
	res.Notes = append(res.Notes,
		"Kaynak 5 dk ön-agregatlar (service_summary_5m / operation_summary_5m / topology_edges_5m): servisin TÜM span'leri, ortamlar birleşik; ortam ayrımı ya da giriş-span'i popülasyonu gerekiyorsa compare_periods.",
		"Bağımlılık gecikmesi çağıranın gördüğü ORTALAMADIR (p95 değil).")

	res.ChartSignal = cbChartSignal(d)
	res.Evidences = cbEvidences(in)
	if len(res.Evidences) > 0 {
		res.Evidence = &res.Evidences[0].cosreEvidence
	}
	res.Data.Blocks = cbBlocks(res, in, scope)
	return res
}

func fmtMs(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// cbTopOps — SAF: en çok değişen ≤5 endpoint. Skor: max(|p95 %|, |hata puanı|×10,
// |trafik %|); yeni / kaybolan endpoint skoru 1000 (önde). Eşit skor → ad.
func cbTopOps(ops []chstore.BaselineOp, q chstore.BaselineQuery) []cbOpView {
	curS, refS := q.Cur.To.Sub(q.Cur.From).Seconds(), q.Ref.To.Sub(q.Ref.From).Seconds()
	var out []cbOpView
	for _, o := range ops {
		v := cbOpView{Endpoint: o.Name, Calls: o.Cur.Calls, BaseCalls: o.Ref.Calls, P95Ms: r2(o.Cur.P95Ms), BaseP95Ms: r2(o.Ref.P95Ms),
			ErrorRatePct: errPct(o.Cur.Errors, o.Cur.Calls), BaseErrRatePct: errPct(o.Ref.Errors, o.Ref.Calls)}
		switch {
		case o.Ref.Calls == 0 && o.Cur.Calls >= cbMinCalls:
			v.Change, v.score = "new", 1000+float64(o.Cur.Calls)/1e9
		case o.Cur.Calls == 0 && o.Ref.Calls >= cbMinCalls:
			v.Change, v.score = "gone", 1000+float64(o.Ref.Calls)/1e9
		case o.Cur.Calls >= cbMinCalls && o.Ref.Calls >= cbMinCalls:
			v.P95Pct = relPct(v.P95Ms, v.BaseP95Ms)
			lat, errS := absf(v.P95Pct), math.Abs(v.ErrorRatePct-v.BaseErrRatePct)*10
			traf := absf(relPct(float64(o.Cur.Calls)/curS, float64(o.Ref.Calls)/refS))
			if math.Abs(v.P95Ms-v.BaseP95Ms) < 5 {
				lat = 0 // ms altı oynama gürültü
			}
			v.Change, v.score = "latency", lat
			if errS > v.score {
				v.Change, v.score = "errors", errS
			}
			if traf > v.score {
				v.Change, v.score = "traffic", traf
			}
			if v.score < 20 {
				continue
			}
		default:
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].Endpoint < out[j].Endpoint
	})
	if len(out) > cbTopN {
		out = out[:cbTopN]
	}
	return out
}

// cbTopDeps — SAF: ortalama gecikmesi / hatası en çok değişen ≤5 bağımlılık.
func cbTopDeps(edges []chstore.BaselineEdge) []cbDepView {
	var out []cbDepView
	for _, e := range edges {
		v := cbDepView{Target: e.Child, Kind: e.Kind, AvgMs: r2(e.CurAvgMs), BaseAvgMs: r2(e.RefAvgMs), Calls: e.CurCalls, BaseCalls: e.RefCalls,
			ErrorRatePct: errPct(e.CurErrs, e.CurCalls), BaseErrRatePct: errPct(e.RefErrs, e.RefCalls)}
		switch {
		case e.RefCalls == 0 && e.CurCalls >= cbMinCalls:
			v.Change, v.score = "new", 1000
		case e.CurCalls == 0 && e.RefCalls >= cbMinCalls:
			v.Change, v.score = "gone", 1000
		case e.CurCalls >= cbMinCalls && e.RefCalls >= cbMinCalls:
			v.AvgPct = relPct(v.AvgMs, v.BaseAvgMs)
			lat := absf(v.AvgPct)
			if math.Abs(v.AvgMs-v.BaseAvgMs) < 2 {
				lat = 0
			}
			errS := math.Abs(v.ErrorRatePct-v.BaseErrRatePct) * 10
			v.Change, v.score = "latency", lat
			if errS > lat {
				v.Change, v.score = "errors", errS
			}
			if v.score < 20 {
				continue
			}
		default:
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].Target < out[j].Target
	})
	if len(out) > cbTopN {
		out = out[:cbTopN]
	}
	return out
}

// cbNewErrors — SAF: ilk görülmesi taban sonundan sonra olan gruplar, sayıya göre.
func cbNewErrors(gs []chstore.ExceptionGroup, q chstore.BaselineQuery) []cbErrView {
	after, until := q.Ref.To.UnixNano(), q.Cur.To.UnixNano()
	var out []cbErrView
	for _, g := range gs {
		if g.FirstSeen < after || g.FirstSeen > until {
			continue
		}
		out = append(out, cbErrView{Type: g.Type, Message: truncateRunes(g.Message, cbMsgRunes), FirstSeenISO: iso(time.Unix(0, g.FirstSeen)),
			Occurrences: g.Occurrences, Fingerprint: g.Fingerprint})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences > out[j].Occurrences
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	if len(out) > cbTopN {
		out = out[:cbTopN]
	}
	return out
}

// cbDeploys — SAF: [taban başı, pencere sonu] deploy'ları; arada olanlar önde.
func cbDeploys(ds []chstore.Deploy, q chstore.BaselineQuery) []cbDeployView {
	var out []cbDeployView
	for _, d := range ds {
		at := time.Unix(0, d.TimeUnixNs)
		if at.Before(q.Ref.From) || at.After(q.Cur.To) {
			continue
		}
		out = append(out, cbDeployView{Version: d.Version, AtISO: iso(at), Between: !at.Before(q.Ref.To), Source: d.Source})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Between != out[j].Between {
			return out[i].Between
		}
		return out[i].AtISO > out[j].AtISO
	})
	if len(out) > cbTopN {
		out = out[:cbTopN]
	}
	return out
}

// cbChartSignal — SAF: grafiğin sinyali = en çok değişen RED bileşeni.
func cbChartSignal(d cbDelta) string {
	switch {
	case math.Abs(d.ErrorRatePP) >= 0.5 && math.Abs(d.ErrorRatePP)*10 >= absf(d.P95Pct):
		return "error_rate"
	case absf(d.P95Pct) >= 20:
		return "p95"
	case absf(d.RatePct) >= 30:
		return "rate"
	}
	return "p95"
}

// cbPoint — SAF: kovanın sinyal değeri.
func cbPoint(p chstore.BaselinePoint, signal string, stepS int) float64 {
	switch signal {
	case "error_rate":
		return errPct(p.Errors, p.Calls)
	case "rate":
		return r2(float64(p.Calls) / float64(stepS))
	}
	return r2(p.P95Ms)
}

func cbSignalMeta(signal string) (name, unit, label string) {
	switch signal {
	case "error_rate":
		return "error_rate", "%", "hata oranı"
	case "rate":
		return "rate", "req/s", "istek/s"
	}
	return "p95", "ms", "p95"
}

// cbChart — SAF: grafik yükü. Noktalar YALNIZ okumanın kovalarından; overlay
// (dün / geçen hafta) kesikli çizgi olarak şimdiki zamana kaydırılır.
func cbChart(in cbInput, signal, scope string) (EvChartPayload, bool) {
	q := in.Q
	if in.ReadErrs["series"] != nil {
		return EvChartPayload{}, false
	}
	name, unit, label := cbSignalMeta(signal)
	c := EvChartPayload{Title: scope + " · " + label + " · " + cbKindTR(in.Kind) + " kıyası",
		Window: EvBlockWindow{FromNs: q.ViewFrom.UnixNano(), ToNs: q.Cur.To.UnixNano()}, Series: []EvChartSeries{}}
	s := EvChartSeries{Name: name, Unit: unit, Points: [][2]float64{}}
	for _, p := range in.Raw.Series {
		s.Points = append(s.Points, [2]float64{float64(p.TimeMs), cbPoint(p, signal, q.StepS)})
	}
	if len(s.Points) == 0 {
		return c, false
	}
	c.Series = append(c.Series, sweepTrimSeries(s))
	if q.Overlay > 0 && len(in.Raw.OverlaySeries) > 0 {
		o := EvChartSeries{Name: name + " · " + cbKindTR(in.Kind), Unit: unit, Style: evStyleDashed, Points: [][2]float64{}}
		shift := q.Overlay.Milliseconds()
		for _, p := range in.Raw.OverlaySeries {
			o.Points = append(o.Points, [2]float64{float64(p.TimeMs + shift), cbPoint(p, signal, q.StepS)})
		}
		c.Series = append(c.Series, sweepTrimSeries(o))
	}
	if q.Overlay == 0 {
		c.Regions = append(c.Regions, EvChartRegion{FromMs: q.Ref.From.UnixMilli(), ToMs: q.Ref.To.UnixMilli(), Label: "taban", Tone: "muted"})
	}
	c.Regions = append(c.Regions, EvChartRegion{FromMs: q.Cur.From.UnixMilli(), ToMs: q.Cur.To.UnixMilli(), Label: "inceleme", Tone: "warn"})
	return c, true
}

// cbTable — SAF: kıyas tablosu (RED + değişen endpoint / bağımlılık / hata / deploy).
func cbTable(res BaselineResult, scope string) EvTablePayload {
	t := EvTablePayload{Title: scope + " · ne değişti (" + cbKindTR(res.Reference.Kind) + ")", Columns: []EvTableColumn{
		{Key: "what", Label: "Ne", Kind: "text"}, {Key: "name", Label: "Ad", Kind: "text"},
		{Key: "base", Label: "Taban", Kind: "text"}, {Key: "now", Label: "Şimdi", Kind: "text"}, {Key: "change", Label: "Değişim", Kind: "text"}}, Rows: [][]any{}}
	pct := func(p *float64) string {
		if p == nil {
			return "—"
		}
		return fmt.Sprintf("%+.0f%%", *p)
	}
	if r := res.RED; r != nil {
		t.Rows = append(t.Rows,
			[]any{"RED", "istek/s", fmt.Sprintf("%.2f", r.Baseline.RatePerS), fmt.Sprintf("%.2f", r.Current.RatePerS), pct(r.Delta.RatePct)},
			[]any{"RED", "hata %", fmt.Sprintf("%.2f", r.Baseline.ErrorRatePct), fmt.Sprintf("%.2f", r.Current.ErrorRatePct), fmt.Sprintf("%+.2f puan", r.Delta.ErrorRatePP)},
			[]any{"RED", "p95 ms", fmtMs(r.Baseline.P95Ms), fmtMs(r.Current.P95Ms), pct(r.Delta.P95Pct)},
			[]any{"RED", "p99 ms", fmtMs(r.Baseline.P99Ms), fmtMs(r.Current.P99Ms), pct(r.Delta.P99Pct)})
	}
	changeTR := map[string]string{"latency": "gecikme", "errors": "hata", "traffic": "trafik", "new": "yeni", "gone": "kayboldu"}
	for _, o := range res.TopEndpoints {
		base, now, ch := fmtMs(o.BaseP95Ms)+" ms", fmtMs(o.P95Ms)+" ms", pct(o.P95Pct)
		switch o.Change {
		case "errors":
			base, now, ch = fmt.Sprintf("%%%.2f", o.BaseErrRatePct), fmt.Sprintf("%%%.2f", o.ErrorRatePct), fmt.Sprintf("%+.2f puan", o.ErrorRatePct-o.BaseErrRatePct)
		case "traffic", "new", "gone":
			base, now, ch = fmt.Sprint(o.BaseCalls), fmt.Sprint(o.Calls), changeTR[o.Change]
		}
		t.Rows = append(t.Rows, []any{"endpoint · " + changeTR[o.Change], truncateRunes(o.Endpoint, 80), base, now, ch})
	}
	for _, dp := range res.Dependencies {
		base, now, ch := fmtMs(dp.BaseAvgMs)+" ms", fmtMs(dp.AvgMs)+" ms", pct(dp.AvgPct)
		if dp.Change != "latency" {
			base, now, ch = fmt.Sprintf("%%%.2f", dp.BaseErrRatePct), fmt.Sprintf("%%%.2f", dp.ErrorRatePct), changeTR[dp.Change]
		}
		t.Rows = append(t.Rows, []any{"bağımlılık · " + changeTR[dp.Change], truncateRunes(dp.Target, 80), base, now, ch})
	}
	for _, e := range res.NewErrorTypes {
		t.Rows = append(t.Rows, []any{"yeni hata", truncateRunes(e.Type, 80), "—", fmt.Sprint(e.Occurrences), "ilk " + e.FirstSeenISO})
	}
	for _, dp := range res.Deploys {
		when := "taban içinde"
		if dp.Between {
			when = "arada"
		}
		t.Rows = append(t.Rows, []any{"deploy", dp.Version, "—", dp.AtISO, when})
	}
	return t
}

// cbBlocks — tablo (changes) + grafik (chart). Kanıt sırasıyla aynı.
func cbBlocks(res BaselineResult, in cbInput, scope string) []pivotBlock {
	out := []pivotBlock{{Hop: cbHopChanges, Type: blocks.TypeTable, Payload: cbTable(res, scope)}}
	if c, ok := cbChart(in, res.ChartSignal, scope); ok {
		out = append(out, pivotBlock{Hop: cbHopChart, Type: blocks.TypeChart, Payload: c})
	}
	return out
}

// cbEvidences — iki çip: kıyas okuması (changes) ve grafik serisi (chart). Grafik
// SON — sohbet varsayılan olarak bloğu olan son çipi açar (satır içi grafik).
func cbEvidences(in cbInput) []cosreHopEvidence {
	q := in.Q
	redA, serA, opsA, _ := chstore.BaselineArgs(q)
	req := map[string]any{"service": q.Service, "family": "red"}
	changesSQL := inlineSQL(in.Raw.SQL.RED, redA...)
	if q.Operation == "" && in.Raw.SQL.Ops != "" {
		changesSQL += ";\n" + inlineSQL(in.Raw.SQL.Ops, opsA...)
	}
	out := []cosreHopEvidence{{Hop: cbHopChanges, As: queryMetricsName, Request: req, cosreEvidence: cosreEvidence{
		Source: evSourceCH, QueryKind: evKindSQL, Query: changesSQL, Window: evWin(q.Ref.From, q.Cur.To), DurationMs: in.DurationsMs["red"]}}}
	if in.ReadErrs["series"] == nil && len(in.Raw.Series) > 0 {
		n := 1
		if len(in.Raw.OverlaySeries) > 0 {
			n = 2
		}
		out = append(out, cosreHopEvidence{Hop: cbHopChart, As: queryMetricsName, Request: req, cosreEvidence: cosreEvidence{
			Source: evSourceCH, QueryKind: evKindSQL, Query: inlineSQL(in.Raw.SQL.Series, serA...), Window: evWin(q.ViewFrom, q.Cur.To),
			Series: intPtr(n), DurationMs: in.DurationsMs["red"]}})
	}
	return out
}

// runCompareBaseline — okumalar + SAF kurucu.
func runCompareBaseline(ctx context.Context, r baselineReader, a compareBaselineArgs, end time.Time) (BaselineResult, error) {
	q, kind, err := cbPlan(a, end)
	if err != nil {
		return BaselineResult{}, err
	}
	in := cbInput{Q: q, Kind: kind, DurationsMs: map[string]int64{}}
	if r == nil {
		res := BaselineResult{Tool: CompareBaselineName, Request: map[string]any{"service": q.Service}, Changed: []string{}, Notes: []string{},
			Error: &cosreError{Code: cosreErrNotConfigured, Message: "ClickHouse okuyucusu bağlı değil"}}
		return res, nil
	}
	el := stopwatch()
	raw, errs, err := r.ReadBaseline(ctx, q)
	in.DurationsMs["red"] = el()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return BaselineResult{}, ctx.Err()
		}
		res := BaselineResult{Tool: CompareBaselineName, Request: map[string]any{"service": q.Service}, Changed: []string{}, Notes: []string{},
			Window: cbWindow{FromISO: iso(q.Cur.From), ToISO: iso(q.Cur.To)}, Reference: cbWindow{FromISO: iso(q.Ref.From), ToISO: iso(q.Ref.To), Kind: kind},
			Error: &cosreError{Code: cosreClassify(err), Message: cosreErrMsg("RED okunamadı (okunamadı ≠ değişiklik yok)", err)}}
		return res, nil
	}
	in.Raw, in.ReadErrs = raw, errs
	in.Deploys, in.DeploysMV, in.DeploysErr = r.BaselineDeploys(ctx, q.Service, q.Ref.From, q.Cur.To)
	in.Exceptions, in.ExcErr = r.ListExceptionGroups(ctx, chstore.ExceptionGroupFilter{Service: q.Service,
		ActiveFromNs: q.Ref.To.UnixNano(), ActiveToNs: q.Cur.To.UnixNano(), Sort: "firstSeen", Dir: "desc", Limit: 50})
	return buildBaselineResult(in), nil
}

// cbCacheKey — SAF: TÜM girdiler (servis, op, tür, pencere sonu, uzunluk).
func cbCacheKey(q chstore.BaselineQuery, kind string) string {
	return fmt.Sprintf("mcp:compare_baseline:v1:%s:%d:%d", fnvHex(q.Service, q.Operation, kind), q.Cur.To.Unix(), int(q.Cur.To.Sub(q.Cur.From).Seconds()))
}

// compareBaselineTool — compare_baseline.
func compareBaselineTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             CompareBaselineName,
		ShortDescription: "Ne değişti / neden yavaşladı: servis ya da endpoint vs önceki dönem, dün, geçen hafta — RED farkı, değişen endpoint, yeni hata, deploy, bağımlılık; grafikli.",
		Description: "Answer 'what changed / why did it get slower' for ONE service (optionally one endpoint) in one cheap call: compares the window ending at the chat's window end (range_s) against a baseline window and returns " +
			"red (current vs baseline: calls, rate_per_s, error_rate_pct 0..100, p50/p95/p99 ms over the whole window; delta with rate_pct / p95_pct / p99_pct relative %, null when the baseline is 0, error_rate_pp in percentage points), " +
			"changed (server-derived one-line findings — quote them, do not recompute), top_endpoints (≤5 most changed endpoints: latency | errors | traffic | new | gone), " +
			"new_error_types (≤5 exception types first seen after the baseline ended), deploys (≤5 version changes from baseline start to window end; between=true means after the baseline), " +
			"dependencies (≤5 called targets whose AVERAGE latency or error rate changed), chart_signal, notes, unread (sections that could not be read — unread is not 'no change'). " +
			"The chat renders a comparison table and an inline chart of chart_signal with the window shaded from this same result; do not describe the chart's points. " +
			"reference: previous (default, same length right before) | day_before | week_before (same hours 1 / 7 days earlier, drawn as a dashed line). " +
			"COST: reads only the 5-minute pre-aggregates (service_summary_5m, operation_summary_5m, topology_edges_5m, service_version_5m) with LIMIT and time caps; cached 60 s — safe to call on every 'ne değişti' question. " +
			"Population is all of the service's spans with environments merged; for per-environment or entry-span-exact comparison use compare_periods. For a full health sweep use service_health_sweep.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":   map[string]any{"type": "string", "description": "Exact service name (from list_services)."},
				"operation": map[string]any{"type": "string", "description": "Exact endpoint / operation name (list_operations). Narrows RED and the chart; top_endpoints is then omitted, dependencies stay service-wide."},
				"range_s": map[string]any{"type": "integer", "minimum": cbMinRangeS, "maximum": cbMaxRangeS,
					"description": "Window length in seconds ending at the chat's window end (rounded to whole 5-minute buckets). Default 3600."},
				"reference": map[string]any{"type": "string", "enum": []string{"previous", "day_before", "week_before"},
					"description": "Baseline window. previous (default) = same length right before; day_before / week_before = same hours 1 / 7 days earlier."},
			},
			"required": []string{"service"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a compareBaselineArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r baselineReader
			if d.Store != nil {
				r = d.Store
			}
			end := nowOrAnchor(ctx)
			q, kind, err := cbPlan(a, end)
			if err != nil {
				return nil, err
			}
			run := func(c context.Context) (any, error) { return runCompareBaseline(c, r, a, end) }
			if d.Memo == nil || r == nil {
				return run(ctx)
			}
			body, err := d.Memo(ctx, cbCacheKey(q, kind), cbCacheTTL, func(c context.Context) (any, bool, error) {
				out, err := run(c)
				ok := err == nil
				if res, isRes := out.(BaselineResult); isRes && (res.Error != nil || res.Partial) {
					ok = false // hata / kısmi sonuç önbelleğe girmez
				}
				return out, ok, err
			})
			if err != nil {
				return nil, err
			}
			return json.RawMessage(body), nil
		},
	}
}
