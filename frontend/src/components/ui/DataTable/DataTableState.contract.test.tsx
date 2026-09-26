// @vitest-environment jsdom
//
// DataTableState.contract.test.tsx — v0.10.939 (tablo standardı T12).
//
// jsdom sözleşme testi, @testing-library YOK (DataTable.contract emsali).
// Çivilenen: DOM sözleşmesi, görünüm değil.
//   • her tür TEK <tr> + TEK <td>; colSpan = leading + GÖRÜNÜR kolon + trailing
//     (headerHidden düşer — VirtualTable boş satırıyla aynı sayım)
//   • başlık dokunulmaz: durum satırı thead'e girmez, th'ler veri varkenkiyle aynı
//   • satır tıklanmaz (T2): role / data-row-action işareti yok
//   • tür başına metin + rol: empty / no-match (+ Filtreleri temizle) /
//     loading (role=status + aria-busy, N iskelet çizgisi) / error
//     (QueryErrorInline, Retry)
//   • v0.10.939 (tablo standardı T12) — odak: "Filtreleri temizle" / "↻ Retry"
//     kendini kaldırınca (satırlar geldi, tür değişti) odak <body>'ye DÜŞMEZ;
//     `returnFocusRef` hedefine, yoksa tablonun kabına (.table-wrap) iner.
//     Odak başka yerdeyse dokunulmaz.
import { describe, it, expect, afterEach } from 'vitest';
import { act, useRef, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableState, DATA_TABLE_STATE_TEXT,
  type ColumnDef, type DataTableStateKind,
} from './index';
import { DEFAULT_W } from './rowHeight';

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

interface Row { id: string; name: string; n: number }
const ROWS: Row[] = [{ id: 'a', name: 'alpha', n: 3 }, { id: 'b', name: 'beta', n: 1 }];
const NO_ROWS: Row[] = [];
// Dört bildirilen kolon, ÜÇÜ görünür: `impact` headerHidden (yalnız sıralama
// boyutu) — colSpan bildirileni değil GÖRÜNENİ saymalı.
const COLS: ColumnDef<Row>[] = [
  { id: 'name', label: 'Name', sortValue: r => r.name, width: 160 },
  { id: 'n', label: 'Calls', sortValue: r => r.n, numeric: true, width: 80 },
  { id: 'impact', label: 'Impact', sortValue: r => r.n, headerHidden: true },
  { id: 'note', label: 'Note', flex: true },
];
const VISIBLE = 3;

interface ProbeProps {
  kind?: DataTableStateKind;
  rows?: Row[];
  leading?: number[];
  trailing?: number[];
  message?: string;
  onClearFilters?: () => void;
  onRetry?: () => void;
  skeletonRows?: number;
}
function Probe({ kind, rows = NO_ROWS, leading, trailing, ...rest }: ProbeProps) {
  const dt = useDataTable<Row>({ storageKey: 'dt-state-contract', columns: COLS, rows });
  return (
    <div className="table-wrap">
      <table>
        <DataTableColgroup dt={dt} leading={leading} trailing={trailing} />
        <DataTableHead dt={dt}
          leading={leading?.map((_, i) => <th key={`l${i}`} />)}
          trailing={trailing?.map((_, i) => <th key={`t${i}`} />)} />
        <tbody>
          {kind
            ? <DataTableState dt={dt} kind={kind} leading={leading} trailing={trailing} {...rest} />
            : dt.sortedRows.map((r, i) => <tr key={r.id} {...dt.rowProps(i)}><td>{r.name}</td><td>{r.n}</td><td /></tr>)}
        </tbody>
      </table>
    </div>
  );
}

const KINDS: DataTableStateKind[] = ['empty', 'no-match', 'loading', 'error'];
const bodyRows = (el: HTMLElement) => [...el.querySelectorAll('tbody > tr')];
const stateCell = (el: HTMLElement) => {
  const trs = bodyRows(el);
  expect(trs.length, 'durum TEK satır basar').toBe(1);
  const tds = trs[0].querySelectorAll(':scope > td');
  expect(tds.length, 'durum satırında TEK hücre').toBe(1);
  return tds[0] as HTMLTableCellElement;
};

