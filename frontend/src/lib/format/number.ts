import { NO_DATA, isNum, type Lang } from './lang';

// lib/format/number — sayı biçimi (v0.10.2484, denetim BÖLÜM 2).
//
// Kurallar (A7, operatör onaylı): TR arayüzde ondalık ayırıcı HER YERDE
// virgül, binlik ayırıcı nokta ("1.234,5"); EN "1,234.5". Ondalıklı değer
// 3 anlamlı hane, sondaki sıfırlar atılır. Intl kullanılmaz: elle
// yerelleştirme girişte yer tutmaz ve çağrı başına biçimleyici kurmaz.
// Veri yoksa (null / undefined / NaN / ±Infinity) "—".

/** "1234.5" gibi düz bir sayı dizesini dile göre yerelleştirir (binlik + ondalık). */
export function localize(plain: string, lang: Lang): string {
  const neg = plain.startsWith('-');
  const body = neg ? plain.slice(1) : plain;
  const dot = body.indexOf('.');
  const int = dot < 0 ? body : body.slice(0, dot);
  const frac = dot < 0 ? '' : body.slice(dot + 1);
  const group = lang === 'tr' ? '.' : ',';
  const dec = lang === 'tr' ? ',' : '.';
  const grouped = int.replace(/\B(?=(\d{3})+(?!\d))/g, group);
  return (neg ? '-' : '') + grouped + (frac ? dec + frac : '');
}

/** 3 anlamlı haneye yuvarlanmış düz dize ("1.24", "12.4", "124", "1235"); sondaki sıfırlar atılır. */
export function sig3Plain(v: number): string {
  if (v === 0) return '0';
  const abs = Math.abs(v);
  const decimals = Math.max(0, 2 - Math.floor(Math.log10(abs)));
  let s = abs.toFixed(Math.min(decimals, 20));
  if (s.includes('.')) s = s.replace(/0+$/, '').replace(/\.$/, '');
  if (s === '0') return '0';
  return (v < 0 ? '-' : '') + s;
}

/** Ondalıklı değer, 3 anlamlı hane, dile göre: TR "12,4" / EN "12.4". */
export function decimal(v: number | null | undefined, lang: Lang): string {
  return isNum(v) ? localize(sig3Plain(v), lang) : NO_DATA;
}

/** Sabit ondalık hane, dile göre: fixed(1234.5, 1, 'tr') → "1.234,5". */
export function fixed(v: number | null | undefined, digits: number, lang: Lang): string {
  return isNum(v) ? localize(v.toFixed(digits), lang) : NO_DATA;
}

/** Tam sayı sayaç, binlik ayırıcılı: TR "1.234.567" / EN "1,234,567". */
export function count(v: number | null | undefined, lang: Lang): string {
  return isNum(v) ? localize(String(Math.round(v)), lang) : NO_DATA;
}

const COMPACT_EN = ['K', 'M', 'B', 'T'];
// v0.10.2484 (operatör): "B" bayt / milyar ile karışıyordu → açık yazım.
const COMPACT_TR = [' bin', ' mn', ' mr', ' tn'];

/**
 * Kısa sayı: EN "1.2K" / "3.4M" / "2.8B" / "1.0T"; TR "1,2 bin" / "3,4 mn" /
 * "2,8 mr" / "1,0 tn". 1000'in altı tam sayıysa olduğu gibi, değilse 3 anlamlı
 * hane. Grafik eksenleri bunu DEĞİL `lib/chartFmt` fmtSmart'ı kullanır.
 */
export function compact(v: number | null | undefined, lang: Lang): string {
  if (!isNum(v)) return NO_DATA;
  const abs = Math.abs(v);
  const sign = v < 0 ? '-' : '';
  const suffixes = lang === 'tr' ? COMPACT_TR : COMPACT_EN;
  const steps = [1e3, 1e6, 1e9, 1e12];
  for (let i = steps.length - 1; i >= 0; i--) {
    if (abs >= steps[i]) return sign + localize((abs / steps[i]).toFixed(1), lang) + suffixes[i];
  }
  return Number.isInteger(v) ? String(v) : decimal(v, lang);
}
