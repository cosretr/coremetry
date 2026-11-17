package api

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chat_tool_scope.go — v0.10.1274 (CoSRE adım 5): EKRAN KAPSAMI ARAÇLARA.
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// v0.10.32'den beri serbest döngü ekranın env/servis/pencere bağlamını
// yalnız PROMPT METNİ olarak alıyordu (screenContextPreambleTR). Araçlar onu
// ancak model argümana KOPYALARSA görüyordu; küçük model çoğu zaman
// kopyalamıyor: Topbar "prod" derken list_anomalies filo geneline, 6
// saatlik ekranda query_logs 1 saate gidiyordu. Sayılar gerçek, kapsam yanlış.
//
// ── ÇÖZÜM ───────────────────────────────────────────────────────────────
//
// applyScreenContext — her yerli araç çağrısından ÖNCE, deterministik:
//
//   - env: aracın şemasında `env` var, model vermedi, ekran env'i SOMUT
//     (boş/"all" değil) → eklenir. Model FARKLI bir env verdiyse modelinki
//     kalır (operatör başka ortamı sormuş olabilir) ve künyeye yazılır.
//   - servis: araç `service`i ZORUNLU tutuyor, model vermedi ve operatör bir
//     SERVİS SAYFASINDA → eklenir (filo sayfasında servis tahmin edilmez).
//   - pencere: araç range_s ya da window (enum) alıyor, model hiçbir pencere
//     vermedi → ekran aralığı. Enum tam eşleşmiyorsa from_ms/to_ms (çıpalıysa
//     çıpaya, değilse şimdiye) — mutlak pencere ekranın kendisi.
//   - trace: query_logs/get_logs_for_trace, model trace_id ve servis vermedi,
//     ekranda seçili trace var ve (query_logs için) soru trace'i anıyor →
//     trace_id eklenir. trace_id taşıyan çağrıya env/pencere EKLENMEZ: kimlik
//     zaten kesin, ortam süzgeci yalnız eşleşmeyi düşürebilir.
//   - env-kör araç (mcptools.EnvBlindReason) + somut ekran env'i → sonuca
//     "env uygulanmadı: <sebep>" notu (annotateEnvBlind).
//   - brush (v0.10.1435): sayfa grafikte seçili bir aralık yayınladıysa
//     (PageContext.Brush, lib/pageContextOverlay.ts) VE soru o aralığı
//     işaret ediyorsa ("bu tepe", "bu sıçrama", "seçili", "bu aralık") pencere
//     ekranın aralığı değil BRUSH'ın kendisi (mutlak, çıpalı).
//
// Uygulanan her değer mcptools.ScopeApplied'e girer: step-result.ctxApplied
// ve kanıt çipi (ChatEvidence.CtxApplied). Dış MCP araçlarına DOKUNULMAZ
// (şema haritası yalnız yerli araçlardan kurulur).

// toolScreenScope — araç katmanına giden ekran kapsamı.
type toolScreenScope struct {
	Env        string // somut env; "" = yok
	Service    string // yalnız servis sayfasında dolu
	RangeS     int64
	AnchorTo   time.Time
	Anchored   bool
	TraceID    string
	TraceAsked bool // operatörün son mesajı trace'i anıyor mu
	// Brush — v0.10.1435: pencere operatörün grafikteki brush'ı (RangeS +
	// AnchorTo onun). Yalnız mutlak from_ms/to_ms alan araçlara iner; göreli
	// range_s şimdiye çapalanırdı (geçmiş bir tepeyi ıskalar) → enjekte edilmez.
	Brush bool
	// StateService — v0.10.2324 (chat_state.go): konuşma durumunun servisi (bu
	// turda açık anılan ya da devralınan). Ekran servisinden farklı olarak
	// `service` alanı olan HER yerli araca (zorunlu olmasa da) model vermediyse
	// konur — devam sorusu ("deploy mu vardı") önceki turun servisinde kalır.
	// Yalnız v2 + cosre_conversation.state; kapalıyken "".
	StateService string
}

// serviceDetailPages — servis enjeksiyonunun izinli olduğu sayfalar.
var serviceDetailPages = map[string]bool{"service": true, "service-backtrace": true}

var traceIDRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// concreteEnv — "" / "all" / "*" / "tümü" somut env DEĞİLDİR. Saf.
func concreteEnv(env string) string {
	e := strings.TrimSpace(env)
	switch strings.ToLower(e) {
	case "", "all", "*", "any", "tümü", "tüm ortamlar", "all environments":
		return ""
	}
	return e
}

// traceDeixis — soru ekrandaki trace'i anıyor mu ("bu trace", "trace'in
// logları", "this trace", trace id'nin kendisi). Saf.
func traceDeixis(text, traceID string) bool {
	t := strings.ToLower(text)
	if traceID != "" && strings.Contains(t, strings.ToLower(traceID)) {
		return true
	}
	return strings.Contains(t, "trace") || strings.Contains(t, "bu istek")
}

