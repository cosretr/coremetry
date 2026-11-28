// @vitest-environment jsdom
//
// v0.10.1388 — ChatTopologyCard render sözleşmesi + chatTopologyView.
// v0.10.1392 — operatör (prod ekran görüntüleri): "Bunu galiba eski service
// map'ten getiriyor. Onun yerine 1 hop topology getirebilir." Kart artık
// Topology sekmesinin grafiği (TopologyFlowGraph, odak sütun yerleşimi) kompakt
// kipte: TopologyPillGraph yok, sayfa kontrolü (zoom/pan) yok, adlar sabit
// genişlikli pilde elipsli + title, kart kendi kabında kayar; "+N more"
// tıklanamaz; kesim notu yalnız gerçek kesimde.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ChatTopologyCard, chatTopologyCutText } from './ChatTopologyCard';
import { chatTopologyColumns, chatTopologyLayoutFocus, chatTopologyMap } from './chatTopologyView';
import type { ChatTopologyPayload, GraphNode } from '@/lib/types';

class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }

const LONG = 'customer-notification-preference-management-service';
const node = (id: string, errorRate = 0, kind: GraphNode['kind'] = 'service'): GraphNode => ({ id, name: id.replace(/^ext:/, ''), kind, calls: 1200, errors: 0, errorRate, rate: 1 });
const fixture = (over: Partial<ChatTopologyPayload> = {}): ChatTopologyPayload => ({
  focus: 'payment-service', scope: 'neighborhood', hops: 1, window: { fromNs: 1, toNs: 2 }, rangeS: 3600,
  nodes: [node('checkout-service'), node(LONG), node('payment-service', 0.4), node('ledger-service', 6.1), node('ext:api.stripe.example', 0, 'external')],
  edges: [
    { source: 'checkout-service', target: 'payment-service', calls: 1200, errors: 5, errorRate: 0.4, rate: 20, avgMs: 40, p99Ms: 320 },
    { source: LONG, target: 'payment-service', calls: 600, errors: 0, errorRate: 0, rate: 10, avgMs: 30, p99Ms: 200 },
    { source: 'payment-service', target: 'ledger-service', calls: 900, errors: 55, errorRate: 6.1, rate: 15, avgMs: 90, p99Ms: 1200 },
    { source: 'payment-service', target: 'ext:api.stripe.example', calls: 300, errors: 4, errorRate: 1.3, rate: 5, avgMs: 200, p99Ms: 800 },
  ],
  deepLink: '/service?name=payment-service&tab=topology&hops=1',
  truncated: { nodes: 0, edges: 0 },
  ...over,
});

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  if (!('ResizeObserver' in globalThis)) (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});
const qc = new QueryClient({ defaultOptions: { queries: { retry: false, enabled: false } } });
const render = (p: ChatTopologyPayload) => act(() => {
  root.render(<MemoryRouter><QueryClientProvider client={qc}><ChatTopologyCard topo={p} /></QueryClientProvider></MemoryRouter>);
});
const hrefs = () => Array.from(host.querySelectorAll('a')).map(a => a.getAttribute('href'));

