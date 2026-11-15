# Redis kullanım denetimi ve tasarım önerisi

Durum: **onaylandı (operatör, 2026-11-15) — K1–K5 öneri gibi; R0 (v0.10.2226), R1 (v0.10.2227), R2 (v0.10.2228), R3 (v0.10.2229), R4 (v0.10.2230) gönderildi.** Okunan ağaç: `int` @ v0.10.2220. Operatör cümlesi: "Coremetry Redis'i daha etkin kullanabilir."
Etiketler: **[Ö]** kodda okundu (dosya:satır) · **[T]** koddan türetildi, ölçülmedi · **[V]** varsayım. Sayısal kazançların hepsi [T]/[V]; her dilim kendi ölçümünü taşır (§5).

## 0. Özet

Redis bugün zaten dört işte kullanılıyor: paylaşımlı L2 yanıt önbelleği (288 çağrı, SWR + süreç içi singleflight), ~35 lider kilidi, bir avuç `SetNX` tekilleştirme anahtarı ve iki pub/sub kanalı; ayrıca `acache` ZSET/HLL/pipeline ile picker verisini tutuyor. Yani eksik olan "Redis'e daha çok şey koymak" değil, **var olan katmanın üç yapısal açığı**:
1. Zaman kovalı anahtarlar (64 `cacheBucket` + 6 `cacheRawQuery` çağrısı) her 30 sn'de yeni anahtar üretir; SWR yalnız **aynı** anahtarda çalıştığı için canlı pencerelerde devreye giremez. 30 sn'de bir poll eden tek kullanıcılı görünümde isabet oranı ~0 [T].
2. Önek geçersizleştirme (`DelPrefix`, 42 çağrı) istek yolunda **tüm anahtar uzayını** `SCAN` eder; ack/resolve başına 3 tarama. Anahtar sayısı büyüdükçe (acache, yakında canlı dizin) yavaşlar.
3. Redis kesintisinde davranış kullanım başına farklı ve iki yerde tehlikeli: kilit boot'ta "herkes lider", çalışırken "kimse lider değil"; fleet özeti 45 sn bekler; `acache` istemcisi 500 ms sınırını taşımaz.

## 1. Envanter

### 1.1 Altyapı
- İstemci: `go-redis/v9` tek `redis.Client`; dial/read/write 500 ms, yeniden deneme yok (`internal/cache/redis.go:19-58`) [Ö]. Arayüz yalnız dize anahtar + bayt değer: `Get/Set/SetNX/Del/MGet/ScanPrefix/DelPrefix/Publish/Subscribe` (`cache/cache.go:21-81`); **pipeline, INCR, hash, zset yok**.
- İkinci, ayrı istemci: `acache.NewStoreFromURL` (`internal/acache/acache.go:255-271`), varsayılan go-redis zaman aşımları (dial 5 sn, 3 yeniden deneme) [Ö].
- Dağıtım (chart varsayılanı): tek replika, kalıcılık kapalı, **`maxmemory` ve tahliye politikası verilmemiş** (`charts/coremetry/templates/redis.yaml:10,38`), kap limiti 1 Gi (`values.yaml:550-576`) [Ö]. Bellek dolarsa tahliye değil OOMKill olur [T].
- Oturum: durumsuz JWT HS256 (`internal/auth/auth.go:205-220`); Redis'te oturum yok, taşınacak bir şey de yok [Ö].

### 1.2 Redis kullanımları

