# CoSRE yakınsama audit'i — sekiz bölümlü hedef model (Faz 0, 2026-11-28)

Taban: `04525ece2` (v0.10.2490 + sohbet deneyimi dilim 1; v0.10.2491 olarak yayında). Hedef model: Elastic Agent Builder
tarzı sekiz bölüm (araç kataloğu, döngü disiplini, bağlam bütçesi, skill, güven sınırı, kanıt disiplini,
boş sonuç, iletişim). Yalnız belge; kod değişmedi. `.claude/rules/` geçerli.

## Özet

Serbest döngü (`internal/api/copilot_chat.go`) hedefin "iskelet" kısmını büyük ölçüde taşıyor: adlandırılmış
durma koşulları, tavan turunda araçsız kısmi cevap, aynı çağrının tekrar muhafızı, sonuç başına bütçe ve
kırpmanın modele söylenmesi, tur öncesi bağlam bütçesi, insan onayına bağlı tek yazma aracı ve araç çıktısına
dayanma kuralı var. Eksik olan "ölçek" kısmı: model her turda 81–88 aracın tamamını görüyor (≈22–25 bin
token, ölçüm aşağıda), araç açıklamalarında "ne zaman / ne zaman değil" yok, aynı turdaki çağrılar sırayla
koşuyor, kırpılan sonuç geri okunamıyor, skill kavramı yok, güven düzeyi ve yüzey-sinyali / kök-neden ayrımı
yalnız tek-atış explain istemlerinde var. En büyük güvenlik bulgusu: dış MCP araçları onaysız ve yazma
yetkileri Coremetry'de denetlenmiyor. Önerilen karar: önce ölçüm ve açıklama dilimleri (davranışı değiştirmeyen
ya da ayarla kapalı), sonra döngü muhafızları ve saklanan sonuç, en son skill; her model-davranışı dilimi
ayarla varsayılan kapalı ve Qwen3.8 Flash + gemma4 eval'iyle açılır.

## Bugünkü durum

### Ölçümler (fikstür / test, prod yok)

| Ölçüm | Değer | Nasıl sayıldı |
|---|---|---|
| Modelin turda gördüğü yerli araç (v1, tam bağımlılık) | **81** | geçici test: `len(mcptools.ChatToolListFor(goldenConditionalDeps(), "full"))`; bağımlılıksız 76, `simple` 77 |
| v2 ek araçları | 8 (7 yeni + `search_logs` yerine geçer) → **≈88** | `len(V2OnlyTools(d))`; birleştirme `mcptools/v2_tools.go:63-83` |
| + onay aracı / dış MCP | +1 (`add_problem_note`, kapılıysa) / +N | `copilot_chat.go:1053`, `:996-1008` |
| Katalog baytı (81 araç) | açıklama 11.799 B + şema 55.554 B = **67.353 B ≈ 22 bin token** | `ChatDescription()` + `json.Marshal(InputSchema)` toplamı; 3 B/token = `chat_round_budget.go` kalibrasyon öncesi oranı |
| v2 ek katalog | +1.856 + 6.808 B (yerine geçen `search_logs` düşülmedi) → **≈76 KB ≈ 25 bin token** [V] | aynı yöntem |
| Döngü sistem istemi (statik çekirdek) | v2 çekirdek 6.306 B + `systemChat` 4.800 B = **11,1 KB ≈ 3,7 bin token** + koşullu önsözler | geçici test `len(SystemPromptChatAgentLoopV2For(false))`, `len(SystemPromptChat())`; birleşim `copilot_chat.go:1293-1309` |
| "ne zaman / değil" içeren araç açıklaması | 81'de 2 / 3 (v2'nin 8'inde 5 / 1) | `ChatDescription` küçük harfte `when\|ne zaman\|kullan` ve `not \|kullanma\|değil` alt dizesi (kaba, üst sınır) |
| Sonuç kırpma | var: 6.000 rune × kanıt ölçeği | `chat_tool_budget.go:46,72`; ölçek = pencere/32768, [1,8] `evidence_scale.go:13-16` |
| Bağlam penceresi | Qwen3.8 Flash Next **262.144** (max çıktı 131.072, varsayılan 32.768); gemma 27b/31b 131.072, diğer gemma 8.192; düz qwen3 32.768 | `ai/modelcaps/tuning.go:35-41`, `contextwindow.go:32-45`; sunucudan okunan pencere önceliklidir `chat_round_budget.go:19-21` |
| Döngü tavanları | taban 6 çağrı / 5 tur; ölçekli taban 8 çağrı; Derin tavanı 14 / 8 | `copilot_chat.go:63-68`, `chat_deep.go:37-39,66,84-99` |
| `sourcestate` kullanan mcptools dosyası | 14 / 94 | `grep -l "sourcestate\." internal/mcptools/*.go` (test hariç) |

