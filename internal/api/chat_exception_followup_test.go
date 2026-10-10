package api

// chat_exception_followup_test.go — v0.10.1053 (operatör: "Exception panelindeki
// takip sohbeti de kod okuyabilsin."): exception öznesinin panel takibinde
// read_source_code. Gerçek copilotChat + gerçek copilot.Service (sahte model,
// loopLLM) + GERÇEK araç handler'ı + gerçek devops.Service (sahte git, scGit).
// Store'suz sunucu: exception grubu ve explain girdisi exceptionInputSeam'den.
//
// Pinlenen sözleşmeler:
//   - Yönlendirme: araç sunulabiliyorsa modele YALNIZ read_source_code (dış MCP
//     ve öteki yerli araçlar yok); prompt = ek + çekmece çekirdeği, kullanıcı
//     bloğu çekmece anlatımının aynısı.
//   - Sunulamıyorsa (DevOps yok, token / kimliksiz çağıran, aracın rol kapısı)
//     ya da grup okunamadıysa tek AKAN anlatım çağrısı, prompt bayt bayt eski
//     (golden).
//   - Düşüş: döngü cevap üretemezse (araç tanımını reddeden 400, küçültülemeyen
//     taşma) hata olayı yok, tek düz çip + aynı anlatım çağrısı; istemci iptali
//     düşmez.
//   - Kapsam: grubun servisi + stack'i basan servis; örnek trace'teki öteki
//     servisler dahil başka her servis git'e istek atmadan reddedilir; okuyucu
//     okunamayan grupta "exception okunamadı" ile savunur. Sunulmayan yerli
//     araç reddedilir, yürütülmez.
//   - Sürüm: yalnız stack'i veren olayın sürümü (StackVersion) ve yalnız o
//     servis için; öteki servis dal.
//   - Sızıntı yok: kod SSE'ye, ai_calls'a, audit'e, span'lere girmez.
//   - Sayı denetimi YOK (çekmecenin cevap sözleşmesi): açıklamadaki sayı ve kod
//     sabiti uyarı kuyruğu almaz.
//   - Maliyet: özne alışveriş başına BİR kez yüklenir (çekmecenin kanıt okuması).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcpclient"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	exFP       = "fp-exc-7713"
	exGroupSvc = "payments"
	exStackSvc = "ledger-writer"
	exExplain  = "KONU: Exception · payments\n**Bulgu** — ChargeHandler.java:120 satırında NullPointerException."
	exSeed     = "Bu exception grubunun kök nedeni ne? (fp-exc-7713)"
	exQuestion = "ChargeHandler'da 120. satırın çevresinde ne var?"
	// exUser — sahte explain girdisinin HAM KANIT'ı (occurrences 523 sayı
	// denetiminde kanıt sayılmalı).
	exUser = "Exception GRUBU:\n```json\n{\"occurrences\":523,\"service\":\"payments\",\"type\":\"java.lang.NullPointerException\"}\n```\n\n" +
		"Temsilî STACKTRACE:\n```\njava.lang.NullPointerException\n\tat com.example.cards.ChargeHandler.charge(ChargeHandler.java:120)\n```"
)

// exGoldenUser — BUGÜNKÜ çekmece anlatımının kullanıcı bloğu, elle yazılmış
// (drawerNarrationUser'dan türetilmedi: golden'ın işi o yolu da pinlemek).
const exGoldenUser = "EKRANDAKİ AÇIKLAMA (operatörün az önce okuduğu CoSRE cevabı):\n" +
	"KONU: Exception · payments\n**Bulgu** — ChargeHandler.java:120 satırında NullPointerException.\n\n" +
	"HAM KANIT (bu açıklamanın dayandığı veri — açıklamada geçmeyen ayrıntılar için BURAYA bak):\n" +
	"Exception GRUBU:\n```json\n{\"occurrences\":523,\"service\":\"payments\",\"type\":\"java.lang.NullPointerException\"}\n```\n\n" +
	"Temsilî STACKTRACE:\n```\njava.lang.NullPointerException\n\tat com.example.cards.ChargeHandler.charge(ChargeHandler.java:120)\n```\n\n" +
	"KONUŞMA (K: operatör, C: sen):\n" +
	"K: Bu exception grubunun kök nedeni ne? (fp-exc-7713)\n\n" +
	"SORU: ChargeHandler'da 120. satırın çevresinde ne var?"

// exSeamLoads — dikişin kaç kez çağrıldığı (alışveriş başına yükleme sayısı).
type exSeamLoads struct {
	mu sync.Mutex
	n  int
}

func (l *exSeamLoads) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// installExceptionInput — store'suz sunucuda exception grubu + explain girdisi.
// in nil → grup okunamıyor.
func installExceptionInput(t *testing.T, in *anomaly.ExceptionExplainInput) *exSeamLoads {
	t.Helper()
	loads := &exSeamLoads{}
	exceptionInputSeam = func(_ context.Context, fp string, _ *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, error) {
		loads.mu.Lock()
		loads.n++
		loads.mu.Unlock()
		if in == nil || fp != exFP {
			return nil, anomaly.ExceptionExplainInput{}, errors.New("exception_groups okunamadı")
		}
		return &chstore.ExceptionGroup{Fingerprint: exFP, Service: exGroupSvc, Type: "java.lang.NullPointerException"}, *in, nil
	}
	t.Cleanup(func() { exceptionInputSeam = nil })
	return loads
}