// buildToolScreenScope — istekten araç kapsamını kurar. Saf.
func buildToolScreenScope(sc ChatScreenContext, page, pinned *agentctx.PageContext, trace, lastUser string) toolScreenScope {
	eff := page
	if pinned != nil {
		eff = pinned
	}
	out := toolScreenScope{
		Env:      concreteEnv(sc.Env),
		RangeS:   sc.RangeS,
		AnchorTo: sc.AnchorTo,
		Anchored: sc.Anchored,
	}
	if eff != nil && serviceDetailPages[eff.Page] {
		out.Service = strings.TrimSpace(sc.Service)
	}
	tid := strings.TrimSpace(trace)
	if tid == "" && eff != nil {
		tid = strings.TrimSpace(eff.TraceID)
	}
	if traceIDRe.MatchString(tid) {
		out.TraceID = strings.ToLower(tid)
		out.TraceAsked = traceDeixis(lastUser, out.TraceID)
	}
	// v0.10.1435 — brush işaret ediliyorsa araç penceresi brush'ın kendisi.
	if eff != nil && eff.Brush != nil && eff.Brush.ToMs > eff.Brush.FromMs && eff.Brush.FromMs > 0 && brushDeixis(lastUser) {
		out.RangeS = max((eff.Brush.ToMs-eff.Brush.FromMs)/1000, 1)
		out.AnchorTo, out.Anchored, out.Brush = time.UnixMilli(eff.Brush.ToMs), true, true
	}
	return out
}

// brushDeixisRe — v0.10.1435: soru grafikteki seçili aralığı işaret ediyor mu
// ("bu tepe", "bu sıçrama", "seçili …", "bu aralık"; TR ekleri dahil, EN ikizi).
var brushDeixisRe = regexp.MustCompile(`(?i)\b(bu|şu)\s+(tepe|sıçrama|sicrama|spike|aralık|aralik|zirve)|seçili|secili|\bthis\s+(spike|peak|range)\b|\bselected\b`)

// brushDeixis — SAF.
func brushDeixis(text string) bool { return brushDeixisRe.MatchString(text) }

// traceInjectTools — trace_id enjeksiyonunun izinli olduğu araçlar.
// get_logs_for_trace doğası gereği trace'e bağlı (deixis gerekmez).
// v0.10.1275 — pivot_from_trace da trace'e bağlı (deixis gerekmez): ekranda
// seçili trace varsa pivot onu açar; servis verilmişse trace eklenmez.
var traceInjectTools = map[string]bool{"query_logs": true, "get_logs_for_trace": true, mcptools.PivotFromTraceName: true}

// serviceInjectTools — v0.10.1275: servisi ZORUNLU tutmayan ama servis
// sayfasında servise kapsanması gereken araçlar (pivot_from_trace: trace_id
// YA DA service ister; servis sayfasında trace yoksa en yavaş trace o servisten).
// v0.10.1284 — explain_log_spike: servis isteğe bağlı (boş = filo geneli), servis
// sayfasında o servisin log artışı sorulur.
var serviceInjectTools = map[string]bool{mcptools.PivotFromTraceName: true, mcptools.ExplainLogSpikeName: true}

