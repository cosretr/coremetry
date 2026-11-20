// TopologyFlowGraph.focus.test.tsx — v0.10.1294 (operatör 2026-10-17:
// "oklar çok uzak ve sarmal bir path gibi gidiyor"). Saf çekirdek
// (lib/topoFocusLayout) yeşil olsa bile grafiğin onu KULLANDIĞI kanıtlanmalı
// ("test edilmiş ama ulaşılamaz" sınıfı): `columns` verilince odak sütun
// yerleşimi, varsayılan görünümde yalnız flow kenarları, durağan çizgi
// (anim-hover), "+N more ▸" atom butonu, sütun başlıkları. Global yol
// (columns yok) eski davranışta kalır.
import { describe, it, expect } from 'vitest';
import { renderToString } from 'react-dom/server';
import type React from 'react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TopologyFlowGraph } from './TopologyFlowGraph';
import { moreNodeId } from '../lib/topoFocusLayout';
import type { ServiceMap } from '../lib/types';

const qc = new QueryClient({ defaultOptions: { queries: { retry: false, enabled: false } } });
const wrap = (el: React.ReactElement, url = '/') => (
  <MemoryRouter initialEntries={[url]}><QueryClientProvider client={qc}>{el}</QueryClientProvider></MemoryRouter>
);

// odak f: çağıran c (−1), bağımlılıklar a, b (+1), 2-hop k (+2), "+3 more" (+1).
// a → b peer, c → b skip.
const MORE = moreNodeId(1);
const data: ServiceMap = {
  sampledFrom: 0, totalSpans: 0,
  nodes: [
    { service: 'f', spanCount: 100, errorRate: 0 },
    { service: 'c', spanCount: 50, errorRate: 0 },
    { service: 'a', spanCount: 40, errorRate: 0 },
    { service: 'b', spanCount: 30, errorRate: 0 },
    { service: 'k', spanCount: 10, errorRate: 0 },
    { service: MORE, spanCount: 9, errorRate: 0, subkind: '+3 more' },
  ],
  edges: [
    ['c', 'f'], ['f', 'a'], ['f', 'b'], ['a', 'k'], ['a', 'b'], ['c', 'b'], ['f', MORE],
  ].map(([caller, callee]) => ({ caller, callee, traceCount: 0, spanCount: 10, errorCount: 0, p99Ms: 5, rate: 1 })),
} as ServiceMap;
const columns = new Map<string, number>([['f', 0], ['c', -1], ['a', 1], ['b', 1], ['k', 2], [MORE, 1]]);

const edgePaths = (html: string) => [...html.matchAll(/<path[^>]*class="topo-edge-flow[^"]*"/g)];

describe('TopologyFlowGraph — odak sütun kipi (v0.10.1294)', () => {
  // SSR metin ayırıcı yorumları (<!-- -->) düşülür.
  const render = (extra: Partial<React.ComponentProps<typeof TopologyFlowGraph>> = {}, url?: string) => renderToString(wrap(
    <TopologyFlowGraph data={data} focus="f" hoverNode={null} onHoverNode={() => {}} onSelectNode={() => {}}
      dropMessaging={false} columns={columns} animate="hover" {...extra} />, url)).replace(/<!-- -->/g, '');

  it('varsayılan: yalnız flow kenarları (5), durağan (live yok), odak kipi sınıfları', () => {
    const html = render();
    expect(html).toContain('topo pannable focus-cols anim-hover');
    expect(edgePaths(html)).toHaveLength(5); // c→f, f→a, f→b, a→k, f→more
  });

  it('"Yan bağlantılar" açık: peer + skip da çizilir', () => {
    const html = render({ showSecondary: true });
    expect(edgePaths(html).length).toBe(edgePaths(render()).length + 2);
  });

  it('"+N more ▸" atom butonu (raw button değil, .topo-more)', () => {
    const html = render();
    expect(html).toMatch(/<button type="button" class="sec xs topo-more"[^>]*>(<span[^>]*>)?\+3 more ▸/);
  });

  it('sütun başlıkları', () => {
    const html = render();
    for (const l of ['Çağıranlar · 1', 'Odak', 'Bağımlılıklar · 2', 'Bağımlılıklar · 2 hop · 1']) expect(html).toContain(l);
  });

  // v0.10.1307 — operatör 2026-10-17: hop'u az olan tarafa boş 280 px sütun
  // ayrılıyordu. Çağıran yoksa sol taraf hiç yer almaz; odağın solunda soluk
  // "Çağıran yok (enstrümante değil)" notu boşluğun eksik veri olmadığını söyler.
  it('çağıran yok: sol sütun yok, soluk "Çağıran yok" notu; çağıran varsa not yok', () => {
    const noCallers: ServiceMap = {
      ...data,
      nodes: data.nodes.filter(n => n.service !== 'c'),
      edges: data.edges.filter(e => e.caller !== 'c'),
    };
    const cols = new Map([...columns].filter(([s]) => s !== 'c'));
    const html = render({ data: noCallers, columns: cols });
    expect(html).toContain('Bağımlılıklar');
    expect(html).not.toContain('Çağıranlar ·');
    expect(html).toMatch(/class="topo-colhdr topo-colhdr-empty"[^>]*>Çağıran yok \(enstrümante değil\)</);
    expect(render()).not.toContain('Çağıran yok');
  });

  it('bayat ?nsfold= odak kipinde yok sayılır (süper-düğüm yok)', () => {
    const html = render({}, '/?nsfold=x,y');
    expect(html).not.toContain('topo-nsnode');
  });

  it('hover: düğümün kendi ikincil kenarı soluk görünür, vurgulu kenar akar (live)', () => {
    const html = render({ hoverNode: 'b' });
    expect(edgePaths(html).length).toBe(edgePaths(render()).length + 2); // a→b, c→b
    expect(html).toMatch(/class="topo-edge-flow live"/);
  });

  // v0.10.1305 — Genel servis haritası: animate varsayılanı 'hover' → global
  // yol da durağan kenarlı (anim-hover); odak sınıfları yok.
  it('global yol (columns yok): odak sınıfı yok, varsayılan durağan (anim-hover)', () => {
    const html = renderToString(wrap(
      <TopologyFlowGraph data={data} focus={null} hoverNode={null} onHoverNode={() => {}} onSelectNode={() => {}} dropMessaging={false} />));
    expect(html).not.toContain('focus-cols');
    expect(html).toContain('anim-hover');
    expect(html).not.toMatch(/class="topo-edge-flow[^"]*live/);
  });

  it('global yol: hover ±2 hop yolu vurgular (reachPath), yoldaki kenar akar', () => {
    // c → f → a → k: hover a → c (2 hop yukarı) ve k yolda; c→f kenarı canlı.
    const html = renderToString(wrap(
      <TopologyFlowGraph data={data} focus={null} hoverNode="a" onHoverNode={() => {}} onSelectNode={() => {}} dropMessaging={false} />));
    const live = [...html.matchAll(/<path[^>]*class="topo-edge-flow[^"]*live"[^>]*>\s*<title>([^<→]+)→/g)].map(m => m[1].trim());
    expect(live).toContain('c'); // c → f: 2-hop çağıran zinciri (1-hop değil)
  });
});
