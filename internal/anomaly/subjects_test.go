package anomaly

// v0.10.1791 — Davis paritesi FAZ 1.3: özne tabanı (endpoint / db / queue) +
// mevsimsel dilim (karar 3). Pinlenen sözleşmeler:
//
//   - Servis yolu UTC'de BAYT BAYT eski (golden: testdata/seasonal_query_pre1791.golden;
//     VM kova kümesi eski uygulamanın kopyasıyla kıyaslanır).
//   - Dilim yalnız gün sınıfı sınırını kaydırır (TR'de yaz saati yok): sınır
//     dışı anda UTC ve İstanbul kümeleri AYNI; Cuma 22:00 UTC İstanbul'da cumartesi.
//   - Özne anahtarı, üst-N seçimi, bütçe planlayıcısı (tablolu).
//   - Endpoint VM ifadeleri (svc, route) ile gruplanır ve SABİT çapalı üst-N
//     seçimiyle süzülür; servis kipinde ifade eskisi.
//   - db/queue CH sorguları MV'den, sınırlı (LIMIT + max_execution_time +
//     zaman WHERE), demet IN bağlı değer listesi.
//   - Özneler kapalıyken scanSubjects hiçbir okuma yapmaz.

import (
	"context"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestSeasonalQueryUTCIsPre1791Golden(t *testing.T) {
	raw, err := os.ReadFile("testdata/seasonal_query_pre1791.golden")
	if err != nil {
		t.Fatal(err)
	}
	golden := string(raw)
	var got, ist strings.Builder
	for _, m := range []string{"error_rate", "p99_ms", "request_rate"} {
		v, _ := metricValueExpr(m)
		got.WriteString("=== " + m + " all\n" + buildAllSeasonalQueryTZ(v, "UTC") + "\n=== " + m + " cal2\n" + buildCalendarSeasonalQueryTZ(v, 2, "UTC") + "\n")
		ist.WriteString("=== " + m + " all\n" + buildAllSeasonalQueryTZ(v, "Europe/Istanbul") + "\n=== " + m + " cal2\n" + buildCalendarSeasonalQueryTZ(v, 2, "Europe/Istanbul") + "\n")
		// Sarmalayıcılar da UTC.
		if buildAllSeasonalQuery(v) != buildAllSeasonalQueryTZ(v, "UTC") || buildCalendarSeasonalQuery(v, 2) != buildCalendarSeasonalQueryTZ(v, 2, "UTC") {
			t.Errorf("%s: sarmalayıcı UTC değil", m)
		}
	}
	if got.String() != golden {
		t.Error("UTC mevsimsel sorgu metni v0.10.1790'dan farklı (servis yolu bayt bayt eski olmalı)")
	}
	// İstanbul: tek fark dilim adı.
	if want := strings.ReplaceAll(golden, "'UTC'", "'Europe/Istanbul'"); ist.String() != want {
		t.Error("İstanbul metni yalnız dilim adında ayrışmalı")
	}
}

// oldVMSeasonalBucketsCal — v0.10.1790 uygulamasının kopyası (oracle).
func oldVMSeasonalBucketsCal(at time.Time, days, neighborBuckets int, cal chstore.AnomalyCalendarConfig) ([]int64, string) {
	at = at.UTC()
	cutoff := at.Add(-time.Duration(days) * 24 * time.Hour)
	target := slotSecondsOfDay(at)
	radius := neighborBuckets * bucketSeconds
	upper := at.Add(-time.Duration(radius+bucketSeconds) * time.Second)
	class := calendarFallbackClass(dayClassCal(at.UTC(), cal), at.UTC())
	var out []int64
	for _, b := range vmBucketsBetween(cutoff.Unix(), upper.Unix()) {
		t := time.Unix(b, 0).UTC()
		if dayClass(t) != class {
			continue
		}
		sod := t.Hour()*3600 + t.Minute()*60
		d := sod - target
		if d < 0 {
			d = -d
		}
		if 86400-d < d {
			d = 86400 - d
		}
		if d <= radius {
			out = append(out, b)
		}
	}
	return out, class
}

func TestVMSeasonalBucketsUTCUnchanged(t *testing.T) {
	for _, at := range []time.Time{
		time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC),
		time.Date(2026, 10, 23, 23, 58, 0, 0, time.UTC), // gece yarısı sarması
		time.Date(2026, 10, 24, 0, 3, 0, 0, time.UTC),   // cumartesi
		time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),  // pazar
	} {
		got, gc := vmSeasonalBucketsCal(at, seasonalDays, seasonalNeighborBuckets, chstore.AnomalyCalendarConfig{}, time.UTC)
		want, wc := oldVMSeasonalBucketsCal(at, seasonalDays, seasonalNeighborBuckets, chstore.AnomalyCalendarConfig{})
		if gc != wc || len(got) != len(want) {
			t.Fatalf("%v: sınıf %s/%s, %d/%d kova", at, gc, wc, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%v: kova %d farklı", at, i)
			}
		}
	}
}

