// traceMetricsUrl.ts — v0.10.968 — Trace › Metrics URL durumu (SAF okuma + yazma).
//
// v0.10.968 — URL görünümün tek kaynağı (frontend-conventions §4): seçim
// (mpod), pencere (mwin), süzgeç (mf), arama (mq), gruplama (mgrp), kardeş
// çizgileri (msib), odak görünümü (mview). Hepsi replace:true ile yazılır.
//
// YAZICI ÜST KÜMEDEN kurar: Trace.tsx `span`/`tab`/`xn`i ham
// `history.replaceState` ile yazar ve router'ı haberdar ETMEZ — router'ın
// `prev`i bayat bir ALT KÜME olur. `prev`i kopyalayan bir yazıcı `tab=metrics`
// ve `span`i sessizce silerdi (v0.8.256 sınıfı; eski panel de bu kusuru
// taşıyordu). Bu yüzden `next` `window.location.search`ten kurulur
// (applyTraceMetricsPatch'e çağıran onu verir).
import {
  TRACE_METRICS_DEFAULT_WINDOW, TRACE_METRICS_MAX_COMPARE, TRACE_METRICS_URL_PARAMS, TRACE_METRICS_WINDOWS,
  type TraceMetricsFilter, type TraceMetricsGrouping, type TraceMetricsWindow,
} from './traceMetricsModel';

export { TRACE_METRICS_URL_PARAMS };

const FILTERS: readonly TraceMetricsFilter[] = ['err', 'crit', 'slow'];

export interface TraceMetricsUrlState {
  /** Karşılaştırma listesi (ilk = seçili); kapalıysa ya da pod yoksa []. */
  compare: string[];
  /** `mpod=-`: panel AÇIKÇA kapatıldı. */
  closed: boolean;
  /** `mpod` URL'de yoktu → varsayılan seçim kullanıldı. */
  isDefault: boolean;
  win: TraceMetricsWindow;
  filter: TraceMetricsFilter;
  query: string;
  grouping: TraceMetricsGrouping;
  siblingLines: boolean;
  /** `mview=pod` — yalnız bir pod seçiliyken geçerli. */
  focus: boolean;
}

/** v0.10.968 — `mpod` doğrulaması: bilinmeyen adlar düşer, ilk adın
 *  servisinden olmayanlar düşer, 4'ten fazlası kesilir; `-` kapalı; yok
 *  (ya da hepsi düştü) → varsayılan. */
export function parseMpod(
  raw: string | null, byPod: ReadonlyMap<string, { service: string }>,
): { kind: 'default' } | { kind: 'closed' } | { kind: 'list'; pods: string[] } {
  const v = (raw ?? '').trim();
  if (!v) return { kind: 'default' };
  if (v === '-') return { kind: 'closed' };
  const seen = new Set<string>();
  const known = v.split(',').map(s => s.trim()).filter(p => {
    if (!p || seen.has(p) || !byPod.has(p)) return false;
    seen.add(p);
    return true;
  });
  if (!known.length) return { kind: 'default' };
  const svc = byPod.get(known[0])!.service;
  return { kind: 'list', pods: known.filter(p => byPod.get(p)!.service === svc).slice(0, TRACE_METRICS_MAX_COMPARE) };
}

/** v0.10.968 — URL → görünüm durumu. `defaultPod` = modelin varsayılan seçimi. */
export function parseTraceMetricsUrl(
  sp: URLSearchParams, byPod: ReadonlyMap<string, { service: string }>, defaultPod: string,
): TraceMetricsUrlState {
  const m = parseMpod(sp.get('mpod'), byPod);
  const compare = m.kind === 'list' ? m.pods : m.kind === 'default' && defaultPod ? [defaultPod] : [];
  const winRaw = Number(sp.get('mwin'));
  const win = (TRACE_METRICS_WINDOWS as readonly number[]).includes(winRaw)
    ? (winRaw as TraceMetricsWindow) : TRACE_METRICS_DEFAULT_WINDOW;
  const f = sp.get('mf') ?? '';
  const filter: TraceMetricsFilter = (FILTERS as readonly string[]).includes(f) ? (f as TraceMetricsFilter) : 'all';
  return {
    compare,
    closed: m.kind === 'closed',
    isDefault: m.kind === 'default',
    win,
    filter,
    query: sp.get('mq') ?? '',
    grouping: sp.get('mgrp') === 'flat' ? 'flat' : 'service',
    siblingLines: sp.get('msib') !== '0',
    focus: sp.get('mview') === 'pod' && compare.length > 0,
  };
}

export interface TraceMetricsUrlPatch {
  /** dizi = açık liste · 'closed' = `mpod=-` · null = sil (varsayılan). */
  compare?: string[] | 'closed' | null;
  win?: TraceMetricsWindow;
  filter?: TraceMetricsFilter;
  query?: string;
  grouping?: TraceMetricsGrouping;
  siblingLines?: boolean;
  focus?: boolean;
}

/** v0.10.968 — yazıcı: ÜST KÜME aramadan (window.location.search) yama
 *  uygulanmış yeni parametreler. Yabancı parametreler (id, span, tab, xn,
 *  range…) aynen kalır; varsayılan değerler URL'e yazılmaz. */
export function applyTraceMetricsPatch(search: string, patch: TraceMetricsUrlPatch): URLSearchParams {
  const next = new URLSearchParams(search);
  const put = (k: string, v: string | null) => { if (v) next.set(k, v); else next.delete(k); };
  if (patch.compare !== undefined) {
    put('mpod', patch.compare === 'closed' ? '-' : patch.compare && patch.compare.length ? patch.compare.join(',') : null);
    // Odak görünümü seçimsiz anlamsız: seçim kapanınca mview de düşer.
    if (patch.compare === 'closed') next.delete('mview');
  }
  if (patch.win !== undefined) put('mwin', patch.win === TRACE_METRICS_DEFAULT_WINDOW ? null : String(patch.win));
  if (patch.filter !== undefined) put('mf', patch.filter === 'all' ? null : patch.filter);
  if (patch.query !== undefined) put('mq', patch.query.trim() ? patch.query : null);
  if (patch.grouping !== undefined) put('mgrp', patch.grouping === 'flat' ? 'flat' : null);
  if (patch.siblingLines !== undefined) put('msib', patch.siblingLines ? null : '0');
  if (patch.focus !== undefined) put('mview', patch.focus ? 'pod' : null);
  return next;
}

/** v0.10.968 — sekmeden çıkarken (Trace.tsx) Metrics'in bütün parametreleri silinir. */
export function clearTraceMetricsParams(sp: URLSearchParams): void {
  for (const k of TRACE_METRICS_URL_PARAMS) sp.delete(k);
}