// exInput — explain girdisi: stack bir ÖRNEKTEN (StackService boş) ya da
// log-fallback'ten (StackService = logu atan servis); sürüm o olayın.
func exInput(stackSvc, stackVersion string) *anomaly.ExceptionExplainInput {
	return &anomaly.ExceptionExplainInput{User: exUser, StackService: stackSvc, StackVersion: stackVersion, TraceID: tfTrace}
}

// exceptionDrawerBody — exception "Explain"inden açılan panelin takip gövdesi
// (AIDrawerBody.tsx: seed + soru, explain, subject; trace/page YOK).
func exceptionDrawerBody(question, conv string) map[string]any {
	return exceptionDrawerBodyWith(exExplain, question, conv)
}

func exceptionDrawerBodyWith(explain, question, conv string) map[string]any {
	return map[string]any{
		"messages": []map[string]any{
			{"role": "user", "text": exSeed},
			{"role": "user", "text": question},
		},
		"context": map[string]any{
			"explain": explain, "subject": "exception:" + exFP, "conversation": conv,
		},
	}
}

// newExLLM — loopLLM'in durum kodu seçebilen ikizi: araç tanımı taşıyan
// istekte onTools(n) (durum, gövde) döner; araçsız istek (çekmecenin akan
// anlatımı) "answer" cevabını alır. hook — istek gelince yan etki (iptal).
func newExLLM(t *testing.T, onTools func(n int) (int, any), answer string, hook func()) *loopLLM {
	t.Helper()
	l := &loopLLM{}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		l.mu.Lock()
		n := len(l.bodies)
		l.bodies = append(l.bodies, body)
		l.mu.Unlock()
		if hook != nil {
			hook()
		}
		w.Header().Set("Content-Type", "application/json")
		if _, ok := body["tools"]; ok {
			status, out := onTools(n)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": answerMsg(answer), "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
		})
	}))
	t.Cleanup(l.srv.Close)
	return l
}

// exPostCtx — scPost'un ctx alan ikizi (istemci iptali testi).
func exPostCtx(t *testing.T, s *Server, ctx context.Context, uid, role string, body map[string]any) []chatFrame {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/chat", strings.NewReader(string(raw)))
	r = r.WithContext(auth.ContextWithClaims(ctx, &auth.Claims{UserID: uid, Email: uid + "@example.test", Role: role}))
	w := httptest.NewRecorder()
	s.copilotChat(w, r)
	var out []chatFrame
	for _, block := range strings.Split(strings.TrimSpace(w.Body.String()), "\n\n") {
		f := chatFrame{}
		var data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				f.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if f.event == "" || data == "" {
			continue
		}
		if err := json.Unmarshal([]byte(data), &f.data); err != nil {
			t.Fatalf("çerçeve JSON değil: %q", data)
		}
		out = append(out, f)
	}
	return out
}

