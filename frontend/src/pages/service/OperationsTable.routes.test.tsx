// @vitest-environment jsdom
//
// OperationsTable.routes.test.tsx — v0.10.1023. Operatör bildirimi:
// "Operation kısmında POST GET neden detail gözükmüyor, sonra trace'e girince
// çıkıyor." Bölünmüş çıplak fiil satırları tabloda:
//   • gösterim "GET /metrics" (Traces listesiyle aynı opDisplayName);
//   • satır bağlantısı name + http.route çipini, artık satır http.route
//     NOT EXISTS çipini taşır; bölünmemiş satır yalnız adı (bugünkü gibi);
//   • kapsamlı grafik simgesi rota ve artık satırda gizli, ham satırda var;
//   • sayaç "· HTTP verb rows split by route" der; süzgeç gösterim metnini arar;
//   • aynı gösterim metnini taşıyan iki satır (gerçek "GET /metrics" span adı +
//     bölünmüş GET /metrics) ayrı satır olarak çizilir.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { FilterExpr, OperationRow } from '@/lib/types';
import { OperationsTable } from './OperationsTable';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const row = (name: string, spanCount: number, extra: Partial<OperationRow> = {}): OperationRow => ({
  name, spanCount, errorCount: 0, errorRate: 0, avgDurationMs: 5,
  p50DurationMs: 4, p95DurationMs: 8, p99DurationMs: 9, apdex: 0, ...extra,
});

const ROWS: OperationRow[] = [
  row('GET', 40, { route: '/metrics' }),
  row('GET', 3, { route: '', splitResidual: true }),
  row('GET /metrics', 2), // gerçekten bu adla gelen span — aynı gösterim metni
  row('SELECT shop.orders', 7),
];

let host: HTMLElement | null = null;
let root: Root | null = null;
function mount(rows: OperationRow[], splitVerbs: number): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(
      <MemoryRouter>
        <OperationsTable service="payments-api" rows={rows} range={{ preset: '1h' }} preset="1h"
          normalized={false} onToggleNormalized={() => {}} splitVerbs={splitVerbs} loading={false} />
      </MemoryRouter>,
    );
  });
  return host!;
}
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

const bodyRows = (el: HTMLElement) => [...el.querySelectorAll('table tbody tr[data-row-idx]')] as HTMLTableRowElement[];
const nameLink = (tr: HTMLElement) => tr.querySelector('td a') as HTMLAnchorElement;
const chipsOf = (href: string): FilterExpr[] =>
  JSON.parse(new URL(href, 'http://x').searchParams.get('filters') ?? '[]');
const rowByText = (el: HTMLElement, text: string, title?: RegExp) =>
  bodyRows(el).filter(tr => nameLink(tr).textContent === text && (!title || title.test(nameLink(tr).title)));

