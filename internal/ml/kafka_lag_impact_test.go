package ml

// kafka_lag_impact_test.go — v0.10.2382 (etki olayı programı, dilim I5).
// SÖZLEŞME: VM kaynaklı metric_kafka consumer_lag kararı 5 dakikalık kova +
// medyan/MAD ile DEĞİL, 1 dakikalık örnekler ve etki kuralıyla verilir
// (impactDecide, impactConsumerLag: son 5 tam dakikanın en az 3'ünde lag ≥
// max(1.000 kayıt, taban medyanı × 2), en son ölçülen dakika da ihlal). Taban
// 5 dakikalık serinin mevsimsel medyanıdır; nokta olmayan dakika ölçülmemiştir;
// açık olay pencerede ihlal kaldıkça uzar, kimliği ve başlangıcı oynamaz.
// Dakikalık yoklama yeni ateşleyen lag için TEK erken tik açar. Testler sahte
// saatle koşar: gerçek uyku yok, saat ve saat dilimi bağımsız.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const lagFam = "metric_kafka"

var lagSvcPart = metricPart(lagFam, kafkaLagExprID)

// lagRows — VM aralık yanıtı biçiminde satır; nokta zamanı dakikanın SONU.
// vals: dakika başı (unix sn) → o dakikanın kapanış okuması. topic "" → etiket yok.
func lagRows(svc, topic string, vals map[int64]float64) chstore.SpanMetricSeries {
	r := chstore.SpanMetricSeries{GroupKey: []string{fmt.Sprintf("service_name=%q", svc)}}
	if topic != "" {
		r.GroupKey = append(r.GroupKey, fmt.Sprintf("topic=%q", topic))
	}
	ms := make([]int64, 0, len(vals))
	for m := range vals {
		ms = append(ms, m)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	for _, m := range ms {
		r.Points = append(r.Points, chstore.SpanMetricPoint{Time: (m + impactStepSec) * 1e9, Value: vals[m]})
	}
	return r
}

func lagS(v float64) impactSample { return impactSample{Value: v, OK: true} }

// Operatörün ifadesi 1 dk adımla örneklenebilir mi: pencere ({win}, [5m], alt
// sorgu) ya da rollup işlevi taşıyan ifade eski 5 dakikalık yolda kalır.
func TestLagExprMinuteSafe(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{"max by ({svc}) (kafka_consumer_records_lag_max)", true},
		{`sum by ({svc}) (kafka_consumergroup_lag{topic!~"audit\\[.*"})`, true}, // köşeli parantez dize içinde
		{`max by ({svc}) (kafka_consumer_records_lag_max{topic=~"orders[.]events"})`, true},
		{"max by ({svc}) (max_over_time(kafka_consumer_records_lag_max[{win}]))", false},
		{"max by ({svc}) (avg_over_time(kafka_consumer_records_lag_max[10m]))", false},
		{"max by ({svc}) (kafka_consumer_records_lag_max)[30m:1m]", false},
		{"max by ({svc}) (deriv(kafka_consumer_records_lag_max))", false}, // MetricsQL örtük pencere
		{"max by ({svc}) (max_over_time(kafka_consumer_records_lag_max))", false},
		{"", false},
	} {
		if got := lagExprMinuteSafe(tc.expr); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.expr, got, tc.want)
		}
	}
	cfg := chstore.DefaultMLConfig()
	if expr, ok := lagImpactExpr(cfg); !ok || expr != "max by ({svc}) (kafka_consumer_records_lag_max)" {
		t.Fatalf("varsayılan ifade etki kuralına girmeli: %q %v", expr, ok)
	}
	fams := map[string][]chstore.MLMetricExpr{}
	for k, v := range cfg.MetricFamilies {
		fams[k] = append([]chstore.MLMetricExpr(nil), v...)
	}
	for i, e := range fams[lagFam] {
		if e.ID == kafkaLagExprID {
			fams[lagFam][i].Expr = "max by ({svc}) (max_over_time(kafka_consumer_records_lag_max[{win}]))"
		}
	}
	cfg.MetricFamilies = fams
	if _, ok := lagImpactExpr(cfg); ok {
		t.Fatal("pencereli operatör ifadesi etki kuralına girmemeli (eski 5 dk yol)")
	}
}

// Aralık satırları → anahtar × dakika başı. Topic kipinde part topic'li,
// servis kipinde servis düzeyi; aynı anahtara iki satır → en büyük; NaN,
// pencere dışı nokta ve (topic kipinde) topic'siz satır yok sayılır.
func TestLagMinuteRows(t *testing.T) {
	end := time.Date(2026, 11, 20, 10, 10, 0, 0, time.UTC).Unix()
	m := func(i int) int64 { return end - int64(impactWindow-i)*impactStepSec } // pencerenin i. dakikası
	rows := []chstore.SpanMetricSeries{
		lagRows("orders", "orders.events", map[int64]float64{m(0): 10, m(4): 5000, m(0) - 60: 99, end: 77}),
		lagRows("orders", "orders.events", map[int64]float64{m(4): 7000, m(1): math.NaN()}),
		lagRows("orders", "", map[int64]float64{m(2): 1}),
		lagRows("", "orders.events", map[int64]float64{m(2): 1}),
	}
	got := lagMinuteRows(rows, lagQuery{svcLabel: "service_name", topic: true}, end)
	k := chstore.MLSeriesKey("orders", kafkaTopicPart("orders.events"))
	if len(got) != 1 || len(got[k]) != 2 || got[k][m(0)] != 10 || got[k][m(4)] != 7000 {
		t.Fatalf("topic kipi: %v", got)
	}
	svc := lagMinuteRows(rows[2:3], lagQuery{svcLabel: "service_name"}, end)
	if len(svc) != 1 || svc[chstore.MLSeriesKey("orders", lagSvcPart)][m(2)] != 1 {
		t.Fatalf("servis kipi: %v", svc)
	}
	s := lagImpactSamples(got[k], end)
	if len(s) != impactWindow || !s[0].OK || s[0].Value != 10 || s[1].OK || s[2].OK || s[3].OK || !s[4].OK || s[4].Value != 7000 {
		t.Fatalf("örnekler (nokta yok = ölçülmedi): %+v", s)
	}
	if !isLagImpactPart(lagSvcPart) || !isLagImpactPart(kafkaTopicPart("orders.events")) ||
		isLagImpactPart(lagSvcPart+"@prod-east") || isLagImpactPart(metricPart(lagFam, "fetch_throttle")) {
		t.Fatal("etki kuralı yalnız VM consumer_lag part'larını sahiplenir (Thanos / diğer ölçüler değil)")
	}
}

