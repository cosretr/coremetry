package chstore

// mv_coverage_test.go — v0.10.825 MV kapsaması (ok | plain | dangling |
// missing) ve host'a özel yeniden kurulum.
//
// Sözleşme: operatörün test kümesinde kanonik bir MV bir host'ta DÜZ iç
// tabloyla, öteki host'ta HİÇ YOK duruyordu; v0.10.762 kartı ikisini de
// görmüyordu ("sarkan MV yok"). Bu testler dört durumun da ölçüldüğünü,
// ok satırlarının çıktıda KALDIĞINI (kart "N MV × M host sağlıklı"
// diyebilsin), düz bir eşin "Eşten kur" adayı OLMADIĞINI ve onarımın tek
// gövdeden koştuğunu çiviler.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	// mvTestView — VIEW'ın uuid'si: iç tablonun ADI bundan doğar.
	mvTestView = "11111111-1111-1111-1111-111111111111"
	// mvTestInner — iç tablonun KENDİ nesne uuid'si (`TO INNER UUID`).
	// v0.10.832: ADA girmez, yalnız uuid KOLONUNDA durur.
	mvTestInner = "22222222-2222-2222-2222-222222222222"
)

// combinedMVRow — Atomic DB'de combined MV satırı, ayar AÇIK biçimi (metin
// hem view uuid'sini hem nesne uuid'sini taşır).
func combinedMVRow(host, name string) mvTableRow {
	return mvTableRow{Host: host, Name: name, UUID: mvTestView, Engine: "MaterializedView",
		CreateQuery: "CREATE MATERIALIZED VIEW coremetry." + name + " UUID '" + mvTestView +
			"' TO INNER UUID '" + mvTestInner + "' (x Int) ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1"}
}

// innerRow — CANLI iç tablo: adı VIEW uuid'sinden, uuid KOLONU nesne uuid'si
// (gerçek system.tables satırı böyle gelir).
func innerRow(host, engine string) mvTableRow {
	return mvTableRow{Host: host, Name: innerTablePrefix + mvTestView, UUID: mvTestInner, Engine: engine}
}

func stateOf(t *testing.T, got []MVHostState, view, host string) MVHostState {
	t.Helper()
	for _, s := range got {
		if s.View == view && s.Host == host {
			return s
		}
	}
	t.Fatalf("%s@%s satırı yok: %+v", view, host, got)
	return MVHostState{}
}

