package anomaly

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// TraceOpAnomaly is a per-(service, operation) error or latency
// signal that's either brand new or up sharply over baseline.
// Different from the service-wide metric anomaly detector in
// that it pinpoints the SPECIFIC operation that's misbehaving —
// the SRE's first question after "is service X broken" is "which
// endpoint inside X".
//
// v0.10.1043 — batch uzun-taban dalında (classifyTraceOps) error_spike'ın
// Ratio'su cari PAY / taban PAYI, BaselineErrors taban payının cari çağrı
// sayısına uygulanmışı (pay × cur_calls: "bu koşu her zamanki payında olsaydı
// N hata"; taban payı traceOpBatchExtShare'de). Ratio ≈ CurrentErrors /
// BaselineErrors birim sözleşmesi korunur; seyrek koşan işin zamana bölünmüş
// tabanı ≈0 çıkıp yalan söylerdi. Mutlak artış kaçışı bugünkü new_error'u
// (sayı oranı, BaselineErrors 0) aynen üretir.
type TraceOpAnomaly struct {
	Service        string  `json:"service"`
	Operation      string  `json:"operation"`
	Kind           string  `json:"kind"` // "new_error" | "error_spike"
	CurrentErrors  uint64  `json:"currentErrors"`
	BaselineErrors uint64  `json:"baselineErrors"`
	Ratio          float64 `json:"ratio"` // current / max(baseline, 1)
	// CurrentCalls — the denominator the qualification now insists on
	// (v0.9.327). Shipping it means the row can say "42 errors of 3,100
	// calls" instead of a bare count the operator has to go look up.
	CurrentCalls  uint64  `json:"currentCalls"`
	ErrorShare    float64 `json:"errorShare"`    // CurrentErrors / CurrentCalls, 0..1
	SampleTraceID string  `json:"sampleTraceId"` // representative trace for one-click drill-in
	LastSeenNs    int64   `json:"lastSeenNs"`
}

// traceOpBucket is one (service, operation) pair's cur/base error
// counts as read from the MV — input to the pure classifier.
type traceOpBucket struct {
	Service   string
	Operation string
	CurErrs   uint64
	BaseErrs  uint64 // RAW baseline count over the whole lookback (un-normalized)
	CurCalls  uint64 // total calls in the current window — the denominator
	// BaseCalls — v0.10.1039: taban penceresindeki çağrı sayısı; YALNIZ batch
	// kalıp listesi boş değilken okunur (aynı iç alt sorgudan, ek tarama yok).
	// Liste boşken 0 kalır ve hiçbir dal ona bakmaz.
	BaseCalls uint64
	// ExtErrs / ExtCalls — v0.10.1043: UZUN taban penceresi
	// [şimdi − traceOpBatchExtLookback, 24 sa tabanın başı) toplamları. YALNIZ
	// batch new_error adayları için ve ikinci okuma BAŞARIYLA bitince dolar
	// (readTraceOpBatchExt); okunmadı / okunamadı / tavan dışı = 0 = bugünkü
	// new_error. Sıfır değer "bilinmiyor" ile "uzun tabanda hata yok"u bilerek
	// AYIRMAZ: ikisinde de karar bugünkü gibi new_error.
	ExtErrs  uint64
	ExtCalls uint64
	// Earlier — v0.10.1586 (Davis paritesi FAZ 0.5 / C): sürdürme
	// penceresinin en yeni kovadan ÖNCEKİ kovaları, yeniden eskiye
	// (Earlier[0] = bir önceki tamamlanmış kova). dwell = 1'de boş. Verisiz
	// kova sıfır değer (hata 0 → taban altı → ihlal DEĞİL).
	Earlier []traceOpSlot
	// Active — v0.10.1586: çiftin trace_op olayı zaten AKTİF (son
	// opLatActiveAge içinde yazılmış). Sürdürme yalnız AÇILIŞI yönetir.
	Active bool
}

// traceOpSlot — sürdürme penceresinin tek bir önceki kovası (v0.10.1586).
type traceOpSlot struct {
	Errs  uint64
	Calls uint64
}

// traceOpPair — bir (servis, operasyon) çifti; uzun taban okumasının
// SQL'de birebir kısıtladığı birim.
type traceOpPair struct{ Service, Operation string }

// Qualification thresholds — v0.9.327, operator (prod): "active anomalyler
// daha sıkı kuralları olsun, prodta çok daha az tetiklensin. Anomaly olmasa
// da şu an event oluşuyor."
//
// Measured at the time of the change: the median firing event carried
// current_count = 3-4, minimum 3. That is what the old floor allowed, and at
// prod volume it is not an anomaly — it is arithmetic on small numbers. A
// "12× spike" of three errors against a baseline of a quarter-error is noise
// wearing a big multiplier.
const (
	// traceOpMinErrs — absolute floor. Under a handful of errors in a
	// five-minute window, nothing above is worth an operator's attention at
	// 1000s of services.
	traceOpMinErrs = 10

	// traceOpMinRatio — a real baseline has to be beaten by more than the
	// diurnal wobble. 2× is inside normal day/night variation for most
	// operations; 3× is not.
	traceOpMinRatio = 3.0

	// traceOpMinErrShare — THE missing rule. Both detectors qualified on
	// error COUNT alone and never looked at the denominator, so 3 errors out
	// of 500,000 calls opened an event exactly like 3 errors out of 3.
	//
	// 1% is not a new invention: it is the SAME floor this codebase's metric
	// policy already applies to error_rate ("<%1 = birkaç-hata gürültüsü,
	// açma", anomaly.go metricPolicies absFloor). Extending it here makes the
	// two detectors agree about what counts as an error problem instead of
	// contradicting each other.
	traceOpMinErrShare = 0.01
)

