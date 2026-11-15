// @vitest-environment jsdom
//
// Clusters.urlState.test.tsx — S0 karakterizasyon (/clusters shadcn geçişi +
// iyileştirme planı docs/frontend/clusters-shadcn-plan.md §11 S0, operatör
// onayı 2026-11-14). Kod değişmeden ÖNCE bugünkü davranışı çiviler; sonraki
// dilimler (S2–S14) bu dosyayı bozmadan geçmelidir.
//
// NE ÇİVİLİYOR (sahte olan yalnız AĞ katmanı; sayfa gerçek):
//   • URL durumu: ?cluster / ?section / ?namespace / ?deployment / ?service /
//     ?q / ?ns / ?tw — okuma VE geri yazma, yabancı parametre korunur.
//   • K3: pod hücresi gerçek bağlantı (/pod); namespace ve iş yükü satırı
//     sayfa içi süzgeç; ?deployment= pod'ları iş yükünün GERÇEK üyeliğiyle
//     (podNames) süzer, ad önekiyle değil.
//   • K5: Overview kaynak grafikleri varsayılan "By node" kırılımıyla çekilir.
//   • K13: ?namespace= / ?service= / ?deployment= taşıyan ?section'sız derin
//     bağlantı pod listesine düşer (servis sayfası pivotu).
//   • Sekme başına /api/clusters/* çağrı sayısı (açık olmayan sekme çekmez).
//
// İddialar bilerek görünen sonuca bağlı (URL, satır, çağrı): sekme şeridinin
// / çekmecenin / süzgeç satırının hangi bileşenle çizildiğine değil.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type {
  ClusterNodeRow, ClusterPodRow, ClusterDeploymentRow, ClusterNamespaceRow,
} from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
  (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
});

const m = vi.hoisted(() => ({
  calls: [] as { name: string; args: unknown[] }[],
  nodes: [] as unknown[],
  pods: [] as unknown[],
  deps: [] as unknown[],
  nss: [] as unknown[],
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  const real = mod.api as Record<string, (...a: unknown[]) => unknown>;
  const stub: Record<string, (...a: never[]) => unknown> = {
    clusterSources: async () => ({ clusters: ['prod-a', 'prod-b'] }),
    clusters: async () => ({ clusters: ['prod-a', 'prod-b'] }),
    clusterSummary: async (cluster: string) => ({ cluster, nodes: 3, pods: 40 }),
    clusterNodes: async (cluster: string) => ({ cluster, nodes: m.nodes, count: m.nodes.length }),
    clusterPods: async (cluster: string) => ({ cluster, pods: m.pods, count: m.pods.length }),
    clusterNamespaces: async (cluster: string) => ({ cluster, namespaces: m.nss, count: m.nss.length }),
    clusterDeployments: async (cluster: string, namespace: string) =>
      ({ cluster, namespace, deployments: m.deps, count: m.deps.length }),
    clusterAlerts: async (cluster: string) => ({ cluster, alerts: [] }),
    clusterResourceTrend: async (cluster: string) => ({ cluster, series: [] }),
    clusterNetworkTrend: async (cluster: string) => ({ cluster, trend: [] }),
    clusterNamespacePodsTrend: async (cluster: string, namespace: string) =>
      ({ cluster, namespace, pods: [], totalPods: 0 }),
  };
  const api = new Proxy(real, {
    get: (t, k: string) => (...a: unknown[]) => {
      m.calls.push({ name: k, args: a });
      return ((stub[k] as ((...x: unknown[]) => unknown) | undefined) ?? t[k])(...a);
    },
  });
  return { ...mod, api };
});
// Grafik STUB (jsdom'da canvas yok) — bu dosya çizimi değil sorguyu ölçer.
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: () => <div data-cpm="1" />,
}));

import ClustersPage from './Clusters';

// Clusters S7 (D6) — kalıcılık testleri için bellek içi localStorage (jsdom'un
// deposu bu ortamda işlevsiz; pickerRecents.test emsali).
function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() { return m.size; },
    clear: () => m.clear(),
    getItem: k => m.get(k) ?? null,
    key: i => [...m.keys()][i] ?? null,
    removeItem: k => { m.delete(k); },
    setItem: (k, v) => { m.set(k, String(v)); },
  };
}

