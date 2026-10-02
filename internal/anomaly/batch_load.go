// batch_load.go — v0.10.1039: batch servislerde yük artışı anomali değil.
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// Bu dosya metrik dedektörünün ve davranış motorunun ORTAK kapısını ve
// metrik dedektörünün açık-satır kapatma geçişini taşır. Yüklem tek:
// chstore.AnomalySensitivityConfig.IsBatchService (tik başına bir kez
// okunan atomic ayardan; CH okuması YOK).
//
// NE SUSAR: yalnız YÜKÜN KENDİSİ — request_rate (iki yön de: batch işinin
// başlaması da bitmesi de olay değil). error_rate ve p99_ms bu servislerde
// de AYNEN değerlendirilir; servis kümelemeden de ÇIKMAZ (diğer
// metrikleriyle aday olmaya devam eder).
package anomaly

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// batchLoadMetric — batch servislerde susturulan TEK metrik. Liste değil
// sabit: kapsamı genişletmek (error_rate, p99_ms) gerçek bir hatayı
// susturmak olurdu — bilinçli olarak tek dizgi, tek karşılaştırma.
const batchLoadMetric = "request_rate"

// batchLoadResolveNote — açık request_rate problemini kapatan geçişin
// DÜRÜST gerekçesi. Bayat süpürmeye bırakılsaydı satır "source silent"
// ekiyle kapanırdı — servis susmadı, kural değişti.
const batchLoadResolveNote = "Resolved: batch servis — yük sinyali anomali sayılmaz (Settings → Anomaly → Batch servis ad kalıpları, v0.10.1039)."

// batchLoadSkipped — SAF kapı: bu (servis, metrik) çifti yük sinyali
// olarak batch kuralına mı takılıyor? Davranış motoru ve metrik
// dedektörü AYNI işlevi çağırır.
func batchLoadSkipped(cfg chstore.AnomalySensitivityConfig, service, metric string) bool {
	return metric == batchLoadMetric && cfg.IsBatchService(service)
}

// batchLoadResolutions — SAF: kapatılacak açık metrik-dedektörü
// request_rate problemleri, kapanmış KOPYALARIYLA (snapshot'taki satır
// değiştirilmez — snapshot 5 sn'lik memo, başka okuyucular da görüyor).
//
// Seçim dar ve kesin: RuleID tam olarak "anomaly:<svc>:request_rate",
// Kind external DEĞİL (dış seri hattı aynı önek biçimini kullanıyor ve
// öznesi gerçek bir servis adı olabilir — o hat bu kuralın dışında),
// servis batch. Durum open ya da acknowledged (snapshot ikisini de
// taşıyor; acknowledged satırı da bayat süpürmeye bırakmak yanlış
// gerekçeyle kapanması demek).
func batchLoadResolutions(all []*chstore.Problem, cfg chstore.AnomalySensitivityConfig, nowNs int64) []chstore.Problem {
	var out []chstore.Problem
	for _, p := range all {
		if p == nil || p.ID == "" || p.Kind == chstore.ProblemKindExternal {
			continue
		}
		if p.Status != "open" && p.Status != "acknowledged" {
			continue
		}
		if p.RuleID != "anomaly:"+p.Service+":"+batchLoadMetric || !cfg.IsBatchService(p.Service) {
			continue
		}
		q := *p
		if !strings.Contains(q.Description, batchLoadResolveNote) {
			q.Description = strings.TrimRight(q.Description, " ") + " " + batchLoadResolveNote
		}
		chstore.MarkResolved(&q, nowNs)
		out = append(out, q)
	}
	return out
}

// resolveBatchLoadProblems — batch servislerin AÇIK request_rate
// problemlerini açık gerekçeyle kapatır (resolveSilentProblems'in
// modeli). Faz 1 bu çiftleri artık hiç değerlendirmediği için bu geçiş
// olmasa satırlar bayat süpürmeyle "source silent" diye kapanırdı.
//
// Dönen küme: bu tikte kapatılan satırların `rule|service` anahtarları —
// scan() bunları kümeleme adaylığından (recentOpenCandidates) düşürür,
// yoksa aynı tikte bir kümeye "merged into" ile katılabilirlerdi.
//
// Yumuşak-hata yönü: snapshot okunamadıysa (nil) HİÇBİR ŞEY kapatılmaz.
// Upsert hatası loglanır; satır açık kalır ve bir sonraki tik yeniden
// dener (anahtar yine kümelemeden düşer — bu tik aday değil).
//
// İKİNCİ KAPI (v0.10.1039 inceleme): ayar bu süreçte en az bir kez BAŞARIYLA
// okunmadıysa (ya da PUT ile gelmediyse) HİÇBİR ŞEY kapatılmaz. Varsayılan
// liste bir tahmin; operatörün `[]` ya da başka kalıplar kaydettiği bir
// kurulumda tahminle kapatmak, doğrulanınca YENİ problem + yeni bildirim
// demek. (scan zaten AnomalySensitivityForDetectors okuyor ve doğrulanmamış
// ayarda liste boş; bu kontrol çağırandan bağımsız kemer.)
func (d *Detector) resolveBatchLoadProblems(ctx context.Context, openSnap *chstore.OpenProblems, cfg chstore.AnomalySensitivityConfig) map[string]bool {
	if !d.store.AnomalySensitivityConfirmed() {
		return nil
	}
	resolved := batchLoadResolutions(openSnap.All(), cfg, time.Now().UnixNano())
	for _, q := range resolved {
		if err := d.store.UpsertProblem(ctx, q); err != nil {
			log.Printf("[anomaly] resolve %s (batch servis): %v", q.RuleID, err)
			continue
		}
		log.Printf("[anomaly] RESOLVED %s · request_rate (batch servis — yük sinyali anomali sayılmaz)", q.Service)
	}
	return batchLoadResolvingKeys(resolved)
}

// batchLoadResolvingKeys — SAF: scan()'in `resolving` kümesiyle AYNI
// anahtar biçimi (`rule|service`; recentOpenCandidates bunu arar).
func batchLoadResolvingKeys(resolved []chstore.Problem) map[string]bool {
	keys := make(map[string]bool, len(resolved))
	for _, q := range resolved {
		keys[q.RuleID+"|"+q.Service] = true
	}
	return keys
}
