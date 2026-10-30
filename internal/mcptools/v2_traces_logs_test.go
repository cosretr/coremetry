package mcptools

// v2_traces_logs_test.go — CoSRE v2 araç seti dilim 3 (v0.10.1688):
// find_traces / search_logs (v2) / explain_trace sözleşmesi — kapsam önceliği,
// anahtar TÜM girdileri hash'ler (v0.5.187: aynı uzunlukta farklı küme
// çakışmaz), gövde ≤ 2 KB + page_url, v1/dış katalog izolasyonu.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestMaxTraceServicesMatchesTeamCap(t *testing.T) {
	if chstore.MaxTraceServices != MaxTeamServices {
		t.Fatalf("chstore.MaxTraceServices=%d, MaxTeamServices=%d", chstore.MaxTraceServices, MaxTeamServices)
	}
}

func TestPlanFindTraces(t *testing.T) {
	cases := []struct {
		name     string
		a        findTracesArgs
		service  string
		services []string
		filters  int
		wantErr  bool
	}{
		{"kapsam yok → hata", findTracesArgs{}, "", nil, 0, true},
		{"küme sıralı", findTracesArgs{Services: []string{"payments-api", "orders-api"}}, "", []string{"orders-api", "payments-api"}, 0, false},
		{"açık servis kümeden üstün", findTracesArgs{Service: "orders-api", Services: []string{"a", "b"}}, "orders-api", nil, 0, false},
		{"tek elemanlı küme tekil", findTracesArgs{Services: []string{"orders-api"}}, "orders-api", nil, 0, false},
		{"kod servissiz yeter", findTracesArgs{FunctionCode: "bkrb001"}, "", nil, 1, false},
		{"geçersiz kod", findTracesArgs{ChannelCode: "KNL 1;", Service: "orders-api"}, "", nil, 0, true},
	}
	for _, c := range cases {
		p, err := planFindTraces(c.a, true)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v", c.name, err)
			continue
		}
		if c.wantErr {
			continue
		}
		if p.Service != c.service || fmt.Sprint(p.Services) != fmt.Sprint(c.services) || len(p.Filters) != c.filters {
			t.Errorf("%s: got %q %v filters=%d", c.name, p.Service, p.Services, len(p.Filters))
		}
		if p.Limit != ftMaxRows || p.WindowS != ftDefaultRangeS && p.Narrowed == "" {
			t.Errorf("%s: limit=%d window=%d", c.name, p.Limit, p.WindowS)
		}
	}
	p, _ := planFindTraces(findTracesArgs{Service: "orders-api", RangeS: 999999}, true)
	if p.WindowS != ftMaxRangeS {
		t.Errorf("pencere tavanı: %d", p.WindowS)
	}
}

func TestV2CacheKeysHashAllInputs(t *testing.T) {
	to := time.Unix(1_700_000_000, 0)
	base := findTracesPlan{Services: []string{"orders-api", "payments-api"}, WindowS: 3600, Limit: 10}
	k := findTracesCacheKey(base, to)
	muts := map[string]func(p *findTracesPlan){
		"aynı uzunlukta farklı küme": func(p *findTracesPlan) { p.Services = []string{"orders-api", "billing-api"} },
		"hata":                       func(p *findTracesPlan) { p.Errors = true },
		"sıralama":                   func(p *findTracesPlan) { p.Slowest = true },
		"min":                        func(p *findTracesPlan) { p.MinMs = 500 },
		"env":                        func(p *findTracesPlan) { p.Env = "prod" },
		"pencere":                    func(p *findTracesPlan) { p.WindowS = 1800 },
		"süzgeç": func(p *findTracesPlan) {
			p.Filters = []chstore.FilterExpr{{Key: "name", Op: "=", Values: []string{"GET /orders"}}}
		},
	}
	for name, m := range muts {
		p := base
		m(&p)
		if findTracesCacheKey(p, to) == k {
			t.Errorf("find_traces anahtarı %s değişikliğini görmüyor", name)
		}
	}
	if findTracesCacheKey(base, to.Add(2*time.Minute)) == k {
		t.Error("pencere sonu anahtarda yok")
	}
	lb := searchLogsPlan{Services: []string{"orders-api", "payments-api"}, WindowS: 3600, Limit: 10}
	lk := searchLogsCacheKey("es", lb, to)
	for name, m := range map[string]func(p *searchLogsPlan){
		"küme":   func(p *searchLogsPlan) { p.Services = []string{"orders-api", "billing-api"} },
		"metin":  func(p *searchLogsPlan) { p.Search = `"timeout"` },
		"seviye": func(p *searchLogsPlan) { p.Severity, p.SevMin = "error", 17 },
		"trace":  func(p *searchLogsPlan) { p.TraceID = strings.Repeat("a", 32) },
		"takım":  func(p *searchLogsPlan) { p.Team = "acme-orders" },
	} {
		p := lb
		m(&p)
		if searchLogsCacheKey("es", p, to) == lk {
			t.Errorf("search_logs anahtarı %s değişikliğini görmüyor", name)
		}
	}
	if searchLogsCacheKey("clickhouse", lb, to) == lk {
		t.Error("arka uç anahtarda yok")
	}
	if explainTraceCacheKey("ab", []string{"a", "b"}) == explainTraceCacheKey("ab", []string{"a", "c"}) {
		t.Error("explain_trace anahtarı kapsamı görmüyor")
	}
}

