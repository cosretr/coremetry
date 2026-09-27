// @vitest-environment jsdom
//
// HeatmapCellExemplars.tableStates.test.tsx — v0.10.967 (tablo standardı
// dilim 5, P-1).
//
// NE ÇİVİLİYOR: heatmap hücresi exemplar modalının tablosu durumlarını
// İÇİNDE çizer, başlık yerinde:
//   • boş: örneklenmiş-heatmap açıklaması mesajda; hücrenin sunucu-tarafı
//     temsilci trace BAĞLANTISI (v0.9.393 ölü uç kapanışı) durum satırının
//     detail yuvasında — silinmedi, tablonun dışına park edilmedi;
//   • hata: eski çare metni ("süzgeçleri genişlet / aralığı kontrol et"),
//     ↻ yoktu → eklenmedi;
//   • başka bir hücrenin okuması düşerse önceki hücrenin satırları bayat
//     kalmaz, hata görünür.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { TraceRow } from '@/lib/types';

const m = vi.hoisted(() => ({
  traces: [] as unknown[],
  fail: false,
  pending: false,
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      traces: () => {
        if (m.pending) return new Promise(() => {});
        if (m.fail) return Promise.reject(new Error('boom'));
        return Promise.resolve({ traces: m.traces });
      },
    },
  };
});

import { HeatmapCellExemplars, type HeatmapCellRef } from './HeatmapCellExemplars';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const EXEMPLAR = 'abcdef0123456789abcdef0123456789';
const CELL: HeatmapCellRef = { timeNs: 1.7e18, lowDurMs: 100, highDurMs: 200, count: 42 };
const FILTERS: never[] = [];
const trace = (id: string): TraceRow => ({
  traceId: id.padEnd(32, '0'), rootName: 'GET /a', serviceName: 'checkout',
  startTime: 1.7e18, durationMs: 150, spanCount: 3, hasError: false,
});

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

function render(cell: HeatmapCellRef, exemplarTraceId?: string) {
  act(() => {
    root!.render(
      <MemoryRouter>
        <HeatmapCellExemplars cell={cell} bucketWidthNs={60e9} filters={FILTERS}
          exemplarTraceId={exemplarTraceId} onClose={() => {}} />
      </MemoryRouter>,
    );
  });
}
async function mount(exemplarTraceId?: string): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => { root = createRoot(host!); });
  render(CELL, exemplarTraceId);
  await wait();
  return host!;
}
const stateRow = (el: HTMLElement) => el.querySelector('table tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr:not([data-dt-state])').length;

beforeEach(() => { m.traces = []; m.fail = false; m.pending = false; });
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('HeatmapCellExemplars — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: iskelet satırı tabloda, başlık yerinde', async () => {
    m.pending = true;
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(el.querySelectorAll('table thead th').length).toBe(6);
  });

  it('boş + temsilci: açıklama mesajda, temsilci trace bağlantısı detail yuvasında', async () => {
    const el = await mount(EXEMPLAR);
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(row.textContent).toContain('Eşleşen trace yok');
    const link = row.querySelector('[data-dt-state-detail] a') as HTMLAnchorElement;
    expect(link).not.toBeNull();
    expect(link.getAttribute('href')).toContain(EXEMPLAR);
    expect(link.textContent).toContain(EXEMPLAR.slice(0, 16));
  });

  it('boş, temsilci yok: detail yuvası basılmaz', async () => {
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    expect(stateRow(el)!.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('hata: eski çare metni, ↻ yok', async () => {
    m.fail = true;
    const el = await mount(EXEMPLAR);
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain('süzgeçleri genişletmeyi dene');
    expect(row.querySelector('button')).toBeNull();
  });

  it('satırlar: durum satırı yok; temsilci satırı tablonun ÜSTÜNDE kalır', async () => {
    m.traces = [trace('1'), trace('2')];
    const el = await mount(EXEMPLAR);
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(2);
    expect(el.textContent).toContain('temsilci (en yavaş)');
  });

  it('başka hücrenin okuması düştü: önceki satırlar bayat kalmaz, hata görünür', async () => {
    m.traces = [trace('1')];
    const el = await mount();
    expect(dataRows(el)).toBe(1);
    m.fail = true;
    render({ ...CELL, timeNs: CELL.timeNs + 60e9 });
    await wait();
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(dataRows(el)).toBe(0);
  });
});
