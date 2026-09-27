package chstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/cilcenk/coremetry/internal/config"
	"github.com/cilcenk/coremetry/migrations"
)

// rollout_layer_admin_test.go — v0.10.197 sözleşmesi (rollout_layer_admin.go
// başlığı; entity_layer_admin_test.go aynası). Gömülü 0012 parse edilir;
// ifade SIRASI kritik sırayı korur (spans_local kolonları → spans
// sarmalayıcı → index'ler → state tabloları → MV → distributed); küme
// token'ı hiçbir ifadede kalmaz; her ifade ON CLUSTER taşır; withMV=false
// MV + sarmalayıcıyı düşürür; nesne listesi her DDL hedefini kapsar.

func TestRolloutLayerStatementsOrderAndCluster(t *testing.T) {
	stmts, err := rolloutLayerStatements("prodcluster", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) < 23 {
		t.Fatalf("0012 en az 23 ifade taşımalı (6+6 kolon, 7 index, 2 tablo, MV + sarmalayıcı), alınan %d", len(stmts))
	}
	for _, s := range stmts {
		if strings.Contains(s, "uptrace_all") {
			t.Fatalf("küme token'ı kaldı: %s", stmtHead(s))
		}
		if !strings.Contains(s, "ON CLUSTER prodcluster") {
			t.Fatalf("her ifade ON CLUSTER taşımalı: %s", stmtHead(s))
		}
	}
	idx := func(sub string) int {
		for i, s := range stmts {
			if strings.Contains(s, sub) {
				return i
			}
		}
		return -1
	}
	firstCol := idx("ALTER TABLE spans_local ON CLUSTER prodcluster\n  ADD COLUMN IF NOT EXISTS k8s_deployment")
	wrapperCol := idx("ALTER TABLE spans ON CLUSTER prodcluster\n  ADD COLUMN IF NOT EXISTS k8s_deployment")
	index := idx("ADD INDEX IF NOT EXISTS idx_k8s_replicaset")
	table := idx("CREATE TABLE IF NOT EXISTS workload_rollouts ON CLUSTER")
	mv := idx("CREATE MATERIALIZED VIEW IF NOT EXISTS workload_revision_activity_1m_local")
	dist := idx("CREATE TABLE IF NOT EXISTS workload_revision_activity_1m ON CLUSTER")
	for name, i := range map[string]int{"kolon": firstCol, "sarmalayıcı": wrapperCol, "index": index, "tablo": table, "mv": mv, "distributed": dist} {
		if i < 0 {
			t.Fatalf("%s ifadesi bulunamadı", name)
		}
	}
	if !(firstCol < wrapperCol && wrapperCol < index && index < table && table < mv && mv < dist) {
		t.Fatalf("sıra bozuk (kolon→sarmalayıcı→index→tablo→mv→distributed): col=%d wrap=%d idx=%d tbl=%d mv=%d dist=%d", firstCol, wrapperCol, index, table, mv, dist)
	}
	// MV state tabloyla aynı şekli okur: store.go şablonuyla aynı kolon kümesi
	mvStmt := stmts[mv]
	for _, want := range []string{"FROM spans_local", "if(k8s_replicaset != '', k8s_replicaset, container_image_tag)   AS revision", "anyLastSimpleState(container_image_tag)", "service_name,", "WHERE workload != '' AND revision != ''"} {
		if !strings.Contains(mvStmt, want) {
			t.Fatalf("0012 MV'de eksik: %s", want)
		}
	}
	if strings.Contains(mvStmt, "indexOf(") {
		t.Fatal("0012 MV dizi araması yapmamalı (terfi kolonu okur)")
	}
	// Faz 1a: MV'siz uygulama
	noMV, _ := rolloutLayerStatements("prodcluster", false)
	if len(noMV) != len(stmts)-2 {
		t.Fatalf("withMV=false MV + sarmalayıcıyı düşürmeli: %d vs %d", len(noMV), len(stmts))
	}
	for _, s := range noMV {
		if strings.Contains(s, "workload_revision_activity_1m") {
			t.Fatalf("withMV=false'ta MV ifadesi kaldı: %s", stmtHead(s))
		}
	}
}

// v0.10.960 — nesne listesi artık 0012 + 0015'i birlikte taşır (tek kart,
// tek durum tablosu); kapsama iki dosyanın ifadelerinin BİRLEŞİMİNE karşı
// iki yönde de aranır.
func TestRolloutLayerObjectsCoverEveryDDLTarget(t *testing.T) {
	stmts, err := rolloutLayerStatements("c", true)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := rolloutV2LayerStatements("c")
	if err != nil {
		t.Fatal(err)
	}
	stmts = append(stmts, v2...)
	all := strings.Join(stmts, "\n")
	for _, o := range RolloutLayerObjects() {
		var needle string
		switch o.Kind {
		case "column":
			needle = "ADD COLUMN IF NOT EXISTS " + o.Name + " "
		case "index":
			needle = "ADD INDEX IF NOT EXISTS " + o.Name + " "
		case "table", "distributed":
			needle = "CREATE TABLE IF NOT EXISTS " + o.Name + " ON CLUSTER"
		case "mv":
			needle = "CREATE MATERIALIZED VIEW IF NOT EXISTS " + o.Name + " ON CLUSTER"
		}
		if !strings.Contains(all, needle) {
			t.Errorf("nesne listesindeki %s (%s) 0012 ya da 0015'te yok", o.Name, o.Kind)
		}
	}
	// ters yön: dosyadaki her CREATE/ADD COLUMN/ADD INDEX hedefi listede
	for _, s := range stmts {
		for _, pre := range []string{"ADD COLUMN IF NOT EXISTS ", "ADD INDEX IF NOT EXISTS ", "CREATE TABLE IF NOT EXISTS ", "CREATE MATERIALIZED VIEW IF NOT EXISTS "} {
			if i := strings.Index(s, pre); i >= 0 {
				name := strings.Fields(s[i+len(pre):])[0]
				found := false
				for _, o := range RolloutLayerObjects() {
					if o.Name == name {
						found = true
					}
				}
				if !found {
					t.Errorf("0012/0015 hedefi nesne listesinde yok: %s", name)
				}
			}
		}
	}
}

