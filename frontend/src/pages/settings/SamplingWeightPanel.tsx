// SamplingWeightPanel.tsx — v0.10.2430: upstream-sampling weight rules +
// verification (docs/operator/tail-sampling-weights.md). Own GET/PUT
// (/api/admin/ingest-sampling, admin + audit settings.ingest_sampling.update).
// Admin screen: not translated.
//
// Table kind: shadcn Table (T1 justification — server-sorted summary, a
// handful of (source, weight) cells, not sortable, rows not clickable).
// No polling: one read on mount + explicit "Refresh" / "Count stored".
import { useEffect, useState, type FormEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { CheckboxField, Field, SelectField, TextareaField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import type { IngestSampling, IngestSamplingStored } from '@/lib/types';
import { FlashBox, SectionTitle, humanize } from './shared';
import { SamplingRuleShadowSection } from './SamplingRuleShadowSection';
import { FALLBACK_RULES, HASH_RULES, POLICY_RULES, impliedFactor, parseSamplingRules, withShares } from './samplingWeight';

const INTRO = 'mb-2.5 text-[length:var(--fs-sm)] text-(--text2)';
const pct = (x: number) => `${(x * 100).toFixed(1)} %`;
const fmtW = (w: number) => (Number.isInteger(w) ? String(w) : w.toFixed(3));

export function SamplingWeightPanel() {
  const q = useQuery({ queryKey: ['settings', 'ingest-sampling'], queryFn: () => api.ingestSampling(), retry: false });
  const [mode, setMode] = useState<IngestSampling['mode']>('off');
  const [def, setDef] = useState('1');
  const [ts, setTs] = useState(true);
  const [attrs, setAttrs] = useState(true);
  const [rules, setRules] = useState('');
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [stored, setStored] = useState<IngestSamplingStored | null>(null);
  const [storedBusy, setStoredBusy] = useState(false);

  const apply = (c: IngestSampling) => {
    setMode(c.mode); setDef(String(c.defaultWeight));
    setTs(c.honorTracestate !== false); setAttrs(c.honorAttributes !== false);
    setRules(c.rules.length ? JSON.stringify(c.rules, null, 2) : '');
  };
  useEffect(() => { if (q.data) apply(q.data.settings); }, [q.data]);
  useEffect(() => { if (q.error) setFlash({ kind: 'err', text: humanize(q.error) }); }, [q.error]);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const parsed = parseSamplingRules(rules);
    if (typeof parsed === 'string') { setFlash({ kind: 'err', text: parsed }); return; }
    const d = Number(def);
    if (!(d >= 1)) { setFlash({ kind: 'err', text: 'Default weight must be a number ≥ 1' }); return; }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putIngestSampling({ mode, defaultWeight: d, honorTracestate: ts, honorAttributes: attrs, rules: parsed });
      apply(saved);
      setFlash({ kind: 'ok', text: `Saved — mode ${saved.mode}; ingest pods pick it up within 30 s.` });
      void q.refetch();
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const countStored = async () => {
    setStoredBusy(true);
    try { setStored(await api.ingestSamplingStored(300)); } catch (err) { setFlash({ kind: 'err', text: humanize(err) }); } finally { setStoredBusy(false); }
  };

  const loaded = q.isFetched;
  const off = !loaded || busy;
  const pod = q.data?.pod;
  const tally = withShares(pod?.tally ?? []);
  const storedRows = withShares(stored?.rows ?? []);

  return (
    <form onSubmit={save} className="mt-7 border-t border-(--border) pt-5">
      <SectionTitle>Upstream sampling weight</SectionTitle>
      <div className={INTRO}>
        When traces are sampled before they reach Coremetry (e.g. a collector keeps every failing trace plus 5 % of the rest),
        each stored span stands for more than one real span. Coremetry stamps <code>spans.sample_weight</code> at ingest from,
        in order: the W3C tracestate <code>ot=th</code> threshold, a <code>sampling.weight</code> / <code>SampleRate</code> attribute,
        the rules below (first match; <code>attr</code> is looked up on span → scope → resource), then the default weight.
        Use <b>shadow</b> first: weights are derived and counted here but 1 is stored. Weighted reads are a later phase — today
        no chart changes. {q.data && !q.data.columnStored && <b>The sample_weight column is not present yet — weights are not stored.</b>}
      </div>
      <div className="grid grid-cols-2 gap-3">
        <SelectField label="Mode" value={mode} disabled={off} onChange={e => setMode(e.target.value as IngestSampling['mode'])}>
          <option value="off">off — weight 1, no work</option>
          <option value="shadow">shadow — derive + count, store 1</option>
          <option value="on">on — store the weight</option>
        </SelectField>
        <Field label="Default weight" small hint="no source, no rule" inputMode="decimal" value={def}
          onChange={e => setDef(e.target.value)} disabled={off} placeholder="1" />
      </div>
      <CheckboxField className="mt-2.5" label="Honour tracestate ot=th" hint="OTel probability sampling threshold"
        checked={ts} disabled={off} onCheckedChange={v => setTs(v)} />
      <CheckboxField className="mt-2.5" label="Honour weight attributes" hint="sampling.weight, SampleRate"
        checked={attrs} disabled={off} onCheckedChange={v => setAttrs(v)} />
      <TextareaField className="mt-2.5" label="Rules (JSON)" rows={8} controlClassName="font-mono" value={rules} disabled={off}
        onChange={e => setRules(e.target.value)}
        hint='[{"name","attr","value","status":"error","traceError":true,"hashPercent","hashSalt","hashBucket":"in|out","weight"}] — all set conditions must hold; no condition = catch-all' />
      <div className="mt-2 flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" disabled={off}
          onClick={() => setRules(JSON.stringify(POLICY_RULES, null, 2))}>Preset: collector records policy (exact)</Button>
        <Button type="button" variant="outline" size="sm" disabled={off}
          onClick={() => setRules(JSON.stringify(HASH_RULES, null, 2))}>Preset: reproduce probabilistic hash</Button>
        <Button type="button" variant="outline" size="sm" disabled={off}
          onClick={() => setRules(JSON.stringify(FALLBACK_RULES, null, 2))}>Preset: status fallback (biased)</Button>
      </div>
      <div className="mt-3">
        <ActionRow confirm={<Button type="submit" loading={busy} disabled={!loaded}>Save</Button>} />
      </div>
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}

      <div className="mt-5 text-[length:var(--fs-sm)] font-semibold">Derived on this pod</div>
      <div className={INTRO}>
        {pod ? <>Pod <code>{pod.host}</code> since start{pod.ingestRole ? '' : ' — this pod does not ingest (split install); use “Count stored” for the fleet'}
          {tally.length > 0 && <> · implied factor ×{impliedFactor(tally).toFixed(2)}</>}
          {pod.overflow > 0 && <> · {pod.overflow} spans beyond the table cap</>}</> : '—'}
        <Button type="button" variant="ghost" size="sm" className="ml-2" onClick={() => void q.refetch()}>Refresh</Button>
      </div>
      {tally.length > 0 && (
        <Table>
          <TableHeader><TableRow><TableHead>Source / rule</TableHead><TableHead>Weight</TableHead><TableHead>Spans</TableHead><TableHead>Share</TableHead></TableRow></TableHeader>
          <TableBody>
            {tally.map(r => (
              <TableRow key={`${r.source}|${r.weight}`}>
                <TableCell>{r.source}</TableCell><TableCell>{fmtW(r.weight)}</TableCell>
                <TableCell>{r.spans.toLocaleString()}</TableCell><TableCell>{pct(r.share)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <div className="mt-5 text-[length:var(--fs-sm)] font-semibold">Stored (fleet, last 5 min)</div>
      <div className={INTRO}>
        Reads the weight column of spans stored in the last 5 minutes (one short raw read, on demand).
        <Button type="button" variant="outline" size="sm" className="ml-2" loading={storedBusy}
          disabled={!q.data?.columnStored} onClick={() => void countStored()}>Count stored</Button>
        {stored && storedRows.length > 0 && <> · implied factor ×{impliedFactor(storedRows).toFixed(2)}</>}
      </div>
      {stored && (
        <Table>
          <TableHeader><TableRow><TableHead>Weight</TableHead><TableHead>Spans</TableHead><TableHead>Share</TableHead></TableRow></TableHeader>
          <TableBody>
            {storedRows.length === 0 && <TableRow><TableCell colSpan={3}>No spans in the window.</TableCell></TableRow>}
            {storedRows.map(r => (
              <TableRow key={r.weight}>
                <TableCell>{fmtW(r.weight)}</TableCell><TableCell>{r.spans.toLocaleString()}</TableCell><TableCell>{pct(r.share)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <SamplingRuleShadowSection />
    </form>
  );
}
