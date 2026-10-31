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

// runInfo — bir olay kimliğinin kesintisiz koşusu.
type runInfo struct {
	first, last time.Time
	service     string
	detector    string
	signal      string
	ev          chstore.MLEvent // son yazılan hal (replay tam kaydı için)
	snap        snapshotState
}

// tracker — süreç-içi koşu durumu (rejim + teyit + started_at). Lider
// değişince sıfırlanır: koşu yeni pod'da baştan sayılır (kabul edilen sapma).
type tracker struct {
	mu   sync.Mutex
	runs map[string]*runInfo
}

func newTracker() *tracker { return &tracker{runs: map[string]*runInfo{}} }

// observe — olay bu tikte görüldü; kesinti (2 tik) varsa koşu yeniden başlar.
// Döner: koşu başlangıcı.
func (t *tracker) observe(ev chstore.MLEvent, now time.Time, tick time.Duration) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.runs[ev.ID]
	if !ok || now.Sub(r.last) > 2*tick {
		r = &runInfo{first: now}
		t.runs[ev.ID] = r
	}
	r.last, r.service, r.detector, r.signal = now, ev.Service, ev.Detector, ev.Signal
	return r.first
}

// confirmers — aynı servis, başka dedektör, son görülme ± pencere.
func (t *tracker) confirmers(ev chstore.MLEvent, now time.Time, win time.Duration) (ids []string, signals int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sig := map[string]bool{}
	for id, r := range t.runs {
		if id == ev.ID || r.service != ev.Service || r.detector == ev.Detector || now.Sub(r.last) > win {
			continue
		}
		ids = append(ids, id)
		if r.signal != ev.Signal {
			sig[r.signal] = true
		}
	}
	sort.Strings(ids)
	return ids, len(sig)
}

// setEvent — süzgeçten geçen olayın son hali.
func (t *tracker) setEvent(ev chstore.MLEvent) {
	t.mu.Lock()
	if r := t.runs[ev.ID]; r != nil {
		r.ev = ev
	}
	t.mu.Unlock()
}

// snap — koşunun replay durumu (yoksa nil). İşaretçi: yalnız lider
// goroutine'i yazar (recorder tek döngü).
func (t *tracker) snap(id string) *snapshotState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.runs[id]; r != nil {
		return &r.snap
	}
	return nil
}

// dueComplete — ön kaydı yazılmış, başından `span` geçmiş, tam kaydı yazılmamış koşular.
func (t *tracker) dueComplete(now time.Time, span time.Duration) []chstore.MLEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []chstore.MLEvent
	for _, r := range t.runs {
		if r.snap.pre && !r.snap.done && now.Sub(r.first) >= span {
			out = append(out, r.ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// gc — 1 günden eski koşular.
func (t *tracker) gc(now time.Time) {
	t.mu.Lock()
	for id, r := range t.runs {
		if now.Sub(r.last) > 24*time.Hour {
			delete(t.runs, id)
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
		if !impactOK(ev.Baseline, ev.Observed, q) {
			sup.Impact++
			continue
		}
		neg, pos := lb.Feedback(ev.Detector, ev.Service)
		if z := feedbackZ(cfg.CriticalZ, neg, pos, q); ev.Score < z {
			sup.Feedback++
			continue
		}
		first := tr.observe(ev, now, tick)
		if now.Sub(first) > time.Duration(q.RegimeHours)*time.Hour {
			sup.Regime++
			continue
		}
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
			"confirmScore": len(ids), "confirmSignals": sigs, "confirmedBy": firstN(ids, 5), "configHash": hash})
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
