// chatBlocks.ts — v0.10.541 (Faz 3.3a): tipli blok biriktirme, SAF.
// Aynı id ikinci kez gelirse (final güncellemesi) yerine geçer; sıra seq.
import type { ChatTypedBlock, ChatAnswerLink, CosreChartSpecLike, ChatTraceListPayload, ChatOperationPayload, ChatEvidenceChartPayload, ChatEvidenceTablePayload, ChatServiceReportPayload, ChatTopologyPayload, ChartsSignalsBlockPayload, LogSpikeBlockPayload, MetricPanelBlockPayload } from './types'; // v0.10.1435 — MetricPanelBlockPayload
import { isSafeAppPath } from './safeAppPath';

export function appendChatBlock(blocks: ChatTypedBlock[] | undefined, b: ChatTypedBlock): ChatTypedBlock[] {
  const rest = (blocks ?? []).filter(x => x.id !== b.id);
  rest.push(b);
  return rest.sort((a, c) => a.seq - c.seq);
}

export function chartBlocks(blocks: ChatTypedBlock[] | undefined): CosreChartSpecLike[] {
  return (blocks ?? []).filter(b => b.type === 'chart').map(b => b.payload as CosreChartSpecLike)
    .filter(p => p && typeof p.service === 'string' && typeof p.agg === 'string');
}

/** v0.10.1314 — ChatBubble http(s) olmayan her href'i `<Link to>` çizer; yalnız
 *  http(s) ya da isSafeAppPath geçen link kalır ("/\evil.com" react-router v6'da
 *  window.location.assign ile dışarı açılırdı). */
function linkHrefOk(href: string): boolean {
  return /^https?:\/\//i.test(href) || isSafeAppPath(href);
}

/** Link blokları cevap link çiplerine katılır; href tekil. Güvensiz href (iki kaynakta da) düşer. */
export function mergeBlockLinks(links: ChatAnswerLink[] | undefined, blocks: ChatTypedBlock[] | undefined): ChatAnswerLink[] {
  const out: ChatAnswerLink[] = (links ?? []).filter(l => typeof l.href === 'string' && linkHrefOk(l.href));
  const seen = new Set(out.map(l => l.href));
  for (const b of blocks ?? []) {
    if (b.type !== 'link') continue;
    const p = b.payload as Partial<ChatAnswerLink> | null;
    if (p && typeof p.href === 'string' && p.href && linkHrefOk(p.href) && !seen.has(p.href)) {
      seen.add(p.href);
      out.push({ label: typeof p.label === 'string' && p.label ? p.label : p.href, href: p.href });
    }
  }
  return out;
}

/** v0.10.688 — trace_list blokları (endpoint_traces.go); şekil doğrulanır, bozuk yük atlanır.
 *  v0.10.1272 — kanıt çipine bağlı (evidenceRef) listeler burada DEĞİL, çipin altında çizilir.
 *  v0.10.1314 — deepLink `<Link to>` olur: isSafeAppPath geçmeyen yük düşer. */
export function traceListBlocks(blocks: ChatTypedBlock[] | undefined): ChatTraceListPayload[] {
  return (blocks ?? []).filter(b => b.type === 'trace_list').map(b => b.payload as ChatTraceListPayload)
    .filter(p => p && Array.isArray(p.traces) && typeof p.deepLink === 'string' && isSafeAppPath(p.deepLink) && !p.evidenceRef);
}

/** v0.10.1272 — kanıt çipine bağlı sohbet içi blok (kanıt zarflı araçların verisi). */
export type EvidenceBlockView =
  | { type: 'chart'; payload: ChatEvidenceChartPayload }
  | { type: 'table'; payload: ChatEvidenceTablePayload }
  | { type: 'trace_list'; payload: ChatTraceListPayload };

