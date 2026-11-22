package chstore

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

// messaging_summary_1m_test.go — v0.10.2450 (operatör 2026-11-22): the
// 1-minute twin of messaging_summary_5m. Pins (db_summary_1m_test.go twin):
//
//  1. catalogue — present, LAST in the slice (positional pins);
//  2. derivable from the sibling: only name / bucket / TTL differ;
//  3. cluster mode — `_local` + ON CLUSTER + Replicated + FROM spans_local +
//     Distributed wrapper with the sibling's shard key; three day-one
//     registries; purge list;
//  4. sampling-weight phase 2 — registry entry, weighted CREATE carries the
//     four w_* states;
//  5. reader resolution (msgTrendGrainFor) + settle-shifted current bucket.

// msg1mTwins — 1m twin → its 5m sibling.
var msg1mTwins = map[string]string{
	"messaging_summary_1m":        "messaging_summary_5m",
	"messaging_caller_summary_1m": "messaging_caller_summary_5m",
}

func TestMsgSummary1mCatalogueLast(t *testing.T) {
	names := canonicalMVNames()
	n := len(names)
	if names[n-2] != "messaging_summary_1m" || names[n-1] != "messaging_caller_summary_1m" {
		t.Fatalf("iki messaging 1m ikizi dilimin sonunda olmalı: %v", names[n-3:])
	}
	for fine, coarse := range msg1mTwins {
		if strings.Contains(canonicalMVDDL(coarse), "INTERVAL 1 MINUTE") ||
			strings.Contains(canonicalMVDDL(fine), "INTERVAL 5 MINUTE") {
			t.Fatalf("%s / %s ad çözümü çakıştı", coarse, fine)
		}
	}
}

func TestMsgSummary1mDerivesFromSibling(t *testing.T) {
	for fine, coarse := range msg1mTwins {
		checkMsgTwinDerives(t, fine, coarse)
	}
}

func checkMsgTwinDerives(t *testing.T, fine, coarse string) {
	t.Helper()
	sib := sqlNormalize(canonicalMVDDL(coarse))
	got := sqlNormalize(canonicalMVDDL(fine))
	derived := strings.NewReplacer(
		coarse, fine,
		"INTERVAL 5 MINUTE", "INTERVAL 1 MINUTE",
		"INTERVAL 90 DAY", "INTERVAL 7 DAY",
	).Replace(sib)
	if derived != got {
		t.Fatalf("1m kardeşinden yalnız ad/kova/TTL ile ayrışmalı:\n5m→1m: %s\n   1m: %s", derived, got)
	}
	states := regexp.MustCompile(`AS ([a-z_]+_state)`)
	if n := len(states.FindAllString(got, -1)); n != 4 {
		t.Fatalf("4 state kolonu beklenir, %d", n)
	}
	if !strings.Contains(got, "TTL toDate(time_bucket) + INTERVAL 7 DAY") {
		t.Fatal("TTL db_summary_1m ile aynı (7 gün) olmalı")
	}
	if strings.Contains(got, "POPULATE") {
		t.Fatal("geriye doldurma yok — POPULATE yasak")
	}
}

func TestMsgSummary1mClusterDDL(t *testing.T) {
	for fine, coarse := range msg1mTwins {
		if !highVolumeTables[fine] || !tablesWithoutTraceID[fine] ||
			defaultShardPolicy[fine] != defaultShardPolicy[coarse] {
			t.Fatalf("%s: üç kayıt kardeşle aynı olmalı", fine)
		}
		purged := false
		for _, n := range telemetryPurgeTables {
			purged = purged || n == fine
		}
		if !purged {
			t.Fatalf("%s telemetri purge listesinde yok", fine)
		}
		local := sqlNormalize((&Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}).adaptDDL(canonicalMVDDL(fine))[0])
		if !strings.Contains(local, "ReplicatedAggregatingMergeTree('/p/{shard}/"+fine+"', '{replica}')") {
			t.Errorf("%s: Replicated motor yok:\n%s", fine, local)
		}
	}
	s := &Store{cfg: config.CHConfig{ClusterName: "c", ReplicaPath: "/p"}}
	got := s.adaptDDL(canonicalMVDDL("messaging_summary_1m"))
	if len(got) != 2 {
		t.Fatalf("küme kipi: _local MV + Distributed sarmalayıcı beklenir, %d ifade", len(got))
	}
	local := sqlNormalize(got[0])
	for _, want := range []string{
		"CREATE MATERIALIZED VIEW IF NOT EXISTS messaging_summary_1m_local ON CLUSTER `c` ",
		"ENGINE = ReplicatedAggregatingMergeTree('/p/{shard}/messaging_summary_1m', '{replica}')",
		"FROM spans_local WHERE msg_system != ''",
	} {
		if !strings.Contains(local, want) {
			t.Errorf("_local DDL %q taşımıyor:\n%s", want, local)
		}
	}
	const wrap = "CREATE TABLE IF NOT EXISTS messaging_summary_1m ON CLUSTER `c` AS messaging_summary_1m_local " +
		"ENGINE = Distributed(`c`, currentDatabase(), messaging_summary_1m_local, cityHash64(destination))"
	if got[1] != wrap {
		t.Errorf("Distributed sarmalayıcı:\n got: %s\nwant: %s", got[1], wrap)
	}
}

