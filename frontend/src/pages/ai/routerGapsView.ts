// routerGapsView — v0.10.2448 (operatör, 2026-11-22: "kimler yazmış göremiyorum",
// aynı soru ~10 sn arayla "Genel soru" + "Serbest döngü" iki satır): /ai
// "Router boşlukları" panelinin saf yardımcıları. Sunucu satırı TUR başına
// sayar (chstore.RouterGaps exchange kökü); e-postalar uç yalnız admin'e açık.

import type { RouterGap, RouterGapKind } from '@/lib/types';

// v0.10.2465 (operator-reported) — netleştirme turları (kaynak sorusu
// doc_or_entity_ask vb., LLM'siz) da router boşluğu: "Netleştirme" türü + süzgeç.
export const GAP_KIND_LABEL: Record<RouterGapKind, string> = { loop: 'Serbest döngü', general: 'Genel soru', clarify: 'Netleştirme' };

export type GapKindFilter = 'all' | RouterGapKind;

/** Tür süzgeci seçenekleri (Tümü önce; sayılar satır sayısı). */
export function gapKindOptions(gaps: Pick<RouterGap, 'kind'>[]): { value: GapKindFilter; label: string }[] {
  const n = (k: RouterGapKind) => gaps.filter(g => g.kind === k).length;
  return [
    { value: 'all', label: `Tümü · ${gaps.length}` },
    ...(['loop', 'general', 'clarify'] as const).map(k => ({ value: k, label: `${GAP_KIND_LABEL[k]} · ${n(k)}` })),
  ];
}

/** Tür süzgeci uygulanmış satırlar (sunucu sırası korunur). */
export function filterGapsByKind<T extends Pick<RouterGap, 'kind'>>(gaps: T[], kind: GapKindFilter): T[] {
  return kind === 'all' ? gaps : gaps.filter(g => g.kind === kind);
}

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
