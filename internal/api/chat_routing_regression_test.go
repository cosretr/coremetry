package api

// chat_routing_regression_test.go — v0.10.1464: CoSRE YÖNLENDİRME REGRESYON
// KÜMESİ (operatör, 2026-10-23: "her yanlış yönlendirme bir ekran görüntüsüyle
// bulunuyor ve tek tek yamanıyor").
//
// Her satır operatörün bildirdiği (ya da bir düzeltmenin bozmaması gereken
// komşu) bir soruyu GERÇEK copilotChat handler'ından geçirir: sohbet geçmişi
// süzgeci, kapsam/çekmece kapısı (chatScopeDrawerGate + withWikiMention),
// hafıza, wiki, operasyon ön-getirmesi (endpoint anması → yol-yalnız →
// dış-yol sondası), guided router (routeGuidedIntentExt), çekmece, RAG, niyet
// sınıflandırıcısı (sahte model) ve serbest döngü — kademe sırası neyse o.
// Turun cosre_interactions satırı (cosreInteractionSeam) KADEMEYİ + rotayı
// söyler; guided rota kararı guidedDispatchSeam'de yakalanır.
//
// Sahteler: model = httptest OpenAI-uyumlu uç (loopLLM / offTopicLLM), katalog
// = önbellek (svcnames/envnames/exthosts), giden çağrı okuyucusu =
// fakeOutboundReader, operasyon kataloğu = opCatalogSeam, wiki = fakeWikiAPI.
// Store YOK: prefetch demetleri (servis sağlığı, db, pod…) koşmaz — dikiş rota
// kararından sonra turu bitirir. outbound_call demeti LLM'siz ve store'suz
// koştuğu için uçtan uca çalışır (passThrough).
//
// Bilinçli sınırlar (her biri kendi testinde):
//   - RAG kademesi etkin değil (s.rag nil): telemetri sorusunun guided'da
//     kalması RAG'ın ÖNÜNDE karar verildiğini kanıtlar (TestChatTierOrderIsPinned).
//   - Katalogda BULUNAN endpoint (operation kademesi) store ister; burada katalog
//     boş, yol-yalnız dal koşar. Katalog dalı: chat_operation_prefetch testleri.
//   - Servis sağlığı grafiği demetin içinde çizilir; "span grafiği yok" bayrağı
//     demetin kaynağını okur (TestServiceHealthBundleChartWiring ile aynı kural).
//   - Sınıflandırıcının KENDİSİ (model off_topic diyecek mi) LLM'e bağlı:
//     o yarı evalset'te (internal/copilot/evalset/intent.json, intent-offtopic-*);
//     burada sahte model off_topic der, test sunucunun ne yaptığını pinler.
//
// ── YENİ YANLIŞ YÖNLENDİRME BİLDİRİLDİĞİNDE (operatörün akışı) ──────────────
//  1. Sorunun METNİNİ (adları sentetikle: orders-api, billing-api,
//     *.example.test; kurum adı YOK) ve bağlamını (servis sayfası? trace
//     çekmecesi?) routingRegressionRows'a bir satır olarak ekle; release
//     alanına düzeltmeyi taşıyacak sürümü yaz, wantTier/wantRoute BEKLENEN
//     (doğru) yönlendirme.
//  2. `go test ./internal/api/ -run TestRoutingRegression -v` — satır kırmızı
//     olmalı (bugünkü yanlış kademe/rota mesajda görünür). Yeşilse bildirilen
//     hata yönlendirme değil (cevap/demet kusuru): satırı yine de bırak.
//  3. Düzelt; satır yeşile döner. Düzeltmenin komşusu olan soruları da ekle
//     (aynı sözcüğü taşıyıp BAŞKA yere gitmesi gerekenler) — kırılmasınlar.
//  4. Sınıflandırıcı (LLM) yarısı ise vakayı evalset intent.json'a da ekle.
//  Haftalık aday listesi: /ai › "Şüpheli yönlendirmeler" (GET /api/ai/routing-report).

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	rrSvc      = "orders-api"
	rrMainHost = "mainframe-gw.example.test"
	rrESBHost  = "esbprod.example.test"
	rrConv     = "conv-routing"
)

