import { describe, expect, it } from 'vitest';
import { KAFKA_FILTER_SPEC } from './kafkaFilterSpec';
import { readUrlParams, writeUrlParams } from './urlSchema';

// v0.10.2467 — Service → Messaging süzgeç URL durumu (?ktopic yeni).
describe('KAFKA_FILTER_SPEC', () => {
  it('round-trip: topic + pod / client, yabancı paramlar korunur', () => {
    const prev = new URLSearchParams('tab=messaging&range=3h');
    const next = writeUrlParams(prev, KAFKA_FILTER_SPEC, { ktopic: 'payments-topic', khost: 'pod-1', kcons: 'c1' });
    expect(next.get('tab')).toBe('messaging');
    expect(readUrlParams(next, KAFKA_FILTER_SPEC)).toEqual({ khost: 'pod-1', kcons: 'c1', kprod: '', ktopic: 'payments-topic' });
  });
  it('Hepsi (boş) param siler; bozuk değer varsayılana düşer', () => {
    const sp = writeUrlParams(new URLSearchParams('ktopic=orders-topic'), KAFKA_FILTER_SPEC, { ktopic: '' });
    expect(sp.has('ktopic')).toBe(false);
    expect(readUrlParams(new URLSearchParams(`ktopic=${'x'.repeat(600)}`), KAFKA_FILTER_SPEC).ktopic).toBe('');
    expect(readUrlParams(new URLSearchParams('ktopic=%20%20'), KAFKA_FILTER_SPEC).ktopic).toBe('');
  });
});
