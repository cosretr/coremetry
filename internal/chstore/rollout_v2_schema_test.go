package chstore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/config"
)

// rollout_v2_schema_test.go — v0.10.959 — ROLLOUTS v2 P1.7 sözleşmesi
// (docs/rollouts/v2-audit.md §10.2–§10.5; operatör kararları 6, 9, 24 —
// 2026-09-26).
//
// Ne pinleniyor:
//   - ŞEKİL: sekiz tablonun DDL'i §10.3'teki kolon listesiyle birebir
//     (ad + tip + DEFAULT, sırayla), motor ReplacingMergeTree(version),
//     ORDER BY TAM OLARAK dedup anahtarı (O3), TTL kolonu + gün sayısı
//     karar 6'daki gibi, PARTITION BY / Nullable / CODEC / SETTINGS YOK.
//     LowCardinality yalnız §10.3'ün işaretlediği kolonlarda (C2/C3) —
//     kolon listesi eşitliği bunu da taşır.
//   - TTL ÇAPASI ŞEKLİ: TTL kolonu §10.3'teki gibi DEFAULT'suz DateTime64(3)
//     (bildirilmiş sentinel yok). GÜVENCE DEĞİL — CH atlanan kolona tip
//     varsayılanını (1970) yazar, satır ilk TTL birleşmesinde düşer; epoch-0
//     koruması P2–P4 yazıcı doğrulayıcısının işi (rollout_v2_schema.go başlığı).
//   - YERLEŞİM: sekizi de `tables` diliminde (canonicalTables), `alters`
//     diliminde değil; planDeclarativeDDL hepsini tanır.
//   - NEGATİF SHARD PİNİ: hiçbiri highVolumeTables / defaultShardPolicy /
//     tablesWithoutTraceID'de değil → birleşik state grubu (v1 emsali
//     rollout_schema_test.go).
//   - KÜME UYARLAMASI: adaptDDL yalnız ON CLUSTER + motor takası yapar;
//     başka tek bayt değişmez (codec/partition sürprizi yok).
//   - PURGE SINIFI (karar 24) + stateTables havuz kapısı.
//
// Canlı ClickHouse YOK: hepsi saf dize / AST testi.

// rolloutV2Want — §10.3'ün kolon listesi (yorumsuz). Kolon tipi DEFAULT
// dahil tek boşlukla normalize.
type rolloutV2Want struct {
	name    string
	cols    []string
	orderBy string
	ttlCol  string
	ttlDays int
}

const rolloutV2Version = "version UInt64 DEFAULT toUnixTimestamp64Nano(now64(9))"