// applyScreenContext — SAF. args JSON nesnesi değilse aynen döner.
func applyScreenContext(tool string, args json.RawMessage, schema map[string]any, sc toolScreenScope, now time.Time) (json.RawMessage, mcptools.ScopeApplied) {
	var applied mcptools.ScopeApplied
	if schema == nil {
		return args, applied
	}
	props, _ := schema["properties"].(map[string]any)
	m := map[string]any{}
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &m); err != nil || m == nil {
			return args, applied
		}
	}
	changed := false
	has := func(k string) bool { _, ok := props[k]; return ok }

	// trace_id önce: kimlik taşıyan çağrı diğer enjeksiyonları kapatır.
	if sc.TraceID != "" && traceInjectTools[tool] && has("trace_id") &&
		argStr(m, "trace_id") == "" && argStr(m, "service") == "" &&
		(tool == "get_logs_for_trace" || tool == mcptools.PivotFromTraceName || sc.TraceAsked) {
		m["trace_id"] = sc.TraceID
		applied.TraceID = sc.TraceID
		changed = true
	}
	hasTrace := argStr(m, "trace_id") != ""

	if sc.Env != "" && !hasTrace {
		if has("env") {
			switch got := argStr(m, "env"); {
			case got == "":
				m["env"] = sc.Env
				applied.Env = sc.Env
				changed = true
			case !strings.EqualFold(got, sc.Env):
				applied.EnvKept, applied.ScreenEnv = got, sc.Env
			}
		} else if r, ok := mcptools.EnvBlindReason(tool); ok {
			applied.EnvNotApplied = r
			applied.ScreenEnv = sc.Env
		}
	}

	if sc.Service != "" && !hasTrace && has("service") && (schemaRequires(schema, "service") || serviceInjectTools[tool]) {
		switch got := argStr(m, "service"); {
		case got == "":
			m["service"] = sc.Service
			applied.Service = sc.Service
			changed = true
		case got != sc.Service:
			applied.ServiceKept = got
		}
	}

	if sc.StateService != "" && !hasTrace && has("service") && argStr(m, "service") == "" {
		m["service"] = sc.StateService
		applied.Service = sc.StateService
		changed = true
	}

	if sc.Brush && !hasTrace && !argsHaveWindow(m) {
		// v0.10.1435 — brush: yalnız mutlak uçlar (enum / range_s yok).
		if has("from_ms") && has("to_ms") {
			m["from_ms"] = sc.AnchorTo.Add(-time.Duration(sc.RangeS) * time.Second).UnixMilli()
			m["to_ms"] = sc.AnchorTo.UnixMilli()
			applied.RangeS = int(sc.RangeS)
			changed = true
		}
	} else if sc.RangeS > 0 && !hasTrace && !argsHaveWindow(m) {
		switch {
		case has("range_s"):
			r := clampSchemaInt(props["range_s"], int(sc.RangeS))
			m["range_s"] = r
			applied.RangeS = r
			changed = true
		case has("window"):
			winSet := false
			if w, ok := windowEnumFor(props["window"], sc.RangeS); ok {
				m["window"] = w
				winSet = true
			} else if has("from_ms") && has("to_ms") {
				to := now
				if sc.Anchored && !sc.AnchorTo.IsZero() {
					to = sc.AnchorTo
				}
				m["from_ms"] = to.Add(-time.Duration(sc.RangeS) * time.Second).UnixMilli()
				m["to_ms"] = to.UnixMilli()
				winSet = true
			}
			if winSet {
				applied.RangeS = int(sc.RangeS)
				changed = true
			}
		}
	}

	if !changed {
		return args, applied
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args, mcptools.ScopeApplied{}
	}
	return b, applied
}

// argStr — argümanın dize değeri (yoksa / dize değilse ""). Saf.
func argStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// argsHaveWindow — model herhangi bir pencere verdi mi (0 / boş sayılmaz).
func argsHaveWindow(m map[string]any) bool {
	for _, k := range []string{"range_s", "window", "from_ms", "to_ms", "from_iso", "to_iso"} {
		switch v := m[k].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return true
			}
		case float64:
			if v != 0 {
				return true
			}
		case nil:
		default:
			return true
		}
	}
	return false
}

func schemaRequires(schema map[string]any, k string) bool {
	switch req := schema["required"].(type) {
	case []string:
		for _, r := range req {
			if r == k {
				return true
			}
		}
	case []any:
		for _, r := range req {
			if r == k {
				return true
			}
		}
	}
	return false
}

// clampSchemaInt — şemanın minimum/maximum'una sığdırır.
func clampSchemaInt(prop any, v int) int {
	p, _ := prop.(map[string]any)
	if mx, ok := schemaNum(p["maximum"]); ok && v > mx {
		v = mx
	}
	if mn, ok := schemaNum(p["minimum"]); ok && v < mn {
		v = mn
	}
	return v
}

func schemaNum(v any) (int, bool) {
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

// windowEnumFor — pencere enum'unda ekran aralığına TAM eşit değer. Saf.
func windowEnumFor(prop any, rangeS int64) (string, bool) {
	p, _ := prop.(map[string]any)
	var vals []string
	switch e := p["enum"].(type) {
	case []string:
		vals = e
	case []any:
		for _, v := range e {
			if s, ok := v.(string); ok {
				vals = append(vals, s)
			}
		}
	}
	for _, v := range vals {
		if d, ok := parseWindowLabel(v); ok && int64(d.Seconds()) == rangeS {
			return v, true
		}
	}
	return "", false
}

// parseWindowLabel — "15m" / "1h" / "7d" → süre.
func parseWindowLabel(s string) (time.Duration, bool) {
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, false
	}
	switch s[len(s)-1] {
	case 'm':
		return time.Duration(n) * time.Minute, true
	case 'h':
		return time.Duration(n) * time.Hour, true
	case 'd':
		return time.Duration(n) * 24 * time.Hour, true
	}
	return 0, false
}

// annotateEnvBlind — env-kör aracın başarılı JSON nesnesi sonucunun BAŞINA
// "env_note" ekler (önizleme ve model kırpması baştan okur). Nesne değilse
// aynen. Saf.
func annotateEnvBlind(content, reason, screenEnv string) string {
	t := strings.TrimSpace(content)
	if reason == "" || !strings.HasPrefix(t, "{") || !strings.HasSuffix(t, "}") {
		return content
	}
	note, _ := json.Marshal("env uygulanmadı: " + reason + " (ekran env=" + screenEnv + "; sonuç bu ortama DARALTILMADI)")
	rest := strings.TrimSpace(t[1:])
	if rest == "}" {
		return `{"env_note":` + string(note) + `}`
	}
	return `{"env_note":` + string(note) + `,` + rest
}
