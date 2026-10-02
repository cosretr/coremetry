package api

// chat_exception_followup.go — v0.10.1053 (operatör: "Exception panelindeki
// takip sohbeti de kod okuyabilsin."): exception öznesinin panel takibine
// read_source_code — TEK araçlı döngüyle.
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// v0.10.1050'den beri trace/span takibi read_source_code ile kod okuyor
// (chat_source_code.go). Exception'ın "Explain"inden açılan panelde sorulan
// takip ("bu metodun devamında ne var?", "X sınıfına da bak") ise
// drawerTraceFollowUp'a girmiyor, çekmece anlatımına (copilot_drawer.go)
// düşüyordu: açıklama ≤3000 rune + HAM KANIT (exception grubu, temsilî stack,
// örnek trace, loglar, deploy, pod) + konuşma, araçsız TEK çağrı. Kod okuma
// yolu yoktu (DECISIONS v0.10.1050 "kabul edilen kalıntılar").
//
// ── KARAR ───────────────────────────────────────────────────────────────
//
// Araç bu çağırana SUNULABİLİYORSA — sohbetin kendi kapıları: DevOps bağlı
// (ChatToolList), aracın MinRole'ü (toolsForRole), oturum kullanıcısı
// (sourceCodeToolsFor) — exception takibi AYNI prompt ve bağlamla koşar
// (SystemPromptDrawerChat + drawerNarrationUser, bayt bayt), önüne yalnız
// SourceCodeChatAddendum eklenir ve modele YALNIZ read_source_code sunulur.
// Döngü serbest döngünün parçalarıyla kurulur: tur ve çağrı tavanı, Executor
// (tekrar muhafızı, 20 s bütçe, audit, ai.tool span'ı), step/step-result,
// maskeler (önizleme ve ai_calls yalnız referans), tavan turu. Cevap
// sözleşmesi çekmeceninki: sayı denetimi YOK (exceptionCodeAnswerTR).
//
// Tam yerli katalog DEĞİL: küçük modelde "schema soup" (v0.10.172/194);
// exception takibinin bugünkü kanıtı zaten prompt'ta, eksik olan yalnız kod.
// Dış MCP araçları da YOK — kod üçüncü bir tarafa çıkamaz.
//
// Sunulamıyorsa (DevOps yok / token / rol) ya da grup okunamadıysa her şey
// bayt bayt eski tek çağrı (TestExceptionFollowUpFallbackByteIdentical
// pinler). Döngü cevap üretemezse (araç tanımını reddeden uç, küçültülemeyen
// taşma, tavan turu hatası; bağlam canlıyken) TEK düz çiple aynı tek çağrıya
// düşer — DevOps'lu kurulumda exception takibi hiçbir zaman bugünkünden kötü
// bitmez. İstemci iptali düşmez.
//
// ── KAPSAM ve SÜRÜM ─────────────────────────────────────────────────────
//
// Servis YALNIZ exception'ın kendi kod incelemesinin okuyacağı servisler:
// grubun servisi ve explain'in seçtiği stack'i basan servis
// (ExceptionExplainInput.CodeService — log-fallback'te logu atan servis).
// Örnek trace'teki öteki servisler kapsam DIŞI: trace takibinin "trace'in
// parçası" sınırı burada geniş kalırdı. Sürüm o stack'i veren olayın çalışan
// sürümü (StackVersion, v0.10.1044) ve YALNIZ o servis için; grup servisi
// stack servisinden farklıysa dal. İkisi de çekmecenin her takipte ZATEN
// kurduğu explain girdisinden (anomaly.BuildExceptionExplainInput) — ek CH/ES
// okuması yok. Grup okunamadıysa döngüye hiç girilmez (eski anlatım); araç
// yine de "exception okunamadı" ıskasıyla savunur, git'e istek yok.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// exceptionCodeSurface — döngünün ai_calls / profil yüzeyi: exception takibi
// bugün olduğu gibi çekmece yüzeyinde sayılır (/ai, yüzey→profil eşlemesi).
const exceptionCodeSurface = "chat-drawer"

// sourceExceptionScope — exception öznesinin kod kapsamı (alışveriş ctx'inde).
// loaded=false → grup okunamadı: her çağrı dürüst ıska.
type sourceExceptionScope struct {
	loaded       bool
	groupService string // grubun servisi
	codeService  string // ExceptionExplainInput.CodeService — explain'in kod servisi
	codeVersion  string // StackVersion — YALNIZ codeService için
}

type sourceExceptionKey struct{}

