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
import { svcDiffLabel, svcCompareSummary } from './mlSvcCompare';
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { t as tr, useLang, type Lang } from '@/lib/i18n';

// v0.10.2282 — metinler arayüz dilinde (lib/i18n mlSvc.*; TR metin aynı).
const oldRuleLabel = (lang: Lang, r?: string) => (!r ? '—' : tr(r.startsWith('svc-slowdown:') ? 'mlSvc.oldSlowdown' : 'mlSvc.oldErrorRate', lang));
const newDetLabel = (lang: Lang, d?: string) => (!d ? '—' : d === 'svc_breadth' || d === 'svc_error_rate' ? mlDetectorLabel(d, lang) : d);
const ts = (ns?: number) => (ns ? fmtDateTime(new Date(ns / 1e6)) : '—');

const colsFor = (lang: Lang): ColumnDef<MLSvcCompareRow>[] => { const c = (k: string) => tr(`mlSvc.col.${k}`, lang); return [
  { id: 'service', label: tr('anomalies.col.service', lang), width: 180, minWidth: 120, flex: true, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
  { id: 'old', label: c('old'), width: 170, minWidth: 120, priority: 1, sortValue: r => r.oldRule ?? '' },
  { id: 'oldStart', label: c('oldStart'), width: 150, minWidth: 110, priority: 2, sortValue: r => r.oldStart ?? 0 },
  { id: 'oldPrio', label: c('oldPrio'), width: 90, priority: 2, sortValue: r => r.oldPriority ?? '' },
  { id: 'new', label: c('new'), width: 160, minWidth: 110, priority: 1, sortValue: r => r.newDetector ?? '' },
  { id: 'newStart', label: c('newStart'), width: 150, minWidth: 110, priority: 2, sortValue: r => r.newStart ?? 0 },
  { id: 'newPrio', label: c('newPrio'), width: 90, priority: 2, sortValue: r => r.newPriority ?? '' },
  { id: 'diff', label: c('diff'), width: 110, priority: 1, sortValue: r => r.diff },
]; };
// v0.10.2307 — İngilizce katalog tembel (lib/i18n.ts): EN kolonları modül
// yüklenirken değil ilk kullanımda (etkin dil 'en' = katalog yüklü) kurulur.
const COLS_BY_LANG: Partial<Record<Lang, ColumnDef<MLSvcCompareRow>[]>> = {};
const colsOf = (lang: Lang): ColumnDef<MLSvcCompareRow>[] => (COLS_BY_LANG[lang] ??= colsFor(lang));

export function MLSvcShadowCompareSection() {
  const [open, setOpen] = useState(false);
  const q = useMLSvcShadowCompare(open);
  const lang = useLang();
  const t = (k: string) => tr(`mlSvc.${k}`, lang);
  return (
    <details data-testid="ml-svc-shadow-compare" className="rounded-md border border-border px-3 py-2" onToggle={e => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">
        {t('title')}
      </summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-xs text-muted-foreground">
          {t('intro')}
        </p>
        {q.isLoading && <Spinner />}
        {q.error && <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title={t('error')}>{t('retry')}</Empty>}
        {q.data && (
          <>
            <p className="m-0 text-sm" data-testid="ml-svc-shadow-summary">{svcCompareSummary(q.data.counts, q.data.days, q.data.truncated, lang)}</p>
            <DataTable<MLSvcCompareRow>
              storageKey="anomalies-ml-svc-shadow" columns={colsOf(lang)} rows={q.data.rows}
              state={{ kind: 'empty', message: t('empty') }} sort={{ initial: { id: 'oldStart', dir: 'desc' } }}
              getRowKey={r => `${r.service}|${r.oldRule ?? ''}|${r.oldStart ?? 0}|${r.newDetector ?? ''}|${r.newStart ?? 0}`} ariaLabel={t('aria')}
              renderCell={(r, c) => {
                switch (c.id) {
                  case 'service': return r.service;
                  case 'old': return oldRuleLabel(lang, r.oldRule);
                  case 'oldStart': return ts(r.oldStart);
                  case 'oldPrio': return r.oldPriority || '—';
                  case 'new': return newDetLabel(lang, r.newDetector);
                  case 'newStart': return ts(r.newStart);
                  case 'newPrio': return r.newPriority || '—';
                  case 'diff': return r.diff === 'missed' || r.diff === 'late'
                    ? <span className="text-[color:var(--warn)]">{svcDiffLabel(r.diff, lang)}</span> : svcDiffLabel(r.diff, lang);
                  default: return null;
                }
              }} />
          </>
        )}
      </div>
    </details>
  );
}
