package api

// cosre_briefing_http_group.go — v0.10.1719: Login Briefing kalem 2'nin
// HTTP/gRPC sunucu ailesi TAKIMIN TÜM servisleri için (1712'de yalnız en
// yoğun servis). Servis başına sorgu YOK: servis kümesi süzgeci
// `svc=~"a|b|c"` + `by (<svc etiketi>)`, topk YOK (aralık sorgusunda topk
// nokta başına seçer, servis serisi delikli gelirdi; seri sayısı küme
// tavanıyla sınırlı).
//
// v0.10.1727 — karma aileli takım: 1719 aileyi en yoğun servisten alıp tek
// küme okuyordu; diğer ailede yazan servis satırsız kalıyordu. Artık servis
// başına aile KATALOGDAN (chstore.ServerEntryKinds, spanmetrics_1m: sunucu
// span'i rota taşıyor → HTTP, taşımıyor → gRPC adayı) — servis başına VM
// keşfi YOK. Var olan her tür (HTTP / gRPC) için BİR sorgu kümesi, tür başına
// İKİ range sorgusu:
//   - istek sayısı `sum by (svc, <durum etiketleri>) (rate(_count))`: hacim
//     (toplam) VE sunucu-hatası oranı (redFamily.IsServerError, Go'da) aynı
//     seriden — 1719'un ayrı istek hızı + 5xx sorguları tek sorgu oldu
//     (oran = 100·Σhata/Σtoplam, sekmenin `{sel5xx}/{sel}` ifadesiyle eş).
//   - p95 gecikme; gRPC'de legacy (ms → /1000) `or` stable tek ifadede.
//   R9: 2 tür × 2 + Kafka 2 = ≤ 6 VM range sorgusu (briefingClientVMQueries).
//   - Aile değişkeni (legacy / stable gRPC; HTTP yazımları) keşfedilmez:
//     türün TÜM ailelerinin ad adayları tek `__name__` alternasyonunda.
//   - svc + ortam etiketi en yoğun servisin çözümünden (resolveSvcREDInputs —
//     sekmeyle aynı keşif + önbellek, 1719'daki gibi tek servis); diğer tür
//     aynı etiket yazımını kullanır (kaynak öznitelikleri, collector-geneli).
//   - Hiç durum değeri görülmeyen servisin oran satırı YOK (0 sanılmasın).
//   - Katalog okunamazsa / boşsa (MV henüz dolmamış) adaylar İKİ türe de
//     girer (R9 yine 6); seri gelmeyen tür satırsız.
// Okuma hatası → failed (partial); seri yok → satır yok.

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

const (
	// briefingHTTPGroupMax — tek sorgu kümesinin servis tavanı.
	briefingHTTPGroupMax = 20
	// briefingHTTPGroupRegexMax — servis regex gövdesinin bayt tavanı.
	briefingHTTPGroupRegexMax = 2048
	// briefingKafkaVMQueries — kalem 2'nin Kafka soru sayısı (briefingKafkaQuestions).
	briefingKafkaVMQueries = 2
	// briefingR9VMMax — R9: takım × env başına VM range sorgusu tavanı.
	briefingR9VMMax = 6
	// briefingGroupLatencyExpr — p95, sekmenin histogram_quantile kalıbı.
	briefingGroupLatencyExpr = "histogram_quantile(0.95, sum by ({byle}) (rate({sel}[{win}])))"
	// briefingGroupCountExpr — istek sayısı hızı, durum kırılımlı.
	briefingGroupCountExpr = "sum{by} (rate({sel}[{win}]))"
)

// briefingHTTPGroupServices — SAF: adaylar (span azalan) → güvenli, tavanlı küme.
func briefingHTTPGroupServices(cands []string) []string {
	var out []string
	seen := map[string]bool{}
	size := 0
	for _, c := range cands {
		if len(out) >= briefingHTTPGroupMax {
			break
		}
		if c == "" || seen[c] || strings.IndexFunc(c, unicode.IsControl) >= 0 {
			continue
		}
		n := len(regexp.QuoteMeta(c))
		if len(out) > 0 {
			n++ // "|"
		}
		if size+n > briefingHTTPGroupRegexMax {
			break
		}
		seen[c], size = true, size+n
		out = append(out, c)
	}
	return out
}

