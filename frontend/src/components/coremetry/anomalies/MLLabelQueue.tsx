// MLLabelQueue — v0.10.2484 (precision programı B.3/2). Sinyaller sekmesinin
// "Bu hafta etiketle (10)" çipi: son 7 günün etiketsiz ML olaylarından
// sunucunun deterministik, tabakalı örneği (GET /api/ml/label-queue;
// chstore.MLLabelQueueSample). Satır olay sayfasına ?lq=1 ile gider; etiket
// oradaki mevcut "Gerçek anomali / Sorun değil" düğmesiyle verilir ve karne
// kuyruk etiketlerini organiklerden ayrı sayar. Ek düğme / ek tık yok.
// Etiketler hiçbir eşiği ya da kararı değiştirmez.
import { Link } from 'react-router';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { useMLLabelQueue } from '@/lib/queries';
import { fmtDateTime } from '@/lib/utils';
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { t as tr, tf, useLang, type Lang } from '@/lib/i18n';
import type { MLLabelQueueItem } from '@/lib/types';
import { labelQueueHref } from './labelQueue';

const cols = (lang: Lang): ColumnDef<MLLabelQueueItem>[] => [
  { id: 'started', label: tr('anomalies.col.started', lang), width: 160, priority: 1, sortValue: r => r.startedAt, naturalDir: 'desc' },
  { id: 'service', label: tr('anomalies.col.service', lang), width: 240, minWidth: 140, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
  { id: 'detector', label: tr('labelQueue.col.detector', lang), flex: true, minWidth: 180, priority: 1, sortValue: r => mlDetectorLabel(r.detector, lang), naturalDir: 'asc' },
  { id: 'open', label: '', width: 150, minWidth: 120, priority: 1, kind: 'actions' },
];

export function MLLabelQueue() {
  const lang = useLang();
  const q = useMLLabelQueue();
  const d = q.data;
  const state = q.isError ? { kind: 'error' as const, message: tr('labelQueue.error', lang) }
    : q.isLoading ? { kind: 'loading' as const } : { kind: 'empty' as const, message: tr('labelQueue.empty', lang) };
  return (
    <div className="flex flex-col gap-2" data-testid="ml-label-queue">
      {d && (
        <p className="m-0 text-sm text-muted-foreground" data-testid="ml-label-queue-hint">
          {tf('labelQueue.hint', lang, { w: d.week, d: d.doneWeek, s: d.size })}
        </p>
      )}
      {d?.truncated && <p className="m-0 text-sm text-[color:var(--warn)]">{tr('labelQueue.truncated', lang)}</p>}
      <DataTable<MLLabelQueueItem>
        storageKey="ml-label-queue" columns={cols(lang)} rows={d?.items ?? []} state={state}
        sort={{ initial: { id: 'started', dir: 'desc' } }} getRowKey={r => r.id} ariaLabel={tr('labelQueue.aria', lang)}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'started': return <span className="font-mono">{fmtDateTime(new Date(r.startedAt / 1e6))}</span>;
            case 'service': return <span className="dt-trunc font-mono" title={r.service}>{r.service || '—'}</span>;
            case 'detector': return mlDetectorLabel(r.detector, lang);
            case 'open': return <Link to={labelQueueHref(r.id)}>{tr('labelQueue.open', lang)}</Link>;
            default: return null;
          }
        }} />
    </div>
  );
}
