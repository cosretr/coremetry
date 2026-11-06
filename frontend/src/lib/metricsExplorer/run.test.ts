// v0.10.1863 — çalıştırma planı + hata gövdesi (Faz 2 dilim 2.3).
import { describe, expect, it } from 'vitest';
import { planRun, parseRunError, runErrorTitle, effectiveStepSec, queryUnit } from './run';
import { blankMxQuery, defaultMxModel, type MxModel } from './model';

const M = 'http_server_request_duration_seconds_count';

describe('planRun', () => {
  const model: MxModel = {
    ...defaultMxModel(),
    queries: [
      { ...blankMxQuery('A'), metric: M, fn: 'rate', agg: 'sum', by: ['service_name'] },
      { ...blankMxQuery('B'), hidden: true, metric: M, fn: 'rate', agg: 'sum', by: ['service_name'], filters: [{ k: 'code', op: '=~', v: '5..' }] },
      { ...blankMxQuery('C') },
      { ...blankMxQuery('F1'), kind: 'formula', expr: 'B / A * 100', unit: 'percent', legend: 'hata %' },
      { ...blankMxQuery('F2'), kind: 'formula', expr: 'Z' },
    ],
  };
  it('gizli satır çalışmaz ama formüle girer; boş metrik atlanır; formül hatası ayrı', () => {
    const p = planRun(model, 3600);
    expect(p.items.map(i => i.id)).toEqual(['A', 'F1']);
    expect(p.items[0].query).toBe(`sum by (service_name) (rate(${M}[300s]))`);
    expect(p.items[0].text).toContain('$__rate_interval');
    expect(p.items[1]).toMatchObject({ kind: 'formula', refs: ['B', 'A'], unit: 'percent', by: ['service_name'], legend: 'hata %' });
    expect(p.errors).toEqual([{ id: 'F2', error: 'Bilinmeyen sorgu: Z' }]);
    expect(p).toMatchObject({ stepSec: 15, limit: 8, rank: 'avg', src: '' });
  });
  it('kod kipi: tek öğe, yer tutucu çözülür, kaynak taşınır', () => {
    const p = planRun({ ...defaultMxModel(), mode: 'code', code: 'rate(up[$__interval])', src: 'vm', step: 60 }, 3600);
    expect(p.items).toEqual([{ id: 'code', kind: 'code', text: 'rate(up[$__interval])', query: 'rate(up[60s])', legend: '', by: [] }]);
    expect(p.src).toBe('vm');
  });
  it('boş kod → öğe yok', () => {
    expect(planRun({ ...defaultMxModel(), mode: 'code', code: '  ' }, 3600).items).toEqual([]);
  });
  it.each([[0, 3600, 15], [0, 6 * 3600, 120], [0, 86400, 600], [60, 86400, 60]])('adım %i, aralık %i → %i', (step, range, want) => {
    expect(effectiveStepSec({ step }, range)).toBe(want);
  });
  it('birim: hızda birimsiz, yüzdelikte metriğin birimi', () => {
    expect(queryUnit({ ...blankMxQuery('A'), fn: 'rate', unit: '1' })).toBeUndefined();
    expect(queryUnit({ ...blankMxQuery('A'), fn: 'p95', unit: 's' })).toBe('s');
    expect(queryUnit({ ...blankMxQuery('A'), fn: 'raw', unit: 'By' })).toBe('By');
  });
});

describe('parseRunError', () => {
  it.each([
    ['400 konumlu', new Error('HTTP 400: {"error":"cannot parse","pos":8,"line":1,"col":9,"detail":"unparsed data left"}'),
      { status: 400, message: 'cannot parse', pos: 8, line: 1, col: 9, detail: 'unparsed data left' }, 'Sorgu hatası'],
    ['502 düz', new Error('HTTP 502: {"error":"upstream metrics backend: dial tcp"}'),
      { status: 502, message: 'upstream metrics backend: dial tcp' }, 'VictoriaMetrics cevap vermedi'],
    ['metin', new Error('network down'), { message: 'network down' }, 'Sorgu çalıştırılamadı'],
  ])('%s', (_n, e, want, title) => {
    const r = parseRunError(e);
    expect(r).toEqual(want);
    expect(runErrorTitle(r, 'VictoriaMetrics')).toBe(title);
  });
});
