import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { fmtClock } from '@/lib/utils'; // v0.10.891 — tek saat biçimleyici (24 sa kilidi)
import { useServiceDeploys } from '@/lib/queries';
import { TimeSeriesPanel, type TSSeries, type TSThreshold } from '@/components/viz/TimeSeriesPanel';
import { rowActivation } from '@/lib/a11y';
import { useDataTable, DataTableHead, DataTableColgroup, ResetLayoutButton } from '@/components/ui/DataTable';
import type { DataTableColumn } from '@/lib/dataTable';
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
// Bant, panelin hazır `thresholds` yatay çizgileriyle çizilir (odaklı pod
// için alt/üst sınır) — paylaşılan çizim kancasına dokunulmaz. Hata durumu
// tek satır (kart yok ≠ hata); ölmüş pod "sessiz · N dk önce" der.

export const HEAP_STATUS: Record<string, { icon: string; label: string; tone: string }> = {
  critical:    { icon: '●', label: 'kritik sapma', tone: 'b-err' },
  deviating:   { icon: '▲', label: 'sapıyor',      tone: 'b-warn' },
  ok:          { icon: '○', label: 'bant içinde',  tone: 'b-gray' }, // v0.10.929 (K5) — sağlıklı = nötr
  no_baseline: { icon: '◌', label: 'baseline yok', tone: 'b-gray' },
};

const LINES_MAX = 12; // grafikte çizilen pod sayısı (tablo hepsini listeler)

/** Bant cümlesi — SAF (pin testli): kaynağı ve örnek sayısını söyler, yoksa neden yok. */
export function heapBandSentence(p: HeapBaselinePod | undefined, d: Pick<HeapBaselineResponse, 'needBuckets' | 'bucketSec' | 'historyHours'>): string {
  if (!p) return '';
  if (p.band.status === 'no_baseline') {
    return `baseline yok (< ${Math.round(d.needBuckets * d.bucketSec / 60)} dk veri)`;
  }
  return `bant: ardışık ${d.historyHours}s · n=${p.band.n}`;
}

const fmtPct = (v: number) => `${v.toFixed(0)} %`;

const COLS: DataTableColumn<HeapBaselinePod>[] = [
  { id: 'pod',    label: 'Pod',        sortValue: r => r.pod,          naturalDir: 'asc', width: 240 },
  { id: 'last',   label: 'Son',        sortValue: r => r.band.current, numeric: true,     width: 80 },
  { id: 'band',   label: 'Bant (24s)', width: 120 },
  { id: 'z',      label: 'z',          sortValue: r => r.band.status === 'no_baseline' ? -99 : r.band.z, numeric: true, width: 70 },
  { id: 'status', label: 'Durum',      sortValue: r => ({ critical: 3, deviating: 2, ok: 1, no_baseline: 0 }[r.band.status] ?? 0), numeric: true, width: 200 },
];

