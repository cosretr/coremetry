package api

// alert_rule_series.go — v0.10.1064 (operatör, prod alarm problemi detayı
// "HTTP P99 latency >3s (sustained 10 min) — <servis>: http_p99_ms değeri
// 3872.62, eşik 3000.00": "grafik olmadığı için de anlamak çok zor
// artışları").
//
//   GET /api/alert-rules/{id}/series?service=<servis>&metric=<metrik>&from=<ns>[&to=<ns>]
//
// Alarm kuralının metriğini, değerlendiricinin kayan penceresiyle zaman
// içinde verir (chstore.AlertMetricSeries) — problem detayının grafiği.
//
// Neden YENİ uç: var olanların hiçbiri ateşleneni çizmiyordu. Kural
// editörünün önizlemesi (ConditionPreview → /api/metrics/resolve) yalnız dört
// temel metriği tanıyor (http_p99_ms bilinmeyince error_rate'e düşerdi),
// spanmetrics'ten okuyor (değerlendirici p99'u service_summary_5m'den,
// http_* metriklerini ham spans'tan okur) ve kova başına ham değer çiziyor
// (kural 10 dk penceresinde ölçer). Servis grafikleri de kova başına ve
// http_method süzgeçsiz. Burada kaynak + süzgeç + pencere değerlendiricinin
// kendisi (measureAllServicesPlan ikizi, parite testli).
//
// Pencere kuraldan (WindowSec) — problem satırı taşımıyor; tek FINAL nokta
// okuması (alert_rules küçük tablo). Metrik problemin kendi metriği (kural
// sonradan düzenlenmiş olsa da başlıktaki sayıyla aynı seri). Dizi çizilemeyen
// kural türleri 404 döner, istemci bölümü hiç çizmez (sahte grafik yok): log
// sorgusu (ES/CH sayımı, servis/metrik yok), içe aktarılan watcher, hedefli
// kurallar (DB ifadesi / http_route / Kafka — ölçüleri ayrı okumalar).
//
// Rol kapısı YOK — salt okunur; viewer problem detayını görür, grafiği de
// görmeli. Önbellek 60 s; anahtar TÜM girdileri taşır (kural, metrik, servis,
// pencere, hizalı uçlar, kova). Kardinalite sınırlı: uçlar kovaya hizalı,
// pencere ham spans okumasında ≤ 6 sa, MV okumasında ≤ 24 sa (kova ≤ 360).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("alert-rule-series", (*Server).registerAlertRuleSeriesRoutes)
}

func (s *Server) registerAlertRuleSeriesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alert-rules/{id}/series", s.getAlertRuleSeries)
}

const alertRuleSeriesTTL = 60 * time.Second

// alertRuleSeriesStore — handler'ın store'dan istediği iki okuma (test dikişi).
type alertRuleSeriesStore interface {
	GetAlertRule(ctx context.Context, id string) (*chstore.AlertRule, error)
	AlertMetricSeries(ctx context.Context, q chstore.AlertMetricSeriesQuery) ([]chstore.AlertMetricPoint, error)
}

var alertRuleSeriesStoreOf = func(s *Server) alertRuleSeriesStore { return s.store }

// alertRuleSeriesUnsupported — SAF. Kural değerlendiricinin span-metrik
// yolundan (evaluateOne) geçmiyorsa sebep; geçiyorsa "".
func alertRuleSeriesUnsupported(r *chstore.AlertRule) string {
	switch {
	case r == nil:
		return "kural bulunamadı"
	case r.LogQuery != "":
		return "log sorgusu kuralı — metrik dizisi yok"
	case r.WatcherJSON != "":
		return "watcher kuralı — metrik dizisi yok"
	case r.Target != nil:
		return "hedefli kural — metrik dizisi yok"
	case r.WindowSec == 0:
		return "kuralın penceresi yok"
	}
	return ""
}

