package api

// problem_breakdown_core_test.go — v0.10.2388 ("Hata nerede" bölümü). SAF
// çekirdeğin sözleşmesi: hangi Problem / hangi pencere, rotaların katkıya göre
// sırası + "Diğer N" katlaması, sürüm kırılımının ne zaman YAZILMADIĞI,
// exception payları, bağımlılık satırları. Saat ve saat dilimi bağımsız
// (sabit `now`, mutlak anlar).

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var bdNow = time.Date(2026, 11, 18, 21, 15, 40, 0, time.UTC)

func bdProblem(startAgo time.Duration, resolvedAgo *time.Duration) chstore.Problem {
	p := chstore.Problem{ID: "p1", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: "error_rate", Status: "open",
		StartedAt: bdNow.Add(-startAgo).UnixNano()}
	if resolvedAgo != nil {
		ns := bdNow.Add(-*resolvedAgo).UnixNano()
		p.Status, p.ResolvedAt = "resolved", &ns
	}
	return p
}

func TestBreakdownSubject(t *testing.T) {
	base := bdProblem(time.Hour, nil)
	cases := []struct {
		name string
		mut  func(*chstore.Problem)
		want bool
	}{
		{"ML servis hata oranı", func(*chstore.Problem) {}, true},
		{"kural tabanlı hata oranı (servis)", func(p *chstore.Problem) { p.RuleID = "builtin-error-rate" }, true},
		{"gecikme Problem'i", func(p *chstore.Problem) { p.Metric = "p99_ms" }, false},
		{"servis yok", func(p *chstore.Problem) { p.Service = "" }, false},
		{"veritabanı öznesi", func(p *chstore.Problem) { p.Kind = chstore.ProblemKindDB }, false},
		{"dış kaynak öznesi", func(p *chstore.Problem) { p.Kind = chstore.ProblemKindExternal }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := base
			c.mut(&p)
			if got := breakdownSubject(p); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestBreakdownWindow(t *testing.T) {
	lag := 2 * time.Minute
	edge := time.Date(2026, 11, 18, 21, 13, 0, 0, time.UTC) // now − gecikme, dakikaya kırpılı
	d := func(v time.Duration) *time.Duration { return &v }
	cases := []struct {
		name     string
		p        chstore.Problem
		ok, open bool
		from, to time.Time
	}{
		{"açık, 12 dk önce başladı: başlangıçtan beri", bdProblem(12*time.Minute+40*time.Second, nil), true, true,
			time.Date(2026, 11, 18, 21, 3, 0, 0, time.UTC), edge},
		{"açık, yeni başladı: en az 5 dk", bdProblem(90*time.Second, nil), true, true, edge.Add(-5 * time.Minute), edge},
		{"açık, 3 saattir: son 30 dk", bdProblem(3*time.Hour, nil), true, true, edge.Add(-30 * time.Minute), edge},
		{"kapanmış, 40 dk sürdü: tüm pencere", bdProblem(100*time.Minute+40*time.Second, d(60*time.Minute+40*time.Second)), true, false,
			time.Date(2026, 11, 18, 19, 35, 0, 0, time.UTC), time.Date(2026, 11, 18, 20, 15, 0, 0, time.UTC)},
		{"kapanmış, 2 gün sürdü: son 6 sa", bdProblem(50*time.Hour, d(2*time.Hour)), true, false,
			bdNow.Add(-2 * time.Hour).Truncate(time.Minute).Add(time.Minute).Add(-6 * time.Hour), bdNow.Add(-2 * time.Hour).Truncate(time.Minute).Add(time.Minute)},
		{"az önce kapandı: bitiş VM'in yazdığı son dakikayı aşmaz", bdProblem(20*time.Minute, d(30*time.Second)), true, false,
			time.Date(2026, 11, 18, 20, 55, 0, 0, time.UTC), edge},
		{"8 gün önce kapandı: bölüm yok", bdProblem(9*24*time.Hour, d(8*24*time.Hour)), false, false, time.Time{}, time.Time{}},
		{"başlangıç yok", chstore.Problem{Service: "orders", Metric: "error_rate", Status: "open"}, false, false, time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, ok := breakdownWindow(c.p, bdNow, lag)
			if ok != c.ok {
				t.Fatalf("ok=%v want %v (%+v)", ok, c.ok, w)
			}
			if !ok {
				return
			}
			if w.Open != c.open || !w.From.Equal(c.from) || !w.To.Equal(c.to) {
				t.Fatalf("got %s → %s open=%v, want %s → %s open=%v", w.From.UTC(), w.To.UTC(), w.Open, c.from, c.to, c.open)
			}
			if span := w.To.Sub(w.From); span < breakdownMinWindow || span > breakdownClosedWindow {
				t.Fatalf("pencere %s sınır dışı", span)
			}
		})
	}
}

// thr — test eşiği: en az %1, tabanın 1,5 katı ve 1 puan üstü (etki kuralı).
func thr(base float64) float64 { return math.Max(0.01, math.Max(base*1.5, base+0.01)) }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRankBreakdownRoutes(t *testing.T) {
	// 10 dakikalık pencere; sayılar penceredeki istek / başarısız istek.
	counts := map[string]breakdownCounts{
		"POST /orders/assessment":    {Requests: 2000, Failed: 364},
		"GET /orders/{id}/relations": {Requests: 1000, Failed: 94},
		"POST /orders":               {Requests: 3000, Failed: 93},
		"GET /orders/{id}":           {Requests: 5000, Failed: 20},
		"GET /health":                {Requests: 600, Failed: 0},
		"GET /idle":                  {Requests: 0, Failed: 0}, // pencerede trafik yok: sayılmaz
	}
	base := map[string]chstore.MLRouteBaseline{
		"POST /orders/assessment":    {CountPos: 30, CountMed: 3, ErrRatios: rep(0.003, 30)},
		"GET /orders/{id}/relations": {CountPos: 30, CountMed: 2, ErrRatios: rep(0.005, 30)},
		"POST /orders":               {CountPos: 30, CountMed: 5, ErrRatios: rep(0.03, 30)},
		"GET /orders/{id}":           {CountPos: 30, CountMed: 8, ErrRatios: rep(0.004, 30)},
		"GET /health":                {CountPos: 30, CountMed: 1},
	}
	t.Run("katkıya göre sıra, ilk N, kalanı Diğer", func(t *testing.T) {
		got := rankBreakdownRoutes(counts, base, 10, 20, 3, thr)
		if len(got.Rows) != 3 || got.OtherCount != 2 || got.Other == nil {
			t.Fatalf("satır=%d diğer=%d other=%v", len(got.Rows), got.OtherCount, got.Other)
		}
		wantOrder := []string{"POST /orders/assessment", "GET /orders/{id}/relations", "POST /orders"}
		for i, r := range got.Rows {
			if r.Route != wantOrder[i] {
				t.Fatalf("sıra %d: %q, want %q", i, r.Route, wantOrder[i])
			}
		}
		top := got.Rows[0]
		if !near(top.Current, 0.182) || !near(top.FailedPerMin, 36.4) || !near(top.RequestsPerMin, 200) || top.Normal == nil || !near(*top.Normal, 0.003) || !top.Deviates {
			t.Fatalf("ilk satır %+v", top)
		}
		// %3,1 şimdi, olağanı %3: eşiğin (%4,5) altında → renk yok.
		if r := got.Rows[2]; !near(r.Current, 0.031) || r.Deviates {
			t.Fatalf("eşik altı satır sapan sayılmamalı: %+v", r)
		}
		// Diğer: (20 + 0) / (5000 + 600); olağanı trafik ağırlıklı (8·0,004 + 1·0) / 9.
		o := got.Other
		if !near(o.Current, 20.0/5600) || !near(o.FailedPerMin, 2) || !near(o.RequestsPerMin, 560) || o.Normal == nil || !near(*o.Normal, 0.032/9) || o.Deviates {
			t.Fatalf("diğer %+v (normal %v)", o, o.Normal)
		}
		if !near(got.FailedPerMin, 57.1) || !near(got.RequestsPerMin, 1160) {
			t.Fatalf("toplam %v / %v", got.FailedPerMin, got.RequestsPerMin)
		}
	})
	t.Run("N'den az başarısız rota: hatasızlar Diğer'e katlanır", func(t *testing.T) {
		got := rankBreakdownRoutes(counts, base, 10, 20, 8, thr)
		if len(got.Rows) != 4 || got.OtherCount != 1 || got.Other == nil || got.Other.FailedPerMin != 0 {
			t.Fatalf("satır=%d diğer=%d other=%+v", len(got.Rows), got.OtherCount, got.Other)
		}
	})
	t.Run("tabanı olmayan rota: normal bilinmiyor, renk yok", func(t *testing.T) {
		got := rankBreakdownRoutes(map[string]breakdownCounts{"POST /new": {Requests: 100, Failed: 50}}, nil, 5, 20, 8, thr)
		if len(got.Rows) != 1 || got.Rows[0].Normal != nil || got.Rows[0].Deviates || got.Other != nil || got.OtherCount != 0 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("Diğer'de tabanı bilinmeyen bir rota varsa Diğer'in normali yazılmaz", func(t *testing.T) {
		c := map[string]breakdownCounts{"a": {Requests: 100, Failed: 10}, "b": {Requests: 100, Failed: 0}, "c": {Requests: 100, Failed: 0}}
		b := map[string]chstore.MLRouteBaseline{"a": {CountPos: 30}, "b": {CountPos: 30, CountMed: 1}}
		got := rankBreakdownRoutes(c, b, 5, 20, 1, thr)
		if got.Other == nil || got.OtherCount != 2 || got.Other.Normal != nil {
			t.Fatalf("%+v", got.Other)
		}
	})
	t.Run("hiç başarısız istek yok: blok yok", func(t *testing.T) {
		got := rankBreakdownRoutes(map[string]breakdownCounts{"a": {Requests: 100}, "b": {Requests: 50}}, base, 5, 20, 8, thr)
		if len(got.Rows) != 0 || got.Other != nil {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("eşit katkıda sıra kararlı (trafik, sonra ad)", func(t *testing.T) {
		c := map[string]breakdownCounts{"b": {Requests: 10, Failed: 5}, "a": {Requests: 10, Failed: 5}, "c": {Requests: 20, Failed: 5}}
		got := rankBreakdownRoutes(c, nil, 5, 20, 8, thr)
		if names := []string{got.Rows[0].Route, got.Rows[1].Route, got.Rows[2].Route}; !reflect.DeepEqual(names, []string{"c", "a", "b"}) {
			t.Fatalf("sıra %v", names)
		}
	})
	t.Run("bozuk sayım (başarısız > istek, NaN) satır üretmez", func(t *testing.T) {
		c := map[string]breakdownCounts{"x": {Requests: 10, Failed: 30}, "y": {Requests: math.NaN(), Failed: 1}}
		got := rankBreakdownRoutes(c, nil, 5, 20, 8, thr)
		if len(got.Rows) != 1 || got.Rows[0].Current != 1 {
			t.Fatalf("%+v", got.Rows)
		}
	})
}

func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestBreakdownVersionRows(t *testing.T) {
	t.Run("iki sürüm: oranlar aynı örneklenmemiş seriden", func(t *testing.T) {
		rows, more := breakdownVersionRows(map[string]breakdownCounts{"v2.14.1": {Requests: 6000, Failed: 426}, "v2.14.0": {Requests: 2000, Failed: 6}}, 10, 4, thr)
		if len(rows) != 2 || more != 0 {
			t.Fatalf("%+v more=%d", rows, more)
		}
		if rows[0].Version != "v2.14.1" || !near(rows[0].Current, 0.071) || !near(rows[0].FailedPerMin, 42.6) || !rows[0].Deviates {
			t.Fatalf("yeni sürüm %+v", rows[0])
		}
		if rows[1].Version != "v2.14.0" || !near(rows[1].Current, 0.003) || rows[1].Deviates {
			t.Fatalf("eski sürüm %+v", rows[1])
		}
	})
	t.Run("tek sürüm: kırılım yok", func(t *testing.T) {
		if rows, _ := breakdownVersionRows(map[string]breakdownCounts{"v1": {Requests: 100, Failed: 9}}, 10, 4, thr); rows != nil {
			t.Fatalf("%+v", rows)
		}
	})
	t.Run("etiketsiz trafik varsa kırılım tam değildir: yazılmaz", func(t *testing.T) {
		in := map[string]breakdownCounts{"v1": {Requests: 100, Failed: 9}, "v2": {Requests: 100}, "": {Requests: 40, Failed: 3}}
		if rows, _ := breakdownVersionRows(in, 10, 4, thr); rows != nil {
			t.Fatalf("%+v", rows)
		}
	})
	t.Run("trafiksiz sürüm sayılmaz", func(t *testing.T) {
		in := map[string]breakdownCounts{"v1": {Requests: 100, Failed: 9}, "v0": {Requests: 0}}
		if rows, _ := breakdownVersionRows(in, 10, 4, thr); rows != nil {
			t.Fatalf("%+v", rows)
		}
	})
	t.Run("tavan: trafiğe göre ilk N, kalanı sayılır", func(t *testing.T) {
		in := map[string]breakdownCounts{"a": {Requests: 50}, "b": {Requests: 40}, "c": {Requests: 30}, "d": {Requests: 20}}
		rows, more := breakdownVersionRows(in, 10, 2, thr)
		if len(rows) != 2 || more != 2 || rows[0].Version != "a" || rows[1].Version != "b" {
			t.Fatalf("%+v more=%d", rows, more)
		}
	})
}

func TestBuildBreakdownExceptions(t *testing.T) {
	types := []chstore.ExceptionTypeCount{{Type: "AppException", Count: 940}, {Type: "TimeoutException", Count: 50}, {Type: "IOException", Count: 7}, {Type: "<unknown>", Count: 3}}
	groups := map[string]chstore.ExceptionTypeGroup{
		"AppException": {Type: "AppException", Fingerprint: "fp-app", Message: "Kayıt bulunamadı", Groups: 2},
	}
	t.Run("pay sayıdan, ilk N, kalanı Diğer", func(t *testing.T) {
		got := buildBreakdownExceptions(types, false, groups, 2)
		if got == nil || len(got.Rows) != 2 || got.Total != 1000 || got.OtherTypes != 2 || got.OtherCount != 10 {
			t.Fatalf("%+v", got)
		}
		r := got.Rows[0]
		if r.Type != "AppException" || r.Count != 940 || r.Share == nil || !near(*r.Share, 0.94) || r.Message != "Kayıt bulunamadı" || r.Fingerprint != "fp-app" || r.Groups != 2 {
			t.Fatalf("ilk satır %+v", r)
		}
		if r := got.Rows[1]; r.Fingerprint != "" || r.Message != "" || r.Share == nil || !near(*r.Share, 0.05) {
			t.Fatalf("grubu olmayan tür bağlantısız kalmalı: %+v", r)
		}
		if got.OtherShare == nil || !near(*got.OtherShare, 0.01) {
			t.Fatalf("diğer payı %v", got.OtherShare)
		}
	})
	t.Run("tür tavanı dolduysa toplam bilinmez: pay yazılmaz, sayı kalır", func(t *testing.T) {
		got := buildBreakdownExceptions(types, true, groups, 2)
		if got == nil || got.Total != 0 || got.Rows[0].Share != nil || got.OtherShare != nil || got.Rows[0].Count != 940 || !got.Capped {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("kayıt yok: blok yok", func(t *testing.T) {
		if got := buildBreakdownExceptions(nil, false, nil, 5); got != nil {
			t.Fatalf("%+v", got)
		}
		if got := buildBreakdownExceptions([]chstore.ExceptionTypeCount{{Type: "X", Count: 0}}, false, nil, 5); got != nil {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("uzun mesaj kırpılır", func(t *testing.T) {
		long := make([]rune, 600)
		for i := range long {
			long[i] = 'ş'
		}
		g := map[string]chstore.ExceptionTypeGroup{"X": {Type: "X", Fingerprint: "f", Message: string(long)}}
		got := buildBreakdownExceptions([]chstore.ExceptionTypeCount{{Type: "X", Count: 1}}, false, g, 5)
		if n := len([]rune(got.Rows[0].Message)); n != breakdownMessageMax {
			t.Fatalf("mesaj %d rune", n)
		}
	})
}

func TestBuildBreakdownDependencies(t *testing.T) {
	now := bdNow.UnixNano()
	ago := func(d time.Duration) int64 { return bdNow.Add(-d).UnixNano() }
	evs := []chstore.DependencyEvent{ // okuma sırası: en yeni önce
		{ID: "e-lat", Detector: chstore.MLDetDependencyLatency, Code: "http|customer-rel:8443", WindowSec: 300, StartedAt: ago(20 * time.Minute), LastSeen: ago(2 * time.Minute), Baseline: 40, Observed: 900},
		{ID: "e-err", Detector: chstore.MLDetDependencyErrorRate, Code: "db|orders_db", WindowSec: 300, StartedAt: ago(14 * time.Minute), LastSeen: ago(3 * time.Minute), Baseline: 0.002, Observed: 0.31},
		{ID: "e-err-old", Detector: chstore.MLDetDependencyErrorRate, Code: "db|orders_db", WindowSec: 300, StartedAt: ago(3 * time.Hour), LastSeen: ago(2 * time.Hour), Baseline: 0.002, Observed: 0.1},
		{ID: "e-closed", Detector: chstore.MLDetDependencyErrorRate, Code: "messaging|checkout.events", Stage: chstore.MLStageClosed, WindowSec: 300, StartedAt: ago(30 * time.Minute), LastSeen: ago(1 * time.Minute), Baseline: 0.01, Observed: 0.2},
		{ID: "e-bad", Detector: chstore.MLDetDependencyErrorRate, Code: "bozuk-kod", StartedAt: ago(time.Minute), LastSeen: now},
		{ID: "e-flat", Detector: chstore.MLDetDependencyErrorRate, Code: "db|flat", StartedAt: ago(time.Minute), LastSeen: now, Baseline: 0.2, Observed: 0.1},
	}
	got := buildBreakdownDependencies(evs, now, 5)
	if got == nil || len(got.Rows) != 3 || got.More != 0 {
		t.Fatalf("%+v", got)
	}
	// Hata oranı satırları önce (erken başlayan önce), gecikme sonra.
	ids := []string{got.Rows[0].EventID, got.Rows[1].EventID, got.Rows[2].EventID}
	if !reflect.DeepEqual(ids, []string{"e-closed", "e-err", "e-lat"}) {
		t.Fatalf("sıra %v", ids)
	}
	if r := got.Rows[1]; r.Family != "db" || r.Target != "orders_db" || r.Measure != "error_rate" || !r.Live || r.Baseline != 0.002 || r.Observed != 0.31 {
		t.Fatalf("hata oranı satırı %+v", r)
	}
	if r := got.Rows[0]; r.Live {
		t.Fatalf("kapalı damgalı olay canlı sayılmamalı: %+v", r)
	}
	if r := got.Rows[2]; r.Measure != "latency" || r.Family != "http" || r.Target != "customer-rel:8443" {
		t.Fatalf("gecikme satırı %+v", r)
	}
	if capped := buildBreakdownDependencies(evs, now, 2); len(capped.Rows) != 2 || capped.More != 1 {
		t.Fatalf("tavan: %+v", capped)
	}
	if none := buildBreakdownDependencies(nil, now, 5); none != nil {
		t.Fatalf("olay yok: blok yok, got %+v", none)
	}
}

func TestFoldBreakdownSeries(t *testing.T) {
	gk := func(kv ...string) []string { return kv }
	pt := func(v float64) []chstore.SpanMetricPoint { return []chstore.SpanMetricPoint{{Value: v}} }
	count := []chstore.SpanMetricSeries{
		{GroupKey: gk(`http_route="/orders"`, `service_version="v2"`), Points: pt(600)},
		{GroupKey: gk(`http_route="/orders"`, `service_version="v1"`), Points: pt(400)},
		{GroupKey: gk(`http_route="/orders/{id}"`, `service_version="v1"`), Points: pt(100)},
		{GroupKey: gk(`service_version="v1"`), Points: pt(10)},      // rota etiketi yok
		{GroupKey: gk(`http_route="/nan"`), Points: pt(math.NaN())}, // sonlu değil: atılır
		{GroupKey: gk(`http_route="/empty"`)},                       // nokta yok: atılır
	}
	errs := []chstore.SpanMetricSeries{
		{GroupKey: gk(`http_route="/orders"`, `service_version="v2"`), Points: pt(60)},
		{GroupKey: gk(`http_route="/ghost"`, `service_version="v2"`), Points: pt(5)}, // sayımı olmayan hata satırı
	}
	routes, versions := map[string]breakdownCounts{}, map[string]breakdownCounts{}
	foldBreakdownSeries(routes, versions, count, errs, "http_route", "service_version")
	wantRoutes := map[string]breakdownCounts{"/orders": {Requests: 1000, Failed: 60}, "/orders/{id}": {Requests: 100}, "": {Requests: 10}, "/ghost": {Failed: 5}}
	wantVers := map[string]breakdownCounts{"v2": {Requests: 600, Failed: 65}, "v1": {Requests: 510}}
	if !reflect.DeepEqual(routes, wantRoutes) {
		t.Fatalf("rotalar %+v", routes)
	}
	if !reflect.DeepEqual(versions, wantVers) {
		t.Fatalf("sürümler %+v", versions)
	}
	// Sürüm etiketi yoksa sürüm sayımı HİÇ tutulmaz (boş anahtar "etiketsiz trafik" demektir).
	r2, v2 := map[string]breakdownCounts{}, map[string]breakdownCounts{}
	foldBreakdownSeries(r2, v2, count[:1], nil, "http_route", "")
	if len(v2) != 0 || r2["/orders"].Requests != 600 {
		t.Fatalf("sürüm etiketi yok: %+v %+v", r2, v2)
	}
}

func TestBreakdownVMQueries(t *testing.T) {
	http, _ := redFamilyByID(redFamilyHTTP)
	c, e := breakdownVMQueries(http, "service_name", "orders", "http_response_status_code", []string{"http_route", "service_version"}, 720)
	for _, q := range []string{c, e} {
		for _, want := range []string{"sum by (http_route, service_version) (increase(", `service_name="orders"`, "[720s]", "http_server_request_duration_seconds_count"} {
			if !strings.Contains(q, want) {
				t.Fatalf("sorguda %q yok: %s", want, q)
			}
		}
		for _, deny := range []string{"topk", "_bucket", "rate("} {
			if strings.Contains(q, deny) {
				t.Fatalf("sorgu %q içermemeli: %s", deny, q)
			}
		}
	}
	// Noktalı OTel adı (HTTP ailesinin pini `http.server.request.duration`) TEK
	// seçicinin __name__ alternasyonunda, kaçışlı ve bir kez durur. Ad, eşleştirici
	// enjekte eden yardımcılardan (thanos.WithEqualMatcher — noktada bölüp her
	// parçaya `{…}` ekliyordu, v0.10.2386 düzeltmesi) GEÇMEZ: ifade her iki
	// sürümde de geçerlidir.
	for _, q := range []string{c, e} {
		if strings.Count(q, `http\\.server\\.request\\.duration_count`) != 1 {
			t.Fatalf("noktalı ad sorguda tam bir kez (kaçışlı) durmalı: %s", q)
		}
		if strings.Contains(q, "}.") || strings.Contains(q, "http{") || strings.Contains(q, "server{") || strings.Count(q, "{") != 1 || strings.Count(q, "}") != 1 {
			t.Fatalf("seçici bölünmüş: %s", q)
		}
		if !strings.HasPrefix(q, `sum by (http_route, service_version) (increase({__name__=~"^(`) || !strings.HasSuffix(q, "[720s]))") {
			t.Fatalf("ifade biçimi: %s", q)
		}
	}
	if strings.Contains(c, "5..") || !strings.Contains(e, `http_response_status_code=~"5.."`) {
		t.Fatalf("hata süzgeci yalnız hata sorgusunda olmalı:\n%s\n%s", c, e)
	}
	grpc, _ := redFamilyByID(redFamilyGRPCLegacy)
	_, ge := breakdownVMQueries(grpc, "service_name", `ord"ers`, "rpc_grpc_status_code", []string{"rpc_method"}, 300)
	if !strings.Contains(ge, `rpc_grpc_status_code=~"`+grpcServerFaultMatch+`"`) || !strings.Contains(ge, "rpc_server_duration_milliseconds_count") || strings.Contains(ge, `"ord"ers"`) {
		t.Fatalf("gRPC hata sorgusu (kaçışlı servis adı): %s", ge)
	}
}

func TestProblemBreakdownKey_HashesAllInputs(t *testing.T) {
	a := problemBreakdownKey("3f2a")
	if a == problemBreakdownKey("3f2b") || a == problemBreakdownKey("P-3f2a") || a == problemBreakdownKey("") {
		t.Fatal("farklı kimlik aynı anahtarı üretmemeli")
	}
	if a != problemBreakdownKey("3f2a") {
		t.Fatal("anahtar kararlı değil")
	}
	// Tek girdi kimliktir; ayırıcı çakışması başka bir yüzeyin anahtarına taşmaz.
	if problemBreakdownKey("x") == problemEventsKey("x") || problemBreakdownKey("x") == problemGraphKey("x") {
		t.Fatal("kardeş uçlarla anahtar çakışıyor")
	}
}
