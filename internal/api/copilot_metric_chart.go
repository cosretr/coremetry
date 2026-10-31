package api

// copilot_metric_chart.go — v0.10.1386 (CoSRE metrik grafiği; operatör:
// "VictoriaMetrics sorgulayıp grafik getirse mesela").
//
// "orders-api'nin JVM heap kullanımı son 6 saat grafik", "checkout pod'larının
// CPU'sunu çiz", "kafka consumer lag trendi", "5xx oranı cluster bazında
// grafik" → guided `metric_chart` niyeti: şablon kataloğundan
// (metric_chart_templates.go) TEK sınırlı VM range sorgusu, sohbet içi veri
// grafiği (EvChartPayload) + kanıt çipi (sorgu · pencere · seri · süre ·
// "Explore'da aç" = PromQL editörü), deterministik grafik özeti (model noktaları
// yeniden saymaz), takip çipleri. Anlatım SystemPromptGuidedChat (D7 — yeni
// prompt yok).
//
// Kararlar (plan D1–D8):
//   - D1 pencere: açık pencere kazanır (5 dk..24 sa), yoksa 1 sa.
//   - D2 grafik fiili ŞART (grafik/çiz/chart/plot/trend…): fiilsiz "heap
//     kullanımı nasıl" pod_health'te, "db bağlantı havuzu nasıl" db_health'te kalır.
//   - D3 ≤20 seri (topk + sonradan kesme), ≤120 nokta.
//   - D4 VM yok → RED şablonları (5xx/rps/p99/p95) mevcut span spec-chart'a
//     düşer (servis şart), diğerleri dürüst "VM yapılandırılmamış".
//   - D5 cAdvisor pod CPU/bellek Thanos'ta kalır: not + Infrastructure linki.
//   - D8 ortam değerleri HTTP RED metriğine karşı çözülür (resolveHTTPEnvScope).
//
// Salt-okur (TestMetricChartReadOnly): yalnız etiket keşfi + tek range sorgusu;
// yazma, denetim, model çağrısı yok. Önbellek anahtarı TÜM girdileri hash'ler.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// guidedMetricChart — v0.10.1386: router-içi niyet (sınıflandırıcı listesinde yok).
const guidedMetricChart guidedIntent = "metric_chart"

// v0.10.1386 — bütçeler ve önbellek ömürleri.
const (
	metricChartDefaultRangeS = 3600
	metricChartQueryTimeout  = 8 * time.Second
	metricChartLabelTimeout  = 2 * time.Second
	metricChartCacheTTL      = 60 * time.Second
	metricChartLabelsTTL     = 5 * time.Minute
	metricChartLabelsLookbk  = 24 * time.Hour
	metricChartTriedMax      = 10
	metricChartStatsMaxLines = 10
	metricChartEnvChipsMax   = 2
)

// ─── Router (SAF) ───────────────────────────────────────────────────────

// metricChartVerbStems — grafik fiili (D2). "grafi" grafik/grafiği/grafiğini'yi
// birlikte yakalar (ğ/k yumuşaması).
var metricChartVerbStems = []string{"grafi", "graph", "chart", "çiz", "ciz", "plot", "trend", "eğri", "egri", "görselle", "gorselle"}

func hasChartVerb(toks []string) bool { return tokenHasPrefix(toks, metricChartVerbStems...) }

// pickMetricChartTemplateID — SAF: metrik sözcüğü → şablon (özgül önce).
// "kuyruk" BİLEREK yok (operatör dilinde iş listesi; hasMessagingSignal gerekçesi).
// "gc"/"ram" tam jeton ("gcp", "rampa" tuzakları).
func pickMetricChartTemplateID(toks []string) string {
	switch {
	case tokenHasPrefix(toks, "kafka", "consumer", "lag"):
		return mcKafkaLag
	case tokenHasPrefix(toks, "pool", "havuz", "bağlantı", "baglanti", "connection"):
		return mcDBPool
	case tokenHasPrefix(toks, "heap"):
		return mcJVMHeap
	case anyToken(toks, "gc"):
		return mcJVMGC
	case tokenHasPrefix(toks, "thread"):
		return mcThreads
	case tokenHasPrefix(toks, "cpu"):
		return mcCPU
	case tokenHasPrefix(toks, "bellek", "memory") || anyToken(toks, "ram"):
		return mcMemory
	case tokenHasPrefix(toks, "5xx", "hata", "error"):
		return mcHTTP5xx
	case tokenHasPrefix(toks, "p95"):
		return mcHTTPP95
	case tokenHasPrefix(toks, "p99", "latency", "gecikme"):
		return mcHTTPP99
	case tokenHasPrefix(toks, "istek", "rps", "request", "throughput"):
		return mcHTTPRPS
	}
	return ""
}

