package chstore

// replica_repair_test.go — v0.10.820 Replika onarımı: saf planlayıcı
// yardımcıları + kaynak pinleri (node-yerel bağlantı, zaman tavanı,
// ON CLUSTER'sız adımlar, SYNC LIGHTWEIGHT + receive_timeout).

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

const testPeerDDL = "CREATE TABLE coremetry.alert_rules\n(\n    `id` String,\n    `version` UInt64\n)\nENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/alert_rules', '{replica}', version)\nORDER BY id\nSETTINGS index_granularity = 8192"

func TestExpandMacros(t *testing.T) {
	m := map[string]string{"shard": "02", "replica": "ch-04"}
	if got, err := expandMacros("/clickhouse/tables/{shard}/alert_rules", m); err != nil || got != "/clickhouse/tables/02/alert_rules" {
		t.Fatalf("got %q err %v", got, err)
	}
	if got, err := expandMacros("{shard}-{replica}", m); err != nil || got != "02-ch-04" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := expandMacros("/clickhouse/tables/{uuid}/x", m); err == nil || !strings.Contains(err.Error(), "{uuid}") {
		t.Fatalf("çözülemeyen makro hata vermeli: %v", err)
	}
	if got, err := expandMacros("/plain/path", nil); err != nil || got != "/plain/path" {
		t.Fatalf("makrosuz yol aynen: %q %v", got, err)
	}
}

func TestReplicatedEngineArgs(t *testing.T) {
	fam, path, rep, ok := replicatedEngineArgs(testPeerDDL)
	if !ok || fam != "ReplicatedReplacingMergeTree" || path != "/clickhouse/tables/{shard}/alert_rules" || rep != "{replica}" {
		t.Fatalf("got %q %q %q ok=%v", fam, path, rep, ok)
	}
	if _, _, _, ok := replicatedEngineArgs("CREATE TABLE db.t (x UInt8) ENGINE = ReplicatedMergeTree ORDER BY x"); ok {
		t.Error("argümansız Replicated (default_replica_path) ok olmamalı")
	}
	if _, _, _, ok := replicatedEngineArgs("CREATE TABLE db.t (x UInt8) ENGINE = MergeTree ORDER BY x"); ok {
		t.Error("düz MergeTree ok olmamalı")
	}
}

func TestDDLTableName(t *testing.T) {
	for in, want := range map[string]string{
		testPeerDDL:                       "alert_rules",
		"CREATE TABLE `db`.`t_x` (\n":     "t_x",
		"CREATE TABLE t (":                "t",
		"  CREATE TABLE db.t UUID 'x' (":  "t",
		"CREATE MATERIALIZED VIEW db.v (": "",
	} {
		if got := ddlTableName(in); got != want {
			t.Errorf("%q → %q, want %q", in[:20], got, want)
		}
	}
}

func TestRewriteReplicaDDL(t *testing.T) {
	out, err := rewriteReplicaDDL(testPeerDDL, "coremetry", "alert_rules", "alert_rules_fix", "/clickhouse/tables/02/alert_rules")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "CREATE TABLE `coremetry`.`alert_rules_fix`\n(") {
		t.Errorf("başlık: %q", out[:60])
	}
	if !strings.Contains(out, "ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/02/alert_rules', '{replica}', version)") {
		t.Errorf("motor argümanları: yol LİTERAL, replika makro, version korunur: %q", out)
	}
	if strings.Contains(out, "{shard}") || strings.Contains(out, "ON CLUSTER") {
		t.Errorf("{shard} / ON CLUSTER kalmamalı: %q", out)
	}
	// Eksik-tablo kipinde aynı ad; backtick'li başlık + ON CLUSTER sökülür.
	bt := "CREATE TABLE `coremetry`.`alert_rules` ON CLUSTER c1\n(\n    `id` String\n)\nENGINE = ReplicatedMergeTree('/p/{shard}/t', '{replica}')\nORDER BY id"
	out, err = rewriteReplicaDDL(bt, "coremetry", "alert_rules", "alert_rules", "/p/01/t")
	if err != nil || !strings.HasPrefix(out, "CREATE TABLE `coremetry`.`alert_rules`\n(") || strings.Contains(out, "ON CLUSTER") || !strings.Contains(out, "ReplicatedMergeTree('/p/01/t', '{replica}')") {
		t.Errorf("backtick/ON CLUSTER: %q err %v", out, err)
	}
	// Yanlış tablo → hata (eşten yanlış nesne gelmiş olmalı).
	if _, err := rewriteReplicaDDL(testPeerDDL, "coremetry", "other", "other_fix", "/x"); err == nil {
		t.Error("tablo adı uyuşmazlığı hata vermeli")
	}
	// Argümansız motor → hata.
	if _, err := rewriteReplicaDDL("CREATE TABLE db.t (x UInt8) ENGINE = ReplicatedMergeTree ORDER BY x", "db", "t", "t", "/x"); err == nil {
		t.Error("argümansız motor hata vermeli")
	}
}

