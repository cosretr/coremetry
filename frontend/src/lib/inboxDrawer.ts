// inboxDrawer — v0.8.292'de /inbox yerinde triyaj çekmecesinin saf
// dikişleriydi (eylem matrisi, RootCauseRibbon çıpası, ?item= çözümü,
// susturma gövdesi). v0.10.1081 (operatör: "Drawer çıkmasın, problem
// sayfasında direkt içeriğine girebileyim, Exceptions sayfası gibi.")
// çekmece KALDIRILDI; her satır tam sayfa detayına açılır ve eski ?item=
// linkleri oraya yönlendirilir (lib/inboxHref inboxItemIdTarget). Çekmeceye
// özgü yardımcılar onunla birlikte silindi; burada yalnız iki tam sayfanın
// (anomali detayı, servis dikkat şeridi) paylaştığı susturma gövdesi kaldı.
// Dosya adı içe aktaranlar değişmesin diye korunuyor.

// AnomalySilenceBody — the createAnomalySilence request shape (mirrors
// api.createAnomalySilence minus the reason). Kept local so the pure builder
// doesn't import the api module.
export interface AnomalySilenceBody {
  fingerprint: string;
  kind: string;
  pattern: string;
  service: string;
  durationSec: number;
}

// anomalyEventSilenceBody — v0.10.1032 (operatör: "Anomali ve alert rule'lara
// girdiğimde drawer çıkıyor. Exception gibi detay gözükmüyor."). Anomali satırı
// tam sayfa detay açıyor ve Mute… orada; sayfanın elinde bir InboxItem değil
// AnomalyEvent var. Gövde TEK yerde kurulur (lib/actions.ts silence-anomaly
// eylemiyle aynı şekil: fingerprint = olayın doğal kimliği). Boş kimlik →
// null (düğmeyi korur).
export function anomalyEventSilenceBody(
  e: { id: string; kind: string; pattern: string; service: string },
  durationSec: number,
): AnomalySilenceBody | null {
  if (!e.id) return null;
  return {
    fingerprint: e.id,
    kind: e.kind,
    pattern: e.pattern,
    service: e.service,
    durationSec,
  };
}
