// FailedTurnsPanel — v0.10.2468 (operatör onayı 2026-11-22: "CoSRE istemlerini
// körlemesine değiştirmeyi bırak"). /ai admin: örtük sinyallerle işaretlenen
// CoSRE turları — İNCELEME KUYRUĞU, etiket değil (👍/👎 girdi değil). Tablo:
// ne zaman, kim, soru, gerekçeler, tura bağlantı; ikinci tablo haftalık
// benzer soru kümeleri. "Eval'e ekle" turu operatörün yazdığı beklentiyle
// "Gerçek sorular" değerlendirme kümesine terfi ettirir (Ayarlar › AI ›
// Değerlendirme; koşu yalnız oradaki düğmeyle). Anonimleştirme yalnız
// operatör işaretlerse.
import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import { SegmentedControl } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTableStateProps } from '@/components/ui/DataTable';
import { Button } from '@/components/shadcn/button';
import { CheckboxField, Field, TextareaField } from '@/components/coremetry/Field';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/coremetry/overlay';
import { useFailedTurns, usePromoteTurn } from '@/lib/queries';
import { tsLong } from '@/lib/utils';
import type { FailedCluster, FailedTurn, FailedTurnsView } from '@/lib/types';
import { emptyPromoteForm, failedSummary, promoteBody, reasonLabel, reasonsText, turnHref, type PromoteForm } from '@/pages/ai/failedTurnsView';

const TURN_COLS: ColumnDef<FailedTurn>[] = [
  { id: 'at',       label: 'Ne zaman', sortValue: t => t.at, width: 165, tone: () => 'faint' },
  { id: 'who',      label: 'Kim',      sortValue: t => t.userEmail ?? '', naturalDir: 'asc', width: 190, tone: () => 'faint' },
  { id: 'question', label: 'Soru',     sortValue: t => t.question, naturalDir: 'asc', flex: true },
  { id: 'reasons',  label: 'Gerekçe',  sortValue: t => reasonsText(t.reasons), naturalDir: 'asc', width: 240 },
  { id: 'action',   label: '',         width: 150 },
];

const CLUSTER_COLS: ColumnDef<FailedCluster>[] = [
  { id: 'week',    label: 'Hafta',     sortValue: c => c.week, width: 100, tone: () => 'faint' },
  { id: 'label',   label: 'Küme',      sortValue: c => c.label, naturalDir: 'asc', flex: true },
  { id: 'count',   label: 'Tur',       sortValue: c => c.count, numeric: true, width: 70 },
  { id: 'users',   label: 'Kullanıcı', sortValue: c => c.users, numeric: true, width: 90 },
  { id: 'reasons', label: 'Gerekçe',   sortValue: c => c.reasons.map(r => r.reason).join(','), naturalDir: 'asc', width: 260, tone: () => 'faint' },
];

