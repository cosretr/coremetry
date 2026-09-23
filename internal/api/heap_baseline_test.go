package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.887 (paritesi #4 dilim 1) — saf yardımcılar: pod anahtarı (pod →
// host → instance[:8]), pct = postgc/limit×100 yalnız limit>0, 5-dk kova
// ortalaması + son eksik kova atılır + boşluk atlanır, en kötü pod z'ye göre
// (bantsız sayılmaz); rota defterde, api.go'da değil.
func TestHeapBaselineHelpers(t *testing.T) {
	if anomaly.HeapPodKey([]string{"", "", "abcdefghijkl"}) != "abcdefgh" || anomaly.HeapPodKey([]string{"p1", "h", "i"}) != "p1" || anomaly.HeapPodKey([]string{"", "host-a", ""}) != "host-a" || anomaly.HeapPodKey(nil) != "" {
		t.Fatal("pod anahtarı sırası")
	}
	ser := func(pod string, pts ...[2]float64) chstore.SpanMetricSeries {
		s := chstore.SpanMetricSeries{GroupKey: []string{pod, "", ""}}
		for _, p := range pts {
			s.Points = append(s.Points, chstore.SpanMetricPoint{Time: int64(p[0]) * 1e9, Value: p[1]})
		}
		return s
	}
	pct := anomaly.HeapPctByPod(
		[]chstore.SpanMetricSeries{ser("a", [2]float64{60, 30}, [2]float64{120, 60}, [2]float64{180, 0}), ser("b", [2]float64{60, 10})},
		[]chstore.SpanMetricSeries{ser("a", [2]float64{60, 100}, [2]float64{120, 100}, [2]float64{180, 100})},
	)
	if len(pct) != 1 || pct["a"][60] != 30 || pct["a"][120] != 60 || len(pct["a"]) != 2 {
		t.Fatalf("pct: %v (b limitsiz düşer, 0 postgc düşer)", pct)
	}
	minutes := map[int64]float64{0: 10, 60: 20, 300: 50, 900: 70, 1200: 99}
	times, vals := anomaly.HeapBuckets(minutes, 900)
	if len(times) != 3 || times[0] != 0 || vals[0] != 15 || times[1] != 300 || vals[1] != 50 || times[2] != 900 || vals[2] != 70 {
		t.Fatalf("kovalar: %v %v (600 boşluk atlanır, 1200 eksik kova atılır)", times, vals)
	}
	bands := map[string]anomaly.HeapBand{
		"x": {Status: anomaly.HeapBandNoBaseline, Z: 99},
		"y": {Status: anomaly.HeapBandOK, Z: 0.4},
		"z": {Status: anomaly.HeapBandCritical, Z: 7.8},
	}
	if anomaly.HeapWorstPod(bands) != "z" || anomaly.HeapWorstPod(map[string]anomaly.HeapBand{"x": bands["x"]}) != "" {
		t.Fatal("en kötü pod")
	}
	if _, ok := extraRouteRegistrars["heap-baseline"]; !ok {
		t.Fatal("rota defterde değil")
	}
	b, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "heap-baseline") {
		t.Fatal("api.go büyümez — rota kendi dosyasında kalmalı")
	}
}

