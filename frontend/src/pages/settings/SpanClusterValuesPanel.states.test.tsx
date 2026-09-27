// @vitest-environment jsdom
//
// SpanClusterValuesPanel.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR: span cluster değerleri statik tablosu (satır başına seçici +
// Assign, dt yok) yükleniyor / hata / boş hâlini tablonun İÇİNDE, başlık
// dururken basar (eskiden Spinner ve Empty "yüklenemedi" TABLONUN YERİNE).
// Sıra aynı: yükleniyor, hata, boş. Hata, React Query önbelleğindeki eski
// satırları da gizler — düşen tazelemenin altında bayat eşleme listesi
// "güncel" gibi okunmasın; geriye dönük tarama anahtarı aynı kapıya bağlı.
//
// NEDEN GERÇEK QueryClient: "tazeleme düştü ama data eski yanıtı tutuyor"
// hâli ancak gerçek önbellekle kurulur (isError + data birlikte).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ThanosSpanClustersResponse } from '@/lib/types';

const h = vi.hoisted(() => ({
  list: vi.fn<() => Promise<ThanosSpanClustersResponse>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    thanosSpanClusters: () => h.list(),
    thanosAssignSpanCluster: () => Promise.resolve({ ok: true }),
  },
}));

import { SpanClusterValuesPanel } from './SpanClusterValuesPanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DATA: ThanosSpanClustersResponse = {
  rows: [
    { value: 'prod-east', spans: 1200, firstSeen: '2026-09-20T00:00:00Z', lastSeen: '2026-09-27T00:00:00Z', ownerId: 'c1', ownerName: 'prod-east' },
    { value: 'ocp-west', spans: 40, firstSeen: '2026-09-26T00:00:00Z', lastSeen: '2026-09-27T00:00:00Z' },
  ],
  unmapped: 1, source: 'entity-7d', since: '2026-09-20T00:00:00Z',
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient | null = null;

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc!}>
        <SpanClusterValuesPanel clusters={[{ id: 'c1', name: 'prod-east' }]} />
      </QueryClientProvider>,
    );
  });
  await flush();
  return host;
}
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  qc?.clear();
  host = null; root = null; qc = null;
  h.list.mockReset();
});

const stateRow = (el: HTMLElement, kind: string) => el.querySelector<HTMLTableRowElement>(`tbody tr[data-dt-state="${kind}"]`);
const dataRows = (el: HTMLElement) => Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));
const backfillToggle = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('label')).find(l => l.textContent?.includes('geriye dönük tara'));

describe('SpanClusterValuesPanel — durumlar tablonun içinde (P-2)', () => {
  it('yükleniyor: başlık durur, iskelet satırı colSpan = 6 <th>, tarama anahtarı yok', async () => {
    h.list.mockReturnValue(new Promise(() => {}));
    const el = await mount();
    expect(el.querySelectorAll('thead th')).toHaveLength(6);
    const row = stateRow(el, 'loading');
    expect(row, 'yükleniyor tablonun içinde değil').not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(6);
    expect(backfillToggle(el)).toBeUndefined();
  });

  it('ilk okuma hatası: hata satırı sunucu metniyle; boş cümle yok', async () => {
    h.list.mockRejectedValue(new Error('HTTP 502: {"error":"thanos unreachable"}'));
    const el = await mount();
    const row = stateRow(el, 'error');
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain('Span cluster değerleri yüklenemedi: thanos unreachable');
    expect(stateRow(el, 'empty')).toBeNull();
  });

  it('boş: standart boş satırı (neden cümlesi korunur)', async () => {
    h.list.mockResolvedValue({ ...DATA, rows: [], unmapped: 0 });
    const el = await mount();
    const row = stateRow(el, 'empty');
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain('Span verisinde cluster değeri yok');
    expect(backfillToggle(el)).toBeUndefined();
  });

  it('satırlar: durum satırı yok, tarama anahtarı ve Assign yerinde', async () => {
    h.list.mockResolvedValue(DATA);
    const el = await mount();
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(dataRows(el)).toHaveLength(2);
    expect(backfillToggle(el)).toBeDefined();
    expect(el.querySelector('tbody')!.textContent).toContain('Assign');
  });

  it('tazeleme düştü, önbellek eski satırları tutuyor: hata satırı, bayat satır ve anahtar yok', async () => {
    h.list.mockResolvedValueOnce(DATA);
    const el = await mount();
    expect(dataRows(el)).toHaveLength(2);
    h.list.mockRejectedValueOnce(new Error('HTTP 503: busy'));
    await act(async () => { await qc!.refetchQueries({ queryKey: ['thanos-span-clusters'] }); });
    await flush();
    expect(stateRow(el, 'error'), 'hata satırı bayat satırların arkasında kayboldu').not.toBeNull();
    expect(dataRows(el)).toHaveLength(0);
    expect(backfillToggle(el)).toBeUndefined();
  });
});
