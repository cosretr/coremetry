// Package mcpclient — DIŞ MCP sunucularının istemcisi (v0.10.86,
// MCP istemci planı dilim ①: protokol + taşıma + kayıt defteri).
//
// internal/mcp Coremetry'nin KENDİ MCP sunucusudur (gelen yön); bu paket
// tam tersini konuşur: operatörün izin listesine yazdığı dış sunuculara
// bağlanır, tool kataloglarını çeker ve çağrı yapar. İki paket aynı
// JSON-RPC zarfını taşır ama sunucunun initialize/tools şekilleri
// unexported olduğu için istemci şekilleri burada yeniden tanımlı —
// bilinçli kopya: iki yön ayrı evrilir, paylaşım iki tarafı birbirine
// zincirlerdi.
//
// Dilim sınırı: burada AĞ ve PROTOKOL var. Ayar kalıcılığı (dilim ②),
// sohbet köprüsü + rol/bütçe/audit (dilim ③) ve selfobs span'leri
// (dilim ④) sonraki sürümlerde. api katmanına tek giriş Registry'dir.
//
// Güvenlik duruşu: sunucu listesi YALNIZ operatör ayarından gelir
// (izin listesi); bu paket kendi başına hiçbir adrese bağlanmaz.
//
// v0.10.995 — ÇİFT DÖNEMLİ istemci (dış skill denetimi 2026-09-19 M2; sunucu
// tarafı internal/mcp/modern.go, v0.10.994). MCP 2026-07-28 el sıkışmasını
// kaldırdı: modern sunucu `initialize`'ı tanımaz, her istek sürümünü ve
// istemci yeteneklerini `_meta`'da taşır. Eski istemci böyle bir sunucuda
// initialize'da düşüyor, ListTools / CallTool hiç çalışmıyordu. Artık:
//
//   - Initialize önce `server/discover` ile YOKLAR (belirtim: stdio ›
//     Backward Compatibility; HTTP'de "modern isteği önce dene"). Üç sonuç:
//     DiscoverResult ve 2026-07-28 destekli → MODERN (el sıkışma yok); tanınan
//     modern hata (-32022 UnsupportedProtocolVersion) → sunucu modern, ilan
//     ettiği sürümlerden konuşabildiğimiz varsa ona geçilir, initialize'a
//     KÖRLEMESİNE düşülmez; başka her hata ya da zaman aşımı → LEGACY
//     (initialize + initialized). Düşüş TEK bir hata koduna bağlı değildir —
//     eski sunucular bilinmeyen yönteme -32601, -32602 ya da hiç yanıt verir.
//   - Modern kipte her istek `_meta` (protocolVersion, clientInfo,
//     clientCapabilities) ve HTTP'de `MCP-Protocol-Version` / `Mcp-Method` /
//     `Mcp-Name` başlıklarını taşır; sonuçta `resultType` okunur (yok =
//     "complete"; "input_required" desteklenmez ve AÇIK hata olur).
//   - Legacy kipte davranış aynı; tek ek: initialize'ın döndürdüğü sürüm
//     sonraki HTTP isteklerinde `MCP-Protocol-Version` başlığına yazılır
//     (2025-06-18+ sunucular bekler).
//
// Dönem sunucunun özelliğidir: istemci nesnesi boyunca bir kez belirlenir
// (Registry aynı istemciyi yeniden kullanır).
package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

// protocolVersion — LEGACY el sıkışmasında ilan edilen MCP sürümü.
const protocolVersion = "2024-11-05"

// modernProtocolVersion — el sıkışmasız dönemin konuşulan sürümü (v0.10.995).
const modernProtocolVersion = "2026-07-28"

// `_meta` anahtarları, istek başlıkları ve modern hata kodları (belirtim
// 2026-07-28; internal/mcp/modern.go ile aynı değerler — bilinçli kopya,
// paket başlığındaki gerekçe).
const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"

	headerProtocolVersion = "MCP-Protocol-Version"
	headerMethod          = "Mcp-Method"
	headerName            = "Mcp-Name"

	rpcHeaderMismatch          = -32020
	rpcMissingClientCapability = -32021
	rpcUnsupportedVersion      = -32022
)

