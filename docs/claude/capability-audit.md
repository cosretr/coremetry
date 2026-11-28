# Claude Code yetenek audit'i (ADIM 1)

2026-11-28, yalnız belge. Kaynaklar `https://code.claude.com/docs/en/<ad>.md` (aynı gün okundu): **[M]** model-config,
**[A]** sub-agents, **[S]** skills, **[H]** hooks, **[P]** permissions, **[SR]** settings-reference, **[ME]** memory,
**[MC]** mcp, **[W]** worktrees. Repo kanıtı `dosya:satır`. Belgede bulunamayanlar sonda "Doğrulanamadı" listesinde.

## Özet

Yazılı kademelendirme bugün fiilen çalışmıyor: oturumlar repo dizininden değil ev dizininden başlıyor, bu yüzden
repodaki beş ajan, 21 skill, izin listesi ve düzenleme sonrası hook bu oturuma yüklenmedi; aynı gün dört audit
"architect kayıtlı değil" diye genel ajana verildi. İkinci büyük açık doğrulama döngüsü: Claude kapıları koşabiliyor
ama değişikliği çalışan uygulamada görmek için standart bir yol yok; bugünkü oturumda yerel deploy, oturum açma,
ekran görüntüsü ve salt okunur sorgu her seferinde elle kuruldu, bir ajan da koşmadığı kapının sonucunu yazdı.
Öneri: oturumu repoda başlat, ajan modellerini takma ada çevir ve çaba düzeyi ver, `scripts/dev-*.sh` + `/dogrula`
ile tek komutlu doğrulama kur, Stop hook'u ile "doğrulanmadan bitti" uyarısı ekle. Yerel yığın bu makineye sığıyor
(6 container ≈ 4,4 GiB / 11,67 GiB); ölçek, örnekleme, gerçek model ve Elasticsearch yolu yerelde doğrulanamaz.

## A. Model ve düşünme

**Bugün.** `CLAUDE.md:70-72`: Sonnet 5.5 basit, Opus 5.5 varsayılan ana oturum, Fable 5.1 ileri. Ajanlar:

| Ajan | `model` | Durum |
|---|---|---|
| architect (`.claude/agents/architect.md`) | `claude-fable-5-1` | **Eski kimlik.** `fable` takma adı Fable 5.2'ye çözülür [M]. |
| implementer, repo-explorer, verifier | `claude-sonnet-5-5` | Bu tam kimlik resmî sayfada geçmiyor (**doğrulanamadı**); oturumun ortam bilgisinde geçiyor. |
| coremetry-feature-shipper | `opus` | Takma ad, güncel. |
| Ana oturum (bugün) | Fable 5.2 | Operatör seçimi; CLAUDE.md "Opus varsayılan" diyor: çelişki. |

- Geçerli takma adlar: `fable`, `opus`, `sonnet`, `haiku`, `best` (= `opus`), `opusplan`, `[1m]` ekleri [M]. Bu hesapta
  `fable`, `opus`, `sonnet`, `haiku` ajan çağrısında kabul edildi (bu oturumda dördü de kullanıldı).
- Ajan `model` alanı takma ad, tam kimlik ya da `inherit` alır; yoksa sıra: çağrı parametresi → frontmatter →
  `CLAUDE_CODE_SUBAGENT_MODEL` → üst oturum [A]. Geçersiz kimlikte ajanın davranışı **doğrulanamadı**.
- **Yükleme:** `.claude/agents/` çalışma dizininden repo köküne doğru taranır; skill'ler başlangıç dizini ve üstlerinden
  yüklenir; `--add-dir` ajan ve skill'i yükler ama CLAUDE.md'yi yalnız `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`
  ile [A][S][ME]. Gözlem: bu oturum `/Users/cenk`'te başladı; ajan listesinde repo ajanı, skill listesinde repo skill'i yok.

**Öneri: iş türü → model.** Kimlik yerine takma ad yazılır; sürüm atlayınca dosya bayatlamaz.

