package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// subject.go — Oracle Aşama 3 dilim C (v0.10.896; spec Onay 2026-09-23,
// audit §6.2 "melez özne + açılışta sabitleme"): dış tarayıcının Subject
// çözücüsü. Yalnız AÇILIŞTA çağrılır (external.go, v0.10.894); sıra:
//
//	① TRACE'TEN: bu poll'un satırlarındaki trace id'ler tek CH sorgusuyla
//	   çözülür (chstore.TraceFactsByIDs — hata veren en derin span'ın servisi,
//	   v0.10.892 kuralı; ≤200 id, öncelik haritada olmayan operasyonlar).
//	   Operasyonun bu tikteki çoğunluk servisi (≥%50) → "trace'ten (v/n)".
//	② ÖĞRENİLMİŞ: op kodu → servis haritası (system_settings
//	   `oracle_opsvc:<kaynak>`; PollState emsali). Girdi ancak ≥3 teyit ve
//	   ≥%70 çoğunlukla "onaylı"; 30 gün teyitsiz düşer; 2000 girdi tavanı
//	   (en eski kullanılan düşer). Sabitlemeden önce servis son 24 saatte
//	   CANLI mı (spans) — ölü link yazılmaz (problemSubject.ts dersi).
//	③ BİLİNMİYOR: boş servis → sentetik ext: özne + "operasyon seviyesi —
//	   servis bilinmiyor" (çok servisli op'ta "çok servisli operasyon").
//
// Trace bulunursa her zaman trace kazanır ve harita teyit sayılır; harita
// yalnız trace'siz açılışlarda 2. basamaktır. Açık Problem'e dokunulmaz
// (sabitleme dış hatta).

const (
	learnedKeyPrefix  = "oracle_opsvc:"
	learnedMaxEntries = 2000
	learnedTTL        = 30 * 24 * time.Hour
	learnedMinHits    = 3
	learnedMinShare   = 0.70
	subjectLookupIDs  = 200
	subjectLookupPad  = 5 * time.Minute
	subjectAliveTTL   = 5 * time.Minute
	subjectAliveSince = 24 * time.Hour
)

// TraceLookup — chstore.Store.TraceFactsByIDs.
type TraceLookup func(ctx context.Context, ids []string, from, to time.Time) (map[string]chstore.TraceFact, error)

// AliveCheck — son 24 saatte span üreten servisler (chstore.ListActiveServiceNames).
type AliveCheck func(ctx context.Context, since time.Duration) ([]string, error)

// LearnedEntry — bir operasyon kodunun öğrenilmiş servisi.
type LearnedEntry struct {
	Service       string `json:"service"`
	Hits          int    `json:"hits"`  // seçili servise oy
	Total         int    `json:"total"` // tüm oylar
	FirstSeen     int64  `json:"firstSeen"`
	LastConfirmed int64  `json:"lastConfirmed"`
	LastUsed      int64  `json:"lastUsed,omitempty"`
}

// Confirmed — SAF: ≥3 teyit, ≥%70 pay, TTL içinde.
func (e *LearnedEntry) Confirmed(now time.Time) bool {
	if e == nil || e.Service == "" || e.Hits < learnedMinHits || e.Total == 0 {
		return false
	}
	if float64(e.Hits)/float64(e.Total) < learnedMinShare {
		return false
	}
	return now.Sub(time.Unix(0, e.LastConfirmed)) <= learnedTTL
}

// LearnedMap — kaynak başına blob.
type LearnedMap struct {
	V       int                      `json:"v"`
	Entries map[string]*LearnedEntry `json:"entries"`
}

// tickFacts — bu poll'dan çözülen gerçekler (Resolve bunları okur).
type tickFacts struct {
	votes  map[string]map[string]int // op → servis → oy
	totals map[string]int            // op → oy
	looked int
	found  int
}

// ObserveResult — Observe özeti (log/istatistik).
type ObserveResult struct {
	Rows, WithTrace, Looked, Found int
	Learned                        int // bu tikte teyit/eklenen op sayısı
	Error                          string
}

