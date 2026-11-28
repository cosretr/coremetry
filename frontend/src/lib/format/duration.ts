import { NO_DATA, isNum, type Lang } from './lang';
import { localize, sig3Plain } from './number';

// lib/format/duration — süre biçimi (v0.10.2484, denetim BÖLÜM 2, A7).
//
// 3 anlamlı hane, TEK birim, birim yazımı sabit: "850 ms", "1,24 s",
// "12,4 s", "3,2 dk", "1,5 sa", "2 gün" (EN "min", "h", "d"). Asla
// "1m 12s" gibi iki birim — tek istisna "açık süre" (fmtStdDuration,
// "1 sa 10 dk"), o kendi dosyasında kalır. Birim sınırında yuvarlama
// üst birime taşır: 999,7 ms → "1 s", 59,98 s → "1 dk".

type Unit = { size: number; next: number; tr: string; en: string };

// size: milisaniye cinsinden; next: bir üst birime geçiş eşiği (bu birimde).
const UNITS: Unit[] = [
  { size: 1e-6, next: 1000, tr: 'ns', en: 'ns' },
  { size: 1e-3, next: 1000, tr: 'µs', en: 'µs' },
  { size: 1, next: 1000, tr: 'ms', en: 'ms' },
  { size: 1e3, next: 60, tr: 's', en: 's' },
  { size: 6e4, next: 60, tr: 'dk', en: 'min' },
  { size: 3.6e6, next: 24, tr: 'sa', en: 'h' },
  { size: 8.64e7, next: Infinity, tr: 'gün', en: 'd' },
];

/** Milisaniye → tek birimli, 3 anlamlı haneli süre. */
export function duration(ms: number | null | undefined, lang: Lang): string {
  if (!isNum(ms)) return NO_DATA;
  if (ms < 0) return '-' + duration(-ms, lang);
  if (ms === 0) return '0 ms';
  let i = UNITS.length - 1;
  while (i > 0 && ms < UNITS[i].size) i--;
  let s = sig3Plain(ms / UNITS[i].size);
  while (Number(s) >= UNITS[i].next && i < UNITS.length - 1) {
    i++;
    s = sig3Plain(ms / UNITS[i].size);
  }
  const u = UNITS[i];
  return `${localize(s, lang)} ${lang === 'tr' ? u.tr : u.en}`;
}

/** Nanosaniye girişli kardeşi (span süreleri). */
export function durationNs(ns: number | null | undefined, lang: Lang): string {
  return isNum(ns) ? duration(ns / 1e6, lang) : NO_DATA;
}

/** Saniye girişli kardeşi. */
export function durationSec(sec: number | null | undefined, lang: Lang): string {
  if (!isNum(sec)) return NO_DATA;
  return sec === 0 ? '0 s' : duration(sec * 1000, lang);
}