Geçici ölçüm testleri koşturulup silindi; ağaçta kalmadı.

### Durum tablosu

| # | Hedef madde | Durum | Kanıt |
|---|---|---|---|
| 1.1 | Çekirdek araçlar hep açık, alan araçları skill ile | **kısmen** | tüm katalog her turda; daraltma yalnız kip bazlı: `simple` (`mcptools/tools.go:708-731`), trace takibi / kod turu (`copilot_chat.go:961-981`), kullanıcı yönlendirmesi (`chat_prepare_step.go:47-49,119-134`), operatör toolset kapatma (`mcptools/toolset.go:3-27`) |
| 1.2 | search / execute_query / ayrı generate_query + doğrulama / get_by_id / şema keşfi isteğe bağlı | **kısmen** | search: `traces_search`, `search_traces`, `search_logs`; get_by_id: `get_trace`, `get_problem`, `find_trace_by_request_id`; şema keşfi araçları (`describe_attributes`, `list_log_fields`, `list_metric_names/labels`, `get_capabilities`) VAR ama hep açık; genel SQL/PromQL yok (karar, `docs/cosre/investigation-design.md:7`) → generate_query yok; argüman şema doğrulaması sohbette `warn` (`chstore/ai_toolarg_settings.go:50-51`) |
| 1.3 | Yıkıcı araç işaretli | **var (yerli) / yok (dış)** | `mcp.Tool.Approval` (`mcp/mcp.go:289-294`); tek kullanıcı `add_problem_note` (`chat_approval_resume.go:45,92-107`); dış MCP aracı `MinRole: viewer`, işaretsiz (`chat_mcp_bridge.go:92-111`) |
| 1.4 | Açıklamada "ne zaman / ne zaman değil" | **yok** | ölçüm satırı; kullanım rehberi istemde toplu (`copilot/prompts.go:3208-3228`) |
| 2.1 | Bağımsız çağrılar tek turda, paralel yürütme | **kısmen** | istem ister (`prompts.go:2458`), model turu çok çağrı taşır; yürütme SIRALI (`copilot_chat.go:1645`, "executor senkron" `:1690-1691`); sağlayıcı gövdesinde `parallel_tool_calls` yok (`grep -ri parallel internal/ai/provider` → 0); yalnız sunucu kanıt paketi paralel (`investigation-design.md:7`, S4) |
| 2.2 | Tıkanma tespiti (aynı imza ≥2, art arda boş → ipucu) | **kısmen** | birebir (araç + kanonik argüman) tekrar yürütülmez + ipucu (`ai/agent/tools/executor.go:188,246-249`); yakın kopya ve art arda boş sonuç tespiti yok; durma koşulları yalnız tavan/iptal/token (`chat_stop.go:21-25`); `LastCalls` alanı var, okuyan koşul yok (`chat_stop.go:35`) |
| 2.3 | Tur tavanı + zarif kısmi özet | **var** | `defaultStop` (`chat_stop.go:110`); tavan turu araçsız + "toplayamadığını söyle" eki (`prompts.go:2525-2528`, `copilot_chat.go:1967-1986`); yineleme düşüşünde deterministik kanıt özeti (`copilot_chat.go:1495-1505`) |
| 2.4 | Araç çağrısı önceki sonuçtaki talimatla sürülmez | **kısmen** | yalnız istem çerçevesi `DataNotInstruction` (`prompts.go:2288-2294`); dış araç varken wiki kapalı (`copilot_chat.go:1015-1020`); kodda çağrı-köken denetimi yok |
| 3.1 | Sonuç başına token tahmini | **kısmen** | sonuç başına rune tavanı (`chat_tool_budget.go:72`); tur başına bayt→token tahmini (`chat_round_budget.go:21-30`); sonuç başına token kaydı yok (çipte yalnız `bytes`, `copilot_chat.go:1819`) |
| 3.2 | Eşik üstü → sakla, modele ref + özet | **yok** | kırpılan kısım modelden kalıcı düşer; tam çıktı yalnız doğrulayıcıda (`copilot_chat.go:1750`) |
| 3.3 | `read_stored_result(ref, offset, limit)` | **yok** | `grep -r read_stored_result internal` → 0 |
| 3.4 | Geçmiş büyüdükçe eski sonuçları özetle | **kısmen** | deterministik saplama + eski çift düşürme (`chat_round_budget.go:31-40,390,546`); taşma sonrası yarıya indirme (`chat_overflow.go:43`); model özeti yok |
| 3.5 | Kırpma modele açıkça söylenir | **var** | `chatToolResultTruncNote` (`chat_tool_budget.go:49-50`), saplamada "kısaltıldı" (`chat_round_budget.go:34-36`) |
| 4.1 | Skill = {ad, açıklama, açılan araçlar, talimat, kaynak} | **yok** | en yakınlar: toolset (kaynak bazlı), kip katalogları, operatör kuralları (`ai/cosrerules/cosrerules.go:3-8`) |
| 4.2 | Varsayılan yalnız çekirdek araçlar | **yok** | 1.1 |
| 4.3 | Soru uyunca skill yüklenir | **yok** | daraltma kancası hazır (`chat_prepare_step.go:47-49`), tetikleyen sınıflandırıcı yok |
| 4.4 | Skill içeriği güvenilmez, kapsam dışı yan etki yetkilendiremez | **kısmen** (kurallar için) | kurallar çitli veri, araç/argüman/güvenliği değiştiremez (`cosrerules.go:5-8`, `prompts.go:2765`) |
| 5.1 | Güvenilen (kullanıcı + sistem) / güvenilmeyen ayrımı kodda | **kısmen** | ayrım istem metninde (`prompts.go:2288-2294`), kodda tür/etiket yok |
| 5.2 | Güvenilmeyen içerik sarılı/etiketli | **kısmen** | kurallar, hafıza, önceki satırlar çitli blok (`cosrerules.go:367`, `cosrememory/cosrememory.go:332`, `chat_state.go:680`); araç sonucu ham JSON (`copilot_chat.go:1775,1907`); ekran/sayfa bağlamı sistem istemine önsöz olarak girer (`copilot_chat.go:1294-1296`) |
| 5.3 | Yıkıcı çağrı → pending_confirmation + onay | **var (yerli) / yok (dış)** | `KindApproval` (`executor.go:203`), jeton (`copilot_chat.go:1716-1721,1926-1931`); yalnız v2 + `ai.toolApproval` + rol ≥ editor (`chat_approval_resume.go:6-9,132-142`); dış MCP: "yazma yetkisi Coremetry'de denetlenmez" (`copilot_chat.go:994`) |
| 5.4 | Alt ajan açık yetkiyle | **kısmen** | profil ayrımında anlatıcı araç görmez (`prompts.go:2533-2538`); araştırma profili aynı kataloğu görür (`copilot_chat.go:949`); açık yetki modeli yok |
| 6.1 | Her iddia araç çıktısına dayanır | **var** | `prompts.go:2470-2473,3190`, son kontrol `:2505-2509`; künye (`copilot_chat.go:1598-1616`); doğrulayıcı gölgede (`copilot_chat.go:1610`) |
| 6.2 | Yüzey sinyali ≠ kök neden (iş kuralı reddi) | **kısmen** | trace explain istemlerinde var (`prompts.go:151,250,259,290`); döngü isteminde yok |
| 6.3 | Güven düzeyi Kritik/Yüksek/Orta/Spekülatif + çoklu hipotez | **yok** | tek cümle "emin değilsen güven seviyeni belirt" (`prompts.go:2498`) |
| 6.4 | İddia kanıt derinliğiyle sınırlı | **kısmen** | explain: "çağrının SONUCUDUR, kök neden değil" (`prompts.go:151`); döngü: "likely_source yoksa kaynak ADLANDIRMA" (`prompts.go:3223-3224`), "telemetri yok ≠ workload yok" (`:3253`) |
| 7.1 | Boş sonuç olası nedenleriyle bulgu | **kısmen** | `sourcestate.Empty` notu (`sourcestate/sourcestate.go:283`) 14/94 dosyada; istem (`prompts.go:2464-2465,3251`); neden listesi (pencere, süzgeç, örnekleme, kaynak) zorunlu değil |
| 8.1 | İlk çağrıdan önce kısa niyet cümlesi | **kısmen** | cümleyi SUNUCU yazar, deterministik evre havuzu (`chat_narration.go:12-30`, `chat_status_line.go:1-25`); modelin önsözü 160 rune kapısında tutulur (`ai/provider/tools_stream_gate.go:47`); istem "planı yazma", "ne yaptığını anlatma" (`prompts.go:2457,2488-2489`) |
| 8.2 | Önemli bulgu / yön değişiminde ara güncelleme | **yok** | durum cümlesi evre bazlı, bulgu bazlı değil (`chat_status_line.go`) |
| 8.3 | Final cevap yorumlar, kanıtla | **var / ölçülmedi** | "doğrudan bulguyla başla" (`prompts.go:2488`), kanıt çipleri (`copilot_chat.go:1881-1889`); "tekrar değil yorum" ölçülmedi |

