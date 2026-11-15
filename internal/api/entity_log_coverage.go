package api

// entity_log_coverage.go — v0.10.2263 (Entity S6 parça 4, P-d; plan K5,
// docs/architecture/entity-s6-cost-note.md §8/4): "bu entity'nin logu var
// mı" işareti, TEMBEL. Tick'te hesaplanmaz; liste sayfalarında hesaplanmaz.
// Yalnız bu uç çağrılınca, entity başına TEK log sorgusu: S3'ün alan
// süzgeçleri (/api/logs?entity= ile aynı kapsam, logsEntityFilter), zorunlu
// son 1 saat penceresi, Limit 1, 5 s zaman aşımı. Sonuç serveCached ile 15 dk
// (L1 + Redis L2 + singleflight): önbellek süresince entity başına en çok bir
// log sorgusu (filo geneli). Backend hatası ya da uygulanamayan süzgeç de
// "bilinmiyor" olarak önbelleğe girer — hata eden ES'e istek başına gidilmez.
//
// GET /api/entity/log-coverage?id=<entity id>   viewer, 15 dk

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// Sabitler (ayar yok).
const (
	entityLogCoverageWindow  = time.Hour
	entityLogCoverageTTL     = 15 * time.Minute
	entityLogCoverageTimeout = 5 * time.Second
)

// EntityLogCoverage — cevap. HasLogs nil = bilinmiyor (Note söyler).
type EntityLogCoverage struct {
	ID        string    `json:"id"`
	HasLogs   *bool     `json:"hasLogs"`
	Window    string    `json:"window"`
	CheckedAt time.Time `json:"checkedAt"`
	Backend   string    `json:"backend,omitempty"`
	Note      string    `json:"note,omitempty"`
}

func init() { registerRoutesExtra("entity-log-coverage", (*Server).registerEntityLogCoverageRoutes) }

func (s *Server) registerEntityLogCoverageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/entity/log-coverage", s.getEntityLogCoverage)
}

func (s *Server) getEntityLogCoverage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if _, ok := entity.ParseID(id); !ok || len(id) > 1024 {
		writeJSONError(w, http.StatusBadRequest, "valid entity id required")
		return
	}
	key := "entity:logcov:v1:" + fnvStr(id)
	s.serveCached(w, r, key, entityLogCoverageTTL, func(ctx context.Context) (any, error) {
		return s.entityLogCoverage(ctx, id), nil
	})
}

// entityLogCoverage — tek sınırlı log sorgusu; hata cevabın içinde.
func (s *Server) entityLogCoverage(ctx context.Context, id string) EntityLogCoverage {
	now := time.Now().UTC()
	out := EntityLogCoverage{ID: id, Window: entityLogCoverageWindow.String(), CheckedAt: now}
	if s.logs == nil {
		out.Note = "log backend'i yapılandırılmamış"
		return out
	}
	out.Backend = s.logs.Backend()
	f := logstore.Filter{From: now.Add(-entityLogCoverageWindow), To: now, Limit: 1}
	qctx, cancel := context.WithTimeout(ctx, entityLogCoverageTimeout)
	defer cancel()
	if _, msg := s.logsEntityFilter(qctx, id, &f); msg != "" {
		out.Note = "kapsam çözülemedi: " + msg
		return out
	}
	page, err := s.logs.Search(qctx, f)
	if err != nil {
		out.Note = "log sorgusu başarısız: " + err.Error()
		return out
	}
	if page == nil {
		out.Note = "log backend'i boş cevap döndü"
		return out
	}
	if len(page.UnappliedFilters) > 0 {
		out.Note = "backend süzgeci uygulayamadı (" + strings.Join(page.UnappliedFilters, ", ") + ") — sonuç entity'ye özgü değil"
		return out
	}
	has := len(page.Logs) > 0
	out.HasLogs = &has
	return out
}
