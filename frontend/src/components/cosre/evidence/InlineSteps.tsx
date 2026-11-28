// InlineSteps — v0.10.2491 (CoSRE sohbet dilim 1): inline akışın çizimi.
// Sıra: ekran bağlamı cümlesi → (anlatım satırı → katlı "N araç çalıştı ›")
// × grup → özet satırı. Grup açılınca her araç StepCard (ad + sonuç özeti +
// amaç satırı; kartın kendi açılırı: etiketler, önizleme, sorgu, ham çıktı,
// İlgili / İlgisiz). Akışta çalışan grup "çalışıyor…" der.
// StepList'in ("Nasıl cevapladım" eşi) bilgileri kaybolmaz: başlık sayaçları
// (çağrı · kayıttan · önbellekten · atlandı) + toplam süre ve data-stop satırı
// (neden · çağrı · tur · süre) + rozetler (ön-yükleme / niyet / zaman tavanı)
// grupların altındaki tek özet satırında.
import { useState } from 'react';
import '@/styles/cosreChat.css';
import { ChevronRight } from 'lucide-react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { cn } from '@/lib/cn';
import { tf } from '@/lib/i18n';
import { COSRE_LANG } from '@/components/ai/capabilityHints';
import type { CosreStepDirective } from '@/lib/types';
import { EVIDENCE_STRINGS as T } from './strings';
import { fmtMsTR, stepsHeaderLabel, stopLine, type StepView, type StepsView, type StopBadge } from './stepView';
import { StepCard } from './StepCard';
import type { FlowGroup, FlowView } from './flowView';

function Group({ g, live, exchangeId, onRedirect }: {
  g: FlowGroup; live: boolean; exchangeId?: string;
  onRedirect?: (step: StepView, d: CosreStepDirective) => void;
}) {
  const [open, setOpen] = useState(false);
  const running = live && g.running;
  return (
    <div className="cm-msg cm-msg--ai gap-1" data-cosre-flow-group={g.id} data-running={running || undefined}>
      {g.text && <p className="text-sm" data-cosre-narration="">{g.text}</p>}
      {g.steps.length > 0 && (
        <Collapsible open={open} onOpenChange={setOpen} data-open={open || undefined}>
          <CollapsibleTrigger asChild>
            <Button variant="ghost" size="xs" aria-expanded={open}
              className="h-auto gap-1 self-start px-1 font-normal text-muted-foreground tabular-nums" data-cosre-flow-toggle="">
              <span>{tf('cosre.flow.toolsRan', COSRE_LANG, { n: g.steps.length })}</span>
              {running && <span data-cosre-flow-running="">· {tf('cosre.flow.running', COSRE_LANG)}</span>}
              <ChevronRight className={cn('size-3 transition-transform', open && 'rotate-90')} aria-hidden />
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="rounded-md border border-border bg-card">
            <div role="list" aria-label={tf('cosre.flow.groupAria', COSRE_LANG)}>
              {g.steps.map(s => <StepCard key={s.id} v={s} exchangeId={exchangeId} onRedirect={live ? undefined : onRedirect} />)}
            </div>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  );
}

export function InlineSteps({ flow, view, live, exchangeId, badges = [], onRedirect }: {
  flow: FlowView; view: StepsView; live: boolean;
  exchangeId?: string;
  badges?: readonly StopBadge[];
  onRedirect?: (step: StepView, d: CosreStepDirective) => void;
}) {
  const elapsed = view.stop?.elapsed_ms ?? view.totalMs;
  return (
    <>
      {flow.context && <div className="cm-msg-note text-muted-foreground" data-cosre-flow-context="">{flow.context}</div>}
      {flow.groups.map(g => <Group key={g.id} g={g} live={live} exchangeId={exchangeId} onRedirect={onRedirect} />)}
      {(view.steps.length > 0 || view.stop || badges.length > 0) && !live && (
        <div className="cm-msg cm-msg--ai flex-row flex-wrap items-center gap-1.5 text-xs text-muted-foreground tabular-nums" role="status"
          data-cosre-flow-summary="" data-cosre-stop={view.stop?.reason ?? ''}>
          <span>{T.stepsTitle} · {stepsHeaderLabel(view)}{elapsed > 0 ? ` · ${fmtMsTR(elapsed)}` : ''}</span>
          {view.stop && <span>· {T.stopPrefix} {stopLine(view.stop)}</span>}
          {badges.map(b => (
            <Badge key={b.label} variant="outline" title={b.title} data-cosre-stop-badge=""
              className={cn('font-normal text-muted-foreground', b.warn && 'border-status-warn-border text-status-warn')}>{b.label}</Badge>
          ))}
        </div>
      )}
    </>
  );
}
