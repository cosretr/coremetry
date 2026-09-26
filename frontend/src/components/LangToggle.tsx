import { setUserLang, useUserLang, type Lang } from '../lib/i18n';
import { useBranding } from '../lib/branding';
import { IconButton } from '@/components/ui/IconButton';

// Two-state TR/EN picker. Mirrors ThemeToggle/DensityToggle visually
// (same `theme-toggle` class, single icon-button slot in the sidebar
// header) so the row of three controls reads as a single group.
//
// The displayed glyph is the OTHER language — i.e. when the UI is in
// English, the button shows "TR" because that's what clicking it
// switches to. Tooltip spells it out so the hover removes any
// ambiguity for someone who is still in the wrong language.
export function LangToggle() {
  const userLang = useUserLang();
  const brand = useBranding();
  const current: Lang = userLang ?? (brand.language === 'tr' ? 'tr' : 'en');
  const next: Lang = current === 'tr' ? 'en' : 'tr';

  const tip = current === 'tr' ? 'Switch to English' : 'Türkçe\'ye geç';
  // v0.10.924 — buton bütünlüğü Faz 2: ThemeToggle ile aynı IconButton kalıbı.
  return (
    <IconButton variant="ghost" size="md" className="theme-toggle tt-text" onClick={() => setUserLang(next)}
      aria-label={tip}
      tooltip={tip}
      icon={next.toUpperCase()} />
  );
}
