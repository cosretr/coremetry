package evaluator

// problem_graph_origin.go — v0.10.2278 (entity grafı ile Problem birleştirme
// S2, docs/problems/entity-graph-merge-design.md §3, §5): olası köken ve etki
// alanı. SALT OKUNUR gölge: sonuç yalnız S1 blobunun kümesine (Origin) yazılır;
// Problem, sahip, üye, durum, öncelik, bildirim ve liste sırası değişmez.
// LLM yok, oy / karar okunmaz.
//
// Köken: sıralı kurallar, puan yok — K-ROLLOUT → K-SHARED → K-DEEP → K-FIRST;
// her kural bağımsız değerlendirilir ve TEK bir önde aday çıkarırsa "ayırdı"
// sayılır. Kazanan, ilk ayıran kuralın lideridir. Güven: kazanan ≥ 2 kuralda
// önde → strong ("güçlü"), tek kuralda → weak ("zayıf"); hiçbir kural
// ayırmadıysa unclear ("köken belirsiz", en erken başlayan üye adıyla).
//
// Adaylar: kümenin birimleri + üyelerdeki bağımlılık olaylarıyla kanıtlı,
// Problem'i olmayan ortak bağımlılıklar (≥ 2 üye, N-HUB değil). İş yükü /
// node / host'un kendi sinyal ailesi olmadığından aday değildir.
//
// Etki alanı (§5): kökenin called_by kenarları, 2 adım; adım başına en çok
// 100 servis. Okumalar S1 okuma turuna bağlıdır (aynı 5 dk hatırlama, aday
// kümesi özeti): workload_rollouts tek okuma (≤ 50 iş yükü, LIMIT 200,
// 10 sn) + entity_relations called_by en çok 2 okuma (≤ 500 uç, LIMIT
// 5000, 10 sn). Hatırlama ıskası dışında tik başına ek okuma yok.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/rollout"
)

const (
	originTieNs     = int64(5 * 60 * 1e9)  // K-FIRST / K-ROLLOUT: 5 dk içi eşit
	originRolloutNs = int64(30 * 60 * 1e9) // K-ROLLOUT: başlangıçtan önceki 30 dk
	blastHopMax     = 100                  // etki alanı: adım başına servis
	blastNamesMax   = 8                    // blobda yazılan ilk ad sayısı
)

// originFacts — S2 okuma turunun gerçekleri (S1 turuyla birlikte hatırlanır).
type originFacts struct {
	RolloutOK bool
	Rollouts  map[string][]int64 // iş yükü entity id → rollout başlangıçları (unix ns)
	BlastOK   bool
	BlastCut  bool                // called_by okuması LIMIT'e değdi
	CalledBy  map[string][]string // varlık → çağıran servis entity id'leri
}

// originCand — köken adayı (birim ya da kanıtlı ortak bağımlılık).
type originCand struct {
	Key, Name, Type string
	Unit            int   // birim indeksi; ortak bağımlılıkta -1
	Onset           int64 // ortak bağımlılıkta 0 (kendi Problem'i yok)
	Deps            []int // K-SHARED: ona bağlı küme birimleri
}

// originCands — SAF: kümenin köken adayları (birimler + kanıtlı ortak bağımlılıklar).
func originCands(us []graphUnit, c graphCluster, f *graphFacts) []originCand {
	inAll := map[string]bool{}
	for _, u := range us {
		inAll[u.Key] = true
	}
	inC := map[string]int{}
	for _, ui := range c.Units {
		inC[us[ui].Key] = ui
	}
	var out []originCand
	for _, ui := range c.Units {
		u := us[ui]
		cd := originCand{Key: u.Key, Name: u.Name, Type: u.Type, Unit: ui, Onset: u.Onset}
		if f.CallsOK {
			for _, vi := range c.Units {
				if vi != ui && us[vi].Type == entity.TypeService && contains(f.Callees[us[vi].Key], u.Key) {
					cd.Deps = append(cd.Deps, vi)
				}
			}
		}
		out = append(out, cd)
	}
	if !f.CallsOK || !f.EvOK || !f.HubOK || f.HubTrunc {
		return out
	}
	shared := map[string][]int{}
	for _, ui := range c.Units {
		u := us[ui]
		if u.Type != entity.TypeService {
			continue
		}
		for _, x := range f.Callees[u.Key] {
			if !inAll[x] && f.depEvidence(u.Name, x) && !containsInt(shared[x], ui) {
				shared[x] = append(shared[x], ui)
			}
		}
	}
	keys := make([]string, 0, len(shared))
	for x, deps := range shared {
		if len(deps) >= 2 && f.Callers[x] <= graphHubMax {
			keys = append(keys, x)
		}
	}
	sort.Strings(keys)
	for _, x := range keys {
		ref, _ := entity.ParseID(x)
		out = append(out, originCand{Key: x, Name: entityName(x), Type: ref.Type, Unit: -1, Deps: shared[x]})
	}
	return out
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func candNames(cs []originCand, idx []int) []string {
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, cs[i].Name)
	}
	return out
}

