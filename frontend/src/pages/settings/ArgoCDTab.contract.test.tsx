// @vitest-environment jsdom
// ArgoCDTab.contract.test.tsx — v0.10.974 — Ayarlar › Argo CD sekmesinin
// sözleşmesi (spec §3.10; mockup argocd-canvas Main / States). Ağ mock:
// GET ayar + Thanos anlık görüntüsü, PUT gövdesi ve hub başına keşif POST'u
// yakalanır. Çivilenenler: okuma kapısı, a1/a2 boş hâller, hub ekle/kaldır,
// satır içi form (uygula, kimlik salt okunur, tokenRef kuralları), PUT
// sırası = TASLAK sırası (tablo sıralıyken), 400 alan yolunun doğru satıra
// düşmesi, pins'in aynen geri gitmesi, iki hub → iki SIRALI POST (hub'ın
// taslak inject bayrağıyla), 429 metni, "Ekle" / "Geri al", "kayıtlı"
// eşleşmesinin yalnız aynı hub'daki KAYITLI satırla olması ve tek seferlik
// "etiketsiz yeniden ara".
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type {
  ArgoCDBound, ArgoCDDiscoverRequest, ArgoCDDiscoverResult, ArgoCDInstanceInput, ArgoCDSettings,
  ArgoCDSettingsInput, ArgoCDSettingsResponse, ThanosSnapshot,
} from '@/lib/types';

const H1 = 'c-4f7a21d9';
const H2 = 'c-9b03e6c2';

const { getArgoCDSettings, putArgoCDSettings, discoverArgoCD, getThanosSettings, fx } = vi.hoisted(() => {
  const BOUNDS: Record<string, { min: number; max: number; default: number }> = {
    'apiWorker.rps': { min: 0.1, max: 20, default: 1 }, 'apiWorker.burst': { min: 1, max: 50, default: 5 },
    'apiWorker.maxConcurrent': { min: 1, max: 10, default: 2 }, 'classification.windowMin': { min: 1, max: 60, default: 5 },
    'classification.outOfBandLookbackMin': { min: 5, max: 240, default: 30 }, 'reader.maxSeries': { min: 1000, max: 50000, default: 50000 },
    'reader.maxBodyMiB': { min: 1, max: 64, default: 64 }, 'reader.timeoutS': { min: 5, max: 45, default: 30 },
    'intervals.metricsS': { min: 30, max: 600, default: 60 }, 'intervals.inventoryMin': { min: 5, max: 120, default: 15 },
    'intervals.mapperMin': { min: 5, max: 240, default: 10 }, 'intervals.classifierReevalH': { min: 1, max: 168, default: 24 },
    'mapping.nameConfidence': { min: 1, max: 100, default: 70 }, 'mapping.namespaceConfidence': { min: 1, max: 100, default: 30 },
  };
  const fx = {
    settings: null as unknown,
    thanos: null as unknown,
    bounds: BOUNDS,
  };
  return {
    fx,
    getArgoCDSettings: vi.fn(),
    putArgoCDSettings: vi.fn(),
    discoverArgoCD: vi.fn(),
    getThanosSettings: vi.fn(),
  };
});
vi.mock('@/lib/api', () => ({
  api: { getArgoCDSettings, putArgoCDSettings, discoverArgoCD, getThanosSettings },
  isCanceled: (e: unknown) => (e as Error)?.name === 'CanceledError',
}));

import { ArgoCDTab } from './ArgoCDTab';

const PINS = [{ clusterId: 'c-1d2e3f40', namespace: 'team-a', workloadKind: 'Deployment', workload: 'api', instanceId: 'team-a-prod', appNamespace: 'team-a-prod', appName: 'team-a-api-prod-ca' }];

