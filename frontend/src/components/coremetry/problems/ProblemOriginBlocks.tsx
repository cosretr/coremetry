// ProblemOriginBlocks — v0.10.2279 (entity grafı ile Problem birleştirme S2,
// docs/problems/entity-graph-merge-design.md §3–§5): olası köken satırı,
// "Etki alanı" tablosu, "Köken kanıtı" listesi. Yalnız çizer; cümleler
// lib/problemGraph (SAF, kural kimliğinden; EN/TR i18n). Salt okunur
// önizleme — hiçbir şeyi değiştirmez.
//
// Etki alanı: gerekçeli statik tablo (tablo standardı T1): en çok 3 sabit
// satır (etkilenen · kökeni çağıran · etkilenenleri çağıran), sıralama /
// süzme yok, satırlar tıklanmaz.
import { Badge } from '@/components/shadcn/badge';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import {
  namesPlus, originConfText, originLineText, originRuleText, originSourceText,
  type GraphT, type ProblemGraphOrigin,
} from '@/lib/problemGraph';

/** Manşet altındaki tek cümlelik köken satırı + güven rozeti. */
export function OriginLine({ origin, t, clock }: { origin: ProblemGraphOrigin; t: GraphT; clock: (ns: number) => string }) {
  return (
    <p className="m-0 mb-2 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-[13px]" data-testid="pg-origin-line" title={t('pg.origin.title')}>
      <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{t('pg.origin.k')}</span>
      <span>{originLineText(origin, t, clock)}</span>
      <Badge variant={origin.confidence === 'strong' ? 'secondary' : 'outline'} className="text-[11px]" data-testid="pg-origin-conf">
        {originConfText(origin, t)}
      </Badge>
      <Badge variant="outline" className="text-[11px] text-muted-foreground">{t('pg.shadowBadge')}</Badge>
    </p>
  );
}

/** "Etki alanı" — etkilenen (ölçüldü) ve risk altında (çağırıyor, sapma yok). */
export function BlastTable({ origin, t }: { origin: ProblemGraphOrigin; t: GraphT }) {
  const b = origin.blast;
  if (!b) return null;
  const rows: { k: string; label: string; n: number; who: string }[] = [
    { k: 'aff', label: t('pg.blast.affected'), n: b.affected.length, who: b.affected.join(', ') || '—' },
  ];
  if (!b.off) {
    rows.push({ k: 'r1', label: t('pg.blast.risk1', { name: origin.name ?? '' }), n: b.atRisk1N, who: namesPlus(b.atRisk1, b.atRisk1N) || '—' });
    rows.push({ k: 'r2', label: t('pg.blast.risk2'), n: b.atRisk2N, who: namesPlus(b.atRisk2, b.atRisk2N) || '—' });
  }
  return (
    <div className="flex flex-col gap-2" data-testid="pg-blast">
      <Table aria-label={t('pg.blast.title')}>
        <TableHeader>
          <TableRow>
            <TableHead>{t('pg.blast.col.group')}</TableHead>
            <TableHead className="w-16 text-right">{t('pg.blast.col.n')}</TableHead>
            <TableHead>{t('pg.blast.col.who')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(r => (
            <TableRow key={r.k} data-testid="pg-blast-row">
              <TableCell className="whitespace-normal">{r.label}</TableCell>
              <TableCell className="text-right tabular-nums">{r.n}</TableCell>
              <TableCell className="whitespace-normal font-mono text-xs">{r.who}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {b.off && <p className="m-0 text-xs text-muted-foreground">{t('pg.blast.off')}</p>}
      {b.cut && <p className="m-0 text-xs text-muted-foreground">{t('pg.blast.cut')}</p>}
      {b.namesDrop && <p className="m-0 text-xs text-muted-foreground">{t('pg.blast.namesDrop')}</p>}
      <p className="m-0 text-xs text-muted-foreground">{t('pg.blast.note')}</p>
    </div>
  );
}

/** "Köken kanıtı" — kural sırasıyla: kimlik · sonuç cümlesi · kaynak. */
export function OriginEvidence({ origin, t, clock }: { origin: ProblemGraphOrigin; t: GraphT; clock: (ns: number) => string }) {
  return (
    <div className="flex flex-col gap-2">
      <ul className="m-0 flex list-none flex-col p-0" data-testid="pg-evidence">
        {origin.rules.map(r => (
          <li key={r.rule} className="grid grid-cols-[84px_minmax(0,1fr)] gap-x-2.5 border-b border-border py-1.5 text-xs last:border-b-0" data-testid="pg-evidence-row">
            <span className="pt-px font-mono text-[10.5px] text-muted-foreground">{r.rule}</span>
            <span>
              <span className={r.separated ? 'font-semibold' : 'text-muted-foreground'}>{originRuleText(r, t, clock)}</span>
              <span className="block text-[10.5px] text-muted-foreground">{originSourceText(r.source, t)}</span>
            </span>
          </li>
        ))}
      </ul>
      <p className="m-0 text-xs text-muted-foreground">{t('pg.ev.hypoNote')}</p>
    </div>
  );
}
