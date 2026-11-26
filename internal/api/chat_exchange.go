package api

// chat_exchange.go — v0.10.1153 (operatör, prod): "/ai (AI insights) her
// CoSRE etkileşimini göstermiyor".
//
// Kök neden: /ai yalnız ai_calls satırlarını okuyor ve o tabloya yalnız
// MODEL ÇAĞRILARI yazılıyordu. Bir sohbet turunun kendisi hiçbir yerde
// kaydedilmiyordu:
//   - LLM'siz cevaplar (/help, bilinmeyen komut, kapsam "bunu mu demek
//     istedin", /wiki kapı mesajları, "Wikide bulunamadı", guided şablonları,
//     netleştirme, önbellekten gelen trace anlatımı …) HİÇ satır üretmiyordu;
//   - yardımcı çağrılar (niyet sınıflandırıcısı, wiki sayfa seçimi) bilerek
//     exchange'siz yazılıyordu (geri bildirim JOIN'i ikiye katlanmasın diye) —
//     /ai'da turla ilişkisiz satırlar olarak görünüyordu;
//   - kademe, rota, Derin bayrağı ve sonucu hiçbir satır taşımıyordu.
//
// Çözüm (yeni tablo/kolon YOK, mevcut kaydedici): her tur, kademesi ne olursa
// olsun, sonunda ai_calls'a TEK bir etkileşim satırı yazar
// (copilot.RecordExchange — RecordUsage'ın ikizi; aynı Recorder, aynı 4 KB
// tavan, 90 g TTL). Yüzey aisurface.TurnLabel (kademe/rota/derin), exchange
// kimliği aisurface.TurnExchangeID(kök). Turun model çağrıları kökle
// gruplanır: cevap çağrısı kökü AYNEN, yardımcı/işaret satırları "kök:yüzey"
// taşır. /api/ai/exchanges (ai_exchanges.go) bunları birleştirir.
//
// Kayıt merkezî: copilotChat'in tek defer'i — yeni bir kademe eklemek kaydı
// unutamaz (her erken dönüş de yazar). Soru ve cevap metni emit akışından
// okunur (tap); kademe chatSpan'dan.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chatExchange — bir CoSRE turunun etkileşim durumu. tap birden çok
// kademeden (ve kurtarma sarmalayıcısından) çağrılabilir → kilitli.
type chatExchange struct {
	mu       sync.Mutex
	root     string
	question string
	deep     bool
	started  time.Time
	route    string
	answer   string
	errMsg   string
	// outcome — v0.10.2201: answer olayının `outcome` alanı (chatOutcomeNotFound).
	outcome string

	// CoSRE öğrenme Faz 1 (cosre_interactions.go) — turun etkileşim satırı.
	conversation string
	turn         uint16
	attempt      string
	page         *agentctx.PageContext
	ctxService   string
	ctxTrace     string
	tools        []chstore.CosreTool
	toolAt       map[int]int       // step i → tools indeksi
	stepAt       map[int]time.Time // step i → çip zamanı (süre için)
	firstOut     time.Time         // ilk görünür çıktı (delta/answer) → ttft
	model        string
	modelCalls   uint16
	inTok        uint32
	outTok       uint32
	// loop / callTTFT — v0.10.2323 (H1): turun döngü telemetrisi (CallNote.Loop
	// toplamı) ve son anlatım/döngü notunun ilk token'ı. YALNIZ değerlendirme
	// koşucusu okur (evalOutcomeFromExchange); hiçbir satıra yazılmaz.
	loop     copilot.LoopUsage
	callTTFT uint32

	// v0.10.1280 (CoSRE adım 8) — cevabı ÜRETEN ai_calls satırı: kökle
	// (alt-rol değil) kaydedilen SON model çağrısı ya da döngülerin cevaptan
	// önce sabitlediği kimlik (noteFinalCall). tap bunu `answer` olayına
	// isteğe bağlı `callId` olarak ekler → 👍/👎 somut çağrıya bağlanır.
	finalCallID string

	// v0.10.1302 — serbest döngünün koştuğu araç seti (full | simple);
	// guided / niyet / LLM'siz kademeler "" bırakır.
	toolset string

	// v0.10.1444 — turun zengin blokları (chat_turn_blocks.go): yakalayıcı
	// emit zincirinde withBlockSeq'in altında (copilotChat kurar); code =
	// son cevabın answer.explain.code künyesi (son cevap kazanır).
	blockCap *turnBlockCapture
	code     *codeContextPayload

	// verify — v0.10.2327 (E1, answer_verify_turn.go): doğrulayıcı toplayıcısı;
	// nil = kapalı (v1 ya da verification=off). Kurulumdan sonra değişmez.
	verify *answerVerifyTurn

	// loopShape / loopRan — CoSRE ölçüm (chat_loop_shape.go): serbest
	// döngünün künyesi; yalnız döngü koştuysa loop_stop sinyali yazılır.
	loopShape cosrelearn.LoopShape
	loopRan   bool
}

