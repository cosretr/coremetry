package api

// ai_evalset_agent.go — v0.10.1316 (DECISIONS "2026-10-18 — Değerlendirme
// seti gerçek ajan döngüsünde"): evalset vakasının AJAN kipi.
//
// v0.10.940'tan beri her evalset vakası TEK model çağrısıydı: araç sonucu
// fikstürün kullanıcı metninde hazır gelir, modele araç kataloğu hiç
// gönderilmezdi (araç seti yalnız prompt varyantını değiştiriyordu). CoSRE'nin
// asıl davranışı — hangi aracı seçtiği, kaç tur döndüğü, bütçeyi nasıl
// harcadığı, eldeki kanıtla ne söylediği — ölçülmüyordu.
//
// Ajan kipi (`"mode":"agent"`, yalnız serbest döngü yüzeylerinde) vakayı
// CANLI sohbet handler'ından geçirir (D1, süreç içi): copilotChat'e gerçek bir
// POST /api/copilot/chat isteği kurulur, ctx'e chatEvalMode işareti konur
// (chat_eval_mode.go) ve SSE gövdesi atılır; sonuç Sink + tur/araç
// kancalarından toplanır. Döngü öncesi kademeler (guided, wiki …) AÇIK
// kalır (D2) — üretim neyse o ölçülür; kademe önemliyse vaka `expectTier`
// ile sabitler. v0.10.1321: niyet kademesi ajan vakasında KAPALI
// (evalAgentSkipIntent), yalnız expectTier "intent" sabitleyen vakada açık.
// Tavanlar canlıdakiler (D3: 5 tur / 6 çağrı, Derin 8 / 12; araç başına
// 20 s), vaka tavanı evalAgentCaseMax. Araç başına
// mcp.tool.call audit'i yazılmaz (D4), koşunun ai.evalset.run satırı kalır.
// ai_calls yüzeyi evalset-ChatLoop (D5, evalset- öneki = K2 dışlaması);
// döngü öncesi kademelerin satırları da aynı öneki alır
// (copilot.WithRecordSurfacePrefix).
//
// Puanlama SAF ve deterministiktir (scoreEvalAgentCase); oylar hiçbir
// aşamada girdi değildir (TestEvalNeverReadsVotes). Bu dosya hiçbir şey
// YAZMAZ: koşunun tek kalıcı yazımı ai_evalset_runs.go'daki SaveEvalRun
// (TestEvalAgentCatalogReadOnly AST taraması).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// evalModeAgent — fikstürün `mode` değeri: vaka canlı ajan döngüsünden koşar.
const evalModeAgent = "agent"

// evalAgentSkipCLI — ajan vakası CLI koşumunda (evalset_test.go, sunucusuz)
// koşulamaz: handler, katalog ve araç bağımlılıkları sunucuda.
const evalAgentSkipCLI = "agent kipi yalnız sunucu koşusunda"

// Tavanlar (bayt, rune-güvenli) — cases JSON'u CH satırında sınırlı kalsın.
const (
	evalAgentStepsMax   = 12
	evalAgentArgsMax    = 256
	evalAgentPreviewMax = 512
	evalAgentTrailMax   = 8 << 10
	// evalAgentToolTextMax — kanıt metni (uydurma sayımının "gösterilen"i);
	// yalnız bellekte, saklanmaz.
	evalAgentToolTextMax = 256 << 10
)

// evalAgentCaseCap — vaka başına üst tavan; canlı alışveriş tavanı
// (chatExchangeTimeout) daha kısaysa o. var: test dikişi.
var evalAgentCaseCap = 10 * time.Minute

// evalAgentCaseMax — SAF: ajan vakasının tavanı.
func evalAgentCaseMax(perCall time.Duration) time.Duration {
	return min(chatExchangeTimeout(perCall), evalAgentCaseCap)
}

// evalRunAgentAllowance / evalRunDeadlineCap — koşu tavanı ajan vakası başına
// büyür (bir ajan vakası tek çağrılı vakanın onlarca katı sürebilir), ama
// sonsuza değil. var: test dikişi.
var evalRunAgentAllowance = 10 * time.Minute

