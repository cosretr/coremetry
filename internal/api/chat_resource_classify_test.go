package api

// chat_resource_classify_test.go — v0.10.1442 (CoSRE kaynak incelemesi R3).
// Operatör: "CoSRE sorulan sorulara frontier model gibi anlamlandırarak gerekli
// toolset'lerini koşsun … bir pod'un CPU/memory metriklerini grafik olarak
// getirme; gerekirse GC'sine, JVM metriklerine bakıp LLM'e yorumlatma."
//
// Sözleşme: dar sınıflandırıcı yalnız güven ≥0.6 VE canlı doğrulanmış hedefle
// kabul edilir; bozuk JSON / düşük güven / bilinmeyen hedef → none (genel
// sınıflandırıcı yolu değişmez); grafik fiili metric_chart'ı kazandırır;
// serbest PromQL yok; önbellek anahtarı tüm girdileri taşır.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

var rcServices = []string{"orders-api", "payments", "checkout-service", "ledger-service", "ledger-writer"}

const rcPod = "orders-api-6d4f8b9c7d-x2k9q"

// rcPods — sahte Thanos: regex'e karşı küçük bir canlı pod kümesi.
func rcPods(readable bool, live ...string) func(string) (bool, bool) {
	return func(podRe string) (bool, bool) {
		if !readable {
			return false, false
		}
		for _, p := range live {
			if strings.Contains(podRe, p) {
				return true, true
			}
		}
		return false, true
	}
}

func rcJSON(intent, target, kind, family string, rangeS, conf float64) string {
	b, _ := json.Marshal(resClassifyJSON{Intent: intent, Target: target, TargetKind: kind, Family: family, RangeS: rangeS, Confidence: conf})
	return string(b)
}

