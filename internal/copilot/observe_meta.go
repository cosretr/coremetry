package copilot

// observe_meta.go — v0.10.409 (CoSRE denetimi E2/O3/O4): ai_calls'a giren
// prompt sürümü, hata sınıfı ve akış istatistikleri.

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
)

// promptVersionRegistry — prompts.go'daki modele giden HER metin sabiti
// (ad → değer). Sürüm = sıralı adların ve değerlerin FNV-64a'sı; bir prompt
// değişince ai_calls.prompt_version değişir ve /ai kırılımı "önce/sonra"
// karşılaştırmasını veriyle yapar. observe_meta_test AD üzerinden pinler:
// prompts.go'da literal taşıyan her `const` burada olmak zorunda (bileşik
// sabitler — systemChatRoundCap gibi kuyruk literali olanlar — dahil;
// saf bileşimler parçalarıyla kapsanır).
var promptVersionRegistry = map[string]string{
	"systemTraceBody":            systemTraceBody,
	"traceFollowUpAddendum":      traceFollowUpAddendum, // v0.10.948; v0.10.972 — aynı başlıklar
	"systemSpan":                 systemSpan,
	"systemProblem":              systemProblem,
	"systemExceptionBody":        systemExceptionBody,   // v0.10.1370 — örnek trace logları; v0.10.1379 — HATA ZİNCİRİ kuralı
	"systemOracleException":      systemOracleException, // v0.10.1100 — Oracle hata grubu; v0.10.1370 — örnek trace logları; v0.10.1379 — HATA ZİNCİRİ
	"systemIncident":             systemIncident,
	"systemAnomaly":              systemAnomaly,
	"systemServiceHealth":        systemServiceHealth,
	"systemRunbook":              systemRunbook,
	"systemCompareTraces":        systemCompareTraces,
	"systemDeployImpact":         systemDeployImpact,
	"systemSLOBurn":              systemSLOBurn,
	"systemServiceTags":          systemServiceTags,
	"systemSlowQuery":            systemSlowQuery,
	"systemNLToQuery":            systemNLToQuery,
	"systemCHQueryOptimize":      systemCHQueryOptimize,
	"systemRCAVerdict":           systemRCAVerdict,
	"systemCodeAddendum":         systemCodeAddendum,
	"systemServiceCharts":        systemServiceCharts,
	"systemShiftSummary":         systemShiftSummary,
	"systemAlertNoise":           systemAlertNoise,
	"systemLogPatterns":          systemLogPatterns,
	"systemLogSpike":             systemLogSpike,         // v0.10.1284 — AI SRE Faz 1a
	"systemLogLine":              systemLogLine,          // v0.10.1291 — ✨ log satırı
	"systemSREInvestigation":     systemSREInvestigation, // v0.10.1287 — AI SRE Faz 1b
	"systemPostmortem":           systemPostmortem,
	"systemRunbookUpdate":        systemRunbookUpdate,
	"systemServiceReport":        systemServiceReport, // v0.10.1337 — guided servis raporu
	"DataNotInstruction":         DataNotInstruction,
	"systemGuidedChat":           systemGuidedChat,
	"latencyEvidenceRule":        latencyEvidenceRule, // v0.10.1299 — guided / serbest döngü / AI SRE ortak kuralı
	"systemDrawerChat":           systemDrawerChat,
	"systemCaseCompare":          systemCaseCompare,
	"systemGroundingJudge":       systemGroundingJudge,
	"systemRAGChat":              systemRAGChat,
	"ChatAnswerStyle":            ChatAnswerStyle,       // v0.10.1137 — ortak sohbet cevap biçimi
	"systemWikiChat":             systemWikiChat,        // v0.10.1124
	"systemWikiFollowUp":         systemWikiFollowUp,    // v0.10.1134 — wiki takip sorusu
	"ragChatCore":                ragChatCore,           // v0.10.1136 — RAG gövdesi (wiki eki bunun üstüne)
	"systemRAGChatWiki":          systemRAGChatWiki,     // v0.10.1136 — RAG + wiki atıf eki
	"systemWikiSelect":           systemWikiSelect,      // v0.10.1136 — iki aşamalı okuma: sayfa seçimi
	"WikiNotInPageSentinel":      WikiNotInPageSentinel, // v0.10.1134 — takip "sayfada yok" işareti
	"systemChat":                 systemChat,
	"systemChatRoundCap":         systemChatRoundCap,
	"systemChatRoundCapAddendum": systemChatRoundCapAddendum, // v0.10.806
	"chatDegenRetryAddendum":     chatDegenRetryAddendum,     // v0.10.1324 — yineleme sonrası araçsız deneme
	"chatDeepAddendum":           chatDeepAddendum,           // v0.10.1150 — Derin düşün üslubu
	"chatDeepLoopBudget":         chatDeepLoopBudget,         // v0.10.1150 — Derin düşün araç bütçesi
	"sourceCodeChatAddendum":     sourceCodeChatAddendum,
	"codeTurnAddendum":           codeTurnAddendum,        // v0.10.1461 — R2 kod turu
	"codeResolveChatAddendum":    codeResolveChatAddendum, // v0.10.1210
	"codeSearchChatAddendum":     codeSearchChatAddendum,  // v0.10.1210     // v0.10.1050 — read_source_code sunulunca
	"wikiChatAddendum":           wikiChatAddendum,        // v0.10.1122 — search_wiki / read_wiki_page sunulunca
	"cosreRulesAddendum":         cosreRulesAddendum,      // v0.10.1289 — operatör kuralı bloğu varken
	"systemRuleProposal":         systemRuleProposal,      // v0.10.1289 — "bunu hatırla" kural önerisi
	"systemChatAgentLoop":        systemChatAgentLoop,     // v0.10.482
	"systemIntentClassify":       systemIntentClassify,
	"IntentNoInstructionLine":    IntentNoInstructionLine,
	"systemGeneralChat":          systemGeneralChat,
	"systemWikiGeneral":          systemWikiGeneral,     // v0.10.1550
	"systemRecordsAnswer":        systemRecordsAnswer,   // v0.10.1553
	"systemOffTopicGeneral":      systemOffTopicGeneral, // v0.10.1458
	"AnswerInTurkish":            AnswerInTurkish,

	// v0.10.1330 — stack yokken operasyon kodu eki (kendi hizalama grubu).
	"systemOperationCodeAddendum": systemOperationCodeAddendum,
	// v0.10.1349 — operasyon fonksiyon kodu sözlüğünden ADAY eki.
	"systemOperationCandidateAddendum": systemOperationCandidateAddendum,

	// v0.10.1302 — simple araç seti varyantları (*.simple; ToolsetText ile türer).
	"systemChatAgentLoop.simple":   SystemPromptChatAgentLoopFor(true),
	"traceFollowUpAddendum.simple": TraceFollowUpAddendumFor(true),

	// v0.10.1331 — operasyonun Exceptions grubu varken serbest döngü eki.
	"operationExceptionsAddendum": operationExceptionsAddendum,
	// v0.10.1572 — Oracle grubu açıklamasının trace sentezi eki.
	"oracleTraceSynthesisAddendum": oracleTraceSynthesisAddendum,

	// v0.10.1374 — endpoint küme kıyası varken serbest döngü eki.
	"operationClusterCompareAddendum": operationClusterCompareAddendum,

	// v0.10.1431 — guided kaynak incelemesi anlatıcısı (kendi hizalama grubu).
	"systemResourceInvestigation": systemResourceInvestigation,

	// v0.10.1442 — kaynak sorusu ön-sınıflandırıcısı (niyet kademesi içi, JSON).
	"systemResourceClassify": systemResourceClassify,
}

