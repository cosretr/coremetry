package chstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
)

// AnomalySensitivityConfig — anomali DEDEKTÖRÜNÜN eşikleri
// (system_settings anahtarı "anomaly_sensitivity", v0.9.826).
//
// NEDEN VİDA: bu eşikler bugüne kadar altı kez kodda değişti (v0.9.48
// düz-MAD tabanı, v0.9.180 mutlak taban, v0.9.193 criticalZ 5→6 ve
// P1-only kapısı, v0.8.220 dwell…) ve her seferinde operatör bir çentik
// ötede aynı duvara çarptı. "Ne kadar sapma OLAY sayılır" filoya, servis
// hacmine ve gecikme profiline bağlı — kodda tahmin edilecek bir sayı
// değil. anomaly_tracked (v0.9.800) hangi metriğin ÖLÇÜLECEĞİNİ verdi;
// bu blob ölçülenin ne zaman OLAY sayılacağını veriyor.
//
// KABLO: anomaly_tracked emsalinin aynısı — system_settings blob'u +
// boot hidrasyonu + 30 sn yenileme + admin PUT + audit; derlenmiş set
// Store'da atomic pointer'da yayınlanır. Dedektör her tarama tikinde
// oradan okur, yani ayar tarayıcıya bir CH okuması EKLEMEDEN canlı kalır.
type AnomalySensitivityConfig struct {
	// Metrics — metrik başına eşikler. Kanonik olmayan anahtarlar okuma
	// yolunda DÜŞÜRÜLÜR: elle düzenlenmiş bir settings satırı dedektöre
	// politikası olmayan bir metrik adı sokamaz.
	Metrics map[string]AnomalyMetricSensitivity `json:"metrics"`
	// DwellBuckets — açılmak için ÜST ÜSTE ateşlemesi gereken 5-dk
	// bucket sayısı (anti-flap). Global: yön/birim taşımıyor.
	DwellBuckets int `json:"dwellBuckets"`
	// CriticalZ — bu |z|'nin üstü critical sayılır. v0.9.193'ten beri
	// dedektör YALNIZ critical verdict'te Problem açtığı için bu, fiilen
	// "açılma eşiği"dir; operatörün en çok çevireceği vida budur.
	CriticalZ float64 `json:"criticalZ"`
	// ExternalOpenCapPerTick (v0.10.587) — dış seri hattında (Influx/Oracle,
	// kind=external) TEK TİKTE açılabilecek YENİ Problem sayısı. Yüksek
	// kardinaliteli bir groupBy (operasyon × hata kodu × kanal) bir tikte
	// yüzlerce Problem açabilirdi; bugüne dek tavan YOKTU. Aşımda kalan
	// seriler için tek bir "tavan aşıldı" özet Problem'i açılır. Yalnız
	// YENİ açılışları kapılar: refresh/resolve/touch etkilenmez, yoksa bayat
	// süpürücü açık Problem'leri "source silent" diye kapatırdı.
	// 0 = ayarlanmamış → varsayılan (20). Kapatma sunulmuyor: sel kapısı
	// bilinçli olarak her zaman açık; 200 pratik tavan.
	ExternalOpenCapPerTick int `json:"externalOpenCapPerTick,omitempty"`
	// v0.10.2270 — `behavior` (davranış motoru, v0.9.935), `traceOpDwellBuckets`
	// ve `logPatternDwellBuckets` (trace_op / log_pattern sürdürmesi) eski
	// dedektörlerle birlikte SİLİNDİ (operatör kararı). Kayıtlı blobdaki
	// alanlar okunmaz; Normalize yalnız bilinen alanları yazdığı için bir
	// sonraki kayıtta düşer (TestAnomalySensitivityRetiredFields).
	// v0.10.2073 — eski `attachToIncident` anahtarı (dedektör problemini incident'a
	// bağlama vidası) Incidents ölü kod diliminde kalktı; kapısı v0.10.2070'te
	// silinmişti. Kayıtlı blobdaki alan okunmaz, bir sonraki kayıtta düşer.
	// v0.10.2068 — eski `serviceSilent` anahtarı (yerel service_silent
	// dedektörü, v0.10.543'ten beri varsayılan kapalı; operatör: "Olmasınlar")
	// dedektörüyle birlikte kalktı. Kayıtlı blobdaki alan okunmaz; Normalize
	// yalnız bilinen alanları yazdığı için bir sonraki kayıtta düşer.
	// TemporalRanking — v0.10.700 (Dynatrace paritesi #2): kök neden
	// hipotezinde zamansal çarpan. "shadow" (varsayılan, boş dahil) =
	// faktör ve gerekçe adaya YAZILIR, sıra/skor değişmez — prod'da bir
	// hafta izlenir; "on" = Score = Structural × (0.5 + 0.5·t), baş
	// şüpheli değişebilir. Bilinmeyen değer Normalize'da shadow'a iner.
	TemporalRanking string `json:"temporalRanking,omitempty"`
	// Runtime — v0.10.890 (paritesi #4 dilim 2): JVM heap bandı → Problem
	// vidaları (anomaly_sensitivity_runtime.go). Varsayılan kip off.
	Runtime AnomalyRuntimeConfig `json:"runtime"`
	// BatchServicePatterns — v0.10.1039 (operatör: "Bazı batch işlerde ani
	// yük artışı olabilir, onları anomali gibi düşünme — özellikle `-batch`
	// geçen servis isimlerinde"): adında bu kalıplardan biri (ASCII büyük-
	// küçük duyarsız ALT DİZGİ) geçen servislerde yük artışı anomali sayılmaz
	// (anomaly_sensitivity_batch.go). *[]string ÇÜNKÜ "yok" ile "boş"
	// AYRI anlamlar taşıyor:
	// nil = alan yazılmamış = varsayılan ["-batch"]; boş liste = operatör
	// kuralı KAPATTI. Normalize somutlaştırır (nil → varsayılan liste), boş
	// liste `[]` olarak yazılır, `null` olarak DEĞİL.
	BatchServicePatterns *[]string `json:"batchServicePatterns,omitempty"`
	// v0.10.2087 — opLatency / opLatencyDwellBuckets / opLatencyMinAffected
	// alanları trace_op_latency dedektörüyle birlikte SİLİNDİ (operatör kararı
	// 2026-11-13). Kayıtlı bloblarda kalan alanlar okunmaz; bir sonraki
	// kayıtta Normalize onları yazmaz.
	// ServiceSlowdown — v0.10.1091 (operatör: "Dün söylediğim CRM sorunu yine
	// oldu, bir sürü anomali geldi ama P1 problem gelmedi" → "Onay"): yaygın
	// yavaşlama hızlı yolu (`svc-slowdown:<servis>`, evaluator). Tek kovada ≥ 3
	// operasyon p99 ≥ 5 s VE ≥ 20× kendi tabanı → servis üzerinde P1. Vidalar
	// ve gerekçe anomaly_sensitivity_slowdown.go'da; Enabled nil = AÇIK.
	ServiceSlowdown ServiceSlowdownConfig `json:"serviceSlowdown"`
	// REDSource — v0.10.1478 (operatör: "Servislerin kendi içerisinde haftalık
	// olağan akışlarını takip etse ve gerçek bir anomaly öyle yakalasa"): metrik
	// dedektörü + davranış motorunun RED serisinin KAYNAĞI. "auto" (varsayılan,
	// boş dahil) = VictoriaMetrics yapılandırılmışsa ve servisin OTel HTTP/gRPC
	// sunucu histogramı varsa ÖRNEKLENMEMİŞ VM serisi, yoksa servis başına span
	// MV'si; "span" = v0.10.1477 davranışı (geri alma yolu). Mantık
	// anomaly_sensitivity_red_source.go + internal/anomaly/red_vm_*.go.
	REDSource string `json:"redSource,omitempty"`
	// REDSourceEnv — v0.10.1478: VM serisinin ortam kapsamı (boş = tüm
	// ortamlar; span MV'si de servis adına göre TÜM ortamları toplar). Dolu
	// ise ortam etiketi + canlı değerler resolveHTTPEnvScope kuralıyla
	// (LabelMap.Env > deployment_environment_name > keşif; toleranslı eşleme);
	// eşleşmezse VM KULLANILMAZ (span yedeği) — ortamlar sessizce karışmaz.
	REDSourceEnv string `json:"redSourceEnv,omitempty"`
	// Calendar — v0.10.1483: mevsimsel baseline'ların takvim etkileri (özel
	// günler + isteğe bağlı otomatik ay sonu sınıfı; anomaly_calendar.go).
	// Eksik bölüm (eski blob) = takvim yok, davranış bayt bayt eski.
	Calendar AnomalyCalendarConfig `json:"calendar"`
	// AlertSource — v0.10.1489: ALARM KURALLARININ (evaluator evaluateOne +
	// Alerts önizlemesi / taban önerisi) ölçü kaynağı. "auto" (varsayılan, boş
	// dahil) = VM yapılandırılmışsa ve servisin HTTP/gRPC sunucu histogramı
	// varsa ÖRNEKLENMEMİŞ VM penceresi, yoksa kural×servis başına span MV'si;
	// "span" = v0.10.1488 davranışı (geri alma). Ortam kapsamı REDSourceEnv
	// (aynı VM kapsamı). Mantık internal/anomaly/alert_vm*.go.
	AlertSource string `json:"alertSource,omitempty"`
	// ErrorRamp — teknik hata (5xx) oranının SÜREKLİ yükselişi: ani-sapma
	// kolunun z + %1 mutlak tabanının göremediği saatlik rampa
	// (anomaly_sensitivity_error_ramp.go). Eksik bölüm = varsayılan (açık).
	ErrorRamp ErrorRampConfig `json:"errorRamp"`
	// Subjects — v0.10.1790 (Davis FAZ 1.2): endpoint / db / queue özne
	// tabanları (anomaly_sensitivity_subjects.go). Eksik bölüm (eski blob) =
	// üçü kapalı, davranış bayt bayt eski.
	Subjects AnomalySubjectsConfig `json:"subjects"`
	// SeasonalTimezone — v0.10.1790 (karar 3): mevsimsel gün sınıfının IANA
	// dilimi. Eksik = DefaultSeasonalTimezone (Europe/Istanbul). Tüketen yol
	// v0.10.1791 (F1.3).
	SeasonalTimezone string `json:"seasonalTimezone,omitempty"`
}

