package chstore

// log_templates_ttl_test.go — v0.10.1116: log_templates 30 günlük TTL.
// DDL / ALTER metin pinleri (ifade + aralık), tek-düğüm / küme varyantları,
// tek seferlik göçün işaret idempotansı ve hata yönleri, purge sınıflaması.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

func squashWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// İfade: DateTime64(9) kolonu toDateTime ile DateTime'a iner (CH code 450),
// toDate YOK (partition yok; alt-gün tuzağı ailesi), aralık sabitle aynı.
func TestLogTemplatesTTLExpr(t *testing.T) {
	if logTemplatesTTLExpr != "toDateTime(last_seen) + INTERVAL 30 DAY" {
		t.Fatalf("TTL ifadesi kaydı: %q", logTemplatesTTLExpr)
	}
	if !strings.Contains(logTemplatesTTLExpr, fmt.Sprintf("INTERVAL %d DAY", LogTemplatesTTLDays)) {
		t.Errorf("ifade %q LogTemplatesTTLDays=%d ile ayrışmış", logTemplatesTTLExpr, LogTemplatesTTLDays)
	}
	if strings.Contains(logTemplatesTTLExpr, "toDate(") {
		t.Errorf("toDate() kullanılmamalı: %q", logTemplatesTTLExpr)
	}
	if LogTemplatesTTLDays < 7 {
		t.Errorf("TTL %d gün < dedektörün 7 günlük bilinen-şablon ufku", LogTemplatesTTLDays)
	}
}

// Yeni kurulum: CREATE TTL'i ORDER BY'dan sonra taşır; partition yok →
// ttl_only_drop_parts da yok.
func TestLogTemplatesCreateCarriesTTL(t *testing.T) {
	ddl := tableDDLByName(canonicalTables(30, 30, 7), "log_templates")
	if ddl == "" {
		t.Fatal("log_templates CREATE bulunamadı")
	}
	got := squashWS(ddl)
	if !strings.Contains(got, "ENGINE = ReplacingMergeTree(version) ORDER BY id TTL toDateTime(last_seen) + INTERVAL 30 DAY") {
		t.Errorf("CREATE TTL taşımıyor:\n%s", got)
	}
	for _, bad := range []string{"PARTITION BY", "ttl_only_drop_parts", "toDate(last_seen)"} {
		if strings.Contains(got, bad) {
			t.Errorf("CREATE %q içermemeli:\n%s", bad, got)
		}
	}
}

// Tek seferlik ALTER metni: ifade aynı, bir kerelik materialize, beklemesiz.
func TestLogTemplatesTTLAlterSQL(t *testing.T) {
	want := "ALTER TABLE log_templates MODIFY TTL toDateTime(last_seen) + INTERVAL 30 DAY" +
		" SETTINGS materialize_ttl_after_modify = 1, alter_sync = 0"
	if got := logTemplatesTTLAlterSQL(); got != want {
		t.Errorf("ALTER:\n got %q\nwant %q", got, want)
	}
}

// Tek düğüm: ifadeler değişmeden. Küme: state tablosu → ON CLUSTER enjekte,
// `_local` YOK, Distributed sarmalayıcı YOK; CREATE Replicated'a çevrilir ve
// TTL'i korur.
func TestLogTemplatesTTLAdaptDDL(t *testing.T) {
	single := &Store{}
	if got := single.adaptDDL(logTemplatesTTLAlterSQL()); len(got) != 1 || got[0] != logTemplatesTTLAlterSQL() {
		t.Errorf("tek düğüm ALTER değişmemeli: %v", got)
	}
	cl := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	alter := cl.adaptDDL(logTemplatesTTLAlterSQL())
	if len(alter) != 1 {
		t.Fatalf("küme ALTER tek parça olmalı (sarmalayıcı yok), gelen %d: %v", len(alter), alter)
	}
	wantAlter := "ALTER TABLE log_templates ON CLUSTER `c` MODIFY TTL toDateTime(last_seen) + INTERVAL 30 DAY" +
		" SETTINGS materialize_ttl_after_modify = 1, alter_sync = 0"
	// adaptDDL enjeksiyonu bir boşluk fazlası bırakır; anlam aynı.
	if squashWS(alter[0]) != wantAlter {
		t.Errorf("küme ALTER:\n got %q\nwant %q", alter[0], wantAlter)
	}
	create := cl.adaptDDL(tableDDLByName(canonicalTables(30, 30, 7), "log_templates"))
	if len(create) != 1 {
		t.Fatalf("küme CREATE tek parça olmalı (state tablosu, sarmalayıcı yok), gelen %d", len(create))
	}
	c := squashWS(create[0])
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS log_templates ON CLUSTER `c`",
		"ENGINE = ReplicatedReplacingMergeTree(",
		"ORDER BY id TTL toDateTime(last_seen) + INTERVAL 30 DAY",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("küme CREATE %q içermeli:\n%s", want, c)
		}
	}
	for _, bad := range []string{"log_templates_local", "Distributed("} {
		if strings.Contains(c, bad) {
			t.Errorf("küme CREATE %q içermemeli:\n%s", bad, c)
		}
	}
}