func TestAttachStatements(t *testing.T) {
	st, err := attachStatements("db", "t", []ReplicaRepairPartition{{ID: "20260919"}, {ID: "202609"}, {ID: "all"}, {ID: "3e5c1f2a"}})
	if err != nil || len(st) != 4 {
		t.Fatalf("%v %v", st, err)
	}
	if st[0] != "ALTER TABLE `db`.`t_fix` ATTACH PARTITION ID '20260919' FROM `db`.`t`" {
		t.Errorf("biçim: %q", st[0])
	}
	if _, err := attachStatements("db", "t", []ReplicaRepairPartition{{ID: "x' OR 1"}}); err == nil {
		t.Error("kimlik allowlist'i enjeksiyonu reddetmeli")
	}
}

func TestCleanupGate(t *testing.T) {
	cases := []struct {
		t, f string
		ok   bool
	}{
		{"ReplicatedReplacingMergeTree", "ReplacingMergeTree", true}, // EXCHANGE olmuş
		{"ReplacingMergeTree", "ReplicatedReplacingMergeTree", true}, // EXCHANGE olmamış: geri alma
		{"ReplicatedMergeTree", "", false},                           // _fix yok
		{"ReplicatedMergeTree", "ReplicatedMergeTree", false},        // belirsiz
		{"MergeTree", "MergeTree", false},                            // belirsiz
		{"", "ReplicatedMergeTree", false},                           // canlı yok
	}
	for _, c := range cases {
		note, err := cleanupGate(c.t, c.f)
		if (err == nil) != c.ok {
			t.Errorf("(%q,%q): ok=%v note=%q err=%v", c.t, c.f, err == nil, note, err)
		}
	}
}

