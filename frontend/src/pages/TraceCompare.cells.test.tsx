// @vitest-environment jsdom
//
// TraceCompare.cells.test.tsx — v0.10.973 (tablo standardı dilim 6, T4/T5/T6/T9/T10).
//
// "Aligned diff" hücreleri satır içi stilden kolon bayraklarına taşındı
// (ColumnDef `numeric` / `tone` / `mono` + <DataTableCell>). Bu çivi ton
// eşlemesinin eski üçlüyle AYNI kaldığını söyler: B yavaşsa `cell-err`,
// iyileşme nötr `cell-muted` (v0.10.929 K5 — yeşil YOK), eşit `cell-faint`;
// Δ ile % aynı tonu taşır; A / B süreleri `cell-muted`; eksik değer soluk
// "—" (`span.cell-empty`). Sayılar arayüz fontunda (`mono` yok), Path kimlik
// kolonu `mono` + tam değer ipucunda (T11). Tablo `.dt`, satır `.cv-row`,
// hiçbir hücrede satır içi stil yok.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SpanRow, TraceDetailResponse } from '@/lib/types';

const MS = 1e6;
const T0 = 1e18;
const span = (traceId: string, id: string, parent: string, name: string, durNs: number, startOff = 0): SpanRow => ({
  traceId, spanId: id, parentSpanId: parent, name, kind: 'server', serviceName: 'checkout', hostName: 'h',
  startTime: T0 + startOff, endTime: T0 + startOff + durNs, durationMs: durNs / MS, statusCode: 'ok', statusMessage: '',
  attributes: {}, resourceAttributes: {}, events: null, scopeName: '',
});

// A → B: root +2ms (yavaş), db.query +2ms (yavaş), cache.get −1ms (hızlı),
// auth 0 (eşit), warmup A'da 0ns (% yok, Δ var), legacy yalnız A'da,
// fresh yalnız B'de.
const TRACES: Record<string, SpanRow[]> = {
  aaa: [
    span('aaa', 'a0', '', 'GET /cart', 10 * MS),
    span('aaa', 'a1', 'a0', 'db.query', 4 * MS, 1),
    span('aaa', 'a2', 'a0', 'cache.get', 2 * MS, 2),
    span('aaa', 'a3', 'a0', 'auth', 3 * MS, 3),
    span('aaa', 'a4', 'a0', 'warmup', 0, 4),
    span('aaa', 'a5', 'a0', 'legacy', 1 * MS, 5),
  ],
  bbb: [
    span('bbb', 'b0', '', 'GET /cart', 12 * MS),
    span('bbb', 'b1', 'b0', 'db.query', 6 * MS, 1),
    span('bbb', 'b2', 'b0', 'cache.get', 1 * MS, 2),
    span('bbb', 'b3', 'b0', 'auth', 3 * MS, 3),
    span('bbb', 'b4', 'b0', 'warmup', 2 * MS, 4),
    span('bbb', 'b6', 'b0', 'fresh', 1 * MS, 6),
  ],
};

