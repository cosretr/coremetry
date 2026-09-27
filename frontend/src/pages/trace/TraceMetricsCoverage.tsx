// TraceMetricsCoverage — v0.10.968 — özet şeridindeki "Metrik kapsamı a/b pod ⓘ"
// tetiği + kapsam diyaloğu (ui/Popover kind='dialog').
//
// v0.10.968 — Onaylı mockup: hangi span cluster değeri bir Remote Cluster
// kaydına eşlendi, hangisi eşlenmedi ve NEDEN; ardından pod sonuçlarının
// dökümü (veri var / örnek yok / okunamadı / belirsiz / yüklenmedi — sıfır
// olan parça yazılmaz). Tetik yalnız eşlenmeyen pod varken uyarı renginde
// (renk yalnız sapmada); sonuç beklenirken nötr kalır (yükleme bir sapma değil).
//
// v0.10.968 — inceleme turu: okunamayan (502) cluster kapsam açığı sayılmaz
// (coverageSummary; tetik zaman aşımında "12/64" + uyarı demez), sonuç
// satırı hash eki değil SERVİS adı söyler (mockup "(audit-log-prod, 10 sn
// zaman aşımı)").
import { useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { LinkButton, Popover } from '@/components/ui';
import type { CoverageLine, CoverageSummary } from './traceMetrics';

function lineText(l: CoverageLine): { lead: string; rest: string } {
  const n = `${l.pods} pod`;
  switch (l.status) {
    case 'mapped': return { lead: l.name, rest: ` · Thanos · ${n} eşlendi` };
    case 'unmapped': return { lead: l.clusterValue, rest: ` · eşlenmemiş · ${n} — bu cluster için Remote Cluster kaydı yok` };
    case 'no_cluster': return { lead: '(cluster yok)', rest: ` · eşlenmemiş · ${n} — cluster özniteliği yok` };
    case 'off': return { lead: l.clusterValue || '(cluster yok)', rest: ` · ${n} — Thanos Remote Cluster tanımlı değil` };
    case 'pending': return { lead: l.clusterValue || '(cluster yok)', rest: ` · yükleniyor · ${n}` };
    default: return { lead: l.clusterValue || '(cluster yok)', rest: ` · okunamadı · ${n}` };
  }
}

/** v0.10.968 — en çok 3 servis adı, fazlası "+N". */
const names = (xs: string[]) => (xs.length > 3 ? `${xs.slice(0, 3).join(', ')} +${xs.length - 3}` : xs.join(', '));

/** v0.10.968 — sonuç satırı: sıfır olan parça yazılmaz. */
function resultParts(c: CoverageSummary): string[] {
  const parts: string[] = [];
  if (c.withData) parts.push(`${c.withData} pod'da veri var`);
  if (c.noSamples.length) parts.push(`${c.noSamples.length} pod'da örnek yok (${names(c.noSamplesSvcs)})`);
  if (c.failed.length) parts.push(`${c.failed.length} pod okunamadı (${names(c.failedSvcs)}${c.timeout ? ', 10 sn zaman aşımı' : ''})`);
  if (c.ambiguous.length) parts.push(`${c.ambiguous.length} belirsiz`);
  if (c.idle.length) parts.push(`${c.idle.length} yüklenmedi`);
  return parts;
}

export function TraceMetricsCoverage({ summary }: { summary: CoverageSummary }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLButtonElement>(null);
  const warn = summary.mapped < summary.total && !summary.pending;
  const parts = resultParts(summary);
  return (
    <>
      <LinkButton ref={ref} underline="dotted" className={warn ? 'tpm-cov is-warn' : 'tpm-cov'}
        aria-haspopup="dialog" aria-expanded={open}
        onClick={() => setOpen(o => !o)}>
        Metrik kapsamı {summary.mapped}/{summary.total} pod ⓘ
      </LinkButton>
      <Popover anchorRef={ref} open={open} onClose={() => setOpen(false)} kind="dialog" ariaLabel="Metrik kapsamı" width={380}>
        <div className="tpm-cov-title">Metrik kapsamı</div>
        <ul className="tpm-cov-list">
          {summary.lines.map(l => {
            const t = lineText(l);
            return (
              <li key={l.clusterValue || '·'} className={l.status === 'mapped' ? undefined : 'tpm-cov-off'}>
                <span className="mono">{t.lead}</span>{t.rest}
              </li>
            );
          })}
        </ul>
        {parts.length > 0 && <div className="tpm-cov-result">{parts.join(' · ')}</div>}
        <Link to="/settings/clusters" className="tpm-cov-link" onClick={() => setOpen(false)}>Ayarlar › Remote Cluster ↗</Link>
      </Popover>
    </>
  );
}
