package api

// ai_evalset_core.go — v0.10.940 (Settings › AI › Değerlendirme paneli;
// operatör onayı 2026-09-26): evalset koşucusunun SAF çekirdeği.
//
// v0.10.422'den beri şema/yükleyici/skorlayıcı evalset_fixture_test.go'da
// yaşıyordu — koşum yalnız `-tags evalset` CLI'ıydı. Panel koşuyu SUNUCUDA
// başlatınca bu parçalar üretim koduna taşındı; davranış BİREBİR (taşınan
// bildirimler metinleriyle aynı, testleri TestScoreEvalCase/TestScoreRCACase/
// TestEvalRubricInput değişmedi). İki fark bilinçli:
//
//  1. Yükleyici diskten değil GÖMÜLÜ kümeden (internal/copilot/evalset FS)
//     okur — çalışma imajında fikstür dizini yok; TestEvalsetFixturesValid
//     de artık gemiye bineni doğrular.
//  2. Vaka başına TEK fonksiyon (runEvalsetCase) hem CLI hem sunucu
//     koşucusunun ortak yolu; çağrının kendisi evalCallFn ile dışarıda
//     (CLI: özel Service + sıfır sıcaklık; sunucu: s.aiCall — make audit
//     CHECK 4 s.copilot.Explain'i internal/api'de yasaklar). Bilinmeyen
//     yüzey / bozuk hipotez artık koşuyu DÜŞÜRMEZ: "fixture: …" ihlaliyle
//     kalan bir vaka olur (CLI yine t.Errorf'lar).
//
// Bu dosya HTTP handler taşımaz: rotalar, iş defteri ve yaşam döngüsü
// ai_evalset_runs.go'da.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/rca"
)

const evalsetSchema = "coremetry.evalset/1"

type evalExpect struct {
	MustContain        []string `json:"mustContain,omitempty"`
	MustNotContain     []string `json:"mustNotContain,omitempty"`
	KnownEntities      []string `json:"knownEntities,omitempty"`
	KnownTeams         []string `json:"knownTeams,omitempty"` // v0.10.429 — team slotu için canlı takım listesi
	MaxUnknownEntities *int     `json:"maxUnknownEntities,omitempty"`
	Intent             string   `json:"intent,omitempty"`
	IntentService      string   `json:"intentService,omitempty"`
	MaxLatencyMs       int      `json:"maxLatencyMs,omitempty"`
	// v0.10.424 — RCA hakemi (surface RCAVerdict): kabul edilen verdict
	// kümesi + kanıt-ID atıf oranı (K2'den geçen / atıf yapılan).
	Verdicts                []string `json:"verdicts,omitempty"`
	MinEvidenceCitationRate *float64 `json:"minEvidenceCitationRate,omitempty"`
	// CoSRE öğrenme Faz 4 (cosre_quality_evalset.go) — örtük kalite
	// dışa aktarımının alanları. keyFacts mustContain gibi sınanır (harf
	// duyarsız alt-dize); expectedTools bilgi amaçlı — tek çağrılı evalset
	// yüzeyleri tool koşmaz, sınama serbest döngü koşucusunun işi.
	// v0.10.1316 — ajan kipinde (mode "agent") expectedTools bir KAPIDIR: her
	// biri en az bir kez başarıyla yürümeli; tek çağrılı kipte bilgi amaçlı kalır.
	ExpectedTools []string `json:"expectedTools,omitempty"`
	KeyFacts      []string `json:"keyFacts,omitempty"`
	// v0.10.1316 — yalnız ajan kipi (scoreEvalAgentCase): en az biri başarıyla
	// yürümeli / hiç denenmemeli / yürütülen çağrı tavanı / beklenen kademe
	// (aisurface: loop, guided, intent …). maxTokens METRİK (aşım iz notu,
	// kaldırmaz) — maxLatencyMs gibi.
	AnyOfTools     []string `json:"anyOfTools,omitempty"`
	ForbiddenTools []string `json:"forbiddenTools,omitempty"`
	MaxToolCalls   *int     `json:"maxToolCalls,omitempty"`
	ExpectTier     string   `json:"expectTier,omitempty"`
	MaxTokens      int      `json:"maxTokens,omitempty"`
	// IntentAnyOf — v0.10.1476: intent'e EK kabul edilen niyetler (yalnız
	// IntentClassify). Vakanın sözleşmesi tek niyet değil bir sınırsa
	// ("off_topic DEĞİL", "talimata uyup problems DEME") kabul kümesi açık
	// yazılır; gerekçe vakanın note alanında.
	IntentAnyOf []string `json:"intentAnyOf,omitempty"`
	// MaxAnswerRunes — v0.10.1476: cevap uzunluk tavanı (rune; 0 = yok).
	// Kısa-cevap sözleşmesi taşıyan yüzeyler için (OffTopicGeneral: "en
	// fazla 2-3 cümle"); model-bağımsız, deterministik.
	MaxAnswerRunes int `json:"maxAnswerRunes,omitempty"`
}

type evalCase struct {
	ID      string `json:"id"`
	Surface string `json:"surface"`
	Why     string `json:"why"`
	User    string `json:"user"`
	// Prompt — v0.10.423 (E5 export): kayıtlı örnek (sistem+kullanıcı
	// birleşik). Doluysa koşucu sistem promptunu BOŞ gönderir, prompt'u
	// kullanıcı olarak yollar; user ile birlikte verilmez.
	Prompt string `json:"prompt,omitempty"`
	// Hypothesis — v0.10.424: RCAVerdict vakası kullanıcı promptu yerine
	// chstore.RootCauseHypothesis taşır; prompt, katalog, rakipler ve şema
	// canlı yolla AYNI kodla üretilir (EvidenceCatalog.byID dışa kapalı —
	// katalog JSON'la elle kurulamaz).
	Hypothesis json.RawMessage `json:"hypothesis,omitempty"`
	Expect     evalExpect      `json:"expect"`
	// Provenance — v0.10.431: export vakasının kaynağı; truncated=true
	// (prompt_sample 4 KiB'de kırpıldı) ise koşucu vakayı PUANLAMAZ —
	// kırpık prompt'la alınan cevap ne pass ne fail kanıtıdır. ai_evalset.go
	// başlığı bunu v0.10.423'ten beri vaat ediyordu, koşucu okumuyordu.
	Provenance *evalProvenance `json:"provenance,omitempty"`
	// Mode — v0.10.1316: "" tek çağrı (v0.10.940 yolu) | "agent" (canlı ajan
	// döngüsü; yalnız serbest döngü yüzeylerinde — ai_evalset_agent.go).
	// Deep — ajan vakasının "Derin düşün" anahtarı (yalnız agent ile).
	Mode string `json:"mode,omitempty"`
	Deep bool   `json:"deep,omitempty"`
	// BindService — v0.10.1476 (yalnız ajan kipi): sorudaki SENTETİK servis
	// adı (checkout …). Canlı katalogda yoksa koşucu vakayı katalogdaki en
	// yoğun servise bağlar (soru, mustContain, knownEntities aynı adla
	// değişir; iz "bound" alanında). Prod kataloğunda sentetik ad olmadığı
	// için döngü resolve_entity'de "bulunamadı" ile DOĞRU biçimde durur ve
	// araç seçimi hiç ölçülmezdi.
	BindService string `json:"bindService,omitempty"`
	// Note — v0.10.1476: beklenti değişikliğinin yazılı gerekçesi (insan
	// okur; koşucu kullanmaz). Bir beklenti gevşetildiyse neden burada.
	Note string `json:"note,omitempty"`
	file string
	// expectRaw — v0.10.940: fikstürdeki expect nesnesi HARFİYEN (panel
	// çekmecesi "Beklenti"yi gösterir; struct gidiş-dönüşü bilinmeyen
	// anahtarı düşürürdü). Yükleyici doldurur; elle kurulan vakada boş.
	expectRaw json.RawMessage
}

type evalProvenance struct {
	ExchangeID string `json:"exchangeId,omitempty"`
	Truncated  bool   `json:"truncated"`
}

// evalCaseSkipReason — SAF: koşucunun atlama kararı (boş = koş).
func evalCaseSkipReason(c evalCase) string {
	if c.Provenance != nil && c.Provenance.Truncated {
		return "provenance.truncated — kırpık prompt puanlanmaz"
	}
	return ""
}

