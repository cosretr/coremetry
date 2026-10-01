package mcp

// v0.10.994 — MCP 2026-07-28 (modern, el sıkışmasız) dönemi; sunucu çift
// dönemli. Beklentiler belirtimin kendisinden (modern.go başlığındaki
// sayfalar): alan adları, hata kodları ve HTTP durumları oradaki örneklerle
// birebir. Legacy yolun DEĞİŞMEDİĞİ ayrıca pinli.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const modernMetaJSON = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"ExampleClient","version":"1.0.0"},"io.modelcontextprotocol/clientCapabilities":{}}`

// postModern — başlıkları açıkça verilen POST. hdr değeri "" olan başlık
// GÖNDERİLMEZ (eksik başlık senaryoları).
func postModern(t *testing.T, ts *httptest.Server, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/mcp", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode (%d): %v", resp.StatusCode, err)
	}
	return resp.StatusCode, out
}

// modernHdr — yönteme uygun, geçerli başlık kümesi.
func modernHdr(method, name string) map[string]string {
	return map[string]string{HeaderProtocolVersion: ProtocolVersionModern, HeaderMethod: method, HeaderName: name}
}

func rpcErrCode(out map[string]any) int {
	e, _ := out["error"].(map[string]any)
	if e == nil {
		return 0
	}
	c, _ := e["code"].(float64)
	return int(c)
}

func modernTestServer(t *testing.T) (*Server, *httptest.Server) {
	srv, ts := testServer(t)
	srv.RegisterResource(Resource{URI: "coremetry://ping", Name: "ping", MimeType: "text/plain",
		Reader: func(context.Context, string) (string, error) { return "pong", nil }})
	srv.RegisterPrompt(Prompt{Name: "explain", Description: "test",
		Renderer: func(context.Context, map[string]string) ([]PromptMessage, error) { return nil, nil }})
	return srv, ts
}

// server/discover — belirtimdeki DiscoverResult şekli.
func TestModernDiscover(t *testing.T) {
	_, ts := modernTestServer(t)
	status, out := postModern(t, ts, `{"jsonrpc":"2.0","id":"discover-1","method":"server/discover","params":{`+modernMetaJSON+`}}`,
		modernHdr("server/discover", ""))
	if status != http.StatusOK || out["error"] != nil || out["id"] != "discover-1" {
		t.Fatalf("discover: %d %v", status, out)
	}
	res := out["result"].(map[string]any)
	if res["resultType"] != "complete" || res["cacheScope"] != "private" || res["ttlMs"] != float64(300000) {
		t.Errorf("zarf alanları: %v", res)
	}
	if got := res["supportedVersions"]; !reflect.DeepEqual(got, []any{"2026-07-28", "2025-03-26"}) {
		t.Errorf("supportedVersions: %v", got)
	}
	caps := res["capabilities"].(map[string]any)
	for _, k := range []string{"tools", "resources", "prompts"} {
		if _, ok := caps[k]; !ok {
			t.Errorf("capabilities.%s eksik: %v", k, caps)
		}
	}
	info := res["_meta"].(map[string]any)[MetaServerInfo].(map[string]any)
	if info["name"] != "coremetry-test" || info["version"] != "v0.0.0-test" {
		t.Errorf("serverInfo _meta'da olmalı: %v", res["_meta"])
	}
	// Denetim reçetesindeki YANLIŞ adlar üretilmez.
	if _, bad := res["protocolVersions"]; bad {
		t.Error("alan adı supportedVersions — protocolVersions değil")
	}
	if _, bad := res["serverInfo"]; bad {
		t.Error("serverInfo üst düzeyde değil, _meta içinde")
	}
}

