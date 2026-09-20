// @vitest-environment jsdom
//
// useUrlPage.test.tsx — v0.10.827 (operatör-bildirimli, 2026-09-20).
//
// SEMPTOM: /services'in alt şeridinde "Next" ve "Last" hiçbir şey yapmıyor;
// tıktan sonra operatör hâlâ 1. sayfada.
//
// KÖK NEDEN: react-router-dom 6.30'un `setSearchParams`ı
// `useCallback(…, [navigate, searchParams])` ve `searchParams`
// `location.search`e memo'lu — yani HER URL yazımı setter'a yeni bir kimlik
// veriyor. Services o setter'ı saran `setPage`i "filtre değişince sayfayı
// sıfırla" efektinin deps'ine koymuştu (v0.9.1111): tıkın kendi URL yazımı
// efekti yeniden tetikliyor, efekt de `setPage(0)` diyordu.
//
// Bu dosya davranışı ÖLÇÜYOR (kaynak metnini değil): gerçek hook, gerçek
// `Pager` atomu, gerçek router. İki pin var —
//   1. kütüphane gerçeği: setSearchParams kimliği URL yazımında DEĞİŞİR
//      (düzeltmenin var olma sebebi; kütüphane bunu bir gün sabitlerse
//      test yeşil kalır ama gerekçe belgeli durur),
//   2. sözleşme: useUrlPage'in setter'ı kimliğini KORUR, dolayısıyla onu
//      deps'inde taşıyan bir efekt tık başına YENİDEN KOŞMAZ ve sayfa
//      gerçekten ilerler.
import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useEffect, useRef, useState } from 'react';
import { BrowserRouter, useSearchParams } from 'react-router-dom';
import { Pager } from '@/components/Pager';
import { useUrlPage } from './useUrlPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render(ui: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(ui); });
}

