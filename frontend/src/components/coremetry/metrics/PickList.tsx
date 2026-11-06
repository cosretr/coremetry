// coremetry/metrics/PickList — v0.10.1862 (Metrics Explorer Faz 2 dilim 2.2).
//
// Sorgu satırı seçicilerinin (metrik, süzgeç etiketi/değeri, `by` etiketi)
// ORTAK açılır listesi: arama kutusu + sunucu sonuçları + durum satırı.
// cmdk (shadcn Command): ↑↓ gezinme, ↵ seç; odak kutuda kalır. Liste
// süzmesi SUNUCUDA (shouldFilter=false) — istemci yalnız çizer.
// `allowFree`: yazılan değer listede olmasa da ↵ ile seçilebilir (etiket
// değeri regex'i, listede olmayan bir etiket adı).
import type { ReactNode } from 'react';
import { Command, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/shadcn/command';
import { Loader2 } from 'lucide-react';
import { cn } from '@/lib/cn';

export interface PickItem {
  value: string;
  label: ReactNode;
  /** Sağdaki kısa not (birim, sayı). */
  aside?: ReactNode;
  /** Alt satır (açıklama). */
  detail?: ReactNode;
}

export interface PickListProps {
  query: string;
  onQuery(q: string): void;
  items: PickItem[];
  loading?: boolean;
  error?: string | null;
  onPick(value: string): void;
  placeholder: string;
  label: string;
  heading?: string;
  /** Listenin altındaki dürüstlük satırı (ör. "137 sonuçtan ilk 20"). */
  footer?: ReactNode;
  allowFree?: boolean;
  emptyText?: string;
  className?: string;
}

const FREE = '\u0000free';

export function PickList({
  query, onQuery, items, loading, error, onPick, placeholder, label, heading, footer, allowFree, emptyText, className,
}: PickListProps) {
  const free = allowFree && query.trim() !== '' && !items.some(i => i.value === query.trim());
  return (
    <Command shouldFilter={false} loop label={label} className={cn('h-auto', className)}>
      <CommandInput value={query} onValueChange={onQuery} placeholder={placeholder} aria-label={label} className="font-mono text-xs" />
      <CommandList>
        {free && (
          <CommandGroup>
            <CommandItem value={FREE} onSelect={() => onPick(query.trim())} className="font-mono text-xs">
              <span className="truncate">{query.trim()}</span>
              <span className="ml-auto text-muted-foreground">↵ kullan</span>
            </CommandItem>
          </CommandGroup>
        )}
        {items.length > 0 && (
          <CommandGroup heading={heading}>
            {items.map(it => (
              <CommandItem key={it.value} value={it.value} onSelect={() => onPick(it.value)}
                className="flex-wrap gap-x-2 gap-y-0.5">
                <span className="min-w-0 flex-1 truncate font-mono text-xs" title={it.value}>{it.label}</span>
                {it.aside && <span className="shrink-0 text-xs text-muted-foreground">{it.aside}</span>}
                {it.detail && <span className="w-full truncate text-xs text-muted-foreground">{it.detail}</span>}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        {!loading && !error && items.length === 0 && !free && (
          <div className="px-3 py-4 text-center text-xs text-muted-foreground">{emptyText ?? 'Sonuç yok'}</div>
        )}
      </CommandList>
      <div className="flex items-center gap-2 border-t px-3 py-1.5 text-xs text-muted-foreground" role="status">
        {loading && <Loader2 className="size-3 animate-spin" aria-label="Aranıyor" />}
        {error ? <span className="text-status-critical">{error}</span> : footer}
        <span className="ml-auto"><kbd className="font-mono">↑↓</kbd> gez · <kbd className="font-mono">↵</kbd> seç</span>
      </div>
    </Command>
  );
}
