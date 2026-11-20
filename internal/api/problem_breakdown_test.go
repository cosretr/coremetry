package api

// problem_breakdown_test.go — v0.10.2388: GET /api/problems/{id}/breakdown.
// Sözleşme: her blok kendi başına düşer (öteki bloklar çizilir, yanıt 200);
// verisi olmayan blok gövdede YOKTUR; bölümün konusu olmayan Problem'de hiçbir
// kaynak okunmaz; viewer okur. VM ve ClickHouse sahte; saat sabit.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

type fakeBreakdownStore struct {
	mu          sync.Mutex
	p           *chstore.Problem
	pErr        error
	gotID       string
	gotDisplay  string
	base        map[string]chstore.MLRouteBaseline
	baseErr     error
	gotBase     chstore.MLRouteBaselineRequest
	types       []chstore.ExceptionTypeCount
	typesErr    error
	gotExFrom   time.Time
	gotExTo     time.Time
	groups      map[string]chstore.ExceptionTypeGroup
	groupsErr   error
	gotGroupTys []string
	deps        []chstore.DependencyEvent
	depsErr     error
	gotDepFrom  time.Time
	gotDepTo    time.Time
	reads       int
}

func (f *fakeBreakdownStore) GetProblem(_ context.Context, id string) (*chstore.Problem, error) {
	f.gotID = id
	return f.p, f.pErr
}
func (f *fakeBreakdownStore) GetProblemByDisplayID(_ context.Context, id string) (*chstore.Problem, error) {
	f.gotDisplay = id
	return f.p, f.pErr
}
func (f *fakeBreakdownStore) GetMLConfig(context.Context) chstore.MLConfig {
	return chstore.DefaultMLConfig()
}
func (f *fakeBreakdownStore) AnomalySensitivityForDetectors() chstore.AnomalySensitivityConfig {
	return chstore.DefaultAnomalySensitivity()
}
func (f *fakeBreakdownStore) MLRouteErrorBaseline(_ context.Context, req chstore.MLRouteBaselineRequest) (map[string]chstore.MLRouteBaseline, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.gotBase = req
	return f.base, false, f.baseErr
}
func (f *fakeBreakdownStore) ServiceErrorExceptionTypes(_ context.Context, _ string, from, to time.Time) ([]chstore.ExceptionTypeCount, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.gotExFrom, f.gotExTo = from, to
	return f.types, false, f.typesErr
}
func (f *fakeBreakdownStore) NewestExceptionGroupsByType(_ context.Context, _ string, types []string, _ time.Time) (map[string]chstore.ExceptionTypeGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.gotGroupTys = types
	return f.groups, f.groupsErr
}
func (f *fakeBreakdownStore) ServiceDependencyEvents(_ context.Context, _ string, from, to time.Time) ([]chstore.DependencyEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.gotDepFrom, f.gotDepTo = from, to
	return f.deps, f.depsErr
}

type fakeBreakdownVM struct {
	mu       sync.Mutex
	labels   map[string][]string // metrik adı (probe) → etiketler
	labelErr error
	count    []chstore.SpanMetricSeries
	errs     []chstore.SpanMetricSeries
	queryErr error
	queries  []string
	at       []time.Time
}

func (f *fakeBreakdownVM) MetricLabelNamesForServiceIn(_ context.Context, metric, _, _ string, _, _ time.Time) ([]string, bool, error) {
	return f.labels[metric], false, f.labelErr
}
func (f *fakeBreakdownVM) MetricLabelMap() vmetrics.LabelMap { return vmetrics.LabelMap{} }
func (f *fakeBreakdownVM) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.at = append(f.at, at)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if strings.Contains(q, "status_code=~") { // sunucu-hatası süzgeci = başarısız istek sorgusu
		return f.errs, nil
	}
	return f.count, nil
}

// bdSeries — VM anlık satırı (rota [, sürüm] → değer).
func bdSeries(route, version string, v float64) chstore.SpanMetricSeries {
	gk := []string{`http_route="` + route + `"`}
	if version != "" {
		gk = append(gk, `service_version="`+version+`"`)
	}
	return chstore.SpanMetricSeries{GroupKey: gk, Points: []chstore.SpanMetricPoint{{Value: v}}}
}