// Batch uzun tabanı — v0.10.1043. Operatör (prod): "Batch'te 'yeni hata'
// gürültüsü: son 24 saatte hiç koşmamış bir iş her koşuda 'yeni hata' diye
// açılıyor."
const (
	// traceOpBatchExtLookback — uzun tabanın ŞİMDİDEN geriye boyu; pencere
	// [hizalı şimdi − bu, 24 sa tabanın başı). 8 gün = haftalık iş + 1 gün
	// kayma payı (geçen haftanın koşusu 7 gün önce, gecikmeli koşu da içeride).
	// operation_summary_5m TTL'i 90 gün (store.go canonicalMVs) ve MV
	// saklama düşürücüsünün (mvRetentionTargets) listesinde yok — 8 gün
	// MV'nin kapsadığı aralığın içinde. Aylık işler KAPSAM DIŞI (her koşu
	// hâlâ new_error; DECISIONS v0.10.1043).
	traceOpBatchExtLookback = 8 * 24 * time.Hour

	// traceOpBatchExtMaxPairs — tik başına uzun tabanı okunan aday çift
	// tavanı (cari hata çoktan aza). Tavan dışı adaylar bugünkü new_error
	// kuralıyla kalır, tik başına BİR satır sayılarla loglanır.
	//
	// Neden 50: sınıflandırıcının çıktı tavanı da 50. Ana sorgu LIMIT 200 ve
	// HAVING Go tabanlarını taşıdığı için LIMIT ısırdığında uzun taban kuralı
	// Go'da en çok 50 satırı susturur, ≥150 aday satır kalır — bu susturma
	// ilk 50'yi boş BIRAKAMAZ (LIMIT sonrası Go süzgecinin "istenen satır
	// pencereye hiç girmedi" sınıfı bu kural yüzünden oluşamaz).
	traceOpBatchExtMaxPairs = 50

	// traceOpBatchExtAbsRise — uzun taban dalının MUTLAK ARTIŞ kaçışı (25
	// yüzde puanı; inceleme bulgusu). Haftalık işte uzun taban payı TEK
	// koşudur: geçen hafta %40 bozuk koşan iş bu hafta %100 bozulsa 3× kuralı
	// 2.5× der ve susardı; taban payı ≥ %33.4 ise hiçbir şey ateşlenemez ve
	// susan kötü koşu gelecek haftanın tabanı olur. Seyrek işte başka emniyet
	// YOK (error_rate metrik/davranış dedektörleri ≥15 dolu kova / ≥3 farklı
	// gün taban ister). cari pay − uzun taban payı ≥ bu → bugünkü new_error
	// (sayı oranıyla; 3×'ün altında bir pay oranlı error_spike terfi
	// tabanını hiç geçemezdi).
	traceOpBatchExtAbsRise = 0.25
)

// classifyTraceOps applies the qualification thresholds and produces
// the sorted anomaly list. Pure — the v0.8.504 MV rewrite extracted it
// from the SQL so the eşik mantığı tablo-testlidir:
//
//   - raw baseline == 0 AND current_errors >= 3 → "new_error"
//   - baseline > 0 AND current >= 2× pencere-normalize baseline → "error_spike"
//
// windowRatio = current window length / baseline lookback; the raw
// baseline is normalized with it so a 12× longer baseline doesn't
// inflate base counts and mask spikes.
//
// v0.10.1039 — isBatch (nil = kural kapalı; üretimde
// AnomalySensitivityConfig.IsBatchService): batch serviste error_spike için
// sayım kuralı (DEĞİŞMEDİ) YETMEZ, hata PAYI da aynı eşikle artmış olmalı
// (traceOpBatchShareHolds) — SAYIM VE PAY. Operatör: "Bazı batch işlerde ani
// yük artışı olabilir, onları anomali gibi düşünme". 20× yükte hata sayısı da
// 20× olur ama pay sabit kalır; sayım kuralı yükün kendisini "hata
// sıçraması" diye raporluyordu.
//
// SAF SUSTURMA (inceleme kararı): pay kuralı sayım kuralının YERİNE geçseydi
// yeni olay da ÜRETİRDİ (cari 5 dk hacmi 24 sa ortalamasının altındayken pay
// artar, sayım artmaz) ve bu batch çifti LIMIT 200 / ilk 50'de batch olmayan
// bir çiftin yerini alabilirdi. VE ile batch olayları eski kümenin ALT
// KÜMESİ; batch olmayan çiftler yalnız yer KAZANABİLİR. Raporlanan ratio ve
// BaselineErrors bugünkü sayım değerleri (UI yanında sayıları yazıyor — tek
// birim). Taban çağrısı 0 (savunma; hata ⊆ çağrı, pratikte olmaz) → pay
// ölçülemez, sayım kararı kalır.
//
// SQL ikizi traceOpQuery'nin HAVING'inde AYNI kural; burası kemer.
//
// v0.10.1043 — batch new_error UZUN TABANA bakar (yalnız Go; veri ikinci,
// aday-kısıtlı okumadan gelir — readTraceOpBatchExt). 24 sa tabanda hiç hata
// yok (base_errs = 0) VE batch VE uzun taban (8 gün) hata görmüş → çift
// "yeni" sayılmadan önce pay kıyaslanır (taban payı = uzun taban hatası /
// (uzun taban çağrısı + 24 sa tabanın TEMİZ çağrısı)):
//   - cari pay − taban payı ≥ 25 puan (traceOpBatchExtAbsRise) → bugünkü
//     new_error (mutlak artış kaçışı; seyrek işte başka emniyet yok);
//   - değilse cari pay ≥ 3 × taban payı → error_spike (oran = pay oranı,
//     BaselineErrors = taban payı × cari çağrı);
//   - değilse olay YOK. Bilinen boşluk: zaten yüksek bir tabandan 25 puanın
//     altında kalan artış (ör. %40 → %60) susar.
//
// Uzun tabanda hata yoksa (ExtErrs 0 — okunmadı, okunamadı, tavan dışı ya
// da gerçekten temiz) new_error AYNEN: hiç hata vermemiş bir batch işinin
// hata vermeye başlaması gerçek sinyal. Batch olmayan servis, nil yüklem
// (kural kapalı) ya da base_errs > 0 → bu dal hiç koşmaz, karar birebir
// bugünkü.
//
// v0.10.1586 (Davis paritesi FAZ 0.5 / C, operatör onayı 2026-10-29) — dwell
// (sürdürme): error_spike VE new_error çift ancak ardışık `dwell` tamamlanmış
// kovanın HER BİRİNDE aynı tabanları (traceOpFloorsHold) ve aynı türün sayım
// kuralını geçerse olaydır (traceOpSustained). Batch uzun-taban dalının
// sonuçları (seyrek koşan iş, v0.10.1043) KAPISIZ — o dal tek koşuya bakar.
// Aktif olay muaf (yalnız açılış; op_latency v0.10.1085 emsali). dwell ≤ 1 =
// bugünkü tek-pencere kararı birebir.
func classifyTraceOps(rows []traceOpBucket, windowRatio float64, isBatch func(service string) bool, dwell int) []TraceOpAnomaly {
	out := []TraceOpAnomaly{}
	for _, r := range rows {
		if !traceOpFloorsHold(r) {
			continue
		}
		basePerWindow := uint64(float64(r.BaseErrs) * windowRatio)
		baseline := basePerWindow
		var kind string
		var ratio float64
		extBranch := false
		switch {
		case r.BaseErrs == 0 && r.ExtErrs > 0 && r.ExtCalls > 0 && isBatch != nil && isBatch(r.Service):
			extBranch = true
			// v0.10.1043 — seyrek koşan batch işi: 24 sa'de hata yok ama uzun
			// taban hata görmüş. Mutlak artış kaçışı → bugünkü new_error;
			// pay 3× → pay oranlı error_spike; değilse sessiz.
			switch verdict, share, expected := traceOpBatchExtShare(r); verdict {
			case traceOpExtNew:
				// v0.10.1587 — taban yok: oran yok (sayım-yalnız).
				kind, ratio = "new_error", chstore.CountOnlyRatio
			case traceOpExtSpike:
				kind, ratio, baseline = "error_spike", share, expected
			}
		case r.BaseErrs == 0:
			// v0.10.1587 (Davis paritesi FAZ 0.5 / B) — taban yok: oran YOK
			// (chstore.CountOnlyRatio). Ham sayıyı oran diye yazmak terfide 20
			// yeni hatayı "20×" → critical P1 yapıyordu; terfi sayım-yalnız
			// olayı ayrı sayım kapısıyla değerlendirir (IsCountOnlyAnomaly).
			kind, ratio = "new_error", chstore.CountOnlyRatio
		case basePerWindow == 0:
			// Baseline var ama pencere-normalize edilince sıfıra
			// yuvarlanıyor (çok seyrek tarihî hata). Eski SQL bu dalda
			// cur/(base*ratio) ile KALİFİYE eder (payda <1 → oran şişer),
			// raporlanan ratio'yu ise cur olarak verirdi — birebir aynı.
			// v0.10.1587 — raporlanan oran eskiden cur'du (sayı); pencereye
			// normalize taban 0 olduğu için dürüst bir oran yok → sayım-yalnız.
			if float64(r.CurErrs)/(float64(r.BaseErrs)*windowRatio) >= traceOpMinRatio {
				kind, ratio = "error_spike", chstore.CountOnlyRatio
			}
		case float64(r.CurErrs)/float64(basePerWindow) >= traceOpMinRatio:
			kind, ratio = "error_spike", float64(r.CurErrs)/float64(basePerWindow)
		}
		if kind == "" {
			continue
		}
		// v0.10.1586 — sürdürme yalnız açılışa (aktif olay tek kovada
		// tazelenir); batch uzun-taban dalı kapısız.
		if dwell > 1 && !extBranch && !r.Active && !traceOpSustained(r, dwell, windowRatio) {
			continue
		}
		// v0.10.1039 — batch: sayım kuralının üstüne EK koşul (yalnız eler).
		// Uzun-taban dalında base_errs = 0 → taban payı 0, bu kapı daima geçer
		// (pay kararı orada uzun taban payıyla verildi).
		if kind == "error_spike" && r.BaseCalls > 0 && isBatch != nil && isBatch(r.Service) && !traceOpBatchShareHolds(r) {
			continue
		}
		out = append(out, TraceOpAnomaly{
			Service:        r.Service,
			Operation:      r.Operation,
			Kind:           kind,
			CurrentErrors:  r.CurErrs,
			CurrentCalls:   r.CurCalls,
			ErrorShare:     float64(r.CurErrs) / float64(r.CurCalls),
			BaselineErrors: baseline,
			Ratio:          ratio,
		})
	}
	// Stable order: new errors first (always more interesting
	// than amplified existing ones), then spikes by ratio desc.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "new_error"
		}
		if out[i].Ratio != out[j].Ratio {
			return out[i].Ratio > out[j].Ratio
		}
		return out[i].CurrentErrors > out[j].CurrentErrors
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