| # | Kullanım | Yer | Anahtar · TTL · biçim |
|---|---|---|---|
| 1 | L2 yanıt önbelleği (`serveCached`/`cachedJSON`), 288 çağrı, 70+ dosya | `internal/api/cache.go:216,252,350` | çıplak önek (`problems:v3:…`, `svc-bundle:…`); Redis TTL = 3 × yumuşak TTL; JSON zarf `{"w":ns,"b":gövde}`; boyut tavanı yok |
| 1a | TTL dağılımı | çağrı listesi | çoğunluk 15/30/60 sn; 5 dk en az 13; 1 sa 1 (`api.go:2486`); 5 sn 3 |
| 1b | Sıcak uçlar | `api.go:1836` services 30 sn · `api.go:8381,8466` problems 15 sn · `inbox.go:197,1123` 15 sn · `entities.go:95-710` 15–60 sn | services/inbox/problems anahtarları tüm girdileri taşıyor (FNV özetli kümeler) |
| 2 | L1 düşürme + config yeniden yükleme yayını | `api/cache.go:451,470,491,709` | kanal `coremetry:cache:invalidate`; yük: anahtar, `prefix:<p>`, `config:<svc>` |
| 3 | Isıtıcı tur hakkı | `api.go:1353,1362` | `coremetry:warm:deps`, `SetNX` 29 sn; ~8 varsayılan anahtar ısınır (`api.go:1489-1586`) |
| 4 | Fleet özeti görüntüsü | `api/cluster_fleet_summary.go:54-63,207` | `coremetry:cluster-fleet:v1:<digest>` JSON blob, 30 dk; `…:claim` `SetNX` 295 sn; ~20–60 KB [T/V] |
| 5 | Lider kilitleri, ~35 `LeaderHolder` | `cache/leader.go:157`, `main.go:1219-1420,2076-2209`, anomaly/notify/topology/… | karışık adlar: `coremetry:lock:*`, `entity-sync`, `problem-explainer:lock`, `topology-aggregator-leader`; TTL 30 sn–10 dk, yenileme TTL/3 (Lua) |
| 6 | İş kalemi kiraları | `sre/drain.go:278,377`, `sre/eval.go:791`, `agent/runner.go:79` | `TryAcquire`, kalem başına |
| 7 | P1 yükseliş tekilleştirme | `anomaly/problem_member.go:137-150` | `coremetry:problem:<id>:p1-escalated`, `SetNX` 24 sa; hata = gönder |
| 8 | Onay jetonu tek kullanım | `api/chat_approval.go:123-145` | `cosre:approval:<sha256>`, `SetNX`; hata/Redis yok = RET |
| 9 | CoSRE kural kilidi | `api/cosre_rules.go:53-54,190` | `coremetry:lock:cosre-rules`, 5 sn, 100 ms döngü |
| 10 | SRE kuyruk bekleme süresi + saatlik sayaçlar | `sre/enqueuer.go:33,208,221`, `sre/orchestrator.go:611-676` | `coremetry:sre:enq:svc:<svc>` `SetNX`; `coremetry:sre:llm:hour:<saat>` **Get+Set** (INCR yok, ±1 yarış kabul edilmiş) |
| 11 | Kalp atışları | `cluster/cluster.go:38,157,189`, `sre/status.go:18-20`, `sre/drain.go:204` | `coremetry:pod:<id>` 30 sn, `coremetry:sre:hb:<pod>` 60 sn; okuma `ScanPrefix` |
| 12 | Kullanıcı varlığı | `api/presence.go:47-54,126,154` | `coremetry:presence:<user>` 24 sa, 19 B; okuma `MGet` |
| 13 | SSE köprüsü | `sse/sse.go:58,156,277` | kanal `coremetry-events`; pub/sub, tampon 64, dolunca düşer, tekrar oynatma yok |
| 14 | Picker önbelleği (`acache`) | `acache/acache.go:30-38,477,667-690` | `coremetry:services[:rank]`, `coremetry:ops[:rank]:<svc>`, `coremetry:attr:<key>` ZSET, `…:card:<key>` HLL; 2 sn'de bir pipeline; kayan 24 sa; TopN 1000 |

`KEYS` yok. `SCAN`: `ScanPrefix` (kalp atışı okumaları, yalnız yönetim sayfaları) ve `DelPrefix` (istek yolu, §2 G2).

### 1.3 Süreç içinde yaşayan, replikalar arasında paylaşılmayan durum

