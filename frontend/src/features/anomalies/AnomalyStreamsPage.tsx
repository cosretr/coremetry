// AnomalyStreamsPage — /anomalies. v0.10.2027 (operatör: "Anomalies hâlâ
// karışık", "tekilleştirme çalışmadı"): TEK SAYFA, sekme yok. Üstte tek liste
// (UnifiedAnomalies: ML grupları + eski dedektör olayları aynı satır modelinde,
// süren olaylar üstte, bitenler kapalı bölümde); en altta KAPALI "Teknik
// ayrıntı / Dedektör durumu" (ML dedektör karnesi + eski dedektörlerin canlı
// kartları ve susturmaları — silinmedi, yer değiştirdi). Eski ?tab=
// değerleri (detectors | ml | groups) yok sayılır, aynı sayfa açılır;
// ?event= / ?replay= tam sayfaya yönlenir (C1).
//
// v0.10.2243 (anomali kartı Dilim C, K8 mockup): üç görünüm sekmesi, URL'de
// ?view= (varsayılan yazılmaz). Servisler = servis ekibi listesi (sınıfı
// telemetry_volume olan ve eski svc_volume_spike satırları SQL'de dışarıda);
// Platform = yalnız onlar; Elenenler (yalnız admin) = yayın kapılarının GÖLGE
// kararıyla tutacağı olaylar — kapılar yürürlükte değil, bu olaylar servis
// listesinde, bildirimde ve Problem'de aynen kalır (sekme yalnız okur).
import { Navigate, useSearchParams } from 'react-router-dom';
import { Tabs, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { useAuthRole } from '@/components/AuthProvider';
import { useT } from '@/lib/i18n';
import { resolveAnomalyView } from '@/lib/anomalyView';
import { legacyAnomalyRedirect } from '@/lib/anomalyHref';
import { Topbar } from '@/components/Topbar';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { TriageCrumb } from '@/components/TriageCrumb';
import { AnomalyStreams } from './streams';
import { PageShell } from '@/components/coremetry/PageShell';
import { MLTab, resolveMLSignal, type MLSignal } from '@/components/coremetry/anomalies/MLTab';
import { UnifiedAnomalies } from '@/components/coremetry/anomalies/UnifiedAnomalies';

export default function AnomalyStreamsPage() {
  const [sp, setSp] = useSearchParams();
  const tr = useT();
  const isAdmin = useAuthRole() === 'admin';
  const view = resolveAnomalyView(sp.get('view'), isAdmin);
  const setView = (v: string) => {
    const next = new URLSearchParams(sp);
    if (v) next.set('view', v); else next.delete('view');
    next.delete('page'); next.delete('donePage');
    setSp(next, { replace: true });
  };
  const signal = resolveMLSignal(sp.get('signal'));
  const setSignal = (s: MLSignal) => {
    const next = new URLSearchParams(sp);
    if (s) next.set('signal', s); else next.delete('signal');
    next.delete('page'); next.delete('donePage'); // v0.10.2051 — süzgeç değişince sayfa başa
    setSp(next, { replace: true });
  };
  const legacy = legacyAnomalyRedirect(sp);
  if (legacy) return <Navigate to={legacy} replace />; // C1: ?event= / ?replay= → tam sayfa
  return (
    <>
      <Topbar title="Anomalies" />
      <PageShell>
        <TriageCrumb label="Anomalies" />
        <SavedViewsBar page="anomalies" />
        <Tabs value={view || 'service'} onValueChange={k => setView(k === 'service' ? '' : k)}>
          <TabsList variant="line" aria-label={tr('anomalies.view.aria')}>
            <TabsTrigger value="service">{tr('anomalies.view.service')}</TabsTrigger>
            <TabsTrigger value="platform">{tr('anomalies.view.platform')}</TabsTrigger>
            {isAdmin && <TabsTrigger value="held">{tr('anomalies.view.held')}</TabsTrigger>}
          </TabsList>
        </Tabs>
        {view === '' ? (
          <UnifiedAnomalies signal={signal} onSignal={setSignal}
            detectorStatus={<><MLTab signal={signal} /><AnomalyStreams withHistory={false} /></>} />
        ) : (
          <UnifiedAnomalies key={view} view={view} signal={signal} onSignal={setSignal}
            hint={tr(view === 'platform' ? 'anomalies.view.platformHint' : 'anomalies.view.heldHint')}
            emptyText={tr(view === 'platform' ? 'anomalies.view.platformEmpty' : 'anomalies.view.heldEmpty')} />
        )}
      </PageShell>
    </>
  );
}
