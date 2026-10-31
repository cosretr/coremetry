package api

// cosre_route_red.go — v0.10.1738 (operatör onayı, mockup "Endpoint seç"
// ikinci adım): CoSRE v2 rota incelemesi. Seçilen (ya da soruda açık) route
// turunun cevabı sohbetin MEVCUT grafik yolundan (typed chart bloğu →
// lib/chatBlocks chartBlocks → components/CosreChart, uPlot) iki grafik taşır:
//   - "Throughput": toplam req/s + 5xx req/s,
//   - "Response time": p50 + p95 (ms),
// servis + route + env için sorunun penceresi boyunca, 1 dk adım (uzun
// pencerede ≤360 nokta).
//
// KAYNAK (operatör eki 2026-10-31): VictoriaMetrics okuması yapılandırılmışsa
// VM — servis RED'iyle aynı çözüm (resolveSvcREDInputs: aile, servise kapsanmış
// etiket keşfi, ortam eşleyicisi, durum etiketi) ve briefing'in gruplu PromQL
// kalıbı (cosre_briefing_http_group.go): istek sayısı `sum by (<durum>)
// (rate(_count))` → toplam + 5xx Go'da; p50/p95 histogram_quantile `sum by
// (le)`; topk YOK. VM yapılandırılmamışsa CH spanmetrics_1m (route MV; ham
// spans ASLA — TestRouteREDNoRawSpans). Kaynak grafik spec'inde ("VM" / "CH")
// ve cevapta; VM yapılandırılmış ama okunamadı → hata (okunamadı ≠ yok), CH'ye
// sessiz düşüş yok.
//
// Uç: GET /api/cosre/v2/route-red (v2 + copilot kapılı, salt-okur, serveCached
// 30 sn, anahtar tüm girdilerin FNV özeti, dakika kovası).

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func init() { registerRoutesExtra("cosre-route-red", (*Server).registerCosreRouteREDRoutes) }

func (s *Server) registerCosreRouteREDRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cosre/v2/route-red", s.requireCosreV2(s.requireCopilot(s.getCosreRouteRED)))
}

const (
	routeREDSrcVM = "VM"
	routeREDSrcCH = "CH"
	// routeChartSource — CosreChart spec.source: rota RED serisi bu uçtan.
	routeChartSource = "route"
	// routeREDMaxPoints — uzun pencerede adım büyür (1 dk tabanı).
	routeREDMaxPoints = 360
	routeREDTTL       = 30 * time.Second
	routeREDBudget    = 8 * time.Second
	routeREDArgMax    = 512
	routeREDMaxRange  = 7 * 24 * time.Hour
)

// routeREDLine — tek çizgi (lejant adı + nokta; zaman unix ns, lib/types SpanMetricSeries).
type routeREDLine struct {
	Name   string                    `json:"name"`
	Points []chstore.SpanMetricPoint `json:"points"`
}

// routeREDResponse — lib/types.ts CosreRouteRED.
type routeREDResponse struct {
	Source     string         `json:"source"`
	StepS      int            `json:"stepS"`
	Throughput []routeREDLine `json:"throughput"`
	Latency    []routeREDLine `json:"latency"`
	Notes      []string       `json:"notes,omitempty"`
	// Skip — çizilemez gerekçesi (VM'de rota etiketi yok, gRPC ailesi…).
	Skip string `json:"skip,omitempty"`
}

// routeREDStepS — SAF: 1 dk taban; pencere/360'ı aşarsa dakikaya yuvarlanmış büyük adım.
func routeREDStepS(rangeS int64) int {
	step := int64(60)
	if rangeS > routeREDMaxPoints*60 {
		step = (rangeS + routeREDMaxPoints - 1) / routeREDMaxPoints
		step = ((step + 59) / 60) * 60
	}
	return int(step)
}

// routeREDSourceFor — SAF: VM okuması yapılandırılmışsa VM, değilse CH (route MV).
func routeREDSourceFor(isVM bool) string {
	if isVM {
		return routeREDSrcVM
	}
	return routeREDSrcCH
}

// routeREDVMQuery — tek VM range sorgusu.
type routeREDVMQuery struct {
	Kind  string // count | p50 | p95
	Query string
}

