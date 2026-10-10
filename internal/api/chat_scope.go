package api

// chat_scope.go — v0.10.1138 (CoSRE sohbet iyileştirmeleri, operatör onaylı):
// composer'daki @-anma ve /-komutlarının SUNUCU yarısı.
//
// İstemci metni operatörün yazdığı gibi gönderir (geçmiş ekranda aynen
// görünsün) ve yanına YAPISAL bir kapsam koyar:
//
//	context.scope   {services?, trace?, problem?, env?, team?, wiki?}
//	context.command "wiki" | "trace" | "rca" | "logs" | "help"
//
// Kural: yapısal kapsam/komut router sezgilerinden ÖNCE uygulanır.
//
//   - Kapsam ve komut YOKSA bu dosya hiçbir şey yapmaz — ctx, mesajlar, istek
//     bağlamı aynen; serbest metin yönlendirmesi bayt bayt eski
//     (chat_scope_test.go pinler).
//   - /help ve bilinmeyen komut: LLM'siz komut listesi.
//   - /wiki ya da @wiki: telemetri yönlendirmesi atlanır, doğrudan wiki
//     kademesi (LiveOnWeak); kapılar AYNEN (wiki kapalı / API token'ı → açık
//     mesaj, sessiz düşüş yok).
//   - @trace:<id> → trace_by_id (Explain çekirdeği, guidedTraceExplain);
//     @problem:<id> → problemin servisi + penceresi üzerinde kök neden.
//   - /trace, /rca, /logs → mevcut kılavuz rotalar (trace_search/slow_traces,
//     root_cause, log_field/log_errors).
//   - Servis/env/takım kapsamı canlı kataloglarla doğrulanır (guided router'ın
//     okuduğu AYNI 60 s'lik listeler); bilinmeyen değer → LLM'siz "şunu mu
//     demek istedin?" + çipler. Geçerli kapsam ctx'e konur ve runGuidedRoute
//     her rotaya uygular (applyChatScopeRoute): kapsam verilen boyutta
//     "Hangi servisi kastettin?" bir daha sorulmaz.
//
// Yetki: kapsam ERİŞİMİ GENİŞLETMEZ. Doğrulama listeleri serbest metnin de
// adlandırabildiği kataloglardır ve kapsam yalnız sorguyu DARALTIR (servis /
// env / takım süzgeci); her bundle kendi okuma yolunu, rol süzgecini ve
// limitlerini aynen kullanır. Wiki kapısı (oturum kullanıcısı, wiki açık)
// /wiki'de de aynı.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// chatScope — istemcinin yapısal kapsamı (context.scope).
type chatScope struct {
	Services []string `json:"services,omitempty"`
	Trace    string   `json:"trace,omitempty"`
	Problem  string   `json:"problem,omitempty"`
	Env      string   `json:"env,omitempty"`
	Team     string   `json:"team,omitempty"`
	Wiki     bool     `json:"wiki,omitempty"`
	// Shaped (v0.10.1139) — Services içinden YALNIZ biçimce eşleşen (servis
	// adı biçimli "@john.doe", "@spring-boot"; tamamlamadan SEÇİLMEMİŞ) adlar.
	// Bilinmeyen biçim-eşleşmesi sessizce düşer (sert "X adında servis yok"
	// cevabı yalnız seçilen adda ya da tek servisli /rca'da). Yoksa (eski
	// istemci) her servis açık seçim sayılır — v0.10.1138 davranışı.
	Shaped []string `json:"shaped,omitempty"`
}

// strictScope — SAF: tek servisli /rca'da ad biçimce yazılmış olsa bile
// operatör açıkça o servisi istedi → Shaped temizlenir (sert cevap kalır).
func strictScope(sc chatScope, cmd string) chatScope {
	if cmd == "rca" && len(sc.Services) == 1 {
		sc.Shaped = nil
	}
	return sc
}

func (sc *chatScope) isShaped(name string) bool {
	for _, v := range sc.Shaped {
		if strings.EqualFold(v, name) {
			return true
		}
	}
	return false
}