function baseSettings(): ArgoCDSettings {
  return {
    enabled: true,
    hubs: [{ clusterId: H1, injectClusterLabel: true }, { clusterId: H2, injectClusterLabel: false }],
    envList: ['prod', 'int'],
    instances: [
      { id: 'zeta-prod', hubClusterId: H1, name: 'Zeta prod', hubNamespace: 'zeta-prod', metricsJob: 'zeta-prod-metrics', apiUrl: 'https://argocd.zeta.example.invalid', tokenRef: 'env:ARGOCD_ZETA_TOKEN', enabled: true },
      { id: 'team-a-prod', hubClusterId: H2, name: 'Team A prod', hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics', enabled: true },
      { id: 'alpha-int', hubClusterId: H1, hubNamespace: 'alpha-int', enabled: true },
    ],
    apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {},
    pins: PINS,
    updatedAt: 1_790_000_000_000_000_000,
  };
}
const THANOS: ThanosSnapshot = { clusters: [
  { id: H1, name: 'hub-1', url: 'http://thanos-hub-1.example.invalid', hasToken: true, enabled: true, thanosLabelName: 'cluster', thanosLabelValue: 'hub-1' },
  { id: H2, name: 'hub-2', url: 'http://thanos-hub-2.example.invalid', hasToken: true, enabled: true, thanosLabelName: 'cluster', thanosLabelValue: 'hub-2' },
  { id: 'c-1d2e3f40', name: 'cluster-a', url: 'http://thanos-a.example.invalid', hasToken: false, enabled: true },
] };

function resp(s: ArgoCDSettings): ArgoCDSettingsResponse {
  return {
    settings: s, resolved: s, defaults: s, bounds: fx.bounds as Record<string, ArgoCDBound>, hubs: [],
    tokens: (s.instances ?? []).filter(i => i.tokenRef).reduce<ArgoCDSettingsResponse['tokens']>((a, i) => {
      a[i.id] = { tokenRef: i.tokenRef!, resolved: true };
      return a;
    }, {}),
  };
}
function discoverResult(hub: string, over: Partial<ArgoCDDiscoverResult> = {}): ArgoCDDiscoverResult {
  return { hubClusterId: hub, hubName: hub === H1 ? 'hub-1' : 'hub-2', injectClusterLabel: true, window: { start: 0, end: 1 }, candidates: [], jobsTruncated: false, calls: 1, saved: false, ...over };
}

function setup(s: ArgoCDSettings = baseSettings(), thanos: ThanosSnapshot = THANOS) {
  getArgoCDSettings.mockImplementation(async () => resp(s));
  getThanosSettings.mockImplementation(async () => thanos);
  putArgoCDSettings.mockImplementation(async (body: ArgoCDSettingsInput) => resp({ ...s, ...body, instances: body.instances.map(({ clearTokenRef: _c, ...i }) => i) }));
}

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><ArgoCDTab /></MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  getArgoCDSettings.mockReset(); putArgoCDSettings.mockReset(); discoverArgoCD.mockReset(); getThanosSettings.mockReset();
});
const tick = async (ms = 20) => { await act(async () => { await new Promise(r => setTimeout(r, ms)); }); };

function buttons(el: HTMLElement, text: string): HTMLButtonElement[] {
  return [...el.querySelectorAll('button')].filter(b => b.textContent?.replace(/\s+/g, ' ').trim() === text) as HTMLButtonElement[];
}
function button(el: HTMLElement, text: string): HTMLButtonElement {
  const b = buttons(el, text)[0];
  if (!b) throw new Error(`"${text}" düğmesi yok`);
  return b;
}
function click(node: Element) { act(() => { (node as HTMLElement).click(); }); }
function setInput(ctl: HTMLInputElement, text: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
    ctl.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
function setSelect(ctl: HTMLSelectElement, value: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(ctl, value);
    ctl.dispatchEvent(new Event('change', { bubbles: true }));
  });
}
const byId = <T extends HTMLElement>(el: HTMLElement, id: string) => {
  const n = el.ownerDocument.getElementById(id);
  if (!n) throw new Error(`#${id} yok`);
  return n as T;
};
const live = (el: HTMLElement) => el.querySelector('[role="status"].sr-only')?.textContent ?? '';
const instRows = (el: HTMLElement) => [...el.querySelectorAll('table[aria-label="Argo CD instance\'ları"] tbody tr[role="button"]')];
async function save(el: HTMLElement): Promise<ArgoCDSettingsInput> {
  click(button(el, 'Kaydet'));
  await tick();
  expect(putArgoCDSettings).toHaveBeenCalledTimes(1);
  return putArgoCDSettings.mock.calls[0][0] as ArgoCDSettingsInput;
}
const inst = (body: ArgoCDSettingsInput, id: string) => body.instances.find(i => i.id === id) as ArgoCDInstanceInput;

describe('ArgoCDTab — yükleme ve boş hâller', () => {
  it('okuma hatası: form çizilmez, SettingsLoadError', async () => {
    getArgoCDSettings.mockRejectedValue(new Error('HTTP 503: argocd settings not wired'));
    getThanosSettings.mockResolvedValue(THANOS);
    const el = render();
    await tick();
    expect(el.textContent).toContain('Ayarlar okunamadı');
    expect(buttons(el, 'Kaydet')).toHaveLength(0);
  });

  it('a1: Remote Cluster var, hub yok → tek sonraki adım', async () => {
    setup({ ...baseSettings(), enabled: false, hubs: undefined, instances: undefined, pins: undefined, envList: undefined });
    const el = render();
    await tick();
    expect(el.textContent).toContain('Henüz hub yok');
    expect(el.textContent).toContain('Ayarlanmadı — hub ve instance yok');
    expect(buttons(el, 'Kaydet')).toHaveLength(0);
    expect(byId<HTMLInputElement>(el, 'acd-enabled').disabled).toBe(true);
    const add = button(el, 'Hub olarak ekle');
    expect(add.disabled).toBe(true);
    setSelect(byId<HTMLSelectElement>(el, 'acd-a1-sel'), H1);
    expect(button(el, 'Hub olarak ekle').disabled).toBe(false);
    click(button(el, 'Hub olarak ekle'));
    expect(live(el)).toBe('hub-1 hub olarak eklendi — kaydedilmedi.');
    expect(el.textContent).toContain('1 kaydedilmemiş değişiklik: hub-1 hub olarak eklendi');
  });

  it('a2: hiç Remote Cluster yok → Remote clusters bağlantısı', async () => {
    setup({ ...baseSettings(), enabled: false, hubs: [], instances: [], pins: [] }, { clusters: [] });
    const el = render();
    await tick();
    expect(el.textContent).toContain('Önce bir Remote Cluster kaydı gerekli');
    expect(el.textContent).toContain('Ayarlanmadı — Remote Cluster kaydı yok');
    const link = [...el.querySelectorAll('a')].find(a => a.textContent?.includes("Remote clusters'a git"));
    expect(link?.getAttribute('href')).toBe('/settings/clusters');
  });
});

