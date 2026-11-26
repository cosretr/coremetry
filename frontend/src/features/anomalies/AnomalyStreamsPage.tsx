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
// Platform = yalnız onlar; Elenenler (yalnız admin) = gürültü kapılarında
// elenen olaylar, nedeni ve sayacıyla. v0.10.2473: kip operatör ayarı
// (Settings › Anomaly › ML, varsayılan on) — on iken bu olaylar Servisler
// listesinde görünmez ve Problem adayı olmaz; shadow iken yalnız "elenecekti".
// v0.10.2476: Yük ve yapı (view=load) = Problem açmayan yük / yapı dedektörleri
// (chstore.MLLoadStructureDetectors); invalid kayıt Elenenler'de.
import { Navigate, useSearchParams } from 'react-router';
import { Tabs, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { useAuthRole } from '@/components/AuthProvider';
import { useT } from '@/lib/i18n';
import { ANOMALY_VIEWS, resolveAnomalyView } from '@/lib/anomalyView';
import { urlSpec, useUrlParams, z, zInt, zParam } from '@/lib/urlSchema';
import { legacyAnomalyRedirect } from '@/lib/anomalyHref';
import { Topbar } from '@/components/Topbar';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { TriageCrumb } from '@/components/TriageCrumb';
import { AnomalyStreams } from './streams';
import { PageShell } from '@/components/coremetry/PageShell';
import { MLTab, resolveMLSignal, type MLSignal } from '@/components/coremetry/anomalies/MLTab';
import { UnifiedAnomalies } from '@/components/coremetry/anomalies/UnifiedAnomalies';
import type { AnomalyView } from '@/lib/types';

// v0.10.2476 — ?view= lib/urlSchema üzerinden (küme dışı değer → servis
// listesi); görünüm değişince iki tablonun sayfası başa (page / donePage
// varsayılan 0 → param silinir). Rol kontrolü resolveAnomalyView'da.
const VIEW_URL_SPEC = urlSpec({
  view: zParam<string>(z.string().check(z.refine(v => (ANOMALY_VIEWS as readonly string[]).includes(v))), ''),
  page: zParam(zInt({ min: 0 }), 0),
  donePage: zParam(zInt({ min: 0 }), 0),
});

const VIEW_TEXT: Record<Exclude<AnomalyView, ''>, { hint: string; empty: string }> = {
  platform: { hint: 'anomalies.view.platformHint', empty: 'anomalies.view.platformEmpty' },
  load: { hint: 'anomalies.view.loadHint', empty: 'anomalies.view.loadEmpty' },
  held: { hint: 'anomalies.view.heldHint', empty: 'anomalies.view.heldEmpty' },
};

export default function AnomalyStreamsPage() {
  const [sp, setSp] = useSearchParams();
  const tr = useT();
  const isAdmin = useAuthRole() === 'admin';
  const [urlView, setUrlView] = useUrlParams(VIEW_URL_SPEC);
  const view = resolveAnomalyView(urlView.view, isAdmin);
  const setView = (v: string) => setUrlView({ view: v, page: 0, donePage: 0 });
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
            <TabsTrigger value="load">{tr('anomalies.view.load')}</TabsTrigger>
            <TabsTrigger value="platform">{tr('anomalies.view.platform')}</TabsTrigger>
            {isAdmin && <TabsTrigger value="held">{tr('anomalies.view.held')}</TabsTrigger>}
          </TabsList>
        </Tabs>
        {view === '' ? (
          <UnifiedAnomalies signal={signal} onSignal={setSignal}
            detectorStatus={<><MLTab signal={signal} /><AnomalyStreams withHistory={false} /></>} />
        ) : (
          <UnifiedAnomalies key={view} view={view} signal={signal} onSignal={setSignal}
            hint={tr(VIEW_TEXT[view].hint)} emptyText={tr(VIEW_TEXT[view].empty)} />
        )}
      </PageShell>
    </>
  );
}