// traceOpSlotQualifies — SAF (v0.10.1586): bir sürdürme kovası, en yeni
// kovanın türüyle aynı kuralı geçiyor mu? Tabanlar (≥ traceOpMinErrs hata,
// ≥ %1 pay) her türde; error_spike'ta ek olarak sayım oranı ≥ 3 × pencereye
// normalize taban — classifyTraceOps'un iki spike dalıyla aynı aritmetik.
// new_error'da taban zaten 0 (pencere tabanın tamamı için geçerli).
func traceOpSlotQualifies(sl traceOpSlot, baseErrs uint64, windowRatio float64) bool {
	if !traceOpFloorsHold(traceOpBucket{CurErrs: sl.Errs, CurCalls: sl.Calls}) {
		return false
	}
	if baseErrs == 0 {
		return true
	}
	basePerWindow := uint64(float64(baseErrs) * windowRatio)
	if basePerWindow == 0 {
		return float64(sl.Errs)/(float64(baseErrs)*windowRatio) >= traceOpMinRatio
	}
	return float64(sl.Errs)/float64(basePerWindow) >= traceOpMinRatio
}

// traceOpSustained — SAF (v0.10.1586): en yeni kovadan önceki dwell−1 kovanın
// HER BİRİ de ihlal ediyor mu? Eksik kova → false.
func traceOpSustained(r traceOpBucket, dwell int, windowRatio float64) bool {
	if len(r.Earlier) < dwell-1 {
		return false
	}
	for i := 0; i < dwell-1; i++ {
		if !traceOpSlotQualifies(r.Earlier[i], r.BaseErrs, windowRatio) {
			return false
		}
	}
	return true
}

// traceOpBatchShareHolds — v0.10.1039, batch serviste sayım kuralına EK
// koşul: cari hata payı ≥ traceOpMinRatio × taban hata payı.
//
//	(cur_errs / cur_calls) >= 3 × (base_errs / base_calls)
//
// PENCERE NORMALİZASYONU YOK ve gerekmiyor: pay, AYNI pencerenin iki
// toplamının oranı (taban 24 saatin hatası / 24 saatin çağrısı); pencere
// uzunluğu pay ile paydada sadeleşir. windowRatio yalnız SAYIM kuralının
// derdi (5 dk sayımı 24 saat sayımıyla kıyaslamak).
//
// İşlem sırası SQL ikiziyle (traceOpQuery HAVING) BİREBİR aynı — iki bölme,
// bir çarpma, aynı IEEE çift — ki eşiğin tam üstündeki bir çift iki tarafta
// farklı yuvarlanmasın. Çağıran CurCalls > 0 (pay tabanı) ve BaseCalls > 0,
// BaseErrs > 0 garantiler.
//
// Bilinen bedel: taban payı zaten ≥ %33 olan bir batch operasyonunda pay
// üçe katlanamaz → error_spike HİÇ açılmaz; tam çöküş error_rate
// dedektörleri ve new_error ile yakalanır (docs/DECISIONS.md v0.10.1039).
func traceOpBatchShareHolds(r traceOpBucket) bool {
	curShare := float64(r.CurErrs) / float64(r.CurCalls)
	baseShare := float64(r.BaseErrs) / float64(r.BaseCalls)
	return curShare >= traceOpMinRatio*baseShare
}

