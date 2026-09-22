package chstore

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/migrations"
)

// table_catalog_test.go — v0.10.846.
//
// Operatör-bildirimli (prod, 2026-09-20): replika tutarlılığı kartı
// `feedbacks` satırında "eksik replika" diyor ve "İlk replikayı kur" düğmesi
// basıyordu. `feedbacks` ürünün v0.8.240'ta KALDIRDIĞI bir tablo; kart ise
// envanterini CANLI system.tables'tan alıp ürünün o tabloyu BEKLEYİP
// beklemediğine hiç bakmıyordu.
//
// Buradaki testler kararın GERÇEK gövdesinden (shardDecision) geçer —
// kaynak-metin pini değil.

// canonicalCatalogue — testlerin kullandığı minik "ürün kataloğu".
var canonicalCatalogue = []string{
	"CREATE TABLE IF NOT EXISTS spans (trace_id String) ENGINE = MergeTree ORDER BY trace_id",
	"CREATE TABLE IF NOT EXISTS problems (id String) ENGINE = ReplacingMergeTree(version) ORDER BY id",
	"CREATE TABLE IF NOT EXISTS saved_views (page String) ENGINE = ReplacingMergeTree(version) ORDER BY page",
}

func testManaged(t *testing.T) map[string]bool {
	t.Helper()
	m := catalogTableNames(canonicalCatalogue)
	for n := range migrationTableNames() {
		m[n] = true
	}
	return m
}

