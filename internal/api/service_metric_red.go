package api

// service_metric_red.go — v0.10.337: Service Overview RED'i METRİKTEN
// (VM ana metrik deposu, dilim 2).
//
// Operatör (2026-09-04): "service overview'da en üstte response throughput
// trace sürelerini de Victoria'ya bağlayabilir miyiz". Response time (avg)
// ve Throughput karoları v0.9.798'den beri metrikten (v0.9.1268 ile VM'den)
// okuyor; P50/P95/P99, latency grafiği ve Failure rate SPAN türevliydi —
// sebep eski bir CH sınırı (metric_points kova sınırı taşımıyordu). VM'de
// `_bucket` serileri var; dikiş (metricsource.go) p50/p95/p99'u
// histogram_quantile ile üretiyor.
//
// GET /api/services/{name}/metric-red → spanMetricBatch ile AYNI seri
// haritası (rate · error_rate · p50 · p95 · p99 · avg) ki Overview aynı
// tüketicilerle çizsin:
//   - rate       req/s (rate modu, rateWindow yumuşatması)
//   - error_rate YÜZDE (span sözleşmesi: 100 × err/total), 5xx =
//                http.response.status_code / http.status_code; etiket yoksa
//                seri YOK + errorsUnknown (0 değil)
//   - p50/p95/p99/avg ms (kaynak birimi ya da `_seconds` adından); birim
//                bilinmiyorsa seriler YOK + latencyUnitKnown=false — tahmin
//                yok, karo span'e düşer ve etiketi söyler
// Kimlik ve ad: httpMetricIdentity (tput bağı → service_name;
// service.throughput_metric + adaylar) — MCP query_metrics de aynısını
// okur (mcp_deps.go). Her okuma dikişten; hiçbir s.store.<metric> çağrısı
// yok. Varsayılan DEĞİŞMEDİ: Overview ?src=metric ile bu uca geçer.
// v0.10.1361 — /api/endpoints/metric silindi; kimlik ve birim yardımcıları
// (httpMetricIdentity, latencyScaleMs) buraya taşındı.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("service-metric-red", (*Server).registerServiceMetricREDRoutes) }

// registerServiceMetricREDRoutes — metric-throughput ile aynı duruş (çıplak,
// okuma-yalnız; kimlikli her rol).
func (s *Server) registerServiceMetricREDRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/metric-red", s.getServiceMetricRED)
}

type serviceMetricREDPlan struct {
	Service  string
	From, To time.Time
	Mdp      int
	RateWin  int
	Env      string
	// v0.10.1377 — VM kaynağında env'in canlı değerlere çözümü (handler /
	// MCP çözer, buildServiceMetricRED SAF kalır). nil → eski yol
	// (src.EnvFilterExpr: CH süzer, VM reddeder).
	EnvScope *httpEnvScope
	// Family — v0.10.1429: "" = bugünkü HTTP kimliği; "grpc" = gRPC sunucu
	// histogramı (ilk var olan aile: rpc.server.duration ms / rpc.server.call.duration s).
	// Overview, aile çözücüsü (GET /red-family) gRPC derse bunu geçer.
	Family string
}

// v0.10.1350 — v2: zarf statusState/statusReason taşır, hata veri güdümlü.
// v0.10.1377 — v3: VM'de env canlı değerlere çözülüp süzülür; zarf envScope
// taşır. lm = LabelMap'in env/cluster etiketlerinin özeti (Settings'te etiket
// değişirse eski süzgeç servis edilmesin); CH'de "-".
// v0.10.1429 — v4: aile anahtarda (fam=); zarf family/familyId/routeLabel taşır.
func serviceMetricREDKey(p serviceMetricREDPlan, srcName, mx, lm string) string {
	return fmt.Sprintf("svc-metric-red:v4:src=%s:%s:%s:mdp%d:rw%d:env=%s:mx=%s:lm=%s:fam=%s",
		srcName, p.Service, cacheBucket(p.From, p.To), p.Mdp, p.RateWin, p.Env, mx, lm, p.Family)
}

// serviceMetricREDLabelDigest — v0.10.1377: anahtarın lm bileşeni.
func serviceMetricREDLabelDigest(src metricSource) string {
	if r, ok := src.(httpEnvLabelReader); ok {
		lm := r.MetricLabelMap().Normalized()
		return fnvStr(lm.Env, lm.Cluster)
	}
	return "-"
}

