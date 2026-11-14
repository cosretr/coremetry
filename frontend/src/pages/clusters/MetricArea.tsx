import { lazy, Suspense, type CSSProperties } from 'react';
import { Card, SegmentedControl } from '@/components/ui';
import { Spinner } from '@/components/Spinner';
import { MultiLineChart } from '@/components/MultiLineChart';
import { namedSeriesToSeries } from '@/pages/clusters/trendSeries';
import { metricEnvelope } from '@/pages/clusters/metricEnvelope';
import type { XPin } from '@/lib/chart/xRange';
import type { Threshold } from '@/lib/chart/thresholdLines';
import type { ClusterNamedSeries } from '@/lib/types';

// v0.10.2147 (S8) — zarf kipinin çizimi (viz/TimeSeriesPanel, uPlot'u doğrudan
// yükler) YALNIZ kip devreye girince indirilir: MetricArea'yı kullanan diğer
// sayfaların parçasına uPlot girmez (MultiLineChart'ın CorePanel'i de tembel).
const EnvelopePanel = lazy(() =>
  import('@/components/viz/TimeSeriesPanel').then(m => ({ default: m.TimeSeriesPanel })));

// Clusters S2 — başlık satırının statik stili `.cl-area-*` sınıflarında
// (globals.css); bekleme kutusunun yüksekliği `height`tan hesaplanır.
const waitBox = (height: number): CSSProperties => ({ height });

