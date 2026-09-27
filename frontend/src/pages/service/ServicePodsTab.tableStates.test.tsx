// @vitest-environment jsdom
// ServicePodsTab.tableStates — v0.10.973 (tablo standardı T12, tarif P6).
//
// NE ÇİVİLİYOR: Pods sekmesinin yükleniyor / hata / boş durumu pod
// tablosunun İÇİNDE (ServicePodsTable `state`), sütun başlıkları durur.
// Zincir eskisinin aynısı (Spinner → "Pod metrikleri okunamadı" → "No pods
// matched"), aynı sırada ve aynı koşullarla:
//   • birleşik küme boşken ve entity / Thanos beklenirken → loading;
//   • cluster'lar yanıt vermedi VE entity kapalı → hata, cluster'lar adıyla
//     ("liste bu yüzden boş olabilir, workload yok demek değil");
//     entity açıkken aynı hata boş durumuna düşer (eski koşul);
//   • aksi hâlde "Eşleşen pod yok" + tanı cümlesi (entity, Thanos / cluster
//     yok, pod adı kalıbı `mono`) aynı hücrede (P-1);
//   • v0.10.973 — kaynak süzgeci (?psrc) açıkken aynı boşluk "eşleşme yok"
//     (tarif §3 sıra 3): süzgeç değeri mesajda, tanı cümlesi aynen, eski
//     "hepsi"ni dene ipucu "Filtreleri temizle" düğmesi — ?psrc'yi siler;
//   • entity satırları Thanos hiçbir şey bulmasa da görünür (v0.10.145
//     kuralı): durum satırı YALNIZ birleşik küme boşken; satırlar kazanır.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ClusterPodRow, ServicePodRow } from '@/lib/types';

const m = vi.hoisted(() => ({
  th: {} as Record<string, unknown>,
  entityEnabled: false,
  entityQ: {} as Record<string, unknown>,
}));

vi.mock('./useServicePods', () => ({ useServicePods: () => m.th }));
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useEntityEnabled: () => ({ enabled: m.entityEnabled, loading: false, clusters: [] }),
    useEntityServicePods: () => m.entityQ,
  };
});
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), serviceRuntime: async () => ({}) } };
});
vi.mock('./RuntimeCharts', () => ({ RuntimeCharts: () => null, familyOf: () => null }));
vi.mock('./HeapBaselineCard', () => ({ HeapBaselineCard: () => null }));
vi.mock('./PodResourceCharts', () => ({ PodResourceCharts: () => null }));

import { ServicePodsTab } from './ServicePodsTab';

