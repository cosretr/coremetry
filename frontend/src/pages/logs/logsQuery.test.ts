// v0.10.1498 — sorgu çubuğunun saf kararları (Search ⇄ ↻ birleşik düğme,
// çıplak trace id önerisi).
import { describe, it, expect } from 'vitest';
import { isDraftDirty, bareTraceIdOf } from './logsQuery';
import type { LogsUrlFilter } from '@/lib/logsUrl';

const base: LogsUrlFilter = {
  service: '', cluster: '', search: '', severity: 0, traceId: '', spanId: '',
  hasTrace: false, pattern: '', team: '',
};

describe('isDraftDirty', () => {
  it('aynı taslak temiz (düğme ↻)', () => {
    expect(isDraftDirty(base, { ...base })).toBe(false);
  });
  it.each([
    ['service', { service: 'orders-api' }],
    ['cluster', { cluster: 'kxzone' }],
    ['search', { search: 'level:error' }],
    ['severity', { severity: 17 }],
    ['traceId', { traceId: 'abc' }],
    ['spanId', { spanId: 'def' }],
    ['hasTrace', { hasTrace: true }],
    ['pattern', { pattern: 'Disk full' }],
    ['team', { team: 'orders-team' }],
  ] as const)('%s farkı kirli (düğme Search)', (_k, patch) => {
    expect(isDraftDirty({ ...base, ...patch }, base)).toBe(true);
  });
});

describe('bareTraceIdOf', () => {
  const id = '0af7651916cd43dd8448eb211c80319c';
  it('çıplak 32-hex → küçük harf id', () => {
    expect(bareTraceIdOf(id)).toBe(id);
    expect(bareTraceIdOf(`  ${id.toUpperCase()} `)).toBe(id);
    expect(bareTraceIdOf(`0x${id}`)).toBe(id);
  });
  it('v0.10.1890 — tireli UUID → tiresiz 32-hex; bozuk UUID boş', () => {
    const uuid = `${id.slice(0, 8)}-${id.slice(8, 12)}-${id.slice(12, 16)}-${id.slice(16, 20)}-${id.slice(20)}`;
    expect(bareTraceIdOf(uuid.toUpperCase())).toBe(id);
    expect(bareTraceIdOf(uuid.replace('-', ''))).toBe('');
    expect(bareTraceIdOf(`${uuid} x`)).toBe('');
  });
  it('KQL, kısa hex, fazladan metin → boş', () => {
    expect(bareTraceIdOf(`trace.id:${id}`)).toBe('');
    expect(bareTraceIdOf(id.slice(0, 16))).toBe('');
    expect(bareTraceIdOf(`${id} error`)).toBe('');
    expect(bareTraceIdOf('')).toBe('');
  });
});