// TestMVCoveragePeerOutsideDangling — v0.10.835: eş adayı ARTIK yalnız
// `dangling` satırında hesaplanmıyor.
//
// NEDEN (835 incelemesi, davranışsal pin): hedef uuid onarımı ADI VAR OLAN
// bir hücrede koşar (şekil-1'in durumu `ok`'tur) ve tarihçeyi eşten
// getirebilmek için aynı eşe ihtiyaç duyar. Eş yalnız dangling dalında
// hesaplanırken o hücrenin PeerHost'u BOŞ geliyordu ve onarım sessizce
// tarihçesiz kanonik dala mahkûm oluyordu — geri alındığında tüm paket YEŞİL
// kalıyordu, yani değişiklik pinsizdi.
//
// Kapılar TEK GÖVDEDEN (replicatedInnerPeer) gelmeye devam eder: düz eş aday
// değil, başka shard'daki eş aday değil. FE'nin "Eşten kur" düğmesi hâlâ
// state === 'dangling' istiyor (danglingMv.test.ts) — düğme YAYILMAZ.
func TestMVCoveragePeerOutsideDangling(t *testing.T) {
	const view = "db_summary_5m_local"
	hosts := []string{"ch-01", "ch-02", "ch-03"}
	sameShard := map[string]int{"ch-01": 1, "ch-02": 1, "ch-03": 1}
	rows := []mvTableRow{
		combinedMVRow("ch-01", view), innerRow("ch-01", "ReplicatedAggregatingMergeTree"),
		combinedMVRow("ch-02", view), innerRow("ch-02", "ReplicatedAggregatingMergeTree"),
		combinedMVRow("ch-03", view), innerRow("ch-03", "AggregatingMergeTree"), // düz
	}
	got := mvCoverageFromRows(rows, []string{view}, hosts, true, sameShard)
	ok := stateOf(t, got, view, "ch-01")
	if ok.State != MVStateOK {
		t.Fatalf("ch-01 sağlıklı olmalıydı: %+v", ok)
	}
	if ok.PeerHost != "ch-02" {
		t.Errorf("`ok` hücresinde eş adayı BOŞ — hedef uuid onarımı eşi göremez ve tarihçesiz dala düşer: %q", ok.PeerHost)
	}
	plain := stateOf(t, got, view, "ch-03")
	if plain.State != MVStatePlain || plain.PeerHost != "ch-01" {
		t.Errorf("`plain` hücresi de eş adayı taşımalı (deterministik: ada göre ilk): %+v", plain)
	}
	// Kapılar tek gövdeden: düz iç tablolu ch-03 kimseye eş OLMAZ.
	only := mvCoverageFromRows([]mvTableRow{
		combinedMVRow("ch-01", view), innerRow("ch-01", "ReplicatedAggregatingMergeTree"),
		combinedMVRow("ch-03", view), innerRow("ch-03", "AggregatingMergeTree"),
	}, []string{view}, []string{"ch-01", "ch-03"}, true, map[string]int{"ch-01": 1, "ch-03": 1})
	if st := stateOf(t, only, view, "ch-03"); st.PeerHost != "ch-01" {
		t.Errorf("düz hücrenin eşi Replicated olan ch-01 olmalı: %q", st.PeerHost)
	}
	if st := stateOf(t, only, view, "ch-01"); st.PeerHost != "" {
		t.Errorf("DÜZ iç tablolu bir host eş adayı OLAMAZ (düz tabloyu çoğaltır): %q", st.PeerHost)
	}
	// Başka shard'daki eş hâlâ aday DEĞİL.
	split := mvCoverageFromRows(rows[:4], []string{view}, []string{"ch-01", "ch-02"}, true,
		map[string]int{"ch-01": 1, "ch-02": 2})
	if st := stateOf(t, split, view, "ch-01"); st.PeerHost != "" {
		t.Errorf("başka shard'daki host eş sayılmış — tarihçe sözü tutulamaz: %q", st.PeerHost)
	}
}

