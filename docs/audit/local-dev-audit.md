# Yerel geliştirme ortamı — Faz 0 audit

2026-11-28 · yalnız belge · dal `claude/capability-setup` (`v0.10.2488-11-g2767b3f4e`). İşaretler: **ölçüm** = bu oturumda ölçüldü · **tahmin** · **[V]** = kanıtsız
varsayım · **op. bilgisi** = operatörün verdiği, koddan doğrulanamayan prod olgusu · **mevcut** = rehberde ya da kodda var, tekrar edilmedi · **yazılıyor** = başka bir
ajanın bugün yazdığı `scripts/dev-*.sh`, `scripts/dev/`, `docker-compose.dev.yml`, `e2e/`, `.claude/skills/dogrula/`. Her bölüm önce "Bugün"ü, sonra "Öneri"yi verir.

## Özet

Yerel yığın prod veri düzleminin biçimini taklit ediyor ama dört yerde yanlış güven veriyor: örnekleme (yerelde %100 ya da %30 olasılıksal, prod'da hatalı izlerin
tamamı ve başarılıların %5'i), küme metrikleri (rollout, JVM/havuz, alarm ve Argo serileri hiç üretilmiyor), log arka ucu (Elasticsearch kapalı) ve LLM (yerelde
hiç yok). İç döngüde önyüz bugün de saniyenin altında (Vite HMR medyan 0,29 sn); Go tarafında taban 8–12 sn, çünkü `internal/api` 192 bin satırlık tek paket;
bugünkü tek yol olan imaja kopyalama bunun üstüne 26 saniyelik önyüz derlemesini ve container yeniden oluşturmayı ekliyor. Go binary'si `frontend/dist` olmadan
`go build -overlay` ile derlenebiliyor; ürün koduna dokunmadan host'ta dosya izlemeli döngü kurulabilir. Demo üretici rastgele ve tohumlanamıyor; istenen altı
senaryodan yalnız OOM olan pod betikli, trafik düşüşü ve regresyonlu rollout hiç yok; bu yüzden dedektör beklentisi de ekran görüntüsü de tekrarlanamıyor. İki yığın
yan yana çalışamıyor: 15 sabit host portu, 19 sabit container adı ve ortak imaj etiketi var; çalışan yığın üç ayrı compose kümesinden oluşmuş ve ClickHouse
volume'unun 31 GB'ının 23 GiB'ı süresiz sistem logu. Öneri: önce hijyen ve parametreler (D1–D2), sonra host'ta izlemeli iç döngü (D3), sonra tek senaryo çizelgesinden beslenen üretici, sıfırlama ve sabit saatli ekran görüntüsü (D5–D9); örnekleme profili, kayıtlı yanıt döndüren LLM ucu ve sürüm sabitleme bağımsız dilimlerdir.