func TestLagImpactDecide(t *testing.T) {
	base600, base0 := lagBaseOf(repeat(600, 30)), lagBaseOf(repeat(0, 30))
	for _, tc := range []struct {
		name          string
		samples       []impactSample
		base          lagBase
		open          bool
		fire, emit    bool
		thr           float64
		viol          int
		clear, silent bool
	}{
		{"taban 600: 3 dakika ≥ 1.200 ve en yenisi ihlal", []impactSample{lagS(600), lagS(700), lagS(1200), lagS(5000), lagS(165000)}, base600, false, true, true, 1200, 3, false, false},
		{"taban 600: 1.100 göreli eşiğin altında", []impactSample{lagS(1100), lagS(1100), lagS(1100), lagS(1100), lagS(1100)}, base600, false, false, false, 1200, 0, true, false},
		{"taban 0: eşik etki tabanı 1.000", []impactSample{lagS(0), lagS(0), lagS(1000), lagS(1500), lagS(2000)}, base0, false, true, true, 1000, 3, false, false},
		{"taban 0: 999 ihlal değil", []impactSample{lagS(999), lagS(999), lagS(999), lagS(999), lagS(999)}, base0, false, false, false, 1000, 0, true, false},
		{"yalnız 2 ihlal", []impactSample{lagS(0), lagS(0), lagS(0), lagS(5000), lagS(5000)}, base0, false, false, false, 1000, 2, false, false},
		{"3 ihlal ama en yeni dakika düzelmiş: yeni olay yok", []impactSample{lagS(5000), lagS(5000), lagS(5000), lagS(0), lagS(0)}, base0, false, false, false, 1000, 3, false, false},
		{"aynı pencere, olay açık: uzar", []impactSample{lagS(5000), lagS(5000), lagS(5000), lagS(0), lagS(0)}, base0, true, false, true, 1000, 3, false, false},
		{"açık, pencerede tek ihlal: uzar", []impactSample{lagS(5000), lagS(0), lagS(0), lagS(0), lagS(0)}, base0, true, false, true, 1000, 1, false, false},
		{"açık, pencere temiz: kapanabilir", []impactSample{lagS(0), lagS(0), lagS(0), lagS(0), lagS(0)}, base0, true, false, false, 1000, 0, true, false},
		{"ölçülmeyen dakika ihlal de temiz de değil", []impactSample{{}, lagS(5000), {}, lagS(5000), lagS(5000)}, base0, false, true, true, 1000, 3, false, false},
		{"hiç ölçüm yok", make([]impactSample, impactWindow), base0, true, false, false, 1000, 0, false, false},
		{"taban örneği az: sessiz (açık olay da uzamaz)", []impactSample{lagS(9e5), lagS(9e5), lagS(9e5), lagS(9e5), lagS(9e5)}, lagBaseOf(repeat(600, 19)), true, false, false, 0, 0, false, true},
	} {
		v := lagImpactDecide(tc.samples, tc.base, 20, tc.open)
		if v.Fire != tc.fire || v.Emit != tc.emit || v.Threshold != tc.thr || v.Violations != tc.viol || v.Clear != tc.clear {
			t.Errorf("%s: fire=%v emit=%v thr=%v viol=%d clear=%v (%s)", tc.name, v.Fire, v.Emit, v.Threshold, v.Violations, v.Clear, v.Reason)
		}
		if tc.silent != strings.HasPrefix(v.Reason, "eksik veri") {
			t.Errorf("%s: gerekçe %q", tc.name, v.Reason)
		}
	}
}

// Olay alanları: kimlik eski lag olayıyla aynı (koşu sürer), başlangıç ilk
// ihlal dakikasının başı, son görülme pencere bitişi, kanıt kuralı açıklar ve
// "dwell" iddia etmez.
func TestLagImpactEventFields(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 20, 12, 5, 0, 0, time.UTC)
	part := kafkaTopicPart("orders.events")
	samples := []impactSample{lagS(600), {}, lagS(40000), lagS(90000), lagS(165000)}
	v := lagImpactDecide(samples, lagBaseOf(repeat(600, 30)), 20, false)
	ev := lagImpactEvent("orders", part, samples, v, end, 0, cfg, "weekday")
	onset := end.Add(-3 * time.Minute)
	if ev.ID != chstore.MLEventID(lagFam, "orders", part) || ev.Detector != lagFam || ev.Code != part || ev.Signal != chstore.MLSignalMetric ||
		ev.Direction != "up" || ev.Stage != chstore.MLStageShadow {
		t.Fatalf("kimlik/alanlar: %+v", ev)
	}
	if ev.StartedAt != onset.UnixNano() || ev.LastSeen != end.UnixNano() || ev.WindowSec != uint32(cfg.BucketSec) {
		t.Fatalf("zaman: started=%d last=%d window=%d", ev.StartedAt, ev.LastSeen, ev.WindowSec)
	}
	if ev.Observed != 165000 || ev.Baseline != 600 || ev.Score < cfg.CriticalZ {
		t.Fatalf("değerler: %+v", ev)
	}
	m := evidenceMap(ev.Evidence)
	vals, _ := m["values"].([]any)
	if m["rule"] != "impact" || m["fired"] != true || m["threshold"] != 1200.0 || m["violations"] != 3.0 || m["window"] != 5.0 ||
		m["minViolations"] != 3.0 || m["sampleSec"] != 60.0 || m["minuteFrom"] != float64(end.Unix()-300) || m["source"] != "vm" || m["expr"] != part ||
		len(vals) != 5 || vals[0] != 600.0 || vals[1] != nil || vals[4] != 165000.0 {
		t.Fatalf("kanıt: %v", m)
	}
	if _, ok := m["dwell"]; ok {
		t.Fatal("kanıt 5 dakikalık kova sürekliliği (dwell) iddia etmemeli")
	}
	mark, ok := impactMarkOf(ev.Evidence)
	if !ok || !mark.Fired || mark.Threshold != 1200 || mark.Violations != 3 || mark.Window != 5 {
		t.Fatalf("kapının okuduğu damga: %+v %v", mark, ok)
	}
	if _, ok := impactMarkOf(`{"source":"vm","dwell":2}`); ok {
		t.Fatal("etki kuralı damgası olmayan kanıt tanınmamalı")
	}
	// açık koşunun yazılmış başlangıcı daha erkense o kalır; daha geçse ilk ihlal dakikası
	early := onset.Add(-7 * time.Minute).UnixNano()
	if got := lagImpactEvent("orders", part, samples, v, end, early, cfg, "weekday").StartedAt; got != early {
		t.Fatalf("pencere kayarken başlangıç oynamamalı: %d", got)
	}
	if got := lagImpactEvent("orders", part, samples, v, end, end.UnixNano(), cfg, "weekday").StartedAt; got != onset.UnixNano() {
		t.Fatalf("başlangıç ilk ihlalden geç olamaz: %d", got)
	}
}

