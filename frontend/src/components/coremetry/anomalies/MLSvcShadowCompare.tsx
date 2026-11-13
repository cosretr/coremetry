// MLSvcShadowCompare — v0.10.2089 (spec docs/plans/spec-ml-service-level.md
// dilim 3): eski servis düzeyi Problem'leri (svc-slowdown:*, anomaly:*:error_rate)
// ile yeni ML gölge kararlarının (anomaly-auto:svc:*, Problem AÇILMAZ) yan yana
// tablosu. Spec "mevcut ML karşılaştırma raporu" diyor; kodda yok — burada,
// Anomalies › ML "Teknik detay"ın içinde. Bölüm açılınca TEK istek (60 sn
// önbellek), yoklama yok.
import { useState } from 'react';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { Empty, Spinner } from '@/components/Spinner';
import { useMLSvcShadowCompare } from '@/lib/queries';
import type { MLSvcCompareRow } from '@/lib/types';
import { fmtDateTime } from '@/lib/utils';
import { SearchX } from 'lucide-react';
import { SVC_DIFF_TR, svcCompareSummary } from './mlSvcCompare';

const OLD_RULE_TR = (r?: string) => (!r ? '—' : r.startsWith('svc-slowdown:') ? 'Yaygın yavaşlama (eski)' : 'Hata oranı (eski)');
const NEW_DET_TR = (d?: string) => (!d ? '—' : d === 'svc_breadth' ? 'Yaygın yavaşlama' : d === 'svc_error_rate' ? 'Servis hata oranı' : d);
const ts = (ns?: number) => (ns ? fmtDateTime(new Date(ns / 1e6)) : '—');

const COLS: ColumnDef<MLSvcCompareRow>[] = [
  { id: 'service', label: 'Servis', width: 180, minWidth: 120, flex: true, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
  { id: 'old', label: 'Eski kural', width: 170, minWidth: 120, priority: 1, sortValue: r => r.oldRule ?? '' },
  { id: 'oldStart', label: 'Eski açılış', width: 150, minWidth: 110, priority: 2, sortValue: r => r.oldStart ?? 0 },
  { id: 'oldPrio', label: 'Eski öncelik', width: 90, priority: 2, sortValue: r => r.oldPriority ?? '' },
  { id: 'new', label: 'Yeni olurdu', width: 160, minWidth: 110, priority: 1, sortValue: r => r.newDetector ?? '' },
  { id: 'newStart', label: 'Yeni açılış', width: 150, minWidth: 110, priority: 2, sortValue: r => r.newStart ?? 0 },
  { id: 'newPrio', label: 'Yeni öncelik', width: 90, priority: 2, sortValue: r => r.newPriority ?? '' },
  { id: 'diff', label: 'Fark', width: 110, priority: 1, sortValue: r => r.diff },
];

export function MLSvcShadowCompareSection() {
  const [open, setOpen] = useState(false);
  const q = useMLSvcShadowCompare(open);
  return (
    <details data-testid="ml-svc-shadow-compare" className="rounded-md border border-border px-3 py-2" onToggle={e => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">
        Servis düzeyi: eski Problem ↔ yeni gölge (14 gün)
      </summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-xs text-muted-foreground">
          Yeni servis düzeyi kurallar (servis hata oranı, yaygın yavaşlama) şimdilik Problem açmaz; burada aynı servis ve zamanda eski
          kuralların açtığı Problem'lerle karşılaştırılır.
        </p>
        {q.isLoading && <Spinner />}
        {q.error && <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title="Karşılaştırma yüklenemedi">Bir süre sonra bölümü kapatıp yeniden açın.</Empty>}
        {q.data && (
          <>
            <p className="m-0 text-sm" data-testid="ml-svc-shadow-summary">{svcCompareSummary(q.data.counts, q.data.days, q.data.truncated)}</p>
            <DataTable<MLSvcCompareRow>
              storageKey="anomalies-ml-svc-shadow" columns={COLS} rows={q.data.rows}
              state={{ kind: 'empty', message: 'Bu dönemde iki taraf da Problem üretmedi' }} sort={{ initial: { id: 'oldStart', dir: 'desc' } }}
              getRowKey={r => `${r.service}|${r.oldRule ?? ''}|${r.oldStart ?? 0}|${r.newDetector ?? ''}|${r.newStart ?? 0}`} ariaLabel="Servis düzeyi karşılaştırma"
              renderCell={(r, c) => {
                switch (c.id) {
                  case 'service': return r.service;
                  case 'old': return OLD_RULE_TR(r.oldRule);
                  case 'oldStart': return ts(r.oldStart);
                  case 'oldPrio': return r.oldPriority || '—';
                  case 'new': return NEW_DET_TR(r.newDetector);
                  case 'newStart': return ts(r.newStart);
                  case 'newPrio': return r.newPriority || '—';
                  case 'diff': return r.diff === 'missed' || r.diff === 'late'
                    ? <span className="text-[color:var(--warn)]">{SVC_DIFF_TR[r.diff]}</span> : (SVC_DIFF_TR[r.diff] ?? r.diff);
                  default: return null;
                }
              }} />
          </>
        )}
      </div>
    </details>
  );
}
