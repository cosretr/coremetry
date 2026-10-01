package mcp

// modern.go — v0.10.994 — MCP 2026-07-28 ("modern") dönemi: sunucu artık ÇİFT
// DÖNEMLİ (dual-era). Dış skill denetimi 2026-09-19 M1.
//
// 2026-07-28 belirtimi el sıkışmasını kaldırdı: `initialize` /
// `notifications/initialized` yok, her istek sürümünü ve istemci yeteneklerini
// `params._meta` içinde taşır ve sunucu ZORUNLU `server/discover` ile
// desteklediği sürümleri ilan eder. Belirtim (birincil kaynak, 2026-10-01'de
// okundu): modelcontextprotocol.io/specification/2026-07-28/ — changelog,
// basic (Statelessness, `_meta`, Error Codes), basic/versioning,
// server/discover, basic/transports/streamable-http.
//
// ⚠ Denetim raporundaki M1 reçetesi alan adlarında YANLIŞTI
// (`{protocolVersions:[…]}`, `_meta.protocolVersion`); doğrusu aşağıdaki
// sabitler: `supportedVersions` ve ad alanlı `io.modelcontextprotocol/…`
// anahtarları. Rapor düzeltildi.
//
// ── Dönem seçimi (versioning › "A dual-era server selects its behavior
// from how the client opens") ──────────────────────────────────────────────
//
//   - İstek gövdesi `params._meta["io.modelcontextprotocol/protocolVersion"]`
//     taşıyorsa (ya da yöntem yalnız modern dönemde var olan
//     `server/discover` ise) MODERN: durumsuz, bu dosya.
//   - Aksi hâlde LEGACY: `initialize` el sıkışması ve mcp.go'daki dispatch —
//     bayt bayt eski davranış (2025-03-26 Streamable, 2024-11-05 SSE).
//     Eski istemciler (Claude Code'un bugünkü `--transport http` yolu dahil)
//     hiçbir fark görmez.
//
// ── Modern istekte sunucunun yaptıkları ────────────────────────────────────
//
//  1. Zorunlu `_meta` alanları: protocolVersion + clientCapabilities. Eksikse
//     -32602 ve HTTP 400.
//  2. Başlık doğrulaması (streamable-http › Server Validation):
//     `MCP-Protocol-Version` gövdedeki sürümle, `Mcp-Method` yöntemle,
//     `Mcp-Name` (tools/call, prompts/get → params.name; resources/read →
//     params.uri; `=?base64?…?=` çözülerek) gövdeyle AYNI olmalı. Eksik ya da
//     farklı → -32020 HeaderMismatch ve HTTP 400. Amaç: başlığa göre
//     yönlendiren bir ara katman ile gövdeye göre çalışan sunucu ayrışmasın.
//  3. Sürüm: yalnız ProtocolVersionModern. Başkası → -32022
//     UnsupportedProtocolVersion, HTTP 400, data.supported + data.requested.
//  4. Yöntem: server/discover, tools/list|call, resources/list|read|
//     templates/list, prompts/list|get. `initialize` ve `ping` modern dönemde
//     YOK → -32601 ve HTTP 404 (bilinmeyen her yöntem gibi).
//  5. Sonuç: her result `resultType: "complete"` ve
//     `_meta["io.modelcontextprotocol/serverInfo"]` taşır; liste / okuma /
//     discover sonuçları ayrıca `ttlMs` + `cacheScope` (CacheableResult).
//     cacheScope hep "private": uç kimlik doğrulamalıdır, paylaşılan ara
//     katman önbelleklememeli.
//  6. Varlık bulunamadı (bilinmeyen tool / prompt / resource) -32602'dir
//     (belirtim -32002'yi ve "yöntem yok" anlamındaki -32601'i bunun için
//     kullanmaz); legacy yol -32601 döndürmeye devam eder.
//
// Kapı (rol + hız) ve gözlem kancaları AYNI handler'larda — modern yol
// yalnız zarfı değiştirir, yürütmeyi değil.
//
// Kapsam dışı (bilinçli): `subscriptions/listen` (sunucu list_changed
// yayınlamıyor — kayıt defteri önyüklemede sabit), MRTR / input_required
// (sunucu sampling / elicitation / roots kullanmıyor), `x-mcp-header`
// (hiçbir araç şeması taşımıyor), `io.modelcontextprotocol/logLevel`
// (logging yeteneği ilan edilmiyor). Toplu (batch) gövde modern dönemde
// tanımlı değil: batch içindeki modern istek -32600 alır.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// ProtocolVersionModern — desteklenen modern (el sıkışmasız) sürüm.
const ProtocolVersionModern = "2026-07-28"

