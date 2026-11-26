// @vitest-environment jsdom
//
// v0.10.2476 — pin: Anomalies › "Yük ve yapı" sekmesi (?view=load, lib/urlSchema).
// Sekme herkese görünür; seçiliyken liste view=load ile okunur, açıklama ve
// boş metin o görünümün; sekme değişince ?view= ve iki tablonun sayfası
// (page / donePage) URL'den düşer. Elenenler yalnız admin. Servis adları SENTETİK.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router';
import type { AnomalyView, UnifiedAnomaliesPage } from '@/lib/types';

const m = vi.hoisted(() => ({ role: 'viewer' as 'viewer' | 'admin', views: [] as (AnomalyView | undefined)[] }));
vi.mock('@/lib/i18n', async (orig) => {
  const mod = await orig<typeof import('@/lib/i18n')>();
  return { ...mod, useLang: () => 'tr', useT: () => (k: string) => mod.t(k, 'tr') };
});
vi.mock('@/lib/queries', () => ({
  useUnifiedAnomalies: (p: { view?: AnomalyView }) => {
    m.views.push(p.view);
    const data: UnifiedAnomaliesPage = { items: [], total: 0, openTotal: 0, doneTotal: 0, offset: 0, limit: 50, truncated: false };
    return { data, isLoading: false, isError: false };
  },
  useMLScorecard: () => ({ data: undefined }),
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/SavedViewsBar', () => ({ SavedViewsBar: () => null }));
vi.mock('@/components/coremetry/anomalies/MLTab', () => ({ MLTab: () => null, resolveMLSignal: () => '' }));
vi.mock('./streams', () => ({ AnomalyStreams: () => null }));

import AnomalyStreamsPage from './AnomalyStreamsPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let search = '';
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.role = 'viewer'; m.views = []; });

function Probe() { search = useLocation().search; return null; }

function render(url: string): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter initialEntries={[url]}><AnomalyStreamsPage /><Probe /></MemoryRouter>); });
  return host;
}
const tabs = (el: HTMLElement) => Array.from(el.querySelectorAll('[role="tab"]'));
const tab = (el: HTMLElement, label: string) => tabs(el).find(t => t.textContent === label) as HTMLElement;
const click = (t: HTMLElement) => act(() => {
  t.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 }));
  t.click();
});

describe('Anomalies › Yük ve yapı (v0.10.2476)', () => {
  it('?view=load: sekme seçili, liste view=load okur, açıklama ve boş metin görünümün', () => {
    const el = render('/anomalies?view=load');
    expect(tab(el, 'Yük ve yapı').getAttribute('aria-selected')).toBe('true');
    expect(m.views).toContain('load');
    expect(el.querySelector('[data-testid="anomalies-view-hint"]')?.textContent).toMatch(/^Yük \(trafik düşüşü, servis sustu, log hacmi\)/);
    expect(el.textContent).toContain('Son 24 saatte yük ya da yapı değişimi yok.');
    // görüntüleyici: Elenenler sekmesi yok, Yük ve yapı var
    expect(tabs(el).map(t => t.textContent)).toEqual(['Servisler', 'Yük ve yapı', 'Telemetri hacmi']);
  });
  it('sekme değişimi URL\'e yazar, sayfa paramlarını düşürür; servis listesi view yazmaz', () => {
    m.role = 'admin';
    const el = render('/anomalies?page=2&donePage=1&signal=trace');
    expect(tabs(el).map(t => t.textContent)).toContain('Elenenler');
    expect(m.views.at(-1)).toBe('');
    // useUrlParams yazımı window.location tabanlı (BrowserRouter); MemoryRouter'da elle eşle.
    window.history.replaceState(null, '', '/anomalies?page=2&donePage=1&signal=trace');
    click(tab(el, 'Yük ve yapı'));
    expect(search).toBe('?signal=trace&view=load');
    expect(m.views.at(-1)).toBe('load');
    window.history.replaceState(null, '', `/anomalies${search}`);
    click(tab(el, 'Servisler'));
    expect(search).toBe('?signal=trace');
  });
  it('tanınmayan ?view= servis listesine düşer', () => {
    const el = render('/anomalies?view=bogus');
    expect(tab(el, 'Servisler').getAttribute('aria-selected')).toBe('true');
    expect(m.views.at(-1)).toBe('');
  });
});