const evalRunDeadlineCap = 180 * time.Minute

// evalRunDeadlineFor — SAF: plan → koşu tavanı. Taban evalRunDeadline (test
// dikişi aynen kalır); ajan vakası başına evalRunAgentAllowance eklenir,
// toplam evalRunDeadlineCap'te (taban daha büyükse taban) kesilir.
func evalRunDeadlineFor(plan []evalCase) time.Duration {
	base := evalRunDeadline
	n := 0
	for _, c := range plan {
		if evalAgentCase(c) {
			n++
		}
	}
	if n == 0 {
		return base
	}
	d := base + time.Duration(n)*evalRunAgentAllowance
	if limit := max(evalRunDeadlineCap, base); d > limit {
		d = limit
	}
	return d
}

// evalAgentCase — SAF: vaka ajan kipinde mi.
func evalAgentCase(c evalCase) bool { return c.Mode == evalModeAgent }

// evalAgentCaseCount — SAF: plandaki ajan vakası sayısı (katalog + özet).
func evalAgentCaseCount(cases []evalCase) int {
	n := 0
	for _, c := range cases {
		if evalAgentCase(c) {
			n++
		}
	}
	return n
}

// evalAgentTools — SAF: ajan kipi kataloğu. Sohbet bağlamı araçları
// (mcptools.ConversationStateTools — konuşma başına Redis durumu) düşer:
// vaka konuşmasızdır ve koşuyu başlatanın bağlamına dokunmamalıdır. Sıra
// korunur; geri kalan katalog canlıyla aynı.
func evalAgentTools(tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if !mcptools.ConversationStateTools[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// ── Toplayıcı ───────────────────────────────────────────────────────────

// evalAgentStats — tur/araç kancalarından (chatEvalMode.OnTurn/OnTool).
type evalAgentStats struct {
	Rounds int // model turu (taşma yeniden denemesi ve tavan turu dâhil)
	Calls  int // modelin istediği araç çağrısı (tavanı aşanlar dâhil)
	// ExecCalls — Executor'a giden çağrı; Executed — handler'ı gerçekten koşan.
	ExecCalls, Executed int
	// Unknown / Repeated — Executor'ın yürütmediği çağrı nedenleri.
	Unknown, Repeated int
	Capped            bool // tavan turu koştu (tur ya da çağrı bütçesi doldu)
	CachedTokens      uint32
	// ToolText — yürüyen başarılı çağrıların argümanı + TAM çıktısı (uydurma
	// sayımının kanıtı; saklanmaz).
	ToolText string
	// Offered — v0.10.1476: serbest döngünün modele sunduğu araç adları
	// (chatEvalMode.OnCatalog); nil = döngü kataloğu kurulmadı.
	Offered []string
	// Scope / ScopeCalls — araç seti dilim 2 (ai_evalset_agent_scope.go): v2
	// döngüsünün çözdüğü kullanıcı kapsamı + yürüyen çağrıların servisleri.
	Scope      *userScope
	ScopeCalls []evalScopeCall
}

// CapSkipped — tavanı aşıp Executor'a hiç gitmeyen çağrı sayısı.
func (s evalAgentStats) CapSkipped() int {
	if n := s.Calls - s.ExecCalls; n > 0 {
		return n
	}
	return 0
}

type evalAgentCollector struct {
	mu       sync.Mutex
	st       evalAgentStats
	toolText strings.Builder
	out      *evalAgentOutcome
}

func (c *evalAgentCollector) onTurn(round int, turn copilot.ChatTurn, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Rounds++
	if round < 0 {
		c.st.Capped = true
	}
	c.st.CachedTokens += turn.CachedTokens
	if err == nil {
		c.st.Calls += len(turn.ToolCalls)
	}
}

func (c *evalAgentCollector) onTool(name string, args json.RawMessage, oc agenttools.Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.ExecCalls++
	switch oc.Kind {
	case agenttools.KindUnknown:
		c.st.Unknown++
	case agenttools.KindRepeated:
		c.st.Repeated++
	}
	c.noteScopeCall(name, args, oc) // araç seti dilim 2
	if !oc.Executed {
		return
	}
	c.st.Executed++
	if oc.IsError || c.toolText.Len() >= evalAgentToolTextMax {
		return
	}
	c.toolText.WriteString(name)
	c.toolText.WriteByte(' ')
	c.toolText.Write(args)
	c.toolText.WriteByte('\n')
	c.toolText.WriteString(oc.Content)
	c.toolText.WriteByte('\n')
}

func (c *evalAgentCollector) onCatalog(names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Offered = append([]string{}, names...)
}

func (c *evalAgentCollector) sink(o evalAgentOutcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out = &o
}

func (c *evalAgentCollector) result() (evalAgentOutcome, evalAgentStats, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st
	st.ToolText = c.toolText.String()
	if c.out == nil {
		return evalAgentOutcome{}, st, false
	}
	return *c.out, st, true
}

// ── Sunucu koşucusu ─────────────────────────────────────────────────────

// evalAgentFn — bir ajan vakasını koşar. Profil ctx'te (runEvalJob
// WithProfile ile sabitler — tek çağrılı yolla aynı); araç seti ctx'te
// (mcptools.WithToolset). nil = sunucusuz koşum (CLI) → vaka atlanır.
type evalAgentFn func(ctx context.Context, c evalCase) (evalAgentOutcome, evalAgentStats, error)

// evalDiscardResponse — SSE gövdesini atan ResponseWriter (Flusher: Emitter
// akış ister). Sonuç gövdeden değil Sink'ten okunur.
type evalDiscardResponse struct {
	mu   sync.Mutex
	h    http.Header
	code int
}

func newEvalDiscardResponse() *evalDiscardResponse { return &evalDiscardResponse{h: http.Header{}} }

func (w *evalDiscardResponse) Header() http.Header         { return w.h }
func (w *evalDiscardResponse) Write(b []byte) (int, error) { return len(b), nil }
func (w *evalDiscardResponse) Flush()                      {}
func (w *evalDiscardResponse) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 {
		w.code = code
	}
}

func (w *evalDiscardResponse) status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

// evalAgentSkipIntent — SAF, v0.10.1321 (operatör; DECISIONS "Ajan eval
// vakalarında intent katmanı kapalı"): ajan vakasında niyet kademesi KAPALI.
// Prod varsayılanı on_no_loop'ta guided'ın eşlemediği düz soru niyet
// kademesinde (sınıflandırıcı + genel cevap) biter, döngüye hiç ulaşmaz ve
// vaka "missing tool" ile kalırdı — ölçmek istediğimiz araç döngüsüydü. guided
// (deterministik router, ayrı mekanizma) ve öteki kademeler açık kalır.
// İstisna: vaka expectTier "intent" sabitlediyse kademeyi ölçmek istiyordur →
// açık kalır.
func evalAgentSkipIntent(c evalCase) bool { return c.Expect.ExpectTier != aisurface.TierIntent }

// evalAgentLoopProbe — SAF, v0.10.1476 (prod koşusu 2026-10-23, ChatLoop 2/6):
// vaka expectTier "loop" sabitlediyse serbest döngünün ARAÇ SEÇİMİNİ ölçen
// bir sondadır → guided, RAG ve niyet kademeleri atlanır. Ölçüm: prod
// kataloğunda bu soruların hepsi deterministik guided rotasına gidiyordu
// (service_topology / log_errors / ask_service …); döngü hiç koşmuyor, iz
// guided çiplerini gösteriyor, anyOfTools "missing" kalıyordu. Prod rotası
// deterministik olduğu için onun pini CI'da (chat_routing_regression_test.go).
func evalAgentLoopProbe(c evalCase) bool { return c.Expect.ExpectTier == aisurface.TierLoop }

// evalAgentSkipError — koşucunun vakayı model çağırmadan atlama kararı
// (bağlanacak canlı servis yok, beklenen araç sunulmuyor …).
type evalAgentSkipError struct{ reason string }

func (e *evalAgentSkipError) Error() string { return e.reason }

// evalNameIn — SAF: ad canlı listede mi (harf duyarsız tam eş).
func evalNameIn(name string, live []string) bool {
	for _, n := range live {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// evalBindCase — SAF, v0.10.1476: vakanın sentetik servis adını (from) canlı
// bir adla (to) değiştirir: sorudaki SINIRLI geçişler, mustContain / keyFacts
// / knownEntities'teki tam eşler. Dilimler kopyalanır (fikstür değişmez);
// to bilinen kümeye eklenir. from boş ya da from == to → aynen.
func evalBindCase(c evalCase, from, to string) evalCase {
	if from == "" || to == "" || strings.EqualFold(from, to) {
		return c
	}
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(from) + `\b`)
	c.User = re.ReplaceAllLiteralString(c.User, to)
	swap := func(in []string) []string {
		if in == nil {
			return nil
		}
		out := make([]string, len(in))
		for i, v := range in {
			if strings.EqualFold(v, from) {
				v = to
			}
			out[i] = v
		}
		return out
	}
	c.Expect.MustContain = swap(c.Expect.MustContain)
	c.Expect.KeyFacts = swap(c.Expect.KeyFacts)
	c.Expect.KnownEntities = swap(c.Expect.KnownEntities)
	if !evalNameIn(to, c.Expect.KnownEntities) {
		c.Expect.KnownEntities = append(c.Expect.KnownEntities, to)
	}
	return c
}

// evalAgentHarnessGap — SAF, v0.10.1476: döngü sondasında beklenen araçlar
// bu kurulumun kataloğunda (rol / araç seti / kapalı toolset / localOnly
// süzgecinden sonra) SUNULMUYORSA neden metni; model sunulmayan aracı
// seçemez → vaka model hatası sayılmaz, atlanır. offered nil (katalog
// kurulmadı) → "" (karar kademe kapısına kalır).
func evalAgentHarnessGap(c evalCase, offered []string) string {
	if offered == nil || !evalAgentLoopProbe(c) {
		return ""
	}
	have := make(map[string]bool, len(offered))
	for _, n := range offered {
		have[n] = true
	}
	var missing []string
	for _, n := range c.Expect.ExpectedTools {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(c.Expect.AnyOfTools) > 0 {
		hit := false
		for _, n := range c.Expect.AnyOfTools {
			hit = hit || have[n]
		}
		if !hit {
			missing = append(missing, c.Expect.AnyOfTools...)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("harness: beklenen araç(lar) bu kurulumun sohbet kataloğunda sunulmuyor (kapalı toolset / rol / araç seti): %v", missing)
}

// evalBindTargetSeam — v0.10.1476: bağlama hedefi (son 1 saatin en yoğun
// canlı servisi, MV okuması). var: test dikişi.
var evalBindTargetSeam = func(ctx context.Context, s *Server, live []string) string {
	if s == nil || s.store == nil {
		return ""
	}
	qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	now := time.Now()
	rows, err := s.store.GetServicesAgg(qctx, now.Add(-time.Hour), now, 20)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.SpanCount > 0 && (len(live) == 0 || evalNameIn(r.Name, live)) {
			return r.Name
		}
	}
	return ""
}

// evalAgentRequest — SAF: vakanın sohbet isteği (tek kullanıcı turu,
// konuşmasız, sayfasız; Derin vakadan).
func evalAgentRequest(c evalCase) chatRequest {
	var req chatRequest
	req.Messages = []copilot.ChatMessage{{Role: "user", Text: c.User}}
	req.Context.Deep = c.Deep
	return req
}

// evalsetServerAgent — sunucu koşucusunun evalAgentFn'i: copilotChat süreç
// içinde, koşuyu başlatanın kimliğiyle (rol süzgeci canlıdaki gibi).
func (s *Server) evalsetServerAgent(job *evalJob) evalAgentFn {
	var bindOnce sync.Once // v0.10.1476 — koşu başına bir bağlama hedefi
	var bindTarget string
	return func(ctx context.Context, c evalCase) (evalAgentOutcome, evalAgentStats, error) {
		if s == nil || s.copilot == nil {
			return evalAgentOutcome{}, evalAgentStats{}, errors.New("CoSRE yapılandırılmamış")
		}
		toolset := modelcaps.ToolsetFull
		if mcp.SimpleToolsetFrom(ctx) {
			toolset = modelcaps.ToolsetSimple
		}
		// v0.10.1476 — sentetik servis canlı katalogda yoksa koşunun TEK
		// hedefine bağlanır (koşu başına bir MV okuması).
		boundTo := ""
		if c.BindService != "" {
			if live := s.guidedServiceNames(ctx); !evalNameIn(c.BindService, live) {
				bindOnce.Do(func() { bindTarget = evalBindTargetSeam(ctx, s, live) })
				if bindTarget == "" {
					return evalAgentOutcome{}, evalAgentStats{}, &evalAgentSkipError{reason: fmt.Sprintf("bağlanacak canlı servis yok: %q katalogda değil, son 1 saatte trafikli servis bulunamadı", c.BindService)}
				}
				boundTo = bindTarget
				c = evalBindCase(c, c.BindService, boundTo)
			}
		}
		col := &evalAgentCollector{}
		em := &chatEvalMode{
			Surface: evalsetSurfacePrefix + c.Surface, ExchangeID: job.row.ID + "/" + c.ID, Toolset: toolset,
			SkipIntent: evalAgentSkipIntent(c), // v0.10.1321
			LoopProbe:  evalAgentLoopProbe(c),  // v0.10.1476
			OnTurn:     col.onTurn, OnTool: col.onTool, Sink: col.sink, OnCatalog: col.onCatalog,
			UserTeam: c.UserTeam, OnScope: col.onScope, // araç seti dilim 2 — fikstür users.team
		}
		cctx := copilot.WithRecordSurfacePrefix(ctx, evalsetSurfacePrefix)
		cctx = auth.ContextWithClaims(cctx, &auth.Claims{UserID: job.userID, Email: job.userEmail, Role: job.userRole})
		cctx = withChatEvalMode(cctx, em)
		if c.UserTeam != "" {
			cctx = withCosreV2Loop(cctx) // araç seti dilim 2 — kullanıcı kapsamı yalnız v2 döngüsünde
		}
		cctx, cancel := context.WithTimeout(cctx, evalAgentCaseMax(s.copilot.ClientTimeout()))
		defer cancel()
		body, err := json.Marshal(evalAgentRequest(c))
		if err != nil {
			return evalAgentOutcome{}, evalAgentStats{}, err
		}
		req, err := http.NewRequestWithContext(cctx, http.MethodPost, "/api/copilot/chat", bytes.NewReader(body))
		if err != nil {
			return evalAgentOutcome{}, evalAgentStats{}, err
		}
		w := newEvalDiscardResponse()
		s.copilotChat(w, req)
		o, st, ok := col.result()
		o.BoundTo = boundTo
		if !ok {
			return o, st, fmt.Errorf("sohbet handler'ı sonuç bildirmedi (HTTP %d)", w.status())
		}
		return o, st, nil
	}
}

// ── Vaka yolu ───────────────────────────────────────────────────────────

// runEvalAgentCase — runEvalsetCase'in ajan dalı (atlama ve araç seti
// çözümünden SONRA). agent nil → atlanır (CLI).
func runEvalAgentCase(ctx context.Context, agent evalAgentFn, c evalCase, toolset string) evalCaseOutcome {
	if agent == nil {
		return evalCaseOutcome{Skipped: true, SkipReason: evalAgentSkipCLI, User: c.User, Toolset: toolset, Mode: evalModeAgent}
	}
	t0 := time.Now()
	o, st, err := agent(ctx, c)
	var skip *evalAgentSkipError
	if errors.As(err, &skip) { // v0.10.1476 — model çağrılmadı
		return evalCaseOutcome{Skipped: true, SkipReason: skip.reason, User: c.User, Toolset: toolset, Mode: evalModeAgent}
	}
	if o.BoundTo != "" { // v0.10.1476 — koşucunun bağladığı adla AYNI vaka puanlanır
		c = evalBindCase(c, c.BindService, o.BoundTo)
	}
	if gap := evalAgentHarnessGap(c, st.Offered) + evalScopeHarnessGap(c, st); gap != "" && err == nil {
		return evalCaseOutcome{Skipped: true, SkipReason: gap, User: c.User, Answer: o.Answer, Toolset: toolset, Mode: evalModeAgent}
	}
	out := evalCaseOutcome{User: c.User, Answer: o.Answer, Err: err, LatencyMs: time.Since(t0).Milliseconds(), Toolset: toolset, Mode: evalModeAgent}
	if o.Toolset != "" {
		out.Toolset = o.Toolset
	}
	if out.Err == nil && strings.TrimSpace(o.Answer) == "" && o.ErrMsg != "" {
		out.Err = errors.New(o.ErrMsg) // panelin "hata" alanı; puan aşağıda cevapsızlıktan kalır
	}
	unknown := 0
	if err != nil {
		out.Fails = []string{"error: " + err.Error()}
	} else {
		out.Fails, unknown = scoreEvalAgentCase(c, o, st, st.ToolText)
		out.Fails = append(out.Fails, evalScopeFails(c, st)...) // araç seti dilim 2 (G8)
	}
	out.Unknown = unknown
	trail := evalAgentTrailFrom(c, o, st, out.LatencyMs)
	out.Agent = &trail
	in := evalRubricInput(c, o.Answer, out.Err, unknown, false)
	in.ToolCallsValid = evalAgentToolCallsValid(st)
	out.Rubric = evalrubric.Score(in)
	return out
}

// evalAgentToolCallsValid — SAF: rubriğin araç boyutu. Döngü hiç koşmadıysa
// (guided / niyet cevabı) n/a; koştuysa bilinmeyen ad, tekrar ve tavanı aşan
// çağrı yoksa geçerli.
func evalAgentToolCallsValid(st evalAgentStats) *bool {
	if st.Rounds == 0 {
		return nil
	}
	ok := st.Unknown == 0 && st.Repeated == 0 && st.CapSkipped() == 0
	return &ok
}

// evalAgentSteps — SAF: turun araç çipleri, niyet sınıflandırıcısının
// sözde-çipi hariç (araç değil; kademesi trail.tier'da).
func evalAgentSteps(tools []chstore.CosreTool) []chstore.CosreTool {
	out := make([]chstore.CosreTool, 0, len(tools))
	for _, t := range tools {
		if t.Origin != "intent" {
			out = append(out, t)
		}
	}
	return out
}

func evalStepSkipped(t chstore.CosreTool) bool { return t.Error == "skipped" }

// scoreEvalAgentCase — SAF, deterministik: ajan vakasının ihlalleri ve
// uydurma ad sayısı. Sıra: cevap var mı → araç yolu (expectedTools her biri
// en az bir kez BAŞARIYLA, anyOfTools en az biri, forbiddenTools hiç
// denenmez, yürütülen ≤ maxToolCalls) → kademe → metin kapıları
// (mustContain / mustNotContain / keyFacts — tek çağrılı yolla aynı) →
// uydurma sayımı (bilinen = knownEntities ∪ sistem + kullanıcı + araç
// metni). Gecikme / token / tur METRİKTİR (trail.overruns), kaldırmaz.
func scoreEvalAgentCase(c evalCase, o evalAgentOutcome, st evalAgentStats, fullToolText string) (fails []string, unknown int) {
	answer := strings.TrimSpace(o.Answer)
	if answer == "" {
		why := o.Status
		if o.ErrMsg != "" {
			why += ": " + o.ErrMsg
		}
		fails = append(fails, "no answer ("+why+")")
	}
	steps := evalAgentSteps(o.Tools)
	okNames, tried := map[string]bool{}, map[string]bool{}
	executed := 0
	for _, t := range steps {
		tried[t.Name] = true
		if evalStepSkipped(t) {
			continue
		}
		executed++
		if t.OK {
			okNames[t.Name] = true
		}
	}
	for _, name := range c.Expect.ExpectedTools {
		if !okNames[name] {
			fails = append(fails, "missing tool: "+name)
		}
	}
	if len(c.Expect.AnyOfTools) > 0 {
		hit := false
		for _, name := range c.Expect.AnyOfTools {
			hit = hit || okNames[name]
		}
		if !hit {
			fails = append(fails, fmt.Sprintf("missing any of tools: %v", c.Expect.AnyOfTools))
		}
	}
	for _, name := range c.Expect.ForbiddenTools {
		if tried[name] {
			fails = append(fails, "forbidden tool: "+name)
		}
	}
	if c.Expect.MaxToolCalls != nil && executed > *c.Expect.MaxToolCalls {
		fails = append(fails, fmt.Sprintf("tool calls %d > %d", executed, *c.Expect.MaxToolCalls))
	}
	if c.Expect.ExpectTier != "" && o.Tier != c.Expect.ExpectTier {
		fails = append(fails, fmt.Sprintf("tier %q, want %q", o.Tier, c.Expect.ExpectTier))
	}
	if answer == "" {
		return fails, 0
	}
	fails = append(fails, evalTextGateFails(c, answer)...)
	system, _ := evalSystemPromptFor(c.Surface, o.Toolset == modelcaps.ToolsetSimple)
	var shown strings.Builder
	shown.WriteString(system)
	shown.WriteByte('\n')
	shown.WriteString(c.User)
	shown.WriteByte('\n')
	shown.WriteString(fullToolText)
	for _, t := range steps {
		// guided ön-yüklemesinin önizlemesi de modele gösterilen veridir.
		shown.WriteString("\n" + t.Name + " " + t.Args + "\n" + t.Output)
	}
	names := evalUnknownNames(c.Expect.KnownEntities, shown.String(), answer) // v0.10.1476 — adlarıyla
	unknown = len(names)
	if c.Expect.MaxUnknownEntities != nil && unknown > *c.Expect.MaxUnknownEntities {
		fails = append(fails, fmt.Sprintf("unknown entities %d > %d: %v", unknown, *c.Expect.MaxUnknownEntities, names))
	}
	_ = st // tur / token metrikleri trail'de; puanı değiştirmez
	return fails, unknown
}

// ── Kalıcı iz (ai_eval_runs cases JSON'u, eklemeli) ────────────────────

// evalAgentStep — izin bir adımı (çip sırası).
type evalAgentStep struct {
	I       int    `json:"i"`
	Tool    string `json:"tool"`
	Origin  string `json:"origin,omitempty"` // "" model · "guided" sunucu ön-yüklemesi
	Args    string `json:"args"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped"`
	Ms      uint32 `json:"ms"`
	Bytes   uint32 `json:"bytes"`
	Preview string `json:"preview"`
}

// evalAgentTrail — ajan vakasının izi (≤ evalAgentTrailMax bayt JSON).
type evalAgentTrail struct {
	Tier string `json:"tier"`
	// Bound — v0.10.1476: "sentetik → canlı" bağlaması (yoksa boş).
	Bound        string `json:"bound,omitempty"`
	Route        string `json:"route,omitempty"`
	Rounds       int    `json:"rounds"`
	Calls        int    `json:"calls"`
	Executed     int    `json:"executed"`
	Skipped      int    `json:"skipped"`
	ModelCalls   int    `json:"modelCalls"`
	InputTokens  uint32 `json:"inputTokens"`
	OutputTokens uint32 `json:"outputTokens"`
	CachedTokens uint32 `json:"cachedTokens"`
	Capped       bool   `json:"capped"`
	Deep         bool   `json:"deep,omitempty"`
	// Overruns — METRİK aşımları (maxTokens / maxLatencyMs); vakayı kaldırmaz.
	Overruns       []string        `json:"overruns,omitempty"`
	Steps          []evalAgentStep `json:"steps"`
	StepsTruncated bool            `json:"stepsTruncated,omitempty"`
}

// evalAgentTrailFrom — SAF: sonuç + sayaçlar → iz (sınırlı).
func evalAgentTrailFrom(c evalCase, o evalAgentOutcome, st evalAgentStats, latencyMs int64) evalAgentTrail {
	steps := evalAgentSteps(o.Tools)
	tr := evalAgentTrail{
		Tier: o.Tier, Route: o.Route, Rounds: st.Rounds, Calls: len(steps), ModelCalls: int(o.ModelCalls),
		InputTokens: o.InputTokens, OutputTokens: o.OutputTokens, CachedTokens: st.CachedTokens,
		Capped: st.Capped, Deep: o.Deep, Steps: []evalAgentStep{},
	}
	if o.BoundTo != "" && c.BindService != "" {
		tr.Bound = c.BindService + " → " + o.BoundTo
	}
	for i, t := range steps {
		if evalStepSkipped(t) {
			tr.Skipped++
		} else {
			tr.Executed++
		}
		if i >= evalAgentStepsMax {
			tr.StepsTruncated = true
			continue
		}
		preview := t.Output
		if !t.OK {
			preview = t.Error
		}
		tr.Steps = append(tr.Steps, evalAgentStep{
			I: i + 1, Tool: t.Name, Origin: t.Origin, Args: clipBytes(t.Args, evalAgentArgsMax),
			OK: t.OK, Skipped: evalStepSkipped(t), Ms: t.Ms, Bytes: t.Bytes,
			Preview: clipBytes(preview, evalAgentPreviewMax),
		})
	}
	if lim := c.Expect.MaxTokens; lim > 0 {
		if tok := int(o.InputTokens + o.OutputTokens); tok > lim {
			tr.Overruns = append(tr.Overruns, fmt.Sprintf("token %d > %d", tok, lim))
		}
	}
	if lim := c.Expect.MaxLatencyMs; lim > 0 && latencyMs > int64(lim) {
		tr.Overruns = append(tr.Overruns, fmt.Sprintf("süre %d ms > %d ms", latencyMs, lim))
	}
	return evalBoundTrail(tr, evalAgentTrailMax)
}

// evalBoundTrail — SAF: izin JSON'u limit baytı aşıyorsa önce önizlemeler,
// sonra argümanlar kısalır, en son adımlar sondan düşer (StepsTruncated).
// Kırpma rune-güvenli (clipBytes).
func evalBoundTrail(tr evalAgentTrail, limit int) evalAgentTrail {
	size := func() int {
		b, _ := json.Marshal(tr)
		return len(b)
	}
	if size() <= limit {
		return tr
	}
	for _, capB := range []int{256, 128, 64, 0} {
		for i := range tr.Steps {
			tr.Steps[i].Preview = clipBytes(tr.Steps[i].Preview, capB)
		}
		if size() <= limit {
			return tr
		}
	}
	for _, capB := range []int{96, 32, 0} {
		for i := range tr.Steps {
			tr.Steps[i].Args = clipBytes(tr.Steps[i].Args, capB)
		}
		if size() <= limit {
			return tr
		}
	}
	for len(tr.Steps) > 0 && size() > limit {
		tr.Steps = tr.Steps[:len(tr.Steps)-1]
		tr.StepsTruncated = true
	}
	return tr
}

// evalAgentMeans — SAF: ajan sonuçlarının araç çağrısı ve token ortalaması
// (atlanan ve izsiz vakalar hariç); hiç yoksa nil, nil (ölçülmedi ≠ 0).
func evalAgentMeans(results []evalCaseResult) (toolCalls, tokens *float64) {
	var calls, tok float64
	n := 0
	for _, r := range results {
		if r.Skipped || r.Agent == nil {
			continue
		}
		n++
		calls += float64(r.Agent.Calls)
		tok += float64(r.Agent.InputTokens + r.Agent.OutputTokens)
	}
	if n == 0 {
		return nil, nil
	}
	a, b := calls/float64(n), tok/float64(n)
	return &a, &b
}

// evalCompareAgentNote — SAF: iki koşunun ajan vakalarında araç çağrısı
// ortalaması değiştiyse nota tek cümle. Bir taraf ölçmediyse (nil) not yok;
// kıyaslanabilirliği değiştirmez.
func evalCompareAgentNote(note string, base, head *float64) string {
	if base == nil || head == nil || fmt.Sprintf("%.1f", *base) == fmt.Sprintf("%.1f", *head) {
		return note
	}
	add := fmt.Sprintf("Ajan vakalarında araç çağrısı ortalaması %.1f → %.1f.", *base, *head)
	if strings.TrimSpace(note) == "" {
		return add
	}
	return strings.TrimRight(note, " ") + " " + add
}
