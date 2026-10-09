package mcptools

// v0.10.1133 — Oracle operasyon adı → FUNCTION_CODE çözümleyicisinin SAF
// çekirdeği + resolve_oracle_operation aracı (operatör: CoSRE operasyon
// adıyla sorulan trace'lere "trace bulunamadı" diyordu). Sentetik adlar.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

func TestResolveOracleOpHits(t *testing.T) {
	hit := func(op string, rows uint64, trace string, codes ...string) chstore.OracleOpHit {
		return chstore.OracleOpHit{Operation: op, Rows: rows, LastTraceID: trace, FunctionCodes: codes}
	}
	cases := []struct {
		name      string
		q         string
		hits      []chstore.OracleOpHit
		wantCodes []string
		wantNear  []string
		wantTrace string
	}{
		{"tam eşleşme", "DEPOSIT_SAMPLE_INQUIRY_REST",
			[]chstore.OracleOpHit{hit("DEPOSIT_SAMPLE_INQUIRY_REST", 40, "t-1", "SAMP01")},
			[]string{"SAMP01"}, []string{}, "t-1"},
		{"harf duyarsız + kırpılmış", "  deposit_sample_inquiry_rest ",
			[]chstore.OracleOpHit{hit("DEPOSIT_SAMPLE_INQUIRY_REST", 40, "", "SAMP01")},
			[]string{"SAMP01"}, []string{}, ""},
		{"alt-dize ASLA kod üretmez, yalnız yakın ad", "DEPOSIT_SAMPLE",
			[]chstore.OracleOpHit{hit("DEPOSIT_SAMPLE_INQUIRY_REST", 40, "t-1", "SAMP01"), hit("DEPOSIT_SAMPLE_CONFIRM", 9, "t-2", "SAMP02")},
			[]string{}, []string{"DEPOSIT_SAMPLE_INQUIRY_REST", "DEPOSIT_SAMPLE_CONFIRM"}, ""},
		{"bir ad birden çok kod", "ORDER_SAMPLE_SERVICE",
			[]chstore.OracleOpHit{hit("ORDER_SAMPLE_SERVICE", 12, "t-9", "SAMP01", "SAMP03", " ")},
			[]string{"SAMP01", "SAMP03"}, []string{}, "t-9"},
		{"harf varyantı isabetleri birleşir, kod tekrarlanmaz", "ORDER_SAMPLE_SERVICE",
			[]chstore.OracleOpHit{hit("ORDER_SAMPLE_SERVICE", 12, "", "SAMP01"), hit("order_sample_service", 3, "t-3", "SAMP01", "SAMP04"), hit("ORDER_SAMPLE_SERVICE_V2", 1, "", "SAMP05")},
			[]string{"SAMP01", "SAMP04"}, []string{"ORDER_SAMPLE_SERVICE_V2"}, "t-3"},
		{"boş ad", "  ", []chstore.OracleOpHit{hit("X_SAMPLE", 1, "", "SAMP01")}, []string{}, []string{}, ""},
		{"isabet yok", "NOPE_SAMPLE", nil, []string{}, []string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ResolveOracleOpHits(c.q, "FUNCTION_CODE", c.hits)
			if !r.Enabled || r.SpanKey != "FUNCTION_CODE" {
				t.Fatalf("enabled/span key: %+v", r)
			}
			if got := r.Codes(); strings.Join(got, ",") != strings.Join(c.wantCodes, ",") {
				t.Errorf("kodlar = %v, want %v", got, c.wantCodes)
			}
			if strings.Join(r.NearNames, ",") != strings.Join(c.wantNear, ",") || r.NearNames == nil {
				t.Errorf("yakın adlar = %v, want %v", r.NearNames, c.wantNear)
			}
			if r.LatestTraceID() != c.wantTrace {
				t.Errorf("son trace = %q, want %q", r.LatestTraceID(), c.wantTrace)
			}
			for _, m := range r.Matches {
				if m.SpanKey != "FUNCTION_CODE" || !strings.EqualFold(m.Operation, strings.TrimSpace(c.q)) {
					t.Errorf("eşleşme tam ad değil: %+v", m)
				}
			}
		})
	}
}

func TestResolveOracleOpHitsNearNamesCapped(t *testing.T) {
	var hits []chstore.OracleOpHit
	for _, op := range []string{"A_SAMPLE_1", "A_SAMPLE_2", "A_SAMPLE_3", "A_SAMPLE_4", "A_SAMPLE_5", "A_SAMPLE_6", "A_SAMPLE_1"} {
		hits = append(hits, chstore.OracleOpHit{Operation: op, FunctionCodes: []string{"SAMP01"}})
	}
	r := ResolveOracleOpHits("A_SAMPLE", "FUNCTION_CODE", hits)
	if len(r.NearNames) != oracleNearNamesMax || len(r.Matches) != 0 {
		t.Fatalf("yakın ad tavanı/tekilliği: %+v", r)
	}
}

type fakeOracleOps struct {
	enabled bool
	res     OracleOpResolution
	err     error
	calls   int
}

func (f *fakeOracleOps) Enabled() bool { return f.enabled }
func (f *fakeOracleOps) ResolveOracleOperation(_ context.Context, name string) (OracleOpResolution, error) {
	f.calls++
	return f.res, f.err
}

