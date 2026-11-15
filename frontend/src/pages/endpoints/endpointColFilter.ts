// endpointColFilter — v0.10.2209: Endpoints başlık hunisinin SAF sözlüğü ve
// URL normalizasyonu (sunucu internal/api/endpoints_list.go ile aynı küme).

export const ENDPOINT_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const;
export const ENDPOINT_STATUS_CLASSES = ['2xx', '3xx', '4xx', '5xx'] as const;

/** SAF: ?method= → geçerli fiil (büyük harf) ya da ''. */
export function normMethod(v: string | null): string {
  const m = (v ?? '').trim().toUpperCase();
  return (ENDPOINT_METHODS as readonly string[]).includes(m) ? m : '';
}

/** SAF: ?status= → 2xx..5xx ya da ''. */
export function normStatusClass(v: string | null): string {
  const s = (v ?? '').trim().toLowerCase();
  return (ENDPOINT_STATUS_CLASSES as readonly string[]).includes(s) ? s : '';
}

/** SAF: kolon sırasının saklama anahtarı — sekme başına (HTTP / RPC). */
export function endpointOrderKey(tableKey: string, entry: 'http' | 'rpc'): string {
  return `${tableKey}:${entry}:col-order`;
}