// computeOrigin — SAF: kümenin olası kökeni (etki alanı hariç).
func computeOrigin(us []graphUnit, c graphCluster, f *graphFacts, of *originFacts) *chstore.ProblemGraphOrigin {
	if len(c.Units) < 2 {
		return nil
	}
	if of == nil {
		of = &originFacts{}
	}
	cs := originCands(us, c, f)
	// En erken başlayan birim (ve ikincisi) — K-FIRST ve belirsiz cümlesi.
	units := make([]int, 0, len(c.Units))
	for i, cd := range cs {
		if cd.Unit >= 0 {
			units = append(units, i)
		}
	}
	sort.SliceStable(units, func(a, b int) bool {
		if cs[units[a]].Onset != cs[units[b]].Onset {
			return cs[units[a]].Onset < cs[units[b]].Onset
		}
		return cs[units[a]].Key < cs[units[b]].Key
	})
	first := cs[units[0]]
	o := &chstore.ProblemGraphOrigin{Earliest: first.Name, EarliestAt: first.Onset}
	leaders := map[string]int{} // kural → aday indeksi (yalnız ayıranlar)

	// 1) K-ROLLOUT
	rr := chstore.ProblemGraphOriginRule{Rule: chstore.OriginRuleRollout, Source: chstore.OriginSrcRollout, Off: !of.RolloutOK || !f.ServesOK}
	if !rr.Off {
		var hit []int
		var via []string
		var at []int64
		for _, i := range units {
			cd := cs[i]
			if cd.Type != entity.TypeService || cd.Onset-first.Onset > originTieNs {
				continue
			}
			for _, wl := range f.WL[cd.Key] {
				for _, t := range of.Rollouts[wl] {
					if t <= cd.Onset && cd.Onset-t <= originRolloutNs {
						hit, via, at = append(hit, i), append(via, entityName(wl)), append(at, t)
						break
					}
				}
				if len(hit) > 0 && hit[len(hit)-1] == i {
					break
				}
			}
		}
		if len(hit) == 1 {
			rr.Separated, rr.Leader, rr.Via, rr.At = true, cs[hit[0]].Name, via[0], at[0]
			leaders[rr.Rule] = hit[0]
		} else if len(hit) > 1 {
			rr.Others = candNames(cs, hit)
		}
	}
	// 2) K-SHARED
	rs := chstore.ProblemGraphOriginRule{Rule: chstore.OriginRuleShared, Source: chstore.OriginSrcCalls, Off: !f.CallsOK}
	if !rs.Off {
		best, n, tie := -1, 0, false
		for i, cd := range cs {
			switch {
			case len(cd.Deps) > n:
				best, n, tie = i, len(cd.Deps), false
			case len(cd.Deps) == n && n > 0:
				tie = true
			}
		}
		if best >= 0 && n >= 2 && !tie {
			rs.Separated, rs.Leader = true, cs[best].Name
			for _, d := range cs[best].Deps {
				rs.Others = append(rs.Others, us[d].Name)
			}
			if cs[best].Unit < 0 {
				rs.Source = chstore.OriginSrcDepEv
			}
			leaders[rs.Rule] = best
		}
	}
	// 3) K-DEEP — üyeler arasında çağrılan, kendisi sapmış bir üyeyi çağırmayan.
	rd := chstore.ProblemGraphOriginRule{Rule: chstore.OriginRuleDeep, Source: chstore.OriginSrcCalls, Off: !f.CallsOK}
	if !rd.Off {
		var leaves []int
		for i, cd := range cs {
			if len(cd.Deps) == 0 {
				continue // çağrı zincirinde değil
			}
			calls := false
			for j, other := range cs {
				if j != i && contains(f.Callees[cd.Key], other.Key) {
					calls = true
					break
				}
			}
			if !calls {
				leaves = append(leaves, i)
			}
		}
		if len(leaves) == 1 {
			rd.Separated, rd.Leader = true, cs[leaves[0]].Name
			leaders[rd.Rule] = leaves[0]
		} else if len(leaves) > 1 {
			rd.Others = candNames(cs, leaves)
		}
	}
	// 4) K-FIRST — 5 dk içindeki farklar eşit.
	rf := chstore.ProblemGraphOriginRule{Rule: chstore.OriginRuleFirst, Source: chstore.OriginSrcOnset, Leader: first.Name, At: first.Onset}
	if len(units) > 1 {
		second := cs[units[1]]
		rf.Runner, rf.RunAt = second.Name, second.Onset
		if second.Onset-first.Onset >= originTieNs {
			rf.Separated = true
			leaders[rf.Rule] = units[0]
		}
	}
	o.Rules = []chstore.ProblemGraphOriginRule{rr, rs, rd, rf}
	win := -1
	for _, r := range o.Rules {
		if i, ok := leaders[r.Rule]; ok {
			win = i
			o.Rule = r.Rule
			break
		}
	}
	if win < 0 {
		o.Confidence = chstore.OriginUnclear
		return o
	}
	for _, r := range o.Rules {
		if i, ok := leaders[r.Rule]; ok && i == win {
			o.Leads = append(o.Leads, r.Rule)
		}
	}
	w := cs[win]
	o.Entity, o.Name, o.Type, o.OnsetAt = w.Key, w.Name, w.Type, w.Onset
	o.Confidence = chstore.OriginWeak
	if len(o.Leads) >= 2 {
		o.Confidence = chstore.OriginStrong
	}
	return o
}