export function FailedTurnsPanel() {
  const [days, setDays] = useState<1 | 7 | 30>(7);
  const q = useFailedTurns(days);
  // undefined = yükleniyor, null = hata (RouterGapsPanel'in durum sözleşmesi).
  const data: FailedTurnsView | null | undefined = q.isError ? null : q.data?.days === days ? q.data : undefined;
  const [open, setOpen] = useState<FailedTurn | null>(null);
  const [flash, setFlash] = useState<string | null>(null);
  const turnDt = useDataTable<FailedTurn>({ storageKey: 'ai-failed-turns', columns: TURN_COLS, rows: data?.turns ?? [] });
  const clusterDt = useDataTable<FailedCluster>({ storageKey: 'ai-failed-clusters', columns: CLUSTER_COLS, rows: data?.clusters ?? [] });
  const state: Omit<DataTableStateProps<FailedTurn>, 'dt'> =
    data === undefined ? { kind: 'loading' }
    : data === null ? { kind: 'error' }
    : { kind: 'empty', message: 'Bu pencerede işaretlenen tur yok.' };
  const clusterState: Omit<DataTableStateProps<FailedCluster>, 'dt'> =
    data === undefined ? { kind: 'loading' } : data === null ? { kind: 'error' } : { kind: 'empty', message: 'Küme yok.' };
  const byReason = useMemo(() => (data?.byReason ?? []).map(r => `${reasonLabel(r.reason)} ${r.count}`).join(' · '), [data]);

  return (
    <div className="card" style={{ marginTop: 16 }} data-failed-turns="">
      <div className="ov-card-h">
        <h3>Başarısız turlar</h3>
        <span className="ov-sub">
          örtük sinyallerle işaretlenen CoSRE turları — inceleme kuyruğu, etiket değil; oylar girdi değil
        </span>
        <span style={{ marginLeft: 'auto' }}>
          <SegmentedControl size="sm" aria-label="Gün penceresi" value={String(days)}
            onChange={v => setDays(v === '30' ? 30 : v === '1' ? 1 : 7)}
            options={([1, 7, 30] as const).map(d => ({ value: String(d), label: `${d}g` }))} />
        </span>
      </div>
      <div className="ov-card-b">
        {data && (
          <div style={{ fontSize: 11.5, color: 'var(--text2)', marginBottom: 8 }}>
            {failedSummary(data.total, data.scanned, data.days, data.truncated)}
            {byReason && <> {byReason}.</>}
          </div>
        )}
        {flash && <div className="field-hint" role="status" style={{ marginBottom: 8 }}>{flash}</div>}
        <div className="table-wrap">
          <table {...turnDt.tableProps}>
            <DataTableColgroup dt={turnDt} />
            <DataTableHead dt={turnDt} />
            <tbody>
              {turnDt.sortedRows.length === 0 ? <DataTableState dt={turnDt} {...state} /> : turnDt.sortedRows.map(t => (
                <tr key={t.exchangeId}>
                  <DataTableCell dt={turnDt} col="at" row={t} value={tsLong(Date.parse(t.at) * 1e6)} className="mono" />
                  <DataTableCell dt={turnDt} col="who" row={t} value={t.userEmail || null} title={t.userEmail || undefined} />
                  <DataTableCell dt={turnDt} col="question" row={t} value={t.question}
                    title={[t.question, t.nextQuestion ? `sonraki mesaj: ${t.nextQuestion}` : '', t.errorMsg ?? ''].filter(Boolean).join('\n')}>
                    <Link to={turnHref(t, days)}>{t.question || '—'}</Link>
                  </DataTableCell>
                  <DataTableCell dt={turnDt} col="reasons" row={t} value={reasonsText(t.reasons)} />
                  <DataTableCell dt={turnDt} col="action" row={t} value={null}>
                    <Button size="sm" variant="outline" onClick={() => { setFlash(null); setOpen(t); }}>Eval'e ekle</Button>
                  </DataTableCell>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div style={{ fontSize: 12, fontWeight: 600, margin: '16px 0 6px' }}>Haftalık benzer soru kümeleri</div>
        <div className="table-wrap">
          <table {...clusterDt.tableProps}>
            <DataTableColgroup dt={clusterDt} />
            <DataTableHead dt={clusterDt} />
            <tbody>
              {clusterDt.sortedRows.length === 0 ? <DataTableState dt={clusterDt} {...clusterState} /> : clusterDt.sortedRows.map(c => (
                <tr key={`${c.week}:${c.label}:${c.exchangeIds[0] ?? ''}`}>
                  <DataTableCell dt={clusterDt} col="week" row={c} value={c.week} className="mono" />
                  <DataTableCell dt={clusterDt} col="label" row={c} value={c.label} title={c.samples.join('\n')} />
                  <DataTableCell dt={clusterDt} col="count" row={c} value={c.count} />
                  <DataTableCell dt={clusterDt} col="users" row={c} value={c.users} />
                  <DataTableCell dt={clusterDt} col="reasons" row={c} value={c.reasons.map(r => `${reasonLabel(r.reason)} ${r.count}`).join(' · ')} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      {open && (
        <PromoteDrawer turn={open} onClose={() => setOpen(null)}
          onDone={caseId => { setOpen(null); setFlash(`Eval setine eklendi: ${caseId} — Ayarlar › AI › Değerlendirme › Gerçek sorular ile koş.`); }} />
      )}
    </div>
  );
}

function PromoteDrawer({ turn, onClose, onDone }: { turn: FailedTurn; onClose: () => void; onDone: (caseId: string) => void }) {
  const [f, setF] = useState<PromoteForm>(() => emptyPromoteForm(turn));
  const promote = usePromoteTurn();
  const busy = promote.isPending;
  const err = promote.error ? (promote.error instanceof Error ? promote.error.message : String(promote.error)) : null;
  const set = <K extends keyof PromoteForm>(k: K, v: PromoteForm[K]) => setF(prev => ({ ...prev, [k]: v }));
  const submit = () => promote.mutate(promoteBody(turn, f), { onSuccess: r => onDone(r.caseId) });
  return (
    <Sheet open onOpenChange={o => { if (!o) onClose(); }}>
      <SheetContent side="right" aria-label="Eval setine ekle" aria-describedby={undefined}
        className="w-full gap-0 overflow-y-auto p-0 sm:w-[560px] sm:max-w-[560px]">
      <SheetHeader className="border-b border-(--divider) px-4 py-3 pr-12">
        <SheetTitle className="text-[length:var(--fs-md)] font-semibold">Eval setine ekle</SheetTitle>
      </SheetHeader>
      <div className="flex flex-col gap-2.5 px-4 py-4">
        <div className="field-hint" style={{ marginTop: 0 }}>
          Gerekçe: {reasonsText(turn.reasons)}{turn.nextQuestion ? ` · sonraki mesaj: “${turn.nextQuestion}”` : ''}
        </div>
        <TextareaField label="Soru" rows={3} value={f.question} onChange={e => set('question', e.target.value)} />
        <Field label="Beklenen kademe" placeholder="guided | loop | intent | clarify" value={f.expectTier} onChange={e => set('expectTier', e.target.value)} />
        <Field label="Beklenen rota" placeholder="service_health, log_errors, messaging_health …" value={f.expectRoute} onChange={e => set('expectRoute', e.target.value)} />
        <Field label="Araçlardan biri (virgülle)" hint="v2 kataloğundaki araç adları" value={f.tools} onChange={e => set('tools', e.target.value)} />
        <TextareaField label="Cevapta geçmeli (satır başına bir ifade)" rows={2} value={f.mustContain} onChange={e => set('mustContain', e.target.value)} />
        <TextareaField label="Cevapta geçmemeli" rows={2} value={f.mustNotContain} onChange={e => set('mustNotContain', e.target.value)} />
        <CheckboxField label="Servis adlarını yer tutucuya çevir (yalnız seçersen; ham cevap saklanmaz)"
          checked={f.anonymize} onCheckedChange={v => set('anonymize', v)} />
        {f.anonymize && (
          <TextareaField label="Ad => yer tutucu (satır başına)" rows={2} placeholder="gercek-servis => checkout"
            hint="Tek çift verilirse koşucu yer tutucuyu en yoğun canlı servise bağlar." value={f.anonPairs} onChange={e => set('anonPairs', e.target.value)} />
        )}
        <TextareaField label="Not" rows={2} value={f.note} onChange={e => set('note', e.target.value)} />
        {err && <div className="is-err field-hint" role="alert">{err}</div>}
        <div style={{ display: 'flex', gap: 8 }}>
          <Button onClick={submit} loading={busy} disabled={busy || !f.question.trim()}>Ekle</Button>
          <Button variant="outline" onClick={onClose}>Vazgeç</Button>
        </div>
      </div>
      </SheetContent>
    </Sheet>
  );
}
