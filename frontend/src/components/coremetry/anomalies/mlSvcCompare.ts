// mlSvcCompare — v0.10.2089: servis düzeyi karşılaştırma tablosunun SAF metinleri.

export const SVC_DIFF_TR: Record<string, string> = {
  matched: 'Eşleşti', missed: 'Kaçırdı', extra: 'Fazladan', late: 'Geç (15 dk+)',
};
/** SAF (test): özet satırı — sayılar + kesik okuma uyarısı. */
export function svcCompareSummary(counts: Record<string, number>, days: number, truncated: boolean): string {
  const n = (k: string) => counts[k] ?? 0;
  const s = `Son ${days} gün: ${n('matched')} eşleşti, ${n('missed')} kaçırdı, ${n('extra')} fazladan, ${n('late')} geç.`;
  return truncated ? `${s} Okuma tavanına değdi; en eski satırlar eksik olabilir.` : s;
}

