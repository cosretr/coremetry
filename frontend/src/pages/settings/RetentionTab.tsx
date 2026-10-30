import { useEffect, useId, useState, type FormEvent } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Label } from '@/components/shadcn/label';
import { NativeSelect } from '@/components/shadcn/native-select';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { humanize } from './shared';

// RetentionTab — per-signal TTL controls. Each signal (spans / logs /
// metrics / profiles) takes a number + unit (hours / days). Save calls
// PUT /api/settings/retention which runs ALTER TABLE ... MODIFY TTL on
// the underlying ClickHouse tables. Effect is online — ClickHouse
// re-evaluates TTL on next merge so deletions catch up within ~10 min.
//
// Faz 2 dilim 1 R8 — shadcn Input/NativeSelect/Label + Button, inline stil
// yerine Tailwind utility. Satır düzeni (etiket solda, sayı + birim yan
// yana) aynı: coremetry/Field'ın etiket-üstte anatomisi bu satır biçimine
// uymadığı için alan anatomisinin denetim kısmı (bg-field, --border-control,
// 12px) doğrudan uygulanır; etiket artık <label htmlFor> ile bağlı.
const CONTROL = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

export function RetentionTab() {
  type Unit = 'h' | 'd';
  type Row = { value: string; unit: Unit };
  const empty: Row = { value: '', unit: 'd' };
  const [spans,    setSpans]    = useState<Row>(empty);
  const [logs,     setLogs]     = useState<Row>(empty);
  const [metrics,  setMetrics]  = useState<Row>(empty);
  const [profiles, setProfiles] = useState<Row>(empty);
  const [busy, setBusy] = useState(false);
  const [msg,  setMsg]  = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const decode = (s?: string): Row => {
    const m = s?.match(/^(\d+)([hd])$/);
    return m ? { value: m[1], unit: m[2] as Unit } : empty;
  };
  const encode = (r: Row): string => r.value ? `${r.value}${r.unit}` : '';

  useEffect(() => {
    api.getRetention().then(sp => {
      setSpans(decode(sp.spans));
      setLogs(decode(sp.logs));
      setMetrics(decode(sp.metrics));
      setProfiles(decode(sp.profiles));
    }).catch(() => {});
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      await api.putRetention({
        spans:    encode(spans),
        logs:     encode(logs),
        metrics:  encode(metrics),
        profiles: encode(profiles),
      });
      setMsg({ kind: 'ok', text: 'Applied — ClickHouse will re-evaluate TTL on next merge (~10 min).' });
    } catch (err) {
      setMsg({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  // Yüzey kabı — SmtpTab ile aynı gerekçe (denetim D7.4): çıplak form
  // açık temada sayfa zeminine yapışıyordu. `.card-static` = `.card`ın
  // etkileşimsiz ikizi. maxWidth 560 → 590: dolgu (14×2) + kenarlık
  // (1×2) içeriği daraltmasın, alan genişlikleri piksel aynı kalsın.
  return (
    <div className="card-static max-w-[590px]">
    <form onSubmit={submit}>
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">Data retention</h2>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Per-signal TTL on the underlying ClickHouse tables. Older data is dropped
        on the next merge cycle. Leave a field blank to keep the current value
        (initial defaults come from the config file: spans 30d, logs 30d,
        metrics 7d, profiles 7d).
      </p>

      <RetentionRow label="Spans"    row={spans}    setRow={setSpans} />
      <RetentionRow label="Logs"     row={logs}     setRow={setLogs} />
      <RetentionRow label="Metrics"  row={metrics}  setRow={setMetrics} />
      <RetentionRow label="Profiles" row={profiles} setRow={setProfiles} />

      <div className="mt-3.5 flex items-center gap-2">
        <Button type="submit" loading={busy}>Apply</Button>
        {msg && (
          <span className={cn('text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {msg.text}
          </span>
        )}
      </div>

      <p className="mt-[18px] text-[length:var(--fs-xs)] text-(--text3)">
        Hour-precision TTL is supported (e.g. <code>36h</code>) but ClickHouse
        partitions data per day, so very short retention windows still
        process at day-boundary granularity. Examples: <code>48h</code> = last 2 days,
        <code> 2d</code> = same thing, <code>30d</code> = last 30 days.
      </p>
    </form>
    </div>
  );
}

function RetentionRow({ label, row, setRow }: {
  label: string;
  row: { value: string; unit: 'h' | 'd' };
  setRow: (r: { value: string; unit: 'h' | 'd' }) => void;
}) {
  const id = useId();
  return (
    <div className="mt-2.5 flex items-center gap-2">
      <Label htmlFor={id} className="w-[90px] text-[length:var(--fs-md)] font-normal">{label}</Label>
      <Input id={id} type="number" min={1} value={row.value}
        onChange={e => setRow({ ...row, value: e.target.value })}
        placeholder="(unchanged)"
        className={cn('w-[100px]', CONTROL)} />
      <NativeSelect size="sm" value={row.unit} aria-label={`${label} unit`}
        onChange={e => setRow({ ...row, unit: e.target.value as 'h' | 'd' })}
        className={CONTROL}>
        <option value="h">hours</option>
        <option value="d">days</option>
      </NativeSelect>
    </div>
  );
}
