// traceMetricsUrl.ts — v0.10.968 — Trace › Metrics URL durumu (SAF okuma + yazma).
//
// v0.10.968 — URL görünümün tek kaynağı (frontend-design-system §4 (eski frontend-conventions)): seçim
// (mpod), pencere (mwin), süzgeç (mf), arama (mq), gruplama (mgrp). Hepsi
// replace:true ile yazılır. v0.10.2207 — karşılaştırma (mpod listesi), kardeş
// çizgileri (msib) ve odak görünümü (mview) kalktı: mpod TEK pod.
//
// YAZICI ÜST KÜMEDEN kurar: Trace.tsx `span`/`tab`/`xn`i ham
// `history.replaceState` ile yazar ve router'ı haberdar ETMEZ — router'ın
// `prev`i bayat bir ALT KÜME olur. `prev`i kopyalayan bir yazıcı `tab=metrics`
// ve `span`i sessizce silerdi (v0.8.256 sınıfı; eski panel de bu kusuru
// taşıyordu). Bu yüzden `next` `window.location.search`ten kurulur
// (applyTraceMetricsPatch'e çağıran onu verir).
import {
  TRACE_METRICS_DEFAULT_WINDOW, TRACE_METRICS_URL_PARAMS, TRACE_METRICS_WINDOWS,
  type TraceMetricsFilter, type TraceMetricsGrouping, type TraceMetricsWindow,
} from './traceMetricsModel';

export { TRACE_METRICS_URL_PARAMS };

const FILTERS: readonly TraceMetricsFilter[] = ['err', 'crit', 'slow'];

export interface TraceMetricsUrlState {
  /** Seçili pod; kapalıysa ya da pod yoksa ''. */
  pod: string;
  /** `mpod=-`: panel AÇIKÇA kapatıldı. */
  closed: boolean;
  /** `mpod` URL'de yoktu → varsayılan seçim kullanıldı. */
  isDefault: boolean;
  win: TraceMetricsWindow;
  filter: TraceMetricsFilter;
  query: string;
  grouping: TraceMetricsGrouping;
}

/** v0.10.968 — `mpod` doğrulaması: bilinmeyen ad → varsayılan; `-` kapalı;
 *  yok → varsayılan. v0.10.2207 — tek pod (eski virgüllü linkte ilk bilinen ad). */
export function parseMpod(
  raw: string | null, byPod: ReadonlyMap<string, { service: string }>,
): { kind: 'default' } | { kind: 'closed' } | { kind: 'pod'; pod: string } {
  const v = (raw ?? '').trim();
  if (!v) return { kind: 'default' };
  if (v === '-') return { kind: 'closed' };
  const known = v.split(',').map(s => s.trim()).find(p => p && byPod.has(p));
  return known ? { kind: 'pod', pod: known } : { kind: 'default' };
}

/** v0.10.968 — URL → görünüm durumu. `defaultPod` = modelin varsayılan seçimi. */
export function parseTraceMetricsUrl(
  sp: URLSearchParams, byPod: ReadonlyMap<string, { service: string }>, defaultPod: string,
): TraceMetricsUrlState {
  const m = parseMpod(sp.get('mpod'), byPod);
  const pod = m.kind === 'pod' ? m.pod : m.kind === 'default' ? defaultPod : '';
  const winRaw = Number(sp.get('mwin'));
  const win = (TRACE_METRICS_WINDOWS as readonly number[]).includes(winRaw)
    ? (winRaw as TraceMetricsWindow) : TRACE_METRICS_DEFAULT_WINDOW;
  const f = sp.get('mf') ?? '';
  const filter: TraceMetricsFilter = (FILTERS as readonly string[]).includes(f) ? (f as TraceMetricsFilter) : 'all';
  return {
    pod,
    closed: m.kind === 'closed',
    isDefault: m.kind === 'default',
    win,
    filter,
    query: sp.get('mq') ?? '',
    grouping: sp.get('mgrp') === 'flat' ? 'flat' : 'service',
  };
}

export interface TraceMetricsUrlPatch {
  /** pod adı · 'closed' = `mpod=-` · null = sil (varsayılan). */
  pod?: string | 'closed' | null;
  win?: TraceMetricsWindow;
  filter?: TraceMetricsFilter;
  query?: string;
  grouping?: TraceMetricsGrouping;
}

/** v0.10.968 — yazıcı: ÜST KÜME aramadan (window.location.search) yama
 *  uygulanmış yeni parametreler. Yabancı parametreler (id, span, tab, xn,
 *  range…) aynen kalır; varsayılan değerler URL'e yazılmaz. */
export function applyTraceMetricsPatch(search: string, patch: TraceMetricsUrlPatch): URLSearchParams {
  const next = new URLSearchParams(search);
  const put = (k: string, v: string | null) => { if (v) next.set(k, v); else next.delete(k); };
  if (patch.pod !== undefined) put('mpod', patch.pod === 'closed' ? '-' : patch.pod || null);
  if (patch.win !== undefined) put('mwin', patch.win === TRACE_METRICS_DEFAULT_WINDOW ? null : String(patch.win));
  if (patch.filter !== undefined) put('mf', patch.filter === 'all' ? null : patch.filter);
  if (patch.query !== undefined) put('mq', patch.query.trim() ? patch.query : null);
  if (patch.grouping !== undefined) put('mgrp', patch.grouping === 'flat' ? 'flat' : null);
  return next;
}

/** v0.10.968 — sekmeden çıkarken (Trace.tsx) Metrics'in bütün parametreleri silinir. */
export function clearTraceMetricsParams(sp: URLSearchParams): void {
  for (const k of TRACE_METRICS_URL_PARAMS) sp.delete(k);
}