// errAuthRejected — sunucu kimliği reddetti (http 401/403). Yoklama bunu
// "legacy sunucu" saymaz: token yanlışken initialize'a düşmek aynı hatayı
// ikinci kez üretir ve nedeni gizler.
var errAuthRejected = errors.New("mcp sunucusu kimliği reddetti")

// callTimeout — TEK tool çağrısının istemci tavanı. Sohbet döngüsünün
// runChatTool 20s tavanından BİLİNÇLİ kısa: dış sunucu ağ gecikmesi o
// bütçeyi yerken pay kalsın; hangi tavanın dolduğu hata metninden
// okunabilsin.
const callTimeout = 15 * time.Second

// listToolsPageCap / listToolsCap — katalog sayfalama tavanları.
// Kaçak bir sunucu sonsuz nextCursor döndürebilir; tavana çarpınca
// kalan sayfalar ATILIR ve bu sessiz kalmaz (Client.ListTools notu).
const (
	listToolsPageCap = 10
	listToolsCap     = 200
)

// ServerConfig — tek dış sunucunun yapılandırması. Kalıcılık dilim ②'de
// devops Settings şablonuyla gelir; şekil şimdiden burada ki dilimler
// arasında tip kaymasın.
type ServerConfig struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"` // "http" | "stdio"
	URL       string   `json:"url,omitempty"`
	Token     string   `json:"token,omitempty"` // http: Authorization Bearer
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	Enabled   bool     `json:"enabled"`
	// AllowTools / DenyTools — tool bazlı süzgeç; UYGULAMASI dilim ③'te
	// (rol kapısıyla aynı yerde). Şekil burada ki ayar blob'u tek sefer
	// tanımlansın.
	AllowTools []string `json:"allowTools,omitempty"`
	DenyTools  []string `json:"denyTools,omitempty"`
	// InsecureSkipVerify — banka içi self-signed uçlar için; devops
	// istemcisiyle aynı bayrak.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
	// Env (v0.10.803, dış skill denetimi M3 — agents-mcp "scoped env vars"):
	// stdio alt sürecine verilen ortam. Alt süreç Coremetry'nin ortamını
	// MİRAS ALMAZ (AI anahtarı, CH/ES kimliği, JWT sırrı geçmez); yalnız
	// stdioBaseEnv allowlist'i + buradakiler. Değerler sır gibi saklanır:
	// snapshot yalnız anahtarları döndürür, PUT'ta "********" saklıyı korur.
	Env map[string]string `json:"env,omitempty"`
}

// Transport — iki taşımanın (stdio, streamable HTTP) ortak yüzü.
//
// Call bir istek/yanıt turu; Notify tek yönlü bildirim (initialized).
// Notifications, SUNUCUDAN gelen bildirimlerin metot adlarını taşır —
// kanal dolarsa bildirim DÜŞER (tamponlu, bloklamaz): bildirimler
// yalnız önbellek tazeleme ipucudur, TTL zaten emniyet ağıdır.
type Transport interface {
	Call(ctx context.Context, method string, params, result any) error
	Notify(ctx context.Context, method string, params any) error
	Notifications() <-chan string
	Close() error
}

// notifyListChanged — kataloğu bayatlatan bildirim.
const notifyListChanged = "notifications/tools/list_changed"

// ── JSON-RPC zarfı (istemci yönü) ───────────────────────────────────────

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp sunucu hatası %d: %s", e.Code, e.Message)
}

// rpcEnvelope — istek + yanıt + bildirim tek şekilde okunur; alanların
// hangisinin dolu olduğu türü söyler (ID'siz method = bildirim).
type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// ── İstemci ─────────────────────────────────────────────────────────────

// ToolDef — dış sunucunun ilan ettiği tool. InputSchema aynen taşınır:
// provider.ToolSpec.InputSchema ile 1:1 (köprü dilim ③).
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Client — tek sunucuya bağlı ince istemci. Taşıma enjekte edilir;
// testler sahte Transport ile protokol katmanını ağsız sürer.
type Client struct {
	tr          Transport
	initialized bool
	// server — span attribute'u için sunucu adı; boş olabilir (test).
	server string
	// modern (v0.10.995) — sunucu 2026-07-28 konuşuyor: el sıkışma yok, her
	// istek `_meta` + başlık taşır. false = legacy (initialize yapıldı).
	modern bool
	// legacyVersion — initialize'ın döndürdüğü sürüm; legacy HTTP isteklerinin
	// MCP-Protocol-Version başlığı. Boş = başlık gönderilmez (eski davranış).
	legacyVersion string
}

