# Runbook yalıtımı v2 — karar belgesi (2026-09-27, v0.10.970)

**Durum:** yalnız ölçüm yapıldı, karar operatörde. Operatör "Önerilerine
evet" dedi (2026-09-27, öneri 5): önce prod çekirdeğinin Landlock desteği
ölçülecek, karar ondan sonra verilecek. v0.10.970 hiçbir kuralı **uygulamaz**.
Yalnız her rolde bir boot satırı basar. Bu belge o satırın nasıl okunacağını,
üç seçeneği ve önerimi içerir.

Bütün adlar sentetiktir: `cm-synthetic` release, `coremetry-synthetic`
namespace, `worker-synthetic-1` düğümü.

---

## 0. Özet

- **Bu sürümde değişen:** `internal/prochard/landlock*.go` ve `main.go`'daki
  tek boot satırı. Davranış değişmez: syscall yalnız ABI sürümünü sorar, fd
  açmaz, kural seti kurmaz.
- **Önerim (§7):**
  1. **A. Şimdi, çekirdekten bağımsız:** runbook'lar varsayılan olarak ayrı
     bir agent pod'unda koşsun (seçenek 3). O pod'un env'i ve SA token'ı
     kırpılsın.
  2. **B. Boot satırı `abi≥1` derse:** agent pod'unda Landlock trampolini,
     `best-effort` kipte (seçenek 1).
  3. **Sidecar (seçenek 2) rafta kalsın.** Yalnız Landlock prod'da yoksa ve
     agent pod'undan kaldırılamayan bir sır dosyası kalırsa gündeme gelsin.
- **Senden istenenler:** §8'deki boot satırı ve üç kontrol, ardından §9'daki
  karar harfleri.

---

## 1. Bugünkü durum ve kalan riskler

v0.10.966 üç şey getirdi:

- bash adımı minimal bir env allowlist'i görür, `COREMETRY_*` hiçbir zaman
  geçmez;
- süreç her rolde `PR_SET_DUMPABLE=0` çalışır, dolayısıyla
  `/proc/$PPID/environ` kapalıdır;
- adımın süreç grubu, zaman aşımında ve adım bitince öldürülür.

Adım hâlâ Coremetry ile **aynı uid, aynı mount'lar, aynı ağ** üzerinde
koşar. Adım komutu `/bin/sh -c` ile çalışır; imaj alpine olduğu için bu
busybox ash'tir.

### v0.10.966'da belgelenen kalan riskler

Kaynaklar: `docs/DECISIONS.md`, `docs/ENV.md` "Runbook bash adımının
ortamı", `internal/agent/executor.go`.

| # | Risk | Ayrıntı |
|---|---|---|
| R1 | Aynı uid'in okuyabildiği dosyalar | `/app/config.yaml`; monolitikte `extraVolumes` ile bağlanan sır dosyaları (ör. `/var/run/secrets/coremetry/integrations/*`); SA token (`/var/run/secrets/kubernetes.io/serviceaccount/token`) |
| R2 | İç ağ | Küme içi ClickHouse ve Redis adımdan erişilebilir |
| R3 | `setsid` kalıntısı | `setsid` ya da double-fork ile süreç grubundan kaçan iş, adım bittikten sonra yaşamaya devam eder |
| R4 | Varsayılan all-mode | Monolitik pod agent'ı süreç içinde koşar. Adım, api, ingest ve worker ile aynı pod'dadır. Gerçek sınır distributed agent rolüdür |
| R5 | MCP stdio sunucuları | Her rolde aynı uid ile koşarlar; R1 ve R2 onlar için de geçerlidir (`internal/prochard` paket yorumu) |

### Bu belge için yapılan ek gözlemler

Bunlar kod okuyarak ve `helm template` çıktısından çıkarıldı; **prod'da
denenmedi**.

