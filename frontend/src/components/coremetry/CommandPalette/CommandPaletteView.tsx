// coremetry/CommandPalette/CommandPaletteView — v0.10.1658 (etkileşim dilimi
// P2, plan §1.1/§1.4). Dondurulmuş `CommandPaletteProps`'un (v0.10.1617)
// cmdk çizicisi: coremetry/overlay/Dialog (tek Esc yığını, Radix odak tuzağı +
// açana odak dönüşü) içinde shadcn `command` parçaları. cmdk süzmesi KAPALI
// (`shouldFilter={false}`): sıra ve süzme çağıranın (sunucu araması +
// lib/paletteScore) — cmdk yalnız klavye gezinmesi + combobox/listbox ARIA'sı.
// Satır seçimi `item.run()`; `href` yalnız bilgi (gezinmeyi çağıran yapar).
// Inline stil yok; irtifa `z-[var(--z-modal)]` (çekmece panelinin üstü,
// zLayers.test) — perde aynı rungu .cp-dialog kardeş kuralından alır (tokens.css, utilities katmanı).
import { useState } from 'react';
import { Dialog, DialogContent, DialogTitle } from '../overlay/Dialog';
import {
  Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut,
} from '@/components/shadcn/command';
import { CM_STRINGS } from '../strings';
import type { CommandPaletteProps } from './CommandPalette';

export const CP_DIALOG_CLASS =
  'cp-dialog z-[var(--z-modal)] top-[12vh] translate-y-0 w-[min(640px,92vw)] max-w-none sm:max-w-none gap-0 overflow-hidden p-0';

export function CommandPaletteView({ open, onOpenChange, groups, query, loading, empty }: CommandPaletteProps) {
  const visible = groups.filter(g => g.items.length > 0);
  // Seçim = cmdk değeri, burada denetimli: liste değişince (sunucu sonuçları
  // geldi, sorgu değişti, kip değişti) imleç İLK satıra döner — eski paletin
  // `selected` sıfırlaması; yoksa eski satır listenin sonuna kayar ve ↵
  // beklenmeyen satırı seçerdi. Türetilmiş durum deseni (effect yok).
  const ids = visible.flatMap(g => g.items.map(i => i.id));
  const sig = ids.join('\u0000');
  const [value, setValue] = useState(ids[0] ?? '');
  const [seenSig, setSeenSig] = useState(sig);
  if (sig !== seenSig) {
    setSeenSig(sig);
    setValue(ids[0] ?? '');
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent showCloseButton={false} className={CP_DIALOG_CLASS} aria-describedby={undefined}>
        <DialogTitle className="sr-only">{CM_STRINGS.commandPaletteTitle}</DialogTitle>
        <Command shouldFilter={false} loop label={CM_STRINGS.commandPaletteTitle} value={value} onValueChange={setValue}
          className="rounded-none bg-[var(--bg)] text-[var(--text)]">
          <CommandInput value={query.value} onValueChange={query.onChange}
            placeholder={query.placeholder} aria-label={query.placeholder ?? CM_STRINGS.search}
            aria-busy={loading || undefined} className="h-12 text-sm" />
          <CommandList label={CM_STRINGS.suggestions} className="max-h-[min(60vh,520px)]">
            {visible.length === 0 && (
              <CommandEmpty>{empty ?? CM_STRINGS.noResults}</CommandEmpty>
            )}
            {visible.map(g => (
              <CommandGroup key={g.id} heading={g.label}>
                {g.items.map(it => (
                  <CommandItem key={it.id} value={it.id} onSelect={() => it.run?.()}>
                    {it.icon && <it.icon aria-hidden="true" />}
                    <span className="min-w-0 flex-1 truncate" title={it.label}>{it.label}</span>
                    {it.hint && <CommandShortcut className="tracking-normal">{it.hint}</CommandShortcut>}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
