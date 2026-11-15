package mcptools

// v0.10.1297 — operatör 2026-10-17: ACM operasyon adı linki yanlış
// (coremetry.io + search=). CoSRE "EXAMPLE_PAYMENTSYSTEMCAMPAIGN_WALLET_
// LIST_OF_GAINED_CAMPAIGNS"i doğru çözdü (kod GXWQD01) ama build_link
// search=<ad> ile çağrıldı → /traces serbest metin araması sıfır trace; model
// de metne https://coremetry.io/… yazdı. Pinler:
//   - build_link mutlak host'u YALNIZ PUBLIC_URL doluyken yazar; href daima
//     kök-göreli, hiçbir dalda sabit bir dış alan adı yok;
//   - operasyon adı (tam eşleşme kodlu) → FUNCTION_CODE süzgeci, search değil
//     (build_link, search_traces kapısı, query_traces + kanıt href'i).

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const acmOpName = "EXAMPLE_PAYMENTSYSTEMCAMPAIGN_WALLET_LIST_OF_GAINED_CAMPAIGNS"

func acmOracle() *fakeOracleOps {
	return &fakeOracleOps{enabled: true, res: OracleOpResolution{Enabled: true, SpanKey: "FUNCTION_CODE",
		Matches: []OracleOpMatch{{Code: "GXWQD01", SpanKey: "FUNCTION_CODE", Operation: acmOpName, Rows: 12}}}}
}

func TestAbsoluteAppLink(t *testing.T) {
	cases := []struct{ base, href, want string }{
		{"", "/traces?x=1", ""},
		{"https://coremetry.bank.local", "/traces?x=1", "https://coremetry.bank.local/traces?x=1"},
		{"https://coremetry.bank.local/", "/traces", "https://coremetry.bank.local/traces"},
		{"HTTPS://apm.bank.local/coremetry/", "/logs?q=a", "https://apm.bank.local/coremetry/logs?q=a"},
		{"coremetry.bank.local", "/traces", ""},               // şemasız
		{"ftp://coremetry.bank.local", "/traces", ""},         // http(s) değil
		{"https://u:p@coremetry.bank.local", "/traces", ""},   // kimlik bilgisi
		{"https://coremetry.bank.local", "traces", ""},        // kök-göreli değil
		{"https://coremetry.bank.local", "//evil/traces", ""}, // protokol-göreli
		{"https://coremetry.bank.local", "/\\evil", ""},
		{"https://coremetry.bank.local?x=1", "/traces", ""},
	}
	for _, c := range cases {
		if got := absoluteAppLink(c.base, c.href); got != c.want {
			t.Errorf("absoluteAppLink(%q,%q) = %q, want %q", c.base, c.href, got, c.want)
		}
	}
}

// build_link: PUBLIC_URL yokken mutlak host YOK; varken yalnız onun host'u.
func TestBuildLinkNeverForeignHost(t *testing.T) {
	args := []string{
		`{"page":"traces","service":"checkout","search":"x","sort":"duration"}`,
		`{"page":"logs","service":"checkout","query":"error"}`,
		`{"page":"service","service":"checkout"}`,
		`{"page":"problem","problem_id":"p1"}`,
		`{"page":"trace","trace_id":"0123456789abcdef0123456789abcdef"}`,
	}
	for _, pub := range []string{"", "  ", "not a url", "https://coremetry.bank.local"} {
		pub := pub
		d := Deps{PublicURL: func() string { return pub }}
		if pub == "" {
			d.PublicURL = nil // dış MCP / test: dikiş bağlı değil
		}
		tool := buildLinkTool(d)
		for _, a := range args {
			out, err := tool.Handler(context.Background(), json.RawMessage(a))
			if err != nil {
				t.Fatalf("%s: %v", a, err)
			}
			m := out.(map[string]any)
			href := m["href"].(string)
			if !strings.HasPrefix(href, "/") || strings.HasPrefix(href, "//") || strings.Contains(href, "://") {
				t.Errorf("pub=%q %s: href kök-göreli değil: %s", pub, a, href)
			}
			abs, has := m["url"].(string)
			switch pub {
			case "https://coremetry.bank.local":
				if !has || abs != pub+href {
					t.Errorf("PUBLIC_URL varken url = %q, want %q", abs, pub+href)
				}
			default:
				if has {
					t.Errorf("pub=%q: PUBLIC_URL geçersiz/boşken mutlak url yazıldı: %s", pub, abs)
				}
			}
			b, _ := json.Marshal(m)
			if strings.Contains(string(b), "coremetry.io") {
				t.Errorf("sabit dış alan adı sızdı: %s", b)
			}
		}
	}
}