func TestRolloutLayerMVGate(t *testing.T) {
	c := func(name string, sampled uint64, rs float64) RolloutLayerClusterCoverage {
		return RolloutLayerClusterCoverage{Cluster: name, Sampled: sampled, ReplicaSet: rs}
	}
	if !rolloutLayerMVGateOK([]RolloutLayerClusterCoverage{c("a", 100, 0.99), c("b", 50, 0.96)}, 0.95) {
		t.Fatal("iki cluster eşik üstünde → açık olmalı")
	}
	// 2026-08-30 dersi: bir cluster namespace/replicaset basmıyor → kapı KAPALI
	if rolloutLayerMVGateOK([]RolloutLayerClusterCoverage{c("a", 100, 0.99), c("b", 50, 0.0)}, 0.95) {
		t.Fatal("bir cluster eşik altında → kapalı olmalı")
	}
	if rolloutLayerMVGateOK(nil, 0.95) || rolloutLayerMVGateOK([]RolloutLayerClusterCoverage{c("a", 0, 0)}, 0.95) {
		t.Fatal("örneklemsiz → kapalı")
	}
	// İnceleme B2: VAR olan (Total>0) ama örneklemde GÖRÜLMEYEN cluster
	// "ölçemedim"dir → atlanmaz, kapatır.
	if rolloutLayerMVGateOK([]RolloutLayerClusterCoverage{c("a", 100, 0.99), {Cluster: "b", Total: 5000}}, 0.95) {
		t.Fatal("örneklemsiz cluster kapıyı kapatmalı")
	}
	// '' = cluster'sız (k8s dışı) trafik: görünür ama kapıya girmez.
	if !rolloutLayerMVGateOK([]RolloutLayerClusterCoverage{c("a", 100, 0.99), c("", 300, 0.0)}, 0.95) {
		t.Fatal("cluster'sız satır kapıyı kapatmamalı")
	}
	if rolloutLayerMVGateOK([]RolloutLayerClusterCoverage{c("", 300, 1.0)}, 0.95) {
		t.Fatal("yalnız cluster'sız trafik → kapalı (adı olan cluster yok)")
	}
}

// TestValidRolloutLayerCluster — inceleme S5: ad DDL'e ham giriyor.
func TestValidRolloutLayerCluster(t *testing.T) {
	for name, ok := range map[string]bool{"uptrace_all": true, "prod-ch.1": true, "a": true,
		"": false, "x; DROP TABLE spans": false, "a b": false, "ç": false, "`x`": false} {
		if got := validRolloutLayerCluster(name); got != ok {
			t.Errorf("%q → %v, want %v", name, got, ok)
		}
	}
	if r := (&Store{}).RolloutLayerApply(context.Background(), "x;y", false); len(r) != 1 || r[0].Err == "" {
		t.Fatalf("geçersiz ad DDL'e ulaşmamalı: %+v", r)
	}
	if r := (&Store{}).RolloutLayerRollback(context.Background(), "x y"); len(r) != 1 || r[0].Err == "" {
		t.Fatalf("geçersiz ad rollback'e ulaşmamalı: %+v", r)
	}
}

