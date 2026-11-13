// @vitest-environment jsdom
//
// Traces.characterization.test.tsx — v0.10.2084 (Traces shadcn geçişi, dilim S0;
// plan docs/frontend/traces-shadcn-plan.md §3 "Değişmeyecekler").
//
// Yaklaşım A (yerinde yeniden giydirme) davranışı taşıyan koda DOKUNMAZ; bu
// dosya o sözü BUGÜNKÜ kod üzerinde ölçer. Üretim kodu değişmedi. Sınıf
// çorbası kaydedilmez — sözleşmeler (kolon kimliği sırası, href, aria-sort,
// satır yüksekliği, hücre başına düğüm sayısı, kap seçicisi) yazılır;
// kasıtlı token değişikliği testi kırmaz, sessiz işlev kaybı kırar.
//
// GERÇEK sayfa monte edilir (yalnız `@/lib/api` sahte; Traces.tableStates
// deseni). jsdom'da yerleşim yok: `.vt-scroll`un offsetHeight'ı taklit
// edilir ki sanallaştırıcı satır ÇİZSİN (Traces.colOrder bunu yapamadığı
// için gövdeyi kaynaktan pinliyordu); clientWidth'i taklit edilir ki
// kolon sığdırmanın KABA ULAŞTIĞI ölçülsün (INCIDENTS v0.10.357: seçici
// `.table-wrap`ken sığdırma sanal tabloya hiç ulaşmamıştı, saf çekirdek
// testi yeşildi).
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { settleUntil } from '@/test/actWait';
import { ROW_H } from '@/components/ui/DataTable';
import Traces from './Traces';

const N_ROWS = 14;
const m = vi.hoisted(() => ({
  listCalls: [] as Record<string, unknown>[],
  extrasCalls: [] as Record<string, unknown>[],
  prefetch: [] as string[],
}));

vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 14 }, (_, i) => ({
    traceId: String(i + 1).padStart(32, '0'),
    serviceName: i % 3 === 0 ? 'com.acme.payments.settlement-reconciliation-worker' : 'orders-api',
    rootName: `GET /api/v1/orders/{id}/line-items/${i}`,
    startTime: 1.7e18 - i * 1e9, durationMs: 12 + i, spanCount: 4 + i,
    hasError: i === 2, errorSpans: i === 2 ? 1 : undefined,
  }));
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: (p: unknown) => { m.listCalls.push(p as Record<string, unknown>); return Promise.resolve({ traces, hasMore: true }); },
    tracesExtras: (p: unknown) => {
      m.extrasCalls.push(p as Record<string, unknown>);
      const extras: Record<string, Record<string, string>> = {};
      for (const t of traces) extras[t.traceId] = { 'http.route': `/srv/${t.traceId.slice(-2)}`, 'deployment.environment': 'prod' };
      return Promise.resolve({ extras });
    },
    trace: (id: unknown) => { m.prefetch.push(String(id)); return Promise.resolve({ spans: [] }); },
    spanMetricBatch: () => Promise.resolve({ series: { count: [], errors: [], rt: [] } }),
    attributeKeys: () => Promise.resolve([{ key: 'http.route' }, { key: 'deployment.environment' }]),
    tracesCount: () => Promise.resolve({ value: 14, atLeast: false }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    operationNames: () => Promise.resolve({ names: [], total: 0 }),
    getPreference: () => Promise.resolve(null),
    putPreference: () => Promise.resolve({}),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

// Yerleşim taklidi: yalnız `.vt-scroll` (Traces'in liste kabı) boyut taşır.
let vtWidth = 4000;
const saved: { oh?: PropertyDescriptor; ow?: PropertyDescriptor; cw?: PropertyDescriptor; ro?: unknown } = {};
const isVt = (el: HTMLElement) => el.classList?.contains('vt-scroll');

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mountTraces(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider>
          {/* /trace ayrı rota: Enter gezinmesi sayfayı söker (yoksa Traces'in URL yazıcısı yolu geri yazar). */}
          <Routes>
            <Route path="/traces" element={<Traces />} />
            <Route path="/trace" element={<div data-trace-page />} />
          </Routes>
        </ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settleUntil(() => qc.isFetching() === 0, 'trace sorguları yerleşti');
  await settle();
}

beforeEach(() => {
  m.listCalls = []; m.extrasCalls = []; m.prefetch = [];
  vtWidth = 4000;
  saved.oh = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight');
  saved.ow = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');
  saved.cw = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth');
  saved.ro = (globalThis as { ResizeObserver?: unknown }).ResizeObserver;
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true, get(this: HTMLElement) { return isVt(this) ? 4000 : 0; },
  });
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
    configurable: true, get(this: HTMLElement) { return isVt(this) ? vtWidth : 0; },
  });
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true, get(this: HTMLElement) { return isVt(this) ? vtWidth : 0; },
  });
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, String(v)); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(), key: () => null, get length() { return store.size; },
  });
  // İki ek kolon (DEFAULT_TRACE_COLUMNS üç; mockup ile aynı iki kolon).
  localStorage.setItem('traces-extra-cols', JSON.stringify(['http.route', 'deployment.environment']));
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
  for (const [k, d] of [['offsetHeight', saved.oh], ['offsetWidth', saved.ow], ['clientWidth', saved.cw]] as const) {
    if (d) Object.defineProperty(HTMLElement.prototype, k, d);
    else delete (HTMLElement.prototype as unknown as Record<string, unknown>)[k];
  }
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = saved.ro;
});