func TestDecideResourceClassify(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		q        string
		ctxSvc   string
		pods     func(string) (bool, bool)
		wantOK   bool
		intent   guidedIntent
		service  string
		target   string
		kind     string
		family   string
		template string
		rangeS   int64
	}{
		{name: "kabul: servis, router'ın kaçırdığı 'işlemci zorlanıyor'", raw: rcJSON("resource_investigation", "orders-api", "service", "cpu", 0, 0.85),
			q: "orders-api'nin işlemcisi zorlanıyor mu?", wantOK: true, intent: guidedResourceInvestigation, service: "orders-api", target: "orders-api", kind: resKindService, family: resFamCPU},
		{name: "kabul: güven tam eşikte (0.6)", raw: rcJSON("resource_investigation", "orders-api", "service", "memory", 0, 0.6),
			q: "orders-api hafızası şişti mi", wantOK: true, intent: guidedResourceInvestigation, service: "orders-api", target: "orders-api", kind: resKindService, family: resFamMemory},
		{name: "ret: düşük güven", raw: rcJSON("resource_investigation", "orders-api", "service", "cpu", 0, 0.59),
			q: "orders-api'nin işlemcisi zorlanıyor mu?"},
		{name: "ret: güven alanı yok (0)", raw: `{"intent":"resource_investigation","target":"orders-api","targetKind":"service","family":"cpu"}`,
			q: "orders-api'nin işlemcisi zorlanıyor mu?"},
		{name: "veto: katalogda olmayan servis", raw: rcJSON("resource_investigation", "billing-api", "service", "cpu", 0, 0.95),
			q: "billing-api işlemcisi zorlanıyor mu"},
		{name: "veto: canlı ama soruda geçmeyen servis (uydurma)", raw: rcJSON("resource_investigation", "payments", "service", "cpu", 0, 0.95),
			q: "işlemci zorlanıyor mu"},
		{name: "kabul: hedefsiz soru ekran servisini alır", raw: rcJSON("resource_investigation", "", "", "memory", 0, 0.8),
			q: "hafıza şişiyor mu", ctxSvc: "payments", wantOK: true, intent: guidedResourceInvestigation, service: "payments", target: "payments", kind: resKindService, family: resFamMemory},
		{name: "kabul: model ekran servisini yazdı", raw: rcJSON("resource_investigation", "payments", "service", "memory", 0, 0.8),
			q: "hafıza şişiyor mu", ctxSvc: "payments", wantOK: true, intent: guidedResourceInvestigation, service: "payments", target: "payments", kind: resKindService, family: resFamMemory},
		{name: "ret: hedef yok, ekran servisi yok", raw: rcJSON("resource_investigation", "", "", "cpu", 0, 0.9), q: "işlemci zorlanıyor mu"},
		{name: "kabul: pod Thanos'ta var, sahibi servis", raw: rcJSON("resource_investigation", rcPod, "pod", "restarts", 0, 0.9),
			q: rcPod + " sürekli kapanıp açılıyor", pods: rcPods(true, rcPod), wantOK: true, intent: guidedResourceInvestigation, service: "orders-api", target: rcPod, kind: resKindPod, family: resFamRestarts},
		{name: "veto: pod Thanos'ta yok (okunabilir)", raw: rcJSON("resource_investigation", rcPod, "pod", "restarts", 0, 0.9),
			q: rcPod + " sürekli kapanıp açılıyor", pods: rcPods(true)},
		{name: "veto: pod adı soruda geçmiyor", raw: rcJSON("resource_investigation", rcPod, "pod", "restarts", 0, 0.9),
			q: "orders-api pod'u sürekli kapanıp açılıyor", pods: rcPods(true, rcPod)},
		{name: "kabul: Thanos okunamadı, sahibi canlı servis", raw: rcJSON("resource_investigation", rcPod, "pod", "memory", 0, 0.9),
			q: rcPod + " hafızası şişti mi", pods: rcPods(false), wantOK: true, intent: guidedResourceInvestigation, service: "orders-api", target: rcPod, kind: resKindPod, family: resFamMemory},
		{name: "veto: Thanos yok, sahibi servis yok", raw: rcJSON("resource_investigation", "zzqx-6d4f8b9c7d-x2k9q", "pod", "memory", 0, 0.9),
			q: "zzqx-6d4f8b9c7d-x2k9q hafızası şişti mi"},
		{name: "kabul: workload adı canlı servis → servis kipi", raw: rcJSON("resource_investigation", "payments", "workload", "memory", 21600, 0.8),
			q: "payments deployment'ı son 6 saatte hafızayı şişirdi mi", wantOK: true, intent: guidedResourceInvestigation, service: "payments", target: "payments", kind: resKindService, family: resFamMemory, rangeS: 21600},
		{name: "kabul: workload Thanos'ta pod'lu", raw: rcJSON("resource_investigation", "billing", "workload", "cpu", 0, 0.8),
			q: "billing deployment'ı işlemciyi zorluyor mu", pods: rcPods(true, "billing"), wantOK: true, intent: guidedResourceInvestigation, target: "billing", kind: resKindWorkload, family: resFamCPU},
		{name: "veto: workload hiçbir yerde yok", raw: rcJSON("resource_investigation", "billing", "workload", "cpu", 0, 0.8),
			q: "billing deployment'ı işlemciyi zorluyor mu", pods: rcPods(true)},
		{name: "ret: bozuk JSON", raw: `intent=resource_investigation target=orders-api`, q: "orders-api işlemcisi zorlanıyor mu"},
		{name: "ret: yarım JSON", raw: `{"intent":"resource_investigation","target":"orders-api"`, q: "orders-api işlemcisi zorlanıyor mu"},
		{name: "kabul: çitli JSON", raw: "```json\n" + rcJSON("resource_investigation", "orders-api", "service", "cpu", 0, 0.9) + "\n```",
			q: "orders-api işlemcisi zorlanıyor mu", wantOK: true, intent: guidedResourceInvestigation, service: "orders-api", target: "orders-api", kind: resKindService, family: resFamCPU},
		{name: "ret: none", raw: rcJSON("none", "", "", "all", 0, 0.9), q: "insan belleği nasıl çalışır"},
		{name: "ret: beyaz liste dışı niyet", raw: rcJSON("service_health", "orders-api", "service", "cpu", 0, 0.9), q: "orders-api işlemcisi zorlanıyor mu"},
		{name: "geçersiz aile → jeton ailesi", raw: rcJSON("resource_investigation", "orders-api", "service", "disk", 0, 0.9),
			q: "orders-api belleği zorlanıyor mu", wantOK: true, intent: guidedResourceInvestigation, service: "orders-api", target: "orders-api", kind: resKindService, family: resFamMemory},
		{name: "grafik fiili metric_chart'ı kazandırır (aile → şablon)", raw: rcJSON("resource_investigation", "orders-api", "service", "cpu", 0, 0.9),
			q: "orders-api işlemci yükünün grafiğini çiz", wantOK: true, intent: guidedMetricChart, service: "orders-api", template: mcCPU},
		{name: "ret: fiilsiz metric_chart", raw: rcJSON("metric_chart", "orders-api", "service", "cpu", 0, 0.9), q: "orders-api işlemcisi zorlanıyor mu"},
		{name: "ret: fiilli ama şablonsuz aile (restart)", raw: rcJSON("metric_chart", "orders-api", "service", "restarts", 0, 0.9), q: "orders-api kapanıp açılmalarını çiz"},
		{name: "kabul: pod_health", raw: rcJSON("pod_health", "checkout-service", "service", "all", 0, 0.75),
			q: "checkout-service pod'ları ayakta mı", wantOK: true, intent: guidedPodHealth, service: "checkout-service"},
		{name: "veto: router vetosu (log şekli)", raw: rcJSON("resource_investigation", "orders-api", "service", "memory", 0, 0.9),
			q: "orders-api loglarında bellek hatası var mı"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route, rangeS, why, ok := decideResourceClassify(tc.raw, tc.q, resClassifyEnv{Services: rcServices, CtxService: tc.ctxSvc, PodsExist: tc.pods,
				// v0.10.2239 (S4): sahip / iş yükü resolver'dan — sahte komşuluk.
				PodOwner: func(pod string) string {
					if pod == rcPod {
						return "orders-api"
					}
					return ""
				},
				WorkloadKnown: func(name string) bool {
					if tc.pods == nil {
						return false
					}
					found, _ := tc.pods(name)
					return found
				}})
			if ok != tc.wantOK {
				t.Fatalf("ok=%v (why=%q), want %v; route=%+v", ok, why, tc.wantOK, route)
			}
			if !ok {
				if why == "" {
					t.Error("ret sebebi boş — panelde görünmeli")
				}
				return
			}
			if route.Intent != tc.intent || route.Service != tc.service || route.ResTarget != tc.target || route.ResKind != tc.kind ||
				route.ResFamily != tc.family || route.ChartTemplate != tc.template || rangeS != tc.rangeS {
				t.Fatalf("route=%+v rangeS=%d, want intent=%s svc=%q target=%q kind=%q fam=%q tmpl=%q range=%d",
					route, rangeS, tc.intent, tc.service, tc.target, tc.kind, tc.family, tc.template, tc.rangeS)
			}
			if route.ChartPromQL != "" {
				t.Fatalf("serbest PromQL rotaya sızdı: %q", route.ChartPromQL)
			}
		})
	}
}

