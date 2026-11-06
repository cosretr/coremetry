import { describe, it, expect } from 'vitest';
import { parseLogsSort, serializeLogsSort, logsSortClick, logsSortBadge, isSortableLogColumn } from './logsSort';

// v0.10.1833 — /logs çoklu alan sıralaması: URL sözleşmesi + başlık tıkı.
describe('logsSort', () => {
  it.each([
    ['', []],
    ['service', [{ field: 'service', desc: false }]],
    ['service:desc,level:asc', [{ field: 'service', desc: true }, { field: 'level', desc: false }]],
    ['a,b,c,d', [{ field: 'a', desc: false }, { field: 'b', desc: false }, { field: 'c', desc: false }]],
    ['service:sideways,level:desc', [{ field: 'level', desc: true }]],
    ['service,service:desc', [{ field: 'service', desc: false }]],
    ['bad field:asc', []],
  ])('parse %j', (raw, want) => {
    expect(parseLogsSort(raw)).toEqual(want);
  });

  it('gidiş-dönüş; boş → null (parametre silinir)', () => {
    const k = parseLogsSort('http.status_code:desc,service:asc');
    expect(serializeLogsSort(k)).toBe('http.status_code:desc,service:asc');
    expect(serializeLogsSort([])).toBeNull();
  });

  it('tık: X↑ → X↓ → yok; başka kolon birincili değiştirir', () => {
    let k = logsSortClick([], 'service', false);
    expect(k).toEqual([{ field: 'service', desc: false }]);
    k = logsSortClick(k, 'service', false);
    expect(k).toEqual([{ field: 'service', desc: true }]);
    expect(logsSortClick(k, 'service', false)).toEqual([]);
    expect(logsSortClick([{ field: 'service', desc: true }, { field: 'level', desc: false }], 'level', false))
      .toEqual([{ field: 'level', desc: false }]);
  });

  it('Shift+tık: ekle (≤3), ↑ → ↓ → kaldır', () => {
    let k = logsSortClick([{ field: 'service', desc: false }], 'level', true);
    expect(k).toEqual([{ field: 'service', desc: false }, { field: 'level', desc: false }]);
    k = logsSortClick(k, 'level', true);
    expect(k[1]).toEqual({ field: 'level', desc: true });
    expect(logsSortClick(k, 'level', true)).toEqual([{ field: 'service', desc: false }]);
    const full = parseLogsSort('a,b,c');
    expect(logsSortClick(full, 'd', true)).toEqual(full);
  });

  it('rozet ↓1 ↑2; tek anahtarda sayı yok; sıralanamaz kolonlar', () => {
    const k = parseLogsSort('service:desc,level:asc');
    expect(logsSortBadge(k, 'service')).toBe('↓1');
    expect(logsSortBadge(k, 'level')).toBe('↑2');
    expect(logsSortBadge(k, 'pod')).toBeNull();
    expect(logsSortBadge(parseLogsSort('pod:desc'), 'pod')).toBe('↓');
    expect(isSortableLogColumn('message')).toBe(false);
    expect(isSortableLogColumn('trace')).toBe(false);
    expect(isSortableLogColumn('http.status_code')).toBe(true);
  });
});
