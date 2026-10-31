// ScopeBadge — CoSRE v2 kapsam rozeti (araç seti dilim 5; tools-audit Q1/Q2).
// CosreSheet başlığında; 7 nedenin hepsi (scopeView). Tık → popover: açıklama
// + kapsamdaki servisler (≤20, fazlası "+N"). Kapsamsız sarı, okuma hatası
// kırmızı — kapsamsız hiçbir zaman sessiz değil.
import { useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/coremetry/overlay';
import { cn } from '@/lib/cn';
import type { CosreScope } from '@/lib/types';
import { EVIDENCE_STRINGS as T } from './strings';
import { scopeView, type ScopeTone } from './scopeView';

const DOT: Record<ScopeTone, string> = { ok: 'bg-status-ok', warn: 'bg-status-warn', err: 'bg-status-critical' };
// v0.10.1729 (mockup A) — yumuşak renkli rozet: durum rengi token'dan, sert kenar yok.
const TINT: Record<ScopeTone, string> = {
  ok: 'bg-status-ok-bg text-status-ok hover:bg-status-ok-bg hover:text-status-ok',
  warn: 'bg-status-warn-bg text-status-warn hover:bg-status-warn-bg hover:text-status-warn',
  err: 'bg-status-critical-bg text-status-critical hover:bg-status-critical-bg hover:text-status-critical',
};

export function ScopeBadge({ scope }: { scope: CosreScope }) {
  const [open, setOpen] = useState(false);
  const v = scopeView(scope);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="xs" className={cn('min-w-0 rounded-full font-normal', TINT[v.tone])}
          aria-label={`${T.scopeAria}: ${v.label}`} data-cosre-scope={scope.reason} data-tone={v.tone}>
          <span className={cn('size-1.5 shrink-0 rounded-full', DOT[v.tone])} aria-hidden />
          <span className="truncate">{v.label}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="flex w-80 flex-col gap-2 text-sm">
        <p>{v.text}</p>
        {v.services.length > 0 && (
          <div className="flex flex-wrap gap-1" data-cosre-scope-services="">
            {v.services.map(s => <span key={s} className="rounded-sm bg-muted px-1.5 font-mono text-xs">{s}</span>)}
            {v.more > 0 && <span className="rounded-sm bg-muted px-1.5 text-xs">{T.scopeTrimmed(v.more)}</span>}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
