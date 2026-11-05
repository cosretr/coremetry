package ml

// v0.10.1812 — ML tik ölçümü (audit §10 dilim 1.1, §13 D3): sinyal türü
// başına servis/seri/sorgu sayaçları. Önceki durum: tik durumunda yalnız
// aday servisleri vardı; "kaç servis görüldü, ES/VM/CH'ye kaç sorgu" ve
// ES terms tavanı dışında kalan servisler hiç ölçülmüyordu.

import (
	"encoding/json"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestFoldKindTicks(t *testing.T) {
	m1 := newTickMeter()
	m1.observeRows([]chstore.MLSeriesRow{{Service: "orders-api", Part: "_total"}, {Service: "orders-api", Part: "_total"}, {Service: "branch-teller", Part: "_error"}})
	m1.observeDropped(17)
	m2 := newTickMeter()
	m2.observeRows([]chstore.MLSeriesRow{{Service: "orders-api", Part: "_total"}})
	m2.observeDropped(12) // aynı servis kümesinin ikinci isteği: toplam değil en büyük
	samples := []kindSample{
		sampleOf(chstore.MLSignalLog, m1, nil, 1, 0, 3, 2000),
		sampleOf(chstore.MLSignalLog, m2, []chstore.MLEvent{{Service: "payments-mobile"}}, 1, 0, 2, 500),
		sampleOf(chstore.MLSignalTrace, nil, []chstore.MLEvent{{Service: "orders-api"}}, 0, 4, 1, 300),
		sampleOf("", nil, nil, 9, 9, 9, 9), // türsüz örnek yok sayılır
	}
	got := foldKindTicks(samples)
	want := map[string]chstore.MLKindTick{
		chstore.MLSignalLog:   {Services: 3, Series: 2, SeriesRows: 4, ES: 2, VM: 0, CH: 5, Dropped: 17, DurationMs: 2500},
		chstore.MLSignalTrace: {Services: 1, Series: 0, SeriesRows: 0, ES: 0, VM: 4, CH: 1, Dropped: 0, DurationMs: 300},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("%s: got %+v want %+v", k, got[k], w)
		}
	}
}

func TestKindTickLines(t *testing.T) {
	lines := kindTickLines(map[string]chstore.MLKindTick{
		chstore.MLSignalMetric: {Services: 1},
		chstore.MLSignalLog:    {Services: 420, Series: 840, SeriesRows: 1680, ES: 2, CH: 6, Dropped: 17, DurationMs: 2100},
		chstore.MLSignalTrace:  {VM: 12},
	})
	if len(lines) != 3 {
		t.Fatalf("%v", lines)
	}
	if lines[0] != "[ml] tick kind=log services=420 series=840 rows=1680 es=2 vm=0 ch=6 dropped=17 dur=2100ms" {
		t.Fatalf("log satırı: %q", lines[0])
	}
	if lines[1][:20] != "[ml] tick kind=trace" || lines[2][:21] != "[ml] tick kind=metric" {
		t.Fatalf("sıra log, trace, metric: %v", lines)
	}
}

func TestMeterNilSafe(t *testing.T) {
	var m *tickMeter
	m.observeRows([]chstore.MLSeriesRow{{Service: "x"}})
	m.observeDropped(3)
	Deps{}.observeDropped(1)
}

// Durum blobu (ml_tick_status) api pod'una JSON ile gider: alanlar kaybolmasın.
func TestTickStatusKindsJSON(t *testing.T) {
	st := chstore.MLTickStatus{At: 1, Kinds: map[string]chstore.MLKindTick{"log": {Services: 5, Dropped: 2, ES: 1, CH: 3}}, ES: 4, VM: 5, CH: 6}
	raw, _ := json.Marshal(st)
	var back chstore.MLTickStatus
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Kinds["log"] != st.Kinds["log"] || back.ES != 4 || back.VM != 5 || back.CH != 6 {
		t.Fatalf("%s", raw)
	}
}