// chatScopeDrawerGate — SAF (v0.10.1139): çekmece bağlamı (explain / subject /
// ekrandaki trace / sayfanın traceId'si) varken @-kapsamı DÜŞER — trace ve
// exception çekmecesi takipleri kendi akışlarında kalır. Açık /komut bu kapıdan
// etkilenmez (istemci yalnız mesaj başındaki eğik çizgiden basar), aynen uygulanır.
func chatScopeDrawerGate(sc *chatScope, drawerCtx ...string) *chatScope {
	for _, v := range drawerCtx {
		if strings.TrimSpace(v) != "" {
			return nil
		}
	}
	return sc
}

// chatScopeMaxServices — tek mesajda en çok bu kadar servis anması işlenir.
const chatScopeMaxServices = 5

func (sc *chatScope) empty() bool {
	return sc == nil || (len(sc.Services) == 0 && sc.Trace == "" && sc.Problem == "" &&
		sc.Env == "" && sc.Team == "" && !sc.Wiki)
}

// chatCommandInfo — /help listesinin bir satırı.
type chatCommandInfo struct {
	Name, Usage, Hint string
}

// chatCommands — desteklenen eğik komutlar (istemci menüsü aynı listeyi
// taşır: frontend/src/components/ai/chatScope.ts CHAT_COMMANDS).
var chatCommands = []chatCommandInfo{
	{"wiki", "/wiki <soru>", "yalnız kurum wiki'sinde ara (telemetri yok)"},
	{"trace", "/trace <ifade>", "trace araması (servis için @servis ekle)"},
	{"rca", "/rca <servis ya da @problem:id>", "kök neden analizi"},
	{"logs", "/logs <ifade>", "log araması (alan=değer de olur)"},
	{"help", "/help", "bu liste"},
}

// normalizeChatCommand — SAF: "/RCA" → "rca". İkinci dönüş komutun bilinip
// bilinmediği; boş girdi ("", true) döner (komut yok).
func normalizeChatCommand(cmd string) (string, bool) {
	c := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cmd), "/"))
	if c == "" {
		return "", true
	}
	for _, k := range chatCommands {
		if k.Name == c {
			return c, true
		}
	}
	return c, false
}

// chatScopeActive — SAF: istek kapsam ya da komut taşıyor mu. false iken
// chatScopeTier dokunmaz (serbest metin bayt bayt eski yol).
func chatScopeActive(scope *chatScope, command string) bool {
	return strings.TrimSpace(command) != "" || !scope.empty()
}

