// v0.10.1864 — sözcük çözümleyici, tamamlama bağlamı, Biçimlendir ve
// "Builder'a çevir" (Faz 2 dilim 2.4). Kabul: builder → kod HER ZAMAN
// (derleyici çıktısı ayrıştırıcıdan kayıpsız döner), kod → builder YALNIZ
// builder biçiminde (diğerleri nedenle reddedilir).
import { describe, expect, it } from 'vitest';
import { lex, completionContext, formatMql, lineColOf } from './lex';
import { parseBuilderForm } from './parse';
import { compileMxQuery } from './compile';
import { blankMxQuery, type MxQuery } from './model';

const M = 'http_server_request_duration_seconds_count';

describe('lex', () => {
  it('token metinleri girdiyi aynen kurar', () => {
    const src = 'WITH (f = {a!~"/h.*"})\nhistogram_quantile(0.95, sum by (le) (rate(x_bucket{f}[$__interval]))) # not';
    expect(lex(src).map(t => t.text).join('')).toBe(src);
  });
  it.each([
    ['sum by (a) (rate(x{k="v"}[5m]))', [['sum', 'metric'], ['by', 'keyword'], ['rate', 'func'], ['x', 'metric'], ['k', 'label'], ['"v"', 'string'], ['5m', 'duration']]],
    ['topk_avg(5, up)', [['topk_avg', 'func'], ['5', 'number'], ['up', 'metric']]],
    ['rate(x[$__rate_interval])', [['$__rate_interval', 'var']]],
  ])('%s', (src, want) => {
    const toks = lex(src);
    for (const [text, kind] of want) expect(toks.find(t => t.text === text)?.kind, text).toBe(kind);
  });
});

describe('completionContext', () => {
  it.each([
    ['rate(http_ser', 'metric-or-func', 'http_ser'],
    ['sum(ra', 'metric-or-func', 'ra'],
    ['up{job', 'label', 'job'],
    ['up{job="a", inst', 'label', 'inst'],
    ['up{job="ap', 'value', 'ap'],
    ['up{job=~"a', 'value', 'a'],
    ['"str', 'none', ''],
  ])('%s → %s', (src, kind, word) => {
    const c = completionContext(src, src.length);
    expect(c.kind).toBe(kind);
    if (c.kind !== 'none') expect(c.word.text).toBe(word);
    if (c.kind === 'label' || c.kind === 'value') expect(c.metric).toBe('up');
    if (c.kind === 'value') expect(c.label).toBe('job');
  });
});

describe('formatMql / lineColOf', () => {
  it.each([
    ['sum  by(a,b)(rate( x{ k = "v" , j!~"w" }[ 5m ] ))', 'sum by (a, b) (rate(x{k="v", j!~"w"}[5m]))'],
    ['a/b*100', 'a / b * 100'],
    ['clamp_min(x,-1) * -2', 'clamp_min(x, -1) * -2'],
    ['sum(rate(x[5m]))  # yorum', 'sum(rate(x[5m])) # yorum'],
  ])('%s', (src, want) => expect(formatMql(src)).toBe(want));
  it('satır/sütun', () => {
    expect(lineColOf('ab\ncd', 4)).toEqual({ line: 2, col: 2 });
    expect(lineColOf('abc', 0)).toEqual({ line: 1, col: 1 });
  });
});

describe('parseBuilderForm — builder → kod → builder kayıpsız', () => {
  const q = (p: Partial<MxQuery>): MxQuery => ({ ...blankMxQuery('A'), ...p });
  it.each<[string, MxQuery]>([
    ['rate sum by', q({ metric: M, fn: 'rate', agg: 'sum', by: ['service_name', 'http_route'], filters: [{ k: 'http_route', op: '!~', v: '/health.*' }] })],
    ['increase', q({ metric: M, fn: 'increase', agg: 'sum' })],
    ['irate max', q({ metric: M, fn: 'irate', agg: 'max', by: ['k8s_pod_name'] })],
    ['ham avg', q({ metric: 'jvm_memory_used_bytes', fn: 'raw', agg: 'avg' })],
    ['toplama yok', q({ metric: 'jvm_memory_used_bytes', fn: 'raw', agg: 'none' })],
    ['p95', q({ metric: 'x_bucket', fn: 'p95', agg: 'sum', by: ['service_name'] })],
    ['p50 tırnaklı etiket', q({ metric: 'x_bucket', fn: 'p50', agg: 'sum', filters: [{ k: 'service.name', op: '=', v: 'a"b' }] })],
    ['noktalı ad', q({ metric: 'http.server.duration', fn: 'raw', agg: 'count' })],
  ])('%s', (_n, want) => {
    const r = parseBuilderForm(compileMxQuery(want));
    expect(r.ok, JSON.stringify(r)).toBe(true);
    if (!r.ok) return;
    const { metricKind: _k, kindEstimated: _e, ...got } = r.query;
    const { metricKind: _k2, kindEstimated: _e2, ...exp } = want;
    expect(got).toEqual(exp);
    expect(r.notes).toEqual([]);
  });

  it('elle yazılan pencere çevrilir, not düşer', () => {
    const r = parseBuilderForm('sum(rate(up[5m]))');
    expect(r).toMatchObject({ ok: true, query: { metric: 'up', fn: 'rate', agg: 'sum' }, notes: ["Pencere [5m] builder'da otomatik pencereye döner"] });
  });
  it('sondaki by biçimi', () => {
    expect(parseBuilderForm('sum(rate(up[5m])) by (job)')).toMatchObject({ ok: true, query: { by: ['job'] } });
  });

  it.each([
    ['WITH (f = {a="b"}) sum(up{f})', 'WITH şablonu'],
    ['sum(rate(a[5m])) / sum(rate(b[5m]))', 'İkili işleç'],
    ['topk_avg(5, up)', "fonksiyon builder'da yok: topk_avg"],
    ['sum without (a) (up)', 'without'],
    ['rate(up[5m:1m])', 'alt sorgu'],
    ['up offset 5m', 'offset'],
    ['histogram_quantile(0.75, sum by (le) (rate(x_bucket[5m])))', 'yüzdelik yalnız'],
    ['', 'Sorgu boş'],
  ])('%s → red', (src, reason) => {
    const r = parseBuilderForm(src);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.reason).toContain(reason);
  });
});
