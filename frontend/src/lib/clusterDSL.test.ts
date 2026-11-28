import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.717 — multi-cluster ilkesi, Details dilim 2: Service.tsx kip
// parametresi, DB paneli cluster daraltması + entity linki, rollout satırında
// cluster. v0.10.2154 (operatör) — Performance bölümü kalktı: ServiceCharts
// kablolaması, clusterDSL conjunct'ı ve Performance kipi bu pinle birlikte
// düştü (tek tüketicileri o bölümdü).
const read = (p: string) => readFileSync(resolve(__dirname, '..', p), 'utf8');

describe('Details dilim 2 kablolama', () => {
  it("Service.tsx: kapsam Topbar'dan, DB kipi URL'de (replace), iki bölüm kablolu", () => {
    const src = read('pages/Service.tsx');
    expect(src).toContain("const scopeCluster = searchParams.get('cluster') ?? ''");
    expect(src).toContain("searchParams.get('dbmode') === 'cluster'");
    expect(src).toContain("if (on) next.set('dbmode', 'cluster'); else next.delete('dbmode');");
    expect(src).not.toContain('perfmode'); // v0.10.2154 — Performance kipi bölümle birlikte kalktı
    expect(src).toContain('byCluster={dbByCluster && !scopeCluster}');
    expect(src).toContain('<DeployHistoryPanel service={svc} cluster={scopeCluster} range={range}');
    // Kaynak rozeti dürüst: panel ham spans okur, MV değil.
    expect(src).not.toContain('title="Database" source="db_statement_summary_5m"');
  });
  it('DBQueriesPanel: kapsam sorguya, cluster başına birleştirme, entity linki, traces pivotu cluster taşır', () => {
    const src = read('components/DBQueriesPanel.tsx');
    expect(src).toContain('limit: DBQ_LIMIT + 1, cluster: cluster || undefined })');
    expect(src).toContain("queryKey: ['db-queries', service, from, to, c]");
    expect(src).toContain("kind: 'record', rec: { type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, opts: { range } }");
    expect(src).toContain('cluster: window.cluster || r.cluster || undefined');
  });
  it('DeployHistoryPanel: kapsam süzgeci + cluster linki', () => {
    const src = read('components/DeployHistoryPanel.tsx');
    expect(src).toContain('!cluster || !r.cluster || r.cluster === cluster');
    expect(src).toContain("kind: 'record', rec: { type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, opts: { range } }");
  });
  it('Endpoints: cluster hücresi entity linki, ortak toggle', () => {
    const src = read('pages/service/DetailsEndpointsSection.tsx');
    expect(src).toContain("<ClusterModeToggle value={mode === 'cluster'}");
    expect(src).toContain("kind: 'record', rec: { type: 'cluster', id: r.cluster"); // v0.10.2488 EntityLink
  });
});
