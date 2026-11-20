package ml

// v0.10.2392 — etki kuralı geri oynatma: 5 dakikalık sentetik seride epizot
// tespiti (başlangıç / süre / tepe), taban yoksa atlama + neden, yük kapısı,
// P1/P2 bölünmesi, yanıt süresi ve consumer lag, aday tavanı, bütçe. Saat ve
// saat dilimi bağımsız: sabit UTC anı, mevsimsel dilim "UTC".

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var rpNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) // çarşamba

type rpFake struct {
	cands     []chstore.ImpactReplayCandidate
	rows      []chstore.ImpactReplaySeries
	seriesErr error
	candReq   chstore.ImpactReplayCandidateReq
	seriesReq []chstore.ImpactReplaySeriesReq
}

func (f *rpFake) ImpactReplayCandidates(_ context.Context, req chstore.ImpactReplayCandidateReq) ([]chstore.ImpactReplayCandidate, error) {
	f.candReq = req
	return f.cands, nil
}

func (f *rpFake) ImpactReplaySeries(_ context.Context, req chstore.ImpactReplaySeriesReq) ([]chstore.ImpactReplaySeries, error) {
	f.seriesReq = append(f.seriesReq, req)
	if f.seriesErr != nil && len(f.seriesReq) > 1 {
		return nil, f.seriesErr
	}
	in := map[string]bool{}
	for _, s := range req.Services {
		in[s] = true
	}
	var out []chstore.ImpactReplaySeries
	for _, r := range f.rows {
		if in[r.Service] {
			out = append(out, r)
		}
	}
	return out, nil
}

// rpSeries — [from, to) arası 5 dk kovalar; f(t) NaN → kova yok.
func rpSeries(svc, part string, from, to time.Time, f func(t time.Time) float64) chstore.ImpactReplaySeries {
	r := chstore.ImpactReplaySeries{Service: svc, Part: part}
	for t := from; t.Before(to); t = t.Add(5 * time.Minute) {
		v := f(t)
		if v != v {
			continue
		}
		r.T = append(r.T, uint32(t.Unix()))
		r.V = append(r.V, v)
		r.Native = append(r.Native, 1)
	}
	return r
}

// rpBump — [at, at+d) içinde hi, dışında lo.
func rpBump(lo, hi float64, at time.Time, d time.Duration) func(time.Time) float64 {
	return func(t time.Time) float64 {
		if !t.Before(at) && t.Before(at.Add(d)) {
			return hi
		}
		return lo
	}
}

func rpConst(v float64) func(time.Time) float64 { return func(time.Time) float64 { return v } }

func rpReq(det string, days int) ImpactReplayRequest {
	cfg := chstore.DefaultMLConfig()
	cfg.BaselineDays = 7
	return ImpactReplayRequest{Detector: det, Days: days, Now: rpNow, Cfg: cfg,
		Sens: chstore.AnomalySensitivityConfig{SeasonalTimezone: "UTC"}, Prio: chstore.DefaultProblemPriority(),
		Clock: func() time.Time { return rpNow }}
}