// Serbest PromQL yok: şema yalnız altı alan; modelin eklediği sorgu alanı
// hiçbir yere taşınmaz.
func TestResourceClassifyNoFreePromQL(t *testing.T) {
	props := resourceClassifySchema()["properties"].(map[string]any)
	want := map[string]bool{"intent": true, "target": true, "targetKind": true, "family": true, "rangeS": true, "confidence": true}
	if len(props) != len(want) {
		t.Fatalf("şema alanları %v", props)
	}
	for k := range props {
		if !want[k] {
			t.Errorf("şemada beklenmeyen alan %q", k)
		}
	}
	enum := props["intent"].(map[string]any)["enum"].([]string)
	if strings.Join(enum, ",") != "resource_investigation,metric_chart,pod_health,none" {
		t.Fatalf("niyet beyaz listesi %v", enum)
	}
	for _, e := range enum {
		if !strings.Contains(copilot.SystemPromptResourceClassify(), "- "+e+":") {
			t.Errorf("%q istemde anlatılmamış", e)
		}
	}
	raw := `{"intent":"resource_investigation","target":"orders-api","targetKind":"service","family":"cpu","rangeS":0,"confidence":0.9,"promql":"sum(rate(x[5m]))","query":"up"}`
	route, _, _, ok := decideResourceClassify(raw, "orders-api işlemcisi zorlanıyor mu", resClassifyEnv{Services: rcServices})
	if !ok || route.ChartPromQL != "" || route.ChartTemplate != "" {
		t.Fatalf("ek sorgu alanı rotaya taşındı: %+v ok=%v", route, ok)
	}
}

