// incidentsRetired — v0.10.1828 (Incidents emekliliği, audit §16.1 E3/E5/E6;
// operatör kararı 2026-11-05: "Incident sekmesi yerine Anomalies olsun; eski
// incident'lar devre dışı kalsın"). Ana anahtar kabuk bayrağından gelir
// (user.features.incidents, AuthProvider useIncidentsEnabled). Kapalıyken:
//   - menü, ⌘K ve "g i" Incidents'ı göstermez; menüde yerine Anomalies girer;
//   - /incidents → /anomalies?tab=groups (eski yer imleri ölmez);
//   - /incident?id= de Anomalies'e iner (arşiv sayfası Incidents ölü kod dilimi 2'de silindi).
// Açıkken her şey bugünkü gibi. Yüzey başına ikinci anahtar YOK.

/** /incidents'ın emekliyken yönlendiği yer. v0.10.2481: Anomalies listesi
 *  Problems sayfasının Sinyaller sekmesi oldu (lib/inboxTabs SIGNALS_HREF). */
export const INCIDENTS_RETIRED_HOME = '/inbox?tab=signals';

/** SAF: bir gezinme hedefi (menü / ⌘K / kısayol) bu anahtar durumunda görünür mü. */
export function incidentsNavVisible(to: string, incidentsOn: boolean): boolean {
  return incidentsOn || to.split('?')[0] !== '/incidents';
}
