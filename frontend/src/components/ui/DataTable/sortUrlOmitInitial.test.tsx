// @vitest-environment jsdom
//
// sortUrlOmitInitial.test.tsx — v0.10.1267 — operatör 2026-10-17: Rollouts
// sunucu-tarafı sıralama (Namespace/Workload/Cluster). Primitifin iki yeni
// opt-in seçeneği:
//   - sortUrlKey: sıra `s_<storageKey>` yerine verilen parametrede (iki
//     storageKey'li tek mantıksal tablo aynı `?sort=`te buluşur);
//   - sortUrlOmitInitial (+ persistSort:false): varsayılan sıra URL'ye
//     yazılmaz; parametresiz URL'ye dönüş durumu varsayılana çeker.
// URL round-trip: başlık tıklaması → URL (replace) → durum; dış URL değişimi
// (geri/ileri, paylaşılan link) → durum; yabancı parametreler korunur.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import { useDataTable, DataTableHead, type DataTable, type ColumnDef } from './index';
import type { SortState } from '@/lib/dataTable';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Row { id: string; ns: string; started: number }
const COLS: ColumnDef<Row>[] = [
  { id: 'started', label: 'Başladı', sortValue: r => r.started, naturalDir: 'desc', width: 120 },
  { id: 'namespace', label: 'Namespace', sortValue: r => r.ns, naturalDir: 'asc', width: 120 },
  { id: 'note', label: 'Not', width: 120 },
];
const DEF: SortState = { id: 'started', dir: 'desc' };
const IDS = ['started', 'namespace'] as const;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let a: DataTable<Row> | null = null;
let b: DataTable<Row> | null = null;
let loc = '';
let nav: ((to: string) => void) | null = null;

function Probe() {
  const opts = { serverSort: true, initialSort: DEF, persistSort: false, sortIds: IDS, sortUrlKey: 'sort', sortUrlOmitInitial: true } as const;
  a = useDataTable<Row>({ storageKey: 'omit-a', columns: COLS, rows: [], ...opts });
  b = useDataTable<Row>({ storageKey: 'omit-b', columns: COLS, rows: [], ...opts });
  const n = useNavigate();
  nav = (to: string) => n(to, { replace: true });
  return <table><DataTableHead dt={a} /><tbody /></table>;
}

// MemoryRouter: konumu bir izleyici bileşenle oku.
function LocSpy() { const l = useLocation(); loc = l.search; return null; }

function render(url: string) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter initialEntries={[url]}><Probe /><LocSpy /></MemoryRouter>); });
}
const q = () => new URLSearchParams(loc);
const clickHeader = (label: string) => {
  const th = [...host!.querySelectorAll('th')].find(t => t.textContent?.includes(label));
  act(() => { th!.click(); });
};

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null; a = null; b = null; nav = null; loc = '';
});

describe('sortUrlKey + sortUrlOmitInitial', () => {
  it('parametresiz URL → varsayılan, URL temiz kalır', () => {
    render('/rollouts?range=6h');
    expect(a!.sort).toEqual(DEF);
    expect(q().get('sort')).toBeNull();
    expect(q().has('s_omit-a')).toBe(false);
  });

  it('başlık tıklaması → ?sort=namespace.asc (paylaşılan parametre), ikinci kanca da izler', () => {
    render('/rollouts?range=6h');
    clickHeader('Namespace');
    expect(q().get('sort')).toBe('namespace.asc');
    expect(q().get('range')).toBe('6h'); // yabancı parametre korunur
    expect(a!.sort).toEqual({ id: 'namespace', dir: 'asc' });
    expect(b!.sort).toEqual({ id: 'namespace', dir: 'asc' });
    clickHeader('Namespace');
    expect(q().get('sort')).toBe('namespace.desc');
  });

  it('varsayılana dönüş parametreyi SİLER', () => {
    render('/rollouts?sort=namespace.asc');
    expect(a!.sort).toEqual({ id: 'namespace', dir: 'asc' });
    clickHeader('Başladı');
    expect(a!.sort).toEqual(DEF);
    expect(q().get('sort')).toBeNull();
  });

  it('dış URL değişimi durumu sürer; parametre kalkınca varsayılana döner', () => {
    render('/rollouts');
    act(() => { nav!('/rollouts?sort=namespace.desc'); });
    expect(a!.sort).toEqual({ id: 'namespace', dir: 'desc' });
    act(() => { nav!('/rollouts'); });
    expect(a!.sort).toEqual(DEF);
  });

  it('sıralanamaz kolon ve tanınmayan kimlik yok sayılır', () => {
    render('/rollouts?sort=note.asc');
    expect(a!.sort).toEqual(DEF);
    clickHeader('Not');
    expect(a!.sort).toEqual(DEF);
  });
});