// setBlockCapture — v0.10.1444: copilotChat'in yakalayıcısını bağlar.
func (x *chatExchange) setBlockCapture(c *turnBlockCapture) {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.blockCap = c
}

// setToolset — v0.10.1302: serbest döngü kataloğunu kurarken çağrılır.
func (x *chatExchange) setToolset(ts string) {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.toolset = ts
}

// setInteraction — istek bağlamı (konuşma, tur no, sayfa, istemci denemesi).
func (x *chatExchange) setInteraction(conversation string, turn int, attempt string, page *agentctx.PageContext, service, trace string) {
	if x == nil {
		return
	}
	if turn > 65535 {
		turn = 65535
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.conversation, x.turn, x.attempt, x.page = conversation, uint16(turn), normalizeChatAttempt(attempt), page
	x.ctxService, x.ctxTrace = service, trace
}

// chatUserTurns — SAF: istekteki kullanıcı mesajı sayısı (tur no, 1'den).
func chatUserTurns(msgs []copilot.ChatMessage) int {
	n := 0
	for _, m := range msgs {
		if m.Role == "user" {
			n++
		}
	}
	return n
}

// normalizeChatAttempt — SAF: istemci beyanı kapalı kümeye (LC kolonu).
func normalizeChatAttempt(a string) string {
	switch a {
	case "retry", "regenerate", "edit":
		return a
	}
	return ""
}

// noteModelCall — copilot.CallSink: turdaki her model çağrısı (son model kazanır).
// v0.10.1280 — kökle kaydedilen çağrının kimliği cevabın çağrısı adayıdır;
// yardımcı/işaret satırları ("kök:rol") cevap üretmez, aday olmaz.
func (x *chatExchange) noteModelCall(n copilot.CallNote) {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if n.Model != "" {
		x.model = n.Model
	}
	// v0.10.2297 — serbest döngünün tek notu N gidiş-dönüşü temsil eder.
	n16 := uint16(1)
	if n.Rounds > 0 {
		n16 = n.Rounds
	}
	if x.modelCalls > 65535-n16 {
		x.modelCalls = 65535
	} else {
		x.modelCalls += n16
	}
	x.inTok += n.InputTokens
	x.outTok += n.OutputTokens
	x.loop.Rounds += n.Loop.Rounds // v0.10.2323 — değerlendirme izi
	x.loop.ModelMs += n.Loop.ModelMs
	x.loop.ToolMs += n.Loop.ToolMs
	x.loop.ToolCalls += n.Loop.ToolCalls
	if t := max(n.Loop.AnswerTTFTMs, n.TTFTMs); t > 0 {
		x.callTTFT = t
	}
	if n.CallID != "" && n.ExchangeID != "" && n.ExchangeID == x.root {
		x.finalCallID = n.CallID
	}
}

// noteFinalCall — v0.10.1280: toplu satırını cevaptan SONRA yazan döngüler
// (serbest döngü, exception kod döngüsü) kimliği önceden basar
// (copilot.NewCallID) ve cevabı yayınlamadan hemen önce buraya bildirir;
// aynı kimlik copilot.WithCallID ile RecordUsage'a geçer.
func (x *chatExchange) noteFinalCall(id string) {
	if x == nil || id == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.finalCallID = id
}

// answerWithCallID — SAF: `answer` yükünün callId'li KOPYASI. Yalnız yük bu
// turun exchangeId'sini taşıyorsa (oylanabilir cevap) ve kimlik biliniyorsa;
// aksi hâlde yük AYNEN döner. Kopya: çağıranın haritası değişmez.
func answerWithCallID(v any, root, callID string) any {
	if callID == "" || root == "" {
		return v
	}
	switch m := v.(type) {
	case map[string]any:
		if xid, _ := m["exchangeId"].(string); xid != root {
			return v
		}
		if _, has := m["callId"]; has {
			return v
		}
		out := make(map[string]any, len(m)+1)
		for k, val := range m {
			out[k] = val
		}
		out["callId"] = callID
		return out
	case map[string]string:
		if m["exchangeId"] != root || m["callId"] != "" {
			return v
		}
		out := make(map[string]string, len(m)+1)
		for k, val := range m {
			out[k] = val
		}
		out["callId"] = callID
		return out
	}
	return v
}

// noteStep — tool çipi (step olayı, kimlik damgalandıktan SONRA). Etiket
// çipleri (tool yok) tool değildir.
func (x *chatExchange) noteStep(v any, now time.Time) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	tool, _ := m["tool"].(string)
	i, hasI := eventInt(m["i"])
	if tool == "" || !hasI {
		return
	}
	args, _ := m["args"].(string)
	origin, _ := m["origin"].(string)
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.toolAt == nil {
		x.toolAt, x.stepAt = map[int]int{}, map[int]time.Time{}
	}
	x.toolAt[i] = len(x.tools)
	x.stepAt[i] = now
	x.tools = append(x.tools, chstore.CosreTool{Name: tool, Origin: origin, Args: clipBytes(args, cosreToolArgsMax), OK: true})
}