func TestSeasonalTimezoneShiftsDayClassOnly(t *testing.T) {
	ist := seasonalLocation("Europe/Istanbul")
	// Çarşamba 12:00 UTC — gün sınırından uzak: iki dilimde küme AYNI
	// (sabit ofsette dairesel saat uzaklığı değişmez).
	mid := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	a, ac := vmSeasonalBucketsCal(mid, seasonalDays, seasonalNeighborBuckets, chstore.AnomalyCalendarConfig{}, time.UTC)
	b, bc := vmSeasonalBucketsCal(mid, seasonalDays, seasonalNeighborBuckets, chstore.AnomalyCalendarConfig{}, ist)
	if ac != "weekday" || bc != "weekday" || len(a) != len(b) {
		t.Fatalf("sınır dışı: %s/%s %d/%d", ac, bc, len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("sınır dışı anda kümeler ayrıştı")
		}
	}
	// Cuma 22:00 UTC = Cumartesi 01:00 İstanbul.
	fri := time.Date(2026, 10, 23, 22, 0, 0, 0, time.UTC)
	_, uc := vmSeasonalBucketsCal(fri, seasonalDays, seasonalNeighborBuckets, chstore.AnomalyCalendarConfig{}, time.UTC)
	set, ic := vmSeasonalBucketsCal(fri, seasonalDays, seasonalNeighborBuckets, chstore.AnomalyCalendarConfig{}, ist)
	if uc != "weekday" || ic != "saturday" {
		t.Fatalf("Cuma 22:00 UTC: UTC %s (want weekday), İstanbul %s (want saturday)", uc, ic)
	}
	if len(set) == 0 {
		t.Fatal("İstanbul cumartesi kümesi boş")
	}
	for _, bkt := range set {
		if wd := time.Unix(bkt, 0).In(ist).Weekday(); wd != time.Saturday {
			t.Fatalf("kova %d yerel %v, want Saturday", bkt, wd)
		}
	}
	// CH tarafı aynı dilimi kullanır: bağlar İstanbul saatinden.
	_, _, cls, target, _ := seasonalBinds(fri, seasonalDays, seasonalNeighborBuckets, ist)
	if cls != "saturday" || target != 3600 {
		t.Errorf("seasonalBinds İstanbul: sınıf %s slot %d, want saturday 3600", cls, target)
	}
}