func TestResolveOracleOperationToolSchema(t *testing.T) {
	tool := toolByName(t, ToolList(Deps{}), OracleOperationToolName)
	props := schemaProps(t, tool)
	if _, ok := props["name"]; !ok || len(props) != 1 {
		t.Fatalf("şema yalnız name taşımalı: %v", props)
	}
	req, _ := tool.InputSchema["required"].([]string)
	if len(req) != 1 || req[0] != "name" {
		t.Fatalf("name zorunlu olmalı: %v", tool.InputSchema["required"])
	}
	// Salt okuma: viewer tabanı (REST eşi kapısız), dış MCP'de kayıtlı (sohbet-yalnız değil).
	if tool.MinRole != "" || chatOnlyTools[tool.Name] || externalOnlyTools[tool.Name] {
		t.Fatalf("MinRole/görünürlük: %+v", tool.MinRole)
	}
	if !strings.Contains(tool.ShortDescription, "FUNCTION_CODE") || !strings.Contains(tool.Description, "Read-only") {
		t.Error("açıklama ne zaman çağrılacağını ve salt okuma olduğunu söylemeli")
	}
	srv := mcp.New("coremetry", "test")
	Register(srv, Deps{})
	if srv.ToolCount() != len(ToolList(Deps{}))-len(chatOnlyTools) {
		t.Fatal("dış MCP kaydı")
	}
}

func TestResolveOracleOperationToolBadInput(t *testing.T) {
	f := &fakeOracleOps{enabled: true}
	tool := toolByName(t, ToolList(Deps{OracleOps: f}), OracleOperationToolName)
	for _, raw := range []string{`{}`, `{"name":"  "}`, `{"name":"AB"}`, `{"name":"` + strings.Repeat("A", 81) + `"}`, `{"name":`} {
		_, err := tool.Handler(context.Background(), json.RawMessage(raw))
		if err == nil {
			t.Errorf("%s: hata bekleniyordu", raw)
			continue
		}
		var te mcp.ToolError
		if jerr := json.Unmarshal([]byte(mcp.ToolErrorJSON(err)), &te); jerr != nil || te.Error != mcp.ToolErrBadArgs {
			t.Errorf("%s: sınıf %q, want bad_args (%v)", raw, te.Error, err)
		}
	}
	if f.calls != 0 {
		t.Fatal("geçersiz girdi çözümleyiciye gitmemeli")
	}
}

func TestResolveOracleOperationToolDisabled(t *testing.T) {
	for _, d := range []Deps{{}, {OracleOps: &fakeOracleOps{enabled: false}}} {
		tool := toolByName(t, ToolList(d), OracleOperationToolName)
		out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"DEPOSIT_SAMPLE_INQUIRY_REST"}`))
		if err != nil {
			t.Fatalf("Oracle kapalı → hata değil boş sonuç: %v", err)
		}
		m := out.(map[string]any)
		if m["enabled"] != false || len(m["codes"].([]string)) != 0 || !strings.Contains(m["hint"].(string), "Oracle") {
			t.Fatalf("kapalı gövde: %v", m)
		}
		// Sohbete SUNULMAZ (koşullu araç).
		for _, tl := range ChatToolList(d) {
			if tl.Name == OracleOperationToolName {
				t.Fatal("Oracle kapalıyken sohbet kataloğunda olmamalı")
			}
		}
	}
}

func TestResolveOracleOperationToolResult(t *testing.T) {
	f := &fakeOracleOps{enabled: true, res: OracleOpResolution{Enabled: true, SpanKey: "FUNCTION_CODE",
		Matches: []OracleOpMatch{
			{Code: "SAMP01", SpanKey: "FUNCTION_CODE", Operation: "DEPOSIT_SAMPLE_INQUIRY_REST", Rows: 40, LatestTraceID: "t-1"},
			{Code: "SAMP03", SpanKey: "FUNCTION_CODE", Operation: "DEPOSIT_SAMPLE_INQUIRY_REST", Rows: 40, LatestTraceID: "t-1"},
		}, NearNames: []string{"DEPOSIT_SAMPLE_INQUIRY_REST_V2"}}}
	d := Deps{OracleOps: f}
	tool := toolByName(t, ToolList(d), OracleOperationToolName)
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":" DEPOSIT_SAMPLE_INQUIRY_REST "}`))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["name"] != "DEPOSIT_SAMPLE_INQUIRY_REST" || m["span_key"] != "FUNCTION_CODE" || m["rows"] != uint64(40) ||
		m["latest_trace_id"] != "t-1" || strings.Join(m["codes"].([]string), ",") != "SAMP01,SAMP03" ||
		len(m["near_names"].([]string)) != 1 || !strings.Contains(m["hint"].(string), "KOD BAŞINA") {
		t.Fatalf("gövde: %v", m)
	}
	in := false
	for _, tl := range ChatToolList(d) {
		in = in || tl.Name == OracleOperationToolName
	}
	if !in {
		t.Fatal("Oracle açıkken sohbet kataloğunda olmalı")
	}
	// Çözümleyici hatası olduğu gibi döner (sözleşmeyi mcp katmanı uygular).
	f.err = errors.New("code: 159, TIMEOUT_EXCEEDED")
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"DEPOSIT_SAMPLE_INQUIRY_REST"}`)); err == nil {
		t.Fatal("hata yutulmamalı")
	}
}

// İnceleme (v0.10.1133): harf varyantı isabetlerinin birleşimi ≤ OracleOpMaxCodes kod.
func TestResolveOracleOpHitsCapsCodes(t *testing.T) {
	hits := []chstore.OracleOpHit{
		{Operation: "ORDER_SAMPLE_SERVICE", FunctionCodes: []string{"SAMP01", "SAMP02", "SAMP03", "SAMP04"}},
		{Operation: "order_sample_service", FunctionCodes: []string{"SAMP04", "SAMP05", "SAMP06", "SAMP07"}},
	}
	r := ResolveOracleOpHits("ORDER_SAMPLE_SERVICE", "FUNCTION_CODE", hits)
	if got := strings.Join(r.Codes(), ","); got != "SAMP01,SAMP02,SAMP03,SAMP04,SAMP05" {
		t.Fatalf("kodlar = %s", got)
	}
}
