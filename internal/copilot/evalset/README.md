# CoSRE evalset — donmuş replay vakaları (v0.10.422, CoSRE denetimi E1/E7)

Altın testler (`prompt_golden_test.go`, `prompt_antifabrication_test.go`)
PROMPT METNİNİ pinler; modelin ÇIKTISINI hiçbir şey ölçmüyordu. Bu klasör
o boşluğu kapatır: her vaka donmuş bir kullanıcı promptu + beklenen
DAVRANIŞ (must-contain / must-not-contain / uydurma ad sayısı / niyet).

- Koşum **CI dışı**, yerel modele karşı, yapı etiketiyle:

  ```
  COREMETRY_EVAL_BASE_URL=http://localhost:11434/v1 \
  COREMETRY_EVAL_MODEL=qwen3:8b \
  go test -tags evalset ./internal/api/ -run TestEvalsetReplay -v
  ```

  `COREMETRY_EVAL_PROVIDER` (varsayılan `openai` — Ollama/vLLM/LM Studio
  uyumlu) ve `COREMETRY_EVAL_API_KEY` (isteğe bağlı). Üretim
  `COREMETRY_AI_*` değişkenleri BİLİNÇLİ okunmaz: evalset bir geliştiricinin
  gerçek anahtarına asla ateşlenmez. Kayıt yazılmaz (recorder bellek içi;
  `ai_calls` satırı yok).

- **Panel (v0.10.940):** Settings › AI › Değerlendirme aynı vakaları
  SUNUCUDA koşar — üretimin yapılandırılmış profil(ler)iyle, onay adımı
  yok. Her yüzey üretimde hangi profile gidiyorsa oraya gider
  (`copilot.SurfaceProfileID(<üretim etiketi>)` + `WithProfile`; tablo
  `internal/api/ai_evalset_core.go` `evalProductionLabels`). Çağrılar
  `ai_calls`'a `surface = "evalset-<Yüzey>"` ile yazılır: /ai'da
  "Kaynak: Değerlendirme" ayrı görünür, üretim sayılarına karışmaz.
  Koşular `ai_eval_runs` tablosunda (son 20 listelenir, 180 gün TTL);
  iki bitmiş koşu `evalrubric.Diff` ile kıyaslanır (`cmd/evalsetdiff`
  ile aynı semantik). Uçlar: `internal/api/ai_evalset_runs.go` başlığı.

- **Gömülü küme (v0.10.940):** `*.json` binary'ye gömülür
  (`embed.go`, `evalset.FS`) — çalışma imajında bu dizin yok. Yeni vaka
  eklemek = JSON'u buraya koymak; panel ve CLI bir sonraki derlemede
  görür. `*.md` gömülmez (.dockerignore da dışlar).

- CLI ile panel TEK vaka yolunu paylaşır (`runEvalsetCase`). Fark yalnız
  çağrıda: CLI özel bir Service + sıcaklık 0 + bellek içi kayıt; panel
  üretim profilinin sıcaklığı + gerçek `ai_calls` satırı. JSON yüzeyleri
  (IntentClassify, NLToQuery, CHQueryOptimize, RCAVerdict) ikisinde de
  üretimin şemasıyla çağrılır. Bilinmeyen yüzey / bozuk hipotez koşuyu
  düşürmez: `fixture: …` ihlaliyle kırmızı vaka olur.

- Fikstür doğrulaması etiketsiz koşar (`TestEvalsetFixturesValid`, GÖMÜLÜ
  kümeyi okur — gemiye bineni doğrular):
  şema `coremetry.evalset/1`, benzersiz id, çözülebilir surface, `why`
  zorunlu, en az bir beklenti. Yazım hatası kırmızı test, sessiz atlama değil.

- Skor satırı: `id surface ok latency_ms unknown_entities fails`; altbilgi
  `prompt_version=<FNV> model=<ad> n= pass= fail=`. `prompt_version`
  değişince eski skor kıyaslanamaz — altbilgi olmadan yeşil koşum hiçbir
  şey söylemez.

## Vaka şeması

```json
{
  "schema": "coremetry.evalset/1",
  "cases": [{
    "id": "problem-evidence-boundary",
    "surface": "Problem",              // copilot.SystemPromptX adı
    "why": "kırmızıya dönünce okunacak cümle",
    "user": "…donmuş kullanıcı promptu…",
    "expect": {
      "mustContain": ["2.14.0"],       // büyük/küçük harf duyarsız
      "mustNotContain": ["kubectl"],
      "knownEntities": ["checkout"],   // rca.CountUnknownEntities bilinen kümesi
      "maxUnknownEntities": 0,         // cevapta gösterilmemiş servis-biçimli ad tavanı
      "intent": "service_health",      // yalnız IntentClassify
      "intentService": "checkout",
      "maxLatencyMs": 30000            // METRİK: aşım uyarı, kırmızı değil (soğuk model)
    }
  }]
}
```

