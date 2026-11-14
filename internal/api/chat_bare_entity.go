package api

// chat_bare_entity.go — v0.10.2139 (CoSRE router boşlukları D6,
// docs/cosre/router-gaps-audit.md D + G). YALNIZ v2 (cosreV2Marked); v1 bu
// dosyanın hiçbir yardımcısını çağırmaz → bayt bayt eski.
//
// D (çıplak varlık): "ordrs-api" gibi yazım hatalı tek ad katalog taramasında
// (ResolveEntityText) aday bulamayınca guided düşüyor, sınıflandırıcı none
// diyordu; "# svc-a-prod" (# + ortam eki) da servise oturmuyordu. Şimdi:
//   - "#" atılır, "<servis>-<canlı ortam>" ayrılır → service_health (+ Env);
//   - tarama adaysızsa bulanık en yakın ≤3 servis (NearNames) → kısa soru +
//     data-question kartı (kind entity_pick; tık = servis adı yeni mesaj, o da
//     find_entity'de tek servise çözülür). Aday yoksa eski yol. SALT SAYI
//     ("73066") eşleşme kurmaz (v0.10.2112 dersi) — eski yol.
//   - tarama birden çok aday bulduysa (bugünkü düz metin liste) ilk ≤3 aday
//     aynı kartla da sunulur.
// G (yapıştırılan gövde): satır başındaki kod çiti ("``` Caused by: …",
// "```textX Params: {…}") şekil dedektöründen ÖNCE soyulur; "<Ad> Params: {"
// JSON parametre dökümü zayıf şekil → ilk tanımlayıcıyla log_field.

import (
	"context"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// questionKindEntityPick — data-question türü (uistream.QuestionKinds).
// Bekleyen soru YAZILMAZ: seçenek etiketi kendi başına yönlenen cümledir.
const questionKindEntityPick = "entity_pick"

// entityPickMax — kart seçenek tavanı (uistream.QuestionMaxOptions).
const entityPickMax = 3

// bareEntityNearQuery — SAF: bulanık eşleşmeye uygun çıplak sorgu: 1–2 ad
// jetonu, salt sayı değil, ≥3 karakter.
func bareEntityNearQuery(q string) (string, bool) {
	q = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(q), "#"))
	toks := guidedTokens(normalizeGuidedMsg(q))
	if len(toks) == 0 || len(toks) > 2 || len(q) < 3 {
		return "", false
	}
	digits := true
	for _, r := range strings.ReplaceAll(q, " ", "") {
		if r < '0' || r > '9' {
			digits = false
			break
		}
	}
	if digits {
		return "", false
	}
	return strings.ToLower(q), true
}

// v2BareServiceEnv — SAF: "# svc-a-prod" / "svc-a-prod" / "#svc-a" → (servis,
// ortam). Mesaj tek jeton (önündeki # hariç) olmalı; ortam eki yalnız canlı
// ortam adıysa ayrılır. Tam servis adı ortam ekiyle bitiyorsa (servis adı
// "svc-a-prod" canlıysa) ad olduğu gibi kalır.
func v2BareServiceEnv(raw string, services, envs []string) (svc, env string, ok bool) {
	q := strings.TrimSpace(raw)
	hashed := strings.HasPrefix(q, "#")
	q = strings.ToLower(strings.TrimSpace(strings.TrimLeft(q, "#")))
	if q == "" || strings.ContainsAny(q, " \t\n") {
		return "", "", false
	}
	live := map[string]string{}
	for _, s := range services {
		live[strings.ToLower(s)] = s
	}
	if s, hit := live[q]; hit {
		return s, "", hashed // çıplak tam ad zaten find_entity'de çözülür; yalnız # biçimi buraya
	}
	for _, e := range envs {
		le := strings.ToLower(e)
		if le == "" || !strings.HasSuffix(q, "-"+le) {
			continue
		}
		if s, hit := live[strings.TrimSuffix(q, "-"+le)]; hit {
			return s, e, true
		}
	}
	return "", "", false
}

// entityPickQuestion — SAF: kart yükü (≤3 seçenek; id = etiket = servis adı).
func entityPickQuestion(text string, names []string) (questionPayload, bool) {
	var opts []questionOption
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		opts = append(opts, questionOption{ID: n, Label: n})
		if len(opts) == entityPickMax {
			break
		}
	}
	if text == "" || len(opts) == 0 {
		return questionPayload{}, false
	}
	return questionPayload{Kind: questionKindEntityPick, Text: text, Options: opts, AllowFree: true}, true
}

