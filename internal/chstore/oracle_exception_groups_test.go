package chstore

// oracle_exception_groups_test.go — v0.10.1092 (operatör: "Oracle hataları
// Exceptions gibi görünsün"). Pinler: parmak izi kararlılığı; span-merkezli
// dedektörler (fırtına, paylaşılan patlama, ölümcül, yayılım) ve span
// tazeleyicisinin boot tohumu `ora:` gruplarını OKUMAZ; Oracle çipi süzgeci;
// servis yapışkanlığı; okuma SQL'lerinin sınırları.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

func TestOracleGroupFingerprintStable(t *testing.T) {
	a := OracleGroupFingerprint("o-11111111", "APP_ERR_001", "OP_TRANSFER")
	if !strings.HasPrefix(a, OracleGroupPrefix) || len(a) != len(OracleGroupPrefix)+16 || !IsOracleGroup(a) {
		t.Fatalf("biçim: %q", a)
	}
	// Aynı girdi → aynı iz; Oracle CHAR dolgusu kırpılır.
	if b := OracleGroupFingerprint(" o-11111111", "APP_ERR_001  ", " OP_TRANSFER "); b != a {
		t.Fatalf("kırpma: %q != %q", b, a)
	}
	// Anahtarın her parçası ayırt eder; sıra karışmaz.
	for _, other := range []string{
		OracleGroupFingerprint("o-22222222", "APP_ERR_001", "OP_TRANSFER"),
		OracleGroupFingerprint("o-11111111", "APP_ERR_002", "OP_TRANSFER"),
		OracleGroupFingerprint("o-11111111", "APP_ERR_001", "OP_PAYMENT"),
		OracleGroupFingerprint("o-11111111", "OP_TRANSFER", "APP_ERR_001"),
	} {
		if other == a {
			t.Fatalf("farklı anahtar aynı iz: %q", other)
		}
	}
	// ALTIN değer — algoritma değişirse her Oracle grubu yeniden doğar (durum,
	// atanan, resolve geçmişi kaybolur): sha1("o-11111111|APP_ERR_001|OP_TRANSFER")[:16].
	if a != "ora:42c30d3ac1ddbf2c" {
		t.Fatalf("altın parmak izi değişti: %q", a)
	}
	if IsOracleGroup("0a1b2c3d4e5f6a7b") {
		t.Fatal("span parmak izi Oracle sayılmamalı")
	}
	if got := OracleGroupFallbackService(" app-err "); got != "oracle:app-err" {
		t.Fatalf("sentetik servis: %q", got)
	}
}

// capConn — Query / QueryRow metnini yakalar ve hata döner (sorgu koşmaz).
type capConn struct {
	driver.Conn
	got []string
}

var errCaptured = errors.New("yakalandı")

func (c *capConn) Query(_ context.Context, q string, _ ...any) (driver.Rows, error) {
	c.got = append(c.got, q)
	return nil, errCaptured
}

func (c *capConn) QueryRow(_ context.Context, q string, _ ...any) driver.Row {
	c.got = append(c.got, q)
	return fakeRow{err: errCaptured}
}

// Fırtına (exception-storm) ve paylaşılan patlama (exception:shared-dependency)
// Oracle gruplarını SAYMAZ; ölümcül dedektör, yayılım ve boot tohumu da.
func TestOracleGroupsExcludedFromSpanDetectors(t *testing.T) {
	ctx := context.Background()
	c := &capConn{}
	s := &Store{conn: c}
	_, _ = s.RecentNewExceptionServices(ctx, time.Now().Add(-10*time.Minute))
	_, _ = s.FindSharedExceptionBursts(ctx, 24*time.Hour, 3)
	_, _ = s.FindFatalExceptions(ctx, 24*time.Hour)
	_, _ = s.MaxExceptionGroupLastSeen(ctx)
	_, _ = s.exceptionSpreadUncached(ctx, time.Minute)
	if len(c.got) != 5 {
		t.Fatalf("5 sorgu bekleniyordu: %d", len(c.got))
	}
	names := []string{"fırtına", "paylaşılan patlama", "ölümcül", "boot tohumu (max last_seen)", "yayılım"}
	for i, q := range c.got {
		if !strings.Contains(q, "exception_groups") || !strings.Contains(q, NotOracleGroupSQL) {
			t.Errorf("%s sorgusu Oracle gruplarını dışlamıyor:\n%s", names[i], q)
		}
	}
	if NotOracleGroupSQL != "NOT startsWith(fingerprint, 'ora:')" {
		t.Fatalf("süzgeç metni: %q", NotOracleGroupSQL)
	}
}

