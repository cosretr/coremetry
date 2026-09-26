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
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ThanosClusterInput, ThanosSettingsInput, ThanosSnapshot } from '@/lib/types';

const { getThanosSettings, putThanosSettings } = vi.hoisted(() => {
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
  return {
    getThanosSettings: vi.fn(async () => snap),
    putThanosSettings: vi.fn(async (s: ThanosSettingsInput) => ({
      clusters: s.clusters.map(c => ({ ...c, id: c.id ?? 'c-new', hasToken: false })),
    }) as ThanosSnapshot),
  };
});
vi.mock('@/lib/api', () => ({ api: { getThanosSettings, putThanosSettings } }));
vi.mock('@/lib/queries', () => ({ useClusters: () => ({ data: ['cluster-a', 'cluster-b'] }) }));
vi.mock('./SpanClusterValuesPanel', () => ({ SpanClusterValuesPanel: () => null }));

import { ClustersTab } from './ClustersTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<ClustersTab />); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putThanosSettings.mockClear();
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