func TestRolloutLayerRollbackDropsOnlyMV(t *testing.T) {
	stmts := rolloutLayerRollbackStatements("c1")
	if len(stmts) != 2 {
		t.Fatalf("geri alma yalnız MV + sarmalayıcı (2), alınan %d", len(stmts))
	}
	for _, s := range stmts {
		if !strings.HasPrefix(s, "DROP TABLE IF EXISTS workload_revision_activity_1m") || !strings.HasSuffix(s, " SYNC") || !strings.Contains(s, "ON CLUSTER c1") {
			t.Fatalf("beklenmeyen geri alma ifadesi: %s", s)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────
// v0.10.960 — 0015 ROLLOUTS v2 STATE TABLOLARI (P1.8; docs/rollouts/v2-audit.md
// §10.5, operatör kararları 6 / 9 / 24 / 25 — 2026-09-26).
//
// Ne pinleniyor:
//   - Nesne listesi 0012'nin 17 nesnesini AYNI sırada tutar ve sonuna sekiz
//     v2 tablosunu (kind table) §10.3 sırasıyla ekler.
//   - 0015 + rollback'i gömülü (FS: sihirbaz okur; AllSQL: tablo kataloğu).
//   - BAYT EŞLİĞİ: 0015'teki her CREATE, rollout_v2_schema.go'daki Go DDL'inin
//     BELGELENMİŞ küme uyarlamasıdır (ON CLUSTER uptrace_all + motor takası),
//     yorum ve girinti DAHİL; bölücü sonrası ifade birebir; boot'un taze
//     kümede (varsayılan önek) ürettiği ifadeyle boşluk/backtick dışında aynı.
//   - Rollback yalnız sekiz tabloyu ters sırada SYNC ile düşürür, v1'e dokunmaz.
//   - Ön kontrol kararı + çakışma sınıflandırması SAF ve tablo-güdümlü.
//   - 0012'nin apply/rollback ifadeleri 0015'e dokunmaz (ayrı yol).
// Canlı ClickHouse YOK.

var rolloutV2WantTables = []string{
	"rollout_events",
	"rollout_workload_state",
	"argocd_app_status",
	"argocd_sync_events",
	"argocd_app_mapping",
	"rollout_classification",
	"ado_commit_enrichment",
	"rollout_worker_runs",
}

const rolloutLayer0012ObjectCount = 17

func TestRolloutLayerObjectsIncludeRolloutV2Tables(t *testing.T) {
	objs := RolloutLayerObjects()
	if len(objs) != rolloutLayer0012ObjectCount+len(rolloutV2WantTables) {
		t.Fatalf("nesne listesi 17 (0012) + 8 (0015) = 25 olmalı, alınan %d", len(objs))
	}
	// 0012 bölümü: 0012 ifadelerinde geçen nesneler, önde ve 0015 adı taşımadan.
	for _, o := range objs[:rolloutLayer0012ObjectCount] {
		for _, n := range rolloutV2WantTables {
			if o.Name == n {
				t.Fatalf("0015 tablosu %s 0012 bölümüne karışmış", n)
			}
		}
	}
	tail := objs[rolloutLayer0012ObjectCount:]
	for i, want := range rolloutV2WantTables {
		if tail[i].Name != want || tail[i].Kind != "table" || tail[i].Table != "" {
			t.Errorf("0015 nesnesi %d = %+v, beklenen {Name:%s Kind:table}", i, tail[i], want)
		}
	}
	// Tek kaynak: liste rollout_v2_schema.go'dan türer, elle ikinci liste yok.
	for i, ddl := range rolloutV2TableDDLs() {
		if n, _ := ddlCreatesObject(ddl); n != tail[i].Name {
			t.Errorf("nesne %d (%s) boot DDL sırasıyla (%s) uyuşmuyor", i, tail[i].Name, n)
		}
	}
}

func TestRolloutV2LayerEmbedded(t *testing.T) {
	if rolloutV2LayerFile != "0015_rollouts_v2.sql" || rolloutV2LayerRollbackFile != "0015_rollouts_v2_rollback.sql" {
		t.Fatalf("dosya adları kaydı: %q / %q", rolloutV2LayerFile, rolloutV2LayerRollbackFile)
	}
	for _, f := range []string{rolloutV2LayerFile, rolloutV2LayerRollbackFile} {
		raw, err := migrations.FS.ReadFile(f)
		if err != nil {
			t.Fatalf("%s FS'e gömülü değil (sihirbaz okuyamaz): %v", f, err)
		}
		if _, err := migrations.AllSQL.ReadFile(f); err != nil {
			t.Fatalf("%s AllSQL'de yok: %v", f, err)
		}
		if !strings.Contains(stripSQLComments(string(raw)), rollupClusterToken) {
			t.Errorf("%s ifadeleri %q token'ı taşımıyor — AdaptRollupDDL no-op olur", f, rollupClusterToken)
		}
	}
	raw, _ := migrations.FS.ReadFile(rolloutV2LayerFile)
	head := string(raw)
	for _, want := range []string{
		"v0.10.960 —",
		"Boot ASLA koşmaz", "v0.9.613",
		"karar 25", "state_replication.go", "/clickhouse/tables/state/",
		"0015_rollouts_v2_rollback.sql",
		"rollout_v2_schema.go",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("0015 başlığı %q taşımıyor", want)
		}
	}
}

func TestRolloutV2LayerStatements(t *testing.T) {
	stmts, err := rolloutV2LayerStatements("prodcluster")
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != len(rolloutV2WantTables) {
		t.Fatalf("0015 tam 8 ifade olmalı (yalnız CREATE TABLE), alınan %d", len(stmts))
	}
	for i, s := range stmts {
		n := rolloutV2WantTables[i]
		if !strings.HasPrefix(s, "CREATE TABLE IF NOT EXISTS "+n+" ON CLUSTER prodcluster (") {
			t.Errorf("ifade %d %s CREATE'i değil ya da ON CLUSTER yanlış yerde: %s", i, n, stmtHead(s))
		}
		if strings.Count(s, "ON CLUSTER") != 1 || strings.Contains(s, rollupClusterToken) {
			t.Errorf("%s: küme token'ı kaldı ya da ON CLUSTER tekrar ediyor", n)
		}
		eng := "ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/" + n + "', '{shard}-{replica}', version)"
		if !strings.Contains(s, eng) {
			t.Errorf("%s: karar 25 motoru yok (%s)", n, eng)
		}
		for _, bad := range []string{"--", "PARTITION BY", "Distributed(", "_local", "CODEC", "Nullable", "coremetry."} {
			if strings.Contains(s, bad) {
				t.Errorf("%s: beklenmeyen %q", n, bad)
			}
		}
	}
	// 0012'nin apply'ı 0015'e dokunmaz (ayrı yol, ayrı kapı).
	old, err := rolloutLayerStatements("prodcluster", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range old {
		for _, n := range rolloutV2WantTables {
			if strings.Contains(s, n) {
				t.Fatalf("0012 ifadesi 0015 tablosu %s'i anıyor: %s", n, stmtHead(s))
			}
		}
	}
}

// rolloutV2MigrationForm — 0012 sözleşmesinin (0012_rollout_layer.sql:11-17)
// BELGELENMİŞ küme uyarlaması, Go DDL'ine uygulanmış hâli: (1) adın hemen
// ardına ` ON CLUSTER uptrace_all`, (2) `ReplacingMergeTree(version)` →
// `ReplicatedReplacingMergeTree('/clickhouse/tables/state/<ad>',
// '{shard}-{replica}', version)` (karar 25). Başka HİÇBİR bayt değişmez.
func rolloutV2MigrationForm(t *testing.T, ddl, name string) string {
	t.Helper()
	create := "CREATE TABLE IF NOT EXISTS " + name + " ("
	engine := "ENGINE = ReplacingMergeTree(version)"
	if strings.Count(ddl, create) != 1 || strings.Count(ddl, engine) != 1 {
		t.Fatalf("%s: Go DDL'i beklenen CREATE/ENGINE biçiminde değil — uyarlama tanımsız", name)
	}
	out := strings.Replace(ddl, create, "CREATE TABLE IF NOT EXISTS "+name+" ON CLUSTER "+rollupClusterToken+" (", 1)
	return strings.Replace(out, engine,
		"ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/"+name+"', '{shard}-{replica}', version)", 1)
}

func normalizeRolloutV2DDL(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(stripSQLComments(s), "`", "")), " ")
}

func TestRolloutV2MigrationByteIdenticalToBootDDL(t *testing.T) {
	b, err := migrations.FS.ReadFile(rolloutV2LayerFile)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b)
	stmts := SplitSQLStatements(raw)
	ddls := rolloutV2TableDDLs()
	if len(stmts) != len(ddls) {
		t.Fatalf("0015 %d ifade, boot %d tablo — biri eklendi/silindi ama öteki değil", len(stmts), len(ddls))
	}
	// Taze küme, varsayılan önek: boot'un küme kipi çıktısı (karar 25'in
	// "varsayılan önekte aynı yol" iddiası).
	boot := &Store{cfg: config.CHConfig{ClusterName: rollupClusterToken}, stateObs: stateObservation{ok: true, paths: map[string]string{}}}
	for i, ddl := range ddls {
		name, _ := ddlCreatesObject(ddl)
		mig := rolloutV2MigrationForm(t, ddl, name)
		// (1) HAM BAYT — yorumlar, girinti, TTL günü dahil.
		if !strings.Contains(raw, mig+";\n") {
			t.Errorf("%s: 0015 metni Go DDL'inin belgelenmiş uyarlaması değil (bayt eşliği bozuk).\nbeklenen blok:\n%s;", name, mig)
		}
		// (2) BÖLÜCÜ SONRASI — sihirbazın CH'ye gönderdiği ifade, SIRA dahil.
		want := SplitSQLStatements(mig)
		if len(want) != 1 || stmts[i] != want[0] {
			t.Errorf("ifade %d (%s): bölünmüş 0015 ifadesi Go DDL uyarlamasıyla birebir değil", i, name)
		}
		// (3) BOOT'UN KÜME ÇIKTISI — adaptDDL'in `ON CLUSTER `c`  (` yazımı
		// dışında (backtick + çift boşluk) aynı ifade.
		frags := boot.adaptDDL(ddl)
		if len(frags) != 1 {
			t.Fatalf("%s: boot %d ifade üretti (state tablosu tek ifade)", name, len(frags))
		}
		if got, w := normalizeRolloutV2DDL(frags[0]), normalizeRolloutV2DDL(stmts[i]); got != w {
			t.Errorf("%s: boot (taze küme, varsayılan önek) ile 0015 ayrışıyor\nboot: %s\n0015: %s", name, got, w)
		}
	}
}

