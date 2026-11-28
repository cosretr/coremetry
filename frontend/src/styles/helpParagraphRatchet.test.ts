import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { stripTsComments } from './zLayers.test';

// helpParagraphRatchet — v0.10.2487 (entity tutarlılık denetimi dilim 3,
// docs/frontend/entity-coherence-audit.md §1.5 + §5.3).
//
// Ölçü = denetimin M9 tanımı birebir: ürün `.tsx` içinde
//   <p\b[^>]*className="[^"]*(note|hint|help|desc|intro|lede|explain|muted|sub|cap)
// Kural: veri YOKKEN açıklama → `Empty` gövdesi (components/Spinner Empty);
// veri VARKEN açıklama → başlıktaki `(?)` yardım balonu
// (components/coremetry/HelpTip). Kalıcı açıklama paragrafı yok.
//
// Kapsam i18nLeakRatchet ile aynı: src/**/*.tsx; HARİÇ *.test.*, *.stories.*,
// components/shadcn/** (üretilen), testdata/, /design. Yönetim kümesi
// (ADMIN, karar A6) dokunulmaz — sayılmaz. Yorumlar ayıklanır.
//
// Dosya başına tavan (CEIL) + TOTAL; listede olmayan dosyanın tavanı 0.
// YALNIZ AŞAĞI: sayı düşünce tavan aynı commit'te ölçülene iner.
// Yeniden ölçüm: HELP_RATCHET_PRINT=1 npx vitest run src/styles/helpParagraphRatchet.test.ts
const SRC = resolve(__dirname, '..');

export const HELP_P_RE = /<p\b[^>]*className="[^"]*(?:note|hint|help|desc|intro|lede|explain|muted|sub|cap)/g;
const ADMIN_RE = /^pages\/(?:(?:Admin\w*|System|Settings|Users)\.tsx$|(?:admin\w*|settings|system)\/)/;
const EXCLUDE = /^(components\/shadcn\/|pages\/Design\.tsx$|pages\/design\/)|(^|\/)testdata\//;

export const helpParagraphs = (src: string): number => (stripTsComments(src).match(HELP_P_RE) ?? []).length;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !/\.(test|stories)\./.test(p)) out.push(p);
  }
  return out;
}

const counts = new Map<string, number>();
for (const p of walk(SRC)) {
  const rel = relative(SRC, p).split(sep).join('/');
  if (EXCLUDE.test(rel) || ADMIN_RE.test(rel)) continue;
  const n = helpParagraphs(readFileSync(p, 'utf8'));
  if (n) counts.set(rel, n);
}
const total = [...counts.values()].reduce((a, b) => a + b, 0);

if (process.env.HELP_RATCHET_PRINT) {
  console.log(`HELP_P product=${total} files=${counts.size}\n${[...counts].sort().map(([f, n]) => `  '${f}': ${n},`).join('\n')}`);
}

const HINT = 'açıklama paragrafı: veri yokken Empty gövdesine (components/Spinner Empty), '
  + 'veri varken başlıktaki (?) yardım balonuna (components/coremetry/HelpTip) taşı';