// noteStepResult — çipin kanıtı/hatası (step-result olayı).
func (x *chatExchange) noteStepResult(v any, now time.Time) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	i, hasI := eventInt(m["i"])
	if !hasI {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	idx, found := x.toolAt[i]
	if !found {
		return
	}
	t := &x.tools[idx]
	okv, _ := m["ok"].(bool)
	preview, _ := m["preview"].(string)
	t.OK = okv
	if b, has := eventInt(m["bytes"]); has && b >= 0 {
		t.Bytes = uint32(b)
	}
	if okv {
		t.Output = clipBytes(preview, cosreToolOutputMax)
	} else {
		t.Error = clipBytes(preview, cosreToolErrMax)
	}
	if sk, _ := m["skipped"].(bool); sk {
		t.OK, t.Error = false, "skipped"
	}
	if d, has := eventInt(m["durationMs"]); has && d >= 0 {
		t.Ms = uint32(d)
	} else if at, has := x.stepAt[i]; has {
		t.Ms = uint32(now.Sub(at).Milliseconds())
	}
	t.Evidence = stepEvidenceSummary(m["evidence"], m["evidenceList"]) // v0.10.1280
}

// cosreToolEvidenceMax — çip başına kanıt özetinin bayt tavanı.
const cosreToolEvidenceMax = 256