const (
	TemporalRankingModeShadow = "shadow"
	TemporalRankingModeOn     = "on"
)

// normalizeTemporalRanking — bilinmeyen/boş → shadow; büyük-küçük harf
// toleranslı.
func normalizeTemporalRanking(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), TemporalRankingModeOn) {
		return TemporalRankingModeOn
	}
	return TemporalRankingModeShadow
}

// TemporalRankingOn — nil-güvenli okuma: yalnız açıkça "on".
func (c AnomalySensitivityConfig) TemporalRankingOn() bool {
	return normalizeTemporalRanking(c.TemporalRanking) == TemporalRankingModeOn
}

// clampRangeF / clampRangeI — aralık dışı (ya da NaN/Inf) → varsayılan.
func clampRangeF(v, min, max, def float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < min || v > max {
		return def
	}
	return v
}

func clampRangeI(v, min, max, def int) int {
	if v < min || v > max {
		return def
	}
	return v
}

// AnomalyMetricSensitivity — tek bir metriğin eşikleri. Beşi de
// BİRLİKTE "bu sapma bir olay mı" sorusunu cevaplıyor ama farklı
// boşlukları kapatıyorlar; biri diğerinin yerine geçmez.
type AnomalyMetricSensitivity struct {
	// FloorPct — göreli değişim tabanı: |current-median|/|median|.
	// İstatistiksel olarak anlamlı ama KÜÇÜK hareketleri eler.
	FloorPct float64 `json:"floorPct"`
	// AbsFloor — mutlak DEĞER tabanı: current bunun altındaysa yükseliş
	// yönlü anomali açılmaz. "Sıfıra yakın tabandan minik sıçrama"
	// boşluğunu kapatır (error_rate=1 → milyonlarca istekte 1-2-3 hata
	// problem olmasın). Düşüşleri etkilemez.
	AbsFloor float64 `json:"absFloor"`
	// MinAbsDelta — mutlak FARK tabanı: |current-median| bunun
	// altındaysa açılmaz. FloorPct'in kör noktası: küçük bir medyanda
	// %10 göreli değişim mutlak olarak hiçbir şey ifade etmeyebilir
	// (1.9ms → 2.1ms yüzde olarak %10'dur ama kimse uyanmaz).
	MinAbsDelta float64 `json:"minAbsDelta"`
	// MinMAD — MAD'in ALT SINIRI, MAX olarak uygulanır:
	// mad = max(mad, minMAD). Sıkı (küçük MAD'li) bir baseline'da z
	// patlar; bu taban sigmanın anlamlı bir alt sınırı olmasını sağlar.
	//
	// flatMADFloor'un (v0.9.48) DÜZELTİLMİŞ hâli: o yalnız mad≈0 iken
	// devreye giriyordu, yani "tarihi hiç kıpırdamamış" seriyi koruyup
	// "neredeyse hiç kıpırdamamış" seriyi korumasız bırakıyordu — ve
	// gerçek false-pozitifler tam o ikinci kümede.
	MinMAD float64 `json:"minMAD"`
	// MinBaselineRate — hacim kapısı: son bucket'ın istek hızı (istek/sn)
	// bunun altındaysa anomali AÇILMAZ. Düşük hacimli bir serviste
	// yüzdeler ve kuyruk gecikmeleri gürültüdür — 20 isteğin 1'i hata
	// %5'tir ama olay değildir. Yalnız AÇILMAYA uygulanır; çözülme
	// etkilenmez (susan servisin problemi kapanmaya devam etmeli).
	MinBaselineRate float64 `json:"minBaselineRate"`
}