const LABEL: Record<string, string> = {
  'Start time': 'time', Service: 'service', Name: 'operation', Duration: 'duration', Status: 'status', Spans: 'spans',
};
const vt = () => host!.querySelector('.vt-scroll') as HTMLDivElement;
const listTable = () => vt().querySelector('table') as HTMLTableElement;
const headThs = () => [...listTable().querySelectorAll<HTMLTableCellElement>('thead th')];
const headIds = () => headThs().map(th => {
  const l = (th.querySelector('.tcf-th-l')?.textContent ?? th.textContent ?? '').replace(/[▲▼↕⋯]/g, '').trim();
  return LABEL[l] ?? l;
});
const bodyRows = () => [...listTable().querySelectorAll<HTMLTableRowElement>('tbody tr[data-row-idx]')];
const colWidths = () => [...listTable().querySelectorAll<HTMLTableColElement>('colgroup col:not([data-dt-hidden-slot])')]
  .map(c => c.style.width);
const qs = () => new URLSearchParams(window.location.search);
// jsdom scrollIntoView taşımaz (useTableNav j/k seçimi kaydırır).
if (typeof Element.prototype.scrollIntoView !== 'function') Element.prototype.scrollIntoView = function () {};
const key = (k: string) => act(() => {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
});

describe('S0 — kolon sırası + tablo kablolaması', () => {
  it('Start time en solda · Service · Name · ek kolonlar · Duration · Status · Spans', async () => {
    await mountTraces('/traces?range=1h');
    expect(headIds()).toEqual(['time', 'service', 'operation', 'http.route', 'deployment.environment', 'duration', 'status', 'spans']);
  });

  it('useDataTable storageKey traces-list + VirtualTable kabı: tablo `.vt-scroll` İÇİNDE, sınıf yalnız vt-scroll', async () => {
    await mountTraces('/traces?range=1h');
    expect(listTable().querySelector('thead[data-table-id="traces-list"]')).not.toBeNull();
    expect(vt().className).toBe('vt-scroll');
    expect(Number(listTable().getAttribute('aria-rowcount'))).toBe(N_ROWS);
    expect(bodyRows()).toHaveLength(N_ROWS);
    for (const tr of bodyRows()) expect(tr.dataset.tableId).toBe('traces-list');
  });
});

