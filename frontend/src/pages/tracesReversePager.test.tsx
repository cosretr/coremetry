// @vitest-environment jsdom
//
// tracesReversePager.test.tsx — v0.10.831 (operatör-bildirimli, 2026-09-20).
//
// SEMPTOM (prod + test, iki kez bildirildi): /traces'te "Last ⇥"e basınca
// sayfa göstergesi hâlâ **1** diyor; operatör hangi sayfada olduğunu
// anlayamıyor.
//
// v0.10.827 bir kök neden düzeltti ama düzelttiği dal TEMİZ bir URL'de
// ULAŞILAMAZDI (`dt.sort.id ?? 'startTime'`; Traces her zaman null olmayan
// bir initialSort geçiyor), yani operatörün gördüğü şey DEĞİŞMEDİ. Bu dosya
// GERÇEK sayfayı (gerçek router + gerçek Pager + gerçek useDataTable + gerçek
// fetch akışı; yalnız `@/lib/api` sahte) monte edip ölçüyor — kaynak metni
// grep'lemek 827'nin dersi gereği repro SAYILMIYOR.
//
// ÖLÇÜLEN GERÇEK (fix öncesi): tık sırayı ASC'ye çeviriyor, etiket
// "Sondan sayfa"ya dönüyor ve numara kutusu **1** yazıyor. Yani şikâyet
// sıranın dönmemesi değil, GÖSTERGENİN KENDİSİ: ters kipte "1" operatöre
// "listenin başındayım" diye okunuyor.
//
// ONAYLANAN DAVRANIŞ (operatör, 2026-09-20): ters kipte numara kutusu hiç
// çizilmez; konum sondan yazılır — 1 → "Son sayfa", 2 → "Sondan 2.".
//
// İkinci ölçüm (B): bayat/elle yazılmış bir `?s_traces-list=<bilinmeyen
// kolon>` paylaşılan sıralama linkini sessizce ÖLDÜRÜYOR — tablo hiçbir
// başlıkta aktif sıralama göstermiyorken sunucu kendi sırasıyla (time desc)
// çekiyor. Doğrulama artık paylaşılan primitifte (useDataTable).
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';

// Sayım yanıtı testten testE DEĞİŞİYOR: tavanlı (prod hâli, kesin son sayfa
// TÜRETİLEMEZ) ve kesin+küçük (lastReachablePage çizilir) iki ayrı dal.
const countHolder = vi.hoisted(() => ({
  value: { value: 10000, atLeast: true } as { value: number; atLeast: boolean },
}));

// Sahte yalnız AĞ katmanı: sayfanın kendi durum/URL/efekt mekaniği gerçek.
vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 50 }, (_, i) => ({
    traceId: String(i).padStart(32, '0'), service: 'checkout', operation: 'GET /cart',
    startTime: 1.7e18, durationMs: 12, spanCount: 4, hasError: false, statusCode: 'ok',
  }));
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    // hasMore: true → "Next →" ters kipte de etkin (konum ilerleyebilsin).
    traces: () => Promise.resolve({ traces, hasMore: true }),
    // Tavanlı sayım = prod hâli: kesin son sayfa TÜRETİLEMEZ (v0.9.638),
    // dolayısıyla Pager'ın `onEnd` (ters sıra) yolu çiziliyor.
    tracesCount: () => Promise.resolve(countHolder.value),
    tracesExtras: () => Promise.resolve({ extras: {} }),
    savedViews: () => Promise.resolve([]),
    // ServicePicker 180 ms debounce sonrası konuşuyor; şekli eksik verilirse
    // (r.names undefined) bileşen TAM SIRA tıkların arasında patlıyor —
    // tek dosya koşusunda zamanlama denk gelmiyordu, tüm pakette geliyordu.
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    operationNames: () => Promise.resolve({ names: [], total: 0 }),
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
// uPlot modül yüklenirken matchMedia okuyor; jsdom'da yok.
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null;
let host: HTMLDivElement | null = null;

async function mountTraces(url: string) {
  window.history.replaceState(null, '', url);
  const Traces = (await import('./Traces')).default;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Traces /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
}

