import { describe, it, expect } from 'vitest';
import { emptyUndo, isDashboardConflict, undoPop, undoPush } from './dashboardEdit';

// v0.10.2000 (Dashboards Faz 3 dilim 3.10) — oturum içi geri al + 409.
describe('undo yığını', () => {
  it('itilen hâl geri gelir, sıra LIFO', () => {
    let st = emptyUndo<number>();
    st = undoPush(st, 1, 'add');
    st = undoPush(st, 2, 'move');
    const a = undoPop(st)!;
    expect(a.state).toBe(2);
    expect(undoPop(a.stack)!.state).toBe(1);
    expect(undoPop(undoPop(a.stack)!.stack)).toBeNull();
  });
  it('aynı anahtarlı ardışık değişiklik tek adım (her tuş adım olmaz)', () => {
    let st = emptyUndo<string>();
    st = undoPush(st, 'a', 'panel:p1');
    st = undoPush(st, 'ab', 'panel:p1');
    st = undoPush(st, 'abc', 'panel:p1');
    expect(st.past).toEqual(['a']);
    st = undoPush(st, 'x', 'panel:p2');
    expect(st.past).toEqual(['a', 'x']);
  });
  it('null anahtar hep yeni adım; geri aldıktan sonra aynı anahtar yeni adım açar', () => {
    let st = emptyUndo<number>();
    st = undoPush(st, 1, null);
    st = undoPush(st, 2, null);
    expect(st.past).toEqual([1, 2]);
    st = undoPush(st, 3, 'k');
    st = undoPop(st)!.stack;
    st = undoPush(st, 4, 'k');
    expect(st.past).toEqual([1, 2, 4]);
  });
  it('tavan en eskiyi düşürür', () => {
    let st = emptyUndo<number>();
    for (let i = 0; i < 5; i++) st = undoPush(st, i, null, 3);
    expect(st.past).toEqual([2, 3, 4]);
  });
});

describe('isDashboardConflict', () => {
  it('yalnız 409', () => {
    expect(isDashboardConflict(new Error('HTTP 409: {"conflict":true}'))).toBe(true);
    expect(isDashboardConflict(new Error('HTTP 500: x'))).toBe(false);
    expect(isDashboardConflict('HTTP 409')).toBe(false);
  });
});
