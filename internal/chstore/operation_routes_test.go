package chstore

// operation_routes_test.go — v0.10.1023. Operatör bildirimi: "Operation
// kısmında POST GET neden detail gözükmüyor, sonra trace'e girince çıkıyor."
// Çivilenen: (1) spanmetrics_1m okumasının şekli (tablo, IN listesi, zaman
// sınırları, GROUP BY, LIMIT, bütçe, 4 genişlikli tdigest indeksleri);
// (2) pencere + sparkline ızgarasının ops MV okumasıyla (queryOperationsFromMV)
// AYNI olması — "All" satırı serileri eleman eleman topluyor; (3) çıplak fiil
// kümesinin tek Go yazımı ile frontend kümesinin eşitliği; (4) satır montajı.

import (
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestBareVerbRouteOpsSQLShape(t *testing.T) {
	sql := bareVerbRouteOpsSQL("spanmetrics_1m")
	for _, w := range []string{
		"FROM spanmetrics_1m",
		"WHERE service_name = ?",
		"AND name IN ('GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS', 'TRACE', 'CONNECT')",
		"AND time_bucket >= ? AND time_bucket < ?",
		"GROUP BY name, http_route, b",
		"GROUP BY name, http_route\n",
		"intDiv(toInt64(toUnixTimestamp(time_bucket)) - ?, ?)",
		"countMerge(calls_state)",
		"countMerge(error_state)",
		"sumMerge(duration_sum_state)",
		// 4 genişlikli durum: 1 = p50, 3 = p95, 4 = p99 (2 = p90, okunmaz).
		"quantilesTDigestMergeState(0.5, 0.9, 0.95, 0.99)(duration_q_state)",
		"arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(q_state) AS q, 1) / 1e6 AS p50_ms",
		"arrayElement(q, 3) / 1e6",
		"arrayElement(q, 4) / 1e6",
		"groupArray(arrayElement(q_slot, 1) / 1e6)",
		"groupArray(arrayElement(q_slot, 3) / 1e6)",
		"groupArray(arrayElement(q_slot, 4) / 1e6)",
		"ORDER BY calls DESC",
		"LIMIT 300",
		"SETTINGS max_execution_time = 15",
	} {
		if !strings.Contains(sql, w) {
			t.Errorf("SQL %q taşımalı:\n%s", w, sql)
		}
	}
	for _, bad := range []string{"FROM spans", "coremetry.", "arrayElement(q, 2)", "arrayElement(q_slot, 2)", "deploy_env"} {
		if strings.Contains(sql, bad) {
			t.Errorf("SQL %q taşımamalı:\n%s", bad, sql)
		}
	}
	// Argüman sayısı: slot kökeni, slot genişliği, servis, alt + üst sınır.
	if got := strings.Count(sql, "?"); got != 5 {
		t.Errorf("yer tutucu sayısı %d, 5 beklenir (Query çağrısının argüman sırası)", got)
	}
}

func TestBareHTTPMethodsLiteralSafe(t *testing.T) {
	re := regexp.MustCompile(`^[A-Z]+$`)
	if len(BareHTTPMethods) != 9 {
		t.Fatalf("küme %d fiil, 9 beklenir: %v", len(BareHTTPMethods), BareHTTPMethods)
	}
	for _, m := range BareHTTPMethods {
		if !re.MatchString(m) {
			t.Errorf("%q SQL'e literal gömülüyor — yalnız büyük harf olmalı", m)
		}
	}
	if bareHTTPMethodRe != `^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT)$` {
		t.Errorf("trace_health regex'i listeden türemeli: %s", bareHTTPMethodRe)
	}
}

// Frontend'in kümesi (lib/opDisplayName.ts HTTP_METHODS) Go listesiyle aynı
// olmalı: Operations satırını tarayıcı isBareHTTPMethod ile, sunucu bu
// listeyle seçiyor. (templater tarafı: templater/http_methods_pin_test.go.)
func TestBareHTTPMethodsMatchFrontend(t *testing.T) {
	b, err := os.ReadFile("../../frontend/src/lib/opDisplayName.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const HTTP_METHODS = new Set\(\[([^\]]*)\]\)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("opDisplayName.ts'te HTTP_METHODS kümesi bulunamadı — test bayatladı")
	}
	var fe []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		fe = append(fe, q[1])
	}
	goList := append([]string(nil), BareHTTPMethods...)
	sort.Strings(fe)
	sort.Strings(goList)
	if strings.Join(fe, ",") != strings.Join(goList, ",") {
		t.Errorf("FE kümesi %v ≠ Go kümesi %v", fe, goList)
	}
}

