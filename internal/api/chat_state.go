package api

// chat_state.go — v0.10.2324 (conversation-audit §6.0 "konuşma durumu = kapsam
// + kanıt defteri, tek nesne"; §6.1 A; §7 kararları 2, 13, 14): CoSRE v2
// konuşma durumu.
//
// Nesne: ChatContext GENİŞLER (aynı Redis anahtarı, yeni alanlar omitempty —
// v1 hiç yazmaz, JSON bayt bayt eski). Kapsam: servis(ler), ortam, cluster,
// pencere {rangeS, toMs, etiket, açık mı}, kanal kodu, fonksiyon kodu,
// Problem, trace, operasyon, hitap, `offer`. Defter: son turun en çok 5
// olgusu {araç, kapsam, ad, değer, birim, pencere} — "kanıtı turlar arası
// taşı" ile aynı nesne; ikinci bir yapı yok.
//
// Kapı: YALNIZ v2 (cosreV2Marked) VE cosre_conversation.state açık. Kapalıyken
// hiçbir kanca koşmaz → v2 bu dilimden önceki hâliyle bayt bayt.
//
// Akış (copilot_chat.go):
//  1. beginChatStateTurn — kademelerden ÖNCE: (Redis yoksa istemcinin geri
//     yolladığı durum doğrulanıp alınır) → çözücü (chat_state_resolve.go) →
//     isteğin ekran alanlarına (service / env / rangeS / toMs) devralınan ya
//     da sorunun açık kapsamı yazılır. Sıra Ek A kuralıdır: açık ifade >
//     sohbet durumu > ekran. Bütün kademeler (guided, niyet, döngü) aynı
//     alanları zaten okuyor; araç argümanlarına ekran kapsamı enjeksiyonu
//     (chat_tool_scope.go) üstünden iner — ikinci bir mekanizma yok.
//  2. Döngü araç argümanından ve sonucundan durumu KENDİSİ yazar (noteTool);
//     modelin set_context çağırmasına bağlı değil.
//  3. Cevap çerçevesinden öneri (offer) okunur; "done"dan önce durum
//     kesinleşir (finalize), flush aynı defer'de yazar.
//
// Redis yokken (karar 2): istemci son durumu `context.state` ile geri
// yollar; alanlar canlı kataloglara doğrulanır (servis, ortam), biçimler
// kapalı kümedir; defter ASLA istemciden alınmaz → istemcinin yankıladığı
// durum hiçbir zaman doğrulayıcı (E) kanıtı olmaz (fromClient işareti).

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// chatOffer — sunucunun/asistanın önerdiği sonraki adım. Kind kapalı küme
// (offerKinds); istem metni SUNUCUDA kind + servisten kurulur (istemci yankısı
// metin taşıyamaz).
type chatOffer struct {
	Kind    string `json:"kind"`
	Service string `json:"service,omitempty"`
	Label   string `json:"label,omitempty"`
	At      int64  `json:"at"` // unix ms
}

// chatFact — kanıt defterinin bir satırı.
type chatFact struct {
	Tool   string  `json:"tool"`
	Scope  string  `json:"scope,omitempty"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit,omitempty"`
	Window string  `json:"window,omitempty"`
}

const chatLedgerMax = 5

// offerKinds — kind → etiket kökü. Yeni tür = yeni satır + offerPrompt dalı.
var offerKinds = map[string]string{
	"logs":    "logları",
	"deploys": "deploy'ları",
	"traces":  "yavaş trace'leri",
	"pods":    "pod'ları",
	"errors":  "hataları",
}

// offerPrompt — SAF: kabul edilen önerinin yürütülecek soru metni.
func offerPrompt(o chatOffer) string {
	svc := strings.TrimSpace(o.Service)
	pre := ""
	if svc != "" {
		pre = svc + " "
	}
	switch o.Kind {
	case "logs":
		return pre + "loglarına bak"
	case "deploys":
		return pre + "için deploy var mı"
	case "traces":
		return pre + "yavaş trace'lerini göster"
	case "pods":
		return pre + "pod'larına bak"
	case "errors":
		return pre + "hatalarına bak"
	}
	return ""
}

