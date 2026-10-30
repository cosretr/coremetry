// coremetry/SearchCommand — CoSRE v2 karşılaması (2026-10-31). Sunucu-aramalı
// shadcn Command: liste ÇAĞIRANIN sunucu cevabıdır (cmdk istemci süzmesi
// kapalı), katalog önden çekilmez — veri katmanı çağıranda (usePickerSearch).
// Liste HER ZAMAN çizilir (cmdk girdisinin aria-controls'u ona işaret eder);
// sonuç yokken durum metni devre dışı tek seçenek — seçeneksiz role=listbox
// axe aria-required-children ihlali olurdu.
import type { ReactNode } from 'react';
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
}

export function SearchCommand({
  query, onQueryChange, placeholder, heading, items, stateText, stateKind, onPick, icon, autoFocus = false, className,
  'data-search': dataSearch,
}: SearchCommandProps) {
  return (
    <Command shouldFilter={false} data-search-command={dataSearch ?? ''} className={className ?? 'rounded-md border'}>
      <CommandInput value={query} onValueChange={onQueryChange} placeholder={placeholder} aria-label={placeholder} autoFocus={autoFocus} />
      <CommandList>
        {items.length === 0 ? (
          <CommandItem disabled value="__state" className="justify-center py-4 text-muted-foreground" data-search-state={stateKind ?? ''}>
            {stateText}
          </CommandItem>
        ) : (
          <CommandGroup heading={heading}>
            {items.map(name => (
              <CommandItem key={name} value={name} onSelect={() => onPick(name)}>
                {icon}
                <span className="min-w-0 flex-1 truncate font-mono" title={name}>{name}</span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}
      </CommandList>
    </Command>
  );
}