function thStub(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    podsUpdatedAt: 0, podsFetching: false, refetchPods: () => {},
    metaQ: { isPending: false }, ns: '', deploy: '', matched: ['c1'], rows: [] as ClusterPodRow[],
    clustersWithPods: [], effNs: '', effDeploy: '', from: 0, to: 1, cFrom: 0, cTo: 1, clamped: false,
    sourcesPending: false, noClusters: false, podsPending: false, podsBlocking: false, podsSettled: 1, podsTotal: 1,
    sourcesError: '', podErrors: [] as string[], truncatedClusters: [] as string[],
    ...over,
  };
}
function entityStub(pods: ServicePodRow[] | undefined, over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    data: pods ? { service: 'orders', pods } : undefined,
    isPending: pods === undefined, isError: false, isFetching: false, error: null,
    refetch: async () => ({}),
    ...over,
  };
}
const entityPod = (pod: string): ServicePodRow => ({
  cluster: 'c1', namespace: 'ns1', pod, service: 'orders', spans: 10, errors: 0, avgMs: 5,
  firstSeen: '2026-09-27T00:00:00Z', lastSeen: '2026-09-27T00:05:00Z',
});
const thanosPod = (pod: string): ClusterPodRow => ({
  cluster: 'c1', namespace: 'ns1', pod, cpuCores: 0.5, memBytes: 1024, service: 'orders',
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
// v0.10.973 — "Filtreleri temizle" URL'yi yazıyor mu: konum sondası.
function LocProbe() {
  const loc = useLocation();
  return <output data-testid="loc">{loc.search}</output>;
}
async function mount(url = '/service?name=orders&tab=pods'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <ServicePodsTab service="orders" range={{ preset: '1h' }} />
          <LocProbe />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host!;
}

const podsTable = (el: HTMLElement) => el.querySelector('table') as HTMLTableElement | null;
const stateRow = (el: HTMLElement) => el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;

beforeEach(() => {
  m.th = thStub();
  m.entityEnabled = false;
  m.entityQ = entityStub([]);
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
});

describe('ServicePodsTab — durumlar pod tablosunun içinde (v0.10.973)', () => {
  it('Thanos beklenirken ve satır yokken: başlık durur, tek loading satırı, dışarıda Spinner yok', async () => {
    m.th = thStub({ metaQ: { isPending: true } });
    const el = await mount();
    expect(podsTable(el)?.querySelector('thead')).not.toBeNull();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    expect(el.querySelector('.spinner')).toBeNull();
  });

  it('entity açık ve bekleniyorsa da loading (eski `entityPending`)', async () => {
    m.entityEnabled = true;
    m.entityQ = entityStub(undefined);
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
  });

  it('cluster\'lar yanıt vermedi ve entity kapalı: hata satırı cluster adlarıyla, "boş" değil', async () => {
    m.th = thStub({ podErrors: ['c1', 'c2'] });
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain("Pod metrikleri okunamadı: c1, c2 cluster'ları sorguya yanıt vermedi");
    expect(row.textContent).toContain('liste bu yüzden boş olabilir, workload yok demek değil');
    expect(row.querySelectorAll('td').length).toBe(1);
    // Yeniden deneme eskiden de yoktu (başlıktaki ↻ sekmenin denemesi).
    expect(row.textContent).not.toContain('Retry');
  });

  it('tek cluster yanıt vermedi: tekil ek', async () => {
    m.th = thStub({ podErrors: ['c1'] });
    const el = await mount();
    expect(stateRow(el)?.textContent).toContain("c1 cluster'ı sorguya yanıt vermedi");
  });

  it('sıra eskisi gibi: bir cluster düştü ama diğeri hâlâ taranıyorsa önce loading', async () => {
    m.th = thStub({ podErrors: ['c1'], podsPending: true, podsSettled: 1, podsTotal: 2 });
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
  });

  it('entity açıkken aynı cluster hatası boş durumuna düşer (eski koşul: yalnız entity kapalıyken hata)', async () => {
    m.entityEnabled = true;
    m.th = thStub({ podErrors: ['c1'] });
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.textContent).toContain('Entity katmanı bu pencerede pod görmedi');
  });

  it('eşleşme yok: "Eşleşen pod yok" + tanı cümlesi aynı hücrede, kalıp mono', async () => {
    m.th = thStub({ matched: ['c1', 'c2'] });
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.textContent).toContain('Eşleşen pod yok');
    expect(row.textContent).toContain('Entity katmanı kapalı.');
    expect(row.textContent).toContain("2 cluster'da denendi — eşleşme yok.");
    expect(row.querySelector('.mono')?.textContent).toBe('(orders)-.*');
    expect(row.querySelectorAll('td').length).toBe(1);
    // Kaynak süzgeci "hepsi" iken ipucu yok.
    expect(row.textContent).not.toContain('Kaynak süzgeci');
  });

  it('k8s metadata eşlemesi varsa cümle onu söyler', async () => {
    m.th = thStub({ ns: 'shop', deploy: 'orders-api' });
    const el = await mount();
    expect(stateRow(el)?.textContent).toContain('Thanos: k8s.namespace=shop · orders-api');
  });

  it('Thanos cluster\'ı tanımlı değilse bunu söyler', async () => {
    m.th = thStub({ noClusters: true, matched: [] });
    const el = await mount();
    expect(stateRow(el)?.textContent).toContain("Thanos cluster'ı tanımlı değil (Settings → Remote clusters).");
  });

  // v0.10.973 — tarif §3 sıra 3: kullanıcı süzgeci açık + sıfır satır = no-match,
  // sayfanın kendi temizleme eylemiyle ("hepsi", replace:true → ?psrc silinir).
  it('kaynak süzgeci "hepsi" değilse: no-match, süzgeç değeri mesajda, tanı aynen, "Filtreleri temizle" ?psrc\'yi siler', async () => {
    m.th = thStub({ matched: ['c1', 'c2'] });
    const el = await mount('/service?name=orders&tab=pods&psrc=thanos');
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(row.textContent).toContain('Kaynak süzgeci "thanos" ile eşleşen pod yok');
    // Tanı cümlesi kaybolmadı; eski ipucu cümlesi düğmeye dönüştü.
    expect(row.textContent).toContain('Entity katmanı kapalı.');
    expect(row.textContent).toContain("2 cluster'da denendi — eşleşme yok.");
    expect(row.querySelector('.mono')?.textContent).toBe('(orders)-.*');
    expect(row.textContent).not.toContain('"hepsi"ni dene');
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(el.querySelector('[data-testid="loc"]')?.textContent).toContain('psrc=thanos');
    const clear = Array.from(row.querySelectorAll('button')).find(b => b.textContent?.includes('Filtreleri temizle'));
    expect(clear).toBeDefined();
    await act(async () => { clear!.click(); });
    const search = el.querySelector('[data-testid="loc"]')?.textContent ?? '';
    expect(search).not.toContain('psrc');
    // Diğer parametreler yerinde (yalnız süzgeç silinir).
    expect(search).toContain('name=orders');
    expect(search).toContain('tab=pods');
    // Süzgeç kalkınca aynı boş küme "empty"ye döner (eski metin, düğme yok).
    const after = stateRow(el)!;
    expect(after.dataset.dtState).toBe('empty');
    expect(after.textContent).toContain('Eşleşen pod yok');
    expect(after.textContent).not.toContain('Filtreleri temizle');
  });

  it('psrc=entity de aynı süzgeç: no-match + değer mesajda', async () => {
    const el = await mount('/service?name=orders&tab=pods&psrc=entity');
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(row.textContent).toContain('Kaynak süzgeci "entity" ile eşleşen pod yok');
  });

  it('süzgeç açıkken de sıra eskisi gibi: loading ve (entity kapalı) hata no-match\'ten önce', async () => {
    m.th = thStub({ metaQ: { isPending: true } });
    let el = await mount('/service?name=orders&tab=pods&psrc=thanos');
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    act(() => { root?.unmount(); });
    qc?.clear();
    host?.remove();
    m.th = thStub({ podErrors: ['c1'] });
    el = await mount('/service?name=orders&tab=pods&psrc=thanos');
    expect(stateRow(el)?.dataset.dtState).toBe('error');
  });

  it('entity satırları Thanos hiçbir şey bulmasa da görünür; durum satırı yok', async () => {
    m.entityEnabled = true;
    m.entityQ = entityStub([entityPod('orders-7f9-abc')]);
    m.th = thStub({ rows: [], matched: ['c1'] });
    const el = await mount();
    expect(stateRow(el)).toBeNull();
    expect(el.querySelector('[id="pod-row-orders-7f9-abc"]')).not.toBeNull();
  });

  it('satırlar varken Thanos hâlâ taranıyor olsa da satırlar kazanır (loading satırı yok)', async () => {
    m.th = thStub({ rows: [thanosPod('orders-1')], podsPending: true, podsSettled: 1, podsTotal: 2 });
    const el = await mount();
    expect(stateRow(el)).toBeNull();
    expect(el.querySelector('[id="pod-row-orders-1"]')).not.toBeNull();
  });
});
