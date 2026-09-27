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
//   • v0.10.967 — P-1 `detail`: sayfanın CTA'sı / bağlantısı (Link, Button)
//     empty / no-match / error satırında mesajdan SONRA, aynı tek <td>
//     içinde; başlık dokunulmaz; loading'de yok sayılır. detail kalkınca
//     (tür aynı kalsa da) odak geri döner; detail gelip giderken türün kendi
//     düğmesi (Retry / Filtreleri temizle) yeniden bağlanmaz, odağını korur;
//     `detailKey` değişince yalnız detail yuvası yeniden bağlanır.
//   • v0.10.967 — P-2 `colSpan`: statik tablo (dt yok) aynı satırı basar;
//     tek tam genişlik hücre, loading = kolon orantısız N tam genişlik
//     çizgi; empty / no-match / error gövdesi dt'li sürümle BİREBİR aynı,
//     odak dönüşü dahil. `dt` ile `colSpan`dan TAM OLARAK biri: ikisi de
//     yoksa ya da ikisi birden verilirse DERLEME hatası (tsc kapısı).
import { describe, it, expect, afterEach } from 'vitest';
import { act, useRef, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { Link, MemoryRouter } from 'react-router-dom';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableState, DATA_TABLE_STATE_TEXT,
  type ColumnDef, type DataTable, type DataTableStateKind,
} from './index';
import { Button } from '../Button';
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
/** Aynı kökte yeniden çiz: bileşen durumu (dt, ref'ler) korunur, yalnız prop'lar değişir. */
function rerender(node: ReactNode) {
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
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
  detail?: ReactNode;
  onClearFilters?: () => void;
  onRetry?: () => void;
  skeletonRows?: number;
}
// Tip kapısı testi (aşağıda) gerçek bir `dt` ister; son Probe'unkini tutar.
let lastDt: DataTable<Row> | null = null;
function Probe({ kind, rows = NO_ROWS, leading, trailing, ...rest }: ProbeProps) {
  const dt = useDataTable<Row>({ storageKey: 'dt-state-contract', columns: COLS, rows });
  lastDt = dt;
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

// ── v0.10.967 — P-1 `detail`: sayfanın CTA'sı / bağlantısı satırın İÇİNDE ──
// Dilim 4'te `action=` CTA'lı, <Link>'li ya da kopyalanan komutlu boş/hata
// durumları "blocked" kaldı (recipe §5 P-1): CTA'yı silmek de tablonun dışına
// park etmek de yasaktı. `detail` onları durum satırının içine, türün kendi
// içeriğinin (mesaj + Filtreleri temizle / ↻ Retry) arkasına alır.
const DETAIL_KINDS = ['empty', 'no-match', 'error'] as const;
const KIND_TEXT: Record<(typeof DETAIL_KINDS)[number], string> = {
  empty: DATA_TABLE_STATE_TEXT.empty,
  'no-match': DATA_TABLE_STATE_TEXT.noMatch,
  error: DATA_TABLE_STATE_TEXT.error,
};
const detailOf = (td: Element) => td.querySelector<HTMLElement>('[data-dt-state-detail]');
const controls = (td: Element) =>
  [...td.querySelectorAll('.dt-state-body button, .dt-state-body a')].map(e => e.textContent);

function Cta({ onAdd }: { onAdd?: () => void }) {
  return (
    <>
      <Link to="/settings/ingest">Kurulum rehberi</Link>
      <Button variant="secondary" size="sm" onClick={onAdd}>Kural ekle</Button>
    </>
  );
}

describe('DataTableState — detail (P-1): Link ve Button tek hücrenin içinde', () => {
  it.each(DETAIL_KINDS)('%s: Link + Button TEK <td> içinde, mesajdan SONRA; satır tıklanmaz', kind => {
    let n = 0;
    const el = render(<Probe kind={kind} detail={<Cta onAdd={() => n++} />} />);
    const td = stateCell(el);
    const body = td.querySelector('.dt-state-body')!;
    const detail = detailOf(td)!;
    expect(detail, 'detail yuvası yok').not.toBeNull();
    expect(detail.parentElement, 'detail .dt-state-body içinde').toBe(body);
    expect(body.lastElementChild, 'detail gövdenin son öğesi').toBe(detail);
    const text = td.textContent!;
    expect(text.indexOf(KIND_TEXT[kind])).toBeGreaterThanOrEqual(0);
    expect(text.indexOf(KIND_TEXT[kind]), 'mesaj önde').toBeLessThan(text.indexOf('Kurulum rehberi'));
    const a = detail.querySelector('a')!;
    expect(a.getAttribute('href')).toBe('/settings/ingest');
    const add = [...detail.querySelectorAll('button')].find(b => b.textContent === 'Kural ekle')!;
    expect(add).toBeTruthy();
    act(() => { add.click(); });
    expect(n).toBe(1);
    expect(el.querySelectorAll('tbody a').length, 'bağlantı tek, tbody içinde').toBe(1);
    const tr = bodyRows(el)[0];
    expect(tr.hasAttribute('role')).toBe(false);
    expect(tr.hasAttribute('data-row-action')).toBe(false);
  });

  it('başlık dokunulmaz: detail\'li her türde thead veri varkenkiyle aynı', () => {
    const withData = render(<Probe rows={ROWS} />);
    const headData = withData.querySelector('thead')!.outerHTML;
    act(() => { root?.unmount(); });
    host?.remove();
    for (const kind of DETAIL_KINDS) {
      const el = render(<Probe kind={kind} detail={<Cta />} />);
      expect(el.querySelector('thead')!.outerHTML, `${kind}: thead değişti`).toBe(headData);
      expect(el.querySelector('thead a, thead [data-dt-state-detail]')).toBeNull();
      act(() => { root?.unmount(); });
      host?.remove();
    }
  });

  it('no-match: "Filtreleri temizle" mesajın yanında kalır, detail en sonda', () => {
    let cleared = 0;
    const td = stateCell(render(<Probe kind="no-match" onClearFilters={() => cleared++} detail={<Cta />} />));
    expect(controls(td)).toEqual([DATA_TABLE_STATE_TEXT.clearFilters, 'Kurulum rehberi', 'Kural ekle']);
    act(() => { td.querySelector<HTMLButtonElement>('button.btn-link')!.click(); });
    expect(cleared).toBe(1);
  });

  it('error: QueryErrorInline (sunucu metni + ↻ Retry) önde, detail arkada', () => {
    let retried = 0;
    const td = stateCell(render(
      <Probe kind="error" message="timeout after 30s" onRetry={() => retried++} detail={<Cta />} />,
    ));
    expect(controls(td)).toEqual(['↻ Retry', 'Kurulum rehberi', 'Kural ekle']);
    expect(td.textContent).toContain('timeout after 30s');
    act(() => { td.querySelector<HTMLButtonElement>('.dt-state-body button')!.click(); });
    expect(retried).toBe(1);
  });

  it('loading: detail yok sayılır (iskelet CTA ya da metin taşımaz)', () => {
    const td = stateCell(render(<Probe kind="loading" detail={<Cta />} />));
    expect(detailOf(td)).toBeNull();
    expect(td.querySelector('a, button')).toBeNull();
    expect(td.textContent).toBe('');
  });

  it.each([false, null, ''])('detail=%j: boş yuva basılmaz (DOM detail\'siz sürümle aynı)', falsy => {
    const plain = stateCell(render(<Probe kind="empty" />)).innerHTML;
    act(() => { root?.unmount(); });
    host?.remove();
    const td = stateCell(render(<Probe kind="empty" detail={falsy} />));
    expect(detailOf(td)).toBeNull();
    expect(td.innerHTML).toBe(plain);
  });

  it('detail düz metin de olabilir (tek cümlelik öneri)', () => {
    const td = stateCell(render(<Probe kind="empty" detail="Pencereyi 1 saate genişletin." />));
    expect(detailOf(td)!.textContent).toBe('Pencereyi 1 saate genişletin.');
  });
});

// Odak: detail'deki düğme kendi kendini kaldıran bir eylem olabilir ("Pencereyi
// genişlet" → yeniden sorgu → loading; ya da tür aynı kalır, öneri kalkar).
// Detail kendi yuvasında (DetailSlot) StateBody'nin bekçisini taşır — tür aynı
// kalsa da detail giderse yuvanın temizliği odağı yakalar.
type AfterDetail = 'drop-detail' | DataTableStateKind | null;
function LiveDetail({ after, returnFocus = false }: { after: AfterDetail; returnFocus?: boolean }) {
  const [kind, setKind] = useState<DataTableStateKind | null>('empty');
  const [withDetail, setWithDetail] = useState(true);
  const searchRef = useRef<HTMLInputElement>(null);
  const dt = useDataTable<Row>({ storageKey: 'dt-state-focus-detail', columns: COLS, rows: kind ? NO_ROWS : ROWS });
  const onWiden = () => { if (after === 'drop-detail') setWithDetail(false); else setKind(after); };
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
                  detail={withDetail && <Button variant="secondary" size="sm" onClick={onWiden}>Pencereyi genişlet</Button>}
                  returnFocusRef={returnFocus ? searchRef : undefined} />
              : dt.sortedRows.map((r, i) => <tr key={r.id} {...dt.rowProps(i)}><td>{r.name}</td><td>{r.n}</td><td /></tr>)}
          </tbody>
        </table>
      </div>
    </>
  );
}
const detailBtn = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('tbody [data-dt-state-detail] button');

