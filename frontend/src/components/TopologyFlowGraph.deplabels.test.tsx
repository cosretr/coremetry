// TopologyFlowGraph.deplabels.test.tsx — v0.10.837 (Operatör-bildirimli,
// prod): bir servisin Topology sekmesinde ALTI ayrı Oracle düğümü vardı ve
// GRAFİKTE hepsinin başlığı aynıydı (aynı db.name). Gerçek kimlik yalnız
// hover ipucunda ve kenar çubuğunda görünüyordu; yükleri ise tamamen
// farklıydı (103 çağrı %0 hata / 2.8K %1.3 / 67.5K %5.5), yani ayırt
// edememek operatörü YANLIŞ düğüme baktırıyordu.
//
// Bu dosya davranışı ÇİVİLER: saf çekirdek (lib/topoLabels.depPillLinesMap)
// yeşil olsa bile grafiğin onu çağırdığı KANITLANMALI — "test edilmiş ama
// ulaşılamaz" sınıfı. renderToString ile gerçek pil metni okunur; ayrımın
// yalnızca hover ipucuna (title attribute) konması bu testi kırmızıya
// düşürür.
//
// Gizlilik: tüm adlar sentetik (shop / db-core-a / shop_prd / example.com).
import { describe, it, expect } from 'vitest';
import { renderToString } from 'react-dom/server';
import type React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TopologyFlowGraph } from './TopologyFlowGraph';
import type { ServiceMap } from '../lib/types';

const qc = new QueryClient({ defaultOptions: { queries: { retry: false, enabled: false } } });
const wrap = (el: React.ReactElement) => (
  <MemoryRouter><QueryClientProvider client={qc}>{el}</QueryClientProvider></MemoryRouter>
);

const HOSTS = ['db-core-a', 'db-core-b', 'db-core-c', 'db-log-a', 'db-log-b', 'db-rep-a'];

// Operatörün ekranının sentetik karşılığı: bir arayan servis + AYNI
// db.name'i taşıyan altı FARKLI Oracle sunucusu.
function sixOracleMap(): ServiceMap {
  const nodes: ServiceMap['nodes'] = [
    { service: 'shop-payment', spanCount: 70_000, errorRate: 0.02 },
    ...HOSTS.map((h, i) => ({
      service: `db:oracle@${h}`,
      spanCount: [103, 2800, 67_500, 410, 9_100, 55][i],
      errorRate: [0, 0.013, 0.055, 0, 0.004, 0][i],
      kind: 'db',
      subkind: 'oracle',
      dbName: 'shop_prd',
    })),
  ];
  const edges: ServiceMap['edges'] = HOSTS.map(h => ({
    caller: 'shop-payment', callee: `db:oracle@${h}`,
    traceCount: 10, spanCount: 50, errorCount: 1,
  }));
  return { nodes, edges, sampledFrom: 0, totalSpans: 0 } as ServiceMap;
}

// Pil metinleri: (.topo-name, .topo-sub) ikilileri, DOM sırasıyla.
function pillLines(html: string): Array<{ title: string; sub: string }> {
  const re = /class="topo-name"[^>]*>(.*?)<\/div>.*?class="topo-sub"[^>]*>(.*?)<\/div>/gs;
  const out: Array<{ title: string; sub: string }> = [];
  for (const m of html.matchAll(re)) out.push({ title: strip(m[1]), sub: strip(m[2]) });
  return out;
}
function strip(s: string): string {
  return s.replace(/<!--.*?-->/gs, '').replace(/<[^>]*>/g, '').trim();
}

describe('TopologyFlowGraph — bağımlılık pili ayrımı (v0.10.837)', () => {
  const html = renderToString(wrap(
    <TopologyFlowGraph
      data={sixOracleMap()} focus={null} hoverNode={null}
      onHoverNode={() => {}} onSelectNode={() => {}} dropMessaging={false}
    />,
  ));
  const pills = pillLines(html);
  const dbPills = pills.filter(p => p.sub.includes('oracle'));

  it('altı Oracle düğümü çizilir', () => {
    expect(dbPills).toHaveLength(HOSTS.length);
  });

  it('altı düğümde ALTI FARKLI başlık metni görünür (hover gerekmez)', () => {
    const titles = dbPills.map(p => p.title);
    expect(new Set(titles).size, `başlıklar: ${titles.join(' | ')}`).toBe(HOSTS.length);
    expect(new Set(titles)).toEqual(new Set(HOSTS));
  });

  it('(başlık, alt satır) ikilileri tamamen benzersiz', () => {
    const pairs = dbPills.map(p => JSON.stringify([p.title, p.sub]));
    expect(new Set(pairs).size, `ikililer: ${pairs.join(' | ')}`).toBe(HOSTS.length);
  });

  it('db.name kaybolmaz — motor adının yanında alt satırda okunur', () => {
    for (const p of dbPills) expect(p.sub).toBe('oracle · shop_prd');
  });

  it('hover ipucu ve tam kimlik DARALTILMADI', () => {
    // Pill title attribute'u hâlâ düğümün tam id'sini ve db.name'i taşır.
    expect(html).toContain('db:oracle@db-core-a');
    expect(html).toContain('db.name: shop_prd');
  });

  it('çakışma YOKKEN bugünkü etiket (v0.10.517) korunur', () => {
    const map: ServiceMap = {
      nodes: [
        { service: 'shop-payment', spanCount: 100, errorRate: 0 },
        { service: 'db:oracle@db-core-a', spanCount: 10, errorRate: 0, kind: 'db', subkind: 'oracle', dbName: 'shop_prd' },
        { service: 'db:postgresql@pg-payments', spanCount: 10, errorRate: 0, kind: 'db', subkind: 'postgresql' },
      ],
      edges: [
        { caller: 'shop-payment', callee: 'db:oracle@db-core-a', traceCount: 1, spanCount: 1, errorCount: 0 },
        { caller: 'shop-payment', callee: 'db:postgresql@pg-payments', traceCount: 1, spanCount: 1, errorCount: 0 },
      ],
      sampledFrom: 0, totalSpans: 0,
    } as ServiceMap;
    const only = renderToString(wrap(
      <TopologyFlowGraph
        data={map} focus={null} hoverNode={null}
        onHoverNode={() => {}} onSelectNode={() => {}} dropMessaging={false}
      />,
    ));
    const p = pillLines(only);
    expect(p).toContainEqual({ title: 'shop_prd', sub: 'oracle' });
    expect(p).toContainEqual({ title: 'pg-payments', sub: 'postgresql' });
  });
});
