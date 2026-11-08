package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type unwrapWriter struct{ http.ResponseWriter }

func (u unwrapWriter) Unwrap() http.ResponseWriter { return u.ResponseWriter }

// v0.10.918 — "[api] error" satırı ucu taşır; sorgu dizesi taşımaz; Flusher korunur.
func TestReqTag(t *testing.T) {
	var got string
	var flusher bool
	h := withReqTag(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = reqTagSuffix(unwrapWriter{w})
		_, flusher = w.(http.Flusher)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/spans/heatmap?q=secret", nil))
	if got != " [GET /api/spans/heatmap]" {
		t.Fatalf("etiket = %q", got)
	}
	if !flusher {
		t.Fatal("SSE için http.Flusher korunmalı")
	}
	if reqTagSuffix(httptest.NewRecorder()) != "" {
		t.Fatal("etiketsiz yazıcı boş dönmeli")
	}
	rec := httptest.NewRecorder()
	writeErr(&reqTagWriter{ResponseWriter: rec, tag: "GET /x"}, errors.New("boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("writeErr davranışı değişmemeli: %d", rec.Code)
	}
}

// v0.10.1971 — prod'da system.query_log yok: ClickHouse bellek / zaman aşımı hatasında isteğin
// sorgu dizesi (süzgeç + pencere) loga yazılır; diğer hatalarda yazılmaz.
func TestCHResourceQuerySuffix(t *testing.T) {
	tw := &reqTagWriter{ResponseWriter: httptest.NewRecorder(), tag: "GET /api/traces", rawQuery: "service=orders-api&range=6h"}
	mem := errors.New("code: 241, message: (total) memory limit exceeded: would use 9.32 GiB")
	if got := chResourceQuerySuffix(tw, mem); got != " q=service=orders-api&range=6h" {
		t.Fatalf("bellek hatası: %q", got)
	}
	if got := chResourceQuerySuffix(tw, errors.New("code: 159, message: Timeout exceeded")); got == "" {
		t.Fatal("zaman aşımı da yazılmalı")
	}
	if got := chResourceQuerySuffix(tw, errors.New("connection refused")); got != "" {
		t.Fatalf("başka hata sorgu dizesi yazmamalı: %q", got)
	}
	long := &reqTagWriter{ResponseWriter: httptest.NewRecorder(), rawQuery: strings.Repeat("a", maxLoggedQuery+10)}
	if got := chResourceQuerySuffix(long, mem); len(got) > maxLoggedQuery+10 {
		t.Fatalf("kırpılmalı: %d", len(got))
	}
	if chResourceQuerySuffix(httptest.NewRecorder(), mem) != "" {
		t.Fatal("etiketsiz yazıcı boş döner")
	}
}
