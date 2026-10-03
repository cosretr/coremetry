package api

// alert_rule_series_test.go — v0.10.1064 (operatör, prod alarm problemi
// detayı: "grafik olmadığı için de anlamak çok zor artışları").
// GET /api/alert-rules/{id}/series: kural türü kapısı, pencere hizası ve
// tavanı, anahtar, okumaya giden sorgu, uç durumları.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAlertRuleSeriesUnsupported(t *testing.T) {
	cases := []struct {
		name string
		r    *chstore.AlertRule
		ok   bool
	}{
		{"yok", nil, false},
		{"span metriği (yerleşik)", &chstore.AlertRule{Metric: "http_p99_ms", WindowSec: 600}, true},
		{"log sorgusu", &chstore.AlertRule{Metric: "log_query", LogQuery: "timeout", WindowSec: 300}, false},
		{"watcher", &chstore.AlertRule{Metric: "watcher", WatcherJSON: "{}", WindowSec: 300}, false},
		{"hedefli (route)", &chstore.AlertRule{Metric: "http_route_p99_ms", WindowSec: 300, Target: &chstore.RuleTarget{}}, false},
		{"pencere yok", &chstore.AlertRule{Metric: "p99_ms"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := alertRuleSeriesUnsupported(c.r) == ""; got != c.ok {
				t.Fatalf("destek=%v, want %v", got, c.ok)
			}
		})
	}
}

func TestSnapAlertRuleSeriesWindow(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 28, 20, 0, time.UTC)
	mv := chstore.AlertSeriesSpec{StepSec: 300, MaxSpan: 24 * time.Hour}
	raw := chstore.AlertSeriesSpec{StepSec: 300, RawSpans: true, MaxSpan: 6 * time.Hour}
	min1 := chstore.AlertSeriesSpec{StepSec: 60, RawSpans: true, MaxSpan: 6 * time.Hour}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 3, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name     string
		from, to time.Time
		sp       chstore.AlertSeriesSpec
		ok       bool
		wf, wt   time.Time
	}{
		{"açık problem: to yok → şimdi, yukarı hizalı", now.Add(-62 * time.Minute), time.Time{}, mv, true, at(9, 25), at(10, 30)},
		{"bitmiş problem: iki uç hizalı", at(8, 3), at(9, 41), mv, true, at(8, 0), at(9, 45)},
		{"gelecek to → şimdi", at(9, 0), now.Add(time.Hour), mv, true, at(9, 0), at(10, 30)},
		{"ham spans tavanı 6 sa: eski uç kırpılır", now.Add(-20 * time.Hour), time.Time{}, raw, true, at(4, 25), at(10, 30)},
		{"MV tavanı 24 sa", now.Add(-30 * time.Hour), time.Time{}, mv, true, at(10, 25).Add(-24 * time.Hour), at(10, 30)},
		{"1 dk ızgara", now.Add(-61 * time.Minute), time.Time{}, min1, true, at(9, 27), at(10, 29)},
		{"from yok", time.Time{}, now, mv, false, time.Time{}, time.Time{}},
		{"ters pencere", now, now.Add(-time.Hour), mv, false, time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, to, ok := snapAlertRuleSeriesWindow(c.from, c.to, now, c.sp)
			if ok != c.ok {
				t.Fatalf("ok=%v want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if !f.Equal(c.wf) || !to.Equal(c.wt) {
				t.Fatalf("got %s → %s, want %s → %s", f.UTC(), to.UTC(), c.wf, c.wt)
			}
			if n := to.Sub(f) / (time.Duration(c.sp.StepSec) * time.Second); n > 360 {
				t.Fatalf("%d kova — tavan aşıldı", n)
			}
		})
	}
}

func TestAlertRuleSeriesKey_HashesAllInputs(t *testing.T) {
	f, to := time.Unix(6000, 0), time.Unix(9600, 0)
	base := alertRuleSeriesKey("builtin-warn-http-p99-3s", "http_p99_ms", "checkout-svc", 600, f, to, 300)
	variants := []string{
		alertRuleSeriesKey("builtin-http-p99-5s", "http_p99_ms", "checkout-svc", 600, f, to, 300),
		alertRuleSeriesKey("builtin-warn-http-p99-3s", "p99_ms", "checkout-svc", 600, f, to, 300),
		alertRuleSeriesKey("builtin-warn-http-p99-3s", "http_p99_ms", "billing-svc", 600, f, to, 300),
		alertRuleSeriesKey("builtin-warn-http-p99-3s", "http_p99_ms", "checkout-svc", 300, f, to, 300),
		alertRuleSeriesKey("builtin-warn-http-p99-3s", "http_p99_ms", "checkout-svc", 600, f.Add(5*time.Minute), to, 300),
		alertRuleSeriesKey("builtin-warn-http-p99-3s", "http_p99_ms", "checkout-svc", 600, f, to.Add(5*time.Minute), 300),
		alertRuleSeriesKey("builtin-warn-http-p99-3s", "http_p99_ms", "checkout-svc", 600, f, to, 60),
	}
	for i, v := range variants {
		if v == base {
			t.Fatalf("varyant %d anahtarı değiştirmedi: %s", i, v)
		}
	}
	if alertRuleSeriesKey("builtin-warn-http-p99-3s", "http_p99_ms", "checkout-svc", 600, f, to, 300) != base {
		t.Fatal("anahtar kararlı değil")
	}
}

