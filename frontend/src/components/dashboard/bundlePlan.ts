// bundlePlan — v0.10.2003 (Dashboards Faz 3 dilim 3.6; audit §5.3 "Yükleme"
// + §3.1 satır 10 "Bundle'a maxDataPoints").
//
// Pano açılışında panellerin TEK isteğe (POST /api/dashboards/data)
// toplanması. v0.5.81'den beri yalnız metric + spanmetric giriyordu; 20
// panelli bir panoda promql / stat / gauge her biri ayrı istek atıyordu.
// Şimdi:
//   • metric (builder)            → metric slotu (eskisi gibi)
//   • metric (PromQL kipi), promql → promql slotu (+ maxDataPoints)
//   • spanmetric                  → spanMetric slotu (eskisi gibi)
//   • stat                        → iki kat pencereli slot (önceki dönem
//                                   farkı için), gauge → normal pencere;
//                                   alt kaynağa göre metric / spanMetric /
//                                   promql. Yalnız groupBy YOKKEN: bundle
//                                   spanMetric 16 seri + kuyruk döner, tek
//                                   değer panelinin "son nokta"sı kırpılmış
//                                   kümede farklı çıkabilirdi.
// DIŞARIDA (kendi isteği, LazyMount ile görünür olunca): top-N (50 satıra
// kadar sıralı tek kova; bundle 16'da keser), heatmap (histogram ucu),
// kaynak pini olan promql (bundle tek kaynak taşır), panel kendi aralığı
// (rangeOverride) olanlar, markdown / row.
import type {
  GaugePanelConfig, MetricPanelConfig, Panel, PromqlPanelConfig, SpanMetricPanelConfig, StatPanelConfig,
} from '@/lib/types';
import { plainVars } from '@/lib/dashboardVars';
import { resolvePanelQuery } from '@/lib/metricsExplorer/panelQuery';
import {
  applyVarsToGauge, applyVarsToMetric, applyVarsToPromql, applyVarsToSpan, applyVarsToStat,
} from './PanelRenderer';

export interface BundleRequest {
  id: string;
  type: 'metric' | 'spanMetric' | 'promql';
  name?: string; service?: string; agg?: string; field?: string;
  groupBy?: string[]; step?: number; filters?: string; dsl?: string;
  query?: string; maxDataPoints?: number; from?: number; to?: number;
}

const splitBy = (g?: string) => (g ? g.split(',').map(s => s.trim()).filter(Boolean) : undefined);

/** SAF: panel bundle'a girer mi (kayıtlı panelde, değişkenlerden bağımsız). */
export function isBundleable(p: Panel): boolean {
  if (p.rangeOverride) return false;
  switch (p.type) {
    case 'metric':
    case 'spanmetric':
      return true;
    case 'promql': {
      // v0.10.2017 — tablo / sıralı liste sunucu top-N ister (limit + rank); kendi isteği.
      const c = p.config as PromqlPanelConfig;
      return !c.source && !!c.query?.trim() && c.viz !== 'table' && c.viz !== 'toplist';
    }
    case 'stat':
    case 'gauge': {
      const c = p.config as StatPanelConfig | GaugePanelConfig;
      return c.source === 'spanmetric' ? !c.span?.groupBy : !c.metric?.groupBy;
    }
    default:
      return false;
  }
}

/**
 * SAF: tek panelin bundle isteği. `stepFor(cfgStep)` panelin genişliğine göre
 * otomatik adımı çözer (Dashboard.tsx, effectivePanelStep); `px` tahmini
 * panel genişliği (maxDataPoints). `win` gövde penceresi (ns).
 */
export function bundleRequestFor(
  p: Panel,
  vars: Record<string, string> | undefined,
  stepFor: (cfgStep: number | undefined) => number | undefined,
  px: number,
  win: { from: number; to: number },
): BundleRequest | null {
  const plain = plainVars(vars);
  const mdp = Math.max(1, Math.min(4000, Math.round(px)));
  const rangeSec = (win.to - win.from) / 1e9;
  // v0.10.2016 — builder'ın yer tutucuları panelin adımıyla çözülür
  // (sunucu değerlendiricisi $__rate_interval bilmez).
  const promql = (query: string, step: number | undefined, extra: Partial<BundleRequest> = {}): BundleRequest =>
    ({ id: p.id, type: 'promql', query: resolvePanelQuery(query, step, rangeSec), step, maxDataPoints: mdp, ...extra });
  switch (p.type) {
    case 'metric': {
      const cfg = applyVarsToMetric(p.config as MetricPanelConfig, plain);
      if (cfg.promql !== undefined) {
        const raw = (p.config as MetricPanelConfig).promql ?? '';
        const q = applyVarsToPromql({ query: raw }, vars).query.trim();
        return q ? promql(q, stepFor(cfg.step)) : null;
      }
      return {
        id: p.id, type: 'metric', name: cfg.metricName, service: cfg.service, agg: cfg.agg,
        groupBy: splitBy(cfg.groupBy), step: stepFor(cfg.step), filters: cfg.filters,
      };
    }
    case 'spanmetric': {
      const cfg = applyVarsToSpan(p.config as SpanMetricPanelConfig, plain);
      return {
        id: p.id, type: 'spanMetric', agg: cfg.agg, field: cfg.field,
        groupBy: splitBy(cfg.groupBy), filters: cfg.filters, dsl: cfg.dsl, step: stepFor(cfg.step),
      };
    }
    case 'promql': {
      const cfg = applyVarsToPromql(p.config as PromqlPanelConfig, vars);
      return cfg.query.trim() ? promql(cfg.query, stepFor(cfg.step)) : null;
    }
    case 'stat':
    case 'gauge': {
      const cfg = p.type === 'stat'
        ? applyVarsToStat(p.config as StatPanelConfig, plain)
        : applyVarsToGauge(p.config as GaugePanelConfig, plain);
      // stat önceki dönem farkı için İKİ KAT pencere çeker (StatPanel ile aynı).
      const w = p.type === 'stat' ? { from: win.from - (win.to - win.from), to: win.to } : null;
      const extra = w ? { from: w.from, to: w.to } : {};
      if (cfg.source === 'spanmetric') {
        const s = cfg.span;
        return { id: p.id, type: 'spanMetric', agg: s?.agg ?? 'count', field: s?.field, filters: s?.filters, dsl: s?.dsl, step: s?.step, ...extra };
      }
      const m = cfg.metric;
      if (m?.promql?.trim()) {
        const raw = ((p.config as StatPanelConfig).metric?.promql) ?? m.promql;
        const span = (w ?? win);
        return { id: p.id, type: 'promql', query: resolvePanelQuery(applyVarsToPromql({ query: raw }, vars).query, m.step, (span.to - span.from) / 1e9), step: m.step, ...extra };
      }
      return { id: p.id, type: 'metric', name: m?.metricName ?? '', service: m?.service, agg: m?.agg, step: m?.step, ...extra };
    }
    default:
      return null;
  }
}
