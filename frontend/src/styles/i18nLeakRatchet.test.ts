import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { stripTsComments } from './zLayers.test';

// i18nLeakRatchet + topbarTitleRatchet — v0.10.2486 (entity tutarlılık
// denetimi dilim 2, docs/frontend/entity-coherence-audit.md §1.3 + §5.3).
//
// (a) i18nLeak — katalog dışı sabit metin, denetimin M7 tanımıyla birebir:
//     yalnız `.tsx`; JSX metni `>\s*(…harf{2,}…)\s*<` + `(title|placeholder|
//     aria-label|label|ariaLabel|emptyMessage|header)="…harf…"`; `=>`, `&&`,
//     `||`, ` = ` içeren ya da `.`/`(` ile başlayan aday atılır. TR = Türkçe
//     harf ya da TR durak sözcüğü, değilse EN. Sezgisel: `ERROR`, `config.yaml`
//     gibi kod sabitleri de sayılır.
//     İstisna: `// i18n-exempt: <neden>` (JSX içinde `{/* i18n-exempt: <neden> */}`) yorumu kendi satırını ve bir sonraki
//     satırı muaf tutar (anahtar gerekçedir, satır numarası değil).
// (b) topbarTitle — M10 `<Topbar title=…>` içinde sabit metin: `title="…"`,
//     şablon dizgesinin `${}` dışındaki harfleri, `|| '…'` yedeği. Hedef 0.
//
// Kapsam: src/**/*.tsx; HARİÇ *.test.*, *.stories.*, components/shadcn/**
// (üretilen), testdata/, /design (pages/Design.tsx + pages/design/**).
// Yorumlar sayımdan önce ayıklanır (stripTsComments). Yönetim kümesi (ADMIN:
// pages/(Admin*|System|Settings|Users).tsx + pages/(admin*|settings|system)/**;
// karar A6: /users yönetim, /ai ürün) çevrilmez — sayılır, TOTAL'i yalnız
// rapor/aşağı yönlü, dosya tavanı yok, hedef dışı.
//
// Ürün dosyası başına tavan (CEIL) + TOTAL_TR / TOTAL_EN; listede olmayan
// ürün dosyasının tavanı 0. YALNIZ AŞAĞI: sayı düşünce tavan aynı commit'te
// ölçülene iner. Tabanlar dilim 2 SONUNDA ölçüldü.
// Yeniden ölçüm: I18N_RATCHET_PRINT=1 npx vitest run src/styles/i18nLeakRatchet.test.ts
const SRC = resolve(__dirname, '..');

const L = 'A-Za-zÇĞİÖŞÜçğıöşü';
const JSX_RE = new RegExp(`>\\s*([^<>{}\\n]*[${L}]{2,}[^<>{}\\n]*?)\\s*<`, 'g');
const ATTR_RE = new RegExp(`(?<![\\w-])(?:title|placeholder|aria-label|label|ariaLabel|emptyMessage|header)="([^"\\n]*[${L}][^"\\n]*)"`, 'g');
const TR_RE = /[çğıöşüÇĞİÖŞÜ]|\b(?:ve|bir|için|yok|değil|olarak|ile|bu|şu|gibi|kadar|sonra|önce|daha|var|yalnız|tüm|hiç)\b/;
const ADMIN_RE = /^pages\/(?:(?:Admin\w*|System|Settings|Users)\.tsx$|(?:admin\w*|settings|system)\/)/;
const EXCLUDE = /^(components\/shadcn\/|pages\/Design\.tsx$|pages\/design\/)|(^|\/)testdata\//;
const EXEMPT_RE = /(?:\/\/|\/\*)\s*i18n-exempt:\s*\S/;

export function isCandidate(s: string): boolean {
  const v = s.trim();
  if (!v) return false;
  if (/=>|&&|\|\|| = /.test(v)) return false;
  if (v.startsWith('.') || v.startsWith('(')) return false;
  return true;
}
export const langOf = (s: string): 'tr' | 'en' => (TR_RE.test(s) ? 'tr' : 'en');

