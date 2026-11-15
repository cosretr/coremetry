package api

import (
	"context"
	"time"

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
	out := make([]entity.SeenEdge, len(aggs))
	for i, g := range aggs {
		out[i] = entity.SeenEdge{Namespace: g.Namespace, Pod: g.Pod, Node: g.Node, Service: g.Service, Spans: g.Spans, Cluster: g.Cluster}
	}
	return out, err
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
	if s.store != nil {
		src = resolverSource{s}
	}
	return entity.NewResolver(src, resolverClusters{s.thanos})
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
