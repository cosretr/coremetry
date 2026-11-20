// @vitest-environment jsdom
//
// OperationCardSection — v0.10.1491 (operasyon kartı R3, trace yarısı).
// Pinler: yükleniyor / hata / ağaç-null (treeError) / örneklem boş / alt
// çağrı yok / dolu — durumlar tablonun İÇİNDE (T12); dolu hâlde ağaç sırası,
// derinlik girintisi, örnek trace linkleri, iş kodu rozetleri, MV dipnotu,
// others notu ve R4 kod yuvası. İstek parametreleri (sig / entry / env)
// hook'a gerçekten gider.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { OperationCardPayload, OpCallTree } from '@/lib/types';

interface FakeQuery<T> { data: T | undefined; isPending: boolean; isError: boolean }
let cardQ: FakeQuery<OperationCardPayload> = { data: undefined, isPending: true, isError: false };
let lastParams: unknown = null;
const codeOpen: boolean[] = [];

vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useOperationCard: (p: unknown) => { lastParams = p; return { ...cardQ, refetch: () => Promise.resolve() }; },
    // v0.10.1506 — "Kod" bölümü kapalıyken sorgu devre dışı (ayrı testi: OperationCardCodeSection.test.tsx).
    useOperationCardCode: (_p: unknown, open: boolean) => { codeOpen.push(open); return { data: undefined, isPending: true, isError: false }; },
  };
});

import { OperationCardSection } from './OperationCardSection';

const REF = { service: 'orders-api', path: '/orders/{id}/approve', sig: false };
const FROM = 1_700_000_000_000_000_000;
const TO = FROM + 3_600_000_000_000;

const edge = (o: Partial<OpCallTree['children'][number]>) => ({
  depth: 1, parentKey: 'entry', key: 'k', kind: 'service', protocol: 'http', target: 'payments-api',
  op: 'POST /payments/authorize', calls: 10, errors: 0, errPct: 0, p50Ms: 5, p95Ms: 20, maxMs: 30,
  sampleTraceIds: [], ...o,
});

const TREE: OpCallTree = {
  entry: { service: 'orders-api', op: 'POST /orders/{id}/approve', kind: 'server', calls: 200, errors: 5, errPct: 2.5, p50Ms: 41, p95Ms: 310, maxMs: 1200 },
  operationCode: 'ORDER_APPROVE', version: '1.42.0',
  children: [
    edge({ key: 'svc', errPct: 6, sampleTraceIds: ['aaa', 'bbb', 'ccc'] }),
    edge({ depth: 2, parentKey: 'svc', key: 'svc>ext', kind: 'external', target: 'ext:bank-gw.example.test', op: 'POST /v1/auth' }),
    edge({ key: 'db', kind: 'db', protocol: 'db', target: 'db:oracle', op: 'SELECT orders' }),
  ],
  others: 4,
  codes: [{ kind: 'function_code', code: 'ORD0042', count: 190 }, { kind: 'channel_code', code: 'MOBILE', count: 120 }],
  sampledTraces: 200, sampled: true, sampleOrder: 'slow', truncated: false,
  windowEdges: [{ childService: 'payments-api', childOp: 'POST /payments/authorize', calls: 48213 }],
};

const payload = (o: Partial<OperationCardPayload> = {}): OperationCardPayload => ({
  service: REF.service, path: REF.path, sig: false, entry: 'http', fromNs: FROM, toNs: TO,
  tree: TREE, code: null, ...o,
});

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

