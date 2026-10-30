// pageFiles — v0.10.1614 (shadcn Faz 1 R6, plan §3.2). Sayfa → dosya/dizin
// eşlemesi; inlineStyleRatchet, rawElementRatchet raporu ve migration-log
// bunu paylaşır. Girdi = src/'ye göre yol: `.tsx` ile biten TAM dosya, `/`
// ile biten DİZİN öneki (ör. `pages/Trace.tsx` `pages/Traces.tsx`i
// KAPSAMAZ). İlk anahtarlar Faz 2 dilimleri (audit §3.1/§9.4); yeni dilim
// ilk sürümünde kendi anahtarını ekler. RemoteCluster, Settings'in alt
// kümesidir (iki sayaç da tutulur).
// Dilim-1 notu (plan §7.1): `.controls > button/input/select` yoğunluk
// kurallarına (globals.css) `:where(:not([data-slot]))` eklemek, PageControls
// içine ilk shadcn denetimini koyan dilimin işidir.
export type Page = 'Settings' | 'RemoteCluster' | 'Logs' | 'Trace' | 'Traces' | 'Explore' | 'Metrics' | 'Shell';

export const PAGE_FILES: Record<Page, readonly string[]> = {
  Settings: ['pages/Settings.tsx', 'pages/settings/'],
  RemoteCluster: ['pages/settings/ClustersTab.tsx', 'pages/settings/SpanClusterValuesPanel.tsx'],
  Logs: [
    'pages/Logs.tsx', 'pages/logs/', 'components/LogTable.tsx', 'components/LogsHistogram.tsx',
    'components/LogFieldsPanel.tsx', 'components/LogPatternsPanel.tsx', 'components/LogPillEditor.tsx',
    'components/LogContextModal.tsx', 'components/KqlSearchInput.tsx',
  ],
  Trace: [
    'pages/Trace.tsx', 'pages/trace/', 'components/TraceWaterfall.tsx', 'components/SpanDetail.tsx',
    'components/SpanK8sSection.tsx', 'components/TracePeekDrawer.tsx', 'components/FlameGraph.tsx',
  ],
  Traces: ['pages/Traces.tsx', 'pages/traces/', 'components/traces/'],
  Explore: ['pages/Explore.tsx', 'pages/explore/', 'components/MetricsExplorer.tsx', 'components/LogsExplorer.tsx'],
  Metrics: ['pages/Metrics.tsx', 'components/viz/MetricQueryEditor.tsx', 'components/viz/TimeSeriesPanel.tsx'],
  Shell: [
    'components/AppShell.tsx', 'components/Topbar.tsx', 'components/Sidebar.tsx',
    'components/CommandPalette.tsx', 'components/TimeRangePicker.tsx',
  ],
};

/** `rel` (src/'ye göre, `/` ayraçlı) bu sayfaya mı ait? */
export function inPage(page: Page, rel: string): boolean {
  return PAGE_FILES[page].some(e => (e.endsWith('/') ? rel.startsWith(e) : rel === e));
}