// stepEvidenceSummary — SAF (v0.10.1280): step-result kanıtının KOMPAKT
// özeti — kalem başına "kaynak[#hop]:satır" (satır yoksa seri "s<N>", ikisi
// de yoksa "?"), virgülle. Sorgu metni, pencere, href gibi hacimli alanlar
// BİLEREK yok: cosre_interactions turun izini tutar, kanıtın kopyasını değil
// (tam kanıt tool_output önizlemesinde ve canlı çipte). Liste varsa listeden
// (bileşik araç), yoksa tek kalemden; kanıtsız çip "".
func stepEvidenceSummary(single, list any) string {
	var evs []mcptools.ChatEvidence
	switch l := list.(type) {
	case []mcptools.ChatEvidence:
		evs = l
	}
	if len(evs) == 0 {
		switch e := single.(type) {
		case mcptools.ChatEvidence:
			evs = []mcptools.ChatEvidence{e}
		case *mcptools.ChatEvidence:
			if e != nil {
				evs = []mcptools.ChatEvidence{*e}
			}
		}
	}
	if len(evs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(evs))
	for _, e := range evs {
		src := e.Source
		if src == "" {
			src = e.Tool
		}
		if e.Hop != "" {
			src += "#" + e.Hop
		}
		n := "?"
		switch {
		case e.Rows != nil:
			n = strconv.Itoa(*e.Rows)
		case e.Series != nil:
			n = "s" + strconv.Itoa(*e.Series)
		}
		parts = append(parts, src+":"+n)
	}
	return clipBytes(strings.Join(parts, ","), cosreToolEvidenceMax)
}

// eventInt — SAF: SSE yükündeki sayı (int / int64 / float64).
func eventInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

type chatExchangeKey struct{}

func newChatExchange(root, question string, deep bool, started time.Time) *chatExchange {
	return &chatExchange{root: root, question: strings.TrimSpace(question), deep: deep, started: started}
}

func withChatExchange(ctx context.Context, x *chatExchange) context.Context {
	return context.WithValue(ctx, chatExchangeKey{}, x)
}

// chatExchangeFrom — ctx'teki tur; sohbet dışında nil (nil-güvenli yöntemler).
func chatExchangeFrom(ctx context.Context) *chatExchange {
	if ctx == nil {
		return nil
	}
	x, _ := ctx.Value(chatExchangeKey{}).(*chatExchange)
	return x
}

// noteRoute — guided rotası (runGuidedRoute girişi). İlk rota kalır: kurtarma
// / takip yolları ikinci bir rota çalıştırsa da turun yönlendirmesi ilkidir.
func (x *chatExchange) noteRoute(route string) {
	if x == nil || route == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.route == "" {
		x.route = route
	}
}

// tap — emit sarmalayıcısı: "answer" metnini (son cevap kazanır) ve "error"
// olayını yakalar, olayı DEĞİŞTİRMEDEN iletir.
func (x *chatExchange) tap(emit func(string, any)) func(string, any) {
	return func(kind string, v any) {
		now := time.Now()
		switch kind {
		case "answer":
			x.mu.Lock()
			x.answer = eventField(v, "text")
			x.outcome = eventField(v, "outcome") // v0.10.2201 — son cevap kazanır
			if x.firstOut.IsZero() {
				x.firstOut = now
			}
			v = answerWithCallID(v, x.root, x.finalCallID) // v0.10.1280 — opsiyonel callId
			x.code = answerExplainCode(v)                  // v0.10.1444 — kod künyesi arşiv zarfına
			v = answerWithTurnID(v, x.root)                // v0.10.1444 — önbellek isabetinde turun kendi kimliği
			x.mu.Unlock()
		case "delta":
			x.mu.Lock()
			if x.firstOut.IsZero() {
				x.firstOut = now
			}
			x.mu.Unlock()
		case "error":
			x.mu.Lock()
			x.errMsg = eventField(v, "error")
			x.mu.Unlock()
		case "step-result":
			x.noteStepResult(v, now)
		case chatStopEventKind:
			x.noteStopEvent(v) // CoSRE ölçüm — döngü künyesi
		}
		emit(kind, v)
		if kind == "step" {
			// kimlik (i) iç sarmalayıcıda (withStepIDs) YERİNDE damgalanır.
			x.noteStep(v, now)
		}
	}
}

// answerExplainCode — SAF (v0.10.1444): answer.explain.code (açıklama
// turunun kod künyesi); yoksa nil.
func answerExplainCode(v any) *codeContextPayload {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	switch e := m["explain"].(type) {
	case chatExplainMeta:
		return e.Code
	case *chatExplainMeta:
		if e != nil {
			return e.Code
		}
	}
	return nil
}

