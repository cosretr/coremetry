import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.715 — Details dilim 1 kaynak pini: altı bölüm başlığı SectionHead
// atomuyla (kaynak rozeti), Endpoints bölümü monte, ToC girdisi, kip URL'de.
const svc = readFileSync(resolve(__dirname, '../Service.tsx'), 'utf8');
const toc = readFileSync(resolve(__dirname, 'DetailsToc.tsx'), 'utf8');
const ep = readFileSync(resolve(__dirname, 'DetailsEndpointsSection.tsx'), 'utf8');
const metrics = readFileSync(resolve(__dirname, 'DetailsMetricsSection.tsx'), 'utf8');

describe('Details bölüm başlıkları (v0.10.715)', () => {
  it('Service.tsx Details başlıkları SectionHead; ham dtl-sech kalmadı', () => {
    for (const id of ['dtl-clusters', 'dtl-db', 'dtl-perf', 'dtl-latency', 'dtl-runtime']) {
      expect(svc).toContain(`<SectionHead id="${id}"`);
    }
    expect(svc).not.toContain('className="dtl-sech"');
    expect(metrics).toContain('<SectionHead id="dtl-metrics"');
  });
  it('Endpoints bölümü monte + ToC girdisi + kip URL\'de (replace)', () => {
    expect(svc).toContain('<DetailsEndpointsSection service={svc} range={range} rangeNs={rangeNs} env={env} />');
    expect(toc).toContain("{ id: 'dtl-endpoints', label: 'Endpoints' }");
    expect(ep).toContain('<SectionHead id="dtl-endpoints"');
    expect(ep).toContain("next.set('epmode', 'cluster')");
    expect(ep).toContain('{ replace: true }');
    expect(ep).toContain("storageKey: 'svc-dtl-endpoints'");
    // multi-cluster: kapsam Topbar'dan; tek cluster kapsamında kip gizli
    expect(ep).toContain("const scopeCluster = sp.get('cluster') ?? ''");
    expect(ep).toContain("scopeCluster ? 'combined' : parseEndpointsMode(");
  });
});

// S0 karakterizasyon (docs/frontend/service-tabs-plan.md §5): bölüm
// kimlikleri (dtl-*) ve #deploys çapası derin link / CoSRE product_guide
// hedefidir — S1/S2 (kapalı satır, ToC kalkar) sonrasında da yaşar; sıra
// v0.10.151 operatör kararı (Clusters + Database en üstte). Clusters ve
// Database açık kalır (DBQueriesPanel defaultOpen).
describe('Details bölüm kimlikleri ve sırası (S0)', () => {
  const all = svc + ep + metrics;
  it('dtl-* kimlikleri ve #deploys çapası var', () => {
    for (const id of ['dtl-props', 'dtl-clusters', 'dtl-db', 'dtl-endpoints', 'dtl-perf', 'dtl-metrics', 'dtl-latency', 'dtl-runtime', 'deploys']) {
      expect(all).toMatch(new RegExp(`id="${id}"`));
    }
  });
  it('sıra: Props → Clusters → Database → Endpoints → Performance → Latency → Runtime', () => {
    const at = (s: string) => svc.indexOf(s);
    const order = ['id="dtl-props"', 'id="dtl-clusters"', 'id="dtl-db"', '<DetailsEndpointsSection', 'id="dtl-perf"', 'id="dtl-latency"', 'id="dtl-runtime"', 'id="deploys"'];
    for (const s of order) expect(at(s), s).toBeGreaterThan(-1);
    for (let i = 1; i < order.length; i++) expect(at(order[i]), order[i]).toBeGreaterThan(at(order[i - 1]));
  });
  it('Clusters ve Database açık: kapanır satıra alınmaz', () => {
    expect(svc).toContain('<ServiceClusterBreakdown service={svc} range={range} />');
    expect(svc).toMatch(/<DBQueriesPanel[\s\S]*?defaultOpen \/>/);
  });
});
