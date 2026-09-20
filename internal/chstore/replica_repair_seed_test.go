package chstore

// replica_repair_seed_test.go — v0.10.829 "İlk replikayı kur" (seed kipi).
//
// Operatör bildirimi (test kümesi, 2026-09-20): `events · shard 1` iki host'ta
// da "Replicated değil (ReplacingMergeTree)"; 820'nin sihirbazı eş replika
// istediği için hiç düğme çıkmıyordu. Seed kipi shard'ın düz tablosu olan bir
// host'unu shard'ın İLK replikası yapar.
//
// Buradaki testler SAF çekirdeği çiviler: uygunluk, DDL yeniden yazımı (kolon
// bloğu BAYT BAYT korunur), makro çakışması, doğrulama notu ve kaynak pinleri.

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/config"
)

// testSeedPlainDDL — hedefin KENDİ SHOW CREATE'i: düz ReplacingMergeTree.
const testSeedPlainDDL = "CREATE TABLE coremetry.events\n(\n    `id` String,\n    `kind` LowCardinality(String),\n    `time` DateTime64(9),\n    `version` UInt64 DEFAULT toUnixTimestamp64Nano(now64(9))\n)\nENGINE = ReplacingMergeTree(version)\nORDER BY (time, id)\nSETTINGS index_granularity = 8192"

// TestSeedEligibility — "İlk replikayı kur" kapısı. Girdi TAZE rapordan
// gelir; FE'deki canSeedFirstReplica aynı dört kuralı okur ama yalnız düğmeyi
// gizler — DDL'i bu kapı durdurur.
func TestSeedEligibility(t *testing.T) {
	plainHost := ReplicaMissingHost{Host: "hostA", Engine: "ReplacingMergeTree"}
	emptyHost := ReplicaMissingHost{Host: "hostB"}
	rep := ReplicaState{Host: "hostC", Engine: "ReplicatedReplacingMergeTree", ZKPath: "/p/events"}
	cases := []struct {
		name       string
		table      string
		shard      *ReplicaShard
		host       string
		wantReason string // "" = uygun
	}{
		{
			name:  "operatör vakası: iki host da düz, Replicated replika yok",
			table: "events", host: "hostA",
			shard: &ReplicaShard{Shard: 1, Verdict: ReplicaNotReplicated, Missing: []ReplicaMissingHost{plainHost, {Host: "hostB", Engine: "ReplacingMergeTree"}}},
		},
		{
			name:  "shard'da Replicated replika VAR → normal Onar",
			table: "events", host: "hostA",
			shard:      &ReplicaShard{Shard: 1, Verdict: ReplicaNotReplicated, Replicas: []ReplicaState{rep}, Missing: []ReplicaMissingHost{plainHost}},
			wantReason: "zaten 1 Replicated replika var",
		},
		{
			// v0.10.829 boş-shard dalı (operatör: span_links_reverse · shard 2,
			// İKİ host da "tablo yok"): taşınacak veri yok, boş Replicated
			// tablo kurulur.
			name:  "(a) shard'ın HEPSİ tablo yok → uygun",
			table: "span_links_reverse", host: "hostB",
			shard: &ReplicaShard{Shard: 2, Verdict: ReplicaMissing, Missing: []ReplicaMissingHost{emptyHost, {Host: "hostD"}}},
		},
		{
			// Tohum verisi bir yerdeyse ilk replika ORADA kurulur: boş host'u
			// birinci replika yapmak dolu host'un satırlarını ikinci sınıf
			// bırakırdı.
			name:  "(b) boş host ama shard'da DÜZ tablo taşıyan başka host var → reddet, o host'u işaret et",
			table: "span_links_reverse", host: "hostB",
			shard:      &ReplicaShard{Shard: 2, Verdict: ReplicaNotReplicated, Missing: []ReplicaMissingHost{emptyHost, plainHost}},
			wantReason: "ÖNCE hostA'ta kur",
		},
		{
			name:  "(c) boş host + shard'da Replicated replika var → yine reddet (Onar eşe katar)",
			table: "span_links_reverse", host: "hostB",
			shard:      &ReplicaShard{Shard: 2, Verdict: ReplicaMissing, Replicas: []ReplicaState{rep}, Missing: []ReplicaMissingHost{emptyHost}},
			wantReason: "zaten 1 Replicated replika var",
		},
		{
			name:  "host'ta tablo Replicated ama kayıtsız",
			table: "events", host: "hostA",
			shard:      &ReplicaShard{Shard: 1, Verdict: ReplicaMissing, Missing: []ReplicaMissingHost{{Host: "hostA", Engine: "ReplicatedReplacingMergeTree"}}},
			wantReason: "zaten Replicated",
		},
		{
			name: "MV iç tablosu", table: ".inner_id.abc", host: "hostA",
			shard:      &ReplicaShard{Shard: 1, Verdict: ReplicaNotReplicated, Missing: []ReplicaMissingHost{plainHost}},
			wantReason: "MV iç tablosu",
		},
		{
			name: "`_fix` sihirbazın geçici tablosu", table: "events_fix", host: "hostA",
			shard:      &ReplicaShard{Shard: 1, Verdict: ReplicaNotReplicated, Missing: []ReplicaMissingHost{plainHost}},
			wantReason: "geçici tablosu",
		},
		{
			name: "yapısal olmayan karar (ıraksama)", table: "events", host: "hostA",
			shard:      &ReplicaShard{Shard: 1, Verdict: ReplicaDivergent, Missing: []ReplicaMissingHost{plainHost}},
			wantReason: "ilk replika yalnız eksik/replike-olmayan",
		},
		{
			name: "host bu shard'da eksik değil", table: "events", host: "hostZ",
			shard:      &ReplicaShard{Shard: 1, Verdict: ReplicaNotReplicated, Missing: []ReplicaMissingHost{plainHost}},
			wantReason: "eksik host değil",
		},
		{
			name: "shard raporda yok", table: "events", host: "hostA",
			wantReason: "shard raporda yok",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := seedEligibility(c.table, c.shard, c.host)
			switch {
			case c.wantReason == "" && got != "":
				t.Fatalf("uygun olmalıydı, ret: %s", got)
			case c.wantReason != "" && got == "":
				t.Fatalf("ret bekleniyordu (%s), uygun döndü", c.wantReason)
			case c.wantReason != "" && !strings.Contains(got, c.wantReason):
				t.Errorf("ret %q, %q içermeli", got, c.wantReason)
			}
		})
	}
}

