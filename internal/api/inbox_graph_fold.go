package api

// inbox_graph_fold.go — v0.10.2341 (entity grafı ile Problem birleştirme S4,
// docs/problems/entity-graph-merge-design.md §2, §4 "Liste satırı", §9):
// Problems listesi bağlı kümeyi TEK satırda katlar. v0.10.2473 — anahtar
// artık operatör ayarı (chstore.ProblemGraphMergeOn, varsayılan açık);
// `shadow` / `off` kipinde liste, sayaçlar, önbellek anahtarı ve gövde
// v0.10.2472 ile bayt bayt aynı.
//
// Kural (incident katlamasıyla aynı yerde: görünüm öncelikleri kesinleştikten
// SONRA, sayaçlardan / sıralamadan / tavandan ÖNCE):
//   - ≥ 2 varlıklı kümenin listede bulunan üye satırları tek satıra katlanır.
//     Temsilci = sahibin satırı; sahip listede yoksa (süzgeç eledi) listede
//     bulunan en erken üye — gizlenen her satırın yerini tutan bir satır
//     ekranda olur. Listede tek üye varsa katlama yok.
//   - Temsilci: öncelik = üyelerin görünüm önceliklerinin en yükseği, ilk
//     görülme en erken, son görülme en geç; Cluster alanı üyeleri (kimlik,
//     varlık, başlık, öncelik, başlangıç, bağ kuralı, "geç katıldı") taşır —
//     katlanan her üye satırı oradan kendi Problem sayfasına açılır.
//   - Hiçbir Problem kapatılmaz, değiştirilmez (karar 1); yalnız görünüm.
//
// SAYIM (v0.10.2473, tek tanım): listenin `total`ı, öncelik / tür çipleri,
// üst sayım cümlesi ve menü rozeti SATIR sayar — katlanan küme BİR satırdır
// (kümenin önceliği = üyelerin en yükseği). Rozet aynı derlemeyi okur
// (computeInboxCountFor → inboxView), CoSRE open_problems ve servis şeridi de.
// Katlanan Problem sayısı satırın kendi özetinde yazar (Cluster.Problems).
//
// ERİŞİLEBİLİRLİK: katlama satır daraltmalarından (servis, arama, ortam,
// ekip, ilk görülme penceresi) SONRA koşar. Bir üyenin servisine süzmek /
// aramak o üyeyi kendi satırıyla getirir (kümeden yalnız o kaldıysa katlama
// yok); doğrudan bağlantı (/problems?problem=<id>) listeye bağlı değildir.
//
// Küme kaynağı evaluator liderinin blobu (tek ayar okuması). Okuma düşerse
// ya da blob bayatsa (lider yazmıyor) katlama / köken yok — düz liste, hiçbir
// satır kaybolmaz, tahmin yok.

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

// inboxGraphBlobMaxAge — blob bundan eskiyse küme bilgisi kullanılmaz.
// Lider blobu değişince, değişmese de 5 dk'da bir yazar; 15 dk = üç kaçırılmış
// yazım (lider yok / pas düşüyor) — eski kümelerle satır gizlenmez.
const inboxGraphBlobMaxAge = 15 * time.Minute

// InboxClusterRef — katlanan kümenin özeti (yalnız görünüm kipi `on`).
type InboxClusterRef struct {
	OwnerID string `json:"ownerId"`
	// Entities / Services / Problems — kümenin TAMAMI (blob; süzgeçten
	// bağımsız): varlık sayısı, bunlardan servis olanlar (üye satırı tavanı
	// kestiyse 0 = bilinmiyor), üye Problem satırı sayısı.
	Entities int `json:"entities"`
	Services int `json:"services,omitempty"`
	Problems int `json:"problems"`
	// Members — bu görünümde (süzgeçlerden sonra) bulunan üyeler.
	Members []InboxClusterMember `json:"members"`
}

// InboxClusterMember — kümedeki bir Problem satırı (listede bulunan).
type InboxClusterMember struct {
	ProblemID string `json:"problemId"`
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"` // service | database | queue | external
	Title     string `json:"title,omitempty"`
	Priority  string `json:"priority,omitempty"`
	StartedAt int64  `json:"startedAt"`
	Owner     bool   `json:"owner,omitempty"`
	Link      string `json:"link,omitempty"`
	Late      bool   `json:"late,omitempty"`
}

