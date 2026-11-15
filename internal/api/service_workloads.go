package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
)

// service_workloads.go — v0.10.2232 (Entity S2; plan §4 "Services (Infra
// sekmesi)"): servisin ÇALIŞTIĞI iş yükleri entity modelinden. Birincil
// kaynak kalıcı `serves` kenarı (wl→svc, lider tick'i yazar); kenar henüz
// yoksa (yeni servis, ilk tick öncesi) servisin son 1 saatte span basan
// pod'ları (entity_seen_5m, LIMIT 500) → resolver PodsByID (canlı dizin,
// ıska → MV) → pod'un iş yükü. Ad eşitliği YOK. Service → Infrastructure
// sekmesi pod seçimini ve PromQL iş yükü adını buradan alır.
//
// GET /api/services/{name}/workloads?at=   viewer
func init() { registerRoutesExtra("service-workloads", (*Server).registerServiceWorkloadsRoutes) }

func (s *Server) registerServiceWorkloadsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/workloads", s.getServiceWorkloads)
}

// serviceWorkloadsMax — cevap tavanı (bir servis yüzlerce iş yükünde koşmaz).
const serviceWorkloadsMax = 200

type serviceWorkload struct {
	ID        string `json:"id"`
	ClusterID string `json:"clusterId"`
	Cluster   string `json:"cluster,omitempty"` // Remote Cluster adı (Thanos uçlarının anahtarı)
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

type serviceWorkloadsResp struct {
	Workloads []serviceWorkload `json:"workloads"`
	Source    string            `json:"source"` // serves | pods | none
}

// serviceWorkloadsFrom — SAF: wl: id'leri → satırlar (id sırasında, tekil).
func serviceWorkloadsFrom(ids []string, clusterName func(cid string) string) []serviceWorkload {
	seen := map[string]bool{}
	out := []serviceWorkload{}
	for _, id := range ids {
		r, ok := entity.ParseID(id)
		if !ok || r.Type != entity.TypeWorkload || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, serviceWorkload{ID: id, ClusterID: r.ClusterID, Cluster: clusterName(r.ClusterID), Namespace: r.Namespace, Kind: r.Kind, Name: r.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > serviceWorkloadsMax {
		out = out[:serviceWorkloadsMax]
	}
	return out
}

func (s *Server) getServiceWorkloads(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || len(name) > 512 {
		writeJSONError(w, http.StatusBadRequest, "service name required")
		return
	}
	if s.store == nil {
		writeJSON(w, serviceWorkloadsResp{Workloads: []serviceWorkload{}, Source: "none"})
		return
	}
	at := parseAt(r.URL.Query().Get("at"))
	key := entityPivotKey("svcwl", name+"@"+atBucket(at), time.Time{}, time.Time{})
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		clusterName := func(cid string) string {
			if s.thanos != nil {
				if c, ok := s.thanos.ClusterByID(cid); ok {
					return c.Name
				}
			}
			return ""
		}
		res := s.entityResolver()
		ids, err := res.ServiceWorkloads(qctx, name, at, serviceWorkloadsMax)
		if len(ids) > 0 {
			return serviceWorkloadsResp{Workloads: serviceWorkloadsFrom(ids, clusterName), Source: "serves"}, nil
		}
		// Yedek: servisin pod'ları → iş yükleri (kenar yazılmadan önce).
		to := at
		if to.IsZero() {
			to = time.Now()
		}
		aggs, aerr := s.store.EntitySeenForService(qctx, name, nil, to.Add(-entity.SeenWindow), to)
		if aerr != nil && err == nil {
			err = aerr
		}
		cm := resolverClusters{s.thanos}
		var podIDs []string
		for _, a := range aggs {
			if a.Namespace == "" || a.Pod == "" || s.thanos == nil {
				continue
			}
			if cid, ok := cm.ID(a.Cluster); ok {
				podIDs = append(podIDs, entity.PodID(cid, a.Namespace, a.Pod))
			}
		}
		pods, _ := res.PodsByID(qctx, podIDs, at)
		wl := make([]string, 0, len(pods))
		for _, p := range pods {
			if p.Workload != "" {
				wl = append(wl, entity.CanonicalID(p.Workload))
			}
		}
		out := serviceWorkloadsResp{Workloads: serviceWorkloadsFrom(wl, clusterName), Source: "pods"}
		if len(out.Workloads) == 0 {
			out.Source = "none"
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	})
}
