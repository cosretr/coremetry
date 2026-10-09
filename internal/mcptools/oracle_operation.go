package mcptools

// oracle_operation.go — v0.10.1133 — Oracle OPERASYON ADI → span süzgeci
// çevirisi (operatör: CoSRE "DEPOSIT_…_INQUIRY_REST trace'lerini getir"e
// "trace bulunamadı" dedi; komut paleti aynı adı /api/oracle/operations ile
// doğru çeviriyordu).
//
// Operasyon adı span'lerde YOK; span'ler yalnız fonksiyon kodunu taşır
// (FUNCTION_CODE = SAMP01 gibi). Köprü Oracle hata satırlarıdır
// (oracle_error_log — poller'ın kopyası; canlı Oracle'a GİDİLMEZ). Bu dosya
// iki şey taşır:
//
//   - SAF çekirdek ResolveOracleOpHits: OracleOperationSearch'ün alt-dize
//     isabetlerinden YALNIZ tam eşleşen (kırpılmış, harf duyarsız) adların
//     kodlarını çıkarır; alt-dize isabetleri AYRI "yakın adlar" listesidir ve
//     hiçbir yüzey onlarla kendiliğinden arama YAPMAZ (yanlış operasyonun
//     trace'leri doğru cevap gibi görünürdü).
//   - resolve_oracle_operation aracı: sohbet ajanı ve dış MCP için aynı
//     çözümleyici (api/oracle_op_resolve.go doldurur; guided akış da onu
//     çağırır — tek uygulama).
//
// SALT OKUMA: araç hiçbir şey yazmaz, ayar değiştirmez; okuma yalnız
// oracle_error_log'dan, mevcut 7 gün penceresi + LIMIT + max_execution_time
// ile. MinRole "" — REST eşi GET /api/oracle/operations rol kapısız.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// OracleOperationToolName — kayıt defteri adı (chatOffered anahtarı).
const OracleOperationToolName = "resolve_oracle_operation"

// oracleNearNamesMax — "bunu mu demek istediniz" listesinin tavanı.
const oracleNearNamesMax = 5

// OracleOpMaxCodes — çözümün taşıdığı en çok kod (guided kod başına bir arama
// yapar; harf varyantı isabetlerinin birleşimi chstore'un isabet başı 5 tavanını
// aşabilir).
const OracleOpMaxCodes = 5

// oracleOpNameMin / oracleOpNameMax — ad uzunluk sınırları (REST ?q ile aynı:
// oracleOpSearchMinQuery / oracleOpSearchMaxQuery).
const (
	oracleOpNameMin = 3
	oracleOpNameMax = 80
)

// OracleOpMatch — tam eşleşen operasyonun TEK fonksiyon kodu.
type OracleOpMatch struct {
	Code          string `json:"code"`
	SpanKey       string `json:"span_key"`
	Operation     string `json:"operation"`
	Rows          uint64 `json:"rows"`
	LatestTraceID string `json:"latest_trace_id,omitempty"`
}

// OracleOpResolution — çözümleyicinin cevabı. Enabled=false: etkin Oracle
// kaynağı yok (hata DEĞİL, boş sonuç).
type OracleOpResolution struct {
	Enabled bool
	// SpanKey — süzgecin anahtar yazımı (eşleşme olmasa da dolu).
	SpanKey string
	// Matches — kod başına bir satır; tam eşleşme yoksa boş.
	Matches []OracleOpMatch
	// NearNames — yalnız ALT-DİZE isabetleri; asla otomatik aranmaz.
	NearNames []string
}

// Codes — tekil kodlar, eşleşme sırasıyla.
func (r OracleOpResolution) Codes() []string {
	out := make([]string, 0, len(r.Matches))
	for _, m := range r.Matches {
		out = append(out, m.Code)
	}
	return out
}

// LatestTraceID — eşleşmelerdeki ilk dolu son-hata trace'i ("" = yok).
func (r OracleOpResolution) LatestTraceID() string {
	for _, m := range r.Matches {
		if m.LatestTraceID != "" {
			return m.LatestTraceID
		}
	}
	return ""
}

// OracleOpSource — çözümleyici kapısı (api doldurur). Enabled ucuz olmalı:
// sohbet kataloğu her tur sorar (chatOffered).
type OracleOpSource interface {
	Enabled() bool
	ResolveOracleOperation(ctx context.Context, name string) (OracleOpResolution, error)
}