func TestHasResourceNoun(t *testing.T) {
	for q, want := range map[string]bool{
		"orders-api'nin işlemcisi zorlanıyor mu": true,
		"payments hafızası şişti mi":             true,
		"orders pod'u sürekli kapanıp açılıyor":  true,
		"konteyner kapasitesi yetiyor mu":        true,
		"checkout heap durumu":                   true,
		"açık problemler neler":                  false,
		"checkout servisi son 1 saatte nasıl":    false,
		"türkiyenin başkenti neresi":             false,
		"checkout'tan ledger'a giden istekler":   false,
	} {
		if got := hasResourceNoun(guidedTokens(normalizeGuidedMsg(q))); got != want {
			t.Errorf("hasResourceNoun(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestResClassifyCacheKeyHashesAllInputs(t *testing.T) {
	base := resClassifyCacheKey("orders-api cpu", "payments", "SYS")
	if !strings.HasPrefix(base, "cosre-rclass:v1:") {
		t.Fatalf("önek %q", base)
	}
	for name, k := range map[string]string{
		"soru değişti":          resClassifyCacheKey("orders-api bellek", "payments", "SYS"),
		"ekran servisi değişti": resClassifyCacheKey("orders-api cpu", "checkout", "SYS"),
		"ekran servisi yok":     resClassifyCacheKey("orders-api cpu", "", "SYS"),
		"istem değişti":         resClassifyCacheKey("orders-api cpu", "payments", "SYS2"),
		"sınır kayması":         resClassifyCacheKey("orders-api cpup", "ayments", "SYS"),
	} {
		if k == base {
			t.Errorf("%s ama anahtar aynı", name)
		}
	}
	if resClassifyCacheKey("orders-api cpu", "payments", "SYS") != base {
		t.Error("anahtar kararsız")
	}
	// Normalize: büyük harf / fazla boşluk aynı soru (modele giden metin de aynı).
	if resClassifyNormalize("  Orders-API   CPU \n") != "orders-api cpu" {
		t.Errorf("normalize %q", resClassifyNormalize("  Orders-API   CPU \n"))
	}
	if r := []rune(resClassifyNormalize(strings.Repeat("ç", 4000))); len(r) != resClassifyMaxRunes {
		t.Errorf("kırpma %d", len(r))
	}
}

// fakeJSONProvider — OpenAI uyumlu sahte uç; içerik ayarlanabilir JSON.
type fakeJSONProvider struct {
	srv     *httptest.Server
	mu      sync.Mutex
	content string
	calls   int
	users   []string
}

func newFakeJSONProvider(t *testing.T, content string) *fakeJSONProvider {
	t.Helper()
	f := &fakeJSONProvider{content: content}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.calls++
		for _, m := range body.Messages {
			if m.Role == "user" {
				f.users = append(f.users, m.Content)
			}
		}
		content := f.content
		f.mu.Unlock()
		out, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 7, "completion_tokens": 3},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJSONProvider) set(c string) { f.mu.Lock(); f.content = c; f.mu.Unlock() }
func (f *fakeJSONProvider) n() int       { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func rcServer(t *testing.T, fp *fakeJSONProvider, rec copilot.Recorder) *Server {
	t.Helper()
	cop := copilot.New(copilot.ProviderOpenAI, "test-key", "gemma4")
	cop.Configure(copilot.ProviderOpenAI, "test-key", "gemma4", fp.srv.URL, false, true)
	if rec != nil {
		cop.SetRecorder(rec)
	}
	if !cop.Active() {
		t.Fatal("sahte copilot aktif değil")
	}
	return &Server{copilot: cop, cache: newMemCache()}
}

type rcEvents struct {
	mu  sync.Mutex
	evs []string
}

func (e *rcEvents) emit(kind string, v any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := v.(map[string]any); ok && kind == "step" {
		m["i"] = len(e.evs) + 1 // sohbet akışının sarmalayıcısı gibi adım kimliği basar
	}
	b, _ := json.Marshal(v)
	e.evs = append(e.evs, kind+" "+string(b))
}

func (e *rcEvents) joined() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.evs, "\n")
}

// Uçtan uca (sahte sağlayıcı): kabul / düşük güven / hedef vetosu / bozuk
// JSON; önbellek isabeti modeli çağırmaz; bozuk çıktı önbelleğe yazılmaz;
// ai_calls satırı chat-resource-classify yüzeyiyle ve çocuk exchange'le.
func TestResourceClassifyFallbackFakeProvider(t *testing.T) {
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "0123456789abcdef0123456789abcdef", UserID: "u1"})
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 16)}
	fp := newFakeJSONProvider(t, rcJSON("resource_investigation", "orders-api", "service", "cpu", 3600, 0.85))
	s := rcServer(t, fp, rec)
	ev := &rcEvents{}
	const q = "orders-api'nin işlemcisi zorlanıyor mu?"

	route, rangeS, ok := s.resourceClassifyFallback(ctx, ev.emit, q, "", rcServices, time.Time{})
	if !ok || route.Intent != guidedResourceInvestigation || route.Service != "orders-api" || route.ResFamily != resFamCPU || rangeS != 3600 {
		t.Fatalf("kabul bekleniyordu: ok=%v route=%+v range=%d", ok, route, rangeS)
	}
	if fp.n() != 1 {
		t.Fatalf("model çağrısı %d", fp.n())
	}
	r := rec.next(t)
	if r.Surface != aisurface.ChatResourceClassify || r.ExchangeID != "0123456789abcdef0123456789abcdef:"+aisurface.ChatResourceClassify {
		t.Fatalf("ai_calls satırı surface=%q xid=%q", r.Surface, r.ExchangeID)
	}
	if sp, ok := aisurface.Lookup(aisurface.ChatResourceClassify); !ok || sp.Role != aisurface.RoleSubCall {
		t.Fatalf("yüzey kaydı %+v %v", sp, ok)
	}
	if j := ev.joined(); !strings.Contains(j, `"tool":"resource_classify"`) || !strings.Contains(j, "step-result") {
		t.Fatalf("adım çipi/sonucu yayınlanmadı:\n%s", j)
	}
	// Aynı soru (büyük harf/boşluk farkıyla) → önbellek; model çağrılmaz.
	if _, _, ok := s.resourceClassifyFallback(ctx, ev.emit, "  ORDERS-API'nin   işlemcisi zorlanıyor mu?", "", rcServices, time.Time{}); !ok || fp.n() != 1 {
		t.Fatalf("önbellek isabeti modeli çağırdı (calls=%d ok=%v)", fp.n(), ok)
	}
	// Ekran servisi farklı → farklı anahtar → model yeniden.
	if _, _, ok := s.resourceClassifyFallback(ctx, ev.emit, q, "payments", rcServices, time.Time{}); !ok || fp.n() != 2 {
		t.Fatalf("ekran servisi anahtara girmedi (calls=%d)", fp.n())
	}
	rec.next(t)

	cases := []struct {
		name, content, q string
	}{
		{"düşük güven", rcJSON("resource_investigation", "orders-api", "service", "memory", 0, 0.3), "orders-api hafızası şişti mi"},
		{"bilinmeyen hedef vetosu", rcJSON("resource_investigation", "billing-api", "service", "memory", 0, 0.95), "billing-api hafızası şişti mi"},
		{"bozuk JSON", `{"intent":"resource_investigation",`, "ledger-service hafızası şişti mi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp.set(tc.content)
			before := fp.n()
			if _, _, ok := s.resourceClassifyFallback(ctx, ev.emit, tc.q, "", rcServices, time.Time{}); ok {
				t.Fatal("kabul edilmemeliydi")
			}
			rec.next(t)
			if !strings.Contains(ev.joined(), "kaynak sınıflandırıcısı reddetti") {
				t.Error("ret sebebi panelde yok")
			}
			_, _, _ = s.resourceClassifyFallback(ctx, ev.emit, tc.q, "", rcServices, time.Time{})
			wantCalls := before + 1 // geçerli JSON önbellekte (ret kararı tekrar hesaplanır, model çağrılmaz)
			if tc.name == "bozuk JSON" {
				wantCalls = before + 2 // bozuk çıktı önbelleğe yazılmaz
				rec.next(t)
			}
			if fp.n() != wantCalls {
				t.Fatalf("model çağrısı %d, want %d", fp.n(), wantCalls)
			}
		})
	}

	// Kaynak ismi yok → sınıflandırıcı hiç koşmaz.
	before := fp.n()
	if _, _, ok := s.resourceClassifyFallback(ctx, ev.emit, "açık problemler neler", "", rcServices, time.Time{}); ok || fp.n() != before {
		t.Fatalf("kaynak isimsiz soruda model çağrıldı (calls %d→%d)", before, fp.n())
	}
	// Modele giden kullanıcı bloğu normalize soru + ekran servisi.
	fp.mu.Lock()
	users := append([]string(nil), fp.users...)
	fp.mu.Unlock()
	if len(users) < 2 || users[0] != resClassifyUser(resClassifyNormalize(q), "") || !strings.Contains(users[1], "Ekrandaki servis: payments") {
		t.Fatalf("kullanıcı blokları %q", users)
	}
}