// stepTexts — step çerçevelerinin etiketi ya da çip adı (guided/çekmece
// çipleri adı `tool` alanında taşır).
func stepTexts(fr []chatFrame) []string {
	var out []string
	for _, st := range framesOf(fr, "step") {
		for _, k := range []string{"label", "tool"} {
			if v, ok := st[k].(string); ok && v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func llmUser(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	if len(msgs) < 2 {
		return ""
	}
	m, _ := msgs[1].(map[string]any)
	s, _ := m["content"].(string)
	return s
}

// ── 1. yönlendirme: sunulabiliyorsa TEK araç ──────────────────────────────

func TestExceptionFollowUpOffersOnlySourceCode(t *testing.T) {
	run := func(uid, role string) (map[string]any, []chatFrame) {
		llm := newLoopLLM(t, func(int, map[string]any) map[string]any {
			return answerMsg("ChargeHandler.java:120 null kontrolü yok.")
		})
		s, _ := newLoopTestServer(t, llm, uid)
		_, s.devops = newSCGit(t)
		// Dış MCP istemcisi bağlı: exception takibine YİNE sızmamalı.
		srv, extCalls := newFakeTrackerMCP(t)
		mc := mcpclient.NewService()
		mc.Configure(mcpclient.Settings{Servers: []mcpclient.ServerConfig{{Name: "tracker", Transport: "http", URL: srv.URL, Enabled: true}}})
		t.Cleanup(mc.Close)
		s.mcpClient = mc
		installExceptionInput(t, exInput("", ""))
		frames, _ := scPost(t, s, uid, role, exceptionDrawerBody(exQuestion, "conv-ex-1"))
		if n := extCalls(); n != 0 {
			t.Fatalf("dış MCP aracı çağrıldı (%d)", n)
		}
		calls := llm.calls()
		if len(calls) != 1 {
			t.Fatalf("model %d kez çağrıldı", len(calls))
		}
		return calls[0], frames
	}
	for _, role := range []string{auth.RoleEditor, auth.RoleAdmin} {
		body, frames := run("u-a", role)
		if names := llmToolNames(body); len(names) != 1 || names[0] != mcptools.SourceCodeToolName {
			t.Fatalf("%s: model YALNIZ read_source_code görmeli: %v", role, names)
		}
		sys := llmSystem(body)
		if sys != copilot.SourceCodeChatAddendum()+"\n\n"+copilot.SystemPromptDrawerChat() {
			t.Fatalf("%s: sistem mesajı = ek + çekmece çekirdeği olmalı:\n%s", role, sys)
		}
		if got := llmUser(body); got != exGoldenUser {
			t.Fatalf("%s: kullanıcı bloğu çekmece anlatımınınkinden saptı:\n%q", role, got)
		}
		labels := strings.Join(stepTexts(frames), " | ")
		for _, want := range []string{"bağlam: ekrandaki AI açıklaması", "kanıt: exception grubu + örnek trace + loglar", exceptionCodeBudgetLabelTR(chatMaxToolCalls)} {
			if !strings.Contains(labels, want) {
				t.Errorf("%s: adım %q yok: %s", role, want, labels)
			}
		}
		if ans := answerText(t, frames); !strings.HasSuffix(ans, drawerSourceNote("exception")) {
			t.Errorf("%s: künye çekmeceninki olmalı: %q", role, ans)
		}
	}
	// Rol kapısı aracın KENDİ MinRole'ü — sabit varsayılmaz (viewer'ın görüp
	// görmediği SourceCodeMinRole'den türer).
	body, _ := run("u-a", auth.RoleViewer)
	if got, want := hasToolName(llmToolNames(body), mcptools.SourceCodeToolName), roleSatisfies(auth.RoleViewer, mcptools.SourceCodeMinRole); got != want {
		t.Fatalf("viewer: sunuldu=%v, aracın rol kapısına göre beklenen %v", got, want)
	}
}

// ── 2. sunulamıyorsa: tek anlatım çağrısı, bayt bayt eski ─────────────────

func TestExceptionFollowUpFallbackByteIdentical(t *testing.T) {
	cases := []struct {
		name, uid, role string
		devops          func(t *testing.T) *devops.Service
	}{
		{"DevOps yok", "u-a", auth.RoleAdmin, func(*testing.T) *devops.Service { return nil }},
		{"DevOps yapılandırılmamış", "u-a", auth.RoleAdmin, func(*testing.T) *devops.Service { return devops.New() }},
		{"API token'ı (admin)", "token:t1", auth.RoleAdmin, func(t *testing.T) *devops.Service { _, dv := newSCGit(t); return dv }},
		{"API token'ı (editor)", "token:t2", auth.RoleEditor, func(t *testing.T) *devops.Service { _, dv := newSCGit(t); return dv }},
		{"kimliksiz", " ", auth.RoleAdmin, func(t *testing.T) *devops.Service { _, dv := newSCGit(t); return dv }},
	}
	if !roleSatisfies(auth.RoleViewer, mcptools.SourceCodeMinRole) {
		// Aracın rol kapısı viewer'ı dışarıda bırakıyorsa o da eski yol.
		cases = append(cases, struct {
			name, uid, role string
			devops          func(t *testing.T) *devops.Service
		}{"rol kapısı altında", "u-a", auth.RoleViewer, func(t *testing.T) *devops.Service { _, dv := newSCGit(t); return dv }})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
			s, _ := newLoopTestServer(t, llm, c.uid)
			s.devops = c.devops(t)
			loads := installExceptionInput(t, exInput("", "1.4.2"))
			frames, _ := scPost(t, s, c.uid, c.role, exceptionDrawerBody(exQuestion, "conv-ex-fb"))
			calls := llm.calls()
			if len(calls) != 1 {
				t.Fatalf("tek anlatım çağrısı bekleniyordu, %d", len(calls))
			}
			if _, ok := calls[0]["tools"]; ok {
				t.Fatal("eski yol araç kataloğu göndermez")
			}
			if calls[0]["stream"] != true {
				t.Fatal("eski yol AKAN anlatım çağrısıdır (stream=true)")
			}
			if sys := llmSystem(calls[0]); sys != copilot.SystemPromptDrawerChat() {
				t.Fatalf("sistem mesajı bayt bayt SystemPromptDrawerChat olmalı:\n%s", sys)
			}
			if got := llmUser(calls[0]); got != exGoldenUser {
				t.Fatalf("kullanıcı bloğu golden'dan saptı:\n%q", got)
			}
			// Olay dizisi de eskisi: bağlam çipi + kanıt çipi/kanıtı + cevap.
			var kinds []string
			for _, f := range frames {
				kinds = append(kinds, f.event)
			}
			if got := strings.Join(kinds, ","); got != "step,step,step-result,answer,done" {
				t.Fatalf("olay dizisi değişti: %s", got)
			}
			for _, l := range stepLabels(frames) {
				if strings.HasPrefix(l, "kod okuma") {
					t.Fatalf("eski yolda döngü etiketi: %q", l)
				}
			}
			if ans := answerText(t, frames); ans != "tamam"+drawerSourceNote("exception") {
				t.Fatalf("cevap: %q", ans)
			}
			if n := loads.count(); n != 1 {
				t.Fatalf("özne %d kez yüklendi", n)
			}
		})
	}
}

// ── 3. sızıntı yok + maliyet ──────────────────────────────────────────────

func TestExceptionFollowUpNoLeakEndToEnd(t *testing.T) {
	llm := newLoopLLM(t, scToolCall(`{"service":"payments","file":"ChargeHandler.java","line":120,"context_lines":5}`,
		"ChargeHandler.java:120 satırında ledger.post çağrılıyor."))
	s, git := scServer(t, llm, nil)
	loads := installExceptionInput(t, exInput("", ""))
	rec := newCapRecorder()
	s.copilot.SetRecorder(rec)
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	s.tracer = tp.Tracer("test")

	frames, stream := scPost(t, s, "u-a", auth.RoleEditor, exceptionDrawerBody(exQuestion, "conv-ex-leak"))

	calls := llm.calls()
	if len(calls) != 2 {
		t.Fatalf("model %d kez çağrıldı (araç turu + cevap bekleniyordu)", len(calls))
	}
	if !strings.Contains(llmMessagesText(calls[1]), "120|         ledger.post(card, 4711); // "+scLeakMarker) {
		t.Fatal("kod modele ulaşmadı — test hiçbir şey ölçmüyor")
	}
	if len(git.requests()) == 0 {
		t.Fatal("git sunucusuna istek gitmedi")
	}
	if n := loads.count(); n != 1 {
		t.Fatalf("özne alışveriş başına bir kez yüklenmeli, %d", n)
	}
	if strings.Contains(stream, scLeakMarker) {
		t.Fatalf("SSE akışı kod taşıyor:\n%s", stream)
	}
	var res map[string]any
	for _, f := range framesOf(frames, "step-result") {
		if f["tool"] == mcptools.SourceCodeToolName {
			res = f
		}
	}
	if res == nil || res["ok"] != true {
		t.Fatalf("read_source_code step-result yok/başarısız: %v", framesOf(frames, "step-result"))
	}
	if prev, _ := res["preview"].(string); !strings.HasPrefix(prev, "src/main/java/com/example/cards/ChargeHandler.java:115-125 · release (dal)") ||
		!strings.Contains(prev, "tarayıcıya gönderilmez") {
		t.Fatalf("önizleme yalnız referans olmalı: %q", prev)
	}
	if ans := answerText(t, frames); !strings.HasSuffix(ans, drawerSourceNote("exception")+" + read_source_code (kaynak kod)") {
		t.Fatalf("künye kod okumasını adlandırmalı: %q", ans)
	}
	// v0.10.1153 — model çağrısı + turun etkileşim satırı; ikisi de kod taşımaz.
	last := modelCallAndTurnNoLeak(t, rec.wait(t, 2), scLeakMarker)
	if last.Surface != exceptionCodeSurface || !strings.Contains(last.PromptSample, "[kod: payments/src/main/java/com/example/cards/ChargeHandler.java:115-125 · 11 satır · release (dal)]") {
		t.Fatalf("ai_calls: yüzey %q, örnek %q", last.Surface, last.PromptSample)
	}
	sawAudit := false
	for done := false; !done; {
		select {
		case e := <-s.auditQ:
			if e.TargetID == mcptools.SourceCodeToolName {
				sawAudit = true
			}
			if strings.Contains(e.Details, scLeakMarker) {
				t.Fatalf("audit kodu taşıyor: %s", e.Details)
			}
		default:
			done = true
		}
	}
	if !sawAudit {
		t.Fatal("araç çağrısı audit'e yazılmadı (kurgu)")
	}
	sawTool := false
	for _, sp := range exp.GetSpans() {
		if sp.Name == "ai.tool" {
			sawTool = true
		}
		for _, kv := range sp.Attributes {
			if strings.Contains(kv.Value.String(), scLeakMarker) {
				t.Fatalf("span %s özniteliği kod taşıyor", sp.Name)
			}
		}
		for _, ev := range sp.Events {
			for _, kv := range ev.Attributes {
				if strings.Contains(kv.Value.String(), scLeakMarker) {
					t.Fatalf("span %s olayı kod taşıyor", sp.Name)
				}
			}
		}
	}
	if !sawTool {
		t.Fatal("ai.tool span'ı yok (kurgu)")
	}
}

// Sayı denetimi YOK: çekmece anlatımının cevap sözleşmesi. Açıklamadaki bir
// sayıyı (97) ve ekin istediği gibi aynen aktarılan kod sabitini (4711)
// tekrarlayan cevap "kanıtta bulunamayan sayı" kuyruğu ALMAZ — almasaydı yalnız
// DevOps'lu kurulumlarda yanlış uyarı çıkardı.
func TestExceptionFollowUpNoNumericTail(t *testing.T) {
	llm := newLoopLLM(t, scToolCall(`{"service":"payments","file":"ChargeHandler.java","line":120,"context_lines":5}`,
		"Açıklamadaki gibi son 24 saatte 97 kez görüldü; 120. satırdaki limit 4711."))
	s, _ := scServer(t, llm, nil)
	installExceptionInput(t, exInput("", ""))
	explain := "KONU: Exception · payments\n**Bulgu** — son 24 saatte 97 kez; ChargeHandler.java:120 NullPointerException."
	frames, _ := scPost(t, s, "u-a", auth.RoleEditor, exceptionDrawerBodyWith(explain, "Limit kaç?", "conv-ex-num"))
	if !strings.Contains(llmMessagesText(llm.calls()[1]), "4711") {
		t.Fatal("kurgu: sabit koddan modele gitmeli")
	}
	ans := answerText(t, frames)
	if w := numericWarningLine(ans); w != "" || strings.Contains(ans, traceFollowUpNumericMarker) {
		t.Fatalf("exception takibinde sayı uyarısı olmamalı: %q", ans)
	}
	if !strings.HasPrefix(ans, "Açıklamadaki gibi son 24 saatte 97 kez görüldü; 120. satırdaki limit 4711.") {
		t.Fatalf("cevap metni aynen kalmalı: %q", ans)
	}
}

// ── 3b. döngü cevap üretemezse bugünkü anlatıma düşer ─────────────────────

func TestExceptionFollowUpFallsBackToNarration(t *testing.T) {
	setup := func(t *testing.T, llm *loopLLM) (*Server, *capRecorder) {
		s, _ := newLoopTestServer(t, llm, "u-a")
		_, s.devops = newSCGit(t)
		rec := newCapRecorder()
		s.copilot.SetRecorder(rec)
		installExceptionInput(t, exInput("", ""))
		return s, rec
	}
	for _, c := range []struct {
		name string
		body any
	}{
		// Araç desteksiz yerel uç: tool tanımlı isteğe 400.
		{"araç tanımı reddedildi", map[string]any{"error": map[string]any{"message": "tools are not supported by this model", "type": "invalid_request_error"}}},
		// İlk turda taşma: küçültülecek araç turu yok.
		{"küçültülemeyen taşma", map[string]any{"error": map[string]any{"message": "This model's maximum context length is 8192 tokens; please reduce the length of the messages."}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			llm := newExLLM(t, func(int) (int, any) { return http.StatusBadRequest, c.body }, "tamam", nil)
			s, rec := setup(t, llm)
			frames, _ := scPost(t, s, "u-a", auth.RoleEditor, exceptionDrawerBody(exQuestion, "conv-ex-fall"))
			calls := llm.calls()
			if len(calls) != 2 {
				t.Fatalf("araçlı tur + araçsız anlatım bekleniyordu, %d çağrı", len(calls))
			}
			if _, ok := calls[0]["tools"]; !ok {
				t.Fatal("kurgu: ilk çağrı araçlı döngü turu olmalı")
			}
			nar := calls[1]
			if _, ok := nar["tools"]; ok || nar["stream"] != true {
				t.Fatal("düşüş çağrısı araçsız AKAN anlatım olmalı")
			}
			if llmSystem(nar) != copilot.SystemPromptDrawerChat() || llmUser(nar) != exGoldenUser {
				t.Fatal("düşüş çağrısı bugünkü anlatımın metinleriyle bayt bayt aynı olmalı")
			}
			if errs := framesOf(frames, "error"); len(errs) != 0 {
				t.Fatalf("düşüşte hata olayı yayınlanmamalı: %v", errs)
			}
			if n := strings.Count(strings.Join(stepTexts(frames), "|"), exceptionCodeFallbackLabel); n != 1 {
				t.Fatalf("düşüş çipi tam bir kez olmalı (%d): %v", n, stepTexts(frames))
			}
			if ans := answerText(t, frames); ans != "tamam"+drawerSourceNote("exception") {
				t.Fatalf("cevap: %q", ans)
			}
			if done := framesOf(frames, "done"); len(done) != 1 || done[0]["ok"] != true {
				t.Fatalf("done: %v", done)
			}
			sawLoopErr := false
			for _, r := range rec.wait(t, 2) {
				if r.Surface == exceptionCodeSurface && r.Status == "error" {
					sawLoopErr = true
				}
			}
			if !sawLoopErr {
				t.Fatal("döngünün ai_calls satırı status=error ile kalmalı (/ai arızayı görsün)")
			}
		})
	}
	t.Run("istemci iptali düşmez", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		llm := newExLLM(t, func(int) (int, any) {
			return http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "tools are not supported"}}
		}, "tamam", cancel)
		s, _ := setup(t, llm)
		frames := exPostCtx(t, s, ctx, "u-a", auth.RoleEditor, exceptionDrawerBody(exQuestion, "conv-ex-cancel"))
		if n := len(llm.calls()); n != 1 {
			t.Fatalf("iptalde ikinci (anlatım) çağrısı yakılmamalı: %d çağrı", n)
		}
		if strings.Contains(strings.Join(stepTexts(frames), "|"), exceptionCodeFallbackLabel) {
			t.Fatal("iptalde düşüş çipi olmamalı")
		}
		if len(framesOf(frames, "answer")) != 0 {
			t.Fatal("iptalde cevap olmamalı")
		}
	})
}

