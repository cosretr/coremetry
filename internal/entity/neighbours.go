package entity

import (
	"context"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/entity/wlkind"
)

// neighbours.go — v0.10.2232 (Entity S2; plan §4): kalıcı köprülerin
// okuması. serves (wl→svc) ve same_as (eski→kanonik) kalıcı katmanda (CH
// referans satırları); bu dosya onları resolver'dan sunar. Tüketiciler
// (Rollouts, Service → Infrastructure, entity detayı) kendi join'ini yazmaz.
//
// İş yükü → servis önceliği: serves kenarı > Paths yedeği (canlı dizin /
// MV pod runs > k8s.deployment.name attr > ad eşitliği — son çare).

// RelationSource — kalıcı kenar okuması (chstore.EntityRelationsFor);
// sınırlı: uç sayısı, LIMIT, max_execution_time, last_seen alt sınırı.
type RelationSource interface {
	RelationsFor(ctx context.Context, rel string, ids []string, byChild bool, at time.Time, limit int) ([]Relation, error)
}

// WithRelations — kalıcı kenar kaynağı (nil = köprü okuması yok).
func (r *Resolver) WithRelations(rs RelationSource) *Resolver {
	r.rels = rs
	return r
}

// CanonicalID — SAF: wl: id'nin kanonik biçimi (sahipsiz ReplicaSet
// "<ad>-<hash>" → Deployment <ad>); diğer id'ler aynen.
func CanonicalID(id string) string {
	ref, ok := ParseID(id)
	if !ok || ref.Type != TypeWorkload {
		return id
	}
	k, n := wlkind.Canonical(ref.Kind, ref.Name)
	return WorkloadID(ref.ClusterID, ref.Namespace, k, n)
}

// sameAs — ids'in same_as hedefleri (tek hedefli olanlar; 1 adım).
func (r *Resolver) sameAs(ctx context.Context, ids []string, at time.Time) (map[string]string, error) {
	out := map[string]string{}
	if r.rels == nil || len(ids) == 0 {
		return out, nil
	}
	edges, err := r.rels.RelationsFor(ctx, RelSameAs, ids, false, at, len(ids)*4)
	n := map[string]int{}
	for _, e := range edges {
		n[e.ParentID]++
		out[e.ParentID] = e.ChildID
	}
	for p, c := range n {
		if c != 1 {
			delete(out, p) // belirsiz: kenar yok sayılır
		}
	}
	return out, err
}

// Canonical — istenen id'nin bugünkü kimliği: kanonik biçim, sonra same_as
// (en çok 1 adım). Eski Derived / ReplicaSet / span-only cluster linkleri
// buradan çözülür.
func (r *Resolver) Canonical(ctx context.Context, id string, at time.Time) string {
	c := CanonicalID(id)
	ids := []string{id}
	if c != id {
		ids = append(ids, c)
	}
	sa, _ := r.sameAs(ctx, ids, at)
	if t := sa[id]; t != "" {
		return t
	}
	if t := sa[c]; t != "" {
		return t
	}
	return c
}

// ServiceWorkloads — servisin iş yükleri (serves kenarları, child yönü),
// id sırasında, en çok limit.
func (r *Resolver) ServiceWorkloads(ctx context.Context, service string, at time.Time, limit int) ([]string, error) {
	if r.rels == nil || service == "" {
		return nil, nil
	}
	edges, err := r.rels.RelationsFor(ctx, RelServes, []string{ServiceID(service)}, true, at, limit)
	out := make([]string, 0, len(edges))
	seen := map[string]bool{}
	for _, e := range edges {
		if !seen[e.ParentID] {
			seen[e.ParentID] = true
			out = append(out, e.ParentID)
		}
	}
	sort.Strings(out)
	return out, err
}

// WorkloadServices — iş yüklerinin servisleri (girdi id → sıralı adlar).
// Önce kanonik id + same_as, serves kenarları; kenarı olmayan iş yükü
// Paths yedeğine düşer (tek servis). Best-effort: ilk hata döner.
func (r *Resolver) WorkloadServices(ctx context.Context, ids []string, at time.Time) (map[string][]string, error) {
	out := map[string][]string{}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	final := make(map[string]string, len(ids))
	q := map[string]bool{}
	for _, id := range ids {
		c := CanonicalID(id)
		final[id] = c
		q[id], q[c] = true, true
	}
	sa, err := r.sameAs(ctx, sortedKeys(q), at)
	keep(err)
	for id, c := range final {
		if t := sa[id]; t != "" {
			final[id] = t
		} else if t := sa[c]; t != "" {
			final[id] = t
		}
		q[final[id]] = true
	}
	by := map[string][]string{}
	if r.rels != nil {
		edges, err := r.rels.RelationsFor(ctx, RelServes, sortedKeys(q), false, at, len(q)*20)
		keep(err)
		for _, e := range edges {
			by[e.ParentID] = append(by[e.ParentID], e.ChildID)
		}
	}
	var missing []string
	for _, id := range ids {
		var svcs []string
		for _, cand := range []string{final[id], CanonicalID(id), id} {
			if len(by[cand]) > 0 {
				svcs = by[cand]
				break
			}
		}
		if len(svcs) == 0 {
			missing = append(missing, final[id])
			continue
		}
		names := make([]string, 0, len(svcs))
		for _, s := range svcs {
			if ref, ok := ParseID(s); ok && ref.Type == TypeService {
				names = append(names, ref.Name)
			}
		}
		sort.Strings(names)
		out[id] = names
	}
	if len(missing) > 0 {
		paths, err := r.Paths(ctx, missing, at, nil)
		keep(err)
		for _, id := range ids {
			if _, done := out[id]; done {
				continue
			}
			if s := paths[final[id]].ServiceName(); s != "" {
				out[id] = []string{s}
			}
		}
	}
	return out, firstErr
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
