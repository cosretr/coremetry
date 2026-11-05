package api

// exception_oracle_test.go — v0.10.1092 (operatör: "Oracle hataları
// Exceptions gibi görünsün"; P1 kuralı koordinatör kararı: "az ama gerçek
// P1"). Pinler: Oracle grubu span merdiveninin yapışkan hacim P1'inden ve
// regressed "açıldıktan sonra ≥N" P1'inden MUAF; P1 yalnız son 1 sa patlaması
// (≥ eşik ve ≥ 3× önceki saat) ya da yeni grup + son 1 sa ≥ eşik; P2 son 1 sa
// ≥ 100 ve taze/regressed; gerisi P3 "sürekli akış (<etiket>)" (v0.10.1108:
// etiket branding oracleGroupLabel, varsayılan "Teknik hata"). Sarmalayıcı
// enjekte edilen istatistiği ve gecikmeyi kullanır; span grubu aynen.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

// v0.10.1803 (operatör 2026-11-05: "Oracle P1 problemse Problems'a dönsün;
// ama 30'dan fazla hata olursa P1 olsun") — eski kural P1'i patlama (≥3×
// önceki saat) ya da yeni grup + son 1 sa ≥ 5000'e bağlıyordu: dakikada
// onlarca hata akan grup hiç P1 olmuyordu. Yeni kural: son 1 sa > eşik
// (varsayılan 30) tek başına P1; P2/P3 kolları aynen.
func TestOraclePriorityAt(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	cfg := chstore.DefaultExceptionTriage() // oracleP1MinOccurrences 30, P1 penceresi 4 sa
	ora := chstore.OracleGroupFingerprint("o-11111111", "APP_ERR_001", "OP_A")
	mk := func(state string, occ uint64, firstAgo, lastAgo time.Duration) chstore.ExceptionGroup {
		return chstore.ExceptionGroup{Fingerprint: ora, Type: "APP_ERR_001", Message: "OP_A", State: state,
			Occurrences: occ, FirstSeen: now.Add(-firstAgo).UnixNano(), LastSeen: now.Add(-lastAgo).UnixNano()}
	}
	old := 72 * time.Hour
	cases := []struct {
		name       string
		g          chstore.ExceptionGroup
		last, prev uint64
		want, why  string
	}{
		{"31 > 30 durgun akış → P1", mk("new", 900000, old, 0), 31, 31, "P1", "Teknik hata: son 1 sa 31 > eşik 30"},
		{"30 = eşik → P1 DEĞİL (taze, < 100 → P3)", mk("new", 900000, old, 0), 30, 30, "P3", "sürekli akış (Teknik hata)"},
		{"durgun büyük akış 9000 vs 8000 → P1 (patlama şartı yok)", mk("new", 900000, old, 0), 9000, 8000, "P1", "> eşik 30 (önceki 1 sa 8,000)"},
		{"patlama bilgisi gerekçeye eklenir", mk("new", 900000, old, 0), 9000, 2000, "P1", "Teknik hata patlaması"},
		{"yeni grup bilgisi gerekçeye eklenir", mk("new", 12000, 90*time.Minute, 0), 6000, 6000, "P1", "yeni Teknik hata grubu"},
		{"regressed ama son 1 sa > eşik → P1", func() chstore.ExceptionGroup {
			g := mk("regressed", 900000, old, 0)
			g.OccurrencesAtResolve = 100
			return g
		}(), 300, 280, "P1", "> eşik 30"},
		{"bayat (son görülme > 4 sa) + son 1 sa 0 → P3", mk("new", 50000, old, 6*time.Hour), 0, 0, "P3", "sürekli akış (Teknik hata)"},
		{"taze ama son 1 sa ≤ eşik → P3", mk("acknowledged", 40000, old, 0), 20, 70, "P3", "sürekli akış (Teknik hata)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := oraclePriorityAt(c.g, cfg, now, c.last, c.prev, chstore.DefaultOracleGroupLabel)
			if got != c.want || !strings.Contains(reason, c.why) {
				t.Fatalf("öncelik %s (%q), beklenen %s (%q)", got, reason, c.want, c.why)
			}
		})
	}
	// P2 kolu aynen: eşik ≥ 100 seçildiğinde 100 ≤ son 1 sa ≤ eşik + taze → P2.
	hi := cfg
	hi.OracleP1MinOccurrences = 7000
	if got, reason := oraclePriorityAt(mk("new", 900000, old, 0), hi, now, 6000, 5000, chstore.DefaultOracleGroupLabel); got != "P2" || !strings.Contains(reason, "son 1 sa") {
		t.Fatalf("P2 kolu: %s (%q)", got, reason)
	}
	if got, _ := oraclePriorityAt(mk("new", 900000, old, 0), hi, now, 7001, 7000, chstore.DefaultOracleGroupLabel); got != "P1" {
		t.Fatalf("7001 > 7000: %s", got)
	}
	// Eşik migrasyonu: kayıtlı 5000 (eski kod varsayılanı) ve 0 → 30.
	for _, thr := range []int{5000, 0} {
		m := cfg
		m.OracleP1MinOccurrences = thr
		if got, _ := oraclePriorityAt(mk("new", 900000, old, 0), m, now, 31, 31, chstore.DefaultOracleGroupLabel); got != "P1" {
			t.Errorf("eşik %d → 30 taşınmalı; 31 → %s", thr, got)
		}
	}
	// v0.10.1108 — gerekçedeki ad branding etiketinden; "Oracle" kelimesi yok.
	if _, reason := oraclePriorityAt(mk("new", 900000, old, 0), cfg, now, 9000, 2000, "DB hatası"); !strings.HasPrefix(reason, "DB hatası patlaması") || strings.Contains(reason, "Oracle") {
		t.Fatalf("özel etiket: %q", reason)
	}
}