func TestModernListsAndCalls(t *testing.T) {
	_, ts := modernTestServer(t)
	wantInfo := func(name string, res map[string]any) {
		t.Helper()
		meta, _ := res["_meta"].(map[string]any)
		if res["resultType"] != "complete" || meta == nil || meta[MetaServerInfo] == nil {
			t.Errorf("%s: resultType + serverInfo beklenir: %v", name, res)
		}
	}
	for _, c := range []struct {
		method string
		key    string
		ttl    float64
	}{
		{"tools/list", "tools", 300000},
		{"resources/list", "resources", 300000},
		{"resources/templates/list", "resourceTemplates", 300000},
		{"prompts/list", "prompts", 300000},
	} {
		status, out := postModern(t, ts, `{"jsonrpc":"2.0","id":1,"method":"`+c.method+`","params":{`+modernMetaJSON+`}}`, modernHdr(c.method, ""))
		if status != http.StatusOK || out["error"] != nil {
			t.Fatalf("%s: %d %v", c.method, status, out)
		}
		res := out["result"].(map[string]any)
		wantInfo(c.method, res)
		if _, ok := res[c.key]; !ok || res["ttlMs"] != c.ttl || res["cacheScope"] != "private" {
			t.Errorf("%s: %q + ttlMs/cacheScope beklenir: %v", c.method, c.key, res)
		}
	}

	// tools/call — Mcp-Name başlığı gövdedeki adla eşleşir.
	status, out := postModern(t, ts, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo_tool","arguments":{"x":1},`+modernMetaJSON+`}}`,
		modernHdr("tools/call", "echo_tool"))
	if status != http.StatusOK || out["error"] != nil {
		t.Fatalf("tools/call: %d %v", status, out)
	}
	res := out["result"].(map[string]any)
	wantInfo("tools/call", res)
	if _, has := res["ttlMs"]; has {
		t.Error("tools/call sonucu önbelleklenebilir değil: ttlMs olmamalı")
	}
	if c, _ := res["content"].([]any); len(c) != 1 {
		t.Errorf("content korunmalı: %v", res)
	}

	// Base64 nöbetçili Mcp-Name çözülerek karşılaştırılır.
	enc := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("echo_tool")) + "?="
	if status, out = postModern(t, ts, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo_tool",`+modernMetaJSON+`}}`,
		modernHdr("tools/call", enc)); status != http.StatusOK || out["error"] != nil {
		t.Errorf("base64 Mcp-Name: %d %v", status, out)
	}

	// resources/read — Mcp-Name = uri; canlı veri: ttlMs 0.
	status, out = postModern(t, ts, `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"coremetry://ping",`+modernMetaJSON+`}}`,
		modernHdr("resources/read", "coremetry://ping"))
	if status != http.StatusOK || out["error"] != nil {
		t.Fatalf("resources/read: %d %v", status, out)
	}
	if res = out["result"].(map[string]any); res["ttlMs"] != float64(0) || res["cacheScope"] != "private" || res["resultType"] != "complete" {
		t.Errorf("resources/read zarfı: %v", res)
	}

	// prompts/get — önbelleklenebilir değil.
	status, out = postModern(t, ts, `{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"explain",`+modernMetaJSON+`}}`,
		modernHdr("prompts/get", "explain"))
	if status != http.StatusOK || out["error"] != nil {
		t.Fatalf("prompts/get: %d %v", status, out)
	}
	if _, has := out["result"].(map[string]any)["ttlMs"]; has {
		t.Error("prompts/get sonucu önbelleklenebilir değil")
	}
}

