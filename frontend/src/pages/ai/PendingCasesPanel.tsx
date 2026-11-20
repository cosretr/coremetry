// PendingCasesPanel — v0.10.1475 — /ai "Onay bekleyen vakalar". AI SRE
// doğruluk ölçümü (SREEvalPanel) yalnız insan ONAYLI vakaları okur; taslaklar
// Problem sayfalarında tek tek bekliyordu. Bu panel taslakları tek listede
// verir; satır açılımı Problem kartının AYNI formu (IncidentCaseCard
// ConfirmForm — çatal yok) ve AYNI onay ucu (POST /api/cases/{id}/confirm,
// audit orada).
//
//   - Sayaç: onaylı / taslak (problem vakaları — eval yalnız onları sayar),
//     bağımsız onay sayısı, ≥N eşiği ipucu.
//   - Tablo: DataTable, sunucu-sayfalı → resize-only (tablo standardı).
//   - Akış: Onayla → satır düşer, sıradaki açılır; "Atla" yalnız bu oturumda
//     geçer (sunucuya hiçbir şey yazılmaz). Gürültü = formdaki kategori,
//     yine Onayla ister. Hiçbir şey otomatik onaylanmaz; 👍/👎 bu panele girmez.
//   - Viewer satırı açar, öneriyi salt-okunur görür.
import { Fragment, useState } from 'react';
import { Link } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { useAuth } from '@/components/AuthProvider';
import { Button, DisclosureButton, SegmentedControl, SelectField, StatTile } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { usePendingCases } from '@/lib/queries/cases';
import { keys } from '@/lib/queries/keys';
import { tsCompact, tsLong } from '@/lib/utils';
import type { IncidentCase, PendingCaseSource } from '@/lib/types';
import { CaseSignals, ConfirmForm } from '@/features/anomalies/IncidentCaseCard';
import { caseHref } from '@/features/anomalies/incidentCaseView';
import { evalReadinessHint, isoToNs, nextCaseId, pendingCounterText, suggestionSourceLabel } from './pendingCasesView';

const PAGE_SIZE = 25;
const DAY_RUNGS = [7, 30, 90, 180, 365];

const COLS: ColumnDef<IncidentCase>[] = [
  { id: 'ref',       label: 'Kaynak',       width: 150, minWidth: 120, priority: 1 },
  { id: 'service',   label: 'Servis',       width: 150, minWidth: 110, priority: 1 },
  { id: 'title',     label: 'Başlık',       width: 220, minWidth: 140, priority: 2 },
  { id: 'suggested', label: 'CoSRE önerisi (doğrulanmadı)', flex: true, minWidth: 220, priority: 1 },
  { id: 'from',      label: 'Öneri kaynağı', width: 110, minWidth: 90, priority: 3 },
  { id: 'opened',    label: 'Açıldı',       width: 130, minWidth: 110, priority: 3 },
  { id: 'closed',    label: 'Kapandı',      width: 130, minWidth: 110, priority: 3 },
  { id: 'actions',   label: '',             width: 80, minWidth: 70, priority: 1 },
];