**Ölçüm yöntemi (bir kez).** Makine 16 GiB RAM, 10 çekirdek; yük ortalaması 3,7–7,5 (başka ajan çalışıyordu), süreler ±%20 oynayabilir. Go: `go build -overlay <dist
taslağı> -o $TMPDIR/… .`; "düzenleme" = overlay ile tek `var` eklenmiş dosya kopyası; soğuk = boş özel `GOCACHE`. Önyüz: bu dalın `frontend/` kaynağı `$TMPDIR`'da,
`npm ci`, `vite --port 4899`; HMR = Playwright ile `LoginPage.tsx` yazımı → DOM'da görünene dek (2 × 5 ölçüm); oturumlu sayfa ölçülmedi. Yığın: `docker ps / stats /
inspect / system df -v / logs -t`, `clickhouse-client --readonly=1`, VM `status/tsdb`. Sayım tanımları: `time.Now()` → `grep -rn 'time\.Now()' <dizin> --include='*.go' |
grep -v _test.go | wc -l`; sabit host portu → `grep -nE '^\s+- "[0-9.:]+:[0-9]+"' docker-compose*.yml`; container adı → `grep -c container_name: docker-compose*.yml`.

## 1. Parite tablosu

| Prod bileşeni | Yereldeki karşılığı | Fark | Farkın yerelde gizlediği hata sınıfı |
|---|---|---|---|
| ClickHouse kümesi, 26.2.4.23 (op. bilgisi) | Tek düğüm, aynı etiket (`docker-compose.yml:3`) | Replicated/Distributed ve Keeper yok; veri ~7000× küçük (op. bilgisi; yerel `spans` 16,5 M satır / 808 MiB, ölçüm); sistem logu TTL dosyası (`docker-compose.yml:21-23`) çalışan container'a bağlı değil (ölçüm) | `ON CLUSTER` / yerel-dağıtık tablo karışıklığı, replika gecikmesi, `GLOBAL IN`, sorgu süresi ve belleği, parça sayısı, merge/TTL yükü |
| VictoriaMetrics (uygulama metrikleri) | `victoria-metrics:v1.110.0` tek düğüm (`docker-compose.dev.yml:19-27`, yazılıyor); collector remote write (`scripts/dev/otelcol.yaml:39-41`) | Prod sürümü ve kipi bilinmiyor [V]; yerelde 83.912 seri / 168 ad (ölçüm); demo adları collector'da çevriliyor (`otelcol.yaml:19-33`) | Kardinalite, SDK'ya göre değişen metrik adı/birimi, VM ailesi olmayan serviste span MV'sine düşüş (`internal/anomaly/red_vm_select.go:471`) |
| Thanos querier, bearer + TLS (op. bilgisi) | Aynı VM "querier" diye kaydedilir (mevcut: `docs/DEMO-REALISM.md:119-128`, capability-audit §B) | Kimlik doğrulama `none`, TLS yok, sorgu motoru farklı [V], kısmi yanıt / dedup / dış etiket yok | 401 ve sertifika hataları, kısmi yanıt, VM'de geçip Thanos'ta reddedilen sorgu, `rate` hesap farkı [V], küme etiketi adı |
| Elasticsearch, log (op. bilgisi) | Varsayılan kapalı; `--es` ile 8.15.0 (`docker-compose.yml:201`, yazılıyor). Çalışan uygulamanın env'i `elasticsearch`, ES container'ı yok (ölçüm) | Varsayılan döngü ClickHouse log yolunu sınar | Sorgu DSL'i, alan eşlemesi, zaman aşımı ve maliyet korumaları (capability-audit §B "doğrulamaz") |
| Collector örneklemesi: hatalı %100 + başarılı %5 (op. bilgisi) | Kök compose %30 olasılıksal (`otel-collector-config.yaml:52-53`); dev collector %100 (`scripts/dev/otelcol.yaml:48-51`); demo kök span'lerin %15'ini atar (`cmd/demo/main.go:521`) | Kuyruk örneklemesi yok; `ingest.sampling` varsayılanı `off` (`internal/chstore/ingest_sampling_settings.go:97-99`), prod değeri bilinmiyor | Hata oranı şişmesi (gerçek %1 → ~%17: `docs/operator/tail-sampling-weights.md:5-8`), ~20× düşük throughput, ağırlıksız okuyucular (`docs/architecture/sampling-weight.md:297`), span yolunda sahte trafik düşüşü, log → iz bağlantısının boşa düşmesi |
| Redis | Tek örnek; compose `valkey:8.1.11` (`docker-compose.yml:188`), çalışan `redis:7-alpine` (ölçüm) | Tek uygulama kopyası; prod topolojisi bilinmiyor [V] | Lider kilidi yarışı, çift koşan worker, çok kopyada cache tutarsızlığı (`docker-compose.ha.yml` mevcut, döngüde yok) |
| LLM ucu: iç ağ geçidi, yerel modeller (op. bilgisi) | Yok; çalışan env bir internet modeli adı taşıyor (ölçüm), anahtar geçersiz (op. bilgisi) | Hiçbir CoSRE akışı çalışmıyor | Araç çağrısının metin olarak dönmesi (`scripts/dev/gemma4_toolcall_smoke.py`), bağlam penceresi, akış ve gecikme |
| Argo / kube-state-metrics | KSM: simülatör 22 `kube_*` adı (ölçüm; `cmd/demo/k8s_metrics.go:390-498`). Argo: yok | ReplicaSet sayı serileri, `ALERTS`, `argocd_*`, `jvm_*`, `hikaricp_*` yok (§2) | Rollout tespiti, Argo envanteri, alarm paneli, JVM ve havuz sekmeleri yerelde hiç çalışmadan gönderilir |

Mevcut ama döngü dışı: dağıtık ClickHouse için minikube yolu (`docs/local-dev.md:182-207`); `docker build` ister, bu makinede kayıt defteri zaman aşımı (op. bilgisi) yüzünden kullanılamıyor.

## 2. Eksik veri düzlemi

**Bugün.** Prometheus uyumlu uç zaten VM (yazılıyor). İki besleme var: Kubernetes simülatörü VM içe aktarma API'sine 15 sn'de bir yazar
(`cmd/demo/k8s_metrics.go:584-592`), collector uygulama metriklerini remote write ile yollar. Çalışan VM'de `kube_` 22, `container_` 6, `node_` 7 ad var; `jvm_`,
`hikaricp_`, `ALERTS`, `argocd_` 0 ad (ölçüm). Tek demo metriği (`oracledb_top_sql_elapsed_seconds`, 46.900 seri) serilerin %56'sı; VM RSS 671 MiB, bayraktaki tavan 256 MiB (`docker-compose.dev.yml:22`). HPA, PVC, quota, cronjob serilerini uygulama sorgulamıyor (keşif grep'i: test dışı 0 kullanım); üretilmeleri gerekmez.

| Aile | Simülatör üretiyor | Üretmiyor, uygulama sorguluyor |
|---|---|---|
| Pod durumu, restart, OOM | `kube_pod_info/owner/status_phase`, `…_status_{ready,restarts_total,last_terminated_reason,waiting_reason}`, istek/limit (`k8s_metrics.go:390-414`) | `kube_pod_status_unschedulable` (`internal/thanos/probe_extras.go:45`), `kube_pod_container_info`, `kube_pod_labels` (`internal/rollout/v2fetch.go:206,683`) |
| CPU, bellek, ağ, node | `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`, CFS, ağ (`:428-437`); `node_*` (`:504-513`) | yok |
| İş yükü, rollout | `kube_replicaset_owner`, `kube_deployment_{spec_replicas,status_replicas_ready,status_condition}`, statefulset (`:444-456`) | `kube_replicaset_{spec_replicas,status_ready_replicas,created}` (`internal/rollout/ksm.go:45-47`; `:62-64` kapısı geçilmez), `kube_deployment_metadata_generation` (`internal/rollout/v2probe/catalog.go:389`); `service.version` sabit `1.0.0` (`cmd/demo/main.go:607`) |
| JVM, havuz | Yalnız OTLP `process.runtime.*`, `db.client.connections.*` (`cmd/demo/main.go:1797-1823`) | `jvm_memory_used_bytes` / `jvm_memory_bytes_used` (`internal/runtimesig/model.go:206-208`), `hikaricp_connections_*` (`internal/runtimesig/hikari.go:53`) |
| Alarm, Argo | yok | `ALERTS` (`internal/thanos/health_queries.go:83`), `argocd_app_info` (`internal/argocd/discover.go:59`) |

- **Öneri, üretici:** `cmd/demo` genişletilir, ayrı üretici yazılmaz. Simülatör span üreticisiyle aynı kimlik yardımcılarını kullanıyor (`docs/DEMO-REALISM.md:68-70`) ve hazır olmayan pod'un trafiğini kardeşlerine kaydırıyor (`cmd/demo/main.go:320`); ayrı süreç bu bağı koparır. Maliyet: `k8s_metrics.go` 614 satır, aile başına dosyaya bölünür. Risk: seri sayısı ve VM belleği. Diğer seçenek (gerçek kube-state-metrics + sahte küme): adlar gerçek olur ama yeni imaj ister ve span kimlikleriyle eşleşmez.
- **Öneri, uç profili:** (A, önerilen) VM tek uç kalır, compose profili adlandırılır, demo kardinalitesi sınırlanır: ek bellek 0. (B) İkinci VM örneği "thanos-sim", TLS açık, yalnız küme metrikleri: prod'daki iki ayrı arka ucu ve sertifika yolunu temsil eder, bearer'ı etmez; +100–200 MiB (tahmin). (C) Gerçek Prometheus/Thanos imajı: sorgu motoru paritesi; imaj yerelde yok, +200–400 MiB (tahmin), K10'a bağlı.
- **Öneri, örnekleme profili:** dev collector yapılandırmasının ikizi: `tail_sampling` ile "durum = hata → tut" ve "%5 olasılıksal"; seçim env ile (`full` | `prod`), `full` kalır. İşlemci çalışan collector imajında var (ölçüm: `otelcol-contrib components`). Yan etki: izler karar bekleme süresi kadar geç görünür; o sürenin prod değeri bilinmiyor [V]. Uygulamanın `ingest.sampling` kipi prod'la eşlenmeden profil yarım kalır (K5).
- **Bellek (ölçüm):** clickhouse 3,46 GiB, victoriametrics 671 MiB, otel-collector 217 MiB, coremetry 178 MiB, go-demo 25 MiB, redis 13 MiB; toplam ≈ 4,5 / 11,67 GiB. **Ek (tahmin):** `tail_sampling` +10–30 MiB (30 span/sn × bekleme), thanos-sim +100–200 MiB, ES +1–2 GiB (capability-audit karar 2). **Açılış:** uygulama 0,76 sn (ölçüm: container başlangıcı → `[api] HTTP listening`); ClickHouse sağlık denetimi 5 sn aralıklı (`docker-compose.yml:26`); boş volume ile toplam açılış ölçülmedi (ikinci proje adıyla `time scripts/dev-up.sh`).

## 3. İç döngü

| Adım | Bugün (ölçüm) | Hedef |
|---|---|---|
| Önyüz düzenleme → ekranda (Vite HMR) | 0,18–0,29 sn, medyan 0,29 (10 ölçüm) | ≤ 1 sn: karşılanıyor (mevcut: `make dev-ui`, `docs/local-dev.md:75-85`) |
| Vite açılışı; `/login` ilk yükleme | 0,5–0,6 sn; 1,0–1,1 sn (210 istek); süreç ağacı 341 MiB | ≤ 3 sn |
| Önyüz düzenleme → ekranda, imaj yolu | `tsc` 20,7 sn + `vite build` 5,6 sn = 26,3 sn, üstüne Go + imaj + yeniden oluşturma (`scripts/dev-deploy.sh:36-55`) | Yalnız sürüm öncesi |
| Go düzenleme → binary (`internal/api`) | host 7,5–8,5 sn (derleme 4,6 + bağlama 4,3); linux çapraz 7,3 sn | ≤ 10 sn |
| Go düzenleme → binary (`internal/chstore`) | host 12,4–12,5 sn; linux çapraz 12,1 sn | ≤ 15 sn |
| Go düzenleme → API cevap, uçtan uca | Ölçülmedi (container yeniden oluşturmak yasaktı; `dev-deploy.sh:56` süreyi kendi yazar). Parçalardan alt sınır 8–13 sn + imaj kopyalama + 2 sn'lik yoklama (`scripts/dev/lib.sh:75`) | Host binary + izleme: 9–14 sn (parçalardan hesap) |

Taban Go derlemesidir: `internal/api` test dışı 708 dosya / 192.415 satır tek paket (ölçüm: `find internal/api -maxdepth 1 -name '*.go' ! -name '*_test.go' | xargs
cat | wc -l`). Değişiklik yokken derleme 0,8 sn, boş `GOCACHE` ile tam derleme 37,1 sn; `-ldflags "-s -w"` bağlamayı kısaltmıyor (8,6 sn). 8 sn'nin altı paket
bölmeyi gerektirir (kapsam dışı). Dosya izleyici yok: `air`, `watchexec`, `entr`, `fswatch` kurulu değil, `fsnotify` `go.mod`'da yok (ölçüm).

**Boş `dist` ile derleme.** Gömme `frontend/dist` ister (`main.go:68`); bugün ne derleme etiketi ne diskten sunma seçeneği var, CI üç işte satır içi taslak yazar
(`.github/workflows/ci.yml:106-109`), düzenleme hook'u taze klonda düşer (`.claude/hooks/post-edit-check.sh:35`). (1, önerilen) `go build -overlay`: taslak
`index.html` ağaca yazılmadan eşlenir; ölçüldü, çalışıyor; ürün kodu değişmez. (2) `make stub-ui`: `dist` yoksa taslak yazar, hook ve CI aynı hedefi kullanır; risk:
taslağın imaja girmesi. (3) Derleme etiketiyle ikinci gömme dosyası: ürün kodunu böler, gerekmez. Öneri: izleme hedefi için 1, hook ve CI için 2.

**Öneri: düzen ve hedefler.** Bağımlılıklar compose'ta (uygulama container'ı olmayan profil), Go binary'si host'ta izlemeyle, önyüz Vite'tan. Hedefler:
`make stub-ui`, `make dev-deps`, `make dev-go` (izle → overlay ile derle → yeniden başlat), `make dev-ui` (mevcut, `Makefile:46`), `make dev` (üçü). İzleyici:
(a, önerilen) `go run <izleyici>@sürüm` ile Makefile'da sabitlenmiş araç, kurulum adımı yok; (b) bağımlılıksız `find -newer` döngüsü; (c) elle kurulan araç.

**Env.** Var olanlar: `COREMETRY_HTTP_ADDR`, `_GRPC_ADDR`, `_OTLP_HTTP_ADDR` (`internal/config/config.go:749,764,770`); `_CH_ADDR`, `_CH_DATABASE`, `_REDIS_URL`,
`_LOGS_BACKEND` (`config.go:641,644,868,871`); `COREMETRY_MODE` (`main.go:157`); `_AI_PROVIDER`, `_AI_BASE_URL` (`config.go:931,940`); `COREMETRY_JWT_SECRET` sabit olmalı,
yoksa her yeniden başlatmada oturum düşer (mevcut: `docs/local-dev.md:217`). Eksik iki ayar: Vite proxy hedefi (`frontend/vite.config.ts:65-66`) ve collector'ın uygulama hedefi (`scripts/dev/otelcol.yaml:36`).

## 4. Senaryolu demo veri

**Bugün.** Olaylar rastgele ve küresel: 3 sn'de bir %2 olasılıkla dört türden biri başlar, tüm servisleri birden etkiler (`cmd/demo/realism.go:124,148-153`); tohum
bayrağı ya da env yok. Tek betikli çizelge OOM pod'udur: süreç başlangıcına göre saf fonksiyon (`cmd/demo/k8s_incident.go:68`), ama pod adı rastgele nesil taşır
(`cmd/demo/main.go:336`). Rollout yalnız süreç yeniden başlayınca "olur". Altın setler üç ayrı yerde ve biçimde:

| Set | Yer ve biçim | Sayı | Not |
|---|---|---|---|
| Anomali (etiketli) | `internal/anomaly/testdata/eval/anomaly_cases.sample.jsonl`; `{id, family, subject, metric, from, to, real, series}` (`internal/anomaly/eval.go:36-64`) | 8 sentetik | Kapı aile başına ≥ 50 gerçek vaka ister (`eval.go:277`); sentetik set "ölçülmedi" sayılır |
| ML olay yeniden yargılama | `internal/ml/testdata/replay/<vaka>/{ml_events.jsonl,expect.json}` | 6 dizin | Ham seriden dedektör koşmaz, yayın kapılarını yargılar (`internal/ml/replay.go:21-23`) |
| CoSRE evalset | `internal/copilot/evalset/**/*.json`, şema `coremetry.evalset/1` | 25 dosya, 160 vaka | Canlı model ister, yoksa atlar (`internal/api/evalset_test.go:56`) |

**Öneri: tek senaryo çizelgesi.** Üreticinin, dedektör beklentisinin ve ekran görüntüsü sayfa listesinin okuduğu tek tablo (kimlik, hedef, başlangıç, süre, şiddet,
beklenen kural ve özne). Betikli kip bayrakla açılır; varsayılan demo değişmez (ilk kurulumda görülen veri odur: `docs/DEMO-REALISM.md:3-5`). Tohum ve başlangıç
anı bayrakla verilir; pod nesli tohumdan türetilir. Sentetik beklentiler ayrı dosyada durur ve `eval.go:277` kapısına sayılmaz (K8).

| # | Senaryo | Hedef | T0 + | İmza | Bugün |
|---|---|---|---|---|---|
| S1 | Hata sıçraması | Tek servis | 20–30 dk | Hata %1 → %25, gecikme sabit | Yalnız küresel ve rastgele |
| S2 | Yavaşlama | Tek servis ve veritabanı bağımlılığı | 45–60 dk | p95 × 3, hata sabit | Yalnız küresel ve rastgele |
| S3 | Trafik düşüşü | Tek servis | 75–85 dk | İstek hızı −%80 | Yok (taban 0,05 senaryo/sn: `cmd/demo/main.go:2114`) |
| S4 | OOM olan pod | Bir ağ geçidi pod'u | 4–22 dk | Bellek rampası, iki OOMKill, CrashLoop | Mevcut (`k8s_incident.go:81-114`) |
| S5 | Regresyonlu rollout | Tek deployment | 100–115 dk | Yeni ReplicaSet + `service.version` değişimi; hata yalnız yeni sürümde | Yok (§2 rollout satırı) |
| S6 | Tek pod aykırı | Üç pod'dan biri | 130–145 dk | Yalnız o pod'da gecikme × 3, bellek normal | Yalnız S4'ün yan etkisi olarak |

**Öneri: sıfırlama.** Yerel veri: `coremetry` veritabanı 3,59 GiB, `system` 23,19 GiB, volume 31,09 GB; VM volume'u 400 MB (ölçüm).

| Seçenek | Ne | Süre | Risk |
|---|---|---|---|
| R1 (önerilen) | Üret + önbellekle: görüntü yoksa betikli üretici geçmişi doldurur, sonra `BACKUP DATABASE`; varsa `RESTORE`. VM için dışa/içe aktarma uçları | `RESTORE` 5–20 sn, doldurma 30–90 sn (tahmin; `system.backups` süreleriyle ölçülür) | `backups` diski tanımlı değil (compose değişikliği); üreticide hızlandırılmış kip yok (`-duration` gerçek zamanlı) |
| R2 | Volume'u `tar` ile kopyala (yığın durur) | 31 GB için 1–3 dk, sistem logu temizlenirse 20–40 sn (tahmin) | Container durdurma; kullanıcı ve ayarlar da geri döner; volume komutları Claude izinlerinde `deny` |
| R3 | Yalnız yeniden üret (görüntü yok) | Her seferinde doldurma süresi | En yavaş; ama her sıfırlamada gerçek ingest yolu sınanır |

Kısıt: görüntü yaşlanınca saklama süresi veriyi siler (span ve log 30 gün, metrik 7 gün: `config.yaml:24-27`; VM 7 gün: `docker-compose.dev.yml:22`). Bu yüzden sabit
başlangıç anlı görüntü ayrı bir veritabanında (`COREMETRY_CH_DATABASE`) ya da ikinci projede (§6), uzun saklama ve üreticisiz tutulur; canlı yığın akmaya devam eder.

## 5. Doğrulama

**Mevcut / yazılıyor (tekrar edilmedi):** tek komut, `redhat` ve `dark` tam sayfa görüntü, konsol hataları, başarısız istekler, önceki görüntüyle fark oranı
(`scripts/dev-shot.sh`, `e2e/shot.mjs`; tasarım capability-audit §B); `/dogrula` kanıt bloğu. Gece işi 10 liste rotasında vitals ve axe ölçer, görüntü almaz
(`frontend/e2e/nightly.pw.ts:28-33`). **Eksik kalan üç şey:**

1. **Sabit saat ve dilim.** `e2e/shot.mjs` içinde `timezoneId`, `locale`, `reducedMotion`, `clock` yok (ölçüm: grep 0). Önyüzde test dışı 147 `Date.now()` ve 16 `new Date()` var (keşif sayımı), zaman tarayıcı diliminde biçimleniyor (`frontend/src/lib/format/time.ts:16-25`). Tasarım: bağlam `timezoneId: UTC`, sabit dil (`coremetry.lang`, `frontend/src/lib/i18n.ts:160`), `reducedMotion: reduce` (`frontend/src/styles/globals.css:934`), `page.clock` ile görüntünün bitiş anı; URL'ye `refresh=off` (`frontend/src/lib/autoRefresh.ts:64-74`) ve `range=custom:<from>-<to>` (`frontend/src/lib/urlState.ts:8-11`). Sunucu saati sabitlenmez: `internal/api` içinde 487 `time.Now()` var (ölçüm). Pencere almayan uçlar (`/api/problems`: `internal/api/api.go:8155`; servis haritası yalnız `since`: `api.go:3096`; pod envanteri: `internal/api/thanos_handlers.go:101`) "canlı bölge" sayılır ve maskelenir.
2. **Sayfa listesi.** Tek dosya: ad, yol, sekmeler, gereken senaryo, kimliğin hangi uçtan bulunacağı, maske seçicileri. Kimlikler canlı veriden değil §4 çizelgesinden bulunur. Yedekler: `/service-map`, `/endpoints`, `/databases/slow-queries`, `/dashboards`.
3. **Temel görüntüler.** Bugün yalnız yerel `.prev.png`; `e2e/shots/` git dışı (`.gitignore:133`). Tasarım: ayrı temel dizini, iki eşik (sabit pencereli sayfa için katı, canlı bölgeli sayfa için gevşek), güncelleme ayrı ve açık komut (K7).

| # | Sayfa | Neden | Senaryo | Pencere |
|---|---|---|---|---|
| 1 | `/services` | En sıcak liste, tablo standardı, RED ve örnekleme rozeti | S1–S3 | Sabitlenir (`api.go:1670-1673`) |
| 2 | `/service?name=` | Grafik katmanı ve sekmeler (her sekme ayrı görüntü: operatör, 2026-11-28) | S1, S2 | Sabitlenir (`api.go:2159-2170`) |
| 3 | `/problems` | Dedektör çıktısı, öncelik standardı | S1–S6 | Canlı: göreli zaman maskelenir |
| 4 | `/anomalies/:id` | Kanıt ve grafik; kimlikli detay | S1 | Kimlik çizelgeden [V] |
| 5 | `/trace?id=` | Şelale, tekrar çipi (`docs/DEMO-REALISM.md:47-56`) | S2 | Sabit; iz kimliği API'den |
| 6 | `/logs` | ClickHouse ve ES yolu (ikinci tur `--es`) | S1 | Sabitlenir (`internal/api/api_logs.go:407-408`) |
| 7 | `/clusters` | Thanos yolu, beş demo kümesi | S4 | Sabitlenir (`api.go:1573`) |
| 8 | `/pod?cluster=&namespace=&pod=` | OOM ve kardeş bandı | S4, S6 | Envanter canlı, grafik sabit [V] |
| 9 | `/rollouts` | Bugün yerelde boş (`internal/rollout/ksm.go:62-64`) | S5 | D6 sonrası anlamlı |
| 10 | `/cosre` | Sohbet akışı, kartlar | Kayıtlı yanıt | D10 sonrası |

## 6. Paralellik

**Bugün.** Hiçbir compose dosyasında `name:` ya da `COMPOSE_PROJECT_NAME` yok; hiçbir host portu env'den gelmiyor.

| Host portu | Ne | Nerede | Env ile değişir mi |
|---|---|---|---|
| 9000, 8123, 6379, 9200 | ClickHouse (2), Redis, Elasticsearch | `docker-compose.yml:9-10,192,213` | Hayır |
| 8088, 8089 | Uygulama; ikinci kopya | `docker-compose.yml:56`, `docker-compose.distributed.yml:126`; `docker-compose.ha.yml:49` | Hayır (container içi: `config.go:749`) |
| 14317, 14318, 13133 | Collector | `docker-compose.yml:261-263` (dev'de kapalı: `docker-compose.dev.yml:34`) | Hayır |
| 3200, 3000, 4040, 8081 | Tempo, Grafana, Pyroscope, JBoss demosu (profil) | `docker-compose.yml:242,278,304,380` | Hayır |
| 8428 | VictoriaMetrics | `docker-compose.dev.yml:24` | Hayır (istemci: `scripts/dev/lib.sh:17`) |
| 4888; 8088 hedefi | Vite; proxy | `frontend/vite.config.ts:63`; `:65-66` | Yalnız `--port`; hedef hayır |
| 8088 (istemci) | Betik ve testler | `scripts/dev/lib.sh:16`, `e2e/shot.mjs:17`, `frontend/e2e/playwright.config.ts:19` | Evet |
| 14318; 8090 | Demo `-endpoint`; perf/parite tabanı | `cmd/demo/main.go:50`; `scripts/perf/budget.json:3`, `Makefile:291` | Bayrak; kısmen |

Kod içi varsayılanlar (`:8088`, `:4317`, `:4318`, `127.0.0.1:9000`: `config.go:508-515`) env ile değişir. Port dışı çakışmalar: 19 sabit `container_name`
(`docker-compose.yml:4,51,151,167,189,202,236,256,276,302,337,395`, `docker-compose.dev.yml:21`, ha 1, distributed 4, grafana-mcp 1); betiklerde sabit adlar
(`scripts/dev/lib.sh:18`, `dev-seed.sh:26`, `dev-up.sh:64`, `dev-deploy.sh:46-47`); ortak durum dizini ve kilit (`lib.sh:19-21`, `dev-deploy.sh:30`); ortak imaj etiketi
(`dev-deploy.sh:47-49`); proje adını ilk kopyanın container'ından okuyan mantık (`lib.sh:44-47`). Ölçüm: çalışan altı container tek projede ama üç ayrı compose kümesinden (biri başka worktree'den) oluşturulmuş; capability-audit §C'deki ezme olayının zemini budur.

**Öneri.** (1, önerilen) Proje adı ve port ön eki env'den (`ports: "${ÖNEK}8088:8088"` biçimi), `container_name` kaldırılır, betikler `docker compose ps` ile bulur,
durum dizini ve imaj etiketi proje başına; varsayılan proje adı bugünkü kalır ki mevcut volume'lar öksüz kalmasın. (2) İkinci yığın yerine aynı ClickHouse'ta ikinci
veritabanı + farklı portta ikinci binary: +~180 MiB, ama VM ve collector paylaşılır. (3) Bugünkü tek yığın + deploy kilidi: yalnız sıra bekletir. İki tam yığın
≈ 9 GiB ister (4,5 GiB × 2); compose'ta bellek tavanı yok (ölçüm: 0 sınır); K1 cevaplanmadan iki tam yığın önerilmez.

## 7. CoSRE

**Bugün.** `openai` sağlayıcısı boş anahtar ve özel taban adresle çalışır (`internal/config/config.go:931-942`, `internal/ai/provider/openai.go:101-108`): OpenAI
uyumlu her uç kod değişmeden takılır. Bağımsız sahte sunucu yok; test içi en uygun çekirdek betikli `newLoopLLM` (`internal/api/chat_trace_followup_loop_test.go:47`).
Modelsiz sözleşme kapısı ağsızdır ve sahte sunucuyu bilerek yasaklar (`scripts/cosre-contracts.sh`, 3 paket ~47 test; `internal/api/cosre_contracts_test.go:30-34`). Host'ta yerel model çalıştırıcı kurulu değil (ölçüm).

- **Y1, kayıtlı yanıt döndüren uç (önerilen; varsayılan ve testte).** Küçük bir Go komutu, `newLoopLLM` betik modelini test dışına taşır; fikstür = soru + araç sonucu → `{content, tool_calls}`. Yeni imaj yok, ~20 MiB (tahmin), deterministik: §5'in 10. sayfası ve `/dogrula`'nın CoSRE adımı. Sınır: akışı doğrular (SSE, araç döngüsü, kart çizimi), kaliteyi değil.
- **Y2, küçük yerel model (isteğe bağlı; yalnız elle keşif).** Host 16 GiB, Docker'a 11,67 GiB ayrılı; 3–5 GiB ister (tahmin), imaj çekilemediği için yalnız host kurulumuyla. Araç çağrısı davranışı prod modellerinden farklıdır (`scripts/dev/gemma4_toolcall_smoke.py` ile ölçülür). Deterministik olmadığı için testte kullanılmaz.

İlişki: sözleşme testleri değişmez ve uca bağlanmaz. Uç fikstürleri evalset vakalarının kimliklerini taşıyabilir ama evalset puanına girmez; `make evalset` gerçek
model ucu ister (`Makefile:62-69`), o uç yoksa kalite yerelde **doğrulanmaz** (K3).

## 8. Tekrarlanabilir ortam

| Araç | Bugün sabit mi | Host (ölçüm) | Seçenek |
|---|---|---|---|
| Go | `go.mod:3` `go 1.26.0`, `toolchain` yok; CI `'1.26'` (`ci.yml:93`); imaj `golang:1.26-alpine` (`Dockerfile:25`) | 1.26.2 | `toolchain` satırı |
| Node | Hiçbir dosyada; yalnız CI ve imaj 22 (`ci.yml:29`, `Dockerfile:9`) | **25.9.0** | `.nvmrc` + `engines` (K9) |
| golangci-lint | Yalnız CI `v2.12.2` (`ci.yml:216-218`); `Makefile:39` `@latest` | 2.12.2 | `go run …@v2.12.2` (`.golangci.yml:6-8`) |
| Playwright | `1.65.0` tam sürüm (`frontend/package.json`); tarayıcıyı yalnız gece işi kurar | Headless kabuk 1254 var, tam Chromium 1234 (eski) | Kurulum denetimi |
| İmajlar | Tam etiket, digest yok (ClickHouse `docker-compose.yml:3`, VM `docker-compose.dev.yml:20`); istemci aşaması `24.8` (`Dockerfile:43`); `cloudflared:latest` (`docker-compose.yml:150,166`) | ClickHouse aynı; Redis compose'tan farklı imajla çalışıyor | Digest + sapma denetimi |

Seçenekler: (1, önerilen) repo içi asgari sabitleme + `make doctor`: Go/Node/lint sürümü, `node_modules` ile kilit dosyasının uyumu, `dist` ya da taslak, gereken
imajların varlığı, boş disk, çalışan container'ların compose'la uyumu. (2) Tek sürüm dosyası okuyan sürüm yöneticisi: tek kaynak, ama yeni araç kurulumu. (3) Devcontainer
ya da Nix: en güçlü, ama taban imaj çekmek gerekir. Gerekçe ölçümden: ana kopyanın `node_modules`'ı kilit dosyasının gerisinde (kurulu `react-router` 6.30.6, istenen `^7.18.5`; `@rolldown/plugin-babel` eksik), bu dalın kaynağı onunla derlenmiyor.

**Klon → çalışan yığın, bugün.** Ölçülen parçalar: `npm ci` 5,5 sn (npm önbelleği sıcak; soğuk ağ ölçülmedi), `tsc` + `vite build` 26,3 sn, soğuk Go derlemesi
37,1 sn: ≈ 69 sn yerel hesap. Ölçülmeyenler: `git clone` (paket 143 MiB), `go mod download` (modül önbelleği 4,2 GiB), imaj çekme (≈ 1,4 GB), boş volume ile ilk
açılış. Rehberdeki `make docker-up` `--build` yapar (`Makefile:141`) ve bu makinede kayıt defteri zaman aşımı yüzünden tamamlanmıyor (op. bilgisi): belgeli tek
komut yolu burada çalışmıyor. Hedef: sıcak önbellekle tek komut ≤ 2 dk, temiz makinede ≤ 10 dk (tahmin; `make doctor` süreyi yazar).

## Kapsam dışı

Prod verisinin yerele alınması (anonim ya da ham); prep ortamı ve ona erişim (ayrı iş, capability-audit karar 3); `internal/api` paketinin bölünmesi (Go derleme
tabanını indiren tek şey, ayrı audit); sunucu saatinin enjekte edilmesi (487 çağrı yeri); CI iş akışları ve gece görsel regresyon işi; minikube / Helm yolu; Docker
kayıt defteri erişiminin onarımı; ES yolunun varsayılana alınması; yedinci senaryo (Kafka tüketici gecikmesi: metrik var, betik yok, `cmd/demo/main.go:1842`).

## Dilim planı

Her dilim tek sürüm ve bağımsız gönderilebilir. Sıra bağımlılıkları: D1 → D3; D5 → D6 → D8 → D9 (katı kip); gerisi serbest.

| # | Kapsam | Giriş ölçütü | Çıkış ölçütü | Koruma | Risk |
|---|---|---|---|---|---|
| D1 | Proje adı + port ön eki env'den; `container_name` kaldırma; betiklerde servis adıyla bulma; proje başına durum dizini ve imaj etiketi | Yazılmakta olan dev betikleri birleşmiş | İki proje yan yana `/api/version` verir | Sayım: env'siz host portu = 0, `container_name` = 0 (Ölçüm yöntemi tanımları) | Varsayılan ad değişirse volume'lar öksüz kalır |
| D2 | Compose bellek tavanları; sistem logu TTL dosyasının dev yığınına bağlanması; disk denetimi | K1, K2 | `system` veritabanı ≤ 2 GiB; tavanlar `docker stats`'ta görünür | `dev-up` çıktısında sınır aşımı uyarısı | Düşük tavan ClickHouse'u OOM'a sokar |
| D3 | İç döngü: `stub-ui`, `dev-deps`, `dev-go`, `dev`; Vite proxy hedefi ve collector hedefi env'den | D1 | Go düzenleme → API ≤ 15 sn, önyüz ≤ 1 sn (ölçülüp yazılır) | CI'da `dist`'siz derleme aynı hedeften; döngü ölçüm betiği | Host ve container uygulaması aynı anda çalışırsa çift worker |
| D4 | Collector örnekleme profili (`full` \| `prod`) | K5 | `prod` profilinde saklanan hata payı beklenen bantta (`tail-sampling-weights.md:5-8`) | Collector yapılandırma doğrulaması + oran denetimi | Karar beklemesi izleri geciktirir |
| D5 | Senaryo çizelgesi, tohum, başlangıç anı; S1–S3 ve S6 üreticide (bayrakla) | K8 | Aynı tohum → 5 dk kovalarında aynı span sayısı | Çizelge saf fonksiyonu için tablo testi | Varsayılan demo gerçekçiliğini bozma (`docs/DEMO-REALISM.md:58-60`) |
| D6 | Rollout serileri + `service.version` değişimi; S5 | D5 | `/rollouts` yerelde regresyonlu rollout'u gösterir | "Zorunlu ad listesi ⊆ simülatörün yazdığı adlar" testi | Seri sayısı, VM belleği |
| D7 | JVM / havuz / alarm (ve istenirse Argo) serileri | K4 | İlgili sekmeler yerelde dolu | D6'daki ad listesi testi genişler | Prod adlarıyla uyuşmayan sahte güven |
| D8 | `dev-reset`: görüntü üret / geri yükle; `backups` diski; VM dışa/içe aktarma | D5, K6 | Sıfırlama ≤ 60 sn (ölçülür); sonrası kova sayıları manifestle aynı | Manifest özeti denetimi | Saklama süresi görüntüyü siler |
| D9 | Sabit saat, dilim, dil, azaltılmış hareket; sayfa listesi; temel dizini ve iki eşik | `dev-shot` birleşmiş; katı kip için D8, K7 | 20 görüntüde art arda iki koşu eşik altında | `make shots-check`; `/dogrula` önyüz adımı | Temel görüntü boyutu (ölçülmedi: ilk tam koşuda `du`) |
| D10 | Kayıtlı yanıt döndüren LLM ucu + dev profili | yok (Y2 için K3) | `/cosre` yerelde betikli soruya araç çağrısıyla cevap verir | Fikstürlerin evalset şemasıyla doğrulanması | Akışın geçmesi kalitenin geçmesi sanılır |
| D11 | Sürüm sabitleme + `make doctor` | K9 | Bu makinede `doctor` yeşil; klon → yığın süresi rehbere yazılır | CI'da `doctor` | Katı Node sürümü operatör makinesini kırar |
| D12 | Rehber düzeltmeleri (aşağıdaki bayat satırlar) | yok | Bayat satır sayısı 0 | Yok (belge) | Yok |

## Operatör kararları

1. **Bellek ve disk sınırı.** Docker 11,67 GiB, yığın ≈ 4,5 GiB, disk 47 GiB boş (ölçüm; op. bilgisi 53). (a) Tek yığın, sınırsız (bugün); (b) compose tavanları + hafif ikinci yığın (§6 seçenek 2); (c) iki tam yığın (≈ 9 GiB). Öneri (b) ve ClickHouse volume'u için 10 GiB bütçe. Cevapsızsa D2 bekler, D1 tek yığınla sınanır.
2. **Yerel sistem loglarının silinmesi (23 GiB).** Veri silme: TTL dosyası bağlanınca ClickHouse container'ı yeniden oluşturulur ve eski loglar silinir. (a) Bağla ve temizle; (b) dokunma. Öneri (a). Cevapsızsa D2 bekler.
3. **Yerel model çalıştırmak mümkün mü?** (a) Yalnız kayıtlı yanıt ucu; (b) ek olarak host'a kurulu küçük model (3–5 GiB, Docker payı düşürülür); (c) geliştirme makinesinden iç ağ geçidine erişim. Öneri: şimdi (a), kalite için (c) prep işiyle birlikte. Cevapsızsa D10 Y1 ile ilerler, Y2 bekler.
4. **Prod'la birebir olması gereken metrik adları.** Adaylar: JVM (`jvm_memory_used_bytes` mı `jvm_memory_bytes_used` mı), havuz (`hikaricp_connections_*`), `ALERTS`, `argocd_app_info`, küme etiketi adı, ReplicaSet ve generation serileri. (a) Prod Thanos'tan yalnız ad listesi (değer yok) alınır; (b) koddaki sorgu listesi esas alınır. Öneri (a). Cevapsızsa D7 bekler; D6 (b) ile ilerler.
5. **Prod `ingest.sampling` kipi ve yerel varsayılan profil.** Kip `off` / `shadow` / `on` hangisi; collector politika adı ya da tracestate damgalıyor mu? Yerel varsayılan: (a) `full`, oran/hata/yüzdelik okuyucularına dokunan işte `/dogrula` `prod` profilini zorunlu kılar; (b) varsayılan `prod`. Öneri (a). Cevapsızsa D4 bekler.
6. **Sıfırlamanın sildiği kapsam.** (a) Yalnız telemetri, kullanıcılar ve ayarlar kalır; (b) tüm veritabanı. Her koşuda açık onay bayrağı istensin mi? Öneri (a) + bayrak. Cevapsızsa D8 bekler.
7. **Temel görüntüler nerede durur?** (a) Git dışı yerel dizin; (b) repo içinde; (c) LFS ya da yapıt deposu. Öneri: önce (a), boyut ölçülüp bir hafta kararlı kalınca (b). Cevapsızsa D9 gevşek kiple ilerler.
8. **Senaryo kümesi ve sentetik beklentinin statüsü.** S1–S6 yeterli mi; sentetik beklentilerin gerçek etiketli sete ve `eval.go:277` kapısına hiç sayılmaması onaylanıyor mu? Öneri: evet, ayrı dosya. Cevapsızsa D5 bekler.
9. **Node sürümü.** Host 25.9.0, CI ve imaj 22. (a) 22'ye sabitle, `doctor` yalnız uyarır; (b) katı zorla; (c) aralığı genişlet. Öneri (a). Cevapsızsa D11'in Node kısmı bekler.
10. **Yeni imaj gerektiren parite (gerçek Prometheus/Thanos, §2 C).** (a) Şimdilik yalnız eldeki imajlar; (b) kayıt defteri erişimi onarılır ya da imajlar dosyadan yüklenir. Öneri (a). Cevapsızsa §2 A ile ilerlenir.

## Bayat satırlar

- `docs/local-dev.md:12` ve `CONTRIBUTING.md:11` "Go 1.25" → `go.mod:3` (`go 1.26.0`).
- `docs/local-dev.md:20` `main.go:60` → `main.go:68`; `:81` `vite.config.ts:32-37` → `frontend/vite.config.ts:62-68`; `:154` `store.go:1854` → `internal/chstore/store.go:4247`; `:177` ve `:219` `main.go:355-369` / `:368` → `main.go:293`, `:409-413`.
- `docs/local-dev.md:157-160` "migrations 0001-0014", gömülü liste 0014'te bitiyor → `migrations/` 0017'ye kadar (23 dosya), `migrations/embed.go` 0015–0017'yi de gömüyor.
- `docs/local-dev.md:16,131` ve `Makefile:37-38` "golangci-lint advisory / CI continue-on-error" → CI'da katı kapı (`.github/workflows/ci.yml:216-219`).
- `CONTRIBUTING.md:14-15` YANLIŞ: `make docker-up` yalnız bağımlılıkları değil uygulamayı ve Elasticsearch'ü de başlatır (`Makefile:141`, `docker-compose.yml:30,56,200`); ardından `./coremetry` 8088'de çakışır (rehberin kendi satırı: `docs/local-dev.md:214`).
- `docs/local-dev.md:104-106` "%30 örnekleme" kök compose için doğru (mevcut), dev yığını için değil (`scripts/dev/otelcol.yaml:48-51`, yazılıyor).
- `docs/DEMO-REALISM.md:120-121` ve `docs/claude/capability-audit.md:67` "VM ve simülatör env'i yalnız repo dışı dosyada" → `docker-compose.dev.yml:19-27,44` (yazılıyor).
- Önce-oku dışı, doğrulanmadı (keşif bulgusu): `docs/operator/tail-sampling-weights.md:12-14` "ekranda henüz bir şey değişmez" ile `docs/architecture/sampling-weight.md` okuyucu kapsamı çelişiyor.