func TestMVCoverageFromRows(t *testing.T) {
	const view = "service_summary_5m_local"
	cases := []struct {
		name       string
		rows       []mvTableRow
		canonical  []string
		hosts      []string
		replicated bool
		shardOf    map[string]int    // nil → hepsi shard 1
		want       map[string]string // host → durum
		wantPeer   map[string]string // host → eş host ("" = yok)
	}{
		{
			name: "dört durum yan yana",
			rows: []mvTableRow{
				combinedMVRow("ch-01", view), innerRow("ch-01", "ReplicatedAggregatingMergeTree"),
				combinedMVRow("ch-02", view), innerRow("ch-02", "AggregatingMergeTree"),
				combinedMVRow("ch-03", view),
				// ch-04: view satırı YOK
			},
			canonical:  []string{view},
			hosts:      []string{"ch-01", "ch-02", "ch-03", "ch-04"},
			replicated: true,
			want: map[string]string{
				"ch-01": MVStateOK, "ch-02": MVStatePlain,
				"ch-03": MVStateDangling, "ch-04": MVStateMissing,
			},
			wantPeer: map[string]string{"ch-03": "ch-01"},
		},
		{
			name: "düz eş 'Eşten kur' adayı DEĞİL",
			rows: []mvTableRow{
				combinedMVRow("ch-01", view), innerRow("ch-01", "AggregatingMergeTree"),
				combinedMVRow("ch-02", view),
			},
			canonical:  []string{view},
			hosts:      []string{"ch-01", "ch-02"},
			replicated: true,
			want:       map[string]string{"ch-01": MVStatePlain, "ch-02": MVStateDangling},
			wantPeer:   map[string]string{"ch-02": ""},
		},
		{
			// v0.10.825 incelemesi: başka shard'daki eş, "tarihçeyi eşten çeker"
			// sözünü tutamaz — aday DEĞİL, satır "Yeniden kur"a düşer.
			name: "eş BAŞKA shard'da → eş yok",
			rows: []mvTableRow{
				combinedMVRow("ch-01", view), innerRow("ch-01", "ReplicatedAggregatingMergeTree"),
				combinedMVRow("ch-03", view),
			},
			canonical:  []string{view},
			hosts:      []string{"ch-01", "ch-03"},
			replicated: true,
			shardOf:    map[string]int{"ch-01": 1, "ch-03": 2},
			want:       map[string]string{"ch-01": MVStateOK, "ch-03": MVStateDangling},
			wantPeer:   map[string]string{"ch-03": ""},
		},
		{
			name: "shard eşlemesi okunamadı → eş yok (bilinmiyor ≠ uygun)",
			rows: []mvTableRow{
				combinedMVRow("ch-01", view), innerRow("ch-01", "ReplicatedAggregatingMergeTree"),
				combinedMVRow("ch-02", view),
			},
			canonical:  []string{view},
			hosts:      []string{"ch-01", "ch-02"},
			replicated: true,
			shardOf:    map[string]int{},
			want:       map[string]string{"ch-01": MVStateOK, "ch-02": MVStateDangling},
			wantPeer:   map[string]string{"ch-02": ""},
		},
		{
			name: "tek düğüm: düz iç tablo NORMAL",
			rows: []mvTableRow{
				combinedMVRow("ch-01", "spanmetrics_hist_5m"),
				innerRow("ch-01", "AggregatingMergeTree"),
			},
			canonical:  []string{"spanmetrics_hist_5m"},
			hosts:      []string{"ch-01"},
			replicated: false,
			want:       map[string]string{"ch-01": MVStateOK},
		},
		{
			name: "TO'lu MV: gizli iç tablosu yok → view varsa ok",
			rows: []mvTableRow{{Host: "ch-01", Name: "span_links_reverse_mv", UUID: mvTestView, Engine: "MaterializedView",
				CreateQuery: "CREATE MATERIALIZED VIEW coremetry.span_links_reverse_mv TO coremetry.span_links_reverse AS SELECT 1"}},
			canonical:  []string{"span_links_reverse_mv"},
			hosts:      []string{"ch-01"},
			replicated: true,
			want:       map[string]string{"ch-01": MVStateOK},
		},
		{
			name:       "hiç satır vermeyen host → eksik",
			rows:       nil,
			canonical:  []string{view},
			hosts:      []string{"ch-01", "ch-02"},
			replicated: true,
			want:       map[string]string{"ch-01": MVStateMissing, "ch-02": MVStateMissing},
		},
		{
			name: "sıfır uuid (Ordinary DB) → kanıtlanamaz, ok",
			rows: []mvTableRow{{Host: "ch-01", Name: "spanmetrics_hist_5m", UUID: zeroUUID, Engine: "MaterializedView",
				CreateQuery: "CREATE MATERIALIZED VIEW coremetry.spanmetrics_hist_5m ENGINE = AggregatingMergeTree AS SELECT 1"}},
			canonical:  []string{"spanmetrics_hist_5m"},
			hosts:      []string{"ch-01"},
			replicated: true,
			want:       map[string]string{"ch-01": MVStateOK},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shardOf := c.shardOf
			if shardOf == nil {
				shardOf = map[string]int{}
				for _, h := range c.hosts {
					shardOf[h] = 1
				}
			}
			got := mvCoverageFromRows(c.rows, c.canonical, c.hosts, c.replicated, shardOf)
			if len(got) != len(c.canonical)*len(c.hosts) {
				t.Fatalf("hücre sayısı %d, beklenen %d: %+v", len(got), len(c.canonical)*len(c.hosts), got)
			}
			for host, want := range c.want {
				if st := stateOf(t, got, c.canonical[0], host); st.State != want {
					t.Errorf("%s: durum %q, beklenen %q (%+v)", host, st.State, want, st)
				}
			}
			for host, want := range c.wantPeer {
				if st := stateOf(t, got, c.canonical[0], host); st.PeerHost != want {
					t.Errorf("%s: eş %q, beklenen %q", host, st.PeerHost, want)
				}
			}
		})
	}
}

