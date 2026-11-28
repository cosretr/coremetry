package api

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// v0.10.2492 (yakınsama audit'i C1) — modele gösterilen katalog / istem boyu
// ölçümü (chat_loop_catalog.go).

func catalogTestSpecs() []copilot.ToolSpec {
	return []copilot.ToolSpec{
		{Name: "service_health", Description: "Servis sağlığı özeti.", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"service": map[string]any{"type": "string"}}, "required": []any{"service"}}},
		{Name: "open_problems", Description: "Açık problemler.", InputSchema: map[string]any{"type": "object"}},
		{Name: "search_logs", Description: "Log arama.", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "integer", "maximum": 200}}}},
	}
}

// Saf birikimci: ilk tur sabitlenir, en küçük araç sayısı yalnız araç
// çağırabilen turlardan; ölçüm yokken satır alanları 0 (bilinmiyor) kalır.
func TestLoopCatalogAccumulates(t *testing.T) {
	var nilCat *loopCatalog
	nilCat.observe(context.Background(), "x", catalogTestSpecs()) // nil alıcı no-op
	if lu := nilCat.mark(copilot.LoopUsage{Rounds: 2}); lu != (copilot.LoopUsage{Rounds: 2}) {
		t.Fatalf("nil birikimci satırı değiştirdi: %+v", lu)
	}
	empty := &loopCatalog{}
	if lu := empty.mark(copilot.LoopUsage{Rounds: 1}); lu != (copilot.LoopUsage{Rounds: 1}) || empty.spanAttrs() != nil {
		t.Fatalf("ölçümsüz birikimci alan yazdı: %+v", lu)
	}

	specs := catalogTestSpecs()
	before, _ := json.Marshal(specs)
	want := specCatalogSize(specs)
	if want.Tools != 3 || want.Bytes() != want.DescBytes+want.SchemaBytes || want.DescBytes != len("Servis sağlığı özeti.")+len("Açık problemler.")+len("Log arama.") {
		t.Fatalf("sayım tanımı: %+v", want)
	}

	m := &loopCatalog{}
	ctx := context.Background()
	m.observe(ctx, "SİSTEM-1", specs)                                      // ilk tur: 3 araç
	m.observe(ctx, "SİSTEM-1 + ek", specs[:2])                             // daralan tur: 2 araç
	m.observe(copilot.WithNoToolCalls(ctx), "SİSTEM-1 + tavan", specs[:1]) // tavan turu: en küçüğe girmez
	m.observe(ctx, "SİSTEM-1", specs)                                      // önbellek anahtarı değişip geri döner
	lu := m.mark(copilot.LoopUsage{Rounds: 4})
	if lu.Rounds != 4 || lu.ToolsOffered != 3 || lu.ToolsOfferedMin != 2 ||
		lu.CatalogBytes != uint32(want.Bytes()) || lu.SystemBytes != uint32(len("SİSTEM-1")) {
		t.Fatalf("satır alanları: %+v (beklenen katalog %d B)", lu, want.Bytes())
	}
	if got := m.sizeOf(specs); got != want {
		t.Fatalf("önbellek bayat değer verdi: %+v, beklenen %+v", got, want)
	}
	attrs := map[string]int64{}
	for _, kv := range m.spanAttrs() {
		attrs[string(kv.Key)] = kv.Value.AsInt64()
	}
	if !reflect.DeepEqual(attrs, map[string]int64{
		"coremetry.ai.chat.tools_offered":     3,
		"coremetry.ai.chat.tools_offered_min": 2,
		"coremetry.ai.chat.catalog_bytes":     int64(want.Bytes()),
		"coremetry.ai.chat.system_bytes":      int64(len("SİSTEM-1")),
	}) {
		t.Fatalf("span öznitelikleri satırla aynı değil: %v", attrs)
	}
	// Ölçüm spec listesine dokunmaz.
	if after, _ := json.Marshal(specs); !bytes.Equal(before, after) {
		t.Fatal("observe spec listesini değiştirdi")
	}

	// Yalnız tavan turu ölçüldüyse en küçük araç sayısı bilinmiyor (0) kalır.
	capOnly := &loopCatalog{}
	capOnly.observe(copilot.WithNoToolCalls(ctx), "s", specs)
	if lu := capOnly.mark(copilot.LoopUsage{}); lu.ToolsOffered != 3 || lu.ToolsOfferedMin != 0 {
		t.Fatalf("yalnız tavan turu: %+v", lu)
	}
}

