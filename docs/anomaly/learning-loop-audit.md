# Anomali → Incident → Problem öğrenme döngüsü — Faz 0 audit

Tarih 2026-11-05 · HEAD v0.10.1810 · Kod değişikliği yok. Yol kısaltmaları: `chstore/` = `internal/chstore/`, `ml/` = `internal/ml/`, `FE/` = `frontend/src/`, `CH-skill` = `.claude/skills/clickhouse-schema/SKILL.md`, `A-skill` = `.claude/skills/aiops/SKILL.md`. Örnek servis adları nötrdür (orders-api, payments-mobile, branch-teller). "doğrulanmadı" = kodda/prod'da teyit edilemedi; liste §9'da.

## 1. Özet ve önerilen karar
**Öneri.** Yeni bir tespit hattı kurma; gemideki `ml_events` hattının üstüne dört parça ekle:

1. **Baseline:** tek yeni tablo `ml_baseline_obs` (seri × haftanın saati × hafta → q10/q50/q90/q99). Günlük iş yalnız DÜNÜN verisini okur. Bant = son 4–8 haftanın medyanı. Tespit, dedektörün zaten çektiği son pencereyi bu tabloyla kıyaslar; ek kaynak sorgusu yok.
2. **Gruplama:** mevcut `incidents` tablosunu KULLANMA. Yeni `anomaly_groups` (+ üyelik + olay günlüğü). Ekranda adı "Anomali grubu" olsun; "Incident" bugünkü Problem-sonrası kavram olarak kalsın (§5.1, açık soru S2).
3. **Etiket ve öğrenme:** terfi / gerekçeli red / birleştir / böl / kaçırılan → tek ek-yazılır günlük. Öğrenilen her ayar `ml_learned` tablosunda revizyonlu; etkin revizyon `system_settings` işaretçisi; geri alma = işaretçiyi eski revizyona çekmek.
4. **Anlatım:** tek Go üreticisi (`ExplainCard`). Liste satırı, detay, CoSRE ve bildirim aynı struct'tan beslenir. Bugünkü üç ayrı FE metin üreticisi kalkar.

