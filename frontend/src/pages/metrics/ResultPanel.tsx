// pages/metrics/ResultPanel — v0.10.1863 (Metrics Explorer Faz 2 dilim 2.3;
// mockup görünüm 1 `.card.ch`).
//
// Sonuç kartı: görsel (Çizgi · Alan · Yığın · Isı haritası · Tablo · Tek
// değer) + adım (Oto · 15 sn · 1 dk · 5 dk · 1 sa) + dürüstlük satırı;
// kırpma bildirimi ("12 seriden 8'i gösteriliyor" + Tümünü göster / Sınırı
// değiştir); grafik CorePanel (uPlot, tembel); formüller AYRI panelde (grafik
// katmanında ikinci eksen yok, birim farklı). Yükleniyor: grafik alanında
// durum, önceki veri soluk kalır; hata: tek cümle + "yeniden dene", 400
// (sorgu) ile 502 (kaynak) ayrı başlık.
import { lazy, Suspense, useMemo, useState } from 'react';
import { RotateCw } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger } from '@/components/coremetry/overlay';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { MetricResultNotice } from '@/components/coremetry/metrics';
import { Empty, Spinner } from '@/components/Spinner';
import { LatencyHeatmap } from '@/components/LatencyHeatmap';
import { otlpUnitToGrafana } from '@/lib/chart/metricUnit';
import { fmtSmart } from '@/lib/chartFmt';
import { trPossessive } from '@/lib/metricMeta';
import { MX_LIMITS, MX_STEPS, type MxLimit, type MxModel, type MxViz } from '@/lib/metricsExplorer/model';
import { runErrorTitle } from '@/lib/metricsExplorer/run';
import { rankText, resultRows, statusLine, truncationOf, type MxSeriesRow } from '@/lib/metricsExplorer/result';
import type { LatencyHeatmap as Heatmap } from '@/lib/types';
import type { RunResult } from './useMetricsRun';

