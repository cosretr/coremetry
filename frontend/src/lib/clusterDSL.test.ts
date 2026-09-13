import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { clusterDSL } from './entrySpans';

// v0.10.717 — multi-cluster ilkesi, Details dilim 2: DSL conjunct'ı,
// ServiceCharts kapsam/kip kablolaması, Service.tsx kip parametreleri,
// DB paneli cluster daraltması + entity linki, rollout satırında cluster.
const read = (p: string) => readFileSync(resolve(__dirname, '..', p), 'utf8');

describe('clusterDSL', () => {
  it('boş → boş; dolu → AND cluster = "…" (tırnak kaçışlı)', () => {
    expect(clusterDSL('')).toBe('');
    expect(clusterDSL('prod-eu')).toBe(' AND cluster = "prod-eu"');
    expect(clusterDSL('a"b')).toBe(' AND cluster = "a\\"b"');
  });
});

describe('Details dilim 2 kablolama', () => {
  it('ServiceCharts: clusterDSL + groupBy cluster + resolver dışı + deps', () => {
    const src = read('components/ServiceCharts.tsx');
    expect(src).toContain('dsl += clusterDSL(cluster);');
    expect(src).toContain("byCluster ? (opScope ? ['cluster'] : ['name', 'cluster'])");
    expect(src).toContain('!rootOnly && !env && !cluster && !byCluster');
    expect(src).toContain('rootOnly, env, cluster, byCluster],');
  });
  it("Service.tsx: kapsam Topbar'dan, kipler URL'de (replace), üç bölüm kablolu", () => {
    const src = read('pages/Service.tsx');
    expect(src).toContain("const scopeCluster = searchParams.get('cluster') ?? ''");
    expect(src).toContain("searchParams.get('dbmode') === 'cluster'");
    expect(src).toContain("searchParams.get('perfmode') === 'cluster'");
    expect(src).toContain('cluster={scopeCluster} byCluster={perfByCluster && !scopeCluster}');
    expect(src).toContain('byCluster={dbByCluster && !scopeCluster}');
    expect(src).toContain('<DeployHistoryPanel service={svc} cluster={scopeCluster} range={range}');
    // Kaynak rozeti dürüst: panel ham spans okur, MV değil.
    expect(src).not.toContain('title="Database" source="db_statement_summary_5m"');
  });
  it('DBQueriesPanel: kapsam sorguya, cluster başına birleştirme, entity linki, traces pivotu cluster taşır', () => {
    const src = read('components/DBQueriesPanel.tsx');
    expect(src).toContain('limit: DBQ_LIMIT + 1, cluster: cluster || undefined })');
    expect(src).toContain("queryKey: ['db-queries', service, from, to, c]");
    expect(src).toContain("entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, { range })");
    expect(src).toContain('cluster: window.cluster || r.cluster || undefined');
  });
  it('DeployHistoryPanel: kapsam süzgeci + cluster linki', () => {
    const src = read('components/DeployHistoryPanel.tsx');
    expect(src).toContain('!cluster || !r.cluster || r.cluster === cluster');
    expect(src).toContain("entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, { range })");
  });
  it('Endpoints: cluster hücresi entity linki, ortak toggle', () => {
    const src = read('pages/service/DetailsEndpointsSection.tsx');
    expect(src).toContain("<ClusterModeToggle value={mode === 'cluster'}");
    expect(src).toContain("entityHref({ type: 'cluster', id: r.cluster");
  });
});
