package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cluster"
)

// admin_attr_kvh_alias.go — v0.10.2410: 0017 sihirbazı uçları (0014
// sihirbazının yerine; defter kaydı, api.go büyümez).
//
//	GET  /api/admin/attr-kvh-alias/status  admin — otomatik ön kontroller + adım × host + pod kipleri + partition'lar + koşu
//	POST /api/admin/attr-kvh-alias/run     admin — {materialize}; bekleyen adımlar sırayla (arka plan); audit başlangıç + bitiş
func init() {
	registerRoutesExtra("attr-kvh-alias", (*Server).registerAttrKVHAliasRoutes)
	// Her pod heartbeat'inde derleyici kipini yayınlar (switch adımının kanıtı).
	cluster.RegisterFact("attrIndexMode", chstore.AttrIndexModeName)
}

func (s *Server) registerAttrKVHAliasRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/attr-kvh-alias/status", auth.RequireRole(auth.RoleAdmin, s.getAttrKVHAliasStatus))
	mux.HandleFunc("POST /api/admin/attr-kvh-alias/run", auth.RequireRole(auth.RoleAdmin, s.postAttrKVHAliasRun))
}

// attrKVHPods — görünen pod'lar ve her birinin bildirdiği kip.
func (s *Server) attrKVHPods(ctx context.Context) []chstore.AttrKVHPod {
	if s.cluster == nil {
		return []chstore.AttrKVHPod{{ID: "single", Hostname: "bu pod", Mode: chstore.AttrIndexModeName(), IsThisPod: true}}
	}
	members, err := s.cluster.Members(ctx)
	if err != nil {
		return nil
	}
	return attrKVHPodsFromMembers(members)
}

// attrKVHPodsFromMembers — SAF.
func attrKVHPodsFromMembers(members []cluster.Member) []chstore.AttrKVHPod {
	out := make([]chstore.AttrKVHPod, 0, len(members))
	for _, m := range members {
		out = append(out, chstore.AttrKVHPod{ID: m.ID, Hostname: m.Hostname, Version: m.Version, Mode: m.Facts["attrIndexMode"], IsThisPod: m.IsThisPod})
	}
	return out
}

func (s *Server) getAttrKVHAliasStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	writeJSON(w, s.store.AttrKVHAliasStatus(ctx, s.attrKVHPods(ctx)))
}

func (s *Server) postAttrKVHAliasRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Materialize bool `json:"materialize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	claims := auth.FromContext(r.Context())
	ip := clientIP(r)
	by := ""
	if claims != nil {
		by = claims.Email
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
	defer cancel()
	st, err := s.store.AttrKVHAliasStart(ctx, chstore.AttrKVHRunOptions{
		StartedBy: by, Materialize: in.Materialize,
		Pods: s.attrKVHPods,
		Done: func(job chstore.AttrKVHJob) {
			s.auditAs(claims, ip, "attr_kvh_alias.run_done", "clickhouse", "0017_attr_kvh_alias", attrKVHJobAudit(job))
		},
	})
	if err != nil {
		s.audit(r, "attr_kvh_alias.run", "clickhouse", "0017_attr_kvh_alias", "REDDEDİLDİ: "+err.Error())
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "attr_kvh_alias.run", "clickhouse", "0017_attr_kvh_alias",
		fmt.Sprintf(`{"cluster":%q,"materialize":%v,"started":true}`, st.Cluster, in.Materialize))
	writeJSON(w, st)
}

// attrKVHJobAudit — SAF: bitiş kaydının ayrıntısı (adım, ilk hata, ifade sayısı).
func attrKVHJobAudit(job chstore.AttrKVHJob) string {
	ok := 0
	for _, l := range job.Log {
		if l.OK {
			ok++
		}
	}
	return fmt.Sprintf(`{"cluster":%q,"materialize":%v,"ok":%d,"total":%d,"failedStep":%q,"error":%q}`,
		job.Cluster, job.Materialize, ok, len(job.Log), job.FailedStep, job.Err)
}
