// resetReducer — v0.10.1618 (CoSRE v2 Faz 2a): `data-reset` indirgeyicisi (SAF).
//
// Operatör kuralı 2026-10-30 ("(2) revizyonu KESİN", ai@5.0.276 doğrulaması):
//   - onData YALNIZ `messageId → {text parçası sırası}` kümesine yazar
//     (recordReset). Akış sırasında setMessages ile metni boşaltmak bir
//     sonraki chunk'ta SDK'nın iç kopyasıyla EZİLİR (aiparse/reducer.mjs).
//   - Render akış boyunca kümedeki text parçalarını GİZLER (visibleParts).
//   - onFinish (normal / error / closed / stop — hepsinde tetiklenir) o
//     parçaları kalıcı siler (applyResets); onError'da da koşar → idempotent.
//   - "Son mesaj / son text parçası" kestirmesi YASAK: hedef = (messageId,
//     sıra), sıra textIdMap.ts'ten.
//
// İdempotans: silinen parçaların ÖZGÜN sıraları metadata.resetApplied'da.
// Kalan text parçalarının özgün sırası bu listeyi atlayarak yeniden kurulur;
// t1 silindikten sonra t2 sıra 0'a kaymaz. SDK mesajı iç kopyasıyla geri
// yazarsa metadata da eski olur → çift tutarlı, temizlik yeniden uygulanır.
import type { UIMessage } from 'ai';
import type { CosreMessageMetadata } from '@/lib/types';

export type CosreUIMessage = UIMessage<CosreMessageMetadata>;
type Part = CosreUIMessage['parts'][number];

/** messageId → reset edilen text parçası sıraları (özgün numaralama). */
export type ResetSet = Map<string, Set<number>>;

export function recordReset(set: ResetSet, messageId: string, ordinal: number): void {
  let s = set.get(messageId);
  if (!s) set.set(messageId, (s = new Set()));
  s.add(ordinal);
}

/** Her parça için özgün text sırası (text değilse -1). */
function originalOrdinals(parts: Part[], applied: readonly number[] | undefined): number[] {
  const skip = new Set(applied ?? []);
  let o = 0;
  return parts.map(p => {
    if (p.type !== 'text') return -1;
    while (skip.has(o)) o++;
    return o++;
  });
}

/** Akış sırasında çizilecek parçalar: kümedeki text parçaları gizli. */
export function visibleParts(message: CosreUIMessage, resets: ResetSet): Part[] {
  const r = resets.get(message.id);
  if (!r?.size) return message.parts;
  const ords = originalOrdinals(message.parts, message.metadata?.resetApplied);
  return message.parts.filter((_, i) => !r.has(ords[i]));
}

/** Kümedeki text parçalarını kalıcı siler. Değişiklik yoksa AYNI dizi. */
export function applyResets(messages: CosreUIMessage[], resets: ResetSet): CosreUIMessage[] {
  let changed = false;
  const out = messages.map(m => {
    const r = resets.get(m.id);
    if (!r?.size) return m;
    const ords = originalOrdinals(m.parts, m.metadata?.resetApplied);
    const drop = ords.filter(o => o >= 0 && r.has(o));
    if (drop.length === 0) return m;
    changed = true;
    const applied = [...new Set([...(m.metadata?.resetApplied ?? []), ...drop])].sort((a, b) => a - b);
    return {
      ...m,
      parts: m.parts.filter((_, i) => !r.has(ords[i])),
      metadata: { ...m.metadata, resetApplied: applied },
    };
  });
  return changed ? out : messages;
}

/** onFinish bayrakları (isAbort → partial, isError → error). İdempotent. */
export function markFinished(
  messages: CosreUIMessage[], id: string, flags: { partial?: boolean; error?: boolean },
): CosreUIMessage[] {
  const set = (Object.keys(flags) as Array<keyof typeof flags>).filter(k => flags[k]);
  if (set.length === 0) return messages;
  const i = messages.findIndex(m => m.id === id);
  if (i < 0 || set.every(k => messages[i].metadata?.[k] === true)) return messages;
  const m = messages[i];
  const next = [...messages];
  next[i] = { ...m, metadata: { ...m.metadata, ...Object.fromEntries(set.map(k => [k, true])) } };
  return next;
}