// `_meta` anahtarları (basic › `_meta` › Reserved keys).
const (
	MetaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	MetaClientInfo         = "io.modelcontextprotocol/clientInfo"
	MetaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	MetaServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// İstek başlıkları (streamable-http › Request Metadata).
const (
	HeaderProtocolVersion = "MCP-Protocol-Version"
	HeaderMethod          = "Mcp-Method"
	HeaderName            = "Mcp-Name"
)

// Belirtimin ayırdığı -32020…-32099 alt aralığından tanımlı kodlar.
const (
	ErrHeaderMismatch             = -32020
	ErrMissingClientCapability    = -32021 // bugün üretilmiyor: sunucu istemci yeteneği istemiyor
	ErrUnsupportedProtocolVersion = -32022
)

// CacheableResult ipuçları (changelog › minor 5). Kayıt defteri önyüklemede
// sabit, o yüzden listeler dakikalarca taze; resources/read canlı veridir.
const (
	modernListTTLMs  = 300_000
	modernCacheScope = "private"
)

// SupportedVersions — bu uç noktanın (POST /api/mcp) konuştuğu sürümler,
// tercih sırasıyla: modern + Streamable'ın legacy el sıkışması. 2024-11-05
// yalnız ayrı HTTP+SSE uç noktasında (Deprecated) yaşar, burada ilan edilmez.
func SupportedVersions() []string { return []string{ProtocolVersionModern, ProtocolVersionStreamable} }

// modernMeta — isteğin `params._meta`'sından okunan protokol alanları.
type modernMeta struct {
	Version         string
	HasVersion      bool
	HasCapabilities bool
	Client          ClientInfo
}

// readModernMeta — SAF: params'tan `_meta` protokol alanları. params nesne
// değilse ya da `_meta` yoksa sıfır değer (modern değil).
func readModernMeta(params json.RawMessage) modernMeta {
	var m modernMeta
	if len(params) == 0 {
		return m
	}
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil || p.Meta == nil {
		return m
	}
	if raw, ok := p.Meta[MetaProtocolVersion]; ok {
		m.HasVersion = json.Unmarshal(raw, &m.Version) == nil && m.Version != ""
	}
	if raw, ok := p.Meta[MetaClientCapabilities]; ok {
		t := strings.TrimSpace(string(raw))
		m.HasCapabilities = strings.HasPrefix(t, "{")
	}
	if raw, ok := p.Meta[MetaClientInfo]; ok {
		_ = json.Unmarshal(raw, &m.Client)
	}
	return m
}

// isModernRequest — dönem seçimi (dosya başı).
func isModernRequest(req *Request) bool {
	if req.Method == "server/discover" {
		return true
	}
	if len(req.Params) == 0 {
		return false
	}
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(req.Params, &p) != nil {
		return false
	}
	_, ok := p.Meta[MetaProtocolVersion]
	return ok
}

// decodeHeaderValue — `=?base64?…?=` nöbetçisini çözer (streamable-http ›
// Value Encoding); nöbetçi yoksa değer aynen. Bozuk base64 → ok=false.
func decodeHeaderValue(v string) (string, bool) {
	const pre, suf = "=?base64?", "?="
	if !strings.HasPrefix(v, pre) || !strings.HasSuffix(v, suf) || len(v) < len(pre)+len(suf) {
		return v, true
	}
	b, err := base64.StdEncoding.DecodeString(v[len(pre) : len(v)-len(suf)])
	if err != nil {
		return "", false
	}
	return string(b), true
}

// modernNameOf — `Mcp-Name` başlığının kaynak alanı: tools/call ve
// prompts/get → params.name, resources/read → params.uri; diğer yöntemlerde
// başlık beklenmez (need=false).
func modernNameOf(method string, params json.RawMessage) (name string, need bool) {
	var p struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	_ = json.Unmarshal(params, &p)
	switch method {
	case "tools/call", "prompts/get":
		return p.Name, true
	case "resources/read":
		return p.URI, true
	}
	return "", false
}

// validateModernHeaders — SAF: başlık ↔ gövde eşleşmesi. "" = geçerli; aksi
// hâlde -32020'nin mesajı.
func validateModernHeaders(h http.Header, req *Request, version string) string {
	hv := h.Get(HeaderProtocolVersion)
	switch {
	case hv == "":
		return "Header mismatch: " + HeaderProtocolVersion + " header is required"
	case hv != version:
		return fmt.Sprintf("Header mismatch: %s header value '%s' does not match body value '%s'", HeaderProtocolVersion, hv, version)
	}
	hm := h.Get(HeaderMethod)
	switch {
	case hm == "":
		return "Header mismatch: " + HeaderMethod + " header is required"
	case hm != req.Method:
		return fmt.Sprintf("Header mismatch: %s header value '%s' does not match body value '%s'", HeaderMethod, hm, req.Method)
	}
	name, need := modernNameOf(req.Method, req.Params)
	if !need {
		return ""
	}
	raw := h.Get(HeaderName)
	if raw == "" {
		return "Header mismatch: " + HeaderName + " header is required for " + req.Method
	}
	hn, ok := decodeHeaderValue(raw)
	if !ok {
		return "Header mismatch: " + HeaderName + " header is not valid base64"
	}
	if hn != name {
		return fmt.Sprintf("Header mismatch: %s header value '%s' does not match body value '%s'", HeaderName, hn, name)
	}
	return ""
}

// modernError — kod + mesaj (+ data) taşıyan hata yanıtı.
func modernError(id json.RawMessage, code int, message string, data any) *Response {
	resp := errorResp(id, code, message)
	if data != nil {
		if raw, err := json.Marshal(data); err == nil {
			resp.Error.Data = raw
		}
	}
	return resp
}

// handleModern — tek modern isteğin tamamı: doğrulama → dispatch → zarf.
// Döner: HTTP durumu + JSON-RPC yanıtı.
func (s *Server) handleModern(ctx context.Context, h http.Header, req *Request) (int, *Response) {
	meta := readModernMeta(req.Params)
	switch {
	case !meta.HasVersion:
		return http.StatusBadRequest, errorResp(req.ID, ErrInvalidParams, "missing required _meta field "+MetaProtocolVersion)
	case !meta.HasCapabilities:
		return http.StatusBadRequest, errorResp(req.ID, ErrInvalidParams, "missing required _meta field "+MetaClientCapabilities)
	}
	if msg := validateModernHeaders(h, req, meta.Version); msg != "" {
		return http.StatusBadRequest, errorResp(req.ID, ErrHeaderMismatch, msg)
	}
	if meta.Version != ProtocolVersionModern {
		return http.StatusBadRequest, modernError(req.ID, ErrUnsupportedProtocolVersion, "Unsupported protocol version",
			map[string]any{"supported": SupportedVersions(), "requested": meta.Version})
	}

	var resp *Response
	cacheable, ttl := false, 0
	switch req.Method {
	case "server/discover":
		if meta.Client.Name != "" {
			log.Printf("[mcp] server/discover from %s/%s", meta.Client.Name, meta.Client.Version)
		}
		resp = successResp(req.ID, map[string]any{
			"supportedVersions": SupportedVersions(),
			"capabilities": ServerCapabilities{
				Tools: &CapToolsBag{}, Resources: &CapResourcesBag{}, Prompts: &CapPromptsBag{},
			},
		})
		cacheable, ttl = true, modernListTTLMs
	case "tools/list":
		resp, cacheable, ttl = s.handleToolsList(req), true, modernListTTLMs
	case "tools/call":
		resp = s.handleToolsCall(ctx, req)
	case "resources/list":
		resp, cacheable, ttl = s.handleResourcesList(req), true, modernListTTLMs
	case "resources/templates/list":
		resp, cacheable, ttl = s.handleResourceTemplatesList(req), true, modernListTTLMs
	case "resources/read":
		resp, cacheable, ttl = s.handleResourcesRead(ctx, req), true, 0 // canlı veri: önbellekleme
	case "prompts/list":
		resp, cacheable, ttl = s.handlePromptsList(req), true, modernListTTLMs
	case "prompts/get":
		resp = s.handlePromptsGet(ctx, req)
	default:
		// initialize / ping dahil: modern dönemde tanımlı değil.
		return http.StatusNotFound, errorResp(req.ID, ErrMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
	if resp.Error != nil {
		// Yöntem biliniyor; handler'ın -32601'i "varlık bulunamadı"dır
		// (bilinmeyen tool / prompt / resource) → modern dönemde -32602.
		if resp.Error.Code == ErrMethodNotFound {
			resp.Error.Code = ErrInvalidParams
		}
		return http.StatusOK, resp
	}
	return http.StatusOK, s.decorateModern(resp, cacheable, ttl)
}

// decorateModern — result'a modern zarf alanları: resultType, serverInfo
// (`_meta` içinde; handler'ın kendi `_meta`'sı korunur) ve önbelleklenebilir
// sonuçlarda ttlMs + cacheScope. result nesne değilse dokunulmaz.
func (s *Server) decorateModern(resp *Response, cacheable bool, ttlMs int) *Response {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &obj); err != nil || obj == nil {
		return resp
	}
	put := func(m map[string]json.RawMessage, k string, v any) {
		if raw, err := json.Marshal(v); err == nil {
			m[k] = raw
		}
	}
	put(obj, "resultType", "complete")
	meta := map[string]json.RawMessage{}
	if raw, ok := obj["_meta"]; ok {
		_ = json.Unmarshal(raw, &meta)
	}
	put(meta, MetaServerInfo, s.info)
	put(obj, "_meta", meta)
	if cacheable {
		put(obj, "ttlMs", ttlMs)
		put(obj, "cacheScope", modernCacheScope)
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return resp
	}
	resp.Result = raw
	return resp
}
