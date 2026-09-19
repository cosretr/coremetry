package chstore

// v0.10.409 — ai_calls iki-boot sözleşmesi. İnceleme bulgusu: edit
// betiğinin çift koşumu genişletilmiş INSERT bloğunu üç kez ekleyip eski
// kolon dalını ULAŞILMAZ yapmıştı — ilk boot'ta (kolonlar yokken) her
// AI çağrısı kaydı Distributed tarafından reddedilecekti. Her iki dal
// saf yardımcılarla pinlenir: SQL kolon sayısı == arg sayısı.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAICallsInsertBranches(t *testing.T) {
	c := AICall{ID: "x", PromptVersion: "v", ErrorClass: "timeout", TTFTMs: 12, StreamFallback: true, ShieldHits: 1, CachedTokens: 77}
	// v0.10.807 — dört kombinasyon: ext × cached (ayrı probe, ayrı bayrak).
	for _, ext := range []bool{false, true} {
		for _, cached := range []bool{false, true} {
			sql := aiCallsInsertSQL(ext, cached)
			inner := sql[strings.Index(sql, "(")+1 : strings.LastIndex(sql, ")")]
			n := len(strings.Split(inner, ","))
			args := aiCallsInsertArgs(c, time.Unix(0, 0).UTC(), ext, cached)
			if n != len(args) {
				t.Fatalf("ext=%v cached=%v: SQL %d kolon, %d arg", ext, cached, n, len(args))
			}
			for _, col := range strings.Split(aiCallsExtCols, ", ") {
				if strings.Contains(inner, col) != ext {
					t.Errorf("ext=%v: kolon %q beklenmedik durumda", ext, col)
				}
			}
			if strings.Contains(inner, "cached_tokens") != cached {
				t.Errorf("cached=%v: cached_tokens kolonu beklenmedik durumda", cached)
			}
			if cached && args[len(args)-1] != uint32(77) {
				t.Errorf("cached_tokens son arg olmalı, %v", args[len(args)-1])
			}
			if ext && !cached && args[len(args)-2] != uint8(1) {
				t.Errorf("stream_fallback UInt8(1) olmalı, %v", args[len(args)-2])
			}
		}
	}
	if aiCallsInsertSQL(false, false) == aiCallsInsertSQL(true, false) || aiCallsInsertSQL(false, false) == aiCallsInsertSQL(false, true) {
		t.Fatal("dallar aynı SQL'i üretiyor")
	}
	// Satır okuması da bayrağa uyar: SELECT kolon sayısı == Scan hedefi.
	for _, cached := range []bool{false, true} {
		sel := aiCallsRowSelect(cached)
		n := len(strings.Split(strings.TrimPrefix(sel, "SELECT "), ","))
		var row AICall
		if got := len(aiCallsRowScan(&row, cached)); got != n {
			t.Errorf("cached=%v: SELECT %d kolon, Scan %d hedef", cached, n, got)
		}
	}
}

// Probe yalnız INSERT hedefine bakar: yerel parçada olup Distributed'da
// henüz olmayan kolon "var" sayılırsa INSERT kırılır.
func TestAICallsProbeTargetsInsertTable(t *testing.T) {
	b, err := os.ReadFile("ai_calls.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "'ai_calls_local'") {
		t.Fatal("probe yerel parçaya bakmamalı — INSERT hedefi ai_calls")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'error_class'") {
		t.Fatal("probe `table = 'ai_calls'` yüklemini taşımalı")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'cached_tokens'") { // v0.10.807
		t.Fatal("cached_tokens probe'u da INSERT hedefine bakmalı")
	}
}

// v0.10.423 — E5 nokta okuması da iki-boot sözleşmesine uyar: genişletilmiş
// kolonlar yalnız bayrakla; SQL kolon sayısı == Scan hedefi sayısı.
func TestAICallEvalSelectBranches(t *testing.T) {
	base := aiCallEvalSelect(false)
	ext := aiCallEvalSelect(true)
	if strings.Contains(base, "prompt_version") || !strings.Contains(ext, "prompt_version, profile_id, error_class") {
		t.Fatalf("dallar yanlış: base=%q ext=%q", base, ext)
	}
	for _, q := range []string{base, ext} {
		if !strings.Contains(q, "WHERE exchange_id = ?") || !strings.Contains(q, "LIMIT 1") || !strings.Contains(q, "max_execution_time") {
			t.Fatalf("nokta okuma sınırları eksik: %q", q)
		}
	}
	inner := ext[len("SELECT "):strings.Index(ext, " FROM")]
	if n := len(strings.Split(inner, ",")); n != 11 {
		t.Fatalf("ext 11 kolon (8 + 3) olmalı, %d", n)
	}
}
