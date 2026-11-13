# Problems sayfası (`/inbox`) sadeleştirme planı (Faz 0, 2026-11-13)

**Durum:** S0 GEMİDE v0.10.2120 (karakterizasyon: `pages/Inbox.characterization.test.tsx`, `internal/api/inbox_team_kinds_test.go`). S1 GEMİDE v0.10.2121 (kolonlar; satır triyajı İlgilenen hücresinde). S2 GEMİDE v0.10.2122 (öncelik → en eski; sayım cümlesi; cache anahtarı plan yazılırken zaten `v11` idi → `v12`). S4 GEMİDE v0.10.2124 (önde Ekip/Öncelik/Tür/arama; kalanlar `coremetry/MoreFilters` "Diğer süzgeçler" altında, varsayılan-dışı değerde açık; taban şeridi ve kayıtlı görünümler bölümde; sayfa altı bağlantıları; `team` çipi derin link için kaldı). S5 GEMİDE v0.10.2125 (alt "Alert rules" mount'u kalktı; "Take it" İlgilenen hücresinde — `features/anomalies/AssigneeCell.tsx`, yalnız alarm problemi + editor+; `ProblemsSection` bileşeni artık mount'suz ama dosya `AlertProblemHost`'u taşıdığı için kaldı, bileşenin ve ona bağlı koruyucu testlerin silinmesi S10'a; `/api/problems` başka çağıranlar için kalır: `AlertProblemHost`, `DatabaseProblemsSection`). Bu parti S0–S2; S3 prod ölçümü bekler, S4+ sonraki parti. Operatör kararları: occurrence tabanı 5 KALIR (Ç2 a); "Ben ilgileniyorum" editöre kendine atama (S6). Operatör şikâyeti (prod): "çok karışık; hangi problem hangi ekibe ait, tipi ne göremiyorum; filtreler çok zor; gerçek problemleri basitçe listele, detayında yine anlat." Mockup onayı: "Problems mockup güzel, tüm problemler gözüksün" (2026-11-13). Emsal biçim: `docs/frontend/traces-shadcn-plan.md`. Sadeleştirme ilkesi: `docs/DECISIONS.md:12922` (v0.10.1879, önce düz cümle, teknik kanıt kapalı).

## 1. Bugün ne var (kanıt)

**Sayfa.** `frontend/src/App.tsx:214` `/inbox` → `pages/Inbox.tsx` (1616 satır, `docs/claude/large-files.md`). İki tablo alt alta:

| | Üst tablo "Ne bozuk" | Alt tablo "Alert rules" |
|---|---|---|
| Bileşen | `Inbox.tsx:669-688` `useDataTable` `storageKey:'inbox'`, `serverSort` | `features/anomalies/ProblemsSection.tsx:350-358` `storageKey:'alert-rules'`, istemci sıralama |
| Kolonlar | `INBOX_COLS` :227-252: Öncelik, Ne bozuk, Etki, Ne zamandan beri, Güven, Karar, Occurrences, Last seen, Assignee (+ seçim kutusu) | `PROBLEM_COLS` :78-93 + Assignee/Triage :602-620: Priority, Severity, Service, Metric, Value, Rule, Started, Status, Assignee, Triage |
| Veri | `GET /api/inbox` (`lib/queries/inbox.ts:59`, `lib/api.ts:2814`; sunucu `internal/api/inbox.go:185`), `limit=300`, poll 30 s | `GET /api/problems` (`lib/queries/problems.ts:17`, `api.ts:3459`; sunucu `api.go:8655`), `limit=200`; sayılar `GET /api/problems/buckets` |
| İçerik | problem + exception + httperror + anomaly (+ incident, emekli 1827) tek listede; `InboxItem.kind` (`lib/types.ts:2719,2792`), `source` insan okunur "Alert rule / Exception / Anomaly" | yalnız `Problem` satırları (alarm kuralı + anomaliden terfi) — üst tablodaki `kind=problem` satırlarının KOPYASI |
| Sıralama | `firstSeen desc` (`Inbox.tsx:103`, URL `s_inbox`) | `priority desc` (:100, :176 varsayılan P1+P2) |

