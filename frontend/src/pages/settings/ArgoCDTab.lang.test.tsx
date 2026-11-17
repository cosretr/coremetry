// @vitest-environment jsdom
//
// v0.10.2313 — pin: arayüz dili İngilizceyken Ayarlar › Argo CD panelleri
// (sekme, hub'lar, instance tablosu + satır içi form, keşif, ortamlar,
// gelişmiş / pin bölümü, boş durum) acd.* kataloğunun Türkçe metnini
// ÇİZMEZ (metin + title / aria-label / placeholder); Türkçe arayüzde aynen.
// argocdForm / argocdDiscovery yardımcılarının cümleleri bu dilimin dışında.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ArgoCDDiscoverRequest, ArgoCDSettings, ArgoCDSettingsResponse, ThanosSnapshot } from '@/lib/types';
import { t } from '@/lib/i18n';
import { EN } from '@/lib/i18n.en';

const H1 = 'c-4f7a21d9';
const H2 = 'c-9b03e6c2';
const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', settings: null as unknown, thanos: null as unknown }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => ({
  api: {
    getArgoCDSettings: async () => m.settings,
    getThanosSettings: async () => m.thanos,
    putArgoCDSettings: async () => m.settings,
    discoverArgoCD: async (b: ArgoCDDiscoverRequest) => ({
      hubClusterId: b.hubClusterId, hubName: 'hub-1', injectClusterLabel: true, window: { start: 0, end: 1 }, jobsTruncated: false, calls: 2, saved: false,
      candidates: b.hubClusterId === H1
        ? [{ id: 'team-b-prod', hubClusterId: H1, name: '', hubNamespace: 'team-b-prod', metricsJob: 'team-b-prod-metrics', appsAnyNamespace: false, discovered: true, appCount: 3, shardCount: 1 }]
        : [],
    }),
  },
  isCanceled: () => false,
}));
import { ArgoCDTab } from './ArgoCDTab';

const SETTINGS: ArgoCDSettings = {
  enabled: true,
  hubs: [{ clusterId: H1, injectClusterLabel: true }, { clusterId: H2, injectClusterLabel: false }],
  envList: ['prod'],
  instances: [
    { id: 'zeta-prod', hubClusterId: H1, name: 'Zeta prod', hubNamespace: 'zeta-prod', metricsJob: 'zeta-prod-metrics', apiUrl: 'https://argocd.zeta.example.invalid', tokenRef: 'env:ARGOCD_ZETA_TOKEN', enabled: true },
    { id: 'alpha-int', hubClusterId: H2, hubNamespace: 'alpha-int', enabled: false },
  ],
  apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {},
  pins: [{ clusterId: H1, namespace: 'team-a', workloadKind: 'Deployment', workload: 'api', instanceId: 'zeta-prod', appNamespace: 'zeta-prod', appName: 'team-a-api-prod' }],
  updatedAt: 1_790_000_000_000_000_000,
};
const resp = (s: ArgoCDSettings): ArgoCDSettingsResponse => ({
  settings: s, resolved: s, defaults: s, hubs: [],
  bounds: { 'intervals.metricsS': { min: 30, max: 600, default: 60 } },
  tokens: { 'zeta-prod': { tokenRef: 'env:ARGOCD_ZETA_TOKEN', resolved: false, error: 'not set' } },
});
const THANOS: ThanosSnapshot = { clusters: [
  { id: H1, name: 'hub-1', url: 'http://thanos-hub-1.example.invalid', hasToken: true, enabled: true, thanosLabelName: 'cluster', thanosLabelValue: 'hub-1' },
  { id: H2, name: 'hub-2', url: 'http://thanos-hub-2.example.invalid', hasToken: true, enabled: false },
] };
const EMPTY: ArgoCDSettings = { enabled: false, apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {} };

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null;
let root: Root | null = null;
const tick = async (ms = 30) => { await act(async () => { await new Promise(r => setTimeout(r, ms)); }); };
async function render(): Promise<HTMLElement> {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><ArgoCDTab /></MemoryRouter>); });
  await tick();
  return host;
}
function close() {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
}
const click = (id: string) => act(() => { document.getElementById(id)!.click(); });
function snap(): string {
  const c = document.body;
  const attrs = [...c.querySelectorAll('[title],[aria-label],[placeholder]')]
    .flatMap(e => ['title', 'aria-label', 'placeholder'].map(a => e.getAttribute(a) ?? ''));
  return [c.textContent ?? '', ...attrs].join('\n');
}
async function all(): Promise<{ text: string; html: string }> {
  const text: string[] = []; const html: string[] = [];
  const take = () => { text.push(snap()); html.push(document.body.innerHTML); close(); };
  m.settings = resp(SETTINGS); m.thanos = THANOS;
  const el = await render();
  click('acd-adv-btn'); click('acd-pins-btn');
  const row = el.querySelector<HTMLElement>('[id^="acd-inst-btn-"]')!;
  act(() => { row.click(); });
  click('acd-disc-btn');
  await tick(60);
  take();
  m.settings = resp(SETTINGS);
  await render();
  click('acd-inst-add');
  take();
  m.settings = resp(EMPTY);
  await render(); take();
  m.thanos = { clusters: [] };
  await render(); take();
  return { text: text.join('\n'), html: html.join('\n') };
}