// offerCueRe — cevabın son kısmında öneri dili; offerKindRes — öneri türü.
var offerCueRe = regexp.MustCompile(`(?i)(istersen|isterseniz|ister misin|ister misiniz|bakayım mı|bakalım mı|bakabilirim|bakabiliriz|sonraki adım|bir sonraki adım|bakmamı ister)`)

var offerKindRes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"logs", regexp.MustCompile(`(?i)\blog`)},
	{"deploys", regexp.MustCompile(`(?i)\b(deploy|rollout|sürüm)`)},
	{"traces", regexp.MustCompile(`(?i)\btrace`)},
	{"pods", regexp.MustCompile(`(?i)\bpod`)},
	{"errors", regexp.MustCompile(`(?i)\b(hata|exception)`)},
}

// detectOffer — SAF: cevabın SON paragrafında öneri dili + TEK öneri türü.
// Birden çok tür (belirsiz) ya da öneri dili yoksa nil — "bak" tahmin etmez.
func detectOffer(answer, service string, now time.Time) *chatOffer {
	t := normalizeGuidedMsg(strings.TrimSpace(answer)) // "İstersen": (?i) İ'yi katlamaz
	if i := strings.LastIndex(t, "\n\n"); i >= 0 {
		t = t[i+2:]
	}
	if r := []rune(t); len(r) > 400 {
		t = string(r[len(r)-400:])
	}
	loc := offerCueRe.FindStringIndex(t)
	if loc == nil {
		return nil
	}
	tail := t[loc[0]:]
	kind := ""
	for _, k := range offerKindRes {
		if k.re.MatchString(tail) {
			if kind != "" {
				return nil
			}
			kind = k.kind
		}
	}
	if kind == "" {
		return nil
	}
	label := offerKinds[kind]
	if service != "" {
		label = service + " " + label
	}
	return &chatOffer{Kind: kind, Service: service, Label: label, At: now.UnixMilli()}
}

// chatStateOn — dilim kapısı: v2 VE cosre_conversation.state.
func chatStateOn(ctx context.Context) bool {
	return cosreV2Marked(ctx) && chstore.CurrentCosreConversation().State
}

// chatStateNow — test dikişi (üretimde time.Now).
var chatStateNow = time.Now

// chatStateRedisless — sohbet durumu Redis'siz mi (no-op önbellek). Test
// dikişi: store'suz test sunucusu guided kataloğunu önbellekten okur.
var chatStateRedisless = func(s *Server) bool { return cache.IsNoop(s.cache) }

// chatStateTurn — bir turun durum işlemi. nil = kapalı (her metot nil-güvenli).
type chatStateTurn struct {
	st        *chatContextState
	res       stateResolveOut
	svcNames  []string
	loc       *time.Location
	facts     []chatFact
	offer     *chatOffer
	answered  bool
	finalized bool
	// assumedSvc / assumedWin — v0.10.2332 (B): bu turda önceki turdan
	// devralınıp isteğe UYGULANAN servis ve pencere etiketi (varsay ve söyle).
	assumedSvc, assumedWin string
}

