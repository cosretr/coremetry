// MLRecordChart — v0.10.2247 (anomali kartı Dilim G, audit §10 G; mockup
// D0/G0 onaylı, K4 = A): kartın TEK grafiği. Yalnız olay kaydındaki seri —
// dedektörün yargıladığı seri — gerçek kova boyunda çizilir.
// v0.10.2366 (operatör: "anomali grafiklerinin anlaşılması zor") — sade: ölçü
// çizgisi + gölgeli "Normal aralık" + kırmızı "Normalin dışında" bölgesi. Kesikli
// beklenen çizgisi ve dikey işaret çizgileri yok (zamanlar başlık altında metin);
// hacim serisi (sağ eksen) gizli başlar, açıklamadaki tek tıkla açılır. Bağlam:
// kaydın tüm snapshot'ı çizilir (olay öncesi elde olan her şey). Render'da ek
// istek yok (ölçüt 9); servis paneli bağlantı olarak kalır.
import { lazy, Suspense, useMemo, useState } from 'react';
import type { TSSeries } from '@/components/viz/TimeSeriesPanel';
import { Spinner } from '@/components/Spinner';
import { Chip } from '@/components/ui/Chip';
import { tf, useLang, useT } from '@/lib/i18n';
import { fmtClock } from '@/lib/utils';
import type { MLRecordView } from '@/lib/types';
import { fmtRecordValue, measureKey, outsideRegions, recordBuckets, axisUnit, peakAtSec } from './mlRecord';
import { requestSeries } from './mlRequests';
import { seriesRoleColor } from '@/lib/chart/seriesRole';
import { MLRequestFigures } from './MLRequestFigures';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

// Ağır uPlot bileşeni ayrı parça (MetricArea ile aynı yol; sayfa bütçesi).
const TimeSeriesPanel = lazy(() => import('@/components/viz/TimeSeriesPanel').then(m => ({ default: m.TimeSeriesPanel })));

const hm = (sec: number) => fmtClock(new Date(sec * 1000)).slice(0, 5);

/** SAF (test): adım metni — "5 min" / "5 dk", "1 h" / "1 sa". */
export function stepText(sec: number, lang: 'en' | 'tr'): string {
  if (sec % 3600 === 0) return `${sec / 3600} ${lang === 'tr' ? 'sa' : 'h'}`;
  if (sec % 60 === 0) return `${sec / 60} ${lang === 'tr' ? 'dk' : 'min'}`;
  return `${sec} ${lang === 'tr' ? 'sn' : 's'}`;
}

export function measureLabel(rec: MLRecordView, t: (k: string) => string): string {
  const k = measureKey(rec);
  return k ? t(k) : rec.metricLabel || rec.metricId || t('mlRecord.measure.value');
}

