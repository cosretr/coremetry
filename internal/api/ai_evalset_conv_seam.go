package api

// ai_evalset_conv_seam.go — v0.10.2322 (H1, docs/cosre/conversation-audit.md
// §6.1): değerlendirme setinin KONUŞMA KİPİ için handler dikişleri.
//
// Konuşma kipi turu canlı sohbet handler'ını (copilotChatWith, v2 yolu)
// üretimdeki kademe zinciriyle koşar; handler'ın üretimden ayrıldığı yerler
// yalnız chatEvalMode.Conversation'a bakan aşağıdaki küçük fonksiyonlardır.
// Hepsi nil işarette (üretim) ya da Conversation nil iken (tek turlu ajan
// vakası) hiçbir şeye dokunmaz — TestEvalConvSeamInertInProduction pinler.
//
//   - sohbet bağlamı: chatEvalMode.evalChatContext (chat_eval_mode.go) —
//     koşucunun bellek içi durumu; kullanıcısız olduğu için flushChatContext
//     Redis'e yazmaz, koşuyu başlatanın bağlamına dokunulmaz;
//   - döngü araçları: stubLoopHandlers — mevcut handler dikişinin
//     (chatLoopHandlerSeam) hemen ardında, sohbet bağlamı araçları HARİÇ her
//     handler vakanın sahte sonucunu döner (CH/ES/VM'e gidilmez);
//   - guided veri paketleri: evalGuidedBundleIntent + evalGuidedStubBundle —
//     rota çözümü ve anlatım çağrısı gerçek, yalnız kanıt paketi sahte;
//   - guided servis kataloğu: evalConvCatalog — router vakanın nötr adlarını
//     (checkout, orders, payments, inventory) tanır.
//
// Bu dosya bilinçli olarak yüzey literali ve model çağrısı taşımaz.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sre"
)

// guidedEvalStub — konuşma kipinde guided veri paketinin yerine geçen sahte
// paketin switch etiketi. Kullanıcı sorusundan hiçbir zaman türemez (router
// bu değeri üretmez); yalnız evalGuidedBundleIntent döner.
const guidedEvalStub guidedIntent = "eval_stub"

// evalConvNoStub — sahtesi olmayan döngü aracının sabit sonucu
// (deterministik; model "veri yok" ile devam eder).
const evalConvNoStub = `{"error":"değerlendirme sahtesi: bu araç için kayıt yok"}`

// errEvalConvNoStub — sahtesi olmayan guided rotası: üretimdeki "ön-getirme
// düştü" yolu (guided cevaplamaz, zincir sürer).
var errEvalConvNoStub = errors.New("değerlendirme sahtesi: bu rota için kayıt yok")

// evalConvTurn — konuşma kipinin bir turu için handler'a giden durum.
type evalConvTurn struct {
	// State — turun sohbet bağlamı (koşucu önceki turun bağlamıyla tohumlar,
	// tur bitince okur). Kullanıcısız: Redis'e yazılmaz.
	State *chatContextState
	// Stubs — ad → sahte sonuç (JSON). Ad döngü aracı ("get_service_health")
	// ya da guided rota niyeti ("service_health") olabilir.
	Stubs map[string]json.RawMessage
	// Catalog — guided router'ın servis kataloğu (nötr adlar).
	Catalog []string
	// Style — v0.10.2331 (B): koşunun üslup anahtarı zorlaması (nil = canlı
	// ayar). Yalnız bu konuşma kipi turunda okunur (answerStyleOn).
	Style *bool
	// Verification — v0.10.2339 (E2): koşunun / vakanın doğrulama zorlaması
	// (off | shadow | enforce; "" = canlı ayar). Yalnız bu turda (verifyEnforceOn).
	Verification string
	// Evidence — v0.10.2360 (S3): vakanın kanıt kipi zorlaması (off | shadow |
	// on; "" = canlı ayar). cosreEvidenceMode okur.
	Evidence string

	mu      sync.Mutex
	evMode  string   // v0.10.2360 — turun koştuğu kanıt kipi ("" = karar koşmadı)
	evShown []string // v0.10.2360 — eklenen blok tipleri
	calls   []evalConvCall
	class   string // v0.10.2331 — turun cevap sınıfı (answerStyleTurn.note)
	dropped int    // v0.10.2338 — cevap dikişinin sildiği model kalıp cümlesi
	enforce string // v0.10.2339 — zorla kipinin kararı (pass | annotated | regenerated…)
	packOK  int    // v0.10.2361 — S4: kanıt paketinin tamamlanan adımı (-1 = koşmadı)
	packUn  int    // v0.10.2361 — unchecked adım
	packRan bool
	packDig string // v0.10.2361 — modele giden özet (sunucu türevi sayılar kanıttır)
}

// notePack — v0.10.2361 (S4): turun kanıt paketi adımları. nil-güvenli.
func (t *evalConvTurn) notePack(steps []sre.ChatPackStep, digest string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.packRan, t.packOK, t.packUn, t.packDig = true, 0, 0, digest
	for _, s := range steps {
		if s.OK {
			t.packOK++
		} else {
			t.packUn++
		}
	}
}

