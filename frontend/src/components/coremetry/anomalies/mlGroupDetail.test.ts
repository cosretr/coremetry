// v0.10.2022 — grup detay sayfası saf yardımcıları.
import { describe, it, expect } from 'vitest';
import type { MLEvent } from '@/lib/types';
import { evidenceSource, findGroup, headerOperation , recordDecision } from './mlGroupDetail';

const ev = (o: Partial<MLEvent>): MLEvent => ({
  id: 'e', detector: 'op_latency', signal: 'trace', stage: 'shadow', service: 'payment-api',
  startedAt: 0, lastSeen: 1, windowSec: 300, baseline: 1, observed: 2, score: 5, direction: 'up', ...o,
});

describe('findGroup', () => {
  const list = [ev({ id: 'a', groupId: 'g', score: 9 }), ev({ id: 'b', groupId: 'g', score: 2 }), ev({ id: 'c', score: 7 })];
  it('üye kimliğiyle açılınca o üyenin grubu, en güçlü üye başta', () => {
    const g = findGroup(list, 'b')!;
    expect(g.members.map(m => m.id).sort()).toEqual(['a', 'b']);
    expect(g.lead.id).toBe('a');
  });
  it('grupsuz olay tek üyeli grup; listede yoksa null', () => {
    expect(findGroup(list, 'c')!.members).toHaveLength(1);
    expect(findGroup(list, 'zz')).toBeNull();
    expect(findGroup(undefined, 'a')).toBeNull();
  });
});

describe('evidenceSource', () => {
  it('sinyalin okunduğu yer', () => {
    expect(evidenceSource('trace_error_count', 'trace')).toBe("ClickHouse span'leri");
    expect(evidenceSource('op_latency', 'trace')).toBe('VictoriaMetrics · span RED metrikleri');
    // v0.10.2204 — trace olayı kaynağını kanıttan alır; VM değilse VM yazmaz.
    expect(evidenceSource('op_latency', 'trace', '{"source":"vm","route":"callBagService"}')).toBe('VictoriaMetrics · span RED metrikleri');
    expect(evidenceSource('trace_change_point', 'trace')).toBe("ClickHouse span'leri");
    expect(evidenceSource('x', 'trace', '{"source":"spanmetrics_1m"}')).toBe("ClickHouse span'leri");
    expect(evidenceSource('template_spike', 'log')).toBe('Elasticsearch');
    expect(evidenceSource('metric_jvm', 'metric', '{"source":"thanos","cluster":"prod-east"}')).toBe('Thanos · prod-east');
    expect(evidenceSource('metric_kafka', 'metric', '{"source":"vm"}')).toBe('VictoriaMetrics');
  });
  it('operasyon yalnız trace olayında', () => {
    expect(headerOperation(ev({ code: 'POST /pay' }))).toBe('POST /pay');
    expect(headerOperation(ev({ signal: 'log', code: 'x' }))).toBe('');
    expect(headerOperation(null)).toBe('');
  });
});

// v0.10.2371 — kayıtlı olay kartı Problem kapısının gerçek kararını gösterir.
describe('recordDecision', () => {
  const gd = { group: 'g1', wouldOpen: true, rule: 'anomaly-auto:group:e1', priority: 'P2', reason: 'tek güçlü sinyal (metric)', detectors: ['metric_kafka'], at: 20, live: true, problemId: 'p1', singleWouldOpen: true };
  it('grup yok / damga yok → boş (kart iddia etmez)', () => {
    expect(recordDecision(null)).toEqual({ chip: null, lines: [] });
    expect(recordDecision({ members: [{}], lead: {} })).toEqual({ chip: null, lines: [] });
  });
  it('canlı karar → Problem çipi (bağlantılı) + kapı satırı', () => {
    const d = recordDecision({ members: [{ groupShadow: { ...gd, at: 10, live: false, wouldOpen: false, reason: 'eski' } }], lead: { groupShadow: gd } }, 'tr');
    expect(d.chip?.href).toBe('/inbox?problem=p1');
    expect(d.lines).toHaveLength(1);
    expect(d.lines[0].v).toContain('tek güçlü sinyal (metric)');
  });
  it('açmazdı kararı → çip yok, gerekçe satırı var', () => {
    const d = recordDecision({ members: [{ groupShadow: { ...gd, live: false, wouldOpen: false, singleWouldOpen: false, problemId: undefined, reason: 'tek sinyal, 10 dk dolmadı' } }] }, 'tr');
    expect(d.chip).toBeNull();
    expect(d.lines[0].v).toContain('10 dk dolmadı');
  });
});
