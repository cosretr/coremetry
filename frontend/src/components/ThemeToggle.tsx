import { lazy, Suspense, useEffect, useRef, useState } from 'react';
import { IconButton } from '@/components/ui/IconButton';

// v0.8.268 — third palette: 'redhat' (PatternFly-flavoured light +
// OpenShift-style dark nav; operator: "tasarım Red Hat ürünlerine
// benzeyebilir").
// v0.10.1849 (operatör onayı 2026-11-06, themes-proposal §6.4) — düğme
// döngü yerine tema LİSTESİNİ açar (Açık / Koyu grupları, kayıttan).
// Liste tembel yüklenir: bu düğme kabukta (Topbar), Radix ilk yüke binmez.
import { applyTheme, currentTheme, DEFAULT_THEME, THEME_LABEL as LABEL, THEME_GLYPH as GLYPH, type Theme } from '@/lib/theme';

const ThemeMenu = lazy(() => import('@/components/coremetry/ThemeMenu'));

/**
 * Opens the theme list; a pick sets `data-theme` + `data-scheme` on <html>
 * (lib/theme.ts applyTheme). Persisted in localStorage; the inline boot
 * script in index.html applies it pre-paint to avoid FOUC.
 */
export function ThemeToggle() {
  // v0.8.285 — default matches the index.html boot default; the useEffect
  // below still reads the real applied attribute (a saved pref wins).
  const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);
  const [open, setOpen] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);

  // Read the theme that the boot script already applied to <html>
  useEffect(() => { setTheme(currentTheme()); }, []);

  const toggle = () => {
    if (open) { setOpen(false); return; }
    setLoaded(true);
    setTheme(currentTheme()); // kullanıcı menüsü / ⌘K arada değiştirmiş olabilir
    setOpen(true);
  };
  const pick = (t: Theme) => { applyTheme(t); setTheme(t); setOpen(false); };

  // v0.10.924 — buton bütünlüğü Faz 2: IconButton (ghost, md = 28px kare).
  // `.theme-toggle` kalıyor: .topbar-prefs üçlüsünü tek bitişik grup yapan
  // kenarlık/radius kuralları ona bağlı (Lang/Density ile aynı kalıp).
  return (
    <>
      <IconButton ref={btn} variant="ghost" size="md" className="theme-toggle" onClick={toggle}
        aria-haspopup="menu" aria-expanded={open}
        aria-label={`Theme: ${LABEL[theme]} — choose a theme`}
        // v0.10.926 — Tooltip; grup köşe kuralı `:last-of-type` (globals.css),
        // kardeş ipucu kutusu ve menü çapası (span) son düğmenin köşesini bozmaz.
        tooltip={`Theme: ${LABEL[theme]}`}
        icon={GLYPH[theme]} />
      {loaded && (
        <Suspense fallback={null}>
          <ThemeMenu open={open} onOpenChange={setOpen} theme={theme} onPick={pick}
            onCloseFocus={() => btn.current?.focus()} />
        </Suspense>
      )}
    </>
  );
}