describe('DataTableState — detail kalkınca odak <body>\'ye düşmez', () => {
  it('tür AYNI kalır, yalnız detail kalkar: odak tablonun kabına (detail yuvasının bekçisi)', () => {
    const el = render(<LiveDetail after="drop-detail" />);
    press(detailBtn(el)!);
    expect(el.querySelector('[data-dt-state]')!.getAttribute('data-dt-state')).toBe('empty');
    expect(detailBtn(el)).toBeNull();
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(wrapOf(el));
  });

  it.each([['loading'], [null]] as const)('detail düğmesi → %s: odak tablonun kabına', after => {
    const el = render(<LiveDetail after={after} />);
    press(detailBtn(el)!);
    expect(document.activeElement).toBe(wrapOf(el));
  });

  it('returnFocusRef: sayfa hedefi tercih edilir', () => {
    const el = render(<LiveDetail after="drop-detail" returnFocus />);
    press(detailBtn(el)!);
    expect(document.activeElement).toBe(el.querySelector('input[aria-label="Filtre"]'));
    expect(wrapOf(el).hasAttribute('tabindex')).toBe(false);
  });

  it('odak dışarıdayken detail kalkarsa odağa dokunulmaz', () => {
    const el = render(<LiveDetail after="drop-detail" />);
    const input = el.querySelector<HTMLInputElement>('input[aria-label="Filtre"]')!;
    act(() => { input.focus(); });
    act(() => { detailBtn(el)!.click(); });
    expect(detailBtn(el)).toBeNull();
    expect(document.activeElement).toBe(input);
    expect(wrapOf(el).hasAttribute('tabindex')).toBe(false);
  });
});