// pack — v0.10.2361: paket koştu mu, tamamlanan ve bakılamayan adım.
func (t *evalConvTurn) pack() (ran bool, ok, unchecked int, digest string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.packRan, t.packOK, t.packUn, t.packDig
}

// noteEnforce — v0.10.2339 (E2): dikişin zorla kararı. nil-güvenli.
func (t *evalConvTurn) noteEnforce(mode string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.enforce = mode
	t.mu.Unlock()
}

func (t *evalConvTurn) enforceMode() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.enforce
}

// noteSeamDropped — v0.10.2338 (D): dikişin sildiği kalıp cümle sayısı. nil-güvenli.
func (t *evalConvTurn) noteSeamDropped(n int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.dropped += n
	t.mu.Unlock()
}

// seamDropped — v0.10.2338: turda silinen kalıp cümle.
func (t *evalConvTurn) seamDropped() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}

// noteAnswerClass — v0.10.2331: turun cevap sınıfı (son kademe kazanır). nil-güvenli.
func (t *evalConvTurn) noteAnswerClass(c string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.class = c
	t.mu.Unlock()
}

// noteEvidence — v0.10.2360 (S3): turun kanıt kararı. nil-güvenli.
func (t *evalConvTurn) noteEvidence(mode string, shown []string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.evMode, t.evShown = mode, append([]string(nil), shown...)
	t.mu.Unlock()
}

// evidence — v0.10.2360: turun kanıt kipi ve eklenen blok tipleri.
func (t *evalConvTurn) evidence() (string, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.evMode, append([]string(nil), t.evShown...)
}

// answerClass — v0.10.2331: turun cevap sınıfı.
func (t *evalConvTurn) answerClass() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.class
}

// evalConvCall — turda sahteden karşılanan bir veri okuması (puanlama:
// hangi varlık ve pencere çözüldü).
type evalConvCall struct {
	Name   string          `json:"name"`
	Origin string          `json:"origin"` // "loop" | "guided"
	Args   json.RawMessage `json:"args"`
	Stub   bool            `json:"stub"` // sahte sonuç vardı
}

func (t *evalConvTurn) note(c evalConvCall) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, c)
}

// snapshot — turun veri okumaları (kopya).
func (t *evalConvTurn) snapshot() []evalConvCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]evalConvCall(nil), t.calls...)
}

// evalConvFrom — ctx'teki konuşma kipi turu; üretimde ve tek turlu vakada nil.
func evalConvFrom(ctx context.Context) *evalConvTurn {
	if em := chatEvalModeFrom(ctx); em != nil {
		return em.Conversation
	}
	return nil
}

// evalConvCatalog — konuşma kipinde guided servis kataloğu; aksi hâlde nil
// (guidedServiceNames üretim yolunu koşar).
func evalConvCatalog(ctx context.Context) []string {
	if t := evalConvFrom(ctx); t != nil && len(t.Catalog) > 0 {
		return append([]string(nil), t.Catalog...)
	}
	return nil
}

// stubLoopHandlers — konuşma kipinde döngü handler'larını sahteye bağlar.
// Sohbet bağlamı araçları (set/get/clear_context) gerçek kalır: bellek içi
// durumu okur/yazar, yani konuşma durumunun parçasıdır. v0.10.2328 notu: v2'de
// cosre_conversation.state açıkken (varsayılan) bu üç araç katalogda HİÇ yok
// (v0.10.2326, chat_state_tools.go) — durum çözücüden ve döngünün kendi
// yazımından gelir; hiçbir vaka beklentisi bu araçları anmaz. nil işaret ya da
// tek turlu vaka → dokunmaz.
func (em *chatEvalMode) stubLoopHandlers(byName map[string]mcp.ToolHandler) {
	if em == nil || em.Conversation == nil {
		return
	}
	t := em.Conversation
	for name := range byName {
		if mcptools.ConversationStateTools[name] {
			continue
		}
		byName[name] = evalConvStubHandler(t, name)
	}
}

func evalConvStubHandler(t *evalConvTurn, name string) mcp.ToolHandler {
	return func(_ context.Context, args json.RawMessage) (any, error) {
		res, ok := evalStubFor(t.Stubs[name], args)
		t.note(evalConvCall{Name: name, Origin: "loop", Args: append(json.RawMessage(nil), args...), Stub: ok})
		if !ok {
			return json.RawMessage(evalConvNoStub), nil
		}
		return append(json.RawMessage(nil), res...), nil
	}
}

// evalGuidedBundleIntent — guided paket switch'inin etiketi: konuşma kipinde
// guidedEvalStub (netleştirme sorusu hariç — o LLM'siz "hangisi?" cevabıdır,
// veri paketi değil), aksi hâlde intent aynen.
func evalGuidedBundleIntent(ctx context.Context, intent guidedIntent) guidedIntent {
	if evalConvFrom(ctx) == nil || intent == guidedAskService {
		return intent
	}
	return guidedEvalStub
}

