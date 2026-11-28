import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { stripTsComments } from './zLayers.test';

// formatUnityRatchet — v0.10.2484 (entity tutarlılık denetimi dilim 1,
// docs/frontend/entity-coherence-audit.md BÖLÜM 2 + §5.3).
//
// Biçimleme TEK modülde: `lib/format/` (number · duration · percent · bytes ·
// time · TimeLabel `<Time>` · tone `errorTone`). İki sayaç, denetimin M5/M6
// tanımlarıyla birebir:
//  (a) RAW — ham biçimleme çağrısı (M6): `.toFixed(` · `.toLocaleString(` /
//      `.toLocaleDateString(` / `.toLocaleTimeString(` · `new Intl.NumberFormat`
//      / `DateTimeFormat` / `RelativeTimeFormat`.
//  (b) DEF — yerel biçimleyici TANIMI (M5): `(function|const) (fmt|format)X…`,
//      `…ago(TR|Lang|Text|Label)`, `duration(TR|Label)…`, `ts(Short|Long|…)…`;
//      ad anahtar sözcüğüyle sınıflanır (bayt / zaman / süre / yüzde / sayı),
//      değer biçimlemeyen adlar (formatChip, formatSortParam …) sayılmaz.
// Kapsam: src/**/*.{ts,tsx}; HARİÇ *.test.*, *.stories.*, *.d.ts,
// components/shadcn/** (üretilen), testdata/, lib/format/** (modülün kendisi).
// Yorumlar sayımdan önce ayıklanır (stripTsComments). Yönetim kümesi (ADMIN:
// pages/(Admin*|System|Settings|Users).tsx + pages/(admin*|settings|system)/**)
// ayrı TOTAL taşır ve hedef dışıdır (/admin + Settings çevrilmez) — tavanı
// yine yalnız aşağı iner. components/ui donuk: içindeki çağrılar sayılır,
// tavanı sabit.
//
// Dosya başına tavan (CEIL_*) + TOTAL; listede olmayan dosyanın tavanı 0.
// YALNIZ AŞAĞI: bir dosya göç edince tavan aynı commit'te düşer. Tabanlar
// dilim 1 SONUNDA ölçüldü (denetim v0.10.2482: RAW 873, DEF 164).
// Yeniden ölçüm: FORMAT_RATCHET_PRINT=1 npx vitest run src/styles/formatUnityRatchet.test.ts
const SRC = resolve(__dirname, '..');

const RAW_RE = /\.toFixed\(|\.toLocale(?:String|DateString|TimeString)\(|new Intl\.(?:NumberFormat|DateTimeFormat|RelativeTimeFormat)/g;
const DEF_RE = /(?:function|const)\s+((?:fmt|format)[A-Z0-9]\w*|\w*[Aa]go(?:TR|Lang|Text|Label)?|duration(?:TR|Label)\w*|ts(?:Short|Long|DateTime|Compact|Minute|Rel)\w*)/g;
const ADMIN_RE = /^pages\/(?:(?:Admin\w*|System|Settings|Users)\.tsx$|(?:admin\w*|settings|system)\/)/;

// M5 sınıflaması — ilk eşleşen sınıf; hiçbiri değilse değer biçimleyici sayılmaz.
const CLASSES: Array<[string, RegExp]> = [
  ['bayt', /Bytes|MB/],
  ['zaman', /[Aa]go|Clock|Time|Date|Tick|^ts[A-Z]|Ts|Age|Rel\b|HourMinute/],
  ['süre', /Dur|^duration|Ms|Ns|Sec|Step|Eta|Uptime|Runtime|Hours|Horizon|Load/],
  ['yüzde', /Pct|Ratio|Share/],
  ['sayı', /Num|Count|Rps|Rate|Compact|Int\b|Cores|Total|Millions|Smart|Token|Rows|Bps|Avg|Dec2|Signed|Val\b|Cost|Score|Delta|P95|Fixed|Floor|Impact|Thr|PerMin|Unit/],
];
export function formatterClass(name: string): string | null {
  for (const [k, re] of CLASSES) if (re.test(name)) return k;
  return null;
}

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !/\.(test|stories)\./.test(p) && !p.endsWith('.d.ts')) out.push(p);
  }
  return out;
}
const EXCLUDE = /^(components\/shadcn\/|lib\/format\/)|(^|\/)testdata\//;

