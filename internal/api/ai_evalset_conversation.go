package api

// ai_evalset_conversation.go — v0.10.2322 (H1 ölçüm tabanı;
// docs/cosre/conversation-audit.md §6.1, DECISIONS "CoSRE değerlendirme:
// konuşma kipi"): değerlendirme setinin KONUŞMA KİPİ.
//
// Ajan kipi (ai_evalset_agent.go) tek turu ölçer ve guided dışındaki
// konuşma kademelerini (netleştirme cevabı, kaynak sorusu, uç nokta kartı,
// rota turu, niyet, sohbet bağlamı) bilerek kapatır. Konuşma kipi vakası
// `turns[]` taşır; turlar SIRAYLA aynı konuşmada, gerçek v2 handler'ından
// ve üretimin TAM kademe zinciriyle koşar. Turlar arası durum gerçek bir
// istemcinin taşıdığı gibidir:
//
//   - konuşma kimliği: vaka × profil başına sabit (context.conversation);
//   - istemci geçmişi: önceki turların kullanıcı metni + cevap metni (düz
//     metin; istemci de araç çağrısı göndermez — sanitizeClientHistory);
//   - sunucu sohbet bağlamı (ChatContext): Redis yerine koşucunun BELLEK İÇİ
//     kopyası — her tur önceki turun bağlamıyla tohumlanır, tur bitince geri
//     okunur. Koşuyu başlatanın Redis bağlamına hiçbir şey yazılmaz
//     (kullanıcısız durum: flushChatContext yazmaz).
//
// Veri tarafı deterministiktir: döngü araçları ve guided kanıt paketleri
// vakanın sahte sonuçlarından gelir (ai_evalset_conv_seam.go). Model
// çağrıları GERÇEKTİR (koşunun profil(ler)i). Puanlama yapısaldır, metin
// eşleşmesi yoktur: hangi veri okumaları yapılabilir/yapılmalı, tur hangi
// varlığa ve pencereye çözülmeli, durum turunda en çok kaç cümle, cevapta
// sahte sonuçlarda olmayan sayı var mı. Oy / geri bildirim hiçbir aşamada
// girdi değildir (TestEvalNeverReadsVotes bu dosyayı da tarar).
//
// Küme gömülüdür (internal/copilot/evalset/conversation/*.json) ve YALNIZ
// açıkça seçilince koşar (yüzey ChatConversation): "tümü" koşusu ve günlük
// koşu bu turları içermez — ≈ 100 sohbet turu model yükü elle tetiklenir.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
)

// evalModeConversation — fikstürün `mode` değeri: konuşma kipi.
const evalModeConversation = "conversation"

// evalConversationSurface — konuşma kipi vakalarının yüzeyi (panel seçimi).
const evalConversationSurface = "ChatConversation"

// evalConvSchema — konuşma kümesi dosyalarının şeması.
const evalConvSchema = "coremetry.evalset.conversation/1"

// evalConvProfilesMax — koşu isteğinde en çok profil (yan yana iki aile).
const evalConvProfilesMax = 2

// Tavanlar (bayt) — vaka sonucu ai_eval_runs.cases JSON'unda sınırlı kalsın.
const (
	evalConvAnswerMax = 6 << 10
	evalConvArgsMax   = 256
	evalConvCallsMax  = 8
)

// evalConvDefaultCatalog — vaka catalog vermezse guided router'ın gördüğü
// nötr servis adları.
var evalConvDefaultCatalog = []string{"checkout", "orders", "payments", "inventory"}

// evalConvFixtureTurn — fikstürde bir kullanıcı turu.
type evalConvFixtureTurn struct {
	User string `json:"user"`
	// Stubs — bu turun sahte veri dünyası (vakanınkini ad bazında ezer).
	Stubs  map[string]json.RawMessage `json:"stubs,omitempty"`
	Expect evalConvExpect             `json:"expect"`
}

