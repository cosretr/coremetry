// coremetry/CommandPalette — v0.10.1617 (shadcn Faz 1 R9, plan §6). YALNIZ
// CEPHE: bugünkü components/CommandPalette (App kökünde mount) ve açıcısı
// yeniden dışa aktarılır; cmdk gövdesi kabuk diliminde (son) gelir ve bu
// DONDURULMUŞ tip sayesinde AppShell/Topbar çağrı biçimleri değişmez.
import type { LucideIcon } from 'lucide-react';
import type { ReactNode } from 'react';

export interface CommandPaletteItem {
  id: string;
  label: string;
  hint?: string;
  icon?: LucideIcon;
  href?: string;
  run?(): void;
}

export interface CommandPaletteGroup {
  id: string;
  label: string;
  items: CommandPaletteItem[];
}

/** Kabuk diliminin cmdk gövdesinin sözleşmesi (bugünkü bileşen prop almıyor). */
export interface CommandPaletteProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  groups: CommandPaletteGroup[];
  query: { value: string; onChange(v: string): void; placeholder?: string };
  loading?: boolean;
  empty?: ReactNode;
}

export { CommandPalette, openCommandPalette } from '@/components/CommandPalette';
