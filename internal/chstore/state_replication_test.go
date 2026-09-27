// v0.9.1308 — state tabloları tek replikasyon grubunda.
//
// ORİJİNAL BELİRTİ: küme kipinde her state tablosu `<prefix>/{shard}/<ad>`
// ZK yoluna kuruluyordu ve Distributed sarmalayıcıları olmadığı için her
// shard AYRI bir replikasyon grubu oluyordu. Uygulama hangi host'a
// bağlanırsa onun dilimini görüyordu:
//
//	LOKAL  problems 4631/191 · anomaly_events 222/60 · alert_rules 8/0
//	PROD   problems 633.236 (bp01/bp02) vs 4.169 (bp03/bp04)
//
// Bu dosya ÜÇ şeyi çiviliyor:
//  1. hangi tablonun hangi yolu aldığı (state vs telemetri),
//  2. telemetri yolunun BİR KARAKTER bile değişmediği,
//  3. split-brain muhafızının karar zinciri.
package chstore

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/config"
)

// measuredStateTables — canlı dağıtık kümeden (chc-0/chc-1, 2026-08-23)
// okunan çıplak Replicated tablo listesi:
//
//	SELECT name FROM system.tables
//	WHERE database='coremetry' AND engine LIKE 'Replicated%'
//	  AND name NOT LIKE '.inner%' AND name NOT LIKE '%\_local'
//
// 37 tablo. Türetimin ölçülen gerçekle aynı kümeyi vermesi ŞART: bu
// liste bir "ikinci kayıt" değil, türetimin PİNİ.
var measuredStateTables = []string{
	"ai_calls", "ai_feedback", "alert_rules", "anomaly_events", "anomaly_silences",
	"api_tokens", "audit_log", "dashboards", "events", "exception_groups",
	"incident_events", "incident_problems", "incidents", "ldap_groups", "log_templates",
	"maintenance_windows", "monitor_results", "monitors", "notification_channels",
	"notification_log", "problems", "rag_chunks", "rca_verdicts", "root_cause_hypotheses",
	"runbook_executions", "runbooks", "saved_views", "service_contracts", "service_metadata",
	"slos", "status_page_components", "status_page_config", "status_page_published",
	"status_page_subscribers", "system_settings", "trace_snapshots", "users",
}

// TestStateTableDerivation — türetim ölçülen kümeyi birebir vermeli.
//
// MUTASYON: `defaultShardPolicy`'ye "problems" eklenirse bu test kızarır
// (doğrulandı) — yani state listesi shard kayıtlarından TÜREDİĞİ için
// iki listenin ıraksaması imkânsız.
func TestStateTableDerivation(t *testing.T) {
	for _, n := range measuredStateTables {
		if !stateTableDDL(n, "table") {
			t.Errorf("%s state sayılmadı — shard kayıtlarından birine sızmış olmalı", n)
		}
	}
	// Ters yön: shard'lı telemetri ASLA state olamaz.
	sharded := map[string]bool{}
	for n := range highVolumeTables {
		sharded[n] = true
	}
	for n := range defaultShardPolicy {
		sharded[n] = true
	}
	for n := range tablesWithoutTraceID {
		sharded[n] = true
	}
	for n := range sharded {
		if stateTableDDL(n, "table") {
			t.Errorf("%s TELEMETRİ ama state sayıldı — birleşik gruba shard'lı veri yazardı", n)
		}
	}
	// Ölçülen küme ile shard kayıtları KESİŞMEMELİ.
	for _, n := range measuredStateTables {
		if sharded[n] {
			t.Errorf("%s hem ölçülen state listesinde hem shard kayıtlarında", n)
		}
	}
}

