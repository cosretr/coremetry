// @vitest-environment jsdom
//
// v0.10.2017 (Dashboards Faz 3 dilim 3.7 kalanı) — promql panelinin
// viz: 'table' | 'toplist' görünümleri (yeni panel tipi değil).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { PromqlTableView, PromqlToplistView, seriesRows, toplistRows } from './PromqlTableViews';
import { isBundleable } from '@/components/dashboard/bundlePlan';
import type { SpanMetricSeries } from '@/lib/types';

const s = (route: string, vals: number[]): SpanMetricSeries =>
  ({ groupKey: [`http_route="${route}"`], points: vals.map((value, i) => ({ time: i, value })) } as SpanMetricSeries);
const series = [s('/a', [1, 2, 3]), s('/b', [10, 4, 5]), s('/c', [7, 7, NaN])];

describe('seriesRows / toplistRows', () => {
  it('son / ortalama / en büyük; ad etiket değeri; NaN atılır', () => {
    const r = seriesRows(series);
    expect(r[0]).toMatchObject({ name: '/a', last: 3, avg: 2, max: 3 });
    expect(r[2]).toMatchObject({ name: '/c', last: 7, max: 7 });
  });
  it('sıralı liste ölçüte göre azalan ve limitli', () => {
    expect(toplistRows(seriesRows(series), 'last', 2).map(r => r.name)).toEqual(['/c', '/b']);
    expect(toplistRows(seriesRows(series), 'max', 1).map(r => r.name)).toEqual(['/b']);
  });
});

let root: Root | null = null;
afterEach(() => { if (root) act(() => root!.unmount()); root = null; document.body.innerHTML = ''; });
function render(node: React.ReactNode) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  act(() => { root = createRoot(host); root.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}

describe('görünümler', () => {
  it('tablo DataTable ile, satır başına seri', () => {
    const el = render(<PromqlTableView series={series} unit="ms" />);
    expect(el.querySelectorAll('table tbody tr').length).toBe(3);
    expect(el.textContent).toContain('/b');
  });
  it('sıralı liste en çok N satır; boşken nedeni yazar', () => {
    const el = render(<PromqlToplistView series={series} limit={2} />);
    expect(el.querySelectorAll('ol li').length).toBe(2);
    const empty = render(<PromqlToplistView series={[]} />);
    expect(empty.textContent).toContain('seri yok');
  });
  it('tablo / sıralı liste bundle dışında (sunucu top-N kendi isteğinde)', () => {
    const p = (viz?: 'table' | 'toplist') => ({ id: 'q', type: 'promql' as const, title: '', width: 2 as const, config: { query: 'up', ...(viz ? { viz } : {}) } });
    expect(isBundleable(p())).toBe(true);
    expect(isBundleable(p('table'))).toBe(false);
    expect(isBundleable(p('toplist'))).toBe(false);
  });
});
