package api

// inbox_graph_fold.go — v0.10.2341 (entity grafı ile Problem birleştirme S4,
// docs/problems/entity-graph-merge-design.md §2, §4 "Liste satırı", §9):
// Problems listesi bağlı kümeyi TEK satırda katlar. KAPALI GELİR: yalnız
// chstore.ProblemGraphMergeEnforced() açıkken çağrılır; kapalıyken liste,
// sayaçlar, önbellek anahtarı ve gövde bugünküyle bayt bayt aynı.
//
// Kural (incident katlamasıyla aynı yerde: görünüm öncelikleri kesinleştikten
// SONRA, sayaçlardan / sıralamadan / tavandan ÖNCE — çip sayıları ve menü
// rozeti kümeyi bir kez sayar; rozet aynı derlemeyi okur):
//   - ≥ 2 varlıklı gölge kümenin listede bulunan üye satırları tek satıra
//     katlanır. Temsilci = sahibin satırı; sahip listede yoksa (süzgeç eledi)
//     listede bulunan en erken üye — gizlenen her satırın yerini tutan bir
//     satır ekranda olur. Listede tek üye varsa katlama yok.
//   - Temsilci: öncelik = üyelerin görünüm önceliklerinin en yükseği, ilk
//     görülme en erken, son görülme en geç; Cluster alanı üyeleri (kimlik,
//     servis, başlık, öncelik, başlangıç, bağ kuralı, "geç katıldı") taşır —
//     katlanan her üye satırı oradan açılır.
//   - Hiçbir Problem kapatılmaz, değiştirilmez (karar 1); yalnız görünüm.
//
// Küme kaynağı evaluator liderinin gölge blobu (tek ayar okuması). Okuma
// düşerse katlama yok (bugünkü düz liste — hiçbir satır kaybolmaz).

import (
	"context"
	"log"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// InboxClusterRef — katlanan kümenin özeti (yalnız S4 açıkken).
type InboxClusterRef struct {
	OwnerID  string               `json:"ownerId"`
	Entities int                  `json:"entities"`
	Origin   string               `json:"origin,omitempty"`
	Members  []InboxClusterMember `json:"members"`
}

// InboxClusterMember — kümedeki bir Problem satırı (listede bulunan).
type InboxClusterMember struct {
	ProblemID string `json:"problemId"`
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	Priority  string `json:"priority,omitempty"`
	StartedAt int64  `json:"startedAt"`
	Owner     bool   `json:"owner,omitempty"`
	Link      string `json:"link,omitempty"`
	Late      bool   `json:"late,omitempty"`
}

// foldGraphClusters — SAF: yukarıdaki kural. Girdi sırası korunur.
func foldGraphClusters(items []InboxItem, groups []chstore.ProblemGraphGroup) []InboxItem {
	if len(groups) == 0 {
		return items
	}
	byProblem := map[string]int{}
	for i, it := range items {
		if it.Kind == "problem" && it.Problem != nil && it.Problem.ID != "" {
			byProblem[it.Problem.ID] = i
		}
	}
	if len(byProblem) < 2 {
		return items
	}
	hidden := map[int]bool{}
	for _, g := range groups {
		if g.Entities < 2 {
			continue
		}
		var present []int
		var mem []chstore.ProblemGraphMember
		for _, m := range g.Members {
			if j, ok := byProblem[m.ProblemID]; ok && !hidden[j] && items[j].Cluster == nil {
				present = append(present, j)
				mem = append(mem, m)
			}
		}
		if len(present) < 2 {
			continue
		}
		rep := 0
		for k, m := range mem {
			if m.ProblemID == g.OwnerID {
				rep = k
			}
		}
		ri := present[rep]
		ref := &InboxClusterRef{OwnerID: g.OwnerID, Entities: g.Entities}
		if g.Origin != nil {
			ref.Origin = g.Origin.Name
		}
		for k, j := range present {
			p := items[j]
			ref.Members = append(ref.Members, InboxClusterMember{ProblemID: mem[k].ProblemID, Name: mem[k].Name, Title: p.Title,
				Priority: p.Priority, StartedAt: p.StartedAt, Owner: mem[k].Owner, Link: mem[k].Link, Late: mem[k].Late})
			if j == ri {
				continue
			}
			hidden[j] = true
			if priorityRank(p.Priority) > priorityRank(items[ri].Priority) {
				items[ri].Priority = p.Priority
				items[ri].PriorityReason = "bağlı problem: " + p.PriorityReason
				items[ri].PriorityReasonMsg = nestedReasonMsg("prio.linked", nil, p.PriorityReasonMsg)
			}
			if p.StartedAt > 0 && (items[ri].StartedAt == 0 || p.StartedAt < items[ri].StartedAt) {
				items[ri].StartedAt = p.StartedAt
			}
			if p.LastSeen > items[ri].LastSeen {
				items[ri].LastSeen = p.LastSeen
			}
		}
		items[ri].Cluster = ref
	}
	if len(hidden) == 0 {
		return items
	}
	kept := make([]InboxItem, 0, len(items)-len(hidden))
	for i, it := range items {
		if !hidden[i] {
			kept = append(kept, it)
		}
	}
	return kept
}

// foldInboxGraphClusters — G/Ç yarısı: blob okuması + SAF katlama.
func (s *Server) foldInboxGraphClusters(ctx context.Context, items []InboxItem) []InboxItem {
	rep, ok, err := s.store.GetProblemGraphShadow(ctx)
	if err != nil {
		log.Printf("[inbox] graf katlaması: gölge blobu okunamadı, satırlar katlanmadı: %v", err)
		return items
	}
	if !ok {
		return items
	}
	return foldGraphClusters(items, rep.Groups)
}

// inboxGraphKeySuffix — S4 açıkken önbellek anahtarı eki; kapalıyken "".
func inboxGraphKeySuffix(on bool) string {
	if on {
		return ":graphMerge=1"
	}
	return ""
}

// withInboxGraphMode — S4 açıkken gövdeye `graphMerge: true` (ön yüz kipi
// API'den öğrenir); kapalıyken gövde aynen.
func withInboxGraphMode(body map[string]any, on bool) map[string]any {
	if on {
		body["graphMerge"] = true
	}
	return body
}