const (
	routeREDCountExpr = "sum{by} (rate({sel}[{win}]))"
	routeREDQExpr     = "histogram_quantile(%s, sum by (le) (rate({sel}[{win}])))"
)

// routeREDVMQueries — SAF: rota süzgeçli üç gruplu sorgu (topk YOK). Durum
// etiketi yoksa sayım kırılımsız (5xx çizgisi yok, not çağıranda).
func routeREDVMQueries(fam redFamily, service, svcLabel, envMatcher, statusLabel, route, method string, winS int) []routeREDVMQuery {
	base := metricChartQueryInput{Service: service, SvcLabel: svcLabel, EnvMatcher: svcRouteMatcher(fam, envMatcher, route, method),
		GroupedNoTopK: true, WinS: winS}
	cnt := base
	cnt.Variant = metricChartVariant{Probe: fam.Probe, Kind: "count", Names: []string{fam.names()}, Expr: routeREDCountExpr}
	if statusLabel != "" {
		cnt.By = []string{statusLabel}
	}
	scale := ""
	if fam.Unit != "ms" {
		scale = " * 1000" // s → ms (ailenin beyan ettiği birim)
	}
	q := func(phi string) string {
		in := base
		in.Variant = metricChartVariant{Probe: fam.Probe, Kind: "bucket", Names: []string{fam.names()}, Expr: fmt.Sprintf(routeREDQExpr, phi)}
		return buildMetricChartPromQL(in) + scale
	}
	return []routeREDVMQuery{
		{Kind: "count", Query: buildMetricChartPromQL(cnt)},
		{Kind: "p50", Query: q("0.5")},
		{Kind: "p95", Query: q("0.95")},
	}
}

// routeREDThroughputVM — SAF: durum kırılımlı sayım serileri → "toplam" +
// "5xx" (nokta başına toplam; hata = ailenin sunucu-hatası kümesi). Durum
// etiketi yoksa yalnız toplam.
func routeREDThroughputVM(fam redFamily, statusLabel string, raw []chstore.SpanMetricSeries) []routeREDLine {
	total, errs := map[int64]float64{}, map[int64]float64{}
	for _, s := range raw {
		isErr := false
		if statusLabel != "" {
			if code, ok := briefingGroupKey(s.GroupKey, statusLabel); ok && code != "" {
				isErr = fam.IsServerError(code)
			}
		}
		for _, p := range s.Points {
			total[p.Time] += p.Value
			if isErr {
				errs[p.Time] += p.Value
			}
		}
	}
	if len(total) == 0 {
		return nil
	}
	out := []routeREDLine{{Name: "toplam", Points: sortedPoints(total, nil)}}
	if statusLabel != "" {
		out = append(out, routeREDLine{Name: "5xx", Points: sortedPoints(errs, total)})
	}
	return out
}

// sortedPoints — SAF: zaman → değer haritası, zaman artan; keys verilirse o
// zamanların hepsi (eksik = 0) — 5xx çizgisi toplamla aynı ızgarada.
func sortedPoints(m, keys map[int64]float64) []chstore.SpanMetricPoint {
	src := m
	if keys != nil {
		src = keys
	}
	ts := make([]int64, 0, len(src))
	for t := range src {
		ts = append(ts, t)
	}
	slices.Sort(ts)
	out := make([]chstore.SpanMetricPoint, 0, len(ts))
	for _, t := range ts {
		out = append(out, chstore.SpanMetricPoint{Time: t, Value: m[t]})
	}
	return out
}

// routeREDLatencyVM — SAF: p50 + p95 tekil serileri (NaN noktası düşer).
func routeREDLatencyVM(p50, p95 []chstore.SpanMetricSeries) []routeREDLine {
	var out []routeREDLine
	for _, l := range []struct {
		name string
		raw  []chstore.SpanMetricSeries
	}{{"p50", p50}, {"p95", p95}} {
		var pts []chstore.SpanMetricPoint
		for _, s := range l.raw {
			for _, p := range s.Points {
				if !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) {
					pts = append(pts, p)
				}
			}
			break // kırılımsız ifade: tek seri
		}
		if len(pts) > 0 {
			out = append(out, routeREDLine{Name: l.name, Points: pts})
		}
	}
	return out
}

