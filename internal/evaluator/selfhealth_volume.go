// selfhealth_volume.go — v0.9.1294: self-health ailesinin BEŞİNCİ kuralı,
// hacim sıçraması (Dynatrace-parite EK A / A1'in (d) maddesi).
//
// v0.9.1279 bu maddeyi BİLEREK ertelemişti ve gerekçesi selfhealth.go'nun
// başlığında duruyor: "sıçrama tespiti baseline/mevsimsellik gerektiriyor
// — davranış motorunun işi". O gerekçe, sıçramayı LATENCY/HATA sinyali
// gibi okuduğu için doğruydu. Buradaki soru başka: hacim sıçraması bir
// PERFORMANS anomalisi değil, bir MALİYET olayı. "Dün bu servis 210 bin
// span yazıyordu, bugün 1,2 milyon" cümlesi mevsimsellik modeli
// gerektirmez — iki sayı ve bir oran gerektirir, ve yanlış olma riski
// modelin değil TABANIN meselesidir (aşağıya bak).
//
// v0.10.2118 (operatör kararı 2026-11-13): ÇIKTI Problem DEĞİL ANOMALİ —
// ml_events satırı (detector=svc_volume_spike), bildirim YOK. Aşağıdaki
// yaşam döngüsü aynı kaldı, yalnız hedef değişti (planVolumeEvents); (3)
// maddesindeki şiddet/eskalasyon gerekçesi yalnız eski açık satırlar
// kapanana dek geçerli.
//
// KURALIN YAŞAM DÖNGÜSÜ İCAT EDİLMEDİ: dört kardeşiyle birebir aynı hat
// (leader-locked tik → deterministik id → tik başındaki OpenProblems
// snapshot'ıyla dedup → UpsertProblem → açılışta notify → koşul geçince
// auto-resolve). Bu dosyanın kendine ait ÜÇ kararı var:
//
//	(1) TABAN GÜRÜLTÜ KAPISI. Küçük serviste 3 span → 30 span 10×'tir ve
//	    hiçbir şey ifade etmez. Kapı olmadan bu kural yanlış-pozitif
//	    fabrikasıdır — ailenin en büyük riski tam olarak bu, çünkü bu
//	    alarmlar operatörün "sisteme güvenebilir miyim" sorusunun cevabı.
//	(2) YENİ SERVİS ≠ SIÇRAMA. Önceki pencerede hiç veri yoksa alarm YOK.
//	    self-ingest-stall'un "önce vardı" şartının simetrik ikizi: orada
//	    yokluk "durdu" iddiasını kuramaz, burada "sıçradı" iddiasını.
//	    Yerel ölçümde bu kapının bedeli somut: portfolio-service 24 saatte
//	    96.146 span / önceki 24 saatte 0 → kapı olmasa 96.146× ile açılan
//	    bir alarm, oysa servis dün henüz yoktu.
//	(3) P1 OLAMAZ. Severity DAİMA warning. Bu bir maliyet uyarısı, kesinti
//	    değil; computePriority'de warning dalı P1 üretmez (en fazla, büyük
//	    ihlalde P2). Yaş-eskalasyonu da bu kuralda KAPALI —
//	    escalationExempt, gerekçesi orada.
package evaluator

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	selfVolumeRuleID = "self-volume-spike"

	// selfVolumeWindow — kıyaslanan iki pencerenin uzunluğu. 24 saat,
	// çünkü gün içi eğri (gece/gündüz) tam bir devir yaptığında sıçrama
	// mevsimsellikten ayrışır: 1 saatlik pencerelerde sabah trafiği her
	// gün "4× sıçrama" olurdu.
	selfVolumeWindow = 24 * time.Hour

	// selfVolumeScanEvery — ÖLÇÜM sıklığı. 24 saatlik iki pencereyi 60
	// saniyede bir yeniden hesaplamak, bir dakikada %0,07 değişebilen bir
	// sayı için tik başına bir MV taraması demekti. Saatte bir yeter.
	//
	// DİKKAT — seyreltilen ÖLÇÜM, yaşam döngüsü DEĞİL: iki tik arasında
	// son taramanın sonucu bellekten yeniden sunulur. Aksi hâlde
	// sweepStaleProblems (3 × tik = 3 dakika tazelenmeyen açık problemi
	// "kaynak sustu" diye kapatır) bu kuralın satırlarını üç dakikada bir
	// kapatır, saat başı yeniden açardı: her saat bir bildirim, her saat
	// sıfırlanan StartedAt.
	selfVolumeScanEvery = time.Hour

	// selfVolumeMaxServices — tik başına aday satır tavanı. Sorgu zaten
	// hacim tabanının ÜSTÜNDEKİLERİ ciro sırasına diziyor; bu, binlerce
	// servisli bir kurulumda dönen satırın sınırı.
	selfVolumeMaxServices = 500
)

