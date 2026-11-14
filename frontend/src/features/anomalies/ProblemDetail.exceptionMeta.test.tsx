// @vitest-environment jsdom
//
// ProblemDetail.exceptionMeta — v0.10.2129 (Problems sadeleştirme S9;
// operatör kararı 2026-11-13: exception detayı KLASİK kalır, yalnız üstüne
// alarm/anomali detayıyla aynı tek satır üst veri gelir).
//
// NE ÇİVİLİYOR: şeridin hemen altında Servis · Ekip · SRE · Tür · Ne zamandır ·
// İlgilenen; ekip Problems süzgecine gider; sentetik Oracle grubunda servis
// linki ve ekip yok; klasik düzen (tip başlığı meta satırının altında) yerinde.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExceptionGroup } from '@/lib/types';

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
    servicesMetadata: async () => ({ checkout: { ownerTeam: 'payments', sreTeam: 'sre-core' } }),
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

const metaKeys = (el: HTMLElement) => [...el.querySelectorAll('.pd-meta dt')].map(d => d.textContent);
const metaVal = (el: HTMLElement, k: string) => el.querySelector<HTMLElement>(`.pd-meta [data-meta="${k}"] dd`);

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  host = null; root = null;
});

describe('ProblemDetail (exception) — üst veri satırı (v0.10.2129, S9)', () => {
  it('span grubu: altı öğe, şeridin hemen altında; ekip süzgece gider; klasik tip başlığı altında', async () => {
    const el = await mount({ ...span, assignee: 'ops@acme.example' });
    expect(metaKeys(el)).toEqual(['Servis', 'Ekip', 'SRE', 'Tür', 'Ne zamandır', 'İlgilenen']);
    const meta = el.querySelector('.pd-meta')!;
    expect(el.querySelector('.rb-bar')!.nextElementSibling).toBe(meta);
    expect(metaVal(el, 'Servis')?.querySelector('a')).not.toBeNull();
    expect(metaVal(el, 'Ekip')?.querySelector('a')?.getAttribute('href')).toBe('/inbox?team=payments');
    expect(metaVal(el, 'Tür')?.textContent).toBe('Exception');
    expect(metaVal(el, 'Ne zamandır')?.textContent).toMatch(/gündür açık · ilk /);
    expect(metaVal(el, 'İlgilenen')?.textContent).toBe('ops@acme.example');
    expect((meta.nextElementSibling as HTMLElement).textContent).toBe('java.net.SocketTimeoutException');
  });
  it('sentetik Oracle grubu: servis düz metin, ekip yok, tür etiketli; atanmamış = kimse', async () => {
    const el = await mount(ora);
    expect(metaKeys(el)).toEqual(['Servis', 'Tür', 'Ne zamandır', 'İlgilenen']);
    expect(metaVal(el, 'Servis')?.querySelector('a')).toBeNull();
    expect(metaVal(el, 'Tür')?.textContent).toBe('Exception · Teknik hata');
    expect(metaVal(el, 'İlgilenen')?.textContent).toBe('kimse');
  });
  it('çözülmüş grup "çözüldü" der', async () => {
    const el = await mount({ ...span, state: 'resolved', resolvedAt: 1_700_000_900e9 });
    expect(metaVal(el, 'Ne zamandır')?.textContent).toMatch(/^çözüldü · /);
  });
});
