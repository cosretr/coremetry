package api

// logs_cancel_test.go — v0.10.1822 (Log Search Faz 1 dilim 1.6).
//
// FE artık her log isteğine iptal sinyali veriyor (yeni arama / sayfadan
// çıkış). Sunucu tarafı sözleşmesi: istemci bağlantıyı kesince (r.Context()
// iptal) bu iptal arka uç çağrısına ULAŞIR (ES transport'u isteğin bağlamıyla
// koşar → ES araması kesilir), yanıt 499'dur ve hiçbir önbellek katmanına
// "degraded/boş" cevap YAZILMAZ — bir sonraki gerçek istek arka uca yeniden
// gider. Önceki risk: iptal edilen ES çağrısı *net.OpError ile dönerse
// MapBackendSlow onu "yavaş" sayıp 200 {degraded} üretiyor ve serveCached
// bunu 15–60 sn saklıyordu.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// blockingLogStore — bağlam iptal edilene dek bekler; iptali GÖRÜRSE sayar
// ve iptal edilmiş bir ES dial'inin biçimiyle (*net.OpError) döner.
type blockingLogStore struct {
	logstore.Store
	started  chan struct{}
	saw      atomic.Int32
	calls    atomic.Int32
	blocking atomic.Bool
}

func (b *blockingLogStore) wait(ctx context.Context) error {
	b.calls.Add(1)
	if !b.blocking.Load() {
		return nil
	}
	b.started <- struct{}{}
	select {
	case <-ctx.Done():
		b.saw.Add(1)
		return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("operation was canceled")}
	case <-time.After(5 * time.Second):
		return errors.New("iptal arka uca ulaşmadı")
	}
}
func (b *blockingLogStore) Search(ctx context.Context, _ logstore.Filter) (*logstore.Page, error) {
	if err := b.wait(ctx); err != nil {
		return nil, err
	}
	return &logstore.Page{}, nil
}
func (b *blockingLogStore) Histogram(ctx context.Context, _ logstore.Filter, _ int, _ string) ([]logstore.LogSeries, error) {
	if err := b.wait(ctx); err != nil {
		return nil, err
	}
	return []logstore.LogSeries{}, nil
}
func (b *blockingLogStore) FieldStats(ctx context.Context, _ logstore.Filter, _ string, _ int) (*logstore.FieldStatsResult, error) {
	if err := b.wait(ctx); err != nil {
		return nil, err
	}
	return &logstore.FieldStatsResult{}, nil
}
func (*blockingLogStore) Backend() string { return "test" }

func TestLogsRequestCancellationReachesBackendAndIsNotCached(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		run  func(s *Server, w *httptest.ResponseRecorder, r *http.Request)
	}{
		{"liste", "/api/logs?service=orders-api&from=1&to=2", func(s *Server, w *httptest.ResponseRecorder, r *http.Request) { s.getLogs(w, r) }},
		{"histogram", "/api/logs/timeseries?service=orders-api&from=1&to=2", func(s *Server, w *httptest.ResponseRecorder, r *http.Request) { s.getLogsTimeseries(w, r) }},
		{"alan istatistiği", "/api/logs/fieldstats?field=level&from=1&to=2", func(s *Server, w *httptest.ResponseRecorder, r *http.Request) { s.getLogsFieldStats(w, r) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &blockingLogStore{started: make(chan struct{}, 1)}
			st.blocking.Store(true)
			s := &Server{logs: st, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}

			ctx, cancel := context.WithCancel(context.Background())
			req := httptest.NewRequest("GET", tc.path, nil).WithContext(ctx)
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { tc.run(s, w, req); close(done) }()
			<-st.started
			cancel() // tarayıcı isteği kesti (yeni arama / sayfadan çıkış)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("handler iptalden sonra dönmedi")
			}
			if st.saw.Load() != 1 {
				t.Fatal("iptal arka uç çağrısına ulaşmadı")
			}
			if w.Code != statusClientClosedRequest {
				t.Fatalf("status %d, 499 beklendi (degraded 200 DEĞİL) — %s", w.Code, w.Body.String())
			}

			// Aynı istek yeniden: önbellekte "degraded" yok → arka uca tekrar gider.
			st.blocking.Store(false)
			w2 := httptest.NewRecorder()
			tc.run(s, w2, httptest.NewRequest("GET", tc.path, nil))
			if w2.Code != 200 || st.calls.Load() != 2 {
				t.Fatalf("iptal sonrası istek arka uca gitmeli: code=%d calls=%d", w2.Code, st.calls.Load())
			}
		})
	}
}
