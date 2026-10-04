// TracePodCharts — v0.10.968 — seçili pod'un Bellek + CPU grafikleri (Trace ›
// Metrics yeniden tasarımı, mockup PodDetail.dc.html / Main.dc.html sağ panel).
//
// v0.10.1096 — satır altı ayrıntı grafikleri panelin EN ÜSTÜNDE tek yatay
// sırada (operatör: "sadece metrik yatayda inline gözükse olacak"). Panel
// `usePodChartBuilds` + `PodChart`'ı doğrudan dizer (Bellek, CPU, varsa JVM
// heap/GC aynı `.tpp-chart-row` ızgarasında); başlık TEK satır ("Bellek 9,03
// GiB · limit 16 GiB", `head`), limit lejantta tekrar edilmez (`legendLimit`).
// Açıklama (`podChartsCaption`) kapalı "Teknik ayrıntı"ya gider. Odak görünümü
// (`TracePodCharts`, alt alta) değişmedi. v0.10.976'nın `layout="row"`u kalktı.
// Öğe kurulumu ve açıklama `usePodChartBuilds.ts`te (bu dosya yalnız bileşen).
//
// v0.10.968 — Çizim TEK motordan: CorePanelMulti, MultiLineChart'ın yüklediği
// gibi lazy (`@/components/chart/corePanelEntry`; sayfa @grafana/data'ya
// statik bağlanmaz). Öğeler SAF çekirdekten (tracePodPanelModel.buildChartItems):
// karşılaştırılan pod'lar `data`, kardeşler `muted` (≤12, "+N çizilmedi"),
// trace bandı en az bir adım geniş (var(--accent2), "trace"), limit çizgisi
// yalnız limit bilinirken (var(--warn), "limit (şu an)"), iki panel de
// zeroBase. Bellek ÖNCE, CPU sonra (v0.10.916 operator-reported sırası).
//
// Lejant panelin kendi tablosu DEĞİL (hideLegend): tek satır — swatch + son
// ek + trace-anı değeri, "limit X (şu an)", "diğer N pod". Swatch rengi
// çizgi rengiyle AYNI kaynaktan; tema değişince useThemeTick yeniden çözer.
//
// v0.10.968 — renk ARTIK yalnız addan değil: karşılaştırma sırasıyla
// ÇAKIŞMASIZ atama (compareColors → seriesColorsFor). Hash-yalnız renkte iki
// pod aynı yuvaya düşüp (mockup'ın kendi p5r8d/b2q6f çifti) aynı çizgiyle
// çiziliyordu. Harita karşılaştırma listesinin TAMAMINDAN kurulur ki çip
// swatch'ı (TracePodPanel) ile aynı rengi versin.
// Senkron crosshair: `trace-metrics-<svc>` + msSyncKey (JVM paneli MLC
// üzerinden aynı ad alanına düşer).
import { lazy, Suspense, type ReactNode } from 'react';
import { Skeleton } from '@/components/Skeleton';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import { useThemeTick } from '@/lib/useThemeTick';
import { legendValue, limitLegend, type ChartBuild } from './tracePodPanelModel';
import { podChartsCaption, usePodChartBuilds, type PodChartInputs } from './usePodChartBuilds';

const CoreMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export interface TracePodChartsProps extends PodChartInputs {
  height?: number;
  /** Odak görünümü: açıklama "Limitler bugünkü değerdir…" ile biter. */
  focus?: boolean;
}

const CHART_TITLE: Record<ChartBuild['kind'], string> = { mem: 'Bellek (working set)', cpu: 'CPU (çekirdek)' };
const UNIT: Record<ChartBuild['kind'], string> = { mem: 'bytes', cpu: 'cores' };

/** v0.10.968 — tek grafik kabı (lejant + CorePanelMulti). v0.10.1096'te satır
 *  altı ayrıntı da doğrudan dizer (usePodChartBuilds öğeleriyle). */
export function PodChart({ build, height, syncKey, xRange, head, legendLimit = true }: {
  build: ChartBuild;
  height: number;
  syncKey: string;
  xRange: { from: number; to: number };
  /** v0.10.1096 — tek satır başlık (değer + limit); verilmezse CHART_TITLE. */
  head?: ReactNode;
  /** v0.10.1096 — false: limit başlıkta, lejantta tekrar yazılmaz. */
  legendLimit?: boolean;
}) {
  useThemeTick();
  const k = build.kind;
  return (
    // v0.10.976 — grafik kabı adlandırılmış grup: CorePanel'e title="" gidiyor
    // (görsel başlık .tpp-sec-title'da), bu yüzden erişilebilir ad BURADAN;
    // CHART_TITLE[k] tek kaynak, görünen metinle ayrışamaz.
    <div className="tpp-chart" data-chart={k} role="group" aria-label={CHART_TITLE[k]}>
      <div className="tpp-sec-title">{head ?? <span>{CHART_TITLE[k]}</span>}</div>
      <div className="tpp-legend">
        {build.legend.map(l => (
          <span key={l.pod} title={l.pod}>
            <span className="tpp-swatch" aria-hidden="true" style={{ background: l.color }} />
            {' '}<span className="mono">{l.label}</span> {legendValue(k, l.value)}
          </span>
        ))}
        {legendLimit && build.limit != null && <span>{limitLegend(k, build.limit)}</span>}
        {build.siblingsDrawn > 0 && <span>diğer {build.siblingsDrawn + build.siblingsHidden} pod</span>}
      </div>
      <Suspense fallback={<Skeleton height={height} />}>
        <CoreMultiLazy
          title=""
          storageKey={`trace-pod-${k}`}
          height={height}
          unit={UNIT[k]}
          items={build.items}
          regions={build.regions}
          thresholds={build.thresholds}
          xRange={xRange}
          syncKey={msSyncKey(syncKey)}
          hideLegend
          zeroBase
        />
      </Suspense>
    </div>
  );
}

/** v0.10.968 — odak görünümü: Bellek üstte, CPU altta, açıklama altta. */
export function TracePodCharts({ height = 140, focus, ...rest }: TracePodChartsProps) {
  const b = usePodChartBuilds(rest);
  // v0.10.916 (operator-reported) — bellek üstte, CPU altta; ikisi de zeroBase.
  return (
    <div className="tpp-chart">
      <PodChart build={b.mem} height={height} syncKey={b.syncKey} xRange={b.xRange} />
      <PodChart build={b.cpu} height={height} syncKey={b.syncKey} xRange={b.xRange} />
      <p className="tpp-caption">{podChartsCaption(b, rest.window, rest.selected.service, focus)}</p>
    </div>
  );
}
