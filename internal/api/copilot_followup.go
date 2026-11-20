package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// copilot_followup.go — konuşma bağlamı devralma (v0.9.410, operatör
// istegi: "chat gerçek bir sohbet gibi olsun"). Guided router yalnız
// SON kullanıcı mesajına bakıyordu; "payments nasıl?" → cevap →
// "peki hata oranı?" ikinci soru servissiz kaldığı için serbest tool
// döngüsüne düşüyor ve küçük model (gemma4) orada bocalıyordu.
//
// İki devralma şekli — ikisi de DETERMİNİSTİK (LLM'e sorulmaz):
//   A) Mevcut soru kendi başına YÖNLENEMEZKEN ("peki payments?",
//      "son 24 saatte?") en yakın yönlenebilen önceki kullanıcı
//      mesajının rotası devralınır; mevcut sorudaki servis/env/range
//      varsa üstüne yazılır.
//   B) Mevcut soru yönlenmiş ama konusu BOŞ kalmışken ("peki hata
//      logları?") filo-geneli cevap yerine önceki turun servisi
//      doldurulur — "tüm/filo/all" gibi açık filo-kapsam kelimeleri
//      doldurmayı iptal eder (operatör bilerek genele çıkıyordur).
//
// Saf çekirdek applyFollowUpContext — copilot_followup_test.go ile
// tablo-testli. Şeffaflık: çağıran (copilotChatGuided) devralma
// olduğunda bir `step` chip'i emit eder.

// followUpMaxRunes — bundan uzun mesaj bağımsız bir sorudur, takip
// değil. Kısa tutulmuş ki "peki bu arada başka bir konu..." gibi
// serbest metinler önceki rotayı kaçırmasın.
const followUpMaxRunes = 80

// followUpScanDepth — geriye doğru en fazla bu kadar önceki kullanıcı
// mesajı taranır (rota + servis çözümü katalog × mesaj maliyeti).
const followUpScanDepth = 8

// priorUserTexts — aktif (son) kullanıcı sorusu HARİÇ, yeniden eskiye
// kullanıcı metinleri. Tool-result taşıyan boş-metin user turları
// atlanır (lastUserText ile aynı kural).
func priorUserTexts(msgs []copilot.ChatMessage) []string {
	out := make([]string, 0, followUpScanDepth)
	seenLast := false
	for i := len(msgs) - 1; i >= 0 && len(out) < followUpScanDepth; i-- {
		if msgs[i].Role != "user" || strings.TrimSpace(msgs[i].Text) == "" {
			continue
		}
		if !seenLast {
			seenLast = true
			continue
		}
		out = append(out, msgs[i].Text)
	}
	return out
}

// isFollowUpCue — normalize edilmiş mesaj bir takip sorusu ipucu
// taşıyor mu? Bilerek dar: bağlaç şekilleri ("peki", "aynı", iyelik
// zamirleri) ya da açık range ("son 24 saatte?"). Tek başına "bu"/
// "o"/"ya" YETERLİ DEĞİL — "peki bu ne demek?" gibi doküman/RAG
// sorularını kaçırmamak devralmaktan iyidir (deterministic beats
// clever). Uzun mesaj hiç takip sayılmaz.
func isFollowUpCue(norm string) bool {
	if utf8.RuneCountInString(norm) > followUpMaxRunes {
		return false
	}
	toks := guidedTokens(norm)
	if tokenHasPrefix(toks, "peki", "aynı", "ayni", "tekrar", "again", "onun", "bunun", "onu", "bunu") {
		return true
	}
	_, explicit := guidedRangeSExplicit(norm)
	return explicit
}

// wantsFleetScope — operatör açıkça filo genelini istiyor; servis
// doldurma (şekil B) iptal.
func wantsFleetScope(toks []string) bool {
	return tokenHasPrefix(toks, "tüm", "tum", "bütün", "butun", "hepsi", "filo", "fleet", "all", "genel", "her")
}

// followUpFillable — konusu boş kaldığında önceki turun servisiyle
// doldurulabilen (aksi halde sessizce filo-geneli cevaplayan)
// intent'ler. serviceHealth servissiz zaten yönlenemez; my* kimlikten,
// familyHealth aile listesinden gelir — üçü de doldurulmaz.
var followUpFillable = map[guidedIntent]bool{
	guidedProblems:     true,
	guidedSlowTraces:   true,
	guidedDeployImpact: true,
	guidedLogErrors:    true,
	guidedPodHealth:    true,
	guidedLogField:     true, // v0.10.433 (D5)
	guidedOpenPage:     true, // v0.10.434 (D7b)
	guidedTraceSearch:  true, // v0.10.436 (D2b)
	guidedCallPeriod:   true, // v0.10.438 (D3)
}