| İş türü | Model | Çaba | Gerekçe |
|---|---|---|---|
| Audit, tasarım | `fable` | `xhigh` | Yanlış karar en pahalı hata; hacim küçük. |
| Zor hata ayıklama, kök neden | `fable` | `xhigh` | Çok adımlı çıkarım; yanlış kök neden boşa dilim demek. |
| İnceleme (diff, spec karşılaştırma) | `opus` | `high` | Yazandan farklı bağlam; yargı gerekir ama keşif değil. |
| Uygulama (onaylı dilim) | `opus` | `high` | Çok dosyalı değişiklikte bugünkü sonuç iyi; Fable'a gerek yok. |
| Keşif, sayım | `sonnet` | `low` | Salt okuma; hız ve maliyet önemli. |
| Mekanik düzenleme, kapı koşma | `sonnet` (ya da `haiku`) | `low` | Karar yok; çıktı özetlenir. |

**Düşünme / çaba.** Düzeyler `low`, `medium`, `high`, `xhigh`, `max`. Ayar yolları: ajan ve skill frontmatter'ında
`effort`; `settings.json` içinde `effortLevel` (en çok `xhigh`) ve model başına `modelSettings`; oturumda `/effort`;
ortam değişkeni `CLAUDE_CODE_EFFORT_LEVEL`; istemde `ultrathink` o turu `xhigh` yapar [M][A][S][SR].
`MAX_THINKING_TOKENS` güncel modellerde etkisiz [M]. Yani ajan bazında mümkün; bugün hiçbir ajan ve skill kullanmıyor.

## B. Kendi işini doğrulama döngüsü

