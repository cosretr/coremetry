// @vitest-environment jsdom
// ClustersTab.contract.test.tsx — v0.10.956 — Rollouts v2 P1.3
// (docs/rollouts/v2-audit.md §3.2). Remote Cluster kaydının üç yeni alanı —
// apiServerUrls (virgül/satır listesi), argoSuffix, pairGroup — GET → form →
// "Save all" PUT gidiş-dönüşünde kaybolmaz:
//   - saklı değerler forma dolar ve aynen geri gönderilir;
//   - `apiServerUrls` dizi olarak gider (boşsa []): sunucu anahtarın
//     varlığını "alanları bilen istemci" işareti sayar — anahtarı olmayan eski
//     istemcide saklı değerleri korur, bu formda boş liste = temizle;
//   - inceleme (karışık sürüm): satırı ESKİ pod yüklediyse (snapshot'ta
//     `apiServerUrls` yok) ve liste elle değişmediyse anahtar GİTMEZ — form
//     saklı değeri görmedi, `[]` yeni pod'da onu silerdi;
//   - liste girdisi virgül ve satır sonuyla bölünür, kırpılır, boşlar atılır.
// Ağ mock; SpanClusterValuesPanel (react-query) ve telemetri adları stub.
//
// v0.10.974 — "Argo CD eşlemesi" (onaylı mockup ClusterFields.dc.html):
//   - Argo CD ayarı kendi efektinde, en-iyi-çaba okunur: hub kaydında nötr
//     "Argo hub" rozeti + instance sayılı not + Ayarlar › Argo CD bağlantısı;
//     not ad girdisi, Etkin kutusu ve Remove düğmesinden aria-describedby ile
//     bağlı; okuma reddedilirse rozet de hata metni de YOK, form çizilir;
//   - küme-içi API server adresi ve büyük/küçük harf duyarsız yinelenen ek
//     istemcide yakalanır: PUT GİTMEZ, özet role=alert, madde alana götürür;
//   - önizleme kaydedilecek biçimi ve notlarını gösterir; pairGroup ipucu
//     "Bu grupta: …" diğer kayıtları sayar; sunucu hatası yedek metin kalır.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ArgoCDSettingsResponse, ThanosClusterInput, ThanosSettingsInput, ThanosSnapshot } from '@/lib/types';

const { getThanosSettings, putThanosSettings, getArgoCDSettings } = vi.hoisted(() => {
  const snap: ThanosSnapshot = {
    clusters: [
      {
        id: 'c-aaaaaaaa', name: 'cluster-a', url: 'http://thanos-a.example.invalid', hasToken: true, enabled: true,
        authType: 'bearer',
        apiServerUrls: ['https://api.cluster-a.example.invalid:6443', 'https://api-int.cluster-a.example.invalid:6443'],
        argoSuffix: 'ca', pairGroup: 'pair-1',
      },
      // Eski sunucu: üç alan hiç yok.
      { id: 'c-bbbbbbbb', name: 'cluster-b', url: 'http://thanos-b.example.invalid', hasToken: false, enabled: true, authType: 'none' },
    ],
  };
  // v0.10.974 — Argo CD ayarı: cluster-a (c-aaaaaaaa) hub, iki instance bağlı;
  // üçüncü instance listede olmayan bir hub'a işaret ediyor (sayılmaz).
  const argo: ArgoCDSettingsResponse = {
    settings: {
      enabled: true,
      hubs: [{ clusterId: 'c-aaaaaaaa', injectClusterLabel: true }],
      instances: [
        { id: 'team-a-prod', hubClusterId: 'c-aaaaaaaa', hubNamespace: 'team-a-prod', enabled: true },
        { id: 'team-a-int', hubClusterId: 'c-aaaaaaaa', hubNamespace: 'team-a-int', enabled: false },
        { id: 'team-b-prod', hubClusterId: 'c-zzzzzzzz', hubNamespace: 'team-b-prod', enabled: true },
      ],
      apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {},
    },
    resolved: { enabled: true, apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {} },
    defaults: { enabled: false, apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {} },
    bounds: {}, tokens: {}, hubs: [{ id: 'c-aaaaaaaa', name: 'cluster-a', enabled: true, found: true, injectClusterLabel: true }],
  };
  return {
    getThanosSettings: vi.fn(async () => snap),
    putThanosSettings: vi.fn(async (s: ThanosSettingsInput) => ({
      clusters: s.clusters.map(c => ({ ...c, id: c.id ?? 'c-new', hasToken: false })),
    }) as ThanosSnapshot),
    getArgoCDSettings: vi.fn(async () => argo),
  };
});
vi.mock('@/lib/api', () => ({ api: { getThanosSettings, putThanosSettings, getArgoCDSettings } }));
vi.mock('@/lib/queries', () => ({ useClusters: () => ({ data: ['cluster-a', 'cluster-b'] }) }));
vi.mock('./SpanClusterValuesPanel', () => ({ SpanClusterValuesPanel: () => null }));