// withSourceExceptionScope — exception takibinin kapsamını araç ctx'ine bağlar.
func withSourceExceptionScope(ctx context.Context, sc *sourceExceptionScope) context.Context {
	if sc == nil {
		return ctx
	}
	return context.WithValue(ctx, sourceExceptionKey{}, sc)
}

// exceptionCodeScopeFrom — SAF: grup + explain girdisinden kapsam. g nil →
// okunamadı. Kural explain'in "Kodu da incele"siyle AYNI (CodeService +
// StackVersion); örnek/stack seçimi burada yeniden yapılmaz.
func exceptionCodeScopeFrom(g *chstore.ExceptionGroup, in anomaly.ExceptionExplainInput) *sourceExceptionScope {
	if g == nil {
		return &sourceExceptionScope{}
	}
	return &sourceExceptionScope{
		loaded: true, groupService: g.Service,
		codeService: in.CodeService(g.Service), codeVersion: in.StackVersion,
	}
}

// services — SAF: izinli servisler (tekil, sıralı; boş ad yok).
func (sc *sourceExceptionScope) services() []string {
	var out []string
	for _, v := range []string{sc.groupService, sc.codeService} {
		if v != "" && !containsString(out, v) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// decide — SAF: servis için (sürüm, ıska, gerekçe, argüman hatası). Kapsam
// dışı servis argüman hatasıdır (trace dalıyla aynı sınıf) ve istek atılmaz.
func (sc *sourceExceptionScope) decide(svc string) (version string, miss devops.SourceOutcome, reason string, err error) {
	if !sc.loaded {
		return "", devops.SourceExceptionUnreadable, "exception okunamadı — kod kapsamı doğrulanamadığı için kod okunmadı", nil
	}
	allowed := sc.services()
	if !containsString(allowed, svc) {
		return "", "", "", fmt.Errorf("service %q bu exception'ın kod kapsamında değil — yalnız grubun servisi ve stack'i basan servisin kodu okunur: %s", svc, strings.Join(allowed, ", "))
	}
	if svc == sc.codeService {
		return sc.codeVersion, "", "", nil
	}
	return "", "", "", nil // stack'i basmayan grup servisi: başka olayın sürümü yamanmaz → dal
}

// exceptionInputSeam — TEST DİKİŞİ (prod'da nil; chatLoopHandlerSeam emsali):
// store'suz testte exception grubu ve explain girdisi sahte kaynaktan gelir.
var exceptionInputSeam func(ctx context.Context, fp string, loc *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, error)

// drawerExceptionInput — çekmecenin exception kanıtının kaynağı: grup +
// explain'in TEK girdi kurucusu. Grup okunamazsa ok=false.
func (s *Server) drawerExceptionInput(ctx context.Context, fp string, loc *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, bool) {
	if exceptionInputSeam != nil {
		g, in, err := exceptionInputSeam(ctx, fp, loc)
		return g, in, err == nil && g != nil
	}
	g, err := s.store.GetExceptionGroup(ctx, fp)
	if err != nil || g == nil {
		return nil, anomaly.ExceptionExplainInput{}, false
	}
	return g, anomaly.BuildExceptionExplainInput(ctx, s.store, s.logs, g, loc), true
}

// drawerLoopEnv — copilotChat'in alışveriş kaynakları: audit için istek,
// ai.chat span'ı, alışveriş tavanı, ai_calls başlangıcı. Sıfır değer (r nil)
// → döngü kurulmaz, çekmece eski tek çağrıda kalır.
type drawerLoopEnv struct {
	r           *http.Request
	span        *chatSpan
	exchangeMax time.Duration
	started     time.Time
}

// exceptionCodeTools — SAF: exception takibinde modele sunulacak araçlar.
// catalog = ChatToolList(mcpDeps) (DevOps yoksa araç zaten yok); rol süzgeci
// aracın KENDİ MinRole'ü; token / kimliksiz çağıran sourceCodeToolsFor'da
// düşer. Sonra YALNIZ read_source_code kalır. Boş → eski tek-çağrılı anlatım.
func exceptionCodeTools(catalog []mcp.Tool, c *auth.Claims) []mcp.Tool {
	role := ""
	if c != nil {
		role = c.Role
	}
	var out []mcp.Tool
	for _, t := range sourceCodeToolsFor(toolsForRole(catalog, role), true, c) {
		if t.Name == mcptools.SourceCodeToolName {
			out = append(out, t)
		}
	}
	return out
}

// exceptionCodeToolsFor — çağıranın kimliği istekten; env yoksa boş.
func (s *Server) exceptionCodeToolsFor(env drawerLoopEnv) []mcp.Tool {
	if env.r == nil || env.span == nil {
		return nil
	}
	return exceptionCodeTools(mcptools.ChatToolList(s.mcpDeps()), auth.FromContext(env.r.Context()))
}

// exceptionCodeSourceNoteTR — SAF: cevabın deterministik künyesi: çekmecenin
// künyesi aynen + veri döndüren kod okuması (read_source_code) varsa adı.
func exceptionCodeSourceNoteTR(evidenceKind string, calledTools []string) string {
	note := drawerSourceNote(evidenceKind)
	if named := dedupePreserveOrder(calledTools); len(named) > 0 {
		note += " + " + strings.Join(named, ", ") + " (kaynak kod)"
	}
	return note
}

// exceptionCodeAnswerTR — SAF: döngü cevabının son metni — çekmece anlatımının
// cevap sözleşmesi: model metni + deterministik künye. Sayı denetimi YOK:
// çekmece anlatımında hiç yoktu; trace takibindeki gerekçe ("ilk cevap zaten
// denetlendi") exception açıklamasında geçerli değil ve açıklamadaki bir sayıyı
// ya da ekin istediği gibi aynen aktarılan kod sabitini / satır numarasını
// yalnız DevOps'lu kurulumlarda "kanıtta yok" diye işaretlerdi. Modelin kendi
// chart çiti sökülür (serbest döngüyle aynı: doğrulanmamış kapsam canlı grafik
// çizmesin).
func exceptionCodeAnswerTR(modelText, evidenceKind string, calledTools []string) string {
	text, _ := stripModelChartFences(modelText)
	return strings.TrimSpace(text) + exceptionCodeSourceNoteTR(evidenceKind, calledTools)
}

// exceptionCodeFallbackLabel — döngü cevap üretemeyip çekmecenin araçsız tek
// çağrısına düşerken operatöre görünen TEK düz çip.
const exceptionCodeFallbackLabel = "kod okuma kullanılamadı — araçsız yanıt"

// exceptionCodeBudgetLabelTR — SAF: döngü başındaki bütçe çipi. Tek araçlı
// döngüde serbest döngünün "araştırma bütçesi: N araç · M tur" etiketi
// yanıltıcıdır (tek araç var); hak dosya penceresi olarak söylenir.
func exceptionCodeBudgetLabelTR(calls int) string {
	return fmt.Sprintf("kod okuma: en çok %d dosya penceresi", calls)
}

// exceptionCodeBudgetExhaustedLabelTR — SAF: hak dolunca (çağrı tavanı ya da
// son tur) açık not; serbest döngü etiketinin tek araçlı karşılığı.
func exceptionCodeBudgetExhaustedLabelTR(slotsUsed, executed, skipped, roundsUsed int) string {
	out := fmt.Sprintf("kod okuma hakkı doldu (%d/%d çağrı · %d/%d tur) — %d çağrı yürütüldü",
		slotsUsed, chatMaxToolCalls, roundsUsed, chatMaxToolRounds, executed)
	if skipped > 0 {
		out += fmt.Sprintf(", %d yürütülmedi", skipped)
	}
	return out + "; eldeki kanıtla cevaplanıyor"
}

// shrinkKeepingHead — SAF: taşma yeniden denemesinde İLK mesaj (açıklama +
// HAM KANIT + SORU bloğu) korunur, yalnız araç turları shrinkConvForRetry ile
// yarıya iner. Düz shrinkConvForRetry en yenileri tuttuğu için soruyu silerdi.
// Tek araç turunda bölünmeden atılabilecek çift yok (false): taşma o zaman
// araçsız anlatıma düşer (exceptionCodeLoop fallback).
func shrinkKeepingHead(conv []copilot.ChatMessage) ([]copilot.ChatMessage, bool) {
	if len(conv) < 2 {
		return conv, false
	}
	tail, ok := shrinkConvForRetry(conv[1:])
	if !ok {
		return conv, false
	}
	return append([]copilot.ChatMessage{conv[0]}, tail...), true
}

// exceptionCodeLoop — exception takibinin tek araçlı döngüsü. Prompt ve ilk
// kullanıcı bloğu çekmece anlatımının aynısı; döngü serbest döngünün
// sözleşmesiyle (copilot_chat.go) koşar. ai_calls'a döngü başına TEK satır
// (çekmece yüzeyi; düşüşte status=error — /ai arızayı görür).
//
// fallback=true: cevap ÜRETİLMEDİ, istek bağlamı hâlâ canlı ve arıza
// sağlayıcıdan (araç tanımını reddeden uç — araç desteksiz yerel modeller 400
// döner —, küçültülemeyen bağlam taşması, tavan turu hatası). Hata olayı
// YAYINLANMAZ; çağıran bugünkü araçsız anlatıma düşer. İstemci iptali ve
// alışveriş tavanı düşmez: hata yayınlanır, ikinci çağrı yakılmaz.
func (s *Server) exceptionCodeLoop(ctx context.Context, emit func(string, any), env drawerLoopEnv, tools []mcp.Tool,
	msgs []copilot.ChatMessage, question, ex, evidence, evidenceKind string, scope *sourceExceptionScope, ctxService string) (ok, fallback bool) {
	meta := copilot.MetaFromContext(ctx)
	meta.Surface = exceptionCodeSurface
	ctx = copilot.WithMeta(ctx, meta)
	ctx = withSourceExceptionScope(ctx, scope)

	byName := make(map[string]mcp.ToolHandler, len(tools))
	specs := make([]copilot.ToolSpec, 0, len(tools))
	for _, t := range tools {
		byName[t.Name] = t.Handler
		specs = append(specs, copilot.ToolSpec{
			Name: t.Name, Description: t.ChatDescription(), InputSchema: t.InputSchema,
		})
	}
	if chatLoopHandlerSeam != nil { // yalnız testte dolu
		for n, h := range byName {
			byName[n] = chatLoopHandlerSeam(n, h)
		}
	}
	exec := agenttools.NewExecutor(byName, nil, agenttools.Hooks{
		Span: env.span.tool,
		Audit: func(name string, args json.RawMessage, dur time.Duration, err error, bytes int) {
			s.audit(env.r, "mcp.tool.call", "mcp_tool", name, chatToolAuditDetails(name, args, dur, err, bytes))
		},
	})

	// Ek ÖNDE, çekmece çekirdeği arkada: DataNotInstruction sonda kalır.
	system := chatSourceCodePromptTR(tools) + copilot.SystemPromptDrawerChat()
	conv := []copilot.ChatMessage{{Role: "user", Text: drawerNarrationUser(question, ex, msgs, evidence)}}

	var calledTools, codeReads []string
	var totalIn, totalOut, totalCached uint32
	var lastErr error
	var finalText string
	overflowRetried := false
	// modelFailed — sağlayıcı çağrısı hata verdi: bağlam canlıysa düşüş (hata
	// yayınlanmaz), değilse iptal / alışveriş tavanı cümlesi.
	modelFailed := func(err error) {
		lastErr = err
		if ctx.Err() == nil {
			fallback = true
			return
		}
		emit("error", map[string]string{"error": chatCancelledMessageTR(ctx.Err(), env.exchangeMax)})
	}
	answer := func(modelText string) {
		finalText = exceptionCodeAnswerTR(modelText, evidenceKind, calledTools)
		emit("answer", chatAnswerEvent(finalText, meta.ExchangeID, s.answerRequestIDLinks(ctx, finalText, ctxService), ""))
	}

	callsLeft := chatMaxToolCalls
	executedN, skippedN := 0, 0
	emit("step", map[string]string{"label": exceptionCodeBudgetLabelTR(chatMaxToolCalls)})
	for round := 0; round < chatMaxToolRounds; round++ {
		tctx, endTurn := env.span.turn(ctx, round, overflowRetried)
		turn, err := s.copilot.ChatWithTools(tctx, system, conv, specs)
		endTurn(turn.InputTokens, turn.OutputTokens, turn.CachedTokens, err)
		if turn.ToolCallsFromText {
			log.Printf("[chat] tool çağrısı METİNDEN ayrıştırıldı (n=%d) — sunucu tool_calls üretmedi", len(turn.ToolCalls))
			emit("step", map[string]string{"label": "tool çağrısı metinden ayrıştırıldı (sunucu parser yok)"})
		}
		totalIn += turn.InputTokens
		totalCached += turn.CachedTokens
		totalOut += turn.OutputTokens
		if err != nil && isContextOverflowErr(err) && !overflowRetried {
			if shrunk, ok := shrinkKeepingHead(conv); ok {
				overflowRetried = true
				conv = shrunk
				emit("step", map[string]string{"label": "bağlam taştı — geçmiş küçültülüp yeniden deneniyor"})
				round--
				continue
			}
		}
		if err != nil {
			modelFailed(err)
			break
		}
		if len(turn.ToolCalls) == 0 {
			answer(turn.Text)
			break
		}
		conv = append(conv, copilot.ChatMessage{
			Role: "assistant", Text: turn.Text, ToolCalls: turn.ToolCalls, RawContent: turn.RawContent,
		})
		results := make([]copilot.ToolResult, 0, len(turn.ToolCalls))
		run, over := splitByCallBudget(turn.ToolCalls, callsLeft)
		callsLeft -= len(run)
		cancelled := false
		for _, tc := range run {
			stepN := emitStepChip(emit, tc.Name, string(tc.Input))
			oc := exec.Call(ctx, tc.Name, tc.Input)
			if !oc.Executed {
				// bilinmeyen ad (yalnız read_source_code var) / tekrar / iptal.
				if oc.Kind == agenttools.KindCancelled {
					cancelled = true
				}
				emit("step-result", skippedStepResult(stepN, tc.Name, oc.Content, oc.Kind))
				results = append(results, copilot.ToolResult{CallID: tc.ID, Name: tc.Name, IsError: true, Content: oc.Content})
				skippedN++
				continue
			}
			executedN++
			tr := copilot.ToolResult{CallID: tc.ID, Name: tc.Name, IsError: oc.IsError, Content: oc.Content}
			srcs := stepSourceStatuses(tr.Content)
			if !tr.IsError && !stepSourcesAllFailed(srcs) {
				calledTools = append(calledTools, tc.Name)
			}
			// Kod tele YALNIZ referans olarak çıkar; ai_calls'a maskeli özet.
			preview, truncated := chatStepPreview(tc.Name, tr.Content, tr.IsError)
			if sum := chatCodeReadSummary(tc.Name, tr.Content, tr.IsError); sum != "" {
				codeReads = append(codeReads, sum)
			}
			stepEv := map[string]any{
				"i": stepN, "tool": tc.Name, "ok": !tr.IsError,
				"preview": preview, "truncated": truncated, "bytes": len(tr.Content),
				"durationMs": oc.Duration.Milliseconds(),
			}
			if srcs != nil && !tr.IsError {
				stepEv["sources"] = srcs
			}
			emit("step-result", stepEv)
			tr.Content, _ = clampToolResultForModel(tr.Content)
			results = append(results, tr)
		}
		for _, tc := range over {
			n := emitStepChip(emit, tc.Name, string(tc.Input))
			emit("step-result", skippedStepResult(n, tc.Name, chatCallCapContent, skipReasonCallCap))
			results = append(results, copilot.ToolResult{CallID: tc.ID, Name: tc.Name, IsError: true, Content: chatCallCapContent})
		}
		skippedN += len(over)
		if cancelled || ctx.Err() != nil {
			lastErr = ctx.Err()
			emit("error", map[string]string{"error": chatCancelledMessageTR(lastErr, env.exchangeMax)})
			break
		}
		conv = append(conv, copilot.ChatMessage{Role: "user", ToolResults: results})
		if round == chatMaxToolRounds-1 || callsLeft <= 0 {
			emit("step", map[string]string{"label": exceptionCodeBudgetExhaustedLabelTR(chatMaxToolCalls-callsLeft, executedN, skippedN, round+1)})
			tctx2, endTurn2 := env.span.turn(ctx, round, false)
			turn2, err2 := s.copilot.ChatWithTools(copilot.WithNoToolCalls(tctx2), system+copilot.ChatRoundCapAddendum(), conv, specs)
			endTurn2(turn2.InputTokens, turn2.OutputTokens, turn2.CachedTokens, err2)
			totalIn += turn2.InputTokens
			totalCached += turn2.CachedTokens
			totalOut += turn2.OutputTokens
			if err2 != nil {
				modelFailed(err2)
			} else {
				answer(turn2.Text)
			}
			break
		}
	}

	status, errMsg := "ok", ""
	if lastErr != nil {
		status, errMsg = "error", lastErr.Error()
	}
	// Düşüşte kök span'ın sonucu anlatımınkidir (tier); döngünün hatası tur
	// span'ında ve ai_calls satırında kalır.
	spanErr := lastErr
	if fallback {
		spanErr = nil
	}
	env.span.finish(totalIn, totalOut, totalCached, spanErr)
	s.copilot.RecordUsage(ctx, env.started, totalIn, totalOut, totalCached, status, errMsg,
		chatPromptSample(lastUserText(msgs), codeReads), finalText)
	return lastErr == nil, fallback
}