| Durum | Yer | Paylaşılmamasının sonucu |
|---|---|---|
| L1 (FIFO 1024 girdi, ≤ 5 sn, bayt tavanı yok) + singleflight | `api/cache.go:49,182`, `api.go:153,446` | soğuk anahtarı R replika ayrı ayrı hesaplar |
| Bildirim tekilleştirme haritası `dedupSeen` | `notify/problem_dedup.go:273-292` | lider devri, çok liderli pencere ya da worker + sre aynı anda → çift bildirim |
| Hız sınırlayıcılar (kaynak kodu, MCP, CoSRE sinyal) | `api/chat_source_rate.go:57-70`, `api/mcp_gate.go:164`, `api/cosre_signal_rate.go:36` | fiilî sınır R × limit (kodda not edilmiş) |
| chstore memoları (`openSnap`, `spreadMemo`, `svcMetaVal`, `clusterMapVal`, `envMapVal`, `dbOwnVal`, `deploysCache`) | `chstore/store.go:383-385,432-483` | replika başına ayrı CH okuması; replikalar ≤ TTL kadar farklı görür |
| Önbellek sayaçları (`cacheStats`, 4096 anahtar) | `api/cache.go:762-800` | isabet oranı replika başına; filo toplamı yok |
| Fleet özeti döngüsü, briefing ve kullanıcı kapsamı memoları | `cluster_fleet_summary.go:87`, `cosre_briefing_settings.go:49`, `ai_user_scope_settings.go:89` | fleet zaten Redis'te; ötekiler küçük |

### 1.4 Adlandırma ve serileştirme
- Üç ad uzayı tek düz DB'de: `coremetry:*`, çıplak önbellek önekleri, çıplak kilit adları. Bugün çakışma yok [Ö: kilit adları ile 15 geçersizleştirme öneki karşılaştırıldı]; ancak `cacheInvalidatePrefix("problems")` gibi çıplak önek ile çıplak kilit adı aynı uzayda, gelecekte `ml:…` ya da `problems-…` adlı bir kilit bir ack ile silinir [T].
- Hepsi JSON; zarf okuması gövdeyi baştan sona ayrıştırır (`cache.go:161-168`), büyük gövdede isabet başına O(n) CPU [T].
- Kullanıcı girdisi anahtara ham giriyor (arama deseni, normalize `RawQuery`): kural ihlali değil (tüm girdiler anahtarda), ama anahtar uzunluğu ve sayısı sınırsız [T]. `len(set)` türü ihlal taramada **bulunmadı** [Ö].

## 2. Boşluklar ve riskler

