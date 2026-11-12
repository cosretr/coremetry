// coremetry/dashboard/PanelQueryEditor — v0.10.2016 (Dashboards Faz 3 dilim
// 3.7 kalanı; audit §5.4 "Sorgu: Metrics Explorer ile AYNI QueryRow.
// Builder ⇄ MetricsQL aynı anahtar"; mockup görünüm 6 "qcard").
//
// metric ve promql panellerinin sorgu bölgesi. Builder kipinde TEK
// coremetry/metrics/QueryRow satırı (formül satırı pano panelinde yok —
// formüllü sorgu kod kipinde yazılır), MetricsQL kipinde Metrics Explorer'ın
// MetricsQLEditor'ü. Builder → kod her zaman; kod → builder yalnız metin
// builder biçimindeyse (değilse nedeni yazılır, kipte kalınır).
//
// Eski builder metric paneli (metricName / agg / groupBy) açılınca satıra
// ÇEVRİLİR (lib/metricsExplorer/panelQuery). Tür son ekten anlaşılmayan
// histogram taban adı için metriğin türü /api/metrics/names'ten okunur
// (v0.10.1887 dersi: histogram avg = rate(_sum)/rate(_count)). Panel config'i
// yalnız operatör sorguyu DEĞİŞTİRİNCE yazılır: aç-kapa panoyu bayt-aynı bırakır.
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { MetricsQLEditor, QueryRow } from '@/components/coremetry/metrics';
import { Button } from '@/components/shadcn/button';
import { Spinner } from '@/components/Spinner';
import { api } from '@/lib/api';
import { parseBuilderForm } from '@/lib/metricsExplorer/parse';
import { compileMxQuery } from '@/lib/metricsExplorer/compile';
import {
  isLegacyMetricPanel, panelQueryState, panelQueryText, type PanelQueryMode, type PanelQueryState,
} from '@/lib/metricsExplorer/panelQuery';
import type { MxQuery } from '@/lib/metricsExplorer/model';
import type { MetricPanelConfig, Panel } from '@/lib/types';

export interface PanelQueryEditorProps {
  panel: Panel;
  /** Metin değişti (builder'da derlenmiş MetricsQL). `query` builder satırı. */
  onQuery(text: string, query?: MxQuery): void;
  /** "Explorer'da aç" hedefi (varsa). */
  explorerHref?: string | null;
}

export function PanelQueryEditor({ panel, onQuery, explorerHref }: PanelQueryEditorProps) {
  const legacy = isLegacyMetricPanel(panel);
  const metricName = legacy ? ((panel.config as MetricPanelConfig).metricName ?? '').trim() : '';
  // Eski builder paneli: metriğin türü (ad zarfı). Tek istek, açılışta.
  const typeQ = useQuery({
    queryKey: ['panel-editor-metric-type', metricName],
    enabled: legacy && metricName !== '',
    queryFn: ({ signal }) => api.metricNamesSearch('', metricName, 5, 0, undefined, { signal }),
    staleTime: 5 * 60_000,
  });
  const reported = typeQ.data?.names.find(n => n.name === metricName)?.type;
  const ready = !legacy || metricName === '' || !typeQ.isLoading;

  const caps = useQuery({
    queryKey: ['panel-editor', 'metrics-capabilities'],
    queryFn: () => api.metricsCapabilities(),
    staleTime: 30_000,
  });
  const metricsql = caps.data?.metricsql ?? true;

  const [st, setSt] = useState<PanelQueryState | null>(null);
  const [reason, setReason] = useState<string | null>(null);
  const seeded = useRef<string | null>(null);
  // Panel kimliği değişince (ya da tür okunduğunda) durum kayıtlı panelden kurulur.
  useEffect(() => {
    if (!ready) return;
    const key = `${panel.id}\u0000${reported ?? ''}`;
    if (seeded.current === key) return;
    seeded.current = key;
    setSt(panelQueryState(panel, reported));
    setReason(null);
  }, [ready, panel, reported]);

  if (!st) return <div className="flex h-16 items-center justify-center"><Spinner /></div>;

  const emit = (next: PanelQueryState) => {
    setSt(next);
    onQuery(panelQueryText(next), next.mode === 'builder' ? next.query : undefined);
  };
  const switchMode = (m: PanelQueryMode) => {
    if (m === st.mode) return;
    if (m === 'code') {
      setReason(null);
      emit({ ...st, mode: 'code', code: compileMxQuery(st.query) || st.code });
      return;
    }
    const p = parseBuilderForm(st.code);
    if (!p.ok) { setReason(p.reason); return; }
    setReason(null);
    emit({ ...st, mode: 'builder', query: p.query, notes: p.notes });
  };

  return (
    <section aria-label="Sorgu" className="flex flex-col gap-2 rounded-md border p-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <SegmentedControl aria-label="Sorgu kipi" size="sm" value={st.mode} onChange={switchMode}
          options={[{ value: 'builder', label: 'Builder' }, { value: 'code', label: 'MetricsQL' }]} />
        <span className="flex-1" />
        {explorerHref && (
          <Button asChild variant="ghost" size="xs"><Link to={explorerHref}>Explorer'da aç</Link></Button>
        )}
      </div>
      {st.mode === 'builder'
        ? <QueryRow query={st.query} showLegend={false} onChange={q => emit({ ...st, query: q })} />
        : <MetricsQLEditor value={st.code} onChange={v => emit({ ...st, code: v })} onRun={() => undefined}
            metricsql={metricsql} metricsqlReason={metricsql ? undefined : 'ClickHouse: PromQL alt kümesi'}
            ariaLabel="Panel sorgusu (MetricsQL)" />}
      {reason && <p role="status" className="text-xs text-status-warning">{reason}</p>}
      {st.notes.length > 0 && <p className="text-xs text-muted-foreground">{st.notes.join(' · ')}</p>}
    </section>
  );
}
