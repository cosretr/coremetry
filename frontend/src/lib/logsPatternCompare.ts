// logsPatternCompare — v0.10.1897 (Log Search Faz 4 dilim 4.4). Desenler
// sekmesinin "Karşılaştır" seçimi: URL `cmp` (prev | 1d | 7d), karşılaştırma
// aralığı (B) sayfa penceresinden (A) türetilir. Kullanıcı seçmedikçe istek yok.
// SAF; tablo testi lib/logsPatternCompare.test.ts.

export type PatternCompareMode = 'prev' | '1d' | '7d';

export const PATTERN_COMPARE_MODES: PatternCompareMode[] = ['prev', '1d', '7d'];

export const PATTERN_COMPARE_LABEL: Record<PatternCompareMode, string> = {
  prev: 'Önceki aralık',
  '1d': '1 gün önce',
  '7d': '1 hafta önce',
};

/** URL değeri → mod; tanınmayan / boş → null (karşılaştırma kapalı). */
export function parsePatternCompare(v: string | null | undefined): PatternCompareMode | null {
  return PATTERN_COMPARE_MODES.includes(v as PatternCompareMode) ? (v as PatternCompareMode) : null;
}

const DAY_NS = 86_400 * 1e9;
const MAX_SPAN_NS = 31 * DAY_NS; // sunucu sınırı (patternsCompareMaxSpan)

/**
 * B aralığı (unix ns). prev = hemen önceki eşit aralık; 1d / 7d = aynı uzunluk,
 * 1 gün / 1 hafta geri. Pencere yoksa ya da 31 günü aşarsa null.
 */
export function patternCompareWindow(mode: PatternCompareMode, fromNs?: number, toNs?: number): { bfrom: number; bto: number } | null {
  if (!fromNs || !toNs || !(toNs > fromNs)) return null;
  const span = toNs - fromNs;
  if (span > MAX_SPAN_NS) return null;
  const shift = mode === 'prev' ? span : mode === '1d' ? DAY_NS : 7 * DAY_NS;
  return { bfrom: fromNs - shift, bto: toNs - shift };
}

export type CompareVerifiedText = { text: string; tone: 'ok' | 'warn' | 'muted' };

/**
 * Doğrulama metni. kind 'new' → öbür aralık = karşılaştırılan; 'gone' → bu aralık.
 * "yes" gerçek sayımla 0; "no" örneklem kaçırmış (satır var) → yeni/kaybolan DEĞİL.
 */
export function compareVerifiedText(kind: 'new' | 'gone', v: string | undefined): CompareVerifiedText {
  const where = kind === 'new' ? 'karşılaştırılan aralıkta' : 'bu aralıkta';
  switch (v) {
    case 'yes': return { text: `doğrulandı: ${where} 0 satır`, tone: 'ok' };
    case 'no': return { text: `${where} da var (örneklem kaçırmış)`, tone: 'warn' };
    case 'unknown': return { text: 'doğrulanamadı (zaman aşımı ya da kısmi yanıt)', tone: 'muted' };
    default: return { text: 'doğrulanmadı (ilk 10 dışında)', tone: 'muted' };
  }
}