**Yazan araçlar (tam liste):** `add_problem_note` (problem notu; onaylı, `chat_approval_resume.go:92-107`);
`set_context` / `clear_context` (yalnız sunucudaki konuşma durumu; v2 durum açıkken katalogdan düşer,
`copilot_chat.go:969`, `mcptools/context_tools.go:33`); **dış MCP araçları** (yan etkisi bilinmiyor). Diğer
yerli mcptools araçlarında yazma çağrısı yok (`grep -l "\.Insert\|\.Save\|Upsert\|\.Delete\|\.Exec(" internal/mcptools/*.go` test hariç → 0).
Problem notu aksiyon kartı araç değil, kullanıcı tıklaması (`copilot_chat.go:1787-1791`).

### Riskler

1. **Katalog maliyeti.** ≈22–25 bin token her turda yeniden gider (tur bütçesi ve önek önbelleği bunu
   hafifletir, `chat_round_budget.go:28-30`); küçük modelde araç seçimi doğruluğu katalog büyüklüğüyle düşer [V].
2. **Dış MCP yan etkisi.** Onaysız, viewer rolüyle, model tarafından sürülen çağrı; enjekte talimatın eyleme
   dönüşebildiği tek yol. Wiki kapatma tek önlem.
3. **İstem/kod tavan çelişkisi.** İstem "bir turda en çok 6 tool çağrısı" (`prompts.go:3248`); kod tabanı 8,
   Derin 14 (`chat_deep.go:39,66`). Model bütçenin altında durabilir.
