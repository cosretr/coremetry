package chstore

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/cilcenk/coremetry/internal/config"
)

// removed_tables_test.go — v0.10.846.
//
// Operatör-bildirimli (prod, 2026-09-20): replika tutarlılığı kartı
// "1/90 tablo sorunlu · eksik replika" dedi; tek sorunlu tablo `feedbacks`'ti
// ve shard 1'in İKİ host'unda da yoktu, shard 2'de 2/2 Replicated duruyordu.
//
// Kök neden: `feedbacks` v0.8.240'ta KALDIRILDI ve boot satır içi bir
// `DROP TABLE IF EXISTS feedbacks` koşuyordu. O ifade ON CLUSTER TAŞIMIYOR,
// adaptDDL de DROP'u yeniden yazmaz (identifyDDLTarget yalnız CREATE/ALTER
// eşler — v0.10.834). Kaldırma yalnız koordinatör host'ta koştu: bir shard
// temizlendi, ötekinde tablo kaldı. "Eksik replika" sanılan şey YARIM KALMIŞ
// SİLME'ydi.
//
// Buradaki testler beş sözleşmeyi çiviler: küme geneli kaldırma, SENKRON
// koşma (erteleme yolundan geçmeme), BİLMİYORKEN YIKMAMA, temizliğin boot'u
// DÜŞÜRMEMESİ ve "kuyruğa alındı ≠ uygulandı" ayrımı.

// ── sahte bağlantı ───────────────────────────────────────────────────
//
// driver.Conn / driver.Rows GÖMÜLÜ: yalnız kullanılan metotlar var, başka
// birine düşülürse nil panic testi patlatır (sessizce geçmez).

type ledgerRows struct {
	driver.Rows
	rows [][2]string
	i    int
}

func (r *ledgerRows) Next() bool { r.i++; return r.i <= len(r.rows) }
func (r *ledgerRows) Scan(dest ...any) error {
	row := r.rows[r.i-1]
	for i, d := range dest {
		p, ok := d.(*string)
		if !ok {
			return errors.New("ledgerRows: string olmayan hedef")
		}
		*p = row[i]
	}
	return nil
}
func (r *ledgerRows) Close() error { return nil }
func (r *ledgerRows) Err() error   { return nil }

type ledgerRow struct{ db string }

func (r ledgerRow) Err() error { return nil }
func (r ledgerRow) Scan(dest ...any) error {
	if p, ok := dest[0].(*string); ok {
		*p = r.db
	}
	return nil
}
func (r ledgerRow) ScanStruct(any) error { return nil }

type ledgerConn struct {
	driver.Conn
	sightings [][2]string // her Query çağrısında dönen (host, engine) satırları
	after     [][2]string // DROP'tan SONRAKİ ölçüm (nil = ilkiyle aynı)
	queryErr  error
	execErr   error
	queries   int
	execs     []string
}

func (c *ledgerConn) QueryRow(context.Context, string, ...any) driver.Row {
	return ledgerRow{db: "shop"}
}

func (c *ledgerConn) Query(_ context.Context, _ string, _ ...any) (driver.Rows, error) {
	c.queries++
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	rows := c.sightings
	if c.queries > 1 && c.after != nil {
		rows = c.after
	}
	return &ledgerRows{rows: rows}, nil
}

