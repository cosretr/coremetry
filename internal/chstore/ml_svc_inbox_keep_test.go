package chstore

// v0.10.2130 — spec-ml-service-level dilim 4 (operatör kararı 2026-11-14
// "Yenileri canlıya al"): servis düzeyi ML Problem'i (`anomaly-auto:svc:`)
// kaynak P1'ini inbox'ta korur; kayıtlı 2056 varsayılanı tek seferlik v5
// göçüyle yeniye taşınır, idempotent. Belirti olmasaydı: eski svc-slowdown /
// error_rate P1'inin halefi inbox'ta P3 görünür, P3 süzen politika düşürürdü.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInboxKeepMLSvcPattern(t *testing.T) {
	if InboxKeepMLSvc != "anomaly-auto:svc:*" {
		t.Fatalf("kalıp servis önekine bağlı olmalı: %q", InboxKeepMLSvc)
	}
	def := DefaultInboxKeepSourcePriority()
	if err := ValidateInboxKeepSourcePriority(def); err != nil {
		t.Fatal(err)
	}
	if got, ok := MatchInboxKeepSourcePriority(def, "anomaly-auto:svc:orders-api"); !ok || got != InboxKeepMLSvc {
		t.Errorf("svc eşleşmeli: %q %v", got, ok)
	}
	for _, p := range []string{"P1", "P2"} {
		if got := NotifyViewPriority(Problem{RuleID: "anomaly-auto:svc:orders-api", Priority: p}, def); got != p {
			t.Errorf("bildirim önceliği %s → %s", p, got)
		}
	}
	if ProblemNotifyKind(Problem{RuleID: MLSvcRulePrefix + "orders-api"}) != NotifyKindAnomaly {
		t.Error("bildirim türü anomaly olmalı")
	}
}

func TestPlanInboxKeepMigrationV5(t *testing.T) {
	mk := func(v any) []byte {
		b, _ := json.Marshal(map[string]any{"bigBreachRatio": 3, "x-unknown": 1, inboxKeepField: v})
		return b
	}
	prev := inboxKeepDefault2056()
	rev := make([]string, len(prev))
	for i := range prev {
		rev[i] = prev[len(prev)-1-i]
	}
	for _, c := range []struct {
		name    string
		raw     []byte
		outcome string
		rewrite bool
	}{
		{"satır yok", nil, InboxKeepMigrateNoRow, false},
		{"alan yok → zaten yeni varsayılan", []byte(`{"bigBreachRatio":3}`), InboxKeepMigrateDefault, false},
		{"2056 varsayılanı (başka sırada) → yeni", mk(rev), InboxKeepMigrateReplaced, true},
		{"zaten yeni varsayılan", mk(DefaultInboxKeepSourcePriority()), InboxKeepMigrateCurrent, false},
		{"1091 varsayılanı → v5 dokunmaz (v4'ün işi)", mk(inboxKeepDefault1091()), InboxKeepMigrateCustomised, false},
		{"operatör özelleştirdi", mk(append(inboxKeepDefault2056(), "custom:payments*")), InboxKeepMigrateCustomised, false},
		{"operatör kapattı ([])", mk([]string{}), InboxKeepMigrateCustomised, false},
	} {
		newRaw, outcome, _, err := planInboxKeepMigrationFrom(c.raw, inboxKeepStepV5.from())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if outcome != c.outcome || (newRaw != nil) != c.rewrite {
			t.Errorf("%s: outcome=%s rewrite=%v, istenen %s/%v", c.name, outcome, newRaw != nil, c.outcome, c.rewrite)
			continue
		}
		if !c.rewrite {
			continue
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(newRaw, &got); err != nil {
			t.Fatal(err)
		}
		if string(got["bigBreachRatio"]) != "3" || string(got["x-unknown"]) != "1" {
			t.Errorf("%s: diğer alanlar korunmalı: %s", c.name, newRaw)
		}
		var cfg ProblemPriorityConfig
		_ = json.Unmarshal(newRaw, &cfg)
		if !reflect.DeepEqual(cfg.InboxKeepSourcePriorityList(), DefaultInboxKeepSourcePriority()) {
			t.Errorf("%s: yeni liste %v", c.name, cfg.InboxKeepSourcePriorityList())
		}
	}
}

func TestMigrateInboxKeepDefaultsV5(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	done := []byte(`{"outcome":"current"}`)
	blob2056, _ := json.Marshal(map[string]any{"bigBreachRatio": 2, inboxKeepField: inboxKeepDefault2056()})

	t.Run("2056 varsayılanı → yeni, ayrı işaret + audit; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: blob2056,
			inboxKeepMigrateKey: done, inboxKeepMigrateKeyV3: done, inboxKeepMigrateKeyV4: done}}
		out, err := MigrateInboxKeepDefaultsV5(ctx, f, now)
		if err != nil || out != InboxKeepMigrateReplaced {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		var cfg ProblemPriorityConfig
		_ = json.Unmarshal(f.kv[problemPriorityKey], &cfg)
		if !reflect.DeepEqual(cfg.InboxKeepSourcePriorityList(), DefaultInboxKeepSourcePriority()) {
			t.Errorf("liste: %v", cfg.InboxKeepSourcePriorityList())
		}
		var m inboxKeepMigrateMarker
		if err := json.Unmarshal(f.kv[inboxKeepMigrateKeyV5], &m); err != nil || m.Version != "v0.10.2130" || m.Outcome != InboxKeepMigrateReplaced {
			t.Errorf("v5 işareti: %s", f.kv[inboxKeepMigrateKeyV5])
		}
		if len(f.audits) != 1 || !strings.Contains(f.audits[0].Details, "ML servis düzeyi") {
			t.Errorf("tek audit: %+v", f.audits)
		}
		writes := len(f.putKeys)
		if out, err := MigrateInboxKeepDefaultsV5(ctx, f, now); err != nil || out != "" || len(f.putKeys) != writes {
			t.Errorf("işaret varken no-op: %q %v", out, err)
		}
	})

	t.Run("sıra: v2 → v5 — 1072 listesi tek turda güncele, sonrakiler 'current'", func(t *testing.T) {
		old, _ := json.Marshal(map[string]any{inboxKeepField: legacyInboxKeepSourcePriority()})
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: old}}
		if out, err := MigrateInboxKeepDefaults(ctx, f, now); err != nil || out != InboxKeepMigrateReplaced {
			t.Fatalf("v2: %s %v", out, err)
		}
		for i, step := range []func(context.Context, inboxKeepMigrateStore, time.Time) (string, error){
			MigrateInboxKeepDefaultsV3, MigrateInboxKeepDefaultsV4, MigrateInboxKeepDefaultsV5} {
			if out, err := step(ctx, f, now); err != nil || out != InboxKeepMigrateCurrent {
				t.Fatalf("adım %d: %s %v", i+3, out, err)
			}
		}
	})

	t.Run("özelleştirilmiş → dokunulmaz, işaret yazılır", func(t *testing.T) {
		custom, _ := json.Marshal(map[string]any{inboxKeepField: []string{"db-health:*"}})
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: custom}}
		if out, err := MigrateInboxKeepDefaultsV5(ctx, f, now); err != nil || out != InboxKeepMigrateCustomised {
			t.Fatalf("%s %v", out, err)
		}
		if string(f.kv[problemPriorityKey]) != string(custom) || len(f.kv[inboxKeepMigrateKeyV5]) == 0 || len(f.audits) != 0 {
			t.Errorf("blob değişmemeli, işaret yazılmalı, audit yok")
		}
	})
}
