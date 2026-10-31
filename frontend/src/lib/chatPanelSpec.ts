// chatPanelSpec — v0.10.1443 (Grafana-Assistant programı madde 3, "sohbetten
// dashboard"; R1). Sohbet cevabındaki grafik bloğunu dashboard Panel'ine
// çeviren SAF eşleme. Testi chatPanelSpec.test.ts.
//
// Eşleme tablosu (plan; çevrilebilen ne, çevrilemeyen ne):
//   kanıt grafiği, kanıtı queryKind=promql + VictoriaMetrics
//     (metric_chart şablon/yapıştırılan, servis sağlığı VM RED) → promql
//     {query, unit: series[0].unit, source:'vm', viz:'line'}
//   service_report.httpCharts[] (sorgu + birim satır-içi)    → aynı, 3 panel
//   span spec-chart (tipli `chart` bloğu ya da ```chart çiti,
//     source span)                                          → spanmetric
//     {agg, filters:[service.name(, name)], groupBy, unit}
//   charts_signals / log_spike / topology / trace_list / tablolar → çevrilmez
//     (düğme gizli)
//
// Neden kaynak pini ('vm'): sohbetin VM sorgusu MetricsQL taşıyabilir
// (histogram_quantiles); panel kurulum varsayılanı CH olan bir yerde açılırsa
// sorgu CH yolunda kırılırdı. Sunucu aynı kuralı yeniden doğrular
// (dashboard_panels_routes.go dashValidatePromQL).
//
// Pencere: rangeS → PRESET_SECONDS'ın ≥ rangeS en yakın anahtarı; dashboard
// varsayılanından farklıysa rangeOverride. Mutlak pencere göreliye döner
// (dashboard canlı bir yüzey; "olay anı" sohbetin derin linkinde kalır).

import type {
  ChatEvidenceChartPayload, ChatQueryEvidence, ChatServiceReportPayload, ChatTurn,
  CosreChartSpecLike, FilterExpr, Panel, PromqlPanelConfig, SpanMetricPanelConfig, TimeRange,
} from './types';
import { PRESET_SECONDS } from './utils';
import { chartBlocks, evidenceBlocksByRef, serviceReportBlocks } from './chatBlocks';

/** Dashboard sayfasının varsayılan penceresi (lib/useUrlRange DEFAULT_RANGE_PRESET ile aynı). */
export const CHAT_PANEL_DASH_DEFAULT_PRESET = '3h';
/** Panel açıklamasındaki soru kırpması. */
const QUESTION_MAX = 160;
/** Sunucu tavanı (dashboard_panels_routes.go dashPanelTitleMax). */
export const CHAT_PANEL_TITLE_MAX = 120;

const SPAN_AGG_UNIT: Record<string, string> = {
  rate: 'req/s', error_rate: '%', errors: '', p50: 'ms', p95: 'ms', p99: 'ms',
};

export interface ChatPanelOrigin {
  question?: string;
  conversationId?: string | null;
}

function rid(): string {
  return Math.random().toString(36).slice(2, 10);
}

function clip(s: string, n: number): string {
  const r = Array.from(s);
  return r.length <= n ? s : `${r.slice(0, n - 1).join('')}…`;
}

/** rangeSToPreset — SAF: ≥ rangeS en yakın preset anahtarı (en büyüğü tavan). */
export function rangeSToPreset(rangeS: number | undefined): string | undefined {
  if (!rangeS || !(rangeS > 0)) return undefined;
  const entries = Object.entries(PRESET_SECONDS).sort((a, b) => a[1] - b[1]);
  for (const [k, s] of entries) if (s >= rangeS) return k;
  return entries[entries.length - 1]?.[0];
}

/** rangeOverrideFor — SAF: dashboard varsayılanından farklıysa panel penceresi. */
export function rangeOverrideFor(rangeS: number | undefined): TimeRange | undefined {
  const preset = rangeSToPreset(rangeS);
  return preset && preset !== CHAT_PANEL_DASH_DEFAULT_PRESET ? { preset } : undefined;
}

