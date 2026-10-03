package chstore

// v0.10.1091 — yaygın yavaşlama hızlı yolu (`svc-slowdown:<servis>`). Operatör:
// "Dün söylediğim CRM sorunu yine oldu, bir sürü anomali geldi ama P1 problem
// gelmedi" → "Onay". Pinler:
//   - iki okumanın SQL golden'ı (MV, sınırlı: LIMIT + max_execution_time +
//     zaman aralıklı WHERE; operasyon okuması op_latency'nin TEK pivotu);
//   - ayar varsayılanı / kelepçe / eski blob / kayıt-okuma turu;
//   - critical + 15 s / 5 s → computePriority P1;
//   - inbox istisna listesi + v3 göçü (yalnız 1083 varsayılanı taşınır).

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

var svcSlowCur = time.Date(2026, 10, 3, 22, 10, 0, 0, time.UTC)

// Operasyon pivotu: op_latency'nin AYNI kurucusu; ek olarak yavaş pay tabanı
// (cur_p95, inceleme A) ve havuzlanmış p95 tabanı (base_p95, inceleme D) AYNI
// tDigest'ten (q bir kez birleşir), max_execution_time 10 (inceleme C).
const goldenSvcSlowOpsSQL = `
		SELECT service_name, name,
		       maxIf(p99, is_cur = 1)   AS cur_p99,
		       maxIf(p95, is_cur = 0)   AS base_p95,
		       sumIf(calls, is_cur = 1) AS cur_calls,
		       sumIf(calls, is_cur = 0) AS base_calls,
		       maxIf(p95, is_cur = 1)   AS cur_p95
		FROM (
		  SELECT service_name, name,
		         time_bucket >= ? AS is_cur,
		         quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state) AS q,
		         arrayElement(q, 3) / 1e6 AS p99,
		         arrayElement(q, 2) / 1e6 AS p95,
		         countMerge(span_count_state) AS calls
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		    AND NOT (positionCaseInsensitive(service_name, ?) > 0)
		  GROUP BY service_name, name, is_cur
		)
		GROUP BY service_name, name
		HAVING cur_calls >= ? AND base_calls >= ?
		   AND base_p95 > 0 AND cur_p99 >= ? * base_p95 AND cur_p99 >= ?
		   AND cur_p95 >= ?
		ORDER BY cur_p99 / base_p95 DESC
		LIMIT 200
		SETTINGS max_execution_time = 10`

const goldenSvcSlowServicesSQL = `
		SELECT service_name,
		       countMergeIf(span_count_state, time_bucket >= ?)                     AS cur_calls,
		       countMergeIf(span_count_state, time_bucket >= ? AND time_bucket < ?) AS hour_calls,
		       arrayElement(quantilesTDigestMergeIf(0.5, 0.95, 0.99)(duration_q_state, time_bucket >= ?), 3) / 1e6 AS cur_p99,
		       arrayElement(quantilesTDigestMergeIf(0.5, 0.95, 0.99)(duration_q_state, time_bucket < ?), 2) / 1e6  AS base_p95
		FROM service_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?
		  AND NOT (positionCaseInsensitive(service_name, ?) > 0)
		GROUP BY service_name
		HAVING service_name IN ?
		    OR (hour_calls >= ? AND cur_calls * ? <= ? * hour_calls
		        AND cur_calls >= ? AND cur_p99 >= ?
		        AND base_p95 > 0 AND cur_p99 >= ? * base_p95)
		ORDER BY cur_p99 / base_p95 DESC
		LIMIT 1000
		SETTINGS max_execution_time = 10`

func TestServiceSlowdownOpsQueryGolden(t *testing.T) {
	cfg := DefaultServiceSlowdown()
	q, args := ServiceSlowdownOpsQuery(svcSlowCur, cfg, DefaultAnomalySensitivity())
	if q != goldenSvcSlowOpsSQL {
		t.Fatalf("SQL değişti:\n%s", q)
	}
	want := []any{svcSlowCur, svcSlowCur.Add(-24 * time.Hour), svcSlowCur.Add(5 * time.Minute), "-batch", 30, 30, 20.0, 5000.0, 2500.0}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar:\n got %#v\nwant %#v", args, want)
	}
	if strings.Count(q, "?") != len(args) {
		t.Fatalf("yer tutucu %d ≠ argüman %d", strings.Count(q, "?"), len(args))
	}
	// Batch kuralı kapalı ([]) → iç WHERE dışlaması yok; op_latency'nin tek
	// kova kapısız metniyle aynı biçim (yalnız tabanlar farklı).
	off := DefaultAnomalySensitivity()
	off.BatchServicePatterns = batchPatternsPtr(nil)
	q2, args2 := ServiceSlowdownOpsQuery(svcSlowCur, cfg, off)
	if strings.Contains(q2, "AND NOT") || len(args2) != 8 {
		t.Fatalf("batch kapalıyken dışlama olmamalı: %s %#v", q2, args2)
	}
}

