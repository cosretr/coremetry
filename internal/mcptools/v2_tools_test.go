package mcptools

// v2_tools_test.go — CoSRE v2 araç seti dilim 2 (tools-audit §9, §10 G10,
// Q7/Q8): list_my_services gövdesi, önbellek anahtarı, v2 model alt kümesi.
// Fikstürler nötr (orders-api, com.acme).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestNormalizeMyServices(t *testing.T) {
	many := make([]string, 0, MaxTeamServices+7)
	for i := 0; i < MaxTeamServices+7; i++ {
		many = append(many, fmt.Sprintf("svc-%03d", i))
	}
	cases := []struct {
		name        string
		in          []string
		want        []string
		wantTrimmed int
	}{
		{"boş", nil, nil, 0},
		{"kırp + tekil + sırala", []string{" orders-api", "billing-api", "orders-api", "", "  "}, []string{"billing-api", "orders-api"}, 0},
		{"tavan", many, many[:MaxTeamServices], 7},
	}
	for _, c := range cases {
		got, tr := normalizeMyServices(c.in)
		if strings.Join(got, ",") != strings.Join(c.want, ",") || tr != c.wantTrimmed {
			t.Errorf("%s: %v/%d, want %v/%d", c.name, got, tr, c.want, c.wantTrimmed)
		}
	}
}

// v0.5.187 dersi: aynı uzunlukta farklı küme farklı anahtar; her girdi anahtara girer.
func TestMyServicesCacheKeyHashesAllInputs(t *testing.T) {
	to := time.Unix(1_800_000_000, 0)
	base := myServicesCacheKey([]string{"billing-api", "orders-api"}, 0, "acme-team", "", to, 3600, 10)
	for name, k := range map[string]string{
		"küme":    myServicesCacheKey([]string{"billing-api", "payments-api"}, 0, "acme-team", "", to, 3600, 10),
		"tavan":   myServicesCacheKey([]string{"billing-api", "orders-api"}, 3, "acme-team", "", to, 3600, 10),
		"takım":   myServicesCacheKey([]string{"billing-api", "orders-api"}, 0, "other-team", "", to, 3600, 10),
		"dakika":  myServicesCacheKey([]string{"billing-api", "orders-api"}, 0, "acme-team", "", to.Add(2*time.Minute), 3600, 10),
		"pencere": myServicesCacheKey([]string{"billing-api", "orders-api"}, 0, "acme-team", "", to, 7200, 10),
		"satır":   myServicesCacheKey([]string{"billing-api", "orders-api"}, 0, "acme-team", "", to, 3600, 20),
	} {
		if k == base {
			t.Errorf("%s anahtara girmiyor: %s", name, k)
		}
	}
}

func myRows(n int, nameLen int) []chstore.ServiceSummary {
	out := make([]chstore.ServiceSummary, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("com.acme.orders-api-%02d", i)
		if len(name) < nameLen {
			name += strings.Repeat("x", nameLen-len(name))
		}
		out = append(out, chstore.ServiceSummary{Name: name, ErrorRate: float64(n - i), ErrorCount: uint64(100 * (n - i)), SpanCount: 123456789, P99Ms: 1234.5})
	}
	return out
}

func TestMyServicesPayload(t *testing.T) {
	names := func(rows []chstore.ServiceSummary, extra ...string) []string {
		out := append([]string{}, extra...)
		for _, r := range rows {
			out = append(out, r.Name)
		}
		return out
	}
	cases := []struct {
		name          string
		team          string
		rows          []chstore.ServiceSummary
		resolvedExtra []string
		limit         int
		wantCount     int
		wantHasMore   bool
		wantTrunc     bool
		wantSilent    int
		wantReasons   bool
	}{
		{"küçük takım", "acme-team", myRows(3, 0), nil, 10, 3, false, false, 0, false},
		{"limit", "acme-team", myRows(15, 0), nil, 10, 10, true, false, 0, false},
		{"sessiz servisler sayılır", "acme-team", myRows(2, 0), []string{"orders-worker", "orders-batch"}, 10, 2, false, false, 2, false},
		{"hiç span yok", "acme-team", nil, []string{"orders-api"}, 10, 0, false, false, 1, true},
		{"uzun adlar 2 KB'ye kırpılır", "acme-team", myRows(20, 120), nil, 20, -1, true, true, 0, false},
	}
	for _, c := range cases {
		resolved := names(c.rows, c.resolvedExtra...)
		out := myServicesPayload(c.team, resolved, 0, c.rows, 3600, c.limit, "", false, nil)
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > MyServicesMaxBytes {
			t.Errorf("%s: gövde %d B > %d", c.name, len(b), MyServicesMaxBytes)
		}
		n, _ := out["count"].(int)
		if c.wantCount >= 0 && n != c.wantCount {
			t.Errorf("%s: count %d, want %d", c.name, n, c.wantCount)
		}
		if c.wantCount < 0 && (n == 0 || n >= len(c.rows)) {
			t.Errorf("%s: kırpılmış satır sayısı %d", c.name, n)
		}
		if out["has_more"] != c.wantHasMore {
			t.Errorf("%s: has_more %v", c.name, out["has_more"])
		}
		if _, tr := out["truncated"]; tr != c.wantTrunc {
			t.Errorf("%s: truncated %v", c.name, tr)
		}
		if out["silent_services"] != c.wantSilent {
			t.Errorf("%s: silent %v, want %d", c.name, out["silent_services"], c.wantSilent)
		}
		if _, r := out["reasons"]; r != c.wantReasons {
			t.Errorf("%s: reasons %v", c.name, r)
		}
		if u, _ := out["page_url"].(string); u != "/services?q=acme-team" {
			t.Errorf("%s: page_url %q", c.name, u)
		}
	}
}

