// @vitest-environment jsdom
//
// v0.10.2473 — pin: Anomalies › Elenenler (view=held) satır başına elenme
// nedenini ("Elenme nedeni" sütunu: kalan kapı, değer, eşik) ve üstte sayacı
// gösterir; kip on = elendi (bilgi), shadow = elenecek, off = sayaç yok.
// Servis listesinde neden sütunu yoktur.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { MLGatesMode, UnifiedAnomaliesPage, UnifiedAnomalyRow } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr', gates: 'on' as MLGatesMode }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));

const NOW_NS = Date.now() * 1e6;
const row = (key: string, heldGates?: UnifiedAnomalyRow['heldGates']): UnifiedAnomalyRow => ({
  key, service: 'cart-svc', start: NOW_NS - 12 * 60e9, end: NOW_NS, signals: ['trace'], status: 'running',
  mlCount: 1, legacyCount: 0, heldGates,
});
vi.mock('@/lib/queries', () => ({
  useUnifiedAnomalies: (p: { section: 'open' | 'done' }) => {
    const items = p.section === 'open'
      ? [row('a', [{ name: 'min_volume', value: 40, threshold: 100 }]), row('b', [{ name: 'min_abs_diff', value: 4, threshold: 25 }])]
      : [];
    const data: UnifiedAnomaliesPage = {
      items, total: items.length, openTotal: items.length, doneTotal: 0, offset: 0, limit: 50, truncated: false,
      gatesMode: m.gates, held: { events: 2, byGate: { min_volume: 1, min_abs_diff: 1 } },
    };
    return { data, isLoading: false, isError: false };
  },
}));

import { UnifiedAnomalies } from './UnifiedAnomalies';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.lang = 'tr'; m.gates = 'on'; });

function render(view: '' | 'held'): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><UnifiedAnomalies view={view} hint="gölge ipucu" /></MemoryRouter>); });
  return host;
}
const q = (el: HTMLElement, id: string) => el.querySelector(`[data-testid="${id}"]`);

describe('Anomalies › Elenenler (v0.10.2473)', () => {
  it('kip on: neden sütunu + sayaç; açıklama "elendi"', () => {
    const el = render('held');
    expect(el.textContent).toContain('Elenme nedeni');
    expect(el.textContent).toContain('Az hacim: 40 çağrı (en az 100)');
    expect(el.textContent).toContain('Küçük fark: 4 ms (en az 25 ms)');
    expect(q(el, 'anomalies-held-counter')?.textContent).toBe('Son 24 saatte elenen olay: 2 (az hacim 1 · küçük mutlak fark 1)');
    expect(q(el, 'anomalies-view-hint')?.textContent).toContain('Gürültü kapıları açık');
  });
  it('EN + shadow: "would be filtered out" sayacı, gölge ipucu', () => {
    m.lang = 'en'; m.gates = 'shadow';
    const el = render('held');
    expect(el.textContent).toContain('Why filtered out');
    expect(q(el, 'anomalies-held-counter')?.textContent).toContain('Events that would be filtered out in the last 24 hours: 2');
    expect(q(el, 'anomalies-view-hint')?.textContent).toBe('gölge ipucu');
  });
  it('kip off: sayaç yok, "kapalı" açıklaması', () => {
    m.gates = 'off';
    const el = render('held');
    expect(q(el, 'anomalies-held-counter')).toBeNull();
    expect(q(el, 'anomalies-view-hint')?.textContent).toContain('kapalı');
  });
  it('servis listesi: neden sütunu ve sayaç yok', () => {
    const el = render('');
    expect(el.textContent).not.toContain('Elenme nedeni');
    expect(q(el, 'anomalies-held-counter')).toBeNull();
  });
});
