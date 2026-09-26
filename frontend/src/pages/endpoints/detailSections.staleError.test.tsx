// @vitest-environment jsdom
//
// detailSections.staleError — v0.10.954 (tablo standardı T12 incelemesi).
//
// NE ÇİVİLİYOR: /endpoint sayfasının "Who calls this" ve "Break down by"
// tabloları, sorgu HATADAYKEN React Query önbellekte eski satırları hâlâ
// tutsa bile hatayı gösterir. T12 geçişinde durum satırı yalnız
// `dt.sortedRows.length === 0` iken çiziliyordu: odak/yeniden-bağlanma
// refetch'i düşünce `isError` true ama `data` son başarılı yanıt → satırlar
// basılıyor, hata satırı HİÇ görünmüyordu (eski kod hata satırını tablodan
// bağımsız basıyordu). Kapı artık `showRows = !isError && rows.length > 0`
// (Metrics / PodTraces / Rollouts emsali). Callers dipnotu ("N sampled
// calls…") da aynı kapıya bağlı — hata satırının altında bayat sayı kalmasın.
//
// NEDEN GERÇEK MOUNT: kapı tip-doğru şekilde yanlış yazılabilir
// (`sortedRows.length === 0`); tsc de eslint de sessiz kalır.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { EndpointCallersResponse, EndpointSplitResponse } from '@/lib/types';

interface FakeQuery<T> { data: T | undefined; isPending: boolean; isError: boolean }

let callersQ: FakeQuery<EndpointCallersResponse> = { data: undefined, isPending: true, isError: false };
let splitQ: FakeQuery<EndpointSplitResponse> = { data: undefined, isPending: true, isError: false };

vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useEndpointCallers: () => ({ ...callersQ, refetch: () => Promise.resolve() }),
    useEndpointSplit: () => ({ ...splitQ, refetch: () => Promise.resolve() }),
  };
});

import { CallersSection, SplitSection } from './detailSections';

const REF = { service: 'orders-api', path: '/api/orders', sig: false };
const FROM = 1_700_000_000_000_000_000;
const TO = FROM + 3_600_000_000_000;

const CALLERS: EndpointCallersResponse = {
  callers: [
    { service: 'gateway', calls: 900, errors: 9, errorRate: 1, p95Ms: 120, shareMs: 5000, sharePct: 62.5 },
    { service: 'billing-worker', calls: 40, errors: 0, errorRate: 0, p95Ms: 400, shareMs: 3000, sharePct: 37.5 },
  ],
  sampledSpans: 1000,
  sampled: false,
  directEntries: 12,
  unresolved: 3,
  totalMs: 8000,
};

const SPLIT: EndpointSplitResponse = {
  by: 'deployment.environment',
  values: [
    { value: 'prod-eu', calls: 800, errors: 2, errorRate: 0.25, avgMs: 20, p50Ms: 12, p99Ms: 90 },
    { value: 'prod-us', calls: 300, errors: 0, errorRate: 0, avgMs: 18, p50Ms: 10, p99Ms: 70 },
  ],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}

const stateRow = (el: HTMLElement, kind: string) => el.querySelector(`tbody tr[data-dt-state="${kind}"]`);
const bodyRows = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('CallersSection — hata, önbellekteki bayat satırları gizler', () => {
  it('sağlıklı yanıt: satırlar + dipnot, durum satırı yok', () => {
    callersQ = { data: CALLERS, isPending: false, isError: false };
    const el = mount(<CallersSection refObj={REF} from={FROM} to={TO} />);
    expect(bodyRows(el).length).toBe(2);
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(el.textContent).toMatch(/sampled calls arrived/);
  });

  it('refetch düştü, data eski yanıtı tutuyor: hata satırı var, bayat satır ve dipnot yok', () => {
    callersQ = { data: CALLERS, isPending: false, isError: true };
    const el = mount(<CallersSection refObj={REF} from={FROM} to={TO} />);
    const err = stateRow(el, 'error');
    expect(err, 'hata satırı görünmüyor — bayat satırlar onu gizledi').not.toBeNull();
    expect(err!.textContent).toMatch(/Çağıran sorgusu başarısız/);
    expect(bodyRows(el).length, 'bayat çağıran satırları hatayla yan yana basıldı').toBe(0);
    expect(el.textContent).not.toMatch(/sampled calls arrived/);
    expect(el.textContent).not.toMatch(/could not be resolved/);
  });

  it('ilk yükleme hatası (data yok): yine hata satırı', () => {
    callersQ = { data: undefined, isPending: false, isError: true };
    const el = mount(<CallersSection refObj={REF} from={FROM} to={TO} />);
    expect(stateRow(el, 'error')).not.toBeNull();
    expect(stateRow(el, 'empty')).toBeNull();
  });
});

describe('SplitSection — hata, önbellekteki bayat satırları gizler', () => {
  it('sağlıklı yanıt: değer satırları, durum satırı yok', () => {
    splitQ = { data: SPLIT, isPending: false, isError: false };
    const el = mount(<SplitSection refObj={REF} from={FROM} to={TO} />);
    expect(bodyRows(el).length).toBe(2);
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
  });

  it('refetch düştü, data eski yanıtı tutuyor: hata satırı var, bayat değer yok', () => {
    splitQ = { data: SPLIT, isPending: false, isError: true };
    const el = mount(<SplitSection refObj={REF} from={FROM} to={TO} />);
    expect(stateRow(el, 'error'), 'hata satırı görünmüyor — bayat satırlar onu gizledi').not.toBeNull();
    expect(bodyRows(el).length).toBe(0);
    expect(el.textContent).not.toMatch(/prod-eu/);
  });

  it('boş yanıt hatasız: boş satırı (hata değil)', () => {
    splitQ = { data: { by: 'deployment.environment', values: [] }, isPending: false, isError: false };
    const el = mount(<SplitSection refObj={REF} from={FROM} to={TO} />);
    expect(stateRow(el, 'empty')).not.toBeNull();
    expect(stateRow(el, 'error')).toBeNull();
  });
});
