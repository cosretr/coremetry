package entity

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity/wlkind"
)

// pods.go — v0.10.2225 (entity-storage-cost-note C + R1b, E1): pod okuma
// tek kapıdan. Pod/container CH'de satır DEĞİL; "şimdi" → canlı dizin,
// ıska ya da geçmiş an → entity_seen_5m (son SeenWindow, at'e göre).
// Yalnız-metrik pod'un geçmişi Thanos'ta (E4); burada yok.

// ResolvedPod — çözülmüş pod (kaynak: canlı dizin ya da MV).
type ResolvedPod struct {
	ID, ClusterID, Namespace, Name, UID string
	Workload                            string // wl: id ("" = bilinmiyor)
	Node                                string // node adı
	Services                            []string
	ClusterValue                        string
	Source                              string
	FirstSeen, LastSeen                 time.Time
	Live                                bool // canlı dizinden
}

// ParentID — pod'un ebeveyni: iş yükü, yoksa namespace.
func (p ResolvedPod) ParentID() string {
	if p.Workload != "" {
		return p.Workload
	}
	return NamespaceID(p.ClusterID, p.Namespace)
}

// PodQuery — liste süzgeci (hepsi isteğe bağlı). Workload bir wl: id'dir;
// eşleşme türden bağımsız (SameWorkload). Limit ≤ 0 = sınırsız (MV yolu
// kendi LIMIT'ini taşır).
type PodQuery struct {
	Namespace, Workload, Node, Search string
	Limit                             int
}

func liveResolvedPod(cid string, gen time.Time, p LivePod) ResolvedPod {
	pi := ResolvedPod{ID: PodID(cid, p.Namespace, p.Name), ClusterID: cid, Namespace: p.Namespace, Name: p.Name, UID: p.UID,
		Workload: p.Workload, Node: p.Node, ClusterValue: p.ClusterValue, Source: p.Source, LastSeen: gen, Live: true}
	for _, s := range p.Services {
		pi.Services = append(pi.Services, s.Name)
	}
	pi.FirstSeen = gen
	if p.FirstSeen > 0 {
		pi.FirstSeen = time.Unix(p.FirstSeen, 0).UTC()
	}
	return pi
}

// seenResolvedPods — SAF: MV satırları → pod'lar (namespace'siz satır kimlik
// üretemez, düşer). İş yükü pod adından türetilir (wlkind.FromPod).
func seenResolvedPods(cid string, rows []SeenEdge) map[string]*ResolvedPod {
	type acc struct {
		pi    *ResolvedPod
		spans map[string]int64
		cv    map[string]int64
	}
	by := map[string]*acc{}
	for _, e := range rows {
		if e.Namespace == "" || e.Pod == "" {
			continue
		}
		id := PodID(cid, e.Namespace, e.Pod)
		a := by[id]
		if a == nil {
			pi := &ResolvedPod{ID: id, ClusterID: cid, Namespace: e.Namespace, Name: e.Pod, Source: SourceSpan}
			if kind, name, ok := wlkind.FromPod(e.Pod); ok {
				pi.Workload = WorkloadID(cid, e.Namespace, kind, name)
			}
			a = &acc{pi: pi, spans: map[string]int64{}, cv: map[string]int64{}}
			by[id] = a
		}
		if e.Service != "" {
			a.spans[e.Service] += e.Spans
		}
		if e.Cluster != "" {
			a.cv[e.Cluster] += e.Spans
		}
		if a.pi.Node == "" {
			a.pi.Node = e.Node
		}
		if !e.FirstSeen.IsZero() && (a.pi.FirstSeen.IsZero() || e.FirstSeen.Before(a.pi.FirstSeen)) {
			a.pi.FirstSeen = e.FirstSeen
		}
		if e.LastSeen.After(a.pi.LastSeen) {
			a.pi.LastSeen = e.LastSeen
		}
	}
	out := make(map[string]*ResolvedPod, len(by))
	for id, a := range by {
		for s := range a.spans {
			a.pi.Services = append(a.pi.Services, s)
		}
		sp := a.spans
		sort.Slice(a.pi.Services, func(i, j int) bool {
			x, y := a.pi.Services[i], a.pi.Services[j]
			if sp[x] != sp[y] {
				return sp[x] > sp[y]
			}
			return x < y
		})
		a.pi.ClusterValue = argmax(a.cv)
		out[id] = a.pi
	}
	return out
}

