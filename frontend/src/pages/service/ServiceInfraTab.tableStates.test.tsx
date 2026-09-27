// @vitest-environment jsdom
// ServiceInfraTab.tableStates — v0.10.967 (tablo standardı T12, dilim 5, P-1).
//
// NE ÇİVİLİYOR: Infrastructure sekmesinin pod envanteri durumları Clusters
// tablosunun İÇİNDE, bölüm başlığı ve sütunlar durur:
//   • pod'lar beklenirken iskelet satırı (eskiden sekme bütünüyle Spinner);
//   • eşleşme yoksa "Eşleşen pod yok" + tanı cümlesi ve (entity açıkken)
//     Pods sekmesi BAĞLANTISI aynı hücrede (P-1 `detail`) — bağlantı silinmez,
//     tablonun dışına da park edilmez;
//   • cluster'lar yanıt vermediyse "boş" DEĞİL hata satırı (eskiden "No pods
//     matched … nothing matched" diyordu);
//   • satırlar varken yenileme düşerse satırlar durur ama hata başlıktaki
//     "N cluster yanıt vermedi" rozetinde görünür (sessiz bayat satır yok);
//   • "No Thanos clusters configured" kapısı tablonun dışında kalır.
//
// v0.10.967 (review G5-1): kısmi hatada (bir cluster yanıt verip eşleşme
// bulamadı, diğeri düştü) hata satırı eski kutunun tanı + çare cümlesini
// korur; sayı YALNIZ yanıt veren cluster'lar. Hepsi düştüyse cümle yok.
// v0.10.967 (review G5-2): Thanos kaynak listesi okunamadıysa "No Thanos
// clusters configured" (yapılandırma eksik) DENMEZ; önbellekteki liste
// duruyorsa satırlar kalır, hata başlık rozetinde.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ClusterPodRow } from '@/lib/types';

const m = vi.hoisted(() => ({
  clusters: ['c1'] as string[],
  pods: [] as unknown[],
  failPods: new Set<string>(),
  holdPods: false,
  entity: false,
  failSources: false,
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      servicesMetadata: async () => ({}),
      clusterSources: async () => {
        if (m.failSources) throw new Error('503 cluster-sources');
        return { clusters: m.clusters };
      },
      clusterPods: (cluster: string) => {
        if (m.holdPods) return new Promise(() => { /* bekler */ });
        if (m.failPods.has(cluster)) return Promise.reject(new Error('502 thanos'));
        const pods = (m.pods as { cluster: string }[]).filter(p => p.cluster === cluster);
        return Promise.resolve({ cluster, pods, count: pods.length });
      },
      clusterDeployments: async (cluster: string, namespace: string) => ({ cluster, namespace, deployments: [], count: 0 }),
      clusters: async () => ({ clusters: [] }),
      entityClusters: async () => (m.entity ? { clusters: [] } : { disabled: true }),
      clusterDeployTrend: async () => ({ series: [] }),
      clusterHaproxyTrend: async () => ({ series: [] }),
    },
  };
});
vi.mock('./ServiceKafkaClientsPanel', () => ({ ServiceKafkaClientsPanel: () => null }));
vi.mock('@/pages/clusters/MetricArea', () => ({ MetricArea: () => null }));

import { ServiceInfraTab } from './ServiceInfraTab';
import { DATA_TABLE_STATE_TEXT } from '@/components/ui/DataTable';

// `service` = zenginleştirme eşleşmesi (podMatchesService'in ilk yolu).
const pod = (cluster: string, name: string): ClusterPodRow => ({
  cluster, namespace: 'ns1', pod: name, cpuCores: 0.5, memBytes: 1024, service: 'orders',
});
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
// Sorgu zinciri (metadata → kaynaklar → cluster başına pod) birkaç turda
// oturur: yükleniyor satırı gidene (ya da tur sınırına) kadar bekle.
async function settle(el: HTMLElement) {
  for (let i = 0; i < 20 && el.querySelector('tr[data-dt-state="loading"], .spinner, [role="status"]'); i++) await wait();
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/service?name=orders&tab=infra']}>
          <ServiceInfraTab service="orders" range={{ preset: '1h' }} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  if (!m.holdPods) await settle(host!);
  return host!;
}

const table = (el: HTMLElement) => el.querySelector('table') as HTMLTableElement | null;
const stateRow = (el: HTMLElement) => el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('tbody tr:not([data-dt-state])').length;
const statGrid = (el: HTMLElement) => el.querySelector('.stat-grid');

beforeEach(() => {
  m.clusters = ['c1']; m.pods = []; m.failPods = new Set(); m.holdPods = false; m.entity = false;
  m.failSources = false;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
});

