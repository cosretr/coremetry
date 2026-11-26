// SREReplayPanel — CoSRE doğruluk ölçümü Faz A, dilim A2 (/ai,
// SREEvalPanel'in yanında; tembel chunk). "Kök neden" etiketli Problem'lerde
// SRE araştırması problemin KENDİ penceresinde yeniden koşar (canlı kayıt
// yazılmaz, bildirim yok); yerel hakem sonucu etikete göre notlar:
// Tam isabet 100 · Hedefte 65 · Iska 35 · Çok uzak 0 + erişim (kanıt suçluya
// vardı mı). Problem başına N koşu → koşudan koşuya yayılım.
//
//   - Karolar: puan, dağılım, erişim, kararsızlık; rapor eğilimi.
//   - Raporlar: DataTable; seçim URL'de (?replayReport=, replace).
//   - Problem satırları: etiket ↔ koşuların notu (ipucunda gerekçe + sonuç).
//   - CoSRE ölçüm: rapor sürümü (başlatan → notlayan), yol notu (okuma /
//     dalga / boşa / tavan; deterministik), sürüm eğilimi (Sparkline) ve
//     iki raporun karşılaştırması (GET /api/sre/replay/compare).
//   - Admin: "Replay başlat" (gün / koşu / Problem / varyant) + haftalık
//     zamanlama aç/kapa (varsayılan kapalı). Admin ekranı — çevrilmez.
import { useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Badge, Button, Field, Row, SelectField, StatTile } from '@/components/ui';
import { stopRowClick, rowActivation } from '@/lib/a11y';
import { tsLong, fmtNum } from '@/lib/utils';
import { keys } from '@/lib/queries/keys';
import { useSREReplayCompare, useSREReplayReport, useSREReplayReports } from '@/lib/queries/sreReplay';
import type { SREReplayCompareItem, SREReplayItem, SREReplayListItem, SREReplaySchedule } from '@/lib/types';
import { Sparkline } from '@/components/Sparkline';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { categoryLabel } from './sreEvalView';
import {
  GRADE_LABEL, GRADE_ORDER, GRADE_TONE, VERDICT_LABEL, VERDICT_TONE, buildText, defaultBase, deltaText, medText, pathCell, pathTitle,
  pctText, reachCell, reportMode, runLabel, runTitle, scoreText, scoreTrend, versionTrend,
} from './sreReplayView';

const REPORT_COLS: ColumnDef<SREReplayListItem>[] = [
  { id: 'when',     label: 'Başlatıldı', sortValue: r => r.createdAt, width: 165, minWidth: 140, priority: 1 },
  { id: 'window',   label: 'Etiket penceresi', sortValue: r => r.days, numeric: true, width: 120, minWidth: 100, priority: 2 },
  { id: 'state',    label: 'Durum', sortValue: r => r.state, naturalDir: 'asc', width: 100, minWidth: 80, priority: 1 },
  { id: 'problems', label: 'Problem', sortValue: r => r.summary.problems, numeric: true, width: 80, minWidth: 64, priority: 2 },
  { id: 'score',    label: 'Puan', sortValue: r => r.summary.score ?? -1, numeric: true, width: 80, minWidth: 64, priority: 1 },
  { id: 'reach',    label: 'Erişim', sortValue: r => r.summary.reachRate ?? -1, numeric: true, width: 80, minWidth: 64, priority: 1 },
  { id: 'unstable', label: 'Kararsız', sortValue: r => r.summary.unstable, numeric: true, width: 85, minWidth: 64, priority: 2 },
  { id: 'mode',     label: 'Kip', sortValue: r => reportMode(r), naturalDir: 'asc', flex: true, minWidth: 120, priority: 2 },
  { id: 'build',    label: 'Sürüm', sortValue: r => r.build ?? '', naturalDir: 'asc', width: 130, minWidth: 90, priority: 2 },
  { id: 'by',       label: 'Başlatan', sortValue: r => r.by ?? '', naturalDir: 'asc', width: 170, minWidth: 110, priority: 3 },
];

