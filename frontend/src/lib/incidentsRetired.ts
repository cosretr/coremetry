// incidentsRetired — v0.10.1828 (Incidents emekliliği, audit §16.1 E3/E5/E6;
// operatör kararı 2026-11-05: "Incident sekmesi yerine Anomalies olsun; eski
// incident'lar devre dışı kalsın"). Ana anahtar kabuk bayrağından gelir
// (user.features.incidents, AuthProvider useIncidentsEnabled). Kapalıyken:
//   - menü, ⌘K ve "g i" Incidents'ı göstermez; menüde yerine Anomalies girer;
//   - /incidents → /anomalies?tab=groups (eski yer imleri ölmez);
//   - /incident?id= salt-okunur ARŞİV olarak açılır (eski bildirim linkleri).
// Açıkken her şey bugünkü gibi. Yüzey başına ikinci anahtar YOK.

/** /incidents'ın emekliyken yönlendiği yer (Anomalies › Gruplar). */
export const INCIDENTS_RETIRED_HOME = '/anomalies?tab=groups';

/** SAF: bir gezinme hedefi (menü / ⌘K / kısayol) bu anahtar durumunda görünür mü. */
export function incidentsNavVisible(to: string, incidentsOn: boolean): boolean {
  return incidentsOn || to.split('?')[0] !== '/incidents';
}

/** Arşiv şeridi metni (incident detay sayfası, emekliyken). */
export const INCIDENTS_ARCHIVE_BANNER = "Incidents akışı emekli (arşiv). Yeni olaylar Anomalies'te.";
