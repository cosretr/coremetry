import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { ClusterChips } from './ClusterChips';

// v0.10.1663 — Operator-reported: küme adı rozetleri her yerde BÜYÜK harf.
// Yalnız gösterim (.cluster-name → text-transform); href/title özgün yazım.
const read = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');

describe('cluster-name rozetleri', () => {
  it('.cluster-name text-transform: uppercase', () => {
    expect(read('../styles/globals.css')).toMatch(/\.cluster-name\s*\{\s*text-transform:\s*uppercase;/);
  });
  it('ClusterChips sınıfı taşır, metin/href/title özgün yazım', () => {
    const html = renderToStaticMarkup(<MemoryRouter><ClusterChips clusters={['prod-eu-west']} /></MemoryRouter>);
    expect(html).toMatch(/<a [^>]*class="cluster-name"[^>]*>prod-eu-west<\/a>/);
    expect(html).toContain('href="/services?cluster=prod-eu-west"');
    expect(html).toContain('title="Show /services scoped to prod-eu-west"');
  });
  it.each([
    ['SpanK8sSection.tsx', /className="sec cluster-name" title=\{ctx\.clusterId\}>\{ctx\.clusterName\}/g, 2],
    ['TraceWaterfall.tsx', /className="wf-cluster cluster-name"/g, 1],
    ['../pages/Clusters.tsx', /className="cluster-name"/g, 2],
    ['../pages/service/ServiceClusterBreakdown.tsx', /className="mono cluster-name"/g, 1],
    ['../pages/service/ServiceInfraTab.tsx', /className="cluster-name"[^>]*>\{r\.cluster\}/g, 1],
  ] as const)('%s küme rozeti .cluster-name taşır', (f, re, n) => {
    expect(read(f).match(re)?.length ?? 0).toBe(n);
  });
  it('namespace/pod linkleri büyük harfe çevrilmez', () => {
    expect(read('SpanK8sSection.tsx')).not.toMatch(/cluster-name[^"]*"[^>]*>\{ctx\.(namespace|pod)\}/);
  });
});
