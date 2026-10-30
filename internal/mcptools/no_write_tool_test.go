package mcptools

// no_write_tool_test.go — v0.10.1316 (değerlendirme seti gerçek ajan
// döngüsünde): katalogda YAZAN araç yok. Ajan kipi evalset vakası canlı
// sohbet döngüsünü gerçek araç kataloğuyla koşar; güvenlik iddiası "katalog
// salt-okur" ise bu test onu pinler:
//
//   - her ToolList kaydı viewer tabanında (MinRole "") — yetki isteyen bir
//     araç (yazma ya da yönetim) kataloğa girerse kırmızı;
//   - yazma fiiliyle başlayan ad (set_ / clear_ / create_ / delete_ …) yalnız
//     ConversationStateTools'ta (sohbetin kendi Redis bağlamı; ajan kipi onu
//     düşürür) ya da aşağıdaki gerekçeli okuma-fiili listesinde olabilir.

import (
	"regexp"
	"testing"
)

// writeToolName — yazma fiiliyle başlayan araç adı.
var writeToolName = regexp.MustCompile(`^(set|clear|create|update|delete|remove|put|post|write|save|upsert|insert|mark|resolve|ack|acknowledge|execute|run|append|assign|close|reopen|snooze|mute|silence|restart|rollback|scale|apply)_`)

// readVerbAllow — fiili yazma gibi okunan ama OKUYAN araçlar (ad çözümü).
// Liste yalnız küçülür; yeni satır gerekçesiyle.
var readVerbAllow = map[string]string{
	"resolve_entity":           "serbest metin → varlık adayı (katalog okuması)",
	"resolve_business_code":    "iş kodu → servis eşlemesi (okuma)",
	"resolve_oracle_operation": "Oracle operasyon adı → FUNCTION_CODE (okuma)",
}

func TestMCPToolsNoWriteTool(t *testing.T) {
	tools := ToolList(Deps{})
	if len(tools) < 30 {
		t.Fatalf("yalnız %d araç — katalog okunamadı mı?", len(tools))
	}
	seenAllow := map[string]bool{}
	for _, tl := range tools {
		// agent-core S4 — tek istisna onay-kapılı araç (Approval:true): yazar
		// ama insan onayıyla; MCP teli ve toolsForRole onu sunmaz.
		if tl.Approval {
			if tl.MinRole == "" {
				t.Errorf("%s: onay-kapılı araç MinRole ister (editor+)", tl.Name)
			}
			continue
		}
		if tl.MinRole != "" {
			t.Errorf("%s: MinRole %q — sohbet/değerlendirme kataloğu viewer tabanında salt-okur kalmalı", tl.Name, tl.MinRole)
		}
		if !writeToolName.MatchString(tl.Name) {
			continue
		}
		if ConversationStateTools[tl.Name] {
			continue
		}
		if _, ok := readVerbAllow[tl.Name]; ok {
			seenAllow[tl.Name] = true
			continue
		}
		t.Errorf("%s: yazma fiiliyle başlayan araç — katalog salt-okur; okuyan bir araçsa readVerbAllow'a gerekçeyle ekle", tl.Name)
	}
	for name := range ConversationStateTools {
		found := false
		for _, tl := range tools {
			found = found || tl.Name == name
		}
		if !found {
			t.Errorf("ConversationStateTools bayat: %s katalogda yok", name)
		}
	}
	for name := range readVerbAllow {
		if !seenAllow[name] {
			t.Errorf("readVerbAllow bayat: %s katalogda yok — satırı sil", name)
		}
	}
}
