package evaluator

// problem_graph_shadow.go — v0.10.2274 (entity grafı ile Problem birleştirme
// S1, docs/problems/entity-graph-merge-design.md §8–§9): GÖLGE PASI.
//
// İkiz göçünün (mergeServiceTwins) zaten okuduğu açık Problem anlık
// görüntüsünü kullanır (yeni zamanlayıcı yok, yalnız evaluator lideri).
// Graf gerçeklerini okur, saf plan işlevini (problem_graph.go) koşar,
// sonucu ve gölge raporunu `system_settings` blobuna yazar. YAZDIĞI TEK ŞEY
// o blobdur: Problem satırı, üye olayı, bildirim, liste ya da CoSRE girdisi
// yok (graphShadowStore arayüzünde yazma metodu yalnız
// SaveProblemGraphShadow; test kilitler). "Problem değil" kararları ve
// oylar OKUNMAZ; LLM yok.
//
// Maliyet (tasarım §8): aday birim < 2 → ek okuma yok. Aksi halde aday
// kümesinin tam özetiyle 5 dk hatırlanan bir okuma turu: entity_relations en
// çok 3 (calls çağıran yönü, serves, gerekiyorsa ortak bağımlılığın çağıran
// sayısı; her biri ≤ 500 uç, LIMIT 5000, 10 sn, last_seen alt sınırı) +
// entity_sync_runs tek satır (calls geçişinin tazeliği, 5 sn) + ml_events
// tek okuma (bağımlılık olayları, son 1 sa, LIMIT 2000, 10 sn; yalnız
// çağrı / ortak bağımlılık adayı varsa) + Redis canlı dizin (süreç başına
// 30 sn yerel kopya; CH okuması değil).
//
// Sayaçlar süreç belleğinde (lider değişince sıfırlanır), 24 sa pencerede
// FARKLI bağ / Problem sayar (ML yayın kapısı gölgesi emsali). Blob değişince
// ya da 5 dk'da bir yazılır; her yazımda tek log satırı.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/rollout"
)

const (
	graphMemoTTL       = 5 * time.Minute
	graphWriteEvery    = 5 * time.Minute
	graphReportWindow  = 24 * time.Hour
	graphCallsStale    = 60 * time.Minute // calls geçişi bundan eskiyse M-CALL / M-DEP kapalı
	graphEvidenceSince = time.Hour
	graphRelLimit      = 5000
	graphEvLimit       = 2000
	graphMaxUnits      = 500 // entity_relations uç tavanı
)

// graphShadowStore — gölge pasının depo yüzü. Okumalar + TEK yazım (blob).
// Problem / ml_events / bildirim yazan hiçbir metot burada YOK.
type graphShadowStore interface {
	EntityRelationsFor(ctx context.Context, rel string, ids []string, byChild bool, at time.Time, limit int) ([]entity.Relation, error)
	EntityLastRunAt(ctx context.Context, cid string, since time.Time) (time.Time, error)
	ProblemGraphDependencyEvents(ctx context.Context, services []string, since time.Time, limit int) ([]chstore.GraphDependencyEvent, error)
	// RolloutsForWorkloads — S2 K-ROLLOUT (≤ 50 iş yükü, LIMIT 200, 10 sn).
	RolloutsForWorkloads(ctx context.Context, keys []rollout.Key, from, to time.Time) ([]chstore.RolloutRow, error)
	// ProblemHistoryForSubjects — S3 sık tekrar geçmişi (≤ 200 özne, ≤ 14 gün,
	// LIMIT 2000, 5 sn, max_rows_to_read 5M 'break').
	ProblemHistoryForSubjects(ctx context.Context, subjects []string, since time.Time) (chstore.FrequentHistoryRead, error)
	SaveProblemGraphShadow(ctx context.Context, r chstore.ProblemGraphShadow) error
}

// GraphLiveSource — canlı dizin (Redis; pod → node, host → servis).
type GraphLiveSource interface {
	// ClusterViews — etkin cluster'ların taze görüntüleri; biri bile yoksa ok=false.
	ClusterViews(ctx context.Context) ([]*entity.LiveView, bool)
	HostView(ctx context.Context) (*entity.LiveView, bool)
}

type graphLive struct {
	idx      *entity.LiveIndex
	clusters func() []string
}

// NewGraphLive — canlı dizin okuyucusu + etkin cluster kimlikleri.
func NewGraphLive(idx *entity.LiveIndex, clusters func() []string) GraphLiveSource {
	return graphLive{idx: idx, clusters: clusters}
}

