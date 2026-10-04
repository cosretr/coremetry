package chstore

import (
	"context"
	"strings"
	"testing"
	"time"
)

// db_detail_trend_test.go — v0.10.1095 — /database detay grafiklerinin
// kaynak seçimi (db_summary_1m ≤ 3 sa, aksi 5m), kapsama probu düşüşü,
// SQL golden (üç sınır) ve kova genişliğine göre hız.

func TestDBTrendGrainForTable(t *testing.T) {
	to := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	fine := DBTrendGrain{Table: "db_summary_1m", BucketSec: 60}
	coarse := DBTrendGrain{Table: "db_summary_5m", BucketSec: 300}
	cases := []struct {
		name   string
		window time.Duration
		covers bool
		want   DBTrendGrain
	}{
		{"15 dk, kapsıyor", 15 * time.Minute, true, fine},
		{"1 sa, kapsıyor", time.Hour, true, fine},
		{"tam 3 sa (dahil), kapsıyor", 3 * time.Hour, true, fine},
		{"3 sa + 1 sn", 3*time.Hour + time.Second, true, coarse},
		{"6 sa", 6 * time.Hour, true, coarse},
		{"24 sa", 24 * time.Hour, true, coarse},
		{"7 gün", 7 * 24 * time.Hour, true, coarse},
		{"1 sa, 1m tablo yok/boş/geç başlıyor", time.Hour, false, coarse},
		{"sıfır pencere", 0, true, coarse},
		{"ters pencere", -time.Hour, true, coarse},
	}
	for _, c := range cases {
		if got := dbTrendGrainFor(to.Add(-c.window), to, c.covers); got != c.want {
			t.Errorf("%s: %+v, beklenen %+v", c.name, got, c.want)
		}
	}
}

// Probe → seçici kablosu. Probe önbelleği elle doldurulur (bağlantısız
// Store): uzun pencerede prob HİÇ koşmamalı (conn nil → koşsa panik).
func TestDBDetailTrendGrainProbeFallback(t *testing.T) {
	ctx := context.Background()
	to := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	seed := func(first time.Time, ok bool) *Store {
		s := &Store{}
		p := &s.priorCoverage[priorSrcDBSummary1m]
		p.at, p.min, p.ok = time.Now(), first, ok
		return s
	}
	if g := (*Store)(nil).DBDetailTrendGrain(ctx, from, to); g.Table != "db_summary_5m" {
		t.Errorf("nil Store → 5m, %+v", g)
	}
	if g := seed(time.Time{}, false).DBDetailTrendGrain(ctx, from, to); g.Table != "db_summary_5m" {
		t.Errorf("prob başarısız (tablo yok / DDL ertelendi) → 5m, %+v", g)
	}
	if g := seed(from.Add(10*time.Minute), true).DBDetailTrendGrain(ctx, from, to); g.Table != "db_summary_5m" {
		t.Errorf("ilk kova pencere başından SONRA (geriye dolmaz) → 5m, %+v", g)
	}
	if g := seed(from, true).DBDetailTrendGrain(ctx, from, to); g.Table != "db_summary_5m" {
		t.Errorf("ilk kova pencere başına EŞİT (yarım kova olabilir) → 5m, %+v", g)
	}
	if g := seed(from.Add(-24*time.Hour), true).DBDetailTrendGrain(ctx, from, to); g.Table != "db_summary_1m" || g.BucketSec != 60 {
		t.Errorf("kapsıyor + ≤3 sa → 1m, %+v", g)
	}
	if g := (&Store{}).DBDetailTrendGrain(ctx, to.Add(-24*time.Hour), to); g.Table != "db_summary_5m" {
		t.Errorf("uzun pencere prob koşmadan 5m, %+v", g)
	}
}

func TestDBDetailTrendSQLGolden(t *testing.T) {
	nameSQL, _ := dbDetailNameFilter("db_name", "APP")
	got := strings.Join(strings.Fields(dbDetailTrendSQL("db_summary_1m", nameSQL)), " ")
	const want = "SELECT toUnixTimestamp64Nano(toDateTime64(time_bucket, 9)) AS bucket_ns, " +
		"countMerge(span_count_state) AS span_count, countIfMerge(error_count_state) AS error_count, " +
		"arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms " +
		"FROM db_summary_1m WHERE db_system = ? AND instance = ? AND db_name = ? " +
		"AND time_bucket >= ? AND time_bucket < ? GROUP BY time_bucket ORDER BY time_bucket " +
		"LIMIT 30000 SETTINGS max_execution_time = 10"
	if got != want {
		t.Fatalf("SQL golden'dan saptı:\n got: %s\nwant: %s", got, want)
	}
	// Boş dbName → yüklem yok (karolarla aynı "tüm veritabanları" kapsamı).
	if q := dbDetailTrendSQL("db_summary_5m", ""); strings.Contains(q, "db_name = ?") || !strings.Contains(q, "FROM db_summary_5m") {
		t.Errorf("boş dbName / 5m SQL'i yanlış: %s", q)
	}
	// Tanınmayan tablo adı SQL'e GİRMEZ — 5m'e düşer.
	if q := dbDetailTrendSQL("spans; DROP TABLE x", ""); !strings.Contains(q, "FROM db_summary_5m") || strings.Contains(q, "DROP") {
		t.Errorf("tablo adı beyaz liste dışı kabul edildi: %s", q)
	}
	// Üst sınır `<` (v0.9.823 sözleşmesi).
	if op := upperBoundOp(t, "dbDetailTrendSQL", dbDetailTrendSQL("db_summary_1m", "")); op != "<" {
		t.Errorf("üst sınır %q — `<` olmalı", op)
	}
}

func TestDBTrendPointOfUsesBucketWidth(t *testing.T) {
	p99 := 12.5
	fine := dbTrendPointOf(1, 600, 30, &p99, 60)
	coarse := dbTrendPointOf(1, 600, 30, &p99, 300)
	if fine.Rps != 10 || coarse.Rps != 2 {
		t.Errorf("hız kova genişliğine bölünmeli: 1m %v (10), 5m %v (2)", fine.Rps, coarse.Rps)
	}
	if fine.ErrorRate != 5 || fine.P99Ms != 12.5 {
		t.Errorf("hata %% / p99: %+v", fine)
	}
	if z := dbTrendPointOf(1, 0, 0, nil, 0); z.Rps != 0 || z.ErrorRate != 0 || z.P99Ms != 0 {
		t.Errorf("boş kova sıfır olmalı: %+v", z)
	}
}

// Kaynak tablo SQL'e yalnız sabitten girer; GetDBDetailTrend tanınmayan
// grenliği 5m'e çevirir (sistem boşken sorgu koşmaz — bağlantısız Store).
func TestGetDBDetailTrendNormalisesGrain(t *testing.T) {
	out, err := (&Store{}).GetDBDetailTrend(context.Background(), DBTrendGrain{Table: "spans", BucketSec: 1}, "", "", "", time.Now().Add(-time.Hour), time.Now())
	if err != nil || out.Source != "db_summary_5m" || out.BucketSec != 300 || out.Points == nil {
		t.Fatalf("tanınmayan grenlik 5m'e düşmeli, boş sistem boş seri: %+v %v", out, err)
	}
}
