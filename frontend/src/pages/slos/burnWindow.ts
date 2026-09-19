/**
 * burnWinLabel — SLO modalındaki "fast/slow burn" etiketine pencere ekler
 * (v0.10.794). Sunucu saniye gönderir (chstore.BurnExplainPolicy); yoksa
 * (eski sunucu, rolling deploy) boş döner ve etiket eskisi gibi kalır.
 */
export function burnWinLabel(seconds?: number): string {
  if (!seconds || seconds <= 0) return '';
  if (seconds % 3600 === 0) return ` (${seconds / 3600}h)`;
  return ` (${Math.round(seconds / 60)}m)`;
}
