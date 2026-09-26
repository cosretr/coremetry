// @vitest-environment jsdom
//
// BulkBar.contract.test.tsx — v0.10.939 (tablo standardı T8).
//
// Küçük bir koşum tablosu useDataTable'ın `selection` seçeneğiyle kurulur;
// satır seçimi checkbox'la `dt.selection.toggle` üzerinden yapılır — sayfada
// ikinci bir Set YOK. Çivilenen DOM sözleşmesi:
//   • N = 0 → çubuk yok (DOM'da hiçbir şey); selection seçeneksiz tablo → yok
//   • N > 0 → "N seçili", TAM BİR birincil düğme + "Temizle"; sıra K5:
//     Temizle solda, birincil EN SAĞDA
//   • birincil seçili kimliklerle çağrılır; Temizle seçimi boşaltır, çubuk
//     kaybolur, onClear ardından çağrılır
//   • loading: birincil aria-busy + devre dışı, Temizle devre dışı
//   • v0.10.939 (tablo standardı T8) — odak: seçim boşalınca (Temizle ya da
//     sayfanın birincil eylemden sonra temizlemesi) odak <body>'ye DÜŞMEZ;
//     her zaman bağlı kaba (tabIndex=-1) ya da `returnFocusRef` hedefine iner.
//   • v0.10.939 (tablo standardı T8) — duyuru: kalıcı, görünmez TEK canlı
//     bölge; ilk seçim dahil her değişimde "N seçili", boşalınca "Seçim
//     temizlendi". Sayaç kendisi canlı bölge DEĞİL (çift duyuru olmasın).
import { describe, it, expect, afterEach } from 'vitest';
import { act, useRef, type RefObject } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { useDataTable, BulkBar, type ColumnDef, type BulkBarAction } from './index';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

interface Row { id: string; name: string }
const ROWS: Row[] = [{ id: 'a', name: 'alpha' }, { id: 'b', name: 'beta' }, { id: 'c', name: 'gamma' }];
const COLS: ColumnDef<Row>[] = [{ id: 'name', label: 'Name', sortValue: r => r.name, width: 120 }];
const MULTI = { mode: 'multi' as const, getRowId: (r: Row) => r.id };