// TestColumnsDiff — v0.10.828 (operatör bildirimi, test kümesi 2026-09-20):
// `exception_groups · shard 2` onarım planı TEK engelle kilitlenmişti —
// "kolonlar eşle aynı değil … kolon sırası farklı". Oysa ATTACH PARTITION
// FROM kolon KÜMESİNİ karşılaştırır (CH v26.2.4.23:
// MergeTreeData::checkStructureAndGetMergeTreeData →
// getAllPhysical().sizeOfDifference(...) → NamesAndTypesList::sizeOfDifference
// iki listeyi birleştirip SIRALAR). Sıra farkı artık engel değil, NOT.
func TestColumnsDiff(t *testing.T) {
	ab := []chColumn{{"id", "String"}, {"version", "UInt64"}}
	cases := []struct {
		name           string
		peer, target   []chColumn
		wantBlocking   []string // her biri alt dize olarak aranır
		wantNoteSubstr string   // "" → not beklenmiyor
	}{
		{name: "aynı liste", peer: ab, target: ab},
		{
			name: "hedefte eksik kolon", peer: ab, target: []chColumn{{"id", "String"}},
			wantBlocking: []string{"hedefte yok: version"},
		},
		{
			name: "eşte olmayan fazla kolon", peer: ab,
			target:       []chColumn{{"id", "String"}, {"version", "UInt64"}, {"extra", "UInt8"}},
			wantBlocking: []string{"eşte yok: extra"},
		},
		{
			name: "tip farkı", peer: ab, target: []chColumn{{"id", "String"}, {"version", "UInt32"}},
			wantBlocking: []string{"tip farklı: version (UInt64 ≠ UInt32)"},
		},
		{
			name: "yalnız sıra farkı", peer: ab, target: []chColumn{{"version", "UInt64"}, {"id", "String"}},
			wantNoteSubstr: "kolon sırası farklı (ilk fark #1: eşte id, hedefte version)",
		},
		{
			// Sıra VE tip birlikte: kümeyi düşüren fark engeller, sıra notu
			// aranmaz (küme zaten eşleşmiyor).
			name: "sıra + tip", peer: ab, target: []chColumn{{"version", "UInt32"}, {"id", "String"}},
			wantBlocking: []string{"tip farklı: version"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blocking, notes := columnsDiff(c.peer, c.target)
			if len(blocking) != len(c.wantBlocking) {
				t.Fatalf("engel kovası %v, want %v", blocking, c.wantBlocking)
			}
			for i, want := range c.wantBlocking {
				if !strings.Contains(blocking[i], want) {
					t.Errorf("engel[%d] = %q, %q içermeli", i, blocking[i], want)
				}
			}
			switch {
			case c.wantNoteSubstr == "" && len(notes) != 0:
				t.Errorf("not beklenmiyordu: %v", notes)
			case c.wantNoteSubstr != "":
				if len(notes) != 1 || !strings.Contains(notes[0], c.wantNoteSubstr) {
					t.Errorf("not kovası %v, %q içeren TEK not olmalı", notes, c.wantNoteSubstr)
				}
			}
		})
	}
}

// TestColumnGate — v0.10.828: plan düzeyi sözleşme. Yalnız-sıra farkı SIFIR
// engel + TEK uyarı üretir (ve ✓ satırı sırayı doğruladığını iddia ETMEZ);
// ad/tip farkı engeller ve o durumda ✓ satırı yazılmaz.
func TestColumnGate(t *testing.T) {
	ab := []chColumn{{"id", "String"}, {"version", "UInt64"}}
	cases := []struct {
		name          string
		peer, target  []chColumn
		wantBlocked   int
		wantWarnings  int
		wantCheckSeen bool
	}{
		{name: "aynı", peer: ab, target: ab, wantCheckSeen: true},
		{
			name: "yalnız sıra", peer: ab, target: []chColumn{{"version", "UInt64"}, {"id", "String"}},
			wantWarnings: 1, wantCheckSeen: true,
		},
		{
			name: "tip farkı", peer: ab, target: []chColumn{{"id", "String"}, {"version", "UInt32"}},
			wantBlocked: 1,
		},
		{
			name: "eksik kolon", peer: ab, target: []chColumn{{"id", "String"}},
			wantBlocked: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blocked, warnings, check := columnGate(c.peer, c.target, "exception_groups_fix")
			if len(blocked) != c.wantBlocked {
				t.Errorf("engel %d (%v), want %d", len(blocked), blocked, c.wantBlocked)
			}
			if len(warnings) != c.wantWarnings {
				t.Errorf("uyarı %d (%v), want %d", len(warnings), warnings, c.wantWarnings)
			}
			if (check != "") != c.wantCheckSeen {
				t.Errorf("✓ satırı %q, beklenen görünürlük %v", check, c.wantCheckSeen)
			}
			if check != "" && !strings.Contains(check, "KÜMESİ") {
				t.Errorf("✓ satırı neyin karşılaştırıldığını söylemeli: %q", check)
			}
		})
	}
	// Uyarı metni: gerekçe + `_fix`'in sırayı eşten aldığı.
	_, warnings, _ := columnGate(ab, []chColumn{{"version", "UInt64"}, {"id", "String"}}, "exception_groups_fix")
	for _, want := range []string{"engel değil", "KÜMESİNİ karşılaştırır", "`exception_groups_fix`"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("uyarı %q, %q içermeli", warnings[0], want)
		}
	}
}