// guidedSuggestions (v0.9.411) — cevap sonrası konuya-duyarlı takip
// önerileri. Frontend'in statik FOLLOWUPS çipleri her cevapta aynıydı;
// bunlar rotadan türetilir ("payments nasıl?" → "payments hata
// logları?"). HER öneri guided router'da kendi başına yönlenen bir
// şekildedir (testte pinli) — çipe tıklamak asla serbest döngüye
// düşürmez. Deterministik; LLM'e sorulmaz.
// v0.10.1249 (operatör): "Takımımın açık problemleri?" → "Takımımın
// exception'ları?" (takım süzgeçli Exceptions linki + sayı); filo geneli
// "Son 1 saatteki log hataları?" önerilerden çıktı — çok servisli kurulumda
// anlamlı değil (soru elle yazılırsa yine çalışır).
func guidedSuggestions(route guidedRoute) []string {
	svc := route.Service
	// v0.9.1134 — "hangi takım?" turu HER ŞEYDEN önce: bundle takım
	// listesini rotaya yazdıysa (kimlik/takım çözülemedi) çipler o
	// takımların ÇIPLAK adları olur. Çipe tıklamak o adı yeni bir mesaj
	// olarak gönderir ve router çıplak adı guidedTeamServices'e yönlendirir
	// — diyalog sunucuda durum tutmadan kapanıyor. Intent'e bakılmaz:
	// my_services/my_problems/my_exceptions üçü de aynı degrade'i paylaşıyor.
	if len(route.TeamOptions) > 0 {
		return route.TeamOptions
	}
	// v0.10.429 (D1) — "hangi servis?" turu: çip = adayın TAM kılavuz cümlesi
	// (askServiceChip), sorulan niyetle; çıplak ad kapıdan geçmezdi.
	if len(route.ServiceOptions) > 0 {
		return askServiceChipFor(route) // v0.10.436 (D2) — çift/arama çipleri diğer yarıyı taşır
	}
	switch route.Intent {
	case guidedEndpointCandidates: // v0.10.688 — onay + aday çipleri (deterministik yönlenir)
		if len(route.EndpointOptions) == 0 {
			return []string{"Son 6 saate genişlet", "Son 24 saate genişlet"}
		}
		return endpointCandidateChips(route)
	case guidedEndpointTraces: // v0.10.688 — kipler (chat_followups.go)
		if route.TraceErrorsOnly {
			return []string{"Son 6 saate genişlet", "Son 24 saate genişlet"}
		}
		return []string{"Sadece hatalı olanlar", "Son 6 saate genişlet", "Son 24 saate genişlet"}
	case guidedServiceHealth:
		return []string{
			svc + " en yavaş trace'ler?",
			svc + " hata logları?",
			svc + " son deploy etkisi?",
			svc + " pod'ları nasıl?",
			serviceReportChip(svc), // v0.10.1337 (D7) — durumdan tam rapora tek tık
		}
	case guidedServiceReport: // v0.10.1337 — rapordan sonraki üç doğal derinleşme
		if svc != "" {
			return []string{svc + " neden yavaş?", svc + " teknik hataları?", svc + " hata logları?"}
		}
		return nil
	case guidedProblems:
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " hata logları?", svc + " en yavaş trace'ler?"}
		}
		return []string{"Takımımın exception'ları?", "Takımımın en hatalı servisleri?", "Takımımın servisleri nasıl?"}
	case guidedSlowTraces:
		if svc != "" {
			// v0.10.1327 — "NEREDE": en yavaş trace'in hedefi (kırılım + o trace'in logları).
			return []string{svc + " en yavaş trace'i nereye gidiyor?", svc + " sağlığı nasıl?", svc + " hata logları?", svc + " son deploy etkisi?"}
		}
		return []string{"Açık problemler?", "Takımımın servisleri nasıl?"}
	case guidedRootCause: // v0.10.1327 — kök-neden sonrası "nerede" adımı
		if svc != "" {
			// v0.10.1337 (D7) — kök-neden cevabından tam rapora tek tık.
			return []string{svc + " en yavaş trace'i nereye gidiyor?", svc + " hata logları?", svc + " son deploy etkisi?", serviceReportChip(svc)}
		}
		return nil
	case guidedTraceDrill: // v0.10.1327
		if svc != "" {
			return []string{svc + " hata logları?", svc + " sağlığı nasıl?", svc + " son deploy etkisi?"}
		}
		return nil
	case guidedDeployImpact:
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?", svc + " hata logları?"}
		}
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
	case guidedLogErrors:
		if svc != "" {
			return []string{svc + " teknik hataları?", svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?"}
		}
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
	case guidedLogField: // v0.10.433 (D5)
		if svc != "" {
			return []string{svc + " hata logları?", svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?"}
		}
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
	case guidedNamespaceServices: // v0.10.470 (F2-3) — handler kendi çiplerini basar; burada güvenli varsayılan
		return []string{"Namespace'leri listele", "Açık problemler?"}
	case guidedFamilyTraces: // v0.10.465 (D2) — üye başına drill-down (≤3 üye)
		var out []string
		for i, m := range route.Family {
			if i >= 3 {
				break
			}
			if route.TraceErrorsOnly {
				out = append(out, m+" hata logları?")
			} else {
				out = append(out, m+" sağlığı nasıl?")
			}
		}
		if len(out) == 0 {
			return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
		}
		return out
	case guidedFindEntity: // v0.10.463 (D1) — kart: eylem çipleri; liste: bulunan adlar (çıplak ad → kart)
		if route.FindList {
			if len(route.TeamServices) > 0 {
				return route.TeamServices
			}
			return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
		}
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?", svc + " hata logları?", svc + " sayfasını aç"}
		}
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
	case guidedOpenPage: // v0.10.434 (D7b)
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " teknik hataları?", svc + " hata logları?"}
		}
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
	case guidedPairRequests: // v0.10.436 (D2a)
		out := []string{route.PairFrom + " sağlığı nasıl?", route.PairFrom + " en yavaş trace'ler?"}
		if route.PairToKind == "service" && route.PairTo != "" {
			out = append(out, route.PairTo+" sağlığı nasıl?")
		}
		return out
	case guidedTraceSearch: // v0.10.436 (D2b)
		if svc != "" {
			return []string{svc + " en yavaş trace'ler?", svc + " hata logları?", svc + " sağlığı nasıl?"}
		}
		return []string{"Takımımın en hatalı servisleri?", "Açık problemler?"}
	case guidedWindowCompare: // v0.10.437 (D6)
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?", svc + " son deploy etkisi?"}
		}
		return []string{"Açık problemler?"}
	case guidedCallPeriod: // v0.10.438 (D3)
		if route.PairFrom != "" && route.PairTo != "" {
			return []string{route.PairFrom + "'dan " + route.PairTo + "'ye giden istekler", route.PairFrom + " sağlığı nasıl?"}
		}
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?"}
		}
		return []string{"Açık problemler?"}
	case guidedFanout: // v0.10.439 (D4)
		return []string{route.PairFrom + "'dan " + route.PairTo + "'ye giden istekler", route.PairTo + " sağlığı nasıl?", route.PairTo + " en yavaş trace'ler?"}
	case guidedFamilyHealth:
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?", "Takımımın exception'ları?"}
	case guidedMyServices:
		// v0.9.651 (operatör: "takımıma ait servisleri listeledikten
		// sonra SEÇECEĞİ servisle ilgili hatalar / logları / en yavaş
		// trace'leri") — çipler artık takımın GERÇEK servislerini
		// adlandırıyor.
		//
		// Öncesi jenerikti ("Açık problemler?") ve operatör bir servis
		// seçemiyordu: cevap servisleri sayıyor, çipler onlara
		// dokunmuyordu. Servis-kapsamlı çipler (svc + " hata logları?",
		// " en yavaş trace'ler?") ZATEN vardı — eksik olan tek halka
		// buydu.
		//
		// "sağlığı nasıl?" seçildi çünkü TEK tık ile service_health'e
		// giriyor ve ORASI dört drill-down'un hepsini açıyor (yavaş
		// trace, hata logları, deploy etkisi, pod'lar). Servis başına
		// iki ayrı çip koymak listeyi altıya çıkarır ve v0.9.579'da
		// kaldırılan "menü" hissini geri getirirdi.
		//
		// ÜÇ servisle sınırlı: takım 100 servis taşıyabiliyor.
		if n := len(route.TeamServices); n > 0 {
			out := make([]string, 0, 4)
			for i, sv := range route.TeamServices {
				if i >= 3 {
					break
				}
				out = append(out, sv+" sağlığı nasıl?")
			}
			return append(out, "Takımımın exception'ları?")
		}
		return []string{"Takımımın exception'ları?", "Takımımın en hatalı servisleri?"}
	case guidedMyProblems:
		return []string{"Takımımın servisleri nasıl?", "Takımımın exception'ları?", "Takımımın en hatalı servisleri?"}
	// v0.9.1134 — takım listesinden sonraki doğal adım EN KÖTÜ servise
	// inmek. TeamServices hata oranına göre sıralı geliyor, yani [0] en
	// kötü; my_services'in v0.9.651 dersini izliyor (jenerik çip operatörü
	// bir servis SEÇEMEZ hâlde bırakıyordu).
	case guidedTeamServices:
		if len(route.TeamServices) > 0 {
			worst := route.TeamServices[0]
			out := []string{worst + " sağlığı nasıl?", worst + " teknik hataları?", worst + " en yavaş trace'ler?"}
			if len(route.TeamServices) > 1 {
				out = append(out, route.TeamServices[1]+" sağlığı nasıl?")
			}
			return out
		}
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?"}
	// v0.9.650 — exception cevabından sonraki doğal adımlar: aynı takımın
	// açık PROBLEM'leri (farklı yüzey, aynı kapsam) ve servis sağlığı.
	case guidedMyExceptions:
		// v0.10.1385 — en son görülen grupların çip cümleleri önce (eski
		// istemci de gruba inebilsin), sonra takımın diğer yüzeyleri.
		return append(teamExceptionSuggestionTexts(route.TeamFollowups, 2), "Takımımın servisleri nasıl?", "Takımımın açık problemleri?")
	case guidedTeamExceptions: // v0.10.1385
		out := teamExceptionSuggestionTexts(route.TeamFollowups, 2)
		if t := strings.TrimSpace(route.Team); t != "" {
			out = append(out, t+" takımının servisleri nasıl?")
		}
		return out
	case guidedServiceExceptions:
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " hata logları?", svc + " en yavaş trace'ler?"}
		}
		return nil
	case guidedTopErrorServices, guidedMyTopErrors, guidedTeamTopErrors: // v0.10.1256 — ikinci adım: operasyonlar (v0.10.1402: adı geçen takım)
		if out := topErrorFollowups(route.TeamServices); len(out) > 0 {
			return out
		}
		if t := strings.TrimSpace(route.Team); route.Intent == guidedTeamTopErrors && t != "" {
			return []string{t + " takımının exception'ları?"} // v0.10.1402 — aynı takımda kal
		}
		return []string{"Takımımın exception'ları?"}
	case guidedTopErrorOps:
		if svc != "" {
			return []string{svc + " teknik hataları?", svc + " hata logları?", svc + " sağlığı nasıl?"}
		}
		return nil
	case guidedPodHealth:
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " hata logları?", svc + " son deploy etkisi?"}
		}
		return []string{"Açık problemler?", "Takımımın servisleri nasıl?"}
	case guidedShiftSummary:
		return []string{"Açık problemler?", "Takımımın en hatalı servisleri?", "Takımımın servisleri nasıl?"}
	// v0.9.1142 — kimlik çözüldüyse doğal adım O SERVİSE inmek. Servis
	// rotaya bundle tarafından yazılıyor (log kaydının servisi); yazılmadıysa
	// (kimlik bulunamadı) jenerik çip vermiyoruz — cevabın konusu tek bir
	// istek, filo geneli bir öneri konuyu dağıtırdı.
	case guidedRequestID:
		if svc != "" {
			return []string{svc + " sağlığı nasıl?", svc + " hata logları?", svc + " en yavaş trace'ler?"}
		}
		return nil
	case guidedDBHealth:
		return []string{"Takımımın en hatalı servisleri?", "Açık problemler?", "Takımımın exception'ları?"}
	case guidedMessagingHealth:
		return []string{"Takımımın en hatalı servisleri?", "Açık problemler?"}
	case guidedMetricChart: // v0.10.1386 (copilot_metric_chart.go)
		return metricChartSuggestions(route)
	case guidedResourceInvestigation: // v0.10.1431 (copilot_resource_investigation.go)
		return resourceSuggestions(route)
	case guidedServiceTopology, guidedTraceTopology: // v0.10.1388 (copilot_service_topology.go)
		return topologySuggestions(route)
	case guidedOutboundCall: // v0.10.1454 (copilot_outbound_call.go)
		return outboundSuggestions(route)
	}
	return nil
}

