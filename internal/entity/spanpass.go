package entity

import (
	"context"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/entity/wlkind"
)

// spanpass.go — SPAN-TÜREVLİ çıkarım (design §3). entity_seen_5m'in son
// dilimi (span cluster değeri, namespace, pod, node, servis) → Remote
// Cluster kaydına eşlenir (SpanClusterValue) → o cluster'ın AYNI tick'teki
// `seen` kümesine eklenir: pod (Thanos bilmiyorsa, source=span), `svc:`
// varlığı, `runs` (pod→svc), node biliniyorsa `runs_on`. Eşlenemeyen
// cluster değeri düşmez, "(unmapped)" koşu satırına `değer → n` sayaçlanır.
//
// Çakışma kuralı (design §3): yapı Thanos'un, etkinlik span'in — aynı
// id'yi ikisi de üretirse Thanos'un varlığı kalır, span yalnız ilişki ekler.

// SeenRow — entity_seen_5m'den bir (cluster, ns, pod, servis) satırı.
type SeenRow struct {
	ClusterValue, Namespace, Pod, Node, Service string
	Spans                                       int
	FirstSeen, LastSeen                         time.Time
}

// SeenReader — chstore.EntitySeenRecent adaptörü.
type SeenReader interface {
	RecentSeen(ctx context.Context, since time.Time) ([]SeenRow, error)
	// RecentSeenFor — v0.10.141: yalnız bir span cluster değerinin satırları
	// (backfill; kesim tavanı o değere uygulanır).
	RecentSeenFor(ctx context.Context, since time.Time, clusterValue string) ([]SeenRow, error)
}

// UnmappedClusterID — eşlenemeyen span cluster değerlerinin koşu satırı.
const UnmappedClusterID = "(unmapped)"

// GroupSeenByCluster — satırları cluster_id'ye göre böler; eşlenemeyenleri sayar.
func GroupSeenByCluster(rows []SeenRow, refs []ClusterRef) (map[string][]SeenRow, map[string]int) {
	byValue := map[string]string{}
	for _, r := range refs {
		if r.ID == "" {
			continue
		}
		vals := append([]string{r.SpanClusterValue}, r.SpanClusterValues...)
		if r.SpanClusterValue == "" && len(r.SpanClusterValues) == 0 {
			vals = []string{r.Name}
		}
		for _, v := range vals {
			if v != "" {
				byValue[v] = r.ID // v0.10.139 — bir kayıt birden çok değer
			}
		}
	}
	out := map[string][]SeenRow{}
	unmapped := map[string]int{}
	for _, row := range rows {
		cid, ok := byValue[row.ClusterValue]
		if !ok {
			unmapped[row.ClusterValue]++
			continue
		}
		out[cid] = append(out[cid], row)
	}
	return out, unmapped
}

// SpanSeenToEntities — bir cluster'ın span satırları → ek varlık/ilişki.
// known: Thanos'un bu tick ürettiği varlıklar (pod çakışmasında kazanır).
func SpanSeenToEntities(cid string, rows []SeenRow, known map[string]Entity) ([]Entity, []Relation) {
	return SpanSeenToEntitiesWith(cid, rows, known, nil)
}

