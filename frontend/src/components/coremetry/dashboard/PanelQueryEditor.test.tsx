// @vitest-environment jsdom
//
// v0.10.2016 (Dashboards Faz 3 dilim 3.7 kalanı) — panel düzenleyicinin
// sorgu bölgesi Metrics Explorer'ın satırını kullanır; eski builder paneli
// açılınca config YAZILMAZ, histogram türü ad zarfından okunur.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Panel } from '@/lib/types';

vi.mock('@/lib/api', async (orig) => {
  const mod = await orig<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      metricNamesSearch: () => Promise.resolve({ names: [{ name: 'http_server_request_duration_seconds', type: 'histogram', unit: 's', description: '' }], total: 1, hasMore: false }),
      metricsCapabilities: () => Promise.resolve({ metricsql: true }),
    },
  };
});

import { PanelQueryEditor } from './PanelQueryEditor';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
afterEach(() => { if (root) act(() => root!.unmount()); root = null; document.body.innerHTML = ''; });
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

async function mount(panel: Panel, onQuery = vi.fn()) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root = createRoot(host);
    root.render(<QueryClientProvider client={qc}><MemoryRouter><PanelQueryEditor panel={panel} onQuery={onQuery} /></MemoryRouter></QueryClientProvider>);
  });
  await wait();
  return { host, onQuery };
}

describe('PanelQueryEditor', () => {
  it('builder biçimindeki promql paneli QueryRow ile açılır; açmak config yazmaz', async () => {
    const { host, onQuery } = await mount({ id: 'q', type: 'promql', title: '', width: 2, config: { query: 'sum by (service_name) (rate(x_total[5m]))' } });
    expect(host.textContent).toContain('x_total');
    expect(host.querySelector('[aria-label="Panel sorgusu (MetricsQL)"]')).toBeNull();
    expect(onQuery).not.toHaveBeenCalled();
  });

  it('builder biçimi olmayan metin kod kipinde; builder\'a geçiş nedeniyle reddedilir', async () => {
    const { host } = await mount({ id: 'q', type: 'promql', title: '', width: 2, config: { query: 'a / b' } });
    expect(host.querySelector('[aria-label="Panel sorgusu (MetricsQL)"]')).not.toBeNull();
    const builder = [...host.querySelectorAll('[role="radio"], button')].find(b => b.textContent?.trim() === 'Builder') as HTMLElement;
    await act(async () => { builder.click(); });
    expect(host.querySelector('[role="status"]')?.textContent ?? '').toMatch(/builder|Builder|İkili/);
  });

  it('eski builder metric paneli: histogram türü ad zarfından, avg → ortalama', async () => {
    const { host } = await mount({ id: 'm', type: 'metric', title: '', width: 2, config: { metricName: 'http_server_request_duration_seconds', agg: 'avg' } });
    await wait();
    expect(host.textContent).toContain('ortalama');
  });
});
