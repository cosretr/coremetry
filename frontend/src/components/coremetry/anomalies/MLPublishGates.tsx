// MLPublishGates — v0.10.2343 (anomali kartı A2 kapalı gelir, K16): yayın
// kapısı gölge raporu ekranda. Operatör A2'yi açmadan önce bu tabloyu okur:
// dedektör başına değerlendirilen / yayımlanırdı / tutulurdu / bilgiye inerdi
// ve tutulacak olaylardan açık Problem'le çakışan ya da Problem'e dönüşen
// sayısı + örnek olay kimlikleri. Veri skor kartının tik durumunda (ek istek
// yok); Anomaliler › ML › Teknik detay içinde.
import { Link } from 'react-router-dom';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import type { MLGateShadow, MLTickStatus } from '@/lib/types';
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { t as tr, tf, useLang, type Lang } from '@/lib/i18n';

export interface GateRow extends MLGateShadow { detector: string }

/** SAF (test): tik durumundan dedektör satırları; değerlendirilen olmayan dedektör düşer. */
export function publishGateRows(t: MLTickStatus | undefined): GateRow[] {
  return Object.entries(t?.publishGates ?? {})
    .filter(([, r]) => r.evaluated > 0)
    .map(([detector, r]) => ({ ...r, detector }));
}

const colsFor = (lang: Lang): ColumnDef<GateRow>[] => { const c = (k: string) => tr(`mlGates.col.${k}`, lang); return [
  { id: 'detector', label: c('detector'), width: 200, minWidth: 140, flex: true, priority: 1, sortValue: r => mlDetectorLabel(r.detector, lang), naturalDir: 'asc' },
  { id: 'evaluated', label: c('evaluated'), width: 120, numeric: true, priority: 1, sortValue: r => r.evaluated },
  { id: 'publish', label: c('publish'), width: 120, numeric: true, priority: 2, sortValue: r => r.wouldPublish },
  { id: 'drop', label: c('drop'), width: 110, numeric: true, priority: 1, sortValue: r => r.wouldDrop },
  { id: 'info', label: c('info'), width: 120, numeric: true, priority: 2, sortValue: r => r.wouldInfo },
  { id: 'dropProblem', label: c('dropProblem'), width: 170, numeric: true, priority: 1, sortValue: r => r.wouldDropProblem },
  { id: 'dropEscalated', label: c('dropEscalated'), width: 170, numeric: true, priority: 1, sortValue: r => r.wouldDropEscalated },
  { id: 'examples', label: c('examples'), width: 220, minWidth: 140, priority: 2, sortValue: r => r.dropExamples?.length ?? 0 },
]; };
const COLS_BY_LANG: Partial<Record<Lang, ColumnDef<GateRow>[]>> = {};
const colsOf = (lang: Lang): ColumnDef<GateRow>[] => (COLS_BY_LANG[lang] ??= colsFor(lang));

export function MLPublishGatesSection({ tick }: { tick?: MLTickStatus }) {
  const lang = useLang();
  const t = (k: string) => tr(`mlGates.${k}`, lang);
  const n = (v: number) => v.toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US');
  return (
    <details data-testid="ml-publish-gates" className="rounded-md border border-border px-3 py-2">
      <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">{t('title')}</summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-sm font-semibold" data-testid="ml-publish-gates-mode">{t(tick?.publishGatesEnforced ? 'modeOn' : 'modeOff')}</p>
        <p className="m-0 text-xs text-muted-foreground">{t('criterion')}</p>
        {tick?.publishGatesWindowH ? <p className="m-0 text-xs text-muted-foreground">{tf('mlGates.window', lang, { h: tick.publishGatesWindowH })}</p> : null}
        <DataTable<GateRow>
          storageKey="anomalies-ml-publish-gates" columns={colsOf(lang)} rows={publishGateRows(tick)}
          state={{ kind: 'empty', message: t('empty') }} sort={{ initial: { id: 'drop', dir: 'desc' } }}
          getRowKey={r => r.detector} ariaLabel={t('aria')}
          renderCell={(r, c) => {
            switch (c.id) {
              case 'detector': return mlDetectorLabel(r.detector, lang);
              case 'evaluated': return n(r.evaluated);
              case 'publish': return n(r.wouldPublish);
              case 'drop': return n(r.wouldDrop);
              case 'info': return n(r.wouldInfo);
              case 'dropProblem': return r.wouldDropProblem ? <span className="text-[color:var(--warn)]">{n(r.wouldDropProblem)}</span> : n(0);
              case 'dropEscalated': return r.wouldDropEscalated ? <span className="text-[color:var(--warn)]">{n(r.wouldDropEscalated)}</span> : n(0);
              case 'examples': return r.dropExamples?.length
                ? <span className="inline-flex flex-wrap gap-2">{r.dropExamples.map(id => <Link key={id} to={`/anomalies/ml/${encodeURIComponent(id)}`} className="font-mono">{id.slice(0, 8)}</Link>)}</span>
                : '—';
              default: return null;
            }
          }} />
      </div>
    </details>
  );
}
