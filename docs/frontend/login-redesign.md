# Login sayfası yeniden tasarımı — Faz 0 audit

Tarih 2026-11-06 · HEAD 170d7a5d7 · yalnız doküman, kod değişmedi. Mockup: `origin/mockups/login-mobile` dalında `docs/frontend/mockups/login-corporate.{html,png}` ve `login-dark.{html,png}` (main'de yok). Mockup'taki kurum adı koda, teste, i18n'e ve bu dokümana girmez; burada `<Kurum>`.

Süreç: operatör "bitir" dedi, uygulama onay beklemeden başlar. §9'daki her açık soru bir **varsayılanla** kapalıdır; ana oturum o varsayılanla ilerler. Kanıt koddan okundu; hiçbir sorgu koşturulmadı, hiçbir şey ölçülmedi (§10). "(Y)" = yeni dosya/sembol. Kısa yollar: `chstore/…`, `api/…`, `vmetrics/…` = `internal/` altı; `pages/…`, `lib/…`, `components/…`, `styles/…` = `frontend/src/` altı.

## 1. Özet ve önerilen karar

| Konu | Karar |
|---|---|
| Uç | `GET /api/public/login-summary` (Y, `internal/api/login_summary.go`). İstek yolu **hiçbir zaman** CH/VM/Redis'e gitmez: bellekteki anlık görüntüyü ayara göre süzüp yazar. `s.serveCached` kullanılmaz (S1). |
| Tazeleme | Lider kilitli tek worker anlık görüntüyü kurar (CH sayıları 60 sn, VM + topoloji + CH boyutu 5 dk), Redis'e yazar; her pod 30 sn'de bir Redis'ten çeker. 15 dk'dan bayatsa `ready:false`, frontend sade login'e düşer. |
| Sayılar | `spans_today`, `traces_today`, `services_monitored` MV'den (ham `spans` yok). "Bugün" = ClickHouse günü (S5). |
| Aktivite | Self-obs span'lerinden **türetilmez** (S2): örnekleme %10, başarı ayrımı ve kullanıcı kimliği span'de yok. `logins` + `traces_viewed` pod sayaçlarından, `cosre_questions` `cosre_interactions`'tan, `rollouts_tracked` rollout tablosundan. Self-obs kapalıyken de çalışır. |
| HTTP grafikleri | Sunucu üç sabit PromQL kurar (istemciden sorgu kabul edilmez), 24 sa'lik halkayı 5 dk'da bir **yalnız son 15 dk'yı** sorgulayarak günceller. p95, mevcut `AllowUnfilteredPercentiles` korumasına uyar (S3). |
| Topoloji | `topology_edges_5m` son 15 dk, 150 servis + 30 db/kuyruk; `tier` yeni saf kural; id = tuzlu özet; ağırlık 0–1 normalize; ad yalnız "isimli" kipte. Durum rengi yok (§7-2). |
| Ayarlar | Yeni tablo/anahtar yok: mevcut `branding` blob'una dört alan. Yer: Settings → **Branding** ("General" sekmesi yok, S6). Varsayılan: özet açık, söz **kapalı** (operatör kararı 2026-11-06, audit sonrası), topoloji **isimsiz**. |
| Frontend | `pages/Login.tsx` shadcn + Tailwind'e yeniden yazılır, §3'teki 10 maddelik envanter korunur. Grafik: mevcut `CorePanelMulti` + tek ek prop `embedded` (Y). Canvas: saf yerleşim çekirdeği + ince çizim katmanı, yeni bağımlılık yok. |
| Söz | Sunucu döndürmez; istemci `dayOfYear % len` ile seçer (S4). |

## 2. İstekten sapmalar (her biri görünür, hiçbiri sessiz değil)

- **S1 — `serveCached` 60 sn yerine anlık görüntü.** `serveCached` ıskada `fn`'i istek içinde koşturur ve hata olunca bayat dönmez, yalnız TTL..3×TTL arası SWR vardır (`api/cache.go:216, 252-324`). Kimliksiz uç için bu, soğuk önbellekte VM/CH'ye kimliksiz tetik demektir. Emsal `warmDependenciesCache` (`api/api.go:1394`) ısıtır ama ıskada yine CH'ye gider. Ship checklist madde 2 "uygulanmaz: girdi yok, arka uç yok" diye işaretlenir.
- **S2 — Aktivite self-obs span'lerinden değil.** Kanıt §5.2. İstenen `service.name=coremetry` + `http.route` süzgeci kodda karşılıksız: ad kipe göre `coremetry-monolithic|api|worker` (`internal/selfobs/selfobs.go:285-298`); `http.route` büyük olasılıkla span'e yazılmıyor (yorum `selfobs.go:130-133`; CH'de doğrulanmadı).
- **S3 — p95 varsayılan kapalı.** Süzgeçsiz yüzdelik, denetim kaydına bağlı bir korumayla reddediliyor (`vmetrics/promql.go:563-568`, `vmetrics/client.go:102-108`: "protection can only be removed by an explicit admin decision that lands in audit_log"). Ham sorgu yolu korumayı çağırmıyor (`vmetrics/histogram.go:347`); login bu açıktan sessizce geçmez. Bayrak açıksa p95 çizilir.
- **S4 — `quote` uçtan çıkar.** Go ikilisi `frontend/src/data/` altını embed edemez; söz veri değil sunum. İstemci seçer, bayrak `branding`'den gelir.
- **S5 — "Bugün" gece yarısı = ClickHouse sunucu saat dilimi.** Ucuz tekil trace sayısı yalnız `trace_summary_1d`'de ve `toDate(time)` ile kesilmiş (`chstore/store.go:1390-1400`). İki KPI aynı pencereyi kullansın diye span sayısı da CH gününden okunur.
- **S6 — Ayarlar Branding sekmesinde, aç/kapa `SelectField` ile.** "General" sekmesi yok (`pages/settings/tabIndex.ts:10-40`); login alanlarının sahibi Branding (`pages/settings/BrandingTab.tsx:116-220`). Repoda `Switch` bileşeni yok; emsal on/off `SelectField` (`BrandingTab.tsx:157-163`).
- **S7 — Yeniden kullanılan paneller istenen adlarla mevcut değil.** Overview'daki "Throughput · metrik" route'a göre kırılır (`pages/service/Overview.tsx:1184-1203`); status kodu serisi ayrı panel "HTTP Status · metrik" (`:1254-1260`); "Response time" yalnız avg (`:1116`), p95 yalnız HTTP Metrics sekmesinde (`lib/httpMetrics.ts:123-127`). Login, aynı `CorePanelMulti` ile status serisini "Throughput · metrik" başlığı altında çizer.
- **S8 — "200 ve avg altında gradient" seçilemez.** CorePanel dolguyu her çizgi serisine uygular (%12 + gradient; `components/chart/CorePanel.tsx:767-772`). Varsayılan: tüm seriler aynı soluk dolguyu alır; seri başına dolgu prop'u eklenmez.
- **S9 — Ortak x ekseni yok.** Ekseni gizleyen prop yok (`CorePanel.tsx:678`); iki panel kendi eksenini çizer, imleç `syncKey` ile eşlenir.
- **S10 — "tüm servisler" = HTTP stable semconv ailesini yayan servisler.** Aileler asla toplanmaz (`api/red_family.go:366-385`); Micrometer ve gRPC servisleri grafikte yoktur. Kart etiketi "tüm HTTP servisleri".
- **S11 — 1100 px altında canvas da yok.** İstek yalnız sağ sütunu söylüyordu; dar ekranda özet çağrısı, grafik chunk'ı ve canvas hiç yüklenmez.

## 3. Mevcut durum

**Login envanteri** (`pages/Login.tsx`, 318 satır; hepsi korunur):
1. `GET /api/auth/config` (`:31`); hata sessiz, yerel form çıkar (`:44-46`). `local.enabled` okunmuyor, form hep çizilir.
2. Demo kipi: alanları doldurur ve otomatik giriş yapar (`:30-43`); "Demo mode" bandı (`:193-204`). Sunucu demo e-posta + parolayı açık döndürür (`api/api.go:5770-5776`).
3. SSO düğmesi `oidc.enabled` ise (`:206-212`), etiket `oidc.displayName` (boşsa "SSO", `:73`), hedef `oidcStartHref()` (`lib/postLoginRedirect.ts:88-93`), ardından ayırıcı (`:213-220`).
4. LDAP bandı + "kullanıcı adı veya e-posta" etiketi (`:224-237`); alan bilerek `type="text"` (`:239-243`).
5. Form: `autoFocus`, `autoComplete`, `required`, e-posta `trim()` (`:61`), `busy` iken düğme kapalı.
6. Hata paneli: `?error=` SSO hatası (`:49-55`), 401 → `login.invalid` + ipucu (`:62-66, 255-271`).
7. Başarı sonrası yönlendirme Login'de değil `components/AuthProvider.tsx:169-171`'de; dönüş hedefi `sessionStorage` (`postLoginRedirect.ts:13`).
8. Marka: `appName`, `loginTitle`, `loginSubtitle`, `signInButtonLabel`, `usernameLabel`, `footerText`, `logoDataUri` (`:137-190, 280-287`).
9. Sürüm satırı: `GET /api/version` (`:98`), boşsa çizilmez (`:296-314`). Uç zaten kimliksiz (`internal/auth/auth.go:278`).
10. Tema seçici sağ üstte (`:126-128`); dil seçici Login'de yok.

Kaynak pinleri (2. dilimde güncellenir): `styles/publicBackground.test.ts:43-68`, `lib/postLoginRedirect.test.ts:166-170`, `pages/k5PagesAM.pin.test.ts:76, 133-137`, `frontend/eslint-suppressions.json:237-241`.

**Arka uç olguları.** Kimliksiz muafiyet tek yerde: `auth.SkipPath` (`internal/auth/auth.go:263-337`); `/api/public/` genel öneki muaf değil, yalnız `/api/public/trace/` (`:311`) ve `/api/public/notify/` (`:314-317`). `/api/health` zaten kimliksiz (`auth.go:276`, kayıt `api/api.go:928`) ve kuyruk, düşen kayıt, rol, spool ayrıntısı döndürür (`api/health.go:58-130`). Login sınırlayıcısı yok (`api/auth_permissions.go:42-46`). i18n var: `lib/i18n.ts` (`useT` `:1260`, `setUserLang` `:1221`, anahtar `coremetry.lang` `:1188`). Tema giriş öncesi uygulanıyor (`frontend/index.html:42-64`); altı tema kayıtlı (`lib/themeRegistry.ts:57-68`).

## 4. Uç sözleşmesi ve sızıntı tablosu

Cevap (alan adları operatörün verdiği gibi): `enabled`, `ready`, `generated_at`, `spans_today`, `traces_today`, `services_monitored`, `http_24h{from,to,step_s,status[],avg,p95?,summary{now_rps,peak_rps,peak_at,avg_now_s,p95_now_s?}}`, `activity_24h{rollouts_tracked?,logins,cosre_questions,traces_viewed}`, `topology{window_s,nodes[{id,tier,weight,name?}],edges[{from,to,weight}]}`, `components{state,checked_at,clickhouse_uncompressed_bytes?}`. Özet kapalıysa `{"enabled":false}`; anlık görüntü yok ya da 15 dk'dan eskiyse `{"enabled":true,"ready":false}`. Seriler `SpanMetricSeries` biçimindedir (`lib/types.ts:4584-4593`, zaman unix ns).

| Alan | Kaynak | Kimliksiz birine ne söyler | Hüküm |
|---|---|---|---|
| `spans_today` | `service_summary_5m.span_count_state` (`chstore/store.go:1225-1241`) | Günlük telemetri hacmi | Döner |
| `traces_today` | `trace_summary_1d.trace_count_state` (`store.go:1390-1400`) | Günlük trace hacmi (yaklaşık, `uniq`) | Döner |
| `services_monitored` | `service_summary_5m`, `uniq(service_name)` (emsal `chstore/sysstats.go:637-642`) | Servis sayısı | Döner |
| `http_24h.status[]` | VM Q1 (§5.3); etiket değeri `^[1-5][0-9]{2}$` değilse atılır, en çok 8 seri | Toplam istek hızı ve kod dağılımı; 5xx payı görünür | Döner (operatör isteği; §9-10) |
| `http_24h.avg`, `.summary` | VM Q2 / ΣQ1; halkadan türetilir | Toplam yanıt süresi, tepe saati | Döner |
| `http_24h.p95` | VM Q3 | Aynı | Ayara bağlı (`AllowUnfilteredPercentiles`) |
| `activity_24h.logins` | Pod sayaçları (§5.2) | Günlük giriş adedi; kullanıcı yok | Döner |
| `activity_24h.traces_viewed` | Pod sayaçları | Kullanım yoğunluğu | Döner |
| `activity_24h.cosre_questions` | `cosre_interactions` satır sayısı (`store.go:2978-3020`) | Yalnız adet; soru metni, kullanıcı yok | Döner |
| `activity_24h.rollouts_tracked` | `RolloutStats(...).Total` (`chstore/rollouts.go:590`) | Günlük dağıtım adedi; iş yükü adı yok | Döner; rollout kapalıysa alan yok |
| `topology.nodes[].id` | `hex(sha256(tuz‖ad))[:10]`; tuz 16 rastgele bayt, anlık görüntüde taşınır | Hiçbir şey (tuzsuz özet sözlükle çözülürdü) | Döner |
| `nodes[].tier` | Saf kural (§5.4) | Mimari şekil: giriş, BFF, veri katmanı sayıları | Döner |
| `nodes[].weight`, `edges[].weight` | `log1p(çağrı)/log1p(en büyük)`, 2 hane | Göreli yoğunluk; mutlak çağrı sayısı **yok** | Döner (normalize) |
| `edges[].from/to` | Seçilen düğümler arası kenarlar | Bağımlılık grafiğinin şekli | Döner |
| `nodes[].name` | Servis adı; db/kuyrukta yalnız sistem adı ("redis", "kafka") | Servis adları | Yalnız "isimli" kipte |
| `components.state`, `.checked_at` | Tazeleyicinin kendi turu (§5.5) | Sistemin ayakta olduğu; ayrıntı yok | Döner |
| `components.clickhouse_uncompressed_bytes` | `system.parts` toplamı (§5.5) | Saklanan verinin büyüklüğü | Döner (operatör kararı) |
| Shard × replika, rol listesi | `system.clusters` (`api/clickhouse_health.go:249-255`) | Altyapı yerleşimi | **Dönmez** |
| Cluster sayısı (mockup sol alt) | `topology_edges_5m.cluster` (probe'lu kolon, `store.go:4811`) | Altyapı büyüklüğü | **Dönmez** (istekte yok) |
| db instance, host, topic, `ext:` düğümleri, namespace, route | `child_node` önekleri (`chstore/identity.go:90-95`) | İç adlandırma | **Dönmez**, "isimli" kipte de |
| Kullanıcı, e-posta, hata metni, Problem, anomali rengi | — | — | **Dönmez** |
| Sürüm | Yeni uçta yok; `GET /api/version` bugünkü gibi (`api/api.go:9503-9526`) | Sürüm (bugün de açık) | Değişmez |

Tek boğum: `Project(snapshot, flags)` (Y) saf fonksiyonu. Anlık görüntü adları taşır; cevap bu fonksiyondan geçmeden yazılmaz. Böylece ayar değişimi yeniden kurulum beklemez (≤30 sn).

## 5. Veri kaynakları

### 5.1 ClickHouse sayıları
`chstore/login_summary.go` (Y), `LoginSummaryCounts(ctx)`; `s.telemetryReadConn()` (`store.go:1082`), tablo adları niteliksiz:
- `SELECT countMerge(span_count_state) FROM service_summary_5m WHERE time_bucket >= toStartOfDay(now()) LIMIT 1 SETTINGS max_execution_time = 5` (emsal `sysstats.go:589-595`). Tüm span'ler sayılır, giriş-span süzgeci yok (`store.go:1234`).
- `SELECT uniqMerge(trace_count_state) FROM trace_summary_1d WHERE day = today() LIMIT 1 SETTINGS max_execution_time = 5` (emsal `sysstats.go:668-672`).
- `SELECT uniq(service_name) FROM service_summary_5m WHERE time_bucket >= now() - toIntervalDay(1) AND NOT startsWith(service_name, 'coremetry-') LIMIT 1 SETTINGS max_execution_time = 8`.

Reddedilen: son 24 saat tekil trace. `trace_summary_5m`'de `uniqState` yok; `uniqExact(trace_id)` 1 saatte yaklaşık 3.3M grup ve 25 sn tavanla çalışıyor (`chstore/trace_root_coverage.go:31-45`).

### 5.2 Aktivite
Self-obs gerçeği: kök örnekleme `TraceIDRatioBased(0.1)` (`selfobs.go:300-304`), `api`/`worker` kipinde uç boşsa trace kapalı (`selfobs.go:163-168`); server span'de yalnız 5xx hata sayılır, 401 girişi başarıdan ayrılmaz; kullanıcı kimliği span'e yazılmıyor; span adı `METHOD collapseRoute(path)` (`api/http_root.go:28`). Sonuç: `operation_summary_5m` toplam çağrıyı verir ama başarıyı vermez, 10 dk tekilleştirme için ham `spans` + olmayan bir attribute gerekir (invariant #3 ihlali ve %10 örnek).

| Sayı | Yol | Not |
|---|---|---|
| `logins` | `TouchUserLogin` yalnız iki başarılı giriş yolunda çağrılıyor: yerel+LDAP `api/api.go:5327`, OIDC `api/api.go:5886`. Bu iki üç satırlık blok tek satır `s.noteLogin(...)` (Y) olur; yardımcı damgayı basar ve sayacı artırır. | `api.go` 4 satır kısalır; `.claude/baselines/api_go_lines` düşürülür. |
| `traces_viewed` | `getTrace` (`api/trace_routes.go:30-49`) içinde: `X-Coremetry-Purpose: prefetch` başlığı yoksa `(kullanıcı, trace)` 10 dk pod-yerel tekilleştirme (en çok 20 000 girdi) sonrası sayaç. | Hover ön-yüklemesi (`pages/Traces.tsx:1072-1078, 1765`) başlığı gönderir. Trace sayfası React Query kullanmıyor, her açılışta yeniden ister (`pages/Trace.tsx:210-231`), yani eksik sayım yok. CoSRE/MCP aracı HTTP çağırmaz (`internal/mcptools/trace_tools.go:636-666`), kendiliğinden hariç. |
| `cosre_questions` | `SELECT count() FROM cosre_interactions WHERE created_at >= now() - INTERVAL 24 HOUR AND tier != 'explain' LIMIT 1 SETTINGS max_execution_time = 5` | Her satır bir sohbet turu, tek yazıcı `api/copilot_chat.go:362`; `explain` = ✨ düğmesi (`internal/ai/aisurface/aisurface.go:214-239`). |
| `rollouts_tracked` | `RolloutStats` / `RolloutV2Stats` (`chstore/rollouts.go:590`, `chstore/rollout_v2_read.go:301`), pencere 24 sa, `topN=0`, `.Total` | Kaynak ve namespace dışlaması `GET /api/rollouts/stats` ile aynı (`api/rollouts.go:237-277`). |

Sayaç deseni ES istek sayacının eşi: pod-yerel dakika hücreleri (`internal/logstore/es_meter.go:38-59`), 30 sn'de `InsertMetrics` ile `metric_points`'e yazım (`api/es_request_meter.go:49-83`, `chstore/repo.go:265`), okuma tüm pod toplamı (`chstore/self_es_series.go:89-107`). Yeni seri `coremetry.self.activity`, etiket `kind=login|trace_view`, `pod`. Lider kilidi yok; başlatma `main.go:977-978` kalıbı. Önerilen span attribute'u (yalnız dogfood için, sayımda kullanılmaz): `coremetry.request.purpose=prefetch`. Hata payı: aynı kullanıcı 10 dk içinde iki pod'a düşerse çift sayılır.

### 5.3 VictoriaMetrics
Aile: `http_server_request_duration_seconds_{count,sum,bucket}`, status etiketi `http_response_status_code` (`lib/redFamily.ts:49-62`; varsayılan ad `chstore/job_service.go:87`). Sorgular Go'da sabit:
- Q1 `sum by (http_response_status_code) (rate(<aile>_count[5m]))`
- Q2 `sum(rate(<aile>_sum[5m]))`; avg = Q2 / ΣQ1 Go'da hesaplanır (ayrı `_count` taraması yok)
- Q3 `histogram_quantile(0.95, sum by (le, vmrange) (rate(<aile>_bucket[5m])))` (biçim emsali `vmetrics/promql.go:759`)

Çağrı `QueryPromQLRange(ctx, q, from, to, 300, 0)` (`vmetrics/histogram.go:347`). Maliyet seri sayısıyla ölçeklenir; Q3, Q1'in kova sayısı katıdır. Emsal olay: servissiz altı aralık sorgusu `histogram_quantile` ile süre aşımına düştü (`docs/INCIDENTS.md:271-283`). Sınırlama yapıyla sağlanır, ölçümle değil:
- **Artımlı halka.** 288 noktalık halka anlık görüntüde durur. Her 5 dk'da yalnız `[şimdi−15 dk, şimdi]` sorgulanır ve hizalı zaman damgasına göre birleştirilir.
- **Boşluk doldurma.** Tur başına en çok 4 adet 1 saatlik parça, sırayla; soğuk başlangıç 30 dk'da dolar.
- **Tavan.** Sorgu başına 5 sn `ctx`; aynı sorgu art arda 3 kez düşerse 30 dk atlanır.
- **Tek kaynak.** Yalnız lider sorgular; istek sayısından bağımsız.

Aile yoksa (Q1 boş) ya da VM kapalıysa (`Configured()` `vmetrics/client.go:376`) `http_24h` hiç dönmez, kart gizlenir. VM istek ölçeri yok (ES'teki `es_request_meter.go` karşılığı bulunamadı); süreler `[login-summary]` log satırına yazılır.

### 5.4 Topoloji
Okuma: `ReadServiceTopologyAgg(ctx, şimdi−15 dk, şimdi, 5000)` (`chstore/topology.go:1284, 1384-1425`): `FINAL`, `sum(calls)`, `max_execution_time = 10`, `node:` kenarları süzülü (`identity.go:85`). 5 dk yerine 15 dk: son kova geç yazılırsa siluet boşalmasın. Seçim `PickSilhouette(edges, 180)` (Y, saf):
1. `ext:` çocuklu kenarlar atılır.
2. Servis skoru = servis↔servis kenarlarında giren + çıkan çağrı (`api/topology.go:809` kuralı); ilk 150 servis.
3. Bu servislere bağlı `db:` / `queue:` düğümlerinden çağrıya göre ilk 30. Mevcut top-N bunları düşürdüğü için yeniden kullanılamıyor.
4. İki ucu da seçili kenarlardan ağırlığa göre ilk 360.

`tier` için mevcut sınıflandırma yok (`internal/topology/`, `api/topology.go`, `chstore/topology.go` tarandı). Kural `TierOf` (Y, saf), sırayla: `db:`/`queue:` → `db`; ad `(^|[-_.])(gw|gateway|ingress|edge|apigw)([-_.]|$)` → `gw`; ad `(^|[-_.])bff([-_.]|$)` → `bff`; servis çağıranı yok ve çıkış ≥ 3 → `gw`; diğer → `svc`. Kural adı sunucuda kullanır, yalnız `tier` çıkar.

### 5.5 `components`
- `state`: tazeleyicinin son turunda CH sayıları başarılı ve (VM kapalı ya da Q1 başarılı) ise `ok`, değilse `degraded`. `/api/health` pod-yerel kuyruk durumudur (`api/api.go:9302`), filo özeti değildir; bu yüzden kopyalanmaz.
- `clickhouse_uncompressed_bytes`: hazır toplam yok. `GetSystemStats` tablo başına döner, MV iç tablolarını dışlar ve 8'den fazla sorgu koşar (`chstore/sysstats.go:444, 473-493`). Yeni tek sorgu `TotalUncompressedBytes(ctx)` (Y): `SELECT sum(data_uncompressed_bytes) FROM <kaynak> WHERE database = currentDatabase() AND active = 1 LIMIT 1 SETTINGS max_execution_time = 8`, `s.conn`.
- `<kaynak>` mevcut kuralla aynı: küme kipinde `cluster('<ad>', system.parts)`, değilse `system.parts` (`sysstats.go:473-475`). `cluster()` shard başına tek replika okur, çift saymaz; `clusterAllReplicas` çift sayardı (`sysstats.go:521-524`).
- MV iç tabloları **dahildir** ("toplam veri"); Admin → Stats tablosunun toplamından büyük çıkar.
- Tazeleyicide 5 dk'da bir koşar. Hata olursa alan dönmez, satır boyut göstermez. İstemci `fmtBytes` ile biçimler (`lib/utils.ts:132`).

### 5.6 Tazeleme, önbellek, hız sınırı
- Kurucu: `internal/loginsummary` (Y) `Builder`; `if mode.worker` bloğunda (`main.go:705-757`, `topology.New` `:731` yanında), `cache.NewLeaderHolder(lockImpl, "login-summary", cache.LeaderTTL(time.Minute))` (kalıp `main.go:1243`).
- Yayın: `api.PublishLoginSummary` (Y) belleğe yazar ve `cache.Set("coremetry:login-summary:v1", blob, 48h)`. Çekme: her pod 30 sn'de `cache.Get`. Redis yoksa `noopCache` hep ıska döner (`internal/cache/cache.go:101-150`); `all` kipinde kurucu ile uç aynı süreçte olduğundan çalışır, Redis'siz çok pod'da `ready:false`.
- Bütçe: uç bellekten okur; sıcak ve soğukta arka uç çağrısı yok. Cevap `Cache-Control: max-age=30`.
- Hız sınırı: mevcut sınırlayıcılar kullanıcı anahtarlı ve pod-yerel (`api/chat_source_rate.go:54-75`, `api/mcp_gate.go:35-40`), kimliksiz uca uymaz. IP emsali `allowSubscribeRate` 60 sn'de 1 ve sahtelenebilir `clientIP` kullanır (`api/api.go:7362-7381`, `api/anomaly_extra.go:367-381`). Genel güvenilir-proxy ayarı yok (`internal/config/config.go:252-270` yalnız trusted-header girişi için).
- Karar: `ipWindowLimiter` (Y, `api/ip_rate.go`), `mcpRateBucket` biçiminde sabit pencere; anahtar `r.RemoteAddr` (XFF'e güvenilmez), 600 istek/dk/anahtar/pod, aşımda 429 + `Retry-After: 30`. Ingress arkasında anahtar ingress IP'sidir, yani sınır pod başına genel bir tavandır; uç O(1) olduğu için yeterli. N pod'da tavan N × 600.

### 5.7 Ayarlar
`chstore.BrandingSettings` (`chstore/branding.go:22-52`) alanları (Y): `orgName` (kırp, ≤ 60 rune), `loginSummary` (`on|off`), `loginQuote` (`on|off`), `loginTopology` (`off|anonymous|named`). Doğrulama `NormalizeBranding` içinde `OnboardingTour` kalıbıyla (`branding.go:82-87`); boş değer varsayılan demektir: özet `on`, söz `off` (operatör kararı), topoloji `anonymous`. Anonim uç ayarı bellekten okur: `cfgRefresh.Add("login-summary", ...)` (`chstore/settings_refresh.go:117`; her rolde çalışır, `main.go:1515`), gecikme ≤ 30 sn. Denetim: `settings.branding.update` zaten yazılıyor (`api/api.go:9499`) ama ayrıntıda yeni alanlar yok ve `api.go` büyüyemez; 4. dilim `getBranding`/`putBranding`'i `api/branding.go`'ya (Y) taşır ve ayrıntıya dört alanı ekler. Kart başlığı `orgName`'den gelir; mevcut bir kurum adı ayarı yok (`appName` ürün adıdır).

## 6. Frontend

### 6.1 Bileşen ağacı
- `pages/Login.tsx`: kök + form (pinler bu yolu okur). Primitifler `shadcn/button`, `input`, `label`, `separator`, `skeleton`; `components/ui` içe aktarılmaz (`styles/uiImportRatchet.test.ts`). Satır içi stil yok: bugünkü 30 `style={{` silinir, `inlineStyleRatchet` `TOTAL` düşürülür.
- `pages/login/LoginTopBar.tsx` (Y): sol `Wordmark` (`components/Wordmark.tsx:5`, "Core" `--brand`) ya da `logoDataUri`; sağ TR · EN (`coremetry/SegmentedControl`, `setUserLang`) + `ThemeToggle`. Mevcut `LangToggle` tek düğmedir ve öbür dili gösterir (`components/LangToggle.tsx:13-27`), mockup'a uymaz.
- `pages/login/LoginSummary.tsx` (Y, lazy): başlık, 3 KPI (düz `div`, tablo değil), `LoginHttpCard`, "Son 24 saatte" satırı, bileşen satırı.
- `pages/login/LoginHttpCard.tsx` (Y, lazy) ve `pages/login/TopologyBackdrop.tsx` (Y, lazy).
- `lib/loginSummary.ts` (Y, saf): `fmtMillions` (`1,214M`, `48.2M`, `2,148`), lejant, satır metinleri. `lib/queries/loginSummary.ts` (Y): `refetchInterval: 60_000`, `retry: false`.
- Yerleşim: kart `min(1120px, 100vw − 48px)`, ızgara `480px 1fr`, ≥ 1100 px. Altında tek sütun `min(340px, 100%)` (bugünkü değer). Koyu şemada kart %86 opak + `blur(6px)`, açıkta düz; arkada radyal perde (`--bg0`, merkez %10 → kenar %88). İki sınıf `styles/globals.css`'e: `.login-card`, `.login-veil`. Bugünkü 64 px teleskop kutusu (`Login.tsx:167-179`) mockup'ta yok, karttan kalkar; `logoDataUri` görseli kalır.

### 6.2 Grafik kartı
`CorePanelMulti` veri çekmez, hazır dizi alır; router, React Query ve auth'a bağlı değil (`components/chart/corePanelEntry.tsx:58-92`, `CorePanel.tsx:30-77`). Eksikler: ⋯ menüsü koşulsuz çizilir (`CorePanel.tsx:1204-1207`), kök hep `.card` çerçevesi taşır (`:1168`), sürükle-seçimi kapatan ayar yok (`:831-834`; `onZoom` yokken seçim kutusu büyük olasılıkla yine çizilir, `:878-892`). Tek ek prop: **`embedded?: boolean`** (Y) → çerçevesiz kök, üst satır yok, `cursor.drag` kapalı; tooltip ve imleç eşlemesi kalır. Region bandı verilmediği için çizilmez.

Kullanım: iki panel, `title=""`, `hideLegend`, `height={96}`, `storageKey="login-tput"` / `"login-rt"`, `syncKey={msSyncKey('login-http')}` (`lib/chart/syncNamespace.ts:22`), `xRange`, `unit="reqps"` / `"s"`. Throughput kalemleri `statusCodeSeries` + sınıf token'ı (`lib/httpMetrics.ts:356-359, 431-445`; kalıp `Overview.tsx:1259-1260`). Aynı sınıftaki kodlar aynı renktedir; mockup'taki 200/201 ton farkı yoktur. avg `--info`, p95 `--purple` (kayıtlı p95 rengi `--orange` 4xx ile çakışırdı). Lejant kartın altında tek, statik satır. Başlık satırları: "Throughput · metrik" sağında `şimdi X req/s · tepe Y (HH:mm)`; "Response time · metrik" sağında `ort A · p95 B`. Seri 2 noktadan azsa kart hiç çizilmez (CorePanel'in boş durum metinleri sabit Türkçe, `CorePanel.tsx:1357-1374`).

Yük: Login zaten lazy (`frontend/src/App.tsx:19`), ilk yük bütçesine (347 KB gz, `frontend/scripts/bundle-budget.mjs:25`) girmez. Grafik chunk'ı (@grafana, yaklaşık 178 KB gz, `frontend/vite.config.ts:73, 88`) yalnız `http_24h` geldikten sonra `lazy(() => import('@/components/chart/corePanelEntry'))` ile yüklenir (kalıp `Overview.tsx:50-51`); form önce boyanır.

### 6.3 Canvas topoloji
Repoda force yerleşimi yok; `dagre` bağımlılığı var ama içe aktarılmıyor. Canvas + rAF + görünürlük + reduced-motion'ı birleştiren emsal de yok; parçalar ayrı (`components/traces/LatencyScatter.tsx:96-123` DPR ve token rengi, `components/ai/useTypewriter.ts:30-38, 77-105` rAF ve reduced-motion).
- **Saf çekirdek** `lib/loginTopology/` (Y): `initLayout(nodes, edges, w, h, seed)` + `stepLayout(state)` (itme O(n²), kenar yayı, merkez çekimi; tohumlu PRNG), `syntheticGraph(seed)`, `shouldDraw(now, last)`. Yerleşim boşta 200 adımda bir kez çözülür; sonrası simülasyon değil, düğüm başına sinüs kayması ve kenar başına akan noktadır (en çok 120 kenar).
- **Çizim katmanı**: 30 fps tavanı (33 ms altı kare atlanır), DPR `min(dpr, 2)`, `ResizeObserver`; `document.hidden`'da rAF durur; `prefers-reduced-motion`'da tek durağan kare; renkler `resolveVar` + `useThemeTick` (`lib/chart/resolveVar.ts:10`, `lib/useThemeTick.ts:33`; kalıp `CorePanel.tsx:340, 748`), yazı `chartMonoFont(11)` (`resolveVar.ts:25`). `aria-hidden`, `pointer-events: none`.
- **Görünüm** (mockup; SVG gövdesi okunmadı, ölçüler koordinatörün): gateway r 9–12 + halka, BFF 5–6, servis 3–4, yaprak 2.2; db/kuyruk 6 px `--purple` yuvarlak kare; servis rengi `--accent2`; etiket opaklığı gateway/BFF 0.75, servis 0.55, en çok 70 etiket.
- **Okunurluk**: perde + opak kart. Yüksek Kontrast temasında (`themeEntry(currentTheme()).contrast === 'aaa'`, `themeRegistry.ts:79`) canvas çizilmez.
- **Sentetik graf**: ayar `off` değil ama `topology` yoksa. Sol alt not yalnız gerçek veride yazılır: "Canlı topoloji silueti · N servis · son 15 dk · ayrıntılar giriş sonrasında".

### 6.4 Dil, tema, düşüş
- TR/EN login'deki **tüm** metni çevirir; mockup'taki karışık dil üretilmez. Mevcut `login.*` anahtarları korunur (`lib/i18n.ts:125-134, 755-764`); bugün sabit İngilizce olan demo bandı, LDAP bandı, SSO ve giriş hataları anahtara bağlanır. Değişmeyenler: marka adı, söz, "an OpenTelemetry-native platform". Parametre desteği yok; `.replace('{n}', …)` kalıbı. Marka ayarları i18n'i ezmeye devam eder.
- Tema: renk yalnız token; dokuz token altı temada da tanımlı (`styles/globals.css:20-634`).
- Düşüş: ayar kapalı, hata, 429, `ready:false` ya da < 1100 px → sağ sütun ve canvas yok, tek sütun kart. Bölüm eksikse yalnız o blok gizlenir. Yenileme 60 sn, sekme gizliyken durur (React Query varsayılanı); dosya `lib/pollingBudget.pin.test.ts:38-65` listesine eklenir.

## 7. Mockup ↔ metin farkları ve hükümler

1. "47 kişi giriş yaptı" → **"47 giriş yapıldı"** (toplam). Tekil kişi sayısı `users.last_login_at`'ten bedava okunurdu; metin geçerli olduğu için kullanılmaz.
2. Kehribar halkalı üç düğüm → **yok**. Giriş öncesi durum rengi çizilmez, süs olarak da kullanılmaz.
3. Sol alt not: cluster sayısı dönmez; "son 5 dk" → "son 15 dk" (§5.4).
4. Bileşen satırı: "ClickHouse 2×2" yerine sıkıştırılmamış boyut (operatör kararı); rol listesi dönmez. Metin: "Tüm bileşenler çalışıyor · ClickHouse 4.20 TiB · son kontrol 20:34"; `degraded` iken "Bazı bileşenler sorunlu · ayrıntı giriş sonrasında".
5. Alt başlık kipe göre: isimli → "Anonim özet · hatalar ve ayrıntılar giriş sonrasında"; diğer → "Anonim özet · servis adları ve hatalar giriş sonrasında".
6. Tepe ve anlık req/s: Throughput başlık satırının sağında (§6.2).
7. Karışık dil: §6.4.
8. SSO etiketi `oidc.displayName`'den (`api/api.go:5765-5768`): yeni anahtar `login.ssoLabel` = "{name} hesabıyla giriş (SSO)" / "Sign in with {name} (SSO)"; ad boşsa "SSO ile giriş". Mockup'taki emoji kullanılmaz, bugünkü `⚿` kalır. Kart başlığı "{orgName} OpenTelemetry metrikleri · son 24 saat"; `orgName` boşsa ad düşer.
9. İlk KPI'daki "canlı" etiketi: anlık görüntü 3 dk'dan tazeyse.

## 8. Dilim planı

Sıra operatörün. Hepsi mekanik (`implementer`); tek istisna 5.1'in içeriği. Sürüm numarasını ana oturum atar. Her Go dilimi: `scripts/gate.sh go` + `golangci-lint`; her FE dilimi: `scripts/gate.sh fe`.

| Dilim | Dosyalar | İçerik | Test | Kabul | Tahmin | Bağ. |
|---|---|---|---|---|---|---|
| 1.1 | `chstore/login_summary.go` (Y), `chstore/self_activity_series.go` (Y) | §5.1 üç sayı, `TotalUncompressedBytes`, `CountCosreQuestions`, sayaç yazım/okuma | SQL metni testi: `max_execution_time`, `LIMIT`, ` FROM spans` yok, küme kipinde `cluster(`; `conn_strategy_test.go` | Sorgular birim testte derlenir; ham `spans` yok | 3 sa | — |
| 1.2 | `api/activity_meter.go` (Y), `api/api.go` (2 blok → 2 satır), `api/trace_routes.go`, `lib/api.ts`, `pages/Traces.tsx` | §5.2 sayaç, `noteLogin`, trace görüntüleme tekilleştirme, ön-yükleme başlığı | Tablo testi: 10 dk penceresi, 20 000 tavanı, başlık varken sayılmaz; `TestApiGoDoesNotGrow` + taban düşer | Giriş ve trace açma sayaç üretir; hover üretmez | 4 sa | 1.1 |
| 1.3 | `internal/loginsummary/` (Y): `types.go`, `topology.go`, `ring.go`, `project.go`, `builder.go` | `PickSilhouette`, `TierOf`, düğüm id, halka birleştirme, özet, `Project`, tur döngüsü, devre kesici | Tablo testleri + sızıntı: isimsiz çıktının baytlarında girdi adlarından hiçbiri geçmez; `ext:` ve db instance hiçbir kipte geçmez; cevap anahtarları izin listesinin alt kümesi; status etiketi süzgeci | §4 tablosu testle çivili | 7 sa | 1.1 |
| 1.4 | `chstore/branding.go`, `main.go` | Dört alan + `NormalizeBranding`; `cfgRefresh.Add("login-summary", …)` | Normalize tablo testi (geçersiz → boş) | Varsayılan: on / on / anonymous | 1 sa | — |
| 1.5 | `api/login_summary.go` (Y), `api/ip_rate.go` (Y), `internal/auth/auth.go` (+1 blok, `:286` kalıbı), `main.go` | Rota (`registerLoginSummaryRoutes`, `init` kaydı), uç, sınırlayıcı, yayın/çekme, kurucu başlatma | `SkipPath` testi (GET evet; POST ve alt yol hayır; emsal `api/auth_session_test.go:172`); sınırlayıcı saat enjekte tablo testi; `TestMuxRoutePatterns` | Kimliksiz 200; kapalıyken `{"enabled":false}`; uç CH/VM çağırmaz | 4 sa | 1.2–1.4 |
| 2.1 | `pages/Login.tsx`, `pages/login/LoginTopBar.tsx` (Y), `lib/i18n.ts`, `styles/globals.css`, §3'teki 4 pin dosyası | §3 envanteri bire bir, shadcn + Tailwind, üst çubuk, i18n anahtarları | Pinler yeni sınıf dizgilerine çevrilir (`--bg0`, `min-h-dvh`, `min(340px,100%)`, `oidcStartHref()` satırı kalır); `Login.test.tsx` (Y): demo, LDAP, SSO, hata dalları | Envanterin 10 maddesi çalışır; `ui/no-raw-element` bastırması silinir; `TOTAL` düşer | 5 sa | — |
| 2.2 | `pages/login/LoginSummary.tsx` (Y), `lib/loginSummary.ts` (Y), `lib/queries/loginSummary.ts` (Y), `lib/types.ts`, `lib/api.ts`, `lib/queries/index.ts`, `lib/branding.ts` | Sağ sütun, KPI, aktivite ve bileşen satırı, iskelet, düşüş | Saf testler: `fmtMillions`, satır metinleri (eksik alan düşer), düşüş kararı; `pollingBudget.pin` listesi | Özet kapalı/hatalıyken tek sütun; < 1100 px'te çağrı yok | 4 sa | 1.5, 2.1 |
| 2.3 | `components/chart/CorePanel.tsx`, `pages/login/LoginHttpCard.tsx` (Y) | `embedded` prop'u; iki panel + lejant | `corePanelContracts.test.ts`, `CorePanel.tooltipSync.pin.test.ts`, `legendScroll.pin.test.ts`, `CorePanel.smoke.test.tsx` yeşil (pin kayarsa güncellenir); `embedded` smoke | Varsayılan davranış değişmez; `bundle:check` ilk yük artmaz | 4 sa | 2.2 |
| 3.1 | `lib/loginTopology/{layout,synthetic,frame}.ts` (Y) | Saf çekirdek | Aynı tohum → aynı yerleşim; düğümler sınır içinde; `shouldDraw` 30 fps | 180 düğümde deterministik | 4 sa | — |
| 3.2 | `pages/login/TopologyBackdrop.tsx` (Y), `pages/Login.tsx`, `styles/globals.css` | Çizim katmanı, perde, sol alt not | Kip kararı saf testi (off / sentetik / gerçek / kontrast teması / reduced-motion) | Gizli sekmede rAF yok; `off` iken canvas yok | 4 sa | 2.2, 3.1 |
| 4 | `api/branding.go` (Y; iki uç `api/api.go:948-949, 9464-9501`'den taşınır), `pages/settings/BrandingTab.tsx` | Taşıma + denetim ayrıntısına dört alan; "Login sayfası" bölümü (3 `SelectField` + `orgName`) | `TestApiGoDoesNotGrow` taban düşer; `TestMuxRoutePatterns` | Değişiklik ≤ 30 sn'de login'e yansır; denetim kaydında dört alan | 3 sa | 1.4 |
| 5.1 | `frontend/src/data/quotes.json` (Y) | 60 söz: `text`, `author`, `source`, `attributed`. **İçeriği ana oturum yazar** (atıf doğruluğu yargı ister) | Şema testi: 60 kayıt, yinelenme yok, `text` ≤ 140, `source` dolu | Şarkı sözü/şiir yok; birincil kaynağı olmayan "attributed to …" | 2 sa | — |
| 5.2 | `pages/login/QuoteOfDay.tsx` (Y), `lib/loginSummary.ts` | `pickQuote(date, len)`; JSON dinamik `import()` | `pickQuote` tablo testi (yıl sınırı, artık yıl) | `loginQuote=off` iken çizilmez | 1 sa | 2.1, 5.1 |

Ship checklist (`.claude/skills/release/SKILL.md:192-200`): 1 MV okuması ✔ (1.1) · 2 `serveCached` uygulanmaz (S1) · 3 kimliksiz, `SkipPath` testiyle (1.5) · 4 denetim (4) · 5 `system_settings` `branding` (1.4) · 6–7 tip + istemci (2.2) · 8 yükleme/hata/boş (2.2) · 9–10 kapılar · 11 hata düzeltmesi değil.

## 9. Açık sorular ve varsayılanlar

| # | Soru | Varsayılan (bununla ilerlenir) | Öbür seçenek |
|---|---|---|---|
| 1 | p95 süzgeçsiz taransın mı? | Hayır; yalnız `AllowUnfilteredPercentiles` açıksa | Login'e özel istisna (artımlı halka yükü sınırlar, ama ölçülmedi) |
| 2 | Özet varsayılan açık mı? | Açık; topoloji **isimsiz** | Yükseltmede kapalı gelsin |
| 3 | `logins` toplam mı, tekil kişi mi? | Toplam (metin) | Tekil kişi: sayaç gerekmez, `users.last_login_at` |
| 4 | "Bugün" hangi gün? | ClickHouse günü | Son 24 saat (trace sayısı için pahalı) |
| 5 | Cluster sayısı notta olsun mu? | Olmasın | `uniq(cluster)` eklenir |
| 6 | Boyut birimi | `fmtBytes` → "4.20 TiB" (Admin ile aynı) | 1000 tabanlı "4.2 TB" |
| 7 | Dar ekranda canvas | Yok | Yalnız durağan kare |
| 8 | Seri başına dolgu, ortak x ekseni | Yok (S8, S9) | CorePanel'e iki prop daha |
| 9 | Sayaç deposu | `metric_points` (ES sayacı emsali) | Redis `Set` + `ScanPrefix` (`internal/cache/cache.go:21-81`) |
| 10 | 5xx serisi giriş öncesi görünsün mü? | Evet (operatör metni status kodu başına seri istiyor) | Yalnız 2xx/3xx + toplam |

## 10. Doğrulanmadı / ölçülmedi

- VM sorgularının seri sayısı ve süresi; Q3'ün gerçek maliyeti. İlk prod turunda `[login-summary]` log sürelerine bakılmalı.
- ClickHouse sunucu saat dilimi (`today()` hangi saatte döner); `COREMETRY_TZ` ile aynı mı.
- Prod'da `ClusterName` dolu mu; boşsa boyut yalnız bağlı shard'ı verir (`sysstats.go:466-470`). `cluster()` replika seçimi `load_balancing`'e bağlı.
- Prod'da `metric_points`'e yazım ve TTL (varsayılan 7 gün, `store.go:3566`).
- `topology_edges_5m FINAL` maliyeti 15 dk pencerede; `system.parts` küme taraması süresi.
- `onZoom` yokken uPlot seçim kutusu; 96 px panel yüksekliği; iç içe kart görünümü; canvas kare süresi. Tarayıcıda bakılmadı.
- `main.go`'da `rolloutCfg`'ye worker bloğundan erişim; bulunamazsa `rollouts_tracked` dönmez.
- CORS izinli başlık listesinin `X-Coremetry-Purpose`'u geçirip geçirmediği.
- `inlineStyleRatchet` `TOTAL`'da kalan boşluk; güncel ilk yük boyutu.
- Tüm `dosya:satır` kanıtları keşif ajanlarının okumasıdır; ana yazar dosyaları kendisi açmadı.

## 11. Yayın sonrası değişiklikler (2026-11-07, prod ilk bakış)

Bu bölüm üstteki audit metnini GEÇERSİZ kılar; ayrıntı `docs/DECISIONS.md` "Login özeti (anonim uç)".

- **1912** `Wordmark` tek kök (başlıkta "Core" / "metry" ayrılıyordu).
- **1913** Topoloji yerleşimi: kısa menzilli itme + duvar itmesi (düğümler kenara yığılıyordu; §6.3'teki "merkez çekimi" yok).
- **1914** HTTP kartı tek TOPLAM throughput serisi: Q1 `sum(rate(<aile>_count[5m]))`, alan `http_24h.throughput` (§4 ve §5.3'teki durum kodu kırılımı ve `status[]` kalktı; §9-10 kapandı).
- **1915** `activity_24h` yalnız `logins` + `cosre_questions`; `rollouts_tracked`, `traces_viewed` ve trace görüntüleme sayacı (§5.2) söküldü; yeni `endpoints_monitored`. Ekranda "Şu an" + "Son 24 saatte" satırları.
- **1931** Veri katmanı: veritabanı mor yuvarlak nokta + veritabanı adı (`db.name` ilk parçası), kuyruk ayrı `queue` katmanı (turkuaz halka + konu adı); her ad bir kez etiketlenir. Host çıkmaz.
- **1932** Akan noktalar Traces süre çizgisiyle aynı token (`--orange`); `--galaxy-gold` silindi.
- **1933** Etiketlere zemin çerçevesi. Merkezi kartın dışına alma reddedildi.
- **1934** Topoloji 360 düğüm (300 servis + 60 veri), kenar tavanı 540; 180'i aşınca noktalar %15 küçük.
- **1935** Üzerine gelince adın altında tür satırı (i18n).
- **1936** Açılışta yıldızlar 1,4 sn'de merkezden kollarına yayılır (bir kez; hareket azaltmada yok).
- **1937** Tüm servisler: siluet dışındaki her servis adsız yıldız (`topology.stars` = [yoğunluk, komşu dizini]); toz o kadar azalır.
- **1938** Açılış hareketi kaldırıldı (1936); galaksi doğrudan yerinde açılır.
- **1939** Sol alttaki "Canlı topoloji silueti" notu kaldırıldı.
