package anomaly

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.891 (paritesi #4 dilim 2, gölge) — halka: sıralı ekleme, aynı kova üstüne
// yazma, tavan; karar tablosu: en kötü canlı pod critical → open, açık yokken
// none, canlı pod yok → resolve (pod silent), maxZ ≤ resolveZ → resolve, aksi
// none; kaynak seçimi auto/vm/ch; faz kümeleme SONRASI, davranış ÖNCESİ.
func TestHeapRingPut(t *testing.T) {
	r := &heapRing{}
	for _, tv := range [][2]int64{{600, 1}, {300, 2}, {900, 3}, {600, 9}} {
		r.put(tv[0], float64(tv[1]))
	}
	if len(r.times) != 3 || r.times[0] != 300 || r.vals[1] != 9 || r.last() != 900 {
		t.Fatalf("halka: %v %v", r.times, r.vals)
	}
	for i := int64(0); i < heapRingLen+10; i++ {
		r.put(1000+i*300, float64(i))
	}
	if len(r.times) != heapRingLen || r.times[0] != 1000+10*300 {
		t.Fatalf("tavan: n=%d ilk=%d", len(r.times), r.times[0])
	}
}

func TestHeapDecideTable(t *testing.T) {
	crit := HeapBand{Status: HeapBandCritical, Z: 7, Current: 90, Median: 60}
	ok := HeapBand{Status: HeapBandOK, Z: 0.4}
	mid := HeapBand{Status: HeapBandOK, Z: 2.5}
	none := HeapBand{Status: HeapBandNoBaseline}
	cases := []struct {
		name    string
		bands   map[string]HeapBand
		live    map[string]bool
		hasOpen bool
		action  string
		pod     string
	}{
		{"canlı critical → open", map[string]HeapBand{"a": crit, "b": ok}, map[string]bool{"a": true, "b": true}, false, "open", "a"},
		{"critical ama ölü → open değil, açık yok → none", map[string]HeapBand{"a": crit, "b": ok}, map[string]bool{"b": true}, false, "none", "b"},
		{"açık var, canlı yok → resolve", map[string]HeapBand{"a": mid}, map[string]bool{}, true, "resolve", ""},
		{"açık var, maxZ ≤ 1.5 → resolve", map[string]HeapBand{"a": ok, "n": none}, map[string]bool{"a": true, "n": true}, true, "resolve", "a"},
		{"açık var, histerezis → none", map[string]HeapBand{"a": mid}, map[string]bool{"a": true}, true, "none", "a"},
		{"bant yok → none", map[string]HeapBand{"n": none}, map[string]bool{"n": true}, false, "none", ""},
		{"tek kovalık yüksek z (ok) critical'ı maskeleyemez", map[string]HeapBand{"a": crit, "b": {Status: HeapBandOK, Z: 9}}, map[string]bool{"a": true, "b": true}, false, "open", "a"},
	}
	for _, c := range cases {
		d := heapDecide(c.bands, c.live, c.hasOpen)
		if d.Action != c.action || d.Pod != c.pod {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
	if !heapWouldP1("svc", HeapBand{Current: 90, Median: 40}, time.Now()) || heapWouldP1("svc", HeapBand{Current: 90, Median: 60}, time.Now()) {
		t.Error("would-P1: 2× oranı P1, 1.5× P2")
	}
}

type fakeHeapBackend struct {
	configured bool
	series     map[string][]chstore.SpanMetricSeries
	labels     []string
	calls      []chstore.MetricQueryFilter
}

func (f *fakeHeapBackend) Configured() bool { return f.configured }
func (f *fakeHeapBackend) MetricExists(context.Context, string) (bool, error) {
	return true, nil
}

// QueryMetric — filo-geneli (4 anahtar) ve servis-parçalı (3 anahtar) sorgular
// farklı grup şekli döndürür; fixture ikisini de taşır, GroupBy uzunluğuna göre seçer.
func (f *fakeHeapBackend) QueryMetric(_ context.Context, q chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	f.calls = append(f.calls, q)
	var out []chstore.SpanMetricSeries
	for _, s := range f.series[q.Name] {
		if len(s.GroupKey) == len(q.GroupBy) {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeHeapBackend) MetricLabelValues(context.Context, string, string, time.Duration, string, int) ([]string, error) {
	return f.labels, nil
}

func TestHeapSourcePick(t *testing.T) {
	vm, ch := &fakeHeapBackend{configured: false}, &fakeHeapBackend{}
	s := NewHeapSource(vm, ch)
	if s.pick(chstore.HeapSourceAuto).name != "ch" || s.pick(chstore.HeapSourceVM).name != "vm" || s.pick(chstore.HeapSourceCH).name != "ch" {
		t.Fatal("auto → ch (VM yapılandırılmamış); vm zorlanır; ch")
	}
	vm.configured = true
	if s.pick(chstore.HeapSourceAuto).name != "vm" {
		t.Fatal("auto → vm (yapılandırılmış)")
	}
	if NewHeapSource(nil, ch).pick(chstore.HeapSourceVM).name != "ch" {
		t.Fatal("vm yokken zorlansa da ch (dürüst ad)")
	}
	var none *HeapSourceOr
	if none.pick("auto").HeapBackend != nil {
		t.Fatal("nil kaynak")
	}
}

// Gölge tik uçtan uca sahte backend'le: doldurma (1 servis) + artımlı okuma +
// hüküm; Problem yazılmaz (store nil → applyOutcome hiç çağrılmaz, panik yok).
func TestScanHeapShadowEndToEnd(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 4, 30, 0, time.UTC)
	base := now.Add(-HeapHistory).Truncate(5 * time.Minute)
	mk := func(gk []string, val func(i int) float64) chstore.SpanMetricSeries {
		s := chstore.SpanMetricSeries{GroupKey: gk}
		for i := 0; i < 289; i++ {
			s.Points = append(s.Points, chstore.SpanMetricPoint{Time: base.Add(time.Duration(i) * 5 * time.Minute).UnixNano(), Value: val(i)})
		}
		return s
	}
	heap := func(i int) float64 {
		if i >= 284 {
			return 95
		}
		return 60 + float64(i%3)
	}
	lim := func(int) float64 { return 100 }
	be := &fakeHeapBackend{configured: true, labels: []string{"shop"}, series: map[string][]chstore.SpanMetricSeries{
		HeapMetricPostGC: {mk([]string{"shop", "shop-1", "", ""}, heap), mk([]string{"shop-1", "", ""}, heap)}, // filo / servis şekli
		HeapMetricLimit:  {mk([]string{"shop", "shop-1", "", ""}, lim), mk([]string{"shop-1", "", ""}, lim)},
	}}
	d := &Detector{}
	d.SetHeapSource(NewHeapSource(be, be))
	sens := chstore.DefaultAnomalySensitivity()
	sens.Runtime.HeapMode = chstore.HeapModeShadow
	snap := &chstore.OpenProblems{}
	d.scanHeap(context.Background(), now, snap, sens) // tik 1: doldurma + artımlı
	// Artımlı pencere: from = L−300, to = L+300 (VM t anında BİTEN kovayı verir).
	last := be.calls[len(be.calls)-1]
	L := HeapLastComplete(now)
	if last.From.Unix() != L-300 || last.To.Unix() != L+300 || len(last.GroupBy) != 4 {
		t.Fatalf("artımlı pencere: %v..%v groupBy=%v", last.From.Unix()-L, last.To.Unix()-L, last.GroupBy)
	}
	st := HeapObservability()
	if !st.Backfilled || st.RingServices != 1 || st.RingPods != 1 || st.Mode != "shadow" || st.Source != "vm" {
		t.Fatalf("gölge tik: %+v", st)
	}
	if st.WouldOpenNow != 1 || st.WouldOpenTotal < 1 || st.WouldP1Now != 0 {
		t.Fatalf("would-open sayacı (95/61 = 1.56× → P2): %+v", st)
	}
	d.scanHeap(context.Background(), now, snap, sens) // tik 2: geçiş sayılmaz
	if HeapObservability().WouldOpenTotal != 1 {
		t.Fatal("aynı servis ikinci tikte yeniden sayılmamalı")
	}
	sens.Runtime.HeapMode = chstore.HeapModeOff
	d.scanHeap(context.Background(), now, snap, sens)
	if d.heap != nil || HeapObservability().RingPods != 0 {
		t.Fatal("off → halka boşalır")
	}
}

func TestHeapPhasePlacement(t *testing.T) {
	src := detectorSrc(t)
	i := strings.Index(src, "func (d *Detector) scan(")
	body := src[i:]
	iCluster, iHeap, iBehavior := strings.Index(body, "d.resolveStaleClusters("), strings.Index(body, "d.scanHeap("), strings.Index(body, "d.scanBehavior(")
	if iCluster < 0 || iHeap < 0 || iBehavior < 0 || !(iCluster < iHeap && iHeap < iBehavior) {
		t.Fatalf("heap fazı kümeleme SONRASI ve davranış ÖNCESİ olmalı: %d %d %d", iCluster, iHeap, iBehavior)
	}
}

// Tavan: filo sonucu korunur, üstüne servis-parçalı okuma (probe yok, tik
// başına ≤ heapCapPerTick, 6 kova pencere), rotasyon devreder.
func TestScanHeapCappedRotation(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 4, 30, 0, time.UTC)
	L := HeapLastComplete(now)
	one := func(gk []string) chstore.SpanMetricSeries {
		return chstore.SpanMetricSeries{GroupKey: gk, Points: []chstore.SpanMetricPoint{{Time: L * 1e9, Value: 50}}}
	}
	// Filo şekli: HeapVMSeriesCap kadar seri → capped.
	fleetPost, fleetLim := []chstore.SpanMetricSeries{}, []chstore.SpanMetricSeries{}
	for i := 0; i < HeapVMSeriesCap; i++ {
		gk := []string{"svc-fleet", "pod-" + itoa(i), "", ""}
		fleetPost = append(fleetPost, one(gk))
		fleetLim = append(fleetLim, chstore.SpanMetricSeries{GroupKey: gk, Points: []chstore.SpanMetricPoint{{Time: L * 1e9, Value: 100}}})
	}
	be := &fakeHeapBackend{configured: true, labels: []string{"svc-a", "svc-b", "svc-c"}, series: map[string][]chstore.SpanMetricSeries{
		HeapMetricPostGC: append(fleetPost, one([]string{"pod-x", "", ""})),
		HeapMetricLimit:  append(fleetLim, chstore.SpanMetricSeries{GroupKey: []string{"pod-x", "", ""}, Points: []chstore.SpanMetricPoint{{Time: L * 1e9, Value: 100}}}),
	}}
	d := &Detector{heap: &heapState{rings: map[string]map[string]*heapRing{}, wouldOpen: map[string]bool{}, lastSource: "vm", backfilled: true}}
	d.SetHeapSource(NewHeapSource(be, be))
	sens := chstore.DefaultAnomalySensitivity()
	sens.Runtime.HeapMode = chstore.HeapModeShadow
	d.scanHeap(context.Background(), now, &chstore.OpenProblems{}, sens)
	st := HeapObservability()
	if !st.Capped || st.RingServices != 4 { // filo (svc-fleet) + parçalı 3 servis
		t.Fatalf("tavan: filo korunmalı + parçalı: %+v", st)
	}
	perSvc := 0
	for _, c := range be.calls {
		if c.Service != "" {
			perSvc++
			if c.From.Unix() != L-(heapCapBuckets-1)*HeapBucketSec || c.To.Unix() != L+HeapBucketSec {
				t.Fatalf("parçalı pencere: %v", c)
			}
		}
	}
	if perSvc != 6 { // 3 servis × 2 metrik, probe yok
		t.Fatalf("parçalı sorgu sayısı %d", perSvc)
	}
	if len(d.heap.capPending) != 0 {
		t.Fatal("3 servis tek tikte biter")
	}
}

func itoa(i int) string {
	return string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
}
