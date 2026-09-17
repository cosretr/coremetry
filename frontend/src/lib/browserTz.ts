/**
 * browserTz — tarayıcının saat dilimi çifti (v0.10.745).
 *
 * Sohbet bağlamı bunu v0.10.437/445'ten beri gönderiyordu; Explain ve
 * insight uçları göndermiyordu → sunucu kanıt damgalarını UTC yazıyor,
 * model onu yerel saat sanıp aktarıyordu (operatör bildirimi: "tepe
 * 22:30" dedi, ekran 01:30 gösteriyordu). Üç yol artık aynı çifti
 * buradan alır: hangisi eksik kalırsa kalsın, tek yerde görünür.
 *
 * - tzOffsetMin: UTC'den dakika, DOĞU POZİTİF (İstanbul +180).
 *   getTimezoneOffset UTC'nin GERİSİNİ verir → işaret çevrilir.
 * - tz: IANA adı ("Europe/Istanbul"); sabit ofset DST'yi bilmez, sunucu
 *   adı çözebilirse onu kullanır. Intl yoksa/atarsa ''.
 */
export interface BrowserTz {
  tz: string;
  tzOffsetMin: number;
}

export function browserTz(now: Date = new Date()): BrowserTz {
  const tzOffsetMin = -now.getTimezoneOffset();
  let tz = '';
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone ?? '';
  } catch {
    tz = '';
  }
  return { tz, tzOffsetMin };
}

/** Sorgu dizesi parçası (`tz=…&tzOffsetMin=…`), başında ayırıcı yok; ikisi de boşsa ''. */
export function tzQuery(t: BrowserTz = browserTz()): string {
  const parts: string[] = [];
  if (t.tz) parts.push('tz=' + encodeURIComponent(t.tz));
  if (t.tzOffsetMin !== 0) parts.push('tzOffsetMin=' + String(t.tzOffsetMin));
  return parts.join('&');
}

/** Sorgu dizesini `path`e ekler (mevcut `?` varsa `&` ile); boşsa `path` aynen. */
export function withTzQuery(path: string, t: BrowserTz = browserTz()): string {
  const q = tzQuery(t);
  if (!q) return path;
  return path + (path.includes('?') ? '&' : '?') + q;
}

/** JSON gövdesine giren alanlar — sıfır/boş olanlar atlanır (sohbet bağlamıyla aynı kural). */
export function tzBodyFields(t: BrowserTz = browserTz()): Partial<BrowserTz> {
  return { ...(t.tzOffsetMin !== 0 ? { tzOffsetMin: t.tzOffsetMin } : {}), ...(t.tz ? { tz: t.tz } : {}) };
}