// v0.10.967 — detail'in gelip gitmesi türün KENDİ düğmesini ("↻ Retry" /
// "Filtreleri temizle") yeniden bağlamaz. Detail'in varlığı gövde anahtarına
// girdiğinde gövde bütünüyle yeniden bağlanıyordu: hâlâ çizilen Retry'dan
// odak kaba, "Filtreleri temizle"den filtre kutusuna kaçıyordu (sonraki tuş
// filtreyi düzenler). Detail artık kendi odak bekçisi olan ayrı yuvada;
// gövde anahtarı yine `tür:eylem-var-mı`. Tetikleyici gerçek: detail başka
// bir sorguya ya da yetkiye bağlı (`detail={canEdit && <Button/>}`).
function Toggle({ kind, detail, detailKey, returnFocus = false }: {
  kind: DataTableStateKind;
  detail?: ReactNode;
  detailKey?: string;
  returnFocus?: boolean;
}) {
  const searchRef = useRef<HTMLInputElement>(null);
  const dt = useDataTable<Row>({ storageKey: 'dt-state-detail-toggle', columns: COLS, rows: NO_ROWS });
  return (
    <>
      <input aria-label="Filtre" ref={searchRef} />
      <div className="table-wrap">
        <table>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            <DataTableState dt={dt} kind={kind} detail={detail} detailKey={detailKey}
              onClearFilters={() => {}} onRetry={() => {}}
              returnFocusRef={returnFocus ? searchRef : undefined} />
          </tbody>
        </table>
      </div>
    </>
  );
}
const ownBtn = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll<HTMLButtonElement>('tbody .dt-state-body button')].find(b => b.textContent === text)!;
const Suggest = () => <Button variant="secondary" size="sm">Anahtarı uygula</Button>;

