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
// Follow-up 2026-11-21: .NET / CPython paneli (DotnetRuntimePanel) [0]=host.name,
// [1]=k8s.pod.name sırasıyla sorar (prod VM host_name = pod adı); ikisi de
// okunabilir pod adı, etiket kuralı aynı.
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

// POD_SCOPE_KEYS — v0.10.2464 (operatör: pod çizgisinde cluster / ortam
// görünsün). POD_GROUP'un [3] "cluster" anahtarı CH yolunda metricClusterExpr
// coalesce'ine çözülür, ama VM yolunda (prod) düz `cluster` etiketidir ve prod
// serileri `openshift_cluster_name` / `k8s_cluster_name` taşır → [3] boş
// kalıyordu. Bu üç anahtar pod gruplamasına EKLENİR (indeks 4..6; pod başına
// tek değer → seri sayısı değişmez): [4] ortam (CH: metricEnvExpr; VM:
// deployment_environment_name), [5] openshift.cluster.name, [6] k8s.cluster.name
// (resource. öneki CH'de res_keys, VM'de düz etiket). Etiketten okunur — ad
// eşlemesi yok. Nesil anahtarı bunlardan SONRA gelir (GEN_KEY_IDX).
export const POD_SCOPE_KEYS = 'deployment.environment.name,resource.openshift.cluster.name,resource.k8s.cluster.name';
/** Nesil kırılımlı sorguda nesil etiketinin groupKey indeksi (POD_GROUP 4 + kapsam 3). */
export const GEN_KEY_IDX = 7;

const at = (gk: readonly string[], i: number) => (gk[i] ?? '').trim();

// podClusterOf — v0.9.532. groupKey[3] = sunucunun çözdüğü cluster adı;
// v0.10.2464: boşsa serinin kendi cluster etiketi ([5] openshift, [6] k8s).
export function podClusterOf(groupKey: readonly string[]): string {
  return at(groupKey, 3) || at(groupKey, 5) || at(groupKey, 6);
}

/** v0.10.2464 — serinin ortam etiketi ([4]); yoksa ''. */
export function podEnvOf(groupKey: readonly string[]): string {
  return at(groupKey, 4);
}

// podScopeLabel — v0.10.2464: pod etiketi + taşıdığı cluster ve/veya ortam,
// "apigateway-…-f4rrz · ocpkb · prod". İkisi de yoksa yalnız pod; ortam
// cluster'la aynıysa bir kez. Pod adı KIRPILMAZ (v0.10.1640 kuralı: kırpılmış
// hash pod'u tanıtmaz) — dar lejand ellipsis + title ile tam değeri gösterir.
export function podScopeLabel(label: string, groupKey: readonly string[]): string {
  const cluster = podClusterOf(groupKey);
  const env = podEnvOf(groupKey);
  return [label, cluster, env && env !== cluster ? env : ''].filter(Boolean).join(' · ');
}

// isAnonPodSeries — follow-up 2026-11-21: pod kırılımı istenen sorgu pod
// kimliği taşımayan tek grup döndü mü (host_name / k8s_pod_name /
// service_instance_id üçü de boş). Prod VM serileri host_name taşır; bu yalnız
// yedek — o zaman çizgi servis toplamıdır ve öyle etiketlenir.
export function isAnonPodSeries(data: { groupKey: string[] }[]): boolean {
  return data.length > 0 && data.every(s => !(s.groupKey[0] ?? '').trim() && !(s.groupKey[1] ?? '').trim() && !(s.groupKey[2] ?? '').trim());
}

// divideByCount — SAF: pay (oran) sinyali anonim seride pod toplamıdır;
// aynı zaman damgasındaki seri sayısına bölünür (Go runtimesig ReadPods'un
// gc_time yedeğiyle aynı kural). Sayı yoksa / ≤1 ise değer aynen.
export function divideByCount<P extends { time: number; value: number | null }>(points: P[], counts: { time: number; value: number | null }[]): P[] {
  const by = new Map(counts.map(c => [c.time, c.value ?? 0]));
  return points.map(p => {
    const n = by.get(p.time) ?? 0;
    return p.value != null && n > 1 ? { ...p, value: p.value / n } : p;
  });
}