// ── kayan pencere simülasyonu ────────────────────────────────────────────

// lagSim — dakika dakika lag üreten sahte kaynak + gerçek koşu izleyicisi:
// her tik VM satırı → dakika haritası → karar → kalite süzgeci → kapanış.
type lagSim struct {
	d     *MetricML
	tr    *tracker
	cfg   chstore.MLConfig
	t0    time.Time
	topic bool
	base  map[string][]float64
	lag   map[[2]string]func(min int) (float64, bool) // {servis, topic} → dakika → kapanış okuması
}

func newLagSim(tickSec int, topic bool) *lagSim {
	cfg := chstore.DefaultMLConfig()
	cfg.TickSec, cfg.IngestLagSec = tickSec, 120
	return &lagSim{d: NewMetricML(), tr: newTracker(), cfg: cfg, t0: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), topic: topic,
		base: map[string][]float64{}, lag: map[[2]string]func(int) (float64, bool){}}
}

func (s *lagSim) part(topic string) string {
	if topic == "" {
		return lagSvcPart
	}
	return kafkaTopicPart(topic)
}

func (s *lagSim) series(svc, topic string, baseLag float64, f func(min int) (float64, bool)) {
	s.base[chstore.MLSeriesKey(svc, s.part(topic))] = repeat(baseLag, 30)
	s.lag[[2]string{svc, topic}] = f
}

// rows — [end − 5 dk, end) penceresinin VM satırları (ad sırasıyla).
func (s *lagSim) rows(end time.Time) []chstore.SpanMetricSeries {
	keys := make([][2]string, 0, len(s.lag))
	for k := range s.lag {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"|"+keys[i][1] < keys[j][0]+"|"+keys[j][1] })
	var out []chstore.SpanMetricSeries
	for _, k := range keys {
		vals := map[int64]float64{}
		for i := 0; i < impactWindow; i++ {
			m := end.Unix() - int64(impactWindow-i)*impactStepSec
			if v, ok := s.lag[k](int((m - s.t0.Unix()) / 60)); ok {
				vals[m] = v
			}
		}
		if len(vals) > 0 {
			out = append(out, lagRows(k[0], k[1], vals))
		}
	}
	return out
}

// tick — t0 + k dk + 30 sn anındaki tik. Döner: yazılan olaylar, kapanışlar.
func (s *lagSim) tick(k int) (kept, closing []chstore.MLEvent) {
	now := s.t0.Add(time.Duration(k)*time.Minute + 30*time.Second)
	tick := time.Duration(s.cfg.TickSec) * time.Second
	end := svcImpactEnd(now, s.cfg.IngestLagSec)
	minute := lagMinuteRows(s.rows(end), lagQuery{svcLabel: "service_name", topic: s.topic}, end.Unix())
	deps := Deps{openRun: func(id string) bool { return s.tr.openRun(id, now, tick) },
		runStart: func(id string) int64 { return s.tr.startedOf(id, now, tick) }}
	evs := s.d.lagImpactEvents(deps, minute, s.base, end, s.cfg, "weekday", now)
	kept, _ = qualityFilter(evs, s.cfg, s.tr, now)
	closing, _ = s.tr.closeStale(now, tickCoverage{})
	return kept, closing
}

// lagIncident — [from, to) dakikalarında lag high, dışında low.
func lagIncident(from, to int, low, high float64) func(int) (float64, bool) {
	return func(m int) (float64, bool) {
		if m >= from && m < to {
			return high, true
		}
		return low, true
	}
}

// Dakikalık tik, topic kırılımı: lag büyür → ateşler → uzar → temizlenir.
// orders/orders.events (taban 600) 10:10–10:20 arası 165.000; checkout/
// checkout.events (taban 0) 10:12–10:16 arası 5.000; orders/orders.audit
// (taban 600) hep 900: mutlak tabanın altında ve 2 katına varmıyor → sessiz.
func TestLagImpactSlidingWindowMinuteTicks(t *testing.T) {
	s := newLagSim(60, true)
	s.cfg.DailyEventCap = 2 // uzatmalar tavan harcamaz: iki olay, iki hak
	s.series("orders", "orders.events", 600, lagIncident(10, 20, 600, 165000))
	s.series("orders", "orders.audit", 600, lagIncident(0, 0, 900, 0))
	s.series("checkout", "checkout.events", 0, lagIncident(12, 16, 0, 5000))
	type want struct {
		fireFrom, fireTo, extendTo, closeAt int // tik indisleri (dahil)
		onset                               int64
		base, high, low                     float64
		code                                string
	}
	wants := map[string]want{
		// ilk olay: üçüncü ihlal dakikası ingest payını geçince (10:12 → 10:15:30 tiki)
		"orders":   {15, 22, 26, 29, s.t0.Add(10 * time.Minute).UnixNano(), 600, 165000, 600, kafkaTopicPart("orders.events")},
		"checkout": {17, 18, 22, 25, s.t0.Add(12 * time.Minute).UnixNano(), 0, 5000, 0, kafkaTopicPart("checkout.events")},
	}
	ids, closed := map[string]string{}, map[string]int{}
	for k := 5; k <= 40; k++ {
		kept, closing := s.tick(k)
		seen := map[string]chstore.MLEvent{}
		for _, e := range kept {
			w, ok := wants[e.Service]
			if !ok || e.Code != w.code {
				t.Fatalf("k=%d beklenmeyen olay (sağlıklı topic?): %s %s", k, e.Service, e.Code)
			}
			seen[e.Service] = e
		}
		for svc, w := range wants {
			e, got := seen[svc]
			if want := k >= w.fireFrom && k <= w.extendTo; got != want {
				t.Fatalf("k=%d %s: olay=%v beklenen=%v", k, svc, got, want)
			}
			if !got {
				continue
			}
			if ids[svc] == "" {
				ids[svc] = e.ID
			}
			if e.ID != ids[svc] || e.StartedAt != w.onset {
				t.Fatalf("k=%d %s kimlik/başlangıç oynadı: id=%s started=%d (want %s / %d)", k, svc, e.ID, e.StartedAt, ids[svc], w.onset)
			}
			fired, _ := evidenceMap(e.Evidence)["fired"].(bool)
			wantObs := w.high
			if k > w.fireTo { // uzatma: en yeni dakika tabana dönmüş; kalite süzgeci (etki eşiği, lag tabanı) kesmemeli
				wantObs = w.low
			}
			if fired != (k <= w.fireTo) || e.Observed != wantObs || e.Baseline != w.base {
				t.Fatalf("k=%d %s fired=%v observed=%v baseline=%v", k, svc, fired, e.Observed, e.Baseline)
			}
			if wantLast := s.t0.Add(time.Duration(k-2) * time.Minute).UnixNano(); e.LastSeen != wantLast {
				t.Fatalf("k=%d %s last_seen=%d want %d", k, svc, e.LastSeen, wantLast)
			}
		}
		for _, c := range closing {
			closed[c.Service]++
			w := wants[c.Service]
			if k != w.closeAt || c.ID != ids[c.Service] || c.Stage != chstore.MLStageClosed || c.StartedAt != w.onset {
				t.Fatalf("k=%d kapanış: %+v", k, c)
			}
		}
	}
	if closed["orders"] != 1 || closed["checkout"] != 1 || ids["orders"] == ids["checkout"] {
		t.Fatalf("servis başına tek kapanış ve ayrı kimlik bekleniyordu: %v %v", closed, ids)
	}
}

