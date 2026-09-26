// middleSplit.ts — v0.10.939 (tablo standardı T11): <MiddleEllipsis>'in saf
// yarısı. Bileşen dosyası yalnız bileşen dışa aktarsın diye ayrı (react-refresh).

/** Hiç kırpılmayan son ekin varsayılan uzunluğu (karakter). */
export const MIDDLE_ELLIPSIS_TAIL = 8;

/** Saf bölme: [baş, kuyruk]. Metin kuyruktan kısa/eşitse bölünmez (tek baş). */
export function splitMiddle(text: string, tail = MIDDLE_ELLIPSIS_TAIL): [string, string] {
  const n = Math.max(0, Math.floor(tail));
  if (n === 0 || text.length <= n) return [text, ''];
  return [text.slice(0, text.length - n), text.slice(text.length - n)];
}
