import { lazy, Suspense, type ComponentType } from 'react';
import { SETTINGS_TAB_INDEX } from './settings/tabIndex';
import { useParams, Navigate, NavLink } from 'react-router-dom';
import { Topbar } from '@/components/Topbar';
import { Empty, Spinner } from '@/components/Spinner';
import { useAuth } from '@/components/AuthProvider';
import { IconLock } from '@/components/icons';

// Settings — the consolidated system-settings area. The fifteen former
// tab components that lived as functions inside one ~3950-line Settings.tsx
// are now one small file each under pages/settings/, mounted inside a left
// vertical sub-nav (the same `sys-subnav` / `sys-layout` / `sys-content`
// shell the System area uses). Deep links live at /settings/<slug>; bare
// /settings or an unknown slug redirects to the first section. Behaviour of
// each section — every save handler + API call — is unchanged; this was a
// pure file split.

import { PageShell } from '@/components/coremetry/PageShell';

// shadcn dilim 1 R0 — sekmeler tembel: her sekme kendi chunk'ı
// (audit §10 j). Eskiden 31 sekme tek ~167 KB gz chunk'tı; şimdi
// bir Settings ziyareti kabuk + bir sekme indirir. Sekme bileşenleri
// adlı export olduğundan `.then(m => ({ default }))`.
const SMTPTab = lazy(() => import('./settings/SmtpTab').then(m => ({ default: m.SMTPTab })));
const ChannelsTab = lazy(() => import('./settings/ChannelsTab').then(m => ({ default: m.ChannelsTab })));
const TeamRoutingTab = lazy(() => import('./settings/TeamRoutingTab').then(m => ({ default: m.TeamRoutingTab })));
const NotifyPolicyTab = lazy(() => import('./settings/NotifyPolicyTab').then(m => ({ default: m.NotifyPolicyTab }))); // v0.10.1533
const ApiTokensTab = lazy(() => import('./settings/ApiTokensTab').then(m => ({ default: m.ApiTokensTab })));
const MaintenanceTab = lazy(() => import('./settings/MaintenanceTab').then(m => ({ default: m.MaintenanceTab })));
const AITab = lazy(() => import('./settings/AiTab').then(m => ({ default: m.AITab })));
const AiSreTab = lazy(() => import('./settings/AiSreTab').then(m => ({ default: m.AiSreTab })));
const KnowledgeTab = lazy(() => import('./settings/KnowledgeTab').then(m => ({ default: m.KnowledgeTab })));
const TempoTab = lazy(() => import('./settings/TempoTab').then(m => ({ default: m.TempoTab })));
const MetricsBackendTab = lazy(() => import('./settings/MetricsBackendTab').then(m => ({ default: m.MetricsBackendTab })));
const TraceFacetsTab = lazy(() => import('./settings/TraceFacetsTab').then(m => ({ default: m.TraceFacetsTab })));
const ExternalLinksTab = lazy(() => import('./settings/ExternalLinksTab').then(m => ({ default: m.ExternalLinksTab })));
const DevOpsTab = lazy(() => import('./settings/DevOpsTab').then(m => ({ default: m.DevOpsTab })));
const McpServersTab = lazy(() => import('./settings/McpServersTab').then(m => ({ default: m.McpServersTab })));
const ClustersTab = lazy(() => import('./settings/ClustersTab').then(m => ({ default: m.ClustersTab })));
const ArgoCDTab = lazy(() => import('./settings/ArgoCDTab').then(m => ({ default: m.ArgoCDTab })));
const OracleTab = lazy(() => import('./settings/OracleTab').then(m => ({ default: m.OracleTab })));
const EntitiesTab = lazy(() => import('./settings/EntitiesTab').then(m => ({ default: m.EntitiesTab })));
const ElasticTab = lazy(() => import('./settings/ElasticTab').then(m => ({ default: m.ElasticTab })));
const KibanaTab = lazy(() => import('./settings/KibanaTab').then(m => ({ default: m.KibanaTab })));
const LogBridgeTab = lazy(() => import('./settings/LogBridgeTab').then(m => ({ default: m.LogBridgeTab })));
const LDAPTab = lazy(() => import('./settings/LdapTab').then(m => ({ default: m.LDAPTab })));
const SSOTab = lazy(() => import('./settings/SsoTab').then(m => ({ default: m.SSOTab })));
const RetentionTab = lazy(() => import('./settings/RetentionTab').then(m => ({ default: m.RetentionTab })));
const AnomalyPromotionTab = lazy(() => import('./settings/AnomalyTab').then(m => ({ default: m.AnomalyPromotionTab })));
const BrandingTab = lazy(() => import('./settings/BrandingTab').then(m => ({ default: m.BrandingTab })));
const CustomRolesTab = lazy(() => import('./settings/RolesTab').then(m => ({ default: m.CustomRolesTab })));
const PipelineTab = lazy(() => import('./settings/PipelineTab').then(m => ({ default: m.PipelineTab })));
const BackupTab = lazy(() => import('./settings/BackupTab').then(m => ({ default: m.BackupTab })));
const DangerZoneTab = lazy(() => import('./settings/DangerZoneTab').then(m => ({ default: m.DangerZoneTab })));

