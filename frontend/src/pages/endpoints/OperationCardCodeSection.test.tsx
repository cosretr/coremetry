// @vitest-environment jsdom
//
// OperationCardCodeSection — v0.10.1506 (operasyon kartı R4, kod yarısı).
// AĞ KATMANINDA (fetch sahte — tarayıcının HAR'ında görünecek olan tam bu):
//   • kapalıyken İSTEK YOK; açınca TEK istek, URL'de code=1;
//   • cevap Go operationCardCodeDTO şeklinde (kod gövdesi alanı YOK) ve DOM
//     yalnız imza / alan adı+tipi / dosya:satır / alıcı.metot / literal çizer;
//   • eşleşen çağrı ağaç satırını adlandırır, eşleşmeyen "eşleşme yok";
//   • Code Search kapalı / giriş bulunamadı → dürüst not, tablo yok.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { OperationCardCode, OperationCardPayload, OpCallEdge } from '@/lib/types';
import { OperationCardCodeSection } from './OperationCardCodeSection';

const PARAMS = { service: 'orders-api', path: '/orders/{id}/approve', from: 1, to: 2 };

const TREE_ROWS: OpCallEdge[] = [
  { depth: 1, parentKey: 'entry', key: 'svc:payments-api|POST /payments/authorize', kind: 'service', protocol: 'http', target: 'payments-api', op: 'POST /payments/authorize', calls: 10, errors: 0, errPct: 0, p50Ms: 1, p95Ms: 2, maxMs: 3, sampleTraceIds: [] },
];

const CODE_OK: OperationCardCode = {
  outcome: 'partial', reason: 'çağrılan AuditClient dosyası okunan ağaçta yok',
  repo: 'orders-api', branch: 'release', source: 'convention', browseUrl: 'https://tfs.example.test/_git/orders-api',
  entry: { path: '/src/main/java/com/example/orders/web/OrderController.java', fromLine: 27, toLine: 36,
    signature: 'public ResponseEntity<ApproveResponse> approve(@PathVariable("id") long id, @Valid @RequestBody ApproveRequest req)',
    url: 'https://tfs.example.test/_git/orders-api?path=%2Fsrc%2FOrderController.java&line=27', found: 'route' },
  request: [{ owner: 'ApproveRequest', name: 'amount', type: 'BigDecimal' }, { owner: 'ApproveRequest', name: 'channel', type: 'ChannelInfo', nested: true }],
  response: [{ owner: 'ApproveResponse', name: 'status', type: 'ApprovalStatus' }],
  callSites: [
    { path: '/src/main/java/com/example/orders/service/OrderServiceImpl.java', line: 15, kind: 'http', callee: 'paymentsClient.authorize', target: '/payments/authorize', service: 'payments-api', matchedChild: 0, url: 'https://tfs.example.test/x?line=15' },
    { path: '/src/main/java/com/example/orders/service/OrderServiceImpl.java', line: 16, kind: 'db', callee: 'jdbcTemplate.update', matchedChild: -1 },
  ],
  stats: { fetched: 6, searches: 2 },
  usages: { method: 'approve', outcome: 'ok', total: 12, hits: [
    { repo: 'web-bff', path: '/src/OrdersFlow.java', line: 88, url: 'https://tfs.example.test/web-bff?line=88', caller: true },
    { repo: 'misc-batch', path: '/src/Batch.java' },
  ] },
};

const payload = (code: OperationCardCode | null): OperationCardPayload => ({
  service: 'orders-api', path: PARAMS.path, sig: false, entry: 'http', fromNs: 1, toNs: 2, tree: null, code,
});

let urls: string[] = [];
let bodies: string[] = [];
let nextCode: OperationCardCode | null = CODE_OK;

beforeEach(() => {
  urls = []; bodies = [];
  vi.stubGlobal('fetch', vi.fn(async (u: string) => {
    // Yalnız kartın ucu sayılır (i18n kabuğu /api/branding de okur).
    if (!String(u).includes('/api/endpoints/operation-card')) return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
    urls.push(String(u));
    const body = JSON.stringify(payload(nextCode));
    bodies.push(body);
    return new Response(body, { status: 200, headers: { 'Content-Type': 'application/json' } });
  }));
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter><OperationCardCodeSection params={PARAMS} treeRows={TREE_ROWS} /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  return host;
}
const flush = async () => { for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); }); };
const toggle = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('[data-testid="opcard-code-toggle"]')!;

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  vi.unstubAllGlobals();
  nextCode = CODE_OK;
});

