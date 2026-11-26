// @vitest-environment jsdom
//
// v0.10.2480 — operatör: taze uç nokta olayında canlı grafik yalnız "kaynak
// okunamadı" diyordu. Her başarısızlık sınıfı kendi cümlesiyle çizilir; istek
// hatası yutulmaz; denenen sorgu + adım Teknik ayrıntılara (onQuery) gider;
// istek olay kanıtındaki aile + etiketleri taşır. Fikstür nötr.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { MLSourceSeries } from '@/lib/types';

const m = vi.hoisted(() => ({
  res: null as unknown,
  fail: null as string | null,
  params: [] as Record<string, unknown>[],
}));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'en' }));
vi.mock('@/lib/api', () => ({
  api: {
    mlSourceSeries: async (p: Record<string, unknown>) => {
      m.params.push(p);
      if (m.fail) throw new Error(m.fail);
      return m.res;
    },
  },
  isCanceled: () => false,
}));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => <div data-testid="chart" /> }));

import { MLSourceChart, type ChartQueryInfo } from './MLSourceChart';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_800_000_000;
const lead = {
  detector: 'op_latency', signal: 'trace', code: 'GET /api/v1/Orders/{id} "x"', service: 'orders', pod: '',
  startedAt: T0 * 1e9, lastSeen: (T0 + 300) * 1e9,
  evidence: JSON.stringify({ measure: 'p95', redFamily: 'http', svcLabel: 'service_name', routeLabel: 'http_route' }),
};
const expr = 'histogram_quantile(0.95, sum by (le) (rate(x_bucket{service_name="orders"}[300s]))) * 1000';
const failed = (reasonCode: string, detail?: string): MLSourceSeries =>
  ({ query: { expr, label: 'p95' }, stepSec: 60, series: [], reasonCode, detail, labels: 'event' });

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; m.fail = null; m.params = []; });

async function render() {
  const got: ChartQueryInfo[] = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter><MLSourceChart lead={lead as never} open={false} fallback={<p data-testid="fb">fb</p>} onQuery={q => got.push(q)} /></MemoryRouter>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 5)); });
  return { gap: host.querySelector('[data-testid="ml-source-gap"]')?.textContent ?? '', fb: !!host.querySelector('[data-testid="fb"]'), got };
}

describe('MLSourceChart — canlı grafik başarısızlık nedeni (v0.10.2480)', () => {
  it.each([
    ['timeout', 'the source query timed out'],
    ['query_error', 'the source rejected the query or returned an error'],
    ['empty', 'has no series in the event window'],
    ['no_vm', 'API pod serving this page'],
    ['no_expr', 'could not be built'],
  ])('%s', async (code, want) => {
    m.res = failed(code, code === 'query_error' ? '422: unknown function' : undefined);
    const out = await render();
    expect(out.gap).toContain(want);
    expect(out.gap).not.toContain('could not be read');
    expect(out.fb).toBe(true);
    if (code === 'query_error') expect(out.gap).toContain('422: unknown function');
    expect(out.got.at(-1)).toMatchObject({ expr, stepSec: 60, labels: 'event' });
    expect(out.got.at(-1)?.reason).toContain(want);
  });

  it('istek hatası kendi metniyle', async () => {
    m.fail = '502 Bad Gateway';
    const out = await render();
    expect(out.gap).toContain('the chart request failed (502 Bad Gateway)');
    expect(out.got.at(-1)?.reason).toContain('502 Bad Gateway');
  });

  it('istek olayın ailesi + etiketleriyle gider', async () => {
    m.res = failed('empty');
    await render();
    expect(m.params[0]).toMatchObject({ detector: 'op_latency', service: 'orders', code: lead.code, measure: 'p95',
      family: 'http', svcLabel: 'service_name', routeLabel: 'http_route' });
  });
});
