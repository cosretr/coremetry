package api

// entity_neighbours.go — v0.10.2239 (Entity S4; plan §4 "GET
// /api/entity/neighbours", "S2–S6 ayarı"): bir entity'nin yolu ve
// komşuları, ilk tüketicisi resolve_entity (MCP / CoSRE) ile. Hepsi
// entity.Resolver'dan: svc → wl `serves` (kalıcı katman), svc / wl → pod
// canlı dizinden (geçmiş anda entity_seen_5m öneki), pod → yol Paths.
// Ad eşitliği yalnız resolver'ın kendi son çare yedeğinde.
// v0.10.2240 (S5): servis / db / queue / ext için calls kenarları (çağırdıkları
// + çağıranlar; kalıcı katman, iki sınırlı okuma).
// v0.10.2260 (S6 parça 1): servis → host'lar, host → servisler ("şimdi" canlı
// blob'dan, geçmiş anda kalıcı host→svc runs; tek sınırlı okuma).
//
// GET /api/entity/neighbours?id=<entity id>&at=<RFC3339|unix>  viewer, 30 s

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func init() { registerRoutesExtra("entity-neighbours", (*Server).registerEntityNeighboursRoutes) }

func (s *Server) registerEntityNeighboursRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/entity/neighbours", s.getEntityNeighbours)
}

func (s *Server) getEntityNeighbours(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	ref, ok := entity.ParseID(id)
	if !ok || len(id) > 1024 {
		writeJSONError(w, http.StatusBadRequest, "valid entity id required")
		return
	}
	at := parseAt(r.URL.Query().Get("at"))
	key := entityPivotKey("neighbours", id+"@"+atBucket(at), time.Time{}, time.Time{})
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		c := mcptools.EntityCandidate{Kind: ref.Type, ClusterID: ref.ClusterID, Namespace: ref.Namespace, Name: ref.Name, ID: id}
		return s.entityNeighboursAt(qctx, c, at)
	})
}

// entityNeighboursFor — Deps.EntityNeighbours ("şimdi").
func (s *Server) entityNeighboursFor(ctx context.Context, c mcptools.EntityCandidate) (*mcptools.EntityNeighbours, error) {
	return s.entityNeighboursAt(ctx, c, time.Time{})
}

func (s *Server) entityNeighboursAt(ctx context.Context, c mcptools.EntityCandidate, at time.Time) (*mcptools.EntityNeighbours, error) {
	res := s.entityResolver()
	out := &mcptools.EntityNeighbours{}
	switch c.Kind {
	case entity.TypeService:
		wls, err := res.ServiceWorkloads(ctx, c.Name, at, 50)
		out.Workloads = wls
		set, perr := res.ServicePods(ctx, c.Name, s.entityClusterIDs(), at)
		fillNeighbourPods(out, set)
		if err == nil {
			err = perr
		}
		if cerr := fillNeighbourCalls(ctx, res, out, entity.ServiceID(c.Name), at); err == nil {
			err = cerr
		}
		hosts, herr := res.ServiceHosts(ctx, c.Name, at)
		out.Hosts = hosts
		if err == nil {
			err = herr
		}
		return out, err
	case entity.TypeHost:
		svcs, err := res.HostServices(ctx, c.Name, at)
		out.Services = svcs
		if len(svcs) == 0 {
			out.Note = "host'ta servis bilinmiyor — son 1 saatte span görülmedi ya da host kalıcı katmana terfi etmedi (≥ 24 sa kesintisiz görülme)"
		}
		return out, err
	case entity.TypeDatabase, entity.TypeQueue, entity.TypeExternal:
		id := c.ID
		if id == "" {
			id = entity.Ref{Type: c.Kind, Name: c.Name}.String()
		}
		err := fillNeighbourCalls(ctx, res, out, id, at)
		if len(out.Calls) == 0 && len(out.CalledBy) == 0 {
			out.Note = "çağrı kenarı bilinmiyor — topolojide son 24 saatte görülmedi ya da entity deposu henüz yazmadı"
		}
		return out, err
	case entity.TypeWorkload:
		id := res.Canonical(ctx, c.ID, at)
		svcs, err := res.WorkloadServices(ctx, []string{id}, at)
		out.Services = svcs[id]
		ref, _ := entity.ParseID(id)
		pods, perr := res.Pods(ctx, ref.ClusterID, entity.PodQuery{Workload: id, Limit: entity.MaxServicePods}, at)
		set := entity.ServicePodSet{Pods: pods, Truncated: len(pods) >= entity.MaxServicePods}
		if len(pods) > 0 {
			set.Source = "mv"
			if pods[0].Live {
				set.Source = "live"
			}
		}
		fillNeighbourPods(out, set)
		if err == nil {
			err = perr
		}
		return out, err
	case entity.TypePod:
		paths, err := res.Paths(ctx, []string{c.ID}, at, nil)
		p := paths[c.ID]
		out.Path = &mcptools.EntityPathIDs{Pod: c.ID, Workload: p.Workload, Service: p.Service, Namespace: p.Namespace, Node: p.Node, Cluster: p.Cluster}
		if n := p.ServiceName(); n != "" {
			out.Services = []string{n}
		}
		return out, err
	}
	return nil, nil
}

// fillNeighbourCalls — calls kenarının iki yönü (entity.Resolver.Callees /
// Callers; her biri tek sınırlı okuma, ≤ entity.MaxCallNeighbours).
func fillNeighbourCalls(ctx context.Context, res *entity.Resolver, out *mcptools.EntityNeighbours, id string, at time.Time) error {
	callees, err := res.Callees(ctx, id, at)
	callers, cerr := res.Callers(ctx, id, at)
	out.Calls, out.CalledBy = callees.IDs, callers.IDs
	out.CallsTruncated = callees.Truncated || callers.Truncated
	if err == nil {
		err = cerr
	}
	return err
}

// fillNeighbourPods — SAF: pod kümesi → cevap alanları (liste tavanlı, sayı tam).
func fillNeighbourPods(out *mcptools.EntityNeighbours, set entity.ServicePodSet) {
	out.PodCount, out.PodsTruncated, out.PodSource = len(set.Pods), set.Truncated, set.Source
	for i, p := range set.Pods {
		if i >= mcptools.EntityNeighbourPodsMax {
			out.PodsTruncated = true
			break
		}
		out.Pods = append(out.Pods, p.ID)
	}
	if len(set.Pods) == 0 {
		out.Note = "pod bilinmiyor — entity deposunda span ya da Kubernetes eşlemesi yok"
	}
}
