// @vitest-environment jsdom
//
// Clusters.list.render.test.tsx — Clusters S4 (v0.10.2169; plan
// docs/frontend/clusters-shadcn-plan.md §0 D1, mockup "Clusters · liste").
//
// Liste görünümünün üç farkı çivilenir (kart düzeni aynı):
//   1. filo alarm şeridi yok; yerine kart durumlarından kurulan tek cümle;
//   2. çubuğun yanında yüzde yerine "kullanılan / kapasite · %";
//   3. durum düz sözcük; yanıt vermeyen kart düz cümle + "Bağlantı ayarını aç".
// Cümlenin ve metinlerin kendisi fleetSummary.test.ts'te; burada sayfanın
// onları doğru veriyle ve doğru bağlantılarla çizdiği ölçülür.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ClusterSummary } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

const GiB = 1024 ** 3;
const m = vi.hoisted(() => ({
  sources: [] as string[],
  observed: [] as string[],
  summaries: {} as Record<string, unknown>,
}));
const calls = vi.hoisted(() => ({
  clusterSources: async () => ({ clusters: m.sources }),
  clusters: async () => ({ clusters: m.observed }),
  clusterSummary: async (cluster: string) => {
    const s = m.summaries[cluster];
    if (s === 'down') throw new Error('HTTP 502: bad gateway');
    return s ?? { cluster };
  },
  // v0.10.2185 — genel görünüm tek yanıttan; bozuk cluster kaydı `error` taşır.
  clusterFleetSummary: async () => ({
    generated_at: new Date().toISOString(), refresh_seconds: 60,
    clusters: m.sources.map(name => {
      const s = m.summaries[name];
      return s === 'down' ? { name, error: 'HTTP 502: bad gateway' } : { name, summary: s ?? { cluster: name } };
    }),
  }),
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), ...calls } };
});

// v0.10.2186 — arayüz dili testten seçilir (varsayılan EN, marka/kullanıcı seçimi yok).
const ui = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useLang: () => ui.lang };
});

import ClustersPage from './Clusters';

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
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  // Sayfa sorguları `retry: 1` taşır; bekleme 0 → hata hemen düşer.
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/clusters?range=1h']}><ClustersPage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  // Her kartın summary'si (hata dahil, bir yeniden deneme ile) otursun.
  await until(() => host!.querySelectorAll('.cl-card').length === m.sources.length
    && !host!.querySelector('.cl-card .spinner, .cl-card [role="progressbar"]')
    && !(host!.querySelector('.cl-fleet')?.textContent ?? '').includes('yükleniyor'), 'kartlar yüklendi');
  return host!;
}
const card = (el: HTMLElement, name: string) =>
  Array.from(el.querySelectorAll<HTMLElement>('.cl-card')).find(c => c.querySelector('.cluster-name')?.textContent === name)!;
const sum = (cluster: string, over: Partial<ClusterSummary> = {}): ClusterSummary => ({
  cluster, nodes: 12, pods: 1204,
  cpuUsedCores: 148.3, cpuCapacityCores: 320,
  memUsedBytes: 922 * GiB, memCapacityBytes: 1.28 * 1024 * GiB,
  ...over,
});