// 5 dakikalık tik, servis düzeyi seri (topic etiketi yok): pencere tikten tike
// kayar (örtüşmez) ama başlangıç ilk ihlal dakikasında kalır; dalgalı lag
// (pencerede tek ihlal) ve scrape boşluğu olayı kapatıp yeniden açmaz.
func TestLagImpactSlidingWindowFiveMinuteTicks(t *testing.T) {
	s := newLagSim(300, false)
	s.series("orders", "", 600, func(m int) (float64, bool) {
		switch {
		case m >= 10 && m < 18: // sürekli birikme
			return 40000, true
		case m >= 18 && m < 33 && m%4 == 0: // dalgalı: 4 dakikada bir
			return 40000, true
		case m == 21: // scrape boşluğu
			return 0, false
		}
		return 600, true
	})
	onset := s.t0.Add(10 * time.Minute).UnixNano()
	var id string
	for k := 5; k <= 60; k += 5 {
		kept, closing := s.tick(k)
		switch {
		case k < 15:
			if len(kept) != 0 {
				t.Fatalf("k=%d erken olay", k)
			}
		case k <= 35: // 15, 20 ateşler; 25, 30, 35 pencerede en az bir ihlal → uzar
			if len(kept) != 1 {
				t.Fatalf("k=%d olay bekleniyordu, %d geldi", k, len(kept))
			}
			if id == "" {
				id = kept[0].ID
			}
			if kept[0].ID != id || kept[0].StartedAt != onset || kept[0].Code != lagSvcPart {
				t.Fatalf("k=%d kimlik/başlangıç oynadı: %+v", k, kept[0])
			}
		default:
			if len(kept) != 0 {
				t.Fatalf("k=%d temiz pencerede olay", k)
			}
		}
		if len(closing) > 0 && k != 50 {
			t.Fatalf("k=%d beklenmeyen kapanış (3 kaçırılan tik: 40, 45, 50)", k)
		}
	}
}

// Günlük tavan yalnız YENİ olayı sayar; tavan doluyken açık olay uzar.
func TestLagImpactDayCap(t *testing.T) {
	s := newLagSim(60, true)
	s.cfg.DailyEventCap = 1
	s.series("checkout", "checkout.events", 0, lagIncident(10, 40, 0, 5000))
	s.series("orders", "orders.events", 0, lagIncident(20, 40, 0, 5000))
	seen := map[string]int{}
	for k := 5; k <= 35; k++ {
		kept, _ := s.tick(k)
		for _, e := range kept {
			seen[e.Service]++
		}
	}
	if seen["checkout"] != 21 || seen["orders"] != 0 {
		t.Fatalf("tavan: checkout 15..35 arası her tik (21), orders hiç: %v", seen)
	}
}

// Eski 5 dakikalık karar bu ölçü için YOK: etki kuralının sahiplendiği part
// kova yargısına girmez; ailenin diğer ölçüleri aynen yargılanır.
func TestJudgeMetricSeriesSkipsOwnedLag(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	last := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	series, base := map[string]map[int64]float64{}, map[string][]float64{}
	lagKey := chstore.MLSeriesKey("orders", kafkaTopicPart("orders.events"))
	otherPart := metricPart(lagFam, "fetch_throttle")
	otherKey := chstore.MLSeriesKey("orders", otherPart)
	for _, k := range []string{lagKey, otherKey} {
		series[k] = map[int64]float64{}
		for i := 0; i < cfg.DwellBuckets; i++ {
			series[k][last.Unix()-int64(i*cfg.BucketSec)] = 90000
		}
		base[k] = repeat(600, 30)
	}
	dirOf := map[string]string{kafkaTopicPart("orders.events"): "up", otherPart: "up"}
	all := func(string) bool { return true }
	none := func(string) map[string]any { return nil }
	evs := judgeMetricSeries(Deps{}, cfg, last, series, base, "weekday", dirOf, all, none, nil)
	if len(evs) != 2 {
		t.Fatalf("sahiplenme yokken iki seri de kova yargısıyla ateşlemeli (senaryo): %d", len(evs))
	}
	evs = judgeMetricSeries(Deps{}, cfg, last, series, base, "weekday", dirOf, all, none, isLagImpactPart)
	if len(evs) != 1 || evs[0].Code != otherPart {
		t.Fatalf("lag part'ı kova yargısına girmemeli, diğer ölçü girmeli: %+v", evs)
	}
}

// ── sorgu: tek aralık sorgusu, yalıtım ───────────────────────────────────

// lagVM — dakika dakika lag üreten sahte VM; aralık sorgularını sayar.
type lagVM struct {
	fakeVM
	t0       time.Time
	lag      map[[2]string]func(min int) (float64, bool)
	ranges   int
	fail     error
	query    string
	from, to int64
	step     int
}

func (f *lagVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.ranges++
	f.query, f.from, f.to, f.step = q, from.Unix(), to.Unix(), step
	if f.fail != nil {
		return nil, f.fail
	}
	keys := make([][2]string, 0, len(f.lag))
	for k := range f.lag {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"|"+keys[i][1] < keys[j][0]+"|"+keys[j][1] })
	var out []chstore.SpanMetricSeries
	for _, k := range keys {
		vals := map[int64]float64{}
		for t := from.Unix(); t <= to.Unix(); t += int64(step) {
			m := t - impactStepSec // nokta dakikanın sonu
			if v, ok := f.lag[k](int((m - f.t0.Unix()) / 60)); ok {
				vals[m] = v
			}
		}
		if len(vals) > 0 {
			out = append(out, lagRows(k[0], k[1], vals))
		}
	}
	return out, nil
}

var lagTopicQ = lagQuery{expr: kafkaTopicExpr("service_name"), svcLabel: "service_name", topic: true}

func lagProbeCfg() chstore.MLConfig {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	cfg.Detectors[lagFam] = true
	return cfg
}

func lagProbeBase(v float64, n int, keys ...[2]string) map[string][]float64 {
	base := map[string][]float64{}
	for _, k := range keys {
		base[chstore.MLSeriesKey(k[0], kafkaTopicPart(k[1]))] = repeat(v, n)
	}
	return base
}