function windowS(w: { fromNs: number; toNs: number } | undefined): number | undefined {
  if (!w || !(w.toNs > w.fromNs)) return undefined;
  return Math.round((w.toNs - w.fromNs) / 1e9);
}

/** chatPanelDescription — `CoSRE · <soru> · /cosre?chat=<id>` (konuşmayı açar, turu değil). */
export function chatPanelDescription(origin?: ChatPanelOrigin): string {
  const parts = ['CoSRE'];
  const q = origin?.question?.replace(/\s+/g, ' ').trim();
  if (q) parts.push(clip(q, QUESTION_MAX));
  if (origin?.conversationId) parts.push(`/cosre?chat=${encodeURIComponent(origin.conversationId)}`);
  return parts.join(' · ');
}

function basePanel(type: Panel['type'], title: string, rangeS: number | undefined, config: Panel['config'], origin?: ChatPanelOrigin): Panel {
  const rangeOverride = rangeOverrideFor(rangeS);
  return {
    id: rid(), type, title: clip(title.trim() || 'Panel', CHAT_PANEL_TITLE_MAX),
    description: chatPanelDescription(origin),
    width: 2, height: 'm',
    ...(rangeOverride ? { rangeOverride } : {}),
    config,
  };
}

/** evidenceIsVmPromql — kanıt bir VM PromQL okuması mı (panelin tek kaynağı). */
export function evidenceIsVmPromql(ev: Pick<ChatQueryEvidence, 'queryKind' | 'source' | 'query'> | undefined): boolean {
  return !!ev && ev.queryKind === 'promql' && ev.source === 'VictoriaMetrics' && !!ev.query?.trim();
}

/** chartBlockToPanel — kanıt grafiği → promql paneli; çevrilemezse null. */
export function chartBlockToPanel(block: ChatEvidenceChartPayload, ev: ChatQueryEvidence | undefined, origin?: ChatPanelOrigin): Panel | null {
  if (!evidenceIsVmPromql(ev)) return null;
  const unit = block.series?.[0]?.unit;
  const config: PromqlPanelConfig = { query: ev!.query.trim(), ...(unit ? { unit } : {}), source: 'vm', viz: 'line' };
  return basePanel('promql', block.title, windowS(block.window), config, origin);
}

/** serviceReportChartsToPanels — servis raporunun VM HTTP grafikleri → promql panelleri. */
export function serviceReportChartsToPanels(rep: Pick<ChatServiceReportPayload, 'service' | 'httpCharts'>, origin?: ChatPanelOrigin): Panel[] {
  const out: Panel[] = [];
  for (const c of rep.httpCharts ?? []) {
    const p = serviceReportChartToPanel(rep.service, c, origin);
    if (p) out.push(p);
  }
  return out;
}

export function serviceReportChartToPanel(service: string, c: { title: string; query?: string; unit?: string; window?: { fromNs: number; toNs: number } }, origin?: ChatPanelOrigin): Panel | null {
  const query = c.query?.trim();
  if (!query) return null;
  const title = service && !c.title.includes(service) ? `${service} · ${c.title}` : c.title;
  const config: PromqlPanelConfig = { query, ...(c.unit ? { unit: c.unit } : {}), source: 'vm', viz: 'line' };
  return basePanel('promql', title, windowS(c.window), config, origin);
}