describe('OperationsTable — çıplak fiil rota satırları (v0.10.1023)', () => {
  it('gösterim, çipler, kapsamlı grafik simgesi ve sayaç', () => {
    const el = mount(ROWS, 1);
    expect(bodyRows(el)).toHaveLength(4);

    const [split] = rowByText(el, 'GET /metrics', /http\.route = \/metrics/);
    expect(split).toBeDefined();
    expect(chipsOf(nameLink(split).getAttribute('href')!)).toEqual([
      { k: 'name', op: '=', v: ['GET'] },
      { k: 'http.route', op: '=', v: ['/metrics'] },
    ]);
    expect(split.querySelector('a[aria-label^="Open scoped charts"]')).toBeNull();

    const [real] = rowByText(el, 'GET /metrics', /service \+ name pre-filtered/);
    expect(real).toBeDefined();
    expect(chipsOf(nameLink(real).getAttribute('href')!)).toEqual([{ k: 'name', op: '=', v: ['GET /metrics'] }]);
    expect(real.querySelector('a[aria-label^="Open scoped charts"]')).not.toBeNull();

    const [res] = rowByText(el, 'GET');
    expect(chipsOf(nameLink(res).getAttribute('href')!)).toEqual([
      { k: 'name', op: '=', v: ['GET'] },
      { k: 'http.route', op: 'NOT EXISTS', v: [] },
    ]);
    // İnceleme R5 — artık satırda da simge YOK: kapsamlı grafikler yalnız
    // span adıyla daralır, satırın gösterdiği rotasız alt kümeyi değil tüm
    // GET'leri çizerdi. (Önceki pin simgeyi burada VAR sayıyordu.)
    expect(res.querySelector('a[aria-label^="Open scoped charts"]')).toBeNull();

    expect(el.textContent).toContain('4 operations in payments-api · HTTP verb rows split by route');
    expect(el.textContent).not.toContain('distinct span name');
  });

  it('bölme yoksa sayaç bugünkü metin', () => {
    const el = mount([row('GET', 5), row('POST', 2)], 0);
    expect(el.textContent).toContain('2 distinct span names in payments-api');
    expect(chipsOf(nameLink(bodyRows(el)[0]).getAttribute('href')!)).toHaveLength(1);
  });

  it('süzgeç gösterim metnini arar ("metrics" iki satırı bulur)', async () => {
    const el = mount(ROWS, 1);
    const input = el.querySelector('input.field') as HTMLInputElement;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      set.call(input, 'metrics');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(bodyRows(el).map(tr => nameLink(tr).textContent)).toEqual(['GET /metrics', 'GET /metrics']);
  });

  // İnceleme R6 — ham "GET" satırında açılan detay paneli, rota yanıtı gelip
  // satır bölündüğünde rotasız ARTIK satıra yapışmaz (ayrı span kümesi);
  // panel kapanır. Önceki anahtar ikisine aynı kimliği veriyordu.
  it('ham → bölünmüş geçişinde açık panel artık satıra yapışmaz', async () => {
    const el = mount([row('GET', 43), row('SELECT shop.orders', 7)], 0);
    const [bare] = rowByText(el, 'GET');
    await act(async () => { (bare.querySelector('td:nth-child(2) button') as HTMLButtonElement).click(); });
    expect([...el.querySelectorAll('a')].some(a => a.textContent?.includes('View traces'))).toBe(true);
    act(() => {
      root!.render(
        <MemoryRouter>
          <OperationsTable service="payments-api" rows={ROWS} range={{ preset: '1h' }} preset="1h"
            normalized={false} onToggleNormalized={() => {}} splitVerbs={1} loading={false} />
        </MemoryRouter>,
      );
    });
    expect(bodyRows(el)).toHaveLength(4);
    expect([...el.querySelectorAll('a')].some(a => a.textContent?.includes('View traces'))).toBe(false);
  });

  it('detay paneli satırın kendi kimliğiyle açılır ve "View traces" rota çipini taşır', async () => {
    const el = mount(ROWS, 1);
    const [split] = rowByText(el, 'GET /metrics', /http\.route = \/metrics/);
    const trendBtn = split.querySelector('td:nth-child(2) button') as HTMLButtonElement;
    await act(async () => { trendBtn.click(); });
    const panelLinks = [...el.querySelectorAll('a')].filter(a => a.textContent?.includes('View traces'));
    expect(panelLinks).toHaveLength(1);
    expect(chipsOf(panelLinks[0].getAttribute('href')!)).toEqual([
      { k: 'name', op: '=', v: ['GET'] },
      { k: 'http.route', op: '=', v: ['/metrics'] },
    ]);
    const explore = [...el.querySelectorAll('a')].find(a => a.textContent?.includes('Calls →'))!;
    expect(chipsOf(explore.getAttribute('href')!)).toEqual([
      { k: 'service.name', op: '=', v: ['payments-api'] },
      { k: 'name', op: '=', v: ['GET'] },
      { k: 'http.route', op: '=', v: ['/metrics'] },
    ]);
  });
});
