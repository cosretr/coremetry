# CoSRE konuşma kalitesi — Faz 0 denetimi (2026-11-17)

Audit-first belge; kod değişmedi. Okunan ağaç `v0.10.2317`. Kısaltma: `api/` = `internal/api/`, `cop/` = `internal/copilot/`, `uistream/` = `internal/cosre/uistream/`, `mcp/` = `internal/mcptools/`, `fe/` = `frontend/src/`; `chat.go` = `api/copilot_chat.go`. Girdi: statik okumadan çıkmış bir bilgi föyü; tasarımın dayandığı iddialar koda karşı yeniden okundu, föyden sapan her madde **[DÜZELTME]** ile işaretli (toplu liste §8). Token tahmini varsayımı: **3 bayt ≈ 1 token** (Türkçe, çok baytlı; tokenizer koşulmadı). Örnek adlar nötr: checkout, orders, payments.

## 1. Hüküm

Hedef üslup bugünkü kodla ulaşılabilir, ama iş "serbest döngünün istemini kısaltmak" değil. Üç yapısal engel var. (1) İstem tersini emrediyor: ilk satır `**Özet:**`, tablo, numaralı liste, "kısaltmak için bilgi atma" (`cop/prompts.go:2205-2213`, `:2935`). (2) Cevapların çoğu serbest döngüden hiç geçmiyor: varsayılan `on_no_loop` kipinde soru ya deterministik yönlendiriciyle tek araçsız anlatım çağrısına (`chat-guided`), ya da araçsız genel cevaba düşer (`api/copilot_intent.go:458-474`); B–E dilimleri yalnız döngüye uygulanırsa kabul ölçütü 2 ve 4 tutmaz. (3) Turlar arası durum yarım: serbest döngü bağlamı ancak model `set_context` çağırırsa yazar, mutlak pencere ("dün") ve sunucunun önerdiği sonraki adım ("bak") hiçbir yerde tutulmaz; Redis yoksa hiçbir şey kalmaz. Altyapının iyi yanı: cevap zaten tek otoriter `answer` olayıyla taslağın yerine geçiyor (`uistream/translator.go:233-243`), yani doğrulayıcı ve kalıp süzgeci için akışı bozmayan tek bir dikiş mevcut; sayı denetiminin çekirdeği de yazılmış (`api/numeric_claims.go:66`), yalnız tek yola bağlı. Öneri: tek birleşik durum nesnesi (kapsam + kanıt defteri), tek cevap dikişi, v2'ye özel iki kısa anlatım istemi; sıra ölçümle başlar (H1), istem en son değil ortada değişir.

## 2. Mevcut denetimlerle ilişki

Tekrarlanmayan konular ve atıf yeri: akış parçaları ve `data-reset` sözleşmesi → `chat-ui-audit.md` §0, §3.2; soru kartı, kaynak çipi, özet + link kartı → `experience-audit.md` §3; sinyal, gece hakemi, koşucu, kapı → `quality-loop-audit.md` §3–4; yönlendirici sinyalleri ve bağlam D5 → `router-gaps-audit.md` Durum tablosu; v1 ↔ v2 farkı ve kalıcı kurallar → `roadmap-status.md` §3 ve "Kalıcı kurallar"; durma koşulu / `prepareStep` → `agent-core-ideas.md` §2–3.

