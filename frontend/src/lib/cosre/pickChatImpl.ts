// pickChatImpl — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §1.2). SAF: kabuk kapısı
// (CopilotChatGate) bir açılma niyetini hangi sohbet gerçeklemesine yollar.
//
//   cosreV2 kapalı / bilinmiyor → HER niyet v1 (bugünkü davranış, değişmez).
//   cosreV2 açık → FAB / ?chat= / coremetry:ai-ask / AI_PREFILL_EVENT → v2.
//   `?ai=<özne>` açıklama turu → B7'ye dek v1 (operatör Q1-a, 2026-10-30):
//   v2 henüz `context.explainAsk` taşımıyor; Explain düğmeleri çalışır kalsın.
//   B7 bu satırı v2'ye çevirir (tablo testi tersine döner).
export type ChatIntent = 'fab' | 'chatParam' | 'ask' | 'prefill' | 'aiSubject';
export type ChatImpl = 'v1' | 'v2';

export function pickChatImpl(cfg: { cosreV2?: boolean } | null | undefined, intent: ChatIntent): ChatImpl {
  if (cfg?.cosreV2 !== true) return 'v1';
  return intent === 'aiSubject' ? 'v1' : 'v2';
}