var (
	chatCmdPrefixRe  = regexp.MustCompile(`^\s*/([A-Za-z]+)(?:\s+|$)`)
	chatKindMention  = regexp.MustCompile(`(^|\s)@(trace|problem|env|team):\S*`)
	chatWikiMention  = regexp.MustCompile(`(?i)(^|\s)@wiki\b`)
	chatNameMention  = regexp.MustCompile(`(^|\s)@([A-Za-z0-9][A-Za-z0-9._/-]*)`)
	chatMultiSpaceRe = regexp.MustCompile(`\s+`)
	chatProblemIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]{0,79}$`)
)

// cleanScopedQuestion — SAF: kapsam işaretlerini metinden ayıklar.
// routed: router'a giden soru (öndeki /komut ve @tür:değer/@wiki düşer,
// "@ad" çıplak "ad" olur — router adı serbest metindeki gibi görür).
// rest: bütün anmalar düşmüş hâli ("yalnız anma mı yazıldı?" kararı).
func cleanScopedQuestion(text string) (routed, rest string) {
	t := text
	if m := chatCmdPrefixRe.FindStringSubmatch(t); m != nil {
		if _, known := normalizeChatCommand(m[1]); known {
			t = t[len(m[0]):]
		}
	}
	t = chatKindMention.ReplaceAllString(t, "$1")
	t = chatWikiMention.ReplaceAllString(t, "$1")
	routed = chatNameMention.ReplaceAllString(t, "$1$2")
	rest = chatNameMention.ReplaceAllString(t, "$1")
	norm := func(s string) string { return strings.TrimSpace(chatMultiSpaceRe.ReplaceAllString(s, " ")) }
	return norm(routed), norm(rest)
}

// chatScopeIssue — doğrulanamayan kapsam boyutu (LLM'siz soru cevabı).
type chatScopeIssue struct {
	Dim, Value string
	Options    []string
}

// scopeNameMatch — SAF: harf kasası duyarsız tam eşleşme → kanonik ad.
func scopeNameMatch(v string, names []string) (string, bool) {
	for _, n := range names {
		if strings.EqualFold(n, v) {
			return n, true
		}
	}
	return "", false
}

// scopeNearNames — SAF: değeri (ya da değer onu) içeren en çok 5 ad, kısa önce.
func scopeNearNames(v string, names []string) []string {
	lv := strings.ToLower(v)
	var out []string
	for _, n := range names {
		ln := strings.ToLower(n)
		if lv != "" && (strings.Contains(ln, lv) || strings.Contains(lv, ln)) {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) < len(out[j]) })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// validateChatScope — SAF: kapsamı canlı kataloglara göre doğrular ve
// kanonik adlara çevirir. Katalog okunamadıysa (nil) o boyut olduğu gibi
// kabul edilir — router da o an kör; kapsam yalnız daraltır.
func validateChatScope(sc chatScope, svcNames, envNames, teamNames []string) (chatScope, *chatScopeIssue) {
	out := chatScope{Wiki: sc.Wiki}
	seen := map[string]bool{}
	for _, s := range sc.Services {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if svcNames != nil {
			c, ok := scopeNameMatch(s, svcNames)
			if !ok && sc.isShaped(s) {
				// v0.10.1139 — seçilmemiş biçim-eşleşmesi ("@john.doe") sessizce düşer.
				continue
			}
			if !ok {
				return out, &chatScopeIssue{Dim: "service", Value: s, Options: scopeNearNames(s, svcNames)}
			}
			s = c
		}
		if !seen[s] && len(out.Services) < chatScopeMaxServices {
			seen[s] = true
			out.Services = append(out.Services, s)
		}
	}
	if v := strings.TrimSpace(sc.Env); v != "" {
		if envNames != nil {
			c, ok := scopeNameMatch(v, envNames)
			if !ok {
				return out, &chatScopeIssue{Dim: "env", Value: v, Options: scopeNearNames(v, envNames)}
			}
			v = c
		}
		out.Env = v
	}
	if v := strings.TrimSpace(sc.Team); v != "" {
		if teamNames != nil {
			c, ok := scopeNameMatch(v, teamNames)
			if !ok {
				return out, &chatScopeIssue{Dim: "team", Value: v, Options: scopeNearNames(v, teamNames)}
			}
			v = c
		}
		out.Team = v
	}
	if v := strings.ToLower(strings.TrimSpace(sc.Trace)); v != "" {
		if extractTraceID(v) != v {
			return out, &chatScopeIssue{Dim: "trace", Value: sc.Trace}
		}
		out.Trace = v
	}
	if v := strings.TrimSpace(sc.Problem); v != "" {
		if !chatProblemIDRe.MatchString(v) {
			return out, &chatScopeIssue{Dim: "problem", Value: v}
		}
		out.Problem = v
	}
	return out, nil
}

// chatScopeIssueAnswer — SAF: doğrulanamayan kapsamın LLM'siz cevabı. Çipler
// AYNI soruyu düzeltilmiş anmayla yeniden kurar (istemci anmayı yine kapsama
// çevirir).
func chatScopeIssueAnswer(is chatScopeIssue, rest string) (string, []string) {
	label := map[string]string{"service": "servis", "env": "ortam", "team": "takım", "trace": "trace", "problem": "problem"}[is.Dim]
	switch is.Dim {
	case "trace":
		return fmt.Sprintf("`%s` geçerli bir trace kimliği değil (32 hex karakter bekleniyor). Kimliği yeniden yapıştırır mısın?", is.Value), nil
	case "problem":
		return fmt.Sprintf("`%s` geçerli bir problem kimliği değil.", is.Value), nil
	}
	if len(is.Options) == 0 {
		return fmt.Sprintf("`%s` adında bir %s bulamadım. Adı kontrol eder misin? (@ yazınca adaylar listelenir.)", is.Value, label), nil
	}
	prefix := map[string]string{"service": "@", "env": "@env:", "team": "@team:"}[is.Dim]
	chips := make([]string, 0, len(is.Options))
	for _, o := range is.Options {
		chips = append(chips, strings.TrimSpace(prefix+o+" "+rest))
	}
	return fmt.Sprintf("`%s` adında bir %s yok. Şunlardan birini mi kastettin: %s?", is.Value, label, strings.Join(is.Options, ", ")), chips
}

// chatHelpText — SAF: /help cevabı (LLM yok).
func chatHelpText(unknown string) string {
	var b strings.Builder
	if unknown != "" {
		fmt.Fprintf(&b, "`/%s` diye bir komut yok. ", unknown)
	}
	b.WriteString("Kullanılabilir komutlar (mesajın başına yaz):\n\n")
	for _, c := range chatCommands {
		fmt.Fprintf(&b, "- `%s` — %s\n", c.Usage, c.Hint)
	}
	b.WriteString("\nKapsam için @ kullan: `@servis`, `@trace:<id>`, `@problem:<id>`, `@env:<ad>`, `@team:<ad>`, `@wiki`.")
	return b.String()
}

// chatScopePlan — planChatScope'un kararı.
type chatScopePlan struct {
	Kind     string // "" (akış sürer) | "wiki" | "route" | "problem" | "ask"
	Route    guidedRoute
	Question string // anlatıma giden soru (boşsa rota için sentezlenir)
	Text     string // ask cevabı
}

// removeNameToken — SAF: metinden adı (harf kasası duyarsız, tam sözcük) çıkarır.
func removeNameToken(text, name string) string {
	if name == "" {
		return strings.TrimSpace(text)
	}
	var keep []string
	for _, f := range strings.Fields(text) {
		if strings.EqualFold(strings.Trim(f, ".,;:!?'\""), name) {
			continue
		}
		keep = append(keep, f)
	}
	return strings.Join(keep, " ")
}

// serviceScopedIntents — kapsamdaki servisin boş Service alanına yazılabildiği
// rotalar (filo geneli de koşabilen, servis süzgeci taşıyan paketler).
var serviceScopedIntents = map[guidedIntent]bool{
	guidedProblems: true, guidedServiceHealth: true, guidedSlowTraces: true, guidedDeployImpact: true,
	guidedLogErrors: true, guidedRootCause: true, guidedLogField: true, guidedTraceSearch: true,
	guidedPodHealth: true, guidedDBHealth: true, guidedMessagingHealth: true, guidedOpenPage: true,
}

// planChatScope — SAF: komut + doğrulanmış kapsam → ne yapılacak.
// routed/rest: cleanScopedQuestion çıktısı.
func planChatScope(cmd string, sc chatScope, routed, rest string, svcNames, envNames []string) chatScopePlan {
	if cmd == "wiki" || sc.Wiki {
		return chatScopePlan{Kind: "wiki", Question: rest}
	}
	if sc.Trace != "" {
		return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedTraceByID, TraceID: sc.Trace, Env: sc.Env},
			Question: orDefault(routed, "bu trace neden yavaş ya da hatalı?")}
	}
	if sc.Problem != "" {
		return chatScopePlan{Kind: "problem", Question: orDefault(routed, "bu problemin kök nedeni ne?")}
	}
	svc := ""
	if len(sc.Services) > 0 {
		svc = sc.Services[0]
	}
	norm := normalizeGuidedMsg(rest)
	textSvc := ""
	if svc == "" && rest != "" {
		textSvc = extractServiceEntity(norm, svcNames, envNames)
		svc = textSvc
	}
	// Komutun ifadesi: anmalar ve metinde adı geçen servis düşmüş metin.
	expr := removeNameToken(rest, textSvc)
	switch cmd {
	case "trace":
		if id := extractTraceID(strings.ToLower(rest)); id != "" {
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedTraceByID, TraceID: id, Env: sc.Env}, Question: orDefault(routed, "bu trace")}
		}
		if expr == "" {
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedSlowTraces, Service: svc, Env: sc.Env},
				Question: orDefault(routed, strings.TrimSpace(svc+" en yavaş trace'ler"))}
		}
		return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedTraceSearch, Service: svc, Env: sc.Env, SearchText: expr},
			Question: orDefault(routed, expr)}
	case "logs":
		if f, v, contains, ok := extractLogFieldQuery(expr, append(guidedTokens(normalizeGuidedMsg(expr)), "log")); ok {
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedLogField, Service: svc, Env: sc.Env, LogField: f, LogValue: v, LogContains: contains},
				Question: orDefault(routed, expr)}
		}
		if expr == "" {
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedLogErrors, Service: svc, Env: sc.Env},
				Question: orDefault(routed, strings.TrimSpace(svc+" log hataları"))}
		}
		return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedLogField, Service: svc, Env: sc.Env, LogField: "message", LogValue: expr, LogContains: true},
			Question: orDefault(routed, expr)}
	case "rca":
		if svc == "" {
			return chatScopePlan{Kind: "ask", Text: "Kök neden analizi için bir servis ya da problem belirt: `/rca @servis` ya da `/rca @problem:<id>`."}
		}
		return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedRootCause, Service: svc, Env: sc.Env},
			Question: orDefault(routed, svc+" kök nedeni ne?")}
	}
	// Yalnız anma ("@svc-orders", "@team:platform"): komutsuz, metinsiz.
	if rest == "" {
		switch {
		case len(sc.Services) > 1:
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedFamilyHealth, Family: sc.Services, Env: sc.Env},
				Question: orDefault(routed, strings.Join(sc.Services, ", ")+" sağlığı nasıl?")}
		case len(sc.Services) == 1:
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedServiceHealth, Service: svc, Env: sc.Env},
				Question: orDefault(routed, svc+" sağlığı nasıl?")}
		case sc.Team != "":
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedTeamServices, Team: sc.Team, Env: sc.Env},
				Question: sc.Team + " takımının servisleri nasıl?"}
		case sc.Env != "":
			return chatScopePlan{Kind: "route", Route: guidedRoute{Intent: guidedProblems, Env: sc.Env},
				Question: sc.Env + " ortamındaki açık problemler?"}
		}
	}
	return chatScopePlan{}
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// applyChatScopeRoute — SAF: ctx'teki kapsamı ÇÖZÜLMÜŞ bir rotaya uygular
// (runGuidedRoute girişi; guided, niyet ve takip yollarının ortak kapısı).
// nil kapsam → rota AYNEN (serbest metin bayt bayt eski).
func applyChatScopeRoute(sc *chatScope, r guidedRoute) guidedRoute {
	if sc.empty() {
		return r
	}
	if sc.Env != "" {
		r.Env = sc.Env
	}
	if len(sc.Services) > 0 {
		svc := sc.Services[0]
		switch {
		case r.Intent == guidedAskService:
			// Kapsam verilen boyutta netleştirme sorusu sorulmaz.
			next := r.AskIntent
			if next == "" || next == guidedAskService {
				next = guidedServiceHealth
			}
			r.Intent, r.AskIntent, r.ServiceOptions, r.DirectAnswer = next, "", nil, ""
			switch {
			case next == guidedPairRequests && r.PairMissing == "from":
				r.PairFrom, r.PairMissing = svc, ""
			case next == guidedPairRequests:
				r.PairTo, r.PairToKind, r.PairMissing = svc, "service", ""
			default:
				r.Service = svc
			}
		case r.Intent == guidedFindEntity && r.Service == "" && len(r.ServiceOptions) > 0:
			r.Service, r.ServiceOptions = svc, nil
		case r.Service == "" && serviceScopedIntents[r.Intent]:
			r.Service = svc
		}
	}
	if sc.Team != "" {
		switch r.Intent {
		case guidedMyServices:
			r = guidedRoute{Intent: guidedTeamServices, Team: sc.Team, Env: r.Env}
		case guidedTeamServices:
			r.Team, r.TeamOptions, r.DirectAnswer = sc.Team, nil, ""
		}
	}
	return r
}

type chatScopeCtxKey struct{}

func ctxWithChatScope(ctx context.Context, sc *chatScope) context.Context {
	return context.WithValue(ctx, chatScopeCtxKey{}, sc)
}

func chatScopeFromCtx(ctx context.Context) *chatScope {
	sc, _ := ctx.Value(chatScopeCtxKey{}).(*chatScope)
	return sc
}

// chatScopeLabel — SAF: şeffaflık çipi ("kapsam: servis svc-orders · env prod").
func chatScopeLabel(sc chatScope) string {
	var parts []string
	if len(sc.Services) > 0 {
		parts = append(parts, "servis "+strings.Join(sc.Services, ", "))
	}
	if sc.Team != "" {
		parts = append(parts, "takım "+sc.Team)
	}
	if sc.Env != "" {
		parts = append(parts, "env "+sc.Env)
	}
	if sc.Trace != "" {
		parts = append(parts, "trace "+sc.Trace)
	}
	if sc.Problem != "" {
		parts = append(parts, "problem "+sc.Problem)
	}
	if len(parts) == 0 {
		return ""
	}
	return "kapsam: " + strings.Join(parts, " · ")
}

// replaceLastUserText — son kullanıcı turunun metnini değiştirir (kopya dizi).
func replaceLastUserText(msgs []copilot.ChatMessage, text string) []copilot.ChatMessage {
	out := append([]copilot.ChatMessage(nil), msgs...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == "user" {
			out[i].Text = text
			break
		}
	}
	return out
}

// chatScopeProblemSeam — TEST DİKİŞİ (prod'da nil): problem okuması.
var chatScopeProblemSeam func(ctx context.Context, id string) (service string, startedNs int64, found bool)

func (s *Server) chatScopeProblem(ctx context.Context, id string) (string, int64, bool) {
	if chatScopeProblemSeam != nil {
		return chatScopeProblemSeam(ctx, id)
	}
	if s.store == nil {
		return "", 0, false
	}
	p, err := s.store.GetProblem(ctx, id)
	if err != nil || p == nil {
		return "", 0, false
	}
	return p.Service, p.StartedAt, true
}

// problemScopeWindow — SAF: problemin açıldığı ana çapalı 1 saatlik pencere
// (açılış + 15 dk'da biter; gelecekteyse şimdiye kırpılır).
func problemScopeWindow(startedNs int64, now time.Time) (anchor time.Time, rangeS int64) {
	if startedNs <= 0 {
		return now, 3600
	}
	a := time.Unix(0, startedNs).Add(15 * time.Minute)
	if a.After(now) {
		a = now
	}
	return a, 3600
}

// chatScopeReq — chatScopeTier'ın istekten okuduğu/yazdığı alanlar.
type chatScopeReq struct {
	Msgs      []copilot.ChatMessage
	Scope     *chatScope
	Command   string
	Service   string // ekran bağlamı; geçerli servis kapsamı EZER
	Env       string
	Operation string
	RangeS    int64
	Loc       *time.Location
	AnchorTo  time.Time
}

// chatScopeTier — yapısal kapsam/komut kademesi (copilotChat, wiki
// kademesinden ÖNCE). Kapsam ve komut yoksa dokunmadan döner. handled=false
// ise akış sürer; rq.Msgs / Service / Env güncellenmiş olabilir ve dönen
// ctx kapsamı taşır (runGuidedRoute uygular).
func (s *Server) chatScopeTier(ctx context.Context, emit func(string, any), rq *chatScopeReq) (context.Context, bool, bool) {
	if !chatScopeActive(rq.Scope, rq.Command) {
		return ctx, false, false
	}
	cmd, known := normalizeChatCommand(rq.Command)
	if !known {
		emit("answer", map[string]any{"text": chatHelpText(cmd)})
		return ctx, true, true
	}
	if cmd == "help" {
		// LLM YOK: liste sunucuda yazılı.
		emit("answer", map[string]any{"text": chatHelpText("")})
		return ctx, true, true
	}
	routed, rest := cleanScopedQuestion(lastUserText(rq.Msgs))
	var sc chatScope
	if rq.Scope != nil {
		sc = *rq.Scope
	}
	if cmd == "wiki" || sc.Wiki {
		if routed != "" {
			rq.Msgs = replaceLastUserText(rq.Msgs, routed)
		}
		ok := s.wikiForcedAnswer(ctx, emit, rq.Msgs, rest)
		return ctx, true, ok
	}
	var svcNames, envNames, teamNames []string
	if len(sc.Services) > 0 || cmd != "" {
		svcNames = s.guidedServiceNames(ctx)
	}
	if len(sc.Services) > 0 || sc.Env != "" || cmd != "" {
		envNames = s.guidedEnvNames(ctx)
	}
	if sc.Team != "" {
		teamNames = s.guidedTeamNames(ctx)
	}
	vsc, issue := validateChatScope(strictScope(sc, cmd), svcNames, envNames, teamNames)
	if issue != nil {
		text, chips := chatScopeIssueAnswer(*issue, rest)
		ans := map[string]any{"text": text}
		if len(chips) > 0 {
			ans["suggestions"] = chips
		}
		emit("answer", ans)
		return ctx, true, true
	}
	if cmd == "" && vsc.empty() {
		// v0.10.1139 — yalnız düşen biçim-eşleşmeleri vardı: serbest metin yolu bayt bayt eski.
		return ctx, false, false
	}
	if routed != "" {
		rq.Msgs = replaceLastUserText(rq.Msgs, routed)
	}
	if len(vsc.Services) > 0 {
		rq.Service = vsc.Services[0]
	}
	if vsc.Env != "" {
		rq.Env = vsc.Env
	}
	ctx = ctxWithChatScope(ctx, &vsc)
	if label := chatScopeLabel(vsc); label != "" {
		emitGuidedContextStep(emit, label)
	}
	plan := planChatScope(cmd, vsc, routed, rest, svcNames, envNames)
	rangeS, explicit := guidedRangeSExplicit(normalizeGuidedMsg(rest))
	if !explicit && rq.RangeS > 0 {
		rangeS = snapRangeS(rq.RangeS)
	}
	anchor := rq.AnchorTo
	switch plan.Kind {
	case "ask":
		emit("answer", map[string]any{"text": plan.Text})
		return ctx, true, true
	case "wiki":
		return ctx, true, s.wikiForcedAnswer(ctx, emit, rq.Msgs, plan.Question)
	case "problem":
		svc, started, found := s.chatScopeProblem(ctx, vsc.Problem)
		if !found {
			emit("answer", map[string]any{"text": fmt.Sprintf("`%s` kimlikli bir problem bulamadım (silinmiş ya da kimlik hatalı olabilir).", vsc.Problem)})
			return ctx, true, true
		}
		now := anchor
		if now.IsZero() {
			now = time.Now()
		}
		anchor, rangeS = problemScopeWindow(started, now)
		explicit = true
		emitGuidedContextStep(emit, "bağlam: problem "+vsc.Problem+prefixed(" · ", svc))
		if svc == "" {
			plan.Route = guidedRoute{Intent: guidedProblems, Env: vsc.Env}
		} else {
			plan.Route = guidedRoute{Intent: guidedRootCause, Service: svc, Env: vsc.Env}
		}
	case "route":
	default:
		return ctx, false, false
	}
	handled, ok := s.runGuidedRoute(ctx, emit, plan.Route, rangeS, plan.Question, rq.Msgs, "", rq.Service, rq.Operation, "", anchor, rq.Loc)
	if handled {
		s.noteChatContextRoute(ctx, plan.Route, rangeS, explicit)
	}
	return ctx, handled, ok
}

func prefixed(sep, v string) string {
	if v == "" {
		return ""
	}
	return sep + v
}

// wikiForcedAnswer — /wiki ve @wiki: telemetri yönlendirmesi ATLANIR, soru
// doğrudan wiki'de aranır (LiveOnWeak). Kapılar açık kademeyle aynı ama sessiz
// değil: wiki kapalı / API token'ı → açık mesaj (operatör AÇIKÇA wiki istedi).
func (s *Server) wikiForcedAnswer(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, question string) bool {
	question = strings.TrimSpace(question)
	answer := func(text string) bool {
		emit("answer", map[string]any{"text": text, "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
		return true
	}
	w := wikiKB()
	if w == nil || !w.Enabled() {
		return answer("Kurum wiki'si bu kurulumda bağlı değil ya da kapalı — `/wiki` kullanılamıyor. Yönetici Ayarlar → Wiki'den açabilir.")
	}
	if !sourceCodeCallerAllowed(auth.FromContext(ctx)) {
		return answer("Wiki araması API token'larıyla kullanılamaz; oturum açmış bir kullanıcı olarak sor.")
	}
	if question == "" {
		return answer("Wikide ne arayayım? Örnek: `/wiki svc-orders deploy runbook'u`.")
	}
	res, hits, err := wikiTierSearch(ctx, w, question, "", wiki.LiveOnWeak) // v0.10.1150 — Derin: geniş havuz
	noTerms := errors.Is(err, wiki.ErrNoTerms)
	if err != nil && !noTerms && ctx.Err() != nil {
		emit("error", map[string]string{"error": err.Error()})
		return false
	}
	emit("step", map[string]string{"label": "kurum wiki'si"})
	if len(hits) == 0 {
		emit("answer", map[string]any{"text": wikiNotFoundText(res, noTerms), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
			"sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
		return true
	}
	ans, used, err := s.wikiNarratedAnswer(ctx, w, question, wikiPriorTurns(msgs), hits, emit)
	if err != nil {
		emit("error", map[string]string{"error": err.Error()})
		return false
	}
	if text, _ := ans["text"].(string); !wikiDeclined(text) {
		s.rememberWikiAnswer(ctx, text, wikiPageRefs(used))
	}
	emit("answer", ans)
	return true
}
