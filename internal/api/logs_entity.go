package api

// logs_entity.go — v0.10.2233 (Entity S3; plan §4 "Pod sayfası, log ↔ trace
// pivotları", "S2–S6 ayarı"): /api/logs?entity=<id> — log aramasının entity
// kapsamı. Kimlik AYRIŞTIRILIR ve backend'in ALAN süzgeçlerine iner (ES:
// esPodFields / esNamespaceFields / esClusterFields × exactTermsBothShapes;
// CH: res-array conjunct'ları) — gövde araması değil (A8: pod adı log
// gövdesinde aranıyordu, CH'de eksik/yanlış sonuç). Zaman penceresi
// ZORUNLU ve ≤ 7 gün (ES maliyeti: sınırlı range + mevcut size/timeout).
//
//	pod:<cid>/<ns>/<pod>        → pod + namespace + cluster değeri (depo okuması yok)
//	ns:<cid>/<ns>               → namespace + cluster değeri
//	wl:<cid>/<ns>/<Kind>/<ad>   → namespace + cluster + iş yükünün servisleri
//	                              (serves; logstore servis kümesi kuralı, ≤ 100)
//	svc:<ad>                    → servis
//	cluster:<cid>               → cluster değeri (same_as: span-only → kayıt)
//
// Cluster değeri Remote Cluster kaydının span cluster anahtarıdır; kaydın
// birden çok değeri varsa cluster süzgeci konmaz (pod + namespace zaten
// dar), kayıtsız (K2 span-only) cid'nin kendisi değerdir.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// logsEntityMaxWindow — entity kapsamlı aramanın en geniş penceresi.
const logsEntityMaxWindow = 7 * 24 * time.Hour

// logsEntityScope — SAF çıktı: Filter'a inecek alanlar.
type logsEntityScope struct {
	Pod, Namespace, Cluster, Service string
	Workload                         bool // servisler resolver'dan (serves)
}

// logsEntityScopeFor — SAF: çözülmüş ref + kaydın span cluster değerleri.
func logsEntityScopeFor(ref entity.Ref, clusterValues []string) (logsEntityScope, error) {
	cv := ""
	if len(clusterValues) == 1 {
		cv = clusterValues[0]
	}
	switch ref.Type {
	case entity.TypePod:
		return logsEntityScope{Pod: ref.Name, Namespace: ref.Namespace, Cluster: cv}, nil
	case entity.TypeNamespace:
		return logsEntityScope{Namespace: ref.Name, Cluster: cv}, nil
	case entity.TypeWorkload:
		return logsEntityScope{Namespace: ref.Namespace, Cluster: cv, Workload: true}, nil
	case entity.TypeService:
		return logsEntityScope{Service: ref.Name}, nil
	case entity.TypeCluster:
		if cv == "" {
			return logsEntityScope{}, fmt.Errorf("cluster'ın tek bir span cluster değeri yok")
		}
		return logsEntityScope{Cluster: cv}, nil
	}
	return logsEntityScope{}, fmt.Errorf("desteklenmeyen entity tipi %q", ref.Type)
}

// spanClusterValues — cid → span cluster değerleri (kayıtsız cid = K2
// span-only: değerin kendisi).
func (s *Server) spanClusterValues(cid string) []string {
	if s.thanos != nil {
		if c, ok := s.thanos.ClusterByID(cid); ok {
			return c.SpanClusterKeys()
		}
	}
	return []string{cid}
}

// applyLogsEntityParam — serveLogsSearch zinciri (team / scope emsali).
func (s *Server) applyLogsEntityParam(w http.ResponseWriter, r *http.Request, q url.Values, f *logstore.Filter) bool {
	raw := strings.TrimSpace(q.Get("entity"))
	if raw == "" {
		return true
	}
	bad := func(msg string) bool {
		writeJSONError(w, http.StatusBadRequest, msg)
		return false
	}
	if len(raw) > 1024 {
		return bad("entity id çok uzun")
	}
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return bad("entity kapsamı zaman penceresi ister (from < to)")
	}
	if f.To.Sub(f.From) > logsEntityMaxWindow {
		return bad("entity kapsamının penceresi en çok 7 gün")
	}
	ref, ok := entity.ParseID(raw)
	if !ok {
		return bad("geçersiz entity id")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	res := s.entityResolver()
	id := raw
	if ref.Type == entity.TypeWorkload || ref.Type == entity.TypeCluster {
		id = res.Canonical(ctx, raw, f.To)
		if r2, ok2 := entity.ParseID(id); ok2 {
			ref = r2
		}
	}
	sc, err := logsEntityScopeFor(ref, s.spanClusterValues(ref.ClusterID))
	if err != nil {
		return bad(err.Error())
	}
	if sc.Service != "" && f.Service != "" && f.Service != sc.Service {
		return bad("service ve entity kapsamı çelişiyor")
	}
	if sc.Workload {
		m, _ := res.WorkloadServices(ctx, []string{id}, f.To)
		svcs := m[id]
		if len(svcs) == 0 {
			writeJSONError(w, http.StatusNotFound, "iş yükünün servisi çözülemedi (span görülmedi)")
			return false
		}
		if f.Service == "" {
			f.Services = svcs
		}
	}
	if sc.Service != "" {
		f.Service = sc.Service
	}
	f.Pod, f.Namespace = sc.Pod, sc.Namespace
	if sc.Cluster != "" {
		f.Cluster = sc.Cluster
	}
	return true
}

// logsEntityKey — SAF: önbellek anahtarının pod / namespace parçası
// (kapsamsız istekte "" — bugünkü anahtarlar bayt-bayt aynı).
func logsEntityKey(f logstore.Filter) string {
	if f.Pod == "" && f.Namespace == "" {
		return ""
	}
	return ":ent=" + fnvStr(f.Pod, "\x02", f.Namespace)
}
