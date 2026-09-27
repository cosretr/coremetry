// @vitest-environment jsdom
//
// ZoomChannelPicker.states — v0.10.967 (tablo standardı dilim 5, P-2 /
// recipe P7).
//
// NE ÇİVİLİYOR: kanal seçicinin statik tablosu (satır tıkı JID'i forma
// yazar, dt yok). Eskiden "Loading channels…" satırı, kırmızı hata kutusu ve
// boş cümle tablonun ÜSTÜNDEYDİ, tablo altta boş gövdeyle çiziliyordu.
//   • yükleniyor / hata / boş tablonun İÇİNDE, başlık dururken;
//   • aramanın hepsini elediği liste artık "Eşleşme yok" + "Filtreleri
//     temizle" (arama kutusunu boşaltır, odak kutuya döner) — eskiden boş
//     gövde; "eşleşme yok" yalnız süzülmemiş listede satır varken;
//   • SATIRLAR KAZANIR ama hata sessizleşmez: sunucu kesilmiş listede kısmi
//     kanalları hatayla BİRLİKTE döner, düşen Refresh eski listeyi bırakır —
//     iki hâlde satırlar kalır ve hata ÜSTTE şerit olarak görünür.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { ZoomChannelPicker } from './ZoomChannelPicker';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface FakeResponse { ok: boolean; status: number; text: () => Promise<string>; json: () => Promise<unknown> }
const okJson = (body: unknown): FakeResponse =>
  ({ ok: true, status: 200, text: () => Promise.resolve(JSON.stringify(body)), json: () => Promise.resolve(body) });
const failJson = (status: number, body: unknown): FakeResponse =>
  ({ ok: false, status, text: () => Promise.resolve(JSON.stringify(body)), json: () => Promise.resolve(body) });

const CHANNELS = [
  { id: 'c1', jid: 'ops@conference.xmpp.zoom.us', name: 'ops-alerts', type: 3 },
  { id: 'c2', jid: 'sre@conference.xmpp.zoom.us', name: 'sre-oncall', type: 4 },
];

const fetchMock = vi.fn<(url: string, init?: unknown) => Promise<FakeResponse>>();
const realFetch = globalThis.fetch;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function openPicker(): Promise<HTMLElement> {
  globalThis.fetch = fetchMock as unknown as typeof fetch;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root!.render(<ZoomChannelPicker accountId="acc" clientId="cid" clientSecret="sec"
      oauthBaseUrl="" apiBaseUrl="" onPick={() => {}} />);
  });
  await act(async () => { buttonByText(host!, /List my channels/)!.click(); });
  await flush();
  return host;
}
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  fetchMock.mockReset();
  globalThis.fetch = realFetch;
});

function buttonByText(el: Element, text: RegExp) {
  return Array.from(el.querySelectorAll('button')).find(b => text.test(b.textContent ?? ''));
}
function typeInto(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
const searchBox = (el: HTMLElement) => el.querySelector<HTMLInputElement>('input[placeholder^="Filter by name"]')!;
const stateRow = (el: HTMLElement, kind: string) => el.querySelector<HTMLTableRowElement>(`tbody tr[data-dt-state="${kind}"]`);
const stateRows = (el: HTMLElement) => el.querySelectorAll('tbody tr[data-dt-state]');
const dataRows = (el: HTMLElement) => Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));
// Tablonun ÜSTÜNDEKİ şerit: tablo kabından önce gelen, hata metnini taşıyan blok.
const bannerAbove = (el: HTMLElement, text: string) => {
  const wrap = el.querySelector('table')!.parentElement!;
  let n = wrap.previousElementSibling;
  while (n) { if (n.textContent?.includes(text)) return n; n = n.previousElementSibling; }
  return null;
};