// serviceMetricREDEnvScope — v0.10.1377: VM kaynağında env'i HTTP histogramının
// ortam etiketine çözer (HTTP Metrics sekmesi / endpoint / liste / Overview
// karolarıyla AYNI çözücü ve aynı önbellekli değerler → iki yüzey aynı değere
// süzülür, "ikili hâl" yok). CH kaynağı ya da boş env → nil (eski yol).
// Okuma hatası → Failed kapsamı (süzgeç uygulanmaz, not söyler).
func (s *Server) serviceMetricREDEnvScope(ctx context.Context, src metricSource, env string, to time.Time) *httpEnvScope {
	return s.serviceMetricREDEnvScopeFor(ctx, src, env, to, "")
}

// serviceMetricREDEnvScopeFor — v0.10.1429: metric "" = HTTP histogramı (bayt
// bayt eski); gRPC kimliğinde o ailenin metriği (ortam etiketi ve canlı
// değerleri kendi serilerinden).
func (s *Server) serviceMetricREDEnvScopeFor(ctx context.Context, src metricSource, env string, to time.Time, metric string) *httpEnvScope {
	r, ok := src.(httpEnvLabelReader)
	if !ok || strings.TrimSpace(env) == "" {
		return nil
	}
	var sc httpEnvScope
	var err error
	if metric == "" {
		sc, err = s.resolveHTTPEnvScope(ctx, r, map[string]string{"env": env}, to)
	} else {
		sc, err = s.resolveMetricEnvScope(ctx, r, metric, map[string]string{"env": env}, to)
	}
	if err != nil {
		f := httpEnvScopeFailed(err)
		return &f
	}
	return &sc
}

// serviceMetricREDResponse — zarf. `series` anahtarları spanMetricBatch'inki.
type serviceMetricREDResponse struct {
	Service          string                                `json:"service"`
	Source           string                                `json:"source"`
	Metric           string                                `json:"metric,omitempty"`
	RTMetric         string                                `json:"rtMetric,omitempty"`
	Instrument       string                                `json:"instrument,omitempty"`
	MatchedBy        string                                `json:"matchedBy,omitempty"`
	MetricExists     bool                                  `json:"metricExists"`
	StepSeconds      int                                   `json:"stepSeconds"`
	StatusKey        string                                `json:"statusKey,omitempty"`
	ErrorsUnknown    bool                                  `json:"errorsUnknown,omitempty"`
	StatusState      statusLabelState                      `json:"statusState,omitempty"`  // v0.10.1350
	StatusReason     string                                `json:"statusReason,omitempty"` // v0.10.1350
	LatencyUnit      string                                `json:"latencyUnit,omitempty"`
	LatencyUnitKnown bool                                  `json:"latencyUnitKnown"`
	LatencyUnitFrom  string                                `json:"latencyUnitFrom,omitempty"`
	EnvAmbiguous     bool                                  `json:"envAmbiguous,omitempty"`
	EnvScope         *httpEnvScope                         `json:"envScope,omitempty"` // v0.10.1377
	Tried            []string                              `json:"tried,omitempty"`
	Partial          []string                              `json:"partial,omitempty"`
	Note             string                                `json:"note"`
	Series           map[string][]chstore.SpanMetricSeries `json:"series"`
	// v0.10.1429 — aile: "http" | "grpc"; FamilyID katalog kimliği; RouteLabel
	// route eşdeğeri OTel anahtarı (http.route / rpc.method — Overview'ın
	// route kırılımı buna göre). Boş = eski gövde = HTTP.
	Family     string `json:"family,omitempty"`
	FamilyID   string `json:"familyId,omitempty"`
	RouteLabel string `json:"routeLabel,omitempty"`
}

// seriesStep — noktalar arası en küçük pozitif aralık (sn); tek nokta → 0.
func seriesStep(ser []chstore.SpanMetricSeries) int {
	best := int64(0)
	for _, s := range ser {
		for i := 1; i < len(s.Points); i++ {
			d := (s.Points[i].Time - s.Points[i-1].Time) / 1e9
			if d > 0 && (best == 0 || d < best) {
				best = d
			}
		}
	}
	return int(best)
}

