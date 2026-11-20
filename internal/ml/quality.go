package ml

// quality.go — kalite çerçevesi (üç sinyal ortak; operatör 2026-10-31).
// Dedektör olayları yazılmadan önce recorder tek süzgeçten geçirir:
//   - etki eşiği: |gözlenen − taban| küçükse istatistiksel sapma olay değil;
//   - geri bildirim: dedektör × servis "değil" kararları z eşiğini yükseltir;
//   - rejim değişimi: kesintisiz N saat süren sapma yeni normaldir, uzatılmaz;
//   - teyit skoru: aynı servis ± pencerede başka dedektör/sinyal olayı;
//   - config hash: olay hangi ayarla üretildi (replay / what-if).
// Eksik veri kuralı dedektör okumasında (dwellWindow). v0.10.2269: operatör
// kararları ("Gerçek anomali / Sorun değil") hiçbir çalışma zamanı yolunu
// sürmez — taban hijyeni (Labels.Exclude) kaldırıldı; kararlar yalnız karne
// ve backtest puanlaması içindir (vote_runtime_guard_test.go).

import (
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// dwellWindow — SAF: [start, last] kovaları (adım step). missingIsZero=false:
// eksik kova (VM scrape boşluğu) → ok=false, değerlendirme yok. Log'da ES
// histogramında satır olmaması gerçek sıfırdır (susma) → missingIsZero=true.
func dwellWindow(byB map[int64]float64, start, last, step int64, missingIsZero bool) ([]float64, bool) {
	var cur []float64
	for b := start; b <= last; b += step {
		v, ok := byB[b]
		if !ok && !missingIsZero {
			return nil, false
		}
		cur = append(cur, v)
	}
	return cur, true
}

// belowImpactFloor — SAF (v0.10.2041): olayın değeri ailenin mutlak etki
// tabanının altında mı. Bugün yalnız Kafka consumer lag (VM ve Thanos
// parçası): değer = dedektörün ölçüsü, yani en kötü partition lag'i (Thanos
// ve eski yol: 5 dk kovanın sonundaki okuma; VM etki kuralı v0.10.2382: en
// yeni ölçülen dakikanın okuması). Taban chstore.MLKafkaLagImpactFloor — etki
// kuralının mutlak tabanıyla AYNI sabit: kuralın açtığı yeni olay bu süzgece
// takılamaz (son ölçülen dakika ≥ eşik ≥ taban); uzatma tiki açık koşudur.
func belowImpactFloor(ev chstore.MLEvent) bool {
	if ev.Detector != "metric_kafka" || ev.Direction == "down" {
		return false
	}
	if _, id, _ := splitThanosPart(ev.Code); id != "consumer_lag" {
		return false
	}
	return ev.Observed < chstore.MLKafkaLagImpactFloor
}

// impactRuleRun — SAF: olayın koşusunu etki kuralı mı yönetiyor (dedektör
// açık koşuyu kural "temiz" diyene dek uzatır): servis hata oranı (I3), servis
// yanıt süresi (I4) ve kanıtı rule=impact damgalı consumer lag (I5). Thanos ve eski yol lag olayı değil.
func impactRuleRun(ev chstore.MLEvent) bool {
	if ev.Detector == detSvcErrorRate || ev.Detector == detSvcResponseTime { // yanıt süresi: v0.10.2385 (I4)
		return true
	}
	if !isConsumerLagEvent(ev) {
		return false
	}
	_, ok := impactMarkOf(ev.Evidence)
	return ok
}

// impactOK — SAF: etki eşiği. Taban 0 iken göreli eşik uygulanmaz.
func impactOK(baseline, observed float64, q chstore.MLQuality) bool {
	d := math.Abs(observed - baseline)
	if d < q.ImpactMinAbs {
		return false
	}
	if b := math.Abs(baseline); b > 0 && d/b < q.ImpactMinRel {
		return false
	}
	return true
}

// ── Epizot modeli (B3, v0.10.1768) ────────────────────────────────────────
// Olayın zamansız taban kimliği (dedektör × servis × bölüm) bir KOŞU
// açar; koşu art arda episodeCloseMisses tik görülmezse kapanır (satır
// stage=closed, last_seen = bitiş); kapanıştan episodeReopenWindow içinde
// yeniden görülürse aynı epizot yeniden açılır, aksi hâlde yeni epizot = yeni
// kimlik (MLEpisodeID: taban + epizot başlangıcı). Eski kimlik tekrar eden
// olayları üstüne yazıyordu (pod_outlier 12:10/12:30 gözlemi); artık her
// epizot kendi satırıdır, replay/karar/karne epizot başına.
const (
	episodeCloseMisses  = 3
	episodeReopenWindow = 30 * time.Minute
)

// runInfo — bir taban kimliğinin açık/kapalı koşusu.
type runInfo struct {
	first, last time.Time
	service     string
	detector    string
	signal      string
	baseID      string
	epID        string
	ev          chstore.MLEvent // son yazılan hal (replay / kapanış satırı)
	snap        snapshotState
	misses      int
	closedAt    time.Time // sıfır = açık
	// v0.10.2359 — veri-yok tutması: heldSince tutmanın başı (sıfır = yok),
	// heldLast son tutulan tik; noData = koşu "veri gelmedi" ile kapandı.
	heldSince, heldLast time.Time
	noData              bool
}

// ref — koşunun son canlılık anı: son görülme ya da son veri-yok tutması.
func (r *runInfo) ref() time.Time {
	if r.heldLast.After(r.last) {
		return r.heldLast
	}
	return r.last
}

// fresh — koşu artık sürmüyor: yeniden görülürse YENİ epizot açılır (kapanıştan
// ya da son canlılıktan beri yeniden açma penceresi geçti).
func (r *runInfo) fresh(now time.Time, tick time.Duration) bool {
	if !r.closedAt.IsZero() {
		return now.Sub(r.closedAt) > episodeReopenWindow
	}
	return now.Sub(r.ref()) > time.Duration(episodeCloseMisses)*tick+episodeReopenWindow
}

// tracker — süreç-içi koşu durumu (rejim + teyit + epizot). Lider değişince
// sıfırlanır: koşu yeni pod'da baştan sayılır (kabul edilen sapma).
type tracker struct {
	mu   sync.Mutex
	runs map[string]*runInfo // taban kimlik → koşu
	byEp map[string]string   // epizot kimliği → taban kimlik
}

func newTracker() *tracker { return &tracker{runs: map[string]*runInfo{}, byEp: map[string]string{}} }

// observe — olay (taban kimlikli) bu tikte görüldü. Döner: epizot başı ve
// epizot kimliği.
func (t *tracker) observe(ev chstore.MLEvent, now time.Time, tick time.Duration) (time.Time, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[ev.ID]
	if r == nil || r.fresh(now, tick) {
		if r != nil {
			delete(t.byEp, r.epID)
		}
		r = &runInfo{first: now, baseID: ev.ID, epID: chstore.MLEpisodeID(ev.ID, now)}
		t.runs[ev.ID] = r
		t.byEp[r.epID] = ev.ID
	}
	r.closedAt, r.heldSince, r.heldLast, r.noData = time.Time{}, time.Time{}, time.Time{}, false
	r.last, r.misses, r.service, r.detector, r.signal = now, 0, ev.Service, ev.Detector, ev.Signal
	return r.first, r.epID
}

func (t *tracker) byEpisode(epID string) *runInfo {
	if base, ok := t.byEp[epID]; ok {
		return t.runs[base]
	}
	return nil
}

// closeStale — bu tikte görülmeyen açık koşular. Ailesi bu tik veri
// değerlendiremeyen koşu (cov.noData) kaçırma saymaz, açık tutulur (held:
// tutmanın ilk tiki, kanıtı noDataSince ile yazılır); noDataHoldMax dolunca
// "veri gelmedi" (endReason) kapanır. Değerlendirilip görülmeyen koşunun
// kaçırması artar; episodeCloseMisses'e ulaşan "banda döndü" kapanır.
// closing: kapanış satırları (stage=closed, last_seen = son görülme).
func (t *tracker) closeStale(now time.Time, cov tickCoverage) (closing, held []chstore.MLEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.runs {
		if !r.closedAt.IsZero() || !r.last.Before(now) || r.ev.ID == "" {
			continue
		}
		if cov.noData(r.detector) || (cov.runBlind != nil && cov.runBlind(r.ev)) {
			if r.heldSince.IsZero() {
				r.heldSince = now
				r.ev.Evidence = mergeEvidence(r.ev.Evidence, map[string]any{"noDataSince": now.Unix()})
				held = append(held, r.ev)
			}
			r.heldLast = now
		} else {
			r.misses++
		}
		switch {
		case r.misses >= episodeCloseMisses:
		case !r.heldSince.IsZero() && now.Sub(r.last) >= noDataHoldMax:
			r.noData = true
		default:
			continue
		}
		r.closedAt = now
		ev := r.ev
		ev.Stage = chstore.MLStageClosed
		if r.noData {
			ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"endReason": EndReasonNoData})
		}
		closing = append(closing, ev)
	}
	sort.Slice(closing, func(i, j int) bool { return closing[i].ID < closing[j].ID })
	sort.Slice(held, func(i, j int) bool { return held[i].ID < held[j].ID })
	return closing, held
}