// AnomalySensitivityMetrics — kanonik metrik listesi VE gösterim sırası.
// AnomalyTrackedMetrics ile AYNI küme olmak zorunda (aynı bilinçli
// tekrar: chstore, anomaly'yi import EDEMEZ — ters yön).
var AnomalySensitivityMetrics = AnomalyTrackedMetrics

// Kelepçeler — elle düzenlenmiş bir settings satırı ya da hatalı bir PUT
// dedektörü işlevsiz bırakamasın. Üst sınırlar "bu vidayı sonuna kadar
// çevirdim, motor sustu" halini engelliyor; alt sınırlar negatif/anlamsız
// değerleri.
const (
	anomalyMinCriticalZ    = 1.0
	anomalyMaxCriticalZ    = 50.0
	anomalyMinDwellBuckets = 1
	anomalyMaxDwellBuckets = 24 // 2 saat; ötesi "hiç açma" demek
	// v0.10.587 — dış hat açılış tavanı: 1 (her tik tek Problem) … 200.
	anomalyMinExternalOpenCap = 1
	anomalyMaxExternalOpenCap = 200
)

// v0.10.1585 — metrik başına hacim kapısı varsayılanları (istek/sn); göç de
// bu iki sabiti yazar.
const (
	MinBaselineRateErrorDefault = 0.2
	MinBaselineRateP99Default   = 0.5
)

