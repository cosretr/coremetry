import { useCallback, useMemo, useRef, useState } from 'react';
import { downsampleBuckets, maxBarsForWidth, barIndexAt, sparkRenderMode, sparkBucketWindow, fmtBucketWindow } from '@/lib/sparkline';
import { fmtNum } from '@/lib/utils';
import { fmtSmart } from '@/lib/chartFmt';
import { SparkReadout } from './SparkReadout';

// TrendSpark — v0.10.697 (operatör: Operations tablosundaki üç mikro sparkline
// "kullanışsız"; mockup B onaylı). Tek geniş trend grafiği: çubuklar çağrı
// hacmi, çubuğun kırmızı kısmı hata payı (aynı ölçek), üstte p99 çizgisi
// (kendi ölçeği), sağ uçta son değer noktası; hover'da kova değerleri.
// Sparkline.tsx'in saf yardımcıları (downsample / genişlik bütçesi / kova
// indeksi) — ikinci geometri yazımı yok. Chart kütüphanesi yok: tablo içi
// mini grafik, Sparkline emsali.
//
// v0.10.1059 (operatör, prod: "üzerine gelince bir şey çıkıyor ama
// anlaşılmıyor") — hover okuması hücrenin içinde absolute çiziliyordu ve
// `tbody td { overflow: hidden }` onu yarıdan kesiyordu; yanında düğmenin
// `title`ı ikinci bir yerel ipucu açıyordu. Okuma artık SparkReadout
// (portal, grafik ipucu şablonu .ov-tt): kovanın SAATİ (fromMs/toMs
// verilince; "kova 16/30" değil) + calls · errors · p99 + isteğe bağlı
// soluk `hint` satırı. Yalnız fare: dokunmatikte dokunuş tıklamadır.
const SERIES_C = { calls: 'var(--orange)', errors: 'var(--err)', p99: 'var(--teal)' } as const;

export function TrendSpark({ calls, errors, p99, width = 160, height = 30, className, fromMs, toMs, hint }: {
  calls: number[];
  errors?: number[];
  p99?: number[];
  width?: number;
  height?: number;
  className?: string;
  /** Serinin kapsadığı pencere (unix ms) — okumadaki saat buradan. */
  fromMs?: number;
  toMs?: number;
  /** Okumanın son, soluk satırı (ör. "tıkla: grafik"). */
  hint?: string;
}) {
  const n = Math.min(maxBarsForWidth(width, 3), Math.max(calls.length, 1));
  const c = useMemo(() => downsampleBuckets(calls, n, 'sum'), [calls, n]);
  const e = useMemo(() => downsampleBuckets(errors ?? [], n, 'sum'), [errors, n]);
  const p = useMemo(() => downsampleBuckets(p99 ?? [], n, 'max'), [p99, n]);
  const [hover, setHover] = useState<number | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const dismiss = useCallback(() => setHover(null), []);
  if (sparkRenderMode(calls) === 'nodata') {
    return <span className={className} style={{ display: 'inline-block', width, height, lineHeight: `${height}px`, textAlign: 'center', color: 'var(--text3)', fontSize: 11 }}>—</span>;
  }
  const N = c.length;
  const bw = width / N;
  const maxC = Math.max(1, ...c.map(v => v ?? 0));
  const maxP = Math.max(1, ...p.map(v => v ?? 0));
  const inner = height - 4;
  const pts: string[] = [];
  let last: [number, number] | null = null;
  p.forEach((v, i) => {
    if (v == null) return;
    const x = i * bw + bw / 2;
    const y = height - 2 - (v / maxP) * (inner - 2);
    pts.push(`${x.toFixed(1)},${y.toFixed(1)}`);
    last = [x, y];
  });
  const hi = hover != null && hover < N ? hover : null;
  const win = hi != null && fromMs != null && toMs != null
    ? sparkBucketWindow(hi, N, calls.length, fromMs, toMs) : null;
  return (
    <span className={`trend-spark${className ? ' ' + className : ''}`} style={{ width, height }}>
      <svg ref={svgRef} width={width} height={height} role="img" aria-label="çağrı · hata · p99 trendi"
        onPointerMove={ev => {
          if (ev.pointerType && ev.pointerType !== 'mouse') return;
          const r = ev.currentTarget.getBoundingClientRect();
          setHover(barIndexAt(ev.clientX - r.left, r.width, N));
        }}
        onPointerLeave={dismiss}>
        {c.map((v, i) => {
          const h = v ? Math.max(1, (v / maxC) * inner) : 0;
          const ev = e[i] ?? 0;
          const eh = ev > 0 ? Math.max(1, (ev / maxC) * inner) : 0;
          const x = i * bw + 0.5;
          return (
            <g key={i} className={i === hi ? 'is-hover' : undefined}>
              {h > 0 && <rect className="ts-bar" x={x} y={height - h} width={Math.max(0.5, bw - 1)} height={h} />}
              {eh > 0 && <rect className="ts-err" x={x} y={height - eh} width={Math.max(0.5, bw - 1)} height={eh} />}
            </g>
          );
        })}
        {pts.length > 1 && <polyline className="ts-p99" points={pts.join(' ')} />}
        {last && <circle className="ts-cur" cx={last[0]} cy={last[1]} r={2} />}
      </svg>
      {hi != null && (
        <SparkReadout anchorRef={svgRef} at={(hi + 0.5) / N}
          title={win ? fmtBucketWindow(win.startMs, win.endMs, win.stepSec) : `kova ${hi + 1}/${N}`}
          rows={[
            { label: 'Calls', color: SERIES_C.calls, value: fmtNum(Math.round(c[hi] ?? 0)) },
            { label: 'Errors', color: SERIES_C.errors, value: fmtNum(Math.round(e[hi] ?? 0)) },
            { label: 'P99', color: SERIES_C.p99, value: fmtSmart(p[hi], 'ms') },
          ]}
          hint={hint}
          onDismiss={dismiss} />
      )}
    </span>
  );
}