func TestServiceSlowdownServicesQueryGolden(t *testing.T) {
	cfg := DefaultServiceSlowdown()
	q, args := ServiceSlowdownServicesQuery(svcSlowCur, cfg, DefaultAnomalySensitivity(), []string{"crm-svc"})
	if q != goldenSvcSlowServicesSQL {
		t.Fatalf("SQL değişti:\n%s", q)
	}
	want := []any{svcSlowCur, svcSlowCur.Add(-time.Hour), svcSlowCur, svcSlowCur, svcSlowCur,
		svcSlowCur.Add(-24 * time.Hour), svcSlowCur.Add(5 * time.Minute), "-batch", []string{"crm-svc"},
		uint64(1200), uint64(1200), 60.0, uint64(100), 5000.0, 3.0}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar:\n got %#v\nwant %#v", args, want)
	}
	if strings.Count(q, "?") != len(args) {
		t.Fatalf("yer tutucu %d ≠ argüman %d", strings.Count(q, "?"), len(args))
	}
	// nil servis listesi boş DİZİ olarak bağlanır (IN ? her zaman geçerli).
	_, a2 := ServiceSlowdownServicesQuery(svcSlowCur, cfg, DefaultAnomalySensitivity(), nil)
	if l, ok := a2[8].([]string); !ok || l == nil || len(l) != 0 {
		t.Fatalf("nil → []string{}: %#v", a2[8])
	}
}

// Sınır disiplini: iki okuma da MV, LIMIT + max_execution_time + zaman
// aralıklı WHERE taşır; ham spans YOK.
func TestServiceSlowdownQueriesBounded(t *testing.T) {
	for _, q := range []string{goldenSvcSlowOpsSQL, goldenSvcSlowServicesSQL} {
		for _, frag := range []string{"LIMIT ", "max_execution_time", "WHERE time_bucket >= ? AND time_bucket < ?"} {
			if !strings.Contains(q, frag) {
				t.Errorf("%q yok", frag)
			}
		}
		if strings.Contains(q, "FROM spans") {
			t.Error("ham spans okunmamalı (MV-first)")
		}
	}
}

func TestServiceSlowdownSettings(t *testing.T) {
	d := DefaultServiceSlowdown()
	if !d.On() || d.MinOps != 3 || d.MinCallsPerOp != 30 || d.MinP99Ms != 5000 || d.RiseFactor != 20 ||
		d.MinCallsTotal != 100 || d.DropPct != 40 || d.ClearBuckets != 2 || d.MaxNewPerTick != 10 {
		t.Fatalf("operatör onaylı varsayılanlar: %+v", d)
	}
	if !reflect.DeepEqual(NormalizeServiceSlowdown(d), d) {
		t.Fatal("varsayılan normalize'da değişmemeli")
	}
	if !reflect.DeepEqual(DefaultAnomalySensitivity().ServiceSlowdown, d) {
		t.Fatal("blob varsayılanı bölüm varsayılanını taşımalı")
	}
	// Eski blob (bölüm yok) → açık + varsayılanlar.
	var old AnomalySensitivityConfig
	if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6}`), &old); err != nil {
		t.Fatal(err)
	}
	if n := NormalizeAnomalySensitivity(old).ServiceSlowdown; !reflect.DeepEqual(n, d) {
		t.Fatalf("eski blob → varsayılan: %+v", n)
	}
	// Kelepçe: aralık dışı → varsayılan; aralık içi aynen.
	bad := NormalizeServiceSlowdown(ServiceSlowdownConfig{MinOps: 1, MinCallsPerOp: 5, MinP99Ms: 100, RiseFactor: 2,
		MinCallsTotal: -1, DropPct: 99, ClearBuckets: 50, MaxNewPerTick: 0})
	if bad.MinOps != 3 || bad.MinCallsPerOp != 30 || bad.MinP99Ms != 5000 || bad.RiseFactor != 20 ||
		bad.MinCallsTotal != 100 || bad.DropPct != 40 || bad.ClearBuckets != 2 || bad.MaxNewPerTick != 10 {
		t.Fatalf("aralık dışı → varsayılan: %+v", bad)
	}
	ok := NormalizeServiceSlowdown(ServiceSlowdownConfig{MinOps: 5, MinCallsPerOp: 50, MinP99Ms: 8000, RiseFactor: 30,
		MinCallsTotal: 500, DropPct: 60, ClearBuckets: 3, MaxNewPerTick: 4})
	if ok.MinOps != 5 || ok.MinCallsPerOp != 50 || ok.MinP99Ms != 8000 || ok.RiseFactor != 30 ||
		ok.MinCallsTotal != 500 || ok.DropPct != 60 || ok.ClearBuckets != 3 || ok.MaxNewPerTick != 4 {
		t.Fatalf("aralık içi aynen: %+v", ok)
	}
	// Kayıt-okuma turu: açıkça false kalır (PUT'ta düşmez), true somutlaşır.
	fa := false
	raw, err := json.Marshal(NormalizeAnomalySensitivity(AnomalySensitivityConfig{ServiceSlowdown: ServiceSlowdownConfig{Enabled: &fa, MinOps: 4}}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"serviceSlowdown":{"enabled":false,"minOps":4`) {
		t.Fatalf("kaydedilen blob: %s", raw)
	}
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if n := NormalizeAnomalySensitivity(back).ServiceSlowdown; n.On() || n.MinOps != 4 {
		t.Fatalf("tur: %+v", n)
	}
	raw2, _ := json.Marshal(NormalizeAnomalySensitivity(AnomalySensitivityConfig{}))
	if !strings.Contains(string(raw2), `"serviceSlowdown":{"enabled":true,`) {
		t.Fatalf("nil bayrak true olarak somutlaşmalı: %s", raw2)
	}
}

