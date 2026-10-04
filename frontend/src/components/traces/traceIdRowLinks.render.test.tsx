// @vitest-environment jsdom
//
// traceIdRowLinks.render — v0.10.1105 (operatör: v0.10.1104'teki Exceptions
// düzeltmesi "diğer sayfalarda da" — trace id orta / Ctrl / ⌘ tıkla yeni
// sekmede açılsın).
//
// NE ÇİVİLİYOR (tablo güdümlü, her satır bileşeni için aynı üç iddia):
//   • satırdaki trace id GERÇEK <a href="/trace?id=…"> — tarayıcının yeni-sekme
//     davranışı ancak gerçek bir href'le çalışır (eskiden Shapes exemplar'ı düz
//     metindi, satır yalnız navigate() çağırıyordu → orta tık ölüydü);
//   • link'e düz tık BİR kez gider: tık satıra çıkmaz (stopRowClick), satırın
//     rowActivation / rowClickHandlers'ı ikinci kez itmez;
//   • Ctrl-tık / ⌘-tık / orta tık satırı AYNI sekmede gezdirmez.
// Satırın boş yerine düz tık hâlâ trace'i açar (rowActivation korunur).
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act, useEffect } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import type { RepeatedSpanRow, TraceRow } from '@/lib/types';

const T1 = '4bf92f3577b34da6a3ce929d0e0e4736';

const traceRow: TraceRow = {
  traceId: T1, rootName: 'GET /orders', serviceName: 'svc-orders',
  startTime: 1_700_000_000e9, durationMs: 42, spanCount: 7, hasError: false,
};
const repeatRow: RepeatedSpanRow = {
  traceId: T1, service: 'svc-orders', rootName: 'GET /orders',
  groupValues: ['SELECT orders'], count: 12, totalDurationMs: 30, startedAt: 1_700_000_000e9,
};

vi.mock('@/lib/api', () => ({
  api: {
    traces: () => Promise.resolve({ traces: [traceRow] }),
    attributeKeys: () => Promise.resolve([]),
  },
  isCanceled: () => false,
}));

import { ShapesView } from './ShapesView';
import { RepeatsResult } from '@/pages/explore/RepeatsResult';
import { TracesResult } from '@/pages/explore/TracesResult';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let visits: string[] = [];
// jsdom belge gezinmesini uygulamaz; değiştirici tuşlu tıkın varsayılan eylemi
// (tarayıcıda yeni sekme) yakalama evresinde yutulur. Router Link değiştiricili
// tıkta zaten gezinmez; satıra çıkıp çıkmadığı ayrıca `outer` ile sınanır.
const swallow = (e: MouseEvent) => { if (e.ctrlKey || e.metaKey) e.preventDefault(); };
// Satırın ÜSTÜNDEKİ dinleyici: link tıkı buraya ulaşıyorsa satırdan da geçmiştir.
const outer = vi.fn();
// rowClickHandlers (TracesResult) değiştiricili satır tıkında window.open çağırır.
const openSpy = vi.fn((...args: unknown[]) => { void args; return null; });

function Probe() {
  const loc = useLocation();
  useEffect(() => { visits.push(loc.pathname + loc.search); }, [loc]);
  return null;
}

async function mount(el: JSX.Element): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  document.addEventListener('click', swallow, true);
  root = createRoot(host);
  await act(async () => {
    root!.render(
      <MemoryRouter initialEntries={['/list']}>
        <Probe />
        <Routes>
          <Route path="/list" element={<div onClick={outer}>{el}</div>} />
          <Route path="/trace" element={<div data-testid="trace-page" />} />
        </Routes>
      </MemoryRouter>,
    );
  });
  // ShapesView örneklemi bir Promise'ten gelir; çözülsün.
  await act(async () => { await Promise.resolve(); });
  return host;
}

beforeEach(() => { vi.stubGlobal('open', openSpy); });
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  document.removeEventListener('click', swallow, true);
  host = null; root = null; visits = [];
  outer.mockReset();
  openSpy.mockClear();
  vi.unstubAllGlobals();
});

const SITES: { name: string; el: () => JSX.Element; href: string; blankCell: number }[] = [
  {
    name: 'ShapesView (Traces → Shapes exemplar)',
    el: () => <ShapesView range={{ preset: '1h' }} />,
    href: `/trace?id=${T1}&range=1h`,
    blankCell: 1, // Traces sayısı
  },
  {
    name: 'RepeatsResult (Explore → Repeats)',
    el: () => <RepeatsResult repeats={[repeatRow]} repeatMin={5} groupBy={['name']} />,
    href: `/trace?id=${T1}`,
    blankCell: 3,
  },
  {
    name: 'TracesResult (Explore → Traces)',
    el: () => (
      <TracesResult traces={[traceRow]} traceTotal={1} traceHasMore={false} onShowTotal={() => {}}
        extraCols={[]} setExtraCols={() => {}} />
    ),
    href: `/trace?id=${T1}`,
    blankCell: 3,
  },
];

describe.each(SITES)('$name — trace id gerçek link (v0.10.1105)', ({ el, href, blankCell }) => {
  const link = (h: HTMLElement) => h.querySelector(`tbody tr a[href^="/trace?"]`) as HTMLAnchorElement | null;

  it('trace id gerçek anchor (href=/trace?id=…); düz tık tek gezinme, satıra çıkmaz', async () => {
    const h = await mount(el());
    const a = link(h);
    expect(a).not.toBeNull();
    expect(a!.getAttribute('href')).toBe(href);
    expect(a!.textContent).toContain(T1.slice(0, 12));
    await act(async () => { a!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })); });
    expect(visits).toEqual(['/list', href]);
    expect(outer).not.toHaveBeenCalled();
  });

  it('Ctrl-tık / ⌘-tık / orta tık satırı aynı sekmede gezdirmez', async () => {
    const h = await mount(el());
    const a = link(h)!;
    await act(async () => {
      a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, ctrlKey: true }));
      a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, metaKey: true }));
      a.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, cancelable: true, button: 1 }));
    });
    expect(visits).toEqual(['/list']);
    expect(outer).not.toHaveBeenCalled();
    expect(openSpy).not.toHaveBeenCalled(); // satır yedeği ikinci sekme açmadı
  });

  it('satırın boş yerine düz tık trace\'i açar (satır etkinleştirmesi korunur)', async () => {
    const h = await mount(el());
    const cell = h.querySelectorAll('tbody tr td')[blankCell] as HTMLElement;
    await act(async () => { cell.click(); });
    expect(visits).toEqual(['/list', href]);
  });
});