// entityNearAskTR — SAF: düz metin soru (kartın Text'i ile aynı cümle).
func entityNearAskTR(q string, near []string) string {
	if len(near) == 1 {
		return "“" + clipRunes(q, 60) + "” adında bir servis bulamadım; " + near[0] + " servisini mi kastettiniz?"
	}
	return "“" + clipRunes(q, 60) + "” adında bir servis bulamadım; şunlardan birini mi kastettiniz?"
}

// v2EntityNearAsk — tarama adaysızken bulanık en yakın servisler. false →
// eski yol (aday yok / v1 / sorgu uygun değil).
func (s *Server) v2EntityNearAsk(ctx context.Context, emit func(string, any), raw string) bool {
	if !cosreV2Marked(ctx) {
		return false
	}
	q, ok := bareEntityNearQuery(raw)
	if !ok {
		return false
	}
	near := mcptools.NearNames(q, s.guidedServiceNames(ctx), entityPickMax)
	if len(near) == 0 {
		return false
	}
	text := entityNearAskTR(q, near)
	chatExchangeFrom(ctx).noteRoute(string(guidedAskService))
	emit("answer", map[string]any{"text": text, "suggestions": near})
	if qp, ok := entityPickQuestion(text, near); ok {
		emit(chatQuestionEventKind, qp)
	}
	return true
}

// v2EntityCandidatesCard — v2: tarama birden çok aday bulduğunda düz metin
// listenin altına ilk ≤3 servis/namespace adayının kartı.
func v2EntityCandidatesCard(ctx context.Context, emit func(string, any), text string, chips []string) {
	if !cosreV2Marked(ctx) {
		return
	}
	if qp, ok := entityPickQuestion(text, chips); ok {
		emit(chatQuestionEventKind, qp)
	}
}

// ── G: yapıştırılan gövde ───────────────────────────────────────────────

// pasteFenceLangs — çitin dil etiketi (yapışık yazılabilir: "```textNative…").
var pasteFenceLangs = []string{"plaintext", "console", "python", "kotlin", "shell", "text", "java", "json", "yaml", "bash", "html", "log", "txt", "xml", "sql", "csharp", "go", "js", "ts", "sh", "cs"}

// stripPasteFences — SAF: satır başı/sonu kod çitlerini (```, ```lang) soyar;
// dil etiketi ardından boşluk, satır sonu ya da BÜYÜK harf gelirse etiket
// sayılır ("```textNativeShellDispatch" → "NativeShellDispatch"). Çit yoksa
// metin AYNEN.
func stripPasteFences(raw string) string {
	if !strings.Contains(raw, "```") {
		return raw
	}
	lines := strings.Split(raw, "\n")
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			t = strings.TrimPrefix(t, "```")
			for _, lang := range pasteFenceLangs {
				if strings.HasPrefix(t, lang) {
					rest := t[len(lang):]
					if rest == "" || rest[0] == ' ' || (rest[0] >= 'A' && rest[0] <= 'Z') {
						t = rest
						break
					}
				}
			}
		}
		t = strings.TrimSuffix(strings.TrimSpace(t), "```")
		lines[i] = strings.TrimSpace(t)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// pastedParamsDumpRe — "<Tanımlayıcı> Params: {…" JSON parametre dökümü
// (satır başında; tanımlayıcı ≥3, harfle başlar).
var pastedParamsDumpRe = regexp.MustCompile(`(?m)^\s*([A-Za-z_][\w.$]{2,})\s+Params:\s*\{`)

// v2PastedParamsRoute — SAF: zayıf şekil (JSON parametre dökümü) → log_field
// (message alanında ilk tanımlayıcı). Soru cümlesi değil, tanımlayıcı servis
// adı değil.
func v2PastedParamsRoute(raw string, services []string) (guidedRoute, bool) {
	m := pastedParamsDumpRe.FindStringSubmatch(raw)
	if m == nil || pastedIsQuestion(raw) {
		return guidedRoute{}, false
	}
	term := m[1]
	if n := len([]rune(term)); n < 3 || n > 256 {
		return guidedRoute{}, false
	}
	return guidedRoute{Intent: guidedLogField, Service: extractServiceFullName(normalizeGuidedMsg(raw), services),
		LogField: "message", LogValue: term, LogContains: true}, true
}

// v2PastedQuestion — SAF: v2'de router'a giden yapıştırma metni: çit soyulmuş
// hâli şekil dedektörünü tutuyorsa o; değilse metin aynen.
func v2PastedQuestion(question string, v2 bool) (string, bool) {
	if !v2 {
		return question, false
	}
	if q := stripPasteFences(question); q != question && looksLikePastedError(q) {
		return q, true
	}
	return question, false
}
