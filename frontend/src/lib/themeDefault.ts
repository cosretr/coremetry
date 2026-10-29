// themeDefault — v0.10.1610 (shadcn Faz 1 R3, plan §2.4 + §12): varsayılan
// temanın TEK kaynağı. Bilerek bağımlılıksız: vite.config.ts de bu dosyayı
// import eder (transformIndexHtml index.html'deki <html data-theme> ve açılış
// betiğinin geri dönüşünü buradan yazar) — uygulama kodu (storage, '@' alias)
// config yüklemesine girmesin. Okuyucular lib/theme.ts üzerinden gelir.

export type Theme = 'dark' | 'light' | 'redhat';

/** Kayıtlı tercih yoksa / okunamazsa / belge yoksa (node testleri) tema. */
export const DEFAULT_THEME: Theme = 'redhat';

/** index.html kaynağındaki yer tutucu; build/dev'de DEFAULT_THEME olur. */
export const DEFAULT_THEME_MARKER = '__COREMETRY_DEFAULT_THEME__';

/** index.html'deki her yer tutucuyu temayla değiştirir; yoksa fırlatır
 *  (yer tutucusu silinmiş bir index.html sessizce literal'e dönmesin). */
export function renderDefaultTheme(html: string, theme: Theme = DEFAULT_THEME): string {
  if (!html.includes(DEFAULT_THEME_MARKER)) {
    throw new Error(`index.html: ${DEFAULT_THEME_MARKER} yer tutucusu yok (varsayılan tema lib/themeDefault.ts'ten yazılır)`);
  }
  return html.split(DEFAULT_THEME_MARKER).join(theme);
}