// evalRCAInputs — hipotez → (katalog, rakipler, izinli varlıklar, kullanıcı
// promptu); rca_verdict.go buildRCAVerdictSurface ile aynı adımlar (extras
// ve imzalar boş: fikstür IO'suz).
func evalRCAInputs(h *chstore.RootCauseHypothesis, now time.Time) (rcaEvidenceCatalog, []string, []string, string) {
	cat := buildRCAEvidenceCatalog(h)
	cands := make([]string, 0, len(h.Candidates))
	for _, c := range h.Candidates {
		cands = append(cands, c.Service)
	}
	rivals := buildRCARivalOptions(cat, h.TopSuspect, cands)
	entities := rcaAllowedEntities(cat)
	return cat, rivals, entities, buildRCAVerdictPrompt(h, cat, rivals, nil, now)
}

// scoreRCACase — SAF: model JSON'u → canlı ayrıştırma + onarım + kalkan
// zinciri (applyRCAShieldsPure); verdict kümesi, atıf oranı
// (K2'den geçen / atıf yapılan; hiç atıf yoksa 1.0 yalnız
// insufficient_evidence'ta), K3 uydurma sayısı.
func scoreRCACase(c evalCase, h *chstore.RootCauseHypothesis, cat rcaEvidenceCatalog, answer string) (fails []string, unknown int) {
	var mv rcaModelVerdict
	parsed := false
	if err := json.Unmarshal([]byte(strings.TrimSpace(answer)), &mv); err == nil && rcaVerdictEnumOK(mv.Verdict) {
		parsed = true
	} else if fixed, ok := salvageJSONObject(answer); ok {
		if err := json.Unmarshal([]byte(fixed), &mv); err == nil && rcaVerdictEnumOK(mv.Verdict) {
			parsed = true
		}
	}
	if !parsed {
		return []string{"unparsed: " + strings.TrimSpace(answer)}, 0
	}
	cited := len(mv.RootCause.Evidence)
	for _, st := range mv.CausalChain {
		cited += len(st.Evidence)
	}
	// v0.10.431 — çürütme atıfları da paydada: applyRCAShieldsPure
	// filterRefutationIDs'in reddettiklerini de RejectedEvidence'a yazar;
	// payda yalnız destek atıflarını sayınca geçerli bir verdict katalog
	// dışı tek bir refuted_by yüzünden 1.0 eşiğinin altına düşüyordu.
	for _, rej := range mv.RejectedHypotheses {
		cited += len(rej.RefutedBy)
	}
	sh := rcaShieldReport{Parsed: true}
	v := applyRCAShieldsPure(h, cat, mv, &sh)
	if len(c.Expect.Verdicts) > 0 {
		okV := false
		for _, w := range c.Expect.Verdicts {
			if v.Verdict == w {
				okV = true
			}
		}
		if !okV {
			fails = append(fails, fmt.Sprintf("verdict %q ∉ %v", v.Verdict, c.Expect.Verdicts))
		}
	}
	if c.Expect.MinEvidenceCitationRate != nil {
		rate := 1.0
		if cited > 0 {
			rate = float64(cited-len(sh.RejectedEvidence)) / float64(cited)
		} else if v.Verdict != "insufficient_evidence" {
			rate = 0
		}
		if rate < *c.Expect.MinEvidenceCitationRate {
			fails = append(fails, fmt.Sprintf("evidence citation rate %.2f < %.2f (cited %d, rejected %v)", rate, *c.Expect.MinEvidenceCitationRate, cited, sh.RejectedEvidence))
		}
	}
	unknown = len(sh.UnknownEntities)
	if c.Expect.MaxUnknownEntities != nil && unknown > *c.Expect.MaxUnknownEntities {
		fails = append(fails, fmt.Sprintf("unknown entities %d > %d: %v", unknown, *c.Expect.MaxUnknownEntities, sh.UnknownEntities))
	}
	return fails, unknown
}

// evalCaseInput — koşucu ve skorlayıcı için (system, user) çifti.
func evalCaseInput(c evalCase, system string) (string, string) {
	if c.Prompt != "" {
		return "", c.Prompt
	}
	return system, c.User
}

type evalFile struct {
	Schema string     `json:"schema"`
	Cases  []evalCase `json:"cases"`
}

// evalSystemPrompt — fikstürdeki surface adı → copilot.SystemPromptX.
// Eksik ad kırmızı test (TestEvalsetFixturesValid), sessiz atlama değil.
// Tam (full) araç seti varyantı — v0.10.1311 öncesiyle bayt bayt aynı.
func evalSystemPrompt(surface string) (string, bool) { return evalSystemPromptFor(surface, false) }

// ── v0.10.1311 — araç seti (v0.10.1302 ModelProfile.Toolset) ────────────
//
// Evalset koşusu artık serbest döngünün araç setini CANLI yolla aynı
// çözer: vakanın üretim profili (WithProfile) → copilot.ToolsetFor (açık
// profil ayarı, boşsa model adından ≤12B kuralı); koşu isteğinde açık
// `toolset` (simple|full) varsa o kazanır. Çözülen set YÜRÜTME bağlamına
// mcptools.WithToolset ile basılır (canlı döngünün ctx'i) ve döngü
// yüzeyinin sistem prompt'u bu bağlamdan okunur (SystemPromptChatAgentLoopFor)
// — prompt varyantı ile bağlam ayrı iki kaynaktan gelmez.
//
// Bilinçli sınır: evalset vakası TEK ÇAĞRIDIR — araç sonucu fikstürün
// kullanıcı metninde hazır gelir, modele araç kataloğu (tool spec) HİÇ
// gönderilmez (full'de de; v0.10.1311 öncesi de). Bu yüzden araç setinin
// evalset'te ölçülen etkisi prompt varyantıdır; gizli araçlar ne katalogda
// (yok) ne prompt'ta (simple varyant) anılır.
//
// v0.10.1316 — bu sınır yalnız TEK ÇAĞRILI vakalar için geçerli: ajan kipi
// vakası (`"mode":"agent"`, ChatLoop yüzeyi) canlı serbest döngüyü gerçek
// araç kataloğuyla koşar; araç seti orada kataloğu da değiştirir
// (ai_evalset_agent.go).
//
// Toolset yalnız serbest döngü prompt'unu koşan yüzeyde anlamlıdır; diğer
// yüzeyler "" kaydeder (cosre_interactions.toolset emsali: guided/niyet "").

// evalLoopSurface — SAF: serbest döngünün sistem prompt'unu (ajan çekirdeği)
// koşan evalset yüzeyi. Araç seti yalnız bunlara uygulanır.
func evalLoopSurface(surface string) bool {
	return surface == "ChatCodeResolve" || surface == evalAgentLoopSurface || // v0.10.1316
		surface == evalOpExceptionsSurface || // v0.10.1331
		surface == evalOpClusterCompareSurface // v0.10.1374
}

// evalOpExceptionsSurface — v0.10.1331: operasyon ön-getirmesi Exceptions
// grubu bulduğunda serbest döngünün prompt'u (çekirdek + Exceptions eki + sohbet).
const evalOpExceptionsSurface = "ChatOperationExceptions"

// evalOpClusterCompareSurface — v0.10.1374: endpoint ön-getirmesi küme kıyası
// yaptığında serbest döngünün prompt'u (çekirdek + küme kıyası eki + sohbet).
const evalOpClusterCompareSurface = "ChatOperationClusterCompare"

// evalAgentLoopSurface — v0.10.1316: ajan döngüsü vakalarının yüzeyi
// (agent_loop.json). ai_calls etiketi evalset-ChatLoop (D5); üretim profili
// serbest döngününki ("chat", evalProfileLabel).
const evalAgentLoopSurface = "ChatLoop"

// evalCaseToolset — SAF: vakanın araç seti. Döngü dışı yüzey "" ; açık
// seçim (simple|full) kazanır; değilse çözülen değer (ToolsetFor), boş ya
// da bilinmeyen → full (canlıdaki güvenli yön).
func evalCaseToolset(surface, override, resolved string) string {
	if !evalLoopSurface(surface) {
		return ""
	}
	switch override {
	case modelcaps.ToolsetSimple, modelcaps.ToolsetFull:
		return override
	}
	if resolved == modelcaps.ToolsetSimple {
		return modelcaps.ToolsetSimple
	}
	return modelcaps.ToolsetFull
}