beforeEach(() => {
  countHolder.value = { value: 10000, atLeast: true };
  try { localStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const pager = () => host!.querySelector('.pager') as HTMLElement;
const pagerBtn = (text: string) =>
  [...pager().querySelectorAll('button')].find(b => b.textContent?.includes(text)) as HTMLButtonElement;
const click = async (b: HTMLButtonElement) => {
  await act(async () => { b.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
};
const activeHeader = () =>
  [...host!.querySelectorAll('th')]
    .filter(th => th.getAttribute('aria-sort') === 'ascending' || th.getAttribute('aria-sort') === 'descending')
    .map(th => `${th.textContent?.replace(/[▲▼↕]/g, '').trim()}:${th.getAttribute('aria-sort')}`);

describe('/traces "Last ⇥" — ters kipte konum SONDAN yazılır (v0.10.831)', () => {
  it('tık sonrası numara kutusu YOK; gösterge "Son sayfa"', async () => {
    await mountTraces('/traces');
    // Önce: ileri kip — numara kutusu ve "Page" etiketi.
    expect(pager().textContent).toContain('Page');
    expect(pager().querySelector('input')!.value).toBe('1');

    await click(pagerBtn('Last ⇥'));

    // Sıra gerçekten döndü (827 sonrası bu KISIM zaten doğruydu).
    expect(new URLSearchParams(window.location.search).get('order')).toBe('asc');
    // Şikâyetin kendisi: "1" yazan kutu. Ters kipte HİÇ çizilmiyor.
    expect(pager().querySelector('input')).toBeNull();
    expect(pager().textContent).toContain('Son sayfa');
    expect(pager().textContent).not.toMatch(/\bPage\b/);
  });

  it('Next konumu SONDAN ilerletir, Prev geri getirir', async () => {
    await mountTraces('/traces');
    await click(pagerBtn('Last ⇥'));
    expect(pager().textContent).toContain('Son sayfa');

    await click(pagerBtn('Next'));
    expect(new URLSearchParams(window.location.search).get('page')).toBe('1');
    expect(pager().textContent).toContain('Sondan 2.');

    await click(pagerBtn('Next'));
    expect(pager().textContent).toContain('Sondan 3.');

    await click(pagerBtn('Prev'));
    expect(pager().textContent).toContain('Sondan 2.');
    // Yön belirsiz kalmasın: ters kipte iki düğmenin de başlığı konumu tarif eder.
    expect(pagerBtn('Next').title).toContain('Sondan');
    expect(pagerBtn('Prev').title).toContain('Son sayfa');
  });

  // v0.10.831 inceleme bulgusu (3): ters kip YALNIZ doğal eksen dönünce.
  // Traces'in doğal sırası zaman/desc; süreye ya da ada göre ARTAN sıralamanın
  // "sondan N." diye bir anlamı yok — operatör Service'e A→Z tıkladığında
  // 1. sayfaya "Son sayfa" demek ve numara kutusunu kaldırmak gerilemedir.
  it('zaman DIŞI artan sıralama ters kip DEĞİL: kutu yerinde kalır', async () => {
    await mountTraces('/traces?sort=duration&order=asc');
    expect(pager().textContent).toContain('Page');
    expect(pager().querySelector('input')!.value).toBe('1');
    expect(pager().textContent).not.toContain('Son sayfa');
  });

  it('ada göre artan sıralama da ters kip değil', async () => {
    await mountTraces('/traces?sort=service&order=asc');
    expect(pager().querySelector('input')).not.toBeNull();
    expect(pager().textContent).not.toContain('Sondan');
  });

  // v0.10.831 inceleme bulgusu (4): ters kipte KESİN bir son sayfa varken bile
  // bitiş yuvası sayfa SIÇRAMASI yapmaz. Ölçülen tuzak: sıçrama sayfayı 5'e
  // götürüyor, gösterge "Sondan 6." diyor, sıra hâlâ ters ve
  // `lastReachablePage > page` yanlışa döndüğü için şeritte geri dönüş düğmesi
  // KALMIYORDU.
  it('ters kipte kesin son sayfa varken de bitiş düğmesi SIRAYI çevirir', async () => {
    countHolder.value = { value: 300, atLeast: false };   // → lastReachablePage = 5
    await mountTraces('/traces?order=asc');
    expect(pager().textContent).toContain('Son sayfa');
    // Tek bitiş düğmesi: sayısal "Last ⇥" sıçraması ters kipte çizilmez.
    const endButtons = [...pager().querySelectorAll('button')]
      .map(b => b.textContent ?? '').filter(t => t.includes('⇤ First') || t.includes('Last ⇥'));
    expect(endButtons).toEqual(['⇤ First']);

    await click(pagerBtn('⇤ First'));
    // Sıra düzeldi, sayfa başa döndü — "sondan 6." gibi bir yere düşmedi.
    expect(new URLSearchParams(window.location.search).get('order')).toBe(null);
    expect(new URLSearchParams(window.location.search).get('page')).toBe(null);
    expect(pager().textContent).toContain('Page');
  });

  it('"⇤ First" ileri kipe döner: numara kutusu geri gelir', async () => {
    await mountTraces('/traces');
    await click(pagerBtn('Last ⇥'));
    await click(pagerBtn('⇤ First'));
    expect(new URLSearchParams(window.location.search).get('order')).toBe(null);
    expect(pager().textContent).toContain('Page');
    expect(pager().querySelector('input')!.value).toBe('1');
  });
});

// (B) Bayat/elle yazılmış sıralama parametresi — v0.10.831.
//
// `?s_traces-list=<kolon>.<yön>` paylaşılan sıralama linkinin kanalı.
// parseSortParam kimliği DOĞRULAMIYORDU: bu tabloda olmayan bir kimlik
// (eski bir yapıdan kalan 'startTime' gibi) tablonun sıralama durumu olup
// çıkıyor, hiçbir başlık aktif görünmüyor ve Traces'in dt.sort → sunucu
// çevirici efekti `if (!server) return;` ile sessizce dönüyor: link sıralama
// VAAT EDİYOR, sunucu kendi varsayılanıyla çekiyor.
describe('/traces bayat ?s_traces-list — bilinmeyen kolon kimliği (v0.10.831)', () => {
  it('bilinmeyen kimlik yok sayılır; sayfa KENDİ sırasını gösterir', async () => {
    await mountTraces('/traces?s_traces-list=startTime.desc');
    expect(activeHeader()).toEqual(['Start time:descending']);
  });

  it('bilinmeyen kimlikle gelen sayfada da "Last ⇥" çalışır', async () => {
    await mountTraces('/traces?s_traces-list=startTime.desc');
    await click(pagerBtn('Last ⇥'));
    expect(new URLSearchParams(window.location.search).get('order')).toBe('asc');
    expect(pager().textContent).toContain('Son sayfa');
  });

  it('GEÇERLİ bir kimlik hâlâ kazanır (paylaşılan link bozulmadı)', async () => {
    await mountTraces('/traces?s_traces-list=duration.asc');
    expect(activeHeader()).toEqual(['Duration:ascending']);
  });
});
