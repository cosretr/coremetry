// @vitest-environment jsdom
//
// OperationsTable.tableStates.test.tsx — v0.10.967 (tablo standardı dilim 5,
// P-1, recipe P3).
//
// NE ÇİVİLİYOR: servis sayfası Operations tablosunun üç erken dönüşü
// (Spinner / normalize-boş / kısa-pencere önerisi) tablonun İÇİNDE; başlık +
// Raw⇄Normalized anahtarı her durumda üstte, sütun başlıkları da duruyor:
//   • `loading` (normalize yeniden çekimi) önceki kipin satırlarını gizler;
//   • kısa pencerede "Widen to last 1h" CTA'sı durum satırının detail
//     yuvasında; tık onWiden'ı çağırır ve düğme gidince odak <body>'ye
//     düşmez, tablonun kabına iner;
//   • süzgeç hepsini elediyse "eşleşme yok" (eskiden yalnız "All" satırı
//     kalıyordu) + Filtreleri temizle → satırlar geri, odak süzgeç kutusunda;
//   • sayaç + süzgeç kutusu yüklenirken / boşken çizilmez ("0 … names").
import { describe, it, expect, afterEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { OperationSummary } from '@/lib/types';
import { OperationsTable } from './OperationsTable';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const op = (name: string): OperationSummary => ({
  name, spanCount: 10, errorCount: 0, errorRate: 0, avgDurationMs: 5,
  p50DurationMs: 4, p95DurationMs: 8, p99DurationMs: 9, apdex: 1,
});

interface HarnessProps {
  rows?: OperationSummary[];
  preset?: string;
  withWiden?: boolean;
  normalized?: boolean;
  loading?: boolean;
  onWidened?: () => void;
}
// Kabuk: onWiden gerçekten pencereyi genişletir (Service.tsx'teki gibi
// preset değişir) — düğmenin kendini kaldırması ve odak dönüşü ölçülsün.
function Harness({ rows = [], preset = '1h', withWiden = true, normalized = false, loading = false, onWidened }: HarnessProps) {
  const [p, setP] = useState(preset);
  return (
    <OperationsTable service="checkout" rows={rows} range={{ preset: p }} preset={p}
      onWiden={withWiden ? () => { onWidened?.(); setP('1h'); } : undefined}
      normalized={normalized} onToggleNormalized={() => {}} loading={loading} />
  );
}

let host: HTMLElement | null = null;
let root: Root | null = null;
function mount(props: HarnessProps): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(<MemoryRouter><Harness {...props} /></MemoryRouter>);
  });
  return host!;
}
const stateRow = (el: HTMLElement) => el.querySelector('table tbody tr[data-dt-state]') as HTMLElement | null;
const opRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr[data-row-idx]').length;
const button = (el: ParentNode, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim().startsWith(text)) as HTMLButtonElement | undefined;
const filterInput = (el: HTMLElement) => el.querySelector('input.field') as HTMLInputElement | null;
async function type(input: HTMLInputElement, value: string) {
  await act(async () => {
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('OperationsTable — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık + kip anahtarı + sütun başlıkları durur; önceki kipin satırları gizli', () => {
    const el = mount({ rows: [op('GET /a')], loading: true });
    expect(el.textContent).toContain('⊙ Operations');
    expect(el.querySelector('[aria-label="Operation names"]')).not.toBeNull();
    expect(el.querySelectorAll('table thead th').length).toBeGreaterThan(0);
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
    expect(opRows(el)).toBe(0);
    expect(filterInput(el)).toBeNull();
  });

  it('kısa pencere boş: öneri mesajda, "Widen to last 1h" detail yuvasında; tık genişletir, odak kaba iner', async () => {
    let widened = 0;
    const el = mount({ preset: '15m', onWidened: () => { widened++; } });
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(row.textContent).toContain('Son 15m içinde checkout için trafik yok');
    const cta = row.querySelector('[data-dt-state-detail] button') as HTMLButtonElement;
    expect(cta?.textContent).toContain('Widen to last 1h');
    expect(filterInput(el)).toBeNull();
    cta.focus();
    await act(async () => { cta.click(); });
    expect(widened).toBe(1);
    // Pencere 1h oldu: öneri ve düğme gitti, sade boş metin.
    expect(stateRow(el)!.textContent).toContain('Bu pencerede operasyon görülmedi');
    expect(stateRow(el)!.querySelector('[data-dt-state-detail]')).toBeNull();
    expect(document.activeElement).toBe(el.querySelector('.table-wrap'));
  });

  it('kısa pencere ama onWiden yok: öneri metni var, düğme yok', () => {
    const el = mount({ preset: '15m', withWiden: false });
    expect(stateRow(el)!.textContent).toContain('trafik yok');
    expect(stateRow(el)!.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('normalize boş: kendi açıklaması, CTA yok', () => {
    const el = mount({ preset: '15m', normalized: true });
    const row = stateRow(el)!;
    expect(row.textContent).toContain('normalize şekil yok');
    expect(row.querySelector('[data-dt-state-detail]')).toBeNull();
  });

  it('uzun pencere boş: sade boş metin', () => {
    const el = mount({ preset: '24h' });
    expect(stateRow(el)!.textContent).toContain('Bu pencerede operasyon görülmedi');
  });

  it('süzgeç hepsini eledi: eşleşme yok (All satırı yok) + temizle satırları getirir, odak kutuda', async () => {
    const el = mount({ rows: [op('GET /a'), op('GET /b')] });
    expect(opRows(el)).toBe(2);
    expect(el.querySelector('tr.agg-row')).not.toBeNull();
    const input = filterInput(el)!;
    await type(input, 'zzz');
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(el.querySelector('tr.agg-row')).toBeNull();
    const clear = button(row, 'Filtreleri temizle')!;
    clear.focus();
    await act(async () => { clear.click(); });
    expect(stateRow(el)).toBeNull();
    expect(opRows(el)).toBe(2);
    expect(input.value).toBe('');
    expect(document.activeElement).toBe(input);
  });
});
