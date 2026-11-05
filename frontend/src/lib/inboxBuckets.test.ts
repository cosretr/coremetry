import { describe, expect, it } from 'vitest';
import { rowInProblemBucket, rowPassesKindFacet } from './inboxBuckets';
import type { InboxItem } from './types';

// v0.10.1803 — Go TestInboxRowInProblemBucket ile aynı tablo.
type Row = Pick<InboxItem, 'kind' | 'priority' | 'exception'>;
const ex = (priority: InboxItem['priority'], fingerprint?: string, kind: InboxItem['kind'] = 'exception'): Row => ({
  kind, priority,
  exception: fingerprint === undefined ? undefined : { fingerprint, type: 'ORA-00001', message: 'OP_ORDERS', occurrences: 40 },
});

describe('rowInProblemBucket', () => {
  it.each<[string, Row, boolean]>([
    ['Oracle P1', ex('P1', 'ora:0a1b2c3d'), true],
    ['Oracle P2', ex('P2', 'ora:0a1b2c3d'), false],
    ['Oracle P3', ex('P3', 'ora:0a1b2c3d'), false],
    ['span P1', ex('P1', 'a1b2c3d4e5f60718'), false],
    ['ref yok', ex('P1'), false],
    ['httperror', ex('P1', 'ora:0a1b2c3d', 'httperror'), false],
  ])('%s', (_n, row, want) => {
    expect(rowInProblemBucket(row)).toBe(want);
  });
});

describe('rowPassesKindFacet', () => {
  const problemsDefault = new Set(['problem', 'anomaly', 'incident']);
  it('Problems varsayılanı: yalnız Oracle P1 geçer', () => {
    expect(rowPassesKindFacet(ex('P1', 'ora:0a1b2c3d'), problemsDefault)).toBe(true);
    expect(rowPassesKindFacet(ex('P3', 'ora:0a1b2c3d'), problemsDefault)).toBe(false);
    expect(rowPassesKindFacet(ex('P1', 'a1b2c3d4e5f60718'), problemsDefault)).toBe(false);
  });
  it('problem kapalıyken kova devreye girmez', () => {
    expect(rowPassesKindFacet(ex('P1', 'ora:0a1b2c3d'), new Set(['anomaly']))).toBe(false);
  });
  it('Exceptions görünümü: Oracle P2/P3 hâlâ geçer', () => {
    const exc = new Set(['exception']);
    expect(rowPassesKindFacet(ex('P2', 'ora:0a1b2c3d'), exc)).toBe(true);
    expect(rowPassesKindFacet(ex('P3', 'ora:0a1b2c3d'), exc)).toBe(true);
  });
});