// Sunulmayan yerli araç (get_trace): Executor bilinmeyen ad olarak reddeder,
// handler hiç koşmaz, audit/span yok.
func TestExceptionFollowUpRefusesUnofferedTool(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n > 0 {
			return answerMsg("Bu araç bu sohbette yok.")
		}
		return toolCallMsg([2]string{"get_trace", `{"trace_id":"` + tfTrace + `"}`})
	})
	s, git := scServer(t, llm, nil)
	installExceptionInput(t, exInput("", ""))
	var ft fakeTools
	ft.install(t) // katalogdaki her handler sahteye bağlı: get_trace yürüseydi burada görünürdü
	frames, _ := scPost(t, s, "u-a", auth.RoleEditor, exceptionDrawerBody(exQuestion, "conv-ex-unoffered"))
	if got := ft.snapshot(); len(got) != 0 {
		t.Fatalf("sunulmayan araç yürütüldü: %+v", got)
	}
	var res map[string]any
	for _, f := range framesOf(frames, "step-result") {
		if f["tool"] == "get_trace" {
			res = f
		}
	}
	if res == nil || res["skipped"] != true || res["reason"] != skipReasonUnknown {
		t.Fatalf("get_trace bilinmeyen ad olarak reddedilmeli: %v", res)
	}
	for done := false; !done; {
		select {
		case e := <-s.auditQ:
			if e.TargetID == "get_trace" {
				t.Fatal("yürütülmeyen çağrı audit'e yazıldı")
			}
		default:
			done = true
		}
	}
	if n := len(git.requests()); n != 0 {
		t.Fatalf("git'e %d istek gitti", n)
	}
}

