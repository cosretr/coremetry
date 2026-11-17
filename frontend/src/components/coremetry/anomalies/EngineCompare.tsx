// EngineCompare — v0.10.2310: Davis özneleri ↔ ML gölge karşılaştırma raporu
// (GET /api/anomalies/engine-compare, api/anomaly_engine_compare.go).
// MLSvcShadowCompare emsali: Anomalies › ML "Teknik detay"ın içinde kapalı
// bölüm; açılınca TEK istek (5 dk önbellek), yoklama yok. Salt-okunur.
// Örneklenmiş kuyruk satırları (yanlı) rozetle işaretli; toplam cümlesi
// onları saymaz (sunucu). Renk yalnız rozette ve sıfır olmayan "yalnız"
// hücrelerinde.
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { Badge } from '@/components/shadcn/badge';
import { Empty, Spinner } from '@/components/Spinner';
import { useAnomalyEngineCompare } from '@/lib/queries';
import type { EngineCompareRow } from '@/lib/types';
import { anomalyEventHref, mlEventHref } from '@/lib/anomalyHref';
import { fmtDurShort } from '@/lib/utils';
import { SearchX } from 'lucide-react';
import { t as tr, useLang, type Lang } from '@/lib/i18n';
import { engineCaveatText, engineSummary } from './engineCompareText';

const pair = (a: number, b: number) => `${a} / ${b}`;
const dur = (s: number) => (s > 0 ? fmtDurShort(s) : '—');


// Kolonlar dil başına ÇİZİM anında kurulur (v0.10.2307: EN katalog tembel).
const colsFor = (lang: Lang): ColumnDef<EngineCompareRow>[] => {
  const c = (k: string) => tr(`engCmp.${k}`, lang);
  const dm = (k: string) => `${c(k)} (D/ML)`;
  return [
    { id: 'service', label: tr('anomalies.col.service', lang), width: 180, minWidth: 120, flex: true, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
    { id: 'family', label: c('col.family'), width: 110, priority: 1, sortValue: r => r.family, naturalDir: 'asc' },
    { id: 'source', label: c('col.source'), width: 120, priority: 2, sortValue: r => r.source, naturalDir: 'asc' },
    { id: 'subject', label: c('col.subject'), width: 90, priority: 1, numeric: true, sortValue: r => r.subject.series },
    { id: 'ml', label: c('col.ml'), width: 90, priority: 1, numeric: true, sortValue: r => r.ml.events },
    { id: 'overlap', label: dm('overlap'), width: 100, priority: 1, numeric: true, sortValue: r => r.subject.matched + r.ml.matched },
    { id: 'onlySubject', label: c('onlySubject'), width: 90, priority: 1, numeric: true, sortValue: r => r.subject.only },
    { id: 'onlyMl', label: c('onlyMl'), width: 90, priority: 1, numeric: true, sortValue: r => r.ml.only },
    { id: 'indeterminate', label: c('indeterminate'), width: 90, priority: 2, numeric: true, sortValue: r => r.ml.indeterminate },
    { id: 'wouldOpen', label: dm('wouldOpen'), width: 120, priority: 2, numeric: true, sortValue: r => r.subject.wouldOpen + r.ml.wouldOpen },
    { id: 'withProblem', label: dm('withProblem'), width: 140, priority: 2, numeric: true, sortValue: r => r.subject.withProblem + r.ml.withProblem },
    { id: 'median', label: c('col.median'), width: 120, priority: 3, numeric: true, sortValue: r => r.subject.medianSec + r.ml.medianSec },
    { id: 'short', label: dm('short'), width: 120, priority: 3, numeric: true, sortValue: r => r.subject.short + r.ml.short },
    { id: 'examples', label: c('col.examples'), width: 140, priority: 3, sortValue: r => r.examples.onlySubject.length + r.examples.onlyMl.length },
  ];
};
const COLS_BY_LANG: Partial<Record<Lang, ColumnDef<EngineCompareRow>[]>> = {};
const colsOf = (lang: Lang): ColumnDef<EngineCompareRow>[] => (COLS_BY_LANG[lang] ??= colsFor(lang));

const onlyCell = (n: number) => (n > 0 ? <span className="text-[color:var(--warn)]">{n}</span> : n);

export function EngineCompareSection() {
  const [open, setOpen] = useState(false);
  const q = useAnomalyEngineCompare(open);
  const lang = useLang();
  const t = (k: string) => tr(`engCmp.${k}`, lang);
  const d = q.data;
  const truncated = d ? d.truncated.subject || d.truncated.ml || d.truncated.problems || d.truncated.rows : false;
  return (
    <details data-testid="engine-compare" className="rounded-md border border-border px-3 py-2" onToggle={e => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">
        {t('title')}
      </summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-xs text-muted-foreground">{t('intro')}</p>
        {q.isLoading && <Spinner />}
        {q.error && <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title={tr('mlSvc.error', lang)}>{tr('mlSvc.retry', lang)}</Empty>}
        {d && (
          <>
            {d.caveats.length > 0 && (
              <ul className="m-0 flex list-none flex-col gap-0.5 p-0 text-xs text-muted-foreground" data-testid="engine-compare-caveats">
                {d.caveats.map(code => <li key={code}>{engineCaveatText(code, lang)}</li>)}
              </ul>
            )}
            <p className="m-0 text-sm" data-testid="engine-compare-summary">
              {engineSummary(d, lang)}{truncated ? ` ${t('truncated')}` : ''}
            </p>
            <DataTable<EngineCompareRow>
              storageKey="anomalies-engine-compare" columns={colsOf(lang)} rows={d.rows}
              state={{ kind: 'empty', message: t('empty') }} sort={{ initial: { id: 'subject', dir: 'desc' } }}
              getRowKey={r => `${r.service}|${r.family}|${r.source}`} ariaLabel={t('aria')}
              renderCell={(r, c) => {
                switch (c.id) {
                  case 'service': return r.service;
                  case 'family': return tr(`engCmp.fam.${r.family}`, lang);
                  case 'source': return r.biased
                    ? <Badge variant="outline" className="border-transparent bg-[color:var(--warn-bg)] text-[color:var(--warn)]"
                        title={t('badge.sampled')} aria-label={t('badge.sampled')} data-testid="engine-compare-sampled">{t('src.sampled')}</Badge>
                    : t('src.metric');
                  case 'subject': return r.subject.series;
                  case 'ml': return r.ml.events;
                  case 'overlap': return pair(r.subject.matched, r.ml.matched);
                  case 'onlySubject': return onlyCell(r.subject.only);
                  case 'onlyMl': return onlyCell(r.ml.only);
                  case 'indeterminate': return r.ml.indeterminate;
                  case 'wouldOpen': return pair(r.subject.wouldOpen, r.ml.wouldOpen);
                  case 'withProblem': return pair(r.subject.withProblem, r.ml.withProblem);
                  case 'median': return `${dur(r.subject.medianSec)} / ${dur(r.ml.medianSec)}`;
                  case 'short': return pair(r.subject.short, r.ml.short);
                  case 'examples': return (
                    <span className="flex flex-wrap gap-x-2">
                      {r.examples.onlySubject.map((id, i) => <Link key={`s${id}`} to={anomalyEventHref(id)}>{`Davis ${i + 1}`}</Link>)}
                      {r.examples.onlyMl.map((id, i) => <Link key={`m${id}`} to={mlEventHref(id)}>{`ML ${i + 1}`}</Link>)}
                    </span>
                  );
                  default: return null;
                }
              }} />
          </>
        )}
      </div>
    </details>
  );
}
