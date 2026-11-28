import { NO_DATA, isNum, type Lang } from './lang';

// lib/format/time — göreli + mutlak zaman (v0.10.2484, denetim BÖLÜM 2).
//
// Kural (A7): göreli BİRİNCİL ("3 dk önce"), mutlak zaman hover'da
// (`<Time>` bileşeni, `title`). Mutlak damga izleyicinin saat diliminde,
// el yapımı ve 24 saat — v0.9.879 kuralı aynen: tarayıcı yereline bağlı
// `toLocale*` ABD tarayıcısında "11:22:34 PM" basıyordu. Gövdeler
// `lib/utils`'ten taşındı (fmtClock / fmtDateTime / ts*); `lib/utils`
// aynı adlarla yeniden dışa verir.

const p2 = (n: number) => String(n).padStart(2, '0');
const p3 = (n: number) => String(n).padStart(3, '0');

/** "HH:mm:ss", 24 saat. Unix-ms ya da Date alır. */
export function clock(v: number | Date): string {
  const d = v instanceof Date ? v : new Date(v);
  return `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
}

/** "dd.mm.yyyy HH:mm:ss", 24 saat. Unix-ms ya da Date alır. */
export function dateTime(v: number | Date): string {
  const d = v instanceof Date ? v : new Date(v);
  return `${p2(d.getDate())}.${p2(d.getMonth() + 1)}.${d.getFullYear()} ${clock(d)}`;
}

/** Mutlak damga (unix-ns): "dd.mm.yyyy HH:mm:ss"; 0 → "—". */
export function absTime(ns: number): string {
  if (!ns) return NO_DATA;
  return dateTime(ns / 1e6);
}
export const tsLong = absTime;

/** "HH:mm:ss.mmm" (unix-ns). */
export function tsShort(ns: number): string {
  if (!ns) return NO_DATA;
  const d = new Date(ns / 1e6);
  return clock(d) + '.' + p3(d.getMilliseconds());
}

/** Sıralanabilir "yyyy-mm-dd HH:mm:ss.mmm" (unix-ns) — v0.5.321. */
export function tsDateTime(ns: number): string {
  if (!ns) return NO_DATA;
  const d = new Date(ns / 1e6);
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${clock(d)}.${p3(d.getMilliseconds())}`;
}

/** Bu yılsa "dd.mm HH:mm:ss", değilse absTime — v0.10.1068 dar kolonlar. */
export function tsCompact(ns: number, nowMs: number = Date.now()): string {
  if (!ns) return NO_DATA;
  const d = new Date(ns / 1e6);
  if (d.getFullYear() !== new Date(nowMs).getFullYear()) return absTime(ns);
  return absTime(ns).replace(/^(\d\d\.\d\d)\.\d{4} /, '$1 ');
}

/** "dd.mm.yyyy HH:mm" (saniyesiz) — v0.9.660. */
export function tsMinute(ns: number): string {
  if (!ns) return NO_DATA;
  return absTime(ns).slice(0, -3);
}

/**
 * Göreli zaman (unix-ns). Geçmiş: TR "az önce" / "5 dk önce" / "3 sa önce" /
 * "2 gün önce"; EN "just now" / "5 min ago" / "3 h ago" / "2 days ago"
 * ("1 day ago"). Gelecek: TR "5 dk sonra", EN "in 5 min". İlk dakika:
 * 5 sn'den eskiyse "51 sn önce" / "51 s ago", değilse (ve gelecekte < 1 dk)
 * "az önce" / "just now" (saat kayması). Tam sayıya yuvarlar.
 */
/** Bu sürenin altındaki fark "az önce" yazılır (saat kayması payı). */
const REL_NOW_SEC = 5;

export function relTime(ns: number | null | undefined, lang: Lang, nowMs: number = Date.now()): string {
  if (!isNum(ns) || ns === 0) return NO_DATA;
  const diff = (nowMs - ns / 1e6) / 1000;
  const s = Math.abs(diff);
  // v0.10.2484 (operatör): ilk dakikada saniye korunur ("51 sn önce") — nöbette
  // "az önce" yetmiyor. 5 sn'nin altı ve gelecekteki damga (saat kayması) "az önce".
  if (s < REL_NOW_SEC || (diff < 0 && s < 60)) return lang === 'tr' ? 'az önce' : 'just now';
  if (s < 60) {
    const sec = Math.floor(s);
    return lang === 'tr' ? `${sec} sn önce` : `${sec} s ago`;
  }
  let n: number;
  let body: string;
  if (s < 3600) {
    n = Math.round(s / 60);
    body = lang === 'tr' ? `${n} dk` : `${n} min`;
  } else if (s < 86400) {
    n = Math.round(s / 3600);
    body = lang === 'tr' ? `${n} sa` : `${n} h`;
  } else {
    n = Math.round(s / 86400);
    body = lang === 'tr' ? `${n} gün` : `${n} ${n === 1 ? 'day' : 'days'}`;
  }
  if (diff >= 0) return lang === 'tr' ? `${body} önce` : `${body} ago`;
  return lang === 'tr' ? `${body} sonra` : `in ${body}`;
}

/**
 * "ne kadar önce" — relTime'ın yalnız geçmiş kipi: gelecekteki damga (saat
 * kayması) "az önce" / "just now" olur (agoTR / agoLang / fmtAgoNs sözleşmesi).
 */
export function agoTime(ns: number | null | undefined, lang: Lang, nowMs: number = Date.now()): string {
  if (!isNum(ns) || ns === 0) return NO_DATA;
  return relTime(Math.min(ns, nowMs * 1e6), lang, nowMs);
}