func (c *ledgerConn) Exec(_ context.Context, query string, _ ...any) error {
	c.execs = append(c.execs, query)
	return c.execErr
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func withLedger(t *testing.T, rows ...removedTable) {
	t.Helper()
	old := removedTables
	removedTables = rows
	t.Cleanup(func() { removedTables = old })
}

func clusterStore(conn driver.Conn) *Store {
	// deferDDL AÇIK: prod yolu (küme + şema yerinde) ertelemeyi açar ve
	// kaldırmanın O KİPTE DE koşması sözleşmenin ta kendisi.
	return &Store{cfg: config.CHConfig{ClusterName: "shop_cluster"}, conn: conn, deferDDL: true}
}

// ── SAF katman ───────────────────────────────────────────────────────

// TestRemovedTableDropDDL — MUTASYON KAPISI (i): küme kolundan ON CLUSTER
// kaldırılırsa kaldırma yalnız koordinatörde koşar (prod'daki kusur).
func TestRemovedTableDropDDL(t *testing.T) {
	cases := []struct {
		name, table, onCluster, want string
	}{
		{"tek düğüm — v0.8.240'tan beri koşanın birebir aynısı", "feedbacks", "", "DROP TABLE IF EXISTS feedbacks"},
		{"küme — ON CLUSTER + SYNC", "feedbacks", " ON CLUSTER `shop_cluster`", "DROP TABLE IF EXISTS feedbacks ON CLUSTER `shop_cluster` SYNC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := removedTableDropDDL(c.table, c.onCluster)
			if got != c.want {
				t.Fatalf("removedTableDropDDL(%q, %q) = %q, beklenen %q", c.table, c.onCluster, got, c.want)
			}
			if c.onCluster == "" {
				if strings.Contains(got, "ON CLUSTER") || strings.Contains(got, "SYNC") {
					t.Fatalf("tek düğümde ON CLUSTER/SYNC olmamalı: %q", got)
				}
				return
			}
			if !strings.Contains(got, "ON CLUSTER") {
				t.Errorf("küme kipinde ON CLUSTER yok — kaldırma yalnız koordinatörde koşar: %q", got)
			}
			if !strings.HasSuffix(got, " SYNC") {
				t.Errorf("küme kipinde SYNC yok: %q", got)
			}
		})
	}
}

// TestRemovedTableDropDDLUsesOnClusterHelper — küme cümlesinin İKİNCİ bir
// yazımı açılmasın (v0.10.846 incelemesi): üretilen metin s.onCluster()
// çıktısını AYNEN taşımalı.
func TestRemovedTableDropDDLUsesOnClusterHelper(t *testing.T) {
	s := &Store{cfg: config.CHConfig{ClusterName: "shop-all"}}
	got := removedTableDropDDL("feedbacks", s.onCluster())
	if !strings.Contains(got, s.onCluster()) {
		t.Fatalf("%q, s.onCluster() çıktısını (%q) taşımıyor", got, s.onCluster())
	}
	if (&Store{}).onCluster() != "" {
		t.Fatal("tek düğümde onCluster() boş olmalı")
	}
}

func TestRemovedTablesLedgerShape(t *testing.T) {
	if len(removedTables) == 0 {
		t.Fatal("defter boş — en az `feedbacks` (v0.8.240) olmalı")
	}
	seen := map[string]bool{}
	for _, rt := range removedTables {
		if rt.Name == "" || rt.Since == "" || rt.Why == "" || rt.Kind == "" {
			t.Errorf("eksik defter satırı: %+v (ad + sürüm + gerekçe + Kind zorunlu)", rt)
		}
		if seen[rt.Name] {
			t.Errorf("%s defterde iki kez", rt.Name)
		}
		seen[rt.Name] = true
	}
	if rt, ok := removedTableEntry("feedbacks"); !ok || rt.Since != "v0.8.240" || rt.Kind != removedPlainTable {
		t.Errorf("feedbacks defterde v0.8.240 + düz tablo olmalı, bulundu %+v (ok=%v)", rt, ok)
	}
	if _, ok := removedTableEntry("spans"); ok {
		t.Error("`spans` defterde — yaşayan bir tablo kaldırılmış sayılamaz")
	}
	// Defter araması TÜREV ada yayılmaz: kaldırma yalnız çıplak adı düşürür.
	if _, ok := removedTableEntry("feedbacks_old"); ok {
		t.Error("türev ad defterde eşleşti — tutulamayacak bir temizlik sözü verilirdi")
	}
}

