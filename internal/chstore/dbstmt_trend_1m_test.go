package chstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

// dbstmt_trend_1m_test.go — v0.10.1525 (operatör, prod 2026-10-24): Statement
// detayının trend grafikleri 5 dk kovaydı. Pinlenen sözleşme:
//
//  1. db_statement_summary_1m katalogun SONUNDA, DDL golden; ORDER BY
//     stmt_hash ile başlar (filtre öznesi), kardeşin dört trend state'ini ve
//     db_name zincirini/WHERE'ini birebir taşır (aynı *Merge okuyucusu);
//  2. kolon kapısı (mvGuardedOff) kardeşle aynı karar; küme kipi + purge;
//  3. seçici: ≤ 6 sa VE kapsama → 1m/60 sn, aksi 5m (bugünkü kabalaşma);
//  4. SQL sınırları: zaman-sınırlı WHERE (alt sınır kaynağın ızgarasına
//     hizalı, üst sınır DIŞLAYICI), LIMIT 400, max_execution_time;
//     1m yolu en çok 361 kova (< LIMIT).

const dbStmtSummary1mGolden = `CREATE MATERIALIZED VIEW IF NOT EXISTS db_statement_summary_1m ENGINE = AggregatingMergeTree ` +
	`PARTITION BY toDate(time_bucket) ORDER BY (stmt_hash, db_system, db_name, service_name, time_bucket) ` +
	`TTL toDate(time_bucket) + INTERVAL 7 DAY SETTINGS index_granularity = 8192 AS SELECT db_system, ` +
	`coalesce(nullIf(attr_values[indexOf(attr_keys, 'db.name')], ''), 'default') AS db_name, service_name, ` +
	`db_stmt_hash AS stmt_hash, toStartOfInterval(time, INTERVAL 1 MINUTE) AS time_bucket, ` +
	`countState() AS span_count_state, countIfState(status_code = 'error') AS error_count_state, ` +
	`sumState(duration) AS duration_sum_state, quantilesTDigestState(0.5, 0.95, 0.99)(duration) AS duration_q_state ` +
	`FROM spans WHERE db_stmt_hash != 0 GROUP BY db_system, db_name, service_name, stmt_hash, time_bucket`

func TestDBStmtSummary1mCatalogue(t *testing.T) {
	names := canonicalMVNames()
	// v0.10.2402 — ardında yalnız daha yeni iki MV durabilir (pinleri
	// mv_raw_agg_test.go'da: service_meta_combo_5m, db_receiver_instance_5m).
	if n := len(names); names[n-3] != "db_statement_summary_1m" ||
		names[n-2] != "service_meta_combo_5m" || names[n-1] != "db_receiver_instance_5m" {
		t.Fatalf("katalog sonu db_statement_summary_1m, service_meta_combo_5m, db_receiver_instance_5m olmalı: %v", names[n-3:])
	}
	got := sqlNormalize(canonicalMVDDL("db_statement_summary_1m"))
	if got != dbStmtSummary1mGolden {
		t.Fatalf("DDL golden'dan saptı:\n got: %s\nwant: %s", got, dbStmtSummary1mGolden)
	}
	if strings.Contains(canonicalMVDDL("db_statement_summary_5m"), "INTERVAL 1 MINUTE") {
		t.Fatal("5m / 1m ad çözümü çakıştı")
	}
	// Kardeşin db_name zinciri, WHERE'i ve trend okuyucusunun dört state'i birebir.
	sib := sqlNormalize(canonicalMVDDL("db_statement_summary_5m"))
	for _, frag := range []string{
		"coalesce(nullIf(attr_values[indexOf(attr_keys, 'db.name')], ''), 'default') AS db_name",
		"db_stmt_hash AS stmt_hash",
		"countState() AS span_count_state",
		"countIfState(status_code = 'error') AS error_count_state",
		"sumState(duration) AS duration_sum_state",
		"quantilesTDigestState(0.5, 0.95, 0.99)(duration) AS duration_q_state",
		"WHERE db_stmt_hash != 0",
	} {
		if !strings.Contains(sib, frag) || !strings.Contains(got, frag) {
			t.Errorf("kardeşle ortak parça ayrıştı: %q", frag)
		}
	}
}

