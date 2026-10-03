// inboxRowText — v0.10.1084 (operatör: "tekilleştir, Exceptions'taki format
// güzel"). Problems kuyruğu (/inbox) satırının Exceptions biçimindeki metni,
// SAF ve tablo testli (inboxRowText.test.ts); sayfa yalnız çizer.
//
//   başlık (kalın)  exception ailesi → tip; problem / incident → olayın DÜZ
//                   cümlesi (açıklama, yaşam döngüsü eki kırpılmış — sunucu
//                   inboxProblemSentence ve incidentSummary problemSentence
//                   ikizi); boşsa kural / incident adı; anomali → başlığı.
//   etiket          başlık cümleyse kural / incident adı ikinci satırın başında
//                   kalır (ne olduğu kaybolmasın); aynıysa tekrar etmez.
//   Occurrences     exception → oluşum; açık incident → bağlı problem sayısı
//                   ("2 problem", sunucu katlaması); diğerleri "—".
import type { InboxItem } from './types';
import { isInboxExcFamily } from './inboxHref';

// Açıklamaya eklenen yaşam döngüsü ekleri (olayın cümlesi değil).
const LIFECYCLE_SUFFIXES = [' · auto-resolved:', ' · resolved:'];

/** Açıklamanın tek cümlesi: yaşam döngüsü eki kırpılmış, kırpılmış boşluk. */
export function rowSentence(desc: string | undefined): string {
  let s = (desc ?? '').trim();
  for (const suf of LIFECYCLE_SUFFIXES) {
    const i = s.indexOf(suf);
    if (i >= 0) s = s.slice(0, i);
  }
  return s.trim();
}

/** Satırın kalın başlığı. */
export function inboxRowHeadline(it: InboxItem): string {
  // Sunucu exception satırında Title = tip (exceptionToInbox); tip yalnız yedek.
  if (isInboxExcFamily(it)) return it.title || it.exception?.type || '';
  if (it.kind === 'problem' || it.kind === 'incident') return rowSentence(it.description) || it.title;
  return it.title;
}

/** İkinci satırın başındaki ad (kural / incident adı) — başlık zaten oysa null. */
export function inboxRowLabel(it: InboxItem): string | null {
  if (it.kind !== 'problem' && it.kind !== 'incident') return null;
  const head = inboxRowHeadline(it);
  const title = (it.title ?? '').trim();
  return title && title !== head ? title : null;
}

/** Occurrences hücresi: sayı metni ya da null ("—"). */
export function inboxOccurrencesLabel(it: InboxItem): string | null {
  if (it.exception) return it.exception.occurrences.toLocaleString();
  const n = it.incident?.problemCount ?? 0;
  if (it.kind === 'incident' && n > 0) return `${n} problem`;
  return null;
}
