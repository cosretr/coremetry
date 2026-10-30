package api

// trace_explain_latency_owner.go — v0.10.1630 (CoSRE kritik yol planı R3,
// operatör onaylı): trace açıklamasının cevabına mekanik "dış bağımlılık
// bekleme: <hedef> <süre>" satırı.
//
// Kanıt KRİTİK YOL bloğunu taşır (explain_trace_critpath.go) ve istem kuralı
// (copilot.systemTraceBody "LATENCY OWNER IS PRECOMPUTED") modelden bu satırla
// başlamasını ister; küçük yerel modeller kalın başlıkla başlayabilir. "Sunucu
// toplar, model anlatır" (sloAnswerWithBurnVerdict emsali): satır kanıt
// METNİNDEN (tracechain.ExternalWaitLineFromBlock) kurulur — evalset aynı
// fonksiyonu aynı girdiyle çağırır (evalServerCompose "Trace"), puanlanan
// operatörün gördüğüdür. Önbellek anahtarı değişmez; cevap ilk satırı zaten
// doğru satırsa aynen, yanlış süre/hedefle yazılmışsa o satır düzeltilir.
// Dış bekleme yoksa (ya da blok yoksa) cevap bayt bayt aynı.

import (
	"context"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/tracechain"
)

// traceAnswerWithLatencyOwner — SAF: evidence = modele giden kullanıcı metni.
func traceAnswerWithLatencyOwner(evidence, answer string) string {
	out, _ := composeLatencyOwner(evidence, answer)
	return out
}

// v0.10.1637 — sunucunun cevaba ne yaptığı (prefix_corrected ayrıntısı).
const (
	prefixActionAdded     = "added"     // model satırı yazmadı → başa kondu
	prefixActionCorrected = "corrected" // model yanlış değerle yazdı → düzeltildi
)

// latencyOwnerEvent — v0.10.1637: bir kompozisyonun sayılabilir sonucu.
// Applied = kural uygulandı (KRİTİK YOL'un en büyük paylı adımı dış bekleme,
// cevap boş değil) — haftalık oranın paydası. Action "" = cevap aynen.
type latencyOwnerEvent struct {
	Applied   bool
	Action    string
	TraceID   string
	Target    string
	Computed  string // kanıttan kurulan satır
	ModelLine string // yalnız corrected: modelin yazdığı ilk satır (normalize)
}

var traceIDInEvidenceRE = regexp.MustCompile(`(?m)^Trace (\S+) with \d+ spans`)

// composeLatencyOwner — SAF: traceAnswerWithLatencyOwner'ın gövdesi + olay.
func composeLatencyOwner(evidence, answer string) (string, latencyOwnerEvent) {
	line := tracechain.ExternalWaitLineFromBlock(evidence)
	if line == "" || strings.TrimSpace(answer) == "" {
		return answer, latencyOwnerEvent{}
	}
	ev := latencyOwnerEvent{Applied: true, Computed: line, Target: latencyLineTarget(line)}
	if m := traceIDInEvidenceRE.FindStringSubmatch(evidence); m != nil {
		ev.TraceID = m[1]
	}
	first, rest, _ := strings.Cut(answer, "\n")
	norm := strings.TrimSpace(strings.Trim(strings.TrimSpace(first), "*_#> "))
	if norm == line {
		return answer, ev
	}
	if strings.HasPrefix(norm, tracechain.ExternalWaitPrefix) || strings.HasPrefix(norm, "Dış bağımlılık bekleme:") {
		ev.Action, ev.ModelLine = prefixActionCorrected, norm
		return line + "\n" + rest, ev // model yazdı ama değer kanıttakiyle aynı değil
	}
	ev.Action = prefixActionAdded
	return line + "\n\n" + answer, ev
}

// latencyLineTarget — SAF: "dış bağımlılık bekleme: <hedef> <sayı> <birim>"
// → <hedef> (hedef boşluk taşıyabilir: "POST /charge").
func latencyLineTarget(line string) string {
	f := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, tracechain.ExternalWaitPrefix)))
	if len(f) <= 2 {
		return strings.Join(f, " ")
	}
	return strings.Join(f[:len(f)-2], " ")
}

// traceComposeRun — ✨ düğmesinin üretim yarısını sarar: üretilen (ve
// önbelleğe yazılan) metin mekanik satırı taşır. v0.10.1637: sonuç sayılır
// (recordLatencyOwner, fire-and-forget) — önbellek isabeti run'ı çağırmaz,
// yani olay yalnız ÜRETİLEN cevapta yazılır (pay ve payda aynı yoldan).
func (s *Server) traceComposeRun(ctx context.Context, run explainRun, evidence string) explainRun {
	return func(onDelta func(string)) (string, error) {
		out, err := run(onDelta)
		if err != nil {
			return out, err
		}
		return s.composeAndRecordLatencyOwner(ctx, evidence, out), nil
	}
}

// composeAndRecordLatencyOwner — kompozisyon + olay kaydı; dönen metin
// traceAnswerWithLatencyOwner ile bayt bayt aynı (kayıt sonucu okunmaz).
func (s *Server) composeAndRecordLatencyOwner(ctx context.Context, evidence, answer string) string {
	out, ev := composeLatencyOwner(evidence, answer)
	s.recordLatencyOwner(ctx, ev)
	return out
}

// streamTraceExplainCore / streamTraceExplainCodeCore — sohbet içi trace
// açıklamasının (chat_explain_turn.go, trace_explain_unified.go) çekirdekleri:
// streamExplainCore / streamExplainCodeCore'un AYNISI, yalnız üretilen metin
// önbelleğe yazılmadan ÖNCE mekanik satırı alır — düğme ile sohbet aynı
// anahtarı paylaşır (v0.10.1400/1406), ikisi de aynı metni saklar.
func (s *Server) streamTraceExplainCore(ctx context.Context, emit func(string, any), surface, system, user, cacheKey string, fresh bool) (explainCoreResult, bool) {
	return s.explainCoreRun(ctx, emit, cacheKey, fresh, s.traceComposeRun(ctx, func(onDelta func(string)) (string, error) {
		return s.copilotStreamSurface(ctx, surface, system, user, onDelta)
	}, user))
}

func (s *Server) streamTraceExplainCodeCore(ctx context.Context, emit func(string, any), surface string, p codeExplainPrep, user string, fresh bool) (explainCoreResult, bool) {
	return s.explainCoreRun(ctx, emit, p.CacheKey, fresh, func(onDelta func(string)) (string, error) {
		out, err := s.copilotExplainEvidenceCtx(ctx, surface, p.SystemNoCode, p.SystemWithCode, user, p.CC, p.SE)
		if err != nil {
			return out, err
		}
		out = s.composeAndRecordLatencyOwner(ctx, user, out)
		onDelta(out)
		return out, nil
	})
}
