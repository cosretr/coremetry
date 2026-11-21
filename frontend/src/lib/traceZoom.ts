// traceZoom.ts — dalga 3 (2026-11-21, Grafana 13 trace minimap): şelalenin
// zaman yakınlaştırması. URL'de `?wz=<başlangıç>_<bitiş>` — trace başlangıcına
// göre ns OFSET (mutlak zaman değil: link başka saat diliminde / farklı
// pencerede açılsa da aynı dilimi gösterir). Paylaşılan link aynı görünüme
// döner. Codec lib/urlSchema üstünden (bozuk değer → yakınlaştırma yok).
import { z, zParam } from './urlSchema';

/** [başlangıç, bitiş] ns, trace başlangıcına göre; null = tam trace. */
export type TraceZoom = readonly [number, number] | null;

const RE = /^\d{1,15}_\d{1,15}$/;

export const traceZoomCodec = zParam<TraceZoom>(
  z.pipe(
    z.string().check(z.regex(RE)),
    z.transform((s): TraceZoom => {
      const [a, b] = s.split('_').map(Number);
      return [a, b] as const;
    }),
  ).check(z.refine(v => v !== null && v[1] > v[0])),
  null,
  v => (v ? `${Math.round(v[0])}_${Math.round(v[1])}` : ''),
);

/** En dar pencere: trace'in %0.5'i (≥1 µs) — tek pikselde çubuk kalmasın. */
export function clampTraceZoom(zoom: TraceZoom, totalNs: number): TraceZoom {
  if (!zoom || totalNs <= 0) return null;
  let a = Math.max(0, Math.min(zoom[0], totalNs));
  let b = Math.max(0, Math.min(zoom[1], totalNs));
  if (b < a) [a, b] = [b, a];
  const minW = Math.max(1_000, totalNs * 0.005);
  if (b - a < minW) {
    const mid = (a + b) / 2;
    a = Math.max(0, mid - minW / 2);
    b = Math.min(totalNs, a + minW);
    a = Math.max(0, b - minW);
  }
  if (a <= 0 && b >= totalNs) return null; // tam trace = yakınlaştırma yok
  return [a, b];
}

/** Çubuğu görünür pencereye kırpar; pencerede değilse null. Yüzdeler pencereye göre. */
export function clipBar(start: number, end: number, viewMin: number, viewTotal: number):
  { s: number; e: number; leftPct: number; widthPct: number } | null {
  const viewMax = viewMin + viewTotal;
  const s = Math.max(start, viewMin), e = Math.min(end, viewMax);
  if (e < s || (e === s && (start > viewMax || end < viewMin))) return null;
  return { s, e, leftPct: ((s - viewMin) / viewTotal) * 100, widthPct: ((e - s) / viewTotal) * 100 };
}
