// MLReplayContent — /anomalies/ml/:id içeriği (C1–C3, v0.10.1770–1771).
// Minimalist anlatım: insan diliyle başlık, durum + süre, tek satır
// "normal → şimdi" özeti, TEK grafik (seri + mevsimsel beklenen bant +
// gölgeli anomali bölgesi + rollout işaretleri), ilk sapma / tespit / tepe /
// bitiş, "Ne değişti" ≤3 madde, eylemler tek satır + "Gerçek mi?", kronik
// uyarısı; güven/skor/eşik/taban/bağlam/what-if → katlanır "Teknik detay".
// Yalnız replay kaydı okunur (ES/rollup yok).
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { TimeChart } from '@/components/charts/TimeChart';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Field } from '@/components/coremetry/Field';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { Spinner } from '@/components/Spinner';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries/keys';
import { traceHref } from '@/lib/traceHref';
import { fmtDateTime } from '@/lib/utils';
import {
  dayClassLabel, exceptionGroupHref, parseObj,
  replayChart, replayContext, replayMarkers, replaySamples, whatIfWindow,
} from '@/lib/mlReplay';
import type { ExplainCard, MLBacktestResult, MLSnapshot } from '@/lib/types';
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
    <section className="mt-3" data-testid="ml-whatif">
      <h4 className="mb-1 font-semibold">What-if</h4>
      <div className="grid grid-cols-3 gap-2">
        <Field type="number" label="Kritik z" step={0.5} small value={z} onChange={e => setZ(e.target.value)} />
        <Field type="number" label="Dwell (kova)" small value={dwell} onChange={e => setDwell(e.target.value)} />
        <Field type="number" label="Etki (göreli)" step={0.05} small value={impact} onChange={e => setImpact(e.target.value)} />
      </div>
      <div className="mt-2 flex gap-2">
        <Button size="sm" variant="outline" onClick={() => { void run(); }} loading={busy}>Karşılaştır</Button>
        {role === 'admin' && res && <Button size="sm" onClick={() => { void apply(); }} disabled={busy}>Bu ayarı uygula</Button>}
      </div>
      {res && <KeyValue className="mt-2" items={[{ k: 'Bugünkü ayar', v: backtestLine(res.cur, snap.detector) }, { k: 'What-if', v: backtestLine(res.alt, snap.detector) }]} />}
      {msg && <p className="mt-1 text-xs">{msg}</p>}
    </section>
  );
}

/** SAF (test): kartın tanım listesi satırları — boş Etki satırı yazılmaz. */
export function cardRows(c: ExplainCard): { k: string; v: string }[] {
  const rows = [
    { k: 'Ne oldu', v: c.what }, { k: 'Ne kadar', v: c.howMuch }, { k: 'Ne zamandan beri', v: c.since },
    { k: 'Etki', v: c.impact ?? '' }, { k: 'Neden önemli', v: c.whyItMatters },
  ];
  return rows.filter(r => r.v);
}

export function MLReplayContent({ id }: { id: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: [...keys.anomalies.ml, 'replay', id], queryFn: () => api.mlReplay(id), enabled: !!id, staleTime: 30_000, retry: false });
  const [busy, setBusy] = useState(false);
  const snap = q.data?.snapshot ?? null;
  const card = q.data?.card;
  const verdict = async (v: 'anomaly' | 'not_anomaly') => {
    if (!card) return;
    setBusy(true);
    try {
      await api.putMlVerdict(id, { verdict: v, detector: card.header.detector, service: card.header.service });
      await qc.invalidateQueries({ queryKey: keys.anomalies.ml });
    } finally {
      setBusy(false);
    }
  };
  if (q.isLoading) return <Spinner />;
  if (q.error || !card) return <p className="text-sm" data-testid="ml-replay">Bu olay bulunamadı (olay kaydı 30, olay anı kaydı 120 gün saklanır).</p>;
  return (
    <div data-testid="ml-replay" className="flex flex-col gap-3">
      <h2 className="text-[length:var(--fs-lg)] font-semibold" data-testid="ml-summary">{card.summary}</h2>
      <p className="text-sm text-muted-foreground">{card.header.status}</p>
      <KeyValue items={cardRows(card)} />
      {snap ? <MLReplayChartBlock snap={snap} /> : <p className="text-sm text-muted-foreground">Bu olay için grafik kaydı yok.</p>}
      <section>
        <h3 className="mb-1 font-semibold">Yakında ne değişti</h3>
        {card.whatChanged.length === 0 ? <p className="text-sm text-muted-foreground">Bu pencerede eşlenen değişiklik yok.</p>
          : <ul className="text-sm" data-testid="ml-what-changed">{card.whatChanged.map(c => <li key={c.text}>{c.text}{c.link?.href && <> <Link to={c.link.href}>{c.link.label} ↗</Link></>}</li>)}</ul>}
      </section>
      <div className="flex flex-wrap items-center gap-3 text-sm" data-testid="ml-actions">
        {card.whereToLook.map(l => (l.href ? <Link key={l.label} to={l.href}>{l.label} ↗</Link> : null))}
        <span className="text-muted-foreground">· Gerçek mi?</span>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => { void verdict('anomaly'); }}>Evet</Button>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => { void verdict('not_anomaly'); }}>Hayır</Button>
      </div>
      {snap && <MLReplayTechnical snap={snap} related={q.data?.related ?? []} card={card} />}
    </div>
  );
}

