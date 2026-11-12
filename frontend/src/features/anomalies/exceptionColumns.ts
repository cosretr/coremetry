// exceptionColumns — v0.10.2023 (operatör 2026-11-12: "Exceptions error
// sayfasında filtre ve kolonlar yer değiştirebilir değil, traces'te gibi").
//
// /problems exception listesinin kullanıcı kolon sırası. SAF; /traces'in
// lib/traceColumns sözleşmesinin attribute kolonu olmayan hâli: sıra
// kayıtlıdan uzlaştırılır (lib/columnModel reconcileColumnModel — bilinmeyen
// id düşer, yeni kolon tanımdaki komşusunun yanına girer), kayıt yoksa
// tanım sırası (EXC_COLS) — yani varsayılan görünüm bugünküyle aynı.
// Sabit kolon yok (stickyLeft/actions yok): hepsi taşınır, hiçbiri kalkmaz.
import { reconcileColumnModel } from '@/lib/columnModel';

/** Kaydedilmiş sırayı (LS / sunucu tercihi) bugünkü kolon setiyle uzlaştırır. */
export function resolveExcOrder(declared: readonly string[], saved: readonly string[] | null | undefined): string[] {
  const cols = declared.map(id => ({ id }));
  if (!saved || saved.length === 0) return [...declared];
  return reconcileColumnModel({ v: 1, order: [...saved], hidden: [], sig: '' }, cols, '').order;
}

export function isDefaultExcOrder(declared: readonly string[], order: readonly string[]): boolean {
  return order.length === declared.length && order.every((id, i) => id === declared[i]);
}

/** localStorage girdisi → id listesi; bozuk / boş → null (varsayılana düşer). */
export function parseStoredExcOrder(raw: string | null): string[] | null {
  if (!raw) return null;
  try {
    const v: unknown = JSON.parse(raw);
    if (!Array.isArray(v)) return null;
    const ids = v.filter((c): c is string => typeof c === 'string' && c !== '');
    return ids.length ? ids : null;
  } catch {
    return null;
  }
}