// ResolveOracleOpHits — SAF (tablo testli): alt-dize isabetleri → tam
// eşleşmelerin kodları + yakın adlar. Aynı ad farklı harf büyüklüğüyle iki
// isabet verebilir (operation_code GROUP BY'ı harf duyarlı): kodlar birleşir,
// tekrar eden kod ilk (en çok satırlı) isabette kalır.
func ResolveOracleOpHits(name, spanKey string, hits []chstore.OracleOpHit) OracleOpResolution {
	res := OracleOpResolution{Enabled: true, SpanKey: spanKey, Matches: []OracleOpMatch{}, NearNames: []string{}}
	want := strings.TrimSpace(name)
	if want == "" {
		return res
	}
	seenCode := map[string]bool{}
	seenNear := map[string]bool{}
	for _, h := range hits {
		op := strings.TrimSpace(h.Operation)
		if op == "" {
			continue
		}
		if !strings.EqualFold(op, want) {
			if !seenNear[op] && len(res.NearNames) < oracleNearNamesMax {
				seenNear[op] = true
				res.NearNames = append(res.NearNames, op)
			}
			continue
		}
		for _, c := range h.FunctionCodes {
			c = strings.TrimSpace(c)
			if c == "" || seenCode[c] || len(res.Matches) >= OracleOpMaxCodes {
				continue
			}
			seenCode[c] = true
			res.Matches = append(res.Matches, OracleOpMatch{Code: c, SpanKey: spanKey, Operation: op, Rows: h.Rows, LatestTraceID: h.LastTraceID})
		}
	}
	return res
}

// ─── resolve_oracle_operation tool ─────────────────────────────

type resolveOracleOperationArgs struct {
	Name string `json:"name"`
}

func resolveOracleOperationTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             OracleOperationToolName,
		ShortDescription: "Büyük harfli ve alt çizgili bir operasyon/servis adı trace aramasında sonuç vermediyse çağır; adı span süzgecinde kullanılacak FUNCTION_CODE değerine çevirir.",
		Description: "Translate an Oracle OPERATION name (uppercase with underscores, e.g. DEPOSIT_SAMPLE_INQUIRY_REST) into the FUNCTION_CODE value(s) spans actually carry. " +
			"Operation names never appear on spans, so a trace search by that name returns nothing — call this when an uppercase/underscore operation or service name found no traces. " +
			"Only an EXACT (case-insensitive) name match yields codes; then call search_traces once PER code with filters=[{key: span_key, op: eq, value: code}] (no service needed; success + error traces). " +
			"`near_names` are substring look-alikes: offer them as \"did you mean\", never search with them automatically. `latest_trace_id` is the newest error row's trace. " +
			"enabled=false means no Oracle source is configured — say so. Read-only: reads Coremetry's copy of the Oracle error table (last 7 days), never the live database.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "The operation name verbatim (3–80 chars)."},
			},
			"required": []string{"name"},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a resolveOracleOperationArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			name := strings.TrimSpace(a.Name)
			if n := len([]rune(name)); n < oracleOpNameMin || len(name) > oracleOpNameMax {
				return nil, fmt.Errorf("invalid argument: name %d–%d karakter olmalı", oracleOpNameMin, oracleOpNameMax)
			}
			if d.OracleOps == nil || !d.OracleOps.Enabled() {
				return oracleOperationResult(name, OracleOpResolution{Matches: []OracleOpMatch{}, NearNames: []string{}}), nil
			}
			res, err := d.OracleOps.ResolveOracleOperation(ctx, name)
			if err != nil {
				return nil, err
			}
			return oracleOperationResult(name, res), nil
		},
	}
}

// oracleOperationResult — SAF: çözüm → aracın JSON gövdesi.
func oracleOperationResult(name string, r OracleOpResolution) map[string]any {
	matches := r.Matches
	if matches == nil {
		matches = []OracleOpMatch{}
	}
	near := r.NearNames
	if near == nil {
		near = []string{}
	}
	var rows uint64
	seenOp := map[string]bool{}
	for _, m := range matches {
		if !seenOp[m.Operation] { // satır sayısı operasyon başına; kod başına tekrar sayılmaz
			seenOp[m.Operation] = true
			rows += m.Rows
		}
	}
	out := map[string]any{
		"name": name, "enabled": r.Enabled, "span_key": r.SpanKey, "codes": r.Codes(), "matches": matches,
		"rows": rows, "near_names": near,
	}
	if t := r.LatestTraceID(); t != "" {
		out["latest_trace_id"] = t
	}
	switch {
	case !r.Enabled:
		out["hint"] = "Oracle kaynağı yapılandırılmamış ya da kapalı — çeviri yapılamaz; bunu söyle, kod UYDURMA."
	case len(matches) > 0:
		out["hint"] = "search_traces'i KOD BAŞINA filters=[{key: span_key, op: eq, value: kod}] ile çağır (servis gerekmez; başarılı + hatalı tüm trace'ler). Trace çıkmazsa latest_trace_id son hata trace'idir."
	case len(near) > 0:
		out["hint"] = "Tam eşleşme yok. near_names'i \"bunu mu demek istediniz\" diye sun; onlarla kendiliğinden arama YAPMA."
	default:
		out["hint"] = "Bu ad son 7 günün Oracle hata satırlarında yok — dürüstçe söyle."
	}
	return out
}