func TestRolloutV2LayerRollbackStatements(t *testing.T) {
	stmts, err := rolloutV2LayerRollbackStatements("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != len(rolloutV2WantTables) {
		t.Fatalf("0015 rollback tam 8 DROP olmalı, alınan %d", len(stmts))
	}
	for i, s := range stmts {
		want := "DROP TABLE IF EXISTS " + rolloutV2WantTables[len(rolloutV2WantTables)-1-i] + " ON CLUSTER c1 SYNC"
		if s != want {
			t.Errorf("rollback[%d] = %q, beklenen %q (ters sıra, SYNC)", i, s, want)
		}
	}
	b, err := migrations.FS.ReadFile(rolloutV2LayerRollbackFile)
	if err != nil {
		t.Fatal(err)
	}
	body := stripSQLComments(string(b))
	for _, v1 := range []string{"workload_rollouts", "rollout_reconcile_runs", "workload_revision_activity_1m", "spans"} {
		if strings.Contains(body, v1+" ") {
			t.Errorf("0015 rollback v1/telemetri nesnesine dokunuyor: %s", v1)
		}
	}
	head := string(b)
	for _, want := range []string{"v0.10.960 —", "argocd_sync_events", "geri gelmez", "SYNC"} {
		if !strings.Contains(head, want) {
			t.Errorf("rollback başlığı %q taşımıyor", want)
		}
	}
	// 0012'nin geri alması DEĞİŞMEDİ: yalnız MV + sarmalayıcı (bkz.
	// TestRolloutLayerRollbackDropsOnlyMV) ve 0015 tablolarını anmaz.
	for _, s := range rolloutLayerRollbackStatements("c1") {
		for _, n := range rolloutV2WantTables {
			if strings.Contains(s, n) {
				t.Fatalf("0012 rollback'i 0015 tablosu %s'i düşürüyor", n)
			}
		}
	}
}

func TestRolloutV2LayerConflicts(t *testing.T) {
	ok := func(h, n string) rolloutV2HostTable {
		return rolloutV2HostTable{Host: h, Table: n, Engine: "ReplicatedReplacingMergeTree", ZKPath: "/clickhouse/tables/state/" + n}
	}
	cases := []struct {
		label   string
		engines []rolloutV2HostTable
		paths   []rolloutV2HostTable
		want    []string // alt dizeler; boşsa çakışma yok
	}{
		{label: "hiç tablo yok (taze)", want: nil},
		{label: "hepsi doğru yolda", engines: []rolloutV2HostTable{ok("h1", "rollout_events"), ok("h2", "rollout_events")},
			paths: []rolloutV2HostTable{ok("h1", "rollout_events"), ok("h2", "rollout_events")}, want: nil},
		{
			// Dış Distributed + ALLOW_UNSET_CLUSTER: boot ilk host'a Replicated
			// OLMAYAN kopya kurdu → 0015 o host'ta IF NOT EXISTS ile no-op kalırdı.
			label:   "boot'un düz kopyası",
			engines: []rolloutV2HostTable{{Host: "h1", Table: "argocd_sync_events", Engine: "ReplacingMergeTree"}},
			want:    []string{"h1", "argocd_sync_events", "ReplacingMergeTree", "Replicated değil"},
		},
		{
			// 0009 öncesi / özel önekli boot kurulumu: eksik host'lar 0015'le
			// AYRI bir ZK grubuna katılırdı (split-brain).
			label: "farklı ZK yolu",
			paths: []rolloutV2HostTable{{Host: "h2", Table: "rollout_events", ZKPath: "/clickhouse/tables/01/rollout_events"}},
			want:  []string{"h2", "rollout_events", "/clickhouse/tables/01/rollout_events", "/clickhouse/tables/state/rollout_events"},
		},
	}
	for _, c := range cases {
		got := rolloutV2LayerConflicts(c.engines, c.paths)
		if len(c.want) == 0 {
			if len(got) != 0 {
				t.Errorf("[%s] çakışma beklenmiyordu: %v", c.label, got)
			}
			continue
		}
		if len(got) != 1 {
			t.Errorf("[%s] tek çakışma beklenirdi: %v", c.label, got)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(got[0], w) {
				t.Errorf("[%s] çakışma %q taşımıyor: %s", c.label, w, got[0])
			}
		}
	}
	// Deterministik sıra (host, tablo) — aynı girdi aynı metni üretir.
	a := rolloutV2LayerConflicts([]rolloutV2HostTable{
		{Host: "h2", Table: "rollout_events", Engine: "MergeTree"},
		{Host: "h1", Table: "rollout_worker_runs", Engine: "ReplacingMergeTree"},
		{Host: "h1", Table: "argocd_app_status", Engine: "ReplacingMergeTree"},
	}, nil)
	if len(a) != 3 || !strings.HasPrefix(a[0], "h1: argocd_app_status") || !strings.HasPrefix(a[1], "h1: rollout_worker_runs") || !strings.HasPrefix(a[2], "h2: rollout_events") {
		t.Errorf("çakışmalar host+tablo sırasında değil: %v", a)
	}
}

