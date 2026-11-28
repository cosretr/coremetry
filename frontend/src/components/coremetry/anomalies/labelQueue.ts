// labelQueue.ts — v0.10.2484 (precision programı B.3/2): haftalık etiket
// kuyruğunun SAF parçaları. Bileşenden ayrı: olay sayfası (MLReplaySheet)
// yalnız parametre adını okur, kuyruk bileşeni onun parçasına girmez.

/** Kuyruktan açılan olay sayfasının işareti: ?lq=1 → oy origin='queue'. */
export const LABEL_QUEUE_PARAM = 'lq';

/** SAF: kuyruk satırının olay sayfası bağlantısı (oy kuyruk etiketi sayılır). */
export function labelQueueHref(id: string): string {
  return `/anomalies/ml/${encodeURIComponent(id)}?${LABEL_QUEUE_PARAM}=1`;
}