| # | Bulgu | Kanıt | Etki |
|---|---|---|---|
| G1 | Kovalı anahtar SWR'ı devre dışı bırakır; 5 dk TTL'li iki anahtar da kovalı | `api.go:3154`, `api.go:1612-1615` (`clusters:`), `services_namespace.go:41` | canlı pencerede her 30 sn sert miss; `clusters` yorumundaki "son iyi değeri sun" niyeti gerçekleşmiyor, `ListClusters` 5 dk yerine 30 sn'de bir koşuyor (ısıtıcı örtüyor); her anahtarın 30 ölü kopyası 15 dk Redis'te [T] |
| G2 | `DelPrefix` istek yolunda tam anahtar uzayı taraması | `cache/redis.go:169-203`, `api.go:8659-8661,8726-8728,6841-6843` | ack/resolve = 3 × ⌈K/200⌉ gidiş-dönüş; K = 50K [V] için ~750 tur, 150–400 ms [T]; istek ctx'i iptal olursa yarım kalır |
| G3 | Soğuk anahtar ve SWR tazelemesi replika başına | `api/cache.go:271-277,291` | R replika aynı CH sorgusunu R kez koşar (kova devrinde eşzamanlı) [T] |
| G4 | Hata önbelleklenmez | `api/cache.go:291-294` | zaman aşımına düşen sorgu her poll'da, her replikada 20 sn bütçeyle yeniden koşar; CH zorlanırken yükü büyütür [T] |
| G5 | Kilit: boot'ta Redis yoksa Noop = herkes lider; çalışırken Redis giderse TTL sonunda **kimse lider değil** | `main.go:604-644`, `cache/leader.go:12-43,157-246` | aynı kesinti, pod'un ne zaman başladığına göre çift bildirim ya da **tüm arka plan işlerinin durması** (alarm değerlendirme dahil); karışık durumda yeniden başlayan pod tek başına lider |
| G6 | Fleet özeti Redis hatasında hakkı kaybetmiş sayılır | `cluster_fleet_summary.go:207-209,226-248` | çalışırken Redis kesintisinde her istek 45 sn bekler, sonra hesaplar, yazamaz, tekrarlar [T] |
| G7 | `acache` istemcisi sınırsız ve yeniden yoklamasız | `acache/acache.go:255-271`, `main.go:542` | kara delik Redis'te picker 5 sn+ takılır; boot'ta ping kaçarsa süreç ömrünce kapalı [T] |
| G8 | Redis kesintisinde L1 5 sn tavanı kalır, devre kesici yok | `api/cache.go:182,260` | her L1 miss'i 500 ms'ye kadar Redis bekler, sonra CH; polled anahtar 30 sn yerine 5 sn'de bir × R hesaplanır [T] |
| G9 | Bellek: `maxmemory` yok; değer tavanı yok; kullanıcı girdili anahtarlar 3 × TTL yaşar | §1.1, `api/cache.go:350` | OOMKill = tüm kilit, tekilleştirme, onay ve sayaç anahtarlarının aynı anda kaybı [T] |
| G10 | Onay jetonu tek kullanımı yalnız Redis'te | `api/chat_approval.go:137` | Redis yeniden başlarsa jeton TTL'i içinde yeniden oynatılabilir [T] |
| G11 | Sayaçlar Get+Set | `sre/orchestrator.go:611-676`, `sre/enqueuer.go:195-208` | pod'lar arası ±1 (kabul edilmiş); `Incr` ile sıfırlanır |
| G12 | `cacheStats.record` her önbellekli istekte global mutex; tavanda yeni anahtar başına 4096'lık doğrusal tarama | `api/cache.go:782-800` | sıcak yolda kilit çekişmesi [T] |
| G13 | SSE köprüsü kayıplı, tekrar oynatma yok | `cache/redis.go:226-262`, `sse/sse.go:156-183` | replika değişiminde ya da yavaş tüketicide olay kaybı; istemci poll ile toparlar |

Redis kapalıyken özet [Ö/T]: L2 = miss sayılır, CH'ye düşer (doğru, ama G8) · ısıtıcı hakkı, P1 tekilleştirme = açık geçer (doğru) · onay jetonu = RET (doğru) · SRE bekleme süresi = süreç içi haritaya düşer · varlık = "bilinmiyor" · pub/sub = susar, TTL toparlar · kilit = G5 · fleet = G6 · acache = G7.

## 3. Fırsatlar (kullanıcıya görünen kazanç × emek sırasıyla)

