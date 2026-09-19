// v0.10.796 — priorityRank saf yardımcı.
import { describe, expect, it } from 'vitest';
import { priorityRank } from './priorityRank';

describe('priorityRank', () => {
  it('P1 > P2 > P3 > yok', () => {
    expect(priorityRank('P1')).toBeGreaterThan(priorityRank('P2'));
    expect(priorityRank('P2')).toBeGreaterThan(priorityRank('P3'));
    expect(priorityRank('P3')).toBeGreaterThan(priorityRank(undefined));
    expect(priorityRank('bogus')).toBe(0);
  });
});
