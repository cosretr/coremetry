package chstore

// retention_current_test.go — v0.10.1869.
//
// Belirti: küme kipinde pod açılışta ~4,5 dk hazır olamıyordu; boot kayıtlı
// saklama sürelerini koşulsuz yeniden uyguluyor (13 ALTER, yavaş dağıtık DDL
// kuyruğunda ifade başına ~20 sn).
//
// Bu testlerin koruduğu şey hız DEĞİL, DOĞRULUK: atlama kararı yanlış
// verilirse TTL bir host'ta eski kalır ve kimse fark etmez (v0.9.620 sınıfı).
// Kural "emin değilsen uygula".
//
// MUTASYON KANITI: ttlCurrentEverywhere'de host sayısı kontrolünü sil →
// "eksik host" satırı kızarır; döngüyü ilk host'ta kes → "tek host ayrışmış"
// kızarır; createQueriesEverywhere'e skip_unavailable_shards ekle → kaynak
// pini kızarır.

import (
	"os"
	"strings"
	"testing"
)

const spansLocalCreate = "CREATE TABLE acme.spans_local (`time` DateTime64(9) CODEC(Delta(8), ZSTD(3)), `trace_id` String CODEC(ZSTD(3)), `service_name` LowCardinality(String)) ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/acme/spans_local', '{replica}') PARTITION BY toDate(time) ORDER BY (service_name, time) TTL toDate(time) + toIntervalDay(30) SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1"

const innerCreate = "CREATE TABLE acme.`.inner_id.6f1c2a34-0000-4000-8000-000000000001` (`time_bucket` DateTime, `service_name` LowCardinality(String), `span_count_state` AggregateFunction(count), `slow_exemplar_state` AggregateFunction(argMax, String, Int64) TTL toDate(time_bucket) + toIntervalDay(30), `error_exemplar_state` AggregateFunction(argMaxIf, String, Int64, UInt8) TTL toDate(time_bucket) + toIntervalDay(30)) ENGINE = AggregatingMergeTree PARTITION BY toDate(time_bucket) ORDER BY (service_name, time_bucket) TTL toDate(time_bucket) + toIntervalDay(90) SETTINGS index_granularity = 8192"

func TestCanonicalTTL(t *testing.T) {
	same := [][2]string{
		{"toDate(time) + INTERVAL 30 DAY", "toDate(time) + toIntervalDay(30)"},
		{"toDateTime(time) + INTERVAL 6 HOUR", "toDateTime(time) + toIntervalHour(6)"},
		{"toDate(time) + INTERVAL 30 DAY", "toDate(`time`)+interval 30 day"},
		{"toDate(start_time) + INTERVAL 7 DAY", "toDate(start_time) + toIntervalDay( 7 )"},
	}
	for _, c := range same {
		if canonicalTTL(c[0]) != canonicalTTL(c[1]) {
			t.Errorf("aynı sayılmalıydı: %q vs %q (%q / %q)", c[0], c[1], canonicalTTL(c[0]), canonicalTTL(c[1]))
		}
	}
	diff := [][2]string{
		{"toDate(time) + INTERVAL 30 DAY", "toDate(time) + toIntervalDay(7)"},         // süre
		{"toDate(time) + INTERVAL 30 DAY", "toDate(time) + toIntervalHour(30)"},       // birim
		{"toDate(time) + INTERVAL 30 DAY", "toDateTime(time) + toIntervalDay(30)"},    // sarmalayıcı
		{"toDate(time) + INTERVAL 30 DAY", "toDate(timestamp) + toIntervalDay(30)"},   // kolon
		{"toDate(time) + INTERVAL 30 DAY", "toDate(time) + toIntervalDay(300)"},       // önek değil
		{"toDate(time) + INTERVAL 30 DAY", ""},                                        // TTL yok
		{"toDate(time) + INTERVAL 30 DAY", "toDate(time) + toIntervalDay(30) DELETE"}, // tanınmayan ek
	}
	for _, c := range diff {
		if canonicalTTL(c[0]) == canonicalTTL(c[1]) {
			t.Errorf("FARKLI sayılmalıydı: %q vs %q", c[0], c[1])
		}
	}
}

