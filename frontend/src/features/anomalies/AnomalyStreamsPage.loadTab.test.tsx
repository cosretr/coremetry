// @vitest-environment jsdom
//
// v0.10.2476 — pin: Anomalies › "Yük ve yapı" görünümü (?view=load, lib/urlSchema).
// v0.10.2481 — sayfa Problems › Sinyaller sekmesi oldu; görünümler çip
// (SegmentedControl): Bağlanmamış anomaliler (varsayılan) · Yük ve yapı ·
// Elenenler (yalnız admin) · Telemetri hacmi. Varsayılan görünüm listeyi
// unattached=true okur; "Problem'e bağlı olanları da göster" ?attached=1 yazar
// ve süzgeci kaldırır. Görünüm değişince ?view= ve iki tablonun sayfası
// (page / donePage) URL'den düşer. Servis adları SENTETİK.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router';
import type { AnomalyView, UnifiedAnomaliesPage } from '@/lib/types';

const m = vi.hoisted(() => ({ role: 'viewer' as 'viewer' | 'admin', calls: [] as { view?: AnomalyView; unattached?: boolean }[] }));
vi.mock('@/lib/i18n', async (orig) => {
  const mod = await orig<typeof import('@/lib/i18n')>();
  return { ...mod, useLang: () => 'tr', useT: () => (k: string) => mod.t(k, 'tr') };
});
vi.mock('@/lib/queries', () => ({
  useUnifiedAnomalies: (p: { view?: AnomalyView; unattached?: boolean }) => {
    m.calls.push({ view: p.view, unattached: p.unattached });
    const data: UnifiedAnomaliesPage = { items: [], total: 0, openTotal: 0, doneTotal: 0, offset: 0, limit: 50, truncated: false };
    return { data, isLoading: false, isError: false };
  },
  useMLScorecard: () => ({ data: undefined }),
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));
vi.mock('@/components/SavedViewsBar', () => ({ SavedViewsBar: () => null }));
vi.mock('@/components/coremetry/anomalies/MLTab', () => ({ MLTab: () => null, resolveMLSignal: () => '' }));
vi.mock('./streams', () => ({ AnomalyStreams: () => null }));

import AnomalyStreamsPage from './AnomalyStreamsPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let search = '';
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.role = 'viewer'; m.calls = []; });

function Probe() { search = useLocation().search; return null; }

function render(url: string): HTMLElement {
  // useUrlParams yazımı window.location tabanlı (BrowserRouter); MemoryRouter'da elle eşle.
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter initialEntries={[url]}><AnomalyStreamsPage /><Probe /></MemoryRouter>); });
  return host;
}
const chips = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('[role="radiogroup"][aria-label="Anomali görünümü"] [role="radio"]')) as HTMLElement[];
const chip = (el: HTMLElement, label: string) => chips(el).find(t => t.textContent === label) as HTMLElement;
const click = (t: HTMLElement) => act(() => { t.click(); });

describe('Problems › Sinyaller görünümleri (v0.10.2476 / v0.10.2481)', () => {
  it('?view=load: çip seçili, liste view=load okur, açıklama ve boş metin görünümün', () => {
    const el = render('/inbox?tab=signals&view=load');
    expect(chip(el, 'Yük ve yapı').getAttribute('aria-checked')).toBe('true');
    expect(m.calls.map(c => c.view)).toContain('load');
    expect(m.calls.filter(c => c.view === 'load').every(c => !c.unattached)).toBe(true);
    expect(el.querySelector('[data-testid="anomalies-view-hint"]')?.textContent).toMatch(/^Yük \(trafik düşüşü, servis sustu, log hacmi\)/);
    expect(el.textContent).toContain('Son 24 saatte yük ya da yapı değişimi yok.');
    // görüntüleyici: Elenenler yok
    expect(chips(el).map(t => t.textContent)).toEqual(['Bağlanmamış anomaliler', 'Yük ve yapı', 'Telemetri hacmi']);
  });
  it('varsayılan: Bağlanmamış anomaliler, yardım satırı, liste unattached okur; anahtar ?attached=1 yazar', () => {
    const el = render('/inbox?tab=signals');
    expect(chip(el, 'Bağlanmamış anomaliler').getAttribute('aria-checked')).toBe('true');
    expect(el.textContent).toContain("Bir Problem'e bağlanmamış olaylar. Rozette sayılmaz, bildirim göndermez.");
    expect(m.calls.at(-1)).toEqual({ view: '', unattached: true });
    const box = el.querySelector('[role="checkbox"]') as HTMLElement;
    expect(box.closest('[data-cm-field]')?.textContent).toContain("Problem'e bağlı olanları da göster");
    click(box);
    expect(search).toBe('?tab=signals&attached=1');
    expect(m.calls.at(-1)).toEqual({ view: '', unattached: false });
  });
  it('görünüm değişimi URL\'e yazar, sayfa paramlarını düşürür; varsayılan view yazmaz', () => {
    m.role = 'admin';
    const el = render('/inbox?tab=signals&page=2&donePage=1&signal=trace');
    expect(chips(el).map(t => t.textContent)).toEqual(['Bağlanmamış anomaliler', 'Yük ve yapı', 'Elenenler', 'Telemetri hacmi']);
    expect(m.calls.at(-1)?.view).toBe('');
    click(chip(el, 'Yük ve yapı'));
    expect(search).toBe('?tab=signals&signal=trace&view=load');
    expect(m.calls.at(-1)?.view).toBe('load');
    window.history.replaceState(null, '', `/inbox${search}`);
    click(chip(el, 'Bağlanmamış anomaliler'));
    expect(search).toBe('?tab=signals&signal=trace');
  });
  it('tanınmayan ?view= varsayılana düşer', () => {
    const el = render('/inbox?tab=signals&view=bogus');
    expect(chip(el, 'Bağlanmamış anomaliler').getAttribute('aria-checked')).toBe('true');
    expect(m.calls.at(-1)?.view).toBe('');
  });
});
