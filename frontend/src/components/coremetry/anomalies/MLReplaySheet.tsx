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
import { useMemo, useState, type ReactNode } from 'react';
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
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { MLEventPlayer } from './MLEventPlayer';
import { MLTraceHeatmap } from './MLTraceHeatmap';
import { MLTraceDots } from './MLTraceDots';
import { MLLogPlayer } from './MLLogPlayer';
import { MLSignalEvidence } from './MLSignalEvidence';
import { groupStatusText, type MLGroupRow } from './mlGroupRows';
import { evidenceSource, groupGateLine, groupProblemChip, headerOperation, latestGroupDecision, latestSvcShadow, svcGateLine } from './mlGroupDetail';
import { MLSourceChart } from './MLSourceChart';
import { MLRecordCard } from './MLRecordCard';
import { MLVerdictReasonForm } from './MLVerdictReasonForm';
import { isVerdictReason, type VerdictReason } from './mlVerdictReason';
import { recordUsable } from './mlRecord';
import { fmtPctLang, t as tl, tf, useLang, useT, type Lang } from '@/lib/i18n'; // v0.10.2291 — sayfa kromu arayüz dilinde (sunucu metni aynen)
import { contributorLine, leadFromSnapshot, panelPeakLine, parseLogsLink, type Contributors } from './mlSourcePanel';
import { evidenceRows, evidenceSub } from './mlEvidenceRows';
import { OrigLang } from '@/features/anomalies/OrigLang'; // v0.10.2302 — EN arayüzde sunucu cümlesi işaretli
import { stripServicePrefix } from './unifiedRows';

const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));
const fmtNum = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(3));

/** SAF (test): backtest sonucundan tek dedektör özeti. */
export function backtestLine(r: MLBacktestResult | undefined, detector: string, lang: Lang = 'tr'): string {
  const d = r?.detectors.find(x => x.detector === detector);
  if (!d) return tl('mlx.bt.zero', lang);
  const p = d.tp + d.fp > 0 ? tf('mlx.bt.prec', lang, { p: fmtPctLang(Math.round(d.precision * 100), lang) }) : '';
  return tf('mlx.bt.line', lang, { f: d.fired, tp: d.tp, fp: d.fp, u: d.unlabeled, p });
}

function WhatIf({ snap }: { snap: MLSnapshot }) {
  const role = useAuthRole();
  const lang = useLang();
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
      setMsg(tl('mlx.wi.applied', lang));
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
        <Field type="number" label={tl('mlx.wi.z', lang)} step={0.5} small value={z} onChange={e => setZ(e.target.value)} />
        <Field type="number" label={tl('mlx.wi.dwell', lang)} small value={dwell} onChange={e => setDwell(e.target.value)} />
        <Field type="number" label={tl('mlx.wi.impact', lang)} step={0.05} small value={impact} onChange={e => setImpact(e.target.value)} />
      </div>
      <div className="mt-2 flex gap-2">
        <Button size="sm" variant="outline" onClick={() => { void run(); }} loading={busy}>{tl('mlx.wi.compare', lang)}</Button>
        {role === 'admin' && res && <Button size="sm" onClick={() => { void apply(); }} disabled={busy}>{tl('mlx.wi.apply', lang)}</Button>}
      </div>
      {res && <KeyValue className="mt-2" items={[{ k: tl('mlx.wi.current', lang), v: backtestLine(res.cur, snap.detector, lang) }, { k: 'What-if', v: backtestLine(res.alt, snap.detector, lang) }]} />}
      {msg && <p className="mt-1 text-xs">{msg}</p>}
    </section>
  );
}