// errorRatePercent — SAF: rate ve 5xx-rate serilerinden YÜZDE serisi
// (zaman damgasıyla hizalı; hata noktası yoksa 0 — "hata yok"; rate 0 ise
// nokta atlanır — 0/0 yüzde değildir).
func errorRatePercent(rate, errs []chstore.SpanMetricSeries) []chstore.SpanMetricSeries {
	if len(rate) == 0 {
		return []chstore.SpanMetricSeries{}
	}
	errAt := map[int64]float64{}
	for _, s := range errs {
		for _, p := range s.Points {
			errAt[p.Time] += p.Value
		}
	}
	// v0.10.842 — Operator-reported: boşta servis (rate hep 0) → hiç nokta
	// eklenmez, nil dilim JSON'a `null` yazılır, Overview .map üstünde düşer.
	// Seri varsa points DİZİDİR (/api-route ev kuralı: nil → []T{}).
	out := chstore.SpanMetricSeries{GroupKey: []string{}, Points: []chstore.SpanMetricPoint{}}
	for _, s := range rate {
		for _, p := range s.Points {
			if p.Value <= 0 || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			e := errAt[p.Time]
			if e < 0 || math.IsNaN(e) || math.IsInf(e, 0) {
				e = 0
			}
			pct := e / p.Value * 100
			if pct > 100 {
				pct = 100
			}
			out.Points = append(out.Points, chstore.SpanMetricPoint{Time: p.Time, Value: pct})
		}
	}
	return []chstore.SpanMetricSeries{out}
}

// sumSeriesByTime — SAF (v0.10.1350): durum kırılımlı serileri zaman
// damgasında toplar → TEK grupsuz seri (eski durumsuz sorgunun şekli).
// NaN/Inf atlanır; seri yoksa boş dilim (JSON `[]`, asla null).
func sumSeriesByTime(ser []chstore.SpanMetricSeries) []chstore.SpanMetricSeries {
	if len(ser) == 0 {
		return []chstore.SpanMetricSeries{}
	}
	at := map[int64]float64{}
	for _, s := range ser {
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			at[p.Time] += p.Value
		}
	}
	out := chstore.SpanMetricSeries{Points: make([]chstore.SpanMetricPoint, 0, len(at))}
	for t, v := range at {
		out.Points = append(out.Points, chstore.SpanMetricPoint{Time: t, Value: v})
	}
	sort.Slice(out.Points, func(i, j int) bool { return out.Points[i].Time < out.Points[j].Time })
	return []chstore.SpanMetricSeries{out}
}

// scaleSeries — değerleri ms'e çevirir (kopya; kaynak serisi mutasyona uğramaz).
func scaleSeries(ser []chstore.SpanMetricSeries, scale float64) []chstore.SpanMetricSeries {
	out := make([]chstore.SpanMetricSeries, 0, len(ser))
	for _, s := range ser {
		c := chstore.SpanMetricSeries{GroupKey: s.GroupKey, Points: make([]chstore.SpanMetricPoint, 0, len(s.Points))}
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			c.Points = append(c.Points, chstore.SpanMetricPoint{Time: p.Time, Value: p.Value * scale})
		}
		out = append(out, c)
	}
	return out
}

// httpMetricIdentity — hangi metrik, hangi kimlik.
// v0.10.1364 — eski adı endpointsMetricIdentity (silinen uçtan kalma); tüketicilere göre yeniden adlandırıldı.
// v0.10.1361 — endpoints_metric.go'dan taşındı (o uç silindi; tüketiciler
// buildServiceMetricRED ve MCP query_metrics).
type httpMetricIdentity struct {
	Metric     string // throughput ailesi (VM'de `_count` yazımı olabilir)
	RTMetric   string // değer okuyan ad (avg / yüzdelik) — LatencyMetricName
	Instrument string // sum | histogram
	Service    string // service_name eşleşmesi (bağ etiketliyse boş)
	Filters    []chstore.FilterExpr
	MatchedBy  string
	Tried      []string
	// Family — v0.10.1429: nil = HTTP (durum anahtarları httpStatusKeyCandidates,
	// hata isHTTP5xx); gRPC kimliğinde ailenin anahtarları ve hata kümesi.
	Family *redFamily
}