// routingRow — tek regresyon satırı.
type routingRow struct {
	release string         // satırı pinleyen sürüm ("komşu" = düzeltmenin bozmaması gereken)
	name    string         // kısa açıklama
	q       string         // kullanıcının sorusu (sentetik adlar)
	ctx     map[string]any // istek bağlamı (servis sayfası, çekmece…); nil = global CoSRE
	setup   func(t *testing.T, s *routingRig)

	wantTier  string // aisurface.Tier*
	wantRoute string // guided rota (guidedIntent); "" = rota YOK (guided'a hiç girmemeli)

	noLLM        bool     // model hiç çağrılmamalı (deterministik cevap)
	noSpanChart  bool     // servis sağlığı: span grafiği çiti yok (v0.10.1453)
	ansPrefix    string   // cevap bu önekle başlamalı
	ansNot       []string // cevapta OLMAMALI (ör. "veri bulunamadı")
	ansHas       []string // v0.10.1468 — cevapta OLMALI (ör. ortam beyanı)
	ansSuffix    string   // v0.10.1485 — cevap bu sonekle bitmeli (genel cevap: kapsam notu en altta)
	generalShape bool     // v0.10.1485 — genel cevap sözleşmesi (assertOffTopicGeneralShape)
}

// routingRig — satırın sahte dünyası.
type routingRig struct {
	offLLM  *offTopicLLM // set edilirse model bu (niyet = off_topic); yoksa noLLM
	cache   map[string][]byte
	outRead *fakeOutboundReader
	excRead *fakeSvcExcReader // v0.10.1486 — service_exceptions demeti store'suz koşar
}

func rrSvcPage() map[string]any {
	return map[string]any{"service": rrSvc, "page": map[string]any{"page": "service", "path": "/services/" + rrSvc, "service": rrSvc}}
}

// rrTraceDrawer — trace çekmecesi bağlamı (AIDrawerBody şekli).
func rrTraceDrawer() map[string]any {
	return map[string]any{
		"explain": "KONU: CoSRE'ye sor · trace 4bf92f35\n**Bulgu** — orders-api p95 1830 ms.",
		"subject": "trace:" + tfTrace, "trace": tfTrace, "service": rrSvc,
		"page": map[string]any{"page": "trace", "path": "/trace", "traceId": tfTrace, "service": rrSvc},
	}
}

// rrScreenEnv — v0.10.1468: ekranda ortam seçili (servis sayfası + env).
func rrScreenEnv(env string) map[string]any {
	m := rrSvcPage()
	m["env"] = env
	return m
}

// withHostCallers — v0.10.1468: dış hostu orders-api çağırıyor (MV), ölçüm boş.
func withHostCallers(t *testing.T, s *routingRig) {
	t.Helper()
	s.outRead = &fakeOutboundReader{hostCallers: func(time.Time, time.Time) []chstore.ExternalCaller {
		return []chstore.ExternalCaller{{Service: rrSvc, Calls: 400, AvgMs: 300}}
	}}
}

// withExcSamples — v0.10.1486: servisin exception grupları örnek trace + stack taşır.
func withExcSamples(t *testing.T, s *routingRig) {
	t.Helper()
	s.excRead = &fakeSvcExcReader{rows: excFixtureRows(), stacks: excFixtureStacks()}
}

func withOutboundHits(t *testing.T, s *routingRig) {
	t.Helper()
	s.outRead = &fakeOutboundReader{mv: func(string, string) ([]chstore.OutboundCaller, error) {
		return []chstore.OutboundCaller{{Service: rrSvc, Host: rrESBHost, Calls: 40}}, nil
	}}
}

// withRoutePrefix — v0.10.1482: önek okuması dikişi; forbid → çağrılırsa hata.
func withRoutePrefix(forbid bool) func(t *testing.T, s *routingRig) {
	return func(t *testing.T, _ *routingRig) {
		t.Helper()
		prev := routePrefixSeam
		routePrefixSeam = func(_ context.Context, _, prefix string, _, _ time.Time, _ int) (chstore.RoutePrefixREDResult, error) {
			if forbid {
				t.Errorf("önek okuması bu soruda koşmamalı (önek %q)", prefix)
			}
			return chstore.RoutePrefixREDResult{
				Rows: []chstore.RoutePrefixRow{{Service: rrSvc, Route: prefix + "/{id}", Calls: 500, Errors: 5, AvgMs: 90, P95Ms: 300}},
				Cur:  chstore.RoutePrefixTotal{Calls: 500, Errors: 5, AvgMs: 90, Routes: 1},
				Prev: chstore.RoutePrefixTotal{Calls: 480, Errors: 4, AvgMs: 88, Routes: 1}}, nil
		}
		t.Cleanup(func() { routePrefixSeam = prev })
	}
}

