// TracesLongWaitPanel — v0.10.2466. Admin cap for the opt-in "Wait longer"
// re-run (Traces partial-result note + CoSRE follow-up). The default read
// budget stays 25 s; a "yes" runs that one query with min(60, cap) seconds.
// Saved to system_settings 'traces_long_wait' (audited PUT). English on
// purpose: admin screens are not translated (DECISIONS 2026-11-17).
import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { api } from '@/lib/api';

const KEY = ['traces-long-wait'] as const;

/** Pure: parse + range-check the input; null = invalid. */
export function parseLongWaitCap(raw: string, min: number, max: number): number | null {
  const n = Number(raw.trim());
  if (!Number.isInteger(n) || n < min || n > max) return null;
  return n;
}

export function TracesLongWaitPanel() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: KEY, queryFn: ({ signal }) => api.getTracesLongWait(signal), staleTime: 30_000 });
  const save = useMutation({
    mutationFn: (sec: number) => api.putTracesLongWait(sec),
    onSuccess: () => { void qc.invalidateQueries({ queryKey: KEY }); },
  });
  const [draft, setDraft] = useState('');
  useEffect(() => { if (q.data) setDraft(String(q.data.maxSec)); }, [q.data]);
  const min = q.data?.minSec ?? 25;
  const max = q.data?.limitSec ?? 180;
  const parsed = parseLongWaitCap(draft, min, max);
  return (
    <div className="mb-6" data-panel="traces-long-wait">
      <h3 className="mb-2 text-[13px] font-bold">Query limits · &quot;Wait longer&quot; cap</h3>
      <p className="cell-hint">
        Trace list reads run with a {q.data?.defaultSec ?? 25} s budget. When one hits it, the Traces page
        shows a partial result with a &quot;Wait longer&quot; button and CoSRE asks before retrying. A yes re-runs
        only that query with min(60 s, this cap). Range {min}–{max} s.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <label className="cell-hint" htmlFor="traces-long-wait-cap">Cap (seconds)</label>
        <Input id="traces-long-wait-cap" type="number" min={min} max={max} value={draft}
          onChange={e => setDraft(e.target.value)} className="h-8 w-24 bg-field dark:bg-field" />
        <Button variant="accent" size="sm" disabled={parsed === null || parsed === q.data?.maxSec}
          loading={save.isPending} onClick={() => { if (parsed !== null) save.mutate(parsed); }}>Save</Button>
        {q.data && <span className="badge b-gray">effective {q.data.effectiveSec} s</span>}
        {parsed === null && draft !== '' && <span className="badge b-err">must be {min}–{max}</span>}
        {q.isError && <span className="badge b-err" title={String(q.error)}>could not load</span>}
        {save.isError && <span className="badge b-err" title={String(save.error)}>save failed</span>}
      </div>
    </div>
  );
}