/** evidenceBlocksByRef — evidenceRef → blok (şekil doğrulanır; bozuk yük düşer). SAF. */
export function evidenceBlocksByRef(blocks: ChatTypedBlock[] | undefined): Map<string, EvidenceBlockView> {
  const out = new Map<string, EvidenceBlockView>();
  for (const b of blocks ?? []) {
    const p = b.payload as { evidenceRef?: unknown } | null;
    const ref = p && typeof p.evidenceRef === 'string' ? p.evidenceRef : '';
    if (!ref) continue;
    if (b.type === 'chart') {
      const c = b.payload as ChatEvidenceChartPayload;
      if (Array.isArray(c.series) && c.series.length > 0) out.set(ref, { type: 'chart', payload: c });
    } else if (b.type === 'table') {
      const t = b.payload as ChatEvidenceTablePayload;
      if (Array.isArray(t.columns) && Array.isArray(t.rows) && t.rows.length > 0) out.set(ref, { type: 'table', payload: t });
    } else if (b.type === 'trace_list') {
      const l = b.payload as ChatTraceListPayload;
      if (Array.isArray(l.traces) && l.traces.length > 0 && typeof l.deepLink === 'string' && isSafeAppPath(l.deepLink)) out.set(ref, { type: 'trace_list', payload: l }); // v0.10.1314
    }
  }
  return out;
}

// operationBlocks — operasyon ön-getirme kartı (geçersiz yük düşer).
// v0.10.1314 — tracesHref `<Link to>` olur: isSafeAppPath geçmeyen yük düşer.
export function operationBlocks(blocks: ChatTypedBlock[] | undefined): ChatOperationPayload[] {
  return (blocks ?? []).filter(b => b.type === 'operation').map(b => b.payload as ChatOperationPayload)
    .filter(p => p && typeof p.operation === 'string' && typeof p.tracesHref === 'string' && isSafeAppPath(p.tracesHref) && p.current != null);
}

/** v0.10.1783 — endpoint turunun deterministik değişim cümleleri (Go endpointDeltaTR):
 *  operasyon kartının deltaText'i; cevabın ÜSTÜNDE çizilir (modelden bağımsız). SAF. */
export function operationDeltaTexts(blocks: ChatTypedBlock[] | undefined): string[] {
  return operationBlocks(blocks).map(p => (typeof p.deltaText === 'string' ? p.deltaText.trim() : '')).filter(t => t !== '');
}

/** v0.10.1337 — servis raporu kartı (copilot_service_report.go). Şekil doğrulanır;
 *  HER href isSafeAppPath'ten geçer: güvensiz link çipi düşer, güvensiz exception
 *  linki satırdan silinir (satır kalır, link olmaz). Trace linkleri traceHref'le
 *  kartta kurulur (sunucu href'i yok). SAF. */
export function serviceReportBlocks(blocks: ChatTypedBlock[] | undefined): ChatServiceReportPayload[] {
  const out: ChatServiceReportPayload[] = [];
  for (const b of blocks ?? []) {
    if (b.type !== 'service_report') continue;
    const p = b.payload as ChatServiceReportPayload | null;
    if (!p || typeof p.service !== 'string' || !p.service || typeof p.verdict !== 'string' || !p.ops) continue;
    out.push({
      ...p,
      links: (p.links ?? []).filter(l => l && typeof l.href === 'string' && isSafeAppPath(l.href) && typeof l.label === 'string'),
      exceptions: (p.exceptions ?? []).map(e => ({
        ...e,
        href: typeof e.href === 'string' && isSafeAppPath(e.href) ? e.href : '',
        explainHref: typeof e.explainHref === 'string' && isSafeAppPath(e.explainHref) ? e.explainHref : '',
      })),
      drills: (p.drills ?? []).filter(d => d && typeof d.traceId === 'string' && d.traceId !== ''),
      // v0.10.1415 — VM HTTP grafikleri: serisiz yük düşer; güvensiz deepLink silinir (grafik kalır).
      httpCharts: (p.httpCharts ?? []).filter(c => c && typeof c.title === 'string' && Array.isArray(c.series) && c.series.length > 0 && !!c.window)
        .map(c => ({ ...c, deepLink: typeof c.deepLink === 'string' && isSafeAppPath(c.deepLink) ? c.deepLink : undefined })),
    });
  }
  return out;
}

/** v0.10.1388 — topoloji bloğu düğüm tavanı (bozuk/aşırı yük düşer). v0.10.1392 —
 *  sunucu: trace ≤15; servis komşuluğu sütun başına ≤12 ("+N more" dahil) → 2 hop'ta ≤49. */
export const TOPOLOGY_BLOCK_MAX_NODES = 60;

