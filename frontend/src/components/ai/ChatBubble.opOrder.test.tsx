// @vitest-environment jsdom
//
// v0.10.2213 (operatör onayı 2026-11-15) — endpoint / operasyon cevabının
// sırası ve TEK trace tablosu: cümle → hatalar → trace tablosu → grafik →
// linkler. İkinci trace listesi ve tekrar eden Çağrı / Hata oranı / p99
// satırı yok (cümle var); sabit Servis / İşlem kolonları başlıkta.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: q.includes('prefers-reduced-motion: reduce'), media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});
vi.mock('@/components/CosreChart', () => ({
  CosreChart: ({ spec }: { spec: { service: string } }) => <div data-cosre={spec.service}>[grafik]</div>,
}));

import { ChatBubble } from './ChatBubble';
import { OperationCard } from './OperationCard';
import type { ChatOperationPayload, ChatTurn, ChatTypedBlock } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

async function mount(turn: ChatTurn) {
  await act(async () => {
    root.render(
      <MemoryRouter>
        <QueryClientProvider client={new QueryClient()}>
          <ChatBubble turn={turn} />
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
}

const T0 = 1_790_496_000_000_000_000;
const OP = {
  operation: 'POST /api/x', matchKind: 'route', rangeS: 1800, route: '/api/x', method: 'POST',
  current: { calls: 13, errors: 7, p99Ms: 378, measured: true }, previous: { calls: 5, errors: 3, p99Ms: 720, measured: true },
  traces: [
    { traceId: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', service: 'svc-a', durationMs: 378, error: true, startTime: T0, rootName: 'POST /api/x' },
    { traceId: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', service: 'svc-a', durationMs: 120, error: false, startTime: T0 + 1e9, rootName: 'POST /api/x' },
  ],
  oracleErrors: null, tracesHref: '/traces?service=svc-a',
  endpointHref: '/endpoint?service=svc-a&path=%2Fapi%2Fx',
  endpointExceptions: [{ type: '500', count: 7, lastSeenNs: T0, href: '/exceptions?service=svc-a' }],
  endpointExceptionsChecked: true,
};
const block = (id: string, type: string, seq: number, payload: unknown): ChatTypedBlock =>
  ({ id, type, seq, final: true, payload } as ChatTypedBlock);
const CHART = { service: 'svc-a', agg: 'rate', title: 'Throughput' };

describe('v0.10.2213 — endpoint cevabı sırası', () => {
  it('cümle → hatalar → TEK trace tablosu → grafik → linkler; ikinci liste ve tekrar satırı yok', async () => {
    await mount({
      role: 'assistant', text: 'POST /api/x son 30 dakikada 13 çağrı, hata oranı %53,85.',
      blocks: [block('b1', 'chart', 1, CHART), block('op', 'operation', 1000, OP)],
    } as ChatTurn);
    const order = ['[data-opcard="head"]', '[data-trace-table]', '[data-cosre]', '[data-opcard="links"]']
      .map(sel => host.querySelector(sel));
    order.forEach((n, i) => expect(n, `parça ${i}`).not.toBeNull());
    for (let i = 1; i < order.length; i++) {
      expect(order[i - 1]!.compareDocumentPosition(order[i]!) & Node.DOCUMENT_POSITION_FOLLOWING, `sıra ${i}`).toBeTruthy();
    }
    expect(host.querySelectorAll('[data-trace-table]')).toHaveLength(1);
    expect(host.querySelectorAll('table')).toHaveLength(1);
    expect(host.textContent).not.toContain('Hata oranı %');
    expect(host.textContent).toContain('Endpoint teknik hataları');
    // Sabit Servis / İşlem kolonları başlıkta bir kez.
    expect([...host.querySelectorAll('[data-trace-table] thead th')].map(t => t.textContent).slice(0, 4)).toEqual(['Başlangıç', 'Trace', 'Süre', 'Durum']);
    expect(host.querySelector('.cm-trace-list__cap')!.textContent).toBe('POST /api/x · svc-a');
    const links = host.querySelector('[data-opcard="links"]')!.textContent!;
    for (const s of ["Traces'te aç", 'Endpoint sayfası', 'Exceptions', '2 trace, 1 hatalı gösterildi']) expect(links).toContain(s);
  });

  it('trace_list bloğu varsa tablo ondan; kartın trace\'leri ikinci tablo açmaz', async () => {
    const tl = { query: '', window: { fromNs: 0, toNs: 0 }, deepLink: '/traces?q=1', truncated: false,
      traces: [{ traceId: 'cccccccccccccccccccccccccccccccc', startTime: T0, serviceName: 'svc-a', rootName: 'POST /api/x', durationMs: 900, spanCount: 4, hasError: false }] };
    await mount({ role: 'assistant', text: 'Özet.', blocks: [block('tl', 'trace_list', 2, tl), block('op', 'operation', 1000, OP)] } as ChatTurn);
    expect(host.querySelectorAll('[data-trace-table]')).toHaveLength(1);
    expect(host.querySelector('[data-trace-table] a[title="cccccccccccccccccccccccccccccccc"]')).not.toBeNull();
  });

  it('cümle yoksa (kartın head\'i headline=false) Çağrı / Hata oranı / p99 satırı kalır', async () => {
    await act(async () => {
      root.render(<MemoryRouter><OperationCard op={OP as unknown as ChatOperationPayload} part="head" /></MemoryRouter>);
    });
    expect(host.textContent).toContain('Hata oranı');
  });
});