describe('DataTableState — iskelet: tek satır, tek colSpan hücre', () => {
  it.each(KINDS)('%s: colSpan = görünür kolon sayısı (headerHidden düşer)', kind => {
    const el = render(<Probe kind={kind} />);
    expect(stateCell(el).colSpan).toBe(VISIBLE);
    expect(bodyRows(el)[0].getAttribute('data-dt-state')).toBe(kind);
  });

  it.each(KINDS)('%s: leading/trailing sayıma eklenir (colgroup ile aynı diziler)', kind => {
    const el = render(<Probe kind={kind} leading={[28, 36]} trailing={[44]} />);
    expect(stateCell(el).colSpan).toBe(2 + VISIBLE + 1);
    // colgroup ile aynı toplam — hücre tablonun tamamını kaplar.
    expect(el.querySelectorAll('colgroup > col').length).toBe(2 + VISIBLE + 1);
  });

  it.each(KINDS)('%s: satır tıklanır görünmez (T2 — işaret yok)', kind => {
    const tr = bodyRows(render(<Probe kind={kind} />))[0];
    expect(tr.hasAttribute('role')).toBe(false);
    expect(tr.hasAttribute('data-row-action')).toBe(false);
    expect(tr.className).toBe('');
  });

  it('başlık dokunulmaz: th listesi veri varkenkiyle aynı, durum satırı thead dışında', () => {
    const withData = render(<Probe rows={ROWS} />);
    const headData = withData.querySelector('thead')!.outerHTML;
    act(() => { root?.unmount(); });
    host?.remove();
    for (const kind of KINDS) {
      const el = render(<Probe kind={kind} />);
      expect(el.querySelector('thead')!.outerHTML, `${kind}: thead değişti`).toBe(headData);
      expect(el.querySelectorAll('thead tr').length).toBe(1);
      expect(el.querySelector('thead [data-dt-state]')).toBeNull();
      act(() => { root?.unmount(); });
      host?.remove();
    }
  });
});

