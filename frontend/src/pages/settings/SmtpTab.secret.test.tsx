// @vitest-environment jsdom
// SmtpTab.secret.test.tsx — Faz 2 dilim 1 R9 (plan R-1, secret sözleşmesi;
// ClustersTab.secret.test emsali). SMTP parolası shadcn'e geçerken sözleşme
// AYNEN kalmalı, işaretlemeden bağımsız ölçülür (yalnız tip/etiket/değer):
//   - sunucu GET'te parolayı "********" nöbetçisiyle döner; gerçek parola
//     forma hiç girmez, alan type=password;
//   - dokunulmamış (nöbetçi duran) alan PUT'a '' gider (sunucu: boş/nöbetçi
//     = saklı olanı koru);
//   - yazılan parola AYNEN gider (kırpma yok);
//   - kayıtlı ve parolası boş gelmişse yer tutucu "(unchanged)".
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { SMTPSettings } from '@/lib/types';

const { getSMTP, putSMTP } = vi.hoisted(() => ({
  getSMTP: vi.fn(async (): Promise<SMTPSettings> => ({
    host: 'smtp.example.invalid', port: 587, username: 'mailer', password: '********',
    from: 'coremetry@example.invalid', fromName: '', startTLS: true, skipVerify: false, configured: true,
  })),
  putSMTP: vi.fn(async (s: SMTPSettings) => ({ ...s, password: '********', configured: true })),
}));
vi.mock('@/lib/api', () => ({ api: { getSMTP, putSMTP, testSMTP: vi.fn() } }));

import { SMTPTab } from './SmtpTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<SMTPTab />); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putSMTP.mockClear();
});
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
const pw = (el: HTMLElement) => el.querySelector<HTMLInputElement>('input[type="password"]')!;
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function save(el: HTMLElement) {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Save settings')!;
  await act(async () => { btn.closest('form')!.requestSubmit(btn); });
  await tick();
  expect(putSMTP).toHaveBeenCalledTimes(1);
  return putSMTP.mock.calls[0][0] as SMTPSettings;
}

describe('SmtpTab — parola secret sözleşmesi', () => {
  it('parola alanı type=password, "Password" etiketine bağlı; gerçek parola yok', async () => {
    const el = render();
    await tick();
    const p = pw(el);
    expect(p).not.toBeNull();
    expect(p.labels?.[0]?.textContent).toContain('Password');
    expect(el.querySelectorAll('input[type="password"]')).toHaveLength(1);
  });

  it('dokunulmamış parola PUT\'a boş gider (saklı korunur)', async () => {
    const el = render();
    await tick();
    const sent = await save(el);
    expect(sent.password).toBe('');
    expect(sent.host).toBe('smtp.example.invalid');
  });

  it('yazılan parola aynen gider', async () => {
    const el = render();
    await tick();
    act(() => { setValue(pw(el), '  new pass '); });
    const sent = await save(el);
    expect(sent.password).toBe('  new pass ');
  });

  it('kayıtlı + boş gelen parola: yer tutucu "(unchanged)"', async () => {
    getSMTP.mockResolvedValueOnce({
      host: 'h', port: 25, username: '', password: '', from: 'a@b.invalid', fromName: '',
      startTLS: false, skipVerify: false, configured: true,
    });
    const el = render();
    await tick();
    expect(pw(el).value).toBe('');
    expect(pw(el).placeholder).toBe('(unchanged)');
  });
});