/** M7 adayları (metin + 1 tabanlı satır) — yorumlar ayıklanmış kod üzerinde. */
export function leaksAt(src: string): Array<{ text: string; line: number }> {
  const code = stripTsComments(src);
  const exempt = new Set<number>();
  src.split('\n').forEach((ln, i) => { if (EXEMPT_RE.test(ln)) { exempt.add(i); exempt.add(i + 1); } });
  const lineAt = (idx: number) => code.slice(0, idx).split('\n').length - 1;
  const out: Array<{ text: string; line: number }> = [];
  for (const re of [JSX_RE, ATTR_RE]) {
    for (const m of code.matchAll(re)) {
      if (!isCandidate(m[1])) continue;
      const at = (m.index ?? 0) + m[0].indexOf(m[1]);
      const line = lineAt(at);
      if (exempt.has(line)) continue;
      out.push({ text: m[1].trim(), line: line + 1 });
    }
  }
  return out;
}
export const leaks = (src: string): string[] => leaksAt(src).map(x => x.text);

// M10 + "sabit": <Topbar title="…"> ya da title={…} içinde ${} dışı harf / '…' yedeği.
const TOPBAR_RE = /<Topbar\s+title=("[^"]*"|\{)/g;
export function hardTopbarTitles(code: string): number {
  let n = 0;
  for (const m of code.matchAll(TOPBAR_RE)) {
    if (m[1] !== '{') { n++; continue; }
    // { … } gövdesini dengeli parantezle al.
    let depth = 0; let i = (m.index ?? 0) + m[0].length - 1; let body = '';
    for (; i < code.length; i++) {
      const c = code[i];
      if (c === '{') depth++;
      else if (c === '}') { depth--; if (depth === 0) break; }
      body += c;
    }
    const literalText = (body.match(/`[^`]*`|'[^']*'|"[^"]*"/g) ?? [])
      .map(s => s.slice(1, -1).replace(/\$\{[^}]*\}/g, ''))
      .some(s => new RegExp(`[${L}]{2,}`).test(s) && !/^\w+(?:\.\w+)+$/.test(s));
    if (literalText) n++;
  }
  return n;
}

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !/\.(test|stories)\./.test(p)) out.push(p);
  }
  return out;
}

const tr = new Map<string, number>();
const en = new Map<string, number>();
const topbar = new Map<string, number>();
for (const p of walk(SRC)) {
  const rel = relative(SRC, p).split(sep).join('/');
  if (EXCLUDE.test(rel)) continue;
  const src = readFileSync(p, 'utf8');
  let a = 0; let b = 0;
  for (const s of leaks(src)) { if (langOf(s) === 'tr') a++; else b++; }
  if (a) tr.set(rel, a);
  if (b) en.set(rel, b);
  const tb = hardTopbarTitles(stripTsComments(src));
  if (tb) topbar.set(rel, tb);
}
const sum = (m: Map<string, number>, admin: boolean) =>
  [...m].reduce((acc, [f, n]) => acc + (ADMIN_RE.test(f) === admin ? n : 0), 0);
const total = (f: string) => (tr.get(f) ?? 0) + (en.get(f) ?? 0);
const productFiles = [...new Set([...tr.keys(), ...en.keys()])].filter(f => !ADMIN_RE.test(f));

const HINT = 'kullanıcıya görünen sabit metni katalog anahtarına taşı (lib/i18n.tr.ts + lib/i18n.en.ts, '
  + 't(key, lang) / useT()); kod sabiti ise satıra `// i18n-exempt: <neden>` ekle';
const HINT_TOPBAR = 'Topbar başlığı katalogdan gelir: title={t(\'nav.x\', lang)} — menü ile aynı anahtar';

if (process.env.I18N_RATCHET_PRINT) {
  const dump = productFiles.sort().map(f => `  '${f}': ${total(f)},`).join('\n');
  console.log(`I18N product TR=${sum(tr, false)} EN=${sum(en, false)} admin TR=${sum(tr, true)} EN=${sum(en, true)}\n${dump}`);
  console.log(`TOPBAR product=${sum(topbar, false)} admin=${sum(topbar, true)}\n${[...topbar].map(([f, n]) => `  ${f}: ${n}`).join('\n')}`);
}

describe('i18nLeakRatchet (denetim M7, yalnız aşağı)', () => {
  it('ürün dosyası başına sabit metin tavanı aşmaz', () => {
    const over = productFiles.filter(f => total(f) > (CEIL[f] ?? 0)).map(f => `${f}: ${total(f)} > ${CEIL[f] ?? 0}`);
    expect(over, `${HINT}\n${over.join('\n')}`).toEqual([]);
  });

  it('TOTAL: ürün TR / EN ayrı; yönetim kümesi hedef dışı (yalnız aşağı)', () => {
    expect(sum(tr, false), HINT).toBeLessThanOrEqual(TOTAL_TR);
    expect(sum(en, false), HINT).toBeLessThanOrEqual(TOTAL_EN);
    expect(sum(tr, true)).toBeLessThanOrEqual(TOTAL_ADMIN_TR);
    expect(sum(en, true)).toBeLessThanOrEqual(TOTAL_ADMIN_EN);
  });

  it('tavan listesi bayatlamaz: tavanlı her dosya ölçülenden büyük tavan taşımaz', () => {
    const stale = Object.entries(CEIL).filter(([f, c]) => total(f) < c).map(([f, c]) => `${f}: ölçülen ${total(f)} < tavan ${c}`);
    const totals = [
      sum(tr, false) < TOTAL_TR && `TOTAL_TR ${TOTAL_TR} → ${sum(tr, false)}`,
      sum(en, false) < TOTAL_EN && `TOTAL_EN ${TOTAL_EN} → ${sum(en, false)}`,
    ].filter(Boolean);
    expect([...stale, ...totals], 'sayı düştü — tavanı aynı commit\'te ölçülene indir').toEqual([]);
  });

  it('desen kaymadı: M7 adayları, dil sınıfı, kapsam', () => {
    expect(leaks('<p>Servis bulunamadı</p>')).toEqual(['Servis bulunamadı']);
    expect(leaks('<b>{x}</b><i>No data</i>')).toEqual(['No data']);
    expect(leaks('<Input placeholder="Search services" />')).toEqual(['Search services']);
    expect(leaks('<p>{a && b}</p>')).toEqual([]);
    expect(leaks('<p>OK</p> // i18n-exempt: protokol kodu')).toEqual([]);
    expect(langOf('Servis yok')).toBe('tr');
    expect(langOf('No data')).toBe('en');
    expect(ADMIN_RE.test('pages/Users.tsx')).toBe(true);
    expect(ADMIN_RE.test('pages/AIObservability.tsx')).toBe(false);
    expect(EXCLUDE.test('pages/Design.tsx')).toBe(true);
  });
});

describe('topbarTitleRatchet (denetim M10, hedef 0)', () => {
  it('ürün ekranında sabit Topbar başlığı yok', () => {
    const prod = [...topbar].filter(([f]) => !ADMIN_RE.test(f)).map(([f, n]) => `${f}: ${n}`);
    expect(prod, `${HINT_TOPBAR}\n${prod.join('\n')}`).toEqual([]);
  });

  it('yönetim kümesi hedef dışı, yalnız aşağı', () => {
    expect(sum(topbar, true)).toBeLessThanOrEqual(TOTAL_TOPBAR_ADMIN);
  });

  it('desen kaymadı', () => {
    expect(hardTopbarTitles('<Topbar title="Services" />')).toBe(1);
    expect(hardTopbarTitles('<Topbar title={`Service · ${svc}`} />')).toBe(1);
    expect(hardTopbarTitles("<Topbar title={draft.title || 'Runbook'} />")).toBe(1);
    expect(hardTopbarTitles("<Topbar title={t('nav.services', lang)} />")).toBe(0);
    expect(hardTopbarTitles('<Topbar title={`${t(\'page.service\', lang)} · ${svc}`} />')).toBe(0);
  });
});

// ── Tabanlar (v0.10.2486, dilim 2 sonu ölçümü) ────────────────────────────
// Aynı tanımla dilim 2 öncesi (v0.10.2485): ürün TR 954 + EN 2317 = 3271, yönetim
// TR 948 + EN 1562; Topbar sabit başlık ürün 53 + yönetim 9. (Denetimin 2103/1138
// TR/EN ayrımı dosya toplamlarıyla tutuyor ama dil sınıfı ters düşmüş — ör.
// PanelEditor 93'ün ~85'i İngilizce; bu test sınıfı yeniden ölçtü.)
const TOTAL_TR = 825; // v0.10.2486 dilim 2: 954 → 825
const TOTAL_EN = 1796; // v0.10.2486 dilim 2: 2317 → 1796
const TOTAL_ADMIN_TR = 956; // hedef dışı (yönetim), yalnız aşağı; 948 + 8 = aynı sürümdeki gölge kuralı ayar ekranı (Settings › ML, yönetim ekranı çevrilmez)
const TOTAL_ADMIN_EN = 1568; // hedef dışı (yönetim), yalnız aşağı; 1562 + 6 = aynı sürümdeki gölge kuralı ayar ekranı
const TOTAL_TOPBAR_ADMIN = 9; // hedef dışı (Settings 2, System 1, Users 2, settings/UserDetail 4)

const CEIL: Record<string, number> = {
  'components/AnnouncementBanner.tsx': 1,
  'components/AuthProvider.tsx': 2,
  'components/BubbleUpPanel.tsx': 7,
  'components/ChangePasswordModal.tsx': 8,
  'components/CodeStatusNote.tsx': 2,
  'components/ColumnManager.tsx': 4,
  'components/Combobox.tsx': 2,
  'components/ContextBar/ContextBar.tsx': 15,
  'components/CopilotChat.tsx': 40,
  'components/CopilotChatGate.tsx': 2,
  'components/CorrelationContextDrawer.tsx': 19,
  'components/DBQueriesPanel.tsx': 15,
  'components/DependenciesTable.tsx': 9,
  'components/DeployHistoryPanel.tsx': 19,
  'components/EnvPicker.tsx': 1,
  'components/ErrorBoundary.tsx': 4,
  'components/ExternalPaths.tsx': 4,
  'components/FilterBuilder.tsx': 11,
  'components/FilterGroupBuilder.tsx': 6,
  'components/FilterQueryBox.tsx': 5,
  'components/FlameDiff.tsx': 3,
  'components/HeatmapCellExemplars.tsx': 3,
  'components/KindBadge.tsx': 1,
  'components/LatencyHeatmap.tsx': 4,
  'components/LogContextModal.tsx': 3,
  'components/LogPatternsPanel.tsx': 12,
  'components/LogTable.tsx': 13,
  'components/LogsHistogram.tsx': 3,
  'components/Markdown.tsx': 2,
  'components/MethodHotspots.tsx': 7,
  'components/MetricPanel.tsx': 5,
  'components/Pager.tsx': 5,
  'components/ProblemRunbookPanel.tsx': 7,
  'components/ProblemVerdictActions.tsx': 1,
  'components/QueryError.tsx': 2,
  'components/RCAVerdictPanel.tsx': 25,
  'components/RolloutWorkloadPicker.tsx': 1,
  'components/RootCausePanel.tsx': 32,
  'components/RootCauseRibbon.tsx': 14,
  'components/RuntimeIcons.tsx': 1,
  'components/SavedViewsBar.tsx': 14,
  'components/ServiceAttentionStrip.tsx': 1,
  'components/ServiceCatalogPill.tsx': 39,
  'components/ServiceMapNodeDrawer.tsx': 13,
  'components/ServiceNeighbors.tsx': 7,
  'components/SessionEndedCard.tsx': 4,
  'components/ShortcutsHelp.tsx': 4,
  'components/Sidebar.tsx': 3,
  'components/SpanDetail.tsx': 35,
  'components/SpanK8sSection.tsx': 5,
  'components/Sparkline.tsx': 1,
  'components/SubjectLink.tsx': 2,
  'components/TimeRangePicker.tsx': 2,
  'components/TopologyFlowGraph.tsx': 8,
  'components/TraceWaterfall.tsx': 9,
  'components/TrendSpark.tsx': 1,
  'components/TriageCrumb.tsx': 1,
  'components/Wordmark.tsx': 2,
  'components/ai/AIExplainButton.tsx': 1,
  'components/ai/ChartsSignalsCard.tsx': 7,
  'components/ai/ChatComposer.tsx': 14,
  'components/ai/ChatContextBar.tsx': 2,
  'components/ai/ChatTopologyCard.tsx': 3,
  'components/ai/ChatTraceList.tsx': 10,
  'components/ai/CodeSourceFooter.tsx': 1,
  'components/ai/CosreCapabilities.tsx': 1,
  'components/ai/CosreCorrectButton.tsx': 3,
  'components/ai/CosreMemoryConfirm.tsx': 13,
  'components/ai/CosreMemoryPanel.tsx': 1,
  'components/ai/CosreRuleConfirm.tsx': 4,
  'components/ai/CosreRulesPanel.tsx': 15,
  'components/ai/CosreSidebar.tsx': 13,
  'components/ai/EvidenceCard.tsx': 1,
  'components/ai/EvidenceChips.tsx': 5,
  'components/ai/EvidenceRelevance.tsx': 10,
  'components/ai/ExplainBody.tsx': 1,
  'components/ai/FollowupChips.tsx': 3,
  'components/ai/InsightCard.tsx': 25,
  'components/ai/LogSpikeCard.tsx': 7,
  'components/ai/MetricPanelCard.tsx': 6,
  'components/ai/ModelPicker.tsx': 1,
  'components/ai/NameCompletionPopup.tsx': 2,
  'components/ai/OperationCard.tsx': 8,
  'components/ai/RowExplainCell.tsx': 1,
  'components/ai/ServiceReportCard.tsx': 25,
  'components/ai/TraceExplainNudge.tsx': 6,
  'components/ai/TraceNarrativeBody.tsx': 1,
  'components/ai/useChatHistory.tsx': 4,
  'components/chart/CorePanel.tsx': 11,
  'components/chart/StatsLegend.tsx': 7,
  'components/charts/AnnotationLane.tsx': 2,
  'components/charts/CompareToggle.tsx': 1,
  'components/charts/TimeChart.tsx': 1,
  'components/coremetry/CommandPalette/CommandPaletteBody.tsx': 1,
  'components/coremetry/Confirm/ConfirmGate.tsx': 1,
  'components/coremetry/FilterBar/FilterChipEditor.tsx': 7,
  'components/coremetry/FilterBar/FilterChipRow.tsx': 2,
  'components/coremetry/PageControls.tsx': 1,
  'components/coremetry/SectionHead.tsx': 1,
  'components/coremetry/ServiceHealthcheck.tsx': 22,
  'components/coremetry/ThemeMenu.tsx': 1,
  'components/coremetry/admin/EsRequestsPanel.tsx': 4,
  'components/coremetry/ai/FailedTurnsPanel.tsx': 19,
  'components/coremetry/anomalies/ImpactReplay.tsx': 7,
  'components/coremetry/anomalies/MLReplaySheet.tsx': 6,
  'components/coremetry/anomalies/MLSignalEvidence.tsx': 2,
  'components/coremetry/anomalies/UnifiedAnomalies.tsx': 1,
  'components/coremetry/dashboard/DashboardListFilters.tsx': 2,
  'components/coremetry/dashboard/GrafanaImportDialog.tsx': 11,
  'components/coremetry/dashboard/PanelEditLayout.tsx': 10,
  'components/coremetry/dashboard/PanelQueryEditor.tsx': 4,
  'components/coremetry/dashboard/PromqlTableViews.tsx': 3,
  'components/coremetry/dashboard/TemplateGallery.tsx': 4,
  'components/coremetry/dashboard/VarDefFields.tsx': 5,
  'components/coremetry/dashboard/VariablePickers.tsx': 3,
  'components/coremetry/logs/CompactLogSearch.tsx': 7,
  'components/coremetry/logs/DisplayOptions.tsx': 10,
  'components/coremetry/logs/FieldSidebar.tsx': 28,
  'components/coremetry/logs/LoadStatus.tsx': 2,
  'components/coremetry/logs/LogContextSection.tsx': 6,
  'components/coremetry/logs/LogDetailSheet.tsx': 21,
  'components/coremetry/logs/LogQueryErrorLine.tsx': 1,
  'components/coremetry/logs/LogQueryInput.tsx': 10,
  'components/coremetry/logs/LogsAiAnswer.tsx': 6,
  'components/coremetry/logs/PatternCompare.tsx': 6,
  'components/coremetry/logs/ServiceLogSummary.tsx': 3,
  'components/coremetry/logs/StackBlock.tsx': 3,
  'components/coremetry/logs/TraceChip.tsx': 8,
  'components/coremetry/logs/TraceLogsView.tsx': 18,
  'components/coremetry/metrics/AddToDashboardDialog.tsx': 10,
  'components/coremetry/metrics/FilterPopover.tsx': 3,
  'components/coremetry/metrics/FormulaRow.tsx': 6,
  'components/coremetry/metrics/LabelPick.tsx': 2,
  'components/coremetry/metrics/MetricPicker.tsx': 2,
  'components/coremetry/metrics/MetricsQLEditor.tsx': 5,
  'components/coremetry/metrics/PickList.tsx': 4,
  'components/coremetry/metrics/QueryRow.tsx': 11,
  'components/cosre/conversation/CosreChatV2.tsx': 3,
  'components/cosre/conversation/CosreSheet.tsx': 1,
  'components/cosre/conversation/CosreWelcomeV2.tsx': 1,
  'components/cosre/conversation/MessageList.tsx': 1,
  'components/cosre/conversation/SheetResizeHandle.tsx': 2,
  'components/cosre/conversation/SuggestionChips.tsx': 1,
  'components/dashboard/PanelRenderer.tsx': 13,
  'components/dashboard/UnitSelect.tsx': 2,
  'components/dashboard/VariableEditor.tsx': 13,
  'components/dashboard/VariablesBar.tsx': 3,
  'components/perf/PerfMeter.tsx': 4,
  'components/topology/FocusedNeighborhood.tsx': 14,
  'components/traces/LatencyScatter.tsx': 1,
  'components/traces/OracleOperationChip.tsx': 4,
  'components/traces/TraceHonesty.tsx': 1,
  'components/traces/TraceMeasures.tsx': 2,
  'components/ui/ConfirmDialog.tsx': 3,
  'components/ui/DataTable/BulkBar.tsx': 2,
  'components/ui/DataTable/DataTable.tsx': 5,
  'components/ui/DataTable/DataTableCell.tsx': 1,
  'components/ui/DataTable/HeadMenu.tsx': 2,
  'components/ui/Drawer.tsx': 3,
  'components/ui/EnterHint.tsx': 1,
  'components/ui/Modal.tsx': 1,
  'components/ui/PickerPopover.tsx': 3,
  'components/ui/RouteSkeleton.tsx': 1,
  'components/ui/SearchField.tsx': 1,
  'components/viz/TimeSeriesPanel.tsx': 9,
  'features/anomalies/AnomaliesPage.tsx': 27,
  'features/anomalies/AnomalyDetailDrawer.tsx': 5,
  'features/anomalies/AnomalyEventDetail.tsx': 4,
  'features/anomalies/AnomalyStreamsPage.tsx': 1,
  'features/anomalies/AnomalyWindowTable.tsx': 3,
  'features/anomalies/AssigneeCell.tsx': 3,
  'features/anomalies/ExceptionPodsPanel.tsx': 9,
  'features/anomalies/ExceptionSampleRow.tsx': 1,
  'features/anomalies/ExternalEvidencePanel.tsx': 3,
  'features/anomalies/HypothesisRuling.tsx': 1,
  'features/anomalies/OracleGroupPanel.tsx': 1,
  'features/anomalies/ProblemInsightStrip.tsx': 2,
  'features/anomalies/ProblemInvestigationCards.tsx': 1,
  'features/anomalies/ProblemInvestigationRecommendations.tsx': 3,
  'features/anomalies/RootCauseLabelCard.tsx': 1,
  'features/anomalies/anomalyDetailParts.tsx': 3,
  'features/anomalies/detailSections.tsx': 1,
  'features/anomalies/streams.tsx': 16,
  'features/dependencies/CallerSection.tsx': 1,
  'features/dependencies/DetailDrawer.tsx': 13,
  'features/dependencies/KafkaTopicClients.tsx': 2,
  'features/dependencies/KafkaTopicMetrics.tsx': 6,
  'features/dependencies/panels/MySQLPanel.tsx': 21,
  'features/dependencies/panels/OraclePanel.tsx': 15,
  'features/dependencies/panels/PostgresPanel.tsx': 14,
  'features/dependencies/panels/RedisPanel.tsx': 19,
  'features/dependencies/panels/shared.tsx': 14,
  'main.tsx': 1,
  'pages/Clusters.tsx': 17,
  'pages/CoSRE.tsx': 1,
  'pages/CoSREShared.tsx': 7,
  'pages/Dashboard.tsx': 39,
  'pages/Dashboards.tsx': 16,
  'pages/DatabaseDetail.tsx': 10,
  'pages/Databases.tsx': 18,
  'pages/EndpointDetail.tsx': 31,
  'pages/Endpoints.tsx': 31,
  'pages/Events.tsx': 19,
  'pages/External.tsx': 12,
  'pages/Hosts.tsx': 7,
  'pages/Inbox.tsx': 20,
  'pages/Logs.tsx': 17,
  'pages/Messaging.tsx': 11,
  'pages/MessagingTopic.tsx': 9,
  'pages/Monitors.tsx': 41,
  'pages/Pod.tsx': 2,
  'pages/Profile.tsx': 8,
  'pages/Profiling.tsx': 20,
  'pages/PublicStatus.tsx': 14,
  'pages/PublicTrace.tsx': 13,
  'pages/Rollouts.tsx': 13,
  'pages/RunbookExecution.tsx': 18,
  'pages/Runbooks.tsx': 9,
  'pages/Service.tsx': 13,
  'pages/ServiceBacktrace.tsx': 10,
  'pages/ServiceMap.tsx': 24,
  'pages/Services.tsx': 23,
  'pages/Shift.tsx': 9,
  'pages/Slos.tsx': 32,
  'pages/SlowQueries.tsx': 10,
  'pages/StatementDetail.tsx': 13,
  'pages/TraceCompare.tsx': 21,
  'pages/TraceKiosk.tsx': 18,
  'pages/Watchers.tsx': 41,
  'pages/ai/CosreQualityPanel.tsx': 5,
  'pages/ai/ExchangeUserPicker.tsx': 3,
  'pages/ai/ExchangesPanel.tsx': 19,
  'pages/ai/PendingCasesPanel.tsx': 14,
  'pages/ai/RoutingReportPanel.tsx': 4,
  'pages/ai/SREEvalCompare.tsx': 12,
  'pages/ai/SREEvalPanel.tsx': 18,
  'pages/ai/SREReplayPanel.tsx': 31,
  'pages/alerts/ConditionPreview.tsx': 9,
  'pages/alerts/KafkaAlertModal.tsx': 10,
  'pages/alerts/NoisyRulesPanel.tsx': 11,
  'pages/alerts/NotifyTeamsField.tsx': 4,
  'pages/alerts/RouteAlertModal.tsx': 13,
  'pages/alerts/RuleSaveError.tsx': 1,
  'pages/alerts/StatementAlertModal.tsx': 11,
  'pages/alerts/StatementPicker.tsx': 2,
  'pages/alerts/ThresholdField.tsx': 8,
  'pages/alerts/WatcherImportModal.tsx': 10,
  'pages/clusters/NamespaceCombobox.tsx': 4,
  'pages/clusters/NamespaceDetail.tsx': 2,
  'pages/clusters/NodeHeatmap.tsx': 2,
  'pages/clusters/TrendPanel.tsx': 4,
  'pages/databases/DatabaseErrorsSection.tsx': 4,
  'pages/databases/DatabaseProblemsSection.tsx': 2,
  'pages/databases/detailSections.tsx': 20,
  'pages/endpoints/ColumnToggle.tsx': 2,
  'pages/endpoints/EndpointColumnFilter.tsx': 3,
  'pages/endpoints/MetricModeSections.tsx': 8,
  'pages/endpoints/detailSections.tsx': 23,
  'pages/explore/GroupTable.tsx': 3,
  'pages/explore/NLQueryBox.tsx': 3,
  'pages/explore/PinToDashboardModal.tsx': 7,
  'pages/explore/QueryPanel.tsx': 5,
  'pages/explore/QueryRow.tsx': 15,
  'pages/explore/RecentQueries.tsx': 3,
  'pages/explore/RowsCappedNote.tsx': 1,
  'pages/explore/SplitByPicker.tsx': 2,
  'pages/explore/TracesResult.tsx': 6,
  'pages/explore/VizRail.tsx': 1,
  'pages/inbox/InboxCells.tsx': 3,
  'pages/inbox/ProblemSignals.tsx': 2,
  'pages/login/LoginPage.tsx': 2,
  'pages/logs/LogsActionsMenus.tsx': 9,
  'pages/logs/LogsExplain.tsx': 2,
  'pages/logs/LogsFilterRow.tsx': 15,
  'pages/logs/LogsKeyHints.tsx': 2,
  'pages/logs/LogsNLQuery.tsx': 9,
  'pages/logs/LogsNotices.tsx': 1,
  'pages/logs/LogsQueryBar.tsx': 11,
  'pages/logs/LogsTableToolbar.tsx': 8,
  'pages/messaging/KafkaConsumersTable.tsx': 2,
  'pages/metrics/CodePanel.tsx': 13,
  'pages/metrics/DiscoverTab.tsx': 24,
  'pages/metrics/HistoryTable.tsx': 6,
  'pages/metrics/MetricsShell.tsx': 3,
  'pages/metrics/QueryTab.tsx': 12,
  'pages/metrics/ResultPanel.tsx': 24,
  'pages/pod/PodContextTables.tsx': 13,
  'pages/pod/PodIdentityLine.tsx': 5,
  'pages/pod/PodMetaRow.tsx': 1,
  'pages/pod/PodRuntimeSection.tsx': 1,
  'pages/pod/PodServicesTable.tsx': 2,
  'pages/pod/PodTracesTable.tsx': 6,
  'pages/search/FacetRail.tsx': 1,
  'pages/search/ResultList.tsx': 1,
  'pages/search/ResultRow.tsx': 2,
  'pages/search/SearchDetailDrawer.tsx': 20,
  'pages/service/DetailsEndpointsSection.tsx': 7,
  'pages/service/DetailsPropsStrip.tsx': 2,
  'pages/service/DownstreamTargetsTable.tsx': 2,
  'pages/service/HeapBaselineCard.tsx': 10,
  'pages/service/InfraSourceNote.tsx': 12,
  'pages/service/KafkaTopicPicker.tsx': 3,
  'pages/service/MetricThroughputNote.tsx': 3,
  'pages/service/OperationsTable.tsx': 20,
  'pages/service/Overview.tsx': 6,
  'pages/service/OverviewTables.tsx': 2,
  'pages/service/PodInlineResourceCharts.tsx': 6,
  'pages/service/PodJmxInline.tsx': 6,
  'pages/service/PodResourceCharts.tsx': 1,
  'pages/service/RuntimeCharts.tsx': 8,
  'pages/service/ServiceClusterBreakdown.tsx': 4,
  'pages/service/ServiceHttpMetricsTab.tsx': 7,
  'pages/service/ServiceInfraTab.tsx': 25,
  'pages/service/ServiceKafkaClientsPanel.tsx': 12,
  'pages/service/ServiceMessagingTab.tsx': 5,
  'pages/service/ServiceNoTracesNote.tsx': 2,
  'pages/service/ServicePodInventory.tsx': 2,
  'pages/service/ServicePodsTable.tsx': 10,
  'pages/service/ServiceRuntimeSection.tsx': 4,
  'pages/service/ServiceSignalTabs.tsx': 2,
  'pages/service/TopEndpointsCard.tsx': 2,
  'pages/service/httpMetricsViz.tsx': 1,
  'pages/slowqueries/stmtDetailSections.tsx': 18,
  'pages/sre/SreReport.tsx': 1,
  'pages/trace/KioskSpanPanel.tsx': 18,
  'pages/trace/TraceLogsPanel.tsx': 6,
  'pages/trace/TraceMetricCell.tsx': 1,
  'pages/trace/TraceMetricsCoverage.tsx': 3,
  'pages/trace/TraceMetricsPanel.tsx': 12,
  'pages/trace/TracePodPanel.tsx': 1,
  'pages/trace/TracePodTable.tsx': 9,
  'pages/traces/ColumnFilter.tsx': 1,
};
