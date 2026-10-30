// @vitest-environment jsdom
// OracleTab.secret.test.tsx — Faz 2 dilim 1 R16 (plan R-1, secret sözleşmesi;
// v0.10.580 oracleForm). Form shadcn'e geçerken şifre sözleşmesi AYNEN
// kalmalı; ölçüm işaretlemeden bağımsız (tip/etiket/metin):
//   - saklı şifre forma YANKILANMAZ (password alanı boş başlar — sunucu bir
//     gün yanlışlıkla `password` gönderse bile);
//   - saklıyken etikette "· kayıtlı" + yer tutucu "(saklı değeri korumak için
//     boş bırakın)"; saklı değilken "Oracle şifresi…" ve "· kayıtlı" yok;
//   - dokunulmamış şifre PUT gövdesine HİÇ girmez (sunucu: yok = koru);
//   - yazılan şifre gider; kenar boşluğunu sourceForSave kırpar (saf çekirdek).
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const SNAP = {
  id: 'core-bank', name: 'core-bank', host: 'oradb.example.invalid', port: 1521, serviceName: 'ORCLPDB1',
  user: 'coremetry_ro', password: 'LEAKED-SECRET', schema: 'APP', table: 'ERROR_LOG', enabled: false,
  hasPassword: true, passwordResolved: false,
};
const h = vi.hoisted(() => ({
  snap: { current: {} as Record<string, unknown> },
  put: vi.fn(async (b: { sources: Record<string, unknown>[] }) => ({ sources: b.sources.map(s => ({ ...s, hasPassword: true, passwordResolved: false })) })),
}));
vi.mock('@/lib/api', () => ({
  api: {
    oracleSettings: async () => ({ sources: [h.snap.current] }),
    putOracleSettings: h.put,
    oracleStatus: async () => ({ sources: [] }),
    oracleLearned: async () => ({ count: 0, entries: {} }),
    oracleSubjectCoverage: async () => ({}),
    oracleLivePreview: async () => ({}),
    testOracleSource: async () => ({ ok: true }),
  },
}));

import { OracleTab } from './OracleTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null; let root: Root | null = null;
async function render(): Promise<HTMLElement> {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<QueryClientProvider client={qc}><OracleTab /></QueryClientProvider>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  h.put.mockClear();
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
  return h.put.mock.calls[0][0].sources[0];
}

describe('OracleTab — şifre secret sözleşmesi', () => {
  it('saklı şifre yankılanmaz; "· kayıtlı" + saklı yer tutucu, etiket bağlı', async () => {
    h.snap.current = { ...SNAP };
    const el = await render();
    expect(pw(el).value).toBe('');
    expect(el.innerHTML).not.toContain('LEAKED-SECRET');
    expect(pw(el).placeholder).toBe('(saklı değeri korumak için boş bırakın)');
    expect(el.textContent).toContain('· kayıtlı');
    expect(pw(el).labels?.[0]?.textContent?.startsWith('Şifre')).toBe(true);
  });

  it('saklı değilse "· kayıtlı" yok, yer tutucu "Oracle şifresi…"', async () => {
    h.snap.current = { ...SNAP, hasPassword: false, password: undefined };
    const el = await render();
    expect(pw(el).placeholder).toBe('Oracle şifresi…');
    expect(el.textContent).not.toContain('· kayıtlı');
  });

  it('dokunulmamış şifre gövdeye girmez', async () => {
    h.snap.current = { ...SNAP };
    const el = await render();
    const body = await save(el);
    expect(body).not.toHaveProperty('password');
    expect(body.user).toBe('coremetry_ro');
  });

  it('yazılan şifre gider (sourceForSave kırpar)', async () => {
    h.snap.current = { ...SNAP };
    const el = await render();
    await setValue(pw(el), '  pw with spaces ');
    const body = await save(el);
    expect(body.password).toBe('pw with spaces'); // sourceForSave kırpar (oracleForm, v0.10.580)
  });
});