RCA hakemi vakaları (v0.10.424): `surface: "RCAVerdict"` + `user` yerine
`hypothesis` (chstore.RootCauseHypothesis JSON); prompt, katalog, rakipler ve
şema canlı yolla aynı kodla üretilir. Beklenti: `verdicts` (kabul kümesi),
`minEvidenceCitationRate` (K2'den geçen atıf / toplam atıf),
`maxUnknownEntities` (K3). Kalkan raporu (rcaShieldReport) skor satırıdır.

Prompt denetimi R3 önce/sonra vakaları (P-L15): `slo_burn.json` (P-F3), `ch_query_optimize.json` (P-F2), `slow_query.json` (P-F12); `user` metinleri canlı kurucularla aynı (copilot_explain_slo.go, insight.SlowQueryPromptUser, copilot_ch_optimize.go).

Vaka kaynağı: elle yazılmış sentetik (demo sözlüğü: checkout, payments,
inventory). Prod 👎 satırları müşteri adı taşıyabilir ve repoya GİRMEZ;
`GET /api/ai/evalset/export` (E5) indirilen dosya repoya elle ve
temizlenerek alınır.

## Ajan kipi (v0.10.1316)

`agent_loop.json`: `surface: "ChatLoop"` + `"mode": "agent"` (isteğe bağlı
`"deep": true`). Vaka tek çağrı DEĞİL: sunucu canlı sohbet handler'ını
(`copilotChat`) süreç içinde, gerçek araç kataloğu, Executor ve tur/çağrı
tavanlarıyla koşar (`internal/api/ai_evalset_agent.go`). Döngü öncesi
kademeler (guided, wiki, RAG …) açıktır; kademe önemliyse `expectTier`
sabitler. **Niyet kademesi (LLM sınıflandırıcısı + `on_no_loop` genel
cevabı) ajan vakasında KAPALIDIR (v0.10.1321, operatör):** prod varsayılanı
`on_no_loop`'ta guided'ın eşlemediği düz soru orada biter ve araç döngüsü
hiç ölçülmezdi. Yalnız `"expectTier": "intent"` sabitleyen vaka kademeyi
açık tutar (`evalAgentSkipIntent`). CLI koşumunda (`-tags evalset`) ajan
vakaları ATLANIR.

Ek beklentiler (yalnız ajan kipi): `expectedTools` (her biri en az bir kez
başarıyla — tek çağrılı kipte bilgi amaçlı), `anyOfTools` (en az biri),
`forbiddenTools` (hiç denenmez), `maxToolCalls` (yürütülen çağrı tavanı),
`expectTier` (aisurface kademesi), `maxTokens` (METRİK, kaldırmaz). Araç
adları sohbet kataloğundan ya da guided ön-yükleme çiplerinden olmalı
(`TestEvalsetFixturesValid`). Sonuç izi (`agent`: kademe, tur, çağrı,
token, ≤12 adım) koşunun cases JSON'unda; ai_calls satırları
`evalset-ChatLoop` (döngü öncesi kademeler de `evalset-` önekli).

## Prod koşusu düzeltmeleri (v0.10.1476)

- **Döngü sondası** (`"expectTier": "loop"`, ajan kipi): guided, doküman RAG'ı
  ve niyet kademesi atlanır (`chatEvalMode.LoopProbe`) — vaka modelin serbest
  döngüdeki ARAÇ SEÇİMİNİ ölçer. Prod bu soruları deterministik guided
  rotasına gönderir; o yarı CI'da `internal/api/chat_routing_regression_test.go`.
  Sondanın `anyOfTools` / `expectedTools` adları sohbet DÖNGÜSÜ kataloğunda
  olmalı (guided-yalnız çip adı model tarafından seçilemez; fikstür testi).
  Beklenen araç bu kurulumda sunulmuyorsa (kapalı toolset, rol) vaka
  `harness: …` nedeniyle atlanır — model hatası sayılmaz.
- **`bindService`** (ajan kipi): sorudaki sentetik servis canlı katalogda
  yoksa koşu, son 1 saatin en yoğun canlı servisine bağlanır (koşu başına bir
  MV okuması); soru, `mustContain`, `keyFacts`, `knownEntities` aynı adla
  değişir, iz `bound` alanında. Hedef yoksa vaka atlanır.
- **Altyapı hatası**: sağlayıcı 5xx / zaman aşımı / erişilemez / kota ile
  cevapsız biten vaka PUANLANMAZ (`skipped` + `infraError`, neden
  "altyapı hatası (…)"); yeniden deneme yok. 4xx / bağlam taşması kalite ya da
  harness hatası olarak kalır.
- **Yeni beklentiler**: `intentAnyOf` (intent'e ek kabul edilen niyetler,
  gerekçe `note`'ta zorunlu), `maxAnswerRunes` (cevap uzunluk tavanı).
  `note`: beklenti değişikliğinin yazılı gerekçesi (koşucu okumaz).
- **`OffTopicGeneral`** yüzeyi: v0.10.1458 genel soru istemi
  (`chat-offtopic-general`).
- **Sunucu bileşimi** (`evalServerCompose`): üretim handler'ının cevaba
  deterministik eklediği metin puanlamadan önce AYNI fonksiyonla uygulanır
  (SLOBurn: `sloAnswerWithBurnVerdict` alarm eşiği satırı).
- Uydurma ad ihlali artık adları da yazar (`unknown entities 1 > 0: [x]`).

## Trace anlatım biçimi (v0.10.2482)

`trace_narrative.json`: `surface: "TraceNarrative"` (anlatım istemi, üretim
etiketi `explain-trace`, düşünme/tavan üretimle aynı). Hakem modelsiz
`expect.narrative` (`internal/api/ai_evalset_narrative.go`):
`label` (kapalı kümeden, cevabın TÜR'ü bununla aynı olmalı), `quotes`
(cevapta harf duyarlı birebir; kanıtta da geçmeli), `minSentences` /
`maxSentences` (KARAR, varsayılan 3–6), `maxCodeTokens` (KARAR + Sonraki
adım'da kod biçimli sözcük tavanı, varsayılan 6; alıntılar sayılmaz), Sonraki
adım zorunlu. Bölümlü `Trace` yüzeyi ve vakaları değişmedi.
