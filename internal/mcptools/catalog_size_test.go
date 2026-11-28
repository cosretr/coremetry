package mcptools

// v0.10.2492 (docs/audit/cosre-convergence-audit.md, dilim C1) — sohbet
// kataloğunun boy RATCHET'i: araç sayısı ve katalog baytı YALNIZ AŞAĞI gider.
//
// Model bu kataloğun tamamını HER TURDA yeniden okur (audit Risk 1: ≈22–25 bin
// token); küçük modelde araç seçimi doğruluğu katalog büyüdükçe düşer. Tek
// tek kapılar (short_desc_test.go: araç başına ve toplam açıklama tavanı) pay
// bırakır ve şemayı hiç saymaz; bu test bugünkü ölçümü tam değeriyle pinler.
//
// Sayım tanımı (CatalogSize): araç başına len(ChatDescription()) +
// len(json.Marshal(InputSchema)); ad ve sağlayıcı zarfı hariç. Şema kayıt
// defterindeki HAM hâliyle sayılır (⟦full|simple⟧ işaretleri çözülmeden) —
// audit böyle ölçtü. Modele giden işlenmiş şema full sette 128 B daha kısadır
// (55.426 B); çalışma zamanı ölçümü (api/chat_loop_catalog.go) onu yazar.
//
// Test düşerse:
//   - "büyüdü": yeni araç ya da uzayan açıklama / şema. Önce kısalt: açıklamayı
//     sık, şemadaki tekrar eden `description` metinlerini kırp, aracı sohbet
//     kataloğu yerine yalnız dış MCP'ye kaydet. Büyüme BİLİNÇLİ ise sabiti
//     ölçülen değere çıkar ve commit gövdesine gerekçeyi + yeni sayıyı yaz.
//   - "küçüldü": iyi haber; sabiti hata iletisindeki değere İNDİR ve aynı
//     commit'e ekle (ratchet bir daha o değerin üstüne çıkmaz).

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// Ölçüm tabanı v0.10.2491 (audit "Ölçümler" tablosu: 81 araç, 11.799 +
// 55.554 = 67.353 B; v2 eki 8 araç, 1.856 + 6.808 = 8.664 B; ikisinin toplamı
// 76.017 B ≈ 76 KB).
const (
	// chatCatalogFullTools / Bytes — v1 sohbet kataloğu, koşullu bağımlılıklar
	// dolu (kod, kod arama, wiki, Oracle), araç seti full.
	chatCatalogFullTools = 81
	chatCatalogFullBytes = 67353
	// chatCatalogV2ExtraTools / Bytes — v2 döngüsünün eklediği araçlar (7 yeni
	// + search_logs'un yerine geçen 1).
	chatCatalogV2ExtraTools = 8
	chatCatalogV2ExtraBytes = 8664
	// chatCatalogV2ViewTools / Bytes — v2 modelinin turda GERÇEKTEN gördüğü
	// katalog (V2ToolList): 81 + 7 yeni = 88, eksi v2'de gizlenen 3 genel sorgu
	// aracı (search_traces, query_metric, list_metric_labels) = 85; açıklama
	// 13.215 + şema 56.516 B. Audit'in "≈88 araç / ≈76 KB" satırı gizlenenleri
	// ve yerine geçen search_logs'u düşmeden toplamıştı.
	chatCatalogV2ViewTools = 85
	chatCatalogV2ViewBytes = 69731
)

func TestChatCatalogSizeRatchet(t *testing.T) {
	d := goldenConditionalDeps()
	for _, tc := range []struct {
		name      string
		tools     []mcp.Tool
		pinTools  int
		pinBytes  int
		toolsName string
		bytesName string
	}{
		{"v1 sohbet kataloğu (tam bağımlılık, full)", ChatToolListFor(d, "full"), chatCatalogFullTools, chatCatalogFullBytes, "chatCatalogFullTools", "chatCatalogFullBytes"},
		{"v2 ek araçları", V2OnlyTools(d), chatCatalogV2ExtraTools, chatCatalogV2ExtraBytes, "chatCatalogV2ExtraTools", "chatCatalogV2ExtraBytes"},
		{"v2 modelinin gördüğü katalog (tam bağımlılık, full)", V2ToolList(d, "full"), chatCatalogV2ViewTools, chatCatalogV2ViewBytes, "chatCatalogV2ViewTools", "chatCatalogV2ViewBytes"},
	} {
		got := ChatCatalogSize(tc.tools)
		switch {
		case got.Tools > tc.pinTools:
			t.Errorf("%s BÜYÜDÜ: %d araç > taban %d. Model her turda tüm kataloğu okur; yeni aracı sohbet kataloğuna eklemeden önce bu dosyanın başlığını oku. Bilinçliyse %s = %d yap ve gerekçeyi commit'e yaz.",
				tc.name, got.Tools, tc.pinTools, tc.toolsName, got.Tools)
		case got.Tools < tc.pinTools:
			t.Errorf("%s küçüldü: %d araç < taban %d — ratchet: %s = %d yap ve aynı commit'e ekle.",
				tc.name, got.Tools, tc.pinTools, tc.toolsName, got.Tools)
		}
		switch {
		case got.Bytes() > tc.pinBytes:
			t.Errorf("%s BÜYÜDÜ: %d B (açıklama %d + şema %d) > taban %d B (+%d B). Model her turda tüm kataloğu okur; önce açıklamayı / şemayı kısalt (bu dosyanın başlığı). Bilinçliyse %s = %d yap ve gerekçeyi commit'e yaz.",
				tc.name, got.Bytes(), got.DescBytes, got.SchemaBytes, tc.pinBytes, got.Bytes()-tc.pinBytes, tc.bytesName, got.Bytes())
		case got.Bytes() < tc.pinBytes:
			t.Errorf("%s küçüldü: %d B (açıklama %d + şema %d) < taban %d B — ratchet: %s = %d yap ve aynı commit'e ekle.",
				tc.name, got.Bytes(), got.DescBytes, got.SchemaBytes, tc.pinBytes, tc.bytesName, got.Bytes())
		}
	}
}

// Sayım tanımının kendisi: ad ve zarf sayılmaz, nil şema "null" (4 B) sayılır,
// harita anahtar sırası sonucu değiştirmez (encoding/json sıralar).
func TestCatalogSizeDefinition(t *testing.T) {
	var c CatalogSize
	c.Add("abc", map[string]any{"type": "object"})
	if c.Tools != 1 || c.DescBytes != 3 || c.SchemaBytes != len(`{"type":"object"}`) || c.Bytes() != 3+17 {
		t.Fatalf("tek araç: %+v", c)
	}
	c.Add("ğ", nil) // 2 bayt UTF-8; şema yok → "null"
	if c.Tools != 2 || c.DescBytes != 5 || c.SchemaBytes != 17+4 {
		t.Fatalf("iki araç: %+v", c)
	}
	got := ChatCatalogSize([]mcp.Tool{
		{Name: "uzun_bir_ad_sayilmaz", Description: "tam metin sayılmaz", ShortDescription: "kısa", InputSchema: map[string]any{"b": 1, "a": 2}},
	})
	if got.Tools != 1 || got.DescBytes != len("kısa") || got.SchemaBytes != len(`{"a":2,"b":1}`) {
		t.Fatalf("ChatCatalogSize: %+v", got)
	}
}