// v0.10.887 (inceleme) — buildHeapBaseline şekli: iki sorgu (postgc, limit)
// 5-dk adım, 24 s tarihçe, üçlü GroupBy, heap süzgeci; son eksik kova atılır;
// seri sayfa penceresine kırpılır; en kötü pod kesimden önce; metrik yoksa
// pods boş + Reason; ölmüş pod canlıların arkasına düşer, lastTs taşır.
func TestBuildHeapBaselineShape(t *testing.T) {
	to := time.Date(2026, 9, 23, 12, 4, 30, 0, time.UTC) // son tam kova 11:55 (12:00 kovası eksik)
	from := to.Add(-time.Hour)
	mk := func(pod string, vals map[int]float64) chstore.SpanMetricSeries { // i = kova indeksi (0 = to-24h)
		s := chstore.SpanMetricSeries{GroupKey: []string{pod, "", ""}}
		base := to.Add(-24 * time.Hour).Truncate(5 * time.Minute)
		for i := 0; i < 289; i++ {
			v, ok := vals[i]
			if !ok {
				continue
			}
			s.Points = append(s.Points, pt(base.Add(time.Duration(i)*5*time.Minute), v))
		}
		return s
	}
	all := func(v float64, upto int) map[int]float64 {
		m := map[int]float64{}
		for i := 0; i <= upto; i++ {
			m[i] = v
		}
		return m
	}
	live := all(60, 288)
	for i := 286; i <= 288; i++ {
		live[i] = 95 // son 3 kova sıçrama (288 = 12:00 kovası, eksik → atılır; 285/286/287 pencere)
	}
	live[285] = 95
	dead := all(70, 200) // 200. kovadan sonra sustu
	src := &fakeHostSource{name: "vm", exists: map[string]bool{anomaly.HeapMetricPostGC: true},
		queryFn: func(f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
			if f.Name == anomaly.HeapMetricLimit {
				return []chstore.SpanMetricSeries{mk("live-1", all(100, 288)), mk("dead-1", all(100, 288))}, nil
			}
			return []chstore.SpanMetricSeries{mk("live-1", live), mk("dead-1", dead)}, nil
		}}
	pol := anomaly.HeapPolicyFrom(chstore.DefaultAnomalySensitivity())
	res, err := buildHeapBaseline(context.Background(), src, "svc", from, to, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.filters) != 2 {
		t.Fatalf("iki sorgu: %v", src.calls)
	}
	for _, f := range src.filters {
		if f.Aggregation != "sum" || f.Service != "svc" || len(f.GroupBy) != 3 || f.StepSeconds != 300 || !f.From.Equal(to.Add(-24*time.Hour)) || !f.To.Equal(to) || len(f.Filters) != 1 || f.Filters[0].Key != "jvm.memory.type" {
			t.Fatalf("sorgu şekli: %+v", f)
		}
	}
	if len(res.Pods) != 2 || res.Pods[0].Pod != "live-1" || res.Pods[1].Pod != "dead-1" {
		t.Fatalf("canlı önce: %+v", res.Pods)
	}
	l := res.Pods[0]
	if l.Band.Status != anomaly.HeapBandCritical || l.Band.Current != 95 || l.LastTs != to.Truncate(5*time.Minute).Add(-5*time.Minute).Unix() {
		t.Fatalf("canlı pod: %+v", l.Band)
	}
	for _, p := range l.Series {
		if p.TimeNs < from.UnixNano() || p.TimeNs > to.UnixNano() || p.TimeNs/1e9 > l.LastTs {
			t.Fatalf("seri pencere dışı / eksik kova: %v", p)
		}
	}
	if len(l.Series) != 11 { // 11:05..11:55 (12:00 eksik kova atılır) = 11 kova; 11:00 < from(11:04:30)
		t.Fatalf("seri uzunluğu %d", len(l.Series))
	}
	if res.Pods[1].LastTs >= to.Add(-time.Hour).Unix() || res.Pods[1].Band.Status != anomaly.HeapBandOK {
		t.Fatalf("ölü pod: %+v", res.Pods[1])
	}
	if res.Worst != "live-1" || res.Capped || res.Truncated != 0 || res.Source != "vm" || res.NeedBuckets != 15 {
		t.Fatalf("özet: %+v", res)
	}
	// metrik yok → pods boş, tek exists çağrısı, sorgu yok
	none := &fakeHostSource{name: "ch", exists: map[string]bool{}}
	r2, err := buildHeapBaseline(context.Background(), none, "svc", from, to, pol)
	if err != nil || len(r2.Pods) != 0 || r2.Reason == "" || len(none.filters) != 0 {
		t.Fatalf("metrik yok: %+v %v", r2, err)
	}
}
