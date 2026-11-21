// v0.10.2440 — Trace inceltme · gölge tahmini (System → ClickHouse). Kademeli
// saklama kuralı açık olsaydı 7–30 gün bandındaki her gün partition'ında ne
// kalır / ne kazanılırdı; lider işçi tikte EN ÇOK bir günü ölçer
// (chstore/trace_thinning_measure.go). SİLME YOK, silme düğmesi yok.
import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { Field } from '@/components/coremetry/Field';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { api, apiErrorDetail } from '@/lib/api';
import { fmtBytes, fmtNum, tsLong } from '@/lib/utils';
import type { TraceThinningDay, TraceThinningSettings, TraceThinningStatus } from '@/lib/types';
import { classShares, pct, validateThinning } from './traceThinning';

const QK = ['admin-trace-thinning'];

const COLS: ColumnDef<TraceThinningDay>[] = [
  { id: 'day', label: 'Gün', width: 100, minWidth: 90, priority: 1, sortValue: d => d.day },
  { id: 'spans', label: 'Span', width: 110, minWidth: 80, numeric: true, priority: 1, sortValue: d => d.spans },
  { id: 'bytes', label: 'Disk', width: 100, minWidth: 80, numeric: true, priority: 2, sortValue: d => d.bytes },
  { id: 'traces', label: 'Trace (tahmin)', width: 120, minWidth: 90, numeric: true, priority: 2, sortValue: d => d.traces },
  { id: 'keep', label: 'Kalır (span %)', width: 120, minWidth: 90, numeric: true, priority: 1, sortValue: d => pct(d.keepSpans, d.spans) },
  { id: 'saved', label: 'Kazanç', width: 100, minWidth: 80, numeric: true, priority: 1, sortValue: d => d.bytesSaved },
  { id: 'imp', label: 'Önemli (hata/ref/yavaş trace)', width: 210, minWidth: 140, numeric: true, priority: 3, sortValue: d => (d.byClass.error?.traces ?? 0) + (d.byClass.ref?.traces ?? 0) + (d.byClass.slow?.traces ?? 0) },
  { id: 'note', label: 'Ölçüm', width: 260, minWidth: 140, flex: true, priority: 3 },
];

function SettingsForm({ cur, onSaved }: { cur: TraceThinningSettings; onSaved: (s: TraceThinningStatus) => void }) {
  const [mode, setMode] = useState<'off' | 'shadow'>(cur.mode === 'shadow' ? 'shadow' : 'off');
  const [keep, setKeep] = useState(String(cur.keepPercent));
  const [full, setFull] = useState(String(cur.fullDays));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    setMode(cur.mode === 'shadow' ? 'shadow' : 'off');
    setKeep(String(cur.keepPercent));
    setFull(String(cur.fullDays));
  }, [cur.mode, cur.keepPercent, cur.fullDays]);
  const next: TraceThinningSettings = { mode, keepPercent: Number(keep), fullDays: Number(full) };
  const invalid = validateThinning(next);
  const dirty = next.mode !== cur.mode || next.keepPercent !== cur.keepPercent || next.fullDays !== cur.fullDays;
  const save = async () => {
    setBusy(true); setErr(null);
    try { onSaved(await api.traceThinningSave(next)); }
    catch (e) { setErr(apiErrorDetail(e).message); }
    finally { setBusy(false); }
  };
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'flex-end', gap: 12, marginBottom: 8 }}>
      <SegmentedControl<'off' | 'shadow'> aria-label="İnceltme modu" size="sm" value={mode} onChange={setMode}
        options={[
          { value: 'off', label: 'off', title: 'Ölçüm yok' },
          { value: 'shadow', label: 'shadow', title: 'Yalnız tahmin — hiçbir şey silinmez' },
        ]} />
      <Field small label="keepPercent" type="number" min={1} max={100} value={keep} onChange={e => setKeep(e.target.value)} style={{ width: 110 }} />
      <Field small label="fullDays" type="number" min={1} max={89} value={full} onChange={e => setFull(e.target.value)} style={{ width: 110 }} />
      <Button type="button" size="sm" disabled={!dirty || !!invalid || busy} onClick={() => void save()}>Kaydet</Button>
      {(invalid && dirty) && <span className="cell-err" style={{ fontSize: 12 }}>{invalid}</span>}
      {err && <span className="cell-err" style={{ fontSize: 12 }}>{err}</span>}
      <span style={{ fontSize: 12, color: 'var(--text3)' }}>mode=on ayrılmış (gerçek silme bu sürümde yok)</span>
    </div>
  );
}