const raw = new Map<string, number>();
const defs = new Map<string, number>();
for (const p of walk(SRC)) {
  const rel = relative(SRC, p).split(sep).join('/');
  if (EXCLUDE.test(rel)) continue;
  const code = stripTsComments(readFileSync(p, 'utf8'));
  const r = (code.match(RAW_RE) ?? []).length;
  if (r) raw.set(rel, r);
  let d = 0;
  for (const m of code.matchAll(DEF_RE)) if (formatterClass(m[1])) d++;
  if (d) defs.set(rel, d);
}
const sum = (m: Map<string, number>, admin: boolean) =>
  [...m].reduce((a, [f, n]) => a + (ADMIN_RE.test(f) === admin ? n : 0), 0);

const HINT_RAW = 'ham toFixed / toLocale* / new Intl yerine lib/format kullan: '
  + 'number (count · compact · decimal · fixed), duration (duration · durationNs · durationSec), '
  + 'percent (percent, from: ratio|pct), bytes, time (relTime · absTime · clock), <Time> (lib/format/TimeLabel)';
const HINT_DEF = 'yeni yerel fmt*/format*/ago*/ts* biçimleyicisi yerine lib/format işlevini kullan '
  + '(eksikse oraya, testiyle ekle)';

if (process.env.FORMAT_RATCHET_PRINT) {
  const dump = (m: Map<string, number>) => [...m].sort(([a], [b]) => a.localeCompare(b)).map(([f, n]) => `  '${f}': ${n},`).join('\n');
  console.log(`RAW product=${sum(raw, false)} admin=${sum(raw, true)}\n${dump(raw)}\nDEF product=${sum(defs, false)} admin=${sum(defs, true)}\n${dump(defs)}`);
}

describe('formatUnityRatchet (denetim M5/M6, yalnız aşağı)', () => {
  it('(a) RAW: dosya başına ham biçimleme çağrısı tavanı aşmaz', () => {
    const over = [...raw].filter(([f, n]) => n > (CEIL_RAW[f] ?? 0)).map(([f, n]) => `${f}: ${n} > ${CEIL_RAW[f] ?? 0}`);
    expect(over, `${HINT_RAW}\n${over.join('\n')}`).toEqual([]);
  });

  it('(a) RAW TOTAL: ürün ve yönetim kümesi ayrı', () => {
    expect(sum(raw, false), HINT_RAW).toBeLessThanOrEqual(TOTAL_RAW_PRODUCT);
    expect(sum(raw, true), HINT_RAW).toBeLessThanOrEqual(TOTAL_RAW_ADMIN);
  });

  it('(b) DEF: dosya başına yerel biçimleyici tanımı tavanı aşmaz', () => {
    const over = [...defs].filter(([f, n]) => n > (CEIL_DEF[f] ?? 0)).map(([f, n]) => `${f}: ${n} > ${CEIL_DEF[f] ?? 0}`);
    expect(over, `${HINT_DEF}\n${over.join('\n')}`).toEqual([]);
  });

  it('(b) DEF TOTAL: ürün ve yönetim kümesi ayrı', () => {
    expect(sum(defs, false), HINT_DEF).toBeLessThanOrEqual(TOTAL_DEF_PRODUCT);
    expect(sum(defs, true), HINT_DEF).toBeLessThanOrEqual(TOTAL_DEF_ADMIN);
  });

  it('tavan listesi bayatlamaz: tavanlı her dosya var ve tavanı ölçülenden büyük değil', () => {
    const stale = [
      ...Object.entries(CEIL_RAW).filter(([f, c]) => (raw.get(f) ?? 0) < c).map(([f, c]) => `RAW ${f}: ölçülen ${raw.get(f) ?? 0} < tavan ${c}`),
      ...Object.entries(CEIL_DEF).filter(([f, c]) => (defs.get(f) ?? 0) < c).map(([f, c]) => `DEF ${f}: ölçülen ${defs.get(f) ?? 0} < tavan ${c}`),
    ];
    expect(stale, 'sayı düştü — tavanı aynı commit\'te ölçülene indir').toEqual([]);
  });

  it('desen kaymadı: sınıflama ve hariç tutma', () => {
    expect(formatterClass('fmtMs')).toBe('süre');
    expect(formatterClass('fmtBytes')).toBe('bayt');
    expect(formatterClass('agoLabel')).toBe('zaman');
    expect(formatterClass('fmtPct')).toBe('yüzde');
    expect(formatterClass('fmtCount')).toBe('sayı');
    expect(formatterClass('formatChip')).toBeNull();
    expect(formatterClass('formatSortParam')).toBeNull();
    expect([...raw.keys()].some(f => f.startsWith('lib/format/'))).toBe(false);
    expect(ADMIN_RE.test('pages/AdminClickhouse.tsx')).toBe(true);
    expect(ADMIN_RE.test('pages/settings/aiEval.ts')).toBe(true);
    expect(ADMIN_RE.test('pages/Service.tsx')).toBe(false);
  });
});

