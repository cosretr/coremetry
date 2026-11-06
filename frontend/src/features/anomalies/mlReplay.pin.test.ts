// v0.10.1753 — ML replay görünümü: kayıt → grafik (bant = medyan ± etkin z ×
// effMAD, sunucuyla aynı), tetik kovaları bölge, rollout işareti; what-if
// penceresi şimdiyi aşmaz; dönem replay gün çözümü; backtest satırı.
import { describe, it, expect } from 'vitest';
import { effMAD, replayChart, whatIfWindow, dayClassLabel, replayContext, exceptionGroupHref } from '@/lib/mlReplay';
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
    expect(c.regions[0].label).toBe('anomali'); // C2: gölgeli anomali bölgesi
    expect(c.regions.slice(1)).toEqual([{ fromSec: 300, toSec: 600, label: 'tetik' }, { fromSec: 600, toSec: 900, label: 'tetik' }]);
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
  // v0.10.1754 — T1b bağlam referansları.
  it('context refs from evidence', () => {
    const c = replayContext(JSON.stringify({ exceptions: [{ fingerprint: 'f1', type: 'NPE', count: 3 }], oracle: [], bubbleUp: 'okunamadı' }));
    expect(c.exceptions[0].fingerprint).toBe('f1');
    expect(c.bubbleUp).toBeNull();
    expect(c.bubbleUnreadable).toBe(true);
    expect(replayContext('').exceptions).toEqual([]);
    expect(exceptionGroupHref('a b')).toBe('/problems?exc=a%20b');
  });
});

// v0.10.1771 (C2/C3) — bant ve işaretler. Anlatım (başlık / özet / durum /
// "ne değişti" / değer çifti) v0.10.1843'te sunucuya taşındı
// (internal/ml/explain_card.go, explain_card_test.go); burada yalnız grafik.
import { parseBand, replayMarkers } from '@/lib/mlReplay';
import { anomalyPlainHeadline, anomalyPlainSummary } from '@/lib/anomalyNarrative';

describe('ml replay markers and band', () => {
  const s = snap({ detector: 'op_latency', part: '/pay|slow', baselineMed: 0.002, baselineMad: 0.001,
    series: JSON.stringify([{ t: 0, v: 0.002 }, { t: 300, v: 0.3 }, { t: 600, v: 0.5 }]), dwell: '[300,600]',
    thresholds: JSON.stringify({ criticalZ: 4, band: [[0, 0.001, 0.002, 0.003], [300, 0.001, 0.002, 0.003]] }),
    evidence: JSON.stringify({ affected: 120 }), complete: true, lastSeen: 1_800_000_600e9 });
  it('markers and band', () => {
    const m = replayMarkers(s);
    expect(m.first).toBe(300);
    expect(m.peak).toEqual({ t: 600, v: 0.5 });
    expect(m.end).toBe(1_800_000_600);
    expect(parseBand(s.thresholds).get(300)).toEqual({ lo: 0.001, med: 0.002, hi: 0.003 });
    expect(parseBand('{').size).toBe(0);
  });
  it('old anomaly plain headline', () => {
    const e = { id: 'a', kind: 'op_latency', pattern: 'POST /pay', service: 'pay', startedAt: 0, lastSeen: 25 * 60e9, peakRatio: 164, currentRatio: 100, currentCount: 3, sample: '', status: 'active' } as unknown as import('@/lib/types').AnomalyEvent;
    expect(anomalyPlainHeadline(e)).toBe('pay POST /pay yavaşladı');
    expect(anomalyPlainSummary(e, 40 * 60e3)).toBe('Sürüyor · 40 dk · 3 olay, normalin üstünde');
    expect(anomalyPlainSummary({ ...e, status: 'cleared' })).toBe('Bitti · 25 dk · 3 olay, normalin üstünde');
  });
});

// v0.10.1843 — kart sunucudan; FE yalnız gösterir (boş Etki satırı yok,
// özetsiz eski satır cümle kurmadan servis · dedektör adı).
import { cardRows } from '@/components/coremetry/anomalies/MLReplaySheet';
import { mlRowText } from '@/components/coremetry/anomalies/MLTab';

describe('ml card display', () => {
  const card = { summary: 's', what: 'w', howMuch: 'h', since: 'n', impact: '', whyItMatters: 'y', whatChanged: [], whereToLook: [],
    technical: { detector: '', score: '', signature: '', thresholds: '', expected: '', revision: '' },
    header: { kind: 'log', detector: 'error_rate', service: 'a', serviceHref: '/service?name=a', open: true, status: 'Sürüyor · 1 dk', profile: 'unknown' } };
  it('rows skip empty impact', () => {
    expect(cardRows(card).map(r => r.k)).toEqual(['Ne oldu', 'Ne kadar', 'Ne zamandan beri', 'Neden önemli']);
    expect(cardRows({ ...card, impact: 'i' }).map(r => r.k)).toContain('Etki');
  });
  it('row text uses server summary', () => {
    const e = { id: 'x', detector: 'error_rate', signal: 'log', stage: 'shadow', service: 'orders-api', startedAt: 0, lastSeen: 0, windowSec: 300, baseline: 1, observed: 2, score: 1, direction: 'up' };
    expect(mlRowText({ ...e, summary: 'orders-api: hata logları …' })).toBe('orders-api: hata logları …');
    expect(mlRowText(e)).toBe('orders-api · Hata oranı');
  });
});