var (
	promptVersionOnce sync.Once
	promptVersionVal  string
)

// PromptVersion — 16 hex; süreç ömrü boyunca sabit, süreçler arası
// deterministik (anahtarlar sıralı yazılır).
func PromptVersion() string {
	promptVersionOnce.Do(func() {
		keys := make([]string, 0, len(promptVersionRegistry))
		for k := range promptVersionRegistry {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		h := fnv.New64a()
		for _, k := range keys {
			h.Write([]byte(k))
			h.Write([]byte{0})
			h.Write([]byte(promptVersionRegistry[k]))
			h.Write([]byte{0})
		}
		promptVersionVal = fmt.Sprintf("%016x", h.Sum64())
	})
	return promptVersionVal
}

// Hata sınıfları — LowCardinality; /ai "kaç timeout, kaç refusal".
const (
	ErrClassTimeout         = "timeout"
	ErrClassQuota           = "quota"
	ErrClassRefusal         = "refusal"
	ErrClassContextOverflow = "context_overflow"
	ErrClassHTTP4xx         = "http_4xx"
	ErrClassHTTP5xx         = "http_5xx"
	ErrClassUnavailable     = "unavailable"
	// ErrClassCancelled — istemci iptali (çekmece kapandı, sekme gitti);
	// sağlayıcı zaman aşımı DEĞİL (inceleme bulgusu: ikisi aynı kovaya
	// düşünce /ai "timeout" sayısı operatör davranışını ölçüyordu).
	ErrClassCancelled = "cancelled"
	ErrClassOther     = "other"
	// ErrClassLocalOnly — v0.10.1451 (karar 8, R1): kod/wiki içeriği taşıyan
	// istek LocalOnly olmayan profile gidecekti; sağlayıcıya tek bayt gitmedi.
	ErrClassLocalOnly = "local_only"
)

// httpStatusRe — metin yolunda durum kodu: boşluk/iki nokta/parantezle
// sınırlı 3 hane ("turn 5: openai 502: bad gateway" → 502; "450ms" değil).
var httpStatusRe = regexp.MustCompile(`(?:^|[\s:(])([45][0-9]{2})(?:[\s:)]|$)`)

// ClassifyAIError — typed hata → sınıf; typed değilse metne düşer.
func ClassifyAIError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return ErrClassCancelled
	}
	if errors.Is(err, ErrLocalOnly) { // v0.10.1451
		return ErrClassLocalOnly
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrClassTimeout
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return ErrClassTimeout
	}
	var operr *net.OpError
	if errors.As(err, &operr) {
		return ErrClassUnavailable
	}
	var herr *aiprov.HTTPError
	if errors.As(err, &herr) {
		switch {
		case herr.Status == 429:
			return ErrClassQuota
		case herr.Status >= 500:
			return ErrClassHTTP5xx
		case herr.Status >= 400:
			if c := ClassifyAIErrorText(herr.Body); c == ErrClassContextOverflow {
				return c
			}
			return ErrClassHTTP4xx
		}
	}
	return ClassifyAIErrorText(err.Error())
}

