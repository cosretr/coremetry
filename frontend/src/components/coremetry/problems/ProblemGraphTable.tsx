// ProblemGraphTable — v0.10.2275 (entity grafı ile Problem birleştirme S1,
// docs/problems/entity-graph-merge-design.md §4): "Neden tek Problem"
// bölümünün gövdesi. Yalnız çizer; veri ve metin features/anomalies
// ProblemGraphSection + lib/problemGraph (SAF, kural kimliğinden cümle).
//
// Statik tablo (tablo standardı T1, gerekçeli): ≤ 25 sabit satır, sıra =
// başlangıç sırası (sunucu), sıralama / süzme denetimi YOK (taslak notu);
// shadcn Table. Satırlar tıklanmaz. Bağlanmayanlar bloğu ≤ 3 satır.
import { Badge } from '@/components/shadcn/badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import {
  graphLinkText, graphPathText, graphReasonText, graphTypeLabel,
  type GraphT, type ProblemGraphMember, type ProblemGraphNearMiss,
} from '@/lib/problemGraph';

function RuleChip({ id }: { id: string }) {
  return <Badge variant="outline" className="ml-1 font-mono text-[11px]">{id}</Badge>;
}

function ReasonCell({ m, t }: { m: ProblemGraphMember; t: GraphT }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span>{graphLinkText(m, t)}{m.owner ? <RuleChip id={t('pg.owner')} /> : m.link && <RuleChip id={m.link} />}</span>
      {m.graphMissing && (
        <span className="text-xs text-muted-foreground" data-testid="pg-graph-missing">
          <Badge variant="secondary" className="mr-1 text-[11px]">{t('pg.graphMissing')}</Badge>
          {t('pg.graphMissingNote')}
        </span>
      )}
    </div>
  );
}

/** Üyeler tablosu (başlangıç sırası) + altında "bağlanmadı" not satırları. */
export function ProblemGraphMembers({ members, near, t, clock, truncated }: {
  members: ProblemGraphMember[]; near: ProblemGraphNearMiss[]; t: GraphT; clock: (ns: number) => string; truncated?: boolean;
}) {
  return (
    <div className="flex flex-col gap-2" data-testid="pg-members">
      <Table aria-label={t('pg.title')}>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10">{t('pg.col.order')}</TableHead>
            <TableHead>{t('pg.col.entity')}</TableHead>
            <TableHead>{t('pg.col.what')}</TableHead>
            <TableHead className="w-20">{t('pg.col.onset')}</TableHead>
            <TableHead>{t('pg.col.reason')}</TableHead>
            <TableHead>{t('pg.col.path')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {members.map((m, i) => (
            <TableRow key={m.id} data-testid="pg-member-row">
              <TableCell className="tabular-nums">{i + 1}</TableCell>
              <TableCell>
                <div className="font-medium">{m.name}</div>
                <div className="text-xs text-muted-foreground">{graphTypeLabel(m.type, t)}{m.owner ? ` · ${t('pg.owner')}` : ''}</div>
              </TableCell>
              <TableCell className="whitespace-normal">{m.title || m.ruleId || '—'}</TableCell>
              <TableCell className="tabular-nums">{clock(m.startedAt)}</TableCell>
              <TableCell className="whitespace-normal"><ReasonCell m={m} t={t} /></TableCell>
              <TableCell className="whitespace-normal font-mono text-xs">{graphPathText(m)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {truncated && <p className="m-0 text-xs text-muted-foreground">{t('pg.truncated')}</p>}
      {near.map(n => (
        <p key={n.id} className="m-0 text-sm text-muted-foreground" data-testid="pg-near-note">
          {t('pg.near.row', { name: n.name, time: clock(n.startedAt), reason: graphReasonText(n, t) })}
          <RuleChip id={n.reason} />
        </p>
      ))}
    </div>
  );
}

/** Tek Problem: aynı pencerede açık olup bağlanmayanlar (≤ 3 satır). */
export function ProblemGraphNearMisses({ near, t, clock }: { near: ProblemGraphNearMiss[]; t: GraphT; clock: (ns: number) => string }) {
  return (
    <Table aria-label={t('pg.near.title')} data-testid="pg-near">
      <TableHeader>
        <TableRow>
          <TableHead>{t('pg.near.col.problem')}</TableHead>
          <TableHead className="w-20">{t('pg.near.col.onset')}</TableHead>
          <TableHead>{t('pg.near.col.reason')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {near.map(n => (
          <TableRow key={n.id}>
            <TableCell>
              <div className="font-medium">{n.name}</div>
              <div className="text-xs text-muted-foreground">
                {graphTypeLabel(n.type, t)}{n.groupSize && n.groupSize > 1 ? ` · ${t('pg.near.group', { n: n.groupSize })}` : ''}
              </div>
            </TableCell>
            <TableCell className="tabular-nums">{clock(n.startedAt)}</TableCell>
            <TableCell className="whitespace-normal">{graphReasonText(n, t)}<RuleChip id={n.reason} /></TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
