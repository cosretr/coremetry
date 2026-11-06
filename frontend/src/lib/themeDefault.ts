// themeDefault — v0.10.1610 (shadcn Faz 1 R3, plan §2.4 + §12): varsayılan
// temanın TEK kaynağı. Bilerek bağımlılıksız: vite.config.ts de bu dosyayı
// import eder (transformIndexHtml index.html'deki <html data-theme> ve açılış
// betiğinin geri dönüşünü buradan yazar) — uygulama kodu (storage, '@' alias)
// config yüklemesine girmesin. Okuyucular lib/theme.ts üzerinden gelir.
// v0.10.1847 — tema birliği ve açılış betiğinin renk/şema haritaları tema
// kaydından (lib/themeRegistry.ts, o da bağımlılıksız) gelir.
import { themeMapLiteral, type Theme } from './themeRegistry';

export type { Theme };

/** Kayıtlı tercih yoksa / okunamazsa / belge yoksa (node testleri) tema. */
export const DEFAULT_THEME: Theme = 'redhat';

/** index.html kaynağındaki yer tutucu; build/dev'de DEFAULT_THEME olur. */
export const DEFAULT_THEME_MARKER = '__COREMETRY_DEFAULT_THEME__';
/** v0.10.1847 — açılış betiğinin `id → --bg0` haritası (kayıttan). */
export const THEME_COLORS_MARKER = '__COREMETRY_THEME_COLORS__';
/** v0.10.1847 — açılış betiğinin `id → data-scheme` haritası (kayıttan). */
export const THEME_SCHEMES_MARKER = '__COREMETRY_THEME_SCHEMES__';

/** index.html'deki her yer tutucuyu doldurur; biri yoksa fırlatır
 *  (yer tutucusu silinmiş bir index.html sessizce literal'e dönmesin). */
export function renderDefaultTheme(html: string, theme: Theme = DEFAULT_THEME): string {
  if (!html.includes(DEFAULT_THEME_MARKER)) {
    throw new Error(`index.html: ${DEFAULT_THEME_MARKER} yer tutucusu yok (varsayılan tema lib/themeDefault.ts'ten yazılır)`);
  }
  const fills: readonly (readonly [string, string])[] = [
    [THEME_COLORS_MARKER, themeMapLiteral(e => e.themeColor)],
    [THEME_SCHEMES_MARKER, themeMapLiteral(e => e.scheme)],
    [DEFAULT_THEME_MARKER, theme],
  ];
  let out = html;
  for (const [marker, value] of fills) {
    if (!out.includes(marker)) throw new Error(`index.html: ${marker} yer tutucusu yok (lib/themeRegistry.ts'ten yazılır)`);
    out = out.split(marker).join(value);
  }
  return out;
}