// clusterMemberKeys — SAF: kümenin birim entity kimlikleri.
func clusterMemberKeys(us []graphUnit, c graphCluster) map[string]string {
	m := map[string]string{}
	for _, ui := range c.Units {
		m[us[ui].Key] = us[ui].Name
	}
	return m
}

func svcCallers(of *originFacts, id string) []string {
	var out []string
	for _, p := range of.CalledBy[id] {
		if strings.HasPrefix(p, "svc:") && !contains(out, p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// blastHop1 — SAF: kökenin ilk adım çağıranları (≤ 100).
func blastHop1(of *originFacts, origin string) ([]string, bool) {
	cs := svcCallers(of, origin)
	if len(cs) > blastHopMax {
		return cs[:blastHopMax], true
	}
	return cs, false
}

// blastSeeds — SAF: ikinci adımın tohumları = ilk adım + köken dışı üyeler.
func blastSeeds(hop1 []string, members map[string]string, origin string) []string {
	out := append([]string(nil), hop1...)
	for k := range members {
		if k != origin && !contains(out, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// computeBlast — SAF: etki alanı. Etkilenen = köken dışı üyeler (ölçülmüş
// sapma); risk altında = kökeni (1) ya da ilk adımı / etkilenenleri (2)
// çağıran, sapması olmayan servisler. busy = açık Problem'i olan ama kümede
// olmayan varlıklar (sapmalı: risk listesine girmez).
func computeBlast(o *chstore.ProblemGraphOrigin, members map[string]string, busy map[string]bool, of *originFacts) *chstore.ProblemGraphBlast {
	b := &chstore.ProblemGraphBlast{Affected: []string{}}
	for k, n := range members {
		if k != o.Entity {
			b.Affected = append(b.Affected, n)
		}
	}
	sort.Strings(b.Affected)
	if of == nil || !of.BlastOK {
		b.Off = true
		return b
	}
	hop1, cut1 := blastHop1(of, o.Entity)
	seen := map[string]bool{o.Entity: true}
	var r1 []string
	for _, c := range hop1 {
		seen[c] = true
		if _, m := members[c]; !m && !busy[c] {
			r1 = append(r1, entityName(c))
		}
	}
	var hop2 []string
	for _, s := range blastSeeds(hop1, members, o.Entity) {
		for _, c := range svcCallers(of, s) {
			if !seen[c] {
				seen[c] = true
				hop2 = append(hop2, c)
			}
		}
	}
	sort.Strings(hop2)
	cut2 := len(hop2) > blastHopMax
	if cut2 {
		hop2 = hop2[:blastHopMax]
	}
	var r2 []string
	for _, c := range hop2 {
		if _, m := members[c]; !m && !busy[c] {
			r2 = append(r2, entityName(c))
		}
	}
	b.AtRisk1N, b.AtRisk2N = len(r1), len(r2)
	if len(r1) > blastNamesMax {
		r1 = r1[:blastNamesMax]
	}
	if len(r2) > blastNamesMax {
		r2 = r2[:blastNamesMax]
	}
	b.AtRisk1, b.AtRisk2 = r1, r2
	b.Cut = cut1 || cut2 || of.BlastCut
	return b
}

// rolloutKeysFor — SAF: kümelerdeki servis birimlerinin iş yükü anahtarları
// (≤ 50; RolloutsForWorkloads da 50'de keser) + id eşlemesi + pencere.
func rolloutKeysFor(us []graphUnit, cls []graphCluster, f *graphFacts) ([]rollout.Key, map[rollout.Key]string, int64, int64) {
	var keys []rollout.Key
	ids := map[rollout.Key]string{}
	var lo, hi int64
	for _, c := range cls {
		if len(c.Units) < 2 {
			continue
		}
		for _, ui := range c.Units {
			u := us[ui]
			if u.Type != entity.TypeService {
				continue
			}
			if lo == 0 || u.Onset < lo {
				lo = u.Onset
			}
			if u.Onset > hi {
				hi = u.Onset
			}
			for _, wl := range f.WL[u.Key] {
				ref, ok := entity.ParseID(wl)
				if !ok || ref.Type != entity.TypeWorkload {
					continue
				}
				k := rollout.Key{ClusterID: ref.ClusterID, Namespace: ref.Namespace, Workload: ref.Name}
				if _, dup := ids[k]; !dup && len(keys) < 50 {
					ids[k] = wl
					keys = append(keys, k)
				}
			}
		}
	}
	return keys, ids, lo - originRolloutNs, hi
}

// fetchOriginFacts — S2 okumaları (S1 turu ıskasında bir kez): rollout (tek)
// + called_by (en çok 2). Okuma hatası = o kural / etki alanı bu tur kapalı.
func fetchOriginFacts(ctx context.Context, st graphShadowStore, p graphPlan, f *graphFacts, rd *chstore.ProblemGraphReads) *originFacts {
	of := &originFacts{Rollouts: map[string][]int64{}, CalledBy: map[string][]string{}}
	multi := false
	for _, c := range p.Clusters {
		if len(c.Units) > 1 {
			multi = true
			break
		}
	}
	if !multi {
		return of
	}
	keys, ids, from, to := rolloutKeysFor(p.Units, p.Clusters, f)
	if len(keys) == 0 {
		of.RolloutOK = true // sorulacak iş yükü yok: kural "ayırmadı"
	} else {
		rd.Rollouts++
		rows, err := st.RolloutsForWorkloads(ctx, keys, time.Unix(0, from), time.Unix(0, to))
		if err != nil {
			rd.Errors++
			logGraphReadErr("rollout kayıtları", err)
		} else {
			for _, r := range rows {
				k := rollout.Key{ClusterID: r.ClusterID, Namespace: r.Namespace, Workload: r.Workload}
				if id, ok := ids[k]; ok {
					of.Rollouts[id] = append(of.Rollouts[id], r.StartedAt.UnixNano())
				}
			}
			of.RolloutOK = true
		}
	}
	if !f.CallsOK {
		return of // calls bayat / okunamadı: etki alanı kapalı (tahmin yok)
	}
	// Kökenler (rollout gerçekleriyle) → ilk adım okuması.
	var origins []string
	members := map[string]map[string]string{}
	for _, c := range p.Clusters {
		if o := computeOrigin(p.Units, c, f, of); o != nil && o.Entity != "" && !contains(origins, o.Entity) {
			origins = append(origins, o.Entity)
			members[o.Entity] = clusterMemberKeys(p.Units, c)
		}
	}
	if len(origins) == 0 {
		of.BlastOK = true
		return of
	}
	sort.Strings(origins)
	if len(origins) > graphMaxUnits {
		origins = origins[:graphMaxUnits]
	}
	read := func(ids []string) bool {
		rd.Blast++
		edges, err := st.EntityRelationsFor(ctx, entity.RelCalls, ids, true, time.Time{}, graphRelLimit)
		if err != nil {
			rd.Errors++
			logGraphReadErr("called_by kenarları", err)
			return false
		}
		for _, e := range edges {
			if !contains(of.CalledBy[e.ChildID], e.ParentID) {
				of.CalledBy[e.ChildID] = append(of.CalledBy[e.ChildID], e.ParentID)
			}
		}
		if len(edges) >= graphRelLimit {
			of.BlastCut = true
		}
		return true
	}
	if !read(origins) {
		return of
	}
	var seeds []string
	for _, o := range origins {
		h1, _ := blastHop1(of, o)
		for _, s := range blastSeeds(h1, members[o], o) {
			if !contains(seeds, s) {
				seeds = append(seeds, s)
			}
		}
	}
	sort.Strings(seeds)
	if len(seeds) > graphMaxUnits {
		seeds, of.BlastCut = seeds[:graphMaxUnits], true
	}
	if len(seeds) == 0 || read(seeds) {
		of.BlastOK = true
	}
	return of
}

// attachOrigins — SAF: planın ≥ 2 varlıklı kümelerine köken + etki alanı.
func attachOrigins(p *graphPlan, f *graphFacts, of *originFacts) {
	byOwner := map[string]int{}
	for i, g := range p.Groups {
		byOwner[g.OwnerID] = i
	}
	busy := map[string]bool{}
	for _, u := range p.Units {
		busy[u.Key] = true
	}
	for _, c := range p.Clusters {
		gi, ok := byOwner[p.Units[c.Units[0]].Rep.ID]
		if !ok || len(c.Units) < 2 {
			continue
		}
		o := computeOrigin(p.Units, c, f, of)
		if o == nil {
			continue
		}
		if o.Entity != "" {
			o.Blast = computeBlast(o, clusterMemberKeys(p.Units, c), busy, of)
		}
		p.Groups[gi].Origin = o
	}
}
