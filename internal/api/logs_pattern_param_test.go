package api

// logs_pattern_param_test.go — v0.10.1071 (operatör, prod ES: "Logları aç"
// pivotu grafiğin saydığından başka satırlar gösterdi). /logs okumaları
// `pattern=<küratörlü ad>` alır; sunucu adı DEDEKTÖRÜN spec'ine çevirip
// Filter.Pattern'e koyar (arka uç yüklemi uygular — parite testi
// logstore/pattern_logs_filter_test.go). Çivilenen: ad → spec uçtan uca,
// bilinmeyen ad 400 (arka uca gidilmez), her önbellek anahtarı deseni taşır.

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/logstore"
)

type patternFilterLogStore struct {
	logstore.Store
	calls int
	got   logstore.Filter
}

func (s *patternFilterLogStore) Backend() string { return "test" }
func (s *patternFilterLogStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	s.calls++
	s.got = f
	return &logstore.Page{}, nil
}
func (s *patternFilterLogStore) Histogram(_ context.Context, f logstore.Filter, _ int, _ string) ([]logstore.LogSeries, error) {
	s.calls++
	s.got = f
	return []logstore.LogSeries{}, nil
}
func (s *patternFilterLogStore) FieldStats(_ context.Context, f logstore.Filter, _ string, _ int) (*logstore.FieldStatsResult, error) {
	s.calls++
	s.got = f
	return &logstore.FieldStatsResult{}, nil
}

func patternFilterServer(st *patternFilterLogStore) *Server {
	return &Server{logs: st, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
}

func TestLogsPatternFromQuery(t *testing.T) {
	if p, ok := logsPatternFromQuery("  "); p != nil || !ok {
		t.Fatalf("boş → süzgeç yok: %v %v", p, ok)
	}
	if p, ok := logsPatternFromQuery("Some retired pattern"); p != nil || ok {
		t.Fatalf("bilinmeyen ad reddedilmeli: %v %v", p, ok)
	}
	want, _ := anomaly.LogPatternSpecByName("External system rejected")
	p, ok := logsPatternFromQuery(" External system rejected ")
	if !ok || p == nil || !reflect.DeepEqual(*p, want) || p.Name != "External system rejected" {
		t.Fatalf("spec=%+v ok=%v, want %+v", p, ok, want)
	}
}

func TestLogsReads_PatternParam(t *testing.T) {
	const win = "&from=1759489200000000000&to=1759492800000000000"
	want, _ := anomaly.LogPatternSpecByName("External system rejected")
	reads := []struct {
		name string
		call func(s *Server, q string) *httptest.ResponseRecorder
	}{
		{"GET /api/logs", func(s *Server, q string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.getLogs(w, httptest.NewRequest("GET", "/api/logs?"+q, nil))
			return w
		}},
		{"POST /api/logs/search", func(s *Server, q string) *httptest.ResponseRecorder {
			// Gövde GET'in param adlarını taşır (logsBodyToValues).
			body := `{"pattern":"` + strings.ReplaceAll(strings.TrimPrefix(strings.SplitN(q, "&", 2)[0], "pattern="), "+", " ") + `","search":"orders-svc"}`
			w := httptest.NewRecorder()
			s.postLogsSearch(w, httptest.NewRequest("POST", "/api/logs/search", strings.NewReader(body)))
			return w
		}},
		{"GET /api/logs/timeseries", func(s *Server, q string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.getLogsTimeseries(w, httptest.NewRequest("GET", "/api/logs/timeseries?groupBy=severity&"+q, nil))
			return w
		}},
		{"GET /api/logs/fieldstats", func(s *Server, q string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.getLogsFieldStats(w, httptest.NewRequest("GET", "/api/logs/fieldstats?field=service&"+q, nil))
			return w
		}},
	}
	for _, r := range reads {
		t.Run(r.name+" — bilinen desen → dedektörün spec'i, serbest metin korunur", func(t *testing.T) {
			st := &patternFilterLogStore{}
			w := r.call(patternFilterServer(st), "pattern=External+system+rejected&search=orders-svc"+win)
			if w.Code != 200 || st.calls != 1 {
				t.Fatalf("code=%d calls=%d body=%s", w.Code, st.calls, w.Body.String())
			}
			if st.got.Pattern == nil || !reflect.DeepEqual(*st.got.Pattern, want) {
				t.Fatalf("Filter.Pattern=%+v, want %+v", st.got.Pattern, want)
			}
			if st.got.Search != "orders-svc" {
				t.Fatalf("serbest metin desenle AND'lenmeli, düşmemeli: %q", st.got.Search)
			}
		})
		t.Run(r.name+" — bilinmeyen desen → 400, arka uca gidilmez", func(t *testing.T) {
			st := &patternFilterLogStore{}
			w := r.call(patternFilterServer(st), "pattern=Nope"+win)
			if w.Code != 400 || st.calls != 0 || !strings.Contains(w.Body.String(), "bilinmeyen log deseni") {
				t.Fatalf("code=%d calls=%d body=%s", w.Code, st.calls, w.Body.String())
			}
		})
	}
	t.Run("desensiz istek — Filter.Pattern nil", func(t *testing.T) {
		st := &patternFilterLogStore{}
		w := httptest.NewRecorder()
		patternFilterServer(st).getLogs(w, httptest.NewRequest("GET", "/api/logs?search=x"+win, nil))
		if w.Code != 200 || st.got.Pattern != nil {
			t.Fatalf("code=%d pattern=%v", w.Code, st.got.Pattern)
		}
	})
}

// Her /logs önbellek anahtarı deseni taşır: desenli cevap desensiz (ya da
// başka desenli) isteğe TTL içinde servis edilmez (v0.5.187 sınıfı).
func TestLogsCacheKeys_IncludePattern(t *testing.T) {
	ext, _ := anomaly.LogPatternSpecByName("External system rejected")
	disk, _ := anomaly.LogPatternSpecByName("Disk full")
	variants := []logstore.Filter{
		{Search: "x"},
		{Search: "x", Pattern: &ext},
		{Search: "x", Pattern: &disk},
	}
	keys := map[string][]string{}
	for _, f := range variants {
		keys["search"] = append(keys["search"], logsSearchKey(f, "1", "2"))
		keys["timeseries"] = append(keys["timeseries"], logsTimeseriesKey(f, "1", "2", 60, "severity"))
		keys["fieldstats"] = append(keys["fieldstats"], logsFieldStatsKey("service", f, "1", "2", 5))
		keys["tail"] = append(keys["tail"], tailFilterKey(f))
		keys["patterns"] = append(keys["patterns"], logsPatternsKey(f, "1", "2", 50, 500, false))
	}
	for name, ks := range keys {
		seen := map[string]bool{}
		for _, k := range ks {
			if seen[k] {
				t.Fatalf("%s anahtarı desenden bağımsız: %v", name, ks)
			}
			seen[k] = true
		}
	}
	// Kararlı: aynı girdi aynı anahtar.
	if logsSearchKey(variants[1], "1", "2") != logsSearchKey(logstore.Filter{Search: "x", Pattern: &ext}, "1", "2") {
		t.Fatal("anahtar kararlı değil")
	}
}