var rolloutV2Spec = []rolloutV2Want{
	{
		name: "rollout_events",
		cols: []string{
			"cluster_id LowCardinality(String)",
			"namespace LowCardinality(String)",
			"workload_kind LowCardinality(String)",
			"workload String",
			"incarnation_at DateTime64(3)",
			"generation UInt64",
			"started_at DateTime64(3)",
			"status LowCardinality(String)",
			"change_type LowCardinality(String)",
			"observed_generation UInt64 DEFAULT 0",
			"spec_replicas UInt32 DEFAULT 0",
			"updated_replicas UInt32 DEFAULT 0",
			"available_replicas UInt32 DEFAULT 0",
			"new_revision String DEFAULT ''",
			"old_revision String DEFAULT ''",
			"images Array(String)",
			"prev_images Array(String)",
			"version_tag String DEFAULT ''",
			"stuck_reason LowCardinality(String) DEFAULT ''",
			"succeeded_at DateTime64(3) DEFAULT 0",
			"stuck_at DateTime64(3) DEFAULT 0",
			"finished_at DateTime64(3) DEFAULT 0",
			"note String DEFAULT ''",
			"updated_at DateTime64(3) DEFAULT now64(3)",
			rolloutV2Version,
		},
		// Karar 9: incarnation_at anahtarda.
		orderBy: "(cluster_id, namespace, workload_kind, workload, incarnation_at, generation)",
		ttlCol:  "started_at", ttlDays: 180,
	},
	{
		name: "rollout_workload_state",
		cols: []string{
			"cluster_id LowCardinality(String)",
			"namespace LowCardinality(String)",
			"workload_kind LowCardinality(String)",
			"workload String",
			"incarnation_at DateTime64(3)",
			"generation UInt64",
			"observed_generation UInt64 DEFAULT 0",
			"pending_generation UInt64 DEFAULT 0",
			"pending_started_at DateTime64(3) DEFAULT 0",
			"current_revision String DEFAULT ''",
			"known_revisions Array(String)",
			"images Array(String)",
			"open_generation UInt64 DEFAULT 0",
			"first_seen_at DateTime64(3)",
			"last_seen_at DateTime64(3)",
			rolloutV2Version,
		},
		orderBy: "(cluster_id, namespace, workload_kind, workload)",
		ttlCol:  "last_seen_at", ttlDays: 400,
	},
	{
		name: "argocd_app_status",
		cols: []string{
			"instance_id LowCardinality(String)",
			"app_namespace LowCardinality(String)",
			"app_name String",
			"changed_at DateTime64(3)",
			"change_kind LowCardinality(String)",
			"sync_status LowCardinality(String)",
			"health_status LowCardinality(String)",
			"operation LowCardinality(String) DEFAULT ''",
			"sync_phase LowCardinality(String) DEFAULT ''",
			"autosync_enabled LowCardinality(String) DEFAULT ''",
			"project String DEFAULT ''",
			"repo String DEFAULT ''",
			"dest_server String DEFAULT ''",
			"dest_namespace LowCardinality(String) DEFAULT ''",
			"cluster_id LowCardinality(String) DEFAULT ''",
			rolloutV2Version,
		},
		orderBy: "(instance_id, app_namespace, app_name, changed_at)",
		ttlCol:  "changed_at", ttlDays: 180,
	},
	{
		name: "argocd_sync_events",
		cols: []string{
			"instance_id LowCardinality(String)",
			"app_namespace LowCardinality(String)",
			"app_name String",
			"op_started_at DateTime64(3)",
			"phase LowCardinality(String)",
			"finished_at DateTime64(3) DEFAULT 0",
			"automated UInt8 DEFAULT 0",
			"initiator String DEFAULT ''",
			"sync_revision String DEFAULT ''",
			"revisions Array(String)",
			"repo_url String DEFAULT ''",
			"target_revision String DEFAULT ''",
			"history_id Int64 DEFAULT -1",
			"synced_workloads Array(String)",
			"managed_workloads Array(String)",
			"dest_server String DEFAULT ''",
			"dest_namespace LowCardinality(String) DEFAULT ''",
			"cluster_id LowCardinality(String) DEFAULT ''",
			"retry_count UInt32 DEFAULT 0",
			"message String DEFAULT ''",
			rolloutV2Version,
		},
		orderBy: "(instance_id, app_namespace, app_name, op_started_at)",
		ttlCol:  "op_started_at", ttlDays: 180,
	},
	{
		name: "argocd_app_mapping",
		cols: []string{
			"cluster_id LowCardinality(String)",
			"namespace LowCardinality(String)",
			"workload_kind LowCardinality(String)",
			"workload String",
			"instance_id LowCardinality(String)",
			"app_namespace LowCardinality(String)",
			"app_name String",
			"match_method LowCardinality(String)",
			"match_class LowCardinality(String)",
			"confidence UInt8",
			"candidates UInt16 DEFAULT 1",
			"first_matched_at DateTime64(3)",
			"last_verified_at DateTime64(3)",
			"removed_at DateTime64(3) DEFAULT 0",
			rolloutV2Version,
		},
		orderBy: "(cluster_id, namespace, workload_kind, workload, instance_id, app_namespace, app_name)",
		ttlCol:  "last_verified_at", ttlDays: 30,
	},
	{
		name: "rollout_classification",
		cols: []string{
			"cluster_id LowCardinality(String)",
			"namespace LowCardinality(String)",
			"workload_kind LowCardinality(String)",
			"workload String",
			"incarnation_at DateTime64(3)",
			"generation UInt64",
			"started_at DateTime64(3)",
			"trigger LowCardinality(String)",
			"basis LowCardinality(String)",
			"confidence UInt8",
			"reason String DEFAULT ''",
			"instance_id LowCardinality(String) DEFAULT ''",
			"app_namespace LowCardinality(String) DEFAULT ''",
			"app_name String DEFAULT ''",
			"match_method LowCardinality(String) DEFAULT ''",
			"op_started_at DateTime64(3) DEFAULT 0",
			"sync_revision String DEFAULT ''",
			"initiator String DEFAULT ''",
			"repo_url String DEFAULT ''",
			"final UInt8 DEFAULT 0",
			"evaluated_at DateTime64(3)",
			rolloutV2Version,
		},
		// rollout_events anahtarıyla AYNI (satır başına bir sınıflandırma).
		orderBy: "(cluster_id, namespace, workload_kind, workload, incarnation_at, generation)",
		ttlCol:  "started_at", ttlDays: 180,
	},
	{
		name: "ado_commit_enrichment",
		cols: []string{
			"repo_url_norm String",
			"sha String",
			"repo_id String DEFAULT ''",
			"enrichment_status LowCardinality(String)",
			"attempts UInt16 DEFAULT 0",
			"next_retry_at DateTime64(3) DEFAULT 0",
			"first_requested_at DateTime64(3)",
			"enriched_at DateTime64(3) DEFAULT 0",
			"project String DEFAULT ''",
			"repo_name String DEFAULT ''",
			"commit_author String DEFAULT ''",
			"commit_author_email String DEFAULT ''",
			"committed_at DateTime64(3) DEFAULT 0",
			"commit_message String DEFAULT ''",
			"is_merge UInt8 DEFAULT 0",
			"pushed_by String DEFAULT ''",
			"pr_id UInt64 DEFAULT 0",
			"pr_title String DEFAULT ''",
			"pr_created_by String DEFAULT ''",
			"pr_closed_by String DEFAULT ''",
			"pr_closed_at DateTime64(3) DEFAULT 0",
			"merge_strategy LowCardinality(String) DEFAULT ''",
			"build_number String DEFAULT ''",
			"build_url String DEFAULT ''",
			"reason String DEFAULT ''",
			rolloutV2Version,
		},
		orderBy: "(repo_url_norm, sha)",
		ttlCol:  "first_requested_at", ttlDays: 180,
	},
	{
		name: "rollout_worker_runs",
		cols: []string{
			"worker LowCardinality(String)",
			"started_at DateTime64(3)",
			"host LowCardinality(String) DEFAULT ''",
			"finished_at DateTime64(3)",
			"status LowCardinality(String)",
			"scopes_total UInt16 DEFAULT 0",
			"scopes_ok UInt16 DEFAULT 0",
			"series_read UInt32 DEFAULT 0",
			"truncated UInt8 DEFAULT 0",
			"partial_response UInt8 DEFAULT 0",
			"rows_written UInt32 DEFAULT 0",
			"unmapped UInt32 DEFAULT 0",
			"api_calls UInt32 DEFAULT 0",
			"api_throttled UInt32 DEFAULT 0",
			"duration_ms UInt32 DEFAULT 0",
			"error String DEFAULT ''",
			rolloutV2Version,
		},
		orderBy: "(worker, started_at, host)",
		ttlCol:  "started_at", ttlDays: 30,
	},
}