// evalConvExpect — turun YAPISAL beklentisi (asla metin eşleşmesi değil).
// Adlar veri okumasıdır: döngü aracı ("get_service_health") ya da guided
// rota niyeti ("service_health").
type evalConvExpect struct {
	// Class — sorunun sınıfı (rapor gruplaması; puanlamaz): status |
	// followup | deploy | next_step | error_hunt | explain | compare |
	// knowledge | smalltalk | no_finding | correction.
	Class string `json:"class"`
	// AnyOfCalls — bu adlardan en az biri okunmalı.
	AnyOfCalls []string `json:"anyOfCalls,omitempty"`
	// ForbiddenCalls — bu adlar okunmamalı.
	ForbiddenCalls []string `json:"forbiddenCalls,omitempty"`
	// NoCalls — tur hiç veri okumamalı (sohbet, bilgi).
	NoCalls bool `json:"noCalls,omitempty"`
	// MaxCalls — en çok veri okuması (nil = sınırsız).
	MaxCalls *int `json:"maxCalls,omitempty"`
	// Entity — turun okumaları bu servise çözülmeli.
	Entity string `json:"entity,omitempty"`
	// NotEntity — okumalar bu servislere gitmemeli (düzeltme turu).
	NotEntity []string `json:"notEntity,omitempty"`
	// Window — turun çözmesi gereken pencere: 15m | 1h | 6h | 24h | 7d
	// (şimdiden geriye); yesterday (dünün takvim günü), morning ("bu sabah"
	// 06:00–12:00) ya da yesterday_morning (sabah penceresinden sonra çıplak
	// "dün" = dün sabah). v0.10.2328: takvim pencereleri koşunun SABİT saat
	// diliminde (evalConvTZ) ve çözücünün kuralıyla (chat_state_resolve.go).
	Window string `json:"window,omitempty"`
	// MaxSentences — en çok cümle (durum soruları kısa); 0 = sınırsız.
	MaxSentences int `json:"maxSentences,omitempty"`
	// Verifier — v0.10.2328 (E1): "pass" = gölge doğrulayıcı sıfır uyuşmazlık
	// saymalı (türetilen serbest); "flag" = sahtede olmayan değeri soran
	// yoklama: cevap sahtede olmayan bir sayı söylerse doğrulayıcı onu
	// uyuşmazlık saymalı (kaçırırsa ihlal). Doğrulama koşmadıysa denetlenmez.
	Verifier string `json:"verifier,omitempty"`
	// NoBoilerplate — v0.10.2338 (D): üslup açık koşulan turda cevap kalıp
	// açılış/kapanış taşımamalı (evalrubric.FluencyOf). Üslup kapalıysa denetlenmez.
	NoBoilerplate bool `json:"noBoilerplate,omitempty"`
	// ArgsContain — v0.10.2356 (S2 arama): turun en az bir okumasının ham
	// argümanı bu parçaların HEPSİNİ içermeli (ör. `"client_ip":"192.0.2.10"`);
	// sunucunun devralıp eklediği argüman dahil.
	ArgsContain []string `json:"argsContain,omitempty"`
	// Evidence — v0.10.2360 (S3): kanıt bloğu beklentisi; yalnız kanıt kipi
	// on koşan turda denetlenir (evalrubric.EvidenceOf üç ölçütü).
	Evidence *evalConvEvidenceExpect `json:"evidence,omitempty"`
	// Pack — v0.10.2361 (S4): true = kanıt paketi koşmalı (en az bir adım
	// tamamlanmış), false = koşmamalı. MaxRounds — döngü model turu tavanı.
	Pack      *bool `json:"pack,omitempty"`
	MaxRounds *int  `json:"maxRounds,omitempty"`
}

// evalConvEvidenceExpect — v0.10.2360: cevapta olması gereken blok tipleri
// ve en çok blok (bilgi / sohbet turunda 0).
type evalConvEvidenceExpect struct {
	Require []string `json:"require,omitempty"`
	Max     *int     `json:"max,omitempty"`
}

// Doğrulayıcı beklentileri.
const (
	evalConvVerifyPass = "pass"
	evalConvVerifyFlag = "flag"
	// evalConvVerifyAnnotate — v0.10.2339 (E2): "flag" + zorla kipinde koşan
	// turda sahtede olmayan sayı cevapta "(doğrulanmamış değer)" ile işaretli olmalı.
	evalConvVerifyAnnotate = "annotate"
)

// evalConvTZ — v0.10.2328: konuşma kipi isteklerinin SABİT saat dilimi
// (context.tz). Çözücü dilimsiz istekte kurulum varsayılanını kullanır;
// sabitlenince "dün" / "bu sabah" beklentisi koşulan makineden bağımsızdır.
const evalConvTZ = "Europe/Istanbul"

// evalConvVerify — turun gölge doğrulama özeti (ai_eval_runs.cases'te sınırlı).
type evalConvVerify struct {
	Checked    int      `json:"checked"`
	Verified   int      `json:"verified"`
	Derived    int      `json:"derived"`
	Mismatch   int      `json:"mismatch"`
	Mismatches []string `json:"mismatches,omitempty"`
}

// evalConvVerifyOf — SAF: doğrulama sonucu → özet (uyuşmayan jeton ≤8).
func evalConvVerifyOf(r *verifyResult) *evalConvVerify {
	if r == nil {
		return nil
	}
	t, _ := r.totals()
	v := &evalConvVerify{Checked: t.Checked, Verified: t.Verified, Derived: t.Derived, Mismatch: t.Mismatch}
	for _, c := range r.mismatches() {
		if len(v.Mismatches) >= verifySignalMaxTokens {
			break
		}
		v.Mismatches = append(v.Mismatches, c.Kind+":"+clipBytes(c.Text, verifySignalTokenMax))
	}
	return v
}

