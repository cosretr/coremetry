package chstore

// v0.10.2056 — ML anomali grubu Problem'i (`anomaly-auto:group:<ilk olay id>`,
// v0.10.2052) kaynak önceliğini (P2 critical; 4 sa sonra P1) inbox'ta ve
// bildirimde korur. Belirti: grup Problem'i inbox'ta P3 görünüyor ve P3
// bildiriliyordu (P3 süzen politika düşürür). Operatör onayı 2026-11-12.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInboxKeepMLGroupPattern(t *testing.T) {
	if pat := InboxKeepMLGroup; pat != "anomaly-auto:group:*" {
		t.Fatalf("kalıp grup önekine bağlı olmalı: %q", InboxKeepMLGroup)
	}
	def := DefaultInboxKeepSourcePriority()
	if err := ValidateInboxKeepSourcePriority(def); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, want string }{
		{"anomaly-auto:group:ev-1", InboxKeepMLGroup},
		// Grup dışı anomaly-auto bilinçli DIŞARIDA.
		{"anomaly-auto:subject:orders-api", ""},
		{"anomaly-auto:ev-1:orders-api", ""},
		{"anomaly-auto:7f3a", ""},
	} {
		got, ok := MatchInboxKeepSourcePriority(def, c.id)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("Match(%q) = (%q, %v), istenen %q", c.id, got, ok, c.want)
		}
	}
	for _, p := range []string{"P1", "P2"} {
		if got := NotifyViewPriority(Problem{RuleID: "anomaly-auto:group:ev-1", Priority: p}, def); got != p {
			t.Errorf("bildirim önceliği %s → %s", p, got)
		}
	}
}

func TestPlanInboxKeepMigrationV4(t *testing.T) {
	mk := func(v any) []byte {
		b, _ := json.Marshal(map[string]any{"bigBreachRatio": 3, "x-unknown": 1, inboxKeepField: v})
		return b
	}
	prev := inboxKeepDefault1091()
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
		{"1091 varsayılanı (başka sırada) → yeni", mk(rev), InboxKeepMigrateReplaced, true},
		{"zaten yeni varsayılan", mk(DefaultInboxKeepSourcePriority()), InboxKeepMigrateCurrent, false},
		{"1083 varsayılanı → v4 dokunmaz (v3'ün işi)", mk(inboxKeepDefault1083()), InboxKeepMigrateCustomised, false},
		{"operatör özelleştirdi (1091 + kendi kalıbı)", mk(append(inboxKeepDefault1091(), "custom:payments*")), InboxKeepMigrateCustomised, false},
		{"operatör kapattı ([])", mk([]string{}), InboxKeepMigrateCustomised, false},
	} {
		newRaw, outcome, _, err := planInboxKeepMigrationFrom(c.raw, inboxKeepStepV4.from())
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

func TestMigrateInboxKeepDefaultsV4(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	done := []byte(`{"outcome":"current"}`)
	blob1091, _ := json.Marshal(map[string]any{"bigBreachRatio": 2, inboxKeepField: inboxKeepDefault1091()})

	t.Run("1091 varsayılanı → yeni, ayrı işaret + audit; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: blob1091,
			inboxKeepMigrateKey: done, inboxKeepMigrateKeyV3: done}}
		out, err := MigrateInboxKeepDefaultsV4(ctx, f, now)
		if err != nil || out != InboxKeepMigrateReplaced {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		var cfg ProblemPriorityConfig
		_ = json.Unmarshal(f.kv[problemPriorityKey], &cfg)
		if !reflect.DeepEqual(cfg.InboxKeepSourcePriorityList(), DefaultInboxKeepSourcePriority()) {
			t.Errorf("liste: %v", cfg.InboxKeepSourcePriorityList())
		}
		var m inboxKeepMigrateMarker
		if err := json.Unmarshal(f.kv[inboxKeepMigrateKeyV4], &m); err != nil || m.Version != "v0.10.2056" || m.Outcome != InboxKeepMigrateReplaced {
			t.Errorf("v4 işareti: %s", f.kv[inboxKeepMigrateKeyV4])
		}
		if len(f.audits) != 1 || !strings.Contains(f.audits[0].Details, "ML anomali grubu") ||
			f.audits[0].Action != "settings.problem_priority.migrate" {
			t.Errorf("tek audit: %+v", f.audits)
		}
		writes := len(f.putKeys)
		if out, err := MigrateInboxKeepDefaultsV4(ctx, f, now); err != nil || out != "" || len(f.putKeys) != writes {
			t.Errorf("işaret varken no-op: %q %v", out, err)
		}
	})

	t.Run("sıra: v2 → v3 → v4 — 1072 listesi tek turda güncele, v3/v4 'current'", func(t *testing.T) {
		old, _ := json.Marshal(map[string]any{inboxKeepField: legacyInboxKeepSourcePriority()})
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: old}}
		if out, err := MigrateInboxKeepDefaults(ctx, f, now); err != nil || out != InboxKeepMigrateReplaced {
			t.Fatalf("v2: %s %v", out, err)
		}
		if out, err := MigrateInboxKeepDefaultsV3(ctx, f, now); err != nil || out != InboxKeepMigrateCurrent {
			t.Fatalf("v3: %s %v", out, err)
		}
		if out, err := MigrateInboxKeepDefaultsV4(ctx, f, now); err != nil || out != InboxKeepMigrateCurrent {
			t.Fatalf("v4: %s %v", out, err)
		}
	})

	t.Run("özelleştirilmiş → dokunulmaz, işaret yazılır", func(t *testing.T) {
		custom, _ := json.Marshal(map[string]any{inboxKeepField: []string{"db-health:*", "svc-slowdown:*"}})
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: custom}}
		if out, err := MigrateInboxKeepDefaultsV4(ctx, f, now); err != nil || out != InboxKeepMigrateCustomised {
			t.Fatalf("%s %v", out, err)
		}
		if string(f.kv[problemPriorityKey]) != string(custom) || len(f.kv[inboxKeepMigrateKeyV4]) == 0 || len(f.audits) != 0 {
			t.Errorf("blob değişmemeli, işaret yazılmalı, audit yok")
		}
	})

	t.Run("açıkça boş liste → boş kalır", func(t *testing.T) {
		empty := []byte(`{"inboxKeepSourcePriority":[]}`)
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: empty}}
		if out, err := MigrateInboxKeepDefaultsV4(ctx, f, now); err != nil || out != InboxKeepMigrateCustomised {
			t.Fatalf("%s %v", out, err)
		}
		if string(f.kv[problemPriorityKey]) != string(empty) || len(f.audits) != 0 {
			t.Errorf("boş liste değişmemeli: %s", f.kv[problemPriorityKey])
		}
	})
}