// v0.10.975 — karar ÜÇ çıktılı (supported, installed, detail): "kurulu"
// ayrı bir hüküm. Kurulu kümede Supported TRUE kalır (sunucu apply kapısı
// değişmedi — zorla basılan 0015 IF NOT EXISTS no-op'tur), kartın Uygula'sı
// Installed'ı okuyup kapanır. Sıra: probe hatası / çakışma "kurulu"yu YENER
// (emin olmadığımız ya da bölünmüş kümeye "kurulu" denmez); kısmi kurulum
// uygulanabilir kalır ve eksikleri sayar.
func TestRolloutV2LayerDecision(t *testing.T) {
	base := rolloutV2LayerGate{SpansLocal: true, Clusters: []string{"uptrace_all", "other"}, Cluster: "uptrace_all"}
	cases := []struct {
		label     string
		mut       func(g *rolloutV2LayerGate)
		ok        bool
		installed bool
		detail    string
	}{
		{"uygulanabilir (taze)", func(g *rolloutV2LayerGate) {}, true, false, "uygulanabilir: sekiz"},
		{"probe hatası kazanır", func(g *rolloutV2LayerGate) { g.ProbeErrors = 1; g.SpansLocal = false }, false, false, "probe hatası"},
		{"tek düğüm", func(g *rolloutV2LayerGate) { g.SpansLocal = false }, false, false, "spans_local yok"},
		{"küme boş", func(g *rolloutV2LayerGate) { g.Cluster = "" }, false, false, "küme seçilmedi"},
		{"küme adı geçersiz", func(g *rolloutV2LayerGate) { g.Cluster = "x;DROP" }, false, false, "geçersiz"},
		{"küme tanımsız", func(g *rolloutV2LayerGate) { g.Cluster = "typo" }, false, false, "system.clusters"},
		{"özel önek", func(g *rolloutV2LayerGate) { g.CustomPrefix = "/ch/tbl" }, false, false, "/ch/tbl"},
		{"çakışma", func(g *rolloutV2LayerGate) { g.Conflicts = []string{"h1: rollout_events — …"} }, false, false, "1 çakışma"},
		// v0.10.975 — dört yeni satır (kurulu / kısmi / çakışma / probe hatası).
		{"kurulu", func(g *rolloutV2LayerGate) { g.Installed = true }, true, true,
			"Kurulu: sekiz tablo her host'ta birleşik yolda — uygulama gerekmiyor"},
		{"kısmi kurulum uygulanabilir kalır", func(g *rolloutV2LayerGate) {
			g.Missing = []string{"host-3: rollout_events, argocd_app_status", "host-4: sekizi de yok"}
		}, true, false, "kısmi kurulum: 2 host'ta eksik"},
		{"çakışma kurulu'yu yener", func(g *rolloutV2LayerGate) {
			g.Installed = true
			g.Conflicts = []string{"host-2: rollout_events — ZK yolu …"}
		}, false, false, "1 çakışma"},
		{"probe hatası kurulu'yu yener", func(g *rolloutV2LayerGate) { g.Installed = true; g.ProbeErrors = 1 }, false, false, "probe hatası"},
		{"özel önek kurulu'yu yener", func(g *rolloutV2LayerGate) { g.Installed = true; g.CustomPrefix = "/ch/tbl" }, false, false, "/ch/tbl"},
	}
	for _, c := range cases {
		g := base
		g.Clusters = append([]string(nil), base.Clusters...)
		c.mut(&g)
		ok, installed, detail := rolloutV2LayerDecision(g)
		if ok != c.ok || installed != c.installed || !strings.Contains(detail, c.detail) {
			t.Errorf("[%s] = (%v, %v, %q), beklenen (%v, %v, …%q…)", c.label, ok, installed, detail, c.ok, c.installed, c.detail)
		}
	}
	// Kurulu hükmü "uygulanabilir" DEMEZ (kart onu yeşil UYGULANABİLİR diye
	// okumasın); kısmi hüküm "uygulanabilir" ile başlar (eski sözleşme).
	g0 := base
	g0.Installed = true
	if _, _, d := rolloutV2LayerDecision(g0); strings.Contains(d, "uygulanabilir") {
		t.Errorf("kurulu hükmü 'uygulanabilir' içermemeli: %s", d)
	}
	g0 = base
	g0.Missing = []string{"host-4: sekizi de yok"}
	if _, _, d := rolloutV2LayerDecision(g0); !strings.HasPrefix(d, "uygulanabilir") {
		t.Errorf("kısmi hüküm 'uygulanabilir' ile başlamalı: %s", d)
	}
	// v0.10.971 — özel önek reddi AYNEN durur, gerekçesi değişti: kural 3
	// kalktı, "kuşak probe'u" bozulamaz. Asıl zarar: 0015'in sabit yolu bu
	// önekte birleşik sayılmaz (eksik host kural 2 ile ayrı gruba düşer);
	// boot sekizi bu önekte zaten birleşik kurar.
	g := base
	g.CustomPrefix = "/ch/tbl"
	_, _, detail := rolloutV2LayerDecision(g)
	for _, want := range []string{"/ch/tbl/state/<ad>", "zaten birleşik"} {
		if !strings.Contains(detail, want) {
			t.Errorf("özel önek gerekçesi %q içermeli: %s", want, detail)
		}
	}
	if strings.Contains(detail, "kuşak") {
		t.Errorf("özel önek gerekçesi kaldırılan kuşak kuralını anıyor: %s", detail)
	}
}

func TestRolloutV2LayerProbeSQL(t *testing.T) {
	for label, q := range map[string]string{
		"tables":   rolloutV2TablesProbeSQL("prod_ch"),
		"replicas": rolloutV2ReplicasProbeSQL("prod_ch"),
	} {
		src := map[string]string{"tables": "clusterAllReplicas('prod_ch', system.tables)", "replicas": "clusterAllReplicas('prod_ch', system.replicas)"}[label]
		for _, want := range append([]string{src, "hostName()", "database = currentDatabase()", "max_execution_time", "LIMIT "}, rolloutV2WantTables...) {
			if !strings.Contains(q, want) {
				t.Errorf("%s probe'u %q taşımıyor: %s", label, want, q)
			}
		}
		// Ulaşılamayan host ATLANMAZ: emin olamadığımız kümeye DDL basmayız.
		if strings.Contains(q, "skip_unavailable_shards") {
			t.Errorf("%s probe'u ulaşılamayan host'u atlamamalı", label)
		}
	}
	// v0.10.975 — host evreni: sekizinden hiçbirini tutmayan host motor/ZK
	// satırlarında GÖRÜNMEZ; "her host'ta kurulu" hükmü evreni bu probe'dan
	// alır. Aynı kurallar: kümeye özgü, sınırlı, ulaşılamayan host atlanmaz.
	q := rolloutV2HostsProbeSQL("prod_ch")
	for _, want := range []string{"clusterAllReplicas('prod_ch', system.one)", "hostName()", "max_execution_time", "LIMIT "} {
		if !strings.Contains(q, want) {
			t.Errorf("host probe'u %q taşımıyor: %s", want, q)
		}
	}
	if strings.Contains(q, "skip_unavailable_shards") {
		t.Errorf("host probe'u ulaşılamayan host'u atlamamalı: %s", q)
	}
}

