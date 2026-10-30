// lib/palette/pages — v0.10.1658 (etkileşim dilimi P2, plan §1.1). ⌘K sayfa
// kataloğu ve sonuç satırı tipi; components/CommandPalette.tsx'ten (v0.5.162 →
// v0.10.1653 gövdesi) AYNEN taşındı. Saf: bileşen importu yok.
import { SETTINGS_TAB_INDEX } from '@/pages/settings/tabIndex';
import type { Action } from '@/lib/actions';
import type { LocalActionId } from './localActions';
import type { PaletteGroupId } from './groups';

export type PaletteResult = {
  kind: 'page' | 'service' | 'trace' | 'action' | 'endpoint' | 'operation';
  // v0.10.1658 (P2) — grup başlığı ezmesi: kind'dan türeyen grubu taşımayan
  // satırlar (problem kimliği, kimlik araması, "tüm sonuçlar") için.
  group?: PaletteGroupId;
  label: string;
  // v0.9.1270 (B5#1) — sayfa adı sidebar'la TEK kaynaktan: navKey
  // varsa etiket t(navKey) ile çözülür (i18n + TR paritesi bedava);
  // aliases eski/alternatif adları aranabilir tutar (paletteScore).
  navKey?: string;
  aliases?: string[];
  // v0.9.1272 (#3/Q3) — admin-kapılı sayfalar palette rol-süzgeçli:
  // viewer'a gidemeyeceği Settings/System girişlerini önermek boş vaat.
  adminOnly?: boolean;
  hint?: string;
  // navigate target — set for page/service/trace; absent for actions
  // (selecting an action enters param-prompt mode, doesn't navigate).
  to?: string;
  // action payload — set when kind === 'action'.
  action?: Action;
  // v0.10.1665 (P3) — yerel eylem (sunucu durumu yazmaz; lib/palette/localActions).
  local?: LocalActionId;
  score?: number;
};