| # | Fırsat | Kaçınılan iş, sıklık | Beklenen etki | Ölçüm | Emek |
|---|---|---|---|---|---|
| O1 | **Canlı pencere anahtarı**: `to` şimdiye bir kova yakınsa anahtara mutlak zaman yerine `live:<süre>` girer; geçmiş pencere mutlak kalır. SWR kova devrinde de çalışır | istek yolundaki CH/VM okuması: canlı polled her anahtarda 30 sn'de 1 bloklayan miss → 0 | bu uçlarda p99, Redis gidiş-dönüşüne iner (< 10 ms); Redis anahtar sayısı düşer (anahtar başına 3–30 kopya → 1). CH sorgu sayısı ~aynı, istek yolundan çıkar | `/api/admin/cache-stats` MISS÷(HIT+STALE+MISS) rota başına; Server-Timing p50/p99 | S–M |
| O2 | **Nesil sayaçlı geçersizleştirme**: `INCR coremetry:gen:<önek>`; zarf nesli taşır; okuma `MGet(anahtar, gen)` tek turda | ack/resolve başına 3 tam `SCAN` → 3 `INCR` | ack/resolve p99'dan 150–400 ms [T]; maliyet anahtar sayısından bağımsız | Redis `INFO commandstats` scan çağrı/usec; ack p99 | M |
| O3 | **Dağıtık singleflight**: miss ve SWR tazelemesinde `SetNX <anahtar>:claim`; kaybeden L2'yi kısa süre yoklar, sonra kendi hesaplar (fleet özeti emsali) | soğuk anahtar başına (R−1) yinelenen CH sorgusu, pahalı uçlarda (topology 60 sn, rootcause, log pattern, dashboard paneli) | kova devrinde CH eşzamanlılığı ÷ R | `system.query_log`, `log_comment = route:<desen>` başına 5 dk sorgu sayısı | M |
| O4 | **Kısa negatif önbellek**: yalnız zaman aşımı hatası 5–10 sn "son hata" zarfı | başarısız sorgunun poll × replika tekrarı | CH zorlanırken yük büyütmez; hata yanıtı < 10 ms | rota başına zaman aşımı sayısı (query_log) | S |
| O5 | **Paylaşımlı tekilleştirme ve sayaçlar**: bildirim `dedupSeen` → `SetNX` (hata = gönder); hız sınırları ve saatlik sayaçlar → `Incr` | sorgu değil; çift bildirim ve R × limit | lider devrinde ve worker + sre yarışında tek bildirim; sınırlar filo geneli | bildirim günlüğünde aynı (problem, kanal, durum) çift sayısı | S |
| O6 | **Servis listesi filo görüntüsü**: varsayılan pencere için tek blob (binlerce satır, ~1 MB [T]), hak sahibi 30 sn'de bir yazar; replika çözülmüş kopyada sıralar/süzer/sayfalar | her ayrı (sayfa, sıralama, süzgeç) bileşimi için 30 sn'de 1 MV sorgusu → pencere başına 1 | Services sayfasında her etkileşim < 10 ms; CH sorgu sayısı bileşim sayısından bağımsız | `route:GET /api/services` sorgu sayısı; p99 | M–L |
| O7 | **İkili zarf + sıkıştırma + değer tavanı**: 8 bayt zaman başlığı + ham gövde; > 8 KB zstd (`klauspost/compress` zaten bağımlılık); > 1 MB L2'ye yazılmaz | Redis belleği ve ağ; isabette JSON ayrıştırma | bellek ÷ 3–5 [V]; büyük gövdede isabet CPU'su düşer | `INFO memory`, değer boyutu histogramı | S–M |
| O8 | **Demet okumalarında `MGet` ön okuma** (`dashboards_data.go:278-291`, inbox yelpazesi) | N `GET` → 1 `MGET` | panel başına ~0,2–0,5 ms [V]; küçük | Redis komut sayısı | S |
| O9 | **SSE için sınırlı Stream** (`XADD MAXLEN ~1000`) + `Last-Event-ID` | yeniden bağlanmada toplu yeniden çekme | olay kaybı kapanır | `Broker.Dropped`, yeniden bağlanma sonrası istek patlaması | M |

Bilinçli olarak önerilmeyenler: sıralama sütunu başına ZSET ya da servis başına HASH (binlerce satırda blob + süreç içi dizin daha basit ve istek başına 0 Redis turu; ZSET ~50K+ üye ya da artımlı güncelleme gerektiğinde değer); HLL (`acache` zaten kullanıyor, başka aday sayım yok); ısıtıcıya sıcak anahtar öğrenme (O1 sonrası SWR, polled anahtarı zaten sıcak tutar).

Entity canlı dizini (kardeş not, R1b) bu sıralamanın dışında, kendi programında; ona gereken iki şey burada R0/R1'de: pipeline ile toplu yazım ve tek degraded kuralı.

## 4. Redis'e konmayacaklar ve degraded kuralı