// hasMetricChartSignal — SAF: grafik fiili VE katalogdaki bir metrik sözcüğü.
func hasMetricChartSignal(toks []string) bool {
	return hasChartVerb(toks) && pickMetricChartTemplateID(toks) != ""
}

// metricChartGroupBy — SAF: "cluster bazında", "ortama göre", "by env",
// "pod başına" → "env" | "pod"; yoksa "".
func metricChartGroupBy(toks []string) string {
	kind := func(t string) string {
		switch {
		case strings.HasPrefix(t, "cluster"), strings.HasPrefix(t, "küme"), strings.HasPrefix(t, "kume"),
			strings.HasPrefix(t, "ortam"), strings.HasPrefix(t, "env"):
			return "env"
		case strings.HasPrefix(t, "pod"):
			return "pod"
		}
		return ""
	}
	for i, t := range toks {
		switch {
		case strings.HasPrefix(t, "bazın") || strings.HasPrefix(t, "bazin") || strings.HasPrefix(t, "bazlı") ||
			strings.HasPrefix(t, "bazli") || t == "göre" || t == "gore" || t == "başına" || t == "basina":
			if i > 0 {
				if k := kind(toks[i-1]); k != "" {
					return k
				}
			}
		case t == "by" || t == "per":
			if i+1 < len(toks) {
				if k := kind(toks[i+1]); k != "" {
					return k
				}
			}
		}
	}
	return ""
}

// routeMetricChart — SAF: metric_chart rotası. Router'ın servis/ortam
// çıkarımından HEMEN sonra, istek/periyot/endpoint dallarından ÖNCE çağrılır:
// "istek hızı grafiği" endpoint aday turuna, "kafka lag trendi" servissiz
// messaging/ask yoluna savrulmasın. Kapı dar: grafik fiili + metrik sözcüğü;
// takım-iyelik ve çift ("A'dan B'ye") şekilleri kendi yollarında kalır.
// Servis isteyen şablonda servis yoksa: tek aday → o; çok aday → ask_service
// (AskIntent metric_chart); ekran servisi canlıysa o; hiçbiri → ask_service.
func routeMetricChart(raw, msg string, toks []string, svc, env string, services, envs []string, ctxService string) (guidedRoute, bool) {
	if !hasMetricChartSignal(toks) || hasTeamSelfSignal(toks) {
		return guidedRoute{}, false
	}
	if _, _, pair := splitPairFragments(raw); pair {
		return guidedRoute{}, false
	}
	id := pickMetricChartTemplateID(toks)
	t, ok := metricChartTemplateByID(id)
	if !ok {
		return guidedRoute{}, false
	}
	r := guidedRoute{Intent: guidedMetricChart, Service: svc, Env: env, ChartTemplate: id,
		ChartGroupBy: metricChartGroupBy(toks), ChartEnvAsked: env != ""}
	if svc != "" || !t.NeedsSvc {
		return r, true
	}
	opts := serviceCandidates(msg, services, envs, guidedServiceAskMax)
	switch {
	case len(opts) == 1:
		r.Service = opts[0]
		return r, true
	case len(opts) > 1:
		return guidedRoute{Intent: guidedAskService, AskIntent: guidedMetricChart, ServiceOptions: opts, Env: env,
			ChartTemplate: id, ChartGroupBy: r.ChartGroupBy, ChartEnvAsked: r.ChartEnvAsked}, true
	}
	for _, s := range services {
		if ctxService != "" && s == ctxService {
			r.Service = ctxService
			return r, true
		}
	}
	return guidedRoute{Intent: guidedAskService, AskIntent: guidedMetricChart, Env: env,
		ChartTemplate: id, ChartGroupBy: r.ChartGroupBy, ChartEnvAsked: r.ChartEnvAsked}, true
}

// metricChartAskChip — SAF: ask_service çipi (kendi başına aynı şablona yönlenir).
func metricChartAskChip(route guidedRoute, svc string) string {
	t, ok := metricChartTemplateByID(route.ChartTemplate)
	if !ok {
		return svc + " sağlığı nasıl?"
	}
	return svc + " " + t.Phrase
}