// bdFixture — "her şey var" sahnesi: orders servisi, 12 dk açık Problem, HTTP
// ailesi sürüm etiketli, iki sürüm, iki exception türü, bir bağımlılık olayı.
func bdFixture() (*fakeBreakdownStore, *fakeBreakdownVM) {
	p := bdProblem(12*time.Minute+40*time.Second, nil)
	ago := func(d time.Duration) int64 { return bdNow.Add(-d).UnixNano() }
	st := &fakeBreakdownStore{
		p: &p,
		base: map[string]chstore.MLRouteBaseline{
			"/orders/assessment": {CountPos: 40, CountMed: 3, ErrRatios: rep(0.003, 40)},
			"/orders":            {CountPos: 40, CountMed: 5, ErrRatios: rep(0.004, 40)},
			"/health":            {CountPos: 40, CountMed: 1},
		},
		types:  []chstore.ExceptionTypeCount{{Type: "AppException", Count: 94}, {Type: "TimeoutException", Count: 6}},
		groups: map[string]chstore.ExceptionTypeGroup{"AppException": {Type: "AppException", Fingerprint: "fp-app", Message: "Kayıt bulunamadı", Groups: 1}},
		deps: []chstore.DependencyEvent{{ID: "ev-dep", Detector: chstore.MLDetDependencyErrorRate, Code: "db|customer-rel", WindowSec: 300,
			StartedAt: ago(14 * time.Minute), LastSeen: ago(3 * time.Minute), Baseline: 0.002, Observed: 0.31}},
	}
	vm := &fakeBreakdownVM{
		labels: map[string][]string{mcHTTPMetric: {"service_name", "http_route", "http_response_status_code", "service_version"}},
		count: []chstore.SpanMetricSeries{
			bdSeries("/orders/assessment", "v2.14.1", 900), bdSeries("/orders/assessment", "v2.14.0", 100),
			bdSeries("/orders", "v2.14.1", 1500), bdSeries("/orders", "v2.14.0", 500), bdSeries("/health", "v2.14.1", 100),
		},
		errs: []chstore.SpanMetricSeries{bdSeries("/orders/assessment", "v2.14.1", 180), bdSeries("/orders", "v2.14.1", 20), bdSeries("/orders", "v2.14.0", 2)},
	}
	return st, vm
}

// bdCall — sahteleri takıp ucu çağırır (rol: viewer; route defterinden).
func bdCall(t *testing.T, st *fakeBreakdownStore, vm *fakeBreakdownVM, id string) (*httptest.ResponseRecorder, problemBreakdownResponse) {
	t.Helper()
	origS, origV, origN := problemBreakdownStoreOf, problemBreakdownVMOf, problemBreakdownNow
	t.Cleanup(func() { problemBreakdownStoreOf, problemBreakdownVMOf, problemBreakdownNow = origS, origV, origN })
	problemBreakdownStoreOf = func(*Server) problemBreakdownStore { return st }
	problemBreakdownVMOf = func(*Server) (problemBreakdownVM, bool) {
		if vm == nil {
			return nil, false
		}
		return vm, true
	}
	problemBreakdownNow = func() time.Time { return bdNow }
	srv := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	mux := http.NewServeMux()
	srv.registerProblemBreakdownRoutes(mux)
	w := rulesDo(mux, "GET", "/api/problems/"+id+"/breakdown", "", auth.RoleViewer, "")
	var out problemBreakdownResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("gövde çözülemedi: %v\n%s", err, w.Body.String())
		}
	}
	return w, out
}

