package chstore

import (
	"strings"
	"testing"
	"time"
)

func TestPeerBinsFromQuantiles(t *testing.T) {
	bins := heatmapDurationBins(peerMinLogMs, peerSubBins, peerBins)
	// Tek nokta kütlesi: tüm kuantiller 10 ms → hepsi 10 ms'i taşıyan kovada.
	vals := make([]float64, len(peerQuantileLevels))
	for i := range vals {
		vals[i] = 10e6
	}
	got := peerBinsFromQuantiles(peerQuantileLevels, vals, 1000, bins)
	var sum uint32
	own := -1
	for j, c := range got {
		sum += c
		if c > 0 {
			own = j
		}
	}
	if sum != 1000 || bins[own] < 10 || (own > 0 && bins[own-1] >= 10) {
		t.Fatalf("nokta kütlesi 10 ms kovasında olmalı: own=%d sum=%d %v", own, sum, got)
	}

	// Doğrusal-log yayılım 1 ms → 100 ms: kütle o aralığın kovalarında, toplam korunur.
	for i, l := range peerQuantileLevels {
		vals[i] = 1e6 * pow10(2*l)
	}
	got = peerBinsFromQuantiles(peerQuantileLevels, vals, 10000, bins)
	sum = 0
	for j, c := range got {
		sum += c
		if c > 0 && (bins[j] < 1 || (j > 0 && bins[j-1] > 100.01)) {
			t.Errorf("kova %d (≤%.2f ms) aralık dışında: %d", j, bins[j], c)
		}
	}
	if sum < 9990 || sum > 10010 {
		t.Errorf("toplam ≈10000 olmalı (yuvarlama), %d", sum)
	}
	if out := peerBinsFromQuantiles(nil, nil, 5, bins); len(out) != len(bins) {
		t.Error("boş girdi sıfır dizisi döndürmeli")
	}
}

func TestSpanPeersMVSQL(t *testing.T) {
	sql := spanPeersMVSQL()
	for _, w := range []string{"FROM spanmetrics_1m", "service_name = ? AND name = ?", "time_bucket >= ? AND time_bucket < ?",
		"quantilesTDigestMerge(0, 0.01,", "LIMIT 1", "max_execution_time"} {
		if !strings.Contains(sql, w) {
			t.Errorf("SQL %q taşımalı:\n%s", w, sql)
		}
	}
}

func TestBubbleUpSlices(t *testing.T) {
	from := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	p, a, r := bubbleUpSlices(from, from.Add(2*time.Minute), 3*time.Minute, 12)
	if r != 1 || len(a) != 2 || p != "(time >= ? AND time <= ?)" {
		t.Fatalf("bütçe içi pencere tam okunmalı: %s %v %v", p, a, r)
	}
	p, a, r = bubbleUpSlices(from, from.Add(24*time.Hour), 3*time.Minute, 12)
	if len(a) != 24 || strings.Count(p, " OR ") != 11 || r < 0.00208 || r > 0.00209 {
		t.Fatalf("24 sa: 12 dilim, oran 3dk/24sa: %d args, %v", len(a), r)
	}
	for i := 0; i < 12; i++ { // eşit aralıklı, 15 sn
		lo, hi := a[2*i].(time.Time), a[2*i+1].(time.Time)
		if !lo.Equal(from.Add(time.Duration(i)*2*time.Hour)) || hi.Sub(lo) != 15*time.Second {
			t.Errorf("dilim %d: %v–%v", i, lo, hi)
		}
	}
}

func TestScaleBubbleUp(t *testing.T) {
	r := &BubbleUpResult{SelectionTotal: 10, BaselineTotal: 100, Attributes: []BubbleUpAttribute{{
		Key: "k", Values: []BubbleUpValue{{SelectionCount: 5, BaselineCount: 7, SelectionPct: 0.5, BaselinePct: 0.07, Score: 0.43}}}}}
	scaleBubbleUp(r, 1, 0.25)
	v := r.Attributes[0].Values[0]
	if r.SelectionTotal != 10 || r.BaselineTotal != 400 || v.SelectionCount != 5 || v.BaselineCount != 28 ||
		v.SelectionPct != 0.5 || v.Score != 0.43 || r.SelectionSampleRate != 1 || r.BaselineSampleRate != 0.25 {
		t.Fatalf("ölçekleme yanlış: %+v", r)
	}
	r2 := &BubbleUpResult{SelectionTotal: 3}
	scaleBubbleUp(r2, 1, 1)
	if r2.SelectionTotal != 3 || r2.BaselineSampleRate != 0 {
		t.Fatal("oran 1 dokunmamalı")
	}
}
