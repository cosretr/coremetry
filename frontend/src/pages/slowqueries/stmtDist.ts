// stmtDist — v0.10.1972 (Statement süre gösterimi "1 2 4", 4 = süre dağılımı; mockup stmt-duration).
// SAF: sunucunun yaklaşık kovaları (TDigest) → çubuk yükseklikleri, medyan / p95 işaret konumları,
// alt yazı oranları. Yükseklik karekök ölçekli: yavaş kuyruk (birkaç yüz çağrı) yüz binlerin yanında
// görünür kalsın.
export interface StmtDistBucket { loMs: number; hiMs: number; count: number }

export interface StmtDistView {
  bars: { key: string; label: string; range: string; count: number; heightPct: number; slow: boolean }[];
  markers: { kind: 'p50' | 'p95'; ms: number; leftPct: number }[];
  total: number;
  under50Pct: number;
  over1sPct: number;
}

export const fmtDistMs = (ms: number): string =>
  ms >= 1000 ? `${+(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)} s` : ms >= 1 ? `${+ms.toFixed(ms >= 10 ? 0 : 1)} ms` : `${+(ms * 1000).toFixed(0)} µs`;

/** Kova içi konum log ölçekte; alt sınırı 0 olan ilk kovada doğrusal, üstü açık son kovada orta. */
function posInBucket(b: StmtDistBucket, ms: number): number {
  if (b.hiMs <= 0) return 0.5;
  if (b.loMs <= 0) return Math.min(1, Math.max(0, ms / b.hiMs));
  return Math.min(1, Math.max(0, Math.log(ms / b.loMs) / Math.log(b.hiMs / b.loMs)));
}

export function stmtDistView(buckets: readonly StmtDistBucket[] | null | undefined, p50Ms?: number, p95Ms?: number): StmtDistView | null {
  const bs = buckets ?? [];
  const total = bs.reduce((a, b) => a + b.count, 0);
  if (bs.length === 0 || total <= 0) return null;
  const mx = Math.max(...bs.map(b => b.count));
  const bars = bs.map(b => ({
    key: `${b.loMs}-${b.hiMs}`,
    label: fmtDistMs(b.loMs),
    range: b.hiMs > 0 ? `${fmtDistMs(b.loMs)} – ${fmtDistMs(b.hiMs)}` : `${fmtDistMs(b.loMs)} üstü`,
    count: b.count,
    heightPct: b.count > 0 ? Math.max(2, Math.sqrt(b.count / mx) * 100) : 0,
    slow: p95Ms != null && p95Ms > 0 && b.loMs >= p95Ms,
  }));
  const markers: StmtDistView['markers'] = [];
  for (const [kind, ms] of [['p50', p50Ms], ['p95', p95Ms]] as const) {
    if (ms == null || !(ms > 0)) continue;
    let i = bs.findIndex(b => ms >= b.loMs && (b.hiMs <= 0 || ms < b.hiMs));
    if (i < 0) i = ms < bs[0].loMs ? 0 : bs.length - 1;
    markers.push({ kind, ms, leftPct: ((i + posInBucket(bs[i], ms)) / bs.length) * 100 });
  }
  const share = (pred: (b: StmtDistBucket) => boolean) => (bs.filter(pred).reduce((a, b) => a + b.count, 0) / total) * 100;
  return {
    bars, markers, total,
    under50Pct: share(b => b.hiMs > 0 && b.hiMs <= 50),
    over1sPct: share(b => b.loMs >= 1000),
  };
}