// SpanSeenToEntitiesWith — v0.10.2262 (S6 parça 3): ex = cluster'ın açık
// iş yükü dizini (workloads.go; nil = yalnız ad türevi). KSM'siz pod'un iş
// yükü önce açık addan; tablodaki (iş yükü, servis) çiftleri serves olur.
func SpanSeenToEntitiesWith(cid string, rows []SeenRow, known map[string]Entity, ex *ExplicitIndex) ([]Entity, []Relation) {
	ents := map[string]Entity{}
	rels := map[string]Relation{}
	rel := func(typ, parent, child string) {
		rels[typ+"|"+parent+"|"+child] = Relation{Type: typ, ClusterID: cid, ParentID: parent, ChildID: child, Source: SourceSpan}
	}
	// v0.10.2232 (S2): KSM'nin bildiği iş yükü (ns + kanonik ad) türetmeden
	// önce gelir — aynı iş yükü için ikinci (Derived) kimlik doğmaz.
	knownWl := map[string]string{}
	for id, e := range known {
		if e.Type == TypeWorkload {
			knownWl[e.Namespace+"/"+e.Name] = id
		}
	}
	for _, r := range rows {
		if r.Namespace == "" || r.Pod == "" {
			continue
		}
		podID := PodID(cid, r.Namespace, r.Pod)
		if _, thanosKnows := known[podID]; !thanosKnows {
			if _, dup := ents[podID]; !dup {
				nsID := NamespaceID(cid, r.Namespace)
				if _, ok := known[nsID]; !ok {
					ents[nsID] = Entity{Type: TypeNamespace, ClusterID: cid, ID: nsID, Name: r.Namespace, ParentID: ClusterID(cid), Labels: map[string]string{}, Source: SourceSpan}
					rel(RelParent, ClusterID(cid), nsID)
				}
				// v0.10.471 (F2-4, G14) — KSM sahibi yok: workload'ı pod adından türet
				// (derived_workload.go); türetilemezse pod doğrudan namespace'in altında.
				parent := nsID
				kind, name, ok := wlkind.FromPod(r.Pod)
				labels := map[string]string{"kind": kind, "derived": "pod-name"}
				if k, n, found := ex.ForPod(r.Namespace, r.Pod, kind, name, ok); found {
					kind, name, ok = k, n, true
					labels = map[string]string{"kind": kind, "source": "attr"}
				}
				if ok {
					wlID := WorkloadID(cid, r.Namespace, kind, name)
					if ksm, found := knownWl[r.Namespace+"/"+name]; found {
						wlID = ksm
					}
					if _, known := known[wlID]; !known {
						if _, dup := ents[wlID]; !dup {
							ents[wlID] = Entity{Type: TypeWorkload, ClusterID: cid, ID: wlID, Namespace: r.Namespace, Name: name, ParentID: nsID,
								Labels: labels, Source: SourceSpan}
							rel(RelParent, nsID, wlID)
						}
					}
					parent = wlID
				}
				ents[podID] = Entity{Type: TypePod, ClusterID: cid, ID: podID, Namespace: r.Namespace, Name: r.Pod, ParentID: parent, Labels: map[string]string{}, Source: SourceSpan}
				rel(RelParent, parent, podID)
			}
		}
		if r.Service != "" {
			svcID := ServiceID(r.Service)
			if _, ok := known[svcID]; !ok {
				ents[svcID] = Entity{Type: TypeService, ClusterID: cid, ID: svcID, Name: r.Service, Labels: map[string]string{}, Source: SourceSpan}
			}
			rel(RelRuns, podID, svcID)
		}
		if r.Node != "" {
			nodeID := NodeID(cid, r.Node)
			if _, ok := known[nodeID]; !ok {
				if _, dup := ents[nodeID]; !dup {
					ents[nodeID] = Entity{Type: TypeNode, ClusterID: cid, ID: nodeID, Name: r.Node, ParentID: ClusterID(cid), Labels: map[string]string{}, Source: SourceSpan}
					rel(RelParent, ClusterID(cid), nodeID)
				}
			}
			rel(RelRunsOn, podID, nodeID)
		}
	}
	ExplicitServes(cid, ex, known, knownWl, ents, rels)
	outE := make([]Entity, 0, len(ents))
	for _, e := range ents {
		outE = append(outE, e)
	}
	sort.Slice(outE, func(i, j int) bool { return outE[i].ID < outE[j].ID })
	outR := make([]Relation, 0, len(rels))
	for _, r := range rels {
		outR = append(outR, r)
	}
	sort.Slice(outR, func(i, j int) bool {
		if outR[i].Type != outR[j].Type {
			return outR[i].Type < outR[j].Type
		}
		if outR[i].ParentID != outR[j].ParentID {
			return outR[i].ParentID < outR[j].ParentID
		}
		return outR[i].ChildID < outR[j].ChildID
	})
	return outE, outR
}

// sortedUnmapped — deterministik Array çifti.
func sortedUnmapped(m map[string]int) ([]string, []uint32) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	counts := make([]uint32, len(keys))
	for i, k := range keys {
		counts[i] = uint32(m[k])
	}
	return keys, counts
}

// ── v0.10.141 — atama sonrası geriye dönük geçiş (backfill) ──
//
// İnceleme dersi: 24 saatlik pencereyi TÜM tick'e uygulamak her cluster'ın
// ölü pod'larını "şimdi açıldı" diye yeniden açıyordu. Doğrusu: yalnız
// ATANAN değerin satırları; canlı olanlar (son görülme ≤ SeenWindow) normal
// diff'e ve canlı dizine katılır. v0.10.2225: ölüler artık yazılmaz — pod
// satırı yok, geçmiş zaten entity_seen_5m'de (entity-storage-cost-note E1).

// BackfillLiveRows — saf: değere süz, yalnız canlı pod'ların satırları.
// Karar POD başına: aynı pod'un bir satırı (servis) canlıysa TÜM satırları
// canlıdır (eski bir servis satırı canlı pod'u bölmesin).
func BackfillLiveRows(rows []SeenRow, value string, liveCut time.Time) []SeenRow {
	alive := map[string]bool{}
	for _, r := range rows {
		if r.ClusterValue == value && r.Pod != "" && r.Namespace != "" && !r.LastSeen.Before(liveCut) {
			alive[r.Namespace+"/"+r.Pod] = true
		}
	}
	var live []SeenRow
	for _, r := range rows {
		if r.ClusterValue == value && alive[r.Namespace+"/"+r.Pod] {
			live = append(live, r)
		}
	}
	return live
}