// DefaultAnomalySensitivity — v0.9.826'nın gemiye giren davranışı:
// BUGÜNKÜ davranış, ARTI iki cerrahi düzeltme (p99_ms).
//
// p99_ms MinMAD=1.0 ve AbsFloor=10 operatörün bildirdiği vakadan
// geliyor: medyan 1.90ms olan bir op 9.69ms'e çıkınca (mad 0.657)
// z=8.0σ üretiyor ve critical anomali açıyordu. 8 milisaniyelik bir
// sıçrama hiçbir kullanıcının fark etmediği bir şey; sorun sapmanın
// istatistiksel büyüklüğü değil, BİRİMİNİN önemsizliğiydi.
//   - MinMAD 1.0ms: sıkı baseline'da sigmayı gerçekçi bir alt sınıra
//     çeker; aynı vakada z 8.0 → 5.25, yani criticalZ 6.0 kapısının
//     altında kalır.
//   - AbsFloor 10ms: 10ms'nin altındaki bir p99 yükseliş yönünde hiç
//     olay sayılmaz — göreli oran ne olursa olsun.
//
// Diğer metrikler AYNEN bugünkü sabitler; MinAbsDelta 0 = kapalı.
//
// v0.10.1585 (Davis paritesi FAZ 0.5 / A, operatör onayı 2026-10-29) —
// MinBaselineRate metrik başına küçük taban: error_rate 0.2 istek/sn (5 dk
// kovada 60 istek → tek hata %1.67 ≈ absFloor %1), p99_ms 0.5 istek/sn (150
// istek → p99 en az iki örnekle). request_rate 0: düşüş yönünde son kova
// tanım gereği düşüktür, taban düşüşü susturur. Kapı SON kovaya bakar (aynı
// semantik).
// Kayıtlı 0 Normalize'dan geçtiği için yeni değer prod'a tek seferlik göçle
// ulaşır (anomaly_sensitivity_minrate_migrate.go).
func DefaultAnomalySensitivity() AnomalySensitivityConfig {
	return AnomalySensitivityConfig{
		Metrics: map[string]AnomalyMetricSensitivity{
			// v0.9.180: <%1 = birkaç-hata gürültüsü, açma.
			"error_rate": {FloorPct: 0.10, AbsFloor: 1.0, MinBaselineRate: MinBaselineRateErrorDefault},
			// v0.9.826: iki cerrahi düzeltme — gerekçe yukarıda.
			"p99_ms": {FloorPct: 0.10, AbsFloor: 10.0, MinMAD: 1.0, MinBaselineRate: MinBaselineRateP99Default},
			// Hacim sıçraması VE düşüşü; mutlak taban yok (düşüş yönü
			// zaten AbsFloor'dan etkilenmiyor, sıçrama için de anlamlı
			// bir evrensel taban yok).
			"request_rate": {FloorPct: 0.15},
		},
		DwellBuckets:           3,                       // v0.8.220 — 3 × 5dk = 15 dk sürekli ateşleme
		CriticalZ:              6.0,                     // v0.9.193 — operatör: yalnız P1-sınıfı gelsin
		ExternalOpenCapPerTick: 20,                      // v0.10.587 — Oracle audit §6.5 önerisi
		Runtime:                DefaultAnomalyRuntime(), // v0.10.890 — heap kipi off
		// v0.10.1039 — BİLİNÇLİ varsayılan davranış değişikliği: operatörün
		// adını verdiği `-batch` kalıbı kutudan açık gelir.
		BatchServicePatterns: batchPatternsPtr(DefaultBatchServicePatterns()),
		// v0.10.1091 — yaygın yavaşlama hızlı yolu, operatör onaylı: AÇIK.
		ServiceSlowdown: DefaultServiceSlowdown(),
		ErrorRamp:       DefaultErrorRamp(),
		// v0.10.1478 — operatör isteği: VM varsa örneklenmemiş seri (auto).
		REDSource: AnomalyREDSourceAuto,
		// v0.10.1483 — takvim boş, otomatik ay sonu KAPALI.
		Calendar: NormalizeAnomalyCalendar(AnomalyCalendarConfig{}),
		// v0.10.1489 — alarm kuralları da VM varsa örneklenmemiş pencereden (auto).
		AlertSource: AnomalyREDSourceAuto,
		// v0.10.1790 — dilim İstanbul (karar 3). v0.10.2308 — özneler endpoint +
		// queue açık, terfi gölge (DefaultAnomalySubjects).
		Subjects:         DefaultAnomalySubjects(),
		SeasonalTimezone: DefaultSeasonalTimezone,
	}
}

