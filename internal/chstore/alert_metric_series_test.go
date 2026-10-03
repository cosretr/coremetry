package chstore

// alert_metric_series_test.go — v0.10.1064 (operatör, prod alarm problemi
// detayı: "grafik olmadığı için de anlamak çok zor artışları").
//
// ÇİVİLER:
//  1. ızgara: pencere → kova + çerçeve (MV ızgarası 5 dk, 5 dk altı 1 dk ham);
//  2. yönlendirme PARİTESİ: dizinin kaynağı değerlendiricinin toplu okumasıyla
//     (measureAllServicesPlan) aynı tablo — grafik ateşlenenden başka bir şey
//     çizmesin;
//  3. SQL şekli: tek servis, zaman sınırlı, LIMIT + max_execution_time, bağ sayısı;
//  4. nokta kurucu: boşluk null, son kova şimdiyle kırpık, sayım ölçeklemesi;
//  5. canlı motor (clickhouse local varsa): dizinin SON noktası, değerlendiricinin
//     o anki ölçümüne eşit.

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAlertMetricSeriesSpec(t *testing.T) {
	cases := []struct {
		metric      string
		window      int
		step, frame int
		raw         bool
		maxSpan     time.Duration
		wantErr     bool
	}{
		{"p99_ms", 300, 300, 300, false, 24 * time.Hour, false},
		{"p99_ms", 600, 300, 600, false, 24 * time.Hour, false},
		{"error_rate", 420, 300, 600, false, 24 * time.Hour, false}, // ⌈7/5⌉ kova
		{"request_rate", 3600, 300, 3600, false, 24 * time.Hour, false},
		{"mq_consume_p99_ms", 600, 300, 600, false, 24 * time.Hour, false},
		{"http_p99_ms", 600, 300, 600, true, 6 * time.Hour, false},
		{"db_error_rate", 300, 300, 300, true, 6 * time.Hour, false},
		// 5 dk altı → değerlendiricinin servis başına ham yolu, 1 dk kova.
		{"p99_ms", 60, 60, 0, true, 6 * time.Hour, false},
		{"error_rate", 90, 60, 60, true, 6 * time.Hour, false},
		{"mq_consume_p99_ms", 120, 60, 60, true, 6 * time.Hour, false},
		{"p99_ms", 0, 0, 0, false, 0, true},
		{"log_query", 300, 0, 0, false, 0, true},
		{"watcher", 300, 0, 0, false, 0, true},
		{"db_stmt_p95_ms", 300, 0, 0, false, 0, true},
		{"kafka_lag_max", 300, 0, 0, false, 0, true},
		{"http_route_p99_ms", 300, 0, 0, false, 0, true},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%d", c.metric, c.window), func(t *testing.T) {
			sp, err := AlertMetricSeriesSpec(c.metric, c.window)
			if c.wantErr {
				if err == nil {
					t.Fatalf("hata bekleniyordu, spec=%+v", sp)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sp.StepSec != c.step || sp.FrameSec != c.frame || sp.RawSpans != c.raw || sp.MaxSpan != c.maxSpan {
				t.Fatalf("spec=%+v, want step=%d frame=%d raw=%v max=%s", sp, c.step, c.frame, c.raw, c.maxSpan)
			}
		})
	}
}