func TestMyServicesPageURL(t *testing.T) {
	for in, want := range map[string]string{"": "/services", "  ": "/services", "acme team": "/services?q=acme+team", "a&b": "/services?q=a%26b"} {
		if got := MyServicesPageURL(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// Kapsam yoksa adlandırılmış hata (model takım uydurmaz, sorar); okuma yapılmaz.
func TestListMyServicesNoScope(t *testing.T) {
	tool := listMyServicesTool(Deps{})
	for _, raw := range []string{``, `{}`, `{"services":[" ",""]}`} {
		if _, err := tool.Handler(context.Background(), json.RawMessage(raw)); !errors.Is(err, errNoMyScope) {
			t.Errorf("%q: err %v, want errNoMyScope", raw, err)
		}
	}
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"services":`)); err == nil || !strings.Contains(err.Error(), "decode args") {
		t.Errorf("bozuk argüman: %v", err)
	}
}

func TestV2NarrowSchema(t *testing.T) {
	in := map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}, "service": map[string]any{"type": "string"}},
		"required":   []string{"query", "service"},
	}
	cases := []struct {
		name, tool string
		wantQuery  bool
		wantReq    []string
	}{
		{"explain_log_spike query düşer", ExplainLogSpikeName, false, []string{"service"}},
		{"başka araç aynen", "query_logs", true, []string{"query", "service"}},
	}
	for _, c := range cases {
		out := V2NarrowSchema(c.tool, in)
		_, has := out["properties"].(map[string]any)["query"]
		if has != c.wantQuery || fmt.Sprint(out["required"]) != fmt.Sprint(c.wantReq) {
			t.Errorf("%s: query=%v required=%v", c.name, has, out["required"])
		}
	}
	if _, ok := in["properties"].(map[string]any)["query"]; !ok {
		t.Fatal("girdi şema değişti")
	}
	anyReq := map[string]any{"properties": map[string]any{"query": 1}, "required": []any{"query"}}
	if r := V2NarrowSchema(ExplainLogSpikeName, anyReq)["required"].([]any); len(r) != 0 {
		t.Errorf("[]any required: %v", r)
	}
}

func TestV2StripArgs(t *testing.T) {
	cases := []struct {
		tool, in, want string
		dropped        int
	}{
		{ExplainLogSpikeName, `{"query":"level:error","service":"orders-api"}`, `{"service":"orders-api"}`, 1},
		{ExplainLogSpikeName, `{"service":"orders-api"}`, `{"service":"orders-api"}`, 0},
		{ExplainLogSpikeName, ``, ``, 0},
		{ExplainLogSpikeName, `not json`, `not json`, 0},
		{"query_logs", `{"query":"x"}`, `{"query":"x"}`, 0},
	}
	for _, c := range cases {
		got, d := V2StripArgs(c.tool, json.RawMessage(c.in))
		if string(got) != c.want || len(d) != c.dropped {
			t.Errorf("%s %s → %s %v", c.tool, c.in, got, d)
		}
	}
}

// v2 alt kümesi registry'yi DEĞİŞTİRMEZ: v2-yalnız araç ToolList / sohbet /
// dış MCP'de yok; gizliler v2'de yok ama registry'de var.
func TestV2SubsetIsolation(t *testing.T) {
	d := goldenConditionalDeps()
	has := func(names []string, n string) bool { return containsStr(names, n) }
	reg := goldenEntry(t, ToolList(d)).Names
	chat := goldenEntry(t, ChatToolListFor(d, "full")).Names
	v2 := goldenEntry(t, V2ToolList(d, "full")).Names
	if has(reg, ListMyServicesName) || has(chat, ListMyServicesName) {
		t.Fatal("list_my_services registry/v1 sohbetine sızdı")
	}
	if !has(v2, ListMyServicesName) {
		t.Fatal("list_my_services v2'de yok")
	}
	for n := range v2HiddenTools {
		if !has(reg, n) {
			t.Errorf("%s registry'den silinmiş (alt küme olmalı)", n)
		}
		if has(v2, n) {
			t.Errorf("%s v2 modeline sunuluyor", n)
		}
	}
	for _, tl := range V2ToolList(d, "full") {
		if tl.Name == ExplainLogSpikeName {
			if _, ok := tl.InputSchema["properties"].(map[string]any)["query"]; ok {
				t.Error("explain_log_spike.query v2 şemasında")
			}
		}
	}
}
