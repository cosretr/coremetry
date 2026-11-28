package api

// chat_loop_catalog.go — v0.10.2492 (docs/audit/cosre-convergence-audit.md,
// dilim C1): serbest döngünün her model turunda modele GÖSTERİLEN şeyin boyu —
// sunulan araç sayısı, katalog baytı, sistem istemi baytı.
//
// Yalnız ÖLÇÜM: spec listesi, istem ve sıra okunur, hiçbiri değişmez (istek
// baytı aynı; chat_loop_catalog_test.go ölçümü gövdeyle karşılaştırır).
//
// Ölçüm noktası loopTurnOne: plan uygulandıktan SONRAKİ gerçek spec listesi ve
// sistem istemi (v2 alt kümesi, kullanıcı yönlendirmesi, tavan eki dahil).
// Katalog baytının sayım tanımı mcptools.CatalogSize (audit "Ölçümler"):
// araç başına len(açıklama) + len(json.Marshal(şema)); burada şema modele
// giden işlenmiş hâlidir (⟦full|simple⟧ çözülmüş, v2 daraltması uygulanmış).
//
// Nereye yazılır:
//   - ai.chat.turn span'ı: o turun üç değeri (tur başına).
//   - ai.chat span'ı + ai_calls satırı (alışveriş başına tek satır): İLK turun
//     araç sayısı / katalog baytı / istem baytı ve araç çağırabilen turların
//     en küçük araç sayısı (katalog döngü içinde daralırsa ilkinden küçük).
//
// Profil ayrımının araçsız anlatım çağrısı (splitNarrate) ölçülmez: araç
// sunmaz, istemi anlatıcı çekirdeğidir; araştırma turları loopTurnOne'dan
// geçtiği için ölçülür.

import (
	"context"
	"hash/fnv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// loopCatalog — alışverişin birikimcisi (tek goroutine: döngü turları sıralı).
// nil alıcı no-op: döngü dışı çağıranlar (ctx'te yoksa) ölçmez.
type loopCatalog struct {
	seen        bool // en az bir tur ölçüldü
	firstTools  int
	firstBytes  int
	firstSystem int
	minSeen     bool // en az bir araç çağırabilen tur ölçüldü
	minTools    int

	// Liste değişmedikçe şemalar yeniden serileştirilmez.
	sizeKey uint64
	size    mcptools.CatalogSize
}

type loopCatalogKey struct{}

func withLoopCatalog(ctx context.Context, m *loopCatalog) context.Context {
	return context.WithValue(ctx, loopCatalogKey{}, m)
}

func loopCatalogFrom(ctx context.Context) *loopCatalog {
	m, _ := ctx.Value(loopCatalogKey{}).(*loopCatalog)
	return m
}

// specCatalogSize — SAF: modele giden spec listesinin boyu (CatalogSize tanımı).
func specCatalogSize(specs []copilot.ToolSpec) mcptools.CatalogSize {
	var c mcptools.CatalogSize
	for _, sp := range specs {
		c.Add(sp.Description, sp.InputSchema)
	}
	return c
}

// sizeOf — specCatalogSize, ad + açıklama boyu özetiyle önbellekli.
func (m *loopCatalog) sizeOf(specs []copilot.ToolSpec) mcptools.CatalogSize {
	h := fnv.New64a()
	for _, sp := range specs {
		n := len(sp.Description)
		_, _ = h.Write([]byte(sp.Name))
		_, _ = h.Write([]byte{0, byte(n), byte(n >> 8), byte(n >> 16)})
	}
	k := h.Sum64()
	if k == m.sizeKey && m.size.Tools == len(specs) && len(specs) > 0 {
		return m.size
	}
	m.sizeKey, m.size = k, specCatalogSize(specs)
	return m.size
}

// observe — bir model turu: ctx çağrının ctx'i (plan seçenekleri uygulanmış;
// içindeki span ai.chat.turn), system ve specs sağlayıcıya giden değerler.
func (m *loopCatalog) observe(ctx context.Context, system string, specs []copilot.ToolSpec) {
	if m == nil {
		return
	}
	size := m.sizeOf(specs)
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.Int("coremetry.ai.turn.tools_offered", size.Tools),
		attribute.Int("coremetry.ai.turn.catalog_bytes", size.Bytes()),
		attribute.Int("coremetry.ai.turn.system_bytes", len(system)),
	)
	if !m.seen {
		m.seen = true
		m.firstTools, m.firstBytes, m.firstSystem = size.Tools, size.Bytes(), len(system)
	}
	// Tavan turu ve yineleme denemesi çağrı yapamaz (WithNoToolCalls); tanımlar
	// gövdede kalsa da "daralma" sayılmaz.
	if !copilot.NoToolCallsSet(ctx) && (!m.minSeen || size.Tools < m.minTools) {
		m.minSeen, m.minTools = true, size.Tools
	}
}

// mark — döngü satırının alanları (ölçüm yoksa lu aynen: 0 = bilinmiyor).
func (m *loopCatalog) mark(lu copilot.LoopUsage) copilot.LoopUsage {
	if m == nil || !m.seen {
		return lu
	}
	lu.ToolsOffered = clampU16(m.firstTools)
	lu.CatalogBytes = clampU32(m.firstBytes)
	lu.SystemBytes = clampU32(m.firstSystem)
	if m.minSeen {
		lu.ToolsOfferedMin = clampU16(m.minTools)
	}
	return lu
}

// spanAttrs — ai.chat span'ına alışveriş özeti (mark ile AYNI değerler).
func (m *loopCatalog) spanAttrs() []attribute.KeyValue {
	if m == nil || !m.seen {
		return nil
	}
	lu := m.mark(copilot.LoopUsage{})
	return []attribute.KeyValue{
		attribute.Int("coremetry.ai.chat.tools_offered", int(lu.ToolsOffered)),
		attribute.Int("coremetry.ai.chat.tools_offered_min", int(lu.ToolsOfferedMin)),
		attribute.Int("coremetry.ai.chat.catalog_bytes", int(lu.CatalogBytes)),
		attribute.Int("coremetry.ai.chat.system_bytes", int(lu.SystemBytes)),
	}
}

func clampU16(n int) uint16 {
	switch {
	case n <= 0:
		return 0
	case n > int(^uint16(0)):
		return ^uint16(0)
	}
	return uint16(n)
}

func clampU32(n int) uint32 {
	switch {
	case n <= 0:
		return 0
	case int64(n) > int64(^uint32(0)):
		return ^uint32(0)
	}
	return uint32(n)
}
