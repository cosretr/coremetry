// @vitest-environment jsdom
//
// ServiceGitOpsTab.states — v0.10.981 (servis GitOps sekmesi).
//
// NE ÇİVİLİYOR:
//   • Yükleniyor / hata / Argo yapılandırılmamış / Rollouts kapalı durumları
//     tabloların İÇİNDE (T12); başlıklar durur.
//   • Dolu cevapta Argo uygulaması satırı (sync/health rozeti, eşleme etiketi,
//     24 sa senkron özeti) ve rollout satırı — satırın yanında o iş yüküne
//     eşlenen Argo uygulaması + Rollouts çekmecesine bağlantı.
//   • Hub hatası sayfayı düşürmez: o hub için tek satırlık not.
//   • v0.10.985 — Argo kaynağı başlık rozetinde ve meta'da: "mapper" eşleyici
//     tablosunu, "live" canlı Thanos'u söyler; source yoksa (eski cevap) rozet
//     bugünkü gibi Thanos, meta'da kaynak notu yok. Eşleyici notu kutuda.
//
// NEDEN GERÇEK MOUNT: yalnız veri kancası sahte, tablo/primitifler gerçek.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ServiceGitOpsResponse, WorkloadRollout } from '@/lib/types';

interface FakeQuery<T> { data: T | undefined; isPending: boolean; error: Error | null; refetch: () => Promise<unknown> }
const h = vi.hoisted(() => ({
  q: { data: undefined, isPending: true, error: null, refetch: () => Promise.resolve() } as FakeQuery<unknown>,
}));

vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useServiceGitOps: () => h.q };
});

import { ServiceGitOpsTab } from './ServiceGitOpsTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const W = { clusterId: 'c1', namespace: 'shop', workload: 'checkout' };
const ROLLOUT: WorkloadRollout = {
  ...W, kind: 'Deployment', revision: 'checkout-7f9c', startedAt: 1_790_000_000_000, status: 'completed',
  prevRevision: 'checkout-6a1b', image: 'reg/checkout', imageTag: '1.4.2', prevImage: 'reg/checkout', prevImageTag: '1.4.1',
  firstSpanAt: 0, trafficConfirmedAt: 0, ksmStartedAt: 0, podsReadyAt: 0, ksmNotReadySince: 0, completedAt: 1_790_000_120_000,
  detectedBy: 'span', spanCount: 10, note: '', updatedAt: 1,
};
const base = (over: Partial<ServiceGitOpsResponse> = {}): ServiceGitOpsResponse => ({
  service: 'checkout', rolloutsFrom: 0, workloadsFrom: 0, to: 0,
  workloads: [{ ...W, clusterName: 'prod-east' }],
  argo: { configured: true, hubs: [], apps: [], otherInNamespace: 0 },
  rollouts: { enabled: true, rows: [] },
  ...over,
});

let root: Root | null = null;
let host: HTMLDivElement | null = null;
function mount() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><ServiceGitOpsTab service="checkout" /></MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null;
  h.q = { data: undefined, isPending: true, error: null, refetch: () => Promise.resolve() };
});