vi.mock('@/lib/api', () => ({
  api: {
    trace: (id: string): Promise<TraceDetailResponse> => Promise.resolve({ traceId: id, spans: TRACES[id] ?? [] }),
  },
}));
// Şelale bu testin konusu değil (split sekmesi); jsdom'da ağır.
vi.mock('@/components/TraceWaterfall', () => ({ TraceWaterfall: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

import TraceComparePage from './TraceCompare';

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mountDiff() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/trace/compare?a=aaa&b=bbb']}><TraceComparePage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  const tab = [...host.querySelectorAll('button[role="tab"]')].find(b => b.textContent === 'Aligned diff') as HTMLButtonElement;
  await act(async () => { tab.click(); });
  await settle();
}

beforeEach(() => {
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

const table = () => host!.querySelector('.table-wrap table') as HTMLTableElement;
const bodyRows = () => [...table().querySelectorAll('tbody tr')] as HTMLTableRowElement[];
/** Path hücresinin ipucu (tam yol etiketi) ile satırı bul; hücreler kolon sırasıyla. */
function row(leaf: string) {
  const tr = bodyRows().find(r => r.cells[2]?.title.endsWith(`checkout / ${leaf}`));
  if (!tr) throw new Error(`satır yok: ${leaf}`);
  const [delta, pct, path, a, b] = [...tr.cells];
  return { tr, delta, pct, path, a, b };
}
const cls = (td: HTMLTableCellElement) => [...td.classList].sort().join(' ');
const empty = (td: HTMLTableCellElement) => td.querySelector(':scope > span.cell-empty')?.textContent === '—';

describe('TraceCompare aligned diff hücre sözleşmesi (v0.10.973)', () => {
  it('tablo .dt, satırlar .cv-row, hiçbir hücrede ya da satırda satır içi stil yok', async () => {
    await mountDiff();
    expect(table().classList.contains('dt')).toBe(true);
    expect(table().getAttribute('style')).toBeNull();
    expect(bodyRows().length).toBe(7);
    for (const tr of bodyRows()) {
      expect(tr.className).toBe('cv-row');
      expect(tr.getAttribute('style')).toBeNull();
      for (const td of tr.cells) {
        expect(td.getAttribute('style')).toBeNull();
        // T4 — sayı hücresi arayüz fontunda; mono yalnız Path'te.
        if (td.classList.contains('num')) expect(td.classList.contains('mono')).toBe(false);
      }
    }
  });

  it('yavaş B: Δ ve % cell-err, "+" işaretli', async () => {
    await mountDiff();
    const r = row('db.query');
    expect(cls(r.delta)).toBe('cell-err num');
    expect(cls(r.pct)).toBe('cell-err num');
    expect(r.delta.textContent).toMatch(/^\+/);
    expect(r.pct.textContent).toBe('+50%');
  });

  it('hızlı B: nötr cell-muted (K5 — iyileşme renk almaz), "−" işaretli', async () => {
    await mountDiff();
    const r = row('cache.get');
    expect(cls(r.delta)).toBe('cell-muted num');
    expect(cls(r.pct)).toBe('cell-muted num');
    expect(r.delta.textContent).toMatch(/^−/);
    expect(r.pct.textContent).toBe('-50%');
    expect(host!.querySelector('.cell-ok, .b-ok')).toBeNull();
  });

  it('eşit: cell-faint', async () => {
    await mountDiff();
    const r = row('auth');
    expect(cls(r.delta)).toBe('cell-faint num');
    expect(cls(r.pct)).toBe('cell-faint num');
    expect(r.pct.textContent).toBe('+0%');
  });

  it('A süresi 0: Δ tonlu, % soluk "—" (eksik değer)', async () => {
    await mountDiff();
    const r = row('warmup');
    expect(cls(r.delta)).toBe('cell-err num');
    expect(r.delta.textContent).toMatch(/^\+/);
    expect(empty(r.pct)).toBe(true);
  });

  it('tek yanlı satırlar: Δ / % ve eksik yan soluk "—", rozet Path hücresinde', async () => {
    await mountDiff();
    const onlyA = row('legacy');
    expect(empty(onlyA.delta) && empty(onlyA.pct) && empty(onlyA.b)).toBe(true);
    expect(empty(onlyA.a)).toBe(false);
    expect(onlyA.path.querySelector('.badge.b-warn')?.textContent?.trim()).toBe('only in A');
    const onlyB = row('fresh');
    expect(empty(onlyB.a)).toBe(true);
    expect(empty(onlyB.b)).toBe(false);
    expect(onlyB.path.querySelector('.badge.b-warn')?.textContent?.trim()).toBe('only in B');
  });

  it('A / B süreleri num cell-muted; Path mono + tam yol ipucunda', async () => {
    await mountDiff();
    const r = row('db.query');
    expect(cls(r.a)).toBe('cell-muted num');
    expect(cls(r.b)).toBe('cell-muted num');
    expect(cls(r.path)).toBe('mono');
    expect(r.path.title).toBe('checkout / GET /cart › checkout / db.query');
    expect(r.path.textContent).toBe('checkout / GET /cart › checkout / db.query');
  });

  it('kimlik bağlantıları tek monospace yığınında (.mono / --font-mono)', async () => {
    await mountDiff();
    const links = [...host!.querySelectorAll('a[href*="aaa"]')] as HTMLAnchorElement[];
    // Kontrol çubuğundaki Trace A bağlantısı `.mono`.
    expect(links.some(l => l.classList.contains('mono'))).toBe(true);
  });
});
