// coremetry/metrics/FilterPopover — v0.10.1862 (Metrics Explorer Faz 2 dilim
// 2.2; mockup görünüm 1 "Süzgeç ekle": etiket ve işleç → değer önerileri).
//
// İki adım tek popover'da: (1) etiket adı (sunucu araması), (2) işleç
// (= != =~ !~) + değer (sunucu önerisi ya da elle; regex işleçlerinde değer
// regex'tir). Var olan süzgeç tıklanınca aynı popover dolu açılır.
import { useState, type ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/coremetry/overlay';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { MX_FILTER_OPS, type MxFilter, type MxFilterOp } from '@/lib/metricsExplorer/model';
import { LabelNamePick, LabelValuePick } from './LabelPick';

export interface FilterPopoverProps {
  metric: string;
  cluster?: string;
  /** Düzenlenen süzgeç; yoksa yeni. */
  value?: MxFilter;
  onSave(f: MxFilter): void;
  /** Tetikleyici düğmenin içeriği ve erişilebilir adı. */
  trigger: ReactNode;
  triggerLabel: string;
  triggerClassName?: string;
}

const OP_TITLE: Record<MxFilterOp, string> = {
  '=': 'eşittir', '!=': 'eşit değil', '=~': 'regex eşleşir', '!~': 'regex eşleşmez',
};

export function FilterPopover({ metric, cluster, value, onSave, trigger, triggerLabel, triggerClassName }: FilterPopoverProps) {
  const [open, setOpen] = useState(false);
  const [key, setKey] = useState(value?.k ?? '');
  const [op, setOp] = useState<MxFilterOp>(value?.op ?? '=');
  const reset = () => { setKey(value?.k ?? ''); setOp(value?.op ?? '='); };
  return (
    <Popover open={open} onOpenChange={o => { setOpen(o); if (o) reset(); }}>
      <PopoverTrigger asChild>
        <Button type="button" variant="ghost" size="xs" aria-label={triggerLabel} className={triggerClassName}>{trigger}</Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[26rem] max-w-[calc(100vw-2rem)] p-0" aria-label="Süzgeç">
        {!key ? (
          <LabelNamePick metric={metric} cluster={cluster} onPick={setKey} />
        ) : (
          <div className="flex flex-col">
            <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
              <span className="font-mono text-xs">{key}</span>
              <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => setKey('')}>etiketi değiştir</Button>
              <SegmentedControl<MxFilterOp> size="sm" aria-label="İşleç" value={op} onChange={setOp} className="ml-auto"
                options={MX_FILTER_OPS.map(o => ({ value: o, label: <span className="font-mono">{o}</span>, title: OP_TITLE[o] }))} />
            </div>
            <LabelValuePick key={`${key}:${op}`} metric={metric} labelName={key} cluster={cluster} initial={value?.k === key ? value.v : ''}
              onPick={v => { onSave({ k: key, op, v }); setOpen(false); }} />
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