// TestDiffsAreOnlyReachedThroughGates — v0.10.828 kaynak pini: planlayıcı
// kolon/anahtar/indeks kararını YALNIZ *Gate seam'lerinden verir. Bir diff
// doğrudan çağrılırsa (tek kovaymış gibi) engel-olmayan fark yine engele
// dönerdi — 828'i doğuran hata tam buydu.
func TestDiffsAreOnlyReachedThroughGates(t *testing.T) {
	b, err := os.ReadFile("replica_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	plan := funcBody(t, "replica_repair.go", "func (s *Store) PlanReplicaRepair(")
	for _, c := range []struct{ diff, gate, call string }{
		{"columnsDiff(", "func columnGate(", "columnGate(pc, tc, fixName)"},
		{"tableKeysDiff(", "func tableKeysGate(", "tableKeysGate(pk, tk)"},
		{"skipIndexDiff(", "func skipIndexGate(", "skipIndexGate(pi, ti, strictIndexMatch(strictVal), strictKnown)"},
	} {
		if gate := funcBody(t, "replica_repair.go", c.gate); !strings.Contains(gate, c.diff) {
			t.Errorf("%s %s çağırmalı", c.gate, c.diff)
		}
		// Tanım + kapı içindeki tek çağrı = 2. Fazlası başka bir çağrandır.
		if n := strings.Count(src, c.diff); n != 2 {
			t.Errorf("%s %d yerde geçiyor (tanım + kapı = 2); başka çağıran engel-olmayan farkı yeniden engele çevirebilir", c.diff, n)
		}
		if !strings.Contains(plan, c.call) {
			t.Errorf("PlanReplicaRepair kapıyı %s ile kurmalı", c.call)
		}
		if strings.Contains(plan, c.diff) {
			t.Errorf("PlanReplicaRepair %s'i doğrudan çağırmamalı", c.diff)
		}
	}
	// Ayarın etkin değeri ATTACH HEDEFİNDE (`_fix`) geçerli olandır → targetConn.
	if !strings.Contains(plan, `targetConn.QueryRow(ctx, "SELECT value FROM system.merge_tree_settings`) {
		t.Error("enforce_index_structure_match_on_partition_manipulation hedef bağlantıda okunmalı")
	}
}

// TestTableKeysDiff — v0.10.828: partition/ORDER BY/PRIMARY KEY ENGEL kalır
// (checkStructureAndGetMergeTreeData bunları AST metni olarak karşılaştırır),
// DEPOLAMA POLİTİKASI engelden nota iner: ATTACH yolunda politika hiç
// karşılaştırılmaz (StorageReplicatedMergeTree::replacePartitionFrom;
// UNKNOWN_POLICY fırlatması movePartitionToTable'da).
func TestTableKeysDiff(t *testing.T) {
	base := chTableKeys{PartitionKey: "toDate(time)", SortingKey: "service_name, time", PrimaryKey: "service_name, time", StoragePolicy: "default"}
	mut := func(f func(*chTableKeys)) chTableKeys {
		c := base
		f(&c)
		return c
	}
	cases := []struct {
		name                    string
		target                  chTableKeys
		wantBlocking, wantNotes int
		blockSubstr, noteSubstr string
	}{
		{name: "aynı", target: base},
		{
			name: "ORDER BY farkı", target: mut(func(c *chTableKeys) { c.SortingKey += ", trace_id" }),
			wantBlocking: 1, blockSubstr: "ORDER BY",
		},
		{
			name: "partition key farkı", target: mut(func(c *chTableKeys) { c.PartitionKey = "toYYYYMM(time)" }),
			wantBlocking: 1, blockSubstr: "partition key",
		},
		{
			name: "PRIMARY KEY farkı", target: mut(func(c *chTableKeys) { c.PrimaryKey = "service_name" }),
			wantBlocking: 1, blockSubstr: "PRIMARY KEY",
		},
		{
			name: "yalnız depolama politikası", target: mut(func(c *chTableKeys) { c.StoragePolicy = "hot" }),
			wantNotes: 1, noteSubstr: "depolama politikası farklı",
		},
		{
			name: "politika + partition", target: mut(func(c *chTableKeys) { c.StoragePolicy, c.PartitionKey = "hot", "toYYYYMM(time)" }),
			wantBlocking: 1, wantNotes: 1, blockSubstr: "partition key", noteSubstr: "depolama politikası",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blocking, notes := tableKeysDiff(base, c.target)
			if len(blocking) != c.wantBlocking || len(notes) != c.wantNotes {
				t.Fatalf("engel %v / not %v, want %d / %d", blocking, notes, c.wantBlocking, c.wantNotes)
			}
			if c.blockSubstr != "" && !strings.Contains(blocking[0], c.blockSubstr) {
				t.Errorf("engel %q, %q içermeli", blocking[0], c.blockSubstr)
			}
			if c.noteSubstr != "" && !strings.Contains(notes[0], c.noteSubstr) {
				t.Errorf("not %q, %q içermeli", notes[0], c.noteSubstr)
			}
		})
	}
}

// TestTableKeysGate — yalnız politika farkı: 0 engel + 1 uyarı. ✓ satırı artık
// "depolama politikası eşle aynı" DİYEMEZ (karşılaştırmıyoruz sayılır).
func TestTableKeysGate(t *testing.T) {
	base := chTableKeys{PartitionKey: "toDate(time)", SortingKey: "service_name, time", PrimaryKey: "service_name, time", StoragePolicy: "default"}
	policy := base
	policy.StoragePolicy = "hot"
	blocked, warnings, check := tableKeysGate(base, policy)
	if len(blocked) != 0 || len(warnings) != 1 || check == "" {
		t.Fatalf("yalnız politika: engel %v, uyarı %v, ✓ %q", blocked, warnings, check)
	}
	for _, want := range []string{"engel değil", "MOVE PARTITION TO TABLE"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("uyarı %q, %q içermeli", warnings[0], want)
		}
	}
	if strings.Contains(check, "depolama politikası eşle aynı") {
		t.Errorf("✓ satırı politikayı doğruladığını iddia etmemeli: %q", check)
	}
	if !strings.Contains(check, "ATTACH'ın şartı değil") {
		t.Errorf("✓ satırı neyin karşılaştırıldığını söylemeli: %q", check)
	}
	order := base
	order.SortingKey += ", trace_id"
	blocked, warnings, check = tableKeysGate(base, order)
	if len(blocked) != 1 || len(warnings) != 0 || check != "" {
		t.Fatalf("ORDER BY farkı engel olmalı: engel %v, uyarı %v, ✓ %q", blocked, warnings, check)
	}
}

