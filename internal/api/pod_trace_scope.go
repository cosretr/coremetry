package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
)

// pod_trace_scope.go — v0.10.2195 (operatör: pod sayfasında "Son trace'ler"
// boş). Pod → trace kapsamı entity modelinden (entity.Resolver.Paths):
//
//   - pod'un runs kenarı varsa (entity_seen_5m, k8s.pod.name taşıyan span)
//     → mode "pod": k8s.pod.name süzgeci + kenarın GERÇEK span cluster değeri
//     (Remote Cluster kaydının eşlemesi; ham ad karşılaştırması değil);
//   - yoksa iş yükü → servis (pod runs > k8s.deployment.name attr > ad
//     eşitliği) → mode "service": o servisin trace'leri, "servis geneli";
//   - ikisi de yoksa "none" (pod süzgeci; liste boş kalabilir).
//
// GET /api/pods/trace-scope?cluster=&namespace=&pod=&deploy=&to=
func init() { registerRoutesExtra("pod-trace-scope", (*Server).registerPodTraceScopeRoutes) }

func (s *Server) registerPodTraceScopeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pods/trace-scope", s.getPodTraceScope)
}

type podTraceScope struct {
	Mode         string `json:"mode"` // pod | service | none
	ClusterValue string `json:"clusterValue,omitempty"`
	Service      string `json:"service,omitempty"`
}

// podTraceScopeFrom — SAF: yol çözümünden trace kapsamı. values: kaydın span
// cluster değerleri; tek değerse servis/none kipinde süzgeç o, çoksa süzgeç yok.
func podTraceScopeFrom(pod, wl entity.Path, values []string) podTraceScope {
	single := ""
	if len(values) == 1 {
		single = values[0]
	}
	if pod.Service != "" {
		cv := pod.ClusterValue
		if cv == "" {
			cv = single
		}
		return podTraceScope{Mode: "pod", ClusterValue: cv}
	}
	if wl.Service != "" {
		return podTraceScope{Mode: "service", ClusterValue: single, Service: wl.ServiceName()}
	}
	return podTraceScope{Mode: "none", ClusterValue: single}
}

func (s *Server) getPodTraceScope(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clusterRef := strings.TrimSpace(q.Get("cluster"))
	ns := strings.TrimSpace(q.Get("namespace"))
	pod := strings.TrimSpace(q.Get("pod"))
	deploy := strings.TrimSpace(q.Get("deploy"))
	if clusterRef == "" || ns == "" || pod == "" {
		http.Error(w, "cluster, namespace, pod query params required", http.StatusBadRequest)
		return
	}
	if len(ns) > 253 || len(pod) > 253 || len(deploy) > 253 {
		http.Error(w, "param too long", http.StatusBadRequest)
		return
	}
	if s.thanos == nil {
		writeJSON(w, podTraceScope{Mode: "none"})
		return
	}
	cfg, ok := s.thanos.ClusterByRef(clusterRef)
	if !ok {
		http.Error(w, "unknown or disabled cluster", http.StatusNotFound)
		return
	}
	at := time.Now()
	if toNs, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil && toNs > 0 && time.Unix(0, toNs).Before(at) {
		at = time.Unix(0, toNs)
	}
	key := fmt.Sprintf("pod-trace-scope:%s:%s:%s:%s:%s:%d", clusterCfgDigest(cfg), ns, pod, deploy, cfg.EffectiveID(), at.Unix()/60)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		qctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		cid := cfg.EffectiveID()
		podID := entity.PodID(cid, ns, pod)
		ids := []string{podID}
		var owned map[string][]string
		wlID := ""
		if deploy != "" {
			wlID = entity.WorkloadID(cid, ns, entity.DerivedKind, deploy)
			ids = append(ids, wlID)
			owned = map[string][]string{wlID: {podID}}
		}
		paths, _ := s.entityResolver().Paths(qctx, ids, at, owned)
		return podTraceScopeFrom(paths[podID], paths[wlID], cfg.SpanClusterKeys()), nil
	})
}