const node = (name: string): ClusterNodeRow => ({ cluster: 'prod-a', node: name, cpuCores: 1, memBytes: 1 });
const pod = (namespace: string, name: string, service?: string): ClusterPodRow =>
  ({ cluster: 'prod-a', namespace, pod: name, cpuCores: 1, memBytes: 1, service });
const dep = (name: string, podNames: string[]): ClusterDeploymentRow => ({
  cluster: 'prod-a', namespace: 'shop', deployment: name, pods: podNames.length, cpuCores: 1, memBytes: 1,
  podNames, desiredReplicas: podNames.length, readyReplicas: podNames.length,
});
const ns = (name: string): ClusterNamespaceRow => ({ cluster: 'prod-a', namespace: name, cpuCores: 1, memBytes: 1 });

let host: HTMLElement | null = null;
let root: Root | null = null;
let loc = { pathname: '', search: '' };
function Probe() {
  const l = useLocation();
  loc = { pathname: l.pathname, search: l.search };
  return null;
}
const sp = () => new URLSearchParams(loc.search);
const tick = (ms = 20) => act(async () => { await new Promise(r => setTimeout(r, ms)); });
/** Koşul tutana dek bekler (yük altındaki makinede sabit bekleme titrer). */
async function until(pred: () => boolean, label: string) {
  for (let i = 0; i < 100; i++) {
    if (pred()) return;
    await tick();
  }
  throw new Error(`beklenen durum oluşmadı: ${label}`);
}
async function mount(url: string): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <Probe />
          <ClustersPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await until(() => m.calls.some(c => c.name === 'clusterSources'), 'clusterSources çağrısı');
  // Kaynak listesi → bağımlı sorgular → render zinciri otursun.
  for (let i = 0; i < 4; i++) await tick();
  return host!;
}

/** Fare tıklaması: mousedown ile etkinleşen (Radix) ve click ile etkinleşen
 *  denetimlerin ikisini de sürer. */
function click(el: Element) {
  act(() => {
    for (const type of ['pointerdown', 'mousedown', 'pointerup', 'mouseup', 'click']) {
      el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button: 0 }));
    }
  });
}
function typeInto(input: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
function choose(select: HTMLSelectElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
  act(() => {
    set.call(select, value);
    select.dispatchEvent(new Event('change', { bubbles: true }));
  });
}

/** /api/clusters/* çağrıları: ad → kaç kez. */
function clusterCalls(): Record<string, number> {
  const out: Record<string, number> = {};
  for (const c of m.calls) {
    if (c.name === 'clusters' || c.name.startsWith('cluster')) out[c.name] = (out[c.name] ?? 0) + 1;
  }
  return out;
}
const callsOf = (name: string) => m.calls.filter(c => c.name === name);
const tabs = (el: HTMLElement) => Array.from(el.querySelectorAll<HTMLElement>('[role="tab"]'));
const tab = (el: HTMLElement, label: string) => tabs(el).find(t => (t.textContent ?? '').trim().startsWith(label))!;
const selectedTab = (el: HTMLElement) =>
  (tabs(el).find(t => t.getAttribute('aria-selected') === 'true')?.textContent ?? '').trim();
const dataRows = (el: HTMLElement) =>
  Array.from(el.querySelectorAll<HTMLTableRowElement>('tbody tr:not([data-dt-state])'));
const rowTexts = (el: HTMLElement) => dataRows(el).map(r => r.textContent ?? '');
// Clusters S9 (D7) — namespace detayında iki tablo: pod satırları gerçek /pod
// bağlantısıyla, iş yükleri tablo adıyla ayrılır.
const podLinks = (el: HTMLElement) =>
  Array.from(el.querySelectorAll<HTMLTableRowElement>('tbody tr:not([data-dt-state])'))
    .map(r => r.querySelector<HTMLAnchorElement>('a[href^="/pod?"]')?.textContent ?? null)
    .filter((t): t is string => t != null);
const workloadRows = (el: HTMLElement, ns: string) =>
  Array.from(el.querySelectorAll<HTMLTableRowElement>(`table[aria-label="${ns} iş yükleri"] tbody tr:not([data-dt-state])`));
const filterInput = (el: HTMLElement) =>
  el.querySelector<HTMLInputElement>('input[placeholder="Filter by name…"]')!;
const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]');