func TestMsgSummary1mWeighted(t *testing.T) {
	for fine := range msg1mTwins {
		cols, ok := weightedMVs[fine]
		if !ok || len(cols) != 4 {
			t.Fatalf("%s: weightedMVs kaydı yok — weightedMVsWhenActive göçürmez", fine)
		}
		if weightedMVBucket(fine) != time.Minute {
			t.Fatalf("%s: kapsama hizası 1 dk olmalı", fine)
		}
		w := weightMVDDL(canonicalMVDDL(fine))
		for _, c := range []string{"w_span_count_state", "w_error_count_state", "w_duration_sum_state", "w_duration_q_state"} {
			if !strings.Contains(w, " AS "+c) {
				t.Errorf("%s: ağırlıklı CREATE %s taşımıyor", fine, c)
			}
		}
		if strings.Contains(canonicalMVDDL(fine), "sample_weight") {
			t.Fatalf("%s: kanonik (ağırlıksız) DDL sample_weight okumamalı — kolonsuz kurulumda ingest düşer", fine)
		}
		if weightMVDDL(mvDDLByName(weightedMVCatalogue(), fine)) != w {
			t.Fatalf("weightedMVCatalogue %s'i bulmalı", fine)
		}
	}
}

func TestMsgTrendGrainFor(t *testing.T) {
	to := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		window time.Duration
		covers bool
		want   string
		sec    int
	}{
		{"1 sa kapsıyor → 1m", time.Hour, true, "messaging_summary_1m", 60},
		{"3 sa sınır dahil → 1m", 3 * time.Hour, true, "messaging_summary_1m", 60},
		{"3 sa + 1 dk → 5m", 3*time.Hour + time.Minute, true, "messaging_summary_5m", 300},
		{"1 sa kapsamıyor → 5m (boşluk yok)", time.Hour, false, "messaging_summary_5m", 300},
		{"ters pencere → 5m", -time.Hour, true, "messaging_summary_5m", 300},
	}
	for _, c := range cases {
		g := msgTrendGrainFor(to.Add(-c.window), to, c.covers)
		if g.Table != c.want || g.BucketSec != c.sec {
			t.Errorf("%s: %+v", c.name, g)
		}
	}
	if !strings.Contains(msgTrendsSQL("messaging_summary_1m"), "FROM messaging_summary_1m") ||
		!strings.Contains(msgTrendsSQL("x; DROP"), "FROM messaging_summary_5m") {
		t.Fatal("msgTrendsSQL yalnız sabit tablolardan seçmeli")
	}
	for _, want := range []string{"time_bucket < ?", "LIMIT", "max_execution_time"} {
		if !strings.Contains(msgTrendsSQL("messaging_summary_1m"), want) {
			t.Errorf("msgTrendsSQL %q taşımıyor", want)
		}
	}
}

func TestMsgTrendCurrentSettles(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 1, 10, 0, time.UTC)
	fine := DBTrendGrain{Table: msgTrendTableFine, BucketSec: 60}
	// 12:00 bucket closed 10 s ago — not settled; the badge reads 11:59.
	pts := []DBTrendPoint{
		{T: now.Add(-130 * time.Second).Truncate(time.Minute).UnixNano(), Rps: 5},
		{T: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC).UnixNano(), Rps: 1},
	}
	out := []DBTrend{{Points: pts}}
	applyDBTrendCurrent(out, 60, msgTrendCurrentNow(fine, now).Unix())
	if out[0].CurRps != 5 || out[0].CurFromPartial {
		t.Fatalf("1m rozet yerleşmemiş son dakikayı okumamalı: %+v", out[0])
	}
	coarse := DBTrendGrain{Table: msgTrendTableCoarse, BucketSec: 300}
	if !msgTrendCurrentNow(coarse, now).Equal(now) {
		t.Fatal("5m davranışı değişmemeli")
	}
}

// Service -> Messaging span fallback (service_messaging_spans.go).
func TestSvcMsgSpansGrainAndSQL(t *testing.T) {
	to := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	if g := svcMsgGrainFor(to.Add(-time.Hour), to, true); g.Table != "messaging_caller_summary_1m" || g.BucketSec != 60 {
		t.Fatalf("1 sa kapsıyor → caller 1m: %+v", g)
	}
	if g := svcMsgGrainFor(to.Add(-time.Hour), to, false); g.Table != "messaging_caller_summary_5m" || g.BucketSec != 300 {
		t.Fatalf("kapsamıyor → caller 5m (boşluk yok): %+v", g)
	}
	if g := svcMsgGrainFor(to.Add(-6*time.Hour), to, true); g.Table != "messaging_caller_summary_5m" {
		t.Fatalf("6 sa → 5m: %+v", g)
	}
	for _, sql := range []string{svcMsgTopicsSQL("messaging_caller_summary_1m"), svcMsgSeriesSQL("messaging_caller_summary_1m")} {
		for _, want := range []string{"FROM messaging_caller_summary_1m", "service_name = ?", "time_bucket >= ? AND time_bucket < ?", "LIMIT", "max_execution_time"} {
			if !strings.Contains(sql, want) {
				t.Errorf("%q yok:\n%s", want, sql)
			}
		}
	}
	if !strings.Contains(svcMsgTopicsSQL("evil"), "FROM messaging_caller_summary_5m") {
		t.Fatal("bilinmeyen tablo → 5m")
	}
}

func TestSvcMsgSeriesTopicsOf(t *testing.T) {
	topics := []ServiceMsgTopic{
		{Destination: "payments-topic", Kind: "producer"},
		{Destination: "payments-topic", Kind: "consumer"},
		{Destination: "orders-topic", Kind: "producer"},
		{Destination: "audit-topic", Kind: "consumer"},
	}
	got := svcMsgSeriesTopicsOf(topics, 2)
	if len(got) != 2 || got[0] != "payments-topic" || got[1] != "orders-topic" {
		t.Fatalf("hacim sırasıyla tekil ilk N: %v", got)
	}
	p95 := 12.5
	pt := svcMsgPointOf(1, 120, 6, &p95, 60)
	if pt.Rate != 2 || pt.ErrPct != 5 || pt.P95Ms != 12.5 {
		t.Fatalf("nokta: %+v", pt)
	}
}
