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
//
// v0.10.1872 — az çağrılan serviste (profil low_traffic / kanıt lowTraffic)
// +1 CH: penceredeki giriş çağrıları (ham spans, LIMIT 500, zaman sınırlı,
// max_execution_time 5) — nokta grafiği; ısı haritası o olayda istenmez.

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
	var calls ml.CourseCalls
	if ml.CourseCallsNeeded(in) {
		from, to := ml.CourseWindow(in)
		cctx, cancel := context.WithTimeout(ctx, mlCourseReadBudget)
		defer cancel()
		pts, err := s.store.MLCallPoints(cctx, in.Service, ml.CourseCallRoute(in), from, to, chstore.MLCallPointsLimit)
		calls = ml.CourseCalls{Points: pts, OK: err == nil}
	}
	return ml.ExplainCourse{Trace: ml.BuildTraceCourse(in, series, calls), Log: ml.BuildLogCourse(in, series)}
}
