import { lazy, Suspense, useMemo } from 'react';
import { Spinner } from '@/components/Spinner';
import { useAlertRuleSeries } from '@/lib/queries';
import type { Problem } from '@/lib/types';
import { Sect } from './detailSections';
import {
  ALERT_SERIES_TEXT, alertMetricUnit, alertSeriesArgs, alertSeriesState, alertSeriesToSpan, alertSeriesXRange,
  alertThreshold, isProblemLive, problemRegion, windowLabel,
} from './alertMetricSeries';

// AlertMetricChartSection — alarm problemi detayının "tetiklenen metrik"
// grafiği (v0.10.1064). Operatör (prod, "HTTP P99 latency >3s (sustained
// 10 min)"): "grafik olmadığı için de anlamak çok zor artışları".
//
// Kuralın kendi serisi (GET /api/alert-rules/{id}/series — değerlendiricinin
// kaynağı ve kayan penceresi), eşik kesik yatay çizgi, problem başlangıcı
// işaretli. Ürünün standart grafiği (CorePanelMulti, uPlot); tooltip zaman +
// değer. Açıklama paragrafı YOK (operatör: "çok detay verince daha anlaşılır
// olmuyor"); yalnız başlık (metrik · servis) + pencere uzunluğu.
//
// Sorgu yalnız bu bölüm mount'luyken (sayfa açık), açık problemde 60 s yoklama
// (gizli sekmede durur). Dizisi olmayan kural (sunucu 404) bölümü hiç çizmez;
// hata yalnız bu panelin içinde kalır — sayfanın geri kalanı etkilenmez.
const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export function AlertMetricChartSection({ problem }: { problem: Problem }) {
  // Yalnız pencereyi / çizimi değiştiren alanlar bağımlılık — yoklamayla gelen
  // yeni problem nesnesi (aynı değerler) sorgu anahtarını ve grafiği oynatmasın.
  const { ruleId, service, metric, startedAt, resolvedAt, status, threshold, comparator, severity } = problem;
  const live = isProblemLive({ status, resolvedAt });
  const args = useMemo(
    () => alertSeriesArgs({ ruleId, service, metric, startedAt, resolvedAt, status }),
    [ruleId, service, metric, startedAt, resolvedAt, status]);
  const q = useAlertRuleSeries(args, { live });
  const data = q.data;
  const state = alertSeriesState({ isPending: q.isPending, isError: q.isError, data });
  const series = useMemo(() => (data ? alertSeriesToSpan(data) : []), [data]);
  const xRange = useMemo(() => (data ? alertSeriesXRange(data) : null), [data]);
  const regions = useMemo(
    () => (xRange ? [problemRegion({ startedAt, resolvedAt, status }, xRange.to)] : []),
    [xRange, startedAt, resolvedAt, status]);
  // Eşik dizisi KİMLİĞİ sabit: CorePanel eşik değişince grafiği yeniden kurar.
  const thresholds = useMemo(
    () => [alertThreshold({ threshold, comparator, severity, metric })],
    [threshold, comparator, severity, metric]);

  if (state === 'gone') return null;
  const title = `${metric} · ${service}`;
  return (
    <Sect title={title} sub={data ? windowLabel(data.windowSec) : undefined}>
      <Suspense fallback={<Spinner />}>
        <CorePanelMultiLazy
          title=""
          ariaLabel={title}
          storageKey="problem-alert-metric"
          height={180}
          unit={alertMetricUnit(metric)}
          zeroBase
          hideLegend
          loading={state === 'loading'}
          error={state === 'error' ? ALERT_SERIES_TEXT.error : undefined}
          emptyReason={state === 'empty' ? ALERT_SERIES_TEXT.empty : undefined}
          xRange={xRange}
          regions={regions}
          thresholds={thresholds}
          items={[{ name: metric, role: 'data', series }]} />
      </Suspense>
    </Sect>
  );
}