func TestCatalogBaseName(t *testing.T) {
	cases := map[string]string{
		"spans":                   "spans",
		"spans_local":             "spans",
		"spans_local_fix":         "spans",
		"problems_old":            "problems",
		"problems_unified":        "problems",
		"feedbacks":               "feedbacks",
		".inner_id.abc":           ".inner_id.abc", // önek soyulmaz; sınıflandırma zaten dokunmaz
		"rollup_metrics_1m_local": "rollup_metrics_1m",
		// v0.10.846 incelemesi — 0010 göçünün CANLI yedekleri. Tanınmasalardı
		// "katalog dışı" sayılır ve kapsama ölçüsü o satırlarda susardı.
		"anomaly_events_repart":      "anomaly_events",
		"anomaly_events_pathfix":     "anomaly_events",
		"anomaly_events_pathfix_old": "anomaly_events",
	}
	for in, want := range cases {
		if got := catalogBaseName(in); got != want {
			t.Errorf("catalogBaseName(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

// TestMigrationTableNames — gömülü migration'lar kataloğa GİRİYOR.
// Bu satır olmadan rollup/entity/rollouts aileleri "ürün kataloğunda yok"
// diye etiketlenirdi ve gerçek bir eksik replika sinyali kaybolurdu.
func TestMigrationTableNames(t *testing.T) {
	names := migrationTableNames()
	if len(names) == 0 {
		t.Fatal("gömülü migration taraması boş — katalog sınıflandırması tamamen kapanır")
	}
	for _, want := range []string{
		"rollup_spans_narrow_1m", "rollup_spans_narrow_1m_local",
		"rollup_metrics_5m", "rollup_metrics_route_1h_local",
		"rollup_spans_wide_5m", // 0002 gömülü DEĞİL ama dosya repoda — ad taraması hepsini okur
		"entities", "entity_relations", "workload_rollouts",
	} {
		if !names[want] {
			t.Errorf("migration kataloğunda %q yok", want)
		}
	}
	// `concat('CREATE TABLE ', database, …)` (0009/0010) kaçak ad üretmemeli.
	for n := range names {
		if strings.ContainsAny(n, "' ,") {
			t.Errorf("kaçak ad yakalandı: %q", n)
		}
	}
}

// TestCatalogVerdictFor — tablo testi: ad → katalog kararı.
func TestCatalogVerdictFor(t *testing.T) {
	managed := testManaged(t)
	cases := []struct {
		name      string
		table     string
		managed   map[string]bool
		want      string
		wantSince string
	}{
		{"ürünün tablosu", "spans", managed, "", ""},
		{"ürünün shard-yerel adı", "spans_local", managed, "", ""},
		{"sihirbazın geçici tablosu", "spans_local_fix", managed, "", ""},
		{"göçün eski tablosu", "problems_old", managed, "", ""},
		{"migration tablosu", "rollup_metrics_5m_local", managed, "", ""},
		{"KALDIRILMIŞ tablo", "feedbacks", managed, ReplicaRemoved, "v0.8.240"},
		{"operatörün kendi tablosu", "musteri_deneme", managed, ReplicaUnmanaged, ""},
		// MV iç tablosu: adı uuid'den (Ordinary DB'de view adından) doğar,
		// hiçbir katalogda geçmez. v0.10.824/830/833 yollarının
		// sahipliğinde kalmalı — ikisi de sınıflandırmadan MUAF.
		{"MV iç tablosu dokunulmaz (Atomic)", ".inner_id.7f3c-0000", managed, "", ""},
		{"MV iç tablosu dokunulmaz (Ordinary)", ".inner.service_summary_5m", managed, "", ""},
		// Defter araması TÜREV ada yayılmaz: kaldırma yalnız çıplak adı
		// düşürür, o yüzden `feedbacks_old` için "bir sonraki boot temizler"
		// demek tutulamayacak bir söz olurdu (v0.10.846 incelemesi).
		{"kaldırılmışın türev adı kalıntı DEĞİL", "feedbacks_old", managed, ReplicaUnmanaged, ""},
		// FAIL-OPEN: katalog okunamadıysa hiçbir şey "yönetilmiyor" sayılmaz.
		{"katalog yok — bilinmeyen ad yine de yönetiliyor sayılır", "musteri_deneme", nil, "", ""},
		// …ama defter DERLEME ANINDA sabittir ve katalogsuz da çalışır.
		{"katalog yok — defter yine ısırır", "feedbacks", nil, ReplicaRemoved, "v0.8.240"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, since := catalogVerdictFor(c.table, c.managed)
			if got != c.want || since != c.wantSince {
				t.Fatalf("catalogVerdictFor(%q) = (%q, %q), beklenen (%q, %q)", c.table, got, since, c.want, c.wantSince)
			}
		})
	}
}

func TestCatalogShardVerdict(t *testing.T) {
	cases := []struct {
		name      string
		class     string
		replicas  int
		want      string
		wantOver  bool
		wantCover bool // kapsama ölçüsü koşmalı mı
	}{
		{"yönetilen tablo — bugünkü yol", "", 2, "", false, true},
		{"kaldırılmış — replikası olsa da kalıntı", ReplicaRemoved, 2, ReplicaRemoved, true, false},
		{"kaldırılmış — hiç replika yok", ReplicaRemoved, 0, ReplicaRemoved, true, false},
		{"yönetilmeyen — kayıt yok, karar yok", ReplicaUnmanaged, 0, ReplicaUnmanaged, true, false},
		{"yönetilmeyen — gerçek kayıt var, ölçüm durur", ReplicaUnmanaged, 3, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, over := catalogShardVerdict(c.class, c.replicas)
			if got != c.want || over != c.wantOver {
				t.Fatalf("catalogShardVerdict(%q, %d) = (%q, %v), beklenen (%q, %v)", c.class, c.replicas, got, over, c.want, c.wantOver)
			}
			if cover := !catalogSuppressesCoverage(c.class); cover != c.wantCover {
				t.Fatalf("kapsama koşma kararı %v, beklenen %v", cover, c.wantCover)
			}
		})
	}
}

// TestCatalogVerdictsRankBelowProblems — katalog kararları kartın başlığını
// kırmızıya boyamamalı: FE "sorunlu" saymayı `lagging` eşiğinden başlatır.
func TestCatalogVerdictsRankBelowProblems(t *testing.T) {
	for _, v := range []string{ReplicaRemoved, ReplicaUnmanaged} {
		r, ok := replicaVerdictRank[v]
		if !ok {
			t.Fatalf("%s sıralamada yok", v)
		}
		if r >= replicaVerdictRank[ReplicaLagging] {
			t.Errorf("%s sıralaması (%d) `lagging` (%d) eşiğinin üstünde — kart bunu 'sorunlu' sayar",
				v, r, replicaVerdictRank[ReplicaLagging])
		}
		if r <= replicaVerdictRank[ReplicaOK] {
			t.Errorf("%s sıralaması (%d) `ok` ile aynı/altında — 'tutarlı' da değil, ölçü uygulanmadı", v, r)
		}
	}
}

// ── GERÇEK KARAR GÖVDESİ ────────────────────────────────────────────────
//
// shardDecision, ReplicaConsistency'nin ÇAĞIRDIĞI gövdedir: ölçüm
// (replicaVerdict) → kapsama (shardCoverage/mergeCoverage) → katalog kararı.
// Aşağıdaki tablo testi prod'daki tam şekli taşır: `feedbacks` shard 1'in iki
// host'unda da yok, shard 2'de iki Replicated replika var.

func repState(host, path string) ReplicaState {
	return ReplicaState{Host: host, ZKPath: path, TotalReplicas: 2, ActiveReplicas: 2, Rows: map[string]uint64{}, Engine: "ReplicatedMergeTree"}
}

func TestShardDecisionCatalogAware(t *testing.T) {
	managed := testManaged(t)
	shard1Hosts := []string{"ch-01", "ch-02"}
	shard2Hosts := []string{"ch-03", "ch-04"}
	twoReplicas := []ReplicaState{repState("ch-03", "/clickhouse/tables/2/feedbacks"), repState("ch-04", "/clickhouse/tables/2/feedbacks")}

	cases := []struct {
		name        string
		table       string
		shard       int
		expected    []string
		rs          []ReplicaState
		engines     map[string]string
		wantVerdict string
		wantMissing int
		hintHas     string
	}{
		{
			// Bugünkü davranış KORUNUYOR: ürünün tablosu bir host'ta yoksa
			// hâlâ eksik replika (ve onarım düğmesinin dayanağı `missing`).
			name: "yönetilen tablo — eksik host hâlâ kırmızı", table: "problems", shard: 1,
			expected: shard1Hosts, rs: []ReplicaState{repState("ch-01", "/clickhouse/tables/1/problems")},
			engines:     map[string]string{"ch-01": "ReplicatedMergeTree"},
			wantVerdict: ReplicaMissing, wantMissing: 1, hintHas: "ch-02 (tablo yok)",
		},
		{
			// PROD ŞEKLİ — shard 1: kaldırma burada TAMAMLANMIŞ.
			name: "kaldırılmış tablo — iki host'ta da yok", table: "feedbacks", shard: 1,
			expected: shard1Hosts, rs: []ReplicaState{}, engines: nil,
			wantVerdict: ReplicaRemoved, wantMissing: 0, hintHas: "KALDIRDI (v0.8.240)",
		},
		{
			// PROD ŞEKLİ — shard 2: kaldırma burada HİÇ koşmamış.
			name: "kaldırılmış tablo — öteki shard'da hâlâ duruyor", table: "feedbacks", shard: 2,
			expected: shard2Hosts, rs: twoReplicas,
			engines:     map[string]string{"ch-03": "ReplicatedMergeTree", "ch-04": "ReplicatedMergeTree"},
			wantVerdict: ReplicaRemoved, wantMissing: 0, hintHas: "bir sonraki boot",
		},
		{
			name: "yönetilmeyen tablo — kapsama kararı verilmez", table: "musteri_deneme", shard: 1,
			expected: shard1Hosts, rs: []ReplicaState{}, engines: map[string]string{"ch-01": "MergeTree"},
			wantVerdict: ReplicaUnmanaged, wantMissing: 0, hintHas: "ürün kataloğunda YOK",
		},
		{
			// Yönetilmeyen ama GERÇEK Replicated kaydı olan tablo: ölçüm
			// geçerlidir ve durur — yalnız kapsama varsayımı düşer.
			name: "yönetilmeyen tablo — kayıtlı replikalar ölçülür", table: "musteri_deneme", shard: 2,
			expected: shard2Hosts, rs: twoReplicas,
			engines:     map[string]string{"ch-03": "ReplicatedMergeTree", "ch-04": "ReplicatedMergeTree"},
			wantVerdict: ReplicaOK, wantMissing: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cls, since := catalogVerdictFor(c.table, managed)
			rsh, _ := shardDecision(c.table, cls, since, c.shard, c.expected, c.rs, c.engines)
			if rsh.Verdict != c.wantVerdict {
				t.Fatalf("%s/shard %d kararı %q, beklenen %q (ipucu: %s)", c.table, c.shard, rsh.Verdict, c.wantVerdict, rsh.Hint)
			}
			if len(rsh.Missing) != c.wantMissing {
				t.Errorf("missing %d satır, beklenen %d (%+v) — FE onarım/seed düğmeleri bu listeden çizilir", len(rsh.Missing), c.wantMissing, rsh.Missing)
			}
			if c.hintHas != "" && !strings.Contains(rsh.Hint, c.hintHas) {
				t.Errorf("ipucu %q içermiyor: %s", c.hintHas, rsh.Hint)
			}
			// Katalog dışı satır ASLA yapısal kırmızı olmamalı: onarım
			// sihirbazının uygunluk kapısı tam olarak bu iki karara bakıyor.
			if cls != "" && (rsh.Verdict == ReplicaMissing || rsh.Verdict == ReplicaNotReplicated) {
				t.Errorf("katalog dışı tablo yapısal kırmızı verdi (%s) — operatöre var olmayan bir iş gösterilir", rsh.Verdict)
			}
		})
	}
}

// TestShardDecisionUnmappedHostStillCarriesCatalogVerdict — v0.10.846
// incelemesi: `shard < 0` (host shard'a eşlenemedi) dalı katalog kararını
// tamamen atlıyordu. Eşlenemeyen bir host'taki KALINTI da kalıntıdır;
// "eşlenemedi" demek operatöre yanlış işi gösterir.
func TestShardDecisionUnmappedHostStillCarriesCatalogVerdict(t *testing.T) {
	managed := testManaged(t)
	// Kontrol: yönetilen bir tablo eşlenemeyen host'ta hâlâ "unmapped".
	cls, since := catalogVerdictFor("problems", managed)
	if rsh, _ := shardDecision("problems", cls, since, -1, nil, []ReplicaState{}, nil); rsh.Verdict != ReplicaUnmapped {
		t.Fatalf("yönetilen tablo/eşlenemeyen host kararı %q, beklenen %q", rsh.Verdict, ReplicaUnmapped)
	}
	cls, since = catalogVerdictFor("feedbacks", managed)
	rsh, _ := shardDecision("feedbacks", cls, since, -1, nil, []ReplicaState{}, nil)
	if rsh.Verdict != ReplicaRemoved {
		t.Fatalf("kaldırılmış tablo/eşlenemeyen host kararı %q, beklenen %q", rsh.Verdict, ReplicaRemoved)
	}
	if !strings.Contains(rsh.Hint, "KALDIRDI (v0.8.240)") {
		t.Errorf("ipucu katalog gerekçesini taşımıyor: %s", rsh.Hint)
	}
}

// TestShardDecisionKeepsCoverageForProductTables — mutasyon kapısı (ii)'nin
// öteki yarısı: katalog kontrolü kaldırılırsa yukarıdaki test kırmızı olur;
// kapsama ölçüsü TAMAMEN kapatılırsa bu test kırmızı olur. İkisi birlikte
// "yalnız katalog dışı tabloda sus" sözleşmesini çiviler.
func TestShardDecisionKeepsCoverageForProductTables(t *testing.T) {
	managed := testManaged(t)
	cls, since := catalogVerdictFor("saved_views", managed)
	if cls != "" {
		t.Fatalf("saved_views yönetilen bir tablo olmalı, karar %q", cls)
	}
	rsh, _ := shardDecision("saved_views", cls, since, 1, []string{"ch-01", "ch-02"},
		[]ReplicaState{}, map[string]string{"ch-01": "MergeTree"})
	if rsh.Verdict != ReplicaNotReplicated {
		t.Fatalf("karar %q, beklenen %q — ürünün tablosunda kapsama ölçüsü koşmalı", rsh.Verdict, ReplicaNotReplicated)
	}
	if len(rsh.Missing) != 2 {
		t.Fatalf("missing %d, beklenen 2 (ch-01 düz + ch-02 yok)", len(rsh.Missing))
	}
}

// TestPlanReplicaRepairCatalogGate — SUNUCU uygunluğu (düğme gizlemek yetmez).
//
// Onarım ve "İlk replikayı kur" AYNI uçtan geçer (mode alanı), yani tek kapı
// ikisini de kapatır. Kapı taze raporu OKUMADAN ÖNCE durduğu için bağlantısız
// ölçülebiliyor — ve bu kasıtlı: karar yalnız ADA ve katalogda bağlı.
//
// Üçüncü vaka KONTROL: ürünün tablosu kapıdan GEÇER ve bir sonraki adımda
// ("küme kipi değil") durur. Olmasaydı "hepsini reddediyor" da bu testi
// yeşil yapardı.
func TestPlanReplicaRepairCatalogGate(t *testing.T) {
	s := &Store{canonicalTableDDL: canonicalCatalogue}
	cases := []struct {
		name, table, wantErrHas string
	}{
		{"kaldırılmış tablo", "feedbacks", "KALDIRDIĞI"},
		{"kaldırılmış tablo sürümü anar", "feedbacks", "v0.8.240"},
		{"yönetilmeyen tablo", "musteri_deneme", "kataloğunda yok"},
		{"ürünün tablosu kapıdan geçer", "problems", "küme kipi değil"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, mode := range []string{"", "seed"} {
				_, err := s.PlanReplicaRepair(t.Context(), ReplicaRepairRequest{Table: c.table, Shard: 1, Host: "ch-01", Mode: mode})
				if err == nil {
					t.Fatalf("%s (mode=%q): hata beklenirken nil döndü", c.table, mode)
				}
				if !strings.Contains(err.Error(), c.wantErrHas) {
					t.Fatalf("%s (mode=%q): %q içermiyor: %v", c.table, mode, c.wantErrHas, err)
				}
			}
		})
	}
}