func TestDBStmtSummary1mGatesAndCluster(t *testing.T) {
	off := &Store{hasDBStmtHashCol: false}
	on := &Store{hasDBStmtHashCol: true}
	if !off.mvGuardedOff("db_statement_summary_1m") || on.mvGuardedOff("db_statement_summary_1m") {
		t.Fatal("1m ikiz db_stmt_hash kapısıyla kardeşle AYNI kararı vermeli (kod 16 ingest bloğu sınıfı)")
	}
	if !highVolumeTables["db_statement_summary_1m"] || !tablesWithoutTraceID["db_statement_summary_1m"] ||
		defaultShardPolicy["db_statement_summary_1m"] != defaultShardPolicy["db_statement_summary_5m"] {
		t.Fatal("üç kayıt (highVolumeTables / tablesWithoutTraceID / shard anahtarı) kardeşle aynı olmalı")
	}
	s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	got := s.adaptDDL(canonicalMVDDL("db_statement_summary_1m"))
	if len(got) != 2 {
		t.Fatalf("küme kipi: _local MV + Distributed sarmalayıcı beklenir, %d ifade", len(got))
	}
	local := sqlNormalize(got[0])
	for _, want := range []string{
		"CREATE MATERIALIZED VIEW IF NOT EXISTS db_statement_summary_1m_local ON CLUSTER `c` ",
		"ReplicatedAggregatingMergeTree('/p/{shard}/db_statement_summary_1m', '{replica}')",
		"FROM spans_local WHERE db_stmt_hash != 0",
	} {
		if !strings.Contains(local, want) {
			t.Errorf("_local DDL %q taşımıyor:\n%s", want, local)
		}
	}
	if !strings.Contains(got[1], "Distributed(`c`, currentDatabase(), db_statement_summary_1m_local, cityHash64(toString(stmt_hash)))") {
		t.Errorf("Distributed sarmalayıcı: %s", got[1])
	}
	found := false
	for _, n := range telemetryPurgeTables {
		found = found || n == "db_statement_summary_1m"
	}
	if !found {
		t.Fatal("db_statement_summary_1m telemetri purge listesinde yok")
	}
}

func TestDBStmtTrendGrainFor(t *testing.T) {
	to := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	fine := DBStmtTrendGrain{Table: "db_statement_summary_1m", GrainSec: 60, BucketSec: 60}
	coarse := func(w time.Duration) DBStmtTrendGrain {
		return DBStmtTrendGrain{Table: "db_statement_summary_5m", GrainSec: 300, BucketSec: dbStmtTrendBucketSec(to.Add(-w), to)}
	}
	cases := []struct {
		name   string
		window time.Duration
		covers bool
		want   DBStmtTrendGrain
	}{
		{"15 dk, kapsıyor", 15 * time.Minute, true, fine},
		{"1 sa, kapsıyor", time.Hour, true, fine},
		{"tam 6 sa (dahil)", 6 * time.Hour, true, fine},
		{"6 sa + 1 sn", 6*time.Hour + time.Second, true, coarse(6*time.Hour + time.Second)},
		{"24 sa", 24 * time.Hour, true, coarse(24 * time.Hour)},
		{"7 gün kabalaşır", 7 * 24 * time.Hour, true, coarse(7 * 24 * time.Hour)},
		{"1 sa, 1m tablo yok/boş/geç başlıyor", time.Hour, false, coarse(time.Hour)},
		{"sıfır pencere", 0, true, coarse(0)},
	}
	for _, c := range cases {
		if got := dbStmtTrendGrainFor(to.Add(-c.window), to, c.covers); got != c.want {
			t.Errorf("%s: %+v, beklenen %+v", c.name, got, c.want)
		}
	}
	if coarse(7*24*time.Hour).BucketSec != 2100 {
		t.Fatal("5m yolunun kabalaşması değişmemeli (7 gün = 2100 sn)")
	}
}

