// @vitest-environment jsdom
// DotnetRuntimePanel.runtimeCharts — v0.10.2443 (v0.10.2436 ödünleri).
//
// NE ÇİVİLİYOR: (1) GC payı / thread pool kartlarında servis p99'u sağ eksende
// (kesikli, kendi birimi ms, etiketli eksen); bindirmesiz kartta sağ eksen yok.
// (2) Nesil kartı pod × nesil okur: varsayılan nesil başına pod medyanı;
// seçici (?rtgen) o pod'un nesil çizgilerine geçer; "Tüm pod'lar"
// (?rtview=lines) her pod × nesil. (4) GC payı eşiği operatörün
// runtime_alerts ayarından; ayar yoksa varsayılan %10.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({
  panels: [] as Record<string, unknown>[],
  groupBys: [] as string[],
  alerts: undefined as undefined | Record<string, number>,
}));
const two = (a: number, b: number) => [{ time: 1e9, value: a }, { time: 2e9, value: b }];
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceRuntimeSignals: async () => ({
        service: 'svc', mode: 'shadow', pods: 3, signals: [], atNs: 0,
        profile: { runtime: 'dotnet', signals: [
          { signal: 'gc_time', metric: 'dotnet.gc.pause.time', kind: 'counter', agg: 'rate', scale: 1 },
          { signal: 'gc_collections', metric: 'dotnet.gc.collections', kind: 'counter', agg: 'rate', scale: 1, genLabel: 'gc.heap.generation' },
          { signal: 'lock_contention', metric: 'dotnet.monitor.lock_contentions', kind: 'counter', agg: 'rate', scale: 1 },
        ] },
        ...(m.alerts ? { runtimeAlerts: m.alerts } : {}),
      }),
      spanMetricBatch: async () => ({ series: { p99: [{ groupKey: [], points: two(120, 340) }] } }),
      metricQuery: async (q: { name: string; groupBy?: string }) => {
        m.groupBys.push(`${q.name}|${q.groupBy ?? ''}`);
        const pods = ['pod-a', 'pod-b', 'pod-c'];
        if (q.name === 'dotnet.gc.collections') {
          // pod-c en çok toplar (en kötü); [0..3] pod anahtarları, [4] nesil.
          return pods.flatMap((p, i) => ['gen0', 'gen1'].map((g, k) => ({
            groupKey: [p, '', '', '', g], points: two((i + 1) * (k ? 1 : 10), (i + 1) * (k ? 2 : 20)),
          })));
        }
        return pods.map((p, i) => ({ groupKey: [p, '', '', ''], points: two(0.01 * (i + 1), 0.02 * (i + 1)) }));
      },
    },
  };
});
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useServiceDeploys: () => ({ data: [] }) };
});
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: Record<string, unknown>) => { m.panels.push(p); return <div data-testid="cpm" />; },
}));

import DotnetRuntimePanel from './DotnetRuntimePanel';

type Item = { name: string; axis?: string; unit?: string; dashed?: boolean; color?: string };
type Panel = { storageKey: string; items: Item[]; rightAxisLabel?: string; thresholds?: { value: number }[] };
let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
function Loc() { return <output data-testid="loc">{useLocation().search}</output>; }
async function mount(url = '/service?name=svc&tab=details'): Promise<HTMLElement> {
  m.panels.length = 0; m.groupBys.length = 0;
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <DotnetRuntimePanel service="svc" from={1} to={2} />
          <Loc />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  for (let i = 0; i < 40 && !(panel('gcgen')?.items.length && panel('gc')?.items.some(it => it.axis)); i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  }
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  return host;
}
const panel = (key: string) => [...m.panels].reverse().find(p => p.storageKey === `rt-dotnet-${key}`) as unknown as Panel | undefined;
const names = (p: Panel | undefined) => (p?.items ?? []).map(i => i.name);

beforeEach(() => { m.alerts = undefined; });
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
});

describe('DotnetRuntimePanel — sağ eksen p99 (madde 1)', () => {
  it('GC payı kartı: p99 sağ eksende, kesikli, ms, etiketli', async () => {
    await mount();
    const gc = panel('gc')!;
    const p99 = gc.items.find(i => i.axis === 'right')!;
    expect(p99).toMatchObject({ name: 'p99 latency', dashed: true, unit: 'ms', color: 'var(--text3)' });
    expect(gc.rightAxisLabel).toBe('p99 latency (ms)');
  });

  it('bindirmesiz kart (lock) sağ eksensiz', async () => {
    await mount();
    const lock = panel('lock')!;
    expect(lock.items.some(i => i.axis === 'right')).toBe(false);
    expect(lock.rightAxisLabel).toBeUndefined();
  });
});

describe('DotnetRuntimePanel — eşik ayardan (madde 4)', () => {
  it('ayar yok → varsayılan %10', async () => {
    await mount();
    expect(panel('gc')!.thresholds?.[0].value).toBe(10);
  });
  it('operatör override\'ı → o değer', async () => {
    m.alerts = { gcShareWarnPct: 2.5 };
    await mount();
    expect(panel('gc')!.thresholds?.[0].value).toBe(2.5);
  });
});

describe('DotnetRuntimePanel — nesil kartı pod kırılımı (madde 2)', () => {
  it('sorgu pod × nesil; varsayılan nesil başına medyan + seçici', async () => {
    const el = await mount();
    expect(m.groupBys).toContain('dotnet.gc.collections|resource.host.name,resource.k8s.pod.name,resource.service.instance.id,cluster,gc.heap.generation');
    expect(names(panel('gcgen'))).toEqual(['gen0 · median', 'gen1 · median']);
    const card = el.querySelector('[data-testid="rt-card-gcgen"]')!;
    expect(card.textContent).toContain('median per generation · 3 pods');
    const sel = card.querySelector('select[data-testid="rt-gen-pod"]') as HTMLSelectElement;
    expect([...sel.options].map(o => o.textContent)).toEqual(['Median (pods)', 'pod-c · worst', 'pod-b', 'pod-a']);
    // Nesilsiz kartta seçici yok.
    expect(el.querySelector('[data-testid="rt-card-gc"] select')).toBeNull();
  });

  it('pod seçimi URL\'ye yazılır, o pod\'un nesil çizgileri çizilir', async () => {
    const el = await mount();
    const sel = el.querySelector('[data-testid="rt-card-gcgen"] select') as HTMLSelectElement;
    await act(async () => {
      sel.value = 'pod-b';
      sel.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    expect(el.querySelector('[data-testid="loc"]')?.textContent).toContain('rtgen=pod-b');
    expect(names(panel('gcgen'))).toEqual(['gen0', 'gen1']);
  });

  it('?rtgen derin linki açılışta uygulanır', async () => {
    await mount('/service?name=svc&tab=details&rtgen=pod-a');
    expect(names(panel('gcgen'))).toEqual(['gen0', 'gen1']);
  });

  it('"Tüm pod\'lar" (?rtview=lines): pod × nesil, seçici yok', async () => {
    const el = await mount('/service?name=svc&tab=details&rtview=lines&rtgen=pod-a');
    expect(names(panel('gcgen'))).toEqual(['pod-c · gen0', 'pod-c · gen1', 'pod-b · gen0', 'pod-b · gen1', 'pod-a · gen0', 'pod-a · gen1']);
    expect(el.querySelector('[data-testid="rt-card-gcgen"] select')).toBeNull();
  });
});