// Exceptions "Oracle" çipi: only / exclude; boş = ikisi de (varsayılan görünüm
// Oracle gruplarını içerir).
func TestExceptionGroupWhereOracleFacet(t *testing.T) {
	if w := buildExceptionGroupWhere(ExceptionGroupFilter{}); strings.Contains(w.sql(), "fingerprint") {
		t.Fatalf("varsayılan görünüm Oracle süzmemeli: %s", w.sql())
	}
	ex := buildExceptionGroupWhere(ExceptionGroupFilter{Oracle: "exclude"})
	if !strings.Contains(ex.sql(), NotOracleGroupSQL) {
		t.Fatalf("exclude: %s", ex.sql())
	}
	only := buildExceptionGroupWhere(ExceptionGroupFilter{Oracle: "only"})
	if !strings.Contains(only.sql(), "startsWith(fingerprint, ?)") || only.args[len(only.args)-1] != OracleGroupPrefix {
		t.Fatalf("only: %s %v", only.sql(), only.args)
	}
}

// Tam-satır taşıma: bu turda servis çözülemeyen (sentetik) Oracle grubu
// önceki GERÇEK servisi korur; durum/atanan/özet her grupta olduğu gibi taşınır.
func TestMergeOracleGroupKeepsResolvedService(t *testing.T) {
	fp := OracleGroupFingerprint("o-1", "APP_ERR_001", "OP_A")
	prev := &ExceptionGroup{Fingerprint: fp, Service: "svc-payments", State: ExStateAcknowledged, Assignee: "u1",
		Occurrences: 100, FirstSeen: 1, LastSeen: 2}
	got := mergeExceptionGroup(ExceptionGroup{Fingerprint: fp, Service: "oracle:app-err", Occurrences: 7, LastSeen: 3}, prev)
	if got.Service != "svc-payments" || got.State != ExStateAcknowledged || got.Assignee != "u1" || got.Occurrences != 107 || got.FirstSeen != 1 {
		t.Fatalf("taşıma: %+v", got)
	}
	// Gerçek servis gelirse ondan güncellenir.
	if got2 := mergeExceptionGroup(ExceptionGroup{Fingerprint: fp, Service: "svc-cards", LastSeen: 3}, prev); got2.Service != "svc-cards" {
		t.Fatalf("yeni gerçek servis: %q", got2.Service)
	}
	// Span grubunda kural yok (servis hash'in parçası, değişmez zaten).
	span := mergeExceptionGroup(ExceptionGroup{Fingerprint: "abc", Service: "oracle:x"}, &ExceptionGroup{Fingerprint: "abc", Service: "svc"})
	if span.Service != "oracle:x" {
		t.Fatalf("span grubu: %q", span.Service)
	}
}

// Okuma SQL'leri sınırlı: FINAL (RMT), zaman aralığı, LIMIT, max_execution_time;
// ağırlık EffectiveWeight'in SQL ikizi.
func TestOracleGroupSQLBounded(t *testing.T) {
	for name, q := range map[string]string{
		"toplama": oracleGroupAggSQL(), "satırlar": oracleErrorsByOpCodeSQL(50), "seri": oracleGroupOccSQL(),
	} {
		for _, want := range []string{"FROM oracle_error_log FINAL", "source_id = ?", "time >= ?", "LIMIT ", "max_execution_time"} {
			if !strings.Contains(q, want) {
				t.Errorf("%s SQL %q içermeli:\n%s", name, want, q)
			}
		}
	}
	if !strings.Contains(oracleGroupAggSQL(), "time < ?") || !strings.Contains(oracleGroupAggSQL(), "GROUP BY error_code, operation_code, channel_code, m") ||
		!strings.Contains(oracleGroupAggSQL(), "ORDER BY m ASC") {
		t.Fatalf("toplama yarı açık aralık + üçlü: %s", oracleGroupAggSQL())
	}
	if !strings.Contains(oracleGroupAggSQL(), "greatest(toUInt64OrZero(attr_values[indexOf(attr_keys, 'coremetry.weight')]), 1)") {
		t.Fatalf("ağırlık ifadesi: %s", oracleGroupAggSQL())
	}
	if q := oracleGroupSourceSQL(); !strings.Contains(q, "LIMIT 50") || !strings.Contains(q, "max_execution_time") || !strings.Contains(q, "time >= ? AND time <= ?") {
		t.Fatalf("kaynak çözümü sınırlı olmalı: %s", q)
	}
}