func TestFindTracesPageURL(t *testing.T) {
	from, to := time.UnixMilli(1_700_000_000_000), time.UnixMilli(1_700_003_600_000)
	small := chstore.TraceFilter{Services: []string{"orders-api", "payments-api"}, Sort: "time"}
	u, scoped := FindTracesPageURL(small, from, to)
	if !scoped || !strings.HasPrefix(u, "/traces?") || !strings.Contains(u, "service.name") || !strings.Contains(u, "orders-api") {
		t.Errorf("küçük küme: %s %v", u, scoped)
	}
	big := chstore.TraceFilter{Sort: "time"}
	for i := 0; i < ftLinkMaxSvcs+1; i++ {
		big.Services = append(big.Services, fmt.Sprintf("svc-%02d", i))
	}
	u, scoped = FindTracesPageURL(big, from, to)
	if scoped || strings.Contains(u, "svc-00") {
		t.Errorf("büyük küme sayfaya yazılmamalı: %s", u)
	}
	if one, _ := FindTracesPageURL(chstore.TraceFilter{Service: "orders-api"}, from, to); !strings.Contains(one, "service=orders-api") {
		t.Errorf("tekil: %s", one)
	}
}

func TestFindTracesPayloadUnder2KB(t *testing.T) {
	p := findTracesPlan{Services: []string{"orders-api", "payments-api"}, WindowS: 3600, Limit: 10}
	from, to := time.UnixMilli(1_700_000_000_000), time.UnixMilli(1_700_003_600_000)
	var rows []chstore.TraceRow
	for i := 0; i < 10; i++ {
		rows = append(rows, chstore.TraceRow{TraceID: strings.Repeat(fmt.Sprint(i%10), 32), RootName: "POST /api/orders/" + strings.Repeat("x", 120), ServiceName: "orders-api", DurationMs: 12.5, SpanCount: 40, HasError: true})
	}
	out := findTracesPayload(p, p.traceFilter(from, to), rows, false, from, to)
	b, _ := json.Marshal(out)
	if len(b) > V2ToolMaxBytes || out["page_url"] == "" || out["count"].(int) == 0 {
		t.Errorf("gövde %d B, count=%v", len(b), out["count"])
	}
	if out["count"].(int) < 10 && out["truncated"] != true {
		t.Error("kırpma ilan edilmedi")
	}
	empty := findTracesPayload(p, p.traceFilter(from, to), nil, false, from, to)
	if empty["reasons"] == nil || empty["count"].(int) != 0 {
		t.Error("boş sonuç nedeni yok")
	}
}

func TestPlanSearchLogs(t *testing.T) {
	tid := strings.Repeat("ab", 16)
	cases := []struct {
		name     string
		a        cosreSearchLogsArgs
		service  string
		services int
		trace    string
		wantErr  bool
	}{
		{"kapsam yok", cosreSearchLogsArgs{Words: "timeout"}, "", 0, "", true},
		{"küme", cosreSearchLogsArgs{Services: []string{"b", "a"}}, "", 2, "", false},
		{"trace kapsamı kümeyi ezer", cosreSearchLogsArgs{TraceID: tid, Services: []string{"a", "b"}, Service: "orders-api"}, "", 0, tid, false},
		{"bozuk trace", cosreSearchLogsArgs{TraceID: "xyz", Service: "orders-api"}, "", 0, "", true},
	}
	for _, c := range cases {
		p, err := planSearchLogs(c.a)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v", c.name, err)
			continue
		}
		if !c.wantErr && (p.Service != c.service || len(p.Services) != c.services || p.TraceID != c.trace) {
			t.Errorf("%s: %q %v %q", c.name, p.Service, p.Services, p.TraceID)
		}
	}
	p, _ := planSearchLogs(cosreSearchLogsArgs{Service: "orders-api", Words: `status:500 AND "x"`, Severity: "hata"})
	if strings.ContainsAny(p.Words, `"`) || p.SevMin != qlOTelErrorFloor {
		t.Errorf("metin/seviye: %q %d", p.Words, p.SevMin)
	}
}