// TestStateTableDDLKindGuard — MV ve ALTER asla state değil.
//
// Bir MV shard-yerel `_local` kaynaktan beslenir; hedefini birleşik
// gruba almak iki shard'ın MV'sini aynı tabloya toplardı. 0009'un
// kapsamı DEĞİL.
func TestStateTableDDLKindGuard(t *testing.T) {
	for _, kind := range []string{"mv", "altertable", ""} {
		if stateTableDDL("problems", kind) {
			t.Errorf("kind=%q için state sayıldı — yalnız CREATE TABLE aday olmalı", kind)
		}
	}
	if stateTableDDL("", "table") {
		t.Error("boş ad state sayıldı")
	}
}

// TestReplicatedArgsTelemetryUnchanged — v0.9.1308 ÖNCESİ biçim.
//
// Telemetri tablolarının ZK yolu bu değişiklikte bir karakter bile
// değişmemeli. Karşılaştırma, adaptDDL'de v0.9.1308'e kadar duran
// format dizesinin BİREBİR kopyasına karşı yapılır.
func TestReplicatedArgsTelemetryUnchanged(t *testing.T) {
	const legacyFmt = "'%s/{shard}/%s', '{replica}'" // v0.9.1308 öncesi adaptDDL:1101
	for _, prefix := range []string{"/clickhouse/tables", "/clickhouse/tables/coremetry"} {
		for name := range highVolumeTables {
			want := fmt.Sprintf(legacyFmt, prefix, name)
			if got := replicatedArgs(prefix, name, false); got != want {
				t.Errorf("%s: %q, eski biçim %q", name, got, want)
			}
		}
	}
}

// TestReplicatedArgsUnified — birleşik yolun tam metni.
//
// MUTASYON: stateReplicaName "{shard}-{replica}" → "{replica}" yapılırsa
// bu test kızarır (doğrulandı). `{replica}` shard başına tekrar ediyorsa
// iki host aynı replika adını iddia eder → REPLICA_ALREADY_EXISTS.
func TestReplicatedArgsUnified(t *testing.T) {
	got := replicatedArgs("/clickhouse/tables", "problems", true)
	want := "'/clickhouse/tables/state/problems', '{shard}-{replica}'"
	if got != want {
		t.Errorf("= %q\nbeklenen %q", got, want)
	}
	if replicatedArgs("/ch/x", "users", true) != "'/ch/x/state/users', '{shard}-{replica}'" {
		t.Errorf("operatör öneki taşınmadı: %q", replicatedArgs("/ch/x", "users", true))
	}
	// Yol {shard} makrosunu İÇERMEMELİ — tek grup olmasının şartı.
	for _, name := range measuredStateTables {
		p := unifiedStatePath("/clickhouse/tables", name)
		if strings.Contains(p, "{shard}") {
			t.Errorf("%s birleşik yolunda {shard} var: %q", name, p)
		}
	}
}

