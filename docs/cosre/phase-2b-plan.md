# CoSRE v2 Faz 2b — v2 sohbet ekranı, `cosre_v2` admin bayrağı arkasında (plan, 2026-10-30)

Durum: **PLAN — uygulanmadı, commit yok.** Operatör kararı 2026-10-30: 2b şimdi başlar,
prep heartbeat testi beklenmez; bayrak kapalı → kimse görmez, açık → v2 sohbet görünür.
Her dilim kendi `v0.10.X`'i; ilk görünür dilimden (B1) sonra operatör durağı (§6).
Bütün bölümler dolu; §7'deki sorular cevaplanmadan B1 kesilmez.

## 0. Bağlam ve bağlayıcı girdiler

**Okunan girdiler (file:line doğrulandı).**
- `docs/cosre/chat-ui-audit.md` §0 (1 ve 3 numaralı kararlar 2026-10-29'da global shadcn
  geçişiyle GEÇERSİZ: v2 `components/shadcn` + `components/ai-elements` üstüne; 2/4/5/6/7
  geçerli), §3.2 tel eşlemesi, §4 kanıt kartı aileleri, §6 F1–F9 (shadcn kararından ÖNCE
  yazıldı — §6'da B dilimlerine çevrildi).
- Scratchpad `cosre-phase2-musts.md` — **BAĞLAYICI**: reset haritası transport'u saran tek
  TransformStream'de (`text-start id → sıra`); `onData` yalnız `messageId → {sıra}` kümesine
  yazar; render akışta gizler; `onFinish` (normal/error/closed/stop hepsinde tetiklenir) kalıcı
  siler, `onError` da koşar, idempotent; kayıt `finish` SONRASI ve `chat.messages`'tan
  (onFinish `message` argümanı eski kopya); sunucu `data-reset`i `transient:true` gönderir;
  çift reset fixture'ı var. Hepsi 2a'da gemide (v0.10.1618) — 2b bunları DEĞİŞTİRMEZ, yalnız
  tüketir.
- 2a çekirdeği `frontend/src/lib/cosre/`: `useCosreChat.ts` (Chat + DefaultChatTransport
  → `POST /api/cosre/v2/chat`; `visibleParts`; `onPersist(ChatMessage[])` kancası),
  `textIdMap.ts`, `resetReducer.ts` (`CosreUIMessage = UIMessage<CosreMessageMetadata>`),
  `cosrePersist.ts` (`toPersistMessages`, `toRequestMessages`, `STOPPED_NOTE`, cap 40),
  `protocol.ts` (kapalı küme: 15 parça türü + 22 `data-*` adı), `uiStreamFixture.ts`,
  `aiSdkPin.test.ts` (ai 5.0.276 / @ai-sdk/react 2.0.279 tam pin; **"src/lib/cosre dışında
  `ai`/`@ai-sdk`/çekirdek importu yok"** — 2b bunu tembel pine çevirir, §1.3),
  `useCosreChat.test.tsx` (gerçek SDK + sahte `fetch` + paylaşılan fixture — 2b'nin
  parça→bileşen testi aynı sürücüyü kullanır, §5).
- v1 sohbet: `components/CopilotChatGate.tsx` (kabuk, eager; FAB / `?ai=` / `?chat=` /
  `coremetry:ai-ask` / `AI_PREFILL_EVENT` niyetleri; niyet kuyruğu + `ReplayQueued`; tek
  tembel `import('./CopilotChat')`), `components/CopilotChat.tsx` (`Drawer backdrop={false}
  closeOnEmptyClick width=620 resizeKey="coremetry-cosre-drawer-w"`, :1049-1058; `variant=
  'page'` → `/cosre`; `TraceKiosk` `launcher={false}`), `components/ai/useChatThread.ts` +
  `chatPersist.ts` (saved_views `page='ai-chat'`, `POST /api/ai/conversations`, 40 mesaj,
  600 ms debounce, sunucu kimliği), `ChatBubble.tsx` (`renderMessage` DIŞA AKTARILMIŞ saf
  çizici :431 — chatMarkdown motoru; ```chart çiti :391; `CodeBlock` :210 iç fonksiyon;
  `ToolStepsPanel initialOpen=false` :645), `stickToBottom.ts`, `EvidenceChips.tsx`,
  `aiVoteRemoved.ratchet.test.ts` (yasak dizgi listesi :34-41).
- shadcn tabanı: `components/coremetry/overlay/Sheet.tsx` (denetimli, `escBridge`, TR
  kapat), `coremetry/strings.ts` (`CM_STRINGS`), `coremetry/index.ts` (kabuk barrel'ı
  içe aktaramaz), `components/shadcn/*` — **v0.10.1639'da 24 dosya** (1613'teki 19 +
  `alert-dialog`, `checkbox`, `collapsible`, `label`, `toggle`, `toggle-group`; yerel
  checkout 1630'da 19 — uygulayıcı önce `git pull`), `docs/frontend/shadcn-audit.md`
  §11–§13 (görsel serbest, IA sor; mockup klasörü `docs/frontend/mockups/` **YOK**),
  `docs/frontend/shadcn-phase1-plan.md` §1 (ilk yük kümesi = `index.html` module script +
  modulepreload; eşleşmeyen `node_modules` eager `vendor` catch-all'a düşer; kabuk import
  yasağı; `scripts/bundle-budget.mjs`), §5.5 (bağımlılık tablosu — dışı operatör sorusu),
  §12 (Q2: ek shadcn bileşeni dilim bazında; Q7: axe 3 hücre).
- Bayrak: `internal/api/cosre_v2_flag.go` (`GET/PUT /api/admin/cosre-v2`, admin + audit,
  30 s memo), `cosre_v2_chat.go` (`requireCosreV2` → 404; gövde v1 ile ORTAK
  `copilotChatWith` → `{messages, context}` kabul eder), `copilot_config.go:84,149`
  (`cosreV2 bool,omitempty` → `/api/copilot/config`). FE: `lib/types.ts:3150 CosreV2Flag`,
  `api.cosreV2Flag` (yalnız GET), `pages/settings/CosreV2StatusLine.tsx` (salt okunur satır,
  AiTab:63). **`useCopilotEnabled.ts` `CopilotConfig`'te `cosreV2` alanı YOK ve
  `normalizeCopilotConfig` bilinmeyen alanı DÜŞÜRÜR → bayrak bugün FE'ye ulaşmıyor** (§1.1).
- Fixture'lar (repo kökü `testdata/cosre/`): `ui-stream-v1` (tam senaryo: step, budget,
  dynamic-tool ok + skipped, block-link, block-chart, 3 text + 2 reset, code-lock,
  source-document, source-url, links, followups+suggestions, evidence-summary,
  message-metadata), `-reset`, `-reset2` (çift), `-error` (class deadline), `-closed`
  (class closed). Hepsi nötr (orders-api, com.acme).
- Skills: `/frontend-conventions`, `/frontend-design-system`, `/copilot-surface`
  (yalnız atıf: 2b yeni AI ucu/prompt eklemez).

**Taban.** `origin/main` @ v0.10.1639. Faz 1 R1–R8 (1592–1607) ve 2a (1618) gemide.
Settings dilimi paralel yürüyor → B numaraları kesim anında verilir (`v0.10.NEXT` tekniği).

**Değişmeyen şeyler.** Tool döngüsü, prompt'lar, model seçimi, Go akış adaptörü, protokol
(`protocol.ts` kapalı kümeleri), 2a çekirdeği. v1 ağacı (`components/ai`, `CopilotChat`)
K-8 gereği dokunulmaz; **tek istisna** §0-4'ün kendisi: `ChatBubble.tsx:391` çit takma adı
(`'chart' || 'coremetry-chart'`) ve `CodeBlock`'un `export` edilmesi (§2.2).

**2b'de de geçerli kapılar (muafiyet yok).** `ui/no-raw-element` (components/cosre MUAF
DEĞİL → shadcn `Button`/`Textarea`); `uiImportRatchet` (`components/ui` donmuş: v2 dosyaları
ui/'den HİÇ import etmez — tavanı olmayan atomun tavanı 0); `inlineStyleRatchet` TOTAL
(v2'de `style={{` SIFIR); overlay import yasağı (Sheet yalnız `@/components/coremetry/
overlay`); kabuk import yasağı (`CopilotChatGate` statik olarak cosre/ai-elements/ai
import edemez); `bundle-budget` (ai/@ai-sdk/shadcn/radix girişte yasak, ≤347 KB gz);
`aiVoteRemoved`; `contrastTokens`/`colorLeaks`/`undefinedCssRefs`; `pollingBudget.pin`
(v2 yeni poll eklemez); `designAxe` (her yeni `components/shadcn` dosyası katalog girdisi
ister); `strings.pin` deseni (üretilen dosyadaki İngilizce metin sarmalayıcıda kapanır).

## 1. Kapı + giriş: bayrak v1/v2 seçer, v2 chunk entry dışında

### 1.1 Bayrak zinciri
`system_settings("cosre_v2")` → `s.cosreV2Enabled` (30 s memo) → (a) `requireCosreV2`
(rota, kapalıyken 404), (b) `/api/copilot/config.cosreV2` (copilot_config.go:149). FE
tarafı eksik: **B1** `CopilotConfig`'e `cosreV2?: boolean`, `normalizeCopilotConfig`'e
`...(c.cosreV2 ? { cosreV2: true } : {})` + test (v0.10.1138 `profiles` / 1461 `codeRead`
düşüşünün ikizi olmasın). Config modül önbelleği süreç ömrü boyunca sabit
(useCopilotEnabled.ts:5-10) → bayrak değişimi **sayfa yenilemesiyle** görünür;
`CosreV2StatusLine` metnine "değişiklik yeni yüklemede" notu eklenir (Q10 toggle ile).

### 1.2 Kapı: `CopilotChatGate` iki tembel gerçekleme tanır
```ts
const CopilotChat = lazy(() => import('./CopilotChat'));              // v1 — bugünkü
const CosreChatV2 = lazy(() => import('./cosre/conversation/CosreChatV2')); // v2 — yeni chunk
```
Seçim SAF: `lib/cosre/pickChatImpl.ts` `pickChatImpl(cfg, intent) → 'v1' | 'v2'`:
`cfg.cosreV2 === true && intent ∈ {fab, chatParam, prefill, ask} → 'v2'`; `intent ===
'aiSubject'` (`?ai=<subject>` açıklama turu) → B7'ye dek `'v1'`. Kapı bugünkü gibi ilk
niyette mount eder ve mount kalır (thread/taslak korunur); kuyruk + `ReplayQueued` aynen,
v2 de aynı pencere olaylarını dinler (`coremetry:ai-ask` → soruyu gönder; `AI_PREFILL_EVENT`
→ composer'ı doldur). **Geçiş dönemi (B1–B6) açıklama turu:** v2 mount iken `?ai=` gelirse
kapı yanına `<CopilotChat launcher={false} />` mount eder (TraceKiosk deseni v0.10.732: FAB
yok, yalnız `?ai=` ile açılır) ve v2 Sheet'ini kapatır; B7 bu dalı siler. Alternatif ve
karar: Q1. `/cosre` sayfası ve `TraceKiosk` 2b boyunca v1 (Q2).

Pin: `CopilotChatGate.pin.test`: `cosreV2:false` → v1; `true` + FAB → v2 chunk; `true` +
`?ai=` → v1 (B7'de tersine çevrilir); kabuk dosyası `components/cosre`'yi yalnız dinamik
`import()` ile anar.

### 1.3 Chunk ve bütçe (bundle-budget yeşil kalır)
- **Yeni `advancedChunks` grubu zorunlu:** `ai` + `@ai-sdk/react` + kapanışı yalnız tembel
  chunk'tan import edilse bile grupsuz kalırsa eager `vendor` catch-all'a (priority 10,
  preload'lu) düşer → `FORBIDDEN_SOURCES` ihlali (plan §1.1'in Radix gerekçesi aynen).
  ```ts
  // v0.10.NEXT — CoSRE v2: AI SDK kapanışı kendi tembel grubunda (ai-*.js).
  { name: 'ai', test: /node_modules\/(ai|@ai-sdk|swr|throttleit|zod|@standard-schema|eventsource-parser|@vercel\/oidc|json-schema|use-stick-to-bottom)[\/]/, priority: 88 },
  ```
  Kapanış package-lock'tan (ölçüldü): `ai@5.0.276` → `@ai-sdk/gateway 2.0.167`,
  `@ai-sdk/provider 2.0.5`, `@ai-sdk/provider-utils 3.0.41`, `@opentelemetry/api 1.9.0`;
  `@ai-sdk/react@2.0.279` → `swr 2.5.1`, `throttleit 2.1.0`; peer `zod 4.6.7`;
  alt: `@standard-schema/spec 1.1.0`, `eventsource-parser 3.1.1`, `@vercel/oidc 3.1.0`,
  `json-schema 0.4.0`. `@opentelemetry/api` regex'te YOK → `otel` grubunda (80) kalır,
  tembel chunk onu zaten yüklü bulur. 88: grafana (90) altı, 80'li gruplar üstü. Kurulumda
  `npm ls zod swr nanoid --all` ile doğrulanır: eager bir tüketicisi olan paket gruptan
  çıkarılır (grafana (90) daha yüksek öncelikli → onun bağımlılıklarını yutmaz; tersi riski
  `radix → grafana` kapısının ikizi olan `ai → grafana` statik import kapısıyla ölçülür).
- `components/cosre/**` + `components/ai-elements/**` tek importer'ın (`CosreChatV2`)
  kapanışında → ayrı grup gerekmez; `CorePanelMulti` zaten tembel (grafana chunk'ı).
- `scripts/bundle-budget.mjs`: `FORBIDDEN_SOURCES[2]` → `/node_modules\/(ai|@ai-sdk|swr|
  throttleit|use-stick-to-bottom)\//`, yeni `[3] /src\/components\/(cosre|ai-elements)\//`;
  rapor satırı `ai chunk: <size>` (`groupChunk('ai')`); kapı `ai-*.js → grafana-*.js`
  statik import yok. `aiSdkPin.test.ts` ve `bundleBudget.pin.test.ts` metin pinleri AYNI
  sürümde güncellenir (kapı kendi metnini ısırır).
- `aiSdkPin.test` "Faz 2a yalıtımı" → "Faz 2b tembel pini": statik `ai` / `@ai-sdk/*` /
  `lib/cosre` çekirdeği importu yalnız `lib/cosre/**`, `components/cosre/**`,
  `components/ai-elements/**`; eslint kabuk listesi (AppShell, Topbar, Sidebar,
  CopilotChatGate, …) hiçbirini statik import etmez.
- `eslint.config.js` kabuk bloğu: `**/components/cosre/**`, `**/components/ai-elements/**`,
  `ai`, `@ai-sdk/*` eklenir (`no-restricted-imports` dinamik `import()`'u saymaz → kapıdaki
  tembel satır serbest); `overlayImportBan.test`'e `components/cosre/x` probe'u (overlay
  yalnız `coremetry/overlay`'den; `shadcn/sheet` doğrudan yasak).
- Tailwind: `styles/index.css:29-31` `@source` listesine `'../components/cosre'` (liste
  pinli → pin güncellenir). Yoksa v2'nin utility sınıfları ÜRETİLMEZ (1631 emsali).
- Hedef ölçü: initial değişmez (±0; yalnız kapı +~0.3 KB gz); `ai-*.js` ~35–45 KB gz
  *(tahmin; B1 ölçer, migration-log'a yazar)*; `CosreChatV2-*.js` B6 sonunda ≤ 40 KB gz
  *(tahmin)*.
- **B1 ölçümü (v0.10.1647, sapma):** zod `ai` regex'indeyken grafana (90) onu yuttu
  ve `ai-*.js` grafana chunk'ını statik import etti (sonda build'i; yeni `ai → grafana`
  kapısı FAIL) → zod AYRI grup `{ name: 'zod', priority: 91 }`, `ai` regex'inden
  çıkarıldı. Sonda: `ai` 15.8 + `zod` 39.5 (grafana ile paylaşılan) KB gz. Initial
  +1.5 KB gz (paylaşılan modüller ayrı preload chunk'larına bölündü), migration-log.

### 1.4 Bayrak çevrimi thread kaybettirmez (aynı kalıcılık)
İki gerçekleme AYNI uç ve AYNI blob'u kullanır: `api.saveAiConversation({id?, messages})`
→ saved_views `page='ai-chat'`; `api.aiConversations()`, `api.aiConversation(id)`;
`?chat=<id>` ⇔ açık thread (`lib/chatUrl.syncChatParam` saf, paylaşılır). v2 (B6):
`useCosreChat({ onPersist })` → `onPersist = msgs => api.saveAiConversation({ id, messages:
msgs })` → yanıttaki sunucu `id` ref'e, sonraki yazımlar taşır; `?chat=` yazımı
`replace:true`. Debounce yok (2a: kayıt yalnız finish'te, tek yazım). Geri yükleme: yeni saf
`fromPersistMessages(ChatMessage[]) → CosreUIMessage[]` (`cosrePersist.ts`): `role/text` →
`text` parçası; `exchangeId` → `metadata.archiveExchangeId` (canlı `exchangeId` DEĞİL →
arşivde sinyal düğmeleri kapalı — v1 `restoreTurns` sözleşmesi, "ölü affordance" dersi);
`blocks[]` (yalnız sunucu doldurur) → `data-block-<type>` parçaları (`id: bN`, `final:true`)
→ canlı kart çizicileri; `codeRead` → `data-code-lock`; `evidence[]`/`explain` v2'de 2b'de
çizilmez (blob aynen kalır, v1 okur — bilgi kaybı yok). Yuvarlama testi §5.5.

## 2. AI Elements: kopyalanacak bileşenler, bağımlılık denetimi, sarmalama kuralı

### 2.1 Registry denetimi (registry.ai-sdk.dev, 2026-10-30 okundu)
`components.json` aliases: `ui → @/components/shadcn`, `utils → @/lib/cn` → CLI
`@/components/ui/x` → `@/components/shadcn/x`, `@/lib/utils` → `@/lib/cn` yazar (R5'teki
`"cn"` paketi sürprizi gibi çıktı DOĞRULANIR). **Kurulum CLI ile DEĞİL:** shadcn CLI
`dependencies`'i otomatik `npm install` eder (streamdown/shiki gelir). Dosyalar registry
JSON'unun `files[].content`inden scratchpad betiğiyle yazılır; bağımlılıklar elle, tam pin.
Verbatim güvencesi: `components/ai-elements/manifest.json` `{item, fetchedAt, sha256}` +
`aiElementsVerbatim.pin.test.ts` (dosya hash'i manifestle eşit; izinli düzenlemeli iki dosya
için düzenleme-sonrası hash kayıtlı; `streamdown|shiki|motion|@xyflow` importu dizinde yok).

| Öğe | npm bağımlılığı (registry) | shadcn bağımlılığı | Karar |
|---|---|---|---|
| `conversation` | lucide-react, **use-stick-to-bottom** (yeni) | button ✓ | KOPYALA — Q3. Reddedilirse `components/cosre/conversation/Conversation.tsx` mevcut saf `ai/stickToBottom.ts` (v0.10.650) ile |
| `message` | ai, lucide-react, **streamdown** (yeni, ağır: react-markdown + shiki + katex + mermaid) | button, button-group, tooltip ✓ | KOPYALA, **tek izinli düzenleme**: `MessageResponse` + `streamdown` importu silinir (Q4). Markdown = §2.2 `MarkdownText` |
| `tool` | ai, lucide-react | badge ✓, collapsible ✓ (1636), `code-block` (→ shiki) | KOPYALA, **tek izinli düzenleme**: `./code-block` → `@/components/cosre/conversation/CodeBlock` (Q4). `ToolHeader` durum rozetleri İngilizce SABİT (Pending/Running/Completed/Error) → kullanılmaz; `StepHeader` cosre/'da (R10 strings deseni) |
| `prompt-input` | ai, lucide-react, nanoid (ağaçta 3.3.20, `nanoid()` API aynı) | button, command, dropdown-menu, select ✓; **hover-card**, **input-group** ✗ | KOPYALA; `hover-card` + `input-group` `npx shadcn@4.21.5 add` (Q2 ruling: dilim bazında) + `/design` katalog girdileri (designAxe şartı). Kablolanan: Provider/PromptInput/Body/Textarea/Footer/Tools/Button/Submit; attachments/speech/select/tabs/command parçaları prop verilmez |
| `sources` | lucide-react | collapsible ✓ | KOPYALA |
| `suggestion` | — | button, scroll-area ✓ | KOPYALA (followups) |
| `loader` | — | — | KOPYALA (küçük; "düşünüyor" durumu) |
| `reasoning` | **@radix-ui/react-use-controllable-state** (ayrı paket; `radix-ui` tek paketinde var ama import yolu farklı), **streamdown**, `shimmer` (→ **motion**) | collapsible | KOPYALAMA. "Araştırma adımları" = `components/cosre/conversation/Steps.tsx` (shadcn collapsible + Tool/ToolContent/ToolInput/ToolOutput + StepHeader). Reasoning = model düşüncesi kapsam dışı (§0) |
| `shimmer`(motion) · `code-block`(shiki) · `response`(streamdown) · `inline-citation`(carousel) · `context`(tokenlens) · `chain-of-thought` · `task` · `queue` · `plan` · `artifact` · `checkpoint` · `confirmation` · `model-selector` · `open-in-chat` · `web-preview` · canvas ailesi (@xyflow) | — | — | KOPYALAMA |

**Onaylı küme (phase1-plan §5.5) dışı — karar ister (Q3/Q4):** `use-stick-to-bottom`
(öneri: tam pin, kabul; React 19 peer kontrolü kurulumda); `streamdown`, `shiki`, `motion`,
`@radix-ui/react-use-controllable-state` (öneri: HAYIR — §0-4 chatMarkdown kararı, bundle);
shadcn `hover-card`, `input-group` (öneri: kabul, B2). `depPins.test`: yeni DOM-şekillendiren
paket (`use-stick-to-bottom`) tam pin. K-12: chat dizinlerinde yeni `https://` host yok
(grep-kapısı — ai-elements dosyaları da taranır).

### 2.2 Coremetry davranışı nerede (üretilen dosyalar verbatim; davranış `components/cosre/`)
Dizin kararı §0-3 (`components/cosre/{conversation,evidence,prompt}`) geçerli; shadcn
kararı yalnız `ai-elements` kopyasını ekledi.

- `conversation/`: `CosreChatV2.tsx` (Sheet + `useCosreChat` + başlık; §4), `Thread.tsx`
  (ai-elements `Conversation` sarmalayıcısı, TR metinler, "↓ En alta"), `MessageList.tsx`
  (`visibleParts(m)` → `partRenderers`), `MarkdownText.tsx` (= **mevcut `renderMessage`**,
  `components/ai/ChatBubble.tsx:431` dışa aktarılmış saf çizici: chatMarkdown + chatInline +
  MdTable + CodeBlock + ```chart çiti + akış imleci; kullanıcı metni `charts:false`), `CodeBlock`
  (ChatBubble.tsx:210 iç fonksiyon `export` edilir — tek kelime; Tool giriş/çıkış JSON'u da
  bununla), `Steps.tsx` + `StepHeader.tsx` (araç adımları + bütçe; varsayılan v1 gibi kapalı,
  `ToolStepsPanel initialOpen=false`), `SourcesList.tsx`, `ErrorCard.tsx`, `StopBadge.tsx`,
  `CodeLockPill.tsx`, `strings.ts` (TR sabitleri, `CM_STRINGS` emsali).
  **Çit takma adı (§0-4):** `ChatBubble.tsx:391` `b.lang === 'chart'` → `(b.lang === 'chart'
  || b.lang === 'coremetry-chart')` — v1 ve v2 aynı motoru paylaştığı için tek yer; v1 de
  kazanır. K-8 istisnası olarak §0'da kayıtlı.
- `evidence/`: `EvidenceCard.tsx` (ortak gövde §4), aile çizicileri `families/{Metrics,
  Traces,Logs,Exceptions,Problems,Services,DbMessaging,Deploys,K8s,Oracle,Wiki,Meta}.tsx`,
  `RelevanceToggle.tsx` (ilgili/ilgisiz), `MessageNote.tsx`, `ChartBlock.tsx` (lazy
  `CorePanelMulti` + `lib/chatPanelSpec.chartBlockToPanel`; spec-şekilli yük → `CosreChart`),
  `LinkChips.tsx` (`lib/chatBlocks.mergeBlockLinks`), `TypedBlock.tsx` (13 blok türü →
  mevcut `components/ai` kartları: `ChatTopologyCard`, `ChartsSignalsCard`, `LogSpikeCard`,
  `MetricPanelCard`, `OperationCard`, `ChatTraceList`, `MdTable` — yeni importer serbest,
  ui/ importu o dosyaların içinde zaten var, ratchet oynamaz).
- `prompt/`: `Composer.tsx` (PromptInput sarmalayıcısı: Enter gönderir / Shift+Enter satır,
  IME `isComposing`, meşgulken `stop()` + bekleyen soru (audit §3.3), konuşma başına taslak
  `ai/composerDraft` kancası ile), `Followups.tsx` (Suggestions → `sendMessage` +
  `followup_click` sinyali).
- `lib/cosre/`: `pickChatImpl.ts`, `partRenderers.ts` (kapalı harita, §3), `familyOf.ts`
  (araç adı → aile; §4 tablosu veri olarak), `signalDetail.ts` (512 B UTF-8 kırpma),
  `cosrePersist.ts` += `fromPersistMessages`.
- Taşınan saf yardımcılar (codemod, shim yok, Drawer importer'ı güncellenir):
  `components/ui/emptyAreaClose.ts` → `lib/emptyAreaClose.ts`, `components/ui/drawerWidth.ts`
  → `lib/drawerWidth.ts` (v2 ui/'den import edemez; ratchet tavanı 0).

## 3. Parça → bileşen eşlemesi (paylaşılan fixture'lardaki her UI stream parçası)

Render girişi her zaman `visibleParts(m)` (reset edilen text parçaları akışta gizli).
`ai@5.0.276` davranışı (2a'da ölçüldü): `data-*` parçaları aynı `id` ile YERİNE GEÇER;
`transient:true` parça `message.parts`'a girmez; `dynamic:true` araç parçaları
`dynamic-tool` tipinde (`state`: input-streaming → input-available → output-available);
`start-step`/`finish-step` → `step-start` parçası; `error` → `chat.error` + `onFinish
isError`; `message-metadata` → `message.metadata`.

| Tel parçası (fixture) | `message.parts`'ta | Bileşen (dosya) | Not |
|---|---|---|---|
| `start{messageId}` | `message.id` (ULID) | — | Sinyal/kayıt anahtarı değil (o `exchangeId`); kimlik sunucudan |
| `start-step` / `finish-step` | `step-start` | **ignore** (null) | Haritada açık `'ignore'` satırı |
| `text-start/delta/end` | `text` (sıra = textIdMap) | `MarkdownText` (`renderMessage`, streaming = son text && status streaming) | Akışta açık ```chart → "grafik hazırlanıyor…" (mevcut) |
| `data-reset` (transient) | YOK; `onData` → ResetSet | render gizler, finish siler | 2a ✓ (reset, reset2 fixture) |
| `tool-input-start/available` (dynamic) | `dynamic-tool` state input-* | `Steps > StepRow` (Tool/ToolContent/ToolInput verbatim gövde + `StepHeader` "çalışıyor") | `toolCallId = s<i>`; `input.argsText`/`args` |
| `tool-output-available` | state output-available; `output{ok,skipped,reason,bytes,durationMs,href,preview,sources,truncated}` | StepRow durumu: `ok` → tamam · `skipped` → "yürütülmedi · reason" · `ok:false` → hata (`tool-output-error` KULLANILMAZ, protokol) **+ `EvidenceCard`** (aile = `familyOf(toolName)`) | Her araç çağrısı = bir kart (§4) |
| `data-step{i,label,phase}` | `data-step` (id `s<i>`) | `Steps` içinde araçsız etiket satırı | Bütçe etiketleri dahil |
| `data-budget` (id sabit) | tek parça, yerine geçer | `Steps` özet satırı "executed/maxCalls çağrı · roundsUsed/maxRounds tur", `exhausted` → rozet | — |
| `data-block-link` | `data-block-link` (id `bN`) | `LinkChips` (`mergeBlockLinks` ile `data-links` birleşir, href tekil, `isSafeAppPath`) | — |
| `data-block-chart` | `data-block-chart` | `ChartBlock`: `{series,title,unit}` → `chartBlockToPanel` → lazy `CorePanelMulti`; `{service,agg,…}` → `CosreChart` | `corePanelContracts` korunur; `Suspense` içinde `Spinner` |
| `data-block-{table,trace_list,evidence,text}` | aynı ad | `TypedBlock` → `MdTable` / `ChatTraceList` / kanıt tablosu / metin | B5 |
| `data-block-{topology,charts_signals,log_spike,metric_panel,operation,service_report,action}` | aynı ad | `TypedBlock` → mevcut `components/ai` kartları (§2.2); `action` → mevcut eylem bloğu | B5; `satisfies Record<BlockType,…>` tüm 13'ü zorlar |
| ```chart / ```coremetry-chart (metin içinde) | `text` | `MarkdownText` → `CosreChart` | ChatBubble.tsx:391 takma adı (§2.2) |
| `data-code-lock{locked}` | `data-code-lock` | `CodeLockPill` ("Kod okundu") | Kayıt `codeRead` (2a ✓) |
| `source-url` / `source-document` | `source-url` / `source-document` | `SourcesList` (ai-elements Sources) — `url` yalnız `https` ya da `isSafeAppPath` ve `metadata.allowedLinks` kapısından; document href'siz satır | — |
| `data-links{links[]}` | `data-links` | `LinkChips` | — |
| `data-followups{followups[],suggestions[]}` | `data-followups` | `Followups` (Suggestions; iki liste tek sıra) → `sendMessage` + `followup_click` | Yalnız son asistan mesajında, status ready iken |
| `data-evidence-summary{evidence[]}` | `data-evidence-summary` | `EvidenceCard`'a sorgu künyesi (source · pencere · satır · ms; `ChatQueryEvidence` alanları) — eşleme (tool, sıra) ile | Join zayıf → Q7 (`stepId`) |
| `message-metadata{exchangeId,callId,allowedLinks,evidenceSpanIds,open?}` | `message.metadata` | sinyal anahtarı (`exchangeId`); `open` → onFinish'te `isSafeAppPath` + SPA navigate, yalnız Sheet kipinde (v1 D7b) | `CosreMessageMetadata.open?: string` eklenir (lib/types.ts) |
| `data-error{class,raw}` + `error{errorText}` | `data-error`; `metadata.error` (onFinish) | `ErrorCard`: class → TR başlık (deadline/overflow/cancelled/max_tokens/provider_down/quota/closed/unknown), `raw` ayrıntı; "Yeniden dene" = `regenerate()` | Kayıt dışı (2a ✓); error + closed fixture |
| `finish{finishReason}` | status → ready | — | — |
| `stop()` → `abort` | `metadata.partial` | `StopBadge` ("yarıda durduruldu") | Geçmişe `STOPPED_NOTE` (2a ✓) |
| `data-unknown` | `data-unknown` | render yok; DEV `console.warn` | Kapalı küme testi zaten pinler |
| `: ping` | — | parser yok sayar | — |

**Kapalılık:** `lib/cosre/partRenderers.ts` `PART_RENDERERS: Record<DataPartName | 'text' |
'dynamic-tool' | 'source-url' | 'source-document' | 'step-start', Renderer | 'ignore'>` —
derleme zamanı eksiksizlik (`satisfies`) + çalışma zamanı testi (`DATA_PART_NAMES` ∪ diğerleri
her biri bir satır). `protocol.ts`'e yeni ad gelince burası da kırmızı (fixture → protocol →
renderer zinciri).

**Kanıt kartı (aile başına bir çizici, §4 audit tablosu veri olarak `familyOf.ts`'te).**
Ortak gövde: başlık (araç adı TR, phase), `request`/`argsText` özeti, pencere, kaynak rozeti
(`output.sources[].source/state`), süre, durum rozeti, kompakt sonuç (`preview` / aile
tablosu / sparkline), "<sayfa>'da aç" pivotu — **href HER ZAMAN sunucudan** (`output.href`),
FE yardımcısı yalnız boşlukta ve `isSafeAppPath` kapısından; kart başına **ilgili / ilgisiz**
(`RelevanceToggle`: iki durumlu, optimistik, yerel; `api.aiSignal(exchangeId,
'evidence_relevant'|'evidence_irrelevant', toolCallId)`), mesaj düzeyinde **not**
(`MessageNote`: "Not" düğmesi → shadcn `Textarea`, 512 B UTF-8 kırpma → `api.aiSignal(…,
'note', text)`). Düğmeler `metadata.exchangeId` gelmeden ve arşiv mesajında (yalnız
`archiveExchangeId`) devre dışı. `CosreSignalKind` birliğine `'evidence_relevant' |
'evidence_irrelevant' | 'note'` eklenir (Go `cosrelearn/signals.go:34-36` zaten var, R7).
**👍/👎 yok**: `aiVoteRemoved.ratchet` yasak listesi (AIFeedbackButtons, postAIFeedback,
`/api/ai/feedback`, 👍/👎 ikon, "Faydalı işaretle", "yararlı mıydı?") v2 dizinlerinde de
taranır — zaten `src/**` genelini tarıyor.

## 4. Yerleşim / IA: Sheet, giriş noktaları, v1 paritesi

**Öneri:** v2, `@/components/coremetry/overlay` `Sheet`'inde, v1 çekmecesiyle AYNI giriş
noktalarından açılır; §13 gereği görsel serbest (Inter + JetBrains Mono, zinc dark, yoğun
satırlar), IA seçimleri aşağıda **"v1 ile aynı" varsayılanıyla** soru olarak listeli.
Mockup yok (`docs/frontend/mockups/` mevcut değil) → ilk görünür dilim (B1) prep'te
operatöre gösterilir, sonra B2.

### 4.1 Sheet kabuğu (B1)
- `Sheet open onOpenChange modal={false}` — v1 `backdrop={false}`: sohbet sayfaya EŞLİK
  eder, arkası tıklanır/kaydırılır (Radix Dialog `modal=false` → Overlay çizilmez,
  pointer-events kapanmaz). `SheetContent side="right"`, `onInteractOutside={e =>
  e.preventDefault()}` (Radix'in dışarı-tık kapatması yerine v1 kuralı) + boş alana tıklama
  kapatır (`lib/emptyAreaClose.isEmptyAreaClick`, v1 `closeOnEmptyClick`). Esc: `escBridge`
  (tek Esc yığını) — v1 `useEscLayer` ile aynı yığın. Kapat düğmesi `CM_STRINGS.close`;
  `aria-label="CoSRE sohbeti"`.
- Genişlik: v1 ile aynı localStorage anahtarı `coremetry-cosre-drawer-w` + aynı
  `clampDrawerWidth` (lib'e taşınan `drawerWidth.ts`), varsayılan `AI_DRAWER_WIDTH` 620
  (`components/ai/answerCard.ts`). Uygulama `style={{` OLMADAN: `useLayoutEffect` →
  `el.style.setProperty('--cosre-sheet-w', …)`; sınıf `w-full sm:w-(--cosre-sheet-w)
  sm:max-w-none` (üretilen `w-3/4 sm:max-w-sm` ezilir). Sürükleme tutamağı (`resizeKey`
  davranışı: sola sürükle, çift tık varsayılan) → `SheetResizeHandle` B6 (Q5).
- Katman: shadcn `z-50` ↔ eski `--z-fab`/`--z-topbar` (`zLayers.test`) — FAB Sheet açıkken
  gizli (v1 gibi), palet/toast Sheet üstünde kalmalı; B1 ölçer.
- Odak: non-modal → Radix odak tuzağı yok; açılışta composer'a odak (v1 gibi); kapanışta
  odak FAB'a döner.
- Yoğunluk: `useDensity` → shadcn Button `size`; tema: tokens.css üç `[data-theme]`; grafik
  `useThemeTick`.

### 4.2 IA seçimleri (hepsi soru; varsayılan = v1)
| # | Seçim | v1 bugün | Öneri (varsayılan) | Dilim |
|---|---|---|---|---|
| IA-1 | Giriş noktaları | FAB · `?ai=` · `?chat=` · `coremetry:ai-ask` · prefill · metrik paneli "Ask CoSRE" | Aynı; `?ai=` geçişi §1.2/Q1 | B1/B7 |
| IA-2 | Başlık | AiMark + "CoSRE" + bağlam pin + Geçmiş + Yeni + Tam sayfa + tema + ✕ | B1: AiMark + başlık + Yeni + ✕; Geçmiş B6; Tam sayfa linki (`/cosre` v1) kalır; tema/pin 2b dışı | B1/B6 |
| IA-3 | Gövde sırası (asistan mesajı) | adım paneli (kapalı) → metin → kanıt çipleri → linkler → takip çipleri | Steps (kapalı, özet satırı) → metin → kanıt kartları (kapalı, kompakt başlık) → LinkChips → Sources → Followups | B2–B5 |
| IA-4 | Steps varsayılanı | kapalı, özet | akışta açık (canlı ilerleme), finish'te kapanır ve özet satırı kalır | B3 (Q8) |
| IA-5 | Kanıt kartı yeri | çipler altta, blok çipin altında açılır | kart = araç çağrısı; metnin altında; son kart açık, öncekiler kapalı (v1 `defaultOpen="last"`) | B4 |
| IA-6 | Composer | markdown araç çubuğu (kapalı varsayılan), @-kapsam, /komut, ad tamamlama, model seçici, Derin düşün, Kod hapı, taslak | B2: Enter/Shift+Enter, IME, durdur, taslak, otomatik yükseklik; diğerleri 2b DIŞI (Faz 3 kararı) | B2 (Q9) |
| IA-7 | Geçmiş | `CosreSidebar` (/cosre) + çekmecede liste | B6: Sheet içinde shadcn liste (`api.aiConversations`), `?chat=` ile aç | B6 (Q6) |
| IA-8 | Mobil | çekmece tam genişlik | `<640px` tam genişlik, Sheet | B1 |
| IA-9 | Boş durum | karşılama + CosreCapabilities kartları (takım seçici) | B1: başlık + tek cümle + composer; CosreCapabilities 2b dışı | B1 |
| IA-10 | Hata/durdurma | hata satırı + "Yeniden dene"; "[YARIDA DURDURULDU]" | ErrorCard + Yeniden dene; StopBadge | B5 |

Her görsel değişiklik ilgili sürümün `docs/frontend/migration-log.md` satırına `design:`
ile yazılır (§13). "No floating strips" (operatör): Sheet içinde yapışkan şerit yok;
composer alt sabit (v1 gibi — composer şerit değil, form).

## 5. Testler

Kapı: `npx tsc --noEmit && npx eslint src && TZ=UTC npx vitest run` + `npm run bundle:check`
(her dilimde). Playwright **yalnız elle** (§0-5): operatör prep'te `/design?cell=` üç temayı
gözler; `browser_snapshot` yok.

### 5.1 Parça → bileşen (paylaşılan fixture, GERÇEK SDK)
`components/cosre/partsToComponents.test.tsx` (jsdom). Sürücü: `useCosreChat.test.tsx`'teki
`sseFetch` → `src/test/cosreSse.ts`'e çıkarılır (test-only, `src/test/axe.ts` emsali);
`createCosreChat({ fetch })` + `testdata/cosre/<name>.sse` baytları; `<MessageList>`
son mesajla render. Tablo:
- `ui-stream-v1`: metin yalnız t3; Steps 2 satır (get_service_health "tamam", query_metrics
  "yürütülmedi"), bütçe "1/2 · 1/5 · doldu"; LinkChips 1 (`/service?name=orders-api`);
  ChartBlock mount (lazy → Suspense fallback kabul); CodeLockPill; Sources 2 (wiki
  `https://…` link, runbook href'siz); Followups 2 (followups + suggestions katlanmış);
  EvidenceCard 1 (Services ailesi, `s2`), ilgili/ilgisiz düğmeleri ETKİN (exchangeId geldi);
  `👍`/`👎` yok.
- `-reset` / `-reset2`: tek metin; `resetApplied` [0] / [0,1].
- Akış ortası (frame `data-reset t1` sonrasında dur): t1 `message.parts`'ta DURUR,
  DOM'da YOK (`visibleParts`), imleç son görünür text'te.
- `-error`: ErrorCard class=deadline TR başlık + raw; "Yeniden dene"; kayıt yok.
- `-closed`: class=closed; Steps etiketi var.
- `stop()` (duraklatılmış akışta): StopBadge; kayıt yarım metinle (2a) ve geçmişe
  `STOPPED_NOTE`.
- Eksiksizlik: `PART_RENDERERS` anahtarları === `DATA_PART_NAMES` ∪ {text, dynamic-tool,
  source-url, source-document, step-start}; `BLOCK_TYPES` 13'ü `TypedBlock`'ta.

### 5.2 Kapı ve bayrak
- `normalizeCopilotConfig({enabled:true, cosreV2:true}).cosreV2 === true`; yoksa alan yok.
- `CopilotChatGate.pin.test`: `pickChatImpl` tablosu (bayrak × niyet); kabuk dosyası
  `components/cosre`'yi yalnız `import(` ile anar; `aiSdkPin` tembel pini (§1.3).
- B7'de tablo tersine döner (`aiSubject` → v2) ve launcher=false dalı silinir.

### 5.3 Bundle
- `bundleBudget.pin.test`: `ai` grubu regex'i + öncelik 88 (grafana < 88 < charts);
  `FORBIDDEN_SOURCES[2]`/`[3]` metinleri; `ai → grafana` statik import kapısı; `@source`
  listesinde `components/cosre`.
- Her dilim `npm run bundle:check` son satırını migration-log'a yazar; `ai chunk` ve
  `CosreChatV2` chunk boyutu Notes'ta.

### 5.4 Erişilebilirlik (axe, 3 tema hücresi)
`/design` kataloğuna `cosre/ChatV2` durumları: boş, akış (fixture ortası), bitmiş (ui-stream-v1
→ Steps + kart + chart + sources + followups), hata, durdurulmuş; her biri `createCosreChat`
ile `sseFetch`'ten (gerçek SDK durumu, sahte veri yok) — `designAxe.test` CI 3 hücre
(dark/light/redhat × comfortable), 9'lu matris gece. Yeni shadcn dosyaları (`hover-card`,
`input-group`) katalog girdisi (designAxe "dosya var, girdi yok" kırmızı). Bilinen axe
notları (R10): Radix Popover `role=dialog` adsız → `aria-label`; `CommandSeparator` kullanma.

### 5.5 Kalıcılık ve bayrak çevrimi
- Finish → `api.saveAiConversation` tek çağrı, `toPersistMessages` çıktısı; yanıt `id` ikinci
  turda taşınır; `?chat=` yazımı `replace:true`; hatalı tur kaydedilmez.
- Yuvarlama: v2 `toPersistMessages` → v1 `restoreTurns` metin dizisi eşit; v1 `persistMessages`
  çıktısı (evidence/explain/exchangeId/codeRead/blocks'lu) → v2 `fromPersistMessages` →
  metinler eşit, `blocks` → `data-block-*` parçaları, `exchangeId` → `archiveExchangeId`,
  sinyal düğmeleri kapalı.
- Stop → partial → kayıt; error → kayıt yok (2a testleri zaten; burada UI düzeyi).

### 5.6 Sinyaller ve metinler
- RelevanceToggle tık → `POST /api/ai/signal` `{exchangeId, kind:'evidence_relevant',
  detail:'s2'}`; ikinci tık ters yön; exchangeId yoksa devre dışı; `MessageNote` 513+ bayt
  Türkçe metin → 512 bayta kırpılır (UTF-8, karakter değil).
- `cosreStrings.pin.test`: ai-elements'ın İngilizce sabitleri (Pending/Running/Completed/
  Error, "Used N sources", "Close") render çıktısında yok; TR `strings.ts` kullanılır.
- `aiElementsVerbatim.pin.test`: manifest hash'leri; dizinde `streamdown|shiki|motion|
  @xyflow` importu yok; izinli düzenleme yalnız iki dosyada ve kayıtlı hash.

### 5.7 Go (yalnız Q7 kabul edilirse)
`uistream` çeviricisi `data-evidence-summary.evidence[].stepId` ekler → golden `-update`,
FE fixture testi aynı baytları okur (R6 sınıfı, ayrı sürüm, döngü mantığı değil).

## 6. Sürüm dilimleri (B1–B7) ve operatör durak noktası

Her dilim tek mantıksal birim, ayrı `v0.10.X` (numara kesimde; Settings dilimiyle paralel),
geri alınabilir, bayrak kapalıyken kullanıcıya görünmez. Ortak kapı §5 + `make audit` +
`~/go/bin/golangci-lint run ./...` (Go'ya dokunan dilimde). Her satır `docs/frontend/
migration-log.md`'ye (bundle satırı + `design:`). Commit biçimi CLAUDE.md.

| # | Sürüm | İçerik | Dosyalar (özet) | Özel kapı / not |
|---|---|---|---|---|
| **B1** | v0.10.NEXT | **Bayrak kablosu + boş v2 kabuğu Sheet'te.** `CopilotConfig.cosreV2` + normalize; `pickChatImpl`; kapı iki tembel gerçekleme (`?ai=` → v1, Q1 deseni); `CosreChatV2` = coremetry Sheet (non-modal, aynı genişlik anahtarı, Esc, boş alan kapatma, başlık: AiMark + "CoSRE" + Yeni + ✕) + boş durum metni + **pasif** composer yerinde (shadcn Textarea, gönderim yok, "B2'de" değil — placeholder "Soru yazın…" ve devre dışı); `ai` chunk grubu + bundle-budget regex/rapor + `@source cosre` + eslint kabuk bloğu + aiSdkPin tembel pini; `emptyAreaClose`/`drawerWidth` → `lib/` codemod; `CosreV2StatusLine` "yeni yüklemede" notu (+ Q10 toggle) | `useCopilotEnabled.ts`, `CopilotChatGate.tsx`, `lib/cosre/pickChatImpl.ts`, `components/cosre/conversation/CosreChatV2.tsx`, `strings.ts`, `vite.config.ts`, `scripts/bundle-budget.mjs`, `styles/index.css`, `eslint.config.js`, pin testleri, `lib/emptyAreaClose.ts`, `lib/drawerWidth.ts`, `ui/Drawer.tsx` (import yolu) | bundle: initial ±0, `ai-*.js` ölçülür. **⏸ OPERATÖR DURAĞI:** prep'te bayrak açılır, Sheet görünür; IA-1/2/8/9 + Q1–Q5 burada kesinleşir. B2 bu onay olmadan başlamaz |
| **B2** | +1 | **Konuşma + metin/reset + prompt input.** ai-elements `conversation`, `message` (izinli düzenleme), `prompt-input`, `loader` + shadcn `hover-card`, `input-group` (+ katalog); `use-stick-to-bottom` pin; `Thread`, `MessageList`, `MarkdownText` (`renderMessage`), `CodeBlock` export, ChatBubble:391 takma adı; `Composer` (Enter/Shift+Enter/IME/stop/taslak); `useCosreChat` kablosu; `coremetry:ai-ask` / prefill dinleyicileri; `partRenderers` iskeleti (text + ignore + diğerleri "yakında" değil → **render etmez**, test 'ignore' sayar); `/design` boş/akış/bitmiş-metin hücreleri | `components/ai-elements/{conversation,message,prompt-input,loader}.tsx` + `manifest.json`, `components/shadcn/{hover-card,input-group}.tsx`, `components/cosre/conversation/*`, `prompt/Composer.tsx`, `lib/cosre/partRenderers.ts`, `pages/design/catalog.tsx` | reset/reset2/akış-ortası testleri UI düzeyinde; axe 3 hücre; `aiElementsVerbatim.pin`; `cosreStrings.pin`. İlk KULLANILABİLİR sürüm |
| **B3** | +2 | **Araç adımları + bütçe ("Araştırma adımları").** ai-elements `tool` (izinli düzenleme); `Steps`, `StepHeader`; `data-step`, `data-budget`, `dynamic-tool` üç durum; akışta açık / finish'te kapalı özet (IA-4) | `components/ai-elements/tool.tsx`, `components/cosre/conversation/{Steps,StepHeader}.tsx` | ui-stream-v1 "2 satır + bütçe" testi; closed fixture etiketi |
| **B4** | +3 | **Kanıt kartları + sinyaller.** `EvidenceCard` ortak gövde + 12 aile çizicisi (§4 audit tablosu `familyOf.ts`), `data-evidence-summary` künyesi, pivot href kuralı; `RelevanceToggle`, `MessageNote`; `CosreSignalKind` += 3; `signalDetail` 512 B; arşivde kapalı | `components/cosre/evidence/*`, `lib/cosre/{familyOf,signalDetail}.ts`, `lib/types.ts` | `aiVoteRemoved` yeşil; sinyal gövdesi testi; Q7 kabulse Go `stepId` AYRI sürüm (B4a, önce) |
| **B5** | +4 | **Veri blokları + hata/durdur.** `data-block-*` 13 tür (`TypedBlock`, `ChartBlock`, `LinkChips`), `data-links`, `data-followups` (Suggestions → gönder + `followup_click`), `data-code-lock` pill, `source-*` (Sources), `metadata.open` navigasyonu, `ErrorCard` + regenerate, `StopBadge`; `CosreMessageMetadata.open` | `components/ai-elements/{sources,suggestion}.tsx`, `components/cosre/evidence/{ChartBlock,LinkChips,TypedBlock}.tsx`, `conversation/{SourcesList,ErrorCard,StopBadge,CodeLockPill}.tsx` | `corePanelContracts`; error/closed/stop UI testleri; 13 blok `satisfies` |
| **B6** | +5 | **Kalıcılık + bayrak çevrimi + geçmiş.** `onPersist` → `saveAiConversation` + sunucu id + `?chat=` senkronu; `fromPersistMessages` (blocks → data-block parçaları, `archiveExchangeId`); Sheet içinde Geçmiş listesi (IA-7); `SheetResizeHandle` (Q5); yuvarlama testleri | `lib/cosre/cosrePersist.ts`, `components/cosre/conversation/{History,SheetResizeHandle}.tsx`, `CosreChatV2.tsx` | §5.5; bayrak kapat/aç → aynı thread iki UI'da |
| **B7** | +6 | **Bağlam paritesi + `?ai=` geçişi.** `prepareSendMessagesRequest` gövdesine `context` (pageContext/range/env; `ai/chatContextScope` saf yardımcıları); `?ai=<subject>` → `context.explainAsk` (`ai/explainTurn` saf yardımcıları, sunucu zaten `chat_explain_turn.go` ile işler); `pickChatImpl` tüm niyetler → v2; kapıdaki `launcher={false}` v1 dalı silinir | `lib/cosre/{useCosreChat,context}.ts`, `CopilotChatGate.tsx`, `pickChatImpl.ts` | `?ai=` kodeği testi (13 tür → explainAsk); CopilotChat pin testleri dokunulmaz. Bundan sonra P1 (bayrak varsayılanı açık) ayrı karar |

Durak: **B1'den sonra** (ilk görünür sürüm). B2 "ilk kullanılabilir" — operatör isterse
ikinci durak. B4a (Go `stepId`) yalnız Q7 "evet" ise ve B4'ten önce.
Geri alma: her dilim tek commit; bayrak kapalıyken hiçbir dilim kullanıcıya dokunmaz
(v1 ağacı değişmez; tek ortak nokta ChatBubble:391 takma adı — v1 için de doğru davranış).

## 7. Operatöre sorular (yalnız gerçek olanlar, önerilen varsayılanla)

| # | Soru | Önerilen varsayılan |
|---|---|---|
| **Q1** | **Geçiş döneminde `?ai=` açıklama turu.** B7'ye dek v2 `context.explainAsk` taşımaz. (a) kapı `?ai=`/açıklama niyetini v1'e yollar (bayrak açıkken bile; v2 mount ise yanına `launcher={false}` v1 — kiosk deseni) · (b) B7 B1'den hemen sonra, B2–B6'dan önce gelir · (c) bayrak açıkken `?ai=` v2'de düz soru olarak gönderilir (kanıt yeniden kurulmaz) | **(a)** — prep'te Explain düğmeleri çalışır kalır, B7 dalı siler; (b) kabul edilebilir ikinci seçenek |
| **Q2** | `/cosre` tam sayfa ve `TraceKiosk` 2b boyunca v1 mi? | **Evet** — Sheet dışı yüzeyler P1 kararına kalır |
| **Q3** | Yeni bağımlılık `use-stick-to-bottom` (ai-elements `conversation`) — tam pin kabul mü? | **Kabul** (küçük, React 19 peer kurulumda doğrulanır); hayırsa `Conversation` mevcut `stickToBottom.ts` ile yazılır, `conversation` kopyalanmaz |
| **Q4** | Üretilen dosyalara **izinli düzenleme** listesi: `message.tsx` (`MessageResponse`/`streamdown` silinir), `tool.tsx` (`./code-block` → mevcut `CodeBlock`); `reasoning`/`shimmer`/`code-block`/`response` KOPYALANMAZ (streamdown/shiki/motion/`@radix-ui/react-use-controllable-state` reddi) | **Kabul**; verbatim güvencesi manifest hash + pin testi (button.tsx/table.tsx izinli düzenleme emsali). shadcn `hover-card` + `input-group` kurulumu B2'de |
| **Q5** | Sheet genişliği: B1 sabit (aynı localStorage anahtarı, 620 varsayılan), sürükleme tutamağı B6 | **Evet** (v1 davranışı korunur, tutamak sonra) |
| **Q6** | Geçmiş (IA-7): Sheet içinde shadcn liste (B6) mi, 2b dışı mı? | **B6'da liste** — `?chat=` ile aç; `CosreSidebar` v1'de kalır |
| **Q7** | `data-evidence-summary.evidence[]` ↔ araç çağrısı eşlemesi: FE (tool, sıra) ile mi, Go `stepId` alanı (R6 sınıfı payload alanı, ayrı küçük sürüm B4a) mı? | **Go `stepId` evet** (küçük, deterministik); FE alan yokken (tool, sıra)'ya düşer |
| **Q8** | Steps paneli: akışta açık, finish'te kapalı özet (IA-4) — v1 "hep kapalı"dan sapma | **Evet** (canlı ilerleme görünür; bitince v1 gibi sade). Hayırsa hep kapalı |
| **Q9** | Composer paritesi 2b'de yalnız Enter/Shift+Enter/IME/durdur/taslak; markdown araç çubuğu, @-kapsam, /komut, ad tamamlama, model seçici, Derin düşün, Kod hapı **2b dışı** (Faz 3 kararı) | **Evet** — her biri ayrı IA kararı; v1'de yaşamaya devam eder |
| **Q10** | Settings › AiTab'daki salt-okunur `CosreV2StatusLine` yanına **admin toggle** (PUT `/api/admin/cosre-v2`, audit sunucuda; shadcn `checkbox` 1639'da var) B1'de eklensin mi? | **Evet** (bayrağı prep'te çevirmek için curl gerekmesin); "değişiklik yeni yüklemede görünür" notu ile |

Not: `evidence_relevant` +0.10 / `evidence_irrelevant` −0.10 / `note` 0 ağırlıkları
(audit §7-3) **gemide** (`cosre_signal_weights.go`, admin ayarı) — soru kapandı.

## 8. Riskler ve kapsam dışı

**Riskler (ölçülecek / kapılı).**
- Chunk yutma (v0.9.708 sınıfı): `ai` grubu 88'de; `zod`/`swr`/`nanoid`'in eager tüketicisi
  varsa grup regex'inden çıkarılır (`npm ls … --all`); `ai → grafana` statik import kapısı.
- SDK tuhaflıkları (2a'da ölçülü): `onFinish` `message` argümanı eski kopya → kayıt
  `chat.messages`'tan; akışta `setMessages` ezilir → yalnız `visibleParts`; `data-*` aynı id
  yerine geçer (bütçe/blok tasarımı buna dayanır — SDK sürümü tam pin, değişirse test kırmızı).
- Radix non-modal Sheet: `onInteractOutside` varsayılanı dışarı tıkta kapatır → preventDefault
  şart; odak tuzağı yok (kabul, v1 de eşlik kipi); `z-50` ↔ eski z token'ları B1'de ölçülür.
- `inlineStyleRatchet` TOTAL: v2'de tek `style={{` bile kırmızı → genişlik CSS var ile ref'ten.
- `uiImportRatchet`: v2 dosyası ui/'den bir tip bile import edemez (`import type` sayılır) →
  `drawerWidth`/`emptyAreaClose` lib'e taşınır; `AI_DRAWER_WIDTH` `components/ai`'den (serbest).
- jsdom: Radix + `use-stick-to-bottom` `ResizeObserver`/`scrollIntoView` ister → `designAxe`
  stub'ları test yardımcısına çıkarılır (`src/test/domStubs.ts`).
- Metin dili: ai-elements İngilizce sabitleri (tool rozetleri, sources başlığı, prompt
  placeholder) → sarmalayıcı/strings; pin testi render çıktısında tarar.
- Kalıcılık asimetrisi: v2 arşivi `evidence[]`/`explain` yazmaz (2a şekli) → v1'de açılan v2
  thread'inde kanıt çipi yok (bloklar sunucudan gelir). Kabul; P1'den önce parite kararı.
- `?ai=` geçiş dalı (Q1-a) iki çekmece deseni: v2 kendini kapatır, v1 açılır; B7 siler.
- Bayrak FE'ye modül önbelleğiyle gelir → çevrim yenileme ister (Settings notu).

**Kapsam dışı (2b; v1'de yaşar, P1 öncesi parite ya da karar).** `/cosre` sayfa kipi,
TraceKiosk, `CosreSidebar`, `CosreCapabilities`/takım kartı, model seçici, Derin düşün, Kod
hapı (`?aicode=`), @-kapsam/`/komut`/ad tamamlama, markdown araç çubuğu + akıllı yapıştırma +
önizleme, bağlam pini + `ChatContextBar`, `TraceContextStrip`, paylaşım (`/cosre/shared/:id`),
"Tam sayfada aç" (v1 linki kalır), saklama/retention, `/ai` ayrı v2 kırılımı (audit §7-4,
cevaplandı: yok). Faz 3 (P1 bayrak varsayılanı açık, P2 söküm, P3 defter) ayrı plan.

## 9. Operator approval (2026-10-30)

Approved with the plan's recommended defaults: Q1 `?ai=` explain turn stays on v1 until B7 · Q2 /cosre + TraceKiosk stay v1 during 2b · Q3 new dependency `use-stick-to-bottom` approved (exact pin, ≥2 weeks old) plus shadcn `hover-card` + `input-group` · Q4 ai-elements written verbatim from registry JSON (hash-pinned), allowed-edit list only; reasoning / code-block / response NOT copied (no streamdown, no shiki, no motion) · Q5 fixed width in B1, resize handle in B6 · Q6 history inside the Sheet in B6 · Q7 Go `stepId` for evidence↔tool mapping as its own small release · Q8 steps open while streaming, collapsed summary on finish · Q9 composer minimal parity in 2b, rest in Phase 3 · Q10 Settings admin toggle in B1. Checkpoint: stop after B1 and show the operator.
