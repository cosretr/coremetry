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
//	①b POD ADINDAN (v0.10.908, operatör önerisi): satırın instance kolonu
//	   (pod adı) → ReplicaSet/pod eki atılır, "…-prod" öneki denenir
//	   (podservice.go); yalnız son 24 saatte CANLI servis adı kabul edilir.
//	   Trace'siz op'ta çoğunluk (≥%50) → "pod adından (v/n)"; haritayı da
//	   besler (trace oyu olmayan op'ta).
//	②b FONKSİYON KODUNDAN (v0.10.1000, kaynak ayarı functionCodeMatch;
//	   fncode.go): satırın kod alanı span'lerdeki FUNCTION_CODE ile aynı
//	   değerse, o kodu taşıyan span'lerin servisi. Önceki basamaklar servis
//	   bulamadığında devreye girer; trace/pod oyu olmayan op'ta haritayı da
//	   besler (zamanla "öğrenilmiş"e döner).
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
	// Alt / AltHits — v0.10.1000: meydan okuyan servis ve ardışık oyları.
	// Mevcut servis hiç oy almazken aynı servis tik çoğunluğunu ≥3 oy
	// biriktirirse girdi ona döner (tek tikte 3 oy şartı, tik başına 1-2 oy
	// veren pod / fonksiyon kodu kanıtında hiç sağlanmıyordu → girdi 30 gün
	// "onaysız" kalıyordu). Mevcut servis oy alırsa sayaç sıfırlanır.
	Alt     string `json:"alt,omitempty"`
	AltHits int    `json:"altHits,omitempty"`
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
	votes   map[string]map[string]int // op → servis → oy
	totals  map[string]int            // op → oy
	exTypes map[string]string         // v0.10.899 — trace id → exception tipi (jenerik kod qualifier'ı)
	// v0.10.908 — pod adından türetilmiş servis oyları (op → servis → oy; (op,pod) başına bir oy).
	podVotes  map[string]map[string]int
	podTotals map[string]int
	// v0.10.1000 — fonksiyon kodu: op → bu tikte görülen kodlar; taze okunan
	// kodlardan öğrenme oyları (op → servis → oy; (op, kod) başına bir oy).
	opCodes  map[string][]string
	fnVotes  map[string]map[string]int
	fnTotals map[string]int
	looked   int
	found    int
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
	lastRes  map[string]anomaly.ExternalSubjectResolution // v0.10.898 — kanıt notu için (kaynak\x00op)
	aliveSet map[string]bool
	aliveAt  time.Time
	aliveErr bool

	// v0.10.1000 — fonksiyon kodu basamağı (fncode.go).
	fnLookup FunctionCodeLookup
	fnFacts  map[string]map[string]fnFact // kaynak → kod → servis dağılımı
	fnOn     map[string]bool              // kaynak ayarı açık (son Observe)
	fnPath   map[string]string            // son okuma yolu ("" = yok)
	fnErr    map[string]bool              // arama hatası bir kez loglansın
}

func NewSubjectResolver(state StateStore, lookup TraceLookup, alive AliveCheck) *SubjectResolver {
	return &SubjectResolver{state: state, lookup: lookup, alive: alive, now: time.Now,
		maps: map[string]*LearnedMap{}, loadedAt: map[string]time.Time{}, dirty: map[string]bool{}, tick: map[string]*tickFacts{},
		lastRes: map[string]anomaly.ExternalSubjectResolution{},
		fnFacts: map[string]map[string]fnFact{}, fnOn: map[string]bool{}, fnPath: map[string]string{}, fnErr: map[string]bool{}}
}

// ExTypeFor — v0.10.899: bu poll'da çözülen trace'in exception tipi ("" = yok).
func (r *SubjectResolver) ExTypeFor(sourceID string) func(traceID string) string {
	return func(traceID string) string {
		r.mu.Lock()
		defer r.mu.Unlock()
		if tf := r.tick[sourceID]; tf != nil {
			return tf.exTypes[traceID]
		}
		return ""
	}
}

// LastResolution — v0.10.898: (kaynak, op) için son çözüm (kanıt cümlesi).
func (r *SubjectResolver) LastResolution(sourceID, op string) (anomaly.ExternalSubjectResolution, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.lastRes[sourceID+"\x00"+op]
	return res, ok
}

