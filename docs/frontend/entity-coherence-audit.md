# Entity tutarlılık denetimi — arayüzü entity etrafında birleştirmek (Faz 0, 2026-11-27)

Durum: DENETİM. Kod, stil, rota, test ve ayar değişikliği YOK. Taban: `v0.10.2482` (7797e4c8d).
Amaç (operatör brief'i): yeni görsel dil değil; mevcut primitiflerin üstüne ortak sözleşme —
"bir ürün ekibi tasarlamış" tutarlılığı. Sonda üç soru operatörün cevabını bekliyor.

İşaretler: **[K]** kodda doğrulandı (dosya:satır, v0.10.2482) · **[Ö]** ölçüldü (komut/tanım
§Ölçüm tanımlarında, kimliği `Mx`) · **[V]** varsayım (ölçülmedi). Yollar `frontend/src/`
altındadır, aksi yazmadıkça. `docs/screenshots/` 2026-10-19 tarihli (`git log -1 --
docs/screenshots`); hiçbir bulgu ekran görüntüsünden alınmadı, hepsi koda karşı.

## Özet

**Ne sayıldı.** Router'daki 68 rota girişi (53 sayfa, 14 yönlendirme, 1 yalnız-geliştirme);
15 satırlık detay yüzeyi tablosu; entity adını basan 7 bağlantı üreticisi + 1 bileşen; 164 biçimleyici tanımı;
873 ham `toFixed`/`toLocale*`/`Intl` çağrısı; ürün ekranlarında 3.241 sabit metin (TR 2.103,
EN 1.138; yönetim ekranları ayrı: TR 1.806, EN 677); 14 farklı hata-oranı eşik biçimi ve 6
adlandırılmış sağlık tanımı (7 fonksiyon).

**En büyük beş tutarsızlık**

1. **Ad karmaşası, rota ile etiket ters.** `/inbox` kullanıcıya "Problems" (EN) / "Sorunlar"
   (TR) diye görünür, `/problems` ise "Exceptions/Errors" sayfasıdır; servis sayfasındaki
   "⚠ Problems" düğmesi Exceptions sayfasına gider. TR katalogda "Olaylar" iki ayrı kavramın
   (Incidents ve Events) adı. [K] `lib/i18n.tr.ts:66,68,69,152`, `pages/Service.tsx:586`,
   `features/anomalies/AnomaliesPage.tsx:584`, `pages/Inbox.tsx:755`.
2. **Sağlığın tek tanımı yok.** Servis listesi noktası açık Problem + hata oranından
   (`internal/api/api.go:1913` `scoreHealth`), servis sayfası başlık noktası yalnız hata
   oranından (`pages/Service.tsx:87`) hesaplanıyor: kritik açık Problem'i olan, hatası %1'in
   altındaki servis listede kırmızı, kendi sayfasında nötr. Ayrıca ön yüzde 50 satırda 14
   farklı eşik biçimi (`>5/>0`, `>=5/>=1`, `>5/>1`, kesir `>0.05/>0.01` …). [Ö] M8.
3. **Her detay sayfasının kendi başlığı var.** 8 detay yüzeyinde 7 ayrı başlık uygulaması,
   paylaşılan bileşen 0; breadcrumb 4 biçimde, pivot düğmeleri 4 biçimde (DrillButton,
   `a.accent`, `ud-pill`, PodPivot listesi). Servis sayfasında ad üç kez yazılıyor (Topbar
   `<h1>`, breadcrumb, ikinci `<h1>`). [K] §0.
4. **Biçimleme dağınık.** 85 dosyada 164 biçimleyici tanımı; aynı ad birden çok kez tanımlı
   (`fmtMs` 9, `fmtCount` 7, `fmtPct` 6, `ago*` 5, `fmtNum` 4 …) ve biçimleyici dışında 540
   ham `toFixed`/`toLocale*` çağrısı. [Ö] M5, M6.
5. **Dil sızıntısı.** Ürün ekranlarında katalog dışı 3.241 sabit metin; sayfa başlıkları
   (`<Topbar title="…">`) 58 yerde sabit metin, hepsi katalog dışı; servis/topic/cluster
   sekme adları kodda sabit İngilizce. [Ö] M7, M10; [K] `pages/Service.tsx:865-873`.

**Bu denetimin açtığı karar.** Üç soru (§Sorular): "Problem" tek üst kavram mı, sekme sırası ve
adları, `/e/<tür>/<kimlik>` adres şeması. Cevaplardan sonra §6'daki sekiz dilim sırayla, her
biri tek sürüm olarak başlayabilir; ilk iki dilim (biçimleyici + dil) sorulardan bağımsızdır.

---

## Ölçüm tanımları (ratchet testleri aynı tanımı benimser)

Ortak kapsam (aksi yazmadıkça): `frontend/src/**/*.{ts,tsx}`; HARİÇ `*.test.*`, `*.stories.*`,
`*.d.ts`, `components/shadcn/**` (üretilen), `testdata/`. Yorumlar sayımdan önce ayıklandı
(`/*…*/`, `//…`; mevcut `styles/zLayers.test.ts` `stripTsComments` ile aynı amaç). 1.509 dosya
tarandı. Python betikleri denetimcinin karalama alanında koştu, depoya girmedi; tanımlar
burada eksiksiz. Yönetim kümesi (ADMIN) = `pages/(Admin*|System|Settings|Users).tsx` +
`pages/(admin*|settings|system)/**` (karar: /admin ve Settings sekmeleri çevrilmez).

| Kimlik | Ne | Tanım (regex / komut) |
|---|---|---|
| M1 | Rota girişi | `rg -c 'path=' frontend/src/App.tsx` → 68; sınıflama elle, `App.tsx:176-261` |
| M2 | Bağlantı üreticisi çağrısı | `\b<ad>\(` eksi `function <ad>\(`; ad ∈ serviceHref, entityHref, podDetailPath, endpointDetailHref, databaseDetailHref, messagingTopicHref, nodeDetailHref |
| M3 | SubjectLink JSX | `<SubjectLink(?=[\s/>])` |
| M4 | Ad bağlantısının stili | `<Link …>` açılış etiketi (çok satırlı) M2 üreticisini içeriyorsa: `mono` / `fontWeight\|font-semibold\|font-medium` / başka `className` / `style=` / düz |
| M5 | Biçimleyici tanımı | `(function\|const)\s+((fmt\|format)[A-Z0-9]\w*\|\w*[Aa]go(TR\|Lang\|Text\|Label)?\|duration(TR\|Label)\w*\|ts(Short\|Long\|DateTime\|Compact\|Minute\|Rel)\w*)`; ad anahtar sözcüğüyle sınıflanır (Bytes→bayt; Ago/Clock/Time/Date/Tick/ts*→zaman; Dur/Ms/Ns/Sec/Step/Eta→süre; Pct/Ratio/Share→yüzde; Num/Count/Rps/Rate/Compact/…→sayı); değer biçimlemeyen adlar (`formatChip`, `formatSortParam` …) sınıf almaz, sayılmaz |
| M6 | Ham biçimleme | `\.toFixed\(` · `\.toLocale(String\|DateString\|TimeString)\(` · `new Intl\.(NumberFormat\|DateTimeFormat\|RelativeTimeFormat)`; "biçimleyici dosyası" = M5 tanımı taşıyan dosya |
| M7 | Sabit (katalog dışı) metin | Yalnız `.tsx`, `/design` hariç. JSX metni `>\s*([^<>{}\n]*[A-Za-zÇĞİÖŞÜçğıöşü]{2,}[^<>{}\n]*?)\s*<` + `(title\|placeholder\|aria-label\|label\|ariaLabel\|emptyMessage\|header)="…harf…"`; `=>`, `&&`, `\|\|`, ` = ` içeren ya da `.`/`(` ile başlayan aday atılır. TR = Türkçe harf ya da TR durak sözcüğü (`ve\|bir\|için\|yok\|değil\|olarak\|ile\|bu\|şu\|gibi\|kadar\|sonra\|önce\|daha\|var\|yalnız\|tüm\|hiç`), değilse EN. Sezgisel: `ERROR`, `config.yaml` gibi kod/sabit adayları da sayılır (örneklemde ~%10, [V]) |
| M8 | Hata eşiği biçimi | satır başına `(errorRate\|errPct\|errorPct)\s*(>=?)\s*([0-9.]+)`, ilk iki eşleşme bir "biçim" |
| M9 | Açıklama paragrafı | ürün `.tsx`: `<p\b[^>]*className="[^"]*(note\|hint\|help\|desc\|intro\|lede\|explain\|muted\|sub\|cap)` |
| M10 | Topbar başlığı | `<Topbar\s+title=("…"\|\{)` |
| M11 | Kavram sözcükleri | `lib/i18n.tr.ts` / `lib/i18n.en.ts` değerleri (`'anahtar': '…'`) üzerinde kavram regex'leri (§1.1 tablosunda) |
| M12 | Filtre kontrolü | `rg -c '<select\b'` (ve `<SegmentedControl\b`, `<Chip\b`, `<FacetMultiSelect\b`, `<FilterBar\b`, `<DropdownMenu\b`, `className="controls`) `-g '*.tsx' -g '!*.test.*' -g '!components/shadcn/**' -g '!components/ui/**'` |
| M13 | Sekme şeridi | aynı glob ile `<TabStrip\b`, `<UiTabStrip\b`, `<TabsList\b` |
| M14 | lucide ikon kullanımı | `import {…} from 'lucide-react'` gövdesindeki adlar, dosya başına |
| M15 | Eylem sütunu | `rg -c "kind: 'actions'" -g '*.ts*' -g '!*.test.*'` |

---

## BÖLÜM 0 — Güncel durum sayımı

**Rotalar [Ö] M1:** `App.tsx`'te 68 `path=` girişi. 14'ü yönlendirme (`/`, `/topology`,
`/deploys`, `/incidents`, `/incident`, `/anomalies`, `/deployment-report`, `/exceptions`,
`/settings`, `/errors`, `/status`, `/system`, `/admin/:tab`, `*`), 1'i yalnız geliştirmede
(`/design`, `App.tsx:96,260`), **53'ü gerçek sayfa**. Brief'teki "68" router girişi sayısıyla
tutar; sayfa sayısı 53.

**Detay yüzeyleri [K]** (tek entity ya da tek olay gösteren her yüzey):

| Rota / yüzey | Başlık bileşeni | Sekmeler (kod sırası) | Üst düğme satırı | Kimlik URL'de |
|---|---|---|---|---|
| `/service` | Topbar `Service · <ad>` + `.svc-head` (crumbs, nokta, `<h1>`, ★, runtime, katalog) `Service.tsx:500-560` | Overview · Operations · Metrics(`details`) · Infrastructure · GitOps · HTTP/GRPC · Messaging · Topology · Logs (`:865-873`, sabit EN) | [↩ Backtrace · ≡ Logs · ⇄ Endpoints] [⚠ Problems · ∿ Signals] Sağlık kontrolü [⋮ Traces] (`:567-600`) | sorgu `?name=` (+`tab`, `env`, `range`); cluster/namespace YOK (`lib/serviceHref.ts:62`) |
| `/service/backtrace` | Topbar `Backtrace · <ad>` (`ServiceBacktrace.tsx:118`) | yok | yok | `?name=` |
| `/endpoint` | Topbar `Endpoint` + satır içi stilli div: MethodBadge, yol (mono), servis linki, kapsam rozetleri (`EndpointDetail.tsx:239-325`) | yok (bölümler) | Alarm · kaynak `<select>` · Ask CoSRE · Service → · Traces → (`a.accent`) | `?service&path` (+`method`, `family`, `sig`, `env`, `cluster`, `compare`, `entry`) `endpointParam.ts:67` |
| `/database` | Topbar `Database` + `DatabaseIdentityHeader` (`databases/detailSections.tsx:89`) | yok | Traces → · Tüm sorgular → (`a.accent`, `:136-145`) | `?system&instance` (+`name`, `source`) `databaseParam.ts:38` |
| `/databases/statement` | Topbar `Statement` (`StatementDetail.tsx:99`) | yok | DrillButton (`slowqueries/stmtDetailSections.tsx`) | `?stmt=<kodek>` |
| `/pod` | Topbar `Pod · <ad>` + `PodIdentityLine` (`Pod.tsx:286-289`) | yok (bölümler) | PodPivot: ← servis · Servis sayfası · Traces · Logs (`Pod.tsx:255-261`) | `?cluster&namespace&pod` (+`service`, `deploy`, `at`, `from`) `podDetailPath.ts:13` |
| `/entity` (node, namespace, workload, db/queue/ext/host) | yalnız Topbar `<tür> · <ad>` (`EntityDetail.tsx:52`) | yok | yok | `?id=<önek>:<cid>/…&at=` (`EntityDetail.tsx:33`) |
| `/messaging/topic` | Topbar `Topic` + `.mtp-crumb` + `.mtp-id` (`MessagingTopic.tsx:119-140`) | Overview · Consumers · Producers · Traces (`:150-153`, sabit EN) | Traces → (`ud-pill`) | `?system&cluster&destination` (+`tab`) `topicHref.ts:60`; ikinci adres: `/messaging?destination=a\|b\|c` çekmecesi (`Messaging.tsx:83`) |
| `/clusters?cluster=` | Topbar `Clusters` + `cl-detail` cümlesi | Overview · Nodes · Namespaces · Pods (shadcn Tabs, `Clusters.tsx:812-815`, sabit EN) | FilterBar + NamespaceCombobox | `?cluster=<AD>` (id değil) `&section&namespace&deployment` |
| namespace (Clusters içinde) | `NamespaceDetail` (`Clusters.tsx:858`) | — | — | `?cluster&section=namespaces&namespace` |
| `/hosts?host=`, `/external?host=` | çekmece | — | — | `?host=` (`Hosts.tsx:67`, `External.tsx:84`) |
| `/trace` (olay) | Topbar `Trace Detail` + `.crumbs` (`Trace.tsx:560,613`) | Trace · Logs · Metrics (`:778`) | eylem kümesi | `?id=` |
| `/inbox?problem=` (olay) | Topbar `Problem` (`Inbox.tsx:1335`) | — | Ask CoSRE vb. | `?problem=<id>` |
| `/anomalies/:id`, `/anomalies/ml/:id` (olay) | kendi kabuğu; ML: Topbar `Anomali` | — | — | yol parametresi |
| `/dashboard`, `/runbook` (çalışma alanı) | Topbar `<ad>` | Runbook: kendi TabStrip | — | `?id=` |

**Entity adını çizen bileşen/stil sayısı.**

| Üretici / bileşen | Çağrı [Ö] M2/M3 | Dosya |
|---|---:|---:|
| `serviceHref` | 102 | 69 |
| `entityHref` | 27 | 14 |
| `podDetailPath` | 9 | 8 |
| `endpointDetailHref` | 6 | 6 |
| `databaseDetailHref` | 5 | 5 |
| `messagingTopicHref` | 4 | 4 |
| `nodeDetailHref` | 3 | 3 |
| `<SubjectLink>` | 5 | 4 |

Üretici ile kurulan 71 `<Link>` etiketi **6 ayrı stille** basılıyor [Ö] M4: başka `className`
21, düz 19, `mono` 17, satır içi `style` 7, kalın 6, `mono`+kalın 1. Çağrıların geri kalanı
`navigate()` ya da `getRowHref` üzerinden (satır bağlantısı). Elle yazılmış `/service?name=`
literali 0 (`serviceHref.test.ts:205` boş izin listesi korunuyor). Detay başlıklarında adı
basan ayrıca 7 başlık uygulaması (yukarıdaki tablo) ve CoSRE kapsam aramasında üçüncü bir
ikon dili (`components/cosre/conversation/CosreScopeSearch.tsx:19`: servis = `Search`,
operasyon = `Workflow`; sidebar'da servis `Boxes`, `Workflow` topoloji).

---

## BÖLÜM 1 — Sözlük ve dil

### 1.1 Kullanıcıya görünen kavramlar

| Kavram | Bugünkü anlam (kod) | Göründüğü yer | Katalogda geçiş [Ö] M11 (TR / EN) |
|---|---|---|---|
| Problem | Triyaj kaydı: kural ateşlemesi, dedektör, dış kaynak; standart satır, Etki sütunu (v0.10.2482, `inbox.col.impact` `i18n.tr.ts:1824`) | `/inbox` (sekme Problemler, `inboxTabs.ts`), `/inbox?problem=`, sidebar rozeti yalnız açık Problem (`Sidebar.tsx:93-97`) | 188 / 193 (`\bproblem\w*\|\bsorun\w*`) |
| Anomaly / Sinyal | ML/istatistik olayı; henüz Problem değilse "Neden Problem değil" (`lib/whyNotProblem.ts:30`, `signals.col.why` `i18n.tr.ts:1839`) | `/inbox?tab=signals`, `/anomalies/:id`, `/anomalies/ml/:id`; `/anomalies` → yönlenir (`App.tsx:220`) | 104 / 110 (`anomal\w*\|sinyal\w*\|signal\w*`) |
| Incident | Problem kümesi; ana anahtar `chstore.IncidentsEnabled`, varsayılan emekli | Sayfa silindi; `/incidents`, `/incident` koşulsuz `/inbox?tab=signals`'a (`App.tsx:215-216`, `lib/incidentsRetired.ts:12`) | 78 / 6 (TR sayısı "olay" sözcüğü yüzünden şişkin) |
| Exception | Hata grubu (exception_groups; Oracle grupları dahil) | `/problems` (Topbar "Exceptions", `AnomaliesPage.tsx:584`; menü "Exceptions/Errors") | 45 / 221 (`exception\w*\|errors?\|istisna\w*`) |
| Alert | Kullanıcı kuralı (eşik); ateşlenince Problem üretir | `/alerts` (Topbar "Alert rules", `Alerts.tsx:400`) | 33 / 39 |
| Watcher | İçe alınmış ES watcher filosu | `/watchers` (`Watchers.tsx:132`) | 1 / 1 |
| Monitor | Sentetik yoklama (http/tcp/ssl-cert/keyword/heartbeat, `types.ts:3861`) | `/monitors` (sidebar'da yok, ⌘K'da var) | 2 / 4 |
| Event | Elle/deploy olay işareti | `/events` (Topbar "Events") | 26 / 104 |

**Hedef:** kullanıcıya dönük tek üst kavram **Problem**; geri kalanlar Problem'in *kaynağı*
(Kural/Alert, Dedektör/Sinyal, Exception, Watcher, Monitor, Dış kaynak) ya da *bağlamı* (Event =
değişiklik işareti, Incident = gruplama). v0.10.2481 bunun yarısını yaptı: tek triyaj sayfası,
Sinyaller sekmesi, `/anomalies` menüden çıktı [K]. Kalan:

- **Rota–ad tersliği:** `/inbox` = Problemler, `/problems` = Exceptions. Öneri: Problem listesi
  `/problems`'e, Exceptions `/exceptions`'a taşınır; eski adresler yönlenir (`/inbox` →
  `/problems`, eski `/problems?…` → `/exceptions?…` — **sıra önemli**: `/problems` yeni anlamına
  geçtiği gün eski `/problems` linkleri Exceptions'a değil Problemler'e düşer; çözüm için
  §Açık kararlar A3).
- **Sidebar etkisi:** Triyaj grubu bugün `Sorunlar` · `Exceptions/Errors` · `Deployment/Rollout'lar`
  (`Sidebar.tsx:92-98`). Öneri: `Problemler` · `Hatalar` (Exceptions) · `Değişiklikler`
  (Rollouts); Alerting grubu `Kurallar` (Alerts) · `Watcher'lar` · `SLO'lar` · `Olay işaretleri`
  (Events). Monitörler menüde yok, dokunulmaz.
- **"Olaylar" çakışması:** TR'de hem `nav.incidents` hem `nav.events` "Olaylar"
  (`i18n.tr.ts:68,152`). Incident emekli olduğu için `nav.incidents` metni anahtar açıkken
  görünür; öneri "Olay grupları".

### 1.2 Kararsız adlar — tek ad önerisi

| Bugün | Yer | Öneri TR / EN |
|---|---|---|
| `Sorunlar` (menü) / `Problemler` (sekme) / `Problems` (Topbar) / `Problem değil` (eylem) | `i18n.tr.ts:66,1832,1920`, `Inbox.tsx:755` | **Problemler / Problems** (menü, sekme, başlık aynı anahtar) |
| `Exceptions/Errors` (menü, iki dilde aynı) / `Exceptions` (Topbar) | `i18n.tr.ts:69`, `i18n.en.ts:35`, `AnomaliesPage.tsx:584` | **Hatalar / Exceptions** (eğik çizgi yok) |
| `Deployment/Rollout'lar`, `Deployment/Rollouts` | `i18n.tr.ts:84`, `i18n.en.ts:50` | **Değişiklikler / Changes** — entity sekmesi adıyla aynı (§3.4); ikinci seçenek "Dağıtımlar / Deployments" |
| `Anomaly` (detay Topbar) / `Sinyaller` (sekme) / `Signals` (servis düğmesi) | `MLReplayPage.tsx:28`, `Service.tsx:589` | **Sinyal / Signal** |
| `Alert rules` (Topbar) / `Alarmlar` (menü) | `Alerts.tsx:400`, `i18n.tr.ts:147` | **Kurallar / Rules** |
| Servis sekmesi `Metrics`, URL anahtarı `details` | `Service.tsx:867` | ad kalır, anahtar §3.4'te `metrics`'e (eski `details` yönlenir) |

### 1.3 Dil sızıntısı

[Ö] M7: ürün ekranlarında katalog dışı **TR 2.103, EN 1.138**; yönetim kümesinde (çeviri dışı
karar) **TR 1.806, EN 677** — ayrı raporlanır, ratchet hedefine girmez. Karşılaştırma için
katalog: `i18n.tr.ts` 3.884, `i18n.en.ts` 3.988 değer [Ö] M11 tanımıyla.

En kötü 10 ürün dosyası (toplam / TR / EN):

| Dosya | Top. | TR | EN |
|---|---:|---:|---:|
| `components/dashboard/PanelEditor.tsx` | 93 | 55 | 38 |
| `pages/AIObservability.tsx` | 73 | 47 | 26 |
| `pages/Alerts.tsx` | 63 | 42 | 21 |
| `pages/Trace.tsx` | 58 | 38 | 20 |
| `components/ai/ChatBubble.tsx` | 57 | 50 | 7 |
| `pages/Runbook.tsx` | 56 | 31 | 25 |
| `pages/Traces.tsx` | 50 | 24 | 26 |
| `features/anomalies/ProblemDetail.tsx` | 48 | 18 | 30 |
| `pages/Explore.tsx` | 46 | 28 | 18 |
| `pages/EntityDetail.tsx` | 43 | 24 | 19 |

Not: `/ai` (AIObservability) menüde yalnız admin+editör (`Sidebar.tsx:171`) ama /admin ya da
Settings değil; bu denetim onu ürün kümesinde saydı (§Açık kararlar A6).

**Ratchet testi tasarımı (`i18nLeakRatchet.test.ts`):** `styles/inlineStyleRatchet.test.ts`
deseni — `stripTsComments` + M7 tanımı birebir; `CEILINGS` sayfa başına (mevcut
`styles/pageFiles.ts` `PAGE_FILES` eşlemesi), `TOTAL_TR = 2103`, `TOTAL_EN = 1138`; yönetim
kümesi sayılır ama tavanı yoktur (yalnız rapor satırı). Kural: sayı yalnız azalır; azaltan
dilim tavanı aynı commit'te düşürür. Yeni dosya tavansız eklenemez (TOTAL korur). Sezgisel
yanlış pozitif (kod sabitleri) için dosya içi gerekçeli istisna: `// i18n-exempt: <neden>`
satırı — satır numarası değil gerekçe anahtar (skill §8 kuralı).

### 1.4 İç jargon — nerede üretiliyor

| Sızıntı | Üreten yer [K] | Mevcut saf fonksiyon |
|---|---|---|
| Kural kimliği kullanıcıya | `features/anomalies/ProblemDetail.tsx:1291` (`<dd className="mono">{problem.ruleId}`) | `lib/problemStandardView.ts:332` `problemTitleView` (kind → okunur başlık) |
| Anomali Inbox başlığı `kind · pattern` | `internal/api/inbox.go:2676` | `lib/anomalyNarrative.ts:15,23` `anomalyPlainHeadline` / `anomalyPlainSummary` |
| Ham oran "peak 3.2x" | `pages/inbox/InboxCells.tsx:198` | `lib/anomalyBaseline.ts:18` `anomalyRatioLabel` |
| ML kayıt başlığı "… tespit anında X, normal üst sınır Y" (9 dedektör) | `internal/ml/*_record.go` (`rec.Title = fmt.Sprintf`; ör. `volume_record.go:224`) | `internal/ml/impact_sentence.go:13` `ImpactSentence`, `internal/ml/explain_card.go:912` `ExplainSummary` |
| Standart başlık | `internal/chstore/problem_standard.go:1125,1229,1296` (`std.Headline`) | `lib/problemStandardView.ts:257,360` `stdHeadlineView` / `splitStdHeadline` |
| Uyarı başlığı (bildirim) | `internal/notify/alert_title.go:56` | aynı |
| "Neden Problem değil" | — | `lib/whyNotProblem.ts:30` `whyNotText` |
| Yanık oranı "burn 1.23×" | `pages/Service.tsx:843`, `pages/Slos.tsx:340,368,377` | yok (dilim 3'te yazılır) |

Dilim 3 ilkesi: başlık ve özet **tek saf fonksiyon çiftinden** (`problemTitle(p, lang)`,
`problemSummary(p, lang)`), sunucu `std.Headline`'ı birincil, istemci yalnız çevirir; kimlik
(`ruleId`) yalnız "Ayrıntılar" açılır alanında, mono.

**Dilim 3 durumu (v0.10.2487).** Ürün yüzeyinde kapatılan satırlar: kural kimliği (Problem
detayında kapalı "Ayrıntılar" alanında, mono); anomali Inbox başlığı (`chstore.AnomalyEventHeadline`,
standart manşetle aynı cümle; tanınmayan tür / ML dedektör kimliği cümlede yok → "sinyal" /
"ML sinyali"); ham oran (`InboxCells` "tepe normalin 3,2× · şimdi 1,1×", tabansızda "taban yok");
standart başlık (liste satırı, detay başlığı ve standart blok tek çift `features/anomalies/
problemTitle.ts` + tek çizim `ProblemTitleText`, ayırıcı " · "); "Neden Problem değil" (zaten
saf, sızıntı yok); yanık oranı (`Service.tsx`, `Slos.tsx` "bütçe tüketimi 1,23×" + düz ipucu,
`lib/format/number` `multiplier`). **Açık kalan iki satır** (ürün kodu dışı karar gerektirir):

| Sızıntı | Neden açık |
|---|---|
| ML kayıt başlığı (9 dedektör, `internal/ml/*_record.go`) | Başlık metni yayın kapısının tekrar ölçüsüne (`publish_gate.go:261` `normText(r.Title)`) ve açıklama kartının özetine (`explain_card.go:945`) giriyor; değiştirmek dedektör / yayın davranışını değiştirir — dedektörler bu dilimin kapsamı dışında. |
| Uyarı başlığı (bildirim) | Bildirim metni ayrı operatör onayı ister (dilim 3 kapsam dışı). Bugün: standart şablonda `internal/notify/tr_template.go:62` `StandardizeProblem` → `std.Headline` (`trTopic` :236, sohbet başlığı `trChatTitle` :251, e-posta konusu `trEmailSubject` :246); eski şablonda (`NotifyTemplateLegacy`) `internal/notify/alert_title.go:56` `alertTitle` = `[SEV][Px] <servis> — <kural adı>` (`notify.go:1225,1750,1791-1793`). Ekrandaki cümle ile eşitlemek bu kararı bekliyor. |

### 1.5 Açıklama paragrafları

[Ö] M9: ürün ekranlarında **93 açıklama paragrafı, 57 dosya.** En yoğun:
`coremetry/anomalies/MLReplaySheet.tsx` 6, `coremetry/problems/ProblemBreakdownBlocks.tsx` 6,
`coremetry/problems/ProblemOriginBlocks.tsx` 5, `coremetry/anomalies/MLSignalEvidence.tsx` 4,
`MLRecordCard.tsx` 3, `MLRecordChart.tsx` 3, `pages/Clusters.tsx` 3,
`pages/ai/PendingCasesPanel.tsx` 3. Kayıtlı görünüm çubuğunun boş satırı da bu sınıfta
(`components/SavedViewsBar.tsx:262-265`, "(none yet — …)", §4.2).
Kural: *veri yokken* açıklama → `Empty` gövdesi; *veri varken* açıklama → başlıktaki `(?)`
yardım balonu (`ui/Tooltip` / `coremetry/overlay/Tooltip`); kalıcı paragraf yok. Ratchet
`helpParagraphRatchet` taban 93.

---

## BÖLÜM 2 — Biçim kuralları

[Ö] M5: **164 biçimleyici tanımı, 85 dosya** — sayı 56, süre 46, zaman 42, yüzde/oran 14, bayt 6.
Aynı ad tekrarları: `fmtMs` 9, `fmtCount` 7, `fmtPct` 6, `ago` 5, `fmtNum` 4, `fmtPerMin` 4,
`fmtRatio` 3, `fmtBytes` 3, `agoLabel` 3, `fmtVal` 3, `fmtClock` 3, `fmtRate` 3; 2'şer:
`fmtRps`, `fmtDur`, `durationTR`, `fmtSeconds`, `fmtShare`, `fmtUptime`, `fmtSec`, `fmtCores`.
En yaygın ortak olanlar (çağrı, `rg -c '\b<ad>\('` ürün+yönetim): `fmtNum` 453, `tsLong` 148,
`fmtBytes` 83, `fmtDateTime` 72, `fmtSmart` 57, `fmtClock` 49, `fmtNs` 43, `fmtFixed` 33.
Dil ayrımı bile tek değil: `lib/utils.ts:177` `agoTR` + `lib/i18n.ts:289` `agoLang` + `fmtAgoNs`.

[Ö] M6: **873 ham çağrı** (`toFixed` 616, `toLocale*` 244, `new Intl.*` 13). 333'ü biçimleyici
tanımlayan dosyalarda, **540'ı dışarıda** (74'ü yönetim kümesinde). Dışarıdaki en yoğun:
`pages/AIObservability.tsx` 19, `pages/service/OperationsTable.tsx` 16,
`pages/databases/detailSections.tsx` 14, `features/anomalies/ExceptionPodsPanel.tsx` 11,
`pages/settings/SamplingRuleShadowSection.tsx` 11, `components/DeployHistoryPanel.tsx` 10,
`components/coremetry/logs/FieldSidebar.tsx` 10, `features/dependencies/DetailDrawer.tsx` 9,
`components/SpanDetail.tsx` 9, `pages/EntityDetail.tsx` 9.

**Tek modül önerisi: `lib/format/`** (saf, `Lang` alır, React'siz; ağaç sallanabilir tek tek
dışa aktarım):

| İşlev | Kural |
|---|---|
| `duration(ms, lang)` | 3 anlamlı hane, tek birim, birim yazımı sabit: `850 ms`, `1,24 s`, `12,4 s`, `3,2 dk`, `1,5 sa` (EN `min`, `h`); ns/µs girişleri `durationNs`; asla `1m 12s` gibi iki birim (tek istisna: `fmtStdDuration` "açık süre" — §Açık kararlar A7) |
| `count(n, lang)` / `compact(n)` | binlik ayırıcı dile göre (TR `.`, EN `,`); `compact` 1,2 B / 3,4 Mn — eksenler `lib/chartFmt.ts:16 fmtSmart`'a bağlı kalır (grafik birimleri `dataFrame.ts` köprüsünde, skill §6 sınırı) |
| `percent(v, lang, {from:'ratio'\|'pct'})` | girdinin ölçeği **tipte** zorunlu (bugün kesir/yüzde karışık: `TopologyFlowGraph.tsx:707` kesir, diğerleri yüzde); 3 anlamlı hane; TR `%12,4`, EN `12.4%` |
| `bytes(n)` | IEC (KiB/MiB) mi SI mi — tek karar (§Açık kararlar A7) |
| `relTime(ns, now, lang)` + `<Time ns>` | göreli birincil ("3 dk önce"), mutlak ISO yerel saat `title`/Tooltip'te; mutlak gerekli yerde (`tsLong`) aynı modülden |
| `id(s)` | mono yalnız kimlik/yol/kod: `trace id`, `span id`, yol, SQL, pod adı; sayıya mono yasak (`tableUnityRatchet` `numMono: 0` zaten tutuyor, `styles/tableUnityRatchet.test.ts:89`) |

Ratchet (`formatUnityRatchet.test.ts`, inlineStyleRatchet deseni): (a) `lib/format/` dışında
M6 eşleşmesi — taban **873** (dilim 1 sonunda ölçülen değer tabana yazılır); (b) `lib/format/`
dışında M5 tanımı — taban **164**; ikisi de dosya başına tavan + TOTAL, yalnız azalır.
`components/ui` donuk olduğu için içindeki ham çağrılar sayılır ama tavanı sabittir.

---

## BÖLÜM 3 — Entity sözleşmesi

### 3.1 Bugün ne var (zemin)

- **Kimlik modeli [K]** `internal/entity/identity.go:18-34,66-71,79-88`: türler `cluster`, `node`,
  `namespace`, `workload`, `pod`, `container`, `service`, `database`, `queue`, `external`, `host`;
  önekler `cluster ns wl pod ctr svc db queue ext host node`; `ServiceID(name) = "svc:" + name`
  **(cluster'sız)**, `PodID = pod:<cid>/<ns>/<pod>`, `WorkloadID = wl:<cid>/<ns>/<kind>/<name>`;
  `db/queue/ext` kimliği topoloji düğüm kimliğiyle birebir (`internal/chstore/identity.go:90`,
  ör. `queue:kafka:<topic>` — cluster yok).
- **Komşuluk [K]** `internal/entity/neighbours.go`: `serves` (wl→svc), `same_as` (eski→kanonik),
  `ServiceWorkloads` / `WorkloadServices`; ilişki tipleri `parent`, `runs_on`, `runs`, `serves`,
  `same_as`, `calls` (`identity.go:38-48`). Canlı dizin Redis blob'u (`internal/entity/live.go`).
- **API [K]** `internal/api/entities.go:38-46`: `GET /api/entities`, `/api/entities/clusters`,
  `/api/entity?id=` (15 s `serveCached`, `:181`), `/api/entity/services|metrics|containers|latency`,
  `/api/services/{name}/pods`. Kimlik `/` taşıdığı için yol segmenti değil `?id=` (`:40-41`).
- **Ön yüz üreticileri:** M2 tablosu; `lib/entityHref.ts:26` pod → `/pod`, cluster →
  `/clusters?cluster=<AD>`, diğerleri → `/entity?id=`.
- **Komşu paneli:** `components/ServiceNeighbors.tsx:24` (topology_edges_5m, tek tüketici
  `pages/service/Overview.tsx:1358`).

### 3.2 Tür listesi ve tek ikon

Yalnız `lucide-react` (shadcn-phase1-plan §7.5); yeni ikon çizilmez. [Ö] M14 = bugünkü import.

| Tür | Kimlik (bugün) | İkon | Bugün kullanılıyor mu |
|---|---|---|---|
| servis | `svc:<ad>` | `Boxes` | evet, sidebar Services |
| endpoint / operasyon | entity modelinde YOK; `service+method+route(+family,sig)` | `Webhook` | evet, sidebar Endpoints |
| veritabanı | `db:<system>:<instance>` (sayfa ek olarak `name`) | `Database` | evet, sidebar |
| kuyruk / topic | `queue:<system>:<topic>` (sayfa ek olarak kafka `cluster`) | `MessageSquare` | evet, sidebar Messaging |
| cluster | `cluster:<cid>` | `Server` | evet, sidebar Clusters |
| node | `node:<cid>/<node>` | `Cpu` | evet (`ai/ModelPicker.tsx`, başka anlamda) |
| namespace | `ns:<cid>/<ns>` | `Layers` | evet (`lib/signalKind.ts:24`, "grup" anlamında — çakışma) |
| workload | `wl:<cid>/<ns>/<kind>/<ad>` | `Package` | hayır (kütüphanede var) |
| pod | `pod:<cid>/<ns>/<pod>` | `Box` | hayır |
| dış bağımlılık | `ext:<host>` | `Globe` | evet (`pages/Runbook.tsx`) |
| host | `host:<ad>` | `HardDrive` | hayır |

Glif kullanan yüzeyler (`↩ ≡ ⇄ ⚠ ∿ ⋮`, `Service.tsx:567-600`) ve `CosreScopeSearch.tsx:19`
(`Search`/`Workflow`) bu tabloya geçer. `Layers` çakışması: `signalKind` "Grup" için başka
ikona geçer (dilim 4 kapsamı).

### 3.3 Adres şeması `/e/<tür>/<kimlik>`

Rota `App.tsx`'e tek giriş: `/e/:kind/*` (react-router splat; kimlik `/` taşır). Kimlik gövdesi
entity önekinden sonraki kısım, **her segment `encodeURIComponent`**; ek bağlam (range, env, at,
tab) sorguda kalır. Kodek `lib/entityRoute.ts` — `lib/urlSchema` üstünde (`urlSpec`, `zParam`,
`zText`), türe göre ayrıştırıcı kayıttan (§3.7).

| Tür | Yol | Not |
|---|---|---|
| servis | `/e/service/<ad>` (+ `?cluster=&ns=` kapsam) | brief: kimlik `cluster+namespace+service.name`; entity modeli bugün cluster'sız (`identity.go:83`) — **§Açık kararlar A1** |
| endpoint | `/e/endpoint/<servis>/<method>/<route>` (+`?family=grpc&sig=1&entry=rpc`) | route `/` taşır → splat'in sonu |
| veritabanı | `/e/database/<system>/<instance>` (+`?name=`) | `name` boşsa "instance'taki her DB" (bugünkü dürüstlük kuralı, `databaseParam.ts:1-10`) |
| statement | `/e/database/<system>/<instance>/statement/<kodek>` | alt kaynak; tür değil |
| topic | `/e/queue/<system>/<cluster>/<topic>` | modeldeki `queue:` kimliğine kafka cluster eklenir (A1 ile aynı soru) |
| cluster | `/e/cluster/<cid>` | id; ad kabul edilip id'ye çevrilir (bugün ad taşınıyor) |
| node | `/e/node/<cid>/<node>` | |
| namespace | `/e/namespace/<cid>/<ns>` | |
| workload | `/e/workload/<cid>/<ns>/<kind>/<ad>` | |
| pod | `/e/pod/<cid>/<ns>/<pod>` (+`?at=`) | |
| dış bağımlılık | `/e/external/<host>` | |
| host | `/e/host/<ad>` | |

**Yönlendirme tablosu (eski → yeni), 14 satır.** Hepsi `QueryRedirect` deseni (`App.tsx:117-121`
— sorgu dizesi korunur) + saf eşleme fonksiyonu; `appRedirects.pin.test.ts` genişler.

| # | Eski | Yeni | Taşınan |
|---|---|---|---|
| 1 | `/service?name=X&tab=T` | `/e/service/X?tab=T'` | `tab` §3.4 eşlemesiyle; range/env/op aynen |
| 2 | `/service/backtrace?name=X` | `/e/service/X?tab=relations&view=callers` | range |
| 3 | `/endpoint?service&path&method…` | `/e/endpoint/…` | env/cluster/compare/src |
| 4 | `/database?system&instance&name&source` | `/e/database/…?name&source` | range/env |
| 5 | `/databases/statement?stmt=` | `/e/database/<sys>/<inst>/statement/<kodek>` | kodekten sys/inst; çözülemezse eski sayfa kalır (A4) |
| 6 | `/pod?cluster&namespace&pod` | `/e/pod/<cid>/<ns>/<pod>` | `cluster` ad ise id'ye çözüm sayfada (ClusterByRef bugünkü davranış) |
| 7 | `/entity?id=<önek>:…` | `/e/<tür>/…` | `at`, range |
| 8 | `/messaging/topic?system&cluster&destination&tab` | `/e/queue/…?tab` | |
| 9 | `/messaging?destination=a\|b\|c` (çekmece) | `/e/queue/a/b/c` | `legacyEndpointTarget` emsali (`endpointParam.ts:98`) |
| 10 | `/clusters?cluster=X` | `/e/cluster/<cid>` | `section` → sekme |
| 11 | `/clusters?cluster=X&namespace=N` | `/e/namespace/<cid>/N` | |
| 12 | `/hosts?host=H` | `/e/host/H` | |
| 13 | `/external?host=H` | `/e/external/H` | |
| 14 | `/endpoints?endpoint=` / `?detail=` (zaten eski) | `/e/endpoint/…` | mevcut `legacyEndpointTarget` hedefi değişir |

`/databases?row=` eski çözücüsü (`databaseParam.ts:124`) zincir yapmadan doğrudan `/e/…`'ye
yönlenir (tek atlama kuralı). Liste sayfaları (`/services`, `/endpoints`, `/databases`,
`/messaging`, `/clusters` çıplak) yerinde kalır. Saklanan `saved_views` satırları ve sohbet
bağlamı: `lib/pageContext.ts` `ROUTE_PAGES` iki yönlü kapsama testi (`pageContext.test.ts`) ve
`lib/chatContext.ts:55` `SERVICE_PARAM_ROUTES` aynı dilimde güncellenir — yoksa CoSRE o
rotada bağlamsız açılır (cross-page-context §Faz 2 şartı).

**Giriş paketi riski:** yönlendirme bileşenleri `App.tsx`'te yani girişte; `lib/urlSchema`
zod taşır. Emsal `components/AnomaliesListRedirect.tsx:2` ("yaprak: zod girişe girmez"):
eşleme fonksiyonları zod'suz yaprak modül, zod doğrulaması tembel `/e/` sayfasında.

### 3.4 EntityHeader ve sekme sırası

**EntityHeader (props sözleşmesi, `components/coremetry/entity/EntityHeader.tsx`):**
`kind` (kayıttan ikon + tür adı) · `name` · `health` (aşağıdaki tek tanım) · `owner?` (katalog
pill'i, bugün `ServiceCatalogPill`) · `scope?` (`env`, `cluster`, `namespace` — kapsam rozeti,
bugün EndpointDetail'in `env=… · cluster=…` rozeti) · `metrics: [ölçü, ölçü, ölçü]` (3 ana ölçü,
türe göre kayıttan: servis/endpoint = istek/s · hata % · p95; DB = çağrı/s · hata % · p95;
topic = mesaj/s · lag · tüketici sayısı; pod = CPU · bellek % · restart; node/cluster = CPU ·
bellek · hazır olmayan pod) · `crumbs` (liste sayfasına `navHref`) · `actions?` (en çok 2:
Ask CoSRE + türe özgü bir eylem; gezinme düğmesi değil). Başlıkta ad bir kez, `<h1>` bir tane.

**Sağlık noktasının tek tanımı.** Bugün **6 adlandırılmış tanım (7 fonksiyon) + 14 satır içi eşik biçimi** [K][Ö]:

| # | Tanım | Kural |
|---|---|---|
| H1 | `internal/api/api.go:1913` `scoreHealth` → `/api/services` `health` → `pages/Services.tsx:1102` `HealthDot`, `ServiceMapNodeDrawer.tsx:47` | açık kritik Problem ya da hata >%5 → kırmızı; açık uyarı/bilgi Problem ya da hata >%1 → sarı |
| H2 | `pages/Service.tsx:87` `serviceHeadDot` | yalnız hata oranı >5 / >1 (Problem'i görmez) |
| H3 | `lib/health.ts:12` `healthToken` (topoloji) | hata >5 / >1 |
| H4 | `internal/healthverdict/verdict.go:198` `Service` (CoSRE hükmü) | RED + taban + açık Problem + hatalı trace |
| H5 | `pages/pod/podSentence.ts:48` ve `pages/clusters/nsDetailModel.ts:34` — **iki ayrı `podStatusWord`** | faz / bekleme nedeni |
| H6 | `pages/service/infraClusters.ts:150` `clusterStatus` | pod toplamları |
| — | satır içi [Ö] M8 | 50 satırda 14 biçim: `>5/>0` 14 satır (10 dosya), `>=5/>=1` 8 (6), `>5/>1` 5 (4), `>0` 4, `>1` 4, `>=5` 4, `>5` 3, `>=1` 2, kesir `>0.05/>0.01` 1 (`TopologyFlowGraph.tsx:707`), `>0.1/>0.02` 1 (`AIObservability.tsx:268`) … |

Öneri: **sağlık = açık Problem'den** (H1'in Problem yarısı): açık kritik → kırmızı, açık
uyarı → sarı, yok → nötr halka (K5 "sağlıklı renk almaz" kuralı korunur). Hata oranı eşiği
sağlık değil *sapma rengi*dir: tek yerde `lib/format/tone.ts` `errorTone(pct)` (≥5 err, ≥1
warn) ve hücrelerde yalnız o. Sunucu `scoreHealth`'in hata-oranı dalları kalkar mı — bu bir
gösterim kararı değil Problem üretim kararı (hata >%5 bugün Problem açıyor mu?) → §Açık
kararlar A2. Tek bileşen: `HealthDot` (Services.tsx'teki tanım `components/coremetry/`a
taşınır, satır içi stil sınıfa).

**Sabit sekme sırası:** Genel · Problemler · Trace · Log · Metrik · İlişkiler · Değişiklikler ·
türe özgü. URL anahtarları: `overview`, `problems`, `traces`, `logs`, `metrics`, `relations`,
`changes`, türe özgü anahtar. Türde olmayan sekme **gizlenir** (boş sekme yok).

**Tür × sekme matrisi** — V = var, T = mevcut yerden taşınır (kaynak), — = yok:

| Tür | Genel | Problemler | Trace | Log | Metrik | İlişkiler | Değişiklikler | Türe özgü |
|---|---|---|---|---|---|---|---|---|
| servis | V Overview | T `ServiceAttentionStrip` + ⚠/∿ düğmeleri | T ⋮ Traces düğmesi → gömülü liste | V Logs (`ServiceLogsTab`) | V `details` + T HTTP/GRPC + T Messaging | T Topology + Backtrace sayfası + Infrastructure + `ServiceNeighbors` | T GitOps + `DeployHistoryPanel` (Details içinde) | Operasyonlar (V Operations), Endpoint'ler (T Details bölümü + ⇄ düğmesi) |
| endpoint | V gövde | T `ExceptionsSection` | T `FailingTracesSection` + Traces → | — | V `MetricREDStrip`/histogram | T `CallersSection` + `WhereTheTimeGoesSection` | — | Kod (`OperationCardCodeSection`) |
| veritabanı | V sinyal şeridi | T `DatabaseErrorsSection` | T Traces → | — | V `DatabaseTrendCards` | V `DatabaseCallersSection` | — | Sorgular (`DatabaseStatementsSection`) |
| topic | V Overview | — | V Traces sekmesi | — | V Kafka metrikleri | T Consumers + Producers sekmeleri birleşir | — | Lag |
| pod | V durum cümlesi + meta | — | V `PodTracesTable` | V `PodLogsSection` | V Thanos trend + JMX | T meta satırı (node, workload) | — | Konteynerler |
| node / namespace / workload | V `/entity` gövdesi | — | — | T log kapsama işareti (v0.10.2263) | T `/api/entity/metrics` | V ebeveyn/çocuk + servis tablosu | workload: T `/rollouts?workload=` linki | Ömürler |
| cluster | V Overview | — | — | — | T Overview kutuları | T Nodes · Namespaces · Pods sekmeleri | — | — |
| dış bağımlılık, host | T çekmece gövdesi | — | T | — | T | T çağıranlar | — | — |

**Üst düğme satırı:** sekmeyle çakışan her düğme kalkar — servis başlığındaki Logs (Logs
sekmesi zaten var), Backtrace, Endpoints, Problems, Signals, Traces; Endpoint/DB'deki
`Traces →`; Pod pivotları; Topic `Traces →`. Kalan: Sağlık kontrolü (eylem), Ask CoSRE, Alarm
(eylem). "Bu sayfayı Traces'te aç" ihtiyacı Trace sekmesinin içinde tek link olur.

### 3.5 EntityLink

Tek bileşen `components/coremetry/entity/EntityLink.tsx`; props: `kind`, `id` (ya da türe
özgü ref), `label?`, `range?`, `env?`, `at?`, `variant?: 'cell'|'inline'|'title'`. `href`'i
kayıttaki üretici kurar (M2 tablosundaki 7 üretici bu kaydın arkasına çekilir; `SubjectLink`'in
"bağlanamayan özne" dalı aynen korunur — `components/SubjectLink.tsx:47-63`). Görünüm tek:
ikon (12 px) + ad, sayısal değilse mono değil, kimlik ise ortadan kırpma (`MiddleEllipsis`).
Tablo hücresi, Problem satırı, trace span'i, CoSRE kartında aynı bileşen.

**Hover kartı:** sağlık + 3 ölçü + pivotlar (Trace/Log/Metrik sekmelerine derin link).
Bütçe kuralları:
- **Gecikme 400 ms** [V], fare ayrılınca iptal (`AbortSignal`); satır başına önden istek **yok**.
- **Önbellek:** React Query anahtarı `['entity-hover', kind, id, rangeBucket]`, `staleTime`
  60 s — aynı sayfada aynı entity ikinci kez istek atmaz.
- **Kaynak uçlar (yeni uç yok):** servis → `GET /api/services?name=<ad>&limit=5` (hot uç,
  `serveCached`, önbellek anahtarı tüm girdileri taşıyor `api.go:1655`; `health` +
  `healthReason` + RED aynı satırda, istemci tam ad eşleşmesini seçer); pod/node/namespace/
  workload/cluster → `GET /api/entity?id=` (15 s önbellek, `entities.go:181`); endpoint/DB/topic
  için hafif özet ucu **yok** — v1'de hover kartı yalnız satırın zaten taşıdığı sayıları
  gösterir (istek atmaz), özet ucu §Açık kararlar A5.
- **Toplu (batch):** tablo görünür satırlarının servislerini tek `/api/services` sayfa
  isteğiyle ısıtmak mümkün ama hot uç bütçesini satır sayısıyla çarpar → v1'de yok.
- **Paket:** `EntityLink` gövdesi küçük (ikon + `<Link>`); kart `lazy(() => import(…))` ile
  ayrı parça; ilk hover'da yüklenir.

### 3.6 İlişkiler paneli

Tek bileşen `coremetry/entity/RelationsPanel` — dört grup: **çağırır** (`calls`, kaynak
`topology_edges_5m`, bugünkü `ServiceNeighbors` verisi) · **çağrılır** (ters `calls`;
bugünkü Backtrace sayfası ve `DatabaseCallersSection` / `CallerSection`) · **üzerinde çalışır**
(`runs`/`runs_on`/`serves`; `internal/entity/neighbours.go` `ServiceWorkloads`,
`/api/services/{name}/pods`) · **sahibi** (`parent`; `/api/entity` ebeveyn zinciri). Her satır
`EntityLink`. Pod/servis/workload/cluster bağı **yalnız entity modeli üzerinden** — ad eşitliği
yok (duran karar). Bugünkü `neighbours.go` yorumu ad eşitliğini "son çare" yedek olarak
tutuyor (`neighbours.go:15-16`); panel bu yedekten gelen satırı ayrı işaretler.

### 3.7 Gelecekteki "iş işlemi" türü

Kayıt tek dosya `lib/entityKinds.ts`: `{ kind, prefix, icon, labelKey, parse(path) → ref,
encode(ref) → path, href(ref, ctx), headerMetrics: {keys, source}, tabs: TabKey[], hover:
{source} }`. `/e/:kind/*` rotası, EntityHeader, EntityLink, sekme şeridi ve yönlendirme
kodeği yalnız bu kayda bakar. "İş işlemi" (`kind: 'txn'`) eklemek: ön yüzde bir kayıt
girdisi + i18n anahtarı; arka yüzde `internal/entity/identity.go` `typePrefix` haritasına bir
satır (`ParseID` ve `Ref.String` haritadan okur, `:118-120` dalı `db/queue/ext` gibi) + kendi
okuma ucu. Bileşen kodu değişmez; kapı testi "kayıttaki her tür için href→parse gidiş-dönüş".

---

## BÖLÜM 4 — Sayfa şablonları

Dört şablon: **Liste** (başlık · tek FilterBar · tek özet satırı · tablo), **Entity detayı**
(EntityHeader · sabit sekmeler), **Olay detayı** (başlık cümlesi · etki · kanıt · eylemler),
**Ayar** (sol alt menü · form). Mevcut iskelet `coremetry/PageShell` 75 kullanım / 50 dosya.

### 4.1 Filtre kontrolleri — tek set

[Ö] M12: `<select>` 119 (48 dosya; yönetim hariç 104/41), `<SegmentedControl>` 72 (55),
`<Chip>` 85 (36), `<DropdownMenu>` 12 (9), `<FacetMultiSelect>` 7 (2), `<FilterBar>` 2 (2),
`className="controls"` 16 (13). Sekme şeridi üç uygulama [Ö] M13: `<TabStrip>` 14,
`<UiTabStrip>` 2, shadcn `<TabsList>` 9.

| Kontrol | Ne zaman |
|---|---|
| FilterBar çipi (`coremetry/FilterBar`) | anahtar=değer süzgeci (servis, namespace, durum …), sunucu araması |
| SegmentedControl | 2–4 karşılıklı dışlayan görünüm kipi (tablo/grafik, kaynak) — süzgeç değil |
| Açılır düğme (DropdownMenu) | 5+ seçenekli tek seçim ya da eylem menüsü |
| native `<select>` | yalnız form (Ayar şablonu); liste/detay sayfasında yok |

Ratchet `filterControlRatchet`: ürün kümesinde `<select>` taban 104, `className="controls"`
taban 16; sekme şeridi uygulaması 3 → 1 (shadcn Tabs `line`, Clusters S5 emsali).

### 4.2 Kayıtlı görünümler

`SavedViewsBar` 10 sayfada; 8'i `bar` kipinde ve boşken "(none yet — …)" satırı çiziyor
(`components/SavedViewsBar.tsx:262-265`); Traces ve Logs `compact` (menü) kipinde. FilterBar'da
`saved` yuvası zaten var (`coremetry/FilterBar/FilterBar.tsx:27`). Kural: kayıtlı görünüm her
zaman `compact` menü olarak FilterBar'ın `saved` yuvasında; boş liste satır kaplamaz, menüde
"Kayıtlı görünüm yok" yazar. Taban: `bar` kipi 8 → 0.

### 4.3 Satır eylemleri

[Ö] M15: 46 `kind: 'actions'` sütunu, 29 dosya. **Çakışma:** DECISIONS "Tablo standardı" T8
"tek eylem sütunu (⋯ hep görünür, soluk)" (`docs/DECISIONS.md:734`); brief "hover/menü"
diyor. Öneri T8'e uyar: satır başına en çok bir ikon + ⋯ menü, ⋯ hep görünür (klavye ve
dokunmatik için hover-only gizleme yok); eylemler menüye. §Açık kararlar A8.

### 4.4 Ad tekrarı

Bugün servis sayfasında ad 3 kez ve iki `<h1>`: Topbar `<h1>{title}</h1>`
(`components/Topbar.tsx:51`, başlık `Service · <ad>`), breadcrumb son öğesi, `.svc-title`
`<h1>` (`Service.tsx:543`). Pod (`Pod · <ad>` + kimlik satırı) ve Backtrace aynı.
**Kalan:** Topbar = bölüm adı (i18n anahtarı, ör. "Servisler"), breadcrumb = yalnız üst düzey
(liste) bağlantısı, ad yalnız EntityHeader `<h1>`'inde. Topbar `<h1>` → başlık rolü olmayan
metin (sayfada tek `<h1>`). [Ö] M10: Topbar başlığı 58 yerde sabit metin, 15 yerde ifade.

### 4.5 53 sayfanın şablon ataması

| Şablon | Rotalar |
|---|---|
| Liste (24) | `/services`, `/traces`, `/logs`, `/endpoints`, `/search`, `/runbooks`, `/databases`, `/databases/slow-queries`, `/external`, `/hosts`, `/clusters` (çıplak), `/messaging`, `/dashboards`, `/inbox`, `/problems`, `/rollouts`, `/alerts`, `/watchers`, `/slos`, `/monitors`, `/events`, `/ai`, `/shift`, `/users` |
| Entity detayı (9) | `/service`, `/service/backtrace` (sekmeye iner), `/endpoint`, `/database`, `/databases/statement` (DB alt kaynağı), `/pod`, `/entity`, `/messaging/topic`, `/clusters?cluster=` (aynı rota, detay kipi) |
| Olay detayı (6) | `/trace`, `/anomalies/:id`, `/anomalies/ml/:id`, `/runbook-exec`, `/sre/report/:id`, `/cosre/shared/:id` |
| Ayar (4) | `/settings/:section`, `/system/:tab`, `/profile`, `/users/:id` |

**Şablona uymayanlar (11), gerekçe:** `/login` (kimlik doğrulama, kabuk dışı) ·
`/cosre` (kromsuz sohbet, `lib/cosrePage.ts` kabuk dalı) · `/public-status` ve
`/public/trace` (kimliksiz kamu yüzeyi; `/public/trace` olay detayının salt-okunur ikizi) ·
`/service-map` (graf tuvali) · `/explore` ve `/metrics` (sorgu çalışma alanı) · `/dashboard`
(pano düzenleyici) · `/runbook` (belge düzenleyici) · `/trace/compare` (iki olayın farkı) ·
`/profiling` (alev grafiği). Sağlama: 24 liste + 8 benzersiz detay rotası (`/clusters` iki satırda) + 6 olay
+ 4 ayar + 11 uymayan = 53 sayfa [K] `App.tsx:176-259`. `/shift` rapor niteliğinde ama tablo
ağırlıklı olduğu için Liste'de.

---

## BÖLÜM 5 — Koruma mekanizması

### 5.1 `/design` sözleşme sayfası

Bugün: `pages/Design.tsx` (273 satır) düğme ailesi + `pages/design/ShadcnCatalog.tsx`; yalnız
`vite dev` (`App.tsx:96`, `Design.pin.test.ts`); CI'da `designAxe.test.tsx` `CI_CELLS` =
kayıttaki `ciCell:true` temalar (`pages/design/cells.ts:12`, `lib/themeRegistry.ts:57-67`:
dark, light, Kurumsal, Yüksek Kontrast), gece tüm hücreler (`.github/workflows/design-axe-nightly.yml`).
Ekleme: `Section`'lar — Liste şablonu (FilterBar + özet + tablo, demo satırlar), Entity
detayı (EntityHeader + sekme şeridi, her tür için bir örnek), Olay detayı, Ayar;
EntityLink (+ açık hover kartı statik örnek), HealthDot üç hâl, `lib/format` örnek tablosu
(her işlev × TR/EN). Örnek veri sabit fixture; axe matrisi bunları otomatik kapsar.

### 5.2 Görsel regresyon

Mevcut: `frontend/e2e/playwright.config.ts` (tek işçi, 1440×900, `retries: 0`),
`e2e/nightly.pw.ts` (temalar `dark,light,<Kurumsal>,contrast` × 10 rota, Web Vitals + axe,
rapor-önce), `.github/workflows/ui-nightly.yml` (compose yığını + `cmd/demo` 3 dk, 60 dk
tavan). Belirlenimcilik bugün YOK: `cmd/demo` `time.Now()` ve tohumsuz `math/rand` kullanıyor
(`cmd/demo/main.go:296,668`); Playwright'ta TZ / saat / hareket sabitlenmiyor; birim
testlerinde TZ yalnız `design:axe:full` betiğinde `TZ=UTC` (`frontend/package.json:10`).

Öneri `e2e/visual.pw.ts`:
- **Veri:** canlı demo değil, kaydedilmiş API fikstürleri — `page.route('/api/**')` ile HAR
  oynatma (kayıt: ui-nightly yığınından bir kez, `e2e/fixtures/*.har`, sentetik adlar). Böylece
  yığın gerekmez, `vite preview` yeter.
- **Sabitleme:** `timezoneId: 'UTC'`, `locale: 'tr-TR'`, `page.clock.install({ time: <sabit> })`,
  `reducedMotion: 'reduce'` + animasyon/geçiş kapatan enjekte CSS, `document.fonts.ready`
  bekleme, uPlot tuvali için `maxDiffPixelRatio` 0,002 [V].
- **Kapsam:** 10 ana sayfa (nightly ROUTES listesi; `/` ve `/topology` yönlendirme olduğu için
  yerlerine `/inbox` ve `/service`) × 3 tema (dark, light, Kurumsal) = 30 görüntü.
- **Süre [V]:** görüntü başına ~2–3 s + Chromium kurulumu ~60 s → ~2,5–3 dk.
- **Flake riski:** fikstürle düşük-orta; kalan kaynaklar tuval kenar yumuşatma, sanallaştırılmış
  tablo ölçümü, göreli zaman metni (saat sabitlenince biter), font yüklemesi.
- **Karar:** önce **gece işi** (ui-nightly'ye ayrı job, rapor-önce, 2 hafta); flake oranı 30 koşuda
  0 ise CI kapısına (ci.yml, `vite preview` + fikstür, yığınsız). Dilim 8.
- **`docs/screenshots` yenileme:** aynı koşu `UPDATE_SCREENSHOTS=1` ile dark tema görüntülerini
  `docs/screenshots/*.png`'ye yazar; 19 Ekim seti (10 dosya) aynı adlarla değişir.

### 5.3 Ratchet listesi ve başlangıç tabanları

Mekanizma: mevcut vitest ratchet deseni (`styles/inlineStyleRatchet.test.ts`: `CEILINGS` +
`TOTAL` + `stripTsComments`, yalnız aşağı). `.claude/baselines/` bugün yalnız Go için
(`api_go_lines`); ön yüz tabanları test dosyasında tutulur (mevcut 13 ratchet testiyle aynı).

| Ratchet | Tanım | Taban (v0.10.2482) | Dilim |
|---|---|---:|---|
| `formatUnityRatchet` (a) ham biçimleme | M6, `lib/format/` dışı | 873 | 1 |
| `formatUnityRatchet` (b) yerel biçimleyici | M5, `lib/format/` dışı | 164 | 1 |
| `i18nLeakRatchet` TR | M7 ürün | 2.103 | 2 |
| `i18nLeakRatchet` EN | M7 ürün | 1.138 | 2 |
| (rapor) yönetim sabit metin | M7 ADMIN | 1.806 TR / 677 EN | — |
| `topbarTitleRatchet` | M10 sabit `title="…"` | 58 | 2 |
| `helpParagraphRatchet` | M9 | 93 | 3 |
| `entityLinkRatchet` | M4 üreticili `<Link>` (EntityLink dışı) | 71 | 4 |
| `healthThresholdRatchet` | M8 satır | 50 (14 biçim) | 4–5 |
| `detailHeaderRatchet` | EntityHeader dışı detay başlığı | 7 | 5 |
| `tabStripRatchet` | M13 uygulama türü | 3 | 5 |
| `legacyRouteRatchet` | detay rotası `App.tsx`'te gerçek sayfa | 8 | 6 |
| `filterControlRatchet` | ürün `<select>` / `.controls` / SavedViews `bar` | 104 / 16 / 8 | 7 |

---

## 6. Dilimler (sıra zorunlu; her biri tek sürüm, `components/ui` donuk)

Giriş paketi bütçesi: `frontend/scripts/bundle-budget.mjs:28` `BUDGET_KB = 164`; payın ~1,4 KB
olduğu operatör bilgisi (bu denetimde `vite build` koşulmadı). Detay sayfaları tembel rota
parçasında olduğundan çoğu iş girişe dokunmaz; risk yalnız `App.tsx`, kabuk (Topbar, Sidebar,
⌘K) ve ortak `lib/` modüllerinden gelir.

| # | Kapsam | Giriş ölçütü | Çıkış ölçütü | Ratchet | Paket riski |
|---|---|---|---|---|---|
| 1 | `lib/format/` (süre, sayı, yüzde, bayt, göreli/mutlak zaman, `<Time>`, `errorTone`); `lib/utils` fmt* oraya delege; en yoğun 10 dosya | bu denetim | M5/M6 testleri yeşil, tabanlar yazılı; birim testleri (TR/EN, sınırlar) | formatUnity a/b | Orta: `lib/utils` girişte; yeni modül ağaç-sallanabilir tek tek dışa aktarım, girişe yalnız kabukta kullanılan `relTime` girer (<0,3 KB gz [V]) |
| 2 | Dil sızıntısı: menü + Topbar başlıkları + sekme adları i18n; §1.2 tek adlar; en kötü 10 dosya | 1 | Topbar sabit başlık 0; menü adları §1.2; TR/EN tavanları düştü | i18nLeak, topbarTitle | Düşük-orta: TR kataloğu tembel (v0.10.2403), EN girişte — yeni EN anahtarları girişe girer; yalnız kabuk anahtarları EN çekirdeğe |
| 3 | Problem başlık/özet: `problemTitle` / `problemSummary` saf çifti; `inbox.go:2676` başlığı; `ruleId` "Ayrıntılar"a; açıklama paragrafları Empty/yardım balonuna | 2 + soru 1 cevabı | Problem satırı/detay/bildirim aynı cümle; jargon tablosu §1.4 boş | helpParagraph | Düşük: Problem parçası tembel |
| 4 | `EntityLink` + kayıt (`lib/entityKinds.ts`) + tembel hover kartı; 7 üretici kaydın arkasında; `SubjectLink` EntityLink'e katlanır; `HealthDot` tek | 3 | üreticili `<Link>` 71 → 0 (izin listesi gerekçeli); hover p95 isteği satır başına 0 | entityLink, healthThreshold | **Yüksek dikkat:** EntityLink çok sayfada; gövde girişte olmamalı (sayfa parçalarında); kart `lazy` |
| 5 | EntityHeader + sabit sekme sırası, tür tür: önce servis (Backtrace sekmeye, üst düğmeler kalkar), sonra endpoint, DB, topic, pod, entity, cluster | 4 + soru 2 cevabı | `detailHeaderRatchet` 7 → 0; her tür §3.4 matrisi; servis `?tab=` eski anahtarları eşlenir | detailHeader, tabStrip, healthThreshold | Düşük: detay rotaları tembel; EntityHeader ortak parça (Vite ortak chunk) |
| 6 | `/e/:kind/*` + 14 satırlık yönlendirme tablosu; `ROUTE_PAGES`, `SERVICE_PARAM_ROUTES`, `saved_views` uyumu | 5 + soru 3 cevabı | eski detay rotaları yalnız yönlendirme; `appRedirects.pin.test.ts` 14 satır | legacyRoute | **Orta:** eşleme fonksiyonları girişte; zod'suz yaprak (§3.3) |
| 7 | Liste şablonu sayfa sayfa: tek FilterBar, kayıtlı görünüm menüde, tek özet satırı, satır eylemi T8 | 6 | `bar` kipi 0; ürün `<select>` ve `.controls` tavanları düşer | filterControl | Düşük |
| 8 | Görsel regresyon: fikstürlü `visual.pw.ts`, ui-nightly job; 2 hafta sonra CI kapısı; `docs/screenshots` aynı koşudan | 7 | 30 görüntü, 14 gece 0 flake → ci.yml | — | Yok (test altyapısı) |

---

## 7. Girdi dokümanlarında bayat satırlar

| Doküman / yer | Doküman diyor | v0.10.2482 gerçeği |
|---|---|---|
| `docs/ux-parity/10-foundation.md:4` | "51 rota" | 68 router girişi, 53 sayfa [Ö] M1 |
| `docs/ux-parity/10-foundation.md` §6 "Benimsememiş" | "/anomalies — features/anomalies/streams.tsx" | `/anomalies` yönlendirme (`App.tsx:220` → `/inbox?tab=signals`); liste Sinyaller sekmesi |
| `docs/frontend/shadcn-phase1-plan.md` §7.5 | "lucide-react … 30 files, ~72 icons" | 91 dosya import ediyor (`rg -ln lucide-react -g '!*.test.*' frontend/src \| wc -l`) |
| `docs/frontend/shadcn-phase1-plan.md` §8.1 | "Themes × densities 3 × 3 = 9 cells" | 6 tema kayıtlı, 4'ü CI hücresi (`lib/themeRegistry.ts:57-67`, `pages/design/cells.ts:12`) |
| `.github/workflows/design-axe-nightly.yml:3-5` | "5 tema = 15 hücre"; "CI yalnız 3 hücreyi koşar" | 6 tema × 3 yoğunluk = 18; CI 4 hücre |
| `.claude/skills/frontend-design-system/SKILL.md` §8 | "3 tema (dark varsayılan, light, …)" | 6 tema (`themeRegistry.ts:57-67`) |
| `docs/frontend/service-tabs-plan.md:9` | "Details — Service.tsx:625-747 … Performance ServiceCharts … Latency … DetailsToc" | Performance v0.10.2154'te kalktı (`Service.tsx:100`); Details `Service.tsx:670-750`; `ServiceCharts`, `ServiceLatencyHeatmap`, `DetailsToc` Service.tsx'te çizilmiyor |
| `docs/frontend/service-tabs-plan.md` §2 K1 | "RED üçlüsü Details/Performance'ta yaşar" | Performance bölümü yok (aynı kanıt) |
| `docs/plans/entity-layer-design-2026-08-28.md` §1.2 | tip tablosu: cluster…service | `database`, `queue`, `external` (v0.10.2240), `host` (v0.10.2260) eklendi (`internal/entity/identity.go:29-34`) |
| `docs/plans/entity-layer-design-2026-08-28.md` §5 | `GET /api/entities/{id}`, `/api/entities/{id}/services`, `/metrics` | `GET /api/entity?id=`, `/api/entity/services`, `/api/entity/metrics` (`internal/api/entities.go:42-46`) |
| `docs/plans/entity-layer-design-2026-08-28.md` §5 | "Cluster seçici URL `?cluster=<id>`" | `/clusters?cluster=<AD>` (`lib/entityHref.ts:33`) |
| `docs/plans/cross-page-context-2026-08-23.md:26` | "`serviceHref` 56 çağrımla" | 102 çağrı / 69 dosya [Ö] M2 |
| `docs/plans/cross-page-context-2026-08-23.md` §1 | "`/trace`, `/logs` … hiç üretici ve hiç kapı yok" | `lib/traceHref.ts:123` `traceHref` (68 çağrı), `lib/logsUrl.ts:328` `logsHref` (28 çağrı) |
| `docs/plans/cross-page-context-2026-08-23.md:64` | "`App.tsx:130` /service, `:142` /endpoint, `:148` /database, `:154` /pod" | `App.tsx:179, 191, 199, 207`; ayrıca `/entity` `:208`, `/messaging/topic` `:210` |
| `docs/plans/cross-page-context-2026-08-23.md:69` | DrillButton "`pages/Service.tsx` … ve `pages/Trace.tsx:417`" | tüketiciler `pages/Service.tsx` ve `pages/slowqueries/stmtDetailSections.tsx`; Trace kullanmıyor |
| `docs/plans/cross-page-context-2026-08-23.md:70` | topic kimlik kodeği `destinationParam.ts` | detay sayfası `/messaging/topic` üç ayrı param (`pages/messaging/topicHref.ts:60,77`); `destinationParam` yalnız çekmece + `nodeDetailHref` |
| `lib/incidentsRetired.ts:5-7` (kod yorumu) | "/incidents → /anomalies?tab=groups"; "Açıkken her şey bugünkü gibi" | `INCIDENTS_RETIRED_HOME = '/inbox?tab=signals'` (`:12`); `/incidents` anahtardan bağımsız yönlenir (`App.tsx:215`) |
| `pages/EntityDetail.tsx:56` (UI metni) | "Entity katmanı kapalı … Settings'ten açılır" | katman v0.10.2214'ten beri hep açık; yalnız bağlanmamış sunucu (testler) 404 (`internal/api/entities.go:50-51`) |
| `docs/DECISIONS.md` "Tablo standardı" | — | bayat değil; T8 brief ile çakışıyor (§4.3) |

---

## Operatöre sorular — CEVAPLANDI (2026-11-27, aşağıda "Kararlar")

1. Kullanıcıya dönük tek kavram "Problem" olsun mu; Incident ayrı kalsın mı?
2. Sekme sırası ve adları onayı.
3. Adres şeması değişikliği (eski linkler yönlenir) kabul mü?

## Açık kararlar

- **Soru 1'in yapılmış kısmı:** v0.10.2479 liste ve rozet yalnız Problem; v0.10.2481 tek sayfa
  (Problemler / Sinyaller), Anomalies menüden çıktı, `/anomalies` ve `/incidents` yönleniyor;
  v0.10.2482 Etki sütunu ve "Neden Problem değil". Incident varsayılan emekli
  (`chstore.IncidentsEnabled`). **Kalan:** rota–ad tersliği (`/inbox` vs `/problems`), menü/
  Topbar/sekme adlarının tek anahtarı, "Olaylar" çakışması, Incident anahtarı açıkken nerede
  görüneceği (sayfa silindi — anahtar yalnız bildirim/gruplama mı kalıyor?).
- **A1 — Servis kimliği.** Brief `cluster+namespace+service.name` diyor; entity modeli ve
  kırmızı çizgi `svc:<ad>` cluster'sız (entity-layer-design §1.2 "E16"; `identity.go:83`;
  `deploy_env` boyutu cross-page §Faz 5'te düşürüldü). Öneri: URL'de `/e/service/<ad>` +
  isteğe bağlı `?cluster=&ns=` kapsamı (kimlik değil filtre); kimliğe cluster girmesi CH/MV
  kararıdır, ayrı `/spec`. Topic için aynı soru (`queue:<sys>:<topic>` vs kafka cluster).
- **A2 — Sağlık tanımı.** Nokta yalnız açık Problem'den mi (öneri), yoksa `scoreHealth`'in hata
  oranı dalları kalsın mı? Kalkarsa hata >%5 ama Problem'i olmayan servis nötr görünür —
  bu, o durumda Problem açılmıyorsa bir sinyal kaybıdır.
- **A3 — `/problems` adresinin anlam değiştirmesi.** Eski paylaşılmış `/problems?service=…`
  linkleri bugün Exceptions'a gidiyor; Problem listesi `/problems`'e taşınırsa bu linkler
  Problemler'e düşer. Seçenekler: (a) Problem listesi `/inbox`'ta kalır, yalnız etiketler
  düzelir; (b) taşınır, eski linkin anlam kayması kabul edilir; (c) yeni adres `/problemler`
  değil `/triage`. Öneri (a) — ad tutarlılığı URL'siz de sağlanır, kırık anlam yok.
- **A4 — Exceptions ayrı sayfa mı?** Öneri: ayrı liste kalır ("Hatalar"), çünkü v0.9.425'te
  operatör "tüm exception'ları görmek istiyorum" dedi (`Sidebar.tsx:83-87`); Problem satırı
  Exception kaynağını gösterir. Alternatif: Problemler sayfasının üçüncü sekmesi.
- **Sekme sırası ↔ service-tabs-plan ve eski operatör kararları çakışması:**
  (i) v0.5.293 "Operations tablosu sayfanın İLK gövde öğesi" (`Service.tsx:620`) — önerilen
  sırada Operasyonlar türe özgü olarak sonda; (ii) v0.10.1320 operatör onaylı başlık düğme
  grupları [Backtrace|Logs|Endpoints][Problems|Signals][Traces] (`Service.tsx:514-533`) —
  öneri bunları kaldırıyor; (iii) service-tabs-plan "Nerede çalışıyor" Infrastructure
  sekmesinde (S3/S6) — öneri İlişkiler'e taşıyor; (iv) v0.10.2436 `details` sekmesinin adı
  "Metrics" — öneri HTTP/GRPC ve Messaging'i de Metrik'e katıyor (sekme 9 → 8 + türe özgü 2).
  Öneri: (i) için Genel sekmesinin ilk bloğu "en yavaş/en hatalı operasyonlar" özeti olur.
- **A5 — Hover kartı için özet ucu.** Endpoint/DB/topic için hafif `GET /api/entity/summary?
  kind=&id=` gerekir mi (yeni uç, `/api-route`, `serveCached`, MV-first)? v1 istek atmadan
  satır verisiyle çalışır.
- **A6 — Çevrilmeyen yönetim kuralı ↔ dil sızıntısı hedefi.** Yönetim kümesi (2.483 metin)
  hedef dışı; sınır `/ai` (admin+editör menüsü ama /admin değil) ve `/users` (Settings mi ayrı
  rota mı) için netleşmeli. Bu denetim `/ai`'yi ürün, `/users`'ı yönetim saydı.
- **A7 — Biçim ayrıntıları:** bayt IEC mi SI mi; "açık süre" (`fmtStdDuration`, iki birim)
  istisnası kalsın mı; TR ondalık virgül her yerde mi (bugün `fmtFixed` düz `toFixed`, yani nokta basıyor — `lib/utils.ts:105-106` [K]).
- **A8 — Satır eylemleri:** brief "hover/menü", DECISIONS T8 "⋯ hep görünür". Öneri T8.
- **A9 — Görsel regresyon temaları:** brief 3 tema; nightly ve axe CI 4 tema (Yüksek Kontrast
  dahil). Öneri 3 (dark, light, Kurumsal) + kontrast yalnız axe'te.
- **A10 — Rozetin Exception'ları sayması:** v0.10.2479 rozeti yalnız Problem'e indirdi; Hatalar
  menü girişi kendi sayacını taşısın mı (bugün yok)?

## Kararlar (operatör, 2026-11-27)

Operatörün cevabı: "Exceptions ayrı kalsın. Önerini yapalım diğerleri için." Buna göre bu
denetimin önerileri karar oldu; dilimler §6 sırasıyla başlar. Önceki bir kararı tersine çeviren
satırlar öyle işaretlidir.

| Konu | Karar |
|---|---|
| Soru 1 — tek kavram | Kullanıcıya dönük tek üst kavram "Problem". Incident ayrı bir kullanıcı kavramı olarak kalmaz (anahtar kapalıyken emekli hâli sürer). Rota/etiket uyumsuzluğu ve "Olaylar" çakışması dilim 2'de düzelir. |
| Soru 2 — sekme sırası | Genel · Problemler · Trace · Log · Metrik · İlişkiler · Değişiklikler · türe özgü. Dilim 5'te servis sayfası için önce mockup gösterilir, çünkü bu sıra v0.5.293 (Operations tablosu ilk öğe), v0.10.1320 (başlık düğme grupları) ve service-tabs-plan ("nerede çalışıyor" Infrastructure'da) kararlarını değiştirir. Operations tablosu "Genel" sekmesinin ilk gövde öğesi olarak kalır. |
| Soru 2 — sekme sırası, GÜNCELLEME (operatör, 2026-11-28) | **Önceki satırı değiştirir.** Yerel üçlü prototipte (servis / endpoint / pod) operatörün verdiği sıra: Genel · türe özgü (Operasyonlar / Zaman dağılımı / Olaylar) · Trace · Metrik · Log · İlişkiler · Değişiklikler · Problemler; servis sayfasında ardından klasik sekmeler (Topoloji İlişkiler'den sonra; Altyapı, GitOps, HTTP/GRPC, Mesajlaşma sonda). Ek kural: yeni görünüm klasik sayfanın hiçbir işlevini düşürmez ("fonksiyonlar kaybolmasın"); işlev eşitleme tablosu `docs/frontend/entity-trio-prototype-notes.md` içinde, prototip dalında. Dilim 5 bu sırayı ve bu kuralı esas alır. |
| Soru 3 — adres şeması | `/e/<tür>/<kimlik>` kabul; eski rotalar yönlendirme olarak kalır, paylaşılmış linkler kırılmaz. |
| A1 — servis kimliği | Kimlik entity modelindeki gibi addır; cluster ve namespace URL'de isteğe bağlı süzgeçtir, kimliğin parçası değildir. |
| A2 — sağlık noktası | Tek tanım: yalnız açık Problem'lerden. Hata oranı eşikleri noktada kullanılmaz; hücre renklendirmesi `errorTone` ile ayrı kalır. |
| A3 — `/problems` adresi | Problems `/inbox`'ta kalır; yalnız etiketler değişir. |
| A4 — Exceptions | **Operatör kararı:** ayrı sayfa kalır. Tek ad "Hatalar" (EN "Errors"). |
| A5 — hover kartı | v1 istek atmaz, satır verisiyle çalışır; özet ucu ayrı karar. |
| A6 — yönetim sınırı | `/ai` ürün, `/users` yönetim sayılır; yönetim ekranları çevrilmez ve dil sızıntısı hedefinin dışındadır. |
| A7 — biçim | Bayt IEC (KiB/MiB/GiB). "Açık süre" iki birim istisnası kalır ("1 sa 10 dk"). Türkçe arayüzde ondalık ayırıcı her yerde virgül. |
| A8 — satır eylemleri | Tablo standardı T8 geçerli: "⋯" her zaman görünür. |
| A9 — görsel regresyon | 3 tema (dark, light, Kurumsal); yüksek kontrast yalnız axe koşusunda. |
| A10 — Hatalar sayacı | Menüdeki mevcut soluk sayaç kalır; yeni sayaç eklenmez. |

