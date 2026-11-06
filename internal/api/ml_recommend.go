package api

// ml_recommend.go — v0.10.1886 (operatör 2026-11-06: "Settings'te anomali
// ayarları çok karmaşık, sen default kur"; VM'de HTTP, gRPC ve Kafka var).
//
//	GET /api/admin/ml/recommended → {vmConfigured, checks, changes, config}
//
// SALT-OKUNUR: ML dedektörlerini besleyen kaynakları VictoriaMetrics'te ölçer
// (ml.SourceChecks — tik dışı, ≤ ~18 kısa sorgu, 4 eşzamanlı, Elasticsearch yok) ve
// chstore.RecommendMLConfig ile önerilen aç/kapa kurulumunu döner. HİÇBİR ŞEY
// YAZMAZ: admin "Uygula"ya basınca istemci `config`i bildik PUT /api/admin/ml'e
// gönderir (audit eski/yeni değerle orada düşer). Önbellek yok (admin tıkı;
// ölçüm taze olmalı). api.go BÜYÜMEZ: rota bu dosyada.

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml_recommend", (*Server).registerMLRecommendRoutes) }

func (s *Server) registerMLRecommendRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/ml/recommended", auth.RequireRole(auth.RoleAdmin, s.getMLRecommended))
}

// mlRecommendBudget — ölçümün toplam süresi; route / ingress 30 sn sınırının altında.
const mlRecommendBudget = 25 * time.Second

type mlRecommendedResponse struct {
	VMConfigured bool                        `json:"vmConfigured"`
	Checks       []chstore.MLSourceCheck     `json:"checks"`
	Changes      []chstore.MLRecommendChange `json:"changes"`
	Config       chstore.MLConfig            `json:"config"`
}

// mlRecommended — SAF sayılır (VM sahte olabilir): ölçüm + öneri.
func mlRecommended(ctx context.Context, vm anomaly.REDVM, cur chstore.MLConfig, now time.Time) mlRecommendedResponse {
	checks := ml.SourceChecks(ctx, vm, cur, now)
	rec, changes := chstore.RecommendMLConfig(cur, checks)
	if changes == nil {
		changes = []chstore.MLRecommendChange{}
	}
	return mlRecommendedResponse{VMConfigured: vm != nil && vm.Configured(), Checks: checks, Changes: changes, Config: rec}
}

func (s *Server) getMLRecommended(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), mlRecommendBudget)
	defer cancel()
	var vm anomaly.REDVM
	if s.vmetrics != nil { // nil işaretçi arayüze sarılmasın
		vm = s.vmetrics
	}
	writeJSON(w, mlRecommended(ctx, vm, s.store.GetMLConfig(ctx), time.Now()))
}
