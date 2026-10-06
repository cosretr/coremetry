// filterQuery.test.ts — v0.10.264 tek satır sorgu kutusu çekirdeği sözleşmesi:
// op kısaltmaları (≠ ~ ∃), satır içi ayrıştırma (değer içindeki = korunur,
// IN listesi, EXISTS değersiz, boşluklu anahtar reddi), çip etiketi,
// upsert (aynı k+op günceller), son kullanılanlar (başa, tekrar düşer, ≤5,
// bozuk JSON → []), anahtar sıralaması (tam > önek > içerik > gözlem sayısı).
import { describe, it, expect } from 'vitest';
import {
  opFromShorthand, parseInlineFilter, splitListValues, chipValueLabel, upsertFilter,
  pushRecent, parseRecent, rankKeys, OP_SHORT, FILTER_OPS,
  chipDisplay, stmtShortId, STMT_HASH_FILTER_KEY,
  OP_GROUP_FILTER_KEY, TRACES_EXTRA_FILTER_KEYS, withExtraKeys,
} from './filterQuery';

describe('opFromShorthand', () => {
  it('kısaltmalar ve kelimeler', () => {
    expect(opFromShorthand('≠')).toBe('!=');
    expect(opFromShorthand('!=')).toBe('!=');
    expect(opFromShorthand('~')).toBe('LIKE');
    expect(opFromShorthand('!~')).toBe('NOT LIKE');
    expect(opFromShorthand('NOT   IN')).toBe('NOT IN');
    expect(opFromShorthand('exists')).toBe('EXISTS');
    expect(opFromShorthand('∄')).toBe('NOT EXISTS');
    expect(opFromShorthand('>=')).toBe('>=');
    expect(opFromShorthand('??')).toBeNull();
    for (const op of FILTER_OPS) expect(OP_SHORT[op]).toBeTruthy();
  });
});

describe('parseInlineFilter', () => {
  it('temel şekiller', () => {
    expect(parseInlineFilter('http.route=/api/v1/accounts/{id}/balance')).toEqual({ k: 'http.route', op: '=', v: ['/api/v1/accounts/{id}/balance'] });
    expect(parseInlineFilter('channel_code in MOBILE, WEB,MOBILE')).toEqual({ k: 'channel_code', op: 'IN', v: ['MOBILE', 'WEB'] });
    expect(parseInlineFilter('status_code != ok')).toEqual({ k: 'status_code', op: '!=', v: ['ok'] });
    expect(parseInlineFilter('duration >= 100')).toEqual({ k: 'duration', op: '>=', v: ['100'] });
    expect(parseInlineFilter('error.type exists')).toEqual({ k: 'error.type', op: 'EXISTS', v: [] });
    expect(parseInlineFilter('function_code ~ TRN')).toEqual({ k: 'function_code', op: 'LIKE', v: ['TRN'] });
  });
  it('değer içindeki = korunur; boşluklu anahtar / eksik değer / boş → null', () => {
    expect(parseInlineFilter('http.route=/x?a=b')).toEqual({ k: 'http.route', op: '=', v: ['/x?a=b'] });
    expect(parseInlineFilter('http route=/x')).toBeNull();
    expect(parseInlineFilter('http.route=')).toBeNull();
    expect(parseInlineFilter('TRN')).toBeNull();
    expect(parseInlineFilter('  ')).toBeNull();
  });
});