| # | Risk | Kanıt |
|---|---|---|
| R6 | Adım Coremetry'yi durdurabilir | `Dockerfile` `ENTRYPOINT ["./coremetry"]`: init yok, Coremetry PID 1. Go SIGTERM için handler kurar (`signal.NotifyContext`). Çekirdek, namespace içinden init'e yalnız handler'ı olan sinyalleri iletir; `kill $PPID` (SIGTERM) bu yüzden geçer. All-mode'da bu, uygulamanın tamamının yeniden başlaması demektir. SIGKILL ve SIGSTOP init'e ulaşmaz |
| R7 | Kaynak tüketimi | Fork bombası ya da bellek taşması pod'un cgroup'unu paylaşır. All-mode'da OOM, api, ingest ve worker'ı birlikte düşürür |
| R8 | Paketli Redis parolasız | `charts/coremetry/templates/redis.yaml` `args: ["redis-server", "--appendonly", "no", "--save", ""]`, `requirepass` yok. İmajdaki busybox araçlarıyla ya da herhangi bir TCP istemcisiyle cache anahtarları, lider kilitleri ve SSE veriyolu okunup yazılabilir. R2'nin keskin hâli: sızıntıya ek olarak bütünlük riski |
| R9 | Distributed agent pod'u da sır taşır | `helm template` (§8, K5) gösteriyor: agent pod'u `COREMETRY_JWT_SECRET`, `COREMETRY_CH_PASSWORD`, `COREMETRY_INITIAL_PASSWORD`, `COREMETRY_REDIS_URL` alır, chart'ın ortak SA'sını kullanır (`automountServiceAccountToken` ayarlanmamış, yani token bağlıdır) ve `/app/config.yaml` bağlıdır. Yalnız `extraEnv`, `envFrom` ve `extraVolumes` gelmez. Env'i bugün allowlist ile `dumpable=0` korur |
| R10 | Adım çıktısı viewer'a açık | Execution kaydı herkese salt-okunur açıktır (invariant 7). Adımın bastığı her şey viewer'a ulaşır |

---

## 2. Ölçüm (v0.10.970)

`prochard.LandlockABI()` şu çağrıyı yapar:
`landlock_create_ruleset(NULL, 0, LANDLOCK_CREATE_RULESET_VERSION)`.
Çekirdeğin en yüksek ABI sürümünü ya da tipli bir neden döner. Sonuç her
rolde boot'ta, dumpable satırının hemen altında loglanır:

```
[prochard] landlock: abi=4 kernel=6.8.0-synthetic seccomp=filter
[prochard] landlock: unavailable (disabled-at-boot, EOPNOTSUPP) kernel=5.14.0-synthetic seccomp=filter
[prochard] landlock: unavailable (blocked-by-runtime, EPERM) kernel=5.14.0-synthetic seccomp=filter
[prochard] landlock: unavailable (kernel-lacks-landlock, ENOSYS; a seccomp filter may also answer ENOSYS) kernel=5.14.0-synthetic seccomp=filter
[prochard] landlock: unavailable (unsupported-os)        ← darwin geliştirme
```

- `kernel=` alanı `/proc/sys/kernel/osrelease`'ten gelir.
- `seccomp=` alanı `/proc/self/status`'taki `Seccomp:` değeridir
  (`disabled`, `strict` ya da `filter`).
- İki alan da ENOSYS'in belirsizliğini çözmek için var. Bazı runtime
  profilleri listelemedikleri syscall'a EPERM yerine ENOSYS döner. Bu yüzden
  "çekirdekte yok" sonucu ancak seccomp kapalıysa ya da düğümün LSM listesi
  (§8, K3) landlock içermiyorsa kesindir.

### Satırın okunuşu

