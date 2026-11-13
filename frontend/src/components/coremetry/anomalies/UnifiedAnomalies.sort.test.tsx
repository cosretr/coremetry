// @vitest-environment jsdom
//
// UnifiedAnomalies.sort — v0.10.2083 (operatör 2026-11-13: /anomalies başlık
// sıralaması geri). v0.10.2051 listeyi sunucu sayfalı yaptı ve tablo standardı
// gereği başlık tıklaması kayboldu. Sıra artık SUNUCUDA; bu dosya davranışı
// ölçer: başlık tıklaması o tablonun URL parametresini (?sort= / ?doneSort=)
// yazar, o tablonun sayfasını başa alır, sunucuya sort/dir gider; "Ne oldu"
// başlığı tıklanmaz; varsayılan sıra URL'ye yazılmaz.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import type { UnifiedAnomaliesPage } from '@/lib/types';

type Params = { section: 'open' | 'done'; offset: number; sort?: string; dir?: string };
const m = vi.hoisted(() => ({ calls: [] as Params[] }));
vi.mock('@/lib/queries', () => ({
  useUnifiedAnomalies: (p: Params) => {
    m.calls.push(p);
    const data: UnifiedAnomaliesPage = { items: [], total: 200, openTotal: 200, doneTotal: 200, offset: p.offset, limit: 50, truncated: false };
    return { data, isLoading: false, isError: false };
  },
}));

import { UnifiedAnomalies } from './UnifiedAnomalies';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<BrowserRouter><UnifiedAnomalies /></BrowserRouter>); });
}

beforeEach(() => { m.calls = []; });
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const params = () => new URLSearchParams(window.location.search);
const th = (table: string, label: string): HTMLElement => {
  const t = document.querySelector(`table[aria-label="${table}"]`);
  const h = [...(t?.querySelectorAll('th') ?? [])].find(x => x.textContent?.startsWith(label));
  if (!h) throw new Error(`başlık yok: ${table} / ${label}`);
  return h as HTMLElement;
};
const lastCall = (section: 'open' | 'done') => [...m.calls].reverse().find(c => c.section === section)!;

describe('UnifiedAnomalies sunucu sıralaması', () => {
  it('başlık tıklaması ?sort= yazar, sayfayı başa alır, sunucuya sort/dir gider', () => {
    window.history.replaceState(null, '', '/anomalies?page=2&donePage=1');
    render();
    expect(lastCall('open')).toMatchObject({ offset: 100 });
    expect(lastCall('open').sort).toBeUndefined();
    act(() => th('Süren anomaliler', 'Servis').click());
    expect(params().get('sort')).toBe('service.asc');
    expect(params().get('page')).toBeNull();
    expect(params().get('donePage')).toBe('1'); // öteki tablonun sayfası yerinde
    expect(lastCall('open')).toMatchObject({ offset: 0, sort: 'service', dir: 'asc' });
    act(() => th('Süren anomaliler', 'Servis').click());
    expect(params().get('sort')).toBe('service.desc');
    expect(th('Süren anomaliler', 'Servis').getAttribute('aria-sort')).toBe('descending');
  });

  it('bitenler tablosu kendi parametresini (?doneSort=) ve sayfasını kullanır', () => {
    window.history.replaceState(null, '', '/anomalies?page=2&donePage=3');
    render();
    act(() => th('Biten anomaliler', 'Sinyaller').click());
    expect(params().get('doneSort')).toBe('signals.asc');
    expect(params().get('donePage')).toBeNull();
    expect(params().get('page')).toBe('2');
    expect(params().get('sort')).toBeNull();
    expect(lastCall('done')).toMatchObject({ offset: 0, sort: 'signals', dir: 'asc' });
  });

  it('"Ne oldu" sıralanmaz; varsayılan sıra URL\'de yok, başlığında ok var', () => {
    window.history.replaceState(null, '', '/anomalies');
    render();
    const head = th('Süren anomaliler', 'Ne oldu');
    expect(head.getAttribute('aria-sort')).toBeNull();
    act(() => head.click());
    expect(params().get('sort')).toBeNull();
    expect(th('Süren anomaliler', 'Durum').getAttribute('aria-sort')).toBe('descending');
    // durum.desc → asc → desc (varsayılan): varsayılan URL'den silinir
    act(() => th('Süren anomaliler', 'Durum').click());
    expect(params().get('sort')).toBe('status.asc');
    act(() => th('Süren anomaliler', 'Durum').click());
    expect(params().get('sort')).toBeNull();
    expect(lastCall('open').sort).toBeUndefined();
  });

  it('bayat / tanınmayan URL sırası varsayılana iner', () => {
    window.history.replaceState(null, '', '/anomalies?sort=headline.asc');
    render();
    expect(lastCall('open').sort).toBeUndefined();
    expect(th('Süren anomaliler', 'Durum').getAttribute('aria-sort')).toBe('descending');
  });
});
