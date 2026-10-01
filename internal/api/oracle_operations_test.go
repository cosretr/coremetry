package api

// v0.10.1002 — Oracle operasyon adı araması ucu: sorgu kelepçesi, önbellek
// anahtarı, onaylı servis kuralı ve kaynak yokken CH'ye gitmeme.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func TestOracleOpSearchQuery(t *testing.T) {
	long := strings.Repeat("A", 81)
	for raw, want := range map[string]string{
		"": "", "ab": "", "  ab ": "", "EFT": "EFT", "  digital_eft  ": "digital_eft", "öde": "öde", long: "",
	} {
		if got := oracleOpSearchQuery(raw); got != want {
			t.Errorf("q=%q → %q, istenen %q", raw, got, want)
		}
	}
}

func TestOracleOpSearchKey(t *testing.T) {
	if oracleOpSearchKey("EFT", 6) != oracleOpSearchKey("eft", 6) {
		t.Error("arama harf duyarsız → anahtar da öyle olmalı")
	}
	if oracleOpSearchKey("eft", 6) == oracleOpSearchKey("eft", 20) || oracleOpSearchKey("eft", 6) == oracleOpSearchKey("efx", 6) {
		t.Error("anahtar q ve limit'i taşımalı")
	}
}

func TestOracleOperationHits(t *testing.T) {
	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour).UnixNano()
	learned := map[string]oracle.LearnedMap{"o-1": {Entries: map[string]*oracle.LearnedEntry{
		"OP_OK":  {Service: "eft-svc", Hits: 9, Total: 10, LastConfirmed: fresh},
		"OP_FEW": {Service: "other-svc", Hits: 1, Total: 1, LastConfirmed: fresh}, // onaysız
	}}}
	hits := []chstore.OracleOpHit{
		{Operation: "OP_OK", Rows: 40, LastSeenMs: 5, LastTraceID: "abc", FunctionCodes: []string{"F1"}, SourceID: "o-1"},
		{Operation: "OP_FEW", Rows: 3, SourceID: "o-1"},
		{Operation: "OP_OTHER", Rows: 1, SourceID: "o-2"},
	}
	got := oracleOperationHits(hits, map[string]string{"o-1": "core-oracle"}, learned, now)
	if len(got) != 3 || got[0].Service != "eft-svc" || got[0].Source != "core-oracle" || got[0].LastTraceID != "abc" || got[0].FunctionCodes[0] != "F1" {
		t.Fatalf("onaylı isabet: %+v", got)
	}
	if got[1].Service != "" || got[1].FunctionCodes == nil {
		t.Errorf("onaysız eşleme servis vermemeli; kodlar boş dizi olmalı: %+v", got[1])
	}
	if got[2].Service != "" || got[2].Source != "" {
		t.Errorf("haritası olmayan kaynak: %+v", got[2])
	}
}

func TestOracleOperationsRoute(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	s.oracle = oracle.New()
	mux := http.NewServeMux()
	s.registerOracleOperationRoutes(mux)
	// Etkin kaynak yok → her rol 200 + enabled:false (CH'ye gidilmez; depo nil).
	for _, role := range []string{auth.RoleViewer, auth.RoleAdmin} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, asRole(httptest.NewRequest("GET", "/api/oracle/operations?q=DIGITAL", nil), role))
		if w.Code != http.StatusOK {
			t.Fatalf("rol %s → %d", role, w.Code)
		}
		var resp oracleOperationsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Enabled || resp.Operations == nil || resp.SpanAttrKey == "" {
			t.Errorf("rol %s cevap: %s", role, w.Body.String())
		}
	}
	src, err := os.ReadFile("oracle_operations.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		`registerRoutesExtra("oracle-operations"`,
		"s.serveCached(w, r, oracleOpSearchKey(q, limit), 60*time.Second",
		"!s.oracle.HasEnabledSources()",
	} {
		if !strings.Contains(string(src), w) {
			t.Errorf("oracle_operations.go %q taşımalı", w)
		}
	}
}
