// pages/metrics/QueryTab — v0.10.1863 (Metrics Explorer Faz 2 dilim 2.3;
// mockup görünüm 1). Sorgu sekmesi: Builder ⇄ MetricsQL, sorgu satırları
// (coremetry/metrics/QueryRow, FormulaRow), "+ Sorgu / + Formül", üretilen
// MetricsQL (ikinci düzey), sonuç kartı.
//
// Builder YAZDIKÇA çalışır (300 ms; Çalıştır düğmesi yok); kod kipi
// Çalıştır'la. Model URL'de (`?mx=`), sayfa yazar; burada yerel durum yok
// (taslak kod metni hariç).
import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link2 } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { AddToDashboardDialog, FormulaRow, QueryRow, type QueryRowScope } from '@/components/coremetry/metrics';
import { api } from '@/lib/api';
import { toast } from '@/lib/toast';
import { useDebouncedValue } from '@/lib/perf/useDebouncedValue';
import type { MetricsCapabilities } from '@/lib/types';
import {
  blankMxFormula, blankMxQuery, kindOf, nextMxFormulaId, nextMxLetter, MX_MAX_QUERIES,
  type MxModel, type MxQuery, type MxSrc,
} from '@/lib/metricsExplorer/model';
import { planRun } from '@/lib/metricsExplorer/run';
import { bucketSibling, sourceLabel } from '@/lib/metricsExplorer/result';
import { panelsFromModel } from '@/lib/metricsExplorer/panels';
import { useMetricsRun } from './useMetricsRun';
import { ResultPanel, type HeatmapState } from './ResultPanel';
import { CodePanel } from './CodePanel';
import { histogramResultToHeatmap } from '@/components/dashboard/histogramHeatmap';

export interface QueryTabProps {
  model: MxModel;
  onModel(next: MxModel): void;
  from: number;
  to: number;
  /** Bağlam çubuğunun cluster seçimi ('' = yok). */
  cluster: string;
  caps?: MetricsCapabilities;
  canEdit: boolean;
}

let panelSeq = 0;
const newPanelId = () => `mx${Date.now().toString(36)}${(panelSeq++).toString(36)}`;

