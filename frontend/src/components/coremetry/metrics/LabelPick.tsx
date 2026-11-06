// coremetry/metrics/LabelPick — v0.10.1862 (Metrics Explorer Faz 2 dilim 2.2).
//
// Etiket ADI ve etiket DEĞERİ seçicileri: sunucu araması (`/api/metrics/
// discover/labels|label-values?match=<metrik>&q=&cluster=`; VM'de limit
// ≤ 200, pencere ≤ 24 sa, 60 sn önbellek). ClickHouse'ta uç
// {supported:false, reason} döner → liste yerine nedeni yazılır, elle
// yazım (↵) yine çalışır (desteklenmeyen kapalı, yol kapanmaz).
import { useState } from 'react';
import { api } from '@/lib/api';
import type { MetricsDiscoverLabels, MetricsDiscoverLabelValues } from '@/lib/types';
import { PickList } from './PickList';
import { useServerSearch } from './useServerSearch';
import { LABEL_PICK_LIMIT, labelMatch } from './labels';

export interface LabelNamePickProps {
  metric: string;
  cluster?: string;
  /** Zaten seçili adlar (listede soluk değil, gizli). */
  exclude?: string[];
  onPick(name: string): void;
  label?: string;
}

export function LabelNamePick({ metric, cluster, exclude, onPick, label = 'Etiket ara' }: LabelNamePickProps) {
  const [q, setQ] = useState('');
  const match = labelMatch(metric);
  const res = useServerSearch<MetricsDiscoverLabels>(true, `${match ?? ''}\u0000${q}\u0000${cluster ?? ''}`,
    signal => api.metricsDiscoverLabels({ match, q: q.trim() || undefined, limit: LABEL_PICK_LIMIT, cluster }, signal));
  const d = res.data;
  const rows = d && d.supported ? d.labels.filter(l => l.name !== '__name__' && !(exclude ?? []).includes(l.name)) : [];
  const footer = d && !d.supported
    ? <span>Öneri yok: {d.reason} · adı yazıp ↵</span>
    : d && d.supported
      ? <span>Sunucu araması{metric ? ' · bu metrikte' : ''} · {rows.length}{d.hasMore ? '+' : ''} etiket{d.partial ? ' · kısmi' : ''}</span>
      : null;
  return (
    <PickList label={label} placeholder="Etiket adı" query={q} onQuery={setQ} allowFree
      loading={res.loading} error={res.error ? `Etiketler okunamadı: ${res.error}` : null}
      items={rows.map(l => ({ value: l.name, label: l.name, aside: l.valueCount != null ? `${l.valueCount} değer` : undefined }))}
      footer={footer} onPick={onPick} />
  );
}

export interface LabelValuePickProps {
  metric: string;
  labelName: string;
  cluster?: string;
  onPick(value: string): void;
  initial?: string;
}

export function LabelValuePick({ metric, labelName, cluster, onPick, initial = '' }: LabelValuePickProps) {
  const [q, setQ] = useState(initial);
  const match = labelMatch(metric);
  const res = useServerSearch<MetricsDiscoverLabelValues>(!!labelName, `${labelName}\u0000${match ?? ''}\u0000${q}\u0000${cluster ?? ''}`,
    signal => api.metricsDiscoverLabelValues({ label: labelName, match, q: q.trim() || undefined, limit: LABEL_PICK_LIMIT, cluster }, signal));
  const d = res.data;
  const rows = d && d.supported ? d.values : [];
  const footer = d && !d.supported
    ? <span>Öneri yok: {d.reason} · değeri yazıp ↵</span>
    : d && d.supported
      ? <span>Değer önerileri{metric ? ' · bu metrikte' : ''}, son {Math.round(d.windowSec / 3600)} sa · {rows.length}{d.hasMore ? '+' : ''} değer</span>
      : null;
  return (
    <PickList label={`${labelName} değeri ara`} placeholder="Değer (ya da regex)" query={q} onQuery={setQ} allowFree
      loading={res.loading} error={res.error ? `Değerler okunamadı: ${res.error}` : null}
      items={rows.map(v => ({ value: v, label: v }))} footer={footer} onPick={onPick} />
  );
}