func boolPtr(b bool) *bool { return &b }

// NormalizeAnomalySensitivity kanonik anahtarları tamamlar, bilinmeyenleri
// düşürür ve her alanı kelepçeler.
//
// NEGATİF → VARSAYILAN (sıfırlamak DEĞİL): negatif bir taban anlamsız ve
// "operatör bu alanı boş bıraktı" ile "operatör bilerek 0 yazdı"yı
// ayırmak gerekiyor. 0 MEŞRU bir değer (vidayı kapat) ve öyle korunur;
// yalnız negatifler varsayılana döner.
func NormalizeAnomalySensitivity(c AnomalySensitivityConfig) AnomalySensitivityConfig {
	d := DefaultAnomalySensitivity()
	out := AnomalySensitivityConfig{
		Metrics:                make(map[string]AnomalyMetricSensitivity, len(AnomalySensitivityMetrics)),
		DwellBuckets:           c.DwellBuckets,
		CriticalZ:              c.CriticalZ,
		ExternalOpenCapPerTick: c.ExternalOpenCapPerTick,                    // v0.10.587 — kopyalanmazsa PUT'ta sessizce düşer
		TemporalRanking:        normalizeTemporalRanking(c.TemporalRanking), // v0.10.700
		Runtime:                NormalizeAnomalyRuntime(c.Runtime),          // v0.10.890 — kopyalanmazsa PUT'ta düşer
		// v0.10.1039 — kopyalanmazsa PUT'ta sessizce düşer ve her kayıt
		// kuralı varsayılana döndürürdü. SOMUTLAŞTIRIR: nil → varsayılan
		// liste; boş liste boş kalır (kural kapalı) ve `[]` yazılır.
		BatchServicePatterns: batchPatternsPtr(c.BatchServicePatternList()),
		// v0.10.1091 — kopyalanmazsa PUT'ta düşerdi. Eksik bölüm (eski blob)
		// sıfır değerdir → kelepçe varsayılanlara doldurur, bayrak nil → açık.
		ServiceSlowdown: NormalizeServiceSlowdown(c.ServiceSlowdown),
		// v0.10.1478 — kopyalanmazsa PUT'ta düşer ve kaynak her kayıtta auto'ya dönerdi.
		REDSource:    normalizeREDSource(c.REDSource),
		REDSourceEnv: normalizeREDSourceEnv(c.REDSourceEnv),
		// v0.10.1489 — kopyalanmazsa PUT'ta düşer (kaynak her kayıtta auto'ya dönerdi).
		AlertSource: normalizeREDSource(c.AlertSource),
		// v0.10.1483 — kopyalanmazsa PUT'ta düşerdi.
		Calendar: NormalizeAnomalyCalendar(c.Calendar),
		// Sürekli teknik hata yükselişi — kopyalanmazsa PUT'ta düşerdi.
		ErrorRamp: NormalizeErrorRamp(c.ErrorRamp),
		// v0.10.1790 — kopyalanmazsa PUT'ta düşerdi (açılan özne her kayıtta
		// kapanır, dilim her kayıtta varsayılana döner).
		Subjects:         NormalizeAnomalySubjects(c.Subjects),
		SeasonalTimezone: normalizeSeasonalTimezone(c.SeasonalTimezone),
	}
	for _, m := range AnomalySensitivityMetrics {
		def := d.Metrics[m]
		got, ok := c.Metrics[m]
		if !ok {
			// Eksik anahtar varsayılanını alır: ileride dördüncü bir
			// metrik eklendiğinde eski satırlar onu varsayılanıyla
			// devralır.
			out.Metrics[m] = def
			continue
		}
		out.Metrics[m] = AnomalyMetricSensitivity{
			FloorPct:        clampFloor(got.FloorPct, def.FloorPct),
			AbsFloor:        clampFloor(got.AbsFloor, def.AbsFloor),
			MinAbsDelta:     clampFloor(got.MinAbsDelta, def.MinAbsDelta),
			MinMAD:          clampFloor(got.MinMAD, def.MinMAD),
			MinBaselineRate: clampFloor(got.MinBaselineRate, def.MinBaselineRate),
		}
	}
	if out.DwellBuckets < anomalyMinDwellBuckets || out.DwellBuckets > anomalyMaxDwellBuckets {
		out.DwellBuckets = d.DwellBuckets
	}
	if out.CriticalZ < anomalyMinCriticalZ || out.CriticalZ > anomalyMaxCriticalZ {
		out.CriticalZ = d.CriticalZ
	}
	// v0.10.587 — 0 "ayarlanmamış"tır, "kapalı" değil: eski settings satırı
	// alanı hiç taşımıyor; sıfırı kapı-yok diye okumak seli geri getirirdi.
	if out.ExternalOpenCapPerTick < anomalyMinExternalOpenCap || out.ExternalOpenCapPerTick > anomalyMaxExternalOpenCap {
		out.ExternalOpenCapPerTick = d.ExternalOpenCapPerTick
	}
	return out
}

