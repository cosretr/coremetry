// StmtDistribution — v0.10.1972 (operatör: Statement süre gösterimi "1 2 4", 4 = süre dağılımı; mockup
// stmt-duration onaylı). Seçili penceredeki çağrıların süre histogramı: log ölçekli kovalar (1-2-5),
// karekök yükseklik, medyan + p95 kesikli işaret. Veri sunucunun TDigest'ten çıkardığı YAKLAŞIK kovalar
// (ek sorgu yok — detay yükünde). Çizim CSS (.stmt-dist*), grafik motoru gerektirmez.
import { useMemo } from 'react';
import { fmtNum } from '@/lib/utils';
import type { DBStmtDetail } from '@/lib/types';
import { stmtDistView, fmtDistMs } from './stmtDist';

export function StmtDistributionSection({ detail }: { detail: DBStmtDetail }) {
  const v = useMemo(
    () => stmtDistView(detail.distribution, detail.summary?.p50Ms, detail.summary?.p95Ms),
    [detail.distribution, detail.summary?.p50Ms, detail.summary?.p95Ms]);
  if (!v) return null;
  return (
    <section className="stmt-dist" aria-label="Süre dağılımı">
      <div className="stmt-dist__head">
        Süre dağılımı
        <span className="stmt-dist__sub">yaklaşık · {fmtNum(v.total)} çağrı · logaritmik süre ekseni</span>
      </div>
      <div>
        <div className="stmt-dist__bars">
          {v.bars.map(b => (
            <div key={b.key} className="stmt-dist__col" title={`${b.range}: ${fmtNum(b.count)} çağrı`}>
              <div className={b.slow ? 'stmt-dist__bar stmt-dist__bar--slow' : 'stmt-dist__bar'}
                style={{ height: `${b.heightPct}%` }} />
            </div>
          ))}
          {v.markers.map(m => (
            <div key={m.kind} className={`stmt-dist__mark stmt-dist__mark--${m.kind}`} style={{ left: `${m.leftPct}%` }}>
              <span>{m.kind === 'p50' ? 'medyan' : 'p95'} {fmtDistMs(m.ms)}</span>
            </div>
          ))}
        </div>
        <div className="stmt-dist__axis">
          {v.bars.map(b => <span key={b.key}>{b.label}</span>)}
        </div>
      </div>
      <div className="stmt-dist__cap">
        Çağrıların %{Math.round(v.under50Pct)} kadarı 50 ms altında; %{v.over1sPct.toFixed(1).replace('.', ',')} kadarı 1 saniyenin üstünde.
      </div>
    </section>
  );
}
