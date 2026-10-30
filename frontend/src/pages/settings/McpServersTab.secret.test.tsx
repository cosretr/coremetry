// @vitest-environment jsdom
// McpServersTab.secret.test.tsx — Faz 2 dilim 1 R13 (plan R-1, secret
// sözleşmesi; v0.10.87 DevOps deseni + "********" sentineli). Form shadcn'e
// geçerken sözleşme AYNEN kalmalı; ölçüm işaretlemeden bağımsız:
//   - saklı token forma YANKILANMAZ: alan yalnız "********" sentinelini taşır
//     (sunucu yanlışlıkla `token` gönderse bile);
//   - saklıyken ipucu "Kayıtlı bir token var"; değilken "Opsiyonel";
//   - dokunulmamış alan PUT'a "********" gider (sunucu: sentinel = koru);
//   - yazılan token AYNEN gider (kırpma yok);
//   - stdio ortamı: saklı anahtarlar KEY=******** satırı, değer yankılanmaz.
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { McpServerInput } from '@/lib/types';

const h = vi.hoisted(() => ({
  snap: { current: {} as Record<string, unknown> },
  put: vi.fn<(s: McpServerInput[]) => Promise<unknown>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getMcpServers: () => Promise.resolve(h.snap.current),
    putMcpServers: (s: McpServerInput[]) => h.put(s),
    testMcpServer: () => Promise.resolve({ ok: true, tools: 1 }),
  },
}));

import { McpServersTab } from './McpServersTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const HTTP = {
  name: 'kb', transport: 'http', url: 'https://mcp.example.invalid', enabled: true,
  hasToken: true, token: 'LEAKED-SECRET',
};

let host: HTMLDivElement | null = null; let root: Root | null = null;
async function render(): Promise<HTMLElement> {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<McpServersTab />); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  h.put.mockReset();
});
const pw = (el: HTMLElement) => el.querySelector<HTMLInputElement>('input[type="password"]')!;
async function setValue(ctl: HTMLInputElement, text: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
    ctl.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function save(el: HTMLElement) {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Kaydet')!;
  await act(async () => { btn.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  expect(h.put).toHaveBeenCalledTimes(1);
  return h.put.mock.calls[0][0];
}

describe('McpServersTab — token secret sözleşmesi', () => {
  it('saklı token yankılanmaz; alan sentinel, ipucu "Kayıtlı"', async () => {
    h.snap.current = { servers: [HTTP] };
    const el = await render();
    expect(pw(el).value).toBe('********');
    expect(pw(el).placeholder).toBe('********');
    expect(el.innerHTML).not.toContain('LEAKED-SECRET');
    expect(el.textContent).toContain('Kayıtlı bir token var');
    expect(pw(el).labels?.[0]?.textContent?.startsWith('Token')).toBe(true);
  });

  it('saklı değilse alan boş, ipucu "Opsiyonel"', async () => {
    h.snap.current = { servers: [{ ...HTTP, hasToken: false, token: undefined }] };
    const el = await render();
    expect(pw(el).value).toBe('');
    expect(el.textContent).toContain('Opsiyonel; Authorization: Bearer olarak gider.');
    expect(el.textContent).not.toContain('Kayıtlı bir token var');
  });

  it('dokunulmamış token sentinel olarak gider', async () => {
    h.snap.current = { servers: [HTTP] };
    h.put.mockResolvedValue({ servers: [HTTP] });
    const el = await render();
    const body = await save(el);
    expect(body[0].token).toBe('********');
    expect(body[0].url).toBe(HTTP.url);
  });

  it('yazılan token aynen gider', async () => {
    h.snap.current = { servers: [{ ...HTTP, hasToken: false, token: undefined }] };
    h.put.mockResolvedValue({ servers: [HTTP] });
    const el = await render();
    await setValue(pw(el), '  tok with spaces ');
    const body = await save(el);
    expect(body[0].token).toBe('  tok with spaces ');
  });

  it('stdio ortamı: saklı anahtar KEY=******** satırı, değer yankılanmaz', async () => {
    h.snap.current = { servers: [{ name: 'fs', transport: 'stdio', command: '/opt/x', enabled: true, hasToken: false, envKeys: ['API_KEY'], env: { API_KEY: 'LEAKED-ENV' } }] };
    const el = await render();
    const ta = el.querySelector('textarea')!;
    expect(ta.value).toBe('API_KEY=********');
    expect(el.innerHTML).not.toContain('LEAKED-ENV');
  });
});