// TestSeedPathDecision — v0.10.829 incelemesi (1): İKİNCİ REPLİKASYON GRUBU
// yaratmayı imkânsız kılan kapı. Kanonik yol pod'un boot anındaki stateObs
// sondajından gelir; bu kapı onu kümede GÖZLENEN kayıtlara karşı doğrular.
func TestSeedPathDecision(t *testing.T) {
	macros := map[string]map[string]string{
		"h1": {"shard": "01", "replica": "r1"},
		"h2": {"shard": "02", "replica": "r2"},
	}
	cases := []struct {
		name            string
		pathArg         string
		computed        string
		observed        []seedObservedPath
		wantPath        string
		wantBlockSubstr string
		wantEvidenceHas string
	}{
		{
			name:    "kayıt yok → kanonik hesap sürer (c)",
			pathArg: "/p/state/events", computed: "/p/state/events",
			wantPath: "/p/state/events", wantEvidenceHas: "hiçbir shard'da Replicated değil",
		},
		{
			// TAM OLAY: kuşak kuralı "eski yol" dedi, ama tablo kardeş
			// shard'da birleşik yolda yaşıyor. Uydurulmuş yola gidilseydi
			// ikinci grup doğardı; gözlenen kazanır.
			name:    "birleşik biçim + gözlenen yol → gözlenen benimsenir (a)",
			pathArg: "/p/state/events", computed: "/p/state/events",
			observed: []seedObservedPath{{Host: "h2", Path: "/p/state/events"}},
			wantPath: "/p/state/events", wantEvidenceHas: "GÖZLENEN yol benimsendi",
		},
		{
			name:    "birleşik biçim + hesap ıraksadı → gözlenen kazanır, kanıt söyler (a)",
			pathArg: "/p/state/events", computed: "/p/state/events_WRONG",
			observed: []seedObservedPath{{Host: "h2", Path: "/p/state/events"}},
			wantPath: "/p/state/events", wantEvidenceHas: "gözlenen kazandı",
		},
		{
			name:    "birleşik biçim + İKİ farklı yol → kurulum zaten bölünmüş, ENGEL",
			pathArg: "/p/state/events", computed: "/p/state/events",
			observed:        []seedObservedPath{{Host: "h1", Path: "/p/state/events"}, {Host: "h2", Path: "/p/01/events"}},
			wantBlockSubstr: "İKİ FARKLI ZK yolunda",
		},
		{
			name:    "makrolu biçim + hesap gözlenenleri üretiyor → geç (b)",
			pathArg: "/p/{shard}/spans", computed: "/p/01/spans",
			observed: []seedObservedPath{{Host: "h2", Path: "/p/02/spans"}},
			wantPath: "/p/01/spans", wantEvidenceHas: "gözlenen yolları ÜRETİYOR",
		},
		{
			// Kuşak yanlış seçilmiş: tablo aslında birleşik yolda, hesap
			// shard'lı yol üretiyor → uydurma. ENGEL.
			name:    "makrolu biçim + hesap kümeyle çelişiyor → ENGEL (b)",
			pathArg: "/p/{shard}/events", computed: "/p/01/events",
			observed:        []seedObservedPath{{Host: "h2", Path: "/p/state/events"}},
			wantBlockSubstr: "hesap kümeyle ÇELİŞİYOR",
		},
		{
			name:    "makrolu biçim + makrosu okunamayan host → doğrulanamadı notu, geç",
			pathArg: "/p/{shard}/events", computed: "/p/01/events",
			observed: []seedObservedPath{{Host: "bilinmeyen", Path: "/p/09/events"}},
			wantPath: "/p/01/events", wantEvidenceHas: "doğrulanamayan host",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, evidence, blocked := seedPathDecision("events", c.pathArg, c.computed, c.observed, macros)
			if c.wantBlockSubstr != "" {
				if blocked == "" {
					t.Fatalf("ENGEL bekleniyordu, yol %q döndü", path)
				}
				if !strings.Contains(blocked, c.wantBlockSubstr) {
					t.Errorf("engel %q, %q içermeli", blocked, c.wantBlockSubstr)
				}
				if path != "" {
					t.Errorf("engelde yol dönmemeli: %q", path)
				}
				return
			}
			if blocked != "" {
				t.Fatalf("beklenmeyen engel: %s", blocked)
			}
			if path != c.wantPath {
				t.Errorf("yol %q, want %q", path, c.wantPath)
			}
			if !strings.Contains(evidence, c.wantEvidenceHas) {
				t.Errorf("kanıt %q, %q içermeli", evidence, c.wantEvidenceHas)
			}
		})
	}
}

