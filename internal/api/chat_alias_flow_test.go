package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_alias_flow_test.go — v0.10.2334 (conversation-audit §6.1 G, kabul):
// GERÇEK v2 handler'ı + sahte model + sahte araçlar + Redis sahtesi + bellek
// içi kullanıcı deposu. Sahte model "kart tarafı" için HER kullanıcıda kendi
// katalog tahminini (orders) yazar; doğru eşleme yalnız o kullanıcının
// düzeltmesinden ve kaydından gelir. Nötr adlar: checkout, orders, payments,
// inventory.

// memPrefStore — saved_views'in bellek içi ikizi (nokta okuma sayacıyla).
type memPrefStore struct {
	mu    sync.Mutex
	rows  map[string]chstore.SavedView
	reads int
}

func (m *memPrefStore) GetSavedView(_ context.Context, id string) (*chstore.SavedView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	v, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (m *memPrefStore) UpsertSavedView(_ context.Context, v chstore.SavedView) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[v.ID] = v
	return nil
}

func (m *memPrefStore) prefs(uid string) cosreUserPrefs {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.rows[cosreUserRowID(uid)]
	if !ok {
		return cosreUserPrefs{}
	}
	return decodeCosreUserPrefs(v.QueryString)
}

func installMemPrefStore(t *testing.T) *memPrefStore {
	t.Helper()
	st := &memPrefStore{rows: map[string]chstore.SavedView{}}
	prev := cosreUserPrefsStoreOf
	cosreUserPrefsStoreOf = func(*Server) displayPrefStore { return st }
	resetCosreUserPrefsCache()
	t.Cleanup(func() { cosreUserPrefsStoreOf = prev; resetCosreUserPrefsCache() })
	return st
}

type aliasRig struct {
	*stateRig
	prefs *memPrefStore
	conv  map[string][]map[string]any // konuşma → geçmiş
}

func newAliasRig(t *testing.T) *aliasRig {
	t.Helper()
	setConversationState(t, true)
	rg := newStateRig(t, func(string, int) map[string]any { return nil }, func(guidedRoute) string { return "Son bir saatte belirgin bir sorun görünmüyor." })
	rg.mc.m["copilot:guided:svcnames"] = []byte(`["checkout","orders","payments","inventory"]`)
	rg.llm = nil
	ar := &aliasRig{stateRig: rg, prefs: installMemPrefStore(t), conv: map[string][]map[string]any{}}
	// Sahte model — araç sunulan çağrıda: son mesaj araç sonucuysa cevap; değilse
	// "kart" geçen (düzeltmesiz) soruda modelin kendi tahmini orders, diğerlerinde
	// argümansız servis sağlığı (servis durumdan iner).
	llm := newLoopLLM(t, func(_ int, body map[string]any) map[string]any {
		if _, ok := body["tools"]; !ok {
			return answerMsg(`{"intent":"none"}`)
		}
		msgs, _ := body["messages"].([]any)
		if len(msgs) > 0 {
			if m, _ := msgs[len(msgs)-1].(map[string]any); m["role"] == "tool" {
				return answerMsg("Son bir saatte belirgin bir sorun görünmüyor.")
			}
		}
		if strings.Contains(lastUserContent(body), "kart") && !strings.Contains(lastUserContent(body), "değil") {
			return toolCallMsg([2]string{"get_service_health", `{"service":"orders"}`})
		}
		return toolCallMsg([2]string{"get_service_health", `{}`})
	})
	rg.s, rg.mc = newLoopTestServer(t, llm, "u-a", "u-b")
	rg.s.l1, rg.s.stats = newL1Cache(64), newCacheStats()
	rg.mc.m["copilot:guided:svcnames"] = []byte(`["checkout","orders","payments","inventory"]`)
	setCosreV2(t, rg.s, true)
	// Niyet "none" → serbest döngü (on): modelin kendi katalog tahmini görünsün.
	rg.s.copilot.SetIntentClassify(copilot.IntentOn)
	return ar
}

