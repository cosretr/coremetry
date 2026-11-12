// lib/metricsExplorer/panels.ts — v0.10.1863 (Metrics Explorer Faz 2 dilim
// 2.3). "Dashboard'a ekle": görünür her satır (formül dahil) bir `metric`
// paneli olur, `promql` alanı derlenmiş MetricsQL'i taşır (panel PromQL
// yolu bugünkü PanelRenderer'da var). Pencere yer tutucuları eklendiği andaki
// adımla çözülür — pano değerlendiricisi `$__interval` bilmez.
import type { MetricPanelConfig, Panel, PanelVizType } from '@/lib/types';
import { MX_MAX_CODE, MX_PARAM, defaultMxModel, encodeMx, type MxModel, type MxViz } from './model';
import { planRun } from './run';

function panelViz(v: MxViz): PanelVizType {
  if (v === 'area') return 'area';
  if (v === 'stacked') return 'stacked-area';
  return 'line';
}

/** Panoya eklenecek paneller; `newId` test için enjekte edilir. */
export function panelsFromModel(model: MxModel, rangeSec: number, newId: () => string): Panel[] {
  const plan = planRun(model, rangeSec);
  return plan.items.map(it => {
    const q = model.queries.find(x => x.id === it.id);
    const cfg: MetricPanelConfig = {
      metricName: q?.metric || it.id,
      promql: it.query,
      ...(it.unit ? { unit: it.unit } : {}),
      viz: panelViz(model.viz),
    };
    const title = it.legend.trim() && !it.legend.includes('{{')
      ? it.legend.trim()
      : it.kind === 'formula' ? `ƒ ${q?.expr ?? it.id}` : it.kind === 'code' ? 'MetricsQL' : (q?.metric || it.id);
    const panel: Panel = { id: newId(), type: 'metric', title, width: 2, config: cfg };
    return panel;
  });
}

/**
 * v0.10.1998 (Dashboards Faz 3 dilim 3.4) — pano panelinin "Explorer'da aç"
 * hedefi: MetricsQL metnini Metrics Explorer'ın KOD kipinde açar (builder
 * biçimindeyse kullanıcı oradan "builder'a çevir" der). Pano değişkenleri
 * çağıran tarafından önceden genişletilmiş olmalı. Boş sorgu → null.
 */
export function metricsExplorerCodeHref(query: string, range?: string, src?: MxModel['src']): string | null {
  const q = query.trim();
  if (!q || q.length > MX_MAX_CODE) return null;
  const sp = new URLSearchParams();
  sp.set(MX_PARAM, encodeMx({ ...defaultMxModel(), mode: 'code', code: q, ...(src ? { src } : {}) }));
  if (range) sp.set('range', range);
  return `/metrics?${sp.toString()}`;
}