// TestTableObservedPaths — kanıt TÜM shard'lardan toplanır (kardeş shard'ın
// kaydı 1 numaralı kapının asıl girdisidir), düz/boş kayıtlar elenir.
func TestTableObservedPaths(t *testing.T) {
	tbl := &ReplicaTable{Table: "events", Shards: []ReplicaShard{
		{Shard: 1, Missing: []ReplicaMissingHost{{Host: "h1", Engine: "ReplacingMergeTree"}}},
		{Shard: 2, Replicas: []ReplicaState{
			{Host: "h3", Engine: "ReplicatedReplacingMergeTree", ZKPath: "/p/state/events"},
			{Host: "h2", Engine: "ReplicatedReplacingMergeTree", ZKPath: "/p/state/events"},
			{Host: "h4", Engine: "ReplacingMergeTree", ZKPath: "/x"}, // düz: kayıt değil
			{Host: "h5", Engine: "ReplicatedMergeTree"},              // yol yok: elenir
		}},
	}}
	got := tableObservedPaths(tbl)
	if len(got) != 2 || got[0].Host != "h2" || got[1].Host != "h3" {
		t.Fatalf("host'a göre sıralı iki kayıt bekleniyordu: %+v", got)
	}
	if tableObservedPaths(nil) != nil {
		t.Error("tablo yoksa kayıt da yok")
	}
}

// TestSeedGenerationEvidence — operatör Uygula'dan ÖNCE hangi kuşağın hangi
// KANITLA seçildiğini görür.
func TestSeedGenerationEvidence(t *testing.T) {
	st := seedGenerationEvidence("events", true, true, "taze veya göç SONRASI kurulum")
	if !strings.Contains(st, "BİRLEŞİK") || !strings.Contains(st, "taze veya göç") {
		t.Errorf("state + birleşik: %q", st)
	}
	if old := seedGenerationEvidence("events", true, false, "tablo kümede eski yolda (/p/01/events)"); !strings.Contains(old, "ESKİ") || !strings.Contains(old, "/p/01/events") {
		t.Errorf("state + eski: %q", old)
	}
	if tel := seedGenerationEvidence("spans_local", false, false, ""); !strings.Contains(tel, "shard'lı telemetri") {
		t.Errorf("telemetri: %q", tel)
	}
}

// TestSeedCanonicalArgsHighVolumeBlocked — (d) çıplak yüksek-hacim adı
// (`spans`): kanonik şekli `spans_local` (Replicated) + `spans` (Distributed).
// Böyle bir host tek-düğüm şeklinde kalmıştır; tablo düzeyi onarım YETMEZ.
// Mesaj neyin eksik olduğunu SÖYLEMELİ (operatör, v0.10.829).
func TestSeedCanonicalArgsHighVolumeBlocked(t *testing.T) {
	s := &Store{}
	for _, tbl := range []string{"spans", "logs", "metric_points"} {
		if !highVolumeTables[tbl] {
			t.Fatalf("%s yüksek hacim kaydında değil — test varsayımı bayat", tbl)
		}
		_, err := s.seedCanonicalArgs(tbl)
		if err == nil {
			t.Fatalf("%s bloke olmalı", tbl)
		}
		for _, want := range []string{tbl + "_local", "Distributed", "tek-düğüm", "terfi"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s mesajı %q içermeli: %s", tbl, want, err)
			}
		}
	}
	// Katalog boş bir Store'da (migrate koşmadı) her tablo fail-closed.
	if _, err := s.seedCanonicalArgs("events"); err == nil || !strings.Contains(err.Error(), "kanonik tanım yok") {
		t.Errorf("katalogsuz Store fail-closed olmalı: %v", err)
	}
}