describe('ArgoCDTab — hub\'lar', () => {
  it('hub ekle → duyurulur ve sayılır', async () => {
    setup();
    const el = render();
    await tick();
    expect(el.textContent).toContain('2 hub');
    setSelect(byId<HTMLSelectElement>(el, 'acd-hub-add'), 'c-1d2e3f40');
    click(button(el, 'Hub ekle'));
    expect(live(el)).toBe('cluster-a hub olarak eklendi — kaydedilmedi.');
    expect(el.textContent).toContain('3 hub');
    expect(el.textContent).toContain('1 kaydedilmemiş değişiklik: cluster-a hub olarak eklendi');
  });

  it('instance\'ları bağlı hub kaldırılamaz (tarayıcıda engel)', async () => {
    setup();
    const el = render();
    await tick();
    click(el.querySelector('button[aria-label="hub-1 hub listesinden kaldır"]')!);
    expect(el.querySelector('[role="alert"]')?.textContent).toBe("hub-1 kaldırılamaz: 2 instance bu hub'a bağlı. Önce onları tablodan kaldırın ya da düzenleme formunda başka hub'a taşıyın.");
    expect(el.textContent).toContain('2 hub');
    expect(el.textContent).toContain('Kayıtlı ayarlarla aynı.');
  });
});

describe('ArgoCDTab — instance formu ve PUT', () => {
  it('instance ekle + uygula; yeni satırın boş ref\'i clearTokenRef: true', async () => {
    setup();
    const el = render();
    await tick();
    click(button(el, '+ Instance ekle'));
    expect(el.textContent).toContain('Yeni instance');
    expect(byId<HTMLInputElement>(el, 'acd-ed-id').readOnly).toBe(false);
    setInput(byId(el, 'acd-ed-id'), 'team-c-prod');
    setInput(byId(el, 'acd-ed-ns'), 'team-c-prod');
    click(button(el, 'Tabloya uygula'));
    expect(live(el)).toBe('team-c-prod tabloya uygulandı — kaydedilmedi.');
    const row = instRows(el).find(r => r.textContent?.includes('team-c-prod'))!;
    expect(row.textContent).toContain('yeni');
    const body = await save(el);
    expect(inst(body, 'team-c-prod')).toMatchObject({ tokenRef: '', clearTokenRef: true, hubClusterId: H1, hubNamespace: 'team-c-prod' });
  });

  it('kayıtlı ref "kayıtlı" gösterir; dokunulmamış satır tokenRef "" gider, bayrak YOK; kimlik salt okunur', async () => {
    setup();
    const el = render();
    await tick();
    const zeta = instRows(el).find(r => r.textContent?.includes('zeta-prod'))!;
    expect(zeta.textContent).toContain('kayıtlı');
    click(byId(el, 'acd-inst-btn-s_zeta-prod'));
    const id = byId<HTMLInputElement>(el, 'acd-ed-id');
    expect(id.readOnly).toBe(true);
    const ref = byId<HTMLInputElement>(el, 'acd-ed-ref');
    expect(ref.value).toBe('');
    expect(el.querySelector('label[for="acd-ed-ref"]')?.textContent).toContain('· kayıtlı');
    expect(byId(el, 'acd-ed-ref-s').textContent).toContain('env:ARGOCD_ZETA_TOKEN');
    expect(byId(el, 'acd-ed-ref-s').textContent).toContain('· çözüldü');
    click(button(el, 'Vazgeç'));
    const body = await save(el);
    const z = inst(body, 'zeta-prod');
    expect(z.tokenRef).toBe('');
    expect('clearTokenRef' in z).toBe(false);
  });

  it('"Referansı kaldır" → clearTokenRef: true', async () => {
    setup();
    const el = render();
    await tick();
    click(byId(el, 'acd-inst-btn-s_zeta-prod'));
    click(button(el, 'Referansı kaldır'));
    expect(byId(el, 'acd-ed-ref-s').textContent).toContain('Kaydedince referans silinir');
    click(button(el, 'Tabloya uygula'));
    const zeta = instRows(el).find(r => r.textContent?.includes('zeta-prod'))!;
    expect(zeta.textContent).toContain('yok · yalnız metrik');
    const body = await save(el);
    expect(inst(body, 'zeta-prod')).toMatchObject({ tokenRef: '', clearTokenRef: true });
  });

  it('PUT sırası = TASLAK sırası (tablo Hub\'a göre sıralıyken); pins aynen', async () => {
    setup();
    const el = render();
    await tick();
    // Görünüm: hub-1 alpha-int, hub-1 zeta-prod, hub-2 team-a-prod.
    expect(instRows(el).map(r => r.querySelector('[id^="acd-inst-btn-"]')?.id)).toEqual([
      'acd-inst-btn-s_alpha-int', 'acd-inst-btn-s_zeta-prod', 'acd-inst-btn-s_team-a-prod',
    ]);
    const body = await save(el);
    expect(body.instances.map(i => i.id)).toEqual(['zeta-prod', 'team-a-prod', 'alpha-int']);
    expect(body.pins).toEqual(PINS);
    expect(body.hubs).toEqual([{ clusterId: H1, injectClusterLabel: true }, { clusterId: H2, injectClusterLabel: false }]);
    expect(el.textContent).toContain('Kaydedildi — 2 hub, 3 instance; 1 pin olduğu gibi geri gönderildi.');
    expect(el.textContent).toContain('son kayıt şimdi');
  });

  it('400 {field: instances[1].apiUrl} doğru satırın altına düşer (taslak dizini)', async () => {
    setup();
    putArgoCDSettings.mockRejectedValueOnce(new Error('HTTP 400: {"error":"instances[1].apiUrl: http:// ya da https:// ile başlamalı","field":"instances[1].apiUrl"}'));
    const el = render();
    await tick();
    await save(el);
    const err = byId(el, 'acd-inst-err-s_team-a-prod');
    expect(err.textContent).toBe('instances[1].apiUrl: http:// ya da https:// ile başlamalı');
    const prev = err.closest('tr')!.previousElementSibling!;
    expect(prev.textContent).toContain('team-a-prod');
    expect(byId(el, 'acd-inst-btn-s_team-a-prod').getAttribute('aria-describedby')).toBe('acd-inst-err-s_team-a-prod');
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Kaydedilmedi — sunucu reddetti.');
    expect(live(el)).toBe('Kaydedilmedi.');
  });
});

