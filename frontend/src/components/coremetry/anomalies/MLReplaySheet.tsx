// MLReplaySheet — ML sekmesi satır → "Replay" (v0.10.1753). Yalnız kayıt
// (ml_event_snapshots) okunur: seri + taban bandı, tetiklenen kovalar,
// rollout işaretleri, takvim sınıfı, eşikler, teyit eden olaylar, servisin
// diğer sinyalleri, ham örnek (trace/log pivotu). What-if: aynı pencerede
// farklı eşikle backtest, yan yana; "Bu ayarı uygula" (admin) Settings
// blob'una yazar (PUT audit'li). Seçim URL'de (?replay=<id>, replace).
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/coremetry/overlay/Sheet';
import { TimeChart } from '@/components/charts/TimeChart';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Field } from '@/components/coremetry/Field';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { Spinner } from '@/components/Spinner';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries/keys';
import { logsHref } from '@/lib/logsUrl';
import { traceHref } from '@/lib/traceHref';
import { fmtDateTime } from '@/lib/utils';
import { dayClassLabel, exceptionGroupHref, parseObj, replayChart, replayContext, replaySamples, whatIfWindow } from '@/lib/mlReplay';
import type { MLBacktestResult, MLSnapshot } from '@/lib/types';
import { ML_DETECTOR_TR } from './MLTab';

const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));
const fmtNum = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(3));

/** SAF (test): backtest sonucundan tek dedektör özeti. */
export function backtestLine(r: MLBacktestResult | undefined, detector: string): string {
  const d = r?.detectors.find(x => x.detector === detector);
  if (!d) return '0 isabet';
  const p = d.tp + d.fp > 0 ? ` · isabet %${Math.round(d.precision * 100)}` : '';
  return `${d.fired} isabet (${d.tp} gerçek, ${d.fp} değil, ${d.unlabeled} etiketsiz)${p}`;
}

