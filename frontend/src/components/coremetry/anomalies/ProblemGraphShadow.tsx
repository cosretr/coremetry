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

export function ProblemGraphShadowSection() {
  const { user } = useAuth();
  const [open, setOpen] = useState(false);
  const q = useProblemGraphShadow(open && user?.role === 'admin');
  if (user?.role !== 'admin') return null;
  return (
    <details data-testid="problem-graph-shadow" className="rounded-md border border-border px-3 py-2" onToggle={e => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">
        Problem birleştirme (entity grafı): gölge raporu
      </summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-xs text-muted-foreground">
          Servisler arası bağlar yalnız ölçülür; hiçbir Problem birleşmez, kapanmaz, bildirim ve liste değişmez. Sayılar son 24 saatte
          farklı bağ / Problem sayısıdır ve evaluator liderinin belleğindedir.
        </p>
        {q.isLoading && <Spinner />}
        {q.error && <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title="Rapor yüklenemedi">Bir süre sonra bölümü kapatıp yeniden açın.</Empty>}
        {q.data && !q.data.available && <p className="m-0 text-sm">Henüz gölge raporu yok (evaluator lideri ilk tikini yazmadı).</p>}
        {q.data?.available && (
          <>
            <div className="flex flex-col gap-0.5 text-sm" data-testid="problem-graph-shadow-lines">
              {graphShadowLines(q.data.report).map(l => <div key={l}>{l}</div>)}
            </div>
            {q.data.largest.length > 0 && (
              <div className="flex flex-col gap-0.5 text-xs text-muted-foreground" data-testid="problem-graph-shadow-largest">
                <div className="font-semibold">En büyük kümeler (şu an, en çok 20)</div>
                {q.data.largest.map(g => (
                  <div key={g.ownerId}>
                    {g.ownerName} · {g.entities} varlık · {fmtDateTime(new Date(g.startedAt / 1e6))} · {Object.keys(g.rules).sort().map(k => `${k} ${g.rules[k]}`).join(', ')}
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