describe('S0 — tek sayfa kaydırıcısı', () => {
  it('kap yüksekliği auto (iç dikey kaydırma yok), contain yok; Pager kabın DIŞINDA ve SONRASINDA, sticky yok', async () => {
    await mountTraces('/traces?range=1h');
    expect(vt().style.height).toBe('auto');
    expect(vt().style.contain).toBe('');
    const pager = host!.querySelector('.pager') as HTMLElement;
    expect(pager).not.toBeNull();
    expect(vt().contains(pager)).toBe(false);
    expect(vt().compareDocumentPosition(pager) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // Satır içi sticky yalnız Start time hücresinin `left` ofseti için sınıfla gelir; başka hiçbir öğe sticky değil.
    const inlineSticky = [...host!.querySelectorAll<HTMLElement>('[style]')].filter(e => e.style.position === 'sticky');
    expect(inlineSticky).toEqual([]);
  });
});

describe('S0 — kolon sığdırma Traces kabına ULAŞIR (INCIDENTS v0.10.357)', () => {
  it('dar `.vt-scroll` ölçümü colgroup genişliklerini değiştirir; geniş kapta beyan genişlikleri', async () => {
    vtWidth = 4000;
    await mountTraces('/traces?range=1h');
    const wide = colWidths();
    const wideHidden = headThs().length;
    act(() => { root?.unmount(); });
    host?.remove();
    vtWidth = 520;
    await mountTraces('/traces?range=1h');
    const narrow = colWidths();
    // Sığdırma kaba ulaşmasaydı (seçici `.vt-scroll`u kaçırsaydı) iki ölçüm aynı olurdu.
    const changed = narrow.join('|') !== wide.join('|') || headThs().length < wideHidden;
    expect(changed, `geniş=${wide.join(',')} dar=${narrow.join(',')}`).toBe(true);
    const px = narrow.map(w => parseFloat(w)).filter(n => !Number.isNaN(n));
    const fixedSum = px.reduce((a, n) => a + n, 0);
    // Ya sığdı ya da kolon gizlendi ("+N sütun") — beyan toplamı dar kabı aşmaz.
    expect(fixedSum <= 520 || headThs().length < wideHidden).toBe(true);
  });
});

describe('S0 — satır sözleşmesi (yalnız tıklanabilen yer tıklanabilir görünür)', () => {
  // Bugünkü kod: tıklanabilen şey HÜCREDEKİ linktir, satır değil.
  // useDataTable.rowProps onOpen yüzünden data-row-action=true verir ama
  // VirtualTable onu `onRowClick || getRowHref` yokken undefined ile ezer;
  // satır imleci/hover'ı a.row-link'ten gelir (orta tık yerel çalışır).
  it('satır data-row-action TAŞIMAZ; her hücre AYNI /trace href\'li tek bir a.row-link içerir', async () => {
    await mountTraces('/traces?range=1h');
    const rows = bodyRows();
    for (const tr of rows) {
      expect(tr.hasAttribute('data-row-action')).toBe(false);
      expect(tr.onclick).toBeNull();
      const tds = [...tr.querySelectorAll('td')];
      expect(tds.length).toBe(headThs().length);
      const hrefs = tds.map(td => {
        const links = td.querySelectorAll('a.row-link');
        expect(links.length).toBe(1);
        return links[0].getAttribute('href');
      });
      expect(new Set(hrefs).size).toBe(1);
      expect(hrefs[0]).toMatch(/^\/trace\?/);
    }
    // Name hücresinin linki ayrıca row-link--name.
    const nameIdx = headIds().indexOf('operation');
    expect(rows[0].querySelectorAll('td')[nameIdx].querySelector('a.row-link.row-link--name')).not.toBeNull();
  });

  it('hover prefetch: hücreye girince api.trace bir kez (aynı id ikinci kez değil)', async () => {
    await mountTraces('/traces?range=1h');
    const td = bodyRows()[0].querySelector('td')!;
    act(() => { td.dispatchEvent(new MouseEvent('mouseover', { bubbles: true, relatedTarget: document.body })); });
    act(() => { td.dispatchEvent(new MouseEvent('mouseover', { bubbles: true, relatedTarget: document.body })); });
    expect(m.prefetch).toEqual(['0'.repeat(31) + '1']);
  });

  // v0.10.2116 (operatör kararı 2026-11-13) — iddia DEĞİŞTİ: satır-içi kiosk
  // düğmesi kaldırıldı (eski iddia: Name hücresinde tek ⧉, window.open(kiosk=1)).
  // Kiosk girişi yalnız trace detayında (kioskAffordance.test.ts). Satırda hiçbir
  // düğme yok: hücre içeriği yalnız Link.
  it('satırda kiosk düğmesi YOK; Name hücresinde düğme yok', async () => {
    await mountTraces('/traces?range=1h');
    const tr = bodyRows()[0];
    expect(tr.querySelectorAll('button.row-kiosk, [data-row-kiosk]')).toHaveLength(0);
    const nameIdx = headIds().indexOf('operation');
    expect(tr.querySelectorAll('td')[nameIdx].querySelectorAll('button')).toHaveLength(0);
    expect(tr.textContent).not.toContain('⧉');
  });

  it('klavye: j satırı seçer, Enter /trace\'e gider', async () => {
    await mountTraces('/traces?range=1h');
    key('j');
    await settle();
    expect(bodyRows()[0].className).toContain('row-selected');
    key('Enter');
    await settle();
    expect(window.location.pathname).toBe('/trace');
    expect(new URLSearchParams(window.location.search).get('id')).toBe('0'.repeat(31) + '1');
    expect(document.querySelector('[data-trace-page]')).not.toBeNull();
  });

  it('renk yalnız sapan değerde: hatalı satırda ERROR yalnız Status hücresinde, satırın kendisi tonlanmaz', async () => {
    await mountTraces('/traces?range=1h');
    const tr = bodyRows()[2];
    const statusIdx = headIds().indexOf('status');
    const tds = [...tr.querySelectorAll('td')];
    expect(tds[statusIdx].textContent).toContain('ERROR');
    tds.forEach((td, i) => { if (i !== statusIdx) expect(td.textContent).not.toContain('ERROR'); });
    expect(tr.getAttribute('style') ?? '').not.toMatch(/background/);
    expect(bodyRows()[0].querySelectorAll('td')[statusIdx].textContent).not.toContain('ERROR');
  });
});

describe('S0 — sunucu sıralama + 50 satırlık sunucu sayfalama + URL', () => {
  it('ilk istek limit 50 / offset 0 / time desc; URL varsayılanları yazmaz', async () => {
    await mountTraces('/traces?range=1h');
    expect(m.listCalls.at(-1)).toMatchObject({ limit: 50, offset: 0, sort: 'time', order: 'desc' });
    for (const k of ['sort', 'order', 'page', 'rt']) expect(qs().get(k)).toBeNull();
  });

  it('Next → offset 50 + URL page=1; Duration başlığı → sunucu sort=duration + URL sort', async () => {
    await mountTraces('/traces?range=1h');
    const next = [...host!.querySelectorAll<HTMLButtonElement>('.pager button')].find(b => b.textContent?.includes('Next'))!;
    await act(async () => { next.click(); });
    await settle();
    expect(m.listCalls.at(-1)).toMatchObject({ limit: 50, offset: 50 });
    expect(qs().get('page')).toBe('1');
    const dur = headThs()[headIds().indexOf('duration')];
    const sortBtn = (dur.querySelector('.tcf-th-l') ?? dur) as HTMLElement;
    await act(async () => { sortBtn.click(); });
    await settle();
    expect(m.listCalls.at(-1)).toMatchObject({ sort: 'duration', offset: 0 });
    expect(qs().get('sort')).toBe('duration');
    expect(headThs()[headIds().indexOf('duration')].getAttribute('aria-sort')).toMatch(/ascending|descending/);
  });
});

describe('S0 — ek kolon değeri sunucudan gelir (istemci yeniden seçmez)', () => {
  it('extras isteği servis süzgecini taşır; hücre sunucunun döndürdüğü değeri basar', async () => {
    await mountTraces('/traces?range=1h&service=orders-api');
    expect(m.extrasCalls.at(-1)).toMatchObject({ service: 'orders-api' });
    const routeIdx = headIds().indexOf('http.route');
    expect(bodyRows()[0].querySelectorAll('td')[routeIdx].textContent).toContain('/srv/01');
  });
});

describe('S0 — şerit varsayılanı + kaldırılmış kontroller', () => {
  it('?rt yokken istatistik seçici p99 seçili (STRIP_STAT_DEFAULT, v0.10.1524)', async () => {
    await mountTraces('/traces?range=1h');
    const grp = host!.querySelector('[aria-label="Yanıt süresi istatistiği"]') as HTMLElement;
    expect(grp).not.toBeNull();
    const on = [...grp.querySelectorAll('button')].filter(b =>
      b.getAttribute('aria-pressed') === 'true' || b.getAttribute('aria-checked') === 'true' || b.getAttribute('data-state') === 'on');
    expect(on.map(b => b.textContent?.trim().toLowerCase())).toEqual(['p99']);
  });

  it('sayfada "veriye sığdır", Correlate, Compare ve şerit expand/shrink düğmesi YOK', async () => {
    await mountTraces('/traces?range=1h');
    const labels = [...host!.querySelectorAll('button, a')].map(e =>
      `${e.textContent ?? ''} ${e.getAttribute('aria-label') ?? ''}`.toLowerCase());
    for (const banned of ['veriye sığdır', 'fit to data', 'correlate', 'compare', 'expand', 'shrink']) {
      expect(labels.filter(l => l.includes(banned)), banned).toEqual([]);
    }
  });
});

describe('S0 — DOM yapı kaydı (sabit fikstür, ilk 3 satır; sınıf adı YOK)', () => {
  it('kolon kimliği sırası, href, aria-sort, satır yüksekliği, hücre başına düğüm sayısı', async () => {
    await mountTraces('/traces?range=1h');
    const ids = headIds();
    const record = {
      cols: ids,
      ariaSort: headThs().map(th => th.getAttribute('aria-sort')),
      rows: bodyRows().slice(0, 3).map(tr => ({
        h: tr.style.height,
        href: tr.querySelector('a.row-link')!.getAttribute('href')!.replace(/&.*$/, ''),
        nodes: [...tr.querySelectorAll('td')].map(td => td.querySelectorAll('*').length),
      })),
    };
    expect(record.cols).toEqual(['time', 'service', 'operation', 'http.route', 'deployment.environment', 'duration', 'status', 'spans']);
    expect(record.ariaSort[0]).toBe('descending');
    expect(record.ariaSort.slice(1).every(a => a === null || a === 'none')).toBe(true);
    record.rows.forEach((r, i) => {
      expect(r.h).toBe(`${ROW_H}px`);
      expect(r.href).toBe(`/trace?id=${String(i + 1).padStart(32, '0')}`);
      expect(r.nodes).toHaveLength(ids.length);
    });
    // Hücre başına düğüm sayısı tabanı (S0 ölçümü). S2–S4 bunu ARTIRMAZ;
    // azalırsa bilinçli bir sadeleşmedir ve burada düşürülür.
    expect(record.rows[0].nodes).toEqual(NODES_BASELINE);
  });
});

// S0'da ölçüldü (bu dosyanın ilk koşusu). Sıra headIds() ile aynı.
// v0.10.2116 — Name (operation) 5 → 2: satır-içi kiosk düğmesi (button + iki
// glif span'ı) operatör kararıyla kaldırıldı — bilinçli sadeleşme.
const NODES_BASELINE: number[] = [2, 2, 2, 2, 2, 5, 2, 1];