// traceOpFloorsHold — v0.9.327'nin her dala uygulanan iki tabanı (sayım +
// pay), tek gövde: classifyTraceOps ve uzun taban aday seçimi
// (traceOpBatchExtCandidates) aynı "bugün olay olur mu" sorusunu sorar —
// ikinci bir kopya ayrışırdı (eski `>= 3`'ün dallar arasında kayması gibi).
func traceOpFloorsHold(r traceOpBucket) bool {
	// Absolute count floor (cur 0 da burada düşer).
	if r.CurErrs < traceOpMinErrs {
		return false
	}
	// Share floor. A zero denominator means the MV had errors but no call
	// count for the pair — refuse rather than divide: an unknown denominator
	// is not evidence of a high rate.
	return r.CurCalls > 0 && float64(r.CurErrs)/float64(r.CurCalls) >= traceOpMinErrShare
}

// traceOpExtVerdict — uzun taban dalının üç sonucu.
type traceOpExtVerdict int

const (
	traceOpExtSilent traceOpExtVerdict = iota // her zamanki payında: olay yok
	traceOpExtSpike                           // pay 3×: pay oranlı error_spike
	traceOpExtNew                             // mutlak artış kaçışı: bugünkü new_error
)

// traceOpBatchExtShare — v0.10.1043 batch uzun-taban dalının kararı.
//
//	taban payı = ext_errs / (ext_calls + base_calls)
//	cari pay − taban payı ≥ traceOpBatchExtAbsRise → traceOpExtNew
//	cari pay ≥ 3 × taban payı                      → traceOpExtSpike
//	aksi                                            → traceOpExtSilent
//
// PAYDA 24 sa tabanın çağrısını da TAŞIR (inceleme bulgusu): o pencerede
// hata yok (dal koşulu base_errs = 0), yani base_calls dün TEMİZ koşan işin
// çağrılarıdır. Paydaya katılmasa dün 1M temiz çağrı + altı gün önce 10/1.000
// hatalı iş %1'lik tabanla kıyaslanır ve bugünkü %2.5 susardı; katılınca taban
// ≈ %0.001. Operatörün asıl vakasında (iş 24 sa'de hiç koşmadı) base_calls 0,
// karar aynı; payda yalnız BÜYÜR → taban payı yalnız KÜÇÜLÜR → kural yalnız
// daha çok ateşler, yeni susturma ekleyemez.
//
// Kaçış 3×'ten ÖNCE: büyük mutlak sıçrama bugünkü gibi (new_error, sayı
// oranı) yüksek sesle açılır. Seyrek işte bunun dışında emniyet yok (sabitin
// yorumu). Kaçışın KAPSAMADIĞI: zaten yüksek bir tabandan 25 puanın altında
// kalan artış (%40 → %60) — 3× de tutmaz, susar.
//
// 3× karşılaştırmasının işlem sırası traceOpBatchShareHolds ile aynı (iki
// bölme, bir çarpma). Çağıran CurCalls > 0 (tabanlar), ExtErrs > 0 ve
// ExtCalls > 0 garantiler.
//
// share = cari pay / taban payı (Spike'ta raporlanan Ratio — pay oranı, sayı
// DEĞİL); expected = taban payı × cari çağrı (UI'nin "N prev"i: "bu koşu her
// zamanki payında olsaydı N hata olurdu"; Ratio ≈ CurrentErrors /
// BaselineErrors). Zamana bölünmüş taban seyrek işte ≈0 çıkıp yalan söylerdi.
func traceOpBatchExtShare(r traceOpBucket) (traceOpExtVerdict, float64, uint64) {
	curShare := float64(r.CurErrs) / float64(r.CurCalls)
	extShare := float64(r.ExtErrs) / float64(r.ExtCalls+r.BaseCalls)
	switch {
	case curShare-extShare >= traceOpBatchExtAbsRise:
		return traceOpExtNew, 0, 0
	case curShare >= traceOpMinRatio*extShare:
		return traceOpExtSpike, curShare / extShare, uint64(math.Round(extShare * float64(r.CurCalls)))
	}
	return traceOpExtSilent, 0, 0
}

// traceOpBatchExtCandidates — v0.10.1043: uzun taban okumasının adayları =
// BUGÜNKÜ kurala göre new_error olacak batch çiftleri (iki taban geçer, 24 sa
// tabanda hata yok). Sıra cari hata çoktan aza (ana sorgunun ORDER BY'ı;
// eşitlikte ad — determinizm), ilk traceOpBatchExtMaxPairs kapsanır; dönen
// ikinci değer kapsam DIŞI kalan aday sayısı (onlar bugünkü new_error).
// isBatch nil (kural kapalı) → aday yok, okuma yok.
func traceOpBatchExtCandidates(rows []traceOpBucket, isBatch func(string) bool) ([]traceOpPair, int) {
	if isBatch == nil {
		return nil, 0
	}
	var cands []traceOpBucket
	for _, r := range rows {
		if r.BaseErrs == 0 && traceOpFloorsHold(r) && isBatch(r.Service) {
			cands = append(cands, r)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].CurErrs != cands[j].CurErrs {
			return cands[i].CurErrs > cands[j].CurErrs
		}
		if cands[i].Service != cands[j].Service {
			return cands[i].Service < cands[j].Service
		}
		return cands[i].Operation < cands[j].Operation
	})
	uncovered := 0
	if len(cands) > traceOpBatchExtMaxPairs {
		uncovered = len(cands) - traceOpBatchExtMaxPairs
		cands = cands[:traceOpBatchExtMaxPairs]
	}
	pairs := make([]traceOpPair, len(cands))
	for i, c := range cands {
		pairs[i] = traceOpPair{c.Service, c.Operation}
	}
	return pairs, uncovered
}

// traceOpBatchExtWindow — v0.10.1043 uzun tabanın sınırları, YARI AÇIK:
// [hizalı şimdi − traceOpBatchExtLookback, baseStart). Üst sınır ana
// sorgunun taban penceresinin ALT sınırıyla AYNI değer: ana sorgu
// `time_bucket >= baseStart` okur, uzun taban `time_bucket < baseStart` —
// hiçbir kova iki kez sayılmaz, arada kova kalmaz. Taban başı = şimdi − pencere
// − max(24 sa, 12 × pencere); pencere ≥ 2 sa'te bu şimdi − 13 × pencere, yani
// 13 × pencere ≥ 8 g olunca (5 dk hizalı pencere ≥ 14 sa 50 dk; yalnız API'nin
// ?window= parametresi buraya ulaşır) uzun pencere boştur → ok false, okuma
// yok, karar bugünkü.
func traceOpBatchExtWindow(alignedNow, baseStart time.Time) (time.Time, time.Time, bool) {
	extStart := alignedNow.Add(-traceOpBatchExtLookback)
	if !extStart.Before(baseStart) {
		return time.Time{}, time.Time{}, false
	}
	return extStart, baseStart, true
}