// Kural ateşlediğinde DAİMA P1: Threshold = en yavaş operasyonun kendi tabanı,
// oran ≥ RiseFactor (≥ 3) → 5.5 s / 120 ms → P1, 15 s / 120 ms → P1; yalnız
// çöküş kolu (servis p99 / taban ≥ 3) → P1. Karşı örnek (eski biçim, 5.5 s /
// eşik 5 s = 1.1×) P2 olurdu — operatör kabul etmedi.
func TestServiceSlowdownPriority(t *testing.T) {
	cfg := DefaultProblemPriority()
	start := svcSlowCur.UnixNano()
	now := start + int64(5*time.Minute)
	base := Problem{RuleID: SvcSlowdownRuleID("crm-svc"), Severity: "critical", Comparator: ">=", Status: "open",
		StartedAt: start, Kind: ProblemKindService, Service: "crm-svc", Metric: "p99_ms"}
	for _, c := range []struct {
		name             string
		value, threshold float64
		want             string
	}{
		{"5.5 s / taban 120 ms → 45.8× → P1", 5500, 120, "P1"},
		{"15 s / taban 120 ms → P1", 15000, 120, "P1"},
		{"en kötü kenar: oran tam RiseFactor alt sınırı 3× → P1", 3000, 1000, "P1"},
		{"eski biçim 5.5 s / eşik 5 s → 1.1× → P2 (artık üretilmez)", 5500, 5000, "P2"},
		{"çöküş kolu: servis p99 2.4 s / taban 300 ms → 8× → P1", 2400, 300, "P1"},
	} {
		p := base
		p.Value, p.Threshold = c.value, c.threshold
		if got, why := computePriority(p, now, cfg); got != c.want {
			t.Errorf("%s: %s (%s)", c.name, got, why)
		}
	}
	// İnceleme F: "tetiklenince daima P1" yalnız bigBreachRatio ≤ 3 (vars. 2)
	// iken geçerli. Operatör bigBreachRatio'yu 3'ün üstüne çıkarırsa çöküş
	// kolunun 3–4× problemi (ve riseFactor'ü bigBreachRatio'nun altına çekilmiş
	// operasyon kolu) critical P2 olur — bilinçli: öncelik vidası operatörün.
	strict := DefaultProblemPriority()
	strict.BigBreachRatio = 4
	p := base
	p.Value, p.Threshold = 3.5*300, 300
	if got, _ := computePriority(p, now, strict); got != "P2" {
		t.Errorf("bigBreachRatio 4 + çöküş kolu 3.5× → P2 olmalı (pin): %s", got)
	}
	if got, _ := computePriority(p, now, cfg); got != "P1" {
		t.Errorf("varsayılan bigBreachRatio (2) ile aynı satır P1: %s", got)
	}
	if ProblemCategory(base) != CategorySlowdown {
		t.Errorf("kategori SLOWDOWN olmalı: %s", ProblemCategory(base))
	}
	if ProblemNotifyKind(base) != NotifyKindProblem {
		t.Errorf("bildirim türü problem olmalı: %s", ProblemNotifyKind(base))
	}
}

