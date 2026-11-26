package ml

// v0.10.2480 — operatör: anomali detay sayfası taze bir uç nokta (op_latency,
// p95) olayının canlı grafiğini çizemedi ve olay anı kaydı da yoktu.
//
// (a) Grafik ifadesi dedektörün aynı olay için koştuğu kayıt ifadesiyle
// (latSeriesQuery / errSeriesQuery / tpSeriesQuery) BİREBİR aynı olmalı; aile
// ve etiketler olay kanıtından (srcOf/errOf/cntOf anahtarları), keşif yok.
// (b) Kronik kayıt koşu izlemesini atlar; replay ön kaydı yine yazılmalı.
// Fikstürler nötr (orders / checkout).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestChartQueryEqualsDetectorRecordQuery(t *testing.T) {
	routes := []string{
		"/orders/{id}",
		"GET /api/v1/Orders/{id}",
		`checkout "express" step`,
		`Payments\Refund`,
		"OrderService/PlaceOrder",
		"Cart Ünite",
	}
	const win, slow = 300, 0.5
	for _, pin := range anomaly.REDFamilyPins() {
		// Olay kanıtı, dedektörün tik anında yazdığı biçimde.
		ev := mergeEvidence("", map[string]any{"redFamily": pin.ID, "svcLabel": "service_name", "routeLabel": "http_route", "statusLabel": "status_code"})
		m := evidenceMap(ev)
		p, l, ok := TraceREDEventSource(evStr(m, "redFamily"), evStr(m, "svcLabel"), evStr(m, "routeLabel"), evStr(m, "statusLabel"))
		if !ok || p.ID != pin.ID {
			t.Fatalf("%s: kanıttan aile çözülmedi", pin.ID)
		}
		for _, route := range routes {
			type c struct {
				det, measure, want string
			}
			lp, _ := latSeriesQuery(pin, l.Svc, l.Route, "orders", route, "p95", win, slow)
			ls, _ := latSeriesQuery(pin, l.Svc, l.Route, "orders", route, "slow_ratio", win, slow)
			le, _ := errSeriesQuery(pin, l.Svc, l.Route, l.Status, "orders", route, win)
			lt, _ := tpSeriesQuery(pin, l.Svc, l.Route, "orders", route, win)
			for _, x := range []c{{"op_latency", "p95", lp.Query}, {"op_latency", "slow_ratio", ls.Query}, {"op_error_rate", "err_ratio", le.Query}, {"throughput_drop", "rate", lt.Query}} {
				sq, ok := TraceREDSourceQuery(p, l, "orders", route, x.measure, win, slow)
				if !ok || x.want == "" {
					t.Fatalf("%s %s %q: kurulamadı", pin.ID, x.det, route)
				}
				if sq.Expr != x.want {
					t.Errorf("%s %s %q:\n grafik   %s\n dedektör %s", pin.ID, x.det, route, sq.Expr, x.want)
				}
				// route değeri tek eşleyicide, kaçışlı (tırnak / ters bölü kırmaz).
				if !strings.Contains(sq.Expr, mEq("http_route", route)) {
					t.Errorf("%s %q: route eşleyicisi yok: %s", pin.ID, route, sq.Expr)
				}
				// Dedektörün tik sorgusuyla aynı seçici (aynı metrik yazım adayları).
				part := partCount
				if x.measure == "p95" || x.measure == "slow_ratio" {
					part = partBucket
				}
				sel := redSel(pin, part)
				if i := strings.Index(sel, "}"); i > 0 {
					sel = sel[:i] // eşleyiciler bundan sonra eklenir
				}
				if !strings.Contains(sq.Expr, sel) {
					t.Errorf("%s %s: dedektör seçicisi %q yok: %s", pin.ID, x.measure, sel, sq.Expr)
				}
				if !strings.Contains(sq.Expr, "[300s]") {
					t.Errorf("%s %s: dedektörün kova penceresi yok: %s", pin.ID, x.measure, sq.Expr)
				}
			}
		}
	}
}

