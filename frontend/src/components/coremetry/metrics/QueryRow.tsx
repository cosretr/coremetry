// coremetry/metrics/QueryRow — v0.10.1862 (Metrics Explorer Faz 2 dilim 2.2;
// audit §1 öneri 3 "tek sorgu bileşeni", §4 satır anatomisi; mockup görünüm 1
// `#qrowA`).
//
// Soldan sağa SABİT anatomi: harf · metrik (tür rozeti) · kapsam çipi
// (kilitli, bağlam çubuğundan) · süzgeç çipleri · + süzgeç | fonksiyon ·
// "sayaç → rate" notu · topla + by çipleri | lejant · göster/gizle · ⋯.
// Metrics Explorer, Explore metrik satırı (2.6) ve panel editörü (Faz 3) bu
// bileşeni kullanır; model lib/metricsExplorer/model.ts (MxQuery).
//
// Seçicilerin hepsi sunucu araması (MetricPicker, LabelPick); `fnOptions` /
// `aggOptions` bir tüketicinin arka ucu desteklemediği seçenekleri kapatır
// (Explore'un /api/metrics/query yolu: irate, count, none yok).
import { useState } from 'react';
import { Eye, EyeOff, MoreHorizontal, Plus, X } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger,
  Popover, PopoverContent, PopoverTrigger,
} from '@/components/coremetry/overlay';
import { cn } from '@/lib/cn';
import type { MetricSourceParam } from '@/lib/metricSource';
import {
  defaultFnFor, isPercentileFn, withMetric, MX_AGGS, type MxAgg, type MxFn, type MxQuery,
} from '@/lib/metricsExplorer/model';
import { MetricPicker } from './MetricPicker';
import { FilterPopover } from './FilterPopover';
import { LabelNamePick } from './LabelPick';
import { AGG_LABEL, FN_LABEL, KIND_LABEL, fnHint, fnsForKind } from './labels';

export interface QueryRowScope {
  label: string;
  value: string;
}

export interface QueryRowProps {
  query: MxQuery;
  onChange(q: MxQuery): void;
  onRemove?(): void;
  onDuplicate?(): void;
  /** Bağlam çubuğunun cluster kapsamı (kilitli çip); null = kapsam yok. */
  scope?: QueryRowScope | null;
  /** Çipin ✕'i: bu sorgu tüm cluster'larda (tek tıkla kapanır, S6). */
  onScopeOff?(): void;
  /** Seçicilere geçen cluster (kapsam açıkken). */
  cluster?: string;
  src?: MetricSourceParam;
  fnOptions?: MxFn[];
  aggOptions?: MxAgg[];
  /** Lejant biçimi alanı (Explore satırında yok). */
  showLegend?: boolean;
  className?: string;
}

function Pill({ children, onRemove, removeLabel, className }: {
  children: React.ReactNode; onRemove?(): void; removeLabel?: string; className?: string;
}) {
  return (
    <span className={cn('inline-flex h-6 max-w-[22rem] items-center gap-1 rounded-md border bg-muted px-1.5 font-mono text-xs', className)}>
      {children}
      {onRemove && (
        <Button type="button" variant="ghost" size="icon-xs" className="size-4" aria-label={removeLabel ?? 'Kaldır'} onClick={onRemove}>
          <X aria-hidden="true" />
        </Button>
      )}
    </span>
  );
}