// Izgara pini: ops okumasının pencere + ızgara hesabı repo.go'da elle
// yazılı; planOperationRoutes onun çevirisi. Kaynak değişirse bu test
// kırmızı verir ve iki taraf birlikte güncellenir.
func TestOpsMVReadGridSourcePin(t *testing.T) {
	b, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, w := range []string{
		"bucketStart := winStart.Truncate(5 * time.Minute)",
		"WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?`+opFilter+`",
		"winSec := int64(winEnd.Sub(bucketStart).Seconds())",
		"bucketSec, nBuckets := sparklineGrid(winSec, 300)",
		"intDiv(toUInt32(time_bucket) - toUInt32(?), ?) AS bidx",
		"return window >= 5*time.Minute && env == \"\"",
	} {
		if !strings.Contains(src, w) {
			t.Errorf("repo.go ops MV okuması değişmiş (%q) — planOperationRoutes'u birlikte güncelle", w)
		}
	}
}

// opsReadSlots — repo.go'nun ops MV okumasının MODELİ: pencerenin aldığı
// 5 dk kovaları ve her birinin slotu. Üst sınır sürücünün yazdığı gibi
// SANİYEYE aşağı (clickhouse-go bindPositional → format(tz, Seconds, v) →
// value.Unix()); ızgara Go'daki tam hassasiyetli hesapla (repo.go).
func opsReadSlots(winStart, winEnd time.Time) (map[int64]int64, int64, int) {
	bucketStart := winStart.Truncate(5 * time.Minute)
	winSec := int64(winEnd.Sub(bucketStart).Seconds())
	bucketSec, n := sparklineGrid(winSec, 300)
	boundEnd := time.Unix(winEnd.Unix(), 0) // `time_bucket < toDateTime('<unix>')`
	out := map[int64]int64{}
	for b := bucketStart; b.Before(boundEnd); b = b.Add(5 * time.Minute) {
		out[b.Unix()] = (b.Unix() - bucketStart.Unix()) / bucketSec
	}
	return out, bucketSec, n
}