// grpcMetricIdentity — v0.10.1429: /metric-red?family=grpc kimliği — ilk var
// olan gRPC ailesi (VM'de tam taban, CH'de OTel adı), servis service_name ile,
// instrument histogram (kaynak başka derse o). Yoksa ok=false + denenenler.
func grpcMetricIdentity(ctx context.Context, src metricSource, service string) (httpMetricIdentity, bool) {
	fam, name, ok, tried := redFamilyIdentity(ctx, src)
	id := httpMetricIdentity{Service: service, MatchedBy: "service_name", Tried: tried}
	if !ok {
		return id, false
	}
	f := fam
	id.Family, id.Metric, id.RTMetric = &f, name, src.LatencyMetricName(name)
	id.MatchedBy = "family:" + fam.ID
	id.Instrument = src.MetricInstrument(ctx, name, service)
	if id.Instrument == "" {
		id.Instrument = src.MetricInstrument(ctx, name, "")
	}
	if id.Instrument == "" {
		id.Instrument = "histogram" // OTel rpc.server.* histogramdır; kaynak susarsa sözleşme
	}
	return id, true
}

func (s *Server) httpMetricIdentity(ctx context.Context, src metricSource, service, env string) (httpMetricIdentity, bool) {
	id := httpMetricIdentity{Service: service, MatchedBy: "service_name"}
	if service != "" {
		// Service Overview'un çözdüğü kimlik (job/name etiketi, `_count`
		// yazımı, instrument) — keşfi tekrarlama.
		if b := s.loadTputBinding(ctx, service, env, src.Name()); b != nil && !b.None && b.Metric != "" {
			id.Metric, id.Instrument = b.Metric, b.Instrument
			id.Service, id.Filters = b.Service, b.Filters
			id.MatchedBy = "binding:" + b.MatchedBy
			id.RTMetric = src.LatencyMetricName(b.Metric)
			return id, true
		}
	}
	resolved, tried := s.resolveThroughputMetric(ctx, src, "")
	id.Tried = tried
	if resolved == "" {
		return id, false
	}
	id.Metric = resolved
	id.RTMetric = src.LatencyMetricName(resolved)
	id.Instrument = src.MetricInstrument(ctx, resolved, service)
	if id.Instrument == "" {
		id.Instrument = src.MetricInstrument(ctx, resolved, "")
	}
	return id, true
}

// latencyScaleMs — birim → ms çarpanı. Kaynak birimi önce; yoksa Prometheus
// ad sözleşmesi (`_seconds` / `_milliseconds`); o da yoksa 1 + bilinmiyor.
// v0.10.1361 — endpoints_metric.go'dan taşındı.
func latencyScaleMs(unit, metric string) (scale float64, known bool, from string) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "s", "seconds":
		return 1000, true, "source"
	case "ms", "milliseconds":
		return 1, true, "source"
	case "us", "microseconds":
		return 0.001, true, "source"
	case "ns", "nanoseconds":
		return 1e-6, true, "source"
	}
	n := strings.ToLower(metric)
	switch {
	case strings.Contains(n, "milliseconds"):
		return 1, true, "name"
	case strings.Contains(n, "seconds"):
		return 1000, true, "name"
	}
	return 1, false, ""
}