describe('OperationCardSection — durumlar', () => {
  it('yükleniyor: tablo içi loading satırı, R4 yuvası yine var', () => {
    cardQ = { data: undefined, isPending: true, isError: false };
    const el = mount(<OperationCardSection refObj={REF} from={FROM} to={TO} />);
    expect(stateRow(el, 'loading')).not.toBeNull();
    expect(el.querySelector('[data-opcard-slot="code"]')).not.toBeNull();
    expect(el.querySelector('#opcard')).not.toBeNull();
  });

  it('istek hatası: hata satırı, bayat satır yok', () => {
    cardQ = { data: payload(), isPending: false, isError: true };
    const el = mount(<OperationCardSection refObj={REF} from={FROM} to={TO} />);
    expect(stateRow(el, 'error')).not.toBeNull();
    expect(bodyRows(el).length).toBe(0);
    expect(el.textContent).not.toMatch(/ORD0042/);
  });

  it('ağaç okunamadı (tree null): sunucunun temiz mesajıyla hata satırı', () => {
    cardQ = { data: payload({ tree: null, treeError: 'Çağrı ağacı okunamadı (test)' }), isPending: false, isError: false };
    const el = mount(<OperationCardSection refObj={REF} from={FROM} to={TO} />);
    expect(stateRow(el, 'error')?.textContent).toMatch(/Çağrı ağacı okunamadı \(test\)/);
  });

  it('örneklem boş ≠ alt çağrı yok: iki ayrı boş metni', () => {
    cardQ = { data: payload({ tree: { ...TREE, children: [], sampledTraces: 0, codes: [], windowEdges: [], others: 0, operationCode: '', version: '' } }), isPending: false, isError: false };
    let el = mount(<OperationCardSection refObj={REF} from={FROM} to={TO} />);
    const noSample = stateRow(el, 'empty')?.textContent ?? '';
    act(() => { root?.unmount(); });
    host?.remove();
    cardQ = { data: payload({ tree: { ...TREE, children: [], sampledTraces: 50, others: 0 } }), isPending: false, isError: false };
    el = mount(<OperationCardSection refObj={REF} from={FROM} to={TO} />);
    const leaf = stateRow(el, 'empty')?.textContent ?? '';
    expect(noSample).not.toBe('');
    expect(leaf).not.toBe('');
    expect(noSample).not.toBe(leaf);
  });
});

describe('OperationCardSection — dolu', () => {
  it('ağaç sırası, girinti, örnek trace, rozetler, dipnotlar', () => {
    cardQ = { data: payload({ windowClamped: true }), isPending: false, isError: false };
    const el = mount(<OperationCardSection refObj={REF} from={FROM} to={TO} />);
    const rows = bodyRows(el);
    expect(rows.length).toBe(3);
    expect(rows.map(r => r.getAttribute('data-depth'))).toEqual(['1', '2', '1']);
    expect(rows[1].textContent).toMatch(/bank-gw\.example\.test/);
    expect(rows[1].textContent).not.toMatch(/ext:/); // hedef öneki etikette yok
    expect(rows[2].textContent).toMatch(/oracle/);
    // Örnek trace linkleri /trace?id=… — üç tane.
    const links = Array.from(rows[0].querySelectorAll('a')).map(a => a.getAttribute('href') ?? '');
    expect(links.filter(h => h.startsWith('/trace?id=')).length).toBe(3);
    // Sapan hata oranı renkli, sağlıklı nötr.
    expect(rows[0].querySelector('.badge.b-err')).not.toBeNull();
    expect(rows[2].querySelector('.badge.b-gray')).not.toBeNull();
    expect(el.textContent).toMatch(/ORDER_APPROVE/);
    expect(el.textContent).toMatch(/ORD0042/);
    expect(el.textContent).toMatch(/MOBILE/);
    expect(el.textContent).toMatch(/v1\.42\.0/);
    expect(el.querySelector('[data-testid="opcard-window"]')?.textContent).toMatch(/payments-api/);
    expect(el.textContent).toMatch(/\+4/);
    expect(el.textContent).toMatch(/200/); // örneklem başlığı
    expect(el.querySelector('[data-opcard-slot="code"]')).not.toBeNull();
    // Kod yarısı açılmadan istenmez; ağaç satırları odak anahtarı taşır.
    expect(codeOpen.length).toBeGreaterThan(0);
    expect(codeOpen.every(o => o === false)).toBe(true);
    expect(rows[0].getAttribute('data-opcard-key')).toBe('svc');
  });

  it('kimlik parametreleri hook\'a gider (sig, entry=rpc, env, cluster)', () => {
    cardQ = { data: payload(), isPending: false, isError: false };
    mount(<OperationCardSection refObj={{ ...REF, path: 'orders.Approve', sig: true }} entry="rpc" env="prod" cluster="c1" from={FROM} to={TO} />);
    expect(lastParams).toEqual({ service: 'orders-api', path: 'orders.Approve', from: FROM, to: TO, sig: '1', entry: 'rpc', env: 'prod', cluster: 'c1' });
  });
});
