package mcpclient

// v0.10.995 — çift dönemli istemci (dış skill denetimi M2). Modern yol
// Coremetry'nin KENDİ çift dönemli sunucusuna (internal/mcp, v0.10.994) karşı
// uçtan uca sürülür: sunucu başlık ↔ gövde doğrulaması yaptığı için istemcinin
// `_meta` ve başlıkları yanlışsa çağrı 400 alır. Legacy düşüş sahte sunucuyla.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// modernServer — gerçek dual-era sunucu + gelen yöntem/başlık kaydı.
type modernServer struct {
	mu      sync.Mutex
	methods []string
	names   []string // Mcp-Name başlıkları
	srv     *httptest.Server
}

func newModernServer(t *testing.T) *modernServer {
	t.Helper()
	m := mcp.New("coremetry-test", "v0.0.0-test")
	echo := func(name string) mcp.Tool {
		return mcp.Tool{Name: name, Description: "yankı", InputSchema: map[string]any{"type": "object"},
			Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
				return map[string]any{"tool": name, "args": string(raw)}, nil
			}}
	}
	m.RegisterTool(echo("echo_tool"))
	m.RegisterTool(echo("ölçüm ara")) // ASCII dışı + boşluk: Mcp-Name base64 nöbetçisiyle gider
	ms := &modernServer{}
	ms.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ms.mu.Lock()
		ms.methods = append(ms.methods, r.Header.Get(headerMethod))
		ms.names = append(ms.names, r.Header.Get(headerName))
		ms.mu.Unlock()
		m.HandleStreamable(w, r)
	}))
	t.Cleanup(ms.srv.Close)
	return ms
}

func (ms *modernServer) client() *Client {
	return NewClient(newHTTPTransport(ServerConfig{URL: ms.srv.URL}, ms.srv.Client()))
}