// DAVRANIŞ DEĞİŞMEZ: aynı tur, ölçüm birikimcisi ctx'te VARKEN ve YOKKEN
// sağlayıcıya bayt bayt aynı isteği gönderir (istem, araç listesi, sıra).
func TestLoopCatalogObserveKeepsRequestBytes(t *testing.T) {
	l := newSplitLLM(t, func(string, int, map[string]any) (int, map[string]any) { return 200, answerMsg("tamam") })
	s, _ := splitServer(t, l, nil, false)
	in := stepInput{Kind: stepNormal, Specs: catalogTestSpecs(), Conv: []copilot.ChatMessage{{Role: "user", Text: "checkout nasıl?"}}}
	emit := func(string, any) {}

	// Isınma: sahte uç akış desteklemez; ilk çağrı akışlı gider, karar
	// önbelleğe alınır ve sonrakiler tamponlu gider (ölçümle ilgisiz fark).
	if _, _, err := s.loopTurnOne(context.Background(), "SİSTEM İSTEMİ", in, emit); err != nil {
		t.Fatalf("ısınma turu: %v", err)
	}
	if _, _, err := s.loopTurnOne(context.Background(), "SİSTEM İSTEMİ", in, emit); err != nil {
		t.Fatalf("ölçümsüz tur: %v", err)
	}
	m := &loopCatalog{}
	if _, _, err := s.loopTurnOne(withLoopCatalog(context.Background(), m), "SİSTEM İSTEMİ", in, emit); err != nil {
		t.Fatalf("ölçümlü tur: %v", err)
	}
	bodies := l.bodies()
	if len(bodies) != 3 {
		t.Fatalf("3 istek bekleniyordu: %d", len(bodies))
	}
	if !bytes.Equal(bodies[1], bodies[2]) {
		t.Fatalf("ölçüm istek baytını değiştirdi:\n%s\n---\n%s", bodies[1], bodies[2])
	}
	if !m.seen || m.firstTools != 3 || m.firstSystem != len("SİSTEM İSTEMİ") {
		t.Fatalf("ölçüm alınmadı: %+v", m)
	}
}

// wireCatalog — OpenAI biçimli istek gövdesinden modelin GERÇEKTEN gördüğü:
// araç sayısı, katalog baytı (CatalogSize tanımı) ve sistem mesajının baytı.
func wireCatalog(t *testing.T, raw []byte) (size mcptools.CatalogSize, systemBytes int) {
	t.Helper()
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("istek gövdesi çözülemedi: %v", err)
	}
	if len(body.Messages) == 0 || body.Messages[0].Role != "system" {
		t.Fatal("ilk mesaj sistem istemi değil")
	}
	for _, tl := range body.Tools {
		size.Add(tl.Function.Description, tl.Function.Parameters)
	}
	return size, len(body.Messages[0].Content)
}

// Uçtan uca: gerçek sohbet döngüsü, gerçek katalog. ai_calls satırına yazılan
// değerler sağlayıcıya giden İLK isteğin gövdesinden sayılanla aynı; tur
// boyunca katalog daralmadığı için en küçük araç sayısı ilkine eşit.
func TestLoopCatalogRecordMatchesWire(t *testing.T) {
	l := newSplitLLM(t, func(_ string, n int, _ map[string]any) (int, map[string]any) {
		if n == 0 {
			return 200, toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		}
		return 200, answerMsg("checkout p95 1830 ms.")
	})
	s, rec := splitServer(t, l, nil, false)
	splitPost(t, s)
	bodies := l.bodies()
	if len(bodies) != 2 {
		t.Fatalf("2 model turu bekleniyordu: %d", len(bodies))
	}
	wire, sysBytes := wireCatalog(t, bodies[0])
	if wire.Tools == 0 || wire.Bytes() == 0 || sysBytes == 0 {
		t.Fatalf("gövdede katalog / istem yok: %+v sys=%d", wire, sysBytes)
	}
	var row copilot.CallRecord
	for i := 0; i < 2 && row.ModelRounds == 0; i++ { // döngü satırı + etkileşim satırı (sıra serbest)
		row = rec.next(t)
	}
	if row.ModelRounds != 2 {
		t.Fatalf("döngü satırı gelmedi: %+v", row)
	}
	if int(row.LoopToolsOffered) != wire.Tools || int(row.LoopToolsOfferedMin) != wire.Tools ||
		int(row.LoopCatalogBytes) != wire.Bytes() || int(row.LoopSystemBytes) != sysBytes {
		t.Fatalf("satır gövdeyle uyuşmuyor: araç %d (en küçük %d), katalog %d B, istem %d B; gövde: araç %d, katalog %d B, istem %d B",
			row.LoopToolsOffered, row.LoopToolsOfferedMin, row.LoopCatalogBytes, row.LoopSystemBytes, wire.Tools, wire.Bytes(), sysBytes)
	}
}