describe('ArgoCDTab — ⋯ menüsü ve istemci denetimi', () => {
  it('pin\'i olan instance menüden kaldırılamaz', async () => {
    setup();
    const el = render();
    await tick();
    click(byId(el, 'acd-inst-menu-s_team-a-prod'));
    const menu = el.ownerDocument.querySelector('.ui-popover[role="menu"]')!;
    expect([...menu.querySelectorAll('[role="menuitem"]')].map(b => b.textContent)).toEqual(['Düzenle', 'Devre dışı bırak', 'Tablodan kaldır']);
    click([...menu.querySelectorAll('[role="menuitem"]')].find(b => b.textContent === 'Tablodan kaldır')!);
    expect(el.querySelector('[role="alert"]')?.textContent).toBe("team-a-prod kaldırılamaz: bu instance'a 1 pin bağlı; önce API'den pin'leri kaldırın (PUT /api/settings/argocd, pins[]).");
    expect(instRows(el)).toHaveLength(3);
  });

  it('BE3 aynası: kaldırılan kayıtlı kimliğin yuvasına yeni kimlik → Kaydet istek GÖNDERMEZ, hata satırda', async () => {
    setup();
    const el = render();
    await tick();
    click(byId(el, 'acd-inst-menu-s_zeta-prod'));
    click([...el.ownerDocument.querySelectorAll('.ui-popover [role="menuitem"]')].find(b => b.textContent === 'Tablodan kaldır')!);
    expect(live(el)).toBe('zeta-prod tablodan kaldırıldı — kaydedilmedi.');
    click(button(el, '+ Instance ekle'));
    setInput(byId(el, 'acd-ed-id'), 'zeta-gitops');
    setInput(byId(el, 'acd-ed-ns'), 'zeta-prod');
    click(button(el, 'Tabloya uygula'));
    click(button(el, 'Kaydet'));
    await tick();
    expect(putArgoCDSettings).not.toHaveBeenCalled();
    const box = el.querySelector('[role="alert"]')!;
    expect(box.textContent).toContain('Kaydedilmedi — 1 alan düzeltilmeli.');
    expect(box.textContent).toContain('instances[2].id — kayıtlı kimlik değiştirilemez');
    const newKeyId = instRows(el).find(r => r.textContent?.includes('zeta-gitops'))!.querySelector('[id^="acd-inst-btn-"]')!.id;
    const err = byId(el, newKeyId.replace('acd-inst-btn-', 'acd-inst-err-'));
    expect(err.textContent).toContain("“zeta-gitops” kayıtlı “zeta-prod” instance'ının yerini alıyor (hub-1/zeta-prod)");
  });
});