// Prob kablosu: kolon kapısı kapalıyken prob KOŞMAZ (conn nil → koşsa panik);
// uzun pencerede de koşmaz.
func TestDBStmtTrendGrainProbe(t *testing.T) {
	ctx := context.Background()
	to := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	seed := func(col bool, first time.Time, ok bool) *Store {
		s := &Store{hasDBStmtHashCol: col}
		p := &s.priorCoverage[priorSrcDBStmtSummary1m]
		p.at, p.min, p.ok = time.Now(), first, ok
		return s
	}
	if g := (*Store)(nil).DBStmtTrendGrain(ctx, from, to); g.Table != "db_statement_summary_5m" {
		t.Errorf("nil Store → 5m, %+v", g)
	}
	if g := (&Store{}).DBStmtTrendGrain(ctx, from, to); g.Table != "db_statement_summary_5m" {
		t.Errorf("kolon yok → 5m (prob koşmaz), %+v", g)
	}
	if g := seed(true, time.Time{}, false).DBStmtTrendGrain(ctx, from, to); g.Table != "db_statement_summary_5m" {
		t.Errorf("prob başarısız → 5m, %+v", g)
	}
	if g := seed(true, from.Add(10*time.Minute), true).DBStmtTrendGrain(ctx, from, to); g.Table != "db_statement_summary_5m" {
		t.Errorf("1m tablo pencereden geç başlıyor → 5m, %+v", g)
	}
	if g := seed(true, from.Add(-24*time.Hour), true).DBStmtTrendGrain(ctx, from, to); g.Table != "db_statement_summary_1m" || g.BucketSec != 60 {
		t.Errorf("kapsıyor → 1m/60, %+v", g)
	}
	if g := (&Store{hasDBStmtHashCol: true}).DBStmtTrendGrain(ctx, to.Add(-24*time.Hour), to); g.Table != "db_statement_summary_5m" {
		t.Errorf("24 sa → 5m (prob koşmaz), %+v", g)
	}
}

func TestDBStmtTrendSQLBounds(t *testing.T) {
	q := DBStmtDetailQuery{Hash: 0xC0FFEE, DBSystem: "postgresql",
		From: time.Date(2026, 10, 24, 10, 3, 27, 0, time.UTC), To: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	wc := dbStmtDetailWhereGrain(q, time.Minute)
	sql := dbStmtTrendSQLFrom("db_statement_summary_1m", wc.sql())
	for _, want := range []string{"FROM db_statement_summary_1m", "stmt_hash = ?", "time_bucket >= ?", "time_bucket < ?",
		"LIMIT 400", "max_execution_time = 10"} {
		if !strings.Contains(sql, want) {
			t.Errorf("1m trend SQL %q taşımıyor:\n%s", want, sql)
		}
	}
	// Alt sınır 1 dk'ya hizalı (5 dk'ya değil): 10:03:27 → 10:03.
	if got := wc.args[1].(time.Time); !got.Equal(time.Date(2026, 10, 24, 10, 3, 0, 0, time.UTC)) {
		t.Errorf("1m alt sınır %s, beklenen 10:03", got)
	}
	if got := dbStmtDetailWhere(q).args[1].(time.Time); !got.Equal(time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("5m alt sınır %s, beklenen 10:00", got)
	}
	// Bilinmeyen tablo adı SQL'e giremez.
	if s := dbStmtTrendSQLFrom("spans; DROP", wc.sql()); !strings.Contains(s, "FROM db_statement_summary_5m") {
		t.Error("bilinmeyen tablo 5m'e düşmeli")
	}
	// 1m yolu LIMIT'in altında: 6 sa = 360 kova (+1 hizalama payı).
	if n := int64(dbStmtTrendFineMaxWindow/time.Minute) + 1; n >= 400 {
		t.Fatalf("1m yolu %d kova — LIMIT 400 kesebilir", n)
	}
}