beforeEach(() => {
  m.calls.length = 0;
  m.nodes = [node('node-1'), node('node-2')];
  m.nss = [ns('shop'), ns('billing')];
  m.pods = [
    pod('shop', 'cart-7d9f-aaaaa', 'cart'),
    pod('shop', 'cart-7d9f-bbbbb', 'cart'),
    // Aynı ad öneki, BAŞKA iş yükü: önek eşlemesi bunu da alırdı (K3).
    pod('shop', 'cart-canary-5c6d-ccccc', 'cart-canary'),
    pod('billing', 'ledger-0', 'ledger'),
  ];
  m.deps = [dep('cart', ['cart-7d9f-aaaaa', 'cart-7d9f-bbbbb']), dep('cart-canary', ['cart-canary-5c6d-ccccc'])];
  loc = { pathname: '', search: '' };
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('Clusters S0 — liste görünümü (?cluster yok)', () => {
  it('kart başına bir summary; pod/node/namespace çekilmez', async () => {
    await mount('/clusters');
    expect(clusterCalls()).toEqual({ clusterSources: 1, clusters: 1, clusterSummary: 2 });
    expect(callsOf('clusterSummary').map(c => c.args[0]).sort()).toEqual(['prod-a', 'prod-b']);
  });

  it('kart gerçek bağlantı: ?cluster=<ad>, pencere korunur, önceki sekme taşınmaz', async () => {
    const el = await mount('/clusters?range=1h&section=nodes');
    const links = Array.from(el.querySelectorAll<HTMLAnchorElement>('a[href*="cluster="]'));
    expect(links.map(a => new URLSearchParams(a.getAttribute('href')!.split('?')[1]).get('cluster'))).toEqual(['prod-a', 'prod-b']);
    const q = new URLSearchParams(links[0].getAttribute('href')!.split('?')[1]);
    expect(q.get('range')).toBe('1h');
    expect(q.get('section')).toBeNull();
  });
});

describe('Clusters S0 — sekme başına çağrı (açık olmayan sekme çekmez)', () => {
  // Clusters S7 (D2, D6) — "Ağ trafiği" bölümü varsayılan KAPALI: ağ trendi
  // açılana kadar çekilmez; açılınca çekilir ve tercih tarayıcıda kalır.
  it('Overview (varsayılan): summary + iki kaynak trendi + alarm + node + namespace; ağ kapalı', async () => {
    const el = await mount('/clusters?cluster=prod-a');
    expect(selectedTab(el)).toBe('Overview');
    expect(clusterCalls()).toEqual({
      clusterSources: 1, clusters: 1, clusterSummary: 1,
      clusterResourceTrend: 2, clusterAlerts: 1,
      clusterNodes: 1, clusterNamespaces: 1,
    });
  });

  it('S7: Ağ trafiği açılınca ağ trendi çekilir ve açık kalır (localStorage)', async () => {
    vi.stubGlobal('localStorage', memoryStorage());
    const el = await mount('/clusters?cluster=prod-a');
    const trigger = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Ağ trafiği'))!;
    await act(async () => { trigger.click(); await new Promise(r => setTimeout(r, 30)); });
    expect(clusterCalls().clusterNetworkTrend).toBe(1);
    expect(localStorage.getItem('clusters.overview.open')).toBe('["net"]');
    vi.unstubAllGlobals();
  });

  it('S7: kayıtlı açık bölüm sonraki ziyarette açık gelir', async () => {
    vi.stubGlobal('localStorage', memoryStorage());
    localStorage.setItem('clusters.overview.open', '["net"]');
    await mount('/clusters?cluster=prod-a');
    expect(clusterCalls().clusterNetworkTrend).toBe(1);
    vi.unstubAllGlobals();
  });

  it('K5: kaynak trendleri varsayılan By node kırılımıyla istenir', async () => {
    await mount('/clusters?cluster=prod-a');
    const res = callsOf('clusterResourceTrend').map(c => [c.args[0], c.args[1], c.args[2]]);
    expect(res).toContainEqual(['prod-a', 'cpu', true]);
    expect(res).toContainEqual(['prod-a', 'mem', true]);
  });

  it('Nodes: summary + node + namespace', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=nodes');
    expect(selectedTab(el)).toBe('Nodes (2)');
    expect(clusterCalls()).toEqual({
      clusterSources: 1, clusters: 1, clusterSummary: 1, clusterNodes: 1, clusterNamespaces: 1,
    });
    expect(dataRows(el).length).toBe(2);
  });

  it('Namespaces: summary + namespace', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=namespaces');
    expect(selectedTab(el)).toBe('Namespaces (2)');
    expect(clusterCalls()).toEqual({
      clusterSources: 1, clusters: 1, clusterSummary: 1, clusterNamespaces: 1,
    });
  });

  it('Pods: summary + pod + namespace', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=pods');
    expect(selectedTab(el)).toBe('Pods (4)');
    expect(clusterCalls()).toEqual({
      clusterSources: 1, clusters: 1, clusterSummary: 1, clusterPods: 1, clusterNamespaces: 1,
    });
  });
});