function MLReplayChartBlock({ snap }: { snap: MLSnapshot }) {
  const ch = replayChart(snap);
  const markers = replayMarkers(snap);
  return (
    <>
      {ch.times.length > 0 ? (
        <TimeChart height={220} times={ch.times} deployMarkers={ch.deploys}
          regions={ch.regions.map(r => ({ ...r, color: r.label === 'anomali' ? 'var(--err)' : 'var(--warn)' }))}
          series={[
            { key: 'v', label: snap.part, data: ch.value, color: 'var(--accent)', type: 'line', width: 2 },
            { key: 'hi', label: 'beklenen üst', data: ch.upper, color: 'var(--text3)', type: 'line', width: 1 },
            { key: 'lo', label: 'beklenen alt', data: ch.lower, color: 'var(--text3)', type: 'line', width: 1 },
          ]} />
      ) : snap.part ? <p className="text-sm text-muted-foreground">Seri boş (ml_series bu pencereyi taşımıyor).</p> : null}
      <KeyValue items={[
        { k: 'İlk sapma', v: markers.first ? fmtDateTime(new Date(markers.first * 1000)) : '—' },
        { k: 'Tespit', v: fmtTs(snap.detectedAt) },
        { k: 'Tepe', v: markers.peak ? `${fmtDateTime(new Date(markers.peak.t * 1000))} (${fmtNum(markers.peak.v)})` : '—' },
        { k: 'Bitiş', v: snap.complete ? fmtTs(snap.lastSeen) : 'sürüyor' },
      ]} />
    </>
  );
}

function MLReplayTechnical({ snap, related, card }: { snap: MLSnapshot; related: MLSnapshot[]; card: ExplainCard }) {
  const ch = replayChart(snap);
  const thr = parseObj(snap.thresholds);
  const ev = parseObj(snap.evidence);
  const ctx = replayContext(snap.evidence);
  const samples = replaySamples(snap);
  const episodes = typeof ev.episodes7d === 'number' ? ev.episodes7d : 0;
  const chronic = snap.detector === 'op_latency' && episodes >= 5;
  return (
    <>
      {chronic && <p className="text-sm" data-testid="ml-chronic">Kronik: son 7 günde {episodes} kez tekrarladı; tek tek olay yerine kalıcı bir desen olarak izleyin.</p>}
      <details className="text-sm">
        <summary className="cursor-pointer font-semibold">Teknik detay</summary>
        <div className="mt-2 flex flex-col gap-3">
          {!snap.complete && <Badge variant="outline">ön kayıt · epizot kapanınca tamamlanır</Badge>}
          <KeyValue items={[
            { k: 'Dedektör', v: card.technical.detector },
            { k: 'Sapma skoru', v: card.technical.score },
            { k: 'İmza', v: card.technical.signature },
            { k: 'Seri', v: snap.part || '—' },
            { k: 'Takvim sınıfı', v: dayClassLabel(snap.dayClass) },
            { k: 'Taban medyan / MAD', v: `${fmtNum(snap.baselineMed)} / ${fmtNum(snap.baselineMad)}` },
            { k: 'Eşik z (etkin)', v: `${fmtNum(Number(thr.criticalZ ?? 0))} (${fmtNum(ch.z)})` },
            { k: 'Dwell · etki', v: `${String(thr.dwellBuckets ?? '—')} kova · %${Math.round(Number(thr.impactMinRel ?? 0) * 100)}` },
            { k: 'Config hash', v: snap.configHash || '—' },
            { k: 'Teyit eden', v: (snap.confirmIds ?? []).length ? (snap.confirmIds ?? []).join(', ') : '—' },
          ]} />
          <div>
            <h4 className="mb-1 font-semibold">Servisin diğer sinyalleri (±2 sa)</h4>
            {related.length === 0 ? <p className="text-muted-foreground">Yok.</p> : (
              <ul>{related.map(r => <li key={r.eventId}>{r.signal} · {ML_DETECTOR_TR[r.detector] ?? r.detector} · <span className="font-mono">{r.part}</span> · {fmtTs(r.detectedAt)}</li>)}</ul>
            )}
          </div>
          {(ctx.exceptions.length > 0 || ctx.oracle.length > 0 || ctx.bubbleUp || ctx.bubbleUnreadable) && (
            <div data-testid="ml-replay-context">
              <h4 className="mb-1 font-semibold">Bağlam (±5 dk, referans)</h4>
              <ul>
                {ctx.exceptions.map(x => <li key={x.fingerprint}>Exception: <Link to={exceptionGroupHref(x.fingerprint)}>{x.type}</Link> · {x.count}</li>)}
                {ctx.oracle.map(o => <li key={`${o.traceId}:${o.code}`}>Oracle: <span className="font-mono">{o.code}</span>{o.operation ? ` · ${o.operation}` : ''} · {o.sourceId} · <Link to={traceHref(o.traceId)}>{o.traceId.slice(0, 16)}</Link></li>)}
                {(ctx.bubbleUp ?? []).map(b => <li key={`${b.key}=${b.value}`}>BubbleUp: <span className="font-mono">{b.key}={b.value}</span> · %{Math.round(b.selPct)} (taban %{Math.round(b.basePct)})</li>)}
                {ctx.bubbleUnreadable && <li className="text-muted-foreground">BubbleUp okunamadı.</li>}
              </ul>
            </div>
          )}
          <div>
            <h4 className="mb-1 font-semibold">Ham örnek</h4>
            {samples.length === 0 ? <p className="text-muted-foreground">Örnek yok.</p> : (
              <ul>{samples.map((x, i) => (
                <li key={`${x.traceId ?? ''}:${i}`} className="font-mono">{fmtTs(x.t)} · {x.traceId ? <Link to={traceHref(x.traceId)}>{x.traceId.slice(0, 16)}</Link> : '—'}
                  {x.body && <span className="ml-2 font-sans text-muted-foreground">{x.body}</span>}</li>
              ))}</ul>
            )}
          </div>
          <WhatIf snap={snap} />
        </div>
      </details>
    </>
  );
}
