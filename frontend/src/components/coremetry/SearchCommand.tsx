// coremetry/SearchCommand — CoSRE v2 karşılaması (2026-10-31). Sunucu-aramalı
// shadcn Command: liste ÇAĞIRANIN sunucu cevabıdır (cmdk istemci süzmesi
// kapalı), katalog önden çekilmez — veri katmanı çağıranda (usePickerSearch).
// Liste HER ZAMAN çizilir (cmdk girdisinin aria-controls'u ona işaret eder);
// sonuç yokken durum metni devre dışı tek seçenek — seçeneksiz role=listbox
// axe aria-required-children ihlali olurdu.
import type { ReactNode } from 'react';
import { Check } from 'lucide-react';
import { cn } from '@/lib/cn';
import { Command, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/shadcn/command';

export interface SearchCommandProps {
  query: string;
  onQueryChange(q: string): void;
  placeholder: string;
  heading: string;
  items: readonly string[];
  /** Sonuç yokken gösterilen durum ("Aranıyor…", "Eşleşme yok", hata). */
  stateText: string;
  stateKind?: string;
  onPick(name: string): void;
  icon?: ReactNode;
  autoFocus?: boolean;
  className?: string;
  'data-search'?: string;
  /** v0.10.1725 — seçim kipi: verilirse satır başında ✓ (seçili satırda görünür) + data-picked. */
  selected?: string;
  /** Seçili satırın ekran okuyucu eki (ör. "(seçili)"). */
  selectedLabel?: string;
  /** v0.10.1725 — satırın sağ ek bilgisi (ör. "12 servis"). */
  meta?: (name: string) => ReactNode;
  disabled?: boolean;
}

export function SearchCommand({
  query, onQueryChange, placeholder, heading, items, stateText, stateKind, onPick, icon, autoFocus = false, className,
  'data-search': dataSearch, selected, selectedLabel, meta, disabled = false,
}: SearchCommandProps) {
  return (
    <Command shouldFilter={false} data-search-command={dataSearch ?? ''} className={className ?? 'rounded-md border'}>
      <CommandInput value={query} onValueChange={onQueryChange} placeholder={placeholder} aria-label={placeholder} autoFocus={autoFocus} disabled={disabled} />
      <CommandList>
        {items.length === 0 ? (
          <CommandItem disabled value="__state" className="justify-center py-4 text-muted-foreground" data-search-state={stateKind ?? ''}>
            {stateText}
          </CommandItem>
        ) : (
          <CommandGroup heading={heading}>
            {items.map(name => (
              <CommandItem key={name} value={name} onSelect={() => onPick(name)} disabled={disabled}
                data-item={name} data-picked={selected !== undefined && name === selected ? '' : undefined}>
                {selected !== undefined
                  ? <Check aria-hidden="true" className={cn('size-4', name === selected ? 'opacity-100' : 'opacity-0')} />
                  : icon}
                <span className="min-w-0 flex-1 truncate font-mono" title={name}>{name}</span>
                {selected !== undefined && name === selected && selectedLabel && <span className="sr-only">{selectedLabel}</span>}
                {meta && <span className="shrink-0 text-xs text-muted-foreground">{meta(name)}</span>}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
      </CommandList>
    </Command>
  );
}
