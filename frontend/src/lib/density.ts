// density — v0.10.933 (tablo standardı, operatör cevabı S7): yoğunluk
// dört basamaktan ÜÇE indi — comfortable · compact · dense. "spacious"
// kalktı: T5'ten sonra yoğunluk ayarı her hücreye ulaşıyor, en geniş
// basamak ayrı bir ritim (14px gövde, 10/14 dolgu) taşıyordu ve tek
// `--row-h` merdiveninin dışında kalıyordu.
//
// Saf yardımcı: DensityToggle ve index.html'in açılış betiği aynı kuralı
// uygular — saklı "spacious" (ya da tanınmayan her değer) "comfortable"a
// göçer.
export type Density = 'comfortable' | 'compact' | 'dense';

/** Döngü sırası — tık bir basamak sıklaştırır, dense'ten başa döner. */
export const DENSITY_STEPS: readonly Density[] = ['comfortable', 'compact', 'dense'];

export function normalizeDensity(stored: string | null | undefined): Density {
  return (DENSITY_STEPS as readonly string[]).includes(stored ?? '')
    ? (stored as Density)
    : 'comfortable';
}
