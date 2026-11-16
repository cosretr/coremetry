import { lazy, Suspense, useMemo } from 'react';
import { Spinner } from '@/components/Spinner';
import { useLogPatternSeries } from '@/lib/queries';
import { Sect } from './detailSections';
import {
  logPatternSeriesText, anomalyRegion, bucketLabel, logPatternSeriesArgs, logPatternSeriesState,
  logPatternSeriesToSpan, verifiedRatioNote, type LogPatternChartEvent,
} from './logPatternSeries';
import { t, useLang } from '@/lib/i18n'; // v0.10.2290 — bölüm metni arayüz dilinde

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
  const lang = useLang();
  const txt = logPatternSeriesText(lang);
  const data = q.data;
  const state = logPatternSeriesState({ isPending: q.isPending, isError: q.isError, data });
  const series = useMemo(() => (data ? logPatternSeriesToSpan(data) : []), [data]);
  const xRange = useMemo(() => (data ? { from: data.from / 1e9, to: data.to / 1e9 } : null), [data]);
  const regions = useMemo(
    () => (data ? [anomalyRegion({ startedAt: event.startedAt, lastSeen: event.lastSeen, status: event.status }, data.to, lang)] : []),
    [data, event.startedAt, event.lastSeen, event.status, lang]);
  // v0.10.1080 — ES'te örneklem doğrulaması < 1 ise kısa not (CH'de hiç yok).
  const note = verifiedRatioNote(event.verifiedRatio, lang);
  const all = t('aed.lp.allSvc', lang);
  const sub = (data ? `${bucketLabel(data.bucketSec, lang)} · ${all}` : all) + (note ? ` · ${note}` : '');

  return (
    <Sect title={t('aed.lp.title', lang)} sub={sub}>
      <Suspense fallback={<Spinner />}>
        <CorePanelMultiLazy
          title=""
          ariaLabel={t('aed.lp.title', lang)}
          storageKey="anomaly-log-pattern-count"
          height={180}
          viz="bars"
          zeroBase
          hideLegend
          loading={state === 'loading'}
          error={state === 'error' ? txt.error : undefined}
          emptyReason={state === 'empty' ? txt.empty
            : state === 'gone' ? txt.gone : undefined}
          xRange={xRange}
          regions={regions}
          note={data?.partial ? txt.partial : null}
          items={[{ name: t('aed.lp.series', lang), role: 'data', series }]} />
      </Suspense>
    </Sect>
  );
}