function Harness({ action, onClear, children, withSelection = true, clearOnPrimary, returnFocus }: {
  action: BulkBarAction; onClear?: () => void; children?: ReactNode; withSelection?: boolean;
  /** Sayfa birincil eylemden sonra seçimi kendisi boşaltır (Inbox "Onayla" deseni). */
  clearOnPrimary?: boolean;
  /** 'page' → sayfanın arama kutusu hedef; 'dangling' → bağlı olmayan ref (current null). */
  returnFocus?: 'page' | 'dangling';
}) {
  const dt = useDataTable<Row>({
    storageKey: 'bulkbar-contract', columns: COLS, rows: ROWS,
    selection: withSelection ? MULTI : undefined,
  });
  const searchRef = useRef<HTMLInputElement>(null);
  const danglingRef = useRef<HTMLInputElement>(null);
  const returnFocusRef: RefObject<HTMLElement | null> | undefined =
    returnFocus === 'page' ? searchRef : returnFocus === 'dangling' ? danglingRef : undefined;
  const act2: BulkBarAction = clearOnPrimary
    ? { ...action, onClick: ids => { action.onClick(ids); dt.selection?.clear(); } }
    : action;
  return (
    <>
      <input aria-label="Ara" ref={searchRef} />
      <button type="button" data-testid="outside">dış</button>
      <BulkBar dt={dt} action={act2} onClear={onClear} returnFocusRef={returnFocusRef}>{children}</BulkBar>
      <div className="table-wrap">
        <table>
          <tbody>
            {dt.sortedRows.map((r, i) => (
              <tr key={r.id} {...dt.rowProps(i)}>
                <td>
                  {dt.selection && (
                    <input type="checkbox" aria-label={`Seç: ${r.name}`}
                      checked={dt.selection.isSelected(r)} onChange={() => dt.selection?.toggle(r)} />
                  )}
                </td>
                <td>{r.name}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

const bar = (el: HTMLElement) => el.querySelector<HTMLElement>('.bulk-bar');
const hostOf = (el: HTMLElement) => el.querySelector<HTMLElement>('[data-bulk-bar-host]');
const live = (el: HTMLElement) => hostOf(el)!.querySelector<HTMLElement>('[role="status"]');
const btn = (el: HTMLElement, text: string) =>
  [...bar(el)!.querySelectorAll('button')].find(b => b.textContent === text)!;
/** Klavye kullanıcısı gibi: önce odak, sonra etkinleştir (jsdom click odak taşımaz). */
const press = (b: HTMLElement) => { act(() => { b.focus(); }); act(() => { b.click(); }); };
const check = (el: HTMLElement, name: string) => {
  const box = el.querySelector<HTMLInputElement>(`input[aria-label="Seç: ${name}"]`)!;
  act(() => { box.click(); });
};
const noop: BulkBarAction = { label: 'Onayla', onClick: () => {} };

describe('BulkBar — görünürlük', () => {
  it('seçim yokken GÖRÜNÜR hiçbir şey basmaz: yalnız boş odak kabı + boş canlı bölge', () => {
    const el = render(<Harness action={noop} />);
    expect(bar(el)).toBeNull();
    expect(el.textContent).not.toContain('seçili');
    const host = hostOf(el)!;
    expect(host, 'her zaman bağlı odak kabı yok').not.toBeNull();
    expect(host.tabIndex, 'kap odak alabilmeli ama sekme sırasına girmemeli').toBe(-1);
    // Kabın tek çocuğu görünmez canlı bölge — N = 0'da metinsiz.
    expect([...host.children].map(c => c.className)).toEqual(['sr-only']);
    expect(host.textContent).toBe('');
  });

  it('selection seçeneği olmayan tabloda (dt.selection null) çubuk da kap da yok', () => {
    const el = render(<Harness action={noop} withSelection={false} />);
    expect(el.querySelector('input[type="checkbox"]')).toBeNull();
    expect(bar(el)).toBeNull();
    expect(hostOf(el)).toBeNull();
  });

  it('N > 0 → "N seçili"; seçim büyüdükçe sayaç güncellenir', () => {
    const el = render(<Harness action={noop} />);
    check(el, 'alpha');
    expect(bar(el)).not.toBeNull();
    expect(bar(el)!.querySelector('.bulk-bar-count')!.textContent).toBe('1 seçili');
    check(el, 'gamma');
    expect(bar(el)!.querySelector('.bulk-bar-count')!.textContent).toBe('2 seçili');
    expect(bar(el)!.getAttribute('role')).toBe('group');
    expect(bar(el)!.getAttribute('aria-label')).toBe('Toplu işlem');
    expect(bar(el)!.parentElement, 'çubuk her zaman bağlı kabın içinde').toBe(hostOf(el));
  });
});

describe('BulkBar — duyuru (kalıcı canlı bölge)', () => {
  it('canlı bölge seçimden ÖNCE bağlı; ilk seçim dahil her değişimi aynı düğümde duyurur', () => {
    const el = render(<Harness action={noop} />);
    const region = live(el)!;
    expect(region, 'N = 0\'da canlı bölge yok — ilk seçim duyurulmaz').not.toBeNull();
    expect(region.getAttribute('aria-live')).toBe('polite');
    expect(region.getAttribute('aria-atomic')).toBe('true');
    expect(region.classList.contains('sr-only'), 'canlı bölge görünür').toBe(true);
    expect(region.textContent).toBe('');
    check(el, 'alpha');
    expect(live(el), 'canlı bölge yeniden bağlandı — AT değişimi kaçırır').toBe(region);
    expect(region.textContent).toBe('1 seçili');
    check(el, 'gamma');
    expect(region.textContent).toBe('2 seçili');
    check(el, 'gamma');
    expect(region.textContent).toBe('1 seçili');
  });

  it('Temizle → "Seçim temizlendi"; sonraki seçim yine duyurulur', () => {
    const el = render(<Harness action={noop} />);
    const region = live(el)!;
    check(el, 'alpha');
    check(el, 'beta');
    press(btn(el, 'Temizle'));
    expect(bar(el)).toBeNull();
    expect(live(el)).toBe(region);
    expect(region.textContent).toBe('Seçim temizlendi');
    check(el, 'gamma');
    expect(region.textContent).toBe('1 seçili');
  });

  it('sayfa birincil eylemden sonra temizlerse de "Seçim temizlendi"', () => {
    const el = render(<Harness action={noop} clearOnPrimary />);
    check(el, 'alpha');
    press(btn(el, 'Onayla'));
    expect(bar(el)).toBeNull();
    expect(live(el)!.textContent).toBe('Seçim temizlendi');
  });

  it('TEK canlı bölge: görünür sayaç aria-live taşımaz (çift duyuru yok)', () => {
    const el = render(<Harness action={noop} />);
    check(el, 'alpha');
    expect(bar(el)!.querySelector('.bulk-bar-count')!.hasAttribute('aria-live')).toBe(false);
    expect(el.querySelectorAll('[aria-live]').length).toBe(1);
  });
});

describe('BulkBar — odak <body>\'ye düşmez', () => {
  it('Temizle: odak her zaman bağlı kaba iner (body değil)', () => {
    const el = render(<Harness action={noop} />);
    check(el, 'alpha');
    press(btn(el, 'Temizle'));
    expect(bar(el)).toBeNull();
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(hostOf(el));
  });

  it('Temizle + returnFocusRef: sayfa hedefi tercih edilir', () => {
    const el = render(<Harness action={noop} returnFocus="page" />);
    check(el, 'alpha');
    press(btn(el, 'Temizle'));
    expect(document.activeElement).toBe(el.querySelector('input[aria-label="Ara"]'));
  });

  it('returnFocusRef bağlı değilse (current null) kaba düşer', () => {
    const el = render(<Harness action={noop} returnFocus="dangling" />);
    check(el, 'alpha');
    press(btn(el, 'Temizle'));
    expect(document.activeElement).toBe(hostOf(el));
  });

  it('sayfa birincil eylemden sonra temizler: odak birincilden kaba iner', () => {
    let got = 0;
    const el = render(<Harness action={{ label: 'Onayla', onClick: () => { got++; } }} clearOnPrimary />);
    check(el, 'alpha');
    press(btn(el, 'Onayla'));
    expect(got).toBe(1);
    expect(bar(el)).toBeNull();
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(hostOf(el));
  });

  it('sayfa birincilden sonra temizler + returnFocusRef: sayfa hedefi', () => {
    const el = render(<Harness action={noop} clearOnPrimary returnFocus="page" />);
    check(el, 'alpha');
    press(btn(el, 'Onayla'));
    expect(document.activeElement).toBe(el.querySelector('input[aria-label="Ara"]'));
  });

  it('odak çubuğun DIŞINDAYKEN seçim boşalırsa odağa dokunulmaz', () => {
    const el = render(<Harness action={noop} />);
    check(el, 'alpha');
    const box = el.querySelector<HTMLInputElement>('input[aria-label="Seç: alpha"]')!;
    act(() => { box.focus(); });
    check(el, 'alpha'); // son seçimi kaldır → çubuk kaybolur
    expect(bar(el)).toBeNull();
    expect(document.activeElement).toBe(box);
  });
});

describe('BulkBar — eylemler', () => {
  it('TAM BİR birincil + "Temizle"; birincil EN SAĞDA (K5)', () => {
    const el = render(<Harness action={{ label: 'Onayla (2)', onClick: () => {} }} />);
    check(el, 'alpha');
    const btns = [...bar(el)!.querySelectorAll('button')];
    expect(btns.map(b => b.textContent)).toEqual(['Temizle', 'Onayla (2)']);
    // Button atomu: primary sınıfsız ('' + boyut), secondary `.sec`.
    const primary = btns.filter(b => !b.classList.contains('sec'));
    expect(primary.length, 'yan yana iki dolu buton yazılamaz').toBe(1);
    expect(primary[0]).toBe(btns[btns.length - 1]);
    expect(btns[0].classList.contains('sec')).toBe(true);
  });

  it('birincil seçili kimliklerle çağrılır', () => {
    let got: string[] = [];
    const el = render(<Harness action={{ label: 'Onayla', onClick: ids => { got = [...ids].sort(); } }} />);
    check(el, 'beta');
    check(el, 'alpha');
    const primary = [...bar(el)!.querySelectorAll('button')].pop()!;
    act(() => { primary.click(); });
    expect(got).toEqual(['a', 'b']);
  });

  it('"Temizle" seçimi boşaltır, çubuk kaybolur, onClear ardından çağrılır', () => {
    let cleared = 0;
    const el = render(<Harness action={noop} onClear={() => cleared++} />);
    check(el, 'alpha');
    check(el, 'beta');
    act(() => { btn(el, 'Temizle').click(); });
    expect(bar(el)).toBeNull();
    expect(cleared).toBe(1);
    for (const box of el.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')) expect(box.checked).toBe(false);
  });

  it('children bağlam satırı sayacın yanında', () => {
    const el = render(<Harness action={noop}>3 onaylanabilir · 1 atlanacak</Harness>);
    check(el, 'alpha');
    expect(bar(el)!.querySelector('.bulk-bar-meta')!.textContent).toBe('3 onaylanabilir · 1 atlanacak');
  });

  it('loading: birincil aria-busy + devre dışı, Temizle devre dışı; disabled yalnız birincili kapatır', () => {
    const busy = render(<Harness action={{ label: 'Onayla', onClick: () => {}, loading: true }} />);
    check(busy, 'alpha');
    const [clear, primary] = [...bar(busy)!.querySelectorAll('button')];
    expect(primary.getAttribute('aria-busy')).toBe('true');
    expect(primary.disabled).toBe(true);
    expect(clear.disabled).toBe(true);
    act(() => { root?.unmount(); });
    host?.remove();

    const off = render(<Harness action={{ label: 'Onayla', onClick: () => {}, disabled: true }} />);
    check(off, 'alpha');
    const [clear2, primary2] = [...bar(off)!.querySelectorAll('button')];
    expect(primary2.disabled).toBe(true);
    expect(clear2.disabled).toBe(false);
  });
});
