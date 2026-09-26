// @vitest-environment jsdom
//
// rowLink.contract.test.tsx — v0.10.939 (tablo standardı T7): "bir satır, bir
// eylem; gezinen satır GERÇEK bağlantı". getRowHref v0.10.216'dan beri vardı
// ve 0 benimseyeni vardı (yazılmış-bağlanmamış). Sözleşme:
//   • rowProps: href'li satır data-row-action + YEDEK fare işleyicisi; href'siz
//     satır ve getRowHref'siz tablo bugünkü gibi (anahtar yok)
//   • v0.10.939 (tablo standardı T7) — yedek lib/utils `rowClickHandlers`tan
//     (etkileşimli öğe koruması orada, Services & co. ile ORTAK); getRowHref'li
//     tabloda `rowProps(i)` satırsız çağrı geliştirmede console.error (bir kez)
//   • rowLink: düz hücre → { to, className: 'row-link' }; YALNIZ ilk link
//     hücresi Tab durağı, diğerleri tabIndex -1; ownLink / actions → null
//   • `row-cell` (dolgu linke) cellProps'tan DEĞİL, linki çizen
//     <DataTableCell>'den; argüman sırası cellProps(row, col) = rowLink(row, col)
//   • <DataTableCell>: gerçek <a href> — orta tık / ⌘-tık tarayıcının
//   • link tıkı satır yedeğini TETİKLEMEZ (tek gezinme); ownLink hücresinin
//     boşluğu yedekle açar; etkileşimli öğe (düğme) yedeği tetiklemez
//   • orta tık / ⌘-tık yedekte yeni sekme; <a> üzerinde yedek SUSAR
//   • rowLinkReplace → linkler ve yedek `replace`
//   • VirtualTable href'li satırı işaretler, yedek onClick'i korur
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter, useLocation, useNavigationType } from 'react-router-dom';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { useDataTable, DataTableCell, VirtualTable, type DataTable, type ColumnDef } from './index';
import { rowClickHandlers } from '@/lib/utils';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter initialEntries={['/list']}>{node}<Where /></MemoryRouter>); });
  return host;
}
// window.open jsdom'da yok ("Not implemented") — yeni sekme çağrısı kaydedilir.
const openSpy = vi.fn((...args: unknown[]) => { void args; return null; });
beforeEach(() => { openSpy.mockClear(); vi.stubGlobal('open', openSpy); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  vi.unstubAllGlobals();
  nav.length = 0;
});

// Gezinme günlüğü: her konum değişimi (yol + tür) — "iki kez gezinmedi" ölçüsü.
const nav: string[] = [];
function Where() {
  const loc = useLocation();
  const type = useNavigationType();
  if (loc.pathname !== '/list' && nav[nav.length - 1] !== `${type} ${loc.pathname}${loc.search}`) {
    nav.push(`${type} ${loc.pathname}${loc.search}`);
  }
  return null;
}

interface Row { id: string; name: string; n: number; linkable: boolean }
const ROWS: Row[] = [
  { id: 'a', name: 'alpha', n: 3, linkable: true },
  { id: 'b', name: 'beta', n: 1, linkable: false },
];
const COLS: ColumnDef<Row>[] = [
  { id: 'name', label: 'Name', sortValue: r => r.name, naturalDir: 'asc', width: 120 },
  { id: 'n', label: 'N', sortValue: r => r.n, numeric: true, width: 80 },
  { id: 'own', label: 'Own', ownLink: true, width: 80 },
  { id: 'act', label: '', kind: 'actions', width: 60 },
];
const href = (r: Row) => (r.linkable ? `/svc/${r.name}` : null);

