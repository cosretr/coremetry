// httpStatusDisplay — v0.10.1519 (operator-reported, prod): /traces listesinin
// http.status_code kolonu uzun (11–18 sn) çağrılarda ve Kafka producer
// span'lerinde "0" yazıyordu. 0 bir HTTP durumu DEĞİL: spans.http_status
// UInt16 DEFAULT 0, yani durum attribute'u olmayan her span (HTTP dışı span ya
// da yanıt hiç gelmemiş istek — istemci zaman aşımı / bağlantı sıfırlanması)
// 0 saklar. Gösterim "—" + ipucu; süzgeç değeri "0" AYNEN kalır
// (`http.status_code = 0` = "durumu olmayan span'ler", anlamı değişmez).
// Saf ve tablo-testli.
import { t, type Lang } from './i18n';

/** http_status kolonuna düşen anahtarlar (filterexpr.go wellKnown ile aynı). */
export const HTTP_STATUS_KEYS: readonly string[] = ['http.status_code', 'http.response.status_code'];

export const NO_HTTP_STATUS_TITLE = 'yanıt yok / HTTP dışı span';

export function isHttpStatusKey(key: string | null | undefined): boolean {
  return !!key && HTTP_STATUS_KEYS.includes(key);
}

/** Durum değeri "yok" mu: boş (sunucu artık 0'ı boş döner) ya da eski yanıtın "0"ı. */
export function isMissingHttpStatus(v: string | null | undefined): boolean {
  const t = (v ?? '').trim();
  return t === '' || t === '0';
}

/** Hücre metni + ipucu. HTTP durumu anahtarı değilse değer aynen (boş → "—"). */
export function httpStatusCell(key: string, v: string | null | undefined, lang: Lang = 'tr'): { text: string; title: string; missing: boolean } {
  const raw = v ?? '';
  if (isHttpStatusKey(key) && isMissingHttpStatus(raw)) {
    // v0.10.2314 — ipucu arayüz dilinde (trc.noHttpStatus; TR = NO_HTTP_STATUS_TITLE)
    return { text: '—', title: t('trc.noHttpStatus', lang), missing: true };
  }
  return { text: raw || '—', title: raw, missing: !raw };
}

/** Aggregated "group by HTTP status" anahtarı ('0' = durumsuz span'ler). */
export function httpStatusGroupLabel(groupKey: string): { text: string; title?: string } {
  return isMissingHttpStatus(groupKey) ? { text: '—', title: `${NO_HTTP_STATUS_TITLE} (http.status_code = 0)` } : { text: groupKey };
}
