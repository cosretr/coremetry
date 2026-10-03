package notify

// exception_regressed_p1_test.go — v0.10.1078: regressed grup P1'e yükselince
// (v0.10.1072 hacim kapısı, occurrences − occurrences_at_resolve ≥ 500) yalnız-P1
// kanallar bir kez haber alır. Akış: regressed P2 → taban; P1 → `:p1:<epoch>` bir
// kez; sonraki tikler sessiz; restart (boş defter, dolu log) sessiz; yeni
// resolve → regress: taban 90 gün içinde YENİDEN GİTMEZ (eski davranış), `:p1`
// yeni damgayla yeniden açık.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestExceptionRegressedP1UpgradeFlow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	// notification_log ikizi: SendProblemAlert'in ok=1 satırı.
	log := map[string]bool{}
	logReads := 0
	newNotifier := func() *ExceptionNotifier {
		return &ExceptionNotifier{sent: map[string]int64{}, logged: func(_ context.Context, id string) (bool, error) {
			logReads++
			return log[id], nil
		}}
	}
	e := newNotifier()
	chP1 := chstore.ChannelMatchRules{MinPriority: "P1", Kinds: []string{chstore.NotifyKindException}}
	chP2 := chstore.ChannelMatchRules{MinPriority: "P2", Kinds: []string{chstore.NotifyKindException}}

	fp := "a1b2c3d4e5f60718"
	grp := func(resolvedAt time.Time, occ uint64) chstore.ExceptionGroup {
		r := resolvedAt.UnixNano()
		return chstore.ExceptionGroup{Fingerprint: fp, Type: "SyntheticTimeoutError",
			Service: "svc-synth", State: chstore.ExStateRegressed, Occurrences: occ,
			OccurrencesAtResolve: 10_000, ResolvedAt: &r, FirstSeen: now.Add(-30 * 24 * time.Hour).UnixNano(),
			LastSeen: now.UnixNano()}
	}
	// tick — routeGroups'un regressed dalı: kimlik + kanal teslimi (P1 kanal, P2 kanal).
	tick := func(g chstore.ExceptionGroup, prio string) (id string, toP1, toP2 bool) {
		id = e.claimRegressed(ctx, g, prio, now)
		if id == "" {
			return "", false, false
		}
		p := exceptionGroupProblem(g, chstore.ExStateRegressed, prio, "synthetic")
		p.ID = id
		p = withPriority(p)
		log[id] = true
		in := chstore.MatchInput{Priority: p.Priority, Kind: chstore.ProblemNotifyKind(p)}
		return id, chP1.MatchesProblem(in), chP2.MatchesProblem(in)
	}

	base := "exception-group:a1b2c3d4e5f60718:regressed"
	upAt := func(r time.Time) string { return base + ":p1:" + strconv.FormatInt(r.Unix(), 10) }
	r1 := now.Add(-2 * time.Hour)
	steps := []struct {
		name           string
		g              chstore.ExceptionGroup
		prio           string
		restart        bool
		wantID         string
		wantP1, wantP2 bool
	}{
		{"regressed P2 → taban; yalnız P2 kanal", grp(r1, 10_040), "P2", false, base, false, true},
		{"P2 sürüyor → sessiz", grp(r1, 10_300), "P2", false, "", false, false},
		{"500 yeni → P1 bir kez; iki kanal", grp(r1, 10_500), "P1", false, upAt(r1), true, true},
		{"P1 sürüyor → sessiz", grp(r1, 10_900), "P1", false, "", false, false},
		{"restart, log dolu, P1 → çift yok", grp(r1, 11_000), "P1", true, "", false, false},
		{"restart, log dolu, P2 → çift yok", grp(r1, 11_000), "P2", true, "", false, false},
	}
	for _, s := range steps {
		if s.restart {
			e = newNotifier()
		}
		id, p1, p2 := tick(s.g, s.prio)
		if id != s.wantID || p1 != s.wantP1 || p2 != s.wantP2 {
			t.Fatalf("%s: id=%q P1kanal=%v P2kanal=%v; want %q %v %v", s.name, id, p1, p2, s.wantID, s.wantP1, s.wantP2)
		}
	}

	// Tik fırtınası yok: 100 P1 tiki → gönderim yok; log en çok bir kez okunur
	// (restart sonrası `:p1` deftere ilk tikte iner).
	reads := logReads
	for i := 0; i < 100; i++ {
		if id, _, _ := tick(grp(r1, 12_000), "P1"); id != "" {
			t.Fatalf("tik %d yeniden gönderdi: %s", i, id)
		}
	}
	if logReads-reads > 1 {
		t.Errorf("100 tikte log %d kez okundu, want ≤1", logReads-reads)
	}

	// Yeni resolve → regress (90 gün içinde): taban GİTMEZ (kronik grup her
	// regresyonda bildirmez); 500'ü yeniden aşınca `:p1` yeni damgayla bir kez.
	r2 := now.Add(-20 * time.Minute)
	e = newNotifier() // defter 24 sa sonra boşalmış gibi: karar log'dan
	if id, _, _ := tick(grp(r2, 10_010), "P2"); id != "" {
		t.Fatalf("2. regresyonda taban yeniden gitti: %q", id)
	}
	if id, p1, p2 := tick(grp(r2, 10_600), "P1"); id != upAt(r2) || !p1 || !p2 {
		t.Fatalf("2. regresyon yükseltme: %q %v %v", id, p1, p2)
	}
	if id, _, _ := tick(grp(r2, 10_900), "P1"); id != "" {
		t.Fatalf("2. regresyonda yükseltme tekrarladı: %q", id)
	}

	// P1 doğan regresyon (log'da hiçbir şey yok): tek mesaj `:p1`, taban da
	// gönderilmiş sayılır — sonra P2'ye inse de, restart sonrası da taban gitmez.
	fp = "0f1e2d3c4b5a6978"
	base = "exception-group:0f1e2d3c4b5a6978:regressed"
	r3 := now.Add(-10 * time.Minute)
	e = newNotifier()
	if id, p1, p2 := tick(grp(r3, 10_700), "P1"); id != upAt(r3) || !p1 || !p2 {
		t.Fatalf("P1 doğan: %q %v %v", id, p1, p2)
	}
	if id, _, _ := tick(grp(r3, 10_700), "P2"); id != "" {
		t.Fatalf("P1 doğan regresyonda taban gitti: %q", id)
	}
	e = newNotifier()
	if id, _, _ := tick(grp(r3, 10_700), "P2"); id != "" {
		t.Fatalf("restart sonrası P1 doğan regresyonda taban gitti: %q", id)
	}

	// Damgasız (eski) satır: `…:p1`.
	if id := exceptionRegressionP1ID("abc", nil); id != "exception-group:abc:regressed:p1" || exceptionGroupFingerprint(id) != "abc" {
		t.Errorf("damgasız p1: %q", id)
	}
}
