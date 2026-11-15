// openSections — Clusters / Pod sayfalarının kapalı bölümleri için "açılan
// bölüm sonraki ziyarette açık kalır" kalıcılığının SAF çekirdeği (plan
// docs/frontend/clusters-shadcn-plan.md §0 D6; emsal Service.tsx
// svcDetailsOpen + pages/service/detailsSummaries.ts). Saklama tarayıcı
// başına, JSON kimlik dizisi; okuma/yazma lib/storage (try/catch sarılı).

/** SAF: saklı ham değer → açık bölüm kümesi (bozuk / bilinmeyen kimlik yok sayılır). */
export function parseOpenSections<T extends string>(raw: string | null | undefined, known: readonly T[]): Set<T> {
  const out = new Set<T>();
  if (!raw) return out;
  try {
    const v: unknown = JSON.parse(raw);
    if (Array.isArray(v)) {
      for (const x of v) if ((known as readonly unknown[]).includes(x)) out.add(x as T);
    }
  } catch { /* bozuk değer = hepsi kapalı */ }
  return out;
}

/** SAF: küme → saklanacak ham değer (bilinen sırayla). */
export function serializeOpenSections<T extends string>(s: ReadonlySet<T>, known: readonly T[]): string {
  return JSON.stringify(known.filter(id => s.has(id)));
}
