package anomaly

// red_vm_store.go — v0.10.1478: VM RED kovalarının lider-belleği + her tikin
// GEREKLİ kova kümesinin SAF tanımı.
//
// NEDEN BELLEK: filo geneli kova histogramı (sum by (svc, le)) her tikte 24 sa
// + 14 gün slot + 28 gün haftanın-saati okumak demek olurdu — milyonlarca seri
// × binlerce örnek. Tamamlanmış bir 5 dk kovası değişmez (okuma vmIngestLag
// sonra yapılır), o yüzden kova BİR KEZ okunur ve gerekli kümede kaldıkça
// bellekte durur. Sabit durumda tik başına yalnız yeni kovalar okunur: ardışık
// pencerenin son kovası, geçmiş günlerin slot kenarı (5 dk'da bir), saat
// dönünce geçmiş haftaların yeni saati. Liderlik değişince yeni lider ısınır
// (ısınana dek servisler span kaynağında — kanıt bunu söyler).
//
// Bellek: kova × aile × tür × servis × 4 B (float32, NaN = seri yok). ~650 kova ×
// 2 aile × 3 tür × 1500 servis ≈ 23 MB üst sınır; servis kimliği tavanı
// vmMaxServices (aşan servis düşer, sayaçta görünür).
//
// Gerekli küme, span okumalarının WHERE yüklemlerinin BİREBİR Go ikizi
// (kaba kuvvetle, 5 dk ızgarada sayılarak — tablo testi SQL yüklemini aynı
// biçimde yeniden yazıp karşılaştırır): ardışık = buildAllBucketsQuery,
// mevsimsel = buildAllSeasonalQuery, davranış = buildBehaviorBucketsQuery.