describe('DataTableState — detail gelip gidince türün kendi düğmesi odağını korur', () => {
  it('error: odak "↻ Retry"da, detail GELİR → odak aynı Retry düğmesinde', () => {
    const el = render(<Toggle kind="error" />);
    const retry = ownBtn(el, '↻ Retry');
    act(() => { retry.focus(); });
    rerender(<Toggle kind="error" detail={<Suggest />} />);
    expect(detailOf(stateCell(el)), 'detail geldi').not.toBeNull();
    expect(retry.isConnected, 'Retry yeniden bağlanmadı').toBe(true);
    expect(document.activeElement).toBe(retry);
  });

  it('no-match + returnFocusRef: odak "Filtreleri temizle"de, detail GELİR → odak filtre kutusuna KAÇMAZ', () => {
    const el = render(<Toggle kind="no-match" returnFocus />);
    const clear = ownBtn(el, DATA_TABLE_STATE_TEXT.clearFilters);
    act(() => { clear.focus(); });
    rerender(<Toggle kind="no-match" returnFocus detail={<Suggest />} />);
    expect(detailOf(stateCell(el))).not.toBeNull();
    expect(clear.isConnected).toBe(true);
    expect(document.activeElement).toBe(clear);
    expect(document.activeElement).not.toBe(el.querySelector('input[aria-label="Filtre"]'));
  });

  it('error: odak "↻ Retry"da, detail (Button) → false → odak aynı Retry düğmesinde', () => {
    const el = render(<Toggle kind="error" detail={<Suggest />} />);
    const retry = ownBtn(el, '↻ Retry');
    act(() => { retry.focus(); });
    rerender(<Toggle kind="error" detail={false} />);
    expect(detailOf(stateCell(el)), 'detail gitti').toBeNull();
    expect(retry.isConnected).toBe(true);
    expect(document.activeElement).toBe(retry);
    expect(wrapOf(el).hasAttribute('tabindex'), 'kap odak için işaretlenmedi').toBe(false);
  });
});

// v0.10.967 — `detailKey`: detail YERİNDE kalırken içindeki odaklı düğme
// kalkarsa (TracesEmpty'de "pencereyi genişlet" gider, diğer teşhisler durur;
// keepPreviousData ile tür de aynı kalır) yuvanın bekçisi bunu göremez —
// yuva bağlı kalır. Sayfa detail'in EYLEM KÜMESİNİ `detailKey`de söyler;
// anahtar değişince yuva yeniden bağlanır ve odak aynı kuralla geri döner.
const Hint = ({ widen }: { widen: boolean }) => (
  <>
    <span>İpucu</span>
    {widen && <Button variant="secondary" size="sm">Genişlet</Button>}
  </>
);