func TestModernServerEndToEnd(t *testing.T) {
	ms := newModernServer(t)
	cl := ms.client()
	ctx := context.Background()

	tools, trunc, err := cl.ListTools(ctx)
	if err != nil || trunc || len(tools) != 2 {
		t.Fatalf("ListTools: %v trunc=%v %+v", err, trunc, tools)
	}
	if !cl.modern || cl.legacyVersion != "" {
		t.Fatalf("sunucu modern tanınmalı: modern=%v legacy=%q", cl.modern, cl.legacyVersion)
	}
	text, isErr, err := cl.CallTool(ctx, "echo_tool", json.RawMessage(`{"x":1}`))
	if err != nil || isErr || !strings.Contains(text, `"tool":"echo_tool"`) || !strings.Contains(text, `x`) {
		t.Fatalf("CallTool: %q isErr=%v err=%v", text, isErr, err)
	}
	// ASCII dışı ad: başlık base64, sunucu çözüp gövdeyle eşleştirir.
	if text, _, err = cl.CallTool(ctx, "ölçüm ara", nil); err != nil || !strings.Contains(text, "ölçüm ara") {
		t.Fatalf("ASCII dışı tool adı: %q err=%v", text, err)
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()
	// El sıkışma YOK: initialize / initialized hiç gitmez; yoklama bir kez.
	if got := strings.Join(ms.methods, ","); got != "server/discover,tools/list,tools/call,tools/call" {
		t.Errorf("modern sunucuda istek sırası: %s", got)
	}
	if ms.names[2] != "echo_tool" || !strings.HasPrefix(ms.names[3], "=?base64?") || ms.names[0] != "" || ms.names[1] != "" {
		t.Errorf("Mcp-Name başlıkları: %q", ms.names)
	}
}

// Bilinmeyen tool modern sunucuda -32602 JSON-RPC hatasıdır (HTTP 200); hata
// operatöre kod + mesajla ulaşır.
func TestModernUnknownToolSurfacesRPCError(t *testing.T) {
	cl := newModernServer(t).client()
	_, _, err := cl.CallTool(context.Background(), "ghost", nil)
	var re *rpcError
	if !errors.As(err, &re) || re.Code != -32602 || !strings.Contains(re.Message, "tool not found") {
		t.Fatalf("-32602 beklenir: %v", err)
	}
}

// scriptedServer — server/discover'a betiklenmiş yanıt veren, gerisinde
// legacy sahte sunucu gibi davranan uç.
func scriptedServer(t *testing.T, discover func(w http.ResponseWriter, id int64)) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env rpcEnvelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		mu.Lock()
		calls = append(calls, env.Method)
		mu.Unlock()
		if env.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch env.Method {
		case "server/discover":
			discover(w, *env.ID)
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26"}}`, *env.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[]}}`, *env.ID)
		case "tools/call":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"input_required","inputRequests":{}}}`, *env.ID)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestEraProbeOutcomes(t *testing.T) {
	unsupported := func(supported string) func(http.ResponseWriter, int64) {
		return func(w http.ResponseWriter, id int64) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32022,"message":"Unsupported protocol version","data":{"supported":[%s],"requested":"2026-07-28"}}}`, id, supported)
		}
	}
	cases := []struct {
		name      string
		discover  func(http.ResponseWriter, int64)
		wantErr   string
		wantCalls string
		modern    bool
	}{
		{"400 + gövdesiz (legacy sunucu) → initialize", func(w http.ResponseWriter, _ int64) { w.WriteHeader(http.StatusBadRequest) },
			"", "server/discover,initialize,notifications/initialized,tools/list", false},
		{"-32602 (legacy: initialize'dan önce istek) → initialize", func(w http.ResponseWriter, id int64) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32602,"message":"not initialized"}}`, id)
		}, "", "server/discover,initialize,notifications/initialized,tools/list", false},
		{"boş sonuç (era-belirsiz legacy) → initialize", func(w http.ResponseWriter, id int64) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, id)
		}, "", "server/discover,initialize,notifications/initialized,tools/list", false},
		{"DiscoverResult + 2026-07-28 → modern, el sıkışma yok", func(w http.ResponseWriter, id int64) {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"resultType":"complete","supportedVersions":["2026-07-28"],"capabilities":{}}}`, id)
		}, "", "server/discover,tools/list", true},
		{"-32022, ortak sürüm el sıkışmalı → initialize", unsupported(`"2027-01-01","2025-03-26"`),
			"", "server/discover,initialize,notifications/initialized,tools/list", false},
		{"-32022, ortak sürüm yok → açık hata, initialize'a düşülmez", unsupported(`"2027-01-01"`),
			"ortak sürüm yok", "server/discover", false},
		{"-32020 (isteğimiz reddedildi) → hata, düşüş yok", func(w http.ResponseWriter, id int64) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32020,"message":"Header mismatch"}}`, id)
		}, "modern isteği reddetti", "server/discover", false},
	}
	for _, c := range cases {
		srv, calls := scriptedServer(t, c.discover)
		cl := NewClient(newHTTPTransport(ServerConfig{URL: srv.URL}, srv.Client()))
		_, _, err := cl.ListTools(context.Background())
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: beklenmeyen hata %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: hata %q beklenir, gelen %v", c.name, c.wantErr, err)
		}
		if got := strings.Join(*calls, ","); got != c.wantCalls {
			t.Errorf("%s: istek sırası %s (istenen %s)", c.name, got, c.wantCalls)
		}
		if cl.modern != c.modern {
			t.Errorf("%s: modern=%v", c.name, cl.modern)
		}
	}
}

// Kimlik reddi yoklamada "legacy" sayılmaz: tek istek, tek biçim hata.
func TestProbeAuthRejectedDoesNotFallBack(t *testing.T) {
	f := newFakeMCPHTTP(t)
	f.authTok = "gizli"
	cl := NewClient(newHTTPTransport(ServerConfig{URL: f.srv.URL, Token: "yanlış"}, f.srv.Client()))
	_, _, err := cl.ListTools(context.Background())
	if err == nil || !errors.Is(err, errAuthRejected) || !strings.Contains(err.Error(), "kimliği reddetti (http 401) — token'ı Ayarlar'dan kontrol edin") {
		t.Fatalf("tek biçim kimlik hatası: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits != 1 {
		t.Errorf("kimlik reddinde initialize'a düşülmemeli: %d istek", f.hits)
	}
}

// input_required (MRTR) sessizce boş sonuç sayılmaz.
func TestModernInputRequiredIsAnError(t *testing.T) {
	srv, _ := scriptedServer(t, func(w http.ResponseWriter, id int64) {
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["2026-07-28"]}}`, id)
	})
	cl := NewClient(newHTTPTransport(ServerConfig{URL: srv.URL}, srv.Client()))
	_, _, err := cl.CallTool(context.Background(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "input_required") {
		t.Fatalf("input_required açık hata olmalı: %v", err)
	}
}

