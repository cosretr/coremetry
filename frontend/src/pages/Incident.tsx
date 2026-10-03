import { Suspense, useEffect, useMemo, useState, type ReactNode } from 'react';
import { useLocation, useSearchParams } from 'react-router-dom';
import { Link } from 'react-router-dom';
import { navHref } from '@/lib/navHref';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, AlertTriangle, Bell, Check, MessageSquare, Zap, PenLine } from 'lucide-react';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { useAuth } from '@/components/AuthProvider';
import { AIExplainButton } from '@/components/ai/AIExplainButton';
import { OverviewChart } from '@/pages/service/charts/OverviewChart';
import {
  useIncident, useIncidentEvents, useIncidentProblems, useIncidentProblemDetails,
  INCIDENT_PROBLEM_DETAIL_CAP, useServiceDeploys, keys,
} from '@/lib/queries';
// v0.10.1081 — sayfanın üstü problem / anomali detayları gibi okunur: tek
// cümle + "ne zaman", bağlı problemler, "Ne yapabilirim". Yapı taşları
// detailSections'tan (hafif modül; ProblemDetail'in ağır zinciri DEĞİL),
// kurallar SAF incidentSummary'de.
import { DetailSummary, Sect, SignalLink } from '@/features/anomalies/detailSections';
import { detailWhenLine } from '@/features/anomalies/detailSummary';
import {
  attachedProblemLabel, incidentHeadline, incidentPivotLinks, incidentPrimaryProblem, problemSentence,
} from '@/features/anomalies/incidentSummary';
import { readIncidentBackTo } from '@/lib/inboxHref';
import { subjectKind } from '@/lib/problemSubject';
import { api } from '@/lib/api';
import { PriorityBadge } from '@/components/ui/PriorityBadge'; // v0.10.796
import { metricQuery } from '@/lib/metricQuery';
import { tsLong } from '@/lib/utils';
import type { Incident } from '@/lib/types';
import { serviceHref, eventLifespanWindow } from '@/lib/serviceHref';
import { incidentRootCauseLabel } from '@/lib/incidentRootCause';
import { Button } from '@/components/ui/Button';
import { PageShell } from '@/components/ui/PageShell';
import { useConfirm } from '@/components/ui/ConfirmDialog';
import { AIFeedbackButtons } from '@/components/ai/AIFeedbackButtons';
import { IconSparkles } from '@/components/icons';
import { aiErrorHint } from '@/lib/aiErrors';
import { TriageStatusBadge } from '@/features/anomalies/statusTone'; // v0.10.929 (K5) — tek durum sözlüğü (hafif yaprak)

export default function IncidentPage() {
  return <Suspense fallback={<Spinner />}><Inner /></Suspense>;
}

// Hangi yazma yolunun uçtuğunu tutan etiket. Dördü de admin yazması,
// yani her biri backend'de bir audit girdisi bırakıyor — çift tık,
// çift audit demekti (v0.9.882).
type IncidentAction = 'ack' | 'resolve' | 'note' | 'pm';

