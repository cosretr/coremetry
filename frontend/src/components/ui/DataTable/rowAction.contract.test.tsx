// @vitest-environment jsdom
//
// rowAction.contract.test.tsx — v0.10.933 (tablo standardı dilim 1, T2).
//
// globals.css el imlecini + hover'ı artık YALNIZ üç işaretten birini taşıyan
// satıra veriyor: `[role="button"]`, `[data-row-action]`,
// `:has(> td > .row-link)`. Yani tıklanabilir bir satır işaretini
// kaybederse ekranda HİÇBİR şey kırılmaz — satır yalnızca "tıklanmaz"
// görünür. Bu kapı iki yarıyı çiviliyor:
//   1. paylaşılan yardımcılar işareti HER ZAMAN basar (rowActivation,
//      rowKeyboard → role=button; rowClickHandlers, dt.rowProps (onOpen
//      varken), VirtualTable onRowClick → data-row-action);
//   2. elle tık işleyicisi (onClick / onMouseDown / onDoubleClick /
//      onAuxClick) takılan her `<tr>` bir işaret taşır;
//   3. TERSİ: işaret taşıyan her `<tr>` (role=button, data-row-action ya da
//      onOpen'lı tablonun rowProps'u) bir işleyici de taşır — imleç yalan
//      söylemez (OperationsTable: rowProps işareti vardı, fare işleyicisi yoktu).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative } from 'node:path';
import { useDataTable, type ColumnDef } from './index';
import { rowActivation, rowKeyboard } from '@/lib/a11y';
import { rowClickHandlers } from '@/lib/utils';
import { jsxOpenTags } from '@/styles/jsxTags';

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
const ROWS: Row[] = [{ id: 'a', name: 'alpha' }, { id: 'b', name: 'beta' }];
const COLS: ColumnDef<Row>[] = [{ id: 'name', label: 'Name', sortValue: r => r.name, width: 120 }];

function Probe({ onOpen }: { onOpen?: (r: Row) => void }) {
  const dt = useDataTable<Row>({ storageKey: 'row-action-contract', columns: COLS, rows: ROWS, onOpen });
  return (
    <table>
      <tbody>{dt.sortedRows.map((r, i) => <tr key={r.id} {...dt.rowProps(i)}><td>{r.name}</td></tr>)}</tbody>
    </table>
  );
}

describe('T2 — satır yardımcıları işaret basar', () => {
  it('rowActivation / rowKeyboard → role="button"', () => {
    expect(rowActivation(() => {}).role).toBe('button');
    expect(rowKeyboard(() => {}).role).toBe('button');
  });

  it('rowClickHandlers → data-row-action', () => {
    expect(rowClickHandlers('/x', () => {})['data-row-action']).toBe(true);
  });

  it('dt.rowProps: onOpen verilmişse data-row-action, verilmemişse YOK', () => {
    const on = render(<Probe onOpen={() => {}} />);
    const rows = [...on.querySelectorAll('tbody tr')];
    expect(rows.length).toBe(2);
    for (const tr of rows) expect(tr.hasAttribute('data-row-action')).toBe(true);
    act(() => { root?.unmount(); });
    host?.remove();
    const off = render(<Probe />);
    for (const tr of off.querySelectorAll('tbody tr')) expect(tr.hasAttribute('data-row-action')).toBe(false);
  });

  /* v0.10.933 (tablo standardı T2) — onOpen'sız tabloda anahtar HİÇ yok
     (`undefined` değil): yayılım sırası açık bir işareti sıfırlayamaz. */
  it('dt.rowProps: onOpen yoksa data-row-action ANAHTARI yok; önceki açık işaret korunur', () => {
    let props: ReturnType<ReturnType<typeof useDataTable<Row>>['rowProps']> | null = null;
    function Keyless() {
      const dt = useDataTable<Row>({ storageKey: 'row-action-keyless', columns: COLS, rows: ROWS });
      props = dt.rowProps(0);
      return (
        <table><tbody>
          <tr {...rowClickHandlers('/x', () => {})} {...dt.rowProps(0)}><td>x</td></tr>
        </tbody></table>
      );
    }
    const el = render(<Keyless />);
    expect(props).not.toBeNull();
    expect(Object.prototype.hasOwnProperty.call(props, 'data-row-action')).toBe(false);
    expect(el.querySelector('tbody tr')!.hasAttribute('data-row-action')).toBe(true);
  });

  /* v0.10.933 (tablo standardı T2) — açılmayan satır tıklanır GÖRÜNMEZ:
     rowActivation yalnız açılan satıra yayılır, satır içi koşullu cursor yok. */
  it('açılmayan satırlar işaret almaz (Databases / detailSections / ShapesView / LogPatternsPanel)', () => {
    const read = (p: string) => readFileSync(resolve(__dirname, '../../..', p), 'utf8');
    expect(read('pages/Databases.tsx')).toContain('{...(r.stmtHash ? rowActivation(() => openStmt(r)) : {})}');
    expect(read('pages/databases/detailSections.tsx')).toContain('{...(r.stmtHash ? rowActivation(() => onOpen(r)) : {})}');
    const shapes = read('components/traces/ShapesView.tsx');
    expect(shapes).toContain('data-row-action={r.exemplar ? true : undefined}');
    expect(shapes).toContain('{...(r.exemplar ? rowActivation(');
    const lp = read('components/LogPatternsPanel.tsx');
    expect(lp.split('{...(r.query ? rowActivation(() => onSearch(r.query)) : {})}').length - 1).toBe(2);
    for (const src of [read('pages/Databases.tsx'), read('pages/databases/detailSections.tsx'), shapes, lp]) {
      expect(src).not.toMatch(/cursor:\s*(?:r\.\w+|has)\s*\?/);
    }
  });

  /* v0.10.933 (tablo standardı T2) — paylaşılan satır hover'ı bg2; bg2
     zeminli diyalogda görünmez. Kanal seçici modal yüzeyi bg1. */
  it('ZoomChannelPicker diyalog yüzeyi bg1 (satır hover görünür)', () => {
    const src = readFileSync(resolve(__dirname, '../../../pages/settings/ZoomChannelPicker.tsx'), 'utf8');
    expect(src).toContain("background: 'var(--bg1)', border: '1px solid var(--border)'");
    expect(src).not.toContain("background: 'var(--bg2)'");
  });

  it('VirtualTable yalnız fareyle açılan (onRowClick) satırı işaretler', () => {
    // v0.10.933 — rowProps'un onOpen işareti burada KULLANILMAZ: onOpen yalnız
    // klavye (j/k, Enter) demek; fare işleyicisi yoksa el imleci yalan söyler.
    const src = readFileSync(resolve(__dirname, 'VirtualTable.tsx'), 'utf8');
    expect(src).toContain('data-row-action={onRowClick ? true : undefined}');
  });
});