func TestSearchLogsPageURLAndPayload(t *testing.T) {
	from, to := time.UnixMilli(1_700_000_000_000), time.UnixMilli(1_700_003_600_000)
	p := searchLogsPlan{Services: []string{"orders-api", "payments-api"}, Team: "acme-orders", WindowS: 3600, Limit: 10, Words: "timeout"}
	u := SearchLogsPageURL(p, from, to)
	if !strings.HasPrefix(u, "/logs") || !strings.Contains(u, "team=acme-orders") {
		t.Errorf("küme linki: %s", u)
	}
	p1 := searchLogsPlan{Service: "orders-api", Team: "acme-orders", WindowS: 3600, Limit: 10}
	if u := SearchLogsPageURL(p1, from, to); strings.Contains(u, "team=") || !strings.Contains(u, "orders-api") {
		t.Errorf("tekil linki: %s", u)
	}
	page := &logstore.Page{Total: 500}
	for i := 0; i < 10; i++ {
		page.Logs = append(page.Logs, &logstore.LogRecord{Timestamp: int64(i) * 1e9, ServiceName: "orders-api", Body: strings.Repeat("connection reset ", 30), TraceID: strings.Repeat("c", 32)})
	}
	out := searchLogsPayload("es", p, page, []int64{1, 2, 3}, nil, from, to)
	b, _ := json.Marshal(out)
	if len(b) > V2ToolMaxBytes || out["has_more"] != true {
		t.Errorf("gövde %d B has_more=%v", len(b), out["has_more"])
	}
	series := []logstore.LogSeries{{Points: []logstore.LogPoint{{T: 2, V: 1}, {T: 1, V: 4}}}, {Points: []logstore.LogPoint{{T: 1, V: 1}}}}
	if got := slHistogram(series); fmt.Sprint(got) != "[5 1]" {
		t.Errorf("histogram: %v", got)
	}
}

func TestExplainTracePayload(t *testing.T) {
	an := traceToolAnalysis{WallMs: 1200, ExtentMs: 1300, SpanCount: 400, ErrorSpanCount: 9,
		Root:     traceToolSpanRef{Service: "orders-api", Name: "POST /api/orders", DurationMs: 1200},
		Services: []traceToolService{{Service: "orders-api"}, {Service: "inventory-api"}, {Service: "payments-api"}},
	}
	for i := 0; i < 12; i++ {
		an.CriticalPath = append(an.CriticalPath, traceToolPathStep{Service: "orders-api", Name: "step " + strings.Repeat("y", 70), SelfOnPathMs: 100, SharePct: 8, Top: i == 0})
		an.ErrorSpans = append(an.ErrorSpans, traceToolErrorSpan{Service: "payments-api", Name: "charge", StatusMessage: strings.Repeat("declined ", 40), ExceptionMessage: strings.Repeat("e", 300)})
	}
	id := strings.Repeat("d", 32)
	out := explainTracePayload(id, an, []string{"orders-api", "payments-api", "billing-api"})
	b, _ := json.Marshal(out)
	if len(b) > V2ToolMaxBytes {
		t.Fatalf("gövde %d B", len(b))
	}
	if out["page_url"] != "/trace/"+id || out["scope_services_in_trace"] != 2 {
		t.Errorf("page_url/kapsam: %v %v", out["page_url"], out["scope_services_in_trace"])
	}
	if steps := out["critical_path"].([]etStep); len(steps) > etMaxPathSteps || len(steps) == 0 {
		t.Errorf("kritik yol %d adım", len(steps))
	}
	if _, has := out["spans"]; has {
		t.Error("span listesi gövdede")
	}
}

func TestV2MergeAndPageURL(t *testing.T) {
	d := goldenConditionalDeps()
	reg := goldenEntry(t, ToolList(d)).Names
	chat := goldenEntry(t, ChatToolListFor(d, "full")).Names
	v2 := V2ToolList(d, "full")
	for _, n := range []string{FindTracesName, ExplainTraceName} {
		if containsStr(reg, n) || containsStr(chat, n) {
			t.Errorf("%s registry/v1 sohbetine sızdı", n)
		}
	}
	seen := 0
	for i, tl := range v2 {
		if tl.Name != SearchLogsV2Name {
			continue
		}
		seen++
		if _, ok := tl.InputSchema["properties"].(map[string]any)["words"]; !ok {
			t.Error("v2 search_logs v1 şemasını taşıyor")
		}
		if chat[i] != SearchLogsV2Name {
			t.Errorf("search_logs sırası değişti (%d)", i)
		}
	}
	if seen != 1 {
		t.Errorf("v2'de %d search_logs", seen)
	}
	for _, tl := range ChatToolListFor(d, "full") {
		if tl.Name == SearchLogsV2Name {
			if _, ok := tl.InputSchema["properties"].(map[string]any)["words"]; ok {
				t.Error("v1 search_logs değişti")
			}
		}
	}
	for _, c := range []struct{ tool, body, want string }{
		{FindTracesName, `{"page_url":"/traces?x=1"}`, "/traces?x=1"},
		{FindTracesName, `{"page_url":"//evil"}`, ""},
		{SearchLogsV2Name, `{"deep_link":"/logs?q"}`, ""},
		{"query_traces", `{"page_url":"/traces"}`, ""},
	} {
		if got := V2PageURL(c.tool, c.body); got != c.want {
			t.Errorf("V2PageURL(%s,%s)=%q", c.tool, c.body, got)
		}
	}
}