beforeEach(() => { window.history.replaceState(null, '', '/services'); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const btn = (label: string) =>
  [...document.querySelectorAll('button')].find(b => b.textContent?.includes(label))!;
const pageInput = () => (document.querySelector('input') as HTMLInputElement).value;
const shownPage = () => host!.querySelector('[data-testid="page"]')!.textContent;

// Kütüphane gerçeğini ölçen minik tanık.
let sawIdentities: unknown[] = [];
function SetterIdentityWitness() {
  const [, setSearchParams] = useSearchParams();
  sawIdentities.push(setSearchParams);
  return (
    <button onClick={() => setSearchParams(p => { p.set('x', String(p.getAll('x').length + 1)); return p; },
      { replace: true })}>write-url</button>
  );
}

// /services'in şerit + sıfırlama efekti anatomisi, GERÇEK hook ile.
// `resetRuns` efektin kaç kez koştuğunu sayıyor: bug'da tık başına artar.
let resetRuns = 0;
function ServicesPagerHarness() {
  const [page, setPage] = useUrlPage();
  const [committedFilter] = useState('');
  const mounted = useRef(false);
  useEffect(() => {
    if (!mounted.current) { mounted.current = true; return; }
    resetRuns++;
    setPage(0);
  }, [committedFilter, setPage]);
  return (
    <>
      <div data-testid="page">{page}</div>
      <Pager mode="offset" count="exact" total={500} page={page} pageSize={50}
        onPage={setPage} lastReachablePage={9} />
    </>
  );
}

// v0.10.831 — FONKSİYON biçimi (setPage(p => p + 1)) kendi kapısını kazandı.
//
// Yukarıdaki testler setter'ı yalnız SAYI ile sürüyordu, yani closure'dan
// hesaplayan bir uygulama (`next(page)`) da yeşil kalırdı: tek tıkta fark
// görünmez, fark ancak İKİ güncelleme AYNI React turunda birleşince ortaya
// çıkar — ikincisi bayat closure'ı okur ve bir adım KAYBOLUR. Hook'un
// yorumu bunu zaten vaat ediyor ("URL'deki DEĞERDEN hesaplıyor"); bu kapı
// vaadi ÖLÇÜYOR.
let bump: ((n: number | ((p: number) => number)) => void) | null = null;
function FunctionalUpdaterHarness() {
  const [page, setPage] = useUrlPage();
  bump = setPage;
  return <div data-testid="page">{page}</div>;
}

describe('useUrlPage — fonksiyon biçimi (v0.10.831)', () => {
  it('tek turda iki artış İKİ sayfa ilerletir (closure bayatlamaz)', () => {
    render(<BrowserRouter><FunctionalUpdaterHarness /></BrowserRouter>);
    act(() => { bump!(p => p + 1); bump!(p => p + 1); });
    expect(window.location.search).toBe('?page=2');
    expect(shownPage()).toBe('2');
  });

  it("fonksiyon biçimi 0'a inince `?page=` SİLİNİR", () => {
    window.history.replaceState(null, '', '/services?page=1&range=3h');
    render(<BrowserRouter><FunctionalUpdaterHarness /></BrowserRouter>);
    act(() => { bump!(p => p - 1); });
    expect(new URLSearchParams(window.location.search).has('page')).toBe(false);
    expect(new URLSearchParams(window.location.search).get('range')).toBe('3h');
  });
});

describe('react-router sözleşmesi (düzeltmenin gerekçesi)', () => {
  it('setSearchParams kimliği bir URL yazımından sonra DEĞİŞİR', () => {
    sawIdentities = [];
    render(<BrowserRouter><SetterIdentityWitness /></BrowserRouter>);
    const before = sawIdentities[sawIdentities.length - 1];
    act(() => { btn('write-url').click(); });
    const after = sawIdentities[sawIdentities.length - 1];
    // Bu satır yeşil olduğu sürece "setter'ı deps'e koymak" bir tuzaktır.
    expect(Object.is(before, after)).toBe(false);
  });
});

describe('useUrlPage — sayfa şeridi URL yazarken kendini sıfırlamaz (v0.10.827)', () => {
  it('Next ileri gider: ?page=1, gösterge 2, sıfırlama efekti koşmaz', () => {
    resetRuns = 0;
    render(<BrowserRouter><ServicesPagerHarness /></BrowserRouter>);
    expect(shownPage()).toBe('0');
    act(() => { btn('Next').click(); });
    // Bug'da bu üçü sırasıyla '0', '' ve 2 idi.
    expect(shownPage()).toBe('1');
    expect(window.location.search).toBe('?page=1');
    expect(pageInput()).toBe('2');
    expect(resetRuns).toBe(0);
  });

  it('Last son sayfaya gider ve orada KALIR', () => {
    resetRuns = 0;
    render(<BrowserRouter><ServicesPagerHarness /></BrowserRouter>);
    act(() => { btn('Last').click(); });
    expect(shownPage()).toBe('9');
    expect(window.location.search).toBe('?page=9');
    expect(pageInput()).toBe('10');
    expect(resetRuns).toBe(0);
  });

  it('art arda Next tıkları birikir (closure bayatlamaz)', () => {
    render(<BrowserRouter><ServicesPagerHarness /></BrowserRouter>);
    act(() => { btn('Next').click(); });
    act(() => { btn('Next').click(); });
    act(() => { btn('Next').click(); });
    expect(shownPage()).toBe('3');
    expect(window.location.search).toBe('?page=3');
  });

  it('sayfa 0 `?page=` YAZMAZ (temiz URL) ve yabancı paramları taşır', () => {
    window.history.replaceState(null, '', '/services?range=3h&cluster=prod');
    render(<BrowserRouter><ServicesPagerHarness /></BrowserRouter>);
    act(() => { btn('Next').click(); });
    expect(new URLSearchParams(window.location.search).get('page')).toBe('1');
    expect(new URLSearchParams(window.location.search).get('cluster')).toBe('prod');
    act(() => { btn('Prev').click(); });
    expect(new URLSearchParams(window.location.search).has('page')).toBe(false);
    expect(new URLSearchParams(window.location.search).get('range')).toBe('3h');
  });
});
