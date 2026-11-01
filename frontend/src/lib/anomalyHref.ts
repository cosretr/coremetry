// anomalyHref.ts — tam sayfa anomali adresleri (C1, v0.10.1770). Drawer
// yerine gerçek sayfa: /anomalies/:id (eski dedektörler) ve /anomalies/ml/:id
// (ML). Eski ?event=, ?replay=, /inbox?anomaly= linkleri yönlendirilir.
export function anomalyEventHref(id: string): string {
  return `/anomalies/${encodeURIComponent(id)}`;
}
export function mlEventHref(id: string): string {
  return `/anomalies/ml/${encodeURIComponent(id)}`;
}
/** SAF: eski /anomalies?event= | ?replay= → yeni sayfa; yoksa null. */
export function legacyAnomalyRedirect(sp: URLSearchParams): string | null {
  const ev = sp.get('event');
  if (ev) return anomalyEventHref(ev);
  const rp = sp.get('replay');
  if (rp) return mlEventHref(rp);
  return null;
}
