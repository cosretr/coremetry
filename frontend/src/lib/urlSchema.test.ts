import { describe, it, expect } from 'vitest';
import { zParam, zInt, zOneOf, zText, urlSpec, readUrlParams, writeUrlParams } from './urlSchema';

const SPEC = urlSpec({
  id: zParam(zText(16), ''),
  at: zParam(zInt({ min: 0 }), 0),
  limit: zParam(zOneOf([100, 500, 1000] as const), 100),
});

const read = (qs: string) => readUrlParams(new URLSearchParams(qs), SPEC);

describe('urlSchema — zod-doğrulamalı URL codec', () => {
  it('geçerli değerler okunur', () => {
    expect(read('id=pod:a&at=1700000000000&limit=500')).toEqual({ id: 'pod:a', at: 1700000000000, limit: 500 });
  });

  it('yok / bozuk / sınır dışı → varsayılan', () => {
    expect(read('')).toEqual({ id: '', at: 0, limit: 100 });
    expect(read('at=abc&limit=777&id=')).toEqual({ id: '', at: 0, limit: 100 });
    expect(read('at=-5&limit=1e3')).toEqual({ id: '', at: 0, limit: 100 });
    expect(read('at=1.5')).toEqual({ id: '', at: 0, limit: 100 });
    expect(read(`id=${'x'.repeat(17)}`).id).toBe('');
    expect(read('id=%20%20ok%20').id).toBe('ok');
  });

  it('yazım: varsayılan siler, yabancılar korunur, geçersiz değer URL\'e yazılmaz', () => {
    const prev = new URLSearchParams('foo=1&limit=500&at=9');
    const next = writeUrlParams(prev, SPEC, { limit: 100, at: 42 });
    expect(next.get('foo')).toBe('1');
    expect(next.has('limit')).toBe(false);
    expect(next.get('at')).toBe('42');
    const bad = writeUrlParams(prev, SPEC, { limit: 777 as 100, at: -1 });
    expect(bad.has('limit')).toBe(false);
    expect(bad.has('at')).toBe(false);
  });

  it('gidiş-dönüş: encode → decode aynı değeri verir', () => {
    const next = writeUrlParams(new URLSearchParams(), SPEC, { id: 'svc:x', at: 5, limit: 1000 });
    expect(readUrlParams(next, SPEC)).toEqual({ id: 'svc:x', at: 5, limit: 1000 });
  });
});
