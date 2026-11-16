// ProblemGraphShadow — v0.10.2274 (entity grafı ile Problem birleştirme S1,
// docs/problems/entity-graph-merge-design.md §9): admin gölge raporu.
// MLSvcShadowCompare emsali: Anomalies › ML "Teknik detay"ın içinde kapalı
// bölüm; açılınca TEK istek (15 sn önbellek), yoklama yok. Yalnız admin
// (uç admin kapılı; diğer rollerde bölüm çizilmez). Hiçbir şey değiştirmez:
// bağlar yalnız ölçülür, Problem / bildirim / liste aynı kalır.
import { useState } from 'react';
import { useAuth } from '@/components/AuthProvider';
import { Empty, Spinner } from '@/components/Spinner';
import { useProblemGraphShadow } from '@/lib/queries';
import { graphShadowLines } from '@/lib/problemGraph';
import { fmtDateTime } from '@/lib/utils';
import { SearchX } from 'lucide-react';
import { t as tr, tf, useLang } from '@/lib/i18n';

export function ProblemGraphShadowSection() {
  const { user } = useAuth();
  const [open, setOpen] = useState(false);
  const lang = useLang();
  const t = (k: string) => tr(`pgShadow.${k}`, lang);
  const q = useProblemGraphShadow(open && user?.role === 'admin');
  if (user?.role !== 'admin') return null;
  return (
    <details data-testid="problem-graph-shadow" className="rounded-md border border-border px-3 py-2" onToggle={e => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">
        {t('title')}
      </summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-xs text-muted-foreground">
          {t('intro')}
        </p>
        {q.isLoading && <Spinner />}
        {q.error && <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title={t('error')}>{tr('mlSvc.retry', lang)}</Empty>}
        {q.data && !q.data.available && <p className="m-0 text-sm">{t('none')}</p>}
        {q.data?.available && (
          <>
            <div className="flex flex-col gap-0.5 text-sm" data-testid="problem-graph-shadow-lines">
              {graphShadowLines(q.data.report, lang).map(l => <div key={l}>{l}</div>)}
            </div>
            {q.data.largest.length > 0 && (
              <div className="flex flex-col gap-0.5 text-xs text-muted-foreground" data-testid="problem-graph-shadow-largest">
                <div className="font-semibold">{t('largest')}</div>
                {q.data.largest.map(g => (
                  <div key={g.ownerId}>
                    {g.ownerName} · {tf('pgShadow.entities', lang, { c: g.entities })} · {fmtDateTime(new Date(g.startedAt / 1e6))} · {Object.keys(g.rules).sort().map(k => `${k} ${g.rules[k]}`).join(', ')}{g.confidence ? tf('pgShadow.origin', lang, { o: g.origin || t('unclear'), conf: g.confidence }) : ''}
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </details>
  );
}
