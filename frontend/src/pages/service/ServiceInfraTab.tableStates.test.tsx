// @vitest-environment jsdom
// ServiceInfraTab.tableStates — v0.10.967 (tablo standardı T12, dilim 5, P-1);
// v0.10.2145 (servis sekmeleri S6, docs/frontend/service-tabs-plan.md; mockup
// operatör onayı 2026-11-13) yeniden yazıldı.
//
// NE ÇİVİLİYOR: Infrastructure'ın "Nerede çalışıyor" tablosu TEK tablodur:
//   • Thanos'un pod gördüğü cluster satırı sayılarıyla (5 kolon varsayılan;
//     Namespace / Restart / Kaynak / Eylemler "Tüm kolonlar" ile);
//   • yalnız span'lerin gördüğü cluster AYNI tabloda satır, boş hücre yerine
//     düz cümle ("Thanos'ta görünmüyor"); eski alttaki ikinci pod listesi yok;
//   • yanıt vermeyen cluster kendi satırı ("durum alınamıyor"), tıklanmaz;
//     önbellekten duran satırda Durum "son okuma başarısız" der;
//   • yükleniyor / boş durumu tablonun İÇİNDE, başlık durur;
//   • başlık rozetleri kalktı: söyledikleri sayfa altındaki kapalı "Kaynak"
//     satırında; okuma hatası o satırın KAPALI cümlesinde de adıyla görünür;
//   • "No Thanos clusters configured" kapısı yalnız gösterecek satır yokken.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ClusterPodRow, ServicePodRow } from '@/lib/types';

const m = vi.hoisted(() => ({
  clusters: ['c1'] as string[],
  pods: [] as unknown[],
  failPods: new Set<string>(),
  holdPods: false,
  entity: false,
  failSources: false,
  entityPods: [] as unknown[],
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
      servicePods: async () => ({ service: 'orders', pods: m.entityPods }),
      clusterDeployTrend: async () => ({ series: [] }),
      clusterHaproxyTrend: async () => ({ series: [] }),
    },
  };
});
vi.mock('./ServiceKafkaClientsPanel', () => ({ ServiceKafkaClientsPanel: () => null }));
// v0.10.1206 — runtime bölümü (uPlot, modül yüklemede matchMedia) bu testin konusu değil.
vi.mock('./ServiceRuntimeSection', () => ({ ServiceRuntimeSection: () => null }));
vi.mock('@/pages/clusters/MetricArea', () => ({ MetricArea: () => null }));
// Pod envanteri (satır altı) kendi testinde; burada yalnız hangi cluster için açıldığı.
vi.mock('./ServicePodInventory', () => ({ ServicePodInventory: ({ cluster }: { cluster: string }) => <div data-testid="inv">{cluster}</div> }));

import { ServiceInfraTab } from './ServiceInfraTab';
import { infraSourceSummary } from './InfraSourceNote';

