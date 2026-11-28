import { NO_DATA, isNum, type Lang } from './lang';
import { localize, sig3Plain } from './number';

// lib/format/percent — yüzde biçimi (v0.10.2484, denetim BÖLÜM 2).
//
// Girdinin ölçeği TİPTE zorunlu: `from: 'ratio'` (0..1 kesir) ya da
// `from: 'pct'` (0..100). Bugün kesir/yüzde karışık geliyor; ölçek
// tahmin edilmez. 3 anlamlı hane; TR "%12,4", EN "12.4%".
// İstenirse `digits` sabit ondalık verir (ör. tablo hizası).

export interface PercentOpts {
  from: 'ratio' | 'pct';
  digits?: number;
}

export function percent(v: number | null | undefined, lang: Lang, opts: PercentOpts): string {
  if (!isNum(v)) return NO_DATA;
  const p = opts.from === 'ratio' ? v * 100 : v;
  const plain = opts.digits != null ? p.toFixed(opts.digits) : sig3Plain(p);
  const neg = plain.startsWith('-') && Number(plain) !== 0;
  const body = localize(plain.replace(/^-/, ''), lang);
  if (lang === 'tr') return `${neg ? '-' : ''}%${body}`;
  return `${neg ? '-' : ''}${body}%`;
}
