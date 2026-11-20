package ml

// impact_probe.go — v0.10.2377 (etki olayı programı, dilim I2; operatör kararı
// 2026-11-19 "tek etki olayı Problem açsın"): "ucuz yoklama, erken tam tik".
//
// NEDEN: ML motorunun TEK döngüsü var ve tüm dedektörler cfg.TickSec'te
// (varsayılan 300 sn) bir koşar. Etki kuralı (I1/I3) 1 dakikalık örnekle karar
// verse de olay ancak bir sonraki 5 dakikalık tikte görülüyordu: başlangıçtan
// ~5–10 dk sonra, 3–4 dakikalık olay iki tikin arasına düşüp hiç görülmeyebilir.
//
// NE: zamanlı tikler arasında lider impactProbeInterval'da bir UCUZ yoklama
// yapar (impactProber; svc_error_rate: aile başına 2 VM aralık sorgusu + son
// tikin taban özeti, CH okuması yok; v0.10.2382 consumer lag, v0.10.2385
// servis yanıt süresi aynı arayüzle). Yoklama tek soruyu
// yanıtlar: etki kuralı ateşleyen VE açık koşusu olmayan bir özne var mı?
// Varsa recorder TEK bir tam tik'i hemen koşar ("erken tik"). Olay, kayıt,
// Problem ve bildirim mevcut TEK hattan çıkar: hat çatallanmaz, yoklama
// hiçbir şey yazmaz.
//
// SINIRLAR:
//   - Zamanlı takvim KAYMAZ: erken tik fazladandır, sonraki zamanlı tik kendi
//     saatinde koşar (tickLoop).
//   - Yoklama aralığı başına en çok bir erken tik.
//   - Erken tik açan aday impactProbeCooldown boyunca yeniden açamaz; tam tik
//     olayı yazmasa da (kalite süzgeci, günlük tavan). Yoksa düşen aday her
//     dakika tam tik koştururdu.
//   - Zamanlı tike impactProbeSkipBefore (15 sn) ya da daha az kaldıysa yoklama
//     atlanır; daha erken atlamak bir pencere bitişini değerlendirmeden bırakırdı.
//   - Yoklama hatası I3'ün 1 dk'lık sorgu hatası gibi YALITILIR: log, erken
//     tik yok, tik hatası yok.
//   - Erken tik, TİK sayan hiçbir şeyi ilerletmez (recorder.go tick: lider tik
//     sayacı, koşu kaçırma sayacı, yaygın yavaşlama ardışıklığı, günlük tavan).
//
// Aralık ve bekleme kod sabitidir (ayar değil; K2 emsali).

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// impactProbeInterval — yoklama aralığı (etki kuralının örnek adımı).
	impactProbeInterval = 60 * time.Second
	// impactProbeSkipBefore — zamanlı tik bu kadar yakınsa yoklama atlanır (tik
	// zaten aynı pencereyi değerlendirecek). Bir yoklama aralığı kadar erken
	// atlamak beş pencere bitişinden birini HİÇ değerlendirmezdi (tam 3 dakikalık
	// olay o boşluğa düşerse kaçardı); pay yalnız "arka arkaya iki tam tik"i önler.
	impactProbeSkipBefore = 15 * time.Second
	// impactProbeCooldown — erken tik açan adayın yeniden açabilmesi için bekleme.
	impactProbeCooldown = 15 * time.Minute
	// impactBaseMaxTicks — yoklamanın kullandığı taban özeti en çok bu kadar
	// tik yaşında olabilir (episodeCloseMisses ile aynı hoşgörü).
	impactBaseMaxTicks = 3
)

// impactCandidate — yoklamanın "etki kuralı şu an ateşliyor" dediği özne.
type impactCandidate struct {
	BaseID  string // koşu izleyicisinin taban kimliği (chstore.MLEventID)
	Service string
}

// impactProber — etki ölçüsünün ucuz yoklaması; dedektör uygular. Yeni ölçü
// (yanıt süresi, consumer lag) bu arayüzü uygulayarak eklenir; recorder ve
// döngü değişmez. Kurallar: CH okuması yok, yazım yok, sorgular dedektörün
// kendi backoff ailesi ve VM sayacıyla (vmRange / vmInstant).
type impactProber interface {
	probeOn(cfg chstore.MLConfig) bool
	// probeImpact — kuralı ŞU AN ateşleyen özneler. Hata = ölçülemedi (çağıran yalıtır).
	probeImpact(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]impactCandidate, error)
}

// impactProbersOf — dedektörün etki yoklamaları: kendisi (impactProber ise) +
// ek ölçüleri (v0.10.2385, I4: TraceRED'in yanıt süresi yoklaması kendi
// dedektör anahtarıyla ayrı sorulur; hata oranı yoklaması aynen).
func impactProbersOf(det Detector) []impactProber {
	var out []impactProber
	if p, ok := det.(impactProber); ok {
		out = append(out, p)
	}
	if m, ok := det.(interface{ moreProbers() []impactProber }); ok {
		out = append(out, m.moreProbers()...)
	}
	return out
}

