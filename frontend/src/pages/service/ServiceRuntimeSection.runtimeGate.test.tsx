// @vitest-environment jsdom
// ServiceRuntimeSection.runtimeGate — 2026-11-21 (operatör: ".NET servis JVM
// metrikleri göstermemeli — Elastic APM bunu iyi yapıyor").
//
// NE ÇİVİLİYOR: runtime tespiti HER runtime yüzeyini sürer — .NET serviste
// sıfır JVM bileşeni (RuntimeCharts JVM kartları, heap bandı) ve DotnetRuntimePanel
// (sinyaller + profilden çözülen kartlar); JVM serviste .NET paneli yok ve
// runtime-signals profili hiç istenmez; dil raporlamayan servis sunucunun
// katalog tespitine göre çizilir; Node → yalnız genel pod kaynakları.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { RuntimeSignalReport } from '@/lib/types';

const m = vi.hoisted(() => ({ language: 'dotnet' as string | undefined, profileRuntime: 'dotnet', profileCalls: 0, metricNames: [] as string[] }));

vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceRuntime: async () => ({ service: 'svc-a', language: m.language }),
      serviceRuntimeSignals: async (): Promise<RuntimeSignalReport> => {
        m.profileCalls++;
        const dotnet = m.profileRuntime === 'dotnet';
        return {
          service: 'svc-a', mode: 'shadow', pods: 2, atNs: 1,
          profile: {
            runtime: m.profileRuntime as RuntimeSignalReport['profile']['runtime'],
            dotnetGen: dotnet ? 'instrumentation' : undefined,
            signals: dotnet ? [
              { signal: 'gc_time', metric: 'process_runtime_dotnet_gc_duration_nanoseconds_total', kind: 'counter', agg: 'rate', scale: 1e-9 },
              { signal: 'pool_queue', metric: 'process_runtime_dotnet_thread_pool_queue_length', kind: 'gauge', agg: 'avg', scale: 1 },
              { signal: 'heap_used', metric: 'process_runtime_dotnet_gc_heap_size_bytes', kind: 'gauge', agg: 'avg', scale: 1, genLabel: 'generation' },
            ] : m.profileRuntime === 'python' ? [
              { signal: 'cpu_util', metric: 'process_runtime_cpython_cpu_utilization_ratio', kind: 'gauge', agg: 'avg', scale: 1 },
              { signal: 'gc_collections', metric: 'process_runtime_cpython_gc_count_bytes_total', kind: 'counter', agg: 'rate', scale: 1, genLabel: 'count' },
            ] : m.profileRuntime === 'jvm' ? [
              { signal: 'heap_used', metric: 'jvm_memory_used_bytes', kind: 'gauge', agg: 'avg', scale: 1 },
            ] : [],
          },
          signals: dotnet ? [{
            check: 'threadpool-starvation', level: 'critical', service: 'svc-a', pod: 'pod-1', pods: 2,
            value: 150, baseline: 2, threshold: 100, unit: 'items', rising: true, extra: { completedRatio: 1.01 }, summary: 'x',
          }] : [],
        };
      },
      metricQuery: async (q: { name: string }) => {
        m.metricNames.push(q.name);
        return [{ groupKey: ['pod-1', '', '', ''], points: [{ time: 1e9, value: 1 }, { time: 2e9, value: 2 }] }];
      },
      spanMetricBatch: async () => ({ stepSeconds: 60, series: { p99: [{ groupKey: [], points: [{ time: 1e9, value: 20 }] }] } }),
    },
  };
});
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useServiceDeploys: () => ({ data: [] }) };
});
vi.mock('@/components/viz/TimeSeriesPanel', () => ({ TimeSeriesPanel: () => <div data-testid="tsp" /> }));
vi.mock('./RuntimeCharts', () => ({ RuntimeCharts: (p: { family: string }) => <div data-testid={`runtime-charts-${p.family}`}>JVM heap</div> }));
vi.mock('./HeapBaselineCard', () => ({ HeapBaselineCard: () => <div data-testid="heap-band">JVM heap band</div> }));
vi.mock('./PodResourceCharts', () => ({ PodResourceCharts: () => <div data-testid="pod-resources" /> }));

import { ServiceRuntimeSection, runtimeSurfaces, hasOwnRuntimeMetrics } from './ServiceRuntimeSection';

const th = { from: 0, to: 3600e9, clustersWithPods: [], effNs: '', effDeploy: '', cFrom: 0, cTo: 3600, clamped: false };

let root: Root | null = null;
let host: HTMLDivElement;

