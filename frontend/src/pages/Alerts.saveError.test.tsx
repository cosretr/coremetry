// @vitest-environment jsdom
//
// Alerts.saveError.test.tsx — v0.10.1341: sunucu kuralı 400 ile reddederse
// (runbookUrl kapısı) hata kural formunun footer'ında SATIR İÇİ görünür,
// `HTTP 400:` / ham JSON gürültüsü olmadan; form açık kalır. Eskiden `save`
// reddi yakalamıyordu (işlenmemiş ret, sessiz form). Runbook alanının
// altındaki ön-kontrol ipucu da (appLinkKind) burada çivili.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { AlertRule } from '@/lib/types';

const m = vi.hoisted(() => ({
  rules: [] as unknown[],
  updateFail: true,
  updates: 0,
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      alertRules: () => Promise.resolve(m.rules),
      problemRuleCounts: () => Promise.resolve({ counts: {} }),
      savedViews: () => Promise.resolve([]),
      updateAlertRule: () => {
        m.updates++;
        return m.updateFail
          ? Promise.reject(new Error('HTTP 400: {"error":"runbookUrl must be an absolute http(s) URL or an app path such as \\"/runbook?id=…\\""}\n'))
          : Promise.resolve({});
      },
    },
  };
});
const users = vi.hoisted(() => ({
  admin: { user: { username: 'op', role: 'admin' }, loading: false },
}));
vi.mock('@/components/AuthProvider', () => ({ useAuth: () => users.admin }));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('./alerts/NoisyRulesPanel', () => ({ NoisyRulesPanel: () => null }));
vi.mock('./alerts/ConditionPreview', () => ({ ConditionPreview: () => null }));

import AlertsPage from './Alerts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const rule = (runbookUrl?: string): AlertRule => ({
  id: 'r1', name: 'rule-r1', service: 'checkout', metric: 'error_rate', comparator: '>', threshold: 5,
  windowSec: 300, severity: 'warning', enabled: true, builtIn: false, createdAt: 1, runbookUrl,
});

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/alerts']}>
          <ConfirmProvider><AlertsPage /></ConfirmProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim().startsWith(text)) as HTMLButtonElement | undefined;
const click = async (b: HTMLElement) => { await act(async () => { b.click(); }); await wait(); };
const saveErr = (el: HTMLElement) => el.querySelector('[data-rule-save-error]') as HTMLElement | null;

beforeEach(() => { m.rules = []; m.updateFail = true; m.updates = 0; });
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('/alerts — kaydetme reddi formda satır içi (v0.10.1341)', () => {
  it('400 {error} → sunucu cümlesi footer\'da, form açık kalır', async () => {
    m.rules = [rule('javascript:alert(1)')];
    const el = await mount();
    await click(button(el, 'Edit')!);
    expect(saveErr(el)).toBeNull();
    await click(button(el, 'Update rule')!);
    expect(m.updates).toBe(1);
    const err = saveErr(el);
    expect(err?.getAttribute('role')).toBe('alert');
    expect(err?.textContent).toBe('runbookUrl must be an absolute http(s) URL or an app path such as "/runbook?id=…"');
    expect(err?.textContent).not.toContain('HTTP 400');
    // Form açık: Update düğmesi hâlâ orada.
    expect(button(el, 'Update rule')).toBeDefined();
  });

  it('Cancel hatayı temizler; başarılı kayıt formu kapatır', async () => {
    m.rules = [rule('/runbook?id=abc')];
    const el = await mount();
    await click(button(el, 'Edit')!);
    await click(button(el, 'Update rule')!);
    expect(saveErr(el)).not.toBeNull();
    await click(button(el, 'Cancel')!);
    await click(button(el, 'Edit')!);
    expect(saveErr(el)).toBeNull();
    m.updateFail = false;
    await click(button(el, 'Update rule')!);
    expect(saveErr(el)).toBeNull();
    expect(button(el, 'Update rule')).toBeUndefined();
  });

  it('runbook ön-kontrol ipucu: güvensiz değerde görünür, geçerli/boşta yok', async () => {
    m.rules = [rule('//evil.com')];
    let el = await mount();
    await click(button(el, 'Edit')!);
    expect(el.querySelector('[data-runbook-hint]')?.textContent).toContain('/runbook?id=');
    act(() => root!.unmount()); host?.remove(); root = null;

    m.rules = [rule('https://wiki.example.com/rb')];
    el = await mount();
    await click(button(el, 'Edit')!);
    expect(el.querySelector('[data-runbook-hint]')).toBeNull();
  });
});
