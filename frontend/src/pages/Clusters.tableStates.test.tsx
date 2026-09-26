// @vitest-environment jsdom
// Clusters.tableStates.test.tsx — v0.10.954 (tablo standardı T12 göçünün
// iki gerilemesi, çapraz incelemede yakalandı):
//
//   1. Yenileme hatası BAYAT satırların altında kayboluyordu. Eski kod
//      "Node/Pod metrics unavailable" / "Workload rollup unavailable" notunu
//      tablo doluyken de çiziyordu; göç durumu yalnız boş tabloya aldı →
//      başarısız bir refetch eski satırları hatasız bırakıyordu. Artık dolu
//      tablonun üstünde tek satırlık not ("Son başarılı veri gösteriliyor").
//   2. `?q=` kaynak boşken de "Eşleşme yok" diyordu. Primitifin sözleşmesi:
//      no-match = veri VAR, süzgeç hepsini eledi. Kaynak gerçekten boşsa
//      boş-durum çaresi (runbook / probe) görünür kalır.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type {
  ClusterNodeRow, ClusterPodRow, ClusterDeploymentRow, ClusterNamespaceRow,
} from '@/lib/types';

const m = vi.hoisted(() => ({
  nodes: [] as unknown[],
  pods: [] as unknown[],
  deps: [] as unknown[],
  nss: [] as unknown[],
  failNodes: false, failPods: false, failDeps: false,
}));
const calls = vi.hoisted(() => ({
  clusterSources: async () => ({ clusters: ['c1'] }),
  clusterSummary: async (cluster: string) => ({ cluster }),
  clusters: async () => ({ clusters: ['c1'] }),
  clusterNodes: async (cluster: string) => {
    if (m.failNodes) throw new Error('boom');
    return { cluster, nodes: m.nodes, count: m.nodes.length };
  },
  clusterPods: async (cluster: string) => {
    if (m.failPods) throw new Error('boom');
    return { cluster, pods: m.pods, count: m.pods.length };
  },
  clusterNamespaces: async (cluster: string) => ({ cluster, namespaces: m.nss, count: m.nss.length }),
  clusterDeployments: async (cluster: string, namespace: string) => {
    if (m.failDeps) throw new Error('boom');
    return { cluster, namespace, deployments: m.deps, count: m.deps.length };
  },
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), ...calls } };
});

import ClustersPage from './Clusters';

const node = (name: string): ClusterNodeRow => ({ cluster: 'c1', node: name, cpuCores: 1, memBytes: 1 });
const pod = (name: string): ClusterPodRow => ({ cluster: 'c1', namespace: 'ns1', pod: name, cpuCores: 1, memBytes: 1 });
const dep = (name: string): ClusterDeploymentRow => ({
  cluster: 'c1', namespace: 'ns1', deployment: name, pods: 1, cpuCores: 1, memBytes: 1,
  podNames: [], desiredReplicas: 1, readyReplicas: 1,
});
const ns = (name: string): ClusterNamespaceRow => ({ cluster: 'c1', namespace: name, cpuCores: 1, memBytes: 1 });

const STALE = 'Son başarılı veri gösteriliyor.';
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

let host: HTMLElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
async function mount(url: string): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  // Sayfa sorguları `retry: 1` taşır; bekleme 0 → hata hemen düşer.
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <ClustersPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
async function refetch(key: string) {
  await act(async () => {
    await qc.refetchQueries({ queryKey: [key] });
    await new Promise(r => setTimeout(r, 30));
  });
}
const stateRow = (el: HTMLElement) => el.querySelector('tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('tbody tr:not([data-dt-state])').length;

beforeEach(() => {
  m.nodes = []; m.pods = []; m.deps = []; m.nss = [];
  m.failNodes = false; m.failPods = false; m.failDeps = false;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('Clusters — yenileme hatası bayat satırların üstünde görünür (v0.10.954)', () => {
  it('nodes: dolu tabloda refetch düşerse satırlar durur + hata notu', async () => {
    m.nodes = [node('n1'), node('n2')];
    const el = await mount('/clusters?cluster=c1&section=nodes');
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).not.toContain(STALE);
    m.failNodes = true;
    await refetch('cluster-nodes');
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).toContain('Node metrikleri okunamadı');
    expect(el.textContent).toContain(STALE);
    // Hata satırı tabloda DEĞİL (satırlar dolu) — iki kez gösterilmez.
    expect(stateRow(el)).toBeNull();
  });

  it('nodes: boş tabloda hata yalnız tablonun içinde (not yok)', async () => {
    m.failNodes = true;
    const el = await mount('/clusters?cluster=c1&section=nodes');
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(el.textContent).not.toContain(STALE);
  });

  it('pods: dolu tabloda refetch düşerse satırlar durur + hata notu', async () => {
    m.pods = [pod('p1')];
    const el = await mount('/clusters?cluster=c1&section=pods');
    expect(dataRows(el)).toBe(1);
    m.failPods = true;
    await refetch('cluster-pods');
    expect(dataRows(el)).toBe(1);
    expect(el.textContent).toContain('Pod metrikleri okunamadı');
    expect(el.textContent).toContain(STALE);
    expect(stateRow(el)).toBeNull();
  });

  it('workloads: dolu tabloda refetch düşerse satırlar durur + hata notu', async () => {
    m.nss = [ns('ns1')];
    m.deps = [dep('d1'), dep('d2')];
    const el = await mount('/clusters?cluster=c1&section=namespaces&namespace=ns1');
    expect(dataRows(el)).toBe(2);
    m.failDeps = true;
    await refetch('cluster-deployments');
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).toContain('İş yükü özeti okunamadı');
    expect(el.textContent).toContain(STALE);
    expect(stateRow(el)).toBeNull();
  });
});

describe('Clusters — ?q= "eşleşme yok" yalnız kaynakta satır varken (v0.10.954)', () => {
  it('nodes: kaynak boşsa ?q= olsa da boş-durum çaresi', async () => {
    const el = await mount('/clusters?cluster=c1&section=nodes&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    expect(el.textContent).toContain('node-exporter serileri boş döndü');
  });
  it('nodes: kaynak doluysa ve ?q= hepsini elediyse eşleşme yok', async () => {
    m.nodes = [node('n1')];
    const el = await mount('/clusters?cluster=c1&section=nodes&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
  });
  it('namespaces: kaynak boşsa boş, doluysa eşleşme yok', async () => {
    let el = await mount('/clusters?cluster=c1&section=namespaces&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    act(() => root!.unmount()); host?.remove(); root = null;
    m.nss = [ns('ns1')];
    el = await mount('/clusters?cluster=c1&section=namespaces&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
  });
  it('workloads: kaynak boşsa boş, doluysa eşleşme yok', async () => {
    m.nss = [ns('ns1')];
    let el = await mount('/clusters?cluster=c1&section=namespaces&namespace=ns1&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    act(() => root!.unmount()); host?.remove(); root = null;
    m.deps = [dep('d1')];
    el = await mount('/clusters?cluster=c1&section=namespaces&namespace=ns1&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
  });
  it('pods: kaynak boşsa boş (namespace süzgeci çaresi), doluysa eşleşme yok', async () => {
    let el = await mount('/clusters?cluster=c1&section=pods&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    expect(el.textContent).toContain('Sorgular seri döndürmedi');
    act(() => root!.unmount()); host?.remove(); root = null;
    m.pods = [pod('p1')];
    el = await mount('/clusters?cluster=c1&section=pods&q=zzz');
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
  });
});