// TestSkipIndexDiffDirection — v0.10.828 YÖN sözleşmesi. Koşulan ifade
// `ALTER TABLE t_fix ATTACH … FROM t`: ATTACH HEDEFİ `_fix` = EŞİN indeksleri,
// ATTACH KAYNAĞI düz `t` = HEDEFİN indeksleri. check_definitions hedefin
// kaynağın ÜST KÜMESİ olmasını ister → düz tablodaki fazla indeks HATA,
// eşteki fazla indeks serbest.
func TestSkipIndexDiffDirection(t *testing.T) {
	i1, i2 := "i1|bloom_filter|x", "i2|minmax|y"
	cases := []struct {
		name                    string
		peer, target            []string
		wantBlocking, wantNotes int
	}{
		{name: "aynı", peer: []string{i1}, target: []string{i1}},
		{name: "yalnız eşte (ATTACH hedefi fazla) → not", peer: []string{i1, i2}, target: []string{i1}, wantNotes: 1},
		{name: "yalnız düz tabloda (ATTACH kaynağı fazla) → engel", peer: []string{i1}, target: []string{i1, i2}, wantBlocking: 1},
		{name: "her iki yön", peer: []string{i1}, target: []string{i2}, wantBlocking: 1, wantNotes: 1},
		{name: "ikisi de boş", peer: nil, target: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blocking, notes := skipIndexDiff(c.peer, c.target)
			if len(blocking) != c.wantBlocking || len(notes) != c.wantNotes {
				t.Fatalf("engel %v / not %v, want %d / %d", blocking, notes, c.wantBlocking, c.wantNotes)
			}
			if c.wantBlocking == 1 && !strings.Contains(blocking[0], "eşte yok") {
				t.Errorf("engel metni yönü söylemeli: %q", blocking[0])
			}
			if c.wantNotes == 1 && !strings.Contains(notes[0], "yalnız eşte var") {
				t.Errorf("not metni yönü söylemeli: %q", notes[0])
			}
		})
	}
}