func traceFilters(t *testing.T, href string) (url.Values, []chstore.FilterExpr) {
	t.Helper()
	u, err := url.Parse(href)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	var fs []chstore.FilterExpr
	if raw := q.Get("filters"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fs); err != nil {
			t.Fatalf("filters JSON: %v (%s)", err, raw)
		}
	}
	return q, fs
}

func TestBuildLinkOperationNameUsesFunctionCode(t *testing.T) {
	cases := []struct {
		name       string
		ops        OracleOpSource
		args       string
		wantSearch string
		wantCode   bool
	}{
		{"ACM operasyon adı → kod", acmOracle(), `{"page":"traces","search":"` + acmOpName + `","sort":"duration"}`, "", true},
		{"servisle birlikte", acmOracle(), `{"page":"traces","service":"acm-payments-campaign-prod","search":"` + acmOpName + `"}`, "", true},
		{"tam eşleşme yok → search kalır", &fakeOracleOps{enabled: true, res: OracleOpResolution{Enabled: true, SpanKey: "FUNCTION_CODE", NearNames: []string{"X_Y"}}}, `{"page":"traces","search":"` + acmOpName + `"}`, acmOpName, false},
		{"Oracle kapalı → search kalır", &fakeOracleOps{enabled: false}, `{"page":"traces","search":"` + acmOpName + `"}`, acmOpName, false},
		{"çözümleyici yok", nil, `{"page":"traces","search":"` + acmOpName + `"}`, acmOpName, false},
		{"operasyon şekli değil", acmOracle(), `{"page":"traces","search":"checkout"}`, "checkout", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Deps{}
			if c.ops != nil {
				d.OracleOps = c.ops
			}
			out, err := buildLinkTool(d).Handler(context.Background(), json.RawMessage(c.args))
			if err != nil {
				t.Fatal(err)
			}
			m := out.(map[string]any)
			q, fs := traceFilters(t, m["href"].(string))
			if got := q.Get("search"); got != c.wantSearch {
				t.Errorf("search = %q, want %q (%s)", got, c.wantSearch, m["href"])
			}
			hasCode := false
			for _, f := range fs {
				if f.Key == "FUNCTION_CODE" && f.Op == "=" && len(f.Values) == 1 && f.Values[0] == "GXWQD01" {
					hasCode = true
				}
			}
			if hasCode != c.wantCode {
				t.Errorf("FUNCTION_CODE=GXWQD01 süzgeci = %v, want %v (%s)", hasCode, c.wantCode, m["href"])
			}
			if _, tr := m["operation_translated"]; tr != c.wantCode {
				t.Errorf("operation_translated = %v, want %v", tr, c.wantCode)
			}
		})
	}
	// Şekil kapısı: operasyon adı değilse çözümleyiciye hiç gidilmez.
	f := acmOracle()
	_, _ = buildLinkTool(Deps{OracleOps: f}).Handler(context.Background(), json.RawMessage(`{"page":"traces","search":"checkout"}`))
	if f.calls != 0 {
		t.Errorf("şekil dışı search için çözümleyici çağrıldı (%d)", f.calls)
	}
	// Traces dışı sayfada search çevrilmez (endpoints serbest metni).
	f = acmOracle()
	out, _ := buildLinkTool(Deps{OracleOps: f}).Handler(context.Background(), json.RawMessage(`{"page":"endpoints","search":"`+acmOpName+`"}`))
	if q, _ := traceFilters(t, out.(map[string]any)["href"].(string)); q.Get("search") != acmOpName || f.calls != 0 {
		t.Errorf("endpoints search değişmemeli: %v calls=%d", q, f.calls)
	}
}

