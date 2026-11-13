// AiEvalCandidates — v0.10.2111 (CoSRE kalite döngüsü D6, operatör
// kararları 2026-11-13): Değerlendirme sekmesinde "Aday vakalar". Gece kalite
// işi her gün en kötü 5 cevabı önerir; admin satır başına TEK eylemle
// ("Kabul") onaylar. Kabul edilen aday günlük koşuya (canlı v2 döngüsünden
// ajan vakası) ve "evalset indir" dosyasına girer; kabul edilmeyen `ttlDays`
// gün sonra listeden düşer. Adaylar CI kapısına girmez.
//
// Önce düz cümle, az sütun. Satır tıklanabilir değil (tek eylem düğmede);
// soru tam metni ve cevap satırın title'ında (insan gözden geçirir —
// maskeleme yok, operatör kararı).
import { useMemo, useState, type ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { useEvalCandidates, useAcceptEvalCandidate } from '@/lib/queries';
import type { EvalsetCandidate } from '@/lib/types';
import { FlashBox, humanize } from './shared';
import { candidateSentence, fmtDec2 } from './aiEval';

const COLS: ColumnDef<EvalsetCandidate>[] = [
  { id: 'day',      label: 'Gün',      sortValue: c => c.day, width: 110 },
  { id: 'question', label: 'Soru',     sortValue: c => c.question, naturalDir: 'asc', truncate: 'wrap', width: 380, flex: true },
  { id: 'signals',  label: 'Neden',    sortValue: c => c.signals.join(', '), naturalDir: 'asc', truncate: 'wrap', width: 220 },
  { id: 'action',   label: 'Durum',    sortValue: c => (c.accepted ? 1 : 0), width: 140 },
];

export function AiEvalCandidates() {
  const q = useEvalCandidates();
  const acceptM = useAcceptEvalCandidate();
  const [err, setErr] = useState<string | null>(null);
  const rows = useMemo(() => q.data?.candidates ?? [], [q.data]);
  const ttlDays = q.data?.ttlDays ?? 14;

  const state: DataTableStateInput<unknown> = q.isPending ? { kind: 'loading' }
    : q.isError ? { kind: 'error', message: humanize(q.error), onRetry: () => { void q.refetch(); } }
    : { kind: 'empty', message: 'Henüz aday yok — gece kalite işi bir günü puanlayınca o günün en kötü 5 cevabı burada görünür' };

  const accept = (id: string) => {
    setErr(null);
    acceptM.mutate(id, { onError: e => setErr(humanize(e)) });
  };

  const cell = (c: EvalsetCandidate, col: ColumnDef<EvalsetCandidate>): ReactNode => {
    switch (col.id) {
      case 'day': return c.day;
      case 'question': return <span title={`Puan ${fmtDec2(c.score)} · cevap: ${c.answer || '—'}`}>{c.question}</span>;
      case 'signals': return c.signals.length ? c.signals.join(', ') : undefined;
      case 'action': return c.accepted
        ? <span title={c.acceptedBy ? `${c.acceptedBy} · ${c.acceptedAt ?? ''}` : undefined}>Kabul edildi</span>
        : (
          <Button size="sm" variant="outline" onClick={() => accept(c.id)}
            loading={acceptM.isPending && acceptM.variables === c.id} disabled={acceptM.isPending}>Kabul</Button>
        );
      default: return undefined;
    }
  };

  return (
    <>
      <SectionHead title="Aday vakalar" meta={rows.length ? `${rows.length} aday` : undefined} />
      <p data-evs-cand-note="" className="mb-2 mt-0 text-[length:var(--fs-sm)] leading-normal text-(--text2)">
        {candidateSentence(rows, ttlDays)}
      </p>
      {err && <FlashBox kind="err">{err}</FlashBox>}
      <div className="table-wrap">
        <DataTable<EvalsetCandidate> storageKey="settings-ai-eval-candidates" columns={COLS} rows={rows}
          getRowKey={c => c.id} renderCell={cell} state={state} />
      </div>
    </>
  );
}
