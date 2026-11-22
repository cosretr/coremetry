// followupSend — v0.10.1291: FollowupChips'in saf yardımcısı (vitest node).
import type { ChatFollowup } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';

/** followupSendOpts — SAF: çipin kapsamı/komutu → send ikinci argümanı (yoksa undefined: istek gövdesi eski). */
export function followupSendOpts(f: ChatFollowup): ChatSendOpts | undefined {
  // v0.10.1400 — açıklama çipi: yapısal niyet (sohbet içi açıklama turu).
  if (f.explainAsk) return { explainAsk: f.explainAsk };
  // v0.10.2466 — "daha uzun bekle" çipi: evet = bu tur okumaları uzun bütçeyle.
  if (f.longWait) return { longWait: true };
  if (!f.scope && !f.command) return undefined;
  return { parsed: { ...(f.scope ? { scope: f.scope } : {}), ...(f.command ? { command: f.command } : {}) } };
}