**Görev metninden bilinçli sapma (karar ister, S1).** Görev "baseline dar rollup'ın tDigest'lerinden" diyor. Oysa 2026-10-31 operatör kararı: Collector'da tail sampling var, örneklenmiş span'den oran/gecikme hesaplanmaz, trace dedektörlerinde rollup KULLANILMAZ (`ml/trace_red.go:5-7`; memory `project-log-ml-2026-10-31.md` L25, L36-39). Önerim: baseline kaynağı dedektörün kendi serisi (`ml_series`; ES ve VM'den beslenir). Dar rollup tDigest'i yalnız "süre dağılımı" kanıtı için ikinci kaynak. Tablo tasarımı iki kaynağı da taşır; karar yalnız besleyiciyi değiştirir.

**En büyük üç risk.**

| # | Risk | Karşı önlem |
|---|---|---|
| 1 | Hiçbir ML dilimi prod'da çalışmadı; ana anahtar KAPALI (`chstore/ml_settings.go:221-227`). Etiket yoksa öğrenme yok. | Faz 1–2 yalnız gölge + görünürlük. Öğrenme (Faz 5) en az 50 etiket ve replay kapısına bağlı. |
| 2 | ML bugün tik başına en çok 500 servis izliyor (`docs/plans/ml-next.md:13`); hedef binlerce servis. | Faz 1 ilk dilimi ölçüm: 5.000 serviste tik süresi, VM/ES bütçesi. Ölçülmeden tavan kalkmaz. |
| 3 | Dört terfi yolu yan yana: genel (`internal/evaluator/evaluator.go:1350-1495`), özne (`internal/evaluator/anomaly_subject_sweep.go:86-160`), ML kopyası (`ml/promote.go`), topoloji kümesi (`internal/anomaly/clustering.go:382-415`). Beşinci eklenirse aynı Problem'i iki döngü ezer (Davis dersi, memory `project-davis-parity-2026-10-29.md` L27). | Grup terfisi TEK yazıcı (sre lideri), ayrı rule_id ailesi; ML otomatik terfi anahtarları kapalı kalır. |

**Önermediklerim.**

| Önerilmeyen | Neden |
|---|---|
| `incidents` tablosuna "aday" satırı yazmak | ≥10 okuyucu ve bildirim yaşam döngüsü var; aday satır hepsine sızar. Aynı gerekçeyle `ml_events` ayrı tablo yapılmıştı (memory log-ml L14). |
| 1008 slot (10 dk) genel baseline | 6 kat satır, slot başına 16 örnek; yüzdelik zayıf. Saatlik slot + komşu slot arası doğrusal geçiş yeter (§4.3). |
| VM recording rule'larını ürünün yönetmesi | Üründe recording rule / vmalert yok (`internal/promql/lex.go:61`). Yeni bileşen yerine günlük iş `ml_series` okur. İsteğe bağlı kural dosyası S5. |
| Yeni grafik kütüphanesi | Isı haritası zaten ham canvas (`FE/components/LatencyHeatmap.tsx:137`); eksik olan yalnız çizgi katmanı. Bütçe 347 KB gz (`frontend/scripts/bundle-budget.mjs:25`). |
| Eski native dedektörleri açmak, `service_silent`, Problems başlığına MTTR/isabet şeridi | Kapalı kararlar (`chstore/native_detectors.go:27-54`; memory `feedback-service-silent-off.md`, `feedback-problems-no-lifecycle-strip.md`). Ölçüm ML karnesine eklenir. |
| LLM'in tespit, eşik veya gruplamaya girmesi | Kısıt. LLM yalnız çok sinyalli grupta "Değerlendirme" paragrafı (§7.4). |

## 2. Mevcut durum tablosu
Karar sütunu: **K** = aynen kullanılır, **G** = genişletilir, **D** = yenisiyle değiştirilir, **X** = dokunulmaz.

| Parça | Ne yapıyor | Dosya:satır | Hedef akıştaki yeri | Eksik / çakışma | Karar |
|---|---|---|---|---|---|
| `ml_events` | ML olayı; RMT(version), ORDER BY id, TTL 30 g; `stage` varsayılan shadow | `chstore/store.go:3122-3143` | Adım 1 anomali kaydı | İmza ve grup kolonu yok | G (kolon yok; bağ üyelik tablosunda) |
| `ml_series` | Dedektör serisi, servis × part × 5 dk; TTL 28 g | `chstore/store.go:3149` | Baseline kaynağı, grafik verisi | 8 hafta için kısa; trace/metrik backfill yok (`ml/log_red.go:322` yalnız log) | G (günlük okuma yeter; TTL değişmez) |
| `ml_event_snapshots` | Olay anı seri + bant kaydı; TTL 120 g | `chstore/store.go:3167`, `ml/snapshot.go` | "Aynı sorgu" grafiği, replay | — | K |
| ML tik döngüsü | 300 sn tik, lider kilidi `coremetry:lock:ml`, sre/worker rolü | `ml/recorder.go:18,67-72,96`, `main_ml.go:21-30`, `ml/placement.go:18` | Tespit çalıştırıcı | 500 servis tavanı | G |
| ML tabanı (bugün) | Son 21 gün, aynı takvim sınıfı, yerel saat ±1 havuzu, medyan+MAD, z≥4, dwell 2 | `ml/log_red.go:103-127,164-174,337-344`; `chstore/ml_settings.go:166-179` | Adım 1 baseline | Haftanın günü yok (Pzt–Cuma tek havuz); slot yok; servise özel eşik yok; saklanan taban yok | D (`ml_baseline_obs`) |
| ML dedektörleri (19) | log 8, trace 6, metrik 5; hepsi kapalı | `FE/pages/settings/MLSubsection.tsx:15-39`, `chstore/ml_settings.go:221-227` | Sinyal üreticileri | Profil kavramı yok | G (karar çekirdeği profil alır) |
| Kalite çerçevesi | Etki eşiği, `feedbackZ` (≥3 net "değil" → +0,5 z, tavan 4), rejim kesmesi (>6 sa), teyit skoru, taban hijyeni | `ml/quality.go:91,104,275,295,306-315` | Büyüklük+süreklilik+etki; öğrenme | `feedbackZ` kaba, sürümsüz, geri alınamaz | G; `feedbackZ` → D (Beta, §6.1) |
| Epizot modeli | 3 tik görülmezse kapanır, 30 dk içinde aynı epizot | `ml/quality.go:112-123` | Grup yaşam döngüsü girdisi | — | K |
| ML backtest + what-if | Canlı kararı `ml_series` üstünde yeniden koşar; `POST /api/ml/backtest` | `ml/backtest.go`, `internal/api/ml.go:130` | Güvenlik: replay | Öğrenilen ayar + profil örneklemi yok | G |
| ML karnesi | Dedektör/sinyal isabeti, terfi kapısı | `internal/api/ml.go:89`, `chstore/ml_events.go:256` | Ölçüm (§8) | İmza/profil kırılımı, kaçırılan, terfi süresi yok | G |
| ML karar etiketi | `PUT /api/ml/{id}/verdict`; `anomaly_verdicts`'e `ml:` önekiyle | `internal/api/ml.go:188`, `chstore/ml_labels.go:47-79`, `chstore/store.go:2507-2520` | Etiket | Yalnız gerçek/değil + not; gerekçe kodu, grup, terfi yok; sayfada not alanı yok (`FE/features/anomalies/MLReplaySheet.tsx:103`) | K (olay düzeyi) + yeni günlük (grup düzeyi) |
| ML terfi kopyası | Olayı `anomaly_events`'e `ml:<det>` türüyle kopyalar; kapalı | `ml/promote.go:3-19,48-52`; çelişen yorum `chstore/ml_settings.go:347-348` | Otomatik terfi | Kullanıcı terfisiyle çift yol olur; gerçek durumu doğrulanmadı | X (kapalı kalır) |
| `anomaly_events` + `promotion` | Eski hat olayı; `promotion` ''/shadow/live | `chstore/store.go:2618-2632,4222-4250`, `chstore/anomaly_event.go:147-148` | Özne olayları ikinci üye türü | — | K |
| Özne anomalileri + FAZ 3 kapısı | endpoint/db/queue; kip off/shadow/live, varsayılan off; ≥600 sn, ≥2× | `chstore/anomaly_sensitivity_subjects.go:21-97`, `internal/evaluator/anomaly_subject_group.go:30-117` | Otomatik, servis-içi gruplama | Yeni grupla aynı işi servis içinde yapıyor | X; özne olayı gruba üye olabilir (Faz 3 kararı) |
| Genel anomali terfisi | oran ≥5, ≥300 sn, sayım ≥10 → `anomaly-auto:<fp>` | `internal/evaluator/evaluator.go:1295,1548`, `chstore/anomaly_promotion.go:90-114` | — | Native dedektörler kapalıyken girdisi yok | X |
| Topoloji kümeleme | `anomaly-cluster:` Problem; ≥3 üye | `internal/anomaly/clustering.go:382-415`; spec "onay bekliyor" `docs/plans/spec-anomaly-clustering.md:14-22` | Adım 2'nin Problem düzeyi karşılığı | Yeni grupla kavram çakışması; etkin mi doğrulanmadı | S3 |
| `incidents` (+`incident_problems`, `incident_events`) | Problem'leri gruplar: aynı servis / 1-hop, 30 dk; elle "Declare incident"; ack, postmortem, bildirim | `chstore/store.go:2819-2856`, `chstore/incident.go:334-369,373,457`, `internal/api/api_incidents.go:65` | Terfi SONRASI operatör katmanı | Yalnız Problem bağlanır; birleştir/böl yok; RMT + `PARTITION BY toDate(started_at)` | X (ad çakışması §5.1) |
| `problems` | RMT ORDER BY id, TTL yok; durum open/resolved; öncelik okuma anında `computePriority` | `chstore/store.go:2746-2764`, `chstore/problem.go:212,449` | Terfi hedefi | Elle Problem açma rotası YOK | K |
| Problem imza kararı (1015) | "gerçek / değil" imzaya; `saved_views(page='problem-verdict')`; yalnız görünüm | `docs/DECISIONS.md:5194-5211` | Problem düzeyi etiket | "Eşiğe geri besleme ayrı karar" notu açık (`:5210-5211`) — bu audit o karardır | K |
| `root_cause_hypotheses` | Tek yazıcı `RootCauseSynthesizer`; çapa anomaly/problem | `chstore/store.go:2672-2687`, `internal/anomaly/rootcause_worker.go:165-188` | "Kök neden X" metni | Grup çapası yok | K (terfi sonrası Problem çapasıyla) |
| `incident_cases` + benzer vaka | CoSRE vaka hafızası; `SimilarIncidentCases` | `chstore/store.go:3221`, `chstore/incident_cases.go:464`, `internal/mcptools/problem_tools.go:316` | Benzer Problem (Faz 6) | Şeması okunmadı (doğrulanmadı) | Faz 6'da K ya da parmak izi grup satırında |
| `cosre_signals` | CoSRE cevap kalitesi örtük sinyali | `chstore/store.go:3026-3036` | — | Konu dışı | X |
| Susturma | `anomaly_silences`: tek parmak izi, `until_at`, `reason` | `chstore/store.go:2486-2498`, `chstore/anomaly_silence.go:30-144` | Süreli bastırma | ML olayını kapsamaz; yinelenen pencere yok | G |
| Bakım penceresi | Servis kalıbı, süreli; yalnız BİLDİRİMİ keser | `chstore/store.go:2463-2480`, `internal/notify/notify.go:280` | Bakım gerekçesi | ML adayına etkisi yok | G (S6) |
| Takvim | holiday / half-day / month-end-like; TR sabit tatilleri; ayın 15'i, son iş günü, tatil öncesi; yarım gün 13:00 | `chstore/anomaly_calendar.go:31-33,55-86`, `internal/anomaly/calendar.go:81-140`, `ml/log_red.go:182-201` | Özel takvim | Arife ayrı tür değil; "maaş günü" adı yok (Mid15 + LastBusinessDay karşılıyor) | K |
| Batch kuralı | `-batch` adında `request_rate` atlanır | `chstore/anomaly_sensitivity_batch.go:44,67-107`, `internal/anomaly/batch_load.go:41` | Profil "batch" | ML hattında uygulanıyor mu doğrulanmadı | G |
| Dar/geniş rollup | Dar: servis × kind × status, tDigest, 1 sa katmanı 13 ay. Geniş: 20 kovalı histogram. Elle uygulanır | `migrations/0001_rollup_narrow.sql:4,26,135-158`, `migrations/0002_rollup_wide.sql:6-8`, `chstore/rollup_read.go:14-18` | Görevin önerdiği kaynak | Tail sampling; prod'da kurulu mu doğrulanmadı | S1 |
| `make eval-anomaly` | Eski hat için etiketli vaka kümesi; ≥50 vaka kapısı | `Makefile:76-77`, `internal/anomaly/eval.go:36-56,283` | Replay biçimi örneği | ML hattını kapsamaz | X (biçim örnek alınır) |
| Anlatım üreticileri | FE: `anomalyNarrative.ts:19,28`, `detailSummary.ts:70`, `mlReplay.ts:155-260`. Go: `StandardizeAnomalyEvent` | `FE/lib/…`, `chstore/problem_standard.go:989-1038` | Yazılı kart | Üç FE + iki Go üretici; liste satırında özet alanı yok | D (tek Go üreticisi) |
| ML detay sayfası | Seri + beklenen bant + anomali bölgesi + deploy işareti (kayıttan) | `FE/features/anomalies/MLReplaySheet.tsx:124-155` | Detay görünümü tabanı | Kanıt bölümleri, grup/komşu/benzer bağlamı, terfi/red/birleştir/böl yok | G |
| Sinyal rengi/ikonu | Yok; yalnız metin rozet | `FE/components/coremetry/anomalies/MLTab.tsx:42` | §7.2 | — | Yeni |
| Ayar geçmişi | `system_settings` son satırı tutar; geçmiş yok | `chstore/settings.go:24-55`, `chstore/store.go:2372-2378` | Sürümlü ayar | Geri alma için yetersiz | `ml_learned` |

**Naif kurulursa çiftlenecekler:** (a) ikinci baseline hesabı (bugünkü `baselineDates` + yeni tablo yan yana) — yenisi gelince eskisi silinir, shim yok; (b) ikinci "incident"; (c) beşinci terfi yolu; (d) dördüncü metin üreticisi; (e) `feedbackZ` ile Beta ayarı aynı anda.

## 3. Baseline kaynağı — seçenekler
| | A: dar rollup tDigest (görev metni) | B: `ml_series` (öneri) | C: VM'yi günlük doğrudan sorgula |
|---|---|---|---|
| Kapsam | Yalnız trace | Log + trace + metrik, tek yol | Trace + metrik |
| Tail sampling | Sayım ve gecikme yanlı | Yok (VM RED + ES sayımı) | Yok |
| Dedektörle aynı veri | Hayır (dedektör VM okur) | Evet | Evet |
| Günlük okuma (5.000 servis) | ~720 bin tDigest durumu (1 sa katmanı, 30 bin anahtar × 24) | ~14 M dar satır | ~20 aralık sorgusu / aile |
| Ek altyapı | Elle migration şart | Yok | Yok |
| Zayıf yanı | p90 düzeyi yok (0,5/0,95/0,99); farklı düzeyle `*Merge` okunur mu doğrulanmadı | Yalnız izlenen seriler; trace/metrik için bir kerelik VM backfill gerekir | `le` etiketi serilerin %74'ü; servissiz yüzdelik pahalı (memory `project-victoriametrics-parked.md` L46-50) |

Öneri **B**. A, operatör "örnekleme yanlılığı kabul" derse yalnız gecikme yüzdelikleri için ikinci besleyici olur (S1).

## 4. Baseline ve profil tasarımı
### 4.1 Tablo: `ml_baseline_obs` (yeni, tek)
| Alan | Değer |
|---|---|
| Motor | `ReplacingMergeTree(version)`; Go işçisi yazar (CH-skill:35-48) |
| ORDER BY | `(signal, service, part, slot, week_start)` — shard anahtarı `service` içinde (CH-skill:153-157) |
| PARTITION BY | `week_start` (~10 parça). Kolon ORDER BY'da ve yeniden yazımda değişmez (CH-skill:96-99) |
| Kolonlar | `signal` LC, `service` LC, `part` String, `slot` UInt8 (0–167, yerel saat), `week_start` Date, `class` LC (normal/holiday/half_day/month_end), `q10,q50,q90,q99,vmax` Float32, `calls` UInt32, `buckets` UInt8, `cfg_hash` String, `version` UInt64 |
| TTL | `class='normal'` 70 gün; diğer sınıflar 400 gün (tatil yılda birkaç kez gelir) |
| Dağıtık | Yeni tablo; `_local` + Distributed çifti, niteliksiz ad (CH-skill:427-439). İki-boot yalnız sonradan kolon eklenirse |
| Okuma | `FINAL` + `LIMIT` + `max_execution_time`; saat başı bir kez, bellekte tutulur |

Satır = "bu seri, bu haftanın bu saatinde ne gördü". Bant okuma anında hesaplanır: aynı slotun son 4–8 `normal` haftasının `q10`/`q50`/`q90` medyanı; "p99 çizgisi" = haftalık `q99` medyanı. Tek kötü hafta bandı bozmaz. Etiketlenmiş gerçek anomali kovaları yazımda dışlanır (bugünkü hijyen, `ml/quality.go:51`). `as_of` tarihine göre süzmek geçmiş bandı aynen verir; replay bunu kullanır.

Özel gün: o günün gözlemi `class` ile yazılır, normal bantlara girmez. Tatilde bant = aynı saatin `holiday` gözlemleri (yoksa Pazar slotu = "Pazar benzeri"). Yarım gün: 13:00 öncesi normal, sonrası `half_day` (bugünkü kural, `ml/log_red.go:182`). Ay sonu / ayın 15'i / son iş günü: `month_end` (`internal/anomaly/calendar.go:81-140`).

### 4.2 Günlük iş (sre rolü, lider kilidi, Settings anahtarı `ml.baseline.enabled`)
1. 03:00 yerel: dünün `ml_series` satırlarını servis karmasına göre 16 dilimde oku (`WHERE bucket` dünle sınırlı, `LIMIT`, `max_execution_time 60`).
2. Her (seri, saat) için 12 kovadan q10/q50/q90/q99/max ve çağrı toplamını hesapla; `ml_baseline_obs`'a yaz (aynı anahtar = idempotent).
3. Haftada bir: profil ve rejim değerlendirmesi (§4.4, §4.6).
4. İş durumu `ml_tick_status` blobuna (mevcut desen, memory log-ml L44).

Bir kerelik backfill: log için mevcut saatlik backfill 21 → 56 güne uzar (1.344 ES agregasyonu, hız sınırlı; `ml/backfill.go`). Trace/metrik için VM `query_range`, gün gün, mevcut VM bütçesi altında (`ml/vm_budget.go`); aile başına 56 gün × ~4 sorgu ≈ 224 sorgu.

### 4.3 Maliyet tahmini
Varsayımlar (ölçüm değil): 5.000 servis (memory tahmini ~1.400, `project-behavior-engine.md` L36); servis başına 10 seri (log 2, trace 4, bağımlılık/metrik 4); endpoint açılırsa servis başına 20 rota × 3 seri; satır ~15 bayt sıkışmış; 10 hafta saklama.

| Kalem | Servis düzeyi (50 bin seri) | + endpoint (350 bin seri) | 10 dk slot olsaydı |
|---|---|---|---|
| Satır / hafta (× 168) | 8,4 M | 58,8 M | × 6 |
| Toplam satır (10 hafta) | 84 M | 588 M | 504 M / 3,5 Mr |
| Disk | ~1,3 GB | ~8,8 GB | ~7,6 GB / ~53 GB |
| Günlük okuma (`ml_series`) | 14,4 M satır | 100,8 M satır | aynı |
| Günlük yazma | 1,2 M satır | 8,4 M satır | × 6 |
| Tespit okuması | saatte 1 sorgu, ≤ 400 bin satır | ≤ 2,8 M satır | 10 dk'da 1 |
| Ek ES sorgusu (sürekli) | 0 | 0 | 0 |
| Ek VM sorgusu (sürekli) | 0 | 0 | 0 |
| VM recording rule olsaydı (S5) | ~25 bin seri | ~325 bin seri (bugün 12 M seri; +%2,7) | — |

Kıyas: 1 Mr span ≈ 47 GB (lokal ölçüm, memory `project-scale-test-result.md` L29-31). Endpoint düzeyi için öneri: 3 saatlik blok (56 slot/hafta) → ~196 M satır, ~3 GB. Dakikalık hassasiyet slot küçülterek değil, 5 dk'lık canlı kovayı komşu iki saatlik slot arasında doğrusal geçişli bantla kıyaslayarak sağlanır.

Faz 1 kabul ölçümü: günlük iş < 10 dk, tepe bellek < 2 GB. Lokalde davranış motoru 2,8 GiB tavanında code-241 vermişti (memory behavior-engine L48-52).

### 4.4 Servis profilleri (haftalık, deterministik)
Girdi: son 4 haftanın saatlik çağrı serisi (`ml_baseline_obs.calls`).

| Profil | Kural (sırayla; ilk tutan) | Tespit yöntemi |
|---|---|---|
| batch | Ad `batchServicePatterns` ile eşleşir (`chstore/anomaly_sensitivity_batch.go:44`) | Yük değerlendirilmez; hata oranı ve log değerlendirilir |
| az çağrılan | Günlük medyan çağrı < 2.000 veya mesai saatlerinde boş kova payı > %50 | 1–6 sa pencere, en az 8 çağrı, binom testi (§4.5) |
| mesai/şube | (mesai dışı medyan / mesai içi medyan) < 0,15 ve (hafta sonu / hafta içi) < 0,25 | Mesai içinde düşüş de anomali: çağrı < 0,5 × q10, 3 kova. Mesai dışı sessizlik normal (bant tabanı ~0 → düşüş kapısı kapalı) |
| 7/24 | Diğerleri | Saatlik bant + 5 dk kova, dwell 2; günlük + haftalık döngü slotta |

Sınıf değişimi iki ardışık haftalık değerlendirme ister. Settings'te servis başına elle sabitleme. Profil `ml_learned(kind='profile')` satırıdır; sürümlü, geri alınabilir.

### 4.5 Anomali şartı: büyüklük + süreklilik + etki (üçü birlikte)
| Şart | Tanım | Mevcut parça |
|---|---|---|
| Büyüklük | `z = (değer − q50) / max(1,4826·MAD_hafta, (q90 − q10)/2,56, 0,1·q50, taban)`; `z ≥ z_eff`. `z_eff = z_temel + Δz(imza)` (§6.1) | `judgeMM`, `ml/log_red.go:103-127` |
| Süreklilik | 7/24: 2 kova; mesai: 3 kova; az çağrılan: pencere testi | dwell, aynı yer |
| Etki | Etkilenen çağrı ≥ max(5, 0,2 × beklenen) | `impactOK`, `ml/quality.go:91`; `MinAffectedRequests`, `ml/trace_red.go:320-337` |

Az çağrılan servis testi: pencerede `n` çağrı, bunların `k` tanesi baseline süre eşiği `T90`'ı (en yakın histogram kenarı) aşıyor. Baseline'da aşma olasılığı 0,10. `P(X ≥ k | n, 0,10) < 0,001` ve `n ≥ 8` ise anomali. Örnek: 10 çağrının 6'sı yavaşsa olasılık ~0,00015 → yakalanır; 10'da 3 ise ~0,07 → sessiz. Bugünkü `op_latency` sabit 1 sn eşiği kullanır (`ml/trace_red.go:320`); öğrenilmiş `T90` onun yerini alır.

### 4.6 Rollout sonrası hızlı yeniden öğrenme
Koşul (üçü): (1) seride kalıcı seviye kayması — mevcut CUSUM (`ml/change_point.go:3-12`) tetikli ve 24 saatlik etkin slotta sürüyor; (2) kırılmadan ≤ 30 dk önce rollout (`migrations/0015_rollouts_v2.sql:91-119`, yedek `service_version_5m` `chstore/store.go:1569`); (3) hata oranı bandında. Sonuç: `ml_learned(kind='regime')` satırı. Bant yalnız rejim başlangıcından sonraki gözlemlerden; 2 hafta dolana dek slot genişletilir (gün sınıfı × saat). Koşul (3) tutmazsa otomatik öğrenme YOK; kullanıcı "yeni normal" gerekçesiyle kapatırsa aynı satır yazılır. Prod'da deploy olayı pipeline adımına bağlı ve operatörde (memory `project-deploy-events-source.md` L20-22); o gelene dek yalnız elle yol çalışır.

## 5. Incident şeması ve imza tanımı
### 5.1 Ad ve sahiplik çakışması
Bugün yön: Problem → `incidents`. Hedef akışta yön: anomali → "incident" → Problem. Aynı ad, ters yön.

| Seçenek | Artı | Eksi |
|---|---|---|
| A: `incidents`'a `stage='candidate'` + anomali bağ tablosu | Tek kavram; timeline/not UI hazır; Davis kararı 5 ile aynı çizgi (`docs/audit/davis-parity-audit.md:566-578`) | Her okuyucuya süzgeç; `AppendIncidentLifecycle` "created" bildirimi (`internal/notify/incident_lifecycle.go:35-43`); inbox katlama (`internal/api/inbox_incident_fold.go:3-27`); partition riski |
| **B: ayrı `anomaly_groups` (öneri)** | Sızıntı yok; Problems ayrı kalır; mevcut zincir bozulmaz | Yeni tablo; kullanıcıya iki ad anlatılır |

Öneri B. Ekran adı "Anomali grubu". Terfi edilen grup → Problem → mevcut kuralla `incidents`'a bağlanır (`chstore/incident.go:355-369`). Operatör "Incident" adını isterse yalnız etiket değişir (S2).

### 5.2 Şema (üç küçük tablo)
| Tablo | Motor / anahtar | Kolonlar | TTL |
|---|---|---|---|
| `anomaly_groups` | RMT(version), ORDER BY `id`, partition yok | `id`, `status` (open/closed/promoted/rejected/merged), `started_at`, `last_seen`, `root_service`, `first_signal`, `services` Array, `signals` Array, `member_count`, `score`, `problem_id`, `merged_into`, `fingerprint` (JSON, §6.4), `summary`, `version` | 400 g |
| `anomaly_group_members` | RMT(version), ORDER BY `member_id` | `member_id` (`ml:<id>` / `ae:<id>`), `group_id`, `attached_at`, `by` (auto/user), `version` | 400 g |
| `anomaly_group_events` | MergeTree, ORDER BY `(time, group_id)` | `time`, `group_id`, `action` (promote/reject/merge/split/missed/new_normal), `reason_code`, `note`, `actor`, `signature`, `profile`, `ref_id`, `details` JSON | 400 g |

Desen `incidents` / `incident_problems` / `incident_events` üçlüsünün aynısıdır (`chstore/store.go:2819-2856`). Tam satır yazımında tüm alanlar taşınır (CH-skill E2). `anomaly_group_events` aynı zamanda etiket günlüğüdür; ayrı etiket tablosu yok.

### 5.3 Gruplama kuralı (tek yazıcı: sre lideri, her tik)
Yeni anomali `a`, açık grup `G`: `skor(a,G) = max_üye [ T(Δt) × R(ilişki) × M(dedektör çifti, ilişki) ]`; `skor ≥ 0,35` ise en yüksek skorlu gruba katılır, yoksa yeni grup.

| Terim | Tanım |
|---|---|
| `T` | `exp(−Δt / 10 dk)`; Δt = iki olayın başlangıç farkı |
| `R` | aynı servis 1,0 · 1-hop 0,6 · 2-hop 0,3 · diğer 0. Komşuluk bellekteki graf: `Correlator.Neighbors`, 5 dk tazelenir (`internal/correlator/correlator.go:95,171`) |
| `M` | öğrenilen çift ağırlığı, başlangıç 1,0, sınır [0,5 – 1,5] (§6.5) |

Örnek: aynı servis, 5 dk → 0,61 katılır. 1-hop, 5 dk → 0,36 katılır. 1-hop, 15 dk → 0,13 katılmaz. 2-hop, 2 dk → 0,25 katılmaz (çift ağırlığı ≥ 1,4 öğrenilirse katılır).

Diğer kurallar: grup, tüm üyeler kapanıp 30 dk sessiz kalınca kapanır (epizot kuralıyla aynı, `ml/quality.go:112-123`); en çok 24 sa; en çok 200 üye. Grup kimliği = ilk üyenin kimliği + başlangıcı (iki yazıcıda aynı). Okuma düşerse o tik grup yazılmaz (A-skill:221). Batch profilli servisin yük olayı gruba girmez. `root_service` = en erken başlayan üye; eşitlikte çağrı yönünde en aşağıdaki.

### 5.4 Anomali – grup – Problem ilişkisi
| Geçiş | Kim | Sonuç |
|---|---|---|
| anomali → grup | Sistem | `anomaly_group_members` satırı. Tek üyeli grup da gruptur |
| grup → Problem (terfi) | Kullanıcı (editor+) | `UpsertProblem`, rule_id `anomaly-auto:group:<gid>`; `AttachProblemToIncidentWith`; bildirim; audit `anomaly_group.promote` |
| grup → kapalı (red) | Kullanıcı (editor+) | Gerekçe kodu zorunlu; audit `anomaly_group.reject` |
| Problem → çözüldü | Sistem / kullanıcı | Grup kapanınca `resolveClearedAnomalyPromotions` genişletilir (`internal/evaluator/evaluator.go:1636`); alt aile eklenince bu şart (Davis dersi) |

Problems'ta yalnız terfi edilmiş gruplar görünür (inbox `problem` türü, `internal/api/inbox.go:2054`). Aday gruplar /anomalies'te kalır. Exception'lar kendi çipinde kalır. Yeni rule_id alt ailesi A-skill:57-60'taki tüketici satırlarını ve pin testlerini ister.

### 5.5 İmza
`imza = fnv64(servis | sinyal | dedektör | yön | part_norm)`

| Bileşen | Kaynak | Kararlılık | Kardinalite |
|---|---|---|---|
| servis, sinyal, dedektör, yön | `ml_events` kolonları (`chstore/store.go:3122-3143`) | Kararlı | 5.000 × 19 × 2 üst sınır |
| metrik | `family|exprID` (`ml/metric_ml.go`) | İfade düzenlenirse değişir → `exprID` sabit tutulmalı | Aile başına birkaç |
| log şablonu | Aile ifadesi `tpl|<ifade>` (`ml/log_templates.go`), Drain kimliği DEĞİL | Drain kimliği tikten tike kararsız (memory log-ml L36-39); aile ifadesi kararlı | ≤ 100 aile (`internal/logstore/es_ml_templates.go:32`) |
| hata kodu | `code` kolonu | Kararlı | Serbest metin değilse düşük; ölçülmeli |
| pod | İmzaya GİRMEZ | Pod adı her rollout'ta değişir | — |

Mevcut `MLEventID(detector, service, part)` (`chstore/ml_events.go:69`) imzaya çok yakın; farkı yön ve pod normalizasyonu (part'ın pod içerip içermediği doğrulanmadı). İmza yalnız olay üretildiğinde doğar; dedektör başına günlük 200 olay tavanı (`chstore/ml_settings.go`, `DailyEventCap`) üst sınırı 19 × 200 = 3.800 yeni imza/gün yapar. Öğrenme seyrek imzada üst düzeye geri çekilir: imza → (servis, dedektör) → (profil, dedektör) → dedektör.

## 6. Öğrenme mekanizmaları ve güvenlik önlemleri
### 6.1 İmza bazlı hassasiyet (Beta, zamanla sönen)
Girdi: `anomaly_group_events` + olay düzeyi `anomaly_verdicts`. Etiket ağırlığı `w = 0,5^(yaş_gün / 90)`.

- `α = α0 + Σ w` (terfi veya "gerçek anomali")
- `β = β0 + Σ w` (red; yalnız "yanlış tespit" ve "etkisiz" gerekçeleri)
- Önsel: `α0 = 4·p_üst`, `β0 = 4·(1 − p_üst)`; `p_üst` bir üst düzeyin isabeti, kökte 0,5.
- İsabet `p = α / (α + β)`; `n_eff = Σ w`; `L`, `U` = Beta'nın %10 ve %90 noktaları.

| Durum | Ayar |
|---|---|
| `n_eff < 3` | Δz = 0 |
| `L > 0,6` | Δz = −0,5 (alt sınır `z_eff ≥ 3`); öncelik bir kademe yukarı |
| `U < 0,35` ve `n_eff ≥ 5` | Δz = +1,0; düşük öncelik |
| `U < 0,25` ve `n_eff ≥ 8` | Bastırma ÖNERİSİ (30 gün süreli; onay ister) |
| Diğer | Δz = 0,25'e yuvarla(2 × (0,5 − p)) × min(1, n_eff / 5); sınır [−0,5, +1,5] |

Her satır kullanıcıya şöyle açıklanır: "Bu imzada 9 etiketin 8'i 'sorun değil'; isabet tahmini %23 (aralık %10–%39); eşik 4,0 → 4,5." Ölçü: önsel 0,5 iken art arda 8 red Δz = +1,0 verir; bastırma önerisi için art arda yaklaşık 12 red gerekir. Bu, bugünkü `feedbackZ`'nin yerini alır (`ml/quality.go:104`). Davranış motoru ilkesi korunur: otomatik ayar önce öneridir (memory `project-behavior-engine.md` L15-16).

### 6.2 Red gerekçesi → doğru mekanizma
| Gerekçe kodu | β'ya girer mi | Tetiklediği |
|---|---|---|
| yanlış tespit / etkisiz | Evet | §6.1 |
| batch işi | Hayır | Yinelenen pencereli bastırma önerisi (gün maskesi + saat aralığı) |
| tatil / takvim | Hayır | Takvime gün ekleme önerisi (`chstore/anomaly_calendar.go:55-79`) |
| bakım | Hayır | Bakım penceresi önerisi |
| yeni normal | Hayır | Rejim satırı (§4.6) |
| bilinen / kabul edilen | Hayır | Süreli bastırma (en çok 90 gün) |

Bastırma deposu: `anomaly_silences` genişler — kapsam (`signature` / `service+signal`), `days_mask`, `start_min`, `end_min` kolonları. Düşük hacimli state tablosu: `ADD COLUMN IF NOT EXISTS` + probe + koşullu INSERT, iki-boot (CH-skill:50-70). Her bastırma süreli ve gerekçeli; süresiz bastırma yok.

### 6.3 Bastırılanların görünür payı
Bastırılan imzanın olayı `fnv(imza | epizot_günü) mod 10 = 0` ise düşük öncelikle gösterilir (%10, deterministik, bildirimsiz). Ayrıca `z ≥ 2 × z_eff` veya etki ≥ 5 × eşik ise bastırma delinir. Gösterilen örneğe gelen etiket normal ağırlıkla sayılır.

### 6.4 Benzer Problem eşleştirme
Terfi anında grup satırına parmak izi yazılır: `{imzalar, servissiz_imzalar, dedektörler, root_service, first_signal, süre, tepe_oran}`.

`benzerlik = 0,35·J(imzalar) + 0,25·J(servissiz_imzalar) + 0,15·J(dedektörler) + 0,15·[aynı root_service] + 0,10·[aynı first_signal]` (J = Jaccard).

`≥ 0,60` → "Şuna benziyor: <Problem>, kök neden: <metin>". Kök neden metni Problem detayındaki alandan veya postmortem'den gelir (Davis kararı 11). Eşleşen Problem P1/P2 idiyse yeni grup listede bir kademe yukarı sıralanır; Problem önceliği `computePriority`'de kalır (A-skill:123). Arama: son 180 günün terfi edilmiş grupları, Go'da düz tarama (yüzlerce satır).

### 6.5 Birleştir/böl → korelasyon ağırlığı
Hücre = (dedektör_1, dedektör_2, ilişki ∈ aynı/1-hop/2-hop); en çok 19 × 19 × 3 ≈ 1.083 hücre.

| Olay | Güncelleme (aynı 90 günlük sönüm) |
|---|---|
| Birleştir (G1 + G2) | İki grup arasındaki her çift hücresi: α += 1 |
| Böl | Ayrılan üye ile kalan her üye çifti: β += 1 |
| Grup dokunulmadan terfi edildi | Grup içi çiftler: α += 0,25 |

`M = clamp(2·α/(α+β), 0,5, 1,5)`, önsel α0 = β0 = 2. Hücreler `ml_learned(kind='pair')` satırlarıdır.

### 6.6 Kaçırılanlar
Elle Problem açma rotası yok (§2). Tanım: (a) elle ilan edilen incident (`POST /api/incidents`), veya (b) ML dışı kaynaktan açılıp "Gerçek problem" işaretlenen Problem; ve başlangıçtan önceki 30 dk içinde aynı serviste veya 1-hop komşuda anomali grubu yok. Kayıt: `anomaly_group_events(action='missed', ref_id, servis, pencere)`. Kullanım: (1) karnede "kaçırılan" sayısı; (2) replay'de recall paydası; (3) o servis+sinyalde bastırma ve Δz > 0 ayarları 30 gün askıya alınır.

### 6.7 Güvenlik önlemleri
| Önlem | Uygulama |
|---|---|
| Sürümlü, geri alınabilir | `ml_learned`: RMT(version), ORDER BY `(kind, key, rev)`; kolonlar `kind` (profile/sig_param/pair/regime), `key`, `rev`, `params` JSON, `stats` JSON, `created_at`, `created_by`. Etkin revizyon `system_settings['ml'].learning.activeRev`. Geri alma = işaretçiyi değiştirmek; admin, audit `ml_learning.activate` |
| Devreye girmeden replay | Mevcut backtest motoru (`ml/backtest.go`) aday revizyonla son 28 günü `ml_series` + `as_of` bantla yeniden koşar. Çıktı: yakalanan Problem (terfi edilmiş grup + kaçırılan), kesilen gürültü (reddedilmiş olay), yeni kaçan. Üç profilden en az ikişer örnek servis zorunlu |
| Etkinleştirme kapısı | Yeni kaçan terfi = 0; kesilen gürültü > 0; ≥ 50 etiket (mevcut eval kapısıyla aynı sayı, `internal/anomaly/eval.go:283`). Kapı geçse de etkinleştirme admin tıklamasıdır |
| Görünür pay | §6.3 |
| Etiket yaşlanması | 90 gün yarı ömür; 400 günden eski etiket silinir (TTL) |
| Öneri ≠ uygulama | Günlük iş yalnız ADAY revizyon yazar |
| Kapatma | `ml.learning.enabled=false` → tüm Δz = 0, bastırmalar pasif; veri silinmez |

## 7. Detay görünümü
Temel: mevcut `/anomalies/ml/:id` sayfası (`FE/features/anomalies/MLReplaySheet.tsx`). Yeni sayfa açılmaz. Sıra: kart → grafik → eylemler → kanıt → bağlam → "Teknik detay" (kapalı). Memory `feedback-detail-pages-plain-not-dense.md` ile uyumlu.

### 7.1 Bölümler, veri kaynağı, yeniden kullanılacak sorgu
| Bölüm | Sinyal | Kaynak | Mevcut uç / işlev |
|---|---|---|---|
| Açıklama kartı | hepsi | `ExplainCard` (Go) | Yeni alan: `/api/ml/events` satırında `summary`; detayda `card` |
| Ana grafik (dedektörle AYNI veri) | hepsi | `ml_event_snapshots`: seri + bant + parametreler | `GET /api/ml/{id}/replay` (`internal/api/ml.go:247`). Kayıt olay anında dondurulur; yeniden sorgu yok |
| Örnek trace'ler | trace | CH | `GET /api/traces` (`internal/api/api.go:4106`); link `tracesPivotHref` (`FE/lib/pivotHref.ts:95`) |
| Endpoint dağılımı | trace | CH MV | `GET /api/services/{name}/operations/routes` (`internal/api/service_operation_routes.go:111`) |
| Gecikme ısı haritası | trace | CH ham span, örneklemeli, `LIMIT 100000`, `max_execution_time 25` | `GET /api/spans/heatmap` (`chstore/heatmap.go:65,99-110,166-167`) |
| Yeni/artan log şablonları | log | ES | `GET /api/logs/patterns?baseline=1` (`internal/api/api_logs_patterns.go:82`). "İlk kez görüldü" bayrağı bu uçta YOK → `MLSeriesFirstSeen` (`chstore/ml_series.go:205`) ile eklenir |
| Log hacmi | log | `ml_series` (`_total`/`_error`) | Kayıttan; ES'e gitmez |
| İlgili + komşu seriler | metrik | VM | `GET /api/metrics/query` (`internal/api/api.go:4769`); link `metricExploreHref` (`FE/lib/metricQuery.ts:130`) |
| Aynı gruptaki anomaliler | hepsi | `anomaly_group_members` | Yeni `GET /api/anomaly-groups/{id}` |
| Topoloji komşuları | hepsi | Bellekteki graf | `GET /api/services/{name}/neighbors` (`internal/api/service_neighbors.go:89`) |
| Son rollout | hepsi | rollout tabloları | `GET /api/services/{name}/deploys` (`internal/api/api.go:2732`) |
| Benzer geçmiş Problem'ler | hepsi | `anomaly_groups.fingerprint` | Yeni; §6.4 |
| Eylemler | hepsi | — | Yeni rotalar `internal/api/anomaly_groups.go`, `registerAnomalyGroupRoutes`; yazmalar editor+, audit'li |

ES maliyetli bölümler yalnız açılınca getirilir (frontend skill:509-514). Her kanıt satırı filtreli link taşır; pencere ve servis URL'de (memory `feedback-entity-model-coherence.md`).

### 7.2 Sinyale göre görselleştirme ve chart katmanı
| Görsel | Durum | Yapılacak |
|---|---|---|
| Metrik: seri + p10–p90 bandı + p99 çizgisi + anomali penceresi | Bölge ve eşik çizgisi var (`FE/lib/chart/overlays.ts:11-20`). Bant kanalı v0.10.284'te tüketicisiz diye silinmiş (`FE/components/chart/CorePanel.tsx:221-226`). ML sayfası bandı `TimeChart` ile çiziyor (`MLReplaySheet.tsx:127-135`) | `CorePanel`'e `bands` kanalı geri gelir; ML grafiği `CorePanelMulti`'ye taşınır (frontend skill:249-262). Küçük genişletme |
| Trace: ısı haritası + baseline p99 çizgisi | Ham `<canvas>`, uPlot değil; hücre tıkı ve kutu seçimi var; çizgi katmanı yok (`FE/components/LatencyHeatmap.tsx:137,341-374,430`) | Aynı canvas'a çizgi katmanı. uPlot gerekmez, yeni kütüphane gerekmez |
| Trace: hata oranı + endpoint kırılımı | Var | `CorePanelMulti` + kayıt listesi tablosu |
| Az çağrılan servis: noktasal | Yok | `CorePanel` nokta kipi (çizgisiz seri); eşik çizgisi `T90` |
| Log: yığılmış hacim + baseline gölge çubuk | `stacked-bars` var (`CorePanel.tsx:316`); Logs sayfası yığın değil (`FE/components/LogsHistogram.tsx:25-29`) | Arkada düşük opaklıklı ikinci çubuk serisi. Küçük genişletme |
| Log: şablon tablosu | — | `DataTable` + `useDataTable`; kolonlar: şablon, sayı, baseline oranı, "ilk kez", örnek satır |
| Grup: üç şeritli ortak zaman çizgisi | Yok. Emsal: düz SVG Gantt (`FE/pages/trace/TracePodSpanTimeline.tsx:1-9`), olay şeridi (`FE/components/charts/AnnotationLane.tsx:7-14`) | Yeni SVG bileşen (zaman serisi değil; uPlot kuralı dışında). İlk başlayan sinyal kalın kenar + "ilk" etiketi |
| Liste satırı sparkline | SVG `Sparkline` var (`FE/components/Sparkline.tsx:7-10`) | Liste ucu satır başına ≤ 24 nokta döner (kayıttan) |

Isı haritası uyarısı: ham span'ler tail-sampled; ısı haritası yavaş/hatalı istekleri olduğundan fazla gösterir. Başlıkta "örneklenmiş span'ler" yazılır. Yansız seçenek VM histogram kovalarıdır (`FE/components/dashboard/histogramHeatmap.ts`, okunmadı — doğrulanmadı).

Sabit renk + ikon (tek dosya `FE/lib/signalKind.ts`; liste, detay, CoSRE kartı aynı):

| Sinyal | Token | İkon | Renksiz ayırt edici |
|---|---|---|---|
| Metrik | `--teal` | çizgi grafik | "Metrik" etiketi, düz çizgi |
| Trace | `--purple` | dallanan span | "Trace" etiketi, kesikli çizgi |
| Log | `--orange` | satır listesi | "Log" etiketi, noktalı çizgi |
| Grup | nötr | katman | "Grup · N" etiketi |

Token'lar üç temada tanımlı (frontend skill:481-493). Renk hiçbir yerde tek taşıyıcı değildir: her yerde ikon + metin; grafikte çizgi deseni (`dash`, `FE/components/viz/TimeSeriesPanel.tsx:74`). Üç temada kontrast Faz 2 kabul kriteridir.

### 7.3 Yazılı açıklama kartı (deterministik, LLM yok)
Üretici: `internal/ml/explain_card.go` (yeni, saf işlev, tablo testli). Girdi: olay + kayıt + profil + grup + rollout. Çıktı struct'ı API'de, CoSRE aracında (`internal/mcptools/v2_ml.go:34,96`) ve bildirimde (`internal/notify/tr_template.go:266` alanları) aynen kullanılır.

| Alan | Şablon | Kaynak |
|---|---|---|
| Ne oldu | `{servis} servisinde {olgu}.` | dedektör → olgu sözlüğü |
| Ne kadar | `{gözlenen}; bu saatte normalde {q10}–{q90}. {oran ifadesi}.` | kayıt + bant |
| Ne zamandan beri | `{gün} {saat}'den beri, {süre}dır sürüyor.` / `… sürdü.` | `started_at`, `last_seen` |
| Etki | `Bu sürede yaklaşık {etkilenen} istek {etki fiili} (toplam {toplam}). Kanal: {kanal}.` | trace serisi; kanal yoksa cümle atlanır |
| Neden önemli | profil × zaman sözlüğü | `ml_learned(profile)` + takvim |
| Yakında ne değişti | en çok 3 madde: rollout, yeni log şablonu, komşu anomali | rollout, şablon ilk görülme, grup |
| Nereye bakmalı | 2–4 filtreli link | `tracesPivotHref`, `logsHref`, `serviceHref` |
| Teknik detay (kapalı) | dedektör, z, eşik, imza, bant haftaları, ayar revizyonu | kayıt |

Oran ifadesi: < 1,3 "biraz yüksek"; 1,3–1,7 "yaklaşık 1,5 kat"; ≥ 1,8 tam sayıya yuvarla "yaklaşık N kat"; > 20 "20 kattan fazla"; taban 0 ise "normalde hiç görülmez" (asla "×0,0"; 1587 dersi, memory davis-parity L17-20). Etki tabanı asla yalnız yüzde değildir (`docs/DECISIONS.md:11591`).

**Tek satır özet** = `{servis}: {olgu kısa} {oran ifadesi}, {süre}dır ({saat}'den beri).` Liste satırı, CoSRE kart başlığı ve bildirim konusu budur.

**Örnek 1 (trace, 7/24 profil)**
- Özet: `payments-mobile: yavaş istek oranı normalin yaklaşık 3 katı, 25 dakikadır (14:10'dan beri).`
- Ne oldu: payments-mobile servisinde yavaş yanıt alan isteklerin oranı arttı.
- Ne kadar: İsteklerin %12'si 1 saniyeden uzun sürüyor; bu saatte normalde %3–5. Yaklaşık 3 kat.
- Ne zamandan beri: Bugün 14:10'dan beri, 25 dakikadır sürüyor.
- Etki: Bu sürede yaklaşık 4.800 istek yavaş yanıt aldı (toplam 40.000). Kanal: mobil.
- Neden önemli: Bu servis günün her saati kullanılıyor ve şu an yoğun saatinde.
- Yakında ne değişti: 13:58'de yeni sürüm yayına alındı. Aynı dakikalarda orders-api'de hata oranı arttı.
- Nereye bakmalı: Yavaş trace'ler · Endpoint dağılımı · Sürüm karşılaştırması.

**Örnek 2 (log, mesai profili)**
- Özet: `branch-teller: hata logları normalin yaklaşık 9 katı, 15 dakikadır (10:30'dan beri).`
- Ne oldu: branch-teller servisinde hata logları arttı ve daha önce görülmemiş bir hata mesajı çıkmaya başladı.
- Ne kadar: Son 15 dakikada 620 hata satırı; bu saatte normalde 50–90. Yaklaşık 9 kat. Yeni mesaj: "Connection pool exhausted" (ilk kez 10:32'de, 410 satır).
- Ne zamandan beri: Bugün 10:30'dan beri, 15 dakikadır sürüyor.
- Etki: Aynı sürede yaklaşık 1.150 istek hata aldı (toplam 14.400). 6 pod'un 6'sında görülüyor.
- Neden önemli: Bu servis mesai saatlerinde kullanılıyor ve şu an mesai içinde.
- Yakında ne değişti: Son 24 saatte yayın yok. Bağlı olduğu veritabanında bağlantı süresi arttı.
- Nereye bakmalı: Bu mesajı içeren loglar · Hatalı trace'ler · Servis sayfası.

### 7.4 LLM "muhtemel hikâye" (yalnız ≥ 2 sinyal türlü grup)
| Kural | Uygulama |
|---|---|
| Çağrı | `copilotExplainJSON` (`internal/api/ai_observability.go:198`); lokal model, Türkçe; prefetch + narrate (memory `project-copilot-runtime.md` L40-45) |
| Girdi | Yalnız kartlar ve numaralı kanıt listesi (E1…En) |
| Çıktı şeması | `{iddialar: [{metin, kanit: ["E2","E5"]}]}` |
| Kanıtsız iddia | Sunucu, listede olmayan kanıta atıf yapan veya atıfsız iddiayı SİLER (emsal: `internal/sre/investigation.go:291-311`). Hiç iddia kalmazsa paragraf gösterilmez |
| Etiket | "Değerlendirme" rozeti; her cümlenin sonunda kanıt linki |
| Sınır | Tespit, eşik, gruplama ve önceliğe girmez |

## 8. Ölçüm
| Ölçü | Tanım | Kırılım | Yer |
|---|---|---|---|
| Terfi oranı (isabet) | terfi / (terfi + red[yanlış tespit, etkisiz]) | imza, dedektör, profil, sinyal | ML karnesi (`/api/ml/scorecard`) |
| Kaçırılan Problem | §6.6 sayısı | servis, profil, sinyal | Aynı |
| Terfi süresi | terfi anı − grup başlangıcı, medyan | profil | Aynı |
| Gürültü | red / gün | imza | Aynı |
| Bastırılan / gösterilen pay | sayaç | imza | Aynı + `/admin/stats` (`ml/stats.go:47`) |

Problems başlığına şerit eklenmez (reddedildi).

## 9. Doğrulanmadı listesi
| # | Konu | Neden önemli |
|---|---|---|
| D1 | Dar rollup tabloları prod'da kurulu mu (`chstore/rollup_read.go:14-18`) | S1 seçenek A |
| D2 | `quantilesTDigestState(0.5,0.95,0.99)` durumundan p90 okunabilir mi | Seçenek A'da p90 |
| D3 | `MaxServices 500` tam olarak neyi sınırlıyor | Ölçek riski 2 |
| D4 | `ml/promote.go` gerçekten Problem kopyası yazıyor mu (yorum çelişkisi `chstore/ml_settings.go:347-348`) | Çift terfi |
| D5 | `clustering.go` (`anomaly-cluster:`) bugün etkin mi | S3 |
| D6 | `incident_cases` şeması ve `SimilarIncidentCases` girdileri | Faz 6 |
| D7 | ML olay `part` alanı pod adı içeriyor mu | İmza kararlılığı |
| D8 | ML hattı batch kalıbını uyguluyor mu | Profil "batch" |
| D9 | `histogramHeatmap.ts` yetenekleri | Yansız ısı haritası |
| D10 | Tüm maliyet sayıları: tahmin. Prod'da ML hiç çalışmadı | Faz 1 ölçümü |

## 10. Faz planı
Her dilim tek sürüm. **[T]** = tasarım (ana oturum / architect), **[M]** = mekanik → `implementer`. Yeni yüzey = önce mockup + "Onay".

### Faz 1 — Baseline, profil, replay doğrulaması
| Dilim | Tür | İş | Kabul |
|---|---|---|---|
| 1.1 | [T] | Ölçek ölçümü: 5.000 serviste tik süresi, VM/ES sorgu sayısı, `ml_series` satır hızı | Sayılar bu dokümana işlenir; D3, D10 kapanır |
| 1.2 | [M] | `ml_baseline_obs` DDL + okuma/yazma + testler | DDL `tables` diliminde; dağıtık çift; `partition_dedup_test` geçer |
| 1.3 | [T] | Günlük iş: saf hesap çekirdeği + sınıf/slot kuralları | Tablo testi; iki yazıcı determinizmi; < 10 dk, < 2 GB |
| 1.4 | [M] | Trace/metrik VM backfill + log backfill 56 gün | Hız sınırlı; yeniden başlatılabilir; ilerleme `ml_tick_status`'ta |
| 1.5 | [T] | Profil sınıflandırıcı + `ml_learned(profile)` + Settings'te salt-okur liste ve elle sabitleme | Üç profilden örnek servislerde elle doğrulama |
| 1.6 | [T] | Karar çekirdeği banda geçer; eski `baselineDates` silinir; binom testi | Backtest: eski/yeni fark raporu; gölge kipte |
| 1.7 | [T] | Rejim kuralı (§4.6) | Sentetik seviye kayması testi |
| 1.8 | [T] | Replay raporu: 28 gün, profil başına ≥ 2 servis | Operatör raporu görür; Faz 2 kapısı |

Operatörden: S1 kararı; prod'da ML ana anahtarını gölgede açmak; üç profilden örnek servis adları.

### Faz 2 — Anomali detay görünümü
| Dilim | Tür | İş | Kabul |
|---|---|---|---|
| 2.1 | [T] | Mockup: kart + grafik + kanıt + bağlam | Operatör "Onay" |
| 2.2 | [T] | `ExplainCard` çekirdeği + sözlükler + tablo testi (§7.3 iki örnek fikstür) | Örnek çıktılar birebir |
| 2.3 | [M] | API'de `summary` / `card`; FE metin üreticileri silinir (`mlReplay.ts` metin işlevleri) | Tek kaynak testi |
| 2.4 | [M] | `signalKind.ts` + liste/detay/CoSRE kartına uygulama | Üç temada kontrast kontrolü |
| 2.5 | [M] | `CorePanel` `bands` kanalı; ML grafiği `CorePanelMulti`'ye | `corePanelContracts.test` geçer |
| 2.6 | [M] | Isı haritası çizgi katmanı; log gölge çubuk; nokta kipi | Görsel fikstür |
| 2.7 | [M] | Kanıt bölümleri (trace, endpoint, şablon tablosu, komşu seriler) + linkler | ES bölümleri yalnız açılınca |
| 2.8 | [M] | Liste sparkline + tek satır özet | Tablo standardı; ≤ 24 nokta |

### Faz 3 — Grup modeli ve gruplama
| Dilim | Tür | İş | Kabul |
|---|---|---|---|
| 3.1 | [T] | S2, S3 kararları + mockup (Gruplar sekmesi, grup detayı, zaman çizgisi) | "Onay" |
| 3.2 | [M] | Üç tablo DDL + chstore işlevleri | Tam satır taşıma testi |
| 3.3 | [T] | Gruplama çekirdeği (saf) + kayan pencere simülasyonu | A-skill:224-230 inceleme şekli |
| 3.4 | [M] | Gruplayıcı döngü (sre, lider), Settings anahtarı | Okuma düşerse yazmaz |
| 3.5 | [M] | `internal/api/anomaly_groups.go` okuma rotaları + Gruplar sekmesi | viewer salt-okur görür |
| 3.6 | [M] | Grup zaman çizgisi bileşeni + grup kartı | İlk sinyal vurgulu |
| 3.7 | [T] | LLM "Değerlendirme" (§7.4) | Atıfsız iddia testte silinir |

### Faz 4 — Eylemler ve etiket kaydı
| Dilim | Tür | İş | Kabul |
|---|---|---|---|
| 4.1 | [T] | Terfi: rule_id ailesi, tüketici satırları, çözülme, bildirim türü | A-skill dört soru yanıtlı; pin testleri |
| 4.2 | [M] | Red + gerekçe kodu + not; audit | editor+; gerekçe zorunlu |
| 4.3 | [M] | Birleştir / böl; üyelik yeniden yazımı; audit | Geri alınabilir (ters işlem) |
| 4.4 | [M] | Kaçırılan kaydı (§6.6) | Elle incident + "Gerçek problem" yolu testli |
| 4.5 | [M] | Karne: isabet, kaçırılan, terfi süresi; imza/profil kırılımı | Mevcut karne tablosunda |

### Faz 5 — İmza bazlı öğrenme ve bastırma
| Dilim | Tür | İş | Kabul |
|---|---|---|---|
| 5.1 | [T] | Beta çekirdeği + hiyerarşi + sınırlar; `feedbackZ` silinir | §6.1 tablo testi |
| 5.2 | [M] | `ml_learned` DDL + revizyon işaretçisi + etkinleştir/geri al rotaları | admin, audit |
| 5.3 | [T] | Replay raporu aday revizyon için; etkinleştirme kapısı | Üç profil örneği; yeni kaçan = 0 |
| 5.4 | [M] | `anomaly_silences` genişlemesi (iki-boot) + yinelenen pencere UI | Süresiz bastırma reddedilir |
| 5.5 | [M] | Gerekçe → öneri akışları (takvim, bakım, batch) | Öneri, onaysız uygulanmaz |
| 5.6 | [M] | Görünür pay (%10) + delme kuralı | Deterministik test |
| 5.7 | [T] | Çift ağırlığı öğrenimi (§6.5) | Simülasyon testi |

Operatörden: ≥ 50 etiket birikmesi; ilk revizyonun etkinleştirme kararı.

### Faz 6 — Benzer Problem eşleştirme
| Dilim | Tür | İş | Kabul |
|---|---|---|---|
| 6.1 | [T] | D6 doğrulaması; parmak izi deposu kararı (grup satırı / `incident_cases`) | Karar notu |
| 6.2 | [M] | Parmak izi yazımı (terfide) + benzerlik işlevi | §6.4 tablo testi |
| 6.3 | [M] | Detayda "Şuna benziyor" + sıralama etkisi + CoSRE aracı | Eşik 0,60; link Problem'e |
| 6.4 | [T] | Eşik ve ağırlıkların etiketli geçmişle kalibrasyonu | Replay raporu |

## 11. Açık sorular (yanıtı tasarımı değiştirenler)
| # | Soru | Önerilen varsayılan |
|---|---|---|
| S1 | Baseline kaynağı dar rollup tDigest mi (görev metni), dedektör serisi mi? Tail sampling kararı hâlâ geçerli mi? | Dedektör serisi (`ml_series`). Rollup yalnız süre dağılımı kanıtı için, yanlılık notuyla |
| S2 | Aday gruplar mevcut `incidents` tablosunda mı, ayrı tabloda mı? Ekranda adı ne? | Ayrı `anomaly_groups`; ekranda "Anomali grubu" |
| S3 | Topoloji kümeleme (`anomaly-cluster:`, onay bekleyen spec) ne olacak? | Yeni grup katmanı onu karşılar; spec kapatılır, `clustering.go` Faz 3'te kaldırılır |
| S4 | Terfi yalnız elle mi, yoksa isabeti kanıtlanmış imzada otomatik de olsun mu? | Yalnız elle. Otomatik terfi ayrı karar, en erken Faz 5 sonrası |
| S5 | VM recording rule'ları ürün mü üretsin? | Hayır. Faz 1 ölçümü VM sorgu maliyetini yüksek bulursa kural dosyası indirme seçeneği eklenir |
| S6 | Bakım penceresi ML adaylarını da kessin mi (bugün yalnız bildirim)? | Evet, yalnız ML hattında: olay düşük öncelikli yazılır, gruba girmez |
| S7 | Elle Problem açma düğmesi eklensin mi (bugün yok)? | Hayır. "Declare incident" + "Gerçek problem" kaçırılan etiketi için yeter |
| S8 | Özne anomalileri (`subject_red`) gruplara üye olsun mu? | Evet, özneler açıldığında; üyelik türü `ae:` hazır |
| S9 | Endpoint düzeyi baseline ilk sürümde var mı? | Hayır. Önce servis düzeyi; endpoint 3 saatlik blokla Faz 1 ölçümünden sonra |
| S10 | Etiket yarı ömrü 90 gün, görünür pay %10 uygun mu? | Evet; ikisi de `system_settings['ml'].learning` altında ayarlanabilir |

## 12. Operatör kararı (2026-11-05)
Operatör: "Benden beklediğin kararda önerini yapalım." S1–S10 **önerilen varsayılanlarla KARARA BAĞLANDI**: S1 baseline kaynağı `ml_series` (rollup yalnız süre dağılımı kanıtı) · S2 ayrı `anomaly_groups`, ekranda "Anomali grubu" · S3 `anomaly-cluster:` spec'i kapanır, `clustering.go` Faz 3'te kalkar · S4 terfi yalnız elle · S5 recording rule üretilmez · S6 bakım penceresi ML adaylarını keser · S7 elle Problem düğmesi yok · S8 özne anomalileri gruba üye · S9 önce servis düzeyi baseline · S10 yarı ömür 90 gün, görünür pay %10. Faz 1 başlangıcı ayrı onay.

## 13. §9 doğrulama sonuçları (2026-11-05, koddan)
| # | Sonuç | Kanıt | Tasarıma etkisi |
|---|---|---|---|
| D1 | Dar rollup boot'ta kurulmaz; yalnız admin sihirbazı | `chstore/rollup_admin.go:202-208,448` | S1 kararı (kaynak `ml_series`) rollup'a bağımlılığı kaldırır; prod durumu hâlâ bilinmiyor |
| D2 | Rollup state'inde p90 YOK (0.5/0.95/0.99) | `migrations/0001_rollup_narrow.sql:26,52`; `chstore/rollup_fastpath.go:97` | Rollup kanıt olarak kullanılırsa bant p95 ile çizilir; state değişmez |
| D3 | `MaxServices` yalnız ES log `terms` boyutu (vars. 500, 10..5000); aşan servisler doc_count sırasıyla SESSİZ düşer; trace/metrik yolunda tavan yok | `logstore/es_ml.go:80`; `chstore/ml_settings.go:228,247` | Faz 1.1 ölçümüne "düşen servis sayacı" eklenir; binlerce serviste log sinyali sayfalanmalı |
| D4 | ML terfisi Problem YAZMAZ; `anomaly_events`'e `ml:<dedektör>` kopyası; varsayılan kapalı | `ml/promote.go:19,50,89`; `chstore/ml_settings.go:225-227` | Terfi yolu sayısı üç + bu kopya; grup terfisi tek Problem yazıcısı kalır. `ml_settings.go:347` yorumu eskimiş |
| D5 | Topoloji kümeleme KOŞULSUZ etkin (ayar kapısı bulunamadı) | `anomaly/anomaly.go:736-742`; `anomaly/clustering.go:155,355` | S3: Faz 3'te kalkana kadar `anomaly-cluster:` Problem'leri açılmaya devam eder |
| D6 | `incident_cases` + `SimilarIncidentCases` var: puan = 3·servis + 2·kod kesişimi + exp(−yaş/30) | `chstore/store.go:3221-3253`; `chstore/incident_cases.go:445-464` | Faz 6 benzer Problem eşleştirmesi bu tablo ve puanı genişletir; yeni tablo gerekmez |
| D7 | Pod-outlier olaylarının kimliği POD ADI içerir; diğer parçalar (route, kod, şablon) pod'dan bağımsız | `ml/snapshot.go:25-52`; `ml/log_breakdown.go:241`; `ml/trace_pods.go:189` | İmza pod adını ÇIKARIR (`pod|*`); aksi halde her restart yeni imza |
| D8 | ML hattı `-batch` kalıbını UYGULAMIYOR | `internal/ml` içinde kullanım yok | Operatör kuralına aykırı (batch yükü anomali değil). Faz 1'in ilk dilimi olarak kapatılır |
| D9 | Isı haritası canvas; çizgi katmanı desteği YOK | `FE/components/LatencyHeatmap.tsx:108,174` | Faz 2'de `overlayLines` prop'u eklenir; yeni kütüphane gerekmez |
| D10 | Maliyet sayıları hâlâ tahmin | — | Faz 1.1 ölçümü kapatır |

## 14. Uygulama düzeltmeleri
Faz 1 uygulanırken bu dokümandaki üç ifadenin yanlış/eksik olduğu görüldü (koddan):

- **(a) ML tabloları state deseni, `_local` + Distributed çifti YOK.** §4.1 "Dağıtık" satırı yanlıştı. `ml_baseline_obs` (v0.10.1813) `ml_series` / `ml_events` gibi state tablosu: küme kipinde tek replikasyon grubu, yazıcı yalnız ML lideri; shard'a bölünmüş çift kurulmaz. Sonraki ML tabloları (`ml_learned`) aynı desen.
- **(b) İki ES terms tavanı aynı servis kümesini görür; düşen sayılar TOPLANMAZ.** Log RED agregasyonu ve kod/pod kırılımı aynı `MaxServices` terms tavanıyla aynı doc_count sırasını görür; tik ölçümünde (v0.10.1812) tavan dışı servis sayısı iki isteğin MAKSİMUMU alınır, toplamı değil.
- **(c) Log `error_rate` bir hata SAYISIDIR, oran değil.** `_error` parçası hata seviyesindeki satır sayısı; batch kuralı (v0.10.1811) bu yüzden onu "hata sayısı" sınıfında değerlendirir ve pay kanıtı (`_error` / `_total`) ister.

## 15. Operatör düzeltmesi — "replay" (2026-11-05)
Operatör: "Replay raporu istemiyorum; bir anomali olduğunda oynat tuşu gibi gösterim için istemiştim."
- **Replay = olayın kendi gösterimi.** Anomali detayında olay anı kaydı (`ml_event_snapshots`, `/anomalies/ml/:id`, `FE/components/coremetry/anomalies/MLReplaySheet.tsx`) seri + beklenen bant + anomali penceresi + işaretlerle oynatılır/gösterilir. Faz 2'nin konusu budur.
- **Kalkanlar:** dilim 1.8 (28 günlük replay raporu); Faz 1 ve Faz 5 başlıklarındaki "+ replay doğrulaması"; §6'daki "etkinleştirmeden önce 28 günlük replay raporu" kapısı; dilim 1.6 kabulündeki "eski/yeni fark raporu".
- **Yerine kalan güvenlik:** öğrenilen her ayar revizyonlu ve tek tıkla geri alınır; yeni karar çekirdeği ve öğrenilen ayarlar önce gölgede çalışır (yalnız sayaç: eski karar / yeni karar farkı tik durumunda); bastırılanların %10'u görünür kalır; etkinleştirme için asgari etiket sayısı şartı sürer.

## 16. Operatör kararı — Incidents yerine Anomalies (2026-11-05)
Operatör (mockup'ı gördükten sonra): "Incident sekmesi yerine Anomalies olsun; eski incident'lar artık devre dışı kalsın, bu yeniler gelsin."
- S2 kararı değişti: mevcut `incidents` yüzeyi "Problem-sonrası katman olarak kalır" DEĞİL; **emekli edilir**. Yerine Anomalies (anomali grupları) gelir.
- Mockup yönü (play tuşlu detay + anomali grubu) kabul edildi sayılır; ayrıntı soruları §Faz 2 uygulamasında önerilen varsayılanlarla.
- Emeklilik planı ayrı bir dilim setidir (envanter → gizle/kapat → bağlantıları yönlendir → veri silinmez). Kapsam envanterden sonra bu bölüme yazılır.

## 17. Faz 2 mockup kararları (2026-11-05, operatör: "Karar isteyen noktalarda önerini yapalım")
Mockup `docs/frontend/mockups/anomaly-detail.html` ONAYLI. Önerilen varsayılanlar karara bağlandı:
- Bölüm sırası: başlık çipleri → kalın özet → kart → grafik + play → **eylemler** → kanıt → bağlam → katlı teknik detay.
- Play: durağan hâlde imleç "şimdi/bitiş"te; listedeki ▶ detayı açıp oynatır (satır içi mini oynatma yok); ilk sapmadan 6 dk önce başlar, kova başına ~0,25 sn, döngü/hız seçici yok; hareket azaltma açıksa adımlar arası atlar.
- Log grafiği varsayılanı mesaj kalıbına göre yığılır; "Seviye" ikinci seçenek.
- Az çağrılan serviste ısı haritasının yerini nokta grafiği alır (ikisi birlikte gösterilmez).
- Sinyal renkleri ayrı üç token (`--sig-metric/-trace/-log`), üç temada kontrast ölçülerek; her yerde ikon + metinle.
- Bastırılan imzadan örnek satır aynı listede soluk + rozetli (ayrı sekme yok).
- "Sorun değil" gerekçeleri: yanlış tespit / batch işi / tatil-takvim / bakım penceresi / yeni normal / diğer; süre isteğe bağlı, bastırma doğuran gerekçelerde varsayılan 30 gün.
- Model "Değerlendirme" kutusu yalnız ≥2 sinyal türü olan grupta ve copilot açıkken; tek anomali detayında hiç görünmez.

### 16.1 Incidents emeklilik planı (envanter: 2026-11-05, koddan)
Envanter özeti: nav + `/incidents` liste + `/incident?id=` detay (`FE/components/Sidebar.tsx:90`, `FE/App.tsx:211-212`); tablolar `incidents`, `incident_events`, `incident_problems` (`chstore/store.go:2819-2856`); ~20 otomatik yazıcı tek kapıdan geçer (`chstore/incident.go:381 AttachProblemToIncidentWith`, `:742 JoinOpenIncidentWith`), yarısının anahtarı yok; bildirim tek kanca (`main.go:652` → `chstore/incident_lifecycle.go:35,43`); Problems listesinde incident satırı + altındaki Problem'leri katlama (`internal/api/inbox.go:769-795`, `inbox_incident_fold.go`); elle "Declare incident", postmortem taslağı, durum sayfasında yayınlama (`internal/api/api_incidents.go`, `postmortem_draft.go`, `api.go:7901`). `incident_cases` AYRI kavramdır (CoSRE vaka belleği) ve KALIR.

| Adım | Ne yapılır | Not |
|---|---|---|
| E1 | Tek ana anahtar `incidents.enabled` (`system_settings`, **varsayılan kapalı**). Kapalıyken: otomatik bağlama/oluşturma durur (iki kapıda erken dönüş), yaşam döngüsü bildirimi gitmez, elle oluşturma 410 döner | Tek commit; anahtar açılırsa eski davranış aynen döner |
| E2 | Problems listesi: `incident` türü varsayılandan ve çiplerden çıkar; katlama kalkar, Problem'ler tek tek görünür | Anomali grupları gelene kadar satır sayısı artabilir |
| E3 | Menü, komut paleti, kısayol, sayfa kaydı: "Incidents" kalkar; `/incidents` → `/anomalies`'e yönlenir. `/incident?id=` **salt-okunur arşiv** olarak doğrudan linkle açılır (eski bildirim linkleri ölmesin) | Veri silinmez |
| E4 | Açık eski incident'lar: mevcut otomatik kapanış sessizce (bildirimsiz) sürer | — |
| E5 | Bildirim türü listesinden ve Anomaly sekmesindeki "incident'a bağla" kutularından `incident` kalkar; CoSRE/MCP metinleri ve `build_link` hedefi güncellenir | Altın testler güncellenir |
| E6 | Anomalies'e üçüncü segment "Gruplar" (Faz 3 ile dolar); o zamana dek boş durum metni | `FE/components/coremetry/anomalies/MLTab.tsx:193-203` |
| E7 | "Kaçırılan" etiketi artık "Declare incident" değil: Anomalies'te **elle anomali grubu oluştur** + Problem'e terfi (S7'nin yerine) | Faz 4 |

Kaybolan işlevler (operatöre söylendi): elle incident ilanı, postmortem taslağı, durum sayfasında "geçmiş incident" yayınlama. Öneri: postmortem ve yayınlama, terfi edilen Problem'e Faz 4'te taşınır; o zamana dek arşiv sayfasında salt-okunur.
