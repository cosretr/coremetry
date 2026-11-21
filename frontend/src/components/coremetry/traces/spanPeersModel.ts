// spanPeersModel.ts — dalga 3 (2026-11-21, Honeycomb "span vs peers"): aynı
// servis + operasyonun trace penceresindeki süre dağılımı. Kaynak mevcut
// /api/spans/peers (2026-11-21; spanmetrics_1m TDigest'inden tek sütun, MV
// boşsa ham heatmap ızgarası — ikisi de LatencyHeatmap şekli). Bu modül SAF:
// ızgarayı zaman ekseninde katlar, span'in kovasını ve yaklaşık yüzdelik
// sırasını bulur.
import type { LatencyHeatmap } from '@/lib/types';

/** Trace penceresinin iki yanına eklenen pay: tek bir trace milisaniyeler sürer, akranlar ±15 dk'da. */
export const PEER_PAD_NS = 15 * 60 * 1e9;

export interface PeerHist {
  bins: number[];     // kova üst sınırı (ms), boş uçlar kırpılmış
  counts: number[];
  total: number;
  spanIdx: number;    // bu span'in kovası (kırpılmış dizide); -1 = dışarıda
  rankPct: number | null; // ≈ yüzdelik sıra (0–100); toplam 0 → null
}

export function peerWindow(traceStartNs: number, traceEndNs: number): { from: number; to: number } {
  // Sunucu önbellek kovasına otursun diye dakikaya yuvarlanır (aynı trace'in
  // span'leri arasında gezinmek aynı anahtarı tekrar kullanır).
  const MIN = 60e9;
  return {
    from: Math.floor((traceStartNs - PEER_PAD_NS) / MIN) * MIN,
    to: Math.ceil((traceEndNs + PEER_PAD_NS) / MIN) * MIN,
  };
}

export function foldPeers(h: LatencyHeatmap | null | undefined, spanMs: number): PeerHist | null {
  if (!h || h.durationBins.length === 0) return null;
  const m = h.durationBins.length;
  const sum = new Array<number>(m).fill(0);
  for (const col of h.counts) for (let j = 0; j < m && j < col.length; j++) sum[j] += col[j] || 0;
  let first = -1, last = -1;
  for (let j = 0; j < m; j++) if (sum[j] > 0) { if (first < 0) first = j; last = j; }
  // Span'in kovası: üst sınırı spanMs'i karşılayan ilk kova; taşma → son kova.
  let own = h.durationBins.findIndex(b => spanMs <= b);
  if (own < 0) own = m - 1;
  if (first < 0) return { bins: [], counts: [], total: 0, spanIdx: -1, rankPct: null };
  const lo = Math.min(first, own), hi = Math.max(last, own);
  const counts = sum.slice(lo, hi + 1);
  const total = counts.reduce((a, b) => a + b, 0);
  let below = 0;
  for (let j = lo; j < own; j++) below += sum[j];
  const rankPct = total > 0 ? Math.round(((below + sum[own] / 2) / total) * 100) : null;
  return { bins: h.durationBins.slice(lo, hi + 1), counts, total, spanIdx: own - lo, rankPct };
}