// ─── v0.10.1383 — servis cevabı sonrası yapısal takip adımları ─────────
//
// Operatör (2026-10-19): "Servis sonrasında operationlar ve exceptionlar
// göster ister misin gibi soru sorabilir veya takım ismi verince onunla
// ilgili daha fazla detay verebilir."
//
// guidedSuggestions düz metin çip listesi (eski istemci) olarak AYNEN kalır;
// guidedFollowups aynı cevaba answer.followups olarak KISA ETİKETLİ, servis
// kapsamlı adımlar ekler (FE önce followups'ı çizer). Label operatörün
// gördüğü kısa soru ("Operasyonları göster"), Text router'a giden TAM cümle
// ("<svc> en hatalı operasyonları?") — her Text guided router'da kendi
// başına PİNLİ niyete yönlenir (TestFollowupTemplatesRouteGuided): çipe
// tıklamak asla serbest döngüye düşmez, LLM'e sorulmaz.

// Servis-adımı türleri (chatFollowup.Kind) — v0.10.1383.
const (
	fuKindOperations = "operations"
	fuKindExceptions = "exceptions"
	fuKindReport     = "report"
)

// guidedFollowupMax — v0.10.1383: takip adımı tavanı (çip şeridi tek satır).
const guidedFollowupMax = 4

