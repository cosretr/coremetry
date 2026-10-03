package chstore

// v0.10.1083 — Oracle hata tablosu Problem'leri P1'e ulaşır. Operatör: "Oracle
// hataları da problemse hâlâ düşmüyor" → onay "Yap". Üç pin:
//   - computePriority: taban (5) eşikli critical 12/dk → P1, 7/dk → P2; eski
//     Threshold 0 satırı oran kuramaz → P2 (düzeltmenin gerekçesi)
//   - varsayılan inbox istisna listesi dış kaynak hata serisini + kümesini korur
//   - tek seferlik göç: kayıtlı ESKİ varsayılan → yeni; özelleştirilmiş → dokunulmaz

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestComputePriority_ExternalThresholdFloor(t *testing.T) {
	cfg := DefaultProblemPriority()
	start := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC).UnixNano()
	now := start + int64(time.Minute)
	base := Problem{RuleID: "anomaly:ext:oracle-errlog/OP_PAY/ORA-00060:ext:error_count", Severity: "critical",
		Comparator: ">", Status: "open", StartedAt: start, Kind: ProblemKindExternal}
	for _, c := range []struct {
		name             string
		value, threshold float64
		want             string
	}{
		{"12/dk vs taban 5 → 2.4× → P1", 12, 5, "P1"},
		{"10/dk vs taban 5 → tam 2× → P1", 10, 5, "P1"},
		{"7/dk vs taban 5 → 1.4× → P2", 7, 5, "P2"},
		{"eski satır: Threshold = medyan 0 → oran yok → P2 (v0.10.1083 öncesi)", 400, 0, "P2"},
	} {
		p := base
		p.Value, p.Threshold = c.value, c.threshold
		if got, why := computePriority(p, now, cfg); got != c.want {
			t.Errorf("%s: %s (%s)", c.name, got, why)
		}
	}
}

func TestDefaultInboxKeep_IncludesExternalErrorSeries(t *testing.T) {
	def := DefaultInboxKeepSourcePriority()
	if err := ValidateInboxKeepSourcePriority(def); err != nil {
		t.Fatalf("varsayılan liste kendi doğrulamasından geçmeli: %v", err)
	}
	if got := NormalizeInboxKeepSourcePriority(def); !reflect.DeepEqual(got, def) {
		t.Fatalf("varsayılan normalize'da değişmemeli: %v", got)
	}
	for _, c := range []struct{ id, want string }{
		{"anomaly:ext:oracle-errlog/OP_PAY/ORA-00060:ext:error_count", InboxKeepExtErrorCount},
		{"anomaly:ext:oracle-errlog:ext:error_count", InboxKeepExtErrorCount},
		{"anomaly-cluster:ext:oracle-errlog/OP_PAY", InboxKeepExtCluster},
		{"anomaly:ext-cap:ext:oracle-errlog:ext:error_count", InboxKeepExtCap},
		// Bilinçli DIŞARIDA: kaynak sağlığı / servis kümesi / başka dış metrik.
		{"anomaly:ext-down:ext:oracle-errlog", ""},
		{"anomaly-cluster:checkout", ""},
		{"anomaly:ext:oracle-errlog/OP_PAY:ext:fail_count", ""},
	} {
		got, ok := MatchInboxKeepSourcePriority(def, c.id)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("Match(%q) = (%q, %v), istenen %q", c.id, got, ok, c.want)
		}
	}
	legacy := legacyInboxKeepSourcePriority()
	if !reflect.DeepEqual(def[:len(legacy)], legacy) || len(def) != len(legacy)+3 ||
		def[len(def)-1] != InboxKeepExtCap {
		t.Errorf("yeni varsayılan = eski varsayılan + 3 dış kaynak kalıbı (seri, küme, tavan) olmalı: %v", def)
	}
}

