// v0.10.1753 — ML replay görünümü: kayıt → grafik (bant = medyan ± etkin z ×
// effMAD, sunucuyla aynı), tetik kovaları bölge, rollout işareti; what-if
// penceresi şimdiyi aşmaz; dönem replay gün çözümü; backtest satırı.
import { describe, it, expect } from 'vitest';
import { effMAD, replayChart, whatIfWindow, dayClassLabel } from '@/lib/mlReplay';
import { resolveBacktestDays } from '@/components/coremetry/anomalies/MLPeriodReplay';
import { backtestLine } from '@/components/coremetry/anomalies/MLReplaySheet';
import type { MLSnapshot } from '@/lib/types';

const snap = (over: Partial<MLSnapshot> = {}): MLSnapshot => ({
  eventId: 'a', detector: 'op_latency', signal: 'trace', service: 'pay', part: '/pay|p95',
  detectedAt: 1_800_000_000e9, lastSeen: 1_800_000_600e9, bucketSec: 300,
  series: JSON.stringify([{ t: 600, v: 300 }, { t: 0, v: 100 }, { t: 300, v: 110 }]),
  baselineMed: 100, baselineMad: 0, thresholds: JSON.stringify({ criticalZ: 4, effectiveZ: 5 }), configHash: 'h',
  dayClass: 'holiday', dwell: '[300,600]', confirmIds: null, evidence: '', samples: '', complete: true,
  deploys: JSON.stringify([{ service: 'pay', version: 'v2', firstSeenNs: 450e9, spanCount: 1 }]), ...over,
});

describe('ml replay', () => {
  it('chart: sorted series, band from effective z, regions, deploys', () => {
    const c = replayChart(snap());
    expect(c.times).toEqual([0, 300, 600]);
    expect(effMAD(100, 0)).toBe(10);
    expect(c.z).toBe(5);
    expect(c.upper[0]).toBe(150);
    expect(c.lower[0]).toBe(50);
    expect(c.regions).toEqual([{ fromSec: 300, toSec: 600, label: 'tetik' }, { fromSec: 600, toSec: 900, label: 'tetik' }]);
    expect(c.deploys).toEqual([450]);
  });
  it('chart tolerates broken JSON', () => {
    const c = replayChart(snap({ series: '{', dwell: 'x', deploys: '', thresholds: '' }));
    expect(c.times).toEqual([]);
    expect(c.z).toBe(4);
  });
  it('what-if window clamps to now', () => {
    expect(whatIfWindow(10_000e9, 9_000_000)).toEqual({ from: 10_000 - 86400, to: 9_000 });
    expect(whatIfWindow(10_000e9, 1e12)).toEqual({ from: 10_000 - 86400, to: 10_000 + 7200 });
  });
  it('labels and days', () => {
    expect(dayClassLabel('holiday')).toBe('tatil');
    expect(dayClassLabel('')).toBe('—');
    expect(resolveBacktestDays(null)).toBe(7);
    expect(resolveBacktestDays('20')).toBe(7);
    expect(resolveBacktestDays('3')).toBe(3);
    expect(backtestLine(undefined, 'op_latency')).toBe('0 isabet');
    expect(backtestLine({ signal: 'trace', service: 'p', from: 0, to: 1, series: 1, buckets: 1, configHash: '', truncated: false, hits: [],
      detectors: [{ detector: 'op_latency', fired: 3, tp: 1, fp: 1, unlabeled: 1, positives: 1, covered: 1, precision: 0.5, recall: 1 }] }, 'op_latency'))
      .toBe('3 isabet (1 gerçek, 1 değil, 1 etiketsiz) · isabet %50');
  });
});