// evalGuidedStubBundle — konuşma kipinde guided kanıt paketi: rotanın niyet
// adıyla vakanın sahtesi. Çip ve kanıt üretimdeki gibi yayınlanır (köken
// "guided"); sahte yoksa errEvalConvNoStub (guided cevaplamaz).
func evalGuidedStubBundle(ctx context.Context, emit func(string, any), route guidedRoute, from, to time.Time) (evidence, sources string, err error) {
	t := evalConvFrom(ctx)
	if t == nil {
		return "", "", errEvalConvNoStub
	}
	name := string(route.Intent)
	args, _ := json.Marshal(map[string]any{
		"service": route.Service, "env": route.Env,
		"from_ms": from.UnixMilli(), "to_ms": to.UnixMilli(),
	})
	res, ok := evalStubFor(t.Stubs[name], args)
	t.note(evalConvCall{Name: name, Origin: "guided", Args: args, Stub: ok})
	if !ok {
		return "", "", errEvalConvNoStub
	}
	evidence = evalStubText(res)
	n := emitGuidedStep(emit, name, string(args))
	emitGuidedStepResult(emit, n, name, evidence, nil)
	return evidence, name + " (değerlendirme sahtesi)", nil
}

// evalConvEndpointStub — uç nokta seçici kartının (chat_endpoint_pick.go)
// sahte satırlarının adı; yoksa satır yok (kart çıkmaz, akış sürer).
const evalConvEndpointStub = "endpoint_candidates"

// evalConvEndpointRows — konuşma kipinde uç nokta kartının satırları.
func evalConvEndpointRows(t *evalConvTurn, service string) []chstore.EndpointRow {
	args, _ := json.Marshal(map[string]string{"service": service})
	raw, ok := evalStubFor(t.Stubs[evalConvEndpointStub], args)
	if !ok {
		return nil // sahte yoksa okuma da sayılmaz: kademe yoklaması, veri okuması değil
	}
	t.note(evalConvCall{Name: evalConvEndpointStub, Origin: "guided", Args: args, Stub: true})
	var rows []chstore.EndpointRow
	_ = json.Unmarshal(raw, &rows)
	return rows
}

// evalStubByServiceKey — sahtenin servise göre seçilen biçimi:
// {"$byService": {"checkout": …, "*": …}}.
const evalStubByServiceKey = "$byService"

// evalStubFor — SAF: okumanın argümanlarına göre sahte sonuç. Düz sahte
// aynen döner; $byService biçiminde argümandaki servis (service, services[0],
// service_name, text) anahtarla eşleşirse o, yoksa "*", o da yoksa sahte yok.
func evalStubFor(raw json.RawMessage, args json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var wrap map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &wrap) == nil && len(wrap) == 1 && wrap[evalStubByArgKey] != nil {
		return evalStubByArg(wrap[evalStubByArgKey], args)
	}
	if json.Unmarshal(raw, &wrap) != nil || len(wrap) != 1 || wrap[evalStubByServiceKey] == nil {
		return raw, true
	}
	by := wrap[evalStubByServiceKey]
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	var cands []string
	for _, k := range []string{"service", "service_name", "serviceName", "text", "query"} {
		if v, ok := m[k].(string); ok && v != "" {
			cands = append(cands, strings.ToLower(v))
		}
	}
	if arr, ok := m["services"].([]any); ok && len(arr) > 0 {
		if v, ok := arr[0].(string); ok {
			cands = append(cands, strings.ToLower(v))
		}
	}
	keys := evalStubNames(by)
	for _, c := range cands { // önce tam eş
		for _, k := range keys {
			if k != "*" && strings.ToLower(k) == c {
				return by[k], true
			}
		}
	}
	for _, c := range cands { // sonra metin içinde geçen ad
		for _, k := range keys {
			if k != "*" && strings.Contains(c, strings.ToLower(k)) {
				return by[k], true
			}
		}
	}
	if v, ok := by["*"]; ok {
		return v, true
	}
	return nil, false
}

// evalStubByArgKey — v0.10.2356 (S2 arama vakaları): sahtenin argüman
// metnine göre seçilen biçimi {"$byArg": {"tenant.teir": …, "*": …}} —
// ham argüman JSON'u anahtarı içeriyorsa o (uzun anahtar önce), yoksa "*".
// Yanlış anahtarla çağrının adlandırılmış hatası, düzeltilmişinin satırları.
const evalStubByArgKey = "$byArg"

func evalStubByArg(by map[string]json.RawMessage, args json.RawMessage) (json.RawMessage, bool) {
	keys := evalStubNames(by)
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		if k != "*" && strings.Contains(string(args), k) {
			return by[k], true
		}
	}
	if v, ok := by["*"]; ok {
		return v, true
	}
	return nil, false
}

// evalStubText — SAF: sahte sonucun metni (JSON dizgisi → içeriği, diğerleri
// sıkıştırılmış JSON).
func evalStubText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// evalStubNames — SAF: sahte adlarının sıralı listesi (iz / hata metni).
func evalStubNames(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
