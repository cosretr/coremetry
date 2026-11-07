package loginsummary

import (
	"math"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func pt(sec int64, v float64) chstore.SpanMetricPoint {
	return chstore.SpanMetricPoint{Time: sec * 1e9, Value: v}
}

func TestRingMergeAlignsAndOverrides(t *testing.T) {
	r := NewRing()
	r.MergeRate([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(1000, 5), pt(1303, 6)}}})
	if len(r.B) != 2 || r.B[0].T != 900 || r.B[1].T != 1200 {
		t.Fatalf("300 sn hizalama: %+v", r.B)
	}
	r.MergeRate([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(1010, 9)}}})
	if len(r.B) != 2 || r.B[0].R == nil || *r.B[0].R != 9 {
		t.Fatalf("aynı kova yeni değerle ezilir: %+v", r.B)
	}
	r.MergeRate([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(100, 1)}}})
	if len(r.B) != 3 || r.B[0].T != 0 || r.B[1].T != 900 {
		t.Fatalf("sıralı ekleme: %+v", r.B)
	}
}

func TestRingIgnoresNaNInfNegative(t *testing.T) {
	r := NewRing()
	r.MergeRate([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(900, math.NaN()), pt(1200, math.Inf(1)), pt(1500, -1)}}})
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
	r.MergeRate([]chstore.SpanMetricSeries{{Points: pts}})
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
	// Gerçek istemci gruplamasız sorguda TEK seri döner (GroupKey nil). İki seri
	// gelirse (savunma) aynı kovadaki değerler toplanır: 10+1, 30+2, 20+0.
	r.MergeRate([]chstore.SpanMetricSeries{
		{Points: []chstore.SpanMetricPoint{pt(900, 10), pt(1200, 30), pt(1500, 20)}},
		{GroupKey: []string{`http_response_status_code="500"`}, Points: []chstore.SpanMetricPoint{pt(900, 1), pt(1200, 2), pt(1500, 0)}},
	})
	r.MergeSum([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(1200, 6.4), pt(1500, 4)}}})
	v, ok := r.View()
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
	if v.Throughput == nil || len(v.Throughput.Points) != 3 || len(v.Throughput.GroupKey) != 0 {
		t.Fatalf("tek toplam hız serisi, grup anahtarı boş: %+v", v.Throughput)
	}
	if v.Throughput.Points[0].Time != 900*1e9 || v.Throughput.Points[1].Value != 32 {
		t.Errorf("zaman unix ns, değer toplam: %+v", v.Throughput.Points)
	}
	if _, ok := (*Ring)(nil).View(); ok {
		t.Error("nil halka → görünüm yok")
	}
	if _, ok := NewRing().View(); ok {
		t.Error("boş halka → görünüm yok")
	}
}

// v0.10.1914 (operatör-raporlu: grafik kartı hiç çıkmadı) — eski MergeStatus
// grup anahtarını çıplak "200" bekliyor, gerçek VM istemcisi ise
// `http_response_status_code="200"` döndürüyordu: her seri atılıyordu. MergeRate
// grup anahtarını HİÇ okumaz; hangi biçimde gelirse gelsin veri kaybolmaz.
func TestRingMergeRateIgnoresGroupKeyShape(t *testing.T) {
	for _, key := range [][]string{nil, {}, {"200"}, {`http_response_status_code="200"`}, {"a", "b"}} {
		r := NewRing()
		r.MergeRate([]chstore.SpanMetricSeries{{GroupKey: key, Points: []chstore.SpanMetricPoint{pt(900, 7), pt(1200, 8)}}})
		v, ok := r.View()
		if !ok || v.Throughput == nil || len(v.Throughput.Points) != 2 || v.Summary.NowRPS != 8 {
			t.Errorf("grup anahtarı %q: veri kaybolmamalı: ok=%v %+v", key, ok, v.Throughput)
		}
	}
}

// Yalnız Q2 verisi olan (hız verisi olmayan) halka kart üretmez.
func TestRingViewNeedsRate(t *testing.T) {
	r := NewRing()
	r.MergeSum([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(900, 2)}}})
	if _, ok := r.View(); ok {
		t.Error("hız verisi yokken görünüm olmamalı")
	}
}

func TestRingCloneIsIndependent(t *testing.T) {
	r := NewRing()
	r.MergeRate([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(900, 1)}}})
	r.MergeSum([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(900, 2)}}})
	c := r.Clone()
	*r.B[0].R = 99
	*r.B[0].Sum = 99
	r.MergeRate([]chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{pt(1200, 3)}}})
	if *c.B[0].R != 1 || *c.B[0].Sum != 2 || len(c.B) != 1 {
		t.Errorf("klon bağımsız olmalı: %+v", c.B)
	}
	if (*Ring)(nil).Clone() != nil {
		t.Error("nil klon nil")
	}
}
