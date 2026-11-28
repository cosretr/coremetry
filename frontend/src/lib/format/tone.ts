// lib/format/tone — hata oranı sapma rengi (v0.10.2484, denetim §3.4).
//
// Hata oranı eşiği sağlık değil, hücredeki SAPMA rengidir; tek yerde:
// yüzde (0..100) ≥ 5 → 'err', ≥ 1 → 'warn', değilse renk yok
// (K5: sağlıklı renk almaz). Veri yoksa da renk yok.

export type ErrorTone = 'err' | 'warn' | undefined;

export function errorTone(pct: number | null | undefined): ErrorTone {
  if (typeof pct !== 'number' || !Number.isFinite(pct)) return undefined;
  if (pct >= 5) return 'err';
  if (pct >= 1) return 'warn';
  return undefined;
}
