// anomalyHref.ts — tam sayfa anomali adresleri (C1, v0.10.1770). Drawer
// yerine gerçek sayfa: /anomalies/:id (eski dedektörler) ve /anomalies/ml/:id
// (ML). Eski ?event=, ?replay=, /inbox?anomaly= linkleri yönlendirilir.
export function anomalyEventHref(id: string): string {
  return `/anomalies/${encodeURIComponent(id)}`;
}
export function mlEventHref(id: string): string {
  return `/anomalies/ml/${encodeURIComponent(id)}`;
}
/** v0.10.2080 — ML olayı eski olay satırı şeklinde de gelir (kind "ml:<dedektör>",
 *  sunucu chstore.ListAnomalyFeed). Kimlik ML olay kimliği: ayrıntı ML sayfası,
 *  karar ML karar ucu, susturma yok. */
export function isMLAnomaly(e: { kind: string }): boolean {
  return e.kind.startsWith('ml:') && e.kind.length > 3;
}
/** SAF: olay satırının tam sayfası (ML satırı ML sayfasına). */
export function anomalyRowHref(e: { id: string; kind: string }): string {
  return isMLAnomaly(e) ? mlEventHref(e.id) : anomalyEventHref(e.id);
}
/** v0.10.2053 (D4) — ML grup Problem'inin kural öneki (chstore.MLGroupRulePrefix);
 *  kuyruk grubun ilk olayının kimliği → grup sayfası. */
export const ML_GROUP_RULE_PREFIX = 'anomaly-auto:group:';
/** SAF: grup Problem'inin kuralından grup sayfası; başka kural → null. */
export function mlGroupProblemHref(ruleId: string | null | undefined): string | null {
  const id = (ruleId ?? '').startsWith(ML_GROUP_RULE_PREFIX) ? (ruleId ?? '').slice(ML_GROUP_RULE_PREFIX.length) : '';
  return id ? mlEventHref(id) : null;
}
/** v0.10.1846 — listedeki ▶: detayı açar ve oynatmayı başlatır (satır tıkı oynatmaz). */
export function mlEventPlayHref(id: string): string {
  return `${mlEventHref(id)}?play=1`;
}
/** SAF: eski /anomalies?event= | ?replay= → yeni sayfa; yoksa null. */
export function legacyAnomalyRedirect(sp: URLSearchParams): string | null {
  const ev = sp.get('event');
  if (ev) return anomalyEventHref(ev);
  const rp = sp.get('replay');
  if (rp) return mlEventHref(rp);
  return null;
}
