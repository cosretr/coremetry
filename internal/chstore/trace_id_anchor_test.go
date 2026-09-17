package chstore

// trace_id_anchor_test.go — v0.10.753: ?traceId= penceresi trace zamanına
// çıpalanır; bulunamazsa pencere aynen + hits=0. Ulaşılabilirlik pini:
// GetTraces çıpayı kimlik-önce dalından ÖNCE koşar (aksi hâlde 1 saat
// varsayılanı geri gelir ve hiçbir test görmez).

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplyTraceIDAnchor(t *testing.T) {
	base := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	f := TraceFilter{TraceID: "0fcd70a94ba1f695ea079750e71a7c10", From: base.Add(-time.Hour), To: base}
	lo, hi := base.Add(-30*24*time.Hour), base.Add(-30*24*time.Hour+10*time.Minute)

	hit := applyTraceIDAnchor(&f, lo, hi, true)
	if !f.From.Equal(lo) || !f.To.Equal(hi) {
		t.Fatalf("pencere çıpalanmadı: %v → %v", f.From, f.To)
	}
	if !hit.TraceID || hit.MatchedKey != "trace_id" || hit.Hits != 1 || hit.WindowFromNs != lo.UnixNano() || hit.WindowToNs != hi.UnixNano() {
		t.Fatalf("hit: %+v", hit)
	}
	if len(hit.Keys) != 1 || hit.Keys[0] != "trace_id" {
		t.Fatalf("keys: %v", hit.Keys)
	}

	// Bulunamadı: pencere DOKUNULMAZ, hits 0, ama Keys yine dolu (yanıta gider).
	g := TraceFilter{TraceID: "x", From: base.Add(-time.Hour), To: base}
	miss := applyTraceIDAnchor(&g, time.Time{}, time.Time{}, false)
	if !g.From.Equal(base.Add(-time.Hour)) || !g.To.Equal(base) {
		t.Fatalf("bulunamayınca pencere değişti: %v → %v", g.From, g.To)
	}
	if miss.Hits != 0 || !miss.TraceID || len(miss.Keys) != 1 || miss.WindowFromNs != 0 {
		t.Fatalf("miss hit: %+v", miss)
	}
	if applyTraceIDAnchor(nil, lo, hi, true).Hits != 0 {
		t.Fatal("nil filtre panic'lemeli değil, hits 0 dönmeli")
	}
}

func TestTraceIDAnchorRunsBeforeIdentityFirst(t *testing.T) {
	src, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "s.anchorTraceID(ctx, &f)")
	j := strings.Index(s, "if identityFirstEligible(f) {")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("anchorTraceID kimlik-önce dalından önce çağrılmalı (anchor=%d identity=%d)", i, j)
	}
	if !strings.Contains(s[i:j], "*f.IdentityHit = hit") {
		t.Fatal("çıpa sonucu IdentityHit OUT paramına yazılmalı — yanıta gitmez")
	}
}