// TestUseUnifiedStatePath — SPLIT-BRAIN muhafızı.
//
// Sessiz bölünmenin imkânsız olduğunu çiviler: kod birleşik yolu yalnız
// ÖNERİR, kümede gözlenen yol her zaman kazanır.
//
// v0.10.971 — operatör kararı 2026-09-27 ("Önerini yapalım", öneri 2): eski
// kural 3 ("herhangi bir state tablosu eski yoldaysa hiç var olmayan tablo da
// eski yola") KALDIRILDI. Prod olayı: ingest_ledger eski yolda doğdu
// (v0.10.767), sonraki her state tablosu (ai_eval_runs + sekiz Rollouts v2)
// shard başına bölünmüş doğdu. Artık hiçbir node'da olmayan tablo HER ZAMAN
// birleşik yola kurulur (kural 4); gözlenen tablo (kural 2) ve koşmamış probe
// (kural 1) davranışı birebir aynı.
func TestUseUnifiedStatePath(t *testing.T) {
	const pfx = "/clickhouse/tables"
	legacy := func(n string) string { return pfx + "/{shard}/" + n }
	unified := func(n string) string { return pfx + "/state/" + n }
	shard := func(sh, n string) string { return pfx + "/" + sh + "/" + n }

	// 0009 öncesi kurulum: ölçülen 37 state tablosunun HEPSİ eski yolda.
	pre0009 := map[string]string{}
	for _, n := range measuredStateTables {
		pre0009[n] = legacy(n)
	}

	tests := []struct {
		name       string
		obs        stateObservation
		tbl        string
		want       bool
		wantReason string // boş değilse gerekçede geçmeli
	}{
		{
			name:       "kural 1: probe koşmadı → ESKİ yol (mevcut kuruluma dokunma)",
			obs:        stateObservation{},
			tbl:        "problems",
			want:       false,
			wantReason: "probe koşmadı",
		},
		{
			name: "kural 1: probe koşmadı, komşular eski yolda olsa da → ESKİ",
			obs:  stateObservation{paths: map[string]string{"problems": legacy("problems")}},
			tbl:  "rollout_events",
			want: false,
		},
		{
			name:       "taze kurulum: hiçbir state tablosu yok → BİRLEŞİK",
			obs:        stateObservation{ok: true, paths: map[string]string{}},
			tbl:        "problems",
			want:       true,
			wantReason: "hiç var olmayan state tablosu birleşik yola kurulur",
		},
		{
			name:       "kural 2: tablo eski yolda → ESKİ (komşularına katıl)",
			obs:        stateObservation{ok: true, paths: map[string]string{"problems": legacy("problems")}},
			tbl:        "problems",
			want:       false,
			wantReason: "komşularına katılıyor",
		},
		{
			name:       "kural 2: tablo birleşik yolda → BİRLEŞİK",
			obs:        stateObservation{ok: true, paths: map[string]string{"problems": unified("problems")}},
			tbl:        "problems",
			want:       true,
			wantReason: "zaten birleşik",
		},
		{
			// "YENİ NODE kümeye giriyor" senaryosu artık YALNIZ kural 2'dir:
			// `users` yeni node'da yok ama clusterAllReplicas onu komşularda
			// eski yolda görür → yeni node komşularına katılır.
			name: "kural 2: YENİ NODE, tablo komşularda eski yolda gözlendi → ESKİ",
			obs: stateObservation{ok: true, paths: map[string]string{
				"problems":    unified("problems"),
				"alert_rules": unified("alert_rules"),
				"users":       shard("02", "users"),
			}},
			tbl:        "users",
			want:       false,
			wantReason: "/clickhouse/tables/02/users",
		},
		{
			// v0.10.971 — ASIL OLAY (prod, v0.10.960): kümede eski yolda state
			// tabloları var, yeni tablo HİÇBİR node'da yok. Eski kural 3 bunu
			// eski yola kurar ve shard başına böldürürdü.
			name: "v0.10.971: tablo hiçbir node'da yok, komşu state tabloları eski yolda → BİRLEŞİK",
			obs: stateObservation{ok: true, paths: map[string]string{
				"ingest_ledger": shard("01", "ingest_ledger"),
				"ai_eval_runs":  shard("02", "ai_eval_runs"),
			}},
			tbl:        "rollout_events",
			want:       true,
			wantReason: "kümede yok",
		},
		{
			// 0009'u hiç koşmamış kurulum: var olanlar eski yolda KALIR (kural 2),
			// hiç var olmayan yeni tablo birleşik doğar ve göç gerektirmez.
			name: "v0.10.971: 0009 öncesi kurulum (37 tablo eski) + yeni tablo → BİRLEŞİK",
			obs:  stateObservation{ok: true, paths: pre0009},
			tbl:  "rollout_worker_runs",
			want: true,
		},
		{
			name: "0009 öncesi kurulum: var olan tablo → ESKİ (kural 2)",
			obs:  stateObservation{ok: true, paths: pre0009},
			tbl:  "problems",
			want: false,
		},
		{
			name: "göç sonrası küme: tablo yok, komşular birleşik → BİRLEŞİK",
			obs: stateObservation{ok: true, paths: map[string]string{
				"problems":    unified("problems"),
				"alert_rules": unified("alert_rules"),
			}},
			tbl:  "users",
			want: true,
		},
		{
			// Göç YARIM kalmış: bazıları taşındı, bazıları taşınmadı.
			// Taşınmamış bir tablo hâlâ eski yolda görülür → ona uy.
			name: "yarım göç: taşınmış tablo BİRLEŞİK",
			obs: stateObservation{ok: true, paths: map[string]string{
				"problems": unified("problems"),
				"users":    legacy("users"),
			}},
			tbl:  "problems",
			want: true,
		},
		{
			name: "yarım göç: taşınmamış tablo ESKİ",
			obs: stateObservation{ok: true, paths: map[string]string{
				"problems": unified("problems"),
				"users":    legacy("users"),
			}},
			tbl:  "users",
			want: false,
		},
		{
			// v0.10.971 — yarım göçte HİÇ görülmemiş tablo artık BİRLEŞİK:
			// katılacağı bir grup yok, eski yol ona yalnız bölünme verirdi.
			name: "yarım göç: görülmemiş tablo BİRLEŞİK",
			obs: stateObservation{ok: true, paths: map[string]string{
				"problems": unified("problems"),
				"users":    legacy("users"),
			}},
			tbl:  "slos",
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := useUnifiedStatePath(tc.obs, pfx, tc.tbl)
			if got != tc.want {
				t.Errorf("= %v (%s), beklenen %v", got, reason, tc.want)
			}
			if reason == "" {
				t.Error("gerekçe boş — log/teşhis değersizleşir")
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("gerekçe %q, %q içermeli", reason, tc.wantReason)
			}
			// v0.10.971 — kaldırılan kuralın metni hiçbir dalda dönmemeli.
			if strings.Contains(reason, "göç ÖNCESİ") {
				t.Errorf("kaldırılan kural 3'ün gerekçesi döndü: %q", reason)
			}
		})
	}
}

