// @vitest-environment jsdom
//
// columnFlags.contract.test.tsx — v0.10.939 (tablo standardı dilim 2).
// Kolon bayrakları → hücre sözleşmesi. Sayfa sınıf yazmaz; primitif basar:
//   • dt.cellProps(row, colId, value): numeric → num (T4), mono (T5), tone →
//     cell-err/-warn/-muted/-faint (T9/T5), truncate 'wrap' → cell-wrap, kind
//     'actions' → col-actions (T8); kırpılan DİZGE değer title'a (T11) —
//     sayı kolonunda yalnız truncate AÇIKÇA verilmişse
//   • v0.10.939 (tablo standardı T4) — <DataTableCell> değersiz hücre soluk "—"
//   • v0.10.939 (tablo standardı T11) — <MiddleEllipsis> satır içi kutular
//     (flex DEĞİL: kopya/ekran okuyucu tek dizge), tam değer title'da
//   • v0.10.939 (tablo standardı T11) — cell-wrap linkli hücrede de sarar
//   • dt.tableProps → table.dt (T10)
//   • kind 'actions' başlığı: etiket boş (ad aria-label'da), sıralanmaz,
//     boyutlanmaz, sağa yaslı
//   • <DataTableCell> / <MiddleEllipsis> DOM sözleşmesi
//   • bastığı her sınıfın globals.css karşılığı var (primitiveClasses
//     kapısı ui/DataTable alt klasörünü taramıyor — bu test o boşluğu kapatır)
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  useDataTable, DataTableHead, DataTableCell, MiddleEllipsis, splitMiddle, MIDDLE_ELLIPSIS_TAIL,
  type DataTable, type ColumnDef,
} from './index';

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

interface Row { id: string; svc: string; err: number; note: string }
const ROWS: Row[] = [
  { id: 'a1b2c3d4e5f6a7b8', svc: 'checkout-api-7c8977f965-7hrqz', err: 6.4, note: 'uzun bir not' },
  { id: 'ffff', svc: 'cart', err: 0, note: '' },
];
const tone = (r: Row) => (r.err > 5 ? 'err' as const : r.err > 1 ? 'warn' as const : r.err === 0 ? 'faint' as const : undefined);
const COLS: ColumnDef<Row>[] = [
  { id: 'id', label: 'Trace ID', mono: true, truncate: 'middle', sortValue: r => r.id, width: 140 },
  { id: 'svc', label: 'Service', sortValue: r => r.svc, width: 160 },
  { id: 'err', label: 'Err %', numeric: true, tone, sortValue: r => r.err, width: 80 },
  { id: 'note', label: 'Not', truncate: 'wrap', width: 200 },
  { id: 'dim', label: 'Dim', tone: () => 'muted', width: 80 },
  // Sahte sıralanabilir eylem kolonu (T3 fakeSortable) — primitif yine sıralamaz.
  { id: 'act', label: 'Actions', kind: 'actions', sortValue: () => 0, width: 60 },
];

let last: DataTable<Row> | null = null;
function Probe({ children }: { children?: (dt: DataTable<Row>) => ReactNode }) {
  const dt = useDataTable<Row>({ storageKey: 'col-flags', columns: COLS, rows: ROWS });
  last = dt;
  return (
    <table {...dt.tableProps}>
      <DataTableHead dt={dt} />
      <tbody>{children?.(dt)}</tbody>
    </table>
  );
}

