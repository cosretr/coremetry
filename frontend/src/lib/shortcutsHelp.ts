// shortcutsHelp — v0.10.1665 (etkileşim P3, plan §1.2). `?` yardım penceresini
// koddan açan TEK yol: ⌘K "shortcuts-help" eylemi bunu çağırır, ShortcutsHelp
// (AppShell'de tek mount) dinler. Bileşen importu yok — palet gövdesi yardım
// penceresini (ve onun ui/ importlarını) çekmez.
export const SHORTCUTS_HELP_EVENT = 'coremetry:shortcuts-help';

export function openShortcutsHelp(): void {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent(SHORTCUTS_HELP_EVENT));
}