// Sarmalayıcı: Oracle grubu enjekte edilen istatistikle ve gecikme kaydırılmış
// "şimdi" ile (akan grup "durdu" okunmaz); span grubu bayt bayt eski merdiven.
func TestExceptionPriorityWrapperOracle(t *testing.T) {
	t.Cleanup(func() { oracleStatsFn.Store(nil) })
	ora := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_001", "OP_A"), Type: "APP_ERR_001", Message: "OP_A",
		State: "new", Occurrences: 900000, FirstSeen: time.Now().Add(-72 * time.Hour).UnixNano(), LastSeen: time.Now().Add(-17 * time.Minute).UnixNano()}
	SetOracleGroupStats(func(g chstore.ExceptionGroup) (oracle.GroupStats, bool) {
		return oracle.GroupStats{Lag: 16 * time.Minute, LastHour: 400, PrevHour: 380, BlobOK: true}, true
	})
	// v0.10.1803 — 400 > varsayılan eşik 30 → P1 (eskiden P2).
	if p, reason := exceptionPriority(ora); p != "P1" || !strings.Contains(reason, "son 1 sa 400") {
		t.Fatalf("Oracle sarmalayıcı: %s %q", p, reason)
	}
	// İstatistik yok → saatlik 0 → P3 (span merdivenine düşmez: 900K yapışkan P1 yok).
	oracleStatsFn.Store(nil)
	if p, reason := exceptionPriority(ora); p != "P3" || !strings.Contains(reason, "sürekli akış (Teknik hata)") {
		t.Fatalf("istatistiksiz: %s %q", p, reason)
	}
	span := ora
	span.Fingerprint = "0a1b2c3d4e5f6a7b"
	if p, _ := exceptionPriority(span); p != "P1" {
		t.Fatalf("span grubu hacim P1 (eski merdiven): %s", p)
	}
}

func TestNormalizeOracleFacet(t *testing.T) {
	for in, want := range map[string]string{"": "", "only": "only", "exclude": "exclude", "ONLY": "", "x": ""} {
		if got := normalizeOracleFacet(in); got != want {
			t.Errorf("normalizeOracleFacet(%q)=%q, beklenen %q", in, got, want)
		}
	}
}

func TestOracleGroupInfoFrom(t *testing.T) {
	g := chstore.ExceptionGroup{Fingerprint: "ora:x", Type: "APP_ERR_001", Message: "OP_A"}
	st := oracle.GroupStats{
		Source: oracle.SourceConfig{ID: "o-1", Name: "app-err"}, Lag: 16 * time.Minute, LastHour: 7000, PrevHour: 1000, BlobOK: true,
		Breakdown: &oracle.ExGroupBreakdown{
			Channels: map[string]uint64{"MOB": 30, "WEB": 20, "ATM": 5},
			Services: map[string]uint64{"svc-a": 9, "svc-b": 5, "svc-c": 3, "svc-d": 1},
		},
	}
	info := oracleGroupInfoFrom(g, st, true, 3)
	if info.SourceName != "app-err" || info.Code != "APP_ERR_001" || info.Operation != "OP_A" || !info.Known ||
		info.LagSec != 960 || info.LastHour != 7000 || info.PrevHour != 1000 {
		t.Fatalf("kimlik: %+v", info)
	}
	if len(info.Channels) != 3 || info.Channels[0].Name != "MOB" || info.ServiceCount != 4 || len(info.Services) != 3 || info.Services[0].Name != "svc-a" {
		t.Fatalf("kırılım: %+v", info)
	}
	if all := oracleGroupInfoFrom(g, st, true, 0); len(all.Services) != 4 {
		t.Fatalf("detay tüm servisler: %+v", all.Services)
	}
	if empty := oracleGroupInfoFrom(g, oracle.GroupStats{}, false, 3); empty.Known || empty.Channels == nil || empty.Services == nil {
		t.Fatalf("bilinmeyen kaynak boş (nil olmayan) dilimler: %+v", empty)
	}
}

func TestExceptionToInboxOracleSource(t *testing.T) {
	g := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_001", "OP_A"), Type: "APP_ERR_001", Message: "OP_A",
		Service: "svc-a", State: chstore.ExStateNew, Occurrences: 10, FirstSeen: 1, LastSeen: 2}
	it := exceptionToInbox(g)
	if it.Kind != "exception" || it.Source != "Oracle" || it.ID != "exception:"+g.Fingerprint || it.Title != "APP_ERR_001" {
		t.Fatalf("inbox satırı: %+v", it)
	}
	span := exceptionToInbox(chstore.ExceptionGroup{Fingerprint: "0a1b", Type: "java.lang.IllegalStateException"})
	if span.Source != "Exception" {
		t.Fatalf("span grubu etiketi değişmemeli: %q", span.Source)
	}
}