function Inner() {
  const [sp] = useSearchParams();
  const location = useLocation();
  const { user } = useAuth();
  const id = sp.get('id') ?? '';
  const isAdmin = user?.role === 'admin' || user?.role === 'editor';
  const qc = useQueryClient();
  // v0.10.1081 — Problems kuyruğundan gelindiyse geri bağlantı oraya (aynı
  // süzgeçlerle) döner; başka her girişte bugünkü gibi Incidents listesi.
  const backTo = readIncidentBackTo(location.state);

  // Three parallel queries — incident detail, timeline, attached problems.
  const incQ = useIncident(id);
  const timelineQ = useIncidentEvents(id);
  const problemsQ = useIncidentProblems(id);
  const inc = incQ.isLoading ? undefined : incQ.isError ? null : incQ.data;
  const timeline = timelineQ.data ?? [];
  const problems = problemsQ.data ?? [];
  // v0.10.1081 — bağlı problemlerin kendi kayıtları (manşet, liste, pivotlar).
  const details = useIncidentProblemDetails(problems);

  const [note, setNote] = useState('');
  const [postmortemDraft, setPostmortemDraft] = useState('');
  const [editingPM, setEditingPM] = useState(false);
  const [busy, setBusy] = useState<IncidentAction | null>(null);
  // v0.9.1197 (Faz 5.4) — AI taslağı + KB'ye ekleme durumları. İkisi de
  // `busy` guard'ından ayrı: taslak üretimi incident yazmaz (audit yok),
  // KB ekleme kendi ucunda audit'lenir.
  const [aiPM, setAiPM] = useState<{ busy: boolean; err: string | null; xid?: string }>({ busy: false, err: null });
  const [kbPM, setKbPM] = useState<{ busy: boolean; done: string | null; err: string | null }>({ busy: false, done: null, err: null });
  const confirm = useConfirm();

  useEffect(() => {
    if (inc && !editingPM) setPostmortemDraft(inc.postmortem ?? '');
  }, [inc, editingPM]);

  // Impact window: incident start → resolved (or "now" captured ONCE so an
  // ongoing incident's window doesn't tick every render → infinite refetch,
  // the timeRangeToNs(range)-in-JSX pitfall). Hooks run unconditionally (before
  // the early returns) per the rules of hooks; they no-op until the service is
  // known via `enabled`.
  // v0.10.1081 — etki grafiği span metriğini SERVİS adıyla sorar; özne bir
  // veritabanı / dış kaynaksa (db-health incident'ı) sorgu hep boş döner ve
  // "No data" kartı olayı yok gösterirdi — o öznede kart çizilmez (pivotlar
  // "Ne yapabilirim"de).
  const rawSvc = incQ.data?.service ?? '';
  const svc = rawSvc && subjectKind(rawSvc) === 'service' ? rawSvc : '';
  const win = useMemo(() => {
    const from = incQ.data?.startedAt ?? 0;
    const to = incQ.data?.resolvedAt ?? Date.now() * 1_000_000;
    return { from, to };
  }, [incQ.data?.startedAt, incQ.data?.resolvedAt, incQ.data?.id]);
  // Madde 4 sweep — impact grafiğinde drag-zoom: incident penceresini yerel
  // olarak daraltır (bu sayfada global range yok; pencere incident'tan
  // türetilir). Zoom effWin'i değiştirir → impact + deploy sorguları dar
  // pencereyle yeniden çözülür; çift-tık tam incident penceresine döner;
  // incident değişince pencere sıfırlanır.
  const [zoomWin, setZoomWin] = useState<{ from: number; to: number } | null>(null);
  useEffect(() => { setZoomWin(null); }, [id]);
  const effWin = zoomWin ?? win;
  const impactMq = useMemo(() => metricQuery({
    source: 'spanmetrics', metric: 'calls_total', agg: 'error_rate', unit: '%',
    filters: { 'service.name': svc },
  }), [svc]);
  const impactQ = useQuery({
    queryKey: ['incident-impact', svc, effWin.from, effWin.to],
    queryFn: () => api.resolveMetric(impactMq, { from: effWin.from, to: effWin.to }),
    enabled: !!svc && effWin.from > 0,
    staleTime: 30_000,
  });
  const deploysQ = useServiceDeploys(svc, effWin.from, effWin.to);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: keys.incidents.one(id) });
    qc.invalidateQueries({ queryKey: keys.incidents.events(id) });
    qc.invalidateQueries({ queryKey: keys.incidents.problems(id) });
  };

  if (!id)               return <Empty icon="⚠" title="No incident selected" />;
  if (inc === undefined) return <Spinner />;
  if (inc === null)      return <Empty icon="⚠" title="Incident not found" />;

  // v0.9.882 (Dalga 2, W2.2) — dördü de korumasız yazma yoluydu: ikinci
  // tık ikinci POST/PUT ve ikinci audit girdisi. `busy` hangi aksiyonun
  // uçtuğunu tutuyor, `run` sarmalayıcısı da guard + finally'yi tek yerde
  // topluyor (dört ayrı try/finally kopyası kayma üretirdi).
  const run = async (kind: IncidentAction, fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(kind);
    try { await fn(); } finally { setBusy(null); }
  };
  const ack     = () => run('ack',     async () => { await api.ackIncident(id); refresh(); });
  const resolve = () => run('resolve', async () => { await api.resolveIncident(id); refresh(); });
  const submitNote = () => {
    if (!note.trim()) return;
    return run('note', async () => {
      await api.addIncidentNote(id, note.trim()); setNote(''); refresh();
    });
  };
  const savePM = () => run('pm', async () => {
    await api.updateIncident(id, { ...inc, postmortem: postmortemDraft });
    setEditingPM(false); refresh();
  });

  // AI taslağı: kanıt paketini sunucu kurar, taslak textarea'ya düşer —
  // operatör düzenler, kaydeden yine savePM. Dolu taslağın üzerine
  // yazmadan önce onay (ConfirmDialog, K6).
  const draftPM = async () => {
    if (aiPM.busy) return;
    if (postmortemDraft.trim()) {
      const ok = await confirm({
        title: 'AI taslağı üzerine yazılsın mı?',
        body: 'Editördeki mevcut metin AI taslağıyla değiştirilecek.',
        confirmLabel: 'Üzerine yaz',
      });
      if (!ok) return;
    }
    setAiPM({ busy: true, err: null });
    try {
      const rsp = await api.draftPostmortem(id);
      setPostmortemDraft(rsp.draft);
      setAiPM({ busy: false, err: null, xid: rsp.exchangeId });
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setAiPM({ busy: false, err: aiErrorHint(msg) ?? msg });
    }
  };

  // Kayıtlı postmortem'i KB'ye indeksle (Faz 5.4 ikinci yarı). İçeriği
  // sunucu incidents satırından okur; yeniden basmak kopya üretmez
  // (docID incident'a çakılı).
  const addPMToKB = async () => {
    if (kbPM.busy) return;
    setKbPM({ busy: true, done: null, err: null });
    try {
      const rsp = await api.ragIngestPostmortem(id);
      setKbPM({ busy: false, done: `KB'ye eklendi (${rsp.chunks} parça)`, err: null });
    } catch (e) {
      setKbPM({ busy: false, done: null, err: e instanceof Error ? e.message : String(e) });
    }
  };

  const elapsedNs = (inc.resolvedAt ?? Date.now() * 1_000_000) - inc.startedAt;
  const cause = incidentRootCauseLabel(inc.rootCause);
  // v0.10.1081 (operatör: "ekteki hata mesela hiç anlaşılmıyor") — manşet
  // birincil problemin cümlesi; pivotlar tek bağlı problemde ONDAN, çoklu
  // problemde incident'ın servisi + penceresinden (incidentSummary, saf).
  const headline = incidentHeadline(inc, details.problems);
  const primary = incidentPrimaryProblem(details.problems);
  const pivots = incidentPivotLinks(inc, problems.length === 1 ? details.problems : []);
  const when = detailWhenLine({
    startedAt: inc.startedAt, durationNs: elapsedNs,
    ongoing: inc.status !== 'resolved', endedAt: inc.resolvedAt,
  });
  const detailById = new Map(details.problems.map(p => [p.id, p]));

  // Impact chart series (error-rate over the incident window) → OverviewChart
  // shape: times in unix seconds, one red area series. Deploy marker = the
  // latest deploy inside the window (the design's ▼ vX flag).
  const impactSeries = impactQ.data?.series?.[0]?.points ?? [];
  const impactTimes = impactSeries.map(p => p.time / 1e9);
  const impactData = impactSeries.map(p => p.value);
  const deploy = (() => {
    const ds = (deploysQ.data ?? []).filter(d => d.timeUnixNs >= effWin.from && d.timeUnixNs <= effWin.to);
    if (!ds.length) return null;
    const latest = ds.reduce((a, b) => (b.timeUnixNs > a.timeUnixNs ? b : a));
    return { sec: latest.timeUnixNs / 1e9, label: latest.version };
  })();

  return (
    <>
      <Topbar title={`Incident · ${inc.title}`} />
      <PageShell>
        {/* Detail bar — back · status · severity · (spacer) · actions */}
        <div className="rb-bar">
          {/* v0.9.1320 — geri linki pencereyi + env'i taşır (navHref).
              v0.10.928 — satır-içi border kalktı; çerçeve + hover a.sec'ten. */}
          <Link to={backTo ?? navHref('/incidents', sp.toString())} className="sec" style={{
            padding: '5px 12px', borderRadius: 6,
            fontSize: 12, color: 'var(--text)', textDecoration: 'none',
            display: 'inline-flex', alignItems: 'center', gap: 6,
          }}><ArrowLeft size={14} strokeWidth={1.75} /> {backTo ? 'Problems' : 'Incidents'}</Link>
          <StatusPill s={inc.status} />
          {/* v0.10.796 — P rozeti liste ve Inbox ile aynı merdivenden (server).
              v0.10.1081 — gerekçe metni ("Declared incident, critical") şeritten
              çıktı, Teknik ayrıntı bölümünde; rozet yalnız önceliği söyler. */}
          {inc.priority && <PriorityBadge p={inc.priority} />}
          <SeverityPill s={inc.severity} />
          <span className="spacer" />
          {/* v0.9.477 — aksiyon çubuğunda buton, cevap sağ AI çekmecesinde
              (eskiden çubuğun altına satır-içi panel açıyordu). */}
          <AIExplainButton subject={{ kind: 'incident', id: inc.id }} />
          {isAdmin && inc.status === 'open' && <Button variant="secondary" onClick={ack}
            loading={busy === 'ack'} disabled={busy !== null}>Acknowledge</Button>}
          {isAdmin && inc.status !== 'resolved' && <Button variant="primary" onClick={resolve}
            loading={busy === 'resolve'} disabled={busy !== null}>Resolve</Button>}
        </div>

        {/* Title + meta chips */}
        <h1 style={{ fontSize: 20, margin: '0 0 4px' }}>{inc.title}</h1>
        {/* v0.10.1081 — başlığın hemen altında nöbetçinin üç saniyede okuduğu
            TEK cümle (birincil problemin kendi cümlesi) + "ne zaman" satırı;
            problem / anomali detaylarıyla aynı DetailSummary. */}
        <DetailSummary sentence={headline} when={when} />
        <div className="meta-row" style={{ marginBottom: 18 }}>
          {inc.service && <span className="chip"><span className="k">service</span><b className="mono">{inc.service}</b></span>}
          {/* v0.10.698 — bağlı problemlerin en güvenli hipotezi; şüpheli
              servisin olay penceresine link. Yoksa çip çizilmez. */}
          {cause && (
            <span className="chip" title={`Likely cause: ${cause.suspect} · confidence ${cause.pct}`}>
              <span className="k">root cause</span>
              <Link className="mono" to={serviceHref(cause.suspect, { range: eventLifespanWindow(inc) })}
                style={{ fontWeight: 600, color: 'var(--text)' }}>{cause.suspect}</Link>
              <span style={{ color: 'var(--text3)', marginLeft: 4 }}>{cause.pct}</span>
            </span>
          )}
          {/* v0.10.1081 — "started" / "duration" çipleri özetin "ne zaman"
              satırına taşındı (aynı bilgi iki kez basılmasın). */}
          {inc.assignee && <span className="chip"><span className="k">assignee</span><b>{inc.assignee}</b></span>}
        </div>

        {/* v0.10.1081 — bağlı problemler (her biri kendi grafikli detayına) +
            problem sayfalarındaki "Ne yapabilirim" pivotları. Eskiden düz
            kimlik listesi ve "Linked" kartı (servis + alarm kuralları). */}
        <div className="pd-cols pd-cols-15">
          <div style={{ minWidth: 0 }}>
            <Sect title="Bağlı problemler" sub={problems.length > 0 ? String(problems.length) : undefined}>
              {problems.length === 0 ? (
                <div className="pd-pivot-note">Bağlı problem yok.</div>
              ) : problems.map((pid, i) => {
                const p = detailById.get(pid);
                const href = `/inbox?problem=${encodeURIComponent(pid)}`;
                if (!p) {
                  // Henüz yüklenmedi / tavan dışı / saklama dışı: kimlikle bağlantı
                  // (sessiz kesim yok — tavan INCIDENT_PROBLEM_DETAIL_CAP).
                  return (
                    <Link key={pid} to={href} className="mono inc-prob inc-prob--bare" data-attached-problem={pid}
                      title={i >= INCIDENT_PROBLEM_DETAIL_CAP ? 'Ayrıntısı bu sayfada yüklenmedi — problem sayfasında aç' : 'Problem sayfasında aç'}>
                      {pid}
                    </Link>
                  );
                }
                return (
                  <Link key={pid} to={href} className="inc-prob" data-attached-problem={pid}
                    title="Problem detayı (grafik, kök neden, kanıt)">
                    <span className="inc-prob__head">
                      {p.priority && <PriorityBadge p={p.priority} />}
                      <b className="inc-prob__name">{attachedProblemLabel(p)}</b>
                      <TriageStatusBadge s={p.status} label={p.status} />
                      {primary?.id === p.id && problems.length > 1 && <span className="badge b-gray">ilk</span>}
                    </span>
                    <span className="inc-prob__what">{problemSentence(p)}</span>
                  </Link>
                );
              })}
            </Sect>
          </div>
          <div style={{ minWidth: 0 }}>
            <Sect title="Ne yapabilirim">
              {pivots.links.length > 0
                ? pivots.links.map(l => <SignalLink key={l.label} to={l.to} label={l.label} sub={l.sub} />)
                : <div className="pd-pivot-note">{pivots.note}</div>}
            </Sect>
          </div>
        </div>

        {/* Timeline (left) · Impact + Postmortem (right) */}
        <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 16 }}>
          {/* LEFT — Timeline */}
          <div className="card">
            <div className="ov-card-h"><h3>Timeline</h3><span className="ov-sub">{timeline.length} events</span></div>
            {timeline.length === 0 ? (
              <div className="ov-card-b" style={{ color: 'var(--text3)', fontSize: 12 }}>No events yet.</div>
            ) : (
              <div>
                {timeline.map((e, i) => {
                  const st = eventStyle(e.kind, inc.severity);
                  return (
                    <div className="prob" key={i}>
                      <div className="ic" style={{ background: `color-mix(in srgb, var(${st.token}) 14%, transparent)`, color: `var(${st.token})` }}>
                        {st.icon}
                      </div>
                      <div style={{ minWidth: 0 }}>
                        <div className="ti">{kindLabel(e.kind)}</div>
                        <div className="de">{e.body || e.actor || '—'}{e.actor && e.body ? ` · ${e.actor}` : ''}</div>
                      </div>
                      <div className="tm">{tsLong(e.time)}</div>
                    </div>
                  );
                })}
              </div>
            )}
            {isAdmin && (
              <div className="ov-card-b" style={{ display: 'flex', gap: 8, borderTop: '1px solid var(--divider)' }}>
                <input value={note} onChange={e => setNote(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && submitNote()}
                  placeholder="Add a note (mitigation tried, hypothesis, who's on it)…"
                  style={{ flex: 1 }} />
                <Button variant="primary" onClick={submitNote} loading={busy === 'note'}
                  disabled={!note.trim() || busy !== null}>Add note</Button>
              </div>
            )}
          </div>

          {/* RIGHT — Impact, Linked, Attached problems, Postmortem (stacked) */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            {/* Impact */}
            {svc && (
              <div className="card">
                <div className="ov-card-h"><h3>Impact</h3><span className="ov-sub">{svc} · error rate</span></div>
                <div className="ov-card-b" style={{ paddingTop: 10, paddingBottom: 10 }}>
                  {impactTimes.length < 2 ? (
                    // v0.9.206 review-fix: this branch unmounts the chart — and
                    // with it the dblclick onZoomReset target — so a zoom that
                    // resolved to an empty/1-point slice needs a visible way out.
                    <div style={{ height: 110, display: 'grid', placeItems: 'center', alignContent: 'center', gap: 8, color: 'var(--text3)', fontSize: 12 }}>
                      <span>{impactQ.isLoading ? 'Loading…' : 'No data in this window'}</span>
                      {zoomWin != null && !impactQ.isLoading && (
                        <Button variant="ghost" size="sm" onClick={() => setZoomWin(null)}>↩ Reset zoom</Button>
                      )}
                    </div>
                  ) : (
                    <OverviewChart times={impactTimes} height={110} mode="area" unit="%"
                      series={[{ label: 'error rate', color: 'var(--err)', data: impactData }]}
                      deployAtSec={deploy?.sec ?? null} deployLabel={deploy?.label}
                      onZoom={(f, t) => {
                        // v0.9.206 review-fix: ProblemDetail precedent (v >= 2
                        // ? v : occAll) — ignore a brush spanning <2 of the
                        // plotted points; it can only dead-end the chart.
                        if (impactTimes.filter(s => s >= f && s <= t).length < 2) return;
                        setZoomWin({ from: Math.round(f * 1e9), to: Math.round(t * 1e9) });
                      }}
                      onZoomReset={() => setZoomWin(null)} />
                  )}
                </div>
              </div>
            )}

            {/* v0.10.1081 — "Linked" (servis + alarm kuralları) ve düz kimlikli
                "Attached problems" kartları kalktı: ikisi de yukarıdaki "Bağlı
                problemler" + "Ne yapabilirim"de, okunur hâliyle. */}

            {/* Postmortem (preserved feature) */}
            <div className="card">
              <div className="ov-card-h">
                <h3><PenLine size={14} strokeWidth={1.75} style={{ verticalAlign: '-2px', marginRight: 4 }} />Postmortem</h3>
                {isAdmin && !editingPM && (
                  <Button variant="secondary" size="sm" onClick={() => setEditingPM(true)} style={{ marginLeft: 'auto' }}>
                    {inc.postmortem ? 'Edit' : 'Write'}
                  </Button>
                )}
              </div>
              <div className="ov-card-b">
                {editingPM ? (
                  <div>
                    <textarea value={postmortemDraft} onChange={e => setPostmortemDraft(e.target.value)}
                      rows={12} className="mono" style={{ width: '100%', resize: 'vertical' }}
                      placeholder={POSTMORTEM_TEMPLATE} />
                    <div style={{ display: 'flex', gap: 6, marginTop: 6, alignItems: 'center' }}>
                      <Button variant="secondary" size="sm" onClick={draftPM} loading={aiPM.busy} disabled={busy !== null}
                        title="Incident kanıtından (zaman çizelgesi + problemler + kök-neden hipotezleri) taslak üret">
                        <IconSparkles /> AI taslağı
                      </Button>
                      {aiPM.xid && !aiPM.busy && <AIFeedbackButtons exchangeId={aiPM.xid} />}
                      {aiPM.err && <span style={{ color: 'var(--err)', fontSize: 12 }}>{aiPM.err}</span>}
                      <div style={{ marginLeft: 'auto', display: 'flex', gap: 6 }}>
                        <Button variant="secondary" disabled={busy !== null}
                          onClick={() => { setEditingPM(false); setPostmortemDraft(inc.postmortem ?? ''); }}>Cancel</Button>
                        <Button variant="primary" onClick={savePM} loading={busy === 'pm'} disabled={busy !== null}>Save</Button>
                      </div>
                    </div>
                  </div>
                ) : inc.postmortem ? (
                  <div>
                    <pre className="mono" style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0 }}>{inc.postmortem}</pre>
                    {isAdmin && (
                      <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center' }}>
                        <Button variant="secondary" size="sm" onClick={addPMToKB} loading={kbPM.busy}
                          title="Kayıtlı postmortem'i bilgi tabanına indeksle — AI benzer incident'larda bulur">
                          KB'ye ekle
                        </Button>
                        {kbPM.done && <span className="badge b-ok">{kbPM.done}</span>}
                        {kbPM.err && <span style={{ color: 'var(--err)', fontSize: 12 }}>{kbPM.err}</span>}
                      </div>
                    )}
                  </div>
                ) : (
                  <div style={{ color: 'var(--text3)', fontSize: 12 }}>
                    {isAdmin ? 'Once resolved, write a blameless postmortem here.' : 'No postmortem yet.'}
                  </div>
                )}
              </div>
            </div>

            {/* v0.10.1081 — teknik gerekçeler (öncelik merdiveni metni, kayıtlı
                özet, kimlik) şeritten ve manşetten çıktı; burada, KAPALI. */}
            <Sect title="Teknik ayrıntı" collapsible>
              <div style={{ fontSize: 12, color: 'var(--text2)', display: 'grid', gap: 6 }}>
                {inc.priorityReason && <div>Öncelik gerekçesi: {inc.priorityReason}</div>}
                <div>Başlangıç: <span className="mono">{tsLong(inc.startedAt)}</span> · süre {fmtDuration(elapsedNs)}{inc.resolvedAt ? '' : ' (sürüyor)'}</div>
                <div>Incident kimliği: <span className="mono">{inc.id}</span></div>
                {inc.summary && inc.summary.trim() !== headline && <div>Kayıtlı özet: {inc.summary}</div>}
              </div>
            </Sect>
          </div>
        </div>
      </PageShell>
    </>
  );
}