// routeREDFromCH — SAF: MV kovaları → aynı iki grafik (req/s = çağrı / adım).
func routeREDFromCH(points []chstore.RoutePoint, stepS int) (thr, lat []routeREDLine) {
	if len(points) == 0 {
		return nil, nil
	}
	if stepS < 1 {
		stepS = 60
	}
	tot := routeREDLine{Name: "toplam"}
	e5 := routeREDLine{Name: "5xx"}
	p50 := routeREDLine{Name: "p50"}
	p95 := routeREDLine{Name: "p95"}
	for _, p := range points {
		ns := p.TimeMs * 1e6
		tot.Points = append(tot.Points, chstore.SpanMetricPoint{Time: ns, Value: float64(p.Calls) / float64(stepS)})
		e5.Points = append(e5.Points, chstore.SpanMetricPoint{Time: ns, Value: float64(p.Errors) / float64(stepS)})
		p50.Points = append(p50.Points, chstore.SpanMetricPoint{Time: ns, Value: p.P50Ms})
		p95.Points = append(p95.Points, chstore.SpanMetricPoint{Time: ns, Value: p.P95Ms})
	}
	return []routeREDLine{tot, e5}, []routeREDLine{p50, p95}
}

// routeREDKey — SAF: tüm girdiler tek FNV özetinde (NUL ayraçlı), v0.5.187.
func routeREDKey(service, route, method, env, src string, fromMin, toMin int64) string {
	h := fnv.New64a()
	for _, p := range []string{service, route, method, env, src, fmt.Sprint(fromMin), fmt.Sprint(toMin)} {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("cosre:routered:v1:%x", h.Sum64())
}

func (s *Server) getCosreRouteRED(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	service, route := strings.TrimSpace(q.Get("service")), strings.TrimSpace(q.Get("route"))
	method, env := strings.ToUpper(strings.TrimSpace(q.Get("method"))), strings.TrimSpace(q.Get("env"))
	if service == "" || route == "" || len(service)+len(route)+len(method)+len(env) > routeREDArgMax || (method != "" && !isHTTPVerb(method)) {
		writeJSONError(w, http.StatusBadRequest, "service and route are required")
		return
	}
	from, to := parseFromTo(r, time.Hour)
	if !to.After(from) {
		writeJSONError(w, http.StatusBadRequest, "from must be before to")
		return
	}
	if to.Sub(from) > routeREDMaxRange {
		from = to.Add(-routeREDMaxRange)
	}
	from, to = from.Truncate(time.Minute), to.Truncate(time.Minute)
	_, isVM := s.metricChartReaderFor()
	src := routeREDSourceFor(isVM)
	key := routeREDKey(service, route, method, env, src, from.Unix()/60, to.Unix()/60)
	s.serveCached(w, r, key, routeREDTTL, func(ctx context.Context) (any, error) {
		return s.readRouteRED(ctx, service, route, method, env, from, to)
	})
}

// readRouteRED — kaynak kararı + okuma (VM ya da CH route MV).
func (s *Server) readRouteRED(ctx context.Context, service, route, method, env string, from, to time.Time) (routeREDResponse, error) {
	step := routeREDStepS(int64(to.Sub(from).Seconds()))
	rd0, isVM := s.metricChartReaderFor()
	rd, _ := rd0.(redFamilyReader)
	if isVM && rd != nil {
		return s.readRouteREDVM(ctx, rd, service, route, method, env, from, to, step)
	}
	out := routeREDResponse{Source: routeREDSrcCH, StepS: step}
	pts, err := s.store.RouteREDSeries(ctx, service, route, from, to, step)
	if err != nil {
		return out, err
	}
	out.Throughput, out.Latency = routeREDFromCH(pts, step)
	if env != "" {
		out.Notes = append(out.Notes, fmt.Sprintf("ortam %q süzgeci UYGULANMADI (spanmetrics_1m ortam boyutu taşımaz) — seriler TÜM ortamları kapsıyor", env))
	}
	if method != "" {
		out.Notes = append(out.Notes, fmt.Sprintf("%s süzgeci UYGULANMADI (spanmetrics_1m rota başına toplar) — rota tüm fiilleri kapsıyor", method))
	}
	return out, nil
}

func (s *Server) readRouteREDVM(ctx context.Context, rd redFamilyReader, service, route, method, env string, from, to time.Time, step int) (routeREDResponse, error) {
	out := routeREDResponse{Source: routeREDSrcVM, StepS: step}
	cctx, cancel := context.WithTimeout(ctx, routeREDBudget)
	defer cancel()
	probe := svcHTTPREDProbe{VM: true, GRPCChecked: true}
	in, ok := s.resolveSvcREDInputs(cctx, rd, service, env, to, &probe)
	skip, err := endpointMetricSkip(probe, in, ok)
	if err != nil {
		return out, err
	}
	if skip != "" {
		out.Skip = skip
		return out, nil
	}
	out.Notes = append(out.Notes, in.notes...)
	if method != "" && !in.methodLabelOK {
		out.Notes = append(out.Notes, fmt.Sprintf("histogram %s etiketini taşımıyor — %s süzgeci UYGULANMADI, rota tüm fiilleri kapsıyor", in.fam.MethodLabel, method))
		method = ""
	}
	qs := routeREDVMQueries(in.fam, service, in.svcLabel, in.envMatcher, in.statusLabel, route, method, metricChartWinS(step))
	raw := make([][]chstore.SpanMetricSeries, len(qs))
	errs := make([]error, len(qs))
	var wg sync.WaitGroup
	for i := range qs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			qctx, qcancel := context.WithTimeout(cctx, metricChartQueryTimeout)
			defer qcancel()
			raw[i], errs[i] = rd.QueryPromQLRange(qctx, qs[i].Query, from, to, step, routeREDMaxPoints+1)
		}(i)
	}
	wg.Wait()
	if errs[0] != nil {
		return out, fmt.Errorf("VictoriaMetrics sorgusu BAŞARISIZ (okunamadı ≠ yok): %w", errs[0])
	}
	out.Throughput = routeREDThroughputVM(in.fam, in.statusLabel, raw[0])
	if errs[1] != nil || errs[2] != nil {
		out.Notes = append(out.Notes, "gecikme okunamadı (okunamadı ≠ yok)")
	} else {
		out.Latency = routeREDLatencyVM(raw[1], raw[2])
	}
	return out, nil
}