4. **Geri dönüşsüz kırpma.** Sayım soruları kırpılmış listeyle cevaplanırsa not uyarıyor ama veri yok.
5. **Sıralı yürütme gecikmesi.** 3–6 bağımsız CH/ES okuması toplam süreyi toplar; paralelleştirme `emit`'in
   tek goroutine kuralını korumalı (`copilot_chat.go:1690-1691`).
6. **Aşırı iddia.** Döngü isteminde güven kademesi ve kök-neden/yüzey ayrımı olmadığından "%100 hata" gibi bir
   sinyal kök neden diye sunulabilir.

## Seçenekler ve önerilen karar

| Alan | Önerilen (ilk) | Alternatif | Maliyet / risk |
|---|---|---|---|
| Katalog | **A:** açıklamalara "ne zaman / değil" (ayrı alan, ayarla seçilir), sonra çekirdek + skill daraltması | B: yalnız açıklama; C: yalnız daraltma | A: iki dilim; açıklamalar ≈ +2–4 KB katalog [V]; daraltma yanlış skill'de araç eksikliği riski → gölge ölçüm |
| Paralel | **A:** salt-okur yerli araçları sınırlı paralel (≤4) koştur, olayları çağrı sırasıyla yayınla | B: sıralı kal | A: orta; CH yükü artar (prod ölçeği ≈27k span/s, `memory prod-scale`) |
| Tıkanma | **A:** yakın-kopya (aynı araç, ≥2 kez, aynı boş/aynı sonuç) ve art arda 2 boş → modele ipucu sonucu | B: durdur (stopCond) | A ucuz; B cevapsız kalma riski |
| Saklanan sonuç | **A:** alışveriş ömürlü bellek içi depo + `read_stored_result`; kırpma notuna ref | B: Redis, konuşma ömürlü | A yeni şema yok; B takip turlarında da okunur |
| Güven sınırı | **A:** araç sonucu etiketli çite; dış MCP salt-okur değilse onaya | B: yalnız çit; C: sohbette dış MCP kapalı | güvenlik → operatör kararı (K5) |
| Kanıt istemi | **A:** döngü istemine güven kademesi + yüzey/kök neden + derinlik + boş sonuç nedenleri (tek ek, ayarlı) | B: yalnız Derin kipte | istem +≈600 B [V]; küçük modelde talimat takibi düşebilir → eval |
| İletişim | **A:** dilim 1 (sunucu anlatımı, inline) kalır; bulgu bazlı ara cümle sunucudan | B: model niyet cümlesi | B: önsöz kapısıyla çelişir, model metni denetlenemez |