// clampFloor — negatif ya da sayı-olmayan (NaN/Inf, elle düzenlenmiş
// JSON'dan gelebilir) bir taban varsayılana döner. Sıfır MEŞRU: vidayı
// kapatmanın yolu.
func clampFloor(v, def float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return def
	}
	return v
}

// For — bir metriğin eşikleri; bilinmeyen metrik varsayılan politikayı
// alır (dedektörün policyFor'u ile aynı duruş: eksik ayar SUSTURMAZ).
func (c AnomalySensitivityConfig) For(metric string) AnomalyMetricSensitivity {
	if s, ok := c.Metrics[metric]; ok {
		return s
	}
	return AnomalyMetricSensitivity{FloorPct: 0.10}
}

const anomalySensitivityKey = "anomaly_sensitivity"

// ReadAnomalySensitivity — KAYITLI blob, hatayı SAKLAMADAN (v0.10.1039).
// Satır yok (hiç kaydedilmemiş) → varsayılanlar + nil: bu BAŞARILI bir
// okumadır, "ayar yok" bilgisi. CH ya da JSON hatası → hata döner; çağıran
// ne yapacağına kendisi karar verir.
//
// NEDEN AYRILDI: eski GetAnomalySensitivity (v0.10.2276'da çağıransız
// olduğu için silindi) hatada varsayılana düşüyordu ve
// LoadAnomalySensitivity o varsayılanı YAYINLIYORDU. v0.10.1039 öncesi
// bunun bedeli bir tikte eşik kaymasıydı; batch kalıplarıyla birlikte
// varsayılan liste TEK YÖNLÜ kapatma eylemleri sürüyor (açık batch
// request_rate / hacim satırları kapanır, sonra YENİ problem + yeni
// bildirim olarak geri açılır). Tek okuma hıçkırığı bunu tetiklememeli.
func (s *Store) ReadAnomalySensitivity(ctx context.Context) (AnomalySensitivityConfig, error) {
	raw, err := s.GetSetting(ctx, anomalySensitivityKey)
	if err != nil {
		return AnomalySensitivityConfig{}, err
	}
	if len(raw) == 0 {
		return DefaultAnomalySensitivity(), nil
	}
	var c AnomalySensitivityConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return AnomalySensitivityConfig{}, fmt.Errorf("anomaly_sensitivity blob'u çözülemedi: %w", err)
	}
	return NormalizeAnomalySensitivity(c), nil
}