// v0.10.2380 (operatör 2026-11-19: "ilk grafikte timeseries gibi olan çizgiler",
// "failure rate kırmızı, başarılı yeşil") — hata oranı kaydında grafik oranı
// değil İSTEĞİ çizer: başarılı istek / dk (yeşil, sol eksen) ve başarısız
// istek / dk (kırmızı, sağ eksen), Servis genel görünümündeki OK / Errors
// ayrımıyla aynı işlev ve aynı rol renkleri. İki eksen: başarısız çoğu zaman
// toplamın birkaç yüzdesi, tek eksende dibe yapışırdı. Renk dışı ayrım:
// başarısız çizgi kalın (2,5 px), başarılı ince (1,5 px) + açıklamada eksen adı.
// Eşik çizgisi / bant yok (eksenler istek sayısı); eşiğin aşıldığı süre gölgeli
// kalır, eşik değeri açıklamada yazar. Altında "Şimdi ve normalde" rakamları.
// Eksenler 0'dan başlar (zeroBase): "trafik düşmedi, hata eklendi" doğru okunsun.
// `frameless`: başka bir bölüm kartının içinde (Problem detayı) çerçevesiz.
export function MLRecordChart({ rec, height = 200, frameless = false }: { rec: MLRecordView; height?: number; frameless?: boolean }) {
  const t = useT();
  const lang = useLang();
  const rq = useMemo(() => requestSeries(rec), [rec]);
  const measure = rq ? t('mlReq.title') : measureLabel(rec, t);
  const b = useMemo(() => recordBuckets(rec), [rec]);
  // v0.10.2344 — uzun birim (ör. "spans/request") her eksen etiketine yazılınca
  // etiket kırpılıyordu ("…est"); ölçü adı zaten başlıkta, eksen yalın sayı gösterir.
  const chartUnit = axisUnit(rec.series.unit);
  const volLabel = t(rec.detector === 'svc_telemetry_volume' ? 'mlRecord.chart.spansPerMin' : 'mlRecord.chart.volume');
  const okLabel = t('mlReq.ok');
  const failLabel = t('mlReq.failed');
  const series = useMemo<TSSeries[]>(() => {
    if (rq) {
      const line = (xs: number[]) => xs.map((v, i) => ({ time: rq.t[i] * 1e9, value: v }));
      return [
        { label: okLabel, points: line(rq.ok), color: seriesRoleColor(okLabel, 'success'), unit: '', width: 1.5 },
        { label: failLabel, points: line(rq.failed), color: seriesRoleColor(failLabel, 'error'), unit: '', axis: 'right', width: 2.5 },
      ];
    }
    const pts = (xs: number[]) => xs.map((v, i) => ({ time: b.t[i] * 1e9, value: v }));
    const out: TSSeries[] = [
      // v0.10.2357 (operatör: "merdiven gibi") — sayfalardaki zaman serisi grafikleri gibi düz çizgi; kova noktaları ayrıca işaretli.
      { label: measure, points: pts(b.v), unit: chartUnit },
    ];
    if (b.n) out.push({ label: volLabel, points: pts(b.n), axis: 'right', unit: '', width: 1 });
    return out;
  }, [b, rq, measure, chartUnit, volLabel, okLabel, failLabel]);
  // v0.10.2366 — hacim gizli başlar; panelin kontrollü gizli-seri yolu (hiddenLabels).
  const [showVol, setShowVol] = useState(false);
  const hidden = useMemo(() => new Set(showVol ? [] : [volLabel]), [showVol, volLabel]);
  const envelope = useMemo(() => (!rq && b.lo && b.hi ? {
    lo: { label: 'lo', points: b.lo.map((v, i) => ({ time: b.t[i] * 1e9, value: v })) },
    hi: { label: 'hi', points: b.hi.map((v, i) => ({ time: b.t[i] * 1e9, value: v })) },
    fill: 'var(--accent)',
  } : undefined), [b, rq]);
  const regions = useMemo(() => outsideRegions(b).map(r => ({ ...r, color: 'var(--err)', subtle: true })), [b]);
  const fmt = (v: number) => fmtRecordValue(v, rec.series.unit, lang);
  const band = `${fmt(rec.baseline.lo)}–${fmt(rec.baseline.hi)}`;
  const peak = peakAtSec(rec);
  const span = b.t.length ? `${hm(b.t[0])}–${hm(b.t[b.t.length - 1] + b.bucketSec)}` : '';
  return (
    <div className={frameless ? '' : 'rounded-md border border-border bg-card px-3 py-2.5'} data-testid="ml-record-chart" data-mode={rq ? 'requests' : 'measure'}>
      <p className="m-0 mb-1.5 text-xs text-muted-foreground" data-testid="ml-record-chart-caption">
        <span data-testid="ml-course-kind">{t('mlCourse.snapshot')}</span>
        {' · '}{measure}{' · '}{tf('mlRecord.chart.step', lang, { step: stepText(b.bucketSec, lang) })}{span && ` · ${span}`}
      </p>
      <p className="m-0 mb-1.5 text-xs text-muted-foreground" data-testid="ml-record-chart-times">
        {t('mlRecord.chart.onset')} {hm(rec.onsetAt / 1e9)} · {t('mlRecord.chart.detected')} {hm(rec.detectedAt / 1e9)}
        {peak != null && ` · ${t('mlRecord.card.peakLabel')} ${hm(peak)}`}
        {rec.endedAt ? ` · ${t(rec.endReason === 'no_data' ? 'mlRecord.chart.noData' : 'mlRecord.chart.ended')} ${hm(rec.endedAt / 1e9)}` : ''}
      </p>
      <Suspense fallback={<Spinner />}>
        <TimeSeriesPanel series={series} envelope={envelope} regions={regions} height={height} hiddenLabels={rq ? undefined : hidden} hideLegend zeroBase={!!rq} />
      </Suspense>
      <p className="m-0 mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground" data-testid="ml-record-chart-legend">
        {rq ? (
          <>
            <span className="text-[color:var(--ok)]">─ {okLabel} ({t('mlReq.leftAxis')})</span>
            <span className="font-semibold text-[color:var(--err)]">━ {failLabel} ({t('mlRecord.chart.rightAxis')})</span>
            {regions.length > 0 && <span>▒ {tf('mlReq.over', lang, { thr: fmt(rec.baseline.hi) })}</span>}
          </>
        ) : (
          <>
            <span>— {measure}</span>
            <span>▒ {tf('mlRecord.chart.band', lang, { band })}</span>
            {regions.length > 0 && <span className="text-[color:var(--err)]">▮ {t('mlRecord.chart.outside')}</span>}
            {b.n && (
              <Chip size="xs" active={showVol} data-testid="ml-record-chart-volume" onClick={() => setShowVol(x => !x)}>
                ┃ {volLabel} ({t('mlRecord.chart.rightAxis')})
              </Chip>
            )}
          </>
        )}
        <EntityLink to={{ kind: 'service', name: rec.subject.service }} className="ml-auto">{t('mlRecord.chart.servicePanel')} ↗</EntityLink>
      </p>
      {rq && <MLRequestFigures rec={rec} />}
    </div>
  );
}
