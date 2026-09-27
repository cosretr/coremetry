# Rollouts v2 — collector seçenekleri: `service.version`'ı imajdan doldurmak

**Sürüm:** v0.10.964 (Rollouts v2 P5.3). **Kaynak:** `docs/rollouts/v2-audit.md` §9 (özellikle §9.4).
**Tür:** yalnız dokümantasyon. Coremetry bu belgedeki hiçbir yapılandırmayı yazmaz, uygulamaz,
collector'a dokunmaz. Bütün adlar örnektir (`cluster-a`, `checkout-api`, `<registry>`); gerçek
değerleri kendi ortamından koy.

## 0. Kısaca

- **Rollouts v2 için collector değişikliği GEREKMEZ.** v2'de sürüm KSM'den gelir
  (`kube_pod_container_info` imajı; audit §9.4, planlanan P2.2/P5.1). Span'ler yalnız bir sürüm
  etiketi ve rollout öncesi/sonrası etki penceresi ekler.
- Coremetry'nin deploy/sürüm/rollout zinciri (deploy tespiti, `service_version_5m`, dönem
  karşılaştırma, problem telemetrisi) zaten **imaj etiketini `service.version`'ın ÖNÜNDE** okur
  (v0.9.66). `container.image.tag` span'e geliyorsa **bu zincir için** `service.version`'ı yeniden
  yazmak gerekmez.
- Ama `service.version`'ı **tek başına** okuyan üç Coremetry yüzeyi var: endpoint'lerde sürüme göre
  bölme, log sürüm rolü ve metrik sürüm rolü (§1). İmaj etiketine düşmezler; `service.version`'ın
  kendisi yeniden yazılana (B, C ya da D) kadar filo genelindeki sabit değeri (audit §9.2)
  göstermeye devam ederler. B/C/D ayrıca Coremetry dışı alıcılar için de geçerli.
- Bir collector restart'ı bilinen takılma riskini taşır (`CLAUDE.md` tuzakları: Coremetry
  Deployment'ı `maxUnavailable: 0` tutmazsa collector rollout'ta "zero addresses" ile takılır;
  düzeltme öncesi çare collector restart'ı). Bu belgedeki her değişiklik **bakım penceresi** işidir.
- Önce/sonra ölçümü: **Admin › K8s Coverage** kartı (§3). v0.10.964 ile kartta `tag`, `ver`, `env`
  kolonları var.

## 1. Coremetry neyi, nereden okuyor

Sürüm anahtarları (`container.image.tag`, `k8s.container.image.tag`, `service.version`, Helm/k8s
label'ları), `k8s.replicaset.name` ve `deployment.environment(.name)` yalnız **resource**
attribute'undan okunur (saklandıkları yer aşağıdaki tabloda). Tek istisna cluster:
`clusterDeriveExpr` (`internal/chstore/repo.go`) anahtar resource'ta yoksa span attribute'una düşer.
Span attribute'u olarak gelen `container.image.tag` ya da `service.version` sürüm zincirine
**düşmez**. Collector'da ekleme yapılacaksa resource kapsamında yapılmalı (§2 B'deki
`context: resource`).

