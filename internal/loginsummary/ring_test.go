package loginsummary

import (
	"fmt"
	"math"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func pt(sec int64, v float64) chstore.SpanMetricPoint {
	return chstore.SpanMetricPoint{Time: sec * 1e9, Value: v}
}

func TestRingMergeAlignsAndOverrides(t *testing.T) {
	r := NewRing()
	r.MergeStatus([]chstore.SpanMetricSeries{{GroupKey: []string{"200"}, Points: []chstore.SpanMetricPoint{pt(1000, 5), pt(1303, 6)}}})
	if len(r.B) != 2 || r.B[0].T != 900 || r.B[1].T != 1200 {
		t.Fatalf("300 sn hizalama: %+v", r.B)
	}
	r.MergeStatus([]chstore.SpanMetricSeries{{GroupKey: []string{"200"}, Points: []chstore.SpanMetricPoint{pt(1010, 9)}}})
	if len(r.B) != 2 || r.B[0].S["200"] != 9 {
		t.Fatalf("aynı kova yeni değerle ezilir: %+v", r.B)
	}
	r.MergeStatus([]chstore.SpanMetricSeries{{GroupKey: []string{"200"}, Points: []chstore.SpanMetricPoint{pt(100, 1)}}})
	if len(r.B) != 3 || r.B[0].T != 0 || r.B[1].T != 900 {
		t.Fatalf("sıralı ekleme: %+v", r.B)
	}
}

func TestRingIgnoresNaNInfNegative(t *testing.T) {
	r := NewRing()
	r.MergeStatus([]chstore.SpanMetricSeries{{GroupKey: []string{"200"}, Points: []chstore.SpanMetricPoint{pt(900, math.NaN()), pt(1200, math.Inf(1)), pt(1500, -1)}}})
	r.MergeSum([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(900, math.NaN())}}})
	if len(r.B) != 0 {
		t.Fatalf("geçersiz değerler atılır: %+v", r.B)
	}
}

func TestRingTrimBoundsToDayAnd288(t *testing.T) {
	r := NewRing()
	now := int64(1_000_000_000)
	now -= now % 300
	var pts []chstore.SpanMetricPoint
	for i := 0; i < 400; i++ { // 400 kova ≈ 33 saat
		pts = append(pts, pt(now-int64(i)*300, 1))
	}
	r.MergeStatus([]chstore.SpanMetricSeries{{GroupKey: []string{"200"}, Points: pts}})
	r.MarkFilled(ChunkStart(now - 30*3600))
	r.Trim(now)
	if len(r.B) > 288 {
		t.Errorf("en çok 288 kova, got %d", len(r.B))
	}
	if r.B[0].T < now-24*3600-300 {
		t.Errorf("24 saatten eski kova kalmamalı: %d", r.B[0].T)
	}
	if len(r.Filled) != 0 {
		t.Errorf("süresi dolan filled atılır: %v", r.Filled)
	}
}

func TestRingMissingChunks(t *testing.T) {
	r := NewRing()
	now := int64(1_700_000_000)
	recentFrom := now - now%300 - 900
	got := r.MissingChunks(now, recentFrom, 4)
	if len(got) != 4 {
		t.Fatalf("tur başına en çok 4 parça, got %v", got)
	}
	for i, c := range got {
		if c%3600 != 0 || c < now-24*3600 || c+3600 > recentFrom {
			t.Errorf("parça saat sınırlı, 24 sa içinde, yakın pencereyle çakışmayan olmalı: %d", c)
		}
		if i > 0 && c <= got[i-1] {
			t.Errorf("en eskiden sıralı: %v", got)
		}
	}
	for _, c := range got {
		r.MarkFilled(c)
	}
	next := r.MissingChunks(now, recentFrom, 4)
	for _, c := range next {
		for _, g := range got {
			if c == g {
				t.Errorf("doldurulmuş parça yeniden istenmez: %d", c)
			}
		}
	}
	// 24 sa = 24 parça; tamamı dolunca boş
	for i := 0; i < 10; i++ {
		for _, c := range r.MissingChunks(now, recentFrom, 4) {
			r.MarkFilled(c)
		}
	}
	if left := r.MissingChunks(now, recentFrom, 4); len(left) != 0 {
		t.Errorf("hepsi dolunca aday yok: %v", left)
	}
}