func TestPlanOperationRoutesMatchesOpsMVRead(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		from, to time.Time
	}{
		{"1h hizasız", base.Add(3*time.Minute + 17*time.Second), base.Add(63*time.Minute + 17*time.Second)},
		{"1h hizalı", base, base.Add(time.Hour)},
		{"to tam 5 dk sınırında", base.Add(2 * time.Minute), base.Add(90 * time.Minute)},
		{"to sınırın 1 sn sonrası", base.Add(2 * time.Minute), base.Add(90*time.Minute + time.Second)},
		// R3 — sürücü saniyeye aşağı yazar: ops B kovasını DIŞLAR, plan da dışlamalı.
		{"to sınırın 400 ms sonrası", base.Add(2 * time.Minute), base.Add(90*time.Minute + 400*time.Millisecond)},
		{"to tam dakikada (5 dk değil)", base.Add(2 * time.Minute), base.Add(93 * time.Minute)},
		{"to dakika + 999 ms", base.Add(2 * time.Minute), base.Add(93*time.Minute + 999*time.Millisecond)},
		{"5 dk tam", base.Add(time.Minute), base.Add(6 * time.Minute)},
		{"24h", base.Add(-24*time.Hour + 41*time.Second), base.Add(41 * time.Second)},
		{"7g", base.Add(-7 * 24 * time.Hour).Add(4 * time.Minute), base.Add(4 * time.Minute)},
		{"10 sa sınırı", base.Add(-10 * time.Hour), base},
		{"10 sa + 1 dk", base.Add(-10*time.Hour - time.Minute), base},
		{"ns hassasiyetli uçlar", base.Add(3*time.Minute + 123456789), base.Add(63*time.Minute + 17*time.Second + 987654321)},
	}
	for _, c := range cases {
		p := planOperationRoutes(c.from, c.to)
		if !p.useMV {
			t.Fatalf("%s: ≥5 dk pencere MV yolunda olmalı", c.name)
		}
		want, bucketSec, n := opsReadSlots(c.from, c.to)
		if p.bucketSec != bucketSec || p.n != n {
			t.Errorf("%s: ızgara %d×%d, ops %d×%d", c.name, p.bucketSec, p.n, bucketSec, n)
		}
		endSec := c.to.Unix()
		// (1) Taranan her 1 dk kovası ops'un aldığı bir 5 dk kovasından ve o
		// kovanın slotunda; (2) R4 kelepçesi: hiçbiri `to`'dan (saniye) sonra
		// başlamaz; (3) başlangıcı `to`'dan önce olan her dakika taranır.
		got := map[int64]bool{}
		for m := p.start; m.Before(p.scanEnd); m = m.Add(time.Minute) {
			five := m.Truncate(5 * time.Minute).Unix()
			slot, ok := want[five]
			if !ok {
				t.Errorf("%s: 1 dk kovası %s ops penceresinde olmayan 5 dk kovasından", c.name, m)
				continue
			}
			if m.Unix() >= endSec {
				t.Errorf("%s: 1 dk kovası %s to'dan (%s) sonra başlıyor — bundle'dan sonra gelen trafik", c.name, m, time.Unix(endSec, 0).UTC())
			}
			b := (m.Unix() - p.start.Unix()) / p.bucketSec // SQL: intDiv(ts - start, bucketSec)
			if b != slot || b < 0 || b >= int64(p.n) {
				t.Errorf("%s: %s slotu %d, ops slotu %d (n=%d)", c.name, m, b, slot, p.n)
			}
			got[five] = true
		}
		if last := p.scanEnd.Add(-time.Minute); !(last.Unix() < endSec && p.scanEnd.Unix() >= endSec) {
			t.Errorf("%s: tarama sonu %s, to %s — to'yu içeren dakikada bitmeli", c.name, p.scanEnd.UTC(), time.Unix(endSec, 0).UTC())
		}
		for five := range want {
			if !got[five] {
				t.Errorf("%s: ops'un aldığı 5 dk kovası %d 1 dk taramasında yok", c.name, five)
			}
		}
	}
}

// R3 — tam sınır: to = B + 400 ms. Ops okuması (saniye) B kovasını almaz;
// plan B'de bitmeli ve ızgara anahtarı to = B ile aynı olmalı.
func TestPlanOperationRoutesSubSecondBoundary(t *testing.T) {
	from := time.Date(2026, 10, 1, 9, 2, 0, 0, time.UTC)
	b := time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)
	p := planOperationRoutes(from, b.Add(400*time.Millisecond))
	if !p.scanEnd.Equal(b) {
		t.Errorf("tarama sonu %s, %s beklenir (ops `time_bucket < B`)", p.scanEnd, b)
	}
	if OperationRoutesGridKey(from, b.Add(400*time.Millisecond)) != OperationRoutesGridKey(from, b) {
		t.Error("B+400ms ile B aynı okumayı yapar — anahtar aynı olmalı")
	}
}

func TestPlanOperationRoutesShortWindow(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	if p := planOperationRoutes(base, base.Add(4*time.Minute+59*time.Second)); p.useMV {
		t.Error("<5 dk pencere: ops ham spans yolunda (1 sn ızgara) — bölme planı olmamalı")
	}
	if k := OperationRoutesGridKey(base, base.Add(4*time.Minute)); k != "mv=0" {
		t.Errorf("kısa pencere anahtarı %q", k)
	}
}

// Aynı 30 sn önbellek hücresine düşen iki `to`: tam 5 dk sınırı ve 1 sn
// sonrası farklı ızgara üretir — anahtar ayırmalı.
func TestOperationRoutesGridKeyDistinct(t *testing.T) {
	from := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	onEdge := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	a := OperationRoutesGridKey(from, onEdge)
	b := OperationRoutesGridKey(from, onEdge.Add(time.Second))
	if a == b {
		t.Errorf("sınır ve sınır+1 sn aynı anahtar: %s", a)
	}
	if OperationRoutesGridKey(from, onEdge.Add(2*time.Second)) != b {
		t.Error("aynı ızgara aynı anahtarı vermeli")
	}
}

