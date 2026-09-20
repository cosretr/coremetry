package chstore

// mv_inner_conn_test.go — v0.10.832 inceleme turu: MV onarım yollarının
// BAĞLANTIYLA konuşan yarıları. Saf çekirdek yeşil olup çağrıldığı yer
// pinlenmemişse düzeltme kendini sessizce iptal eder
// ([[feedback-tested-but-unreachable]]), o yüzden bu dosya sahte bir
// driver.Conn üzerinden GERÇEK davranışı koşar: hangi sorgu gönderildi,
// hangi kapı ısırdı, hangi adım koştu.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ── betikli sahte bağlantı ────────────────────────────────────────────

// scriptStep — sırada beklenen BİR sorgu: metninde `match` geçmeli, Scan
// hedeflerine `vals` yazılır (sırayla), `err` varsa Scan onu döndürür.
type scriptStep struct {
	match string
	vals  []any
	err   error
}

type scriptRow struct {
	vals []any
	err  error
}

func (r scriptRow) Err() error { return r.err }
func (r scriptRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		if i >= len(r.vals) {
			break
		}
		switch p := d.(type) {
		case *string:
			if v, ok := r.vals[i].(string); ok {
				*p = v
			}
		case *uint64:
			if v, ok := r.vals[i].(uint64); ok {
				*p = v
			}
		case *uint32:
			if v, ok := r.vals[i].(uint32); ok {
				*p = v
			}
		case *uint8:
			if v, ok := r.vals[i].(uint8); ok {
				*p = v
			}
		}
	}
	return nil
}
func (r scriptRow) ScanStruct(any) error { return r.err }

// scriptConn — QueryRow/Exec'i betikten cevaplayan, GÖNDERİLEN metinleri
// saklayan sahte bağlantı. Betik bitince gelen sorgu HATA'dır: beklenmeyen
// bir okuma sessizce "boş" dönmez.
type scriptConn struct {
	driver.Conn
	name    string
	steps   []scriptStep
	i       int
	queries []string
	execs   []string
	execErr error
}

func (c *scriptConn) QueryRow(ctx context.Context, q string, args ...any) driver.Row {
	c.queries = append(c.queries, q)
	if c.i >= len(c.steps) {
		return scriptRow{err: fmt.Errorf("%s: betik dışı sorgu: %s", c.name, q)}
	}
	st := c.steps[c.i]
	c.i++
	if st.match != "" && !strings.Contains(q, st.match) {
		return scriptRow{err: fmt.Errorf("%s: sıra dışı sorgu (%q bekleniyordu): %s", c.name, st.match, q)}
	}
	return scriptRow{vals: st.vals, err: st.err}
}

func (c *scriptConn) Exec(ctx context.Context, q string, args ...any) error {
	c.execs = append(c.execs, q)
	return c.execErr
}

// ── item 1: sınıflandırma okumaları AYARI SABİTLER ────────────────────