// rolloutV2ShapeRe — yorumları sökülmüş DDL'in TAM şekli: CREATE, kolon
// gövdesi, motor, ORDER BY, TTL ve BAŞKA HİÇBİR ŞEY (PARTITION BY,
// PRIMARY KEY, SAMPLE BY, SETTINGS araya giremez — girerse eşleşme düşer).
var rolloutV2ShapeRe = regexp.MustCompile(`(?s)^\s*CREATE TABLE IF NOT EXISTS ([a-z_0-9]+) \(\n(.*)\n\s*\) ENGINE = ([A-Za-z]+\([^)]*\))\s*\n\s*ORDER BY (\([^)]*\))\s*\n\s*TTL (.+?)\s*$`)

type rolloutV2Parsed struct {
	name, engine, orderBy, ttl string
	cols                       []string // "ad tip [DEFAULT …]" normalize
}

func parseRolloutV2DDL(t *testing.T, ddl string) rolloutV2Parsed {
	t.Helper()
	m := rolloutV2ShapeRe.FindStringSubmatch(stripSQLComments(ddl))
	if m == nil {
		t.Fatalf("DDL beklenen şekilde değil (CREATE … ENGINE … ORDER BY … TTL, başka yan tümce yok):\n%s", ddl)
	}
	p := rolloutV2Parsed{name: m[1], engine: m[3], orderBy: m[4], ttl: strings.Join(strings.Fields(m[5]), " ")}
	for _, line := range strings.Split(m[2], "\n") {
		line = strings.TrimSuffix(strings.TrimSpace(line), ",")
		if line == "" {
			continue
		}
		p.cols = append(p.cols, strings.Join(strings.Fields(line), " "))
	}
	return p
}