// groupRow — GetExceptionGroup'un QueryRow'una tek grup satırı (snap=false
// sırası: exGroupScanDest) basar.
type groupRow struct{ g ExceptionGroup }

func (r groupRow) Err() error                { return nil }
func (r groupRow) ScanStruct(dest any) error { return nil }
func (r groupRow) Scan(dest ...any) error {
	vals := []any{r.g.Fingerprint, r.g.Type, r.g.Message, r.g.Service, r.g.State, r.g.Assignee,
		r.g.FirstSeen, r.g.LastSeen, nil, r.g.Occurrences, "", "", int64(0)}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = vals[i].(string)
		case *int64:
			*p = vals[i].(int64)
		case *uint64:
			*p = vals[i].(uint64)
		}
	}
	return nil
}

type branchConn struct {
	capConn
	g ExceptionGroup
}

func (c *branchConn) QueryRow(_ context.Context, q string, _ ...any) driver.Row {
	return groupRow{g: c.g}
}

// Örnek + oluşum ucu `ora:` önekinde Oracle satırlarına dallanır (ilk sorgu
// oracle_error_log kaynak çözümü), span grubunda spans'a gider.
func TestExceptionReadsBranchOnOraclePrefix(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UnixNano()
	ora := ExceptionGroup{Fingerprint: OracleGroupFingerprint("o-1", "APP_ERR_001", "OP_A"), Type: "APP_ERR_001", Message: "OP_A",
		Service: "svc-a", State: ExStateNew, FirstSeen: now - int64(time.Hour), LastSeen: now, Occurrences: 9}
	c := &branchConn{g: ora}
	s := &Store{conn: c}
	_, _ = s.GetExceptionGroupSamples(ctx, ora.Fingerprint, 10)
	_, _ = s.GetExceptionOccurrences(ctx, ora.Fingerprint)
	if len(c.got) != 2 {
		t.Fatalf("2 sorgu bekleniyordu: %d", len(c.got))
	}
	for _, q := range c.got {
		if !strings.Contains(q, "FROM oracle_error_log") || strings.Contains(q, "FROM spans") {
			t.Fatalf("Oracle grubu oracle_error_log'a gitmeli:\n%s", q)
		}
	}
	span := ora
	span.Fingerprint = "0a1b2c3d4e5f6a7b"
	c2 := &branchConn{g: span}
	s2 := &Store{conn: c2}
	_, _ = s2.GetExceptionGroupSamples(ctx, span.Fingerprint, 10)
	_, _ = s2.GetExceptionOccurrences(ctx, span.Fingerprint)
	for _, q := range c2.got {
		if !strings.Contains(q, "FROM spans") {
			t.Fatalf("span grubu spans'a gitmeli:\n%s", q)
		}
	}
}

// Örnek kartı: gövde yoksa "kod · operasyon"; kırılım durum satırında.
func TestOracleSampleFromRow(t *testing.T) {
	r := OracleErrorRow{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", Time: time.Unix(100, 0), ErrorCode: "APP_ERR_001 ", OperationCode: "OP_A",
		ChannelCode: "MOB", HostName: "db-host-01", AttrKeys: []string{OracleWeightAttr}, AttrValues: []string{"4"}}
	sm := oracleSampleFromRow(r)
	if sm.Message != "APP_ERR_001 · OP_A" || sm.SpanName != "OP_A" || sm.StatusMsg != "kanal MOB · host db-host-01 · ×4" || sm.TraceID == "" || sm.Time != 100e9 {
		t.Fatalf("örnek: %+v", sm)
	}
}

// Oluşum serisi penceresi: son görülmeden en çok 24 sa geri.
func TestOracleOccWindow(t *testing.T) {
	last := int64(100 * time.Hour)
	if f, l := oracleOccWindow(0, last); f != last-int64(24*time.Hour) || l != last {
		t.Fatalf("uzun ömür kırpılır: %d %d", f, l)
	}
	first := last - int64(time.Hour)
	if f, _ := oracleOccWindow(first, last); f != first {
		t.Fatalf("kısa ömür ilk görülmeden: %d", f)
	}
	if f, l := oracleOccWindow(last, last); l <= f {
		t.Fatalf("dejenere pencere: %d %d", f, l)
	}
}