// metricChartEnvOptions — SAF: takip çipleri için ≤max diğer canlı ortam.
func metricChartEnvOptions(envs []string, cur string, max int) []string {
	var out []string
	for _, e := range envs {
		e = strings.TrimSpace(e)
		if e == "" || e == cur || len(out) >= max {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ─── Takip çipleri / linkler (SAF) ─────────────────────────────────────

// Takip türleri — v0.10.1386.
const (
	fuKindMetricEnv    = "metric_env"
	fuKindMetricWindow = "window"
	fuKindMetricLink   = "metrics_link"
)

// metricChartFollowups — SAF: ≤2 diğer ortamda aynı metrik, "son 24 saate
// genişlet" (bağlam penceresi kipi — LastRoute klonu, chat_followups.go),
// servisin en yavaş trace'leri, Metrics'te aç (PromQL editörü). Tavan 4.
func metricChartFollowups(route guidedRoute, asked []string) []chatFollowup {
	if route.ChartPromQL != "" { // v0.10.1416 — R2: 24 sa (sorgu izin veriyorsa) + Metrics'te aç
		return pastedPromQLFollowups(route, asked)
	}
	t, ok := metricChartTemplateByID(route.ChartTemplate)
	if !ok {
		return nil
	}
	svc := strings.TrimSpace(route.Service)
	if svc != "" && !fuServiceRe.MatchString(svc) {
		svc = ""
	}
	var cands []chatFollowup
	for _, e := range route.ChartEnvOptions {
		text := t.Phrase + " " + e + " ortamında çiz"
		sc := &chatScope{Env: e}
		if svc != "" {
			text = svc + " " + text
			sc.Services = []string{svc}
		}
		cands = append(cands, chatFollowup{Text: text, Label: "Aynı metriği " + e + " ortamında çiz", Kind: fuKindMetricEnv, Scope: sc})
	}
	if route.ChartRangeS > 0 && route.ChartRangeS < 86400 {
		cands = append(cands, chatFollowup{Text: "Son 24 saate genişlet", Label: "Son 24 saate genişlet", Kind: fuKindMetricWindow})
	}
	if svc != "" {
		cands = append(cands, serviceStepFollowup(fuKindSlow, svc, route.Env))
	}
	out := dedupFollowupsAsked(cands, asked, guidedFollowupMax)
	if href := strings.TrimSpace(route.ChartHref); href != "" && len(out) < guidedFollowupMax && strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		out = append(out, chatFollowup{Text: "Metrics'te aç", Label: "Metrics'te aç", Kind: fuKindMetricLink, Href: href})
	}
	return out
}

// metricChartSuggestions — SAF: eski istemcinin düz çipleri.
func metricChartSuggestions(route guidedRoute) []string {
	if svc := route.Service; svc != "" {
		return []string{svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?", svc + " pod'ları nasıl?"}
	}
	return []string{"Açık problemler?", "Takımımın servisleri nasıl?"}
}

// metricChartLinks — SAF: cevap linkleri. Editör linki mutlak pencereyi
// kendisi taşır (/metrics range okuyan yollar listesinde değil); cpu/bellek
// şablonunda pod konteyner serileri için Infrastructure (D5).
func metricChartLinks(route guidedRoute) []guidedAnswerLink {
	var out []guidedAnswerLink
	if href := route.ChartHref; href != "" {
		out = append(out, guidedAnswerLink{Label: "Metrics'te aç", Href: href})
	} else {
		out = append(out, guidedAnswerLink{Label: "Metrics kataloğu", Href: "/metrics"})
	}
	if t, ok := metricChartTemplateByID(route.ChartTemplate); ok && t.Infra && route.Service != "" {
		out = append(out, guidedAnswerLink{Label: route.Service + " · Infrastructure (pod CPU/bellek)", Href: "/service?name=" + url.QueryEscape(route.Service) + "&tab=infra"})
	}
	return out
}

// ─── Özet (SAF) ─────────────────────────────────────────────────────────

// fmtMetricChartValue — SAF: birime göre kısa değer (By ikili önek, % bir
// ondalık, s/ms süre, birimsiz k/M).
func fmtMetricChartValue(v float64, unit string) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	trim := func(f string, x float64) string {
		s := fmt.Sprintf(f, x)
		if strings.Contains(s, ".") {
			s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		}
		return s
	}
	switch unit {
	case "By":
		a := math.Abs(v)
		for _, u := range []struct {
			n string
			f float64
		}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
			if a >= u.f {
				return trim("%.1f", v/u.f) + " " + u.n
			}
		}
		return trim("%.0f", v) + " B"
	case "%":
		return trim("%.1f", v) + "%"
	case "s":
		if math.Abs(v) < 1 {
			return trim("%.1f", v*1000) + " ms"
		}
		return trim("%.2f", v) + " s"
	case "ms":
		if math.Abs(v) >= 1000 {
			return trim("%.2f", v/1000) + " s"
		}
		return trim("%.1f", v) + " ms"
	}
	a := math.Abs(v)
	switch {
	case a >= 1e6:
		return trim("%.1f", v/1e6) + "M"
	case a >= 1e4:
		return trim("%.1f", v/1e3) + "k"
	case a >= 100:
		return trim("%.0f", v)
	}
	return trim("%.2f", v)
}

// metricChartSeriesStats — tek serinin deterministik özeti.
type metricChartSeriesStats struct {
	Min, Max, Avg, Last float64
	MaxAtMs             int64
	FirstThird          float64
	LastThird           float64
	Trend               string // up | down | flat
	Spike               string // "" | last | max
}

// metricChartStats — SAF: min/max/ort/son + tepe zamanı; eğilim ilk üçte bir
// ortalaması → son üçte bir ortalaması ±%20; sıçrama son ≥1.5×ort ya da tepe
// ≥2.0×ort (followupSlowLastFactor / followupSlowMaxFactor).
func metricChartStats(pts [][2]float64) (metricChartSeriesStats, bool) {
	var st metricChartSeriesStats
	n := len(pts)
	if n == 0 {
		return st, false
	}
	st.Min, st.Max = pts[0][1], pts[0][1]
	sum := 0.0
	for _, p := range pts {
		v := p[1]
		sum += v
		if v < st.Min {
			st.Min = v
		}
		if v > st.Max {
			st.Max, st.MaxAtMs = v, int64(p[0])
		}
	}
	if st.MaxAtMs == 0 {
		st.MaxAtMs = int64(pts[0][0])
	}
	st.Avg, st.Last = sum/float64(n), pts[n-1][1]
	st.Trend = "flat"
	if n >= 3 {
		k := n / 3
		mean := func(xs [][2]float64) float64 {
			t := 0.0
			for _, p := range xs {
				t += p[1]
			}
			return t / float64(len(xs))
		}
		st.FirstThird, st.LastThird = mean(pts[:k]), mean(pts[n-k:])
		switch a, b := st.FirstThird, st.LastThird; {
		case a == 0 && b > 0:
			st.Trend = "up"
		case a != 0 && (b-a)/math.Abs(a) > 0.2:
			st.Trend = "up"
		case a != 0 && (b-a)/math.Abs(a) < -0.2:
			st.Trend = "down"
		}
	}
	if st.Avg > 0 {
		switch {
		case st.Last >= followupSlowLastFactor*st.Avg:
			st.Spike = "last"
		case st.Max >= followupSlowMaxFactor*st.Avg:
			st.Spike = "max"
		}
	}
	return st, true
}

// metricChartStatsTR — SAF: modelin okuduğu "GRAFİK ÖZETİ" bloğu (≤10 satır).
func metricChartStatsTR(series []mcptools.EvChartSeries, unit string, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	var b strings.Builder
	b.WriteString("GRAFİK ÖZETİ (deterministik; model noktaları yeniden sayma):\n")
	trendTR := map[string]string{"up": "yükseliyor", "down": "düşüyor", "flat": "yatay"}
	hm := func(ms int64) string { return time.UnixMilli(ms).In(loc).Format("15:04") }
	lines := 0
	for _, s := range series {
		st, ok := metricChartStats(s.Points)
		if !ok {
			continue
		}
		if lines >= metricChartStatsMaxLines {
			fmt.Fprintf(&b, "- (+%d seri daha; grafikte)\n", len(series)-lines)
			break
		}
		lines++
		f := func(v float64) string { return fmtMetricChartValue(v, unit) }
		fmt.Fprintf(&b, "- %s: min %s, max %s (%s), ort %s, son %s; eğilim %s", s.Name, f(st.Min), f(st.Max), hm(st.MaxAtMs), f(st.Avg), f(st.Last), trendTR[st.Trend])
		if len(s.Points) >= 3 {
			fmt.Fprintf(&b, " (ilk üçte bir ort %s → son üçte bir ort %s)", f(st.FirstThird), f(st.LastThird))
		}
		switch st.Spike {
		case "last":
			fmt.Fprintf(&b, "; SIÇRAMA: son değer ortalamanın %.1f×'i", st.Last/st.Avg)
		case "max":
			fmt.Fprintf(&b, "; SIÇRAMA: tepe ortalamanın %.1f×'i (%s)", st.Max/st.Avg, hm(st.MaxAtMs))
		}
		b.WriteString("\n")
	}
	if lines == 0 {
		b.WriteString("- veri noktası yok\n")
	}
	return b.String()
}

// metricChartSeriesName — SAF: GroupKey (`label="değer"`) → okunur ad.
func metricChartSeriesName(gk []string, fallback string) string {
	var vals []string
	for _, kv := range gk {
		_, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		if v != "" {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return fallback
	}
	return strings.Join(vals, " · ")
}

// metricChartSeries — SAF: VM serileri → grafik serileri. NaN/Inf atılır,
// nokta ≤120 (sondan), seri ≤20 (tepe değere göre azalan, ad eşitlik kırıcı).
func metricChartSeries(in []chstore.SpanMetricSeries, unit, fallback string) ([]mcptools.EvChartSeries, bool) {
	truncated := false
	out := make([]mcptools.EvChartSeries, 0, len(in))
	peak := map[int]float64{}
	for _, s := range in {
		cs := mcptools.EvChartSeries{Name: metricChartSeriesName(s.GroupKey, fallback), Unit: unit}
		mx := math.Inf(-1)
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			cs.Points = append(cs.Points, [2]float64{float64(p.Time / 1e6), p.Value})
			if p.Value > mx {
				mx = p.Value
			}
		}
		if len(cs.Points) == 0 {
			continue
		}
		if len(cs.Points) > metricChartMaxPoints {
			cs.Points, truncated = cs.Points[len(cs.Points)-metricChartMaxPoints:], true
		}
		peak[len(out)] = mx
		out = append(out, cs)
	}
	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if peak[idx[a]] != peak[idx[b]] {
			return peak[idx[a]] > peak[idx[b]]
		}
		return out[idx[a]].Name < out[idx[b]].Name
	})
	sorted := make([]mcptools.EvChartSeries, 0, len(out))
	for _, i := range idx {
		if len(sorted) >= metricChartMaxSeries {
			truncated = true
			break
		}
		sorted = append(sorted, out[i])
	}
	return sorted, truncated
}