## Kapsam dışı

Eval setlerinin içeriği ve puanlama (`docs/cosre/eval`), guided/tek-atış explain yüzeyleri (yalnız karşılaştırma
için anıldı), arka plan AI SRE döngüsü (`internal/sre`), UI çizimi, prod ölçümleri (prod yok), oy/geri bildirim
(oylar hiçbir dilimde çalışma zamanı kararı ya da eval etiketi olmaz — kural), `copilot_guided.go` (4721 satır,
okunmadı). Hedef modelin "kullanıcı ara adımları görmez" maddesi uygulanmaz; K8'e bakınız.

## Dilim planı

Her dilim tek sürüm, tek commit. Model davranışını değiştiren her dilim `cosre_conversation` blobunda (ya da
`ai.*` ayarında) yeni alanla **varsayılan kapalı**; açmadan önce Qwen3.8 Flash ve gemma4 ile eval (iki model
de; tek-model kod yolu yok).

| # | Kapsam | Giriş ölçütü | Çıkış ölçütü | Koruma | Risk |
|---|---|---|---|---|---|
| C1 | Ölçüm: tur başına katalog baytı / araç sayısı / istem baytı `ai_calls` + span özniteliği; katalog boyu ratchet testi (bu belgedeki sayım tanımı) | yok | 81/88 ve bayt değerleri testte pinli; davranış bayt aynı | ratchet testi (yalnız aşağı) | düşük |
| C2 | İstem tavan cümlesi gerçek bütçeden (Risk 3) | K10 | istemdeki sayı = `maxCalls`; Qwen/gemma eval | istem altın testi | düşük |
| C3 | Araç açıklamalarına "ne zaman / değil" (ayrı alan, ayar `toolDescriptions=v2`, kapalı) | K1 | 81 aracın tamamında iki cümle; yapısal test boş bırakmaz; eval araç seçimi ≥ bugünkü | mcptools yapısal test | orta (katalog büyür) |
| C4 | Tıkanma ipucu: yakın kopya + art arda boş (ayarlı, kapalı) | K3 | sahte araçla tablo testi; ipucu sonucu modele, durma yok | executor testi | düşük |
| C5 | Saklanan sonuç + `read_stored_result` (ayarlı, kapalı) | K4 | kırpılan sonuç ref taşır; offset/limit okuma; alışveriş sonunda silinir | executor + bütçe testi | orta |
| C6 | Salt-okur yerli araçların sınırlı paralel yürütmesi (ayarlı, kapalı) | K2 | olay sırası = çağrı sırası; süre ölçümü; iptal yolu aynı | döngü testi (sıra pin) | orta (CH yükü) |
| C7 | Güven sınırı: araç sonucu çiti + dış MCP onayı | K5, K6 | çit kapalıyken bayt aynı; dış araç kararına göre onay | prompt_injection testi genişler | orta-yüksek (güvenlik) |
| C8 | Kanıt istemi eki: güven kademesi, yüzey/kök neden, derinlik, boş sonuç nedenleri, bulgu bazlı ara cümle (ayarlı, kapalı) | K7, K11 | eval'de aşırı iddia oranı düşer, cevap boyu sınırda | istem altın testi | orta |
| C9 | Skill kaydı (Go içinde, şema yok): {ad, açıklama, araçlar, talimat, kaynak}; varsayılan çekirdek katalog; `narrowSpecs` ile açma; önce gölge (seçimi kaydet, uygulama) | C1, C3, K1 | gölgede skill isabeti ölçülür; açıkken katalog ≤ ~25 araç [V] | skill tablo testi + eval | yüksek (araç eksikliği) |

