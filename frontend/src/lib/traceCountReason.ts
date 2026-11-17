// v0.9.638 — sayım REDDEDİLDİĞİNDE operatöre sebebini söyle.
//
// Bazı şekiller trace_summary_5m'de ucuza sayılamıyor. "Yanlış sayı,
// sayı yokluğundan kötüdür" ilkesinin devamı: PAHALI sayı da dürüst bir
// retten kötüdür — 25 saniye bekletip zaman aşımına düşmektense
// nedenini söyleyip listeyi hızlı tutuyoruz.
//
// Sessiz bir "—" da yeterli değildi: operatör sayının neden gelmediğini
// bilmezse filtreyi gevşetmeyi denemez.

import type { TraceCountResponse } from './types';
import { t, type Lang } from './i18n';

type Reason = NonNullable<TraceCountResponse['reason']>;

// v0.10.2314 — metinler katalogda (trc.count.why.*); TR değerler birebir aynı.
const HINTS: Record<Reason, string> = {
  // Filtre MV hızlı yolunu zaten kapatıyor; sayım ham spans üzerinde
  // olurdu ve tam da kaçındığımız pahalı tarama bu.
  'raw-path-filter': 'trc.count.why.raw',
  // Süre trace_summary_5m'de iki merge state'in farkı — her satır için
  // finalize gerekir, LIMIT erken duramaz.
  'duration-filter': 'trc.count.why.duration',
  // Liste bunu iki tabloyu bağlayan bir alt-sorguyla karşılıyor; aynısı
  // sayımda DISTINCT'in erken durmasını öldürüyor.
  'service+filter': 'trc.count.why.svc',
  // NOT HAS: liste MV yolunda adayları sınırlı bir probla eler; pencere
  // geneli sayım tam pencere anti-join'i ister.
  'trace-exclude': 'trc.count.why.exclude',
};

/** traceCountReasonHint — ret sebebini operatör diline çevirir. */
export function traceCountReasonHint(reason: string, lang: Lang = 'tr'): string {
  return t(HINTS[reason as Reason] ?? 'trc.count.why.other', lang);
}