// evalConvTurnResult — bir turun saklanan sonucu (ai_eval_runs.cases).
type evalConvTurnResult struct {
	I               int            `json:"i"`
	User            string         `json:"user"`
	Class           string         `json:"class"`
	Answer          string         `json:"answer"`
	AnswerTruncated bool           `json:"answerTruncated,omitempty"`
	Tier            string         `json:"tier"`
	Route           string         `json:"route,omitempty"`
	Status          string         `json:"status,omitempty"`
	Error           string         `json:"error,omitempty"`
	OK              bool           `json:"ok"`
	Fails           []string       `json:"fails"`
	Calls           []evalConvCall `json:"calls"`
	Services        []string       `json:"services,omitempty"`
	Windows         []string       `json:"windows,omitempty"`
	StateService    string         `json:"stateService,omitempty"`
	StateRangeS     int64          `json:"stateRangeS,omitempty"`
	MaxSentences    int            `json:"maxSentences,omitempty"`
	Rounds          int            `json:"rounds"`
	ToolCalls       int            `json:"toolCalls"`
	ModelCalls      int            `json:"modelCalls"`
	InputTokens     uint32         `json:"inputTokens"`
	OutputTokens    uint32         `json:"outputTokens"`
	LatencyMs       int64          `json:"latencyMs"`
	Toolset         string         `json:"toolset,omitempty"`
	// v0.10.2365 (S6) — koşunun araştırma profili ve ayrımın sinyalleri
	// (research_no_tools / research_fallback / narrate_fallback).
	ResearchProfile string   `json:"researchProfile,omitempty"`
	Split           []string `json:"split,omitempty"`
	// v0.10.2323 — v0.10.2318 döngü telemetrisi (ai_calls: ttft_ms,
	// loop_model_ms, loop_tool_ms, loop_tool_calls; tur sayısı model_rounds).
	// 0 = bilinmiyor (döngü koşmadı ya da akış yok).
	LoopRounds    int    `json:"loopRounds,omitempty"`
	LoopToolCalls int    `json:"loopToolCalls,omitempty"`
	LoopModelMs   uint32 `json:"loopModelMs,omitempty"`
	LoopToolMs    uint32 `json:"loopToolMs,omitempty"`
	TTFTMs        uint32 `json:"ttftMs,omitempty"`
	// StateToMs — v0.10.2328: tur sonu durum penceresinin bitişi (mutlak pencere; 0 = göreli).
	StateToMs int64 `json:"stateToMs,omitempty"`
	// Verify — v0.10.2328 (E1): gölge doğrulayıcının sayaçları; nil = koşmadı.
	Verify *evalConvVerify `json:"verify,omitempty"`
	// v0.10.2331 (B) — turun cevap sınıfı (sunucunun saf eşlemesi; "" =
	// anlatımsız kademe) ve turun koştuğu üslup anahtarı (on | off; forced =
	// koşu isteği zorladı).
	AnswerClass string `json:"answerClass,omitempty"`
	Style       string `json:"style,omitempty"`
	StyleForced bool   `json:"styleForced,omitempty"`
	// SeamDropped — v0.10.2338 (D): cevap dikişinin sildiği model kalıp cümlesi
	// (ilk / son); akıcılık raporunun "model yazdı" oranı bunu da sayar.
	SeamDropped int `json:"seamDropped,omitempty"`
	// Verification / Enforce — v0.10.2339 (E2): turun koştuğu doğrulama kipi
	// (off | shadow | enforce; forced = koşu ya da vaka zorladı) ve zorla
	// kipinin kararı (pass | annotated | regenerated…; "" = koşmadı).
	Verification       string `json:"verification,omitempty"`
	VerificationForced bool   `json:"verificationForced,omitempty"`
	Enforce            string `json:"enforce,omitempty"`
	// EvidenceMode / EvidenceBlocks — v0.10.2360 (S3): turun kanıt kipi ve
	// cevaba eklenen blok tipleri ("" = karar koşmadı).
	EvidenceMode   string   `json:"evidenceMode,omitempty"`
	EvidenceBlocks []string `json:"evidenceBlocks,omitempty"`
	// Pack / PackOK / PackUnchecked — v0.10.2361 (S4): kanıt paketi koştu mu,
	// tamamlanan ve bakılamayan adım sayısı.
	Pack          bool   `json:"pack,omitempty"`
	PackOK        int    `json:"packOk,omitempty"`
	PackUnchecked int    `json:"packUnchecked,omitempty"`
	packDigest    string // kanıt metnine (sunucu türevi sayılar)
}

// evalConvCase — SAF: vaka konuşma kipinde mi.
func evalConvCase(c evalCase) bool { return c.Mode == evalModeConversation }

// evalConvCaseCount — SAF: plandaki konuşma vakası sayısı.
func evalConvCaseCount(cases []evalCase) int {
	n := 0
	for _, c := range cases {
		if evalConvCase(c) {
			n++
		}
	}
	return n
}

// ── Yükleyici ───────────────────────────────────────────────────────────

// evalConversationLoader — test dikişi.
var evalConversationLoader = func() ([]evalCase, error) { return loadEvalConversationFS(evalset.ConversationFS) }

// loadEvalConversationFS — konuşma kümesi (conversation/*.json, ad sıralı).
func loadEvalConversationFS(fsys fs.FS) ([]evalCase, error) {
	files, err := fs.Glob(fsys, "conversation/*.json")
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
		if ef.Schema != evalConvSchema {
			return nil, fmt.Errorf("%s: schema %q, want %q", f, ef.Schema, evalConvSchema)
		}
		for i := range ef.Cases {
			c := ef.Cases[i]
			c.file = path.Base(f)
			c.Mode = evalModeConversation
			if c.Surface == "" {
				c.Surface = evalConversationSurface
			}
			if c.Surface != evalConversationSurface {
				return nil, fmt.Errorf("%s/%s: surface %q, want %q", f, c.ID, c.Surface, evalConversationSurface)
			}
			if len(c.Turns) == 0 {
				return nil, fmt.Errorf("%s/%s: turns boş", f, c.ID)
			}
			exp := make([]evalConvExpect, len(c.Turns))
			for j, t := range c.Turns {
				exp[j] = t.Expect
			}
			c.expectRaw, _ = json.Marshal(map[string]any{"turns": exp})
			out = append(out, c)
		}
	}
	return out, nil
}