describe('ServiceInfraTab — pod envanteri durumları Clusters tablosunun içinde (v0.10.967)', () => {
  it('pod\'lar beklenirken: başlık + sütunlar durur, tek loading satırı; KPI şeridi yok', async () => {
    m.holdPods = true;
    const el = await mount();
    expect(el.textContent).toContain('Clusters');
    expect(table(el)?.querySelector('thead')).not.toBeNull();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    // "0 / 1 cluster tarandı" ilerlemesi başlıkta görünür.
    expect(el.textContent).toContain('cluster tarandı');
    expect(statGrid(el)).toBeNull();
  });

  it('eşleşme yok: "empty" satırı tanı cümlesiyle; entity kapalıyken bağlantı yok', async () => {
    const el = await mount();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toContain('Eşleşen pod yok');
    expect(row?.textContent).toContain('pod adı kalıbı');
    expect(row?.textContent).toContain('(orders)-.*');
    // Tanı + çare cümlesi eskisiyle aynı: cluster sayısı ve adlandırma önerisi.
    expect(row?.textContent).toContain("1 Thanos cluster'ında");
    expect(row?.textContent).toContain('adlandırmasına uyduğunu');
    expect(row?.querySelector('a')).toBeNull();
    // colSpan = görünür kolon sayısı (8).
    expect(row?.querySelector('td')?.getAttribute('colspan')).toBe('8');
    expect(statGrid(el)).toBeNull();
    // Eski kutu gitti.
    expect(el.textContent).not.toContain('No pods matched');
  });

  it('eşleşme yok + entity açık: Pods sekmesi bağlantısı AYNI hücrede (P-1 detail)', async () => {
    m.entity = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    const link = row.querySelector('a');
    expect(link?.textContent).toBe('Pods sekmesinde');
    expect(link?.getAttribute('href')).toContain('tab=pods');
    // Bağlantı durum hücresinin içinde, tablonun dışında değil.
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(row.querySelector('[data-dt-state-detail]')?.contains(link!)).toBe(true);
  });

  it('cluster yanıt vermedi + satır yok: "boş" DEĞİL hata satırı (Pods bağlantısı korunur)', async () => {
    m.failPods = new Set(['c1']);
    m.entity = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain("Pod envanteri okunamadı: c1 cluster'ı sorguya yanıt vermedi");
    expect(row.textContent).not.toContain(DATA_TABLE_STATE_TEXT.empty);
    expect(row.textContent).not.toContain('Eşleşen pod yok');
    expect(row.querySelector('a')?.textContent).toBe('Pods sekmesinde');
    expect(el.textContent).toContain('1 cluster yanıt vermedi');
    // Hiçbir cluster yanıt vermedi → arama koşmadı; "denendi · adlandırmayı
    // kontrol et" cümlesi burada yanlış olurdu (G5-1).
    expect(row.textContent).not.toContain('adlandırmasına uyduğunu');
    expect(row.textContent).not.toContain('pod adı kalıbı');
  });

  it('hepsi düştü + entity kapalı: detail yuvası basılmaz (boş fragment yok)', async () => {
    m.failPods = new Set(['c1']);
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('kısmi hata (c1 yanıt verdi eşleşme yok, c2 düştü): hata satırı tanı + çare cümlesini korur (G5-1)', async () => {
    m.clusters = ['c1', 'c2'];
    // Eşleşmeyen pod: ne enrichment servisi ne iş yükü adı "orders".
    m.pods = [{ ...pod('c1', 'unrelated-5d9f7c6b8-abcde'), service: 'billing' }];
    m.failPods = new Set(['c2']);
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain("c2 cluster'ı sorguya yanıt vermedi");
    expect(row.textContent).toContain('pod adı kalıbı');
    expect(row.textContent).toContain('adlandırmasına uyduğunu');
    // Sayı yalnız yanıt veren cluster'lar (1), eşleşen kaynak sayısı (2) değil.
    expect(row.textContent).toContain("yanıt veren 1 Thanos cluster'ında");
    expect(row.textContent).not.toContain('2 Thanos cluster');
    expect(row.textContent).not.toContain('Eşleşen pod yok');
    // Tek hücre: cümle durum satırının içinde.
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(dataRows(el)).toBe(0);
  });

  it('satırlar varken: durum satırı yok, KPI şeridi var', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount();
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(1);
    expect(statGrid(el)).not.toBeNull();
  });

  it('bayat satır + yenileme düştü: satırlar durur, hata başlık rozetinde görünür', async () => {
    m.clusters = ['c1', 'c2'];
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde'), pod('c2', 'orders-5d9f7c6b8-fghij')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).not.toContain('cluster yanıt vermedi');
    m.failPods = new Set(['c2']);
    await act(async () => {
      await qc.refetchQueries({ queryKey: ['cluster-pods'] });
      await new Promise(r => setTimeout(r, 30));
    });
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).toContain('1 cluster yanıt vermedi');
  });

  it('"No Thanos clusters configured" kapısı tablonun DIŞINDA kalır', async () => {
    m.clusters = [];
    const el = await mount();
    expect(el.textContent).toContain('No Thanos clusters configured');
    expect(el.textContent).not.toContain('Thanos kaynakları okunamadı');
    expect(table(el)).toBeNull();
  });

  it('kaynak listesi okunamadı: "yapılandırılmamış" DEĞİL okuma hatası (G5-2)', async () => {
    m.failSources = true;
    const el = await mount();
    expect(el.textContent).toContain('Thanos kaynakları okunamadı');
    expect(el.textContent).toContain('okuma hatası');
    expect(el.textContent).toContain('503 cluster-sources');
    expect(el.textContent).not.toContain('No Thanos clusters configured');
    expect(table(el)).toBeNull();
  });

  it('kaynak listesi yenilemesi düştü, önbellekte cluster var: satırlar durur, hata rozette (G5-2)', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount();
    expect(dataRows(el)).toBe(1);
    expect(el.textContent).not.toContain('Thanos kaynaklarına erişilemedi');
    m.failSources = true;
    await act(async () => {
      await qc.refetchQueries({ queryKey: ['cluster-sources'] });
      await new Promise(r => setTimeout(r, 30));
    });
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(1);
    expect(el.textContent).toContain('Thanos kaynaklarına erişilemedi');
    expect(el.textContent).not.toContain('Thanos kaynakları okunamadı');
  });
});