const ITEM_COLS: ColumnDef<SREReplayItem>[] = [
  { id: 'problem',  label: 'Problem', sortValue: i => i.problemId, naturalDir: 'asc', width: 120, minWidth: 90, priority: 1 },
  { id: 'service',  label: 'Servis', sortValue: i => i.service, naturalDir: 'asc', width: 150, minWidth: 100, priority: 2 },
  { id: 'label',    label: 'Etiket (gerçek kök neden)', sortValue: i => i.cause, naturalDir: 'asc', flex: true, minWidth: 220, priority: 1 },
  { id: 'culprit',  label: 'Suçlu', sortValue: i => i.culprit ?? '', naturalDir: 'asc', width: 130, minWidth: 90, priority: 3 },
  { id: 'runs',     label: 'Koşuların notu', sortValue: i => i.mean ?? -1, width: 220, minWidth: 160, priority: 1 },
  { id: 'mean',     label: 'Ort.', sortValue: i => i.mean ?? -1, numeric: true, width: 70, minWidth: 56, priority: 1 },
  { id: 'spread',   label: 'Yayılım', sortValue: i => i.spread, numeric: true, width: 80, minWidth: 64, priority: 2 },
  { id: 'reach',    label: 'Erişim', sortValue: i => reachCell(i.runs) ?? '', naturalDir: 'asc', width: 75, minWidth: 60, priority: 2 },
  { id: 'path',     label: 'Yol (okuma · dalga · boşa)', sortValue: i => i.runs.reduce((n, r) => n + (r.path?.wasted ?? 0), 0), numeric: true, width: 230, minWidth: 160, priority: 2 },
];

const CMP_COLS: ColumnDef<SREReplayCompareItem>[] = [
  { id: 'problem',  label: 'Problem', sortValue: i => i.problemId, naturalDir: 'asc', width: 120, minWidth: 90, priority: 1 },
  { id: 'service',  label: 'Servis', sortValue: i => i.service, naturalDir: 'asc', width: 150, minWidth: 100, priority: 2 },
  { id: 'verdict',  label: 'Hüküm', sortValue: i => i.verdict, naturalDir: 'asc', width: 120, minWidth: 90, priority: 1 },
  { id: 'base',     label: 'Taban', sortValue: i => i.baseMean ?? -1, numeric: true, width: 80, minWidth: 64, priority: 1 },
  { id: 'cand',     label: 'Aday', sortValue: i => i.candMean ?? -1, numeric: true, width: 80, minWidth: 64, priority: 1 },
  { id: 'delta',    label: 'Fark', sortValue: i => i.delta ?? 0, numeric: true, width: 80, minWidth: 64, priority: 1 },
  { id: 'grades',   label: 'Notlar (taban → aday)', sortValue: i => i.candGrades.join(','), naturalDir: 'asc', flex: true, minWidth: 200, priority: 2 },
];

