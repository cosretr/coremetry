package ml

// explain_course_test.go — öğrenme döngüsü Faz 2 parti B (audit §7.2, §17):
// "Olayın seyri" ek katmanlarının SAF kurulumu.
//
// v0.10.1870 — trace ısı haritası: pencere kayıtla aynı (başlangıç − 1 sa …
// bitiş + 1 sa, şimdiyi ve 6 sa'yi aşmaz), süzgeç yalnız http.route
// değerinde, çizgi = olay öncesi p95 medyanı (seri yoksa çizgi YOK).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1871 — log yığını: taban olayın kendi serisi, ilk 2 aile + diğer,
// yığın tabanı aşmaz, gölge = bant üstü, seviye = hata + diğer seviyeler.
func logCourseFixture() (CardInput, map[string][]chstore.MLPoint) {
	pts := func(vs ...float64) []chstore.MLPoint {
		var out []chstore.MLPoint
		for i, v := range vs {
			out = append(out, chstore.MLPoint{T: at(14, 0).Add(time.Duration(i) * 5 * time.Minute).Unix(), V: v})
		}
		return out
	}
	in := CardInput{Detector: "error_rate", Signal: chstore.MLSignalLog, Service: "orders-api", Part: partError, Direction: "up",
		StartedAt: at(14, 15), LastSeen: at(14, 25), Now: at(14, 30), Loc: trLoc, BucketSec: 300,
		Series: pts(5, 5, 5, 60, 62, 58), Band: bands(at(14, 0), 3, 4, 5, 7),
		NewTemplate: &CardTemplate{Text: "Connection pool exhausted"}}
	series := map[string][]chstore.MLPoint{
		tplPartPrefix + "Connection pool exhausted":         pts(0, 0, 0, 40, 45, 40),
		tplPartPrefix + "Timeout calling payments-mobile":   pts(0, 1, 0, 20, 25, 30),
		tplPartPrefix + "Order failed upstream unavailable": pts(1, 0, 1, 2, 2, 2),
		partError: pts(5, 5, 5, 60, 62, 58),
		partTotal: pts(50, 50, 50, 110, 112, 108),
	}
	return in, series
}

func TestBuildLogCourse(t *testing.T) {
	in, series := logCourseFixture()
	if !CourseSeriesNeeded(in) {
		t.Fatal("log tabanı ml_series okuması ister")
	}
	lc := BuildLogCourse(in, series)
	if lc == nil {
		t.Fatal("log katmanı yok")
	}
	var labels []string
	for _, s := range lc.ByPattern {
		labels = append(labels, s.Label)
	}
	eqList(t, "kalıplar", labels, []string{"Connection pool exhausted", "Timeout calling payments-mobile", "diğer hata mesajları"})
	if !lc.ByPattern[0].New || lc.ByPattern[1].New {
		t.Error("yalnız ilk kez görülen mesaj yeni")
	}
	wantVals := [][]float64{{0, 0, 0, 40, 45, 40}, {0, 1, 0, 20, 17, 18}, {5, 4, 5, 0, 0, 0}}
	for k, w := range wantVals {
		for i, v := range w {
			if lc.ByPattern[k].Values[i] != v {
				t.Fatalf("yığın %d kova %d: %v ≠ %v", k, i, lc.ByPattern[k].Values[i], v)
			}
		}
	}
	for i, b := range in.Series { // çubuk boyu = dedektörün değeri
		sum := 0.0
		for _, s := range lc.ByPattern {
			sum += s.Values[i]
		}
		if sum != b.V {
			t.Errorf("kova %d toplam %v ≠ taban %v", i, sum, b.V)
		}
	}
	if len(lc.Shadow) != 6 || lc.Shadow[0] == nil || *lc.Shadow[0] != 7 || lc.Shadow[3] != nil {
		t.Errorf("gölge bant üstü olmalı, bantsız kova null: %v", lc.Shadow)
	}
	if len(lc.ByLevel) != 2 || lc.ByLevel[1].Label != "diğer seviyeler" || lc.ByLevel[1].Values[3] != 50 || lc.ByLevel[0].Values[4] != 62 {
		t.Errorf("seviye: %+v", lc.ByLevel)
	}

	none := BuildLogCourse(in, nil) // ml_series okunamadı → tek katman, seviye yok
	if len(none.ByPattern) != 1 || none.ByPattern[0].Label != "hata satırları" || none.ByLevel != nil {
		t.Errorf("serisiz: %+v", none)
	}
	tpl := in
	tpl.Detector, tpl.Part = "rare_template", tplPartPrefix+"Connection pool exhausted"
	if x := BuildLogCourse(tpl, series); len(x.ByPattern) != 1 || !x.ByPattern[0].New || x.ByLevel != nil || CourseSeriesNeeded(tpl) {
		t.Errorf("şablon dedektörü tek katman, okuma yok: %+v", x)
	}
	tr := playFixture()
	if BuildLogCourse(tr, series) != nil {
		t.Error("trace olayında log katmanı yok")
	}
	nb := in
	nb.Band = nil
	if BuildLogCourse(nb, series).Shadow != nil {
		t.Error("bant yoksa gölge yok")
	}
}