// MetricArea — başlık + Total/By-X segmented toggle + area chart kartı
// (v0.9.49, design handoff §3+§8 ortak bileşeni). Clusters Overview'un
// CPU/Mem kartlarından çıkarıldı (v0.9.35 ResToggleHeader'ın genel
// hali): Servis → Infrastructure sekmesi aynı kartı "By pod"
// etiketiyle kullanır. Seri yoksa null döner — görünmez-düşer.
export function MetricArea({ title, subtitle, byLabel, totalLabel = 'Total', by, onToggle, series, seriesName, unit, height = 180, maxSeries, totalSeries, onZoom, onZoomReset, syncKey, labelTrimPrefix, xRange, thresholds, zeroBase, envelope }: {
  title: string;
  // v0.9.383 (redesign D7) — insanileştirilmiş başlığın altında ham
  // metrik adı (monospace, soluk) — bilgi kaybı yasak.
  subtitle?: string;
  // v0.9.534 — byLabel/onToggle opsiyonel: HAProxy route panelleri hep
  // route-bazlı, toggle anlamsız; onToggle verilmezse toggle hiç çizilmez.
  byLabel?: string; // "By node" | "By pod" — toggle'ın sağ şıkkı
  // v0.9.146 — sol şık etiketi (varsayılan "Total"); jboss datasource
  // panelleri "By datasource" (off=data_source, on=pod+data_source) yapar.
  totalLabel?: string;
  by?: boolean;
  onToggle?: (v: boolean) => void;
  series: ClusterNamedSeries[] | null | undefined;
  seriesName: string; // Total modunda tek serinin legend adı
  unit?: string;
  height?: number;
  // v0.9.148 — MultiLineChart fold eşiği; jboss datasource panelleri
  // tüm datasource'ları göstersin diye yüksek verilir (others kesme yok).
  maxSeries?: number;
  // v0.9.370 — sunucu kesmesi ifşası: kesme öncesi toplam seri sayısı.
  // series.length'ten büyükse başlıkta "N / M pod" rozeti çizilir; sekme
  // "hangi pod'un heap'i dolu" sorusuna bakarken 8-pod top-cut sessizdi.
  totalSeries?: number;
  // v0.9.58 — drag-seçim global time picker'a yazılsın (operatör
  // isteği): MultiLineChart'ın onZoom'u aynen iletilir; çağıran
  // setRange({preset:'custom', fromMs, toMs}) yapar (Service.tsx emsali).
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  // Grafana-parite M1 — çift-tık: sayfa zoom geri-yığınını pop eder
  // (MultiLineChart'a aynen iletilir; verilmezse mevcut davranış).
  onZoomReset?: () => void;
  // Madde 4 sweep — kardeş kartlarla imleç senkronu (uPlot.sync);
  // MultiLineChart'a aynen iletilir (Clusters CPU/Mem = 'clusters',
  // servis JMX panelleri = RuntimeCharts'ın `runtime:${service}` grubu).
  syncKey?: string;
  // v0.9.539 — lejant kısaltma öneki (genelde deployment adı). Ortak
  // önek her seride aynı olduğu için ayırt edici değil; atılınca
  // lejant Grafana yoğunluğuna yaklaşır. Verilmezse tam ad.
  labelTrimPrefix?: string;
  // v0.9.1042 — x-ekseni sorgu penceresine mıhlanır (unix sec;
  // ServiceCharts v0.9.725 paritesi). Verilmezse veri-fit.
  xRange?: XPin | null;
  // v0.10.719 — yatay eşik çizgileri (pod limit toplamı vb.); MultiLineChart'a
  // aynen iletilir. Verilmezse çizgi yok.
  thresholds?: Threshold[];
  // v0.10.1641 (operator-reported) — y tabanı 0 (softMin 0; CorePanel
  // zeroBase). Mutlak miktar kartları (Clusters CPU/Memory) verir; %0,2'lik
  // değişim tam boy sıçrama gibi çizilmez. Verilmezse veri-fit (bugünkü).
  zeroBase?: boolean;
  // v0.10.2147 (servis sekmeleri S8, mockup operatör onayı 2026-11-13) —
  // AÇIKÇA istenirse ve seri sayısı 3'ü aşıyorsa çizgi yığını yerine zarf (en
  // düşük–en yüksek aralığı) + ortanca + adıyla en yüksek seri (metricEnvelope;
  // çizim viz/TimeSeriesPanel). 3 ve altı seride ya da verilmezse eski çizgiler.
  // Yalnız eşiksiz, tek birimli paneller verir (Router / HAProxy üçlüsü).
  envelope?: boolean;
}) {
  if (!series || series.length === 0) return null;
  const converted = namedSeriesToSeries(series, seriesName, labelTrimPrefix);
  const env = envelope ? metricEnvelope(converted, unit) : null;
  return (
    <Card header={
      <div className="cl-area-head">
        <span>
          {title}
          {subtitle && (
            <span className="mono cl-area-sub">{subtitle}</span>
          )}
          {totalSeries != null && series.length < totalSeries && (
            <span className="badge b-warn cl-area-cut"
              title={`Sunucu ortalaması en yüksek ${series.length} pod'u döndürdü (${totalSeries} pod'dan). Pencere sonunda sıçrayan ama ortalaması düşük bir pod bu kesimin dışında kalabilir — tek pod'a bakmak için satırdan pod seçin.`}>
              {series.length} / {totalSeries} pod
            </span>
          )}
        </span>
        {onToggle && byLabel && (
          <span onClick={e => e.stopPropagation()}>
            <SegmentedControl size="sm" aria-label="Kırılım" value={by ? 'by' : 'total'}
              onChange={v => onToggle(v === 'by')}
              options={[{ value: 'total', label: totalLabel }, { value: 'by', label: byLabel }]} />
          </span>
        )}
      </div>
    }>
      {env ? (
        <>
          {/* Saniye eksenli motor: imleç senkronu ham anahtarla (CorePanel'in '-ms' ad alanına karışmaz). */}
          <Suspense fallback={<div className="metric-env-wait" style={waitBox(height)}><Spinner /></div>}>
            <EnvelopePanel series={env.lines} envelope={env.envelope} height={height} mode="line"
              syncKey={syncKey} onZoom={onZoom} onZoomReset={onZoomReset} xRange={xRange} />
          </Suspense>
          <p className="metric-env-note">
            {env.n} seri · gri alan en düşük–en yüksek aralığı · çizgiler ortanca ve en yüksek (<span className="mono">{env.top}</span>)
          </p>
        </>
      ) : (
      <MultiLineChart series={converted} height={height} unit={unit} maxSeries={maxSeries} onZoom={onZoom} onZoomReset={onZoomReset} syncKey={syncKey} xRange={xRange} thresholds={thresholds} zeroBase={zeroBase} />
      )}
    </Card>
  );
}