// TestUseUnifiedStatePathNeverKeyedByOtherTables — v0.10.971: karar YALNIZ
// sorulan tablonun gözlemine bakar. Aynı gözleme başka eski yollu tablolar
// eklemek, hiç var olmayan bir tablonun cevabını DEĞİŞTİRMEZ (kaldırılan
// kural 3'ün kilidi geri gelirse bu test kızarır).
func TestUseUnifiedStatePathNeverKeyedByOtherTables(t *testing.T) {
	const pfx = "/clickhouse/tables"
	paths := map[string]string{}
	base, baseReason := useUnifiedStatePath(stateObservation{ok: true, paths: paths}, pfx, "rollout_events")
	for i, n := range measuredStateTables {
		if i%2 == 0 {
			paths[n] = pfx + "/{shard}/" + n
		} else {
			paths[n] = pfx + "/state/" + n
		}
		got, reason := useUnifiedStatePath(stateObservation{ok: true, paths: paths}, pfx, "rollout_events")
		if got != base || reason != baseReason || !got {
			t.Fatalf("%d. tablodan (%s) sonra = (%v, %q), boş gözlemde (%v, %q)", i+1, n, got, reason, base, baseReason)
		}
	}
}

// TestStateProbeLogLine — v0.10.971: "(N birleşik, M eski)" biçimi runbook'ta
// alıntılanır (0010 AŞAMA B, sihirbazın başarı notu) ve DEĞİŞMEZ; kaldırılan
// kuşak eki ("yeni state tabloları ESKİ yola kurulacak") geri gelmez.
func TestStateProbeLogLine(t *testing.T) {
	clean := stateProbeLogLine(47, 47, 0)
	if !strings.Contains(clean, "47 tablo gözlendi (47 birleşik, 0 eski)") || strings.Contains(clean, "BÖLÜNMÜŞ") {
		t.Errorf("temiz küme: %q", clean)
	}
	split := stateProbeLogLine(47, 37, 10)
	if !strings.Contains(split, "(37 birleşik, 10 eski) — eski yoldakiler shard başına BÖLÜNMÜŞ") ||
		!strings.Contains(split, "hiç var olmayan state tabloları birleşik yola kurulur") {
		t.Errorf("bölünmüş küme: %q", split)
	}
	for _, l := range []string{clean, split} {
		if strings.Contains(l, "ESKİ yola kurulacak") || strings.Contains(l, "yola kurulacak") {
			t.Errorf("kaldırılan kuşak eki döndü: %q", l)
		}
	}
}