// evalSystemPromptFor — evalSystemPrompt'un araç setine göre hâli: simple
// yalnız serbest döngü yüzeyinde ajan çekirdeğini değiştirir (canlıdaki
// copilot_chat.go loopPrompt'u gibi); diğer yüzeyler iki sette aynı.
func evalSystemPromptFor(surface string, simple bool) (string, bool) {
	switch surface {
	case "Trace":
		return copilot.SystemPromptTrace(), true
	case "Span":
		return copilot.SystemPromptSpan(), true
	case "Problem":
		return copilot.SystemPromptProblem(), true
	case "Exception":
		return copilot.SystemPromptException(), true
	case "OracleException": // v0.10.1100 — Oracle hata grubu (`ora:`)
		return copilot.SystemPromptOracleException(), true
	case "Incident":
		return copilot.SystemPromptIncident(), true
	case "Anomaly":
		return copilot.SystemPromptAnomaly(), true
	case "ServiceHealth":
		return copilot.SystemPromptServiceHealth(), true
	case "Runbook":
		return copilot.SystemPromptRunbook(), true
	case "CompareTraces":
		return copilot.SystemPromptCompareTraces(), true
	case "DeployImpact":
		return copilot.SystemPromptDeployImpact(), true
	case "SLOBurn":
		return copilot.SystemPromptSLOBurn(), true
	case "SlowQuery":
		return copilot.SystemPromptSlowQuery(), true
	case "NLToQuery":
		return copilot.SystemPromptNLToQuery(), true
	case "CHQueryOptimize":
		return copilot.SystemPromptCHQueryOptimize(), true
	case "RCAVerdict":
		return copilot.SystemPromptRCAVerdict(), true
	case "ServiceCharts":
		return copilot.SystemPromptServiceCharts(), true
	case "GeneralChat":
		return copilot.SystemPromptGeneralChat(), true
	case "OffTopicGeneral": // v0.10.1476 — v0.10.1458 genel soru cevabı (chat_offtopic_general.go)
		return copilot.SystemPromptOffTopicGeneral(), true
	case "Chat":
		return copilot.SystemPromptChat(), true
	case "ChatCodeResolve": // v0.10.1210 — iş kodu / kod arama eki (serbest döngü prompt'u + ek)
		return copilot.SystemPromptChatAgentLoopFor(simple) + copilot.CodeResolveChatAddendum(true) + "\n\n" + copilot.SystemPromptChat(), true // v0.10.1311 — araç setine göre
	case "IntentClassify":
		return copilot.SystemPromptIntentClassify(), true
	case "ServiceReport": // v0.10.1337 — guided servis raporu anlatıcısı (copilot_service_report.go)
		return copilot.SystemPromptServiceReport(), true
	case evalOpExceptionsSurface: // v0.10.1331 — copilot_chat.go loopPrompt'undaki ek sırası
		return copilot.SystemPromptChatAgentLoopFor(simple) + copilot.OperationExceptionsAddendum() + "\n\n" + copilot.SystemPromptChat(), true
	case evalOpClusterCompareSurface: // v0.10.1374 — copilot_chat.go loopPrompt'undaki ek sırası
		return copilot.SystemPromptChatAgentLoopFor(simple) + copilot.OperationClusterCompareAddendum() + "\n\n" + copilot.SystemPromptChat(), true
	case evalAgentLoopSurface: // v0.10.1316 — serbest döngü çekirdeği + sohbet çekirdeği (ekran/sayfa önsözsüz)
		return copilot.SystemPromptChatAgentLoopFor(simple) + "\n\n" + copilot.SystemPromptChat(), true
	}
	return "", false
}

// evalJSONSurface — JSON kipinde çağrılan yüzeyler (canlı yolla aynı).
func evalJSONSurface(surface string) bool {
	switch surface {
	case "IntentClassify", "RCAVerdict", "NLToQuery", "CHQueryOptimize":
		return true
	}
	return false
}

// scoreEvalCase — SAF: cevap + hata → ihlal listesi ve uydurma ad sayısı.
// mustContain/mustNotContain büyük/küçük harf duyarsız; uydurma sayımı
// rca.CountUnknownEntities (E6 sayacıyla AYNI tanım); niyet
// parseIntentJSON (canlı ayrıştırıcı — "none" = eşleşmedi).
func scoreEvalCase(c evalCase, system, answer string, err error) (fails []string, unknown int) {
	if err != nil {
		return []string{"error: " + err.Error()}, 0
	}
	fails = evalTextGateFails(c, answer) // v0.10.1316 — ajan kipiyle ortak
	sys, user := evalCaseInput(c, system)
	names := evalUnknownNames(c.Expect.KnownEntities, sys+"\n"+user, answer)
	unknown = len(names)
	if c.Expect.MaxUnknownEntities != nil && unknown > *c.Expect.MaxUnknownEntities {
		fails = append(fails, fmt.Sprintf("unknown entities %d > %d: %v", unknown, *c.Expect.MaxUnknownEntities, names))
	}
	if c.Expect.Intent != "" {
		route, _, matched := parseIntentJSON(answer, c.Expect.KnownEntities, nil, c.Expect.KnownTeams, "")
		got := "none"
		if matched {
			got = string(route.Intent)
		}
		if !evalIntentAccepted(c.Expect, got) {
			fails = append(fails, fmt.Sprintf("intent %q, want %q (raw %s)", got, evalIntentWant(c.Expect), strings.TrimSpace(answer)))
		} else if matched && c.Expect.IntentService != "" && evalIntentSlot(route) != c.Expect.IntentService {
			fails = append(fails, fmt.Sprintf("intent service %q, want %q (raw %s)", evalIntentSlot(route), c.Expect.IntentService, strings.TrimSpace(answer)))
		}
	}
	return fails, unknown
}

// evalUnknownNames — SAF, v0.10.1476: CountUnknownEntities'in AYNI tanımı
// (bilinen ∪ gösterilen jetonlar), sayı yerine adlar — kırmızı vakanın
// mesajı uydurulan adı söylesin (UInt8 kıstırması burada yok).
func evalUnknownNames(known []string, shown, answer string) []string {
	set := rca.LowerKnownSet(known...)
	rca.AddShownTokens(set, shown)
	return rca.ScanUnknownEntities(set, answer)
}

// evalIntentAccepted — SAF, v0.10.1476: niyet beklentisi (intent ∪ intentAnyOf).
func evalIntentAccepted(e evalExpect, got string) bool {
	if got == e.Intent {
		return true
	}
	for _, alt := range e.IntentAnyOf {
		if got == alt {
			return true
		}
	}
	return false
}

// evalIntentWant — fail mesajının "want" kısmı (tek niyet ya da küme).
func evalIntentWant(e evalExpect) string {
	if len(e.IntentAnyOf) == 0 {
		return e.Intent
	}
	return strings.Join(append([]string{e.Intent}, e.IntentAnyOf...), "|")
}

// evalIntentSlot — SAF, v0.10.1476: sınıflandırıcının servis slotunun
// rotadaki karşılığı. Çözülen ad route.Service'te; find_entity birden çok
// yakın aday bulduğunda (aday çipleri) slot route.FindQuery'de kalır —
// skorlayıcı yalnız Service'e bakıp doğru slotu "" sanıyordu (prod koşusu
// 2026-10-23: intent-find-entity-bare-name / -verb).
func evalIntentSlot(route guidedRoute) string {
	if route.Service != "" {
		return route.Service
	}
	return route.FindQuery
}

// evalTextGateFails — SAF (v0.10.1316'da scoreEvalCase'ten ayrıldı; metinler
// aynı): mustContain / mustNotContain / keyFacts, büyük/küçük harf duyarsız.
func evalTextGateFails(c evalCase, answer string) []string {
	var fails []string
	low := strings.ToLower(answer)
	for _, m := range c.Expect.MustContain {
		if !strings.Contains(low, strings.ToLower(m)) {
			fails = append(fails, "missing: "+m)
		}
	}
	for _, m := range c.Expect.MustNotContain {
		if strings.Contains(low, strings.ToLower(m)) {
			fails = append(fails, "forbidden: "+m)
		}
	}
	for _, f := range c.Expect.KeyFacts {
		if !strings.Contains(low, strings.ToLower(f)) {
			fails = append(fails, "missing fact: "+f)
		}
	}
	if lim := c.Expect.MaxAnswerRunes; lim > 0 { // v0.10.1476
		if n := utf8.RuneCountInString(strings.TrimSpace(answer)); n > lim {
			fails = append(fails, fmt.Sprintf("answer %d runes > %d", n, lim))
		}
	}
	return fails
}

// evalRubricInput — v0.10.666 (Faz A): vaka beklentisi + çıktı → deterministik
// rubrik girdisi. Dil boyutu yalnız düz metin yüzeylerde (JSON/şema yüzeyleri
// ve RCA hakemi n/a); grounded tavanı vakanın maxUnknownEntities'i (yoksa 0).
func evalRubricInput(c evalCase, answer string, err error, unknown int, rca bool) evalrubric.Input {
	maxUnknown := 0
	if c.Expect.MaxUnknownEntities != nil {
		maxUnknown = *c.Expect.MaxUnknownEntities
	}
	return evalrubric.Input{
		Answer:         answer,
		Err:            err,
		UnknownCount:   unknown,
		MaxUnknown:     maxUnknown,
		MustContain:    append(append([]string{}, c.Expect.MustContain...), c.Expect.KeyFacts...),
		MustNotContain: c.Expect.MustNotContain,
		ExpectTurkish:  !evalJSONSurface(c.Surface) && !rca,
	}
}

