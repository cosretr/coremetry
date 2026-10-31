// AnomalyStreamsPage — /anomalies. v0.10.1743 (log ML dilim 0): iki sekme,
// URL kaynak (?tab=detectors|logml). Eski dedektörler "Dedektörler"
// sekmesinde olduğu gibi; "Log ML" sekmesi ayrı tablo (log_ml_events),
// karışmaz (docs/audits/log-ml-audit.md K1).
import { useSearchParams } from 'react-router-dom';
import { Topbar } from '@/components/Topbar';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { TriageCrumb } from '@/components/TriageCrumb';
import { AnomalyStreams } from './streams';
import { PageShell } from '@/components/coremetry/PageShell';
import { LogMLTab, AnomaliesTabBar, resolveAnomaliesTab, type AnomaliesTab } from '@/components/coremetry/anomalies/LogMLTab';


export default function AnomalyStreamsPage() {
  const [sp, setSp] = useSearchParams();
  const tab = resolveAnomaliesTab(sp.get('tab'));
  const setTab = (t: AnomaliesTab) => {
    const next = new URLSearchParams(sp);
    if (t === 'detectors') next.delete('tab'); else next.set('tab', t);
    setSp(next, { replace: true });
  };
  return (
    <>
      <Topbar title="Anomalies" />
      <PageShell>
        <TriageCrumb label="Anomalies" />
        <SavedViewsBar page="anomalies" />
        <AnomaliesTabBar value={tab} onChange={setTab} />
        {tab === 'logml' ? <LogMLTab /> : <AnomalyStreams />}
      </PageShell>
    </>
  );
}
