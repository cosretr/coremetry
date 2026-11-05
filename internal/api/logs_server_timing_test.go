package api

// logs_server_timing_test.go — v0.10.1817 (Log Search Faz 1 dilim 1.1,
// audit §5): /api/logs* uçları `Server-Timing` basar (cache katmanı + es +
// total); kaydedicisiz serveCached çağrısı (diğer tüm uçlar) basmaz.
// Gövde ve durum kodu değişmez — yalnız başlık eklenir.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/servertiming"
)

// timingLogStore — arka uç çağrısında ES transport'unun yapacağını taklit
// eder: bağlamdaki kaydediciye `es` süresi yazar.
type timingLogStore struct{ logstore.Store }

func (timingLogStore) Search(ctx context.Context, _ logstore.Filter) (*logstore.Page, error) {
	servertiming.Add(ctx, "es", 7*time.Millisecond)
	return &logstore.Page{}, nil
}
func (timingLogStore) Histogram(ctx context.Context, _ logstore.Filter, _ int, _ string) ([]logstore.LogSeries, error) {
	servertiming.Add(ctx, "es", 3*time.Millisecond)
	return []logstore.LogSeries{}, nil
}
func (timingLogStore) FieldStats(ctx context.Context, _ logstore.Filter, _ string, _ int) (*logstore.FieldStatsResult, error) {
	servertiming.Add(ctx, "es", 2*time.Millisecond)
	return &logstore.FieldStatsResult{}, nil
}
func (timingLogStore) Backend() string { return "test" }

func timingServer() *Server {
	return &Server{logs: timingLogStore{}, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
}

func TestLogsEndpointsEmitServerTiming(t *testing.T) {
	s := timingServer()
	cases := []struct {
		name string
		run  func(*httptest.ResponseRecorder)
	}{
		{"GET /api/logs", func(w *httptest.ResponseRecorder) {
			s.getLogs(w, httptest.NewRequest("GET", "/api/logs?service=orders-api&from=1&to=2", nil))
		}},
		{"POST /api/logs/search", func(w *httptest.ResponseRecorder) {
			s.postLogsSearch(w, httptest.NewRequest("POST", "/api/logs/search", strings.NewReader(`{"service":"payments-mobile","from":1,"to":3}`)))
		}},
		{"GET /api/logs/timeseries", func(w *httptest.ResponseRecorder) {
			s.getLogsTimeseries(w, httptest.NewRequest("GET", "/api/logs/timeseries?service=orders-api&from=1&to=2", nil))
		}},
		{"GET /api/logs/fieldstats", func(w *httptest.ResponseRecorder) {
			s.getLogsFieldStats(w, httptest.NewRequest("GET", "/api/logs/fieldstats?field=level&from=1&to=2", nil))
		}},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		c.run(w)
		if w.Code != 200 {
			t.Fatalf("%s: status %d — %s", c.name, w.Code, w.Body.String())
		}
		st := w.Header().Get("Server-Timing")
		for _, want := range []string{`cache;desc="MISS"`, "es;dur=", "total;dur="} {
			if !strings.Contains(st, want) {
				t.Errorf("%s: Server-Timing %q, %q içermeli", c.name, st, want)
			}
		}
		if w.Header().Get("X-Cache") != "MISS" {
			t.Errorf("%s: X-Cache değişmemeli, %q", c.name, w.Header().Get("X-Cache"))
		}
	}
	// İkinci okuma L1'den: es girdisi YOK (arka uca gidilmedi), katman HIT-L1.
	w := httptest.NewRecorder()
	s.getLogs(w, httptest.NewRequest("GET", "/api/logs?service=orders-api&from=1&to=2", nil))
	if st := w.Header().Get("Server-Timing"); !strings.HasPrefix(st, `cache;desc="HIT-L1", total;dur=`) {
		t.Errorf("L1 isabeti: %q", st)
	}
}

func TestServeCachedWithoutRecorderHasNoServerTiming(t *testing.T) {
	s := timingServer()
	w := httptest.NewRecorder()
	s.serveCached(w, httptest.NewRequest("GET", "/api/other", nil), "k", time.Second, func(context.Context) (any, error) {
		return map[string]int{"a": 1}, nil
	})
	if st := w.Header().Get("Server-Timing"); st != "" {
		t.Fatalf("kaydedicisiz uçta başlık basılmamalı: %q", st)
	}
}
