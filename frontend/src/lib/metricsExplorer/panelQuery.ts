// lib/metricsExplorer/panelQuery — v0.10.2016 (Dashboards Faz 3 dilim 3.7
// kalanı; audit §5.4 "Sorgu: Metrics Explorer ile AYNI QueryRow").
//
// Pano panelinin sorgusu ↔ Metrics Explorer satırı (MxQuery). SAF:
//   • panelQueryState: kayıtlı panel → düzenleyici durumu. MetricsQL metni
//     (promql paneli `query`, metric panelinin `promql`'i) builder biçimindeyse
//     builder satırı, değilse kod kipi. Eski builder metric paneli
//     (metricName / agg / groupBy / service / filters) satıra ÇEVRİLİR:
//     ad son eki → tür; `avg` histogramda `mean` (rate(_sum)/rate(_count),
//     v0.10.1887 dersi), yüzdelik → histogram_quantile(_bucket), noktalı
//     anahtar → Prometheus etiketi (service.name → service_name), servis →
//     service_name süzgeci. Çevrilemeyen süzgeç `notes`a yazılır.
//   • applyPanelQuery: düzenleyici çıktısı (MetricsQL metni) → panel config.
//     Metric panelinde builder alanları (agg / groupBy / service / filters)
//     DÜŞER, sorgu `promql`'de yaşar; birim, viz, eşik, adım korunur.
//   • resolvePanelQuery: `$__rate_interval` / `$__interval` yer tutucularını
//     panelin adımıyla çözer (pano değerlendiricisi onları bilmez).
import type { FilterExpr, MetricPanelConfig, Panel, PromqlPanelConfig } from '@/lib/types';
import { blankMxQuery, kindOf, type MxQuery } from './model';
import { compileMxQuery, hasIntervalVars, substituteIntervals } from './compile';
import { parseBuilderForm } from './parse';
import { mxFilterFromExpr, mxFnAggFromLegacy, promLabel } from './legacy';

export type PanelQueryMode = 'builder' | 'code';

export interface PanelQueryState {
  mode: PanelQueryMode;
  query: MxQuery;
  code: string;
  /** Çeviride düşen / değişen şeyler (düzenleyici satırın altında yazar). */
  notes: string[];
}

/** Eski builder metric paneli mi (tür bilgisi gerekir)? */
export function isLegacyMetricPanel(p: Panel): boolean {
  return p.type === 'metric' && (p.config as MetricPanelConfig).promql === undefined;
}

/** Sorgu düzenleyicisi olan panel tipleri. */
export function hasQueryEditor(p: Panel): boolean {
  return p.type === 'metric' || p.type === 'promql';
}

function parseFilters(raw: string | undefined, notes: string[]): FilterExpr[] {
  if (!raw?.trim()) return [];
  try {
    const v = JSON.parse(raw) as unknown;
    return Array.isArray(v) ? (v as FilterExpr[]) : [];
  } catch {
    notes.push('Süzgeç JSON\'u okunamadı; süzgeçler alınmadı');
    return [];
  }
}

/** SAF: eski builder metric paneli → Metrics Explorer satırı. */
export function mxFromLegacyMetricPanel(cfg: MetricPanelConfig, notes: string[] = [], reportedType?: string): MxQuery {
  const name = (cfg.metricName ?? '').trim();
  const { kind, estimated } = kindOf(name, reportedType);
  const filters = [];
  if (cfg.service?.trim()) filters.push({ k: 'service_name', op: '=' as const, v: cfg.service.trim() });
  for (const f of parseFilters(cfg.filters, notes)) {
    const m = mxFilterFromExpr(f);
    if (m) filters.push(m);
    else notes.push(`Süzgeç çevrilemedi: ${f.k} ${f.op} ${(f.v ?? []).join(',')}`);
  }
  const by = (cfg.groupBy ?? '').split(',').map(s => promLabel(s)).filter(Boolean);
  if ((cfg.agg ?? '').toLowerCase() === 'last') notes.push('"last" toplaması en büyük son değer (max) olarak açıldı');
  return {
    ...blankMxQuery('A'),
    metric: name,
    metricKind: kind,
    ...(estimated ? { kindEstimated: true } : {}),
    ...(cfg.unit ? { unit: cfg.unit } : {}),
    filters,
    ...mxFnAggFromLegacy(cfg.agg, name, reportedType),
    by,
  };
}

function fromText(text: string): PanelQueryState {
  const code = text.trim();
  if (!code) return { mode: 'builder', query: blankMxQuery('A'), code: '', notes: [] };
  const p = parseBuilderForm(code);
  if (p.ok) return { mode: 'builder', query: p.query, code, notes: p.notes };
  return { mode: 'code', query: blankMxQuery('A'), code, notes: [] };
}

/** SAF: kayıtlı panel → düzenleyici durumu. `reportedType`: eski builder
 *  panelinde metriğin ad zarfındaki türü (histogram taban adı son ekten
 *  anlaşılmaz; düzenleyici /api/metrics/names'ten okur). */
export function panelQueryState(p: Panel, reportedType?: string): PanelQueryState {
  if (p.type === 'promql') return fromText((p.config as PromqlPanelConfig).query ?? '');
  const cfg = p.config as MetricPanelConfig;
  if (cfg.promql !== undefined) return fromText(cfg.promql);
  const notes: string[] = [];
  const query = mxFromLegacyMetricPanel(cfg, notes, reportedType);
  return { mode: 'builder', query, code: compileMxQuery(query), notes };
}

/** SAF: düzenleyici kipinin MetricsQL metni. */
export function panelQueryText(st: Pick<PanelQueryState, 'mode' | 'query' | 'code'>): string {
  return st.mode === 'builder' ? compileMxQuery(st.query) : st.code;
}

/** SAF: MetricsQL metni → panel (yalnız metric / promql; diğerleri aynen). */
export function applyPanelQuery(p: Panel, text: string, query?: MxQuery): Panel {
  if (p.type === 'promql') return { ...p, config: { ...(p.config as PromqlPanelConfig), query: text } };
  if (p.type !== 'metric') return p;
  const cfg = p.config as MetricPanelConfig;
  const unit = cfg.unit ?? query?.unit;
  const next: MetricPanelConfig = {
    metricName: query?.metric ?? cfg.metricName ?? '',
    promql: text,
    ...(unit ? { unit } : {}),
    ...(cfg.viz ? { viz: cfg.viz } : {}),
    ...(cfg.step ? { step: cfg.step } : {}),
    ...(cfg.thresholds?.length ? { thresholds: cfg.thresholds } : {}),
  };
  return { ...p, config: next };
}

/** SAF: yer tutucuları panelin adımıyla çöz; adım yoksa aralık / 300. */
export function resolvePanelQuery(query: string, stepSec: number | null | undefined, rangeSec: number): string {
  if (!hasIntervalVars(query)) return query;
  const step = stepSec && stepSec > 0 ? stepSec : Math.max(1, Math.ceil(rangeSec / 300));
  return substituteIntervals(query, step);
}