// ── Saf karar çekirdeği (tablo testli — selfhealth_volume_test.go) ──────

// volumeSpikeDecision — bir servisin telemetri hacmi SIÇRADI mı?
//
// Dört dal, üçü ayrı bir yanlış-pozitif sınıfını kapatıyor:
//
//  1. factor <= 0 → kural KAPALI. (patchSelfHealth normalde 0'ı
//     varsayılana çeviriyor; bu, saf fonksiyonun kendi güvenliği —
//     spoolBreachDecision'ın sıfır-eşik dalıyla aynı duruş.)
//  2. cur < minSpans → TABAN. Maliyet, ŞU AN gelenle orantılıdır: güncel
//     pencere mutlak olarak küçükse oran ne olursa olsun ne disk dolar
//     ne kardinalite patlar. Bu satırı silen mutasyon, küçük-servis
//     vakasını kızartır.
//  3. prev == 0 → YENİ SERVİS. Oran tanımsız (sonsuz); "sıçradı" demek
//     için önce bir "önce" gerekir.
//  4. Asıl karar: cur >= prev × factor. `>=` bilinçli — eşiğin TAM
//     üstünde durmak ihlaldir (aile geneli: spoolBreachDecision,
//     channelBrokenDecision).
func volumeSpikeDecision(cur, prev, minSpans uint64, factor float64) bool {
	if factor <= 0 {
		return false
	}
	if cur < minSpans {
		return false
	}
	if prev == 0 {
		return false
	}
	return float64(cur) >= float64(prev)*factor
}

// escalationExempt — bu kural YAŞLANMAYLA ŞİDDET KAZANMAZ.
//
// Neden gerekli: yaş-eskalasyonu (varsayılan açık) 30 dakika açık kalan
// bir warning'i critical'a çıkarıyor, computePriority ise 4 saati geçen
// critical'ı P1'e. Hacim sıçraması TANIMI GEREĞİ en az 24 saat açık kalır
// (pencereler ancak bir gün sonra eşitlenir), yani eskalasyon bu kuralda
// bir olasılık değil KESİNLİK olurdu: her sıçrama birkaç saat içinde P1'e
// tırmanır ve "maliyet uyarısı" bir kesinti alarmının yerini kapardı.
// Kardeş dört kural bu tuzağa düşmüyor çünkü hepsi zaten critical
// doğuyor ve nextSeverity critical'ı yükseltmiyor.
//
// İki yerde okunuyor — reconcileSelfHealth'in tazeleme dalı ve
// escalateStaleProblems süpürmesi. Biri unutulursa diğeri tek başına
// yetmez: süpürme satırı yükseltir, tazeleme geri indirir ve v0.8.309'un
// bildirim seli aynen geri gelir.
//
// v0.10.800 (dış skill denetimi 2026-09-19 S1, Honeycomb slos-and-triggers;
// operatör onayı) — SLO burn-rate problemleri (`slo:<id>:<severity>`) de
// muaf: şiddetleri politikadan gelir (critical 1 sa/6 sa, warning 6 sa/24 sa
// — chstore.BurnPolicies), yaşla ARTMAZ. Yavaş burn tanımı gereği saatlerce
// açık kalır; merdiven onu 30 dk'da critical'a, 4 sa'de P1'e taşıyor ve
// "trend" alarmı her zaman pager'a düşüyordu (prod ekranı: warning
// politikası CRITICAL rozetiyle). Tazeleme dalı (slo_burn.go) da şiddeti
// politikaya sabitler — iki kelepçe.
func escalationExempt(ruleID string) bool {
	return ruleID == selfVolumeRuleID || strings.HasPrefix(ruleID, "slo:")
}

