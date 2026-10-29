package anomaly

// v0.10.1585 — Davis paritesi FAZ 0.5 / A: seyrek seride dwell kovaları
// ARDIŞIK olmalı. Belirti (FP sınıfı a_low_traffic / d_single_window):
// GROUP BY yalnız verisi olan 5 dk kovaları döndürdüğü için seyrek bir
// servisin son üç ÖRNEĞİ (55, 30 ve 5 dk önce) "son 15 dakika" sayılıyor ve
// tek tük isteklerden dwell=3 "sürekli" anomali açılıyordu. fillDwellGrid
// dwell penceresinin ızgara kovalarını (ilk gözlemden sonrakiler) sıfırla
// doldurur; taban kısmına dokunmaz (v0.9.449 kuyruk dolgusu aynen).

import (
	"reflect"
	"testing"
	"time"
)

func TestFillDwellGridInsertsSilentSlotsInsideDwellWindow(t *testing.T) {
	const s = int64(bucketSeconds)
	upper := int64(1_800_000_000) - int64(1_800_000_000)%s // ızgara hizalı
	at := func(k int) int64 { return upper - int64(k)*s }  // k kova önce başlayan
	cases := []struct {
		name               string
		times              []int64
		values             []float64
		dwell              int
		wantVals, wantRate []float64
	}{
		{
			name:  "boşluksuz seri aynen",
			times: []int64{at(5), at(4), at(3), at(2), at(1)}, values: []float64{1, 2, 3, 4, 5}, dwell: 3,
			wantVals: []float64{1, 2, 3, 4, 5}, wantRate: []float64{1, 2, 3, 4, 5},
		},
		{
			name:  "dwell penceresi içindeki boşluk sıfırla dolar",
			times: []int64{at(11), at(6), at(1)}, values: []float64{1, 2, 3}, dwell: 3,
			// taban [1 (11 önce), 2 (6 önce)]; pencere at(3), at(2), at(1) → 0, 0, 3
			wantVals: []float64{1, 2, 0, 0, 3}, wantRate: []float64{1, 2, 0, 0, 3},
		},
		{
			name:  "taban içindeki boşluğa dokunulmaz",
			times: []int64{at(9), at(5), at(3), at(2), at(1)}, values: []float64{1, 2, 3, 4, 5}, dwell: 3,
			wantVals: []float64{1, 2, 3, 4, 5}, wantRate: []float64{1, 2, 3, 4, 5},
		},
		{
			name:  "pencere ortasında tek boşluk",
			times: []int64{at(4), at(3), at(1)}, values: []float64{1, 2, 3}, dwell: 3,
			wantVals: []float64{1, 2, 0, 3}, wantRate: []float64{1, 2, 0, 3},
		},
		{
			name:  "susan kuyruk = padTrailingSilence birebir",
			times: []int64{at(8), at(7)}, values: []float64{1, 2}, dwell: 3,
			wantVals: []float64{1, 2, 0, 0, 0, 0, 0, 0}, wantRate: []float64{1, 2, 0, 0, 0, 0, 0, 0},
		},
		{
			name:  "ilk gözlemden ÖNCEKİ kova doldurulmaz",
			times: []int64{at(2), at(1)}, values: []float64{1, 2}, dwell: 3,
			wantVals: []float64{1, 2}, wantRate: []float64{1, 2},
		},
		{
			name:  "dwell 1: yalnız son kova (kuyruk dolgusuyla aynı)",
			times: []int64{at(6), at(3)}, values: []float64{1, 2}, dwell: 1,
			wantVals: []float64{1, 2, 0, 0}, wantRate: []float64{1, 2, 0, 0},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rates := append([]float64(nil), c.values...)
			gotV, gotR := fillDwellGrid(append([]float64(nil), c.values...), rates, c.times, upper, c.dwell)
			if !reflect.DeepEqual(gotV, c.wantVals) || !reflect.DeepEqual(gotR, c.wantRate) {
				t.Fatalf("fillDwellGrid = %v / %v, want %v / %v", gotV, gotR, c.wantVals, c.wantRate)
			}
		})
	}
}

// Explain replay'i canlı dedektörün AYNI dolgusunu kullanmalı: aynı
// gözlemlerden spanConsecutiveAt ile fetchAllBuckets'ın uyguladığı
// fillDwellGrid aynı seriyi üretir.
func TestSpanConsecutiveAtMirrorsFetchAllBucketsFill(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC().Add(90 * time.Second)
	upper := lastCompleteBucketStart(now).Unix()
	rows := map[int64]spanRow{}
	var times []int64
	var vals, rates []float64
	for _, k := range []int{200, 150, 40, 30, 20, 11, 6, 1} {
		b := upper - int64(k)*bucketSeconds
		rows[b] = spanRow{v: float64(k), rate: float64(k) / 10}
		times = append(times, b)
		vals = append(vals, float64(k))
		rates = append(rates, float64(k)/10)
	}
	for _, dwell := range []int{1, 3, 6} {
		gotV, gotR := spanConsecutiveAt(rows, now, dwell)
		wantV, wantR := fillDwellGrid(append([]float64(nil), vals...), append([]float64(nil), rates...), times, upper, dwell)
		if !reflect.DeepEqual(gotV, wantV) || !reflect.DeepEqual(gotR, wantR) {
			t.Fatalf("dwell=%d: replay %v/%v ≠ canlı %v/%v", dwell, gotV, gotR, wantV, wantR)
		}
		if len(gotV) < dwell || gotV[len(gotV)-2] != 0 && dwell >= 3 {
			t.Fatalf("dwell=%d: pencere içindeki boşluk dolmadı: %v", dwell, gotV)
		}
	}
}