func TestGetProblemBreakdown_AllBlocks(t *testing.T) {
	st, vm := bdFixture()
	w, out := bdCall(t, st, vm, "p1")
	if w.Code != 200 {
		t.Fatalf("viewer okuyabilmeli: code=%d body=%s", w.Code, w.Body.String())
	}
	if !out.Applicable || out.Service != "orders" || !out.Open || out.ProblemID != "p1" || st.gotID != "p1" {
		t.Fatalf("zarf %+v", out)
	}
	wantFrom, wantTo := time.Date(2026, 11, 18, 21, 3, 0, 0, time.UTC), time.Date(2026, 11, 18, 21, 13, 0, 0, time.UTC)
	if out.WindowFrom != wantFrom.UnixNano() || out.WindowTo != wantTo.UnixNano() {
		t.Fatalf("pencere %d → %d", out.WindowFrom, out.WindowTo)
	}
	// VM: tek aile (HTTP) → tam iki anlık sorgu, pencere bitişinde, pencere kadar.
	if len(vm.queries) != 2 {
		t.Fatalf("VM sorgusu %d (2 bekleniyor): %v", len(vm.queries), vm.queries)
	}
	for i, q := range vm.queries {
		if !strings.Contains(q, "[600s]") || !strings.Contains(q, `service_name="orders"`) || !strings.Contains(q, "sum by (http_route, service_version)") || !vm.at[i].Equal(wantTo) {
			t.Fatalf("sorgu %d: %s @ %s", i, q, vm.at[i])
		}
	}
	eb := out.Endpoints
	if eb == nil || eb.Failed || len(eb.Rows) != 2 || eb.Other == nil || eb.OtherCount != 1 || eb.NormalFailed {
		t.Fatalf("endpoint bloğu %+v", eb)
	}
	if r := eb.Rows[0]; r.Route != "/orders/assessment" || !near(r.Current, 0.18) || !near(r.FailedPerMin, 18) || r.Normal == nil || !near(*r.Normal, 0.003) || !r.Deviates {
		t.Fatalf("ilk rota %+v", r)
	}
	if r := eb.Rows[1]; r.Route != "/orders" || !near(r.Current, 0.011) || r.Deviates {
		t.Fatalf("ikinci rota (eşik altı) %+v", r)
	}
	if !near(eb.FailedPerMin, 20.2) || !near(eb.RequestsPerMin, 310) {
		t.Fatalf("toplam %v / %v", eb.FailedPerMin, eb.RequestsPerMin)
	}
	// Taban okuması dedektörün dilimiyle ve servise kapsanmış gitti.
	if st.gotBase.Service != "orders" || st.gotBase.TargetSec != chstore.DefaultMLConfig().BucketSec || len(st.gotBase.Dates) == 0 || st.gotBase.HourTo < st.gotBase.HourFrom {
		t.Fatalf("taban isteği %+v", st.gotBase)
	}
	vb := out.Versions
	if vb == nil || len(vb.Rows) != 2 || vb.Label != "service_version" || vb.Rows[0].Version != "v2.14.1" || !near(vb.Rows[0].Current, 0.08) || !vb.Rows[0].Deviates || vb.Rows[1].Deviates {
		t.Fatalf("sürüm bloğu %+v", vb)
	}
	xb := out.Exceptions
	if xb == nil || xb.Failed || len(xb.Rows) != 2 || xb.Total != 100 || xb.Rows[0].Fingerprint != "fp-app" || xb.Rows[0].Share == nil || !near(*xb.Rows[0].Share, 0.94) || xb.Rows[1].Fingerprint != "" {
		t.Fatalf("exception bloğu %+v", xb)
	}
	if !st.gotExFrom.Equal(wantFrom) || !st.gotExTo.Equal(wantTo) {
		t.Fatalf("exception penceresi sayım penceresi olmalı: %s → %s", st.gotExFrom, st.gotExTo)
	}
	db := out.Dependencies
	if db == nil || db.Failed || len(db.Rows) != 1 || db.Rows[0].EventID != "ev-dep" || db.Rows[0].Target != "customer-rel" || !db.Rows[0].Live {
		t.Fatalf("bağımlılık bloğu %+v", db)
	}
	// Bağımlılıklar Problem'in YAŞAM penceresiyle okunur (başlangıç − pay … şimdi).
	if want := time.Unix(0, st.p.StartedAt).Add(-chstore.ProblemEventSlack); !st.gotDepFrom.Equal(want) || !st.gotDepTo.Equal(bdNow) {
		t.Fatalf("bağımlılık penceresi %s → %s", st.gotDepFrom, st.gotDepTo)
	}
}