func (g graphLive) ClusterViews(ctx context.Context) ([]*entity.LiveView, bool) {
	if g.idx == nil || g.clusters == nil {
		return nil, false
	}
	cids := g.clusters()
	if len(cids) == 0 {
		return nil, false
	}
	out := make([]*entity.LiveView, 0, len(cids))
	for _, cid := range cids {
		v, ok := g.idx.View(ctx, cid)
		if !ok {
			return nil, false // eksik görüntü: tahmin yok, aile kapalı
		}
		out = append(out, v)
	}
	return out, true
}

func (g graphLive) HostView(ctx context.Context) (*entity.LiveView, bool) {
	if g.idx == nil {
		return nil, false
	}
	return g.idx.View(ctx, entity.HostClusterID)
}

// graphShadow — Evaluator'daki gölge durumu (sıfır değer kullanılabilir).
type graphShadow struct {
	live     atomic.Value // GraphLiveSource
	mu       sync.Mutex
	st       *graphShadowState
	freqOnce sync.Once
	freq     *frequentState // S3: ayrı kilit (bildirim gölgesi de okur)
}

// frequent — S3 durumu (tembel; pas ve bildirim gölgesi paylaşır).
func (e *Evaluator) frequent() *frequentState {
	e.graphShadow.freqOnce.Do(func() { e.graphShadow.freq = newFrequentState() })
	return e.graphShadow.freq
}

// SetProblemGraphLive — v0.10.2274: canlı dizin kaynağı (main; Start sonrası güvenli).
func (e *Evaluator) SetProblemGraphLive(src GraphLiveSource) {
	if src != nil {
		e.graphShadow.live.Store(src)
	}
}

func (e *Evaluator) problemGraphLive() GraphLiveSource {
	if v, ok := e.graphShadow.live.Load().(GraphLiveSource); ok {
		return v
	}
	return nil
}

type graphLinkEntry struct {
	rule, age string
	strict    bool
	at        int64
}

type graphSeen struct {
	val string
	n   int
	at  int64
}

// graphShadowState — süreç belleği sayaçları + okuma önbelleği.
type graphShadowState struct {
	since       time.Time
	links       map[string]graphLinkEntry // kural|a|b
	noLink      map[string]graphSeen      // Problem → neden
	missing     map[string]int64          // Problem → son görülme
	sizes       map[string]graphSeen      // sahip → boy (düz)
	sizesStrict map[string]graphSeen      // sahip → boy (katı)
	familyOff   map[string]int64
	reads       chstore.ProblemGraphReads
	memoKey     uint64
	memoAt      time.Time
	memo        *graphFacts
	omemo       *originFacts // S2: S1 turuyla aynı anda tazelenir
	omemoAt     time.Time
	origins     map[string]graphSeen // sahip → "kural|güven" (S2)
	freq        *frequentState       // S3
	lastWrite   time.Time
	lastHash    uint64
}

func newGraphShadowState(now time.Time) *graphShadowState {
	return &graphShadowState{since: now, links: map[string]graphLinkEntry{}, noLink: map[string]graphSeen{},
		missing: map[string]int64{}, sizes: map[string]graphSeen{}, sizesStrict: map[string]graphSeen{}, familyOff: map[string]int64{},
		origins: map[string]graphSeen{}, freq: newFrequentState()}
}

// problemGraphShadow — mergeServiceTwins'in anlık görüntüsüyle gölge pası.
// Hata / panik tiki durdurmaz (yalnız log).
func (e *Evaluator) problemGraphShadow(ctx context.Context, open []*chstore.Problem) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[evaluator] problem_graph_shadow panik (tik sürer): %v", r)
		}
	}()
	e.graphShadow.mu.Lock()
	defer e.graphShadow.mu.Unlock()
	now := time.Now()
	if e.graphShadow.st == nil {
		e.graphShadow.st = newGraphShadowState(now)
		e.graphShadow.st.freq = e.frequent()
	}
	prio := chstore.CurrentProblemPriority()
	pol := chstore.OwnerPolicy{Keep: prio.InboxKeepSourcePriorityList(), Priority: prio, NowNs: now.UnixNano()}
	runGraphShadow(ctx, e.store, e.problemGraphLive(), e.graphShadow.st, open, pol, now)
}

