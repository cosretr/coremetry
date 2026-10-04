package chstore

// oracle_sample_traces_test.go — v0.10.1104 (operatör: "bazı traceidler de
// aslında coremetry üzerinde olmayabilir"). Pinler: Oracle örneklerinin trace
// id'leri tek sınırlı TraceFactsByIDs aramasıyla işaretlenir (bulundu → true,
// yok → false, hata → nil/bilinmiyor); pencere satır aralığı ±5 dk, ≤100 id,
// ≤6 sn; yalnız `ora:` dalı arar (span grubu örneği span'den doğar).

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	trA = "4bf92f3577b34da6a3ce929d0e0e4736"
	trB = "5af92f3577b34da6a3ce929d0e0e4737"
	trC = "6cf92f3577b34da6a3ce929d0e0e4738"
)

func boolp(b bool) *bool { return &b }

func TestMarkOracleSampleTraces(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	base := func() []ExceptionSample {
		return []ExceptionSample{
			{TraceID: trA, Time: t0.Add(2 * time.Minute).UnixNano(), Message: "ORA-00001 · OP_A"},
			{TraceID: trB, Time: t0.UnixNano()},
			{TraceID: "", Time: t0.Add(-time.Hour).UnixNano()}, // id'siz: pencereye de girmez
			{TraceID: trA + " ", Time: t0.Add(time.Minute).UnixNano()},
			{TraceID: trC, Time: t0.Add(10 * time.Minute).UnixNano()},
		}
	}
	cases := []struct {
		name  string
		found map[string]TraceFact
		err   error
		want  []*bool
	}{
		{"karışık: biri yok", map[string]TraceFact{trA: {Service: "svc-orders"}, trC: {Service: "svc-orders"}}, nil,
			[]*bool{boolp(true), boolp(false), nil, boolp(true), boolp(true)}},
		{"hiçbiri yok", map[string]TraceFact{}, nil,
			[]*bool{boolp(false), boolp(false), nil, boolp(false), boolp(false)}},
		{"nil harita = boş sonuç", nil, nil,
			[]*bool{boolp(false), boolp(false), nil, boolp(false), boolp(false)}},
		{"sorgu hatası → bilinmiyor", nil, errors.New("timeout"),
			[]*bool{nil, nil, nil, nil, nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			samples := base()
			var gotIDs []string
			var gotFrom, gotTo time.Time
			var budget time.Duration
			calls := 0
			markOracleSampleTraces(context.Background(), samples, func(ctx context.Context, ids []string, from, to time.Time) (map[string]TraceFact, error) {
				calls++
				gotIDs, gotFrom, gotTo = ids, from, to
				if dl, ok := ctx.Deadline(); ok {
					budget = time.Until(dl)
				}
				return tc.found, tc.err
			})
			if calls != 1 {
				t.Fatalf("tek sorgu bekleniyordu: %d", calls)
			}
			if strings.Join(gotIDs, ",") != strings.Join([]string{trA, trB, trC}, ",") {
				t.Fatalf("ayrık, kırpılmış id'ler: %v", gotIDs)
			}
			if !gotFrom.Equal(t0.Add(-5*time.Minute)) || !gotTo.Equal(t0.Add(15*time.Minute)) {
				t.Fatalf("pencere satır aralığı ±5 dk olmalı: %v → %v", gotFrom, gotTo)
			}
			if budget <= 0 || budget > 6*time.Second {
				t.Fatalf("zaman aşımı ≤6 sn olmalı: %v", budget)
			}
			for i, sm := range samples {
				w := tc.want[i]
				switch {
				case w == nil && sm.TraceInCoremetry != nil:
					t.Fatalf("#%d bilinmiyor kalmalıydı: %v", i, *sm.TraceInCoremetry)
				case w != nil && (sm.TraceInCoremetry == nil || *sm.TraceInCoremetry != *w):
					t.Fatalf("#%d: want %v got %v", i, *w, sm.TraceInCoremetry)
				}
			}
		})
	}
}

// Aranacak id yoksa sorgu YOK; tavan 100 id; tavan dışı id "yok" sayılmaz.
func TestMarkOracleSampleTracesBounds(t *testing.T) {
	calls := 0
	lookup := func(_ context.Context, ids []string, _, _ time.Time) (map[string]TraceFact, error) {
		calls++
		if len(ids) > oracleSampleTraceMaxIDs {
			t.Fatalf("id tavanı aşıldı: %d", len(ids))
		}
		return map[string]TraceFact{}, nil
	}
	markOracleSampleTraces(context.Background(), []ExceptionSample{{TraceID: " ", Time: 1}, {Time: 2}}, lookup)
	if calls != 0 {
		t.Fatalf("id yokken sorgu atılmamalı")
	}
	var many []ExceptionSample
	for i := 0; i < 130; i++ {
		many = append(many, ExceptionSample{TraceID: strings.Repeat("a", 30) + string(rune('A'+i/26)) + string(rune('a'+i%26)), Time: int64(i + 1)})
	}
	markOracleSampleTraces(context.Background(), many, lookup)
	if calls != 1 {
		t.Fatalf("tek sorgu: %d", calls)
	}
	if many[0].TraceInCoremetry == nil || *many[0].TraceInCoremetry {
		t.Fatalf("sorulan ve bulunmayan → false")
	}
	if many[129].TraceInCoremetry != nil {
		t.Fatalf("tavan dışı id bilinmiyor kalmalı")
	}
}

// Kablolama: yalnız Oracle dalı TraceFactsByIDs ile işaretler; span dalı aramaz.
func TestOracleSamplesMarkWiring(t *testing.T) {
	src, err := os.ReadFile("oracle_exception_groups.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func (s *Store) oracleGroupSamples(")
	if i < 0 {
		t.Fatal("oracleGroupSamples bulunamadı")
	}
	fn := body[i:]
	fn = fn[:strings.Index(fn, "\n}\n")]
	if !regexp.MustCompile(`markOracleSampleTraces\(ctx, res\.Samples, s\.TraceFactsByIDs\)`).MatchString(fn) {
		t.Fatalf("oracleGroupSamples örnekleri TraceFactsByIDs ile işaretlemeli")
	}
	inbox, err := os.ReadFile("exception_inbox.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inbox), "markOracleSampleTraces(") {
		t.Fatalf("span grubu örnekleri işaretlenmez (trace span'den doğar)")
	}
	if oracleSampleTraceSlack != 5*time.Minute || oracleSampleTraceTimeout > 6*time.Second || oracleSampleTraceMaxIDs > 100 {
		t.Fatalf("sınırlar: pay %v, bütçe %v, tavan %d", oracleSampleTraceSlack, oracleSampleTraceTimeout, oracleSampleTraceMaxIDs)
	}
	// TraceFactsByIDs'in kendisi sınırlı: zaman WHERE + LIMIT + max_execution_time.
	q := traceFactsSQL(3, exFragments(false))
	for _, w := range []string{"time >= ? AND time <= ?", "LIMIT", "max_execution_time"} {
		if !strings.Contains(q, w) {
			t.Fatalf("traceFactsSQL %q taşımalı", w)
		}
	}
}
