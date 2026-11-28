import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { stripTsComments } from './zLayers.test';

// healthThresholdRatchet — v0.10.2488 (entity tutarlılık denetimi dilim 4,
// docs/frontend/entity-coherence-audit.md §3.4 + §5.3, Karar A2).
//
// (a) Ölçü = denetimin M8 tanımı birebir: satır içi hata eşiği
//       (errorRate|errPct|errorPct)\s*(>=?)\s*([0-9.]+)
//     eşleşmesi olan SATIR sayısı. Kural: hata oranı sapma rengi tek yerde,
//     `lib/format/tone.ts` `errorTone(pct)`; sağlık noktası yalnız açık
//     Problem'den (`components/coremetry/HealthDot`). Yeni satır içi eşik
//     yazılmaz; mevcutlar errorTone'a taşındıkça tavan iner.
// (b) Tek sağlık noktası: `HealthDot` tanımı yalnız
//     components/coremetry/HealthDot.tsx'te; eski `serviceHeadDot` yok;
//     HealthDot.tsx hata oranı okumaz.
//
// Kapsam: src/**/*.{ts,tsx}; HARİÇ *.test.*, *.stories.*, *.d.ts,
// components/shadcn/**, testdata/, lib/format/** (errorTone'un kendisi).
// Yorumlar ayıklanır. Dosya başına tavan + TOTAL, YALNIZ AŞAĞI.
// Yeniden ölçüm: HEALTH_RATCHET_PRINT=1 npx vitest run src/styles/healthThresholdRatchet.test.ts
const SRC = resolve(__dirname, '..');

export const THRESHOLD_RE = /(errorRate|errPct|errorPct)\s*(>=?)\s*([0-9.]+)/;
const EXCLUDE = /^(components\/shadcn\/|lib\/format\/)|(^|\/)testdata\//;

export const thresholdLines = (src: string): number =>
  stripTsComments(src).split('\n').filter(l => THRESHOLD_RE.test(l)).length;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(p) && !/\.(test|stories)\.|\.d\.ts$/.test(p)) out.push(p);
  }
  return out;
}

const counts = new Map<string, number>();
const healthDotDefs: string[] = [];
const headDotDefs: string[] = [];
for (const p of walk(SRC)) {
  const rel = relative(SRC, p).split(sep).join('/');
  if (EXCLUDE.test(rel)) continue;
  const src = readFileSync(p, 'utf8');
  const n = thresholdLines(src);
  if (n) counts.set(rel, n);
  const code = stripTsComments(src);
  if (/\b(?:function|const)\s+HealthDot\b/.test(code)) healthDotDefs.push(rel);
  if (/\bserviceHeadDot\b/.test(code)) headDotDefs.push(rel);
}
const total = [...counts.values()].reduce((a, b) => a + b, 0);

if (process.env.HEALTH_RATCHET_PRINT) {
  console.log(`HEALTH_THRESHOLD lines=${total} files=${counts.size}\n${[...counts].sort().map(([f, n]) => `  '${f}': ${n},`).join('\n')}`);
}

const HINT = 'hata oranı eşiği: hücre rengi için lib/format/tone.ts errorTone(pct); '
  + 'sağlık noktası için components/coremetry/HealthDot (yalnız açık Problem)';

// Taban: denetim (v0.10.2482) 50 satır / 14 biçim. Aynı tanımla v0.10.2487:
// 50 / 33 dosya. Dilim 4 (v0.10.2488): 50 → 48 — Service.tsx serviceHeadDot
// (>5 / >1, iki satır) kalktı. lib/health.ts healthToken (topoloji düğüm
// tonu) eşiği pinli kalır (k5Residue: >5 / >1); errorTone'a geçişi dilim 5.
const TOTAL = 48;
const CEIL: Record<string, number> = {
  'components/DBQueriesPanel.tsx': 1,
  'components/DependenciesTable.tsx': 2,
  'components/ExternalPaths.tsx': 2,
  'components/RootCausePanel.tsx': 1,
  'components/ServiceMapNodeDrawer.tsx': 2,
  'components/TopologyFlowGraph.tsx': 3,
  'components/ai/ServiceReportCard.tsx': 1,
  'components/topology/FocusedNeighborhood.tsx': 2,
  'components/traces/ShapesView.tsx': 1,
  'features/anomalies/ProblemDetail.tsx': 1,
  'features/dependencies/CallerSection.tsx': 1,
  'features/dependencies/DetailDrawer.tsx': 2,
  'lib/health.ts': 1,
  'lib/serviceFilters.ts': 1,
  'pages/AIObservability.tsx': 1,
  'pages/AdminStats.tsx': 1,
  'pages/EndpointDetail.tsx': 2,
  'pages/Endpoints.tsx': 3,
  'pages/External.tsx': 2,
  'pages/MessagingTopic.tsx': 1,
  'pages/ServiceBacktrace.tsx': 2,
  'pages/Traces.tsx': 1,
  'pages/databases/detailSections.tsx': 2,
  'pages/endpoints/detailSections.tsx': 2,
  'pages/endpoints/opCallTreeCols.ts': 1,
  'pages/search/SearchDetailDrawer.tsx': 1,
  'pages/service/DetailsEndpointsSection.tsx': 1,
  'pages/service/DownstreamTargetsTable.tsx': 1,
  'pages/service/OperationsTable.tsx': 3,
  'pages/service/ServiceClusterBreakdown.tsx': 1,
  'pages/service/ServicePodsTable.tsx': 1,
  'pages/service/TopEndpointsCard.tsx': 1,
};

describe('healthThresholdRatchet (denetim M8, yalnız aşağı)', () => {
  it('dosya başına satır içi hata eşiği tavanı aşmaz', () => {
    const over = [...counts].filter(([f, n]) => n > (CEIL[f] ?? 0)).map(([f, n]) => `${f}: ${n} > ${CEIL[f] ?? 0}`);
    expect(over, `${HINT}\n${over.join('\n')}`).toEqual([]);
  });

  it('TOTAL aşılmaz', () => {
    expect(total, HINT).toBeLessThanOrEqual(TOTAL);
  });

  it('tavan listesi bayatlamaz', () => {
    const stale = Object.entries(CEIL).filter(([f, c]) => (counts.get(f) ?? 0) < c).map(([f, c]) => `${f}: ölçülen ${counts.get(f) ?? 0} < tavan ${c}`);
    if (total < TOTAL) stale.push(`TOTAL ${TOTAL} → ${total}`);
    expect(stale, 'sayı düştü — tavanı aynı commit\'te ölçülene indir').toEqual([]);
  });

  it('tek sağlık noktası: HealthDot tek tanım, hata oranı okumaz (Karar A2)', () => {
    expect(healthDotDefs).toEqual(['components/coremetry/HealthDot.tsx']);
    expect(headDotDefs).toEqual([]);
    const dot = stripTsComments(readFileSync(join(SRC, 'components/coremetry/HealthDot.tsx'), 'utf8'));
    expect(dot).not.toMatch(/errorRate|errPct|errorPct|errorTone/);
  });

  it('desen kaymadı: M8', () => {
    expect(thresholdLines('const c = errorRate > 5 ? a : errorRate > 1 ? b : c;')).toBe(1);
    expect(thresholdLines('x = s.errPct >= 0.5')).toBe(1);
    expect(thresholdLines('// errorRate > 5')).toBe(0);
    expect(thresholdLines('errorTone(s.errorRate)')).toBe(0);
  });
});