// answerWithTurnID — SAF (v0.10.1444): cevabın exchangeId'si turun kökü
// DEĞİLSE (açıklama önbellek isabeti saklanan üretimin kimliğini taşır; 👍/👎
// o çağrıya gider) `turnId` = kök eklenir. İstemci arşive bunu yazar: turun
// cosre_interactions satırı (blok zarfı) kökle anahtarlı. Kökle aynıysa
// cevap bayt-bayt eski.
func answerWithTurnID(v any, root string) any {
	if root == "" {
		return v
	}
	switch m := v.(type) {
	case map[string]any:
		if xid, _ := m["exchangeId"].(string); xid == root {
			return v
		}
		if _, has := m["turnId"]; has {
			return v
		}
		out := make(map[string]any, len(m)+1)
		for k, val := range m {
			out[k] = val
		}
		out["turnId"] = root
		return out
	case map[string]string:
		if m["exchangeId"] == root || m["turnId"] != "" {
			return v
		}
		out := make(map[string]string, len(m)+1)
		for k, val := range m {
			out[k] = val
		}
		out["turnId"] = root
		return out
	}
	return v
}

// eventField — SAF: SSE yükünden dize alan (yükler map[string]any ya da
// map[string]string).
func eventField(v any, key string) string {
	switch m := v.(type) {
	case map[string]any:
		s, _ := m[key].(string)
		return s
	case map[string]string:
		return m[key]
	}
	return ""
}

// chatExchangeOutcome — SAF: turun sonucu. Kademe başarısızsa (chatSpan.err)
// ya da akışa hata olayı düştüyse "error"; cevap yokken alışveriş tavanı
// dolduysa (deadline) yine "error" — sessiz kayıp sayılmaz.
//
// v0.10.1280 (CoSRE adım 8) — "aborted": cevap gelmeden İSTEMCİ GİTTİ
// (context.Canceled). Arıza değil, operatörün kararı (durdur / sekme
// kapandı); "error" sayılınca /ai hata oranını ve örtük sinyalleri
// şişiriyordu. Döngü iptalde akışa kendi hata cümlesini yazdığı için bu dal
// yayınlanan hatadan ÖNCE bakılır. Cevap yayınlandıysa iptal sonucu
// değiştirmez (ok).
func chatExchangeOutcome(tierErr error, emittedErr string, answered bool, ctxErr error) (status, msg string) {
	switch {
	case !answered && errors.Is(ctxErr, context.Canceled):
		return "aborted", ctxErr.Error()
	case emittedErr != "":
		return "error", emittedErr
	case tierErr != nil:
		return "error", tierErr.Error()
	case !answered && ctxErr != nil:
		return "error", ctxErr.Error()
	}
	return "ok", ""
}

// chatExchangeStatusWithOutcome — SAF (v0.10.2201): cevaplanmış ("ok") tur
// öznenin dürüst yokluğunu söylediyse "not_found" — hata DEĞİL (routing
// raporu "hata" demez, ai_calls hata oranına girmez), başarı da değil.
func chatExchangeStatusWithOutcome(status, outcome string) string {
	if status == "ok" && outcome == chatOutcomeNotFound {
		return chatOutcomeNotFound
	}
	return status
}