func TestImpactReplayFailureRateEpisodes(t *testing.T) {
	hist := rpNow.Add(-17 * 24 * time.Hour)
	ratio, count := tracePart(svcRoute, svcMeasureRatio), tracePart(svcRoute, svcMeasureCount)
	incA := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	incB := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	f := &rpFake{
		cands: []chstore.ImpactReplayCandidate{{Service: "svc-a"}, {Service: "svc-b"}, {Service: "svc-c"}, {Service: "svc-d"}},
		rows: []chstore.ImpactReplaySeries{
			// taban %0,2 → eşik %1; %5 tepe → taban ×25 → P1
			rpSeries("svc-a", ratio, hist, rpNow, rpBump(0.002, 0.05, incA, 30*time.Minute)),
			rpSeries("svc-a", count, hist, rpNow, rpConst(10)),
			// taban %2 → eşik %3; %3,5 tepe → ×1,75 < 2 → P2
			rpSeries("svc-b", ratio, hist, rpNow, rpBump(0.02, 0.035, incB, 20*time.Minute)),
			rpSeries("svc-b", count, hist, rpNow, rpConst(10)),
			// geçmiş yok: taban örneği az → atlanır, nedenli
			rpSeries("svc-c", ratio, rpNow.Add(-2*24*time.Hour), rpNow, rpConst(0.5)),
			rpSeries("svc-c", count, rpNow.Add(-2*24*time.Hour), rpNow, rpConst(10)),
			// yük kapısı: 0,1 istek/sn = 6/dk < 10 → oran %50 olsa da ihlal yok
			rpSeries("svc-d", ratio, hist, rpNow, rpBump(0.002, 0.5, incA, time.Hour)),
			rpSeries("svc-d", count, hist, rpNow, rpConst(0.1)),
		},
	}
	res, err := ImpactReplay(context.Background(), f, rpReq(ImpactReplayFailureRate, 3))
	if err != nil {
		t.Fatal(err)
	}
	if res.To != rpNow.Unix() || res.From != rpNow.Add(-72*time.Hour).Unix() || res.BucketSec != 300 {
		t.Errorf("pencere: %d..%d kova %d", res.From, res.To, res.BucketSec)
	}
	if f.candReq.Signal != chstore.MLSignalTrace || f.candReq.MinValues[0] != 0.01 || f.candReq.MinLoadPerMin != 10 || f.candReq.Limit != ImpactReplaySubjectCap+1 {
		t.Errorf("aday isteği: %+v", f.candReq)
	}
	// geriye bakış = pencere başı − (taban günü + 1)
	if got := f.seriesReq[0].From; !got.Equal(time.Unix(res.From, 0).Add(-8 * 24 * time.Hour)) {
		t.Errorf("geriye bakış %v", got)
	}
	if len(res.Episodes) != 2 {
		t.Fatalf("epizot %d: %+v", len(res.Episodes), res.Episodes)
	}
	a, b := res.Episodes[0], res.Episodes[1] // en yeni önce
	if a.Service != "svc-a" || a.Start != incA.Unix() || a.DurationSec != 34*60 || a.Priority != "P1" || a.Open {
		t.Errorf("svc-a: %+v", a)
	}
	if a.Peak != 0.05 || a.Normal != 0.002 || math.Abs(a.Threshold-0.012) > 1e-9 || // max(%1, ×1,5, +1 puan)
		a.Measure != "failure_rate" {
		t.Errorf("svc-a tepe: %+v", a)
	}
	if b.Service != "svc-b" || b.Start != incB.Unix() || b.DurationSec != 24*60 || b.Priority != "P2" {
		t.Errorf("svc-b: %+v", b)
	}
	if res.Totals != (ImpactReplayTotals{Problems: 2, P1: 1, P2: 1, Services: 2, MedianSec: 29 * 60}) {
		t.Errorf("toplam: %+v", res.Totals)
	}
	if res.SkippedTotal != 1 || len(res.Skipped) != 1 || res.Skipped[0].Service != "svc-c" || !strings.Contains(res.Skipped[0].Reason, "taban örneği az") {
		t.Errorf("atlanan: %+v", res.Skipped)
	}
	if res.Candidates != 4 || res.Truncated || res.Partial || res.EpisodesTruncated {
		t.Errorf("bayraklar: %+v", res)
	}
	if len(res.Notes) < 2 || !strings.Contains(res.Notes[0], "Yaklaşık") {
		t.Errorf("notlar: %v", res.Notes)
	}
}

func TestImpactReplayOpenAtWindowEnd(t *testing.T) {
	hist := rpNow.Add(-17 * 24 * time.Hour)
	ratio, count := tracePart(svcRoute, svcMeasureRatio), tracePart(svcRoute, svcMeasureCount)
	at := rpNow.Add(-30 * time.Minute)
	f := &rpFake{cands: []chstore.ImpactReplayCandidate{{Service: "svc-a"}}, rows: []chstore.ImpactReplaySeries{
		rpSeries("svc-a", ratio, hist, rpNow, rpBump(0.002, 0.05, at, time.Hour)),
		rpSeries("svc-a", count, hist, rpNow, rpConst(10)),
	}}
	res, err := ImpactReplay(context.Background(), f, rpReq(ImpactReplayFailureRate, 1))
	if err != nil || len(res.Episodes) != 1 {
		t.Fatalf("err %v epizot %+v", err, res.Episodes)
	}
	if e := res.Episodes[0]; !e.Open || e.End != rpNow.Unix() || e.Start != at.Unix() {
		t.Errorf("açık epizot: %+v", e)
	}
}

