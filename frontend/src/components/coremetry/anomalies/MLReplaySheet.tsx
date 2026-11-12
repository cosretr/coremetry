// MLReplayContent — /anomalies/ml/:id içeriği. v0.10.2022: GRUP sayfası
// (mockup anomaly-unified.html): başlıkta servis · operasyon + durum, tek
// cümle, TEK grafik (en güçlü üyenin olay anı kaydı + normal aralık bandı),
// kanıt satırları (sinyal + cümle + kaynak), eylemler, KAPALI "Teknik
// ayrıntı" (aşağıdaki eski bölümler, replay / what-if dahil, oraya taşındı).
// Önceki düzen — v0.10.1845 (öğrenme döngüsü
// Faz 2, mockup docs/frontend/mockups/anomaly-detail.html görünüm 2–4; audit
// §17 bölüm sırası): başlık çipleri → kalın tek satır özet → açıklama kartı
// (sunucu ml.BuildExplainCard; FE cümle KURMAZ) → olayın seyri (olay anı
// kaydının grafiği) → eylemler → kanıt (kayıttaki örnekler + bağlam
// referansları) → bağlam (aynı servisin ±2 sa kayıtları, yayınlar) → katlı
// "Teknik detay". Yalnız replay ucu okunur (ES/VM yok); kayıt yoksa kart
// yine çizilir, grafik yerine dürüst not.
import { useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { SearchX } from 'lucide-react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { TimeChart } from '@/components/charts/TimeChart';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Field } from '@/components/coremetry/Field';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SignalKindBadge } from '@/components/coremetry/SignalKindBadge';
import { Empty, Spinner } from '@/components/Spinner';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries/keys';
import { traceHref } from '@/lib/traceHref';
import { mlEventHref } from '@/lib/anomalyHref';
import { serviceHref } from '@/lib/serviceHref';
import { signalKindOf } from '@/lib/signalKind';
import { fmtDateTime } from '@/lib/utils';
import {
  dayClassLabel, exceptionGroupHref, parseObj,
  replayChart, replayContext, replayDeploys, replayMarkers, replaySamples, whatIfWindow,
} from '@/lib/mlReplay';
import type { TraceCourse, ExplainCard, ExplainLink, MLBacktestResult, MLReplayVerdict, MLSample, MLSnapshot, RecentDeployEntry } from '@/lib/types';
import { ML_DETECTOR_TR } from './MLTab';
import { MLEventPlayer } from './MLEventPlayer';
import { MLTraceHeatmap } from './MLTraceHeatmap';
import { MLTraceDots } from './MLTraceDots';
import { MLLogPlayer } from './MLLogPlayer';
import { MLSignalEvidence } from './MLSignalEvidence';
import { groupStatusText, type MLGroupRow } from './mlGroupRows';
import { evidenceSource, headerOperation } from './mlGroupDetail';
import { MLSourceChart } from './MLSourceChart';
import { contributorLine, leadFromSnapshot, parseLogsLink, type Contributors } from './mlSourcePanel';

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

/** SAF (test): sayfa bölümlerinin sırası. v0.10.2022 (mockup anomaly-unified.html): başlık → tek cümle → tek grafik → kanıt satırları → eylemler → KAPALI teknik ayrıntı (kart, bağlam, örnekler, replay / what-if). */
export const ML_EVENT_SECTIONS = ['ml-header', 'ml-summary', 'ml-course', 'ml-evidence', 'ml-actions', 'ml-technical'] as const;

function Chip({ link }: { link: ExplainLink }) {
  if (!link.href) return <Badge variant="outline">{link.label}</Badge>;
  return <Link to={link.href} className="inline-flex h-[22px] items-center rounded-full border border-[color:var(--accent-border)] bg-[color:var(--accent-bg)] px-2 text-[11px] font-medium whitespace-nowrap">{link.label} ↗</Link>;
}

function Section({ id, title, sub, children }: { id: string; title: string; sub?: string; children: ReactNode }) {
  return (
    <section className="mt-3" data-testid={id}>
      <h3 className="mb-1.5 flex flex-wrap items-center gap-2 text-xs font-semibold">{title}{sub && <span className="font-normal text-muted-foreground">{sub}</span>}</h3>
      {children}
    </section>
  );
}

export function MLReplayContent({ id, autoPlay = false, group = null }: { id: string; autoPlay?: boolean; group?: MLGroupRow | null }) {
  const q = useQuery({ queryKey: [...keys.anomalies.ml, 'replay', id], queryFn: () => api.mlReplay(id), enabled: !!id, staleTime: 30_000, retry: false });
  const snap = q.data?.snapshot ?? null;
  const card = q.data?.card;
  // v0.10.2033 — panel verisinden suçlu seri(ler) (ek sorgu yok).
  const [contrib, setContrib] = useState<Contributors>({ kind: 'none' });
  if (q.isLoading) return <Spinner />;
  if (q.error || !card) {
    return <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title="Bu olay bulunamadı">Olay kaydı 30, olay anı kaydı 120 gün saklanır; daha eski olaylar listeden de düşer.</Empty>;
  }
  const h = card.header;
  const tc = q.data?.course?.trace; // v0.10.1870 — endpoint'li trace olayı: ısı haritası (teknik ayrıntıda)
  const lc = q.data?.course?.log; // v0.10.1871 — log olayı: yığılı çubuk + gölge
  const ev = snap ? parseObj(snap.evidence) : {};
  const episodes = typeof ev.episodes7d === 'number' ? ev.episodes7d : 0;
  const lead = group?.lead ?? null;
  const op = h.subject?.label || headerOperation(lead);
  const status = group ? groupStatusText(group, Date.now() * 1e6) : h.status;
  const running = group ? group.status === 'running' || group.status === 'watching' : h.open;
  const members = group?.members ?? [];
  const sample = snap ? replaySamples(snap).find(x => x.traceId) : undefined;
  const shadow = members.map(m => m.groupShadow).filter(Boolean).sort((x, y) => (y!.at - x!.at))[0];
  const chartLead = lead ?? (snap ? leadFromSnapshot(snap) : null);
  const contribLine = contributorLine(contrib, 'istemci');
  const snapshotView = (
    <div className="rounded-md border border-border bg-card px-3 py-2.5">
      {snap && <p className="m-0 mb-1.5 text-xs text-muted-foreground">Kayıtlı görüntü · {Math.round((snap.bucketSec || 300) / 60)} dakikalık kova · normal aralık ince çizgilerle</p>}
      {snap && lc ? <MLLogPlayer snap={snap} play={card.play} autoPlay={autoPlay} lc={lc} />
        : snap ? <MLEventPlayer snap={snap} play={card.play} compact />
          : <p className="text-sm text-muted-foreground">Bu olay için grafik kaydı yok (olay anı kaydı yazılmadan önce açılmış ya da süresi dolmuş).</p>}
    </div>
  );
  return (
    <div data-testid="ml-replay" className="flex flex-col">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-sm" data-testid="ml-header">
        <Link to={h.serviceHref} className="font-mono">{h.service}</Link>
        {op && <span className="font-mono text-muted-foreground">· {op}</span>}
        <Badge variant="outline" className={running ? 'border-transparent bg-[color:var(--warn-bg)] text-[color:var(--warn)]' : ''}>{status}</Badge>
      </div>
      <p className="mb-2.5 text-sm font-semibold leading-normal" data-testid="ml-summary">{card.summary}</p>
      {snap?.detector === 'op_latency' && episodes >= 5 && (
        <p className="mb-2 text-sm text-muted-foreground" data-testid="ml-chronic">Kronik: son 7 günde {episodes} kez tekrarladı; tek tek olay yerine kalıcı bir desen olarak izleyin.</p>
      )}
      <Section id="ml-course" title="Olayın seyri">
        {/* v0.10.2033 — servis sayfasındaki panelin aynısı (mlSourcePanel); yoksa kayıtlı 5 dakikalık görüntü. */}
        {chartLead ? (
          <MLSourceChart lead={chartLead} open={running} onContributors={setContrib} fallback={snapshotView}
            logsLink={card.whereToLook.find(l => parseLogsLink(l.href))?.href} />
        ) : snapshotView}
      </Section>
      <Section id="ml-evidence" title="Kanıt" sub={members.length > 1 ? `${members.length} sinyal aynı olayda` : undefined}>
        <ul className="m-0 flex list-none flex-col gap-2 p-0" data-testid="ml-evidence-rows">
          {contribLine && (
            <li data-testid="ml-contributors" className="flex flex-wrap items-baseline gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm">
              <SignalKindBadge kind="metric" />
              <span className="min-w-0 flex-1 select-all [overflow-wrap:anywhere]">{contribLine}</span>
              <span className="text-xs text-muted-foreground">Messaging paneli · VictoriaMetrics</span>
            </li>
          )}
          {(members.length ? members : [null]).map(m => (
            <li key={m?.id ?? 'lead'} className="flex flex-wrap items-baseline gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm">
              <SignalKindBadge kind={m ? signalKindOf(m.detector, m.signal) : signalKindOf(h.detector, h.kind)} />
              <span className="min-w-0 flex-1">{m ? (m.summary || card.what) : card.what}</span>
              <span className="text-xs text-muted-foreground">{m ? evidenceSource(m.detector, m.signal, m.evidence) : evidenceSource(h.detector, h.kind, snap?.evidence)}</span>
            </li>
          ))}
        </ul>
      </Section>
      <Section id="ml-actions" title="Ne yapmak istersiniz?">
        <div className="mb-2 flex flex-wrap gap-2" data-testid="ml-where">
          {sample?.traceId && <Chip link={{ label: "Örnek trace'i aç", href: traceHref(sample.traceId) }} />}
          {card.whereToLook.map(l => <Chip key={l.label} link={l} />)}
        </div>
        <MLEventActions id={id} card={card} verdict={q.data?.verdict ?? null} />
      </Section>
      <details className="mt-3.5 rounded-md border border-border bg-card px-2.5 py-1.5 text-sm" data-testid="ml-technical">
        <summary className="cursor-pointer text-xs font-semibold">Teknik ayrıntı</summary>
        <div className="mt-2 flex flex-col gap-3">
          <KeyValue labelWidth="wide" items={[
            { k: 'Dedektörler', v: (members.length ? [...new Set(members.map(m => m.detector))] : [h.detector]).map(d => ML_DETECTOR_TR[d] ?? d).join(', ') },
            ...(group ? [{ k: 'Grup aralığı', v: `${fmtTs(group.start)} – ${group.status === 'running' || group.status === 'watching' ? 'sürüyor' : fmtTs(group.end)}` }] : []),
            { k: 'Problem kapısı (gölge)', v: shadow ? `${shadow.wouldOpen ? 'açardı' : 'açmazdı'}${shadow.priority ? ` · ${shadow.priority}` : ''} — ${shadow.reason}` : 'karar yok' },
          ]} />
          <div className="rounded-md border border-border bg-card" data-testid="ml-card">
            <KeyValue items={[
              ...cardRows(card),
              { k: 'Yakında ne değişti', v: card.whatChanged.length === 0
                ? <span className="text-muted-foreground">Bu pencerede eşlenen değişiklik yok.</span>
                : <ul className="m-0 pl-4" data-testid="ml-what-changed">{card.whatChanged.map(c => <li key={c.text}>{c.text}{c.link && <> <Chip link={c.link} /></>}</li>)}</ul> },
              { k: 'Nereye bakmalı', v: <span className="flex flex-wrap gap-2">{card.whereToLook.map(l => <Chip key={l.label} link={l} />)}</span> },
            ]} />
          </div>
          {/* v0.10.2032 — tam görünüm (seriler + alarm sınırları + oynatma) burada; özetteki grafik sade. */}
          {snap && !lc && (
            <div data-testid="ml-replay-full">
              <MLEventPlayer snap={snap} play={card.play} autoPlay={autoPlay} />
            </div>
          )}
          {tc && <MLTraceCourse tc={tc} cursorSec={null} />}
          <MLSignalEvidence id={id} signal={h.kind} />
          {snap ? <MLEvidence snap={snap} /> : <p className="text-sm text-muted-foreground">Olay anı kaydı yok; örnek satır gösterilemiyor.</p>}
          <div data-testid="ml-context"><MLContext snap={snap} related={q.data?.related ?? []} /></div>
          <MLTechnical snap={snap} card={card} />
        </div>
      </details>
    </div>
  );
}

// v0.10.1872 — az çağrılan serviste ısı haritasının YERİNE nokta grafiği (§17: ikisi birlikte gösterilmez).
function MLTraceCourse({ tc, cursorSec }: { tc: TraceCourse; cursorSec: number | null }) {
  return tc.view === 'dots' ? <MLTraceDots tc={tc} cursorSec={cursorSec} /> : <MLTraceHeatmap tc={tc} cursorSec={cursorSec} />;
}

function MLEventActions({ id, card, verdict }: { id: string; card: ExplainCard; verdict: MLReplayVerdict | null }) {
  const qc = useQueryClient();
  const role = useAuthRole();
  const canWrite = role === 'admin' || role === 'editor';
  const [busy, setBusy] = useState(false);
  const put = async (v: 'anomaly' | 'not_anomaly') => {
    setBusy(true);
    try {
      await api.putMlVerdict(id, { verdict: v, detector: card.header.detector, service: card.header.service });
      await qc.invalidateQueries({ queryKey: keys.anomalies.ml });
    } finally {
      setBusy(false);
    }
  };
  const why = canWrite ? undefined : 'Etiketlemek için editör yetkisi gerekir';
  return (
    <div className="rounded-md border border-border bg-card px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" disabled={busy || !canWrite} title={why} onClick={() => { void put('anomaly'); }}>Gerçek anomali</Button>
        <Button size="sm" variant="outline" disabled={busy || !canWrite} title={why} onClick={() => { void put('not_anomaly'); }}>Sorun değil</Button>
        {verdict && <Badge variant="outline" data-testid="ml-verdict">{verdict.verdict === 'anomaly' ? 'gerçek ✓' : 'değil ✗'}{verdict.by ? ` · ${verdict.by}` : ''}</Badge>}
        <span className="text-xs text-muted-foreground">Etiketiniz bu dedektörün isabet ölçüsüne (ML karnesi) girer.</span>
      </div>
    </div>
  );
}

const SAMPLE_COLS: ColumnDef<MLSample>[] = [
  { id: 't', label: 'Saat', width: 150, sortValue: s => s.t },
  { id: 'trace', label: 'Trace', width: 160, sortValue: s => s.traceId ?? '' },
  { id: 'body', label: 'Satır', flex: true, minWidth: 200, sortValue: s => s.body ?? '' },
];

function MLEvidence({ snap }: { snap: MLSnapshot }) {
  const samples = replaySamples(snap);
  const ctx = replayContext(snap.evidence);
  const hasCtx = ctx.exceptions.length > 0 || ctx.oracle.length > 0 || !!ctx.bubbleUp || ctx.bubbleUnreadable;
  return (
    <div className="flex flex-col gap-2">
      <div className="rounded-md border border-border bg-card">
        <DataTable<MLSample> storageKey="ml-event-samples" columns={SAMPLE_COLS} rows={samples} ariaLabel="Örnek trace ve log satırları"
          state={{ kind: 'empty', message: 'Bu olay için örnek kaydı yok.' }} sort={{ initial: { id: 't', dir: 'desc' } }}
          getRowKey={(s, i) => `${s.traceId ?? ''}:${i}`}
          renderCell={(s, c) => {
            switch (c.id) {
              case 't': return <span className="font-mono">{fmtTs(s.t)}</span>;
              case 'trace': return s.traceId ? <Link to={traceHref(s.traceId)} className="font-mono">{s.traceId.slice(0, 16)} ↗</Link> : '—';
              case 'body': return s.body ? <span title={s.body}>{s.body}</span> : '—';
              default: return null;
            }
          }} />
      </div>
      {hasCtx && (
        <ul className="m-0 pl-4 text-sm" data-testid="ml-replay-context">
          {ctx.exceptions.map(x => <li key={x.fingerprint}>Exception: <Link to={exceptionGroupHref(x.fingerprint)}>{x.type}</Link> · {x.count}</li>)}
          {ctx.oracle.map(o => <li key={`${o.traceId}:${o.code}`}>Oracle: <span className="font-mono">{o.code}</span>{o.operation ? ` · ${o.operation}` : ''} · {o.sourceId} · <Link to={traceHref(o.traceId)}>{o.traceId.slice(0, 16)}</Link></li>)}
          {(ctx.bubbleUp ?? []).map(b => <li key={`${b.key}=${b.value}`}>Yoğunlaşma: <span className="font-mono">{b.key}={b.value}</span> · %{Math.round(b.selPct)} (taban %{Math.round(b.basePct)})</li>)}
          {ctx.bubbleUnreadable && <li className="text-muted-foreground">Yoğunlaşma (BubbleUp) okunamadı.</li>}
        </ul>
      )}
    </div>
  );
}

interface ContextRow { key: string; rel: string; kind: 'signal' | 'deploy'; snap?: MLSnapshot; deploy?: RecentDeployEntry; t: number }

/** SAF (test): bağlam satırları — aynı servisin ±2 sa kayıtları + kayıttaki yayınlar, zaman sıralı. */
export function contextRows(snap: MLSnapshot | null, related: MLSnapshot[]): ContextRow[] {
  const rows: ContextRow[] = related.map(r => ({ key: `s:${r.eventId}`, rel: 'Aynı servis', kind: 'signal', snap: r, t: r.detectedAt }));
  if (snap) for (const d of replayDeploys(snap)) rows.push({ key: `d:${d.version}:${d.firstSeenNs}`, rel: 'Yayın', kind: 'deploy', deploy: d, t: d.firstSeenNs });
  return rows.sort((a, b) => a.t - b.t);
}

const CONTEXT_COLS: ColumnDef<ContextRow>[] = [
  { id: 'rel', label: 'İlişki', width: 120, sortValue: r => r.rel },
  { id: 'what', label: 'Ne', flex: true, minWidth: 240, sortValue: r => r.snap?.detector ?? r.deploy?.version ?? '' },
  { id: 'when', label: 'Ne zaman', width: 150, sortValue: r => r.t },
  { id: 'go', label: 'Git', width: 110, sortValue: r => r.kind },
];

function MLContext({ snap, related }: { snap: MLSnapshot | null; related: MLSnapshot[] }) {
  return (
    <div className="rounded-md border border-border bg-card">
      <DataTable<ContextRow> storageKey="ml-event-context" columns={CONTEXT_COLS} rows={contextRows(snap, related)} ariaLabel="Olayın bağlamı"
        state={{ kind: 'empty', message: 'Aynı serviste ±2 saatte başka sinyal ya da yayın yok.' }} sort={{ initial: { id: 'when', dir: 'asc' } }}
        getRowKey={r => r.key}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'rel': return r.rel;
            case 'what': return r.snap
              ? <span className="inline-flex flex-wrap items-center gap-1.5"><SignalKindBadge kind={signalKindOf(r.snap.detector, r.snap.signal)} />{ML_DETECTOR_TR[r.snap.detector] ?? r.snap.detector}{r.snap.part && <span className="font-mono text-muted-foreground">{r.snap.part}</span>}</span>
              : <span className="font-mono">{r.deploy?.service} {r.deploy?.version}</span>;
            case 'when': return <span className="font-mono">{fmtTs(r.t)}</span>;
            case 'go': return r.snap ? <Link to={mlEventHref(r.snap.eventId)}>Anomali ↗</Link>
              : r.deploy ? <Link to={serviceHref(r.deploy.service, { tab: 'gitops' })}>Rollouts ↗</Link> : null;
            default: return null;
          }
        }} />
    </div>
  );
}