async function render() {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root = createRoot(host);
    root.render(<QueryClientProvider client={qc}><MemoryRouter><ServiceRuntimeSection service="svc-a" th={th} /></MemoryRouter></QueryClientProvider>);
  });
  // Lazy chunk (DotnetRuntimePanel) gerçek import süresi ister.
  const settled = () => !host.querySelector('.spinner') && !!host.querySelector('[data-testid$="-runtime"], [data-testid^="runtime-charts"], [data-testid="pod-resources"]');
  for (let i = 0; i < 80 && !settled(); i++) await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  for (let i = 0; i < 4; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

beforeEach(() => { m.profileCalls = 0; m.metricNames = []; });
afterEach(() => { act(() => root?.unmount()); root = null; host.remove(); });

const q = (id: string) => host.querySelector(`[data-testid="${id}"]`);

describe('runtime gate', () => {
  it('.NET service: zero JVM components, .NET panel with signals and resolved names', async () => {
    m.language = 'dotnet'; m.profileRuntime = 'dotnet';
    await render();
    expect(q('runtime-charts-jvm')).toBeNull();
    expect(q('heap-band')).toBeNull();
    expect(q('pod-resources')).toBeNull();
    expect(q('dotnet-runtime')).not.toBeNull();
    expect(q('rt-sig-threadpool-starvation')).not.toBeNull();
    expect(q('rt-card-gc')).not.toBeNull();
    expect(q('rt-card-pool')).not.toBeNull();
    expect(q('rt-card-lock')).toBeNull(); // profilde yok → kart yok
    expect(host.textContent).not.toMatch(/JVM/);
    expect(m.metricNames).toContain('process_runtime_dotnet_gc_duration_nanoseconds_total');
    expect(m.metricNames.some(n => n.startsWith('jvm'))).toBe(false);
  });

  it('JVM service: JVM charts + heap band, no .NET panel', async () => {
    m.language = 'java'; m.profileRuntime = 'jvm';
    await render();
    expect(q('runtime-charts-jvm')).not.toBeNull();
    expect(q('heap-band')).not.toBeNull();
    expect(q('dotnet-runtime')).toBeNull();
    expect(m.metricNames.some(n => n.startsWith('process_runtime_dotnet'))).toBe(false);
    expect(host.textContent).not.toMatch(/\.NET|dotnet/);
  });

  it('no language: server catalogue detection drives the view', async () => {
    m.language = undefined; m.profileRuntime = 'dotnet';
    await render();
    expect(q('dotnet-runtime')).not.toBeNull();
    expect(q('runtime-charts-jvm')).toBeNull();
  });

  it('unknown runtime: only generic pod resources, never an empty JVM section', async () => {
    m.language = undefined; m.profileRuntime = '';
    await render();
    expect(q('pod-resources')).not.toBeNull();
    expect(q('runtime-charts-jvm')).toBeNull();
    expect(q('heap-band')).toBeNull();
  });

  it('Python service: CPython panel, no JVM / .NET components', async () => {
    m.language = 'python'; m.profileRuntime = 'python';
    await render();
    expect(q('python-runtime')).not.toBeNull();
    expect(q('rt-card-cpu')).not.toBeNull();
    expect(q('rt-card-gcgen')).not.toBeNull();
    expect(q('runtime-charts-jvm')).toBeNull();
    expect(q('heap-band')).toBeNull();
    expect(q('dotnet-runtime')).toBeNull();
    expect(q('pod-resources')).toBeNull();
    expect(host.textContent).not.toMatch(/JVM/);
  });
});

describe('runtimeSurfaces', () => {
  it('maps each family to its own surfaces only', () => {
    expect(runtimeSurfaces('dotnet')).toEqual({ jvmCharts: false, heapBand: false, dotnet: true, goCharts: false, python: false, podResources: false });
    expect(runtimeSurfaces('jvm')).toEqual({ jvmCharts: true, heapBand: true, dotnet: false, goCharts: false, python: false, podResources: false });
    expect(runtimeSurfaces('python', false)).toMatchObject({ python: false, podResources: true });
    expect(hasOwnRuntimeMetrics([{ signal: 'fd_open' }])).toBe(false);
    expect(hasOwnRuntimeMetrics([{ signal: 'cpu_util' }])).toBe(true);
    expect(runtimeSurfaces('nodejs').podResources).toBe(true);
    expect(runtimeSurfaces('go', false)).toMatchObject({ goCharts: false, podResources: true });
    expect(runtimeSurfaces(null)).toMatchObject({ jvmCharts: false, dotnet: false, podResources: true });
  });
});