// TestLedgerNeverNamesALivingTable — KAPI (v0.10.846 incelemesi, asıl tehlike).
//
// `dropRemovedTables`, `tables` dilimi KOŞTUKTAN SONRA çalışır. Deftere
// YAŞAYAN bir ad düşerse boot o tabloyu KURAR ve hemen ardından
// `DROP … ON CLUSTER … SYNC` ile küme genelinde SİLER — her boot, sessizce,
// hata vermeden. Bugün kesişim yok ama bu bir TESADÜFTÜ: kodda bunu tutan
// hiçbir şey yoktu. Artık GERÇEK katalogla (canonicalTables + canonicalMVs +
// migrations) tutuluyor.
func TestLedgerNeverNamesALivingTable(t *testing.T) {
	s := &Store{canonicalTableDDL: realCatalogue()}
	managed := s.productTableNames()
	if len(managed) == 0 {
		t.Fatal("gerçek katalog boş — kapı ölçmüyor")
	}
	for _, rt := range removedTables {
		if managed[rt.Name] {
			t.Fatalf("%s hem defterde (ürün %s'te kaldırdı) hem ÜRÜN KATALOĞUNDA — boot her açılışta "+
				"kurup küme genelinde siler; defter satırını ya da kataloğu düzelt", rt.Name, rt.Since)
		}
	}
	// Kapının ısırdığını da ölç: sentetik bir "yaşayan ad" defterde olsaydı
	// yakalanmalıydı (yoksa bu test her zaman yeşil kalan bir süs olurdu).
	if !managed["spans"] {
		t.Fatal("`spans` gerçek katalogda yok — kapı yanlış kümeyi okuyor")
	}
}

// TestRemovedSimpleDropBlocked — defterin ŞEKİL sözleşmesi. Bugünkü tek
// muhafız (highVolumeTables üyeliği) defter büyüdüğünde yeşile dönerdi.
func TestRemovedSimpleDropBlocked(t *testing.T) {
	cases := []struct {
		name    string
		rt      removedTable
		blocked bool
		has     string
	}{
		{"düz tablo geçer", removedTable{Name: "feedbacks", Since: "v0.8.240", Kind: removedPlainTable}, false, ""},
		{"MV basit yoldan GEÇMEZ", removedTable{Name: "eski_mv", Since: "v0.9.1", Kind: removedMV}, true, "dropCombinedMV"},
		{"yüksek hacim basit yoldan GEÇMEZ", removedTable{Name: "eski_span", Since: "v0.9.1", Kind: removedHighVolume}, true, "Distributed"},
		{"beyan ile kayıt çelişiyor", removedTable{Name: "spans", Since: "v0.9.1", Kind: removedPlainTable}, true, "highVolumeTables üyesi"},
		{"Kind boş — bilinmeden DROP yok", removedTable{Name: "x", Since: "v0.9.1"}, true, "Kind boş"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := removedSimpleDropBlocked(c.rt)
			if (got != "") != c.blocked {
				t.Fatalf("engel=%q, beklenen blocked=%v", got, c.blocked)
			}
			if c.has != "" && !strings.Contains(got, c.has) {
				t.Errorf("gerekçe %q içermiyor: %s", c.has, got)
			}
		})
	}
	// Bugünkü defterin HER satırı basit yoldan geçebilmeli, yoksa temizlik
	// sessizce hiç koşmaz.
	for _, rt := range removedTables {
		if reason := removedSimpleDropBlocked(rt); reason != "" {
			t.Errorf("%s defterde ama basit yoldan geçemiyor: %s", rt.Name, reason)
		}
		if n := bareDestructiveTarget(removedTableDropDDL(rt.Name, " ON CLUSTER `shop_cluster`")); n != "" {
			t.Errorf("%s: bareDestructiveTarget %q döndü — v0.10.834 muhafızı bu DDL'i reddeder", rt.Name, n)
		}
	}
}

func TestRemovedObservedBlocked(t *testing.T) {
	rt := removedTable{Name: "feedbacks", Since: "v0.8.240", Kind: removedPlainTable}
	if got := removedObservedBlocked(rt, []string{"ReplicatedMergeTree", "MergeTree"}); got != "" {
		t.Errorf("MergeTree ailesi engellenmemeli: %s", got)
	}
	if got := removedObservedBlocked(rt, []string{"MaterializedView"}); !strings.Contains(got, "MaterializedView") {
		t.Errorf("kümede MV görülürse basit DROP engellenmeli: %s", got)
	}
	if got := removedObservedBlocked(rt, []string{"Distributed"}); !strings.Contains(got, "beklenmedik motor") {
		t.Errorf("beklenmedik motor engellenmeli: %s", got)
	}
}

