// @vitest-environment jsdom
//
// v0.10.2482 — Problems › Sinyaller: ?service= listeyi sunucuda o servise
// süzer ve süzgeç görünür (adıyla, tek tıkla kaldırılır); bağlanmamış liste
// "Neden Problem değil" sütununu taşır; sekmenin üst çubuğu "Problems ·
// Sinyaller" + env seçici (bu sayfada uygulanmaz, eski Anomalies gibi);
// ?problem= / ?anomaly= detayı Sinyaller'den de açılır. Servis adları SENTETİK.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, Suspense } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router';
import type { UnifiedAnomaliesPage } from '@/lib/types';

const m = vi.hoisted(() => ({ calls: [] as { service?: string; unattached?: boolean }[], topbar: [] as Record<string, unknown>[] }));
vi.mock('@/lib/i18n', async (orig) => {
  const mod = await orig<typeof import('@/lib/i18n')>();
  return { ...mod, useLang: () => 'tr', useT: () => (k: string) => mod.t(k, 'tr') };
});
vi.mock('@/lib/queries', () => ({
  useUnifiedAnomalies: (p: { service?: string; unattached?: boolean }) => {
    m.calls.push({ service: p.service, unattached: p.unattached });
    const data: UnifiedAnomaliesPage = { items: [], total: 0, openTotal: 0, doneTotal: 0, offset: 0, limit: 50, truncated: false };
    return { data, isLoading: false, isError: false };
  },
  useMLScorecard: () => ({ data: undefined }),
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => 'viewer' }));
vi.mock('@/components/SavedViewsBar', () => ({ SavedViewsBar: () => null }));
vi.mock('@/components/coremetry/anomalies/MLTab', () => ({ MLTab: () => null, resolveMLSignal: () => '' }));
vi.mock('./streams', () => ({ AnomalyStreams: () => null }));
vi.mock('@/components/Topbar', () => ({ Topbar: (p: Record<string, unknown>) => { m.topbar.push(p); return null; } }));

import AnomalyStreamsPage from './AnomalyStreamsPage';
import { SignalsTab } from '@/pages/inbox/InboxTabs';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let search = '';
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.calls = []; m.topbar = []; });
function Probe() { search = useLocation().search; return null; }

function render(url: string, node = <AnomalyStreamsPage />): HTMLElement {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter initialEntries={[url]}><Suspense fallback={null}>{node}</Suspense><Probe /></MemoryRouter>); });
  return host;
}

describe('Sinyaller › servis süzgeci', () => {
  it('?service= sunucuya gider, görünür ve kaldırılabilir', () => {
    const el = render('/inbox?tab=signals&service=orders-api');
    expect(m.calls.every(c => c.service === 'orders-api' && c.unattached === true)).toBe(true);
    const f = el.querySelector('[data-testid="signals-service-filter"]')!;
    expect(f.textContent).toContain('Servis: orders-api');
    act(() => { (f.querySelector('button') as HTMLButtonElement).click(); });
    expect(search).not.toContain('service=');
    expect(search).toContain('tab=signals');
  });
  it('servis yokken süzgeç satırı yok, liste tüm servisler', () => {
    const el = render('/inbox?tab=signals');
    expect(el.querySelector('[data-testid="signals-service-filter"]')).toBeNull();
    expect(m.calls.every(c => !c.service)).toBe(true);
  });
  it('bağlanmamış listede "Neden Problem değil" sütunu', () => {
    const el = render('/inbox?tab=signals');
    expect(el.textContent).toContain('Neden Problem değil');
  });
});

describe('Sinyaller sekmesi üst çubuğu ve detay bağlantıları', () => {
  it('başlık "Problems · Sinyaller", env seçici görünür ama uygulanmaz (eski Anomalies gibi; zaman seçici yok)', () => {
    render('/inbox?tab=signals', <SignalsTab strip={null} />);
    expect(m.topbar[0]).toMatchObject({ title: 'Problems · Sinyaller', showEnv: true, envApplies: false });
    expect(m.topbar[0].range).toBeUndefined();
  });
  it('?problem= / ?anomaly= iki sekmeden de açılır (detay sahibi Problemler)', () => {
    const src = readFileSync(resolve(__dirname, '../../pages/Inbox.tsx'), 'utf8');
    expect(src).toContain('const detail = readInboxDetail(sp) != null;');
    expect(src).toContain("tab === 'signals' && !detail ? <SignalsTab strip={strip} /> : <ProblemsTab tabStrip={strip} />");
  });
});
