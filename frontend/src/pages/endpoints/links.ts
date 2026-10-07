import { encodeRange, encodeFilters, buildQuery } from '@/lib/urlState';
import type { FilterExpr, TimeRange } from '@/lib/types';
import { NAME_SHAPE_FILTER_KEY, ROUTE_SHAPE_FILTER_KEY } from '@/lib/filterQuery';

// links.ts — the /endpoints family's outbound pivots (v0.9.839).
//
// Both generators lived inside Endpoints.tsx while the drill-down was a
// drawer and a modal in the same file. The drill-down is now its own
// PAGE, and both surfaces still need the identical links: duplicating
// them is how two pivots off the same row drift into asking different
// questions. One module, one definition.
//
// The parameters take only the IDENTITY ({service, path} + optional
// sig/entry) — never a whole EndpointRow. The detail page must build these
// links from a deep link that has no row behind it, and a signature that
// demanded a row would force a fabricated one.

/**
 * EndpointPivotRef — v0.10.1117: satır kimliği + satırın NASIL kurulduğu.
 * `sig` = satır "Group by shape" şekli (/users/:id); `entry: 'rpc'` = RPC &
 * Messaging sekmesi (yol sütunu span ADI, http_route tanım gereği boş).
 * İkisi de yoksa bugünkü HTTP ham rota kimliği.
 */
export interface EndpointPivotRef {
  service: string;
  path: string;
  sig?: boolean;
  entry?: 'http' | 'rpc';
}

/**
 * endpointIdentityFilter — v0.10.1117 (boş liste düzeltmesi): satırın kimliği,
 * /endpoints'in o satırı GRUPLADIĞI ifadeyle eşleşen TEK çip. SAF.
 *
 *   HTTP ham   → `http.route = <rota>`        (değişmedi)
 *   HTTP şekil → `http.route_shape = <şekil>` (sunucu: opSigWrap(http_route))
 *   RPC ham    → `name = <span adı>`          (http_route RPC'de '' — eski
 *                `http.route = <ad>` HER ZAMAN boş dönüyordu)
 *   RPC şekil  → `name_shape = <şekil>`       (sunucu: opSigWrap(name))
 *
 * Şekil `http.route = /users/:id` olarak gidince span'ler ham id taşıdığında
 * hiçbir satır eşleşmiyordu (v0.10.1115'in Operations › Normalized ikizi).
 */
export function endpointIdentityFilter(r: EndpointPivotRef): FilterExpr {
  const rpc = r.entry === 'rpc';
  if (r.sig) return { k: rpc ? NAME_SHAPE_FILTER_KEY : ROUTE_SHAPE_FILTER_KEY, op: '=', v: [r.path] };
  return { k: rpc ? 'name' : 'http.route', op: '=', v: [r.path] };
}

/**
 * tracesLink — /traces filtered to this endpoint.
 *
 * v0.9.307 — env/cluster ride the pivot. Without them a row read under
 * env=uat opened an UNFILTERED trace list: the pivot silently widened
 * the question it was launched from.
 *
 * v0.9.1372 — iki değişiklik, ikisi de operatör isteği:
 *
 * (a) `search=<path>` YERİNE yapısal `http.route = <path>` filtresi.
 *     `search` bir SERBEST METİN eşleşmesi: span adında VEYA
 *     özniteliklerde geçen her şeyi tutuyordu, yani `/api/v1/pay`
 *     araması `/api/v1/payment-retry`i de getiriyordu ve pivot,
 *     başlatıldığı satırdan farklı bir soru soruyordu. Doğru kodlama
 *     zaten bu dosyada, on satır aşağıda duruyordu (exploreLink'in
 *     `http.route` filtresi) — iki pivot aynı satırdan çıkıp farklı
 *     evrene gidiyordu.
 *
 * (b) `rootOnly=false` YERİNE `rootOnly=auto`. Operatör root seçili bir
 *     liste istiyor (endpoint'in trace'i = onu çağıran akışın tamamı),
 *     ama bu deployment'ta bir kısım endpoint root span DEĞİL — mesaj
 *     tüketicilerinin ve iç servislerin ortasında yaşıyorlar ve root
 *     filtresi onlarda her zaman sıfır döndürür. `auto` niyeti taşıyor,
 *     kararı /traces sonuca bakarak veriyor (`traces/rootOnlyFallback`).
 *
 * v0.10.789 (ekip isteği, operatör onayı 2026-09-19) — `auto` → `false`.
 *     "Traces tıklandığında Root tikli sayfaya yönlendiriyor ve daha az
 *     trace geliyor; Root seçili olmasın." `auto` yalnız SIFIR sonuçta
 *     düşüyordu; root'u olan ama çoğu trace'i root olmayan bir endpoint'te
 *     liste küçük kalıyordu (endpoint'in kendi span'i genellikle root
 *     değil: gateway → servis). Pivot artık endpoint'in TÜM trace'lerini
 *     açar; Root kutusu /traces'te bir tık uzakta. 1372'nin (a) yarısı
 *     (yapısal http.route filtresi) aynen.
 */
export function tracesLink(
  r: EndpointPivotRef, range: TimeRange, env?: string, cluster?: string,
): string {
  // v0.10.1117 — kimlik çipi satırın kuruluşuna göre (endpointIdentityFilter).
  const filters = encodeFilters([endpointIdentityFilter(r)]);
  return `/traces?${buildQuery([
    ['service', r.service],
    ['filters', filters],
    ['range', encodeRange(range)],
    ['env', env ?? ''],
    ['cluster', cluster ?? ''],
    ['view', 'list'],
    ['rootOnly', 'false'],
  ])}`;
}

/**
 * exploreLink — "Open in Explore →" (v0.9.307, brief N6b).
 *
 * Zero new queries: http.route is already a resolver tier dimension
 * (TIER_DIM_KEYS), so Explore answers this from the spanmetrics
 * rollups rather than raw spans. The URL is the SAME legacy
 * ?result=metric shape OperationsTable already emits — no new scheme
 * invented, and seedFromLegacyParams decodes it unchanged.
 */
export function exploreLink(
  r: EndpointPivotRef, range: TimeRange, agg: string,
  env?: string, cluster?: string,
): string {
  // v0.10.1117 — şekil çipi rollup boyutu değil: Explore o hâlde ham spans
  // yolundan cevaplar (TIER_DIM_KEYS dışı); ham rota / ad yine kademelerden.
  const filters = encodeFilters([
    { k: 'service.name', op: '=', v: [r.service] },
    endpointIdentityFilter(r),
  ]);
  return `/explore?${buildQuery([
    ['range', encodeRange(range)],
    ['filters', filters],
    ['agg', agg],
    ['field', 'duration_ms'],
    ['result', 'metric'],
    ['env', env ?? ''],
    ['cluster', cluster ?? ''],
  ])}`;
}
