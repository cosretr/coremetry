package loginsummary

// ring.go — 288 noktalık (24 sa × 5 dk) HTTP halkası (audit §5.3).
//
// VM her turda yalnız son 15 dakikayı (+ tur başına en çok 4 boşluk parçası)
// döndürür; halka bunları 5 dk'lık hizalı kovalara birleştirir ve anlık
// görüntüde durur — lider değişince ya da pod yeniden başlayınca 24 saat
// yeniden taranmaz. Bu dosya SAF: VM'e dokunmaz.

import (
	"math"
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	ringStepS   = 300
	ringMaxBuck = 288 // 24 sa / 5 dk
	ringSpanS   = 24 * 3600
	// ringChunkS — boşluk doldurma parçası (1 saat).
	ringChunkS = 3600
)

// RingBucket — bir 5 dk'lık kova. R: TOPLAM istek/sn (Q1); Sum: yanıt süresi
// toplam hızı (Q2, sn/sn). v0.10.1922: p95 (Q3) kaldırıldı.
//
// v0.10.1914 — durum kodu kırılımı kalktı (operatör: "illa status code şart
// değil"): tek toplam throughput serisi. Eski hâl `sum by (status)` idi ve
// gerçek VM istemcisinin grup anahtarı `etiket="200"` biçiminde geldiği için
// süzgeç tüm serileri atıyor, kart hiç çizilmiyordu. Gruplamasız sorguda
// ayrıştırılacak etiket yok; giriş öncesi hata dağılımı da görünmez.
type RingBucket struct {
	T   int64    `json:"t"` // unix sn, 300'ün katı
	R   *float64 `json:"r,omitempty"`
	Sum *float64 `json:"sum,omitempty"`
}

// Ring — kovalar artan zamanla; Filled: boşluk doldurması TAMAMLANMIŞ saatlik
// parçaların başlangıcı (unix sn) — trafiksiz bir saat sonsuza dek yeniden
// sorgulanmasın.
type Ring struct {
	Step   int          `json:"step"`
	B      []RingBucket `json:"b"`
	Filled []int64      `json:"filled,omitempty"`
}

// NewRing — boş halka.
func NewRing() *Ring { return &Ring{Step: ringStepS} }

