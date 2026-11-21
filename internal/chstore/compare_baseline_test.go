package chstore

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Şekil: her sorgu MV okur (ham spans asla), zaman sınırlı, LIMIT + bütçeli.
func TestBaselineSQLShape(t *testing.T) {
	for name, q := range map[string]string{
		"red":      BaselineREDSQL(""),
		"red_op":   BaselineREDSQL("GET /x"),
		"series":   BaselineSeriesSQL("", 600),
		"seriesOp": BaselineSeriesSQL("GET /x", 600),
		"ops":      BaselineOpsSQL(),
		"edges":    BaselineEdgesSQL(),
	} {
		low := strings.ToLower(q)
		if strings.Contains(low, "from spans") || strings.Contains(low, "spanmetrics") {
			t.Errorf("%s: ham/span tablosu okunuyor:\n%s", name, q)
		}
		for _, must := range []string{"limit ", "max_execution_time", "time_bucket >= ?", "time_bucket < ?"} {
			if !strings.Contains(low, must) {
				t.Errorf("%s: %q yok", name, must)
			}
		}
		if !strings.Contains(q, "_5m") {
			t.Errorf("%s: 5 dk ön-agregat okumuyor", name)
		}
	}
	if !strings.Contains(BaselineREDSQL("x"), "operation_summary_5m") || !strings.Contains(BaselineREDSQL(""), "service_summary_5m") {
		t.Error("op kapsamı tablo seçmiyor")
	}
}

func TestBaselineArgsArity(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	q := BaselineQuery{Service: "checkout", Cur: PeriodWindow{From: now.Add(-time.Hour), To: now},
		Ref: PeriodWindow{From: now.Add(-25 * time.Hour), To: now.Add(-24 * time.Hour)}, ViewFrom: now.Add(-2 * time.Hour), Overlay: 24 * time.Hour, StepS: 300}
	for _, op := range []string{"", "GET /x"} {
		q.Operation = op
		red, ser, ops, edg := BaselineArgs(q)
		for name, c := range map[string][2]int{
			"red": {strings.Count(BaselineREDSQL(op), "?"), len(red)}, "series": {strings.Count(BaselineSeriesSQL(op, 300), "?"), len(ser)},
			"ops": {strings.Count(BaselineOpsSQL(), "?"), len(ops)}, "edges": {strings.Count(BaselineEdgesSQL(), "?"), len(edg)},
		} {
			if c[0] != c[1] {
				t.Errorf("op=%q %s: %d yer tutucu, %d argüman", op, name, c[0], c[1])
			}
		}
	}
}

func TestSplitBaselineSeries(t *testing.T) {
	vf := time.UnixMilli(10_000)
	pts := []BaselinePoint{{TimeMs: 1_000}, {TimeMs: 10_000}, {TimeMs: 11_000}}
	cur, ov := SplitBaselineSeries(pts, vf, time.Hour)
	if len(cur) != 2 || len(ov) != 1 || ov[0].TimeMs != 1_000 {
		t.Fatalf("cur=%v ov=%v", cur, ov)
	}
	cur, ov = SplitBaselineSeries(pts, vf, 0)
	if len(cur) != 3 || ov != nil {
		t.Fatalf("overlay yokken hepsi şimdiki olmalı: %v %v", cur, ov)
	}
}

func baselineInline(t *testing.T, q string, args []any) string {
	t.Helper()
	var b strings.Builder
	i := 0
	for _, r := range q {
		if r != '?' {
			b.WriteRune(r)
			continue
		}
		switch v := args[i].(type) {
		case string:
			b.WriteString(chQuote(v))
		case time.Time:
			fmt.Fprintf(&b, "toDateTime('%s', 'UTC')", v.UTC().Format("2006-01-02 15:04:05"))
		default:
			b.WriteString(fmt.Sprint(v))
		}
		i++
	}
	return b.String()
}

