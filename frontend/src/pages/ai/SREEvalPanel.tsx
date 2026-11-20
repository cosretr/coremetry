// SREEvalPanel — v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk
// ölçümü (/ai). Ajan, insanın SONRADAN onayladığı kök nedenlerin (onaylı
// incident_cases) olduğu geçmiş problemlerde yeniden koşturulur (Kind=replay,
// çıpa problemin başlangıcı); hipotez sırası onaylı kategoriyle karşılaştırılır.
//
//   - Karolar: top-1 / top-3 isabeti, varlık eşleşmesi, kapsam (saklaması
//     dolmuş pencerede okuyucular "veri yok" döner), canlı koşularda ilk
//     hipoteze varış p50 / p90.
//   - Koşular: DataTable; satır seçimi URL'de (?evalRun=, replace).
//   - Vakalar: beklenen kategori vs ilk 3 hipotez.
//   - 👎 listesi: YALNIZ "incelemeye değer" adayları (admin). Operatör kuralı
//     "oylara çok güvenmeyelim": oy etiket değil, puan girdisi değil.
//   - Admin "Değerlendirme başlat" formu (POST /api/sre/eval/runs, audit).
//   - v0.10.1450 (program 2a): manşet karo "bağımsız top-1" (onaylayan CoSRE
//     önerisini aynen kabul etmediği vakalar — operatör kararı 2); koşu
//     tablosunda Kip + Taban; admin "Aynı kümeyi yeniden koş" ({rerunOf});
//     seçili koşu yeniden koşuysa taban ↔ bu koşu karşılaştırması
//     (SREEvalCompare).
import { useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Badge, Button, Field, Row, StatTile } from '@/components/ui';
import { SREEvalCompare } from './SREEvalCompare';
import { rowActivation, stopRowClick } from '@/lib/a11y';
import { tsLong, fmtNum } from '@/lib/utils';
import { keys } from '@/lib/queries/keys';
import { useSREEvalRuns, useSREEvalRun, useSREEvalReview } from '@/lib/queries/sreEval';
import type { SREEvalCase, SREEvalRun, SREReviewDownvote } from '@/lib/types';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTableStateProps, type DataTable } from '@/components/ui/DataTable';
import {
  HYP_KIND_LABEL, HIT_LABEL, CASE_STATE_LABEL, categoryLabel, caseHit, caseUnscoredReason, coverageText,
  coveragePct, defaultEvalWindow, durationTR, evalPct, independentTop1, runMode, validateEvalForm,
} from './sreEvalView';

const dayTR = (ns: number) => new Date(ns / 1e6).toISOString().slice(0, 10);

const RUN_COLS: ColumnDef<SREEvalRun>[] = [
  { id: 'when',     label: 'Başlatıldı', sortValue: r => r.createdAt, width: 165, minWidth: 140, priority: 1 },
  { id: 'window',   label: 'Vaka penceresi', sortValue: r => r.from, naturalDir: 'asc', flex: true, minWidth: 170, priority: 1 },
  { id: 'state',    label: 'Durum',     sortValue: r => r.state, naturalDir: 'asc', width: 110, minWidth: 90, priority: 1 },
  { id: 'cases',    label: 'Vaka',      sortValue: r => r.found, numeric: true, width: 70, minWidth: 60, priority: 2 },
  { id: 'scored',   label: 'Puanlı',    sortValue: r => r.score.scored, numeric: true, width: 75, minWidth: 60, priority: 2 },
  { id: 'ind1',     label: 'Bağımsız top-1', sortValue: r => r.score.independentTop1Acc ?? -1, numeric: true, width: 110, minWidth: 80, priority: 1 },
  { id: 'top1',     label: 'Top-1',     sortValue: r => r.score.top1Acc ?? -1, numeric: true, width: 75, minWidth: 60, priority: 1 },
  { id: 'top3',     label: 'Top-3',     sortValue: r => r.score.top3Acc ?? -1, numeric: true, width: 75, minWidth: 60, priority: 2 },
  { id: 'coverage', label: 'Kapsam',    sortValue: r => r.score.coveragePct ?? -1, numeric: true, width: 80, minWidth: 64, priority: 2 },
  { id: 'mode',     label: 'Kip',       sortValue: r => runMode(r), naturalDir: 'asc', width: 115, minWidth: 90, priority: 3 },
  { id: 'base',     label: 'Taban',     sortValue: r => r.rerunOf ?? '', naturalDir: 'asc', width: 120, minWidth: 90, priority: 2 },
  { id: 'by',       label: 'Başlatan',  sortValue: r => r.by ?? '', naturalDir: 'asc', width: 170, minWidth: 110, priority: 3 },
];

