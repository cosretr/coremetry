// TraceMetricCell — v0.10.968 — Trace › Metrics tablosunun CPU / Bellek hücresi.
//
// v0.10.968 — Metrikler Thanos'tan GEÇ gelir ve eksik veri HÜCREDE söylenir
// (onaylı mockup "Hücre durumları"; T12: durum tablonun içinde). Her durumun
// ayrı metni var: yükleniyor, eşlenmemiş, örnek yok, belirsiz, okunamadı (bir
// HATA, boş sonuç değil — v0.10.962 hata 3), yüklenmedi. Renk YALNIZ sapmada
// (limitin ≥%70'i uyarı, ≥%90'ı kritik + 600); limit tanımsızsa mutlak değer,
// renksiz. Hücre `<td>`sini tablo basar; ton sınıfı metricCellTone ile aynı
// karardan.
//
// v0.10.968 — inceleme turu: ipucu TÜM hücrede (ui/Tooltip, Türkçe, §7
// biçimleri); sparkline SÜS — aria-hidden sarmalayıcıda, fareyi almaz
// (.tpm-spark pointer-events:none) ve başlık taşımaz: kendi İngilizce kova
// başlığı ("bucket 57/121: 1.5B", noktalı ondalık) hiç görünmez, ekran okuyucu
// ipucunu tek kez duyar. Sparkline YALNIZ limit bilinirken çizilir (spec
// hücre tablosu "ok, limit known"); mutlak değer durumlarında ("612,34 MiB")
// 116 px'lik kolonun tamamı metne kalır — sparkline'la birlikte kırpılıyordu.
import { Tooltip } from '@/components/ui';
import { Sparkline } from '@/components/Sparkline';
import { Skeleton } from '@/components/Skeleton';
import { okCellText, stateCellText, type MetricKind } from './traceMetrics';
import type { PodMetricSeries, PodMetricState } from './traceMetricsModel';

export function TraceMetricCell({ state, which, markerAt }: {
  state: PodMetricState | null;
  which: MetricKind;
  /** Trace anının kesirli kova indeksi (seri başına). */
  markerAt: (d: PodMetricSeries) => number | undefined;
}) {
  if (!state || state.kind === 'off') return null;
  if (state.kind === 'loading') {
    return (
      <span className="tpm-mcell" role="img" aria-label="Yükleniyor">
        <Skeleton width={48} height={8} inline />
      </span>
    );
  }
  if (state.kind !== 'ok') {
    const { text, tip } = stateCellText(state);
    return (
      <Tooltip content={tip}>
        <span className="tpm-mcell">{text}</span>
      </Tooltip>
    );
  }
  const d = state.data;
  const { text, tip } = okCellText(d, which);
  const series = which === 'cpu' ? d.cpu : d.mem;
  const lim = which === 'cpu' ? d.cpuLimit : d.memLimit;
  const lv = which === 'cpu' ? d.cpuLevel : d.memLevel;
  const limKnown = !!lim && lim > 0;
  let spark = null;
  if (limKnown) {
    const finite = series.filter((v): v is number => v != null && Number.isFinite(v));
    const smax = finite.length ? Math.max(...finite) : 0;
    const domainMax = Math.max(lim, smax) || undefined;
    const color = lv === 'err' ? 'var(--err)' : lv === 'warn' ? 'var(--warn)' : 'var(--text3)';
    spark = (
      <span className="tpm-spark" aria-hidden="true">
        <Sparkline values={series} width={52} height={16} color={color} domainMax={domainMax} markerAt={markerAt(d)} />
      </span>
    );
  }
  return (
    <Tooltip content={tip}>
      <span className="tpm-mcell">
        {spark}
        <span className="tpm-mval">{text}</span>
      </span>
    </Tooltip>
  );
}
