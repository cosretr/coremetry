package chstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
)

// ProblemPriorityConfig — alert-problem öncelik merdiveninin (P1/P2/P3)
// iki gömülü sabiti, artık operatörün elinde.
//
// v0.9.838 — operatör-bildirimli: "hâlâ çok fazla alert rule'dan P1
// geliyor". Prod'da 29 critical'in 22'si P1'di; örnek bir error_rate
// problemi 3.93/1.31 = 3.0× oranla eşiği aşıyor ve 2× kapısından
// otomatik P1 çıkıyordu.
//
// Kök neden, exception tarafındaki v0.9.775 vakasının aynısı: değer
// yanlış değil, DEĞERİN KODDA GÖMÜLÜ OLMASI yanlış. "Eşiğin kaç katı
// ihlal artık gece kaldırır" sorusunun cevabı filoya, kural setine ve
// nöbet devrine bağlı. HİÇBİR ayar blobu bu hatta dokunmuyordu:
// exception_triage yalnız exception gruplarına, anomaly_* yalnız
// anomali dedektörlerine iniyor.
//
// Bu sürüm DAVRANIŞ DEĞİŞTİRMİYOR — varsayılanlar bugünkü sabitlerin
// birebir aynısı (2.0× ve 4 saat). Yalnız vidayı takıyor; katı sıkmak
// operatörün kararı.
type ProblemPriorityConfig struct {
	// BigBreachRatio — "büyük ihlal" kapısı: değer eşiğin bu kadar katına
	// çıktığında (">" kuralları) ya da eşiğin bu kadar katı altına
	// düştüğünde ("<" kuralları, oran ters çevrilir) ihlal büyük sayılır.
	// critical + büyük ihlal = P1; warning + büyük ihlal = P2.
	//
	// Varsayılan 2.0. Kelepçe ≥1.1: 1.0 "eşiği aşan HER ŞEY büyük ihlal"
	// demek olurdu ve merdivenin üst basamağını anlamsızlaştırırdı.
	BigBreachRatio float64 `json:"bigBreachRatio"`
	// StaleCriticalHours — bir critical problem bu kadar saattir AÇIKSA
	// (çözülmemiş) tek başına P1'e terfi eder. Varsayılan 4.
	//
	// 0 = bu terfi TAMAMEN KAPALI. Operatörün P1 selini kesmek için
	// isteyebileceği ilk vida bu olabilir: uzun süre açık kalan bir
	// critical "hâlâ ilgilenilmedi" demek, "şu an daha acil" demek değil.
	// Negatif değer kelepçeyle 0'a düşer.
	StaleCriticalHours float64 `json:"staleCriticalHours"`
	// InboxKeepSourcePriority — v0.10.1072 (operatör, prod 2026-10-02: "Dün
	// akşam CRM database'inde sorun oldu ama problemlerde P1 gelmedi"): inbox
	// görünüm kuralının (v0.9.487, exception dışı türler HEP P3) DAR istisna
	// listesi. Kaynak kimliği (Problem.RuleID; incident için
	// "incident:<severity>") bu kalıplardan birine uyan satır inbox'ta kendi
	// P1/P2'sini KORUR; gerisi bugünkü gibi P3'e çivilenir. Kalıp sözdizimi ve
	// varsayılan liste problem_priority_inbox.go'da.
	//
	// *[]string ÇÜNKÜ "yok" ile "boş" ayrı anlam taşıyor (BatchServicePatterns
	// emsali): nil = alan yazılmamış = varsayılan liste; boş liste = operatör
	// istisnayı KAPATTI (saf v0.9.487). Normalize somutlaştırır.
	InboxKeepSourcePriority *[]string `json:"inboxKeepSourcePriority,omitempty"`
}

// DefaultProblemPriority — v0.9.838 ÖNCESİNİN gömülü sabitleri, birebir.
// Bu değerler değişirse davranış değişir; sürüm notunda söylenmeli.
func DefaultProblemPriority() ProblemPriorityConfig {
	c := defaultProblemPriorityKnobs()
	// v0.10.1072 — BİLİNÇLİ varsayılan davranış değişikliği (operatör
	// onaylı): dar istisna listesi kutudan açık gelir.
	c.InboxKeepSourcePriority = inboxKeepPtr(DefaultInboxKeepSourcePriority())
	return c
}