Konmaz: gerçeğin kaynağı (problem, olay, ayar, bildirim günlüğü, hüküm, sohbet geçmişi, değerlendirme kümeleri); geçmiş gerektiren her soru ("T anında neydi" → MV / Thanos); sınırsız kümeler (trace kimlikleri, span/pod başına anahtar, yüksek kardinaliteli attribute değerleri, log satırları); > 1 MB gövdeler; dayanıklılık isteyen kuyruklar; sır ve jeton (yalnız özet). MV-first değişmez: Redis, MV sonucunun kopyasını tutar, MV'nin yerini almaz.

Her kullanım şu sınıflardan birini **kodda beyan eder** ve testle sabitler:
- **A · Önbellek/ipucu:** hata = miss; kaynak CH/VM'den yeniden kurulur; istek Redis'i 500 ms'den fazla beklemez; art arda hatada devre kesici Redis'i atlar. Anahtar bulunamaması "yok" değil "bilinmiyor" demektir.
- **B · Hak/tekilleştirme:** hata = işi yap (çift iş idempotent olmalı). Tek istisna güvenlik hakları (onay jetonu): hata = RET.
- **C · Liderlik:** işçi başına açıkça "açık geçer + idempotent" ya da "kapalı kalır"; **boot'ta ve çalışırken aynı**.
Ortak: her anahtar TTL'li; `coremetry:<alan>:v<N>:` öneki; istek yolunda `SCAN` yok; flush/tahliye sonrası toparlanma ≤ 1 tick; yeni ayar/düğme yok.

## 5. Dilimler (her biri tek sürüm, bağımsız gönderilebilir)

| Dilim | İçerik | Risk | Test | Ölçüm (önce/sonra) |
|---|---|---|---|---|
| R0 ✅ v0.10.2226 | Degraded tutarlılığı: kilit kuralı (K1) boot = çalışma anı; fleet özeti Redis hatasında beklemeden hesaplar (G6); `acache` aynı istemciyi/sınırları ve yeniden yoklamayı kullanır (G7); L2 devre kesici + kesintide L1 tavanı = TTL (G8) | orta: lider davranışı değişir | `leader_test.go`, `lockdegraded_test.go`, `switchable_test.go` genişler; sahte `Lock`/`Cache` ile kesinti senaryoları | Redis durdurulmuş stack'te: lider sayısı/işçi, `/api/clusters/fleet-summary` süresi, `/api/services` p99 |
| R1 ✅ v0.10.2227 | Arayüz: `Pipeline` (toplu `Set`/`Del`) ve `Incr`; saatlik sayaçlar `Incr`'e (G11); chart'a `maxmemory` + politika (K3); taban ölçüm: katman sayaçları ve Redis tur süresi selfobs metriği; `cacheStats` kilidi (G12) | düşük | Noop + Redis uygulaması sözleşme testi | taban tablo: rota başına MISS oranı, `DBSIZE`, `used_memory`, `redis-cli --latency` |
| R2 ✅ v0.10.2228 | O1 canlı pencere anahtarı; önce 5 sıcak uç (services, clusters, namespaces, databases, messaging), sonra kalan `cacheBucket` çağrıları; G1'in 5 dk anahtarları düzelir | orta: bayatlık semantiği (K2) | `cache_key_test.go` pinleri; canlı/geçmiş ayrımı saf fonksiyon testi | rota başına MISS oranı ve p99; `ListClusters` sorgu sayısı/saat |
| R3 ✅ v0.10.2229 | O2 nesil sayaçlı geçersizleştirme; 15 önek kayıt altına; `DelPrefix` istek yolundan çıkar | orta: kayıt dışı önek bayat kalır | `cache_invalidate_test.go`: yazdığını oku, replikalar arası | ack/resolve p50/p99; `commandstats` scan çağrısı → 0 |
| R4 ✅ v0.10.2230 | O3 dağıtık singleflight + O4 negatif önbellek | orta: kaybedene ek bekleme; hata = hemen hesapla | iki sahte replika, tek hesap; hak sahibi ölürse zaman aşımı | pahalı 5 rotada 5 dk CH sorgu sayısı; zaman aşımı tekrarı |
| R5 | O5 bildirim tekilleştirme + hız sınırları Redis'te | düşük: hata = gönder | lider devri simülasyonu, tek gönderim | çift bildirim sayısı/hafta |
| R6 | O7 ikili zarf, sıkıştırma, 1 MB tavan (eski zarf `HIT-LEGACY` yoluyla okunur) | düşük–orta: sürüm geçişi | gidiş-dönüş + karışık sürüm testi | `used_memory`, değer boyutu p99, isabet CPU'su |
| R7 | O6 servis filo görüntüsü — **yalnız** R2 sonrası `/api/services` MISS payı hâlâ yüksekse | yüksek: SQL semantiğinin Go kopyası | CH yolu ile satır satır eşlik testi | `/api/services` sorgu sayısı ve p99 |
| R8 | O8 `MGet` ön okuma; O9 SSE Stream — **yalnız** `Broker.Dropped` > 0 ölçülürse | düşük / orta | — | Redis komut sayısı; düşen olay |