func rolloutV2DDLByName(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, ddl := range rolloutV2TableDDLs() {
		name, ok := ddlCreatesObject(ddl)
		if !ok {
			t.Fatalf("ddlCreatesObject tanımadı (IF NOT EXISTS'siz CREATE mi?):\n%.120s", ddl)
		}
		if _, dup := out[name]; dup {
			t.Fatalf("%s iki kez tanımlı", name)
		}
		out[name] = ddl
	}
	return out
}

func TestRolloutV2TableSetAndOrder(t *testing.T) {
	ddls := rolloutV2TableDDLs()
	if len(ddls) != len(rolloutV2Spec) {
		t.Fatalf("rolloutV2TableDDLs %d DDL döndü, karar 6 sekiz tablo diyor", len(ddls))
	}
	for i, ddl := range ddls {
		name, _ := ddlCreatesObject(ddl)
		if name != rolloutV2Spec[i].name {
			t.Errorf("sıra %d: %q, beklenen %q (§10.3 sırası; P1.8 sihirbazının nesne listesi bu sırayı izler)",
				i, name, rolloutV2Spec[i].name)
		}
	}
}

func TestRolloutV2DDLShape(t *testing.T) {
	byName := rolloutV2DDLByName(t)
	for _, want := range rolloutV2Spec {
		t.Run(want.name, func(t *testing.T) {
			ddl, ok := byName[want.name]
			if !ok {
				t.Fatalf("%s DDL'i yok", want.name)
			}
			p := parseRolloutV2DDL(t, ddl)

			if p.engine != "ReplacingMergeTree(version)" {
				t.Errorf("motor %q — state tablosu ReplacingMergeTree(version) olmalı (§1, E1)", p.engine)
			}
			if got := strings.Join(strings.Fields(p.orderBy), " "); got != want.orderBy {
				t.Errorf("ORDER BY %q, beklenen %q — ORDER BY münhasıran dedup anahtarı (O3)", got, want.orderBy)
			}
			wantTTL := "toDate(" + want.ttlCol + ") + INTERVAL " + strconv.Itoa(want.ttlDays) + " DAY"
			if p.ttl != wantTTL {
				t.Errorf("TTL %q, beklenen %q (karar 6)", p.ttl, wantTTL)
			}
			if strings.Join(p.cols, "\n") != strings.Join(want.cols, "\n") {
				t.Errorf("kolon listesi §10.3'ten sapıyor (ad/tip/DEFAULT/sıra; LowCardinality yalnız işaretli kolonlarda):\n got:\n  %s\nwant:\n  %s",
					strings.Join(p.cols, "\n  "), strings.Join(want.cols, "\n  "))
			}

			// Düşmanca genel kontroller — beklenen listeden BAĞIMSIZ.
			for _, bad := range []string{"PARTITION BY", "Nullable", "CODEC(", "SETTINGS", ";", "?"} {
				if strings.Contains(ddl, bad) {
					t.Errorf("DDL %q içermemeli (Kural P1 / C4 / codec yok §10.2 / tek ifade / bind yer tutucusu yok)", bad)
				}
			}
			cols := map[string]string{}
			for _, c := range p.cols {
				f := strings.SplitN(c, " ", 2)
				if len(f) != 2 {
					t.Fatalf("kolon ayrıştırılamadı: %q", c)
				}
				cols[f[0]] = f[1]
				if strings.Contains(f[1], "DateTime") && !strings.HasPrefix(f[1], "DateTime64(3)") {
					t.Errorf("%s: zaman kolonu DateTime64(3) olmalı (§10.2), %q", f[0], f[1])
				}
			}
			// TTL kolonu §10.3 şekli: DEFAULT'suz DateTime64(3) (bildirilmiş
			// sentinel yok). Epoch-0'a karşı koruma DEĞİL — atlanan kolon = tip
			// varsayılanı 1970, sıfır time.Time = 1970 ya da öncesi → satır ilk
			// TTL birleşmesinde düşer. Bu pin yazıcı kontrolünün (E2, P2–P4 saf
			// doğrulayıcı) yerini TUTMAZ.
			if typ := cols[want.ttlCol]; typ != "DateTime64(3)" {
				t.Errorf("TTL kolonu %s tipi %q — §10.3 şekli DEFAULT'suz DateTime64(3) (epoch-0 koruması yazıcıda, şemada değil)",
					want.ttlCol, typ)
			}
			for _, k := range strings.Split(strings.Trim(want.orderBy, "()"), ",") {
				if _, ok := cols[strings.TrimSpace(k)]; !ok {
					t.Errorf("ORDER BY kolonu %q kolon listesinde yok", strings.TrimSpace(k))
				}
			}
			if p.cols[len(p.cols)-1] != rolloutV2Version {
				t.Errorf("son kolon %q — version UInt64 DEFAULT toUnixTimestamp64Nano(now64(9)) olmalı", p.cols[len(p.cols)-1])
			}
			// adaptDDL motor regex'i YORUMLAR dahil tüm metne uygulanır:
			// yorumda ikinci bir "ENGINE = …MergeTree(" geçseydi o da takas edilirdi.
			if n := len(reEngine.FindAllStringIndex(ddl, -1)); n != 1 {
				t.Errorf("reEngine %d eşleşme buldu, tam 1 olmalı (dört tabandan biri, tek ENGINE)", n)
			}
			if n, kind := identifyDDLTarget(ddl); n != want.name || kind != "table" {
				t.Errorf("identifyDDLTarget = (%q,%q), beklenen (%q,\"table\")", n, kind, want.name)
			}
		})
	}
}

