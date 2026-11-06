// v0.10.1854 (Log Search Faz 2 dilim 2.3) — sabit haplar (`pins`) ve
// LogFilter ⇄ FilterChip köprüsü. Kabul: "sabit hap servis değişince kalır;
// URL'de".
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  parsePinsParam, encodePillParams, importPills, keepPinned, logFilterToChip, chipToLogFilter,
  compileSearch, LOG_FILTER_OPS, type LogFilter,
} from './logFilters';
import { logsUrlSig, writeLogsParams, readLogsUrlState, logsUrlHasFilterState, readLogsParams } from './logsUrl';

const lvl: LogFilter = { key: 'level', value: 'ERROR', negated: false, disabled: false, pinned: true };
const chan: LogFilter = { key: 'CHANNEL_CODE', value: 'WEB', negated: true, disabled: false };
const pod: LogFilter = { key: 'pod', value: 'orders-api-7d9f6-x2k4p', negated: false, disabled: true };

describe('sabit haplar — URL', () => {
  it('sabitler `pins`e, diğerleri `filters`e; gidiş-dönüş', () => {
    const { filtersRaw, pinsRaw } = encodePillParams([chan, lvl, pod]);
    expect(pinsRaw).toBe('[["level","ERROR",0,0]]');
    expect(filtersRaw).toBe('[["CHANNEL_CODE","WEB",1,0],["pod","orders-api-7d9f6-x2k4p",0,1]]');
    expect(parsePinsParam(pinsRaw)).toEqual([lvl]);
    expect(importPills(filtersRaw, pinsRaw, [])).toEqual([lvl, chan, pod]);
  });

  it('pins YOKSA mevcut sabitler kalır (servis değişimi / SPA gezinmesi)', () => {
    const urlPills = encodePillParams([chan]).filtersRaw;
    expect(importPills(urlPills, '', [lvl, pod])).toEqual([lvl, chan]);
    // pins varsa URL kazanır (paylaşılan link gördüğünü açar).
    const other: LogFilter = { key: 'svc', value: 'payments-mobile', negated: false, disabled: false, pinned: true };
    expect(importPills('', encodePillParams([other]).pinsRaw, [lvl])).toEqual([other]);
  });

  it('Tümünü temizle / sıfırla yalnız sabitleri bırakır', () => {
    expect(keepPinned([chan, lvl, pod])).toEqual([lvl]);
  });

  it('sabit hap sorguya derlenir (bayrak sorguyu değiştirmez)', () => {
    expect(compileSearch([lvl, chan], '')).toBe('level:"ERROR" AND NOT CHANNEL_CODE:"WEB"');
  });

  it('logsUrl: pins imzayı oynatır, yazılır/okunur; sabitsiz imza eskisi', () => {
    const f = readLogsParams(new URLSearchParams('service=orders-api'));
    expect(logsUrlSig(f, '', '')).toBe(logsUrlSig(f, '', '', ''));
    expect(logsUrlSig(f, '', '', '[["level","ERROR",0,0]]')).not.toBe(logsUrlSig(f, '', ''));
    const p = writeLogsParams(new URLSearchParams('range=1h'), f, '', '', '[["level","ERROR",0,0]]');
    expect(p.get('pins')).toBe('[["level","ERROR",0,0]]');
    const st = readLogsUrlState(p);
    expect(st.pinsRaw).toBe('[["level","ERROR",0,0]]');
    expect(st.sig).toBe(logsUrlSig(f, '', '', st.pinsRaw));
    expect(writeLogsParams(p, f, '', '').has('pins')).toBe(false);
    expect(logsUrlHasFilterState(new URLSearchParams('pins=[["a","b",0,0]]'))).toBe(true);
  });

  it('sayfa: içe aktarma importPills + mevcut sabitler; Tümünü temizle / sıfırla keepPinned', () => {
    const src = readFileSync(resolve(__dirname, '../pages/Logs.tsx'), 'utf8');
    expect(src).toContain('importPills(filtersRaw, pinsRaw, filtersRef.current)');
    expect(src).toContain('onClearPills={() => applyPills(keepPinned(filters))}');
    expect(src).toContain('const kept = keepPinned(filters);');
  });
});

describe('LogFilter ⇄ FilterChip', () => {
  const cases: [LogFilter, ReturnType<typeof logFilterToChip>][] = [
    [{ key: 'k', value: 'v', negated: false, disabled: false }, { key: 'k', op: '=', value: 'v' }],
    [{ key: 'k', value: 'v', negated: true, disabled: true }, { key: 'k', op: '!=', value: 'v', disabled: true }],
    [{ key: 'k', value: '', negated: false, disabled: false, exists: true }, { key: 'k', op: 'exists' }],
    [{ key: 'k', value: '', negated: true, disabled: false, exists: true, pinned: true }, { key: 'k', op: '!exists', pinned: true }],
    [{ key: 'k', value: '', negated: false, disabled: false, values: ['a', 'b'] }, { key: 'k', op: 'in', values: ['a', 'b'] }],
    [{ key: 'k', value: '', negated: true, disabled: false, values: ['a', 'b'] }, { key: 'k', op: '!in', values: ['a', 'b'] }],
    [{ key: 'k', value: '500', negated: false, disabled: false, op: 'gte' }, { key: 'k', op: '>=', value: '500' }],
    [{ key: 'k', value: '9', negated: false, disabled: false, op: 'lte' }, { key: 'k', op: '<=', value: '9' }],
  ];
  it.each(cases)('%o ⇄ %o', (lf, chip) => {
    expect(logFilterToChip(lf)).toEqual(chip);
    expect(chipToLogFilter(chip)).toEqual(lf);
  });
  it('tek değerli in → eşitlik; ~ eşitlik sayılır; işleç listesi LogFilter kapsamında', () => {
    expect(chipToLogFilter({ key: 'k', op: 'in', values: ['a'] })).toEqual({ key: 'k', value: 'a', negated: false, disabled: false });
    expect(chipToLogFilter({ key: 'k', op: '!~', value: 'a' })).toEqual({ key: 'k', value: 'a', negated: true, disabled: false });
    expect(LOG_FILTER_OPS).not.toContain('~');
  });
});