describe('ServiceGitOpsTab', () => {
  it('yükleniyor: iki tablo da başlığıyla durur, durum tablonun içinde', () => {
    const el = mount();
    expect(el.querySelectorAll('table').length).toBe(2);
    expect(el.textContent).toContain('Argo CD uygulamaları');
    expect(el.textContent).toContain("Rollout'lar");
  });

  it('hata: iki tabloda da mesaj', () => {
    h.q = { ...h.q, isPending: false, error: new Error('boom') };
    const el = mount();
    expect(el.textContent?.match(/GitOps bilgisi yüklenemedi: boom/g)?.length).toBe(2);
    expect(el.textContent).not.toContain('Son 24 saatte bu servisin');
  });

  // v0.10.1057 — operatör: "İş yükleri ayrıca yazmasına gerek yok." Ayrı
  // rozet bloğu yok; iş yükleri tablolarda. Veri tablolar için gerekli
  // (cluster adı, "iş yükü yok" boş durumu), eşlenemeyen cluster notu Argo
  // bölümünde.
  it('ayrı "İş yükleri" bloğu yok; iş yükü tablonun kolonunda', () => {
    h.q = { ...h.q, isPending: false, data: base({
      unmappedClusters: ['dc-x'],
      argo: { configured: true, hubs: [], otherInNamespace: 0, apps: [{
        hubClusterId: 'h1', appNamespace: 'apps', name: 'p-shop-checkout-prod-e', match: 'name', confidence: 70, workloads: [W], syncs24h: {},
      }] },
    }) };
    const el = mount();
    expect(el.querySelector('#gitops-workloads')).toBeNull();
    expect(el.textContent).not.toContain('İş yükleri');
    expect(el.querySelectorAll('section').length).toBe(2);
    expect(el.querySelector('table')?.textContent).toContain('checkout (prod-east)');
    expect(el.querySelector('#gitops-argo')?.closest('section')?.textContent)
      .toContain('Remote Cluster kaydına eşlenemeyen span cluster değerleri atlandı: dc-x');
  });

  it('iş yükü görülmedi: açıklama Argo tablosunun boş durumunda', () => {
    h.q = { ...h.q, isPending: false, data: base({ workloads: [] }) };
    const el = mount();
    expect(el.querySelector('table')?.textContent)
      .toContain("Son 24 saatte bu servisin span'lerinde k8s iş yükü adı");
  });

  it('Argo yapılandırılmamış + Rollouts kapalı: notlar ve ayar bağlantıları', () => {
    h.q = { ...h.q, isPending: false, data: base({
      argo: { configured: false, note: "Argo CD hub'ı tanımlı değil (Ayarlar › Argo CD)", hubs: [], apps: [], otherInNamespace: 0 },
      rollouts: { enabled: false, note: 'Rollouts kapalı — etkinleştir', rows: [] },
    }) };
    const el = mount();
    expect(el.textContent).toContain("Argo CD hub'ı tanımlı değil");
    expect(el.textContent).toContain('Rollouts kapalı — etkinleştir');
    expect(el.querySelector('a[href="/settings/argocd"]')).not.toBeNull();
    expect(el.querySelector('a[href="/rollouts"]')).not.toBeNull();
  });

  it('eşleşme yok: aynı namespace sayısı ve pin önerisi', () => {
    h.q = { ...h.q, isPending: false, data: base({ argo: { configured: true, hubs: [], apps: [], otherInNamespace: 3 } }) };
    const el = mount();
    expect(el.textContent).toContain("aynı namespace'e deploy eden 3 uygulama var");
    expect(el.textContent).toContain('Pin ekleyerek elle bağlayın');
  });

  it('dolu: uygulama ve rollout satırları, rollout yanında Argo uygulaması, hub notu', () => {
    h.q = { ...h.q, isPending: false, data: base({
      argo: {
        configured: true, otherInNamespace: 0,
        hubs: [{ hubClusterId: 'h1', hubName: 'hub-east', status: 'ok', apps: 1 }, { hubClusterId: 'h2', hubName: 'hub-west', status: 'error', error: 'unauthorized: 403', apps: 0 }],
        apps: [{
          hubClusterId: 'h1', appNamespace: 'apps', name: 'p-shop-checkout-prod-e', project: 'shop',
          syncStatus: 'OutOfSync', healthStatus: 'Healthy', autoSync: true, destClusterId: 'c1', destNamespace: 'shop',
          match: 'name', confidence: 70, workloads: [W], syncs24h: { Succeeded: 2, Failed: 1 }, repo: 'https://git.example/shop/deploy.git',
        }],
      },
      rollouts: { enabled: true, rows: [ROLLOUT] },
    }) };
    const el = mount();
    const [appT, roT] = Array.from(el.querySelectorAll('table'));
    expect(appT.textContent).toContain('p-shop-checkout-prod-e');
    expect(appT.textContent).toContain('OutOfSync');
    expect(appT.textContent).toContain('tahmini %70');
    expect(appT.textContent).toContain('2 başarılı · 1 başarısız');
    expect(appT.textContent).toContain('prod-east / shop');
    expect(appT.textContent).toContain('git.example/shop/deploy');
    expect(roT.textContent).toContain('tamamlandı');
    expect(roT.textContent).toContain('1.4.1 → 1.4.2');
    expect(roT.textContent).toContain('p-shop-checkout-prod-e');
    expect(roT.textContent).toContain('autosync');
    expect(roT.querySelector('a[href^="/rollouts?rollout="]')).not.toBeNull();
    expect(el.textContent).toContain('Hub hub-west: sorgu başarısız — unauthorized: 403');
    expect(el.textContent).not.toContain('Hub hub-east');
    // source yok (v0.10.981 cevabı): rozet Thanos, meta'da kaynak notu yok.
    expect(argoHead(el).textContent).toContain('Thanos · argocd_app_info');
    expect(argoHead(el).textContent).not.toContain('canlı sorgu');
    expect(argoHead(el).textContent).not.toContain('eşleyici');
  });

  const APP = {
    hubClusterId: 'h1', appNamespace: 'apps', name: 'p-shop-checkout-prod-e', syncStatus: 'Synced', healthStatus: 'Healthy',
    destClusterId: 'c1', destNamespace: 'shop', match: 'name', confidence: 70, workloads: [W], syncs24h: { Succeeded: 1 },
  };
  const argoHead = (el: HTMLElement) => el.querySelector('#gitops-argo') as HTMLElement;

  it('kaynak: eşleyici tablosu — rozet + meta + eşleyici notu', () => {
    h.q = { ...h.q, isPending: false, data: base({
      argo: { configured: true, source: 'mapper', otherInNamespace: 2, note: '1 eşlenmiş uygulamanın güncel durumu yok',
        hubs: [{ hubClusterId: 'h1', status: 'ok', apps: 1 }], apps: [APP] },
    }) };
    const el = mount();
    const head = argoHead(el);
    expect(head.querySelector('.sec-head__src')?.textContent).toBe('eşleyici · argocd_app_mapping');
    expect(head.querySelector('.sec-head__meta')?.textContent).toBe("1 uygulama · aynı namespace'te eşleşmeyen 2 · eşleyici tablosundan (işçi aralıkları kadar gecikmeli)");
    expect(el.textContent).toContain('1 eşlenmiş uygulamanın güncel durumu yok');
    expect(el.querySelector('table')?.textContent).toContain('p-shop-checkout-prod-e');
  });

  it('kaynak: canlı — rozet Thanos, meta "canlı sorgu"; eşleşme yokken de kaynak meta\'da', () => {
    h.q = { ...h.q, isPending: false, data: base({ argo: { configured: true, source: 'live', hubs: [], apps: [APP], otherInNamespace: 0 } }) };
    let el = mount();
    expect(argoHead(el).querySelector('.sec-head__src')?.textContent).toBe('Thanos · argocd_app_info');
    expect(argoHead(el).querySelector('.sec-head__meta')?.textContent).toBe('1 uygulama · canlı sorgu');
    act(() => root?.unmount());
    host?.remove();
    h.q = { ...h.q, isPending: false, data: base({ argo: { configured: true, source: 'mapper', hubs: [], apps: [], otherInNamespace: 0 } }) };
    el = mount();
    expect(argoHead(el).querySelector('.sec-head__meta')?.textContent).toBe('eşleyici tablosundan (işçi aralıkları kadar gecikmeli)');
    expect(el.textContent).toContain('Bu servisin iş yüklerine eşlenen Argo uygulaması yok');
  });
});
