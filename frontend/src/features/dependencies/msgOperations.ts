// msgOperations.ts — v0.10.563 (Messaging Faz 4b). SAF: messaging_summary_5m'in
// operation boyutundan gelen satırların GÖRÜNEN etiketi.
//
// Neden ayrı bir dosya: '' değeri iki farklı şeyi ayırt ediyor ve bu ayrım
// çekmecenin JSX'i içine gömülünce test edilemez hâle geliyordu. '' bir
// EKSİKLİK ölçümü — "bu pencerede publish olmadı" değil, "SDK hiçbir
// messaging.operation.* niteliği yaymadı". Boş hücre bunu sıfır gibi
// okutur; etiket bunu açıkça söyler.
import type { MsgOperationStat } from '@/lib/types';
import { t, type Lang } from '@/lib/i18n'; // v0.10.2306 — etiket isteğe bağlı lang (varsayılan 'tr')

/** SDK operation niteliği yaymadığında görünen etiket. */
export const OP_MISSING_LABEL = t('mt.opMissing', 'tr');

/**
 * Hücrenin title'ı — etiketin NEDEN böyle olduğunu tek cümlede söyler.
 * Coalesce zinciri burada YAZILI: operatör hangi niteliği aramaya
 * gideceğini çekmeceden okur.
 */
export const OP_MISSING_TITLE = t('mt.opMissingTitle', 'tr');
/** v0.10.2306 — OP_MISSING_TITLE arayüz dilinde. */
export function opMissingTitle(lang: Lang = 'tr'): string { return t('mt.opMissingTitle', lang); }

/**
 * opLabelTR — ham operation değerini görünen etikete çevirir.
 *
 * Yalnız BOŞLUK durumu özel: bilinen türler (publish / receive / process /
 * settle / create …) semconv'un kendi sözcükleri ve ÇEVRİLMEZ — operatör
 * aynı sözcüğü SDK belgelerinde, span niteliğinde ve burada görmeli.
 * Sadece-boşluk dizeler de eksiklik sayılır (bir SDK boş dize yerine ' '
 * yayarsa iki farklı "eksik" satırı oluşmasın).
 */
export function opLabelTR(op: string | null | undefined, lang: Lang = 'tr'): string {
  const v = (op ?? '').trim();
  return v === '' ? t('mt.opMissing', lang) : v;
}

/** Etiket eksiklik mi anlatıyor (soluk render + title için). */
export function isOpMissing(op: string | null | undefined): boolean {
  return (op ?? '').trim() === '';
}

/**
 * msgOperationRows — sunucu zarfını tabloya girecek dizi hâline getirir.
 *
 * `undefined` (pre-563 ısınmış önbellek) ve `null` (Go nil → JSON null)
 * ikisi de BOŞ DİZİ; çekmece hook'u erken dönüşlerden önce çağrıldığı için
 * `data` henüz yokken de çağrılır (rules-of-hooks).
 */
export function msgOperationRows(
  ops: MsgOperationStat[] | null | undefined,
): MsgOperationStat[] {
  return Array.isArray(ops) ? ops : [];
}