import (
	"math"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// vmMaxServices — servis kimliği tavanı (bellek sınırı).
const vmMaxServices = 20000

// vmFamFrame — bir kovanın bir ailedeki değerleri; tür başına servis dizini.
type vmFamFrame struct {
	have [vmKindN]bool
	vals [vmKindN][]float32
}

// vmFrame — bir kova (tüm aileler).
type vmFrame struct {
	fam []*vmFamFrame
}

// vmREDStore — kova belleği. Yalnız dedektörün tik goroutine'inden yazılır
// (lider döngüsü tek goroutine); kilit yok.
type vmREDStore struct {
	ids     map[string]int32
	names   []string
	frames  map[int64]*vmFrame
	dropped int // tavan yüzünden düşen servis sayısı (süreç ömrü)
}

func newVMREDStore() *vmREDStore {
	return &vmREDStore{ids: map[string]int32{}, frames: map[int64]*vmFrame{}}
}

// id — servis kimliği (yoksa açar; tavanda -1).
func (s *vmREDStore) id(svc string) int32 {
	if id, ok := s.ids[svc]; ok {
		return id
	}
	if len(s.names) >= vmMaxServices {
		s.dropped++
		return -1
	}
	id := int32(len(s.names))
	s.ids[svc] = id
	s.names = append(s.names, svc)
	return id
}

func (s *vmREDStore) famFrame(bucket int64, fam int, create bool) *vmFamFrame {
	fr := s.frames[bucket]
	if fr == nil {
		if !create {
			return nil
		}
		fr = &vmFrame{fam: make([]*vmFamFrame, len(vmREDFamilies))}
		s.frames[bucket] = fr
	}
	ff := fr.fam[fam]
	if ff == nil && create {
		ff = &vmFamFrame{}
		fr.fam[fam] = ff
	}
	return ff
}

// markHave — sorgulanan kovalar: seri dönmese de "okundu" (servis yok = yok).
func (s *vmREDStore) markHave(fam int, k vmREDKind, buckets []int64) {
	for _, b := range buckets {
		s.famFrame(b, fam, true).have[k] = true
	}
}

// put — tek değer (NaN/Inf yazılmaz = seri yok).
func (s *vmREDStore) put(fam int, k vmREDKind, bucket int64, svc string, v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	id := s.id(svc)
	if id < 0 {
		return
	}
	ff := s.famFrame(bucket, fam, true)
	vals := ff.vals[k]
	for int(id) >= len(vals) {
		vals = append(vals, float32(math.NaN()))
	}
	vals[id] = float32(v)
	ff.vals[k] = vals
}

// get — değer; ok=false: kova okunmadı ya da servisin serisi yok.
func (s *vmREDStore) get(fam int, k vmREDKind, bucket int64, svc string) (float64, bool) {
	id, ok := s.ids[svc]
	if !ok {
		return 0, false
	}
	ff := s.famFrame(bucket, fam, false)
	if ff == nil || int(id) >= len(ff.vals[k]) {
		return 0, false
	}
	v := ff.vals[k][id]
	if math.IsNaN(float64(v)) {
		return 0, false
	}
	return float64(v), true
}

// servicesAt — kovada türün değeri > 0 olan servisler.
func (s *vmREDStore) servicesAt(fam int, k vmREDKind, bucket int64) []string {
	ff := s.famFrame(bucket, fam, false)
	if ff == nil {
		return nil
	}
	var out []string
	for id, v := range ff.vals[k] {
		if !math.IsNaN(float64(v)) && v > 0 && id < len(s.names) {
			out = append(out, s.names[id])
		}
	}
	return out
}

// has — kova bu tür için okundu mu.
func (s *vmREDStore) has(fam int, k vmREDKind, bucket int64) bool {
	ff := s.famFrame(bucket, fam, false)
	return ff != nil && ff.have[k]
}

// missing — gerekli kovalardan bu türde okunmamış olanlar.
func (s *vmREDStore) missing(fam int, k vmREDKind, required []int64) []int64 {
	var out []int64
	for _, b := range required {
		if !s.has(fam, k, b) {
			out = append(out, b)
		}
	}
	return out
}

// retain — gerekli küme dışındaki kovaları atar (bellek sınırı).
func (s *vmREDStore) retain(required map[int64]bool) {
	for b := range s.frames {
		if !required[b] {
			delete(s.frames, b)
		}
	}
}

// flushFamily — ailenin kapsamı (servis/durum/ortam etiketi) değişince tüm
// kovaları geçersizdir: eski kapsamla okunmuş değer yeni kapsamla karışmasın.
func (s *vmREDStore) flushFamily(fam int) {
	for _, fr := range s.frames {
		fr.fam[fam] = nil
	}
}

// ── Gerekli kova kümeleri (SAF) ─────────────────────────────────────────────

// vmBucketsBetween — [from, to) aralığındaki 5 dk ızgara kova başları (from
// yukarı yuvarlanır — SQL `time_bucket >= ?` eşdeğeri).
func vmBucketsBetween(from, to int64) []int64 {
	start := from
	if r := start % vmStepS; r != 0 {
		if start > 0 {
			start += vmStepS - r
		} else {
			start -= r
		}
	}
	var out []int64
	for b := start; b < to; b += vmStepS {
		out = append(out, b)
	}
	return out
}

// vmConsecutiveBuckets — buildAllBucketsQuery'nin WHERE'i:
// time_bucket >= now−24h AND time_bucket < lastCompleteBucketStart(now).
func vmConsecutiveBuckets(now time.Time) []int64 {
	upper := lastCompleteBucketStart(now).Unix()
	return vmBucketsBetween(now.Add(-time.Duration(historyHours)*time.Hour).Unix(), upper)
}

// vmSeasonalBuckets — buildAllSeasonalQuery'nin WHERE'i (seasonalBinds —
// fetchAllSeasonal ile AYNI bağlar): cutoff = at−days, üst = at−(radius+5dk),
// sınıf = dayClass(at), dairesel saniye-of-day uzaklığı ≤ radius. UTC (v0.8.323).
func vmSeasonalBuckets(at time.Time, days, neighborBuckets int) []int64 {
	out, _ := vmSeasonalBucketsCal(at, days, neighborBuckets, chstore.AnomalyCalendarConfig{}, time.UTC)
	return out
}

// vmSeasonalBucketsCal — v0.10.1483: takvimli ikiz. Özel günde (holiday /
// half-day / month-end-like) VM yolu YEDEK sınıfı kullanır (pazar / cumartesi /
// günün sınıfı): önceki özel günlerin örnekleri CH yolunda 90 günden okunur,
// VM deposu yalnız mevsimsel pencereyi tutar. Takvim boşsa vmSeasonalBuckets
// ile bayt bayt aynı küme. Dönen sınıf gün çeşitliliği eşiğinin girdisi.
//
// v0.10.1791 — loc: buildAllSeasonalQueryTZ'nin dilimi (seasonalTimezone);
// time.UTC ile küme eskisiyle bayt bayt aynı.
func vmSeasonalBucketsCal(at time.Time, days, neighborBuckets int, cal chstore.AnomalyCalendarConfig, loc *time.Location) ([]int64, string) {
	loc = seasonalLocOrUTC(loc)
	cutoff, upper, _, target, radius := seasonalBinds(at, days, neighborBuckets, loc)
	class := calendarFallbackClass(dayClassCal(at.In(loc), cal), at.In(loc))
	var out []int64
	for _, b := range vmBucketsBetween(cutoff.Unix(), upper.Unix()) {
		t := time.Unix(b, 0).In(loc)
		if dayClass(t) != class {
			continue
		}
		sod := t.Hour()*3600 + t.Minute()*60
		d := sod - target
		if d < 0 {
			d = -d
		}
		if 86400-d < d {
			d = 86400 - d
		}
		if d <= radius {
			out = append(out, b)
		}
	}
	return out, class
}

// vmBehaviorBuckets — buildBehaviorBucketsQuery'nin WHERE'i (behaviorBinds):
// [upper−28g, upper) içinde haftanın-saati dairesel uzaklığı ≤ 1 YA DA son 3 sa.
func vmBehaviorBuckets(now time.Time) []int64 {
	upper, cutoff, recentFrom, target := behaviorBinds(now)
	var out []int64
	for _, b := range vmBucketsBetween(cutoff.Unix(), upper.Unix()) {
		if b >= recentFrom.Unix() || hourOfWeekDistance(hourOfWeek(time.Unix(b, 0)), target) <= behaviorNeighborHours {
			out = append(out, b)
		}
	}
	return out
}

// vmRequiredSet — üç kümenin birleşimi (retain girdisi).
func vmRequiredSet(sets ...[]int64) map[int64]bool {
	out := map[int64]bool{}
	for _, s := range sets {
		for _, b := range s {
			out[b] = true
		}
	}
	return out
}