describe('DataTableState — tür başına metin ve rol', () => {
  it('empty: "Bu aralıkta veri yok", düğme yok', () => {
    const td = stateCell(render(<Probe kind="empty" />));
    expect(td.textContent).toBe(DATA_TABLE_STATE_TEXT.empty);
    expect(DATA_TABLE_STATE_TEXT.empty).toBe('Bu aralıkta veri yok');
    expect(td.querySelector('button')).toBeNull();
    expect(td.className).toBe('dt-state');
  });

  it('empty: message varsayılanı ezer', () => {
    const td = stateCell(render(<Probe kind="empty" message="Henüz kullanıcı yok" />));
    expect(td.textContent).toBe('Henüz kullanıcı yok');
  });

  it('no-match: "Eşleşme yok"; onClearFilters yoksa kurtuluş düğmesi de yok', () => {
    const td = stateCell(render(<Probe kind="no-match" />));
    expect(td.textContent).toBe('Eşleşme yok');
    expect(td.querySelector('button')).toBeNull();
  });

  it('no-match + onClearFilters: "Filtreleri temizle" LinkButton\'u çağırır', () => {
    let n = 0;
    const td = stateCell(render(<Probe kind="no-match" onClearFilters={() => n++} />));
    const btn = td.querySelector('button')!;
    expect(btn).not.toBeNull();
    expect(btn.textContent).toBe('Filtreleri temizle');
    expect(btn.classList.contains('btn-link')).toBe(true); // LinkButton — gezinmez, <a> değil
    expect(btn.type).toBe('button');
    expect(td.querySelector('a')).toBeNull();
    act(() => { btn.click(); });
    expect(n).toBe(1);
    // "Eşleşme yok" ile "Bu aralıkta veri yok" AYRI metin (T12: üç ayrı metin).
    expect(td.textContent).toContain('Eşleşme yok');
    expect(td.textContent).not.toContain(DATA_TABLE_STATE_TEXT.empty);
  });

  it('loading: role=status + aria-busy, varsayılan 8 iskelet çizgisi, her çizgi kolon sayısı kadar hücre', () => {
    const td = stateCell(render(<Probe kind="loading" leading={[28]} />));
    expect(td.classList.contains('dt-state--loading')).toBe(true);
    const status = td.querySelector('[role="status"]')!;
    expect(status).not.toBeNull();
    expect(status.getAttribute('aria-busy')).toBe('true');
    expect(status.getAttribute('aria-label')).toBe(DATA_TABLE_STATE_TEXT.loading);
    const lines = [...td.querySelectorAll('.dt-state-skel')];
    expect(lines.length).toBe(8);
    for (const line of lines) {
      expect(line.getAttribute('aria-hidden'), 'iskelet süs — AT yalnız status etiketini duyar').toBe('true');
      const cells = [...line.querySelectorAll(':scope > .dt-state-skel-cell')];
      expect(cells.length).toBe(1 + VISIBLE);
      // Baş kolon (checkbox yuvası) çubuksuz; veri kolonları çubuklu.
      expect(cells[0].querySelector('.skeleton-pulse')).toBeNull();
      for (const c of cells.slice(1)) expect(c.querySelector('.skeleton-pulse')).not.toBeNull();
      // Sayısal kolon (Calls) sağa yaslı çubuk.
      expect(cells[2].classList.contains('dt-state-skel-cell--num')).toBe(true);
      expect(cells[1].classList.contains('dt-state-skel-cell--num')).toBe(false);
    }
    // Metin yok: yükleniyor "veri yok" diye okunmaz.
    expect(td.textContent).toBe('');
  });

  it('loading: skeletonRows sayısı ve colgroup orantısı (sabit px, flex kolon artanı emer)', () => {
    const td = stateCell(render(<Probe kind="loading" skeletonRows={3} />));
    const lines = td.querySelectorAll('.dt-state-skel');
    expect(lines.length).toBe(3);
    const flex = [...lines[0].querySelectorAll<HTMLElement>('.dt-state-skel-cell')].map(c => c.style.flex);
    // name 160px, Calls 80px sabit (flex kolon varken büyümez); Note flex → 1 1 0px.
    expect(flex).toEqual(['0 1 160px', '0 1 80px', '1 1 0px']);
  });

  it('loading: genişliği bildirilmemiş kolon colgroup ile AYNI varsayılanı alır (DEFAULT_W, rowHeight.ts — kopya yok)', () => {
    // v0.10.939 (tablo standardı T12) — iskelet sabiti eskiden elle kopyaydı
    // (SKEL_DEFAULT_W); iki değer ayrışırsa çizgi gerçek satır gelince kayar.
    const COLS2: ColumnDef<Row>[] = [
      { id: 'name', label: 'Name', sortValue: r => r.name },
      { id: 'n', label: 'Calls', sortValue: r => r.n, numeric: true, width: 80 },
    ];
    function NoWidth() {
      const dt = useDataTable<Row>({ storageKey: 'dt-state-default-w', columns: COLS2, rows: NO_ROWS });
      return (
        <table>
          <DataTableColgroup dt={dt} />
          <tbody><DataTableState dt={dt} kind="loading" skeletonRows={1} /></tbody>
        </table>
      );
    }
    const el = render(<NoWidth />);
    const flex = [...el.querySelectorAll<HTMLElement>('.dt-state-skel-cell')].map(c => c.style.flex);
    expect(flex).toEqual([`${DEFAULT_W} 1 ${DEFAULT_W}px`, '80 1 80px']);
    expect(el.querySelector<HTMLElement>('colgroup > col')!.style.width).toBe(`${DEFAULT_W}px`);
  });

  it('error: QueryErrorInline aynı yuvada, Türkçe varsayılan, Retry çağırır', () => {
    let n = 0;
    const td = stateCell(render(<Probe kind="error" onRetry={() => n++} />));
    expect(td.textContent).toContain(DATA_TABLE_STATE_TEXT.error);
    expect(td.textContent).toContain('bu bir hata, boş sonuç değil');
    expect(td.textContent).not.toContain(DATA_TABLE_STATE_TEXT.empty);
    const retry = td.querySelector('button')!;
    expect(retry).not.toBeNull();
    act(() => { retry.click(); });
    expect(n).toBe(1);
  });

  it('error: message sunucu metnini taşır; onRetry yoksa düğme yok', () => {
    const td = stateCell(render(<Probe kind="error" message="timeout after 30s" />));
    expect(td.textContent).toContain('timeout after 30s');
    expect(td.querySelector('button')).toBeNull();
  });
});