// noDataServices — "veri gelmedi" ile kapanmış koşusu olan ve açık koşusu
// olmayan servisler (ML Problem'i "bitti" diye kapatılmaz).
func (t *tracker) noDataServices() map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	out, open := map[string]bool{}, map[string]bool{}
	for _, r := range t.runs {
		if r.closedAt.IsZero() {
			open[r.service] = true
		} else if r.noData {
			out[r.service] = true
		}
	}
	for s := range open {
		delete(out, s)
	}
	return out
}

// confirmers — aynı servis, başka dedektör, son görülme ± pencere (açık koşular).
func (t *tracker) confirmers(ev chstore.MLEvent, now time.Time, win time.Duration) (ids []string, signals int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sig := map[string]bool{}
	for _, r := range t.runs {
		if r.epID == ev.ID || r.service != ev.Service || r.detector == ev.Detector || now.Sub(r.last) > win {
			continue
		}
		ids = append(ids, r.epID)
		if r.signal != ev.Signal {
			sig[r.signal] = true
		}
	}
	sort.Strings(ids)
	return ids, len(sig)
}

// firstOf — v0.10.2215 (H2): epizodun ilk görüldüğü tik (kayıt detected_at);
// bilinmiyorsa sıfır.
func (t *tracker) firstOf(epID string) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.byEpisode(epID); r != nil {
		return r.first
	}
	return time.Time{}
}