export function QueryRow({
  query: q, onChange, onRemove, onDuplicate, scope, onScopeOff, cluster, src, fnOptions, aggOptions, showLegend = true, className,
}: QueryRowProps) {
  const [fnOpen, setFnOpen] = useState(false);
  const [aggOpen, setAggOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [byOpen, setByOpen] = useState(false);
  const kind = q.metricKind;
  const fns = fnsForKind(kind).filter(f => !fnOptions || fnOptions.includes(f));
  const fnList = fns.includes(q.fn) ? fns : [q.fn, ...fns];
  const aggs = MX_AGGS.filter(a => !aggOptions || aggOptions.includes(a));
  const pct = isPercentileFn(q.fn);
  const autoNote = kind && kind !== 'unknown' && q.metric && q.fn === defaultFnFor(kind) && q.fn !== 'raw'
    ? `${KIND_LABEL[kind]} → ${FN_LABEL[q.fn].text}` : '';
  const set = (p: Partial<MxQuery>) => onChange({ ...q, ...p });

  return (
    <div role="group" aria-label={`Sorgu ${q.id}`} data-testid={`query-row-${q.id}`}
      className={cn('flex flex-wrap items-center gap-1.5 border-b py-1.5 last:border-b-0', q.hidden && '[&>*:not([data-keep])]:opacity-60', className)}>
      <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-md bg-muted font-mono text-xs font-semibold" title={`Sorgu ${q.id}`}>{q.id}</span>
      <MetricPicker value={q.metric} kind={kind} kindEstimated={q.kindEstimated} cluster={cluster} src={src}
        onPick={m => onChange(withMetric(q, m.name, m.typeSource === 'inferred' ? undefined : m.type, m.unit))} />
      {scope && (
        <Pill className="border-dashed" onRemove={onScopeOff} removeLabel="Kapsamı kaldır: tüm cluster'lar">
          <span className="font-sans text-[10px] uppercase text-muted-foreground" title="Bağlam çubuğundaki cluster seçiminden gelir">kapsam</span>
          <span className="truncate">{scope.label} = {scope.value}</span>
        </Pill>
      )}
      {q.filters.map((f, i) => (
        <span key={`${f.k}${f.op}${f.v}${i}`} className="inline-flex items-center">
          <FilterPopover metric={q.metric} cluster={cluster} value={f}
            triggerLabel={`Süzgeci düzenle: ${f.k} ${f.op} ${f.v}`}
            triggerClassName="h-6 rounded-r-none border border-r-0 bg-muted px-1.5 font-mono"
            trigger={<span className="max-w-[18rem] truncate">{f.k} {f.op} <span className="text-foreground">{f.v === '' ? '""' : f.v}</span></span>}
            onSave={nf => set({ filters: q.filters.map((x, j) => (j === i ? nf : x)) })} />
          <Button type="button" variant="ghost" size="icon-xs" className="h-6 rounded-l-none border border-l-0 bg-muted"
            aria-label={`Süzgeci kaldır: ${f.k}`} onClick={() => set({ filters: q.filters.filter((_, j) => j !== i) })}>
            <X aria-hidden="true" />
          </Button>
        </span>
      ))}
      <FilterPopover metric={q.metric} cluster={cluster} triggerLabel="Süzgeç ekle" trigger="+ süzgeç"
        onSave={f => set({ filters: [...q.filters, f] })} />
      <span className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />

      <DropdownMenu open={fnOpen} onOpenChange={setFnOpen}>
        <DropdownMenuTrigger asChild>
          <Button type="button" variant="outline" size="xs" aria-label={`Fonksiyon: ${FN_LABEL[q.fn].text}`}>
            <span className="text-muted-foreground">fonksiyon</span><span className="font-mono">{FN_LABEL[q.fn].text}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-56">
          <DropdownMenuRadioGroup value={q.fn} onValueChange={v => set({ fn: v as MxFn })}>
            {fnList.map(f => (
              <DropdownMenuRadioItem key={f} value={f} className="gap-3">
                <span className="font-mono">{FN_LABEL[f].text}</span>
                <span className="ml-auto text-xs text-muted-foreground">{fnHint(f, kind)}</span>
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {autoNote && (
        <span className="text-xs text-muted-foreground"
          title={`Metrik türü ${KIND_LABEL[kind!]}${q.kindEstimated ? ' (tahmini)' : ''} olduğu için ${FN_LABEL[q.fn].text} kendiliğinden seçildi; değiştirebilirsiniz`}>
          {autoNote}{q.kindEstimated ? ' · tahmini' : ''}
        </span>
      )}

      {pct ? (
        <span className="text-xs text-muted-foreground" title="Yüzdelik, kovaları sum by (le) ile toplar; seri kırılımı by ile">topla <span className="font-mono">sum</span></span>
      ) : (
        <DropdownMenu open={aggOpen} onOpenChange={setAggOpen}>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="outline" size="xs" aria-label={`Toplama: ${AGG_LABEL[q.agg]}`}>
              <span className="text-muted-foreground">topla</span><span className="font-mono">{AGG_LABEL[q.agg]}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuRadioGroup value={q.agg} onValueChange={v => set({ agg: v as MxAgg })}>
              {aggs.map(a => <DropdownMenuRadioItem key={a} value={a} className="font-mono">{AGG_LABEL[a]}</DropdownMenuRadioItem>)}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {(pct || q.agg !== 'none') && (
        <>
          <span className="text-xs text-muted-foreground">by</span>
          {q.by.map(b => (
            <Pill key={b} onRemove={() => set({ by: q.by.filter(x => x !== b) })} removeLabel={`Gruplamadan çıkar: ${b}`}>
              <span className="truncate">{b}</span>
            </Pill>
          ))}
          <Popover open={byOpen} onOpenChange={setByOpen}>
            <PopoverTrigger asChild>
              <Button type="button" variant="ghost" size="icon-xs" aria-label="Gruplama etiketi ekle"><Plus aria-hidden="true" /></Button>
            </PopoverTrigger>
            <PopoverContent align="start" className="w-[22rem] max-w-[calc(100vw-2rem)] p-0">
              <LabelNamePick metric={q.metric} cluster={cluster} exclude={q.by} label="Gruplama etiketi ara"
                onPick={name => { if (!q.by.includes(name)) set({ by: [...q.by, name] }); setByOpen(false); }} />
            </PopoverContent>
          </Popover>
        </>
      )}

      {showLegend && (
        <>
          <span className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />
          <label className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            lejant
            <Input value={q.legend} onChange={e => set({ legend: e.target.value })} placeholder="{{service_name}}"
              className="h-6 w-44 px-1.5 font-mono text-xs md:text-xs" aria-label={`Sorgu ${q.id} lejant biçimi`} />
          </label>
        </>
      )}
      <span className="flex-1" />
      <Button type="button" variant="ghost" size="icon-xs" data-keep
        aria-label={q.hidden ? `${q.id} sorgusunu grafikte göster` : `${q.id} sorgusunu grafikte gizle`}
        aria-pressed={q.hidden} onClick={() => set({ hidden: !q.hidden })}>
        {q.hidden ? <EyeOff aria-hidden="true" /> : <Eye aria-hidden="true" />}
      </Button>
      {(onDuplicate || onRemove) && (
        <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="ghost" size="icon-xs" data-keep aria-label={`Sorgu ${q.id} menüsü`}><MoreHorizontal aria-hidden="true" /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {onDuplicate && <DropdownMenuItem onSelect={onDuplicate}>Çoğalt</DropdownMenuItem>}
            {onRemove && <DropdownMenuItem onSelect={onRemove} variant="destructive">Sil</DropdownMenuItem>}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}
