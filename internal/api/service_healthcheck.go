package api

// service_healthcheck.go — v0.10.2039 (servis sağlık kontrolü, dilim 1;
// spec docs/plans/spec-service-healthcheck-runbook.md, operatör onayı
// 2026-11-12).
//
//	GET /api/services/{name}/healthcheck?env=&window=15m|1h|6h   viewer
//
// Gövde mcptools.ReadServiceHealthcheck — CoSRE niyeti ve MCP aracı AYNI
// fonksiyonu çağırır (dört yüzey, tek hüküm). Kurallardan deterministik;
// model yok.
//
// NEDEN rol kapısı YOK: salt-okunur bir kontrol turu; restart / scale gibi
// eylem yok, durum yazmaz → audit yok. Viewer da koşturabilir (operatör
// kararı; runbook execute'un editor kapısı burada uygulanmaz çünkü bash/http
// adımı yok).
//
// NEDEN ayrı rota, mevcut /api/services/{name}/bundle değil: bundle sayfa
// açılışında koşar; bu tur YALNIZ kullanıcı düğmeye bastığında koşar (cluster
// başına ~13 Thanos sorgusu) — varsayılan açılışa yük eklenmez.
//
// Önbellek 60 sn: art arda tıklama kaynakları yeniden dövmez. Anahtar TÜM
// girdileri hash'ler (v0.5.187): servis, env, pencere, etkin cluster kümesi
// (sıralı, span değerleriyle) ve katalog sürümü. Sorgular kendi yüzey
// etiketini taşır (chstore.WithQueryTag "healthcheck") — CH query_log ve ES
// istek sayacında ayrı satır.
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): kayıt init()'te registerRoutesExtra.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func init() {
	registerRoutesExtra("service-healthcheck", (*Server).registerServiceHealthcheckRoutes)
}

func (s *Server) registerServiceHealthcheckRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/healthcheck", s.getServiceHealthcheck)
}

// v0.10.2048 — anahtar mcptools.HealthcheckCacheKey'de (sohbet ve MCP aracı
// AYNI girdiyi okur/yazar: Memo ve serveCached ikisi de cachedJSON).

func (s *Server) getServiceHealthcheck(w http.ResponseWriter, r *http.Request) {
	service := strings.TrimSpace(r.PathValue("name"))
	if service == "" {
		writeJSONError(w, http.StatusBadRequest, "servis adı zorunlu")
		return
	}
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	window := mcptools.HCNormalizeWindow(q.Get("window"))
	d := s.mcpDeps()
	var clusters []mcptools.ClusterRef
	if d.Clusters != nil {
		clusters = d.Clusters()
	}
	key := mcptools.HealthcheckCacheKey(service, env, window, clusters)
	s.serveCached(w, r, key, mcptools.HealthcheckTTL, func(ctx context.Context) (any, error) {
		ctx = chstore.WithQueryTag(ctx, "healthcheck")
		res, err := mcptools.ReadServiceHealthcheck(ctx, d, mcptools.HealthcheckRequest{Service: service, Env: env, Window: window})
		if errors.Is(err, mcptools.ErrHealthcheckNoService) {
			return nil, errBadRequest
		}
		return res, err
	})
}
