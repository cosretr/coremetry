package evaluator

// problem_frequent.go — v0.10.2280 (entity grafı ile Problem birleştirme S3,
// docs/problems/entity-graph-merge-design.md §6): "sık tekrar eden" GÖLGESİ.
//
// Her açık Problem için kural açılış bildirimini tutar mıydı: imza =
// (kümenin olası kökeni, yoksa Problem'in kendi öznesi; aile; yön), kural
// kimliği imzaya girmez. Karar chstore.EvaluateFrequent (SAF). Hiçbir
// Problem, öncelik, bildirim ya da liste sırası değişmez; LLM yok; oy /
// karar okunmaz. Bildirim gölge satırı notify tarafında yazılır
// (FrequentShadowVerdict → policy_shadow.go), gönderim kararına bağlanmaz.
//
// Okuma: problems geçmişi özne başına 1 sa hatırlanır; ıskada TEK toplu
// okuma (≤ 200 özne, ≤ 14 gün, LIMIT 2000, 5 sn, max_rows_to_read 5M
// 'break'). Kararlar Problem başına 1 sa hatırlanır (imza değişirse yeniden).
// Tik başına ek okuma yalnız yeni özne / süresi dolan hatırlama varsa.

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	freqMemoTTL      = time.Hour
	freqBlobMax      = 200 // blobdaki sık Problem kararı tavanı
	freqTopSigs      = 10
	freqP1ListMax    = 20
	freqReadDeadline = 5 * time.Second
)

// freqStore — geçmiş okuması (graphShadowStore'un parçası; ayrı arayüz
// isteğe bağlı çağrı için).
type freqStore interface {
	ProblemHistoryForSubjects(ctx context.Context, subjects []string, since time.Time) (chstore.FrequentHistoryRead, error)
}

type freqHistRow struct {
	sig string
	ep  chstore.FrequentEpisode
}

type freqHist struct {
	rows    []freqHistRow
	since   time.Time
	at      time.Time
	partial bool
}

type freqVerdictEntry struct {
	v  chstore.FrequentVerdict
	at time.Time
}

// freqSeenEntry — 24 sa rapor sayacı (Problem başına).
type freqSeenEntry struct {
	kind, sig, name, family, dir string
	prio0                        string
	becameP1                     bool
	at                           int64
	v                            chstore.FrequentVerdict
}

// frequentState — süreç belleği (ayrı kilit: bildirim gölgesi de okur).
type frequentState struct {
	mu       sync.Mutex
	hist     map[string]*freqHist        // problems.service değeri → geçmiş
	verdicts map[string]freqVerdictEntry // Problem → karar
	origin   map[string]string           // Problem → son plandaki köken entity kimliği
	seen     map[string]*freqSeenEntry
	reads    chstore.FrequentReads
}

func newFrequentState() *frequentState {
	return &frequentState{hist: map[string]*freqHist{}, verdicts: map[string]freqVerdictEntry{},
		origin: map[string]string{}, seen: map[string]*freqSeenEntry{}}
}

// frequentSubjectKey — SAF: Problem'in kendi öznesinin imza kimliği (graf
// birimi kimliği; birleşmeye katılmayan aile için "subj/<tür>:<özne>").
func frequentSubjectKey(p chstore.Problem) (key, name string) {
	if k, _, n := graphUnitKey(p); k != "" {
		return k, n
	}
	return "subj/" + chstore.ProblemSubjectKind(p.Kind) + ":" + p.Service, p.Service
}

// subjectColumn — SAF: imza varlığının problems.service değeri.
func subjectColumn(key string) string {
	if strings.HasPrefix(key, "svc:") {
		return strings.TrimPrefix(key, "svc:")
	}
	if rest, ok := strings.CutPrefix(key, "subj/"); ok {
		_, svc, _ := strings.Cut(rest, ":")
		return svc
	}
	return key // db: / queue: / ext: özne kimliği problems.service'te aynen
}

