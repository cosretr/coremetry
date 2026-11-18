package evaluator

// problem_graph_merge.go — v0.10.2341 (entity grafı ile Problem birleştirme
// S4, docs/problems/entity-graph-merge-design.md §2, §7, §9): "birleşmeyi
// uygula" yolunun evaluator yarısı. KAPALI GELİR
// (chstore.ProblemGraphMergeEnforced).
//
//   - mergeHoldsOf (SAF): plandan "sonradan bağlanan üye" listesi — ≥ 2
//     varlıklı kümede sahibin satırı dışında, sahipten SONRA başlayan her
//     satır; kuralı (M-*), kümenin o üyeden önceki en yüksek önceliği /
//     büyüklüğü, aynı serviste emme sınırı sonrası "geç katıldı".
//   - Gölge pası (her iki kipte) bu listeyle 24 sa sayaçlarını tutar: kaç
//     açılış tutulurdu, bunlardan kaçı sonradan P1'e çıktı (geçiş ölçütü:
//     sıfır). Kapalıyken plan, blob kümeleri ve bildirim AYNI kalır; yalnız
//     admin raporuna sayaç eklenir.
//   - MergeHold (notify.MergeHoldSource): bildirim anında Problem'in kümesi.
//     Yalnız S4 açıkken çağrılır. Açık Problem anlık görüntüsü (süreç başına
//     5 sn önbellek) + bu Problem; graf gerçekleri kendi 5 dk hatırlamasıyla
//     (aday kümesi özeti; gölge pasıyla aynı sınırlı okumalar); kümelerin
//     hatırlanan başlangıcı gölge blobundan. Okuma hatası / graf yok = üye
//     değil (tutma yok; güvenli yön).
//
// "Problem değil" kararları ve oylar OKUNMAZ; LLM yok; hiçbir Problem
// kapatılmaz, yazılmaz.

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const mergeP1ListMax = 20

// mergeCand — sonradan bağlanan üye adayı.
type mergeCand struct {
	hold chstore.MergeHold
	row  chstore.Problem
	prio string // görünüm önceliği
}

// graphViewPriority — SAF (yapılandırma okur): satırın bildirim görünüm önceliği.
func graphViewPriority(p chstore.Problem, keep []string) string {
	if p.Priority == "" {
		if out := chstore.EnrichProblemsWithPriority([]chstore.Problem{p}); len(out) == 1 {
			p = out[0]
		}
	}
	return chstore.NotifyViewPriority(p, keep)
}

// mergeHoldsOf — SAF: plandaki sonradan bağlanan üyeler (Problem → aday).
func mergeHoldsOf(plan graphPlan, keep []string) map[string]mergeCand {
	out := map[string]mergeCand{}
	for _, c := range plan.Clusters {
		if len(c.Units) < 2 {
			continue
		}
		owner := plan.Units[c.Units[0]]
		onset := c.Onset
		if onset == 0 {
			onset = owner.Onset
		}
		type row struct {
			p    chstore.Problem
			unit int
		}
		var rows []row
		for _, ui := range c.Units {
			for _, p := range plan.Units[ui].Rows {
				rows = append(rows, row{p: p, unit: ui})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].p.StartedAt != rows[j].p.StartedAt {
				return rows[i].p.StartedAt < rows[j].p.StartedAt
			}
			return rows[i].p.ID < rows[j].p.ID
		})
		ownerRow := owner.Rep
		refPrio, refMag := "", 0.0
		for _, r := range rows {
			prio := graphViewPriority(r.p, keep)
			mag := chstore.StdMagnitude(chstore.StandardizeProblem(r.p, chstore.StdFacts{Now: time.Unix(0, r.p.StartedAt)}))
			if r.p.ID != ownerRow.ID && r.p.StartedAt > owner.Onset {
				u := plan.Units[r.unit]
				h := chstore.MergeHold{OwnerID: ownerRow.ID, OwnerName: owner.Name, RefPriority: refPrio, RefMagnitude: refMag}
				o := ownerRow
				h.Owner = &o
				if r.unit == c.Units[0] || r.p.ID != u.Rep.ID {
					h.Rule = chstore.GraphRuleSvc
					h.Late = r.p.StartedAt-onset > graphAbsorbNs
				} else {
					h.Rule = c.Links[r.unit].Rule
				}
				out[r.p.ID] = mergeCand{hold: h, row: r.p, prio: prio}
			}
			if prioRankEval(prio) > prioRankEval(refPrio) {
				refPrio = prio
			}
			if mag > refMag {
				refMag = mag
			}
		}
	}
	return out
}

func prioRankEval(p string) int {
	switch p {
	case "P1":
		return 3
	case "P2":
		return 2
	case "P3":
		return 1
	}
	return 0
}

// mergeSeenEntry — 24 sa rapor sayacı (Problem başına).
type mergeSeenEntry struct {
	name     string
	prio0    string
	hold     bool // tutulurdu (P1 değil, şiddet artmadı, P3 değil)
	late     bool
	becameP1 bool
	at       int64
}