export function SREReplayPanel() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const qc = useQueryClient();
  const [sp, setSp] = useSearchParams();
  const listQ = useSREReplayReports();
  const reports = listQ.data?.reports;
  const selectedId = sp.get('replayReport') || reports?.[0]?.reportId || '';
  const detailQ = useSREReplayReport(selectedId);
  const select = (id: string) => setSp(prev => {
    const n = new URLSearchParams(prev);
    n.set('replayReport', id);
    return n;
  }, { replace: true });
  const sm = detailQ.data?.summary ?? reports?.find(r => r.reportId === selectedId)?.summary;
  const reportDt = useDataTable<SREReplayListItem>({ storageKey: 'ai-sre-replay-reports', columns: REPORT_COLS, rows: reports ?? [] });
  const items = detailQ.data?.items;
  const itemDt = useDataTable<SREReplayItem>({ storageKey: 'ai-sre-replay-items', columns: ITEM_COLS, rows: items ?? [] });
  const refresh = () => { void qc.invalidateQueries({ queryKey: keys.sre.all }); };

  return (
    <div className="card sre-eval" data-testid="sre-replay">
      <div className="ov-card-h">
        <h3>CoSRE replay notu — etiketli Problem'ler</h3>
        <span className="ov-sub">
          araştırma problemin kendi penceresinde yeniden koşar (canlı kayıt / bildirim yok); yerel hakem "Kök neden" etiketine göre notlar ·
          Tam isabet 100 · Hedefte 65 · Iska 35 · Çok uzak 0
        </span>
      </div>
      <div className="ov-card-b">
        {isAdmin && <StartForm enabled={listQ.data?.enabled ?? true} schedule={listQ.data?.schedule} onStarted={select} />}

        <div className="stat-grid">
          <StatTile label="Puan (0–100)">
            <span className="mono">{scoreText(sm?.score)}</span>{' '}
            <span className="field-hint">{sm ? `${sm.graded} notlu koşu · ${sm.pending} bekliyor · ${sm.errored} notsuz` : ''}</span>
          </StatTile>
          <StatTile label="Dağılım">
            {sm
              ? <span className="dt-trunc">{GRADE_ORDER.map(g => (
                <Badge key={g} tone={GRADE_TONE[g]} className="sre-eval-hyp">{GRADE_LABEL[g]} {sm.dist[g] ?? 0}</Badge>
              ))}</span>
              : <span className="mono">—</span>}
          </StatTile>
          <StatTile label="Erişim (kanıt suçluya vardı)">
            <span className="mono">{pctText(sm?.reachRate)}</span>{' '}
            <span className="field-hint">{sm ? `${sm.reached}/${sm.reachKnown} koşu` : ''}</span>
          </StatTile>
          <StatTile label="Kanıt yetersiz (sonuçsuz)">
            <span className="mono">{sm?.inconclusive ?? '—'}</span>{' '}
            <span className="field-hint">{sm ? `${sm.runs} koşu` : ''}</span>
          </StatTile>
          <StatTile label="Koşudan koşuya" tone={sm && sm.unstable > 0 ? 'warn' : undefined}>
            <span className="mono">{sm ? `${sm.unstable}/${sm.multiRun}` : '—'}</span>{' '}
            <span className="field-hint">{sm ? `kararsız · ort. yayılım ${scoreText(sm.meanSpread)} · en çok ${sm.maxSpread}` : ''}</span>
          </StatTile>
          <StatTile label="Eğilim (eskiden yeniye)">
            <span className="mono">{reports ? scoreTrend(reports) : '—'}</span>
          </StatTile>
          <StatTile label="Yol (medyan, deterministik)" tone={sm && (sm.capHits ?? 0) > 0 ? 'warn' : undefined}>
            <span className="mono">{sm?.pathRuns ? `${medText(sm.medToolCalls)} okuma · ${medText(sm.medRounds)} dalga · ${medText(sm.medWasted)} boşa` : '—'}</span>{' '}
            <span className="field-hint"
              title="boşa = sonucu hiçbir hipotez kontrolünün kanıt künyesine girmeyen ve bulgu üretmeyen okuma (hata veren okuma dahil)">
              {sm?.pathRuns ? `${sm.pathRuns} koşu · ${sm.capHits ?? 0} tavana dayandı · medyan ${medText(sm.medSteps)} adım` : 'yol notu yok (eski rapor)'}
            </span>
          </StatTile>
          <VersionTrendTile reports={reports} />
        </div>

        <Row gap={2}>
          <Button variant="ghost" size="sm" onClick={refresh}>Yenile</Button>
        </Row>

        <div className="table-wrap">
          <table {...reportDt.tableProps}>
            <DataTableColgroup dt={reportDt} />
            <DataTableHead dt={reportDt} />
            <tbody>
              {reportDt.sortedRows.length === 0
                ? <DataTableState dt={reportDt} kind={listQ.isError ? 'error' : reports === undefined ? 'loading' : 'empty'}
                    message={isAdmin ? 'Henüz replay raporu yok — yukarıdan başlatın' : 'Henüz replay raporu yok (yönetici başlatır)'} />
                : reportDt.sortedRows.map(r => (
                  <tr key={r.reportId} className={r.reportId === selectedId ? 'row-selected' : undefined}
                    aria-selected={r.reportId === selectedId} {...rowActivation(() => select(r.reportId))}>
                    <DataTableCell dt={reportDt} col="when" row={r} value={tsLong(r.createdAt)} className="mono" />
                    <DataTableCell dt={reportDt} col="window" row={r} value={`son ${r.days} gün`} />
                    <DataTableCell dt={reportDt} col="state" row={r} value={r.state === 'done' ? 'bitti' : 'sürüyor'} />
                    <DataTableCell dt={reportDt} col="problems" row={r} value={fmtNum(r.summary.problems)} />
                    <DataTableCell dt={reportDt} col="score" row={r} value={r.summary.score == null ? null : scoreText(r.summary.score)} />
                    <DataTableCell dt={reportDt} col="reach" row={r} value={r.summary.reachRate == null ? null : pctText(r.summary.reachRate)} />
                    <DataTableCell dt={reportDt} col="unstable" row={r} value={fmtNum(r.summary.unstable)} />
                    <DataTableCell dt={reportDt} col="mode" row={r} value={`${reportMode(r)} · ${r.repeats} koşu`} />
                    <DataTableCell dt={reportDt} col="build" row={r} value={r.build || r.gradeBuild ? buildText(r) : null} className="mono" />
                    <DataTableCell dt={reportDt} col="by" row={r} value={r.by || null} />
                  </tr>
                ))}
            </tbody>
          </table>
        </div>

        {selectedId && (
          <>
            <div className="ov-card-h sre-eval-sec">
              <h3>Problem'ler</h3>
              <span className="ov-sub">etiket ↔ her koşunun notu (ipucunda hakem gerekçesi + ajanın sonucu) · yayılım = en yüksek − en düşük puan</span>
            </div>
            <div className="table-wrap">
              <table {...itemDt.tableProps}>
                <DataTableColgroup dt={itemDt} />
                <DataTableHead dt={itemDt} />
                <tbody>
                  {itemDt.sortedRows.length === 0
                    ? <DataTableState dt={itemDt} kind={detailQ.isError ? 'error' : items === undefined ? 'loading' : 'empty'}
                        message="Penceredeki etiketli Problem yok — Problem detayında “Kök neden” girilir" />
                    : itemDt.sortedRows.map(i => (
                      <tr key={i.problemId}>
                        <DataTableCell dt={itemDt} col="problem" row={i}>
                          <Link to={`/problems?problem=${encodeURIComponent(i.problemId)}`} onClick={stopRowClick} className="mono">{i.problemId}</Link>
                        </DataTableCell>
                        <DataTableCell dt={itemDt} col="service" row={i} value={i.service || null} />
                        <DataTableCell dt={itemDt} col="label" row={i} value={`${categoryLabel(i.category)} · ${i.cause}`} title={i.cause} />
                        <DataTableCell dt={itemDt} col="culprit" row={i} value={i.culprit || null} className="mono" />
                        <DataTableCell dt={itemDt} col="runs" row={i}>
                          {i.state === 'skipped'
                            ? <span className="cell-empty" title={i.reason}>atlandı — {i.reason}</span>
                            : <span className="dt-trunc">{i.runs.map(r => (
                              <Badge key={r.rep} className="sre-eval-hyp" tone={r.grade ? GRADE_TONE[r.grade] : 'neutral'} title={runTitle(r)}>
                                {r.rep}. {runLabel(r)}
                              </Badge>
                            ))}</span>}
                        </DataTableCell>
                        <DataTableCell dt={itemDt} col="mean" row={i} value={i.mean == null ? null : scoreText(i.mean)} />
                        <DataTableCell dt={itemDt} col="spread" row={i} value={i.mean == null ? null : String(i.spread)} />
                        <DataTableCell dt={itemDt} col="reach" row={i} value={reachCell(i.runs)} />
                        <DataTableCell dt={itemDt} col="path" row={i} value={pathCell(i.runs)} title={pathTitle(i.runs)} className="mono" />
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
            {reports && reports.length > 1 && <ComparePanel key={selectedId} reports={reports} candId={selectedId} />}
          </>
        )}
      </div>
    </div>
  );
}

function StartForm({ enabled, schedule, onStarted }: { enabled: boolean; schedule?: SREReplaySchedule; onStarted: (id: string) => void }) {
  const qc = useQueryClient();
  const [days, setDays] = useState('30');
  const [repeats, setRepeats] = useState('2');
  const [limit, setLimit] = useState('20');
  const [variant, setVariant] = useState('default');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      await qc.invalidateQueries({ queryKey: keys.sre.all });
    } catch (e) {
      setErr(e instanceof Error ? e.message.replace(/^HTTP \d+: /, '') : 'başarısız');
    } finally {
      setBusy(false);
    }
  };
  const start = () => run(async () => {
    const rep = await api.startSREReplay({ days: Number(days), repeats: Number(repeats), limit: Number(limit),
      variant: variant === 'contextPack' ? { contextPack: true } : variant === 'plain' ? { plain: true }
        : variant === 'noMemory' ? { noMemory: true } : variant === 'noKnown' ? { noKnown: true } : {} });
    onStarted(rep.reportId);
  });
  const weekly = !!schedule?.weekly;
  const toggleWeekly = () => run(() => api.putSREReplaySchedule({ weekly: !weekly, days: Number(days), repeats: Number(repeats) }));
  return (
    <div className="sre-eval-form">
      <Row gap={3} wrap>
        <SelectField label="Etiket penceresi" value={days} onChange={e => setDays(e.target.value)}>
          {['7', '30', '90', '180'].map(d => <option key={d} value={d}>son {d} gün</option>)}
        </SelectField>
        <SelectField label="Problem başına koşu" value={repeats} onChange={e => setRepeats(e.target.value)}>
          {['1', '2', '3'].map(n => <option key={n} value={n}>{n}</option>)}
        </SelectField>
        <Field label="En çok Problem (1–30)" type="number" min={1} max={30} value={limit} onChange={e => setLimit(e.target.value)} />
        <SelectField label="Varyant" value={variant} onChange={e => setVariant(e.target.value)}>
          <option value="default">varsayılan</option>
          <option value="contextPack">bağlam paketi (contextPack)</option>
          <option value="plain">B kapalı — oyun kitabı / hipotez durumu / kanıt kapısı yok (plain)</option>
          {/* CoSRE Faz E — hafıza ölçümü: aynı pencerede varsayılan + noMemory iki rapor. */}
          <option value="noMemory">ekip notları kapalı (noMemory)</option>
          {/* v0.10.2477 — köken / etki / onaylı benzer vaka adımlarının ölçümü. */}
          <option value="noKnown">platform bilgisi kapalı — köken / etki / benzer vaka yok (noKnown)</option>
        </SelectField>
        <Button variant="primary" size="sm" loading={busy} disabled={busy || !enabled} onClick={() => { void start(); }}
          title={enabled ? 'Etiketli Problem\'leri kendi pencerelerinde yeniden koştur ve yerel hakemle notla (düşük öncelik; hakem saatlik AI bütçesinin en çok yarısını kullanır)' : 'AI SRE kapalı'}>
          Replay başlat
        </Button>
        <Button variant="secondary" size="sm" disabled={busy} onClick={() => { void toggleWeekly(); }}
          title="Haftalık zamanlanmış rapor (son raporun üstünden 7 gün geçince; varsayılan kapalı)">
          {weekly ? `Haftalık: açık (${schedule?.days} gün · ${schedule?.repeats} koşu) — kapat` : 'Haftalık: kapalı — aç'}
        </Button>
      </Row>
      {err && <p className="field-error" role="alert">{err}</p>}
    </div>
  );
}

