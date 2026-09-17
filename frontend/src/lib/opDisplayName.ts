/**
 * opDisplayName — çıplak HTTP fiili olan span adı için gösterim adı
 * (v0.10.756; trace bütünlüğü denetimi Q3-#1, operatör onayı).
 *
 * OTel enstrümantasyonu http.route yokken span adını yalnız fiil ("POST")
 * yapar; ham `name` OTel'e sadık kalır (invariant #1), gösterim ise kök
 * span'ın route'unu (trace_summary_5m entry_route_state / ham anyIf) ekler:
 * "POST /shop/orders/:id". Route yoksa ya da ad çıplak fiil değilse ad
 * olduğu gibi. Yalnız Traces listesi (kök span'ın route'u bilinir);
 * seçici/anomali başlığı ham ad üzerinden çalışır (bir ad çok route).
 */
const HTTP_METHODS = new Set(['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS', 'TRACE', 'CONNECT']);

export function isBareHTTPMethod(name: string | undefined | null): boolean {
  return HTTP_METHODS.has((name ?? '').trim().toUpperCase());
}

export function opDisplayName(name: string | undefined | null, route?: string | null): string {
  const n = (name ?? '').trim();
  const r = (route ?? '').trim();
  if (n && r && isBareHTTPMethod(n)) return `${n} ${r}`;
  return name ?? '';
}