func TestInboxKeepSvcSlowdown(t *testing.T) {
	def := DefaultInboxKeepSourcePriority()
	if got, ok := MatchInboxKeepSourcePriority(def, SvcSlowdownRuleID("crm-svc")); !ok || got != InboxKeepSvcSlowdown {
		t.Fatalf("svc-slowdown varsayılan istisnada olmalı: %q %v", got, ok)
	}
	if err := ValidateInboxKeepSourcePriority(def); err != nil {
		t.Fatal(err)
	}
	// Bilinçli DIŞARIDA kalanlar değişmedi.
	for _, id := range []string{"anomaly-auto:ev-1:crm-svc", "slo:s1:critical", "self-ingest-stall"} {
		if _, ok := MatchInboxKeepSourcePriority(def, id); ok {
			t.Errorf("%s istisnada olmamalı", id)
		}
	}
}

func TestPlanInboxKeepMigrationV3(t *testing.T) {
	mk := func(v any) []byte {
		b, _ := json.Marshal(map[string]any{"bigBreachRatio": 3, "x-unknown": 1, inboxKeepField: v})
		return b
	}
	prev := inboxKeepDefault1083()
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
		{"1083 varsayılanı (başka sırada) → yeni", mk(rev), InboxKeepMigrateReplaced, true},
		{"zaten yeni varsayılan", mk(DefaultInboxKeepSourcePriority()), InboxKeepMigrateCurrent, false},
		{"1072 eski varsayılanı → v3 dokunmaz (v2'nin işi)", mk(legacyInboxKeepSourcePriority()), InboxKeepMigrateCustomised, false},
		{"operatör daralttı", mk([]string{"db-health:*"}), InboxKeepMigrateCustomised, false},
		{"operatör kapattı ([])", mk([]string{}), InboxKeepMigrateCustomised, false},
	} {
		newRaw, outcome, _, err := planInboxKeepMigrationFrom(c.raw, inboxKeepStepV3.from())
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

func TestMigrateInboxKeepDefaultsV3(t *testing.T) {
	ctx := context.Background()
	now := svcSlowCur
	blob1083, _ := json.Marshal(map[string]any{"bigBreachRatio": 2, inboxKeepField: inboxKeepDefault1083()})

	t.Run("1083 varsayılanı → yeni, ayrı işaret + audit; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: blob1083, inboxKeepMigrateKey: []byte(`{"outcome":"current"}`)}}
		out, err := MigrateInboxKeepDefaultsV3(ctx, f, now)
		if err != nil || out != InboxKeepMigrateReplaced {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		var cfg ProblemPriorityConfig
		_ = json.Unmarshal(f.kv[problemPriorityKey], &cfg)
		if !reflect.DeepEqual(cfg.InboxKeepSourcePriorityList(), DefaultInboxKeepSourcePriority()) {
			t.Errorf("liste: %v", cfg.InboxKeepSourcePriorityList())
		}
		var m inboxKeepMigrateMarker
		if err := json.Unmarshal(f.kv[inboxKeepMigrateKeyV3], &m); err != nil || m.Version != "v0.10.1091" || m.Outcome != InboxKeepMigrateReplaced {
			t.Errorf("v3 işareti: %s", f.kv[inboxKeepMigrateKeyV3])
		}
		if len(f.audits) != 1 || !strings.Contains(f.audits[0].Details, "yaygın yavaşlama") {
			t.Errorf("tek audit: %+v", f.audits)
		}
		writes := len(f.putKeys)
		if out, err := MigrateInboxKeepDefaultsV3(ctx, f, now); err != nil || out != "" || len(f.putKeys) != writes {
			t.Errorf("işaret varken no-op: %q %v", out, err)
		}
	})

	t.Run("sıra: v2 → v3 — 1072 listesi tek turda güncele, v3 'current'", func(t *testing.T) {
		old, _ := json.Marshal(map[string]any{inboxKeepField: legacyInboxKeepSourcePriority()})
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: old}}
		if out, err := MigrateInboxKeepDefaults(ctx, f, now); err != nil || out != InboxKeepMigrateReplaced {
			t.Fatalf("v2: %s %v", out, err)
		}
		if out, err := MigrateInboxKeepDefaultsV3(ctx, f, now); err != nil || out != InboxKeepMigrateCurrent {
			t.Fatalf("v3: %s %v", out, err)
		}
	})

	t.Run("özelleştirilmiş → dokunulmaz, işaret yazılır", func(t *testing.T) {
		custom, _ := json.Marshal(map[string]any{inboxKeepField: []string{"db-health:*"}})
		f := &migFakeStore{kv: map[string][]byte{problemPriorityKey: custom}}
		if out, err := MigrateInboxKeepDefaultsV3(ctx, f, now); err != nil || out != InboxKeepMigrateCustomised {
			t.Fatalf("%s %v", out, err)
		}
		if string(f.kv[problemPriorityKey]) != string(custom) || len(f.kv[inboxKeepMigrateKeyV3]) == 0 || len(f.audits) != 0 {
			t.Errorf("blob değişmemeli, işaret yazılmalı, audit yok")
		}
	})
}