afterEach(() => { close(); m.lang = 'en'; });

// Katalogdaki her acd.* Türkçe değerinin yer tutucusuz en uzun parçası (≥ 12 karakter).
// acd.rcLink hariç: argocdDiscovery'nin (kapsam dışı) pasif hub cümlesi aynı bağlantı adını taşır.
const TR_FRAGMENTS = Object.keys(EN).filter(k => k.startsWith('acd.') && !k.endsWith('_one') && k !== 'acd.rcLink')
  .map(k => t(k, 'tr').split(/\{\w+\}/).sort((a, b) => b.length - a.length)[0].trim())
  .filter(s => s.length >= 12);

describe('Ayarlar › Argo CD — İngilizce arayüz (v0.10.2313)', () => {
  it('acd.* Türkçe metni yok; İngilizce karşılıklar var', async () => {
    expect(TR_FRAGMENTS.length).toBeGreaterThan(150);
    const { text } = await all();
    expect(TR_FRAGMENTS.filter(f => text.includes(f))).toEqual([]);
    for (const en of [
      'Argo CD integration on', 'Run the metrics worker (argocd-metrics)', 'Register discovered instances automatically',
      'Hubs', 'Cluster label added to Argo queries', 'Actions', 'passive', 'Add hub', 'Pick a Remote Cluster…',
      'Instances', 'instances: 2 · hubs: 2', 'Status', 'unresolved', 'off', 'none',
      'Editing zeta-prod', 'Display name', 'Saved reference:', 'Remove reference', 'Apply to table', 'Cancel', 'Remove from table',
      'Search hubs for instances', 'Discovery result', 'Search again', 'hub-1 discovery candidates', '+ Add',
      'Environments ', 'Remove environment prod', 'add environment', 'Advanced', 'Without tokenRef', 'Manual mappings ',
      '1 record (written via the API)', 'into argocd_app_mapping as match_method=\'manual\'', 'Bound instances: zeta-prod (1).',
      'Revert changes', 'Save', 'Save validates first, then writes the settings.argocd.update audit entry',
      'New instance', 'adding by hand · not written until Save',
      'No hub yet', 'Remote Cluster to select as hub', 'Add as hub', 'Not configured — no hubs or instances',
      'A Remote Cluster record is needed first', 'Go to Settings › Remote clusters ↗', 'Not configured — no Remote Cluster record',
    ]) expect(text, en).toContain(en);
  });

  it('Türkçe arayüzde bilinen metin aynen', async () => {
    m.lang = 'tr';
    const { text } = await all();
    for (const tr of [
      'Argo CD entegrasyonu açık', "Hub'lar", 'Argo sorgularına eklenen küme etiketi', 'Eylemler', 'pasif', 'Hub ekle',
      "Instance'lar", '2 instance · 2 hub', 'çözülemedi', 'kapalı', 'zeta-prod düzenleniyor', 'Kayıtlı referans:',
      'Tabloya uygula', 'Vazgeç', "Hub'larda instance ara", 'Keşif sonucu', 'hub-1 keşif adayları', '+ Ekle',
      'Ortamlar ', 'prod ortamını kaldır', 'Gelişmiş', 'tokenRef yokken', 'Elle eşlemeler ',
      "argocd_app_mapping'e match_method='manual' olarak kopyalar;", "Bağlı instance'lar: zeta-prod (1).",
      'Değişiklikleri geri al', 'Kaydet önce doğrular, sonra settings.argocd.update denetim kaydını yazar',
      'Yeni instance', 'Henüz hub yok', 'Hub olarak ekle', 'Önce bir Remote Cluster kaydı gerekli',
    ]) expect(text, tr).toContain(tr);
  });
});