func TestSeasonalLocation(t *testing.T) {
	cases := map[string]string{
		"":                 "Europe/Istanbul",
		"UTC":              "UTC",
		"utc":              "UTC",
		"Europe/Istanbul":  "Europe/Istanbul",
		"America/New_York": "America/New_York",
		"Mars/Olympus":     "UTC", // yüklenemeyen → eski davranış
		"Local":            "UTC",
	}
	for in, want := range cases {
		if got := seasonalTZLiteral(seasonalLocation(in)); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if seasonalDayKey(time.UTC) != nil || seasonalDayKey(nil) != nil {
		t.Error("UTC gün anahtarı nil olmalı (t/86400 — eski davranış)")
	}
	if k := seasonalDayKey(seasonalLocation("Europe/Istanbul")); k == nil || k(time.Date(2026, 10, 23, 22, 0, 0, 0, time.UTC).Unix()) != 20261024 {
		t.Error("İstanbul gün anahtarı yerel gün değil")
	}
	if seasonalDay(86400*3+5, nil) != 3 {
		t.Error("seasonalDay nil anahtarla UTC günü değil")
	}
}

// TestScanWiresSeasonalTimezone — dilim ayardan okunup İKİ kaynağa da gidiyor
// (parametre olarak geçen probe testsiz kablolamadır — [[probe-as-parameter]]).
func TestScanWiresSeasonalTimezone(t *testing.T) {
	for file, pins := range map[string][]string{
		"anomaly.go": {
			"seasonalLoc := seasonalLocation(sens.SeasonalTimezone)",
			"d.fetchAllSeasonal(ctx, m, now, days, neighbor, sens.Calendar, minSamples, seasonalLoc)",
			"buildAllSeasonalQueryTZ(vexpr, tz)",
			"seasonalDayKey(loc))",
			"d.scanSubjects(ctx, now, sens, tracked, days, neighbor, redView.queries)",
		},
		"red_vm_source.go": {
			"sloc := seasonalLocation(sens.SeasonalTimezone)",
			"vmSeasonalBucketsCal(vnow, days, neighbor, sens.Calendar, sloc)",
			"v.seasonalDayKey = seasonalDayKey(sloc)",
		},
		"anomaly_explain.go": {
			"vmSeasonalBucketsCal(vnow, days, neighbor, sens.Calendar, seasonalLocation(sens.SeasonalTimezone))",
		},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pins {
			if !strings.Contains(string(b), p) {
				t.Errorf("%s: kablo yok: %q", file, p)
			}
		}
	}
}

func TestSubjectKeys(t *testing.T) {
	k := subjectKey{Kind: chstore.AnomalySubjectEndpoint, Owner: "checkout", ID: "GET /api/cart/{id}"}
	back, ok := parseSubjectKey(k.String())
	if !ok || back != k {
		t.Fatalf("gidiş-dönüş: %+v %v", back, ok)
	}
	for _, bad := range []string{"", "endpoint", "endpoint\x1fsvc", "\x1fsvc\x1fid", "endpoint\x1fsvc\x1f"} {
		if _, ok := parseSubjectKey(bad); ok {
			t.Errorf("%q kabul edildi", bad)
		}
	}
	sk := endpointStoreKey("checkout", "/a")
	if svc, route, ok := splitEndpointStoreKey(sk); !ok || svc != "checkout" || route != "/a" {
		t.Errorf("depo anahtarı: %q %q %v", svc, route, ok)
	}
	if subjectOwner(sk) != "checkout" || subjectOwner("checkout") != "checkout" {
		t.Error("subjectOwner")
	}
	if chstore.QueueSubjectID("Kafka", " orders ") != "queue:kafka@orders" || chstore.QueueSubjectID("", "x") != "" {
		t.Error("QueueSubjectID")
	}
}

func TestPickTopSubjects(t *testing.T) {
	vols := []subjectVolume{
		{"b", "r1", 10}, {"a", "r3", 5}, {"a", "r1", 50}, {"a", "r2", 50}, {"a", "r4", 1},
		{"a", "zero", 0}, {"a", "nan", math.NaN()}, {"a", "", 99}, {"b", "r2", 30},
	}
	got := pickTopSubjects(vols, 2)
	want := []string{"a/r1", "a/r2", "b/r2", "b/r1"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].Group+"/"+got[i].ID != w {
			t.Errorf("%d: %s/%s, want %s", i, got[i].Group, got[i].ID, w)
		}
	}
	if pickTopSubjects(vols, 0) != nil {
		t.Error("topN 0 → nil")
	}
	if n := len(pickTopSubjects(vols, 100)); n != 6 {
		t.Errorf("geçerli aday sayısı %d, want 6 (sıfır/NaN/boş kimlik düşer)", n)
	}
}

func TestPlanSubjectBudget(t *testing.T) {
	on := chstore.AnomalySubjectKindConfig{Enabled: true, TopN: 20, MaxSeriesPerTick: 100}
	cases := []struct {
		name   string
		req    subjectKindReq
		vmLeft int
		skip   bool
		reason string
		budget int
	}{
		{"kapalı", subjectKindReq{Kind: "db", Cfg: chstore.AnomalySubjectKindConfig{TopN: 20, MaxSeriesPerTick: 100}, Series: 500}, 24, false, "", 0},
		{"seri tavanı", subjectKindReq{Kind: "db", Cfg: on, Series: 101}, 24, true, subjectSkipSeriesCap, 0},
		{"tavanda", subjectKindReq{Kind: "db", Cfg: on, Series: 100}, 0, false, "", 0},
		{"VM payı yok", subjectKindReq{Kind: "endpoint", Cfg: on, Series: -1, VM: true, VMNeedMin: 2}, 1, true, subjectSkipVMBudget, 0},
		{"VM payı var", subjectKindReq{Kind: "endpoint", Cfg: on, Series: -1, VM: true, VMNeedMin: 2}, 7, false, "", 7},
	}
	for _, c := range cases {
		p := planSubjectBudget([]subjectKindReq{c.req}, c.vmLeft)[0]
		if p.Skipped != c.skip || p.Reason != c.reason || p.VMBudget != c.budget {
			t.Errorf("%s: %+v", c.name, p)
		}
	}
	// Varsayılan ayarda üç tür de kapalı.
	for _, r := range subjectKindsOrdered(chstore.DefaultAnomalySubjects()) {
		if p := planSubjectBudget([]subjectKindReq{r}, 24)[0]; p.Enabled || p.Skipped {
			t.Errorf("%s varsayılanda açık/atlanmış: %+v", r.Kind, p)
		}
	}
}