// TestSeedCanonicalArgsHappyPath — v0.10.829 incelemesi (6): özelliğin ANA
// İDDİASI (kanonik katalog → adaptDDL → motor argümanları) mutlu yolda
// çivilenir. Girdi (canonicalTableDDL, stateObs, tablo) → çıktı (aile, yol
// argümanı, replika argümanı); çıkan FRAGMAN rewriteCanonicalSeedDDL'e
// beslenir (literal DDL yazmak, tam da test etmek istediğimiz üretimi atlardı).
func TestSeedCanonicalArgsHappyPath(t *testing.T) {
	const evDDL = "CREATE TABLE IF NOT EXISTS events (\n\t`id` String,\n\t`version` UInt64\n) ENGINE = ReplacingMergeTree(version)\nORDER BY (time, id)"
	// span_links: yüksek hacim kaydında → adaptDDL `_local` + Distributed üretir.
	const slDDL = "CREATE TABLE IF NOT EXISTS span_links (\n\t`trace_id` String\n) ENGINE = MergeTree()\nORDER BY trace_id"
	if !highVolumeTables["span_links"] {
		t.Skip("span_links yüksek hacim kaydında değil — fixture bayat")
	}
	catalogue := []string{evDDL, slDDL}
	newStore := func(obs stateObservation) *Store {
		return &Store{
			cfg:               config.CHConfig{ClusterName: "ch_cluster", ReplicaPath: "/ch/tbl"},
			stateObs:          obs,
			canonicalTableDDL: catalogue,
		}
	}
	cases := []struct {
		name                          string
		obs                           stateObservation
		table                         string
		wantFamily, wantPath, wantRep string
	}{
		{
			// Sondaj koşmadı → kuşak kuralı ESKİ yol (mevcut kuruluma dokunma).
			name: "state tablosu · sondaj yok → eski yol", obs: stateObservation{},
			table:      "events",
			wantFamily: "ReplicatedReplacingMergeTree", wantPath: "/ch/tbl/{shard}/events", wantRep: "{replica}",
		},
		{
			// Taze/göç sonrası kurulum → BİRLEŞİK state yolu, {shard}-{replica}.
			name:       "state tablosu · taze kurulum → birleşik yol",
			obs:        stateObservation{ok: true, paths: map[string]string{}},
			table:      "events",
			wantFamily: "ReplicatedReplacingMergeTree", wantPath: "/ch/tbl/state/events", wantRep: "{shard}-{replica}",
		},
		{
			// AYNI tablo, stateObs onu ESKİ yolda görmüş → komşularına katıl.
			name:       "state tablosu · stateObs eski yolda gördü → eski yol",
			obs:        stateObservation{ok: true, paths: map[string]string{"events": "/ch/tbl/01/events"}},
			table:      "events",
			wantFamily: "ReplicatedReplacingMergeTree", wantPath: "/ch/tbl/{shard}/events", wantRep: "{replica}",
		},
		{
			// `_local` türetimi: arama ÇIPLAK adla, eşleşme ÜRETİLEN adla —
			// Distributed sarmalayıcı fragmanı atlanmalı.
			name:       "yüksek hacim `_local` · Distributed fragman atlanır",
			obs:        stateObservation{ok: true, paths: map[string]string{}},
			table:      "span_links_local",
			wantFamily: "ReplicatedMergeTree", wantPath: "/ch/tbl/{shard}/span_links", wantRep: "{replica}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := newStore(c.obs).seedCanonicalArgs(c.table)
			if err != nil {
				t.Fatal(err)
			}
			if got.Family != c.wantFamily || got.PathArg != c.wantPath || got.ReplicaArg != c.wantRep {
				t.Fatalf("(%q, %q, %q), want (%q, %q, %q)", got.Family, got.PathArg, got.ReplicaArg, c.wantFamily, c.wantPath, c.wantRep)
			}
			// Fragman gerçekten bu tabloyu kuruyor ve hedefte koşacak hâle
			// getirilebiliyor mu? (Üretimden gelen metinle, literalle değil.)
			zk := strings.ReplaceAll(c.wantPath, "{shard}", "02")
			ddl, rerr := rewriteCanonicalSeedDDL(got.Frag, "coremetry", c.table, zk)
			if rerr != nil {
				t.Fatalf("fragman hedefe uyarlanamadı: %v (frag=%q)", rerr, got.Frag)
			}
			if !strings.HasPrefix(ddl, "CREATE TABLE `coremetry`.`"+c.table+"`") {
				t.Errorf("başlık: %q", ddl[:60])
			}
			if !strings.Contains(ddl, "ENGINE = "+c.wantFamily+"('"+zk+"', '"+c.wantRep+"'") {
				t.Errorf("motor argümanları: %q", ddl)
			}
			if strings.Contains(ddl, "{shard}/") {
				t.Errorf("yol literalleşmedi: %q", ddl)
			}
		})
	}
	// Katalogda olmayan ad (operatörün elle kurduğu tablo) → ENGEL, uydurma yok.
	if _, err := newStore(stateObservation{ok: true}).seedCanonicalArgs("operator_scratch"); err == nil || !strings.Contains(err.Error(), "kanonik tanım yok") {
		t.Errorf("katalog dışı tablo engellenmeli: %v", err)
	}
}

// TestRewriteCanonicalSeedDDL — boş-shard dalının kaynak DDL'i: kanonik
// adaptDDL fragmanı. Başlık nitelenir, IF NOT EXISTS + ON CLUSTER sökülür,
// ZK yolu GENİŞLETİLMİŞ literal olur; gövde aynen taşınır.
func TestRewriteCanonicalSeedDDL(t *testing.T) {
	const frag = "CREATE TABLE IF NOT EXISTS span_links_reverse ON CLUSTER `c1` (\n\t`trace_id` String,\n\t`version` UInt64\n) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/span_links_reverse', '{shard}-{replica}', version)\nORDER BY trace_id"
	out, err := rewriteCanonicalSeedDDL(frag, "coremetry", "span_links_reverse", "/clickhouse/tables/state/span_links_reverse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "CREATE TABLE `coremetry`.`span_links_reverse` (") {
		t.Errorf("başlık: %q", out[:70])
	}
	for _, bad := range []string{"ON CLUSTER", "IF NOT EXISTS"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s sökülmeliydi: %q", bad, out)
		}
	}
	if !strings.Contains(out, "ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/span_links_reverse', '{shard}-{replica}', version)") {
		t.Errorf("motor argümanları: %q", out)
	}
	if !strings.Contains(out, "`trace_id` String") || !strings.Contains(out, "ORDER BY trace_id") {
		t.Errorf("gövde taşınmadı: %q", out)
	}
	// {shard} taşıyan kanonik yol GENİŞLETİLMİŞ literalle değişir.
	legacy := strings.Replace(frag, "/clickhouse/tables/state/span_links_reverse", "/clickhouse/tables/{shard}/span_links_reverse", 1)
	out, err = rewriteCanonicalSeedDDL(legacy, "coremetry", "span_links_reverse", "/clickhouse/tables/02/span_links_reverse")
	if err != nil || strings.Contains(out, "{shard}/") {
		t.Errorf("makro yol literalleşmeli: %q err %v", out, err)
	}
	if !strings.Contains(out, "'{shard}-{replica}'") {
		t.Error("replika ŞABLONU korunmalı (her host kendi adını üretir)")
	}
	// Yanlış tablo / Replicated olmayan fragman reddedilir.
	if _, err := rewriteCanonicalSeedDDL(frag, "coremetry", "other", "/p"); err == nil {
		t.Error("başka tablonun fragmanı reddedilmeli")
	}
	plain := "CREATE TABLE IF NOT EXISTS t (`x` UInt8) ENGINE = MergeTree ORDER BY x"
	if _, err := rewriteCanonicalSeedDDL(plain, "db", "t", "/p/t"); err == nil {
		t.Error("düz motorlu fragman (tek düğüm kipi) reddedilmeli")
	}
}

