// traceMetricsFmt.ts — v0.10.968 — Trace › Metrics sayı biçimleri (SAF).
//
// v0.10.968 — Onaylı mockup'ın kopyası Türkçe ondalık virgülle yazılı
// ("1,21 sn", "0,97 çekirdek", "%97"). Tarayıcı yerel ayarına (Intl /
// toLocaleString) BAĞLANMAZ: en-US bir Chrome "0.97" basardı ve aynı
// satırdaki kopya iki ayrı dil konuşurdu. toFixed + virgül değişimi; sondaki
// sıfırlar atılır ("1 çekirdek", "2 GiB", "0,5 çekirdek").
import { fmtPctLang, tf, type Lang } from '@/lib/i18n';

/** v0.10.968 — toFixed(d) + ondalık virgül; sondaki sıfırlar (ve yalnız kalan virgül) atılır. */
// v0.10.2316 — `lang` (varsayılan 'tr'): EN ondalık nokta + İngilizce birim
// sözcüğü; TR çıktısı birebir aynı. Yalnız trace pod paneli 'en' geçirir.
function dec(v: number, d: number, lang: Lang = 'tr'): string {
  let s = v.toFixed(d);
  if (s.includes('.')) s = s.replace(/0+$/, '').replace(/\.$/, '');
  if (s === '-0') s = '0';
  return lang === 'tr' ? s.replace('.', ',') : s;
}

/** v0.10.968 — süre: ≥1 sn iki ondalık ("1,21 sn"), altı ms ("440 ms", "1,5 ms", "0,35 ms"). */
export function fmtDurNs(ns: number, lang: Lang = 'tr'): string {
  if (!Number.isFinite(ns) || ns <= 0) return '0 ms';
  const s = ns / 1e9;
  if (s >= 1) return lang === 'tr' ? `${s.toFixed(2).replace('.', ',')} sn` : `${s.toFixed(2)} s`;
  const ms = ns / 1e6;
  if (ms >= 10) return `${Math.round(ms)} ms`;
  if (ms >= 1) return `${dec(ms, 1, lang)} ms`;
  return `${dec(ms, 2, lang)} ms`;
}

function coreNum(v: number, lang: Lang = 'tr'): string {
  if (!Number.isFinite(v)) return '—';
  const a = Math.abs(v);
  if (a >= 100) return dec(v, 0, lang);
  if (a > 0 && a < 0.1) return dec(v, 3, lang);
  return dec(v, 2, lang);
}

/** v0.10.968 — çekirdek: "0,97 çekirdek", "1 çekirdek", "0,004 çekirdek". */
export function fmtCores(v: number, lang: Lang = 'tr'): string {
  return tf('tpp.cores', lang, { n: coreNum(v, lang) });
}

/** v0.10.968 — tablo hücresi kısa biçimi: "0,62 c". */
export function fmtCoresShort(v: number, lang: Lang = 'tr'): string {
  return `${coreNum(v, lang)} c`;
}

/** v0.10.968 — bayt, ikili birim, iki ondalık: "1,42 GiB", "512 MiB", "2 GiB". */
export function fmtBytesTr(v: number, lang: Lang = 'tr'): string {
  if (!Number.isFinite(v)) return '—';
  const a = Math.abs(v);
  const units: [number, string][] = [[1024 ** 4, 'TiB'], [1024 ** 3, 'GiB'], [1024 ** 2, 'MiB'], [1024, 'KiB']];
  for (const [n, u] of units) {
    if (a >= n) return `${dec(v / n, 2, lang)} ${u}`;
  }
  return `${Math.round(v)} B`;
}

/** v0.10.968 — oran → yüzde, tam sayı: 0,97 → "%97". */
export function fmtPct(ratio: number, lang: Lang = 'tr'): string {
  if (!Number.isFinite(ratio)) return '—';
  return fmtPctLang(Math.round(ratio * 100), lang);
}

const pad = (n: number, w = 2) => String(n).padStart(w, '0');

/** v0.10.968 — yerel saat, milisaniyeli: "12:04:31.402" (ns girdi). */
export function fmtClockNs(ns: number): string {
  // ns değerleri 2^53'ü aşar (float kesinliği ±256 ns): taban değil yuvarlama,
  // yoksa .402 ms "…401,99999" olup .401 basılır.
  const ms = Math.round(ns / 1e6);
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

/** v0.10.968 — yerel saat, saniye: "12:16:48" (unix s girdi). */
export function fmtClockSec(s: number): string {
  const d = new Date(s * 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** v0.10.968 — kardeş oranı, bir ondalık: 2,5 → "2,5 katı", 2 → "2 katı". */
export function fmtRatio(x: number, lang: Lang = 'tr'): string {
  if (!Number.isFinite(x)) return '—';
  return tf('tpp.ratio', lang, { n: dec(x, 1, lang) });
}