// Grup okunamadıysa döngüye girilmez: bugünkü tek akan anlatım (HAM KANIT'sız).
func TestExceptionFollowUpUnreadableGroupIsNarration(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
	s, git := scServer(t, llm, nil)
	installExceptionInput(t, nil)
	frames, _ := scPost(t, s, "u-a", auth.RoleEditor, exceptionDrawerBody(exQuestion, "conv-ex-unread"))
	calls := llm.calls()
	if len(calls) != 1 {
		t.Fatalf("tek anlatım çağrısı bekleniyordu, %d", len(calls))
	}
	if _, ok := calls[0]["tools"]; ok || calls[0]["stream"] != true || llmSystem(calls[0]) != copilot.SystemPromptDrawerChat() {
		t.Fatal("okunamayan grupta araçsız akan çekmece anlatımı olmalı")
	}
	if strings.Contains(llmUser(calls[0]), "HAM KANIT") {
		t.Fatal("kanıt yokken HAM KANIT bloğu olmamalı")
	}
	if ans := answerText(t, frames); ans != "tamam"+drawerSourceNote("") {
		t.Fatalf("cevap: %q", ans)
	}
	if n := len(git.requests()); n != 0 {
		t.Fatalf("git'e %d istek gitti", n)
	}
}

// ── 4. kapsam ve sürüm ────────────────────────────────────────────────────