// dropBatchVolumeRows — v0.10.1039, SAF: batch servislerin hacim sıçraması
// satırlarını düşürür. Operatör: "Bazı batch işlerde ani yük artışı
// olabilir, onları anomali gibi düşünme — özellikle `-batch` geçen servis
// isimlerinde." Bir batch işinin hacmi tanımı gereği dalgalı; dünle
// kıyaslanan 4× bu servislerde olağan çalışma.
//
// YENİ dilim döner, girdiyi DEĞİŞTİRMEZ: girdi volCache'in kendisi; yerinde
// süzme (rows[:0]) önbelleği bozar ve kural tekrar açıldığında (liste
// boşaltılınca) batch satırı bir saat boyunca geri gelmezdi.
func dropBatchVolumeRows(rows []chstore.ServiceVolume, sens chstore.AnomalySensitivityConfig) []chstore.ServiceVolume {
	out := make([]chstore.ServiceVolume, 0, len(rows))
	for _, r := range rows {
		if sens.IsBatchService(r.Service) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ── Tik ─────────────────────────────────────────────────────────────────

// selfVolumeSpike — evaluateSelfHealth geçişi: bu tikte sıçramış servisler.
//
// Dönen ikinci değer "bu kural bu tikte KAPSANDI mı": yalnız kapsanan
// tikte epizot kapatılabilir (v0.9.984 fail-open dersi). Ölçemediğimiz bir
// tikte hüküm vermiyoruz — ama son geçerli ölçümü sunmaya devam ediyoruz,
// çünkü "ölçemedim" ne "temiz" ne de "sustu" demektir.
//
// v0.10.1039 — batch süzgeci ÖNBELLEKTEN SONRA, her tikte: önbellek ham
// ölçümü (saatlik) tutar, süzgeç atomic ayardan (CH okuması yok) tik
// başına uygulanır. Böylece ayar değişikliği saatlik yenilemeyi beklemeden
// bir sonraki tikte etkili olur; kural kapsandığı (ok=true) için açık batch
// epizotları reconcileVolumeEvents'in normal kapatma dalıyla kapanır.
func (e *Evaluator) selfVolumeSpike(ctx context.Context, cfg chstore.SelfHealthConfig) ([]chstore.ServiceVolume, bool) {
	rows, ok := e.selfVolumeMeasured(ctx, cfg)
	if !ok {
		return nil, false
	}
	// ForDetectors: ayar bu süreçte hiç doğrulanmadıysa batch listesi boş →
	// süzgeç yok (tahmini varsayılanla açık epizot kapatılmaz).
	return dropBatchVolumeRows(rows, e.store.AnomalySensitivityForDetectors()), true
}

// selfVolumeMeasured — saatlik ölçüm + önbellek (v0.9.1294 gövdesi; v0.10.2118
// önbellek ham sıçrama satırlarını tutar).
func (e *Evaluator) selfVolumeMeasured(ctx context.Context, cfg chstore.SelfHealthConfig) ([]chstore.ServiceVolume, bool) {
	e.volMu.Lock()
	cached, scannedAt := e.volCache, e.volAt
	e.volMu.Unlock()

	if !scannedAt.IsZero() && time.Since(scannedAt) < selfVolumeScanEvery {
		return cached, true // ucuz tik: ölçüm saatlik, yaşam döngüsü her tikte
	}

	rows, err := e.store.ReadServiceVolumeWindows(ctx, selfVolumeWindow,
		cfg.VolumeSpikeMinSpans, selfVolumeMaxServices)
	if err != nil {
		log.Printf("[evaluator] self-health hacim penceresi okunamadı: %v", err)
		if scannedAt.IsZero() {
			return nil, false // hiç ölçüm yapılmadı: hüküm YOK
		}
		// volAt İLERLETİLMİYOR → bir sonraki tik yeniden dener.
		return cached, true
	}

	var out []chstore.ServiceVolume
	for _, r := range rows {
		if volumeSpikeDecision(r.Cur, r.Prev, cfg.VolumeSpikeMinSpans, cfg.VolumeSpikeFactor) {
			out = append(out, r)
		}
	}

	e.volMu.Lock()
	e.volCache, e.volAt = out, time.Now()
	e.volMu.Unlock()
	return out, true
}

// ── v0.10.2118: çıktı Problem değil ANOMALİ (ml_events) ─────────────────
//
// Operatör kararı (2026-11-13): "servis hacmi sıçradı" Problem değil
// anomali; açılış bildirimi GÖNDERİLMEZ; koşul geçince kendiliğinden
// kapanma aynı. Kural (yukarıdaki saf çekirdek) DEĞİŞMEDİ, yalnız hedef:
// Anomalies sayfası, Inbox kind=anomaly ve menü rozeti ml_events'i
// ListAnomalyFeed üzerinden zaten okuyor. Bildirim garantisi iki kat:
// (1) bu yol notifier'a hiç dokunmaz; (2) ML grup kapısı bu dedektörü
// hiçbir kolda saymaz (ml/group_gate.go groupExcluded + pin testi).
//
// Kimlik uzayı ML motorununkinden ayrık (MLEventID dedektör adını hash'e
// katar); ML lideri ml_events'i okumaz, kapanışı (closeStale) yalnız kendi
// bellek-içi koşularına uygular — iki yazıcı aynı satıra dokunmaz.

// selfVolumeEventWindowSec — olayın window_sec'i = TARAMA kadansı (saat).
// MLEventOpen "son görülme ≤ 4 × pencere" der: ölçüm dört saat üst üste
// başarısızsa olay kendiliğinden "Bitti" görünür (emniyet kapanışı).
const selfVolumeEventWindowSec = uint32(selfVolumeScanEvery / time.Second)

// volumeEvidence — olay kanıtı (teknik ayrıntı; düz cümle kartta).
type volumeEvidence struct {
	Prev             uint64  `json:"prev"`
	Cur              uint64  `json:"cur"`
	Factor           float64 `json:"factor"`
	Threshold        float64 `json:"threshold"`
	MeasureWindowSec int64   `json:"measure_window_sec"`
	MinSpans         uint64  `json:"min_spans"`
}

// volumeEventFor — SAF: bir sıçramanın ml_events satırı. start epizodun
// başlangıcı (kimliğe girer), now son görülme.
func volumeEventFor(v chstore.ServiceVolume, cfg chstore.SelfHealthConfig, start, now time.Time) chstore.MLEvent {
	factor := 0.0
	if v.Prev > 0 {
		factor = float64(v.Cur) / float64(v.Prev)
	}
	ev, _ := json.Marshal(volumeEvidence{Prev: v.Prev, Cur: v.Cur, Factor: factor, Threshold: cfg.VolumeSpikeFactor,
		MeasureWindowSec: int64(selfVolumeWindow / time.Second), MinSpans: cfg.VolumeSpikeMinSpans})
	base := chstore.MLEventID(chstore.MLDetSvcVolumeSpike, v.Service, "")
	return chstore.MLEvent{
		ID:        chstore.MLEpisodeID(base, start),
		Detector:  chstore.MLDetSvcVolumeSpike,
		Signal:    chstore.MLSignalTrace,
		Stage:     chstore.MLStageShadow,
		Service:   v.Service,
		StartedAt: start.UnixNano(),
		LastSeen:  now.UnixNano(),
		WindowSec: selfVolumeEventWindowSec,
		Baseline:  float64(v.Prev),
		Observed:  float64(v.Cur),
		Score:     factor,
		Direction: "up",
		Evidence:  string(ev),
	}
}

// seedVolumeOpen — SAF: CH'deki satırlardan açık epizotlar (servis başına
// en geç başlayan, MLEventOpen kuralıyla). Lider değişimi / yeniden
// başlatma sonrası epizot aynı kimlikle sürer, yeniden açılmaz.
func seedVolumeOpen(evs []chstore.MLEvent, now time.Time) map[string]chstore.MLEvent {
	out := map[string]chstore.MLEvent{}
	for _, e := range evs {
		if e.Detector != chstore.MLDetSvcVolumeSpike || !chstore.MLEventOpen(e, now.UnixNano()) {
			continue
		}
		if cur, ok := out[e.Service]; !ok || e.StartedAt > cur.StartedAt {
			out[e.Service] = e
		}
	}
	return out
}

// planVolumeEvents — SAF yaşam döngüsü (eski reconcileSelfHealth'in
// birebir karşılığı): sıçrayan servis açık değilse AÇILIR (start = now);
// açıksa yalnız TAZE ölçümde (refresh) last_seen/observed/score tazelenir,
// started_at korunur (RMT tam satır: tüm alanlar yeniden yazılır); açık
// olup artık sıçramayan servis aynı satırla stage=closed, last_seen=now.
// Çağıran yalnız KAPSANAN tikte çağırır (ölçülemeyen tik yazmaz).
func planVolumeEvents(open map[string]chstore.MLEvent, spikes []chstore.ServiceVolume, cfg chstore.SelfHealthConfig, refresh bool, now time.Time) (writes []chstore.MLEvent, next map[string]chstore.MLEvent) {
	next = make(map[string]chstore.MLEvent, len(spikes))
	sorted := append([]chstore.ServiceVolume(nil), spikes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Service < sorted[j].Service })
	for _, v := range sorted {
		if _, dup := next[v.Service]; dup {
			continue
		}
		cur, ok := open[v.Service]
		switch {
		case !ok:
			ev := volumeEventFor(v, cfg, now, now)
			writes = append(writes, ev)
			next[v.Service] = ev
		case refresh:
			ev := volumeEventFor(v, cfg, time.Unix(0, cur.StartedAt), now)
			writes = append(writes, ev)
			next[v.Service] = ev
		default:
			next[v.Service] = cur
		}
	}
	closing := make([]string, 0)
	for svc := range open {
		if _, still := next[svc]; !still {
			closing = append(closing, svc)
		}
	}
	sort.Strings(closing)
	for _, svc := range closing {
		ev := open[svc]
		ev.Stage = chstore.MLStageClosed
		ev.LastSeen = now.UnixNano()
		writes = append(writes, ev)
	}
	return writes, next
}

// reconcileVolumeEvents — planı yazar. Açık epizot kümesi bellekte (lider);
// ilk çağrıda ve her TAZE ölçümde CH'den yeniden tohumlanır (lider değişimi
// sonrası bayat bellek başka liderin kapattığı epizodu diriltmesin).
// Tohum okunamazsa bu tik YAZMAZ: tekilleştirme güvencesi olmadan açmak
// aynı sıçramaya ikinci epizot demek.
func (e *Evaluator) reconcileVolumeEvents(ctx context.Context, spikes []chstore.ServiceVolume, cfg chstore.SelfHealthConfig) {
	now := time.Now()
	e.volMu.Lock()
	measuredAt := e.volAt
	e.volMu.Unlock()
	refresh := measuredAt.After(e.volWroteAt)
	if e.volOpen == nil || refresh {
		evs, err := e.store.ListMLEvents(ctx, chstore.MLEventsFilter{Detector: chstore.MLDetSvcVolumeSpike,
			Since: now.Add(-24 * time.Hour), Limit: chstore.MLUnifiedCap})
		if err != nil {
			log.Printf("[evaluator] hacim sıçraması epizotları okunamadı (bu tik yazılmaz): %v", err)
			return
		}
		e.volOpen = seedVolumeOpen(evs, now)
	}
	writes, next := planVolumeEvents(e.volOpen, spikes, cfg, refresh, now)
	for _, w := range writes {
		if err := e.store.UpsertMLEvent(ctx, w); err != nil {
			log.Printf("[evaluator] hacim sıçraması anomalisi yazılamadı %s: %v", w.Service, err)
			if _, wasOpen := e.volOpen[w.Service]; !wasOpen {
				delete(next, w.Service) // açılış yazılamadı: sonraki tik yeniden dener
			}
			continue
		}
		if w.Stage == chstore.MLStageClosed {
			log.Printf("[evaluator] ANOMALY CLEARED (svc_volume_spike): %s", w.Service)
		} else if _, wasOpen := e.volOpen[w.Service]; !wasOpen {
			log.Printf("[evaluator] ANOMALY OPENED (svc_volume_spike): %s %.0f → %.0f span", w.Service, w.Baseline, w.Observed)
		}
	}
	e.volOpen = next
	if refresh {
		e.volWroteAt = measuredAt
	}
}

// volumeMovedNote — SAF (v0.10.2118 geçişi): eski "servis hacmi sıçradı"
// Problem satırı kapanırken açıklamasına düşülen not. Tekrar uygulanırsa
// çoğalmaz (kapanış satırı yeniden yazılabilir).
const volumeMovedPrefix = "Anomali tarafına taşındı (v0.10.2118): servis hacmi sıçraması artık Problem değil, Anomalies listesinde görünür. "

func volumeMovedNote(desc string) string {
	if strings.HasPrefix(desc, volumeMovedPrefix) {
		return desc
	}
	return volumeMovedPrefix + desc
}
