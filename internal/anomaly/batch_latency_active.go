// batch_latency_active.go — v0.10.1046: batch gecikme kapısının "zaten
// AKTİF olana dokunma" yarısı (olay tabanlı iki karar noktası).
//
// Neden: kapı yalnız AÇILIŞI kesmeli. Yük sıçramasından ÖNCE başlamış, yükle
// ilgisi olmayan bir gecikme gerilemesi (olay aktif, belki Problem'e terfi
// etmiş) yük gelince tazelenmeyi bırakırsa terfi Problem'i ~10 dk sonra
// "anomaly cleared" diye kapanır — p99 hâlâ 10× iken — ve tepe geçince YENİ
// bildirimle yeniden açılır. Bu yüzden kapı, türün o an aktif olaylarını muaf
// tutar.
//
// "Aktif" (chstore.ListActiveAnomalyKeys):
//
//	trace_op_latency  son yazım ≤ 15 dk önce (opLatActiveAge: /anomalies'in 10 dk
//	                  aktifliği + bir 5 dk kova; gerekçe sabitin yorumunda)
//	davranış motoru   son yazım ≤ 10 dk önce (status türetimiyle aynı; olaylar
//	                  her taramada, 2 dk'da bir yeniden yazılır)
//
// OKUMA, SINIRLI ve TEK: tür + aktiflik + batch servis koşulu SQL'de, tavan+1
// satır döner, max_execution_time 5.
//
//	trace_op_latency  recorder tikinde (1 dk) bir kez, sorgudan ÖNCE, yalnız
//	                  batch kalıp listesi doluysa. Hata → kapı o tik HİÇ yok.
//	                  Tavan (200 çift ya da 64 KiB servis+operasyon metni)
//	                  aşılırsa en taze olanlar muaf, ötesi muaf DEĞİL (SQL bind
//	                  listesi sınırlı olmak zorunda) — geçişte bir kez loglanır.
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

// opLatActiveAge — trace_op_latency muafiyetinin aktiflik penceresi.
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

// batchLatActiveCap — aktif-olay okumasının tavanı (satır). trace_op_latency
// tarafında SQL'e (servis, operasyon) çifti olarak bind edilir: 200 çift =
// 400 argüman; tespit sorgusunun LIMIT 200'üyle aynı mertebe.
const batchLatActiveCap = 200

// opLatExemptMaxBytes — trace_op_latency muaf listesinin TOPLAM metin tavanı
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
	opLatActiveReadLatch    batchLatLatch
	opLatActiveCapLatch     batchLatLatch
	opLatActiveBytesLatch   batchLatLatch
	behaviorActiveReadLatch batchLatLatch
	behaviorActiveCapLatch  batchLatLatch
)
