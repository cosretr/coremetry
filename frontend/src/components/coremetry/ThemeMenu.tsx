// ThemeMenu — v0.10.1849 (docs/frontend/themes-proposal.md §6.4, operatör
// onayı 2026-11-06). Üst şeritteki tema düğmesi artık DÖNGÜ değil liste
// açar: 3'ten fazla temada döngü kullanışsız. Gruplar (Açık / Koyu) ve
// sıra tema kaydından (lib/themeRegistry.ts). ThemeToggle bu dosyayı
// tembel yükler: düğme kabukta (Topbar) — Radix ilk yüke binmez
// (bundle-budget radix-in-entry 0).
import { Fragment } from 'react';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuRadioGroup,
  DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/coremetry/overlay/DropdownMenu';
import { SCHEME_LABEL, THEME_GROUPS, THEME_LABEL, type Theme } from '@/lib/theme';
import { isThemeId } from '@/lib/themeRegistry';

export interface ThemeMenuProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  theme: Theme;
  onPick(theme: Theme): void;
  /** Kapanınca odak düğmeye döner (gizli çapa span'ine değil). */
  onCloseFocus(): void;
}

export default function ThemeMenu({ open, onOpenChange, theme, onPick, onCloseFocus }: ThemeMenuProps) {
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      {/* Çapa: düğmenin hemen ardındaki 0×0 kardeş (akışta, satırın dibinde) —
          menü düğmenin sağ-alt köşesine hizalanır; ölçüm / satır içi stil yok. */}
      <DropdownMenuTrigger asChild>
        <span aria-hidden="true" tabIndex={-1} className="pointer-events-none inline-block size-0 self-end" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" aria-label="Tema seç"
        onCloseAutoFocus={e => { e.preventDefault(); onCloseFocus(); }}>
        {THEME_GROUPS.map((g, i) => (
          <Fragment key={g.scheme}>
            {i > 0 && <DropdownMenuSeparator />}
            <DropdownMenuLabel>{SCHEME_LABEL[g.scheme]}</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={theme} onValueChange={v => { if (isThemeId(v)) onPick(v); }}>
              {g.themes.map(t => (
                <DropdownMenuRadioItem key={t} value={t}>{THEME_LABEL[t]}</DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
