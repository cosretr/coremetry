// v0.10.523 — tek arama terimi: liste/şerit/sayım/RED aynı şeyi sorar.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { effectiveTraceSearch } from './traceSearchTerm';

describe('effectiveTraceSearch', () => {
  it('arama kutusu önce; kimlik kutusundaki 32-hex olmayan değer yedek; 32-hex id terim değil', () => {
    expect(effectiveTraceSearch({ search: ' POST /x ', traceId: 'abc' })).toBe('POST /x');
    expect(effectiveTraceSearch({ search: '', traceId: ' 0301010778 ' })).toBe('0301010778');
    expect(effectiveTraceSearch({ search: '', traceId: '0123456789abcdef0123456789abcdef' })).toBeUndefined();
    expect(effectiveTraceSearch({ search: '', traceId: '' })).toBeUndefined();
    expect(effectiveTraceSearch({})).toBeUndefined();
  });
  it('kaynak pini: Traces.tsx dört yüzeyde de effectiveTraceSearch kullanır, ham filter.search hiçbir isteğe gitmez', () => {
    const src = readFileSync(resolve(__dirname, '../pages/Traces.tsx'), 'utf8');
    // v0.10.1082 — liste (ve Errors şeridi) terimi ortak süzgeç yardımcısından
    // alır (pages/traces/scopeParams.ts); şerit / sayım / toplu doğrudan.
    expect((src.match(/search: effectiveTraceSearch\(filter\)/g) ?? []).length).toBeGreaterThanOrEqual(3);
    const scope = readFileSync(resolve(__dirname, '../pages/traces/scopeParams.ts'), 'utf8');
    expect(scope).toContain('search: effectiveTraceSearch(f)');
    expect((src.match(/\.\.\.traceScopeParams\(\{ filter, env, cluster: clusterScope/g) ?? []).length).toBe(1);
    expect(src).not.toMatch(/search: filter\.search \|\| /);
    expect(src).toContain("stripScope([...chartFilters, ...groupLeaves(grouped ? advGroup : null)], effectiveTraceSearch(filter) ?? '')");
    // şerit effect'i kimlik kutusunu da izler (v0.10.1082: tüm filter — süre /
    // services Errors şeridi uygunluğunu da belirler)
    expect(src).toMatch(/\[view, listRangeNs, filter, env, clusterScope, advFiltersEff, grouped/);
  });
});
