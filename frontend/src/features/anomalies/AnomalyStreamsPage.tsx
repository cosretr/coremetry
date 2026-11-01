// AnomalyStreamsPage — /anomalies. v0.10.1743 (log ML dilim 0): iki sekme,
// URL kaynak (?tab=detectors|logml). Eski dedektörler "Dedektörler"
// sekmesinde olduğu gibi; "Log ML" sekmesi ayrı tablo (ml_events),
// karışmaz (docs/audits/ml-audit.md K1).
import { Navigate, useSearchParams } from 'react-router-dom';
import { legacyAnomalyRedirect } from '@/lib/anomalyHref';
import { Topbar } from '@/components/Topbar';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { TriageCrumb } from '@/components/TriageCrumb';
import { AnomalyStreams } from './streams';
import { PageShell } from '@/components/coremetry/PageShell';
import { MLTab, AnomaliesTabBar, resolveAnomaliesTab, resolveMLSignal, type AnomaliesTab, type MLSignal } from '@/components/coremetry/anomalies/MLTab';


export default function AnomalyStreamsPage() {
  const [sp, setSp] = useSearchParams();
  const tab = resolveAnomaliesTab(sp.get('tab'));
  const signal = resolveMLSignal(sp.get('signal'));
  const setSignal = (s: MLSignal) => {
    const next = new URLSearchParams(sp);
    if (s) next.set('signal', s); else next.delete('signal');
    setSp(next, { replace: true });
  };
  const setTab = (t: AnomaliesTab) => {
    const next = new URLSearchParams(sp);
    if (t === 'detectors') next.delete('tab'); else next.set('tab', t);
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
        <AnomaliesTabBar value={tab} onChange={setTab} />
        {tab === 'ml' ? <MLTab signal={signal} onSignal={setSignal} /> : <AnomalyStreams />}
      </PageShell>
    </>
  );
}