describe('dt.cellProps — bayrak → sınıf', () => {
  it('numeric + tone err → "num cell-err"; tonu olmayan satır yalnız num', () => {
    render(<Probe />);
    expect(last!.cellProps(ROWS[0], 'err').className).toBe('num cell-err');
    expect(last!.cellProps(ROWS[1], 'err').className).toBe('num cell-faint');
    expect(last!.cellProps({ ...ROWS[1], err: 2 }, 'err').className).toBe('num cell-warn');
    expect(last!.cellProps({ ...ROWS[1], err: 0.5 }, 'err').className).toBe('num');
    expect(last!.cellProps(ROWS[0], 'dim').className).toBe('cell-muted');
  });

  it('mono kimlik kolonu; bayraksız kolon sınıf ANAHTARI taşımaz', () => {
    render(<Probe />);
    expect(last!.cellProps(ROWS[0], 'id').className).toBe('mono');
    const plain = last!.cellProps(ROWS[0], 'svc');
    expect(Object.prototype.hasOwnProperty.call(plain, 'className')).toBe(false);
  });

  it('truncate: end/middle dizge değeri title\'a koyar; wrap koymaz, cell-wrap basar', () => {
    render(<Probe />);
    expect(last!.cellProps(ROWS[0], 'svc', ROWS[0].svc).title).toBe(ROWS[0].svc);   // varsayılan 'end'
    expect(last!.cellProps(ROWS[0], 'id', ROWS[0].id).title).toBe(ROWS[0].id);      // 'middle'
    const wrap = last!.cellProps(ROWS[0], 'note', ROWS[0].note);
    expect(wrap.className).toBe('cell-wrap');
    expect(wrap.title).toBeUndefined();
    // Dizge olmayan / boş değer ipucu değildir.
    expect(last!.cellProps(ROWS[0], 'err', 6.4).title).toBeUndefined();
    expect(last!.cellProps(ROWS[1], 'svc', '').title).toBeUndefined();
    expect(last!.cellProps(ROWS[1], 'svc').title).toBeUndefined();
  });

  // v0.10.939 (tablo standardı T11) — sayı kırpılmaz: numeric kolonda biçimli
  // dizge ("6.40") bile ipucu DEĞİL; truncate açıkça verilirse ipucu döner.
  // Kimlik (mono) ve middle kolonları ipucunu tutar.
  it('numeric kolon: title yok — truncate açıkça verilmedikçe', () => {
    function P() {
      const dt = useDataTable<Row>({
        storageKey: 'col-flags-num-title', rows: ROWS, columns: [
          { id: 'err', label: 'Err %', numeric: true, width: 80 },
          { id: 'errT', label: 'Err % (kırpılır)', numeric: true, truncate: 'end', width: 80 },
          { id: 'errM', label: 'Kimlik no', numeric: true, truncate: 'middle', width: 80 },
        ],
      });
      last = dt;
      return null;
    }
    render(<P />);
    expect(last!.cellProps(ROWS[0], 'err', '6.40').title).toBeUndefined();
    expect(last!.cellProps(ROWS[0], 'errT', '6.40').title).toBe('6.40');
    expect(last!.cellProps(ROWS[0], 'errM', '1234567890').title).toBe('1234567890');
  });

  it('kind actions → yalnız col-actions (num/tone/title yok); bilinmeyen kolon → {}', () => {
    render(<Probe />);
    expect(last!.cellProps(ROWS[0], 'act', 'x')).toEqual({ className: 'col-actions' });
    expect(last!.cellProps(ROWS[0], 'yok', 'x')).toEqual({});
  });

  it('tableProps → table.dt', () => {
    const el = render(<Probe />);
    expect(last!.tableProps).toEqual({ className: 'dt' });
    expect(el.querySelector('table')!.className).toBe('dt');
  });
});

