// ProblemRepeatBlock — v0.10.2281 (entity grafı ile Problem birleştirme S3,
// docs/problems/entity-graph-merge-design.md §6): nötr "sık tekrar eden"
// çipi (renk yok; ipucu gölge ölçüm olduğunu ve bildirimin değişmediğini
// söyler) ve detaydaki "Tekrar" bloğu (tür, son 24 sa, son 7 gün, şiddet
// kıyası, "kural uygulansaydı", önceki bölümler ≤ 5). Yalnız çizer;
// cümleler lib/problemGraph (SAF, EN/TR). Mevcut "yinelenen" çipi yerinde.
import { Badge } from '@/components/shadcn/badge';
import { episodeText, frequentChipTitle, repeatRows, type FrequentVerdict, type GraphT } from '@/lib/problemGraph';

// v0.10.2342 (S5) — enforce: tutma uygulanıyor; ipucu gölge yerine bunu söyler.
export function FrequentChip({ f, t, enforce }: { f: { prior24h: number; days7d: number }; t: GraphT; enforce?: boolean }) {
  return (
    <Badge variant="outline" className="text-[11px] font-normal text-muted-foreground" title={frequentChipTitle(f, t, enforce)} data-testid="pg-frequent-chip">
      {t('pg.freq.chip')}
    </Badge>
  );
}

export function RepeatBlock({ v, t, when }: { v: FrequentVerdict; t: GraphT; when: (ns: number) => string }) {
  const eps = (v.episodes ?? []).slice(0, 5);
  return (
    <div className="flex flex-col gap-2" data-testid="pg-repeat">
      <dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-3.5 gap-y-1 text-xs">
        {repeatRows(v, t).map(r => (
          <div key={r.k} className="contents">
            <dt className="text-muted-foreground">{r.k}</dt>
            <dd className="m-0 [overflow-wrap:anywhere]">{r.v}</dd>
          </div>
        ))}
      </dl>
      {eps.length > 0 && (
        <div className="flex flex-col gap-0.5 text-xs">
          <span className="text-muted-foreground">{t('pg.rep.prev')}</span>
          <ul className="m-0 list-none p-0 tabular-nums" data-testid="pg-repeat-episodes">
            {eps.map(e => <li key={e.id}>{episodeText(e, t, when)}</li>)}
          </ul>
        </div>
      )}
      {v.partial && <p className="m-0 text-xs text-muted-foreground">{t('pg.rep.partial')}</p>}
    </div>
  );
}