func TestImpactReplayResponseTimeAndLag(t *testing.T) {
	hist := rpNow.Add(-17 * 24 * time.Hour)
	inc := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	f := &rpFake{cands: []chstore.ImpactReplayCandidate{{Service: "svc-r"}}, rows: []chstore.ImpactReplaySeries{
		rpSeries("svc-r", tracePart(svcRoute, svcMeasureP50), hist, rpNow, rpBump(50, 400, inc, 15*time.Minute)),
		rpSeries("svc-r", tracePart(svcRoute, svcMeasureP90), hist, rpNow, rpConst(200)),
		rpSeries("svc-r", tracePart(svcRoute, svcMeasureCount), hist, rpNow, rpConst(5)),
	}}
	res, err := ImpactReplay(context.Background(), f, rpReq(ImpactReplayResponseTime, 3))
	if err != nil || len(res.Episodes) != 1 {
		t.Fatalf("err %v epizot %+v", err, res.Episodes)
	}
	if e := res.Episodes[0]; e.Measure != "response_median" || e.Peak != 400 || e.Normal != 50 || e.Priority != "P1" || e.Start != inc.Unix() {
		t.Errorf("yanıt süresi: %+v", e)
	}
	if got := f.candReq.MinValues; len(got) != 2 || got[0] != 100 || got[1] != 1000 {
		t.Errorf("alt sınırlar %v", got)
	}

	lagPart := metricPart(detMetricKafka, kafkaLagExprID) + kafkaTopicSep + "orders"
	g := &rpFake{cands: []chstore.ImpactReplayCandidate{{Service: "svc-q", Part: lagPart}}, rows: []chstore.ImpactReplaySeries{
		rpSeries("svc-q", lagPart, hist, rpNow, rpBump(100, 50000, inc, 20*time.Minute)),
		rpSeries("svc-q", metricPart(detMetricKafka, kafkaLagExprID)+kafkaTopicSep+"other", hist, rpNow, rpBump(100, 50000, inc, 20*time.Minute)),
	}}
	res, err = ImpactReplay(context.Background(), g, rpReq(ImpactReplayConsumerLag, 3))
	if err != nil || len(res.Episodes) != 1 {
		t.Fatalf("lag: err %v epizot %+v", err, res.Episodes)
	}
	// aday olmayan topic oynatılmaz; lag Value = Threshold → P2
	if e := res.Episodes[0]; e.Subject != lagPart || e.Measure != "consumer_lag" || e.Priority != "P2" {
		t.Errorf("lag: %+v", e)
	}
	if g.candReq.Signal != chstore.MLSignalMetric || g.candReq.PartPrefix == "" {
		t.Errorf("lag aday isteği: %+v", g.candReq)
	}
}

func TestImpactReplayCapsBudgetAndPartial(t *testing.T) {
	var cands []chstore.ImpactReplayCandidate
	for i := 0; i < ImpactReplaySubjectCap+1; i++ {
		cands = append(cands, chstore.ImpactReplayCandidate{Service: "svc-" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)})
	}
	f := &rpFake{cands: cands}
	res, err := ImpactReplay(context.Background(), f, rpReq(ImpactReplayFailureRate, 1))
	if err != nil || !res.Truncated || res.Candidates != ImpactReplaySubjectCap {
		t.Fatalf("tavan: err %v trunc %v aday %d", err, res.Truncated, res.Candidates)
	}
	if len(f.seriesReq) != ImpactReplaySubjectCap/impactReplayChunk {
		t.Errorf("parça sayısı %d", len(f.seriesReq))
	}
	// bütçe: saat her çağrıda 6 sn ilerler → ikinci parçadan önce aşılır
	now := rpNow
	req := rpReq(ImpactReplayFailureRate, 1)
	req.Clock = func() time.Time { now = now.Add(6 * time.Second); return now }
	f = &rpFake{cands: cands}
	res, err = ImpactReplay(context.Background(), f, req)
	if err != nil || !res.Partial || len(f.seriesReq) != 1 || !strings.Contains(res.Notes[len(res.Notes)-1], "kısmi") {
		t.Errorf("bütçe: err %v kısmi %v parça %d", err, res.Partial, len(f.seriesReq))
	}
	// sonraki parçanın okuması düşerse kısmi; ilk parça düşerse hata
	f = &rpFake{cands: cands, seriesErr: errors.New("zaman aşımı")}
	if res, err = ImpactReplay(context.Background(), f, rpReq(ImpactReplayFailureRate, 1)); err != nil || !res.Partial {
		t.Errorf("ikinci parça hatası: err %v kısmi %v", err, res.Partial)
	}
	if res.Episodes == nil || res.Skipped == nil || res.Notes == nil {
		t.Error("nil dilim (FE .map)")
	}
}

func TestImpactReplayDetectorOK(t *testing.T) {
	for d, want := range map[string]bool{"failure_rate": true, "response_time": true, "consumer_lag": true, "": false, "svc_error_rate": false} {
		if ImpactReplayDetectorOK(d) != want {
			t.Errorf("%q", d)
		}
	}
}
