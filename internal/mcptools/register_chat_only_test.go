package mcptools

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// Sohbet-yalnız araçlar (set/get/clear_context) dış MCP sunucusuna
// kaydedilmez: konuşma durumu olmadan yalnız hata dönebilirler. Uygulama içi
// sohbet ToolList'i doğrudan kullanır, orada kalırlar.
func TestRegisterHidesChatOnlyTools(t *testing.T) {
	srv := mcp.New("coremetry", "test")
	Register(srv, Deps{})
	if got, want := srv.ToolCount(), len(ToolList(Deps{}))-len(chatOnlyTools); got != want {
		t.Fatalf("kayıtlı tool = %d, want %d (sohbet-yalnız %d araç hariç)", got, want, len(chatOnlyTools))
	}
	in := map[string]bool{}
	for _, tool := range ToolList(Deps{}) {
		in[tool.Name] = true
	}
	for name := range chatOnlyTools {
		if !in[name] {
			t.Errorf("chatOnlyTools %q ToolList'te yok — ad bayat", name)
		}
	}
}