// metricChartCacheKey — SAF (CLAUDE.md "cache key hashes ALL inputs"): kaynak
// adı, şablon, son PromQL, servis, env eşleştiricisi, kırılım, dakikaya
// oturtulmuş uçlar ve adım — FNV, uzunluk-yalnız bileşen yok (v0.5.187).
func metricChartCacheKey(src, template, query, service, envMatcher, groupBy string, from, to time.Time, step int) string {
	return "cosre-mchart:v1:" + fnvStr(src, template, query, service, envMatcher, groupBy,
		fmt.Sprint(from.Unix()/60), fmt.Sprint(to.Unix()/60), fmt.Sprint(step))
}

// metricChartLabelsKey — SAF: etiket keşfi önbelleği (metrik + 10 dk uç).
func metricChartLabelsKey(metric string, to time.Time) string {
	return "cosre-mchart-labels:v1:" + fnvStr(metric, fmt.Sprint(to.Truncate(httpEnvValuesSnap).Unix()), fmt.Sprint(int64(metricChartLabelsLookbk/time.Second)))
}

// ─── Demet ──────────────────────────────────────────────────────────────

// metricChartReader — demetin VM'den okuduğu dört şey (vmMetricSource
// karşılar; testler sahte okuyucu verir). Hepsi okuma.
type metricChartReader interface {
	httpEnvLabelReader
	redFamilyLabelReader // v0.10.1704 — servise kapsanmış etiket keşfi (metricLabelSetFor)
	MetricLabelNamesIn(ctx context.Context, metric string, from, to time.Time) ([]string, bool, error)
	QueryPromQLRange(ctx context.Context, query string, from, to time.Time, stepSeconds, maxDataPoints int) ([]chstore.SpanMetricSeries, error)
}