// ── HTTP başlıklarının taşıma katmanına inişi ───────────────────────────

// mcpHeadersKey — Client'ın bir çağrı için istediği HTTP başlıkları ctx ile
// iner; Transport arayüzü değişmez (stdio'da başlık katmanı yok, yok sayar).
type mcpHeadersKey struct{}

func withMCPHeaders(ctx context.Context, h map[string]string) context.Context {
	if len(h) == 0 {
		return ctx
	}
	return context.WithValue(ctx, mcpHeadersKey{}, h)
}

func mcpHeadersFrom(ctx context.Context) map[string]string {
	h, _ := ctx.Value(mcpHeadersKey{}).(map[string]string)
	return h
}

// encodeHeaderValue — SAF: `Mcp-Name` değeri düz ASCII başlık değeri olarak
// güvenle taşınamıyorsa (ASCII dışı, kontrol karakteri, baş/son boşluk) ya da
// nöbetçi kalıbına benziyorsa `=?base64?…?=` (belirtim: Value Encoding).
func encodeHeaderValue(v string) string {
	const pre, suf = "=?base64?", "?="
	safe := v != "" && v == strings.TrimSpace(v) && !(strings.HasPrefix(v, pre) && strings.HasSuffix(v, suf))
	if safe {
		for i := 0; i < len(v); i++ {
			if c := v[i]; c != ' ' && (c < 0x21 || c > 0x7e) {
				safe = false
				break
			}
		}
	}
	if safe {
		return v
	}
	return pre + base64.StdEncoding.EncodeToString([]byte(v)) + suf
}

// modernHeaders — SAF: modern isteğin HTTP başlıkları. name yalnız
// tools/call, prompts/get (params.name) ve resources/read (params.uri) için.
func modernHeaders(method, name string) map[string]string {
	h := map[string]string{headerProtocolVersion: modernProtocolVersion, headerMethod: method}
	if name != "" {
		h[headerName] = encodeHeaderValue(name)
	}
	return h
}

// modernParams — SAF: params'ın `_meta` eklenmiş KOPYASI (çağıranın haritası
// değişmez). Zorunlu iki alan + kimlik.
func modernParams(params map[string]any) map[string]any {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	out["_meta"] = map[string]any{
		metaProtocolVersion:    modernProtocolVersion,
		metaClientInfo:         map[string]any{"name": "coremetry", "version": "1"},
		metaClientCapabilities: map[string]any{},
	}
	return out
}