/** cosreSpecToPanel — span spec-chart → spanmetric paneli; metrik kaynaklı (VM /metric-red) çevrilmez. */
export function cosreSpecToPanel(spec: CosreChartSpecLike, origin?: ChatPanelOrigin): Panel | null {
  if (!spec || typeof spec.service !== 'string' || !spec.service || typeof spec.agg !== 'string' || !spec.agg) return null;
  if (spec.source === 'metric' || spec.source === 'route') return null; // v0.10.1738 — rota RED uçtan, panel tipi yok
  const filters: FilterExpr[] = [{ k: 'service.name', op: '=', v: [spec.service] }];
  if (spec.operation) filters.push({ k: 'name', op: '=', v: [spec.operation] });
  const unit = spec.unit ?? SPAN_AGG_UNIT[spec.agg];
  const config: SpanMetricPanelConfig = {
    agg: spec.agg,
    filters: JSON.stringify(filters),
    ...(spec.groupBy ? { groupBy: spec.groupBy } : {}),
    ...(unit ? { unit } : {}),
    viz: 'line',
  };
  const title = spec.title || [spec.service, spec.operation, spec.agg].filter(Boolean).join(' · ');
  const rangeS = spec.fromNs && spec.toNs && spec.toNs > spec.fromNs ? Math.round((spec.toNs - spec.fromNs) / 1e9) : (spec.rangeS ?? 1800);
  return basePanel('spanmetric', title, rangeS, config, origin);
}

/** chartFenceSpecs — cevap metnindeki kapalı ```chart çitleri (tipli blok yoksa). Bozuk JSON atlanır. */
export function chartFenceSpecs(text: string | undefined): CosreChartSpecLike[] {
  const out: CosreChartSpecLike[] = [];
  if (!text) return out;
  const re = /```chart[^\n]*\n([\s\S]*?)```/g;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    try {
      const spec = JSON.parse(m[1].trim()) as CosreChartSpecLike;
      if (spec && typeof spec.service === 'string' && typeof spec.agg === 'string') out.push(spec);
    } catch { /* bozuk çit — atla */ }
  }
  return out;
}

/** panelDedupeKey — SAF: sunucunun FNV anahtarıyla aynı alanlar (tür + normalize sorgu/süzgeç + agg + birim + kaynak). */
export function panelDedupeKey(p: Panel): string {
  const c = p.config as Partial<PromqlPanelConfig & SpanMetricPanelConfig>;
  return [p.type, (c.query ?? '').trim().replace(/\s+/g, ' '), c.filters ?? '', c.agg ?? '', c.unit ?? '',
    c.source ?? '', c.field ?? '', c.groupBy ?? '', (c.dsl ?? '').trim()].join('\u0000');
}

/**
 * turnToPanels — bir cevabın çevrilebilen BÜTÜN grafikleri (sıra: kanıt
 * grafikleri, servis raporu grafikleri, span grafikleri); tekrar düşer.
 * Kanıt: cevabın `evidence`'i, yoksa adımların kanıtları (ChatBubble'ın
 * answerEvidence kuralı) — evidenceRef ile blok eşlenir.
 */
export function turnToPanels(turn: Pick<ChatTurn, 'blocks' | 'evidence' | 'stepDetails' | 'text'>, origin?: ChatPanelOrigin): Panel[] {
  const out: Panel[] = [];
  const seen = new Set<string>();
  const push = (p: Panel | null) => {
    if (!p) return;
    const k = panelDedupeKey(p);
    if (seen.has(k)) return;
    seen.add(k);
    out.push(p);
  };
  const evBlocks = evidenceBlocksByRef(turn.blocks);
  const evidence = turn.evidence ?? (turn.stepDetails ?? []).flatMap(d => d.evidenceList?.length ? d.evidenceList : d.evidence ? [d.evidence] : []);
  for (const ev of evidence) {
    const b = ev.ref ? evBlocks.get(ev.ref) : undefined;
    if (b?.type === 'chart') push(chartBlockToPanel(b.payload, ev, origin));
  }
  for (const rep of serviceReportBlocks(turn.blocks)) {
    for (const p of serviceReportChartsToPanels(rep, origin)) push(p);
  }
  const typed = chartBlocks(turn.blocks);
  for (const spec of typed.length ? typed : chartFenceSpecs(turn.text)) push(cosreSpecToPanel(spec, origin));
  return out;
}