export function PendingCasesPanel() {
  const { user } = useAuth();
  const isEditor = user?.role === 'admin' || user?.role === 'editor';
  const qc = useQueryClient();
  const [source, setSource] = useState<PendingCaseSource>('problem');
  const [days, setDays] = useState(90);
  const [page, setPage] = useState(0);
  const [open, setOpen] = useState<string | null>(null);
  const [skipped, setSkipped] = useState<ReadonlySet<string>>(() => new Set());
  const [done, setDone] = useState<ReadonlySet<string>>(() => new Set());

  const q = usePendingCases({ source, days, offset: page * PAGE_SIZE, limit: PAGE_SIZE });
  const data = q.data;
  // Onaylanan satır sunucu cevabı tazelenene dek de görünmesin.
  const rows = (data?.items ?? []).filter(c => !done.has(c.caseId));
  const hasMore = !!data && data.offset + data.items.length < data.total;
  const dt = useDataTable<IncidentCase>({
    storageKey: 'ai-pending-cases', columns: COLS, rows,
    server: { page, pageSize: PAGE_SIZE, hasMore, onPage: setPage },
  });
  const locked = q.isPlaceholderData; // önceki süzgecin satırı onaylanmasın

  const resetPaging = () => { setPage(0); setOpen(null); };
  const advance = (from: string, skip: ReadonlySet<string>) => setOpen(nextCaseId(rows, from, skip));
  const onConfirmed = (c: IncidentCase) => {
    const nextDone = new Set(done).add(c.caseId);
    setDone(nextDone);
    advance(c.caseId, new Set([...skipped, ...nextDone]));
    void qc.invalidateQueries({ queryKey: keys.cases.all });
  };
  const skip = (id: string) => {
    const next = new Set(skipped).add(id);
    setSkipped(next);
    advance(id, new Set([...next, ...done]));
  };

  const counts = data?.counts;
  const ready = counts ? evalReadinessHint(counts, data.evalMinConfirmed) : null;

  return (
    <div className="card sre-eval" data-testid="pending-cases">
      <div className="ov-card-h">
        <h3>Onay bekleyen vakalar</h3>
        <span className="ov-sub">CoSRE'nin kapanan olaylar için kurduğu taslaklar — onaylanana dek olgu sayılmaz, doğruluk ölçümüne girmez</span>
      </div>
      <div className="ov-card-b">
        <div className="stat-grid">
          <StatTile label="Problem vakaları" tone={ready && !ready.ready ? 'warn' : undefined}>
            <span className="mono" data-testid="pending-counter">{counts ? pendingCounterText(counts) : '—'}</span>{' '}
            <span className="field-hint">{counts ? `son ${data.days} gün · exception: onaylı ${counts.confirmedException} / taslak ${counts.draftException}` : ''}</span>
          </StatTile>
          <StatTile label="Bağımsız onay (manşet)">
            <span className="mono">{counts ? `${counts.independentProblem}${counts.independentCapped ? '+' : ''}` : '—'}</span>{' '}
            <span className="field-hint">öneriyi düzelterek onaylanan problem vakası</span>
          </StatTile>
        </div>
        {ready && <p className="field-hint" data-testid="pending-eval-hint">{ready.text}.</p>}
        <p className="field-hint">
          Öneriyi <b>düzelterek</b> onaylamak manşet ölçüye (bağımsız vakalar) sayılır; öneriyi aynen kabul etmek yalnız tüm-vakalar ölçüsüne girer.
          Emin değilseniz atlayın — yanlış onay ölçümü bozar.
          {!isEditor && ' Salt-okunur: onay editor ya da admin rolü ister.'}
        </p>

        <div className="pending-cases-counter">
          <SegmentedControl aria-label="Vaka kaynağı" value={source} size="sm"
            onChange={v => { setSource(v); resetPaging(); }}
            options={[{ value: 'problem', label: 'Problem' }, { value: 'exception', label: 'Exception' }, { value: 'all', label: 'Tümü' }]} />
          <SelectField label="Açılış penceresi" value={String(days)} onChange={e => { setDays(Number(e.target.value)); resetPaging(); }}>
            {DAY_RUNGS.map(d => <option key={d} value={d}>son {d} gün</option>)}
          </SelectField>
          <span className="field-hint">
            {data ? `${data.total} taslak${data.total > PAGE_SIZE ? ` · ${data.offset + 1}–${Math.min(data.total, data.offset + PAGE_SIZE)}` : ''}` : ''}
          </span>
        </div>

        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {rows.length === 0
                ? <DataTableState dt={dt} {...(q.isError ? { kind: 'error' as const, message: 'Vakalar okunamadı.', onRetry: () => { void q.refetch(); } }
                  : data === undefined ? { kind: 'loading' as const }
                  : { kind: 'empty' as const, message: 'Bu pencerede onay bekleyen vaka yok — CoSRE kapanan problem / çözülen exception grubu için taslak kurar' })} />
                : rows.map(c => {
                  const expanded = open === c.caseId;
                  const opened = isoToNs(c.openedAt);
                  const closed = isoToNs(c.closedAt);
                  return (
                    <Fragment key={c.caseId}>
                      <tr className={skipped.has(c.caseId) ? 'pending-cases-skipped' : undefined} data-testid="pending-case-row">
                        <DataTableCell dt={dt} col="ref" row={c} title={c.sourceId}>
                          <DisclosureButton expanded={expanded} aria-label={`${c.title || c.caseId} onay formu`}
                            onClick={() => setOpen(expanded ? null : c.caseId)} style={{ marginRight: 4 }} />
                          <Link to={caseHref(c)} className="mono">{c.source === 'exception' ? 'Exception' : 'Problem'} {c.sourceId.slice(0, 10)}</Link>
                        </DataTableCell>
                        <DataTableCell dt={dt} col="service" row={c} value={c.service || null} />
                        <DataTableCell dt={dt} col="title" row={c} value={c.title || null} title={c.title || undefined} />
                        <DataTableCell dt={dt} col="suggested" row={c} value={c.suggestedCause || null} title={c.suggestedCause || undefined} />
                        <DataTableCell dt={dt} col="from" row={c} value={suggestionSourceLabel(c.suggestedSource)} />
                        <DataTableCell dt={dt} col="opened" row={c} value={tsCompact(opened)} title={tsLong(opened)} className="mono" />
                        <DataTableCell dt={dt} col="closed" row={c} value={tsCompact(closed)} title={tsLong(closed)} className="mono" />
                        <DataTableCell dt={dt} col="actions" row={c}>
                          {isEditor && !skipped.has(c.caseId)
                            ? <Button variant="ghost" size="sm" onClick={() => skip(c.caseId)} title="Bu oturumda geç — hiçbir şey yazılmaz">Atla</Button>
                            : <span className="cell-empty">{skipped.has(c.caseId) ? 'atlandı' : ''}</span>}
                        </DataTableCell>
                      </tr>
                      {expanded && (
                        <tr data-testid="pending-case-form">
                          <td colSpan={dt.shownColumns.length} className="pending-cases-form">
                            <CaseSignals c={c} />
                            {locked
                              ? <p className="field-hint">Liste yenileniyor…</p>
                              : <ConfirmForm c={c} categories={data?.categories ?? []} onConfirmed={onConfirmed} />}
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
            </tbody>
          </table>
        </div>
        {(page > 0 || hasMore) && (
          <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
            <Button variant="secondary" size="sm" disabled={page === 0} onClick={() => { setPage(p => Math.max(0, p - 1)); setOpen(null); }}>← Önceki</Button>
            <Button variant="secondary" size="sm" disabled={!hasMore} onClick={() => { setPage(p => p + 1); setOpen(null); }}>Sonraki →</Button>
          </div>
        )}
      </div>
    </div>
  );
}