/** SAF (test): kartın tanım listesi satırları — boş Etki satırı yazılmaz. */
export function cardRows(c: ExplainCard, lang: Lang = 'tr'): { k: string; v: string }[] {
  const rows = [
    { k: tl('mlx.card.what', lang), v: c.what }, { k: tl('mlx.card.howMuch', lang), v: c.howMuch }, { k: tl('mlx.card.since', lang), v: c.since },
    { k: tl('mlx.card.impact', lang), v: c.impact ?? '' }, { k: tl('mlx.card.why', lang), v: c.whyItMatters },
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
  const t = useT();
  const lang = useLang();
  const q = useQuery({ queryKey: [...keys.anomalies.ml, 'replay', id], queryFn: () => api.mlReplay(id), enabled: !!id, staleTime: 30_000, retry: false });
  const snap = q.data?.snapshot ?? null;
  const card = q.data?.card;
  const rec = recordUsable(q.data?.record) ? q.data!.record! : null;
  // v0.10.2033 — panel verisinden suçlu seri(ler) (ek sorgu yok).
  const [contrib, setContrib] = useState<Contributors>({ kind: 'none' });
  // v0.10.2044 — panelin olay aralığındaki tepesi. v0.10.2329 (audit B8): başlığa
  // yazılmaz; ayrı, etiketli satır (başlık dedektörün kaydettiği değer).
  const [peak, setPeak] = useState<number | null>(null);
  if (q.isLoading) return <Spinner />;
  if (q.error || !card) {
    return <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title={tl('mlx.notFound', lang)}>{tl('mlx.notFound.body', lang)}</Empty>;
  }
  // v0.10.2248 (Dilim D) — geçerli olay kaydı: mockup "ÖNERİLEN" kartı (tek grafik
  // MLRecordChart, metin kayıttan); kayıtsız olay aşağıdaki eski kartı çizer.
  if (rec) {
    const recSample = snap ? replaySamples(snap).find(x => x.traceId) : undefined;
    return (
      <div data-testid="ml-replay" className="flex flex-col">
        <MLRecordCard rec={rec} groupMembers={group?.members} leadId={id} actions={<>
          <div className="mb-2 flex flex-wrap gap-2" data-testid="ml-where">
            {card.header.kind !== 'metric' && recSample?.traceId && <Chip link={{ label: t('mlRecord.card.sampleTrace'), href: traceHref(recSample.traceId) }} />}
            <Chip link={{ label: t('mlRecord.card.serviceLink'), href: serviceHref(rec.subject.service) }} />
          </div>
          <MLEventActions id={id} card={card} verdict={q.data?.verdict ?? null} localized />
        </>} />
      </div>
    );
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
  const shadow = latestGroupDecision(members);
  const svcShadow = latestSvcShadow(lead ? [...members, lead] : members); // v0.10.2089
  const problemChip = groupProblemChip(shadow, lang);
  const chartLead = lead ?? (snap ? leadFromSnapshot(snap) : null);
  const contribLine = contributorLine(contrib, tl('mlx.contrib.noun', lang), lang, tl('mlx.contrib.nouns', lang));
  // v0.10.2044 — servis başlıkta. v0.10.2329 — başlık sunucu cümlesi aynen (dedektörün
  // kaydettiği değer); canlı panel tepesi yalnız metrik olayında ayrı satır.
  const headline = stripServicePrefix(card.summary, h.service);
  const peakLine = h.kind === 'metric' ? panelPeakLine(peak, card.value?.unit, card.value?.direction, lang) : '';
  const rows = members.length
    ? evidenceRows(members, stripServicePrefix(card.summary, h.service), card.howMuch, h.service)
    : [{ key: 'lead', kind: signalKindOf(h.detector, h.kind), detector: h.detector, text: card.howMuch, ids: [] as string[] }];
  const snapshotView = (
    <div className="rounded-md border border-border bg-card px-3 py-2.5">
      {/* v0.10.2204 — canlı seri yoksa kayıtlı görüntü; boş durum yalnız ikisi de yokken (courseKind). */}
      {snap && <p className="m-0 mb-1.5 text-xs text-muted-foreground"><span data-testid="ml-course-kind">{t('mlCourse.snapshot')}</span> · {tf('mlx.snap.sub', lang, { n: Math.round((snap.bucketSec || 300) / 60) })}</p>}
      {snap && lc ? <MLLogPlayer snap={snap} play={card.play} autoPlay={autoPlay} lc={lc} />
        : snap ? <MLEventPlayer snap={snap} play={card.play} compact />
          : <p className="text-sm text-muted-foreground">{tl('mlx.snap.none', lang)}</p>}
    </div>
  );
  return (
    <div data-testid="ml-replay" className="flex flex-col">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-sm" data-testid="ml-header">
        <Link to={h.serviceHref} className="font-mono">{h.service}</Link>
        {op && <span className="font-mono text-muted-foreground">· {op}</span>}
        <Badge variant="outline" className={running ? 'border-transparent bg-[color:var(--warn-bg)] text-[color:var(--warn)]' : ''}>{status}</Badge>
        {/* v0.10.2053 (D4) — grubun kararı: canlıda "Problem açtı" + bağlantı. */}
        {problemChip && <span data-testid="ml-group-problem"><Chip link={problemChip} /></span>}
      </div>
      <p className="mb-2.5 text-sm font-semibold leading-normal" data-testid="ml-summary"><OrigLang>{headline}</OrigLang></p>
      {peakLine && <p className="mb-2.5 text-sm text-muted-foreground" data-testid="ml-panel-peak">{peakLine}</p>}
      {snap?.detector === 'op_latency' && episodes >= 5 && (
        <p className="mb-2 text-sm text-muted-foreground" data-testid="ml-chronic">{tf('mlx.chronic', lang, { n: episodes })}</p>
      )}
      <Section id="ml-course" title={tl('mlRecord.card.course', lang)}>
        {/* v0.10.2033 — servis sayfasındaki panelin aynısı (mlSourcePanel); yoksa kayıtlı 5 dakikalık görüntü. */}
        {chartLead ? (
          <MLSourceChart lead={chartLead} open={running} onContributors={setContrib} fallback={snapshotView}
            onPeak={setPeak} direction={card.value?.direction}
            logsLink={card.whereToLook.find(l => parseLogsLink(l.href))?.href} />
        ) : snapshotView}
      </Section>
      <Section id="ml-evidence" title={tl('mlRecord.card.evidence', lang)} sub={evidenceSub(members)}>
        <ul className="m-0 flex list-none flex-col gap-2 p-0" data-testid="ml-evidence-rows">
          {contribLine && (
            <li data-testid="ml-contributors" className="flex flex-wrap items-baseline gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm">
              <SignalKindBadge kind="metric" />
              <span className="min-w-0 flex-1 select-all [overflow-wrap:anywhere]">{contribLine}</span>
              <span className="text-xs text-muted-foreground">{tl('mlx.contrib.src', lang)}</span>
            </li>
          )}
          {rows.map(r => {
            const m = members.find(x => r.ids.includes(x.id));
            return (
              <li key={r.key} className="flex flex-wrap items-baseline gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm">
                <SignalKindBadge kind={r.kind} />
                <span className="min-w-0 flex-1">{r.text ? <OrigLang>{r.text}</OrigLang> : r.text}</span>
                {m?.groupShadow?.live && <span className="text-xs font-medium" data-testid="ml-row-problem">{tl('mlx.gate.opened', lang)}</span>}
                <span className="text-xs text-muted-foreground">{m ? evidenceSource(m.detector, m.signal, m.evidence, lang) : evidenceSource(h.detector, h.kind, snap?.evidence, lang)}</span>
              </li>
            );
          })}
        </ul>
      </Section>
      <Section id="ml-actions" title={tl('mlRecord.card.actions', lang)}>
        <div className="mb-2 flex flex-wrap gap-2" data-testid="ml-where">
          {/* v0.10.2044 — örnek trace metrik olayında yok (Kafka lag olayında alakasızdı). */}
          {h.kind !== 'metric' && sample?.traceId && <Chip link={{ label: tl('mlRecord.card.sampleTrace', lang), href: traceHref(sample.traceId) }} />}
          {card.whereToLook.map(l => <Chip key={l.label} link={l} />)}
        </div>
        <MLEventActions id={id} card={card} verdict={q.data?.verdict ?? null} />
      </Section>
      <details className="mt-3.5 rounded-md border border-border bg-card px-2.5 py-1.5 text-sm" data-testid="ml-technical">
        <summary className="cursor-pointer text-xs font-semibold">{tl('mlRecord.card.technical', lang)}</summary>
        <div className="mt-2 flex flex-col gap-3">
          <KeyValue labelWidth="wide" items={[
            { k: tl('mlx.tech.detectors', lang), v: (members.length ? [...new Set(members.map(m => m.detector))] : [h.detector]).map(d => mlDetectorLabel(d, lang)).join(', ') },
            ...(group ? [{ k: tl('mlx.tech.groupRange', lang), v: `${fmtTs(group.start)} – ${group.status === 'running' || group.status === 'watching' ? tl('pd.when.ongoing', lang) : fmtTs(group.end)}` }] : []),
            { k: tl('mlx.tech.gate', lang), v: groupGateLine(shadow, lang) },
            ...(svcShadow ? [{ k: tl('mlx.tech.svcGate', lang), v: svcGateLine(svcShadow, lang) }] : []),
          ]} />
          <div className="rounded-md border border-border bg-card" data-testid="ml-card">
            <KeyValue items={[
              ...cardRows(card, lang),
              { k: tl('mlx.card.changed', lang), v: card.whatChanged.length === 0
                ? <span className="text-muted-foreground">{tl('mlx.card.noChange', lang)}</span>
                : <ul className="m-0 pl-4" data-testid="ml-what-changed">{card.whatChanged.map(c => <li key={c.text}>{c.text}{c.link && <> <Chip link={c.link} /></>}</li>)}</ul> },
              { k: tl('mlx.card.where', lang), v: <span className="flex flex-wrap gap-2">{card.whereToLook.map(l => <Chip key={l.label} link={l} />)}</span> },
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
          {snap ? <MLEvidence snap={snap} /> : <p className="text-sm text-muted-foreground">{tl('mlx.noSnap', lang)}</p>}
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

// localized (v0.10.2248): kayıtlı kartta metinler i18n'den (EN/TR); eski kart Türkçe kalır.
// Oylar yalnız etiket/karne içindir; hiçbir çalışma zamanı kararını sürmez (K3).
function MLEventActions({ id, card, verdict, localized = false }: { id: string; card: ExplainCard; verdict: MLReplayVerdict | null; localized?: boolean }) {
  const tr = useT();
  const lang = useLang();
  // v0.10.2291 — eski kart Türkçe arayüzde bugünkü metni korur; İngilizcede katalog.
  const L = (key: string, fallback: string) => (localized || lang !== 'tr' ? tr(key) : fallback);
  const qc = useQueryClient();
  const role = useAuthRole();
  const canWrite = role === 'admin' || role === 'editor';
  const [busy, setBusy] = useState(false);
  // v0.10.2256 — yeni kartta "Sorun değil" neden sorar (mockup §5); eski kart tek tık kalır.
  const [asking, setAsking] = useState(false);
  const put = async (v: 'anomaly' | 'not_anomaly', reason?: VerdictReason, note?: string) => {
    setBusy(true);
    try {
      await api.putMlVerdict(id, { verdict: v, detector: card.header.detector, service: card.header.service, ...(reason ? { reason } : {}), ...(note ? { note } : {}) });
      setAsking(false);
      await qc.invalidateQueries({ queryKey: keys.anomalies.ml });
      await qc.invalidateQueries({ queryKey: keys.anomalies.unifiedAll }); // v0.10.2051 — birleşik liste ML kararını taşır
    } finally {
      setBusy(false);
    }
  };
  const why = canWrite ? undefined : L('mlRecord.vote.needEditor', 'Etiketlemek için editör yetkisi gerekir');
  const reasonText = localized && verdict?.verdict === 'not_anomaly' && isVerdictReason(verdict.reason) ? ` · ${tr(`mlRecord.reason.${verdict.reason}`)}` : '';
  return (
    <div className="rounded-md border border-border bg-card px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" disabled={busy || !canWrite} title={why} onClick={() => { setAsking(false); void put('anomaly'); }}>{L('mlRecord.vote.real', 'Gerçek anomali')}</Button>
        <Button size="sm" variant="outline" disabled={busy || !canWrite} title={why} aria-expanded={localized ? asking : undefined}
          onClick={() => { if (localized) setAsking(a => !a); else void put('not_anomaly'); }}>{L('mlRecord.vote.notIssue', 'Sorun değil')}</Button>
        {verdict && <Badge variant="outline" data-testid="ml-verdict" title={verdict.note || undefined}>{verdict.verdict === 'anomaly' ? L('mlRecord.vote.isReal', 'gerçek ✓') : L('mlRecord.vote.isNot', 'değil ✗')}{reasonText}{verdict.by ? ` · ${verdict.by}` : ''}</Badge>}
        <span className="text-xs text-muted-foreground">{L('mlRecord.vote.note', 'Etiketiniz bu dedektörün isabet ölçüsüne (ML karnesi) girer.')}</span>
      </div>
      {localized && asking && canWrite && (
        <MLVerdictReasonForm busy={busy} onCancel={() => setAsking(false)} onSave={(r, n) => { void put('not_anomaly', r, n); }} />
      )}
    </div>
  );
}

const sampleCols = (lang: Lang): ColumnDef<MLSample>[] => [
  { id: 't', label: tl('mlx.col.time', lang), width: 150, sortValue: s => s.t },
  { id: 'trace', label: 'Trace', width: 160, sortValue: s => s.traceId ?? '' },
  { id: 'body', label: tl('mlx.ev.lines', lang), flex: true, minWidth: 200, sortValue: s => s.body ?? '' },
];

function MLEvidence({ snap }: { snap: MLSnapshot }) {
  const lang = useLang();
  const cols = useMemo(() => sampleCols(lang), [lang]);
  const samples = replaySamples(snap);
  const ctx = replayContext(snap.evidence);
  const hasCtx = ctx.exceptions.length > 0 || ctx.oracle.length > 0 || !!ctx.bubbleUp || ctx.bubbleUnreadable;
  return (
    <div className="flex flex-col gap-2">
      <div className="rounded-md border border-border bg-card">
        <DataTable<MLSample> storageKey="ml-event-samples" columns={cols} rows={samples} ariaLabel={tl('mlx.samples.aria', lang)}
          state={{ kind: 'empty', message: tl('mlx.samples.empty', lang) }} sort={{ initial: { id: 't', dir: 'desc' } }}
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
          {(ctx.bubbleUp ?? []).map(b => <li key={`${b.key}=${b.value}`}>{tl('mlx.ctx.conc', lang)} <span className="font-mono">{b.key}={b.value}</span> · {fmtPctLang(Math.round(b.selPct), lang)} ({tl('mlx.ctx.base', lang)} {fmtPctLang(Math.round(b.basePct), lang)})</li>)}
          {ctx.bubbleUnreadable && <li className="text-muted-foreground">{tl('mlx.ctx.concErr', lang)}</li>}
        </ul>
      )}
    </div>
  );
}

interface ContextRow { key: string; rel: string; kind: 'signal' | 'deploy'; snap?: MLSnapshot; deploy?: RecentDeployEntry; t: number }

/** SAF (test): bağlam satırları — aynı servisin ±2 sa kayıtları + kayıttaki yayınlar, zaman sıralı. */
export function contextRows(snap: MLSnapshot | null, related: MLSnapshot[], lang: Lang = 'tr'): ContextRow[] {
  const rows: ContextRow[] = related.map(r => ({ key: `s:${r.eventId}`, rel: tl('mlx.ctx.sameSvc', lang), kind: 'signal', snap: r, t: r.detectedAt }));
  if (snap) for (const d of replayDeploys(snap)) rows.push({ key: `d:${d.version}:${d.firstSeenNs}`, rel: tl('mlx.ctx.release', lang), kind: 'deploy', deploy: d, t: d.firstSeenNs });
  return rows.sort((a, b) => a.t - b.t);
}

const contextCols = (lang: Lang): ColumnDef<ContextRow>[] => [
  { id: 'rel', label: tl('mlx.ctx.rel', lang), width: 120, sortValue: r => r.rel },
  { id: 'what', label: tl('mlx.ctx.what', lang), flex: true, minWidth: 240, sortValue: r => r.snap?.detector ?? r.deploy?.version ?? '' },
  { id: 'when', label: tl('mlx.ctx.when', lang), width: 150, sortValue: r => r.t },
  { id: 'go', label: tl('mlx.go', lang), width: 110, sortValue: r => r.kind },
];

function MLContext({ snap, related }: { snap: MLSnapshot | null; related: MLSnapshot[] }) {
  const lang = useLang();
  const cols = useMemo(() => contextCols(lang), [lang]);
  return (
    <div className="rounded-md border border-border bg-card">
      <DataTable<ContextRow> storageKey="ml-event-context" columns={cols} rows={contextRows(snap, related, lang)} ariaLabel={tl('mlx.ctx.aria', lang)}
        state={{ kind: 'empty', message: tl('mlx.ctx.empty', lang) }} sort={{ initial: { id: 'when', dir: 'asc' } }}
        getRowKey={r => r.key}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'rel': return r.rel;
            case 'what': return r.snap
              ? <span className="inline-flex flex-wrap items-center gap-1.5"><SignalKindBadge kind={signalKindOf(r.snap.detector, r.snap.signal)} />{mlDetectorLabel(r.snap.detector, lang)}{r.snap.part && <span className="font-mono text-muted-foreground">{r.snap.part}</span>}</span>
              : <span className="font-mono">{r.deploy?.service} {r.deploy?.version}</span>;
            case 'when': return <span className="font-mono">{fmtTs(r.t)}</span>;
            case 'go': return r.snap ? <Link to={mlEventHref(r.snap.eventId)}>{tl('pd.meta.anomaly', lang)} ↗</Link>
              : r.deploy ? <Link to={serviceHref(r.deploy.service, { tab: 'gitops' })}>Rollouts ↗</Link> : null;
            default: return null;
          }
        }} />
    </div>
  );
}

function MLTechnical({ snap, card }: { snap: MLSnapshot | null; card: ExplainCard }) {
  const t = card.technical;
  const lang = useLang();
  const k = (key: string) => tl(`mlx.tech.${key}`, lang);
  const thr = snap ? parseObj(snap.thresholds) : {};
  return (
    <div className="mt-2 flex flex-col gap-3">
      {snap && !snap.complete && <Badge variant="outline">{k('prelim')}</Badge>}
      <KeyValue labelWidth="wide" items={[
        { k: k('detector'), v: t.detector },
        { k: k('score'), v: t.score, mono: true },
        { k: k('signature'), v: t.signature, mono: true },
        { k: k('thresholds'), v: t.thresholds },
        { k: k('expected'), v: t.expected },
        { k: k('revision'), v: t.revision, mono: true },
        // v0.10.2044 — cümle panel tepesini söyleyebilir; dedektörün değeri burada.
        ...(t.measured ? [{ k: k('measured'), v: t.measured, mono: true }] : []),
        ...(snap ? [
          { k: k('dayClass'), v: dayClassLabel(snap.dayClass, lang) },
          { k: k('baseline'), v: `${fmtNum(snap.baselineMed)} / ${fmtNum(snap.baselineMad)}`, mono: true },
          { k: k('critZ'), v: fmtNum(Number(thr.criticalZ ?? 0)), mono: true },
          { k: k('confirmedBy'), v: (snap.confirmIds ?? []).join(', '), mono: true },
        ] : []),
      ]} />
      {snap && <WhatIf snap={snap} />}
    </div>
  );
}
