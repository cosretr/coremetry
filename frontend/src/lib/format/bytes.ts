import { NO_DATA, isNum, type Lang } from './lang';
import { localize } from './number';

// lib/format/bytes — bayt biçimi (v0.10.2484, A7: IEC birimleri).
//
// KiB / MiB / GiB / TiB / PiB (1024 tabanı) — SI "GB" değil. Hane kuralı
// v0.8.272'den: <10 → 2 ondalık, <100 → 1, değilse 0 (≈3 anlamlı hane);
// `fixed` verilirse sabit ondalık (v0.10.2194, "248.3 MiB"). Ondalık
// ayırıcı dile göre (TR virgül). Veri yoksa "—"; negatif işaretle basılır.

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

export function bytes(n: number | null | undefined, lang: Lang, fixed?: number): string {
  if (!isNum(n)) return NO_DATA;
  if (n < 0) return '-' + bytes(-n, lang, fixed);
  let i = 0;
  let v = n;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  if (i === 0) return `${Math.round(v)} B`;
  const digits = fixed != null ? fixed : v < 10 ? 2 : v < 100 ? 1 : 0;
  let s = v.toFixed(digits);
  // Yuvarlama 1024'e taşıyorsa ("1024 KiB") bir üst birim.
  if (fixed == null && Number(s) >= 1024 && i < UNITS.length - 1) {
    i++;
    s = (v / 1024).toFixed(2);
  }
  return `${localize(s, lang)} ${UNITS[i]}`;
}
