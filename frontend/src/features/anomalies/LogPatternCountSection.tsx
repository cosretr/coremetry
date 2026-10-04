import { lazy, Suspense, useMemo } from 'react';
import { Spinner } from '@/components/Spinner';
import { useLogPatternSeries } from '@/lib/queries';
import { Sect } from './detailSections';
import {
  LOG_PATTERN_SERIES_TEXT, anomalyRegion, bucketLabel, logPatternSeriesArgs, logPatternSeriesState,
  logPatternSeriesToSpan, verifiedRatioNote, type LogPatternChartEvent,
} from './logPatternSeries';

// LogPatternCountSection — log deseni anomalisinin "Desen sayısı" bar grafiği
// (v0.10.1060). Operatör (prod): "Bunu doğru yakalamış ama artışın ne zaman
// başladığını göstermiyor. Elastic'e gidip bakınca barlardan net görüyorum."
//
// Kova başına eşleşme sayısı (GET /api/anomalies/log-pattern-series —
// dedektörün kendi yüklemi, CH ya da ES), olay başlangıcı işaretli.
// Ürünün standart grafiği (CorePanelMulti, uPlot, 'bars'); tooltip zaman +
// sayı; olay penceresi standart bölge katmanıyla (sol kenar = başlangıç). Açıklama paragrafı YOK (operatör: "çok detay verince daha anlaşılır
// olmuyor"); yalnız kısa başlık + kova genişliği.
//
// Sorgu yalnız bu bölüm mount'luyken (sayfa açık), aktif olayda 60 s yoklama
// (gizli sekmede durur). Hata yalnız bu panelin içinde kalır — sayfanın geri
// kalanı etkilenmez.
//
// v0.10.1106 — prop yalnız okunan beş alan (LogPatternChartEvent): terfi
// Problem'inin detayı aynı bileşeni kaynak olayın özetiyle çizer
// (promotedPatternChartEvent); sözleşme (kova ≤ 120, ≤ 7 gün, 60 s önbellek,
// yalnız aktifte 60 s yoklama) aynen.
const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export function LogPatternCountSection({ event }: { event: LogPatternChartEvent }) {
  // v0.10.1062 — argümanlar ortak kurucudan: "Ne yapabilirim" kartı AYNI
  // anahtarla okur (ikinci istek yok).
  const args = useMemo(
    () => logPatternSeriesArgs({ pattern: event.pattern, startedAt: event.startedAt, lastSeen: event.lastSeen, status: event.status }),
    [event.pattern, event.startedAt, event.lastSeen, event.status]);
  const q = useLogPatternSeries(args, { live: event.status === 'active' });
  const data = q.data;
  const state = logPatternSeriesState({ isPending: q.isPending, isError: q.isError, data });
  const series = useMemo(() => (data ? logPatternSeriesToSpan(data) : []), [data]);
  const xRange = useMemo(() => (data ? { from: data.from / 1e9, to: data.to / 1e9 } : null), [data]);
  const regions = useMemo(
    () => (data ? [anomalyRegion({ startedAt: event.startedAt, lastSeen: event.lastSeen, status: event.status }, data.to)] : []),
    [data, event.startedAt, event.lastSeen, event.status]);
  // v0.10.1080 — ES'te örneklem doğrulaması < 1 ise kısa not (CH'de hiç yok).
  const note = verifiedRatioNote(event.verifiedRatio);
  const sub = (data ? `${bucketLabel(data.bucketSec)} · tüm servisler` : 'tüm servisler') + (note ? ` · ${note}` : '');

  return (
    <Sect title="Desen sayısı" sub={sub}>
      <Suspense fallback={<Spinner />}>
        <CorePanelMultiLazy
          title=""
          ariaLabel="Desen sayısı"
          storageKey="anomaly-log-pattern-count"
          height={180}
          viz="bars"
          zeroBase
          hideLegend
          loading={state === 'loading'}
          error={state === 'error' ? LOG_PATTERN_SERIES_TEXT.error : undefined}
          emptyReason={state === 'empty' ? LOG_PATTERN_SERIES_TEXT.empty
            : state === 'gone' ? LOG_PATTERN_SERIES_TEXT.gone : undefined}
          xRange={xRange}
          regions={regions}
          note={data?.partial ? LOG_PATTERN_SERIES_TEXT.partial : null}
          items={[{ name: 'eşleşen log', role: 'data', series }]} />
      </Suspense>
    </Sect>
  );
}
