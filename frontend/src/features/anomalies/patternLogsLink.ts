// patternLogsLink — /anomalies desen kartının "logs ↗" bağlantısının SAF
// çekirdeği (v0.10.1071'te streams.tsx'ten taşındı: bileşen dosyası yalnız
// bileşen dışa aktarsın — react-refresh; davranış aynen + `pattern=`).

import type { LogPatternAnomaly } from '@/lib/types';
import { logsHref } from '@/lib/logsUrl';

// logsLinkForPattern — v0.5.306. /logs URL narrowed to the service AND the
// detector pattern.
//
// v0.10.1071 (operatör, prod ES: anomali "Logları aç" grafiğin saydığından
// başka satırlar gösterdi) — desen artık `pattern=<ad>` ile gider; token'lar
// arama metnine ÇEVRİLMEZ. Eskiden yalnız tek token'lı desende `q` yazılıyor
// (çok token'lı desen servisin tüm loglarına iniyordu); şimdi her desen
// sunucuda dedektörün kendi yüklemiyle süzülür (CH: token ön süzgeci + regex,
// ES: dedektörün query_string'i) — liste dedektörün saydığıdır. Servis
// `service=`de kalır (v0.9.1386: CH'de serbest metin servisi bulamaz).
//
// v0.9.862 (UX denetimi Ö2) — PENCERE: desenin son görülmesi etrafında 30 dk
// öncesi + 10 dk sonrası (patternLogWindow); damga bozuksa '' → `|| null`
// üreticinin açık reddi ("pencere yok" bir karar, unutkanlık değil).
export function logsLinkForPattern(a: Pick<LogPatternAnomaly, 'pattern' | 'service' | 'lastSeenNs'>): string {
  return logsHref({
    window: patternLogWindow(a.lastSeenNs) || null,
    service: a.service || undefined,
    pattern: a.pattern || undefined,
    panel: 'patterns', // v0.10.449 (C8 üretici) — anomali → desen paneli açık iner
  });
}

// patternLogWindow — v0.9.862. lastSeenNs etrafındaki /logs penceresi,
// `custom:<fromMs>-<toMs>` olarak. Damga yoksa/bozuksa '' döner ve çağıran
// paramı hiç yazmaz: decodeRange'in reddedeceği bir token yazmak adres
// çubuğunda kendinden emin ama SAHTE bir pencere gösterirdi.
export function patternLogWindow(lastSeenNs: number | undefined | null): string {
  if (!lastSeenNs || !Number.isFinite(lastSeenNs)) return '';
  const fromMs = Math.floor((lastSeenNs - 30 * 60 * 1e9) / 1e6);
  const toMs = Math.ceil((lastSeenNs + 10 * 60 * 1e9) / 1e6);
  if (!(fromMs > 0) || !(toMs > fromMs)) return '';
  return `custom:${fromMs}-${toMs}`;
}
