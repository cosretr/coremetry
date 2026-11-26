// ProblemSourceBreakdown — v0.10.2478 (operatör: "Çok fazla problem üretiyor,
// gerçek problemler olsun isterim"). Problems sayfasının "Döküm" görünümü:
// hangi kural ailesi kaç Problem açıyor, ne kadar sürüyor, ne kadarı 15 dk
// içinde kapanıyor. SALT OKUMA (GET /api/problems/breakdown, 5 dk cache) —
// viewer da aynısını görür; eşik / kapı / bildirim değiştiren hiçbir eylem yok.
//
// URL: ?view=breakdown çekmeceyi açar, ?bdw=24h|30d pencereyi seçer (7d
// varsayılan, param yok) — lib/urlSchema codec'i, geçersiz değer varsayılana
// düşer. Aile satırları TIKLANAMAZ: listenin aile süzgeci yok (yeni süzgeç
// bu dilimin kapsamı dışı); "En çok açan servisler" listenin mevcut
// ?service= süzgecine bağlanır. Yoklama yok — elle "Yenile" (refresh=1).
import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { DetailSheet } from '@/components/coremetry/DetailSheet';
import { DataTable as CmDataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { Button } from '@/components/shadcn/button';
import { Sparkline } from '@/components/Sparkline';
import { useProblemSourceBreakdown } from '@/lib/queries';
import { t as tl, tf, useLang, fmtPctLang, type Lang } from '@/lib/i18n';
import { fmtDurShort } from '@/lib/utils';
import { urlSpec, useUrlParams, zParam, zText, z } from '@/lib/urlSchema';
import type { ProblemSourceFamily, ProblemSourceWindow } from '@/lib/types';

const BREAKDOWN_WINDOWS: readonly ProblemSourceWindow[] = ['24h', '7d', '30d'];
const zWindow = z.pipe(z.string(), z.transform(s => s as ProblemSourceWindow))
  .check(z.refine(s => (BREAKDOWN_WINDOWS as readonly string[]).includes(s)));
const BREAKDOWN_URL_SPEC = urlSpec({
  view: zParam(zText(32), ''),
  bdw: zParam<ProblemSourceWindow>(zWindow, '7d'),
});
const BREAKDOWN_VIEW = 'breakdown';

/** SAF: aile anahtarı → etiket (bilinmeyen anahtar = ham anahtar, gizlenmez). */
function familyLabel(family: string, lang: Lang): string {
  const k = `inbox.bd.fam.${family}`;
  const v = tl(k, lang);
  return v === k ? family : v;
}

const pctText = (v: number, lang: Lang) => fmtPctLang(Math.round(v), lang);

// v0.10.2479 (operator-reported) — aile sütunu prod'da HİÇ görünmüyordu:
// `flex` + genişliksiz idi, sabit sütunlar ~1100 px'i 1040 px'lik çekmecede
// aşınca table-layout:fixed 'auto' sütuna 0 px bıraktı (/frontend-design-system
// "table-layout clip"). Aile (ve açılınca metrik) adı artık GERÇEK genişlikte;
// esneyen sütun sondaki "en çok açan servisler"; çekmece toplamı karşılar
// (DetailSheet genişliği viewport ile sınırlar, dar ekranda sığdırma
// önce öncelik-3 sütunları gizler).
const BREAKDOWN_SHEET_W = 1360;

function famCols(lang: Lang): ColumnDef<ProblemSourceFamily>[] {
  return [
    { id: 'family', label: tl('inbox.bd.col.family', lang), sortValue: r => familyLabel(r.family, lang), naturalDir: 'asc', width: 240, minWidth: 180, priority: 1 },
    { id: 'count', label: tl('inbox.bd.col.count', lang), sortValue: r => r.count, numeric: true, width: 90, minWidth: 70, priority: 1 },
    { id: 'share', label: tl('inbox.bd.col.share', lang), sortValue: r => r.sharePct, numeric: true, width: 70, minWidth: 56, priority: 2 },
    { id: 'services', label: tl('inbox.bd.col.services', lang), sortValue: r => r.services, numeric: true, width: 80, minWidth: 60, priority: 3 },
    { id: 'open', label: tl('inbox.bd.col.open', lang), sortValue: r => r.openNow, numeric: true, width: 70, minWidth: 56, priority: 2 },
    { id: 'sev', label: tl('inbox.bd.col.sev', lang), sortValue: r => r.critical, numeric: true, width: 150, minWidth: 110, priority: 3 },
    { id: 'median', label: tl('inbox.bd.col.median', lang), sortValue: r => r.medianSec, numeric: true, width: 100, minWidth: 80, priority: 2 },
    // Renk yalnız sapan değerde: yarısından fazlası 15 dk içinde kapanıyorsa (flap gürültüsü).
    { id: 'short', label: tl('inbox.bd.col.short', lang), sortValue: r => r.short15Pct, numeric: true, width: 130, minWidth: 100, priority: 2,
      tone: r => (r.count > 0 && r.short15Pct >= 50 ? 'warn' : undefined) },
    { id: 'long', label: tl('inbox.bd.col.long', lang), sortValue: r => r.long1h, numeric: true, width: 70, minWidth: 56, priority: 3 },
    { id: 'churn', label: tl('inbox.bd.col.churn', lang), sortValue: r => r.churnCount, numeric: true, width: 80, minWidth: 60, priority: 3,
      tone: r => (r.churnPairs > 0 ? 'warn' : undefined) },
    { id: 'top', label: tl('inbox.bd.col.top', lang), sortValue: r => r.topServices[0]?.count ?? 0, numeric: true, flex: true, width: 200, minWidth: 140, priority: 3 },
  ];
}

function serviceHrefInList(service: string): string {
  const p = new URLSearchParams({ service, status: 'all' });
  return `/inbox?${p.toString()}`;
}

/** Problems sayfasının tek giriş noktası: "Döküm" bağlantısı + çekmece. */
export function ProblemSourceBreakdownEntry() {
  const lang = useLang();
  const [{ view }, setUrl] = useUrlParams(BREAKDOWN_URL_SPEC);
  const open = view === BREAKDOWN_VIEW;
  return (
    <>
      <Button variant="link" size="sm" className="h-auto p-0" title={tl('inbox.bd.openTitle', lang)}
        onClick={() => setUrl({ view: BREAKDOWN_VIEW })}>
        {tl('inbox.bd.open', lang)}
      </Button>
      <DetailSheet open={open} onOpenChange={o => { if (!o) setUrl({ view: '', bdw: '7d' }); }}
        title={tl('inbox.bd.title', lang)} width={BREAKDOWN_SHEET_W}>
        {open && <ProblemSourceBreakdownView />}
      </DetailSheet>
    </>
  );
}

export function ProblemSourceBreakdownView() {
  const lang = useLang();
  const [{ bdw }, setUrl] = useUrlParams(BREAKDOWN_URL_SPEC);
  const q = useProblemSourceBreakdown(bdw);
  const d = q.data;
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const cols = useMemo(() => famCols(lang), [lang]);
  const toggle = (f: string) => setExpanded(prev => {
    const n = new Set(prev);
    if (n.has(f)) n.delete(f); else n.add(f);
    return n;
  });

  const state: DataTableStateInput<ProblemSourceFamily> = q.isError
    ? { kind: 'error', message: tl('inbox.bd.error', lang) }
    : q.isLoading ? { kind: 'loading' }
    : { kind: 'empty', message: tl('inbox.bd.empty', lang) };

  const renderCell = (r: ProblemSourceFamily, c: ColumnDef<ProblemSourceFamily>) => {
    switch (c.id) {
      case 'family': {
        const isOpen = expanded.has(r.family);
        const Icon = isOpen ? ChevronDown : ChevronRight;
        return (
          <span className="inline-flex items-center gap-1 min-w-0">
            {r.metrics.length > 0 && (
              <Button variant="ghost" size="icon" className="size-5 shrink-0" aria-expanded={isOpen}
                aria-label={tf('inbox.bd.expand', lang, { f: familyLabel(r.family, lang) })}
                onClick={() => toggle(r.family)}>
                <Icon className="size-3.5" aria-hidden="true" />
              </Button>
            )}
            <span className="dt-trunc" title={r.family}>{familyLabel(r.family, lang)}</span>
          </span>
        );
      }
      case 'count': return r.count.toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US');
      case 'share': return pctText(r.sharePct, lang);
      case 'services': return r.services;
      case 'open': return r.openNow;
      case 'sev': return `${r.critical} / ${r.warning} / ${r.otherSev}`;
      case 'median': return fmtDurShort(r.medianSec);
      case 'short': return `${r.short15} (${pctText(r.short15Pct, lang)})`;
      case 'long': return r.long1h;
      case 'churn': return (
        <span title={tf('inbox.bd.churnTitle', lang, { p: r.churnPairs, n: r.churnCount })}>{r.churnCount}</span>
      );
      case 'top': return (
        <span className="dt-trunc">
          {r.topServices.map((s, i) => (
            <span key={s.service}>
              {i > 0 && ', '}
              <Link to={serviceHrefInList(s.service)}>{s.service}</Link> {s.count}
            </span>
          ))}
        </span>
      );
    }
    return null;
  };

  const seriesLabel = d && d.bucketSec < 86400 ? tl('inbox.bd.perBucket.hour', lang) : tl('inbox.bd.perBucket.day', lang);

  return (
    <div className="flex flex-col gap-3" data-testid="problem-source-breakdown">
      <div className="flex flex-wrap items-center gap-3">
        <SegmentedControl<ProblemSourceWindow> size="sm" aria-label={tl('inbox.bd.window', lang)} value={bdw}
          onChange={v => setUrl({ bdw: v })}
          options={BREAKDOWN_WINDOWS.map(w => ({ value: w, label: tl(`inbox.bd.w.${w}`, lang) }))} />
        <Button variant="outline" size="sm" onClick={q.refresh} disabled={q.isFetching}>{tl('inbox.bd.refresh', lang)}</Button>
      </div>
      {d && (
        <div className="flex flex-wrap items-center gap-4">
          <strong data-testid="bd-total">{tf('inbox.bd.total', lang, { n: d.total, open: d.openNow })}</strong>
          <span className="inline-flex items-center gap-2 text-[color:var(--text2)]">
            {seriesLabel}
            <Sparkline values={d.series.map(p => p.n)} width={220} height={28} mode="count" title={seriesLabel} />
          </span>
        </div>
      )}
      {d?.partial && <p className="text-[color:var(--warn)]">{tl('inbox.bd.partial', lang)}</p>}
      {/* v0.10.2479 — .table-wrap: DataTableColgroup sığdırması (öncelik-3 sütunları
          gizleme) yalnız bu kabı ölçer; çıplak tablo dar çekmecede taşıyordu. */}
      <div className="table-wrap">
        <CmDataTable<ProblemSourceFamily>
          storageKey="problem-source-breakdown"
          ariaLabel={tl('inbox.bd.title', lang)}
          columns={cols}
          rows={d?.families ?? []}
          sort={{ initial: { id: 'count', dir: 'desc' } }}
          state={state}
          getRowKey={r => r.family}
          renderCell={renderCell}
          detail={{
            isOpen: r => expanded.has(r.family),
            cellClassName: () => 'row-detail',
            render: r => (
              <ul className="flex flex-col gap-0.5" aria-label={tf('inbox.bd.expand', lang, { f: familyLabel(r.family, lang) })}>
                {r.metrics.map(m => (
                  <li key={m.metric}>
                    <code>{m.metric}</code> — {m.count} ({pctText(m.sharePct, lang)}) · {tl('inbox.bd.col.median', lang)} {fmtDurShort(m.medianSec)}
                    {' · '}{tl('inbox.bd.col.short', lang)} {m.short15} ({pctText(m.short15Pct, lang)})
                    {' · '}{tl('inbox.bd.col.long', lang)} {m.long1h}
                  </li>
                ))}
              </ul>
            ),
          }}
        />
      </div>
      <p className="text-[color:var(--text2)] text-xs">{tl('inbox.bd.sevNote', lang)} {tl('inbox.bd.durNote', lang)}</p>
    </div>
  );
}