// `service` = zenginleştirme eşleşmesi (podMatchesService'in ilk yolu).
const pod = (cluster: string, name: string): ClusterPodRow => ({
  cluster, namespace: 'ns1', pod: name, cpuCores: 0.5, memBytes: 1024, service: 'orders',
});
const entityPod = (cluster: string, name: string): ServicePodRow => ({
  cluster, namespace: 'ns1', pod: name, service: 'orders', spans: 10, errors: 0, avgMs: 5,
  firstSeen: '2026-09-27T00:00:00Z', lastSeen: '2026-09-27T00:05:00Z',
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
async function mount(url = '/service?name=orders&tab=infra'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
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
const heads = (el: HTMLElement) => [...el.querySelectorAll('thead th')].map(th => (th.textContent ?? '').replace(/[↕▼▲⋯]/g, '').trim()).filter(Boolean);
const rowOf = (el: HTMLElement, cluster: string) =>
  [...el.querySelectorAll('tbody tr')].find(tr => tr.querySelector('.cluster-name')?.textContent === cluster) as HTMLElement | undefined;
const sourceNote = (el: HTMLElement) => el.querySelector('#infra-source') as HTMLElement | null;
// v0.10.2144 (S5, mockup operatör onayı 2026-11-13) — KPI karoları kalktı; yerinde üst cümle bloğu.
const statGrid = (el: HTMLElement) => el.querySelector('.infra-sum');

beforeEach(() => {
  m.clusters = ['c1']; m.pods = []; m.failPods = new Set(); m.holdPods = false; m.entity = false;
  m.failSources = false; m.entityPods = [];
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  qc?.clear();
  host?.remove();
  host = null; root = null;
});

describe('ServiceInfraTab — "Nerede çalışıyor" tek tablo (S6)', () => {
  it('pod\'lar beklenirken: başlık + sütunlar durur, tek loading satırı; üst cümle yok', async () => {
    m.holdPods = true;
    const el = await mount();
    expect(el.textContent).toContain('Nerede çalışıyor');
    expect(table(el)?.querySelector('thead')).not.toBeNull();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    // "0 / 1 cluster tarandı" ilerlemesi başlıkta görünür.
    expect(el.textContent).toContain('cluster tarandı');
    expect(statGrid(el)).toBeNull();
  });

  it('eşleşme yok: "empty" satırı tanı + çare cümlesiyle', async () => {
    const el = await mount();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toContain('Eşleşen pod yok');
    expect(row?.textContent).toContain('pod adı kalıbı');
    expect(row?.textContent).toContain('(orders)-.*');
    expect(row?.textContent).toContain("1 Thanos cluster'ında");
    expect(row?.textContent).toContain('adlandırmasına uyduğunu');
    expect(row?.querySelector('a')).toBeNull();
    // colSpan = görünür kolon sayısı (varsayılan 5).
    expect(row?.querySelector('td')?.getAttribute('colspan')).toBe('5');
    expect(statGrid(el)).toBeNull();
  });

  it('varsayılan 5 kolon; başlık rozetleri ve kaynak rozeti yok', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount();
    expect(heads(el)).toEqual(['Cluster', 'Pod', 'CPU (cores)', 'Bellek', 'Durum']);
    const head = el.querySelector('#infra-clusters')!;
    expect(head.querySelector('.badge')).toBeNull();
    expect(head.textContent).not.toContain('Thanos · kube-state');
    expect(dataRows(el)).toBe(1);
    expect(stateRow(el)).toBeNull();
    expect(statGrid(el)).not.toBeNull();
  });

  it('"Tüm kolonlar": Namespace / Restart / Kaynak / Eylemler gelir', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount();
    const btn = [...el.querySelectorAll('button')].find(b => b.textContent === 'Tüm kolonlar')!;
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    await act(async () => { btn.click(); });
    // Eylemler kolonunun başlığı yalnız ⋯ (kind: 'actions').
    expect(heads(el)).toEqual(['Cluster', 'Pod', 'CPU (cores)', 'Bellek', 'Durum', 'Namespace', 'Restart', 'Kaynak']);
    expect(el.querySelectorAll('thead th').length).toBe(9);
    expect(rowOf(el, 'c1')?.querySelector('.badge.b-gray')?.textContent).toBe('T');
  });

  it('restart sayısı Durum hücresinde (Restart kolonu varsayılanda gizli)', async () => {
    m.pods = [{ ...pod('c1', 'orders-5d9f7c6b8-abcde'), phase: 'Running', restarts: 3 }];
    const el = await mount();
    expect(rowOf(el, 'c1')?.textContent).toContain('all running · 3 restart');
  });

  it('satır tıkı pod\'ları satırın altında açar (?icluster), ikinci tık kapatır', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount();
    expect(el.querySelector('[data-testid="inv"]')).toBeNull();
    await act(async () => { rowOf(el, 'c1')!.click(); });
    expect(el.querySelector('[data-testid="inv"]')?.textContent).toBe('c1');
    expect(rowOf(el, 'c1')?.getAttribute('aria-expanded')).toBe('true');
    await act(async () => { rowOf(el, 'c1')!.click(); });
    expect(el.querySelector('[data-testid="inv"]')).toBeNull();
  });

  // v0.10.2206 — satır tıkı gezinmez, açar/kapar; yalnız ↗ cluster detayına gider
  // (satır içi ikon, block bağlantı değil); klavye Enter/Space aynı geçiş.
  it('satır genişletir (aria-expanded, Enter/Space); yalnız ↗ cluster detayına bağlanır', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount();
    const row = rowOf(el, 'c1')!;
    expect(row.getAttribute('aria-expanded')).toBe('false');
    expect(row.getAttribute('role')).toBe('button');
    const links = [...row.querySelectorAll('a')];
    expect(links).toHaveLength(1);
    expect(links[0].className).toBe('infra-where-open');
    expect(links[0].getAttribute('aria-label')).toBe('c1 cluster detayı');
    expect(row.querySelector('.row-link')).toBeNull();
    await act(async () => { row.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    expect(rowOf(el, 'c1')!.getAttribute('aria-expanded')).toBe('true');
    await act(async () => { rowOf(el, 'c1')!.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true })); });
    expect(rowOf(el, 'c1')!.getAttribute('aria-expanded')).toBe('false');
  });

  it('eski linkteki ?psrc / ?pview yok sayılır; ?icluster çalışır', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount('/service?name=orders&tab=infra&psrc=entity&pview=flat&icluster=c1');
    expect(dataRows(el)).toBe(2); // cluster satırı + açık envanter satırı
    expect(el.querySelector('[data-testid="inv"]')?.textContent).toBe('c1');
  });

  it('yalnız span\'lerin gördüğü cluster AYNI tabloda satır: düz cümle, boş hücre yok, açılır', async () => {
    m.entity = true;
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    m.entityPods = [entityPod('c9', 'orders-7f9-aaa'), entityPod('c9', 'orders-7f9-bbb')];
    const el = await mount();
    const row = rowOf(el, 'c9')!;
    expect(row.querySelectorAll('td').length).toBe(2);
    expect(row.querySelectorAll('td')[1].getAttribute('colspan')).toBe('4');
    expect(row.textContent).toContain("2 pod (span'lerden) · Thanos'ta görünmüyor — kaynak ve durum bilinmiyor");
    // Alttaki ikinci pod listesi yok.
    expect(el.querySelector('#pods-sec')).toBeNull();
    expect(el.textContent).not.toContain("Diğer cluster'larda");
    await act(async () => { row.click(); });
    expect(el.querySelector('[data-testid="inv"]')?.textContent).toBe('c9');
  });

  it('Thanos eşleşmedi ama span\'ler pod görüyor: satır var, tanı cümlesi tablonun altında', async () => {
    m.entity = true;
    m.entityPods = [entityPod('c9', 'orders-7f9-aaa')];
    const el = await mount();
    expect(stateRow(el)).toBeNull();
    expect(rowOf(el, 'c9')).toBeDefined();
    expect(el.textContent).toContain("Thanos'ta eşleşen pod yok.");
    expect(el.textContent).toContain("yanıt veren 1 Thanos cluster'ında");
    expect(statGrid(el)).toBeNull();
  });

  it('yanıt vermeyen cluster kendi satırı: "durum alınamıyor", tıklanmaz; "boş" denmez', async () => {
    m.failPods = new Set(['c1']);
    const el = await mount();
    expect(stateRow(el)).toBeNull();
    const row = rowOf(el, 'c1')!;
    expect(row.textContent).toContain('durum alınamıyor — cluster sorguya yanıt vermedi');
    expect(row.getAttribute('role')).toBeNull();
    expect(row.querySelector('.pods-caret')).toBeNull();
    expect(el.textContent).not.toContain('Eşleşen pod yok');
    // Hiçbir cluster yanıt vermedi → arama koşmadı; "denendi" cümlesi yanlış olurdu (G5-1).
    expect(el.textContent).not.toContain('adlandırmasına uyduğunu');
    // Kapalı "Kaynak" satırı hatayı adıyla söyler.
    expect(sourceNote(el)?.textContent).toContain('1 cluster yanıt vermedi');
  });

  it('kısmi hata (c1 yanıt verdi eşleşme yok, c2 düştü): c2 satır, tanı yalnız yanıt verenleri sayar (G5-1)', async () => {
    m.clusters = ['c1', 'c2'];
    m.pods = [{ ...pod('c1', 'unrelated-5d9f7c6b8-abcde'), service: 'billing' }];
    m.failPods = new Set(['c2']);
    const el = await mount();
    expect(rowOf(el, 'c2')?.textContent).toContain('durum alınamıyor');
    expect(rowOf(el, 'c1')).toBeUndefined();
    expect(dataRows(el)).toBe(1);
    expect(el.textContent).toContain("yanıt veren 1 Thanos cluster'ında");
    expect(el.textContent).toContain('adlandırmasına uyduğunu');
    expect(el.textContent).not.toContain('2 Thanos cluster');
  });

  // v0.10.2144 (S5) — dört KPI karosu yerine düz cümle + kaynak cümlesi;
  // "Dikkat isteyenler" yalnız sapma varken.
  it('S5: üst cümle + kaynak cümlesi; karo yok; sapma yokken Dikkat kutusu yok', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    const el = await mount();
    expect(el.querySelector('.stat-grid')).toBeNull();
    const sum = statGrid(el)!;
    expect(sum.textContent).toContain("1 pod, 1 cluster'da çalışıyor.");
    expect(sum.textContent).toContain('CPU');
    expect(sum.textContent).toContain('Bellek');
    expect(el.querySelector('.infra-attn')).toBeNull();
  });

  it('S5: restart eden pod "Dikkat isteyenler"de; "pod\'ları aç" cluster satırını seçer (?icluster)', async () => {
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde'), { ...pod('c1', 'orders-5d9f7c6b8-zzzzz'), phase: 'Running', restarts: 3 }];
    const el = await mount();
    const attn = el.querySelector('.infra-attn')!;
    expect(attn.textContent).toContain('Dikkat isteyenler');
    expect(attn.textContent).toContain('3 kez yeniden başladı');
    expect(attn.querySelectorAll('li').length).toBe(1);
    const open = [...attn.querySelectorAll('button')].find(b => b.textContent?.includes("pod'ları aç"))!;
    await act(async () => { open.click(); });
    expect(el.querySelector('tbody tr[aria-expanded="true"]')).not.toBeNull();
  });

  // 2026-11-21 (operatör): Thanos'ta görünmeyen pod Dikkat kutusuna girmez.
  it('Thanos\'ta görünmeyen pod tek başına Dikkat kutusu açmaz', async () => {
    m.entity = true;
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    m.entityPods = [entityPod('c9', 'orders-7f9-aaa')];
    const el = await mount();
    expect(el.querySelector('.infra-attn')).toBeNull();
  });

  it('bayat satır + yenileme düştü: satır durur, Durum "son okuma başarısız" der', async () => {
    m.clusters = ['c1', 'c2'];
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde'), pod('c2', 'orders-5d9f7c6b8-fghij')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).not.toContain('son okuma başarısız');
    m.failPods = new Set(['c2']);
    await act(async () => {
      await qc.refetchQueries({ queryKey: ['cluster-pods'] });
      await new Promise(r => setTimeout(r, 30));
    });
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(2);
    expect(rowOf(el, 'c2')?.textContent).toContain('son okuma başarısız');
    expect(rowOf(el, 'c1')?.textContent).not.toContain('son okuma başarısız');
    expect(statGrid(el)?.textContent).toContain("1 cluster'dan durum alınamıyor");
  });

  it('"No Thanos clusters configured" kapısı tablonun DIŞINDA kalır', async () => {
    m.clusters = [];
    const el = await mount();
    expect(el.textContent).toContain('No Thanos clusters configured');
    expect(el.textContent).not.toContain('Thanos kaynakları okunamadı');
    expect(table(el)).toBeNull();
  });

  it('Thanos tanımlı değil ama span\'ler pod görüyor: kutu yerine tablo, satır bunu söyler', async () => {
    m.clusters = [];
    m.entity = true;
    m.entityPods = [entityPod('c9', 'orders-7f9-aaa')];
    const el = await mount();
    expect(el.textContent).not.toContain('No Thanos clusters configured');
    expect(rowOf(el, 'c9')?.textContent).toContain("1 pod (span'lerden) · Thanos cluster'ı tanımlı değil — kaynak ve durum bilinmiyor");
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

  it('kaynak listesi yenilemesi düştü, önbellekte cluster var: satırlar durur, hata kapalı "Kaynak" satırında (G5-2)', async () => {
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
    expect(sourceNote(el)?.textContent).toContain('Thanos kaynaklarına erişilemedi');
    expect(el.textContent).not.toContain('Thanos kaynakları okunamadı');
  });

  it('"Kaynak" satırı sayfanın altında kapalı; açınca eşleşme kuralı ve kaynak sayıları', async () => {
    m.entity = true;
    m.pods = [pod('c1', 'orders-5d9f7c6b8-abcde')];
    m.entityPods = [entityPod('c9', 'orders-7f9-aaa')];
    const el = await mount();
    const note = sourceNote(el)!;
    expect(note.textContent).toContain('Kaynak');
    expect(el.querySelector('.infra-src')).toBeNull();
    await act(async () => { note.querySelector('button')!.click(); });
    const body = el.querySelector('.infra-src')!;
    expect(body.textContent).toContain('pod adı: (orders)-.*');
    expect(body.textContent).toContain('1 / 1 cluster tarandı.');
    expect(body.textContent).toContain("1 yalnız span'lerde (E)");
    expect(body.textContent).toContain("1 yalnız Thanos'ta (T)");
  });
});

describe('infraSourceSummary — kapalı satır okuma hatasını gizlemez', () => {
  const none = { unmapped: [], statusNotes: [], truncated: [], unreachable: [], sourcesError: '', entityError: '' };
  it('sorun yok: düz açıklama', () => {
    expect(infraSourceSummary(none)).toContain('ayrıntı için aç');
    expect(infraSourceSummary(none)).not.toContain('yanıt vermedi');
  });
  it('sorunlar adıyla', () => {
    expect(infraSourceSummary({ ...none, unreachable: ['a', 'b'], truncated: ['c'], entityError: 'x', unmapped: ['u'] }))
      .toBe('2 cluster yanıt vermedi · entity katmanı okunamadı · Thanos kısmi (1 cluster) · 1 eşlenmemiş cluster değeri — ayrıntı için aç.');
  });
});