func TestPlanInboxKeepMigration(t *testing.T) {
	oldList := []string{"incident:critical", "db-health:*", "builtin-*", "anomaly:*:error_rate"} // sıra önemsiz
	mk := func(v any) []byte {
		b, _ := json.Marshal(map[string]any{"bigBreachRatio": 3, "staleCriticalHours": 6, "x-unknown": 1, inboxKeepField: v})
		return b
	}
	cases := []struct {
		name    string
		raw     []byte
		outcome string
		rewrite bool
	}{
		{"satır yok", nil, InboxKeepMigrateNoRow, false},
		{"alan yok (nil = zaten yeni varsayılan)", []byte(`{"bigBreachRatio":3,"staleCriticalHours":6}`), InboxKeepMigrateDefault, false},
		{"alan null", []byte(`{"bigBreachRatio":3,"inboxKeepSourcePriority":null}`), InboxKeepMigrateDefault, false},
		{"ESKİ varsayılan (başka sırada) → yeni", mk(oldList), InboxKeepMigrateReplaced, true},
		{"eski varsayılan + boşluk/tekrar (normalize sonrası aynı) → yeni", mk([]string{" builtin-* ", "db-health:*", "builtin-*", "incident:critical", "anomaly:*:error_rate"}), InboxKeepMigrateReplaced, true},
		{"zaten yeni varsayılan", mk(DefaultInboxKeepSourcePriority()), InboxKeepMigrateCurrent, false},
		{"operatör daralttı (alt küme)", mk([]string{"db-health:*", "incident:critical"}), InboxKeepMigrateCustomised, false},
		{"operatör genişletti (üst küme)", mk(append(append([]string{}, oldList...), "slo:*")), InboxKeepMigrateCustomised, false},
		{"operatör kapattı ([])", mk([]string{}), InboxKeepMigrateCustomised, false},
	}
	for _, c := range cases {
		newRaw, outcome, _, err := planInboxKeepMigration(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if outcome != c.outcome || (newRaw != nil) != c.rewrite {
			t.Errorf("%s: outcome=%s rewrite=%v, istenen %s/%v", c.name, outcome, newRaw != nil, c.outcome, c.rewrite)
		}
		if !c.rewrite {
			continue
		}
		// Diğer alanlar AYNEN korunur; liste yeni varsayılan.
		var got map[string]json.RawMessage
		if err := json.Unmarshal(newRaw, &got); err != nil {
			t.Fatal(err)
		}
		if string(got["bigBreachRatio"]) != "3" || string(got["staleCriticalHours"]) != "6" || string(got["x-unknown"]) != "1" {
			t.Errorf("%s: diğer alanlar korunmalı: %s", c.name, newRaw)
		}
		var cfg ProblemPriorityConfig
		if err := json.Unmarshal(newRaw, &cfg); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg.InboxKeepSourcePriorityList(), DefaultInboxKeepSourcePriority()) {
			t.Errorf("%s: yeni liste %v", c.name, cfg.InboxKeepSourcePriorityList())
		}
	}
	if _, _, _, err := planInboxKeepMigration([]byte(`{`)); err == nil {
		t.Error("bozuk blob hata döndürmeli (tahminin üstüne yazmayız)")
	}
}

// migFakeStore — bellek içi system_settings + audit.
type migFakeStore struct {
	kv      map[string][]byte
	audits  []AuditEntry
	getErr  map[string]error
	putErr  map[string]error
	putKeys []string
}

func (f *migFakeStore) GetSetting(_ context.Context, k string) ([]byte, error) {
	if err := f.getErr[k]; err != nil {
		return nil, err
	}
	return f.kv[k], nil
}
func (f *migFakeStore) PutSetting(_ context.Context, k string, v []byte) error {
	if err := f.putErr[k]; err != nil {
		return err
	}
	f.putKeys = append(f.putKeys, k)
	f.kv[k] = v
	return nil
}
func (f *migFakeStore) AppendAudit(_ context.Context, e AuditEntry) error {
	f.audits = append(f.audits, e)
	return nil
}