describe('ArgoCDTab — keşif', () => {
  const H1_CANDS = [
    { id: 'team-a-prod', hubClusterId: H1, name: 'team-a-prod', hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics', appsAnyNamespace: false, discovered: true, namespaceCase: 'A' as const, appCount: 412, shardCount: 2 },
    { id: 'alpha-int', hubClusterId: H1, name: 'alpha-int', hubNamespace: 'alpha-int', metricsJob: 'alpha-int-metrics', appsAnyNamespace: false, discovered: true, namespaceCase: 'A' as const, configuredId: 'alpha-int', appCount: 1184, shardCount: 3 },
  ];

  it('iki hub → iki SIRALI POST, her biri hub\'ın taslak inject bayrağıyla', async () => {
    setup();
    let release: (() => void) | null = null;
    discoverArgoCD.mockImplementation((b: ArgoCDDiscoverRequest) => new Promise(res => {
      release = () => res(discoverResult(b.hubClusterId, { injectClusterLabel: !!b.injectClusterLabel }));
    }));
    const el = render();
    await tick();
    // hub-2'nin işaretini taslakta aç (kaydetmeden): keşif TASLAK değeri gönderir.
    click(el.querySelector('input[aria-label^=\'cluster="hub-2"\']')!);
    click(button(el, "Hub'larda instance ara"));
    await tick();
    expect(discoverArgoCD).toHaveBeenCalledTimes(1);
    expect(discoverArgoCD.mock.calls[0][0]).toEqual({ hubClusterId: H1, injectClusterLabel: true });
    expect(live(el)).toBe('hub-1 aranıyor…');
    await act(async () => { release!(); });
    await tick();
    expect(discoverArgoCD).toHaveBeenCalledTimes(2);
    expect(discoverArgoCD.mock.calls[1][0]).toEqual({ hubClusterId: H2, injectClusterLabel: true });
    expect(live(el)).toBe('hub-1 bitti · hub-2 aranıyor…');
    await act(async () => { release!(); });
    await tick();
    expect(live(el)).toBe('Keşif tamamlandı: 2 hub, 0 aday.');
  });

  it('429 metni', async () => {
    setup();
    discoverArgoCD.mockRejectedValue(new Error('HTTP 429: {"error":"bir Argo CD keşfi zaten koşuyor"}'));
    const el = render();
    await tick();
    click(button(el, "Hub'larda instance ara"));
    await tick(40);
    expect(el.textContent).toContain('Bir Argo CD keşfi zaten koşuyor — bitince yeniden arayın.');
  });

  it('"Ekle" → kaydedilmemiş satır (discovered: true) + "Geri al"; "kayıtlı" yalnız aynı hub\'daki kayıtlı satır', async () => {
    setup();
    discoverArgoCD.mockImplementation(async (b: ArgoCDDiscoverRequest) =>
      discoverResult(b.hubClusterId, b.hubClusterId === H1 ? { candidates: H1_CANDS, calls: 5 } : { candidates: [] }));
    const el = render();
    await tick();
    click(button(el, "Hub'larda instance ara"));
    await tick(40);
    const table = el.querySelector('table[aria-label="hub-1 keşif adayları"]')!;
    const rows = [...table.querySelectorAll('tbody tr')];
    // team-a-prod hub-2'de KAYITLI; hub-1'deki aday yeni ve kimliği çakışmadan önerilir.
    expect(rows[0].textContent).toContain('team-a-prod-2');
    expect(rows[0].textContent).toContain('+ Ekle');
    expect(rows[0].textContent).toContain('412');
    expect(rows[1].textContent).toContain('kayıtlı');
    expect(rows[1].textContent).toContain('1.184');
    click(rows[0].querySelector('button')!);
    expect(live(el)).toBe('team-a-prod-2 tabloya eklendi — kaydedilmedi. API URL, tokenRef ya da kimlik için satırı açın.');
    const again = [...el.querySelector('table[aria-label="hub-1 keşif adayları"]')!.querySelectorAll('tbody tr')];
    expect(again[0].textContent).toContain('eklendi');
    expect(again[0].textContent).not.toContain('kayıtlı');
    expect(instRows(el).some(r => r.textContent?.includes('team-a-prod-2'))).toBe(true);
    // Geri al → satır gider, aday yine "Ekle".
    click(button(el, 'Geri al'));
    expect(instRows(el).some(r => r.textContent?.includes('team-a-prod-2'))).toBe(false);
    expect(el.querySelector('table[aria-label="hub-1 keşif adayları"]')!.textContent).toContain('+ Ekle');
    // Yeniden ekle ve kaydet: discovered true, yeni satırın boş ref'i temizlenir.
    click([...el.querySelector('table[aria-label="hub-1 keşif adayları"]')!.querySelectorAll('tbody tr')][0].querySelector('button')!);
    const body = await save(el);
    expect(inst(body, 'team-a-prod-2')).toMatchObject({ discovered: true, hubClusterId: H1, hubNamespace: 'team-a-prod', metricsJob: 'team-a-prod-metrics', clearTokenRef: true });
  });

  it('etiketli boş sonuç → "etiketsiz yeniden ara" injectClusterLabel: false gönderir (tek seferlik)', async () => {
    setup();
    discoverArgoCD.mockImplementation(async (b: ArgoCDDiscoverRequest) => discoverResult(b.hubClusterId, { injectClusterLabel: !!b.injectClusterLabel }));
    const el = render();
    await tick();
    click(button(el, "Hub'larda instance ara"));
    await tick(40);
    expect(el.textContent).toContain('için 0 seri döndü. Sorgu hatasız; ama hub-1 kaydı her sorguya cluster="hub-1" ekliyor.');
    discoverArgoCD.mockClear();
    click(button(el, "hub-1'de etiketsiz yeniden ara"));
    await tick(40);
    expect(discoverArgoCD).toHaveBeenCalledTimes(1);
    expect(discoverArgoCD.mock.calls[0][0]).toEqual({ hubClusterId: H1, injectClusterLabel: false });
    // Ayar değişmedi: taslakta hub-1 işareti hâlâ açık.
    expect((el.querySelector('input[aria-label^=\'cluster="hub-1"\']') as HTMLInputElement).checked).toBe(true);
    expect(el.textContent).toContain('Kayıtlı ayarlarla aynı.');
  });

  // v0.10.974 — inceleme: "Durum" kolonu durum metni taşır (başlığı görünür);
  // "Ekle" koşu sürerken de çalışır (mockup addCand); yeniden-ara düğmeleri
  // koşu boyunca aria-disabled (tıklanan düğme odağı tutar).
  it('"Durum" başlığı görünür; hub-2 aranırken hub-1 "Ekle" çalışır, yeniden-ara düğmeleri aria-disabled', async () => {
    setup();
    let releaseH2: (() => void) | null = null;
    discoverArgoCD.mockImplementation((b: ArgoCDDiscoverRequest) => (b.hubClusterId === H1
      ? Promise.resolve(discoverResult(H1, { candidates: H1_CANDS, calls: 5 }))
      : new Promise(res => { releaseH2 = () => res(discoverResult(H2)); })));
    const el = render();
    await tick();
    click(button(el, "Hub'larda instance ara"));
    await tick(40);
    expect(discoverArgoCD).toHaveBeenCalledTimes(2);
    expect(live(el)).toBe('hub-1 bitti · hub-2 aranıyor…');
    const table = el.querySelector('table[aria-label="hub-1 keşif adayları"]')!;
    const last = [...table.querySelectorAll('thead th')].pop()!;
    expect(last.textContent).toContain('Durum');
    expect(last.classList.contains('col-actions')).toBe(false);
    for (const b of [button(el, "hub-1'de yeniden ara"), button(el, 'Yeniden ara'), button(el, 'hub-2 aranıyor…')]) {
      expect(b.getAttribute('aria-disabled')).toBe('true');
    }
    const add = table.querySelector<HTMLButtonElement>('button[id^="acd-cand-add-"]')!;
    expect(add.hasAttribute('aria-disabled')).toBe(false);
    click(add);
    expect(instRows(el).some(r => r.textContent?.includes('team-a-prod-2'))).toBe(true);
    expect(el.querySelector('table[aria-label="hub-1 keşif adayları"] tbody tr')!.textContent).toContain('eklendi');
    await act(async () => { releaseH2!(); });
    await tick();
    expect(button(el, "hub-1'de yeniden ara").hasAttribute('aria-disabled')).toBe(false);
    expect(live(el)).toBe('Keşif tamamlandı: 2 hub, 2 aday.');
  });

  // v0.10.974 — inceleme: yeniden arama sürerken hub'ın önceki aday tablosu
  // kalır (mockup buildDisc); yalnız özet "aranıyor…", rozet ve ayrıntı gizli.
  it('yeniden aranırken önceki aday tablosu kalır; özet "aranıyor…", rozet ve ayrıntı gizli', async () => {
    setup();
    let n = 0;
    let release: (() => void) | null = null;
    discoverArgoCD.mockImplementation((b: ArgoCDDiscoverRequest) => {
      if (b.hubClusterId !== H1) return Promise.resolve(discoverResult(b.hubClusterId));
      n += 1;
      if (n === 1) return Promise.resolve(discoverResult(H1, { candidates: H1_CANDS, calls: 5, jobsTruncated: true }));
      return new Promise(res => { release = () => res(discoverResult(H1, { candidates: H1_CANDS, calls: 5 })); });
    });
    const el = render();
    await tick();
    click(button(el, "Hub'larda instance ara"));
    await tick(40);
    const block = () => [...el.querySelectorAll('h5')].find(h => h.textContent === 'hub-1')!.parentElement!.parentElement!;
    const rowsOf = () => [...el.querySelectorAll('table[aria-label="hub-1 keşif adayları"] tbody tr')];
    expect(block().textContent).toContain('limitli');
    expect(block().textContent).toContain('≤50 iş ya da iş başına ≤100 değer sınırı doldu');
    expect(rowsOf()).toHaveLength(2);
    click(button(el, "hub-1'de yeniden ara"));
    await tick();
    expect(discoverArgoCD).toHaveBeenCalledTimes(3);
    expect(block().textContent).toContain('aranıyor…');
    expect(block().textContent).not.toContain('limitli');
    expect(block().textContent).not.toContain('≤50 iş');
    expect(rowsOf()).toHaveLength(2);
    expect(rowsOf()[0].textContent).toContain('team-a-prod-2');
    await act(async () => { release!(); });
    await tick();
    expect(rowsOf()).toHaveLength(2);
    expect(block().textContent).not.toContain('aranıyor…');
    expect(block().textContent).not.toContain('limitli');
  });
});

describe('ArgoCDTab — v0.10.974 inceleme düzeltmeleri', () => {
  it('yeni satırda yazılı ref: yeniden açınca "yeni, kaydedince denetlenir"; kaldırma kutuyu boşaltır ve odağı kutuya verir', async () => {
    setup();
    const el = render();
    await tick();
    click(button(el, '+ Instance ekle'));
    setInput(byId(el, 'acd-ed-id'), 'team-c-prod');
    setInput(byId(el, 'acd-ed-ns'), 'team-c-prod');
    setInput(byId(el, 'acd-ed-ref'), 'env:ARGOCD_TEAM_C_TOKEN');
    click(button(el, 'Tabloya uygula'));
    const row = instRows(el).find(r => r.textContent?.includes('team-c-prod'))!;
    expect(row.textContent).toContain('yeni · kaydedince denetlenir');
    click(row.querySelector('[id^="acd-inst-btn-"]')!);
    const status = byId(el, 'acd-ed-ref-s');
    expect(status.textContent).toContain('Kayıtlı referans:');
    expect(status.textContent).toContain('env:ARGOCD_TEAM_C_TOKEN');
    expect(status.textContent).toContain('· yeni, kaydedince denetlenir');
    expect(status.textContent).not.toContain('Kayıtlı referans yok');
    click(button(el, 'Referansı kaldır'));
    expect(byId<HTMLInputElement>(el, 'acd-ed-ref').value).toBe('');
    expect(document.activeElement?.id).toBe('acd-ed-ref');
    expect(byId(el, 'acd-ed-ref-s').textContent).toContain('Kayıtlı referans yok');
  });

  it('"Referansı kaldır" ⇄ "Geri al" aynı düğme: odak <body>\'ye kaçmaz; çip × odağı ortam kutusuna verir', async () => {
    setup();
    const el = render();
    await tick();
    click(byId(el, 'acd-inst-btn-s_zeta-prod'));
    const btn = button(el, 'Referansı kaldır');
    act(() => { btn.focus(); });
    click(btn);
    expect(btn.isConnected).toBe(true);
    expect(btn.textContent).toBe('Geri al');
    expect(document.activeElement).toBe(btn);
    click(btn);
    expect(btn.textContent).toBe('Referansı kaldır');
    expect(document.activeElement).toBe(btn);
    click(el.querySelector('button[aria-label="int ortamını kaldır"]')!);
    expect(document.activeElement?.id).toBe('acd-env-in');
    expect(el.querySelector('button[aria-label="int ortamını kaldır"]')).toBeNull();
  });

  it('istemci reddinde hatalı DEĞER işaretlenir (err tonu + aria-describedby) ve başlık "1\'i hatalı" der', async () => {
    const s = baseSettings();
    s.instances![1] = { ...s.instances![1], apiUrl: 'ftp://argocd.team-a.example.invalid' };
    setup(s);
    const el = render();
    await tick();
    expect(el.textContent).toContain('3 instance · 2 hub');
    expect(el.textContent).not.toContain('hatalı');
    click(button(el, 'Kaydet'));
    await tick();
    expect(putArgoCDSettings).not.toHaveBeenCalled();
    expect(el.textContent).toContain("3 instance · 2 hub · 1'i hatalı");
    const row = instRows(el).find(r => r.textContent?.includes('team-a-prod'))!;
    const url = [...row.querySelectorAll('td')].find(td => td.textContent?.includes('argocd.team-a.example.invalid'))!;
    expect(url.classList.contains('cell-err')).toBe(true);
    expect(url.classList.contains('cell-muted')).toBe(false);
    expect(url.getAttribute('aria-describedby')).toBe('acd-inst-err-s_team-a-prod');
    const job = [...row.querySelectorAll('td')].find(td => td.textContent === 'team-a-prod-metrics')!;
    expect(job.classList.contains('cell-err')).toBe(false);
    expect(job.hasAttribute('aria-describedby')).toBe(false);
  });

  it('"Değişiklikleri geri al" hub kaldırma engelinin iletisini de siler', async () => {
    setup();
    const el = render();
    await tick();
    const hubAlert = () => [...el.querySelectorAll('[role="alert"]')].find(a => a.textContent?.includes('kaldırılamaz'));
    click(el.querySelector('button[aria-label="hub-1 hub listesinden kaldır"]')!);
    expect(hubAlert()).toBeTruthy();
    click(el.querySelector('input[aria-label^=\'cluster="hub-2"\']')!);
    expect(el.textContent).toContain('1 kaydedilmemiş değişiklik');
    click(button(el, 'Değişiklikleri geri al'));
    expect(hubAlert()).toBeUndefined();
    expect(el.textContent).toContain('Kayıtlı ayarlarla aynı.');
  });

  it('Kaydet uçuştayken düzenlenebilir alan kilitli; yanıt gelince açılır ve taslak = yanıt', async () => {
    setup();
    let finish: (() => void) | null = null;
    putArgoCDSettings.mockImplementation((body: ArgoCDSettingsInput) => new Promise(res => {
      finish = () => res(resp({ ...baseSettings(), ...body, instances: body.instances.map(({ clearTokenRef: _c, ...i }) => i) }));
    }));
    const el = render();
    await tick();
    click(el.querySelector('input[aria-label^=\'cluster="hub-2"\']')!);
    click(button(el, 'Kaydet'));
    await tick();
    expect(putArgoCDSettings).toHaveBeenCalledTimes(1);
    const inj = el.querySelector<HTMLInputElement>('input[aria-label^=\'cluster="hub-1"\']')!;
    const lock = inj.closest('fieldset')!;
    expect(lock.disabled).toBe(true);
    expect(lock.getAttribute('aria-busy')).toBe('true');
    expect(inj.matches(':disabled')).toBe(true);
    expect(button(el, '+ Instance ekle').matches(':disabled')).toBe(true);
    expect(button(el, 'Değişiklikleri geri al').disabled).toBe(true);
    click(inj);
    expect(inj.checked).toBe(true);
    await act(async () => { finish!(); });
    await tick();
    expect(lock.disabled).toBe(false);
    expect(lock.hasAttribute('aria-busy')).toBe(false);
    expect(inj.matches(':disabled')).toBe(false);
    expect(el.textContent).toContain('Kayıtlı ayarlarla aynı.');
    expect((el.querySelector('input[aria-label^=\'cluster="hub-2"\']') as HTMLInputElement).checked).toBe(true);
  });

  it('hata kutusu Kaydet anının görüntüsü: satır içi formda yazmak onu yeniden yazmaz; taslak düzenlenince kapanır, alan hatası canlı kalır', async () => {
    setup();
    const el = render();
    await tick();
    click(byId(el, 'acd-adv-btn'));
    setInput(byId(el, 'acd-adv-reader-timeoutS'), '99');
    click(button(el, 'Kaydet'));
    await tick();
    const box = () => [...el.querySelectorAll('[role="alert"]')].find(a => a.textContent?.startsWith('Kaydedilmedi —'));
    const before = box()?.textContent ?? '';
    expect(before).toContain('Kaydedilmedi — 1 alan düzeltilmeli.');
    expect(before).toContain('girilen 99');
    click(byId(el, 'acd-inst-btn-s_zeta-prod'));
    setInput(byId(el, 'acd-ed-name'), 'Z');
    setInput(byId(el, 'acd-ed-name'), 'Ze');
    expect(box()?.textContent).toBe(before);
    click(button(el, 'Vazgeç'));
    setInput(byId(el, 'acd-adv-reader-timeoutS'), '120');
    expect(box()).toBeUndefined();
    expect(el.textContent).toContain('girilen 120');
  });

  it('400 pins[0].clusterId → bağlantı pins notunu açar ve başlığına odaklanır; gidilecek yeri olmayan yol düz metin', async () => {
    setup();
    putArgoCDSettings.mockRejectedValueOnce(new Error('HTTP 400: {"error":"pins[0].clusterId: bilinmeyen Remote Cluster id","field":"pins[0].clusterId"}'));
    const el = render();
    await tick();
    await save(el);
    const box = [...el.querySelectorAll('[role="alert"]')].find(a => a.textContent?.includes('sunucu reddetti'))!;
    const link = [...box.querySelectorAll('button')].find(b => b.textContent === 'pins[0].clusterId')!;
    expect(el.querySelector('#acd-pins-body')).toBeNull();
    click(link);
    await tick();
    expect(el.querySelector('#acd-pins-body')).not.toBeNull();
    expect(document.activeElement?.id).toBe('acd-pins-btn');
  });

  it('400 {field: "instances"} (gidilecek yer yok) → yol bağlantı değil düz metin', async () => {
    setup();
    putArgoCDSettings.mockRejectedValueOnce(new Error('HTTP 400: {"error":"instances: en çok 64 instance","field":"instances"}'));
    const el = render();
    await tick();
    await save(el);
    const box = [...el.querySelectorAll('[role="alert"]')].find(a => a.textContent?.includes('sunucu reddetti'))!;
    expect(box.querySelectorAll('button')).toHaveLength(0);
    expect(box.textContent).toContain('instances — en çok 64 instance');
  });

  it('⋯ → "Düzenle" açık satırda formu kapatmaz; kirli formda da engel çıkmaz, yazılan korunur', async () => {
    setup();
    const el = render();
    await tick();
    const menuEdit = () => {
      click(byId(el, 'acd-inst-menu-s_zeta-prod'));
      click([...el.ownerDocument.querySelectorAll('.ui-popover [role="menuitem"]')].find(b => b.textContent === 'Düzenle')!);
    };
    menuEdit();
    expect(el.querySelector('#acd-inst-edit')).not.toBeNull();
    menuEdit();
    expect(el.querySelector('#acd-inst-edit')).not.toBeNull();
    setInput(byId(el, 'acd-ed-name'), 'Zeta prod 2');
    menuEdit();
    expect(el.querySelector('#acd-inst-edit')).not.toBeNull();
    expect(byId<HTMLInputElement>(el, 'acd-ed-name').value).toBe('Zeta prod 2');
    expect(live(el)).not.toContain('uygulanmamış');
    // Satırın ad düğmesi aç/kapa kalır (temiz formda kapatır).
    click(button(el, 'Vazgeç'));
    click(byId(el, 'acd-inst-btn-s_zeta-prod'));
    expect(el.querySelector('#acd-inst-edit')).not.toBeNull();
    click(byId(el, 'acd-inst-btn-s_zeta-prod'));
    expect(el.querySelector('#acd-inst-edit')).toBeNull();
  });
});
