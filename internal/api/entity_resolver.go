package api

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// entity_resolver.go — v0.10.2193 (Entity S0): entity.Resolver'ın api
// adaptörü ve Clusters /pods + /deployments zenginleştirmesi. Sayfaya özel
// pod↔servis join'i yok (eski PodServiceMap / pickPodService / FE çoğunluğu
// silindi); iki tablo aynı yoldan aynı cevabı verir. Best-effort: CH hatası
// alanları boş bırakır, okuma düşmez.

type resolverSource struct{ s *Server }

func (a resolverSource) SeenForPods(ctx context.Context, values []string, ns string, pods []string, from, to time.Time) ([]entity.SeenEdge, error) {
	aggs, err := a.s.store.EntitySeenForPods(ctx, values, ns, pods, from, to)
	return seenEdges(aggs), err
}

// SeenPods — v0.10.2225: Resolver.Pods'un MV yedeği.
func (a resolverSource) SeenPods(ctx context.Context, values []string, ns string, from, to time.Time) ([]entity.SeenEdge, error) {
	aggs, err := a.s.store.EntitySeenPods(ctx, values, ns, from, to)
	return seenEdges(aggs), err
}

// SeenForService — v0.10.2239 (S4): Resolver.ServicePods'un MV yedeği.
func (a resolverSource) SeenForService(ctx context.Context, service string, values []string, from, to time.Time) ([]entity.SeenEdge, error) {
	aggs, err := a.s.store.EntitySeenForService(ctx, service, values, from, to)
	return seenEdges(aggs), err
}

func seenEdges(aggs []chstore.EntitySeenAgg) []entity.SeenEdge {
	out := make([]entity.SeenEdge, len(aggs))
	for i, g := range aggs {
		out[i] = entity.SeenEdge{Namespace: g.Namespace, Pod: g.Pod, Node: g.Node, Service: g.Service, Spans: g.Spans, Cluster: g.Cluster,
			FirstSeen: g.FirstSeen, LastSeen: g.LastSeen}
	}
	return out
}

func (a resolverSource) DeploymentLinks(ctx context.Context) ([]entity.DeploymentLink, error) {
	meta, err := a.s.store.ListServiceMetadata(ctx)
	var out []entity.DeploymentLink
	for svc, m := range meta {
		if m.Deployment != "" {
			out = append(out, entity.DeploymentLink{Service: svc, Deployment: m.Deployment, Namespace: m.Namespace})
		}
	}
	return out, err
}

// RelationsFor — v0.10.2232 (S2): kalıcı köprüler (serves, same_as).
func (a resolverSource) RelationsFor(ctx context.Context, rel string, ids []string, byChild bool, at time.Time, limit int) ([]entity.Relation, error) {
	return a.s.store.EntityRelationsFor(ctx, rel, ids, byChild, at, limit)
}

func (a resolverSource) KnownServices(ctx context.Context) ([]string, error) {
	return a.s.store.ListActiveServiceNames(ctx, 24*time.Hour)
}

// resolverClusters — cid ↔ span cluster değerleri, Remote Cluster kayıtlarından.
type resolverClusters struct{ t *thanos.Service }

func (c resolverClusters) Values(cid string) []string {
	if cfg, ok := c.t.ClusterByID(cid); ok {
		return cfg.SpanClusterKeys()
	}
	return nil
}

func (c resolverClusters) ID(value string) (string, bool) {
	cfg, ok := thanos.SpanClusterOwner(c.t.CurrentSettings(), value)
	return cfg.EffectiveID(), ok
}

func (s *Server) entityResolver() *entity.Resolver {
	var src entity.ResolverSource
	var rels entity.RelationSource
	if s.store != nil {
		src, rels = resolverSource{s}, resolverSource{s}
	}
	// v0.10.2225 (R1b): "şimdi" soruları önce lider tick'inin Redis canlı
	// dizininden; ıska / Redis yok → MV. Okuyucu süreç başına tek (LiveIndexFor).
	var live *entity.LiveIndex
	if s.cache != nil {
		live = entity.LiveIndexFor(s.cache)
	}
	return entity.NewResolver(src, resolverClusters{s.thanos}).WithLive(live).WithRelations(rels)
}

// attachPodPaths — /pods satırları: servis resolver'dan; node yalnız
// Thanos boş bıraktıysa (kube_pod_info yok) span'deki node.
func (s *Server) attachPodPaths(ctx context.Context, cfg thanos.ClusterConfig, rows []thanos.PodRow) {
	if len(rows) == 0 {
		return
	}
	cid := cfg.EffectiveID()
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = entity.PodID(cid, r.Namespace, r.Pod)
	}
	paths, _ := s.entityResolver().Paths(ctx, ids, time.Now(), nil)
	fillPodsFromPaths(rows, ids, paths)
}

// fillPodsFromPaths — SAF: Service resolver'ın; Thanos node'u ezilmez.
func fillPodsFromPaths(rows []thanos.PodRow, ids []string, paths map[string]entity.Path) {
	for i := range rows {
		p := paths[ids[i]]
		rows[i].Service = p.ServiceName()
		if rows[i].Node == "" {
			rows[i].Node = p.NodeName()
		}
	}
}

// attachWorkloadServices — /deployments satırlarına iş yükünün servisi
// (sahiplik Thanos'tan: satırın PodNames'i).
func (s *Server) attachWorkloadServices(ctx context.Context, cfg thanos.ClusterConfig, namespace string, rows []thanos.DeploymentRow) {
	if len(rows) == 0 {
		return
	}
	cid := cfg.EffectiveID()
	ids := make([]string, len(rows))
	owned := make(map[string][]string, len(rows))
	for i, r := range rows {
		ids[i] = workloadEntityID(cid, namespace, r)
		pods := make([]string, len(r.PodNames))
		for j, p := range r.PodNames {
			pods[j] = entity.PodID(cid, namespace, p)
		}
		owned[ids[i]] = pods
	}
	paths, _ := s.entityResolver().Paths(ctx, ids, time.Now(), owned)
	for i := range rows {
		rows[i].Service = paths[ids[i]].ServiceName()
	}
}

func workloadEntityID(cid, namespace string, r thanos.DeploymentRow) string {
	kind := r.Kind
	if kind == "" {
		kind = entity.DerivedKind
	}
	return entity.WorkloadID(cid, namespace, kind, r.Deployment)
}