| Anahtar | Nerede saklanıyor | Kim okuyor |
|---|---|---|
| `container.image.tag`, `k8s.container.image.tag` | `res_keys`/`res_values`; ayrıca terfi kolonu `container_image_tag` (MATERIALIZED, iki yazımın coalesce'i; `internal/chstore/promoted_attr.go`). Kolon prod'da yalnız `migrations/0012` uygulandıysa var (dış Distributed `spans`'ta boot ALTER'ı atlar). | Sürüm zinciri `effectiveVersionExpr` (`internal/chstore/deploys.go`) — zincirin **başı**; `service_version_5m` MV (`internal/chstore/store.go`); v1 rollout revizyonu (terfi kolonu); frontend `frontend/src/lib/runningVersion.ts` |
| `service.version` | yalnız `res_keys`/`res_values` (**kolonu yok**) | Aynı sürüm zinciri, imaj etiketinden **sonra** (deploy tespiti, `service_version_5m`, dönem karşılaştırma, problem telemetrisi; bunlarda imaj etiketi gelirse onu örter). Yer tutucu değerler (`latest`, `dev`, `unknown`, `0.0.1-SNAPSHOT`, `${project.version}` …; tam liste `placeholderVersionList`, `deploys.go`) elenir ve zincir bir sonrakine düşer. **Tek başına okuyanlar** (imaj etiketine düşmez): endpoint'lerde sürüme göre bölme (`endpointSplitDims`, `internal/chstore/endpoints_detail.go`); log sürüm rolü (CH `logs` `res_keys` için `chVersionKeys`, ES için `esVersionFields`; `internal/logstore/field_mapping.go`); metrik sürüm rolü (`RoleCandidates(RoleVersion)` → `service_version` etiketi; `internal/vmetrics/labelmap.go`). |
| `k8s.deployment.labels.app_kubernetes_io_version`, `k8s.pod.labels.app_kubernetes_io_version`, `k8s.deployment.labels.version`, `helm.chart.version` | `res_keys`/`res_values` | Sürüm zincirinin kuyruğu (aynı sıra) |
| `deployment.environment.name` (yedek `deployment.environment`) | `spans.deploy_env` kolonu; ingest yeni anahtarı, yoksa eskisini yazar (`internal/otlp/convert.go`). Ham hâli ayrıca `res_keys`/`res_values`'ta. | Env filtreleri (tam eşleşme). K8s Coverage `env` yalnız yeni anahtarı sayar (`res_keys`, §3). |
| `k8s.cluster.name` → `openshift.cluster.name` → `cluster` (önce resource, sonra span attribute) | `spans.cluster` MATERIALIZED coalesce (`clusterDeriveExpr`, `internal/chstore/repo.go`) | Her cluster filtresi; Remote Cluster eşlemesi (tam eşleşme, harf duyarlı) |
| `k8s.replicaset.name` | `res_keys`; terfi kolonu `k8s_replicaset` | v1 rollout revizyonu; K8s Coverage `rs` |

**Sürüm zinciri sırası** (ilk yer-tutucu-olmayan kazanır):
`container.image.tag` → `k8s.container.image.tag` → `service.version` → Helm/k8s label'ları.
Çoğul `container.image.tags` (yeni semconv) hiçbir okuyucuya düşmez; dizi değerleri `res_values`'ta
JSON metni olarak saklanır ve zincir tekil anahtarı okur.

**Repo'nun gördüğü collector** (`charts/coremetry/templates/otel-collector.yaml`,
`otel/opentelemetry-collector-contrib:0.111.0`): `k8sattributes` **opt-in**
(`otelCollector.k8sattributes.enabled`, varsayılan kapalı) ve altı anahtar çıkarır
(`k8s.namespace.name`, `k8s.deployment.name`, `k8s.pod.name`, `k8s.pod.uid`, `k8s.node.name`,
`k8s.container.name`). `resource` / `resourcedetection` / `transform` processor'ı yok. Yani chart'ın
collector'ı `container.image.*`, `k8s.replicaset.name`, `k8s.cluster.name` ya da `service.version`
**üretmez**. Prod collector'ını repo göremez (audit §9.2).

## 2. Seçenekler (audit §9.4)

### A. `k8sattributes` imaj etiketini ve ReplicaSet'i çıkarsın — önerilen ilk adım

Coremetry'nin deploy/sürüm/rollout zinciri imaj etiketini öne aldığı için **o zincir için** yeniden
yazma gerekmez. `service.version`'ı tek başına okuyan üç yüzey (§1: endpoint sürüm bölmesi, log ve
metrik sürüm rolü) A'dan etkilenmez; onlar için B, C ya da D.

```yaml
processors:
  k8sattributes:
    auth_type: serviceAccount
    passthrough: false
    extract:
      metadata:
        - k8s.namespace.name
        - k8s.deployment.name
        - k8s.replicaset.name      # yeni
        - k8s.pod.name
        - k8s.pod.uid
        - k8s.node.name
        - k8s.container.name
        - container.image.name     # yeni
        - container.image.tag      # yeni
    pod_association:
      - sources:
          - from: connection
```

Uyarılar:
- Çok container'lı pod'da imajın hangi container'a ait olduğu için resource'ta `container.id` ya da
  `k8s.container.name` gerekir (dış kaynak: https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/k8sattributesprocessor/README.md).
- `from: connection` eşlemesi uygulamanın bu collector'a **doğrudan** gönderdiğini varsayar; araya
  bir agent collector girerse bağlantı IP'si agent'ınkidir.