// SaveAnomalySensitivity — system_settings'e yazar (yeni şema yok,
// invariant #6).
func (s *Store) SaveAnomalySensitivity(ctx context.Context, c AnomalySensitivityConfig) error {
	raw, err := json.Marshal(NormalizeAnomalySensitivity(c))
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, anomalySensitivityKey, raw)
}

// SetAnomalySensitivity yayınlar (atomic) — anomaly_tracked kablosunun
// aynısı. YETKİLİ değer yayınıdır (başarılı okuma ya da admin PUT'u) ve
// ayarı DOĞRULANMIŞ işaretler (v0.10.1039, AnomalySensitivityConfirmed).
func (s *Store) SetAnomalySensitivity(c AnomalySensitivityConfig) {
	n := NormalizeAnomalySensitivity(c)
	s.anomalySensitivity.Store(&n)
	s.anomalySensitivityConfirmed.Store(true)
}

// AnomalySensitivity — yayınlanmış ayar. Hiç hidrate edilmemişse
// (Store{} kuran testler, hidrasyondan önceki ilk tik) varsayılanlar.
func (s *Store) AnomalySensitivity() AnomalySensitivityConfig {
	if p := s.anomalySensitivity.Load(); p != nil {
		return *p
	}
	return DefaultAnomalySensitivity()
}

