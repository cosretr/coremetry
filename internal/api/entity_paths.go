package api

// entity_paths.go — v0.10.2233 (Entity S3; plan §4 "POST /api/entity/paths",
// A9): span'in Kubernetes alanları (cluster değeri, namespace, pod, node,
// servis) → entity yolu (pod → iş yükü → servis → namespace → node →
// cluster). Çözüm TEK yerde: span cluster değeri → Remote Cluster kaydı
// (resolveSpanCluster; eski tracePodClusterFor ve FE'nin traceK8sHref /
// spanK8sContext içindeki cluster eşlemesi silindi). Trace listesi hücreleri
// (workloads=false: depo okuması YOK, yalnız kimlik) ve span detayı çipleri
// (workloads=true: iş yükü canlı dizinden, geçmiş anda entity_seen_5m —
// Resolver.PodsByID; kanonik + same_as) bu uçtan geçer. Servis çipi span'in
// kendi service.name'i (span kolonu; ad eşitliği yok).
//
// POST /api/entity/paths  {hints:[{cluster,namespace,pod,node,service}], at, workloads}  viewer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func init() { registerRoutesExtra("entity-paths", (*Server).registerEntityPathsRoutes) }

func (s *Server) registerEntityPathsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/entity/paths", s.postEntityPaths)
}

// entityPathsMax — istek başına ipucu tavanı (trace listesi sayfası ≤ 200 satır).
const entityPathsMax = 200

type entityPathHint struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Node      string `json:"node"`
	Service   string `json:"service"`
}

// entityPath — çözülmüş yol (entity id'leri; "" = bilinmiyor). Reason
// FE'nin ilan ettiği durum: ok | no-k8s | no-cluster | unmapped-cluster |
// no-namespace.
type entityPath struct {
	Reason       string `json:"reason"`
	ClusterValue string `json:"clusterValue,omitempty"`
	ClusterID    string `json:"clusterId,omitempty"`
	ClusterName  string `json:"clusterName,omitempty"`
	Cluster      string `json:"cluster,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Node         string `json:"node,omitempty"`
	Pod          string `json:"pod,omitempty"`
	Workload     string `json:"workload,omitempty"`
	Service      string `json:"service,omitempty"`
}

// spanClusterRef — span cluster değerinin çözümü (TEK kural: Remote Cluster
// kaydının span anahtarları, kayıt etkin). ThanosOn=false: hiç etkin kayıt
// yok. Reason: no_cluster_value | no_remote_cluster | "" (eşlendi).
type spanClusterRef struct {
	Cfg      thanos.ClusterConfig
	ThanosOn bool
	Reason   string
}

func (r spanClusterRef) mapped() bool { return r.ThanosOn && r.Reason == "" }

// resolveSpanCluster — span cluster değeri → Remote Cluster kaydı / entity cid
// (resolverClusters ile aynı sahiplik kuralı + Enabled).
func (s *Server) resolveSpanCluster(cv string) spanClusterRef {
	if s.thanos == nil || !s.thanos.HasEnabledClusters() {
		return spanClusterRef{}
	}
	if strings.TrimSpace(cv) == "" {
		return spanClusterRef{ThanosOn: true, Reason: "no_cluster_value"}
	}
	c, ok := thanos.SpanClusterOwner(s.thanos.CurrentSettings(), cv)
	if !ok || !c.Enabled {
		return spanClusterRef{ThanosOn: true, Reason: "no_remote_cluster"}
	}
	return spanClusterRef{Cfg: c, ThanosOn: true}
}

// entityPathFor — SAF: ipucu + cluster çözümü → kimlik yolu (iş yükü hariç).
func entityPathFor(h entityPathHint, ref spanClusterRef) entityPath {
	p := entityPath{Reason: "no-k8s", ClusterValue: h.Cluster}
	if h.Service != "" {
		p.Service = entity.ServiceID(h.Service)
	}
	if h.Pod == "" && h.Namespace == "" && h.Node == "" {
		return p
	}
	if h.Cluster == "" {
		p.Reason = "no-cluster"
		return p
	}
	if !ref.mapped() {
		p.Reason = "unmapped-cluster"
		return p
	}
	cid := ref.Cfg.EffectiveID()
	p.ClusterID, p.ClusterName, p.Cluster = cid, ref.Cfg.Name, entity.ClusterID(cid)
	if h.Node != "" {
		p.Node = entity.NodeID(cid, h.Node)
	}
	if h.Namespace == "" {
		p.Reason = "no-namespace"
		return p
	}
	p.Namespace = entity.NamespaceID(cid, h.Namespace)
	if h.Pod != "" {
		p.Pod = entity.PodID(cid, h.Namespace, h.Pod)
	}
	p.Reason = "ok"
	return p
}

type entityPathsReq struct {
	Hints     []entityPathHint `json:"hints"`
	At        int64            `json:"at"` // ms; 0 = şimdi
	Workloads bool             `json:"workloads"`
}

func (s *Server) postEntityPaths(w http.ResponseWriter, r *http.Request) {
	var req entityPathsReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz gövde")
		return
	}
	if len(req.Hints) > entityPathsMax {
		writeJSONError(w, http.StatusBadRequest, "en çok 200 ipucu")
		return
	}
	var at time.Time
	if req.At > 0 {
		at = time.UnixMilli(req.At)
	}
	keyParts := []string{atBucket(at), map[bool]string{true: "w", false: "-"}[req.Workloads]}
	for _, h := range req.Hints {
		keyParts = append(keyParts, h.Cluster, h.Namespace, h.Pod, h.Node, h.Service, "\x02")
	}
	key := "entities:paths:" + fnvStr(keyParts...)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		out := make([]entityPath, len(req.Hints))
		refs := map[string]spanClusterRef{}
		var podIDs []string
		for i, h := range req.Hints {
			ref, done := refs[h.Cluster]
			if !done {
				ref = s.resolveSpanCluster(h.Cluster)
				refs[h.Cluster] = ref
			}
			out[i] = entityPathFor(h, ref)
			if req.Workloads && out[i].Pod != "" {
				podIDs = append(podIDs, out[i].Pod)
			}
		}
		if len(podIDs) > 0 {
			qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			res := s.entityResolver()
			sort.Strings(podIDs)
			pods, _ := res.PodsByID(qctx, podIDs, at)
			for i := range out {
				if p, ok := pods[out[i].Pod]; ok && p.Workload != "" {
					out[i].Workload = res.Canonical(qctx, p.Workload, at)
				}
			}
		}
		return map[string]any{"paths": out}, nil
	})
}