// TestSeedEmptyStepShape — boş dalda adım sayısı TAM 2: CREATE + SYNC.
// `_fix` / ATTACH / EXCHANGE hiçbir ifadede geçmez, Temizle üretilmez.
func TestSeedEmptyStepShape(t *testing.T) {
	const frag = "CREATE TABLE IF NOT EXISTS span_links_reverse ON CLUSTER `c1` (\n\t`trace_id` String\n) ENGINE = ReplicatedMergeTree('/clickhouse/tables/state/span_links_reverse', '{shard}-{replica}')\nORDER BY trace_id"
	create, err := rewriteCanonicalSeedDDL(frag, "coremetry", "span_links_reverse", "/clickhouse/tables/state/span_links_reverse")
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{create, syncReplicaStmt("coremetry", "span_links_reverse")}
	if len(steps) != 2 {
		t.Fatalf("adım sayısı %d, want 2", len(steps))
	}
	if !strings.HasPrefix(steps[0], "CREATE TABLE `coremetry`.`span_links_reverse`") {
		t.Errorf("1. adım tabloyu KENDİ adıyla kurmalı (`_fix` değil): %q", steps[0])
	}
	if steps[1] != "SYSTEM SYNC REPLICA `coremetry`.`span_links_reverse` LIGHTWEIGHT" {
		t.Errorf("2. adım SYNC LIGHTWEIGHT olmalı: %q", steps[1])
	}
	for _, st := range steps {
		for _, bad := range []string{"_fix", "ATTACH PARTITION", "EXCHANGE TABLES", "ON CLUSTER"} {
			if strings.Contains(st, bad) {
				t.Errorf("boş dalda %q geçmemeli: %q", bad, st)
			}
		}
	}
	// Kip ayrımı: boş dal yerel veri TAŞIMAZ (Temizle de çıkmaz), düz dal taşır.
	if repairMovesLocalData(replicaRepairModeSeedEmpty) {
		t.Error("seed_empty yerel veri taşımaz")
	}
	if !isSeedMode(replicaRepairModeSeedEmpty) || !isSeedMode(replicaRepairModeSeed) {
		t.Error("iki dal da ilk-replika ailesinden: 1/1 doğrulama notu ikisinde de geçerli")
	}
	if isSeedMode(replicaRepairModePlain) || isSeedMode(replicaRepairModeMissing) {
		t.Error("820 kipleri ilk-replika ailesinde değil")
	}
}

// TestRepairMovesLocalData — ATTACH+EXCHANGE merdiveni hangi kiplerde koşar.
// `_fix` varlık kapısı, Cleanup metni, kolon/anahtar/indeks kapıları,
// partition listesi ve EXCHANGE adımı hep bu tek yüklemden okur.
func TestRepairMovesLocalData(t *testing.T) {
	for mode, want := range map[string]bool{
		replicaRepairModePlain:     true,
		replicaRepairModeSeed:      true,
		replicaRepairModeSeedEmpty: false, // boş dal: taşınacak veri yok
		replicaRepairModeMissing:   false,
		"":                         false,
		"Seed":                     false, // büyük harf kip DEĞİL (allowlist API'de)
	} {
		if got := repairMovesLocalData(mode); got != want {
			t.Errorf("%q → %v, want %v", mode, got, want)
		}
	}
}

// TestPlainEngineFamily — adaptDDL'in tanıdığı ailelerin AYNISI; Replicated
// motor "" döner (çapraz kontrol o yüzden "Replicated"+aile ile karşılaştırır).
func TestPlainEngineFamily(t *testing.T) {
	for in, want := range map[string]string{
		testSeedPlainDDL: "ReplacingMergeTree",
		"CREATE TABLE t (x UInt8) ENGINE = MergeTree() ORDER BY x":          "MergeTree",
		"CREATE TABLE t (x UInt8) ENGINE = AggregatingMergeTree ORDER BY x": "AggregatingMergeTree",
		"CREATE TABLE t (x UInt8) ENGINE = SummingMergeTree(v) ORDER BY x":  "SummingMergeTree",
		testPeerDDL: "", // zaten Replicated
		"CREATE TABLE t (x UInt8) ENGINE = Distributed(c, db, t, rand())":          "",
		"CREATE TABLE t (x UInt8) ENGINE = ReplicatedMergeTree('/p', '{replica}')": "",
	} {
		if got := plainEngineFamily(in); got != want {
			t.Errorf("%.40q → %q, want %q", in, got, want)
		}
	}
}

