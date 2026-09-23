// v0.10.883 (paritesi #8 dilim 3) — cluster kırılımı MV'den p50/p95 + çağrı serisi;
// kolon sırası hücrelerle eşleşir, kaynak rozeti dürüst (MV / spans).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'ServiceClusterBreakdown.tsx'), 'utf8');

describe('ServiceClusterBreakdown — MV kolonları', () => {
  it('kolon kimlikleri sırayla: cluster, calls, trend, errRate, avg, p50, p95, p99', () => {
    const ids = [...src.matchAll(/\{ id: '([a-zA-Z0-9]+)',\s+label:/g)].map(m => m[1]);
    expect(ids.slice(0, 8)).toEqual(['cluster', 'calls', 'trend', 'errRate', 'avg', 'p50', 'p95', 'p99']);
  });
  it('seri Sparkline ile, yoksa —; kaynak rozeti', () => {
    expect(src).toContain("<Sparkline values={c.series}");
    expect(src).toContain("q.data.source === 'mv' ? 'MV' : 'spans'");
    expect(src).toContain("c.p50DurationMs != null");
  });
});
