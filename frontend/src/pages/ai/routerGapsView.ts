// routerGapsView — v0.10.2448 (operatör, 2026-11-22: "kimler yazmış göremiyorum",
// aynı soru ~10 sn arayla "Genel soru" + "Serbest döngü" iki satır): /ai
// "Router boşlukları" panelinin saf yardımcıları. Sunucu satırı TUR başına
// sayar (chstore.RouterGaps exchange kökü); e-postalar uç yalnız admin'e açık.

import type { RouterGap } from '@/lib/types';

/** "Kim" hücresi: soranların e-postaları; e-postasız (eski) satır "". */
export function gapWho(g: Pick<RouterGap, 'emails'>): string {
  return (g.emails ?? []).filter(Boolean).join(', ');
}

/** Tür hücresinin title'ı: önce «none» sonra serbest döngüye düşen tur sayısı. */
export function gapChainTitle(g: Pick<RouterGap, 'kind' | 'viaGeneral'>): string | undefined {
  const n = g.viaGeneral ?? 0;
  if (g.kind !== 'loop' || n <= 0) return undefined;
  return `${n.toLocaleString('tr-TR')} tur önce genel soru (sınıflandırıcı «none») → sonra serbest döngü; tek tur sayıldı`;
}
