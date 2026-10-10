package api

// chat_source_code_test.go — v0.10.1050 (operatör: "Sohbet kod okuyabilsin:
// takip soruları bugün kod okuyamıyor."): read_source_code'un sohbet
// döngüsünden UÇTAN UCA geçişi. Gerçek copilotChat + gerçek copilot.Service
// (sahte OpenAI-uyumlu model, loopLLM) + GERÇEK araç handler'ı + gerçek
// devops.Service (sahte git sunucusu) + sahte Tempo (öznenin span'leri).
// Seam (chatLoopHandlerSeam) KURULMAZ.
//
// Pinlenen sözleşmeler:
//   - SIZINTI YOK: fikstür kodunun ayırt edici dizgisi modele gider ama SSE
//     akışına (step / step-result / sources / answer), ai_calls kaydına, audit
//     satırına ve span'lere GİRMEZ ("kod tarayıcıya gitmez").
//   - Sunulma (güvenlik incelemesi): YALNIZ panel trace takibinde, oturum
//     kullanıcısına (v0.10.1052: viewer dahil her rol; v0.10.1050'de editor+)
//     ve DevOps bağlıyken. Bağımsız sohbette (dış MCP olsun olmasın) ve API
//     token'ında (rolü ne olursa olsun) YOK; sunulmadığında katalog ve prompt
//     bayt bayt eski.
//   - Kapsam: servis öznenin trace'inde olmalı; trace okunamazsa dürüst ıska.
//   - Sürüm: model seçemez; öznenin span'lerinden (explain'in yardımcısı).
//   - Sayı denetimi kanıtına kod GİRMEZ (yalnız referans satırı).

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcpclient"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/tempo"
)

const scLeakMarker = "ZEBRA_CHAT_FIXTURE_9137"

const (
	scPath   = "/src/main/java/com/example/cards/ChargeHandler.java"
	scBig    = "/src/main/java/com/example/cards/Generated.java"
	scCommit = "c0ffee0000000000000000000000000000000099"
)

// scGit — sahte git (Azure DevOps items/refs) sunucusu. Büyük dosyada JSON
// içerik yolu 500 döner, metin yolu ($format=text) ham gövdeyi verir —
// gerçek okuyucunun tavana dayandığı yol.
type scGit struct {
	mu    sync.Mutex
	reqs  []string
	tree  []string
	files map[string]string
	tags  map[string]string   // "tags/1.4.2" → commit
	trees map[string][]string // commit → ağaç
}

func newSCGit(t *testing.T) (*scGit, *devops.Service) {
	t.Helper()
	var body strings.Builder
	for i := 1; i <= 300; i++ {
		if i == 120 {
			fmt.Fprintf(&body, "        ledger.post(card, 4711); // %s\n", scLeakMarker)
			continue
		}
		fmt.Fprintf(&body, "    // satır %d %s\n", i, scLeakMarker)
	}
	g := &scGit{tree: []string{scPath, scBig}, files: map[string]string{scPath: body.String()}, tags: map[string]string{}, trees: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.reqs = append(g.reqs, r.URL.RequestURI())
		g.mu.Unlock()
		p, q := r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(p, "/refs") && strings.HasPrefix(q.Get("filter"), "tags/"):
			out := map[string]any{"value": []any{}}
			if sha, ok := g.tags[q.Get("filter")]; ok {
				out["value"] = []any{map[string]string{"name": "refs/" + q.Get("filter"), "objectId": sha}}
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasSuffix(p, "/refs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]string{{"name": "refs/heads/release"}, {"name": "refs/heads/master"}}})
		case strings.HasSuffix(p, "/items") && q.Get("recursionLevel") == "Full":
			paths := g.tree
			if q.Get("versionDescriptor.versionType") == "commit" {
				var ok bool
				if paths, ok = g.trees[q.Get("versionDescriptor.version")]; !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
			}
			items := []map[string]any{}
			for _, pth := range paths {
				items = append(items, map[string]any{"path": pth, "gitObjectType": "blob"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": items})
		case strings.HasSuffix(p, "/items"):
			g.mu.Lock()
			c, ok := g.files[q.Get("path")]
			g.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if q.Get("$format") == "text" {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, c)
				return
			}
			if len(c) > 1<<20 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": c})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"defaultBranch": "refs/heads/master"})
		}
	}))
	t.Cleanup(srv.Close)
	dv := devops.New()
	dv.Configure(devops.Settings{BaseURL: srv.URL, Collection: "DefaultCollection", Project: "Payments",
		PAT: "test-pat", Flavor: devops.FlavorServer})
	return g, dv
}

func (g *scGit) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.reqs...)
}

