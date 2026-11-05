/**
 * opPick — Traces sayfasında seçilen operasyon (v0.10.752; operatör:
 * "service ve altındaki sorguyu seçince listede operation name farklı
 * farklı çıkıyor").
 *
 * Sebep: OperationPicker'dan seçim `search=` alt-dizge aramasına
 * dönüşüyordu — trace'in HERHANGİ bir span'ında geçen metin (attr
 * değerleri dahil) eşleşir, liste ise KÖK span adını gösterir → seçilen
 * "SELECT …" için satırlarda gRPC/log kök adları görünür.
 *
 * Şimdi: seçim `?op=` taşır → sunucuya span düzeyinde TAM eşleşme çipi
 * (`operation = <ad>`, filterexpr.go `operation` → `name`); Name hücresi
 * seçilen operasyonu yazar, kök farklıysa ipucunda "Kök: …". Serbest
 * yazılan metin eskisi gibi `search=`.
 */
import type { FilterExpr } from '@/lib/types';

/** `?op=` → sunucu çipi; boş → yok. */
export function opChipFor(op: string): FilterExpr[] {
  const v = op.trim();
  return v ? [{ k: 'operation', op: '=', v: [v] }] : [];
}

/** Name hücresinin metni: seçilen operasyon varsa o, yoksa kök adı, o da yoksa '—'. */
export function opCellText(op: string, rootName: string | undefined): string {
  return op || rootName || '—';
}

/**
 * Name hücresinin ipucu: operasyon seçiliyken satır etiketi farklıysa onu
 * söyler. v0.10.1807 — servis süzgeci varken (tek `service`, `services`
 * yok) sunucu rootName'e o servisin GİRİŞ span'ini yazar, kökü değil;
 * etiket buna göre.
 */
export function opCellTitle(op: string, rootName: string | undefined, serviceScoped = false): string {
  const label = serviceScoped ? "Servis giriş span'i" : 'Kök';
  if (op) return rootName && rootName !== op ? `${label}: ${rootName}` : op;
  return rootName ?? '';
}
