// @vitest-environment jsdom
//
// Pod.sections.test.tsx — S0 karakterizasyon (/clusters shadcn geçişi +
// iyileştirme planı docs/frontend/clusters-shadcn-plan.md §10.1 + §11 S0,
// operatör onayı 2026-11-14).
//
// NE ÇİVİLİYOR: /pod sayfası MONTE EDİLİNCE (hiçbir bölüm açılmadan) hangi
// uçları çağırdığı ve hangi bölümleri çizdiği. Sahte olan yalnız AĞ katmanı.
// Plan D8 / S10 bazı bölümleri kapalıya alır ("kapalı bölüm verisi açılana
// kadar ÇEKİLMEZ"); o dilim aşağıdaki "BUGÜN mount'ta" listesini bilerek
// daraltır — bu dosya daralmanın ölçüsüdür, sessizce değişmez.
//
// Bugünkü mount listesi (entity katmanı açık, cluster + servis verilmiş):
//   Thanos pod satırı (cluster-pods), tek-pod CPU/Bellek trendi + deploy
//   işaretleri, RED iki toplu sorgusu, servis anomalileri + sessize alma,
//   annotation şeridi, entity (kimlik, taşıdığı servisler, konteynerler),
//   trace listesi, JMX metrik KEŞFİ (trend değil).
// Mount'ta ÇEKİLMEYEN: JMX trendleri ve loglar (ikisi de açılınca).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

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

const POD = 'cart-7d9f-aaaaa';
const ENTITY_ID = `pod:c-1/shop/${POD}`;
const rec = (over: Record<string, unknown>) => ({
  type: 'pod', clusterId: 'c-1', id: ENTITY_ID, namespace: 'shop', name: POD, source: 'thanos',
  validFrom: '2026-11-14T00:00:00Z', firstSeen: '2026-11-14T00:00:00Z', lastSeen: '2026-11-14T10:00:00Z',
  ...over,
});

const m = vi.hoisted(() => ({ calls: [] as { name: string; args: unknown[] }[], entityDisabled: false }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  const real = mod.api as Record<string, (...a: unknown[]) => unknown>;
  const stub: Record<string, (...a: never[]) => unknown> = {
    clusterSources: async () => ({ clusters: ['prod-a'] }),
    clusterPods: async (cluster: string) => ({
      cluster, count: 1,
      pods: [{ cluster, namespace: 'shop', pod: 'cart-7d9f-aaaaa', cpuCores: 0.2, memBytes: 1e8, service: 'cart', phase: 'Running' }],
    }),
    clusterPodDetail: async () => ({ trend: [] }),
    serviceDeploys: async () => [],
    spanMetricBatch: async () => ({ series: {}, stepSeconds: 60 }),
    anomalyEvents: async () => ({ items: [], activeTotal: 0, clearedTotal: 0, truncated: false }),
    anomalySilences: async () => [],
    annotations: async () => [],
    entityClusters: async () => (m.entityDisabled
      ? { disabled: true }
      : { clusters: [{ id: 'c-1', name: 'prod-a', spanClusterValue: 'prod-a' }] }),
    entity: async () => ({
      entity: rec({ labels: { app: 'cart' } }), parents: [], children: {}, lifetimes: [rec({})],
      cluster: { id: 'c-1', name: 'prod-a' }, atMatch: true, siblings: [], containers: [],
    }),
    entityServices: async () => ({ entity: 'pod:c-1/shop/cart-7d9f-aaaaa', pods: [], services: [] }),
    entityContainers: async () => ({ entity: 'pod:c-1/shop/cart-7d9f-aaaaa', containers: [] }),
    traces: async () => ({ traces: [], total: 0 }),
    clusterJmxMetrics: async () => ({ metrics: ['jvm_memory_used_bytes'] }),
    clusterJmxTrend: async () => ({ series: [] }),
    logs: async () => ({ logs: [] }),
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
  CorePanelMulti: (p: { title?: string }) => <div data-cpm={p.title ?? ''} />,
}));

import PodPage from './Pod';

let host: HTMLElement | null = null;
let root: Root | null = null;
const tick = (ms = 20) => act(async () => { await new Promise(r => setTimeout(r, ms)); });
async function until(pred: () => boolean, label: string) {
  for (let i = 0; i < 150; i++) {
    if (pred()) return;
    await tick();
  }
  throw new Error(`beklenen durum oluşmadı: ${label}`);
}
// Kabuk çağrıları (Topbar ortam seçicisi — modül önbellekli, sayfa verisi değil).
const CHROME = new Set(['environments']);
const names = () => [...new Set(m.calls.map(c => c.name))].filter(n => !CHROME.has(n)).sort();
const count = (name: string) => m.calls.filter(c => c.name === name).length;