func TestEraOf(t *testing.T) {
	rpc := func(code int, data string) error {
		return &rpcError{Code: code, Message: "x", Data: json.RawMessage(data)}
	}
	cases := []struct {
		name      string
		supported []string
		err       error
		modern    bool
		legacy    bool
		wantErr   bool
	}{
		{"discover: modern sürüm var", []string{"2026-07-28", "2025-03-26"}, nil, true, false, false},
		{"discover: yalnız el sıkışmalı sürümler", []string{"2025-11-25"}, nil, false, true, false},
		{"discover: liste yok", nil, nil, false, true, false},
		{"-32601 → legacy", nil, rpc(-32601, ``), false, true, false},
		{"zaman aşımı → legacy", nil, context.DeadlineExceeded, false, true, false},
		{"taşıma hatası → legacy", nil, errors.New("mcp http 400: Bad Request"), false, true, false},
		{"-32022 + modern destekli (yeniden dene)", nil, rpc(-32022, `{"supported":["2026-07-28"]}`), true, false, false},
		{"-32022 + legacy destekli", nil, rpc(-32022, `{"supported":["2099-01-01","2024-11-05"]}`), false, true, false},
		{"-32022 + ortak sürüm yok", nil, rpc(-32022, `{"supported":["2099-01-01"]}`), false, false, true},
		{"-32022 + data yok", nil, rpc(-32022, ``), false, false, true},
		{"-32021 → hata", nil, rpc(-32021, ``), false, false, true},
		{"kimlik reddi → hata", nil, fmt.Errorf("%w (http 403)", errAuthRejected), false, false, true},
	}
	for _, c := range cases {
		m, l, err := eraOf(c.supported, c.err)
		if m != c.modern || l != c.legacy || (err != nil) != c.wantErr {
			t.Errorf("%s: modern=%v legacy=%v err=%v", c.name, m, l, err)
		}
	}
}

func TestModernRequestShaping(t *testing.T) {
	in := map[string]any{"name": "x"}
	out := modernParams(in)
	if _, mutated := in["_meta"]; mutated || out["name"] != "x" {
		t.Fatalf("çağıranın haritası değişmemeli: in=%v out=%v", in, out)
	}
	meta := out["_meta"].(map[string]any)
	if meta[metaProtocolVersion] != "2026-07-28" || meta[metaClientCapabilities] == nil || meta[metaClientInfo] == nil {
		t.Errorf("zorunlu _meta alanları: %v", meta)
	}
	if raw, _ := json.Marshal(modernParams(nil)); !strings.Contains(string(raw), `"io.modelcontextprotocol/clientCapabilities":{}`) {
		t.Errorf("clientCapabilities boş NESNE olmalı: %s", raw)
	}
	if got := modernHeaders("tools/list", ""); !reflect.DeepEqual(got, map[string]string{headerProtocolVersion: "2026-07-28", headerMethod: "tools/list"}) {
		t.Errorf("liste başlıkları: %v", got)
	}
	for _, c := range []struct{ in, want string }{
		{"get_weather", "get_weather"},
		{"file:///projects/app/config.json", "file:///projects/app/config.json"},
		{"Hello, 世界", "=?base64?SGVsbG8sIOS4lueVjA==?="}, // belirtimin örneği
		{" padded ", "=?base64?IHBhZGRlZCA=?="},
		{"line1\nline2", "=?base64?bGluZTEKbGluZTI=?="},
		{"=?base64?literal?=", "=?base64?PT9iYXNlNjQ/bGl0ZXJhbD89?="},
	} {
		if got := encodeHeaderValue(c.in); got != c.want {
			t.Errorf("encodeHeaderValue(%q) = %q, istenen %q", c.in, got, c.want)
		}
	}
	for raw, wantErr := range map[string]bool{
		``: false, `{}`: false, `{"resultType":"complete"}`: false, `[]`: false,
		`{"resultType":"input_required"}`: true, `{"resultType":"mystery"}`: true,
	} {
		if err := checkResultType(json.RawMessage(raw)); (err != nil) != wantErr {
			t.Errorf("checkResultType(%s) err=%v", raw, err)
		}
	}
}