// ── DAVRANIŞ (sahte bağlantı) ────────────────────────────────────────

// TestDropRemovedTableRunsSynchronously — SENKRON koşar, ERTELENMEZ.
//
// v0.9.633 dersi: erteleme kipinde execDDL kuyruğa alıp nil döner, yani
// kartın "bir sonraki boot küme genelinde temizler" cümlesi TUTULAMAYAN bir
// söz olurdu. deferDDL AÇIKKEN bile DROP gerçekten koşmalı.
func TestDropRemovedTableRunsSynchronously(t *testing.T) {
	captureLog(t)
	conn := &ledgerConn{sightings: [][2]string{{"ch-03", "ReplicatedMergeTree"}, {"ch-04", "ReplicatedMergeTree"}}, after: [][2]string{}}
	s := clusterStore(conn)
	s.dropRemovedTables(context.Background())

	if len(conn.execs) != 1 {
		t.Fatalf("%d DDL koştu, 1 bekleniyordu (%v)", len(conn.execs), conn.execs)
	}
	if len(s.deferredDDL) != 0 {
		t.Fatalf("temizlik erteleme kuyruğuna düştü: %v", s.deferredDDL)
	}
	got := conn.execs[0]
	if !strings.Contains(got, "ON CLUSTER") || !strings.HasSuffix(got, " SYNC") {
		t.Fatalf("koşan DDL küme geneli değil: %q", got)
	}
	if !strings.Contains(got, "feedbacks") {
		t.Fatalf("koşan DDL defter satırını hedeflemiyor: %q", got)
	}
}

// TestDropRemovedTableSkipsWhenAbsent — nesne hiçbir host'ta yoksa DDL
// GÖNDERİLMEZ (her pod'un her boot'unda bir dağıtık DDL turu israfı).
//
// Ölçüm KÜME GENELİ olmak zorunda: prod'da kaldırma koordinatörün host'unda
// zaten koşmuştu; yerel bir varlık kontrolü "temiz" deyip öteki shard'ı
// sonsuza dek bırakırdı.
func TestDropRemovedTableSkipsWhenAbsent(t *testing.T) {
	captureLog(t)
	conn := &ledgerConn{sightings: [][2]string{}}
	s := clusterStore(conn)
	s.dropRemovedTables(context.Background())
	if len(conn.execs) != 0 {
		t.Fatalf("tablo hiçbir yerde yokken DDL gönderildi: %v", conn.execs)
	}
	if !strings.Contains(s.clusterSystemSource("tables"), "clusterAllReplicas") {
		t.Fatal("küme kipinde varlık ölçümü koordinatör-yerel — yarım kalmış silme görünmez")
	}
}

// TestDropRemovedTableDoesNotDestroyWhatItCannotMeasure — sözleşme 3.
func TestDropRemovedTableDoesNotDestroyWhatItCannotMeasure(t *testing.T) {
	buf := captureLog(t)
	conn := &ledgerConn{queryErr: errors.New("read: i/o timeout")}
	s := clusterStore(conn)
	s.dropRemovedTables(context.Background())
	if len(conn.execs) != 0 {
		t.Fatalf("şekil okunamazken DROP koştu: %v", conn.execs)
	}
	if !strings.Contains(buf.String(), "ATLANDI") {
		t.Errorf("atlama loglanmadı: %s", buf.String())
	}
}

// TestDropRemovedTableRefusesUnexpectedShape — kümede MV görülürse basit
// DROP koşmaz (iç tablo hacim guard'ısız kalırdı).
func TestDropRemovedTableRefusesUnexpectedShape(t *testing.T) {
	captureLog(t)
	conn := &ledgerConn{sightings: [][2]string{{"ch-01", "MaterializedView"}}}
	s := clusterStore(conn)
	s.dropRemovedTables(context.Background())
	if len(conn.execs) != 0 {
		t.Fatalf("beklenmedik şekilde DROP koştu: %v", conn.execs)
	}
}

