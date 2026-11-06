// logsSmartColumns — v0.10.1856 (Log Search Faz 2 dilim 2.5; audit §8).
// Akıllı varsayılan kolonlar: yüklenen örnekte doluluğu ≥ %80 olan İŞ ALANLARI
// (system_settings logs_search.businessFields) tabloya kolon olarak girer —
// YALNIZ kullanıcının kayıtlı kolon tercihi yoksa (URL `cols`, tarayıcı tercihi,
// sunucu kolon modeli; hiçbiri). SAF; logsSmartColumns.test.ts.
//
// İstek tarafı: uygunken iş alanı ADAYLARI dar `_source` listesine eklenir ki
// doluluk ölçülebilsin (aday ES'te yoksa zararsız). Kolon kümesi isteği
// DEĞİŞTİRMEZ — aday listesi ayardan gelir, satırdan değil (geri besleme yok).
import type { LogRow, LogsBusinessField } from '@/lib/types';

export const SMART_FILL_MIN = 0.8;
/** Dar `_source` tavanı (sunucu logstore.MaxSourceFields). */
export const SOURCE_FIELDS_MAX = 50;

/** İş alanı adayları (tanım sırası, tekil) — uygunken `_source`a eklenir. */
export function businessCandidates(defs: readonly LogsBusinessField[] | undefined): string[] {
  const out: string[] = [];
  for (const d of defs ?? []) for (const f of d.fields) if (!out.includes(f)) out.push(f);
  return out;
}

/** İstek kolon listesi: tabanın arkasına adaylar, tavan içinde. */
export function requestColumns(base: readonly string[], extra: readonly string[]): string[] {
  const out = [...base];
  for (const f of extra) {
    if (out.length >= SOURCE_FIELDS_MAX) break;
    if (!out.includes(f)) out.push(f);
  }
  return out;
}

const filled = (r: LogRow, f: string) => {
  const v = r.attributes?.[f] ?? r.resourceAttributes?.[f];
  return v !== undefined && v !== null && String(v).trim() !== '';
};

/**
 * Doluluğu eşiği geçen iş alanları (tanım başına EN dolu aday; eşitlikte tanım
 * sırası). Örnek boşsa hiçbiri.
 */
export function smartBusinessColumns(
  defs: readonly LogsBusinessField[] | undefined, rows: readonly LogRow[], min = SMART_FILL_MIN,
): string[] {
  if (!defs?.length || rows.length === 0) return [];
  const out: string[] = [];
  for (const d of defs) {
    let best = '', bestN = 0;
    for (const f of d.fields) {
      let n = 0;
      for (const r of rows) if (filled(r, f)) n++;
      if (n > bestN) { best = f; bestN = n; }
    }
    if (best && bestN / rows.length >= min && !out.includes(best)) out.push(best);
  }
  return out;
}

/** Tabana akıllı kolonları ekle: 'message'dan ÖNCE (mockup: Servis · iş alanı · Mesaj). */
export function withSmartColumns(base: readonly string[], smart: readonly string[]): string[] {
  const add = smart.filter(c => !base.includes(c));
  if (add.length === 0) return [...base];
  const i = base.indexOf('message');
  return i < 0 ? [...base, ...add] : [...base.slice(0, i), ...add, ...base.slice(i)];
}
