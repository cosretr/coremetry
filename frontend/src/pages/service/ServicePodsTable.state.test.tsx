// @vitest-environment jsdom
// ServicePodsTable.state — v0.10.967 (tablo standardı T12, tarif P6).
//
// NE ÇİVİLİYOR: başlığı basan bileşen durum satırını da basar (`state` prop,
// VirtualTable emsali): satır yokken tek `tr[data-dt-state]`, colSpan görünür
// kolonlardan, başlık yerinde; verilmezse varsayılan "empty"; satır varken
// durum satırı yok. Hata türü `detail` (CTA / bağlantı) ile aynı hücrede.
//
// v0.10.973 — ServicePodsTab artık `state` veriyor ve tabloyu her durumda
// bağlıyor; sekme tarafı ServicePodsTab.tableStates.test.tsx'te çivili.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Link } from 'react-router-dom';
import { useDataTable, DATA_TABLE_STATE_TEXT, type DataTableStateProps } from '@/components/ui/DataTable';
import type { DataTableColumn } from '@/lib/dataTable';
import type { MergedPodRow } from './podsMerge';
import { ServicePodsTable } from './ServicePodsTable';

const COLS: DataTableColumn<MergedPodRow>[] = [
  { id: 'pod', label: 'Pod', sortValue: r => r.pod, width: 200 },
  { id: 'status', label: 'Durum', width: 100 },
  { id: 'cluster', label: 'Cluster', sortValue: r => r.cluster, width: 120 },
];
type PodsState = Omit<DataTableStateProps<MergedPodRow>, 'dt' | 'leading' | 'trailing'>;

const ROW: MergedPodRow = {
  key: 'c1/ns1/orders-1', pod: 'orders-1', cluster: 'c1', namespace: 'ns1', workload: null,
  statusKnown: false, restartsUnknown: true, source: 'thanos',
};

function Harness({ rows, state }: { rows: MergedPodRow[]; state?: PodsState }) {
  const dt = useDataTable<MergedPodRow>({ storageKey: 'test-service-pods-state', columns: COLS, rows });
  return (
    <ServicePodsTable dt={dt} view="flat" service="orders" range={{ preset: '1h' }}
      effNs="ns1" effDeploy="orders" cFrom={0} cTo={1} rangeParam={null} state={state} />
  );
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(rows: MergedPodRow[], state?: PodsState): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><Harness rows={rows} state={state} /></MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});

const stateRow = (el: HTMLElement) => el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;

describe('ServicePodsTable — durum satırı başlığın dosyasında (v0.10.967)', () => {
  it('state verilmezse ve satır yoksa: varsayılan "empty", başlık durur, colSpan = görünür kolonlar', () => {
    const el = mount([]);
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toContain(DATA_TABLE_STATE_TEXT.empty);
    expect(el.querySelector('thead')).not.toBeNull();
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    expect(row?.querySelector('td')?.getAttribute('colspan')).toBe('3');
  });

  it('sekmenin state\'i aynen çizilir: loading', () => {
    const el = mount([], { kind: 'loading', skeletonRows: 3 });
    expect(stateRow(el)?.dataset.dtState).toBe('loading');
  });

  it('hata + detail: bağlantı aynı hücrede, tablonun dışında değil', () => {
    const el = mount([], {
      kind: 'error', message: 'Pod metrikleri okunamadı: c1',
      detail: <Link to="/service?tab=infra">Infra sekmesi</Link>,
    });
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain('Pod metrikleri okunamadı: c1');
    expect(row.querySelectorAll('td').length).toBe(1);
    expect(row.querySelector('a')?.textContent).toBe('Infra sekmesi');
  });

  it('satır varken durum satırı yok (state verilse bile satırlar kazanır)', () => {
    const el = mount([ROW], { kind: 'error' });
    expect(stateRow(el)).toBeNull();
    expect(el.querySelector('#pod-row-orders-1')).not.toBeNull();
  });
});