// metricChartReaderFor — yapılandırılmış metrik kaynağı VM ise okuyucu.
func (s *Server) metricChartReaderFor() (metricChartReader, bool) {
	if vm, ok := s.metricSource().(vmMetricSource); ok {
		return vm, true
	}
	return nil, false
}

// metricChartCached — 60 sn önbellek gövdesi (yalnız sorgu sonucu).
type metricChartCached struct {
	Series     []mcptools.EvChartSeries `json:"series"`
	Truncated  bool                     `json:"truncated,omitempty"`
	DurationMs int64                    `json:"durationMs"`
	Query      string                   `json:"query"`
}

func (s *Server) metricChartCacheGet(ctx context.Context, key string, out any) bool {
	if s == nil || s.cache == nil {
		return false
	}
	b, ok, _ := s.cache.Get(ctx, key)
	return ok && len(b) > 0 && json.Unmarshal(b, out) == nil
}

func (s *Server) metricChartCacheSet(ctx context.Context, key string, v any, ttl time.Duration) {
	if s == nil || s.cache == nil {
		return
	}
	if b, err := json.Marshal(v); err == nil {
		_ = s.cache.Set(ctx, key, b, ttl)
	}
}

// metricChartLabelSet — metriğin son 24 sa etiket kümesi (2 sn, 5 dk önbellek).
// Boş küme = metrik yok. Hata önbelleğe yazılmaz.
func (s *Server) metricChartLabelSet(ctx context.Context, rd metricChartReader, metric string, to time.Time) (map[string]bool, error) {
	key := metricChartLabelsKey(metric, to)
	var names []string
	if !s.metricChartCacheGet(ctx, key, &names) {
		lctx, cancel := context.WithTimeout(ctx, metricChartLabelTimeout)
		end := to.Truncate(httpEnvValuesSnap).Add(httpEnvValuesSnap)
		got, _, err := rd.MetricLabelNamesIn(lctx, metric, end.Add(-metricChartLabelsLookbk), end)
		cancel()
		if err != nil {
			return nil, err
		}
		names = got
		if names == nil {
			names = []string{}
		}
		s.metricChartCacheSet(ctx, key, names, metricChartLabelsTTL)
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set, nil
}

// metricLabelSetFor — v0.10.1704 (v0.10.1689'un ikinci yarısı: prod'da
// kapsamsız /api/v1/labels match[]={__name__=~…} filonun 24 sa'ini tarayıp
// "context deadline exceeded" veriyordu). service != "" → redFamilyLabelSet
// ({__name__=~…, <servis etiketi>="<servis>"}, aile çözücüsüyle aynı anahtar);
// service == "" → hedef GERÇEKTEN filo geneli (servissiz grafik, Fleet
// varyantı, servissiz pod/workload JVM'i): metricChartLabelSet — tek metrik
// adı match[], 24 sa pencere, 2 sn bütçe, 5 dk önbellek ile sınırlı.
func (s *Server) metricLabelSetFor(ctx context.Context, rd metricChartReader, metric, service string, to time.Time) (map[string]bool, error) {
	if strings.TrimSpace(service) == "" {
		return s.metricChartLabelSet(ctx, rd, metric, to)
	}
	return s.redFamilyLabelSet(ctx, rd, metric, redFamilySvcLabel(rd.MetricLabelMap().Normalized()), service, to)
}

// guidedMetricChartBundle — metric_chart rotasının kanıtı. Hiçbir okuma
// hatası demeti düşürmez (serbest döngüye bırakmaz): dürüst metin döner.
func (s *Server) guidedMetricChartBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64, loc *time.Location) (string, string, error) {
	rd, ok := s.metricChartReaderFor()
	if route.ChartPromQL != "" { // v0.10.1416 — R2: yapıştırılan PromQL (copilot_metric_chart_promql.go)
		return s.metricChartRunPromQL(ctx, emit, route, rd, ok, from, to, rangeS, loc)
	}
	route.ChartEnvOptions = metricChartEnvOptions(s.guidedEnvNames(ctx), route.Env, metricChartEnvChipsMax)
	return s.metricChartRun(ctx, emit, route, rd, ok, from, to, rangeS, loc)
}