describe('Clusters S0 — ?section', () => {
  it('sekme tıklaması ?section yazar, yabancı parametre ve ?cluster kalır', async () => {
    const el = await mount('/clusters?cluster=prod-a&range=1h');
    click(tab(el, 'Nodes'));
    await until(() => sp().get('section') === 'nodes', '?section=nodes');
    expect(sp().get('cluster')).toBe('prod-a');
    expect(sp().get('range')).toBe('1h');
    expect(selectedTab(el)).toMatch(/^Nodes/);
    // Overview'a dönüş AÇIK yazılır (yokluk-türetmesine bırakılmaz).
    click(tab(el, 'Overview'));
    await until(() => sp().get('section') === 'overview', '?section=overview');
  });

  it('bilinmeyen ?section Overview; eski ?tab=nodes Nodes', async () => {
    let el = await mount('/clusters?cluster=prod-a&section=bogus');
    expect(selectedTab(el)).toBe('Overview');
    act(() => root!.unmount()); host?.remove(); root = null;
    el = await mount('/clusters?cluster=prod-a&tab=nodes');
    expect(selectedTab(el)).toMatch(/^Nodes/);
  });
});

describe('Clusters S0 — K13 derin bağlantılar pod listesine düşer', () => {
  // Clusters S9 (D7) — ?section'sız ?namespace= namespace detayına düşer;
  // pod listesi detayın Pod'lar tablosunda (aynı süzgeç, aynı veri).
  it('?namespace= (section yok): namespace detayı, o namespace\'in pod\'ları', async () => {
    const el = await mount('/clusters?cluster=prod-a&namespace=billing');
    expect(callsOf('clusterPods').length).toBe(1);
    expect(el.querySelector('[role="tablist"]')).toBeNull();
    expect(el.querySelector('nav[aria-label="Konum"]')?.textContent).toContain('Namespaces');
    expect(podLinks(el)).toEqual(['ledger-0']);
  });

  it('?service= (section yok): o servisin pod\'ları', async () => {
    const el = await mount('/clusters?cluster=prod-a&service=cart');
    expect(selectedTab(el)).toMatch(/^Pods/);
    expect(rowTexts(el).length).toBe(2);
    expect(rowTexts(el).every(t => t.includes('cart-7d9f-'))).toBe(true);
  });

  it('servis sayfası pivotu (?section=pods&namespace=&service=) sekme görünümünde kalır', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=pods&namespace=shop&service=cart');
    expect(selectedTab(el)).toMatch(/^Pods/);
    expect(rowTexts(el).length).toBe(2);
  });

  it('?section=overview açıkken ?namespace= Overview\'u ezmez', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=overview&namespace=shop');
    expect(selectedTab(el)).toBe('Overview');
    expect(callsOf('clusterPods').length).toBe(0);
  });
});