Sıra: C1 → C2 → (C3, C4, C5, C6 bağımsız) → C7 → C8 → C9. C9, C3'e bağlı (açıklamalar skill seçimini besler).

## Operatör kararları

1. **K1 — Katalog yönü.** (a) önce açıklamalar (C3), sonra skill daraltması gölgede (C9) · (b) yalnız
   açıklamalar · (c) yalnız daraltma. **Öneri: (a).** Bekleyen: C3, C9.
2. **K2 — Paralel yürütme.** (a) salt-okur yerli araçlar ≤4 eşzamanlı, ayarla · (b) sıralı kal. **Öneri: (a).**
   Bekleyen: C6.
3. **K3 — Tıkanma tepkisi.** (a) ipucu sonucu, döngü sürer · (b) durdur ve kısmi özet. **Öneri: (a).** Bekleyen: C4.
4. **K4 — Saklanan sonuç ömrü.** (a) alışveriş ömürlü bellek · (b) konuşma ömürlü Redis. **Öneri: (a).** Bekleyen: C5.
5. **K5 — Dış MCP araçları (güvenlik).** (a) MCP `readOnlyHint` ya da operatör salt-okur listesi dışındaki her dış
   araç onaya · (b) bugünkü gibi · (c) sohbette dış MCP kapalı. **Öneri: (a).** Bekleyen: C7.
6. **K6 — Araç sonucu çiti.** (a) etiketli çit, ayarla kapalı başlar · (b) yalnız istem çerçevesi kalsın.
   **Öneri: (a).** Bekleyen: C7.
7. **K7 — Güven kademeleri.** (a) Kritik / Yüksek / Orta / Spekülatif, en az iki hipotez yalnız belirsizlikte ·
   (b) üç kademe (yüksek/orta/düşük, `prompts.go:1086` ile aynı) · (c) yok. **Öneri: (b)** — repoda mevcut
   sözlük; dört kademe istenirse (a). Bekleyen: C8.
8. **K8 — Ara adım görünürlüğü (çelişki).** Hedef model "kullanıcı ara araç adımlarını görmez" diyor; dilim 1
   (operatör onaylı, DECISIONS 2026-11-28 v0.10.2491) adımları cevabın içinde gösteriyor (`flow=inline`
   varsayılan, `chstore/cosre_conversation_settings.go:83,242`). (a) dilim 1 kalır, hedefin bu maddesi
   uygulanmaz · (b) `flow=block`'a dön. **Öneri: (a)** — eski karar korunur. Bekleyen: C8 (ara cümle biçimi).
9. **K9 — Model gerçekleri.** Kaynak metin "Qwen3-27B" varsayıyor; repoda varsayılan Qwen3.8 Flash (262.144
   pencere), ikincil gemma4 (27b/31b 131.072, diğerleri 8.192; ad tablosu `contextwindow.go:36-41`). Prod pencere
   geçitten okunuyor mu (`Source == "server"`, `chat_round_budget.go:43-45`) doğrulanmadı. (a) tüm dilimler iki
   modelle eval, pencereyi prod geçitten teyit et · (b) yalnız Qwen eval. **Öneri: (a)** (tek-model kod yolu yasak).
10. **K10 — İstem tavan cümlesi.** (a) istemdeki "en çok 6" gerçek bütçeyle değişsin · (b) sabit kalsın.
    **Öneri: (a).** Bekleyen: C2.
