// svcSlowdownPivots — v0.10.1091: "yaygın yavaşlama" (`svc-slowdown:<servis>`)
// Problem'inin pivotları. Operatör: "Dün söylediğim CRM sorunu yine oldu, bir
// sürü anomali geldi ama P1 problem gelmedi" — kural artık servis üzerinde P1
// açıyor; detay sayfası onu servis sayfasına, Operations sekmesine (hangi
// operasyonlar yavaşladı) ve problem penceresinin yavaş trace'lerine bağlar.
//
// Yeni link üreticisi YOK: serviceHref (pencere + sekme) ve slowTracesHref
// (eşik + pencere) aynen.
//
// Yavaş trace eşiği BİLEREK Problem'in threshold'u DEĞİL: o, en yavaş
// operasyonun kendi tabanı (ör. 120 ms; öncelik oranı için — kural ateşlediğinde
// daima P1). Süzgeç kuralın mutlak tabanı minP99Ms (ayar, vars. 5000 ms) —
// çağıran onu sunucunun dizi cevabından (`AlertRuleSeries.threshold`) geçirir.
//
// SAF: React yok, yalnız href.

import { serviceHref } from '@/lib/serviceHref';
import { slowTracesHref } from './slowTracesHref';

/** Go chstore.RuleSvcSlowdownPrefix ikizi (testle pinli). */
export const SVC_SLOWDOWN_RULE_PREFIX = 'svc-slowdown:';

/** Kuralın varsayılan operasyon p99 tabanı (Go DefaultServiceSlowdown.MinP99Ms);
 *  ayar değeri henüz gelmediyse yavaş trace süzgeci buna düşer. */
export const SVC_SLOWDOWN_DEFAULT_MIN_P99_MS = 5000;

export function isSvcSlowdownRule(ruleId: string | null | undefined): boolean {
  return (ruleId ?? '').startsWith(SVC_SLOWDOWN_RULE_PREFIX) && (ruleId ?? '').length > SVC_SLOWDOWN_RULE_PREFIX.length;
}

export interface SvcSlowdownPivots {
  /** Servis sayfası (problem penceresi). */
  serviceHref: string;
  /** Operations sekmesi — hangi operasyonlar yavaşladı. */
  operationsHref: string;
  /** Problem penceresinde ≥ minP99Ms yavaş trace'ler (kök olmayan span'ler dahil). */
  slowTracesHref: string;
  /** Yavaş trace süzgecinin ms eşiği (etiket için). */
  slowMinMs: number;
}

// svcSlowdownPivots — yaygın yavaşlama problemi değilse null. minP99Ms: ayardaki
// operasyon p99 tabanı (sunucu dizi cevabının threshold'u); yok / geçersiz →
// varsayılan 5000 ms. Problem'in threshold'u KULLANILMAZ.
export function svcSlowdownPivots(
  p: { ruleId?: string | null; service?: string | null },
  window: { fromNs: number; toNs: number },
  minP99Ms?: number | null,
): SvcSlowdownPivots | null {
  if (!isSvcSlowdownRule(p.ruleId) || !p.service) return null;
  const slowMinMs = typeof minP99Ms === 'number' && Number.isFinite(minP99Ms) && minP99Ms > 0
    ? minP99Ms : SVC_SLOWDOWN_DEFAULT_MIN_P99_MS;
  return {
    serviceHref: serviceHref(p.service, { range: window }),
    operationsHref: serviceHref(p.service, { range: window, tab: 'operations' }),
    slowTracesHref: slowTracesHref(p.service, slowMinMs, window),
    slowMinMs,
  };
}