// frequentKeyOf — SAF: Problem'in imzası (köken varsa köken öznesi).
func frequentKeyOf(p chstore.Problem, originKey, originName string, now time.Time) (chstore.FrequentVerdict, chstore.ProblemStandard) {
	std := chstore.StandardizeProblem(p, chstore.StdFacts{Now: now})
	key, name := frequentSubjectKey(p)
	v := chstore.FrequentVerdict{Entity: key, Name: name, Family: std.Family, Direction: chstore.FrequentDirection(p, std.Family)}
	if originKey != "" {
		v.Entity, v.Name, v.ViaOrigin = originKey, originName, true
	}
	v.Signature = chstore.FrequentSignature(v.Entity, v.Family, v.Direction)
	return v, std
}

// histRowsFrom — SAF: okunan satırlar → (imza, bölüm).
func histRowsFrom(rows []chstore.Problem, keep []string) []freqHistRow {
	out := make([]freqHistRow, 0, len(rows))
	for _, p := range rows {
		std := chstore.StandardizeProblem(p, chstore.StdFacts{Now: time.Unix(0, p.StartedAt)})
		key, _ := frequentSubjectKey(p)
		ep := chstore.FrequentEpisode{ID: p.ID, StartedAt: p.StartedAt, Priority: chstore.NotifyViewPriority(p, keep), Magnitude: chstore.StdMagnitude(std)}
		if p.ResolvedAt != nil {
			ep.ResolvedAt = *p.ResolvedAt
		}
		out = append(out, freqHistRow{sig: chstore.FrequentSignature(key, std.Family, chstore.FrequentDirection(p, std.Family)), ep: ep})
	}
	return out
}

// fetchHistory — eksik / bayat özneler için TEK toplu okuma (çağıran kilitli).
func (s *frequentState) fetchHistory(ctx context.Context, st freqStore, subjects []string, since, now time.Time, keep []string) {
	var need []string
	for _, sub := range subjects {
		h := s.hist[sub]
		if h == nil || now.Sub(h.at) >= freqMemoTTL || h.since.After(since) {
			if !contains(need, sub) {
				need = append(need, sub)
			}
		}
	}
	if len(need) == 0 || st == nil {
		return
	}
	sort.Strings(need)
	if len(need) > chstore.FrequentHistorySubjects {
		need = need[:chstore.FrequentHistorySubjects]
	}
	rctx, cancel := context.WithTimeout(ctx, freqReadDeadline)
	defer cancel()
	s.reads.Reads++
	res, err := st.ProblemHistoryForSubjects(rctx, need, since)
	s.reads.Rows += res.RowsRead
	if res.RowsRead > s.reads.MaxRows {
		s.reads.MaxRows = res.RowsRead
	}
	if err != nil {
		s.reads.Errors++
		log.Printf("[evaluator] problem_frequent: geçmiş okunamadı (bu tur karar yok, tahmin yok): %v", err)
		return
	}
	if res.Capped {
		s.reads.Capped++
	}
	by := map[string][]chstore.Problem{}
	for _, p := range res.Rows {
		by[p.Service] = append(by[p.Service], p)
	}
	for _, sub := range need {
		s.hist[sub] = &freqHist{rows: histRowsFrom(by[sub], keep), since: since, at: now, partial: res.Capped}
	}
}

// verdictFor — çağıran kilitli; geçmiş hatırlamada yoksa ok=false.
func (s *frequentState) verdictFor(p chstore.Problem, originKey, originName string, keep []string, now time.Time) (chstore.FrequentVerdict, bool) {
	v, std := frequentKeyOf(p, originKey, originName, now)
	if !chstore.FrequentFamilyEligible(v.Family) {
		return v, true
	}
	if e, ok := s.verdicts[p.ID]; ok && e.v.Signature == v.Signature && now.Sub(e.at) < freqMemoTTL {
		return e.v, true
	}
	h := s.hist[subjectColumn(v.Entity)]
	if h == nil {
		return v, false
	}
	var eps []chstore.FrequentEpisode
	for _, r := range h.rows {
		if r.sig == v.Signature {
			eps = append(eps, r.ep)
		}
	}
	out := chstore.EvaluateFrequent(v, p, std, chstore.NotifyViewPriority(p, keep), eps)
	out.Partial = h.partial
	s.verdicts[p.ID] = freqVerdictEntry{v: out, at: now}
	return out, true
}