11. **K11 — Boş sonuç sözleşmesi.** (a) kalan 80 mcptools dosyasının boş dönüşleri `sourcestate.Empty` + olası
    neden alanına geçsin (araç çıktısı değişir → eval) · (b) yalnız istem kuralı. **Öneri: (a), C8'den sonra
    ayrı dilim** (bu planda numarasız; onaylanırsa eklenir).

## Ek — ikinci kaynak metnin "Adım 0" soruları (2026-11-28)

Operatör aynı gün ikinci bir metin verdi (iki model profili, niyet yönlendirme, üç davranış kuralı, bağlam
bütçesi, döngü tespiti, güven sınırı, eval vakaları). Dokuz ön analiz sorusunun cevabı:

| # | Soru | Bulgu |
|---|---|---|
| 1 | Guided intent'ler nerede | `internal/api/copilot_guided.go:59` `guidedIntent` sabit tablosu (≈35 niyet), saf ve tablo testli yönlendirici; rota `ai_routes.go:68`. Dosya 4721 satır, bütün okunmadı |
| 2 | Araç sonucu modele hangi biçimde | ham JSON araç mesajı, sarmalayıcı/etiket yok (`copilot_chat.go:1775,1907`); kırpma notu eklenir; kurallar/hafıza çitli (5.2) |
| 3 | Boş sonuç | `sourcestate.Empty` notu 14/94 araç dosyasında + istem kuralı; neden listesi zorunlu değil (7.1) |
| 4 | CLAUDE.md kısıtları | tek binary; `api.go` büyümez, yeni rota kendi dosyasında, AI rotaları `requireCopilot` altında; ayar `system_settings` blobu; admin yazımı = audit kaydı; sır geri gösterilmez; PII maskeleme yok. Ek kalıcı kurallar: prod LLM yalnız yerel, tek-model kod yolu yok, oylar çalışma zamanı kararı olmaz |
| 5 | `.claude/skills/` | 23 skill, hepsi GELİŞTİRME (Claude Code) skill'i: aiops, api-route, audit, bugfix, clickhouse-schema, copilot-surface, dilim, dogrula, mcp-tools, mockup, release, spec, tdd… CoSRE çalışma zamanında skill kavramı yok (4.1) |
| 6 | Model profilleri | VAR: `copilot/profiles.go:50` `ModelProfile` (en çok 20, rol kapısı `:110`), tek `system_settings` blobu (`:44`); yüzey→profil haritası `SurfaceProfiles()` `:430`, gruplar intent / background / research (`:786-791`); araştırma / anlatım profil ayrımı `api/chat_profile_split.go` (v0.10.2365): araçlı turlar araştırma profilinde, tek akan anlatım sohbet profilinde. Profil kod değil VERİ: Ayarlar › AI profilleri |
| 7 | vLLM tool-call parser | repoda YOK (`tool-call-parser`, `enable-auto-tool-choice` yalnız `docs/audit/cosre-agent-session1.md`'de geçer; `charts/` içinde vLLM yok). Model sunucusu repo dışı. Sağlayıcı vLLM tuhaflıklarını karşılar (`ai/provider/stream.go:47,178`); istek gövdesinde `parallel_tool_calls` yok (2.1) |
| 8 | Hipotez kartı | `internal/sre/hypotheses.go:92` `Hypothesis` {kind, category, label, score, band, prior, checks, humanState, state, stateRefs, stateReason}; bant üç kademe high / medium / low (`BandOf` `:230`); skor DETERMİNİSTİK (`Score` `:242`, 8 hipotez), model üretmez |
| 9 | Eval düzeneği | `internal/copilot/evalset/*.json` (18 set: agent_loop, intent, rca_verdict…) + `conversation/fluency.json` (22 konuşma, 55 tur; sahte veri, gerçek model, YAPISAL beklenti, kelime eşleşmesi yok — `docs/cosre/eval/README.md`); SRE eval `internal/sre/eval.go`; koşu API'si `api/ai_evalset_runs.go`; rubrik `internal/ai/evalrubric` |

İkinci metnin bu belgeyle örtüşen adımları: Adım 3-B → K11, Adım 4 → C5/K4, Adım 5 → C4/K3, Adım 6 → C7/K5-K6,
Adım 3-C → C8/K7. Örtüşmeyenler ek karar olarak aşağıda. `localOnly` kapısı mevcut (`copilot/stream.go:65`,
`ErrClassLocalOnly` `observe_meta.go:193-195`, kod/wiki içeriği) ve hiçbir dilimde değişmez.

### Ek operatör kararları

12. **K12 — İki profil ve model adları.** Metin `qwen3-8b-flash-fp8` / `qwen3-235b-a22b-fp8` ve bir API geçidi
    varsayıyor; kayıtlarımızda varsayılan Qwen3.8 Flash, ikincil gemma4. Profil ve yüzey eşlemesi zaten veri.
    (a) kod değişmez: operatör iki profili Ayarlar'da tanımlar, `chat-research` yüzeyini büyük modele eşler; adlar
    ve pencereler prod geçitten doğrulanır · (b) profiller koda/varsayılana gömülür. **Öneri: (a)** (tek-model kod
    yolu yasağı). Metindeki `curl` SSE yoklamasını operatör prod ağında koşar; buradan prod LLM'e erişim yok.
13. **K13 — Soruya göre profil seçimi.** Bugün ayrım adım türüne göre (araçlı tur / anlatım) ve Derin kipe göre;
    soru metnine göre değil. (a) mevcut ayrım + Derin kip kalır, kelime tabanlı sınıflandırıcı önce GÖLGEDE
    kaydedilir (hangi profili seçerdi) · (b) kelime sınıflandırıcı doğrudan yönlendirir. **Öneri: (a).**
14. **K14 — Kural A (yüzey sinyali doğrulama).** (a) hata sinyali olan ama hata mesajı kanıtı okunmamış turda
    modele ipucu sonucu ("mesajı oku": trace / exception / log), sınıflandırmayı model kanıt göstererek yapar;
    metindeki kelime listeleri yalnız gölgede etiket · (b) kelime listeleri kodda kesin sınıflandırır. **Öneri:
    (a)** — "not found", "unauthorized", "rate limit" gerçek arıza da olabilir. Her iki seçenekte kapsam yalnız
    sohbet cevabı; Problem kaydı, öncelik ve bildirim hattına dokunmaz. Tetik eşiği (%10 hata) ayar olur.
15. **K15 — Saklanan sonucun yeri.** Metin `/tmp/coremetry-ctx/` diyor; çok kopyalı kurulumda `/tmp` paylaşılmaz.
    **Öneri: K4 (a)** bellek içi, alışveriş ömürlü; konuşma ömrü istenirse Redis (K4 b). Dosya sistemi yok.
16. **K16 — Yeni eval vakaları.** Üç senaryo (iş kuralı reddi, boş sonuç, döngü) eklenir. (a) beklenti yapısal
    (hangi araç okunmalı, cevapta sahte veride olmayan iddia var mı, döngü ipucu çıktı mı) · (b) metindeki gibi
    "şu kelimeyi içermeli". **Öneri: (a)** — mevcut düzeneğin ilkesi. Senaryolar nötr adlarla yazılır.
17. **K17 — Tur tavanı.** Metin 40 turu tıkanma sayıyor; kod tavanı 5 tur / 8 çağrı, Derin 8 / 14 (Ölçümler).
    **Öneri:** tavanlar değişmez; tıkanma ipucu (C4) tavandan önce devreye girer.
18. **K18 — Model sunucusu bayrakları.** Tek turda çoklu araç çağrısı için sunucu ayarı repo dışında. (a) operatör
    ya da altyapı ekibi prod sunucuda doğrular, sonuç C6'nın giriş ölçütü olur · (b) doğrulanmadan C6. **Öneri: (a).**

## Bayat satırlar

- `docs/cosre/agent-core-ideas.md` §1.3 yan etki envanteri `add_problem_note` öncesi yazıldı [V, satır
  doğrulanmadı] → güncel liste bu belgenin "Yazan araçlar" paragrafı.
- `internal/mcptools/toolset.go:3` "74 aracı" der → bugün `ToolList` 82 (ölçüm: `len(ToolList(d))`).
