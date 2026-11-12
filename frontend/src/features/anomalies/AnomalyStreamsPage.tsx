// AnomalyStreamsPage — /anomalies. v0.10.2027 (operatör: "Anomalies hâlâ
// karışık", "tekilleştirme çalışmadı"): TEK SAYFA, sekme yok. Üstte tek liste
// (UnifiedAnomalies: ML grupları + eski dedektör olayları aynı satır modelinde,
// süren olaylar üstte, bitenler kapalı bölümde); en altta KAPALI "Teknik
// ayrıntı / Dedektör durumu" (ML dedektör karnesi + eski dedektörlerin canlı
// kartları ve susturmaları — silinmedi, yer değiştirdi). Eski ?tab=
// değerleri (detectors | ml | groups) yok sayılır, aynı sayfa açılır;
// ?event= / ?replay= tam sayfaya yönlenir (C1).
import { Navigate, useSearchParams } from 'react-router-dom';
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
  const signal = resolveMLSignal(sp.get('signal'));
  const setSignal = (s: MLSignal) => {
    const next = new URLSearchParams(sp);
    if (s) next.set('signal', s); else next.delete('signal');
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
        <UnifiedAnomalies signal={signal} onSignal={setSignal}
          detectorStatus={<><MLTab signal={signal} /><AnomalyStreams withHistory={false} /></>} />
      </PageShell>
    </>
  );
}
