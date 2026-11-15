// podWorkload.ts — Servis → Infrastructure pod seçimi (v0.9.56+).
//
// v0.10.2232 (Entity S2): pod ADINDAN iş yükü sezgiseli (podWorkloadName,
// workloadMatchesService, dominantWorkload — Go stripPodSuffixes'in FE
// aynası) SİLİNDİ. Pod → iş yükü sahipliği entity modelinden gelir: servisin
// iş yükleri /api/services/{name}/workloads (kalıcı serves kenarı), iş
// yükünün pod'ları KSM sahiplik zinciri (/api/clusters/…/deployments
// podNames). Tek kanonikleştirme backend'de (internal/entity/wlkind).

// stripEnvSuffix — v0.9.535 (operatör direktifi: "mobile*bff-prod
// sonunda prod olmadan bul"). Servis adının SONUNDAKİ bilinen env eki
// soyulur: filoda k8s deployment adı env ekini taşımayabiliyor (servis
// mobile-loans-bff-prod, deployment mobile-loans-bff). Yalnız BİLİNEN
// ekler (whitelist) ve yalnız kuyrukta — "-production" gibi serbest
// varyantlar ya da ad ortasındaki "prod" DOKUNULMAZ.
//
// Çapraz-env notu (operatörle konuşuldu, kabul edilen risk):
// mobile-loans-bff-prod ve mobile-loans-bff-int soyulunca aynı ada
// iner; iki env'in pod'ları aynı Thanos setinde ve katalog ns boşsa
// karışabilir. Çare katalogda namespace girmek (ns süzgeci kesin
// ayırır). Prod tek-env olduğu için beklenen durumda sorun yok.
const ENV_SUFFIXES = ['-prod', '-int', '-uat', '-prep'];

export function stripEnvSuffix(service: string): string {
  for (const suf of ENV_SUFFIXES) {
    if (service.length > suf.length && service.endsWith(suf)) {
      return service.slice(0, -suf.length);
    }
  }
  return service;
}

// servicePodRegex — v0.9.536. Servis sekmelerinin HEDEFLİ envanter
// seçicisi: sunucu bu regex'i pod=~ olarak PromQL'e gömer, topk(500)
// servisin kendi pod'ları içinde işler (cluster-geneli kesim kalkar —
// operator-reported: 0.001 core'luk BFF pod'ları top-500'e giremiyordu).
//
// Adaylar: katalog deployment'ı (varsa) + servis adı + env eki soyulmuş
// hâli. Önek kalıbı BİLİNÇLİ gevşek — "(mobile-home)-.*" hem
// mobile-home-bff'i hem olası kardeşleri getirir; kesin ayrımı
// istemcideki podMatchesService eşitlik disiplini yapar (sunucu
// DARALTIR, istemci AYIKLAR).
export function servicePodRegex(service: string, deploy: string, workloads: string[] = []): string {
  const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const cands = [...new Set([deploy, ...workloads, service, stripEnvSuffix(service)].filter(Boolean))];
  return `(${cands.map(esc).join('|')})-.*`;
}

// PodMatchInput — podMatchesService'in ihtiyaç duyduğu ClusterPodRow
// alt kümesi (test edilebilirlik için dar tip; ClusterPodRow bunu karşılar).
export interface PodMatchInput {
  pod: string;
  namespace: string;
  service?: string;
}

// podMatchesService — Servis → Infrastructure sekmesinin pod-eşleşme
// kararı (SAF). v0.9.130: deploy varken podSet ADDİTİF (üyelik VEYA
// "<deploy>-" öneki — kısmi KSM'li cluster'da da bulur). v0.10.2232 (S2):
// podNames artık katalog deploy'unun YANINDA servisin entity iş
// yüklerinin (serves) KSM pod'larını da taşır; ad eşitliği yedeği
// (soyulmuş pod adı == servis) silindi — sahiplik modelden gelir.
export function podMatchesService(
  p: PodMatchInput,
  opts: { service: string; deploy: string; ns: string; podNames: Set<string> | null },
): boolean {
  const { service, deploy, ns, podNames } = opts;
  // Namespace süzgeci — metadata ns türetildiyse aynı adlı başka
  // namespace'in pod'unu dışlar; ns boşsa uygulanmaz.
  if (ns && p.namespace !== ns) return false;
  if (podNames?.has(p.pod)) return true;
  // Pod satırının servisi resolver'dan (S0: pod runs kenarı).
  if (p.service === service) return true;
  return !!deploy && p.pod.startsWith(deploy + '-');
}