// recordChatExchange — turun etkileşim satırı (copilotChat'in defer'i).
// ctx alışverişin ctx'i (kullanıcı meta'sı, açık/derin profil); iptal edilmiş
// olabilir — kayıt kendi sınırlı ctx'iyle yazılır (copilot.emitRecord).
func (s *Server) recordChatExchange(ctx context.Context, x *chatExchange, cs *chatSpan) {
	if s == nil || s.copilot == nil || x == nil {
		return
	}
	tier, tierErr := aisurface.TierLoop, error(nil)
	if cs != nil {
		tier, tierErr = cs.tierV, cs.err
	}
	x.mu.Lock()
	answer, emittedErr, route, outcome := x.answer, x.errMsg, x.route, x.outcome
	x.mu.Unlock()
	status, msg := chatExchangeOutcome(tierErr, emittedErr, answer != "", ctx.Err())
	status = chatExchangeStatusWithOutcome(status, outcome)
	vo, verified := x.verify.finish() // v0.10.2327 — E1 gölge: akış bitti, yalnız sayılır
	if evm := chatEvalModeFrom(ctx); evm != nil {
		// v0.10.1316 — ajan kipi evalset vakası: etkileşim satırı (cosre-turn)
		// ve cosre_interactions YOK; sonuç değerlendirme koşucusuna.
		if evm.Sink != nil {
			o := evalOutcomeFromExchange(x, tier, status, msg)
			if verified {
				o.Verify = &vo.Result
			}
			evm.Sink(o)
		}
		return
	}
	m := copilot.MetaFromContext(ctx)
	m.Surface = aisurface.TurnLabel(tier, route, x.deep)
	m.ExchangeID = aisurface.TurnExchangeID(x.root)
	m.Observe, m.Shield, m.PromptLogOverride = nil, nil, ""
	s.copilot.RecordExchange(copilot.WithMeta(ctx, m), x.started, status, msg, x.question, answer)
	row := buildCosreInteraction(x, tier, m, status, msg, time.Now())
	if verified {
		row.Verify = vo.Compact
		s.writeSystemSignals(ctx, x.root, verifySignals(x.root, vo.signalResult(), vo.Truncated)) // v0.10.2339 — yeniden üretilen uydurma da sinyal
	}
	s.enqueueCosreInteraction(row)
	s.writeSystemSignals(ctx, x.root, loopStopSignals(x)) // CoSRE ölçüm — round_cap / context_overflow + katalog boyu
}

// buildCosreInteraction — SAF (x kilidi altında okur): turun etkileşim satırı.
func buildCosreInteraction(x *chatExchange, tier string, m copilot.CallMeta, status, msg string, now time.Time) chstore.CosreInteraction {
	x.mu.Lock()
	defer x.mu.Unlock()
	answer := strings.TrimSpace(x.answer)
	r := chstore.CosreInteraction{
		ExchangeID: x.root, ConversationID: x.conversation, Turn: x.turn,
		CreatedAt: x.started, UserID: m.UserID, UserEmail: m.UserEmail,
		Tier: tier, Surface: m.Surface,
		Service: x.ctxService, TraceID: x.ctxTrace,
		Question:    clipBytes(x.question, cosreQuestionMax),
		Answer:      clipBytes(answer, cosreAnswerMax),
		AnswerChars: uint32(len(answer)),
		Tools:       append([]chstore.CosreTool(nil), x.tools...),
		Model:       x.model, ModelCalls: x.modelCalls, InputTokens: x.inTok, OutputTokens: x.outTok,
		Status: status, ErrorMsg: clipBytes(msg, cosreToolErrMax),
		Attempt: x.attempt, Deep: x.deep,
		Toolset:     x.toolset, // v0.10.1302
		EmptyAnswer: answer == "",
		NoEvidence:  answerAdmitsNoEvidence(answer),
	}
	// v0.10.1444 — blok zarfı (≤256 KiB; blok/kod yoksa ""). Akış bitti: tek
	// defer'de, SSE yolunun dışında.
	r.Blocks, _ = compactTurnBlocks(x.blockCap.snapshot(), x.code, now, turnBlocksCapBytes)
	if p := x.page; p != nil {
		r.Page, r.ProblemID, r.ExceptionID = p.Page, p.ProblemID, p.ExceptionID
		if p.Service != "" {
			r.Service = p.Service
		}
		if p.TraceID != "" {
			r.TraceID = p.TraceID
		}
	}
	if !x.started.IsZero() {
		r.TotalMs = uint32(now.Sub(x.started).Milliseconds())
		if !x.firstOut.IsZero() {
			r.TTFTMs = uint32(x.firstOut.Sub(x.started).Milliseconds())
		}
	}
	return r
}
