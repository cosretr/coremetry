// searchText — v0.10.1335: /search sayfasının SAF yardımcıları (metin, sıra, seçim).
//
// sourceNote: kaynak durumu → şerit satırı. "okunamadı" ile "sonuç yok"
// AYRI söylenir (operatör onaylı): unavailable/timeout bir arıza, skipped
// bir daraltma kuralı ("Traces: servis ya da trace id gerekli"), ok + reason
// bir dürüstlük notu (env uygulanmadı, pencere kırpıldı…).
import type { SearchResult, SearchResultType, SearchSourceStatus, SearchTypeFacet } from '@/lib/types';
import { SEARCH_TYPES, type SearchGroup, type SearchSel } from '@/lib/searchUrl';

/** fmt — katalog metnindeki {k} yer tutucularını doldurur. */
export function fmt(s: string, vars: Record<string, string | number>): string {
  return s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
}

/** Kaynak (çoğul ürün yüzeyi) adları — v0.10.2486 dilim 2: menüyle aynı anahtar. */
export const SOURCE_NAME_KEY: Record<SearchResultType, string> = {
  service: 'nav.services', endpoint: 'nav.endpoints', problem: 'nav.inbox',
  exception: 'nav.problems', trace: 'nav.traces', log: 'nav.logs',
};

export type SourceNoteTone = 'warn' | 'muted';
export interface SourceNote { type: SearchResultType; tone: SourceNoteTone; text: string }

/** Backend'in trace daraltma gerekçesi (search_query.go planTraceSearch). */
export const NEEDS_SERVICE_REASON = 'needs service or trace id';

export function sourceNote(
  type: SearchResultType, st: SearchSourceStatus, t: (k: string) => string,
): SourceNote | null {
  const src = t(SOURCE_NAME_KEY[type]);
  switch (st.status) {
    case 'unavailable':
      return { type, tone: 'warn', text: fmt(t('search.src.unavailable'), { src, reason: st.reason || 'query failed' }) };
    case 'timeout':
      return { type, tone: 'warn', text: fmt(t('search.src.unavailable'), { src, reason: 'timeout' }) };
    case 'skipped':
      if (st.reason === NEEDS_SERVICE_REASON) {
        return { type, tone: 'muted', text: fmt(t('search.src.needsService'), { src }) };
      }
      return { type, tone: 'muted', text: fmt(t('search.src.skipped'), { src, reason: st.reason || '—' }) };
    default:
      return st.reason ? { type, tone: 'muted', text: `${src}: ${st.reason}` } : null;
  }
}

/** Tür facet'i sayısı: kesikse "N+" ("en az N" — trace hasMore'da "20+"). */
export function typeCountLabel(f: SearchTypeFacet | undefined): string {
  if (!f) return '—';
  return f.truncated ? `${f.count}+` : String(f.count);
}

/** Vurgu terimleri: tam sorgu + ≥2 karakterli sözcükler (en uzun önce kazanır). */
export function highlightTerms(q: string): string[] {
  const full = q.trim();
  if (full.length < 2) return [];
  const words = full.split(/\s+/).filter(w => w.length >= 2);
  return [...new Set([full, ...words])];
}

/** orderResults — "Türe göre" gruplamada kanonik tür sırası, grup içinde
 *  sunucu sırası korunur (kararlı); liste görünümünde sunucu sırası aynen. */
export function orderResults(results: SearchResult[], group: SearchGroup): SearchResult[] {
  if (group !== 'type') return results;
  const rank = (t: SearchResultType) => SEARCH_TYPES.indexOf(t);
  return results.map((r, i) => ({ r, i }))
    .sort((a, b) => rank(a.r.type) - rank(b.r.type) || a.i - b.i)
    .map(x => x.r);
}

/** focusRow — idx'li satırın gövde düğmesine odak; yoksa false. */
export function focusRow(root: HTMLElement | null, idx: number): boolean {
  const el = root?.querySelector<HTMLElement>(`[data-row-idx="${idx}"] .srch-main`);
  if (!el) return false;
  el.focus();
  return true;
}

/** rowForSel — listede bulunmayan seçim (paylaşılmış link, süresi dolmuş
 *  önbellek) için kimlikten asgari satır. Log için null: tek kaydın ayrı
 *  okuyucusu yok, gövde SectionUnavailable der (satır uydurulmaz). */
export function rowForSel(sel: SearchSel): SearchResult | null {
  const base = { id: sel.id, title: sel.id, subtitle: '', service: '', href: '' };
  switch (sel.type) {
    case 'service': return { ...base, type: 'service', service: sel.id };
    case 'endpoint': {
      const [service, ...rest] = sel.id.split('\x1f');
      const name = rest.join('\x1f') || sel.id;
      return { ...base, type: 'endpoint', title: name, service, payload: { service, name } };
    }
    case 'problem': return { ...base, type: 'problem', href: `/problems?problem=${encodeURIComponent(sel.id)}`,
      payload: { id: sel.id, displayId: '', ruleName: sel.id, severity: '', status: '', service: '', description: '', startedAt: 0 } };
    case 'exception': return { ...base, type: 'exception', href: `/problems?exc=${encodeURIComponent(sel.id)}`,
      payload: { fingerprint: sel.id, type: '', message: '', service: '', state: '', firstSeen: 0, lastSeen: 0, occurrences: 0 } };
    case 'trace': return { ...base, type: 'trace',
      payload: { traceId: sel.id, rootName: '', serviceName: '', startTime: 0, durationMs: 0, spanCount: 0, hasError: false } };
    case 'log': return null;
  }
}

/** Span adı "GET /x" ise /api/endpoints/detail yolu "/x" (rota kimliği). */
export function endpointPathOf(name: string): string {
  const m = /^[A-Z]+\s+(\/.*)$/.exec(name);
  return m ? m[1] : name;
}

/** v0.10.1367 — span adı "GET /x" ise fiil "GET"; fiilsiz ad → "" (endpointPathOf'un ikizi). */
export function endpointMethodOf(name: string): string {
  const m = /^([A-Z]+)\s+\//.exec(name);
  return m ? m[1] : '';
}
