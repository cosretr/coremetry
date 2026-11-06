package logstore

// es_meter_test.go — v0.10.1878. ES istek sayacı: her istek sayılır, tür ve
// yüzey sınırlı kümelerdir, boşaltma yalnız tamamlanmış dakikaları verir.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestESOpKind(t *testing.T) {
	cases := map[string]string{
		"/app-*/_search":                "search",
		"/app-orders-api.acme/_search":  "search",
		"/_msearch":                     "msearch",
		"/app-*/_count":                 "count",
		"/app-*/_field_caps":            "field_caps",
		"/app-*/_mapping":               "mapping",
		"/app-*/_pit":                   "pit",
		"/_pit":                         "pit",
		"/app-*/_async_search":          "async_search",
		"/_sql":                         "sql",
		"/":                             "meta",
		"":                              "meta",
		"/_cat/indices/app-*":           "meta",
		"/_cluster/health":              "meta",
		"/app-orders-api.acme/_doc/abc": "other",
		"/APP-*/_SEARCH":                "search",
	}
	for path, want := range cases {
		if got := ESOpKind(path); got != want {
			t.Errorf("ESOpKind(%q) = %q, beklenen %q", path, got, want)
		}
	}
}

func TestESSurfaceLabel(t *testing.T) {
	if got := esSurfaceLabel(""); got != esSurfaceOther {
		t.Errorf("etiketsiz = %q", got)
	}
	if got := esSurfaceLabel("  route:GET /api/logs  "); got != "route:GET /api/logs" {
		t.Errorf("kırpma = %q", got)
	}
	long := "route:" + string(make([]byte, 300))
	if got := esSurfaceLabel(long); len(got) != esSurfaceMaxLen {
		t.Errorf("uzunluk tavanı = %d", len(got))
	}
}

func TestESMeterRecordAndDrain(t *testing.T) {
	m := &esMeter{cells: map[esMeterKey]*ESRequestStat{}, surfaces: map[int64]map[string]struct{}{}}
	t0 := time.Date(2026, 11, 6, 12, 0, 10, 0, time.UTC)
	m.record(t0, "search", "route:GET /api/logs", 120*time.Millisecond, false)
	m.record(t0.Add(5*time.Second), "search", "route:GET /api/logs", 80*time.Millisecond, true)
	m.record(t0.Add(6*time.Second), "search", "worker:templater", 10*time.Millisecond, false)
	m.record(t0.Add(70*time.Second), "search", "route:GET /api/logs", 10*time.Millisecond, false) // sonraki dakika

	// İçinde bulunulan dakika (12:01) boşaltılmaz.
	got := m.drain(t0.Add(75 * time.Second))
	if len(got) != 2 {
		t.Fatalf("tamamlanmış dakika hücresi = %d, beklenen 2: %+v", len(got), got)
	}
	a := got[0]
	if a.Surface != "route:GET /api/logs" || a.Count != 2 || a.Errors != 1 || a.DurMs != 200 || a.Op != "search" {
		t.Fatalf("hücre yanlış: %+v", a)
	}
	if got[1].Surface != "worker:templater" || got[1].Count != 1 {
		t.Fatalf("ikinci hücre yanlış: %+v", got[1])
	}
	// Aynı hücre iki kez verilmez.
	if again := m.drain(t0.Add(75 * time.Second)); len(again) != 0 {
		t.Fatalf("boşaltılan hücre yeniden döndü: %+v", again)
	}
	if rest := m.drain(t0.Add(3 * time.Minute)); len(rest) != 1 || rest[0].Count != 1 {
		t.Fatalf("sonraki dakika: %+v", rest)
	}
}

func TestESMeterBoundsSurfacesAndMinutes(t *testing.T) {
	m := &esMeter{cells: map[esMeterKey]*ESRequestStat{}, surfaces: map[int64]map[string]struct{}{}}
	t0 := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for i := 0; i < esMeterMaxSurfaces+40; i++ {
		m.record(t0, "search", fmt.Sprintf("route:GET /api/x%d", i), time.Millisecond, false)
	}
	got := m.drain(t0.Add(time.Minute))
	if len(got) != esMeterMaxSurfaces {
		t.Fatalf("yüzey sayısı %d, tavan %d", len(got), esMeterMaxSurfaces)
	}
	var total, other uint64
	for _, c := range got {
		total += c.Count
		if c.Surface == esSurfaceOther {
			other = c.Count
		}
	}
	if total != uint64(esMeterMaxSurfaces+40) {
		t.Fatalf("istek kaybı: toplam %d", total)
	}
	if other == 0 {
		t.Fatal("tavanı aşan yüzeyler \"diğer\"e katlanmalı")
	}
	// Boşaltılmayan sayaç dakika tavanını aşmaz.
	for i := 0; i < esMeterMaxMinutes+30; i++ {
		m.record(t0.Add(time.Duration(i)*time.Minute), "search", "worker:x", time.Millisecond, false)
	}
	if len(m.surfaces) > esMeterMaxMinutes {
		t.Fatalf("dakika sayısı %d, tavan %d", len(m.surfaces), esMeterMaxMinutes)
	}
}

type fakeRT struct {
	status int
	err    error
}

func (f fakeRT) RoundTrip(*http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: f.status, Body: http.NoBody}, nil
}

func TestESMeterTransportCountsEveryRequest(t *testing.T) {
	m := &esMeter{cells: map[esMeterKey]*ESRequestStat{}, surfaces: map[int64]map[string]struct{}{}}
	do := func(rt http.RoundTripper, path, tag string) {
		ctx := context.Background()
		if tag != "" {
			ctx = chstore.WithQueryTag(ctx, tag)
		}
		req := (&http.Request{Method: "POST", URL: &url.URL{Path: path}}).WithContext(ctx)
		_, _ = (&esMeterRT{base: rt, meter: m}).RoundTrip(req)
	}
	do(fakeRT{status: 200}, "/app-*/_search", "route:GET /api/logs")
	do(fakeRT{status: 503}, "/app-*/_search", "route:GET /api/logs")
	do(fakeRT{err: errors.New("dial tcp: refused")}, "/app-*/_search", "route:GET /api/logs")
	do(fakeRT{err: context.Canceled}, "/app-*/_search", "route:GET /api/logs") // iptal: sayılır, hata değil
	do(fakeRT{status: 200}, "/app-*/_field_caps", "")

	got := m.drain(time.Now().Add(2 * time.Minute))
	byKey := map[string]ESRequestStat{}
	for _, c := range got {
		byKey[c.Op+"|"+c.Surface] = c
	}
	s := byKey["search|route:GET /api/logs"]
	if s.Count != 4 || s.Errors != 2 {
		t.Fatalf("search hücresi: %+v", s)
	}
	if o := byKey["field_caps|"+esSurfaceOther]; o.Count != 1 {
		t.Fatalf("etiketsiz istek \"diğer\"de sayılmalı: %+v", got)
	}
}