func TestTranslateOperationSearchMultiCode(t *testing.T) {
	f := &fakeOracleOps{enabled: true, res: OracleOpResolution{Enabled: true, SpanKey: "function_code", Matches: []OracleOpMatch{
		{Code: "A1"}, {Code: "A1"}, {Code: " B2 "}, {Code: ""}}}}
	tr, ok := translateOperationSearch(context.Background(), f, "  "+acmOpName+" ")
	if !ok || tr.Operation != acmOpName || strings.Join(tr.Codes, ",") != "A1,B2" {
		t.Fatalf("çeviri: %+v ok=%v", tr, ok)
	}
	if fe := tr.Filter(); fe.Key != "function_code" || fe.Op != "IN" || len(fe.Values) != 2 {
		t.Errorf("çok kod IN olmalı: %+v", fe)
	}
	if _, ok := translateOperationSearch(context.Background(), &fakeOracleOps{enabled: true, res: OracleOpResolution{Matches: []OracleOpMatch{{Code: "A1"}}}}, acmOpName); ok {
		t.Error("span anahtarı boşken çeviri olmamalı")
	}
}

func TestBusinessCodeScopeFilter(t *testing.T) {
	cases := []struct {
		f    chstore.FilterExpr
		want bool
	}{
		{chstore.FilterExpr{Key: "FUNCTION_CODE", Op: "=", Values: []string{"GXWQD01"}}, true},
		{chstore.FilterExpr{Key: "function_code", Op: "IN", Values: []string{"A", "B"}}, true},
		{chstore.FilterExpr{Key: "CHANNEL_CODE", Op: "=", Values: []string{"060203"}}, true},
		{chstore.FilterExpr{Key: "FUNCTION_CODE", Op: "=~", Values: []string{"GC.*"}}, false}, // regex kapsam değil
		{chstore.FilterExpr{Key: "http.route", Op: "=", Values: []string{"/x"}}, false},
	}
	for _, c := range cases {
		if got := isBusinessCodeScopeFilter(c.f); got != c.want {
			t.Errorf("%+v → %v, want %v", c.f, got, c.want)
		}
	}
	in := []chstore.FilterExpr{cases[0].f, cases[4].f, cases[2].f}
	out := partitionScopeFilters(in)
	if out[0].Key != "http.route" || out[1].Key != "FUNCTION_CODE" || out[2].Key != "CHANNEL_CODE" {
		t.Errorf("operatör süzgeçleri önde, kod süzgeçleri sonda (sıra korunur): %+v", out)
	}
}

// query_traces: operation bir operasyon adıysa kod süzgeci + kanıt href'i de kodla.
func TestQueryTracesOperationNameUsesFunctionCode(t *testing.T) {
	c := &cqTraces{}
	b := c.backends()
	b.oracleOps = acmOracle()
	r, err := runQueryTraces(cqCtx(), b, cqRaw(t, map[string]any{"service": "acm-payments-campaign-prod", "operation": acmOpName}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.got) != 1 || len(c.got[0].Filters) != 1 || c.got[0].Filters[0].Key != "FUNCTION_CODE" || c.got[0].Filters[0].Values[0] != "GXWQD01" {
		t.Fatalf("okuma kod süzgeciyle yapılmalı: %+v", c.got)
	}
	raw, _ := json.Marshal(r)
	ev, ok := ToolResultEvidence(queryTracesName, string(raw))
	if !ok {
		t.Fatalf("kanıt yok: %s", raw)
	}
	q, fs := traceFilters(t, ev.Href)
	if q.Get("search") != "" || len(fs) != 1 || fs[0].Key != "FUNCTION_CODE" || fs[0].Values[0] != "GXWQD01" {
		t.Errorf("kanıt href'i kod süzgeci taşımalı: %s", ev.Href)
	}
	// Çevrilemeyen ad eski name süzgecinde kalır.
	c2 := &cqTraces{}
	if _, err := runQueryTraces(cqCtx(), c2.backends(), cqRaw(t, map[string]any{"service": "checkout", "operation": "GET /x"})); err != nil {
		t.Fatal(err)
	}
	if len(c2.got) != 1 || c2.got[0].Filters[0].Key != "name" {
		t.Errorf("span operasyonu name süzgecinde kalmalı: %+v", c2.got)
	}
}