func TestGetProblemBreakdown_BlocksDegradeIndependently(t *testing.T) {
	boom := errors.New("upstream: timeout")
	t.Run("VM yok: endpoint ve sürüm blokları yok, ötekiler çizilir", func(t *testing.T) {
		st, _ := bdFixture()
		w, out := bdCall(t, st, nil, "p1")
		if w.Code != 200 || out.Endpoints != nil || out.Versions != nil || out.Exceptions == nil || out.Dependencies == nil {
			t.Fatalf("code=%d %+v", w.Code, out)
		}
		if strings.Contains(w.Body.String(), `"endpoints"`) {
			t.Fatalf("verisi olmayan blok gövdede olmamalı: %s", w.Body.String())
		}
	})
	t.Run("VM sorgusu düşer: endpoint okunamadı, ötekiler çizilir", func(t *testing.T) {
		st, vm := bdFixture()
		vm.queryErr = boom
		w, out := bdCall(t, st, vm, "p1")
		if w.Code != 200 || out.Endpoints == nil || !out.Endpoints.Failed || len(out.Endpoints.Rows) != 0 || out.Versions != nil {
			t.Fatalf("code=%d endpoints=%+v versions=%+v", w.Code, out.Endpoints, out.Versions)
		}
		if out.Exceptions == nil || out.Exceptions.Failed || out.Dependencies == nil || out.Dependencies.Failed {
			t.Fatalf("öteki bloklar etkilenmemeli: %+v %+v", out.Exceptions, out.Dependencies)
		}
	})
	t.Run("etiket keşfi düşer: endpoint okunamadı (yok DEĞİL)", func(t *testing.T) {
		st, vm := bdFixture()
		vm.labelErr = boom
		_, out := bdCall(t, st, vm, "p1")
		if out.Endpoints == nil || !out.Endpoints.Failed || len(vm.queries) != 0 {
			t.Fatalf("%+v (sorgu %d)", out.Endpoints, len(vm.queries))
		}
	})
	t.Run("rota tabanı düşer: tablo olağansız çizilir ve bunu söyler", func(t *testing.T) {
		st, vm := bdFixture()
		st.baseErr = boom
		_, out := bdCall(t, st, vm, "p1")
		eb := out.Endpoints
		if eb == nil || eb.Failed || !eb.NormalFailed || len(eb.Rows) != 2 || eb.Rows[0].Normal != nil || eb.Rows[0].Deviates {
			t.Fatalf("%+v", eb)
		}
	})
	t.Run("exception sayımı düşer: yalnız o blok okunamadı", func(t *testing.T) {
		st, vm := bdFixture()
		st.typesErr = boom
		w, out := bdCall(t, st, vm, "p1")
		if w.Code != 200 || out.Exceptions == nil || !out.Exceptions.Failed || out.Endpoints == nil || out.Endpoints.Failed || out.Dependencies == nil {
			t.Fatalf("code=%d %+v", w.Code, out)
		}
	})
	t.Run("exception grup araması düşer: sayımlar kalır, bağlantı yok", func(t *testing.T) {
		st, vm := bdFixture()
		st.groupsErr = boom
		_, out := bdCall(t, st, vm, "p1")
		xb := out.Exceptions
		if xb == nil || xb.Failed || len(xb.Rows) != 2 || xb.Rows[0].Count != 94 || xb.Rows[0].Fingerprint != "" {
			t.Fatalf("%+v", xb)
		}
	})
	t.Run("bağımlılık okuması düşer: yalnız o blok okunamadı", func(t *testing.T) {
		st, vm := bdFixture()
		st.depsErr = boom
		w, out := bdCall(t, st, vm, "p1")
		if w.Code != 200 || out.Dependencies == nil || !out.Dependencies.Failed || out.Exceptions == nil || out.Exceptions.Failed {
			t.Fatalf("code=%d %+v", w.Code, out)
		}
	})
	t.Run("hiçbir kaynakta veri yok: üç blok da gövdede yok", func(t *testing.T) {
		st, vm := bdFixture()
		st.types, st.deps, vm.errs = nil, nil, nil
		w, out := bdCall(t, st, vm, "p1")
		if w.Code != 200 || !out.Applicable || out.Endpoints != nil || out.Versions != nil || out.Exceptions != nil || out.Dependencies != nil {
			t.Fatalf("code=%d %+v", w.Code, out)
		}
	})
}

func TestGetProblemBreakdown_VersionOnlyWhenDefensible(t *testing.T) {
	t.Run("seride sürüm etiketi yok: sürüm bloğu yok, sorgu sürüme göre kırılmaz", func(t *testing.T) {
		st, vm := bdFixture()
		vm.labels[mcHTTPMetric] = []string{"service_name", "http_route", "http_response_status_code"}
		_, out := bdCall(t, st, vm, "p1")
		if out.Versions != nil || out.Endpoints == nil || len(out.Endpoints.Rows) != 2 {
			t.Fatalf("%+v / %+v", out.Versions, out.Endpoints)
		}
		for _, q := range vm.queries {
			if strings.Contains(q, "service_version") {
				t.Fatalf("sürüm etiketi keşfedilmeden sorguya girmemeli: %s", q)
			}
		}
	})
	t.Run("tek sürüm: kıyas yok, blok yok", func(t *testing.T) {
		st, vm := bdFixture()
		vm.count = []chstore.SpanMetricSeries{bdSeries("/orders", "v2.14.1", 1000)}
		vm.errs = []chstore.SpanMetricSeries{bdSeries("/orders", "v2.14.1", 100)}
		_, out := bdCall(t, st, vm, "p1")
		if out.Versions != nil || out.Endpoints == nil {
			t.Fatalf("%+v / %+v", out.Versions, out.Endpoints)
		}
	})
	t.Run("durum etiketi yok: hata oranı ölçülemez, endpoint bloğu yok", func(t *testing.T) {
		st, vm := bdFixture()
		vm.labels[mcHTTPMetric] = []string{"service_name", "http_route"}
		_, out := bdCall(t, st, vm, "p1")
		if out.Endpoints != nil || len(vm.queries) != 0 {
			t.Fatalf("%+v (sorgu %d)", out.Endpoints, len(vm.queries))
		}
	})
}