// AnomalySensitivityConfirmed — v0.10.1039: bu süreçte ayar en az bir kez
// YETKİLİ kaynaktan geldi mi (başarılı CH okuması ya da admin PUT'u)?
// false iken yayınlanmış değer yalnız varsayılan TAHMİNİDİR.
func (s *Store) AnomalySensitivityConfirmed() bool {
	return s.anomalySensitivityConfirmed.Load()
}

// AnomalySensitivityForDetectors — v0.10.1039: batch kuralını tüketen karar
// noktalarının (metrik dedektörü + davranış motoru, trace_op,
// self-volume-spike) tik okuması. Ayar
// henüz DOĞRULANMADIYSA batch listesi BOŞ döner = kural devre dışı =
// v0.10.1039 öncesi davranış.
//
// Yumuşak-hata yönü bilinçli SÜZGEÇSİZ (aiops §5: "mute listesi okunamadı →
// süzgeçsiz terfi"): varsayılan liste bir tahmin; operatör kuralı kapattıysa
// ya da başka kalıplar yazdıysa, tahminle susturmak satırları tek yönlü
// kapatır (açık problem kapanır, bayat süpürme dokunulmayan satırı kapatır,
// olaylar yaşlanıp terfi problemini kapatır) ve doğrulanınca hepsi YENİ
// bildirimle geri açılır. Süzgeçsiz kalmanın bedeli en kötü ihtimalle
// doğrulanana dek bugünkü gürültü. Diğer eşikler (z, taban, davranış)
// yayınlanan değerden aynen gelir.
func (s *Store) AnomalySensitivityForDetectors() AnomalySensitivityConfig {
	c := s.AnomalySensitivity()
	if !s.AnomalySensitivityConfirmed() {
		c.BatchServicePatterns = batchPatternsPtr(nil)
	}
	return c
}

// LoadAnomalySensitivity = oku + yayınla. Boot hidrasyonunun ve 30 sn
// yenilemesinin tek gövdesi; hem API sunucusu hem dedektör/evaluator/
// recorder (kendi Start'larında) bunu çağırır.
//
// v0.10.1039 — OKUMA HATASINDA SON YAYINLANAN DEĞER KORUNUR. Varsayılan
// yalnız HİÇBİR ŞEY yayınlanmamışsa yayınlanır ve bu durumda ayar
// doğrulanmamış kalır (AnomalySensitivityConfirmed false → batch kuralı
// devre dışı). Hata geçişte BİR KEZ loglanır (30 sn'de bir değil); başarılı
// okuma dönünce bir kez daha.
func (s *Store) LoadAnomalySensitivity(ctx context.Context) AnomalySensitivityConfig {
	return s.loadAnomalySensitivityWith(func() (AnomalySensitivityConfig, error) {
		return s.ReadAnomalySensitivity(ctx)
	})
}

// loadAnomalySensitivityWith — Load'un okuyucusu enjekte edilebilir gövdesi
// (test: iyi değer → hata → iyi değer geçişleri CH'siz).
func (s *Store) loadAnomalySensitivityWith(read func() (AnomalySensitivityConfig, error)) AnomalySensitivityConfig {
	c, err := read()
	if err != nil {
		if s.anomalySensitivityReadFailing.CompareAndSwap(false, true) {
			log.Printf("[settings] anomaly_sensitivity okunamadı: %v — son yayınlanan değer korunuyor (doğrulanmış: %v)",
				err, s.AnomalySensitivityConfirmed())
		}
		if s.anomalySensitivity.Load() == nil {
			d := DefaultAnomalySensitivity()
			s.anomalySensitivity.CompareAndSwap(nil, &d) // doğrulanmış SAYILMAZ
		}
		return s.AnomalySensitivity()
	}
	if s.anomalySensitivityReadFailing.CompareAndSwap(true, false) {
		log.Printf("[settings] anomaly_sensitivity yeniden okunabiliyor — kayıtlı değer yayınlandı")
	}
	s.SetAnomalySensitivity(c)
	return s.AnomalySensitivity()
}