// TestAdaptDDLNewStateTableOnLegacyCluster — v0.10.971 uçtan uca: eski yolda
// state tabloları olan bir kümede boot'un CREATE'i. Hiç var olmayan tablo
// birleşik yola ve {shard}-{replica} adına, gözlenen eski tablo kendi eski
// yoluna (unified=false dalı v0.9.1308 öncesiyle bayt bayt aynı) kurulur.
func TestAdaptDDLNewStateTableOnLegacyCluster(t *testing.T) {
	s := &Store{
		cfg: config.CHConfig{ClusterName: "uptrace_all"},
		stateObs: stateObservation{ok: true, paths: map[string]string{
			"ingest_ledger": "/clickhouse/tables/01/ingest_ledger",
			"problems":      "/clickhouse/tables/{shard}/problems",
		}},
	}
	ddl := func(n string) string {
		return "CREATE TABLE IF NOT EXISTS " + n + " (\n\t`id` String,\n\t`version` UInt64\n) ENGINE = ReplacingMergeTree(version)\nORDER BY id"
	}
	fresh := strings.Join(s.adaptDDL(ddl("rollout_events")), "\n")
	if !strings.Contains(fresh, "ReplicatedReplacingMergeTree('/clickhouse/tables/state/rollout_events', '{shard}-{replica}', version)") {
		t.Errorf("hiç var olmayan tablo birleşik yola kurulmadı:\n%s", fresh)
	}
	joined := strings.Join(s.adaptDDL(ddl("problems")), "\n")
	if !strings.Contains(joined, "ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/problems', '{replica}', version)") {
		t.Errorf("gözlenen eski tablo komşularına katılmadı:\n%s", joined)
	}
}

// TestStateProbeTable — probe'un gördüğü GERÇEK CH adları elenmeli.
//
// system.replicas `spans_local` döndürür; shard kayıtlarında o ad yok,
// yani eleme olmasaydı `spans_local` STATE sayılır: boot log'unun sayımı
// ve Replika tutarlılığı kartının listesi onu "eski yolda bölünmüş state
// tablosu" diye gösterirdi (v0.10.971 öncesi kuşak kararını da kilitlerdi).
func TestStateProbeTable(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"problems", true},
		{"users", true},
		{"spans_local", false},
		{"metric_points_local", false},
		{"service_callers_5m_local", false},
		{".inner_id.45f12d2e-36ac-4cb4-8bef-5025847af024", false},
		{"spans", false},            // shard'lı telemetri (Distributed sarmalayıcı)
		{"logs", false},             //
		{"problems_old", false},     // 0009 yedeği — eski yolda YAŞAR
		{"problems_unified", false}, // 0009 ara tablosu
	}
	for _, tc := range tests {
		if got := stateProbeTable(tc.name); got != tc.want {
			t.Errorf("stateProbeTable(%q) = %v, beklenen %v", tc.name, got, tc.want)
		}
	}
}

// v0.10.858 (scale-audit) — replika yolu taraması tavanlı; skip bayrağı ayarı
// ekler, kaldırmaz.
func TestReplicaPathsQueryIsCapped(t *testing.T) {
	for _, skip := range []bool{false, true} {
		q := replicaPathsSQL("clusterAllReplicas('c', system.replicas)", skip)
		if !strings.Contains(q, "max_execution_time = 10") {
			t.Fatalf("skip=%v tavansız: %s", skip, q)
		}
		if strings.Contains(q, "skip_unavailable_shards = 1") != skip {
			t.Fatalf("skip=%v ayarı yanlış: %s", skip, q)
		}
	}
}