**Sunucu birleştirme VAR.** `inbox.go:185` kaynak başına fetch (problems `srcLimit`, exception `inboxExcScanMax`, anomali `ListAnomalyFeed` = anomaly_events + ml_events, :748 v0.10.2082) → ortak süzgeç → `forceNonExceptionP3 → foldInboxIncidents → facet sayaçları → facet → sıralama/tavan` (aiops SKILL :184-196) → `s.serveCached` TTL 15 s, anahtar `inboxListKey` + since/cat/floorDefault/inc (`inbox.go:345`, `inbox:v10:`). Yanıt: satırlar + `counts` + `scanCapped` + `minOccDefault/minOcc/hiddenByMinOcc/keptBySpread/keptRegressed` (`Inbox.tsx:606-621`). Ekip satırda var: `InboxItem.OwnerTeam/SRETeam` okuma anında `service_metadata` join'i (`inbox.go:860-895`); kök neden hipotezi satırda YOK.

**Süzgeçler** (hepsi URL'de, `Inbox.tsx`):

| Etiket | Param | Varsayılan | Nerede uygulanır |
|---|---|---|---|
| Durum Open/All/Ignored (:909) | `status` | `open` (exception için ignored hariç tüm durumlar, 751) | sunucu |
| Öğrenilen Triage / Problem değil (:923) | `verdict` | `triage` (noise imzaları gizli) | istemci `filterByVerdictView` :656 |
| Özne Servisler/Veritabanları/Dış kaynaklar (:951) | `subject` | `service` (DB ve dış kaynak satırları GİZLİ) | sunucu |
| Öncelik (:969) | `prio` | **P1** (`PRIO_DEFAULT` :98) | sunucu |
| Tür (:988, yalnız service şeridinde) | `kind` | problem+anomaly (`KIND_DEFAULT` :126-132) | sunucu |
| Kategori (:998) | `cat` | hepsi | yalnız istemci (:655) |
| First seen (:1006) | `since` | boş | sunucu |
| Owner / SRE (:1038-1051) | `owner`/`sre` | boş | sunucu (`ownerTeam`/`sreTeam`, VE değil, DECISIONS:12118) |
| Takım çipi (:1078) | `team` | boş | sunucu (owner VEYA sre) |
| Arama (:1059), Servis, Env | `q`, `service`, `env` | boş | sunucu |

Varsayılan geçmişi (`Inbox.tsx:68-100`): 487 yalnız P1 → 659 P1+P2 → 1014 HEPSİ (DECISIONS:5221) → 1081 yeniden yalnız P1 ("hiç anlaşılmıyor", DECISIONS:1313). Bu plan dördüncü dönüş; fark: öncelik-önce sıralama + Tür/Ekip kolonları okunurluğu taşır.

**Occurrence tabanı.** `DEFAULT_MIN_OCC=5` (`Inbox.tsx:258`) / `inboxDefaultMinOcc=5` (`inbox.go:1552`); ≥2 servis veya regressed muaf (`effectiveDefaultFloor`, `floorExempt` `inbox.go:278-282, 603-613`). Metin "Showing N+ groups, regressed groups and groups (2+ occurrences) seen in ≥2 services…" :1181-1186; `show all` → `minOcc=0`, `10+` :1187-1188.

**Eylemler.** Toplu çubuk :1096-1157: Onayla (`POST /api/problems/acknowledge` + exception bulk state), Gerçek problem / Problem değil (`PUT /api/problem-verdicts`, editor+), Kapat (`POST /api/problems/resolve`, `problems_bulk.go:26`), Kalıcı sil (admin). Satır triyajı `ProblemVerdictRow` :1407 hover/focus (DECISIONS:11581 D1). Alt tabloda: "Triage ▶" :820 → `?problem=<id>` → `AlertProblemHost` (`Inbox.tsx:1446`); "▸ Ne oldu?" `InsightRowChip` :794; ROOT CAUSE `RootCauseRibbon` :790 (`p.rootCause` özeti); "Take it" `AssigneeCell` ~:960 → `PATCH /api/problems/:id/assignee`. Satır tıkı tam sayfa (:458-501, çekmece 1081'de kaldırıldı). Sayım şeridi yok (şeritler operatör kararıyla kaldırıldı :1167). Kayıtlı görünümler `<SavedViewsBar page="inbox">` :888 — yalnız `queryString` saklar (`SavedViewsBar.tsx:144-149`).

**Detaylar.**
- Alarm problemi: `features/anomalies/ProblemDetail.tsx:846` `AlertProblemDetail`, `?problem=` ile yerinde. Sıra: `rb-bar` (:941, Acknowledge admin) → `ProblemStandardHeader` :985 (tek cümle) → `ProblemTriage` :988 (Assign `window.prompt`, admin) → `ProblemInsightStrip`/`ProblemCoSRESection`/`IncidentCaseCard` → sol: grafik, "Root cause analysis" (:1012, `RootCausePanel`, `AIExplainButton` ×2), `ProblemInvestigationPanel`, `ProblemOffenders`, Metric/timeline (kapalı); sağ: Blast radius :1149, Correlated signals :1206, Log kanıtı :1299, Bildirim/Runbook/Description (kapalı). Resolve/Ignore yok (yaşam döngüsü kural motorunda).
- Anomali: `/anomalies/:id` (`App.tsx:219`) ve `?anomaly=`; `AnomalyEventDetail.tsx:231-335` (v0.10.2027): `DetailSummary` → `AnomalyTriage` → tek grafik, "Kanıt", "Kök neden" (yalnız güven yeterliyse) → "Teknik ayrıntı" kapalı :289 → sağ "Ne yapabilirim". Mockup'a en yakın.
- Exception: `ProblemDetail.tsx:303`, `/problems?exc=<fp>`; klasik düzen (:767 "Klasik düzen değişmez", v0.10.1032): rb-bar (Ack/Reopen/Ignore/Resolve admin) → tip+mesaj → `ProblemStandardHeader` → servis/ekip çipi → aiSummary → "Occurrences over time" (`TimeChart`, `/occurrences`) → Stack trace | pods, sample traces, triyaj. `Sect` yok.

**Koruyucu testler (bugün yeşil):** `Inbox.rowOpen.test.tsx` (1032/1081/1827), `Inbox.rowFormat.test.tsx` (1084/1529), `Inbox.promotedRecurring.test.tsx`, `Inbox.tableStates.test.tsx`, `lib/savedViewPage.test.ts`; Go: `inbox_scan_test.go`, `inbox_team_scope_test.go`, `inbox_badge_default_view_test.go` (rozet = varsayılan liste, 1086), `problems_read_filters_test.go`.

## 2. Bağlayıcı kararlarla çakışmalar (her biri operatöre soru, §7'de numaralı)

- **Ç1 Varsayılan P1 (1081) ↔ "tüm problemler gözüksün".** 1014'te "hepsi" denendi, 1081'de geri alındı. Öneri: hepsi (P1+P2+P3), ama öncelik-önce sıralama + Tür kolonu şart (S2 S3'ten önce). Rozet sayısı da değişir (1086). → S1.
- **Ç2 Occurrence tabanı 5 (949/1202).** İki seçenek: (a) taban kalır, "Diğer süzgeçler"de `show all`; (b) taban 0. Satır etkisi KODSUZ ölçülür: prod'da `GET /api/inbox?prio=P1,P2,P3&subject=…` yanıtındaki `hiddenByMinOcc` (gizlenen) ile dönen satır sayısı; `&minOcc=0` ile karşılaştır, `scanCapped` true ise tavan aşımı. Öneri: (a). → S2.
- **Ç3 Exceptions = ignored hariç hepsi, resolved dahil rozetle (751)** ↔ mockup alt linki "Çözülenler". Öneri: 751 kalır (resolved exception satırda "çözüldü" rozetiyle), "Çözülenler" linki `status=all`'a gider; alarm problemleri için resolved zaten listede yok. → S3.
- **Ç4 Özne şeridi (1342, Oracle 1017/1026) mockup'ta yok;** varsayılan `subject=service` DB/dış kaynak problemlerini gizliyor — "tüm problemler" ile çelişir. Öneri: varsayılan tüm özneler; Servis kolonu `SubjectLink` ile DB/dış kaynak gösterir; şerit "Diğer süzgeçler"e iner. → S4.
- **Ç5 Alert rules bölümü (837'de Exceptions'tan buraya; 1523 triage programı: Triage ▶, Take it, ROOT CAUSE, Ne oldu?).** Birleşmede bölüm kalkar. Öneri: Take it → İlgilenen hücresi; Triage ▶ → satır tıkı (zaten tam sayfa); ROOT CAUSE rozeti ve "Ne oldu?" yalnız detayda (satır başına ek çağrı yasak). → S5.
- **Ç6 Karar/verdict kolonu + Öğrenilen süzgeci (1015, 1523 D1/D11).** Mockup'ta yok. Öneri: kolon kalkar, hover triyaj eylemleri D1 gereği kalır; `verdict` süzgeci "Diğer süzgeçler"de; noise gizleme varsayılanı değişmez. → S6.
- **Ç7 Exception detayı klasik (1032, memory "Exception detay klasik").** Mockup tek detay şablonu öneriyor. Öneri: exception detayına DOKUNULMAZ; yalnız üst meta satırına ekip/tür/ne zamandır eklenir. Dört kutu alarm problemi + anomaliye uygulanır. → S7.
- **Ç8 "Çözüldü" eylemi alarm probleminde yok** (yaşam döngüsü kural motoru). Öneri: alarm detayında "Çözüldü" yerine "Onayla" (acknowledge); exception'da Resolve kalır. → S8.
- **Ç9 "Ben ilgileniyorum"** = acknowledge + kendine atama; bugün Assign admin-only `window.prompt`. Öneri: editor+ kendine atayabilir (`PATCH /assignee` rol kapısı genişler, audit var); başkasına atama admin. → S9.
- **Uyumlu, yeniden önerilmez:** MTTR/lifecycle şeridi yok (üst sayım TEK cümledir, şerit değil, sticky değil); P1 yapışkan (`exceptionPriorityAt` inbox.go:1832) dokunulmaz; batch servisleri dedektör tarafı; dört tablo türü — `DataTable`+`useDataTable` kalır, sunucu sıralı = resize only; her hücre pivot (Servis → `/services/:id`, Ekip → `?team=`); emoji yok; `timeRangeToNs` yok (sayfa `range` kullanmıyor).

## 3. Birleşik liste veri modeli

- **Yeni endpoint GEREKMEZ, yeni CH şeması GEREKMEZ.** `/api/inbox` zaten üç kaynağı birleştiriyor; alt tablo aynı `Problem` satırlarının kopyası. Birleşme = `ProblemsSection`'ın Inbox'tan çıkarılması.
- **Sunucu değişiklikleri (küçük, `inbox.go` içinde):** (a) birleşik sıralama `sort=priority,firstSeen` (öncelik desc, sonra firstSeen asc = en eski üstte); (b) yanıta `oldestFirstSeen` (tavan öncesi) ve `counts.team` facet'i; (c) satıra `teams []string` (çok servisli exception için `SpreadServices` → `service_metadata` join; tekil `OwnerTeam` alanı korunur); (d) cache anahtarı `inbox:v10:` → `v11`, yeni girdiler (sort bileşimi) hash'e girer. `oldestFirstSeen` istemcide hesaplanAMAZ: liste 300'de kesiliyor.
- **Tablo >100 satır.** `limit=300` sunucu tavanı sürer; `contentVisibility:'auto'` satırlarda (var mı doğrulanmadı → S0'da kontrol). Varsayılan "hepsi" ile 300 tavanı aşılırsa kırpma uyarısı (:1213-1232) kalır; sayfalama operatör gerekirse (soru 10).
- **Bütçe.** `/api/inbox` için yazılı p99 bütçesi yok (`docs/perf/perf-budget-2026-08-28.md` yalnız `/api/problems`: cold 0.59 s, `problems FINAL` 6.5k satır). "Hepsi" varsayılanı `problemScanLimit`/`inboxExcScanMax` taramasını büyütür; S3 öncesi prod'da `scanCapped` ve yanıt süresi ölçülür (ES sayacı emsali: ölçülmemiş yük varsayılana girmez). Sıcak hedef 50 ms (`loadtest-plan.md:203`).
- **DUR işareti:** tavan ya da süre bütçeyi aşarsa `OpenProblemsSnapshot` emsali (FINAL'sız okuma) ayrı `/perf-triage` dilimi; bu planın parçası değil.

## 4. "Ekip" kolonu

- Kaynak: CH `service_metadata` (`store.go:2339`: owner_team, sre_team, `*_team_auto`, RMT(version)); okuma `ListServiceMetadata` (`chstore/service_metadata.go:162`); alias `team_aliases` (DECISIONS:12118, v0.10.1683).
- Satıra bağlama: problem ve exception için servis → owner_team (yoksa `owner_team_auto`, yoksa sre_team); anomali aynı (servis alanı var). DB öznesi `chstore.ResolveDBOwner` ("en çok çağıranın takımı").
- Görünüm: tek ekip → ad (tıkla: `team=` süzgeci); çok servisli exception → farklı ekip sayısı "4 ekip" (detayda liste); hiç eşleme yok → "sahipsiz" (soluk, renk yok — renk yalnız sapmada). Owner VE SRE farklıysa owner gösterilir, SRE detay meta satırında.
- Ekip süzgeci = mevcut `team` paramı (owner VEYA sre, 1683 kararı değişmez); `owner`/`sre` ayrı paramlar "Diğer süzgeçler"de kalır.

## 5. Düz cümle ve dört kutu — deterministik kaynaklar

- **Liste "Ne bozuk" HER ZAMAN deterministik:** bugün de öyle — `InboxItem.std` (= `ProblemStandard`, sunucu) + `lib/inboxRowText.ts` + `TriageTitleCell`; LLM yok. Değişmez; alt satıra tek satır ayrıntı: alarm → "kural · metrik değer/eşik"; exception → "tip · N oluşum · M servis"; anomali → "sinyal · yön · gözlenen/taban" (`MLEvent.observed/baseline/direction`, `AnomalyEvent.peak_ratio`).
- **Detay kutuları (alarm problemi, anomali):**
  - *Ne oluyor*: `ProblemStandardHeader`/`DetailSummary` cümlesi (mevcut, deterministik).
  - *Kimi etkiliyor*: Problem.Service/Clusters/Pod, OwnerTeam/SRETeam; exception `SpreadServices`; anomali servis. Blast radius (:1149) özeti buraya tek cümleyle, ayrıntı teknik kanıta.
  - *Olası neden (güven)*: `root_cause_hypotheses` (`store.go:2669`; `TopSuspect`, `Confidence` 0..1, `RecentDeploy`) `GET /api/problems/{id}/rootcause`. Deterministik; güven eşiğin altında veya kayıt yoksa KUTU GİZLİ (anomali detayı bugün böyle). LLM düzyazısı (`rootCauseExplainProse`, `s.copilotExplain`, model kapalıyken 503 `rootcause.go:641`) yalnız "CoSRE'ye sor" düğmesiyle, kutunun içinde değil.
  - *Ne yapılabilir*: `exemplar_trace_id` → `/trace/:id`; servis → `/logs?service=`; `RunbookURL`; "CoSRE'ye sor" (`AIExplainButton`, model kapalıyken düğme gizli — `copilot.Active()`).
- Model kapalıyken dört kutudan üçü dolu, "Olası neden" hipotez varsa dolu; `aiSummary` bloğu teknik kanıta iner.

## 6. Dilimler (her biri tek commit, kendi `v0.10.X`, `git revert` ile geri alınır)

Kural: §1'deki koruyucu testler S0 dışında DEĞİŞMEDEN geçmeli; davranış değişen dilimde ilgili test aynı commit'te güncellenir ve commit gövdesinde gerekçesi yazılır. Kalkan URL paramı için uyumluluk dolgusu EKLENMEZ; bilinmeyen param zaten yok sayılır — kayıtlı görünümler (`queryString`) kırılmaz, yalnız "Diğer süzgeçler"e inen süzgeçler kayıtlı görünümde set ise bölüm açık gelir.

- **S0 Karakterizasyon testleri** (üretim kodu değişmez). Yeni `[Y]`: `/api/inbox` ekip alanı her kind için doluyor (Go, `inbox_team_*`); `std` cümlesi üç kind için snapshot (vitest); hover triyaj eylemleri satırda; satır tıkı tam sayfa (var); `contentVisibility` satır stili kontrolü; rozet = varsayılan liste (var). Dosyalar: `frontend/src/pages/Inbox.*.test.tsx`, `internal/api/inbox_*_test.go`.
- **S1 Kolonlar.** `INBOX_COLS`: Öncelik · Ne bozuk · Servis · Ekip · Tür · Ne zamandan beri · İlgilenen. Etki/Güven/Karar/Occurrences/Last seen kalkar (occurrence sayısı alt ayrıntı satırına). Sunucu: `teams[]`. `storageKey:'inbox'` → `'inbox2'` (eski kolon genişlikleri). Test: `Inbox.rowFormat` güncellenir. Operatöre görünen: kolonlar. Risk: düşük.
- **S2 Sıralama + sayım cümlesi.** `s_inbox` varsayılanı `priority desc, firstSeen asc`; üstte "N açık problem: a P1, b P2, c P3. En eskisi X gündür açık." (`counts` + yeni `oldestFirstSeen`). Cache `v11`. Dosyalar: `inbox.go`, `Inbox.tsx`, `queries/inbox.ts`. Risk: sunucu sıralama; `inbox_scan_test`.
- **S3 Varsayılan "hepsi".** `PRIO_DEFAULT` → P1,P2,P3; `subject` → all (Ç4 onayı); `minOcc` Ç2 cevabına göre. Önce prod ölçümü (§3 bütçe, Ç2 ölçümü). `inbox_badge_default_view_test` güncellenir (rozet artık tüm açık problemleri sayar — operatöre söylenir). Risk: ORTA — 1081'in geri dönüşü; S1+S2 gemide olmadan yapılmaz. Geri alma tek revert.
- **S4 Süzgeç sadeleştirme.** Ön plan: Ekip (`team`), Öncelik, Tür, arama. "Diğer süzgeçler" (kapalı; URL'de varsayılan-dışı değer varsa açık): Durum, Öğrenilen, Özne, Kategori, First seen, Owner/SRE, taban (`show all`/`10+`), SavedViewsBar. Sayfa altı linkler: Çözülenler (`?status=all`), Yok sayılanlar (`?status=ignored`), Alarm kurallarını yönet (`/alerts`). Hiçbir param silinmez. Primitif: `coremetry/FilterBar` ailesi (DECISIONS:12289); yeni collapsible emsali yok → `/frontend-design-system` ile kontrol. Risk: düşük.
- **S5 Alert rules tablosunun kaldırılması.** `Inbox.tsx:1433-1440` `ProblemsSection` mount'u ve `useProblems` çağrısı kalkar; Take it → İlgilenen hücresi (`PATCH /assignee`, mevcut `AssigneeCell` yeniden kullanılır); `AlertProblemHost` (`?problem=`) KALIR. `ProblemsSection.tsx` başka mount'u yoksa silinir (S0'da grep). `/api/problems`, `/buckets` başka tüketicileri için kalır. Kaybolan: ROOT CAUSE rozeti ve "Ne oldu?" listede (Ç5). Risk: orta (1523 programı yüzeyi); revert kolay.
- **S6 Alarm detayı — başlık ve eylemler.** `AlertProblemDetail`: başlık "P1 · düz cümle"; meta satırı servis/ekip/tür/ne zamandır/ilgilenen; eylemler "Ben ilgileniyorum" (ack + self-assign, Ç9), "Onayla"→kalkar (ilgileniyorum kapsar), "Problem değil" (`ProblemVerdictActions`). Audit mevcut (`problem.assign`, `problem.acknowledge`). Risk: rol kapısı.
- **S7 Alarm detayı — dört kutu + teknik kanıt kapalı.** Sol: Ne oluyor · Kimi etkiliyor · Olası neden · Ne yapılabilir; sağ: `AlertMetricChartSection` + kısa geçmiş ("Problem timeline" ilk 5 satır). "Teknik kanıt" (`Sect collapsible`, kapalı): kural tanımı, eşik/ölçüm, Root cause panel ayrıntısı, Blast radius tablosu, Correlated signals, Investigation, Offenders, Log kanıtı, Bildirim, Runbook, Description, aiSummary. Mount edilmez (bugünkü `Sect` davranışı). Risk: ORTA — 1523 programının detay yüzeyleri gizleniyor, operatör görür.
- **S8 Anomali detayı hizalama.** `AnomalyEventDetail` zaten yakın; kutu adları/sıra S7 ile aynı, meta satırına ekip/tür. Risk: düşük.
- **S9 Exception detayı — yalnız meta satırı** (Ç7): ekip/tür/ne zamandır/ilgilenen çipleri; klasik düzen, sıklık çizgisi, stack dokunulmaz. Risk: düşük.
- **S10 Temizlik.** Ölü kod (`InsightRowChip`/`RootCauseRibbon` liste kullanımı, `inboxBuckets` alt tablo yolları), `large-files.md` satır sayıları, DECISIONS girişi, `Inbox.tsx` bölünmesi (kolonlar/süzgeçler ayrı dosya) — davranış değişmez.

## 7. Kapsam dışı, doğrulanamayanlar, operatöre sorular

**Kapsam dışı:** `/alerts` kural yönetimi; `/anomalies` sayfası ve ML sekmesi; exception klasik düzeni; bildirim/notify; `OpenProblemsSnapshot` performans işi; sayfalama (tavan 300 sürer).

**Doğrulanamayanlar (tarayıcı yok):** kolon genişlikleri ve "Ne bozuk" iki satırlı hücrenin sığması; "Diğer süzgeçler" açılış animasyonu; prod satır sayısı ve `hiddenByMinOcc` (operatör ölçer, §2 Ç2); `/api/inbox` "hepsi" ile yanıt süresi.

**Operatöre sorular (öneri ile):**
1. Varsayılan "hepsi" = P1+P2+P3 VE tüm türler VE tüm özneler (DB, dış kaynak dahil) mi? Öneri: evet, üçü de.
2. Occurrence tabanı 5 kalsın mı? Öneri: kalsın; ölçüm `hiddenByMinOcc` ile; 0 isteniyorsa S3'te tek satır.
3. Çözülmüş exception'lar varsayılan listede rozetle kalsın mı (751) yoksa "Çözülenler" linkine mi insin? Öneri: kalsın.
4. Alt "Alert rules" tablosu tamamen kalksın mı? ROOT CAUSE rozeti ve "Ne oldu?" listede kaybolur, detayda kalır. Öneri: kalksın.
5. Karar (verdict) kolonu kalksın, hover eylemleri kalsın mı? Öneri: evet.
6. Exception detayı klasik kalsın (yalnız meta satırı) mı? Öneri: evet.
7. "Ben ilgileniyorum" editor için kendine atama hakkı açsın mı? Öneri: evet, başkasına atama admin.
8. Alarm probleminde "Çözüldü" düğmesi olmayacak (kural motoru kapatır); kabul mü? Öneri: evet, "Problem değil" yeter.
9. Rozet (menü sayısı) varsayılanla birlikte tüm açık problemleri sayacak; kabul mü? Öneri: evet (1086 ilkesi korunur).
10. 300 satır tavanı aşılırsa sayfalama istenir mi? Öneri: önce ölç, aşılırsa ayrı dilim.
11. Üst sayım cümlesi tek satır, kaydırmayla gider (sticky değil); kabul mü? Öneri: evet.