describe('kind: actions başlığı (T8)', () => {
  it('etiket boş, ad aria-label\'da; sıralanmaz, boyutlanmaz, sağa yaslı', () => {
    const el = render(<Probe />);
    const th = el.querySelector<HTMLElement>('thead th.col-actions')!;
    expect(th).not.toBeNull();
    expect(th.getAttribute('aria-label')).toBe('Actions');
    expect(th.querySelector('.sort-arrow')).toBeNull();
    expect(th.querySelector('.col-resize-handle')).toBeNull();
    expect(th.getAttribute('aria-sort')).toBeNull();
    expect(th.getAttribute('tabindex')).toBeNull();
    expect(th.classList.contains('sortable')).toBe(false);
    expect(th.style.textAlign).toBe('right');
    // Metin YALNIZ menü tetiğinin glifi (son kolon ⋯ menüsünü taşır).
    expect(th.querySelector('.dt-menu')).not.toBeNull();
    expect(th.textContent).toBe('⋯');
    const sortBefore = last!.sort;
    act(() => { th.click(); });
    act(() => { last!.toggleSort('act'); });
    expect(last!.sort).toEqual(sortBefore);
  });

  it('etiketsiz eylem kolonu "Eylemler" adını alır', () => {
    function P() {
      const dt = useDataTable<Row>({ storageKey: 'col-flags-anon', columns: [COLS[1], { id: 'act', label: '', kind: 'actions', width: 60 }], rows: ROWS });
      return <table><DataTableHead dt={dt} /><tbody /></table>;
    }
    const el = render(<P />);
    expect(el.querySelector('thead th.col-actions')!.getAttribute('aria-label')).toBe('Eylemler');
  });

  it('diğer kolonlar sıralanabilir kalır (sayısal ok önce, etiket sonra)', () => {
    const el = render(<Probe />);
    const errTh = [...el.querySelectorAll('thead th')].find(t => t.textContent?.includes('Err %'))!;
    expect(errTh.classList.contains('num')).toBe(true);
    expect(errTh.getAttribute('aria-sort')).toBe('none');
  });
});

describe('<DataTableCell>', () => {
  it('sınıf + title + değer; middle → MiddleEllipsis; sayfanın className\'i eklenir', () => {
    const el = render(
      <Probe>{dt => (
        <tr>
          <DataTableCell dt={dt} col="id" row={ROWS[0]} value={ROWS[0].id} className="extra" />
          <DataTableCell dt={dt} col="err" row={ROWS[0]} value="6.40" />
          <DataTableCell dt={dt} col="svc" row={ROWS[0]} value={ROWS[0].svc}><b>özel</b></DataTableCell>
        </tr>
      )}</Probe>,
    );
    const [idTd, errTd, svcTd] = [...el.querySelectorAll('tbody td')];
    expect(idTd.className).toBe('mono extra');
    expect(idTd.getAttribute('title')).toBe(ROWS[0].id);
    const mid = idTd.querySelector<HTMLElement>('.mid-ellipsis')!;
    expect(mid.querySelector('.mid-ellipsis__head')!.textContent).toBe(ROWS[0].id.slice(0, -MIDDLE_ELLIPSIS_TAIL));
    expect(mid.querySelector('.mid-ellipsis__tail')!.textContent).toBe(ROWS[0].id.slice(-MIDDLE_ELLIPSIS_TAIL));
    expect(idTd.textContent).toBe(ROWS[0].id); // tam metin DOM'da (kopyala / ekran okuyucu)
    expect(mid.getAttribute('title')).toBe(ROWS[0].id); // tam değer ipucu kökte de
    expect(mid.style.getPropertyValue('--mid-tail-w')).toBe(`${MIDDLE_ELLIPSIS_TAIL}ch`);
    expect(errTd.className).toBe('num cell-err');
    expect(errTd.textContent).toBe('6.40');
    expect(errTd.getAttribute('title')).toBeNull(); // v0.10.939 — sayı ipucu değil
    expect(svcTd.querySelector('b')!.textContent).toBe('özel'); // children kazanır
    expect(svcTd.getAttribute('title')).toBe(ROWS[0].svc);
    // getRowHref yok → link yok.
    expect(el.querySelector('tbody a')).toBeNull();
  });

  it('sayfanın title\'ı hücrenin ipucunu ezer (middle içeriğinde de)', () => {
    const el = render(
      <Probe>{dt => (
        <tr>
          <DataTableCell dt={dt} col="svc" row={ROWS[0]} value={ROWS[0].svc} title="elle" />
          <DataTableCell dt={dt} col="id" row={ROWS[0]} value={ROWS[0].id} title="elle-id" />
        </tr>
      )}</Probe>,
    );
    const [svcTd, idTd] = [...el.querySelectorAll('tbody td')];
    expect(svcTd.getAttribute('title')).toBe('elle');
    expect(idTd.getAttribute('title')).toBe('elle-id');
    expect(idTd.querySelector('.mid-ellipsis')!.getAttribute('title')).toBe('elle-id');
  });

  // v0.10.939 (tablo standardı T4) — değer yoksa boşluk değil soluk "—"
  // (KeyValue'nun boş glifi); 0 bir değerdir; children verilmişse o kazanır.
  it('değersiz hücre (null / undefined / \'\') soluk "—"; 0 değer; children kazanır', () => {
    const el = render(
      <Probe>{dt => (
        <tr>
          <DataTableCell dt={dt} col="svc" row={ROWS[1]} value={null} />
          <DataTableCell dt={dt} col="svc" row={ROWS[1]} />
          <DataTableCell dt={dt} col="note" row={ROWS[1]} value="" />
          <DataTableCell dt={dt} col="err" row={ROWS[1]} value={0} />
          <DataTableCell dt={dt} col="svc" row={ROWS[1]} value={null}>özel</DataTableCell>
        </tr>
      )}</Probe>,
    );
    const tds = [...el.querySelectorAll('tbody td')];
    for (const td of tds.slice(0, 3)) {
      const glyph = td.querySelector('span.cell-empty');
      expect(glyph, td.outerHTML).not.toBeNull();
      expect(glyph!.textContent).toBe('—');
      expect(td.getAttribute('title')).toBeNull();
    }
    expect(tds[3].textContent).toBe('0');
    expect(tds[3].querySelector('.cell-empty')).toBeNull();
    expect(tds[4].textContent).toBe('özel');
  });
});