// ── Tabanlar (v0.10.2484, dilim 1 sonu ölçümü) ────────────────────────────
const TOTAL_RAW_PRODUCT = 648; // v0.10.2487 dilim 3: 655 → 648; v0.10.2484 dilim 1: 766 → 655 (aynı tanımla v0.10.2483 ölçümü 766)
const TOTAL_RAW_ADMIN = 118; // hedef dışı (yönetim), yalnız aşağı
const TOTAL_DEF_PRODUCT = 137; // v0.10.2484 dilim 1: 146 → 137
const TOTAL_DEF_ADMIN = 19; // hedef dışı (yönetim), yalnız aşağı

const CEIL_RAW: Record<string, number> = {
  'components/AffectedEntitiesList.tsx': 1,
  'components/ai/ChartsSignalsCard.tsx': 5,
  'components/ai/chatHistoryGroups.ts': 2,
  'components/ai/ChatTraceList.tsx': 2,
  'components/ai/EvidenceCard.tsx': 1,
  'components/ai/greeting.ts': 1,
  'components/ai/logSpikeView.ts': 1,
  'components/ai/OperationCard.tsx': 2,
  'components/ai/operationCardView.ts': 1,
  'components/ai/ServiceReportCard.tsx': 4,
  'components/ai/sourceChips.ts': 1,
  'components/ai/stepPreview.ts': 2,
  'components/ai/toolSteps.ts': 1,
  'components/ai/TraceNarrativeBody.tsx': 2,
  'components/BubbleUpPanel.tsx': 4,
  'components/coremetry/anomalies/ImpactReplay.tsx': 3,
  'components/coremetry/anomalies/MLPublishGates.tsx': 1,
  'components/coremetry/anomalies/mlRecord.ts': 1,
  'components/coremetry/anomalies/MLReplaySheet.tsx': 1,
  'components/coremetry/anomalies/MLSignalEvidence.tsx': 3,
  'components/coremetry/anomalies/mlSourcePanel.ts': 2,
  'components/coremetry/anomalies/MLTab.tsx': 14,
  'components/coremetry/anomalies/unifiedRows.ts': 2,
  'components/coremetry/logs/CompactLogSearch.tsx': 4,
  'components/coremetry/logs/DisplayOptions.tsx': 2,
  'components/coremetry/logs/LogQueryInput.tsx': 2,
  'components/coremetry/logs/PatternCompare.tsx': 4,
  'components/coremetry/logs/ServiceLogSummary.tsx': 1,
  'components/coremetry/logs/TraceChip.tsx': 3,
  'components/coremetry/logs/TraceLogsView.tsx': 4,
  'components/coremetry/problems/ProblemSourceBreakdown.tsx': 1,
  'components/coremetry/ServiceHealthcheck.tsx': 1,
  'components/CorrelationContextDrawer.tsx': 2,
  'components/cosre/conversation/endpointPick.ts': 1,
  'components/cosre/evidence/stepView.ts': 1,
  'components/dashboard/PanelRenderer.tsx': 7,
  'components/DBQueriesPanel.tsx': 7,
  'components/DependenciesTable.tsx': 12,
  'components/ExternalPaths.tsx': 4,
  'components/FilterBuilder.tsx': 1,
  'components/FlameDiff.tsx': 8,
  'components/FlameGraph.tsx': 5,
  'components/HeatmapCellExemplars.tsx': 2,
  'components/KindBadge.tsx': 2,
  'components/LatencyHeatmap.tsx': 3,
  'components/LogContextModal.tsx': 2,
  'components/LogPatternsPanel.tsx': 7,
  'components/LogsHistogram.tsx': 2,
  'components/MethodHotspots.tsx': 3,
  'components/Pager.tsx': 4,
  'components/perf/PerfMeter.tsx': 1,
  'components/RCAVerdictPanel.tsx': 6,
  'components/RolloutDrawer.tsx': 4,
  'components/RootCausePanel.tsx': 7,
  'components/RootCauseRibbon.tsx': 1,
  'components/ServiceAttentionStrip.tsx': 2,
  'components/ServiceNeighbors.tsx': 5,
  'components/Sparkline.tsx': 3,
  'components/topology/FocusedNeighborhood.tsx': 2,
  'components/TopologyFlowGraph.tsx': 11,
  'components/traces/ServiceTimeline.tsx': 1,
  'components/traces/ShapesView.tsx': 2,
  'components/traces/shared.tsx': 2,
  'components/traces/TraceHonesty.tsx': 3,
  'components/traces/TraceLogList.tsx': 2,
  'components/traces/volumeSeries.ts': 2,
  'components/TraceWaterfall.tsx': 7,
  'components/TrendDelta.tsx': 2,
  'components/TrendSpark.tsx': 2,
  'components/ui/PickerPopover.tsx': 1,
  'features/anomalies/alertMetricSeries.ts': 2,
  'features/anomalies/anomalyDetailParts.tsx': 1,
  'features/anomalies/detailSummary.ts': 1,
  'features/anomalies/exceptionSamples.ts': 1,
  'features/anomalies/ExternalEvidencePanel.tsx': 3,
  'features/anomalies/hypothesisView.ts': 1,
  'features/anomalies/incidentCaseView.ts': 2,
  'features/anomalies/investigationResultView.ts': 2,
  'features/anomalies/ProblemDetail.tsx': 5,
  'features/anomalies/problemEvents.ts': 6,
  'features/anomalies/problemInsight.ts': 3,
  'features/anomalies/ProblemLogEvidence.tsx': 4,
  'features/anomalies/ProblemNewLogTemplates.tsx': 1,
  'features/anomalies/problemTime.ts': 2,
  'features/anomalies/streams.tsx': 3,
  'features/dependencies/CallerSection.tsx': 5,
  'features/dependencies/panels/MySQLPanel.tsx': 8,
  'features/dependencies/panels/OraclePanel.tsx': 4,
  'features/dependencies/panels/PostgresPanel.tsx': 5,
  'features/dependencies/panels/RedisPanel.tsx': 5,
  'features/dependencies/panels/shared.tsx': 11,
  'lib/ai-rates.ts': 5,
  'lib/anomalyBaseline.ts': 1,
  'lib/anomalyNarrative.ts': 1,
  'lib/anomalyView.ts': 1,
  'lib/chart/overlays.ts': 2,
  'lib/chartFmt.ts': 16,
  'lib/chatEvidence.ts': 8,
  'lib/esRequests.ts': 2,
  'lib/externalLinks.ts': 1,
  'lib/fmtEta.ts': 3,
  'lib/fmtEtaHours.ts': 2,
  'lib/httpMetrics.ts': 5,
  'lib/impactSummaryView.ts': 3,
  'lib/inboxRowText.ts': 2,
  'lib/logFieldLift.ts': 2,
  'lib/logFieldSample.ts': 1,
  'lib/loginSummary.ts': 5,
  'lib/logsGroup.ts': 1,
  'lib/logsScope.ts': 1,
  'lib/metricsExplorer/discover.ts': 3,
  'lib/mlBaselineBackfill.ts': 2,
  'lib/mlProfiles.ts': 2,
  'lib/problemBreakdown.ts': 1,
  'lib/problemStandardView.ts': 6,
  'lib/serverTiming.ts': 1,
  'lib/traceAiContext.ts': 1,
  'lib/traceNarrative.ts': 2,
  'lib/whyNotProblem.ts': 1,
  'pages/AdminCardinality.tsx': 7,
  'pages/adminch/replicaConsistency.ts': 1,
  'pages/adminch/traceHealth.ts': 3,
  'pages/adminch/wizards.ts': 1,
  'pages/AdminClickhouse.tsx': 27,
  'pages/AdminElastic.tsx': 1,
  'pages/AdminK8sCoverage.tsx': 3,
  'pages/AdminStats.tsx': 9,
  'pages/adminstats/panels.tsx': 8,
  'pages/adminstats/shared.tsx': 3,
  'pages/adminstats/spoolOrder.ts': 5,
  'pages/adminstats/StatusSection.tsx': 1,
  'pages/ai/aiBudgetView.ts': 3,
  'pages/ai/ExchangesPanel.tsx': 1,
  'pages/ai/exchangeView.ts': 2,
  'pages/ai/failedTurnsView.ts': 2,
  'pages/ai/rcaQualityView.ts': 4,
  'pages/ai/routerGapsView.ts': 1,
  'pages/ai/routingReportView.ts': 2,
  'pages/ai/SREEvalCompare.tsx': 1,
  'pages/ai/SREEvalPanel.tsx': 1,
  'pages/ai/sreEvalView.ts': 2,
  'pages/AIObservability.tsx': 7,
  'pages/alerts/NoisyRulesPanel.tsx': 2,
  'pages/alerts/StatementPicker.tsx': 2,
  'pages/alerts/ThresholdField.tsx': 1,
  'pages/Clusters.tsx': 2,
  'pages/clusters/podUsage.ts': 1,
  'pages/clusters/thresholds.ts': 2,
  'pages/Databases.tsx': 4,
  'pages/databases/DatabaseErrorsSection.tsx': 1,
  'pages/databases/databaseProblems.ts': 2,
  'pages/databases/errRateDelta.ts': 1,
  'pages/EndpointDetail.tsx': 9,
  'pages/Endpoints.tsx': 13,
  'pages/endpoints/detailSections.tsx': 19,
  'pages/endpoints/OperationCardSection.tsx': 2,
  'pages/Explore.tsx': 2,
  'pages/explore/GroupTable.tsx': 1,
  'pages/explore/RepeatsResult.tsx': 1,
  'pages/explore/SummaryViz.tsx': 2,
  'pages/explore/TracesResult.tsx': 1,
  'pages/External.tsx': 6,
  'pages/Hosts.tsx': 3,
  'pages/login/statusLine.ts': 2,
  'pages/Logs.tsx': 9,
  'pages/logs/LogsActionsMenus.tsx': 1,
  'pages/logs/LogsExplain.tsx': 1,
  'pages/logs/LogsTableToolbar.tsx': 6,
  'pages/MessagingTopic.tsx': 7,
  'pages/Monitors.tsx': 2,
  'pages/pod/PodServicesTable.tsx': 3,
  'pages/Profile.tsx': 2,
  'pages/Profiling.tsx': 4,
  'pages/PublicStatus.tsx': 4,
  'pages/PublicTrace.tsx': 1,
  'pages/Rollouts.tsx': 2,
  'pages/search/SearchDetailDrawer.tsx': 4,
  'pages/Service.tsx': 3, // v0.10.2487 dilim 3
  'pages/service/DetailsEndpointsSection.tsx': 4,
  'pages/service/DotnetRuntimePanel.tsx': 3,
  'pages/service/DownstreamTargetsTable.tsx': 2,
  'pages/service/HeapBaselineCard.tsx': 4,
  'pages/service/Overview.tsx': 15,
  'pages/service/OverviewTables.tsx': 6,
  'pages/service/overviewWindowKpi.ts': 1,
  'pages/service/RuntimeCharts.tsx': 2,
  'pages/service/RuntimePodChart.tsx': 2,
  'pages/service/ServiceClusterBreakdown.tsx': 5,
  'pages/service/ServicePodsTable.tsx': 3,
  'pages/service/ServiceSignalTabs.tsx': 2,
  'pages/service/TopEndpointsCard.tsx': 4,
  'pages/ServiceBacktrace.tsx': 4,
  'pages/settings/aiEval.ts': 9,
  'pages/settings/aiThinking.ts': 1,
  'pages/settings/argocdDiscovery.ts': 1,
  'pages/settings/BrandingTab.tsx': 1,
  'pages/settings/errorRamp.ts': 1,
  'pages/settings/KnowledgeTab.tsx': 2,
  'pages/settings/LdapTab.tsx': 3,
  'pages/settings/MLServiceProfiles.tsx': 1,
  'pages/settings/oracleProbe.ts': 4,
  'pages/settings/PipelineTab.tsx': 2,
  'pages/settings/SamplingRuleShadowSection.tsx': 11,
  'pages/settings/samplingWeight.ts': 4,
  'pages/settings/SamplingWeightPanel.tsx': 6,
  'pages/settings/SpanClusterValuesPanel.tsx': 1,
  'pages/settings/WikiKnowledgeSection.tsx': 1,
  'pages/settings/WikiTestSearch.tsx': 1,
  'pages/Slos.tsx': 14, // v0.10.2487 dilim 3
  'pages/SlowQueries.tsx': 9,
  'pages/slowqueries/stmtDetailSections.tsx': 9,
  'pages/Trace.tsx': 3,
  'pages/trace/TraceLogsPanel.tsx': 1,
  'pages/trace/traceMetricsFmt.ts': 3,
  'pages/TraceCompare.tsx': 1,
  'pages/TraceKiosk.tsx': 1,
  'pages/Traces.tsx': 20,
  'pages/traces/ColumnFilter.tsx': 1,
  'pages/traces/TracesPartialNote.tsx': 1,
};

