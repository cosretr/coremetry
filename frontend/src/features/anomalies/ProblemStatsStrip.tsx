// ProblemStatsStrip — v0.10.774 (Dynatrace paritesi #7: MTTR / seri).
//
// Problems (Inbox) başlığında yaşam döngüsü şeridi: pencerede açılan /
// çözülen problem serisi, MTTR (çözülenlerden), şu an açık olanların
// öncelik dağılımı, açılanların kategori dağılımı. Pencere URL'de (?sw=,
// paylaşılabilir görünüm), env Topbar seçicisiyle aynı (liste ile aynı
// satırlar). Liste ZAMANSIZ (açık kuyruk) olduğu için şeridin kendi
// penceresi var. Viewer görür; okuma 60 s önbellekli.
import { useSearchParams } from 'react-router-dom';
import { StatTile } from '@/components/ui/StatTile';
import { TimeChart } from '@/components/charts/TimeChart';
import { useProblemStats } from '@/lib/queries';
import { fmtNum } from '@/lib/utils';
import {
  PROBLEM_STATS_PARAM, PROBLEM_STATS_WINDOWS, categoryChips, fmtMttr, priorityChips, statsChart, statsWindowFromParam,
  type ProblemStatsWindow,
} from './problemStats';

export function ProblemStatsStrip({ env }: { env?: string }) {
  const [params, setParams] = useSearchParams();
  const win = statsWindowFromParam(params.get(PROBLEM_STATS_PARAM));
  const q = useProblemStats(win, env);
  const st = q.data;
  const setWin = (w: ProblemStatsWindow) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (w === '24h') next.delete(PROBLEM_STATS_PARAM); else next.set(PROBLEM_STATS_PARAM, w);
    return next;
  }, { replace: true });
  const chart = st ? statsChart(st) : null;
  return (
    <div style={{ marginBottom: 14 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 12, fontWeight: 600 }}>Yaşam döngüsü</span>
        <select value={win} onChange={e => setWin(e.target.value as ProblemStatsWindow)} aria-label="Yaşam döngüsü penceresi">
          {PROBLEM_STATS_WINDOWS.map(w => <option key={w} value={w}>son {w}</option>)}
        </select>
        {st?.truncated && <span className="badge b-warn" title="Pencere 5000 problemden fazla; eski uç kırpıldı">kırpıldı</span>}
        {q.isError && <span className="badge b-err" title={String(q.error)}>okunamadı</span>}
        {st && (
          <span style={{ color: 'var(--text3)', fontSize: 11 }}>
            açık: {priorityChips(st.byPriority).map(c => `${c.key} ${fmtNum(c.count)}`).join(' · ')}
            {categoryChips(st.byCategory).length > 0 && <> · açılan: {categoryChips(st.byCategory).map(c => `${c.key} ${fmtNum(c.count)}`).join(' · ')}</>}
          </span>
        )}
      </div>
      {st && (
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'stretch' }}>
          <StatTile label="Açılan"><span className="mono">{fmtNum(st.opened)}</span></StatTile>
          <StatTile label="Çözülen"><span className="mono">{fmtNum(st.resolved)}</span></StatTile>
          <StatTile label="Açık (şimdi)" tone={st.openNow > 0 ? 'warn' : undefined}>
            <span className="mono">{fmtNum(st.openNow)}</span>
            {st.carried > 0 && <span className="field-hint"> {fmtNum(st.carried)} devreden</span>}
          </StatTile>
          <StatTile label="MTTR (ortanca)">
            <span className="mono" title={`ortalama ${fmtMttr(st.mttr.meanS, st.mttr.n)} · p90 ${fmtMttr(st.mttr.p90S, st.mttr.n)} · ${st.mttr.n} çözülen`}>{fmtMttr(st.mttr.medianS, st.mttr.n)}</span>
            {st.mttr.n > 0 && <span className="field-hint"> p90 {fmtMttr(st.mttr.p90S, st.mttr.n)}</span>}
          </StatTile>
          {chart && chart.times.length > 1 && (
            <div style={{ flex: '1 1 320px', minWidth: 0 }}>
              <TimeChart times={chart.times} series={chart.series} height={88} leftUnit="count" />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