// snapAlertRuleSeriesWindow — SAF. to boş ya da gelecekteyse now; pencere
// tavanı aşarsa ESKİ uç kırpılır (açık problemin "şimdi"si görünür kalsın);
// uçlar kovaya hizalanır (başlangıç aşağı, bitiş yukarı). ok=false: boş/ters.
func snapAlertRuleSeriesWindow(from, to, now time.Time, sp chstore.AlertSeriesSpec) (f, t time.Time, ok bool) {
	if from.IsZero() || sp.StepSec <= 0 {
		return time.Time{}, time.Time{}, false
	}
	if to.IsZero() || to.After(now) {
		to = now
	}
	if sp.MaxSpan > 0 && to.Sub(from) > sp.MaxSpan {
		from = to.Add(-sp.MaxSpan)
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, false
	}
	bn := int64(sp.StepSec) * int64(time.Second)
	fs := floorDiv(from.UnixNano(), bn) * bn
	ts := -floorDiv(-to.UnixNano(), bn) * bn
	return time.Unix(0, fs), time.Unix(0, ts), true
}

// alertRuleSeriesKey — SAF; her girdi anahtarda (v0.5.187).
func alertRuleSeriesKey(ruleID, metric, service string, windowSec int, from, to time.Time, stepSec int) string {
	return fmt.Sprintf("alert-rule-series:v1:r=%s:m=%s:svc=%s:w=%d:from=%d:to=%d:b=%d",
		ruleID, metric, service, windowSec, from.UnixNano(), to.UnixNano(), stepSec)
}

type alertRuleSeriesResponse struct {
	Metric    string                     `json:"metric"`
	Service   string                     `json:"service"`
	WindowSec int                        `json:"windowSec"`
	StepSec   int                        `json:"stepSec"`
	From      int64                      `json:"from"` // ns, kovaya hizalı
	To        int64                      `json:"to"`   // ns, kovaya hizalı (dahil değil)
	Points    []chstore.AlertMetricPoint `json:"points"`
}

func (s *Server) getAlertRuleSeries(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	q := r.URL.Query()
	service := strings.TrimSpace(q.Get("service"))
	metric := strings.TrimSpace(q.Get("metric"))
	if id == "" || service == "" || metric == "" {
		writeJSONError(w, http.StatusBadRequest, "kural, service ve metric zorunlu")
		return
	}
	from := parseTime(q.Get("from"))
	if from.IsZero() {
		writeJSONError(w, http.StatusBadRequest, "from zorunlu")
		return
	}
	st := alertRuleSeriesStoreOf(s)
	rule, err := st.GetAlertRule(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, err)
		return
	}
	if err != nil {
		rule = nil
	}
	if reason := alertRuleSeriesUnsupported(rule); reason != "" {
		writeJSONError(w, http.StatusNotFound, reason)
		return
	}
	windowSec := int(rule.WindowSec)
	spec, err := chstore.AlertMetricSeriesSpec(metric, windowSec)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "bu metrik için dizi yok")
		return
	}
	// Snap anahtarın ÖNÜNDE: ham ns uçlar sınırsız ayrı girdi basardı.
	f, t, ok := snapAlertRuleSeriesWindow(from, parseTime(q.Get("to")), time.Now(), spec)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from, to'dan önce olmalı")
		return
	}
	key := alertRuleSeriesKey(id, metric, service, windowSec, f, t, spec.StepSec)
	s.serveCached(w, r, key, alertRuleSeriesTTL, func(ctx context.Context) (any, error) {
		pts, err := st.AlertMetricSeries(ctx, chstore.AlertMetricSeriesQuery{
			Metric: metric, Service: service, WindowSec: windowSec, From: f, To: t, Now: time.Now(),
		})
		if err != nil {
			return nil, err
		}
		if pts == nil {
			pts = []chstore.AlertMetricPoint{}
		}
		return alertRuleSeriesResponse{
			Metric: metric, Service: service, WindowSec: windowSec, StepSec: spec.StepSec,
			From: f.UnixNano(), To: t.UnixNano(), Points: pts,
		}, nil
	})
}
