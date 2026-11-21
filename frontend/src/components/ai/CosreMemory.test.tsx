// @vitest-environment jsdom
// CosreMemory.test.tsx — CoSRE Faz E (ekip notları). Sözleşme: panel viewer'a
// listeyi GÖSTERİR ama ekleme / eylem menüsü yok; editör servis kapsamında
// "manual" not ekler; sohbet onay kartı ve "Düzelt" formu YALNIZ tıkla ve
// source=correction ile yazar; kart ayrıştırıcısı ve saf yardımcılar.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CosreMemoryItem, CosreMemoryPayload } from '@/lib/types';

const NS = Date.UTC(2026, 10, 20, 10, 0) * 1e6;
const item = (id: string, statement: string, extra: Partial<CosreMemoryItem> = {}): CosreMemoryItem => ({
  id, statement, scope: { kind: 'service', value: 'checkout' }, source: { kind: 'correction', by: 'Ayşe', at: NS }, status: 'active', ...extra,
});

const m = vi.hoisted(() => ({
  payload: null as CosreMemoryPayload | null,
  gets: [] as unknown[][],
  creates: [] as unknown[][],
}));
vi.mock('@/lib/api', () => ({
  api: {
    cosreMemory: vi.fn(async (...a: unknown[]) => { m.gets.push(a); return m.payload; }),
    createCosreMemory: vi.fn(async (...a: unknown[]) => { m.creates.push(a); return { item: {} }; }),
    updateCosreMemory: vi.fn(async () => ({ item: {} })),
    deleteCosreMemory: vi.fn(async () => ({ ok: true })),
    serviceNames: vi.fn(async () => []),
  },
}));

import { CosreMemoryPanel } from './CosreMemoryPanel';
import { CosreMemoryConfirm } from './CosreMemoryConfirm';
import { dateInputToNs, memState, nsToDateInput, memStatementError } from './cosreMemory';
import { parseCosreMemoryAction } from '@/lib/pageActions';
import { ConfirmGate } from '@/components/coremetry/Confirm';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.gets = []; m.creates = [];
  document.body.innerHTML = '';
});
const tick = async () => { for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 10)); }); };

async function mount(node: React.ReactNode) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><ConfirmGate>{node}</ConfirmGate></MemoryRouter></QueryClientProvider>);
  });
  await tick();
  return host;
}
const setValue = (el: HTMLTextAreaElement | HTMLInputElement, v: string) => {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, v);
  el.dispatchEvent(new Event('input', { bubbles: true }));
};
const btn = (el: HTMLElement, re: RegExp) => Array.from(el.querySelectorAll('button')).find(b => re.test(b.textContent ?? '')) as HTMLButtonElement | undefined;

describe('CosreMemoryPanel', () => {
  it('viewer listeyi görür; ekleme ve eylem menüsü yok; servis süzgeci GET\'e gider', async () => {
    m.payload = { items: [item('a', "checkout'un veritabanı pay-db"), item('b', 'emekli not', { status: 'retired' })], limits: { maxItems: 500, minStatement: 8, maxStatement: 240, promptItems: 5 } };
    const el = await mount(<CosreMemoryPanel service="checkout" canEdit={false} storageKey="t-mem" />);
    expect(el.textContent).toContain("checkout'un veritabanı pay-db");
    expect(m.gets[0][0]).toBe('checkout');
    expect(btn(el, /Add note|Not ekle/)).toBeUndefined();
    expect(Array.from(el.querySelectorAll('[aria-label]')).some(e => /Note actions|Not eylemleri/.test(e.getAttribute('aria-label') ?? ''))).toBe(false);
  });

  it('editör servis kapsamında elle not ekler (source manual, kapsam sabit)', async () => {
    m.payload = { items: [], limits: { maxItems: 500, minStatement: 8, maxStatement: 240, promptItems: 5 } };
    const el = await mount(<CosreMemoryPanel service="checkout" canEdit storageKey="t-mem2" />);
    await act(async () => { btn(el, /Add note|Not ekle/)!.click(); });
    const ta = el.querySelector('textarea')!;
    await act(async () => { setValue(ta, 'ödeme sağlayıcısı zaman aşımı 30 sn'); });
    await act(async () => { (el.querySelector('form') as HTMLFormElement).requestSubmit(); });
    await tick();
    expect(m.creates).toHaveLength(1);
    expect(m.creates[0][0]).toEqual({ statement: 'ödeme sağlayıcısı zaman aşımı 30 sn', scope: { kind: 'service', value: 'checkout' }, expiresAt: 0, source: { kind: 'manual' } });
  });
});

describe('sohbet düzeltme kartı', () => {
  it('yalnız Kaydet tıkı source=correction + exchangeId ile yazar; viewer kapalı', async () => {
    const act1 = parseCosreMemoryAction({ kind: 'cosre_memory', statement: "checkout'un veritabanı pay-db", scope: { kind: 'service', value: 'checkout' },
      scopeOptions: [{ kind: 'service', value: 'checkout', label: 'servis checkout' }], canSave: true, exchangeId: 'ex1' })!;
    expect(act1.exchangeId).toBe('ex1');
    const el = await mount(<CosreMemoryConfirm action={act1} />);
    expect(m.creates).toHaveLength(0);
    await act(async () => { btn(el, /^Kaydet$/)!.click(); });
    await tick();
    expect(m.creates[0][0]).toEqual({ statement: "checkout'un veritabanı pay-db", scope: { kind: 'service', value: 'checkout' }, source: { kind: 'correction', exchangeId: 'ex1' } });
    act(() => root?.unmount());
    const viewer = await mount(<CosreMemoryConfirm action={{ ...act1, canSave: false }} />);
    expect(btn(viewer, /^Kaydet$/)!.disabled).toBe(true);
    expect(viewer.textContent).toContain('editör ya da admin');
  });

  it('ayrıştırıcı başka türü / bozuk yükü reddeder', () => {
    expect(parseCosreMemoryAction({ kind: 'cosre_rule', text: 'x' })).toBeNull();
    expect(parseCosreMemoryAction({ kind: 'cosre_memory' })).toBeNull();
    expect(parseCosreMemoryAction({ kind: 'cosre_memory', statement: 'yeterince uzun not', canSave: 'yes' })!.canSave).toBe(false);
  });
});

describe('saf yardımcılar', () => {
  it('durum, tarih dönüşümü, uzunluk', () => {
    const now = NS / 1e6;
    expect(memState(item('a', 'x'), now)).toBe('active');
    expect(memState(item('a', 'x', { status: 'retired' }), now)).toBe('retired');
    expect(memState(item('a', 'x', { expiresAt: NS - 1 }), now)).toBe('expired');
    expect(nsToDateInput(dateInputToNs('2026-12-01'))).toBe('2026-12-01');
    expect(dateInputToNs('')).toBe(0);
    expect(memStatementError('kısa', 'tr')).toBeTruthy();
    expect(memStatementError('yeterince uzun bir not', 'tr')).toBeUndefined();
  });
});
