import { describe, it, expect } from 'vitest';
import { bundleRequestFor, isBundleable } from './bundlePlan';
import { MULTI } from '@/lib/dashboardVars';
import type { Panel } from '@/lib/types';

// v0.10.2003 (Dashboards Faz 3 dilim 3.6) — bundle planı.
const P = (id: string, type: Panel['type'], config: Panel['config'], extra: Partial<Panel> = {}): Panel =>
  ({ id, type, title: id, width: 2, config, ...extra });
const win = { from: 1_000_000_000_000, to: 1_000_003_600_000_000_000 / 1_000_000 };
const step = () => undefined;

describe('isBundleable', () => {
  it('metric / spanmetric / promql / stat / gauge girer; top-N, heatmap, pinli, override, markdown girmez', () => {
    expect(isBundleable(P('a', 'metric', { metricName: 'm' }))).toBe(true);
    expect(isBundleable(P('b', 'spanmetric', { agg: 'count' }))).toBe(true);
    expect(isBundleable(P('c', 'promql', { query: 'up' }))).toBe(true);
    expect(isBundleable(P('d', 'promql', { query: 'up', source: 'vm' }))).toBe(false);
    expect(isBundleable(P('e', 'stat', { source: 'metric', metric: { metricName: 'm' } }))).toBe(true);
    expect(isBundleable(P('f', 'stat', { source: 'spanmetric', span: { agg: 'count', groupBy: 'x' } }))).toBe(false);
    expect(isBundleable(P('g', 'gauge', { source: 'metric', metric: { metricName: 'm' } }))).toBe(true);
    expect(isBundleable(P('h', 'topn', { agg: 'count', groupBy: 'x' }))).toBe(false);
    expect(isBundleable(P('i', 'heatmap', { metricName: 'x' }))).toBe(false);
    expect(isBundleable(P('j', 'markdown', { text: '' }))).toBe(false);
    expect(isBundleable(P('k', 'metric', { metricName: 'm' }, { rangeOverride: { preset: '24h' } }))).toBe(false);
  });

  it('20 panelli tipik pano: grafik / tek değer panellerinin hepsi TEK bundle isteğinde', () => {
    const panels: Panel[] = [];
    for (let i = 0; i < 6; i++) panels.push(P(`m${i}`, 'metric', { metricName: 'm' }));
    for (let i = 0; i < 6; i++) panels.push(P(`s${i}`, 'spanmetric', { agg: 'p95' }));
    for (let i = 0; i < 4; i++) panels.push(P(`q${i}`, 'promql', { query: 'sum(rate(x[5m]))' }));
    for (let i = 0; i < 2; i++) panels.push(P(`t${i}`, 'stat', { source: 'metric', metric: { metricName: 'm', promql: 'up' } }));
    for (let i = 0; i < 2; i++) panels.push(P(`g${i}`, 'gauge', { source: 'spanmetric', span: { agg: 'error_rate' } }));
    expect(panels.filter(isBundleable).length).toBe(20);
  });
});

describe('bundleRequestFor', () => {
  it('promql: değişken (çoklu =~) + maxDataPoints kelepçeli', () => {
    const r = bundleRequestFor(P('q', 'promql', { query: 'up{env="${env}"}' }), { env: MULTI + 'a|b' }, step, 99999, win)!;
    expect(r).toMatchObject({ type: 'promql', query: 'up{env=~"a|b"}', maxDataPoints: 4000 });
  });
  it('stat iki kat pencere, gauge gövde penceresi', () => {
    const s = bundleRequestFor(P('s', 'stat', { source: 'metric', metric: { metricName: 'm' } }), {}, step, 400, win)!;
    expect(s).toMatchObject({ type: 'metric', name: 'm', from: win.from - (win.to - win.from), to: win.to });
    const g = bundleRequestFor(P('g', 'gauge', { source: 'metric', metric: { metricName: 'm', promql: 'up' } }), {}, step, 400, win)!;
    expect(g).toMatchObject({ type: 'promql', query: 'up' });
    expect(g.from).toBeUndefined();
  });
  it('PromQL kipindeki metric paneli promql slotu olur', () => {
    const r = bundleRequestFor(P('m', 'metric', { metricName: '', promql: 'sum(up{s="${s}"})' }), { s: 'api' }, () => 60, 600, win)!;
    expect(r).toMatchObject({ type: 'promql', query: 'sum(up{s="api"})', step: 60, maxDataPoints: 600 });
  });
});