/** v0.10.1388 — topoloji grafiği (copilot_service_topology.go). Şekil doğrulanır;
 *  deepLink isSafeAppPath geçmezse yük düşer; traceHref güvensizse silinir (kart
 *  kalır, trace linki olmaz); kenarı olmayan ya da >60 düğümlü yük düşer;
 *  ucu düğüm kümesinde olmayan kenar atılır. SAF. */
export function topologyBlocks(blocks: ChatTypedBlock[] | undefined): ChatTopologyPayload[] {
  const out: ChatTopologyPayload[] = [];
  for (const b of blocks ?? []) {
    if (b.type !== 'topology') continue;
    const p = b.payload as ChatTopologyPayload | null;
    if (!p || typeof p.focus !== 'string' || !p.focus || !Array.isArray(p.nodes) || !Array.isArray(p.edges)) continue;
    if (p.scope !== 'neighborhood' && p.scope !== 'trace') continue;
    if (typeof p.deepLink !== 'string' || !isSafeAppPath(p.deepLink)) continue;
    if (p.nodes.length === 0 || p.nodes.length > TOPOLOGY_BLOCK_MAX_NODES) continue;
    const ids = new Set(p.nodes.filter(n => n && typeof n.id === 'string').map(n => n.id));
    const edges = p.edges.filter(e => e && ids.has(e.source) && ids.has(e.target));
    if (edges.length === 0) continue;
    out.push({
      ...p,
      nodes: p.nodes.filter(n => n && typeof n.id === 'string'),
      edges,
      traceHref: typeof p.traceHref === 'string' && isSafeAppPath(p.traceHref) ? p.traceHref : undefined,
      // v0.10.1392 — servis komşuluğunda sütun tavanının yön kırılımı da taşınır.
      truncated: {
        nodes: Number(p.truncated?.nodes) || 0, edges: Number(p.truncated?.edges) || 0,
        callers: Number(p.truncated?.callers) || 0, callees: Number(p.truncated?.callees) || 0,
      },
    });
  }
  return out;
}

/**
 * v0.10.1407 (Tek CoSRE adım 4) — açıklama turunun kanıt kartı blokları. Şekil
 * doğrulanır (bozuk yük atlanır → balon ExplainBody'ye düşer); İLK geçerli blok.
 */
export function chartsSignalsBlock(blocks: ChatTypedBlock[] | undefined): ChartsSignalsBlockPayload | null {
  for (const b of blocks ?? []) {
    if (b.type !== 'charts_signals') continue;
    const p = b.payload as ChartsSignalsBlockPayload | null;
    if (!p || typeof p.service !== 'string' || !p.service || !p.signals || typeof p.signals !== 'object') continue;
    if (!(p.fromNs > 0) || !(p.toNs > p.fromNs)) continue;
    return p;
  }
  return null;
}

export function logSpikeBlock(blocks: ChatTypedBlock[] | undefined): LogSpikeBlockPayload | null {
  for (const b of blocks ?? []) {
    if (b.type !== 'log_spike') continue;
    const p = b.payload as LogSpikeBlockPayload | null;
    if (!p || typeof p.service !== 'string' || !p.spike || typeof p.spike !== 'object') continue;
    if (!(p.fromNs > 0) || !(p.toNs > p.fromNs)) continue;
    return p;
  }
  return null;
}

/**
 * v0.10.1435 — panel açıklamasının kanıt bloğu (`metric_panel`). Şekil
 * doğrulanır (bozuk yük atlanır → balon ExplainBody'ye düşer); İLK geçerli blok.
 * Bağlantılar yalnız uygulama-içi yol.
 */
export function metricPanelBlock(blocks: ChatTypedBlock[] | undefined): MetricPanelBlockPayload | null {
  for (const b of blocks ?? []) {
    if (b.type !== 'metric_panel') continue;
    const p = b.payload as MetricPanelBlockPayload | null;
    if (!p || typeof p.title !== 'string' || !p.chart || !Array.isArray(p.chart.series) || !Array.isArray(p.stats)) continue;
    if (!(p.fromNs > 0) || !(p.toNs > p.fromNs)) continue;
    return {
      ...p,
      href: typeof p.href === 'string' && isSafeAppPath(p.href) ? p.href : undefined,
      chart: { ...p.chart, deepLink: typeof p.chart.deepLink === 'string' && isSafeAppPath(p.chart.deepLink) ? p.chart.deepLink : undefined },
    };
  }
  return null;
}