// v0.10.975 — KURULUM RESMİ (saf): aynı probe satırlarından (host evreni +
// motor + ZK yolu) "kurulu" ve kısmi kurulumda "ne eksik". Bir (host, tablo)
// KURULU = motor Replicated* VE ZK yolu 0015'in birleşik yolu. Kurulu =
// en az bir host ve HER (host, tablo) kurulu. Eksik listesi host başına tek
// satır, host adı sırasında, tablo §10.3 sırasında; taze kümede (hiçbir
// host'ta hiç tablo yok) BOŞ — o hâl "eksik" değil "kurulmamış". Çakışan
// satır (düz motor / yanlış yol) ne kurulu ne eksik sayılır (çakışma
// listesinin işi). Host adları sentetik.
func TestRolloutV2LayerInstall(t *testing.T) {
	hosts := []string{"host-1", "host-2", "host-3", "host-4"}
	good := func(h, n string) rolloutV2HostTable {
		return rolloutV2HostTable{Host: h, Table: n, Engine: "ReplicatedReplacingMergeTree", ZKPath: "/clickhouse/tables/state/" + n}
	}
	// full — verilen host'larda sekiz tablo, skip'teki (host/tablo) çiftleri hariç.
	full := func(hs []string, skip map[string]bool) (eng, zk []rolloutV2HostTable) {
		for _, h := range hs {
			for _, n := range rolloutV2WantTables {
				if skip[h+"/"+n] || skip[h+"/*"] {
					continue
				}
				eng = append(eng, good(h, n))
				zk = append(zk, good(h, n))
			}
		}
		return eng, zk
	}
	cases := []struct {
		label     string
		hosts     []string
		skip      map[string]bool
		mut       func(eng, zk []rolloutV2HostTable) ([]rolloutV2HostTable, []rolloutV2HostTable)
		installed bool
		missing   []string
	}{
		{label: "kurulu: 4 host × 8 tablo, birleşik yol", hosts: hosts, installed: true, missing: []string{}},
		{label: "taze: hiçbir host'ta tablo yok → eksik değil, kurulmamış", hosts: hosts,
			skip: map[string]bool{"host-1/*": true, "host-2/*": true, "host-3/*": true, "host-4/*": true}, missing: []string{}},
		{label: "host evreni boş + satır yok", hosts: nil,
			skip: map[string]bool{"host-1/*": true, "host-2/*": true, "host-3/*": true, "host-4/*": true}, missing: []string{}},
		{
			// Sonradan katılan host-4 hiç tablo tutmuyor: motor satırlarında
			// görünmez, YALNIZ host evreninden bilinir. host-3'te iki tablo eksik.
			label: "kısmi", hosts: hosts,
			skip:    map[string]bool{"host-3/rollout_events": true, "host-3/argocd_sync_events": true, "host-4/*": true},
			missing: []string{"host-3: rollout_events, argocd_sync_events", "host-4: sekizi de yok"},
		},
		{
			// Motor Replicated ama system.replicas satırı görünmüyor (oluşturma
			// anı yarışı): yolu doğrulanamadı → kurulu DEĞİL, notla listelenir.
			label: "ZK satırı görünmüyor", hosts: hosts,
			mut: func(eng, zk []rolloutV2HostTable) ([]rolloutV2HostTable, []rolloutV2HostTable) {
				out := zk[:0:0]
				for _, r := range zk {
					if !(r.Host == "host-2" && r.Table == "rollout_worker_runs") {
						out = append(out, r)
					}
				}
				return eng, out
			},
			missing: []string{"host-2: rollout_worker_runs (ZK yolu görünmüyor)"},
		},
		{
			// Çakışan satır (düz kopya) kurulu değildir ama "eksik" de değildir.
			label: "çakışan satır eksik sayılmaz", hosts: hosts,
			mut: func(eng, zk []rolloutV2HostTable) ([]rolloutV2HostTable, []rolloutV2HostTable) {
				for i := range eng {
					if eng[i].Host == "host-1" && eng[i].Table == "argocd_app_status" {
						eng[i].Engine = "ReplacingMergeTree"
					}
				}
				out := zk[:0:0]
				for _, r := range zk {
					if !(r.Host == "host-1" && r.Table == "argocd_app_status") {
						out = append(out, r)
					}
				}
				return eng, out
			},
			missing: []string{},
		},
		{
			// Yanlış ZK yolu da çakışmadır: kurulu değil, eksik değil.
			label: "yanlış yol eksik sayılmaz", hosts: hosts,
			mut: func(eng, zk []rolloutV2HostTable) ([]rolloutV2HostTable, []rolloutV2HostTable) {
				for i := range zk {
					if zk[i].Host == "host-2" && zk[i].Table == "rollout_events" {
						zk[i].ZKPath = "/clickhouse/tables/01/rollout_events"
					}
				}
				return eng, zk
			},
			missing: []string{},
		},
		{
			// Host evreni motor satırlarındaki host'larla BİRLEŞİR: evren probe'u
			// bir host'u kaçırsa da (ad biçimi farkı) o host'un tabloları sayılır.
			label: "evren motor host'larıyla birleşir", hosts: []string{"host-1"},
			skip: map[string]bool{"host-3/*": true, "host-4/*": true}, installed: true, missing: []string{},
		},
	}
	for _, c := range cases {
		eng, zk := full(hosts, c.skip)
		if c.mut != nil {
			eng, zk = c.mut(eng, zk)
		}
		installed, missing := rolloutV2LayerInstall(c.hosts, eng, zk)
		if installed != c.installed || fmt.Sprint(missing) != fmt.Sprint(c.missing) || missing == nil {
			t.Errorf("[%s] = (%v, %q), beklenen (%v, %q) (nil değil)", c.label, installed, missing, c.installed, c.missing)
		}
	}
	// Deterministik: girdi sırası karışık da olsa aynı çıktı.
	eng, zk := full(hosts, map[string]bool{"host-4/*": true, "host-2/ado_commit_enrichment": true})
	for i, j := 0, len(eng)-1; i < j; i, j = i+1, j-1 {
		eng[i], eng[j] = eng[j], eng[i]
	}
	_, a := rolloutV2LayerInstall([]string{"host-4", "host-2", "host-1", "host-3", "host-2"}, eng, zk)
	if want := []string{"host-2: ado_commit_enrichment", "host-4: sekizi de yok"}; fmt.Sprint(a) != fmt.Sprint(want) {
		t.Errorf("eksik listesi host sırasında ve tekil olmalı: %q", a)
	}
}

func TestRolloutV2LayerActionsRejectBadCluster(t *testing.T) {
	for _, c := range []string{"", "  ", "x;y", "a b", "`x`"} {
		if r := (&Store{}).RolloutV2LayerApply(context.Background(), c); len(r) != 1 || r[0].OK || r[0].Err == "" {
			t.Errorf("apply %q DDL'e ulaşmamalı: %+v", c, r)
		}
		if r := (&Store{}).RolloutV2LayerRollback(context.Background(), c); len(r) != 1 || r[0].OK || r[0].Err == "" {
			t.Errorf("rollback %q DDL'e ulaşmamalı: %+v", c, r)
		}
	}
}

// v0.10.960 — ÖN KONTROL KABLOLAMASI (bulgu F1). TestRolloutV2LayerConflicts
// ve TestRolloutV2LayerDecision yalnız SAF parçaları sınar; gerçek
// (*Store).RolloutV2LayerPreflight'ı hiçbir test koşmuyordu. Kapıya giden
// girdiyi koparan dört mutant (Conflicts: nil · probe'u önerilen kümeye
// sormak · özel önek dalını kapatmak · SpansLocal: true) bütün testleri
// YEŞİL geçiyordu ve her biri apply-0015'i şu durumlardan birinde
// yürütürdü: boot'un düz (Replicated olmayan) kopyası üstüne (IF NOT EXISTS
// o host'ta no-op), eski {shard} ZK yolu üstüne (replikasyon grubu
// bölünür), özel ReplicaPath'le (0015'in sabit yolu o önekte birleşik
// sayılmaz: boot kural 2 ile eksik host'a ayrı bir yol kurar; v0.10.971
// öncesi kural 3 ile sonraki state tabloları da eski yola dönerdi). Bu
// test gerçek fonksiyonu kalıba göre cevaplayan sahte bağlantıyla koşar
// (emsal: service_metadata_test.go fakeConn, mv_inner_conn_test.go
// scriptRow) — canlı ClickHouse YOK.

