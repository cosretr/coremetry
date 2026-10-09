import type { ChatTurn } from '@/lib/types';
import type { ParsedChatInput } from './chatScope';

// chatRegenerate — v0.10.1138: "↻ Yeniden üret" ve "son mesajı düzenle"nin
// SAF yarısı (useChatThread React'i bunları çağırır; test chatRegenerate.test.ts).

/** Ekranda tutulan önceki cevap sayısı tavanı (en eskiler düşer). */
export const MAX_ANSWER_VERSIONS = 5;

/** lastUserIndex — son kullanıcı turunun indeksi; yoksa -1. */
export function lastUserIndex(turns: readonly ChatTurn[]): number {
  for (let i = turns.length - 1; i >= 0; i--) if (turns[i].role === 'user') return i;
  return -1;
}

/** canRegenerate — son tur TAMAMLANMIŞ (akmıyor) bir asistan cevabı ve
 *  önünde metinli bir kullanıcı sorusu var mı. Hatalı tur "Yeniden dene"nin işi. */
export function canRegenerate(turns: readonly ChatTurn[]): boolean {
  const n = turns.length;
  if (n < 2) return false;
  const last = turns[n - 1];
  const prev = turns[n - 2];
  return last.role === 'assistant' && !last.pending && !last.error && !!last.text
    && prev.role === 'user' && !!prev.text?.trim();
}

/** stripTurn — önceki cevap olarak saklanacak hâl (iç içe geçmiş sürüm yok). */
function stripTurn(t: ChatTurn): ChatTurn {
  const { alternatives: _alts, ...rest } = t;
  void _alts;
  return { ...rest, pending: false };
}

/**
 * regenerateBase — yeniden üretimin girdisi: son soru+cevap düşmüş turlar,
 * aynı soru ve AYNI kapsam/komut, yeni cevabın taşıyacağı önceki cevaplar
 * (eskiden yeniye, en çok MAX_ANSWER_VERSIONS). Koşul sağlanmazsa null.
 */
export function regenerateBase(turns: readonly ChatTurn[]): {
  base: ChatTurn[]; question: string; parsed: ParsedChatInput; alternatives: ChatTurn[];
} | null {
  if (!canRegenerate(turns)) return null;
  const n = turns.length;
  const last = turns[n - 1];
  const user = turns[n - 2];
  const alternatives = [...(last.alternatives ?? []), stripTurn(last)].slice(-MAX_ANSWER_VERSIONS);
  return {
    base: turns.slice(0, n - 2),
    question: user.text ?? '',
    parsed: { ...(user.command ? { command: user.command } : {}), ...(user.scope ? { scope: user.scope } : {}) },
    alternatives,
  };
}

/**
 * answerVersion — SAF: son cevabın sürüm görünümü. idx null/aralık dışı =
 * en yeni (gösterilen). total = önceki cevaplar + 1.
 */
export function answerVersion(t: ChatTurn, idx: number | null): { view: ChatTurn; index: number; total: number } {
  const alts = t.alternatives ?? [];
  const total = alts.length + 1;
  const i = idx === null || idx < 0 || idx >= total ? total - 1 : idx;
  return { view: i === total - 1 ? t : alts[i], index: i, total };
}