/** Sürüm eğilimi: başlatan sürüm başına ortalama puan (Sparkline, 0–100). */
function VersionTrendTile({ reports }: { reports?: SREReplayListItem[] }) {
  const pts = useMemo(() => versionTrend(reports ?? []), [reports]);
  const first = pts[0];
  const last = pts[pts.length - 1];
  return (
    <StatTile label="Sürüm eğilimi (puan)">
      {pts.length >= 2
        ? <Sparkline values={pts.map(p => p.score)} width={140} height={24} domainMax={100}
            title={pts.map(p => `${p.build}: ${scoreText(p.score)} (${p.reports} rapor)`).join(' · ')} />
        : <span className="mono">{last ? scoreText(last.score) : '—'}</span>}{' '}
      <span className="field-hint">
        {first && last ? (first === last ? last.build : `${first.build} → ${last.build} · ${pts.length} sürüm`) : 'sürüm damgalı rapor yok'}
      </span>
    </StatTile>
  );
}

/** İki raporun karşılaştırması: aday = seçili rapor, taban = seçilen (varsayılan bir önceki). */
function ComparePanel({ reports, candId }: { reports: SREReplayListItem[]; candId: string }) {
  const [base, setBase] = useState(() => defaultBase(reports, candId));
  const q = useSREReplayCompare(base, candId);
  const c = q.data;
  const rows = c?.items;
  const dt = useDataTable<SREReplayCompareItem>({ storageKey: 'ai-sre-replay-compare', columns: CMP_COLS, rows: rows ?? [] });
  const others = reports.filter(r => r.reportId !== candId);
  return (
    <>
      <div className="ov-card-h sre-eval-sec">
        <h3>Karşılaştır</h3>
        <span className="ov-sub">seçili rapor (aday) ↔ taban · Problem'ler problemId ile eşlenir · fark = notlu koşu ortalaması (≥1 puan iyileşti / geriledi)</span>
      </div>
      <Row gap={3} wrap>
        <SelectField label="Taban rapor" value={base} onChange={e => setBase(e.target.value)}>
          <option value="">— seçin —</option>
          {others.map(r => <option key={r.reportId} value={r.reportId}>{tsLong(r.createdAt)} · {buildText(r)} · {reportMode(r)}</option>)}
        </SelectField>
      </Row>
      {c && (
        <div className="stat-grid">
          <StatTile label="Puan farkı" tone={c.scoreDelta != null && c.scoreDelta < 0 ? 'warn' : undefined}>
            <span className="mono">{deltaText(c.scoreDelta)}</span>{' '}
            <span className="field-hint">{scoreText(c.base.summary.score)} → {scoreText(c.cand.summary.score)}</span>
          </StatTile>
          <StatTile label="Erişim farkı">
            <span className="mono">{deltaText(c.reachDelta, true)}</span>{' '}
            <span className="field-hint">{pctText(c.base.summary.reachRate)} → {pctText(c.cand.summary.reachRate)}</span>
          </StatTile>
          <StatTile label="Dağılım farkı">
            <span className="dt-trunc">{GRADE_ORDER.map(g => (
              <Badge key={g} tone={GRADE_TONE[g]} className="sre-eval-hyp">{GRADE_LABEL[g]} {deltaText(c.distDelta[g] ?? 0)}</Badge>
            ))}</span>
          </StatTile>
          <StatTile label="Problem hükmü" tone={c.regressed > 0 ? 'warn' : undefined}>
            <span className="mono">{c.improved} iyileşti · {c.regressed} geriledi · {c.same} aynı</span>{' '}
            <span className="field-hint">{c.ungraded} notsuz · {c.onlyBase} yalnız tabanda · {c.onlyCand} yalnız adayda</span>
          </StatTile>
          <StatTile label="Yol farkı (medyan)">
            <span className="mono">{deltaText(c.medToolCallsDelta)} okuma · {deltaText(c.medRoundsDelta)} dalga · {deltaText(c.medWastedDelta)} boşa</span>{' '}
            <span className="field-hint">tavan {deltaText(c.capHitsDelta)} · sürüm {buildText(c.base)} → {buildText(c.cand)}</span>
          </StatTile>
        </div>
      )}
      {base && (
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.length === 0
                ? <DataTableState dt={dt} kind={q.isError ? 'error' : rows === undefined ? 'loading' : 'empty'} message="İki raporda Problem yok" />
                : dt.sortedRows.map(i => (
                  <tr key={i.problemId}>
                    <DataTableCell dt={dt} col="problem" row={i}>
                      <Link to={`/problems?problem=${encodeURIComponent(i.problemId)}`} onClick={stopRowClick} className="mono">{i.problemId}</Link>
                    </DataTableCell>
                    <DataTableCell dt={dt} col="service" row={i} value={i.service || null} title={i.cause} />
                    <DataTableCell dt={dt} col="verdict" row={i}>
                      <Badge tone={VERDICT_TONE[i.verdict]}>{VERDICT_LABEL[i.verdict] ?? i.verdict}</Badge>
                    </DataTableCell>
                    <DataTableCell dt={dt} col="base" row={i} value={i.baseMean == null ? null : scoreText(i.baseMean)} />
                    <DataTableCell dt={dt} col="cand" row={i} value={i.candMean == null ? null : scoreText(i.candMean)} />
                    <DataTableCell dt={dt} col="delta" row={i} value={i.delta == null ? null : deltaText(i.delta)} />
                    <DataTableCell dt={dt} col="grades" row={i}
                      value={`${i.baseGrades.map(g => GRADE_LABEL[g] ?? g).join(', ') || '—'} → ${i.candGrades.map(g => GRADE_LABEL[g] ?? g).join(', ') || '—'}`} />
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
