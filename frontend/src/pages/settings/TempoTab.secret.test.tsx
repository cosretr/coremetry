// @vitest-environment jsdom
// TempoTab.secret.test.tsx — Faz 2 dilim 1 R8 (plan R-1, secret sözleşmesi;
// Tempo deseni v0.8.577 — sözleşmenin ŞABLONU bu sekme). Form shadcn'e
// geçerken sözleşme AYNEN kalmalı; ölçüm işaretlemeden bağımsız (rol/tip/metin):
//   - saklı token forma YANKILANMAZ (password alanı boş başlar — sunucu bir
//     gün yanlışlıkla `token` gönderse bile);
//   - saklıyken etiket satırında "· stored" + yer tutucu
//     "(leave empty to keep stored value)"; saklı değilken ikisi de yok;
//   - dokunulmamış alan PUT'a '' gider (sunucu: boş = saklı olanı koru);
//   - yazılan token AYNEN gider (kırpma yok);
//   - token referansı (tokenRef) secret DEĞİL: görünür, kırpılıp gider.
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { TempoSettingsInput } from '@/lib/types';
import { ConfirmGate } from '@/components/coremetry/Confirm';

const { getTempoSettings, putTempoSettings, snap } = vi.hoisted(() => {
  const snap = {
    current: {
      enabled: true, baseUrl: 'https://tempo.example.invalid', authType: 'bearer',
      hasToken: true, token: 'LEAKED-SECRET', tokenRef: 'env:TEMPO_TOKEN',
      username: '', orgId: '', insecureSkipVerify: false,
    } as Record<string, unknown>,
  };
  return {
    snap,
    getTempoSettings: vi.fn(async () => snap.current),
    putTempoSettings: vi.fn(async (s: TempoSettingsInput) => ({ ...s, hasToken: true })),
  };
});
vi.mock('@/lib/api', () => ({ api: { getTempoSettings, putTempoSettings } }));

import { TempoTab } from './TempoTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<ConfirmGate><TempoTab /></ConfirmGate>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putTempoSettings.mockClear();
  snap.current = { ...snap.current, hasToken: true, authType: 'bearer' };
});
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
const pw = (el: HTMLElement) => el.querySelector<HTMLInputElement>('input[type="password"]')!;
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function save(el: HTMLElement) {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Save')!;
  act(() => { btn.click(); });
  await tick();
  expect(putTempoSettings).toHaveBeenCalledTimes(1);
  return putTempoSettings.mock.calls[0][0] as TempoSettingsInput;
}

describe('TempoTab — token secret sözleşmesi', () => {
  it('saklı token yankılanmaz; "· stored" + saklı yer tutucu, etiket bağlı', async () => {
    const el = render();
    await tick();
    expect(pw(el).value).toBe('');
    expect(el.innerHTML).not.toContain('LEAKED-SECRET');
    expect(pw(el).placeholder).toBe('(leave empty to keep stored value)');
    expect(el.textContent).toContain('· stored');
    expect(pw(el).labels?.[0]?.textContent?.startsWith('Bearer token')).toBe(true);
  });

  it('saklı değilse "· stored" yok, yer tutucu "paste token…"', async () => {
    snap.current = { ...snap.current, hasToken: false };
    const el = render();
    await tick();
    expect(pw(el).placeholder).toBe('paste token…');
    expect(el.textContent).not.toContain('· stored');
  });

  it('dokunulmamış token PUT\'a boş gider; tokenRef görünür ve kırpılır', async () => {
    const el = render();
    await tick();
    const ref = [...el.querySelectorAll<HTMLInputElement>('input')].find(i => i.value === 'env:TEMPO_TOKEN');
    expect(ref).toBeTruthy();
    act(() => { setValue(ref!, '  env:TEMPO_TOKEN2 '); });
    const sent = await save(el);
    expect(sent.token).toBe('');
    expect(sent.tokenRef).toBe('env:TEMPO_TOKEN2');
    expect(sent.authType).toBe('bearer');
  });

  it('yazılan token aynen gider; kayıttan sonra alan yeniden boş', async () => {
    const el = render();
    await tick();
    act(() => { setValue(pw(el), '  tok with spaces '); });
    const sent = await save(el);
    expect(sent.token).toBe('  tok with spaces ');
    expect(pw(el).value).toBe('');
  });
});