// say — kullanıcı uid, konuşma conv'da bir tur; akış gövdesi.
func (ar *aliasRig) say(uid, conv, q string) string {
	ar.t.Helper()
	ar.conv[conv] = append(ar.conv[conv], map[string]any{"role": "user", "text": q})
	raw, _ := json.Marshal(map[string]any{"messages": ar.conv[conv], "context": map[string]any{"conversation": conv, "tz": "Europe/Istanbul"}})
	r := httptest.NewRequest(http.MethodPost, "/api/cosre/v2/chat", bytes.NewReader(raw))
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: uid, Email: uid + "@example.test", Role: "viewer"}))
	w := serveV2(ar.s, r)
	if w.Code != http.StatusOK {
		ar.t.Fatalf("%q → %d %s", q, w.Code, w.Body.String())
	}
	body := w.Body.String()
	ar.conv[conv] = append(ar.conv[conv], map[string]any{"role": "assistant", "text": v2AnswerText(body)})
	return body
}

func (ar *aliasRig) stateOf(uid, conv string) ChatContext {
	ar.t.Helper()
	var c ChatContext
	raw, ok := ar.mc.m[chatContextKey(uid, conv)]
	if !ok {
		ar.t.Fatalf("Redis'te %s/%s durumu yok", uid, conv)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		ar.t.Fatal(err)
	}
	return c
}

// mark — okuma sayaçlarının şimdiki konumu (araç çağrıları, guided rotaları).
func (ar *aliasRig) mark() [2]int {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	return [2]int{len(ar.tools.snapshot()), len(ar.guided)}
}

// servicesRead — işaretten sonra okunan servisler: döngü araç argümanları ve
// guided rotalarının servisi (iki kademe de "okuma"dır).
func (ar *aliasRig) servicesRead(from [2]int) []string {
	var out []string
	for _, c := range ar.tools.snapshot()[from[0]:] {
		s, _ := c.args["service"].(string)
		out = append(out, s)
	}
	ar.mu.Lock()
	for _, g := range ar.guided[from[1]:] {
		out = append(out, g.route.Service)
	}
	ar.mu.Unlock()
	return out
}

func seedAlias(t *testing.T, st *memPrefStore, uid string, a cosreUserAlias) {
	t.Helper()
	p := withCosreUserAlias(cosreUserPrefs{}, a)
	_ = st.UpsertSavedView(context.Background(), chstore.SavedView{ID: cosreUserRowID(uid), OwnerID: uid, Name: cosreUserPage,
		Page: cosreUserPage, QueryString: encodeCosreUserPrefs(p)})
}

// TestAliasCorrectionInConversation — düzeltme durumu değiştirir ve aynı
// deyim aynı konuşmada doğrudan yeni varlığa çözülür; eski varlık okunmaz.
func TestAliasCorrectionInConversation(t *testing.T) {
	ar := newAliasRig(t)
	ar.say("u-a", "c1", "kart tarafı nasıl")
	if got := ar.servicesRead([2]int{}); len(got) != 1 || got[0] != "orders" {
		t.Fatalf("tur 1: model tahmini orders okunmalıydı: %v", got)
	}
	if st := ar.stateOf("u-a", "c1"); st.Service != "orders" {
		t.Fatalf("tur 1 durumu: %+v", st)
	}
	n := ar.mark()
	ar.say("u-a", "c1", "o değil, payments")
	st := ar.stateOf("u-a", "c1")
	if st.Service != "payments" || len(st.Corrections) != 1 {
		t.Fatalf("düzeltme durumu: %+v", st)
	}
	if k := st.Corrections[0]; k.Phrase != "kart tarafı" || k.Wrong != "orders" || k.Right != "payments" {
		t.Fatalf("düzeltme kaydı: %+v", k)
	}
	if got := ar.servicesRead(n); len(got) == 0 || got[0] != "payments" {
		t.Fatalf("tur 2 okuması: %v", got)
	}
	n = ar.mark()
	body := ar.say("u-a", "c1", "kart tarafında hata var mı")
	if got := ar.servicesRead(n); !allEqual(got, "payments") {
		t.Fatalf("aynı konuşmada yanlış eşleme yeniden okundu: %v", got)
	}
	if !hasLabel(v2StepLabels(body), "eş adın: «kart tarafı» → payments") {
		t.Fatalf("eş ad çipi yok: %v", v2StepLabels(body))
	}
	if len(ar.prefs.rows) != 0 {
		t.Fatalf("onaysız kalıcı kayıt yazıldı: %+v", ar.prefs.rows)
	}
}

