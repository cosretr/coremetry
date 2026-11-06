package api

// logs_trace_preview_test.go — v0.10.1883 (Log Search Faz 3 dilim 3.4). Kimlik
// normalizasyonu, pencere (∓15 dk, 5 dk kova, ≤6 sa; dışındaki kimlik yanıtta yok), beş durum (hata ASLA
// "örneklenmiş" değil), önbellek anahtarı (sıra-bağımsız, içerik duyarlı —
// v0.5.187), tavan, uç (CH yokken 200 + error durumu, tanınmayan kimlik).

import (
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestNormalizeTraceID(t *testing.T) {
	cases := map[string][2]string{
		"9C1D07AA5B3E4F0A8D217C66F201E41F":     {"9c1d07aa5b3e4f0a8d217c66f201e41f", "ok"},
		" 0x9c1d07aa5b3e4f0a8d217c66f201e41f ": {"9c1d07aa5b3e4f0a8d217c66f201e41f", "ok"},
		"9c1d07aa-5b3e-4f0a-8d21-7c66f201e41f": {"9c1d07aa5b3e4f0a8d217c66f201e41f", "ok"},
		"5f1c22e09ab47d10":                     {"00000000000000005f1c22e09ab47d10", "ok"},
		"ORD-88213":                            {"", ""},
		"9c1d07aa5b3e4f0a8d217c66f201e41":      {"", ""},
		"zz1d07aa5b3e4f0a8d217c66f201e41f":     {"", ""},
	}
	for raw, want := range cases {
		got, ok := normalizeTraceID(raw)
		if got != want[0] || ok != (want[1] == "ok") {
			t.Errorf("%q → (%q,%v), want %v", raw, got, ok, want)
		}
	}
}

func TestTracePreviewWindow(t *testing.T) {
	base := time.Date(2026, 11, 6, 14, 35, 12, 0, time.UTC)
	lo, hi := tracePreviewWindow(base.UnixNano(), base.Add(3*time.Minute).UnixNano())
	if !lo.Equal(time.Date(2026, 11, 6, 14, 20, 0, 0, time.UTC)) || !hi.Equal(time.Date(2026, 11, 6, 14, 55, 0, 0, time.UTC)) {
		t.Fatalf("pencere: %v … %v", lo, hi)
	}
	lo, hi = tracePreviewWindow(base.Add(-30*24*time.Hour).UnixNano(), base.UnixNano())
	if hi.Sub(lo) != 6*time.Hour {
		t.Fatalf("6 sa tavanı: %v", hi.Sub(lo))
	}
}

// Geniş aralığa saçılmış örnek: pencere en yeni logdan 6 saat geriye kırpılır;
// dışında kalan kimlik SORGULANMAZ ve yanıtta YOK — "örneklenmiş" denmez
// (bakılmadı), ClickHouse günlerce MV taramaz.
func TestTracePreviewWindowCapOmitsOldIDs(t *testing.T) {
	now := time.Date(2026, 11, 6, 14, 40, 0, 0, time.UTC)
	fresh, old := "9c1d07aa5b3e4f0a8d217c66f201e41f", "77e0b1c2aaaabbbbccccddddeeeeffff"
	p := planTracePreview([]tracePreviewItem{
		{ID: fresh, TsNs: now.Add(-10 * time.Minute).UnixNano()},
		{ID: old, TsNs: now.Add(-3 * 24 * time.Hour).UnixNano()},
		{ID: "ORD-88213", TsNs: now.Add(-3 * 24 * time.Hour).UnixNano()},
	})
	lo, hi := tracePreviewWindow(p.minNs, p.maxNs)
	if hi.Sub(lo) != tracePreviewMaxWindow {
		t.Fatalf("pencere kırpılmalı: %v", hi.Sub(lo))
	}
	if got := p.coveredIDs(lo); !reflect.DeepEqual(got, []string{fresh}) {
		t.Fatalf("sorgulanan kimlikler: %v", got)
	}
	out := buildTracePreviews(p, lo, map[string]chstore.TraceAggregateStub{}, nil, now)
	if _, ok := out[old]; ok {
		t.Fatalf("pencere dışı kimlik yanıtta olmamalı: %+v", out[old])
	}
	if out[fresh].State != tpSampled || out["ORD-88213"].State != tpUnrecognized {
		t.Fatalf("durumlar: %+v", out)
	}
	// Hata durumunda da pencere dışı kimlik için iddia yok.
	if _, ok := buildTracePreviews(p, lo, nil, errors.New("timeout"), now)[old]; ok {
		t.Fatal("hata durumunda pencere dışı kimlik yanıtta olmamalı")
	}
}

func TestTracePreviewStatesAndKey(t *testing.T) {
	now := time.Date(2026, 11, 6, 14, 40, 0, 0, time.UTC)
	items := []tracePreviewItem{
		{ID: "9c1d07aa5b3e4f0a8d217c66f201e41f", TsNs: now.Add(-5 * time.Minute).UnixNano()},
		{ID: "77E0B1C2AAAABBBBCCCCDDDDEEEEFFFF", TsNs: now.Add(-10 * time.Minute).UnixNano()},
		{ID: "4bf92f3577b34da6a3ce929d0e0e4736", TsNs: now.Add(-30 * time.Second).UnixNano()},
		{ID: "ORD-88213", TsNs: now.UnixNano()},
		{ID: "9c1d07aa5b3e4f0a8d217c66f201e41f", TsNs: now.Add(-6 * time.Minute).UnixNano()}, // tekrar
	}
	p := planTracePreview(items)
	if len(p.ids) != 3 || !reflect.DeepEqual(p.ids, []string{"4bf92f3577b34da6a3ce929d0e0e4736", "77e0b1c2aaaabbbbccccddddeeeeffff", "9c1d07aa5b3e4f0a8d217c66f201e41f"}) {
		t.Fatalf("plan: %v", p.ids)
	}
	found := map[string]chstore.TraceAggregateStub{
		"9c1d07aa5b3e4f0a8d217c66f201e41f": {RootService: "orders-api", RootName: "POST /api/orders", SpanCount: 23, ErrorCount: 2, DurationMs: 1840},
	}
	lo0, _ := tracePreviewWindow(p.minNs, p.maxNs)
	got := buildTracePreviews(p, lo0, found, nil, now)
	want := map[string]string{
		"9c1d07aa5b3e4f0a8d217c66f201e41f": tpFound,
		"77E0B1C2AAAABBBBCCCCDDDDEEEEFFFF": tpSampled, // istemci yazımıyla anahtarlı
		"4bf92f3577b34da6a3ce929d0e0e4736": tpPending,
		"ORD-88213":                        tpUnrecognized,
	}
	for id, st := range want {
		if got[id].State != st {
			t.Errorf("%s: %q, want %q", id, got[id].State, st)
		}
	}
	if got["9c1d07aa5b3e4f0a8d217c66f201e41f"].ErrorCount != 2 || got["9c1d07aa5b3e4f0a8d217c66f201e41f"].RootService != "orders-api" {
		t.Fatalf("bulunan özet: %+v", got["9c1d07aa5b3e4f0a8d217c66f201e41f"])
	}
	// Sorgu hatası: tanınan kimlikler error, ASLA sampled; tanınmayan aynen.
	errd := buildTracePreviews(p, lo0, nil, errors.New("timeout"), now)
	for raw, v := range errd {
		if raw == "ORD-88213" {
			if v.State != tpUnrecognized {
				t.Errorf("tanınmayan: %q", v.State)
			}
			continue
		}
		if v.State != tpError {
			t.Errorf("%s: hata durumunda %q", raw, v.State)
		}
	}
	// Anahtar: sıra-bağımsız, içerik duyarlı.
	lo, hi := tracePreviewWindow(p.minNs, p.maxNs)
	rev := append([]tracePreviewItem(nil), items...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	if tracePreviewKey(planTracePreview(rev).raws, lo, hi) != tracePreviewKey(p.raws, lo, hi) {
		t.Fatal("anahtar sıra-bağımsız olmalı")
	}
	other := planTracePreview(items[:2])
	if tracePreviewKey(other.raws, lo, hi) == tracePreviewKey(p.raws, lo, hi) {
		t.Fatal("anahtar içerik duyarlı olmalı")
	}
}

func TestTracePreviewCap(t *testing.T) {
	var items []tracePreviewItem
	for i := 0; i < chstore.TracePreviewMaxIDs+50; i++ {
		id := strings.Repeat("0", 28) + strings.ToLower(strings.TrimLeft(strings.ToUpper(hex4(i)), ""))
		items = append(items, tracePreviewItem{ID: id, TsNs: 1})
	}
	if p := planTracePreview(items); len(p.ids) != chstore.TracePreviewMaxIDs {
		t.Fatalf("tavan: %d", len(p.ids))
	}
}

func hex4(i int) string {
	const h = "0123456789abcdef"
	return string([]byte{h[i>>12&15], h[i>>8&15], h[i>>4&15], h[i&15]})
}

func TestPostLogsTracePreviewNoStore(t *testing.T) {
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	body := `{"items":[{"id":"9c1d07aa5b3e4f0a8d217c66f201e41f","tsNs":1759494800000000000},{"id":"not-a-trace","tsNs":1}]}`
	w := httptest.NewRecorder()
	s.postLogsTracePreview(w, httptest.NewRequest("POST", "/api/logs/trace-preview", strings.NewReader(body)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"9c1d07aa5b3e4f0a8d217c66f201e41f":{"state":"error"}`) ||
		!strings.Contains(w.Body.String(), `"not-a-trace":{"state":"unrecognized"}`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.postLogsTracePreview(w, httptest.NewRequest("POST", "/api/logs/trace-preview", strings.NewReader(`{bozuk`)))
	if w.Code != 400 {
		t.Fatalf("bozuk JSON 400: %d", w.Code)
	}
}