function MLTechnical({ snap, card }: { snap: MLSnapshot | null; card: ExplainCard }) {
  const t = card.technical;
  const thr = snap ? parseObj(snap.thresholds) : {};
  return (
    <div className="mt-2 flex flex-col gap-3">
      {snap && !snap.complete && <Badge variant="outline">ön kayıt · epizot kapanınca tamamlanır</Badge>}
      <KeyValue labelWidth="wide" items={[
        { k: 'Dedektör', v: t.detector },
        { k: 'Sapma skoru', v: t.score, mono: true },
        { k: 'İmza', v: t.signature, mono: true },
        { k: 'Eşikler', v: t.thresholds },
        { k: 'Beklenen aralık', v: t.expected },
        { k: 'Ayar revizyonu', v: t.revision, mono: true },
        ...(snap ? [
          { k: 'Takvim sınıfı', v: dayClassLabel(snap.dayClass) },
          { k: 'Taban medyan / MAD', v: `${fmtNum(snap.baselineMed)} / ${fmtNum(snap.baselineMad)}`, mono: true },
          { k: 'Eşik z (kritik)', v: fmtNum(Number(thr.criticalZ ?? 0)), mono: true },
          { k: 'Teyit eden', v: (snap.confirmIds ?? []).join(', '), mono: true },
        ] : []),
      ]} />
      {snap && <WhatIf snap={snap} />}
    </div>
  );
}