func TestTableTTLFromCreate(t *testing.T) {
	if got := tableTTLFromCreate(spansLocalCreate); got != "toDate(time) + toIntervalDay(30)" {
		t.Fatalf("tablo TTL'i = %q", got)
	}
	// Kolon TTL'i tablo TTL'i sanılmaz: inner tabloda tablo düzeyi 90 gün.
	if got := tableTTLFromCreate(innerCreate); got != "toDate(time_bucket) + toIntervalDay(90)" {
		t.Fatalf("inner tablo TTL'i = %q (kolon TTL'i ile karıştı mı?)", got)
	}
	noTTL := "CREATE TABLE acme.x (`a` String) ENGINE = MergeTree ORDER BY a SETTINGS index_granularity = 8192"
	if got := tableTTLFromCreate(noTTL); got != "" {
		t.Fatalf("TTL'siz tabloda %q döndü", got)
	}
	// Yalnız kolon TTL'i olan tabloda tablo TTL'i YOK.
	onlyCol := "CREATE TABLE acme.x (`a` String TTL toDate(t) + toIntervalDay(1), `t` DateTime) ENGINE = MergeTree ORDER BY a"
	if got := tableTTLFromCreate(onlyCol); got != "" {
		t.Fatalf("kolon TTL'i tablo TTL'i sanıldı: %q", got)
	}
	if got := tableTTLFromCreate("bozuk metin"); got != "" {
		t.Fatalf("bozuk metinde %q", got)
	}
}

func TestColumnTTLFromCreate(t *testing.T) {
	for _, col := range []string{"slow_exemplar_state", "error_exemplar_state"} {
		if got := columnTTLFromCreate(innerCreate, col); got != "toDate(time_bucket) + toIntervalDay(30)" {
			t.Errorf("%s kolon TTL'i = %q", col, got)
		}
	}
	if got := columnTTLFromCreate(innerCreate, "span_count_state"); got != "" {
		t.Errorf("TTL'siz kolonda %q", got)
	}
	if got := columnTTLFromCreate(innerCreate, "yok_boyle_kolon"); got != "" {
		t.Errorf("olmayan kolonda %q", got)
	}
	// Ad öneki eşleşmesi yok: slow_exemplar_state ≠ slow_exemplar.
	if got := columnTTLFromCreate(innerCreate, "slow_exemplar"); got != "" {
		t.Errorf("önek eşleşti: %q", got)
	}
}

func TestTTLCurrentEverywhere(t *testing.T) {
	want := "toDate(time) + INTERVAL 30 DAY"
	ok := "toDate(time) + toIntervalDay(30)"
	old := "toDate(time) + toIntervalDay(7)"
	cases := []struct {
		name     string
		observed []string
		hosts    int
		skip     bool
	}{
		{"dört host, hepsi güncel", []string{ok, ok, ok, ok}, 4, true},
		{"tek düğüm, güncel", []string{ok}, 1, true},
		{"tek host ayrışmış (v0.9.620 sınıfı)", []string{ok, ok, old, ok}, 4, false},
		{"son host ayrışmış", []string{ok, ok, ok, old}, 4, false},
		{"eksik host: 4 beklenir 3 geldi", []string{ok, ok, ok}, 4, false},
		{"fazla satır", []string{ok, ok}, 1, false},
		{"hiç satır", nil, 4, false},
		{"bir host'ta TTL yok", []string{ok, "", ok, ok}, 4, false},
		{"host sayısı okunamadı", []string{ok}, 0, false},
	}
	for _, c := range cases {
		if got := ttlCurrentEverywhere(want, c.observed, c.hosts); got != c.skip {
			t.Errorf("%s: atla=%v, beklenen %v", c.name, got, c.skip)
		}
	}
	if ttlCurrentEverywhere("", []string{""}, 1) {
		t.Error("boş istenen TTL atlama üretmemeli")
	}
}