// CorePanel @grafana/data'ya bağlı: statik bağlamak vendor'ı şişirir (corePanelEntry notu).
const CorePanelMulti = lazy(() => import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

const VIZ_OPTS: { value: MxViz; label: string }[] = [
  { value: 'line', label: 'Çizgi' }, { value: 'area', label: 'Alan' }, { value: 'stacked', label: 'Yığın' },
  { value: 'heatmap', label: 'Isı haritası' }, { value: 'table', label: 'Tablo' }, { value: 'stat', label: 'Tek değer' },
];
const STEP_LABEL: Record<number, string> = { 0: 'Oto', 15: '15 sn', 60: '1 dk', 300: '5 dk', 3600: '1 sa' };

/** Grafana birim kimliği (CorePanel) — ham UCUM ya da formülün seçtiği kimlik. */
function chartUnit(raw: string | undefined): string | undefined {
  if (!raw) return undefined;
  return otlpUnitToGrafana(raw) ?? raw;
}
function fmtUnit(u: string | undefined): string | undefined {
  if (u === 'percent') return '%';
  return u;
}

export interface HeatmapState {
  data?: Heatmap | null;
  loading: boolean;
  error?: string | null;
  note?: string | null;
  /** Isı haritası çizilemez: nedeni + (varsa) kova kardeşine geç eylemi. */
  blocked?: { reason: string; sibling?: string };
}

export interface ResultPanelProps {
  model: MxModel;
  results: RunResult[];
  stepSec: number;
  onModel(p: Partial<MxModel>): void;
  heatmap: HeatmapState;
  onSwitchToBucket?(name: string): void;
  /** Satır yokken (sorgu kurulmadı) gösterilecek yönlendirme. */
  emptyHint: string;
  /** Metin dışı kaynak adı (Thanos). */
  sourceOverride?: string;
  unitFor(id: string): string | undefined;
  /** Kod kipinde "Sorgu izi" (VM trace=1): desteklenmiyorsa nedeni; undefined = düğme yok. */
  traceReason?: string;
}

export function ResultPanel({ model, results, stepSec, onModel, heatmap, onSwitchToBucket, emptyHint, sourceOverride, unitFor, traceReason }: ResultPanelProps) {
  const [limitOpen, setLimitOpen] = useState(false);
  const main = results.filter(r => r.item.kind !== 'formula');
  const formulas = results.filter(r => r.item.kind === 'formula');
  const rows = useMemo(() => resultRows(main.map(r => ({
    id: r.item.id, legend: r.item.legend, by: r.item.by, series: r.series, rawKeys: !!r.sourceLabel,
  }))), [main]);
  const metas = results.map(r => r.meta);
  const tr = truncationOf(metas);
  const loading = results.some(r => r.loading);
  const fetching = results.some(r => r.fetching);
  const errors = results.filter(r => r.error);
  const status = results.length
    ? statusLine({ metas, seriesCount: rows.length, stepSec, auto: model.step === 0, sourceOverride: sourceOverride ?? results[0]?.sourceLabel })
    : '';
  const mainUnit = main.length === 1 ? chartUnit(unitFor(main[0].item.id)) : undefined;
  const nextLimit = (MX_LIMITS.find(l => tr?.total != null && l >= tr.total) ?? MX_LIMITS[MX_LIMITS.length - 1]) as MxLimit;

  return (
    <section aria-label="Sonuç" className="rounded-lg border bg-card">
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
        <SegmentedControl<MxViz> size="sm" aria-label="Görselleştirme" value={model.viz} onChange={viz => onModel({ viz })} options={VIZ_OPTS} />
        <span className="text-xs text-muted-foreground">Adım</span>
        <SegmentedControl<string> size="sm" aria-label="Adım" value={String(model.step)} onChange={v => onModel({ step: Number(v) })}
          options={MX_STEPS.map(s => ({ value: String(s), label: STEP_LABEL[s] }))} />
        <span className="flex-1" />
        {fetching && <Spinner />}
        {status && <span role="status" className="font-mono text-xs text-muted-foreground tabular-nums">{status}</span>}
        {traceReason !== undefined && (
          <Button type="button" variant="ghost" size="xs" disabled title={traceReason}>Sorgu izi</Button>
        )}
      </div>

      {tr && (
        <div role="status" className="flex flex-wrap items-center gap-2 border-b border-status-warn-border bg-status-warn-bg px-3 py-1.5 text-xs">
          <b>{tr.total != null ? `${tr.total} seriden ${trPossessive(tr.shown)} gösteriliyor.` : `İlk ${tr.shown} seri gösteriliyor; toplam bilinmiyor.`}</b>
          {tr.limit && <span>{rankText(tr.rank, tr.limit)}.</span>}
          <span className="flex-1" />
          {tr.total != null && tr.total <= 50 && model.limit < nextLimit && (
            <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => onModel({ limit: nextLimit })}>
              Tümünü göster ({tr.total})
            </Button>
          )}
          <DropdownMenu open={limitOpen} onOpenChange={setLimitOpen}>
            <DropdownMenuTrigger asChild>
              <Button type="button" variant="link" size="xs" className="h-auto px-0">Sınırı değiştir</Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuRadioGroup value={String(model.limit)} onValueChange={v => onModel({ limit: Number(v) as MxLimit })}>
                {MX_LIMITS.map(l => <DropdownMenuRadioItem key={l} value={String(l)}>{l} seri</DropdownMenuRadioItem>)}
              </DropdownMenuRadioGroup>
              <DropdownMenuRadioGroup value={model.rank} onValueChange={v => onModel({ rank: v as MxModel['rank'] })}>
                <DropdownMenuRadioItem value="avg">Ortalamaya göre</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="max">En yüksek değere göre</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="last">Son değere göre</DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      )}

      <div className="flex flex-col gap-2 p-3">
        {results.map(r => (r.meta && (r.meta.partial || r.meta.note))
          ? <MetricResultNotice key={`n-${r.item.id}`} meta={{ ...r.meta, truncated: false }} prefix={results.length > 1 ? r.item.id : undefined} className="mb-0" />
          : null)}
        {errors.map(r => (
          <div key={`e-${r.item.id}`} role="alert" className="flex flex-wrap items-center gap-2 rounded-md border border-status-critical-border bg-status-critical-bg px-3 py-2 text-sm">
            <b className="text-status-critical">{results.length > 1 ? `${r.item.id}: ` : ''}{runErrorTitle(r.error!, sourceOverride ?? 'VictoriaMetrics')}</b>
            <span className="min-w-0 flex-1 break-words">{r.error!.message}</span>
            <Button type="button" variant="outline" size="xs" onClick={r.refetch}><RotateCw aria-hidden="true" />Yeniden dene</Button>
          </div>
        ))}

        {results.length === 0 ? (
          <Empty compact icon={null} title="Sorgu yok">{emptyHint}</Empty>
        ) : model.viz === 'heatmap' ? (
          <HeatmapBody state={heatmap} onSwitch={onSwitchToBucket} />
        ) : model.viz === 'table' ? (
          <SeriesTable rows={rows} unit={fmtUnit(mainUnit)} loading={loading} />
        ) : model.viz === 'stat' ? (
          <StatGrid rows={rows} unit={fmtUnit(mainUnit)} loading={loading} />
        ) : (
          <Suspense fallback={<Spinner />}>
            <div className={fetching && !loading ? 'opacity-60 transition-opacity' : undefined}>
              <CorePanelMulti title="Sonuç" height={240} storageKey="metrics-explorer:main" legendDefaultOpen
                viz={model.viz === 'stacked' ? 'stacked' : model.viz === 'area' ? 'area' : 'line'}
                loading={loading && rows.length === 0}
                emptyReason={!loading && errors.length === 0 && rows.length === 0 ? 'Bu aralıkta seri yok' : undefined}
                emptyHint="Aralığı genişletin ya da süzgeçleri gevşetin"
                unit={mainUnit}
                items={rows.map(r => ({ series: [r.series], name: r.legend }))} />
            </div>
          </Suspense>
        )}

        {model.viz !== 'heatmap' && formulas.map(f => {
          const frows = resultRows([{ id: f.item.id, legend: f.item.legend, by: f.item.by, series: f.series }]);
          const u = chartUnit(unitFor(f.item.id));
          return (
            <div key={f.item.id} className="flex flex-col gap-1">
              <p className="text-xs"><span className="font-mono font-semibold">ƒ {f.item.id}</span> <span className="text-muted-foreground">{f.item.refs?.join(', ')} üzerinden · ayrı panel (birim farklı olabilir)</span></p>
              {model.viz === 'table' ? <SeriesTable rows={frows} unit={fmtUnit(u)} loading={f.loading} />
                : model.viz === 'stat' ? <StatGrid rows={frows} unit={fmtUnit(u)} loading={f.loading} />
                  : (
                    <Suspense fallback={<Spinner />}>
                      <CorePanelMulti title={`Formül ${f.item.id}`} height={160} storageKey={`metrics-explorer:formula:${f.item.id}`}
                        loading={f.loading && frows.length === 0} unit={u}
                        emptyReason={!f.loading && !f.error && frows.length === 0 ? 'Formül seri üretmedi' : undefined}
                        emptyHint="Sorguların etiketleri eşleşmeli (aynı by)"
                        items={frows.map(r => ({ series: [r.series], name: r.legend, dashed: true }))} />
                    </Suspense>
                  )}
            </div>
          );
        })}
      </div>
    </section>
  );
}