describe('ChatTopologyCard (v0.10.1392 — Topology sekmesinin 1 hop görünümü)', () => {
  it('sekmenin odak sütun grafiği, kompakt: pil grafiği yok, zoom/pan/lejant yok, kendi kabında', () => {
    render(fixture());
    const topo = host.querySelector('.cm-topo-flow > .topo');
    expect(topo).not.toBeNull();
    expect(topo!.className).toContain('focus-cols');
    expect(topo!.className).toContain('compact');
    expect(topo!.className).not.toContain('pannable');
    expect(host.querySelector('.topo-zoomctl')).toBeNull();
    expect(host.querySelector('.topo-legend')).toBeNull();
    // Kaynakta da eski pil grafiği yok.
    const src = readFileSync(resolve(__dirname, 'ChatTopologyCard.tsx'), 'utf8');
    expect(src).not.toMatch(/TopologyPillGraph/);
    // Sütun başlıkları sekmeyle aynı dil.
    const hdrs = Array.from(host.querySelectorAll('.topo-colhdr')).map(h => h.textContent);
    expect(hdrs).toEqual(['Callers · 2', 'Focus', 'Dependencies · 2']);
  });

  it('her düğüm pili: tam ad title\'da, ad satırı elips sınıfında (kırpma/üst üste binme yok)', () => {
    render(fixture());
    const pills = Array.from(host.querySelectorAll<HTMLElement>('.topo-node'));
    expect(pills).toHaveLength(5);
    for (const p of pills) {
      expect(p.getAttribute('title')).toBeTruthy();
      expect(p.querySelector('.topo-name')).not.toBeNull();
    }
    const long = pills.find(p => p.getAttribute('aria-label') === LONG)!;
    expect(long.getAttribute('title')).toContain(LONG);
    expect(host.querySelector('.topo-node.focus')?.getAttribute('aria-label')).toBe('payment-service');
    // Pil merkezleri sütun içinde ≥ bir satır aralığı (üst üste binmez) ve tuvalin içinde.
    const box = host.querySelector<HTMLElement>('.topo.compact')!;
    const W = parseFloat(box.style.width);
    const pos = pills.map(p => ({ x: parseFloat(p.style.left), y: parseFloat(p.style.top) }));
    for (const p of pos) {
      expect(p.x - 85).toBeGreaterThanOrEqual(0);
      expect(p.x + 85).toBeLessThanOrEqual(W);
    }
    const callers = pos.filter(p => p.x === pos[pills.indexOf(long)].x).map(p => p.y).sort((a, b) => a - b);
    expect(callers).toHaveLength(2);
    expect(callers[1] - callers[0]).toBeGreaterThanOrEqual(40);
  });

  it('linkler; kesim yokken "gösterilmedi" notu yok; ham düğme yok', () => {
    render(fixture());
    expect(host.querySelector('.ev-head')?.textContent).toContain('Topoloji · payment-service · 1 hop · son 1 sa');
    expect(hrefs()).toEqual(['/service?name=payment-service&tab=topology&hops=1']);
    expect(host.textContent).not.toContain('gösterilmedi');
    expect(host.textContent).not.toContain('more');
    expect(host.querySelector('button')).toBeNull();
  });

  it('sütun tavanı aşıldı: "+N more" tıklanamaz etiket, yön kırılımlı kesim notu', () => {
    const p = fixture({
      nodes: [...fixture().nodes, { id: 'more:-1', name: '+3 more', kind: 'service', calls: 90, errors: 0, errorRate: 0, rate: 1 }],
      edges: [...fixture().edges, { source: 'more:-1', target: 'payment-service', calls: 90, errors: 0, errorRate: 0, rate: 1, avgMs: 10, p99Ms: 50 }],
      truncated: { nodes: 3, edges: 3, callers: 3, callees: 0 },
    });
    render(p);
    const more = host.querySelector('.topo-more');
    expect(more?.tagName).toBe('SPAN');
    expect(more?.textContent).toBe('+3 more');
    expect(host.querySelector('button')).toBeNull();
    expect(host.textContent).toContain('+3 çağıran (+3 kenar) "+N more" altında');
    expect(chatTopologyColumns(p).get('more:-1')).toBe(-1);
  });

  it('trace kipi: operasyon başlıkta, trace linki, kök solda', () => {
    const p = fixture({ scope: 'trace', hops: 0, operation: 'PAYMENT_CREATE', traceId: '0af7651916cd43dd8448eb211c80319c', traceHref: '/trace?id=0af7651916cd43dd8448eb211c80319c' });
    render(p);
    expect(host.querySelector('.ev-head')?.textContent).toContain('örnek trace (PAYMENT_CREATE)');
    expect(hrefs()).toContain('/trace?id=0af7651916cd43dd8448eb211c80319c');
    expect(chatTopologyLayoutFocus(p)).toBe('checkout-service'); // gelen kenarı yok, en çok çağıran kök
  });
});

describe('chatTopologyView', () => {
  it('servis komşuluğu: çağıranlar −1, odak 0, bağımlılıklar +1', () => {
    const c = chatTopologyColumns(fixture());
    expect(Object.fromEntries(c)).toEqual({ 'payment-service': 0, 'checkout-service': -1, [LONG]: -1, 'ledger-service': 1, 'ext:api.stripe.example': 1 });
  });
  it('trace: kökten ileri derinlik; köke ulaşmayan düğüm en sağın bir sağında', () => {
    const p = fixture({ scope: 'trace', nodes: [...fixture().nodes, node('orphan-service')] });
    const c = chatTopologyColumns(p);
    expect(c.get('checkout-service')).toBe(0);
    expect(c.get('payment-service')).toBe(1);
    expect(c.get('ledger-service')).toBe(2);
    expect(c.get('orphan-service')).toBe(3);
  });
  it('düğüm adaptörü sekmeyle ortak: hata oranı kesir, dış düğüm kind=external', () => {
    const m = chatTopologyMap(fixture());
    expect(m.nodes.find(n => n.service === 'ledger-service')?.errorRate).toBeCloseTo(0.061);
    expect(m.nodes.find(n => n.service === 'ext:api.stripe.example')?.kind).toBe('external');
  });
  it('kesim notu yalnız gerçek kesimde', () => {
    expect(chatTopologyCutText(fixture())).toBe('');
    expect(chatTopologyCutText(fixture({ truncated: { nodes: 2, edges: 5 } }))).toBe('+2 düğüm, +5 kenar gösterilmedi');
  });
});