// storeSliceIdents — store.go'daki `<name> := []string{…}` bileşik
// sabitinin ÇIPLAK TANIMLAYICI elemanları. migrateDDLSlice (ddl_slice_
// placement_test.go) yalnız dize sabitlerini görür; v1/v2 rollout DDL'leri
// paket değişkeni olarak kaydedildiği için o gezici onlara "" döner.
func storeSliceIdents(t *testing.T, name string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "store.go", nil, 0)
	if err != nil {
		t.Fatalf("store.go ayrıştırılamadı: %v", err)
	}
	out := map[string]bool{}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); !ok || id.Name != name {
			return true
		}
		cl, ok := as.Rhs[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		found = true
		for _, el := range cl.Elts {
			if id, ok := el.(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
		return false
	})
	if !found {
		t.Fatalf("store.go içinde `%s := []string{…}` bulunamadı", name)
	}
	return out
}

func TestRolloutV2RegisteredInTablesSlice(t *testing.T) {
	boot := canonicalTables(30, 30, 7)
	byName := rolloutV2DDLByName(t)
	for _, want := range rolloutV2Spec {
		if got := tableDDLByName(boot, want.name); got != byName[want.name] {
			t.Errorf("%s boot `tables` diliminde (canonicalTables) yok ya da farklı metinle", want.name)
		}
	}

	// Yerleşim: değişkenler `tables`'ta, `alters`'ta DEĞİL (ddl_slice_
	// placement sözleşmesi; AST gezicisi tanımlayıcıları göremediği için
	// burada ayrıca).
	vars := []string{
		"rolloutEventsDDL", "rolloutWorkloadStateDDL", "argocdAppStatusDDL",
		"argocdSyncEventsDDL", "argocdAppMappingDDL", "rolloutClassificationDDL",
		"adoCommitEnrichmentDDL", "rolloutWorkerRunsDDL",
	}
	inTables, inAlters := storeSliceIdents(t, "tables"), storeSliceIdents(t, "alters")
	if !inTables["workloadRolloutsDDL"] {
		t.Fatal("gezici v1 workloadRolloutsDDL'i `tables`'ta görmüyor — ölü tarama")
	}
	for _, v := range vars {
		if !inTables[v] {
			t.Errorf("%s store.go `tables` diliminde kayıtlı değil", v)
		}
		if inAlters[v] {
			t.Errorf("%s `alters` diliminde — CREATE `tables`'a (planDeclarativeDDL eler), alters ALTER taşır", v)
		}
	}

	// Bildirimsel eleyici sekizini de tanır: mevcutken hiçbiri gönderilmez,
	// taze kurulumda hepsi gönderilir.
	existing := map[string]bool{}
	ddls := rolloutV2TableDDLs()
	for _, w := range rolloutV2Spec {
		existing[w.name] = true
	}
	if send, skipped := planDeclarativeDDL(ddls, existing); len(send) != 0 || skipped != len(ddls) {
		t.Errorf("tablolar mevcutken %d gönderildi / %d elendi — hepsi elenmeliydi", len(send), skipped)
	}
	if send, skipped := planDeclarativeDDL(ddls, map[string]bool{}); len(send) != len(ddls) || skipped != 0 {
		t.Errorf("taze kurulumda %d gönderildi / %d elendi — hepsi gönderilmeliydi", len(send), skipped)
	}
}

