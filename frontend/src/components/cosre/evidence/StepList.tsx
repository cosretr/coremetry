// StepList — CoSRE v2 adım listesi (araç seti dilim 5; mockup onayı 2026-10-31).
// Cevabın ÜSTÜNDE; akarken açık, cevap bitince tek özet satırına katlanır
// (operatör açabilir). Kartlar varsayılan kapalı (StepCard). data-stop listenin
// altında tek satır.
// v0.10.1710 — v1 panelinin rozetleri (ön-yükleme / niyet / bütçe aşıldı) data-stop
// satırında; kartlar İlgili / İlgisiz için cevabın exchangeId'sini alır.
import { useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { cn } from '@/lib/cn';
import { EVIDENCE_STRINGS as T, STOP_LABELS } from './strings';
import { fmtMsTR, stopLine, type StepsView, type StopBadge } from './stepView';
import { StepCard } from './StepCard';

export function StepList({ view, live, exchangeId, badges = [] }: {
  view: StepsView; live: boolean;
  /** Bitmiş cevabın kimliği; yoksa (akış / arşiv) İlgili / İlgisiz devre dışı. */
  exchangeId?: string;
  badges?: readonly StopBadge[];
}) {
  // null = kullanıcı dokunmadı: akarken açık, bitince kapalı.
  const [touched, setTouched] = useState<boolean | null>(null);
  const open = touched ?? live;
  if (view.steps.length === 0 && !view.stop) return null;
  const elapsed = view.stop?.elapsed_ms ?? view.totalMs;
  return (
    <Collapsible open={open} onOpenChange={setTouched} data-cosre-steps="" data-open={open || undefined}
      className="cm-msg cm-msg--ai rounded-md border border-border">
      <CollapsibleTrigger asChild>
        <Button variant="ghost" size="sm" aria-expanded={open}
          className={cn('h-auto w-full justify-start gap-2 rounded-none px-3 py-2 font-normal', open && 'border-b border-border')}>
          <ChevronRight className={cn('size-3 text-muted-foreground transition-transform', open && 'rotate-90')} aria-hidden />
          <span className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
            {T.stepsTitle} · {T.calls(view.steps.length)}
          </span>
          {!open && view.stop && <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{T.stopPrefix} {STOP_LABELS[view.stop.reason]}</span>}
          <span className="ml-auto text-xs text-muted-foreground tabular-nums">{elapsed > 0 ? fmtMsTR(elapsed) : ''}</span>
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div role="list" aria-label={T.stepsListLabel}>
          {view.steps.map(s => <StepCard key={s.id} v={s} exchangeId={exchangeId} />)}
        </div>
        {(view.stop || badges.length > 0) && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-border px-3 py-2 text-xs text-muted-foreground" role="status" data-cosre-stop={view.stop?.reason ?? ''}>
            {view.stop && <><span>{T.stopPrefix}</span><span>{stopLine(view.stop)}</span></>}
            {badges.map(b => (
              <Badge key={b.label} variant="outline" title={b.title} data-cosre-stop-badge=""
                className={cn('font-normal text-muted-foreground', b.warn && 'border-status-warn-border text-status-warn')}>{b.label}</Badge>
            ))}
          </div>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}