// beginChatStateTurn — çözücüyü koşar ve isteği kapsamla günceller. Kapalıyken nil.
func (s *Server) beginChatStateTurn(ctx context.Context, req *chatRequest, st *chatContextState, emit func(string, any)) *chatStateTurn {
	if st == nil || !chatStateOn(ctx) || len(req.Messages) == 0 {
		return nil
	}
	now := chatStateNow()
	loc := chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin)
	svcs, envs := s.guidedServiceNames(ctx), s.guidedEnvNames(ctx)
	if req.Context.State != nil && chatStateRedisless(s) && st.ctx.UpdatedAt == 0 && st.ctx.Service == "" && st.ctx.RangeS == 0 {
		if echo, ok := validateStateEcho(*req.Context.State, svcs, envs, now); ok {
			st.ctx, st.fromClient = mergeStateEcho(st.ctx, echo), true
		}
	}
	if p := req.Context.StatePatch; p != nil { // v0.10.2325 — istemcinin alan değişikliği (doğrulanır)
		if next, ok := applyStatePatch(st.ctx, *p, svcs, envs, now); ok && !chatContextEqual(st.ctx, next) {
			st.ctx, st.dirty = next, true
		}
	}
	stt := &chatStateTurn{st: st, svcNames: svcs, loc: loc}
	last := len(req.Messages) - 1
	res := resolveChatState(stateResolveIn{Question: req.Messages[last].Text, Prev: st.ctx, Now: now, Loc: loc, Services: svcs, Envs: envs,
		HasPrior: chatUserTurns(req.Messages) > 1})
	stt.res = res
	if !chatContextEqual(st.ctx, res.Next) {
		st.ctx, st.dirty = res.Next, true
	}
	c := st.ctx
	if res.Offer != nil {
		if p := offerPrompt(*res.Offer); p != "" {
			req.Messages[last].Text = p
			emit("step", map[string]string{"label": "öneri yürütülüyor: " + res.Offer.Label})
		}
	}
	var inherited []string
	if res.InheritService && !screenDeixis(req.Messages[last].Text) && req.Context.Service != c.Service {
		req.Context.Service = c.Service
		inherited = append(inherited, c.Service)
		stt.assumedSvc = c.Service // v0.10.2332
	}
	if res.EnvSet || (res.InheritEnv && concreteEnv(req.Context.Env) != c.Env) {
		req.Context.Env = c.Env
		if res.InheritEnv {
			inherited = append(inherited, c.Env)
		}
	}
	if res.WindowSet || res.InheritWindow {
		req.Context.RangeS, req.Context.ToMs = c.RangeS, c.WinToMs
		if res.InheritWindow {
			inherited = append(inherited, stateWindowLabel(c))
			stt.assumedWin = assumedWindowPhrase(c) // v0.10.2332
		}
	}
	if len(inherited) > 0 {
		emit("step", map[string]string{"label": "bağlam (önceki turdan): " + strings.Join(inherited, " · ")})
	}
	if res.WindowSet {
		emit("step", map[string]string{"label": "pencere: " + stateWindowLabel(c)})
	}
	return stt
}

// screenDeixis — soru ekrandakini işaret ediyor ("bu servis", "bu sayfa",
// "burada", "ekrandaki"): ekran servisi sohbet durumunu ezer.
func screenDeixis(q string) bool {
	n := normalizeGuidedMsg(q)
	for _, p := range []string{"bu servis", "bu sayfa", "burada", "ekrandaki", "bu ekran"} {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// toolService — döngünün araç kapsamına inen durum servisi ("" = yok):
// bu turda açık anılan ya da devralınan servis.
func (t *chatStateTurn) toolService() string {
	if t == nil || !(t.res.ServiceSet || t.res.InheritService) {
		return ""
	}
	return t.st.ctx.Service
}

// noteTool — döngünün başarılı araç çağrısı: argümandan kapsam (boş alana),
// sonuçtan defter olguları.
func (t *chatStateTurn) noteTool(name string, args json.RawMessage, content string, isErr bool) {
	if t == nil || isErr {
		return
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return
	}
	c := t.st.ctx
	if v := argStr(m, "service"); v != "" && c.Service == "" && containsFold(t.svcNames, v) {
		c.Service = v
	}
	if v := concreteEnv(argStr(m, "env")); v != "" && c.Env == "" {
		c.Env = v
	}
	if v := argStr(m, "trace_id"); v != "" {
		c = withContextTrace(c, v)
	}
	if v := argStr(m, "problem_id"); v != "" && chatProblemIDRe.MatchString(v) {
		c.Problem = v
	}
	if f, ok := m["range_s"].(float64); ok && f >= 60 && c.RangeS == 0 {
		c.RangeS = int64(f)
		c.WinLabel = fmtRangeTR(c.RangeS)
	}
	if !chatContextEqual(t.st.ctx, c) {
		t.st.ctx, t.st.dirty = c, true
	}
	scope := strings.Trim(argStr(m, "service")+" "+concreteEnv(argStr(m, "env")), " ")
	for _, f := range ledgerFacts(name, scope, stateWindowLabel(c), content) {
		if len(t.facts) >= chatLedgerMax {
			break
		}
		t.facts = append(t.facts, f)
	}
}

// noteFacts — v0.10.2327 (E1): kademenin sunucu olguları (guided özet sayıları)
// deftere; döngü olgularıyla aynı tavan.
func (t *chatStateTurn) noteFacts(facts []chatFact) {
	if t == nil {
		return
	}
	for _, f := range facts {
		if len(t.facts) >= chatLedgerMax {
			break
		}
		t.facts = append(t.facts, f)
	}
}

type chatStateTurnKey struct{}

// withChatStateTurn — v0.10.2327: turun durum işlemi ctx'e (guided kademesi defter yazar); nil → ctx aynen.
func withChatStateTurn(ctx context.Context, t *chatStateTurn) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, chatStateTurnKey{}, t)
}

