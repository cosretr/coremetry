// bandFill — v0.10.2443 (Runtime kartları, madde 3). CorePanel'in alt–üst
// bant dolgusu (`fillBetween`) için ÇÖZÜLMÜŞ rengi saydamlaştırır. SAF:
// renk token'ı çağıran tarafta resolveVar'dan geçmiş gelir (tema-canlı).
//
// Eski TimeSeriesPanel zarfının (`envelopeFill`, ~%13 alfa) birebir karşılığı;
// hex (#rgb / #rrggbb), rgb() ve rgba() biçimleri anlaşılır, tanınmayan metin
// AYNEN döner (uPlot yine doldurur, yalnız opak — bant kaybolmaz).

export const BAND_FILL_ALPHA = 0.13;

export function bandFillColor(color: string, alpha: number = BAND_FILL_ALPHA): string {
  const c = color.trim();
  const a = Math.min(1, Math.max(0, alpha));
  let m = /^#([0-9a-f])([0-9a-f])([0-9a-f])$/i.exec(c);
  if (m) return `rgba(${parseInt(m[1] + m[1], 16)}, ${parseInt(m[2] + m[2], 16)}, ${parseInt(m[3] + m[3], 16)}, ${a})`;
  m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})(?:[0-9a-f]{2})?$/i.exec(c);
  if (m) return `rgba(${parseInt(m[1], 16)}, ${parseInt(m[2], 16)}, ${parseInt(m[3], 16)}, ${a})`;
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:\s*[,/]\s*[\d.%]+)?\s*\)$/i.exec(c);
  if (m) return `rgba(${m[1]}, ${m[2]}, ${m[3]}, ${a})`;
  return c;
}
