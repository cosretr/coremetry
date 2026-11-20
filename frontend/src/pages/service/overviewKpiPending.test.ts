import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// v0.10.368 — operator-reported: "Service overview yüklenirken önce başka
// response time çıkıyor, sayfa yüklendikten sonra başka". In metric mode
// the KPI tiles decide their source from the metric query's RESULT, so
// while it was loading "no result yet" read as "span" and the tiles
// flashed span P99 / span throughput (206 ms, 69 req/s) before jumping
// to the metric numbers (16 ms, 4.1 req/s). The tiles now wait: metric
// label, "…" value, no spark/delta — span only when the metric truly
// does not exist. Source pin: the gate must stay on BOTH tiles.
const src = readFileSync(fileURLToPath(new URL('./Overview.tsx', import.meta.url)), 'utf8');

describe('Overview KPI tiles wait for the metric query in metric mode', () => {
  it('derives the pending flag from metric mode + metric query loading', () => {
    expect(src).toContain('const metricTilesPending = metricMode && metricTputQ.isLoading;');
  });

  it('labels stay metric-flavoured while pending (no "(span)" flash)', () => {
    // v0.10.1360 — Response time karosu pencere sorgusunu da bekler.
    expect(src).toContain('const rtTilePending = metricTilesPending || (metricMode && ovWin.avgPending);');
    // v0.10.2315 — "son adım" etiketi katalogda (ov.rt.labelLast); TR değeri aynen.
    expect(src).toContain("const rtLabel = rtFromMetric || rtTilePending\n    ? (rtLastStep && !rtTilePending ? tl('ov.rt.labelLast', lang) : 'Response time · avg')");
    expect(readFileSync(fileURLToPath(new URL('../../lib/i18n.tr.ts', import.meta.url)), 'utf8')).toContain("'ov.rt.labelLast': 'Response time · avg · son adım',");
    expect(src).toContain("tputFromMetric || metricTilesPending ? 'Throughput'");
  });

  it('both tiles render "…" with no spark / delta while pending', () => {
    const rt = src.indexOf('<KpiTile lab={rtLabel}');
    const tput = src.indexOf('<KpiTile lab={tputLabel}');
    expect(rt).toBeGreaterThan(0);
    expect(tput).toBeGreaterThan(rt);
    // v0.10.1360 — Response time karosunun kapısı rtTilePending (⊇ metricTilesPending).
    const tiles: [string, string][] = [[src.slice(rt, tput), 'rtTilePending'], [src.slice(tput, src.indexOf('<KpiTile lab="Failure rate"')), 'metricTilesPending']];
    for (const [tile, gate] of tiles) {
      expect(tile).toContain(`val={${gate} ? '…' :`);
      expect(tile).toContain(`spark={${gate} ? undefined :`);
      expect(tile).toContain(`delta={${gate} ? null :`);
    }
  });

  it('the P99 sub-line never shows a span number under a pending metric tile', () => {
    // v0.10.1360 — alt satır rtSub'da; bekleme anında hiç yazılmaz.
    expect(src).toContain('const rtSub = rtTilePending || !rtFromMetric ? undefined');
    expect(src).toContain('sub={rtSub}');
  });
});
