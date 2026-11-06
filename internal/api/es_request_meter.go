package api

// es_request_meter.go — v0.10.1878 (operatör, ES aşırı yük olayı 2026-11-06:
// "Elastic'e giden sorgu sayısını takip edeyim ben admin olarak, artarsa
// anlarım; şu an körüm").
//
// İki parça:
//   - StartESRequestMeter: her pod'da dakikada bir, tamamlanmış dakikaların ES
//     istek hücrelerini (logstore/es_meter.go) kalıcı seriye yazar
//     (chstore/self_es_series.go). Yazılamayan hücreler ATILIR (birikmez).
//   - GET /api/admin/elastic/requests: tüm pod'ların toplamı; zaman serisi +
//     işlem türü ve yüzey kırılımı + "son 15 dk" ile "pencerenin geri kalanı"
//     karşılaştırması. Yalnız admin; ClickHouse'tan okur (ES'e istek ATMAZ).

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func init() { registerRoutesExtra("es_request_meter", (*Server).registerESRequestMeterRoutes) }

func (s *Server) registerESRequestMeterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/elastic/requests", auth.RequireRole(auth.RoleAdmin, s.adminElasticRequests))
}

const (
	esMeterFlushEvery = 30 * time.Second
	// esMeterRecentMin — "son N dakika" penceresi (içinde bulunulan dakika ve
	// yazma gecikmesi için son 1 dakika hariç tutulur).
	esMeterRecentMin  = 15
	esMeterTopSurface = 15
)

// esMetricSink — yazıcının tek bağımlılığı (*chstore.Store karşılar).
type esMetricSink interface {
	InsertMetrics(ctx context.Context, pts []*chstore.MetricPoint) error
}

// StartESRequestMeter — dakikalık ES istek hücrelerini kalıcı seriye yazar;
// ctx bitene dek koşar. Her rolde çağrılır (ES'e istek atan her pod sayılır).
func StartESRequestMeter(ctx context.Context, sink esMetricSink, pod string) {
	if sink == nil {
		return
	}
	ctx = chstore.WithQueryTag(ctx, "worker:es-meter")
	tk := time.NewTicker(esMeterFlushEvery)
	defer tk.Stop()
	var lastErrLog time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			if err := flushESRequestStats(ctx, sink, pod, now); err != nil && time.Since(lastErrLog) > 5*time.Minute {
				lastErrLog = time.Now()
				log.Printf("[es-meter] istek sayacı yazılamadı (hücreler atıldı): %v", err)
			}
		}
	}
}

