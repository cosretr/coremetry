// @vitest-environment jsdom
//
// ProblemDetail.oracleLabel — v0.10.1108 (operatör: "Oracleden gelen
// problemlerde exceptionsta Oracle yazıyor onun yerine başka bir şey yazsa.
// Teknik Hata gibi mesela").
//
// NE ÇİVİLİYOR: Oracle hata tablosu grubunun (`ora:`) detayında
//   • üst şerit rozeti varsayılan "Teknik hata" (title Oracle'ı açıklamaya
//     devam eder), detay kartı başlığı "Teknik hata grubu";
//   • branding oracleGroupLabel doluysa ikisi de o etiketi taşır;
//   • span grubunda rozet yok.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExceptionGroup } from '@/lib/types';
import { invalidateBranding } from '@/lib/branding';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionSamples: async () => ({ samples: [] }),
    servicesMetadata: async () => ({}),
    problemVerdicts: async () => ({ verdicts: [] }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('./ExceptionPodsPanel', () => ({ ExceptionPodsPanel: () => null }));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));

import { ProblemDetail } from './ProblemDetail';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const ora: ExceptionGroup = {
  fingerprint: 'ora:aaaaaaaaaaaaaaaa', type: 'APP_ERR_001', message: 'OP_TRANSFER', service: 'oracle:app-err',
  priority: 'P2', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9,
  occurrences: 4200, notes: '',
  oracle: { sourceName: 'app-err', code: 'APP_ERR_001', operation: 'OP_TRANSFER', channels: [], services: [], serviceCount: 0, known: true },
};
const span: ExceptionGroup = {
  fingerprint: '0a1b2c3d4e5f6a7b', type: 'java.net.SocketTimeoutException', message: 'Read timed out', service: 'checkout',
  priority: 'P1', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9, occurrences: 42, notes: '',
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(group: ExceptionGroup): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><ProblemDetail group={group} isAdmin={false} onBack={() => {}} onChanged={() => {}} /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await act(async () => { await new Promise(r => setTimeout(r, 30)); });
  return host;
}

const barBadges = (el: HTMLElement) => Array.from(el.querySelectorAll('.rb-bar .badge'));
const panelHeading = (el: HTMLElement) => Array.from(el.querySelectorAll('h3')).map(h => h.textContent);

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  host = null; root = null;
});

describe('ProblemDetail — Oracle grubu görünen adı (v0.10.1108)', () => {
  it('varsayılan "Teknik hata": rozet + detay kartı başlığı; title Oracle\'ı açıklar', async () => {
    const el = await mount(ora);
    const badge = barBadges(el).find(b => b.textContent === 'Teknik hata');
    expect(badge).toBeTruthy();
    expect(badge!.getAttribute('title')).toContain('Oracle hata tablosu');
    expect(barBadges(el).some(b => b.textContent === 'Oracle')).toBe(false);
    expect(panelHeading(el)).toContain('Teknik hata grubu');
  });

  it('span grubunda rozet yok', async () => {
    const el = await mount(span);
    expect(barBadges(el).some(b => b.textContent === 'Teknik hata')).toBe(false);
  });

  it('branding etiketi doluysa rozet ve başlık onu taşır', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ oracleGroupLabel: '  DB hatası ' }), { status: 200 })));
    try {
      await invalidateBranding();
      const el = await mount(ora);
      expect(barBadges(el).some(b => b.textContent === 'DB hatası')).toBe(true);
      expect(panelHeading(el)).toContain('DB hatası grubu');
    } finally {
      vi.stubGlobal('fetch', vi.fn(async () => new Response('null', { status: 200 })));
      await invalidateBranding();
      vi.unstubAllGlobals();
    }
  });
});