beforeEach(() => {
  m.sources = ['prod-a', 'prod-b', 'prod-c', 'prod-d'];
  m.observed = ['prod-a', 'prod-b', 'prod-c', 'prod-d'];
  m.summaries = {
    'prod-a': sum('prod-a', { alertsCritical: 2, alertsWarning: 1 }),
    'prod-b': sum('prod-b', { alertsWarning: 3 }),
    'prod-c': sum('prod-c', { nodes: 6, pods: 212, cpuUsedCores: 22, cpuCapacityCores: 96, memUsedBytes: 118 * GiB, memCapacityBytes: 384 * GiB }),
    'prod-d': 'down',
  };
  ui.lang = 'en';
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('Clusters S4 — filo cümlesi', () => {
  it('alarm şeridi yok; kart durumlarından tek cümle', async () => {
    const el = await mount();
    expect(el.querySelector('.cl-fleet')!.textContent)
      .toBe("4 cluster: 2 sağlıklı, 1'inde kritik alarm var, 1'i yanıt vermiyor.");
    expect(el.textContent).not.toContain('firing across the fleet');
    expect(el.querySelector('.pulse-dot')).toBeNull();
  });

  it('hepsi sağlıklıysa kısa cümle', async () => {
    m.sources = ['prod-b', 'prod-c'];
    const el = await mount();
    expect(el.querySelector('.cl-fleet')!.textContent).toBe('2 cluster: hepsi sağlıklı.');
  });
});

describe('Clusters S4 — kart: üç sayı + iki çubuk + durum sözcüğü', () => {
  it('kritik alarmlı kart: sayı + sözcük, kart yine detaya bağlantı', async () => {
    const el = await mount();
    const c = card(el, 'prod-a');
    expect(c.tagName).toBe('A');
    expect(new URLSearchParams(c.getAttribute('href')!.split('?')[1]).get('cluster')).toBe('prod-a');
    expect(c.classList.contains('cl-card--crit')).toBe(true);
    expect(c.querySelector('.cl-status')!.textContent).toBe('2 kritik alarm');
    expect(c.querySelector('.cl-status')!.classList.contains('cl-status--crit')).toBe(true);
    // Üç sayı: Nodes / Pods / Alerts (kritik + uyarı).
    expect(Array.from(c.querySelectorAll('.cl-kpi__label')).map(n => n.textContent)).toEqual(['Nodes', 'Pods', 'Alerts']);
    expect(Array.from(c.querySelectorAll('.cl-kpi__val')).map(n => n.textContent)).toEqual(['12', '1.2K', '3']);
  });

  it('yalnız uyarı düzeyi alarm kartı sağlıksız yapmaz (eski rozet kuralı)', async () => {
    const el = await mount();
    const c = card(el, 'prod-b');
    expect(c.querySelector('.cl-status')!.textContent).toBe('sağlıklı');
    expect(c.querySelector('.cl-status')!.className).toBe('cl-status');
    expect(c.classList.contains('cl-card--crit')).toBe(false);
  });

  it('çubuğun yanında "kullanılan / kapasite · %"; eşik üstü değer tonlu', async () => {
    const el = await mount();
    const rows = Array.from(card(el, 'prod-a').querySelectorAll<HTMLElement>('.cl-usage__row'));
    expect(rows.map(r => r.querySelector('.cl-usage__text')!.textContent)).toEqual([
      'CPU148 / 320 cores · 46%',
      'Memory922 GiB / 1.28 TiB · 70%',
    ]);
    expect(rows[0].querySelector('.cl-usage__val')!.className).toBe('cl-usage__val');
    expect(rows[1].querySelector('.cl-usage__val')!.className).toBe('cl-usage__val cl-usage__val--warn');
    expect(rows.every(r => r.querySelector('.cl-bar'))).toBe(true);
    // Birim aynıysa bir kez yazılır.
    expect(card(el, 'prod-c').querySelectorAll('.cl-usage__val')[1].textContent).toBe('118 / 384 GiB · 31%');
  });

  it('kapasite yoksa yalnız kullanılan; kullanım da yoksa çubuk satırı yok', async () => {
    m.sources = ['prod-b', 'prod-c'];
    m.summaries['prod-b'] = sum('prod-b', { cpuCapacityCores: undefined, memCapacityBytes: undefined });
    m.summaries['prod-c'] = { cluster: 'prod-c', nodes: 3, pods: 40 };
    const el = await mount();
    expect(Array.from(card(el, 'prod-b').querySelectorAll('.cl-usage__val')).map(n => n.textContent))
      .toEqual(['148 cores', '922 GiB']);
    expect(card(el, 'prod-c').querySelector('.cl-usage')).toBeNull();
    expect(card(el, 'prod-c').querySelectorAll('.cl-kpi__val').length).toBe(3);
  });
});

// v0.10.2186 — CPU birimi ve yüzde biçimi arayüz dilinden (lib/i18n): EN
// varsayılan "cores · 46%", TR "çekirdek · %46".
describe('Clusters — CPU metni arayüz dilinde', () => {
  it('Türkçe arayüz: çekirdek · %46', async () => {
    ui.lang = 'tr';
    const el = await mount();
    expect(Array.from(card(el, 'prod-a').querySelectorAll('.cl-usage__val')).map(n => n.textContent))
      .toEqual(['148 / 320 çekirdek · %46', '922 GiB / 1.28 TiB · %70']);
  });
});

describe('Clusters S4 — yanıt vermeyen cluster', () => {
  it('düz cümle + "Bağlantı ayarını aç"; kart tıklanabilir değil, ad detaya gider', async () => {
    const el = await mount();
    const c = card(el, 'prod-d');
    expect(c.tagName).toBe('DIV');
    expect(c.classList.contains('card-link')).toBe(false);
    expect(c.querySelector('.cl-status')!.textContent).toBe('yanıt vermiyor');
    expect(c.querySelector('.cl-card__note')!.textContent)
      .toBe("Cluster metriklerine ulaşılamıyor. Span'ler gelmeye devam ediyor.");
    const links = Array.from(c.querySelectorAll<HTMLAnchorElement>('a'));
    expect(links.map(a => a.textContent)).toEqual(['prod-d', 'Bağlantı ayarını aç']);
    expect(new URLSearchParams(links[0].getAttribute('href')!.split('?')[1]).get('cluster')).toBe('prod-d');
    expect(links[1].getAttribute('href')).toBe('/settings/clusters');
    // Eski jargon gitti.
    expect(el.textContent).not.toContain('Thanos Querier unreachable');
    expect(c.querySelector('.cl-kpis')).toBeNull();
  });

  it('ad telemetride görülmüyorsa "span geliyor" denmez', async () => {
    m.observed = ['prod-a', 'prod-b', 'prod-c'];
    const el = await mount();
    expect(card(el, 'prod-d').querySelector('.cl-card__note')!.textContent)
      .toBe('Cluster metriklerine ulaşılamıyor.');
  });

  it('erişilebilir ama telemetride görülmeyen kart rozetini korur', async () => {
    m.observed = ['prod-a'];
    const el = await mount();
    expect(card(el, 'prod-b').querySelector('.badge')!.textContent).toBe('not in telemetry');
    expect(card(el, 'prod-a').querySelector('.badge')).toBeNull();
  });
});
