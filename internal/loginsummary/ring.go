package loginsummary

// ring.go — 288 noktalık (24 sa × 5 dk) HTTP halkası (audit §5.3).
//
// VM her turda yalnız son 15 dakikayı (+ tur başına en çok 4 boşluk parçası)
// döndürür; halka bunları 5 dk'lık hizalı kovalara birleştirir ve anlık
// görüntüde durur — lider değişince ya da pod yeniden başlayınca 24 saat
// yeniden taranmaz. Bu dosya SAF: VM'e dokunmaz.

import (
	"math"
	"regexp"
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	ringStepS   = 300
	ringMaxBuck = 288 // 24 sa / 5 dk
	ringSpanS   = 24 * 3600
	// ringChunkS — boşluk doldurma parçası (1 saat).
	ringChunkS = 3600
	// ringMaxStatus — cevaba giren en çok durum serisi.
	ringMaxStatus = 8
)

// statusLabelRe — cevaba yalnız gerçek HTTP durum kodları girer (3 hane, 1-5).
var statusLabelRe = regexp.MustCompile(`^[1-5][0-9]{2}$`)

// RingBucket — bir 5 dk'lık kova. S: durum kodu → istek/sn (Q1); Sum:
// yanıt süresi toplam hızı (Q2, sn/sn); P95: saniye (Q3, ayar açıksa).
type RingBucket struct {
	T   int64              `json:"t"` // unix sn, 300'ün katı
	S   map[string]float64 `json:"s,omitempty"`
	Sum *float64           `json:"sum,omitempty"`
	P95 *float64           `json:"p95,omitempty"`
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

// MergeStatus — Q1 serilerini birleştirir; etiket süzgecinden geçmeyen seri atılır.
func (r *Ring) MergeStatus(series []chstore.SpanMetricSeries) {
	for _, s := range series {
		if len(s.GroupKey) != 1 || !statusLabelRe.MatchString(s.GroupKey[0]) {
			continue
		}
		code := s.GroupKey[0]
		for _, p := range s.Points {
			if !finite(p.Value) || p.Value < 0 {
				continue
			}
			b := r.bucket(alignT(p.Time))
			if b.S == nil {
				b.S = map[string]float64{}
			}
			b.S[code] = p.Value
		}
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

// MergeP95 — Q3.
func (r *Ring) MergeP95(series []chstore.SpanMetricSeries) {
	for _, s := range series {
		for _, p := range s.Points {
			if !finite(p.Value) || p.Value < 0 {
				continue
			}
			v := p.Value
			r.bucket(alignT(p.Time)).P95 = &v
		}
	}
}

// DropP95 — ayar kapandıysa halkadaki eski p95'leri siler.
func (r *Ring) DropP95() {
	for i := range r.B {
		r.B[i].P95 = nil
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

func (b RingBucket) total() float64 {
	var t float64
	for _, v := range b.S {
		t += v
	}
	return t
}

// View — halkadan cevaba giren hâl.
type View struct {
	Status  []chstore.SpanMetricSeries
	Avg     *chstore.SpanMetricSeries
	P95     *chstore.SpanMetricSeries
	Summary HTTPSummary
	From    int64 // unix ms
	To      int64 // unix ms
}

// View — halkayı cevaba çevirir. p95 yalnız allowP95 ve veri varsa. Durum
// serileri toplam hıza göre en büyük 8 ile sınırlıdır (kalan kodlar atılır).
// Hiç durum verisi yoksa ok=false (kart gizlenir).
func (r *Ring) View(allowP95 bool) (View, bool) {
	var v View
	if r == nil || len(r.B) == 0 {
		return v, false
	}
	area := map[string]float64{}
	hasStatus := false
	for _, b := range r.B {
		for k, x := range b.S {
			area[k] += x
			hasStatus = true
		}
	}
	if !hasStatus {
		return v, false
	}
	codes := make([]string, 0, len(area))
	for k := range area {
		if statusLabelRe.MatchString(k) { // savunma: halka dışarıdan yüklenmiş olabilir
			codes = append(codes, k)
		}
	}
	sort.Slice(codes, func(i, j int) bool {
		if area[codes[i]] != area[codes[j]] {
			return area[codes[i]] > area[codes[j]]
		}
		return codes[i] < codes[j]
	})
	if len(codes) > ringMaxStatus {
		codes = codes[:ringMaxStatus]
	}
	sort.Strings(codes) // sabit lejant sırası
	for _, c := range codes {
		ser := chstore.SpanMetricSeries{GroupKey: []string{c}}
		for _, b := range r.B {
			if x, ok := b.S[c]; ok {
				ser.Points = append(ser.Points, chstore.SpanMetricPoint{Time: b.T * 1e9, Value: x})
			}
		}
		v.Status = append(v.Status, ser)
	}

	var avg, p95 chstore.SpanMetricSeries
	var peak float64
	var peakAt int64
	var lastTotal float64
	var lastAvg, lastP95 *float64
	for _, b := range r.B {
		tot := b.total()
		lastTotal = tot
		if tot > peak {
			peak, peakAt = tot, b.T
		}
		if b.Sum != nil && tot > 0 {
			a := *b.Sum / tot
			avg.Points = append(avg.Points, chstore.SpanMetricPoint{Time: b.T * 1e9, Value: a})
			lastAvg = &a
		}
		if allowP95 && b.P95 != nil {
			x := *b.P95
			p95.Points = append(p95.Points, chstore.SpanMetricPoint{Time: b.T * 1e9, Value: x})
			lastP95 = &x
		}
	}
	if len(avg.Points) > 0 {
		avg.GroupKey = []string{}
		v.Avg = &avg
	}
	if len(p95.Points) > 0 {
		p95.GroupKey = []string{}
		v.P95 = &p95
	}
	v.Summary = HTTPSummary{NowRPS: lastTotal, PeakRPS: peak, PeakAt: peakAt * 1000, AvgNowS: lastAvg, P95NowS: lastP95}
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
		if b.S != nil {
			nb.S = make(map[string]float64, len(b.S))
			for k, v := range b.S {
				nb.S[k] = v
			}
		}
		if b.Sum != nil {
			v := *b.Sum
			nb.Sum = &v
		}
		if b.P95 != nil {
			v := *b.P95
			nb.P95 = &v
		}
		c.B[i] = nb
	}
	return c
}
