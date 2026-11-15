package api

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// cluster_entity_links.go — v0.10.2191/2192: Clusters namespace detayının
// iş yükü → servis ve pod → servis/node eşlemeleri entity grafiğinden
// (entity.ResolveWorkloadService / IndexPodEdges). Hepsi best-effort: CH
// hatası satırları etiketsiz bırakır, okuma düşmez.

// entitySeenWindow — pod → servis kenarının bakış penceresi: düşük
// trafikli servisin pod'u 15 dk'da span basmayabilir.
const entitySeenWindow = time.Hour

// nsPodEdges — namespace'in verilen pod'ları için span-türevli pod →runs→
// service / pod → node kenarları (entity_seen_5m; EntitySeenForPods ≤500 pod).
func (s *Server) nsPodEdges(ctx context.Context, cfg thanos.ClusterConfig, namespace string, pods []string) map[string]entity.PodEdges {
	if s.store == nil || len(pods) == 0 {
		return nil
	}
	now := time.Now()
	aggs, err := s.store.EntitySeenForPods(ctx, cfg.SpanClusterKeys(), namespace, pods, now.Add(-entitySeenWindow), now)
	if err != nil {
		return nil
	}
	seen := make([]entity.SeenEdge, len(aggs))
	for i, a := range aggs {
		seen[i] = entity.SeenEdge{Namespace: a.Namespace, Pod: a.Pod, Node: a.Node, Service: a.Service, Spans: a.Spans}
	}
	return entity.IndexPodEdges(namespace, seen)
}

// attachWorkloadServices — /deployments satırlarına iş yükünün servisi.
func (s *Server) attachWorkloadServices(ctx context.Context, cfg thanos.ClusterConfig, namespace string, rows []thanos.DeploymentRow) {
	if s.store == nil || len(rows) == 0 {
		return
	}
	var pods []string
	for _, r := range rows {
		pods = append(pods, r.PodNames...)
	}
	edges := s.nsPodEdges(ctx, cfg, namespace, pods)
	var links []entity.DeploymentLink
	if meta, err := s.store.ListServiceMetadata(ctx); err == nil {
		for svc, m := range meta {
			if m.Deployment != "" {
				links = append(links, entity.DeploymentLink{Service: svc, Deployment: m.Deployment, Namespace: m.Namespace})
			}
		}
	}
	known, _ := s.store.ListActiveServiceNames(ctx, 24*time.Hour)
	for i := range rows {
		rows[i].Service = entity.ResolveWorkloadService(rows[i].Deployment, namespace, rows[i].PodNames, edges, links, known)
	}
}

// fillPodsFromEdges — SAF: boş Service / Node alanlarını entity
// kenarlarından doldurur; Thanos'tan gelen değer ezilmez.
func fillPodsFromEdges(rows []thanos.PodRow, edges map[string]entity.PodEdges) {
	for i := range rows {
		e, ok := edges[rows[i].Pod]
		if !ok {
			continue
		}
		if rows[i].Service == "" {
			rows[i].Service = e.Service
		}
		if rows[i].Node == "" {
			rows[i].Node = e.Node
		}
	}
}
