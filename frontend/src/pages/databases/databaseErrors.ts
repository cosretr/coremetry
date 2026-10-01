// databaseErrors — v0.10.1020 — /database "hangi hata" bölümünün saf çekirdeği
// (Databases × Dynatrace, dilim 2). Sunucu: internal/chstore/db_errors.go.

import type { DBErrorGroup, DBErrorKind } from '@/lib/types';

/** İmzanın ekranda okunan hâli; mesajsız hata boş imza taşır. */
export function dbErrorLabel(g: Pick<DBErrorGroup, 'signature' | 'kind'>): string {
  return g.kind === 'none' || !g.signature ? '(mesajsız hata)' : g.signature;
}

/** İmzanın kaynağı — kısa etiket. */
export function dbErrorKindLabel(kind: DBErrorKind): string {
  switch (kind) {
    case 'code': return 'hata kodu';
    case 'type': return 'exception';
    case 'message': return 'mesaj';
    default: return 'bilinmiyor';
  }
}

/** Pay: imzanın, listelenen toplam içindeki yüzdesi (toplam 0 ise null). */
export function dbErrorShare(count: number, total: number): number | null {
  return total > 0 ? (count / total) * 100 : null;
}

/** Çağıran özeti: "pay-api" ya da "pay-api +3". */
export function dbErrorServicesText(g: Pick<DBErrorGroup, 'topService' | 'services'>): string {
  if (!g.topService) return g.services > 0 ? `${g.services} servis` : '—';
  return g.services > 1 ? `${g.topService} +${g.services - 1}` : g.topService;
}