describe('OperationCardCodeSection', () => {
  it('kapalıyken 0 istek; açınca tek istek code=1', async () => {
    const el = mount();
    await flush();
    expect(urls).toEqual([]);
    expect(toggle(el).getAttribute('aria-expanded')).toBe('false');
    act(() => { toggle(el).click(); });
    await flush();
    expect(urls.length).toBe(1);
    expect(urls[0]).toMatch(/\/api\/endpoints\/operation-card\?/);
    expect(new URL(urls[0], 'http://x').searchParams.get('code')).toBe('1');
    expect(toggle(el).getAttribute('aria-expanded')).toBe('true');
  });

  it('dolu: imza, alanlar, çağrı yerleri, eşleşme, kaynak satırı — gövde yok', async () => {
    const el = mount();
    act(() => { toggle(el).click(); });
    await flush();
    const sig = el.querySelector('[data-testid="opcard-code-sig"]')?.textContent ?? '';
    expect(sig).toMatch(/approve\(@PathVariable/);
    expect(el.querySelector('[data-testid="opcard-code-request"]')?.textContent).toMatch(/ApproveRequest\.amount.*BigDecimal/);
    expect(el.querySelector('[data-testid="opcard-code-response"]')?.textContent).toMatch(/ApprovalStatus/);
    const rows = Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toMatch(/OrderServiceImpl\.java:15/);
    expect(rows[0].textContent).toMatch(/payments-api · POST \/payments\/authorize/);
    expect(rows[1].textContent).toMatch(/eşleşme yok|no match/);
    expect(el.querySelector('[data-testid="opcard-code-note"]')?.textContent).toMatch(/AuditClient/);
    expect(el.querySelector('[data-code-source]')?.textContent).toMatch(/orders-api/);
    // v0.10.1509 — "Bu metodu kim kullanıyor?": olası etiketi, çağıran işareti, kalan sayısı.
    const usages = el.querySelector('[data-testid="opcard-usages"]');
    expect(usages?.textContent).toMatch(/approve/);
    expect(usages?.textContent).toMatch(/olası|possible/);
    expect(usages?.querySelectorAll('[data-usage-caller="1"]').length).toBe(1);
    expect(usages?.textContent).toMatch(/OrdersFlow\.java:88/);
    expect(usages?.textContent).toMatch(/\+10/);
    // "HAR": ağdan gelen gövde kod içeriği taşıyan bir anahtar içermez.
    expect(bodies.length).toBe(1);
    const code = JSON.parse(bodies[0]).code as Record<string, unknown>;
    for (const k of ['windows', 'content', 'body', 'snippet']) expect(code).not.toHaveProperty(k);
    expect(code.entry).not.toHaveProperty('content');
  });

  it('Code Search kapalı: dürüst not, tablo yok', async () => {
    nextCode = { outcome: 'search-off', reason: 'giriş metodu için Code Search gerekli', entry: null, request: [], response: [], callSites: [], stats: { fetched: 0, searches: 0 } };
    const el = mount();
    act(() => { toggle(el).click(); });
    await flush();
    expect(el.querySelector('[data-testid="opcard-code-note"]')?.textContent).toMatch(/Code Search gerekli/);
    expect(el.querySelector('table')).toBeNull();
    expect(el.querySelector('[data-code-source]')).toBeNull();
    expect(el.querySelector('[data-testid="opcard-usages"]')).toBeNull();
  });

  it('giriş bulunamadı (reason boş): genel dürüst metin', async () => {
    nextCode = { outcome: 'tree-miss', entry: null, request: [], response: [], callSites: [], stats: { fetched: 2, searches: 1 } };
    const el = mount();
    act(() => { toggle(el).click(); });
    await flush();
    expect(el.querySelector('[data-testid="opcard-code-note"]')?.textContent?.length).toBeGreaterThan(5);
    expect(el.querySelector('table')).toBeNull();
  });
});
