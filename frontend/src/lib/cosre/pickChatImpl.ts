// pickChatImpl — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §1.2). SAF: kabuk kapısı
// (CopilotChatGate) bir açılma niyetini hangi sohbet gerçeklemesine yollar.
//
//   cosreV2 kapalı / bilinmiyor → HER niyet v1 (bugünkü davranış, değişmez).
//   cosreV2 açık → HER niyet v2: FAB / ?chat= / coremetry:ai-ask /
//   AI_PREFILL_EVENT ve v0.10.1672 (B7) itibarıyla `?ai=<özne>` açıklama turu
//   da (v2 artık context.explainAsk + takip bağlamını taşıyor; Q1-a geçiş dalı
//   silindi).
export type ChatIntent = 'fab' | 'chatParam' | 'ask' | 'prefill' | 'aiSubject';
export type ChatImpl = 'v1' | 'v2';

export function pickChatImpl(cfg: { cosreV2?: boolean } | null | undefined, _intent: ChatIntent): ChatImpl {
  return cfg?.cosreV2 === true ? 'v2' : 'v1';
}