func TestGetProblemBreakdown_NotApplicableReadsNothing(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*chstore.Problem)
	}{
		{"gecikme Problem'i", func(p *chstore.Problem) { p.Metric = "p99_ms" }},
		{"veritabanı öznesi", func(p *chstore.Problem) { p.Kind = chstore.ProblemKindDB }},
		{"8 gün önce kapanmış", func(p *chstore.Problem) {
			ns := bdNow.Add(-8 * 24 * time.Hour).UnixNano()
			p.Status, p.ResolvedAt, p.StartedAt = "resolved", &ns, bdNow.Add(-9*24*time.Hour).UnixNano()
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, vm := bdFixture()
			c.mut(st.p)
			w, out := bdCall(t, st, vm, "p1")
			if w.Code != 200 || out.Applicable || out.ProblemID != "p1" {
				t.Fatalf("code=%d %+v", w.Code, out)
			}
			if st.reads != 0 || len(vm.queries) != 0 {
				t.Fatalf("konu dışı Problem'de kaynak okunmamalı: CH=%d VM=%d", st.reads, len(vm.queries))
			}
			if strings.Contains(w.Body.String(), "windowFrom") {
				t.Fatalf("pencere dönmemeli: %s", w.Body.String())
			}
		})
	}
}

func TestGetProblemBreakdown_LookupAndErrors(t *testing.T) {
	t.Run("görüntü kimliği (P-…) kabul edilir", func(t *testing.T) {
		st, vm := bdFixture()
		if w, out := bdCall(t, st, vm, "P-3f9a2"); w.Code != 200 || st.gotDisplay != "P-3f9a2" || st.gotID != "" || !out.Applicable {
			t.Fatalf("code=%d display=%q id=%q", w.Code, st.gotDisplay, st.gotID)
		}
	})
	t.Run("Problem yok → 404", func(t *testing.T) {
		st, vm := bdFixture()
		st.p = nil
		if w, _ := bdCall(t, st, vm, "gone"); w.Code != 404 || st.reads != 0 {
			t.Fatalf("code=%d reads=%d", w.Code, st.reads)
		}
	})
	t.Run("Problem okuması düşer → 5xx (boş bölüm diye yutulmaz)", func(t *testing.T) {
		st, vm := bdFixture()
		st.p, st.pErr = nil, errors.New("code: 159, timeout")
		if w, _ := bdCall(t, st, vm, "p1"); w.Code < 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("kapanmış Problem: bağımlılık penceresi kapanışta biter", func(t *testing.T) {
		st, vm := bdFixture()
		res := bdNow.Add(-30 * time.Minute).UnixNano()
		st.p.Status, st.p.ResolvedAt, st.p.StartedAt = "resolved", &res, bdNow.Add(-70*time.Minute).UnixNano()
		_, out := bdCall(t, st, vm, "p1")
		// Kapanış 20:45:40 → sayım penceresi kapanış dakikasının sonunda (20:46) biter;
		// başlangıç 20:05:40 → 20:05: 41 dakika.
		wantTo := time.Date(2026, 11, 18, 20, 46, 0, 0, time.UTC)
		if out.Open || !st.gotDepTo.Equal(time.Unix(0, res)) || out.WindowTo != wantTo.UnixNano() {
			t.Fatalf("open=%v depTo=%s windowTo=%d", out.Open, st.gotDepTo, out.WindowTo)
		}
		if len(vm.queries) != 2 || !strings.Contains(vm.queries[0], "[2460s]") || !vm.at[0].Equal(wantTo) {
			t.Fatalf("kapanmış pencere 41 dk olmalı: %v", vm.queries)
		}
	})
}

// Route defterinde, metot önekli ve rol kapısız (viewer okur; kardeş /events duruşu).
func TestProblemBreakdownRouteRegistered(t *testing.T) {
	mux := http.NewServeMux()
	(&Server{}).registerProblemBreakdownRoutes(mux)
	r := httptest.NewRequest("GET", "/api/problems/p1/breakdown", nil)
	if _, pat := mux.Handler(r); pat != "GET /api/problems/{id}/breakdown" {
		t.Fatalf("kalıp %q", pat)
	}
	if _, pat := mux.Handler(httptest.NewRequest("POST", "/api/problems/p1/breakdown", nil)); pat == "GET /api/problems/{id}/breakdown" {
		t.Fatal("yalnız GET kayıtlı olmalı")
	}
}