func TestMigrateInboxKeepDefaults(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	oldBlob, _ := json.Marshal(map[string]any{"bigBreachRatio": 2, "staleCriticalHours": 4, inboxKeepField: legacyInboxKeepSourcePriority()})

	t.Run("eski varsayılan → yeni, audit + işaret; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: oldBlob}}
		out, err := MigrateInboxKeepDefaults(ctx, f, now)
		if err != nil || out != InboxKeepMigrateReplaced {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		var cfg ProblemPriorityConfig
		if err := json.Unmarshal(f.kv[problemPriorityKey], &cfg); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg.InboxKeepSourcePriorityList(), DefaultInboxKeepSourcePriority()) {
			t.Errorf("kayıtlı liste yeni varsayılan olmalı: %v", cfg.InboxKeepSourcePriorityList())
		}
		if len(f.audits) != 1 || f.audits[0].ActorID != "system" || f.audits[0].TargetID != problemPriorityKey {
			t.Errorf("tek system audit satırı: %+v", f.audits)
		}
		var m inboxKeepMigrateMarker
		if err := json.Unmarshal(f.kv[inboxKeepMigrateKey], &m); err != nil || m.Outcome != InboxKeepMigrateReplaced || m.Version == "" {
			t.Errorf("işaret: %s (%v)", f.kv[inboxKeepMigrateKey], err)
		}
		// Operatör sonradan listeyi daraltır → işaret varken göç bir daha koşmaz.
		narrowed, _ := json.Marshal(map[string]any{inboxKeepField: legacyInboxKeepSourcePriority()})
		f.kv[problemPriorityKey] = narrowed
		writes := len(f.putKeys)
		if out, err := MigrateInboxKeepDefaults(ctx, f, now); err != nil || out != "" || len(f.putKeys) != writes {
			t.Errorf("işaret varken no-op: outcome=%q err=%v yazım=%d", out, err, len(f.putKeys)-writes)
		}
	})

	t.Run("özelleştirilmiş liste dokunulmaz, işaret yazılır, audit yok", func(t *testing.T) {
		custom, _ := json.Marshal(map[string]any{inboxKeepField: []string{"db-health:*"}})
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: custom}}
		out, err := MigrateInboxKeepDefaults(ctx, f, now)
		if err != nil || out != InboxKeepMigrateCustomised {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		if string(f.kv[problemPriorityKey]) != string(custom) || len(f.audits) != 0 {
			t.Errorf("blob değişmemeli, audit olmamalı: %s %+v", f.kv[problemPriorityKey], f.audits)
		}
		if len(f.kv[inboxKeepMigrateKey]) == 0 {
			t.Error("işaret yazılmalı (log bir kez)")
		}
	})

	t.Run("satır yok → yalnız işaret", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{}}
		if out, err := MigrateInboxKeepDefaults(ctx, f, now); err != nil || out != InboxKeepMigrateNoRow {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		if len(f.kv[problemPriorityKey]) != 0 || len(f.putKeys) != 1 || f.putKeys[0] != inboxKeepMigrateKey {
			t.Errorf("yalnız işaret yazılmalı: %v", f.putKeys)
		}
	})

	t.Run("işaret okunamadı → hiçbir şey yazılmaz", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: oldBlob}, getErr: map[string]error{inboxKeepMigrateKey: errors.New("ch down")}}
		if _, err := MigrateInboxKeepDefaults(ctx, f, now); err == nil || len(f.putKeys) != 0 {
			t.Errorf("hata + yazım yok: err=%v yazım=%v", err, f.putKeys)
		}
	})

	t.Run("liste yazılamadı → işaret YOK (sonraki tur yeniden dener)", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: oldBlob}, putErr: map[string]error{problemPriorityKey: errors.New("ch down")}}
		if _, err := MigrateInboxKeepDefaults(ctx, f, now); err == nil || len(f.kv[inboxKeepMigrateKey]) != 0 {
			t.Errorf("işaret yazılmamalı: err=%v", err)
		}
	})
}
