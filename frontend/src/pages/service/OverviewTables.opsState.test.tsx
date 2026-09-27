// @vitest-environment jsdom
// OverviewTables.opsState — v0.10.973 (tablo standardı T12, tarif P6).
//
// NE ÇİVİLİYOR: Overview'un Operations kartı (OpsCard) satır yokken durumu
// tablonun İÇİNDE basar, başlık durur. Operasyonlar sayfanın bundle
// okumasından prop olarak gelir ve bundle hatası da [] bırakır; boşu
// hatadan yalnız Service.tsx ayırır → durum Service → ServiceOverview →
// OpsCard zinciriyle gelir:
//   • bundle hatası → hata satırı (eskiden yalnız başlıklı boş tablo);
//     yeniden deneme YOK — sayfanın tek denemesi üstteki QueryError'da;
//   • başarılı ama boş → "Bu pencerede operasyon yok";
//   • satır varken durum satırı yok (satırlar kazanır), satır tıkı aynen.
import { describe, it, expect, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { DATA_TABLE_STATE_TEXT } from '@/components/ui/DataTable';
import type { OperationSummary } from '@/lib/types';
import { OpsCard } from './OverviewTables';

type OpsState = Parameters<typeof OpsCard>[0]['state'];

const OP: OperationSummary = {
  name: 'GET /orders', spanCount: 1200, errorCount: 5, errorRate: 0.4,
  avgDurationMs: 20, p50DurationMs: 15, p95DurationMs: 60, p99DurationMs: 88, apdex: 0.98, sparkline: [1, 2, 3],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(operations: OperationSummary[], state?: OpsState): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root!.render(
      <MemoryRouter>
        <OpsCard service="orders" range={{ preset: '1h' }} operations={operations} state={state} />
      </MemoryRouter>,
    );
  });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});

const stateRow = (el: HTMLElement) => el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;

describe('OpsCard — durum tablonun içinde (v0.10.973)', () => {
  it('bundle hatası: tek hata satırı, başlık durur, Retry yok', () => {
    const el = mount([], { kind: 'error' });
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain(DATA_TABLE_STATE_TEXT.error);
    expect(row.textContent).not.toContain('Retry');
    expect(el.querySelector('thead')).not.toBeNull();
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    // colSpan = görünür kolonlar (Operation · Calls · Err % · P99 · Trend).
    expect(row.querySelector('td')?.getAttribute('colspan')).toBe('5');
  });

  it('başarılı ama boş: "Bu pencerede operasyon yok"', () => {
    const el = mount([], { kind: 'empty', message: 'Bu pencerede operasyon yok' });
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.textContent).toBe('Bu pencerede operasyon yok');
  });

  it('state verilmezse varsayılan "empty"', () => {
    const el = mount([]);
    expect(stateRow(el)?.dataset.dtState).toBe('empty');
    expect(stateRow(el)?.textContent).toContain(DATA_TABLE_STATE_TEXT.empty);
  });

  it('satır varken durum satırı yok (hata verilse bile satırlar kazanır)', () => {
    const el = mount([OP], { kind: 'error' });
    expect(stateRow(el)).toBeNull();
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    expect(el.querySelector('tbody')?.textContent).toContain('GET /orders');
  });
});

describe('opsState zinciri Service → ServiceOverview → OpsCard (v0.10.973)', () => {
  const read = (rel: string) => readFileSync(join(__dirname, rel), 'utf8');
  it('Service.tsx boşu bundle hatasından ayırır, onRetry eklemez; Overview kart\'a geçirir', () => {
    const service = read('../Service.tsx');
    expect(service).toContain("bundleErr ? { kind: 'error' } : { kind: 'empty', message: 'Bu pencerede operasyon yok' }");
    expect(service).toMatch(/<ServiceOverview[^>]*opsState=\{opsState\}/);
    // Sayfa düzeyindeki QueryError ve ↻'si yerinde.
    expect(service).toContain('<QueryError message={bundleErr} onRetry={() => setRetryNonce(n => n + 1)}>');
    const overview = read('Overview.tsx');
    expect(overview).toContain('<OpsCard service={service} range={range} operations={operations} state={opsState} />');
  });
});