// ── KATALOG BÜTÜNLÜĞÜ ───────────────────────────────────────────────────
//
// Katalog bir SUSTURUCU: katalog dışı sayılan tabloda kapsama ölçüsü HİÇ
// koşmuyor. Dolayısıyla kataloğun HER EKSİĞİ gerçek bir eksik replikayı
// gizler. v0.10.846 incelemesinin KRİTİK bulgusu tam buydu: katalog ürünün
// 21 kanonik MV'sini hiç tanımıyordu (`s.canonicalTableDDL` yalnız `tables`
// dilimi), yani MV replika eksikliği — v0.10.818-835 programının ölçtüğü
// şeyin ta kendisi — sessizce ölçülmez olmuştu.
//
// Bu test kataloğun gelecekte de sessizce eksilmesini yakalar: ürünün
// ÜRETTİĞİ her ad (boot `tables` + `mvs` + migrations/*.sql + çalışma
// zamanı türevleri) katalogda BULUNMALI, bulunmayanlar LİSTELENEREK düşer.

// realCatalogue — ÜRÜNÜN GERÇEK boot tablo kataloğu. canonicalTables
// v0.10.846'da migrate() gövdesinden saf bir fonksiyona çıkarıldı (emsal:
// canonicalMVs), böylece testler kataloğu AST ile ayıklamak yerine üretenin
// kendisini çağırıyor. Saklama günleri kararı etkilemez (yalnız TTL metni).
func realCatalogue() []string { return canonicalTables(30, 30, 7) }