export function QueryTab({ model, onModel, from, to, cluster, caps, canEdit }: QueryTabProps) {
  const [dashOpen, setDashOpen] = useState(false);
  const rangeSec = Math.max(1, (to - from) / 1e9);
  const set = (p: Partial<MxModel>) => onModel({ ...model, ...p });
  const setQuery = (id: string, q: MxQuery) => set({ queries: model.queries.map(x => (x.id === id ? q : x)) });

  // Kapsam (S6): bağlam çubuğunun cluster'ı, çip ✕ ile bu görünümde kapanır.
  const scopeSupported = caps ? caps.clusterScope.promql : true;
  const scopeOn = !!cluster && !model.scopeOff && scopeSupported;
  const scopeLabel = caps?.clusterScope.label || 'cluster';
  const scope: QueryRowScope | null = scopeOn ? { label: scopeLabel, value: cluster } : null;

  // Builder yazdıkça çalışır: 300 ms debounce (audit §4).
  const debounced = useDebouncedValue(model, model.mode === 'builder' ? 300 : 0);
  const plan = useMemo(() => planRun(debounced, rangeSec), [debounced, rangeSec]);
  const thanosAllowed = canEdit;
  const results = useMetricsRun({ plan, from, to, cluster: scopeOn || plan.src === 'thanos' ? cluster : '', thanosAllowed });

  // Isı haritası: builder'ın ilk görünür metrik satırı; histogram değilse nedenli boş durum.
  const heatQ = model.mode === 'builder' ? model.queries.find(q => q.kind === 'metric' && !q.hidden && q.metric) : undefined;
  const heatKind = heatQ ? (heatQ.metricKind ?? kindOf(heatQ.metric).kind) : undefined;
  const heatOn = model.viz === 'heatmap' && !!heatQ && heatKind === 'histogram';
  const heatStep = Math.max(1, Math.round(rangeSec / 80));
  const heat = useQuery({
    queryKey: ['mx-heat', heatQ?.metric ?? '', JSON.stringify(heatQ?.filters ?? []), from, to, heatStep, scopeOn ? cluster : ''] as const,
    queryFn: ({ signal }) => api.metricHistogram({
      name: heatQ!.metric,
      filters: heatQ!.filters.length ? JSON.stringify(heatQ!.filters.map(f => ({ k: f.k, op: f.op, v: [f.v] }))) : undefined,
      from, to, step: heatStep, ...(scopeOn ? { cluster } : {}),
    }, signal),
    enabled: heatOn && from > 0,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const heatmap: HeatmapState = model.mode === 'code'
    ? { loading: false, blocked: { reason: 'Isı haritası builder kipinde, histogram metriğiyle çalışır.' } }
    : !heatQ
      ? { loading: false, blocked: { reason: 'Önce bir metrik seçin.' } }
      : heatKind !== 'histogram'
        ? {
          loading: false,
          blocked: {
            reason: `Bu sorgunun metriği ${heatKind === 'counter' ? 'bir sayaç' : heatKind === 'gauge' ? 'bir gösterge' : 'histogram değil'}. Süre dağılımı için kova metriğine geçin.`,
            ...(bucketSibling(heatQ.metric) ? { sibling: bucketSibling(heatQ.metric)! } : {}),
          },
        }
        : {
          loading: heat.isLoading,
          data: heat.data ? histogramResultToHeatmap(heat.data, heatQ.unit) : heat.isSuccess ? null : undefined,
          error: heat.error ? (heat.error instanceof Error ? heat.error.message : String(heat.error)) : null,
          note: heat.data?.note ?? null,
        };

  const switchToBucket = (name: string) => {
    if (!heatQ) return;
    setQuery(heatQ.id, { ...heatQ, metric: name, metricKind: 'histogram', kindEstimated: true, fn: 'p95', agg: 'sum' });
  };

  const metricRows = model.queries.filter(q => q.kind === 'metric');
  const canAdd = metricRows.length < MX_MAX_QUERIES && nextMxLetter(model.queries) !== null;
  const generated = model.mode === 'builder' ? plan.items.filter(i => i.kind !== 'code') : [];
  const codeErr = model.mode === 'code' ? results[0]?.error ?? null : null;
  const scopeChipCode = cluster && !model.scopeOff ? (
    scopeSupported ? (
      <span className="inline-flex h-6 items-center gap-1 rounded-md border border-dashed bg-muted px-1.5 font-mono text-xs" title="Sorgu metni değişmez; kapsam ayrı uygulanır">
        <span className="font-sans text-[10px] uppercase text-muted-foreground">kapsam</span>{scopeLabel} = {cluster}
        <Button type="button" variant="ghost" size="icon-xs" className="size-4" aria-label="Kapsamı kaldır: tüm cluster'lar" onClick={() => set({ scopeOff: true })}>✕</Button>
      </span>
    ) : <span className="text-xs text-muted-foreground">kapsam uygulanmıyor: {caps?.clusterScope.reason || 'kaynak desteklemiyor'}</span>
  ) : null;

  const share = () => {
    void navigator.clipboard?.writeText(window.location.href).then(
      () => toast.success('Bağlantı kopyalandı'),
      () => toast.error('Bağlantı kopyalanamadı'));
  };

  return (
    <div className="flex flex-col gap-3">
      <section aria-label="Sorgu" className="rounded-lg border bg-card px-3 py-2">
        <div className="flex flex-wrap items-center gap-2 pb-2">
          <SegmentedControl<'builder' | 'code'> size="sm" aria-label="Sorgu kipi" value={model.mode}
            onChange={mode => {
              // Builder → kod her zaman: görünür sorguların derlenmiş metni taşınır.
              if (mode === 'code' && !model.code.trim() && generated.length) set({ mode, code: generated[0].text });
              else set({ mode });
            }}
            options={[{ value: 'builder', label: 'Builder' }, { value: 'code', label: 'MetricsQL' }]} />
          <span className="flex-1" />
          <Button type="button" variant="outline" size="sm" onClick={() => setDashOpen(true)} disabled={!canEdit}
            title={canEdit ? undefined : 'Pano düzenleme editor ve üstü rol ister'}>Dashboard'a ekle</Button>
          <Button type="button" variant="outline" size="sm" onClick={share}><Link2 aria-hidden="true" />Paylaş</Button>
        </div>

        {model.mode === 'builder' ? (
          <>
            {model.queries.map(q => (q.kind === 'metric' ? (
              <QueryRow key={q.id} query={q} onChange={n => setQuery(q.id, n)} scope={scope}
                onScopeOff={() => set({ scopeOff: true })} cluster={scopeOn ? cluster : undefined}
                onDuplicate={canAdd ? () => {
                  const id = nextMxLetter(model.queries);
                  if (id) set({ queries: [...model.queries, { ...q, id }] });
                } : undefined}
                onRemove={model.queries.length > 1 ? () => set({ queries: model.queries.filter(x => x.id !== q.id) }) : undefined} />
            ) : (
              <FormulaRow key={q.id} query={q} queries={model.queries} onChange={n => setQuery(q.id, n)}
                onRemove={() => set({ queries: model.queries.filter(x => x.id !== q.id) })} />
            )))}
            <div className="flex flex-wrap items-center gap-2 pt-2">
              <Button type="button" variant="ghost" size="xs" disabled={!canAdd}
                onClick={() => { const id = nextMxLetter(model.queries); if (id) set({ queries: [...model.queries, blankMxQuery(id)] }); }}>+ Sorgu</Button>
              <Button type="button" variant="ghost" size="xs"
                onClick={() => set({ queries: [...model.queries, blankMxFormula(nextMxFormulaId(model.queries))] })}>+ Formül</Button>
              {model.scopeOff && cluster && (
                <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => set({ scopeOff: false })}>
                  Kapsamı geri aç ({cluster})
                </Button>
              )}
              <span className="flex-1" />
              <span className="text-xs text-muted-foreground">Yazdıkça çalışır.</span>
            </div>
            {generated.length > 0 && (
              <details className="mt-2 border-t pt-2 text-xs">
                <summary className="cursor-pointer text-muted-foreground">Üretilen MetricsQL</summary>
                {generated.map(g => (
                  <div key={g.id} className="mt-1 flex flex-col gap-1">
                    <pre className="m-0 overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs whitespace-pre-wrap break-all"><b>{g.id}</b>  {g.text}</pre>
                  </div>
                ))}
                <Button type="button" variant="link" size="xs" className="h-auto px-0"
                  onClick={() => set({ mode: 'code', code: generated[0].text })}>MetricsQL kipinde aç →</Button>
              </details>
            )}
            {plan.errors.length > 0 && null}
          </>
        ) : (
          <CodePanel code={model.code} src={model.src} caps={caps} canThanos={canEdit} thanosCluster={cluster}
            onRun={code => set({ code })} onSrc={(src: MxSrc) => set({ src })} error={codeErr} scopeChip={scopeChipCode} />
        )}
      </section>

      <ResultPanel model={model} results={results} stepSec={plan.stepSec}
        onModel={p => set(p)} heatmap={heatmap} onSwitchToBucket={switchToBucket}
        sourceOverride={plan.src === 'thanos' ? `Thanos · ${cluster}` : caps ? sourceLabel(caps.source) : undefined}
        emptyHint={model.mode === 'code' ? 'Sorguyu yazıp Çalıştır\'a basın.' : 'Bir metrik seçin; sorgu yazdıkça çalışır. Metrikleri Keşif sekmesinde gezebilirsiniz.'}
        unitFor={id => plan.items.find(i => i.id === id)?.unit} />

      <AddToDashboardDialog open={dashOpen} onOpenChange={setDashOpen}
        buildPanels={() => panelsFromModel(model, rangeSec, newPanelId)} />
    </div>
  );
}