// v0.10.1872 — az çağrılan servis: ısı haritasının YERİNE noktalar; çizgi
// kanıttaki alışılmış üst sınır (T90) önce, yoksa olay öncesi p95; okuma
// hatası "yok" sayılmaz.
func TestBuildTraceCourseDots(t *testing.T) {
	in := playFixture() // 14:30 başladı, açık, şimdi 14:44
	in.Profile = ProfileLowTraffic
	if !CourseCallsNeeded(in) || CourseCallRoute(in) != "/notify/order-status" {
		t.Fatal("az çağrılan endpoint'li trace olayı çağrı okuması ister")
	}
	ms := func(h, m int) int64 { return at(h, m).UnixMilli() }
	calls := CourseCalls{OK: true, Points: []chstore.MLCallPoint{
		{T: ms(14, 20), Ms: 3000}, // olaydan önce: sayılmaz
		{T: ms(14, 35), Ms: 1500}, {T: ms(14, 36), Ms: 700}, {T: ms(14, 40), Ms: 2000, Err: true},
	}}
	in.LowTraffic = &CardLowTraffic{N: 10, K: 6, LimitMs: 1200}
	tc := BuildTraceCourse(in, nil, calls)
	eq(t, "view", tc.View, "dots")
	eq(t, "çizgi", tc.Line.Label, "alışılmış üst sınır ≈ 1,2 sn (normalde 10 çağrıdan 1'i aşar)")
	eq(t, "özet", tc.DotsSummary, "Olay penceresindeki 3 çağrının 2'si alışılmış üst sınırın üstünde.")
	if len(tc.Dots) != 4 || tc.DotsCapped || tc.DotsUnread {
		t.Errorf("noktalar: %+v", tc)
	}

	p95 := playFixture()
	p95.Profile = ProfileLowTraffic
	series := map[string][]chstore.MLPoint{tracePart(p95.Code, "p95"): {{T: at(14, 0).Unix(), V: 620}}}
	x := BuildTraceCourse(p95, series, calls)
	eq(t, "p95 çizgisi", x.Line.Label, "olay öncesi p95 ≈ 620 ms")
	eq(t, "p95 özeti", x.DotsSummary, "Olay penceresindeki 3 çağrının 3'ü çizginin üstünde.")

	if u := BuildTraceCourse(p95, series, CourseCalls{}); u.View != "dots" || !u.DotsUnread || u.Dots != nil || u.DotsSummary != "" {
		t.Errorf("okunamadı ≠ yok: %+v", u)
	}
	busy := playFixture()
	busy.Profile = ProfileAlwaysOn
	if CourseCallsNeeded(busy) || BuildTraceCourse(busy, series, calls).View != "heatmap" {
		t.Error("yoğun serviste ısı haritası kalır, çağrı okuması yok")
	}
}