| İş türü | Bugünkü doğrulama | Eksik |
|---|---|---|
| Go API | Hook: düzenlemede `go build` (`.claude/hooks/post-edit-check.sh:21-43`). `scripts/gate.sh go`: build, vet, sözleşme, test (`gate.sh:50-71`). | golangci-lint ve `make audit` kapıda yok (CI'da var). Yeni ucu çalışan uygulamada çağırmanın standart yolu ve oturum açma yok. |
| ClickHouse sorgu / MV | SQL metni testleri, audit CHECK 5, CI duman testi. Yerel CH prod sürümüyle aynı imaj (`docker-compose.yml`, 26.2.4.23). | Salt okunur sorgu betiği yok. Maliyet ve süre yerelde ölçülemez (veri ~7000× küçük). |
| Önyüz sayfası | tsc, eslint, vitest (jsdom), paket bütçesi. | Gerçek tarayıcı yalnız gece işi (`frontend/e2e/nightly.pw.ts`, 1 dosya); `toHaveScreenshot` 0 kullanım; `docs/screenshots` üreten betik yok. |
| CoSRE akışı | Modelsiz sözleşme testleri (`scripts/cosre-contracts.sh`), `make evalset` (CI dışı). | Yerel LLM anahtarı geçersiz; gerçek modelle hiçbir akış yerelde doğrulanamıyor. |
| Helm | `helm template` / `lint` izinli (`.claude/settings.json`). | Kapıda yok; OpenShift'e özgü davranış (rastgele UID, route) doğrulanmıyor. |

**Yerel yığın (ölçüm).** Çalışan 6 container: clickhouse 3,32 GiB, victoriametrics 654 MiB, otel-collector 226 MiB,
coremetry 177 MiB, go-demo 25 MiB, redis 13 MiB; toplam ≈ 4,4 GiB, Docker sınırı 11,67 GiB, 10 CPU. Compose'ta bellek
sınırı tanımlı değil. Sıcak yeniden başlatma bugün < 1 dk; boş volume ile soğuk kalkış **ölçülmedi**. Disk: 53 GiB boş;
Docker imajları 64 GiB (%95 geri kazanılabilir), `.claude/worktrees` 82 GiB (154 giriş).
Sorunlar: VictoriaMetrics yalnız repo dışındaki `~/.coremetry-local/docker-compose.minimal.yml` içinde; `docker build`
her seferinde kayıt defterine soruyor ve bugün 5 kez zaman aşımına düştü; üç ajan aynı anda kapı koşunca yük 38'e çıktı.

**Tasarım: betikler** (hepsi `scripts/`, salt yerel, `localhost` dışına çıkmaz):

| Betik | İş | Güvenlik sınırı |
|---|---|---|
| `dev-up.sh [--es]` | Repo içi `docker-compose.dev.yml` ile clickhouse, redis, victoriametrics, otel-collector, coremetry, go-demo. Uygulama imajı `docker build` ile değil, host'ta derlenen binary ile güncellenir. Hazır olunca süre ve bellek yazar. | Volume silmez; yalnız bu projenin container'ları. |
| `dev-seed.sh` | Demo trafiğini başlatır, demo cluster'larını kaydeder (`scripts/demo-k8s-clusters.sh`), "N servis görünüyor" olana dek bekler. | Yalnız yerel ayar yazar. |
| `dev-api.sh GET <yol>` | Yerel varsayılan hesapla oturum açar, çerezi 0600 geçici dosyada tutar, durum kodu + kırpılmış gövde basar. | Varsayılan yalnız GET; kimlik bilgisi çıktıya yazılmaz. |
| `dev-ch.sh "<SELECT>"` | `clickhouse-client --readonly=1`, `max_execution_time` ve satır sınırıyla. | SELECT dışını reddeder. |
| `dev-prom.sh "<PromQL>"` | Yerel VictoriaMetrics `api/v1/query[_range]`. | Yalnız sorgu ucu. |
| `dev-shot.sh <yol> [ad]` | Playwright ile sayfayı `redhat` ve `dark` temada açar; `e2e/shots/<ad>-<tema>.png`, konsol hataları ve başarısız istekler JSON; önceki görüntüyle fark oranı. | Yalnız `localhost:8088`. |
| `dev-down.sh` | `compose stop`. | `down -v` ve prune yok. |

**`/dogrula` skill'i.** `git diff --name-only` ile iş türünü seçer: Go → `gate.sh go` + `dev-api.sh`; CH → `dev-ch.sh` ile
sorgunun kendisi + `EXPLAIN`; önyüz → `gate.sh fe` + `dev-shot.sh` (iki tema, konsol hatası 0); Helm → `helm lint` +
`template`. Çıktı tek "kanıt bloğu": komut, çıkış kodu, son satır, ekran görüntüsü yolu, süre. Kanıt üretilemeyen adım
"doğrulanmadı" yazılır. Kapılar bağımsız doğrulayıcıda (`verifier`) koşar; ana oturum yalnız bloğu okur.

**Dış sistemlerin yerel temsili.**
- **VictoriaMetrics:** gerçek servis (bugün de öyle; 256 MiB tavanlı). Sahte uç PromQL davranışını saklar.
- **Thanos:** ayrı servis yok; aynı VictoriaMetrics "querier" olarak kaydedilir (`docs/DEMO-REALISM.md:120-128`).
  Sorgu yolu gerçek, kimlik doğrulama (bearer/TLS) ve dış etiketler temsil edilmez.
- **Elasticsearch:** varsayılan kapalı (bellek); `--es` ile gerçek servis. Sahte uç önerilmez: sorgu DSL'i ve eşleme
  farkları asıl hata kaynağı. Prod log arka ucu ES olduğu için bu yol varsayılan döngüde **doğrulanmaz**.
- **LLM:** yerelde geçerli anahtar yok. Kayıtlı yanıt oynatan sahte uç yalnız akışı doğrular, kaliteyi değil.

**Yerel demo neyi doğrular, neyi doğrulamaz.** Demo `-rps 3`, ~45 servis, 233 pod, 30 node, 5 cluster
(`docker-compose.yml:397`, `DEMO-REALISM.md:95-97`); prod ~27 bin span/sn. Demo örneklemesiz; prod'da collector tüm
hatalı trace'leri ve başarılıların %5'ini yolluyor, `ingest.sampling.mode` bilinmiyor.
Doğrular: sorgu sözdizimi, sayfa çizimi, boş/hata durumları, bağlantılar, sözleşmeler, senaryolu OOMKill akışı.
Doğrulamaz: sorgu maliyeti ve süresi, örnekleme yanlılığı, kardinalite, dedektör gürültüsü, gerçek model cevabı, ES yolu,
OpenShift kimlik doğrulaması, çok kopyalı kip.

## C. Araçlar ve bağlam

**Eklentiler** (`.claude/settings.json` `enabledPlugins`): gopls-lsp, typescript-lsp, context7, frontend-design, playwright
açık; superpowers kapalı. `docs/claude/plugin-trial.md` denemeyi başlatmış ama "Sonuç: (bekliyor)"; gopls ve
typescript-language-server'ın kurulu olmadığı not edilmiş (bugün yeniden ölçülmedi). Bu oturumda yalnız playwright
kullanıldı (ekran görüntüsü, konsol); context7 araç listesinde yoktu.
**MCP:** `.mcp.json` tek sunucu `astra` (ikincil inceleyici, salt okunur). Uygulamanın kendi MCP sunucusu var
(`POST /api/mcp`, JWT, salt okunur araçlar; `.claude/skills/mcp-tools/SKILL.md:8-14`) ama Claude'a bağlı değil.
Salt okunur erişim önerisi: ClickHouse ve VictoriaMetrics için yeni MCP sunucusu yerine `dev-ch.sh` / `dev-prom.sh`
(tek izin kuralı, bağlam yükü yok); uygulama verisi için yerel Coremetry MCP'si, `claude mcp add --transport http --scope local …` [MC].

**İzinler.** `.claude/settings.json`: 35 `allow`, `ask` ve `deny` yok. `.claude/settings.local.json` (git dışı, 72 KB):
**692 `allow` girişi**; bir kısmı satır içinde Bearer belirteci ve yönetici parolası taşıyor. Kural: önce `deny`, sonra
`ask`, sonra `allow`; listeler kapsamlar arasında birleşir; bileşik komutta her alt komut ayrı eşleşir [P].
- Eklenecek `allow` (salt okuma): `Bash(scripts/gate.sh *)`, `Bash(scripts/dev-*.sh *)`, `Bash(git status *)`,
  `Bash(git diff *)`, `Bash(git log *)`, `Bash(git show *)`, `Bash(rg *)`, `Bash(gh run list *)`, `Bash(gh run view *)`,
  `Bash(docker stats --no-stream *)`, `Bash(golangci-lint run *)`.
- Açıkça `ask`: `Bash(git push *)`, `Bash(git tag *)`, `Bash(git commit *)`, `Bash(docker compose up *)`, `Bash(helm upgrade *)`,
  `Bash(kubectl apply *)`. `deny`: `Bash(docker volume *)`, `Bash(docker system prune *)`, `Bash(docker compose down -v*)`,
  `Bash(kubectl delete *)`, `Read(./.env)`.
- `curl` izinli yapılmaz: önekle GET/POST ayrılamaz; yerine `dev-api.sh`. Yerel dosyadaki 692 giriş bu kalıplara
  indirilir, içindeki gizli değerler silinir.

**Hook'lar.** Bugün tek hook: `PostToolUse`, `Edit|Write|MultiEdit` → build / tsc / api.go kapısı. Belgede 30'dan fazla
olay var; işe yarayanlar: `Stop`, `SubagentStop`, `SessionStart`, `PreToolUse`, `PostToolUseFailure`, `PreCompact`,
`WorktreeCreate` [H]. `Stop` çıkış kodu 2 ya da `decision: "block"` ile durmayı engeller ve gerekçeyi Claude'a verir;
komut hook'unun varsayılan süresi 600 sn; `SessionEnd` bütçesi 1,5 sn (en çok 60) [H].
Sonuç: **"doğrulanmadan bitti" uyarısı mümkün**, oturum sonunda tam kapı koşmak uygun değil. Öneri: `/dogrula` ve
`gate.sh` çalışma ağacının özetini bir damga dosyasına yazar; `Stop` hook'u ürün kodu değişmiş ve damga eskiyse bir
kez engeller. Döngü koruması damgayla yapılır, çünkü belgedeki `stop_hook_active` alanı **doğrulanamadı**.
`SubagentStop` aynı denetimi ajan raporuna uygular.

**Bağlam (ölçüm).** Kök `CLAUDE.md` 84 satır / 9,6 KB (öneri < 200 satır [ME]). 21 skill, açıklamalar toplam ≈ 4,8 bin
karakter, tekil 187–345 (liste sınırı skill başına 1.536 [S]): sorun yok. Üç skill 400 satırı aşıyor (otel-conventions
643, frontend-design-system 563, clickhouse-schema 472; öneri < 500 [S]); yalnız `aiops` yan dosya kullanıyor.
Alt dizin `CLAUDE.md` ve `.claude/rules/` yok; `paths:` ile dosya yoluna bağlı kural mümkün [ME].
Oturumlar arası hafıza ev dizini projesinde (`~/.claude/projects/-Users-cenk/memory`, 35 dosya); belgeye göre hafıza
repo başınadır [ME], yani oturum repoda başlayınca bu notlar görünmez: taşıma gerekir. Sürüm betikleri (`rel.sh`,
`local-deploy.sh`) de repo dışında ve sürümsüz.

**Paralel çalışma (yalnız değerlendirme).** Bugün dört ajan ayrı worktree'lerde eşzamanlı çalıştı: kod çakışması
olmadı, ama üç kez kullanım limitine takıldı, yerel ClickHouse aşırı yüklendi, iki ajan aynı container'ı birbirinin
üstüne deploy etti. "Aynı anda tek iş" kuralı şöyle okunabilir: aynı anda tek **sürüm** ve yerel yığının tek **sahibi**;
bağımsız dilimler worktree'de paralel yazılabilir, kapılar sırayla koşar. `isolation: worktree` ve `--worktree` resmî [A][W].

## D. Çalışma biçimi

- **İnceleyici.** `reviewer` adlı ajan yok. Var olanlar: `verifier` (Sonnet; kapı + madde madde karşılaştırma),
  `/review-changes` skill'i, `astra` MCP'si. `.claude/skills/release/SKILL.md` içinde inceleme adımı geçmiyor.
  Öneri: `/release` adım 3 (Gate) ile 4 (Stage) arasına, 3+ dosyalık ya da Go + önyüz karışık değişiklikte `verifier`;
  ajan raporundaki kapı satırları yerine yalnız doğrulayıcının ya da sürüm betiğinin çıktısı "geçti" sayılır.
- **"Bitti" tanımı.** `docs/frontend/migration-log.md:10` on sütunlu, kanıt sütunu yok; yalnız shadcn geçişini izliyor.
  Öneri: önyüz dilimlerinde Notes sütununa `/dogrula` kanıt yolu; diğer işlerde kanıt bloğu commit gövdesinde.
- **Skill'e ya da kurala taşınacak tekrarlar** (bugünkü ajan briflerinde her seferinde elle yazıldı):
  taban etikete `reset --hard`; dosyaları adıyla stage et, `git add -A` ve çıplak `stash` yok; tek commit + imza satırı;
  yalnız gerçek komut çıktısının son satırları; zaman aşımına düşen testi tek başına koş; alt ajan açma;
  `api.go` büyümez, `components/ui` donuk, `globals.css` sonuna CSS yok; biçim, dil ve yardım paragrafı ratchet'leri;
  audit CHECK 5; gerçek şirket/cluster adı yok; tablo gri zeminde çıplak kalmaz; yeni görünüm işlev düşürmez.
  Öneri: `.claude/rules/` altında yol kapsamlı kurallar + bir `/dilim` brif skill'i; `CLAUDE.md` yalnız işaret eder.
- **Çelişkiler.** `CLAUDE.md:60` imzası `Claude <…>`, kullanılan `Claude Fable 5.2 <…>`; `CLAUDE.md:72` "Fable 5.1"; `/audit`, `/mockup` listede yok.

## Kapsam dışı

Ürün kodu; CI iş akışları; prod erişimi kurulumu; eklenti deneme sonucu; gece görsel regresyon işi; sandbox [SR].

## Etki × efor: ilk 10

| # | Madde | Etki | Efor |
|---|---|---|---|
| 1 | Oturumu repoda başlat; hafıza notlarını ve sürüm betiklerini repoya taşı | Çok yüksek: ajan, skill, hook, izin hepsi devreye girer | Düşük |
| 2 | `dev-up` / `dev-seed` / `dev-down` + repo içi dev compose (kayıt defterine bağımsız) | Yüksek | Orta |
| 3 | `dev-api`, `dev-ch`, `dev-prom` salt okunur betikleri | Yüksek | Düşük |
| 4 | `dev-shot` (iki tema, konsol, fark) + `e2e/` iskeleti | Yüksek | Orta |
| 5 | `/dogrula` skill'i ve kanıt bloğu | Yüksek | Düşük |
| 6 | Ajan modelleri takma ada, `effort` alanı; `CLAUDE.md` kademe ve imza düzeltmesi | Orta | Düşük |
| 7 | `Stop` / `SubagentStop` "doğrulanmadan bitti" hook'u | Orta | Orta |
| 8 | İzin listesi: `allow` / `ask` / `deny`; yerel dosyadaki 692 girişin ve gizli değerlerin temizliği | Orta (güvenlik + daha az onay) | Orta |
| 9 | Tekrarlanan brif kuralları `.claude/rules/` + `/dilim`; `/release`'e doğrulayıcı adımı | Orta | Orta |
| 10 | Worktree ve Docker disk temizliği betiği (82 GiB + 61 GiB) | Düşük-orta | Düşük |

## Operatör kararları

1. **Ana oturum modeli.** (a) Her işte en güçlü model; (b) ana oturum `opus`, yalnız audit / tasarım / zor hata
   ayıklama `fable` ajanında. Öneri (b): bugün üç kez 5 saatlik limite takıldık. Cevapsızsa madde 6 bekler.
2. **Yerel yığın bu makinede kalıcı mı?** Ölçüm sığdığını gösteriyor (≈ 4,4 / 11,67 GiB; ES açılırsa +1–2 GiB).
   Bellek ya da disk sınırı koymak ister misin? Cevapsızsa 2–5 varsayılanla (ES kapalı) ilerler.
3. **Prep ortamına salt okunur erişim.** Verilirse ölçek, örnekleme ve ES yolu orada doğrulanır; verilmezse yerel
   yığın tek yol ve yukarıdaki "doğrulamaz" listesi kalıcı açık olur. Öneri: yalnız GET yapan, süresi kısa bir belirteç.
4. **İzin listesi.** C bölümündeki `allow`, `ask`, `deny` kalıplarını onaylıyor musun? `git commit` `ask`'ta kalsın mı?
5. **Oturum başlangıç dizini ve hafıza.** Repoda başlamayı ve 35 hafıza notunun repo hafızasına taşınmasını onaylıyor
   musun? Cevapsızsa madde 1 bekler; diğer her şeyin etkisi buna bağlı.
6. **`settings.local.json` içindeki belirteç ve parola.** Yerel varsayılan mı, gerçek mi? Gerçekse yenilenmeli.
7. **Kanıt nereye yazılır?** migration-log Notes sütunu mu, commit gövdesi mi, ikisi mi?

## Doğrulanamadı

`claude-sonnet-5-5` tam kimliği; geçersiz model kimliğinde ajan davranışı; `stop_hook_active`; izin kuralında iki
noktalı biçim (`Bash(x:*)`) ve yol önekleri; skill listesi bütçe yüzdesi; otomatik hafızanın varsayılan yolu; resmî
belgede Playwright MCP. Ölçülmeyenler: soğuk kalkış süresi, LSP sunucularının kurulum durumu, ev dizini oturumunda proje hook'u.