// Değerlendiricinin toplu okumasının kabul ettiği HER metrik dizide de aynı
// tablodan okunur (MV ızgarası). 5 dk altı pencerede her şey ham spans.
func TestAlertMetricSeriesPlan_SourceParity(t *testing.T) {
	metrics := []string{
		"error_rate", "error_count", "request_rate", "avg_ms", "p50_ms", "p95_ms", "p99_ms",
		"mq_consume_p99_ms", "mq_consume_p95_ms", "mq_publish_p50_ms", "mq_publish_error_rate",
		"mq_consume_avg_ms", "mq_consume_count",
		"http_p99_ms", "http_p95_ms", "http_avg_ms", "http_5xx_rate", "http_4xx_rate", "http_error_rate",
		"db_p99_ms", "db_avg_ms", "db_count", "db_error_rate", "rpc_error_rate", "rpc_p99_ms",
	}
	for _, m := range metrics {
		t.Run(m, func(t *testing.T) {
			ev, err := measureAllServicesPlan(m, "spanmetrics_1m")
			if err != nil {
				t.Fatalf("değerlendirici planı: %v", err)
			}
			p, err := alertMetricSeriesPlan(m, false, "spanmetrics_1m")
			if err != nil {
				t.Fatal(err)
			}
			if p.table != ev.source {
				t.Fatalf("dizi %s'ten, değerlendirici %s'ten okuyor", p.table, ev.source)
			}
			raw, err := alertMetricSeriesPlan(m, true, "spanmetrics_1m")
			if err != nil {
				t.Fatal(err)
			}
			if raw.table != "spans" {
				t.Fatalf("5 dk altı pencere ham spans okumalı, %s", raw.table)
			}
		})
	}
}

func TestAlertMetricSeriesSQL_Shape(t *testing.T) {
	for _, c := range []struct {
		metric string
		window int
		want   []string
	}{
		{"p99_ms", 600, []string{"FROM service_summary_5m", "time_bucket >= toDateTime(?)", "quantilesTDigestMerge(0.5,0.95,0.99)(q) OVER w, 3)", "RANGE BETWEEN 600 PRECEDING", "STEP 300"}},
		{"error_rate", 300, []string{"FROM spanmetrics_1m", "kind IN ('server','consumer')", "toStartOfInterval(time_bucket, INTERVAL 300 SECOND)"}},
		{"http_p99_ms", 600, []string{"FROM spans", "time >= toDateTime(?)", "kind='server' AND http_method != ''", "quantileMerge(0.99)(q) OVER w"}},
		{"http_5xx_rate", 600, []string{"FROM spans", "countIf(http_status >= 500) AS e"}},
		{"p95_ms", 60, []string{"FROM spans", "quantileMerge(0.95)(q)", "RANGE BETWEEN 0 PRECEDING", "STEP 60", "INTERVAL 60 SECOND"}},
	} {
		t.Run(c.metric, func(t *testing.T) {
			sp, err := AlertMetricSeriesSpec(c.metric, c.window)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := alertMetricSeriesPlan(c.metric, !UseSummaryMV(time.Duration(c.window)*time.Second), "spanmetrics_1m")
			sql := alertMetricSeriesSQL(p, sp)
			for _, w := range append([]string{"service_name = ?", "LIMIT 2000", "max_execution_time = 10", "WITH FILL", "WHERE b >= toDateTime(?)"}, c.want...) {
				if !strings.Contains(sql, w) {
					t.Errorf("SQL %q içermiyor\n%s", w, sql)
				}
			}
			args := alertMetricSeriesArgs(AlertMetricSeriesQuery{Service: "svc-a", From: time.Unix(6000, 0), To: time.Unix(9000, 0)}, sp)
			if strings.Count(sql, "?") != len(args) {
				t.Fatalf("%d bağ yeri, %d argüman", strings.Count(sql, "?"), len(args))
			}
			if args[1] != int64(6000-sp.FrameSec) {
				t.Fatalf("okuma başı çerçeve kadar önce olmalı: %v", args[1])
			}
		})
	}
}