// Okuyucu düzeyi: grup servisi ve stack servisi okunur; öteki her servis
// (örnek trace'te geçen dahil) ve okunamayan grup git'e TEK istek atmaz.
// Sürüm yalnız stack'i veren olaydan ve yalnız o servis için.
func TestExceptionSourceScopeAndVersion(t *testing.T) {
	git, dv := newSCGit(t)
	git.tags["tags/2.0.0"] = scCommit
	git.trees[scCommit] = []string{scPath}
	// Örnek trace'te "checkout" var: kapsam "trace'in servisleri"ne genişlerse
	// aşağıdaki red kırmızıya döner.
	s := &Server{devops: dv, tempo: scTempo(t, map[string]string{"service.name": "checkout"})}
	g := &chstore.ExceptionGroup{Service: exGroupSvc}
	ctx := withSourceExceptionScope(context.Background(), exceptionCodeScopeFrom(g, *exInput(exStackSvc, "2.0.0")))

	for _, svc := range []string{"checkout", "infra-vault"} {
		_, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: svc, File: "ChargeHandler.java"})
		if err == nil || !strings.Contains(err.Error(), "kod kapsamında değil") || !strings.Contains(err.Error(), "ledger-writer, payments") {
			t.Fatalf("%s reddedilmeli (izinliler anılarak): %v", svc, err)
		}
	}
	unread := withSourceExceptionScope(context.Background(), exceptionCodeScopeFrom(nil, anomaly.ExceptionExplainInput{}))
	sr, err := s.readSourceCode(unread, mcptools.SourceCodeRequest{Service: exGroupSvc, File: "ChargeHandler.java"})
	if err != nil || sr.Outcome != devops.SourceExceptionUnreadable || !strings.Contains(sr.Reason, "exception okunamadı") {
		t.Fatalf("okunamayan grup dürüst ıska olmalı: %v %+v", err, sr)
	}
	if n := len(git.requests()); n != 0 {
		t.Fatalf("kapsam dışı çağrılarda git'e %d istek gitti", n)
	}

	// Stack'i basan servis: kendi olayının sürümü (2.0.0 → commit).
	sr, err = s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: exStackSvc, File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.Outcome != devops.SourceOK || sr.RefKind != "commit" || sr.Commit != scCommit || sr.VersionBasis != "sohbet öznesi (exception örneği)" {
		t.Fatalf("stack servisi kendi sürümünden okunmalı: %v %+v", err, sr)
	}
	// Grup servisi stack'i basmadı: başka olayın sürümü YAMANMAZ → dal.
	before := len(git.requests())
	sr, err = s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: exGroupSvc, File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.Outcome != devops.SourceOK || sr.RefKind != "branch" || sr.Version != "" || sr.VersionBasis != "" {
		t.Fatalf("grup servisi dal ucundan okunmalı: %v %+v", err, sr)
	}
	for _, r := range git.requests()[before:] {
		if strings.Contains(r, "2.0.0") {
			t.Fatalf("grup servisi için stack servisinin sürümü sorgulandı: %s", r)
		}
	}
	// Stack bir ÖRNEKTEN geldiyse (StackService boş) kod servisi grup servisidir
	// ve sürüm o örneğin sürümüdür.
	same := withSourceExceptionScope(context.Background(), exceptionCodeScopeFrom(g, *exInput("", "2.0.0")))
	sr, err = s.readSourceCode(same, mcptools.SourceCodeRequest{Service: exGroupSvc, File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.RefKind != "commit" || sr.Commit != scCommit {
		t.Fatalf("örnekten gelen stack'te grup servisi örneğin sürümünden okunmalı: %v %+v", err, sr)
	}
	if _, err := s.readSourceCode(same, mcptools.SourceCodeRequest{Service: exStackSvc, File: "X.java"}); err == nil {
		t.Fatal("stack örnekten geldiyse log servisi kapsamda değil")
	}
}

// Uçtan uca: model örnek trace'te geçen bir servisi ister → git'e istek yok,
// hata modele izinli servisleri söyler.
func TestExceptionFollowUpScopeEndToEnd(t *testing.T) {
	t.Run("örnek trace'teki öteki servis", func(t *testing.T) {
		llm := newLoopLLM(t, scToolCall(`{"service":"checkout","file":"ChargeHandler.java","line":120}`, "Bu servisin kodu okunamıyor."))
		s, git := scServer(t, llm, map[string]string{"service.name": "checkout"})
		installExceptionInput(t, exInput("", ""))
		frames, _ := scPost(t, s, "u-a", auth.RoleEditor, exceptionDrawerBody(exQuestion, "conv-ex-scope"))
		if n := len(git.requests()); n != 0 {
			t.Fatalf("kapsam dışı servis için git'e %d istek gitti", n)
		}
		var res map[string]any
		for _, f := range framesOf(frames, "step-result") {
			if f["tool"] == mcptools.SourceCodeToolName {
				res = f
			}
		}
		if res == nil || res["ok"] != false {
			t.Fatalf("kapsam dışı çağrı hata olmalı: %v", res)
		}
		if msgs := llmMessagesText(llm.calls()[1]); !strings.Contains(msgs, "kod kapsamında değil") || !strings.Contains(msgs, "payments") {
			t.Fatalf("model izinli servisleri görmeli: %s", msgs)
		}
	})
}

// İki çağrı (biri tekrar muhafızına takılır, öbürü yürür): özne yine BİR kez
// yüklenir; döngü serbest döngünün tekrar muhafızını kullanır.
func TestExceptionFollowUpLoadsSubjectOnce(t *testing.T) {
	args := `{"service":"payments","file":"ChargeHandler.java","line":120,"context_lines":5}`
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		switch n {
		case 0:
			return toolCallMsg([2]string{mcptools.SourceCodeToolName, args})
		case 1:
			return toolCallMsg([2]string{mcptools.SourceCodeToolName, args}, [2]string{mcptools.SourceCodeToolName, `{"service":"payments","file":"ChargeHandler.java","line":200,"context_lines":5}`})
		}
		return answerMsg("tamam")
	})
	s, _ := scServer(t, llm, nil)
	loads := installExceptionInput(t, exInput("", ""))
	frames, _ := scPost(t, s, "u-a", auth.RoleEditor, exceptionDrawerBody(exQuestion, "conv-ex-once"))
	if n := loads.count(); n != 1 {
		t.Fatalf("özne %d kez yüklendi", n)
	}
	skipped, executed := 0, 0
	for _, f := range framesOf(frames, "step-result") {
		if f["tool"] != mcptools.SourceCodeToolName {
			continue // çekmecenin kanıt çipi
		}
		if f["skipped"] == true {
			skipped++
		} else if f["ok"] == true {
			executed++
		}
	}
	if skipped != 1 || executed != 2 {
		t.Fatalf("tekrar muhafızı: skipped=%d executed=%d", skipped, executed)
	}
}