// traceOpBatchExtQuery — v0.10.1043 uzun taban okuması. Aynı MV
// (operation_summary_5m), aynı state birleştiricileri; SQL'de İKİ kısıt:
// (a) aday çiftler BİREBİR — `(service_name, name) IN (tuple(?, ?), …)`
// birincil anahtar önekine (service_name, name, time_bucket) oturur, granül
// budar; iki ayrı IN listesinin kartezyen üst-kümesi DEĞİL, Go'da çift
// eşlemesi yok; (b) batch koşulu (BatchServiceSQL, ana sorgunun HAVING'iyle
// AYNI liste) — Go yüklemiyle ayrışma olursa çift sonuçtan düşer = bugünkü
// new_error (güvenli yön).
//
// GROUP BY çift ⊆ aday listesi → satır sayısı ≤ len(pairs) ≤ LIMIT; LIMIT
// hiçbir satırı kesemez. Çağıran pairs'i boş ve batchCond'u "" GÖNDERMEZ
// (aday yoksa ya da liste boşsa okuma kurulmaz).
func traceOpBatchExtQuery(extStart, extEnd time.Time, pairs []traceOpPair, batchCond string, batchArgs []any) (string, []any) {
	tuples := make([]string, len(pairs))
	args := make([]any, 0, 2+2*len(pairs)+len(batchArgs))
	args = append(args, extStart, extEnd)
	for i, p := range pairs {
		tuples[i] = "tuple(?, ?)"
		args = append(args, p.Service, p.Operation)
	}
	args = append(args, batchArgs...)
	return `
		SELECT service_name, name,
		       countIfMerge(error_count_state) AS ext_errs,
		       countMerge(span_count_state)    AS ext_calls
		FROM operation_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?
		  AND (service_name, name) IN (` + strings.Join(tuples, ", ") + `)
		  AND ` + batchCond + `
		GROUP BY service_name, name
		LIMIT ` + strconv.Itoa(traceOpBatchExtMaxPairs) + `
		SETTINGS max_execution_time = 10`, args
}

// traceOpConn — tespitin bağlantıdan kullandığı TEK metot (driver.Conn
// bunu sağlar). Sahte bağlantıyla davranış testi için seam
// (trace_ops_batch_ext_test.go).
type traceOpConn interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
}

// readTraceOpBatchExt — v0.10.1043: batch new_error adayları için uzun
// tabanı okur ve rows'un ExtErrs/ExtCalls alanlarını YERİNDE doldurur.
// Tik başına EN ÇOK BİR MV sorgusu, yalnız en az bir aday varken.
//
// YUMUŞAK-HATA YÖNÜ (aiops §5, SÜZGEÇ kuralı: "süzgeç okunamadıysa
// süzme"): okuma ya da tarama hatası → HİÇBİR alan doldurulmaz (yarım sonuç
// da uygulanmaz), adaylar bugünkü new_error ile kalır, tek satır log. Bu
// kural yalnız SUSTURUR; okunamayan kanıtla susturmak gerçek bir ilk hatayı
// kör ederdi. Tavan aşımı da aynı yön: kapsanmayan adaylar bugünkü
// new_error, tik başına bir satır (yalnız sayılar).
func readTraceOpBatchExt(ctx context.Context, conn traceOpConn, rows []traceOpBucket, isBatch func(string) bool,
	alignedNow, baseStart time.Time, batchCond string, batchArgs []any, logf func(string, ...any)) {
	if isBatch == nil || batchCond == "" {
		return
	}
	pairs, uncovered := traceOpBatchExtCandidates(rows, isBatch)
	if len(pairs) == 0 {
		return
	}
	extStart, extEnd, ok := traceOpBatchExtWindow(alignedNow, baseStart)
	if !ok {
		return
	}
	if uncovered > 0 {
		logf("[trace-op] batch uzun taban: %d aday, tavan %d — %d çift bugünkü new_error kuralıyla kaldı",
			len(pairs)+uncovered, traceOpBatchExtMaxPairs, uncovered)
	}
	q, args := traceOpBatchExtQuery(extStart, extEnd, pairs, batchCond, batchArgs)
	got, err := scanTraceOpBatchExt(ctx, conn, q, args)
	if err != nil {
		logf("[trace-op] batch uzun taban okunamadı — %d aday bugünkü new_error kuralıyla kaldı: %v", len(pairs), err)
		return
	}
	for i := range rows {
		if e, ok := got[traceOpPair{rows[i].Service, rows[i].Operation}]; ok {
			rows[i].ExtErrs, rows[i].ExtCalls = e[0], e[1]
		}
	}
}

