package api

// chat_history_sanitize.go — v0.10.948 (CoSRE araştırma asistanı, Faz B):
// istemcinin gönderdiği sohbet geçmişi YALNIZ metin turlarıdır.
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// /api/copilot/chat gövdesinin `messages` dizisi provider.ChatMessage'a
// doğrudan çözülüyordu: Role, Text, ToolCalls, ToolResults etiketsiz alanlar
// ve Go'nun büyük/küçük harfe duyarsız eşleşmesi `toolCalls` / `toolResults`
// anahtarlarını da kabul ediyor. Yani bir istemci, sunucunun HİÇ
// yürütmediği bir araç çağrısını ve "sonucunu" (ör. uydurma bir get_trace
// çıktısı ya da "system" rolünde talimat) konuşmaya enjekte edebilirdi; model
// bunu kendi araç kanıtı sanar ve künye onu gerçek veri gibi anlatırdı.
// Rol de süzülmüyordu: "system" / "tool" rolü sağlayıcıya olduğu gibi gidiyordu.
//
// Frontend bugün yalnız {role, text} gönderir (useChatThread.ts); araç
// turları yalnız TEK alışverişin içinde, sunucuda yaşar (copilot_chat.go
// conv). Sözleşme bu yüzden daraltılıyor, genişletilmiyor: rol yalnız
// user/assistant; araç çağrısı, araç sonucu ve ham içerik düşer; metni boş
// kalan tur (saf araç turu) atılır. Redaksiyon DEĞİL: metin baytları aynen
// kalır — yalnız istemcinin kuramayacağı alanlar düşer.

import (
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// sanitizeClientHistory — SAF. Dönen dilim yeni bir kopyadır; `dropped`
// atılan tur, `stripped` araç alanı soyulan (ama metni kalan) tur sayısı.
func sanitizeClientHistory(in []copilot.ChatMessage) (out []copilot.ChatMessage, dropped, stripped int) {
	out = make([]copilot.ChatMessage, 0, len(in))
	for _, m := range in {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			dropped++
			continue
		}
		if strings.TrimSpace(m.Text) == "" {
			dropped++
			continue
		}
		if len(m.ToolCalls) > 0 || len(m.ToolResults) > 0 || len(m.RawContent) > 0 {
			stripped++
		}
		out = append(out, copilot.ChatMessage{Role: role, Text: m.Text})
	}
	return out, dropped, stripped
}