func (q PodQuery) match(p ResolvedPod) bool {
	if q.Namespace != "" && p.Namespace != q.Namespace {
		return false
	}
	if q.Workload != "" && !SameWorkload(p.Workload, q.Workload) {
		return false
	}
	if q.Node != "" && p.Node != q.Node {
		return false
	}
	if s := strings.TrimSpace(q.Search); s != "" && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(s)) {
		return false
	}
	return true
}

func sortLimitPods(out []ResolvedPod, limit int) []ResolvedPod {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Pods — cluster'ın pod'ları (at sıfır = şimdi). Canlı dizin varsa yalnız
// o; yoksa MV'nin [at−SeenWindow, at] penceresi.
func (r *Resolver) Pods(ctx context.Context, cid string, q PodQuery, at time.Time) ([]ResolvedPod, error) {
	out := []ResolvedPod{}
	if v, ok := r.view(ctx, cid, at); ok {
		for _, p := range v.Pods {
			if pi := liveResolvedPod(cid, v.Gen, p); q.match(pi) {
				out = append(out, pi)
			}
		}
		return sortLimitPods(out, q.Limit), nil
	}
	var values []string
	if r.clusters != nil {
		values = r.clusters.Values(cid)
	}
	if len(values) == 0 || r.src == nil {
		return out, nil
	}
	to := at
	if to.IsZero() {
		to = r.now()
	}
	rows, err := r.src.SeenPods(ctx, values, q.Namespace, to.Add(-SeenWindow), to)
	for _, pi := range seenResolvedPods(cid, rows) {
		if q.match(*pi) {
			out = append(out, *pi)
		}
	}
	return sortLimitPods(out, q.Limit), err
}

// PodsByID — verilen pod id'leri (bulunamayan yok). Canlı dizin ıskaları
// cluster başına tek MV okumasıyla (≤ MaxPathIDs parça) tamamlanır.
func (r *Resolver) PodsByID(ctx context.Context, ids []string, at time.Time) (map[string]ResolvedPod, error) {
	out := map[string]ResolvedPod{}
	miss := map[string][]Ref{}
	for _, id := range ids {
		ref, ok := ParseID(id)
		if !ok || ref.Type != TypePod {
			continue
		}
		if v, live := r.view(ctx, ref.ClusterID, at); live {
			if p, found := v.Pod(ref.Namespace, ref.Name); found {
				out[id] = liveResolvedPod(ref.ClusterID, v.Gen, p)
				continue
			}
		}
		miss[ref.ClusterID] = append(miss[ref.ClusterID], ref)
	}
	if r.src == nil || r.clusters == nil {
		return out, nil
	}
	to := at
	if to.IsZero() {
		to = r.now()
	}
	var firstErr error
	for cid, refs := range miss {
		values := r.clusters.Values(cid)
		if len(values) == 0 {
			continue
		}
		names := make([]string, 0, len(refs))
		nsSet := map[string]bool{}
		for _, ref := range refs {
			names = append(names, ref.Name)
			nsSet[ref.Namespace] = true
		}
		ns := ""
		if len(nsSet) == 1 {
			ns = refs[0].Namespace
		}
		sort.Strings(names)
		var rows []SeenEdge
		for i := 0; i < len(names); i += MaxPathIDs {
			s, err := r.src.SeenForPods(ctx, values, ns, names[i:min(i+MaxPathIDs, len(names))], to.Add(-SeenWindow), to)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			rows = append(rows, s...)
		}
		found := seenResolvedPods(cid, rows)
		for _, ref := range refs {
			id := PodID(cid, ref.Namespace, ref.Name)
			if pi, ok := found[id]; ok {
				out[id] = *pi
			}
		}
	}
	return out, firstErr
}