async function mount(url: string, settledOn: string): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><PodPage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await until(() => count(settledOn) > 0, `${settledOn} çağrısı`);
  // Bağımlı zincir (entity → konteynerler, keşif → paneller) otursun.
  for (let i = 0; i < 6; i++) await tick();
  return host!;
}
beforeEach(() => {
  m.calls.length = 0;
  m.entityDisabled = false;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

const URL_FULL = `/pod?cluster=prod-a&namespace=shop&pod=${POD}&service=cart&range=1h`;

describe('Pod S0 — mount\'ta çağrılan uçlar (entity katmanı açık)', () => {
  it('bugünkü mount listesi', async () => {
    await mount(URL_FULL, 'entityContainers');
    expect(names()).toEqual([
      'annotations',
      'anomalyEvents',
      'anomalySilences',
      'clusterJmxMetrics',
      'clusterPodDetail',
      'clusterPods',
      'entity',
      'entityClusters',
      'entityContainers',
      'entityServices',
      'serviceDeploys',
      'spanMetricBatch',
      'traces',
    ]);
  });

  it('RED iki toplu sorgu (throughput/hata + gecikme); karşılaştırma kapalıyken hayalet yok', async () => {
    await mount(URL_FULL, 'entityContainers');
    expect(count('spanMetricBatch')).toBe(2);
  });

  it('cluster verildiyse kaynak listesi aranmaz; pod satırı tek istekle', async () => {
    await mount(URL_FULL, 'entityContainers');
    expect(count('clusterSources')).toBe(0);
    expect(count('clusterPods')).toBe(1);
    expect(m.calls.find(c => c.name === 'clusterPods')!.args[0]).toBe('prod-a');
  });

  it('JMX: yalnız keşif; trendler ve loglar açılana dek çekilmez', async () => {
    await mount(URL_FULL, 'entityContainers');
    expect(count('clusterJmxMetrics')).toBe(1);
    expect(count('clusterJmxTrend')).toBe(0);
    expect(count('logs')).toBe(0);
  });

  it('tek-pod trendi adıyla sorulur (cluster, namespace, pod)', async () => {
    await mount(URL_FULL, 'entityContainers');
    const c = m.calls.find(x => x.name === 'clusterPodDetail')!;
    expect(c.args.slice(0, 3)).toEqual(['prod-a', 'shop', POD]);
  });
});

describe('Pod S0 — mount\'ta çizilen bölümler', () => {
  it('sıra: CPU/Bellek, servis metrikleri, taşıdığı servisler, trace listesi, konteynerler, kardeşler/etiketler/yaşam döngüsü, ek', async () => {
    const el = await mount(URL_FULL, 'entityContainers');
    const text = el.textContent ?? '';
    const order = [
      'CPU / Bellek · Thanos', 'Servis metrikleri · bu pod', 'Taşıdığı servisler', 'Trace listesi',
      'Konteynerler', 'Kardeş pod\'lar', 'Etiketler', 'Yaşam döngüsü', 'JVM / JBoss (JMX)', 'Prometheus sorguları',
    ];
    const at = order.map(t => text.indexOf(t));
    expect(at.every(i => i >= 0), JSON.stringify(order.filter((_, i) => at[i] < 0))).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    // RED üçlüsü açık çizilir.
    expect(Array.from(el.querySelectorAll<HTMLElement>('[data-cpm]')).map(n => n.dataset.cpm))
      .toEqual(['Response time', 'Throughput', 'Failure rate']);
  });

  it('kapalı JMX satırı keşfedilen metrik sayısını söyler', async () => {
    const el = await mount(URL_FULL, 'entityContainers');
    expect(el.textContent).toContain('1 metrik');
  });
});

describe('Pod S0 — entity katmanı kapalı', () => {
  it('entity uçları çağrılmaz; RED, trend, trace ve JMX keşfi yine çekilir', async () => {
    m.entityDisabled = true;
    const el = await mount(URL_FULL, 'traces');
    await until(() => count('clusterJmxMetrics') > 0, 'JMX keşfi');
    for (const n of ['entity', 'entityServices', 'entityContainers']) expect(count(n), n).toBe(0);
    for (const n of ['clusterPods', 'clusterPodDetail', 'spanMetricBatch', 'traces', 'clusterJmxMetrics']) {
      expect(count(n), n).toBeGreaterThan(0);
    }
    expect(el.textContent).not.toContain('Taşıdığı servisler');
    expect(el.textContent).not.toContain('Konteynerler');
  });
});

describe('Pod S0 — servis parametresi yok, entity kapalı', () => {
  it('RED sorgusu atılmaz ("eşlenmedi" ekranı); trend ve trace listesi çekilir', async () => {
    m.entityDisabled = true;
    const el = await mount(`/pod?cluster=prod-a&namespace=shop&pod=${POD}`, 'traces');
    expect(count('spanMetricBatch')).toBe(0);
    expect(count('anomalyEvents')).toBe(0);
    expect(el.textContent).toContain('Bu pod bir Coremetry servisine eşlenmedi');
  });
});