// buildServiceMetricRED — SAF (kaynak arayüz): sorgular → seri haritası.
func buildServiceMetricRED(ctx context.Context, src metricSource, id httpMetricIdentity, p serviceMetricREDPlan) (serviceMetricREDResponse, error) {
	resp := serviceMetricREDResponse{
		Service: p.Service, Source: src.Name(),
		Metric: id.Metric, RTMetric: id.RTMetric, Instrument: id.Instrument, MatchedBy: id.MatchedBy,
		MetricExists: true,
		Series:       map[string][]chstore.SpanMetricSeries{},
	}
	// v0.10.1429 — aile: durum anahtarları ve hata kümesi aileden (HTTP bayt bayt eski).
	fam := redFamilies[0]
	if id.Family != nil {
		fam = *id.Family
		resp.Family, resp.FamilyID, resp.RouteLabel = fam.Kind, fam.ID, fam.RouteKey
	}
	base := chstore.MetricQueryFilter{
		Name: id.Metric, Service: id.Service, Filters: id.Filters,
		From: p.From, To: p.To, MaxDataPoints: p.Mdp, RateWindowSec: p.RateWin,
	}
	switch {
	case p.Env != "" && p.EnvScope != nil:
		// v0.10.1377 — VM: canlı değerlere çözülmüş ortam etiketi.
		resp.EnvScope = p.EnvScope
		if fs := httpEnvScopeFilters(*p.EnvScope); len(fs) > 0 {
			base.Filters = append(append([]chstore.FilterExpr(nil), base.Filters...), fs...)
		} else {
			resp.EnvAmbiguous = true
		}
	case p.Env != "":
		_, applied := src.EnvFilterExpr(p.Env)
		resp.EnvAmbiguous = !applied
		base = withEnvFilter(base, p.Env, src)
	}
	unit := src.MetricUnit(ctx, id.RTMetric, p.Service)
	scale, unitKnown, unitFrom := latencyScaleMs(unit, id.RTMetric)
	resp.LatencyUnit, resp.LatencyUnitKnown, resp.LatencyUnitFrom = unit, unitKnown, unitFrom
	if unit == "" && unitKnown {
		resp.LatencyUnit = "s"
		if scale == 1 {
			resp.LatencyUnit = "ms"
		}
	}

	rate := src.QueryMetricRate
	if id.Instrument == "histogram" {
		rate = src.QueryMetricCountRate
	}
	// v0.10.1350 — veri güdümlü durum yoklaması (http_labels.go): rate
	// sorgusu durum adayıyla gruplanır; rate = durumların toplamı, hata =
	// 5xx serileri. Ayrı 5xx sorgusu ve 7 günlük etiket taraması yok.
	probe := probeStatusLabelKeys(func(key string) ([]chstore.SpanMetricSeries, error) {
		f := base
		f.GroupBy = []string{key}
		return statusRowsChecked(rate(ctx, f, "rate"))
	}, 0, fam.StatusKeys)
	resp.StatusState, resp.StatusKey = probe.State, probe.Key
	resp.ErrorsUnknown = probe.State != statusLabelPresent
	byStatus := probe.Series
	if probe.State == statusLabelUnknown {
		resp.StatusReason = probe.Reason
		if byStatus == nil { // ilk aday hata verdi: rate durumsuz sorgudan
			var err error
			byStatus, err = rate(ctx, base, "rate")
			if err != nil {
				return resp, err
			}
		}
	}
	rateSer := sumSeriesByTime(byStatus)
	resp.Series["rate"] = rateSer
	resp.StepSeconds = seriesStep(rateSer)
	if probe.State == statusLabelPresent {
		var errSer []chstore.SpanMetricSeries
		for _, sr := range byStatus {
			if len(sr.GroupKey) > 0 && fam.IsServerError(sr.GroupKey[0]) {
				errSer = append(errSer, sr)
			}
		}
		resp.Series["error_rate"] = errorRatePercent(rateSer, errSer)
	}
	if unitKnown {
		for _, ag := range []string{"p50", "p95", "p99", "avg"} {
			vf := base
			vf.Name = id.RTMetric
			vf.Aggregation = ag
			ser, verr := src.QueryMetric(ctx, vf)
			if verr != nil {
				resp.Partial = append(resp.Partial, ag+": "+verr.Error())
				continue
			}
			resp.Series[ag] = scaleSeries(ser, scale)
		}
	}
	resp.Note = serviceMetricREDNote(resp)
	return resp, nil
}

// serviceMetricREDNote — kapsam rozeti + tooltip metni.
func serviceMetricREDNote(r serviceMetricREDResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kaynak: METRİK (%s)", r.Source)
	if !r.MetricExists {
		b.WriteString(" — metrik bulunamadı")
		if len(r.Tried) > 0 {
			b.WriteString(" (denenen: " + strings.Join(r.Tried, ", ") + ")")
		}
		b.WriteString("; karolar ve grafikler span'a düşer.")
		return b.String()
	}
	fmt.Fprintf(&b, " — %s, kimlik %s.", r.Metric, r.MatchedBy)
	fam, _ := redFamilyByID(r.FamilyID) // v0.10.1429 — boş/bilinmeyen → HTTP (eski cümleler)
	if r.Family == redKindGRPC {
		b.WriteString(" Aile: gRPC sunucu histogramı (servis HTTP sunucu histogramı yazmıyor); route kırılımı rpc.method.")
	}
	b.WriteString(statusLabelNoteFamily(r.StatusState, r.StatusKey, r.StatusReason, r.Source, "Failure rate", "", fam))
	if r.LatencyUnitKnown {
		// v0.10.1360 — grafik = adım serisi, karo = pencere değeri (Overview
		// karoları artık HTTP Metrics'in windowQueries'inden okur).
		fmt.Fprintf(&b, " P50/P95/P99: grafik serileri adım başına histogram_quantile(rate[180s]); karolar pencere değeri (increase[W], HTTP/RPC ile aynı); birim %s", r.LatencyUnit)
		if r.LatencyUnitFrom == "name" {
			b.WriteString(" (metrik adından)")
		}
		b.WriteString(".")
	} else {
		b.WriteString(" Süre birimi BİLİNMİYOR: P50/P95/P99 metrikten verilmedi, karo span'a düşer.")
	}
	b.WriteString(serviceMetricREDEnvNote(r))
	if len(r.Partial) > 0 {
		b.WriteString(" Eksik: " + strings.Join(r.Partial, "; ") + ".")
	}
	return b.String()
}

