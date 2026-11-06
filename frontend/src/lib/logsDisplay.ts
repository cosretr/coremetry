// logsDisplay — v0.10.1830 (Log Search Faz 1 dilim 1.7; audit §8, mockup
// görünüm 1 "Görünüm ▾").
//
// Doküman tablosunun görünüm seçenekleri URL'de, EKLENEN parametrelerle:
//   rh  gövde satır yüksekliği   1 | 2 | 3 | auto
//   hh  başlık satır yüksekliği  1 | 2 | 3 | auto
//   den yoğunluk                 compact | normal | expanded
//   pp  sayfa başına satır       50 | 100 | 250 | 500
// Parametre YOKSA bugünkü görünüm: rh = tarayıcının "⤶ sar" tercihi (sarılı
// → auto, değilse 1), hh 1, den normal (genel yoğunluk ayarını izler), pp 100.
// Bilinmeyen değer yok sayılır (o anahtar varsayılana düşer). Saf, tablo testli.
import type { LogsDisplay, LogsLines, LogsDensity, LogsPageSize } from './types';

export const LOGS_DISPLAY_PARAMS = ['rh', 'hh', 'den', 'pp'] as const;
export type LogsDisplayParam = typeof LOGS_DISPLAY_PARAMS[number];

export const LOGS_PAGE_SIZES: readonly LogsPageSize[] = [50, 100, 250, 500];
export const LOGS_DEFAULT_DISPLAY: LogsDisplay = { rowLines: 1, headerLines: 1, density: 'normal', pageSize: 100 };

export function parseLines(v: string | null | undefined): LogsLines | null {
  if (v === '1' || v === '2' || v === '3') return Number(v) as LogsLines;
  if (v === 'auto') return 'auto';
  return null;
}
export function parseDensity(v: string | null | undefined): LogsDensity | null {
  return v === 'compact' || v === 'normal' || v === 'expanded' ? v : null;
}
export function parsePageSize(v: string | null | undefined): LogsPageSize | null {
  const n = Number(v);
  return (LOGS_PAGE_SIZES as readonly number[]).includes(n) ? (n as LogsPageSize) : null;
}

/** URL → görünüm. `base` = parametre yokken geçerli değerler (varsayılan ya
 *  da kullanıcı tercihi); `explicit` = URL'de geçerli değeri olan anahtarlar. */
export function readLogsDisplay(p: URLSearchParams, base: LogsDisplay = LOGS_DEFAULT_DISPLAY): { display: LogsDisplay; explicit: LogsDisplayParam[] } {
  const rh = parseLines(p.get('rh'));
  const hh = parseLines(p.get('hh'));
  const den = parseDensity(p.get('den'));
  const pp = parsePageSize(p.get('pp'));
  const explicit: LogsDisplayParam[] = [];
  if (rh !== null) explicit.push('rh');
  if (hh !== null) explicit.push('hh');
  if (den !== null) explicit.push('den');
  if (pp !== null) explicit.push('pp');
  return {
    display: {
      rowLines: rh ?? base.rowLines,
      headerLines: hh ?? base.headerLines,
      density: den ?? base.density,
      pageSize: pp ?? base.pageSize,
    },
    explicit,
  };
}

/** Seçilen değeri URL'ye yazar (yabancı parametreler korunur). Açık seçim
 *  her zaman yazılır — varsayılana eşit olsa bile: paylaşılan link
 *  gönderenin görünümünü birebir açar. */
export function writeLogsDisplay(prev: URLSearchParams, patch: Partial<LogsDisplay>): URLSearchParams {
  const next = new URLSearchParams(prev);
  if (patch.rowLines !== undefined) next.set('rh', String(patch.rowLines));
  if (patch.headerLines !== undefined) next.set('hh', String(patch.headerLines));
  if (patch.density !== undefined) next.set('den', patch.density);
  if (patch.pageSize !== undefined) next.set('pp', String(patch.pageSize));
  return next;
}

/** İstemci sayfalaması: yüklenmiş örneğin sayfa sayısı ve geçerli dilim. */
export function pageSlice<T>(rows: readonly T[], page: number, pageSize: number): { rows: T[]; page: number; pages: number } {
  const pages = Math.max(1, Math.ceil(rows.length / pageSize));
  const p = Math.min(Math.max(0, page), pages - 1);
  return { rows: rows.slice(p * pageSize, (p + 1) * pageSize), page: p, pages };
}