| Satır | Anlamı | Sonuç |
|---|---|---|
| `abi=1..3` | Dosya sistemi kuralları kullanılabilir. ABI 2 dizinler arası rename/link (`REFER`) kuralı, ABI 3 `TRUNCATE` ekler | B uygulanabilir: R1 kapanır |
| `abi=4..5` | Ek olarak TCP bind/connect kuralları (yalnız **port** bazında, host bazında değil) | B ile CH ve Redis portlarına connect adımdan kapatılabilir: R2 ve R8 kısmen kapanır |
| `abi≥6` | Ek olarak sinyal ve abstract unix socket kapsamı (`LANDLOCK_SCOPE_SIGNAL`) | B ile adım ebeveynine sinyal gönderemez: R6 kapanır |
| `disabled-at-boot` | Çekirdekte Landlock var ama `lsm=` listesinde yok | Açmak için MachineConfig ile kernel argümanı değişikliği ve düğüm reboot'u gerekir (platform ekibi). Beklemem düşük; B yerine A ile ilerlenir |
| `blocked-by-runtime` | seccomp profili syscall'ı reddediyor | Özel bir Localhost seccomp profili gerekir, bu da restricted-v2'den çıkmak demektir. B yerine A ile ilerlenir |
| `kernel-lacks-landlock` ile `seccomp=filter` | Belirsiz | K3 ile düğüm LSM listesine bakılır. Listede landlock varsa satır `blocked-by-runtime` gibi okunur, yoksa çekirdekte Landlock yoktur |
| `kernel-lacks-landlock` ile `seccomp=disabled` | Çekirdekte Landlock yok (5.13 öncesi ya da `CONFIG_SECURITY_LANDLOCK=n`) | B yok, A ile ilerlenir. `seccomp=disabled` ayrıca bir bulgudur: restricted-v2 altında `filter` beklenir |
| `unexpected-errno` | Eşlemede olmayan bir errno | Satırı bana ilet, eşleme genişletilir |

**ABI ve upstream çekirdek:** 1 → 5.13, 2 → 5.19, 3 → 6.2, 4 → 6.7,
5 → 6.10, 6 → 6.12. RHEL/RHCOS çekirdekleri backport alabilir. Bu yüzden
sürüm numarasından tahmin yapılmaz; satırdaki `abi=` esas alınır.

**Güvenlik payı:** yoklama kural seti kurmaz, süreci kısıtlamaz ve
idempotenttir. Linux testi, yoklamadan sonra dosya okuma ve yazmanın
değişmediğini doğrular. Tek uç durum şudur: varsayılan eylemi KILL ya da TRAP
olan **özel** bir Localhost seccomp profili bu çağrıda süreci öldürür.
RuntimeDefault profilleri (restricted-v2 dahil) errno döner, süreci
öldürmez. Özel profil kullanan bir kurulum bilmiyorum.

---

## 3. Seçenek 1: Landlock trampolini

**Nasıl çalışır.** Go'nun `os/exec`'i fork ile exec arasında kod koşturamaz.
Landlock ayrıca yalnız çağıran thread'i kısıtlar. Bu yüzden adım doğrudan
`/bin/sh` ile değil, aynı binary'nin gizli bir alt komutuyla başlatılır
(`coremetry` zaten `ch` gibi alt komutlar taşıyor). Tek imaj, tek binary
kuralı korunur. Adımlar:

1. `runtime.LockOSThread()`.
2. `prctl(PR_SET_NO_NEW_PRIVS, 1)`. Pod'da `allowPrivilegeEscalation: false`
   zaten NNP'yi açar, çağrı yine de idempotent olduğu için yapılır.
3. `landlock_create_ruleset(handled = ABI'nin desteklediği bütün fs hakları)`.
4. İzin listesindeki yollar için `landlock_add_rule` çağrıları.
5. `landlock_restrict_self`.
6. `syscall.Exec("/bin/sh", "-c", cmd)`.

Kısıtlama exec'ten ve bütün torunlara geçer, geri alınamaz. Coremetry'nin
kendisi kısıtlanmaz.

**İzin listesi taslağı (alpine imajı):**