// setEvent — süzgeçten geçen olayın son hali (epizot kimliğiyle).
func (t *tracker) setEvent(ev chstore.MLEvent) {
	t.mu.Lock()
	if r := t.byEpisode(ev.ID); r != nil {
		r.ev = ev
	}
	t.mu.Unlock()
}

// openRun — v0.10.2041: taban kimliğin açık bir koşusu var mı (observe'un
// "taze değil" kuralının tersi; kapanmamış ve son görülme kapanış eşiğinin
// içinde).
func (t *tracker) openRun(baseID string, now time.Time, tick time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[baseID]
	return r != nil && r.closedAt.IsZero() && now.Sub(r.ref()) <= time.Duration(episodeCloseMisses)*tick
}

// seenAt — v0.10.2377 (I2): taban kimlik TAM bu tikte görüldü mü (observe
// now ile çağrıldı). Erken tikin günlük tavan iadesi buna bakar.
func (t *tracker) seenAt(baseID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[baseID]
	return r != nil && r.last.Equal(now)
}

// startedOf — v0.10.2373 (I3): taban kimliğin SÜREN epizodunun (açık ya da
// yeniden açılabilir; observe yeni epizot açmayacak) yazılmış başlangıcı,
// yoksa 0. Kayan pencereyle karar veren dedektör başlangıcı buradan taşır.
func (t *tracker) startedOf(baseID string, now time.Time, tick time.Duration) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.runs[baseID]
	if r == nil || r.ev.ID == "" || r.fresh(now, tick) {
		return 0
	}
	return r.ev.StartedAt
}

// prevEvent — v0.10.2037: epizodun önceki tikte yazılan hali (contrib kapısı).
func (t *tracker) prevEvent(epID string) (chstore.MLEvent, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.byEpisode(epID); r != nil && r.ev.ID != "" {
		return r.ev, true
	}
	return chstore.MLEvent{}, false
}

// events — v0.10.2015 (D3): koşuların son yazılan halleri (açık + 24 sa
// içinde kapanmış); gölge gruplamanın süreç-içi girdisi.
func (t *tracker) events() []chstore.MLEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]chstore.MLEvent, 0, len(t.runs))
	for _, r := range t.runs {
		if r.ev.ID != "" {
			out = append(out, r.ev)
		}
	}
	return out
}

// openIDs — v0.10.2052 (D4): koşusu açık (kapanmamış) epizot kimlikleri +
// bu tikin olayları. Grup "bitti" kararının girdisi (closeStale sonrası).
func (t *tracker) openIDs(kept []chstore.MLEvent) map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]bool, len(t.runs)+len(kept))
	for _, r := range t.runs {
		if r.closedAt.IsZero() && r.epID != "" {
			out[r.epID] = true
		}
	}
	for _, e := range kept {
		out[e.ID] = true
	}
	return out
}

// snap — koşunun replay durumu (yoksa nil); yalnız lider goroutine'i yazar.
func (t *tracker) snap(epID string) *snapshotState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.byEpisode(epID); r != nil {
		return &r.snap
	}
	return nil
}