// Genel sınıflandırıcı: resource_investigation beyaz listede ve servis ister;
// aile/drill jetonlardan dolar.
func TestIntentResourceInvestigationAllowed(t *testing.T) {
	route, _, ok := parseIntentJSON(`{"intent":"resource_investigation","service":"orders-api"}`, rcServices, nil, nil, "")
	if !ok || route.Intent != guidedResourceInvestigation || route.Service != "orders-api" {
		t.Fatalf("route=%+v ok=%v", route, ok)
	}
	if r, _, ok := parseIntentJSON(`{"intent":"resource_investigation"}`, rcServices, nil, nil, ""); !ok || r.Intent != guidedAskService || r.AskIntent != guidedResourceInvestigation {
		t.Fatalf("servissiz → ask_service bekleniyordu: %+v", r)
	}
	if r, _, ok := parseIntentJSON(`{"intent":"resource_investigation"}`, rcServices, nil, nil, "payments"); !ok || r.Service != "payments" {
		t.Fatalf("ekran servisi devralınmadı: %+v", r)
	}
	e := intentResourceEnrich(route, "orders-api heap ve bellek durumu prod-a cluster'ında yavaş mı")
	if e.ResFamily != resFamAll || e.ResDrillAsk != resDrillAskJVM || e.ResCluster != "prod-a" {
		t.Fatalf("zenginleştirme %+v", e)
	}
	if e := intentResourceEnrich(guidedRoute{Intent: guidedResourceInvestigation, Service: "x"}, "x nasıl"); e.ResFamily != resFamAll {
		t.Fatalf("varsayılan aile all olmalı: %+v", e)
	}
	if e := intentResourceEnrich(guidedRoute{Intent: guidedPodHealth}, "x cpu"); e.ResFamily != "" {
		t.Fatalf("başka niyete dokunuldu: %+v", e)
	}
}

