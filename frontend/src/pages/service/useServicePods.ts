import { useMemo } from 'react';
import { useQuery, useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useServicesMetadata } from '@/lib/queries';
import { timeRangeToNs } from '@/lib/utils';
import { clampThanosWindow } from '@/lib/thanosWindow';
import { podMatchesService, servicePodRegex } from '@/pages/clusters/podWorkload';
import type { ClusterPodRow, TimeRange } from '@/lib/types';

// dominantNamespace — eşleşen pod'ların en sık namespace'i (v0.9.56):
// metadata ns türetilememişse grafik/JMX sorgularının namespace parametresi
// buradan gelir (yedek modda pod'lar zaten ada göre eşleşti).
function dominantNamespace(rows: ClusterPodRow[]): string {
  const counts = new Map<string, number>();
  for (const r of rows) {
    if (r.namespace) counts.set(r.namespace, (counts.get(r.namespace) ?? 0) + 1);
  }
  let best = '', n = 0;
  for (const [ns, c] of counts) {
    if (c > n || (c === n && ns < best)) { best = ns; n = c; }
  }
  return best;
}

// useServicePods (v0.9.158) — servisin Thanos pod envanteri, hem Infrastructure
// hem yeni Pods sekmesince paylaşılan VERİ KATMANI. Önceden ServiceInfraTab'ın
// içindeydi; Pods sekmesi de aynı eşleşmeye (rows/effNs/effDeploy) ihtiyaç
// duyduğundan hook'a çıkarıldı (fetch'ler cache-paylaşımlı, tekrar istek yok).
//
// Cluster keşfi TÜM etkin Thanos kaynaklarını tarar (v0.9.138); span-türetimi
// cluster SEÇİMİNDE kullanılmaz — hangi cluster'da pod var precise
// pod-eşleşmesi (podMatchesService, v0.9.130) belirler. Grafik parametreleri
// yedek modda da dolu: deploy yoksa servis adı, ns yoksa baskın namespace.
export function useServicePods(service: string, range: TimeRange) {
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);

  const metaQ = useServicesMetadata();
  const ns = metaQ.data?.[service]?.namespace ?? '';
  const deploy = metaQ.data?.[service]?.deployment ?? '';

  const sourcesQ = useQuery({
    queryKey: ['cluster-sources'],
    queryFn: () => api.clusterSources(),
    staleTime: 300_000,
  });
  const matched = useMemo(() => sourcesQ.data?.clusters ?? [], [sourcesQ.data]);

  // v0.10.2232 (Entity S2) — servisin iş yükleri entity modelinden (kalıcı
  // serves kenarı). Pod adından iş yükü sezgiseli yok: iş yükünün pod'ları
  // KSM sahiplik zincirinden (/deployments podNames).
  const wlQ = useQuery({
    queryKey: ['service-workloads', service],
    queryFn: () => api.serviceWorkloads(service),
    staleTime: 60_000, retry: 1, enabled: service !== '',
  });
  const workloads = useMemo(() => wlQ.data?.workloads ?? [], [wlQ.data]);
  // (cluster adı, namespace) çiftleri: katalog ns'i varsa her cluster'da o;
  // yoksa entity iş yüklerinin bulunduğu cluster/namespace'ler.
  const depPairs = useMemo(() => {
    const out: { c: string; ns: string }[] = [];
    const seen = new Set<string>();
    const add = (c: string, n: string) => {
      const k = c + '\u0000' + n;
      if (n && !seen.has(k)) { seen.add(k); out.push({ c, ns: n }); }
    };
    for (const c of matched) {
      if (ns) add(c, ns);
      for (const w of workloads) if (w.cluster === c) add(c, w.namespace);
    }
    return out;
  }, [matched, ns, workloads]);
  const depQs = useQueries({
    queries: depPairs.map(({ c, ns: n }) => ({
      queryKey: ['cluster-deployments', c, n],
      queryFn: () => api.clusterDeployments(c, n),
      staleTime: 60_000, retry: 1,
    })),
  });
  // v0.9.536 — HEDEFLİ envanter (operator-reported: BFF pod'ları
  // cluster-geneli topk(500)'e giremiyordu → istemci eşleştirmesi hiç
  // gelmeyen pod'u eşleştiremez, "No pods matched"). Sunucu regex'le
  // daraltır; anahtar podRe'yi taşır — /clusters sayfasının düz
  // envanter cache'iyle ÇAKIŞMAZ (farklı anahtar, farklı sonuç).
  const wlNames = [...new Set(workloads.map(w => w.name))];
  const podRe = servicePodRegex(service, deploy, wlNames);
  const podQs = useQueries({
    queries: matched.map(c => ({
      queryKey: ['cluster-pods', c, podRe],
      queryFn: () => api.clusterPods(c, podRe),
      staleTime: 60_000,
      // v0.9.539 (operatör: "20+ cluster, kullanıcılar 20-30 sn
      // bekliyor") — RETRY YOK. Aritmetik: handler'ın cluster başına
      // 10s deadline'ı × retry:1 = tek yanıt vermeyen cluster 20
      // saniye. Yeniden deneme burada BEDAVA DEĞİL ve işe de yaramaz:
      // 10 saniyede yanıt vermemiş bir Thanos'un hemen ardından
      // yanıt verme olasılığı düşük, ve 60s'lik poll zaten doğal
      // yeniden denemedir. Hata o cluster için görünür kalır
      // (podErrors → "X cluster'ı yanıt vermedi"), sessizce yutulmaz.
      retry: false,
    })),
  });

  // Pod eşleşme (podMatchesService, testli): ns süzgeci + podSet ÜYELİĞİ
  // (katalog deploy'u ⋃ entity iş yüklerinin KSM pod'ları) ⋃ resolver'ın
  // pod→servis alanı ⋃ "<deploy>-" önek. Bilinçli memo'suz: useQueries
  // kimliği her render değişir, tarama ≤ birkaç bin satır.
  const rows: ClusterPodRow[] = [];
  matched.forEach((c, i) => {
    const owned = new Set<string>();
    depPairs.forEach((pair, j) => {
      if (pair.c !== c) return;
      for (const d of depQs[j]?.data?.deployments ?? []) {
        const mine = (deploy && pair.ns === ns && d.deployment === deploy) ||
          workloads.some(w => w.cluster === c && w.namespace === pair.ns && w.name === d.deployment);
        if (mine) for (const p of d.podNames ?? []) owned.add(p);
      }
    });
    for (const p of podQs[i]?.data?.pods ?? []) {
      if (podMatchesService(p, { service, deploy, ns, podNames: owned })) rows.push(p);
    }
  });
  const clustersWithPods = [...new Set(rows.map(r => r.cluster))];
  // PromQL iş yükü adı: katalog > entity iş yükü (serves) > servis adı.
  const effDeploy = deploy || workloads[0]?.name || service;
  const effNs = ns || workloads[0]?.namespace || dominantNamespace(rows);

  // Sunucu pencere tavanı — Clusters Overview'la aynı dürüstlük
  // (v0.9.21). Kural tek gövdede (lib/thanosWindow), sunucuyla
  // ayrışması Go testiyle kapalı (v0.9.1370).
  const { cFrom, cTo, clamped } = useMemo(
    () => clampThanosWindow(from, to), [from, to]);

  // Gate bit'leri (her iki sekme aynı boş/yükleniyor durumlarını gösterir).
  const sourcesPending = sourcesQ.isPending;
  const noClusters = (sourcesQ.data?.clusters ?? []).length === 0;
  // v0.9.538 (operatör: "infra sayfası tüm cluster'ları döndüğü için
  // yavaş yükleniyor") — KADEMELİ render. Eskiden podsPending =
  // some(isPending) idi: TEK bir yavaş cluster tüm sayfayı spinner'da
  // tutuyordu. 14 cluster'ın 13'ü 200ms'de dönse de 14.'sü handler'ın
  // 10s deadline'ına dayanırsa operatör 10 saniye boş ekran görüyordu.
  //
  // Cluster listesi DARALTILAMAZ: servis, Coremetry'ye trace
  // göndermediği (yalnız Thanos'ta metrik/JMX olan) cluster'larda da
  // koşabilir — spans'tan türetmek v0.9.138'in düzelttiği "bazı
  // cluster'lar görünmüyor" bug'ını geri getirirdi (v0.9.142 notu).
  // O yüzden hepsini sormaya devam ediyoruz, ama GELENİ hemen çiziyoruz.
  const podsSettled = podQs.filter(q => !q.isPending).length;
  const podsTotal = podQs.length;
  const podsPending = podsSettled < podsTotal;
  // podsBlocking — spinner SADECE hiçbir eşleşme yokken ve hâlâ
  // beklenen cluster varken. Erken "No pods matched" YASAK: ilk dönen
  // cluster'lar boş çıkıp pod'lar 12. cluster'da olabilir; o hâlde
  // "yok" demek yalan olurdu.
  const podsBlocking = rows.length === 0 && podsPending;
  // v0.9.363 — hatalar dışarı çıkıyor. Eskiden Thanos 502'si, 10s handler
  // deadline'ı ya da süresi dolmuş token, "No pods matched … nothing
  // matched" ile AYNI ekranı üretiyordu: operatöre ürün yanlış yapılandırılmış
  // ya da workload yok deniyordu — tam da bakması gereken anda.
  const sourcesError = sourcesQ.isError ? String(sourcesQ.error) : null;
  const podErrors = podQs
    .map((q, i) => (q.isError ? (matched[i] ?? `cluster ${i + 1}`) : null))
    .filter((x): x is string => !!x);
  // v0.9.369 — sunucu topk(500) tavanına dayanan cluster'lar: istemci
  // süzmesi o cluster'da "yok" sonucunu KANITLAYAMAZ (sakin pod'lar
  // topk dışında). Boş durum ve başlık bunu söylemek zorunda.
  const truncatedClusters = podQs
    .map((q, i) => (q.data?.truncated ? (matched[i] ?? `cluster ${i + 1}`) : null))
    .filter((x): x is string => !!x);

  // v0.10.718 — tazelik + elle yenile (Infra Clusters başlığındaki "N sn
  // önce ↻"): en yeni cluster cevabının zamanı; refetch tüm cluster'lara.
  const podsUpdatedAt = podQs.reduce((m, q) => Math.max(m, q.dataUpdatedAt || 0), 0);
  const podsFetching = podQs.some(q => q.isFetching);
  const refetchPods = () => { for (const q of podQs) void q.refetch(); };

  return {
    podsUpdatedAt, podsFetching, refetchPods,
    metaQ, ns, deploy, matched, rows, clustersWithPods, workloads,
    effNs, effDeploy, from, to, cFrom, cTo, clamped,
    sourcesPending, noClusters, podsPending, podsBlocking, podsSettled, podsTotal,
    sourcesError, podErrors,
    truncatedClusters,
  };
}
