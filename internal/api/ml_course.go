package api

// ml_course.go — öğrenme döngüsü Faz 2 parti B: /api/ml/{id}/replay
// yanıtının `course` alanı (ml.ExplainCourse) için okumalar. Saf kurulum
// internal/ml/explain_course.go'da; burada yalnız kaynaklar.
//
// v0.10.1870 — endpoint'li trace olayında EN ÇOK bir ml_series okuması (CH,
// MLSeriesRange: zaman sınırlı + LIMIT + max_execution_time) — ısı haritası
// çizgisinin olay öncesi p95'i. Isı haritasının kendisi FE'den mevcut
// /api/spans/heatmap ile (kendi serveCached'i). Okuma hatası katmanı sessizce
// çizgisiz bırakır; ana grafik (olay anı kaydı) etkilenmez.
//
// v0.10.1871 — log olayında (taban `_error` / `_total`) AYNI tek okuma:
// servisin mesaj kalıbı aileleri + seviye serileri (ml_series, CH). ES'e
// gitmez. Okunamazsa yığın tek katman ("hata satırları") kalır.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

const mlCourseReadBudget = 5 * time.Second

func (s *Server) mlCourse(ctx context.Context, in ml.CardInput, cfg chstore.MLConfig) ml.ExplainCourse {
	var series map[string][]chstore.MLPoint
	if ml.CourseSeriesNeeded(in) {
		from, to := ml.CourseReadWindow(in)
		cctx, cancel := context.WithTimeout(ctx, mlCourseReadBudget)
		defer cancel()
		if x, err := s.store.MLSeriesRange(cctx, in.Signal, in.Service, from, to, cfg.BucketSec); err == nil {
			series = x
		}
	}
	return ml.ExplainCourse{Trace: ml.BuildTraceCourse(in, series), Log: ml.BuildLogCourse(in, series)}
}
