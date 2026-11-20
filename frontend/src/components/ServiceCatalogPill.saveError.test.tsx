// @vitest-environment jsdom
//
// ServiceCatalogPill.saveError.test.tsx — v0.10.1341: katalog editörünün
// Save'i sunucu reddini (400 "customLinks[2].url: …") SATIR İÇİ gösterir ve
// editör açık kalır. Eskiden submit try/finally'di, catch yoktu → işlenmemiş
// ret, sessiz form.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';

const m = vi.hoisted(() => ({ fail: true, puts: 0 }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceMetadata: () => Promise.resolve({ service: 'payments', ownerTeam: 'pay' }),
      putServiceMetadata: () => {
        m.puts++;
        return m.fail
          ? Promise.reject(new Error('HTTP 400: {"error":"customLinks[2].url: must be an absolute http(s) URL or an app path such as \\"/runbook?id=…\\""}\n'))
          : Promise.resolve();
      },
    },
  };
});
const users = vi.hoisted(() => ({ editor: { user: { username: 'op', role: 'editor' }, loading: false } }));
vi.mock('@/components/AuthProvider', () => ({ useAuth: () => users.editor }));

import { ServiceCatalogPill } from './ServiceCatalogPill';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(<MemoryRouter><ServiceCatalogPill service="payments" /></MemoryRouter>);
  });
  await wait();
  return host!;
}
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim().startsWith(text)) as HTMLButtonElement | undefined;
const click = async (b: HTMLElement) => { await act(async () => { b.click(); }); await wait(); };

beforeEach(() => { m.fail = true; m.puts = 0; });
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('CatalogEditor — kaydetme reddi satır içi (v0.10.1341)', () => {
  it('400 {error} → alan adlı cümle role=alert, editör açık', async () => {
    const el = await mount();
    await click(button(el, '✎')!);
    await click(button(el, 'Save')!);
    expect(m.puts).toBe(1);
    const err = el.querySelector('[role="alert"]');
    expect(err?.textContent).toBe('customLinks[2].url: must be an absolute http(s) URL or an app path such as "/runbook?id=…"');
    expect(button(el, 'Save')).toBeDefined();
  });

  it('yeniden deneme başarılıysa hata kalkar ve editör kapanır', async () => {
    const el = await mount();
    await click(button(el, '✎')!);
    await click(button(el, 'Save')!);
    expect(el.querySelector('[role="alert"]')).not.toBeNull();
    m.fail = false;
    await click(button(el, 'Save')!);
    expect(el.querySelector('[role="alert"]')).toBeNull();
    expect(button(el, 'Save')).toBeUndefined();
  });
});