describe('DataTableState — detailKey: detail kalırken içindeki odaklı düğme kalkarsa', () => {
  it('detailKey değişir: odak tablonun kabına (body\'ye değil)', () => {
    const el = render(<Toggle kind="empty" detail={<Hint widen />} detailKey="widen" />);
    const widen = detailBtn(el)!;
    act(() => { widen.focus(); });
    rerender(<Toggle kind="empty" detail={<Hint widen={false} />} detailKey="hint" />);
    expect(detailOf(stateCell(el))!.textContent, 'detail yerinde, düğmesiz').toBe('İpucu');
    expect(widen.isConnected).toBe(false);
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(wrapOf(el));
  });

  it('detailKey değişir + returnFocusRef: sayfa hedefi tercih edilir', () => {
    const el = render(<Toggle kind="empty" returnFocus detail={<Hint widen />} detailKey="widen" />);
    act(() => { detailBtn(el)!.focus(); });
    rerender(<Toggle kind="empty" returnFocus detail={<Hint widen={false} />} detailKey="hint" />);
    expect(document.activeElement).toBe(el.querySelector('input[aria-label="Filtre"]'));
    expect(wrapOf(el).hasAttribute('tabindex')).toBe(false);
  });

  it('detailKey AYNI kalır, detail metni değişir: odaklı düğme yeniden bağlanmaz', () => {
    const el = render(<Toggle kind="empty" detail={<><span>3 öneri</span><Suggest /></>} detailKey="suggest" />);
    const btn = detailBtn(el)!;
    act(() => { btn.focus(); });
    rerender(<Toggle kind="empty" detail={<><span>2 öneri</span><Suggest /></>} detailKey="suggest" />);
    expect(detailOf(stateCell(el))!.textContent).toContain('2 öneri');
    expect(btn.isConnected).toBe(true);
    expect(document.activeElement).toBe(btn);
  });

  it('detailKey değişir ama odak dışarıda: odağa dokunulmaz', () => {
    const el = render(<Toggle kind="empty" detail={<Hint widen />} detailKey="widen" />);
    const input = el.querySelector<HTMLInputElement>('input[aria-label="Filtre"]')!;
    act(() => { input.focus(); });
    rerender(<Toggle kind="empty" detail={<Hint widen={false} />} detailKey="hint" />);
    expect(document.activeElement).toBe(input);
    expect(wrapOf(el).hasAttribute('tabindex')).toBe(false);
  });

  it('detailKey türün kendi düğmesini yeniden bağlamaz (yalnız detail yuvası)', () => {
    const el = render(<Toggle kind="error" detail={<Hint widen />} detailKey="widen" />);
    const retry = ownBtn(el, '↻ Retry');
    act(() => { retry.focus(); });
    rerender(<Toggle kind="error" detail={<Hint widen={false} />} detailKey="hint" />);
    expect(retry.isConnected).toBe(true);
    expect(document.activeElement).toBe(retry);
  });
});

// ── v0.10.967 — P-2 `colSpan`: statik tablo (dt yok) ──────────────────────
// Statik tablo (≤10 sabit satır, düzenlenebilir liste, seçici) `useDataTable`
// kullanmaz; colgroup orantısı yoktur. Sayfa thead'deki <th> sayısını verir.
// empty / no-match / error dt'li sürümle AYNI gövde (odak dönüşü dahil);
// loading kolon orantısız N tam genişlik çizgi.
const STATIC_COLS = 3;
function StaticHead() {
  return <thead><tr><th>Ad</th><th>Çağrı</th><th>Not</th></tr></thead>;
}
function StaticProbe({ kind, rows = NO_ROWS, ...rest }: Omit<ProbeProps, 'leading' | 'trailing'>) {
  return (
    <div className="table-wrap">
      <table>
        <StaticHead />
        <tbody>
          {kind
            ? <DataTableState colSpan={STATIC_COLS} kind={kind} {...rest} />
            : rows.map(r => <tr key={r.id}><td>{r.name}</td><td>{r.n}</td><td /></tr>)}
        </tbody>
      </table>
    </div>
  );
}