func TestVMEndpointExprShape(t *testing.T) {
	f := vmREDFamilies[0]
	base := vmQueryScope{SvcLabel: "service_name", StatusLabels: []string{"http_response_status_code"}}
	// Servis kipi: rota alanları boşken ifade eskisi (seçim/ayar yok).
	for k := vmREDKind(0); k < vmKindN; k++ {
		e, _ := vmREDExpr(f, k, base, 300)
		if strings.Contains(e, "and on") || strings.Contains(e, "http_route") {
			t.Errorf("servis kipi ifadesi rota anıyor: %s", e)
		}
	}
	sc := base
	sc.RouteLabel = "http_route"
	sel := vmEndpointSelectExpr(f, sc, 20, 1_700_000_000)
	for _, w := range []string{"topk by (service_name) (20, sum by (service_name, http_route)", `http_route!=""`, "[1h] @ 1700000000"} {
		if !strings.Contains(sel, w) {
			t.Errorf("seçim ifadesinde yok %q: %s", w, sel)
		}
	}
	sc.Select = sel
	for k := vmREDKind(0); k < vmKindN; k++ {
		e, ok := vmREDExpr(f, k, sc, 86400)
		if !ok {
			t.Fatalf("%v ölçülemedi", k)
		}
		if !strings.Contains(e, "by (service_name, http_route") || !strings.HasSuffix(e, "and on (service_name, http_route) ("+sel+")") {
			t.Errorf("%v endpoint ifadesi: %s", k, e)
		}
	}
	key, off, ok := vmParseGroupKeyRoute([]string{`cm_off="600"`, `http_route="/a"`, `service_name="svc"`}, "service_name", "http_route")
	if !ok || key != endpointStoreKey("svc", "/a") || off != 600 {
		t.Errorf("grup anahtarı: %q %d %v", key, off, ok)
	}
	if _, _, ok := vmParseGroupKeyRoute([]string{`service_name="svc"`}, "service_name", "http_route"); ok {
		t.Error("rotasız seri kabul edildi")
	}
}

func TestSubjectCHQueries(t *testing.T) {
	for _, src := range subjectCHSources {
		v, _ := metricValueExpr("p99_ms")
		for name, q := range map[string]string{
			"seçim":     buildSubjectSelectQuery(src),
			"ardışık":   buildSubjectBucketsQuery(src, v, 3),
			"mevsimsel": buildSubjectSeasonalQuery(src, v, "Europe/Istanbul", 3),
		} {
			if !strings.Contains(q, "FROM "+src.Table) || strings.Contains(q, "FROM spans") {
				t.Errorf("%s/%s: MV'den okumuyor", src.Kind, name)
			}
			if !strings.Contains(q, "LIMIT ") || !strings.Contains(q, "max_execution_time") || !strings.Contains(q, "time_bucket >= ?") || !strings.Contains(q, "time_bucket < ?") {
				t.Errorf("%s/%s: sınırsız sorgu:\n%s", src.Kind, name, q)
			}
			if strings.Contains(q, "IN (SELECT") || strings.Contains(q, "distributed_group_by_no_merge") {
				t.Errorf("%s/%s: alt sorgu / GROUP BY itmesi", src.Kind, name)
			}
		}
		b := buildSubjectBucketsQuery(src, v, 3)
		if strings.Count(b, "(?, ?)") != 3 || !strings.Contains(b, "LIMIT 1000 BY "+src.SysCol+", "+src.IDCol) {
			t.Errorf("%s ardışık demet/LIMIT BY:\n%s", src.Kind, b)
		}
		s := buildSubjectSeasonalQuery(src, v, "Europe/Istanbul", 2)
		if strings.Count(s, "'Europe/Istanbul'") != 6 || strings.Contains(s, "'UTC'") || strings.Count(s, "(?, ?)") != 2 {
			t.Errorf("%s mevsimsel dilim/demet:\n%s", src.Kind, s)
		}
		// Bağ sayısı = yer tutucu sayısı.
		if got, want := strings.Count(s, "?"), 6+2*2; got != want {
			t.Errorf("%s mevsimsel %d yer tutucu, want %d", src.Kind, got, want)
		}
		if got, want := strings.Count(b, "?"), 2+2*3; got != want {
			t.Errorf("%s ardışık %d yer tutucu, want %d", src.Kind, got, want)
		}
	}
	if args := subjectPairArgs([]subjectPair{{"oracle", "db1"}, {"kafka", "t"}}); len(args) != 4 || args[2] != "kafka" {
		t.Errorf("demet bağları: %v", args)
	}
}