// Kanonik olmayan view kapsama eksenine GİRMEZ (onun "eksik" olması diye bir
// şey yok); düz iç tablo motoru satırda taşınır; sıra view sonra host.
func TestMVCoverageIgnoresNonCanonicalAndSorts(t *testing.T) {
	rows := []mvTableRow{
		combinedMVRow("ch-02", "b_mv"), innerRow("ch-02", "AggregatingMergeTree"),
		combinedMVRow("ch-01", "a_mv"), innerRow("ch-01", "ReplicatedAggregatingMergeTree"),
		// katalogda olmayan MV: görünmez
		{Host: "ch-01", Name: "rollup_custom_mv", UUID: mvTestView, Engine: "MaterializedView",
			CreateQuery: "CREATE MATERIALIZED VIEW coremetry.rollup_custom_mv ENGINE = AggregatingMergeTree AS SELECT 1"},
	}
	got := mvCoverageFromRows(rows, []string{"b_mv", "a_mv"}, []string{"ch-02", "ch-01"}, true, map[string]int{"ch-01": 1, "ch-02": 1})
	var order []string
	for _, s := range got {
		if s.View == "rollup_custom_mv" {
			t.Fatal("kanonik olmayan view kapsamaya girdi")
		}
		order = append(order, s.View+"@"+s.Host)
	}
	want := []string{"a_mv@ch-01", "a_mv@ch-02", "b_mv@ch-01", "b_mv@ch-02"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("sıra view, host olmalı: %v", order)
	}
	if st := stateOf(t, got, "b_mv", "ch-02"); st.State != MVStatePlain || st.InnerEngine != "AggregatingMergeTree" {
		t.Errorf("düz satır motoru taşımalı: %+v", st)
	}
	// ok satırları ÇIKTIDA kalır (kart "N MV × M host sağlıklı" der).
	if st := stateOf(t, got, "a_mv", "ch-01"); st.State != MVStateOK {
		t.Errorf("ok satırı düşmemeli: %+v", st)
	}
}

// Kanonik katalog EKSENİ: her DDL'den nesne adı çıkar, katalog boyunca eksiksiz.
func TestCanonicalMVNames(t *testing.T) {
	names := canonicalMVNames()
	if len(names) != len(canonicalMVs()) {
		t.Fatalf("%d ad, katalogda %d DDL — bir DDL'in adı okunamadı", len(names), len(canonicalMVs()))
	}
	seen := map[string]bool{}
	for _, n := range names {
		if !chObjRe.MatchString(n) {
			t.Errorf("geçersiz MV adı %q", n)
		}
		if seen[n] {
			t.Errorf("katalogda çift ad: %q", n)
		}
		seen[n] = true
		if canonicalMVDDL(n) == "" {
			t.Errorf("%q adı kanonik DDL'e geri çözülmüyor", n)
		}
	}
	if !seen["service_summary_5m"] || !seen["spanmetrics_hist_5m"] {
		t.Errorf("katalog beklenen MV'leri taşımıyor: %v", names)
	}
	if got := mvNameFromDDL("CREATE MATERIALIZED VIEW IF NOT EXISTS x_5m\n ENGINE = AggregatingMergeTree"); got != "x_5m" {
		t.Errorf("ad ayrıştırma: %q", got)
	}
	if got := mvNameFromDDL("CREATE TABLE IF NOT EXISTS t (a Int)"); got != "" {
		t.Errorf("MV olmayan DDL ad vermemeli: %q", got)
	}
}

