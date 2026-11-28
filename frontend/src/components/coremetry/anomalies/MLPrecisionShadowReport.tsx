// MLPrecisionShadowReport — v0.10.2486 (precision programı B.2): Sinyaller ›
// Teknik ayrıntı › Dedektör durumu altındaki "otomatik gölge kuralı" bloğu.
// Kip satırı, son değerlendirme, düşerdi / gölgedeki dedektörlerin pencerede
// beslediği Problem ve bunların onaylı, gürültü olmayan vakalı olanları
// (operatörün `on` onayından önce gördüğü sayı) ve son 30 günün kayıtları.
// Veri karne zarfından (ek istek yok). Yeni sayfa yok.
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { tf, type Lang } from '@/lib/i18n';
import { count } from '@/lib/format/number';
import { dateTime } from '@/lib/format/time';
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import type { MLPrecisionRecord, MLPrecisionView } from '@/lib/types';
import { fedLine, precisionModeText, recordNumbersText, recordOutcomeText } from './mlPrecisionShadow';

const M = (k: string, lang: Lang, vars?: Record<string, string | number>) => tf(`mlTab.${k}`, lang, vars);

const colsFor = (lang: Lang): ColumnDef<MLPrecisionRecord>[] => [
  { id: 'day', label: M('shadow.col.day', lang), width: 110, minWidth: 90, priority: 1, sortValue: r => r.at },
  { id: 'detector', label: M('col.detector', lang), width: 180, minWidth: 130, priority: 1, sortValue: r => mlDetectorLabel(r.detector, lang), naturalDir: 'asc' },
  { id: 'outcome', label: M('shadow.col.outcome', lang), width: 170, minWidth: 120, priority: 1, sortValue: r => r.outcome, naturalDir: 'asc',
    tone: r => (r.outcome === 'would_demote' || r.outcome === 'demote' ? 'warn' : undefined) },
  { id: 'nums', label: M('shadow.col.nums', lang), width: 210, minWidth: 150, priority: 2, numeric: true, sortValue: r => r.n },
  { id: 'problems', label: M('shadow.col.problems', lang), width: 130, minWidth: 100, priority: 2, numeric: true, sortValue: r => r.problemsFed },
  { id: 'reason', label: M('shadow.col.reason', lang), width: 360, minWidth: 180, priority: 3, flex: true },
];
const COLS: Partial<Record<Lang, ColumnDef<MLPrecisionRecord>[]>> = {};
const colsOf = (lang: Lang) => (COLS[lang] ??= colsFor(lang));

export function MLPrecisionShadowReport({ view, lang }: { view: MLPrecisionView | undefined; lang: Lang }) {
  if (!view) return null;
  const recs = view.records ?? [];
  const listed = view.wouldDemote ?? [];
  return (
    <section data-testid="ml-precision-shadow" className="flex flex-col gap-1.5">
      <h4 className="m-0 text-[length:var(--fs-sm)] font-semibold">{M('shadow.title', lang)}</h4>
      <p className="m-0 text-[length:var(--fs-sm)] text-muted-foreground" data-testid="ml-precision-mode">
        {precisionModeText(view.mode, lang)}
        {view.lastEvalAt ? ` ${M('shadow.lastEval', lang, { t: dateTime(view.lastEvalAt * 1000) })}` : ''}
      </p>
      {view.lastError && <p className="m-0 text-[length:var(--fs-sm)] text-[color:var(--warn)]">{M('shadow.error', lang, { e: view.lastError })}</p>}
      {listed.length > 0 && (
        <div data-testid="ml-precision-fed" className="text-[length:var(--fs-sm)]">
          <div className="font-semibold">{M('shadow.fedTitle', lang)}</div>
          {listed.map(d => <div key={d}>{fedLine(mlDetectorLabel(d, lang), view.fed?.[d], lang)}</div>)}
        </div>
      )}
      <DataTable<MLPrecisionRecord>
        storageKey="anomalies-ml-precision-shadow" columns={colsOf(lang)} rows={recs}
        state={{ kind: 'empty', message: M('shadow.empty', lang) }} sort={{ initial: { id: 'day', dir: 'desc' } }}
        getRowKey={r => `${r.at}:${r.detector}:${r.outcome}`} ariaLabel={M('shadow.aria', lang)}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'day': return r.day;
            case 'detector': return mlDetectorLabel(r.detector, lang);
            case 'outcome': return recordOutcomeText(r.outcome, lang);
            case 'nums': return recordNumbersText(r, lang);
            case 'problems': return `${count(r.problemsFed, lang)} (${count(r.problemsReal, lang)})`;
            case 'reason': return <span className="dt-trunc" title={r.reason}>{r.reason}</span>;
            default: return null;
          }
        }} />
    </section>
  );
}