// observeMerge — gölge pası (her iki kip): sonradan bağlanan üyelerin 24 sa
// sayaçları. Kapalıyken de sayar; plan ve blob kümelerine dokunmaz.
func (s *graphShadowState) observeMerge(plan graphPlan, keep []string, now time.Time) {
	if s.mergeSeen == nil {
		s.mergeSeen = map[string]*mergeSeenEntry{}
	}
	at := now.Unix()
	for id, c := range mergeHoldsOf(plan, keep) {
		e := s.mergeSeen[id]
		if e == nil {
			e = &mergeSeenEntry{name: c.hold.OwnerName, prio0: c.prio,
				hold: c.prio != "P3" && chstore.MergeWouldHold(c.row, c.prio, c.hold, now)}
			s.mergeSeen[id] = e
		}
		e.at, e.late = at, e.late || c.hold.Late
		if e.hold && e.prio0 != "P1" && c.prio == "P1" {
			e.becameP1 = true
		}
	}
	cut := now.Add(-graphReportWindow).Unix()
	for id, e := range s.mergeSeen {
		if e.at < cut {
			delete(s.mergeSeen, id)
		}
	}
}

// mergeReport — 24 sa sayaçları (S4 geçiş ölçütü).
func (s *graphShadowState) mergeReport() *chstore.ProblemGraphMergeReport {
	r := &chstore.ProblemGraphMergeReport{Enforce: chstore.ProblemGraphMergeEnforced()}
	for id, e := range s.mergeSeen {
		r.Joined++
		if e.late {
			r.Late++
		}
		if !e.hold {
			continue
		}
		r.WouldHold++
		if e.becameP1 {
			r.HeldBecameP1++
			if len(r.BecameP1) < mergeP1ListMax {
				r.BecameP1 = append(r.BecameP1, chstore.FrequentP1{ProblemID: id, Name: e.name})
			}
		}
	}
	sort.Slice(r.BecameP1, func(i, j int) bool { return r.BecameP1[i].ProblemID < r.BecameP1[j].ProblemID })
	return r
}

// rememberAbsorb — yalnız S4 açıkken: kümeye alınan birim temsilcisi →
// kümenin en erken başlangıcı; açık olmayan satır unutulur.
func (s *graphShadowState) rememberAbsorb(plan graphPlan) {
	if s.absorb == nil {
		s.absorb = map[string]int64{}
	}
	live := map[string]bool{}
	for _, u := range plan.Units {
		live[u.Rep.ID] = true
	}
	for id := range s.absorb {
		if !live[id] {
			delete(s.absorb, id)
		}
	}
	for _, c := range plan.Clusters {
		if len(c.Units) < 2 {
			continue
		}
		for _, ui := range c.Units {
			id := plan.Units[ui].Rep.ID
			if r, ok := s.absorb[id]; !ok || c.Onset < r {
				s.absorb[id] = c.Onset
			}
		}
	}
}

// mergeHoldState — bildirim anı kararının kendi graf hatırlaması (gölge
// pasının sayaçlarına dokunmaz).
type mergeHoldState struct {
	mu sync.Mutex
	st *graphShadowState
}

// decide — Problem'in sonradan bağlanan üye kararı (test edilebilir gövde).
func (h *mergeHoldState) decide(ctx context.Context, st graphShadowStore, live GraphLiveSource, open []*chstore.Problem,
	p chstore.Problem, rem map[string]int64, pol chstore.OwnerPolicy, now time.Time) (chstore.MergeHold, bool) {
	rows := make([]*chstore.Problem, 0, len(open)+1)
	cp := p
	replaced := false
	for _, o := range open {
		if o != nil && o.ID == p.ID {
			rows, replaced = append(rows, &cp), true
			continue
		}
		rows = append(rows, o)
	}
	if !replaced {
		rows = append(rows, &cp)
	}
	units, kinds := buildGraphUnits(rows, pol)
	if len(units) < 2 {
		return chstore.MergeHold{}, false
	}
	if len(units) > graphMaxUnits {
		units = units[:graphMaxUnits]
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st == nil {
		h.st = newGraphShadowState(now)
	}
	facts := h.st.factsFor(ctx, st, live, units, now)
	plan := planGraphOpts(units, kinds, facts, graphEnforceOpts{On: true, Onset: rem})
	c, ok := mergeHoldsOf(plan, pol.Keep)[p.ID]
	return c.hold, ok
}

// rememberedOnsets — SAF: blob kümelerinin S4 başlangıcı (üye → başlangıç).
func rememberedOnsets(r chstore.ProblemGraphShadow) map[string]int64 {
	out := map[string]int64{}
	for _, g := range r.Groups {
		if g.Onset <= 0 {
			continue
		}
		for _, m := range g.Members {
			out[m.ProblemID] = g.Onset
		}
	}
	return out
}

// MergeHold — notify.MergeHoldSource. Yalnız S4 açıkken anlamlı; kapalıyken
// ok=false (notify zaten sormaz).
func (e *Evaluator) MergeHold(ctx context.Context, p chstore.Problem) (chstore.MergeHold, bool) {
	if e == nil || e.store == nil || p.ID == "" || p.Status != "open" || !chstore.ProblemGraphMergeEnforced() {
		return chstore.MergeHold{}, false
	}
	if k, _, _ := graphUnitKey(p); k == "" {
		return chstore.MergeHold{}, false
	}
	snap, err := e.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		log.Printf("[evaluator] problem_graph_merge: açık Problem okunamadı (tutma yok): %v", err)
		return chstore.MergeHold{}, false
	}
	var rem map[string]int64
	if rep, ok, err := e.store.GetProblemGraphShadow(ctx); err == nil && ok {
		rem = rememberedOnsets(rep)
	}
	now := time.Now()
	prio := chstore.CurrentProblemPriority()
	pol := chstore.OwnerPolicy{Keep: prio.InboxKeepSourcePriorityList(), Priority: prio, NowNs: now.UnixNano()}
	return e.graphShadow.hold.decide(ctx, e.store, e.problemGraphLive(), snap.All(), p, rem, pol, now)
}