func TestCourseReadWindow(t *testing.T) {
	in, _ := logCourseFixture()
	from, to := CourseReadWindow(in)
	eq(t, "from", from.In(trLoc).Format("15:04"), "14:00")
	eq(t, "to", to.In(trLoc).Format("15:04"), "14:30")
}

func TestCourseWindow(t *testing.T) {
	in := playFixture() // açık olay, 14:30 başladı, şimdi 14:44
	from, to := CourseWindow(in)
	eq(t, "from", from.In(trLoc).Format("15:04"), "13:30")
	eq(t, "to (şimdi)", to.In(trLoc).Format("15:04"), "14:44")

	closed := playFixture()
	closed.Now = at(23, 0)
	closed.LastSeen = at(15, 0)
	_, to = CourseWindow(closed)
	eq(t, "to (bitiş + 1 sa)", to.In(trLoc).Format("15:04"), "16:00")

	long := playFixture()
	long.StartedAt, long.LastSeen, long.Now = at(2, 0), at(12, 0), at(23, 0)
	from, to = CourseWindow(long)
	if to.Sub(from) != 6*time.Hour {
		t.Errorf("tavan 6 sa olmalı: %s", to.Sub(from))
	}
	eq(t, "uzun olay baştan", from.In(trLoc).Format("15:04"), "01:00")
}

func TestBuildTraceCourse(t *testing.T) {
	in := playFixture()
	var p95 []chstore.MLPoint
	for i, v := range []float64{600, 640, 0, 620, 2300} { // 0 = boş kova, sayılmaz; 14:20 sonrası olay
		p95 = append(p95, chstore.MLPoint{T: at(14, 10).Add(time.Duration(i) * 5 * time.Minute).Unix(), V: v})
	}
	series := map[string][]chstore.MLPoint{tracePart(in.Code, "p95"): p95}
	tc := BuildTraceCourse(in, series, CourseCalls{})
	if tc == nil {
		t.Fatal("trace katmanı yok")
	}
	eq(t, "view", tc.View, "heatmap")
	eq(t, "route süzgeci", tc.Route, "/notify/order-status")
	if tc.Line == nil || tc.Line.Ms != 620 {
		t.Fatalf("çizgi olay öncesi medyan olmalı: %+v", tc.Line)
	}
	eq(t, "etiket", tc.Line.Label, "olay öncesi p95 ≈ 620 ms")
	if tc.OnsetNs != in.StartedAt.UnixNano() || tc.EndNs != in.Now.UnixNano() {
		t.Errorf("pencere uçları: %d %d", tc.OnsetNs, tc.EndNs)
	}

	slow := map[string][]chstore.MLPoint{tracePart(in.Code, "p95"): {{T: at(14, 0).Unix(), V: 1100}}}
	eq(t, "saniye ölçeği", BuildTraceCourse(in, slow, CourseCalls{}).Line.Label, "olay öncesi p95 ≈ 1,1 sn")

	if tc := BuildTraceCourse(in, nil, CourseCalls{}); tc == nil || tc.Line != nil {
		t.Errorf("seri yoksa katman var, çizgi yok: %+v", tc)
	}

	rpc := playFixture()
	rpc.Code = "orders.v1.Orders/Create"
	if tc := BuildTraceCourse(rpc, nil, CourseCalls{}); tc == nil || tc.Route != "" {
		t.Errorf("rpc etiketi http.route süzgecine girmez: %+v", tc)
	}

	for _, det := range []string{"dependency_latency", "trace_pod_outlier"} {
		x := playFixture()
		x.Detector = det
		if BuildTraceCourse(x, series, CourseCalls{}) != nil || CourseSeriesNeeded(x) {
			t.Errorf("%s: endpoint'siz dedektörde ısı haritası yok", det)
		}
	}
	lg := playFixture()
	lg.Signal, lg.Detector = chstore.MLSignalLog, "error_rate"
	if BuildTraceCourse(lg, series, CourseCalls{}) != nil {
		t.Error("log olayında trace katmanı yok")
	}
}