// serviceStepTemplate — v0.10.1383 SAF: tür → (kısa etiket, tam cümle).
func serviceStepTemplate(kind, svc string) (label, text string) {
	switch kind {
	case fuKindOperations:
		return "Operasyonları göster", svc + " en hatalı operasyonları?"
	case fuKindExceptions:
		return "Exception'ları göster", svc + " teknik hataları?"
	case fuKindSlow:
		return "Yavaş trace'ler", svc + " en yavaş trace'ler?"
	case fuKindDeploy:
		return "Son deploy'dan beri ne değişti?", svc + " son deploy etkisi?"
	case fuKindReport:
		return "Tam rapor", serviceReportChip(svc)
	}
	return "", ""
}

// serviceStepOrder — v0.10.1383: adım sırası. Operasyon + exception önce
// (operatörün adıyla istediği iki adım); "Tam rapor" her zaman SON aday ve
// tavana takılmaz (aşağıda ayrı eklenir) — v0.10.1337 D7 "durumdan tam
// rapora tek tık" korunur.
var serviceStepOrder = []string{fuKindOperations, fuKindExceptions, fuKindSlow, fuKindDeploy}

// serviceStepCovered — v0.10.1383: adım sunulan servis-kapsamlı niyetler ve
// her birinin ZATEN cevapladığı türler (aynı adımı tekrar önermek çıkmaz).
// Listede olmayan niyet servis adımı almaz (filo, takım, aile, çift…).
var serviceStepCovered = map[guidedIntent][]string{
	guidedServiceHealth:     nil,
	guidedRootCause:         nil,
	guidedProblems:          nil,
	guidedLogErrors:         nil,
	guidedPodHealth:         nil,
	guidedSlowTraces:        {fuKindSlow},
	guidedTraceDrill:        {fuKindSlow},
	guidedDeployImpact:      {fuKindDeploy},
	guidedServiceExceptions: {fuKindExceptions},
	guidedTopErrorOps:       {fuKindOperations},
	// Rapor operasyonları ve teknik hataları zaten içeriyor.
	guidedServiceReport: {fuKindOperations, fuKindExceptions, fuKindReport},
}

// guidedFollowups — v0.10.1383 SAF: guided cevabın yapısal takip adımları.
// asked = konuşmadaki kullanıcı metinleri (chatUserTexts); sorulmuş ya da
// ≥0.8 benzer adım düşer ve sıradaki aday dener (followupChips kuralı).
// Netleştirme turu (TeamOptions/ServiceOptions) ve konusuz rota → nil (FE
// suggestions'a / statik listeye düşer). Takım rotaları bundle'ın yazdığı
// route.TeamFollowups'ı taşır (copilot_team_summary.go).
func guidedFollowups(route guidedRoute, asked []string) []chatFollowup {
	if len(route.TeamOptions) > 0 || len(route.ServiceOptions) > 0 {
		return nil
	}
	switch route.Intent { // v0.10.1385 — takım exception rotaları grup çiplerini taşır
	case guidedTeamServices, guidedMyServices: // v0.10.1397 — servis başına "hangi servis?" çipleri + özet adımları
		return dedupFollowupsAsked(route.TeamFollowups, asked, teamOverviewFollowupMax)
	case guidedMyExceptions, guidedTeamExceptions:
		return dedupFollowupsAsked(route.TeamFollowups, asked, guidedFollowupMax)
	case guidedMetricChart: // v0.10.1386 — diğer ortam · 24 sa · yavaş trace'ler · Metrics'te aç
		return metricChartFollowups(route, asked)
	case guidedResourceInvestigation: // v0.10.1431 — hata logları · son deploy · GC/heap'e bak (v0.10.1432: jvm ailesi) · diğer pod'lar
		return resourceFollowups(route, asked)
	case guidedServiceTopology, guidedTraceTopology: // v0.10.1388 — hop · örnek trace · hatalı bağımlılık · komşu
		return topologyFollowups(route, asked)
	case guidedOutboundCall: // v0.10.1454 — son 6 sa · hatalı çağrıların trace'leri · host'un yolları
		return outboundFollowups(route, asked)
	}
	covered, ok := serviceStepCovered[route.Intent]
	svc := strings.TrimSpace(route.Service)
	if !ok || !fuServiceRe.MatchString(svc) {
		return nil
	}
	skip := map[string]bool{}
	for _, k := range covered {
		skip[k] = true
	}
	var cands []chatFollowup
	for _, k := range serviceStepOrder {
		if !skip[k] {
			cands = append(cands, serviceStepFollowup(k, svc, route.Env))
		}
	}
	out := dedupFollowupsAsked(cands, asked, guidedFollowupMax-1)
	if !skip[fuKindReport] {
		out = append(out, dedupFollowupsAsked([]chatFollowup{serviceStepFollowup(fuKindReport, svc, route.Env)}, asked, 1)...)
	}
	return out
}

// serviceStepFollowup — v0.10.1383 SAF: tek servis adımı (servis + env kapsamlı).
func serviceStepFollowup(kind, svc, env string) chatFollowup {
	label, text := serviceStepTemplate(kind, svc)
	return chatFollowup{Text: text, Label: label, Kind: kind, Scope: &chatScope{Services: []string{svc}, Env: env}}
}

