// v0.10.2021 — ML listesi: satır = grup (mockup "Liste: tek sayfa").
import { describe, it, expect } from 'vitest';
import type { MLEvent } from '@/lib/types';
import { groupRows, groupStatus, groupStatusText, leadMember } from './mlGroupRows';

const min = 60e9;
const ev = (o: Partial<MLEvent>): MLEvent => ({
  id: 'e', detector: 'op_latency', signal: 'trace', stage: 'shadow', service: 'payment-api',
  startedAt: 0, lastSeen: 10 * min, windowSec: 300, baseline: 1, observed: 2, score: 5, direction: 'up', ...o,
});

describe('groupRows', () => {
  it('aynı grubun üyeleri tek satır; tek üyeli de satır; sinyaller Trace · Log · Metrik sırası', () => {
    const rows = groupRows([
      ev({ id: 'a', groupId: 'g1', detector: 'trace_error_count', score: 9, summary: 'payment-api servisinin POST /payments operasyonunda hata sayısı …', groupStart: 1 * min, groupEnd: 22 * min, groupSignals: ['log', 'trace'] }),
      ev({ id: 'b', groupId: 'g1', detector: 'template_spike', signal: 'log', score: 6, groupStart: 1 * min, groupEnd: 22 * min, groupSignals: ['log', 'trace'] }),
      ev({ id: 'c', service: 'notification-worker', detector: 'rare_template', signal: 'log', score: 4 }),
    ]);
    expect(rows).toHaveLength(2);
    const g = rows.find(r => r.key === 'g1')!;
    expect(g.lead.id).toBe('a');
    expect(g.signals).toEqual(['trace', 'log']);
    expect(g.start).toBe(1 * min);
    expect(g.end).toBe(22 * min);
    expect(rows.find(r => r.key === 'ev:c')!.signals).toEqual(['log']);
  });
  it('en güçlü üye: skor, eşitse en son görülen', () => {
    expect(leadMember([ev({ id: 'x', score: 3, lastSeen: 5 }), ev({ id: 'y', score: 3, lastSeen: 9 })]).id).toBe('y');
  });
});

describe('groupStatus / metin', () => {
  const shadowOpen = { group: 'g', wouldOpen: true, rule: 'r', reason: 'açardı', detectors: [], at: 1 };
  it('Sürüyor = açık + açardı; İzleniyor = açık + açmazdı; Bitti; Değil dendi', () => {
    expect(groupStatus([ev({ groupShadow: shadowOpen })], 10 * min)).toBe('running');
    expect(groupStatus([ev({ groupShadow: { ...shadowOpen, wouldOpen: false } })], 10 * min)).toBe('watching');
    expect(groupStatus([ev({})], 10 * min)).toBe('watching');
    expect(groupStatus([ev({ stage: 'closed' })], 10 * min)).toBe('done');
    expect(groupStatus([ev({ verdict: 'not_anomaly' }), ev({})], 10 * min)).toBe('rejected');
    expect(groupStatus([ev({ verdict: 'not_anomaly' }), ev({ verdict: 'anomaly' })], 10 * min)).toBe('watching');
  });
  it('metinler', () => {
    expect(groupStatusText({ status: 'running', start: 0, end: 0 }, 12 * min)).toBe('Sürüyor · 12 dk');
    expect(groupStatusText({ status: 'watching', start: 0, end: 0 }, 8 * min)).toBe('İzleniyor · 8 dk');
    expect(groupStatusText({ status: 'done', start: 0, end: 18 * min }, 99 * min)).toBe('Bitti · 18 dk sürdü');
    expect(groupStatusText({ status: 'rejected', start: 0, end: 0 }, 1)).toBe('Değil dendi');
  });
});