// ── 5. saf kapılar ────────────────────────────────────────────────────────

func TestExceptionCodeToolsGate(t *testing.T) {
	_, dv := newSCGit(t)
	withDevOps := mcptools.ChatToolList((&Server{devops: dv}).mcpDeps())
	without := mcptools.ChatToolList((&Server{}).mcpDeps())
	if len(withDevOps) < 5 || !hasToolName(toolNames(withDevOps), mcptools.SourceCodeToolName) {
		t.Fatal("kurgu: DevOps'lu katalog araç içermeli")
	}
	only := func(ts []mcp.Tool) bool { return len(ts) == 1 && ts[0].Name == mcptools.SourceCodeToolName }
	if got := exceptionCodeTools(withDevOps, &auth.Claims{UserID: "u-1", Role: auth.RoleAdmin}); !only(got) {
		t.Fatalf("admin: yalnız read_source_code: %v", toolNames(got))
	}
	for _, c := range []*auth.Claims{nil, {UserID: "token:abc", Role: auth.RoleAdmin}, {UserID: " ", Role: auth.RoleAdmin}} {
		if got := exceptionCodeTools(withDevOps, c); len(got) != 0 {
			t.Fatalf("%+v: araç sunulmamalı: %v", c, toolNames(got))
		}
	}
	if got := exceptionCodeTools(without, &auth.Claims{UserID: "u-1", Role: auth.RoleAdmin}); len(got) != 0 {
		t.Fatal("DevOps yokken araç sunulmamalı")
	}
	got := exceptionCodeTools(withDevOps, &auth.Claims{UserID: "u-1", Role: auth.RoleViewer})
	if want := roleSatisfies(auth.RoleViewer, mcptools.SourceCodeMinRole); only(got) != want {
		t.Fatalf("viewer: aracın rol kapısı (%q) uygulanmalı", mcptools.SourceCodeMinRole)
	}
	if (&Server{devops: dv}).exceptionCodeToolsFor(drawerLoopEnv{}) != nil {
		t.Fatal("env yoksa döngü kurulmaz")
	}
}

