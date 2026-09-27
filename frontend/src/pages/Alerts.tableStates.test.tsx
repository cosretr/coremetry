// @vitest-environment jsdom
//
// Alerts.tableStates.test.tsx — v0.10.967 (tablo standardı dilim 5, P-1).
//
// NE ÇİVİLİYOR: /alerts kural tablosunun dört durumu tablonun İÇİNDE,
// başlık yerinde:
//   • boş: "+ New rule" CTA'sı durum satırının detail yuvasında (silinmedi,
//     tablonun dışına da park edilmedi) ve formu açıyor; viewer'da yok;
//   • hata: sunucu metni + v0.9.858 uyarısı + aynı ↻ (refetch);
//   • yenileme düşüp önbellek eski kuralları tutuyorsa HATA görünür, bayat
//     satır kalmaz (hata ≠ boş, hata ≠ sessiz bayat liste);
//   • tür çipi hepsini elediyse "eşleşme yok" + Filtreleri temizle (= All);
//     kaynak boşsa çip seçili kalsa da boş durum.
// NEDEN GERÇEK MOUNT: kapılar tip-doğru biçimde yanlış yazılabilir; tsc ve
// eslint sessiz kalır.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { AlertRule } from '@/lib/types';

const m = vi.hoisted(() => ({
  rules: [] as unknown[],
  fail: false,
  pending: false,
  role: 'admin',
  calls: 0,
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      alertRules: () => {
        m.calls++;
        if (m.pending) return new Promise(() => {});
        if (m.fail) return Promise.reject(new Error('upstream 502'));
        return Promise.resolve(m.rules);
      },
      problemRuleCounts: () => Promise.resolve({ counts: {} }),
      savedViews: () => Promise.resolve([]),
    },
  };
});
// Kararlı kimlik: sayfanın `[user]` efekti (preset yükleme) her render'da
// yeni nesneyle sonsuz döngüye girmesin.
const users = vi.hoisted(() => ({
  admin: { user: { username: 'op', role: 'admin' }, loading: false },
  viewer: { user: { username: 'op', role: 'viewer' }, loading: false },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => (m.role === 'viewer' ? users.viewer : users.admin),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
// Kendi sorgusu olan gürültü paneli bu testin konusu değil.
vi.mock('./alerts/NoisyRulesPanel', () => ({ NoisyRulesPanel: () => null }));

import AlertsPage from './Alerts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const rule = (id: string, metric = 'error_rate'): AlertRule => ({
  id, name: `rule-${id}`, service: 'checkout', metric, comparator: '>', threshold: 5,
  windowSec: 300, severity: 'warning', enabled: true, builtIn: false, createdAt: 1,
});

let host: HTMLElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
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
const stateRow = (el: HTMLElement) => el.querySelector('table tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr:not([data-dt-state])').length;
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim().startsWith(text)) as HTMLButtonElement | undefined;
const click = async (b: HTMLElement) => { await act(async () => { b.click(); }); await wait(); };

beforeEach(() => {
  m.rules = []; m.fail = false; m.pending = false; m.role = 'admin'; m.calls = 0;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('/alerts — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: iskelet satırı tabloda, başlık yerinde', async () => {
    m.pending = true;
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('table thead th').length).toBeGreaterThan(0);
  });

  it('boş: mesaj + "+ New rule" CTA detail yuvasında, tek hücrede; tık formu açar', async () => {
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(row.textContent).toContain('Alarm kuralı yok');
    const cta = row.querySelector('[data-dt-state-detail] button') as HTMLButtonElement;
    expect(cta?.textContent).toContain('+ New rule');
    expect(button(el, 'Save rule')).toBeUndefined();
    await click(cta);
    expect(button(el, 'Save rule')).toBeDefined();
  });

  it('viewer: boş satırda CTA yok (yazma yetkisi yok)', async () => {
    m.role = 'viewer';
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    expect(stateRow(el)!.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('hata: sunucu metni + uyarı + ↻ aynı refetch', async () => {
    m.fail = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain('upstream 502');
    expect(row.textContent).toContain('boş kural kümesi değil');
    // Hata dalında boş-durum CTA'sı YOK (v0.9.858).
    expect(row.textContent).not.toContain('+ New rule');
    const before = m.calls;
    m.fail = false; m.rules = [rule('a')];
    await click(button(row, '↻ Retry')!);
    expect(m.calls).toBeGreaterThan(before);
    expect(dataRows(el)).toBe(1);
    expect(stateRow(el)).toBeNull();
  });

  it('yenileme düştü, önbellek eski kuralları tutuyor: hata görünür, bayat satır yok', async () => {
    m.rules = [rule('a'), rule('b')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['alerts', 'rules'] }); });
    await wait();
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(dataRows(el)).toBe(0);
  });

  it('tür çipi hepsini eledi: eşleşme yok + Filtreleri temizle satırları geri getirir', async () => {
    m.rules = [rule('a'), rule('b')];
    const el = await mount();
    await click(button(el, 'Watcher')!);
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(row.textContent).toContain('Watcher türünde alarm kuralı yok');
    // CTA eşleşme-yok satırında da korunur (eski Empty her iki durumda basıyordu).
    expect(row.querySelector('[data-dt-state-detail] button')?.textContent).toContain('+ New rule');
    await click(button(row, 'Filtreleri temizle')!);
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(2);
  });
});
