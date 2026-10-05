// @vitest-environment jsdom
//
// AnomaliesPage.oracleRow — v0.10.1092 (operatör: "Oracle hataları Exceptions
// gibi görünsün, hatta Exceptions altında da olabilir.")
//
// NE ÇİVİLİYOR: Exceptions listesinde Oracle hata tablosu grubu (`ora:`)
//   • aynı satır biçimi (TriageTitleCell): kalın "<kod> · <operasyon>", "Oracle"
//     rozeti, soluk "Oracle · <kaynak> · N servis";
//   • sentetik servis (`oracle:<kaynak>`) link DEĞİL; çözülmüş servis link;
//   • "Oracle" çipi kaynak varken görünür, sayıyı taşır; varsayılan istek
//     `oracle` parametresi GÖNDERMEZ (Oracle grupları dahil), çip seçilince
//     `oracle=only` gider;
//   • span grubu satırı değişmedi (rozet yok, mesaj mono).
// v0.10.1108 (operatör: "exceptionsta Oracle yazıyor onun yerine … Teknik Hata
// gibi") — rozet / soluk satır / çip görünen adı branding'den: varsayılan
// "Teknik hata", özel etiket aynen; "Oracle" kelimesi görünen metinde yok
// (title'lar Oracle'ı açıklar; aria-label ve ?oracle= değişmedi).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { ExceptionGroup } from '@/lib/types';
import { invalidateBranding } from '@/lib/branding';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], params: [] as Record<string, unknown>[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionGroups: (p: unknown) => {
      m.params.push(p as Record<string, unknown>);
      return Promise.resolve({ items: m.items, total: m.items.length, oracleCount: 2 });
    },
    listUsers: async () => [],
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    serviceNames: async () => ({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'viewer' }, loading: false }),
}));

import ProblemsPage from './AnomaliesPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const oracleGroup = (fp: string, service: string, serviceCount: number): ExceptionGroup => ({
  fingerprint: fp, type: 'APP_ERR_001', message: 'OP_TRANSFER', service,
  priority: 'P2', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9,
  occurrences: 4200, notes: '',
  oracle: {
    sourceName: 'app-err', code: 'APP_ERR_001', operation: 'OP_TRANSFER',
    channels: [{ name: 'MOB', count: 3000 }, { name: 'WEB', count: 1200 }],
    services: serviceCount ? [{ name: 'svc-payments', count: 9 }, { name: 'svc-cards', count: 2 }].slice(0, serviceCount) : [],
    serviceCount, known: true,
  },
});
const spanGroup: ExceptionGroup = {
  fingerprint: '0a1b2c3d4e5f6a7b', type: 'java.net.SocketTimeoutException', message: 'Read timed out', service: 'checkout',
  priority: 'P1', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9, occurrences: 42, notes: '',
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(url = '/problems'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><ConfirmProvider><ProblemsPage /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  host = null; root = null;
  m.items = []; m.params = [];
});

describe('Exceptions — Oracle grubu satırı', () => {
  it('başlık, rozet, soluk satır, servis linki', async () => {
    m.items = [oracleGroup('ora:aaaaaaaaaaaaaaaa', 'svc-payments', 2), oracleGroup('ora:bbbbbbbbbbbbbbbb', 'oracle:app-err', 0), spanGroup];
    const el = await mount();
    const rows = Array.from(el.querySelectorAll('tbody tr'));
    expect(rows.length).toBe(3);
    const [r1, r2, r3] = rows;
    expect(r1.querySelector('.triage-title')?.textContent).toContain('APP_ERR_001 · OP_TRANSFER');
    expect(Array.from(r1.querySelectorAll('.badge')).some(b => b.textContent === 'Teknik hata')).toBe(true);
    expect(Array.from(r1.querySelectorAll('.badge')).some(b => b.textContent === 'Oracle')).toBe(false);
    expect(r1.querySelector('.triage-sub')?.textContent).toBe('Teknik hata · app-err · 2 servis');
    expect(r1.querySelector('.triage-sub')?.classList.contains('mono')).toBe(false);
    expect(Array.from(r1.querySelectorAll('a')).some(a => a.textContent === 'svc-payments')).toBe(true);
    expect(r1.textContent).toContain('+1 servis');
    // Sentetik servis: link yok, soluk ad.
    expect(Array.from(r2.querySelectorAll('a')).some(a => a.textContent === 'oracle:app-err')).toBe(false);
    expect(r2.textContent).toContain('oracle:app-err');
    expect(r2.querySelector('.triage-sub')?.textContent).toBe('Teknik hata · app-err · servis bilinmiyor');
    // Span grubu değişmedi.
    expect(Array.from(r3.querySelectorAll('.badge')).some(b => b.textContent === 'Teknik hata')).toBe(false);
    expect(r3.querySelector('.triage-title')?.textContent).toContain('java.net.SocketTimeoutException');
    expect(r3.querySelector('.triage-sub.mono')?.textContent).toBe('Read timed out');
  });

  it('Oracle çipi: varsayılan dahil, seçilince only', async () => {
    m.items = [spanGroup];
    const el = await mount();
    expect(m.params[0]?.oracle).toBeUndefined();
    const group = el.querySelector('[role="radiogroup"][aria-label="Oracle hata grupları"]');
    expect(group).not.toBeNull();
    const radios = Array.from(group!.querySelectorAll('[role="radio"]'));
    const only = radios.find(b => b.textContent?.startsWith('Teknik hata 2'));
    expect(only).toBeTruthy();
    expect(radios.some(b => b.textContent === 'Teknik hata hariç')).toBe(true);
    expect(radios.some(b => b.textContent?.includes('Oracle'))).toBe(false);
    await act(async () => { (only as HTMLElement).click(); });
    await settle();
    expect(m.params[m.params.length - 1]?.oracle).toBe('only');
  });

  it('özel branding etiketi rozet, soluk satır ve çipte', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ oracleGroupLabel: 'DB hatası' }), { status: 200 })));
    try {
      await invalidateBranding();
      m.items = [oracleGroup('ora:aaaaaaaaaaaaaaaa', 'svc-payments', 2)];
      const el = await mount();
      const r1 = el.querySelector('tbody tr')!;
      expect(Array.from(r1.querySelectorAll('.badge')).some(b => b.textContent === 'DB hatası')).toBe(true);
      expect(r1.querySelector('.triage-sub')?.textContent).toBe('DB hatası · app-err · 2 servis');
      const group = el.querySelector('[role="radiogroup"][aria-label="Oracle hata grupları"]')!;
      expect(Array.from(group.querySelectorAll('[role="radio"]')).some(b => b.textContent === 'DB hatası hariç')).toBe(true);
    } finally {
      vi.stubGlobal('fetch', vi.fn(async () => new Response('null', { status: 200 })));
      await invalidateBranding();
      vi.unstubAllGlobals();
    }
  });
});