describe('DataTableState colSpan (P-2) — statik tabloda tek satır, tek tam genişlik hücre', () => {
  it.each(KINDS)('%s: TEK <tr> + TEK <td colSpan = verilen sayı>; satır tıklanmaz', kind => {
    const el = render(<StaticProbe kind={kind} />);
    expect(stateCell(el).colSpan).toBe(STATIC_COLS);
    const tr = bodyRows(el)[0];
    expect(tr.getAttribute('data-dt-state')).toBe(kind);
    expect(tr.hasAttribute('role')).toBe(false);
    expect(tr.hasAttribute('data-row-action')).toBe(false);
    expect(tr.className).toBe('');
  });

  it('başlık dokunulmaz: thead her türde veri varkenkiyle aynı', () => {
    const headData = render(<StaticProbe rows={ROWS} />).querySelector('thead')!.outerHTML;
    act(() => { root?.unmount(); });
    host?.remove();
    for (const kind of KINDS) {
      const el = render(<StaticProbe kind={kind} detail={<Cta />} />);
      expect(el.querySelector('thead')!.outerHTML, `${kind}: thead değişti`).toBe(headData);
      expect(el.querySelector('thead [data-dt-state]')).toBeNull();
      act(() => { root?.unmount(); });
      host?.remove();
    }
  });

  it('loading: role=status + aria-busy, varsayılan 8 çizgi, her çizgi TEK tam genişlik hücre (kolon orantısı yok)', () => {
    const td = stateCell(render(<StaticProbe kind="loading" />));
    expect(td.className).toBe('dt-state dt-state--loading');
    const status = td.querySelector('[role="status"]')!;
    expect(status.getAttribute('aria-busy')).toBe('true');
    expect(status.getAttribute('aria-label')).toBe(DATA_TABLE_STATE_TEXT.loading);
    const lines = [...td.querySelectorAll('.dt-state-skel')];
    expect(lines.length).toBe(8);
    for (const line of lines) {
      expect(line.getAttribute('aria-hidden')).toBe('true');
      const cells = [...line.querySelectorAll<HTMLElement>(':scope > .dt-state-skel-cell')];
      expect(cells.length, 'çizgi başına tek hücre').toBe(1);
      expect(cells[0].style.flex).toBe('1 1 0px');
      expect(cells[0].classList.contains('dt-state-skel-cell--num')).toBe(false);
      const bar = cells[0].querySelector<HTMLElement>('.skeleton-pulse')!;
      expect(bar).not.toBeNull();
      expect(bar.style.width, 'çubuk tam genişlik').toBe('100%');
    }
    expect(td.textContent).toBe('');
  });

  it('loading: skeletonRows ve message (durum etiketi) geçer; detail yok sayılır', () => {
    const td = stateCell(render(
      <StaticProbe kind="loading" skeletonRows={3} message="Kanallar yükleniyor" detail={<Cta />} />,
    ));
    expect(td.querySelectorAll('.dt-state-skel').length).toBe(3);
    expect(td.querySelector('[role="status"]')!.getAttribute('aria-label')).toBe('Kanallar yükleniyor');
    expect(td.querySelector('a, button')).toBeNull();
  });

  it.each([
    ['empty', {}],
    ['empty', { message: 'Henüz link yok', detail: <Cta /> }],
    ['no-match', {}],
    ['no-match', { onClearFilters: () => {}, detail: <Cta /> }],
    ['error', {}],
    ['error', { message: 'timeout after 30s', onRetry: () => {}, detail: <Cta /> }],
  ] as const)('%s %#: hücre dt\'li sürümle BİREBİR aynı (sınıf + gövde)', (kind, extra) => {
    const withDt = stateCell(render(<Probe kind={kind} {...extra} />));
    const dtHtml = withDt.outerHTML.replace(/ colspan="\d+"/, '');
    act(() => { root?.unmount(); });
    host?.remove();
    const td = stateCell(render(<StaticProbe kind={kind} {...extra} />));
    expect(td.colSpan).toBe(STATIC_COLS);
    expect(td.outerHTML.replace(/ colspan="\d+"/, '')).toBe(dtHtml);
  });

  it('no-match "Filtreleri temizle" ve error "↻ Retry" çağırır', () => {
    let cleared = 0;
    const nm = stateCell(render(<StaticProbe kind="no-match" onClearFilters={() => cleared++} />));
    act(() => { nm.querySelector<HTMLButtonElement>('button')!.click(); });
    expect(cleared).toBe(1);
    act(() => { root?.unmount(); });
    host?.remove();
    let retried = 0;
    const er = stateCell(render(<StaticProbe kind="error" onRetry={() => retried++} />));
    act(() => { er.querySelector<HTMLButtonElement>('button')!.click(); });
    expect(retried).toBe(1);
  });
});

function StaticLive({ initial, afterClear = null, returnFocus = false, bare = false }: {
  initial: DataTableStateKind;
  afterClear?: DataTableStateKind | null;
  returnFocus?: boolean;
  /** Kapsız statik tablo (çekmece/kart içi): iniş hedefi <table>. */
  bare?: boolean;
}) {
  const [kind, setKind] = useState<DataTableStateKind | null>(initial);
  const searchRef = useRef<HTMLInputElement>(null);
  const table = (
    <table>
      <StaticHead />
      <tbody>
        {kind
          ? <DataTableState colSpan={STATIC_COLS} kind={kind}
              onClearFilters={() => setKind(afterClear)}
              onRetry={() => setKind('loading')}
              returnFocusRef={returnFocus ? searchRef : undefined} />
          : ROWS.map(r => <tr key={r.id}><td>{r.name}</td><td>{r.n}</td><td /></tr>)}
      </tbody>
    </table>
  );
  return (
    <>
      <input aria-label="Filtre" ref={searchRef} />
      {bare ? table : <div className="table-wrap">{table}</div>}
    </>
  );
}