const CASE_COLS: ColumnDef<SREEvalCase>[] = [
  { id: 'problem',  label: 'Problem',   sortValue: c => c.displayId || c.problemId, naturalDir: 'asc', width: 110, minWidth: 90, priority: 1 },
  { id: 'service',  label: 'Servis',    sortValue: c => c.service, naturalDir: 'asc', width: 160, minWidth: 110, priority: 1 },
  { id: 'expected', label: 'Beklenen',  sortValue: c => c.category, naturalDir: 'asc', width: 120, minWidth: 90, priority: 1 },
  { id: 'top',      label: 'Ajanın ilk 3 hipotezi', sortValue: c => c.top?.[0]?.kind ?? '', naturalDir: 'asc', flex: true, minWidth: 220, priority: 1 },
  { id: 'hit',      label: 'İsabet',    sortValue: c => hitRank(c), width: 100, minWidth: 80, priority: 1 },
  { id: 'subject',  label: 'Özne',      sortValue: c => c.top?.[0]?.subject ?? '', naturalDir: 'asc', width: 140, minWidth: 100, priority: 3 },
  { id: 'coverage', label: 'Kapsam',    sortValue: c => c.coverage?.data ?? -1, numeric: true, width: 80, minWidth: 64, priority: 2 },
  { id: 'cause',    label: 'Onaylı neden', sortValue: c => c.finalCause, naturalDir: 'asc', width: 220, minWidth: 140, priority: 3 },
  { id: 'started',  label: 'Başlangıç', sortValue: c => c.startedAt, width: 165, minWidth: 140, priority: 3 },
];

const REVIEW_COLS: ColumnDef<SREReviewDownvote>[] = [
  { id: 'when',    label: 'Ne zaman', sortValue: r => r.createdAt, width: 165, minWidth: 140, priority: 1 },
  { id: 'surface', label: 'Yüzey',    sortValue: r => r.surface, naturalDir: 'asc', width: 150, minWidth: 110, priority: 2, mono: true },
  { id: 'reason',  label: 'Neden',    sortValue: r => r.reason ?? '', naturalDir: 'asc', width: 100, minWidth: 80, priority: 1 },
  { id: 'comment', label: 'Yorum / cevap', sortValue: r => r.comment || r.response || '', naturalDir: 'asc', flex: true, minWidth: 200, priority: 1 },
  { id: 'who',     label: 'Kim',      sortValue: r => r.userEmail ?? '', naturalDir: 'asc', width: 170, minWidth: 110, priority: 3 },
];

const HIT_TONE = { top1: 'success', top3: 'info', miss: 'warning' } as const;

function hitRank(c: SREEvalCase): number {
  const h = caseHit(c);
  return h === 'top1' ? 3 : h === 'top3' ? 2 : h === 'miss' ? 1 : 0;
}

function stateOf<T>(data: T[] | undefined, isError: boolean, empty: string): Omit<DataTableStateProps<T>, 'dt'> {
  if (isError) return { kind: 'error' };
  if (data === undefined) return { kind: 'loading' };
  return { kind: 'empty', message: empty };
}