// SubjectResolver — poller tiki tek goroutine; mu API okumaları için.
type SubjectResolver struct {
	state  StateStore
	lookup TraceLookup
	alive  AliveCheck
	now    func() time.Time

	mu       sync.Mutex
	maps     map[string]*LearnedMap
	loadedAt map[string]time.Time // v0.10.897 — API'den sıfırlama görünsün diye 5 dk'da bir yeniden okunur
	dirty    map[string]bool
	tick     map[string]*tickFacts
	aliveSet map[string]bool
	aliveAt  time.Time
	aliveErr bool
}

func NewSubjectResolver(state StateStore, lookup TraceLookup, alive AliveCheck) *SubjectResolver {
	return &SubjectResolver{state: state, lookup: lookup, alive: alive, now: time.Now,
		maps: map[string]*LearnedMap{}, loadedAt: map[string]time.Time{}, dirty: map[string]bool{}, tick: map[string]*tickFacts{}}
}

const learnedReloadEvery = 5 * time.Minute

// learnedKey — system_settings anahtarı.
func learnedKey(sourceID string) string { return learnedKeyPrefix + sourceID }

func (r *SubjectResolver) mapFor(ctx context.Context, sourceID string) *LearnedMap {
	if m, ok := r.maps[sourceID]; ok && r.now().Sub(r.loadedAt[sourceID]) < learnedReloadEvery {
		return m
	}
	m := &LearnedMap{V: 1, Entries: map[string]*LearnedEntry{}}
	r.loadedAt[sourceID] = r.now()
	if r.state != nil {
		if raw, err := r.state.GetSetting(ctx, learnedKey(sourceID)); err == nil && len(raw) > 0 {
			var loaded LearnedMap
			if json.Unmarshal(raw, &loaded) == nil && loaded.Entries != nil {
				m = &loaded
			}
		}
	}
	r.maps[sourceID] = m
	return m
}

// pruneLearned — SAF: TTL dışı girdiler düşer; tavan aşılırsa en eski kullanılan/teyitli düşer.
func pruneLearned(m *LearnedMap, now time.Time) int {
	dropped := 0
	for op, e := range m.Entries {
		if now.Sub(time.Unix(0, e.LastConfirmed)) > learnedTTL {
			delete(m.Entries, op)
			dropped++
		}
	}
	if len(m.Entries) > learnedMaxEntries {
		type oe struct {
			op string
			at int64
		}
		order := make([]oe, 0, len(m.Entries))
		for op, e := range m.Entries {
			at := e.LastUsed
			if e.LastConfirmed > at {
				at = e.LastConfirmed
			}
			order = append(order, oe{op, at})
		}
		sort.Slice(order, func(i, j int) bool { return order[i].at < order[j].at })
		for _, o := range order[:len(m.Entries)-learnedMaxEntries] {
			delete(m.Entries, o.op)
			dropped++
		}
	}
	return dropped
}

// candidateIDs — SAF: ≤max trace id; önce haritada onaylı olmayan op'lardan
// birer id (öğrenme), sonra kalanlar (satır sırası). Tekrarsız.
func candidateIDs(rows []chstore.OracleErrorRow, m *LearnedMap, now time.Time, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, max)
	add := func(id string) bool {
		if id == "" || seen[id] || len(out) >= max {
			return false
		}
		seen[id] = true
		out = append(out, id)
		return true
	}
	opDone := map[string]bool{}
	for _, r := range rows {
		if r.TraceID == "" || opDone[r.OperationCode] {
			continue
		}
		if e := m.Entries[r.OperationCode]; e != nil && e.Confirmed(now) {
			continue
		}
		if add(r.TraceID) {
			opDone[r.OperationCode] = true
		}
	}
	for _, r := range rows {
		if len(out) >= max {
			break
		}
		add(r.TraceID)
	}
	return out
}

