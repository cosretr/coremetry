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
//
// v0.10.1369 (operatör, prod: "Bir de traceId'nin yarısını yazmış, oraya onu
// kopyalayıp kendimde aratamıyorum" + "Tempo üzerinden search yapsa"):
//   • DOM'da TAM 32 hex id (link ve "Coremetry'de yok" dalı) — kısaltma CSS'te;
//   • kopyala düğmesi navigator.clipboard.writeText(TAM id) çağırır, gezinmez,
//     satıra çıkmaz;
//   • traceSource="tempo" → gerçek link + "Tempo" rozeti (açıklayıcı title).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, useEffect, type JSX } from 'react';
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
import { TRACE_MISSING_TITLE, TRACE_TEMPO_TITLE } from './sampleTrace';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T1 = '4bf92f3577b34da6a3ce929d0e0e4736';
const T2 = '5af92f3577b34da6a3ce929d0e0e4737';
const sample = (traceId: string, traceInCoremetry?: boolean, traceSource?: 'tempo'): ExceptionSample => ({
  traceId, spanId: 'b7ad6b7169203331', time: 1_700_000_000e9, message: 'ORA-00001 · OP_A',
  stacktrace: '', spanName: 'OP_A', statusMsg: '', ...(traceInCoremetry === undefined ? {} : { traceInCoremetry }),
  ...(traceSource ? { traceSource } : {}),
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
    // v0.10.1356 (R4) — satır artık role=button değil (rowClickHandlers): ARIA
    // button'ın çocukları sunumsaldır, içteki bağlantıyı gizlerdi.
    expect(el.querySelector('tr')?.getAttribute('role')).toBeNull();
    expect(el.querySelector('tr')?.hasAttribute('data-row-action')).toBe(true);
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
    // v0.10.1369 — eskiden yalnız ilk 16 karakter + "…" DOM'daydı.
    expect(tr.textContent).toContain(T1);
    expect(tr.textContent).not.toContain('…');
    const badge = tr.querySelector('.badge.b-gray');
    expect(badge?.textContent).toBe("Coremetry'de yok");
    expect(tr.querySelector(`[title="${TRACE_MISSING_TITLE}"]`)).not.toBeNull();
    await act(async () => { tr.click(); });
    expect(visits).toEqual(['/problems']);
  });
});

describe('ExceptionSampleRow — tam id + kopyala + Tempo (v0.10.1369)', () => {
  const copyBtn = (el: HTMLElement) => el.querySelector('tr button.copy-btn') as HTMLButtonElement;
  const stubClipboard = () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    return writeText;
  };

  it('linkli satır: DOM metni TAM 32 hex id (kısaltma CSS ellipsis), title = tam id', async () => {
    const el = await mount(table(sample(T1)));
    const a = el.querySelector('tr a') as HTMLAnchorElement;
    expect(a.textContent).toBe(T1);
    expect(a.getAttribute('title')).toBe(T1);
    expect(a.style.textOverflow).toBe('ellipsis');
    expect(a.style.overflow).toBe('hidden');
  });

  it('kopyala → navigator.clipboard.writeText(TAM id); gezinmez, satıra çıkmaz', async () => {
    const writeText = stubClipboard();
    for (const s of [sample(T1), sample(T2, false)]) {
      const el = await mount(table(s));
      const btn = copyBtn(el);
      expect(btn).not.toBeNull();
      await act(async () => { btn.click(); });
      expect(writeText).toHaveBeenLastCalledWith(s.traceId);
      expect(visits).toEqual(['/problems']);
      expect(outer).not.toHaveBeenCalled();
      expect(btn.getAttribute('title')).toBe('Kopyalandı');
      act(() => root?.unmount());
      host?.remove();
      visits = [];
    }
    expect(writeText).toHaveBeenCalledTimes(2);
  });

  it('traceSource=tempo → gerçek link + "Tempo" rozeti, "Coremetry\'de yok" yok', async () => {
    const el = await mount(table(sample(T1, true, 'tempo')));
    const a = el.querySelector('tr a') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe(`/trace?id=${T1}`);
    const badge = el.querySelector(`[title="${TRACE_TEMPO_TITLE}"]`);
    expect(badge?.textContent).toBe('Tempo');
    expect(TRACE_TEMPO_TITLE).toBe("Bu trace örneklemeye girmedi, Grafana Tempo'dan açılır");
    expect(el.textContent).not.toContain("Coremetry'de yok");
  });

  it('CH\'de bulunan (kaynak yok) linkte Tempo rozeti YOK', async () => {
    const el = await mount(table(sample(T1, true)));
    expect(el.querySelector(`[title="${TRACE_TEMPO_TITLE}"]`)).toBeNull();
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

  it('v0.10.1369 — TAM id\'ler DOM\'da + kopyala; Tempo\'da bulunan link + "Tempo" rozeti', async () => {
    const T3 = '6cf92f3577b34da6a3ce929d0e0e4738';
    const el = await mount(<OracleGroupPanel group={group} samples={[sample(T1, false), sample(T3, true, 'tempo')]} />);
    const text = el.textContent ?? '';
    expect(text).toContain(T1);
    expect(text).toContain(T3);
    expect(text).not.toContain(`${T1.slice(0, 16)}…`); // panelin "…" yükleniyor yer tutucuları ayrı
    const links = Array.from(el.querySelectorAll('a')).filter(a => a.getAttribute('href')?.startsWith('/trace?'));
    expect(links.map(a => a.getAttribute('href'))).toEqual([`/trace?id=${T3}`]);
    expect(links[0].getAttribute('title')).toContain(T3);
    expect(el.querySelector(`[title="${TRACE_TEMPO_TITLE}"]`)?.textContent).toBe('Tempo');
    expect(el.querySelectorAll('button.copy-btn').length).toBe(2);
  });
});
