import { createContext, useContext } from 'react';

// v0.10.1826 — Incidents emekliliği (operatör kararı 2026-11-05: "Incident
// sekmesi yerine Anomalies olsun; eski incident'lar devre dışı kalsın").
// Settings › Anomaly sekmesinde ana anahtarın SON KAYDEDİLEN değeri; aile
// başına "incident'a bağla" kutuları kapalıyken devre dışı + gerekçe.
// null = henüz okunmadı (kutular bu arada bugünkü gibi çizilir).
export const IncidentsSwitchCtx = createContext<boolean | null>(null);

export function useIncidentsSwitch(): boolean | null {
  return useContext(IncidentsSwitchCtx);
}

/** Kapalı ana anahtarda aile kutusunun gerekçesi (açık / bilinmiyor → null). */
export function incidentAttachLockedReason(on: boolean | null): string | null {
  return on === false
    ? 'Eski Incidents akışı kapalı (yukarıdaki ana anahtar): bu kutu şu an etkisiz, kayıtlı değeri korunur.'
    : null;
}
