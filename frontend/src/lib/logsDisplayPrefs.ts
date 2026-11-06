// logsDisplayPrefs — v0.10.1832 (Log Search Faz 1 dilim 1.9; audit §8).
//
// /logs görünüm ayarlarının ÖNCELİK kuralı (saf, tablo testli):
//   URL (paylaşılan link / kayıtlı arama — ikisi de URL'yi yazar)
//     > kullanıcı tercihi (GET /api/preferences/logs/display)
//       > varsayılan (bugünkü görünüm; rh için tarayıcının eski "⤶ sar" tercihi)
// Kayıtlı arama (saved_views page='logs') sorgu dizesini saklar; açılınca URL
// olur — yani "URL > kayıtlı arama" ayrımı tek katmandır.
// URL'de olmayan anahtarlar tercihten gelir ve sayfa açılışında URL'ye
// YAZILIR (replace): böylece Share / Kaydet her zaman gönderenin gördüğü
// görünümü taşır (URL tek doğruluk kaynağı).
import type { DisplayPref, LogsDisplay } from './types';
import { parseDensity, parseLines, parsePageSize, type LogsDisplayParam } from './logsDisplay';

export type LogsViewParam = LogsDisplayParam | 'smp';

/** Sunucu tercihi → görünüm alanları + örnek boyu (geçersiz alan düşer). */
export function prefToView(p: DisplayPref | null | undefined): Partial<LogsDisplay> & { sample?: number } {
  if (!p) return {};
  const out: Partial<LogsDisplay> & { sample?: number } = {};
  const rh = parseLines(p.rh ?? null); if (rh !== null) out.rowLines = rh;
  const hh = parseLines(p.hh ?? null); if (hh !== null) out.headerLines = hh;
  const den = parseDensity(p.den ?? null); if (den !== null) out.density = den;
  const pp = parsePageSize(p.pp !== undefined ? String(p.pp) : null); if (pp !== null) out.pageSize = pp;
  if (typeof p.smp === 'number' && Number.isInteger(p.smp) && p.smp >= 10) out.sample = p.smp;
  return out;
}

/** Görünüm → sunucu gövdesi (tam tercih: her alan yazılır). */
export function viewToPref(d: LogsDisplay, sample: number): DisplayPref {
  return { v: 1, rh: String(d.rowLines), hh: String(d.headerLines), den: d.density, pp: d.pageSize, smp: sample };
}

/** Taban (varsayılan) + tercih → URL'de olmayan anahtarların kaynağı. */
export function mergeBase(base: LogsDisplay, pref: DisplayPref | null | undefined): LogsDisplay {
  const p = prefToView(pref);
  return {
    rowLines: p.rowLines ?? base.rowLines,
    headerLines: p.headerLines ?? base.headerLines,
    density: p.density ?? base.density,
    pageSize: p.pageSize ?? base.pageSize,
  };
}

/** Tercihte olup URL'de OLMAYAN anahtarlar → URL'ye yazılacak değerler.
 *  Tercih yoksa boş (URL değişmez — tercihsiz kullanıcıda eski linkler aynen). */
export function prefParamsMissingFromUrl(url: URLSearchParams, pref: DisplayPref | null | undefined): Record<string, string> {
  const p = prefToView(pref);
  const out: Record<string, string> = {};
  const put = (k: LogsViewParam, v: string | undefined) => { if (v !== undefined && !url.has(k)) out[k] = v; };
  put('rh', p.rowLines !== undefined ? String(p.rowLines) : undefined);
  put('hh', p.headerLines !== undefined ? String(p.headerLines) : undefined);
  put('den', p.density);
  put('pp', p.pageSize !== undefined ? String(p.pageSize) : undefined);
  put('smp', p.sample !== undefined ? String(p.sample) : undefined);
  return out;
}