// stateTurnFrom — ctx'teki durum işlemi (kapalıyken nil; yöntemler nil-güvenli).
func stateTurnFrom(ctx context.Context) *chatStateTurn {
	t, _ := ctx.Value(chatStateTurnKey{}).(*chatStateTurn)
	return t
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// ledgerFactKeys — sayısal olgu adları (son ek eşleşmesi).
var ledgerFactKeys = []string{"p50_ms", "p90_ms", "p95_ms", "p99_ms", "avg_ms", "latency_ms", "duration_ms",
	"error_rate", "error_pct", "err_pct", "error_count", "errors", "rps", "request_count", "requests", "count", "open_problems"}

// ledgerFacts — SAF: araç JSON çıktısının üst ve bir alt düzeyindeki bilinen
// sayısal alanlar (sıra: anahtar adına göre deterministik).
func ledgerFacts(tool, scope, window, content string) []chatFact {
	var root map[string]any
	if json.Unmarshal([]byte(content), &root) != nil {
		return nil
	}
	var out []chatFact
	var walk func(m map[string]any, prefix string, depth int)
	walk = func(m map[string]any, prefix string, depth int) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(out) >= chatLedgerMax {
				return
			}
			switch v := m[k].(type) {
			case float64:
				if ledgerKey(k) {
					out = append(out, chatFact{Tool: tool, Scope: scope, Name: prefix + k, Value: v, Unit: ledgerUnit(k), Window: window})
				}
			case map[string]any:
				if depth == 0 {
					walk(v, k+".", 1)
				}
			}
		}
	}
	walk(root, "", 0)
	return out
}

func ledgerKey(k string) bool {
	lk := strings.ToLower(k)
	for _, s := range ledgerFactKeys {
		if lk == s || strings.HasSuffix(lk, "_"+s) {
			return true
		}
	}
	return false
}

func ledgerUnit(k string) string {
	lk := strings.ToLower(k)
	switch {
	case strings.HasSuffix(lk, "_ms"):
		return "ms"
	case strings.HasSuffix(lk, "_pct"):
		return "%"
	case strings.HasSuffix(lk, "rps"):
		return "req/s"
	}
	return ""
}

// wrapEmit — "answer" çerçevesinden öneri okunur; "done"dan önce durum kesinleşir.
func (t *chatStateTurn) wrapEmit(emit func(string, any)) func(string, any) {
	if t == nil {
		return emit
	}
	return func(kind string, payload any) {
		switch kind {
		case "answer":
			t.answered = true
			if text := stateAnswerText(payload); text != "" {
				t.offer = detectOffer(text, t.st.ctx.Service, chatStateNow())
			}
		case "done":
			t.finalize()
			// v0.10.2325 — data-state: turun çözülmüş kapsamı (defter değerleri yok), "done"dan hemen önce.
			if w := chatStateWireOf(t.st.ctx, chatStateNow()); !stateWireEmpty(w) {
				emit(chatStateEventKind, w)
			}
		}
		emit(kind, payload)
	}
}

