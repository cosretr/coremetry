// @vitest-environment jsdom
//
// coremetry/logs/FieldSidebar — v0.10.1853 (Log Search Faz 2 dilim 2.2).
//   • İş alanları bölümü en üstte; tanım yoksa / çözülmezse GİZLİ;
//   • ES-maliyet kilidi: bağlanınca fieldstats YOK, alan açılınca TEK istek;
//   • pencere ≥ 200.000 → `est` gider ve "~ örneklenmiş" etiketi çizilir
//     (v0.10.1852); altında `est` hiç gönderilmez;
//   • degraded / partial / boş durumları ayrı metin.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogFieldStats } from '@/lib/types';

const m = vi.hoisted(() => ({ calls: [] as Record<string, unknown>[], next: null as LogFieldStats | null }));
vi.mock('@/lib/api', () => ({
  api: {
    logsFieldStats: (p: Record<string, unknown>) => {
      m.calls.push(p);
      return Promise.resolve(m.next ?? { field: String(p.field), total: 10, values: [{ value: 'MOBILE', count: 6 }, { value: 'WEB', count: 4 }] });
    },
  },
}));

import { FieldSidebar, fieldsPanelOpen } from './FieldSidebar';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: q.includes('min-width: 1280px'), media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

let root: Root | null = null; let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
beforeEach(() => {
  m.calls = []; m.next = null;
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); }, clear: () => store.clear(), key: () => null, length: 0,
  });
});
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; vi.unstubAllGlobals(); });

function mount(props: Partial<Parameters<typeof FieldSidebar>[0]> = {}) {
  host = document.createElement('div'); document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => root!.render(
    <QueryClientProvider client={qc}>
      <FieldSidebar fields={['message', 'CHANNEL_CODE', 'kubernetes.pod_name', 'thread_name']} fieldsTotal={4}
        columns={['message']} scope={{ service: 'orders-api' }}
        onToggleColumn={() => {}} onPillAdd={() => {}} onPillExclude={() => {}} {...props} />
    </QueryClientProvider>));
  return host;
}
const click = (el: Element | null) => act(() => { (el as HTMLElement).click(); });

describe('FieldSidebar', () => {
  it('fieldsPanelOpen: saklı tercih kazanır, yoksa genişlik', () => {
    expect(fieldsPanelOpen('1', false)).toBe(true);
    expect(fieldsPanelOpen('0', true)).toBe(false);
    expect(fieldsPanelOpen(null, true)).toBe(true);
    expect(fieldsPanelOpen(null, false)).toBe(false);
  });

  it('iş alanı yokken bölüm gizli; varken en üstte', () => {
    const h = mount();
    expect(h.querySelector('[data-testid="fs-business"]')).toBeNull();
    act(() => root!.unmount()); host!.remove();
    const h2 = mount({ businessFields: [{ label: 'Kanal', field: 'CHANNEL_CODE', verified: true }] });
    const sec = h2.querySelector('[data-testid="fs-business"]')!;
    expect(sec.textContent).toContain('İş alanları');
    expect(sec.textContent).toContain('Kanal');
    // İş alanı "Mevcut alanlar"da ikinci kez çizilmez.
    expect(h2.querySelectorAll('[data-testid="fs-field-CHANNEL_CODE"]').length).toBe(1);
    // Sıra: iş alanları bölümü ilk başlık.
    expect(h2.querySelector('h5')!.textContent).toBe('İş alanları');
  });

  it('bağlanınca istek yok; alan açılınca TEK istek, est yok (küçük pencere)', async () => {
    const h = mount({ windowTotal: 5_000 });
    expect(m.calls).toHaveLength(0);
    click(h.querySelector('[data-testid="fs-field-thread_name"]'));
    await settle();
    expect(m.calls).toHaveLength(1);
    expect(m.calls[0]).not.toHaveProperty('est');
    expect(h.querySelector('[data-testid="fs-sampled"]')).toBeNull();
    expect(h.textContent).toContain('MOBILE');
  });

  it('büyük pencere → est + "~ örneklenmiş" etiketi', async () => {
    m.next = { field: 'CHANNEL_CODE', total: 2000, sampled: true, probability: 0.05, values: [{ value: 'MOBILE', count: 1200 }, { value: 'WEB', count: 800 }] };
    const h = mount({ windowTotal: 4_000_000 });
    click(h.querySelector('[data-testid="fs-field-CHANNEL_CODE"]'));
    await settle();
    expect(m.calls[0]).toMatchObject({ field: 'CHANNEL_CODE', est: 4_000_000, service: 'orders-api' });
    expect(h.querySelector('[data-testid="fs-sampled"]')!.textContent).toBe('~ örneklenmiş (%5) · 2 değer');
    expect(h.textContent).not.toContain("dokümanların ~%");
  });

  it('degraded / partial / boş ayrı metin', async () => {
    m.next = { field: 'thread_name', total: 0, values: [], degraded: true, reason: 'log backend slow/unreachable' };
    const h = mount();
    click(h.querySelector('[data-testid="fs-field-thread_name"]'));
    await settle();
    expect(h.textContent).toContain('üst değerler hesaplanamadı');
    expect(h.textContent).not.toContain('Bu pencerede değer yok');
  });
});
