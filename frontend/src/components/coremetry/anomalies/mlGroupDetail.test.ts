// v0.10.2022 — grup detay sayfası saf yardımcıları.
import { describe, it, expect } from 'vitest';
import type { MLEvent } from '@/lib/types';
import { evidenceSource, findGroup, headerOperation } from './mlGroupDetail';

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
    expect(evidenceSource('op_latency', 'trace')).toBe('VictoriaMetrics');
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
