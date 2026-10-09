// chatWikiRefs.ts — v0.10.1134: wiki takip sorusu için önceki cevabın
// kaynak sayfaları.
//
// Sohbet geçmişi sunucuya yalnız {role,text} gider; önceki cevabın "Wiki · "
// çipleri ve kaynakları (yapısal veri) hiç ulaşmıyordu. Operatör (prod):
// "pipeline linki nedir" takibi önceki cevabın sayfasını bilmediği için
// wiki'de yalnız "pipeline linki" arandı. Bu yardımcı SON asistan turunun
// wiki href'lerini toplar; istek gövdesinde context.wikiRefs olarak gider
// (internal/api/chat_wiki_followup.go). Son tur wiki cevabı değilse boş.

import type { ChatTurn } from '../../lib/types';

/** Sunucunun wiki çip/kaynak etiket öneki (chat_wiki.go ragWikiLinks, wikiHitSource). */
export const WIKI_LABEL_PREFIX = 'Wiki · ';
const MAX_REFS = 4;

const isHttp = (u: string) => u.startsWith('https://') || u.startsWith('http://');

/** SAF: son (hatasız, tamamlanmış) asistan turunun wiki sayfa href'leri. */
export function prevWikiRefs(turns: readonly ChatTurn[]): string[] {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i];
    if (t.role !== 'assistant' || t.error || t.pending) continue;
    const out: string[] = [];
    const add = (u?: string) => {
      const v = (u ?? '').trim();
      if (v && isHttp(v) && !out.includes(v) && out.length < MAX_REFS) out.push(v);
    };
    for (const l of t.links ?? []) if (l.label.startsWith(WIKI_LABEL_PREFIX)) add(l.href);
    for (const s of t.sources ?? []) if (s.doc.startsWith(WIKI_LABEL_PREFIX)) add(s.ref);
    return out;
  }
  return [];
}