// probeTriggers — SAF: erken tik isteyen adaylar = ateşleyen − açık koşusu
// olan − bekleme süresindeki. cool: taban kimlik → son erken tik anı.
func probeTriggers(cands []impactCandidate, open func(baseID string) bool, cool map[string]time.Time, now time.Time) []impactCandidate {
	var out []impactCandidate
	for _, c := range cands {
		if open != nil && open(c.BaseID) {
			continue
		}
		if at, ok := cool[c.BaseID]; ok && now.Sub(at) < impactProbeCooldown {
			continue
		}
		out = append(out, c)
	}
	return out
}

// probeImpact — dakikalık yoklama: erken tik gerekli mi? Yalnız ML açık, lider
// ve tablo hazırken (son tikin kabul kapısı). Sorgu hatası erken tik açmaz,
// tik hatası sayılmaz (yalnız log). Tetikleyen her aday beklemeye alınır.
func (r *Recorder) probeImpact(ctx context.Context, cfg chstore.MLConfig, leader bool, now time.Time) bool {
	if !cfg.Enabled || !leader || !r.ready {
		return false
	}
	tick := time.Duration(cfg.TickSec) * time.Second
	open := func(id string) bool { return r.runs.openRun(id, now, tick) }
	deps := Deps{VM: r.vm, Backoff: r.backoff, Background: ctx, ObserveVM: func(d time.Duration) { global.observeVM(d.Milliseconds()) }}
	pctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMs)*time.Millisecond)
	defer cancel()
	for id, at := range r.probeCool {
		if now.Sub(at) >= impactProbeCooldown {
			delete(r.probeCool, id)
		}
	}
	var hit []impactCandidate
	for _, det := range r.detectors {
		for _, p := range impactProbersOf(det) {
			if !p.probeOn(cfg) {
				continue
			}
			global.probes.Add(1)
			cands, err := p.probeImpact(pctx, deps, cfg, now)
			if err != nil {
				log.Printf("[ml] etki yoklaması düştü (%s; erken tik yok): %v", det.Name(), err)
				continue
			}
			hit = append(hit, probeTriggers(cands, open, r.probeCool, now)...)
		}
	}
	if len(hit) == 0 {
		return false
	}
	if r.probeCool == nil {
		r.probeCool = map[string]time.Time{}
	}
	names := make([]string, 0, len(hit))
	for _, c := range hit {
		r.probeCool[c.BaseID] = now
		names = append(names, c.Service)
	}
	log.Printf("[ml] etki yoklaması: %d yeni aday (%s) → erken tik", len(hit), truncRunes(strings.Join(names, ", "), 200))
	return true
}

// admit — tikin kabul kapıları + lider tik sayacı (store'a dokunmaz; döngü
// simülasyonu aynı kodu koşar). Lider tik sayacı yalnız ZAMANLI tikte artar:
// "lider ≥ N ardışık tik koşmadan Problem kapatma" kuralı (group_live.go)
// erken tiklerle hızlanmaz. Liderlik gittiyse sayaç erken tikte de sıfırlanır.
func (r *Recorder) admit(cfg chstore.MLConfig, leader, ready bool, err error, early bool) bool {
	r.ready = err == nil && ready
	if !cfg.Enabled { // ana anahtar kapalı: ES'e HİÇ sorgu yok
		global.skipped.Add(1)
		return false
	}
	if err != nil || !ready {
		global.skipped.Add(1)
		if err != nil {
			global.setError(err)
			log.Printf("[ml] table probe failed: %v (tik atlandı)", err)
		} else {
			log.Printf("[ml] ml_events tablosu yok — api/worker boot'u DDL'i indirene dek bekleniyor")
		}
		return false
	}
	if !leader {
		r.leaderTicks = 0
		global.skipped.Add(1)
		return false
	}
	if !early {
		r.leaderTicks++
	}
	return true
}

// tickLoop — recorder döngüsünün iskeleti; dış dünya (saat, bekleme, ayar, tik,
// yoklama) kancalardan gelir, test sahte saatle koşturur.
type tickLoop struct {
	now   func() time.Time
	wait  func(d time.Duration) bool // false = context bitti
	cfg   func() chstore.MLConfig    // zamanlı tur başına bir kez
	tick  func(cfg chstore.MLConfig, early bool)
	probe func(cfg chstore.MLConfig, now time.Time) bool // true = erken tik
}

// run — zamanlı tik → cfg.TickSec bekleme (eski döngünün aynısı); bekleme
// impactProbeInterval dilimlerine bölünür ve her dilimin sonunda yoklanır.
// Sonraki zamanlı tikin anı (due) erken tikten ETKİLENMEZ. Zamanlı tike
// impactProbeSkipBefore ya da daha az kaldıysa yoklama atlanır (tik zaten geliyor).
// Her yoklamadan önce tam bir aralık beklendiği için aralık başına en çok bir
// erken tik koşar. Yoklama ayarı zamanlı turun ayarıdır (yoklama CH okumaz).
func (l tickLoop) run() {
	for {
		cfg := l.cfg()
		l.tick(cfg, false)
		due := l.now().Add(time.Duration(cfg.TickSec) * time.Second)
		for {
			left := due.Sub(l.now())
			if left <= 0 {
				break
			}
			if left <= impactProbeInterval {
				if !l.wait(left) {
					return
				}
				break
			}
			if !l.wait(impactProbeInterval) {
				return
			}
			now := l.now()
			if due.Sub(now) <= impactProbeSkipBefore {
				continue
			}
			if l.probe(cfg, now) {
				l.tick(cfg, true)
			}
		}
	}
}
