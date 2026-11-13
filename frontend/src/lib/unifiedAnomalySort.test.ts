// v0.10.2083 — /anomalies sunucu sıralaması kodeği (operatör 2026-11-13).
// Sözlük internal/ml/unified_sort.go UnifiedSortCols ile aynı kalmalı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { UNIFIED_SORT_IDS, parseUnifiedSort, unifiedSortApiParams } from './unifiedAnomalySort';

describe('unifiedAnomalySort', () => {
  it('URL → sıra; tanınmayan/bozuk → bölüm varsayılanı', () => {
    expect(parseUnifiedSort('open', null)).toEqual({ id: 'status', dir: 'desc' });
    expect(parseUnifiedSort('done', null)).toEqual({ id: 'started', dir: 'desc' });
    expect(parseUnifiedSort('open', 'service.asc')).toEqual({ id: 'service', dir: 'asc' });
    expect(parseUnifiedSort('open', 'headline.asc')).toEqual({ id: 'status', dir: 'desc' });
    expect(parseUnifiedSort('done', 'service.sideways')).toEqual({ id: 'started', dir: 'desc' });
  });

  it('varsayılan sıra API parametresi üretmez (RQ anahtarı + sunucu anahtarı aynı)', () => {
    expect(unifiedSortApiParams('open', { id: 'status', dir: 'desc' })).toEqual({});
    expect(unifiedSortApiParams('done', { id: 'started', dir: 'desc' })).toEqual({});
    expect(unifiedSortApiParams('open', { id: 'started', dir: 'desc' })).toEqual({ sort: 'started', dir: 'desc' });
    expect(unifiedSortApiParams('done', { id: 'status', dir: 'desc' })).toEqual({ sort: 'status', dir: 'desc' });
    expect(unifiedSortApiParams('open', { id: null, dir: 'desc' })).toEqual({});
  });

  it('kimlikler sunucu beyaz listesiyle aynı', () => {
    const go = readFileSync(resolve(__dirname, '../../../internal/ml/unified_sort.go'), 'utf8');
    const consts = Object.fromEntries([...go.matchAll(/(UnifiedSort\w+)\s+=\s+"(\w+)"/g)].map(m => [m[1], m[2]]));
    const list = go.match(/UnifiedSortCols = \[\]string\{([^}]+)\}/)![1].split(',').map(s => consts[s.trim()]);
    expect(list).toEqual([...UNIFIED_SORT_IDS]);
  });
});