// flushESRequestStats — tamamlanmış dakikaları boşaltıp yazar.
func flushESRequestStats(ctx context.Context, sink esMetricSink, pod string, now time.Time) error {
	stats := logstore.DrainESRequestStats(now)
	if len(stats) == 0 {
		return nil
	}
	cells := make([]chstore.SelfESCell, 0, len(stats))
	for _, st := range stats {
		cells = append(cells, chstore.SelfESCell{Minute: st.Minute, Op: st.Op, Surface: st.Surface, Count: st.Count, Errors: st.Errors, DurMs: st.DurMs})
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return sink.InsertMetrics(wctx, chstore.SelfESRequestPoints(cells, pod))
}

// ── Okuma ───────────────────────────────────────────────────────────────

type esRequestPoint struct {
	T      int64   `json:"t"` // kova başı, unix saniye
	Count  float64 `json:"count"`
	Errors float64 `json:"errors"`
	AvgMs  float64 `json:"avgMs"`
}

type esRequestOp struct {
	Op    string  `json:"op"`
	Count float64 `json:"count"`
}

type esRequestSurface struct {
	Surface      string  `json:"surface"`
	Count        float64 `json:"count"`
	Errors       float64 `json:"errors"`
	AvgMs        float64 `json:"avgMs"`
	RecentPerMin float64 `json:"recentPerMin"`
	BasePerMin   float64 `json:"basePerMin"`
}

type esRequestsPayload struct {
	WindowMin    int                `json:"windowMin"`
	StepSec      int                `json:"stepSec"`
	RecentMin    int                `json:"recentMin"`
	Total        float64            `json:"total"`
	Errors       float64            `json:"errors"`
	RecentPerMin float64            `json:"recentPerMin"`
	BasePerMin   float64            `json:"basePerMin"`
	Points       []esRequestPoint   `json:"points"`
	ByOp         []esRequestOp      `json:"byOp"`
	BySurface    []esRequestSurface `json:"bySurface"`
}

// esRequestsWindow — SAF: `window` paramı (dakika) → pencere + adım. Kapalı
// küme (önbellek anahtarı sınırlı kalsın); tanınmayan değer 180.
func esRequestsWindow(raw string) (windowMin, stepSec int) {
	n, _ := strconv.Atoi(raw)
	switch n {
	case 60:
		return 60, 60
	case 360:
		return 360, 300
	case 1440:
		return 1440, 900
	}
	return 180, 60
}

// summarizeESRequests — SAF: hücreler → yanıt. `to` pencerenin (dışlayıcı)
// sonu; "son N dk" = [to−N dk, to), geri kalanı taban.
func summarizeESRequests(buckets []chstore.SelfESBucket, from, to time.Time, windowMin, stepSec int) esRequestsPayload {
	out := esRequestsPayload{WindowMin: windowMin, StepSec: stepSec, RecentMin: esMeterRecentMin,
		Points: []esRequestPoint{}, ByOp: []esRequestOp{}, BySurface: []esRequestSurface{}}
	recentFrom := to.Add(-esMeterRecentMin * time.Minute).Unix()
	baseMin := to.Sub(from).Minutes() - esMeterRecentMin
	type acc struct{ count, errs, ms, recent, base float64 }
	byT := map[int64]*acc{}
	byOp := map[string]float64{}
	bySurf := map[string]*acc{}
	var recent, base float64
	for _, b := range buckets {
		a := byT[b.T]
		if a == nil {
			a = &acc{}
			byT[b.T] = a
		}
		a.count += b.Count
		a.errs += b.Errors
		a.ms += b.DurMs
		byOp[b.Op] += b.Count
		sa := bySurf[b.Surface]
		if sa == nil {
			sa = &acc{}
			bySurf[b.Surface] = sa
		}
		sa.count += b.Count
		sa.errs += b.Errors
		sa.ms += b.DurMs
		if b.T >= recentFrom {
			recent += b.Count
			sa.recent += b.Count
		} else {
			base += b.Count
			sa.base += b.Count
		}
		out.Total += b.Count
		out.Errors += b.Errors
	}
	perMin := func(v, minutes float64) float64 {
		if minutes <= 0 {
			return 0
		}
		return v / minutes
	}
	out.RecentPerMin = perMin(recent, esMeterRecentMin)
	out.BasePerMin = perMin(base, baseMin)
	// Zaman serisi: boş kovalar 0 ile doldurulur (çizgi "veri yok" ile "0 istek"i
	// karıştırmasın).
	step := int64(stepSec)
	for t := from.Unix() - from.Unix()%step; t < to.Unix(); t += step {
		p := esRequestPoint{T: t}
		if a := byT[t]; a != nil {
			p.Count, p.Errors = a.count, a.errs
			if a.count > 0 {
				p.AvgMs = a.ms / a.count
			}
		}
		out.Points = append(out.Points, p)
	}
	for op, c := range byOp {
		out.ByOp = append(out.ByOp, esRequestOp{Op: op, Count: c})
	}
	sort.Slice(out.ByOp, func(i, j int) bool {
		if out.ByOp[i].Count != out.ByOp[j].Count {
			return out.ByOp[i].Count > out.ByOp[j].Count
		}
		return out.ByOp[i].Op < out.ByOp[j].Op
	})
	for name, a := range bySurf {
		row := esRequestSurface{Surface: name, Count: a.count, Errors: a.errs,
			RecentPerMin: perMin(a.recent, esMeterRecentMin), BasePerMin: perMin(a.base, baseMin)}
		if a.count > 0 {
			row.AvgMs = a.ms / a.count
		}
		out.BySurface = append(out.BySurface, row)
	}
	// Sıra: SON 15 dakikada en çok istek atan önde (artışı arayan göz için).
	sort.Slice(out.BySurface, func(i, j int) bool {
		a, b := out.BySurface[i], out.BySurface[j]
		if a.RecentPerMin != b.RecentPerMin {
			return a.RecentPerMin > b.RecentPerMin
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Surface < b.Surface
	})
	if len(out.BySurface) > esMeterTopSurface {
		rest := esRequestSurface{Surface: "diğerleri"}
		var restMs float64
		for _, r := range out.BySurface[esMeterTopSurface:] {
			rest.Count += r.Count
			rest.Errors += r.Errors
			rest.RecentPerMin += r.RecentPerMin
			rest.BasePerMin += r.BasePerMin
			restMs += r.AvgMs * r.Count
		}
		if rest.Count > 0 {
			rest.AvgMs = restMs / rest.Count
		}
		out.BySurface = append(out.BySurface[:esMeterTopSurface:esMeterTopSurface], rest)
	}
	return out
}

func (s *Server) adminElasticRequests(w http.ResponseWriter, r *http.Request) {
	windowMin, stepSec := esRequestsWindow(r.URL.Query().Get("window"))
	// Pencere sonu: tamamlanmış ve YAZILMIŞ son dakika (içinde bulunulan dakika
	// + bir dakika yazma payı hariç).
	to := time.Now().Truncate(time.Minute).Add(-time.Minute)
	from := to.Add(-time.Duration(windowMin) * time.Minute)
	key := "admin-es-requests:v1:" + fnvStr(strconv.Itoa(windowMin), strconv.FormatInt(to.Unix(), 10))
	s.serveCached(w, r, key, 20*time.Second, func(ctx context.Context) (any, error) {
		if s.store == nil {
			return summarizeESRequests(nil, from, to, windowMin, stepSec), nil
		}
		buckets, err := s.store.SelfESRequestSeries(ctx, from, to, stepSec)
		if err != nil {
			return nil, err
		}
		return summarizeESRequests(buckets, from, to, windowMin, stepSec), nil
	})
}
