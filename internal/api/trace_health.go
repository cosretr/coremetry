package api

// trace_health.go — v0.10.757 "Trace hattı sağlığı" paneli (trace bütünlüğü
// denetimi 2026-09-17; operatör onayı: sihirbaz değil panel, önce pod-içi).
//
//	GET /api/admin/clickhouse/trace-health?range_s=3600   (admin; serveCached 30 s)
//
// Üç kart, bölüm başına YUMUŞAK hata (bir CH sorgusu düşerse öteki kartlar
// gelir, `errors` alanı hangisinin düştüğünü söyler):
//   - kayıp: BU POD'un ingest sayaçları (accepted/dropped/write_failed/
//     kuyruk) + v0.10.754 reject sayaçları + dönüştürücü degrade'leri +
//     dağıtık spool durumu, yanında CH'de saklanan span/5 dk (service_
//     summary_5m). Mutabakat pod-içi: çok-podlu ingest'te toplam için ayrı
//     tablo gerekir (Faz B, ayrı dilim) — cevap `pod.host` ile hangi podun
//     saydığını söyler.
//   - kapsama: kök tanımı, MV gap günleri, son 5 dk kök oranı (strict /
//     entry) — mevcut TraceRootCoverage, kısa pencere.
//   - ad kalitesi: çıplak fiil adlı span payı, boş ad, ayrık ad, servis
//     başına ayrık ad ilk 10 (24 sa).
// api.go BÜYÜMEZ: route defteri.

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/otlp"
)

func init() { registerRoutesExtra("trace-health", (*Server).registerTraceHealthRoutes) }

func (s *Server) registerTraceHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/clickhouse/trace-health", auth.RequireRole(auth.RoleAdmin, s.getTraceHealth))
}

// traceHealthRange — SAF: 300..86400 sn, varsayılan 3600.
func traceHealthRange(raw string) int {
	r := parseInt(raw, 3600)
	if r < 300 {
		return 300
	}
	if r > 86400 {
		return 86400
	}
	return r
}

type traceHealthPod struct {
	Host string `json:"host"`
	// IngestRole (v0.10.760) — bu pod OTLP alıyor mu (COREMETRY_MODE ingest/all).
	// api-rolü podda sayaçlar hep sıfırdır; "kayıp yok" demek yanıltırdı
	// (prod ekranı: coremetry-api podu). Sayaçlar ingest podlarındadır.
	IngestRole  bool              `json:"ingestRole"`
	Accepted    int64             `json:"accepted"`
	Dropped     int64             `json:"dropped"`
	WriteFailed int64             `json:"writeFailed"`
	Queued      int               `json:"queued"`
	Capacity    int               `json:"capacity"`
	Rejects     map[string]uint64 `json:"rejects"`
	Degrades    map[string]uint64 `json:"degrades"`
}

type traceHealthCoverage struct {
	Def           string   `json:"def"`
	GapDays       []string `json:"gapDays"`
	RangeS        int      `json:"rangeS"`
	Source        string   `json:"source,omitempty"`
	Traces        uint64   `json:"traces"`
	WithRoot      uint64   `json:"withRoot"`
	WithEntryRoot uint64   `json:"withEntryRoot"`
}

type traceHealthResponse struct {
	GeneratedAt   int64                        `json:"generatedAt"`
	RangeS        int                          `json:"rangeS"`
	Pod           traceHealthPod               `json:"pod"`
	Spool         *chstore.DistributionQueue   `json:"spool,omitempty"`
	SpoolDegraded bool                         `json:"spoolDegraded"`
	SpoolDetail   string                       `json:"spoolDetail,omitempty"`
	Stored        []chstore.StoredSpanBucket   `json:"stored"`
	StoredTotal   uint64                       `json:"storedTotal"`
	Coverage      traceHealthCoverage          `json:"coverage"`
	Names         chstore.OperationNameQuality `json:"names"`
	Errors        map[string]string            `json:"errors,omitempty"`
}

// sumStored — SAF.
func sumStored(b []chstore.StoredSpanBucket) uint64 {
	var n uint64
	for _, x := range b {
		n += x.Spans
	}
	return n
}

func (s *Server) getTraceHealth(w http.ResponseWriter, r *http.Request) {
	rangeS := traceHealthRange(r.URL.Query().Get("range_s"))
	key := "admin:trace-health:range=" + strconv.Itoa(rangeS)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		host, _ := os.Hostname()
		resp := traceHealthResponse{
			GeneratedAt: now.UnixNano(), RangeS: rangeS,
			Errors: map[string]string{},
			Pod: traceHealthPod{
				Host:       host,
				IngestRole: !s.roleIngestOff,
				Rejects:    otlp.IngestRejectCounts(),
				Degrades:   otlp.ConvertDegradeCounts(),
			},
			Stored: []chstore.StoredSpanBucket{},
		}
		if s.ing != nil && s.ing.Spans != nil {
			resp.Pod.Accepted = s.ing.Spans.Accepted()
			resp.Pod.Dropped = s.ing.Spans.Dropped()
			resp.Pod.WriteFailed = s.ing.Spans.WriteFailed()
			resp.Pod.Queued, resp.Pod.Capacity = s.ing.Spans.QueueLen(), s.ing.Spans.Capacity()
		}
		resp.Spool, resp.SpoolDegraded, resp.SpoolDetail = s.distributionBacklog()

		from := now.Add(-time.Duration(rangeS) * time.Second).Truncate(5 * time.Minute)
		if b, err := s.store.StoredSpanBuckets(ctx, from, now); err != nil {
			resp.Errors["stored"] = err.Error()
		} else {
			resp.Stored, resp.StoredTotal = b, sumStored(b)
		}

		resp.Coverage = traceHealthCoverage{Def: string(s.store.TraceRootDef()), GapDays: s.store.TraceMVGapDayList(ctx), RangeS: 300}
		if rows, source, err := s.store.TraceRootCoverage(ctx, 300); err != nil {
			resp.Errors["coverage"] = err.Error()
		} else {
			resp.Coverage.Source = source
			resp.Coverage.Traces, resp.Coverage.WithRoot = rootCoverageTotals(rows)
			resp.Coverage.WithEntryRoot = rootCoverageEntryRoot(rows)
		}

		if q, err := s.store.OperationNameQuality(ctx, now.Add(-24*time.Hour)); err != nil {
			resp.Errors["names"] = err.Error()
		} else {
			resp.Names = q
		}
		if len(resp.Errors) == 0 {
			resp.Errors = nil
		}
		return resp, nil
	})
}