func TestPlanLogTemplatesTTL(t *testing.T) {
	const noTTL = "ReplacingMergeTree(version) ORDER BY id SETTINGS index_granularity = 8192"
	const withTTL = "ReplacingMergeTree(version) ORDER BY id TTL toDateTime(last_seen) + toIntervalDay(30) SETTINGS index_granularity = 8192"
	const replNoTTL = "ReplicatedReplacingMergeTree('/clickhouse/tables/state/log_templates', '{shard}-{replica}', version) ORDER BY id SETTINGS index_granularity = 8192"
	const replWithTTL = "ReplicatedReplacingMergeTree('/clickhouse/tables/state/log_templates', '{shard}-{replica}', version) ORDER BY id TTL toDateTime(last_seen) + toIntervalDay(30) SETTINGS index_granularity = 8192"
	const otherTTL = "ReplacingMergeTree(version) ORDER BY id TTL toDateTime(first_seen) + toIntervalDay(90) SETTINGS index_granularity = 8192"
	cases := []struct {
		name   string
		marker bool
		exists bool
		ef     string
		want   string
	}{
		{"işaret var → hiçbir şey (tablo durumu sorulmaz)", true, true, noTTL, LogTemplatesTTLDone},
		{"işaret var, tablo yok", true, false, "", LogTemplatesTTLDone},
		{"tablo yok → işaretsiz bekle", false, false, "", LogTemplatesTTLAbsent},
		{"mevcut kurulum, TTL yok → ALTER", false, true, noTTL, LogTemplatesTTLApplied},
		{"küme, TTL yok → ALTER", false, true, replNoTTL, LogTemplatesTTLApplied},
		{"yeni kurulum, CREATE TTL'li → yalnız işaret", false, true, withTTL, LogTemplatesTTLPresent},
		{"küme, CREATE TTL'li → yalnız işaret", false, true, replWithTTL, LogTemplatesTTLPresent},
		{"operatörün başka TTL'i ezilmez", false, true, otherTTL, LogTemplatesTTLPresent},
	}
	for _, c := range cases {
		if got := planLogTemplatesTTL(c.marker, c.exists, c.ef); got != c.want {
			t.Errorf("%s: %q, istenen %q", c.name, got, c.want)
		}
	}
}

// fakeLTTTLStore — bellek içi göç yüzeyi.
type fakeLTTTLStore struct {
	settings   map[string][]byte
	engineFull string
	exists     bool
	getErr     error
	probeErr   error
	execErr    error
	putErr     error
	execs      []string
	puts       int
	probes     int
}

func (f *fakeLTTTLStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.settings[key], nil
}

func (f *fakeLTTTLStore) PutSetting(_ context.Context, key string, value []byte) error {
	if f.putErr != nil {
		return f.putErr
	}
	if f.settings == nil {
		f.settings = map[string][]byte{}
	}
	f.settings[key] = value
	f.puts++
	return nil
}

func (f *fakeLTTTLStore) logTemplatesEngineFull(context.Context) (string, bool, error) {
	f.probes++
	return f.engineFull, f.exists, f.probeErr
}

func (f *fakeLTTTLStore) execDDL(_ context.Context, sql string) error {
	if f.execErr != nil {
		return f.execErr
	}
	f.execs = append(f.execs, sql)
	// Uygulanan ALTER tabloya TTL kazandırır (gerçek CH gibi).
	f.engineFull += " TTL toDateTime(last_seen) + toIntervalDay(30)"
	return nil
}

func TestApplyLogTemplatesTTL_IdempotentMarker(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	st := &fakeLTTTLStore{exists: true, engineFull: "ReplacingMergeTree(version) ORDER BY id"}

	out, err := applyLogTemplatesTTL(ctx, st, now)
	if err != nil || out != LogTemplatesTTLApplied {
		t.Fatalf("ilk koşu: %q %v", out, err)
	}
	if len(st.execs) != 1 || st.execs[0] != logTemplatesTTLAlterSQL() {
		t.Fatalf("tek ALTER beklenirdi: %v", st.execs)
	}
	var m logTemplatesTTLMarker
	if err := json.Unmarshal(st.settings[logTemplatesTTLMarkerKey], &m); err != nil {
		t.Fatalf("işaret çözülemedi: %v", err)
	}
	if m.Outcome != LogTemplatesTTLApplied || m.Version != logTemplatesTTLVersion || m.TTLDays != 30 || m.AppliedAt != now.UnixNano() {
		t.Errorf("işaret gövdesi: %+v", m)
	}

	// İkinci (ve sonraki) koşular: işaret var → ALTER yok, prob yok, yazım yok.
	for i := 0; i < 3; i++ {
		out, err = applyLogTemplatesTTL(ctx, st, now.Add(time.Hour))
		if err != nil || out != LogTemplatesTTLDone {
			t.Fatalf("tekrar koşu %d: %q %v", i, out, err)
		}
	}
	if len(st.execs) != 1 || st.puts != 1 || st.probes != 1 {
		t.Errorf("idempotans bozuk: execs=%d puts=%d probes=%d", len(st.execs), st.puts, st.probes)
	}
}

