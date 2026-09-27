import { describe, it, expect } from 'vitest';
import { treeNav, type TreeNavRow, type TreeNavResult } from './treeNav';

// v0.10.968 — Trace › Metrics treegrid klavyesi (saf): (satırlar, indeks, tuş)
// → odak / aç-kapa / etkinleştir. Kenarlar dahil.
const ROWS: TreeNavRow[] = [
  { kind: 'group', level: 1, open: true },   // 0 fraud-score-prod (açık)
  { kind: 'pod', level: 2 },                 // 1
  { kind: 'more', level: 2 },                // 2 "N pod daha"
  { kind: 'group', level: 1, open: false },  // 3 audit-log-prod (kapalı)
  { kind: 'nopod', level: 1, open: true },   // 4 pod'suz span'lar (açık)
  { kind: 'nopod-svc', level: 2 },           // 5
];

describe('treeNav', () => {
  const cases: [string, number, string, TreeNavResult][] = [
    ['↓', 0, 'ArrowDown', { kind: 'focus', index: 1 }],
    ['↓ son satırda kalır', 5, 'ArrowDown', { kind: 'focus', index: 5 }],
    ['↑', 3, 'ArrowUp', { kind: 'focus', index: 2 }],
    ['↑ ilk satırda kalır', 0, 'ArrowUp', { kind: 'focus', index: 0 }],
    ['Home', 4, 'Home', { kind: 'focus', index: 0 }],
    ['End', 1, 'End', { kind: 'focus', index: 5 }],
    ['→ kapalı grubu açar', 3, 'ArrowRight', { kind: 'toggle', index: 3, open: true }],
    ['→ açık gruptan ilk çocuğa', 0, 'ArrowRight', { kind: 'focus', index: 1 }],
    ['→ pod\'da yerinde', 1, 'ArrowRight', { kind: 'focus', index: 1 }],
    ['← açık grubu kapatır', 0, 'ArrowLeft', { kind: 'toggle', index: 0, open: false }],
    ['← pod\'dan grubuna', 1, 'ArrowLeft', { kind: 'focus', index: 0 }],
    ['← "daha" satırından grubuna', 2, 'ArrowLeft', { kind: 'focus', index: 0 }],
    ['← pod\'suz çocuktan başlığına', 5, 'ArrowLeft', { kind: 'focus', index: 4 }],
    ['← kapalı düzey-1 grupta yerinde', 3, 'ArrowLeft', { kind: 'focus', index: 3 }],
    ['Enter pod\'u seçer', 1, 'Enter', { kind: 'activate', index: 1 }],
    ['Boşluk grubu aç/kapa', 0, ' ', { kind: 'toggle', index: 0, open: false }],
    ['Enter kapalı grubu açar', 3, 'Enter', { kind: 'toggle', index: 3, open: true }],
    ['Enter "daha"yı genişletir', 2, 'Enter', { kind: 'activate', index: 2 }],
    ['Enter pod\'suz çocukta hiçbir şey', 5, 'Enter', null],
    ['başka tuş bizim değil', 1, 'x', null],
    ['Escape bizim değil (kabuğun katmanı)', 1, 'Escape', null],
  ];
  it.each(cases)('%s', (_n, i, key, want) => {
    expect(treeNav(ROWS, i, key)).toEqual(want);
  });
  it('boş tablo → null; taşan indeks kıstırılır', () => {
    expect(treeNav([], 0, 'ArrowDown')).toBeNull();
    expect(treeNav(ROWS, 99, 'ArrowUp')).toEqual({ kind: 'focus', index: 4 });
  });
  it('açık grubun çocuğu yoksa → yerinde', () => {
    expect(treeNav([{ kind: 'group', level: 1, open: true }], 0, 'ArrowRight')).toEqual({ kind: 'focus', index: 0 });
  });
});