// Tik / yoklama başına TEK aralık sorgusu (tüm servisler, 5 nokta), dedektörün
// kendi ifadesi; hata KENDİ backoff ailesine yazılır (metric:metric_kafka
// anlık sorguları beklemeye girmez) ve yalnız "ölçülemedi" notu olur.
func TestLagImpactFetchIsOneIsolatedRangeQuery(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	now := t0.Add(17*time.Minute + 30*time.Second)
	end := svcImpactEnd(now, 120)
	vm := &lagVM{t0: t0, lag: map[[2]string]func(int) (float64, bool){{"orders", "orders.events"}: lagIncident(10, 40, 600, 165000)}}
	d := NewMetricML()
	bo := newVMBackoff()
	deps := Deps{VM: vm, Backoff: bo, meter: newTickMeter()}
	minute, err := d.lagMinutes(context.Background(), deps, lagTopicQ, end, now)
	if err != nil || vm.ranges != 1 || vm.query != `max by (service_name, topic) (kafka_consumer_records_lag_max{topic!=""})` ||
		vm.from != end.Add(-4*time.Minute).Unix() || vm.to != end.Unix() || vm.step != 60 {
		t.Fatalf("tek sorgu, 5 nokta: err=%v n=%d q=%q from=%d to=%d step=%d", err, vm.ranges, vm.query, vm.from, vm.to, vm.step)
	}
	for i, sm := range lagImpactSamples(minute[chstore.MLSeriesKey("orders", kafkaTopicPart("orders.events"))], end.Unix()) {
		if !sm.OK || sm.Value != 165000 {
			t.Fatalf("örnek %d: %+v", i, sm)
		}
	}
	if d.Unmeasured() != nil {
		t.Fatal("hata yokken ölçülemedi notu olmamalı")
	}
	// ağır hata: kendi ailesi beklemeye girer, aile sorguları girmez
	vm.fail = errors.New("HTTP 422 maxSamplesPerQuery")
	_, err = d.lagMinutes(context.Background(), deps, lagTopicQ, end, now)
	d.setLagNote(err)
	if err == nil || !bo.skipping(lagImpactBackoff, now.Add(time.Second)) || bo.skipping("metric:"+lagFam, now.Add(time.Second)) {
		t.Fatalf("etki sorgusu kendi ailesinde bekler: err=%v", err)
	}
	if u := d.Unmeasured(); len(u) != 1 || !strings.HasPrefix(u[lagFam], "kısmen ölçülüyor: ") {
		t.Fatalf("yalnız metric_kafka 'kısmen' görünmeli: %v", u)
	}
	// beklemede: VM'e gidilmez, sessiz
	q := vm.ranges
	if m, err := d.lagMinutes(context.Background(), deps, lagTopicQ, end, now.Add(time.Second)); !errors.Is(err, errLagImpactWaiting) || m != nil || vm.ranges != q {
		t.Fatalf("beklemedeki etki ailesi VM'e gitmemeli: %v %v sorgu=%d→%d", m, err, q, vm.ranges)
	}
	d.setLagNote(nil)
	if d.Unmeasured() != nil {
		t.Fatal("başarıda not silinmeli")
	}
}

// ── yoklama (I2 arayüzü) ─────────────────────────────────────────────────

// Dedektörün yoklaması: taban özeti ve ifade son tikten; karar tikle aynı.
func TestLagImpactProbe(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	now := t0.Add(11*time.Minute + 30*time.Second) // pencere bitişi 10:09 → dakika 4..8
	cfg := lagProbeCfg()
	ctx := context.Background()
	tick := time.Duration(cfg.TickSec) * time.Second
	orders, checkout := [2]string{"orders", "orders.events"}, [2]string{"checkout", "checkout.events"}
	vm := &lagVM{t0: t0, lag: map[[2]string]func(int) (float64, bool){orders: lagIncident(6, 40, 600, 165000), checkout: lagIncident(0, 0, 600, 0)}}
	d := NewMetricML()
	deps := Deps{VM: vm, Backoff: newVMBackoff()}
	var _ impactProber = d

	// son tikin durumu yok → aday yok, VM'e de gidilmez
	if c, err := d.probeImpact(ctx, deps, cfg, now); err != nil || len(c) != 0 || vm.ranges != 0 {
		t.Fatalf("taban yokken yoklama sessiz ve sorgusuz olmalı: %v %v sorgu=%d", c, err, vm.ranges)
	}
	// taban var: orders ateşler, sağlıklı checkout ateşlemez; TEK sorgu
	d.cacheLagImpact(lagTopicQ, lagProbeBase(600, 30, orders, checkout), now.Add(-time.Minute))
	c, err := d.probeImpact(ctx, deps, cfg, now)
	if err != nil || candServices(c) != "orders" || c[0].BaseID != chstore.MLEventID(lagFam, "orders", kafkaTopicPart("orders.events")) || vm.ranges != 1 {
		t.Fatalf("orders adayı bekleniyordu: %v %v sorgu=%d", c, err, vm.ranges)
	}
	// kural henüz ateşlemiyor (2 ihlal dakikası)
	if c, _ := d.probeImpact(ctx, deps, cfg, now.Add(-time.Minute)); len(c) != 0 {
		t.Fatalf("2 ihlalde aday olmamalı: %v", c)
	}
	// taban örneği az → sessiz
	d.cacheLagImpact(lagTopicQ, lagProbeBase(600, cfg.MinBaselineSamples-1, orders), now.Add(-time.Minute))
	if c, _ := d.probeImpact(ctx, deps, cfg, now); len(c) != 0 {
		t.Fatalf("taban örneği azken aday olmamalı: %v", c)
	}
	// taban bayat (son tik çok eski) → yok sayılır, sorgu atılmaz
	d.cacheLagImpact(lagTopicQ, lagProbeBase(600, 30, orders), now.Add(-impactBaseMaxTicks*tick-time.Second))
	q := vm.ranges
	if c, _ := d.probeImpact(ctx, deps, cfg, now); len(c) != 0 || vm.ranges != q {
		t.Fatalf("bayat taban yok sayılmalı: %v sorgu=%d→%d", c, q, vm.ranges)
	}
	// sorgu hatası: hata döner, aday yok, satırlar saklanmaz
	d.cacheLagImpact(lagTopicQ, lagProbeBase(600, 30, orders), now.Add(-time.Minute))
	vm.fail = errors.New("connection refused")
	if c, err := d.probeImpact(ctx, deps, cfg, now.Add(time.Minute)); err == nil || len(c) != 0 {
		t.Fatalf("sorgu hatası dönmeli, aday olmamalı: %v %v", c, err)
	}
	vm.fail = nil
	q = vm.ranges
	if _, err := d.lagMinutes(ctx, deps, lagTopicQ, svcImpactEnd(now.Add(time.Minute), cfg.IngestLagSec), now.Add(time.Minute)); err != nil || vm.ranges != q+1 {
		t.Fatal("hatalı yoklamanın satırları saklanmamalı (tik yeniden sorar)")
	}
	// son tik lag'i etki kuralıyla değerlendirmediyse (ifade pencereli, aile Thanos'ta) durum silinir
	d.clearLagImpact()
	q = vm.ranges
	if c, _ := d.probeImpact(ctx, deps, cfg, now); len(c) != 0 || vm.ranges != q {
		t.Fatal("durum silinince yoklama sessiz ve sorgusuz olmalı")
	}
	// VM yok / aile kapalı
	if c, err := d.probeImpact(ctx, Deps{}, cfg, now); err != nil || len(c) != 0 {
		t.Fatalf("VM yokken sessiz: %v %v", c, err)
	}
	off := lagProbeCfg()
	off.Detectors[lagFam] = false
	if !d.probeOn(cfg) || d.probeOn(off) {
		t.Fatal("yoklama yalnız metric_kafka açıkken")
	}
}