// ClassifyAIErrorText — yalnız mesaj varken (chat.go RecordUsage).
func ClassifyAIErrorText(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case m == "":
		return ""
	case strings.Contains(m, localOnlyErrMarker): // v0.10.1451 — ErrLocalOnly metni (sohbet RecordUsage)
		return ErrClassLocalOnly
	case strings.Contains(m, "refusal") || strings.Contains(m, "reddetti"):
		return ErrClassRefusal
	case strings.Contains(m, "context length") || strings.Contains(m, "maximum context") ||
		strings.Contains(m, "context window") || strings.Contains(m, "too many tokens") || strings.Contains(m, "prompt is too long"):
		return ErrClassContextOverflow
	case strings.Contains(m, "context canceled") || strings.Contains(m, "operation was canceled"):
		return ErrClassCancelled
	case strings.Contains(m, "deadline exceeded") || strings.Contains(m, "timeout") || strings.Contains(m, "timed out"):
		return ErrClassTimeout
	case isQuotaErr(errors.New(msg)):
		return ErrClassQuota
	case strings.Contains(m, "connection refused") || strings.Contains(m, "no such host") || strings.Contains(m, "connection reset"):
		return ErrClassUnavailable
	}
	if sm := httpStatusRe.FindStringSubmatch(m); sm != nil {
		switch {
		case sm[1] == "429":
			return ErrClassQuota
		case sm[1][0] == '5':
			return ErrClassHTTP5xx
		default:
			return ErrClassHTTP4xx
		}
	}
	return ErrClassOther
}

// streamStats — StreamText çağrısı başına ilk-token damgası + düşüş bayrağı.
type streamStats struct {
	mu       sync.Mutex
	started  time.Time
	first    time.Time
	fallback bool
	// cached — v0.10.807 (dış skill denetimi L2): sağlayıcının bildirdiği
	// önek-önbellek giriş token'ı (son yazan kazanır: akış düşüşünde
	// buffered çağrının değeri). Tuple imzaları (metin, in, out, err)
	// değişmesin diye v0.10.409'un TTFT taşıyıcısıyla aynı kanal.
	cached uint32
}

// noteCachedTokens — sağlayıcı cevabı geldiği yerde çağrılır; taşıyıcı
// yoksa (ChatWithTools yolu ChatTurn.CachedTokens ile doğrudan taşır) sessiz.
func noteCachedTokens(ctx context.Context, n int) {
	if st := streamStatsFromContext(ctx); st != nil {
		st.mu.Lock()
		st.cached = clampTokens(n)
		st.mu.Unlock()
	}
}

func (st *streamStats) cachedTokens() uint32 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.cached
}

func (st *streamStats) markFirst() {
	st.mu.Lock()
	if st.first.IsZero() {
		st.first = time.Now()
	}
	st.mu.Unlock()
}

func (st *streamStats) ttftMs() uint32 {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.first.IsZero() {
		return 0
	}
	return uint32(st.first.Sub(st.started).Milliseconds())
}

// fallbackSet — kilit altında okuma (yazıcı markStreamFallback ile aynı kilit).
func (st *streamStats) fallbackSet() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.fallback
}

type streamStatsKey struct{}

func withStreamStats(ctx context.Context, st *streamStats) context.Context {
	return context.WithValue(ctx, streamStatsKey{}, st)
}

func streamStatsFromContext(ctx context.Context) *streamStats {
	st, _ := ctx.Value(streamStatsKey{}).(*streamStats)
	return st
}

func markStreamFallback(ctx context.Context) {
	if st := streamStatsFromContext(ctx); st != nil {
		st.mu.Lock()
		st.fallback = true
		st.mu.Unlock()
	}
}
