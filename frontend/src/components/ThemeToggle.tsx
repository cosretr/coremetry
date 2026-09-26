import { useEffect, useState } from 'react';
import { setRaw } from '@/lib/storage';
import { IconButton } from '@/components/ui/IconButton';

// v0.8.268 — third palette: 'redhat' (PatternFly-flavoured light +
// OpenShift-style dark nav; operator: "tasarım Red Hat ürünlerine
// benzeyebilir"). The toggle cycles dark → light → redhat.
type Theme = 'dark' | 'light' | 'redhat';

const STORAGE_KEY = 'coremetry-theme';

const NEXT: Record<Theme, Theme> = { dark: 'light', light: 'redhat', redhat: 'dark' };
const GLYPH: Record<Theme, string> = { dark: '☾', light: '☀', redhat: '⬢' };
const LABEL: Record<Theme, string> = {
  dark: 'Dark', light: 'Light', redhat: 'Red Hat',
};

/**
 * Cycles the palette by setting the `data-theme` attribute on
 * <html>. Persisted in localStorage; the inline boot script in
 * index.html applies it pre-paint to avoid FOUC.
 */
export function ThemeToggle() {
  // v0.8.285 — default 'redhat' to match the index.html boot default; the
  // useEffect below still reads the real applied attribute (a saved pref wins).
  const [theme, setTheme] = useState<Theme>('redhat');

  // Read the theme that the boot script already applied to <html>
  useEffect(() => {
    const t = document.documentElement.getAttribute('data-theme');
    setTheme(t === 'dark' || t === 'light' ? t : 'redhat');
  }, []);

  const toggle = () => {
    const next = NEXT[theme];
    setTheme(next);
    document.documentElement.setAttribute('data-theme', next);
    setRaw(STORAGE_KEY, next);
  };

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