// pass — gölge pası adımı: açık Problem'ler için kararlar + 24 sa sayaçları.
// Dönüş: bloba girecek sık Problem kararları (Problem kimliği → karar).
func (s *frequentState) pass(ctx context.Context, st freqStore, open []*chstore.Problem, origins map[string][2]string,
	keep []string, now time.Time) map[string]chstore.FrequentVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads.Passes++
	s.origin = map[string]string{}
	var subjects []string
	since := now
	for _, p := range open {
		if p == nil || p.ID == "" || p.Status != "open" {
			continue
		}
		o := origins[p.ID]
		s.origin[p.ID] = o[0] + "\x00" + o[1]
		v, _ := frequentKeyOf(*p, o[0], o[1], now)
		if !chstore.FrequentFamilyEligible(v.Family) {
			continue
		}
		if e, ok := s.verdicts[p.ID]; ok && e.v.Signature == v.Signature && now.Sub(e.at) < freqMemoTTL {
			continue
		}
		subjects = append(subjects, subjectColumn(v.Entity))
		if t := time.Unix(0, p.StartedAt).Add(-chstore.FrequentWin7d); t.Before(since) {
			since = t
		}
	}
	s.fetchHistory(ctx, st, subjects, since, now, keep)
	out := map[string]chstore.FrequentVerdict{}
	at := now.Unix()
	for _, p := range open {
		if p == nil || p.ID == "" || p.Status != "open" {
			continue
		}
		o := origins[p.ID]
		v, ok := s.verdictFor(*p, o[0], o[1], keep, now)
		if !ok || !v.Frequent {
			continue
		}
		out[p.ID] = v
		e := s.seen[p.ID]
		if e == nil {
			e = &freqSeenEntry{prio0: v.Priority}
			s.seen[p.ID] = e
		}
		e.kind, e.sig, e.name, e.family, e.dir, e.at, e.v = v.Kind, v.Signature, v.Name, v.Family, v.Direction, at, v
		// Kabul ölçütü: tutulacak Problem sonradan P1'e çıktı mı (açıkken).
		if v.Kind == chstore.FrequentKindHold && e.prio0 != "P1" && chstore.NotifyViewPriority(*p, keep) == "P1" {
			e.becameP1 = true
		}
	}
	cut := now.Add(-graphReportWindow).Unix()
	for id, e := range s.seen {
		if e.at < cut {
			delete(s.seen, id)
		}
	}
	for id, e := range s.verdicts {
		if now.Sub(e.at) >= 2*freqMemoTTL {
			delete(s.verdicts, id)
		}
	}
	for sub, h := range s.hist {
		if now.Sub(h.at) >= 2*freqMemoTTL {
			delete(s.hist, sub)
		}
	}
	return out
}

// report — SAF olmayan katlama: 24 sa sayaçları.
func (s *frequentState) report() chstore.FrequentReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := chstore.FrequentReport{AtThreshold: map[string]int64{}, Reads: s.reads}
	sigs := map[string]*chstore.FrequentTopSig{}
	for id, e := range s.seen {
		r.Frequent++
		switch e.kind {
		case chstore.FrequentKindHold:
			r.WouldHold++
			t := sigs[e.sig]
			if t == nil {
				t = &chstore.FrequentTopSig{Signature: e.sig, Name: e.name, Family: e.family, Direction: e.dir}
				sigs[e.sig] = t
			}
			t.Held++
			if e.becameP1 {
				r.HeldBecameP1++
				if len(r.BecameP1) < freqP1ListMax {
					r.BecameP1 = append(r.BecameP1, chstore.FrequentP1{ProblemID: id, Name: e.name, Family: e.family})
				}
			}
		case chstore.FrequentKindSeverity:
			r.SeverityUp++
		}
		for _, n := range []int{2, 3, 5} {
			if chstore.FrequentWouldHoldAt(e.v, n) {
				r.AtThreshold[string(rune('0'+n))]++
			}
		}
	}
	for _, t := range sigs {
		r.TopSignatures = append(r.TopSignatures, *t)
	}
	sort.Slice(r.TopSignatures, func(i, j int) bool {
		if r.TopSignatures[i].Held != r.TopSignatures[j].Held {
			return r.TopSignatures[i].Held > r.TopSignatures[j].Held
		}
		return r.TopSignatures[i].Signature < r.TopSignatures[j].Signature
	})
	if len(r.TopSignatures) > freqTopSigs {
		r.TopSignatures = r.TopSignatures[:freqTopSigs]
	}
	sort.Slice(r.BecameP1, func(i, j int) bool { return r.BecameP1[i].ProblemID < r.BecameP1[j].ProblemID })
	return r
}