func TestBuildAlertSeriesPoints(t *testing.T) {
	from := time.Unix(3000, 0) // 300'e hizalı
	to := from.Add(20 * time.Minute)
	sp := AlertSeriesSpec{StepSec: 300, FrameSec: 600}
	f := func(v float64) *float64 { return &v }
	eq := func(a, b *float64) bool {
		if a == nil || b == nil {
			return a == b
		}
		return math.Abs(*a-*b) < 1e-9
	}
	cases := []struct {
		name string
		scan alertSeriesScan
		now  time.Time
		vals map[int64]float64
		want []AlertMetricPoint
	}{
		{"yüzdelik: NaN ve eksik satır boşluk; kova sonu zaman", seriesScanFloat, to.Add(time.Hour),
			map[int64]float64{3000: 120, 3300: math.NaN(), 3900: 4000},
			[]AlertMetricPoint{{T: 3300e9, V: f(120)}, {T: 3600e9}, {T: 3900e9}, {T: 4200e9, V: f(4000)}}},
		{"açık problem: son kova şimdiyle kırpık, gelecek kova yok", seriesScanFloat, from.Add(7 * time.Minute),
			map[int64]float64{3000: 1, 3300: 2, 3600: 3},
			[]AlertMetricPoint{{T: 3300e9, V: f(1)}, {T: 3420e9, V: f(2)}}},
		{"error_count: kapsanan süreye ölçek (W=600, kapsanan 900)", seriesScanCountScaled, to.Add(time.Hour),
			map[int64]float64{3000: 90},
			[]AlertMetricPoint{{T: 3300e9, V: f(60)}, {T: 3600e9, V: f(0)}, {T: 3900e9, V: f(0)}, {T: 4200e9, V: f(0)}}},
		{"request_rate: kapsanan süreye böl; kırpık kovada daha kısa süre", seriesScanCountRate, from.Add(7 * time.Minute),
			map[int64]float64{3000: 900, 3300: 720}, // 3300 kovası: [2700, 3420) = 720 sn
			[]AlertMetricPoint{{T: 3300e9, V: f(1)}, {T: 3420e9, V: f(1)}}},
		{"taşıma count: ölçeklenmez", seriesScanCountRaw, to.Add(time.Hour),
			map[int64]float64{3600: 7},
			[]AlertMetricPoint{{T: 3300e9, V: f(0)}, {T: 3600e9, V: f(0)}, {T: 3900e9, V: f(7)}, {T: 4200e9, V: f(0)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := AlertMetricSeriesQuery{WindowSec: 600, From: from, To: to, Now: c.now}
			got := buildAlertSeriesPoints(c.vals, q, sp, c.scan)
			if len(got) != len(c.want) {
				t.Fatalf("got %d nokta %+v, want %d", len(got), got, len(c.want))
			}
			for i := range got {
				if got[i].T != c.want[i].T || !eq(got[i].V, c.want[i].V) {
					gv, wv := "nil", "nil"
					if got[i].V != nil {
						gv = fmt.Sprint(*got[i].V)
					}
					if c.want[i].V != nil {
						wv = fmt.Sprint(*c.want[i].V)
					}
					t.Fatalf("nokta %d: got T=%d V=%s, want T=%d V=%s", i, got[i].T, gv, c.want[i].T, wv)
				}
			}
		})
	}
	if got := buildAlertSeriesPoints(nil, AlertMetricSeriesQuery{From: to, To: from, Now: to}, sp, seriesScanFloat); len(got) != 0 || got == nil {
		t.Fatalf("ters pencere → boş dilim (null değil): %#v", got)
	}
}

// alertSeriesFixture — spans + iki MV'nin (gerçek DDL'deki durum ifadeleriyle)
// clickhouse local kopyası. svc-a: 5 sn'de bir span, son 30 dk'da gecikme
// tırmanıyor; svc-b gürültü (servis süzgecini sınar).
func alertSeriesFixture(startUnix int64, n int) string {
	return fmt.Sprintf(`
CREATE TABLE spans (service_name String, name String, time DateTime64(9), duration UInt64,
  kind String, http_method String, http_status UInt16, http_route String, status_code String,
  db_system String, rpc_system String) ENGINE = MergeTree ORDER BY (service_name, time);
INSERT INTO spans SELECT
  if(number %% 9 = 0, 'svc-b', 'svc-a'), 'op', fromUnixTimestamp64Nano(toInt64((%d + number * 5) * 1000000000)),
  toUInt64((50 + (number * 37) %% 400 + if(number > %d, (number - %d) * 9, 0)) * 1000000),
  ['server','client','consumer','server'][number %% 4 + 1], if(number %% 3 = 0, '', 'GET'),
  if(number %% 11 = 0, 500, 200), '/r', if(number %% 7 = 0, 'error', 'ok'), '', ''
FROM numbers(%d);
CREATE TABLE service_summary_5m ENGINE = AggregatingMergeTree ORDER BY (service_name, time_bucket) AS SELECT
  service_name, toStartOfInterval(time, INTERVAL 5 MINUTE) AS time_bucket,
  countState() AS span_count_state, countIfState(status_code = 'error') AS error_count_state,
  sumState(duration) AS duration_sum_state, quantilesTDigestState(0.5, 0.95, 0.99)(duration) AS duration_q_state
FROM spans GROUP BY service_name, time_bucket;
CREATE TABLE spanmetrics_1m ENGINE = AggregatingMergeTree ORDER BY (service_name, name, kind, status_code, http_route, time_bucket) AS SELECT
  service_name, name, kind, status_code, http_route, toStartOfInterval(time, INTERVAL 1 MINUTE) AS time_bucket,
  countState() AS calls_state, countIfState(status_code = 'error') AS error_state,
  sumState(duration) AS duration_sum_state, quantilesTDigestState(0.5, 0.9, 0.95, 0.99)(duration) AS duration_q_state
FROM spans GROUP BY service_name, name, kind, status_code, http_route, time_bucket;
`, startUnix, n-360, n-360, n)
}

// TestAlertMetricSeries_LastPointEqualsEvaluator — canlı motor: dizinin SON
// noktası (şimdi), değerlendiricinin aynı anda yaptığı ölçümle (toplu MV
// planı ya da ham yol) aynı sayı. clickhouse ikilisi yoksa atlanır.
func TestAlertMetricSeries_LastPointEqualsEvaluator(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — canlı parite testi atlandı (şekil testleri yine koşar)")
	}
	const start = int64(1_790_000_100) // 300'e hizalı; 5 sn aralıkla ~2 saat veri (şimdiye kadar)
	for _, c := range []struct {
		metric string
		window int
		nowOff int64 // start'tan saniye; veri yalnız < now
		tol    float64
	}{
		{"p99_ms", 600, 7050, 0.02}, // TDigest durum-üstü birleştirme ±%2
		{"p95_ms", 300, 7050, 0.02},
		{"avg_ms", 600, 7050, 1e-9},
		{"error_rate", 600, 7050, 1e-9},
		{"error_count", 600, 7050, 1e-9},
		{"request_rate", 600, 7050, 1e-9},
		{"http_p99_ms", 600, 7050, 1e-9},
		{"http_5xx_rate", 600, 7050, 1e-9},
		{"mq_consume_p99_ms", 600, 7050, 0.02},
		{"mq_consume_count", 600, 7050, 1e-9},
		// 5 dk altı ham yol: şimdi dakika sınırında → son kova tam pencere.
		{"p99_ms", 120, 7080, 1e-9},
		{"error_rate", 60, 7080, 1e-9},
	} {
		t.Run(fmt.Sprintf("%s/%d", c.metric, c.window), func(t *testing.T) {
			now := time.Unix(start+c.nowOff, 0)
			sp, err := AlertMetricSeriesSpec(c.metric, c.window)
			if err != nil {
				t.Fatal(err)
			}
			raw := !UseSummaryMV(time.Duration(c.window) * time.Second)
			p, _ := alertMetricSeriesPlan(c.metric, raw, "spanmetrics_1m")
			step := time.Duration(sp.StepSec) * time.Second
			to := now.Truncate(step)
			if to.Before(now) {
				to = to.Add(step)
			}
			q := AlertMetricSeriesQuery{Metric: c.metric, Service: "svc-a", WindowSec: c.window,
				From: to.Add(-time.Hour), To: to, Now: now}
			seriesSQL := inlineArgs(t, alertMetricSeriesSQL(p, sp), alertMetricSeriesArgs(q, sp))
			// Veri yalnız şimdiye kadar: fikstür satır sayısı now'a göre kesilir.
			rows := int((now.Unix() - start) / 5)
			evalSQL := evaluatorMeasureSQL(t, c.metric, c.window, now)
			script := alertSeriesFixture(start, rows) + seriesSQL + " FORMAT TSV;\nSELECT 'SPLIT';\n" + evalSQL + ";"
			cmd := exec.Command(bin, "local", "--multiquery", "--query", script)
			cmd.Env = append(os.Environ(), "TZ=UTC")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("clickhouse local: %v\n%s", err, out)
			}
			parts := strings.Split(strings.TrimRight(string(out), "\n"), "SPLIT\n")
			if len(parts) != 2 {
				t.Fatalf("çıktı bölünemedi:\n%s", out)
			}
			vals := map[int64]float64{}
			for _, ln := range strings.Split(strings.TrimRight(parts[0], "\n"), "\n") {
				f := strings.Split(ln, "\t")
				ts, perr := time.ParseInLocation("2006-01-02 15:04:05", f[0], time.UTC)
				v, verr := strconv.ParseFloat(f[1], 64)
				if perr != nil || verr != nil {
					t.Fatalf("satır %q", ln)
				}
				vals[ts.Unix()] = v
			}
			pts := buildAlertSeriesPoints(vals, q, sp, p.scan)
			if len(pts) == 0 || pts[len(pts)-1].V == nil {
				t.Fatalf("son nokta yok: %+v", pts)
			}
			last := pts[len(pts)-1]
			if last.T != now.UnixNano() {
				t.Fatalf("son nokta zamanı %d, şimdi %d", last.T, now.UnixNano())
			}
			want, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err != nil {
				t.Fatalf("değerlendirici çıktısı %q", parts[1])
			}
			got := *last.V
			if d := math.Abs(got - want); d > c.tol*math.Max(1, math.Abs(want)) {
				t.Fatalf("dizinin son noktası %.6f, değerlendirici %.6f", got, want)
			}
		})
	}
}

