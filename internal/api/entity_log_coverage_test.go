package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// v0.10.2263 (Entity S6 parça 4) — tembel log kapsama işareti: entity
// başına tek, sınırlı (1 saat, Limit 1) sorgu; önbellek süresince tekrar yok.

type covLogStore struct {
	logstore.Store
	calls int
	got   logstore.Filter
	page  *logstore.Page
	err   error
}

func (c *covLogStore) Backend() string { return "elasticsearch" }
func (c *covLogStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	c.calls++
	c.got = f
	return c.page, c.err
}

func TestEntityLogCoverageBounded(t *testing.T) {
	ls := &covLogStore{page: &logstore.Page{Logs: []*logstore.LogRecord{{ServiceName: "checkout-api"}}}}
	s := &Server{logs: ls}
	out := s.entityLogCoverage(context.Background(), "pod:cluster-a/shop/checkout-api-7d9f8b6c5d-x2x9z")
	if out.HasLogs == nil || !*out.HasLogs || ls.calls != 1 {
		t.Fatalf("kapsama: %+v calls=%d", out, ls.calls)
	}
	f := ls.got
	if f.Limit != 1 || f.To.Sub(f.From) != entityLogCoverageWindow || f.Pod != "checkout-api-7d9f8b6c5d-x2x9z" || f.Namespace != "shop" {
		t.Fatalf("süzgeç: %+v", f)
	}
	// Süzgeç uygulanamadı → bilinmiyor (yanlış "var" yok).
	ls.page = &logstore.Page{Logs: []*logstore.LogRecord{{}}, UnappliedFilters: []string{"namespace"}}
	if out := s.entityLogCoverage(context.Background(), "ns:cluster-a/shop"); out.HasLogs != nil {
		t.Fatalf("uygulanamayan süzgeç: %+v", out)
	}
	// Desteklenmeyen tip: sorgu yok.
	before := ls.calls
	if out := s.entityLogCoverage(context.Background(), "host:node-a-01"); out.HasLogs != nil || ls.calls != before {
		t.Fatalf("host: %+v calls=%d", out, ls.calls)
	}
	// Backend hatası: bilinmiyor + not.
	ls.err = errors.New("timeout")
	if out := s.entityLogCoverage(context.Background(), "svc:checkout-api"); out.HasLogs != nil || out.Note == "" {
		t.Fatalf("hata: %+v", out)
	}
}

func TestEntityLogCoverageCached(t *testing.T) {
	ls := &covLogStore{page: &logstore.Page{}}
	c, _ := cache.NewNoop()
	s := &Server{logs: ls, cache: c, l1: newL1Cache(16), stats: newCacheStats()}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		s.getEntityLogCoverage(rec, httptest.NewRequest("GET", "/api/entity/log-coverage?id=svc:checkout-api-cov-test", nil))
		if rec.Code != 200 {
			t.Fatalf("durum %d: %s", rec.Code, rec.Body.String())
		}
	}
	if ls.calls != 1 {
		t.Fatalf("önbellek süresince tek log sorgusu beklenir: %d", ls.calls)
	}
}
