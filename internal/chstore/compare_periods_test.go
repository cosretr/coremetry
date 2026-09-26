package chstore

// compare_periods_test.go — v0.10.944. SQL kurucu pinleri (yüzdelik tüm
// pencereden, popülasyon giriş span'i, sınırlar, bind sırası), saf birleştirme
// (pay, karışım kayması, farklar, düşük örnek, kapsama) ve sürüm zincirinin
// SQL↔Go ikizliği. İnceleme turu: MV-önce kapısı (spanmetrics_1m kind taşır;
// ham yalnız env/cluster/namespace ya da kapsama dışı), okuma başına tavan,
// pod/sürüm ayrımı, tek okumalı ortam keşfi.

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var (
	cpT0  = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	cpCur = PeriodWindow{From: cpT0, To: cpT0.Add(time.Hour)}
	cpRef = PeriodWindow{From: cpT0.Add(-time.Hour), To: cpT0}
)

// cpBind — SAF test yardımcısı: ? yer tutucularını sırayla argümanla doldurur
// (clickhouse-go'nun konumsal bağlamasıyla aynı sıra) ki "hangi değer hangi
// yükleme gitti" metin üzerinden doğrulanabilsin.
func cpBind(t *testing.T, sql string, args []any) string {
	t.Helper()
	if n := strings.Count(sql, "?"); n != len(args) {
		t.Fatalf("yer tutucu %d, argüman %d — konumsal bağlama kayar:\n%s", n, len(args), sql)
	}
	var b strings.Builder
	i := 0
	for _, r := range sql {
		if r == '?' {
			switch v := args[i].(type) {
			case time.Time:
				fmt.Fprintf(&b, "T[%s]", v.Format(time.RFC3339))
			case []string:
				fmt.Fprintf(&b, "%q", v)
			default:
				fmt.Fprintf(&b, "'%v'", v)
			}
			i++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func cpFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

// cpRawBuilders — ham kurucular sabit tavanla (imza: pencereler + tavan).
func cpRawBuilders() map[string]func(PeriodScope, PeriodWindow, PeriodWindow) (string, []any) {
	w := func(b func(PeriodScope, PeriodWindow, PeriodWindow, int) (string, []any), capS int) func(PeriodScope, PeriodWindow, PeriodWindow) (string, []any) {
		return func(sc PeriodScope, cur, ref PeriodWindow) (string, []any) { return b(sc, cur, ref, capS) }
	}
	return map[string]func(PeriodScope, PeriodWindow, PeriodWindow) (string, []any){
		"red": w(comparePeriodsREDSQL, periodCapRED), "ops": w(comparePeriodsOpsSQL, periodCapSecondary),
		"deps": w(comparePeriodsDepsSQL, periodCapSecondary), "pods": w(comparePeriodsPodsSQL, periodCapSecondary),
		"versions": w(comparePeriodsVersionsSQL, periodCapVersions),
	}
}

func TestComparePeriodsSQLBindOrderAndScope(t *testing.T) {
	full := PeriodScope{Service: "checkout", Env: "prod", Clusters: []string{"cluster-a"}, Namespace: "shop", Operation: "GET /cart"}
	builders := cpRawBuilders()
	for name, b := range builders {
		for _, sc := range []PeriodScope{{Service: "checkout"}, full} {
			sql, args := b(sc, cpCur, cpRef)
			bound := cpFlat(cpBind(t, sql, args))
			// period ifadesi sorun penceresine bağlı ve metnin BAŞINDA.
			if !strings.HasPrefix(bound, "WITH if(time >= T[2026-09-26T10:00:00Z] AND time < T[2026-09-26T11:00:00Z], 0, 1) AS period") {
				t.Errorf("%s: period ifadesi sorun penceresine bağlanmamış:\n%s", name, bound)
			}
			if !strings.Contains(bound, "service_name = 'checkout'") {
				t.Errorf("%s: servis yüklemi: %s", name, bound)
			}
			// iki pencere de yarı açık ve birincil anahtar budanabilir biçimde
			want := "((time >= T[2026-09-26T10:00:00Z] AND time < T[2026-09-26T11:00:00Z]) OR (time >= T[2026-09-26T09:00:00Z] AND time < T[2026-09-26T10:00:00Z]))"
			if !strings.Contains(bound, want) {
				t.Errorf("%s: zaman yüklemi yok ya da sıra kaymış:\n%s", name, bound)
			}
			// v0.10.944 — okuma başına tavan; tek 20 s tavanı bir yavaş ikincil
			// okumanın bütçenin tamamını yemesine izin veriyordu.
			if !strings.Contains(sql, "max_execution_time = ") || strings.Contains(sql, "max_execution_time = 20") || !strings.Contains(sql, "LIMIT ") {
				t.Errorf("%s: LIMIT + okuma başına max_execution_time zorunlu:\n%s", name, sql)
			}
			if sc.Env != "" && !strings.Contains(bound, "deploy_env = 'prod'") {
				t.Errorf("%s: env uygulanmadı — ortamlar BİRLEŞİR: %s", name, bound)
			}
			if sc.Env == "" && strings.Contains(sql, "deploy_env") {
				t.Errorf("%s: env verilmeden env yüklemi", name)
			}
			if len(sc.Clusters) > 0 && !strings.Contains(bound, `cluster IN (["cluster-a"])`) {
				t.Errorf("%s: cluster yüklemi: %s", name, bound)
			}
			if sc.Namespace != "" && !strings.Contains(bound, "k8s_namespace = 'shop'") {
				t.Errorf("%s: namespace yüklemi: %s", name, bound)
			}
		}
	}
}

func TestComparePeriodsPopulationAndOperationScope(t *testing.T) {
	sc := PeriodScope{Service: "checkout", Operation: "GET /cart"}
	b := cpRawBuilders()
	for name, sql := range map[string]string{
		"red":      first(b["red"](sc, cpCur, cpRef)),
		"ops":      first(b["ops"](sc, cpCur, cpRef)),
		"pods":     first(b["pods"](sc, cpCur, cpRef)),
		"versions": first(b["versions"](sc, cpCur, cpRef)),
		"red_mv":   first(comparePeriodsREDMVSQL("spanmetrics_1m", sc, cpCur, cpRef, periodCapRED)),
		"ops_mv":   first(comparePeriodsOpsMVSQL("spanmetrics_1m", sc, cpCur, cpRef, periodCapSecondary)),
	} {
		if !strings.Contains(sql, "kind IN ('server', 'consumer')") {
			t.Errorf("%s: requests GİRİŞ span'leri olmalı (MV'de kind yok tuzağı)", name)
		}
		if !strings.Contains(sql, " AND name = ?") {
			t.Errorf("%s: operation süzgeci giriş okumasına uygulanmalı", name)
		}
	}
	deps := first(b["deps"](sc, cpCur, cpRef))
	if !strings.Contains(deps, "kind IN ('client', 'producer')") {
		t.Error("bağımlılık okuması servisin istemci/üretici span'lerinden")
	}
	// istemci span'i giriş operasyonunun adını taşımaz — süzgeç uygulanırsa
	// sonuç sessizce BOŞ döner; çağıran bunu nota çevirir.
	if strings.Contains(deps, " AND name = ?") {
		t.Error("bağımlılık okumasına operation süzgeci uygulanmamalı")
	}
}

func first(s string, _ []any) string { return s }

// Yüzdelik pini: TEK tdigest, tüm pencere üzerinden. Kovaya göre gruplanan
// ya da yüzdelik ortalayan bir okuma geri gelirse kırmızı.
func TestComparePeriodsPercentilesAreWholeWindow(t *testing.T) {
	b := cpRawBuilders()
	red := cpFlat(first(b["red"](PeriodScope{Service: "checkout"}, cpCur, cpRef)))
	if !strings.Contains(red, "quantilesTDigest(0.5, 0.95, 0.99)(duration)") {
		t.Fatalf("RED yüzdelikleri ham süre üzerinden tdigest olmalı:\n%s", red)
	}
	if !strings.Contains(red, "GROUP BY period ORDER BY") {
		t.Errorf("RED yalnız döneme göre gruplanmalı (kova yok):\n%s", red)
	}
	for _, sql := range []string{red,
		cpFlat(first(b["ops"](PeriodScope{Service: "checkout"}, cpCur, cpRef))),
		cpFlat(first(b["deps"](PeriodScope{Service: "checkout"}, cpCur, cpRef))),
		cpFlat(first(comparePeriodsREDMVSQL("spanmetrics_1m", PeriodScope{Service: "checkout"}, cpCur, cpRef, periodCapRED))),
		cpFlat(first(comparePeriodsOpsMVSQL("spanmetrics_1m", PeriodScope{Service: "checkout"}, cpCur, cpRef, periodCapSecondary))),
		cpFlat(serviceWindowREDSQL()), cpFlat(serviceEnvWindowREDSQL()),
	} {
		for _, bad := range []string{"avg(", "avgWeighted", "GROUP BY time_bucket", "toStartOfInterval(time, INTERVAL 5 MINUTE) AS", "quantilesTDigestState"} {
			if strings.Contains(sql, bad) {
				t.Errorf("yüzdelik ortalaması / kova gruplaması izi %q:\n%s", bad, sql)
			}
		}
	}
	ops := first(b["ops"](PeriodScope{Service: "checkout"}, cpCur, cpRef))
	if !strings.Contains(ops, "quantileTDigestIf(0.95)(duration, period = 0)") || !strings.Contains(ops, "quantileTDigestIf(0.95)(duration, period = 1)") {
		t.Error("operasyon p95'i dönem başına tüm pencere tdigest'i olmalı")
	}
}

// Dağıtık güvenlik: alt sorgu/JOIN yok (GLOBAL gerektiren şekil yok), bağımlılık
// hedefi topolojiyle aynı zincir.
func TestComparePeriodsDistributedSafeShape(t *testing.T) {
	b := cpRawBuilders()
	for _, sql := range []string{
		first(b["red"](PeriodScope{Service: "s", Clusters: []string{"a"}}, cpCur, cpRef)),
		first(b["ops"](PeriodScope{Service: "s"}, cpCur, cpRef)),
		first(b["deps"](PeriodScope{Service: "s"}, cpCur, cpRef)),
		first(b["pods"](PeriodScope{Service: "s"}, cpCur, cpRef)),
		first(b["versions"](PeriodScope{Service: "s"}, cpCur, cpRef)),
		first(comparePeriodsREDMVSQL("spanmetrics_1m", PeriodScope{Service: "s"}, cpCur, cpRef, periodCapRED)),
		first(comparePeriodsOpsMVSQL("spanmetrics_1m", PeriodScope{Service: "s"}, cpCur, cpRef, periodCapSecondary)),
		first(comparePeriodsVersionsMVSQL(PeriodScope{Service: "s"}, cpCur, cpRef, periodCapSecondary)),
		first(periodEnvsSQL(true, "s", cpCur, cpRef, periodCapSecondary)),
		first(periodEnvsSQL(false, "s", cpCur, cpRef, periodCapSecondary)),
		serviceWindowREDSQL(), serviceEnvWindowREDSQL(),
	} {
		up := strings.ToUpper(sql)
		if strings.Contains(up, "(SELECT") || regexp.MustCompile(`\bJOIN\s+SPANS\b`).MatchString(up) {
			t.Errorf("alt sorgu/JOIN: dağıtıkta GLOBAL ister, bu okuma onsuz tasarlandı:\n%s", sql)
		}
	}
	deps := first(b["deps"](PeriodScope{Service: "s"}, cpCur, cpRef))
	if !strings.Contains(deps, externalPeerHostSQL) {
		t.Error("bağımlılık hedefi topology/external_paths ile aynı peer zinciri olmalı")
	}
	if !strings.Contains(deps, fmt.Sprintf("LIMIT %d", PeriodTopN+1)) || !strings.Contains(deps, fmt.Sprintf("max_execution_time = %d,", periodCapSecondary)) {
		t.Error("bağımlılık ham kalır ama LIMIT + düşük tavanla")
	}
	for _, ops := range []string{first(b["ops"](PeriodScope{Service: "s"}, cpCur, cpRef)),
		first(comparePeriodsOpsMVSQL("spanmetrics_1m", PeriodScope{Service: "s"}, cpCur, cpRef, periodCapSecondary))} {
		if !strings.Contains(ops, fmt.Sprintf("LIMIT %d", PeriodTopN+1)) {
			t.Error("operasyon okuması N+1 ile kırpılmayı ölçmeli")
		}
	}
	// v0.10.944 — pod ve sürüm AYRI okumalar: pod yalnız terfi kolonu (res
	// dizisi yok), sürüm MV ya da ham zincir. İkisi de ilk N + gerçek distinct.
	pods := first(b["pods"](PeriodScope{Service: "s"}, cpCur, cpRef))
	if strings.Contains(pods, "res_keys") || strings.Contains(pods, "res_values") || strings.Contains(pods, "ARRAY JOIN") || !strings.Contains(pods, "k8s_pod") {
		t.Errorf("pod okuması yalnız k8s_pod kolonu olmalı:\n%s", pods)
	}
	for name, vals := range map[string]string{"pods": pods, "versions": first(b["versions"](PeriodScope{Service: "s"}, cpCur, cpRef)),
		"versions_mv": first(comparePeriodsVersionsMVSQL(PeriodScope{Service: "s"}, cpCur, cpRef, periodCapSecondary))} {
		if !strings.Contains(vals, fmt.Sprintf("LIMIT %d BY period", PeriodTopN)) || !strings.Contains(vals, "count() OVER (PARTITION BY period)") {
			t.Errorf("%s: ilk N + gerçek distinct sayısı", name)
		}
	}
	if v := first(b["versions"](PeriodScope{Service: "s"}, cpCur, cpRef)); !strings.Contains(v, effectiveVersionExpr) || !strings.Contains(v, fmt.Sprintf("max_execution_time = %d,", periodCapVersions)) {
		t.Error("ham sürüm deploys.go effectiveVersionExpr zinciri olmalı, en düşük tavanla")
	}
}

// v0.10.944 — MV-önce: RED + top_operations spanmetrics_1m'den (kind MV
// boyutu). Merge indeksleri MV'nin (0.5, 0.9, 0.95, 0.99) düzenine göre
// 1/3/4 — 0.9 atlanırsa p95 yerine p90, p99 yerine p95 okunurdu.
func TestComparePeriodsMVBuilders(t *testing.T) {
	sc := PeriodScope{Service: "checkout", Operation: "GET /cart"}
	red, args := comparePeriodsREDMVSQL("spanmetrics_1m", sc, cpCur, cpRef, periodCapRED)
	bound := cpFlat(cpBind(t, red, args))
	for _, want := range []string{
		"WITH if(time_bucket >= T[2026-09-26T10:00:00Z] AND time_bucket < T[2026-09-26T11:00:00Z], 0, 1) AS period",
		"FROM spanmetrics_1m",
		"service_name = 'checkout' AND kind IN ('server', 'consumer') AND name = 'GET /cart'",
		"((time_bucket >= T[2026-09-26T10:00:00Z] AND time_bucket < T[2026-09-26T11:00:00Z]) OR (time_bucket >= T[2026-09-26T09:00:00Z] AND time_bucket < T[2026-09-26T10:00:00Z]))",
		"countMerge(calls_state)", "countMerge(error_state)",
		"arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state) AS q, 1)",
		"arrayElement(q, 3) / 1e6 AS p95_ms", "arrayElement(q, 4) / 1e6 AS p99_ms",
		"uniqExact(toStartOfInterval(time_bucket, INTERVAL 5 MINUTE))",
		fmt.Sprintf("max_execution_time = %d,", periodCapRED),
	} {
		if !strings.Contains(bound, want) {
			t.Errorf("RED MV %q içermeli:\n%s", want, bound)
		}
	}
	ops := cpFlat(first(comparePeriodsOpsMVSQL("spanmetrics_1m", sc, cpCur, cpRef, periodCapSecondary)))
	for _, want := range []string{
		"FROM spanmetrics_1m", "kind IN ('server', 'consumer')",
		"countMergeIf(calls_state, period = 0)", "countMergeIf(error_state, period = 1)",
		"arrayElement(quantilesTDigestMergeIf(0.5, 0.9, 0.95, 0.99)(duration_q_state, period = 0), 3)",
		"arrayElement(quantilesTDigestMergeIf(0.5, 0.9, 0.95, 0.99)(duration_q_state, period = 1), 3)",
	} {
		if !strings.Contains(ops, want) {
			t.Errorf("ops MV %q içermeli:\n%s", want, ops)
		}
	}
	// Kapıdan kaçan env MV kurucusunda olmayan kolona takılır (YÜKSEK SESLE
	// düşer) — sessizce ortamları birleştirmez.
	if env := first(comparePeriodsREDMVSQL("spanmetrics_1m", PeriodScope{Service: "s", Env: "prod"}, cpCur, cpRef, 8)); !strings.Contains(env, "deploy_env = ?") {
		t.Error("MV kurucusu env'i sessizce düşürmemeli")
	}
	vers, vargs := comparePeriodsVersionsMVSQL(PeriodScope{Service: "checkout"}, cpCur, cpRef, periodCapSecondary)
	vb := cpFlat(cpBind(t, vers, vargs))
	for _, want := range []string{"FROM service_version_5m", "countMerge(span_count_state)", "service_name = 'checkout' AND ((time_bucket >="} {
		if !strings.Contains(vb, want) {
			t.Errorf("sürüm MV %q içermeli:\n%s", want, vb)
		}
	}
}

// v0.10.944 — MV kapısı: ham yol YALNIZ env/cluster/namespace süzgecinde ya
// da pencere MV kapsamından önce başladığında.
func TestPeriodSpanmetricsGate(t *testing.T) {
	cov := cpT0.Add(-2 * time.Hour)
	cases := []struct {
		name   string
		sc     PeriodScope
		need   time.Time
		cov    time.Time
		wantMV bool
		reason string
	}{
		{"süzgeçsiz, kapsamda", PeriodScope{Service: "s"}, cpRef.From, cov, true, ""},
		{"operation MV boyutu", PeriodScope{Service: "s", Operation: "GET /cart"}, cpRef.From, cov, true, ""},
		{"kapsam sınırında", PeriodScope{Service: "s"}, cov, cov, true, ""},
		{"env", PeriodScope{Service: "s", Env: "prod"}, cpRef.From, cov, false, PeriodRawReasonScope},
		{"cluster", PeriodScope{Service: "s", Clusters: []string{"cluster-a"}}, cpRef.From, cov, false, PeriodRawReasonScope},
		{"namespace", PeriodScope{Service: "s", Namespace: "shop"}, cpRef.From, cov, false, PeriodRawReasonScope},
		{"kapsam geç başlıyor", PeriodScope{Service: "s"}, cpRef.From, cpT0.Add(-30 * time.Minute), false, PeriodRawReasonCoverage},
		{"kapsam bilinmiyor", PeriodScope{Service: "s"}, cpRef.From, time.Time{}, false, PeriodRawReasonCoverage},
	}
	for _, c := range cases {
		mv, reason := periodSpanmetricsGate(c.sc, c.need, c.cov)
		if mv != c.wantMV || reason != c.reason {
			t.Errorf("%s: mv=%v reason=%q, istenen %v %q", c.name, mv, reason, c.wantMV, c.reason)
		}
	}
	for _, c := range []struct {
		sc     PeriodScope
		covers bool
		want   bool
	}{
		{PeriodScope{Service: "s"}, true, true},
		{PeriodScope{Service: "s"}, false, false},
		{PeriodScope{Service: "s", Operation: "GET /cart"}, true, false}, // MV operasyon taşımaz
		{PeriodScope{Service: "s", Env: "prod"}, true, false},
	} {
		if got := periodVersionsGate(c.sc, c.covers); got != c.want {
			t.Errorf("sürüm kapısı %+v covers=%v: %v", c.sc, c.covers, got)
		}
	}
}

// Bitişik previous çifti ızgarada kova PAYLAŞMAZ; uzunluk korunur.
func TestPeriodGridContiguousPairSharesNoBucket(t *testing.T) {
	cur := PeriodWindow{From: cpT0.Add(30 * time.Second), To: cpT0.Add(time.Hour + 30*time.Second)}
	ref := PeriodWindow{From: cpT0.Add(-time.Hour + 30*time.Second), To: cur.From}
	gc, gr := periodGrid(cur, periodMVGrid), periodGrid(ref, periodMVGrid)
	if !gr.To.Equal(gc.From) || gc.To.Sub(gc.From) != time.Hour || gr.To.Sub(gr.From) != time.Hour {
		t.Errorf("ızgara: cur=%v ref=%v", gc, gr)
	}
	if !gc.From.Equal(cpT0) {
		t.Errorf("alt uç aşağı yuvarlanmalı: %v", gc.From)
	}
	g5 := periodGrid(PeriodWindow{From: cpT0.Add(7 * time.Minute), To: cpT0.Add(13 * time.Minute)}, periodBucket)
	if !g5.From.Equal(cpT0.Add(5*time.Minute)) || !g5.To.Equal(cpT0.Add(10*time.Minute)) {
		t.Errorf("5 dk ızgarası: %v", g5)
	}
}

func TestPeriodCapSeconds(t *testing.T) {
	now := cpT0
	cases := []struct {
		fixed int
		dl    time.Time
		ok    bool
		want  int
	}{
		{8, time.Time{}, false, 8},
		{8, now.Add(20 * time.Second), true, 8},
		{8, now.Add(5500 * time.Millisecond), true, 5},
		{4, now.Add(300 * time.Millisecond), true, 1},
		{4, now.Add(-time.Second), true, 1},
	}
	for _, c := range cases {
		if got := periodCapSeconds(c.fixed, c.dl, c.ok, now); got != c.want {
			t.Errorf("cap(%d, %v, %v) = %d, istenen %d", c.fixed, c.dl.Sub(now), c.ok, got, c.want)
		}
	}
}

// v0.10.944 — ortam keşfi iki pencere için TEK okuma; MV'de alt uç 5 dk
// kovasına hizalı.
func TestPeriodEnvsSQL(t *testing.T) {
	cur := PeriodWindow{From: cpT0.Add(2 * time.Minute), To: cpT0.Add(62 * time.Minute)}
	ref := PeriodWindow{From: cpT0.Add(-58 * time.Minute), To: cur.From}
	mv, margs := periodEnvsSQL(true, "checkout", cur, ref, periodCapSecondary)
	got := cpFlat(cpBind(t, mv, margs))
	for _, want := range []string{"FROM service_env_summary_5m", "service_name = 'checkout' AND deploy_env != ''",
		"(time_bucket >= T[2026-09-26T10:00:00Z] AND time_bucket < T[2026-09-26T11:02:00Z]) OR (time_bucket >= T[2026-09-26T09:00:00Z] AND time_bucket < T[2026-09-26T10:02:00Z])",
		"GROUP BY deploy_env ORDER BY deploy_env LIMIT 10"} {
		if !strings.Contains(got, want) {
			t.Errorf("env MV %q içermeli:\n%s", want, got)
		}
	}
	rq, rargs := periodEnvsSQL(false, "checkout", cur, ref, periodCapSecondary)
	raw := cpFlat(cpBind(t, rq, rargs))
	if !strings.Contains(raw, "FROM spans") || !strings.Contains(raw, "(time >= T[2026-09-26T10:02:00Z] AND time < T[2026-09-26T11:02:00Z]) OR (time >= T[2026-09-26T09:02:00Z]") {
		t.Errorf("env ham okuma iki pencereyi OR'lamalı:\n%s", raw)
	}
}

func TestServiceEnvWindowREDSQL(t *testing.T) {
	sql := cpFlat(serviceEnvWindowREDSQL())
	for _, want := range []string{"quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state)", "FROM service_env_summary_5m",
		"WHERE service_name = ? AND deploy_env = ? AND time_bucket >= ? AND time_bucket < ?"} {
		if !strings.Contains(sql, want) {
			t.Errorf("ortam RED %q içermeli:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "GROUP BY") || strings.Count(sql, "?") != 4 {
		t.Errorf("tek satır, 4 bind:\n%s", sql)
	}
}

// Hız ve kapsama okumanın GERÇEK (ızgaralı) pencereleriyle: 5 dk'lık
// [10:00:59, 10:06:00) penceresi MV'de [10:00, 10:06) okunur — istek/sn 6 dk
// üzerinden, kapsama 2 kova üzerinden.
func TestBuildPeriodComparisonUsesReadWindows(t *testing.T) {
	cur := PeriodWindow{From: cpT0.Add(59 * time.Second), To: cpT0.Add(6 * time.Minute)}
	ref := PeriodWindow{From: cur.From.Add(-cur.To.Sub(cur.From)), To: cur.From}
	raw := PeriodCompareRaw{
		RED:     [2]PeriodRED{{Requests: 360, Buckets: 2}, {Requests: 360, Buckets: 2}},
		Windows: [2]PeriodWindow{periodGrid(cur, periodMVGrid), periodGrid(ref, periodMVGrid)},
	}
	c := BuildPeriodComparison(raw, cur, ref)
	if c.Problem.RatePerS != 1 || c.Problem.BucketsTotal != 2 || c.Problem.Coverage != 1 {
		t.Errorf("ızgaralı pencere: %+v", c.Problem)
	}
}

func TestServiceWindowREDSQLMergesWholeWindow(t *testing.T) {
	sql := cpFlat(serviceWindowREDSQL())
	for _, want := range []string{
		"quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state)",
		"FROM service_summary_5m",
		"WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?",
		"max_execution_time",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("pencere RED'i %q içermeli:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "GROUP BY") {
		t.Errorf("tek pencere = tek satır; kova başına gruplama yüzdelik ortalamasına kapı açar:\n%s", sql)
	}
	if n := strings.Count(sql, "?"); n != 3 {
		t.Errorf("bind sayısı %d", n)
	}
}

func TestPeriodBucketsTouched(t *testing.T) {
	cases := []struct {
		from, to time.Time
		want     int
	}{
		{cpT0, cpT0.Add(time.Hour), 12},
		{cpT0.Add(2 * time.Minute), cpT0.Add(32 * time.Minute), 7}, // hizasız uçlar iki kısmi kova
		{cpT0, cpT0.Add(5 * time.Minute), 1},
		{cpT0, cpT0.Add(24 * time.Hour), 288},
		{cpT0, cpT0, 0},
		{cpT0.Add(time.Hour), cpT0, 0},
	}
	for _, c := range cases {
		if got := PeriodBucketsTouched(c.from, c.to); got != c.want {
			t.Errorf("%s→%s: %d, istenen %d", c.from.Format(time.Kitchen), c.to.Format(time.Kitchen), got, c.want)
		}
	}
}

func TestBuildPeriodComparison(t *testing.T) {
	raw := PeriodCompareRaw{
		RED: [2]PeriodRED{
			{Requests: 3600, Errors: 360, P50Ms: 40, P95Ms: 812.34, P99Ms: 1500, Buckets: 12},
			{Requests: 3600, Errors: 36, P50Ms: 30, P95Ms: 400, P99Ms: 900, Buckets: 9},
		},
		Ops: []PeriodPairRow{
			{Key: "GET /cart", Count: [2]uint64{2520, 1080}, Errors: [2]uint64{300, 10}, P95Ms: [2]float64{1200, 500}},
			{Key: "POST /pay", Count: [2]uint64{1080, 2520}, Errors: [2]uint64{60, 26}, P95Ms: [2]float64{300, 350}},
			{Key: "GET /legacy", Count: [2]uint64{0, 0}},
		},
		Deps: []PeriodPairRow{
			{Kind: "db", Key: "postgresql@db-a", Count: [2]uint64{5000, 4000}, Errors: [2]uint64{50, 0}, P95Ms: [2]float64{80, 20}},
			{Kind: "service", Key: "inventory", Count: [2]uint64{0, 900}, P95Ms: [2]float64{0, 45}},
		},
		Values: []PeriodValueRow{
			{Period: 0, Dim: "pod", Value: "checkout-7d9-abc", Count: 2000, Distinct: 3},
			{Period: 0, Dim: "version", Value: "1.4.0", Count: 3600, Distinct: 1},
			{Period: 1, Dim: "version", Value: "1.3.9", Count: 3600, Distinct: 1},
		},
	}
	c := BuildPeriodComparison(raw, cpCur, cpRef)

	// Yüzdelik OLDUĞU GİBİ geçer (yalnız 1 ondalık yuvarlama); operasyonların
	// p95'leri servis p95'ini ETKİLEMEZ — ortalama yok.
	if c.Problem.P95Ms != 812.3 || c.Reference.P95Ms != 400 {
		t.Errorf("p95 geçişi: %v / %v", c.Problem.P95Ms, c.Reference.P95Ms)
	}
	// v0.10.944 — hata oranları YÜZDE (error_rate_pct); fark yüzde puanı.
	if c.Problem.RatePerS != 1 || c.Problem.ErrorRate != 10 || c.Problem.Samples != 3600 || c.Problem.LowSample {
		t.Errorf("sorun dönemi: %+v", c.Problem)
	}
	if c.Problem.Coverage != 1 || c.Reference.Coverage != 0.75 || c.Reference.BucketsTotal != 12 {
		t.Errorf("kapsama: %v %v %d", c.Problem.Coverage, c.Reference.Coverage, c.Reference.BucketsTotal)
	}
	// pay + karışım kayması: GET /cart %30 → %70 = +40 puan
	if len(c.TrafficMixShift) != 3 || c.TrafficMixShift[0].DeltaPP != 40 && c.TrafficMixShift[0].DeltaPP != -40 {
		t.Fatalf("karışım kayması: %+v", c.TrafficMixShift)
	}
	if c.MaxMixShiftOperation != "GET /cart" || math.Abs(c.MaxMixShiftPP-40) > 1e-9 {
		t.Errorf("en büyük kayma: %s %v", c.MaxMixShiftOperation, c.MaxMixShiftPP)
	}
	if got := c.Problem.TopOperations; len(got) != 2 || got[0].Operation != "GET /cart" || got[0].Share != 0.7 || got[0].P95Ms != 1200 {
		t.Errorf("sorun operasyonları (sayısı 0 olan satır düşer, sayıya göre sıralı): %+v", got)
	}
	if got := c.Reference.TopOperations; len(got) != 2 || got[0].Operation != "POST /pay" {
		t.Errorf("referans operasyonları kendi sayısına göre sıralı: %+v", got)
	}
	// bağımlılık: yalnız o dönemde çağrılanlar
	if len(c.Problem.Dependencies) != 1 || len(c.Reference.Dependencies) != 2 || c.Problem.Dependencies[0].ErrorRate != 1 {
		t.Errorf("bağımlılıklar: %+v / %+v", c.Problem.Dependencies, c.Reference.Dependencies)
	}
	if c.Problem.PodsDistinct != 3 || len(c.Problem.Pods) != 1 || c.Problem.Versions[0].Value != "1.4.0" || c.Reference.Versions[0].Value != "1.3.9" {
		t.Errorf("pod/sürüm: %+v / %+v", c.Problem, c.Reference)
	}
	// farklar: mutlak + göreli
	if d := c.Deltas.P95Ms; d.Abs != 412.3 || d.RelPct == nil || *d.RelPct != 103.1 {
		t.Errorf("p95 farkı: %+v", d)
	}
	if d := c.Deltas.ErrorRate; d.Abs != 9 || d.RelPct == nil || *d.RelPct != 900 {
		t.Errorf("hata oranı farkı: %+v", d)
	}
	if d := c.Deltas.Requests; d.Abs != 0 || d.RelPct == nil || *d.RelPct != 0 {
		t.Errorf("istek farkı: %+v", d)
	}
}

func TestBuildPeriodComparisonLowSampleAndEmpty(t *testing.T) {
	raw := PeriodCompareRaw{RED: [2]PeriodRED{{Requests: 12, Errors: 1, P95Ms: 90, Buckets: 3}, {}}}
	c := BuildPeriodComparison(raw, cpCur, cpRef)
	if !c.Problem.LowSample || !c.Reference.LowSample {
		t.Errorf("düşük örnek bayrağı: %+v / %+v", c.Problem, c.Reference)
	}
	if c.Reference.Coverage != 0 || c.Problem.Coverage != 0.25 {
		t.Errorf("kapsama: %v %v", c.Problem.Coverage, c.Reference.Coverage)
	}
	// referans örneksiz: yüzdelik/oran farkı hesaplanmaz, göreli fark tanımsız
	if d := c.Deltas.P95Ms; !d.NoData || d.Abs != 0 || d.RelPct != nil {
		t.Errorf("örneksiz dönem farkı: %+v", d)
	}
	if d := c.Deltas.Requests; d.RelPct != nil || d.Abs != 12 {
		t.Errorf("referans 0 → göreli fark nil: %+v", d)
	}
	// iki dönemde de istek yoksa karışım kayması hesaplanmaz, listeler boş (null değil)
	if len(c.TrafficMixShift) != 0 || c.TrafficMixShift == nil || c.Problem.TopOperations == nil || c.Reference.Pods == nil {
		t.Errorf("boş dilimler JSON'da [] olmalı: %+v", c)
	}
}

func TestBuildPeriodComparisonTruncation(t *testing.T) {
	var ops []PeriodPairRow
	for i := 0; i < PeriodTopN+1; i++ {
		ops = append(ops, PeriodPairRow{Key: fmt.Sprintf("op-%02d", i), Count: [2]uint64{10, 10}})
	}
	c := BuildPeriodComparison(PeriodCompareRaw{RED: [2]PeriodRED{{Requests: 200}, {Requests: 200}}, Ops: ops, Deps: ops}, cpCur, cpRef)
	if !c.OperationsTruncated || len(c.Problem.TopOperations) != PeriodTopN || !c.DependenciesTrunc || len(c.Problem.Dependencies) != PeriodTopN {
		t.Errorf("N+1 → kırpıldı: ops=%v(%d) deps=%v(%d)", c.OperationsTruncated, len(c.Problem.TopOperations), c.DependenciesTrunc, len(c.Problem.Dependencies))
	}
}

// Sürüm zinciri Go ikizi SQL'den türetilen listeyle AYNI olmalı — ayrışırsa
// get_trace bağlamı ile compare_periods sürümü aynı pod için farklı söyler.
func TestEffectiveVersionMirrorsSQL(t *testing.T) {
	keyRe := regexp.MustCompile(`indexOf\(res_keys, '([^']+)'\)\] NOT IN`)
	var sqlKeys []string
	for _, m := range keyRe.FindAllStringSubmatch(effectiveVersionExpr, -1) {
		sqlKeys = append(sqlKeys, m[1])
	}
	if fmt.Sprint(sqlKeys) != fmt.Sprint(effectiveVersionKeys) {
		t.Errorf("sürüm anahtar sırası ayrıştı:\nSQL %v\nGo  %v", sqlKeys, effectiveVersionKeys)
	}
	litRe := regexp.MustCompile(`'([^']*)'`)
	var sqlPH []string
	for _, m := range litRe.FindAllStringSubmatch(placeholderVersionList, -1) {
		sqlPH = append(sqlPH, m[1])
	}
	var goPH []string
	for k := range placeholderVersions {
		goPH = append(goPH, k)
	}
	sort.Strings(sqlPH)
	sort.Strings(goPH)
	if fmt.Sprint(sqlPH) != fmt.Sprint(goPH) {
		t.Errorf("yer tutucu listesi ayrıştı:\nSQL %q\nGo  %q", sqlPH, goPH)
	}
	for _, c := range []struct {
		res  map[string]string
		want string
	}{
		{map[string]string{"service.version": "2.1.0", "container.image.tag": "release.20260926.1"}, "release.20260926.1"},
		{map[string]string{"service.version": "2.1.0", "container.image.tag": "latest"}, "2.1.0"},
		{map[string]string{"service.version": "0.0.1-SNAPSHOT", "helm.chart.version": "3.2.1"}, "3.2.1"},
		{map[string]string{"service.version": ""}, ""},
		{nil, ""},
	} {
		if got := EffectiveVersion(c.res); got != c.want {
			t.Errorf("EffectiveVersion(%v)=%q, istenen %q", c.res, got, c.want)
		}
	}
}
