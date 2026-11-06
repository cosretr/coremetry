// logBusinessFields — v0.10.1853 (Log Search Faz 2 dilim 2.2; audit §8 "İş
// alanları"). `system_settings['logs_search'].businessFields` (etiket → ES alan
// adayları) eşlemedeki gerçek alana çözülür. SAF; logBusinessFields.test.ts.
//
// Kural: ilk aday eşlemede (/api/logs/fields) VARSA o alan. Eşleme kırpılmışsa
// (fieldsTotal > fields.length) ya da hiçbir aday listede yoksa ama liste
// kırpıksa ilk aday "doğrulanmamış" olarak kalır — kırpılmış listede yok demek
// eşlemede yok demek değil. Eşleme tam ve aday yoksa alan çıkarılır. Alan
// listesi BOŞSA (CH log yolu — eşleme keşfi yok, ya da okunamadı) bölüm boş:
// yeni özellik CH'de sessizce kapalı (operatör 2026-11-06: prod logları ES'te).
import type { LogsBusinessField } from '@/lib/types';

export interface ResolvedBusinessField {
  label: string;
  /** Sorgulanacak ES alanı. */
  field: string;
  /** Alan eşleme listesinde görüldü mü (false = kırpılmış eşlemede ilk aday). */
  verified: boolean;
}

export function resolveBusinessFields(
  defs: readonly LogsBusinessField[] | undefined,
  fields: readonly string[],
  fieldsTotal?: number,
): ResolvedBusinessField[] {
  if (!defs?.length || fields.length === 0) return [];
  const known = new Set(fields);
  const clipped = typeof fieldsTotal === 'number' && fieldsTotal > fields.length;
  const out: ResolvedBusinessField[] = [];
  const seen = new Set<string>();
  for (const d of defs) {
    const hit = d.fields.find(f => known.has(f));
    const field = hit ?? (clipped ? d.fields[0] : undefined);
    if (!field || seen.has(field)) continue;
    seen.add(field);
    out.push({ label: d.label, field, verified: !!hit });
  }
  return out;
}