- `k8s.deployment.name` / `k8s.replicaset.name` için ClusterRole'de `replicasets` okuma izni gerekir
  (chart'ta `otelcol-rbac.yaml`, yalnız `k8sattributes.enabled` açıkken).
- Digest ile sabitlenmiş (`<registry>/checkout-api@sha256:…`) ya da `:latest` imajlarda etiket
  sürüm taşımaz; `latest` zaten yer tutucu listesinde ve elenir.

### B. `transform` ile `service.version` ← imaj etiketi (Coremetry dışı alıcılar ve `service.version`'ı tek başına okuyan Coremetry yüzeyleri için)

`k8sattributes`'tan **sonra** koşmalı. Resource kapsamında yazılır; `where` koşulu uygulamanın
kendi (manifest) sürümünü ezmez.

```yaml
processors:
  transform/version-from-image:
    error_mode: ignore
    trace_statements:
      - context: resource
        statements:
          - set(attributes["service.version"], attributes["container.image.tag"]) where attributes["service.version"] == nil and attributes["container.image.tag"] != nil

service:
  pipelines:
    traces:
      processors: [memory_limiter, k8sattributes, transform/version-from-image, batch]
```

Uyarılar:
- `where` koşulu olmadan yazmak JAR manifest'inden (`Implementation-Version`) gelen sürümü **siler**
  (dış kaynak: https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/pkg/ottl/ottlfuncs/README.md).
- Filoda `service.version` sabit geliyorsa (audit §9.2: Java agent'ın manifest sürümü olası sebep)
  `where … == nil` hiçbir şey yapmaz; o durumda ezmek bilinçli bir karardır.
- Örnek yalnız **traces** hattını değiştirir (`trace_statements`). Log ve metrik sürüm rollerinin
  (§1) de düzelmesi isteniyorsa aynı ifade `log_statements` / `metric_statements` altında da
  yazılmalı ve processor `logs` / `metrics` hatlarına da eklenmeli. Bu yalnız o sinyaller **bu
  collector'dan geçiyorsa** işe yarar; Thanos etiketleri için ayrıca resource attribute'larının
  Prometheus etiketine nasıl dönüştüğüne bağlıdır. İkisi de doğrulanmadı (§4 madde 8).
- Aynı araç `replace_pattern` ile `k8s.cluster.name`'i env'den türetebilir. **Önerilmez:**
  audit §9.3 riskleri (uygulama etiketi altyapı kimliği olur, env biçimi doğrulanmadı, geçmiş iki
  değere bölünür). Cluster kimliği için E.

### C. `k8sattributes` `extract.metadata: [service.version]`

Öncelik: annotation → `app.kubernetes.io/version` label'ı → imaj etiketi (dış kaynak:
https://opentelemetry.io/docs/specs/semconv/non-normative/k8s-attributes/).

Uyarılar:
- Chart'ın 0.111.0'ından **yeni** (https://github.com/open-telemetry/opentelemetry-collector-contrib/pull/39335);
  prod collector sürümü bilinmiyor.
- Aynı yeni sürümlerde semconv çoğul `container.image.tags`'e geçiyor; Coremetry onu JSON metni
  olarak saklar ve sürüm zinciri **okumaz** (§1).

### D. Manifest label'ı + downward API (uygulama tarafı)

Collector'a dokunmaz; her uygulama chart'ında değişiklik ister.

```yaml
# checkout-api Deployment pod şablonu (örnek)
metadata:
  labels:
    app.kubernetes.io/version: "release.20260927.1"
spec:
  containers:
    - name: checkout-api
      env:
        - name: APP_VERSION
          valueFrom:
            fieldRef:
              fieldPath: metadata.labels['app.kubernetes.io/version']
        - name: OTEL_RESOURCE_ATTRIBUTES
          value: "service.version=$(APP_VERSION)"
```

`OTEL_RESOURCE_ATTRIBUTES` zaten kullanılıyorsa değer mevcut listeye virgülle eklenir.

### E. `resourcedetection` `openshift` dedektörü → `k8s.cluster.name` (altyapıdan, env'den değil)

Audit §9.3.1 riskini kaldırır: cluster kimliği uygulama etiketinden değil platformdan gelir.

```yaml
processors:
  resourcedetection/openshift:
    detectors: [openshift]
    override: false
```

Uyarılar:
- `config.openshift.io` `infrastructures` okuma izni gerekir (dış kaynak:
  https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/resourcedetectionprocessor/internal/openshift/documentation.md).
- Dedektör collector'ın **çalıştığı** cluster'ı okur (pod'un service account'u ve in-cluster API
  adresi), span'in geldiği cluster'ı değil; değeri collector açılışında bir kez belirler ve bütün
  telemetriye aynı değeri yazar. Yalnız her cluster'da kendi collector'ı varsa ve uygulamalar o
  yerel collector'a gönderiyorsa doğrudur. Birden çok cluster'dan alan merkezi (gateway) bir
  collector, `k8s.cluster.name` taşımayan bütün span'lere **kendi** cluster adını yazar
  (`override: false` yalnız zaten taşıyanları korur); bu, §9.3.1 riskinin yeni bir biçimidir.
  Agent → gateway zincirinde dedektörü cluster içindeki agent katmanında koştur. Topoloji
  doğrulanmadı (§4 madde 4).
- `k8s.cluster.name`, `openshift.cluster.name`'in önündedir (`clusterDeriveExpr`); kaynak değişince
  span cluster değeri değişir ve geçmiş iki değere bölünür (§9.3.4). Yeni değer Remote Cluster
  kaydının span cluster değerlerine eklenmeli (çok değerli eşleme mevcut çare).

### F. Coremetry ingest "enrich" hattı

Yalnız statik, cluster başına kurallar; bir attribute'u başka birine **kopyalayamaz** ve Thanos ya da
Elasticsearch'e ulaşmaz (audit §9.4). `service.version`'ı imajdan doldurmak için seçenek **değil**.

## 3. Doğrulama: önce ve sonra aynı tablo

**Admin › K8s Coverage** (`/system/k8s`, yalnız admin) servis başına şunları sayar:

| Kolon | Anahtar | Nasıl sayılıyor |
|---|---|---|
| `img` | `container.image.name` | anahtar var mı (`res_keys`) |
| `tag` | `container.image.tag` \| `k8s.container.image.tag` | anahtar var mı (`res_keys`; iki yazımdan biri). Terfi kolonu `container_image_tag` okunmaz; kart 0012'ye bağlı değil. |
| `ver` | `service.version` | anahtar var mı |
| `env` | `deployment.environment.name` | anahtar var mı (`res_keys`); eski `deployment.environment` SAYILMAZ — ingest `deploy_env`'i ondan da doldurur ama env'den türetilen cluster (audit §9.3.2) onu görmez |
| `rs`, `k8s`, `ocp` | `k8s.replicaset.name`, `k8s.cluster.name`, `openshift.cluster.name` | anahtar var mı |

Kartı okurken:
- Sayılar bir **örneklem**: servis başına kota, pencere 12 zaman dilimine bölünür, dış tavan
  200.000 satır. "—" **ölçülmedi** demektir, "yok" değil (eski sunucudan gelen yükte yeni kolonlar
  da "—" görünür). "Örneklem tavanı doldu" uyarısı varsa bazı servisler tabloya hiç girmemiştir.
- Kart **servis** başına sayar, cluster başına değil. Cluster kırılımı için audit §11.8 T1/T2
  sorguları.
- Sürüm doğruluğunu değil, anahtarın **gelip gelmediğini** söyler. Filonun tamamında aynı
  `service.version` görülmesi (§9.2) bu kartta "✓" olarak görünür.
- `env` ✗ iken env filtreleri o servis için çalışıyorsa servis büyük olasılıkla eski
  `deployment.environment` yazımını yayıyordur: ingest onu da `deploy_env`'e yazar, ama env'den
  türetilen cluster boş kalır (audit §9.3.2). Kart bu ayrımı görünür kılmak için yalnız yeni anahtarı
  sayar (§11.8 T1 ile aynı).

Beklenen etki (A uygulandıktan sonra): `tag`, `img`, `rs` kolonları ✓'ye döner; `ver` değişmez
(B/C/D uygulanmadıkça). E sonrasında `k8s` kolonu ✓'ye döner (kart anahtarın varlığını sayar,
değerin doğruluğunu değil; merkezi collector'da yanlış değer de ✓ görünür).

## 4. §11 canlı sorgu paketinin doğrulaması gereken varsayımlar

Bu belge ve v0.10.964 hiçbir canlı gerçeğe dayanmıyor; aşağıdakiler operatörün §11 cevaplarıyla
doğrulanacak:

1. Prod span'lerinde hangi anahtarların **cluster başına** geldiği (§11.8 T1); ekran görüntüleri
   cluster'lar arasında çelişiyor (audit §9.2).
2. `deploy_env` değerinin biçimi (`prod-<cluster>` olduğu gibi mi, önek atılmış mı) ve `cluster`
   ile ilişkisi (§11.8 T2).
3. Prod `spans`'ta terfi kolonlarının (0011/0012: `cluster`, `container_image_tag`,
   `k8s_replicaset`) var olup olmadığı. Kart bu kolonlara bağlı değil (üç yeni sayaç da `res_keys`
   üzerinde `has()`); kolonları okuyan öteki yüzeyler (ör. v1 rollout revizyonu, cluster filtreleri)
   için önemli.
4. Prod collector'ının sürümü, processor zinciri ve **topolojisi**: her cluster'da ayrı collector
   mı, yoksa birden çok cluster'dan alan merkezi/gateway collector mı (repo göremez). C'nin
   kullanılabilirliği sürüme, A'nın `from: connection` eşlemesi ve E'nin doğruluğu topolojiye bağlı.
5. `service.version`'ın filoda sabit olmasının sebebi (Java agent manifest sürümü hipotezi, §9.2).
6. Çok container'lı pod oranı (A'nın doğruluğu buna bağlı).
7. OpenShift'te `infrastructures` RBAC'ının verilip verilemeyeceği (E).
8. Prod log'ları (CH `logs` / Elasticsearch) ve metrikleri (Thanos) trace'lerle **aynı collector'dan**
   mı geçiyor, ve resource attribute'ları Prometheus etiketine nasıl dönüşüyor. B ve C'nin log ve
   metrik sürüm rollerine (§1) etkisi buna bağlı.
