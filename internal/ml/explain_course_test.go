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
	tc := BuildTraceCourse(in, series)
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
	eq(t, "saniye ölçeği", BuildTraceCourse(in, slow).Line.Label, "olay öncesi p95 ≈ 1,1 sn")

	if tc := BuildTraceCourse(in, nil); tc == nil || tc.Line != nil {
		t.Errorf("seri yoksa katman var, çizgi yok: %+v", tc)
	}

	rpc := playFixture()
	rpc.Code = "orders.v1.Orders/Create"
	if tc := BuildTraceCourse(rpc, nil); tc == nil || tc.Route != "" {
		t.Errorf("rpc etiketi http.route süzgecine girmez: %+v", tc)
	}

	for _, det := range []string{"dependency_latency", "trace_pod_outlier"} {
		x := playFixture()
		x.Detector = det
		if BuildTraceCourse(x, series) != nil || CourseSeriesNeeded(x) {
			t.Errorf("%s: endpoint'siz dedektörde ısı haritası yok", det)
		}
	}
	lg := playFixture()
	lg.Signal, lg.Detector = chstore.MLSignalLog, "error_rate"
	if BuildTraceCourse(lg, series) != nil {
		t.Error("log olayında trace katmanı yok")
	}
}