// Prometheus yazımlı VM: HTTP ailesinin seçicisi noktalı mantıksal adı ÇIPLAK
// sormaz; _seconds_bucket yazımını kapsar (v0.10.2390 dersi grafikte de).
func TestChartQueryCoversPromNamedHistogram(t *testing.T) {
	for _, pin := range anomaly.REDFamilyPins() {
		if !strings.Contains(pin.Names, ".") {
			continue
		}
		l := TraceREDLabels{Svc: "service_name", Route: "http_route"}
		sq, ok := TraceREDSourceQuery(pin, l, "orders", "/orders/{id}", "p95", 300, 0.5)
		if !ok {
			t.Fatalf("%s: p95 kurulamadı", pin.ID)
		}
		prom := strings.ReplaceAll(pin.Names, ".", "_") + "_seconds_bucket"
		if !strings.Contains(sq.Expr, prom) {
			t.Errorf("%s: Prometheus yazımı %q aranmıyor: %s", pin.ID, prom, sq.Expr)
		}
		if strings.Contains(sq.Expr, pin.Names+"_bucket{") {
			t.Errorf("%s: noktalı ad çıplak soruluyor: %s", pin.ID, sq.Expr)
		}
	}
}

func TestTraceREDEventSourceRejectsUnknown(t *testing.T) {
	if _, _, ok := TraceREDEventSource("", "service_name", "http_route", ""); ok {
		t.Fatal("aile boşken ok olmamalı (eski olay → keşif)")
	}
	if _, _, ok := TraceREDEventSource("no-such-family", "service_name", "http_route", ""); ok {
		t.Fatal("bilinmeyen aile ok olmamalı")
	}
	pin := anomaly.REDFamilyPins()[0]
	if _, _, ok := TraceREDEventSource(pin.ID, "", "http_route", ""); ok {
		t.Fatal("servis etiketi boşken ok olmamalı")
	}
}

// (b) Kronik kayıt: qualityFilter koşu izlemez → tracker'da durum yok (kök
// neden); snapStateFor yine durum verir, aynı epizotta bir kez yazılır, yeni
// epizotta yeniden.
func TestChronicEventGetsSnapshotState(t *testing.T) {
	r := &Recorder{runs: newTracker()}
	cfg := chstore.MLConfig{TickSec: 120}
	tick := 2 * time.Minute
	t0 := time.Unix(1_800_000_000, 0)
	ev := chstore.MLEvent{ID: chstore.MLEventID("chronic:"+detOpLatency, "orders", "/orders/{id}"), Detector: detOpLatency,
		Signal: chstore.MLSignalTrace, Stage: chstore.MLStageChronic, Service: "orders", Code: "/orders/{id}"}
	kept, _ := qualityFilter([]chstore.MLEvent{ev}, cfg, r.runs, t0)
	if len(kept) != 1 || kept[0].ID != ev.ID {
		t.Fatalf("kronik olay süzgeçten aynen geçmeli: %+v", kept)
	}
	if r.runs.snap(ev.ID) != nil {
		t.Fatal("ön koşul: kronik kaydın tracker koşusu yok")
	}
	st := r.snapStateFor(kept[0], t0, tick)
	if st == nil || st.pre {
		t.Fatal("kronik olay replay ön kaydı almalı")
	}
	st.pre = true // writeSnapshots yazdı
	if again := r.snapStateFor(kept[0], t0.Add(tick), tick); again == nil || !again.pre {
		t.Fatal("aynı epizotta tik başına yeniden yazılmamalı")
	}
	gap := time.Duration(episodeCloseMisses)*tick + episodeReopenWindow + tick
	if next := r.snapStateFor(kept[0], t0.Add(tick+gap), tick); next == nil || next.pre {
		t.Fatal("yeni epizot yeni ön kayıt almalı")
	}
	// Koşusu izlenen (kronik olmayan) ve koşusu olmayan olay: nil (eski davranış).
	other := ev
	other.ID, other.Stage = "x", chstore.MLStageShadow
	if r.snapStateFor(other, t0, tick) != nil {
		t.Fatal("koşusu olmayan kronik-dışı olaya durum uydurulmamalı")
	}
	// Bellek sınırı: 24 sa görülmeyen girdi silinir.
	r.snapStateFor(other, t0, tick)
	_ = r.snapStateFor(kept[0], t0.Add(48*time.Hour), tick)
	if len(r.chronic) != 1 {
		t.Fatalf("eski girdiler silinmeli: %d", len(r.chronic))
	}
}