// defaultProblemPriorityKnobs — iki sayısal vidanın varsayılanı, listesiz
// (sıcak yol ayırma yapmasın).
func defaultProblemPriorityKnobs() ProblemPriorityConfig {
	return ProblemPriorityConfig{
		BigBreachRatio:     2.0,
		StaleCriticalHours: 4,
	}
}

// MinBigBreachRatio — 1.0 ve altı, "eşiği aşan her şey büyük ihlal"
// anlamına gelirdi (oran zaten ≥1 olduğunda ihlal var). Kapının bir
// ANLAMI kalması için alt sınır.
const MinBigBreachRatio = 1.1

const problemPriorityKey = "problem_priority"

// NormalizeProblemPriority saçma / eksik değerleri kullanılabilir bir
// merdivene çeker. Saf + tablo-testli; API doğrulaması ve okuma yolu
// AYNI kuralları kullanır ki elle düzenlenmiş bir system_settings satırı
// da güvenli bir şekle düşsün.
//
// TAMAMEN SIFIR struct = "hiç doldurulmamış" → varsayılanlar. Bu dal
// bilinçli: StaleCriticalHours'ta 0 ANLAMLI bir değer ("terfi kapalı"),
// yani alan-alan "0 ise varsayılan" diyemiyoruz. Zero-value'yu bütün
// olarak yakalamak, `ProblemPriorityConfig{}` yazan bir çağıranın
// sessizce bayat-critical terfisini kapatmasını engelliyor — ve
// BigBreachRatio 0'da `ratio >= 0` HER problemi büyük ihlal yapardı.
//
// v0.10.1072 — "tamamen sıfır" artık YALNIZ iki sayısal alana bakar (dilim
// işaretçisi struct'ı karşılaştırılamaz yaptı); istisna listesi kendi
// kuralıyla somutlaşır: nil → varsayılan liste, boş liste boş kalır.
func NormalizeProblemPriority(c ProblemPriorityConfig) ProblemPriorityConfig {
	c = normalizeProblemPriorityKnobs(c)
	c.InboxKeepSourcePriority = inboxKeepPtr(c.InboxKeepSourcePriorityList())
	return c
}

// normalizeProblemPriorityKnobs — yalnız iki sayısal vida. computePriority
// SATIR BAŞINA bunu çağırır: istisna listesini her satırda yeniden
// normalize etmek (dilim + harita ayırma) sıcak yolda boşa iş olurdu ve
// merdiven listeyi okumuyor bile.
func normalizeProblemPriorityKnobs(c ProblemPriorityConfig) ProblemPriorityConfig {
	d := defaultProblemPriorityKnobs()
	if c.BigBreachRatio == 0 && c.StaleCriticalHours == 0 {
		c.BigBreachRatio, c.StaleCriticalHours = d.BigBreachRatio, d.StaleCriticalHours
		return c
	}
	if c.BigBreachRatio <= 0 {
		c.BigBreachRatio = d.BigBreachRatio
	}
	if c.BigBreachRatio < MinBigBreachRatio {
		c.BigBreachRatio = MinBigBreachRatio
	}
	if c.StaleCriticalHours < 0 {
		c.StaleCriticalHours = 0
	}
	return c
}

// problemPriorityCfg — süreç-genelinde tek kopya, atomik.
//
// EnrichProblemsWithPriority satır BAŞINA computePriority çağırıyor ve
// bildirim hattından da (notify/alert_title.go) geçiyor; oradan bir CH
// okuması yapmak ayarı hot-path'e sokardı. Blob boot'ta hidrate edilir
// (api.LoadProblemPriority), PUT'ta güncellenir ve çok-pod kurulumlarda
// 30 sn'lik yenileme döngüsüyle yakınsar — exception_triage kablosunun
// aynısı, yalnız global chstore'da çünkü EnrichProblemsWithPriority
// paket-düzeyi ve ctx almıyor.
var problemPriorityCfg atomic.Pointer[ProblemPriorityConfig]

// CurrentProblemPriority — hiç hidrate edilmemişse varsayılanlar. Test
// ikilisi hiçbir zaman Store'a bağlanmaz, yani saf öncelik testleri bu
// daldan geçer ve v0.9.838 öncesi davranışı görür.
func CurrentProblemPriority() ProblemPriorityConfig {
	if c := problemPriorityCfg.Load(); c != nil {
		return *c
	}
	return DefaultProblemPriority()
}