// Taban: denetim (v0.10.2482) 93 / 57 dosya; v0.10.2486'da aynı tanımla 96 /
// 59. Dilim 3 (v0.10.2487) Problem ailesi: 96 → 90 (ProblemBreakdownBlocks,
// ProblemOriginBlocks, ProblemGraphSection açıklamaları (?) balonuna / Empty'ye).
const TOTAL = 90;
const CEIL: Record<string, number> = {
  'components/ai/CosreMemoryPanel.tsx': 1,
  'components/ai/CosreRulesPanel.tsx': 1,
  'components/coremetry/anomalies/EngineCompare.tsx': 1,
  'components/coremetry/anomalies/ImpactReplay.tsx': 2,
  'components/coremetry/anomalies/MLEventPlayer.tsx': 2,
  'components/coremetry/anomalies/MLLabelQueue.tsx': 1,
  'components/coremetry/anomalies/MLPrecisionShadowReport.tsx': 1,
  'components/coremetry/anomalies/MLPublishGates.tsx': 2,
  'components/coremetry/anomalies/MLRecordCard.tsx': 3,
  'components/coremetry/anomalies/MLRecordChart.tsx': 3,
  'components/coremetry/anomalies/MLReplaySheet.tsx': 6,
  'components/coremetry/anomalies/MLSignalEvidence.tsx': 4,
  'components/coremetry/anomalies/MLSourceChart.tsx': 2,
  'components/coremetry/anomalies/MLSvcShadowCompare.tsx': 1,
  'components/coremetry/anomalies/MLTab.tsx': 2,
  'components/coremetry/anomalies/MLTraceDots.tsx': 2,
  'components/coremetry/anomalies/MLTraceHeatmap.tsx': 2,
  'components/coremetry/anomalies/ProblemGraphShadow.tsx': 1,
  'components/coremetry/anomalies/UnifiedAnomalies.tsx': 1,
  'components/coremetry/dashboard/GrafanaImportDialog.tsx': 1,
  'components/coremetry/dashboard/PanelQueryEditor.tsx': 1,
  'components/coremetry/dashboard/PromqlTableViews.tsx': 1,
  'components/coremetry/dashboard/TemplateGallery.tsx': 1,
  'components/coremetry/logs/DisplayOptions.tsx': 1,
  'components/coremetry/logs/PatternCompare.tsx': 1,
  'components/coremetry/logs/TraceLogsView.tsx': 1,
  'components/coremetry/problems/ProblemBreakdownBlocks.tsx': 3,
  'components/coremetry/problems/ProblemGraphDiagram.tsx': 1,
  'components/coremetry/problems/ProblemGraphTable.tsx': 2,
  'components/coremetry/problems/ProblemOriginBlocks.tsx': 3,
  'components/coremetry/problems/ProblemRepeatBlock.tsx': 1,
  'components/cosre/conversation/ApprovalCard.tsx': 1,
  'components/cosre/conversation/CosreWelcomeV2.tsx': 1,
  'components/cosre/conversation/EndpointPickCard.tsx': 1,
  'components/cosre/conversation/SummaryLinkCard.tsx': 1,
  'components/cosre/conversation/TeamPickCard.tsx': 1,
  'components/cosre/evidence/StepCard.tsx': 2,
  'components/cosre/evidence/parts/ExceptionPartCard.tsx': 1,
  'features/anomalies/AnomaliesPage.tsx': 1,
  'features/anomalies/AnomalyStreamsPage.tsx': 2,
  'features/tour/TourOverlay.tsx': 1,
  'pages/Clusters.tsx': 3,
  'pages/Pod.tsx': 1,
  'pages/ai/PendingCasesPanel.tsx': 3,
  'pages/ai/SREEvalPanel.tsx': 1,
  'pages/clusters/MetricArea.tsx': 1,
  'pages/clusters/NamespaceDetail.tsx': 2,
  'pages/databases/DbColumnFilter.tsx': 1,
  'pages/login/LoginSummary.tsx': 1,
  'pages/metrics/DiscoverTab.tsx': 2,
  'pages/metrics/ResultPanel.tsx': 1,
  'pages/search/FacetRail.tsx': 1,
  'pages/search/SearchDetailDrawer.tsx': 1,
  'pages/service/DetailsEndpointsSection.tsx': 1,
  'pages/service/ServiceInfraTab.tsx': 1,
  'pages/service/ServicePodInventory.tsx': 1,
  'pages/trace/TracePodPanel.tsx': 1,
  'pages/traces/ColumnFilter.tsx': 1,
};

describe('helpParagraphRatchet (denetim M9, yalnız aşağı)', () => {
  it('dosya başına açıklama paragrafı tavanı aşmaz', () => {
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

  it('desen kaymadı: M9', () => {
    expect(helpParagraphs('<p className="muted">x</p>')).toBe(1);
    expect(helpParagraphs('<p className="pd-note small">x</p>')).toBe(1);
    expect(helpParagraphs('<p className="title">x</p>')).toBe(0);
    expect(helpParagraphs('<div className="hint">x</div>')).toBe(0);
    expect(helpParagraphs('// <p className="hint">x</p>')).toBe(0);
  });
});