// TestRewriteSeedDDL — ad + motor değişir, GERİSİ bayt bayt aynı kalır.
func TestRewriteSeedDDL(t *testing.T) {
	const zk = "/clickhouse/tables/state/events"
	out, err := rewriteSeedDDL(testSeedPlainDDL, "coremetry", "events", "events_fix", zk, "{shard}-{replica}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "CREATE TABLE `coremetry`.`events_fix`\n(") {
		t.Errorf("başlık: %q", out[:60])
	}
	if !strings.Contains(out, "ENGINE = ReplicatedReplacingMergeTree('"+zk+"', '{shard}-{replica}', version)") {
		t.Errorf("motor: yol LİTERAL, replika ŞABLON, version korunur: %q", out)
	}
	if strings.Contains(out, "ON CLUSTER") {
		t.Errorf("ON CLUSTER kalmamalı: %q", out)
	}
	if !strings.Contains(out, "{replica}") {
		t.Error("{replica} makrosu korunmalı — her host kendi adını üretir")
	}
	// Kolon bloğu (başlık ile ENGINE arası) GİRDİYLE BAYT BAYT AYNI: `_fix`,
	// ATTACH kaynağının ikizidir, aksi hâlde checkStructureAndGetMergeTreeData
	// düşerdi. ORDER BY / SETTINGS kuyruğu da aynen taşınır.
	body := func(s string) string { return s[strings.Index(s, "\n("):strings.Index(s, "ENGINE =")] }
	if body(out) != body(testSeedPlainDDL) {
		t.Errorf("kolon bloğu değişmiş:\n%q\n%q", body(out), body(testSeedPlainDDL))
	}
	if tail := out[strings.Index(out, "ORDER BY"):]; tail != testSeedPlainDDL[strings.Index(testSeedPlainDDL, "ORDER BY"):] {
		t.Errorf("ORDER BY/SETTINGS kuyruğu değişmiş: %q", tail)
	}
	// Eski konvansiyon (shard'lı yol + {replica}) aynı gövdeden geçer.
	out, err = rewriteSeedDDL(testSeedPlainDDL, "coremetry", "events", "events_fix", "/clickhouse/tables/01/events", "{replica}")
	if err != nil || !strings.Contains(out, "ReplicatedReplacingMergeTree('/clickhouse/tables/01/events', '{replica}', version)") {
		t.Errorf("eski yol konvansiyonu: %q err %v", out, err)
	}
	// Argümansız motor: ZK argümanları tek başına parantezi doldurur.
	out, err = rewriteSeedDDL("CREATE TABLE db.t\n(\n    `x` UInt8\n)\nENGINE = MergeTree\nORDER BY x", "db", "t", "t_fix", "/p/t", "{replica}")
	if err != nil || !strings.Contains(out, "ENGINE = ReplicatedMergeTree('/p/t', '{replica}')") {
		t.Errorf("argümansız motor: %q err %v", out, err)
	}
}

// TestRewriteSeedDDLRejects — uydurma riski taşıyan girdiler REDDEDİLİR.
func TestRewriteSeedDDLRejects(t *testing.T) {
	cases := []struct{ name, ddl, want string }{
		{"başka tablo", "CREATE TABLE db.other\n(\n    `x` UInt8\n)\nENGINE = MergeTree\nORDER BY x", "bekleniyordu"},
		{"UUID cümlesi", "CREATE TABLE db.events UUID 'a-b'\n(\n    `x` UInt8\n)\nENGINE = MergeTree\nORDER BY x", "UUID"},
		{"zaten Replicated", strings.Replace(testPeerDDL, "alert_rules", "events", 1), "zaten Replicated"},
		{"tanınmayan motor", "CREATE TABLE db.events\n(\n    `x` UInt8\n)\nENGINE = Log", "tanınan"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := rewriteSeedDDL(c.ddl, "db", "events", "events_fix", "/p/events", "{replica}")
			if err == nil {
				t.Fatalf("hata bekleniyordu, çıktı: %q", out)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("hata %q, %q içermeli", err, c.want)
			}
		})
	}
}