describe('ZoomChannelPicker — durumlar tablonun içinde (P-2 / P7)', () => {
  it('yükleniyor: başlık durur, iskelet satırı colSpan = 3 <th>; üstte "Loading channels…" yok', async () => {
    fetchMock.mockReturnValue(new Promise(() => {}));
    const el = await openPicker();
    expect(el.querySelectorAll('thead th')).toHaveLength(3);
    const row = stateRow(el, 'loading');
    expect(row, 'yükleniyor tablonun içinde değil').not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(3);
    expect(row!.querySelector('[aria-label="Kanallar yükleniyor"]')).not.toBeNull();
    expect(el.textContent).not.toContain('Loading channels…');
  });

  it('boş: bot üyeliği cümlesi tablonun içinde', async () => {
    fetchMock.mockResolvedValue(okJson({ channels: [] }));
    const el = await openPicker();
    expect(stateRows(el)).toHaveLength(1);
    expect(stateRow(el, 'empty')!.textContent).toContain('Bu S2S uygulamasına görünen kanal yok');
  });

  it('hata, satır yok: tablonun içinde hata satırı sunucu metniyle, üstte kutu yok', async () => {
    fetchMock.mockResolvedValue(failJson(401, { error: 'invalid client credentials' }));
    const el = await openPicker();
    const row = stateRow(el, 'error');
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain('Zoom kanalları okunamadı: invalid client credentials');
    expect(bannerAbove(el, 'invalid client credentials')).toBeNull();
    expect(stateRow(el, 'empty')).toBeNull();
  });

  it('kısmi liste + hata: satırlar kalır, hata üstte şerit (sessiz kısmi liste yok)', async () => {
    fetchMock.mockResolvedValue(failJson(206, { channels: CHANNELS.slice(0, 1), error: 'truncated at 500 channels' }));
    const el = await openPicker();
    expect(dataRows(el)).toHaveLength(1);
    expect(stateRows(el)).toHaveLength(0);
    expect(bannerAbove(el, 'truncated at 500 channels')).not.toBeNull();
  });

  it('düşen Refresh: eski satırlar kalır ama hata üstte görünür', async () => {
    fetchMock.mockResolvedValueOnce(okJson({ channels: CHANNELS }));
    const el = await openPicker();
    expect(dataRows(el)).toHaveLength(2);
    fetchMock.mockRejectedValueOnce(new Error('network down'));
    await act(async () => { buttonByText(el, /^Refresh$/)!.click(); });
    await flush();
    expect(dataRows(el)).toHaveLength(2);
    expect(bannerAbove(el, 'network down'), 'düşen Refresh bayat listenin arkasında sessiz kaldı').not.toBeNull();
  });

  it('Refresh sürerken satırlar kalır: yükleniyor satırı yok, üstte "Loading channels…"', async () => {
    fetchMock.mockResolvedValueOnce(okJson({ channels: CHANNELS }));
    const el = await openPicker();
    fetchMock.mockReturnValueOnce(new Promise(() => {}));
    await act(async () => { buttonByText(el, /^Refresh$/)!.click(); });
    expect(dataRows(el)).toHaveLength(2);
    expect(stateRow(el, 'loading')).toBeNull();
    expect(el.textContent).toContain('Loading channels…');
  });

  it('arama hepsini eledi: "Eşleşme yok" + "Filtreleri temizle"; temizle kutuyu boşaltır, odak kutuya döner', async () => {
    fetchMock.mockResolvedValue(okJson({ channels: CHANNELS }));
    const el = await openPicker();
    typeInto(searchBox(el), 'zzz-yok');
    const row = stateRow(el, 'no-match');
    expect(row, 'süzülmüş boş liste boş gövde kaldı').not.toBeNull();
    expect(stateRow(el, 'empty')).toBeNull();
    const clear = buttonByText(row!, /Filtreleri temizle/)!;
    clear.focus();
    await act(async () => { clear.click(); });
    expect(searchBox(el).value).toBe('');
    expect(dataRows(el)).toHaveLength(2);
    expect(document.activeElement).toBe(searchBox(el));
  });
});