type fakeAlertRuleSeriesStore struct {
	rule     *chstore.AlertRule
	ruleErr  error
	pts      []chstore.AlertMetricPoint
	err      error
	calls    int
	gotQuery chstore.AlertMetricSeriesQuery
}

func (f *fakeAlertRuleSeriesStore) GetAlertRule(context.Context, string) (*chstore.AlertRule, error) {
	return f.rule, f.ruleErr
}
func (f *fakeAlertRuleSeriesStore) AlertMetricSeries(_ context.Context, q chstore.AlertMetricSeriesQuery) ([]chstore.AlertMetricPoint, error) {
	f.calls++
	f.gotQuery = q
	return f.pts, f.err
}

func TestGetAlertRuleSeries(t *testing.T) {
	orig := alertRuleSeriesStoreOf
	t.Cleanup(func() { alertRuleSeriesStoreOf = orig })
	srv := func(st *fakeAlertRuleSeriesStore) *Server {
		alertRuleSeriesStoreOf = func(*Server) alertRuleSeriesStore { return st }
		return &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	}
	from := time.Now().Add(-70 * time.Minute).UnixNano()
	call := func(st *fakeAlertRuleSeriesStore, id, q string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/alert-rules/"+id+"/series?"+q, nil)
		r.SetPathValue("id", id)
		srv(st).getAlertRuleSeries(w, r)
		return w
	}
	httpRule := &chstore.AlertRule{ID: "builtin-warn-http-p99-3s", Metric: "http_p99_ms", WindowSec: 600}
	ok := "service=checkout-svc&metric=http_p99_ms&from=" + itoa64(from)

	t.Run("eksik parametre → 400, okumaya gidilmez", func(t *testing.T) {
		for _, q := range []string{"metric=http_p99_ms&from=1", "service=checkout-svc&from=1", "service=checkout-svc&metric=http_p99_ms"} {
			st := &fakeAlertRuleSeriesStore{rule: httpRule}
			if w := call(st, "r1", q); w.Code != 400 || st.calls != 0 {
				t.Fatalf("%s: code=%d calls=%d", q, w.Code, st.calls)
			}
		}
	})
	t.Run("kural yok → 404", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{ruleErr: sql.ErrNoRows}
		if w := call(st, "gone", ok); w.Code != 404 || st.calls != 0 {
			t.Fatalf("code=%d calls=%d", w.Code, st.calls)
		}
	})
	t.Run("kural okuma hatası → 5xx (404 diye yutulmaz)", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{ruleErr: errors.New("code: 159, timeout")}
		if w := call(st, "r1", ok); w.Code < 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("log sorgusu kuralı → 404 (sahte grafik yok)", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: &chstore.AlertRule{Metric: "log_query", LogQuery: "timeout", WindowSec: 300}}
		if w := call(st, "r1", "service=checkout-svc&metric=log_query&from=1"); w.Code != 404 || st.calls != 0 {
			t.Fatalf("code=%d calls=%d", w.Code, st.calls)
		}
	})
	t.Run("bilinmeyen metrik → 404", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: httpRule}
		if w := call(st, "r1", "service=checkout-svc&metric=bogus&from=1"); w.Code != 404 || st.calls != 0 {
			t.Fatalf("code=%d calls=%d", w.Code, st.calls)
		}
	})
	t.Run("veri → kuralın penceresi + problemin metriği/servisi, hizalı uçlar", func(t *testing.T) {
		v := 3872.62
		st := &fakeAlertRuleSeriesStore{rule: httpRule, pts: []chstore.AlertMetricPoint{{T: 1, V: &v}, {T: 2}}}
		w := call(st, "builtin-warn-http-p99-3s", ok)
		if w.Code != 200 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		gq := st.gotQuery
		if gq.Metric != "http_p99_ms" || gq.Service != "checkout-svc" || gq.WindowSec != 600 {
			t.Fatalf("sorgu %+v", gq)
		}
		if gq.From.UnixNano()%int64(5*time.Minute) != 0 || gq.To.UnixNano()%int64(5*time.Minute) != 0 {
			t.Fatalf("uçlar 5 dk'ya hizalı değil: %s → %s", gq.From, gq.To)
		}
		var body alertRuleSeriesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.WindowSec != 600 || body.StepSec != 300 || len(body.Points) != 2 || body.Points[1].V != nil || *body.Points[0].V != v {
			t.Fatalf("gövde %+v", body)
		}
		// Boşluk JSON'da null — istemci sıfır diye çizmesin.
		if !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `{"t":2,"v":null}`) {
			t.Fatalf("boşluk null değil: %s", w.Body.String())
		}
	})
	t.Run("boş sonuç → [] (null değil)", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: httpRule}
		w := call(st, "r1", ok)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"points":[]`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("okuma hatası → 5xx", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: httpRule, err: errors.New("code: 241, memory limit")}
		if w := call(st, "r2", ok); w.Code < 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

func TestAlertRuleSeriesRouteRegistered(t *testing.T) {
	if _, ok := extraRouteRegistrars["alert-rule-series"]; !ok {
		t.Fatal("rota deftere kaydolmamış — kayıtsız rota 200 + boş SPA döner")
	}
}