// metricChartRun — demetin çekirdeği (okuyucu dışarıdan: test dikişi).
func (s *Server) metricChartRun(ctx context.Context, emit func(string, any), route *guidedRoute, rd metricChartReader, isVM bool, from, to time.Time, rangeS int64, loc *time.Location) (string, string, error) {
	route.ChartRangeS = rangeS
	t, ok := metricChartTemplateByID(route.ChartTemplate)
	args := fmt.Sprintf(`{"template":%s,"service":%s,"range_s":%d}`, jsonStr(route.ChartTemplate), jsonStr(route.Service), rangeS)
	n := emitGuidedStep(emit, "metric_chart", withEnvArg(args, route.Env))
	done := func(text, sources string) (string, string, error) {
		emitGuidedStepResult(emit, n, "metric_chart", text, nil)
		return text, sources, nil
	}
	if !ok {
		return done("METRİK GRAFİĞİ: bilinmeyen şablon "+jsonStr(route.ChartTemplate)+" — grafik çizilmedi.\n", "metric_chart")
	}
	scope := strings.TrimPrefix(guidedScopeTR(route.Service, route.Env), ", ")
	if scope == "" {
		scope = "filo geneli"
	}
	head := fmt.Sprintf("METRİK GRAFİĞİ (%s, şablon %s): %s, son %s.\n", t.Title, t.ID, scope, fmtAgoTR(rangeS))

	// D4 — VM yok: RED şablonu servisliyse span spec-chart; diğerleri dürüst.
	if !isVM || rd == nil {
		if t.SpanAgg != "" && route.Service != "" {
			text := head + "VictoriaMetrics yapılandırılmamış — grafik span RED rollup'ından (ClickHouse) çizildi; metrik deposu değil.\n" +
				chartFence(guidedChartSpec{Title: route.Service + " · " + t.SpanAgg, Service: route.Service, Agg: t.SpanAgg, RangeS: rangeS})
			return done(text, "ClickHouse span RED (spec-chart)")
		}
		return done(head+"VictoriaMetrics yapılandırılmamış: bu grafik VictoriaMetrics metrik deposu ister — span verisinden çizilemez, grafik YOK.\n", "metric_chart (VM yok)")
	}

	qTo := to.Truncate(time.Minute)
	qFrom := qTo.Add(-to.Sub(from))
	step := metricChartStepS(rangeS)

	// Varyant: etiket keşfi (varlık + pod/durum/ortam etiketleri tek okumada).
	present := map[string]map[string]bool{}
	var readErr error
	for _, v := range t.Variants {
		if _, seen := present[v.Probe]; seen {
			continue
		}
		// v0.10.1704 — servis biliniyorsa etiket keşfi SERVİSE kapsanır
		// (metricLabelSetFor); Fleet varyantı servis etiketi taşımaz → filo.
		svc := route.Service
		if v.Fleet {
			svc = ""
		}
		set, err := s.metricLabelSetFor(ctx, rd, v.Probe, svc, qTo)
		if err != nil {
			readErr = err
			continue
		}
		present[v.Probe] = set
	}
	v, labels, found := pickMetricChartVariant(t, present)
	if !found {
		tried := strings.Join(metricChartTriedNames(t, metricChartTriedMax), ", ")
		if readErr != nil {
			return done(head+fmt.Sprintf("VictoriaMetrics etiket okuması BAŞARISIZ (okunamadı ≠ yok): %v. Denenen yazımlar: %s.\n", readErr, tried), "VictoriaMetrics (okunamadı)")
		}
		return done(head+fmt.Sprintf("Bu metrik VictoriaMetrics'te BULUNAMADI (son 24 sa; denenen yazımlar: %s) — grafik YOK. Metrics kataloğunda adı aratın.\n", tried), "VictoriaMetrics (seri yok)")
	}
	unit := t.Unit
	if v.Unit != "" {
		unit = v.Unit
	}
	lm := rd.MetricLabelMap().Normalized()
	var notes []string

	// Servis etiketi (konvansiyon service_name; LabelMap / keşif).
	svcLabel := ""
	if route.Service != "" {
		if v.Fleet {
			notes = append(notes, "bu metrik servis etiketi taşımıyor — servis süzgeci UYGULANMADI, değerler filo geneli")
		} else if res := vmetrics.ResolveLabelRole(vmetrics.RoleService, lm, labels, true); res.Applied() {
			svcLabel = res.Label
		} else {
			return done(head+fmt.Sprintf("Metrik var ama servis etiketi bulunamadı (denenen: %s, %s) — %q süzülemedi, grafik YOK (yanlış servisin grafiği çizilmez).\n",
				"service_name", strings.Join(vmetrics.RoleCandidates(vmetrics.RoleService), ", "), route.Service), "VictoriaMetrics (servis etiketi yok)")
		}
	}

	// Ortam (D8): HTTP RED metriğinin ortam etiketine çözülür; asla sessiz düşmez.
	envMatcher, envLabel := "", ""
	if route.Env != "" {
		sc, err := s.resolveHTTPEnvScope(ctx, rd, map[string]string{vmetrics.RoleEnv: route.Env}, qTo)
		reason := ""
		switch {
		case err != nil:
			reason = "ortam etiketi okunamadı: " + err.Error()
		case !sc.Applied:
			reason = sc.Reason
		case !labels[sc.Label]:
			reason = fmt.Sprintf("bu metrik %s etiketini taşımıyor", sc.Label)
		default:
			envMatcher, envLabel = sc.Matcher, sc.Label
		}
		if reason != "" {
			if route.ChartEnvAsked {
				return done(head+fmt.Sprintf("Ortam süzgeci %q uygulanamadı (%s) — tüm ortamların grafiği yanlış cevap olurdu, grafik YOK.\n", route.Env, reason), "VictoriaMetrics (ortam çözülemedi)")
			}
			notes = append(notes, fmt.Sprintf("ekrandaki ortam %q süzgeci UYGULANMADI (%s) — seriler TÜM ortamları kapsıyor", route.Env, reason))
		}
	}
	if route.ChartGroupBy == "env" && envLabel == "" {
		if el, err := s.httpEnvLabelFor(ctx, rd, qTo); err == nil {
			envLabel = el.Label
		}
	}
	podLabel := ""
	if pr := vmetrics.ResolveLabelRole(vmetrics.RolePod, lm, labels, true); pr.Applied() {
		podLabel = pr.Label
	}
	by, byNote := metricChartByLabels(t, v, route.ChartGroupBy, podLabel, envLabel, labels)
	if byNote != "" {
		notes = append(notes, byNote)
	}
	status := ""
	if strings.Contains(v.Expr, "{sel5xx}") {
		if status = metricChartStatusLabel(labels); status == "" {
			return done(head+fmt.Sprintf("HTTP metriğinde durum kodu etiketi yok (%s) — 5xx oranı ölçülemez, grafik YOK (0 sanılmasın).\n", strings.Join(mcStatusLabels, " / ")), "VictoriaMetrics (durum etiketi yok)")
		}
	}
	query := buildMetricChartPromQL(metricChartQueryInput{Variant: v, Service: route.Service, SvcLabel: svcLabel,
		EnvMatcher: envMatcher, By: by, StatusLabel: status, WinS: metricChartWinS(step)})
	href := mcptools.PromQLEditorHref(query, "vm", "", qFrom.UnixMilli(), qTo.UnixMilli())
	route.ChartHref = href

	key := metricChartCacheKey(metricSourceVM, t.ID, query, route.Service, envMatcher, route.ChartGroupBy, qFrom, qTo, step)
	var res metricChartCached
	if s.metricChartCacheGet(ctx, key, &res) {
		emitGuidedContextStep(emit, "önbellek: aynı metrik grafiği (≤60 sn)")
	} else {
		rec := &vmetrics.QueryRecorder{}
		qctx, cancel := context.WithTimeout(vmetrics.WithQueryRecorder(ctx, rec), metricChartQueryTimeout)
		start := time.Now()
		raw, err := rd.QueryPromQLRange(qctx, query, qFrom, qTo, step, metricChartMaxPoints)
		cancel()
		if err != nil {
			text := head + "PromQL: " + query + "\nVictoriaMetrics sorgusu BAŞARISIZ (okunamadı ≠ yok): " + err.Error() + "\n"
			emitGuidedStepResult(emit, n, "metric_chart", text, err)
			return text, "VictoriaMetrics (okunamadı)", nil
		}
		fallback := route.Service
		if fallback == "" {
			fallback = "filo"
		}
		res.Series, res.Truncated = metricChartSeries(raw, unit, fallback)
		res.DurationMs = time.Since(start).Milliseconds()
		// VM'e GERÇEKTEN giden ifade (kaydedici); kanıt çipi onu gösterir.
		res.Query = query
		if qs := rec.Queries(); len(qs) > 0 {
			res.Query = qs[len(qs)-1]
		}
		s.metricChartCacheSet(ctx, key, res, metricChartCacheTTL)
	}

	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "Kaynak: VictoriaMetrics, adım %ds, rate penceresi %ds, %d seri", step, metricChartWinS(step), len(res.Series))
	if res.Truncated {
		fmt.Fprintf(&b, " (tavan: ≤%d seri / ≤%d nokta uygulandı)", metricChartMaxSeries, metricChartMaxPoints)
	}
	b.WriteString(".\nPromQL: " + query + "\n")
	if envMatcher != "" {
		b.WriteString("Ortam süzgeci metrikte " + envMatcher + " ile uygulandı.\n")
	}
	for _, nt := range notes {
		b.WriteString("Not: " + nt + ".\n")
	}
	if t.Infra {
		b.WriteString("Not: bu grafik uygulamanın KENDİ process metriği; pod KONTEYNER CPU/bellek (cAdvisor container_*) Thanos'ta — Infrastructure sekmesi.\n")
	}
	if len(res.Series) == 0 {
		b.WriteString("Bu pencerede SERİ YOK (metrik son 24 sa'te var ama bu süzgeç + pencerede veri dönmedi) — grafik YOK; sorguyu Metrics'te açıp pencereyi/süzgeci değiştirin.\n")
	} else {
		b.WriteString("Grafik cevabın altında kanıt çipinde çizildi (sunucu verisi; grafik bloğu YAZMA).\n")
		b.WriteString(metricChartStatsTR(res.Series, unit, loc))
	}
	text := b.String()

	nSeries := len(res.Series)
	if res.Query == "" {
		res.Query = query
	}
	ev := mcptools.ChatEvidence{Tool: "metric_chart", Source: "VictoriaMetrics", Query: res.Query, QueryKind: "promql",
		Window: mcptools.EvidenceWindow{FromMs: qFrom.UnixMilli(), ToMs: qTo.UnixMilli()}, Series: &nSeries,
		DurationMs: res.DurationMs, Href: href}
	if envMatcher != "" {
		ev.Env = route.Env
	}
	title := t.Title
	if route.Service != "" {
		title = route.Service + " · " + t.Title
	}
	chart := &mcptools.EvChartPayload{Title: title, DeepLink: href,
		Window: mcptools.EvBlockWindow{FromNs: qFrom.UnixNano(), ToNs: qTo.UnixNano()}, Series: res.Series, Truncated: res.Truncated}
	emitGuidedEvidenceResult(emit, n, "metric_chart", text, ev, chart)
	return text, "VictoriaMetrics · metric_chart/" + t.ID, nil
}
