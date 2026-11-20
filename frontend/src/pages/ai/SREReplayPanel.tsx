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
//   - Admin: "Replay başlat" (gün / koşu / Problem / varyant) + haftalık
//     zamanlama aç/kapa (varsayılan kapalı). Admin ekranı — çevrilmez.
import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Badge, Button, Field, Row, SelectField, StatTile } from '@/components/ui';
import { stopRowClick, rowActivation } from '@/lib/a11y';
import { tsLong, fmtNum } from '@/lib/utils';
import { keys } from '@/lib/queries/keys';
import { useSREReplayReport, useSREReplayReports } from '@/lib/queries/sreReplay';
import type { SREReplayItem, SREReplayListItem, SREReplaySchedule } from '@/lib/types';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { categoryLabel } from './sreEvalView';
import { GRADE_LABEL, GRADE_ORDER, GRADE_TONE, pctText, reachCell, reportMode, runLabel, runTitle, scoreText, scoreTrend } from './sreReplayView';

const REPORT_COLS: ColumnDef<SREReplayListItem>[] = [
  { id: 'when',     label: 'Başlatıldı', sortValue: r => r.createdAt, width: 165, minWidth: 140, priority: 1 },
  { id: 'window',   label: 'Etiket penceresi', sortValue: r => r.days, numeric: true, width: 120, minWidth: 100, priority: 2 },
  { id: 'state',    label: 'Durum', sortValue: r => r.state, naturalDir: 'asc', width: 100, minWidth: 80, priority: 1 },
  { id: 'problems', label: 'Problem', sortValue: r => r.summary.problems, numeric: true, width: 80, minWidth: 64, priority: 2 },
  { id: 'score',    label: 'Puan', sortValue: r => r.summary.score ?? -1, numeric: true, width: 80, minWidth: 64, priority: 1 },
  { id: 'reach',    label: 'Erişim', sortValue: r => r.summary.reachRate ?? -1, numeric: true, width: 80, minWidth: 64, priority: 1 },
  { id: 'unstable', label: 'Kararsız', sortValue: r => r.summary.unstable, numeric: true, width: 85, minWidth: 64, priority: 2 },
  { id: 'mode',     label: 'Kip', sortValue: r => reportMode(r), naturalDir: 'asc', flex: true, minWidth: 120, priority: 2 },
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
          <StatTile label="Koşudan koşuya" tone={sm && sm.unstable > 0 ? 'warn' : undefined}>
            <span className="mono">{sm ? `${sm.unstable}/${sm.multiRun}` : '—'}</span>{' '}
            <span className="field-hint">{sm ? `kararsız · ort. yayılım ${scoreText(sm.meanSpread)} · en çok ${sm.maxSpread}` : ''}</span>
          </StatTile>
          <StatTile label="Eğilim (eskiden yeniye)">
            <span className="mono">{reports ? scoreTrend(reports) : '—'}</span>
          </StatTile>
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
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
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
      variant: variant === 'contextPack' ? { contextPack: true } : {} });
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
