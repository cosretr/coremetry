// spread.ts — v0.10.949 (operatör 2026-09-26: "aynı anda farklı servislerden
// gelmiyorsa 5'ten düşük exception'ı göstermeye gerek yok"). Inbox ve
// /problems Exceptions'ın ORTAK saf yardımcıları: satır yayılım işareti,
// ipucu metni ve yanıttaki taban alanlarının okunması.
//
// Sunucu (internal/api/exception_spread.go) her exception/httperror satırına
// `spread` (aynı exception aynı anda kaç serviste, kendisi dahil) ve
// `spreadServices` (ortakların ilk 5'i, alfabetik) yazar; alanlar yalnız
// spread ≥ 2 iken gelir. "Aynı anda" penceresi (±dk) yanıttaki
// `spreadWindowMin` — istemci kendi sabitini tutmaz.
//
// Okuyucular `in` daraltmasıyla yazılı: alanların lib/types.ts/api.ts
// karşılığı ayrı birleştirme adımında geliyor; iki hâlde de tip güvenli
// (cast yok).

/** Satırın yayılım işareti: n = servis sayısı (kendisi dahil), partners = ortaklar (≤5). */
export type SpreadMark = { n: number; partners: string[] };

/** Satır (ExceptionGroup ya da InboxItem.exception) → yayılım işareti; spread < 2 → null. */
export function spreadOf(row: object | null | undefined): SpreadMark | null {
  if (!row || !('spread' in row)) return null;
  const n = typeof row.spread === 'number' ? row.spread : 0;
  if (!(n >= 2)) return null;
  const raw = 'spreadServices' in row && Array.isArray(row.spreadServices) ? row.spreadServices : [];
  const partners = raw.filter((s): s is string => typeof s === 'string');
  return { n, partners };
}

/**
 * İşaretin ipucu (tam değer ipucunda — tablo standardı T11):
 * "Aynı exception aynı anda 3 serviste: a, b (+1) · pencere ±10 dk".
 * `+k` = adı yazılmayan ortaklar (n − 1 − partners).
 */
export function spreadTitle(n: number, partners: readonly string[], windowMin?: number): string {
  const rest = Math.max(0, n - 1 - partners.length);
  const names = partners.length ? partners.join(', ') + (rest > 0 ? ` (+${rest})` : '') : '';
  const win = typeof windowMin === 'number' && windowMin > 0 ? ` · pencere ±${windowMin} dk` : '';
  return `Aynı exception aynı anda ${n} serviste${names ? `: ${names}` : ''}${win}`;
}

/**
 * Varsayılan taban şeridinin ipucu — kural cümlesi. `floor` sunucunun etkin
 * tabanı (min(5, P1 eşiği)), `windowMin` "aynı anda" penceresi.
 *
 * v0.10.949 (operatör kararı 2026-09-26) — regressed gruplar (çözülmüş, sonra
 * yeniden görülmüş) tabanın altında ve tek serviste de olsa gösterilir;
 * kural cümlesi bunu söyler (sunucu: exceptionIsRegressed / FloorExemptRegressed).
 */
export function defaultFloorTitle(floor: number, windowMin?: number): string {
  const win = typeof windowMin === 'number' && windowMin > 0 ? ` (±${windowMin} dk)` : '';
  return `Varsayılan kural: ${floor}+ oluşumlu gruplar, regressed gruplar (çözülmüş, sonra yeniden görülmüş) `
    + `ve aynı anda${win} en az 2 serviste görülen gruplar gösterilir. `
    + `${floor}'in altında, tek serviste kalan ve regressed olmayan gruplar gizli — hepsini görmek için "show all".`;
}

/**
 * v0.10.949 — yayılım okunamadığında (sunucu `spreadAvailable: false`:
 * CH hatası ya da hata sonrası backoff) varsayılan şeridin ipucu. Taban
 * istisnasız uygulandı; gizli sayı o an çoklu-servis grupları da içerir.
 */
export function spreadOffFloorTitle(floor: number): string {
  // v0.10.949 — regressed istisnası yayılımdan bağımsız: yayılım okunamasa da
  // regressed gruplar görünür kalır.
  return `Çoklu-servis istisnası geçici olarak kullanılamıyor (yayılım okunamadı): `
    + `${floor}'in altındaki gruplar gizli, aynı anda birden çok serviste görülenler dahil — `
    + `yalnız regressed gruplar (çözülmüş, sonra yeniden görülmüş) gösteriliyor. Hepsini görmek için "show all".`;
}

/** Yanıttaki taban alanları (Inbox + /api/exception-groups). Hepsi opsiyonel. */
export type FloorMeta = {
  minOcc?: number;
  hiddenByMinOcc?: number;
  keptBySpread?: number;
  /** v0.10.949 — tabanın altında olup regressed olduğu için gösterilen satırlar (Inbox). */
  keptRegressed?: number;
  floorDefault?: boolean;
  spreadWindowMin?: number;
  /**
   * v0.10.949 — false: yayılım okuması soft-fail, istisna uygulanmadı.
   * undefined = eski/önbellekteki gövde → "var" sayılır (geri uyum).
   */
  spreadAvailable?: boolean;
};

const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);

/** Yanıt gövdesinden taban alanlarını okur; eksik/yanlış tipli alan undefined. */
export function readFloorMeta(body: object | null | undefined): FloorMeta {
  if (!body) return {};
  const out: FloorMeta = {};
  if ('minOcc' in body) out.minOcc = num(body.minOcc);
  if ('hiddenByMinOcc' in body) out.hiddenByMinOcc = num(body.hiddenByMinOcc);
  if ('keptBySpread' in body) out.keptBySpread = num(body.keptBySpread);
  if ('keptRegressed' in body) out.keptRegressed = num(body.keptRegressed);
  if ('spreadWindowMin' in body) out.spreadWindowMin = num(body.spreadWindowMin);
  if ('floorDefault' in body && typeof body.floorDefault === 'boolean') out.floorDefault = body.floorDefault;
  if ('minOccDefault' in body && typeof body.minOccDefault === 'boolean') out.floorDefault = body.minOccDefault;
  if ('spreadAvailable' in body && typeof body.spreadAvailable === 'boolean') out.spreadAvailable = body.spreadAvailable;
  return out;
}