// evalWantsConversation — SAF: koşu isteği konuşma yüzeyini açıkça seçti mi
// (boş seçim = "tümü" konuşma kümesini İÇERMEZ).
func evalWantsConversation(surfaces []string) bool {
	for _, s := range surfaces {
		if strings.TrimSpace(s) == evalConversationSurface {
			return true
		}
	}
	return false
}

// evalNormProfiles — SAF: istekteki profil listesi (kırpılmış, tekil, sıra
// korunur). Tavan aşılırsa hata.
func evalNormProfiles(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) > evalConvProfilesMax {
		return nil, fmt.Errorf("en çok %d profil seçilebilir", evalConvProfilesMax)
	}
	return out, nil
}

// evalExpandProfiles — SAF: plan × profil. Profiller SIRAYLA (önce birincinin
// bütün vakaları, sonra ikincinin); iki profilde öğe kimliği "<vaka>@<profil>".
// Profil yoksa plan aynen.
func evalExpandProfiles(plan []evalCase, profiles []string) []evalCase {
	if len(profiles) == 0 {
		return plan
	}
	out := make([]evalCase, 0, len(plan)*len(profiles))
	for _, p := range profiles {
		for _, c := range plan {
			c.profile = p
			if len(profiles) > 1 {
				c.ID = c.ID + "@" + p
			}
			out = append(out, c)
		}
	}
	return out
}

// evalCaseBaseID — SAF: "<vaka>@<profil>" → "<vaka>".
func evalCaseBaseID(id string) string {
	if i := strings.LastIndex(id, "@"); i > 0 {
		return id[:i]
	}
	return id
}

// ── Koşucu ──────────────────────────────────────────────────────────────

// runEvalConvCase — konuşma vakasını tur tur koşar. Profil c.profile (yoksa
// üretimin "chat" eşlemesi — ctx'te WithProfile). Koşu bağlamı biterse kalan
// turlar koşmaz (runEvalJob vakayı saymaz).
func (s *Server) runEvalConvCase(ctx context.Context, job *evalJob, c evalCase) evalCaseOutcome {
	out := evalCaseOutcome{User: evalConvTranscriptUsers(c), Mode: evalModeConversation}
	if s == nil || s.copilot == nil {
		out.Skipped, out.SkipReason = true, "CoSRE yapılandırılmamış"
		return out
	}
	t0 := time.Now()
	catalog := c.Catalog
	if len(catalog) == 0 {
		catalog = evalConvDefaultCatalog
	}
	convID := "eval-" + job.row.ID + "-" + c.ID
	var (
		hist     []copilot.ChatMessage
		state    ChatContext
		evidence strings.Builder // sahte sonuçlar + kullanıcı metinleri (sayı denetimi)
		answers  []string
		firstErr error
		fails    []string
	)
	for k, v := range c.Stubs {
		evidence.WriteString(k + " " + evalStubText(v) + "\n")
	}
	for i, ft := range c.Turns {
		if ctx.Err() != nil {
			break
		}
		evidence.WriteString(ft.User + "\n")
		for k, v := range ft.Stubs {
			evidence.WriteString(k + " " + evalStubText(v) + "\n")
		}
		tr, err := s.runEvalConvTurn(ctx, job, c, i, ft, hist, &state, convID, catalog)
		evidence.WriteString(tr.packDigest + "\n")          // v0.10.2361 — paket özeti sunucu kanıtıdır
		evidence.WriteString(evalAssumeEvidence(tr.Answer)) // v0.10.2332 — sunucunun varsayım cümlesi kanıttır (model sayısı değil)
		tr.Fails = append(tr.Fails, scoreEvalConvTurn(ft.Expect, tr, evidence.String(), time.Now(), evalConvLoc())...)
		tr.OK = len(tr.Fails) == 0
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, f := range tr.Fails {
			fails = append(fails, fmt.Sprintf("tur %d: %s", i+1, f))
		}
		if out.Toolset == "" {
			out.Toolset = tr.Toolset
		}
		answers = append(answers, tr.Answer)
		hist = append(hist, copilot.ChatMessage{Role: "user", Text: ft.User})
		if a := strings.TrimSpace(tr.Answer); a != "" {
			hist = append(hist, copilot.ChatMessage{Role: "assistant", Text: tr.Answer})
		}
		tr.Answer, tr.AnswerTruncated = evalTruncate(tr.Answer, evalConvAnswerMax)
		out.Turns = append(out.Turns, tr)
	}
	out.LatencyMs = time.Since(t0).Milliseconds()
	out.Answer = evalConvTranscript(c, answers)
	out.Err = firstErr
	out.Fails = fails
	in := evalrubric.Input{Answer: strings.Join(answers, "\n\n"), Err: firstErr, ExpectTurkish: true}
	if len(answers) == 0 {
		in.Err = errors.New("hiçbir tur koşmadı")
	}
	out.Rubric = evalrubric.Score(in)
	return out
}

