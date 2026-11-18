package evaluator

// problem_graph.go — v0.10.2274 (entity grafı ile Problem birleştirme S1,
// docs/problems/entity-graph-merge-design.md §2): SAF plan işlevi. Açık
// Problem'ler + graf gerçekleri → gölge bağlar, kümeler, bağlanmayanlar.
// Hiçbir okuma / yazma yok; girdi satırları kopyalanır, değiştirilmez.
//
// Birim = varlık: servis özneli, birleşmeye katılan ailelerin satırları
// servis başına TEK birimdir (aynı servis = M-SVC, Faz 1 kuralı); db / queue
// özneli Problem'ler kendi varlığıdır. Bağ yalnız iki birim arasında, tek
// graf adımıyla (ortak nokta kuralları ortak varlık üzerinden iki kenar)
// kurulur; ara halka her zaman açık Problem'i olan bir birimdir, yani
// sağlıklı bir servisin üzerinden atlanmaz.

import (
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

// Sabitler (tasarım §2, operatör kararı 4; ayar YOK).
const (
	graphWindowNs   = int64(30 * 60 * 1e9)                   // komşunun başlangıcına en çok 30 dk
	graphHubMax     = 20                                     // N-HUB: ortak noktayı kullanan servis
	graphSizeMax    = 25                                     // N-SIZE: birleşik Problem en çok 25 varlık
	graphMaxHops    = 3                                      // sahipten en çok 3 bağ
	graphAbsorbNs   = int64(chstore.ProblemGraphAbsorbLimit) // emme sınırı: S4 açıkken uygulanır, kapalıyken yalnız ölçülür
	graphNearMax    = 3                                      // bağlanmayanlar bloğu en çok 3 satır
	graphMemberRows = 25                                     // üyeler tablosu en çok 25 satır
)

// graphUnit — bir varlığın açık Problem'leri.
type graphUnit struct {
	Key   string // entity id
	Type  string
	Name  string
	Onset int64 // satırların en erken başlangıcı (unix ns)
	Rep   chstore.Problem
	Rows  []chstore.Problem // başlangıç sırası
}

// graphFacts — bir okuma turunun graf gerçekleri (yalnız aday birimler).
type graphFacts struct {
	CallsOK  bool                // calls kenarları okundu ve geçiş taze
	Callees  map[string][]string // çağıran entity → çağrılanlar
	HubOK    bool                // ortak bağımlılık çağıran sayısı okundu
	HubTrunc bool                // hub okuması LIMIT'e değdi
	Callers  map[string]int      // ortak bağımlılık → çağıran servis sayısı
	ServesOK bool
	WL       map[string][]string // svc entity → iş yükü id'leri
	NodeOK   bool
	Nodes    map[string][]string // svc entity → "cid/node"
	NodeSvcs map[string]int      // "cid/node" → servis sayısı
	HostOK   bool
	Hosts    map[string][]string // svc entity → host: id
	HostSvcs map[string]int      // host: id → servis sayısı
	EvOK     bool
	DepTgt   map[string][]string // servis adı → bağımlılık olayı hedefleri
}

// graphKindRow — birleşmeye katılmayan aile (N-KIND adayı).
type graphKindRow struct {
	P   chstore.Problem
	Svc string // servis özneli ise servis adı
}

// buildGraphUnits — SAF: açık satırlar → birimler (+ N-KIND satırları).
func buildGraphUnits(open []*chstore.Problem, pol chstore.OwnerPolicy) ([]graphUnit, []graphKindRow) {
	byKey := map[string]*graphUnit{}
	var kinds []graphKindRow
	for _, p := range open {
		if p == nil || p.ID == "" || p.Status == "resolved" {
			continue
		}
		key, typ, name := graphUnitKey(*p)
		if key == "" {
			svc := ""
			if chstore.ProblemSubjectKind(p.Kind) == chstore.ProblemKindService {
				svc = p.Service
			}
			kinds = append(kinds, graphKindRow{P: *p, Svc: svc})
			continue
		}
		u := byKey[key]
		if u == nil {
			u = &graphUnit{Key: key, Type: typ, Name: name, Onset: p.StartedAt}
			byKey[key] = u
		}
		u.Rows = append(u.Rows, *p)
		if p.StartedAt < u.Onset {
			u.Onset = p.StartedAt
		}
	}
	out := make([]graphUnit, 0, len(byKey))
	for _, u := range byKey {
		sort.Slice(u.Rows, func(i, j int) bool {
			if u.Rows[i].StartedAt != u.Rows[j].StartedAt {
				return u.Rows[i].StartedAt < u.Rows[j].StartedAt
			}
			return u.Rows[i].ID < u.Rows[j].ID
		})
		u.Rep = u.Rows[0]
		if u.Type == entity.TypeService {
			ptrs := make([]*chstore.Problem, len(u.Rows))
			for i := range u.Rows {
				ptrs[i] = &u.Rows[i]
			}
			if o := chstore.ServiceOwnerOf(ptrs, u.Name, pol); o != nil {
				u.Rep = *o
			}
		}
		out = append(out, *u)
	}
	sortUnits(out)
	return out, kinds
}

func sortUnits(us []graphUnit) {
	sort.Slice(us, func(i, j int) bool {
		if us[i].Onset != us[j].Onset {
			return us[i].Onset < us[j].Onset
		}
		return us[i].Key < us[j].Key
	})
}

// graphUnitKey — SAF: satırın birimi. Servis özneli ve birleşmeye katılan
// aile → svc:<ad>; db / queue özneli satır (db-health:, anomaly:ext…) → özne
// kimliği (topoloji düğüm kimliğiyle birebir). Kalan her şey (alarm kuralı,
// exception-storm, self-*, anomaly-cluster:, dış metrik kaynağı …) "".
func graphUnitKey(p chstore.Problem) (key, typ, name string) {
	switch chstore.ProblemSubjectKind(p.Kind) {
	case chstore.ProblemKindService:
		if p.Service != "" && chstore.MemberEligibleRule(p.RuleID) {
			return entity.ServiceID(p.Service), entity.TypeService, p.Service
		}
		if strings.HasPrefix(p.RuleID, "anomaly:ext") {
			if ref, ok := entity.ParseID(p.Service); ok && graphDepType(ref.Type) {
				return p.Service, ref.Type, ref.Name
			}
		}
	case chstore.ProblemKindDB:
		if ref, ok := entity.ParseID(p.Service); ok && graphDepType(ref.Type) {
			return p.Service, ref.Type, ref.Name
		}
	}
	return "", "", ""
}

func graphDepType(t string) bool {
	return t == entity.TypeDatabase || t == entity.TypeQueue || t == entity.TypeExternal
}

// graphHasData — SAF: birimin herhangi bir graf gerçeği var mı.
func (f *graphFacts) hasData(u *graphUnit) bool {
	if len(f.Callees[u.Key]) > 0 || len(f.WL[u.Key]) > 0 || len(f.Nodes[u.Key]) > 0 || len(f.Hosts[u.Key]) > 0 {
		return true
	}
	for _, cs := range f.Callees {
		for _, c := range cs {
			if c == u.Key {
				return true
			}
		}
	}
	return false
}

// pairResult — iki birim arasındaki karar.
type pairResult struct {
	Rule   string // M-* (bağ) ya da ""
	Strict bool
	Via    string   // ortak varlık adı
	Path   []string // adlar
	Reason string   // Rule "" ise N-*
	Hint   string   // ilişki var, ek koşul yok (M-*)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		if contains(b, x) && !contains(out, x) {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func entityName(id string) string {
	if ref, ok := entity.ParseID(id); ok {
		return ref.Name
	}
	return id
}

// depTargetMatches — SAF: bağımlılık olayı hedefi (server.address,
// db.namespace, peer.service …) çağrılan varlığa mı ait. Eşleşme kesin
// eşitlik: ad, "@" sonrası örnek adı, ya da port'suz host. Tahmin yok.
func depTargetMatches(target, calleeID string) bool {
	if target == "" {
		return false
	}
	name := entityName(calleeID)
	cands := []string{name}
	if _, inst, ok := strings.Cut(name, "@"); ok {
		cands = append(cands, inst)
	}
	for _, c := range append([]string(nil), cands...) {
		if h, _, ok := strings.Cut(c, ":"); ok && h != "" {
			cands = append(cands, h)
		}
	}
	t := target
	if h, _, ok := strings.Cut(t, ":"); ok && h != "" {
		t = h
	}
	for _, c := range cands {
		if c == target || c == t {
			return true
		}
	}
	return false
}

func (f *graphFacts) depEvidence(svc, calleeID string) bool {
	for _, t := range f.DepTgt[svc] {
		if depTargetMatches(t, calleeID) {
			return true
		}
	}
	return false
}

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

// judgePair — SAF: iki birim için bağ kuralı ya da bağlanmama nedeni.
// Kural sırası: M-CALL, M-WL, M-DEP, M-NODE, M-HOST; ilk sağlanan kazanır.
func judgePair(a, b *graphUnit, f *graphFacts, isUnit func(string) bool) pairResult {
	var related bool // herhangi bir graf ilişkisi var (koşulu sağlanmasa da)
	var hub *pairResult
	var hint pairResult
	setHint := func(rule, via string) {
		related = true
		if hint.Hint == "" {
			hint = pairResult{Reason: chstore.GraphNoRel, Hint: rule, Via: via}
		}
	}
	link := func(r pairResult) pairResult {
		if absDiff(a.Onset, b.Onset) > graphWindowNs {
			return pairResult{Reason: chstore.GraphNoTime, Hint: r.Rule, Via: r.Via}
		}
		return r
	}
	svcA, svcB := a.Type == entity.TypeService, b.Type == entity.TypeService
	// M-CALL — A calls B (B servis, db, queue ya da ext), tek adım.
	if f.CallsOK {
		for _, d := range [][2]*graphUnit{{a, b}, {b, a}} {
			caller, callee := d[0], d[1]
			if caller.Type == entity.TypeService && contains(f.Callees[caller.Key], callee.Key) {
				return link(pairResult{Rule: chstore.GraphRuleCall, Strict: f.EvOK && f.depEvidence(caller.Name, callee.Key),
					Path: []string{caller.Name, callee.Name}})
			}
		}
	}
	// M-WL — aynı iş yükü iki servisi sunuyor.
	if f.ServesOK && svcA && svcB {
		if wl := intersect(f.WL[a.Key], f.WL[b.Key]); len(wl) > 0 {
			n := entityName(wl[0])
			return link(pairResult{Rule: chstore.GraphRuleWL, Via: n, Path: []string{a.Name, "wl " + n, b.Name}})
		}
	}
	// M-DEP — ikisi de aynı hedefi çağırıyor; hedefin kendi Problem'i yok.
	if f.CallsOK && svcA && svcB {
		for _, x := range intersect(f.Callees[a.Key], f.Callees[b.Key]) {
			if isUnit(x) {
				continue // hedefin Problem'i var: bağ M-CALL ile kurulur
			}
			n := entityName(x)
			switch {
			case !f.HubOK:
				related = true
				if hub == nil {
					hub = &pairResult{Reason: chstore.GraphNoGraph, Hint: chstore.GraphRuleDep, Via: n}
				}
			case f.Callers[x] > graphHubMax || (f.HubTrunc && f.Callers[x] <= graphHubMax):
				related = true
				if hub == nil {
					hub = &pairResult{Reason: chstore.GraphNoHub, Hint: chstore.GraphRuleDep, Via: n}
				}
			case f.EvOK && f.depEvidence(a.Name, x) && f.depEvidence(b.Name, x):
				return link(pairResult{Rule: chstore.GraphRuleDep, Via: n, Path: []string{a.Name, n, b.Name}})
			default:
				setHint(chstore.GraphRuleDep, n)
			}
		}
	}
	// M-NODE — iki servis aynı node'da ve sapma o node ile sınırlı (servisin
	// bütün canlı pod'ları o node'da); node düzeyi kendi sinyali olan aile yok.
	if f.NodeOK && svcA && svcB {
		for _, n := range intersect(f.Nodes[a.Key], f.Nodes[b.Key]) {
			_, node, _ := strings.Cut(n, "/")
			switch {
			case f.NodeSvcs[n] > graphHubMax:
				related = true
				if hub == nil || hub.Reason != chstore.GraphNoHub {
					hub = &pairResult{Reason: chstore.GraphNoHub, Hint: chstore.GraphRuleNode, Via: node}
				}
			case len(f.Nodes[a.Key]) == 1 && len(f.Nodes[b.Key]) == 1:
				return link(pairResult{Rule: chstore.GraphRuleNode, Via: node, Path: []string{a.Name, "node " + node, b.Name}})
			default:
				setHint(chstore.GraphRuleNode, node)
			}
		}
	}
	// M-HOST — aynı K8s dışı host; servis yalnız o host'ta (host sinyali ailesi yok).
	if f.HostOK && svcA && svcB {
		for _, h := range intersect(f.Hosts[a.Key], f.Hosts[b.Key]) {
			n := entityName(h)
			switch {
			case f.HostSvcs[h] > graphHubMax:
				related = true
				if hub == nil || hub.Reason != chstore.GraphNoHub {
					hub = &pairResult{Reason: chstore.GraphNoHub, Hint: chstore.GraphRuleHost, Via: n}
				}
			case len(f.Hosts[a.Key]) == 1 && len(f.Hosts[b.Key]) == 1:
				return link(pairResult{Rule: chstore.GraphRuleHost, Via: n, Path: []string{a.Name, "host " + n, b.Name}})
			default:
				setHint(chstore.GraphRuleHost, n)
			}
		}
	}
	if hub != nil {
		return *hub
	}
	if related {
		return hint
	}
	if !f.hasData(a) || !f.hasData(b) {
		return pairResult{Reason: chstore.GraphNoGraph}
	}
	return pairResult{Reason: chstore.GraphNoRel}
}

// graphLink — kümeye katılış.
type graphLink struct {
	Unit, Peer int // birim indeksleri
	pairResult
	Hops int
}

// graphCluster — bir kümenin birimleri (ilk = sahip) + katılışlar.
type graphCluster struct {
	Units []int
	Links map[int]graphLink // birim → katılış (sahipte yok)
	Cut   []int             // tavan / uzaklık yüzünden alınmayan bağlı birimler
	// Capped — v0.10.2341 (S4): emme sınırı yüzünden alınmayan bağlı birimler
	// (N-CAP); Onset — kümenin hatırlanan en erken başlangıcı. İkisi de yalnız
	// S4 açıkken dolar.
	Capped []int
	Onset  int64
}

// graphEnforceOpts — v0.10.2341 (S4): kapalıyken sıfır değer (bugünkü plan).
// Onset — önceki tiklerde kümeye alınmış Problem'in (birim temsilcisi)
// kümesinin en erken başlangıcı: sahip kapanıp küme yeniden kurulsa da emme
// sınırı ilk başlangıçtan ölçülür.
type graphEnforceOpts struct {
	On    bool
	Onset map[string]int64
}

// clusterUnits — SAF: birimler başlangıç sırasıyla; atanmamış en erken birim
// sahip olur, bağlar üzerinden genişlik öncelikli (≤ 3 bağ, ≤ 25 varlık).
// strict: M-CALL bağı katı sürümü de sağlamalı.
func clusterUnits(us []graphUnit, pairs [][]pairResult, strict bool) []graphCluster {
	return clusterUnitsOpt(us, pairs, strict, graphEnforceOpts{})
}

// clusterUnitsOpt — clusterUnits + S4 emme sınırı (opt.On): kümenin en erken
// başlangıcından 90 dk sonra başlayan, daha önce kümeye alınmamış birim
// bağlanmaz (servisler arası; birim = varlık, aynı servis zaten tek birim).
func clusterUnitsOpt(us []graphUnit, pairs [][]pairResult, strict bool, opt graphEnforceOpts) []graphCluster {
	assigned := make([]bool, len(us))
	ok := func(i, j int) (pairResult, bool) {
		r := pairs[i][j]
		if r.Rule == "" {
			return r, false
		}
		if strict && r.Rule == chstore.GraphRuleCall && !r.Strict {
			return r, false
		}
		return r, true
	}
	var out []graphCluster
	for s := range us {
		if assigned[s] {
			continue
		}
		assigned[s] = true
		c := graphCluster{Units: []int{s}, Links: map[int]graphLink{}}
		type qe struct{ i, hops int }
		q := []qe{{s, 0}}
		cut := map[int]bool{}
		capped := map[int]bool{}
		if opt.On {
			c.Onset = us[s].Onset
			if r := opt.Onset[us[s].Rep.ID]; r > 0 && r < c.Onset {
				c.Onset = r
			}
		}
		for len(q) > 0 {
			cur := q[0]
			q = q[1:]
			for j := range us {
				if assigned[j] {
					continue
				}
				r, linked := ok(cur.i, j)
				if !linked {
					continue
				}
				if cur.hops+1 > graphMaxHops || len(c.Units) >= graphSizeMax {
					cut[j] = true
					continue
				}
				if opt.On && opt.Onset[us[j].Rep.ID] == 0 && us[j].Onset-c.Onset > graphAbsorbNs {
					capped[j] = true
					continue
				}
				assigned[j] = true
				delete(cut, j)
				delete(capped, j)
				if r := opt.Onset[us[j].Rep.ID]; opt.On && r > 0 && r < c.Onset {
					c.Onset = r
				}
				c.Units = append(c.Units, j)
				c.Links[j] = graphLink{Unit: j, Peer: cur.i, pairResult: r, Hops: cur.hops + 1}
				q = append(q, qe{j, cur.hops + 1})
			}
		}
		for j := range cut {
			if !assigned[j] {
				c.Cut = append(c.Cut, j)
			}
		}
		sort.Ints(c.Cut)
		for j := range capped {
			if !assigned[j] {
				c.Capped = append(c.Capped, j)
			}
		}
		sort.Ints(c.Capped)
		out = append(out, c)
	}
	return out
}

// graphPlan — bir tikin SAF sonucu.
type graphPlan struct {
	Units    []graphUnit
	Clusters []graphCluster // düz sürüm
	Strict   []graphCluster // katı M-CALL sürümü (yalnız boy dağılımı)
	Groups   []chstore.ProblemGraphGroup
	// NoLink — bağlanmayan Problem → neden (tek birimli kümeler + kesilenler + N-KIND).
	NoLink map[string]string
	// GraphMissing — graf verisi olmayan birimlerin Problem kimlikleri.
	GraphMissing []string
}

// reasonRank — bağlanmama nedeninin bilgi değeri (küçük = önce).
func reasonRank(r pairResult) int {
	switch r.Reason {
	case chstore.GraphNoSize:
		return 0
	case chstore.GraphNoHub:
		return 1
	case chstore.GraphNoTime:
		return 2
	case chstore.GraphNoGraph:
		return 3
	case chstore.GraphNoKind:
		return 4
	case chstore.GraphNoRel:
		if r.Hint != "" {
			return 5
		}
		return 6
	}
	return 7
}

// planGraph — SAF: birimler + gerçekler → plan (S4 kapalı: bugünkü plan).
func planGraph(us []graphUnit, kinds []graphKindRow, f *graphFacts) graphPlan {
	return planGraphOpts(us, kinds, f, graphEnforceOpts{})
}

// planGraphOpts — planGraph + S4 seçenekleri (emme sınırı, "geç katıldı",
// küme başlangıcı). opt sıfır değerde çıktı planGraph'ınkiyle aynıdır.
func planGraphOpts(us []graphUnit, kinds []graphKindRow, f *graphFacts, opt graphEnforceOpts) graphPlan {
	plan := graphPlan{Units: us, NoLink: map[string]string{}}
	if f == nil {
		f = &graphFacts{}
	}
	keys := make(map[string]bool, len(us))
	for _, u := range us {
		keys[u.Key] = true
	}
	isUnit := func(k string) bool { return keys[k] }
	pairs := make([][]pairResult, len(us))
	for i := range pairs {
		pairs[i] = make([]pairResult, len(us))
	}
	for i := range us {
		for j := i + 1; j < len(us); j++ {
			r := judgePair(&us[i], &us[j], f, isUnit)
			pairs[i][j], pairs[j][i] = r, r
		}
	}
	plan.Clusters = clusterUnitsOpt(us, pairs, false, opt)
	plan.Strict = clusterUnits(us, pairs, true)
	clusterOf := make([]int, len(us))
	for ci, c := range plan.Clusters {
		for _, u := range c.Units {
			clusterOf[u] = ci
		}
	}
	for i := range us {
		if !f.hasData(&us[i]) {
			plan.GraphMissing = append(plan.GraphMissing, us[i].Rep.ID)
		}
	}
	for _, c := range plan.Clusters {
		near := nearMisses(us, pairs, c, clusterOf, plan.Clusters, kinds)
		if len(c.Units) == 1 {
			// Tek birim: en bilgili bağlanmama nedeni (pencerede ya da ilişkili
			// bir karşı taraf varsa); kesilen birimler N-SIZE.
			if _, done := plan.NoLink[us[c.Units[0]].Rep.ID]; !done && len(near) > 0 {
				plan.NoLink[us[c.Units[0]].Rep.ID] = near[0].Reason
			}
		}
		for _, j := range c.Cut {
			plan.NoLink[us[j].Rep.ID] = chstore.GraphNoSize
		}
		for _, j := range c.Capped {
			if _, done := plan.NoLink[us[j].Rep.ID]; !done {
				plan.NoLink[us[j].Rep.ID] = chstore.GraphNoCap
			}
		}
		if len(c.Units) == 1 && len(near) == 0 {
			continue
		}
		plan.Groups = append(plan.Groups, groupView(us, c, near, f, opt.On))
	}
	for _, k := range kinds {
		if k.Svc != "" && keys[entity.ServiceID(k.Svc)] {
			plan.NoLink[k.P.ID] = chstore.GraphNoKind
		}
	}
	// Önemli küme önce (blob tavanı sondan keser): çok varlıklı, sonra erken.
	sort.SliceStable(plan.Groups, func(i, j int) bool {
		if plan.Groups[i].Entities != plan.Groups[j].Entities {
			return plan.Groups[i].Entities > plan.Groups[j].Entities
		}
		return plan.Groups[i].Members[0].StartedAt < plan.Groups[j].Members[0].StartedAt
	})
	return plan
}

// nearMisses — SAF: kümeye bağlanmayan, aynı pencerede açık ya da graf
// ilişkisi olan birimler (en çok 3, en bilgili neden önce) + N-KIND satırları.
func nearMisses(us []graphUnit, pairs [][]pairResult, c graphCluster, clusterOf []int, cls []graphCluster,
	kinds []graphKindRow) []chstore.ProblemGraphNearMiss {
	in := map[int]bool{}
	for _, u := range c.Units {
		in[u] = true
	}
	type cand struct {
		r    pairResult
		peer int
		v    int
		dist int64
	}
	var cs []cand
	for v := range us {
		if in[v] {
			continue
		}
		var best *cand
		for _, g := range c.Units {
			r := pairs[g][v]
			if r.Rule != "" {
				// Bağ kuralı sağlanıyor ama ayrı kümedeler: kurulum gereği bunun
				// tek nedeni üye tavanı / 3 bağ uzaklık kesimidir.
				r = pairResult{Reason: chstore.GraphNoSize, Hint: r.Rule, Via: r.Via}
			}
			d := absDiff(us[g].Onset, us[v].Onset)
			if r.Hint == "" && (r.Reason == chstore.GraphNoRel || r.Reason == chstore.GraphNoGraph) {
				if d > graphWindowNs {
					continue // ilişkisiz ve pencere dışı: aday değil
				}
			}
			k := cand{r: r, peer: g, v: v, dist: d}
			if best == nil || reasonRank(k.r) < reasonRank(best.r) || reasonRank(k.r) == reasonRank(best.r) && k.dist < best.dist {
				best = &k
			}
		}
		if best != nil {
			cs = append(cs, *best)
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		ri, rj := reasonRank(cs[i].r), reasonRank(cs[j].r)
		if ri != rj {
			return ri < rj
		}
		if cs[i].dist != cs[j].dist {
			return cs[i].dist < cs[j].dist
		}
		return us[cs[i].v].Key < us[cs[j].v].Key
	})
	var out []chstore.ProblemGraphNearMiss
	for _, k := range cs {
		v := us[k.v]
		out = append(out, chstore.ProblemGraphNearMiss{ProblemID: v.Rep.ID, Name: v.Name, Type: v.Type, StartedAt: v.Onset,
			Reason: k.r.Reason, Hint: k.r.Hint, Peer: us[k.peer].Name, Via: k.r.Via, GroupSize: len(cls[clusterOf[k.v]].Units)})
	}
	// N-KIND: kümedeki bir servisin, birleşmeye katılmayan ailedeki satırı.
	for _, kr := range kinds {
		if kr.Svc == "" {
			continue
		}
		for _, g := range c.Units {
			if us[g].Type == entity.TypeService && us[g].Name == kr.Svc && absDiff(us[g].Onset, kr.P.StartedAt) <= graphWindowNs {
				out = append(out, chstore.ProblemGraphNearMiss{ProblemID: kr.P.ID, Name: kr.Svc, Type: entity.TypeService,
					StartedAt: kr.P.StartedAt, Reason: chstore.GraphNoKind, Peer: us[g].Name, GroupSize: 1})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return reasonRank(pairResult{Reason: out[i].Reason, Hint: out[i].Hint}) < reasonRank(pairResult{Reason: out[j].Reason, Hint: out[j].Hint})
	})
	if len(out) > graphNearMax {
		out = out[:graphNearMax]
	}
	return out
}

// groupView — SAF: küme → sayfa / blob görünümü. Satırlar başlangıç
// sırasıyla; birimin bütün satırları (M-SVC) üye satırıdır. ≤ 25 satır.
//
// v0.10.2341 (S4, yalnız enforce): küme başlangıcı (Onset) ve aynı servisin
// emme sınırından sonra başlayan satırına "geç katıldı" (Late) işareti.
func groupView(us []graphUnit, c graphCluster, near []chstore.ProblemGraphNearMiss, f *graphFacts, enforce bool) chstore.ProblemGraphGroup {
	owner := us[c.Units[0]]
	g := chstore.ProblemGraphGroup{OwnerID: owner.Rep.ID, Entities: len(c.Units), NearMisses: near}
	if enforce && len(c.Units) > 1 {
		g.Onset = c.Onset
	}
	var rows []chstore.ProblemGraphMember
	for _, ui := range c.Units {
		u := us[ui]
		missing := !f.hasData(&u)
		l, linked := c.Links[ui]
		for _, p := range u.Rows {
			m := chstore.ProblemGraphMember{ProblemID: p.ID, Entity: u.Key, Type: u.Type, Name: u.Name, RuleID: p.RuleID,
				Title: p.RuleName, Severity: p.Severity, StartedAt: p.StartedAt, GraphMissing: missing}
			switch {
			case p.ID == owner.Rep.ID:
				m.Owner = true
			case p.ID != u.Rep.ID:
				m.Link, m.Peer = chstore.GraphRuleSvc, u.Name
				m.Late = enforce && len(c.Units) > 1 && p.StartedAt-c.Onset > graphAbsorbNs
			case linked:
				m.Link, m.Peer, m.Via, m.Path, m.Strict = l.Rule, us[l.Peer].Name, l.Via, l.Path, l.Strict
			}
			rows = append(rows, m)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].StartedAt != rows[j].StartedAt {
			return rows[i].StartedAt < rows[j].StartedAt
		}
		return rows[i].ProblemID < rows[j].ProblemID
	})
	if len(rows) > graphMemberRows {
		rows, g.Truncated = rows[:graphMemberRows], true
	}
	g.Members = rows
	return g
}

// graphAgeBucket — SAF: sahibin yaşı (dk) kovası (§7 gölge sayacı).
func graphAgeBucket(ageNs int64) string {
	switch m := ageNs / int64(60*1e9); {
	case m < 30:
		return "0-30"
	case m < 60:
		return "30-60"
	case m < 90:
		return "60-90"
	}
	return ">90"
}

// graphSizeBucket — SAF: küme boyu kovası.
func graphSizeBucket(n int) string {
	switch {
	case n <= 3:
		return string(rune('0' + n))
	case n <= 5:
		return "4-5"
	case n <= 10:
		return "6-10"
	}
	return "11-25"
}
