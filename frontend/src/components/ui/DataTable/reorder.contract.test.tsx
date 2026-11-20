// @vitest-environment jsdom
//
// reorder.contract.test.tsx — v0.10.1516 (opt-in kolon sırası, /traces
// operatör isteği 2026-10-24). Pinlenen:
//   • OPT-IN: `reorderable` verilmeyen tablo bayt bayt eski (draggable yok,
//     dt.reorder null, ⋯ menüsünde sıra öğesi yok);
//   • sürükle-bırak columnModel.onChange'e yeni sırayı verir; renderLabel
//     içeriği (huni) başlığıyla taşınır;
//   • stickyLeft / actions sabit: sürüklenmez, hedef olmaz;
//   • genişlik imzası BİLDİRİLEN kolonlardan: sıra değişince sürüklenmiş
//     genişlik KALIR;
//   • boyut tutamağında başlayan basış sürükleme başlatmaz.
import { describe, it, expect, afterEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { useDataTable, DataTableHead, type DataTable, type ColumnDef, type ColumnModel } from './index';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  try { localStorage.clear(); } catch { /* jsdom --localstorage-file yok */ }
});

interface Row { id: string }
const COLS: ColumnDef<Row>[] = [
  { id: 'time', label: 'Time', width: 100, stickyLeft: true },
  { id: 'svc', label: 'Svc', width: 100, sortValue: r => r.id },
  { id: 'dur', label: 'Dur', width: 80 },
  { id: 'act', label: '', kind: 'actions', width: 40 },
];
let last: DataTable<Row> | null = null;
let changes: string[][] = [];
function Probe({ reorderable }: { reorderable: boolean }) {
  const [model, setModel] = useState<ColumnModel | null>(null);
  const dt = useDataTable<Row>({
    storageKey: 'reorder-contract', columns: COLS, rows: [],
    columnModel: { value: model, onChange: m => { changes.push(m.order); setModel(m); } },
    reorderable,
  });
  last = dt;
  return (
    <table>
      <DataTableHead dt={dt} renderLabel={c => <span className="lbl">{c.label}<button type="button" className="funnel" aria-label={`f-${c.id}`} /></span>} />
    </table>
  );
}
function mount(reorderable: boolean) {
  changes = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><Probe reorderable={reorderable} /></MemoryRouter>); });
}
const ths = () => [...host!.querySelectorAll('th')];
const ids = () => ths().map(th => th.querySelector('.funnel')?.getAttribute('aria-label')?.slice(2) ?? 'act');
const th = (id: string) => ths()[ids().indexOf(id)];
function dnd(type: string, el: Element) {
  const ev = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(ev, 'dataTransfer', { value: { setData() {}, effectAllowed: 'move', dropEffect: 'move' } });
  act(() => { el.dispatchEvent(ev); });
  return ev;
}

describe('useDataTable reorderable (opt-in)', () => {
  it('kapalıyken eski davranış: draggable yok, dt.reorder null', () => {
    mount(false);
    expect(last!.reorder).toBeNull();
    expect(host!.querySelector('th[draggable]')).toBeNull();
  });

  it('sürükle-bırak sırayı değiştirir; huni başlığıyla taşınır; genişlik kalır', () => {
    mount(true);
    act(() => { last!.resizeBy('dur', 20); });
    expect(last!.colWidths.dur).toBe(100);
    expect(th('time').getAttribute('draggable')).toBeNull();
    expect(th('act').getAttribute('draggable')).toBeNull();
    expect(th('dur').getAttribute('draggable')).toBe('true');
    expect(last!.reorder!.isDefault).toBe(true);
    dnd('dragstart', th('dur'));
    dnd('dragover', th('svc'));
    expect(th('svc').className).toContain('dt-drop-before');
    dnd('drop', th('svc'));
    expect(changes.at(-1)).toEqual(['time', 'dur', 'svc', 'act']);
    expect(ids()).toEqual(['time', 'dur', 'svc', 'act']);
    expect(ths()[1].querySelector('.funnel')!.getAttribute('aria-label')).toBe('f-dur');
    expect(last!.colWidths.dur).toBe(100);
    expect(last!.reorder!.isDefault).toBe(false);
    act(() => { last!.reorder!.reset(); });
    expect(ids()).toEqual(['time', 'svc', 'dur', 'act']);
    expect(last!.colWidths.dur).toBe(100);
  });

  it('sabit kolona bırakmak ve tutamaktan başlayan sürükleme yok sayılır', () => {
    mount(true);
    dnd('dragstart', th('svc'));
    const over = dnd('dragover', th('time'));
    expect(over.defaultPrevented).toBe(false);
    dnd('drop', th('time'));
    expect(changes).toEqual([]);
    const handle = th('dur').querySelector('.col-resize-handle')!;
    act(() => { handle.dispatchEvent(new Event('pointerdown', { bubbles: true })); });
    const start = dnd('dragstart', th('dur'));
    expect(start.defaultPrevented).toBe(true);
  });
});