// scTempo — öznenin trace'i: tek kaynak (resource) altında span'ler.
func scTempo(t *testing.T, res map[string]string) *tempo.Service {
	t.Helper()
	var attrs []string
	for k, v := range res {
		attrs = append(attrs, fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, k, v))
	}
	ns := time.Now().Add(-time.Hour).UnixNano()
	body := `{"batches":[{"resource":{"attributes":[` + strings.Join(attrs, ",") + `]},"scopeSpans":[{"spans":[` +
		fmt.Sprintf(`{"traceId":%q,"spanId":%q,"name":"charge","kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":{"code":2}}`, tfTrace, tfSpan, ns, ns+1_000_000) +
		`]}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	tp := tempo.New()
	tp.Configure(tempo.Settings{Enabled: true, BaseURL: srv.URL})
	return tp
}

// scPost — postLoopChat'in ham gövdeyi de döndüren, kimliği seçilebilen ikizi
// (sızıntı taraması AKIŞIN TAMAMINA bakar).
func scPost(t *testing.T, s *Server, uid, role string, body map[string]any) ([]chatFrame, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/chat", strings.NewReader(string(raw)))
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: uid, Email: uid + "@example.test", Role: role}))
	w := httptest.NewRecorder()
	s.copilotChat(w, r)
	stream := w.Body.String()
	var out []chatFrame
	for _, block := range strings.Split(strings.TrimSpace(stream), "\n\n") {
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
	return out, stream
}

func scToolCall(args, answer string) func(int, map[string]any) map[string]any {
	return func(n int, _ map[string]any) map[string]any {
		if n > 0 {
			return answerMsg(answer)
		}
		return toolCallMsg([2]string{mcptools.SourceCodeToolName, args})
	}
}

// llmMessagesText — isteğin TÜM mesaj içerikleri (araç sonuçları dahil).
func llmMessagesText(body map[string]any) string {
	b, _ := json.Marshal(body["messages"])
	return string(b)
}

func hasToolName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// scServer — trace takibi sunucusu: DevOps (sahte git) + öznenin trace'i
// (Tempo; ödeme servisi). llm verilmezse "tamam" cevabı.
func scServer(t *testing.T, llm *loopLLM, traceRes map[string]string) (*Server, *scGit) {
	t.Helper()
	s, _ := newLoopTestServer(t, llm, "u-a")
	git, dv := newSCGit(t)
	s.devops = dv
	if traceRes != nil {
		s.tempo = scTempo(t, traceRes)
	}
	return s, git
}

// TestReadSourceCodeNoLeakEndToEnd — sızıntı pini: kod modele gider, başka
// hiçbir kopyaya gitmez. chatStepPreview'ün maskesi kalkarsa bu test kırmızı.
func TestReadSourceCodeNoLeakEndToEnd(t *testing.T) {
	llm := newLoopLLM(t, scToolCall(`{"service":"payments","file":"ChargeHandler.java","line":120,"context_lines":5}`,
		"ChargeHandler.java:120 satırında ledger.post çağrılıyor."))
	s, git := scServer(t, llm, map[string]string{"service.name": "payments"})
	rec := newCapRecorder()
	s.copilot.SetRecorder(rec)
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	s.tracer = tp.Tracer("test")

	frames, stream := scPost(t, s, "u-a", auth.RoleEditor, traceDrawerBody("ChargeHandler'da 120. satırın çevresinde ne var?", anchoredToMs(), "conv-sc"))

	// Kurgu: araç GERÇEKTEN koştu ve kod modele gitti.
	calls := llm.calls()
	if len(calls) != 2 {
		t.Fatalf("model %d kez çağrıldı (araç turu + cevap bekleniyordu)", len(calls))
	}
	if !strings.Contains(llmMessagesText(calls[1]), "120|         ledger.post(card, 4711); // "+scLeakMarker) {
		t.Fatal("kod modele ulaşmadı — test hiçbir şey ölçmüyor")
	}
	if n := len(git.requests()); n == 0 {
		t.Fatal("git sunucusuna istek gitmedi")
	}
	// 1) SSE akışının TAMAMI (step, step-result, sources, block, answer, done).
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
	prev, _ := res["preview"].(string)
	if !strings.HasPrefix(prev, "src/main/java/com/example/cards/ChargeHandler.java:115-125 · release (dal)") ||
		!strings.Contains(prev, "tarayıcıya gönderilmez") || res["truncated"] != false {
		t.Fatalf("önizleme yalnız referans olmalı: %q", prev)
	}
	srcs, _ := res["sources"].([]any)
	if len(srcs) != 1 || srcs[0].(map[string]any)["state"] != "ok" {
		t.Fatalf("kaynak rozeti: %v", res["sources"])
	}
	// 2) ai_calls kaydı: maskeli "[kod: …]" özeti var, kod yok.
	// v0.10.1153 — model çağrısı + turun etkileşim satırı; ikisi de kod taşımaz.
	last := modelCallAndTurnNoLeak(t, rec.wait(t, 2), scLeakMarker)
	if !strings.Contains(last.PromptSample, "[kod: payments/src/main/java/com/example/cards/ChargeHandler.java:115-125 · 11 satır · release (dal)]") {
		t.Fatalf("ai_calls maskeli özet yok: %q", last.PromptSample)
	}
	// 3) audit satırları (mcp.tool.call): argüman + bayt, kod yok.
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
	// 4) span'ler (ai.chat / ai.chat.turn / ai.tool): öznitelik ve olaylarda kod yok.
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

// Sayı denetimi: koddaki sabit (4711) cevabı TEMELLENDİRMEZ — kanıta yalnız
// referans satırı girer. Kod kanıta girseydi uyarı çıkmazdı.
func TestReadSourceCodeEvidenceIsReferenceOnly(t *testing.T) {
	llm := newLoopLLM(t, scToolCall(`{"service":"payments","file":"ChargeHandler.java","line":120,"context_lines":5}`,
		"Limit 4711 olarak sabitlenmiş."))
	s, _ := scServer(t, llm, map[string]string{"service.name": "payments"})
	frames, _ := scPost(t, s, "u-a", auth.RoleEditor, traceDrawerBody("Limit kaç?", anchoredToMs(), "conv-ev"))
	if !strings.Contains(llmMessagesText(llm.calls()[1]), "4711") {
		t.Fatal("kurgu: sabit koddan modele gitmeli")
	}
	if w := numericWarningLine(answerText(t, frames)); !strings.Contains(w, "4711") {
		t.Fatalf("koddaki sayı kanıt sayıldı (uyarı yok): %q", w)
	}
	if got := chatFollowUpEvidence("list_services", `{"x":4711}`); got != `{"x":4711}` {
		t.Fatal("diğer araçların kanıtı aynen kalmalı")
	}
}

// Sunulma kapısı + bayt-kimliği (trace takibi, editor): DevOps yokken araç ve
// ek YOK; varken tek fark ek + araç (diğer araçların şemaları aynen).
func TestReadSourceCodeOfferedOnlyWithDevOpsAndPromptByteIdentical(t *testing.T) {
	run := func(withDevOps bool) map[string]any {
		llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
		s, _ := newLoopTestServer(t, llm, "u-a")
		if withDevOps {
			_, s.devops = newSCGit(t)
		} else {
			s.devops = devops.New() // var ama yapılandırılmamış
		}
		scPost(t, s, "u-a", auth.RoleEditor, traceDrawerBody("Özetle?", anchoredToMs(), "conv-b"))
		calls := llm.calls()
		if len(calls) != 1 {
			t.Fatalf("model %d kez çağrıldı", len(calls))
		}
		return calls[0]
	}
	off, on := run(false), run(true)
	offNames, onNames := llmToolNames(off), llmToolNames(on)
	if hasToolName(offNames, mcptools.SourceCodeToolName) || !hasToolName(onNames, mcptools.SourceCodeToolName) {
		t.Fatalf("araç yalnız DevOps varken sunulmalı: off=%v", hasToolName(offNames, mcptools.SourceCodeToolName))
	}
	if len(onNames) != len(offNames)+1 {
		t.Fatalf("DevOps varken tek ek araç bekleniyordu: %d vs %d", len(onNames), len(offNames))
	}
	sysOff, sysOn := llmSystem(off), llmSystem(on)
	if strings.Contains(sysOff, "read_source_code") {
		t.Fatal("araç sunulmazken prompt eki var")
	}
	add := copilot.SourceCodeChatAddendum() + "\n\n"
	if strings.Count(sysOn, add) != 1 || strings.Replace(sysOn, add, "", 1) != sysOff {
		t.Fatal("araç sunulunca sistem mesajındaki TEK fark ek olmalı (bayt-kimliği)")
	}
	if !strings.HasSuffix(sysOn, copilot.DataNotInstruction) || strings.Index(sysOn, add) > strings.Index(sysOn, strings.TrimSpace(copilot.SystemPromptChat())[:40]) {
		t.Fatal("ek sohbet çekirdeğinin ÖNÜNDE, DataNotInstruction sonda olmalı")
	}
	onTools, _ := json.Marshal(dropTool(on["tools"].([]any), mcptools.SourceCodeToolName))
	offTools, _ := json.Marshal(off["tools"])
	if string(onTools) != string(offTools) {
		t.Fatal("DevOps varken diğer araçların spec'i değişti")
	}
	if chatSourceCodePromptTR(nil) != "" {
		t.Fatal("katalogda araç yoksa ek boş olmalı")
	}
}

func dropTool(tools []any, name string) []any {
	out := []any{}
	for _, tl := range tools {
		if fn, _ := tl.(map[string]any)["function"].(map[string]any); fn["name"] == name {
			continue
		}
		out = append(out, tl)
	}
	return out
}

// BAĞIMSIZ SOHBET: araç HİÇ sunulmaz — dış MCP istemcisi olsun olmasın
// (dış araçların yanında onay adımı yok; ekilmiş bir log satırı kodu dış
// araca taşıtabilirdi). Prompt ve katalog DevOps'tan bağımsız bayt bayt aynı.
func TestReadSourceCodeNeverInStandaloneLoop(t *testing.T) {
	run := func(withDevOps, withExternal bool) map[string]any {
		llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
		s, _ := newLoopTestServer(t, llm, "u-a")
		s.copilot.SetIntentClassify(copilot.IntentOff) // serbest döngüye ulaşsın
		if withDevOps {
			_, s.devops = newSCGit(t)
		}
		if withExternal {
			srv, _ := newFakeTrackerMCP(t)
			mc := mcpclient.NewService()
			mc.Configure(mcpclient.Settings{Servers: []mcpclient.ServerConfig{{Name: "tracker", Transport: "http", URL: srv.URL, Enabled: true}}})
			t.Cleanup(mc.Close)
			s.mcpClient = mc
		}
		scPost(t, s, "u-a", auth.RoleAdmin, map[string]any{
			"messages": []map[string]any{{"role": "user", "text": "ChargeHandler sınıfının 120. satırına bak"}},
			"context":  map[string]any{"conversation": "conv-st"},
		})
		calls := llm.calls()
		if len(calls) == 0 {
			t.Fatal("model çağrılmadı")
		}
		return calls[0]
	}
	for _, ext := range []bool{false, true} {
		on, off := run(true, ext), run(false, ext)
		if names := llmToolNames(on); hasToolName(names, mcptools.SourceCodeToolName) {
			t.Fatalf("dış MCP=%v: bağımsız sohbet read_source_code sunuyor", ext)
		} else if ext && !strings.Contains(strings.Join(names, " "), "tracker__") {
			t.Fatal("kurgu: dış MCP aracı katalogda değil")
		}
		if llmSystem(on) != llmSystem(off) {
			t.Fatalf("dış MCP=%v: bağımsız sohbet prompt'u DevOps'a göre değişti", ext)
		}
		onTools, _ := json.Marshal(on["tools"])
		offTools, _ := json.Marshal(off["tools"])
		if string(onTools) != string(offTools) {
			t.Fatalf("dış MCP=%v: bağımsız sohbet kataloğu DevOps'a göre değişti", ext)
		}
	}
}

// API TOKEN'I ve ROL — v0.10.1052 (operatör: "Kod okuma aracı viewer'lara da
// açılsın (bugün editor ve admin)."): oturum kullanıcısı viewer/editor/admin
// görür; token principal'ı (cmk_, UserID "token:…") ve kimliksiz çağıran
// ROLDEN BAĞIMSIZ görmez — viewer token'ı da dahil. MinRole tek sabit = ""
// (viewer tabanı). Mutasyon: editor kapısı geri gelirse viewer satırı,
// token dışlaması kalkarsa token satırları kırmızı.
func TestReadSourceCodeCallerAndRoleGate(t *testing.T) {
	listed := func(uid, role string) (bool, string) {
		llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
		s, _ := newLoopTestServer(t, llm, uid) // hitap önbelleği (store'suz sunucu)
		_, s.devops = newSCGit(t)
		scPost(t, s, uid, role, traceDrawerBody("Özetle?", anchoredToMs(), "conv-r"))
		calls := llm.calls()
		if len(calls) == 0 {
			t.Fatal("model çağrılmadı")
		}
		return hasToolName(llmToolNames(calls[0]), mcptools.SourceCodeToolName), llmSystem(calls[0])
	}
	for _, c := range []struct {
		uid, role string
		want      bool
	}{
		{"u-a", auth.RoleViewer, true},
		{"u-a", auth.RoleEditor, true},
		{"u-a", auth.RoleAdmin, true},
		{"token:t0", auth.RoleViewer, false},
		{"token:t1", auth.RoleAdmin, false},
		{"token:t2", auth.RoleEditor, false},
		{"", auth.RoleViewer, false}, // kimliksiz (boş UserID) — rol tek başına açmaz
	} {
		got, sys := listed(c.uid, c.role)
		if got != c.want {
			t.Errorf("%s/%s: sunuldu=%v, beklenen %v", c.uid, c.role, got, c.want)
		}
		if !got && strings.Contains(sys, "read_source_code") {
			t.Errorf("%s/%s: araç yokken prompt eki var", c.uid, c.role)
		}
	}
	if mcptools.SourceCodeMinRole != "" {
		t.Fatalf("SourceCodeMinRole %q — viewer tabanı (\"\") olmalı", mcptools.SourceCodeMinRole)
	}
	for _, c := range []struct {
		claims *auth.Claims
		want   bool
	}{
		{&auth.Claims{UserID: "u-1"}, true}, {&auth.Claims{UserID: "token:abc"}, false},
		{&auth.Claims{UserID: " "}, false}, {nil, false},
		{&auth.Claims{UserID: "u-1", Role: auth.RoleViewer}, true},
		{&auth.Claims{UserID: "token:abc", Role: auth.RoleViewer}, false},
		{&auth.Claims{UserID: "", Role: auth.RoleAdmin}, false},
	} {
		if got := sourceCodeCallerAllowed(c.claims); got != c.want {
			t.Errorf("sourceCodeCallerAllowed(%+v) = %v", c.claims, got)
		}
	}
}

// KAPSAM: servis öznenin trace'inde olmalı. Trace'te olmayan servis, öznesiz
// çağrı, okunamayan trace ve biçimsiz servis adı git'e TEK istek atmaz.
// (Telemetri gönderen herkes servis adı yaratabilir; konvansiyon onu herhangi
// bir depoya çevirirdi.)
func TestReadSourceCodeScopedToSubjectTrace(t *testing.T) {
	git, dv := newSCGit(t)
	s := &Server{devops: dv, tempo: scTempo(t, map[string]string{"service.name": "payments"})}
	ctx := withSourceSubject(context.Background(), tfTrace, tfSpan)

	_, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "infra-vault", File: "Vault.java"})
	if err == nil || !strings.Contains(err.Error(), "bu trace'te bulunamadı") || !strings.Contains(err.Error(), "payments") {
		t.Fatalf("trace dışı servis reddedilmeli (trace'in servisleri anılarak): %v", err)
	}
	sr, err := s.readSourceCode(context.Background(), mcptools.SourceCodeRequest{Service: "payments", File: "ChargeHandler.java"})
	if err != nil || sr.Outcome != devops.SourceOutOfScope {
		t.Fatalf("öznesiz çağrı out_of_scope olmalı: %v %+v", err, sr)
	}
	noTrace := &Server{devops: dv}
	sr, err = noTrace.readSourceCode(withSourceSubject(context.Background(), tfTrace, tfSpan), mcptools.SourceCodeRequest{Service: "payments", File: "ChargeHandler.java"})
	if err != nil || sr.Outcome != devops.SourceTraceUnreadable || !strings.Contains(sr.Reason, "trace okunamadı") {
		t.Fatalf("okunamayan trace dürüst ıska olmalı: %v %+v", err, sr)
	}
	if _, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "pay ments", File: "X.java"}); err == nil {
		t.Fatal("biçimsiz servis adı reddedilmeli")
	}
	if n := len(git.requests()); n != 0 {
		t.Fatalf("kapsam dışı çağrılarda git'e %d istek gitti", n)
	}
	// Trace'teki servis okunur.
	sr, err = s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "payments", File: "ChargeHandler.java", Line: 120, ContextLines: 3})
	if err != nil || sr.Outcome != devops.SourceOK {
		t.Fatalf("trace'teki servis okunmalı: %v %+v", err, sr)
	}
	if got := traceServiceNames([]chstore.SpanRow{{ServiceName: "b"}, {ServiceName: "a"}, {ServiceName: "b"}, {}}); fmt.Sprint(got) != "[a b]" {
		t.Fatalf("traceServiceNames: %v", got)
	}
	// DevOps yapılandırılmamış → okuyucu yok (araç sunulmaz).
	if (&Server{devops: devops.New()}).sourceCodeReaderOrNil() != nil || (&Server{}).sourceCodeReaderOrNil() != nil {
		t.Fatal("DevOps yokken okuyucu nil olmalı")
	}
}

// Sürüm sohbet öznesinden: trace'te ödeme servisinin çalışan sürümü 1.4.2,
// tag var → kod o commit'ten; tag yoksa dal + not.
func TestReadSourceCodeVersionFromTraceSubject(t *testing.T) {
	git, dv := newSCGit(t)
	git.tags["tags/1.4.2"] = scCommit
	git.trees[scCommit] = []string{scPath}
	s := &Server{devops: dv, tempo: scTempo(t, map[string]string{"service.name": "payments", "service.version": "1.4.2"})}
	ctx := withSourceSubject(context.Background(), tfTrace, tfSpan)
	sr, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "payments", File: "ChargeHandler.java", Line: 120, ContextLines: 5})
	if err != nil {
		t.Fatal(err)
	}
	if sr.Outcome != devops.SourceOK || sr.RefKind != "commit" || sr.Commit != scCommit || sr.VersionBasis != "sohbet öznesi (trace)" {
		t.Fatalf("özne sürümünden commit'e okunmalı: %+v", sr)
	}
	s2 := &Server{devops: dv, tempo: scTempo(t, map[string]string{"service.name": "payments", "service.version": "9.9.9"})}
	sr, _ = s2.readSourceCode(withSourceSubject(context.Background(), tfTrace, tfSpan), mcptools.SourceCodeRequest{Service: "payments", File: "ChargeHandler.java", Line: 120, ContextLines: 5})
	if sr.RefKind != "branch" || sr.Version != "9.9.9" || !strings.Contains(sr.Reason, "9.9.9 depoda bulunamadı") {
		t.Fatalf("tag yoksa dal + not: %+v", sr)
	}
}

// Büyük dosya: gövde okuma tavanına dayandı → "dosya büyük, ilk N satır
// okundu"; dosya dışı satır için YANLIŞ "dosyanın dışında" cümlesi yok.
func TestReadSourceCodeTruncatedFileSaysSo(t *testing.T) {
	git, dv := newSCGit(t)
	var big strings.Builder
	for i := 1; big.Len() < 2<<20+4096; i++ {
		fmt.Fprintf(&big, "    int field%07d = %d; // üretilmiş dolgu satırı\n", i, i)
	}
	git.mu.Lock()
	git.files[scBig] = big.String()
	git.mu.Unlock()
	s := &Server{devops: dv, tempo: scTempo(t, map[string]string{"service.name": "payments"})}
	ctx := withSourceSubject(context.Background(), tfTrace, tfSpan)
	sr, err := s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "payments", File: "Generated.java", Line: 10, ContextLines: 2})
	if err != nil || sr.Outcome != devops.SourceOK || !sr.FileTruncated {
		t.Fatalf("kesik okunmalı: %v %+v", err, sr.Reason)
	}
	if !strings.Contains(sr.Reason, fmt.Sprintf("dosya büyük (yanıt tavanı 2 MB), ilk %d satır okundu", sr.TotalLines)) {
		t.Fatalf("kesik dosya söylenmeli: %q", sr.Reason)
	}
	sr, _ = s.readSourceCode(ctx, mcptools.SourceCodeRequest{Service: "payments", File: "Generated.java", Line: 9_000_000, ContextLines: 2})
	if strings.Contains(sr.Reason, "dosyanın dışında") || !strings.Contains(sr.Reason, "okunan kısmın dışında") {
		t.Fatalf("kesik dosyada 'dosyanın dışında' denmemeli: %q", sr.Reason)
	}
}

// subjectServiceVersion — SAF: odak span İSTENEN servisinse onun sürümü
// (canary), değilse servisin span'lerinde çoğunluk; başka servisin odak
// span'i sürüm vermez.
func TestSubjectServiceVersion(t *testing.T) {
	sp := func(id, svc, ver string, start int64) chstore.SpanRow {
		return chstore.SpanRow{SpanID: id, ServiceName: svc, StartTime: start, ResourceAttributes: map[string]string{"service.version": ver}}
	}
	spans := []chstore.SpanRow{
		sp("a1", "payments", "1.5.0", 3), sp("a2", "payments", "1.4.2", 1), sp("a3", "payments", "1.4.2", 2),
		sp("b1", "checkout", "7.0.0", 4),
	}
	for _, c := range []struct{ svc, focus, want string }{
		{"payments", "a1", "1.5.0"}, // odak (canary) çoğunluğu ezer
		{"payments", "", "1.4.2"},   // çoğunluk
		{"payments", "b1", "1.4.2"}, // başka servisin odağı sayılmaz
		{"checkout", "a1", "7.0.0"},
		{"ledger", "", ""},
	} {
		if got := subjectServiceVersion(spans, c.svc, c.focus); got != c.want {
			t.Errorf("%s/%s: %q, beklenen %q", c.svc, c.focus, got, c.want)
		}
	}
}

// Zarf tavanı api'nin araç sonucu kırpmasının ALTINDA (pencere yapının
// ortasından kesilmesin).
func TestSourceResultFitsChatToolClamp(t *testing.T) {
	if mcptools.SourceResultMaxRunes >= chatToolResultMaxRunes {
		t.Fatalf("read_source_code zarfı (%d) sohbet kırpmasının (%d) altında olmalı", mcptools.SourceResultMaxRunes, chatToolResultMaxRunes)
	}
}

// chatStepPreview / chatPromptSample — saf kapılar: başka araç bugünkü
// kırpmadan geçer, hata sonucu ToolErrorJSON olarak kalır, kod okunmadıysa
// ai_calls örneği bayt bayt eski.
func TestChatStepPreviewAndPromptSample(t *testing.T) {
	big := strings.Repeat("x", chatStepPreviewMax+10)
	if p, tr := chatStepPreview("list_services", big, false); len(p) != chatStepPreviewMax || !tr {
		t.Fatal("diğer araçlar bugünkü 4 KB kırpmasından geçmeli")
	}
	errJSON := `{"error":"bad_args","retryable":false,"hint":"x","detail":"file geçersiz"}`
	if p, _ := chatStepPreview(mcptools.SourceCodeToolName, errJSON, true); p != errJSON {
		t.Fatalf("hata önizlemesi ToolErrorJSON kalmalı (arayüz hata rozeti onu okur): %q", p)
	}
	if p, _ := chatStepPreview(mcptools.SourceCodeToolName, `{"outcome":"ok","code":"`+scLeakMarker+`"}`, false); strings.Contains(p, scLeakMarker) {
		t.Fatal("başarılı sonuç önizlemesi kod taşıyor")
	}
	if chatPromptSample("soru", nil) != "soru" {
		t.Fatal("kod okunmadıysa örnek bayt bayt eski olmalı")
	}
	if got := chatPromptSample("soru", []string{"[kod: a]", "[kod: b]"}); got != "soru\n\n[kod: a]\n[kod: b]" {
		t.Fatalf("örnek: %q", got)
	}
	if chatCodeReadSummary("list_services", "{}", false) != "" || chatCodeReadSummary(mcptools.SourceCodeToolName, "{}", true) != "" {
		t.Fatal("yalnız başarılı read_source_code özetlenir")
	}
}

// Kaynak pini: copilot_chat.go sunulma kapısını, önizleme maskesini, sayı
// denetimi kanıtını ve ai_calls örneğini kapılardan geçirir.
func TestCopilotChatWiresSourceCodeMasks(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"tools = sourceCodeToolsFor(tools, isTraceFollowUp, c)",
		"preview, truncated := chatStepPreview(tc.Name, tr.Content, tr.IsError)",
		"fuEvidence.WriteString(chatFollowUpEvidence(tc.Name, oc.Content))",
		"chatPromptSample(lastUserText(req.Messages), codeReads)",
		"chatSourceCodePromptTR(tools) +",
		"ctx = withSourceSubject(ctx, tf.TraceID, tf.SpanID)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("copilot_chat.go %q bağlantısını kaybetti", want)
		}
	}
	if regexp.MustCompile(`clipStepPreview\(tr\.Content\)|fuEvidence\.WriteString\(oc\.Content\)`).MatchString(src) {
		t.Error("copilot_chat.go araç sonucunu maskesiz önizlemeye ya da kanıta koyuyor")
	}
	// Sunulma kapısı katalog/spec/prompt KURULMADAN önce.
	if i, j := strings.Index(src, "tools = sourceCodeToolsFor("), strings.Index(src, "byName[t.Name] = t.Handler"); i < 0 || j < 0 || i > j {
		t.Error("sunulma kapısı byName/spec kurulumundan ÖNCE olmalı")
	}
}