describe('chip / upsert / recent / rank', () => {
  it('chipValueLabel + splitListValues', () => {
    expect(chipValueLabel({ k: 'a', op: 'IN', v: ['x', 'y'] })).toBe('x, y');
    expect(chipValueLabel({ k: 'a', op: 'EXISTS', v: [] })).toBe('');
    expect(splitListValues(' a, ,b ,a')).toEqual(['a', 'b']);
  });
  // v0.10.1093 — ifade kimliği çipi: ham 20 hane yerine statement detayının
  // kısa kimliği; düzenleme metni (chipValueLabel) ham kalır.
  it('chipDisplay: db_stmt_hash → "statement #<id>", diğerleri aynen', () => {
    const f = { k: STMT_HASH_FILTER_KEY, op: '=' as const, v: ['12345678901234567890'] };
    expect(chipDisplay(f)).toEqual({ key: 'statement', op: '', value: '#12345678' });
    expect(chipValueLabel(f)).toBe('12345678901234567890');
    expect(chipDisplay({ k: STMT_HASH_FILTER_KEY, op: '!=', v: ['987654321'] }))
      .toEqual({ key: 'statement', op: '≠', value: '#98765432' });
    expect(chipDisplay({ k: STMT_HASH_FILTER_KEY, op: 'IN', v: ['111111111', '222222222'] }).value)
      .toBe('#11111111, #22222222');
    expect(chipDisplay({ k: 'http.route', op: '=', v: ['/x'] })).toEqual({ key: 'http.route', op: '=', value: '/x' });
    expect(stmtShortId('12345678901234567890')).toBe('12345678');
  });
  // v0.10.1115 — operasyon şekli çipi (Operations › Normalized → /traces):
  // anahtar insan diliyle, op ve değer olduğu gibi; düzenleme metni ham.
  it('chipDisplay: op_group → "operation shape", op/değer aynen', () => {
    const f = { k: OP_GROUP_FILTER_KEY, op: '=' as const, v: ['GET /orders/:id'] };
    expect(chipDisplay(f)).toEqual({ key: 'operation shape', op: '=', value: 'GET /orders/:id' });
    expect(chipValueLabel(f)).toBe('GET /orders/:id');
    expect(chipDisplay({ k: OP_GROUP_FILTER_KEY, op: 'NOT IN', v: ['GET /a/:id', 'POST /a'] }))
      .toEqual({ key: 'operation shape', op: 'NOT IN', value: 'GET /a/:id, POST /a' });
  });
  it('withExtraKeys: eksik ekleri sona koyar, kopya yok, boş ek → aynı dizi', () => {
    expect(TRACES_EXTRA_FILTER_KEYS).toContain('op_group');
    const keys = ['http.route', 'name'];
    expect(withExtraKeys(keys, TRACES_EXTRA_FILTER_KEYS)).toEqual(['http.route', 'name', 'op_group']);
    expect(withExtraKeys(['op_group', 'name'], TRACES_EXTRA_FILTER_KEYS)).toEqual(['op_group', 'name']);
    expect(withExtraKeys(keys, undefined)).toBe(keys);
    expect(rankKeys(withExtraKeys(keys, TRACES_EXTRA_FILTER_KEYS), 'op_')).toEqual(['op_group']);
  });
  it('upsertFilter aynı k+op günceller', () => {
    const out = upsertFilter([{ k: 'a', op: '=', v: ['1'] }], { k: 'a', op: '=', v: ['2'] });
    expect(out).toEqual([{ k: 'a', op: '=', v: ['2'] }]);
    expect(upsertFilter(out, { k: 'a', op: '!=', v: ['3'] }).length).toBe(2);
  });
  it('pushRecent başa alır, tekrar düşer, ≤5; parseRecent toleranslı', () => {
    let r = pushRecent([], { k: 'a', op: '=', v: ['1'] });
    r = pushRecent(r, { k: 'b', op: '=', v: ['2'] });
    r = pushRecent(r, { k: 'a', op: '=', v: ['1'] });
    expect(r.map(f => f.k)).toEqual(['a', 'b']);
    for (let i = 0; i < 10; i++) r = pushRecent(r, { k: `k${i}`, op: '=', v: ['v'] });
    expect(r.length).toBe(5);
    expect(parseRecent(JSON.stringify(r))).toEqual(r);
    expect(parseRecent('{bad')).toEqual([]);
    expect(parseRecent(JSON.stringify([{ k: 'a', op: 'BOGUS', v: [] }, { k: '', op: '=', v: [] }, { k: 'ok', op: '=', v: ['1', 2] }]))).toEqual([{ k: 'ok', op: '=', v: ['1'] }]);
  });
  it('rankKeys sırası', () => {
    const keys = ['http.route', 'http.method', 'channel_code', 'function_code', 'db.system'];
    expect(rankKeys(keys, 'http')).toEqual(['http.method', 'http.route']);
    expect(rankKeys(keys, 'http', { 'http.route': 100, 'http.method': 5 })).toEqual(['http.route', 'http.method']);
    expect(rankKeys(keys, 'code')).toEqual(['channel_code', 'function_code']);
    expect(rankKeys(keys, 'db.system')[0]).toBe('db.system');
    expect(rankKeys(keys, '', { 'db.system': 9 })[0]).toBe('db.system');
    expect(rankKeys(keys, 'zzz')).toEqual([]);
  });
});