// Observe — poll sonrası: trace'leri çözer, bu tikin oylarını tutar, haritayı
// günceller ve değişince yazar. Hata poll'u düşürmez.
func (r *SubjectResolver) Observe(ctx context.Context, src SourceConfig, rows []chstore.OracleErrorRow, from, to time.Time) ObserveResult {
	res := ObserveResult{Rows: len(rows)}
	if r == nil || src.ID == "" {
		return res
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.mapFor(ctx, src.ID)
	tf := &tickFacts{votes: map[string]map[string]int{}, totals: map[string]int{}}
	r.tick[src.ID] = tf
	for _, row := range rows {
		if row.TraceID != "" {
			res.WithTrace++
		}
	}
	ids := candidateIDs(rows, m, now, subjectLookupIDs)
	tf.looked = len(ids)
	res.Looked = len(ids)
	if len(ids) > 0 && r.lookup != nil {
		facts, err := r.lookup(ctx, ids, from.Add(-subjectLookupPad), to.Add(subjectLookupPad))
		if err != nil {
			res.Error = "trace araması: " + err.Error()
			log.Printf("[oracle/subject] %s: %s", src.Name, res.Error)
		} else {
			for _, row := range rows {
				f, ok := facts[row.TraceID]
				if !ok || f.Service == "" {
					continue
				}
				if tf.votes[row.OperationCode] == nil {
					tf.votes[row.OperationCode] = map[string]int{}
				}
				tf.votes[row.OperationCode][f.Service]++
				tf.totals[row.OperationCode]++
			}
			tf.found = len(facts)
			res.Found = len(facts)
		}
	}
	// Harita güncellemesi: op başına bu tikin çoğunluğu.
	changed := false
	for op, byS := range tf.votes {
		best, bestN, total := "", 0, tf.totals[op]
		for s, n := range byS {
			if n > bestN || (n == bestN && s < best) {
				best, bestN = s, n
			}
		}
		if best == "" {
			continue
		}
		e := m.Entries[op]
		switch {
		case e == nil:
			m.Entries[op] = &LearnedEntry{Service: best, Hits: bestN, Total: total, FirstSeen: now.UnixNano(), LastConfirmed: now.UnixNano()}
			changed = true
			res.Learned++
		case e.Service == best:
			e.Hits += bestN
			e.Total += total
			e.LastConfirmed = now.UnixNano()
			changed = true
			res.Learned++
		default:
			// Farklı servis: yalnız bu tikte tek başına onay eşiğini geçiyorsa döner
			// (≥3 oy ve ≥%70); aksi hâlde girdi zayıflar (Total artar, Hits değil).
			if bestN >= learnedMinHits && float64(bestN)/float64(total) >= learnedMinShare {
				log.Printf("[oracle/subject] %s: %s → %s (eskisi %s, %d/%d)", src.Name, op, best, e.Service, bestN, total)
				m.Entries[op] = &LearnedEntry{Service: best, Hits: bestN, Total: total, FirstSeen: e.FirstSeen, LastConfirmed: now.UnixNano()}
			} else {
				e.Total += total
			}
			changed = true
		}
	}
	if pruneLearned(m, now) > 0 {
		changed = true
	}
	if changed {
		r.save(ctx, src.ID, m)
	}
	return res
}

func (r *SubjectResolver) save(ctx context.Context, sourceID string, m *LearnedMap) {
	if r.state == nil {
		return
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := r.state.PutSetting(ctx, learnedKey(sourceID), raw); err != nil {
		log.Printf("[oracle/subject] %s: öğrenilmiş harita yazılamadı: %v", sourceID, err)
	}
}

// isAlive — 5 dk önbellekli canlılık kümesi; okunamazsa canlı varsayılır
// (yön bilinçli: geçici CH hıçkırığı öğrenilmiş özneyi susturmasın).
func (r *SubjectResolver) isAlive(ctx context.Context, service string) bool {
	if r.alive == nil {
		return true
	}
	now := r.now()
	if r.aliveSet == nil || now.Sub(r.aliveAt) > subjectAliveTTL {
		names, err := r.alive(ctx, subjectAliveSince)
		if err != nil {
			if !r.aliveErr {
				log.Printf("[oracle/subject] canlı servis listesi okunamadı, canlı varsayılıyor: %v", err)
			}
			r.aliveErr = true
			return true
		}
		r.aliveErr = false
		r.aliveSet = make(map[string]bool, len(names))
		for _, n := range names {
			r.aliveSet[n] = true
		}
		r.aliveAt = now
	}
	return r.aliveSet[service]
}

// Resolve — ExternalTarget.Subject gövdesi; values[0] = operasyon kodu.
func (r *SubjectResolver) Resolve(ctx context.Context, sourceID string, values []string) anomaly.ExternalSubjectResolution {
	if r == nil || len(values) == 0 || values[0] == "" {
		return anomaly.ExternalSubjectResolution{Note: "operasyon seviyesi — servis bilinmiyor"}
	}
	op := values[0]
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	// ① trace'ten (bu tik)
	if tf := r.tick[sourceID]; tf != nil {
		if total := tf.totals[op]; total > 0 {
			best, bestN := "", 0
			for s, n := range tf.votes[op] {
				if n > bestN || (n == bestN && s < best) {
					best, bestN = s, n
				}
			}
			if best != "" && float64(bestN)/float64(total) >= 0.5 {
				if e := r.mapFor(ctx, sourceID).Entries[op]; e != nil && e.Service == best {
					e.LastUsed = now.UnixNano()
				}
				return anomaly.ExternalSubjectResolution{Service: best, Source: "trace", Note: fmt.Sprintf("trace'ten (%d/%d)", bestN, total)}
			}
			return anomaly.ExternalSubjectResolution{Note: fmt.Sprintf("çok servisli operasyon (%d trace, çoğunluk yok)", total)}
		}
	}
	// ② öğrenilmiş
	m := r.mapFor(ctx, sourceID)
	if e := m.Entries[op]; e != nil {
		if e.Confirmed(now) {
			if r.isAlive(ctx, e.Service) {
				e.LastUsed = now.UnixNano()
				r.save(ctx, sourceID, m)
				ago := now.Sub(time.Unix(0, e.LastConfirmed)).Round(time.Minute)
				return anomaly.ExternalSubjectResolution{Service: e.Service, Source: "learned",
					Note: fmt.Sprintf("öğrenilmiş eşleme %s→%s (%d/%d, son teyit %s önce)", op, e.Service, e.Hits, e.Total, ago)}
			}
			return anomaly.ExternalSubjectResolution{Note: fmt.Sprintf("öğrenilmiş servis %s son 24 saatte canlı değil — servis bilinmiyor", e.Service)}
		}
		return anomaly.ExternalSubjectResolution{Note: fmt.Sprintf("öğrenilmiş eşleme henüz onaysız (%d/%d) — servis bilinmiyor", e.Hits, e.Total)}
	}
	return anomaly.ExternalSubjectResolution{Note: "operasyon seviyesi — servis bilinmiyor"}
}

// ResolveFor — kaynağa bağlı çözücü (ExternalTarget.Subject).
func (r *SubjectResolver) ResolveFor(sourceID string) func(ctx context.Context, values []string) anomaly.ExternalSubjectResolution {
	return func(ctx context.Context, values []string) anomaly.ExternalSubjectResolution {
		return r.Resolve(ctx, sourceID, values)
	}
}

// Learned — kaynağın haritası (kopya; salt okuma API'si için).
func (r *SubjectResolver) Learned(ctx context.Context, sourceID string) LearnedMap {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.mapFor(ctx, sourceID)
	out := LearnedMap{V: m.V, Entries: make(map[string]*LearnedEntry, len(m.Entries))}
	for op, e := range m.Entries {
		c := *e
		out.Entries[op] = &c
	}
	return out
}

// Reset — haritayı siler (kaynak silinince / operatör sıfırlayınca).
func (r *SubjectResolver) Reset(ctx context.Context, sourceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.maps, sourceID)
	delete(r.tick, sourceID)
	if r.state != nil {
		_ = r.state.PutSetting(ctx, learnedKey(sourceID), []byte(`{"v":1,"entries":{}}`))
	}
}
