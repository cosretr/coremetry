// @vitest-environment jsdom
// PodRuntimeSection — 2026-11-21: pod sayfasında runtime sinyalleri yalnız
// varlık katmanının bu pod'a çözdüğü sinyaller; grafikler pod'a süzülü.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ filters: [] as string[] }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceRuntimeSignals: async () => ({
        service: 'svc-a', mode: 'shadow', pods: 2, atNs: 1,
        profile: { runtime: 'dotnet', signals: [
          { signal: 'heap_used', metric: 'process_runtime_dotnet_gc_heap_size_bytes', kind: 'gauge', agg: 'avg', scale: 1, genLabel: 'generation' },
          { signal: 'pool_queue', metric: 'process_runtime_dotnet_thread_pool_queue_length', kind: 'gauge', agg: 'avg', scale: 1 },
        ] },
        signals: [
          { check: 'memory-leak', level: 'warning', service: 'svc-a', pod: 'svc-a-pod-1', pods: 1, value: 900, baseline: 400, threshold: 15, unit: 'MB', rising: true, summary: 'x',
            podEntity: { id: 'pod:cl-a/ns-a/svc-a-pod-1', clusterId: 'cl-a', namespace: 'ns-a', pod: 'svc-a-pod-1' } },
          { check: 'gc-pressure', level: 'warning', service: 'svc-a', pod: 'svc-a-pod-2', pods: 1, value: 20, baseline: 5, threshold: 10, unit: '%', rising: true, summary: 'y',
            podEntity: { id: 'pod:cl-a/ns-a/svc-a-pod-2', clusterId: 'cl-a', namespace: 'ns-a', pod: 'svc-a-pod-2' } },
        ],
      }),
      metricQuery: async (q: { filters?: string }) => {
        m.filters.push(q.filters ?? '');
        return [{ groupKey: ['gen2'], points: [{ time: 1e9, value: 1 }] }];
      },
    },
  };
});
vi.mock('@/components/viz/TimeSeriesPanel', () => ({ TimeSeriesPanel: () => <div data-testid="tsp" /> }));

import PodRuntimeSection from './PodRuntimeSection';

let root: Root | null = null;
let host: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); root = null; host.remove(); });

describe('PodRuntimeSection', () => {
  it('shows only this pod\'s entity-resolved signals and pod-filtered charts', async () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      root = createRoot(host);
      root.render(<QueryClientProvider client={qc}><MemoryRouter><PodRuntimeSection service="svc-a" pod="svc-a-pod-1" from={0} to={3600e9} /></MemoryRouter></QueryClientProvider>);
    });
    for (let i = 0; i < 6; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(host.querySelector('[data-testid="rt-sig-memory-leak"]')).not.toBeNull();
    expect(host.querySelector('[data-testid="rt-sig-gc-pressure"]')).toBeNull(); // başka pod
    expect(host.querySelector('[data-testid="pod-rt-heap_used"]')).not.toBeNull();
    expect(host.querySelector('[data-testid="pod-rt-pool_queue"]')).not.toBeNull();
    expect(m.filters.every(f => f.includes('"resource.host.name"') && f.includes('svc-a-pod-1'))).toBe(true);
    const link = host.querySelector('[data-testid="rt-sig-memory-leak"] a');
    expect(link?.getAttribute('href')).toContain('pod=svc-a-pod-1');
  });
});