let last: DataTable<Row> | null = null;
function Probe({ replace, withHref = true }: { replace?: boolean; withHref?: boolean }) {
  const dt = useDataTable<Row>({
    storageKey: 'row-link', columns: COLS, rows: ROWS, initialSort: { id: 'name', dir: 'asc' },
    getRowHref: withHref ? href : undefined, rowLinkReplace: replace,
  });
  last = dt;
  return (
    <table {...dt.tableProps}>
      <tbody>
        {dt.sortedRows.map((r, i) => (
          <tr key={r.id} {...dt.rowProps(i, r)}>
            <DataTableCell dt={dt} col="name" row={r} value={r.name} />
            <DataTableCell dt={dt} col="n" row={r} value={String(r.n)} />
            <td {...dt.cellProps(r, 'own')}><a href="#own" className="own">x</a> boşluk</td>
            <td {...dt.cellProps(r, 'act')}><button type="button" className="act-btn">⋯</button></td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const rowOf = (el: HTMLElement, name: string) =>
  [...el.querySelectorAll<HTMLTableRowElement>('tbody tr')].find(tr => tr.textContent?.startsWith(name))!;

describe('rowProps — işaret + yedek', () => {
  it('href\'li satır data-row-action + onClick/onAuxClick/onMouseDown; href\'siz satır yok', () => {
    render(<Probe />);
    const a = last!.rowProps(0, ROWS[0]);
    expect(a['data-row-action']).toBe(true);
    expect(typeof a.onClick).toBe('function');
    expect(typeof a.onAuxClick).toBe('function');
    expect(typeof a.onMouseDown).toBe('function');
    const b = last!.rowProps(1, ROWS[1]);
    expect(Object.prototype.hasOwnProperty.call(b, 'data-row-action')).toBe(false);
    expect(b.onClick).toBeUndefined();
  });

  it('getRowHref yoksa bugünkü rowProps (anahtar/işleyici yok)', () => {
    render(<Probe withHref={false} />);
    const p = last!.rowProps(0);
    expect(Object.keys(p).sort()).toEqual(['className', 'data-row-idx', 'data-table-id']);
    expect(last!.rowLink(ROWS[0], 'name')).toBeNull();
    expect(last!.cellProps(ROWS[0], 'name').className).toBeUndefined();
  });

  it('rowProps(i, row): gruplu listede satır açıkça verilir', () => {
    render(<Probe />);
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      // index 1 → sortedRows'ta beta (href yok); satır açıkça alpha verilince işaret var.
      expect(last!.rowProps(1)['data-row-action']).toBeUndefined();
      expect(last!.rowProps(1, ROWS[0])['data-row-action']).toBe(true);
    } finally { err.mockRestore(); }
  });

  // v0.10.939 (tablo standardı T7) — sortedRows[index] tuzağı SESLİ: gruplu /
  // süzülmüş listede `rowProps(i)` başka bir satırın href'ine gider. Geliştirmede
  // tablo başına BİR console.error (her satır × her render değil); satır
  // verilince ya da getRowHref'siz tabloda sessiz.
  it('getRowHref\'li tabloda rowProps(i) satırsız → geliştirmede tek console.error', () => {
    render(<Probe />);
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      last!.rowProps(0, ROWS[0]);
      expect(err).not.toHaveBeenCalled();
      last!.rowProps(0);
      last!.rowProps(1);
      expect(err).toHaveBeenCalledTimes(1);
      expect(String(err.mock.calls[0][0])).toContain('rowProps(i, row)');
      expect(String(err.mock.calls[0][0])).toContain('row-link');
    } finally { err.mockRestore(); }
    // Satırsız çağrının kaynağı DEV kapısının arkasında (üretim paketinde yok).
    const src = readFileSync(resolve(__dirname, 'DataTable.tsx'), 'utf8');
    expect(src).toMatch(/if \(import\.meta\.env\.DEV && getRowHref && rowArg === undefined/);
  });

  it('getRowHref\'siz tabloda rowProps(i) sessiz', () => {
    render(<Probe withHref={false} />);
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      last!.rowProps(0);
      expect(err).not.toHaveBeenCalled();
    } finally { err.mockRestore(); }
  });
});

describe('rowLink — tek Tab durağı, ownLink/actions sarılmaz', () => {
  it('ilk link kolonu Tab durağı, diğerleri -1; ownLink/actions/href\'siz → null', () => {
    render(<Probe />);
    const first = last!.rowLink(ROWS[0], 'name')!;
    expect(first.to).toBe('/svc/alpha');
    expect(first.className).toBe('row-link');
    expect(Object.prototype.hasOwnProperty.call(first, 'tabIndex')).toBe(false);
    expect(Object.prototype.hasOwnProperty.call(first, 'replace')).toBe(false);
    expect(last!.rowLink(ROWS[0], 'n')!.tabIndex).toBe(-1);
    expect(last!.rowLink(ROWS[0], 'own')).toBeNull();
    expect(last!.rowLink(ROWS[0], 'act')).toBeNull();
    expect(last!.rowLink(ROWS[1], 'name')).toBeNull();
  });

  // v0.10.939 (tablo standardı T7) — `row-cell` "dolgu linke" demek: linki
  // çizen <DataTableCell> basar. cellProps basmaz — onu elle yayan linksiz
  // hücre dolgusunu kaybetmesin.
  it('cellProps row-cell BASMAZ; <DataTableCell> yalnız link çizdiği hücreye basar', () => {
    const el = render(<Probe />);
    expect(last!.cellProps(ROWS[0], 'name').className).toBeUndefined();
    expect(last!.cellProps(ROWS[0], 'n').className).toBe('num');
    expect(last!.cellProps(ROWS[0], 'own').className).toBeUndefined();
    expect(last!.cellProps(ROWS[0], 'act').className).toBe('col-actions');
    const [nameTd, nTd, ownTd, actTd] = [...rowOf(el, 'alpha').querySelectorAll('td')];
    expect(nameTd.className).toBe('row-cell');
    expect(nTd.className).toBe('num row-cell');
    expect(ownTd.className).toBe('');
    expect(actTd.className).toBe('col-actions');
    // href'siz satır: link yok → row-cell yok (dolgu hücrede kalır).
    const [betaName, betaN] = [...rowOf(el, 'beta').querySelectorAll('td')];
    expect(betaName.className).toBe('');
    expect(betaN.className).toBe('num');
  });

  it('DOM: gerçek <a href class=row-link>; satır başına TEK Tab durağı; ownLink içinde <a> içinde <a> yok', () => {
    const el = render(<Probe />);
    const tr = rowOf(el, 'alpha');
    const links = [...tr.querySelectorAll<HTMLAnchorElement>('a.row-link')];
    expect(links.map(a => a.getAttribute('href'))).toEqual(['/svc/alpha', '/svc/alpha']);
    expect(links.map(a => a.tabIndex)).toEqual([0, -1]);
    expect(tr.querySelector('a a')).toBeNull();
    expect(rowOf(el, 'beta').querySelector('a.row-link')).toBeNull();
  });
});

describe('tık davranışı — tek gezinme', () => {
  it('link tıkı bir kez gezinir (satır yedeği susar)', () => {
    const el = render(<Probe />);
    act(() => { rowOf(el, 'alpha').querySelector<HTMLAnchorElement>('a.row-link')!.click(); });
    expect(nav).toEqual(['PUSH /svc/alpha']);
  });

  it('ownLink hücresinin boşluğu yedekle açar; düğme ve kendi <a>\'sı yedeği tetiklemez', () => {
    const el = render(<Probe />);
    const tr = rowOf(el, 'alpha');
    act(() => { tr.querySelector<HTMLButtonElement>('.act-btn')!.click(); });
    expect(nav).toEqual([]);
    // Kendi <a>'sının varsayılan gezinmesi jsdom'da yok; yedek susmalı.
    act(() => { tr.querySelector<HTMLAnchorElement>('a.own')!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })); });
    expect(nav).toEqual([]);
    act(() => { tr.querySelectorAll('td')[2].dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(nav).toEqual(['PUSH /svc/alpha']);
  });

  it('⌘-tık ve orta tık yedekte YENİ SEKME; <a> üzerinde orta tık yedeği susar', () => {
    const el = render(<Probe />);
    const tr = rowOf(el, 'alpha');
    const pad = tr.querySelectorAll('td')[2];
    act(() => { pad.dispatchEvent(new MouseEvent('click', { bubbles: true, metaKey: true })); });
    act(() => { pad.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, button: 1 })); });
    expect(openSpy.mock.calls).toEqual([
      ['/svc/alpha', '_blank', 'noopener,noreferrer'],
      ['/svc/alpha', '_blank', 'noopener,noreferrer'],
    ]);
    act(() => { tr.querySelector('a.row-link')!.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, button: 1 })); });
    expect(openSpy.mock.calls.length).toBe(2);
    expect(nav).toEqual([]);
  });

  it('rowLinkReplace: link ve yedek replace ile gezinir', () => {
    const el = render(<Probe replace />);
    expect(last!.rowLink(ROWS[0], 'name')!.replace).toBe(true);
    act(() => { rowOf(el, 'alpha').querySelectorAll('td')[2].dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(nav).toEqual(['REPLACE /svc/alpha']);
  });
});