// planOrigins — SAF: plandan Problem → (köken entity kimliği, adı); yalnız
// kökeni belli (belirsiz olmayan) ≥ 2 varlıklı kümelerin üyeleri.
func planOrigins(p graphPlan) map[string][2]string {
	out := map[string][2]string{}
	for _, g := range p.Groups {
		if g.Origin == nil || g.Origin.Entity == "" || g.Entities < 2 {
			continue
		}
		for _, m := range g.Members {
			out[m.ProblemID] = [2]string{g.Origin.Entity, g.Origin.Name}
		}
	}
	return out
}

// capFrequentBlob — SAF: blobdaki karar sayısı ≤ freqBlobMax (önce tutulacaklar,
// sonra kimlik sırası).
func capFrequentBlob(m map[string]chstore.FrequentVerdict) map[string]chstore.FrequentVerdict {
	if len(m) <= freqBlobMax {
		return m
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		hi, hj := m[ids[i]].Kind == chstore.FrequentKindHold, m[ids[j]].Kind == chstore.FrequentKindHold
		if hi != hj {
			return hi
		}
		return ids[i] < ids[j]
	})
	out := make(map[string]chstore.FrequentVerdict, freqBlobMax)
	for _, id := range ids[:freqBlobMax] {
		out[id] = m[id]
	}
	return out
}

// FrequentShadowVerdict — v0.10.2280: bildirim gölgesinin isteğe bağlı
// sorusu (notify.FrequentShadowSource). Kararı hatırlamadan verir; yoksa
// tek özne için sınırlı okuma yapar (5 sn). Köken yalnız bu süreç evaluator
// lideriyse bilinir; değilse Problem'in kendi öznesi. ok=false = karar yok
// (okunamadı / aile dışı) → gölge satırı yazılmaz. S5 kapalıyken gönderimi
// ETKİLEMEZ; v0.10.2342 (S5) açıkken notify enforce yolu da sorar
// (frequent_hold.go, NotifiableAt `frequent` adımı).
func (e *Evaluator) FrequentShadowVerdict(ctx context.Context, p chstore.Problem) (chstore.FrequentVerdict, bool) {
	if e == nil || p.ID == "" {
		return chstore.FrequentVerdict{}, false
	}
	fs := e.frequent()
	keep := chstore.CurrentProblemPriority().InboxKeepSourcePriorityList()
	now := time.Now()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	okey, oname, _ := strings.Cut(fs.origin[p.ID], "\x00")
	v, _ := frequentKeyOf(p, okey, oname, now)
	if !chstore.FrequentFamilyEligible(v.Family) {
		return v, false
	}
	if out, ok := fs.verdictFor(p, okey, oname, keep, now); ok {
		return out, true
	}
	var st freqStore
	if e.store != nil {
		st = e.store
	}
	fs.fetchHistory(ctx, st, []string{subjectColumn(v.Entity)}, time.Unix(0, p.StartedAt).Add(-chstore.FrequentWin7d), now, keep)
	return fs.verdictFor(p, okey, oname, keep, now)
}