func withOffTopic(allow bool) func(t *testing.T, s *routingRig) {
	return func(t *testing.T, s *routingRig) {
		setGeneralQuestions(t, allow)
		s.offLLM = newOffTopicLLM(t, "off_topic")
	}
}

// withOffTopicAnswer — v0.10.1485: anahtar açık, model verilen parçaları akıtır.
func withOffTopicAnswer(chunks []string) func(t *testing.T, s *routingRig) {
	return func(t *testing.T, s *routingRig) {
		withOffTopic(true)(t, s)
		s.offLLM.chunks = chunks
	}
}

// routingRegressionRows — KÜME. Yeni satır: dosya başlığındaki akış.
var routingRegressionRows = []routingRow{
	// ── operatör bildirimleri ───────────────────────────────────────────
	{release: "v0.10.1455", name: "mainframe cevap süresi artışı → giden çağrı (1446'da RAG 'bilgi yok' idi)",
		q: rrSvc + " mainframe cevap sürelerimde artış var mı?", setup: withOutboundHits,
		wantTier: aisurface.TierGuided, wantRoute: string(guidedOutboundCall), noLLM: true},
	{release: "v0.10.1454", name: "kalın + '/**' + U+2026 yol → dış-yol sondası → giden çağrı (1447'de serbest döngü 'veri bulunamadı')",
		q: "**/esb/external/…/customer-kvkk/** kısmı için grafikleri ortalamayı gösterebilirsin", ctx: rrSvcPage(), setup: withOutboundHits,
		wantTier: aisurface.TierGuided, wantRoute: string(guidedOutboundCall), noLLM: true,
		ansNot: []string{"veri bulunamadı", "Bu soru telemetriyle eşleşmedi"}},
	{release: "v0.10.1453", name: "servis sağlığı → service_health, grafik yalnız VM",
		q: rrSvc + " sağlığı nasıl?", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceHealth), noLLM: true, noSpanChart: true},
	{release: "v0.10.1458", name: "genel soru, anahtar AÇIK → kısa genel cevap (v0.10.1485: cevap üstte, tek not altta)",
		q: offTopicCapitalQ, setup: withOffTopic(true), wantTier: aisurface.TierIntent, wantRoute: string(guidedOffTopic),
		ansPrefix: offTopicFakeClean, ansSuffix: "\n\n" + offTopicGeneralNoteTR, ansNot: []string{"telemetri", "Şöyle sorabilirsin"}},
	{release: "v0.10.1485", name: "Python palindrom fonksiyonu, anahtar AÇIK → kod çitte, tek not en altta (prod: not iki kez, kod tek satır)",
		q: offTopicPalindromeQ, setup: withOffTopicAnswer(offTopicPalindromeChunks), wantTier: aisurface.TierIntent, wantRoute: string(guidedOffTopic),
		ansPrefix: "Aşağıdaki fonksiyonu kullanabilirsin:\n\n```python\n", ansSuffix: "\n\n" + offTopicGeneralNoteTR, generalShape: true},
	{release: "v0.10.1458", name: "genel soru, anahtar KAPALI → sınır metni, model cevabı yok",
		q: offTopicCapitalQ, setup: withOffTopic(false), wantTier: aisurface.TierIntent, wantRoute: string(guidedOffTopic), ansPrefix: offTopicBoundaryTR},
	{release: "v0.10.1460", name: "@wiki trace çekmecesinde → wiki kapsamı, telemetri değil",
		q: "@wiki DBeaver nasıl kurulur?", ctx: rrTraceDrawer(),
		setup: func(t *testing.T, _ *routingRig) {
			withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{"/Araçlar/DBeaver kurulumu": "# DBeaver\nKurulum: paket yöneticisinden dbeaver-ce kurun."}})
		},
		wantTier: aisurface.TierScope}, // yerel model wiki sayfasını anlatır (localOnly)

	{release: "v0.10.1468", name: "ortam yalnız ekrandan → host özeti MV'de, 'tüm ortamlar' beyanı",
		q: rrESBHost + "'a giden çağrılar yavaşladı mı?", ctx: rrScreenEnv("uat"), setup: withHostCallers,
		wantTier: aisurface.TierGuided, wantRoute: string(guidedOutboundCall), noLLM: true,
		ansHas: []string{"tüm ortamlar (MV)", "ekran ortamı (uat) süzgeci uygulanmadı"}, ansNot: []string{"ham client span okuması"}},
	// ── v0.10.1476: evalset ajan vakalarının PROD rotası ────────────────
	// agent_loop.json vakaları artık döngü sondası (guided'ı atlar); aynı
	// soruların prod'da gittiği deterministik guided rotası burada pinli
	// (prod koşusu 2026-10-23: döngü hiç koşmamıştı). Adlar sentetik.
	{release: "v0.10.1476", name: "evalset agent-payments-error-logs → log_errors",
		q: rrSvc + " servisinde son 30 dakikadaki hata loglarını özetle; en sık hata mesajı ne?", wantTier: aisurface.TierGuided, wantRoute: string(guidedLogErrors)},
	{release: "v0.10.1476", name: "evalset agent-inventory-deploy-impact → deploy_impact",
		q: rrSvc + " servisinde son 24 saatte deploy oldu mu? Deploy sonrasında hata oranı ya da gecikme değişti mi?", wantTier: aisurface.TierGuided, wantRoute: string(guidedDeployImpact)},
	{release: "v0.10.1476", name: "evalset agent-open-problems-checkout → problems",
		q: "Şu an açık problemler neler? Hangisi " + rrSvc + " servisini etkiliyor?", wantTier: aisurface.TierGuided, wantRoute: string(guidedProblems)},
	{release: "v0.10.1476", name: "servisli exception grubu sorusu → service_exceptions",
		q: rrSvc + " servisinde son 1 saatte hangi exception grupları arttı?", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceExceptions)},
	{release: "v0.10.1476", name: "evalset intent-offtopic-negative-errors metni sınıflandırıcıya gitmez → problems",
		q: "son 3 saatte kaç hata oldu?", wantTier: aisurface.TierGuided, wantRoute: string(guidedProblems), noLLM: true},
	{release: "v0.10.1476", name: "evalset intent-find-entity-bare-name metni: çıplak ad → find_entity (deterministik)",
		q: "shop-payment", wantTier: aisurface.TierGuided, wantRoute: string(guidedFindEntity), noLLM: true,
		setup: func(_ *testing.T, s *routingRig) {
			s.cache["copilot:guided:svcnames"] = []byte(`["orders-api","billing-api","shop-payment-api","shop-payment-worker"]`)
		}},

	// ── v0.10.1479: 1476 sondasında görülen iki yanlış yönlendirme ──────
	// (1) exception sorusundaki "trace'i bul" exception niyetini ezip
	// family_traces'e gidiyordu; örnek stack + ilgili trace exception
	// cevabının parçası. (2) "neden yavaşladı, hangi bağımlılık" topolojiye
	// gidiyordu; soru bir kök-neden sorusu (bağımlılık = şüpheli), harita değil.
	{release: "v0.10.1479", name: "exception grupları + örnek stack + ilgili trace → service_exceptions (family_traces değil)",
		q: rrSvc + " exception grupları neler, örnek stack ve ilgili trace'i bul", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceExceptions)},
	// v0.10.1486 — aynı soru artık cevabın SONUNDA grup başına örnek trace
	// linki (/trace?id=…&span=…) ve stack kesiti taşır (sunucu eki).
	{release: "v0.10.1486", name: "exception grupları + örnek stack + trace → cevapta trace linki ve stack kesiti",
		q: rrSvc + " exception grupları neler, örnek stack ve ilgili trace'i bul", setup: withExcSamples,
		wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceExceptions),
		ansHas: []string{"**Örnek trace ve stack**", "(/trace?id=" + excTID1 + "&span=" + excSID1 + ")", "OrderService.reserve (OrderService.java:42)", "örnek yok"}},
	{release: "v0.10.1479", name: "exception'lar + ilgili trace'leri göster → service_exceptions",
		q: rrSvc + " exception'ları neler, ilgili trace'leri göster", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceExceptions)},
	{release: "v0.10.1479", name: "en sık exception + örnek trace → service_exceptions",
		q: rrSvc + " servisinde en sık exception hangisi, örnek trace bul", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceExceptions)},
	{release: "v0.10.1479", name: "neden yavaşladı + hangi bağımlılık → root_cause (service_topology değil)",
		q: rrSvc + " neden yavaşladı, hangi bağımlılık", wantTier: aisurface.TierGuided, wantRoute: string(guidedRootCause)},
	{release: "v0.10.1479", name: "neden yavaş + hangi bağımlılıktan → root_cause",
		q: rrSvc + " neden yavaş, hangi bağımlılıktan kaynaklanıyor?", wantTier: aisurface.TierGuided, wantRoute: string(guidedRootCause)},
	{release: "v0.10.1479", name: "yavaşlamanın sebebi hangi downstream → root_cause",
		q: rrSvc + " yavaşlamasının sebebi hangi downstream?", wantTier: aisurface.TierGuided, wantRoute: string(guidedRootCause)},

	// ── v0.10.1482: önek kipi (sarmalayıcısız "/x/**") ──────────────────
	{release: "v0.10.1482", name: "'/x/** yavaş mı' → önek toplamı (deterministik, operasyon kademesi)",
		q: "/api/orders/** yavaş mı?", setup: withRoutePrefix(false),
		wantTier: aisurface.TierOperation, noLLM: true, ansHas: []string{"/api/orders/** önek toplamı", "p95 yalnız rota başına"}},
	{release: "v0.10.1482", name: "'**/x/**' kalın sarmalayıcı → tam yol akışı, önek DEĞİL",
		q: rrSvc + " **/api/orders/** endpoint'i neden yavaş", setup: withRoutePrefix(true),
		wantTier: aisurface.TierGuided, wantRoute: string(guidedEndpointCandidates), ansNot: []string{"önek toplamı"}},
	{release: "v0.10.1482", name: "'/x/y' tam yol → tam yol akışı, önek DEĞİL",
		q: rrSvc + " /api/orders/list endpoint'i neden yavaş", setup: withRoutePrefix(true),
		wantTier: aisurface.TierGuided, wantRoute: string(guidedEndpointCandidates), ansNot: []string{"önek toplamı"}},

	// ── komşular (düzeltmeler bunları bozmamalı) ────────────────────────
	{release: "komşu v0.10.1479", name: "exception'lı trace'ler (exception trace'i niteliyor) → family_traces",
		q: rrSvc + " exception'lı trace'leri göster", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "komşu v0.10.1479", name: "exception alan trace'ler → family_traces",
		q: rrSvc + " exception alan trace'leri bul", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "komşu v0.10.1479", name: "hatalı trace'leri → family_traces (exception sözcüğü yok)",
		q: rrSvc + " hatalı trace'leri göster", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "komşu v0.10.1479", name: "bağımlılıkları neler → service_topology",
		q: rrSvc + " bağımlılıkları neler", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceTopology)},
	{release: "komşu v0.10.1479", name: "hangi bağımlılıkları var (neden yok) → service_topology",
		q: rrSvc + " hangi bağımlılıkları var?", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceTopology)},
	{release: "komşu v0.10.1479", name: "hatalı bağımlılıkları (neden yok) → service_topology",
		q: rrSvc + " hatalı bağımlılıkları göster", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceTopology)},
	{release: "komşu v0.10.1479", name: "neden yavaş + açık topoloji isteği → service_topology",
		q: rrSvc + " neden yavaş, topolojisini göster", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceTopology)},
	{release: "komşu v0.10.1479", name: "neden yavaşladı (bağımlılık yok) → root_cause",
		q: rrSvc + " neden yavaşladı?", wantTier: aisurface.TierGuided, wantRoute: string(guidedRootCause)},
	{release: "komşu v0.10.1468", name: "ortam soruda (ekran başka) → ham, ortama süzülmüş okuma",
		q: rrESBHost + " prod ortamında gecikme arttı mı", ctx: rrScreenEnv("uat"), setup: withHostCallers,
		wantTier: aisurface.TierGuided, wantRoute: string(guidedOutboundCall), noLLM: true,
		ansHas: []string{"ortam prod", "ham client span okuması"}, ansNot: []string{"süzgeci uygulanmadı", "tüm ortamlar (MV)"}},
	{release: "komşu v0.10.1455", name: "oracle cevap süresi → db_health (giden çağrı değil)",
		q: "oracle cevap süreleri arttı mı", wantTier: aisurface.TierGuided, wantRoute: string(guidedDBHealth)},
	{release: "komşu v0.10.1455", name: "esb gecikme grafiği → giden çağrı (metric_chart değil)",
		q: rrSvc + " esb gecikme grafiği", setup: withOutboundHits,
		wantTier: aisurface.TierGuided, wantRoute: string(guidedOutboundCall), noLLM: true},
	{release: "komşu v0.10.1455", name: "kafka kuyruğu birikmesi → messaging_health",
		q: rrSvc + " kafka kuyruğunda birikme var mı", wantTier: aisurface.TierGuided, wantRoute: string(guidedMessagingHealth)},
	{release: "komşu v0.10.1454", name: "servis + iç endpoint yavaşlığı → endpoint (katalog boş: aday turu)",
		q: rrSvc + " /api/x endpoint'i neden yavaş", wantTier: aisurface.TierGuided, wantRoute: string(guidedEndpointCandidates)},
	{release: "komşu v0.10.1386", name: "grafik fiili → metric_chart",
		q: rrSvc + " latency grafiğini çiz", wantTier: aisurface.TierGuided, wantRoute: string(guidedMetricChart)},
	{release: "komşu v0.10.1431", name: "pod sürekli restart → pod_health",
		q: rrSvc + "-7d9f pod sürekli restart oluyor", wantTier: aisurface.TierGuided, wantRoute: string(guidedPodHealth)},
	{release: "komşu v0.10.1453", name: "servis sayfasında 'durum nasıl' → service_health",
		q: "durum nasıl?", ctx: rrSvcPage(), wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceHealth)},
	{release: "komşu v0.10.1388", name: "topoloji sözcüğü → service_topology",
		q: rrSvc + " topolojisi", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceTopology)},
	// v0.10.1487 — düz trace sorusu (hata/yavaş sözcüğü yok) servisin son
	// trace listesine; eskiden find_entity'nin varlık kartına düşüyordu.
	{release: "v0.10.1487", name: "<svc> trace'leri → family_traces (son trace'ler)",
		q: rrSvc + " trace'leri", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "v0.10.1487", name: "<svc> trace'i bul → family_traces",
		q: rrSvc + " trace'i bul", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "v0.10.1487", name: "<svc> son trace'ler → family_traces",
		q: rrSvc + " son trace'ler", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "v0.10.1487", name: "<svc> trace'lerini göster → family_traces",
		q: rrSvc + " trace'lerini göster", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "v0.10.1487", name: "<svc> servisinin son traceleri (kesmesiz) → family_traces",
		q: rrSvc + " servisinin son traceleri", wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "v0.10.1487", name: "servis sayfasında son trace'ler → family_traces (ekran servisi)",
		q: "son trace'ler", ctx: rrSvcPage(), wantTier: aisurface.TierGuided, wantRoute: string(guidedFamilyTraces)},
	{release: "komşu v0.10.1487", name: "trace sayfasını aç → open_page",
		q: rrSvc + " trace sayfasını aç", wantTier: aisurface.TierGuided, wantRoute: string(guidedOpenPage)},
	{release: "komşu v0.10.1487", name: "çıplak varlık sorusu (nedir) → find_entity",
		q: rrSvc + " nedir", wantTier: aisurface.TierGuided, wantRoute: string(guidedFindEntity)},
	{release: "komşu v0.10.1487", name: "hangi takımın → find_entity",
		q: rrSvc + " hangi takımın", wantTier: aisurface.TierGuided, wantRoute: string(guidedFindEntity)},
	{release: "komşu v0.10.1487", name: "yavaş trace'leri (tek servis) → slow_traces",
		q: rrSvc + " yavaş trace'leri", wantTier: aisurface.TierGuided, wantRoute: string(guidedSlowTraces)},
	{release: "komşu v0.10.1487", name: "trace id'li soru → trace_by_id",
		q: tfTrace + " trace'ini bul", wantTier: aisurface.TierGuided, wantRoute: string(guidedTraceByID)},
	{release: "komşu v0.10.1487", name: "exception grupları + trace (1479 vetosu) → service_exceptions",
		q: rrSvc + " exception grupları ve trace'leri", wantTier: aisurface.TierGuided, wantRoute: string(guidedServiceExceptions)},
	// v0.10.1473 — takım cevabı "Son loglar (takım)" bölümünü my_services /
	// team_services gövdesinde taşır; soru o gövdeye gitmeye devam etmeli.
	{release: "komşu v0.10.1473", name: "takımımın servisleri → my_services (son loglar bölümü bu gövdede)",
		q: "takımımın servisleri nasıl?", wantTier: aisurface.TierGuided, wantRoute: string(guidedMyServices)},
	// v0.10.1569 — operatör-bildirimi: "takımının servisleri nasıl?" wiki'ye
	// gidiyordu; cümle iyelikle başlıyor, adı geçen takım yok → kendi takımı.
	{release: "v0.10.1569", name: "takımının servisleri (iyelikle başlayan) → my_services",
		q: "takımının servisleri nasıl?", wantTier: aisurface.TierGuided, wantRoute: string(guidedMyServices)},
	{release: "v0.10.1569", name: "ekibinin problemleri → my_problems",
		q: "ekibinin açık problemleri neler", wantTier: aisurface.TierGuided, wantRoute: string(guidedMyProblems)},
}

