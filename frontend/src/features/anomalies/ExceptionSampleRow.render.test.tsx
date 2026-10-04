// @vitest-environment jsdom
//
// ExceptionSampleRow.render — v0.10.1104 (operatör: "Exceptions sayfasındaki
// traceidler mouse orta clickle yeni sekmede açmıyorum. Bu arada bazı traceidler
// de aslında coremetry üzerinde olmayabilir").
//
// NE ÇİVİLİYOR:
//   • örnek satırındaki trace id GERÇEK <a href="/trace?id=…"> — orta / Ctrl / ⌘
//     tık tarayıcının yeni-sekme davranışını alır (eskiden <span> + navigate);
//   • link'e düz tık BİR kez gider (satırın rowActivation'ı ikinci kez itmez);
//     Ctrl-tık / orta tık satırı aynı sekmede gezdirmez;
//   • satırın boş yerine düz tık hâlâ trace'i açar (rowActivation korunur);
//   • traceInCoremetry=false → anchor YOK, satır tıklanmaz, "Coremetry'de yok"
//     + açıklayıcı title; true / undefined → link;
//   • Oracle panelinin "Trace'ler" satırı da aynı kuralı izler.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, useEffect } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExceptionGroup, ExceptionSample } from '@/lib/types';

vi.mock('@/lib/api', () => ({
  api: { exceptionGroupOracle: () => Promise.resolve(null) },
  isCanceled: () => false,
}));

import { ExceptionSampleRow } from './ExceptionSampleRow';
import { OracleGroupPanel } from './OracleGroupPanel';
import { TRACE_MISSING_TITLE } from './sampleTrace';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T1 = '4bf92f3577b34da6a3ce929d0e0e4736';
const T2 = '5af92f3577b34da6a3ce929d0e0e4737';
const sample = (traceId: string, traceInCoremetry?: boolean): ExceptionSample => ({
  traceId, spanId: 'b7ad6b7169203331', time: 1_700_000_000e9, message: 'ORA-00001 · OP_A',
  stacktrace: '', spanName: 'OP_A', statusMsg: '', ...(traceInCoremetry === undefined ? {} : { traceInCoremetry }),
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let visits: string[] = [];
// jsdom belge gezinmesini uygulamaz; değiştirici tuşlu tıkın varsayılan eylemi
// (tarayıcıda yeni sekme) yakalama evresinde yutulur. Router Link değiştiricili
// tıkta zaten gezinmez; satıra çıkıp çıkmadığı (stopPropagation) yine sınanır.
const swallow = (e: MouseEvent) => { if (e.ctrlKey || e.metaKey) e.preventDefault(); };

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
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/problems']}>
          <Probe />
          <Routes>
            <Route path="/problems" element={el} />
            <Route path="/trace" element={<div data-testid="trace-page" />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  return host;
}

// Satırın ÜSTÜNDEKİ dinleyici: link tıkı buraya ulaşıyorsa satırdan da geçmiştir
// (rowActivation ikinci kez gezinirdi) — aynı act'teki iki push tek render'a
// birleştiği için konum izinden ayırt edilemez, kabarcıktan edilir.
const outer = vi.fn();
const table = (s: ExceptionSample) => (
  <div onClick={outer}><table><tbody><ExceptionSampleRow s={s} isEv={false} /></tbody></table></div>
);

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  document.removeEventListener('click', swallow, true);
  host = null; root = null; visits = [];
  outer.mockReset();
});

describe('ExceptionSampleRow — trace id gerçek link (v0.10.1104)', () => {
  it('trace id gerçek anchor (href=/trace?id=…); düz tık tek gezinme (satır ikinci kez itmez)', async () => {
    const el = await mount(table(sample(T1)));
    const a = el.querySelector('tr a') as HTMLAnchorElement;
    expect(a).not.toBeNull();
    expect(a.getAttribute('href')).toBe(`/trace?id=${T1}`);
    expect(el.querySelector('tr')?.getAttribute('role')).toBe('button');
    await act(async () => { a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })); });
    expect(visits).toEqual(['/problems', `/trace?id=${T1}`]);
    expect(outer).not.toHaveBeenCalled();
  });

  it('Ctrl-tık / ⌘-tık / orta tık satırı aynı sekmede gezdirmez (tarayıcı yeni sekmeyi açar)', async () => {
    const el = await mount(table(sample(T1)));
    const a = el.querySelector('tr a') as HTMLAnchorElement;
    await act(async () => {
      a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, ctrlKey: true }));
      a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, metaKey: true }));
      a.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, cancelable: true, button: 1 }));
    });
    expect(visits).toEqual(['/problems']);
  });

  it('satırın boş yerine düz tık trace\'i açar (rowActivation korunur)', async () => {
    const el = await mount(table(sample(T1)));
    const cell = el.querySelectorAll('tr td')[2] as HTMLElement;
    await act(async () => { cell.click(); });
    expect(visits).toEqual(['/problems', `/trace?id=${T1}`]);
    expect(outer).toHaveBeenCalledTimes(1);
  });

  it('traceInCoremetry=true → link; undefined (span grubu) → link', async () => {
    const el = await mount(table(sample(T1, true)));
    expect(el.querySelector('tr a')?.getAttribute('href')).toBe(`/trace?id=${T1}`);
    expect(el.textContent).not.toContain("Coremetry'de yok");
  });

  it('traceInCoremetry=false → anchor yok, satır tıklanmaz, "Coremetry\'de yok" işareti', async () => {
    const el = await mount(table(sample(T1, false)));
    const tr = el.querySelector('tr') as HTMLElement;
    expect(tr.querySelector('a')).toBeNull();
    expect(tr.getAttribute('role')).toBeNull();
    expect(tr.getAttribute('tabindex')).toBeNull();
    expect(tr.textContent).toContain(T1.slice(0, 16));
    const badge = tr.querySelector('.badge.b-gray');
    expect(badge?.textContent).toBe("Coremetry'de yok");
    expect(tr.querySelector(`[title="${TRACE_MISSING_TITLE}"]`)).not.toBeNull();
    await act(async () => { tr.click(); });
    expect(visits).toEqual(['/problems']);
  });
});

describe('OracleGroupPanel — Trace\'ler satırı (v0.10.1104)', () => {
  const group: ExceptionGroup = {
    fingerprint: 'ora:aaaaaaaaaaaaaaaa', type: 'ORA-00001', message: 'OP_A', service: 'svc-orders',
    priority: 'P2', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9,
    occurrences: 12, notes: '',
  };
  it('Coremetry\'de olmayan trace linksiz + işaretli; bulunan önce ve link; tekrar eden id tek çip', async () => {
    const el = await mount(<OracleGroupPanel group={group} samples={[sample(T1, false), sample(T1, false), sample(T2, true)]} />);
    const links = Array.from(el.querySelectorAll('a')).filter(a => a.getAttribute('href')?.startsWith('/trace?'));
    expect(links.map(a => a.getAttribute('href'))).toEqual([`/trace?id=${T2}`]);
    const missing = el.querySelectorAll(`[title="${TRACE_MISSING_TITLE}"]`);
    expect(missing.length).toBe(1);
    expect(missing[0].textContent).toContain("Coremetry'de yok");
    // Bulunan trace önce.
    const text = el.textContent ?? '';
    expect(text.indexOf(T2.slice(0, 16))).toBeLessThan(text.indexOf(T1.slice(0, 16)));
  });
});
