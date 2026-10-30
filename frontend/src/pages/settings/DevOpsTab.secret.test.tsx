// @vitest-environment jsdom
// DevOpsTab.secret.test.tsx — Faz 2 dilim 1 R13 (plan R-1, secret sözleşmesi;
// Tempo deseni). Form shadcn'e geçerken PAT sözleşmesi AYNEN kalmalı; ölçüm
// işaretlemeden bağımsız (tip/etiket/metin):
//   - saklı PAT forma YANKILANMAZ (password alanı boş başlar — sunucu bir gün
//     yanlışlıkla `pat` gönderse bile);
//   - saklıyken etikette "· kayıtlı" + yer tutucu "(boş bırak = saklı değer
//     korunur)"; saklı değilken "PAT yapıştır…" ve "· kayıtlı" yok;
//   - dokunulmamış PAT PUT gövdesine HİÇ girmez (sunucu: yok = saklıyı koru);
//   - yazılan PAT AYNEN gider (kırpma yok), kayıttan sonra alan yeniden boş.
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { DevOpsSnapshot } from '@/lib/types';
import { ConfirmGate } from '@/components/coremetry/Confirm';

const h = vi.hoisted(() => ({
  snap: { current: {} as Record<string, unknown> },
  put: vi.fn<(s: Record<string, unknown>) => Promise<DevOpsSnapshot>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getDevOpsSettings: () => Promise.resolve(h.snap.current),
    putDevOpsSettings: (s: Record<string, unknown>) => h.put(s),
    testDevOpsSettings: () => Promise.resolve({ ok: true, projectCount: 1 }),
    resolveDevOpsDryRun: () => Promise.resolve(null),
    getSchemaCatalog: () => Promise.resolve({ tables: 0, columns: 0, importedAt: 0, snapshotSql: {} }),
  },
}));

import { DevOpsTab } from './DevOpsTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const BASE = {
  baseUrl: 'https://devops.example.invalid/tfs', collection: 'DefaultCollection',
  hasPat: true, pat: 'LEAKED-SECRET', flavor: 'auto', effectiveLookupLimit: 6,
};

let host: HTMLDivElement | null = null; let root: Root | null = null;
async function render(): Promise<HTMLElement> {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<ConfirmGate><DevOpsTab /></ConfirmGate>); });
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
  await act(async () => { pw(el).form!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  expect(h.put).toHaveBeenCalledTimes(1);
  return h.put.mock.calls[0][0];
}

describe('DevOpsTab — PAT secret sözleşmesi', () => {
  it('saklı PAT yankılanmaz; "· kayıtlı" + saklı yer tutucu, etiket bağlı', async () => {
    h.snap.current = { ...BASE };
    const el = await render();
    expect(pw(el).value).toBe('');
    expect(el.innerHTML).not.toContain('LEAKED-SECRET');
    expect(pw(el).placeholder).toBe('(boş bırak = saklı değer korunur)');
    expect(el.textContent).toContain('· kayıtlı');
    expect(pw(el).labels?.[0]?.textContent?.startsWith('Personal Access Token (PAT)')).toBe(true);
  });

  it('saklı değilse "· kayıtlı" yok, yer tutucu "PAT yapıştır…"', async () => {
    h.snap.current = { ...BASE, hasPat: false, pat: undefined };
    const el = await render();
    expect(pw(el).placeholder).toBe('PAT yapıştır…');
    expect(el.textContent).not.toContain('· kayıtlı');
  });

  it('dokunulmamış PAT gövdeye girmez', async () => {
    h.snap.current = { ...BASE };
    h.put.mockResolvedValue({ ...BASE, hasPat: true } as unknown as DevOpsSnapshot);
    const el = await render();
    const body = await save(el);
    expect(body).not.toHaveProperty('pat');
    expect(body.baseUrl).toBe(BASE.baseUrl);
  });

  it('yazılan PAT aynen gider; kayıttan sonra alan yeniden boş', async () => {
    h.snap.current = { ...BASE, hasPat: false, pat: undefined };
    h.put.mockResolvedValue({ ...BASE, hasPat: true } as unknown as DevOpsSnapshot);
    const el = await render();
    await setValue(pw(el), '  pat with spaces ');
    const body = await save(el);
    expect(body.pat).toBe('  pat with spaces ');
    expect(pw(el).value).toBe('');
    expect(el.textContent).toContain('· kayıtlı');
  });
});