describe('<MiddleEllipsis>', () => {
  it('splitMiddle: kuyruktan kısa metin bölünmez', () => {
    expect(splitMiddle('abc', 8)).toEqual(['abc', '']);
    expect(splitMiddle('abcdefghij', 4)).toEqual(['abcdef', 'ghij']);
    expect(splitMiddle('abcdef', 0)).toEqual(['abcdef', '']);
  });
  it('iki span; kuyruk yoksa tek span; --mid-tail-w kuyruk uzunluğu; title tam değer', () => {
    const el = render(<><MiddleEllipsis text="GET /api/orders/{id}" tail={4} /><MiddleEllipsis text="kısa" /></>);
    const [a, b] = [...el.querySelectorAll<HTMLElement>('.mid-ellipsis')];
    expect(a.querySelector('.mid-ellipsis__tail')!.textContent).toBe('{id}');
    expect(a.style.getPropertyValue('--mid-tail-w')).toBe('4ch');
    expect(a.getAttribute('title')).toBe('GET /api/orders/{id}');
    expect(b.querySelectorAll('span').length).toBe(1);
    expect(b.textContent).toBe('kısa');
    // Kuyruk yoksa baş kuyruğa yer ayırmaz — kısa metin boşuna kırpılmaz.
    expect(b.style.getPropertyValue('--mid-tail-w')).toBe('0ch');
  });
});