// checkResultType — SAF: modern sonucun `resultType`'ı. Yok ya da "complete"
// → tamam (eski sürüm sunucuları alanı taşımaz). "input_required" (MRTR:
// sunucu sampling / elicitation istiyor) bu istemcide DESTEKLENMEZ ve sessizce
// boş sonuç sayılmaz; bilinmeyen değer belirtim gereği geçersizdir.
func checkResultType(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var r struct {
		ResultType string `json:"resultType"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return nil // nesne olmayan sonuç: tür alanı yok
	}
	switch r.ResultType {
	case "", "complete":
		return nil
	case "input_required":
		return errors.New("mcp sunucusu ek girdi istedi (resultType input_required: sampling / elicitation) — Coremetry istemcisi desteklemiyor")
	}
	return fmt.Errorf("mcp sunucusu bilinmeyen resultType döndürdü: %q", r.ResultType)
}

// isModernRPCError — hata, belirtimin modern döneme ayırdığı kodlardan biri mi.
func isModernRPCError(err error) (*rpcError, bool) {
	var re *rpcError
	if !errors.As(err, &re) {
		return nil, false
	}
	switch re.Code {
	case rpcHeaderMismatch, rpcMissingClientCapability, rpcUnsupportedVersion:
		return re, true
	}
	return nil, false
}

// eraOf — SAF: yoklamanın (server/discover) sonucundan dönem kararı.
// supported: DiscoverResult.supportedVersions ya da -32022'nin data.supported
// listesi. Döner: modern mi; legacy el sıkışmasına düşülsün mü; ikisi de
// değilse err (sunucu modern ama ortak sürüm yok / isteğimizi reddetti).
func eraOf(supported []string, probeErr error) (modern, legacy bool, err error) {
	usable := func(vs []string) (bool, bool) {
		m, l := false, false
		for _, v := range vs {
			switch {
			case v == modernProtocolVersion:
				m = true
			case v < modernProtocolVersion: // tarih biçimli sürümler sözlük sırasıyla kıyaslanır
				l = true
			}
		}
		return m, l
	}
	if probeErr == nil {
		if m, _ := usable(supported); m {
			return true, false, nil
		}
		// Sürüm listesi yok (era-belirsiz bir legacy sunucu boş sonuç döndü)
		// ya da yalnız el sıkışmalı sürümler var → legacy.
		return false, true, nil
	}
	if errors.Is(probeErr, errAuthRejected) {
		return false, false, probeErr
	}
	re, ok := isModernRPCError(probeErr)
	if !ok {
		return false, true, nil // tanınmayan her hata / zaman aşımı: legacy sunucu
	}
	if re.Code != rpcUnsupportedVersion {
		return false, false, fmt.Errorf("mcp sunucusu modern isteği reddetti: %w", probeErr)
	}
	var data struct {
		Supported []string `json:"supported"`
	}
	_ = json.Unmarshal(re.Data, &data)
	switch m, l := usable(data.Supported); {
	case m:
		return true, false, nil
	case l:
		return false, true, nil
	}
	return false, false, fmt.Errorf("mcp sunucusu %s sürümünü desteklemiyor (desteklediği: %s) — ortak sürüm yok",
		modernProtocolVersion, strings.Join(data.Supported, ", "))
}

func NewClient(tr Transport) *Client { return &Client{tr: tr} }

// NewNamedClient — span'lere sunucu adını taşıyan kurucu (dilim ④).
// Registry ve Test probu bunu kullanır; adsız NewClient testlerde kalır.
func NewNamedClient(server string, tr Transport) *Client {
	return &Client{tr: tr, server: server}
}

// startSpan — giden MCP çağrısının selfobs span'i (v0.10.89, dilim ④).
//
// Depoda BUGÜNE DEK hiçbir dış HTTP istemcisi span'lenmiyordu (keşif
// bulgusu: otelhttp.NewTransport 0 kullanım); MCP çağrısı modelin cevap
// süresine DOĞRUDAN girer, yani "sohbet niye 12 sn" sorusunun cevabı
// tam buradadır. selfobs kapalıyken Tracer() noop döner — bedel sıfır
// (traced_conn deseni). Hata mesajı SafeAttr'dan geçer.
func (c *Client) startSpan(ctx context.Context, op string, extra ...attribute.KeyValue) (context.Context, func(error)) {
	ctx, span := selfobs.Tracer().Start(ctx, op)
	span.SetAttributes(append([]attribute.KeyValue{
		attribute.String("mcp.server", c.server),
	}, extra...)...)
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(fmt.Errorf("%s", selfobs.SafeAttr(err.Error())))
			span.SetStatus(codes.Error, selfobs.SafeAttr(err.Error()))
		}
		span.End()
	}
}

// Initialize — dönem yoklaması + (legacy sunucuda) el sıkışma ve initialized
// bildirimi. İkinci çağrı no-op: Registry tembel başlatır ve aynı istemciyi
// yeniden kullanır. v0.10.995: önce server/discover (paket başlığı).
func (c *Client) Initialize(ctx context.Context) (err error) {
	if c.initialized {
		return nil
	}
	ctx, end := c.startSpan(ctx, "mcpclient.initialize")
	defer func() { end(err) }()

	var disc struct {
		SupportedVersions []string `json:"supportedVersions"`
	}
	pctx, pcancel := context.WithTimeout(ctx, callTimeout)
	probeErr := c.tr.Call(withMCPHeaders(pctx, modernHeaders("server/discover", "")), "server/discover", modernParams(nil), &disc)
	pcancel()
	if ctx.Err() != nil {
		return fmt.Errorf("server/discover: %w", ctx.Err()) // çağıran vazgeçti: düşüş denenmez
	}
	modern, legacy, eerr := eraOf(disc.SupportedVersions, probeErr)
	switch {
	case eerr != nil:
		return eerr
	case modern:
		c.modern, c.initialized = true, true
		return nil
	case !legacy:
		return errors.New("mcp sunucusunun dönemi belirlenemedi")
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "coremetry", "version": "1"},
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := c.tr.Call(ctx, "initialize", params, &res); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	// Sürüm uyuşmazlığı KAPI değil, not: MCP sürümleri geriye uyumlu
	// evriliyor ve sert kapı her yeni sunucu sürümünde operatörü
	// kilitlemek olurdu. Uyumsuzluk gerçek bir kırılım üretirse hata
	// zaten tools/list'te görünür.
	// v0.10.995 — sunucunun döndürdüğü sürüm sonraki HTTP isteklerinin
	// MCP-Protocol-Version başlığıdır (2025-06-18+; yoksa başlık gönderilmez).
	c.legacyVersion = res.ProtocolVersion
	if err := c.tr.Notify(c.legacyCtx(ctx), "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("initialized bildirimi: %w", err)
	}
	c.initialized = true
	return nil
}

// legacyCtx — legacy kipte müzakere edilen sürümü başlık olarak taşıyan ctx.
func (c *Client) legacyCtx(ctx context.Context) context.Context {
	if c.legacyVersion == "" {
		return ctx
	}
	return withMCPHeaders(ctx, map[string]string{headerProtocolVersion: c.legacyVersion})
}

// call — tek istek/yanıt turu, döneme göre. Legacy: params ve result aynen
// (v0.10.86 davranışı) + sürüm başlığı. Modern: `_meta` + başlıklar, sonra
// resultType denetimi. name = Mcp-Name'in kaynağı (tool adı); listelerde "".
func (c *Client) call(ctx context.Context, method, name string, params map[string]any, result any) error {
	if !c.modern {
		return c.tr.Call(c.legacyCtx(ctx), method, params, result)
	}
	var raw json.RawMessage
	if err := c.tr.Call(withMCPHeaders(ctx, modernHeaders(method, name)), method, modernParams(params), &raw); err != nil {
		return err
	}
	if err := checkResultType(raw); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if result != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, result); err != nil {
			return fmt.Errorf("mcp %s: yanıt çözümlenemedi: %w", method, err)
		}
	}
	return nil
}

// ListTools — katalog; nextCursor sayfalarını tavana kadar toplar.
// Tavana çarpıldıysa truncated=true döner — sessiz kesme yok.
func (c *Client) ListTools(ctx context.Context) (tools []ToolDef, truncated bool, err error) {
	if err := c.Initialize(ctx); err != nil {
		return nil, false, err
	}
	ctx, end := c.startSpan(ctx, "mcpclient.list_tools")
	defer func() { end(err) }()
	cursor := ""
	for page := 0; page < listToolsPageCap; page++ {
		cctx, cancel := context.WithTimeout(ctx, callTimeout)
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Tools      []ToolDef `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		callErr := c.call(cctx, "tools/list", "", params, &res)
		cancel()
		if callErr != nil {
			return nil, false, fmt.Errorf("tools/list: %w", callErr)
		}
		tools = append(tools, res.Tools...)
		if len(tools) >= listToolsCap {
			return tools[:listToolsCap], true, nil
		}
		if res.NextCursor == "" {
			return tools, false, nil
		}
		cursor = res.NextCursor
	}
	return tools, true, nil
}

// CallTool — tek çağrı. Dönen metin content[].text parçalarının
// birleşimidir; metin-dışı parça atlanır ve ATLANDIĞI SÖYLENİR (model
// eksik kanıtı tam sanmasın). isError sunucunun kendi bayrağıdır —
// taşıma hatası değil, tool'un "başarısız sonuç" sözleşmesi; ikisini
// ayırmak dilim ③'ün ToolErrorJSON köprüsüne lazım.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error) {
	if err := c.Initialize(ctx); err != nil {
		return "", false, err
	}
	ctx, end := c.startSpan(ctx, "mcpclient.call",
		attribute.String("mcp.tool", name))
	defer func() { end(err) }()
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", name, params, &res); err != nil {
		return "", false, err
	}
	var sb strings.Builder
	skipped := 0
	for _, part := range res.Content {
		if part.Type == "text" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(part.Text)
			continue
		}
		skipped++
	}
	if skipped > 0 {
		fmt.Fprintf(&sb, "\n[%d metin-dışı içerik parçası atlandı]", skipped)
	}
	return sb.String(), res.IsError, nil
}

// Close — taşımayı kapatır (stdio'da süreci indirir).
func (c *Client) Close() error { return c.tr.Close() }
