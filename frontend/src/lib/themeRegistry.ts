// themeRegistry — v0.10.1847 (docs/frontend/themes-proposal.md §7): temaların
// TEK kaydı. Birlik tipi, seçici listesi, döngü sırası, etiket, glif, şema
// (açık/koyu), kontrast katmanı, seri paleti ve `meta theme-color` buradan
// türer; başka dosya tema kimliği listesi taşımaz.
//
// Bilerek bağımlılıksız: vite.config.ts lib/themeDefault.ts üzerinden bu
// dosyayı yükler (index.html açılış betiğinin renk/şema haritaları buradan
// yazılır) — '@' takma adı ya da uygulama modülü import etme.
//
// Yeni tema = bu listeye bir girdi + globals.css'te `[data-theme="<id>"]`
// bloğu (styles/themeBlocks.ts THEME_TOKENS'in hepsi + color-scheme) +
// tokens.css'te seri seçicisine kimlik. Tamlık kapısı:
// styles/themeRegistry.pin.test.ts.

export type ThemeScheme = 'light' | 'dark';
/** Kontrast katmanı: 'aa' = repo eşikleri (contrastTokens), 'aaa' = yüksek kontrast. */
export type ThemeContrast = 'aa' | 'aaa';

/** Adlandırılmış seri paletleri (canvas var() okuyamaz; tokens.css aynası). */
// v0.10.920 (sade palet, operatör onayı) — her renk bg0-bg2'ye ≥3:1, durum
// renklerinden (ok/uyarı/hata) OKLab ≥0.10 uzak. Sıra: mavi, turkuaz, gül,
// limon, çevre mavisi, turuncu, mor, orkide. Doğrulama: contrastTokens.test.ts.
export const SERIES_PALETTE_SETS = {
  dark:  ['#288dd4', '#06d8d9', '#cb4f73', '#b5b614', '#95b6fe', '#d76f04', '#ac78d2', '#fca0e8'],
  light: ['#045c93', '#0c9999', '#921545', '#898905', '#567cd3', '#d46e07', '#7b489e', '#bf68ae'],
  // v0.10.1850 — Yüksek Kontrast: siyah zeminde her yuva ≥4.5 ('aaa' katmanı).
  // Mockup paletinin dört yuvası durum rengine OKLab < 0.10 idi (seriesPalette
  // kapısı); en küçük değişiklikle: 2 #4fe0d0→#4fded8 (--ok 0.093→0.107),
  // 3 #ff8fb3→#ff87cb (--err 0.063→0.106), 4 #e6e04a→#c6f056 (--warn
  // 0.052→0.105), 6 #ffa257→#ffa227 (--err 0.075→0.107).
  contrast: ['#6cb6ff', '#4fded8', '#ff87cb', '#c6f056', '#b3c4ff', '#ffa227', '#cfa0ff', '#ffb3ec'],
  // v0.10.1851 — Kağıt: sıcak açık zemin (light paleti bg0'da 3.01 sınırda).
  // Mockup'tan iki yuva durum rengine OKLab < 0.10 idi; en küçük değişiklik:
  // 3 #a3245a→#972472 (--err 0.057→0.105), 4 #7d7a08→#7d7e10 (--warn 0.095→0.106).
  stone: ['#1f6fa8', '#0d8f8a', '#972472', '#7d7e10', '#5a6fd0', '#b85c0a', '#7a4aa6', '#b85aa3'],
} as const;
export type SeriesPaletteId = keyof typeof SERIES_PALETTE_SETS;

interface ThemeEntryShape {
  /** `data-theme` değeri ve localStorage `coremetry-theme` değeri. */
  readonly id: string;
  /** Seçicide görünen ad. */
  readonly label: string;
  /** Üst şerit düğmesinin glifi. */
  readonly glyph: string;
  /** `data-scheme`; Tailwind `dark:` varyantı, Grafana modu ve toast bunu okur. */
  readonly scheme: ThemeScheme;
  readonly contrast: ThemeContrast;
  readonly series: SeriesPaletteId;
  /** `<meta name="theme-color">` = globals.css bloğundaki `--bg0`. */
  readonly themeColor: string;
  /** /design axe kapısının CI hücresi (varsayılan yoğunluk). Gece işi hepsini koşar. */
  readonly ciCell: boolean;
}

// Sıra = döngü sırası ve gruplar içindeki liste sırası.
export const THEME_REGISTRY = [
  { id: 'dark', label: 'Dark', glyph: '☾', scheme: 'dark', contrast: 'aa', series: 'dark', themeColor: '#09090b', ciCell: true },
  { id: 'light', label: 'Light', glyph: '☀', scheme: 'light', contrast: 'aa', series: 'light', themeColor: '#f4f6f8', ciCell: true },
  // v0.10.1178 (operatör): "Red Hat" adı görünmesin; iç anahtar 'redhat' aynı.
  { id: 'redhat', label: 'Kurumsal', glyph: '⬢', scheme: 'light', contrast: 'aa', series: 'light', themeColor: '#f4f4f5', ciCell: true },
  // v0.10.1849 — Arduvaz: shadcn slate tabanı, mavi vurgu; `dark` seri paletini paylaşır (operatör).
  { id: 'slate', label: 'Arduvaz', glyph: '◑', scheme: 'dark', contrast: 'aa', series: 'dark', themeColor: '#0f172a', ciCell: false },
  // v0.10.1850 — Yüksek Kontrast: AAA katmanı, kendi paleti; CI axe temsilcisi (öneri §3).
  { id: 'contrast', label: 'Yüksek Kontrast', glyph: '◐', scheme: 'dark', contrast: 'aaa', series: 'contrast', themeColor: '#000000', ciCell: true },
  // v0.10.1851 — Kağıt: sıcak açık, turuncu vurgu, --heat = --info (operatör).
  { id: 'stone', label: 'Kağıt', glyph: '❖', scheme: 'light', contrast: 'aa', series: 'stone', themeColor: '#f4f2ee', ciCell: false },
] as const satisfies readonly ThemeEntryShape[];

export type ThemeEntry = (typeof THEME_REGISTRY)[number];
export type Theme = ThemeEntry['id'];

export const THEME_IDS: readonly Theme[] = THEME_REGISTRY.map(e => e.id);

export function isThemeId(v: unknown): v is Theme {
  return typeof v === 'string' && THEME_REGISTRY.some(e => e.id === v);
}

export function themeEntry(id: Theme): ThemeEntry {
  const e = THEME_REGISTRY.find(x => x.id === id);
  if (!e) throw new Error(`themeRegistry: bilinmeyen tema ${id}`);
  return e;
}

/** Kayıttan tema başına harita (Record<Theme, T>). */
export function byTheme<T>(pick: (e: ThemeEntry) => T): Record<Theme, T> {
  const out = {} as Record<Theme, T>;
  for (const e of THEME_REGISTRY) out[e.id] = pick(e);
  return out;
}

/** index.html açılış betiği için JS nesne metni: `{ dark: '#09090b', … }`. */
export function themeMapLiteral(pick: (e: ThemeEntry) => string): string {
  return `{ ${THEME_REGISTRY.map(e => `${e.id}: '${pick(e)}'`).join(', ')} }`;
}