export function TraceThinningPanel() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: QK, queryFn: () => api.traceThinningStatus(), staleTime: 60_000 });
  const d = q.data;
  const t = d?.totals;
  const shares = t ? classShares(t.byClass, t.keepTraces) : [];
  return (
    <div style={{ marginBottom: 24 }}>
      <SectionHead title="Trace inceltme · gölge tahmini" source="trace_summary_5m · system.parts"
        description={<>0–{d?.settings.fullDays ?? 7} gün her şey kalır; sonrası retention&apos;a kadar trace&apos;lerin deterministik %{d?.settings.keepPercent ?? 10}&apos;u + önemli trace&apos;ler.
          Yalnız ölçüm: hiçbir veri silinmez. Lider işçi 10 dk&apos;da en çok bir gün partition&apos;ı ölçer (günün her saatinden bir 5 dk kova, hash örneklemi).</>}
        meta={d?.lastRunAt ? <span style={{ fontSize: 12, color: 'var(--text3)' }}>son koşu {tsLong(d.lastRunAt)}</span> : undefined} />
      {d && <SettingsForm cur={d.settings} onSaved={s => qc.setQueryData(QK, s)} />}
      {d?.lastError && <div className="cell-err" style={{ fontSize: 12, marginBottom: 8 }}>Son hata: {d.lastError}</div>}
      {d && t && t.days > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <span className="badge b-info" title="Ölçülmüş günlerin toplam spans disk boyutu × (1 − kalan span payı)">
            tahmini kazanç {fmtBytes(t.bytesSaved)} / {fmtBytes(t.bytes)} ({pct(t.bytesSaved, t.bytes)}%)
          </span>
          <span className="badge b-gray">kalan span %{pct(t.keepSpans, t.spans)} · trace %{pct(t.keepTraces, t.traces)}</span>
          <span className="badge b-gray">{t.days}/{d.bandDays} gün ölçüldü{d.pending > 0 ? ` · ${d.pending} bekliyor` : ''}</span>
          {shares.map(s => (
            <span key={s.id} className="badge b-gray" title={s.hint}>{s.label} %{s.share} ({fmtNum(s.traces)})</span>
          ))}
        </div>
      )}
      <DataTable<TraceThinningDay> storageKey="admin-trace-thinning-days" columns={COLS} rows={d?.days ?? []} ariaLabel="Trace inceltme gün tahminleri"
        getRowKey={r => r.day}
        sort={{ initial: { id: 'day', dir: 'desc' } }}
        state={q.isLoading ? { kind: 'loading' }
          : q.isError ? { kind: 'error', onRetry: () => void q.refetch(), message: 'Gölge tahmini okunamadı' }
            : { kind: 'empty', message: d?.settings.mode === 'shadow'
              ? (d.bandDays === 0 ? 'Bant boş: spans retention ≤ fullDays' : 'Henüz ölçülmüş gün yok — işçi 10 dk içinde ilk günü ölçer.')
              : 'Mod off — shadow seçip kaydedince ölçüm başlar.' }}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'day': return r.day;
            case 'spans': return r.failed ? '—' : fmtNum(r.spans);
            case 'bytes': return r.failed ? '—' : fmtBytes(r.bytes);
            case 'traces': return r.failed ? '—' : fmtNum(r.traces);
            case 'keep': return r.failed ? '—' : `%${pct(r.keepSpans, r.spans)}`;
            case 'saved': return r.failed ? '—' : fmtBytes(r.bytesSaved);
            case 'imp': return r.failed ? '—' : `${fmtNum(r.byClass.error?.traces ?? 0)} / ${fmtNum(r.byClass.ref?.traces ?? 0)} / ${fmtNum(r.byClass.slow?.traces ?? 0)}`;
            case 'note': return r.failed
              ? <span className="cell-err" title={r.note}>başarısız: {r.note}</span>
              : <span style={{ color: 'var(--text3)' }} title={r.note || undefined}>
                {`örnek ${fmtNum(r.sampledTraces)} trace · 1/${r.sampleDen} · ${r.elapsedMs} ms · ref ${r.refsInDay}${r.refsTruncated ? ' (sınırda)' : ''}`}{r.note ? ` · ${r.note}` : ''}
              </span>;
          }
          return null;
        }} />
    </div>
  );
}