// runEvalConvTurn — tek tur: gerçek handler, bellek içi bağlam, sahte veri.
func (s *Server) runEvalConvTurn(ctx context.Context, job *evalJob, c evalCase, i int, ft evalConvFixtureTurn,
	hist []copilot.ChatMessage, state *ChatContext, convID string, catalog []string) (evalConvTurnResult, error) {
	tr := evalConvTurnResult{I: i + 1, User: ft.User, Class: ft.Expect.Class, MaxSentences: ft.Expect.MaxSentences, Fails: []string{}, Calls: []evalConvCall{}}
	style, verif := job.styleOverride, job.verifyOverride // v0.10.2339 — koşu > vaka > canlı ayar
	if style == nil {
		style = c.ConvStyle
	}
	if verif == "" {
		verif = c.Verification
	}
	ct := &evalConvTurn{
		State:        &chatContextState{ctx: *state},
		Stubs:        evalMergeStubs(c.Stubs, ft.Stubs),
		Catalog:      catalog,
		Style:        style,      // v0.10.2331 — yalnız bu koşu (nil = canlı ayar)
		Verification: verif,      // v0.10.2339
		Evidence:     c.Evidence, // v0.10.2360
	}
	tr.Style, tr.StyleForced = evalStyleLabel(style)
	tr.Verification, tr.VerificationForced = evalVerifyLabel(verif)
	col := &evalAgentCollector{parts: newEvalPartSink()}
	em := &chatEvalMode{
		Surface: evalsetSurfacePrefix + c.Surface, ExchangeID: fmt.Sprintf("%s/%s/t%d", job.row.ID, c.ID, i+1),
		Toolset: job.toolsetOverride, // "" = canlı çözüm (profilin araç seti)
		OnTurn:  col.onTurn, OnTool: col.onTool, Sink: col.sink, OnCatalog: col.onCatalog,
		Conversation: ct,
		// v0.10.2365 (S6) — "B araştırır → A anlatır" sütunu; sinyaller tura yazılır.
		ResearchProfile: job.researchProfile,
		OnSplit:         func(sig string) { tr.Split = append(tr.Split, sig) },
	}
	tr.ResearchProfile = job.researchProfile
	cctx := copilot.WithRecordSurfacePrefix(ctx, evalsetSurfacePrefix)
	cctx = auth.ContextWithClaims(cctx, &auth.Claims{UserID: job.userID, Email: job.userEmail, Role: job.userRole})
	cctx = withChatEvalMode(cctx, em)
	cctx = withCosreV2Loop(cctx)
	cctx, cancel := context.WithTimeout(cctx, evalAgentCaseMax(s.copilot.ClientTimeout()))
	defer cancel()
	var req chatRequest
	req.Messages = append(append([]copilot.ChatMessage{}, hist...), copilot.ChatMessage{Role: "user", Text: ft.User})
	req.Context.Conversation = convID
	req.Context.Profile = c.profile
	req.Context.Tz = evalConvTZ // v0.10.2328 — "dün" / "bu sabah" deterministik
	body, err := json.Marshal(req)
	if err != nil {
		return tr, err
	}
	hreq, err := http.NewRequestWithContext(cctx, http.MethodPost, "/api/copilot/chat", bytes.NewReader(body))
	if err != nil {
		return tr, err
	}
	t0 := time.Now()
	w := newEvalDiscardResponse()
	s.copilotChatWith(w, hreq, evalV2SinkFactory(col.parts))
	tr.LatencyMs = time.Since(t0).Milliseconds()
	o, st, ok := col.result()
	*state = ct.State.ctx
	tr.Calls = evalBoundConvCalls(ct.snapshot())
	tr.Services, tr.Windows = evalConvResolved(tr.Calls, time.Now())
	tr.StateService, tr.StateRangeS, tr.StateToMs = state.Service, state.RangeS, state.WinToMs
	tr.Verify = evalConvVerifyOf(o.Verify) // v0.10.2328
	tr.Answer, tr.Tier, tr.Route, tr.Status, tr.Toolset = o.Answer, o.Tier, o.Route, o.Status, o.Toolset
	tr.Rounds, tr.ToolCalls, tr.ModelCalls = st.Rounds, st.Executed, int(o.ModelCalls)
	tr.InputTokens, tr.OutputTokens = o.InputTokens, o.OutputTokens
	tr.LoopRounds, tr.LoopToolCalls = int(o.Loop.Rounds), int(o.Loop.ToolCalls) // v0.10.2323
	tr.LoopModelMs, tr.LoopToolMs, tr.TTFTMs = o.Loop.ModelMs, o.Loop.ToolMs, o.TTFTMs
	tr.AnswerClass = ct.answerClass()                               // v0.10.2331
	tr.SeamDropped = ct.seamDropped()                               // v0.10.2338
	tr.Enforce = ct.enforceMode()                                   // v0.10.2339
	tr.EvidenceMode, tr.EvidenceBlocks = ct.evidence()              // v0.10.2360
	tr.Pack, tr.PackOK, tr.PackUnchecked, tr.packDigest = ct.pack() // v0.10.2361
	if !ok {
		err = fmt.Errorf("sohbet handler'ı sonuç bildirmedi (HTTP %d)", w.status())
		tr.Error = err.Error()
		tr.Fails = append(tr.Fails, "error: "+tr.Error)
		return tr, err
	}
	if strings.TrimSpace(o.Answer) == "" {
		why := o.Status
		if o.ErrMsg != "" {
			why += ": " + o.ErrMsg
			tr.Error = o.ErrMsg
		}
		tr.Fails = append(tr.Fails, "no answer ("+why+")")
	}
	return tr, nil
}