// ── v0.10.939 (tablo standardı T12) — odak geri dönüşü ──────────────────
// Canlı koşum: sayfa gibi davranır — "Filtreleri temizle" filtreyi siler
// (satırlar gelir ya da pencere yine boştur), "↻ Retry" yeniden sorgular
// (tür 'loading'e döner). Eylem düğmesi DOM'dan kalkar; odak kaybolmamalı.
function Live({ initial, afterClear = null, returnFocus = false }: {
  initial: DataTableStateKind;
  /** "Filtreleri temizle"den sonraki durum: null → satırlar geldi. */
  afterClear?: DataTableStateKind | null;
  returnFocus?: boolean;
}) {
  const [kind, setKind] = useState<DataTableStateKind | null>(initial);
  const searchRef = useRef<HTMLInputElement>(null);
  const dt = useDataTable<Row>({ storageKey: 'dt-state-focus', columns: COLS, rows: kind ? NO_ROWS : ROWS });
  return (
    <>
      <input aria-label="Filtre" ref={searchRef} />
      <div className="table-wrap">
        <table>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {kind
              ? <DataTableState dt={dt} kind={kind}
                  onClearFilters={() => setKind(afterClear)}
                  onRetry={() => setKind('loading')}
                  returnFocusRef={returnFocus ? searchRef : undefined} />
              : dt.sortedRows.map((r, i) => <tr key={r.id} {...dt.rowProps(i)}><td>{r.name}</td><td>{r.n}</td><td /></tr>)}
          </tbody>
        </table>
      </div>
    </>
  );
}

const press = (b: HTMLElement) => { act(() => { b.focus(); }); act(() => { b.click(); }); };
const actionBtn = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('tbody [data-dt-state] button')!;
const wrapOf = (el: HTMLElement) => el.querySelector<HTMLElement>('.table-wrap')!;

describe('DataTableState — eylem kendini kaldırınca odak <body>\'ye düşmez', () => {
  it('no-match → "Filtreleri temizle" → satırlar geldi: odak tablonun kabına', () => {
    const el = render(<Live initial="no-match" />);
    const wrap = wrapOf(el);
    expect(wrap.hasAttribute('tabindex')).toBe(false);
    press(actionBtn(el));
    expect(el.querySelector('[data-dt-state]'), 'durum satırı kalkmadı').toBeNull();
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(wrap);
    // Kap odak için GEÇİCİ olarak tabIndex=-1 alır; odak çıkınca geri alınır
    // (sayfanın sekme/tık davranışı kalıcı değişmez).
    expect(wrap.tabIndex).toBe(-1);
    act(() => { el.querySelector<HTMLInputElement>('input[aria-label="Filtre"]')!.focus(); });
    expect(wrap.hasAttribute('tabindex')).toBe(false);
  });

  it('no-match → "Filtreleri temizle" → yine boş (empty): düğme kalktı, odak kapta', () => {
    const el = render(<Live initial="no-match" afterClear="empty" />);
    press(actionBtn(el));
    expect(el.querySelector('[data-dt-state]')!.getAttribute('data-dt-state')).toBe('empty');
    expect(actionBtn(el)).toBeNull();
    expect(document.activeElement).toBe(wrapOf(el));
  });

  it('error → "↻ Retry" → loading: odak tablonun kabına', () => {
    const el = render(<Live initial="error" />);
    press(actionBtn(el));
    expect(el.querySelector('[data-dt-state]')!.getAttribute('data-dt-state')).toBe('loading');
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(wrapOf(el));
  });

  it.each(['no-match', 'error'] as const)('%s + returnFocusRef: sayfa hedefi tercih edilir', kind => {
    const el = render(<Live initial={kind} returnFocus />);
    press(actionBtn(el));
    expect(document.activeElement).toBe(el.querySelector('input[aria-label="Filtre"]'));
    expect(wrapOf(el).hasAttribute('tabindex'), 'hedef varken kap işaretlenmez').toBe(false);
  });

  it('kendi tabindex\'i olan kap: öznitelik korunur, odak çıkınca silinmez', () => {
    const el = render(<Live initial="no-match" />);
    const wrap = wrapOf(el);
    wrap.setAttribute('tabindex', '0');
    press(actionBtn(el));
    expect(document.activeElement).toBe(wrap);
    act(() => { el.querySelector<HTMLInputElement>('input[aria-label="Filtre"]')!.focus(); });
    expect(wrap.getAttribute('tabindex')).toBe('0');
  });

  it('odak durum satırının DIŞINDAYKEN satır kalkarsa odağa dokunulmaz', () => {
    const el = render(<Live initial="no-match" />);
    const input = el.querySelector<HTMLInputElement>('input[aria-label="Filtre"]')!;
    act(() => { input.focus(); });
    act(() => { actionBtn(el).click(); }); // fareyle: odak düğmeye gitmedi
    expect(el.querySelector('[data-dt-state]')).toBeNull();
    expect(document.activeElement).toBe(input);
    expect(wrapOf(el).hasAttribute('tabindex')).toBe(false);
  });
});