export function SREEvalPanel() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const [sp, setSp] = useSearchParams();
  const runsQ = useSREEvalRuns();
  const runs = runsQ.data?.runs;
  const selectedId = sp.get('evalRun') || runs?.[0]?.runId || '';
  const runQ = useSREEvalRun(selectedId);
  const reviewQ = useSREEvalReview(7, isAdmin);
  const select = (id: string) => setSp(prev => {
    const n = new URLSearchParams(prev);
    n.set('evalRun', id);
    return n;
  }, { replace: true });

  const selected = runQ.data ?? runs?.find(r => r.runId === selectedId);
  const score = selected?.score;
  const ind = selected ? independentTop1(selected) : null;
  const live = runsQ.data?.live;

  const runDt = useDataTable<SREEvalRun>({ storageKey: 'ai-sre-eval-runs', columns: RUN_COLS, rows: runs ?? [] });
  const cases = runQ.data?.cases;
  const caseDt = useDataTable<SREEvalCase>({ storageKey: 'ai-sre-eval-cases', columns: CASE_COLS, rows: cases ?? [] });
  const reviewDt = useDataTable<SREReviewDownvote>({ storageKey: 'ai-sre-eval-review', columns: REVIEW_COLS, rows: reviewQ.data?.items ?? [] });

  return (
    <div className="card sre-eval">
      <div className="ov-card-h">
        <h3>AI SRE doğruluğu — geçmiş problemler</h3>
        <span className="ov-sub">ajan onaylı vakalarda yeniden koşar; doğruluk etiketi YALNIZ onaylı kök neden (👍/👎 puana girmez)</span>
      </div>
      <div className="ov-card-b">
        {isAdmin && <StartForm onStarted={select} enabled={runsQ.data?.enabled ?? true} selected={selected} />}

        <div className="stat-grid">
          <StatTile label="Bağımsız top-1 (manşet)">
            <span className="mono">{ind ? ind.pct : '—'}</span>{' '}
            <span className="field-hint">{ind ? ind.hint : selected ? 'eski koşu — öneri bayrağı yok' : ''}</span>
          </StatTile>
          <StatTile label="Top-1 isabet · tüm vakalar">
            <span className="mono">{evalPct(score?.top1Acc)}</span>{' '}
            <span className="field-hint">{score ? `${score.top1Hits}/${score.scored} vaka` : ''}</span>
          </StatTile>
          <StatTile label="Top-3 isabet">
            <span className="mono">{evalPct(score?.top3Acc)}</span>{' '}
            <span className="field-hint">{score ? `${score.top3Hits}/${score.scored} vaka` : ''}</span>
          </StatTile>
          <StatTile label="Varlık eşleşmesi">
            <span className="mono">{evalPct(score?.entityAcc)}</span>{' '}
            <span className="field-hint">{score ? `${score.entityHits}/${score.entityScored} — özne onaylı nedende` : ''}</span>
          </StatTile>
          <StatTile label="Kapsam" tone={score?.coveragePct != null && score.coveragePct < 50 ? 'warn' : undefined}>
            <span className="mono">{coveragePct(score?.coveragePct)}</span>{' '}
            <span className="field-hint">{score ? `veri yok ${score.noData} · eşlenmeyen ${score.unmapped} · sürüyor ${score.pending}` : ''}</span>
          </StatTile>
          <StatTile label="İlk hipotez p50 / p90">
            <span className="mono">{durationTR(live?.p50Ms)} / {durationTR(live?.p90Ms)}</span>{' '}
            <span className="field-hint">{live ? `canlı ${fmtNum(live.runs)} koşu` : ''}</span>
          </StatTile>
        </div>

        <div className="table-wrap">
          <table {...runDt.tableProps}>
            <DataTableColgroup dt={runDt} />
            <DataTableHead dt={runDt} />
            <tbody>
              {runDt.sortedRows.length === 0
                ? <DataTableState dt={runDt} {...stateOf(runs, runsQ.isError, isAdmin ? 'Henüz değerlendirme yok — yukarıdan başlatın' : 'Henüz değerlendirme yok (yönetici başlatır)')} />
                : runDt.sortedRows.map(r => (
                  <tr key={r.runId} className={r.runId === selectedId ? 'row-selected' : undefined}
                    aria-selected={r.runId === selectedId} {...rowActivation(() => select(r.runId))}>
                    <DataTableCell dt={runDt} col="when" row={r} value={tsLong(r.createdAt)} className="mono" />
                    <DataTableCell dt={runDt} col="window" row={r} value={`${dayTR(r.from)} → ${dayTR(r.to)} · en çok ${r.limit}`} />
                    <DataTableCell dt={runDt} col="state" row={r} value={r.state === 'done' ? 'bitti' : 'sürüyor'} />
                    <DataTableCell dt={runDt} col="cases" row={r} value={fmtNum(r.found)} />
                    <DataTableCell dt={runDt} col="scored" row={r} value={fmtNum(r.score.scored)} />
                    <DataTableCell dt={runDt} col="ind1" row={r} value={r.suggestionKnown ? evalPct(r.score.independentTop1Acc) : null} />
                    <DataTableCell dt={runDt} col="top1" row={r} value={evalPct(r.score.top1Acc)} />
                    <DataTableCell dt={runDt} col="top3" row={r} value={evalPct(r.score.top3Acc)} />
                    <DataTableCell dt={runDt} col="coverage" row={r} value={coveragePct(r.score.coveragePct)} />
                    <DataTableCell dt={runDt} col="mode" row={r}><Badge tone={r.loop || r.contextPack ? 'info' : 'neutral'}>{runMode(r)}</Badge></DataTableCell>
                    <DataTableCell dt={runDt} col="base" row={r}>
                      {r.rerunOf
                        ? <Button variant="ghost" size="sm" className="mono" title="taban koşusunu seç"
                          onClick={e => { stopRowClick(e); select(r.rerunOf!); }}>{r.rerunOf}</Button>
                        : <span className="cell-empty">—</span>}
                    </DataTableCell>
                    <DataTableCell dt={runDt} col="by" row={r} value={r.by || null} />
                  </tr>
                ))}
            </tbody>
          </table>
        </div>

        {selected?.rerunOf && <SREEvalCompare base={selected.rerunOf} cand={selected.runId} />}

        {selectedId && (
          <>
            <div className="ov-card-h sre-eval-sec">
              <h3>Vakalar</h3>
              <span className="ov-sub">beklenen = onaylı vakanın kategorisi · isabet ilk 3 hipotezin kategorisiyle</span>
            </div>
            <div className="table-wrap">
              <table {...caseDt.tableProps}>
                <DataTableColgroup dt={caseDt} />
                <DataTableHead dt={caseDt} />
                <tbody>
                  {caseDt.sortedRows.length === 0
                    ? <DataTableState dt={caseDt} {...stateOf(runQ.data ? (cases ?? []) : undefined, runQ.isError, 'Penceredeki onaylı problem vakası yok')} />
                    : caseDt.sortedRows.map(c => <CaseRow key={c.caseId || c.problemId} c={c} dt={caseDt} />)}
                </tbody>
              </table>
            </div>
          </>
        )}

        <div className="ov-card-h sre-eval-sec">
          <h3>👎 incelemeye değer</h3>
          <span className="ov-sub">{reviewQ.data?.note ?? 'AI SRE yüzeylerine verilen 👎 — etiket DEĞİL, puana girmez'} · son 7 gün</span>
        </div>
        {isAdmin ? (
          <div className="table-wrap">
            <table {...reviewDt.tableProps}>
              <DataTableColgroup dt={reviewDt} />
              <DataTableHead dt={reviewDt} />
              <tbody>
                {reviewDt.sortedRows.length === 0
                  ? <DataTableState dt={reviewDt} {...stateOf(reviewQ.data?.items, reviewQ.isError, 'Son 7 günde AI SRE yüzeylerine 👎 yok')} />
                  : reviewDt.sortedRows.map(r => (
                    <tr key={r.exchangeId + (r.callId ?? '')}>
                      <DataTableCell dt={reviewDt} col="when" row={r} value={tsLong(r.createdAt)} className="mono" />
                      <DataTableCell dt={reviewDt} col="surface" row={r} value={r.surface} />
                      <DataTableCell dt={reviewDt} col="reason" row={r} value={r.reason || null} />
                      <DataTableCell dt={reviewDt} col="comment" row={r} value={r.comment || r.response || null}
                        title={r.prompt ? `İstem: ${r.prompt}` : undefined} />
                      <DataTableCell dt={reviewDt} col="who" row={r} value={r.userEmail || null} />
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="field-hint">Liste istem / cevap örneği taşıdığı için yalnız yöneticiye görünür.</p>
        )}
      </div>
    </div>
  );
}

function CaseRow({ c, dt }: { c: SREEvalCase; dt: DataTable<SREEvalCase> }) {
  const hit = caseHit(c);
  const unscored = caseUnscoredReason(c);
  return (
    <tr>
      <DataTableCell dt={dt} col="problem" row={c}>
        <Link to={`/problems?problem=${encodeURIComponent(c.problemId)}`} onClick={stopRowClick} className="mono">{c.displayId || c.problemId}</Link>
      </DataTableCell>
      <DataTableCell dt={dt} col="service" row={c} value={c.service || null} />
      <DataTableCell dt={dt} col="expected" row={c} value={categoryLabel(c.category)} />
      <DataTableCell dt={dt} col="top" row={c}>
        {(c.top?.length ?? 0) === 0
          ? <span className="cell-empty">{CASE_STATE_LABEL[c.state] ?? c.state}</span>
          : <span className="dt-trunc">{(c.top ?? []).map((h, i) => (
            <Badge key={h.kind} className="sre-eval-hyp" tone={h.category === c.category ? 'success' : 'neutral'} title={`${categoryLabel(h.category)} · skor ${h.score.toFixed(2)} (${h.band})`}>
              {i + 1}. {HYP_KIND_LABEL[h.kind] ?? h.kind}
            </Badge>
          ))}</span>}
      </DataTableCell>
      <DataTableCell dt={dt} col="hit" row={c}>
        {hit ? <Badge tone={HIT_TONE[hit]}>{HIT_LABEL[hit]}</Badge> : <span className="cell-empty" title={unscored ?? undefined}>{unscored ?? '—'}</span>}
      </DataTableCell>
      <DataTableCell dt={dt} col="subject" row={c} value={c.top?.[0]?.subject || null} />
      <DataTableCell dt={dt} col="coverage" row={c} value={coverageText(c)}
        title={c.coverage?.noDataSteps?.length ? `veri yok: ${c.coverage.noDataSteps.join(', ')}` : undefined} />
      <DataTableCell dt={dt} col="cause" row={c} value={c.finalCause || null} title={c.finalCause || undefined} />
      <DataTableCell dt={dt} col="started" row={c} value={tsLong(c.startedAt)} className="mono" />
    </tr>
  );
}

function StartForm({ onStarted, enabled, selected }: { onStarted: (id: string) => void; enabled: boolean; selected?: SREEvalRun }) {
  const qc = useQueryClient();
  const init = useMemo(() => defaultEvalWindow(Date.now()), []);
  const [from, setFrom] = useState(init.from);
  const [to, setTo] = useState(init.to);
  const [limit, setLimit] = useState('20');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const submit = async (rerunOf?: string, contextPack?: boolean) => {
    const v = rerunOf ? { rerunOf, ...(contextPack ? { contextPack: true } : {}) } : validateEvalForm(from, to, Number(limit));
    if (typeof v === 'string') { setErr(v); return; }
    setBusy(true);
    setErr(null);
    try {
      const run = await api.startSREEval(v);
      await qc.invalidateQueries({ queryKey: keys.sre.all });
      onStarted(run.runId);
    } catch (e) {
      setErr(e instanceof Error ? e.message.replace(/^HTTP \d+: /, '') : 'başlatılamadı');
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="sre-eval-form">
      <Row gap={3} wrap>
        <Field label="Vaka açılışı — başlangıç" type="date" value={from} onChange={e => setFrom(e.target.value)} />
        <Field label="bitiş" type="date" value={to} onChange={e => setTo(e.target.value)} />
        <Field label="En çok vaka (1–50)" type="number" min={1} max={50} value={limit} onChange={e => setLimit(e.target.value)} />
        <Button variant="primary" size="sm" loading={busy} disabled={busy || !enabled} onClick={() => { void submit(); }}
          title={enabled ? 'Onaylı problem vakalarını ajanla yeniden koştur (düşük öncelik, LLM çağrısı yok)' : 'AI SRE kapalı'}>
          Değerlendirme başlat
        </Button>
        {selected && (
          <Button variant="secondary" size="sm" disabled={busy || !enabled || selected.state !== 'done'} onClick={() => { void submit(selected.runId); }}
            title={selected.state === 'done'
              ? `Seçili koşunun (${selected.runId}) vaka kümesini aynı çıpalarla yeniden koştur — sonra taban ↔ yeni koşu karşılaştırılır (LLM çağrısı yok)`
              : 'seçili koşu bitmeden yeniden koşulamaz'}>
            Aynı kümeyi yeniden koş
          </Button>
        )}
        {selected && (
          <Button variant="secondary" size="sm" disabled={busy || !enabled || selected.state !== 'done'} onClick={() => { void submit(selected.runId, true); }}
            title={selected.state === 'done'
              ? `Seçili koşunun (${selected.runId}) kümesini Context Pack okuyucularıyla (baseline / anomaliler / hata özeti) yeniden koştur — deterministik, LLM çağrısı yok; sonra taban ↔ paketli koşu`
              : 'seçili koşu bitmeden yeniden koşulamaz'}>
            Bağlam paketiyle yeniden koş
          </Button>
        )}
      </Row>
      {err && <p className="field-error" role="alert">{err}</p>}
    </div>
  );
}