// TestScanSubjectsOffIsNoop — özneler kapalıyken (varsayılan) okuma yok:
// mağazasız Detector'da çağrı panik etmez, nil döner.
func TestScanSubjectsOffIsNoop(t *testing.T) {
	d := &Detector{}
	if got := d.scanSubjects(context.Background(), time.Now(), chstore.DefaultAnomalySensitivity(), []string{"error_rate"}, 14, 3, 0); got != nil {
		t.Fatalf("kapalıyken tik %v", got)
	}
}

// ── endpoint kaynağı uçtan uca (sahte VM) ──────────────────────────────

type fakeEndpointVM struct {
	routes  map[string][]string // servis → rotalar (seçim)
	queries atomic.Int64
	sawSel  atomic.Int64 // aralık sorgularında seçim süzgeci görüldü
}

func (f *fakeEndpointVM) Configured() bool                  { return true }
func (f *fakeEndpointVM) MetricLabelMap() vmetrics.LabelMap { return vmetrics.LabelMap{} }
func (f *fakeEndpointVM) MetricLabelNamesIn(_ context.Context, metric string, _, _ time.Time) ([]string, bool, error) {
	if metric == vmREDFamilies[0].Probe {
		return []string{"service_name", "http_route", "http_response_status_code", "le"}, false, nil
	}
	return nil, false, nil
}
func (f *fakeEndpointVM) MetricLabelValuesIn(context.Context, string, string, time.Time, time.Time, int) ([]string, bool, error) {
	return nil, false, nil
}
func (f *fakeEndpointVM) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	f.queries.Add(1)
	var out []chstore.SpanMetricSeries
	for svc, rs := range f.routes {
		if strings.Contains(q, "topk") {
			for _, r := range rs {
				out = append(out, chstore.SpanMetricSeries{GroupKey: []string{`http_route="` + r + `"`, `service_name="` + svc + `"`}, Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: 10}}})
			}
			continue
		}
		out = append(out, chstore.SpanMetricSeries{GroupKey: []string{`service_name="` + svc + `"`}, Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: 1}}})
	}
	return out, nil
}
func (f *fakeEndpointVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.queries.Add(1)
	if strings.Contains(q, "and on (service_name, http_route) (topk") {
		f.sawSel.Add(1)
	}
	val := 3000.0
	switch {
	case strings.Contains(q, "histogram_quantile"):
		val = 120
	case strings.Contains(q, `=~"5.."`):
		val = 30
	}
	var out []chstore.SpanMetricSeries
	for _, m := range offRe.FindAllStringSubmatch(q, -1) {
		for svc, rs := range f.routes {
			for _, r := range rs {
				s := chstore.SpanMetricSeries{GroupKey: []string{`cm_off="` + m[1] + `"`, `http_route="` + r + `"`, `service_name="` + svc + `"`}}
				for ts := from.Unix() + int64(step); ts <= to.Unix(); ts += int64(step) {
					s.Points = append(s.Points, chstore.SpanMetricPoint{Time: (ts - int64(step)) * int64(time.Second), Value: val})
				}
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func TestEndpointSourceEndToEnd(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC)
	sens := chstore.DefaultAnomalySensitivity()
	f := &fakeEndpointVM{routes: map[string][]string{"checkout": {"/cart", "/pay"}}}
	src := newVMEndpointSource()
	src.wire.Store(&vmREDBox{vm: f})
	src.topN, src.maxSeries, src.queryCap = 20, 100, 24
	prevObs := redObs.Load()
	var v *vmREDView
	for i := 0; i < 400; i++ {
		v = src.prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
		if v.reason != "" {
			t.Fatalf("gerekçe: %s", v.reason)
		}
		if v.queries > 24 {
			t.Fatalf("tik %d sorgu, tavan 24", v.queries)
		}
		if v.pending == 0 {
			break
		}
	}
	if v.pending != 0 || v.selected != 2 {
		t.Fatalf("ısınmadı / seçim: pending %d selected %d", v.pending, v.selected)
	}
	if f.sawSel.Load() == 0 {
		t.Error("aralık sorguları seçimle süzülmüyor")
	}
	keys := v.vmServices()
	if len(keys) != 2 || keys[0] != endpointStoreKey("checkout", "/cart") {
		t.Fatalf("özne anahtarları: %q", keys)
	}
	for _, m := range []string{"error_rate", "p99_ms", "request_rate"} {
		s, u := v.seriesFor(keys[0], m, sens.DwellBuckets)
		if !u.VM || len(s.Values) < minSamples+sens.DwellBuckets || len(s.Seasonal) == 0 {
			t.Errorf("%s: VM %v seri %d mevsimsel %d (%s)", m, u.VM, len(s.Values), len(s.Seasonal), u.Reason)
		}
	}
	// Servis yolunun ölçümü endpoint kipinden yazılmaz.
	if redObs.Load() != prevObs {
		t.Error("endpoint kaynağı servis ölçümünü (REDSourceObservability) ezdi")
	}

	// Seri tavanı: iki rota > 1 → tür atlanır, aralık okuması yok.
	f2 := &fakeEndpointVM{routes: map[string][]string{"checkout": {"/cart", "/pay"}}}
	src2 := newVMEndpointSource()
	src2.wire.Store(&vmREDBox{vm: f2})
	src2.topN, src2.maxSeries, src2.queryCap = 20, 1, 24
	v2 := src2.prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
	if v2.reason != subjectSkipSeriesCap || f2.sawSel.Load() != 0 {
		t.Errorf("tavan aşımı: gerekçe %q, aralık sorgusu %d", v2.reason, f2.sawSel.Load())
	}

	// Seçim değişince (yeni rota) aile kovaları atılır ve yeni rota da okunur.
	f.routes["checkout"] = []string{"/cart", "/pay", "/refund"}
	src.sel.at = time.Time{} // TTL doldu
	before := src.sel.anchor
	v = src.prepare(context.Background(), now.Add(10*time.Minute), sens, seasonalDays, seasonalNeighborBuckets)
	if v.selected != 3 || src.sel.anchor == before {
		t.Errorf("seçim yenilenmedi: %d, çapa %d→%d", v.selected, before, src.sel.anchor)
	}
	// Seçim aynı kalırsa çapa korunur (sorgu metni ve bellek değişmez).
	src.sel.at = time.Time{}
	anchor := src.sel.anchor
	src.prepare(context.Background(), now.Add(20*time.Minute), sens, seasonalDays, seasonalNeighborBuckets)
	if src.sel.anchor != anchor {
		t.Error("küme aynıyken çapa değişti (gereksiz bellek atımı)")
	}
}

// TestEndpointSourceRequiresRouteLabel — rota etiketi yoksa tür kurulmaz.
func TestEndpointSourceRequiresRouteLabel(t *testing.T) {
	f := &fakeREDVM{labels: map[string][]string{vmREDFamilies[0].Probe: {"service_name", "http_response_status_code", "le"}},
		statusSvcs: []string{"orders-api"}, val: steadyVal, svcs: []string{"orders-api"}}
	src := newVMEndpointSource()
	src.wire.Store(&vmREDBox{vm: f})
	src.topN, src.maxSeries, src.queryCap = 20, 100, 24
	v := src.prepare(context.Background(), time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC), chstore.DefaultAnomalySensitivity(), seasonalDays, seasonalNeighborBuckets)
	if v.reason != redReasonNoSelection || src.meta[0].reason != redReasonNoRoute {
		t.Errorf("gerekçe %q / meta %q", v.reason, src.meta[0].reason)
	}
}