// Erken tik yoklamanın satırlarını yeniden kullanır: aynı pencere bitişine
// ikinci sorgu gitmez; pencere bitişi ya da ifade değişince yeniden sorulur.
func TestLagEarlyTickReusesProbeRows(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	now := t0.Add(11*time.Minute + 30*time.Second)
	cfg := lagProbeCfg()
	ctx := context.Background()
	orders := [2]string{"orders", "orders.events"}
	vm := &lagVM{t0: t0, lag: map[[2]string]func(int) (float64, bool){orders: lagIncident(6, 40, 600, 165000)}}
	d := NewMetricML()
	deps := Deps{VM: vm, Backoff: newVMBackoff()}
	d.cacheLagImpact(lagTopicQ, lagProbeBase(600, 30, orders), now.Add(-time.Minute))
	if _, err := d.probeImpact(ctx, deps, cfg, now); err != nil || vm.ranges != 1 {
		t.Fatalf("yoklama tek aralık sorgusu atmalı: %v sorgu=%d", err, vm.ranges)
	}
	early := now.Add(5 * time.Second) // aynı dakika → aynı pencere bitişi
	end := svcImpactEnd(early, cfg.IngestLagSec)
	minute, err := d.lagMinutes(ctx, deps, lagTopicQ, end, early)
	if err != nil || vm.ranges != 1 {
		t.Fatalf("aynı pencere bitişinde yeniden sorgu olmamalı: %v sorgu=%d", err, vm.ranges)
	}
	k := chstore.MLSeriesKey("orders", kafkaTopicPart("orders.events"))
	if v := lagImpactDecide(lagImpactSamples(minute[k], end.Unix()), lagBaseOf(repeat(600, 30)), cfg.MinBaselineSamples, false); !v.Fire {
		t.Fatalf("yeniden kullanılan satırlar aynı kararı vermeli: %+v", v)
	}
	other := lagQuery{expr: "max by (service_name) (kafka_consumer_records_lag_max)", svcLabel: "service_name"}
	if _, err := d.lagMinutes(ctx, deps, other, end, early); err != nil || vm.ranges != 2 {
		t.Fatalf("ifade değiştiyse saklı satırlar kullanılmaz: %v sorgu=%d", err, vm.ranges)
	}
	next := now.Add(time.Minute)
	if _, err := d.lagMinutes(ctx, deps, lagTopicQ, svcImpactEnd(next, cfg.IngestLagSec), next); err != nil || vm.ranges != 3 {
		t.Fatalf("yeni pencere bitişinde bir sorgu daha: %v sorgu=%d", err, vm.ranges)
	}
}

// Döngü (sahte saat): zamanlı tik 300 sn, yoklama 60 sn. orders lag'i 10:06'da
// büyür → olay 10:15:30 yerine 10:11:30 erken tikinde. Bekleme: olayı kalite
// süzgecinde düşen aday (payments) 15 dk dolmadan yeniden erken tik açmaz.
func TestLagTickLoopEarlyTicks(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	stop := t0.Add(50 * time.Minute)
	cfg := lagProbeCfg()
	cfg.TickSec, cfg.IngestLagSec = 300, 120
	cfg.Quality.ImpactMinAbs = 3000 // payments (0 → 2.000) etki eşiğinin altında kalır
	ctx := context.Background()
	orders, payments, search := [2]string{"orders", "orders.events"}, [2]string{"payments", "payments.events"}, [2]string{"search", "search.events"}
	vm := &lagVM{t0: t0, lag: map[[2]string]func(int) (float64, bool){
		orders: lagIncident(6, 60, 600, 165000), payments: lagIncident(27, 60, 0, 2000), search: lagIncident(0, 0, 0, 0)}}
	base := lagProbeBase(600, 30, orders)
	for k, v := range lagProbeBase(0, 30, payments, search) {
		base[k] = v
	}
	d := NewMetricML()
	r := &Recorder{detectors: []Detector{d}, runs: newTracker(), backoff: newVMBackoff(), vm: vm}

	clk := t0.Add(30 * time.Second)
	var early, sched []time.Time
	probes := 0
	firstEvent := map[string]time.Time{}
	tickLoop{
		now:  func() time.Time { return clk },
		wait: func(dur time.Duration) bool { clk = clk.Add(dur); return clk.Before(stop) },
		cfg:  func() chstore.MLConfig { return cfg },
		probe: func(cfg chstore.MLConfig, now time.Time) bool {
			probes++
			return r.probeImpact(ctx, cfg, true, now)
		},
		// tam tikin lag kesiti: gerçek kabul kapısı, 1 dk satırları (yoklamadan
		// yeniden kullanım), karar, kalite süzgeci, kapanış.
		tick: func(cfg chstore.MLConfig, isEarly bool) {
			now := clk
			if !r.admit(cfg, true, true, nil, isEarly) {
				t.Fatalf("%s tik kabul edilmeliydi", now.Format("15:04:05"))
			}
			tick := time.Duration(cfg.TickSec) * time.Second
			deps := Deps{VM: vm, Backoff: r.backoff,
				openRun:  func(id string) bool { return r.runs.openRun(id, now, tick) },
				runStart: func(id string) int64 { return r.runs.startedOf(id, now, tick) }}
			end := svcImpactEnd(now, cfg.IngestLagSec)
			q := vm.ranges
			minute, err := d.lagMinutes(ctx, deps, lagTopicQ, end, now)
			if err != nil {
				t.Fatal(err)
			}
			if isEarly && vm.ranges != q {
				t.Fatalf("%s erken tik yoklamanın satırlarını yeniden kullanmalı", now.Format("15:04:05"))
			}
			d.cacheLagImpact(lagTopicQ, base, now)
			kept, _ := qualityFilter(d.lagImpactEvents(deps, minute, base, end, cfg, "weekday", now), cfg, r.runs, now)
			for _, e := range kept {
				if _, ok := firstEvent[e.Service]; !ok {
					firstEvent[e.Service] = now
				}
			}
			if isEarly {
				early = append(early, now)
				clk = clk.Add(20 * time.Second) // erken tik zaman alır; takvim kaymamalı
			} else {
				sched = append(sched, now)
				r.runs.closeStale(now, tickCoverage{})
			}
		},
	}.run()

	at := func(min, sec int) time.Time {
		return t0.Add(time.Duration(min)*time.Minute + time.Duration(sec)*time.Second)
	}
	for i, s := range sched {
		if want := at(5*i, 30); !s.Equal(want) {
			t.Fatalf("zamanlı tik %d kaydı: %s", i, s.Format("15:04:05"))
		}
	}
	// orders: 3. ihlal dakikası 10:08, ingest payı 120 sn → 10:11'den sonraki ilk
	// yoklama. payments: 10:32:30 ve (olayı düştüğü için) bekleme dolunca 10:47:30.
	wantEarly := []time.Time{at(11, 30), at(32, 30), at(47, 30)}
	if len(early) != len(wantEarly) {
		t.Fatalf("erken tikler: %v (want %v)", early, wantEarly)
	}
	for i := range early {
		if !early[i].Equal(wantEarly[i]) {
			t.Fatalf("erken tik %d: %s (want %s)", i, early[i].Format("15:04:05"), wantEarly[i].Format("15:04:05"))
		}
	}
	if !firstEvent["orders"].Equal(at(11, 30)) {
		t.Fatalf("orders olayı erken tikte yazılmalıydı (yoklama olmasa 10:15:30): %v", firstEvent)
	}
	if len(firstEvent) != 1 {
		t.Fatalf("payments kalite süzgecinde düşmeli, sağlıklı topic olay üretmemeli: %v", firstEvent)
	}
	if r.leaderTicks != len(sched) {
		t.Fatalf("lider tik sayacı %d, zamanlı tik %d", r.leaderTicks, len(sched))
	}
	// VM maliyeti: yoklama başına 1, zamanlı tik başına 1, erken tik 0 aralık sorgusu
	if want := probes + len(sched); vm.ranges != want {
		t.Fatalf("aralık sorgusu %d, beklenen %d (yoklama %d, zamanlı tik %d)", vm.ranges, want, probes, len(sched))
	}
}