func TestRetentionProbeTableAndColumns(t *testing.T) {
	want30 := "toDate(time) + INTERVAL 30 DAY"
	// Küme kipi: depolama <ad>_local, host başına bir satır.
	p := &retentionProbe{hosts: 2, creates: map[string][]string{
		"spans_local": {spansLocalCreate, spansLocalCreate},
	}}
	if !p.tableCurrent("spans", want30) {
		t.Error("iki host da güncel: atlanmalıydı")
	}
	if p.tableCurrent("spans", "toDate(time) + INTERVAL 7 DAY") {
		t.Error("istenen değer farklı: uygulanmalıydı")
	}
	if p.tableCurrent("logs", want30) {
		t.Error("tanımı okunamayan tablo: uygulanmalıydı")
	}
	// Hem çıplak hem _local depolama tablosu varsa emin değiliz → uygula.
	both := &retentionProbe{hosts: 1, creates: map[string][]string{
		"spans": {spansLocalCreate}, "spans_local": {spansLocalCreate},
	}}
	if both.tableCurrent("spans", want30) {
		t.Error("iki depolama tablosu birden: uygulanmalıydı")
	}
	// nil probe (admin PUT yolu / okuma hatası) HER ZAMAN uygula.
	var none *retentionProbe
	if none.tableCurrent("spans", want30) || none.columnsCurrent([]string{innerCreate}, exemplarStateCols, want30) {
		t.Error("nil probe atlama üretmemeli")
	}
	// Kolonlar: ikisi de her host'ta güncel olmalı.
	colWant := "toDate(time_bucket) + INTERVAL 30 DAY"
	if !p.columnsCurrent([]string{innerCreate, innerCreate}, exemplarStateCols, colWant) {
		t.Error("exemplar kolonları iki host'ta güncel: atlanmalıydı")
	}
	stale := strings.Replace(innerCreate, "AggregateFunction(argMaxIf, String, Int64, UInt8) TTL toDate(time_bucket) + toIntervalDay(30)", "AggregateFunction(argMaxIf, String, Int64, UInt8)", 1)
	if p.columnsCurrent([]string{innerCreate, stale}, exemplarStateCols, colWant) {
		t.Error("bir host'ta bir kolonun TTL'i yok: uygulanmalıydı")
	}
	if p.columnsCurrent([]string{innerCreate}, exemplarStateCols, colWant) {
		t.Error("eksik host: uygulanmalıydı")
	}
}

// Kaynak pinleri: atlama YALNIZ açılış yolunda; küme okuması erişilemeyen
// replikayı sessizce atlamaz.
func TestRetentionSkipOnlyOnBootPath(t *testing.T) {
	src, err := os.ReadFile("retention.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "return s.setRetention(ctx, sp, actor, false)") {
		t.Error("admin yolu (SetRetention) her zaman uygulamalı: skipCurrent=false")
	}
	if !strings.Contains(body, `return s.setRetention(ctx, sp, "system:boot", true)`) {
		t.Error("açılış yolu atlama denetimini kullanmıyor")
	}
	cur, err := os.ReadFile("retention_current.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := string(cur)
	i := strings.Index(fn, "func (s *Store) createQueriesEverywhere")
	j := strings.Index(fn, "// retentionProbe —")
	if i < 0 || j < i {
		t.Fatal("createQueriesEverywhere bulunamadı")
	}
	q := fn[i:j]
	if strings.Contains(q, "skip_unavailable_shards") {
		t.Error("küme okuması erişilemeyen replikayı atlamamalı (eksik host = uygula)")
	}
	for _, must := range []string{"clusterAllReplicas", "max_execution_time", "LIMIT", "'Distributed'"} {
		if !strings.Contains(q, must) {
			t.Errorf("createQueriesEverywhere %q içermiyor", must)
		}
	}
}