func toolNames(ts []mcp.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

func TestExceptionCodeHelpers(t *testing.T) {
	if got := exceptionCodeSourceNoteTR("exception", nil); got != drawerSourceNote("exception") {
		t.Fatalf("araçsız künye çekmeceninki: %q", got)
	}
	if got := exceptionCodeSourceNoteTR("exception", []string{"read_source_code", "read_source_code"}); got != drawerSourceNote("exception")+" + read_source_code (kaynak kod)" {
		t.Fatalf("künye: %q", got)
	}
	if got := exceptionCodeAnswerTR("cevap\n```chart\n{\"service\":\"x\"}\n```", "", nil); strings.Contains(got, "```chart") || !strings.HasPrefix(got, "cevap") {
		t.Fatalf("modelin chart çiti sökülmeli: %q", got)
	}
	if got := exceptionCodeBudgetLabelTR(6); got != "kod okuma: en çok 6 dosya penceresi" {
		t.Fatalf("bütçe çipi: %q", got)
	}
	if got := exceptionCodeBudgetExhaustedLabelTR(6, 5, 1, 3); got != "kod okuma hakkı doldu (6/6 çağrı · 3/5 tur) — 5 çağrı yürütüldü, 1 yürütülmedi; eldeki kanıtla cevaplanıyor" {
		t.Fatalf("bütçe doldu çipi: %q", got)
	}
	// Tek araç turu küçültülemez → döngü araçsız anlatıma düşer.
	if _, ok := shrinkKeepingHead([]copilot.ChatMessage{{Role: "user", Text: "SORU"}, {Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: "a"}}}, {Role: "user", ToolResults: []copilot.ToolResult{{CallID: "a"}}}}); ok {
		t.Fatal("tek araç turunda küçültme yok (düşüş yolu)")
	}
	head := copilot.ChatMessage{Role: "user", Text: "SORU"}
	tc := copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: "a"}}}
	res := copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: "a"}}}
	conv := []copilot.ChatMessage{head, tc, res, tc, res}
	shrunk, ok := shrinkKeepingHead(conv)
	if !ok || len(shrunk) != 3 || shrunk[0].Text != "SORU" || len(shrunk[1].ToolCalls) == 0 {
		t.Fatalf("baş blok korunmalı, araç çifti bölünmemeli: %+v", shrunk)
	}
	if _, ok := shrinkKeepingHead(conv[:1]); ok {
		t.Fatal("tek blok küçültülmez")
	}
	sc := exceptionCodeScopeFrom(&chstore.ExceptionGroup{Service: "a"}, anomaly.ExceptionExplainInput{StackService: "a"})
	if got := sc.services(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("aynı servis bir kez: %v", got)
	}
}

// Kaynak pini: döngü serbest döngünün maskelerini ve tavanlarını kullanır,
// dış MCP'ye ve ham önizlemeye dokunmaz; kapsam explain'in tek girdi
// kurucusundan (ikinci örnek/stack seçimi yok).
func TestExceptionFollowUpWiring(t *testing.T) {
	b, err := os.ReadFile("chat_exception_followup.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"preview, truncated := chatStepPreview(tc.Name, tr.Content, tr.IsError)",
		"chatSourceCodePromptTR(tools) + copilot.SystemPromptDrawerChat()",
		"Text: drawerNarrationUser(question, ex, msgs, evidence)",
		"chatPromptSample(lastUserText(msgs), codeReads)",
		"splitByCallBudget(turn.ToolCalls, callsLeft)",
		"round == chatMaxToolRounds-1 || callsLeft <= 0",
		"copilot.WithNoToolCalls(tctx2)",
		"clampToolResultForModel(tr.Content)",
		"Span: env.span.tool,",
		"sourceCodeToolsFor(toolsForRole(catalog, role), true, c)",
		"anomaly.BuildExceptionExplainInput(ctx, s.store, s.logs, g, loc)",
		"codeService: in.CodeService(g.Service), codeVersion: in.StackVersion,",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("chat_exception_followup.go %q bağlantısını kaybetti", want)
		}
	}
	if regexp.MustCompile(`externalChatTools|s\.mcpClient|clipStepPreview\(tr\.Content\)|WriteString\(oc\.Content\)|GetExceptionGroupSamples|pickExceptionStack|StackServiceVersion\(`).MatchString(src) {
		t.Error("döngü dış MCP'ye, maskesiz önizlemeye ya da ikinci bir örnek/stack seçimine dokunuyor")
	}
	// Sayı denetimi bu döngüde YOK (çekmecenin cevap sözleşmesi).
	if regexp.MustCompile(`traceFollowUpNumericWarning|numericClaimWarningTR|ungroundedNumbers|chatFollowUpEvidence`).MatchString(src) {
		t.Error("exception döngüsüne sayı denetimi geri gelmiş")
	}
	drawer, _ := os.ReadFile("copilot_drawer.go")
	for _, want := range []string{
		"g, in, ok := s.drawerExceptionInput(ectx, subj.ID, loc)",
		"if exScope != nil && exScope.loaded {",
		"lok, fallback := s.exceptionCodeLoop(",
		"emitGuidedContextStep(emit, exceptionCodeFallbackLabel)",
	} {
		if !strings.Contains(string(drawer), want) {
			t.Errorf("copilot_drawer.go %q bağlantısını kaybetti", want)
		}
	}
}
