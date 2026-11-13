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

// v0.10.2121 (Problems sadeleştirme S1) — "Tür" kolonunun etiketi. Kaynak
// türü (InboxItem.kind); bilinmeyen tür sunucunun insan okunur kaynağına
// (`source`) düşer.
const KIND_LABEL_TR: Record<string, string> = {
  problem: 'Alarm', exception: 'Exception', httperror: 'HTTP hatası', anomaly: 'Anomali', incident: 'Incident',
};

export function inboxKindLabel(it: Pick<InboxItem, 'kind' | 'source'>): string {
  return KIND_LABEL_TR[it.kind] ?? it.source;
}

// v0.10.2122 (Problems sadeleştirme S2) — üst sayım cümlesi, TEK satır, şerit
// değil: "N açık problem: a P1, b P2, c P3. En eskisi X gündür açık."
// Sayılar sunucunun öncelik çiplerinden (süzgeç ve tavandan önceki küme),
// "en eskisi" aynı kümeden (oldestFirstSeen). Sayı yoksa null (cümle yok).
export function inboxCountSentence(counts: Record<string, number> | undefined, oldestNs: number | undefined, nowNs: number): string | null {
  if (!counts) return null;
  const p1 = counts.P1 ?? 0, p2 = counts.P2 ?? 0, p3 = counts.P3 ?? 0;
  const n = p1 + p2 + p3;
  if (n === 0) return 'Açık problem yok.';
  const f = (v: number) => v.toLocaleString('tr-TR');
  let s = `${f(n)} açık problem: ${f(p1)} P1, ${f(p2)} P2, ${f(p3)} P3.`;
  if (oldestNs && oldestNs > 0 && nowNs > oldestNs) {
    s += ` En eskisi ${openForTR(nowNs - oldestNs)} açık.`;
  }
  return s;
}

/** SAF: süre → "3 gündür" / "5 saattir" / "12 dakikadır" (aşağı yuvarlar, en az 1). */
export function openForTR(ns: number): string {
  const min = Math.floor(ns / 60e9);
  if (min >= 24 * 60) return `${Math.floor(min / (24 * 60))} gündür`;
  if (min >= 60) return `${Math.floor(min / 60)} saattir`;
  return `${Math.max(1, min)} dakikadır`;
}