// TestStrictIndexMatch — system.merge_tree_settings.value → bool.
func TestStrictIndexMatch(t *testing.T) {
	for in, want := range map[string]bool{"1": true, "true": true, "TRUE": true, "0": false, "": false, "false": false} {
		if got := strictIndexMatch(in); got != want {
			t.Errorf("%q → %v, want %v", in, got, want)
		}
	}
}

// TestSkipIndexGate — eşteki fazla indeks ayar KAPALI/BİLİNMİYOR iken uyarı,
// AÇIK iken engel; düz tablodaki fazla indeks her durumda engel.
func TestSkipIndexGate(t *testing.T) {
	i1, i2 := "i1|bloom_filter|x", "i2|minmax|y"
	cases := []struct {
		name                     string
		peer, target             []string
		strictOn, strictKnown    bool
		wantBlocked, wantWarning int
		wantCheck                bool
		warnSubstr               string
	}{
		{name: "aynı · ayar bilinmiyor", peer: []string{i1}, target: []string{i1}, wantCheck: true},
		{
			name: "eşte fazla · ayar okunamadı", peer: []string{i1, i2}, target: []string{i1},
			wantWarning: 1, wantCheck: true, warnSubstr: "açıksa ATTACH yine düşebilir",
		},
		{
			name: "eşte fazla · ayar kapalı", peer: []string{i1, i2}, target: []string{i1}, strictKnown: true,
			wantWarning: 1, wantCheck: true, warnSubstr: "kapalı okundu",
		},
		{
			name: "eşte fazla · ayar AÇIK", peer: []string{i1, i2}, target: []string{i1}, strictOn: true, strictKnown: true,
			wantBlocked: 1,
		},
		{
			name: "düz tabloda fazla · ayar kapalı", peer: []string{i1}, target: []string{i1, i2}, strictKnown: true,
			wantBlocked: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blocked, warnings, check := skipIndexGate(c.peer, c.target, c.strictOn, c.strictKnown)
			if len(blocked) != c.wantBlocked || len(warnings) != c.wantWarning {
				t.Fatalf("engel %v / uyarı %v, want %d / %d", blocked, warnings, c.wantBlocked, c.wantWarning)
			}
			if (check != "") != c.wantCheck {
				t.Errorf("✓ satırı %q, beklenen görünürlük %v", check, c.wantCheck)
			}
			if c.warnSubstr != "" && !strings.Contains(warnings[0], c.warnSubstr) {
				t.Errorf("uyarı %q, %q içermeli", warnings[0], c.warnSubstr)
			}
			if c.wantBlocked == 1 && !strings.Contains(blocked[0], "ÜST kümesi") && !strings.Contains(blocked[0], "BİREBİR") {
				t.Errorf("engel metni gerekçeyi söylemeli: %q", blocked[0])
			}
		})
	}
	// ✓ satırı neyin karşılaştırıldığını söyler (tam eşitlik İDDİA ETMEZ).
	_, _, check := skipIndexGate([]string{i1, i2}, []string{i1}, false, true)
	if !strings.Contains(check, "fazlası ATTACH'ın şartı değil") {
		t.Errorf("✓ satırı: %q", check)
	}
}