// evalMergeStubs — SAF: vaka + tur sahteleri (tur ezer).
func evalMergeStubs(base, over map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// evalBoundConvCalls — SAF: okumalar listesi tavanlı (argümanlar kırpılır).
func evalBoundConvCalls(calls []evalConvCall) []evalConvCall {
	out := make([]evalConvCall, 0, min(len(calls), evalConvCallsMax))
	for i, c := range calls {
		if i >= evalConvCallsMax {
			break
		}
		if len(c.Args) > evalConvArgsMax || !json.Valid(c.Args) {
			b, _ := json.Marshal(clipBytes(string(c.Args), evalConvArgsMax))
			c.Args = b
		}
		out = append(out, c)
	}
	return out
}

// evalConvTranscriptUsers — SAF: vakanın kullanıcı turları (panel "girdi").
func evalConvTranscriptUsers(c evalCase) string {
	var b strings.Builder
	for i, t := range c.Turns {
		fmt.Fprintf(&b, "K%d: %s\n", i+1, t.User)
	}
	return strings.TrimRight(b.String(), "\n")
}

// evalConvTranscript — SAF: kullanıcı + cevap dökümü (panel "cevap").
func evalConvTranscript(c evalCase, answers []string) string {
	var b strings.Builder
	for i, a := range answers {
		if i < len(c.Turns) {
			fmt.Fprintf(&b, "K%d: %s\n", i+1, c.Turns[i].User)
		}
		fmt.Fprintf(&b, "C%d: %s\n\n", i+1, strings.TrimSpace(a))
	}
	return strings.TrimSpace(b.String())
}

// ── Yapısal puanlama (SAF) ──────────────────────────────────────────────

// evalConvArgsContain — SAF (v0.10.2356): bir okumanın ham argümanı bütün parçaları içeriyor mu.
func evalConvArgsContain(calls []evalConvCall, parts []string) bool {
	for _, c := range calls {
		var buf bytes.Buffer
		args := string(c.Args)
		if json.Compact(&buf, c.Args) == nil {
			args = buf.String()
		}
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(args, p)
		}
		if ok {
			return true
		}
	}
	return false
}

// evalConvLoc — koşunun sabit dilimi (çözülemezse kurulum varsayılanı; chatLocationNamed).
func evalConvLoc() *time.Location { return chatLocationNamed(evalConvTZ, 0) }