// ── Yükleyici (gömülü küme) ─────────────────────────────────────────────

// loadEvalsetCases — v0.10.940: GÖMÜLÜ fikstürler (internal/copilot/evalset
// FS). Panel, CLI ve TestEvalsetFixturesValid aynı kümeyi okur.
func loadEvalsetCases() ([]evalCase, error) { return loadEvalsetFS(evalset.FS) }

// loadEvalsetFS — dosya adı sıralı; doğrulama hataları eski disk
// yükleyicisinin metniyle aynı ("<dosya>: schema %q, want %q"), yalnız
// t.Fatal yerine hata döner. fs.FS parametresi test dikişi (fstest.MapFS).
func loadEvalsetFS(fsys fs.FS) ([]evalCase, error) {
	files, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []evalCase
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var ef evalFile
		if err := json.Unmarshal(b, &ef); err != nil {
			return nil, fmt.Errorf("%s: %v", f, err)
		}
		if ef.Schema != evalsetSchema {
			return nil, fmt.Errorf("%s: schema %q, want %q", f, ef.Schema, evalsetSchema)
		}
		// expect'in harfiyen hâli — aynı dizinle eşlenir (ikinci çözme
		// ilkini geçtiyse de geçer; geçmezse expectRaw boş kalır).
		var raw struct {
			Cases []struct {
				Expect json.RawMessage `json:"expect"`
			} `json:"cases"`
		}
		_ = json.Unmarshal(b, &raw)
		for i := range ef.Cases {
			ef.Cases[i].file = path.Base(f)
			if i < len(raw.Cases) {
				ef.Cases[i].expectRaw = raw.Cases[i].Expect
			}
			out = append(out, ef.Cases[i])
		}
	}
	return out, nil
}

// ── Üretim eşlemesi ─────────────────────────────────────────────────────

// evalsetSurfacePrefix — panel ve CLI çağrılarının ai_calls.surface öneki;
// /ai'da "Kaynak: Değerlendirme" ayrımı bu önekle yapılır (yeni kolon yok).
// Tek kaynak chstore'daki sabit: okuma filtresi ile yazan etiket ayrı iki
// literal olursa biri değişince evalset satırları sessizce üretime karışır.
const evalsetSurfacePrefix = chstore.AICallEvalsetSurfacePrefix

// evalProductionLabels — v0.10.940: evalset yüzeyi → ÜRETİMİN o promptu
// çağırdığı ai_calls etiketi. İki işi var: (1) profil yönlendirmesi —
// koşucu copilot.SurfaceProfileID(etiket) ile üretimin seçeceği profili
// bulur ve WithProfile ile sabitler; (2) CLI'da JSON şema adı (üretim
// gövdesiyle aynı ad). Etiketler çağrı noktalarından doğrulandı:
//   - Problem / Exception → operatörün ✨ tıklaması (explain-*). Arka plan
//     ikizleri (*-auto-explain) ayrı "background" profiline gidebilir;
//     panel operatörün gördüğü etkileşimli yolu ölçer.
//   - RCAVerdict → rootcause-verdict (etkileşimli; rootcause-auto değil),
//     aynı gerekçe.
//   - SlowQuery → insight-slow-query: SystemPromptSlowQuery'nin bugünkü TEK
//     çağıranı insight kartı (aiSurfaceFromPath "insight-<tür>"). Export
//     eşlemesi (evalSurfaceFromLabel) eski "slow-query" adlarını tanıyor,
//     bu adı tanımıyor — geri dönüşümdeki tek bilinçli istisna (pin testi).
//
// Eksik yüzey bu tabloda boş etiket → varsayılan profil (grupsuz yüzeyle aynı).
var evalProductionLabels = map[string]string{
	"Trace":           "explain-trace",
	"Span":            "explain-span",
	"Problem":         "explain-problem",
	"Exception":       "explain-exception",
	"Incident":        "explain-incident",
	"Anomaly":         "explain-anomaly",
	"ServiceHealth":   "explain-service",
	"Runbook":         "runbook",
	"CompareTraces":   "compare-traces",
	"DeployImpact":    "deploy-impact",
	"SLOBurn":         "explain-slo",
	"SlowQuery":       "insight-slow-query",
	"NLToQuery":       "nl-to-query",
	"CHQueryOptimize": "ch-optimize",
	"RCAVerdict":      rcaVerdictSurface,
	"ServiceCharts":   "explain-charts",
	"GeneralChat":     "chat-general",
	"OffTopicGeneral": "chat-offtopic-general", // v0.10.1476
	"Chat":            "chat",
	"IntentClassify":  "chat-intent",
}

func evalProductionLabel(surface string) string { return evalProductionLabels[surface] }

// evalProfileLabel — model profili seçimi: ChatCodeResolve (v0.10.1210) canlıda
// serbest sohbet döngüsünün ("chat") ekidir, aynı profille koşar. Etiket
// tablosunda DEĞİL (👎 export geri dönüşümü Chat'e düşmeli).
func evalProfileLabel(surface string) string {
	if surface == "ChatCodeResolve" || surface == evalAgentLoopSurface || surface == evalOpExceptionsSurface || surface == evalOpClusterCompareSurface { // v0.10.1316 — ChatLoop da serbest döngü; v0.10.1331; v0.10.1374
		return evalProductionLabel("Chat")
	}
	return evalProductionLabel(surface)
}

// evalSurfaceSchema — v0.10.940 (JSON eşitliği): üretimin bu yüzeyde
// gönderdiği şema. Önceden CLI yalnız IntentClassify'a şema veriyordu;
// NLToQuery ve CHQueryOptimize üretimde şemalı çağrılırken evalset'te düz
// json_object alıyordu — ölçülen davranış üretiminki değildi. RCAVerdict'in
// şeması hipotezden kurulur (runEvalsetCase), burada yok.
func evalSurfaceSchema(surface string) map[string]any {
	switch surface {
	case "IntentClassify":
		return intentClassifySchema()
	case "NLToQuery":
		return nlToQuerySchema()
	case "CHQueryOptimize":
		return chOptimizeSchema()
	}
	return nil
}

// ── Vaka başına ortak yol ───────────────────────────────────────────────

// evalCallFn — tek model çağrısı. surface = fikstür yüzey adı
// (IntentClassify…); etiketi/profili/kalkanı çağıran kurar. schema boşsa
// ve json true ise json_object.
type evalCallFn func(ctx context.Context, surface, system, user string, schema map[string]any, json bool) (string, error)

// evalCaseOutcome — runEvalsetCase'in ham sonucu (CLI log satırı ve sunucu
// vaka sonucu buradan türer).
type evalCaseOutcome struct {
	Skipped    bool
	SkipReason string
	Fixture    bool   // fikstür hatası (bilinmeyen yüzey / bozuk hipotez) — model çağrılmadı
	User       string // modele giden kullanıcı promptu (RCA: kurulan prompt)
	Answer     string
	Err        error // taşıma/sağlayıcı hatası
	LatencyMs  int64
	Fails      []string
	Unknown    int
	RCA        bool
	Rubric     evalrubric.Result
	// Toolset — v0.10.1311: vakanın koştuğu araç seti (full | simple; döngü
	// dışı yüzey ""). Bağlamdan okunur (mcptools.WithToolset), prompt da.
	Toolset string
	// Mode / Agent — v0.10.1316: ajan kipi vakasının kipi ve izi (tek çağrıda "" / nil).
	Mode  string
	Agent *evalAgentTrail
	// Infra — v0.10.1476: sağlayıcı/taşıma hatası sınıfı (timeout | http_5xx |
	// unavailable | quota); doluysa vaka PUANLANMAZ (Skipped) — kalite
	// başarısızlığı değil, geçme oranını kirletmez. Yeniden deneme YOK.
	Infra string
}

// evalInfraClass — SAF, v0.10.1476: hata modelin değil altyapının mı
// (copilot.ClassifyAIError — /ai'ın sınıflandırıcısı). 4xx / bağlam taşması /
// localOnly / ret altyapı DEĞİL: istek bizim, düzeltmesi bizde.
func evalInfraClass(err error) string {
	if err == nil {
		return ""
	}
	switch c := copilot.ClassifyAIError(err); c {
	case copilot.ErrClassTimeout, copilot.ErrClassHTTP5xx, copilot.ErrClassUnavailable, copilot.ErrClassQuota:
		return c
	}
	return ""
}