// ── Elle tık işleyicili her <tr> işaret taşır ──────────────────────────────
const SRC = resolve(__dirname, '../../..');
function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}

/* v0.10.933 (tablo standardı T2) — `<tr … >` açılış etiketi süslü parantez
   derinliğiyle kesilir (`onClick={() => …}` içindeki `>` etiketi bitirmez);
   yürüyücü styles/jsxTags.ts'te, tableUnityRatchet trCursor ile ORTAK. */
const trTags = (s: string) => jsxOpenTags(s, 'tr');

describe('T2 — elle tık işleyicili satır işaretli', () => {
  it('onClick/onMouseDown/onDoubleClick/onAuxClick taşıyan her <tr> role=button ya da data-row-action taşır', () => {
    const HANDLER = /\bon(Click|MouseDown|DoubleClick|AuxClick)\s*=/;
    const MARKER = /role="button"|rowActivation\(|rowKeyboard\(|rowClickHandlers\(|data-row-action/;
    const bad: string[] = [];
    let seen = 0;
    for (const f of walk(SRC)) {
      for (const { tag, line } of trTags(readFileSync(f, 'utf8'))) {
        if (!HANDLER.test(tag)) continue;
        seen++;
        if (!MARKER.test(tag)) bad.push(`${relative(SRC, f)}:${line}`);
      }
    }
    expect(seen, 'tarayıcı hiçbir tıklanabilir satır bulamadı — kapı BAYAT').toBeGreaterThan(5);
    expect(bad, 'satır tıklanıyor ama imleç/hover almaz (globals.css T2)').toEqual([]);
  });
});

/* v0.10.933 (tablo standardı T2) — TERS yön: işaret → işleyici.
   İşaret kaynakları: `role="button"` / rowActivation( / rowKeyboard( (rol) ve
   `data-row-action` / rowClickHandlers( / onOpen'lı tablonun rowProps'u
   (eylem). Eylem işareti el imleci verir → FARE işleyicisi şart (onClick… /
   rowActivation( / rowClickHandlers(); yalnız rol işaretinde onKeyDown /
   rowKeyboard( da sayılır. rowProps'un tablosu dosyadaki
   `const <ad> = useDataTable(…)` çağrısından çözülür (doğrudan
   `<ad>.rowProps(` ya da `const rp = <ad>.rowProps(` / `const { …, ...rp } =
   <ad>.rowProps(` ile yayılan değişken); çağrı argümanında `onOpen` varsa
   tablo açılabilir. Prop olarak gelen dt (VirtualTable) çözülmez — o satır
   işaretini ve onClick'ini açıkça yazıyor. */
function callArgs(src: string, open: number): string {
  let d = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === '(') d++;
    else if (src[i] === ')' && --d === 0) return src.slice(open, i + 1);
  }
  return src.slice(open);
}
function tableHasOnOpen(src: string, dt: string): boolean {
  const m = new RegExp(`(?:const|let)\\s+${dt}\\s*=\\s*useDataTable\\s*(?:<[^(]*>)?\\s*\\(`).exec(src);
  return !!m && /\bonOpen\b/.test(callArgs(src, m.index + m[0].length - 1));
}
function openRowProps(src: string, tag: string): boolean {
  const tables = new Set<string>();
  for (const m of tag.matchAll(/(\w+)\.rowProps\(/g)) tables.add(m[1]);
  for (const m of tag.matchAll(/\{\.\.\.(\w+)\}/g)) {
    const v = m[1];
    const direct = new RegExp(`(?:const|let)\\s+${v}\\s*=\\s*(\\w+)\\.rowProps\\(`).exec(src);
    const rest = new RegExp(`(?:const|let)\\s*\\{[^}]*\\.\\.\\.${v}\\s*\\}\\s*=\\s*(\\w+)\\.rowProps\\(`).exec(src);
    if (direct) tables.add(direct[1]);
    if (rest) tables.add(rest[1]);
  }
  return [...tables].some(t => tableHasOnOpen(src, t));
}
const ROLE_MARK = /role="button"|rowActivation\(|rowKeyboard\(/;
const ACTION_MARK = /data-row-action|rowClickHandlers\(/;
const MOUSE = /\bon(Click|MouseDown|DoubleClick|AuxClick)\s*=|rowActivation\(|rowClickHandlers\(/;
const KEYS = /\bonKeyDown\s*=|rowKeyboard\(/;
/** İşaretli ama işleyicisiz satırlar (boş = temiz); `open` = rowProps onOpen'lı. */
function unhandledMarkedRows(src: string): { line: number; open: boolean }[] {
  const out: { line: number; open: boolean }[] = [];
  for (const { tag, line } of trTags(src)) {
    const open = openRowProps(src, tag);
    const action = open || ACTION_MARK.test(tag);
    if (!action && !ROLE_MARK.test(tag)) continue;
    const handled = action ? MOUSE.test(tag) : MOUSE.test(tag) || KEYS.test(tag);
    if (!handled) out.push({ line, open });
  }
  return out;
}

describe('T2 — işaretli satır işleyicili (ters yön)', () => {
  it('çözücü: onOpen\'lı rowProps + fare işleyicisi yok → yakalanır; rowClickHandlers ile → temiz', () => {
    const before = `const dt = useDataTable<Op>({ storageKey: 'x', onOpen: (op) => go(op) });
      rows.map((op, i) => { const rp = dt.rowProps(i); return (<tr key={op.name} {...rp} style={{ a: 1 }}><td /></tr>); })`;
    expect(unhandledMarkedRows(before)).toEqual([{ line: 2, open: true }]);
    const after = before.replace('{...rp} style', '{...rp} {...rowClickHandlers(href(op), () => go(op))} style');
    expect(unhandledMarkedRows(after)).toEqual([]);
    // onOpen'sız tablonun rowProps'u işaret basmaz → satır serbest.
    expect(unhandledMarkedRows(before.replace(", onOpen: (op) => go(op)", ''))).toEqual([]);
    // Elle role="button" + yalnız onKeyDown geçer; elle data-row-action + yalnız onKeyDown geçmez.
    expect(unhandledMarkedRows('<tr role="button" onKeyDown={k}>')).toEqual([]);
    expect(unhandledMarkedRows('<tr data-row-action onKeyDown={k}>')).toEqual([{ line: 1, open: false }]);
  });

  it('role=button / data-row-action / onOpen\'lı rowProps taşıyan her <tr> bir işleyici taşır', () => {
    const bad: string[] = [];
    let seen = 0;
    let openSeen = 0;
    for (const f of walk(SRC)) {
      const src = readFileSync(f, 'utf8');
      for (const { tag } of trTags(src)) {
        const open = openRowProps(src, tag);
        if (open) openSeen++;
        if (open || ACTION_MARK.test(tag) || ROLE_MARK.test(tag)) seen++;
      }
      for (const { line, open } of unhandledMarkedRows(src)) {
        bad.push(`${relative(SRC, f)}:${line}${open ? ' (onOpen\'lı rowProps)' : ''}`);
      }
    }
    expect(seen, 'tarayıcı hiçbir işaretli satır bulamadı — kapı BAYAT').toBeGreaterThan(20);
    expect(openSeen, 'rowProps çözücüsü onOpen\'lı tablo bulamadı — kapı BAYAT').toBeGreaterThan(5);
    expect(bad, 'satır tıklanabilir görünüyor (imleç/hover) ama işleyicisi yok').toEqual([]);
  });

  it('OperationsTable: işlem satırı onOpen\'lı rowProps taşır ve fare işleyicisi de taşır', () => {
    const f = resolve(SRC, 'pages/service/OperationsTable.tsx');
    const src = readFileSync(f, 'utf8');
    const rows = trTags(src).filter(t => openRowProps(src, t.tag));
    expect(rows.length).toBe(1);
    expect(rows[0].tag).toContain('rowClickHandlers(opHref(op.name), () => navigate(opHref(op.name)))');
    expect(unhandledMarkedRows(src)).toEqual([]);
  });
});