function HeatmapBody({ state, onSwitch }: { state: HeatmapState; onSwitch?(name: string): void }) {
  if (state.blocked) {
    return (
      <Empty compact icon={null} title="Isı haritası histogram ister."
        action={state.blocked.sibling && onSwitch
          ? <Button type="button" variant="outline" size="sm" onClick={() => onSwitch(state.blocked!.sibling!)}>{state.blocked.sibling} metriğine geç</Button>
          : undefined}>
        {state.blocked.reason}
      </Empty>
    );
  }
  if (state.loading && !state.data) return <Spinner label="Isı haritası yükleniyor" />;
  if (state.error) return <Empty compact icon={null} title="Isı haritası okunamadı">{state.error}</Empty>;
  if (!state.data) return <Empty compact icon={null} title="Bu aralıkta kova verisi yok">{state.note || 'Aralığı genişletin ya da süzgeçleri gevşetin.'}</Empty>;
  return (
    <div className="flex flex-col gap-1">
      <LatencyHeatmap data={state.data} />
      {state.note && <p className="text-xs text-muted-foreground">{state.note}</p>}
    </div>
  );
}

const TABLE_COLS: ColumnDef<MxSeriesRow>[] = [
  { id: 'legend', label: 'Seri', width: 360, minWidth: 160, flex: true, priority: 1, sortValue: r => r.legend },
  { id: 'last', label: 'Son', width: 110, minWidth: 80, numeric: true, priority: 1, sortValue: r => r.last ?? -Infinity },
  { id: 'min', label: 'Min', width: 110, minWidth: 80, numeric: true, priority: 2, sortValue: r => r.min ?? -Infinity },
  { id: 'max', label: 'Maks', width: 110, minWidth: 80, numeric: true, priority: 2, sortValue: r => r.max ?? -Infinity },
  { id: 'avg', label: 'Ort', width: 110, minWidth: 80, numeric: true, priority: 2, sortValue: r => r.avg ?? -Infinity },
];

function SeriesTable({ rows, unit, loading }: { rows: MxSeriesRow[]; unit?: string; loading: boolean }) {
  return (
    <DataTable<MxSeriesRow> storageKey="metrics-explorer-series" columns={TABLE_COLS} rows={rows} ariaLabel="Seriler"
      getRowKey={r => r.key}
      sort={{ initial: { id: 'avg', dir: 'desc' } }}
      state={loading ? { kind: 'loading' } : { kind: 'empty', message: 'Bu aralıkta seri yok' }}
      renderCell={(r, c) => (c.id === 'legend'
        ? <span className="font-mono" title={r.legend}>{r.legend}</span>
        : fmtSmart(r[c.id as 'last' | 'min' | 'max' | 'avg'], unit))} />
  );
}

function StatGrid({ rows, unit, loading }: { rows: MxSeriesRow[]; unit?: string; loading: boolean }) {
  if (loading && rows.length === 0) return <Spinner label="Yükleniyor" />;
  if (rows.length === 0) return <Empty compact icon={null} title="Bu aralıkta seri yok">Aralığı genişletin ya da süzgeçleri gevşetin.</Empty>;
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-2">
      {rows.map(r => (
        <div key={r.key} className="flex flex-col gap-1 rounded-md border p-3">
          <span className="truncate font-mono text-xs text-muted-foreground" title={r.legend}>{r.legend}</span>
          <span className="text-2xl font-semibold tabular-nums">{fmtSmart(r.last, unit)}</span>
          <span className="text-xs text-muted-foreground tabular-nums">son değer · ort {fmtSmart(r.avg, unit)}</span>
        </div>
      ))}
    </div>
  );
}