describe('Clusters S0 — K3 satır davranışı', () => {
  it('pod hücresi /pod sayfasına gerçek bağlantı', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=pods&q=ledger');
    const a = el.querySelector<HTMLAnchorElement>('tbody a[href^="/pod?"]')!;
    expect(a.textContent).toBe('ledger-0');
    const q = new URLSearchParams(a.getAttribute('href')!.split('?')[1]);
    expect(q.get('cluster')).toBe('prod-a');
    expect(q.get('namespace')).toBe('billing');
    expect(q.get('pod')).toBe('ledger-0');
    expect(q.get('service')).toBe('ledger');
  });

  it('namespace satırı sayfa içi süzgeç: ?namespace yazar, iş yükleri çekilir', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=namespaces');
    const row = dataRows(el).find(r => (r.textContent ?? '').includes('shop'))!;
    // Clusters S12 — coremetry/DataTable openRow: satır düğme (sayfa içi seçim, gezinme değil).
    expect(row.getAttribute('role')).toBe('button');
    expect(row.querySelector('a')).toBeNull();
    expect(callsOf('clusterDeployments').length).toBe(0);
    click(row);
    await until(() => sp().get('namespace') === 'shop', '?namespace=shop');
    expect(sp().get('section')).toBe('namespaces');
    await until(() => callsOf('clusterDeployments').length === 1, 'iş yükü sorgusu');
    expect(callsOf('clusterDeployments')[0].args).toEqual(['prod-a', 'shop']);
    await until(() => workloadRows(el, 'shop').some(r => (r.textContent ?? '').includes('cart-canary')), 'iş yükü satırları');
    expect(workloadRows(el, 'shop').length).toBe(2);
  });

  it('iş yükü satırı ?deployment yazar; pod\'lar gerçek üyelikle süzülür', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=namespaces&namespace=shop');
    await until(() => workloadRows(el, 'shop').length === 2, 'iş yükü satırları');
    const row = workloadRows(el, 'shop').find(r => /^cart(?!-canary)/.test(r.textContent ?? ''))!;
    click(row);
    await until(() => sp().get('deployment') === 'cart', '?deployment=cart');
    // Clusters S9 (D7) — süzgeç detayın içindeki pod tablosuna; sekme değişmez.
    expect(sp().get('section')).toBe('namespaces');
    expect(sp().get('namespace')).toBe('shop');
    await until(() => podLinks(el).some(t => t.includes('cart-7d9f-aaaaa')), 'pod satırları');
    // cart-canary-… "cart" önekini taşır ama podNames'te YOK → listede yok.
    expect(podLinks(el).length).toBe(2);
    expect(podLinks(el).some(t => t.includes('cart-canary'))).toBe(false);
  });

  it('?deployment= derin bağlantısı aynı süzgeci kurar', async () => {
    const el = await mount('/clusters?cluster=prod-a&namespace=shop&deployment=cart-canary');
    expect(podLinks(el)).toEqual(['cart-canary-5c6d-ccccc']);
  });
});

describe('Clusters S0 — ?q metin süzgeci', () => {
  it('URL\'den okunur ve üç tabloyu süzer', async () => {
    let el = await mount('/clusters?cluster=prod-a&section=nodes&q=node-2');
    expect(filterInput(el).value).toBe('node-2');
    expect(rowTexts(el).length).toBe(1);
    act(() => root!.unmount()); host?.remove(); root = null;
    el = await mount('/clusters?cluster=prod-a&section=namespaces&q=bill');
    expect(rowTexts(el).length).toBe(1);
    act(() => root!.unmount()); host?.remove(); root = null;
    el = await mount('/clusters?cluster=prod-a&section=pods&q=LEDGER');
    expect(rowTexts(el).length).toBe(1);
  });

  it('yazınca ?q yazılır, boşalınca silinir; öteki parametreler kalır', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=pods&range=1h');
    typeInto(filterInput(el), 'ledger');
    await until(() => sp().get('q') === 'ledger', '?q=ledger');
    expect(sp().get('section')).toBe('pods');
    expect(sp().get('range')).toBe('1h');
    expect(rowTexts(el).length).toBe(1);
    typeInto(filterInput(el), '');
    await until(() => !sp().has('q'), '?q silindi');
    expect(rowTexts(el).length).toBe(4);
  });
});