// Negatif shard pini (rollout_schema_test.go TestWorkloadRevisionActivityRegisteredDayOne
// emsali): sekizi de düşük hacimli state → üç kayda GİRMEZ → birleşik state grubu.
func TestRolloutV2StateTablesStayUnsharded(t *testing.T) {
	for _, w := range rolloutV2Spec {
		if highVolumeTables[w.name] {
			t.Errorf("%s highVolumeTables'da — _local + Distributed üretilir, FINAL shard'lar arası birleştiremez (O5)", w.name)
		}
		if _, ok := defaultShardPolicy[w.name]; ok {
			t.Errorf("%s defaultShardPolicy'de — state tablosu shard edilmez", w.name)
		}
		if tablesWithoutTraceID[w.name] {
			t.Errorf("%s tablesWithoutTraceID'de — shard kaydı state sınıfını bozar", w.name)
		}
		if !stateTableDDL(w.name, "table") || !stateProbeTable(w.name) {
			t.Errorf("%s birleşik state grubuna düşmüyor (stateTableDDL/stateProbeTable)", w.name)
		}
	}
}

// Küme kipi: adaptDDL her tabloyu TEK ifadeye çevirir, yalnız ON CLUSTER
// ekler ve motoru takas eder. Beklenen metin ORİJİNALDEN türetilir, yani
// başka tek bir bayt (codec, partition, _local, Distributed) değişirse eşitlik düşer.
func TestRolloutV2ClusterAdaptation(t *testing.T) {
	legacyObs := stateObservation{ok: true, paths: map[string]string{
		"problems": "/clickhouse/tables/{shard}/problems", // 0009 öncesi kurulum
	}}
	cases := []struct {
		label       string
		replicaPath string
		obs         stateObservation
		engine      func(name string) string
	}{
		{
			// Taze küme, varsayılan önek: 0015'in sabit yoluyla AYNI (karar 25).
			label: "taze küme, varsayılan önek", obs: stateObservation{ok: true, paths: map[string]string{}},
			engine: func(n string) string {
				return "ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/" + n + "', '{shard}-{replica}', version)"
			},
		},
		{
			label: "taze küme, özel önek", replicaPath: "/ch/tbl/", obs: stateObservation{ok: true, paths: map[string]string{}},
			engine: func(n string) string {
				return "ENGINE = ReplicatedReplacingMergeTree('/ch/tbl/state/" + n + "', '{shard}-{replica}', version)"
			},
		},
		{
			// Probe koşmadı → eski (shard'lı) yol: mevcut kümeye dokunulmaz.
			label: "probe koşmadı", obs: stateObservation{},
			engine: func(n string) string {
				return "ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/" + n + "', '{replica}', version)"
			},
		},
		{
			// Göç öncesi kurulum → yeni tablo komşularına katılır (split-brain muhafızı).
			label: "0009 öncesi küme", obs: legacyObs,
			engine: func(n string) string {
				return "ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/" + n + "', '{replica}', version)"
			},
		},
	}
	byName := rolloutV2DDLByName(t)
	for _, c := range cases {
		s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: c.replicaPath}, stateObs: c.obs}
		for _, w := range rolloutV2Spec {
			ddl := byName[w.name]
			frags := s.adaptDDL(ddl)
			if len(frags) != 1 {
				t.Errorf("[%s] %s: %d ifade — state tablosu Distributed sarmalayıcı almaz", c.label, w.name, len(frags))
				continue
			}
			want := strings.Replace(ddl, "CREATE TABLE IF NOT EXISTS "+w.name,
				"CREATE TABLE IF NOT EXISTS "+w.name+" ON CLUSTER `c` ", 1)
			want = strings.Replace(want, "ENGINE = ReplacingMergeTree(version)", c.engine(w.name), 1)
			if frags[0] != want {
				t.Errorf("[%s] %s: küme uyarlaması yalnız ON CLUSTER + motor takası olmalı\n got: %s\nwant: %s",
					c.label, w.name, frags[0], want)
			}
		}
	}

	// Tek düğüm (uygulama sahibi): ifade bayt bayt aynı gider.
	single := &Store{cfg: config.CHConfig{}}
	for _, w := range rolloutV2Spec {
		if got := single.adaptDDL(byName[w.name]); len(got) != 1 || got[0] != byName[w.name] {
			t.Errorf("tek düğümde %s değiştirilmemeli", w.name)
		}
	}
}