func TestApplyLogTemplatesTTL_PresentAndAbsent(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(0, 1)

	present := &fakeLTTTLStore{exists: true, engineFull: "ReplacingMergeTree(version) ORDER BY id TTL toDateTime(last_seen) + toIntervalDay(30)"}
	if out, err := applyLogTemplatesTTL(ctx, present, now); err != nil || out != LogTemplatesTTLPresent {
		t.Fatalf("present: %q %v", out, err)
	}
	if len(present.execs) != 0 || present.puts != 1 {
		t.Errorf("present: ALTER gönderilmemeli, işaret yazılmalı: execs=%v puts=%d", present.execs, present.puts)
	}

	absent := &fakeLTTTLStore{exists: false}
	if out, err := applyLogTemplatesTTL(ctx, absent, now); err != nil || out != LogTemplatesTTLAbsent {
		t.Fatalf("absent: %q %v", out, err)
	}
	if len(absent.execs) != 0 || absent.puts != 0 {
		t.Errorf("absent: ne ALTER ne işaret: execs=%v puts=%d", absent.execs, absent.puts)
	}
	// Ertelenmiş CREATE indi (TTL'li) → sonraki boot yalnız işaret yazar.
	absent.exists = true
	absent.engineFull = "ReplicatedReplacingMergeTree('/p/state/log_templates', '{shard}-{replica}', version) ORDER BY id TTL toDateTime(last_seen) + toIntervalDay(30)"
	if out, err := applyLogTemplatesTTL(ctx, absent, now); err != nil || out != LogTemplatesTTLPresent {
		t.Fatalf("absent→present: %q %v", out, err)
	}
	if len(absent.execs) != 0 || absent.puts != 1 {
		t.Errorf("absent→present: execs=%v puts=%d", absent.execs, absent.puts)
	}
}

// Hata yönü: işaret YALNIZ başarıdan sonra; düşen tur sonraki boot'ta yeniden.
func TestApplyLogTemplatesTTL_FailuresLeaveNoMarker(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(0, 1)
	boom := errors.New("boom")

	getFail := &fakeLTTTLStore{exists: true, getErr: boom}
	if _, err := applyLogTemplatesTTL(ctx, getFail, now); err == nil {
		t.Error("işaret okunamadı → hata beklenirdi")
	}
	if getFail.probes != 0 || len(getFail.execs) != 0 {
		t.Error("işaret okunamadan kör koşulmamalı")
	}

	probeFail := &fakeLTTTLStore{probeErr: boom}
	if _, err := applyLogTemplatesTTL(ctx, probeFail, now); err == nil {
		t.Error("prob düştü → hata beklenirdi")
	}
	if len(probeFail.execs) != 0 || probeFail.puts != 0 {
		t.Error("prob düşünce ALTER/işaret yok")
	}

	execFail := &fakeLTTTLStore{exists: true, engineFull: "ReplacingMergeTree(version) ORDER BY id", execErr: boom}
	if _, err := applyLogTemplatesTTL(ctx, execFail, now); err == nil {
		t.Error("ALTER düştü → hata beklenirdi")
	}
	if execFail.puts != 0 {
		t.Error("ALTER düşünce işaret YAZILMAMALI")
	}
	execFail.execErr = nil // sonraki boot
	if out, err := applyLogTemplatesTTL(ctx, execFail, now); err != nil || out != LogTemplatesTTLApplied || execFail.puts != 1 {
		t.Errorf("yeniden deneme: %q %v puts=%d", out, err, execFail.puts)
	}

	putFail := &fakeLTTTLStore{exists: true, engineFull: "ReplacingMergeTree(version) ORDER BY id", putErr: boom}
	if out, err := applyLogTemplatesTTL(ctx, putFail, now); err == nil || out != LogTemplatesTTLApplied {
		t.Errorf("işaret yazılamadı → sonuç applied + hata: %q %v", out, err)
	}
	if len(putFail.execs) != 1 {
		t.Errorf("ALTER bir kez gönderilmiş olmalı: %v", putFail.execs)
	}
}

// Purge sınıflaması değişmedi: log_templates purge.go'da YALNIZ korunan
// listede (fabrika sıfırlaması onu silmez); TTL bunu değiştirmez.
func TestLogTemplatesPurgeClassificationUnchanged(t *testing.T) {
	n := 0
	for _, name := range configPreserveTables {
		if name == "log_templates" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("log_templates configPreserveTables'ta tam bir kez olmalı, %d", n)
	}
	for _, name := range telemetryPurgeTables {
		if name == "log_templates" {
			t.Error("log_templates purge allowlist'ine girmemeli")
		}
	}
}