func TestDiskHeadroomOK(t *testing.T) {
	if !diskHeadroomOK(0, 100, 100) {
		t.Error("okunamayan boş alan (0) karar vermez")
	}
	if !diskHeadroomOK(230, 100, 100) || diskHeadroomOK(219, 100, 100) {
		t.Error("eş + yerel + %10 payı")
	}
}

// Engelli plan bile JSON'da steps:[] taşır (FE plan.steps.length okur; null Modal'ı düşürürdü).
func TestReplicaRepairPlanJSONNeverNullSteps(t *testing.T) {
	plan := &ReplicaRepairPlan{Table: "t", Partitions: []ReplicaRepairPartition{}, Steps: []string{}, Checks: []string{}, Blocked: []string{"x"}}
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"steps":[]`, `"partitions":[]`, `"checks":[]`, `"fixExists":false`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s yok: %s", want, b)
		}
	}
	if strings.Contains(string(b), "targetAddr") {
		t.Error("iç adres alanları JSON'a çıkmamalı")
	}
}

// Kaynak pinleri: hedef ve eş node-yerel bağlantıda, hostName() yoklaması,
// DB motoru okunur, her adım zaman tavanlı, SYNC LIGHTWEIGHT + receive_timeout,
// koşulan hiçbir ifade ON CLUSTER taşımaz, system.* okumaları tavanlı.
func TestReplicaRepairSourcePins(t *testing.T) {
	b, err := os.ReadFile("replica_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"SELECT hostName()", "system.databases", "context.WithTimeout", "SYSTEM SYNC REPLICA", "LIGHTWEIGHT",
		`"receive_timeout"`, "zkChildren(", "purgeGuard", "s.ReplicaConsistency(ctx)", "ATTACH PARTITION ID", "EXCHANGE TABLES",
		// inceleme 2026-09-19: tipler SQL'de sabit; anahtar/indeks/disk kapıları; Steps hiç nil değil.
		"toUInt32(total_replicas), toUInt32(active_replicas), toUInt8(is_readonly)",
		"partition_key, sorting_key, primary_key, storage_policy", "system.data_skipping_indices", "system.disks",
		"Steps: []string{}", "res.VerifyError = verr.Error()",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	if strings.Count(src, "s.shardConn(") < 3 {
		t.Error("hedef + eş + yoklama shardConn ile bağlanmalı")
	}
	if strings.Contains(src, "telemetryReadConn") || strings.Contains(src, "s.read.") {
		t.Error("DDL/system okumaları okuma havuzuna gitmemeli")
	}
	// Go string literallerinde ON CLUSTER yok (yorumlar serbest).
	for _, m := range regexp.MustCompile(`"[^"\n]*ON CLUSTER[^"\n]*"`).FindAllString(src, -1) {
		t.Errorf("koşulan ifadede ON CLUSTER: %s", m)
	}
	if n, m := strings.Count(src, "FROM system."), strings.Count(src, "max_execution_time"); m < n {
		t.Errorf("system.* okumaları tavanlı olmalı: %d okuma, %d tavan", n, m)
	}
	// Uygula plan engelliyken koşmaz; temizlik motorları DROP'tan önce okur.
	apply := src[strings.Index(src, "func (s *Store) ApplyReplicaRepair"):]
	if strings.Index(apply, "len(plan.Blocked) > 0") > strings.Index(apply, "conn.Exec(") {
		t.Error("Blocked kontrolü Exec'ten önce olmalı")
	}
	clean := src[strings.Index(src, "func (s *Store) CleanupReplicaRepair"):]
	if strings.Index(clean, "cleanupGate(") > strings.Index(clean, "conn.Exec(") {
		t.Error("cleanupGate DROP'tan önce olmalı")
	}
}
