package mcptools

// catalog_size.go — v0.10.2492 (docs/audit/cosre-convergence-audit.md, dilim
// C1): modelin bir turda gördüğü araç kataloğunun boyu.
//
// Sayım tanımı audit'in "Ölçümler" tablosuyla AYNI ve tek yerde: araç başına
// len(sohbet açıklaması) + len(json.Marshal(giriş şeması)). Araç adı ve
// sağlayıcı zarfı (tools dizisinin JSON iskeleti) sayılmaz; sağlayıcıdan
// bağımsız, sürümler arası karşılaştırılabilir ölçü budur. Serbest döngü tur
// başına bunu ai_calls satırına ve span'a yazar (api/chat_loop_catalog.go);
// ratchet testi (catalog_size_test.go) kataloğun sessizce büyümesini durdurur.

import (
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// CatalogSize — bir araç listesinin modele maliyeti (SAF birikimci; sıfır
// değer kullanılabilir).
type CatalogSize struct {
	Tools       int
	DescBytes   int
	SchemaBytes int
}

// Bytes — açıklama + şema toplamı.
func (c CatalogSize) Bytes() int { return c.DescBytes + c.SchemaBytes }

// Add — bir aracı sayar: modele giden açıklama metni ve giriş şemasının JSON'u.
func (c *CatalogSize) Add(description string, schema map[string]any) {
	c.Tools++
	c.DescBytes += len(description)
	if raw, err := json.Marshal(schema); err == nil {
		c.SchemaBytes += len(raw)
	}
}

// ChatCatalogSize — kayıt defterindeki araçların sohbet görünümü:
// ChatDescription() + InputSchema (audit ölçümünün birebir tanımı).
func ChatCatalogSize(tools []mcp.Tool) CatalogSize {
	var c CatalogSize
	for _, t := range tools {
		c.Add(t.ChatDescription(), t.InputSchema)
	}
	return c
}
