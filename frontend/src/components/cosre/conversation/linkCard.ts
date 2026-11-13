// linkCard — v0.10.2101 (docs/cosre/experience-audit.md D4). SAF.
// Sunucu üç guided rotada (servis sağlığı, problemler, takımımın servisleri)
// data-links'e deterministik title + summary ekler; kart ↔ çip seçimi
// title'ın VARLIĞIYLA: kart varsa aynı linkler balonda çip olarak çizilmez
// (iki kez görünmesin), yoksa bugünkü çipler aynen.
import type { ChatTurn, CosreLinkCard } from '@/lib/types';
import { parseDataLinksCard } from '@/lib/cosre/protocol';

type Part = { type: string; data?: unknown };

/** Mesajın kart yükü (son geçerli data-links kartı; yoksa null). */
export function linkCardOf(parts: readonly Part[]): CosreLinkCard | null {
  let out: CosreLinkCard | null = null;
  for (const p of parts) {
    if (p.type === 'data-links') out = parseDataLinksCard(p);
  }
  return out;
}

/** Kart çizildiğinde balonun turu: kartın linkleri çip satırından çıkar, ama
 *  cevap metnindeki aynı linkler satır içi kalabilsin diye allowedLinks'e girer. */
export function bubbleTurnForCard(turn: ChatTurn, card: CosreLinkCard | null): ChatTurn {
  if (!card || !turn.links?.length) return turn;
  const hrefs = new Set(card.links.map(l => l.href));
  const rest = turn.links.filter(l => !hrefs.has(l.href));
  return {
    ...turn,
    links: rest.length ? rest : undefined,
    allowedLinks: [...(turn.allowedLinks ?? []), ...hrefs],
  };
}