// Reddedilen istekler: kod + HTTP durumu belirtimdeki gibi.
func TestModernRejections(t *testing.T) {
	_, ts := modernTestServer(t)
	call := func(name string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `",` + modernMetaJSON + `}}`
	}
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + modernMetaJSON + `}}`
	meta := func(version string, caps bool) string {
		m := `"io.modelcontextprotocol/protocolVersion":"` + version + `"`
		if caps {
			m += `,"io.modelcontextprotocol/clientCapabilities":{}`
		}
		return `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` + m + `}}}`
	}
	cases := []struct {
		name       string
		body       string
		hdr        map[string]string
		wantStatus int
		wantCode   int
		wantMsg    string
	}{
		{"clientCapabilities eksik → -32602 / 400", meta("2026-07-28", false), modernHdr("tools/list", ""), 400, ErrInvalidParams, MetaClientCapabilities},
		{"server/discover _meta'sız → -32602 / 400", `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`, modernHdr("server/discover", ""), 400, ErrInvalidParams, MetaProtocolVersion},
		{"MCP-Protocol-Version başlığı yok → -32020", list, map[string]string{HeaderMethod: "tools/list"}, 400, ErrHeaderMismatch, "MCP-Protocol-Version header is required"},
		{"başlık sürümü ≠ gövde → -32020", list, map[string]string{HeaderProtocolVersion: "2025-11-25", HeaderMethod: "tools/list"}, 400, ErrHeaderMismatch, "does not match body value '2026-07-28'"},
		{"Mcp-Method yok → -32020", list, map[string]string{HeaderProtocolVersion: ProtocolVersionModern}, 400, ErrHeaderMismatch, "Mcp-Method header is required"},
		{"Mcp-Method ≠ gövde → -32020", list, modernHdr("tools/call", ""), 400, ErrHeaderMismatch, "Mcp-Method header value 'tools/call' does not match body value 'tools/list'"},
		{"tools/call Mcp-Name yok → -32020", call("echo_tool"), modernHdr("tools/call", ""), 400, ErrHeaderMismatch, "Mcp-Name header is required"},
		{"Mcp-Name ≠ gövde → -32020 (belirtimin örnek metni)", call("bar"), modernHdr("tools/call", "foo"), 400, ErrHeaderMismatch, "Header mismatch: Mcp-Name header value 'foo' does not match body value 'bar'"},
		{"Mcp-Name bozuk base64 → -32020", call("echo_tool"), modernHdr("tools/call", "=?base64?!!!?="), 400, ErrHeaderMismatch, "not valid base64"},
		{"bilinmeyen sürüm → -32022 / 400", meta("1900-01-01", true), map[string]string{HeaderProtocolVersion: "1900-01-01", HeaderMethod: "tools/list"}, 400, ErrUnsupportedProtocolVersion, "Unsupported protocol version"},
		{"bilinmeyen yöntem → -32601 / 404", `{"jsonrpc":"2.0","id":1,"method":"nope/nope","params":{` + modernMetaJSON + `}}`, modernHdr("nope/nope", ""), 404, ErrMethodNotFound, "method not found"},
		{"initialize modern dönemde yok → 404", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` + modernMetaJSON + `}}`, modernHdr("initialize", ""), 404, ErrMethodNotFound, "method not found: initialize"},
		{"ping modern dönemde yok → 404", `{"jsonrpc":"2.0","id":1,"method":"ping","params":{` + modernMetaJSON + `}}`, modernHdr("ping", ""), 404, ErrMethodNotFound, "method not found: ping"},
		{"bilinmeyen tool → -32602 (yöntem biliniyor: 200)", call("ghost"), modernHdr("tools/call", "ghost"), 200, ErrInvalidParams, "tool not found: ghost"},
		{"bilinmeyen resource → -32602 (-32002 / -32601 değil)", `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"coremetry://nope",` + modernMetaJSON + `}}`, modernHdr("resources/read", "coremetry://nope"), 200, ErrInvalidParams, "resource not found"},
		{"bilinmeyen prompt → -32602", `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"nope",` + modernMetaJSON + `}}`, modernHdr("prompts/get", "nope"), 200, ErrInvalidParams, "prompt not found"},
	}
	for _, c := range cases {
		status, out := postModern(t, ts, c.body, c.hdr)
		e, _ := out["error"].(map[string]any)
		msg, _ := e["message"].(string)
		if status != c.wantStatus || rpcErrCode(out) != c.wantCode || !strings.Contains(msg, c.wantMsg) {
			t.Errorf("%s: durum %d kod %d mesaj %q (istenen %d / %d / %q)", c.name, status, rpcErrCode(out), msg, c.wantStatus, c.wantCode, c.wantMsg)
		}
		if out["result"] != nil {
			t.Errorf("%s: hata yanıtı result taşımamalı", c.name)
		}
	}

	// -32022'nin data'sı: supported + requested (belirtimdeki örnek şekli).
	_, out := postModern(t, ts, meta("1900-01-01", true), map[string]string{HeaderProtocolVersion: "1900-01-01", HeaderMethod: "tools/list"})
	data := out["error"].(map[string]any)["data"].(map[string]any)
	if !reflect.DeepEqual(data["supported"], []any{"2026-07-28", "2025-03-26"}) || data["requested"] != "1900-01-01" {
		t.Errorf("UnsupportedProtocolVersion data: %v", data)
	}
}

// Kapı modern yolda da aynı handler'da: rol / hız reddi ve tool hatası
// yürütme değişmeden gelir (yalnız zarf farklı).
func TestModernKeepsGateAndToolErrors(t *testing.T) {
	srv, ts := modernTestServer(t)
	srv.RegisterTool(Tool{Name: "boom", InputSchema: map[string]any{"type": "object"},
		Handler: func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("kaboom") }})
	body := func(name string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `",` + modernMetaJSON + `}}`
	}
	status, out := postModern(t, ts, body("boom"), modernHdr("tools/call", "boom"))
	res, _ := out["result"].(map[string]any)
	if status != http.StatusOK || res == nil || res["isError"] != true || res["resultType"] != "complete" {
		t.Fatalf("tool hatası isError sonuçtur (JSON-RPC hatası değil): %d %v", status, out)
	}
	srv.SetCallGate(func(context.Context, GateCall) error { return errors.New("rate limited: test") })
	if status, out = postModern(t, ts, body("echo_tool"), modernHdr("tools/call", "echo_tool")); status != http.StatusOK || rpcErrCode(out) != ErrRateLimited {
		t.Errorf("kapı reddi -32000 kalmalı: %d %v", status, out)
	}
}

