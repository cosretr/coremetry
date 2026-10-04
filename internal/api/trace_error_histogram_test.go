package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1082 — Operator-reported (prod): "Error seçildiğinde histogram
// gelmiyor." Errors + `function_code` / `k8s.pod.name` çipiyle liste dolu,
// şerit 0. Şerit artık /api/traces ile AYNI sorgu dizesini AYNI ayrıştırıcıdan
// (parseTraceFilter) geçirir; bu test o eşlemeyi ve uygunluk kapısını pinler.

// Liste ile şerit aynı sorgu dizesinden AYNI TraceFilter'ı kurar (step/stat
// listenin süzgecine karışmaz).
func TestTraceErrorHistogramSharesListParser(t *testing.T) {
	list := "from=1791021600000000000&to=1791025200000000000&hasError=true&service=svc-a&env=prod" +
		"&filters=" + url.QueryEscape(`[{"k":"function_code","op":"=","v":["KYC0001"]}]`)
	lq, _ := url.ParseQuery(list)
	sq, _ := url.ParseQuery(list + "&step=60&stat=p95")
	lf, err := parseTraceFilter(lq)
	if err != nil {
		t.Fatal(err)
	}
	sf, err := parseTraceFilter(sq)
	if err != nil {
		t.Fatal(err)
	}
	if lf.Service != sf.Service || lf.Env != sf.Env || lf.HasError != sf.HasError || !lf.From.Equal(sf.From) || !lf.To.Equal(sf.To) ||
		len(lf.Filters) != 1 || len(sf.Filters) != 1 || lf.Filters[0].Key != sf.Filters[0].Key || lf.Filters[0].Values[0] != sf.Filters[0].Values[0] {
		t.Fatalf("şerit süzgeci listeden ayrıştı:\n%+v\n%+v", lf, sf)
	}
	if !chstore.TraceErrorHistogramEligible(sf) {
		t.Fatal("Errors + function_code çipi uygun olmalı")
	}
}

// v0.10.1101 — çipsiz Errors (yalnız / servis / ortam / küme): liste sorgu
// dizesinden uygun TraceFilter kurulur; şerit metric-batch'e düşmez.
func TestTraceErrorHistogramScopeOnlyEligible(t *testing.T) {
	for _, qs := range []string{
		"hasError=true",
		"hasError=true&service=svc-a",
		"hasError=true&env=prod",
		"hasError=true&service=svc-a&cluster=c1",
	} {
		q, _ := url.ParseQuery("from=1791021600000000000&to=1791025200000000000&" + qs)
		f, err := parseTraceFilter(q)
		if err != nil {
			t.Fatalf("%s: %v", qs, err)
		}
		if !chstore.TraceErrorHistogramEligible(f) {
			t.Errorf("%s: uygun olmalı", qs)
		}
	}
}

func TestTraceErrorHistogramRejectsIneligible(t *testing.T) {
	s := &Server{}
	cases := []string{
		"hasError=true&rootOnly=true", // v0.10.1101 — çipsiz + Root → metric-batch (kök trace düzeyi)
		"filters=" + url.QueryEscape(`[{"k":"function_code","op":"=","v":["KYC0001"]}]`), // Errors yok
		"hasError=true&search=x&filters=" + url.QueryEscape(`[{"k":"function_code","op":"=","v":["KYC0001"]}]`),
		"hasError=true&minMs=5&filters=" + url.QueryEscape(`[{"k":"function_code","op":"=","v":["KYC0001"]}]`),
	}
	for _, qs := range cases {
		w := httptest.NewRecorder()
		s.getTraceErrorHistogram(w, httptest.NewRequest(http.MethodGet, "/api/traces/error-histogram?"+qs, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: kod %d, beklenen 400", qs, w.Code)
		}
	}
}

func TestTraceErrorHistogramQuantile(t *testing.T) {
	for in, want := range map[string]float64{"p50": 0.5, "p95": 0.95, "P99": 0.99, "": 0.5, "p999": 0.5, "x": 0.5} {
		if got := traceErrorHistogramQuantile(in); got != want {
			t.Errorf("%q → %v, beklenen %v", in, got, want)
		}
	}
}

// Anahtar TÜM girdileri taşır (v0.5.187): stat / step / çip değeri / kök
// tanımı değişince anahtar değişir; from/to ızgarada (cacheRawQuery).
func TestTraceErrorHistogramKey(t *testing.T) {
	base := "hasError=true&filters=" + url.QueryEscape(`[{"k":"function_code","op":"=","v":["KYC0001"]}]`) + "&step=60&stat=p50"
	k0 := traceErrorHistogramKey(base, "")
	for name, other := range map[string]string{
		"stat":  traceErrorHistogramKey(strings.Replace(base, "stat=p50", "stat=p95", 1), ""),
		"step":  traceErrorHistogramKey(strings.Replace(base, "step=60", "step=120", 1), ""),
		"çip":   traceErrorHistogramKey(strings.Replace(base, "KYC0001", "KYC0002", 1), ""),
		"kök":   traceErrorHistogramKey(base, ":rd=entry"),
		"liste": "traces:" + cacheRawQueryString(base),
	} {
		if other == k0 {
			t.Errorf("%s değişimi anahtarı değiştirmeli", name)
		}
	}
	if !strings.HasPrefix(k0, "traces-error-histogram:") {
		t.Errorf("önek: %s", k0)
	}
}

// Zarf metric-batch ile aynı şekil (series.count/errors/rt + stepSeconds) +
// kip/tavan; boş seri null değil [] (FE .map()'liyor).
func TestTraceErrorHistogramPayload(t *testing.T) {
	p := []chstore.SpanMetricPoint{{Time: 1, Value: 2}}
	b, _ := json.Marshal(traceErrorHistogramPayload(chstore.TraceErrorHistogram{Mode: "trace", Step: 60, Capped: true, Source: "rollup", Count: p, Errors: p, RT: p}))
	var got struct {
		Series      map[string][]chstore.SpanMetricSeries `json:"series"`
		StepSeconds int                                   `json:"stepSeconds"`
		Mode        string                                `json:"mode"`
		Capped      bool                                  `json:"capped"`
		Source      string                                `json:"source"` // v0.10.1101
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.StepSeconds != 60 || got.Mode != "trace" || !got.Capped || got.Source != "rollup" || len(got.Series["count"]) != 1 || got.Series["errors"][0].Points[0].Value != 2 {
		t.Fatalf("zarf: %s", b)
	}
	b, _ = json.Marshal(traceErrorHistogramPayload(chstore.TraceErrorHistogram{Mode: "trace", Step: 60}))
	if strings.Contains(string(b), "null") {
		t.Fatalf("boş seri [] olmalı: %s", b)
	}
}
