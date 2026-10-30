// runtimePodLabel (v0.9.91) — Runtime panelinin pod-bazlı çizgi etiketi.
//
// groupBy ÜÇLÜ (öncelik sırası okunabilirlik için — churn-tespitinden
// FARKLI, o benzersizliği yeğler, deploys.go instanceIdExpr):
//   [0] k8s.pod.name         — collector k8sattributes zenginleştirmesi
//                              (her ortamda yok; varsa altın standart)
//   [1] host.name            — container hostname = k8s pod adı
//                              (javaagent 2.x default; podservice.go:11,
//                              store.go:2392 — kod tabanının bildiği gerçek)
//   [2] service.instance.id  — javaagent'in ürettiği UUID (SON çare;
//                              operatör "4abcd5" olarak bunu görüyordu)
//
// Okunabilir pod adı (0 veya 1) varsa TAM hali basılır; yoksa instance id
// ilk 8; üçü de boşsa spec fallback'i (tek çizgi aggregate).
// v0.10.1640 (operatör raporu) — servis-adı öneki artık KIRPILMAZ: eski
// kırpma lejand/tooltip'te yalnız "6b49d8f679-69tf7" bırakıyordu, pod
// kimliği okunmuyordu. Dar lejand TimeSeriesPanel'de ellipsis + title.
export function podLineLabel(groupKey: string[], fallback: string): string {
  const podName = (groupKey[0] ?? '').trim() || (groupKey[1] ?? '').trim();
  if (podName) return podName;
  const inst = (groupKey[2] ?? '').trim();
  if (inst) return inst.slice(0, 8);
  return fallback;
}

// podClusterOf — v0.9.532. groupKey[3] = sunucunun çözdüğü cluster adı
// (POD_GROUP'un 4. anahtarı); yoksa ''.
export function podClusterOf(groupKey: string[]): string {
  return (groupKey[3] ?? '').trim();
}

// withClusterPrefix — cluster önekini YALNIZ ayırt ediciyken basar.
//
// Tek cluster'lı serviste her etikete "ocp-eu · " eklemek gürültü —
// operatörün sorusu "hangisi hangi cluster'da", cevabı ancak birden çok
// cluster varken anlamlı. Cluster'ı boş olan seri (attr basmayan ~%1-2)
// önek almaz; etiketi değişmediği için rengi de stabil kalır.
export function withClusterPrefix(label: string, cluster: string, multiCluster: boolean): string {
  return multiCluster && cluster ? `${cluster} · ${label}` : label;
}