// scoreEvalConvTurn — SAF: turun yapısal ihlalleri. evidence = vakanın o
// tura dek sahte sonuçları + kullanıcı metinleri; now = pencere denetiminin
// "şimdi"si, loc = koşunun sabit dilimi.
func scoreEvalConvTurn(e evalConvExpect, tr evalConvTurnResult, evidence string, now time.Time, loc *time.Location) []string {
	var fails []string
	names := map[string]bool{}
	for _, c := range tr.Calls {
		names[c.Name] = true
	}
	if e.NoCalls && len(tr.Calls) > 0 {
		fails = append(fails, fmt.Sprintf("veri okunmamalıydı: %v", evalConvCallNames(tr.Calls)))
	}
	if len(e.AnyOfCalls) > 0 {
		hit := false
		for _, n := range e.AnyOfCalls {
			hit = hit || names[n]
		}
		if !hit {
			fails = append(fails, fmt.Sprintf("beklenen okumalardan hiçbiri yok: %v (okunan %v)", e.AnyOfCalls, evalConvCallNames(tr.Calls)))
		}
	}
	for _, n := range e.ForbiddenCalls {
		if names[n] {
			fails = append(fails, "yasak okuma: "+n)
		}
	}
	if len(e.ArgsContain) > 0 && !evalConvArgsContain(tr.Calls, e.ArgsContain) {
		fails = append(fails, fmt.Sprintf("argüman beklentisi tutmadı: %q", e.ArgsContain))
	}
	if e.MaxCalls != nil && len(tr.Calls) > *e.MaxCalls {
		fails = append(fails, fmt.Sprintf("okuma %d > %d", len(tr.Calls), *e.MaxCalls))
	}
	if e.Entity != "" && !evalNameIn(e.Entity, tr.Services) && !strings.EqualFold(e.Entity, tr.StateService) {
		fails = append(fails, fmt.Sprintf("varlık %q çözülmedi (okumalar %v, bağlam %q)", e.Entity, tr.Services, tr.StateService))
	}
	for _, n := range e.NotEntity {
		if evalNameIn(n, tr.Services) {
			fails = append(fails, fmt.Sprintf("düzeltilen varlık yine okundu: %q", n))
		}
	}
	if e.Window != "" && !evalConvWindowOK(e.Window, tr.Calls, tr.StateRangeS, tr.StateToMs, now, loc) {
		fails = append(fails, fmt.Sprintf("pencere %q çözülmedi (okumalar %v, bağlam %d sn)", e.Window, tr.Windows, tr.StateRangeS))
	}
	if e.Pack != nil && *e.Pack != (tr.Pack && tr.PackOK > 0) { // v0.10.2361 — S4
		fails = append(fails, fmt.Sprintf("kanıt paketi beklentisi %v, koştu %v (%d adım)", *e.Pack, tr.Pack, tr.PackOK))
	}
	if e.MaxRounds != nil && tr.LoopRounds > *e.MaxRounds {
		fails = append(fails, fmt.Sprintf("döngü turu %d > %d", tr.LoopRounds, *e.MaxRounds))
	}
	answer := strings.TrimSpace(tr.Answer)
	if answer == "" {
		return fails
	}
	if e.MaxSentences > 0 {
		if n := evalrubric.Sentences(answer); n > e.MaxSentences {
			fails = append(fails, fmt.Sprintf("cümle %d > %d", n, e.MaxSentences))
		}
	}
	extra := ungroundedNumbers(evalrubric.StripSourceLine(answer), evidence)
	if len(extra) > 0 {
		fails = append(fails, fmt.Sprintf("sahte sonuçlarda olmayan sayı: %v", extra))
	}
	if v := tr.Verify; v != nil {
		switch e.Verifier {
		case evalConvVerifyPass:
			if v.Mismatch > 0 {
				fails = append(fails, fmt.Sprintf("doğrulayıcı uyuşmazlığı: %v", v.Mismatches))
			}
		case evalConvVerifyFlag, evalConvVerifyAnnotate:
			if len(extra) > 0 && v.Mismatch == 0 {
				fails = append(fails, fmt.Sprintf("doğrulayıcı sahtede olmayan sayıyı kaçırdı: %v", extra))
			}
		}
	}
	// v0.10.2339 (E2) — zorla kipinde uydurulan değer işaretsiz kalmamalı.
	if e.Verifier == evalConvVerifyAnnotate && len(extra) > 0 && tr.Verification == chstore.CosreVerifyEnforce &&
		!strings.Contains(answer, verifyFootnotePrefix) {
		fails = append(fails, fmt.Sprintf("zorla kipinde sahtede olmayan değer işaretlenmedi: %v", extra))
	}
	// v0.10.2360 (S3) — kanıt ölçütleri (yalnız kanıt kipi on koşan turda).
	if e.Evidence != nil && tr.EvidenceMode == chstore.CosreEvidenceOn {
		sc := evalrubric.EvidenceOf(evalrubric.EvidenceInput{Answer: answer, Calls: len(tr.Calls), Attached: tr.EvidenceBlocks,
			Ungrounded: len(extra), Require: e.Evidence.Require, Max: e.Evidence.Max})
		if (sc.ClaimsHaveEvidence != nil && !*sc.ClaimsHaveEvidence) || !sc.NoPile || !sc.NoGuessAsData {
			fails = append(fails, fmt.Sprintf("kanıt ölçütü: %v", sc.Notes))
		}
	}
	// v0.10.2338 (D) — üslup açıkken kalıp açılış/kapanış kalmamalı.
	if e.NoBoilerplate && tr.Style == "on" {
		if f := evalrubric.FluencyOf(answer); f.BoilerplateOpening || f.BoilerplateClosing {
			fails = append(fails, "üslup açıkken kalıp açılış/kapanış kaldı")
		}
	}
	return fails
}

func evalConvCallNames(calls []evalConvCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Name)
	}
	return out
}

// evalConvArgs — SAF: okuma argümanları (bozuksa boş).
func evalConvArgs(c evalConvCall) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(c.Args, &m)
	return m
}

// evalConvResolved — SAF: okumaların servisleri ve pencereleri (insan
// okur: "1h", "from..to").
func evalConvResolved(calls []evalConvCall, now time.Time) (services, windows []string) {
	seenS, seenW := map[string]bool{}, map[string]bool{}
	for _, c := range calls {
		m := evalConvArgs(c)
		for _, svc := range evalConvServicesOf(m) {
			if k := strings.ToLower(svc); !seenS[k] {
				seenS[k] = true
				services = append(services, svc)
			}
		}
		for _, w := range evalConvWindowsOf(m, now) {
			if !seenW[w.label] {
				seenW[w.label] = true
				windows = append(windows, w.label)
			}
		}
	}
	return services, windows
}