// Kaynak kapısı: ön-sınıflandırıcı niyet kademesinin İÇİNDE, genel
// sınıflandırıcı çağrısından ÖNCE; iki yol aynı kuyruğa (intentDispatch).
func TestResourceClassifyInsideIntentTier(t *testing.T) {
	b, err := os.ReadFile("copilot_intent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	fn := strings.Index(src, "func (s *Server) copilotChatIntent(")
	fb := strings.Index(src, "s.resourceClassifyFallback(ctx, emit,")
	gen := strings.Index(src, `s.copilotExplainJSONSurface(cctx, "chat-intent"`)
	if fn < 0 || fb < 0 || gen < 0 || !(fn < fb && fb < gen) {
		t.Fatalf("ön-sınıflandırıcı genel sınıflandırıcıdan önce değil: fn=%d fb=%d gen=%d", fn, fb, gen)
	}
	if n := strings.Count(src, "s.intentDispatch(ctx, emit,"); n != 2 {
		t.Fatalf("intentDispatch çağrısı %d, want 2 (ön-sınıflandırıcı + genel)", n)
	}
	cb, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cb), "resourceClassify") {
		t.Fatal("ön-sınıflandırıcı yeni kademe olarak copilot_chat.go'ya girmiş (kademe sırası pinli)")
	}
}
