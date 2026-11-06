// coremetry/metrics/FormulaRow — v0.10.1862 (Metrics Explorer Faz 2 dilim
// 2.2; mockup görünüm 1 "ƒ  B / A * 100 · birim · lejant").
//
// Formül ifadesi harflere başvurur; derleme lib/metricsExplorer/compile.ts
// (compileMxFormula) — hesap SUNUCUDA, seriler etiketleriyle eşleşir. Hata
// satırın altında tek cümle (role=alert). Birim formülün kendi birimidir
// (oran → %), kaynak metriklerden türetilmez.
import { useState } from 'react';
import { Eye, EyeOff, MoreHorizontal } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger,
} from '@/components/coremetry/overlay';
import { cn } from '@/lib/cn';
import { compileMxFormula } from '@/lib/metricsExplorer/compile';
import type { MxQuery } from '@/lib/metricsExplorer/model';
import { FORMULA_UNITS } from './labels';

export interface FormulaRowProps {
  query: MxQuery;
  queries: MxQuery[];
  onChange(q: MxQuery): void;
  onRemove?(): void;
  className?: string;
}

export function FormulaRow({ query: q, queries, onChange, onRemove, className }: FormulaRowProps) {
  const [unitOpen, setUnitOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const comp = q.expr.trim() ? compileMxFormula(q.expr, queries) : null;
  const unitLabel = FORMULA_UNITS.find(u => u.value === (q.unit ?? ''))?.label ?? q.unit ?? 'birimsiz';
  const set = (p: Partial<MxQuery>) => onChange({ ...q, ...p });
  return (
    <div role="group" aria-label={`Formül ${q.id}`} data-testid={`formula-row-${q.id}`}
      className={cn('flex flex-col gap-1 border-b py-1.5 last:border-b-0', className)}>
      <div className={cn('flex flex-wrap items-center gap-1.5', q.hidden && '[&>*:not([data-keep])]:opacity-60')}>
        <span className="inline-flex h-6 min-w-6 shrink-0 items-center justify-center rounded-md border border-dashed px-1 font-mono text-xs font-semibold" title={`Formül ${q.id}`}>ƒ</span>
        <Input value={q.expr} onChange={e => set({ expr: e.target.value })} placeholder="B / A * 100"
          aria-label={`Formül ${q.id} ifadesi`} aria-invalid={comp?.ok === false || undefined}
          className="h-6 w-56 px-1.5 font-mono text-xs md:text-xs" />
        <span className="text-xs text-muted-foreground">birim</span>
        <DropdownMenu open={unitOpen} onOpenChange={setUnitOpen}>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="outline" size="xs" aria-label={`Formül birimi: ${unitLabel}`}>{unitLabel}</Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuRadioGroup value={q.unit ?? ''} onValueChange={v => set({ unit: v || undefined })}>
              {FORMULA_UNITS.map(u => <DropdownMenuRadioItem key={u.value || 'none'} value={u.value}>{u.label}</DropdownMenuRadioItem>)}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        <label className="inline-flex items-center gap-1 text-xs text-muted-foreground">
          lejant
          <Input value={q.legend} onChange={e => set({ legend: e.target.value })} placeholder="hata % {{service_name}}"
            className="h-6 w-48 px-1.5 font-mono text-xs md:text-xs" aria-label={`Formül ${q.id} lejant biçimi`} />
        </label>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground">Sunucuda hesaplanır; seriler etiketleriyle eşleşir.</span>
        <Button type="button" variant="ghost" size="icon-xs" data-keep
          aria-label={q.hidden ? `${q.id} formülünü grafikte göster` : `${q.id} formülünü grafikte gizle`}
          aria-pressed={q.hidden} onClick={() => set({ hidden: !q.hidden })}>
          {q.hidden ? <EyeOff aria-hidden="true" /> : <Eye aria-hidden="true" />}
        </Button>
        {onRemove && (
          <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
            <DropdownMenuTrigger asChild>
              <Button type="button" variant="ghost" size="icon-xs" data-keep aria-label={`Formül ${q.id} menüsü`}><MoreHorizontal aria-hidden="true" /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={onRemove} variant="destructive">Sil</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
      {comp?.ok === false && <div role="alert" className="pl-8 text-xs text-status-critical">{comp.error}</div>}
    </div>
  );
}