// TestClassificationReadsPinTheUUIDSetting — v0.10.832 inceleme (ÖNEMLİ):
// sınıflandırma, metinde uuid görünüp görünmemesine göre karar verdiği için
// PROFİLE bağımlıydı. Regex düzeltmesi kemer, bu askı: envanter ve doğrulama
// okumaları ayarı 0'a SABİTLER, böylece hangi profilde koşarsak koşalım
// create_table_query metni AYNI biçimde gelir.
func TestClassificationReadsPinTheUUIDSetting(t *testing.T) {
	// Muhafız KENDİ metnini ısırmasın: yorumlarda ayarın adı ve "= 1" biçimi
	// GEÇER (gerekçeyi anlatır), aranan şey SQL'deki ATAMADIR.
	code := func(file string) string {
		var out []string
		for _, line := range strings.Split(readGoSource(t, file), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}
	const pin0 = "show_table_uuid_in_table_create_query_if_not_nil = 0"
	const pin1 = "show_table_uuid_in_table_create_query_if_not_nil = 1"
	cov := code("mv_coverage.go")
	if n := strings.Count(cov, pin0); n < 2 {
		t.Errorf("mv_coverage.go: sınıflandırma okumaları (mvInventory + verifyMVRebuild) ayarı 0'a sabitlemeli; bulunan %d", n)
	}
	if strings.Contains(cov, pin1) {
		t.Error("sınıflandırma dosyası ayarı 1 yapmamalı — metin biçimi değişir, aynı MV başka sınıflanır")
	}
	// Nesne uuid'sini İSTEYEN tek yer ayarı 1 yapar (dangling_mv_admin.go).
	if !strings.Contains(code("dangling_mv_admin.go"), pin1) {
		t.Error("nesne uuid'si okuması ayarı 1 yapmalı — varsayılanda metin uuid taşımaz")
	}
}

// ── item 8 + 5a: verifyMVRebuild ─────────────────────────────────────

// TestVerifyMVRebuildResolvesInnerByViewUUID — v0.10.832: doğrulama iç
// tabloyu VIEW uuid'sinden arar. Bu yolun testi yoktu; ad çözümü bu yamada
// değişti ve yanlış adı arayan bir doğrulama SAĞLAM bir kurulumu "iç tablo
// doğmadı" diye reddeder.
func TestVerifyMVRebuildResolvesInnerByViewUUID(t *testing.T) {
	const (
		view = "service_summary_5m"
		vu   = "11111111-1111-1111-1111-111111111111"
		obj  = "22222222-2222-2222-2222-222222222222"
	)
	cq := "CREATE MATERIALIZED VIEW coremetry." + view + " UUID '" + vu + "' TO INNER UUID '" + obj + "' (x Int) ENGINE = AggregatingMergeTree AS SELECT 1"
	conn := &scriptConn{name: "yerel", steps: []scriptStep{
		{match: "toString(uuid), substring(create_table_query", vals: []any{vu, cq}},
		{match: "SELECT engine FROM system.tables", vals: []any{"AggregatingMergeTree"}},
	}}
	s := &Store{} // tek düğüm: system.replicas okuması yok
	steps, err := s.verifyMVRebuild(context.Background(), conn, view, nil)
	if err != nil {
		t.Fatalf("sağlam kurulum reddedildi: %v (%v)", err, steps)
	}
	// İkinci sorgunun BİND ARGÜMANI değil, aradığı ADIN doğruluğu önemli —
	// argümanı yakalayamadığımız için adın kurulduğu gövdeyi ölçüyoruz.
	if got := innerTableName(vu); got != ".inner_id."+vu {
		t.Errorf("iç tablo adı %q", got)
	}
	if len(conn.queries) != 2 {
		t.Errorf("iki okuma bekleniyordu: %v", conn.queries)
	}
}

// TestVerifyMVRebuildSkipsWhenViewUUIDIsZero — v0.10.832 inceleme (KÜÇÜK):
// innerTableName'in ilan ettiği validUUID sözleşmesi burada YOKTU. Ordinary
// DB'de MV uuid'si sıfırdır ve iç tablo `.inner.<ad>` olarak yaşar; eski kod
// `.inner_id.0000…`'ı arayıp BAŞARILI bir kurulumu "iç tablo doğmadı" diye
// raporluyordu.
func TestVerifyMVRebuildSkipsWhenViewUUIDIsZero(t *testing.T) {
	const view = "service_summary_5m"
	cq := "CREATE MATERIALIZED VIEW coremetry." + view + " (x Int) ENGINE = AggregatingMergeTree AS SELECT 1"
	conn := &scriptConn{name: "ordinary", steps: []scriptStep{
		{match: "toString(uuid), substring(create_table_query", vals: []any{zeroUUID, cq}},
	}}
	s := &Store{}
	steps, err := s.verifyMVRebuild(context.Background(), conn, view, nil)
	if err != nil {
		t.Fatalf("sıfır uuid HATA değildir (Ordinary DB): %v", err)
	}
	if len(conn.queries) != 1 {
		t.Errorf("sıfır uuid'de iç tablo ARANMAMALI (`.inner_id.0000…` diye bir tablo yok): %v", conn.queries)
	}
	if !strings.Contains(strings.Join(steps, "\n"), "uuid") {
		t.Errorf("atlama SESSİZ olmamalı, adımlarda gerekçe olmalı: %v", steps)
	}
}

// ── item 5b: purge'ün Distributed dalı ───────────────────────────────

// TestTruncateStmtGuardsZeroUUIDOnDistributedMV — v0.10.832 inceleme
// (KÜÇÜK): `truncateStmt`in MaterializedView dalı validUUID kapısını
// uyguluyor, Distributed → `<ad>_local` MaterializedView dalı UYGULAMIYORDU.
// Sıfır uuid'de `.inner_id.0000…` TRUNCATE edilir, `IF EXISTS` yüzünden
// SESSİZ no-op olur ve purge "başarılı" yazar — silinmeyen veri silinmiş
// sayılır. İki dal AYNI kapıyı uygulamalı ve sessiz no-op yerine HATA.
func TestTruncateStmtGuardsZeroUUIDOnDistributedMV(t *testing.T) {
	const (
		name   = "db_summary_5m"
		local  = name + "_local"
		engine = "Distributed('shop_cluster', currentDatabase(), db_summary_5m_local, rand())"
		good   = "33333333-3333-3333-3333-333333333333"
	)
	run := func(localUUID string) (string, error) {
		s := &Store{conn: &scriptConn{name: "purge", steps: []scriptStep{
			{match: "system.tables", vals: []any{"Distributed", engine, good, uint64(1)}},
			{match: "system.tables", vals: []any{"MaterializedView", "", localUUID, uint64(1)}},
		}}}
		stmt, _, err := s.truncateStmt(context.Background(), name)
		return stmt, err
	}
	t.Run("sıfır uuid → HATA", func(t *testing.T) {
		stmt, err := run(zeroUUID)
		if err == nil {
			t.Fatalf("sessiz no-op üretildi, hata beklenirdi: %q", stmt)
		}
		if strings.Contains(stmt, zeroUUID) {
			t.Errorf("sıfır uuid'li ad ifadeye girdi: %q", stmt)
		}
	})
	t.Run("geçerli uuid → iç tablo TRUNCATE", func(t *testing.T) {
		stmt, err := run(good)
		if err != nil {
			t.Fatalf("geçerli uuid reddedildi: %v", err)
		}
		if !strings.Contains(stmt, "`"+innerTableName(good)+"`") {
			t.Errorf("iç tablo adı yok: %q", stmt)
		}
		if !strings.Contains(stmt, "ON CLUSTER `shop_cluster`") {
			t.Errorf("shard'lara dağıtılmalı: %q", stmt)
		}
	})
}

// readGoSource — kaynak pini yardımcısı.
func readGoSource(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
