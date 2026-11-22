import { lazy, Suspense, useMemo, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { fmtClock } from '@/lib/utils'; // v0.10.891 — tek saat biçimleyici (24 sa kilidi)
import { useServiceDeploys } from '@/lib/queries';
import { Spinner } from '@/components/Spinner';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import type { ChartTimeRegion } from '@/lib/chart/overlays';
import type { CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import type { SpanMetricSeries } from '@/lib/types';
import { rowActivation } from '@/lib/a11y';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { parseRtView, rtViewParam } from './runtimeHeapView';
import type { HeapBaselinePod, HeapBaselineResponse } from '@/lib/types';

// HeapBaselineCard — v0.10.887 (Dynatrace paritesi #4, dilim 1; spec Onay
// 2026-09-23). Pods sekmesi › Runtime, RuntimeCharts altı, yalnız JVM ailesi.
// Pod başına heap-after-GC (% limit) 5-dk kova serisi + 24 s ardışık
// pencereden bant (medyan ± 3.5·MAD/0.6745, anomaly.ComputeHeapBand).
// YALNIZ GÖRÜNÜRLÜK: problem açmaz, bildirim yok (dilim 2 ayrı Onay).
//
// Dürüstlük: pods boşsa kart HİÇ çizilmez (metrik akmıyor / JVM pod yok —
// boş küme kaybolur, sıfır göstermez); n < needBuckets → "baseline yok",
// bant çizilmez; başlık bandın kaynağını (24 s, n) ve deponun adını söyler.
// v0.10.2443 — grafik paylaşılan motorda (CorePanelMulti, lazy): odaklı pod'un
// bandı alt–üst kenar serileri arası DOLGU (CorePanel.fillBetween; eskiden
// TimeSeriesPanel'de iki yatay eşik çizgisiydi). Hata durumu tek satır (kart
// yok ≠ hata); ölmüş pod "sessiz · N dk önce" der.

export const HEAP_STATUS: Record<string, { icon: string; label: string; tone: string }> = {
  critical:    { icon: '●', label: 'kritik sapma', tone: 'b-err' },
  deviating:   { icon: '▲', label: 'sapıyor',      tone: 'b-warn' },
  ok:          { icon: '○', label: 'bant içinde',  tone: 'b-gray' }, // v0.10.929 (K5) — sağlıklı = nötr
  no_baseline: { icon: '◌', label: 'baseline yok', tone: 'b-gray' },
};

const LINES_MAX = 12; // grafikte çizilen pod sayısı (tablo hepsini listeler)

// CorePanel LAZY (RuntimePodChart emsali): @grafana/* Service sayfasının ilk
// chunk'ına girmez.
const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

const toSeries = (points: { time: number; value: number | null }[]): SpanMetricSeries =>
  ({ groupKey: [], points: points as SpanMetricSeries['points'] });

/** Pod çizgileri + odaklı pod'un bant kenarları (alt/üst, kesikli, arası dolu). SAF. */
export function heapChartItems(pods: readonly HeapBaselinePod[], focused: HeapBaselinePod | undefined, max: number = LINES_MAX): CorePanelMultiItem[] {
  // Odaklı pod HER ZAMAN çizilir (bant neyin bandı belli olsun), kalan max-1.
  const drawn = focused ? [focused, ...pods.filter(p => p !== focused)].slice(0, max) : pods.slice(0, max);
  const items: CorePanelMultiItem[] = drawn.map(p => ({
    name: p.pod, series: [toSeries(p.series.map(pt => ({ time: pt.t, value: pt.v })))],
  }));
  if (focused && focused.band.status !== 'no_baseline' && focused.series.length > 0) {
    const edge = (v: number) => toSeries(focused.series.map(pt => ({ time: pt.t, value: v })));
    items.push(
      { name: 'bant alt', series: [edge(focused.band.lower)], color: 'var(--text3)', dashed: true, band: 'lo' },
      { name: `bant üst · ${focused.pod}`, series: [edge(focused.band.upper)], color: 'var(--text3)', dashed: true, band: 'hi' },
    );
  }
  return items;
}
const HEAP_TITLE = 'Heap after GC — % of limit (by pod)'; // v0.10.954 — kart ve hata kartı aynı başlık

/** Bant cümlesi — SAF (pin testli): kaynağı ve örnek sayısını söyler, yoksa neden yok. */
export function heapBandSentence(p: HeapBaselinePod | undefined, d: Pick<HeapBaselineResponse, 'needBuckets' | 'bucketSec' | 'historyHours'>): string {
  if (!p) return '';
  if (p.band.status === 'no_baseline') {
    return `baseline yok (< ${Math.round(d.needBuckets * d.bucketSec / 60)} dk veri)`;
  }
  return `bant: ardışık ${d.historyHours}s · n=${p.band.n}`;
}

const fmtPct = (v: number) => `${v.toFixed(0)} %`;

// ── v0.10.2148 (servis sekmeleri S9; plan K12 — operatör kararı 2026-11-14) ──
// ÖZET görünüm (`?rtview=summary`) "sınıra en yakın 5 pod": grafik yok, yalnız GC sonrası
// heap doluluğu en yüksek beş pod (heap-baseline yanıtının kendi % sınır
// değeri; yeni sorgu yok). "Hangi pod" sorusunun cevabı burada; pod başına
// çizgi + bant grafiği ve TÜM pod'ların tablosu varsayılan görünümdür
// (v0.10.2446; `?rtview=summary` bu özeti açar, eski kart aşağıda aynen).
export const NEAR_LIMIT_MAX = 5;

/** Sınıra en yakın pod'lar: son GC-sonrası doluluğa göre azalan; eşitlikte ada göre (kararlı). SAF. */
export function nearestToLimit(pods: readonly HeapBaselinePod[], max: number = NEAR_LIMIT_MAX): HeapBaselinePod[] {
  return [...pods].sort((a, b) => b.band.current - a.band.current || a.pod.localeCompare(b.pod)).slice(0, max);
}

const NEAR_COLS: ColumnDef<HeapBaselinePod>[] = [
  { id: 'pod',    label: 'Pod',        sortValue: r => r.pod,          naturalDir: 'asc', flex: true, minWidth: 200 },
  { id: 'last',   label: 'Son (% sınır)', sortValue: r => r.band.current, numeric: true,  width: 120 },
  { id: 'band',   label: 'Bant (24s)', width: 120 },
  { id: 'status', label: 'Durum',      sortValue: r => ({ critical: 3, deviating: 2, ok: 1, no_baseline: 0 }[r.band.status] ?? 0), numeric: true, width: 220 },
];

type HeapCardProps = {
  service: string;
  from: number; // unix ns — parent'ın çözülmüş penceresi (RQ anahtarı hizalı)
  to: number;
  onZoom?: (fromSec: number, toSec: number) => void;
  onZoomReset?: () => void;
};

function useHeapBaseline(service: string, from: number, to: number) {
  return useQuery({
    queryKey: ['svc-heap-baseline', service, from, to],
    queryFn: () => api.serviceHeapBaseline(service, from, to),
    enabled: !!service,
    staleTime: 60_000,
  });
}

/** Dedektörün gölge hükmü: "açılırdı" (Problem yazılmadı). İki görünümde de görünür. */
function VerdictBadge({ data }: { data: HeapBaselineResponse }) {
  if (!(data.mode !== 'off' && data.verdict?.wouldOpen)) return null;
  return (
    <span className="badge b-warn" title={`dedektör (${data.verdict.source}, kip ${data.verdict.mode}): pod ${data.verdict.pod} · z ${data.verdict.z.toFixed(1)} · ${fmtPct(data.verdict.current)} vs medyan ${fmtPct(data.verdict.median)} · ${fmtClock(data.verdict.at * 1000)}${data.verdict.wouldP1 ? ' · P1 olurdu' : ' · P2 olurdu'}`}>
      ◐ gölge: problem AÇILIRDI · {data.verdict.pod}{data.verdict.wouldP1 ? ' · P1' : ''}
    </span>
  );
}

/** Durum hücresi: rozet + kaç kova / neden bant yok / ne zamandır sessiz. */
function StatusCell({ p, data }: { p: HeapBaselinePod; data: HeapBaselineResponse }) {
  const st = HEAP_STATUS[p.band.status] ?? HEAP_STATUS.no_baseline;
  const noBand = p.band.status === 'no_baseline';
  const silentAfter = 3 * data.bucketSec; // son kova bundan eskiyse "sessiz"
  const silentFor = p.lastTs > 0 ? Math.max(0, data.to / 1e9 - p.lastTs - data.bucketSec) : 0;
  return (
    <td>
      <span className={`badge ${st.tone}`}>{st.icon} {st.label}</span>
      {!noBand && p.band.status !== 'ok' && <span className="cell-faint"> · {p.band.dwell} kova</span>}
      {noBand && <span className="cell-faint"> · {heapBandSentence(p, data)}</span>}
      {silentFor > silentAfter && <span className="cell-faint"> · sessiz · {Math.round(silentFor / 60)} dk önce</span>}
    </td>
  );
}

function HeapNearLimit({ service, from, to }: HeapCardProps) {
  const q = useHeapBaseline(service, from, to);
  const [params] = useSearchParams();
  const data = q.data ?? undefined;
  const pods = useMemo(() => data?.pods ?? [], [data]);
  const rows = useMemo(() => nearestToLimit(pods), [pods]);
  // Hook koşulsuz, erken dönüşlerin ÜSTÜNDE (useDataTable kuralı).
  const dt = useDataTable<HeapBaselinePod>({
    storageKey: 'service-heap-near-limit', columns: NEAR_COLS, rows,
    initialSort: { id: 'last', dir: 'desc' },
  });
  const linesSearch = (() => {
    const p = new URLSearchParams(params);
    const v = rtViewParam({ ...parseRtView(params.get('rtview')), lines: true });
    if (v) p.set('rtview', v); else p.delete('rtview');
    const q = p.toString();
    return q ? `?${q}` : '';
  })();
  const table = (body: ReactNode) => (
    <div className="table-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>{body}</tbody>
      </table>
    </div>
  );
  const title = `Sınıra en yakın ${NEAR_LIMIT_MAX} pod — GC sonrası heap`;
  if (q.isError) { // hata ≠ boş küme: sessizce kaybolmaz
    return (
      <div className="card" data-testid="heap-baseline-error">
        <div className="ov-card-h"><h3>{title}</h3></div>
        <div className="ov-card-b heap-near-b">
          {table(<DataTableState dt={dt} kind="error"
            message={`heap bandı okunamadı: ${q.error instanceof Error ? q.error.message : String(q.error)}`} />)}
        </div>
      </div>
    );
  }
  if (!data || pods.length === 0) return null; // metrik akmıyor / JVM pod yok → kart yok
  const sourceName = data.source === 'vm' ? 'VictoriaMetrics' : 'ClickHouse';
  return (
    <div className="card" data-testid="heap-near-limit-card">
      <div className="ov-card-h">
        <h3>{title}</h3>
        <span className="rt-view-note">
          <span>{rows.length} / {pods.length} pod</span>
          {data.truncated > 0 && <span>· {data.truncated} pod kesildi</span>}
          {data.capped && <span className="badge b-warn" title="Kaynak satır tavanına çarpıldı (ClickHouse LIMIT); bazı pod'lar eksik ya da kısmi olabilir.">kaynak satır tavanı</span>}
          <VerdictBadge data={data} />
          <Link replace to={{ search: linesSearch }}>Tüm pod&#39;lar ve bant grafiği</Link>
        </span>
      </div>
      <div className="ov-card-b heap-near-b">
        {table(dt.sortedRows.map(p => (
          <tr key={p.pod}>
            <td className="mono">{p.pod}</td>
            <td className={`num ${p.band.current >= 90 ? 'cell-err' : p.band.current >= 75 ? 'cell-warn' : ''}`}>{fmtPct(p.band.current)}</td>
            <td className="num">{p.band.status === 'no_baseline' ? '—' : `${fmtPct(p.band.lower)}–${fmtPct(p.band.upper)}`}</td>
            <StatusCell p={p} data={data} />
          </tr>
        )))}
        <div className="kc-line heap-near-foot">
          GC sonrası heap doluluğu (jvm.memory.used_after_last_gc / jvm.memory.limit), {Math.round(data.bucketSec / 60)}-dk kova,
          bant son {data.historyHours} saatten; kaynak: {sourceName}. Bu kart problem açmaz.
        </div>
      </div>
    </div>
  );
}

/** Görünüm seçimi URL'den (`?rtview`): varsayılan tüm pod'lar (eski kart), `summary` özet. */
export function HeapBaselineCard(props: HeapCardProps) {
  const [params] = useSearchParams();
  return parseRtView(params.get('rtview')).lines ? <HeapBaselineLines {...props} /> : <HeapNearLimit {...props} />;
}

const COLS: ColumnDef<HeapBaselinePod>[] = [
  { id: 'pod',    label: 'Pod',        sortValue: r => r.pod,          naturalDir: 'asc', width: 240 },
  { id: 'last',   label: 'Son',        sortValue: r => r.band.current, numeric: true,     width: 80 },
  { id: 'band',   label: 'Bant (24s)', width: 120 },
  { id: 'z',      label: 'z',          sortValue: r => r.band.status === 'no_baseline' ? -99 : r.band.z, numeric: true, width: 70 },
  { id: 'status', label: 'Durum',      sortValue: r => ({ critical: 3, deviating: 2, ok: 1, no_baseline: 0 }[r.band.status] ?? 0), numeric: true, width: 200 },
];

function HeapBaselineLines({ service, from, to, onZoom, onZoomReset }: HeapCardProps) {
  const q = useHeapBaseline(service, from, to);
  const deploysQ = useServiceDeploys(service, from, to);
  const deployNs = useMemo(() => {
    const d = deploysQ.data;
    return d && d.length ? d.map(x => x.timeUnixNs) : undefined;
  }, [deploysQ.data]);
  const [focus, setFocus] = useState<string | null>(null);
  const data = q.data ?? undefined;
  const pods = useMemo(() => data?.pods ?? [], [data]);
  const focused = pods.find(p => p.pod === focus) ?? pods.find(p => p.pod === data?.worst) ?? pods[0];

  const items = useMemo(() => heapChartItems(pods, focused), [pods, focused]);
  const xRange = useMemo(() => ({ from: from / 1e9, to: to / 1e9 }), [from, to]);
  const regions = useMemo<ChartTimeRegion[] | undefined>(() => {
    if (!deployNs?.length) return undefined;
    const spanSec = Math.max(1, (to - from) / 1e9);
    return deployNs.map(ns => ({ fromSec: ns / 1e9, toSec: ns / 1e9 + spanSec * 0.004, color: 'var(--purple)', label: '▼ deploy' }));
  }, [deployNs, from, to]);

  // Hook koşulsuz, boş-küme erken dönüşünün ÜSTÜNDE (useDataTable kuralı).
  const dt = useDataTable<HeapBaselinePod>({
    storageKey: 'service-heap-baseline',
    columns: COLS,
    rows: pods,
    initialSort: { id: 'status', dir: 'desc' },
  });

  // v0.10.954 — tablo standardı T12: tablo (başlığıyla) TEK yerde; hata
  // kartı da aynı tabloyu çizer, durum satırı gövdede.
  const renderTable = (body: ReactNode) => (
    <div className="table-wrap heap-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>{body}</tbody>
      </table>
    </div>
  );

  if (q.isError) { // hata ≠ boş küme: sessizce kaybolmaz (ship checklist #8)
    // v0.10.954 — tablo standardı T12: hata artık kartın tablosunun İÇİNDE
    // (başlık durur); grafik / rozetler / dipnot cevaptan türediği için
    // hata kartında yok — eski tek satırla aynı bilgi, sunucu metni dahil.
    return (
      <div className="card" data-testid="heap-baseline-error">
        <div className="ov-card-h"><h3>{HEAP_TITLE}</h3></div>
        <div className="ov-card-b rt-card-b">
          {renderTable(
            <DataTableState dt={dt} kind="error"
              message={`heap bandı okunamadı: ${q.error instanceof Error ? q.error.message : String(q.error)}`} />,
          )}
        </div>
      </div>
    );
  }
  if (!data || pods.length === 0) return null; // metrik akmıyor / JVM pod yok → kart yok

  const sourceName = data.source === 'vm' ? 'VictoriaMetrics' : 'ClickHouse';
  const bandTxt = heapBandSentence(focused, data);
  return (
    <div className="card" data-testid="heap-baseline-card">
      <div className="ov-card-h">
        <h3>{HEAP_TITLE}</h3>
        <span className="heap-meta">
          <span className="badge b-gray" title={`kaynak: ${sourceName}`}>{data.source}</span>
          {focused && <span className="badge b-gray" title={`odaklı pod: ${focused.pod}`}>{bandTxt}</span>}
          {pods.length > LINES_MAX && <span>{LINES_MAX} / {pods.length} pod çizildi</span>}
          {data.truncated > 0 && <span>· {data.truncated} pod kesildi</span>}
          {data.capped && <span className="badge b-warn" title="Kaynak satır tavanına çarpıldı (ClickHouse LIMIT); bazı pod'lar eksik ya da kısmi olabilir.">kaynak satır tavanı</span>}
          {/* v0.10.891 — dedektörün gölge hükmü: "açılırdı" (Problem yazılmadı). Canlı kip 892. */}
          <VerdictBadge data={data} />
        </span>
      </div>
      <div className="ov-card-b rt-card-b">
        <Suspense fallback={<div className="rt-slot"><Spinner /></div>}>
          <CorePanelMultiLazy
            title=""
            ariaLabel={HEAP_TITLE}
            storageKey="service-heap-baseline-chart"
            height={220}
            unit="percent"
            viz="line"
            plain /* v0.10.2464: pod çizgileri dolgusuz; taban bandı (fillBetween) kalır */
            xRange={xRange}
            items={items}
            regions={regions}
            onZoom={onZoom}
            onZoomReset={onZoomReset}
            syncKey={msSyncKey(`runtime:${service}`)}
          />
        </Suspense>
        {focused && (
          <div className="kc-line heap-line">
            en kötü pod: <span className="mono">{data.worst || '—'}</span>
            {' · '}odak: <span className="mono">{focused.pod}</span> · {fmtPct(focused.band.current)}
            {focused.band.status !== 'no_baseline' && <> · z {focused.band.z.toFixed(1)} · bant {fmtPct(focused.band.lower)}–{fmtPct(focused.band.upper)}</>}
          </div>
        )}
        {renderTable(
          dt.sortedRows.map(p => {
            const noBand = p.band.status === 'no_baseline';
            /* v0.10.933 (tablo standardı T2) — odaklı pod satırı satır içi
               bg3 zemin yerine tek seçili görünüm `.row-selected`.
               v0.10.943 (T7) — "odakla" ipucu satırdan kimlik hücresine; odaklı
               satır AT'ye aria-current ile. */
            return (
              <tr key={p.pod} {...rowActivation(() => setFocus(p.pod))}
                  className={focused?.pod === p.pod ? 'row-selected' : undefined}
                  aria-current={focused?.pod === p.pod ? 'true' : undefined}>
                <td className="mono" title="Bandı bu poda odakla">{p.pod}</td>
                <td className="num">{fmtPct(p.band.current)}</td>
                <td className="num">{noBand ? '—' : `${fmtPct(p.band.lower)}–${fmtPct(p.band.upper)}`}</td>
                <td className="num">{noBand ? '—' : p.band.z.toFixed(1)}</td>
                <StatusCell p={p} data={data} />
              </tr>
            );
          }),
        )}
        <div className="kc-line heap-line">
          Bant = bu pod'un son {data.historyHours} saatteki GC-sonrası heap doluluğu
          (jvm.memory.used_after_last_gc / jvm.memory.limit), {Math.round(data.bucketSec / 60)}-dk kova, kaynak: {sourceName}.
          Bu kart problem açmaz.{' '}
          {data.mode === 'shadow' || data.mode === 'on'
            ? <>Problem kipi: <b>{data.mode === 'on' ? 'açık (bu sürümde gölge gibi)' : 'gölge'}</b> — dedektör hükmü rozette (Settings › Anomaly).</>
            : <>Problem kipi kapalı (Settings › Anomaly › JVM heap bandı).</>}
        </div>
      </div>
    </div>
  );
}