// rv2PreRows — dize satırları döndüren sahte driver.Rows.
type rv2PreRows struct {
	driver.Rows
	vals [][]string
	i    int
}

func (r *rv2PreRows) Next() bool { r.i++; return r.i <= len(r.vals) }
func (r *rv2PreRows) Scan(dest ...any) error {
	row := r.vals[r.i-1]
	if len(dest) != len(row) {
		return fmt.Errorf("Scan %d hedef, satır %d kolon", len(dest), len(row))
	}
	for k, d := range dest {
		p, ok := d.(*string)
		if !ok {
			return fmt.Errorf("Scan hedefi %d *string değil: %T", k, d)
		}
		*p = row[k]
	}
	return nil
}
func (r *rv2PreRows) Close() error { return nil }
func (r *rv2PreRows) Err() error   { return nil }

// rv2PreConn — ön kontrolün dört sorgusunu kalıba göre cevaplar; her sorgu
// metnini saklar. clusterAllReplicas probe'ları YALNIZ probeFor kümesi
// sorulduğunda satır döndürür: yanlış kümeye soran kablolama çakışmayı
// göremez ve test kızarır.
type rv2PreConn struct {
	driver.Conn
	spansLocal uint8
	clusters   []string
	probeFor   string
	hosts      []string   // v0.10.975 — system.one host evreni
	hostsErr   error      // v0.10.975
	engines    [][]string // host, table, engine
	zk         [][]string // host, table, zookeeper_path
	zkErr      error
	queries    []string
}

func (c *rv2PreConn) QueryRow(_ context.Context, q string, _ ...any) driver.Row {
	c.queries = append(c.queries, q)
	if strings.HasPrefix(q, "EXISTS TABLE spans_local") {
		return scriptRow{vals: []any{c.spansLocal}}
	}
	return scriptRow{err: errors.New("beklenmeyen QueryRow: " + q)}
}

func (c *rv2PreConn) Query(_ context.Context, q string, _ ...any) (driver.Rows, error) {
	c.queries = append(c.queries, q)
	mine := strings.Contains(q, "clusterAllReplicas('"+c.probeFor+"',")
	switch {
	case strings.Contains(q, "FROM system.clusters"):
		v := make([][]string, len(c.clusters))
		for i, n := range c.clusters {
			v[i] = []string{n}
		}
		return &rv2PreRows{vals: v}, nil
	case strings.Contains(q, "system.tables)"):
		if !mine {
			return &rv2PreRows{}, nil
		}
		return &rv2PreRows{vals: c.engines}, nil
	case strings.Contains(q, "system.replicas)"):
		if c.zkErr != nil {
			return nil, c.zkErr
		}
		if !mine {
			return &rv2PreRows{}, nil
		}
		return &rv2PreRows{vals: c.zk}, nil
	case strings.Contains(q, "system.one)"):
		if c.hostsErr != nil {
			return nil, c.hostsErr
		}
		if !mine {
			return &rv2PreRows{}, nil
		}
		v := make([][]string, len(c.hosts))
		for i, h := range c.hosts {
			v[i] = []string{h}
		}
		return &rv2PreRows{vals: v}, nil
	}
	return nil, errors.New("beklenmeyen Query: " + q)
}