// evaluatorMeasureSQL — değerlendiricinin o anki ölçümü: MV ızgarasında
// toplu plan (MeasureAllServices), 5 dk altında servis başına ham yolun eşi.
// Sayım türlerinde Go tarafındaki v0.8.315 matematiği SQL'de uygulanır.
func evaluatorMeasureSQL(t *testing.T, metric string, window int, now time.Time) string {
	t.Helper()
	w := time.Duration(window) * time.Second
	if !UseSummaryMV(w) {
		cut := fmt.Sprintf("time >= fromUnixTimestamp64Nano(toInt64(%d)) AND time < fromUnixTimestamp64Nano(toInt64(%d))",
			now.Add(-w).UnixNano(), now.UnixNano())
		switch metric {
		case "p99_ms":
			return "SELECT quantile(0.99)(duration) / 1e6 FROM spans WHERE service_name = 'svc-a' AND " + cut
		case "error_rate":
			return "SELECT countIf(status_code='error') / nullIf(count(),0) * 100 FROM spans WHERE service_name = 'svc-a' AND " +
				cut + " AND " + EntrySpanKindsWhere
		}
		t.Fatalf("ham parite yok: %s", metric)
	}
	plan, err := measureAllServicesPlan(metric, "spanmetrics_1m")
	if err != nil {
		t.Fatal(err)
	}
	startArg := fmt.Sprintf("toDateTime(%d)", MVWindowStart(now, w).Unix())
	inner := strings.Replace(plan.sql, "?", startArg, 1)
	expr := "v"
	switch plan.scan {
	case scanCountScaled:
		expr = fmt.Sprintf("v * %d / %f", window, MVCoveredSeconds(now, w))
	case scanCountRate:
		expr = fmt.Sprintf("v / %f", MVCoveredSeconds(now, w))
	}
	// Plan iki adsız kolon döndürür (service_name, değer): değere ad ver
	// (ilk FROM ana kaynak — planlarda alt sorgu yok), SETTINGS'i dışarı al.
	i := strings.Index(inner, "FROM ")
	inner = inner[:i] + " AS v " + inner[i:]
	inner = strings.Replace(inner, "SETTINGS max_execution_time = 10", "", 1)
	return fmt.Sprintf("SELECT toFloat64(%s) FROM (%s) WHERE service_name = 'svc-a'", expr, inner)
}