describe('Clusters S0 — ?ns namespace trend çekmecesi ve ?tw', () => {
  it('?ns=<cluster>|<namespace> çekmeceyi açar, trend o namespace için çekilir', async () => {
    await mount('/clusters?cluster=prod-a&section=namespaces&ns=prod-a%7Cshop');
    await until(() => callsOf('clusterNamespacePodsTrend').length === 1, 'namespace trend sorgusu');
    const c = callsOf('clusterNamespacePodsTrend')[0];
    expect([c.args[0], c.args[1]]).toEqual(['prod-a', 'shop']);
    expect(dialog()).not.toBeNull();
    expect(dialog()!.textContent).toContain('shop');
  });

  it('?ns yokken çekmece ve trend sorgusu yok', async () => {
    await mount('/clusters?cluster=prod-a&section=namespaces');
    expect(dialog()).toBeNull();
    expect(callsOf('clusterNamespacePodsTrend').length).toBe(0);
  });

  it('?tw=1h trend penceresini bir saate çeker (sayfa penceresinden bağımsız)', async () => {
    await mount('/clusters?cluster=prod-a&section=namespaces&ns=prod-a%7Cshop&tw=1h&range=15m');
    await until(() => callsOf('clusterNamespacePodsTrend').length === 1, 'namespace trend sorgusu');
    const [, , from, to] = callsOf('clusterNamespacePodsTrend')[0].args as [string, string, number, number];
    expect(to - from).toBe(3600 * 1e9);
  });

  it('pencere seçimi ?tw yazar; ?ns ve süzgeçler kalır', async () => {
    await mount('/clusters?cluster=prod-a&section=namespaces&ns=prod-a%7Cshop&q=sh');
    await until(() => dialog() !== null, 'çekmece');
    const sel = dialog()!.querySelector<HTMLSelectElement>('select')!;
    expect(Array.from(sel.options).map(o => o.value)).toEqual(['', '15m', '1h', '6h', '24h']);
    choose(sel, '6h');
    await until(() => sp().get('tw') === '6h', '?tw=6h');
    expect(sp().get('ns')).toBe('prod-a|shop');
    expect(sp().get('q')).toBe('sh');
  });

  it('satırdaki trend düğmesi ?ns yazar, namespace süzgecini değiştirmez', async () => {
    const el = await mount('/clusters?cluster=prod-a&section=namespaces');
    const btn = el.querySelector<HTMLButtonElement>('button[aria-label="Open per-pod trend charts for namespace billing"]')!;
    click(btn);
    await until(() => sp().get('ns') === 'prod-a|billing', '?ns yazıldı');
    expect(sp().has('namespace')).toBe(false);
    expect(sp().get('section')).toBe('namespaces');
  });
});

describe('Clusters S0 — "All clusters" dönüşü', () => {
  it('detaya ait tüm parametreleri siler; pencere ve ?tw kalır', async () => {
    const el = await mount(
      '/clusters?cluster=prod-a&section=pods&namespace=shop&deployment=cart&service=cart&q=x&range=1h&tw=6h');
    const back = Array.from(el.querySelectorAll<HTMLElement>('button, a'))
      .find(b => (b.textContent ?? '').includes('All clusters'))!;
    click(back);
    await until(() => !sp().has('cluster'), '?cluster silindi');
    for (const k of ['section', 'namespace', 'deployment', 'service', 'q', 'ns']) expect(sp().has(k), k).toBe(false);
    expect(sp().get('range')).toBe('1h');
    expect(sp().get('tw')).toBe('6h');
  });
});