func TestRingViewSummary(t *testing.T) {
	r := NewRing()
	r.MergeStatus([]chstore.SpanMetricSeries{
		{GroupKey: []string{"200"}, Points: []chstore.SpanMetricPoint{pt(900, 10), pt(1200, 30), pt(1500, 20)}},
		{GroupKey: []string{"500"}, Points: []chstore.SpanMetricPoint{pt(900, 1), pt(1200, 2), pt(1500, 0)}},
	})
	r.MergeSum([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(1200, 6.4), pt(1500, 4)}}})
	r.MergeP95([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(1500, 0.25)}}})
	v, ok := r.View(true)
	if !ok {
		t.Fatal("görünüm beklenir")
	}
	if v.Summary.NowRPS != 20 || v.Summary.PeakRPS != 32 || v.Summary.PeakAt != 1200_000 {
		t.Errorf("özet: %+v", v.Summary)
	}
	if v.Summary.AvgNowS == nil || math.Abs(*v.Summary.AvgNowS-0.2) > 1e-9 {
		t.Errorf("avg_now_s = Q2/ΣQ1 = 4/20: %+v", v.Summary.AvgNowS)
	}
	if v.Avg == nil || len(v.Avg.Points) != 2 {
		t.Errorf("avg serisi yalnız Sum'lı kovalarda: %+v", v.Avg)
	}
	if v.P95 == nil || v.Summary.P95NowS == nil || *v.Summary.P95NowS != 0.25 {
		t.Errorf("p95: %+v", v.P95)
	}
	if v.Status[0].GroupKey[0] != "200" || v.Status[1].GroupKey[0] != "500" {
		t.Errorf("lejant sırası koda göre: %+v", v.Status)
	}
	if v.Status[0].Points[0].Time != 900*1e9 {
		t.Errorf("zaman unix ns: %+v", v.Status[0].Points[0])
	}
	if v2, _ := r.View(false); v2.P95 != nil || v2.Summary.P95NowS != nil {
		t.Error("allowP95 yoksa p95 yok")
	}
	if _, ok := (*Ring)(nil).View(true); ok {
		t.Error("nil halka → görünüm yok")
	}
	if _, ok := NewRing().View(true); ok {
		t.Error("boş halka → görünüm yok")
	}
}

func TestRingStatusCapIsTopEightByArea(t *testing.T) {
	r := NewRing()
	var series []chstore.SpanMetricSeries
	for i := 0; i < 12; i++ {
		series = append(series, chstore.SpanMetricSeries{GroupKey: []string{fmt.Sprintf("%d", 200+i)}, Points: []chstore.SpanMetricPoint{pt(900, float64(i+1))}})
	}
	r.MergeStatus(series)
	v, _ := r.View(false)
	if len(v.Status) != 8 {
		t.Fatalf("8 seri, got %d", len(v.Status))
	}
	for _, s := range v.Status {
		if s.GroupKey[0] < "204" { // en küçük 4 (200..203) elenir
			t.Errorf("küçük seri kalmamalı: %v", s.GroupKey)
		}
	}
}

func TestRingCloneIsIndependent(t *testing.T) {
	r := NewRing()
	r.MergeStatus([]chstore.SpanMetricSeries{{GroupKey: []string{"200"}, Points: []chstore.SpanMetricPoint{pt(900, 1)}}})
	r.MergeSum([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(900, 2)}}})
	c := r.Clone()
	r.B[0].S["200"] = 99
	*r.B[0].Sum = 99
	r.MergeStatus([]chstore.SpanMetricSeries{{GroupKey: []string{"200"}, Points: []chstore.SpanMetricPoint{pt(1200, 3)}}})
	if c.B[0].S["200"] != 1 || *c.B[0].Sum != 2 || len(c.B) != 1 {
		t.Errorf("klon bağımsız olmalı: %+v", c.B)
	}
	if (*Ring)(nil).Clone() != nil {
		t.Error("nil klon nil")
	}
}
