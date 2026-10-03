package api

// v0.10.1091 — yaygın yavaşlama (`svc-slowdown:<servis>`) Problem'inin detay
// grafiği: kural satırı yok, alert_rules okunmaz; sentetik kural (pencere
// 300 s, yalnız p99_ms) servis p99 dizisini değerlendiricinin MV yolundan verir.
// Grafiğin eşik çizgisi ayardaki minP99Ms (vars. 5000 ms) — Problem'in
// Threshold'u (en yavaş operasyonun kendi tabanı, öncelik oranı için) DEĞİL.

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAlertRuleSeriesSynthetic(t *testing.T) {
	r := alertRuleSeriesSynthetic("svc-slowdown:checkout-svc", "p99_ms", 5000)
	if r == nil || r.WindowSec != 300 || r.Metric != "p99_ms" || r.Threshold != 5000 || alertRuleSeriesUnsupported(r) != "" {
		t.Fatalf("svc-slowdown p99_ms sentetik kural: %+v", r)
	}
	for _, c := range []struct{ id, metric string }{
		{"svc-slowdown:checkout-svc", "error_rate"}, // yalnız servis p99'u
		{"builtin-warn-http-p99-3s", "p99_ms"},      // gerçek kural → okuma
		{"db-health:oracle@h/d", "p99_ms"},
		{"r-1a2b", "p99_ms"},
	} {
		if alertRuleSeriesSynthetic(c.id, c.metric, 5000) != nil {
			t.Errorf("%s/%s sentetik olmamalı", c.id, c.metric)
		}
	}
}

func TestGetAlertRuleSeries_SvcSlowdown(t *testing.T) {
	orig := alertRuleSeriesStoreOf
	t.Cleanup(func() { alertRuleSeriesStoreOf = orig })
	call := func(st *fakeAlertRuleSeriesStore, id, metric string) *httptest.ResponseRecorder {
		alertRuleSeriesStoreOf = func(*Server) alertRuleSeriesStore { return st }
		s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
		from := time.Now().Add(-70 * time.Minute).UnixNano()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/alert-rules/x/series?service=checkout-svc&metric="+metric+"&from="+itoa64(from), nil)
		r.SetPathValue("id", id)
		s.getAlertRuleSeries(w, r)
		return w
	}
	// Kural okuması HATA verse bile sentetik yol ona hiç gitmez.
	st := &fakeAlertRuleSeriesStore{ruleErr: errors.New("alert_rules okunmamalı")}
	w := call(st, "svc-slowdown:checkout-svc", "p99_ms")
	if w.Code != 200 || st.calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", w.Code, st.calls, w.Body.String())
	}
	if q := st.gotQuery; q.Metric != "p99_ms" || q.Service != "checkout-svc" || q.WindowSec != 300 {
		t.Fatalf("sorgu %+v", q)
	}
	var body alertRuleSeriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Threshold == nil || *body.Threshold != 5000 {
		t.Fatalf("grafik çizgisi ayardaki minP99Ms (5000) olmalı: %+v", body.Threshold)
	}
	// Operatör tabanı değiştirirse çizgi onu izler (ve önbellek anahtarı ayrışır).
	sens := chstore.DefaultAnomalySensitivity()
	sens.ServiceSlowdown.MinP99Ms = 8000
	st2 := &fakeAlertRuleSeriesStore{sens: &sens}
	w = call(st2, "svc-slowdown:checkout-svc", "p99_ms")
	body = alertRuleSeriesResponse{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 200 || body.Threshold == nil || *body.Threshold != 8000 || st2.calls != 1 {
		t.Fatalf("ayar 8000: code=%d thr=%v calls=%d", w.Code, body.Threshold, st2.calls)
	}
	spec, err := chstore.AlertMetricSeriesSpec("p99_ms", 300)
	if err != nil || spec.RawSpans || spec.StepSec != 300 {
		t.Fatalf("300 s pencere MV yolundan (service_summary_5m, 5 dk kova) okunmalı: %+v %v", spec, err)
	}
	// Gerçek kuralların cevabı eşik taşımaz (istemci problemin eşiğini çizer).
	st3 := &fakeAlertRuleSeriesStore{rule: &chstore.AlertRule{ID: "r1", Metric: "p99_ms", WindowSec: 600, Threshold: 3000}}
	w = call(st3, "r1", "p99_ms")
	if w.Code != 200 || containsKey(w.Body.Bytes(), "threshold") {
		t.Fatalf("gerçek kural threshold taşımamalı: %d %s", w.Code, w.Body.String())
	}
}

func containsKey(b []byte, k string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	_, ok := m[k]
	return ok
}
