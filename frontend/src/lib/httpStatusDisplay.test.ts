// v0.10.1519 regresyon (operator-reported, prod): /traces http.status_code
// kolonu durumsuz span'lerde (uzun timeout'lu çağrılar, Kafka producer) "0"
// yazıyordu. Gösterim "—" + "yanıt yok / HTTP dışı span"; süzgeç değeri "0".
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { httpStatusCell, httpStatusGroupLabel, isHttpStatusKey, isMissingHttpStatus, NO_HTTP_STATUS_TITLE } from './httpStatusDisplay';

describe('httpStatusDisplay — saf', () => {
  it.each([
    ['http.status_code', '0', '—', NO_HTTP_STATUS_TITLE],
    ['http.status_code', '', '—', NO_HTTP_STATUS_TITLE],
    ['http.status_code', undefined, '—', NO_HTTP_STATUS_TITLE],
    ['http.response.status_code', '0', '—', NO_HTTP_STATUS_TITLE],
    ['http.status_code', '200', '200', '200'],
    ['http.status_code', '504', '504', '504'],
    // HTTP durumu dışındaki anahtarlarda "0" gerçek bir değerdir.
    ['retry.count', '0', '0', '0'],
    ['http.method', '', '—', ''],
  ] as const)('%s=%s → %s', (k, v, text, title) => {
    const c = httpStatusCell(k, v);
    expect(c.text).toBe(text);
    expect(c.title).toBe(title);
  });
  it('anahtar ve "yok" yüklemleri', () => {
    expect(isHttpStatusKey('http.status_code')).toBe(true);
    expect(isHttpStatusKey('http.response.status_code')).toBe(true);
    expect(isHttpStatusKey('http.route')).toBe(false);
    expect(isMissingHttpStatus(' 0 ')).toBe(true);
    expect(isMissingHttpStatus('200')).toBe(false);
  });
  it('Aggregated grup: "0" → "—" ve ipucu süzgeç anlamını söyler', () => {
    expect(httpStatusGroupLabel('0')).toEqual({ text: '—', title: `${NO_HTTP_STATUS_TITLE} (http.status_code = 0)` });
    expect(httpStatusGroupLabel('404')).toEqual({ text: '404' });
  });
  it('tablo, Aggregated ve süzgeç önerisi yardımcıyı kullanır; süzgeç değeri "0" aynen', () => {
    const traces = readFileSync(resolve(__dirname, '../pages/Traces.tsx'), 'utf8');
    expect(traces).toContain('const c = httpStatusCell(id, t.extras?.[id], lang);');
    expect(traces).toContain("groupBy === 'http_status'");
    expect(traces).toContain('httpStatusGroupLabel(a.groupKey)');
    const fq = readFileSync(resolve(__dirname, '../components/FilterQueryBox.tsx'), 'utf8');
    expect(fq).toContain("isHttpStatusKey(draft.k) && r.value === '0'");
    // commit yolu değişmedi: seçilen değer r.value (yani "0").
    expect(fq).toContain('v: isList ? splitListValues(r.value) : [r.value]');
  });
});