func evalConvServicesOf(m map[string]any) []string {
	var out []string
	for _, k := range []string{"service", "service_name", "serviceName"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	if arr, ok := m["services"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

// evalConvWindow — çözülmüş bir pencere: geriye bakış (lookback) ya da
// mutlak [from, to].
type evalConvWindow struct {
	lookback time.Duration
	from, to time.Time
	label    string
}

var evalConvDurRe = regexp.MustCompile(`^(\d+)\s*(m|min|h|d|g|sa|dk|saat|gün|dakika)$`)

// evalParseLookback — SAF: "15m" | "1h" | "24h" | "7d" (Türkçe kısaltmalar da).
func evalParseLookback(s string) (time.Duration, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, true
	}
	m := evalConvDurRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	switch m[2] {
	case "m", "min", "dk", "dakika":
		return time.Duration(n) * time.Minute, true
	case "h", "sa", "saat":
		return time.Duration(n) * time.Hour, true
	default:
		return time.Duration(n) * 24 * time.Hour, true
	}
}

// evalConvWindowsOf — SAF: tek okumanın pencereleri.
func evalConvWindowsOf(m map[string]any, now time.Time) []evalConvWindow {
	var out []evalConvWindow
	for _, k := range []string{"window", "range", "lookback", "since", "duration"} {
		if v, ok := m[k].(string); ok {
			if d, ok := evalParseLookback(v); ok {
				out = append(out, evalConvWindow{lookback: d, label: d.String()})
			}
		}
	}
	for k, unit := range map[string]time.Duration{"minutes": time.Minute, "hours": time.Hour, "rangeS": time.Second, "range_s": time.Second, "window_s": time.Second} {
		if v, ok := m[k].(float64); ok && v > 0 {
			d := time.Duration(v) * unit
			out = append(out, evalConvWindow{lookback: d, label: d.String()})
		}
	}
	from, to, okAbs := evalConvAbsWindow(m)
	if okAbs {
		w := evalConvWindow{from: from, to: to, label: from.Format("2006-01-02T15:04") + ".." + to.Format("2006-01-02T15:04")}
		if now.Sub(to).Abs() <= 10*time.Minute {
			w.lookback = to.Sub(from)
		}
		out = append(out, w)
	}
	return out
}

// evalConvAbsWindow — SAF: mutlak pencere (from_ms/to_ms ya da
// from_iso/to_iso); yoksa ok=false.
func evalConvAbsWindow(m map[string]any) (from, to time.Time, ok bool) {
	fromV, okF := m["from_ms"].(float64)
	toV, okT := m["to_ms"].(float64)
	if okF && okT && toV > fromV {
		return time.UnixMilli(int64(fromV)).UTC(), time.UnixMilli(int64(toV)).UTC(), true
	}
	fs, okF := m["from_iso"].(string)
	ts, okT := m["to_iso"].(string)
	if okF && okT {
		f, err1 := time.Parse(time.RFC3339, fs)
		t, err2 := time.Parse(time.RFC3339, ts)
		if err1 == nil && err2 == nil && t.After(f) {
			return f.UTC(), t.UTC(), true
		}
	}
	return time.Time{}, time.Time{}, false
}

// evalConvCalendarWindow — SAF: takvim penceresi beklentisinin [from, to)'su,
// çözücünün kuralıyla (chat_state_resolve.go resolveTurnWindow) koşunun
// diliminde: yesterday = dünün tamamı; morning = bugün 06–12 (06'dan önce
// dünün sabahı), şimdiye kırpılır; yesterday_morning = sabah penceresinin bir
// gün gerisi — ama sabah penceresi zaten dündeyse (yerel 06'dan önce) çıplak
// "dün" dünün tamamıdır. ok=false: takvim beklentisi değil.
func evalConvCalendarWindow(want string, now time.Time, loc *time.Location) (from, to time.Time, ok bool) {
	today := dayStart(now, loc)
	morning := func() (time.Time, time.Time) {
		f, t := today.Add(6*time.Hour), today.Add(12*time.Hour)
		if f.After(now) {
			f, t = f.AddDate(0, 0, -1), t.AddDate(0, 0, -1)
		}
		if t.After(now) {
			t = now
		}
		return f, t
	}
	switch want {
	case "yesterday":
		return today.AddDate(0, 0, -1), today, true
	case "morning":
		f, t := morning()
		return f, t, true
	case "yesterday_morning":
		f, t := morning()
		if f.Before(today) {
			return today.AddDate(0, 0, -1), today, true
		}
		return f.Add(-24 * time.Hour), t.Add(-24 * time.Hour), true
	}
	return time.Time{}, time.Time{}, false
}

// evalConvWindowOK — SAF: beklenen pencere okumalardan ya da sohbet
// bağlamından çözüldü mü. Geriye bakış ±%10. Takvim pencereleri
// (evalConvCalendarWindow) mutlak uçlarla, ±15 dk: okumaların from/to'su ya
// da durumun mutlak penceresi (StateToMs − RangeS … StateToMs).
func evalConvWindowOK(want string, calls []evalConvCall, stateRangeS, stateToMs int64, now time.Time, loc *time.Location) bool {
	var ws []evalConvWindow
	for _, c := range calls {
		ws = append(ws, evalConvWindowsOf(evalConvArgs(c), now)...)
	}
	if from, to, ok := evalConvCalendarWindow(want, now, loc); ok {
		if stateToMs > 0 && stateRangeS > 0 {
			st := time.UnixMilli(stateToMs)
			ws = append(ws, evalConvWindow{from: st.Add(-time.Duration(stateRangeS) * time.Second), to: st})
		}
		const tol = 15 * time.Minute
		for _, w := range ws {
			if !w.to.IsZero() && w.from.Sub(from).Abs() <= tol && w.to.Sub(to).Abs() <= tol {
				return true
			}
		}
		return false
	}
	d, ok := evalParseLookback(want)
	if !ok {
		return false
	}
	near := func(got time.Duration) bool {
		return got > 0 && math.Abs(float64(got-d)) <= 0.1*float64(d)
	}
	for _, w := range ws {
		if near(w.lookback) {
			return true
		}
	}
	return len(calls) == 0 && near(time.Duration(stateRangeS)*time.Second)
}
