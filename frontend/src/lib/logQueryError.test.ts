import { describe, it, expect } from 'vitest';
import { parseLogQueryError, logQueryErrorMessage } from './logQueryError';

// v0.10.1818 — 400 {error, pos, detail} gövdesinin istemci ayrıştırması.
describe('parseLogQueryError', () => {
  it('yapısal 400 → alanlar', () => {
    const e = new Error('HTTP 400: {"error":"Sorgu sözdizimi hatalı (17. karakter).","pos":16,"detail":"Cannot parse \'a (\'"}\n');
    expect(parseLogQueryError(e)).toEqual({ error: 'Sorgu sözdizimi hatalı (17. karakter).', pos: 16, detail: "Cannot parse 'a ('" });
  });
  it('raw yalnız varsa', () => {
    expect(parseLogQueryError(new Error('HTTP 400: {"error":"x","raw":"ES search 400"}'))).toEqual({ error: 'x', raw: 'ES search 400' });
  });
  it.each([
    ['500', new Error('HTTP 500: {"error":"boom"}')],
    ['JSON değil', new Error('HTTP 400: field required')],
    ['error alanı yok', new Error('HTTP 400: {"pos":1}')],
    ['Error değil', 'HTTP 400: {"error":"x"}'],
    ['iptal', new Error('request canceled')],
  ])('%s → null', (_n, e) => {
    expect(parseLogQueryError(e)).toBeNull();
  });
  it('geçersiz pos atılır', () => {
    expect(parseLogQueryError(new Error('HTTP 400: {"error":"x","pos":-1}'))).toEqual({ error: 'x' });
    expect(parseLogQueryError(new Error('HTTP 400: {"error":"x","pos":1.5}'))).toEqual({ error: 'x' });
  });
  it('mesaj: yapısalsa cümle, değilse yedek', () => {
    expect(logQueryErrorMessage(new Error('HTTP 400: {"error":"Sorgu hatalı"}'), 'genel')).toBe('Sorgu hatalı');
    expect(logQueryErrorMessage(new Error('HTTP 502: x'), 'genel')).toBe('genel');
  });
});