Canlı dizin (R1b) R1'den sonra herhangi bir noktada girebilir.

## 6. Operatör için açık kararlar

| # | Karar | Öneri |
|---|---|---|
| K1 | Redis yokken liderlik: bugün boot'ta herkes lider, çalışırken kimse | **Alarm yolu (evaluator, anomaly, notify) iki durumda da açık geçer**, çift iş R5 tekilleştirmesi + deterministik kimliklerle zararsız; ağır yazıcılar (retention, aggregator, entity-sync, rollout) iki durumda da **kapalı kalır**. Gözlem aracının alarmı, kendi önbelleği düştü diye susmamalı |
| K2 | Canlı pencerede bayatlık: O1 ile en kötü durum 3 × TTL (30 sn TTL'de 90 sn); tek kullanıcılı görünüm hep bir poll eski veri görür, karşılığında her poll < 10 ms | **Kabul**; `problems-count` bugün aynı sözleşmeyle çalışıyor (`api.go:8375-8381`). `?refresh=1` taze yolu korur |
| K3 | Bellek politikası: tek Redis'te önbellek ile kilit/hak anahtarları birlikte | **Tek örnek kalsın**; `maxmemory` ≈ limitin %75'i + `volatile-lru`, `evicted_keys` `/admin/stats`'ta görünsün. İkinci Redis yeni URL ayarı ister, kapsam dışı |
| K4 | Onay jetonu tek kullanımı Redis yeniden başlayınca kayboluyor (G10) | Jeton TTL'i kısa ise **kabul**; değilse harcanan jeton CH'deki mevcut onay kaydından da doğrulansın (yeni şema yok) |
| K5 | R7 (servis görüntüsü) ve R8 (SSE Stream) yapılacak mı | **Şimdi değil**; R2 ve R4 ölçümünden sonra, eşik aşılırsa |

### Onay (operatör, 2026-11-15)

Denetim ve beş karar öneri gibi onaylandı:
- **K1** — Redis yokken alarm yolu (evaluator, anomaly detector + recorder, exception notifier) açık geçer ve idempotent; ağır yazıcılar kapalı kalır; kural boot'ta ve çalışırken aynı. Kodda `cache.LeaderPolicy` (`FailOpen` / varsayılan `FailClosed`), `NewFailOpenLeaderHolder`; boot'ta Redis tanımlı ama erişilemezse `UnavailableLock` (always-leader Noop değil). Redis hiç tanımlı değilse (tek pod) Noop always-leader değişmedi.
- **K2** — canlı pencerede bayatlık en çok 3 × TTL kabul.
- **K3** — tek Redis örneği; `maxmemory` ≈ chart limitinin %75'i + `volatile-lru`; `evicted_keys` görünür.
- **K4** — kodda jeton TTL'i = onay soru süresi (`ToolApprovalTimeout*`: varsayılan 300 sn, en çok 3600 sn) ve jeton exchange + adım + araç + argüman özeti + kullanıcıya bağlı → **kısa sayıldı, kabul**; CH onay kaydı kontrolü eklenmedi.
- **K5** — R7/R8 şimdi değil.