// evalInfraReasonMax — atlama nedenindeki hata metni tavanı (bayt).
const evalInfraReasonMax = 300

// evalMarkInfra — SAF, v0.10.1476: cevapsız + altyapı sınıflı hata → vaka
// atlanır (Infra + SkipReason "altyapı hatası (…)"), ihlal ve rubrik boşalır.
// Kısmi cevap varsa puanlanır (model konuştu). Zaten atlanmış vaka aynen.
func evalMarkInfra(out evalCaseOutcome) evalCaseOutcome {
	if out.Skipped || out.Err == nil || strings.TrimSpace(out.Answer) != "" {
		return out
	}
	cls := evalInfraClass(out.Err)
	if cls == "" {
		return out
	}
	msg, _ := evalTruncate(out.Err.Error(), evalInfraReasonMax)
	out.Skipped, out.Infra = true, cls
	out.SkipReason = "altyapı hatası (" + cls + "): " + msg
	out.Fails = nil
	out.Rubric = evalrubric.Result{}
	return out
}

// runEvalsetCase — v0.10.940: CLI (evalset_test.go) ve sunucu koşucusunun
// TEK vaka yolu. Adımlar v0.10.422-666 CLI döngüsünün aynısı: atlama →
// sistem promptu → (system,user) → RCA girdileri + şema → çağrı → skor →
// rubrik. maxLatencyMs METRİK kalır (ihlal üretmez). Sıcaklık burada
// ayarlanmaz: CLI kendi özel Service'inde 0 kullanır, sunucu üretim
// profilinin/küresel ayarın sıcaklığıyla koşar (üretim eşitliği; JSON
// istekleri sağlayıcı katmanında zaten 0'a iner).
//
// v0.10.1316 — agent: ajan kipi vakasının koşucusu (ai_evalset_agent.go). Tek
// çağrılı vakalarda kullanılmaz; nil (CLI) iken ajan vakası atlanır.
func runEvalsetCase(ctx context.Context, call evalCallFn, agent evalAgentFn, c evalCase) evalCaseOutcome {
	if why := evalCaseSkipReason(c); why != "" { // v0.10.431
		_, user := evalCaseInput(c, "")
		return evalCaseOutcome{Skipped: true, SkipReason: why, User: user}
	}
	// v0.10.1311 — araç seti YÜRÜTME bağlamından (runEvalJob basar; CLI
	// işaretsiz → full, eski davranış). Prompt varyantı aynı kaynaktan.
	simple := mcp.SimpleToolsetFrom(ctx)
	toolset := ""
	if evalLoopSurface(c.Surface) {
		toolset = modelcaps.ToolsetFull
		if simple {
			toolset = modelcaps.ToolsetSimple
		}
	}
	if evalAgentCase(c) { // v0.10.1316 — canlı ajan döngüsü
		return evalMarkInfra(runEvalAgentCase(ctx, agent, c, toolset)) // v0.10.1476
	}
	system, ok := evalSystemPromptFor(c.Surface, simple)
	if !ok {
		return evalCaseOutcome{Fixture: true, User: c.User, Fails: []string{fmt.Sprintf("fixture: surface %q çözülemiyor", c.Surface)}}
	}
	sys, user := evalCaseInput(c, system) // v0.10.423 — export vakaları ham prompt taşır
	schema := evalSurfaceSchema(c.Surface)
	// v0.10.424 — RCA hakemi: hipotezden canlı yolla aynı prompt/şema.
	var rcaH *chstore.RootCauseHypothesis
	var rcaCat rcaEvidenceCatalog
	if len(c.Hypothesis) > 0 {
		rcaH = &chstore.RootCauseHypothesis{}
		if err := json.Unmarshal(c.Hypothesis, rcaH); err != nil {
			return evalCaseOutcome{Fixture: true, User: user, Fails: []string{fmt.Sprintf("fixture: hypothesis: %v", err)}}
		}
		var rivals, entities []string
		rcaCat, rivals, entities, user = evalRCAInputs(rcaH, time.Now())
		schema = rcaVerdictSchema(entities, rivals)
	}
	t0 := time.Now()
	answer, err := call(ctx, c.Surface, sys, user, schema, evalJSONSurface(c.Surface))
	if err == nil {
		// v0.10.1476 — üretimin bu yüzeyde cevaba eklediği SUNUCU metni (aynı
		// fonksiyon, aynı girdi): puanlanan, operatörün gördüğü cevaptır.
		answer = evalServerCompose(c.Surface, user, answer)
	}
	out := evalCaseOutcome{User: user, Answer: answer, Err: err, LatencyMs: time.Since(t0).Milliseconds(), RCA: rcaH != nil, Toolset: toolset}
	if rcaH != nil && err == nil {
		out.Fails, out.Unknown = scoreRCACase(c, rcaH, rcaCat, answer)
	} else {
		out.Fails, out.Unknown = scoreEvalCase(c, system, answer, err)
	}
	out.Rubric = evalrubric.Score(evalRubricInput(c, answer, err, out.Unknown, rcaH != nil))
	return evalMarkInfra(out) // v0.10.1476
}

// evalServerCompose — SAF, v0.10.1476: üretim handler'ının model cevabına
// deterministik olarak eklediği sunucu metni, yüzey başına. Tek kaynak
// handler'ın kullandığı fonksiyon; burada yalnız çağrılır. Eşlemesiz yüzey
// cevabı aynen döndürür.
func evalServerCompose(surface, user, answer string) string {
	switch surface {
	case "SLOBurn": // copilot_explain_slo.go — alarm eşiği satırı
		return sloAnswerWithBurnVerdict(user, answer)
	case "Trace": // v0.10.1630 — trace_explain_latency_owner.go — dış bağımlılık satırı
		return traceAnswerWithLatencyOwner(user, answer)
	}
	return answer
}

// ── Panel şekilleri (HTTP sözleşmesi — frontend lib/types.ts aynası) ────

const (
	evalStatusRunning   = "running"
	evalStatusDone      = "done"
	evalStatusCancelled = "cancelled"
	evalStatusFailed    = "failed"
	evalStatusAbandoned = "abandoned" // YALNIZ okumada türetilir, yazılmaz
)

// evalRunStaleAfter — sahipsiz "running" satırı bu kadar sessizse terk
// edilmiş sayılır. Vaka başına yazım var; tek vaka en fazla profil zaman
// aşımı (≤ 600 sn) sürer. v0.10.1316 — 15 → 20 dk: ajan vakası
// evalAgentCaseMax'e (≤ 10 dk) dek sürebilir; pay iki katı kalsın.
const evalRunStaleAfter = 20 * time.Minute

// Kırpma tavanları (bayt, rune-güvenli): liste değil çekmece yükü, ama 51
// vakalık cases JSON'u CH satırında ve yanıtta sınırlı kalsın.
const (
	evalInputCap  = 8 << 10
	evalAnswerCap = 16 << 10
)