func stateAnswerText(payload any) string {
	switch p := payload.(type) {
	case map[string]any:
		s, _ := p["text"].(string)
		return s
	case map[string]string:
		return p["text"]
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	s, _ := m["text"].(string)
	return s
}

// finalize — turun sonu (bir kez): çözücünün açık / devralınan penceresi
// kademe yazımlarına karşı korunur (guided rotası kaba basamağı yazar), öneri
// ve defter son turun olur.
func (t *chatStateTurn) finalize() {
	if t == nil || t.finalized {
		return
	}
	t.finalized = true
	c := t.st.ctx
	if t.res.WindowSet || t.res.InheritWindow {
		w := t.res.Next
		c.RangeS, c.WinToMs, c.WinLabel, c.RangeExplicit = w.RangeS, w.WinToMs, w.WinLabel, true
	}
	if t.answered {
		c.Offer = t.offer
	}
	c.Ledger = append([]chatFact(nil), t.facts...)
	if len(c.Ledger) == 0 {
		c.Ledger = nil
	}
	if !chatContextEqual(t.st.ctx, c) {
		t.st.ctx, t.st.dirty = c, true
	}
}

// stateWindowLabel — SAF: durum penceresinin insan etiketi.
func stateWindowLabel(c ChatContext) string {
	if c.WinLabel != "" {
		return c.WinLabel
	}
	if c.RangeS > 0 {
		return fmtRangeTR(c.RangeS)
	}
	return ""
}

// chatStateLineTR — döngü isteminin SONUNA giden durum bloğu (değişken
// bloklar sonda — §6.0). AKTİF SOHBET BAĞLAMI önsözünde olmayan alanlar +
// defter. Boşsa "".
func chatStateLineTR(c ChatContext) string {
	var lines []string
	if w := stateWindowLabel(c); w != "" && c.RangeExplicit {
		lines = append(lines, "- pencere: "+w)
	}
	if c.Env != "" {
		lines = append(lines, "- ortam: "+c.Env)
	}
	if len(c.Services) > 1 {
		lines = append(lines, "- servisler: "+strings.Join(c.Services, ", "))
	}
	if c.ChannelCode != "" {
		lines = append(lines, "- kanal kodu: "+c.ChannelCode)
	}
	if c.FunctionCode != "" {
		lines = append(lines, "- fonksiyon kodu: "+c.FunctionCode)
	}
	if c.Problem != "" {
		lines = append(lines, "- problem: "+c.Problem)
	}
	if c.Address == addressSiz {
		lines = append(lines, "- hitap: siz (kullanıcı siz diye yazıyor; cevapta siz kullan)")
	}
	var b strings.Builder
	if len(lines) > 0 {
		b.WriteString("\n\nKONUŞMA DURUMU (sunucu tutar; kullanıcı değiştirmedikçe bu kapsamla çalış, devraldığın alanı cevapta bir kez söyle):\n")
		b.WriteString(strings.Join(lines, "\n"))
	}
	if len(c.Ledger) > 0 {
		b.WriteString("\n\nÖNCEKİ TURUN OLGULARI (araç çıktısından; bu turda yeniden okumadan yeni sayı türetme):\n")
		for _, f := range c.Ledger {
			fmt.Fprintf(&b, "- %s", f.Tool)
			if f.Scope != "" {
				fmt.Fprintf(&b, " %s", f.Scope)
			}
			fmt.Fprintf(&b, " · %s=%s", f.Name, strconv.FormatFloat(f.Value, 'f', -1, 64))
			if f.Unit != "" {
				fmt.Fprintf(&b, " %s", f.Unit)
			}
			if f.Window != "" {
				fmt.Fprintf(&b, " · %s", f.Window)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// promptLine — döngü istemi bloğu (nil-güvenli).
func (t *chatStateTurn) promptLine() string {
	if t == nil {
		return ""
	}
	return chatStateLineTR(t.st.ctx)
}

// chatStateIntentLine — niyet sınıflandırıcısının girdisine eklenen tek satır
// (D5 notu: sınıflandırıcı bağlamı görmüyordu). Boşsa "".
func chatStateIntentLine(c ChatContext) string {
	var parts []string
	if c.Service != "" {
		parts = append(parts, "servis "+c.Service)
	}
	if w := stateWindowLabel(c); w != "" && c.RangeExplicit {
		parts = append(parts, "pencere "+w)
	}
	if c.Env != "" {
		parts = append(parts, "ortam "+c.Env)
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\n(konuşma bağlamı: " + strings.Join(parts, " · ") + ")"
}

// stateHasSubject — karar 14: durumda özne (servis, trace, operasyon,
// problem) var mı.
func stateHasSubject(c ChatContext) bool {
	return c.Service != "" || c.Trace != "" || c.Operation != "" || c.Problem != ""
}

// ── istemci yankısı (Redis yok, karar 2) ───────────────────────────────────

// chatStateWire — data-state yükü ve context.state yankısının ORTAK şekli
// (kapsam; defter değerleri yok).
type chatStateWire struct {
	Service      string            `json:"service,omitempty"`
	Services     []string          `json:"services,omitempty"`
	Env          string            `json:"env,omitempty"`
	Cluster      string            `json:"cluster,omitempty"`
	Window       *chatStateWireWin `json:"window,omitempty"`
	ChannelCode  string            `json:"channelCode,omitempty"`
	FunctionCode string            `json:"functionCode,omitempty"`
	Problem      string            `json:"problem,omitempty"`
	Trace        string            `json:"trace,omitempty"`
	Operation    string            `json:"operation,omitempty"`
	Address      string            `json:"address,omitempty"`
	Offer        *chatStateWireOff `json:"offer,omitempty"`
	UpdatedAt    int64             `json:"updatedAt,omitempty"` // unix ms
}

type chatStateWireWin struct {
	RangeS   int64  `json:"rangeS"`
	ToMs     int64  `json:"toMs,omitempty"`
	Label    string `json:"label,omitempty"`
	Explicit bool   `json:"explicit,omitempty"`
}

type chatStateWireOff struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	At    int64  `json:"at,omitempty"`
}

var stateCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)

// validateStateEcho — SAF: istemcinin geri yolladığı durum. Servis / ortam
// canlı kataloğa, biçimler kapalı kümeye; geçmeyen alan DÜŞER. Operasyon
// katalog okuması pahalı olduğundan yankıdan alınmaz. updatedAt yok ya da
// TTL dışı → hiçbiri.
func validateStateEcho(in chatStateWire, svcs, envs []string, now time.Time) (chatStateWire, bool) {
	if in.UpdatedAt <= 0 || now.Sub(time.UnixMilli(in.UpdatedAt)) > chatStateTTL || time.UnixMilli(in.UpdatedAt).After(now.Add(time.Minute)) {
		return chatStateWire{}, false
	}
	out := chatStateWire{UpdatedAt: in.UpdatedAt}
	if v := canonicalIn(svcs, in.Service); v != "" {
		out.Service = v
	}
	for _, s := range in.Services {
		if v := canonicalIn(svcs, s); v != "" && len(out.Services) < 5 {
			out.Services = append(out.Services, v)
		}
	}
	if v := canonicalIn(envs, in.Env); v != "" {
		out.Env = v
	}
	if w := in.Window; w != nil && w.RangeS >= 60 && w.RangeS <= 30*86400 {
		if _, anchored := chatAnchorTime(w.ToMs, now); w.ToMs == 0 || anchored {
			out.Window = &chatStateWireWin{RangeS: w.RangeS, ToMs: w.ToMs, Label: clipRunes(strings.TrimSpace(w.Label), 60), Explicit: w.Explicit}
		}
	}
	if stateCodeRe.MatchString(in.ChannelCode) {
		out.ChannelCode = in.ChannelCode
	}
	if stateCodeRe.MatchString(in.FunctionCode) {
		out.FunctionCode = in.FunctionCode
	}
	if chatProblemIDRe.MatchString(in.Problem) {
		out.Problem = in.Problem
	}
	if traceIDRe.MatchString(in.Trace) {
		out.Trace = strings.ToLower(in.Trace)
	}
	if in.Address == addressSen || in.Address == addressSiz {
		out.Address = in.Address
	}
	if o := in.Offer; o != nil && offerKinds[o.Kind] != "" && out.Service != "" {
		out.Offer = &chatStateWireOff{Kind: o.Kind, At: o.At}
	}
	return out, true
}

func canonicalIn(list []string, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return s
		}
	}
	return ""
}

// mergeStateEcho — SAF: doğrulanmış yankı boş Redis durumunun üstüne.
func mergeStateEcho(c ChatContext, e chatStateWire) ChatContext {
	c.Service, c.Services, c.Env = e.Service, e.Services, e.Env
	if w := e.Window; w != nil {
		c.RangeS, c.WinToMs, c.WinLabel, c.RangeExplicit = w.RangeS, w.ToMs, w.Label, w.Explicit
	}
	c.ChannelCode, c.FunctionCode, c.Problem, c.Trace, c.Address = e.ChannelCode, e.FunctionCode, e.Problem, e.Trace, e.Address
	if o := e.Offer; o != nil {
		c.Offer = &chatOffer{Kind: o.Kind, Service: e.Service, Label: e.Service + " " + offerKinds[o.Kind], At: o.At}
	}
	c.UpdatedAt = e.UpdatedAt * int64(time.Millisecond)
	c.Ledger = nil
	return c
}

// chatStateWireOf — SAF: durumun tel görünümü (data-state; defter yok).
func chatStateWireOf(c ChatContext, now time.Time) chatStateWire {
	w := chatStateWire{Service: c.Service, Services: c.Services, Env: c.Env, Cluster: c.Cluster,
		ChannelCode: c.ChannelCode, FunctionCode: c.FunctionCode, Problem: c.Problem, Trace: c.Trace,
		Operation: c.Operation, Address: c.Address, UpdatedAt: now.UnixMilli()}
	if c.RangeS > 0 {
		w.Window = &chatStateWireWin{RangeS: c.RangeS, ToMs: c.WinToMs, Label: stateWindowLabel(c), Explicit: c.RangeExplicit}
	}
	if o := c.Offer; o != nil {
		w.Offer = &chatStateWireOff{Kind: o.Kind, Label: o.Label, At: o.At}
	}
	return w
}

// stateWireEmpty — data-state yayınlanmaz (kapsam yok).
func stateWireEmpty(w chatStateWire) bool {
	return w.Service == "" && w.Env == "" && w.Cluster == "" && w.Window == nil && w.ChannelCode == "" &&
		w.FunctionCode == "" && w.Problem == "" && w.Trace == "" && w.Operation == "" && w.Offer == nil
}

// chatStateEventKind — v0.10.2325: data-state olayı (yalnız v2 yazıcısına
// ulaşır; v1OnlySink düşürür — chat_stop_event.go v2OnlyEventKinds).
const chatStateEventKind = "state"

// chatStatePatch — istemcinin `context.statePatch`i (v0.10.2325, karar 4
// sözleşmesi; arayüzde düzenleme henüz yok). nil alan dokunmaz; Clear
// listedeki alanları siler. Doğrulama yankı ile aynı kurallar.
type chatStatePatch struct {
	Service *string           `json:"service,omitempty"`
	Env     *string           `json:"env,omitempty"`
	Window  *chatStateWireWin `json:"window,omitempty"`
	Clear   []string          `json:"clear,omitempty"`
}

// applyStatePatch — SAF: geçersiz alan varsa yama BÜTÜNÜYLE reddedilir
// (ok=false) — yarım uygulanmış düzenleme yok.
func applyStatePatch(c ChatContext, p chatStatePatch, svcs, envs []string, now time.Time) (ChatContext, bool) {
	for _, f := range p.Clear {
		switch f {
		case "service":
			c.Service, c.Services, c.Operation, c.Trace = "", nil, "", ""
		case "env":
			c.Env = ""
		case "window":
			c.RangeS, c.WinToMs, c.WinLabel, c.RangeExplicit = 0, 0, "", false
		case "offer":
			c.Offer = nil
		default:
			return c, false
		}
	}
	if p.Service != nil {
		v := canonicalIn(svcs, *p.Service)
		if v == "" {
			return c, false
		}
		if v != c.Service {
			c.Operation, c.Trace = "", ""
		}
		c.Service, c.Services = v, nil
	}
	if p.Env != nil {
		v := canonicalIn(envs, *p.Env)
		if v == "" {
			return c, false
		}
		c.Env = v
	}
	if w := p.Window; w != nil {
		if w.RangeS < 60 || w.RangeS > 30*86400 {
			return c, false
		}
		if _, anchored := chatAnchorTime(w.ToMs, now); w.ToMs != 0 && !anchored {
			return c, false
		}
		label := clipRunes(strings.TrimSpace(w.Label), 60)
		if label == "" {
			label = fmtRangeTR(w.RangeS)
		}
		c.RangeS, c.WinToMs, c.WinLabel, c.RangeExplicit = w.RangeS, w.ToMs, label, true
	}
	return c, true
}