func TestOpRoutesCovered(t *testing.T) {
	cov := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name     string
		coverage time.Time
		need     time.Time
		want     bool
	}{
		{"kapsam bilinmiyor", time.Time{}, cov.Add(time.Hour), false},
		{"pencere kapsamdan önce", cov, cov.Add(-time.Minute), false},
		{"tam kapsam başı", cov, cov, true},
		{"kapsam içinde", cov, cov.Add(48 * time.Hour), true},
	} {
		if got := opRoutesCovered(c.coverage, c.need); got != c.want {
			t.Errorf("%s: %v, %v beklenir", c.name, got, c.want)
		}
	}
}

func TestAssembleRouteRows(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	nan := math.NaN()
	raw := []routeRowRaw{
		{
			name: "GET", route: "/metrics", calls: 30, errors: 3,
			avgMs: f(12.5), p50: f(10), p95: f(20), p99: f(40),
			slotIdx:   []int64{0, 2, 7}, // 7 ızgara dışı → düşer
			slotCalls: []uint64{10, 20, 99}, slotErrs: []uint64{1, 2, 9},
			slotSumMs: []float64{100, 300, 1}, slotP50: []float64{9.999, 11, 1},
			slotP95: []float64{19, 21, 1}, slotP99: []float64{nan, 41.234, 1},
		},
		{name: "GET", route: "", calls: 0, avgMs: nil, p50: f(nan)},
	}
	out := assembleRouteRows(raw, 3)
	if len(out) != 2 {
		t.Fatalf("%d satır", len(out))
	}
	g := out[0]
	if g.Name != "GET" || g.Route != "/metrics" || g.SpanCount != 30 || g.ErrorCount != 3 || g.ErrorRate != 10 {
		t.Errorf("skalerler: %+v", g)
	}
	if g.AvgMs != 12.5 || g.P50Ms != 10 || g.P95Ms != 20 || g.P99Ms != 40 || g.Apdex != 0 {
		t.Errorf("gecikme: %+v", g)
	}
	if want := []uint64{10, 0, 20}; !equalU(g.Sparkline, want) {
		t.Errorf("sparkline %v, %v", g.Sparkline, want)
	}
	if want := []uint64{1, 0, 2}; !equalU(g.ErrorsSparkline, want) {
		t.Errorf("errors %v, %v", g.ErrorsSparkline, want)
	}
	if want := []float64{0, 0, 41.23}; !equalF(g.P99Sparkline, want) {
		t.Errorf("p99 (NaN → 0, round2) %v, %v", g.P99Sparkline, want)
	}
	if want := []float64{10, 0, 15}; !equalF(g.AvgSparkline, want) {
		t.Errorf("avg %v, %v", g.AvgSparkline, want)
	}
	if want := []float64{10, 0, 11}; !equalF(g.P50Sparkline, want) {
		t.Errorf("p50 %v, %v", g.P50Sparkline, want)
	}
	r := out[1]
	if r.Route != "" || r.ErrorRate != 0 || r.AvgMs != 0 || r.P50Ms != 0 {
		t.Errorf("artık satır: %+v", r)
	}
	if len(r.Sparkline) != 3 {
		t.Errorf("artık satır da aynı ızgarada: %v", r.Sparkline)
	}
}

func TestAssembleRouteRowsLatSparkCap(t *testing.T) {
	raw := make([]routeRowRaw, latSparkCap+1)
	for i := range raw {
		raw[i] = routeRowRaw{name: "POST", route: "/r", calls: 1, slotIdx: []int64{0},
			slotCalls: []uint64{1}, slotErrs: []uint64{0}, slotSumMs: []float64{1},
			slotP50: []float64{1}, slotP95: []float64{1}, slotP99: []float64{1}}
	}
	out := assembleRouteRows(raw, 1)
	if out[latSparkCap-1].P95Sparkline == nil || out[latSparkCap].P95Sparkline != nil {
		t.Error("avg/p50/p95 serileri yalnız ilk latSparkCap satırda (ops okumasının sözleşmesi)")
	}
	if out[latSparkCap].Sparkline == nil || out[latSparkCap].P99Sparkline == nil {
		t.Error("calls/errors/p99 serileri her satırda")
	}
}

func equalU(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalF(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