// ── Sohbet tarafı ─────────────────────────────────────────────────────

// routeTurn — rota inceleme turu (servis + fiil? + route).
type routeTurn struct {
	Service, Method, Route string
}

// routeTurnOf — SAF: sorudaki ilk rota ("FİİL /yol" ya da "/yol") + servis
// (sorudaki canlı ad önce, yoksa bağlam). Servis ya da rota yoksa ok=false.
func routeTurnOf(question, svcFromText, ctxService string) (routeTurn, bool) {
	m := endpointPathRe.FindStringSubmatchIndex(question)
	if m == nil {
		return routeTurn{}, false
	}
	route := strings.TrimRight(question[m[2]:m[3]], ".,;:!?")
	if len(route) > 1 {
		route = strings.TrimRight(route, "/")
	}
	svc := svcFromText
	if svc == "" {
		svc = strings.TrimSpace(ctxService)
	}
	if svc == "" || route == "" || route == "/" {
		return routeTurn{}, false
	}
	method := ""
	if f := strings.Fields(question[:m[2]]); len(f) > 0 && isHTTPVerb(strings.Trim(f[len(f)-1], "*`")) {
		method = strings.ToUpper(strings.Trim(f[len(f)-1], "*`"))
	}
	return routeTurn{Service: svc, Method: method, Route: route}, true
}

// routeChartSpecs — SAF: iki grafik bloğunun spec'i (mutlak pencere, kaynak etiketi).
func routeChartSpecs(rt routeTurn, env, src string, from, to time.Time) []guidedChartSpec {
	base := guidedChartSpec{Service: rt.Service, Source: routeChartSource, Route: rt.Route, Method: rt.Method, Env: env, Src: src,
		RangeS: int64(to.Sub(from).Seconds()), FromNs: from.UnixNano(), ToNs: to.UnixNano()}
	thr, lat := base, base
	thr.Title, thr.Agg = "Throughput", "rate"
	lat.Title, lat.Agg = "Response time", "p95"
	return []guidedChartSpec{thr, lat}
}