describe('VirtualTable — href\'li satır', () => {
  // jsdom yerleşim yapmaz: virtualizer kabın offsetHeight'ını 0 görür ve hiç
  // satır basmaz. Test süresince `.vt-scroll` kabına 200 px yükseklik verilir.
  let restore: (() => void) | null = null;
  beforeEach(() => {
    const proto = HTMLElement.prototype;
    const h = Object.getOwnPropertyDescriptor(proto, 'offsetHeight');
    const w = Object.getOwnPropertyDescriptor(proto, 'offsetWidth');
    Object.defineProperty(proto, 'offsetHeight', { configurable: true, get(this: HTMLElement) { return this.classList.contains('vt-scroll') ? 200 : 0; } });
    Object.defineProperty(proto, 'offsetWidth', { configurable: true, get(this: HTMLElement) { return this.classList.contains('vt-scroll') ? 800 : 0; } });
    restore = () => {
      if (h) Object.defineProperty(proto, 'offsetHeight', h);
      if (w) Object.defineProperty(proto, 'offsetWidth', w);
    };
  });
  afterEach(() => { restore?.(); restore = null; });

  function V() {
    const dt = useDataTable<Row>({ storageKey: 'row-link-vt', columns: COLS, rows: ROWS, getRowHref: href });
    return (
      <VirtualTable<Row> dt={dt} height={200} getRowKey={r => r.id}
        renderRow={r => <><DataTableCell dt={dt} col="name" row={r} value={r.name} /><td {...dt.cellProps(r, 'own')}>boşluk</td></>} />
    );
  }
  it('işaret + yedek onClick; href\'siz satır işaretsiz; tablo sınıfı dt', () => {
    const el = render(<V />);
    const rows = [...el.querySelectorAll<HTMLTableRowElement>('tbody tr[data-row-idx]')];
    expect(rows.length).toBe(2);
    const alpha = rows.find(tr => tr.textContent?.startsWith('alpha'))!;
    const beta = rows.find(tr => tr.textContent?.startsWith('beta'))!;
    expect(alpha.hasAttribute('data-row-action')).toBe(true);
    expect(beta.hasAttribute('data-row-action')).toBe(false);
    expect(alpha.querySelector('a.row-link')!.getAttribute('href')).toBe('/svc/alpha');
    act(() => { alpha.querySelectorAll('td')[1].dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(nav).toEqual(['PUSH /svc/alpha']);
    expect(el.querySelector('table')!.className).toBe('dt');
  });
});

// v0.10.939 (tablo standardı T7) — satırın yedek işleyicisi TEK yardımcı:
// lib/utils `rowClickHandlers`. Etkileşimli öğe koruması oraya taşındı, yani
// Services / TracesResult / OperationsTable satırları da kazandı: hücredeki
// <a>'ya orta tık artık iki sekme açmaz, satırdaki düğme satırı gezdirmez.
describe('rowClickHandlers — etkileşimli öğe koruması (ortak yardımcı)', () => {
  function Row({ go }: { go: () => void }) {
    return (
      <table><tbody>
        <tr {...rowClickHandlers('/x', go)}>
          <td className="pad">boşluk</td>
          <td><a href="#own" className="own">link</a></td>
          <td><button type="button" className="btn">eylem</button></td>
          <td><span role="button" className="rb">rol</span></td>
          <td><input className="inp" /></td>
        </tr>
      </tbody></table>
    );
  }
  it('boşluk gezinir; a / button / role=button / input tıkı gezinmez', () => {
    const go = vi.fn();
    const el = render(<Row go={go} />);
    for (const sel of ['.own', '.btn', '.rb', '.inp']) {
      act(() => { el.querySelector(sel)!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })); });
    }
    expect(go).not.toHaveBeenCalled();
    act(() => { el.querySelector('.pad')!.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(go).toHaveBeenCalledTimes(1);
  });
  it('orta tık / ⌘-tık boşlukta yeni sekme; <a> üzerinde orta tık yardımcıyı susturur', () => {
    const go = vi.fn();
    const el = render(<Row go={go} />);
    act(() => { el.querySelector('.own')!.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, button: 1 })); });
    act(() => { el.querySelector('.btn')!.dispatchEvent(new MouseEvent('click', { bubbles: true, metaKey: true })); });
    expect(openSpy).not.toHaveBeenCalled();
    act(() => { el.querySelector('.pad')!.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, button: 1 })); });
    act(() => { el.querySelector('.pad')!.dispatchEvent(new MouseEvent('click', { bubbles: true, metaKey: true })); });
    expect(openSpy.mock.calls).toEqual([
      ['/x', '_blank', 'noopener,noreferrer'],
      ['/x', '_blank', 'noopener,noreferrer'],
    ]);
    expect(go).not.toHaveBeenCalled();
  });
  it('role=button SATIRIN kendisi etkileşimli sayılmaz', () => {
    const go = vi.fn();
    const el = render(<table><tbody><tr role="button" {...rowClickHandlers('/x', go)}><td className="c">x</td></tr></tbody></table>);
    act(() => { el.querySelector('.c')!.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(go).toHaveBeenCalledTimes(1);
  });
  it('useDataTable yedeği yardımcıdan kurulur — ikinci koruma / openNewTab kopyası yok', () => {
    const strip = (t: string) => t.replace(/\/\*[\s\S]*?\*\//g, '').split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');
    const dt = strip(readFileSync(resolve(__dirname, 'DataTable.tsx'), 'utf8'));
    expect(dt).toContain('...rowClickHandlers(href, () => navigate(href, { replace: rowLinkReplace }))');
    expect(dt).not.toMatch(/function fromInteractive|ROW_INTERACTIVE|openNewTab|window\.open\(/);
  });
});