// ── Problem kapısı (tek sinyal kolu) ─────────────────────────────────────

// lagGateEv — başlangıcı `now − ago` olan açık consumer lag olayı.
func lagGateEv(id string, now time.Time, ago time.Duration, base, obs float64, evidence string) chstore.MLEvent {
	return chstore.MLEvent{ID: id, Detector: lagFam, Signal: chstore.MLSignalMetric, Service: "orders", Code: kafkaTopicPart("orders.events"),
		StartedAt: now.Add(-ago).UnixNano(), LastSeen: now.UnixNano(), WindowSec: 300, Baseline: base, Observed: obs, Score: 9, Direction: "up", Evidence: evidence}
}

func lagImpactEvidence(fired bool, thr float64, viol int) string {
	return mergeEvidence("", map[string]any{"source": "vm", "rule": "impact", "fired": fired, "threshold": thr, "violations": viol, "window": impactWindow})
}

// Etki kuralıyla açılmış lag olayı 600 sn sürdürme ve "≥ 2 × taban" tekrarı
// OLMADAN Problem açar (5'te 3 penceresi sürdürmedir); damgasız olay 600 sn
// kuralında kalır. v0.10.2473: gürültü kapısı kipi on iken (pub dolu) etki
// kuralı olayı MUAF — kanıt sayısına (K10) bakılmaz, karar beklenmez.
func TestSingleArmOpensOnImpactRuleLag(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	pubOK := map[string]PubVerdict{"a": {State: PublishPublished}}
	pubHeld := map[string]PubVerdict{"a": {State: PublishHeld}}
	thanos := lagGateEv("a", now, 4*time.Minute, 600, 165000, lagImpactEvidence(true, 1200, 4))
	thanos.Code = lagSvcPart + "@prod-east"
	thanos.Evidence = mergeEvidence("", map[string]any{"source": "thanos", "dwell": 2})
	for _, tc := range []struct {
		name   string
		ev     chstore.MLEvent
		pub    map[string]PubVerdict
		want   bool
		prio   string   // v0.10.2396: etki kuralı lag ≥ 2 × taban → P1
		why    []string // gerekçede geçmeli
		whyNot string   // gerekçede geçmemeli
	}{
		{"etki kuralı, 4 dk önce başladı → açar", lagGateEv("a", now, 4*time.Minute, 600, 165000, lagImpactEvidence(true, 1200, 4)), nil, true, "P1",
			[]string{"etki kuralı", "son 5 dakikada 4 ihlal", "eşik 1200", "şimdi 165000", "normalde 600"}, "sürdü"},
		{"etki kuralı, taban 0 → açar", lagGateEv("a", now, 3*time.Minute, 0, 5000, lagImpactEvidence(true, 1000, 3)), nil, true, "P1",
			[]string{"etki kuralı", "eşik 1000", "normalde hiç yok"}, "sürdü"},
		{"etki kuralı, uzatma tiki (en yeni dakika eşiğin altında) → karar değişmez", lagGateEv("a", now, 9*time.Minute, 600, 900, lagImpactEvidence(false, 1200, 2)), nil, true, "P2",
			[]string{"etki kuralıyla açık", "pencerede 2 ihlal"}, "sürdü"},
		{"damgasız lag olayı, 4 dk → açmaz (600 sn kuralı)", lagGateEv("a", now, 4*time.Minute, 600, 165000, `{"source":"vm","dwell":2}`), nil, false, "", nil, ""},
		{"damgasız lag olayı, 10 dk → açar (eski kol)", lagGateEv("a", now, 10*time.Minute, 600, 165000, `{"source":"vm","dwell":2}`), nil, true, "P2",
			[]string{"10 dk sürdü", "normalin 275.0 katı"}, "etki kuralı"},
		{"Thanos lag olayı, 4 dk → açmaz (kapsam dışı, 600 sn kuralı)", thanos, nil, false, "", nil, ""},
		{"kapılar on, yayımlandı → açar", lagGateEv("a", now, 4*time.Minute, 600, 165000, lagImpactEvidence(true, 1200, 4)), pubOK, true, "P1",
			[]string{"etki kuralı"}, "sürdü"},
		{"kapılar on, karar yok (ilk tik) → etki kuralı muaf, açar", lagGateEv("a", now, 4*time.Minute, 600, 165000, lagImpactEvidence(true, 1200, 4)), map[string]PubVerdict{}, true, "P1",
			[]string{"etki kuralı"}, "sürdü"},
		{"kapılar on, held yazılmış olsa da etki kuralı muaf → açar", lagGateEv("a", now, 4*time.Minute, 600, 165000, lagImpactEvidence(true, 1200, 4)), pubHeld, true, "P1",
			[]string{"etki kuralı"}, "sürdü"},
		{"kapılar on, damgasız lag held → açmaz", lagGateEv("a", now, 10*time.Minute, 600, 165000, `{"source":"vm","dwell":2}`), pubHeld, false, "", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gs := GroupMLEvents([]chstore.MLEvent{tc.ev}, 10*time.Minute)
			if len(gs) != 1 {
				t.Fatalf("tek grup bekleniyordu: %d", len(gs))
			}
			d := DecideGroupPromotion(gs[0], []chstore.MLEvent{tc.ev}, GroupGateInput{Published: tc.pub}, now)
			if d.WouldOpen != tc.want || d.SingleWouldOpen != tc.want {
				t.Fatalf("WouldOpen=%v SingleWouldOpen=%v, istenen %v (%s)", d.WouldOpen, d.SingleWouldOpen, tc.want, d.Reason)
			}
			if !tc.want {
				if d.Severity != "" || d.Priority != "" {
					t.Fatalf("açmayan karar şiddet/öncelik taşımamalı: %+v", d)
				}
				return
			}
			if d.Severity != "critical" || d.Priority != tc.prio || d.SingleEvent != "a" || !strings.HasPrefix(d.Reason, "açardı: tek güçlü sinyal (metric)") {
				t.Fatalf("karar: %+v", d)
			}
			for _, w := range tc.why {
				if !strings.Contains(d.Reason, w) {
					t.Fatalf("gerekçede %q yok: %s", w, d.Reason)
				}
			}
			if strings.Contains(d.Reason, tc.whyNot) {
				t.Fatalf("gerekçe %q dememeli: %s", tc.whyNot, d.Reason)
			}
		})
	}
	// kapanmış olay açmaz; iyileşme yönü açmaz (etki damgası kolun diğer şartlarını atlatmaz)
	closedEv := lagGateEv("a", now, 4*time.Minute, 600, 165000, lagImpactEvidence(true, 1200, 4))
	closedEv.Stage = chstore.MLStageClosed
	gs := GroupMLEvents([]chstore.MLEvent{closedEv}, 10*time.Minute)
	if d := DecideGroupPromotion(gs[0], []chstore.MLEvent{closedEv}, GroupGateInput{}, now); d.WouldOpen {
		t.Fatalf("kapanmış olay Problem açmamalı: %+v", d)
	}
}