// runGraphShadow — pasın gövdesi (test edilebilir: depo arayüzden).
func runGraphShadow(ctx context.Context, st graphShadowStore, live GraphLiveSource, s *graphShadowState,
	open []*chstore.Problem, pol chstore.OwnerPolicy, now time.Time) (graphPlan, bool) {
	units, kinds := buildGraphUnits(open, pol)
	s.reads.Passes++
	var facts *graphFacts
	if len(units) < 2 {
		s.reads.SkippedFew++
		facts = &graphFacts{}
	} else {
		if len(units) > graphMaxUnits {
			units = units[:graphMaxUnits]
		}
		facts = s.factsFor(ctx, st, live, units, now)
	}
	plan := planGraph(units, kinds, facts)
	if len(units) >= 2 {
		// S2: köken + etki alanı; okumalar yalnız S1 turu tazelendiğinde.
		if s.omemo == nil || !s.omemoAt.Equal(s.memoAt) {
			s.omemo, s.omemoAt = fetchOriginFacts(ctx, st, plan, facts, &s.reads), s.memoAt
		}
		attachOrigins(&plan, facts, s.omemo)
	}
	s.observe(plan, now)
	// S3: sık tekrar gölgesi (tek Problem'ler dahil; köken varsa imza kökenden).
	var freq map[string]chstore.FrequentVerdict
	if s.freq != nil {
		freq = s.freq.pass(ctx, st, open, planOrigins(plan), pol.Keep, now)
	}
	rep := s.report(now, len(units))
	rep.Groups = plan.Groups
	if s.freq != nil {
		rep.Frequent = capFrequentBlob(freq)
		fr := s.freq.report()
		rep.FrequentReport = &fr
	}
	size := chstore.FitProblemGraphBlob(&rep, chstore.ProblemGraphBlobMax)
	h := graphReportHash(rep)
	if h == s.lastHash && now.Sub(s.lastWrite) < graphWriteEvery {
		return plan, false
	}
	if err := st.SaveProblemGraphShadow(ctx, rep); err != nil {
		log.Printf("[evaluator] problem_graph_shadow: blob yazılamadı: %v", err)
		return plan, false
	}
	s.lastHash, s.lastWrite = h, now
	log.Print(graphShadowLine(rep, size))
	return plan, true
}

// graphReportHash — SAF: At hariç içerik özeti (değişmeyen blob yeniden yazılmaz).
func graphReportHash(r chstore.ProblemGraphShadow) uint64 {
	r.At, r.Reads = 0, chstore.ProblemGraphReads{} // tur sayaçları içerik değil
	if r.FrequentReport != nil {
		fr := *r.FrequentReport
		fr.Reads = chstore.FrequentReads{}
		r.FrequentReport = &fr
	}
	raw, _ := json.Marshal(r)
	h := fnv.New64a()
	_, _ = h.Write(raw)
	return h.Sum64()
}