// routeTurnChips — SAF: rota cevabının takip çipleri (mockup sırası). Çipte
// kısa etiket, tıkta yönlenen tam cümle (servis + route taşır). En yavaş aşağı
// bağımlılık yoksa o çip yok.
func routeTurnChips(rt routeTurn, slowest string) []chatFollowup {
	op := strings.TrimSpace(rt.Method + " " + rt.Route)
	out := []chatFollowup{
		{Kind: "endpoint", Label: "Hatalı trace'leri aç", Text: fmt.Sprintf("%s %s hatalı trace'lerini getir", rt.Service, rt.Route)},
	}
	if slowest != "" {
		// Ek ünlü uyumu yazımdan çıkarılamaz ("core" → 'u): ek yerine "servisini".
		out = append(out, chatFollowup{Kind: "endpoint", Label: slowest + " servisini incele", Text: slowest + " servisini incele"})
	}
	return append(out,
		chatFollowup{Kind: "endpoint", Label: "Son deploy'la karşılaştır", Text: fmt.Sprintf("%s %s son deploy'la karşılaştır", rt.Service, op)},
		chatFollowup{Kind: "endpoint", Label: "Başka endpoint seç", Text: rt.Service + " için başka endpoint seç"},
	)
}

// withRouteChips — SAF: rota çipleri önce, döngünün çipleri ardından (metne göre tekil).
func withRouteChips(route, loop []chatFollowup) []chatFollowup {
	if len(route) == 0 {
		return loop
	}
	seen := map[string]bool{}
	out := make([]chatFollowup, 0, len(route)+len(loop))
	for _, c := range append(append([]chatFollowup(nil), route...), loop...) {
		if seen[c.Text] {
			continue
		}
		seen[c.Text] = true
		out = append(out, c)
	}
	return out
}

// slowCalleeNameRe — çipe girecek ad (servis adı karakterleri).
var slowCalleeNameRe = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,80}$`)

// pickSlowestService — SAF: en yavaş SERVİS bağımlılığı (db / kuyruk / dış uç değil).
func pickSlowestService(cs []chstore.CalleeLatency) string {
	for _, c := range cs {
		if c.Kind == chstore.NodeKindService && slowCalleeNameRe.MatchString(c.Callee) {
			return c.Callee
		}
	}
	return ""
}

// routeTurnPlan — v2 rota turunun sohbet yükü: grafik spec'leri, ek, çipler.
type routeTurnPlan struct {
	Turn     routeTurn
	Specs    []guidedChartSpec
	Addendum string
	Chips    []chatFollowup
}

// planRouteTurn — v2: soru (ya da kartın İncele turu) bir servis + route
// taşıyorsa grafik spec'leri (kaynak VM/CH), rota ek ve çipler. Okuma: servis
// kataloğu (60 sn önbellek) + en yavaş aşağı bağımlılık (topology_edges_5m).
func (s *Server) planRouteTurn(ctx context.Context, question, ctxService, env string, rangeS int64, anchorTo time.Time) (*routeTurnPlan, bool) {
	if !endpointPathRe.MatchString(question) {
		return nil, false // ucuz ön kapı: katalog okunmadan
	}
	svc := extractServiceEntity(normalizeGuidedMsg(question), s.guidedServiceNames(ctx), s.guidedEnvNames(ctx))
	rt, ok := routeTurnOf(question, svc, ctxService)
	if !ok {
		return nil, false
	}
	if v, explicit := guidedRangeSExplicit(normalizeGuidedMsg(question)); explicit && v > 0 {
		rangeS = v
	}
	if rangeS <= 0 {
		rangeS = endpointPickDefaultRangeS
	}
	to := anchorTo
	if to.IsZero() {
		to = time.Now()
	}
	to = to.Truncate(time.Minute)
	from := to.Add(-time.Duration(rangeS) * time.Second)
	_, isVM := s.metricChartReaderFor()
	src := routeREDSourceFor(isVM)
	slowest := ""
	if cs, err := s.store.SlowestCallees(ctx, rt.Service, from, to, 5); err == nil {
		slowest = pickSlowestService(cs)
	}
	return &routeTurnPlan{Turn: rt, Specs: routeChartSpecs(rt, env, src, from, to),
		Addendum: copilot.ChatRouteInvestigationAddendum(rt.Service, strings.TrimSpace(rt.Method+" "+rt.Route), rt.Route, src, trRangeLocative(rangeS)),
		Chips:    routeTurnChips(rt, slowest)}, true
}