// briefingSplitKinds — SAF: adaylar (span azalan) + katalog → tür başına küme
// (sıra korunur). known=false (katalog okunamadı / boş): adaylar iki türde de.
// Sunucu span'i olmayan servis (yalnız istemci / tüketici) hiçbir kümede yok.
func briefingSplitKinds(cands []string, calls map[string]chstore.ServerEntryCalls, known bool) (httpSvcs, grpcSvcs []string) {
	for _, c := range cands {
		e, ok := calls[c]
		if !known {
			e, ok = chstore.ServerEntryCalls{HTTP: 1, Other: 1}, true
		}
		if !ok {
			continue
		}
		if e.HTTP > 0 {
			httpSvcs = append(httpSvcs, c)
		}
		if e.Other > 0 {
			grpcSvcs = append(grpcSvcs, c)
		}
	}
	return briefingHTTPGroupServices(httpSvcs), briefingHTTPGroupServices(grpcSvcs)
}

// briefingKindFamilies — SAF: türün (redKindHTTP / redKindGRPC) aileleri, katalog sırası.
func briefingKindFamilies(kind string) []redFamily {
	var out []redFamily
	for _, f := range redFamilies {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// briefingKindStatusLabels — SAF: türün tüm durum etiketi yazımları (tekil, sıralı).
func briefingKindStatusLabels(fams []redFamily) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range fams {
		for _, l := range f.StatusLabels {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}

// briefingHTTPGroupQueries — SAF: türün küme için İKİ range sorgusu (istek
// sayısı × durum, p95). Boş küme / svc etiketi yok → nil (süzgeçsiz filo
// sorgusu ASLA).
func briefingHTTPGroupQueries(kind string, services []string, svcLabel, envMatcher string, winS int) []svcHTTPREDQuery {
	fams := briefingKindFamilies(kind)
	if len(services) == 0 || svcLabel == "" || len(fams) == 0 {
		return nil
	}
	base := metricChartQueryInput{SvcLabel: svcLabel, SvcValues: services, GroupedNoTopK: true, EnvMatcher: envMatcher, WinS: winS}
	names := make([]string, 0, len(fams))
	for _, f := range fams {
		names = append(names, f.names())
	}
	cnt := base
	cnt.Variant = metricChartVariant{Kind: "count", Names: names, Expr: briefingGroupCountExpr}
	cnt.By = append([]string{svcLabel}, briefingKindStatusLabels(fams)...)
	lats := make([]string, 0, len(fams))
	for _, f := range fams {
		in := base
		in.By = []string{svcLabel}
		in.Variant = metricChartVariant{Kind: "bucket", Names: []string{f.names()}, Expr: briefingGroupLatencyExpr}
		q := "(" + buildMetricChartPromQL(in) + ")"
		if f.Unit == "ms" {
			q += " / 1000" // ölçek ailenin beyan ettiği birimden
		}
		lats = append(lats, q)
	}
	return []svcHTTPREDQuery{
		{Kind: svcHTTPREDRate, Query: buildMetricChartPromQL(cnt)},
		{Kind: svcHTTPREDLatency, Query: strings.Join(lats, " or ")},
	}
}

// briefingGroupKey — SAF: seri GroupKey'inden (`k="v"`) etiketin değeri.
func briefingGroupKey(gk []string, label string) (string, bool) {
	for _, kv := range gk {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k != label {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			return u, true
		}
	}
	return "", false
}

func briefingPoints(s chstore.SpanMetricSeries, scale float64) [][2]float64 {
	pts := make([][2]float64, 0, len(s.Points))
	for _, p := range s.Points {
		pts = append(pts, [2]float64{float64(p.Time / 1e6), p.Value * scale})
	}
	return pts
}

// briefingHTTPGroupRows — SAF: türün ham serileri ⇒ servis başına kalem 2
// satırları. İstek-sayısı serileri servis × durum: nokta başına toplam (hacim)
// ve sunucu-hatası payı (oran %). Yalnız kümedeki servisler; hacim iki
// pencerede okunmadıysa oran kıyası kapalı; durum değeri hiç yoksa oran yok.
func briefingHTTPGroupRows(kind string, byKind map[string][]chstore.SpanMetricSeries, svcLabel string, services []string, fromMs int64, windowS int) []briefing.ClientRow {
	fams := briefingKindFamilies(kind)
	if len(fams) == 0 {
		return nil
	}
	in := make(map[string]bool, len(services))
	for _, s := range services {
		in[s] = true
	}
	famID := briefing.FamilyHTTP
	if kind == redKindGRPC {
		famID = briefing.FamilyGRPC
	}
	statusLabels := briefingKindStatusLabels(fams)
	type acc struct {
		total, errs map[int64]float64
		status      bool
	}
	per := map[string]*acc{}
	var order []string
	for _, s := range byKind[svcHTTPREDRate] {
		svc, ok := briefingGroupKey(s.GroupKey, svcLabel)
		if !ok || !in[svc] {
			continue
		}
		a := per[svc]
		if a == nil {
			a = &acc{total: map[int64]float64{}, errs: map[int64]float64{}}
			per[svc] = a
			order = append(order, svc)
		}
		code := ""
		for _, l := range statusLabels {
			if v, ok := briefingGroupKey(s.GroupKey, l); ok && v != "" {
				code = v
				break
			}
		}
		isErr := code != "" && fams[0].IsServerError(code)
		a.status = a.status || code != ""
		for _, p := range s.Points {
			ms := p.Time / 1e6
			a.total[ms] += p.Value
			if isErr {
				a.errs[ms] += p.Value
			}
		}
	}
	var rows []briefing.ClientRow
	for _, svc := range order {
		a := per[svc]
		var vol, pct [][2]float64
		for ms, t := range a.total {
			vol = append(vol, [2]float64{float64(ms), t})
			if t > 0 {
				pct = append(pct, [2]float64{float64(ms), 100 * a.errs[ms] / t})
			}
		}
		if !a.status {
			continue // durum etiketi yok → oran ölçülemez (0 sanılmasın)
		}
		rn, rp, hvn, hvp := briefing.SplitMeans(vol, fromMs)
		n, p, hn, hp := briefing.SplitMeans(pct, fromMs)
		if !hvn || !hvp {
			hn, hp = false, false // hacim okunmadı → oran kıyası yok
		}
		rows = append(rows, briefing.ClientRow{Service: svc, Family: famID, Metric: briefing.ClientErrPct,
			Now: n, Prev: p, HasNow: hn, HasPrev: hp, VolNow: rn * float64(windowS), VolPrev: rp * float64(windowS)})
	}
	for _, s := range byKind[svcHTTPREDLatency] {
		svc, ok := briefingGroupKey(s.GroupKey, svcLabel)
		if !ok || !in[svc] {
			continue
		}
		n, p, hn, hp := briefing.SplitMeans(briefingPoints(s, 1), fromMs)
		rows = append(rows, briefing.ClientRow{Service: svc, Family: famID, Metric: briefing.ClientLatency, Now: n, Prev: p, HasNow: hn, HasPrev: hp})
	}
	return rows
}

// briefingKindGroup — bir türün okunacak kümesi.
type briefingKindGroup struct {
	Kind     string
	Services []string
}

// briefingKindGroups — SAF: boş olmayan tür kümeleri (HTTP önce).
func briefingKindGroups(httpSvcs, grpcSvcs []string) []briefingKindGroup {
	var out []briefingKindGroup
	if len(httpSvcs) > 0 {
		out = append(out, briefingKindGroup{redKindHTTP, httpSvcs})
	}
	if len(grpcSvcs) > 0 {
		out = append(out, briefingKindGroup{redKindGRPC, grpcSvcs})
	}
	return out
}

// briefingGroupsQueries — SAF: tüm türlerin sorguları (R9 sayımı için).
func briefingGroupsQueries(groups []briefingKindGroup, svcLabel, envMatcher string, winS int) [][]svcHTTPREDQuery {
	out := make([][]svcHTTPREDQuery, len(groups))
	for i, g := range groups {
		out[i] = briefingHTTPGroupQueries(g.Kind, g.Services, svcLabel, envMatcher, winS)
	}
	return out
}

// readBriefingHTTPGroup — takımın HTTP/gRPC sunucu aileleri, tür başına bir
// sorgu kümesi. failed = okuma hatası (etiket keşfi ya da bir sorgu); seri
// yok ≠ hata. Katalog okuması hatası failed SAYILMAZ (iki türe de düşülür).
func (s *Server) readBriefingHTTPGroup(ctx context.Context, rd redFamilyReader, cands []string, env string, pFrom, from, to time.Time) (rows []briefing.ClientRow, failed bool) {
	cands = briefingHTTPGroupServices(append([]string(nil), cands...))
	if len(cands) == 0 {
		return nil, false
	}
	calls, cerr := s.store.ServerEntryKinds(ctx, cands, pFrom, to)
	httpSvcs, grpcSvcs := briefingSplitKinds(cands, calls, cerr == nil && len(calls) > 0)
	groups := briefingKindGroups(httpSvcs, grpcSvcs)
	if len(groups) == 0 {
		return nil, false
	}
	rep := groups[0].Services[0]
	for _, c := range cands { // en yoğun sunucu servisi
		if slices.Contains(httpSvcs, c) || slices.Contains(grpcSvcs, c) {
			rep = c
			break
		}
	}
	vctx, cancel := context.WithTimeout(ctx, svcHTTPREDBudget)
	defer cancel()
	qTo := to.Truncate(time.Minute)
	qFrom := qTo.Add(-to.Sub(pFrom))
	step := metricChartStepS(int64(to.Sub(pFrom).Seconds()))
	probe := svcHTTPREDProbe{VM: true, GRPCChecked: true}
	in, ok := s.resolveSvcREDInputs(vctx, rd, rep, env, qTo, &probe)
	if !ok {
		return nil, probe.LabelErr != nil
	}
	all := briefingGroupsQueries(groups, in.svcLabel, in.envMatcher, metricChartWinS(step))
	byKind := make([]map[string][]chstore.SpanMetricSeries, len(groups))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for gi, qs := range all {
		byKind[gi] = make(map[string][]chstore.SpanMetricSeries, len(qs))
		for _, q := range qs {
			wg.Add(1)
			go func(gi int, q svcHTTPREDQuery) {
				defer wg.Done()
				qctx, qcancel := context.WithTimeout(vctx, metricChartQueryTimeout)
				raw, err := rd.QueryPromQLRange(qctx, q.Query, qFrom, qTo, step, metricChartMaxPoints)
				qcancel()
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failed = true
					return
				}
				byKind[gi][q.Kind] = raw
			}(gi, q)
		}
	}
	wg.Wait()
	for gi, g := range groups {
		rows = append(rows, briefingHTTPGroupRows(g.Kind, byKind[gi], in.svcLabel, g.Services, from.UnixMilli(), int(to.Sub(from).Seconds()))...)
	}
	return rows, failed
}

// briefingClientVMQueries — SAF (R9 pini): kalem 2'nin takım × env başına VM
// range sorgusu sayısı (tür kümeleri + Kafka).
func briefingClientVMQueries(sets [][]svcHTTPREDQuery) int {
	n := briefingKafkaVMQueries
	for _, qs := range sets {
		n += len(qs)
	}
	return n
}
