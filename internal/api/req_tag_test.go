package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
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