const POSTMORTEM_TEMPLATE = `## Summary
What happened, in one paragraph.

## Impact
Who was affected and for how long.

## Root cause
The actual technical cause (be specific).

## Resolution
What we did to mitigate and fix.

## Action items
- [ ] Owner — concrete change to prevent recurrence
`;

// v0.10.929 (K5) — ton tek durum sözlüğünden (features/anomalies/statusTone):
// OPEN/ACK nötr, yalnız RESOLVED (geçiş) yeşil. Sözlük ProblemDetail'den
// değil hafif yaprak modülden gelir — bu parçaya ağır zincir girmez.
function StatusPill({ s }: { s: Incident['status'] }) {
  const label = s === 'open' ? 'OPEN' : s === 'acknowledged' ? 'ACK' : 'RESOLVED';
  return <TriageStatusBadge s={s} label={label} />;
}
function SeverityPill({ s }: { s: string }) {
  const cls = s === 'critical' ? 'b-err' : s === 'warning' ? 'b-warn' : 'b-info';
  return <span className={`badge ${cls}`}>{s.toUpperCase()}</span>;
}
function fmtDuration(ns: number): string {
  const sec = Math.floor(ns / 1e9);
  if (sec < 60)    return sec + 's';
  if (sec < 3600)  return Math.floor(sec / 60) + 'm';
  if (sec < 86400) return (sec / 3600).toFixed(1) + 'h';
  return Math.floor(sec / 86400) + 'd';
}
function kindLabel(k: string): string {
  switch (k) {
    case 'created':          return 'Incident opened';
    case 'ack':              return 'Acknowledged';
    case 'resolved':         return 'Resolved';
    case 'note':             return 'Note';
    case 'problem_attached': return 'Problem attached';
    case 'problem_resolved': return 'Problem resolved';
    case 'severity_raised':  return 'Severity raised'; // v0.10.802
    default:                 return k;
  }
}
// eventStyle — the .prob icon chip glyph + token tint per event kind, matching
// the prototype's level→color map (red/amber/green/accent).
function eventStyle(kind: string, severity: string): { icon: ReactNode; token: string } {
  switch (kind) {
    case 'created':          return { icon: <AlertTriangle size={16} />, token: severity === 'critical' ? '--err' : '--warn' };
    case 'ack':              return { icon: <Bell size={16} />, token: '--text3' }; // v0.10.929 (K5) — ack normal durum, STATUS_TONE gibi nötr
    case 'resolved':         return { icon: <Check size={16} />, token: '--ok' };
    case 'note':             return { icon: <MessageSquare size={16} />, token: '--accent' };
    case 'problem_attached': return { icon: <AlertTriangle size={16} />, token: '--err' };
    case 'problem_resolved': return { icon: <Check size={16} />, token: '--ok' };
    case 'severity_raised':  return { icon: <AlertTriangle size={16} />, token: '--err' }; // v0.10.802
    default:                 return { icon: <Zap size={16} />, token: '--text3' };
  }
}
