// v0.10.2023 — Exceptions listesi kolon sırası (operatör 2026-11-12: "filtre
// ve kolonlar yer değiştirebilir değil, traces'te gibi"). Kayıt yokken sıra
// tanım sırası olmalı (varsayılan görünüm değişmez); eski/bozuk kayıt
// tabloyu kolon kaybettirmemeli.
import { describe, expect, it } from 'vitest';
import { isDefaultExcOrder, parseStoredExcOrder, resolveExcOrder } from './exceptionColumns';

const DECL = ['priority', 'state', 'service', 'type', 'occurrences', 'firstSeen', 'lastSeen', 'assignee'];

describe('resolveExcOrder', () => {
  it('kayıt yok → tanım sırası (varsayılan görünüm aynı)', () => {
    expect(resolveExcOrder(DECL, null)).toEqual(DECL);
    expect(resolveExcOrder(DECL, [])).toEqual(DECL);
    expect(isDefaultExcOrder(DECL, resolveExcOrder(DECL, null))).toBe(true);
  });
  it('kayıtlı sıra korunur', () => {
    const saved = ['type', 'service', 'priority', 'state', 'occurrences', 'firstSeen', 'lastSeen', 'assignee'];
    expect(resolveExcOrder(DECL, saved)).toEqual(saved);
    expect(isDefaultExcOrder(DECL, saved)).toBe(false);
  });
  it('bilinmeyen id düşer, eksik kolon komşusunun arkasına girer', () => {
    const saved = ['type', 'gone', 'priority', 'state', 'service', 'occurrences', 'firstSeen', 'lastSeen'];
    const got = resolveExcOrder(DECL, saved);
    expect(got).not.toContain('gone');
    expect([...got].sort()).toEqual([...DECL].sort());
    expect(got.indexOf('assignee')).toBe(got.indexOf('lastSeen') + 1);
  });
  it('yinelenen id tek kez', () => {
    const got = resolveExcOrder(DECL, ['state', 'state', ...DECL]);
    expect(got.length).toBe(DECL.length);
    expect(got[0]).toBe('state');
  });
});

describe('parseStoredExcOrder', () => {
  it('bozuk / boş / dizi olmayan → null', () => {
    expect(parseStoredExcOrder(null)).toBeNull();
    expect(parseStoredExcOrder('{')).toBeNull();
    expect(parseStoredExcOrder('{"a":1}')).toBeNull();
    expect(parseStoredExcOrder('[]')).toBeNull();
  });
  it('dizgeleri alır', () => {
    expect(parseStoredExcOrder('["type",1,"","state"]')).toEqual(['type', 'state']);
  });
});