// LastResolutionFor — v0.10.1000: SERİNİN (op, kod) son çözümü; yoksa op'unki.
// Fonksiyon kodu basamağında aynı operasyonun farklı kodları farklı servise
// çözülebilir — kanıt notu başka serinin cümlesini taşımasın.
func (r *SubjectResolver) LastResolutionFor(sourceID, op, code string) (anomaly.ExternalSubjectResolution, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if res, ok := r.lastRes[sourceID+"\x00"+op+"\x00"+code]; ok {
		return res, true
	}
	res, ok := r.lastRes[sourceID+"\x00"+op]
	return res, ok
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
	// v0.10.900 — her poll'da blob YENİDEN okunur (küçük; API'den sıfırlama
	// bellekteki eski kopyayla geri yazılmasın — read-modify-write).
	delete(r.loadedAt, src.ID)
	m := r.mapFor(ctx, src.ID)
	tf := &tickFacts{votes: map[string]map[string]int{}, totals: map[string]int{}, exTypes: map[string]string{},
		podVotes: map[string]map[string]int{}, podTotals: map[string]int{},
		opCodes: map[string][]string{}, fnVotes: map[string]map[string]int{}, fnTotals: map[string]int{}}
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
		lo, hi := lookupWindow(rows, from, to, subjectLookupPad) // v0.10.917
		facts, err := r.lookup(ctx, ids, lo, hi)
		if err != nil {
			res.Error = "trace araması: " + err.Error()
			log.Printf("[oracle/subject] %s: %s", src.Name, res.Error)
		} else {
			for id, f := range facts {
				if f.ExType != "" {
					tf.exTypes[id] = f.ExType
				}
			}
			// v0.10.900 — oy TRACE başına: aynı trace'in birden çok satırı (tek istek
			// birkaç hata satırı loglar) tek oy; yoksa tek trace "üç teyit" olurdu.
			voted := map[string]bool{}
			for _, row := range rows {
				f, ok := facts[row.TraceID]
				if !ok || f.Service == "" {
					continue
				}
				vk := row.OperationCode + "\x00" + row.TraceID
				if voted[vk] {
					continue
				}
				voted[vk] = true
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
	// v0.10.908 — pod adından oylar: (op, pod) başına bir oy; yalnız doğrulanmış
	// canlı servis adı (canlılık listesi okunamazsa pod oyu YOK — uydurma yok).
	if alive := r.aliveSetVerified(ctx); len(alive) > 0 {
		podSvc := map[string]string{}
		voted := map[string]bool{}
		for _, row := range rows {
			if row.InstanceID == "" {
				continue
			}
			vk := row.OperationCode + "\x00" + row.InstanceID
			if voted[vk] {
				continue
			}
			voted[vk] = true
			svc, ok := podSvc[row.InstanceID]
			if !ok {
				svc = podService(row.InstanceID, alive)
				podSvc[row.InstanceID] = svc
			}
			if svc == "" {
				continue
			}
			if tf.podVotes[row.OperationCode] == nil {
				tf.podVotes[row.OperationCode] = map[string]int{}
			}
			tf.podVotes[row.OperationCode][svc]++
			tf.podTotals[row.OperationCode]++
		}
	}
	// v0.10.1000 — fonksiyon kodundan oylar (kaynak ayarı açıksa; fncode.go).
	if msg := r.observeFunctionCodes(ctx, src, rows, to, tf, now); msg != "" && res.Error == "" {
		res.Error = msg
	}
	// Harita güncellemesi: op başına bu tikin çoğunluğu (trace oyu yoksa pod
	// oyu, o da yoksa fonksiyon kodu oyu).
	learnVotes, learnTotals := tf.votes, tf.totals
	if len(tf.podVotes)+len(tf.fnVotes) > 0 {
		learnVotes = make(map[string]map[string]int, len(tf.votes)+len(tf.podVotes)+len(tf.fnVotes))
		learnTotals = make(map[string]int, len(tf.totals)+len(tf.podTotals)+len(tf.fnTotals))
		for op, v := range tf.votes {
			learnVotes[op], learnTotals[op] = v, tf.totals[op]
		}
		for op, v := range tf.podVotes {
			if _, has := learnVotes[op]; !has {
				learnVotes[op], learnTotals[op] = v, tf.podTotals[op]
			}
		}
		for op, v := range tf.fnVotes {
			if _, has := learnVotes[op]; !has {
				learnVotes[op], learnTotals[op] = v, tf.fnTotals[op]
			}
		}
	}
	changed := false
	for op, byS := range learnVotes {
		best, bestN, total := "", 0, learnTotals[op]
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
			e.Alt, e.AltHits = "", 0
			changed = true
			res.Learned++
		default:
			// Farklı servis çoğunlukta: yalnız bu tikte tek başına onay eşiğini
			// geçiyorsa (≥3 oy ve ≥%70) girdi döner. Aksi hâlde MEVCUT servise
			// giden oylar da sayılır (v0.10.900: karışık tikte girdi haksız
			// düşmesin), pay Total üzerinden doğal olarak azalır.
			if bestN >= learnedMinHits && float64(bestN)/float64(total) >= learnedMinShare {
				log.Printf("[oracle/subject] %s: %s → %s (eskisi %s, %d/%d)", src.Name, op, best, e.Service, bestN, total)
				m.Entries[op] = &LearnedEntry{Service: best, Hits: bestN, Total: total, FirstSeen: e.FirstSeen, LastConfirmed: now.UnixNano()}
			} else if mine := byS[e.Service]; mine > 0 {
				e.Hits += mine
				e.LastConfirmed = now.UnixNano()
				e.Total += total
				e.Alt, e.AltHits = "", 0
			} else {
				// v0.10.1000 — mevcut servis bu tikte HİÇ oy almadı: meydan
				// okuyan birikir; ≥3 ardışık oyda girdi döner (LearnedEntry.Alt).
				if e.Alt == best {
					e.AltHits += bestN
				} else {
					e.Alt, e.AltHits = best, bestN
				}
				if e.AltHits >= learnedMinHits {
					log.Printf("[oracle/subject] %s: %s → %s (eskisi %s, %d ardışık oy)", src.Name, op, best, e.Service, e.AltHits)
					m.Entries[op] = &LearnedEntry{Service: best, Hits: e.AltHits, Total: e.AltHits, FirstSeen: e.FirstSeen, LastConfirmed: now.UnixNano()}
				} else {
					e.Total += total
				}
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
	set := r.aliveSetVerified(ctx)
	if set == nil {
		return true
	}
	return set[service]
}

// aliveSetVerified — v0.10.908: canlı servis kümesi; OKUNAMAZSA nil (isAlive
// o durumda canlı varsayar, pod türetmesi ise HİÇ oy vermez — bir adı
// doğrulamadan servis ilan etmek uydurma olurdu).
func (r *SubjectResolver) aliveSetVerified(ctx context.Context) map[string]bool {
	if r.alive == nil {
		return nil
	}
	now := r.now()
	if r.aliveSet == nil || now.Sub(r.aliveAt) > subjectAliveTTL {
		names, err := r.alive(ctx, subjectAliveSince)
		if err != nil {
			if !r.aliveErr {
				log.Printf("[oracle/subject] canlı servis listesi okunamadı, canlı varsayılıyor: %v", err)
			}
			r.aliveErr = true
			return nil
		}
		r.aliveErr = false
		r.aliveSet = make(map[string]bool, len(names))
		for _, n := range names {
			r.aliveSet[n] = true
		}
		r.aliveAt = now
	}
	if r.aliveErr {
		return nil
	}
	return r.aliveSet
}

// Resolve — ExternalTarget.Subject gövdesi; values[0] = operasyon kodu,
// values[1] = hata/fonksiyon kodu (CounterGroupBy sırası).
func (r *SubjectResolver) Resolve(ctx context.Context, sourceID string, values []string) anomaly.ExternalSubjectResolution {
	if r == nil {
		return anomaly.ExternalSubjectResolution{Note: "operasyon seviyesi — servis bilinmiyor"}
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.resolveOp(ctx, sourceID, values, now)
	if res.Service != "" {
		return res
	}
	// ②b fonksiyon kodundan (v0.10.1000) — yalnız önceki basamaklar servis
	// bulamadıysa; çözülen özneler bu basamaktan ETKİLENMEZ.
	fn := r.resolveByFunctionCode(sourceID, values, now)
	if fn.Service != "" {
		return fn
	}
	if fn.Note != "" {
		res.Note += "; " + fn.Note
	}
	return res
}

// resolveOp — trace → pod → öğrenilmiş basamakları (r.mu tutulur).
func (r *SubjectResolver) resolveOp(ctx context.Context, sourceID string, values []string, now time.Time) anomaly.ExternalSubjectResolution {
	if len(values) == 0 || values[0] == "" {
		return anomaly.ExternalSubjectResolution{Note: "operasyon seviyesi — servis bilinmiyor"}
	}
	op := values[0]
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
		// ①b pod adından (bu tik; trace'siz op)
		if total := tf.podTotals[op]; total > 0 {
			best, bestN := "", 0
			for s, n := range tf.podVotes[op] {
				if n > bestN || (n == bestN && s < best) {
					best, bestN = s, n
				}
			}
			if best != "" && float64(bestN)/float64(total) >= 0.5 {
				return anomaly.ExternalSubjectResolution{Service: best, Source: "pod", Note: fmt.Sprintf("pod adından (%d/%d pod)", bestN, total)}
			}
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

// ResolveFor — kaynağa bağlı çözücü (ExternalTarget.Subject); son çözümü saklar.
func (r *SubjectResolver) ResolveFor(sourceID string) func(ctx context.Context, values []string) anomaly.ExternalSubjectResolution {
	return func(ctx context.Context, values []string) anomaly.ExternalSubjectResolution {
		res := r.Resolve(ctx, sourceID, values)
		if len(values) > 0 {
			r.mu.Lock()
			r.lastRes[sourceID+"\x00"+values[0]] = res
			if len(values) > 1 {
				r.lastRes[sourceID+"\x00"+values[0]+"\x00"+values[1]] = res
			}
			r.mu.Unlock()
		}
		return res
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
	delete(r.fnFacts, sourceID)
	if r.state != nil {
		_ = r.state.PutSetting(ctx, learnedKey(sourceID), []byte(`{"v":1,"entries":{}}`))
	}
}