describe('DataTableState colSpan (P-2) — odak dönüşü dt\'li sürümle aynı', () => {
  it('no-match → "Filtreleri temizle" → satırlar geldi: odak tablonun kabına', () => {
    const el = render(<StaticLive initial="no-match" />);
    press(actionBtn(el));
    expect(el.querySelector('[data-dt-state]')).toBeNull();
    expect(document.activeElement).toBe(wrapOf(el));
    expect(wrapOf(el).tabIndex).toBe(-1);
  });

  it('no-match → "Filtreleri temizle" → yine boş (empty): odak kapta', () => {
    const el = render(<StaticLive initial="no-match" afterClear="empty" />);
    press(actionBtn(el));
    expect(el.querySelector('[data-dt-state]')!.getAttribute('data-dt-state')).toBe('empty');
    expect(document.activeElement).toBe(wrapOf(el));
  });

  it('error → "↻ Retry" → loading: odak tablonun kabına', () => {
    const el = render(<StaticLive initial="error" />);
    press(actionBtn(el));
    expect(el.querySelector('[data-dt-state]')!.getAttribute('data-dt-state')).toBe('loading');
    expect(document.activeElement).toBe(wrapOf(el));
  });

  it.each(['no-match', 'error'] as const)('%s + returnFocusRef: sayfa hedefi tercih edilir', kind => {
    const el = render(<StaticLive initial={kind} returnFocus />);
    press(actionBtn(el));
    expect(document.activeElement).toBe(el.querySelector('input[aria-label="Filtre"]'));
  });

  it('kapsız statik tablo: odak <table>\'a iner (geçici tabIndex=-1)', () => {
    const el = render(<StaticLive initial="no-match" bare />);
    press(actionBtn(el));
    const table = el.querySelector('table')!;
    expect(document.activeElement).toBe(table);
    expect(table.tabIndex).toBe(-1);
  });
});

// ── v0.10.967 — tip kapısı: `dt` ile `colSpan`dan TAM OLARAK biri ─────────
// Button.contract (M3) emsali: kapı TİP DÜZEYİNDE ve iki yönlü. Aşağıdaki
// `@ts-expect-error`lar bugün birer hatayı bastırıyor; biri tipi gevşetirse
// (ör. ikisini de opsiyonel yaparsa) satır derlenir ve `tsc --noEmit`
// "unused '@ts-expect-error' directive" diye KENDİSİ kırılır.
function typeGate(dt: DataTable<Row>) {
  return {
    // @ts-expect-error — ne dt ne colSpan: satır kaç kolon kaplayacağını bilemez
    neither: <DataTableState kind="empty" />,
    // @ts-expect-error — ikisi birden: colSpan dt'nin görünür kolon sayımıyla yarışır
    both: <DataTableState dt={dt} colSpan={5} kind="empty" />,
    // @ts-expect-error — statik tabloda leading/trailing yok (colSpan zaten toplam)
    staticLeading: <DataTableState colSpan={3} kind="empty" leading={[28]} />,
    // Pozitif kontroller: tek başına dt / tek başına colSpan derlenir; detail
    // (+ detailKey) ikisinde de.
    withDt: <DataTableState dt={dt} kind="empty" detail={<Cta />} detailKey="cta" />,
    withColSpan: <DataTableState colSpan={3} kind="no-match" detail="x" detailKey="x" onClearFilters={() => {}} />,
  };
}

describe('DataTableState — tip kapısı (tsc): dt ile colSpan\'dan TAM OLARAK biri', () => {
  it('pozitif kontroller derlenir ve çizilir; ihlaller @ts-expect-error ile çivili', () => {
    render(<Probe kind="empty" />);
    const gate = typeGate(lastDt!);
    act(() => { root?.unmount(); });
    host?.remove();
    const el = render(<table><tbody>{gate.withColSpan}</tbody></table>);
    expect(stateCell(el).colSpan).toBe(3);
    expect(detailOf(stateCell(el))!.textContent).toBe('x');
  });
});