func TestRolloutV2LayerPreflightWiring(t *testing.T) {
	cases := []struct {
		label       string
		req         string // istenen küme ("" = önerilen, cfg.ClusterName)
		replicaPath string
		conn        rv2PreConn
		ok          bool
		detail      string
		probeErrs   bool
	}{
		{
			// Sahte bağlantının dürüstlüğü: temiz kümede kapı AÇIK.
			label: "temiz", conn: rv2PreConn{spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all"},
			ok: true, detail: "uygulanabilir",
		},
		{
			// Conflicts kapıya taşınmalı (mutant: Conflicts: nil).
			label: "boot'un düz kopyası", conn: rv2PreConn{spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all",
				engines: [][]string{{"h1", "argocd_sync_events", "ReplacingMergeTree"}}},
			detail: "Replicated değil",
		},
		{
			// Probe İSTENEN kümeye sorulmalı (mutant: SuggestedCluster).
			label: "istenen küme ≠ önerilen", req: "other",
			conn: rv2PreConn{spansLocal: 1, clusters: []string{"other", "uptrace_all"}, probeFor: "other",
				zk: [][]string{{"h2", "rollout_events", "/clickhouse/tables/01/rollout_events"}}},
			detail: "ZK yolu",
		},
		{
			// Küme kipinde özel önek, henüz hiçbir host'ta tablo yok: çakışma
			// satırı OLMADIĞI için yalnız özel-önek dalı durdurur.
			label: "özel önek", replicaPath: "/ch/tbl",
			conn:   rv2PreConn{spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all"},
			detail: "/ch/tbl",
		},
		{
			// spans_local probe sonucu kapıya taşınmalı (mutant: SpansLocal: true).
			label:  "spans_local yok",
			conn:   rv2PreConn{spansLocal: 0, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all"},
			detail: "spans_local yok",
		},
		{
			// Probe hatası sayısı kapıya taşınmalı (mutant: ProbeErrors: 0).
			label:  "probe hatası",
			conn:   rv2PreConn{spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all", zkErr: errors.New("timeout")},
			detail: "probe hatası", probeErrs: true,
		},
		{
			// v0.10.975 — host evreni probe'unun hatası da probe hatasıdır
			// (ulaşılamayan host: "her host'ta kurulu" denemez, DDL de basılmaz).
			label:  "host probe hatası",
			conn:   rv2PreConn{spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all", hostsErr: errors.New("code: 279")},
			detail: "probe hatası", probeErrs: true,
		},
	}
	for _, c := range cases {
		conn := c.conn
		s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all", ReplicaPath: c.replicaPath}, conn: &conn}
		got, err := s.RolloutV2LayerPreflight(context.Background(), c.req)
		if err != nil {
			t.Fatalf("[%s] hata: %v", c.label, err)
		}
		if got.Supported != c.ok || !strings.Contains(got.Detail, c.detail) {
			t.Errorf("[%s] = (%v, %q), beklenen (%v, …%q…)", c.label, got.Supported, got.Detail, c.ok, c.detail)
		}
		if (len(got.ProbeErrors) > 0) != c.probeErrs {
			t.Errorf("[%s] ProbeErrors = %v", c.label, got.ProbeErrors)
		}
		// İki çakışma probe'u da (v0.10.975: + host evreni) istenen kümeye
		// (boşsa önerilene) gitmeli.
		want := c.req
		if want == "" {
			want = "uptrace_all"
		}
		for _, src := range []string{"system.tables)", "system.replicas)", "system.one)"} {
			found := false
			for _, q := range conn.queries {
				if strings.Contains(q, src) {
					found = true
					if !strings.Contains(q, "clusterAllReplicas('"+want+"',") {
						t.Errorf("[%s] %s probe'u %q yerine başka kümeye soruldu: %s", c.label, src, want, q)
					}
				}
			}
			if !found {
				t.Errorf("[%s] %s probe'u koşmadı", c.label, src)
			}
		}
	}
}

// TestRolloutV2LayerPreflightAfterStatePathRebuild — v0.10.965 (gereksinim 3):
// state yolu sihirbazı sekiz Rollouts v2 tablosunu varsayılan önekte birleşik
// yola (0015'in AYNI ifadesiyle) kurduktan sonra Rollouts kartının 0015 ön
// kontrolü çakışma GÖSTERMEZ. Üretim değişikliği gerekmez: rolloutV2LayerConflicts
// yalnız Replicated olmayan motoru ve '/clickhouse/tables/state/<ad>' dışı yolu
// işaretler; sihirbaz sonrası ikisi de sağlanır. Host adları sentetik.
func TestRolloutV2LayerPreflightAfterStatePathRebuild(t *testing.T) {
	var engines, zk [][]string
	for _, h := range []string{"host-1", "host-2", "host-3", "host-4"} {
		for _, n := range rolloutV2TableNames() {
			engines = append(engines, []string{h, n, "ReplicatedReplacingMergeTree"})
			zk = append(zk, []string{h, n, "/clickhouse/tables/state/" + n})
		}
	}
	if len(engines) != 32 {
		t.Fatalf("fikstür %d satır (4 host × 8 tablo)", len(engines))
	}
	conn := rv2PreConn{spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all",
		hosts: []string{"host-1", "host-2", "host-3", "host-4"}, engines: engines, zk: zk}
	s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: &conn}
	got, err := s.RolloutV2LayerPreflight(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 0 || got.Conflicts == nil {
		t.Errorf("çakışma = %v, beklenen []", got.Conflicts)
	}
	// v0.10.975 — bu, v0.10.965 + v0.10.971'den beri NORMAL durum: artık
	// "uygulanabilir" değil "Kurulu" (Supported yine true — sunucu apply
	// kapısı değişmedi, zorla basılan 0015 IF NOT EXISTS no-op).
	if !got.Supported || !got.Installed || got.Detail != "Kurulu: sekiz tablo her host'ta birleşik yolda — uygulama gerekmiyor" {
		t.Errorf("= (supported %v, installed %v, %q), beklenen (true, true, Kurulu…)", got.Supported, got.Installed, got.Detail)
	}
	if got.Missing == nil || len(got.Missing) != 0 {
		t.Errorf("kurulu kümede eksik listesi [] olmalı: %v", got.Missing)
	}
	// Sihirbazın kurduğu yol = 0015'in denetlediği yol (tek gerçek).
	for _, n := range rolloutV2TableNames() {
		if unifiedStatePath((&Store{}).zkPrefix(), n) != unifiedStatePath(rolloutV2ZKPrefix, n) {
			t.Errorf("%s: varsayılan önekte sihirbaz yolu 0015 yolundan farklı", n)
		}
	}
}

// TestRolloutV2LayerPreflightPartialInstall — v0.10.975: kısmi kurulum
// (çakışma yok) UYGULANABİLİR kalır ve neyin eksik olduğunu söyler. host-4
// sonradan katılmış, sekizinden hiçbirini tutmuyor → motor/ZK satırlarında
// GÖRÜNMEZ; yalnız host evreni probe'u onu bilir. Mutant (evren probe'unu
// kablolamamak / sonucu karara taşımamak) bu kümeyi "Kurulu" ilan eder ve
// Uygula kapanırdı — host-4 hiç kurulmadan. Host adları sentetik.
func TestRolloutV2LayerPreflightPartialInstall(t *testing.T) {
	var engines, zk [][]string
	for _, h := range []string{"host-1", "host-2", "host-3"} {
		for _, n := range rolloutV2TableNames() {
			if h == "host-3" && n == "argocd_app_mapping" {
				continue
			}
			engines = append(engines, []string{h, n, "ReplicatedReplacingMergeTree"})
			zk = append(zk, []string{h, n, "/clickhouse/tables/state/" + n})
		}
	}
	conn := rv2PreConn{spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all",
		hosts: []string{"host-1", "host-2", "host-3", "host-4"}, engines: engines, zk: zk}
	s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: &conn}
	got, err := s.RolloutV2LayerPreflight(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Supported || got.Installed || !strings.HasPrefix(got.Detail, "uygulanabilir") || !strings.Contains(got.Detail, "kısmi kurulum: 2 host'ta eksik") {
		t.Errorf("= (supported %v, installed %v, %q), beklenen (true, false, uygulanabilir — kısmi…)", got.Supported, got.Installed, got.Detail)
	}
	if want := []string{"host-3: argocd_app_mapping", "host-4: sekizi de yok"}; fmt.Sprint(got.Missing) != fmt.Sprint(want) {
		t.Errorf("eksik = %q, beklenen %q", got.Missing, want)
	}
	// Çakışma varken eksik listesi DÖNMEZ (UYGULANAMAZ kartında "Uygula
	// eksikleri kurar" yanıltırdı).
	conn2 := conn
	conn2.queries = nil
	conn2.engines = append([][]string{{"host-1", "rollout_events", "ReplacingMergeTree"}}, engines[1:]...)
	s2 := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: &conn2}
	got2, err := s2.RolloutV2LayerPreflight(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Supported || got2.Installed || got2.Missing == nil || len(got2.Missing) != 0 {
		t.Errorf("çakışmada = (supported %v, installed %v, missing %q), beklenen (false, false, [])", got2.Supported, got2.Installed, got2.Missing)
	}
}

// TestRolloutV2LayerPreflightJSONShape — v0.10.975: yeni iki alan telde her
// zaman var (installed bool; missing [] — null DEĞİL: FE .map()/.length).
// FE aynası: frontend/src/lib/rolloutV2Layer.contract.test.ts (struct
// etiketlerini okur) + types.ts RolloutV2LayerPreflightResult.
func TestRolloutV2LayerPreflightJSONShape(t *testing.T) {
	for label, conn := range map[string]rv2PreConn{
		"taze":          {spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all", hosts: []string{"host-1", "host-2"}},
		"probe hatası":  {spansLocal: 1, clusters: []string{"uptrace_all"}, probeFor: "uptrace_all", zkErr: errors.New("timeout")},
		"küme tanımsız": {spansLocal: 1, clusters: []string{"other"}, probeFor: "other"},
	} {
		c := conn
		s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: &c}
		got, err := s.RolloutV2LayerPreflight(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["installed"]) != "false" {
			t.Errorf("[%s] installed = %s, beklenen false", label, m["installed"])
		}
		if string(m["missing"]) != "[]" {
			t.Errorf("[%s] missing = %s, beklenen [] (null değil)", label, m["missing"])
		}
	}
}