// routingPassThrough — dikişin demeti koşturduğu rotalar (store'suz çalışanlar).
// v0.10.1486 — service_exceptions da: okuyucu dikişi (serviceExcReadFn) sahte,
// anlatım sahte modelden; sunucu eki (örnek trace tablosu) cevapta görünür.
var routingPassThrough = map[guidedIntent]bool{guidedOutboundCall: true, guidedOffTopic: true, guidedServiceExceptions: true}

func TestRoutingRegression(t *testing.T) {
	for _, row := range routingRegressionRows {
		t.Run(row.release+" "+row.name, func(t *testing.T) {
			rig := &routingRig{cache: map[string][]byte{
				"copilot:guided:svcnames":   []byte(`["orders-api","billing-api"]`),
				"copilot:guided:envnames":   []byte(`["prod","uat"]`),
				"copilot:guided:teamcat:v2": []byte(`[]`),
				outboundHostsCacheKey:       []byte(`[{"h":"` + rrMainHost + `","c":["orders-api"],"n":120},{"h":"` + rrESBHost + `","c":["orders-api"],"n":400}]`),
			}}
			rig.outRead = &fakeOutboundReader{}
			rig.excRead = &fakeSvcExcReader{}
			if row.setup != nil {
				row.setup(t, rig)
			}
			installOutboundReader(t, rig.outRead)
			installExcReader(t, rig.excRead)
			prevCat := opCatalogSeam
			opCatalogSeam = func(context.Context, []string, int) ([]chstore.CatalogOperation, error) { return nil, nil }
			t.Cleanup(func() { opCatalogSeam = prevCat })
			var mu sync.Mutex
			var routes []string
			prevSeam := guidedDispatchSeam
			guidedDispatchSeam = func(r guidedRoute) bool {
				mu.Lock()
				routes = append(routes, string(r.Intent))
				mu.Unlock()
				return !routingPassThrough[r.Intent]
			}
			t.Cleanup(func() { guidedDispatchSeam = prevSeam })

			rows := captureCosreRows(t)
			llm := noLLM(t)
			if rig.offLLM != nil {
				llm = &loopLLM{srv: rig.offLLM.srv}
			}
			s, mc := newLoopTestServer(t, llm, "u-route")
			for k, v := range rig.cache {
				mc.m[k] = v
			}
			body := map[string]any{"messages": []map[string]any{{"role": "user", "text": row.q}}}
			ctx := map[string]any{"conversation": rrConv}
			for k, v := range row.ctx {
				ctx[k] = v
			}
			body["context"] = ctx
			fr := postLoopChat(t, s, context.Background(), "u-route", body)

			got := rows()
			if len(got) != 1 {
				t.Fatalf("tur başına tek etkileşim satırı bekleniyordu, %d", len(got))
			}
			r := got[0]
			tier, route, _, _ := aisurface.ParseTurnLabel(r.Surface)
			text := ""
			if a := framesOf(fr, "answer"); len(a) > 0 {
				text, _ = a[len(a)-1]["text"].(string)
			}
			mu.Lock()
			seen := append([]string(nil), routes...)
			mu.Unlock()
			if r.Tier != row.wantTier || route != row.wantRoute {
				t.Fatalf("YANLIŞ YÖNLENDİRME\n  soru:  %q\n  bugün: kademe=%q rota=%q (guided kararları %v)\n  beklenen: kademe=%q rota=%q\n  cevap: %.200s",
					row.q, r.Tier, route, seen, row.wantTier, row.wantRoute, text)
			}
			if tier != r.Tier {
				t.Errorf("etiket kademesi %q ≠ satır kademesi %q", tier, r.Tier)
			}
			if row.wantRoute == "" && len(seen) > 0 {
				t.Errorf("guided'a hiç girmemeliydi, kararlar: %v", seen)
			}
			// Serbest araç döngüsü koşmadı: araç seti yalnız döngüde yazılır.
			if r.Toolset != "" || r.Tier == aisurface.TierLoop {
				t.Errorf("serbest araç döngüsü koştu (toolset=%q)", r.Toolset)
			}
			if r.Status != "ok" {
				t.Errorf("tur sonucu %q (%s)", r.Status, r.ErrorMsg)
			}
			if row.noLLM && (r.ModelCalls > 0 || len(llm.calls()) > 0) {
				t.Errorf("deterministik olmalıydı: %d model çağrısı", r.ModelCalls)
			}
			if row.ansPrefix != "" && !strings.HasPrefix(text, row.ansPrefix) {
				t.Errorf("cevap öneki:\n got %.160q\nwant %.160q…", text, row.ansPrefix)
			}
			if row.ansSuffix != "" && !strings.HasSuffix(text, row.ansSuffix) {
				t.Errorf("cevap soneki:\n got %.400q\nwant …%q", text, row.ansSuffix)
			}
			if row.generalShape {
				assertOffTopicGeneralShape(t, text)
			}
			for _, want := range row.ansHas {
				if !strings.Contains(text, want) {
					t.Errorf("cevapta %q olmalı: %.400s", want, text)
				}
			}
			for _, bad := range row.ansNot {
				if strings.Contains(text, bad) {
					t.Errorf("cevapta %q olmamalı: %.200s", bad, text)
				}
			}
			if strings.Contains(text, "```chart") {
				t.Errorf("cevapta span grafiği çiti var: %.200s", text)
			}
			if row.noSpanChart {
				assertServiceHealthNoSpanChart(t)
			}
			if row.offTopicOff() && len(rig.offLLM.streams()) != 0 {
				t.Error("anahtar kapalıyken genel cevap akışı koşmamalı")
			}
		})
	}
}