// TestDropRemovedTablesNeverFailsBoot — MUTASYON KAPISI (ii).
//
// Temizlik bir ONARIM değil: okuyucusu olmayan bir kalıntının kalması
// sistemi çalışmaz yapmaz. Erteleme KAPALI kipte (taze kurulum /
// RESET_SCHEMA — operatörün test kümesinde canlı olan yol) bir DROP hatası
// boot'u düşürürdü. Hata yutulmaz, LOGLANIR; döngü de durmaz.
func TestDropRemovedTablesNeverFailsBoot(t *testing.T) {
	buf := captureLog(t)
	withLedger(t,
		removedTable{Name: "eski_a", Since: "v0.8.1", Why: "test", Kind: removedPlainTable},
		removedTable{Name: "eski_b", Since: "v0.8.2", Why: "test", Kind: removedPlainTable},
	)
	conn := &ledgerConn{sightings: [][2]string{{"ch-01", "ReplicatedMergeTree"}}, execErr: errors.New("TABLE_IS_READ_ONLY")}
	s := clusterStore(conn)
	s.dropRemovedTables(context.Background()) // hata DÖNDÜRMEZ: imza gereği boot'u düşüremez

	if len(conn.execs) != 2 {
		t.Fatalf("%d DDL denendi, 2 bekleniyordu — ilk hatada döngü durmuş olabilir (%v)", len(conn.execs), conn.execs)
	}
	if !strings.Contains(buf.String(), "düşürülemedi") {
		t.Errorf("başarısızlık yüksek sesle loglanmadı: %s", buf.String())
	}
}

// TestDropRemovedTableDistinguishesQueuedFromApplied — MUTASYON KAPISI (iii).
//
// execWithReadonlyRetry dağıtık DDL "kuyruğa alındı"yı (kod 159) BAŞARI
// sayar (v0.9.604), yani dönüş değeri uygulamayı KANITLAMAZ. Kanıt yalnız
// ikinci ölçümdedir (v0.9.633 dersi: iki ayrı fiil).
func TestDropRemovedTableDistinguishesQueuedFromApplied(t *testing.T) {
	t.Run("uygulandı", func(t *testing.T) {
		buf := captureLog(t)
		conn := &ledgerConn{sightings: [][2]string{{"ch-03", "ReplicatedMergeTree"}}, after: [][2]string{}}
		clusterStore(conn).dropRemovedTables(context.Background())
		if !strings.Contains(buf.String(), "DÜŞÜRÜLDÜ (uygulandı)") {
			t.Errorf("uygulanmış kaldırma böyle raporlanmadı: %s", buf.String())
		}
	})
	t.Run("yalnız kuyruğa alındı", func(t *testing.T) {
		buf := captureLog(t)
		conn := &ledgerConn{
			sightings: [][2]string{{"ch-03", "ReplicatedMergeTree"}},
			after:     [][2]string{{"ch-03", "ReplicatedMergeTree"}}, // hâlâ orada
		}
		clusterStore(conn).dropRemovedTables(context.Background())
		out := buf.String()
		if !strings.Contains(out, "KUYRUĞA ALINDI") {
			t.Errorf("kuyruk durumu raporlanmadı: %s", out)
		}
		if strings.Contains(out, "(uygulandı)") {
			t.Errorf("uygulanmadığı hâlde 'uygulandı' dendi — v0.9.633'ün öğrettiği yalan: %s", out)
		}
	})
}

// TestDropRemovedTableSingleNodeStaysNodeLocal — tek düğümde davranış
// v0.8.240'tan beri koşanla birebir aynı.
func TestDropRemovedTableSingleNodeStaysNodeLocal(t *testing.T) {
	captureLog(t)
	conn := &ledgerConn{sightings: [][2]string{{"", "MergeTree"}}, after: [][2]string{}}
	s := &Store{cfg: config.CHConfig{}, conn: conn}
	s.dropRemovedTables(context.Background())
	if len(conn.execs) != 1 || conn.execs[0] != "DROP TABLE IF EXISTS feedbacks" {
		t.Fatalf("tek düğüm DDL'i değişti: %v", conn.execs)
	}
}
