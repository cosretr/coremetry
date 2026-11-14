// dbQueryShare.ts — v0.10.2142 (servis sekmeleri S4). SAF.

/** sharePct — satırın toplam süresinin, LİSTELENEN ifadelerin toplamına oranı (%). */
export function sharePct(totalMs: number, sumMs: number): number | null {
  if (!(sumMs > 0) || !(totalMs >= 0)) return null;
  return (totalMs / sumMs) * 100;
}
