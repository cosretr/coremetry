// Package uistream — CoSRE v2 sohbetinin taşıması: v1 sohbet olaylarını
// (`step`, `step-result`, `delta`, `answer`, `block`, `error`, `code-lock`,
// `done`) Vercel AI SDK v5 "UI Message Stream" parçalarına çevirir ve SSE
// olarak yazar (v0.10.1591, CoSRE v2 Faz 1 R3).
//
// Konum: internal/api'deki sohbet gövdesinin (copilotChatWith) EN ALT
// yazıcısı. Üstteki sarım zinciri v1 sözlüğünde konuşur; bu paket o
// çerçeveleri tamamen süslenmiş hâlde (i, bN/seq/final, callId/turnId) görür.
// internal/api'yi içe aktarmaz.
//
// Tel biçimi varsayımları (frontend'de pinli `ai` paketi henüz yok; Faz 2'de
// node_modules/ai'a karşı doğrulanacak):
//   - yanıt başlığı `x-vercel-ai-ui-message-stream: v1`, `Content-Type:
//     text/event-stream`;
//   - çerçeve `data: <json>\n\n`, çerçeve başına bir parça, `event:` satırı yok;
//   - akışın sonu `data: [DONE]\n\n`;
//   - heartbeat SSE yorumu `: ping\n\n` (ayrıştırıcı yorum satırını atlar);
//   - araç parçaları `dynamic:true` (araç kümesi açık uçlu: 74 araç + MCP);
//   - aynı `id`'li `data-*` parçası öncekinin yerine geçer;
//   - `start` parçası sunucu üretimi `messageId` taşır (ULID, ulid.go;
//     v0.10.1603).
//
// Araç seti dilim 1 (docs/cosre/tools-audit.md §10 G1/G2): her araç
// step-result'ı tool-output-available'ın yanında `data-tool-result{id:s<i>}`
// (tek parça, `kind` içeride, ≤ 2 KB, tool_result.go) üretir; döngünün her
// çıkışı `stop` → `data-stop{reason, calls, rounds, elapsed_ms}`.
//
// Geri alma TEK mekanizmadır: `data-reset{data:{textId:<text-start id>},transient:true}`
// (v0.10.1606; parçanın kendi id'si yok).
// Son cevap, akan taslağı reset'le geri alıp otoriter metni TEK delta olarak
// yollar; önek farkı hesaplanmaz.
package uistream

// ProtocolVersion — HeaderName başlığının değeri.
const ProtocolVersion = "v1"

// HeaderName — AI SDK istemcisinin akışı tanıdığı yanıt başlığı.
const HeaderName = "x-vercel-ai-ui-message-stream"

// DoneMarker — akışın son `data:` satırı.
const DoneMarker = "[DONE]"