// TestAliasSavedMappingIsPersonal — kayıtlı eşleme yeni konuşmada katalogdan
// önce uygulanır (orders hiç okunmaz); başka kullanıcı etkilenmez.
func TestAliasSavedMappingIsPersonal(t *testing.T) {
	ar := newAliasRig(t)
	seedAlias(t, ar.prefs, "u-a", cosreUserAlias{P: "kart tarafı", ID: "svc:payments", Not: "svc:orders", At: 1})

	ar.say("u-a", "c2", "kart tarafı nasıl")
	if got := ar.servicesRead([2]int{}); !allEqual(got, "payments") {
		t.Fatalf("u-a: eşleme uygulanmadı / orders okundu: %v", got)
	}
	if st := ar.stateOf("u-a", "c2"); st.Service != "payments" {
		t.Fatalf("u-a durumu: %+v", st)
	}
	n := ar.mark()
	ar.say("u-b", "c3", "kart tarafı nasıl")
	if got := ar.servicesRead(n); len(got) != 1 || got[0] != "orders" {
		t.Fatalf("u-b etkilendi: %v", got)
	}
	if st := ar.stateOf("u-b", "c3"); st.Service != "orders" {
		t.Fatalf("u-b durumu: %+v", st)
	}
}

// TestAliasStaleTargetIgnored — hedef katalogdan kalkmışsa eşleme yok sayılır.
func TestAliasStaleTargetIgnored(t *testing.T) {
	ar := newAliasRig(t)
	seedAlias(t, ar.prefs, "u-a", cosreUserAlias{P: "kart tarafı", ID: "svc:legacy-cards", Not: "svc:orders", At: 1})
	body := ar.say("u-a", "c4", "kart tarafı nasıl")
	if got := ar.servicesRead([2]int{}); len(got) != 1 || got[0] != "orders" {
		t.Fatalf("bayat eşleme uygulandı: %v", got)
	}
	if hasLabel(v2StepLabels(body), "eş adın") {
		t.Fatalf("bayat eşleme çipi: %v", v2StepLabels(body))
	}
}

// TestAliasPrefsOneCachedRead — kaydı olmayan kullanıcıda turlar boyunca tek nokta okuma.
func TestAliasPrefsOneCachedRead(t *testing.T) {
	ar := newAliasRig(t)
	ar.say("u-b", "c5", "checkout nasıl")
	ar.say("u-b", "c5", "peki inventory")
	if ar.prefs.reads != 1 {
		t.Fatalf("kaydı olmayan kullanıcıda okuma sayısı %d (1 bekleniyor)", ar.prefs.reads)
	}
}

func allEqual(list []string, v string) bool {
	for _, s := range list {
		if s != v {
			return false
		}
	}
	return len(list) > 0
}

// TestAliasServiceNotVerifierEvidence — eş addan çözülen servis doğrulayıcı
// kanıtına girmez; aynı servis açık adla çözüldüyse girer.
func TestAliasServiceNotVerifierEvidence(t *testing.T) {
	v := &answerVerifyTurn{}
	v.noteState(&chatContextState{ctx: ChatContext{Service: "payments", Env: "uat"}, aliasService: "payments"})
	for _, n := range v.names {
		if n == "payments" {
			t.Fatalf("eş ad servisi kanıta girdi: %v", v.names)
		}
	}
	v = &answerVerifyTurn{}
	v.noteState(&chatContextState{ctx: ChatContext{Service: "payments"}})
	if len(v.names) != 1 || v.names[0] != "payments" {
		t.Fatalf("açık ad kanıtı: %v", v.names)
	}
}

// TestAliasAddressSeedsNewConversation — karar 13: kayıtlı hitap yeni
// konuşmanın başlangıcı; açık işaret kayda yazılır.
func TestAliasAddressSeedsNewConversation(t *testing.T) {
	ar := newAliasRig(t)
	_ = ar.prefs.UpsertSavedView(context.Background(), chstore.SavedView{ID: cosreUserRowID("u-a"), OwnerID: "u-a", Name: cosreUserPage,
		Page: cosreUserPage, QueryString: encodeCosreUserPrefs(cosreUserPrefs{Addr: "siz"})})
	ar.say("u-a", "c6", "checkout nasıl")
	if st := ar.stateOf("u-a", "c6"); st.Address != "siz" {
		t.Fatalf("yeni konuşma kayıtlı hitapla başlamadı: %q", st.Address)
	}
	ar.say("u-b", "c7", "checkout'a bakar mısınız")
	waitFor(t, "hitap kaydı", func() bool { return ar.prefs.prefs("u-b").Addr == "siz" })
}