function WhatIf({ snap }: { snap: MLSnapshot }) {
  const role = useAuthRole();
  const thr = parseObj(snap.thresholds);
  const [z, setZ] = useState(String(thr.criticalZ ?? 4));
  const [dwell, setDwell] = useState(String(thr.dwellBuckets ?? 2));
  const [impact, setImpact] = useState(String(thr.impactMinRel ?? 0.2));
  const [res, setRes] = useState<{ cur: MLBacktestResult; alt: MLBacktestResult } | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');
  const win = whatIfWindow(snap.detectedAt);
  const overrides = { criticalZ: Number(z), dwellBuckets: Number(dwell), impactMinRel: Number(impact) };

  const run = async () => {
    setBusy(true); setMsg('');
    try {
      const base = { signal: snap.signal, service: snap.service, from: win.from, to: win.to };
      const [cur, alt] = await Promise.all([api.mlBacktest(base), api.mlBacktest({ ...base, overrides })]);
      setRes({ cur, alt });
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const apply = async () => {
    setBusy(true); setMsg('');
    try {
      const cfg = await api.getMl();
      await api.putMl({ ...cfg, criticalZ: overrides.criticalZ, dwellBuckets: overrides.dwellBuckets, quality: { ...cfg.quality, impactMinRel: overrides.impactMinRel } });
      setMsg('Uygulandı — Settings › Anomaly › ML; bir sonraki tikte geçerli (audit kaydı düştü).');
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="mt-4" data-testid="ml-whatif">
      <h3 className="mb-1 font-semibold">What-if</h3>
      <p className="mb-2 text-xs text-muted-foreground">Tespitten 24 sa önce → 2 sa sonra, kayıtlı seri üzerinde canlı kararla aynı kural. Geri bildirim uygulanmaz.</p>
      <div className="grid grid-cols-3 gap-2">
        <Field type="number" label="Kritik z" step={0.5} small value={z} onChange={e => setZ(e.target.value)} />
        <Field type="number" label="Dwell (kova)" small value={dwell} onChange={e => setDwell(e.target.value)} />
        <Field type="number" label="Etki (göreli)" step={0.05} small value={impact} onChange={e => setImpact(e.target.value)} />
      </div>
      <div className="mt-2 flex gap-2">
        <Button size="sm" variant="outline" onClick={() => { void run(); }} loading={busy}>Karşılaştır</Button>
        {role === 'admin' && res && <Button size="sm" onClick={() => { void apply(); }} disabled={busy}>Bu ayarı uygula</Button>}
      </div>
      {res && (
        <KeyValue className="mt-2" items={[
          { k: 'Bugünkü ayar', v: backtestLine(res.cur, snap.detector) },
          { k: 'What-if', v: backtestLine(res.alt, snap.detector) },
        ]} />
      )}
      {msg && <p className="mt-1 text-xs">{msg}</p>}
    </section>
  );
}

export function MLReplaySheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const q = useQuery({
    queryKey: [...keys.anomalies.ml, 'replay', id ?? ''],
    queryFn: () => api.mlReplay(id ?? ''),
    enabled: !!id, staleTime: 60_000, retry: false,
  });
  const snap = q.data?.snapshot;
  const ch = snap ? replayChart(snap) : null;
  const thr = snap ? parseObj(snap.thresholds) : {};
  const samples = snap ? replaySamples(snap) : [];
  const ctx = snap ? replayContext(snap.evidence) : null;
  const det = snap ? ML_DETECTOR_TR[snap.detector] ?? snap.detector : '';

  return (
    <Sheet open={!!id} onOpenChange={o => { if (!o) onClose(); }}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-[760px]">
        <SheetHeader>
          <SheetTitle>Replay{snap ? ` · ${det} · ${snap.service}` : ''}</SheetTitle>
          <SheetDescription>Tespit anının kaydı; Elasticsearch ya da rollup okunmaz.</SheetDescription>
        </SheetHeader>
        <div className="px-4 pb-6" data-testid="ml-replay">
          {q.isLoading && <Spinner />}
          {q.error && <p className="text-sm">Bu olay için replay kaydı yok (kayıt v0.10.1752 sonrası açılan olaylarda oluşur).</p>}
          {snap && ch && (
            <>
              {!snap.complete && <Badge variant="outline">ön kayıt · +2 sa kaydı henüz yok</Badge>}
              {ch.times.length > 0 ? (
                <TimeChart height={200} times={ch.times} deployMarkers={ch.deploys}
                  regions={ch.regions.map(r => ({ ...r, color: 'var(--warn)' }))}
                  series={[
                    { key: 'v', label: snap.part, data: ch.value, color: 'var(--accent)', type: 'line', width: 2 },
                    { key: 'hi', label: `taban + ${fmtNum(ch.z)}σ`, data: ch.upper, color: 'var(--text3)', type: 'line', width: 1 },
                    { key: 'lo', label: `taban − ${fmtNum(ch.z)}σ`, data: ch.lower, color: 'var(--text3)', type: 'line', width: 1 },
                  ]} />
              ) : <p className="text-sm text-muted-foreground">Seri boş (ml_series bu pencereyi taşımıyor).</p>}
              <KeyValue className="mt-3" items={[
                { k: 'Tespit', v: fmtTs(snap.detectedAt) },
                { k: 'Son görülme', v: fmtTs(snap.lastSeen) },
                { k: 'Takvim sınıfı', v: dayClassLabel(snap.dayClass) },
                { k: 'Taban medyan / MAD', v: `${fmtNum(snap.baselineMed)} / ${fmtNum(snap.baselineMad)}` },
                { k: 'Eşik z (etkin)', v: `${fmtNum(Number(thr.criticalZ ?? 0))} (${fmtNum(ch.z)})` },
                { k: 'Dwell · etki', v: `${String(thr.dwellBuckets ?? '—')} kova · %${Math.round(Number(thr.impactMinRel ?? 0) * 100)}` },
                { k: 'Config hash', v: snap.configHash || '—' },
                { k: 'Teyit eden', v: (snap.confirmIds ?? []).length ? (snap.confirmIds ?? []).join(', ') : '—' },
              ]} />
              <h3 className="mt-4 mb-1 font-semibold">Servisin diğer sinyalleri (±2 sa)</h3>
              {(q.data?.related ?? []).length === 0 ? <p className="text-sm text-muted-foreground">Yok.</p> : (
                <ul className="text-sm">
                  {(q.data?.related ?? []).map(r => (
                    <li key={r.eventId}>{r.signal} · {ML_DETECTOR_TR[r.detector] ?? r.detector} · <span className="font-mono">{r.part}</span> · {fmtTs(r.detectedAt)}</li>
                  ))}
                </ul>
              )}
              {ctx && (ctx.exceptions.length > 0 || ctx.oracle.length > 0 || ctx.bubbleUp || ctx.bubbleUnreadable) && (
                <section data-testid="ml-replay-context">
                  <h3 className="mt-4 mb-1 font-semibold">Bağlam (±5 dk, referans)</h3>
                  <ul className="text-sm">
                    {ctx.exceptions.map(x => (
                      <li key={x.fingerprint}>Exception: <Link to={exceptionGroupHref(x.fingerprint)}>{x.type}</Link> · {x.count}</li>
                    ))}
                    {ctx.oracle.map(o => (
                      <li key={`${o.traceId}:${o.code}`}>Oracle: <span className="font-mono">{o.code}</span>{o.operation ? ` · ${o.operation}` : ''} · {o.sourceId} · <Link to={traceHref(o.traceId)}>{o.traceId.slice(0, 16)}</Link></li>
                    ))}
                    {(ctx.bubbleUp ?? []).map(b => (
                      <li key={`${b.key}=${b.value}`}>BubbleUp: <span className="font-mono">{b.key}={b.value}</span> · %{Math.round(b.selPct)} (taban %{Math.round(b.basePct)})</li>
                    ))}
                    {ctx.bubbleUnreadable && <li className="text-muted-foreground">BubbleUp okunamadı.</li>}
                  </ul>
                </section>
              )}
              <h3 className="mt-4 mb-1 font-semibold">Ham örnek</h3>
              <div className="flex flex-wrap gap-3 text-sm">
                <Link to={logsHref({ window: { fromNs: snap.detectedAt - 15 * 60e9, toNs: snap.lastSeen }, service: snap.service })}>Logs ↗</Link>
                <Link to={`/traces?service=${encodeURIComponent(snap.service)}`}>Traces ↗</Link>
              </div>
              {samples.length === 0 ? <p className="text-sm text-muted-foreground">Örnek yok.</p> : (
                <ul className="mt-1 text-sm">
                  {samples.map((x, i) => (
                    <li key={`${x.traceId ?? ''}:${i}`} className="font-mono">
                      {fmtTs(x.t)} · {x.traceId ? <Link to={traceHref(x.traceId)}>{x.traceId.slice(0, 16)}</Link> : '—'}
                      {x.body && <span className="ml-2 font-sans text-muted-foreground">{x.body}</span>}
                    </li>
                  ))}
                </ul>
              )}
              <WhatIf snap={snap} />
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
