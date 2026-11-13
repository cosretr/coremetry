// batch_latency_active.go — v0.10.1046: "zaten AKTİF olana dokunma"
// yardımcıları (olay tabanlı karar noktaları).
//
// Neden: kapılar yalnız AÇILIŞI kesmeli. Önceden başlamış, hâlâ süren bir
// olay tazelenmeyi bırakırsa terfi Problem'i "anomaly cleared" diye kapanır
// ve sonra YENİ bildirimle yeniden açılır. Bu yüzden kapı, türün o an aktif
// olaylarını muaf tutar.
//
// v0.10.2087 — trace_op_latency dedektörü silindi (operatör kararı
// 2026-11-13); buradaki sınırlı aktif-olay okuma yardımcıları (aktiflik
// penceresi, sayı + bayt tavanı, sürdürme muafiyeti) trace_op / log_pattern
// sürdürmesi ve davranış motoru için yaşar.
//
// "Aktif" (chstore.ListActiveAnomalyKeys):
//
//	trace_op / log_pattern  son yazım ≤ 15 dk önce (opLatActiveAge: /anomalies'in
//	                        10 dk aktifliği + bir 5 dk kova; gerekçe sabitin yorumunda)
//	davranış motoru         son yazım ≤ 10 dk önce (status türetimiyle aynı; olaylar
//	                        her taramada, 2 dk'da bir yeniden yazılır)
//
// OKUMA, SINIRLI ve TEK: tür + aktiflik SQL'de, tavan+1 satır döner,
// max_execution_time 5.
//
//	davranış motoru   dedektör tikinde (2 dk) en çok bir kez, TEMBEL: yalnız en
//	                  az bir batch p99 adayı susturulacakken; WHERE'de p99
//	                  pattern'i (diğer metriklerin olayları tavana sayılmaz).
//	                  Hata ya da tavan aşımı → o tik HİÇBİR aday susturulmaz
//	                  (küme Go'da; tavanın bir SQL zorunluluğu yok, bilinmeyen =
//	                  aktif sayılır).
package anomaly

import (
	"log"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// opLatActiveAge — sürdürme muafiyetinin aktiflik penceresi (ad, ilk
// tüketicisi trace_op_latency'den kaldı; bugün trace_op + log_pattern).
//
// Recorder dakikada bir son TAMAMLANMIŞ 5 dk kovayı değerlendirir. k
// kovasında yazılan olayın last_seen'i ≈ k'nın sonu. k+1 eşiği BİR KEZ
// kaçırırsa (ör. p99 oranı 2.9×'e iner) k+2, k'nın sonundan EN ERKEN 10 dk
// sonra değerlendirilir (k+1 ve k+2 kovalarının kapanması). 10 dk'lık
// aktiflikle çift tam o anda "aktif değil" olur, kapı yük tepesinin geri
// kalanında onu susturur ve terfi Problem'i "anomaly cleared" ile kapanıp
// sonra yeni bildirimle açılır — muafiyetin önlemek için var olduğu şey.
// 15 dk = 10 dk aktiflik + bir 5 dk kova: tek kaçırılmış kova tolere edilir
// (k+2'yi değerlendiren tüm tikler k'nın sonundan 10–15 dk sonradır).
const opLatActiveAge = 15 * time.Minute

// batchLatActiveCap — aktif-olay okumasının tavanı (satır).
const batchLatActiveCap = 200

// opLatExemptMaxBytes — sürdürme muaf listesinin TOPLAM metin tavanı
// (servis + operasyon bayt). Operasyon adları sınırsız (SQL metni span adları
// olabilir); sayı tavanı tek başına sorgu boyunu sınırlamaz.
const opLatExemptMaxBytes = 64 << 10

// batchLatCapKeys — SAF: okuma tavan+1 ister; tavanı aşan kısım kesilir ve
// aşım bildirilir.
func batchLatCapKeys(keys []chstore.ActiveAnomalyKey) ([]chstore.ActiveAnomalyKey, bool) {
	if len(keys) > batchLatActiveCap {
		return keys[:batchLatActiveCap], true
	}
	return keys, false
}

// opLatCapExemptBytes — SAF: muaf listesini toplam servis+operasyon baytıyla
// sınırlar. Girdi en tazeden eskiye sıralı (okumanın ORDER BY'ı); tavanı
// aşacak ilk anahtardan itibaren kesilir — tavan ötesi muaf DEĞİL. Dönen
// dropped, kesilen anahtar sayısı (log için).
func opLatCapExemptBytes(keys []chstore.ActiveAnomalyKey) (kept []chstore.ActiveAnomalyKey, dropped int) {
	total := 0
	for i, k := range keys {
		total += len(k.Service) + len(k.Pattern)
		if total > opLatExemptMaxBytes {
			return keys[:i], len(keys) - i
		}
	}
	return keys, 0
}

// sustainExemptPairs — SAF (v0.10.1085; v0.10.2087'de silinen op_latency.go'dan
// taşındı): sürdürmeden muaf AKTİF çiftler, tekilleştirilmiş, okuma sırasıyla
// (en taze önce; tavan önceden uygulanmış). dwell ≤ 1 (sürdürme yok) ya da
// okuma hatası → boş.
func sustainExemptPairs(dwell int, active []chstore.ActiveAnomalyKey, readErr error) []chstore.OpPair {
	if dwell <= 1 || readErr != nil {
		return nil
	}
	seen := make(map[chstore.OpPair]bool, len(active))
	out := make([]chstore.OpPair, 0, len(active))
	for _, k := range active {
		p := chstore.OpPair{Service: k.Service, Operation: k.Pattern}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// batchLatBehaviorExempt — SAF: davranış motorunun muafiyet yüklemi.
// Okuma hatası ya da tavan aşımı → herkes muaf (susturma yok); aksi hâlde
// servisin p99 davranış olayı (behaviorEventID — kararlı fingerprint)
// aktifse muaf.
func batchLatBehaviorExempt(keys []chstore.ActiveAnomalyKey, readErr error) func(service string) bool {
	if readErr != nil || len(keys) > batchLatActiveCap {
		return func(string) bool { return true }
	}
	ids := make(map[string]bool, len(keys))
	for _, k := range keys {
		ids[k.ID] = true
	}
	return func(service string) bool { return ids[behaviorEventID(service, batchLatencyMetric)] }
}

// batchLatLatch — "durum değişince BİR KEZ logla" mandalı: koşul sürdükçe
// tekrar loglamaz (recorder dakikada bir koşar), koşul kalkınca sıfırlanır.
type batchLatLatch struct{ on atomic.Bool }

func (l *batchLatLatch) report(cond bool, format string, args ...any) {
	if !cond {
		l.on.Store(false)
		return
	}
	if !l.on.Swap(true) {
		log.Printf(format, args...)
	}
}

var (
	behaviorActiveReadLatch batchLatLatch
	behaviorActiveCapLatch  batchLatLatch
	// v0.10.1586 — trace_op / log_pattern sürdürme muafiyetinin aktif-olay okuması.
	traceOpActiveReadLatch    batchLatLatch
	logPatternActiveReadLatch batchLatLatch
)