// Karar 24: yedisi telemetri sınıfı (türev, yeniden doğar); argocd_sync_events
// korunur (Argo yalnız son 10 geçmiş kaydını tutar — purge edilen senkron
// tarihçesi geri gelmez).
func TestRolloutV2PurgeClassification(t *testing.T) {
	inPurge, inPreserve := map[string]bool{}, map[string]bool{}
	for _, n := range telemetryPurgeTables {
		inPurge[n] = true
	}
	for _, n := range configPreserveTables {
		inPreserve[n] = true
	}
	for _, w := range rolloutV2Spec {
		wantPreserve := w.name == "argocd_sync_events"
		if wantPreserve {
			if !inPreserve[w.name] || inPurge[w.name] {
				t.Errorf("%s configPreserveTables'da olmalı, telemetryPurgeTables'da OLMAMALI (karar 24)", w.name)
			}
			continue
		}
		if !inPurge[w.name] || inPreserve[w.name] {
			t.Errorf("%s telemetryPurgeTables'da olmalı, configPreserveTables'da OLMAMALI (karar 24)", w.name)
		}
	}
}

// Havuz kapısı (conn_strategy_test.go): sekiz FROM + v1'in eksik
// `FROM workload_rollouts`'u stateTables'ta — RoundRobin okuma havuzuna
// sızan bir state okuması test kızarır.
func TestRolloutV2StateTablesGuardedFromReadPool(t *testing.T) {
	have := map[string]bool{}
	for _, s := range stateTables {
		have[s] = true
	}
	want := []string{"FROM workload_rollouts"}
	for _, w := range rolloutV2Spec {
		want = append(want, "FROM "+w.name)
	}
	for _, s := range want {
		if !have[s] {
			t.Errorf("stateTables %q taşımıyor", s)
		}
	}
}
