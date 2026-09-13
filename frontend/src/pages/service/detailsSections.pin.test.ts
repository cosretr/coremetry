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