export function HeapBaselineCard({ service, from, to, onZoom, onZoomReset }: {
  service: string;
  from: number; // unix ns — parent'ın çözülmüş penceresi (RQ anahtarı hizalı)
  to: number;
  onZoom?: (fromSec: number, toSec: number) => void;
  onZoomReset?: () => void;
}) {
  const q = useQuery({
    queryKey: ['svc-heap-baseline', service, from, to],
    queryFn: () => api.serviceHeapBaseline(service, from, to),
    enabled: !!service,
    staleTime: 60_000,
  });
  const deploysQ = useServiceDeploys(service, from, to);
  const deployNs = useMemo(() => {
    const d = deploysQ.data;
    return d && d.length ? d.map(x => x.timeUnixNs) : undefined;
  }, [deploysQ.data]);
  const [focus, setFocus] = useState<string | null>(null);
  const data = q.data ?? undefined;
  const pods = useMemo(() => data?.pods ?? [], [data]);
  const focused = pods.find(p => p.pod === focus) ?? pods.find(p => p.pod === data?.worst) ?? pods[0];

  const series = useMemo<TSSeries[]>(() => {
    // Odaklı pod HER ZAMAN çizilir (bant neyin bandı belli olsun), kalan LINES_MAX-1.
    const drawn = focused ? [focused, ...pods.filter(p => p !== focused)].slice(0, LINES_MAX) : pods.slice(0, LINES_MAX);
    return drawn.map(p => ({ label: p.pod, unit: '%', points: p.series.map(pt => ({ time: pt.t, value: pt.v })) }));
  }, [pods, focused]);
  const thresholds = useMemo<TSThreshold[] | undefined>(() => {
    if (!focused || focused.band.status === 'no_baseline') return undefined;
    return [
      { value: focused.band.upper, label: `bant üst · ${focused.pod}`, color: 'var(--text3)' },
      { value: focused.band.lower, label: 'bant alt', color: 'var(--text3)' },
    ];
  }, [focused]);

  // Hook koşulsuz, boş-küme erken dönüşünün ÜSTÜNDE (useDataTable kuralı).
  const dt = useDataTable<HeapBaselinePod>({
    storageKey: 'service-heap-baseline',
    columns: COLS,
    rows: pods,
    initialSort: { id: 'status', dir: 'desc' },
  });

  if (q.isError) { // hata ≠ boş küme: sessizce kaybolmaz (ship checklist #8)
    return <div className="kc-line" data-testid="heap-baseline-error">heap bandı okunamadı: {q.error instanceof Error ? q.error.message : String(q.error)}</div>;
  }
  if (!data || pods.length === 0) return null; // metrik akmıyor / JVM pod yok → kart yok
  const silentAfter = 3 * data.bucketSec; // son kova bundan eskiyse "sessiz"
  const silentFor = (p: HeapBaselinePod) => (p.lastTs > 0 ? Math.max(0, data.to / 1e9 - p.lastTs - data.bucketSec) : 0);

  const sourceName = data.source === 'vm' ? 'VictoriaMetrics' : 'ClickHouse';
  const bandTxt = heapBandSentence(focused, data);
  return (
    <div className="card" data-testid="heap-baseline-card">
      <div className="ov-card-h">
        <h3>Heap after GC — % of limit (by pod)</h3>
        <span style={{ fontSize: 11, color: 'var(--text3)', display: 'flex', gap: 6, alignItems: 'center' }}>
          <span className="badge b-gray" title={`kaynak: ${sourceName}`}>{data.source}</span>
          {focused && <span className="badge b-gray" title={`odaklı pod: ${focused.pod}`}>{bandTxt}</span>}
          {pods.length > LINES_MAX && <span>{LINES_MAX} / {pods.length} pod çizildi</span>}
          {data.truncated > 0 && <span>· {data.truncated} pod kesildi</span>}
          {data.capped && <span className="badge b-warn" title="Kaynak satır tavanına çarpıldı (ClickHouse LIMIT); bazı pod'lar eksik ya da kısmi olabilir.">kaynak satır tavanı</span>}
          {/* v0.10.891 — dedektörün gölge hükmü: "açılırdı" (Problem yazılmadı). Canlı kip 892. */}
          {data.mode !== 'off' && data.verdict?.wouldOpen && (
            <span className="badge b-warn" title={`dedektör (${data.verdict.source}, kip ${data.verdict.mode}): pod ${data.verdict.pod} · z ${data.verdict.z.toFixed(1)} · ${fmtPct(data.verdict.current)} vs medyan ${fmtPct(data.verdict.median)} · ${fmtClock(data.verdict.at * 1000)}${data.verdict.wouldP1 ? ' · P1 olurdu' : ' · P2 olurdu'}`}>
              ◐ gölge: problem AÇILIRDI · {data.verdict.pod}{data.verdict.wouldP1 ? ' · P1' : ''}
            </span>
          )}
          {/* v0.10.903 — sürüklenmiş kolon genişliğinin çıkış yolu (resetLayoutAdoption muhasebesi). */}
          <ResetLayoutButton dt={dt} />
        </span>
      </div>
      <div className="ov-card-b" style={{ paddingTop: 10, paddingBottom: 10 }}>
        <TimeSeriesPanel
          series={series}
          thresholds={thresholds}
          height={220}
          mode="line"
          syncKey={`runtime:${service}`}
          deploys={deployNs}
          onZoom={onZoom}
          onZoomReset={onZoomReset}
        />
        {focused && (
          <div className="kc-line" style={{ marginTop: 6 }}>
            en kötü pod: <span className="mono">{data.worst || '—'}</span>
            {' · '}odak: <span className="mono">{focused.pod}</span> · {fmtPct(focused.band.current)}
            {focused.band.status !== 'no_baseline' && <> · z {focused.band.z.toFixed(1)} · bant {fmtPct(focused.band.lower)}–{fmtPct(focused.band.upper)}</>}
          </div>
        )}
        <div className="table-wrap is-fit" style={{ marginTop: 8 }}>
          <table style={{ tableLayout: 'fixed', width: '100%' }}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.map(p => {
                const st = HEAP_STATUS[p.band.status] ?? HEAP_STATUS.no_baseline;
                const noBand = p.band.status === 'no_baseline';
                /* v0.10.933 (tablo standardı T2) — odaklı pod satırı satır içi
                   bg3 zemin yerine tek seçili görünüm `.row-selected`. */
                return (
                  <tr key={p.pod} {...rowActivation(() => setFocus(p.pod))} title="Bandı bu poda odakla"
                      className={focused?.pod === p.pod ? 'row-selected' : undefined}>
                    <td className="mono" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.pod}</td>
                    <td className="num mono">{fmtPct(p.band.current)}</td>
                    <td className="num mono">{noBand ? '—' : `${fmtPct(p.band.lower)}–${fmtPct(p.band.upper)}`}</td>
                    <td className="num mono">{noBand ? '—' : p.band.z.toFixed(1)}</td>
                    <td>
                      <span className={`badge ${st.tone}`}>{st.icon} {st.label}</span>
                      {!noBand && p.band.status !== 'ok' && <span style={{ color: 'var(--text3)' }}> · {p.band.dwell} kova</span>}
                      {noBand && <span style={{ color: 'var(--text3)' }}> · {heapBandSentence(p, data)}</span>}
                      {silentFor(p) > silentAfter && <span style={{ color: 'var(--text3)' }}> · sessiz · {Math.round(silentFor(p) / 60)} dk önce</span>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <div className="kc-line" style={{ marginTop: 6 }}>
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