// v0.8.525 — güncel rotalara hizalandı. Önceki katalogda ölü hedefler
// vardı (Home /, Topology /topology, Errors /errors, Status /status —
// hepsi redirect/retired) ve tüm 'Admin · …' girişleri /admin/* idi;
// admin sayfaları v0.8.9'da /system/:tab altına taşındı. Eksik canlı
// sayfalar da eklendi (Inbox, Endpoints, Service Map, External, Hosts,
// Trace compare, Events, AI). Sidebar'dan gizli sayfalar (Monitors,
// Profiling, External, Hosts, Events) ⌘K'da BİLEREK kalır — keşif
// yüzeyi burasıdır.
export const PAGES: PaletteResult[] = [
  // Triage
  { kind: 'page', label: 'Inbox',       navKey: 'nav.inbox', aliases: ['inbox'], hint: 'Unified triage queue', to: '/inbox' },
  { kind: 'page', label: 'Incidents',   navKey: 'nav.incidents', hint: 'Manual incident log', to: '/incidents' },
  { kind: 'page', label: 'Problems',    navKey: 'nav.problems', aliases: ['problems'], hint: 'Open alert + exception inbox', to: '/problems' },
  { kind: 'page', label: 'Anomalies',   navKey: 'nav.anomalies', hint: 'Log + trace anomaly streams', to: '/anomalies' },
  // v0.10.201 — Inbox/Incidents/Problems/Anomalies'in navKey'siz KOPYA bloğu silindi
  // (⌘K aynı dört sayfayı iki kez listeliyordu, dedup yok); Deployment Report → Rollouts.
  // v0.10.984 (Rollouts v2 P2.3) — sayfa adı «Deployment/Rollouts»; "deployments" araması da bulur.
  { kind: 'page', label: 'Deployment/Rollouts', navKey: 'nav.rollouts', aliases: ['deploy', 'deployments', 'deployment report', 'rollout', 'rollouts'], hint: 'Live workload rollouts + deploy stats', to: '/rollouts' },
  // Services
  { kind: 'page', label: 'Services',    navKey: 'nav.services', hint: 'Per-service RED + latency', to: '/services' },
  { kind: 'page', label: 'Endpoints',   navKey: 'nav.endpoints', hint: 'Per-route RED', to: '/endpoints' },
  { kind: 'page', label: 'Service Map', navKey: 'nav.topology', aliases: ['service map', 'topology'], hint: 'Topology / flows', to: '/service-map' },
  { kind: 'page', label: 'Databases',   navKey: 'nav.databases', hint: 'DBM-style query catalog', to: '/databases' },
  { kind: 'page', label: 'Messaging',   navKey: 'nav.messaging', hint: 'Kafka / RabbitMQ / SQS', to: '/messaging' },
  { kind: 'page', label: 'External',    navKey: 'nav.external', hint: 'Third-party dependencies', to: '/external' },
  { kind: 'page', label: 'Hosts',       navKey: 'nav.hosts', hint: 'Infrastructure inventory', to: '/hosts' },
  // Signals
  { kind: 'page', label: 'Traces',      navKey: 'nav.traces', hint: 'Search raw traces', to: '/traces' },
  { kind: 'page', label: 'Metrics',     navKey: 'nav.metrics', hint: 'Time-series explorer', to: '/metrics' },
  { kind: 'page', label: 'Logs',        navKey: 'nav.logs', hint: 'Elasticsearch logs', to: '/logs' },
  { kind: 'page', label: 'Profiling',   navKey: 'nav.profiling', hint: 'Continuous profiling', to: '/profiling' },
  { kind: 'page', label: 'Trace compare', hint: 'Diff two traces', to: '/trace/compare' },
  // Workspaces
  { kind: 'page', label: 'Explore',     navKey: 'nav.explore', hint: 'Cross-signal ad-hoc query', to: '/explore' },
  // v0.10.1335 — birleşik arama sayfası (GET /api/search).
  { kind: 'page', label: 'Search',      navKey: 'nav.search', aliases: ['search', 'arama', 'find'], hint: 'Services, endpoints, problems, exceptions, traces, logs', to: '/search' },
  { kind: 'page', label: 'Runbooks',    navKey: 'nav.runbooks', hint: 'Operational procedures', to: '/runbooks' },
  { kind: 'page', label: 'Dashboards',  navKey: 'nav.dashboards', hint: 'Operator-curated', to: '/dashboards' },
  // Alerting
  { kind: 'page', label: 'Alerts',      navKey: 'nav.alerts', hint: 'Alert rules + noisy report', to: '/alerts' },
  { kind: 'page', label: 'SLOs',        hint: 'Service level objectives', to: '/slos' },
  { kind: 'page', label: 'Monitors',    hint: 'Synthetic probes', to: '/monitors' },
  { kind: 'page', label: 'Events',      hint: 'Deploy / config markers', to: '/events' },
  // v0.9.952 (UX denetimi F1 / Ö17) — üç CANLI rota katalogda YOKTU:
  // /deploys, /clusters, /watchers. Son ikisi sidebar'da GÖRÜNÜR ama
  // palette'te aranamıyordu; klavyeyle gezinen operatör için "yok"
  // demekle aynı şey. (Üçüncüsü /deploys idi — v0.10.209'da sayfa
  // emekli edildi, rota /rollouts'a yönlendirir; ⌘K girdisi de gitti.)
  { kind: 'page', label: 'Clusters',    navKey: 'nav.clusters', hint: 'Kubernetes / OpenShift inventory', to: '/clusters' },
  { kind: 'page', label: 'Watchers',    hint: 'Log pattern watchers', to: '/watchers' },
  // System (v0.8.9 — /admin/* → /system/:tab)
  { kind: 'page', label: 'System · Overview', adminOnly: true,      hint: 'Internal CH + cache stats', to: '/system/stats' },
  { kind: 'page', label: 'System · ClickHouse', adminOnly: true,    hint: 'CH health + mutations', to: '/system/clickhouse' },
  { kind: 'page', label: 'System · Elasticsearch', adminOnly: true, hint: 'ES backend health', to: '/system/elastic' },
  { kind: 'page', label: 'System · Cluster', adminOnly: true,       hint: 'Cluster topology', to: '/system/cluster' },
  { kind: 'page', label: 'System · Cardinality', adminOnly: true,   hint: 'Attribute cardinality watch', to: '/system/cardinality' },
  { kind: 'page', label: 'System · Catalog', adminOnly: true,       hint: 'Owner / runbook / oncall metadata', to: '/system/catalog' },
  { kind: 'page', label: 'System · Audit log', adminOnly: true,     hint: 'Operator action history', to: '/system/audit' },
  { kind: 'page', label: 'System · SQL', adminOnly: true,           hint: 'Raw CH query console', to: '/system/sql' },
  { kind: 'page', label: 'System · Query', adminOnly: true,         hint: 'Query console', to: '/system/query' },
  { kind: 'page', label: 'System · Status page', adminOnly: true,   hint: 'Components + subscribers', to: '/system/status-page' },
  { kind: 'page', label: 'AI observability', navKey: 'nav.ai', aliases: ['cosre', 'ai observability'], hint: 'CoSRE usage + cost', to: '/ai' },
  // Meta
  { kind: 'page', label: 'Settings',    adminOnly: true, hint: 'AI / SMTP / retention / theme', to: '/settings' },
  { kind: 'page', label: 'Users',       adminOnly: true, hint: 'Role + team management', to: '/users' },
  { kind: 'page', label: 'Public status',  hint: 'Public incident page', to: '/public-status' },
];

/** Rol süzgeçli + yerelleştirilmiş katalog (admin → + Settings sekmeleri). */
export function localizePages(t: (key: string) => string, isAdmin: boolean): PaletteResult[] {
  const base = PAGES
    .filter(pg => !pg.adminOnly || isAdmin)
    .map(pg => (pg.navKey ? { ...pg, label: t(pg.navKey) } : pg));
  if (!isAdmin) return base;
  // v0.9.1272 (#3) — 23 Settings sekmesi aranabilir: "ayar ara…"
  // vaadi artık boş değil. Kaynak yaprak dizin (settings/tabIndex) —
  // bileşen import'u yok, bundle şişmez; alias = slug.
  const settings = SETTINGS_TAB_INDEX.map<PaletteResult>(tb => ({
    kind: 'page', label: `Settings · ${tb.label}`, aliases: [tb.slug],
    hint: 'Settings', to: `/settings/${tb.slug}`, adminOnly: true,
  }));
  return [...base, ...settings];
}
