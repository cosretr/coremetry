package chstore

import (
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/config"
)

// db_summary_1m_test.go — v0.10.1095 (operatör kararı "Önerin A yapalım"):
// db_summary_5m'in 1 dakikalık ikizi. Pinlenen sözleşme:
//
//  1. katalogda (canonicalMVs) ve dilimin SONUNDA (konumsal pinler);
//  2. DDL golden — tek düğüm metni birebir;
//  3. kardeşten TÜRETİLEBİLİR: 5m DDL'inde yalnız ad / kova / TTL değişince
//     1m DDL'i çıkar — instance/db_name zinciri, WHERE, state kolonları
//     ayrışırsa iki tablonun satır kimliği ayrışır ve grafik karolarla
//     çelişir;
//  4. küme kipi: `_local` + ON CLUSTER + Replicated + FROM spans_local +
//     Distributed sarmalayıcı, kardeşle aynı shard anahtarı;
//  5. purge listesinde (telemetri temizliği 1m'i de boşaltır).

// sqlNormalize — SQL yorum satırlarını atar, boşlukları teke indirir.
func sqlNormalize(ddl string) string {
	var b strings.Builder
	for _, ln := range strings.Split(ddl, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "--") {
			continue
		}
		b.WriteString(ln)
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

const dbSummary1mGolden = `CREATE MATERIALIZED VIEW IF NOT EXISTS db_summary_1m ENGINE = AggregatingMergeTree ` +
	`PARTITION BY toDate(time_bucket) ORDER BY (db_system, instance, db_name, time_bucket) ` +
	`TTL toDate(time_bucket) + INTERVAL 7 DAY SETTINGS index_granularity = 8192 AS SELECT db_system, ` +
	`coalesce( nullIf(peer_service, ''), nullIf(attr_values[indexOf(attr_keys, 'server.address')], ''), ` +
	`nullIf(attr_values[indexOf(attr_keys, 'net.peer.name')], ''), nullIf(attr_values[indexOf(attr_keys, 'db.host')], ''), ` +
	`nullIf(attr_values[indexOf(attr_keys, 'db.name')], ''), nullIf(service_name, ''), 'unknown' ) AS instance, ` +
	`coalesce(nullIf(attr_values[indexOf(attr_keys, 'db.name')], ''), 'default') AS db_name, ` +
	`toStartOfInterval(time, INTERVAL 1 MINUTE) AS time_bucket, countState() AS span_count_state, ` +
	`countIfState(status_code = 'error') AS error_count_state, sumState(duration) AS duration_sum_state, ` +
	`quantilesTDigestState(0.5, 0.95, 0.99)(duration) AS duration_q_state FROM spans WHERE db_system != '' ` +
	`GROUP BY db_system, instance, db_name, time_bucket`

func TestDBSummary1mInCatalogueLast(t *testing.T) {
	names := canonicalMVNames()
	if len(names) == 0 || names[len(names)-1] != "db_summary_1m" {
		t.Fatalf("db_summary_1m katalogun SONUNDA olmalı (konumsal pinler), son: %v", names[len(names)-1])
	}
	if canonicalMVDDL("db_summary_1m") == "" {
		t.Fatal("canonicalMVDDL(db_summary_1m) boş")
	}
	// mvDDLByName ön-ek tuzağı: 5m adı 1m'i, 1m adı 5m'i bulmamalı.
	if strings.Contains(canonicalMVDDL("db_summary_5m"), "INTERVAL 1 MINUTE") ||
		strings.Contains(canonicalMVDDL("db_summary_1m"), "INTERVAL 5 MINUTE") {
		t.Fatal("db_summary_5m / db_summary_1m ad çözümü çakıştı")
	}
}

func TestDBSummary1mDDLGolden(t *testing.T) {
	got := sqlNormalize(canonicalMVDDL("db_summary_1m"))
	if got != dbSummary1mGolden {
		t.Fatalf("DDL golden'dan saptı:\n got: %s\nwant: %s", got, dbSummary1mGolden)
	}
	if strings.Contains(got, "quantilesState(") {
		t.Fatal("rezervuar quantilesState yasak — quantilesTDigestState")
	}
}

func TestDBSummary1mDerivesFromSibling(t *testing.T) {
	sib := sqlNormalize(canonicalMVDDL("db_summary_5m"))
	derived := strings.NewReplacer(
		"db_summary_5m", "db_summary_1m",
		"INTERVAL 5 MINUTE", "INTERVAL 1 MINUTE",
		"INTERVAL 90 DAY", "INTERVAL 7 DAY",
	).Replace(sib)
	if derived != sqlNormalize(canonicalMVDDL("db_summary_1m")) {
		t.Fatalf("db_summary_1m kardeşinden yalnız ad/kova/TTL ile ayrışmalı:\n5m→1m: %s\n   1m: %s",
			derived, sqlNormalize(canonicalMVDDL("db_summary_1m")))
	}
	// State kolonları sırasıyla aynı (aynı *Merge okuyucuları).
	states := regexp.MustCompile(`AS ([a-z_]+_state)`)
	a := states.FindAllStringSubmatch(sib, -1)
	b := states.FindAllStringSubmatch(sqlNormalize(canonicalMVDDL("db_summary_1m")), -1)
	if len(a) != len(b) || len(a) != 4 {
		t.Fatalf("state kolon sayısı %d vs %d", len(a), len(b))
	}
}

func TestDBSummary1mClusterDDL(t *testing.T) {
	if !highVolumeTables["db_summary_1m"] || !tablesWithoutTraceID["db_summary_1m"] ||
		defaultShardPolicy["db_summary_1m"] != defaultShardPolicy["db_summary_5m"] {
		t.Fatal("üç kayıt (highVolumeTables / tablesWithoutTraceID / shard anahtarı) kardeşle aynı olmalı")
	}
	s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	got := s.adaptDDL(canonicalMVDDL("db_summary_1m"))
	if len(got) != 2 {
		t.Fatalf("küme kipi: _local MV + Distributed sarmalayıcı beklenir, %d ifade: %#v", len(got), got)
	}
	local := sqlNormalize(got[0])
	for _, want := range []string{
		"CREATE MATERIALIZED VIEW IF NOT EXISTS db_summary_1m_local ON CLUSTER `c` ",
		"ENGINE = ReplicatedAggregatingMergeTree('/p/{shard}/db_summary_1m', '{replica}')",
		"FROM spans_local WHERE db_system != ''",
		"TTL toDate(time_bucket) + INTERVAL 7 DAY",
	} {
		if !strings.Contains(local, want) {
			t.Errorf("_local DDL %q taşımıyor:\n%s", want, local)
		}
	}
	const wrap = "CREATE TABLE IF NOT EXISTS db_summary_1m ON CLUSTER `c` AS db_summary_1m_local " +
		"ENGINE = Distributed(`c`, currentDatabase(), db_summary_1m_local, cityHash64(db_system))"
	if got[1] != wrap {
		t.Errorf("Distributed sarmalayıcı golden'dan saptı:\n got: %s\nwant: %s", got[1], wrap)
	}
	// Tek düğüm: değişmeden geçer.
	single := (&Store{}).adaptDDL(canonicalMVDDL("db_summary_1m"))
	if len(single) != 1 || single[0] != canonicalMVDDL("db_summary_1m") {
		t.Error("tek düğümde DDL değişmeden geçmeli")
	}
}

func TestDBSummary1mPurged(t *testing.T) {
	for _, n := range telemetryPurgeTables {
		if n == "db_summary_1m" {
			return
		}
	}
	t.Fatal("db_summary_1m telemetri purge listesinde yok — temizlik sonrası 1m eski kovaları taşır")
}
