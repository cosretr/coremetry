package api

// cosre_signal_rate_test.go — v0.10.2115 (audit §8 soru 6): POST /api/ai/signal
// kullanıcı başına dakikada 60; 61. istek 429 + Retry-After, başka kullanıcı ve
// yeni dakika etkilenmez; bozuk gövde kotayı yemez.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func TestCosreSignalLimiter(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 30, 0, time.UTC)
	l := newCosreSignalLimiter(func() time.Time { return now })
	for i := 0; i < cosreSignalsPerMin; i++ {
		if ok, _ := l.allow("u1"); !ok {
			t.Fatalf("%d. istek reddedildi", i+1)
		}
	}
	ok, wait := l.allow("u1")
	if ok || wait != 30*time.Second {
		t.Fatalf("61. istek: ok=%v wait=%s (istenen red, 30 sn)", ok, wait)
	}
	if ok, _ := l.allow("u2"); !ok {
		t.Fatal("başka kullanıcı etkilendi")
	}
	if ok, _ := l.allow(""); !ok {
		t.Fatal("kimliksiz bağlam sayılmamalı")
	}
	now = now.Add(30 * time.Second) // yeni dakika
	if ok, _ := l.allow("u1"); !ok {
		t.Fatal("yeni dakikada sayaç sıfırlanmadı")
	}
	now = time.Date(2026, 11, 13, 10, 1, 59, 900_000_000, time.UTC)
	for i := 0; i < cosreSignalsPerMin; i++ {
		l.allow("u3")
	}
	if _, wait := l.allow("u3"); wait != time.Second {
		t.Fatalf("pencere sonuna <1 sn: Retry-After tabanı 1 sn olmalı, %s", wait)
	}
}

func TestPostCosreSignalRateLimited(t *testing.T) {
	prev := cosreSignalRate
	cosreSignalRate = newCosreSignalLimiter(time.Now)
	t.Cleanup(func() { cosreSignalRate = prev })
	for i := 0; i < cosreSignalsPerMin; i++ {
		cosreSignalRate.allow("u1")
	}
	s := &Server{}
	post := func(body, uid string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/ai/signal", bytes.NewBufferString(body))
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: uid, Email: uid + "@example.com", Role: auth.RoleViewer}))
		w := httptest.NewRecorder()
		s.postCosreSignal(w, r)
		return w
	}
	// Bozuk gövde 400 — kota kontrolüne hiç gelmez (sınır dolu olsa da).
	if w := post(`{`, "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("bozuk gövde %d", w.Code)
	}
	w := post(`{"exchangeId":"ex1","kind":"copy"}`, "u1")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("taşma: %d Retry-After=%q", w.Code, w.Header().Get("Retry-After"))
	}
}
