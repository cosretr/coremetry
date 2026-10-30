// @vitest-environment jsdom
// ElasticTab.secret.test.tsx — Faz 2 dilim 1 R8 (plan R-1, secret sözleşmesi;
// TempoTab şablonu). İki secret: API key + basic-auth Password. Form shadcn'e
// geçerken sözleşme AYNEN kalmalı; ölçüm işaretlemeden bağımsız (rol/tip/metin):
//   - saklı değerler forma YANKILANMAZ (iki password alanı boş başlar);
//   - saklıyken etiket satırında "· stored" + saklı yer tutucu
//     (API key: "(leave empty to keep stored value)", Password: "(keep stored)");
//     saklı değilken ikisi de yok;
//   - dokunulmamış alanlar PUT'a '' gider (sunucu: boş = saklı olanı koru);
//   - yazılan değer AYNEN gider (kırpma yok); kayıttan sonra alanlar boşalır.
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ESLogstoreInput, ESLogstoreSnapshot } from '@/lib/types';

const { getLogstoreSettings, putLogstoreSettings, snap } = vi.hoisted(() => {
  const base = {
    backend: 'elasticsearch', addresses: ['https://es-0.example.invalid:9200'], username: 'reader',
    hasPassword: true, hasApiKey: true, insecureSkipVerify: false, index: 'app-*', indexTemplate: '',
    source: 'ui', fields: {},
    // Sunucu secret'ı hiç göndermez; gönderirse de forma girmemeli.
    password: 'LEAKED-PASSWORD', apiKey: 'LEAKED-APIKEY',
  };
  const snap = { base, current: base as unknown as ESLogstoreSnapshot };
  return {
    snap,
    getLogstoreSettings: vi.fn(async () => snap.current),
    putLogstoreSettings: vi.fn(async (_s: ESLogstoreInput) => snap.current),
  };
});
vi.mock('@/lib/api', () => ({ api: { getLogstoreSettings, putLogstoreSettings, testLogstoreSettings: vi.fn(async () => ({ ok: true })) } }));

import { ElasticTab } from './ElasticTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<ElasticTab />); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putLogstoreSettings.mockClear();
  snap.current = snap.base as unknown as ESLogstoreSnapshot;
});
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
const pws = (el: HTMLElement) => [...el.querySelectorAll<HTMLInputElement>('input[type="password"]')];
const byLabel = (el: HTMLElement, start: string) =>
  pws(el).find(i => i.labels?.[0]?.textContent?.trim().startsWith(start))!;
// Etiket satırı: eski işaretlemede sarmalayan <label>, yenisinde alan kabı.
const labelRow = (i: HTMLInputElement) => i.closest('[data-cm-field], label')!.textContent ?? '';
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function save(el: HTMLElement) {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Save & apply')!;
  act(() => { btn.click(); });
  await tick();
  expect(putLogstoreSettings).toHaveBeenCalledTimes(1);
  return putLogstoreSettings.mock.calls[0][0] as ESLogstoreInput;
}

describe('ElasticTab — API key + Password secret sözleşmesi', () => {
  it('saklı değerler yankılanmaz; her iki alanda "· stored" + saklı yer tutucu', async () => {
    const el = render();
    await tick();
    expect(pws(el)).toHaveLength(2);
    const key = byLabel(el, 'API key'); const pw = byLabel(el, 'Password');
    expect(key.value).toBe(''); expect(pw.value).toBe('');
    expect(el.innerHTML).not.toContain('LEAKED-');
    expect(key.placeholder).toBe('(leave empty to keep stored value)');
    expect(pw.placeholder).toBe('(keep stored)');
    expect(labelRow(key)).toContain('· stored');
    expect(labelRow(pw)).toContain('· stored');
  });

  it('saklı değilse "· stored" yok, saklı yer tutucu yok', async () => {
    snap.current = { ...snap.base, hasApiKey: false, hasPassword: false } as unknown as ESLogstoreSnapshot;
    const el = render();
    await tick();
    expect(el.textContent).not.toContain('· stored');
    expect(byLabel(el, 'API key').placeholder).toBe('base64 id:api_key — takes precedence over basic auth');
    expect(byLabel(el, 'Password').placeholder).toBe('');
  });

  it('dokunulmamış alanlar PUT\'a boş gider', async () => {
    const el = render();
    await tick();
    const sent = await save(el);
    expect(sent.apiKey).toBe('');
    expect(sent.password).toBe('');
    expect(sent.username).toBe('reader');
  });

  it('yazılan değerler aynen gider; kayıttan sonra alanlar boş', async () => {
    const el = render();
    await tick();
    act(() => { setValue(byLabel(el, 'API key'), ' key==  '); });
    act(() => { setValue(byLabel(el, 'Password'), '  p w '); });
    const sent = await save(el);
    expect(sent.apiKey).toBe(' key==  ');
    expect(sent.password).toBe('  p w ');
    expect(pws(el).map(i => i.value)).toEqual(['', '']);
  });
});