// dueComplete — ön kaydı yazılmış, başından `span` geçmiş ya da kapanmış, tam kaydı yazılmamış koşular.
func (t *tracker) dueComplete(now time.Time, span time.Duration) []chstore.MLEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []chstore.MLEvent
	for _, r := range t.runs {
		if r.snap.pre && !r.snap.done && (now.Sub(r.first) >= span || !r.closedAt.IsZero()) {
			out = append(out, r.ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// gc — kapanıştan ya da son görülmeden 24 sa geçen koşular.
func (t *tracker) gc(now time.Time) {
	t.mu.Lock()
	for base, r := range t.runs {
		ref := r.last
		if !r.closedAt.IsZero() {
			ref = r.closedAt
		}
		if now.Sub(ref) > 24*time.Hour {
			delete(t.byEp, r.epID)
			delete(t.runs, base)
		}
	}
	t.mu.Unlock()
}

// regimeLimit — SAF: rejim bastırmasının süresi. v0.10.2242 (Dilim B, K15):
// telemetri hacmi kaydı rejim kuralından muaf ("<tarih>'ten beri" kartı
// yaşar), tavan 7 gün; diğerleri RegimeHours.
func regimeLimit(detector string, q chstore.MLQuality) time.Duration {
	if detector == detTelemetryVolume {
		return volMaxDuration
	}
	return time.Duration(q.RegimeHours) * time.Hour
}

// suppression — bastırma sayaçları (self-obs).
type suppression struct{ Impact, Regime int64 }

// qualityFilter — recorder'ın tek süzgeci. Sıra: etki → koşu kaydı
// (rejim) → teyit. Operatör kararları burada okunmaz (K3, v0.10.2218). Bastırılan olay yazılmaz; RMT'de son satırı
// (last_seen) donar. Kalan olayın StartedAt'i koşu başı, kanıtına teyit +
// config hash eklenir.
func qualityFilter(evs []chstore.MLEvent, cfg chstore.MLConfig, tr *tracker, now time.Time) ([]chstore.MLEvent, suppression) {
	q := cfg.Quality
	tick := time.Duration(cfg.TickSec) * time.Second
	var sup suppression
	kept := evs[:0]
	for _, ev := range evs {
		if ev.Stage == chstore.MLStageChronic { // kronik kayıt: tek satır, epizot/teyit yok
			kept = append(kept, ev)
			continue
		}
		// v0.10.2373 (I3): açık servis hata oranı koşusu, en yeni dakika tabana
		// dönse de etki kuralı "temiz" diyene dek sürer (dedektör uzatır); etki
		// eşiği yalnız YENİ olayı süzer (aşağıdaki lag tabanıyla aynı ilke).
		// v0.10.2382 (I5): etki kuralıyla açılmış consumer lag koşusu da öyle.
		if !impactOK(ev.Baseline, ev.Observed, q) && !(impactRuleRun(ev) && tr.openRun(ev.ID, now, tick)) {
			sup.Impact++
			continue
		}
		// v0.10.2041 — mutlak etki tabanı (Kafka lag < 1.000 kayıt): yalnız
		// YENİ olay; açık koşu tabanın altına inse de sürer.
		if belowImpactFloor(ev) && !tr.openRun(ev.ID, now, tick) {
			sup.Impact++
			continue
		}
		first, epID := tr.observe(ev, now, tick)
		if now.Sub(first) > regimeLimit(ev.Detector, q) {
			sup.Regime++
			continue
		}
		ev.ID = epID // epizot kimliği: her epizot kendi satırı
		// koşu başı; dedektör daha erken bir an verdiyse (kırılma noktası) o kalır
		if ev.StartedAt == 0 || first.UnixNano() < ev.StartedAt {
			ev.StartedAt = first.UnixNano()
		}
		kept = append(kept, ev)
	}
	hash := cfg.ConfigHash()
	win := time.Duration(q.ConfirmWindowMin) * time.Minute
	for i := range kept {
		ids, sigs := tr.confirmers(kept[i], now, win)
		kept[i].Evidence = mergeEvidence(kept[i].Evidence, map[string]any{
			"confirmScore": len(ids), "confirmSignals": sigs, "confirmedBy": firstN(ids, 5), "configHash": hash,
			// replay kaydı serisiz olaylarda (pod aykırısı) anlatım için (v0.10.1775)
			"observed": kept[i].Observed, "baseline": kept[i].Baseline, "pod": kept[i].Pod, "code": kept[i].Code, "direction": kept[i].Direction})
		tr.setEvent(kept[i])
	}
	return kept, sup
}

// mergeEvidence — SAF: dedektör kanıtı JSON'una alan ekler (bozuk kanıt = yeni nesne).
func mergeEvidence(raw string, add map[string]any) string {
	m := map[string]any{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	for k, v := range add {
		m[k] = v
	}
	out, _ := json.Marshal(m)
	return string(out)
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	if xs == nil {
		return []string{}
	}
	return xs
}
