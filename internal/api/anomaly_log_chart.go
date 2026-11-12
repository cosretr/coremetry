package api

// anomaly_log_chart.go — v0.10.2035 (anomali detayı: "grafik servis
// sayfasının paneliyle"; son dilim LOG olayları).
//
//   GET /api/anomalies/log-chart?service=<svc>&search=<q>&severity=<n>[&pattern=<ad>]&from=<ns>&to=<ns>
//
// v0.10.2036 — eski anomaly_events log türleri de bu uçla: log_template_new
// `search` (olayın LogQuery'si), log_pattern `pattern` (küratörlü desen adı;
// /logs ile aynı yüklem, applyLogsPatternParam).
//
// ML log olayının detay grafiği, Logs sayfasının histogramıyla AYNI okuma
// (logstore.Histogram, seviye bantlı) ve AYNI süzgeçle: istemci süzgeci
// kartın "Bu mesajı içeren loglar / Hata logları / Loglar" bağlantısından
// (ml.explain_links logsLink) okur — servis + q + severity. /api/logs/timeseries
// yerine ayrı uç, çünkü:
//   - ES istek sayacında KENDİ yüzey etiketi (`anomaly-detail-log-chart`)
//     görünsün (6 Kas ES yükü kuralı: ölçülmeden yeni ES yolu yok);
//   - pencere ve kova SUNUCUDA sabitlenir: ≤ 3 sa → 1 dk kova, daha uzun
//     pencerede kova büyür (≤ 180 kova), pencere en çok 7 gün.
// Sayfa açılışı başına TEK _search (size 0, tek date_histogram + seviye
// alt-agregasyonu). Servis zorunlu: servissiz istek tüm indeksleri tarardı.
// Rol kapısı yok (salt okunur; viewer detayı görür). Önbellek 30 s; anahtar
// TÜM girdileri taşır (servis, arama FNV, seviye, hizalı pencere, kova).
// Yavaş/erişilemeyen arka uç → boş seri (istemci kayıtlı görüntüye düşer).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func init() {
	registerRoutesExtra("anomaly-log-chart", (*Server).registerAnomalyLogChartRoutes)
}

func (s *Server) registerAnomalyLogChartRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/anomalies/log-chart", s.getAnomalyLogChart)
}

const (
	anomalyLogChartSurface    = "anomaly-detail-log-chart"
	anomalyLogChartMaxBuckets = 180 // 3 sa × 1 dk
	anomalyLogChartMaxSpan    = 7 * 24 * time.Hour
	anomalyLogChartTTL        = 30 * time.Second
)

// snapAnomalyLogChartWindow — SAF. to boş/gelecekte → now; pencere ≤ 7 gün
// (eski uç kırpılır); kova = hizalı kova sayısı ≤ 180 olan EN KÜÇÜK basamak
// (patternSeriesRungs: 1 dk, 2 dk, 5 dk … 1 g); uçlar kovaya hizalanır
// (başlangıç aşağı, bitiş yukarı). ok=false: pencere boş/ters.
func snapAnomalyLogChartWindow(from, to, now time.Time) (f, t time.Time, bucketSec int, ok bool) {
	if from.IsZero() {
		return time.Time{}, time.Time{}, 0, false
	}
	if to.IsZero() || to.After(now) {
		to = now
	}
	if to.Sub(from) > anomalyLogChartMaxSpan {
		from = to.Add(-anomalyLogChartMaxSpan)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, 0, false
	}
	for _, b := range patternSeriesRungs {
		bn := int64(b) * int64(time.Second)
		fs := floorDiv(from.UnixNano(), bn) * bn
		ts := -floorDiv(-to.UnixNano(), bn) * bn
		if (ts-fs)/bn <= anomalyLogChartMaxBuckets {
			return time.Unix(0, fs), time.Unix(0, ts), b, true
		}
	}
	return time.Time{}, time.Time{}, 0, false
}

// anomalyLogChartKey — SAF; her girdi anahtarda (v0.5.187). Arama metni FNV
// ile (uzun şablon metni anahtarı şişirmesin; fnvStr ayırıcılı).
func anomalyLogChartKey(svc, search, pattern string, sev uint8, from, to time.Time, bucketSec int) string {
	return fmt.Sprintf("anomaly-log-chart:v2:%s:sev=%d:from=%d:to=%d:b=%d",
		fnvStr(svc, search, pattern), sev, from.UnixNano(), to.UnixNano(), bucketSec)
}

func (s *Server) getAnomalyLogChart(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	svc := strings.TrimSpace(q.Get("service"))
	if svc == "" {
		writeJSONError(w, http.StatusBadRequest, "service parametresi zorunlu")
		return
	}
	if s.logs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "log arka ucu yapılandırılmamış")
		return
	}
	sev, _ := strconv.Atoi(q.Get("severity"))
	if sev < 0 || sev > 24 {
		sev = 0
	}
	from, to, bucketSec, ok := snapAnomalyLogChartWindow(parseTime(q.Get("from")), parseTime(q.Get("to")), time.Now())
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from zorunlu ve to'dan önce olmalı")
		return
	}
	search := strings.TrimSpace(q.Get("search"))
	f := logstore.Filter{Service: svc, Search: search, SeverityMin: uint8(sev), From: from, To: to}
	if !applyLogsPatternParam(w, q, &f) {
		return
	}
	key := anomalyLogChartKey(svc, search, strings.TrimSpace(q.Get("pattern")), uint8(sev), from, to, bucketSec)
	s.serveCached(w, r, key, anomalyLogChartTTL, func(ctx context.Context) (any, error) {
		// Rota etiketinin yerine yüzey adı: ES istek sayacı bu grafiği ayrı sayar.
		tctx, cancel := context.WithTimeout(chstore.WithQueryTag(ctx, anomalyLogChartSurface), 30*time.Second)
		defer cancel()
		series, err := s.logs.Histogram(tctx, f, bucketSec, "severity")
		if err != nil {
			if mapped := logstore.MapBackendSlow(err, tctx, ctx); errors.Is(mapped, logstore.ErrBackendSlow) {
				log.Printf("[anomaly] log chart degraded (backend=%s, svc=%q): %v", s.logs.Backend(), svc, err)
				return []logstore.LogSeries{}, nil
			}
			return nil, fmt.Errorf("%w: log chart: %v", errUpstream, err)
		}
		if series == nil {
			series = []logstore.LogSeries{}
		}
		return series, nil
	})
}
