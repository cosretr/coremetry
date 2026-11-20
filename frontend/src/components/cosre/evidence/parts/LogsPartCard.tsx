// LogsPartCard — v0.10.2363 (S5). `logs` kanıt bloğu: sorgu + toplam (yükten),
// ≤10 örnek satır TraceLogList'in satır dilinde (sevClass + mono ızgara), her
// satırda varsa trace linki. Sabit ≤10 sıralanmamış satır: tablo standardının
// statik liste istisnası (DataTable değil).
import type { CSSProperties } from 'react';
import { Link } from 'react-router';
import { sevClass } from '@/components/traces/TraceLogList';
import { traceHref } from '@/lib/traceHref';
import { tsShort } from '@/lib/utils';
import type { LogsPartPayload } from '@/lib/cosre/evidenceParts';
import { fmtNumTR } from '../stepView';
import { EVIDENCE_PART_STRINGS as T } from './strings';
import { PartShell } from './PartShell';

const ROW: CSSProperties = {
  display: 'grid', gridTemplateColumns: '84px 44px 96px 1fr auto', gap: 6, padding: '2px 0',
  fontSize: 11, fontFamily: 'var(--font-mono)', borderBottom: '1px solid var(--divider)', alignItems: 'baseline',
};
const CLIP: CSSProperties = { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' };

export function LogsPartCard({ p }: { p: LogsPartPayload }) {
  const rows = p.rows.slice(0, 10);
  const meta = [T.logsTotal(fmtNumTR(p.total, 0)), rows.length < p.total ? T.logsShown(rows.length) : ''].filter(Boolean).join(' · ');
  return (
    <PartShell kind="logs" href={p.href} title={<span className="mono">{p.query}</span>} meta={meta}>
      <div className="mt-1" role="list">
        {rows.map((r, i) => (
          <div key={i} role="listitem" style={ROW}>
            <span style={{ color: 'var(--text3)' }}>{tsShort(r.ts * 1e6)}</span>
            <span className={sevClass(r.sev)} style={{ fontWeight: 600 }}>{(r.sev || '').toUpperCase().slice(0, 4) || '—'}</span>
            <span style={{ ...CLIP, color: 'var(--text2)' }} title={r.service}>{r.service}</span>
            <span style={CLIP} title={r.body}>{r.body}</span>
            {r.traceId ? <Link to={traceHref(r.traceId)} title={r.traceId} data-log-trace="">{T.logsTrace}</Link> : <span />}
          </div>
        ))}
      </div>
    </PartShell>
  );
}