// Kaynak pinleri: onarım NODE-YEREL bağlantıda, ON CLUSTER'sız, zaman
// tavanlı; durum Exec'ten ÖNCE yeniden ölçülür; doğrulama tipleri SQL'de
// sabit; sarkan onarımın kanonik dalı AYNI gövdeyi çağırır.
func TestMVRebuildSourcePins(t *testing.T) {
	b, err := os.ReadFile("mv_coverage.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"s.shardConn(", "stripOnCluster(", "s.adaptDDL(canonicalMVDDL(name))", "purgeGuard",
		"DROP TABLE IF EXISTS `", "` SYNC", // SYNC eki: kaskad iç tabloyu da götürür
		"context.WithTimeout(ctx, mvRebuildStepTimeout)",
		"toUInt32(total_replicas), toUInt32(active_replicas), toUInt8(is_readonly)",
		"clusterAllReplicas('%s', system.one)", "resolveHostAddrs(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	// Koşulan ifadelerde ON CLUSTER yok (yorumlar serbest).
	for _, m := range regexp.MustCompile(`"[^"\n]*ON CLUSTER[^"\n]*"`).FindAllString(src, -1) {
		t.Errorf("koşulan ifadede ON CLUSTER: %s", m)
	}
	// system.* okumaları tavanlı.
	if n, m := strings.Count(src, "FROM system."), strings.Count(src, "max_execution_time"); m < n {
		t.Errorf("system.* okumaları tavanlı olmalı: %d okuma, %d tavan", n, m)
	}
	// Durum yeniden ölçümü: RebuildMVOnHost kendi gövdesinde Exec KOŞMAZ,
	// önce MVCoverage'tan taze durumu okur (pencere komşu fonksiyona taşmasın).
	fn := funcBody(t, "mv_coverage.go", "func (s *Store) RebuildMVOnHost(")
	// v0.10.833 — kapı DENYLIST'ten ("durum ok ise reddet") ALLOWLIST'e geçti
	// (mvRebuildAllowed): State'e eklenecek yeni bir değer yıkıcı eylemi
	// kendiliğinden AÇMAMALI. Pin sabitin YAZIMINA değil KAPININ KENDİSİNE.
	for _, want := range []string{"s.mvCoverageReport(ctx, false)", "mvRebuildAllowed(row.State, row.TargetResolves)", "canonicalMVForObject(view)", "chObjRe.MatchString(view)"} {
		if !strings.Contains(fn, want) {
			t.Errorf("RebuildMVOnHost içinde eksik: %s", want)
		}
	}
	if strings.Contains(fn, "conn.Exec(") {
		t.Error("RebuildMVOnHost durumu ölçmeden DDL koşmamalı — Exec paylaşılan gövdede")
	}
	if i, j := strings.Index(fn, "mvRebuildAllowed("), strings.Index(fn, "s.rebuildMVOnConn("); i < 0 || j < 0 || i > j {
		t.Error("izin kapısı onarım çağrısından ÖNCE olmalı")
	}
	// Tek gövde: sarkan onarımın kanonik dalı aynı fonksiyonu çağırır ve
	// ikinci bir DROP + kanonik DDL merdiveni tutmaz.
	d, err := os.ReadFile("dangling_mv_admin.go")
	if err != nil {
		t.Fatal(err)
	}
	dsrc := string(d)
	// v0.10.825 incelemesi: EKRANDA "Eşten kur" yazıyorsa ve eş çözülemiyorsa
	// onarım SESSİZCE DROP + kanonik CREATE'e DÜŞEMEZ — onay alınmamış bir
	// eylemle tarihçe yanardı. Kapı wantPeer ve eş dalından ÖNCE.
	if !strings.Contains(dsrc, "wantPeer bool)") {
		t.Error("RepairDanglingMV operatörün gördüğü eylemi (wantPeer) almalı")
	}
	if !strings.Contains(dsrc, "aynı shard'da Replicated eş yok") {
		t.Error("eş çözülemediğinde açık RET yok — sessiz düşüş")
	}
	rep := funcBody(t, "dangling_mv_admin.go", "func (s *Store) RepairDanglingMV(")
	if i, j := strings.Index(rep, "aynı shard'da Replicated eş yok"), strings.Index(rep, "canonicalMVForObject(view)"); i < 0 || j < 0 || i > j {
		t.Error("wantPeer RET'i kanonik dala DÜŞMEDEN önce olmalı")
	}
	if !strings.Contains(dsrc, "s.rebuildMVOnConn(ctx, conn, view, name)") {
		t.Error("RepairDanglingMV kanonik dalı paylaşılan gövdeyi çağırmalı")
	}
	if strings.Contains(dsrc, "adaptDDL(canonicalMVDDL(") {
		t.Error("kanonik yeniden kurulum merdiveni iki gövdede — ayrışır")
	}
}

// v0.10.825 — sarkan liste DE aynı eş kuralını uygular: düz iç tablolu host
// "Eşten kur" adayı değildir (SHOW CREATE ile kurmak düz tabloyu çoğaltır,
// iki host birbirini hiç replike etmez ve kart bunu "onarıldı" sayardı).
func TestDanglingPeerRequiresReplicatedInner(t *testing.T) {
	rows := []mvTableRow{
		combinedMVRow("ch-01", "service_summary_5m"), innerRow("ch-01", "AggregatingMergeTree"),
		combinedMVRow("ch-02", "service_summary_5m"),
	}
	got := danglingFromRows(rows, map[string]int{"ch-01": 1, "ch-02": 1})
	if len(got) != 1 || got[0].Host != "ch-02" {
		t.Fatalf("yalnız ch-02 sarkan olmalı: %+v", got)
	}
	if got[0].PeerHost != "" {
		t.Errorf("düz iç tablolu eş aday olmamalı: %q", got[0].PeerHost)
	}
	rows[1] = innerRow("ch-01", "ReplicatedAggregatingMergeTree")
	if got = danglingFromRows(rows, map[string]int{"ch-01": 1, "ch-02": 1}); len(got) != 1 || got[0].PeerHost != "ch-01" {
		t.Errorf("Replicated eş aday olmalı: %+v", got)
	}
}

// v0.10.825 — boot'ta BİLEREK atlanan MV kapsamaya GİRMEZ. Girseydi kart
// kolonu olmayan kurulumda iki MV'yi her host'ta "eksik" gösterir, "Yeniden
// kur" düğmesi de tam migrate()'in engellediği DDL'i koşar ve insert-trigger
// kod 16 ile TÜM ingest'i bloklardı (v0.8.186 / v0.8.375).
func TestMVGuardedOffIsOneBody(t *testing.T) {
	s := &Store{}
	for _, n := range []string{"operation_group_summary_5m", "db_statement_summary_5m"} {
		if !s.mvGuardedOff(n) {
			t.Errorf("%s kolonu yokken kapı kapalı olmalı", n)
		}
	}
	if s.mvGuardedOff("service_summary_5m") {
		t.Error("koşulsuz MV kapatılmamalı")
	}
	s.hasOpGroupCol, s.hasDBStmtHashCol = true, true
	for _, n := range []string{"operation_group_summary_5m", "db_statement_summary_5m"} {
		if s.mvGuardedOff(n) {
			t.Errorf("%s kolonu varken kapı açık olmalı", n)
		}
	}
	// Tek gövde: hem boot create döngüsü hem kapsama ekseni AYNI kapıyı okur.
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "if s.mvGuardedOff(mvNameFromDDL(q)) {") {
		t.Error("migrate() create döngüsü paylaşılan kapıyı kullanmalı")
	}
	if strings.Contains(string(src), `strings.Contains(q, "operation_group_summary_5m")`) {
		t.Error("eski ikiz kapı duruyor — iki gövde ayrışır")
	}
	cov, err := os.ReadFile("mv_coverage.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cov), "if s.mvGuardedOff(n) {") {
		t.Error("MVCoverage kanonik eksenden kapalı MV'leri elemeli")
	}
}