// scanTraceOpBatchExt — sorguyu koşar, sonucu YALNIZ tam başarıda döndürür
// (tarama ya da rows.Err hatasında nil + hata: yarım harita uygulanmaz).
func scanTraceOpBatchExt(ctx context.Context, conn traceOpConn, q string, args []any) (map[traceOpPair][2]uint64, error) {
	rs, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	got := map[traceOpPair][2]uint64{}
	for rs.Next() {
		var p traceOpPair
		var errs, calls uint64
		if err := rs.Scan(&p.Service, &p.Operation, &errs, &calls); err != nil {
			return nil, err
		}
		got[p] = [2]uint64{errs, calls}
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	return got, nil
}

// traceOpQuery — tespitin MV sorgusu + argümanları (v0.10.1039'da saf
// kurucuya çıkarıldı ki batch dalı SQL düzeyinde pinlenebilsin).
//
// batchCond == "" (kalıp listesi boş = kural kapalı) → metin ve argümanlar
// v0.10.1039 ÖNCESİYLE BİREBİR aynı (TestTraceOpQueryLegacyIdentity).
//
// batchCond dolu → iki ek: (a) `base_calls` — aynı iç alt sorgunun
// `calls`'ı, is_cur = 0 tarafı; ek tarama yok; (b) HAVING'in spike koluna
// batch servisler için PAY koşulu VE ile eklenir (sayım koşulu aynen).
// Batch koşulu HAVING'de, Go'da DEĞİL: LIMIT 200 cur_errs'e göre sıralıyor ve
// yük altında en çok hata sayan çiftler tam da sabit-paylı batch çiftleri —
// Go'da elenseler LIMIT'i doldurup gerçek sıçramaları dışarıda bırakırlardı
// (v0.9.327'nin dersi: LIMIT yalnız hayatta kalabilecek satırlarda ısırmalı).
// Koşul yalnız ELEDİĞİ için batch olmayan çiftler LIMIT'te yalnız yer kazanır.
func traceOpQuery(curStart, baseStart, alignedNow time.Time, windowRatio float64, batchCond string, batchArgs []any) (string, []any) {
	sel := `
		SELECT service_name, name,
		       sumIf(errs,  is_cur = 1) AS cur_errs,
		       sumIf(errs,  is_cur = 0) AS base_errs,
		       sumIf(calls, is_cur = 1) AS cur_calls`
	spike := `((base_errs = 0) OR (cur_errs >= ? * base_errs * ?))`
	args := []any{curStart, baseStart, alignedNow, traceOpMinErrs, traceOpMinErrShare}
	if batchCond == "" {
		args = append(args, traceOpMinRatio, windowRatio)
	} else {
		sel += `,
		       sumIf(calls, is_cur = 0) AS base_calls`
		// classifyTraceOps ile aynı: taban hatası yok → new_error (dokunulmaz);
		// aksi → bugünkü sayım koşulu VE (batch değil YA DA taban çağrısı 0
		// YA DA pay ≥ 3× taban payı).
		spike = `((base_errs = 0)
		        OR (cur_errs >= ? * base_errs * ?
		            AND (NOT (` + batchCond + ` AND base_calls > 0)
		                 OR cur_errs / cur_calls >= ? * (base_errs / base_calls))))`
		args = append(args, traceOpMinRatio, windowRatio)
		args = append(args, batchArgs...)
		args = append(args, traceOpMinRatio)
	}
	return sel + `
		FROM (
		  SELECT service_name, name,
		         time_bucket >= ? AS is_cur,
		         countIfMerge(error_count_state) AS errs,
		         countMerge(span_count_state)    AS calls
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		  GROUP BY service_name, name, is_cur
		)
		GROUP BY service_name, name
		-- v0.9.327 — the coarse filter now carries the SAME floors the Go
		-- classifier applies. It used to be deliberately looser, which meant
		-- the LIMIT 200 filled with pairs Go would then reject: the ranking
		-- was spent on rows that could never qualify. Same lesson as the
		-- inbox status narrow (v0.9.322) — the LIMIT has to bite on rows
		-- that can actually survive.
		HAVING cur_errs >= ? AND cur_calls > 0
		   AND cur_errs >= ? * cur_calls
		   AND ` + spike + `
		ORDER BY cur_errs DESC
		LIMIT 200
		SETTINGS max_execution_time = 25`, args
}

const traceOpBucketLen = 5 * time.Minute

// traceOpWindows — SAF (v0.10.1586): tespitin zaman sınırları, op_latency
// opLatencyWindows'un ikizi. slotStarts[i] = en yeni (i+1). kovanın başı,
// hepsi TAMAMLANMIŞ; taban sürdürme penceresinin TAMAMINDAN önce biter
// (sıçrayan kovalar kendi tabanını şişirmez). windowRatio = kova / taban boyu
// (taban boyu bugünkü kural: 24 sa ya da 12 × kova). dwell = 1 → bugünkü
// pencereler birebir.
func traceOpWindows(now time.Time, window time.Duration, dwell int) (slotStarts []time.Time, baseStart, alignedNow time.Time, windowRatio float64) {
	if dwell < 1 {
		dwell = 1
	}
	alignedNow = now.Truncate(traceOpBucketLen)
	curBuckets := int(window / traceOpBucketLen)
	if curBuckets < 1 {
		curBuckets = 1
	}
	curWindow := time.Duration(curBuckets) * traceOpBucketLen
	slotStarts = make([]time.Time, dwell)
	for i := range slotStarts {
		slotStarts[i] = alignedNow.Add(-time.Duration(i+1) * curWindow)
	}
	baseLookback := 24 * time.Hour
	if 12*curWindow > baseLookback {
		baseLookback = 12 * curWindow
	}
	baseStart = slotStarts[dwell-1].Add(-baseLookback)
	return slotStarts, baseStart, alignedNow, float64(curWindow) / float64(baseLookback)
}

// traceOpQueryDwell — v0.10.1586: traceOpQuery'nin sürdürmeli hâli. len(slotStarts)
// ≤ 1 → traceOpQuery BİREBİR (TestTraceOpQueryLegacyIdentity korunur). dwell ≥ 2:
// iç alt sorgu kovayı `slot` kolonuna böler (multiIf: 0 = en yeni, i = önceki,
// −1 = taban), dış seviye önceki kova başına prev_errs_<i> / prev_calls_<i>;
// HAVING'e her önceki kova için aynı tabanlar + aynı sayım kuralı VE ile eklenir,
// iki muafiyetle: (a) olayı AKTİF çiftler (tuple IN), (b) batch kalıbı doluyken
// batch çiftinde base_errs = 0 (uzun-taban dalı kapısız, kararı Go verir).
// Koşul yalnız ELER — LIMIT yalnız hayatta kalabilecek satırlarda ısırır.
func traceOpQueryDwell(slotStarts []time.Time, baseStart, alignedNow time.Time, windowRatio float64, batchCond string, batchArgs []any, active []traceOpPair) (string, []any) {
	if len(slotStarts) <= 1 {
		var cur time.Time
		if len(slotStarts) == 1 {
			cur = slotStarts[0]
		}
		return traceOpQuery(cur, baseStart, alignedNow, windowRatio, batchCond, batchArgs)
	}
	sel := `
		SELECT service_name, name,
		       sumIf(errs,  slot = 0)  AS cur_errs,
		       sumIf(errs,  slot = -1) AS base_errs,
		       sumIf(calls, slot = 0)  AS cur_calls`
	if batchCond != "" {
		sel += `,
		       sumIf(calls, slot = -1) AS base_calls`
	}
	slotExpr := "multiIf("
	var slotArgs []any
	for i, st := range slotStarts {
		slotExpr += fmt.Sprintf("time_bucket >= ?, %d, ", i)
		slotArgs = append(slotArgs, st)
		if i > 0 {
			sel += fmt.Sprintf(`,
		       sumIf(errs,  slot = %d) AS prev_errs_%d,
		       sumIf(calls, slot = %d) AS prev_calls_%d`, i, i, i, i)
		}
	}
	slotExpr += "-1)"

	args := append([]any{}, slotArgs...)
	args = append(args, baseStart, alignedNow, traceOpMinErrs, traceOpMinErrShare)
	spike := `((base_errs = 0) OR (cur_errs >= ? * base_errs * ?))`
	if batchCond == "" {
		args = append(args, traceOpMinRatio, windowRatio)
	} else {
		spike = `((base_errs = 0)
		        OR (cur_errs >= ? * base_errs * ?
		            AND (NOT (` + batchCond + ` AND base_calls > 0)
		                 OR cur_errs / cur_calls >= ? * (base_errs / base_calls))))`
		args = append(args, traceOpMinRatio, windowRatio)
		args = append(args, batchArgs...)
		args = append(args, traceOpMinRatio)
	}
	sustain := ""
	for i := 1; i < len(slotStarts); i++ {
		if i > 1 {
			sustain += "\n\t\t        AND "
		}
		sustain += fmt.Sprintf(`(prev_errs_%d >= ? AND prev_calls_%d > 0 AND prev_errs_%d >= ? * prev_calls_%d
		             AND ((base_errs = 0) OR (prev_errs_%d >= ? * base_errs * ?)))`, i, i, i, i, i)
		args = append(args, traceOpMinErrs, traceOpMinErrShare, traceOpMinRatio, windowRatio)
	}
	if batchCond != "" {
		sustain += "\n\t\t        OR (" + batchCond + " AND base_errs = 0)"
		args = append(args, batchArgs...)
	}
	if len(active) > 0 {
		tuples := make([]string, len(active))
		for i, p := range active {
			tuples[i] = "(?, ?)"
			args = append(args, p.Service, p.Operation)
		}
		sustain += "\n\t\t        OR (service_name, name) IN (" + strings.Join(tuples, ", ") + ")"
	}
	return sel + `
		FROM (
		  SELECT service_name, name,
		         ` + slotExpr + ` AS slot,
		         countIfMerge(error_count_state) AS errs,
		         countMerge(span_count_state)    AS calls
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		  GROUP BY service_name, name, slot
		)
		GROUP BY service_name, name
		HAVING cur_errs >= ? AND cur_calls > 0
		   AND cur_errs >= ? * cur_calls
		   AND ` + spike + `
		   AND (` + sustain + `)
		ORDER BY cur_errs DESC
		LIMIT 200
		SETTINGS max_execution_time = 25`, args
}

// DetectTraceOpAnomalies finds per-operation error spikes over
// the last `window` against a longer trailing baseline (1h or
// 12×window, whichever is larger, capped at 24h).
//
// v0.8.504 (perf raporu #1): pre-MV sürüm her koşuda raw spans'i
// İKİ kez tarıyordu (window cur + 1-24h base GROUP BY) — 60s tick'te
// lokalde bile 10-19s/koşu, ~700K satır; 1B span/gün'de dakikada
// milyonlarca satır. Sayımlar artık operation_summary_5m'den okunur
// (MV-first invariant: "raw spans for an aggregate = bug"); raw
// spans'e yalnız KALİFİYE ≤50 çiftin örnek trace'i için dar,
// service_name-prefix'li ikinci sorgu gider. Bedel: pencereler 5m
// bucket'a hizalanır — tespit en fazla ~5dk gecikir (v0.8.315/316'da
// kabul edilmiş desen).
//
// The asymmetric baseline (window vs ≥1h trailing) keeps fresh
// spikes visible for ~1 hour — a window-vs-window comparison would
// have the spike fall into baseline within minutes, flickering the
// anomaly section as windows slide.
//
// v0.10.1586 — bu yol (/api/anomalies/trace-ops canlı şeridi) TEK pencere;
// olay yazan recorder DetectTraceOpAnomaliesSustained'i çağırır.
func DetectTraceOpAnomalies(ctx context.Context, store *chstore.Store, window time.Duration) ([]TraceOpAnomaly, error) {
	// v0.10.1039 — batch kalıpları: metrik dedektörünün okuduğu AYNI atomic
	// ayar (tik başına CH okuması YOK; boot hidrasyonu + 30 sn yenileme).
	// Bu işlev hem recorder'dan hem /api/anomalies/trace-ops'tan çağrılıyor;
	// ikisi de aynı Store'u taşıdığı için seam burası. Liste boşsa (kural
	// kapalı) batchCond "" ve sorgu bugünküyle birebir. ForDetectors: ayar
	// hiç doğrulanmadıysa liste boş (kural devre dışı, tahminle susturma yok).
	sens := store.AnomalySensitivityForDetectors()
	return detectTraceOps(ctx, store.TelemetryReadConn(), sens, time.Now(), window, 1, nil, log.Printf)
}

// DetectTraceOpAnomaliesSustained — v0.10.1586 (Davis paritesi FAZ 0.5 / C,
// operatör onayı 2026-10-29): recorder'ın olay yazan yolu. Sürdürme
// anomaly_sensitivity.traceOpDwellBuckets (vars. 2) ardışık kova; recorder 1
// dk'da bir 5 dk pencereye bakar ve tek kovalık sıçrama 5 tik yeniden
// yazılıp terfinin MinSustained'ini geçiyordu (FP sınıfı d_single_window).
// dwell ≥ 2'de sorgudan ÖNCE tek sınırlı aktif-olay okuması (op_latency'nin
// aynı ListActiveAnomalyKeys + tavanları): o çiftler sürdürmeden muaf.
// Okuma hatası → muafiyet YOK (aktif çift de iki kova ister — sessiz yön),
// geçişte bir kez log.
func DetectTraceOpAnomaliesSustained(ctx context.Context, store *chstore.Store, window time.Duration) ([]TraceOpAnomaly, error) {
	sens := store.AnomalySensitivityForDetectors()
	dwell := sens.TraceOpDwell()
	var active []traceOpPair
	if dwell > 1 {
		keys, err := store.ListActiveAnomalyKeys(ctx, traceOpKind, "", opLatActiveAge, "", nil, batchLatActiveCap+1)
		traceOpActiveReadLatch.report(err != nil,
			"[anomaly-recorder] trace ops: aktif olay okunamadı (%v) — sürdürme muafiyeti bu okuma düzelene dek UYGULANMIYOR", err)
		capped, _ := batchLatCapKeys(keys)
		capped, _ = opLatCapExemptBytes(capped)
		for _, p := range opLatSustainExempt(dwell, capped, err) {
			active = append(active, traceOpPair(p))
		}
	}
	return detectTraceOps(ctx, store.TelemetryReadConn(), sens, time.Now(), window, dwell, active, log.Printf)
}

// traceOpKind — anomaly_events.kind (recorder yazımı + aktif-olay okuması).
const traceOpKind = "trace_op"

// detectTraceOps — DetectTraceOpAnomalies'in gövdesi. v0.10.1043'te
// bağlantı, ayar, saat ve log enjekte edilebilir oldu: batch uzun tabanının
// "okuma hatası → bugünkü new_error, tek log" ve "tavan aşımı" yönleri sahte
// bağlantıyla DAVRANIŞ olarak testli (trace_ops_batch_ext_test.go).
//
// v0.10.1586 — dwell + active (sürdürme; dwell ≤ 1 → bugünkü tek pencere
// birebir, active yok sayılır).
func detectTraceOps(ctx context.Context, conn traceOpConn, sens chstore.AnomalySensitivityConfig,
	now time.Time, window time.Duration, dwell int, active []traceOpPair, logf func(string, ...any)) ([]TraceOpAnomaly, error) {
	// Tam-bucket hizası: MV bucket'ı kapanmadan sayımı eksiktir.
	if dwell < 1 {
		dwell = 1
	}
	slotStarts, baseStart, alignedNow, windowRatio := traceOpWindows(now, window, dwell)
	curStart := slotStarts[0]

	// v0.9.334 — taban penceresi 1 saat → 24 saat.
	//
	// Operatör (prod, v0.9.327'den SONRA): "Prodta hâlâ çok anomali var."
	//
	// Sertleştirilen eşikler `base_errs > 0` dalını kısıyordu ama "new_error"
	// dalını değil: taban penceresinde hiç hata yoksa oran şartı HİÇ
	// uygulanmıyor. 1 saatlik pencereyle, iki saatte bir tekrarlayan bir hata
	// HER SEFERİNDE "yeni" sayılıyor — kalıcı bir olay üreteci. 24 saatte
	// "yeni", gerçekten "bir gündür görülmedi" demek.
	//
	// Normalizasyon bunu ücretsiz kılıyor: base 24 saatlik toplam olduğu için
	// windowRatio da 24× küçülüyor, yani basePerWindow (dolayısıyla spike
	// eşiği) değişmiyor. Değişen tek şey "taban sıfır" iddiasının ne kadar
	// zor olduğu.
	//
	// Maliyet ölçüldü (yerel, operation_summary_5m): 1h 0.050s / 24h 0.058s.
	// MV önceden toplandığı için genişleme çıktı kardinalitesini değil yalnız
	// birleştirilen bucket sayısını artırıyor.
	// (Pencereler v0.10.1586'dan beri traceOpWindows'ta — dwell = 1'de birebir.)

	batchCond, batchArgs := sens.BatchServiceSQL("service_name")
	activeSet := make(map[traceOpPair]bool, len(active))
	if dwell > 1 {
		for _, p := range active {
			activeSet[p] = true
		}
	} else {
		active = nil
	}

	// Tek MV geçişi: iç seviye (pair, is_cur) bazında state merge, dış
	// seviye cur/base'i yan yana koyar. Kaba eleme SQL'de kalır ki
	// LIMIT anlamlı olsun (eşiğin gevşek hâli); kesin eşik/kind
	// sınıflaması Go'da (classifyTraceOps, tablo-testli).
	q, args := traceOpQueryDwell(slotStarts, baseStart, alignedNow, windowRatio, batchCond, batchArgs, active)
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	buckets := []traceOpBucket{}
	for rows.Next() {
		var b traceOpBucket
		dest := []any{&b.Service, &b.Operation, &b.CurErrs, &b.BaseErrs, &b.CurCalls}
		if batchCond != "" {
			dest = append(dest, &b.BaseCalls)
		}
		if dwell > 1 {
			b.Earlier = make([]traceOpSlot, dwell-1)
			for i := range b.Earlier {
				dest = append(dest, &b.Earlier[i].Errs, &b.Earlier[i].Calls)
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		b.Active = activeSet[traceOpPair{Service: b.Service, Operation: b.Operation}]
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var isBatch func(string) bool
	if batchCond != "" {
		isBatch = sens.IsBatchService
	}
	// v0.10.1043 — seyrek koşan batch işi: 24 sa tabanda hiç hata yoksa
	// new_error demeden önce 8 günlük uzun tabana bak. İkinci MV okuması
	// YALNIZ en az bir batch new_error adayı varken, aday çiftlerle ve batch
	// koşuluyla kısıtlı; hata → bugünkü new_error + tek log. Liste boşken
	// (isBatch nil) hiç kurulmaz.
	readTraceOpBatchExt(ctx, conn, buckets, isBatch, alignedNow, baseStart, batchCond, batchArgs, logf)
	out := classifyTraceOps(buckets, windowRatio, isBatch, dwell)
	if len(out) == 0 {
		return out, nil
	}

	// İkinci, DAR raw-spans sorgusu: yalnız kalifiye çiftlerin örnek
	// trace'i + son görülme anı. service_name PK-prefix + zaman
	// sınırı + LIMIT — 1B span/gün'de bile küçük bir dilim. İki ayrı
	// IN listesi kesişimin ÜST-kümesini tarar; kesin çift eşlemesi
	// aşağıdaki map'te — fazla gruplar sadece atlanır. LIMIT = 50×50
	// kartezyen tavanı, yani LIMIT hiçbir grubu kesemez. (Demet-IN bind
	// emsali VAR — chstore/topology.go, rollout_services.go ve yukarıdaki
	// traceOpBatchExtQuery; bu sorgu dokunulmadan iki IN listesiyle kaldı.)
	svcs := make([]string, 0, len(out))
	ops := make([]string, 0, len(out))
	for _, a := range out {
		svcs = append(svcs, a.Service)
		ops = append(ops, a.Operation)
	}
	srows, err := conn.Query(ctx, `
		SELECT service_name, name,
		       argMax(trace_id, time)           AS sample,
		       toUnixTimestamp64Nano(max(time)) AS last_ns
		FROM spans
		WHERE time >= ? AND time < ?
		  AND status_code = 'error'
		  AND service_name IN ?
		  AND name IN ?
		GROUP BY service_name, name
		LIMIT 2500
		SETTINGS max_execution_time = 10`,
		curStart, alignedNow, svcs, ops,
	)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	type sampleKey struct{ svc, op string }
	type sampleVal struct {
		trace  string
		lastNs int64
	}
	samples := map[sampleKey]sampleVal{}
	for srows.Next() {
		var svc, op, trace string
		var lastNs int64
		if err := srows.Scan(&svc, &op, &trace, &lastNs); err != nil {
			return nil, err
		}
		samples[sampleKey{svc, op}] = sampleVal{trace, lastNs}
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if s, ok := samples[sampleKey{out[i].Service, out[i].Operation}]; ok {
			out[i].SampleTraceID = s.trace
			out[i].LastSeenNs = s.lastNs
		}
	}
	return out, nil
}