import { ClustersTab } from './ClustersTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  // v0.10.974 — Argo CD sekmesine gerçek <Link> → yönlendirici bağlamı.
  act(() => { root!.render(<MemoryRouter><ClustersTab /></MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putThanosSettings.mockClear();
  getArgoCDSettings.mockClear();
});
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };

// Field atomu label[for] → input/textarea bağını kurar; i. satırın alanı.
function fieldByLabel(el: HTMLElement, label: string, i: number): HTMLInputElement | HTMLTextAreaElement {
  const labels = [...el.querySelectorAll('label.field-label')].filter(l => l.textContent?.startsWith(label));
  const lab = labels[i] as HTMLLabelElement | undefined;
  if (!lab) throw new Error(`"${label}" etiketli alan #${i} yok`);
  const ctl = el.ownerDocument.getElementById(lab.htmlFor);
  if (!ctl) throw new Error(`"${label}" etiketi bir alana bağlı değil`);
  return ctl as HTMLInputElement | HTMLTextAreaElement;
}
function setValue(ctl: HTMLInputElement | HTMLTextAreaElement, text: string) {
  const proto = ctl instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function saveAll(el: HTMLElement): Promise<ThanosClusterInput[]> {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Save all');
  if (!btn) throw new Error('Save all yok');
  act(() => { (btn as HTMLButtonElement).click(); });
  await tick();
  expect(putThanosSettings).toHaveBeenCalledTimes(1);
  return (putThanosSettings.mock.calls[0][0] as ThanosSettingsInput).clusters;
}

// api.putThanosSettings gövdeyi JSON.stringify ile yollar — sunucunun gördüğü biçim.
const wire = (c: ThanosClusterInput): Record<string, unknown> => JSON.parse(JSON.stringify(c)) as Record<string, unknown>;

const URLS = 'API server URLs';
const SUFFIX = 'Argo app suffix';
const PAIR = 'Pair group';

describe('ClustersTab — Rollouts v2 alanları (v0.10.956)', () => {
  it('saklı apiServerUrls / argoSuffix / pairGroup forma dolar ve aynen geri gönderilir', async () => {
    const el = render();
    await tick();
    expect(fieldByLabel(el, URLS, 0).value).toBe('https://api.cluster-a.example.invalid:6443\nhttps://api-int.cluster-a.example.invalid:6443');
    expect(fieldByLabel(el, SUFFIX, 0).value).toBe('ca');
    expect(fieldByLabel(el, PAIR, 0).value).toBe('pair-1');
    expect(fieldByLabel(el, URLS, 1).value).toBe('');

    const sent = await saveAll(el);
    expect(sent[0].apiServerUrls).toEqual(['https://api.cluster-a.example.invalid:6443', 'https://api-int.cluster-a.example.invalid:6443']);
    expect(sent[0].argoSuffix).toBe('ca');
    expect(sent[0].pairGroup).toBe('pair-1');
    // v0.10.956 — inceleme: alanı olmayan kayıt = GET'i ESKİ bir pod yanıtladı
    // (rolling upgrade). Form saklı değeri hiç görmedi; `apiServerUrls: []`
    // göndermek yeni pod'da gövdeyi yetkili yapıp saklı üç alanı SİLERDİ.
    // Anahtar gitmez → sunucu saklı listeyi ve boş metinlerin saklı değerini korur.
    // Tel biçimi api.putThanosSettings'in JSON.stringify'ı: undefined anahtar düşer.
    expect(wire(sent[1])).not.toHaveProperty('apiServerUrls');
    expect(sent[1].argoSuffix ?? '').toBe('');
    expect(sent[1].pairGroup ?? '').toBe('');
    // Kaydet yanıtı forma geri yazılır (gidiş-dönüş kapanır).
    expect(fieldByLabel(el, SUFFIX, 0).value).toBe('ca');
  });

  it('düzenleme: liste virgül/satır sonuyla bölünür, kırpılır, boşlar atılır; temizleme [] ve "" gönderir', async () => {
    const el = render();
    await tick();
    act(() => { setValue(fieldByLabel(el, URLS, 1), ' https://api.cluster-b.example.invalid:6443 ,\n\nHTTPS://api-2.cluster-b.example.invalid/, '); });
    act(() => { setValue(fieldByLabel(el, SUFFIX, 1), '  cb '); });
    act(() => { setValue(fieldByLabel(el, PAIR, 1), ' pair-1 '); });
    // cluster-a'nın üç alanı da temizleniyor.
    act(() => { setValue(fieldByLabel(el, URLS, 0), ''); });
    act(() => { setValue(fieldByLabel(el, SUFFIX, 0), ''); });
    act(() => { setValue(fieldByLabel(el, PAIR, 0), ''); });

    const sent = await saveAll(el);
    expect(sent[1].apiServerUrls).toEqual(['https://api.cluster-b.example.invalid:6443', 'HTTPS://api-2.cluster-b.example.invalid/']);
    expect(sent[1].argoSuffix).toBe('cb');
    expect(sent[1].pairGroup).toBe('pair-1');
    expect(sent[0].apiServerUrls).toEqual([]);
    expect(sent[0].argoSuffix).toBe('');
    expect(sent[0].pairGroup).toBe('');
  });

  it('eski sunucudan yüklenen kayıt: yalnız suffix/pair düzenlenirse liste anahtarı gitmez; yeni satır anahtarı [] ile gönderir', async () => {
    const el = render();
    await tick();
    act(() => { setValue(fieldByLabel(el, SUFFIX, 1), 'cb'); });
    act(() => { setValue(fieldByLabel(el, PAIR, 1), 'pair-1'); });
    const addBtn = [...el.querySelectorAll('button')].find(b => /add cluster/i.test(b.textContent ?? ''));
    if (!addBtn) throw new Error('Add cluster yok');
    act(() => { (addBtn as HTMLButtonElement).click(); });
    // Yeni satırın ad girdisi (Combobox): adsız satır kaydı keser.
    const nameInputs = [...el.querySelectorAll<HTMLInputElement>('input[placeholder="prod-ist"]')];
    expect(nameInputs).toHaveLength(3);
    act(() => { setValue(nameInputs[2], 'cluster-new'); });
    // Etkin satırda URL `required` — boşken form gönderilmez.
    const urlInputs = [...el.querySelectorAll<HTMLInputElement>('input[placeholder^="https://thanos-querier"]')];
    expect(urlInputs).toHaveLength(3);
    act(() => { setValue(urlInputs[2], 'http://thanos-new.example.invalid'); });
    const sent = await saveAll(el);
    // Dolu metin anahtarsız da uygulanır (sunucu sözleşmesi); saklı liste korunur.
    expect(wire(sent[1])).not.toHaveProperty('apiServerUrls');
    expect(sent[1].argoSuffix).toBe('cb');
    expect(sent[1].pairGroup).toBe('pair-1');
    // Formda yeni eklenen satır: saklı değer yok, anahtar [] ile gider.
    expect(sent[2].apiServerUrls).toEqual([]);
  });

  it('eski sunucudan yüklenen kayıtta liste DÜZENLENİRSE gövde yetkili olur (anahtar gider)', async () => {
    const el = render();
    await tick();
    act(() => { setValue(fieldByLabel(el, URLS, 1), 'https://api.cluster-b.example.invalid:6443'); });
    const sent = await saveAll(el);
    expect(sent[1].apiServerUrls).toEqual(['https://api.cluster-b.example.invalid:6443']);
  });
});

// ── v0.10.974 — Argo CD eşlemesi (ClusterFields.dc.html) ─────────────────────
const card = (el: HTMLElement, name: string): HTMLElement => {
  const g = el.querySelector<HTMLElement>(`[role="group"][aria-label="${name} kaydı"]`);
  if (!g) throw new Error(`${name} kartı yok`);
  return g;
};
const alertBox = (el: HTMLElement) => el.querySelector<HTMLElement>('[role="alert"]');
async function clickSave(el: HTMLElement) {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Save all');
  if (!btn) throw new Error('Save all yok');
  act(() => { (btn as HTMLButtonElement).click(); });
  await tick();
}

describe('ClustersTab — Argo CD eşlemesi (v0.10.974)', () => {
  it('hub kaydı: nötr "Argo hub" rozeti + instance sayılı not + Argo CD bağlantısı; not üç kontrole bağlı', async () => {
    const el = render();
    await tick();
    expect(getArgoCDSettings).toHaveBeenCalledTimes(1);
    const a = card(el, 'cluster-a');
    const badge = [...a.querySelectorAll('.badge')].find(b => b.textContent?.trim() === 'Argo hub');
    expect(badge, 'Argo hub rozeti').toBeTruthy();
    // Nötr: öznitelik, sapma değil (b-gray; yeşil/kırmızı yok).
    expect(badge!.className).toContain('b-gray');
    expect(badge!.querySelector('svg')).toBeTruthy();
    const link = a.querySelector<HTMLAnchorElement>('a[href="/settings/argocd"]');
    expect(link?.textContent).toBe('Ayarlar › Argo CD');
    const note = link!.parentElement!;
    expect(note.textContent).toBe("Argo CD hub'ı (2 instance): kaldırır ya da kapatırsanız Argo CD ayarı bir sonraki kayıtta reddedilir. Hub seçimi ve küme etiketi kararı Ayarlar › Argo CD sekmesinde; burada değiştirilemez.");
    expect(note.id).not.toBe('');
    const name = a.querySelector<HTMLInputElement>('input[placeholder="prod-ist"]')!;
    const enabled = a.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    const remove = [...a.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Remove')!;
    for (const ctl of [name, enabled, remove]) expect(ctl.getAttribute('aria-describedby')).toBe(note.id);
    // Uyarır, engellemez: Remove etkin, kayıt kaldırılabilir.
    expect(remove.disabled).toBe(false);

    const b = card(el, 'cluster-b');
    expect(b.textContent).not.toContain('Argo hub');
    expect(b.querySelector('a[href="/settings/argocd"]')).toBeNull();
    expect(b.querySelector('input[placeholder="prod-ist"]')!.hasAttribute('aria-describedby')).toBe(false);
    // Alt başlık iki kartta da, üç v0.10.956 alanının üstünde.
    // v0.10.974 — inceleme: mockup'taki gibi GERÇEK h3 (başlık gezinmesi),
    // büyük harfli SectionHead ayırıcısı değil.
    for (const c of [a, b]) {
      const h3 = [...c.querySelectorAll('h3')].find(h => h.textContent === 'Argo CD eşlemesi');
      expect(h3, 'Argo CD eşlemesi h3').toBeTruthy();
      expect(h3!.closest('.dtl-sech')).toBeNull();
      expect(h3!.nextElementSibling?.textContent).toBe("Argo'nun dest_server ve uygulama adı eki bu kümeye nasıl çözülür");
    }
  });

  it('Argo CD ayarı okunamazsa: rozet yok, not yok, hata metni yok — form yine çizilir', async () => {
    getArgoCDSettings.mockRejectedValueOnce(new Error('HTTP 503: {"error":"argocd settings store unavailable"}'));
    const el = render();
    await tick();
    expect(el.textContent).not.toContain('Argo hub');
    expect(el.querySelector('a[href="/settings/argocd"]')).toBeNull();
    expect(el.textContent).not.toMatch(/503|unavailable|okunamadı/);
    expect(alertBox(el)).toBeNull();
    expect(fieldByLabel(el, SUFFIX, 0).value).toBe('ca');
  });

  it('önizleme kaydedilecek biçimi gösterir; pairGroup "Bu grupta: …"; geçerli gövde PUT edilir', async () => {
    const el = render();
    await tick();
    const a = card(el, 'cluster-a');
    expect(a.textContent).toContain('Kaydedilecek biçim · tüm kayıtlarda tekil');
    expect(a.textContent).toContain('https://api.cluster-a.example.invalid:6443değişmedi');
    act(() => { setValue(fieldByLabel(el, URLS, 1), 'HTTPS://API.Cluster-B.example.invalid/'); });
    act(() => { setValue(fieldByLabel(el, PAIR, 1), 'pair-1'); });
    const b = card(el, 'cluster-b');
    expect(b.textContent).toContain('https://api.cluster-b.example.invalid:6443küçük harf · sondaki / atıldı · :6443 eklendi');
    expect(b.textContent).toContain('https://kubernetes.default.svc yazılmaz — Argo\'nun küme-içi hedefi instance\'ın hub\'ına çözülür; hub için dış API adresini girin.');
    const pairB = fieldByLabel(el, PAIR, 1);
    expect(el.ownerDocument.getElementById(pairB.getAttribute('aria-describedby')!)?.textContent)
      .toBe('Serbest metin · aynı değeri taşıyan kümeler aktif-aktif çifttir. Bu grupta: cluster-a.');
    const pairA = fieldByLabel(el, PAIR, 0);
    expect(el.ownerDocument.getElementById(pairA.getAttribute('aria-describedby')!)?.textContent)
      .toBe('Serbest metin · aynı değeri taşıyan kümeler aktif-aktif çifttir. Bu grupta: cluster-b.');
    const sent = await saveAll(el);
    expect(sent[1].apiServerUrls).toEqual(['HTTPS://API.Cluster-B.example.invalid/']);
  });

  it('küme-içi adres istemcide engellenir: PUT yok, satır içi hata, bağlantılı özet alana götürür', async () => {
    const el = render();
    await tick();
    const ta = fieldByLabel(el, URLS, 1);
    act(() => { setValue(ta, 'https://api.cluster-b.example.invalid:6443\nhttps://kubernetes.default.svc'); });
    expect(ta.getAttribute('aria-invalid')).toBe('true');
    expect(card(el, 'cluster-b').textContent).toContain('https://kubernetes.default.svcyazılamaz: küme-içi hedef');
    // Geçerli satırın alanı temiz.
    expect(fieldByLabel(el, URLS, 0).getAttribute('aria-invalid')).toBeNull();

    await clickSave(el);
    expect(putThanosSettings).not.toHaveBeenCalled();
    const box = alertBox(el)!;
    expect(box.textContent).toContain('Kaydedilmedi — istemci denetimi 1 sorun buldu; istek gönderilmedi, hiçbir kayıt değişmedi.');
    expect(box.textContent).toContain("· cluster-b · API server URL'leri: https://kubernetes.default.svc yazılamaz — Argo'nun küme-içi hedefi instance'ın hub'ına çözülür. Satırı silin; hub'ın dış API adresi zaten listede.");
    const go = [...box.querySelectorAll('button')].find(b => b.textContent === "cluster-b · API server URL'leri")!;
    act(() => { go.click(); });
    expect(el.ownerDocument.activeElement).toBe(ta);
  });

  it('yinelenen ek (büyük/küçük harf duyarsız) engellenir; hata DÜZENLENEN satırda', async () => {
    const el = render();
    await tick();
    act(() => { setValue(fieldByLabel(el, SUFFIX, 1), 'CA'); });
    const sfxB = fieldByLabel(el, SUFFIX, 1);
    const sfxA = fieldByLabel(el, SUFFIX, 0);
    expect(sfxB.getAttribute('aria-invalid')).toBe('true');
    expect(el.ownerDocument.getElementById(sfxB.getAttribute('aria-describedby')!)?.textContent)
      .toBe('“CA” eki cluster-a kaydında da var (büyük/küçük harf duyarsız). Ek, uygulama adının son jetonundan kümeyi seçer; her kümede tekil olmalı. cluster-b uygulamalarındaki son jetonu girin, ör. cb.');
    expect(sfxA.getAttribute('aria-invalid')).toBeNull();
    expect(el.ownerDocument.getElementById(sfxA.getAttribute('aria-describedby')!)?.textContent)
      .toBe('Argo uygulama adının son jetonu (…-env-ek). Tekil, büyük/küçük harf duyarsız.');

    await clickSave(el);
    expect(putThanosSettings).not.toHaveBeenCalled();
    const box = alertBox(el)!;
    expect(box.textContent).toContain('Kaydedilmedi — istemci denetimi 1 sorun buldu; istek gönderilmedi, hiçbir kayıt değişmedi.');
    expect(box.textContent).toContain("· cluster-b · Argo app eki: “CA” cluster-a kaydında da var (büyük/küçük harf duyarsız). cluster-b'ye kendi ekini verin, ör. cb.");
    const go = [...box.querySelectorAll('button')].find(b => b.textContent === 'cluster-b · Argo app eki')!;
    act(() => { go.click(); });
    expect(el.ownerDocument.activeElement).toBe(sfxB);
  });

  it('istemci denetimi geçerse sunucunun ilk hatası yedek metin olarak gösterilir', async () => {
    putThanosSettings.mockRejectedValueOnce(new Error('HTTP 400: {"error":"apiServerUrls: https://api.cluster-x.example.invalid:6443 zaten \\"cluster-x\\" kaydına bağlı"}'));
    const el = render();
    await tick();
    await clickSave(el);
    expect(putThanosSettings).toHaveBeenCalledTimes(1);
    expect(alertBox(el)?.textContent).toBe('apiServerUrls: https://api.cluster-x.example.invalid:6443 zaten "cluster-x" kaydına bağlı');
  });
});