// LEGACY DEĞİŞMEDİ: `_meta` taşımayan istek eski yolda; zarf alanları yok.
func TestLegacyPathUnchangedByModernEra(t *testing.T) {
	_, ts := modernTestServer(t)
	_, out := postStreamable(t, ts, rpc("initialize", 1, `{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"old","version":"1"}}`))
	res := out["result"].(map[string]any)
	if res["protocolVersion"] != ProtocolVersionStreamable || res["serverInfo"] == nil {
		t.Fatalf("legacy initialize: %v", out)
	}
	if _, has := res["resultType"]; has {
		t.Error("legacy initialize sonucu resultType taşımamalı (bayt bayt eski)")
	}
	_, out = postStreamable(t, ts, rpc("tools/list", 2, ""))
	res = out["result"].(map[string]any)
	for _, k := range []string{"resultType", "ttlMs", "cacheScope", "_meta"} {
		if _, has := res[k]; has {
			t.Errorf("legacy tools/list %q taşımamalı: %v", k, res)
		}
	}
	// progressToken gibi BAŞKA bir _meta anahtarı dönemi değiştirmez.
	_, out = postStreamable(t, ts, rpc("tools/list", 3, `{"_meta":{"progressToken":"p1"}}`))
	if _, has := out["result"].(map[string]any)["resultType"]; has {
		t.Error("yalnız protocolVersion anahtarı modern dönemi seçer")
	}
	// Legacy'de bilinmeyen tool -32601 kalır; ping çalışır.
	_, out = postStreamable(t, ts, rpc("tools/call", 4, `{"name":"ghost"}`))
	if rpcErrCode(out) != ErrMethodNotFound {
		t.Errorf("legacy bilinmeyen tool -32601: %v", out)
	}
	if _, out = postStreamable(t, ts, rpc("ping", 5, "")); out["error"] != nil {
		t.Errorf("legacy ping: %v", out)
	}
	// Legacy'de server/discover YOK değil: yöntem modern döneme aittir ve
	// `_meta`'sız çağrı -32602 alır (legacy istemci onu zaten göndermez).
	resp, out := postStreamable(t, ts, rpc("server/discover", 6, ""))
	if resp.StatusCode != http.StatusBadRequest || rpcErrCode(out) != ErrInvalidParams {
		t.Errorf("_meta'sız discover: %d %v", resp.StatusCode, out)
	}
}

// Batch modern dönemde tanımlı değil: modern eleman -32600, legacy eleman çalışır.
func TestModernRequestInBatchRejected(t *testing.T) {
	_, ts := modernTestServer(t)
	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + modernMetaJSON + `}},{"jsonrpc":"2.0","id":2,"method":"ping"}]`
	resp, err := http.Post(ts.URL+"/api/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out) != 2 {
		t.Fatalf("batch: %v %v", err, out)
	}
	if rpcErrCode(out[0]) != ErrInvalidRequest || out[1]["error"] != nil {
		t.Errorf("modern eleman -32600, legacy eleman başarı: %v", out)
	}
}

func TestModernPureHelpers(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"get_weather", "get_weather", true},
		{"=?base64?SGVsbG8sIOS4lueVjA==?=", "Hello, 世界", true}, // belirtimin örneği
		{"=?base64?IHBhZGRlZCA=?=", " padded ", true},
		{"=?base64?%%%?=", "", false},
		{"=?base64?", "=?base64?", true}, // nöbetçi tam değil → düz değer
	} {
		if got, ok := decodeHeaderValue(c.in); got != c.want || ok != c.ok {
			t.Errorf("decodeHeaderValue(%q) = %q, %v", c.in, got, ok)
		}
	}
	m := readModernMeta(json.RawMessage(`{` + modernMetaJSON + `}`))
	if !m.HasVersion || m.Version != "2026-07-28" || !m.HasCapabilities || m.Client.Name != "ExampleClient" {
		t.Errorf("readModernMeta: %+v", m)
	}
	for _, raw := range []string{``, `[]`, `{"_meta":null}`, `{"_meta":{"io.modelcontextprotocol/protocolVersion":7}}`, `{"_meta":{"io.modelcontextprotocol/clientCapabilities":"x"}}`} {
		if m := readModernMeta(json.RawMessage(raw)); m.HasVersion || m.HasCapabilities {
			t.Errorf("readModernMeta(%s) sıfır değer olmalı: %+v", raw, m)
		}
	}
	if isModernRequest(&Request{Method: "tools/list", Params: json.RawMessage(`{"_meta":{"progressToken":1}}`)}) ||
		!isModernRequest(&Request{Method: "server/discover"}) ||
		!isModernRequest(&Request{Method: "tools/list", Params: json.RawMessage(`{` + modernMetaJSON + `}`)}) {
		t.Error("dönem seçimi: yalnız protocolVersion anahtarı ya da server/discover")
	}
	// Belirtimin ayırdığı kodlar.
	if ErrHeaderMismatch != -32020 || ErrMissingClientCapability != -32021 || ErrUnsupportedProtocolVersion != -32022 {
		t.Error("hata kodları belirtimdeki değerlerde olmalı")
	}
}
