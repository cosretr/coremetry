package api

// exception_oracle_test.go — v0.10.1092 (operatör: "Oracle hataları
// Exceptions gibi görünsün"; P1 kuralı koordinatör kararı: "az ama gerçek
// P1"). Pinler: Oracle grubu span merdiveninin yapışkan hacim P1'inden ve
// regressed "açıldıktan sonra ≥N" P1'inden MUAF; P1 yalnız son 1 sa patlaması
// (≥ eşik ve ≥ 3× önceki saat) ya da yeni grup + son 1 sa ≥ eşik; P2 son 1 sa
// ≥ 100 ve taze/regressed; gerisi P3 "sürekli akış (Oracle)". Sarmalayıcı
// enjekte edilen istatistiği ve gecikmeyi kullanır; span grubu aynen.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func TestOraclePriorityAt(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	cfg := chstore.DefaultExceptionTriage() // oracleP1MinOccurrences 5000, P1 penceresi 4 sa
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
		{"patlama: 9000 vs 2000 (≥3×, ≥eşik)", mk("new", 900000, old, 0), 9000, 2000, "P1", "Oracle patlaması"},
		{"durgun büyük akış: 9000 vs 8000 → P2 (ömür toplamı 900K yapışkan P1 YAPMAZ)", mk("new", 900000, old, 0), 9000, 8000, "P2", "son 1 sa"},
		{"önceki saat yoksa (0) ve ≥eşik → patlama", mk("new", 6000, old, 0), 6000, 0, "P1", "Oracle patlaması"},
		{"yeni grup + son 1 sa ≥ eşik (önceki saat de büyük)", mk("new", 12000, 90*time.Minute, 0), 6000, 6000, "P1", "yeni Oracle grubu"},
		{"yeni grup ama son 1 sa < eşik → P2", mk("new", 1200, 30*time.Minute, 0), 1200, 0, "P2", "son 1 sa"},
		{"regressed, ömür 900K, yeniden açılıştan beri çok → P1 DEĞİL (muaf)", func() chstore.ExceptionGroup {
			g := mk("regressed", 900000, old, 0)
			g.OccurrencesAtResolve = 100
			return g
		}(), 300, 280, "P2", "regressed"},
		{"bayat (son görülme > 4 sa) + az → P3", mk("new", 50000, old, 6*time.Hour), 0, 0, "P3", "sürekli akış (Oracle)"},
		{"taze ama son 1 sa < 100 → P3", mk("acknowledged", 40000, old, 0), 60, 70, "P3", "sürekli akış (Oracle)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := oraclePriorityAt(c.g, cfg, now, c.last, c.prev)
			if got != c.want || !strings.Contains(reason, c.why) {
				t.Fatalf("öncelik %s (%q), beklenen %s (%q)", got, reason, c.want, c.why)
			}
		})
	}
	// Vida: eşik 1000'e inerse 2000 vs 500 patlama P1.
	low := cfg
	low.OracleP1MinOccurrences = 1000
	if got, _ := oraclePriorityAt(mk("new", 90000, old, 0), low, now, 2000, 500); got != "P1" {
		t.Fatalf("vida: %s", got)
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
	if p, reason := exceptionPriority(ora); p != "P2" || !strings.Contains(reason, "son 1 sa 400") {
		t.Fatalf("Oracle sarmalayıcı: %s %q", p, reason)
	}
	// İstatistik yok → saatlik 0 → P3 (span merdivenine düşmez: 900K yapışkan P1 yok).
	oracleStatsFn.Store(nil)
	if p, reason := exceptionPriority(ora); p != "P3" || !strings.Contains(reason, "sürekli akış (Oracle)") {
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
