// anomaliesRedirect — v0.10.2481: eski /anomalies (liste) → Problems ›
// Sinyaller. AYRI yaprak modül: App.tsx onu statik içe aktarır; lib/inboxTabs
// (urlSchema → zod) giriş paketine sürüklenmesin (bundle bütçesi).
import { legacyAnomalyRedirect } from '@/lib/anomalyHref';

/** SAF: eski /anomalies (liste) → yeni adres. ?event= / ?replay= tam sayfaya
 *  (bugünkü C1 kuralı); geri kalan her parametre korunur, eski ?tab= değeri
 *  (ml | groups | detectors — artık anlamı yok) düşer. */
export function anomaliesListRedirect(search: string): string {
  const sp = new URLSearchParams(search);
  const legacy = legacyAnomalyRedirect(sp);
  if (legacy) return legacy;
  const out = new URLSearchParams([['tab', 'signals']]);
  for (const [k, v] of sp) if (k !== 'tab') out.append(k, v);
  return `/inbox?${out.toString()}`;
}
