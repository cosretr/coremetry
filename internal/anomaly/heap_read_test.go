package anomaly

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.890 — okuyucu yardımcıları sahibi paketinde pinli (887'de api'deydi):
// pod anahtarı sırası, pct yalnız limit>0 & postgc>0, 5-dk kova ortalaması +
// boşluk atlanır + son eksik kova atılır, en kötü pod z'ye göre (bantsız
// sayılmaz, eşitlikte ad), lastComplete = to'nun kovasından bir önceki.
func TestHeapReadHelpers(t *testing.T) {
	if HeapPodKey([]string{"", "", "abcdefghijkl"}) != "abcdefgh" || HeapPodKey([]string{"p1", "h", "i"}) != "p1" || HeapPodKey([]string{"", "host-a", ""}) != "host-a" || HeapPodKey(nil) != "" {
		t.Fatal("pod anahtarı sırası")
	}
	ser := func(pod string, pts ...[2]float64) chstore.SpanMetricSeries {
		s := chstore.SpanMetricSeries{GroupKey: []string{pod, "", ""}}
		for _, p := range pts {
			s.Points = append(s.Points, chstore.SpanMetricPoint{Time: int64(p[0]) * 1e9, Value: p[1]})
		}
		return s
	}
	pct := HeapPctByPod(
		[]chstore.SpanMetricSeries{ser("a", [2]float64{60, 30}, [2]float64{120, 60}, [2]float64{180, 0}), ser("b", [2]float64{60, 10})},
		[]chstore.SpanMetricSeries{ser("a", [2]float64{60, 100}, [2]float64{120, 100}, [2]float64{180, 100})},
	)
	if len(pct) != 1 || pct["a"][60] != 30 || pct["a"][120] != 60 || len(pct["a"]) != 2 {
		t.Fatalf("pct: %v", pct)
	}
	times, vals := HeapBuckets(map[int64]float64{0: 10, 60: 20, 300: 50, 900: 70, 1200: 99}, 900)
	if len(times) != 3 || times[0] != 0 || vals[0] != 15 || times[2] != 900 || vals[2] != 70 {
		t.Fatalf("kovalar: %v %v", times, vals)
	}
	bands := map[string]HeapBand{"x": {Status: HeapBandNoBaseline, Z: 99}, "y": {Status: HeapBandOK, Z: 0.4}, "z": {Status: HeapBandCritical, Z: 7.8}, "w": {Status: HeapBandCritical, Z: 7.8}}
	if HeapWorstPod(bands) != "w" || HeapWorstPod(map[string]HeapBand{"x": bands["x"]}) != "" {
		t.Fatalf("en kötü pod: %s", HeapWorstPod(bands))
	}
	to := time.Date(2026, 9, 23, 12, 4, 30, 0, time.UTC)
	if HeapLastComplete(to) != time.Date(2026, 9, 23, 11, 55, 0, 0, time.UTC).Unix() {
		t.Fatal("lastComplete")
	}
}