// Canlı motor: SQL çalışır ve iki pencere doğru ayrılır. clickhouse yoksa atlanır.
func TestBaselineSQLLive(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok")
	}
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	q := BaselineQuery{Service: "checkout", Cur: PeriodWindow{From: now.Add(-30 * time.Minute), To: now},
		Ref: PeriodWindow{From: now.Add(-60 * time.Minute), To: now.Add(-30 * time.Minute)}, ViewFrom: now.Add(-60 * time.Minute), StepS: 300}
	red, ser, ops, edg := BaselineArgs(q)
	var fx strings.Builder
	fx.WriteString(`CREATE TABLE spans (service_name String, name String, time DateTime('UTC'), duration UInt64, status_code String) ENGINE = Memory;
CREATE MATERIALIZED VIEW service_summary_5m ENGINE = AggregatingMergeTree ORDER BY (service_name, time_bucket) AS SELECT service_name, toStartOfInterval(time, INTERVAL 5 MINUTE) AS time_bucket,
 countState() AS span_count_state, countIfState(status_code = 'error') AS error_count_state, quantilesTDigestState(0.5, 0.95, 0.99)(duration) AS duration_q_state FROM spans GROUP BY service_name, time_bucket;
CREATE MATERIALIZED VIEW operation_summary_5m ENGINE = AggregatingMergeTree ORDER BY (service_name, name, time_bucket) AS SELECT service_name, name, toStartOfInterval(time, INTERVAL 5 MINUTE) AS time_bucket,
 countState() AS span_count_state, countIfState(status_code = 'error') AS error_count_state, quantilesTDigestState(0.5, 0.95, 0.99)(duration) AS duration_q_state FROM spans GROUP BY service_name, name, time_bucket;
CREATE TABLE topology_edges_5m (time_bucket DateTime('UTC'), parent_service String, child_node String, node_kind String, calls UInt64, sum_duration_ns UInt64, errors UInt64) ENGINE = ReplacingMergeTree ORDER BY (time_bucket, parent_service, child_node);
`)
	// Referans: 10 çağrı/kova 20 ms; şimdiki: 10 çağrı/kova 200 ms, 2 hata.
	fmt.Fprintf(&fx, "INSERT INTO spans SELECT 'checkout', if(number %% 2 = 0, 'GET /a', 'GET /b'), toDateTime('%s', 'UTC') + number * 30, if(number >= 60, 200000000, 20000000), if(number >= 60 AND number %% 30 = 0, 'error', 'ok') FROM numbers(120);\n",
		q.Ref.From.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&fx, "INSERT INTO topology_edges_5m VALUES (toDateTime('%s','UTC'),'checkout','db:orders','db',100,1000000000,0),(toDateTime('%s','UTC'),'checkout','db:orders','db',100,5000000000,3);\n",
		q.Ref.From.Format("2006-01-02 15:04:05"), q.Cur.From.Format("2006-01-02 15:04:05"))
	script := fx.String() +
		strings.Replace(baselineInline(t, BaselineREDSQL(""), red), "LIMIT 2", "ORDER BY is_cur LIMIT 2", 1) + " FORMAT TSV;\n" +
		baselineInline(t, BaselineSeriesSQL("", 600), ser) + " FORMAT TSV;\n" +
		baselineInline(t, BaselineOpsSQL(), ops) + " FORMAT TSV;\n" +
		baselineInline(t, BaselineEdgesSQL(), edg) + " FORMAT TSV;\n"
	cmd := exec.Command(bin, "local", "--multiquery", "--query", script)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clickhouse local: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	// RED: is_cur 0 (60 çağrı, 0 hata) ve 1 (60 çağrı, 2 hata).
	if !strings.HasPrefix(lines[0], "0\t60\t0\t") || !strings.HasPrefix(lines[1], "1\t60\t2\t") {
		t.Fatalf("RED satırları beklenmedik:\n%s", out)
	}
	// Seri 600 sn kovalarla 6 satır; ops 2 ad × 2 pencere = 4; kenar 2.
	if len(lines) != 2+6+4+2 {
		t.Fatalf("satır sayısı %d:\n%s", len(lines), out)
	}
}