| Yol | Hak |
|---|---|
| `/bin`, `/sbin`, `/usr`, `/lib` | okuma ve çalıştırma |
| `/etc/ssl`, `/etc/resolv.conf`, `/etc/hosts`, `/etc/passwd`, `/etc/group`, `/etc/localtime` | yalnız okuma (tek tek dosya; Landlock'ta "deny" kuralı yoktur, bir dizine izin vermek altındaki her şeye izin vermektir) |
| `/tmp/runbook-<execution>` | okuma ve yazma (adıma özel dizin; `/tmp`'nin geri kalanı kapalı) |
| `/dev/null`, `/dev/urandom` | cihaz dosyaları |
| `/proc` | okuma. `ps` gibi araçlar için gerekir. `/proc/1/environ` ve `/proc/1/root` zaten dumpable=0 ve Landlock'un ptrace kısıtıyla kapalıdır |
| `SSL_CERT_FILE` / `SSL_CERT_DIR` hedefleri | okuma (env'de verilmişse) |

### Neyi engeller, neyi engelleyemez

| Kaynak | Landlock ile |
|---|---|
| `/app/config.yaml`, binary, `/app/VERSION` | **Kapanır**: `/app` listede yok |
| Bağlı sır dosyaları (`extraVolumes`, `/var/run/secrets/coremetry/...`) | **Kapanır**: listede değilse. İstisna: operatör sırrı `/etc` ya da `/usr` altına bağlarsa, yukarıdaki dosya bazlı liste yine korur |
| SA token (`/var/run/secrets/kubernetes.io/serviceaccount`) | **Kapanır**. Bedeli: token'la k8s API'sine giden runbook'lar kırılır. İhtiyaç varsa açık bir yol izni verilir (§9, S3) |
| `/tmp` ve başka adımların artıkları | **Kapanır**: her adım yalnız kendi dizinini görür |
| `setsid` kalıntısı (R3) | Süreç yine yaşar ama kısıtlı kalır (domain miras alınır). Ömrünü sınırlamaz |
| Ağ (R2, R8) | ABI < 4: **engelleyemez**. ABI ≥ 4: yalnız TCP connect ve bind, port bazında. Ör. yalnız 80 ve 443'e izin verilir; CH (9000/8123) ve Redis (6379) kapanır, ama aynı porttaki başka host'lar ayırt edilemez. UDP ve DNS etkilenmez |
| Ebeveyne sinyal (R6) | ABI < 6: **engelleyemez**. ABI ≥ 6: `LANDLOCK_SCOPE_SIGNAL` ile kapanır |
| Kaynak tüketimi (R7) | **Engelleyemez** (cgroup işi) |
| env | Kapsam dışı: v0.10.966 allowlist'i zaten kapatıyor |
| `stat` ve dosya varlığı | Kısmen: `READ_DIR` listelemeyi kapatır, ama bilinen bir yolun meta verisi görülebilir. İçerik okunamaz |
| MCP stdio sunucuları (R5) | Aynı trampolin onlara da uygulanabilir (ayrı iş) |

**Desteklenmediğinde ne olur (karar S2):**

- `best-effort` (önerim): kısıt uygulanamazsa adım yine koşar. Çıktıya
  `[agent] landlock unavailable (<reason>) — step ran without filesystem isolation`
  notu eklenir; boot satırı zaten sebebi gösterir. Kısmi ABI durumunda
  desteklenen haklar uygulanır, ör. ABI 1'de dizinler arası rename her zaman
  reddedilir.
- `required`: kısıt uygulanamazsa bash adımı hiç koşmaz ve açık bir hatayla
  düşer (fail-closed).
- Anahtar bir env değişkeni olarak önerilir (chart değeri; platform
  sahibinin kararı, runtime'da admin toggle'ı değil). Ad önerisi:
  `COREMETRY_AGENT_LANDLOCK=off|best-effort|required`, ENV.md'ye eklenir.

**Maliyet: M, yaklaşık 2 sürüm.** İş kalemleri:

- `internal/prochard` içinde saf bir kural seti kurucusu (tablo testli);
- gizli alt komut (`main.go` birkaç satır büyür, `api.go` dokunulmaz);
- `executeBash` değişikliği;
- CI'da gerçek uygulama testi. GitHub `ubuntu-latest` (6.8 çekirdek)
  runner'ının ABI ≥ 4 vermesi beklenir; v0.10.970'ün linux testi bunu
  `-v` çıktısında `landlock abi=N` olarak loglar. Testte `cat /app/...` ve
  SA token yolu reddedilmelidir;
- ENV.md, DECISIONS.md ve chart değeri.

Chart ve imaj değişmez.

---

## 4. Seçenek 2: ayrı uid'li sidecar/executor konteyneri

**Nasıl çalışır.** Agent'ı koşan pod'a (all-mode pod'u ya da agent pod'u)
ikinci bir konteyner eklenir: `runbook-executor`. Aynı imajı kullanır,
yalnız farklı bir kipte başlar (ör. `coremetry executor --socket
/run/coremetry-exec/exec.sock`, ya da 6. mod `COREMETRY_MODE=executor`, ki bu
Hard-constraints tablosundaki mod listesini değiştirir).

**Adımın executor'a devri:**

- İki konteyner ortak bir `emptyDir` (`/run/coremetry-exec`) paylaşır. İçinde
  0600 izinli bir Unix socket vardır.
- Agent, istek başına bir bağlantı açar ve `{command, timeoutMs, env[]}`
  gönderir. Executor bugünkü `executeBash` mantığıyla koşar (süreç grubu,
  16 KiB tavan, WaitDelay) ve çıktıyla çıkış durumunu geri akıtır.
- Zaman aşımının otoritesi agent'tadır.
- Executor yoksa ya da cevap vermiyorsa adım açık bir hatayla düşer. Süreç
  içi koşuya geri **düşülmez** (fail-closed).

**Executor'un aldığı sırlar: hiçbiri.**

- Chart env'i yok (JWT secret, CH ve ES parolaları, initial password,
  Redis URL gelmez).
- `/app/config.yaml` bağlanmaz, extras yok, kendi `/tmp` emptyDir'i vardır.
- `COREMETRY_AGENT_ENV_PASSTHROUGH` değerleri chart'ta executor
  konteynerinin env listesine taşınır; allowlist chart'a geçer.
- SA token: `automountServiceAccountToken` pod düzeyinde bir ayardır. Go
  kodu SA token'ını kendiliğinden okumaz (`/var/run/secrets` referansı yok,
  in-cluster istemci yok). Bu yüzden pod düzeyinde `false` genelde
  güvenlidir. İstisna: bir tokenRef (Thanos, Argo CD)
  `file:/var/run/secrets/kubernetes.io/serviceaccount/token`'a işaret
  ediyorsa, o pod'da token kapatılamaz. k8s API isteyen runbook'lar için
  token yine pod'un SA'sına ait olur ve iki konteyner aynı RBAC'ı paylaşır.

**Ayrı uid konusu (OpenShift).** restricted-v2 `MustRunAsRange` kullanır:
`runAsUser` verilmezse bütün konteynerler aynı uid'i alır. Farklı uid için
namespace aralığından (`openshift.io/sa.scc.uid-range`) bir değer chart'a
elle verilmelidir; chart bunu render anında bilemez. Asıl sınır zaten uid
değil, başka üç şeydir:

- ayrı mount kümesi (sır dosyaları executor'a hiç bağlanmaz);
- ayrı PID namespace (`shareProcessNamespace: false` varsayılanı; executor
  Coremetry'yi göremez, sinyal de gönderemez);
- ayrı konteyner cgroup'u (bellek tavanı konteyner başınadır).

Aynı uid yalnız ortak socket dizininde önem taşır. Bu yüzden ayrı uid
**isteğe bağlı** kalır.

**İmaj:** değişmez, aynı imaj `/bin/sh`'ı taşıyor. Ayrı bir "araç imajı"
(curl, jq, kubectl) tek-imaj kuralını bozar; önermiyorum.

**Neyi kapatır:**

- R1: dosyalar bağlanmaz.
- R6: ayrı PID namespace.
- R7: ayrı konteyner limiti. pids limiti ise pod düzeyindedir.
- R9 executor tarafında.
- Çekirdekten bağımsızdır.

**Neyi kapatamaz:**

- R2 ve R8: ağ namespace'i ortaktır, NetworkPolicy konteyner ayırt etmez.
  localhost:8088 da erişilebilir.
- R3 executor içinde sürer.
- R10.

**Maliyet: L, 3 sürüm ya da daha fazla.** Yeni kip, socket protokolü ve
testleri; `deployment.yaml` ile `deployment-distributed.yaml` şablonlarına
konteyner ve hacimler; values; restricted-v2 uyumluluğu (non-root, drop
ALL, RuntimeDefault, readOnlyRootFilesystem); hazır olma kontrolü; belgeler.

---

## 5. Seçenek 3: dağıtık agent rolü runbook'lar için varsayılan olsun

**Bugün.** Chart'ın varsayılanı `deployment.mode: monolithic`. Pod'da
`COREMETRY_MODE` yok, yani `all` koşar ve agent süreç içindedir (R4).
Distributed modda `deployment.roles.agent.enabled` varsayılanı `false`.
Açılırsa ayrı bir pod gelir, ama R9'daki env ve SA token ile.

**Öneri.**

1. Monolitik modda da ayrı bir `<release>-coremetry-agent` Deployment
   varsayılan olarak render edilir ve ana pod agent koşmaz. Bunun için ya
   `COREMETRY_AGENT=off` gibi bir düğme ya da `COREMETRY_MODE`'a liste
   (`ingest,api,worker`) gerekir. İlki daha dar bir değişiklik.
2. Distributed modda `roles.agent.enabled` varsayılanı `true` olur.
3. **Agent pod'u kırpılır:**
   - JWT secret, OIDC client secret, ES kimlikleri ve initial password
     render edilmez. Agent CH'ye doğrudan bağlanır, bu yüzden CH parolası
     ve Redis URL'i kalır.
   - Uygulama adımında doğrulanacak: runbook bitiş bildiriminin
     (`SendRunbookComplete`) imzalı bir "Sustur" bağlantısı taşıyıp
     taşımadığı. Taşıyorsa agent'a JWT secret değil
     `COREMETRY_NOTIFY_ACTION_SECRET` verilir.
4. Agent kendi SA'sını `automountServiceAccountToken: false` ile kullanır.
   k8s API gereken runbook'lar için opt-in bir değer olur (S3).

**Neyi kapatır:**

- R4, ayrıca R6 ve R7'nin veri düzlemine etkisi: adım yalnız agent'ı
  düşürebilir, api, ingest ve worker ayakta kalır.
- R1'in en tehlikeli kısmı: extras zaten yoktu; SA token ve JWT secret
  pod'dan tamamen çıkar. Allowlist ve dumpable bir gün aşılsa bile admin
  oturumu üretilemez.
- Pod'a özel NetworkPolicy mümkün hâle gelir. Ancak agent CH ve Redis'e
  kendisi bağlandığı için bu portlar adıma da açık kalır; R2 ve R8 yalnız
  kısmen kapanır.

**Neyi kapatamaz:** R2, R8, R3, R10; agent pod'undaki `/app/config.yaml`
(chart'ın ConfigMap'inde sır yok, sırlar env'den gelir; ama chart dışı
kurulumlar config'e sır yazabilir); MCP stdio (R5; api rolünde koşar).

**Yükseltme etkisi:** mevcut monolitik kurulumlara ikinci bir pod eklenir.
Tek agent replikası Redis'siz de güvenlidir. Birden fazla replika Redis
kilidi ister; chart'ta `redis.enabled` varsayılan olarak `true`.

**Maliyet: S–M, 1–2 sürüm.** İş kalemleri: chart (iki şablon ve values),
`parseRunMode` düğmesi, agent rolünde env'lerin koşullu render'ı, SA
şablonu, `helm template` testleri, ENV.md ve DECISIONS.md.

---

## 6. Karşılaştırma

"K" kapanır, "kısmen" kısmen kapanır, "—" etkisiz demektir. Landlock
satırları prod ABI'sine bağlıdır.

| Risk | 1 Landlock | 2 Sidecar | 3 Agent pod'u (kırpılmış) | 3 + 1 |
|---|---|---|---|---|
| R1 dosyalar: config, sır mount'ları, SA token | K (ABI ≥ 1) | K | kısmen (SA token, JWT, extras çıkar; config kalır) | K |
| R2 iç ağ (CH, Redis) | kısmen (ABI ≥ 4, port bazlı) | — | kısmen (pod NetworkPolicy) | kısmen |
| R3 `setsid` kalıntısı | kısmen (kısıtlı kalır) | kısmen (executor içinde) | kısmen (agent pod'unda) | kısmen |
| R4 all-mode süreç içi | — | kısmen | K | K |
| R5 MCP stdio | K (ayrı iş) | — | — | K (ayrı iş) |
| R6 PID 1'e SIGTERM | K (ABI ≥ 6) | K | kısmen (yalnız agent düşer) | K (ABI ≥ 6) |
| R7 kaynak tüketimi | — | K (bellek) | kısmen (yalnız agent düşer) | kısmen |
| R8 parolasız Redis | kısmen (ABI ≥ 4) | — | — | kısmen |
| R9 agent pod'undaki sırlar | kısmen (dosyalar) | K | K | K |
| R10 çıktı viewer'a açık | — | — | — | — |
| Çekirdeğe bağımlılık | var | yok | yok | yalnız 1 için |
| Chart ya da imaj değişikliği | yok | chart (büyük) | chart (orta) | chart (orta) |
| Maliyet | M | L | S–M | M + S–M |

---

## 7. Öneri

1. **Şimdi A: seçenek 3, kırpılmış hâliyle.** Çekirdekten bağımsızdır ve en
   ucuz gerçek sınırdır. Veri düzlemini adımdan ayırır (R4, R6, R7) ve JWT
   secret ile SA token'ı adımın pod'undan tamamen çıkarır (R1, R9).
2. **Boot satırı `abi≥1` derse B: seçenek 1, `best-effort` kipte, agent
   pod'unda.** Kalan dosya erişimini kapatır, `setsid` kalıntılarını kısıtlı
   tutar. ABI ≥ 4 ise CH ve Redis portlarını adımdan kapatır (R2, R8); ABI ≥ 6
   ise sinyali kapatır. Aynı trampolin sonra MCP stdio'ya uygulanabilir (R5).
3. **Seçenek 2 rafta.** A, sidecar'ın sağladığı dosya yalıtımının çoğunu
   (sırları pod'dan çıkararak) çekirdekten bağımsız olarak zaten veriyor.
   Sidecar'ın maliyeti en yüksek, ağda ise ek kazancı yok. Yalnız iki koşul
   birlikte gerçekleşirse gündeme alınmalı: prod satırı Landlock'u
   kullanılamaz gösterir **ve** agent pod'unda kaldırılamayan bir sır dosyası
   kalır.
4. **Bu belgenin kapsamı dışında ama R8 için önemli:** paketli Redis'e
   `requirepass` eklemek (chart ve `COREMETRY_REDIS_URL`). Hangi seçenek
   seçilirse seçilsin R8'i en ucuz yoldan kapatır. Ayrı bir öneri olarak
   kuyruğa girebilir.

R10 (çıktının viewer'a açık olması) hiçbir seçenekle kapanmaz. Sırrı adıma
hiç ulaştırmamak tek savunmadır; A ve B tam olarak bunu yapar.

---

## 8. Prod'da yapılacak kontroller

Canlı sisteme ben bağlanmadım. Komutlar senin çalıştırman içindir; adlar
sentetik, kendi adlarınla değiştir.

**K1: boot satırı, her rol ve her pod için.** Farklı düğüm havuzlarının
çekirdekleri farklı olabilir.

```bash
oc get pods -n coremetry-synthetic -l 'app.kubernetes.io/name=coremetry,app.kubernetes.io/component in (coremetry,coremetry-api,coremetry-ingest,coremetry-worker,coremetry-agent)' -o name \
  | while read p; do echo "== $p"; oc logs -n coremetry-synthetic "$p" | grep -m1 '\[prochard\] landlock'; done
```

**K2: düğüm çekirdekleri.** Satırdaki `kernel=` değeriyle karşılaştır.

```bash
oc get nodes -o custom-columns=NAME:.metadata.name,KERNEL:.status.nodeInfo.kernelVersion,OS:.status.nodeInfo.osImage
```

**K3: düğümün etkin LSM listesi.** Yalnız satır `kernel-lacks-landlock` ile
`seccomp=filter` gösterirse ya da `disabled-at-boot` gösterirse gerekir;
cluster-admin yetkisi ister.

```bash
oc debug node/worker-synthetic-1 -- chroot /host cat /sys/kernel/security/lsm
# "landlock" listede → çekirdek hazır, ENOSYS/EPERM runtime'dan geliyor
```

**K4: seccomp ve SCC.** Beklenen: `restricted-v2` ve `RuntimeDefault`.
Boot satırında da `seccomp=filter` görünmeli. İkinci satır önce pod
düzeyindeki, sonra konteyner düzeyindeki profili gösterir. Chart konteyner
düzeyini, examples/openshift manifestleri pod düzeyini ayarlar; biri boş
çıkabilir.

```bash
P=$(oc get pods -n coremetry-synthetic -l 'app.kubernetes.io/name=coremetry,app.kubernetes.io/component in (coremetry,coremetry-api,coremetry-ingest,coremetry-worker,coremetry-agent)' -o name | head -1)
oc get -n coremetry-synthetic "$P" -o jsonpath='{.metadata.annotations.openshift\.io/scc}{"\n"}{.spec.securityContext.seccompProfile.type}{" / "}{.spec.containers[0].securityContext.seccompProfile.type}{"\n"}'
oc debug node/worker-synthetic-1 -- chroot /host sh -c 'crio config 2>/dev/null | grep -i seccomp'
# seccomp_profile boş → CRI-O'nun yerleşik varsayılan profili; landlock'a izin verip vermediğini K1 söyler
```

**K5: agent pod'unun bugün ne aldığı.** Chart'tan, cluster'a dokunmadan
çalışır. Bu komut v0.10.970 çalışma ağacında çalıştırıldı; exit 0, çıktıda
`COREMETRY_JWT_SECRET`, `COREMETRY_CH_PASSWORD`,
`COREMETRY_INITIAL_PASSWORD`, `COREMETRY_REDIS_URL`, ortak SA ve
`/app/config.yaml` göründü.

```bash
helm template cm-synthetic charts/coremetry -n coremetry-synthetic \
  --set deployment.mode=distributed --set deployment.roles.agent.enabled=true \
  --set secrets.existingSecret=cm-synthetic-secrets \
  | awk 'BEGIN{RS="\n---\n"} /name: cm-synthetic-coremetry-agent\n/' \
  | grep -E 'name: COREMETRY_(JWT_SECRET|CH_PASSWORD|INITIAL_PASSWORD|REDIS_URL)|serviceAccountName|mountPath'
```

**K6: runbook'lar neye dokunuyor.** A'daki SA token kararı ve B'deki
izin listesi bu sonuca bağlı.

```bash
curl -s -H "Authorization: Bearer $COREMETRY_SYNTHETIC_TOKEN" https://coremetry.synthetic.example/api/runbooks \
  | jq -r '.[] | .title as $t | .steps[] | select(.kind=="bash") | "\($t): \(.command)"' \
  | grep -nE 'serviceaccount|KUBERNETES_SERVICE|6379|9000|8123|/app/|/var/run/secrets'
```

---

## 9. Karar soruları

- **S1: sıra.** **A**: önce seçenek 3 (kırpılmış), sonra satır uygunsa
  Landlock (önerim). **L**: önce Landlock. **S**: sidecar.
- **S2: Landlock desteklenmediğinde.** **B**: `best-effort`, adım koşar ve
  çıktıya not düşülür (önerim). **Z**: `required`, bash adımı fail-closed
  düşer.
- **S3: SA token.** **K**: agent pod'unda kapalı; k8s isteyen runbook için
  opt-in değer (önerim; K6 boş dönerse kesin). **A**: açık kalsın.
- **S4: paketli Redis parolası (R8).** **E**: ayrı bir öneri olarak kuyruğa
  girsin. **H**: şimdilik kalsın.

---

## 10. Karar sonrası iş kalemleri (taslak)

- **A:**
  - `charts/coremetry/templates/deployment.yaml` (monolitikte agent
    Deployment'ı);
  - `deployment-distributed.yaml` (agent env kırpma, SA);
  - `serviceaccount.yaml` (agent SA'sı, `automountServiceAccountToken: false`);
  - `values.yaml`;
  - `main.go` `parseRunMode` düğmesi;
  - `docs/ENV.md`, `docs/DECISIONS.md`, chart README ve
    `docs/openshift-distributed.md`;
  - `/helm-chart-coremetry` ile.
- **B:**
  - `internal/prochard` kural seti kurucusu (saf ve tablo testli, linux
    uygulama testi CI'da);
  - `main.go` gizli alt komutu;
  - `internal/agent/executor.go` (`executeBash` trampolin üzerinden);
  - `COREMETRY_AGENT_LANDLOCK`, ENV.md.
- **Ölçüm (v0.10.970, bitti):**
  - `internal/prochard/landlock.go` (saf: neden eşlemesi, satır biçimi,
    seccomp ayrıştırma);
  - `landlock_linux.go` / `landlock_other.go` (yoklama);
  - `landlock_nr_*_linux.go` (syscall numarası: generic 444, mips o32 4444,
    mips n64 5444);
  - `main.go` boot satırı.