// unitDigest — SAF: aday kümesinin TAM özeti (önbellek anahtarı; sayı değil).
func unitDigest(us []graphUnit) uint64 {
	keys := make([]string, len(us))
	for i, u := range us {
		keys[i] = u.Key
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// factsFor — okuma turu (5 dk, aday kümesi özetiyle hatırlanır).
func (s *graphShadowState) factsFor(ctx context.Context, st graphShadowStore, live GraphLiveSource, us []graphUnit, now time.Time) *graphFacts {
	key := unitDigest(us)
	if s.memo != nil && s.memoKey == key && now.Sub(s.memoAt) < graphMemoTTL {
		return s.memo
	}
	f, rel := fetchGraphFacts(ctx, st, live, us, now, &s.reads)
	s.reads.Refreshes++
	s.reads.Relations += int64(rel)
	if rel > s.reads.MaxRelPerPass {
		s.reads.MaxRelPerPass = rel
	}
	for fam, off := range map[string]bool{"calls": !f.CallsOK, "serves": !f.ServesOK, "hub": !f.HubOK,
		"node": !f.NodeOK, "host": !f.HostOK, "evidence": !f.EvOK} {
		if off {
			s.familyOff[fam]++
		}
	}
	s.memo, s.memoKey, s.memoAt = f, key, now
	return f
}

// fetchGraphFacts — en çok 3 entity_relations okuması (dönüş: sayısı).
func fetchGraphFacts(ctx context.Context, st graphShadowStore, live GraphLiveSource, us []graphUnit, now time.Time, rd *chstore.ProblemGraphReads) (*graphFacts, int) {
	f := &graphFacts{Callees: map[string][]string{}, Callers: map[string]int{}, WL: map[string][]string{},
		Nodes: map[string][]string{}, NodeSvcs: map[string]int{}, Hosts: map[string][]string{}, HostSvcs: map[string]int{},
		DepTgt: map[string][]string{}}
	var svcIDs, svcNames []string
	isUnit := map[string]bool{}
	for _, u := range us {
		isUnit[u.Key] = true
		if u.Type == entity.TypeService {
			svcIDs = append(svcIDs, u.Key)
			svcNames = append(svcNames, u.Name)
		}
	}
	rel := 0
	fail := func(what string, err error) {
		rd.Errors++
		logGraphReadErr(what, err)
	}
	// 1) calls tazeliği + çağıran yönü.
	rd.Runs++
	last, err := st.EntityLastRunAt(ctx, entity.TopologyClusterID, now.Add(-graphCallsStale))
	switch {
	case err != nil:
		fail("calls geçiş tazeliği", err)
	case last.IsZero():
		// son başarılı geçiş 60 dk'dan eski: M-CALL / M-DEP kapalı
	case len(svcIDs) > 0:
		rel++
		edges, err := st.EntityRelationsFor(ctx, entity.RelCalls, svcIDs, false, time.Time{}, graphRelLimit)
		if err != nil {
			fail("calls kenarları", err)
			break
		}
		for _, e := range edges {
			if !contains(f.Callees[e.ParentID], e.ChildID) {
				f.Callees[e.ParentID] = append(f.Callees[e.ParentID], e.ChildID)
			}
		}
		f.CallsOK = true
	}
	// 2) serves (iş yükü → servis), çocuk yönü.
	if len(svcIDs) > 0 {
		rel++
		edges, err := st.EntityRelationsFor(ctx, entity.RelServes, svcIDs, true, time.Time{}, graphRelLimit)
		if err != nil {
			fail("serves kenarları", err)
		} else {
			for _, e := range edges {
				wl := entity.CanonicalID(e.ParentID)
				if !contains(f.WL[e.ChildID], wl) {
					f.WL[e.ChildID] = append(f.WL[e.ChildID], wl)
				}
			}
			f.ServesOK = true
		}
	}
	// 3) ortak bağımlılığın çağıran sayısı (yalnız ≥ 2 aday servisin çağırdığı,
	// Problem'i olmayan hedefler).
	f.HubOK = true
	if f.CallsOK {
		shared := sharedCallees(f.Callees, isUnit)
		if len(shared) > 0 {
			rel++
			edges, err := st.EntityRelationsFor(ctx, entity.RelCalls, shared, true, time.Time{}, graphRelLimit)
			if err != nil {
				f.HubOK = false
				fail("ortak bağımlılık çağıranları", err)
			} else {
				seen := map[string]bool{}
				for _, e := range edges {
					if strings.HasPrefix(e.ParentID, "svc:") && !seen[e.ParentID+"\x00"+e.ChildID] {
						seen[e.ParentID+"\x00"+e.ChildID] = true
						f.Callers[e.ChildID]++
					}
				}
				f.HubTrunc = len(edges) >= graphRelLimit
			}
		}
		// Bağımlılık olayı kanıtı (M-CALL katı sürümü, M-DEP): yalnız aday varsa.
		if len(shared) > 0 || hasCallBetweenUnits(f.Callees, isUnit) {
			rd.Evidence++
			evs, err := st.ProblemGraphDependencyEvents(ctx, svcNames, now.Add(-graphEvidenceSince), graphEvLimit)
			if err != nil {
				fail("bağımlılık olayları", err)
			} else {
				for _, e := range evs {
					f.DepTgt[e.Service] = append(f.DepTgt[e.Service], e.Target)
				}
				f.EvOK = true
			}
		} else {
			f.EvOK = true // sorulacak bağ yok
		}
	}
	// Canlı dizin: pod → node, host → servis (Redis; CH okuması yok).
	if live != nil {
		want := map[string]bool{}
		for _, n := range svcNames {
			want[n] = true
		}
		if views, ok := live.ClusterViews(ctx); ok {
			f.NodeOK = true
			nodeSvcs := map[string]map[string]bool{}
			for _, v := range views {
				for _, p := range v.Pods {
					if p.Node == "" {
						continue
					}
					nk := v.ClusterID + "/" + p.Node
					for _, sv := range p.Services {
						if nodeSvcs[nk] == nil {
							nodeSvcs[nk] = map[string]bool{}
						}
						nodeSvcs[nk][sv.Name] = true
						if id := entity.ServiceID(sv.Name); want[sv.Name] && !contains(f.Nodes[id], nk) {
							f.Nodes[id] = append(f.Nodes[id], nk)
						}
					}
				}
			}
			for nk, m := range nodeSvcs {
				f.NodeSvcs[nk] = len(m)
			}
		}
		if hv, ok := live.HostView(ctx); ok && hv != nil {
			f.HostOK = true
			for _, h := range hv.Hosts {
				hid := entity.HostID(h.Name)
				f.HostSvcs[hid] = len(h.Services)
				for _, sv := range h.Services {
					if id := entity.ServiceID(sv.Name); want[sv.Name] && !contains(f.Hosts[id], hid) {
						f.Hosts[id] = append(f.Hosts[id], hid)
					}
				}
			}
		}
	}
	return f, rel
}

// sharedCallees — SAF: ≥ 2 aday servisin çağırdığı, Problem'i olmayan hedefler.
func sharedCallees(callees map[string][]string, isUnit map[string]bool) []string {
	n := map[string]int{}
	for _, cs := range callees {
		for _, c := range cs {
			n[c]++
		}
	}
	var out []string
	for c, k := range n {
		if k >= 2 && !isUnit[c] {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	if len(out) > graphMaxUnits {
		out = out[:graphMaxUnits]
	}
	return out
}

// hasCallBetweenUnits — SAF: iki aday arasında çağrı kenarı var mı.
func hasCallBetweenUnits(callees map[string][]string, isUnit map[string]bool) bool {
	for p, cs := range callees {
		if !isUnit[p] {
			continue
		}
		for _, c := range cs {
			if isUnit[c] {
				return true
			}
		}
	}
	return false
}

// observe — planı 24 sa pencereli sayaçlara katar (FARKLI bağ / Problem).
func (s *graphShadowState) observe(p graphPlan, now time.Time) {
	at := now.Unix()
	for _, c := range p.Clusters {
		owner := p.Units[c.Units[0]]
		for _, ui := range c.Units {
			u := p.Units[ui]
			for _, r := range u.Rows { // aynı servisin diğer satırları
				if u.Type == entity.TypeService && r.ID != u.Rep.ID {
					s.links[chstore.GraphRuleSvc+"|"+u.Rep.ID+"|"+r.ID] = graphLinkEntry{rule: chstore.GraphRuleSvc, at: at}
				}
			}
			l, ok := c.Links[ui]
			if !ok {
				continue
			}
			key := l.Rule + "|" + p.Units[l.Peer].Rep.ID + "|" + u.Rep.ID
			e, seen := s.links[key]
			if !seen {
				e = graphLinkEntry{rule: l.Rule, strict: l.Strict, age: graphAgeBucket(u.Onset - owner.Onset)}
			}
			e.at = at
			s.links[key] = e
		}
		if len(c.Units) > 1 {
			bump(s.sizes, owner.Rep.ID, len(c.Units), at)
		}
	}
	for _, c := range p.Strict {
		if len(c.Units) > 1 {
			bump(s.sizesStrict, p.Units[c.Units[0]].Rep.ID, len(c.Units), at)
		}
	}
	for id, r := range p.NoLink {
		s.noLink[id] = graphSeen{val: r, at: at}
	}
	for _, g := range p.Groups {
		if o := g.Origin; o != nil {
			s.origins[g.OwnerID] = graphSeen{val: o.Rule + "|" + o.Confidence, at: at}
		}
	}
	for _, id := range p.GraphMissing {
		s.missing[id] = at
	}
	s.prune(now)
}

func bump(m map[string]graphSeen, k string, n int, at int64) {
	e := m[k]
	if n > e.n {
		e.n = n
	}
	e.at = at
	m[k] = e
}

func (s *graphShadowState) prune(now time.Time) {
	cut := now.Add(-graphReportWindow).Unix()
	for k, e := range s.links {
		if e.at < cut {
			delete(s.links, k)
		}
	}
	for _, m := range []map[string]graphSeen{s.noLink, s.sizes, s.sizesStrict, s.origins} {
		for k, e := range m {
			if e.at < cut {
				delete(m, k)
			}
		}
	}
	for k, at := range s.missing {
		if at < cut {
			delete(s.missing, k)
		}
	}
}

// report — SAF olmayan katlama (durumdan): rapor sayaçları.
func (s *graphShadowState) report(now time.Time, units int) chstore.ProblemGraphShadow {
	r := chstore.ProblemGraphShadow{At: now.Unix(), Since: s.since.Unix(), WindowH: int(graphReportWindow / time.Hour), Units: units,
		Links: map[string]int64{}, NoLink: map[string]int64{}, Sizes: map[string]int64{}, SizesStrict: map[string]int64{},
		Age: map[string]int64{}, AgeByRule: map[string]map[string]int64{}, FamilyOff: map[string]int64{}, Reads: s.reads,
		GraphMissing: int64(len(s.missing))}
	for _, e := range s.links {
		r.Links[e.rule]++
		if e.rule == chstore.GraphRuleCall && e.strict {
			r.CallStrict++
		}
		if e.rule == chstore.GraphRuleSvc {
			continue // emme yaşı yalnız servisler arası
		}
		r.Age[e.age]++
		if r.AgeByRule[e.rule] == nil {
			r.AgeByRule[e.rule] = map[string]int64{}
		}
		r.AgeByRule[e.rule][e.age]++
	}
	// N-CAP — S1'de UYGULANMAZ; 90 dk'yı aşan servisler arası bağ sayısı.
	if n := r.Age[">90"]; n > 0 {
		r.NoLink[chstore.GraphNoCap] = n
	}
	for _, e := range s.noLink {
		r.NoLink[e.val]++
	}
	for _, e := range s.sizes {
		r.Sizes[graphSizeBucket(e.n)]++
	}
	for _, e := range s.sizesStrict {
		r.SizesStrict[graphSizeBucket(e.n)]++
	}
	for k, v := range s.familyOff {
		r.FamilyOff[k] = v
	}
	r.OriginWins, r.OriginConf = map[string]int64{}, map[string]int64{}
	for _, e := range s.origins {
		rule, conf, _ := strings.Cut(e.val, "|")
		r.OriginConf[conf]++
		if conf == chstore.OriginUnclear {
			r.OriginUnclear++
		} else {
			r.OriginWins[rule]++
		}
	}
	return r
}

// graphKV — SAF: "k:v,k:v" (sıralı).
func graphKV(m map[string]int64) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for i, k := range ks {
		ks[i] = fmt.Sprintf("%s:%d", k, m[k])
	}
	return strings.Join(ks, ",")
}

// graphShadowLine — SAF: yazım başına tek log satırı.
func graphShadowLine(r chstore.ProblemGraphShadow, bytes int) string {
	return fmt.Sprintf("[evaluator] problem_graph_shadow window=%dh units=%d links=%s call_strict=%d no_link=%s sizes=%s sizes_strict=%s age=%s graph_missing=%d family_off=%s origin_wins=%s origin_unclear=%d origin_conf=%s frequent=%s reads=passes:%d,skipped_few:%d,refreshes:%d,relations:%d,max_rel_per_pass:%d,runs:%d,evidence:%d,rollouts:%d,blast:%d,errors:%d groups=%d bytes=%d truncated=%v",
		r.WindowH, r.Units, graphKV(r.Links), r.CallStrict, graphKV(r.NoLink), graphKV(r.Sizes), graphKV(r.SizesStrict), graphKV(r.Age),
		r.GraphMissing, graphKV(r.FamilyOff), graphKV(r.OriginWins), r.OriginUnclear, graphKV(r.OriginConf), frequentKV(r.FrequentReport),
		r.Reads.Passes, r.Reads.SkippedFew, r.Reads.Refreshes, r.Reads.Relations, r.Reads.MaxRelPerPass,
		r.Reads.Runs, r.Reads.Evidence, r.Reads.Rollouts, r.Reads.Blast, r.Reads.Errors, len(r.Groups), bytes, r.Truncated)
}

func logGraphReadErr(what string, err error) {
	log.Printf("[evaluator] problem_graph_shadow: %s okunamadı (bu tur o kural ailesi kapalı): %v", what, err)
}

// frequentKV — SAF: log satırının S3 parçası.
func frequentKV(f *chstore.FrequentReport) string {
	if f == nil {
		return "off"
	}
	return fmt.Sprintf("frequent:%d,would_hold:%d,severity_up:%d,held_became_p1:%d,hist_reads:%d,hist_rows:%d,hist_max_rows:%d,hist_capped:%d,hist_errors:%d",
		f.Frequent, f.WouldHold, f.SeverityUp, f.HeldBecameP1, f.Reads.Reads, f.Reads.Rows, f.Reads.MaxRows, f.Reads.Capped, f.Reads.Errors)
}