func TestProductCatalogueCoversEveryProducedName(t *testing.T) {
	boot := realCatalogue()
	s := &Store{canonicalTableDDL: boot}
	managed := s.productTableNames()
	if len(managed) == 0 {
		t.Fatal("katalog boş — sınıflandırma tamamen kapalı olurdu")
	}

	produced := map[string]string{} // ad → hangi kaynak
	add := func(src string, names ...string) {
		for _, n := range names {
			if n != "" {
				produced[n] = src
			}
		}
	}
	for n := range catalogTableNames(boot) {
		add("boot `tables` dilimi", n)
	}
	add("boot `mvs` dilimi (canonicalMVs)", canonicalMVNames()...)
	for n := range migrationTableNames() {
		add("migrations/*.sql", n)
	}
	// Koşullu MV'ler: migrate `mvs`'e append ediyor; adları 0011/0012'de de
	// geçiyor, yani kapsanmaları migration kaynağına bağlı — açıkça ara.
	add("koşullu MV (entity/rollouts)", "entity_seen_1m", "entity_seen_5m", "workload_revision_activity_1m")
	// Çalışma zamanı türevleri: adaptDDL'in `_local`'i, onarım sihirbazının
	// `_fix`'i, 0009/0010 göçünün canlı yedekleri.
	for hv := range highVolumeTables {
		add("adaptDDL `_local` (küme kipi)", hv+"_local")
	}
	add("replica_repair `_fix`", "spans_local_fix", "problems_fix")
	add("0009/0010 göç yedekleri", "problems_old", "problems_unified", "anomaly_events_repart",
		"anomaly_events_pathfix", "anomaly_events_pathfix_old")
	// CH'nin ürettiği MV iç tabloları — iki veritabanı motoru, iki yazım.
	add("CH iç tablosu (Atomic/Ordinary)", ".inner_id.7f3c-0000", ".inner.service_summary_5m")

	var missing []string
	for name, src := range produced {
		if v, _ := catalogVerdictFor(name, managed); v != "" {
			missing = append(missing, name+" ("+src+" → "+v+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("ürünün ÜRETTİĞİ %d ad katalogda bulunamadı — bu adların satırında kapsama ölçüsü "+
			"SUSTURULUR ve gerçek bir eksik replika gizlenir:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// migration0010DerivedRe — 0010'un ürettiği TÜREV adlar. İki incelik:
//   - `pathfix_old` alternasyonda ÖNCE gelir, yoksa `old` kolu
//     `anomaly_events_pathfix` + `_old` diye böler;
//   - taban HARFLE başlar ([a-z], `_` değil), yoksa dosyadaki prose
//     mention'ı (“ `_pathfix_old` “) tablo adı sanılır.
var migration0010DerivedRe = regexp.MustCompile(`\b([a-z][a-z0-9_]*?)_(pathfix_old|pathfix|repart|unified|old)\b`)

// TestMigration0010DerivedNamesStayMeasured — v0.10.846 incelemesi, ikinci
// yarı. 0010'un runbook'u `…_pathfix_old`'u BİLEREK bırakıyor ve o tablo
// `problems` / `anomaly_events`'in TEK yedeği. Göç penceresinde o yedeğin
// replika kapsaması ölçülmüyor olamaz — katalog bir susturucu olduğu için
// tanınmayan bir türev sessizce "katalog dışı" olur ve ölçü kapanır.
//
// Girdi GERÇEK dosyadan okunuyor: 0010'a yeni bir türev aile eklenirse bu
// test onu kendiliğinden kapsar.
func TestMigration0010DerivedNamesStayMeasured(t *testing.T) {
	raw, err := migrations.AllSQL.ReadFile("0010_state_repartition.sql")
	if err != nil {
		t.Fatalf("0010 okunamadı: %v", err)
	}
	s := &Store{canonicalTableDDL: realCatalogue()}
	managed := s.productTableNames()

	seen := map[string]bool{}
	for _, m := range migration0010DerivedRe.FindAllStringSubmatch(string(raw), -1) {
		seen[m[0]] = true
	}
	if len(seen) == 0 {
		t.Fatal("0010'da türev ad bulunamadı — regex ya da dosya değişmiş, test ölçmüyor")
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	var lost []string
	for _, n := range names {
		if !catalogDerivedName(n) {
			lost = append(lost, n+" (türev sayılmadı)")
			continue
		}
		if v, _ := catalogVerdictFor(n, managed); v != "" {
			lost = append(lost, n+" ("+v+" → kapsama ölçüsü susar)")
			continue
		}
		// Aynı türev, state ZK yolu probe'undan da ELENMELİ: eski (shard'lı)
		// yolda duran bir yedek kuşak kararını geriye kilitler.
		if stateProbeTable(n) {
			lost = append(lost, n+" (stateProbeTable onu kanonik state tablosu sanıyor)")
		}
	}
	if len(lost) > 0 {
		t.Fatalf("0010'un ürettiği %d/%d türev ad korumasız:\n  %s", len(lost), len(names), strings.Join(lost, "\n  "))
	}
	// En az üç aile gerçekten görüldü mü (regex tek aileye çökerse test
	// yeşil kalan bir süs olurdu).
	for _, want := range []string{"_repart", "_pathfix", "_pathfix_old"} {
		found := false
		for _, n := range names {
			if strings.HasSuffix(n, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("0010 taramasında `%s` ailesi hiç görünmedi (%v)", want, names)
		}
	}
}

// TestLedgerCatalogueDecisionOrder — kesişim OLSAYDI hangi karar kazanır?
// Defter (kaldırma niyeti kataloğun yaşattığı addan daha yenidir). Sıra
// örtük kalmasın diye AÇIKÇA çivileniyor; kesişimin kendisi ayrı bir KAPI
// (removed_tables_test.go / TestLedgerNeverNamesALivingTable).
func TestLedgerCatalogueDecisionOrder(t *testing.T) {
	withLedger(t, removedTable{Name: "ikili_ad", Since: "v0.9.1", Why: "test", Kind: removedPlainTable})
	both := map[string]bool{"ikili_ad": true}
	if v, since := catalogVerdictFor("ikili_ad", both); v != ReplicaRemoved || since != "v0.9.1" {
		t.Fatalf("kesişimde karar (%q, %q) — defter kazanmalı", v, since)
	}
}

// TestCatalogRepairReject — sunucu uygunluk metni her iki sınıfı da adlandırır.
func TestCatalogRepairReject(t *testing.T) {
	if got := catalogRepairReject("feedbacks", ReplicaRemoved, "v0.8.240"); !strings.Contains(got, "KALDIRDIĞI") || !strings.Contains(got, "v0.8.240") {
		t.Errorf("kaldırılmış tablo reddi sürümü anmıyor: %s", got)
	}
	if got := catalogRepairReject("musteri_deneme", ReplicaUnmanaged, ""); !strings.Contains(got, "kataloğunda yok") {
		t.Errorf("yönetilmeyen tablo reddi: %s", got)
	}
	if got := catalogRepairReject("spans", "", ""); got != "" {
		t.Errorf("yönetilen tablo için ret metni olmamalı: %s", got)
	}
}
