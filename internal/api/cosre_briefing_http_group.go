package api

// cosre_briefing_http_group.go — v0.10.1719: Login Briefing kalem 2'nin
// HTTP/gRPC sunucu ailesi TAKIMIN TÜM servisleri için (1712'de yalnız en
// yoğun servis). R9 (takım × env ≤ 6 VM) korunur: servis başına sorgu YOK,
// aile ifadeleri (svcREDFamilyQueriesIn — Service → HTTP/GRPC sekmesinin
// AYNI üç ifadesi) servis kümesi süzgeci + `by (<svc etiketi>)` ile TEK
// sorgu kümesi olur: istek hızı + [hata oranı] + gecikme = ≤ 3 range sorgusu
// (+ Kafka 2 = ≤ 5).
//
//   - Küme: kur penceresinde span azalan aday, en çok briefingHTTPGroupMax
//     servis; kontrol karakterli ad ATILIR; regex gövdesi (QuoteMeta'lı)
//     briefingHTTPGroupRegexMax baytı aşmaz. Seçici vmetrics.EnvMatcher
//     (QuoteMeta + yalnız \ ve " dize kaçışı; VM regex'i tam çapalı).
//   - topk YOK (GroupedNoTopK): aralık sorgusunda topk nokta başına seçer,
//     servis serisi delikli gelirdi; seri sayısı küme tavanıyla sınırlı
//     (gecikme p95+p99 → ≤ 2×tavan).
//   - Aile + etiketler (svc / ortam / durum) EN YOĞUN servisten çözülür
//     (resolveSvcREDInputs — sekmeyle aynı keşif ve önbellek). Başka ailede
//     yazan servisin serisi gelmez → satırı yok (uydurma yok).
// Okuma hatası → failed (partial); seri yok → satır yok.

import (
	"context"
	"regexp"
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

// briefingHTTPGroupQueries — SAF: ailenin RED'i küme için TEK sorgu kümesi (by svc).
func briefingHTTPGroupQueries(fam redFamily, services []string, svcLabel, envMatcher, statusLabel string, winS int) []svcHTTPREDQuery {
	if len(services) == 0 || svcLabel == "" {
		return nil
	}
	return svcREDFamilyQueriesIn(fam, "takım", metricChartQueryInput{
		SvcLabel: svcLabel, SvcValues: services, By: []string{svcLabel}, GroupedNoTopK: true,
		EnvMatcher: envMatcher, StatusLabel: statusLabel, WinS: winS,
	})
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

// briefingHTTPGroupRows — SAF: sorgu türü → ham seriler ⇒ servis başına kalem 2
// satırları. Yalnız kümedeki servisler; istek hızı okunmadıysa oran kıyası kapalı.
func briefingHTTPGroupRows(fam redFamily, byKind map[string][]chstore.SpanMetricSeries, svcLabel string, services []string, fromMs int64, windowS int) []briefing.ClientRow {
	in := make(map[string]bool, len(services))
	for _, s := range services {
		in[s] = true
	}
	famID := briefing.FamilyHTTP
	if fam.Kind == redKindGRPC {
		famID = briefing.FamilyGRPC
	}
	type vol struct {
		now, prev float64
		ok        bool
	}
	vols := map[string]vol{}
	for _, s := range byKind[svcHTTPREDRate] {
		svc, ok := briefingGroupKey(s.GroupKey, svcLabel)
		if !ok || !in[svc] {
			continue
		}
		rn, rp, hn, hp := briefing.SplitMeans(briefingPoints(s, 1), fromMs)
		vols[svc] = vol{rn * float64(windowS), rp * float64(windowS), hn && hp}
	}
	var rows []briefing.ClientRow
	for _, s := range byKind[svcHTTPRED5xx] {
		svc, ok := briefingGroupKey(s.GroupKey, svcLabel)
		if !ok || !in[svc] {
			continue
		}
		n, p, hn, hp := briefing.SplitMeans(briefingPoints(s, 1), fromMs)
		v := vols[svc]
		if !v.ok {
			hn, hp = false, false // hacim okunmadı → oran kıyası yok
		}
		rows = append(rows, briefing.ClientRow{Service: svc, Family: famID, Metric: briefing.ClientErrPct,
			Now: n, Prev: p, HasNow: hn, HasPrev: hp, VolNow: v.now, VolPrev: v.prev})
	}
	for _, s := range byKind[svcHTTPREDLatency] {
		svc, ok := briefingGroupKey(s.GroupKey, svcLabel)
		if phi, _ := briefingGroupKey(s.GroupKey, "phi"); !ok || !in[svc] || phi != "0.95" {
			continue
		}
		n, p, hn, hp := briefing.SplitMeans(briefingPoints(s, 1), fromMs)
		rows = append(rows, briefing.ClientRow{Service: svc, Family: famID, Metric: briefing.ClientLatency, Now: n, Prev: p, HasNow: hn, HasPrev: hp})
	}
	return rows
}

// readBriefingHTTPGroup — takımın HTTP/gRPC sunucu ailesi, tek sorgu kümesi.
// failed = okuma hatası (etiket keşfi ya da bir sorgu); seri yok ≠ hata.
func (s *Server) readBriefingHTTPGroup(ctx context.Context, rd redFamilyReader, cands []string, env string, pFrom, from, to time.Time) (rows []briefing.ClientRow, failed bool) {
	svcs := briefingHTTPGroupServices(cands)
	if len(svcs) == 0 {
		return nil, false
	}
	vctx, cancel := context.WithTimeout(ctx, svcHTTPREDBudget)
	defer cancel()
	qTo := to.Truncate(time.Minute)
	qFrom := qTo.Add(-to.Sub(pFrom))
	step := metricChartStepS(int64(to.Sub(pFrom).Seconds()))
	probe := svcHTTPREDProbe{VM: true, GRPCChecked: true}
	in, ok := s.resolveSvcREDInputs(vctx, rd, svcs[0], env, qTo, &probe)
	if !ok {
		return nil, probe.LabelErr != nil
	}
	qs := briefingHTTPGroupQueries(in.fam, svcs, in.svcLabel, in.envMatcher, in.statusLabel, metricChartWinS(step))
	byKind := make(map[string][]chstore.SpanMetricSeries, len(qs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, q := range qs {
		wg.Add(1)
		go func(q svcHTTPREDQuery) {
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
			byKind[q.Kind] = raw
		}(q)
	}
	wg.Wait()
	return briefingHTTPGroupRows(in.fam, byKind, in.svcLabel, svcs, from.UnixMilli(), int(to.Sub(from).Seconds())), failed
}

// briefingClientVMQueries — SAF (R9 pini): kalem 2'nin takım × env başına VM
// range sorgusu sayısı (HTTP kümesi + Kafka).
func briefingClientVMQueries(qs []svcHTTPREDQuery) int { return len(qs) + briefingKafkaVMQueries }
