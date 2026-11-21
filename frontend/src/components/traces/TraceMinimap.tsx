// TraceMinimap — v0.10.278 (trace view Dilim 1d, docs/audit/trace-view.md §3.6).
// Uzun şelalenin tamamı tek bakışta: her satır bir ince çizgi (x = başlangıç/
// süre, y = satır sırası), renk servis (svcColor ile aynı hex paleti — canvas
// CSS var okuyamaz), görünür pencere accent çerçeve, tıkla → o satıra kaydır.
// Canvas: 5000+ satırda SVG düğümü yerine tek çizim. Yapışkan DEĞİL (ev
// kuralı: sayfa düzeyi floating şerit yok) — başlığın altında durur.
//
// Dalga 3 (2026-11-21, Grafana 13 minimap) — YATAY SÜRÜKLEME = zaman fırçası:
// bırakınca onBrush([başlangıç, bitiş] ns ofset) → şelale o dilime yakınlaşır.
// 4 px'ten kısa hareket tıklama sayılır (eski satıra-git davranışı aynen).
// Mevcut yakınlaştırma `zoom` ile accent dolgu + dışı soluk çizilir.
import { useEffect, useRef, useState, type CSSProperties } from 'react';
import type { SpanRow } from '@/lib/types';
import type { TraceZoom } from '@/lib/traceZoom';
import { useT } from '@/lib/i18n';

const DRAG_PX = 4;

export function TraceMinimap({ spans, minT, totalNs, colorFor, range, onSeek, height = 40, zoom = null, onBrush }: {
  spans: SpanRow[];            // satır sırasında
  minT: number;
  totalNs: number;
  colorFor: (s: SpanRow) => string;
  /** görünür satır aralığı [ilk, son] (sanal modda); null = çerçeve yok */
  range: [number, number] | null;
  onSeek: (rowIndex: number) => void;
  height?: number;
  /** Dalga 3 — şelalenin güncel yakınlaştırması (ns ofset); null = tam trace. */
  zoom?: TraceZoom;
  /** Dalga 3 — verilirse yatay sürükleme bir zaman aralığı seçer. */
  onBrush?: (z: [number, number]) => void;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const t = useT();
  const dragRef = useRef<{ x0: number; left: number; width: number } | null>(null);
  const [drag, setDrag] = useState<[number, number] | null>(null); // 0..1 kesirleri
  const brushedRef = useRef(false); // sürükleme bitti → ardından gelen click satıra gitmesin
  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    const draw = () => {
      const w = cv.clientWidth || 600;
      const dpr = (typeof window !== 'undefined' && window.devicePixelRatio) || 1;
      cv.width = Math.round(w * dpr); cv.height = Math.round(height * dpr);
      const ctx = cv.getContext?.('2d');
      if (!ctx) return; // jsdom: canvas yok
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, height);
      const n = spans.length || 1;
      const rowH = height / n;
      for (let i = 0; i < spans.length; i++) {
        const s = spans[i];
        const x = ((s.startTime - minT) / totalNs) * w;
        const bw = Math.max(1, ((s.endTime - s.startTime) / totalNs) * w);
        ctx.fillStyle = colorFor(s);
        ctx.globalAlpha = 0.85;
        ctx.fillRect(x, i * rowH, bw, Math.max(1, rowH));
      }
      ctx.globalAlpha = 1;
      if (range) {
        const accent = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#2d73d8';
        const y0 = (range[0] / n) * height, y1 = ((range[1] + 1) / n) * height;
        ctx.strokeStyle = accent; ctx.lineWidth = 1.5;
        ctx.strokeRect(0.75, y0 + 0.75, w - 1.5, Math.max(2, y1 - y0 - 1.5));
      }
    };
    draw();
    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(draw);
    ro.observe(cv);
    return () => ro.disconnect();
  }, [spans, minT, totalNs, colorFor, range, height]);

  const frac = (x: number) => {
    const d = dragRef.current!;
    return Math.max(0, Math.min(1, (x - d.left) / Math.max(1, d.width)));
  };
  const band = drag ?? (zoom ? [zoom[0] / totalNs, zoom[1] / totalNs] as [number, number] : null);
  // Hesaplanan konum (tek eleman; dışı .tm-brush box-shadow'u ile soluk).
  const brushStyle: CSSProperties | null = band
    ? { left: `${band[0] * 100}%`, width: `${(band[1] - band[0]) * 100}%` } : null;
  return (
    <div className="tm-wrap" data-testid="trace-minimap"
      title={onBrush ? t('trace.minimap.tip') : 'Trace haritası — tıkla: o satıra git'} style={{ height }}
      onPointerDown={e => {
        if (!onBrush || e.button !== 0) return;
        const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
        dragRef.current = { x0: e.clientX, left: r.left, width: r.width };
        (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
      }}
      onPointerMove={e => {
        const d = dragRef.current;
        if (!d || Math.abs(e.clientX - d.x0) < DRAG_PX) return;
        const a = frac(d.x0), b = frac(e.clientX);
        setDrag([Math.min(a, b), Math.max(a, b)]);
      }}
      onPointerUp={e => {
        const d = dragRef.current;
        dragRef.current = null;
        setDrag(null);
        if (!d || Math.abs(e.clientX - d.x0) < DRAG_PX) return;
        brushedRef.current = true;
        const a = (Math.max(0, Math.min(1, (d.x0 - d.left) / Math.max(1, d.width))));
        const b = (Math.max(0, Math.min(1, (e.clientX - d.left) / Math.max(1, d.width))));
        onBrush?.([Math.min(a, b) * totalNs, Math.max(a, b) * totalNs]);
      }}
      onPointerCancel={() => { dragRef.current = null; setDrag(null); }}
      onClick={e => {
        if (brushedRef.current) { brushedRef.current = false; return; }
        const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const y = e.clientY - r.top;
        onSeek(Math.max(0, Math.min(spans.length - 1, Math.floor((y / Math.max(1, r.height)) * spans.length))));
      }}>
      <canvas ref={ref} />
      {brushStyle && <span className="tm-brush" data-testid="trace-minimap-brush" style={brushStyle} />}
    </div>
  );
}