// dedupFollowupsAsked — v0.10.1383 SAF: sorulmuş metinle normalize-eşit ya
// da token Jaccard ≥0.8 olan ve kendi arasında tekrar eden çipleri düşürür;
// en çok max.
func dedupFollowupsAsked(cands []chatFollowup, asked []string, max int) []chatFollowup {
	var askedNorm []string
	var askedToks []map[string]bool
	for _, q := range asked {
		if strings.TrimSpace(q) == "" {
			continue
		}
		askedNorm = append(askedNorm, normalizeGuidedMsg(q))
		askedToks = append(askedToks, followupTokens(q))
	}
	seen := map[string]bool{}
	var out []chatFollowup
	for _, c := range cands {
		if len(out) >= max {
			break
		}
		n := normalizeGuidedMsg(c.Text)
		if n == "" || seen[n] {
			continue
		}
		dup := false
		toks := followupTokens(c.Text)
		for i := range askedNorm {
			if askedNorm[i] == n || followupJaccard(toks, askedToks[i]) >= followupDedupJaccard {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		seen[n] = true
		out = append(out, c)
	}
	return out
}

// withGuidedFollowups — v0.10.1383: guided answer olayına followups ekler;
// suggestions'a DOKUNMAZ (eski istemci guidedSuggestions'ı aynen görür).
// Çip yoksa olay bayt bayt eski.
func withGuidedFollowups(ans map[string]any, chips []chatFollowup) map[string]any {
	if len(chips) > 0 {
		ans["followups"] = chips
	}
	return ans
}

// guidedAnswerLink — cevap altındaki derin-link çipi (v0.9.419).
type guidedAnswerLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
	// ID (v0.10.35) — linkin türediği HAM kimlik; yalnız kimlik-avlayan
	// üreticiler dolduruyor (request_id). Arayüz bunu cevap METNİNDE
	// bulup satır içi link olarak sarıyor.
	//
	// ⚠ Neden sunucudan: hangi kimliğin linklenebilir olduğuna karar
	// veren mantık burada (anahtar-kelime kapısı, şablon geçerliliği,
	// env seçimi). İstemcide naif bir regex'le yeniden avlamak, aynı
	// kararı iki yerde vermek olurdu ve ikisi sessizce ayrışırdı.
	ID string `json:"id,omitempty"`
}

// guidedAnswerLinks (v0.9.419, CoSRE fikir #4) — cevabın konusuna giden
// deterministik uygulama-içi linkler. LLM çıktısından DEĞİL rotadan
// üretilir (gemma4'e link biçimletmeyiz); frontend çip olarak çizer ve
// SPA navigate eder. Saf — copilot_followup_test.go.
//
// v0.9.1321 (§3.1 K6) — pencere ZORUNLU argüman. Aşağıdaki 25 href
// satırından yalnız biri (request-ID log köprüsü) pencere yazıyordu;
// geri kalanı operatörü cevabın konuştuğu ana değil sticky penceresine
// götürüyordu. Burada `win linkWindow` bir parametre çünkü bunu bir
// grep-kapısıyla korumak, 26'ncı satırı yazan kişinin kuralı
// hatırlamasına bağlı olurdu; imza ise derlemeyi durdurur. Pencere
// TEK ÇIKIŞTA uygulanır (applyAll), yani yeni bir case eklemek onu
// düşüremez.
func guidedAnswerLinks(route guidedRoute, win linkWindow) []guidedAnswerLink {
	return win.applyAll(guidedAnswerLinkTargets(route))
}

// guidedAnswerLinkTargets — penceresiz HAM hedefler. guidedAnswerLinks
// dışından çağrılmamalı; link_window_test.go tek-çağıran sözleşmesini
// pinler (imzanın ifade edemediği tek şey bu).
func guidedAnswerLinkTargets(route guidedRoute) []guidedAnswerLink {
	svc := route.Service
	svcQ := url.QueryEscape(svc)
	switch route.Intent {
	case guidedMetricChart: // v0.10.1386 — PromQL editörü (mutlak pencereli) + Infrastructure
		return metricChartLinks(route)
	case guidedResourceInvestigation: // v0.10.1431 — Thanos editörü · Pod sayfası · Infrastructure
		return resourceLinks(route)
	case guidedServiceTopology, guidedTraceTopology: // v0.10.1388 — Topology sekmesi · Service Map · Overview (trace önce)
		return topologyLinks(route)
	case guidedOutboundCall: // v0.10.1454 — Traces (yol araması) · External host · örnek trace'ler
		return outboundLinks(route)
	case guidedServiceHealth:
		// Sağlık özeti (2026-10-15, operatör: "kullanıcıyı yönlendirsin") —
		// hükmün kanıt sayfaları tek tık; otomatik geçiş YOK.
		return []guidedAnswerLink{
			{Label: svc + " · Overview", Href: "/service?name=" + svcQ},
			{Label: "Hatalı trace'ler", Href: "/traces?service=" + svcQ + "&hasError=true"},
			{Label: "Loglar (error)", Href: "/logs?service=" + svcQ + "&severity=17"},
		}
	case guidedProblems:
		if svc != "" {
			return []guidedAnswerLink{
				{Label: "Problemler", Href: "/problems?service=" + svcQ},
				{Label: svc + " · Overview", Href: "/service?name=" + svcQ},
			}
		}
		return []guidedAnswerLink{{Label: "Problemler", Href: "/problems"}}
	case guidedSlowTraces, guidedTraceDrill: // v0.10.1327 — kırılım aynı listeden
		if svc != "" {
			return []guidedAnswerLink{{Label: "Trace'ler (en yavaş)", Href: "/traces?service=" + svcQ + "&sort=duration"}}
		}
		return []guidedAnswerLink{{Label: "Trace'ler (en yavaş)", Href: "/traces?sort=duration"}}
	case guidedDeployImpact:
		if svc != "" {
			return []guidedAnswerLink{{Label: svc + " · Overview", Href: "/service?name=" + svcQ}}
		}
		return nil
	case guidedLogErrors:
		// v0.9.1130 — param adı `severity` (logsUrl.ts:58 okuyucusu).
		// Eski ad /logs'un HİÇ okumadığı bir paramdı → çip "error
		// logları" vaat edip tüm seviyeleri açıyordu (K4 ölü-param
		// sınıfı; kaynak-pin testi yasak adı adıyla arar, o yüzden
		// burada anılmıyor).
		if svc != "" {
			return []guidedAnswerLink{{Label: "Loglar (error)", Href: "/logs?service=" + svcQ + "&severity=17"}}
		}
		return []guidedAnswerLink{{Label: "Loglar (error)", Href: "/logs?severity=17"}}
	case guidedEndpointCandidates, guidedEndpointTraces: // v0.10.688 — aynı süzgeçle /traces
		return []guidedAnswerLink{{Label: "Trace'ler (" + route.SearchText + ")", Href: endpointTracesHref(route.SearchText, route.TraceErrorsOnly)}}
	case guidedNamespaceServices: // v0.10.470 (F2-3) — handler linkleri kendisi kurar
		return []guidedAnswerLink{{Label: "Clusters", Href: "/clusters"}}
	case guidedFamilyTraces: // v0.10.465 (D2) — aynı süzgeçle /traces
		label := "Trace'ler (en yavaş)"
		mode := familyTraceMode(route)
		switch mode {
		case familyModeErrors:
			label = "Trace'ler (hatalı)"
		case familyModeRecent: // v0.10.1487
			label = "Trace'ler (en yeni)"
		}
		out := []guidedAnswerLink{{Label: label, Href: familyTracesHref(route.Family, mode)}}
		for i, m := range route.Family {
			if i >= 2 {
				break
			}
			out = append(out, guidedAnswerLink{Label: m + " · Overview", Href: "/service?name=" + url.QueryEscape(m)})
		}
		return out
	case guidedFindEntity: // v0.10.463 (D1)
		if route.FindList {
			if q := strings.TrimSpace(route.FindQuery); q != "" { // v0.10.485 — aile listesi aynı arama ile
				return []guidedAnswerLink{{Label: "Servisler · " + q, Href: "/services?q=" + url.QueryEscape(strings.Fields(q)[0])}}
			}
			return []guidedAnswerLink{{Label: "Servisler", Href: "/services"}}
		}
		if svc == "" {
			return nil
		}
		return []guidedAnswerLink{
			{Label: svc + " · Overview", Href: "/service?name=" + svcQ},
			{Label: "Trace'ler", Href: "/traces?service=" + svcQ},
			{Label: "Loglar", Href: "/logs?service=" + svcQ},
		}
	case guidedOpenPage: // v0.10.434 (D7b) — sayfa türüne göre; overview özne ister
		withSvc := func(base string) string {
			if svc != "" {
				return base + "?service=" + svcQ
			}
			return base
		}
		switch route.Page {
		case "problems":
			return []guidedAnswerLink{{Label: "Problemler", Href: withSvc("/problems")}}
		case "logs":
			return []guidedAnswerLink{{Label: "Loglar", Href: withSvc("/logs")}}
		case "traces":
			return []guidedAnswerLink{{Label: "Trace'ler", Href: withSvc("/traces")}}
		case "endpoints":
			return []guidedAnswerLink{{Label: "Endpoint'ler", Href: withSvc("/endpoints")}}
		}
		if svc == "" {
			return nil
		}
		return []guidedAnswerLink{{Label: svc + " · Overview", Href: "/service?name=" + svcQ}}
	case guidedPairRequests: // v0.10.436 (D2a) — eş-görünüm linki (etiket "birlikte içeren")
		a, b := url.QueryEscape(route.PairFrom), url.QueryEscape(route.PairTo)
		if route.PairToKind == "service" {
			return []guidedAnswerLink{
				{Label: "Trace'ler (" + route.PairFrom + " ve " + route.PairTo + "'ı birlikte içeren)", Href: "/traces?services=" + a + "," + b + "&view=list&rootOnly=false"},
				{Label: "Servis haritası · " + route.PairFrom, Href: "/service-map?focus=" + a},
			}
		}
		return []guidedAnswerLink{
			{Label: "Trace'ler (" + route.PairFrom + ", " + route.PairTo + ")", Href: "/traces?service=" + a + "&search=" + b},
			{Label: "Servis haritası · " + route.PairFrom, Href: "/service-map?focus=" + a},
		}
	case guidedTraceSearch: // v0.10.436 (D2b)
		href := "/traces?search=" + url.QueryEscape(route.SearchText)
		if len(route.SearchKeys) > 0 { // v0.10.476 (F3-5) — anahtar çözüldü: tam eşitlik çipi (ilk anahtar)
			fe, _ := json.Marshal([]chstore.FilterExpr{{Key: route.SearchKeys[0], Op: "=", Values: []string{route.SearchText}}})
			href = "/traces?filters=" + url.QueryEscape(string(fe))
		}
		if len(route.SearchKeys) > 0 && len(route.OracleCodes) > 0 { // v0.10.1133 — Oracle operasyon adı → kod
			fe, _ := json.Marshal([]chstore.FilterExpr{oracleCodeFilter(route.SearchKeys[0], route.OracleCodes)})
			href = "/traces?filters=" + url.QueryEscape(string(fe))
		}
		if route.SearchSQL {
			fe, _ := json.Marshal([]chstore.FilterExpr{{Key: "db.statement", Op: "LIKE", Values: []string{route.SearchText}}})
			href = "/traces?filters=" + url.QueryEscape(string(fe))
		}
		if svc != "" {
			href += "&service=" + svcQ
		}
		return []guidedAnswerLink{{Label: "Trace'ler (arama)", Href: href}}
	case guidedFanout: // v0.10.439 (D4)
		a, b, c := url.QueryEscape(route.PairFrom), url.QueryEscape(route.PairTo), url.QueryEscape(route.FanoutTo)
		out := []guidedAnswerLink{{Label: "Servis haritası · " + route.PairTo, Href: "/service-map?focus=" + b}}
		if route.FanoutToKind == "service" {
			out = append([]guidedAnswerLink{{Label: "Trace'ler (" + route.PairFrom + ", " + route.PairTo + " ve " + route.FanoutTo + "'yi birlikte içeren)", Href: "/traces?services=" + a + "," + b + "," + c + "&view=list&rootOnly=false"}}, out...)
		} else {
			out = append([]guidedAnswerLink{{Label: "Trace'ler (" + route.PairFrom + " ve " + route.PairTo + "'yi birlikte içeren)", Href: "/traces?services=" + a + "," + b + "&view=list&rootOnly=false"}}, out...)
		}
		return out
	case guidedCallPeriod: // v0.10.438 (D3)
		a := route.PairFrom
		if a == "" {
			a = svc
		}
		if a == "" {
			return nil
		}
		out := []guidedAnswerLink{{Label: "Servis haritası · " + a, Href: "/service-map?focus=" + url.QueryEscape(a)}, {Label: a + " · Overview", Href: "/service?name=" + url.QueryEscape(a)}}
		if route.PairToKind == "service" && route.PairTo != "" {
			out = append(out, guidedAnswerLink{Label: route.PairTo + " · Overview", Href: "/service?name=" + url.QueryEscape(route.PairTo)})
		}
		return out
	case guidedWindowCompare: // v0.10.437 (D6) — her pencere kendi range'iyle (applyAll dokunmaz)
		if svc == "" || len(route.Windows) == 0 {
			return nil
		}
		loc := route.Windows[0].From.Location()
		var out []guidedAnswerLink
		for i, w := range route.Windows {
			out = append(out, guidedAnswerLink{
				Label: fmt.Sprintf("Pencere %d · %s", i+1, absWindowLabel(w, loc)),
				Href:  fmt.Sprintf("/service?name=%s&range=custom:%d-%d", svcQ, w.From.UnixMilli(), w.To.UnixMilli()),
			})
		}
		return out
	case guidedLogField: // v0.10.433 (D5) — bundle'ın koştuğu sorgu; yoksa backend'siz şekil
		q := route.LogQuery
		if q == "" {
			q, _ = logFieldSearchQuery(route.LogField, route.LogValue, route.LogContains, "")
		}
		href := "/logs?q=" + url.QueryEscape(q)
		if svc != "" {
			href += "&service=" + svcQ
		}
		links := []guidedAnswerLink{{Label: "Loglar (" + route.LogField + ")", Href: href}}
		if route.ExceptionTerm != "" { // v0.10.819 — yapıştırılan hata (tür-şekilli terim): exception grupları da, aynı servis kapsamıyla
			ih := "/inbox?kind=exception&q=" + url.QueryEscape(route.ExceptionTerm)
			if svc != "" {
				ih += "&service=" + svcQ
			}
			links = append(links, guidedAnswerLink{Label: "Exception grupları (" + route.ExceptionTerm + ")", Href: ih})
		}
		return links
	case guidedFamilyHealth:
		return []guidedAnswerLink{{Label: "Servisler", Href: "/services"}}
	case guidedMyProblems:
		return []guidedAnswerLink{{Label: "Problemler", Href: "/problems"}}
	// v0.10.1397 — takım servis cevabının linkleri (my_services = kimlikten
	// çözülen takımın team_services cevabı): takım süzgeçli Servisler, en kötü
	// servis, takımın Exceptions/Errors sayfası (teamExceptionsLinks — Inbox'a
	// DEĞİL; operatör: "Takım Exceptions dediğimde Problems'i açıyor").
	// Gövde copilot_team_overview.go teamServicesAnswerLinks.
	case guidedTeamServices, guidedMyServices:
		return teamServicesAnswerLinks(route)
	case guidedServiceExceptions:
		if svc != "" { // v0.10.1486 — örnek trace çipleri Exceptions linkinin ardından
			return append([]guidedAnswerLink{{Label: svc + " · Exceptions", Href: "/problems?service=" + svcQ}}, route.ExcSampleLinks...)
		}
		return nil
	case guidedTopErrorOps: // v0.10.1256
		if svc != "" {
			return []guidedAnswerLink{{Label: svc + " · Operations", Href: "/service?name=" + svcQ + "&tab=operations"}}
		}
		return nil
	case guidedServiceReport: // v0.10.1337 — raporun üç kanıt sayfası
		if svc != "" {
			return []guidedAnswerLink{
				{Label: svc + " · Overview", Href: "/service?name=" + svcQ},
				{Label: svc + " · Operations", Href: "/service?name=" + svcQ + "&tab=operations"},
				{Label: svc + " · Exceptions", Href: "/problems?service=" + svcQ},
			}
		}
		return nil
	case guidedMyExceptions, guidedTeamExceptions:
		// v0.9.1246 — link takımı taşır (kimlik URL'e girmez: katalog yazımı).
		// v0.10.1385 (operatör: "arkada Exceptions açıp filtrelenebilir") —
		// hedef artık Exceptions SAYFASI: takım süzgeçli, son görülme sıralı;
		// "teknik hata" + Oracle grubu → oracle=only ikinci link; iki eksenli
		// takımda birleşim için Inbox linki (copilot_team_exceptions.go).
		return teamExceptionsLinks(route)
	case guidedPodHealth:
		if svc != "" {
			return []guidedAnswerLink{{Label: svc + " · Infrastructure", Href: "/service?name=" + svcQ + "&tab=infra"}}
		}
		return nil
	case guidedShiftSummary:
		links := []guidedAnswerLink{{Label: "Inbox", Href: "/inbox"}, {Label: "Problemler", Href: "/problems"}}
		if svc != "" {
			links = append(links, guidedAnswerLink{Label: svc + " · Overview", Href: "/service?name=" + svcQ})
		}
		return links
	// v0.9.1142 — yapılandırılmış request kimliği rotası.
	//
	// Trace çözüldüyse asıl gidilecek yer trace detayı (/trace?id=).
	// Yanına loglar: `q` + `range=custom:<fromMs>-<toMs>` /logs'un
	// GERÇEKTEN okuduğu iki param (logsUrl.ts readLogsParams +
	// logsRangeParam) — ÖLÜ-PARAM DENETİMİ yapıldı, K4 sınıfı (v0.9.1130)
	// tekrar etmesin. Pencere kimliğin damgasından geliyor, yani çip
	// operatörü tam o ana götürür; bundle doldurmadıysa (çözülemedi)
	// pencere param'ı hiç YAZILMAZ — yanlış aralıklı bir link,
	// linksizlikten kötü.
	case guidedRequestID:
		var links []guidedAnswerLink
		if route.TraceID != "" {
			links = append(links, guidedAnswerLink{
				Label: "Trace", Href: "/trace?id=" + url.QueryEscape(route.TraceID),
			})
		}
		if svc != "" {
			links = append(links, guidedAnswerLink{
				Label: svc + " · Overview", Href: "/service?name=" + svcQ,
			})
		}
		if route.RequestID != "" && route.ReqWindowFromMs > 0 && route.ReqWindowToMs > route.ReqWindowFromMs {
			links = append(links, guidedAnswerLink{
				Label: "Loglar (istek penceresi)",
				Href: "/logs?q=" + url.QueryEscape(route.RequestID) +
					"&range=custom:" + strconv.FormatInt(route.ReqWindowFromMs, 10) +
					"-" + strconv.FormatInt(route.ReqWindowToMs, 10),
			})
		}
		return links
	case guidedDBHealth:
		return []guidedAnswerLink{{Label: "Databases", Href: "/databases"}}
	case guidedMessagingHealth:
		return []guidedAnswerLink{{Label: "Messaging", Href: "/messaging"}}
	}
	return nil
}

// applyFollowUpContext — devralma çekirdeği. route = mevcut sorunun
// kendi rotası (guidedNone olabilir). Dönenler: yeni rota, rangeS,
// devralınan temel mesaj (operasyon çözümü için) ve değişiklik bayrağı.
// changed=false → çağıran kendi route/rangeS'iyle devam eder.
// teams (v0.9.1134) — canlı takım kataloğu; önceki turun yeniden
// yönlendirilmesinde takım rotası da tanınsın diye taşınıyor ("orionsy
// takımı" → "peki son 24 saatte?").
func applyFollowUpContext(route guidedRoute, question string, prior []string, services, envs, teams []string) (guidedRoute, int64, string, bool) {
	msg := normalizeGuidedMsg(question)
	if !isFollowUpCue(msg) || len(prior) == 0 {
		return route, 0, "", false
	}
	toks := guidedTokens(msg)
	curSvc := extractServiceEntity(msg, services, envs)
	curEnv := extractEnvEntity(msg, envs)
	curRange, curExplicit := guidedRangeSExplicit(msg)

	// Şekil B: yönlenmiş ama konusuz — önceki turdan servis doldur.
	if route.Intent != guidedNone {
		if route.Service == "" && len(route.Family) == 0 &&
			followUpFillable[route.Intent] && !wantsFleetScope(toks) {
			for _, p := range prior {
				if svc := extractServiceEntity(normalizeGuidedMsg(p), services, envs); svc != "" {
					route.Service = svc
					rangeS := guidedRangeS(question)
					if !curExplicit {
						if v, ok := guidedRangeSExplicit(normalizeGuidedMsg(p)); ok {
							rangeS = v
						}
					}
					return route, rangeS, p, true
				}
			}
		}
		return route, 0, "", false
	}

	// Şekil A: hiç yönlenememiş takip sorusu. Devralmak için mevcut
	// mesajın SOMUT bir katkısı olmalı (servis/env/range ya da yarım
	// kalmış bir guided sinyali) — "peki bu ne demek?" gibi katkısız
	// sorular RAG/serbest döngüye kalır.
	if curSvc == "" && curEnv == "" && !curExplicit && !hasGuidedSignal(msg) {
		return route, 0, "", false
	}
	for _, p := range prior {
		pr := routeGuidedIntent(p, services, envs, teams, "")
		if pr.Intent == guidedNone {
			continue
		}
		if curSvc != "" {
			pr.Service, pr.Family = curSvc, nil
		}
		if curEnv != "" {
			pr.Env = curEnv
		}
		rangeS := int64(1800)
		if curExplicit {
			rangeS = curRange
		} else if v, ok := guidedRangeSExplicit(normalizeGuidedMsg(p)); ok {
			rangeS = v
		}
		return pr, rangeS, p, true
	}
	return route, 0, "", false
}

// askServiceChip — v0.10.429 (D1): aday servis + sorulan niyet → router'ın
// DETERMİNİSTİK olarak aynı niyete çözdüğü tam cümle (near_names_test
// gidiş-dönüşü pinler: çipe tıklamak serbest döngüye düşmez).
// askServiceChipFor — v0.10.436 (D2): çift/arama sorularının çipleri
// sorulan tarafın dışındaki yarıyı da taşır ki çip yeniden AYNI rotaya
// çözülsün ("checkout-service'dan payment-service'ye giden istekler").
func askServiceChipFor(route guidedRoute) []string {
	var out []string
	for _, opt := range route.ServiceOptions {
		switch route.AskIntent {
		case guidedPairRequests:
			if route.PairMissing == "to" {
				out = append(out, route.PairFrom+"'dan "+opt+"'ye giden istekler")
			} else {
				out = append(out, opt+"'dan "+route.PairTo+"'ye giden istekler")
			}
		case guidedTraceSearch:
			out = append(out, opt+" servisinde içinde \""+route.SearchText+"\" geçen trace'ler")
		case guidedLogField: // v0.10.443 — alan/değer çipe biner (eskiden varsayılan "sağlığı nasıl?")
			mode := "geçen"
			if !route.LogContains {
				mode = "olan"
			}
			out = append(out, opt+" loglarında "+route.LogField+" alanında \""+route.LogValue+"\" "+mode+" loglar")
		case guidedWindowCompare: // v0.10.437 (D6) — çip pencere metnini aynen taşır
			out = append(out, opt+" "+route.WindowText+" arası kıyas")
		case guidedCallPeriod: // v0.10.438 (D3)
			out = append(out, opt+" isteklerinde periyot var mı")
		case guidedFanout: // v0.10.439 (D4)
			out = append(out, opt+"'dan "+route.PairTo+"'ye gidenlerin hepsi "+route.FanoutTo+"'ye gidiyor mu")
		case guidedMetricChart: // v0.10.1386 — "<svc> JVM heap grafiği" aynı şablona yönlenir
			out = append(out, metricChartAskChip(route, opt))
		case guidedResourceInvestigation: // v0.10.1431 — "<svc> CPU kullanımı nasıl?" aynı aileye yönlenir; v0.10.1432 drill isteği de
			out = append(out, resAskChipText(route, opt))
		case guidedServiceTopology, guidedTraceTopology: // v0.10.1388 — "<svc> topolojisi?" aynı niyete yönlenir
			out = append(out, topologyAskChip(route, opt))
		default:
			out = append(out, askServiceChip(route.AskIntent, opt))
		}
	}
	return out
}

func askServiceChip(intent guidedIntent, svc string) string {
	switch intent {
	case guidedRootCause:
		return svc + " neden yavaş?"
	case guidedSlowTraces:
		return svc + " en yavaş trace'ler?"
	case guidedDeployImpact:
		return svc + " son deploy etkisi?"
	case guidedLogErrors:
		return svc + " hata logları?"
	case guidedFindEntity: // v0.10.463 (D1) — çıplak tam ad bu kademede tek servise çözülür
		return svc
	case guidedOpenPage: // v0.10.434 (D7b)
		return svc + " sayfasını aç"
	case guidedTraceSearch: // v0.10.436 (D2b) — parça route'tan (askServiceChipFor)
		return svc + " servisinde içinde \"…\" geçen trace'ler"
	case guidedPodHealth:
		return svc + " pod'ları nasıl?"
	case guidedDBHealth:
		return svc + " db sorguları nasıl?"
	case guidedMessagingHealth:
		return svc + " kafka gecikmesi nasıl?"
	case guidedProblems:
		return svc + " problemleri?"
	case guidedServiceExceptions: // v0.10.1252
		return svc + " teknik hataları?"
	case guidedServiceReport: // v0.10.1337
		return serviceReportChip(svc)
	case guidedServiceTopology: // v0.10.1388
		return svc + " topolojisi?"
	case guidedTraceTopology: // v0.10.1388
		return svc + " örnek trace topolojisi?"
	}
	return svc + " sağlığı nasıl?"
}