// Span: tur değerleri ai.chat.turn'e, alışveriş özeti ai.chat köküne; döngü
// koşmayan (kademe cevaplı) alışverişte kök bu öznitelikleri HİÇ taşımaz.
func TestLoopCatalogSpanAttributes(t *testing.T) {
	s, exp := bareSpanServer(t)
	specs := catalogTestSpecs()
	want := specCatalogSize(specs)
	ctx, cs := s.beginChatSpan(copilot.WithMeta(context.Background(), copilot.CallMeta{Surface: "chat"}), "xid-c1")
	tctx, endTurn := cs.turn(ctx, 0, false)
	loopCatalogFrom(tctx).observe(tctx, "SİSTEM", specs)
	endTurn(10, 5, 0, nil)
	tctx2, endTurn2 := cs.turn(ctx, 1, false)
	loopCatalogFrom(tctx2).observe(tctx2, "SİSTEM + ek", specs[:1])
	endTurn2(10, 5, 0, nil)
	cs.end()

	var root map[string]int64
	turns := map[int64]map[string]int64{}
	for _, sp := range exp.GetSpans() {
		a := attrMap(sp.Attributes)
		ints := map[string]int64{}
		for _, k := range []string{"tools_offered", "tools_offered_min", "catalog_bytes", "system_bytes"} {
			for _, scope := range []string{"coremetry.ai.chat.", "coremetry.ai.turn."} {
				if v, ok := a[scope+k]; ok {
					ints[scope+k] = v.AsInt64()
				}
			}
		}
		switch sp.Name {
		case "ai.chat":
			root = ints
		case "ai.chat.turn":
			turns[a["coremetry.ai.turn.round"].AsInt64()] = ints
		}
	}
	if !reflect.DeepEqual(root, map[string]int64{
		"coremetry.ai.chat.tools_offered": 3, "coremetry.ai.chat.tools_offered_min": 1,
		"coremetry.ai.chat.catalog_bytes": int64(want.Bytes()), "coremetry.ai.chat.system_bytes": int64(len("SİSTEM")),
	}) {
		t.Fatalf("kök: %v", root)
	}
	if !reflect.DeepEqual(turns[0], map[string]int64{
		"coremetry.ai.turn.tools_offered": 3, "coremetry.ai.turn.catalog_bytes": int64(want.Bytes()), "coremetry.ai.turn.system_bytes": int64(len("SİSTEM")),
	}) {
		t.Fatalf("0. tur: %v", turns[0])
	}
	one := specCatalogSize(specs[:1])
	if !reflect.DeepEqual(turns[1], map[string]int64{
		"coremetry.ai.turn.tools_offered": 1, "coremetry.ai.turn.catalog_bytes": int64(one.Bytes()), "coremetry.ai.turn.system_bytes": int64(len("SİSTEM + ek")),
	}) {
		t.Fatalf("1. tur: %v", turns[1])
	}

	s2, exp2 := bareSpanServer(t)
	_, cs2 := s2.beginChatSpan(copilot.WithMeta(context.Background(), copilot.CallMeta{Surface: "chat"}), "xid-c1b")
	cs2.tier("guided", true)
	cs2.end()
	if _, ok := attrMap(exp2.GetSpans()[0].Attributes)["coremetry.ai.chat.tools_offered"]; ok {
		t.Fatal("döngü koşmadan kök span katalog özniteliği taşıyor")
	}
}