| Belge, bölüm | Bu işle ilişki | Tür |
|---|---|---|
| `chat-ui-audit.md` başlık ("Değişmeyecek: … prompt'lar, model seçimi") | UI yeniden kurulumunun kapsam dondurmasıydı; C dilimi istemi değiştirir | çatışma değil, dondurma bu işle kalkar — operatör onayı gerekir (§7-1) |
| `chat-ui-audit.md` §0 "tek geri alma mekanizması `data-reset`; final = reset + otoriter metin" | D/E ikinci bir geri alma mekanizması kuramaz; düzeltme `answer` içinde, taslakta ise yayından ÖNCE tutma ile yapılır | kısıt, uyumlu |
| `chat-ui-audit.md` §0-2 (oy düğmesi yok, sinyal `cosre_signals`) | E'nin sayacı sistem sinyali olur (emsal `prefix_applied` / `prefix_corrected`, `internal/cosrelearn/signals.go:65-77`) | uyumlu |
| `experience-audit.md` §3.3 (özet cümlesi sunucuda deterministik; modelin `**Özet:**` satırı okunmaz) | C `**Özet:**` kuralını kaldırır; kart bundan etkilenmez. B'nin "link kartı" isteği yalnız üç guided rotada var (`api/chat_summary_card.go`), döngüde link çipi var | örtüşme |
| `experience-audit.md` §8-2 ve 2026-10-29 "çipler çok kullanılmıyor" | A'nın düzenlenebilir etiketleri ve B'nin seçenekli sorusu aynı karara dokunur. `data-question` kapalı küme, ≤3 seçenek (`uistream/part.go:27-31`); B yeni bir `kind` ister | **çatışma** (§7-4) |
| `experience-audit.md` §5 ("policy katmanı — erken soyutlama") | B bir karar motoru olmamalı: sınıf → uzunluk tavanı veren saf bir eşleme | kısıt |
| `quality-loop-audit.md` karar 5 ve §5 ("hakem kapı değil; yeni hakem işi önerilmez") | H'nin hakeme iki ölçüt eklemesi bu kararın dışına çıkar | **çatışma** (§7-10) |
| `quality-loop-audit.md` §4 D1 (koşucu v2 yolunda, v0.10.2106) | Görev tanımındaki "koşucu v1'i ölçüyor" önermesi bayat **[DÜZELTME]**: `api/ai_evalset_agent.go:459,471`. Eksik olan çok turlu vaka ve kademe zinciri (§3.9) | düzeltme |
| `quality-loop-audit.md` §3.2 (`stream_contract` boyutu), §3.5 (aday vaka) | H'nin otomatik ölçütleri aynı desenle rubrik boyutu olur; E uyuşmazlıkları aday vaka yoluna (`saved_views` page `cosre_evalset_candidate`) beslenir | örtüşme |
| `router-gaps-audit.md` D5 (`ChatContext.Trace/Operation`, zamir devralma, yalnız v2) | A bunu genişletir, yerine geçmez. D5 notu "sınıflandırıcı bağlam görmüyor" A'da çözülmeli | örtüşme |
| `router-gaps-audit.md` §1 (pencere `guidedRangeRe`, tavan [300, 86400]; D3 "hafta") | "dün" bugün ayrıştırılmıyor (`api/copilot_guided.go:1785`); A mutlak pencere ekler, trace kırılımı 24 sa tavanında kalır | örtüşme |
| `roadmap-status.md` "Kalıcı kurallar" | tek iş, `api.go` büyümez, v1'e davranış yok, audit'siz iş başlamaz — hepsi geçerli | kısıt |
| `roadmap-status.md` §2 satır 11c ("görev bazlı model yönlendirme başlamadı") | Soru 4'ün öncülü kodda yok: profil yüzeye göre seçilir. H'nin iki aileli koşusu 11c'nin beklediği ölçümdür | düzeltme |
| `roadmap-status.md` §4 sıra 5 (v1 emekliliği) | C'nin v2 istem varyantları v1 silinene kadar çift sabit demektir (§7-12) | sıra kararı |
| `agent-core-ideas.md` §3 (`SystemAddendum` yalnız `prompts.go` accessor'ı; `WithMaxTokensCap`) | B'nin sınıf eki ve C'nin örnekleri `v2Prepare` üzerinden iner (`api/chat_prepare_step.go:157-167`) | yeniden kullanım |
| `agent-core-ideas.md` §7 Q8 ("ölçüm olmadan davranış değişikliği yok") | C'den önce taban ölçümü (H1) şart | kısıt |

## 3. Sekiz soru

### 3.1 v2 yolunda sistem istemi — Durum: var, istek başına kuruluyor; tek istem değil, kademe başına
- Rota `api/cosre_v2_chat.go:27` → `cosreV2Chat` `:53-55` v1 gövdesini v2 işaretiyle çağırır (`chat.go:215`).
- Serbest döngü istemi `chat.go:1096-1109`: `systemChatAgentLoop` (`cop/prompts.go:2910-2968`, 4.212 B) + ekran / sayfa / sohbet bağlamı önsözleri + kural bloğu + koşullu ekler + `systemChat` (`:2277-2326`, 2.983 B) + `ChatAnswerStyle` (`:2205-2213`, 996 B) + `DataNotInstruction` (`:2118-2126`, 477 B) + `latencyEvidenceRule` (`:2133`, 323 B). v2 eki `chat.go:1136-1139` (kapsam 266–504 B, genel soru 432 B, rota incelemesi) çağrı anında sona eklenir (`api/chat_prepare_step.go:164`).
- Boyut: sabit çekirdek **8.991 B ≈ 3.000 token** **[DÜZELTME: föy 9,6 KB / 3.200 diyordu; satır aralığı sayımı yorum satırlarını da almıştı]**. v2 ekleriyle çağrı başına ≈ 9,7–9,9 KB ≈ 3.250–3.300 token; araç kataloğu şeması ve önsözler bu sayıda yok (§8). Döngü istemi her turda yeniden gider: tur maliyeti = çağrı × `model_calls`.
- Kural sayısı 35 (ajan çekirdeği 5 numaralı + 8 madde + gecikme kuralı; `systemChat` 15; biçim 6). Örnek diyalog: **yok**; yalnız satır içi örnekler (`"p99 2.130ms"`).
- Guided anlatım ayrı istemdir: `systemGuidedChat` (`cop/prompts.go:2139-2152`, 1.058 B, 8 kural) + aynı iki ek ≈ 1,9 KB ≈ 620 token, tek çağrı (`api/copilot_guided.go:2515`). Rapor rotaları kendi sabit bölümlü istemini kullanır (`:4631-4642`).
- Bayt tavanını pinleyen test yok; yalnız kural bloğu (5 kural / 600 B, `internal/ai/cosrerules/cosrerules.go:44-45`).

### 3.2 Turlar arası bağlam — Durum: kısmi
- Yapısal durum var: `ChatContext` (`api/chat_context.go:34-70`) cluster, namespace, workload, servis, pod, `RangeS` (şimdiden geriye saniye), yalnız-hatalı, süzgeç, aranan değer, son rota, bekleyen soru, v2'de trace ve operasyon. Redis anahtarı `copilot:ctx:<kullanıcı>:<konuşma>`, 24 sa (`:77-87`).
- **Yapısal olmayanlar:** ortam (her istekte istemciden, `fe/components/cosre/conversation/requestContext.ts:46-49`), kanal kodu, fonksiyon kodu, hitap, mutlak pencere (bitiş anı), son incelenen Problem, CoSRE'nin önerdiği sonraki adım.
- Yazım anı: istek sonunda tek `defer` (`chat.go:408-412`, 2 sn). Yazanlar: başarılı guided cevap (`api/copilot_guided.go:2247`, `api/copilot_intent.go:545`, `api/chat_scope.go:716`), açıklama turu (`chat.go:440`), operasyon ön-getirme (`chat.go:601`), modelin `set_context` aracı (`api/chat_context.go:444-454`). **Serbest döngü kendiliğinden yazmaz**; istem modele "her başarılı aramadan sonra set_context ile yaz" der (`cop/prompts.go:2945-2946`).
- Geçmiş yalnız metin: `sanitizeClientHistory` rol dışını, araç çağrısı / sonucu / ham içeriği düşürür (`api/chat_history_sanitize.go:32-50`, çağrı `chat.go:234`); tavan 40 mesaj ve 6.000 rune (`chat.go:64`, `internal/ai/assemble/history.go:34`). Önceki turun araç sonuçları sonraki turda yoktur; yalnız cevap metni kalır.
- Redis yoksa (`api/api.go:145` Noop önbellek) okuma boş, yazma etkisiz: önsöz çıkmaz, bekleyen soru çalışmaz, devam soruları yalnız metin geçmişine dayanır.
- İstemciye giden: yalnız `"sohbet bağlamı: …"` etiketi (`chat.go:413-415`) ve `data-scope`. Nesnenin kendisi gitmez.
- Kırılma noktaları: (a) "peki dün?" — `guidedRangeRe` yalnız "N gün/saat/dakika" okur (`api/copilot_guided.go:1785`), durumda bitiş anı yok; (b) "bak" — takip mutasyonu açık aralık ya da pod/log sözcüğü ister (`api/chat_followups.go:51-58`), öneri tutulmadığından neye bakılacağı yalnız modelin metinden çıkarımına kalır; (c) döngü cevabından sonraki tur — model `set_context` çağırmadıysa servis taşınmaz; (d) LLM niyet sınıflandırıcısı bağlamı görmez (`router-gaps-audit.md` D5 notu); (e) yeni konuşmanın ilk turu köprüyü okumaz (`chat.go:401`); (f) zamir devralma yalnız ekranda özne yokken (`api/chat_context_subject.go:84-121`).

### 3.3 Araç sonuçlarının modele gidişi — Durum: var; ham JSON, kırpılmış, ayrı özet katmanı yok
- Model yürütücünün ham JSON çıktısını görür (`chat.go:1503`), ortam notu eklenir (`:1506`), sonra kırpılır (`:1632`). v2 tavanı 2.048 bayt (`api/ai_loop_budget_settings.go:39`, `:91-96`), v1 6.000 rune (`api/chat_tool_budget.go:41`); kesim not düşülerek ama JSON ortasında yapılır. v2 araçları kendi gövdesini 2 KB'de tutar (`mcp/v2_tools.go:51`); taban katalogdan gelen araçlar kesilebilir.
- Anlatan modelin sayı kaynağı: aynı turun konuşmasındaki araç sonucu mesajları (`chat.go:1683`). Guided yolda kaynak sunucunun yazdığı düz metin kanıt bloğudur (`api/copilot_guided.go:2512`).
- Yapısal olgu çıkaran iki parça zaten var ama anlatıma girmiyor: `followupFactsFromTool` (varlıklar, `api/chat_followup_chips.go:163`) ve `guidedSummaryFacts` (üç rotanın sayıları, `api/chat_summary_card.go:26`).

### 3.4 Anlatımı yapan model — Durum: var; görev bazlı yönlendirme yok
- Çözüm sırası: açık / Derin profil > yüzey haritası > grup kardeşi > varsayılan (`cop/profiles.go:317-345`). Döngü yüzeyi `chat`, guided `chat-guided`, sınıflandırıcı `chat-intent`; ayar grupları yalnız `intent` ve `background` (`:781-789`). Bir turun bütün döngü çağrıları aynı profildedir (`api/chat_prepare_step.go:157-167`).
- Parametreler: `max_tokens` profil ya da 4.096 (`cop/copilot.go:428`, `:956`); `temperature` profil ya da 0,2, yalnız openai-uyumlu gövdede (`cop/copilot.go:433`, `internal/ai/provider/tools.go:463-464`); `top_p` gönderilmez; düşünme profil alanı `""/off/on` (`internal/ai/modelcaps/modelcaps.go:31-37`, `cop/provider_calls.go:84`), düşünce metni akmaz. Yineleme sonrası yeniden deneme yalnız Derin kipte, 700 token tavanla (`api/chat_answer_degen.go:55`).
- Prod'da hangi modelin hangi yüzeye bağlı olduğu ayar blobundadır (§8).

### 3.5 Çıktı son işlemesi — Durum: var, deterministik, `answer` öncesinde; yalnız döngüde tam
- Döngü: `finalAnswerText` (`chat.go:1245-1280`) yineleme / yankı süzgeci → link süzgeci, grafik, sayı uyarısı, kaynak künyesi (`:1367-1373`) → `emit("answer")` (`:1384-1385`; tavan turunda `:1752-1768`). v2'de düz "Kaynak:" satırı adım çipine taşınır (`:1368-1371`).
- Akış: araç sunulan turda ilk 160 görünür rune tutulur (`internal/ai/provider/tools_stream_gate.go:47`, `:230`); kısa önsöz + çağrı hiç görünmez, uzun önsöz `Reset` ile geri alınır. `answer` gelince çevirici açık taslağı `text-end` + `data-reset{transient}` ile geri alır, otoriter metni yeni tek parça olarak yollar (`uistream/translator.go:204-214`, `:233-243`).
- **Ekleme yeri:** tek tek kademelerde değil. `answer` 37 dosyada yaklaşık 85 yerden yayınlanıyor; ortak nokta `emit` sarım zinciri (`chat.go:283-297`). v2'ye özel en dış sarıcı ("cevap dikişi") `answer` yükünü yayından önce dönüştürürse bütün kademeleri kapsar, kayda geçen cevap da düzeltilmiş hâl olur. Yeni parça türü gerekmez; mevcut altın dosyalar değişmez.
- Guided anlatımında tutma kapısı yok: delta doğrudan akar (`api/copilot_guided.go:2515-2517`), sonra sunucu `AnswerTail` + `"\n\nKaynak: "` ekler (`:2538`) — v2'de de metnin içinde kalır.

### 3.6 Örnekleme sorguları — Durum: yazıldı (§4)
Tablo `cosre_interactions` (`internal/chstore/store.go:2986-3028`): soru ve cevap aynı satırda, cevap 8 KiB'de kırpılır (`api/cosre_interactions.go:34`), `ORDER BY (created_at, tier)`. `answer_chars` adına rağmen **bayt** uzunluğudur **[DÜZELTME]** (`api/chat_exchange.go:563`, `len(answer)`). v1 / v2 ayıran kolon yok.

### 3.7 Kullanıcı düzeltmeleri ve takma ad — Durum: varlık düzeltmesi yok; kural tarafı kısmi
- "O değil, öbür servis" hiçbir yere yazılmaz. `team_aliases` LDAP takım adını telemetri takım adına eşler, genel, admin yazar, ≤500 (`api/team_aliases.go:36`); servis takma adı değildir.
- Operatör kuralları: `system_settings` `cosre_rules`, ≤200 kural, kapsam genel / takım / servis (`internal/ai/cosrerules/cosrerules.go:35`, `:56-58`). Hafıza kademesi kendisi kaydetmez; `cosre_rule` aksiyon kartı döner, kayıt editor+ tıkla (`api/chat_memory.go:346-348`). Kullanıcı başına depo yok.
- Varlık çözücü öğrenilmiş eşleme okumaz (`mcp/resolve_entity.go`).

### 3.8 Araç çağrısı sırasında akan — Durum: var (çip); ara cümle yok
- Çağrı başına `step` (araç adı, argüman, `phase`) ve `step-result` (`chat.go:1438`, `:1545-1620`); bileşik araç ilerleme satırı (`api/chat_step_phase.go:74-80`). Hepsi sabit sunucu metni ya da araç verisi.
- Evre haritası hazır: `stepPhaseByTool` araç → 12 aile (`api/chat_step_phase.go:20-65`). Eksikler: v2'ye özel araçlar haritada yok (`find_traces`, `open_problems`, `endpoint_metrics`, `list_my_services`, `get_log_anomalies`, `explain_ml_event`); v2 çeviricisi araç adımında `phase` alanını taşımaz (`uistream/translator.go:124-135`), yalnız etiketli `data-step` taşır **[föye ek]**.
- İptal: istemci `stop()` bağlantıyı keser; sunucu kalan çağrıları atlar, modeli yeniden çağırmaz (`chat.go:1668-1676`). O ana dek gelen çipler mesajda kalır, sonraki istekte metne "yarıda durduruldu" notu eklenir (`fe/lib/cosre/cosrePersist.ts:19`, `:194`). Bağlantı kapandığı için sunucu özet gönderemez.

### 3.9 Kademe zinciri: hangisi kalıp üretiyor
Sıra (`chat.go`): kod-yapışkan `:419` → açıklama `:442` → kapsam `:463` → hafıza `:481` → netleştirme cevabı `:494` → doküman/varlık sorusu `:554` → kaynak keşfi `:561` → belge satırı `:575` → wiki `:582` → operasyon ön-getirme `:599` → rota planı `:636` → operasyon-neden `:643` → uç nokta kartı `:658` → guided `:667` → kaynak sorusu `:713` → çekmece `:721` → RAG `:732` → LLM niyet `:749` → serbest döngü `:1299` **[DÜZELTME: föy satırları 5–11 satır kaymış ve v2'ye özel üç kademeyi atlamıştı]**.

| Kademe (yüzey) | Metni kim yazar | Kalıp görünümü | B–F kapsamı |
|---|---|---|---|
| guided / niyet→guided (`chat-guided`) | model, tek araçsız çağrı; sunucu eki | "1-2 cümle, sonra sayıları sırala, madde işareti", hükümle başla (`cop/prompts.go:2144-2150`); her cevabın sonunda `Kaynak:` satırı | **evet** — durum soruları burada cevaplanır |
| niyet `none` → genel cevap (`chat-general`) | model, araçsız | her cevabın başında sabit sunucu notu (`api/copilot_intent.go:364`) | evet (D: not çipe) |
| serbest döngü (`chat`, `chat-prefetch`) | model | `**Özet:**`, tablo, liste (aşağıda) | evet |
| wiki, RAG, çekmece | model, `ChatAnswerStyle` ile | `**Özet:**` + biçim kuralları | C ve D; E yalnız sayı kanıtı varsa |
| açıklama turu, servis raporu, kaynak incelemesi, rota incelemesi | model, sabit bölümlü istem | bilerek rapor ("KARAR kutusu", altı bölüm) | **hayır** — istenmiş rapor, ölçütlerin dışında tutulur |
| kapsam, hafıza, netleştirme, yol tarifi, belge satırı, keşif, operasyon seçimi | sunucu şablonu | sabit cümle | hayır (deterministik; sayı koddan gelir) |

Sonuç: B–E en az üç yere uygulanmalı (guided anlatım, genel cevap, döngü). `on_no_loop` kipinde sınıflandırıcı `none` derse döngüye hiç girilmez (`api/copilot_intent.go:458`); kademe dağılımı §4 S1 ile ölçülmeden hangi istemin ağırlık taşıdığı bilinemez.

## 4. Örnekleme sorguları (operatör koşar)

Hepsi 14 günle ve `max_execution_time` ile sınırlı; S2 ve S3 yalnız model anlatımı üreten kademelere bakar (S1 bilerek hepsine). Kalıp listeleri en üstte, düzenlenebilir.

```sql
-- S1: kademe ve rota dağılımı + tur başına token tabanı (kabul ölçütü 5'in "bugünkü değeri")
SELECT tier, surface, count() AS tur,
       round(avg(input_tokens)) AS ort_giris_token, round(avg(output_tokens)) AS ort_cikis_token,
       round(avg(model_calls), 2) AS ort_model_cagrisi, round(avg(length(tool_name)), 2) AS ort_arac
FROM cosre_interactions
WHERE created_at >= now() - INTERVAL 14 DAY
GROUP BY tier, surface ORDER BY tur DESC LIMIT 80
SETTINGS max_execution_time = 30;
```

```sql
-- S2: kalıp açılış/kapanış, uzunluk, liste, soruyla bitiş (kademe başına + toplam)
WITH
  ['guided','intent','loop','drawer','rag','wiki'] AS sohbet_kademeleri,
  ['tabii','elbette','merhaba','selam','harika bir soru','güzel soru','anladım','hemen bakıyorum',
   'size yardımcı','yardımcı olayım','aşağıda','işte ','öncelikle','verilen veriye göre',
   'sağlanan verilere göre','verilere göre','elimdeki verilere göre','incelediğimde','analiz sonucunda'] AS acilis_kaliplari,
  ['başka bir sorunuz','başka sorunuz','başka bir konuda','yardımcı olabileceğim başka','umarım yardımcı',
   'umarım faydalı','sormaktan çekinme','bana bildirin','haber verin','iyi çalışmalar','kolay gelsin',
   'daha fazla bilgi isterseniz','detay isterseniz','ister misiniz'] AS kapanis_kaliplari,
  replaceRegexpOne(trimBoth(answer), '\\n+(\\S+ )?Kaynak: [^\\n]*$', '') AS govde,
  lowerUTF8(replaceAll(substringUTF8(replaceRegexpOne(govde, '^[\\s*_>#-]+', ''), 1, 80), 'İ', 'i')) AS bas,
  lowerUTF8(replaceAll(substringUTF8(govde, greatest(1, toInt64(lengthUTF8(govde)) - 199), 200), 'İ', 'i')) AS son,
  answer_chars <= 8192 AS tam
SELECT tier, count() AS tur,
  round(100 * countIf(arrayExists(p -> startsWith(bas, p), acilis_kaliplari)) / tur, 1) AS acilis_kalip_pct,
  round(100 * countIf(startsWith(bas, 'özet:')) / tur, 1) AS ozet_satiri_pct,
  round(100 * countIf(startsWith(bas, 'bu soru telemetriyle eşleşmedi')) / tur, 1) AS sunucu_notu_pct,
  round(100 * countIf(match(answer, '\\n+(\\S+ )?Kaynak: [^\\n]*$')) / tur, 1) AS kaynak_satiri_pct,
  round(100 * countIf(tam AND arrayExists(p -> position(son, p) > 0, kapanis_kaliplari)) / countIf(tam), 1) AS kapanis_kalip_pct,
  round(100 * countIf(tam AND endsWith(trimRight(govde), '?')) / countIf(tam), 1) AS soruyla_biten_pct,
  round(100 * countIf(match(answer, '(?m)^\\s*([-*•]|\\d+[.)])\\s')) / tur, 1) AS madde_listeli_pct,
  round(100 * countIf(match(answer, '(?m)^\\|.*\\|\\s*$')) / tur, 1) AS tablolu_pct,
  round(avg(answer_chars)) AS ort_bayt, round(quantile(0.5)(answer_chars)) AS p50_bayt,
  round(avgIf(lengthUTF8(govde), tam)) AS ort_karakter,
  round(avgIf(length(extractAll(govde, '[.!?…](\\s|$)')), tam), 1) AS ort_cumle,
  round(avg(output_tokens)) AS ort_cikis_token
FROM cosre_interactions
WHERE created_at >= now() - INTERVAL 14 DAY
  AND has(sohbet_kademeleri, tier) AND status = 'ok' AND empty_answer = 0
GROUP BY tier WITH TOTALS ORDER BY tur DESC
SETTINGS max_execution_time = 30;
```

```sql
-- S3: aynı sorunun 2 dakika içinde yeniden sorulması (kullanıcı başına ardışık tur)
SELECT count() AS tur,
  countIf(ara_s <= 120 AND ayni_metin) AS ayni_metin_2dk,
  countIf(ara_s <= 120 AND jaccard >= 0.5) AS benzer_2dk,
  round(100 * benzer_2dk / tur, 1) AS yeniden_sorma_pct,
  countIf(attempt != '') AS istemci_beyanli_tekrar
FROM (
  SELECT attempt, dateDiff('second', onceki_an, created_at) AS ara_s, q = onceki_q AS ayni_metin,
    if(empty(toks) OR empty(onceki_toks), 0,
       length(arrayIntersect(toks, onceki_toks)) / length(arrayDistinct(arrayConcat(toks, onceki_toks)))) AS jaccard
  FROM (
    SELECT created_at, attempt, q, toks,
      lagInFrame(created_at) OVER w AS onceki_an, lagInFrame(q) OVER w AS onceki_q, lagInFrame(toks) OVER w AS onceki_toks
    FROM (
      SELECT created_at, user_id, attempt, lowerUTF8(trimBoth(question)) AS q,
        arrayDistinct(arrayFilter(t -> lengthUTF8(t) > 1, splitByRegexp('[^\\p{L}\\p{N}_]+', q))) AS toks
      FROM cosre_interactions
      WHERE created_at >= now() - INTERVAL 14 DAY AND user_id != ''
        AND tier IN ('guided','intent','loop','drawer','rag','wiki')
    )
    WINDOW w AS (PARTITION BY user_id ORDER BY created_at ROWS BETWEEN 1 PRECEDING AND CURRENT ROW)
  )
)
SETTINGS max_execution_time = 30;
```

```sql
-- S4: v2 turlarını ayırma vekili (kolon yok). Önce bayrağın açıldığı an:
SELECT time, actor_email, details FROM audit_log
WHERE action = 'settings.cosre_v2.update' AND time >= now() - INTERVAL 90 DAY ORDER BY time
SETTINGS max_execution_time = 30;
-- sonra gün başına v2 izi taşıyan tur sayısı (sağlama):
SELECT toDate(created_at) AS gun, count() AS tur,
  countIf(arrayExists(t -> t IN ('list_my_services','open_problems','find_traces','endpoint_metrics',
                                'get_log_anomalies','explain_ml_event'), tool_name)) AS v2_arac_izli,
  countIf(match(surface, '/(problems_root_cause|operation_root_cause|business_code|operation_not_found|not_available|trace_search_link)')) AS v2_rota_izli
FROM cosre_interactions
WHERE created_at >= now() - INTERVAL 14 DAY GROUP BY gun ORDER BY gun
SETTINGS max_execution_time = 30;
```

Yaklaşık olanlar ve nedeni:
- **Kapanış kalıbı, soruyla bitiş, cümle sayısı, karakter ortalaması** yalnız kırpılmamış satırlarda (`answer_chars <= 8192`) hesaplanır; uzun cevapların sonu tabloda yok. Cevap grafik çitiyle bitiyorsa "soruyla bitiş" kaçar. Cümle sayısı noktalama sayımıdır; numaralı liste satırı cümle sayılır.
- **Açılış kalıbı** ilk 80 karakterde önek eşleşmesidir; liste dışı kalıpları görmez. `**Özet:**`, sunucu notu ve `Kaynak:` satırı ayrı kolonlardadır çünkü bunları model değil istem ve sunucu üretir; kabul ölçütü 2'ye dahil edilip edilmeyeceği §7-9.
- **Yeniden sorma** ürünün gece türetimiyle aynı eşiği kullanır (2 dk, kelime Jaccard ≥ 0,5; `internal/cosrelearn/signals.go:128-131`, `:282`), ama ardışıklığı kullanıcı başına sayar ve Türkçe ekleri kök saymaz.
- **v2 ayrımı vekildir:** bayrak geneldir ve açıkken her açılış v2'ye gider (`fe/lib/cosre/pickChatImpl.ts:13-15`), dolayısıyla en iyi vekil bayrağın açıldığı andan sonraki turlardır. Araç ve rota izleri yalnız sağlamadır: v2 aracı çağırmayan v2 turunu görmez; rota etiketlerinin `surface` kolonuna bu adlarla yazıldığı doğrulanmadı.
- S2 dış `WITH` sabitlerini aynı `SELECT` içinde kullanır; S3 pencere fonksiyonunu ayrı düzeyde tutar (eski çözümleyicide iç içe ifade hatası vermesin).

## 5. Hedef üslubu bugün engelleyenler

1. **İstem rapor istiyor.** Çelişen kurallar, aynen: "cevap sorunun gerektirdiği kadar EKSİKSİZ olsun, kısaltmak için bilgi atma"; "İlk satır tek cümlelik özet: \"**Özet:** …\""; "Adım adım prosedür → numaralı liste …; karşılaştırma → tablo; tek tek olgular → madde işaretleri" (`cop/prompts.go:2207-2209`); "CEVAPLA — kısa özet, sonra dar bir tablo (cluster, namespace, workload/servis, pod, hata oranı, p95)" (`:2935`); guided'da "Kısa ve taranabilir yaz: madde işaretleri kullan" (`:2150`). Hedefle uyumlu olup kalacaklar: "Giriş cümlesi, tekrar ve kapanış nezaketi yazma" (`:2213`), "Cevaba doğrudan bulguyla başla" (`:2316`), "Emin değilsen güven seviyeni ve nedenini belirt" (`:2326`).
2. **Örnek yok, kural çok.** 35 kural, sıfır diyalog; küçük model üslubu kuraldan değil örnekten alır.
3. **Önceki tura bağlanacak malzeme yok.** Model önceki turun yalnız metnini görür; sayıları ve kapsamı yapısal olarak taşıyan bir şey yok, o yüzden "Hayır, o saatte rollout yok…" gibi bağlanan cümle ancak tesadüfen çıkar.
4. **Uzunluğu belirleyen bir şey yok.** Tek uzunluk aracı 4.096 token tavanı; soru sınıfı cevap ölçüsüne bağlanmıyor.
5. **Sunucu da kalıp ekliyor:** guided `Kaynak:` satırı, genel cevap notu, `AnswerTail`.
6. **Serbest anlatımın güvencesi tek yolda.** `ungroundedNumbers`'ın tek çağıranı trace takibi (`api/chat_trace_followup.go:333`; `chat.go:1372`, `:1757`); istem bu yüzden "her sayıyı aynen aktar, kaynağını göster" diye sıkılaştırılmış, bu da rapor dilini besliyor.
7. **Ara cümle için kanal yok.** Modelin kısa önsözü bilerek gizleniyor; sunucunun yerine koyduğu şey araç adı çipi.

## 6. Dilim planı

### 6.0 Dilimlerin paylaştığı üç dikiş (iki ayrı tasarım yerine tek tasarım)
- **Konuşma durumu = kapsam + kanıt defteri, tek nesne.** `ChatContext` genişletilir (yeni alanlar `omitempty`; v1 yazmaz, bayt aynı — D5 emsali). Kapsam: servis(ler), ortam, cluster, pencere `{rangeS, toMs, etiket, açık mı}`, kanal kodu, fonksiyon kodu, Problem, trace, operasyon, hitap, `offer` (sunucunun önerdiği sonraki adım). Defter: son turun en çok 5 olgusu `{araç, kapsam, ad, değer, birim, pencere}`. "Kanıtı turlar arası taşı" önerisi ile A ve E aynı şeydir: defterin tam hâli o turda E'nin kanıtıdır (bellekte, kırpılmamış çıktı — `chat.go:1483-1487` bunu takip yolunda zaten yapıyor), sıkıştırılmış hâli sonraki tura önsöz olur. Besleyenler hazır: `followupFactsFromTool`, `guidedSummaryFacts`, guided rota yaması.
- **Cevap dikişi.** v2'de `emit` zincirinin en dışına tek sarıcı: `delta` için cümle kapısı (cümle tamamlanmadan yayınlama), `answer` için saf dönüştürücüler (kalıp süzgeci, doğrulayıcı). Bütün kademeleri kapsar, ikinci geri alma mekanizması kurmaz.
- **v2 anlatım istemleri.** `ChatAnswerStyle` v1 ile ortak sabit; v1 değişmeyeceği için v2 varyantı ayrı accessor olur, seçim `cosreV2Marked` ile. Değişken blok (durum, defter, sınıf eki, seçilen örnekler) istemin SONUNA konur; bugün değişken önsözler iki sabit bloğun ortasında (`chat.go:1096-1109`), bu önek önbelleğini 4,2 KB'den sonra kırıyor.

### 6.1 Dilimler (yeniden kesilmiş)

| Dilim | Kapsam (koda göre) | Dosyalar | Test | Risk | Tur başına token (3 B/token) | Ölçüt | Anahtar |
|---|---|---|---|---|---|---|---|
| **H1** ölçüm tabanı | 20 soruluk çok turlu Türkçe set; koşucuya "konuşma kipi": vaka `turns[]`, turlar arası bellek içi durum, kademe zinciri atlanmaz (bugün ajan kipi guided, niyet, netleştirme ve bağlamı atlar: `chat.go:402-406`, `:493`, `:667`, `:749`); iki profil ardışık (koşu isteğinde profil seçimi bugün yok, yalnız `surfaces` ve `toolset`: `api/ai_evalset_runs.go:315-320`); otomatik ölçütler (S2 ifadelerinin Go karşılığı, tur sayısı v0.10.2318+ telemetrisinden); çıktı `docs/cosre/eval/fluency-<tarih>.md` | `api/ai_evalset_core.go`, `ai_evalset_agent.go`, yeni `api/ai_evalset_fluency.go`, `cop/evalset/fluency.json`, `internal/ai/evalrubric/` | ölçüt fonksiyonları tablo testi; çok turlu sahte modelle koşu | düşük; koşu başına ≈ 20 soru × ~2,5 tur × 2 aile ≈ 100 sohbet turu model yükü, elle tetik | canlı tur 0 | 2, 4, 5 ölçümü | yok |
| **A** durum nesnesi | `ChatContext` genişler; deterministik çözücü her turun başında (göreli zaman: dün, bugün, geçen hafta, "aynı saat"; ortam; kod; hitap; `offer`); döngü araç argümanı ve sonucundan otomatik yazım; sınıflandırıcıya durum satırı; `data-state` parçası; istemciden `context.statePatch`. Küçük model çağrısı eklenmez: gereken yerde mevcut niyet çağrısının JSON'u kullanılır. İkinci sürüm: v2 kataloğundan `set/get/clear_context` ve istemdeki BAĞLAM KURALLARI (640 B) çıkar | `api/chat_context.go`, yeni `api/chat_state.go`, `chat_state_resolve.go`, `chat.go` (yazım noktası), `copilot_intent.go`, `uistream/part.go` + `translator.go`, `api/chat_stop_event.go`, `fe/lib/cosre/protocol.ts`, `requestContext.ts` | çözücü tablo testi; referans diyalogun üç devamı (deploy olmuş mu / bak / peki dün) Redis sahtesiyle; yeni altın `ui-stream-v1-state`; `v2_loop.sse` ve `v2_help.sse` yenilenir, v1 altınları değişmez | orta-yüksek: yanlış devralma. Sınır: açık ifade kazanır, devralınan alan cevapta söylenir, D5'in <80 rune kuralı korunur | çağrı başına −110…0 (önsöz + defter ≤ +500 B, çıkan kural −830 B); araç şeması çıkınca ek düşüş (ölçülmedi) | 1 | "durum" |
| **E1** doğrulayıcı, gölge | Defter + genişletilmiş denetim, cevabı DEĞİŞTİRMEDEN sayar. Eklenenler: bütün anlatım kademeleri (bugün tek yol); birim dönüşümü (ms/sn/dk, oran ↔ yüzde); büyüklük sözcüğü ("74 bin" ↔ 74.167, "1,2 milyon"); sayı sözcüğü (bir…on, yarı); saat/tarih (kullanıcı saat dilimine çevrilmiş kanıt damgasıyla, ±1 dk ya da kova boyu — bugün maskelenip atlanıyor, `api/numeric_claims.go:37`); ad denetimi varlık modeli üzerinden (defter ∪ durum ∪ katalog). Sinyal: `verify_checked` / `verify_mismatch` | `api/numeric_claims.go`, yeni `api/answer_verify.go`, `api/turn_ledger.go`, `cosrelearn/signals.go`, `api/cosre_system_signal.go` | tolerans tablo testi (her kural için eşleşen / eşleşmeyen çift); mevcut `numeric_claims` testleri aynen | düşük (gölge); asıl bilgi yanlış pozitif oranı | 0 | 3 (hazırlık) | "doğrulama" = gölge |
| **B** niyet ve ölçü | Sınıf saf eşlemeden: guided niyet → sınıf (durum / hata avı / açıklama / karşılaştırma / bilgi / sohbet / rapor); döngüde deterministik ipucu. Sınıf yalnız uzunluk verir: tek satır ek + `WithMaxTokensCap`. Eksik bilgi: cevap değişmeyecekse varsay ve söyle (durumun `assumed` alanından sunucu cümlesi), değişecekse tek `data-question` (yeni tür `slot`) | yeni `api/chat_answer_class.go`, `api/chat_prepare_step.go`, `copilot_guided.go` (anlatım çağrısı), `api/chat_question.go`, `uistream/part.go`, `cop/prompts.go` | sınıf eşleme tablo testi; soru kartı altını güncellenir | orta: token tavanı cümleyi yarıda keserse kötü; tavan cümle sınırında bitirme kuralıyla birlikte | giriş +40; çıkış durum sorularında tahmini −%30…−%60 (S2 `ort_cikis_token` ile doğrulanır) | 4 | "üslup" |
| **C** örnekle üslup | v2 varyantlarında çıkanlar: `ChatAnswerStyle` (996 B), `systemChat` CEVAP bloğundan biçim maddeleri (726 B), adım 5 tablo emri (520 B), CEVAP ÜSLUBU paragrafı (388 B) = 2.630 B. Girenler: ≤500 B üslup çekirdeği + sınıfa göre 3–4 örnek (her biri ≤450 B). Guided varyantında "madde işaretleri" çıkar, 1–2 örnek girer. 8–10 diyalog sürümlü dosyalarda; bayt bütçesi testle pinlenir (yeni ≤ bugünkü 8.991 B) | `cop/prompts.go` (v2 accessor'ları), yeni `cop/styledialogs/` (+ `docs/cosre/style-dialogs/` dizini, §7-8), `chat.go:1096-1109` sıralama, `copilot_guided.go:4631` | bütçe pini; dil kapısı; örneklerde gerçek ad yok pini; v1 istem baytı değişmedi pini | **yüksek**: tüm cevapların dili değişir; küçük model örneği kopyalayabilir (örnekler farklı konu ve sayılarla, 3+ aileden) | döngü çağrısı −110…−260; guided çağrısı +150 giriş, çıkışta düşüş beklenir — H1 ile ölçülür, aşarsa guided tek örneğe iner | 2, 4, 5 | "üslup" |
| **D + E2** cümle kapısı | Cevap dikişi devreye girer. D: yasak açılış/kapanış listesi ayardan; ilk ve son cümle süzgeci, yeniden üretim yok; v2'de guided `Kaynak:` satırı ve genel cevap notu çipe taşınır (döngü emsali `chat.go:1368-1371`). E2: uyuşmayan cümle taslakta tutulur, `answer` metninde "(doğrulanmamış değer)" notuyla işaretlenir; yeniden üretim yalnız Derin kipte bir kez | yeni `api/answer_seam.go`, `chat.go:283-297`, `chstore/settings.go` (blob), `api/chat_source_note.go`, `copilot_intent.go` | süzgeç tablo testi; akış testi: yasak cümle hiçbir `text-delta`'da yok; uyuşmayan sayı hiçbir parçada işaretsiz yok | orta: yanlış pozitif doğru sayıyı işaretler → E1 gölge oranı eşiğin altına inmeden açılmaz; ilk cümle bir cümle gecikir | 0; yeniden üretim açılırsa uyuşmazlık turunda +1 model çağrısı (≈ o turun giriş tokenı kadar) | 2, 3 | "doğrulama" = uygula; D "üslup" altında |
| **F1** ara cümle | Araç turu başlarken sunucu cümlesi; havuz araç başına değil evre ailesi başına (12 aile × 3–4 cümle), art arda aynı cümle yok (adım kimliğiyle deterministik seçim); paralel yürütme gelirse tur başına tek cümle. v2 araçları evre haritasına eklenir | `api/chat_step_phase.go`, yeni `api/chat_status_line.go`, `uistream/part.go` + `translator.go`, `api/chat_stop_event.go`, FE durum satırı | havuz testi (tekrar yok, bilinmeyen araç → cümle yok); yeni altın `ui-stream-v1-status`; `v2_loop.sse` yenilenir | düşük | 0 | — (hedef üslup) | "üslup" |
| **G** düzeltmeden öğrenme | Düzeltme ipucu (kapalı sözcük listesi) + önceki turun çözülen varlığı + bu turun yeni varlığı → durum güncellenir, `cosre_alias` aksiyon kartı önerilir; onayda yazılır. Olumlu ve olumsuz eşleme (yanlış aday da saklanır). Çözücü kullanıcının eşlemesini katalog sıralamasından önce uygular | yeni `api/cosre_user_prefs.go` (`registerCosreUserPrefsRoutes`), `mcp/resolve_entity.go`, `api/copilot_guided.go` (servis çıkarımı), `api/chat_memory.go` (kart deseni), FE aksiyon kartı | "aynı kullanıcıda aynı yanlış eşleme ikinci kez çıkmaz" tablo testi; tavan ve çıkarma testi | orta: yanlış öğrenilen eşleme kalıcı hata olur → yalnız onayla, kullanıcı silebilir | uygulanınca ≤ +30 | — (G hedefi) | yok (onaylı) |
| **H2** hakem ölçütleri | Gece hakemine iki etiket: "doğal mı", "sorulanı cevapladı mı"; kapı değil, yalnız yerel profil (`cosrelearn.LocalOnlyJudge` mevcut) | `cop/prompts.go:3019`, `cosrelearn/grounding.go` | ayrıştırma testi | düşük; gece çağrı tavanı 40 değişmez | canlı tur 0 | 2 (ikincil) | yok |

Dışarıda bırakılan: **F2 "durdur ve özetle"**. İptal bağlantıyı kestiği için sunucu özet yollayamaz; gereken şey ayrı bir yumuşak durdurma ucu, podlar arası işaret (Redis) ve bir model çağrısıdır. Ucuz karşılığı istemcide: durdurulan mesajın altına eldeki araç sonuçlarından deterministik tek satır. Karar §7-11.

### 6.2 Önerilen sıra
1. **H1** — taban ölçülmeden hiçbir istem değişmez; S1–S4 ile birlikte "bugünkü değer" kayda geçer.
2. **A** — ölçüt 1; B, C ve E hepsi durum nesnesine yaslanır.
3. **E1** — defter A ile aynı sürüm ailesinde; gölge sayaç bugünkü istemle uydurma oranını da verir.
4. **B** — C örnekleri sınıfa göre seçeceği için önce sınıf.
5. **C** — en riskli değişiklik, ölçüm ve geri dönüş anahtarı hazırken.
6. **D + E2** — C'den sonra kalan kalıp oranı ölçülür; %5'in altındaysa D yalnız sunucu satırlarının çipe taşınmasına iner.
7. **F1**, 8. **G**, 9. **H2**.

Birleşenler: D ile E2 aynı dikiş, tek sürüm; A ile "kanıt defteri" tek nesne; G'nin kullanıcı deposu hitap tercihini de tutar. Düşebilecekler: D'nin süzgeç kısmı (ölçüme bağlı), H2 (operatör `fluency-<tarih>.md`'yi kendisi puanlıyorsa), F2.

## 7. Operatöre açık kararlar

1. **Kapsam ve istem dondurması.** B–E guided anlatıma ve genel cevaba da uygulansın mı, yoksa yalnız döngüye mi? Öneri: üçüne de; yoksa ölçüt 2 ve 4 tutmaz. Rapor türleri (servis raporu, kaynak incelemesi, trace açıklaması, rota incelemesi) ölçütlerin dışında kalır.
2. **Durum: genişlet mi, yenisi mi?** Öneri: `ChatContext` genişler, aynı Redis anahtarı. Redis yokken: istemcinin son `data-state`'i `context.state` ile geri yollamasına izin verilsin (alanlar kataloğa doğrulanır; asla doğrulayıcı kanıtı olmaz). Öneri: evet, davranış Redis'li ve Redis'siz aynı olur.
3. **`set_context` / `get_context` / `clear_context`.** Sunucu durumu yazınca v2 kataloğundan çıksın mı? Öneri: A'nın ikinci sürümünde çıkar; v1 aynen.
4. **Düzenlenebilir etiketler ve 2026-10-29 çip kararı.** Öneri: sözleşme şimdi (`data-state`, `statePatch`); arayüzde önce tek satır salt-okur bağlam, düzenleme sonra ve ayrı onayla.
5. **Anahtar sayısı.** İstenen dilim başına anahtar = 8 yeni anahtar; alışkanlık "yeni vida yok". Seçenekler: (a) 8; (b) tek blob `cosre_conversation` içinde 3 alan — "durum" (A, G), "üslup" (B, C, D, F1), "doğrulama" (kapalı / gölge / uygula; E); H anahtarsız; (c) tek anahtar. Öneri: (b); gerilemeyi ayırmaya yeter, `cosre_v2` genel kapı olarak kalır.
6. **E düzeltme kipi.** İşaretle ("doğrulanmamış değer"), cümleyi sil, ya da bir kez yeniden üret. Öneri: işaretle; yeniden üretim yalnız Derin kipte. Ölçüt 3'ün tam karşılanması için taslakta cümle kapısı (ilk yayın bir cümle gecikir) kabul mü? Öneri: evet.
7. **E toleransı.** Öneri: yazılan son anlamlı basamağın yarım birimi ("74 bin" → 73.500–74.499); "yaklaşık / ~" ±%5; birim dönüşümü kabul; türetilmiş değer (fark, kat, toplam) araç çıktısında yoksa doğrulanmamış sayılır — bu, araçların farkı kendisinin döndürmesini teşvik eder. Uygulamaya geçiş eşiği: gölgede 7 gün, işaretlenenlerden 50 örnekte yanlış pozitif ≤ %2.
8. **Örnek diyalogların yeri.** `go:embed` paket dizini dışını okuyamaz; `docs/cosre/style-dialogs/` doğrudan gömülemez. Öneri: tek kopya `cop/styledialogs/` altında (evalset emsali), `docs/cosre/style-dialogs/` yalnız dizin ve inceleme notu. Alternatif: iki kopya + eşitlik testi.
9. **Kalıp tanımı.** `**Özet:**`, `Kaynak:` satırı ve genel cevap notu ölçüt 2'de sayılsın mı? Öneri: sayılsın; ilkini C kaldırır, diğer ikisi v2'de çipe taşınır.
10. **Hakeme iki ölçüt** (`quality-loop-audit.md` §5 ile çelişir). Öneri: H2 en sona, kapı değil, gece tavanı aynı; operatör puanlaması yetiyorsa hiç yapılmaz.
11. **F: parça türü ve iptal.** Ara cümle için geçici `data-status` (mesaja ve arşive girmez) mi, mevcut `data-step` mi? Öneri: `data-status`. F2 için öneri: ertele; istenirse önce istemci tarafı deterministik satır.
12. **v1 emekliliği sırası.** v2 istem varyantları v1 silinene kadar çift sabit. Öneri: kabul; v1 silinirken eski sabitler de gider.
13. **Hitap.** Varsayılan ve saklama yeri. Öneri: kullanıcının kendi yazımından deterministik (sen / siz), varsayılan "sen", kullanıcı deposunda kalıcı.
14. **`none` + özne.** Durumda özne varken sınıflandırıcı `none` derse tur araçsız genel cevaba değil döngüye gitsin mi ("bak" bunu gerektirir)? Öneri: evet, yalnız v2 ve yalnız özne varken.
15. **G deposu.** `saved_views` page `cosre_user`, kullanıcı başına tek satır, ≤50 eşleme, ≤4 KiB, en eski çıkar; viewer kendi eşlemesini kaydedebilir. Öneri: evet (kullanıcı durumu; admin yazması değil).

## 8. Koddan doğrulanamayanlar ve bilgi föyü düzeltmeleri

| Doğrulanamayan | Hangi prod değeri kapatır |
|---|---|
| Kademe dağılımı; `chat-guided` / genel cevap / döngü payı | S1 |
| Sınıflandırıcı kipi (`on_no_loop` mu `on` mu) | AI ayar blobu; S1'de `loop` payı |
| Redis'in prod'da bağlı olduğu | dağıtım ortam değişkeni; "sohbet bağlamı" çipinin görünmesi |
| `chat`, `chat-guided`, `chat-intent` yüzeylerinin profili: model ailesi, `max_tokens`, `temperature`, düşünme | profil ayarı; `ai_calls.model`, `profile_id` |
| Gerçek bayt/token oranı (3 varsayıldı) ve araç kataloğu şemasının token maliyeti | `ai_calls.input_tokens` ile `prompt_sample` boyu; araçsız ve araçlı çağrı farkı |
| `ai.loopBudget`, `ai.generalQuestions`, `cosre_v2` bayrağının açılış tarihi | ayar blobları; S4 ilk sorgu |
| Rota etiketlerinin `surface` kolonundaki yazımı | S1 çıktısı |
| `lowerUTF8('İ')` davranışı, pencere fonksiyonu ve `splitByRegexp` desteği | prod ClickHouse sürümü; S2/S3'ün hatasız koşması |
| v2 arayüzünün etiketli adımı akış sırasında nasıl çizdiği | tarayıcıda bakılır (bileşen okunmadı) |
| Görev tanımında anılan önceki döngü denetimi (tur bütçesi, kanıt defteri, paralel araç, önek önbelleği) | `docs/` altında belge bulunamadı; §6.0 yalnız görev tanımındaki özete dayanır |
| v0.10.2318+ uçuş hâlindeki işler (döngü telemetrisi, bilinmeyen araçta yakın ad, geçit yeniden denemesi) | bu ağaçta yok; H1 "tur sayısı" ölçütü o telemetriden okunacak, E2'nin yeniden üretimi geçit yeniden denemesiyle üst üste binmemeli (tur başına en çok bir düzeltici çağrı) |

Föy düzeltmeleri (hepsi yukarıda yerinde işaretli):
1. Sabit istem çekirdeği 8.991 B ≈ 3.000 token; föy 9,6 KB ≈ 3.200 (`systemChat` 2.983 B, `ChatAnswerStyle` 996 B, `DataNotInstruction` 477 B).
2. `answer_chars` karakter değil bayt (`api/chat_exchange.go:563`).
3. `chat.go` satırları kaymış: açıklama `:442`, kapsam `:463`, hafıza `:481`, netleştirme `:494`, ön-getirme `:599`, bağlam yazımı `:408-412`, araç sonucu `:1503`, ortam notu `:1506`, konuşmaya ekleme `:1683`.
4. Kademe listesi eksikti: operasyon-neden `:643`, uç nokta kartı `:658`, doküman/varlık sorusu `:554`; ve "ancak ondan sonra serbest döngü" ifadesi `on_no_loop` kipinde yanlış — `none` araçsız genel cevapla biter.
5. "Koşucu v1 yolunu ölçüyor" bayat: v0.10.2106'dan beri v2 (`api/ai_evalset_agent.go:459`).
6. 160 rune tutma yalnız araç turunu değil araç sunulan her turu kapsar (final cevabın ilk 160 runesi gecikir, düşmez); guided anlatımında kapı yoktur.
7. v2 çeviricisi araç adımında `phase` alanını taşımaz (föyde yoktu).

Doğrulanıp aynen kalanlar: geçmiş yalnız metin ve araç sonuçları turlar arası düşer; `finalAnswerText` ve `answer` / `data-reset` sırası; `ungroundedNumbers`'ın tek çağıranı ve yalnız uyarı eklediği, birim dönüşümü ve sözcük sayı okumadığı; v2 araç sonucu tavanı 2.048 bayt; `cosre_interactions`'ta v1 / v2 kolonu olmadığı.