func alignT(tNs int64) int64 {
	s := tNs / 1e9
	return s - s%ringStepS
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (r *Ring) bucket(t int64) *RingBucket {
	i := sort.Search(len(r.B), func(i int) bool { return r.B[i].T >= t })
	if i < len(r.B) && r.B[i].T == t {
		return &r.B[i]
	}
	r.B = append(r.B, RingBucket{})
	copy(r.B[i+1:], r.B[i:])
	r.B[i] = RingBucket{T: t}
	return &r.B[i]
}

// MergeRate — Q1 (toplam istek hızı). Sorgu gruplamasızdır ve tek seri
// döner; yine de birden çok seri gelirse aynı kovadaki değerler TOPLANIR
// (grup anahtarı okunmaz — biçimine güvenilmez). Kovanın önceki değeri ezilir.
func (r *Ring) MergeRate(series []chstore.SpanMetricSeries) {
	acc := map[int64]float64{}
	for _, s := range series {
		for _, p := range s.Points {
			if !finite(p.Value) || p.Value < 0 {
				continue
			}
			acc[alignT(p.Time)] += p.Value
		}
	}
	for t, v := range acc {
		v := v
		r.bucket(t).R = &v
	}
}

// MergeSum — Q2 (yanıt süresi toplam hızı).
func (r *Ring) MergeSum(series []chstore.SpanMetricSeries) {
	for _, s := range series {
		for _, p := range s.Points {
			if !finite(p.Value) || p.Value < 0 {
				continue
			}
			v := p.Value
			r.bucket(alignT(p.Time)).Sum = &v
		}
	}
}

// Trim — 24 saatten eski ve 288'i aşan kovaları (en eskiden) atar; süresi
// dolan Filled girdilerini de.
func (r *Ring) Trim(nowSec int64) {
	cut := nowSec - ringSpanS
	cut -= cut % ringStepS
	i := sort.Search(len(r.B), func(i int) bool { return r.B[i].T >= cut })
	r.B = r.B[i:]
	if len(r.B) > ringMaxBuck {
		r.B = r.B[len(r.B)-ringMaxBuck:]
	}
	keep := r.Filled[:0]
	for _, f := range r.Filled {
		if f+ringChunkS > cut {
			keep = append(keep, f)
		}
	}
	r.Filled = keep
}

func (r *Ring) filled(c int64) bool {
	for _, f := range r.Filled {
		if f == c {
			return true
		}
	}
	return false
}

// MarkFilled — saatlik parçayı tamamlanmış işaretler.
func (r *Ring) MarkFilled(chunkStart int64) {
	if !r.filled(chunkStart) {
		r.Filled = append(r.Filled, chunkStart)
	}
}

// ChunkStart — saatlik parça başlangıcı: saat sınırına hizalı (Unix saati).
func ChunkStart(t int64) int64 { return t - t%ringChunkS }

// MissingChunks — boşluk doldurma adayları: tamamı `recentFromSec`ten ÖNCE
// kalan, 24 sa içindeki, Filled'da olmayan saatlik parçalar, en eskiden;
// en çok `limit` adet. Yakın pencereyle çakışan parçalar atlanır (her turda
// zaten çekiliyor; geçmişe karışınca bir kez sorgulanır).
func (r *Ring) MissingChunks(nowSec, recentFromSec int64, limit int) []int64 {
	first := ChunkStart(nowSec - ringSpanS)
	if first < nowSec-ringSpanS {
		first += ringChunkS
	}
	var out []int64
	for c := first; c+ringChunkS <= recentFromSec && len(out) < limit; c += ringChunkS {
		if !r.filled(c) {
			out = append(out, c)
		}
	}
	return out
}

// ── Okuma: cevap serileri + özet ────────────────────────────────────────

// View — halkadan cevaba giren hâl.
type View struct {
	Throughput *chstore.SpanMetricSeries
	Avg        *chstore.SpanMetricSeries
	Summary    HTTPSummary
	From       int64 // unix ms
	To         int64 // unix ms
}

// View — halkayı cevaba çevirir. Hiç hız verisi yoksa ok=false (kart gizlenir).
func (r *Ring) View() (View, bool) {
	var v View
	if r == nil || len(r.B) == 0 {
		return v, false
	}
	var tput, avg chstore.SpanMetricSeries
	var peak float64
	var peakAt int64
	var lastTotal float64
	var lastAvg *float64
	for _, b := range r.B {
		if b.R != nil {
			tot := *b.R
			tput.Points = append(tput.Points, chstore.SpanMetricPoint{Time: b.T * 1e9, Value: tot})
			lastTotal = tot
			if tot > peak {
				peak, peakAt = tot, b.T
			}
			if b.Sum != nil && tot > 0 {
				a := *b.Sum / tot
				avg.Points = append(avg.Points, chstore.SpanMetricPoint{Time: b.T * 1e9, Value: a})
				lastAvg = &a
			}
		}
	}
	if len(tput.Points) == 0 {
		return v, false
	}
	tput.GroupKey = []string{}
	v.Throughput = &tput
	if len(avg.Points) > 0 {
		avg.GroupKey = []string{}
		v.Avg = &avg
	}
	v.Summary = HTTPSummary{NowRPS: lastTotal, PeakRPS: peak, PeakAt: peakAt * 1000, AvgNowS: lastAvg}
	v.From = r.B[0].T * 1000
	v.To = (r.B[len(r.B)-1].T + ringStepS) * 1000
	return v, true
}

// Clone — derin kopya. Builder çalışma halkasını değiştirirken yayınlanmış
// anlık görüntüyü okuyan uç yarışmasın diye yayın öncesi alınır.
func (r *Ring) Clone() *Ring {
	if r == nil {
		return nil
	}
	c := &Ring{Step: r.Step, B: make([]RingBucket, len(r.B)), Filled: append([]int64(nil), r.Filled...)}
	for i, b := range r.B {
		nb := RingBucket{T: b.T}
		if b.R != nil {
			v := *b.R
			nb.R = &v
		}
		if b.Sum != nil {
			v := *b.Sum
			nb.Sum = &v
		}
		c.B[i] = nb
	}
	return c
}