// offTopicOff — satır genel-soru anahtarı KAPALI satırı mı (sınır metni).
func (row routingRow) offTopicOff() bool { return row.ansPrefix == offTopicBoundaryTR }

// assertServiceHealthNoSpanChart — v0.10.1453: servis sağlığı demeti span
// grafiği çiti basmaz (yalnız VictoriaMetrics; çizemiyorsa not). Demet store
// ister; kural demetin kaynağında okunur.
func assertServiceHealthNoSpanChart(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) guidedServiceHealthBundle(")
	if i < 0 {
		t.Fatal("guidedServiceHealthBundle bulunamadı")
	}
	j := strings.Index(src[i:], "\n}\n")
	if body := stripGoCommentsAPI(src[i : i+j]); strings.Contains(body, "chartFence(") || !strings.Contains(body, "DropOperationCharts()") {
		t.Error("servis sağlığı demeti span grafiği çizebilir (v0.10.1453: yalnız VictoriaMetrics)")
	}
}

// TestRoutingRegressionRowsWellFormed — kümenin kendi sözleşmesi: her satır
// sürüm atfı taşır, kademe bilinen bir kademe, guided rotası yalnız guided'da.
func TestRoutingRegressionRowsWellFormed(t *testing.T) {
	known := map[string]bool{aisurface.TierScope: true, aisurface.TierWiki: true, aisurface.TierGuided: true,
		aisurface.TierDrawer: true, aisurface.TierIntent: true, aisurface.TierOperation: true, aisurface.TierMemory: true,
		aisurface.TierExplain: true, "rag": true, "doc-rows": true}
	seen := map[string]bool{}
	for _, r := range routingRegressionRows {
		key := r.release + " " + r.name
		if seen[key] {
			t.Errorf("yinelenen satır: %s", key)
		}
		seen[key] = true
		if !strings.Contains(r.release, "v0.10.") || r.name == "" || r.q == "" {
			t.Errorf("satır sürüm atfı / ad / soru taşımalı: %+v", r)
		}
		if !known[r.wantTier] {
			t.Errorf("%s: bilinmeyen kademe %q (serbest döngü beklenen bir yönlendirme değildir)", key, r.wantTier)
		}
		if r.wantRoute != "" && r.wantTier != aisurface.TierGuided && r.wantTier != aisurface.TierIntent {
			t.Errorf("%s: rota yalnız guided / niyet kademesinde anlamlı", key)
		}
	}
}
