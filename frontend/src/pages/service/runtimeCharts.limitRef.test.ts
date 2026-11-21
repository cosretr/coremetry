import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// S0 karakterizasyon (docs/frontend/service-tabs-plan.md §5, K11–K13):
// JVM bellek kartlarında limit serisi referans (ref) ve KESİK çizgi; pod
// çizgi etiketi podLineLabel sözleşmesinden. v0.10.2436 — 40 pod tavanı
// kalktı: pod seçimi runtimePodReduce'ta (medyan + en kötü + eşik üstü), çizim
// RuntimePodChart (CorePanel) üzerinden; TimeSeriesPanel kullanılmaz.
const src = readFileSync(resolve(__dirname, 'RuntimeCharts.tsx'), 'utf8');
const dotnet = readFileSync(resolve(__dirname, 'DotnetRuntimePanel.tsx'), 'utf8');
const pool = readFileSync(resolve(__dirname, 'RuntimePoolCards.tsx'), 'utf8');
const chart = readFileSync(resolve(__dirname, 'RuntimePodChart.tsx'), 'utf8');

describe('RuntimeCharts limit / etiket (S0)', () => {
  it('heap ve non-heap limit serisi ref:true; referans kesik', () => {
    const refs = src.match(/metric: 'jvm\.memory\.limit'[^}]*ref: true/g) ?? [];
    expect(refs.length).toBe(2);
    expect(src).toContain('dashed: !!line.ref');
  });
  it('pod çizgileri podLineLabel ile etiketlenir; 40 tavanı yok', () => {
    expect((src.match(/labelOf: podLineLabel/g) ?? []).length).toBeGreaterThanOrEqual(8);
    expect(src).not.toContain('FANOUT_MAX');
    expect(dotnet).not.toContain('FANOUT_MAX');
  });
});

describe('Runtime grafikleri tek motor (v0.10.2436)', () => {
  it('RuntimeCharts / .NET / havuz kartları RuntimePodChart; TimeSeriesPanel yok', () => {
    for (const f of [src, dotnet, pool]) {
      expect(f).toContain('<RuntimePodChart');
      expect(f).not.toContain('TimeSeriesPanel');
    }
  });
  it('RuntimePodChart CorePanelMulti\'yi lazy yükler, görünüm ?rtview\'dan', () => {
    expect(chart).toContain("lazy(() =>\n  import('@/components/chart/corePanelEntry')");
    expect(chart).toContain("parseRtView(params.get('rtview'))");
    expect(chart).toContain('reducePods(pods ?? []');
    expect(chart).not.toMatch(/localStorage|STORAGE_KEYS/);
  });
  it('heap kartı sınırın yüzdesi: pod başına TOPLAM okumalar', () => {
    expect(src.match(/agg: 'sum', role: 'pct(Used|Limit)'/g)?.length).toBe(2);
    expect(src).toContain('const pct = podPercentLines(pctUsed, pctLimit);');
  });
});

// v0.10.2149 (S10) — non-heap düzse cümle; yalnız JVM non-heap kartı aday,
// düzlük ham + kesilmemiş kümeden ve okumalar oturunca söylenir.
describe('RuntimeCharts non-heap düzlük (S10)', () => {
  it('aday yalnız JVM non-heap; okumalar oturmadan "düz" denmez', () => {
    expect(src).toContain("const isFlatCandidate = (family: string | null, card: CardSpec) => family === 'jvm' && card.key === 'nonheap';");
    expect(src).toContain("isFlatCandidate(family, card) && status === 'ready' ? flatSummary(flatInput) : null;");
  });
  it('cümle yalnız varsayılan görünümde; nonheap / lines grafiği açar', () => {
    expect(src).toContain('flat && !view.lines && !view.nonheap ? (');
  });
});