// TestSeedReplicaCollisions — shard'ın öteki host'u AYNI replika adını
// üretiyorsa şimdi söylenir: ilk replika kurulur ama ötekinin "Onar"ı
// 'Replica already exists' ile düşerdi.
func TestSeedReplicaCollisions(t *testing.T) {
	cases := []struct {
		name       string
		replicaArg string
		selfName   string
		others     map[string]map[string]string
		want       []string
	}{
		{
			name: "ayrık makrolar · çakışma yok", replicaArg: "{replica}", selfName: "h1",
			others: map[string]map[string]string{"hostB": {"replica": "h2"}},
		},
		{
			name: "aynı {replica} · çakışma", replicaArg: "{replica}", selfName: "h1",
			others: map[string]map[string]string{"hostB": {"replica": "h1"}}, want: []string{"hostB"},
		},
		{
			name: "{shard}-{replica} · shard ayırıyor", replicaArg: "{shard}-{replica}", selfName: "01-h1",
			others: map[string]map[string]string{"hostB": {"shard": "02", "replica": "h1"}},
		},
		{
			name: "{shard}-{replica} · ikisi de aynı", replicaArg: "{shard}-{replica}", selfName: "01-h1",
			others: map[string]map[string]string{"hostB": {"shard": "01", "replica": "h1"}}, want: []string{"hostB"},
		},
		{
			name: "makro okunamadı · sessiz", replicaArg: "{replica}", selfName: "h1",
			others: map[string]map[string]string{"hostB": {}},
		},
		{
			name: "çok host · ada göre sıralı", replicaArg: "{replica}", selfName: "h1",
			others: map[string]map[string]string{
				"hostC": {"replica": "h1"}, "hostA": {"replica": "h1"}, "hostB": {"replica": "h9"},
			},
			want: []string{"hostA", "hostC"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := seedReplicaCollisions(c.replicaArg, c.selfName, c.others)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestSeedReplicaCountNote — 1 beklenen, 0 hata, >1 yalnız not (DDL koştu;
// başarılı onarımı okuma yorumuyla "başarısız" göstermek 820'nin sınıfı).
func TestSeedReplicaCountNote(t *testing.T) {
	for _, c := range []struct {
		total   int
		wantErr bool
		note    string
	}{
		{total: 1},
		{total: 0, wantErr: true},
		{total: -1, wantErr: true},
		{total: 2, note: "2 kayıtlı replika"},
	} {
		note, err := seedReplicaCountNote(c.total)
		if (err != nil) != c.wantErr {
			t.Errorf("total=%d err=%v, wantErr=%v", c.total, err, c.wantErr)
		}
		if c.note != "" && !strings.Contains(note, c.note) {
			t.Errorf("total=%d not %q, %q içermeli", c.total, note, c.note)
		}
		if c.note == "" && !c.wantErr && note != "" {
			t.Errorf("total=%d beklenen durumda not olmamalı: %q", c.total, note)
		}
	}
}

// TestSeedZKOwnershipGate — v0.10.829 incelemesi (2): seed dalında sahiplik
// kapısı FAIL-CLOSED. "Okuyamadım" ile "sahipsiz" aynı şey değildir.
func TestSeedZKOwnershipGate(t *testing.T) {
	const p = "/clickhouse/tables/state/events"
	cases := []struct {
		name      string
		names     []string
		err       error
		wantBlock string // "" = geç
		wantHasEv string
	}{
		{name: "ZNONODE → sahipsiz DOĞRULANDI, geç", err: errors.New("code: 999, Coordination::Exception: No node (ZNONODE)"), wantHasEv: "ZNONODE doğrulandı"},
		{name: "yol var, replika yok → geç", wantHasEv: "kayıtlı replika yok"},
		{name: "sahip VAR → engel, sahiplerin ADI ve çıkış yolu", names: []string{"01-r1", "02-r2"}, wantBlock: "01-r1, 02-r2"},
		{name: "Keeper okunamadı → ENGEL (fail-closed)", err: errors.New("read: connection reset by peer"), wantBlock: "DOĞRULANMIŞ yola kurulur"},
		{name: "zaman aşımı → ENGEL (fail-closed)", err: errors.New("code: 159, Timeout exceeded"), wantBlock: "DOĞRULANMIŞ yola kurulur"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, blocked := seedZKOwnershipGate(p, c.names, c.err)
			if c.wantBlock != "" {
				if blocked == "" {
					t.Fatalf("ENGEL bekleniyordu, kanıt: %q", ev)
				}
				if !strings.Contains(blocked, c.wantBlock) {
					t.Errorf("engel %q, %q içermeli", blocked, c.wantBlock)
				}
				return
			}
			if blocked != "" {
				t.Fatalf("beklenmeyen engel: %s", blocked)
			}
			if !strings.Contains(ev, c.wantHasEv) {
				t.Errorf("kanıt %q, %q içermeli", ev, c.wantHasEv)
			}
		})
	}
	// Sahip engeli gerçek çıkış yolunu söyler (kartın çizemediği düğmeyi DEĞİL).
	_, blocked := seedZKOwnershipGate(p, []string{"01-r1"}, nil)
	for _, want := range []string{"yeniden Ölç", "PAYLAŞILIYOR", "runbook"} {
		if !strings.Contains(blocked, want) {
			t.Errorf("sahip engeli %q içermeli: %s", want, blocked)
		}
	}
}

// TestZKNoNode — "yol henüz yok" seed kipinde BEKLENEN cevaptır, okuma
// hatası değil; ayırmazsak sağlıklı durum "ZK okunamadı" uyarısı üretirdi.
func TestZKNoNode(t *testing.T) {
	if zkNoNode(nil) {
		t.Error("nil hata değil")
	}
	for _, msg := range []string{
		"code: 999, message: Coordination::Exception: No node, path /clickhouse/tables/state/events/replicas",
		"ZNONODE",
	} {
		if !zkNoNode(errors.New(msg)) {
			t.Errorf("%q ZNONODE sayılmalı", msg)
		}
	}
	if zkNoNode(errors.New("read: connection reset by peer")) {
		t.Error("bağlantı hatası ZNONODE değil")
	}
}

// TestSeedStepShape — 0 / 1 / N partition için adım listesi: CREATE `_fix`,
// partition başına ATTACH, EXCHANGE, SYNC. attachStatements + syncReplicaStmt
// ile aynı gövde (plan bunları sırayla ekler).
func TestSeedStepShape(t *testing.T) {
	const zk = "/clickhouse/tables/state/events"
	create, err := rewriteSeedDDL(testSeedPlainDDL, "coremetry", "events", "events_fix", zk, "{shard}-{replica}")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		parts []ReplicaRepairPartition
	}{
		{name: "0 partition (boş tablo)"},
		{name: "1 partition", parts: []ReplicaRepairPartition{{ID: "202609"}}},
		{name: "N partition", parts: []ReplicaRepairPartition{{ID: "20260918"}, {ID: "20260919"}, {ID: "all"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			att, err := attachStatements("coremetry", "events", c.parts)
			if err != nil {
				t.Fatal(err)
			}
			steps := append([]string{create}, att...)
			steps = append(steps, "EXCHANGE TABLES `coremetry`.`events` AND `coremetry`.`events_fix`", syncReplicaStmt("coremetry", "events"))
			if len(steps) != len(c.parts)+3 {
				t.Fatalf("adım sayısı %d, want %d", len(steps), len(c.parts)+3)
			}
			if !strings.HasPrefix(steps[0], "CREATE TABLE `coremetry`.`events_fix`") {
				t.Errorf("1. adım CREATE `_fix` olmalı: %q", steps[0])
			}
			for i, p := range c.parts {
				want := "ALTER TABLE `coremetry`.`events_fix` ATTACH PARTITION ID '" + p.ID + "' FROM `coremetry`.`events`"
				if steps[1+i] != want {
					t.Errorf("ATTACH %d: %q, want %q", i, steps[1+i], want)
				}
			}
			if steps[len(steps)-2] != "EXCHANGE TABLES `coremetry`.`events` AND `coremetry`.`events_fix`" {
				t.Errorf("sondan 2. adım EXCHANGE olmalı: %q", steps[len(steps)-2])
			}
			if steps[len(steps)-1] != "SYSTEM SYNC REPLICA `coremetry`.`events` LIGHTWEIGHT" {
				t.Errorf("son adım SYNC LIGHTWEIGHT olmalı: %q", steps[len(steps)-1])
			}
			for _, st := range steps {
				if strings.Contains(st, "ON CLUSTER") {
					t.Errorf("koşulan ifadede ON CLUSTER: %q", st)
				}
			}
		})
	}
	// Beklenmeyen partition kimliği allowlist'e takılır (adım üretilmez).
	if _, err := attachStatements("coremetry", "events", []ReplicaRepairPartition{{ID: "'; DROP TABLE x --"}}); err == nil {
		t.Error("partition_id allowlist'i ısırmalı")
	}
}

// TestSpliceReplicatedEngineSharedBody — adaptDDL ile seed kipi AYNI gövdeyi
// kullanır (v0.9.1358: iki kopya ayrışır, kimse fark etmez). Replicated bir
// motoru eşlemez, yani ikinci çağrı çifte önek yazamaz.
func TestSpliceReplicatedEngineSharedBody(t *testing.T) {
	const args = "'/p/t', '{replica}'"
	for in, want := range map[string]string{
		"ENGINE = MergeTree()":                 "ENGINE = ReplicatedMergeTree('/p/t', '{replica}')",
		"ENGINE = MergeTree":                   "ENGINE = ReplicatedMergeTree('/p/t', '{replica}')",
		"ENGINE = ReplacingMergeTree(version)": "ENGINE = ReplicatedReplacingMergeTree('/p/t', '{replica}', version)",
		"ENGINE = AggregatingMergeTree":        "ENGINE = ReplicatedAggregatingMergeTree('/p/t', '{replica}')",
	} {
		if got := spliceReplicatedEngine(in, args); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	// İdempotent değil ama zararsız: Replicated motor eşleşmez.
	once := spliceReplicatedEngine("ENGINE = ReplacingMergeTree(version)", args)
	if twice := spliceReplicatedEngine(once, args); twice != once {
		t.Errorf("Replicated motor yeniden yazılmamalı: %q", twice)
	}
	// cluster.go'daki adaptDDL bu gövdeyi çağırıyor olmalı (tek gövde pini).
	b, err := os.ReadFile("cluster.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "spliceReplicatedEngine(rewritten, replicatedArgs(zkPrefix, name, unified))") {
		t.Error("adaptDDL motor takasını paylaşılan gövdeden yapmalı")
	}
}

// TestSeedSourcePins — seed kipi 820'nin güvenlik duruşunu DEVRALIR:
// node-yerel bağlantı, TAZE rapor, ON CLUSTER'sız ifadeler, kanonik
// katalogdan ZK yolu (uydurma yok), mevcut CleanupReplicaRepair.
func TestSeedSourcePins(t *testing.T) {
	b, err := os.ReadFile("replica_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"s.seedCanonicalArgs(", "s.adaptDDL(ddl)", "tableDDLByName(s.canonicalTableDDL",
		"rewriteSeedDDL(", "spliceReplicatedEngine(", "seedReplicaCollisions(", "seedReplicaCountNote(",
		"repairMovesLocalData(plan.Mode)", "plan.peerAddr = plan.targetAddr",
		// v0.10.829 boş-shard dalı: kaynak kanonik fragman, SHOW CREATE
		// atlanır (tablo yok), tek CREATE + SYNC.
		"rewriteCanonicalSeedDDL(", "shardPlainSeeder(", "if !seedEmpty {",
		// İnceleme (1,2,3,4): yol kararı kardeş shard'lara karşı doğrulanır,
		// ZK sahipliği fail-closed, disk payı kipe göre, seed'de yapısal
		// kapılar atlanır.
		"seedPathDecision(", "tableObservedPaths(tbl)", "seedGenerationEvidence(",
		"seedZKOwnershipGate(", "repairHeadroomNeed(plan.Mode", "if isSeedMode(plan.Mode) {",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	// ZK yolu UYDURULMAZ: seed dalında elle kurulan bir yol öneki olmamalı.
	for _, m := range regexp.MustCompile(`"/clickhouse/tables[^"\n]*"`).FindAllString(src, -1) {
		t.Errorf("elle yazılmış ZK yolu: %s (kanonik katalog + replicatedArgs kullan)", m)
	}
	// APPLY her zaman TAZE plan kurar (FE'nin gönderdiği kipe göre yeniden ölçer).
	apply := src[strings.Index(src, "func (s *Store) ApplyReplicaRepair"):]
	if !strings.Contains(apply[:strings.Index(apply, "conn.Exec(")], "s.PlanReplicaRepair(ctx, req)") {
		t.Error("APPLY Exec'ten önce planı TAZE kurmalı")
	}
	// Temizlik MEVCUT gövdeyi kullanır: seed kipi için ikinci bir DROP yolu yok.
	if strings.Count(src, "func (s *Store) CleanupReplicaRepair") != 1 {
		t.Error("tek temizlik gövdesi (seed `_fix`'i de aynı yoldan düşer)")
	}
	if strings.Count(src, "DROP TABLE IF EXISTS ") != 2 { // plan.Cleanup metni + Cleanup adımı
		t.Error("DROP ifadesi yalnız temizlik yolunda üretilmeli")
	}
}