// graphGroupServices — SAF: kümedeki FARKLI servis varlığı sayısı. Üye satırı
// tavanı kestiyse 0 (bilinmiyor; eksik sayı yazılmaz).
func graphGroupServices(g chstore.ProblemGraphGroup) int {
	if g.Truncated {
		return 0
	}
	seen := map[string]bool{}
	for _, m := range g.Members {
		if m.Type == entity.TypeService && m.Entity != "" {
			seen[m.Entity] = true
		}
	}
	return len(seen)
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
		ref := &InboxClusterRef{OwnerID: g.OwnerID, Entities: g.Entities, Services: graphGroupServices(g), Problems: len(g.Members)}
		for k, j := range present {
			p := items[j]
			ref.Members = append(ref.Members, InboxClusterMember{ProblemID: mem[k].ProblemID, Name: mem[k].Name, Type: mem[k].Type, Title: p.Title,
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

// freshGraphGroups — SAF: blobun kullanılabilir kümeleri; blob yoksa ya da
// bayatsa nil (katlama / köken yok).
func freshGraphGroups(rep chstore.ProblemGraphShadow, ok bool, now time.Time) []chstore.ProblemGraphGroup {
	if !ok || rep.At <= 0 || now.Sub(time.Unix(rep.At, 0)) > inboxGraphBlobMaxAge {
		return nil
	}
	return rep.Groups
}

// inboxGraphGroups — G/Ç yarısı: blob okuması (derleme başına TEK). Hata =
// küme yok (düz liste; hiçbir satır kaybolmaz).
func (s *Server) inboxGraphGroups(ctx context.Context) []chstore.ProblemGraphGroup {
	if s.store == nil {
		return nil
	}
	rep, ok, err := s.store.GetProblemGraphShadow(ctx)
	if err != nil {
		log.Printf("[inbox] graf katlaması: küme blobu okunamadı, satırlar katlanmadı: %v", err)
		return nil
	}
	return freshGraphGroups(rep, ok, time.Now())
}

// attachInboxOrigins — SAF: bağlı kümenin üyesi olan her Problem satırının
// standardına köken alanını yazar (katlanan temsilci de, süzgeç yüzünden tek
// başına kalan üye de). Standart yoksa / köken gösterilmeyecekse dokunmaz.
func attachInboxOrigins(items []InboxItem, groups []chstore.ProblemGraphGroup) {
	if len(groups) == 0 {
		return
	}
	of := map[string]int{}
	for gi, g := range groups {
		if g.Entities < 2 || g.Origin == nil {
			continue
		}
		for _, m := range g.Members {
			if _, dup := of[m.ProblemID]; !dup {
				of[m.ProblemID] = gi
			}
		}
	}
	for i := range items {
		it := &items[i]
		if it.Kind != "problem" || it.Problem == nil || it.Std == nil {
			continue
		}
		if gi, ok := of[it.Problem.ID]; ok {
			it.Std.Origin = chstore.StdOriginOf(groups[gi], it.Problem.ID)
		}
	}
}

// problemGraphOrigin — G/Ç: tek Problem'in köken alanı (detay başlığı);
// görünüm kipi `on` değilse, blob yok / bayatsa ya da köken yoksa nil.
func (s *Server) problemGraphOrigin(ctx context.Context, problemID string) *chstore.StdOrigin {
	if !chstore.ProblemGraphMergeOn() || problemID == "" {
		return nil
	}
	for _, g := range s.inboxGraphGroups(ctx) {
		for _, m := range g.Members {
			if m.ProblemID == problemID {
				return chstore.StdOriginOf(g, problemID)
			}
		}
	}
	return nil
}

// inboxGraphKeySuffix — görünüm kipi `on` iken önbellek anahtarı eki;
// değilken "" (anahtar v0.10.2472 ile aynı).
func inboxGraphKeySuffix(on bool) string {
	if on {
		return ":graphMerge=1"
	}
	return ""
}

// withInboxGraphMode — `on` iken gövdeye `graphMerge: true` (ön yüz kipi
// API'den öğrenir); değilken gövde aynen.
func withInboxGraphMode(body map[string]any, on bool) map[string]any {
	if on {
		body["graphMerge"] = true
	}
	return body
}
