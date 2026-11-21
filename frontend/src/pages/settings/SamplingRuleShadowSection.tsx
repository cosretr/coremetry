// SamplingRuleShadowSection.tsx — v0.10.2441: span error-rate rules scored
// on stored vs model-weighted values while ingest.sampling.mode = shadow
// (GET /api/admin/ingest-sampling/rule-shadow, admin). Admin screen: not
// translated. Observation only — nothing here changes alerting.
//
// Table kind: shadcn Table (T1 justification — server-sorted summary by
// disagreement, capped at 50 rows, not sortable, rows not clickable).
// No polling: one read on mount + explicit "Refresh".
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { api } from '@/lib/api';
import { fmtDateTime } from '@/lib/utils';
import { fmtShadowValue, ruleShadowTotals } from './samplingWeight';

const INTRO = 'mb-2.5 text-[length:var(--fs-sm)] text-(--text2)';
const MAX_ROWS = 50;

export function SamplingRuleShadowSection() {
  const q = useQuery({ queryKey: ['settings', 'ingest-sampling', 'rule-shadow'], queryFn: () => api.ingestSamplingRuleShadow(), retry: false });
  const d = q.data;
  const rows = d?.state.rows ?? [];
  const tot = ruleShadowTotals(rows);
  const m = d?.model;

  return (
    <div className="mt-5">
      <div className="text-[length:var(--fs-sm)] font-semibold">Rule thresholds under weighting (shadow)</div>
      <div className={INTRO}>
        With mode <b>on</b>, span error rates drop to their true (much lower) values and every absolute error-rate
        threshold sees different numbers. While mode is <b>shadow</b> the evaluator and the anomaly detector also score
        each span-sourced error-rate rule on a model-weighted value (failing spans weigh{' '}
        <b>{m ? fmtShadowValue(m.errWeight) : '—'}</b>, the rest <b>{m ? fmtShadowValue(m.okWeight) : '—'}</b>
        {m?.basis ? <> — from your rules, {m.basis}</> : null}). Nothing is changed; the estimate is at or below what
        mode on will read (non-failing spans of failing traces are kept at weight 1 upstream).
        {d && !d.modelOk && <b> The rules give no weight spread — nothing can be estimated.</b>}
        {d && d.mode !== 'shadow' && <> Mode is <b>{d.mode}</b>: counts are not being recorded.</>}
        <Button type="button" variant="ghost" size="sm" className="ml-2" onClick={() => void q.refetch()}>Refresh</Button>
      </div>
      {q.error && <div className={INTRO}>Could not load: {String((q.error as Error).message ?? q.error)}</div>}
      {d && rows.length > 0 && (
        <>
          <div className={INTRO}>
            Since {fmtDateTime(new Date(d.state.since))} (updated {fmtDateTime(new Date(d.state.updatedAt))}):
            {' '}{tot.evals.toLocaleString()} evaluations · fire today {tot.today.toLocaleString()} · would fire weighted{' '}
            {tot.weighted.toLocaleString()} · <b>stop firing {tot.todayOnly.toLocaleString()}</b> · start firing{' '}
            {tot.weightedOnly.toLocaleString()}
            {d.state.overflow ? <> · {d.state.overflow} beyond the row cap</> : null}
          </div>
          <Table>
            <TableHeader><TableRow>
              <TableHead>Rule</TableHead><TableHead>Threshold</TableHead><TableHead>Retune to</TableHead>
              <TableHead>Evals</TableHead><TableHead>Fire today</TableHead><TableHead>Fire weighted</TableHead>
              <TableHead>Stop</TableHead><TableHead>Start</TableHead><TableHead>Last (stored → weighted)</TableHead>
            </TableRow></TableHeader>
            <TableBody>
              {rows.slice(0, MAX_ROWS).map(r => (
                <TableRow key={`${r.family}|${r.ruleId}`}>
                  <TableCell>{r.name || r.ruleId} <span className="text-(--text3)">· {r.metric}</span></TableCell>
                  <TableCell>{r.family === 'anomaly' ? `absFloor ${fmtShadowValue(r.threshold)} %` : `${r.comparator} ${fmtShadowValue(r.threshold)} %`}</TableCell>
                  <TableCell>{r.family === 'anomaly' ? '—' : `${fmtShadowValue(r.retune)} %`}</TableCell>
                  <TableCell>{r.evals.toLocaleString()}</TableCell>
                  <TableCell>{r.todayFires.toLocaleString()}</TableCell>
                  <TableCell>{r.weightedFires.toLocaleString()}</TableCell>
                  <TableCell className={r.todayOnly > 0 ? 'font-semibold' : undefined}>{r.todayOnly.toLocaleString()}</TableCell>
                  <TableCell className={r.weightedOnly > 0 ? 'font-semibold' : undefined}>{r.weightedOnly.toLocaleString()}</TableCell>
                  <TableCell>{r.lastService ? `${r.lastService}: ${fmtShadowValue(r.lastToday)} → ${fmtShadowValue(r.lastWeighted)} %` : '—'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
      {d && d.hints.length > 0 && (
        <>
          <div className="mt-3 text-[length:var(--fs-sm)] font-semibold">Recommended retune before switching to on</div>
          <div className={INTRO}>
            Enabled alert rules whose absolute threshold shifts under weighting (span path; services the VictoriaMetrics
            source serves are unaffected). Not applied automatically — edit the rule when you switch the mode.
          </div>
          <Table>
            <TableHeader><TableRow><TableHead>Rule</TableHead><TableHead>Now</TableHead><TableHead>On weighted rates</TableHead><TableHead>Basis</TableHead></TableRow></TableHeader>
            <TableBody>
              {d.hints.slice(0, MAX_ROWS).map(h => (
                <TableRow key={h.ruleId}>
                  <TableCell>{h.name || h.ruleId} <span className="text-(--text3)">· {h.metric}</span></TableCell>
                  <TableCell>{h.comparator} {fmtShadowValue(h.threshold)}</TableCell>
                  <TableCell>{h.comparator} {fmtShadowValue(h.retune)}</TableCell>
                  <TableCell>{h.note}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
    </div>
  );
}