type evalCatalogSurface struct {
	Surface      string `json:"surface"`
	Cases        int    `json:"cases"`
	ProfileID    string `json:"profileId"`
	ProfileLabel string `json:"profileLabel"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	BaseURL      string `json:"baseUrl"`
}

type evalCatalog struct {
	Ready         bool   `json:"ready"`
	AppVersion    string `json:"appVersion"`
	PromptVersion string `json:"promptVersion"`
	// DefaultProfileID — v0.10.940: koşu başlarken özetin profileId'sine
	// yazılan profil (haritasız yüzey = varsayılan; AYNI çözücü). Panel
	// başlığı "varsayılan profil"i yüzey sırasından tahmin etmesin. Profil
	// KİMLİĞİ, anahtar değil.
	DefaultProfileID string `json:"defaultProfileId"`
	// Toolset — v0.10.1311: OTOMATİK araç setinin bugünkü çözümü (serbest
	// döngü yüzeyinin üretim profili + ToolsetFor): full | simple; kümede
	// döngü yüzeyi yoksa "". Panelin "Otomatik (…)" seçeneği bunu yazar.
	Toolset  string               `json:"toolset"`
	Total    int                  `json:"total"`
	Surfaces []evalCatalogSurface `json:"surfaces"`
	// AgentCases — v0.10.1316: kümedeki ajan kipi vaka sayısı.
	AgentCases int `json:"agentCases"`
}

type evalSurfaceSummary struct {
	Surface string `json:"surface"`
	Cases   int    `json:"cases"`
	Pass    int    `json:"pass"`
	Fail    int    `json:"fail"`
	Skipped int    `json:"skipped"`
	// UnknownEntities — null: bu yüzeyin HİÇBİR vakası maxUnknownEntities
	// taşımıyor (ölçülmüyor ≠ sıfır uydurma); değilse taşıyan vakaların TOPLAMI.
	UnknownEntities *int  `json:"unknownEntities"`
	AvgLatencyMs    int64 `json:"avgLatencyMs"`
	// v0.10.1316 — ajan kipi: plandaki ajan vakası sayısı ve tamamlanan
	// ajan vakalarının araç çağrısı / token ortalaması (nil = yüzeyde ajan
	// vakası yok ya da henüz biten yok — ölçülmedi ≠ 0).
	AgentCases   int      `json:"agentCases"`
	AvgToolCalls *float64 `json:"avgToolCalls"`
	AvgTokens    *float64 `json:"avgTokens"`
	// Infra — v0.10.1476: atlananların altyapı hatası olanları (Skipped'in
	// alt kümesi; geçme oranına girmez).
	Infra int `json:"infra"`
}

type evalRunSummary struct {
	ID            string               `json:"id"`
	Status        string               `json:"status"`
	StartedAt     string               `json:"startedAt"`
	UpdatedAt     string               `json:"updatedAt"`
	FinishedAt    string               `json:"finishedAt"`
	StartedBy     string               `json:"startedBy"`
	AppVersion    string               `json:"appVersion"`
	PromptVersion string               `json:"promptVersion"`
	Model         string               `json:"model"`
	ProfileID     string               `json:"profileId"`
	Surfaces      []string             `json:"surfaces"`
	Total         int                  `json:"total"`
	Done          int                  `json:"done"`
	Pass          int                  `json:"pass"`
	Fail          int                  `json:"fail"`
	Skipped       int                  `json:"skipped"`
	RubricMean    float64              `json:"rubricMean"`
	Error         string               `json:"error"`
	BySurface     []evalSurfaceSummary `json:"bySurface"`
	// Toolset — v0.10.1311: koşunun serbest döngü yüzeylerinin araç seti
	// (full | simple); planda döngü yüzeyi yoksa "". ToolsetPinned: koşu
	// isteğinde açık seçildi (otomatik çözüm değil).
	Toolset       string `json:"toolset"`
	ToolsetPinned bool   `json:"toolsetPinned"`
	// Infra — v0.10.1476: altyapı hatasıyla atlanan vaka sayısı (Skipped'in
	// alt kümesi; yüzey özetlerinin toplamı — CH kolonu yok).
	Infra int `json:"infra"`
}

type evalCaseResult struct {
	ID              string          `json:"id"`
	Surface         string          `json:"surface"`
	Why             string          `json:"why"`
	OK              bool            `json:"ok"`
	Skipped         bool            `json:"skipped"`
	SkipReason      string          `json:"skipReason"`
	LatencyMs       int64           `json:"latencyMs"`
	Fails           []string        `json:"fails"`
	UnknownEntities int             `json:"unknownEntities"`
	RubricTotal     float64         `json:"rubricTotal"`
	ProfileID       string          `json:"profileId"`
	Model           string          `json:"model"`
	Input           string          `json:"input"`
	InputTruncated  bool            `json:"inputTruncated"`
	Answer          string          `json:"answer"`
	AnswerTruncated bool            `json:"answerTruncated"`
	Error           string          `json:"error"`
	Expect          json.RawMessage `json:"expect"`
	// InfraError — v0.10.1476: altyapı hatası sınıfı (skipped=true ile);
	// "" = model cevabı puanlandı ya da başka nedenle atlandı.
	InfraError string `json:"infraError,omitempty"`
	// Toolset — v0.10.1311: full | simple; döngü dışı yüzey "".
	Toolset string `json:"toolset"`
	// Mode / Agent — v0.10.1316: ajan kipi ("agent") ve izi; tek çağrıda yok.
	Mode  string          `json:"mode,omitempty"`
	Agent *evalAgentTrail `json:"agent,omitempty"`
}

type evalCompareSide struct {
	ID            string `json:"id"`
	StartedAt     string `json:"startedAt"`
	Model         string `json:"model"`
	PromptVersion string `json:"promptVersion"`
	AppVersion    string `json:"appVersion"`
	Pass          int    `json:"pass"`
	Fail          int    `json:"fail"`
	Total         int    `json:"total"`
	Toolset       string `json:"toolset"` // v0.10.1311 — "" = döngü yüzeyi yok / eski koşu
	// v0.10.1316 — ajan vakalarının araç çağrısı / token ortalaması (nil = ajan vakası yok).
	ToolCallsMean *float64 `json:"toolCallsMean"`
	TokensMean    *float64 `json:"tokensMean"`
}

type evalCompareCase struct {
	ID      string `json:"id"`
	Surface string `json:"surface"`
}

type evalCompareDelta struct {
	ID      string  `json:"id"`
	Surface string  `json:"surface"`
	Before  float64 `json:"before"`
	After   float64 `json:"after"`
}

type evalCompare struct {
	Comparable      bool               `json:"comparable"`
	Note            string             `json:"note"`
	Base            evalCompareSide    `json:"base"`
	Head            evalCompareSide    `json:"head"`
	RubricMeanDelta float64            `json:"rubricMeanDelta"`
	NewlyFailing    []evalCompareCase  `json:"newlyFailing"`
	NewlyPassing    []evalCompareCase  `json:"newlyPassing"`
	Regressed       []evalCompareDelta `json:"regressed"`
	Improved        []evalCompareDelta `json:"improved"`
	OnlyInBase      []string           `json:"onlyInBase"`
	OnlyInHead      []string           `json:"onlyInHead"`
}

// evalRunSummaryBlob — summary kolonunun şekli (chstore için opak JSON).
// v0.10.1311 — araç seti de burada (CH kolonu yok: şema değişmez; eski
// satırda alan yok → "").
type evalRunSummaryBlob struct {
	BySurface     []evalSurfaceSummary `json:"bySurface"`
	Toolset       string               `json:"toolset,omitempty"`
	ToolsetPinned bool                 `json:"toolsetPinned,omitempty"`
}

// evalBlobFromRow — SAF: summary kolonu → blob; boş/bozuk → sıfır değer.
func evalBlobFromRow(r chstore.EvalRun) evalRunSummaryBlob {
	var blob evalRunSummaryBlob
	if r.Summary != "" {
		_ = json.Unmarshal([]byte(r.Summary), &blob)
	}
	return blob
}

// ── Saf yardımcılar ─────────────────────────────────────────────────────

// evalTruncate — SAF: s'yi en fazla maxBytes bayta, RUNE SINIRINDA keser
// (Türkçe metin çok baytlı; ortadan bölünmüş rune JSON'da U+FFFD olur).
func evalTruncate(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	i := maxBytes
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i], true
}

// evalTimeString — RFC3339 UTC; sıfır zaman "" (finishedAt sürerken).
func evalTimeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// evalExpectJSON — harfiyen fikstür nesnesi; yoksa struct'ın kendisi.
func evalExpectJSON(c evalCase) json.RawMessage {
	if len(c.expectRaw) > 0 {
		return c.expectRaw
	}
	b, err := json.Marshal(c.Expect)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// evalCaseResultFrom — SAF: vaka + ham sonuç → panel satırı (kırpma dahil).
func evalCaseResultFrom(c evalCase, o evalCaseOutcome, profileID, model string) evalCaseResult {
	in, inT := evalTruncate(o.User, evalInputCap)
	ans, ansT := evalTruncate(o.Answer, evalAnswerCap)
	fails := o.Fails
	if fails == nil {
		fails = []string{}
	}
	res := evalCaseResult{
		ID: c.ID, Surface: c.Surface, Why: c.Why,
		OK: !o.Skipped && len(o.Fails) == 0, Skipped: o.Skipped, SkipReason: o.SkipReason,
		LatencyMs: o.LatencyMs, Fails: fails, UnknownEntities: o.Unknown, RubricTotal: o.Rubric.Total,
		ProfileID: profileID, Model: model,
		Input: in, InputTruncated: inT, Answer: ans, AnswerTruncated: ansT,
		Expect: evalExpectJSON(c), Toolset: o.Toolset,
		Mode: o.Mode, Agent: o.Agent, // v0.10.1316
		InfraError: o.Infra, // v0.10.1476
	}
	if o.Err != nil {
		res.Error = o.Err.Error()
	}
	return res
}

// evalSurfaceCounts — SAF: yüzey → vaka sayısı, sıra: sayı azalan, ad artan
// (katalog ve yüzey özeti AYNI sırayı kullanır).
func evalSurfaceCounts(cases []evalCase) []evalSurfaceSummary {
	idx := map[string]int{}
	var out []evalSurfaceSummary
	for _, c := range cases {
		i, ok := idx[c.Surface]
		if !ok {
			i = len(out)
			idx[c.Surface] = i
			out = append(out, evalSurfaceSummary{Surface: c.Surface})
		}
		out[i].Cases++
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cases != out[j].Cases {
			return out[i].Cases > out[j].Cases
		}
		return out[i].Surface < out[j].Surface
	})
	if out == nil {
		out = []evalSurfaceSummary{}
	}
	return out
}

// evalSurfaceSummaries — SAF: plan (koşunun vakaları) + şimdiye dek gelen
// sonuçlar → yüzey özeti. unknownEntities: planda maxUnknownEntities taşıyan
// vaka yoksa null; varsa o vakaların (tamamlananların) toplamı — koşu
// ortasında null↔0 salınmasın diye karar PLANDAN. avgLatencyMs yalnız
// atlanmamış vakalardan.
func evalSurfaceSummaries(plan []evalCase, results []evalCaseResult) []evalSurfaceSummary {
	out := evalSurfaceCounts(plan)
	idx := map[string]int{}
	for i, s := range out {
		idx[s.Surface] = i
	}
	measured := map[string]bool{} // vaka id → maxUnknownEntities taşıyor
	for _, c := range plan {
		if c.Expect.MaxUnknownEntities != nil {
			measured[c.ID] = true
			if i, ok := idx[c.Surface]; ok && out[i].UnknownEntities == nil {
				zero := 0
				out[i].UnknownEntities = &zero
			}
		}
	}
	for _, c := range plan { // v0.10.1316 — ajan vakası sayısı PLANDAN
		if i, ok := idx[c.Surface]; ok && evalAgentCase(c) {
			out[i].AgentCases++
		}
	}
	agentRes := make([][]evalCaseResult, len(out))
	latSum := make([]int64, len(out))
	latN := make([]int64, len(out))
	for _, r := range results {
		i, ok := idx[r.Surface]
		if !ok {
			continue
		}
		switch {
		case r.Skipped:
			out[i].Skipped++
			if r.InfraError != "" {
				out[i].Infra++ // v0.10.1476
			}
		case r.OK:
			out[i].Pass++
		default:
			out[i].Fail++
		}
		if !r.Skipped {
			latSum[i] += r.LatencyMs
			latN[i]++
		}
		if measured[r.ID] && out[i].UnknownEntities != nil {
			*out[i].UnknownEntities += r.UnknownEntities
		}
		if r.Agent != nil {
			agentRes[i] = append(agentRes[i], r)
		}
	}
	for i := range out {
		if latN[i] > 0 {
			out[i].AvgLatencyMs = latSum[i] / latN[i]
		}
		out[i].AvgToolCalls, out[i].AvgTokens = evalAgentMeans(agentRes[i]) // v0.10.1316
	}
	return out
}

// evalDerivedStatus — SAF: saklanan durum → gösterilen durum. Bu süreç
// SAHİP OLMADIĞI ve updated_at'i evalRunStaleAfter'dan eski bir "running"
// satırı "abandoned"dır (pod öldü / yeniden başladı). Yazılmaz, okumada
// türetilir — satırı düzeltmeye çalışan bir yazıcı başka pod'un canlı
// koşusunu ezebilirdi.
func evalDerivedStatus(status string, updatedAt, now time.Time, ownedHere bool) string {
	if status == evalStatusRunning && !ownedHere && now.Sub(updatedAt) > evalRunStaleAfter {
		return evalStatusAbandoned
	}
	return status
}

// evalRunBlocksStart — SAF: çapraz-pod kapısı. En yeni satır taze bir
// "running" ise (başka pod sürüyor olabilir) yeni koşu 409 alır.
// evalDerivedStatus'un tümleyeni: terk edilmiş koşu engel değildir.
func evalRunBlocksStart(r chstore.EvalRun, now time.Time) bool {
	return r.Status == evalStatusRunning && now.Sub(r.UpdatedAt) <= evalRunStaleAfter
}

// evalSummaryFromRow — SAF: CH satırı → özet (durum türetilmiş).
func evalSummaryFromRow(r chstore.EvalRun, now time.Time, ownedHere bool) evalRunSummary {
	blob := evalBlobFromRow(r)
	if blob.BySurface == nil {
		blob.BySurface = []evalSurfaceSummary{}
	}
	surfaces := r.Surfaces
	if surfaces == nil {
		surfaces = []string{}
	}
	return evalRunSummary{
		ID: r.ID, Status: evalDerivedStatus(r.Status, r.UpdatedAt, now, ownedHere),
		StartedAt: evalTimeString(r.StartedAt), UpdatedAt: evalTimeString(r.UpdatedAt), FinishedAt: evalTimeString(r.FinishedAt),
		StartedBy: r.StartedBy, AppVersion: r.AppVersion, PromptVersion: r.PromptVersion,
		Model: r.Model, ProfileID: r.ProfileID, Surfaces: surfaces,
		Total: int(r.Total), Done: int(r.Done), Pass: int(r.Pass), Fail: int(r.Fail), Skipped: int(r.Skipped),
		RubricMean: r.RubricMean, Error: r.Error, BySurface: blob.BySurface,
		Toolset: blob.Toolset, ToolsetPinned: blob.ToolsetPinned, // v0.10.1311
		Infra: evalInfraTotal(blob.BySurface), // v0.10.1476
	}
}

// evalInfraTotal — SAF, v0.10.1476: yüzey özetlerinden altyapı hatası toplamı.
func evalInfraTotal(by []evalSurfaceSummary) int {
	n := 0
	for _, s := range by {
		n += s.Infra
	}
	return n
}

// evalCasesFromRow — SAF: cases kolonu → dilim; boş/bozuk → [] (ilerleme
// yazımı cases'i bilinçli boş bırakır).
func evalCasesFromRow(r chstore.EvalRun) []evalCaseResult {
	var out []evalCaseResult
	if r.Cases != "" {
		_ = json.Unmarshal([]byte(r.Cases), &out)
	}
	if out == nil {
		out = []evalCaseResult{}
	}
	return out
}

// evalMergeRunList — SAF: CH listesi + bu süreçteki kopyalar → en yeni önce,
// en fazla keep. Sürecin kendi kopyası CH satırından TAZEDİR (her vaka
// sonrası bellek önce güncellenir) → aynı kimlikli satır düşer, bellekteki
// yerine geçer. Kalanlar sahipsiz sayılır (terk türetmesi onlara uygulanır).
// v0.10.940 — own artık dilim: koşan iş + son yazımı düşen iş (CH'de bayat
// "running" bırakan bitmiş koşu). İkincisi başa değil ZAMANINA oturur
// (arada başka pod'un koşusu olabilir) → startedAt'e göre kararlı sıra;
// RFC3339 UTC sabit genişlikte, metin sırası zaman sırası.
func evalMergeRunList(rows []chstore.EvalRun, own []evalRunSummary, now time.Time, keep int) []evalRunSummary {
	out := make([]evalRunSummary, 0, len(rows)+len(own))
	mine := make(map[string]bool, len(own))
	for _, o := range own {
		mine[o.ID] = true
		out = append(out, o)
	}
	for _, r := range rows {
		if mine[r.ID] {
			continue
		}
		out = append(out, evalSummaryFromRow(r, now, false))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	if len(out) > keep {
		out = out[:keep]
	}
	return out
}

// evalSelectCases — SAF: istenen yüzeyler → seçilen vakalar (fikstür
// sırası) + normalize yüzey listesi (boş = tümü → []). Bilinmeyen yüzey
// (kümede vakası olmayan ad) hata: UI kataloğun dışını gönderemez, gönderen
// crafted istektir.
func evalSelectCases(all []evalCase, want []string) ([]evalCase, []string, error) {
	have := map[string]bool{}
	for _, c := range all {
		have[c.Surface] = true
	}
	pick := map[string]bool{}
	surfaces := []string{}
	for _, w := range want {
		w = strings.TrimSpace(w)
		if w == "" || pick[w] {
			continue
		}
		if !have[w] {
			return nil, nil, fmt.Errorf("bilinmeyen yüzey: %q", w)
		}
		pick[w] = true
		surfaces = append(surfaces, w)
	}
	if len(pick) == 0 {
		return all, []string{}, nil
	}
	var out []evalCase
	for _, c := range all {
		if pick[c.Surface] {
			out = append(out, c)
		}
	}
	return out, surfaces, nil
}

// evalCompareRuns — SAF: iki BİTMİŞ koşu → evalrubric.Diff'in panel hâli.
// Diff'in kendisi yeniden kullanılır (cmd/evalsetdiff ile AYNI kıyas
// semantiği: kimlikle eşleşme, model farkı = kıyaslanamaz, aynı
// prompt_version = gürültü tabanı notu). Atlanan vakalar Diff'e girmez —
// CLI artefaktı da onları taşımıyordu.
func evalCompareRuns(base, head chstore.EvalRun) evalCompare {
	bCases, hCases := evalCasesFromRow(base), evalCasesFromRow(head)
	surf := map[string]string{}
	toRun := func(r chstore.EvalRun, cases []evalCaseResult) evalrubric.Run {
		run := evalrubric.Run{Schema: evalrubric.RunSchema, At: r.StartedAt, PromptVersion: r.PromptVersion, Model: r.Model,
			Summary: evalrubric.Summary{Cases: int(r.Total), Pass: int(r.Pass), Fail: int(r.Fail), Skipped: int(r.Skipped), RubricMean: r.RubricMean}}
		for _, c := range cases {
			surf[c.ID] = c.Surface
			if c.Skipped {
				continue
			}
			run.Cases = append(run.Cases, evalrubric.CaseResult{ID: c.ID, Surface: c.Surface, OK: c.OK, LatencyMs: c.LatencyMs, Fails: c.Fails,
				Rubric: evalrubric.Result{Total: c.RubricTotal}})
		}
		return run
	}
	bRun := toRun(base, bCases)
	hRun := toRun(head, hCases) // head sonra: yüzey adı çakışırsa head'inki kalır
	rep := evalrubric.Diff(bRun, hRun)
	side := func(r chstore.EvalRun, cases []evalCaseResult) evalCompareSide {
		sd := evalCompareSide{ID: r.ID, StartedAt: evalTimeString(r.StartedAt), Model: r.Model, PromptVersion: r.PromptVersion,
			AppVersion: r.AppVersion, Pass: int(r.Pass), Fail: int(r.Fail), Total: int(r.Total), Toolset: evalBlobFromRow(r).Toolset}
		sd.ToolCallsMean, sd.TokensMean = evalAgentMeans(cases) // v0.10.1316
		return sd
	}
	bSide, hSide := side(base, bCases), side(head, hCases)
	note := evalCompareNote(rep.Note, evalBlobFromRow(base).Toolset, evalBlobFromRow(head).Toolset)
	note = evalCompareAgentNote(note, bSide.ToolCallsMean, hSide.ToolCallsMean) // v0.10.1316
	cmp := evalCompare{
		Comparable: rep.Comparable, Note: note, Base: bSide, Head: hSide,
		RubricMeanDelta: rep.MeanDelta,
		NewlyFailing:    []evalCompareCase{}, NewlyPassing: []evalCompareCase{},
		Regressed: []evalCompareDelta{}, Improved: []evalCompareDelta{},
		OnlyInBase: []string{}, OnlyInHead: []string{},
	}
	for _, id := range rep.NewlyFailing {
		cmp.NewlyFailing = append(cmp.NewlyFailing, evalCompareCase{ID: id, Surface: surf[id]})
	}
	for _, id := range rep.NewlyPassing {
		cmp.NewlyPassing = append(cmp.NewlyPassing, evalCompareCase{ID: id, Surface: surf[id]})
	}
	for _, d := range rep.Regressed {
		cmp.Regressed = append(cmp.Regressed, evalCompareDelta{ID: d.ID, Surface: surf[d.ID], Before: d.Before, After: d.After})
	}
	for _, d := range rep.Improved {
		cmp.Improved = append(cmp.Improved, evalCompareDelta{ID: d.ID, Surface: surf[d.ID], Before: d.Before, After: d.After})
	}
	cmp.OnlyInBase = append(cmp.OnlyInBase, rep.OnlyInBefore...)
	cmp.OnlyInHead = append(cmp.OnlyInHead, rep.OnlyInAfter...)
	return cmp
}

// evalCompareNote — v0.10.1311, SAF: iki koşunun araç seti farklıysa
// Diff notuna tek cümle eklenir. Kıyaslanabilirliği DEĞİŞTİRMEZ: full ↔
// simple kıyası tam da ölçülmek istenen şey (model farkı gibi değil).
// Bir taraf "" (döngü yüzeyi yok / eski koşu) ise not yok.
func evalCompareNote(note, baseTS, headTS string) string {
	if baseTS == "" || headTS == "" || baseTS == headTS {
		return note
	}
	add := fmt.Sprintf("Araç seti farklı: %s → %s (serbest döngü yüzeyleri).", baseTS, headTS)
	if strings.TrimSpace(note) == "" {
		return add
	}
	return strings.TrimRight(note, " ") + " " + add
}

// ── Katalog ─────────────────────────────────────────────────────────────

// evalAppVersion — imajın gerçek kimliği (buildVersion), yoksa gösterim
// sürümü, yoksa "dev" (getVersion ile aynı düşüş).
func (s *Server) evalAppVersion() string {
	switch {
	case s.buildVersion != "":
		return s.buildVersion
	case s.version != "":
		return s.version
	}
	return "dev"
}

// evalProfileFor — yüzeyin üretimde gideceği profil (kimlik + anahtarsız
// görünüm). copilot nil ise boş.
func (s *Server) evalProfileFor(surface string) (id, label, provider, model, baseURL string) {
	id = s.copilot.SurfaceProfileID(evalProfileLabel(surface))
	label, provider, model, baseURL, _ = s.copilot.ProfileView(id)
	return id, label, provider, model, baseURL
}

// evalToolsetFor — v0.10.1311: yüzeyin araç seti CANLI çözümle — ctx'teki
// profil (WithProfile) ya da yüzey eşlemesi → copilot.ToolsetFor; açık
// seçim kazanır (evalCaseToolset). Döngü dışı yüzey "".
func (s *Server) evalToolsetFor(ctx context.Context, surface, override string) string {
	if !evalLoopSurface(surface) {
		return ""
	}
	return evalCaseToolset(surface, override, s.copilot.ToolsetFor(ctx))
}

// evalRunToolset — v0.10.1311: koşu düzeyi araç seti (özet + panel). Planda
// döngü yüzeyi yoksa "" (araç seti hiçbir vakaya uygulanmadı). Döngü
// yüzeylerinin hepsi aynı profil etiketine ("chat") gider → ilk döngü
// vakasının profiliyle çözülür; vaka başı değer runEvalJob'da AYNI yoldan.
func (s *Server) evalRunToolset(plan []evalCase, override string) string {
	for _, c := range plan {
		if !evalLoopSurface(c.Surface) {
			continue
		}
		ctx := context.Background()
		if pid, _, _, _, _ := s.evalProfileFor(c.Surface); pid != "" {
			ctx = copilot.WithProfile(ctx, pid)
		}
		return s.evalToolsetFor(ctx, c.Surface, override)
	}
	return ""
}

// evalsetCatalog — GET /api/ai/evalset/catalog gövdesi. `ready` bir KAPI
// DEĞİL, durum alanı: katalog AI kapalıyken de cevap verir ki panel "Koş"
// düğmesinin NEDEN pasif olduğunu söyleyebilsin (insight kartının AIOff
// duruşu). 503 kapısı yalnız POST koşuda, ai_evalset_runs.go'daki kayıtta
// requireCopilot ile. Bu yüzden bu yardımcı handler dosyasında değil
// (TestNoInlineCopilotGates handler-içi 503 kopyasını tarar; burada 503 yok).
func (s *Server) evalsetCatalog() (evalCatalog, error) {
	cases, err := evalsetCasesLoader()
	if err != nil {
		return evalCatalog{}, err
	}
	defaultPID, _, _, _, _ := s.evalProfileFor("") // startAIEvalsetRun'daki çağrının AYNISI
	cat := evalCatalog{
		Ready: s.copilotReady(), AppVersion: s.evalAppVersion(), PromptVersion: copilot.PromptVersion(),
		DefaultProfileID: defaultPID, Toolset: s.evalRunToolset(cases, ""), Total: len(cases), Surfaces: []evalCatalogSurface{},
		AgentCases: evalAgentCaseCount(cases), // v0.10.1316
	}
	for _, sc := range evalSurfaceCounts(cases) {
		id, label, provider, model, base := s.evalProfileFor(sc.Surface)
		cat.Surfaces = append(cat.Surfaces, evalCatalogSurface{
			Surface: sc.Surface, Cases: sc.Cases,
			ProfileID: id, ProfileLabel: label, Provider: provider, Model: model, BaseURL: base,
		})
	}
	return cat, nil
}
