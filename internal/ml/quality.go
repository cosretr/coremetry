package ml

// quality.go — kalite çerçevesi (üç sinyal ortak; operatör 2026-10-31).
// Dedektör olayları yazılmadan önce recorder tek süzgeçten geçirir:
//   - etki eşiği: |gözlenen − taban| küçükse istatistiksel sapma olay değil;
//   - geri bildirim: dedektör × servis "değil" kararları z eşiğini yükseltir;
//   - rejim değişimi: kesintisiz N saat süren sapma yeni normaldir, uzatılmaz;
//   - teyit skoru: aynı servis ± pencerede başka dedektör/sinyal olayı;
//   - config hash: olay hangi ayarla üretildi (replay / what-if).
// Taban hijyeni (gerçek anomali kovaları tabana girmez) ve eksik veri kuralı
// dedektör okumasında (Labels.Exclude, dwellWindow). Etiket seti ortak:
// chstore.MLLabels (sinyal fark etmez).

import (
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Labels — tik başına bir kez yüklenen etiket dizini. nil güvenli.
type Labels struct {
	all []chstore.MLLabel
	// pos — servis → gerçek anomali pencereleri (hijyen).
	pos map[string][][2]int64
	// fb — detector\x00service → (değil, gerçek) sayıları.
	fb map[string][2]int
}

// NewLabels — SAF.
func NewLabels(ls []chstore.MLLabel) *Labels {
	l := &Labels{all: ls, pos: map[string][][2]int64{}, fb: map[string][2]int{}}
	for _, x := range ls {
		k := x.Detector + "\x00" + x.Service
		c := l.fb[k]
		if x.Positive {
			l.pos[x.Service] = append(l.pos[x.Service], [2]int64{x.From, x.To})
			c[1]++
		} else {
			c[0]++
		}
		l.fb[k] = c
	}
	return l
}

// Exclude — MLBaselineRequest.Exclude; hijyen kapalıysa ya da etiket yoksa nil.
func (l *Labels) Exclude(cfg chstore.MLConfig) map[string][][2]int64 {
	if l == nil || cfg.Quality.HygieneOff || len(l.pos) == 0 {
		return nil
	}
	return l.pos
}

// Feedback — (değil, gerçek) karar sayıları.
func (l *Labels) Feedback(detector, service string) (neg, pos int) {
	if l == nil {
		return 0, 0
	}
	c := l.fb[detector+"\x00"+service]
	return c[0], c[1]
}

// All — backtest için ham liste.
func (l *Labels) All() []chstore.MLLabel {
	if l == nil {
		return nil
	}
	return l.all
}

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

// feedbackZ — SAF: geri bildirimle yükseltilmiş z eşiği. Net "değil" sayısı
// (değil − gerçek) eşik sayısına ulaşınca adım başına ZStep, tavan MaxBoost.
func feedbackZ(baseZ float64, neg, pos int, q chstore.MLQuality) float64 {
	if q.FeedbackOff || neg < q.FeedbackMinNegatives || neg <= pos {
		return baseZ
	}
	boost := q.FeedbackZStep * float64(neg-pos-q.FeedbackMinNegatives+1)
	return baseZ + math.Min(math.Max(boost, 0), q.FeedbackMaxBoost)
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
	fresh := r == nil ||
		(!r.closedAt.IsZero() && now.Sub(r.closedAt) > episodeReopenWindow) ||
		(r.closedAt.IsZero() && now.Sub(r.last) > time.Duration(episodeCloseMisses)*tick+episodeReopenWindow)
	if fresh {
		if r != nil {
			delete(t.byEp, r.epID)
		}
		r = &runInfo{first: now, baseID: ev.ID, epID: chstore.MLEpisodeID(ev.ID, now)}
		t.runs[ev.ID] = r
		t.byEp[r.epID] = ev.ID
	}
	r.closedAt = time.Time{}
	r.last, r.misses, r.service, r.detector, r.signal = now, 0, ev.Service, ev.Detector, ev.Signal
	return r.first, r.epID
}

func (t *tracker) byEpisode(epID string) *runInfo {
	if base, ok := t.byEp[epID]; ok {
		return t.runs[base]
	}
	return nil
}

// episodeOf — taban kimliğin güncel epizot kimliği (test).
func (t *tracker) episodeOf(baseID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.runs[baseID]; r != nil {
		return r.epID
	}
	return ""
}

// closeStale — bu tikte görülmeyen açık koşuların kaçırma sayısı artar;
// episodeCloseMisses'e ulaşan kapanır ve kapanış satırı (stage=closed,
// last_seen = son görülme) döner.
func (t *tracker) closeStale(now time.Time) []chstore.MLEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []chstore.MLEvent
	for _, r := range t.runs {
		if !r.closedAt.IsZero() || !r.last.Before(now) || r.ev.ID == "" {
			continue
		}
		r.misses++
		if r.misses >= episodeCloseMisses {
			r.closedAt = now
			ev := r.ev
			ev.Stage = chstore.MLStageClosed
			out = append(out, ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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

// setEvent — süzgeçten geçen olayın son hali (epizot kimliğiyle).
func (t *tracker) setEvent(ev chstore.MLEvent) {
	t.mu.Lock()
	if r := t.byEpisode(ev.ID); r != nil {
		r.ev = ev
	}
	t.mu.Unlock()
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

// suppression — bastırma sayaçları (self-obs).
type suppression struct{ Impact, Feedback, Regime int64 }

// qualityFilter — recorder'ın tek süzgeci. Sıra: etki → geri bildirim →
// koşu kaydı (rejim) → teyit. Bastırılan olay yazılmaz; RMT'de son satırı
// (last_seen) donar. Kalan olayın StartedAt'i koşu başı, kanıtına teyit +
// config hash eklenir.
func qualityFilter(evs []chstore.MLEvent, cfg chstore.MLConfig, lb *Labels, tr *tracker, now time.Time) ([]chstore.MLEvent, suppression) {
	q := cfg.Quality
	tick := time.Duration(cfg.TickSec) * time.Second
	var sup suppression
	kept := evs[:0]
	for _, ev := range evs {
		if ev.Stage == chstore.MLStageChronic { // kronik kayıt: tek satır, epizot/teyit yok
			kept = append(kept, ev)
			continue
		}
		if !impactOK(ev.Baseline, ev.Observed, q) {
			sup.Impact++
			continue
		}
		neg, pos := lb.Feedback(ev.Detector, ev.Service)
		if z := feedbackZ(cfg.CriticalZ, neg, pos, q); ev.Score < z {
			sup.Feedback++
			continue
		}
		first, epID := tr.observe(ev, now, tick)
		if now.Sub(first) > time.Duration(q.RegimeHours)*time.Hour {
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
