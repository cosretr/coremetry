import { useEffect, useState } from 'react';
import { IconButton } from '@/components/ui/IconButton';

// v0.8.268 — third palette: 'redhat' (PatternFly-flavoured light +
// OpenShift-style dark nav; operator: "tasarım Red Hat ürünlerine
// benzeyebilir"). The toggle cycles dark → light → redhat.
import { cycleTheme, currentTheme, DEFAULT_THEME, THEME_LABEL as LABEL, THEME_NEXT as NEXT, THEME_GLYPH as GLYPH, type Theme } from '@/lib/theme';

/**
 * Cycles the palette by setting the `data-theme` attribute on
 * <html>. Persisted in localStorage; the inline boot script in
 * index.html applies it pre-paint to avoid FOUC.
 */
export function ThemeToggle() {
  // v0.8.285 — default 'redhat' to match the index.html boot default; the
  // useEffect below still reads the real applied attribute (a saved pref wins).
  const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);

  // Read the theme that the boot script already applied to <html>
  useEffect(() => { setTheme(currentTheme()); }, []);

  const toggle = () => { setTheme(cycleTheme()); };

  // v0.10.924 — buton bütünlüğü Faz 2: IconButton (ghost, md = 28px kare).
  // `.theme-toggle` kalıyor: .topbar-prefs üçlüsünü tek bitişik grup yapan
  // kenarlık/radius kuralları ona bağlı (Lang/Density ile aynı kalıp).
  return (
    <IconButton variant="ghost" size="md" className="theme-toggle" onClick={toggle}
      aria-label={`Theme: ${LABEL[theme]} — switch to ${LABEL[NEXT[theme]]}`}
      // v0.10.926 — Tooltip; grup köşe kuralı `:last-of-type` (globals.css),
      // kardeş ipucu kutusu son düğmenin köşesini bozmaz.
      tooltip={`Theme: ${LABEL[theme]} — click for ${LABEL[NEXT[theme]]}`}
      icon={GLYPH[theme]} />
  );
}