const CEIL_DEF: Record<string, number> = {
  'components/ai/operationCardView.ts': 1,
  'components/ai/stepPreview.ts': 1,
  'components/ai/toolSteps.ts': 1,
  'components/BubbleUpPanel.tsx': 1,
  'components/coremetry/anomalies/MLReplaySheet.tsx': 2,
  'components/coremetry/anomalies/MLSignalEvidence.tsx': 1,
  'components/coremetry/anomalies/MLTab.tsx': 4,
  'components/coremetry/anomalies/UnifiedAnomalies.tsx': 1,
  'components/coremetry/logs/TraceChip.tsx': 1,
  'components/cosre/conversation/briefingView.ts': 1,
  'components/cosre/conversation/endpointPick.ts': 3,
  'components/cosre/evidence/stepView.ts': 3,
  'components/DBQueriesPanel.tsx': 1,
  'components/DependenciesTable.tsx': 1,
  'components/FlameDiff.tsx': 1,
  'components/LogPatternsPanel.tsx': 1,
  'components/LogsHistogram.tsx': 1,
  'components/RefreshPicker.tsx': 1,
  'components/ServiceAttentionStrip.tsx': 1,
  'components/ServiceRuntimeBadge.tsx': 1,
  'components/Sparkline.tsx': 1,
  'components/topology/FocusedNeighborhood.tsx': 1,
  'components/TopologyFlowGraph.tsx': 1,
  'components/traces/shared.tsx': 1,
  'components/traces/volumeSeries.ts': 2,
  'components/TraceWaterfall.tsx': 1,
  'features/anomalies/alertMetricSeries.ts': 1,
  'features/anomalies/detailSummary.ts': 1,
  'features/anomalies/hypothesisView.ts': 1,
  'features/anomalies/investigationResultView.ts': 1,
  'features/anomalies/occTick.ts': 1,
  'features/anomalies/problemInsight.ts': 2,
  'features/anomalies/problemTime.ts': 3,
  'features/dependencies/panels/shared.tsx': 2,
  'features/tour/steps.ts': 1,
  'lib/ai-rates.ts': 1,
  'lib/chartFmt.ts': 6,
  'lib/chatEvidence.ts': 5,
  'lib/esRequests.ts': 3,
  'lib/filterChips.ts': 1,
  'lib/fmtEta.ts': 1,
  'lib/fmtEtaHours.ts': 1,
  'lib/httpMetrics.ts': 5,
  'lib/i18n.ts': 2,
  'lib/loginSummary.ts': 5,
  'lib/metricsExplorer/discover.ts': 2,
  'lib/mlBaselineBackfill.ts': 1,
  'lib/problemBreakdown.ts': 3,
  'lib/problemStandardView.ts': 4,
  'lib/rangePicker.ts': 2,
  'lib/serverTiming.ts': 1,
  'lib/traceAiContext.ts': 1,
  'lib/traceNarrative.ts': 1,
  'lib/utils.ts': 8,
  'lib/whyNotProblem.ts': 1,
  'pages/adminch/statePaths.ts': 1,
  'pages/AdminClickhouse.tsx': 4,
  'pages/adminstats/shared.tsx': 2,
  'pages/ai/exchangeView.ts': 2,
  'pages/ai/sreEvalView.ts': 1,
  'pages/clusters/AlertsPanel.tsx': 1,
  'pages/clusters/fleetSummary.ts': 1,
  'pages/clusters/thresholds.ts': 2,
  'pages/databases/horizonNotice.ts': 1,
  'pages/Endpoints.tsx': 2,
  'pages/endpoints/detailSections.tsx': 1,
  'pages/Events.tsx': 1,
  'pages/explore/stepAlign.ts': 1,
  'pages/metrics/ResultPanel.tsx': 1,
  'pages/Runbook.tsx': 1,
  'pages/service/DownstreamTargetsTable.tsx': 1,
  'pages/service/HeapBaselineCard.tsx': 1,
  'pages/service/Overview.tsx': 1,
  'pages/service/RuntimeCharts.tsx': 1,
  'pages/service/RuntimePodChart.tsx': 1,
  'pages/service/ServiceInfraTab.tsx': 1,
  'pages/service/ServiceKafkaClientsPanel.tsx': 1,
  'pages/settings/aiEval.ts': 8,
  'pages/settings/argocdDiscovery.ts': 2,
  'pages/settings/argocdForm.ts': 1,
  'pages/settings/samplingWeight.ts': 1,
  'pages/Slos.tsx': 1,
  'pages/slowqueries/stmtDetailSections.tsx': 1,
  'pages/trace/traceMetricsFmt.ts': 8,
  'pages/trace/TracePodPanel.tsx': 1,
  'pages/trace/tracePodPanelModel.ts': 1,
  'pages/Traces.tsx': 1,
};
