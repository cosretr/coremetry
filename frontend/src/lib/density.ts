// density — v0.10.933 (tablo standardı, operatör cevabı S7): yoğunluk
// dört basamaktan ÜÇE indi — comfortable · compact · dense. "spacious"
// kalktı: T5'ten sonra yoğunluk ayarı her hücreye ulaşıyor, en geniş
// basamak ayrı bir ritim (14px gövde, 10/14 dolgu) taşıyordu ve tek
// `--row-h` merdiveninin dışında kalıyordu.
//
// Saf yardımcı: DensityToggle ve index.html'in açılış betiği aynı kuralı
// uygular — saklı "spacious" (ya da tanınmayan her değer) "comfortable"a
// göçer.
import { setRaw } from '@/lib/storage';

export type Density ='comfortable' | 'compact' | 'dense';

/** Döngü sırası — tık bir basamak sıklaştırır, dense'ten başa döner. */
export const DENSITY_STEPS: readonly Density[] = ['comfortable', 'compact', 'dense'];

export function normalizeDensity(stored: string | null | undefined): Density {
  return (DENSITY_STEPS as readonly string[]).includes(stored ?? '')
    ? (stored as Density)
    : 'comfortable';
}

// v0.10.1665 (etkileşim P3, plan §1.2) — yazıcı DensityToggle'dan buraya
// çıkarıldı: ⌘K "density-*" eylemleri ve üst çubuk düğmesi AYNI yolu yazar
// (aynı saklama anahtarı — index.html açılış betiği de bunu okur — aynı
// <html data-density> özniteliği). Düğmenin döngüsü yerel state yerine
// `currentDensity()` okur; paletten yapılan seçim sonraki tıkı bozmaz.
export const DENSITY_STORAGE_KEY = 'coremetry-density';
export const DENSITY_EVENT = 'coremetry:density';

/** Şu anki yoğunluk (<html data-density>); belge yoksa comfortable. */
export function currentDensity(): Density {
  if (typeof document === 'undefined') return 'comfortable';
  return normalizeDensity(document.documentElement.getAttribute('data-density'));
}

/** Döngüde sıradaki basamak (dense → comfortable). */
export function nextDensity(d: Density): Density {
  return DENSITY_STEPS[(DENSITY_STEPS.indexOf(d) + 1) % DENSITY_STEPS.length];
}

/** Yoğunluğu uygular + kalıcı yazar; düğmenin glifi DENSITY_EVENT ile güncellenir. */
export function applyDensity(d: Density): void {
  document.documentElement.setAttribute('data-density', d);
  setRaw(DENSITY_STORAGE_KEY, d);
  window.dispatchEvent(new CustomEvent(DENSITY_EVENT, { detail: d }));
}