interface SettingsTab {
  slug: string;
  label: string;
  Comp: ComponentType;
}

// Slug order mirrors the former horizontal tab strip so deep links and the
// operator's muscle memory both stay stable.
// v0.9.1272 — sekme kimlikleri (slug+label) YAPRAK dizinde
// (settings/tabIndex.ts): CommandPalette bileşen import'suz okur.
// Burası yalnız slug→bileşen eşlemesi; TABS dizinden türetilir.
// Tamlık kaynak-pin vitest'inde (settingsTabIndex.test.ts) — dizine
// girip buraya girmeyen (ya da tersi) slug testte patlar.
const TAB_COMPS: Record<string, ComponentType> = {
  'smtp': SMTPTab,
  'channels': ChannelsTab,
  'team-routing': TeamRoutingTab,
  'notify-policy': NotifyPolicyTab,
  'api-tokens': ApiTokensTab,
  'maintenance': MaintenanceTab,
  'ai': AITab,
  'ai-sre': AiSreTab,
  'knowledge': KnowledgeTab,
  'tempo': TempoTab,
  'metrics-backend': MetricsBackendTab,
  'trace-facets': TraceFacetsTab,
  'external-links': ExternalLinksTab,
  'clusters': ClustersTab,
  'argocd': ArgoCDTab,
  'oracle': OracleTab,
  'entities': EntitiesTab,
  'elastic': ElasticTab,
  'kibana': KibanaTab,
  'log-bridge': LogBridgeTab,
  'devops': DevOpsTab,
  'mcp-servers': McpServersTab,
  'ldap': LDAPTab,
  'sso': SSOTab,
  'retention': RetentionTab,
  'anomaly': AnomalyPromotionTab,
  'branding': BrandingTab,
  'roles': CustomRolesTab,
  'pipeline': PipelineTab,
  'backup': BackupTab,
  'danger': DangerZoneTab,
};
const TABS: SettingsTab[] = SETTINGS_TAB_INDEX.map(t => ({ ...t, Comp: TAB_COMPS[t.slug] })).filter(t => !!t.Comp);

export default function SettingsPage() {
  const { section } = useParams<{ section: string }>();
  const { user } = useAuth();

  // Admin-only — same gate the monolithic page enforced. Non-admins see the
  // lock state, not a blank page.
  if (user && user.role !== 'admin') {
    return (
      <>
        <Topbar title="Settings" />
        <PageShell>
          <Empty icon={<IconLock size={28} />} title="Admin access required">
            System settings are only available to administrators.
          </Empty>
        </PageShell>
      </>
    );
  }

  // Bare /settings or an unknown slug → redirect to the first section.
  const active = TABS.find(t => t.slug === section);
  if (!active) {
    return <Navigate to={`/settings/${TABS[0].slug}`} replace />;
  }

  const Body = active.Comp;
  return (
    <>
      <Topbar title="Settings" />
      <PageShell>
        <div className="sys-layout">
          <nav className="sys-subnav" aria-label="Settings sections">
            <div className="sys-subnav-title">Settings</div>
            {TABS.map(t => (
              <NavLink
                key={t.slug}
                to={`/settings/${t.slug}`}
                className={({ isActive }) => 'sys-subnav-item' + (isActive ? ' active' : '')}>
                {t.label}
              </NavLink>
            ))}
          </nav>
          <div className="sys-content">
            <Suspense fallback={<Spinner />}>
              <Body />
            </Suspense>
          </div>
        </div>
      </PageShell>
    </>
  );
}