// Kalite süzgeci kuralla aynı yönde: yeni olay lag tabanının altındaysa yine
// yazılmaz; etki kuralıyla açık koşunun uzatma tiki (değer tabana dönmüş) ne
// etki eşiğine ne lag tabanına takılır. Damgasız (eski yol) olayda etki eşiği aynen.
func TestQualityKeepsImpactLagExtension(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	t0 := time.Unix(1_800_000_000, 0)
	part := kafkaTopicPart("orders.events")
	ev := func(obs float64, evidence string) chstore.MLEvent {
		return chstore.MLEvent{ID: chstore.MLEventID(lagFam, "orders", part), Detector: lagFam, Signal: chstore.MLSignalMetric, Service: "orders",
			Code: part, Baseline: 600, Observed: obs, Score: 9, Direction: "up", Evidence: evidence}
	}
	tr := newTracker()
	if kept, _ := qualityFilter([]chstore.MLEvent{ev(165000, lagImpactEvidence(true, 1200, 3))}, cfg, tr, t0); len(kept) != 1 {
		t.Fatal("açılış yazılmalı")
	}
	if kept, sup := qualityFilter([]chstore.MLEvent{ev(600, lagImpactEvidence(false, 1200, 1))}, cfg, tr, t0.Add(5*time.Minute)); len(kept) != 1 {
		t.Fatalf("etki kuralıyla açık koşunun uzatması yazılmalı: %+v", sup)
	}
	tr2 := newTracker()
	if kept, _ := qualityFilter([]chstore.MLEvent{ev(165000, `{"source":"thanos"}`)}, cfg, tr2, t0); len(kept) != 1 {
		t.Fatal("açılış yazılmalı")
	}
	if kept, sup := qualityFilter([]chstore.MLEvent{ev(600, `{"source":"thanos"}`)}, cfg, tr2, t0.Add(5*time.Minute)); len(kept) != 0 || sup.Impact != 1 {
		t.Fatalf("damgasız olayda etki eşiği aynen uygulanmalı: %d %+v", len(kept), sup)
	}
	if kept, sup := qualityFilter([]chstore.MLEvent{ev(600, lagImpactEvidence(false, 1200, 1))}, cfg, newTracker(), t0); len(kept) != 0 || sup.Impact != 1 {
		t.Fatalf("açık koşusu olmayan uzatma adayı yazılmamalı: %d %+v", len(kept), sup)
	}
}

// Kart: teknik satır etki kuralını söyler (kova sürekliliği değil); değer
// parçası 1 dakikalık ölçüm der (5 dakikalık değil).
func TestLagImpactCardLines(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 20, 12, 5, 0, 0, time.UTC)
	part := kafkaTopicPart("orders.events")
	samples := []impactSample{lagS(600), lagS(600), lagS(40000), lagS(90000), lagS(165000)}
	ev := lagImpactEvent("orders", part, samples, lagImpactDecide(samples, lagBaseOf(repeat(600, 30)), 20, false), end, 0, cfg, "weekday")
	in := SummaryInput(ev, cfg, end.Add(3*time.Minute), time.UTC)
	if in.ImpactRule == nil || in.DwellBuckets != 0 {
		t.Fatalf("girdi: %+v dwell=%d", in.ImpactRule, in.DwellBuckets)
	}
	in.DwellBuckets, in.ImpactMinRel = 2, 0.2 // olay anı kaydının eşikleri (ApplySnapshot)
	c := BuildExplainCard(in)
	if got, want := c.Technical.Thresholds, "Etki kuralı: son 5 dakikalık örnekte en az 3 ihlal · eşik 1.200"; got != want {
		t.Fatalf("teknik satır:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(c.Technical.Measured, "1 dakikalık ölçüm") || strings.Contains(c.Summary+c.Technical.Measured, "5 dakikalık") {
		t.Fatalf("değer 1 dakikalık ölçümdür: %q / %q", c.Technical.Measured, c.Summary)
	}
}