// SetProblemPriority — normalize EDİLMİŞ hâlini yayınlar, böylece
// okuyucular hiçbir zaman anlamsız bir kapı görmez.
func SetProblemPriority(c ProblemPriorityConfig) {
	n := NormalizeProblemPriority(c)
	problemPriorityCfg.Store(&n)
}

// ReadProblemPriority — KAYITLI blob, hatayı SAKLAMADAN (v0.10.1072;
// ReadAnomalySensitivity emsali). Satır yok → varsayılanlar + nil (başarılı
// okuma: "ayar yok" bilgisi); CH ya da JSON hatası → hata döner.
//
// Unmarshal ÖNCEDEN DOLDURULMUŞ bir struct'a yapılıyor: JSON'da olmayan
// bir alan varsayılanında kalır. Yoksa `{"bigBreachRatio":3}` gibi elle
// yazılmış bir blob, staleCriticalHours'ı sessizce 0'a (terfi kapalı)
// çekerdi — operatörün yazmadığı bir karar.
func (s *Store) ReadProblemPriority(ctx context.Context) (ProblemPriorityConfig, error) {
	raw, err := s.GetSetting(ctx, problemPriorityKey)
	if err != nil {
		return ProblemPriorityConfig{}, err
	}
	if len(raw) == 0 {
		return DefaultProblemPriority(), nil
	}
	c := DefaultProblemPriority()
	if err := json.Unmarshal(raw, &c); err != nil {
		return ProblemPriorityConfig{}, fmt.Errorf("problem_priority blob'u çözülemedi: %w", err)
	}
	return NormalizeProblemPriority(c), nil
}

// GetProblemPriority — ReadProblemPriority'nin hatada varsayılana yumuşak
// düşen hâli (eski imza). Yeni çağıran Read* kullanmalı.
func (s *Store) GetProblemPriority(ctx context.Context) ProblemPriorityConfig {
	c, err := s.ReadProblemPriority(ctx)
	if err != nil {
		return DefaultProblemPriority()
	}
	return c
}

// problemPriorityReadFailing — hata geçişini BİR KEZ loglamak için (30 sn'de
// bir değil).
var problemPriorityReadFailing atomic.Bool

// LoadProblemPriorityWith — boot hidrasyonu + 30 sn yenilemenin gövdesi,
// okuyucu enjekte edilebilir (test CH'siz).
//
// v0.10.1072 — OKUMA HATASINDA SON İYİ DEĞER KORUNUR (anomaly_sensitivity
// v0.10.1039 emsali). Hatada varsayılanı yayınlamak, operatörün daralttığı ya
// da boşalttığı istisna listesini tek bir CH hıçkırığında varsayılana
// döndürür ve inbox'ta P1 satırları bir tik görünüp kaybolurdu. Hiç yayın
// yoksa CurrentProblemPriority zaten varsayılan döner.
func LoadProblemPriorityWith(read func() (ProblemPriorityConfig, error)) ProblemPriorityConfig {
	c, err := read()
	if err != nil {
		if problemPriorityReadFailing.CompareAndSwap(false, true) {
			log.Printf("[settings] problem_priority okunamadı: %v — son yayınlanan değer korunuyor", err)
		}
		return CurrentProblemPriority()
	}
	if problemPriorityReadFailing.CompareAndSwap(true, false) {
		log.Printf("[settings] problem_priority yeniden okunabiliyor — kayıtlı değer yayınlandı")
	}
	SetProblemPriority(c)
	return CurrentProblemPriority()
}

// SaveProblemPriority persists the config under system_settings — yeni
// şema yok, her operatör ayarıyla aynı anahtar/değer tablosu.
func (s *Store) SaveProblemPriority(ctx context.Context, c ProblemPriorityConfig) error {
	// v0.10.1072 — normalize edilmiş hâli yazılır: boş istisna listesi `[]`
	// olarak (null DEĞİL) kalır, yoksa geri okumada nil = varsayılan olurdu.
	raw, err := json.Marshal(NormalizeProblemPriority(c))
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, problemPriorityKey, raw)
}