describe('CSS karşılıkları (globals.css)', () => {
  const css = readFileSync(resolve(__dirname, '../../../styles/globals.css'), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');
  it.each([
    ['.cell-muted', /\.cell-muted\s*\{[^}]*color:\s*var\(--text2\)/],
    ['.cell-faint', /\.cell-faint\s*\{[^}]*color:\s*var\(--text3\)/],
    ['.cell-err', /\.cell-err\s*\{[^}]*color:\s*var\(--err\)/],
    ['.cell-warn', /\.cell-warn\s*\{[^}]*color:\s*var\(--warn\)/],
    ['td.cell-wrap', /tbody td\.cell-wrap\s*\{[^}]*overflow-wrap:\s*anywhere/],
    ['td.col-actions', /td\.col-actions, th\.col-actions\s*\{[^}]*text-align:\s*right/],
    ['.cell-empty', /\.cell-empty\s*\{[^}]*color:\s*var\(--text3\)/],
    ['.mid-ellipsis__head', /\.mid-ellipsis__head\s*\{[^}]*text-overflow:\s*ellipsis/],
    ['table.dt', /table\.dt\s*\{[^}]*table-layout:\s*fixed/],
    ['.dt-menu-pop', /\.dt-menu-pop\s*\{[^}]*position:\s*fixed/],
  ])('%s', (_name, re) => {
    expect(css).toMatch(re);
  });
  it('cell-wrap harf ortasından KIRMAZ (break-all yok)', () => {
    const rule = /tbody td\.cell-wrap\s*\{([^}]*)\}/.exec(css)![1];
    expect(rule).not.toMatch(/break-all/);
  });

  // v0.10.939 (tablo standardı T11) — linkli satırda sarılan hücrenin içeriği
  // `.row-link`in (nowrap + "…") içinde: link de sarmazsa `truncate: 'wrap'`
  // linkli tabloda sessizce kırpar.
  it('cell-wrap linkli hücrede de sarar (td.cell-wrap > .row-link)', () => {
    const rule = /tbody td\.cell-wrap > \.row-link\s*\{([^}]*)\}/.exec(css)?.[1];
    expect(rule, 'tbody td.cell-wrap > .row-link kuralı yok').toBeDefined();
    expect(rule).toMatch(/white-space:\s*normal/);
    expect(rule).toMatch(/overflow:\s*visible/);
    expect(rule).toMatch(/text-overflow:\s*clip/);
    expect(rule).toMatch(/overflow-wrap:\s*anywhere/);
    expect(rule).not.toMatch(/break-all/);
  });

  // v0.10.939 (tablo standardı T8) — kolon türü sınıfı `col-actions`; eski
  // `td.cell-actions` / `th.cell-actions` kuralı iç sarmalayıcıyla (td >
  // .cell-actions) aynı adı taşıyordu.
  it('eylem kolonu col-actions; td/th.cell-actions kuralı yok, iç sarmalayıcı duruyor', () => {
    expect(css).not.toMatch(/(^|[\s,}])(td|th)\.cell-actions\b/);
    expect(css).toMatch(/td > \.cell-actions\s*\{[^}]*display:\s*flex/);
  });

  // v0.10.939 (tablo standardı T11) — MiddleEllipsis SATIR İÇİ kutular:
  // `display: flex` baş ve kuyruğu bloklaştırır → kopyala-yapıştır / innerText
  // iki satır, ekran okuyucu iki parça. Kap blok + nowrap; baş inline-block.
  it('mid-ellipsis flex DEĞİL: blok kap + inline-block baş + satır içi kuyruk', () => {
    const body = (sel: string) => new RegExp(`(?:^|[}\\s])${sel.replace(/[.]/g, '\\.')}\\s*\\{([^}]*)\\}`).exec(css)?.[1] ?? '';
    const box = body('.mid-ellipsis');
    const head = body('.mid-ellipsis__head');
    const tail = body('.mid-ellipsis__tail');
    expect(box).toMatch(/display:\s*block/);
    expect(box).toMatch(/white-space:\s*nowrap/);
    expect(box).toMatch(/overflow:\s*hidden/);
    expect(head).toMatch(/display:\s*inline-block/);
    expect(head).toMatch(/max-width:\s*calc\(100% - var\(--mid-tail-w, 8ch\)\)/);
    expect(head).toMatch(/vertical-align:\s*bottom/);
    expect(head).toMatch(/text-overflow:\s*ellipsis/);
    expect(tail).toMatch(/white-space:\s*pre/);
    for (const b of [box, head, tail]) {
      expect(b).not.toMatch(/display:\s*(inline-)?flex|(^|[;\s])flex\s*:/);
    }
    expect(css).not.toMatch(/\.mid-ellipsis[\w-]*\s*\{[^}]*display:\s*(inline-)?flex/);
  });
});
