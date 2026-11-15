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
// v0.10.2178 (Clusters S10, D8) — daraldı. Mount listesi (entity katmanı
// açık, cluster + servis verilmiş): Thanos pod satırı (cluster-pods), tek-pod
// CPU/Bellek trendi + deploy işaretleri, entity (kimlik, taşıdığı servisler —
// v0.10.190 gereği hep görünür —, konteynerler).
// Mount'ta ÇEKİLMEYEN (kapalı bölüm açılınca): trace kapsamı + listesi (Son
// trace'ler), JMX keşfi ve trendleri (Ağ ve JVM), loglar. v0.10.2195 — İstek
// metrikleri (RED) bölümü kalktı; trace kapsamı entity modelinden.
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

const m = vi.hoisted(() => ({ calls: [] as { name: string; args: unknown[] }[], entityDisabled: false,
  scope: { mode: 'pod', clusterValue: 'prod-a', workload: 'wl:c-1/shop/Deployment/cart' } as { mode: string; clusterValue?: string; service?: string; workload?: string } }));
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
    podTraceScope: async () => m.scope,
    anomalyEvents: async () => ({ items: [], activeTotal: 0, clearedTotal: 0, truncated: false }),
    anomalySilences: async () => [],
    annotations: async () => [],
    entityClusters: async () => (m.entityDisabled
      ? { disabled: true }
      : { clusters: [{ id: 'c-1', name: 'prod-a', spanClusterValue: 'prod-a' }] }),
    entity: async () => ({
      entity: rec({ labels: { app: 'cart' } }), parents: [rec({ type: 'workload', id: 'wl:c-1/shop/Deployment/cart', name: 'cart', labels: { kind: 'Deployment' } })], children: {}, lifetimes: [rec({})],
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
// Kapalı bölümü başlık düğmesinden açar (CollapsedSection tetikleyicisi).
async function openSection(el: HTMLElement, title: string) {
  const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.trim() === title)!;
  await act(async () => { btn.click(); await new Promise(r => setTimeout(r, 20)); });
}
// Kalıcılık testi için bellek içi localStorage (jsdom deposu bu ortamda işlevsiz).
function memoryStorage(): Storage {
  const s = new Map<string, string>();
  return {
    get length() { return s.size; },
    clear: () => s.clear(),
    getItem: k => s.get(k) ?? null,
    key: i => [...s.keys()][i] ?? null,
    removeItem: k => { s.delete(k); },
    setItem: (k, v) => { s.set(k, String(v)); },
  };
}
beforeEach(() => {
  m.calls.length = 0;
  m.entityDisabled = false;
  m.scope = { mode: 'pod', clusterValue: 'prod-a', workload: 'wl:c-1/shop/Deployment/cart' };
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

const URL_FULL = `/pod?cluster=prod-a&namespace=shop&pod=${POD}&service=cart&range=1h`;

describe('Pod S0 — mount\'ta çağrılan uçlar (entity katmanı açık)', () => {
  it('S10 mount listesi: kapalı bölümlerin (RED, trace, JMX) verisi yok', async () => {
    await mount(URL_FULL, 'entityContainers');
    expect(names()).toEqual([
      'clusterPodDetail',
      'clusterPods',
      'entity',
      'entityClusters',
      'entityContainers',
      'entityServices',
      'serviceDeploys',
    ]);
  });

  it('Son trace\'ler açılınca trace listesi çekilir; açık bölüm kalıcı', async () => {
    vi.stubGlobal('localStorage', memoryStorage());
    const el = await mount(URL_FULL, 'entityContainers');
    expect(count('traces')).toBe(0);
    await openSection(el, 'Son trace\'ler');
    await until(() => count('traces') > 0, 'trace listesi');
    expect(localStorage.getItem('pod.sections.open')).toBe('["traces"]');
    vi.unstubAllGlobals();
  });

  it('cluster verildiyse kaynak listesi aranmaz; pod satırı tek istekle', async () => {
    await mount(URL_FULL, 'entityContainers');
    expect(count('clusterSources')).toBe(0);
    expect(count('clusterPods')).toBe(1);
    expect(m.calls.find(c => c.name === 'clusterPods')!.args[0]).toBe('prod-a');
  });

  it('JMX: keşif de trendler de loglar da açılana dek çekilmez; Ağ ve JVM açılınca keşif + jmxTitle', async () => {
    const el = await mount(URL_FULL, 'entityContainers');
    expect(count('clusterJmxMetrics')).toBe(0);
    expect(count('clusterJmxTrend')).toBe(0);
    expect(count('logs')).toBe(0);
    await openSection(el, 'Ağ ve JVM');
    await until(() => count('clusterJmxTrend') > 0, 'JMX trendi');
    expect(count('clusterJmxMetrics')).toBe(1);
    expect(el.textContent).toContain('JVM (JMX)');
    expect(el.textContent).not.toContain('JVM / JBoss (JMX)');
  });

  it('tek-pod trendi adıyla sorulur (cluster, namespace, pod)', async () => {
    await mount(URL_FULL, 'entityContainers');
    const c = m.calls.find(x => x.name === 'clusterPodDetail')!;
    expect(c.args.slice(0, 3)).toEqual(['prod-a', 'shop', POD]);
  });
});

describe('Pod S0 — mount\'ta çizilen bölümler', () => {
  it('S10/S11 sırası: durum cümlesi, meta satırı, CPU/Bellek, taşıdığı servisler, konteynerler, kardeşler, kapalı bölümler', async () => {
    const el = await mount(URL_FULL, 'entityContainers');
    const text = el.textContent ?? '';
    const order = [
      'Sağlıklı', 'CoSRE\'ye sor', 'CPU / Bellek · Thanos', 'Taşıdığı servisler', 'Konteynerler',
      'Son trace\'ler', 'Ağ ve JVM', 'Etiketler ve geçmiş',
    ];
    const at = order.map(t => text.indexOf(t));
    expect(at.every(i => i >= 0), JSON.stringify(order.filter((_, i) => at[i] < 0))).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    // KPI şeridi ve RED üçlüsü yok (v0.10.2195).
    expect(el.querySelectorAll('[data-cpm]').length).toBe(0);
    expect(text).not.toContain('İstek metrikleri');
    expect(text).not.toContain('Trace listesi');
    // Clusters S11 — durum cümlesi; kardeş tablosu kapalı bölümde, kardeş yokken kıyas cümlesi yok.
    expect(el.querySelector('.pod-status')?.textContent).toBe('SağlıklıÇalışıyor.');
    expect(text).not.toContain('Kardeş pod\'lar');
    expect(text).not.toContain('Aynı iş yükündeki diğer pod');
  });

  it('meta satırı: servis, iş yükü (entity modeli, S2), yaş; sürüm etiketi yoksa çizilmez', async () => {
    const el = await mount(URL_FULL, 'entityContainers');
    const meta = el.querySelector('.pod-meta')!.textContent ?? '';
    expect(meta).toContain('Serviscart');
    expect(meta).toContain('İş yükü');
    expect(meta).toContain('Yaş');
    expect(meta).not.toContain('Sürüm');
  });

  it('CoSRE\'ye sor: çekmeceyi bağlamlı soruyla doldurur (göndermez)', async () => {
    const el = await mount(URL_FULL, 'entityContainers');
    const got: string[] = [];
    const h = (e: Event) => got.push((e as CustomEvent<{ question?: string }>).detail.question ?? '');
    window.addEventListener('coremetry:ai-prefill', h);
    const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('CoSRE\'ye sor'))!;
    await act(async () => { btn.click(); });
    window.removeEventListener('coremetry:ai-prefill', h);
    expect(got).toEqual([`${POD} pod'unun durumu nedir, bir sorun var mı? (cluster prod-a, namespace shop, servis cart)`]);
  });
});

describe('Pod S0 — entity katmanı kapalı', () => {
  it('entity uçları çağrılmaz; trend yine çekilir', async () => {
    m.entityDisabled = true;
    const el = await mount(URL_FULL, 'clusterPodDetail');
    for (const n of ['entity', 'entityServices', 'entityContainers']) expect(count(n), n).toBe(0);
    for (const n of ['clusterPods', 'clusterPodDetail']) {
      expect(count(n), n).toBeGreaterThan(0);
    }
    expect(el.textContent).not.toContain('Taşıdığı servisler');
    expect(el.textContent).not.toContain('Konteynerler');
  });
});

describe('Pod — trace kapsamı entity modelinden (v0.10.2195)', () => {
  it('pod kenarı: k8s.pod.name + span\'in cluster değeri', async () => {
    m.scope = { mode: 'pod', clusterValue: 'PROD-A' };
    const el = await mount(`/pod?cluster=prod-a&namespace=shop&pod=${POD}`, 'clusterPodDetail');
    await openSection(el, 'Son trace\'ler');
    await until(() => count('traces') > 0, 'trace listesi');
    expect(count('podTraceScope')).toBe(1);
    const p = m.calls.find(c => c.name === 'traces')!.args[0] as Record<string, unknown>;
    expect(p.cluster).toBe('PROD-A');
    expect(JSON.parse(p.filters as string)).toEqual([{ k: 'k8s.pod.name', op: '=', v: [POD] }]);
  });
  it('pod span\'i yok → iş yükünün servisi, "servis geneli" etiketi, pod süzgeci yok', async () => {
    m.scope = { mode: 'service', clusterValue: 'prod-a', service: 'cart' };
    const el = await mount(`/pod?cluster=prod-a&namespace=shop&pod=${POD}`, 'clusterPodDetail');
    await openSection(el, 'Son trace\'ler');
    await until(() => count('traces') > 0, 'trace listesi');
    const p = m.calls.find(c => c.name === 'traces')!.args[0] as Record<string, unknown>;
    expect(p.service).toBe('cart');
    expect(p.filters).toBeUndefined();
    expect(el.textContent).toContain('servis geneli');
  });
  it('pencerede hatalı trace yoksa varsayılan Tümü (hasError\'suz sorgu)', async () => {
    m.scope = { mode: 'pod', clusterValue: 'prod-a' };
    const el = await mount(`/pod?cluster=prod-a&namespace=shop&pod=${POD}`, 'clusterPodDetail');
    await openSection(el, 'Son trace\'ler');
    await until(() => m.calls.some(c => c.name === 'traces' && !(c.args[0] as Record<string, unknown>).hasError), 'Tümü sorgusu');
  });
});