// serviceMetricREDEnvNote — SAF (v0.10.1377): notun env cümlesi (boş = env
// seçili değil ya da CH süzdü).
func serviceMetricREDEnvNote(r serviceMetricREDResponse) string {
	sc := r.EnvScope
	switch {
	case sc != nil && sc.Applied && !r.EnvAmbiguous:
		return fmt.Sprintf(" env süzgeci metrikte %s ile uygulandı.", sc.Matcher)
	case sc != nil && sc.Failed:
		return fmt.Sprintf(" env süzgeci çözülemedi (%s) — süzgeç uygulanmadı, seriler TÜM ortamları kapsıyor.", sc.Reason)
	case sc != nil:
		return fmt.Sprintf(" env süzgeci uygulanamadı (%s) — seriler TÜM ortamları kapsıyor.", sc.Reason)
	case r.EnvAmbiguous:
		return " env filtresi bu depoda ifade edilemiyor, seriler TÜM ortamları kapsıyor."
	}
	return ""
}

// getServiceMetricRED — GET /api/services/{name}/metric-red
func (s *Server) getServiceMetricRED(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErr(w, fmt.Errorf("%w: service required", errBadRequest))
		return
	}
	from, to := parseFromTo(r, time.Hour)
	mdp, _ := strconv.Atoi(r.URL.Query().Get("maxDataPoints"))
	if mdp < 0 {
		mdp = 0
	}
	if mdp > 4000 {
		mdp = 4000
	}
	rateWin, _ := strconv.Atoi(r.URL.Query().Get("rateWindow"))
	if rateWin <= 0 {
		rateWin = 180
	}
	if rateWin > 600 {
		rateWin = 600
	}
	p := serviceMetricREDPlan{Service: name, From: from, To: to, Mdp: mdp, RateWin: rateWin, Env: strings.TrimSpace(r.URL.Query().Get("env"))}
	// v0.10.1429 — ?family=grpc: gRPC sunucu histogramı kimliği; başka değer yok sayılır (400 değil).
	if r.URL.Query().Get("family") == redKindGRPC {
		p.Family = redKindGRPC
	}
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	mx := s.store.MetricExclusions().Digest()
	key := serviceMetricREDKey(p, src.Name(), mx, serviceMetricREDLabelDigest(src))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		var id httpMetricIdentity
		var ok bool
		if p.Family == redKindGRPC {
			id, ok = grpcMetricIdentity(ctx, src, name)
		} else {
			id, ok = s.httpMetricIdentity(ctx, src, name, p.Env)
		}
		if !ok {
			resp := serviceMetricREDResponse{Service: name, Source: src.Name(), Tried: id.Tried, Series: map[string][]chstore.SpanMetricSeries{}}
			if p.Family == redKindGRPC {
				resp.Family = redKindGRPC
			}
			resp.Note = serviceMetricREDNote(resp)
			return resp, nil
		}
		// v0.10.1377 — closure'ın kendi ctx'i (SWR tazelemesi istek bağlamına bağlanmaz).
		pp := p
		if id.Family != nil {
			pp.EnvScope = s.serviceMetricREDEnvScopeFor(ctx, src, p.Env, p.To, id.Family.Probe) // v0.10.1429 — ailenin metriği
		} else {
			pp.EnvScope = s.serviceMetricREDEnvScope(ctx, src, p.Env, p.To)
		}
		return buildServiceMetricRED(ctx, src, id, pp)
	})
}
