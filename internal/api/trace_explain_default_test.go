package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/tempo"
)

// trace_explain_default_test.go — v0.10.1036 (operatör: "Aslında CoSRE'nin eski
// explain trace'teki yapısı daha iyiydi, neden sonradan değişti. … Eski kanıt
// toplayıcı güzeldi."; varsayılan için: "dönsün"). "CoSRE'ye sor" (POST
// /api/copilot/explain-trace/{id}, kodsuz) yine KLASİK toplayıcıdan geçer:
// SystemPromptTrace, klasik önbellek anahtarı, akan cevap; adım olayı,
// `sources`, sayı uyarısı ve "Kaynak durumu" künyesi yok. v0.10.948 incelemesi
// uçtan ERİŞİLEMEZ (kodu temizlik sürümüne dek duruyor).
//
// Davranış pinleri sahte LLM (invCaptureProvider) + sahte Tempo ile: ClickHouse
// sahtesi yok (chstore.Store somut; nil'de GetTrace panikler), trace Tempo'dan
// gelir (resolveTraceSpans: Tempo önce). İnceleme koşucusu dikişi
// (newTraceInvestigationRunner, invHandlerServer) her testte kuruludur ve HİÇ
// çağrılmamalıdır. "Trace hiçbir yerde yok → 404" CH'siz kurulamadığı için
// kaynak pinli + writeExplainPrepareErr'in davranışıyla (aşağıda).

// defTempoTrace — iki span'lik sentetik trace: checkout GET /cart (kök, hata)
// → payments POST /charge (hata, "card declined").
func defTempoTrace(t *testing.T) *httptest.Server {
	t.Helper()
	ns := invTestT0.UnixNano()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/traces/"+invTestTrace) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"batches":[
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},
 "scopeSpans":[{"spans":[{"traceId":%q,"spanId":%q,"name":"GET /cart","kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":{"code":2,"message":"upstream error"}}]}]},
{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"payments"}}]},
 "scopeSpans":[{"spans":[{"traceId":%q,"spanId":%q,"parentSpanId":%q,"name":"POST /charge","kind":3,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","status":{"code":2,"message":"card declined"}}]}]}]}`,
			invTestTrace, invTestRoot, ns, ns+1_234_500_000,
			invTestTrace, invTestPaySpan, invTestRoot, ns+100_000_000, ns+1_000_000_000)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// defServer — sahte LLM + sahte Tempo + inceleme koşucusu dikişi (sayaç).
func defServer(t *testing.T, p *invCaptureProvider) (*Server, *fakeInvRunner) {
	t.Helper()
	f := newFakeInvRunner(invTestT0)
	s := invHandlerServer(t, p, f)
	s.tempo = tempo.New()
	s.tempo.Configure(tempo.Settings{Enabled: true, BaseURL: defTempoTrace(t).URL})
	return s, f
}

func defCall(s *Server, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-trace/"+invTestTrace+query, strings.NewReader(body))
	r.SetPathValue("id", invTestTrace)
	w := httptest.NewRecorder()
	s.copilotExplainTrace(w, r)
	return w
}

func defAnswer(t *testing.T, w *httptest.ResponseRecorder) (map[string]any, []sseFrame) {
	t.Helper()
	frames := parseSSE(t, w.Body.String())
	for _, fr := range frames {
		if fr.event == "step" || fr.event == "step-result" {
			t.Fatalf("klasik varsayılanda adım olayı: %v", fr)
		}
	}
	for _, fr := range frames {
		if fr.event == "answer" {
			return fr.data, frames
		}
	}
	t.Fatalf("answer çerçevesi yok (%d): %s", w.Code, w.Body.String())
	return nil, nil
}

// lastMessages — sahte LLM'e giden son isteğin system / user içerikleri.
func (p *invCaptureProvider) lastMessages(t *testing.T) (system, user string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) == 0 {
		t.Fatal("LLM'e istek gitmedi")
	}
	msgs, _ := p.bodies[len(p.bodies)-1]["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		c, _ := mm["content"].(string)
		switch mm["role"] {
		case "system":
			system = c
		case "user":
			user = c
		}
	}
	return system, user
}

const (
	defAnswerA = "**İşlem Akışı ve Veri Özeti**\n- payments POST /charge card declined döndü.\n\n"
	defAnswerB = "**Kök Neden ve Sonraki Adım**\n- Kart reddi payments'ta. payments loglarına bak."
)

// Varsayılan (kodsuz) istek: klasik istem + klasik anahtar, akan; inceleme
// okuması yok; `?span=` yok sayılır (aynı klasik satır isabet eder); eski
// inceleme önbellek satırları hiç okunmaz.
func TestExplainTraceDefaultIsClassic(t *testing.T) {
	p := newInvCaptureProvider(t, defAnswerA, defAnswerB)
	s, f := defServer(t, p)
	mc := s.cache.(*memCache)
	// v0.10.948–1035 döneminden kalmış inceleme satırları (kodsuz ve span odaklı):
	// farklı anahtar → klasik yol bunları ASLA servis etmez.
	ctx := context.Background()
	for _, span := range []string{"", invTestPaySpan} {
		s.explainCacheSet(ctx, traceInvestigationCacheKey(copilot.SystemPromptTraceInvestigation(), invTestTrace, span), "ESKİ İNCELEME CEVABI", "xid-eski")
	}

	first, frames := defAnswer(t, defCall(s, "?stream=1&span="+invTestPaySpan, ""))
	if n := f.callCount(); n != 0 {
		t.Fatalf("varsayılan yol inceleme okuması koştu (%d araç çağrısı)", n)
	}
	if p.requests() != 1 {
		t.Fatalf("LLM çağrısı = %d; 1", p.requests())
	}
	system, user := p.lastMessages(t)
	if system != copilot.SystemPromptTrace() {
		t.Errorf("sistem istemi SystemPromptTrace değil (ilk 80: %.80q)", system)
	}
	if !strings.HasPrefix(user, "Trace "+invTestTrace+" with 2 spans") || !strings.Contains(user, "card declined") {
		t.Errorf("user istemi klasik kanıt paketi değil: %.200q", user)
	}
	if strings.Contains(user, "## [K]") || strings.Contains(user, "[T1]") {
		t.Error("user istemi inceleme bölümleri taşıyor")
	}

	model := defAnswerA + defAnswerB
	var deltas strings.Builder
	for _, fr := range frames {
		if fr.event == "delta" {
			deltas.WriteString(fr.data["text"].(string))
		}
	}
	text, _ := first["text"].(string)
	if text != model || deltas.String() != model {
		t.Errorf("cevap model metni olmalı (künye/sayı uyarısı yok):\ntext=%q\ndelta=%q", text, deltas.String())
	}
	if frames[len(frames)-1].event != "done" {
		t.Error("done çerçevesi yok")
	}
	if _, ok := first["sources"]; ok {
		t.Error("klasik cevapta sources (Kaynak durumu künyesi) var")
	}
	if code, ok := first["code"]; !ok || code != nil {
		t.Errorf("code = %v (var=%v); kodsuz istekte nil", code, ok)
	}
	if _, ok := first["oracleRows"]; !ok {
		t.Error("oracleRows eki yok (klasik traceExplainExtra)")
	}
	ev, _ := first["evidenceSpanIds"].([]any)
	hasPay := false
	for _, id := range ev {
		hasPay = hasPay || id == invTestPaySpan
	}
	if !hasPay {
		t.Errorf("evidenceSpanIds = %v; hata span'i (waterfall kutulaması) yok", first["evidenceSpanIds"])
	}
	if xid, _ := first["exchangeId"].(string); xid == "" || xid == "xid-eski" {
		t.Errorf("exchangeId = %q", xid)
	}

	// Klasik anahtar: explainCacheKey(SystemPromptTrace(), in.User, "").
	in, err := s.buildTraceExplainInput(ctx, invTestTrace)
	if err != nil {
		t.Fatal(err)
	}
	mc.mu.Lock()
	_, stored := mc.m[explainCacheKey(copilot.SystemPromptTrace(), in.User, "")]
	var invMeta []string
	for k := range mc.m {
		if strings.HasSuffix(k, ":inv") {
			invMeta = append(invMeta, k)
		}
	}
	mc.mu.Unlock()
	if !stored {
		t.Error("cevap klasik anahtara yazılmadı")
	}
	if len(invMeta) != 0 {
		t.Errorf("inceleme yan kaydı yazıldı: %v", invMeta)
	}

	// Seçili span yok sayılır: span'siz istek AYNI klasik satıra isabet eder.
	hit, _ := defAnswer(t, defCall(s, "?stream=1", ""))
	if p.requests() != 1 || f.callCount() != 0 {
		t.Fatalf("isabette LLM/okuma koştu (llm=%d araç=%d)", p.requests(), f.callCount())
	}
	if hit["cached"] != true || hit["text"] != model || hit["exchangeId"] != first["exchangeId"] {
		t.Errorf("isabet: cached=%v xid=%v/%v text=%q", hit["cached"], hit["exchangeId"], first["exchangeId"], hit["text"])
	}
	if ev, _ := hit["evidenceSpanIds"].([]any); len(ev) == 0 {
		t.Error("isabette waterfall kutulaması kayboldu")
	}
	if _, ok := hit["sources"]; ok {
		t.Error("isabette sources var")
	}
}

// Buffered (bayraksız) istek: gövde bugünkü JSON — explanation = model metni,
// evidenceSpanIds dolu, sources yok; inceleme okuması yok.
func TestExplainTraceDefaultBufferedBody(t *testing.T) {
	p := newInvCaptureProvider(t, defAnswerA+defAnswerB)
	s, f := defServer(t, p)
	w := defCall(s, "", "")
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("buffered gövde JSON değil: %v (%s)", err, w.Body.String())
	}
	if body["explanation"] != defAnswerA+defAnswerB {
		t.Errorf("explanation = %q", body["explanation"])
	}
	if ev, _ := body["evidenceSpanIds"].([]any); len(ev) == 0 {
		t.Error("buffered gövdede evidenceSpanIds yok")
	}
	if _, ok := body["sources"]; ok {
		t.Error("buffered gövdede sources var")
	}
	if f.callCount() != 0 {
		t.Errorf("inceleme okuması koştu (%d)", f.callCount())
	}
	if sys, _ := p.lastMessages(t); sys != copilot.SystemPromptTrace() {
		t.Error("buffered yolda sistem istemi SystemPromptTrace değil")
	}
}

// "Kodu da incele" değişmedi (v0.10.1035): klasik + kod bağlamı, kodlu istem,
// buffered üretim (delta yok); inceleme okuması yok. Kod entegrasyonu yok →
// dürüst gerekçe code.reason'da.
func TestExplainTraceCodePathStillClassicWithCode(t *testing.T) {
	p := newInvCaptureProvider(t, defAnswerA+defAnswerB)
	s, f := defServer(t, p)
	ans, frames := defAnswer(t, defCall(s, "?stream=1", `{"includeCode":true}`))
	if f.callCount() != 0 {
		t.Errorf("kod yolu inceleme okuması koştu (%d)", f.callCount())
	}
	for _, fr := range frames {
		if fr.event == "delta" {
			t.Fatal("kod yolu akmamalı (explainPromptBuffered)")
		}
	}
	if sys, _ := p.lastMessages(t); sys != copilot.SystemPromptTraceWithCode() {
		t.Errorf("kod yolunun sistem istemi SystemPromptTraceWithCode değil (ilk 80: %.80q)", sys)
	}
	code, _ := ans["code"].(map[string]any)
	if code == nil || code["reason"] != "kod entegrasyonu yapılandırılmamış" {
		t.Errorf("code = %v; yapılandırılmamış gerekçesi", ans["code"])
	}
	if ev, _ := ans["evidenceSpanIds"].([]any); len(ev) == 0 {
		t.Error("kod yolunda evidenceSpanIds yok")
	}
}

// 404 + erişilemezlik (kaynak pini): kodsuz dal klasik hazırlığı çağırır ve
// hatasını writeExplainPrepareErr'e verir (errExplainTraceNotFound → düz metin
// 404, ilk bayttan önce); buildTraceExplainInput sıfır span'de (Tempo ve CH
// ıskası) o hatayı döner; üretim kodunda explainTraceInvestigation'ı çağıran yok.
func TestExplainTraceDefaultNotFoundAndInvestigationUnreachable(t *testing.T) {
	body, ok := serverFuncBodies(t)["copilotExplainTrace"]
	if !ok {
		t.Fatal("copilotExplainTrace bulunamadı")
	}
	code := stripGoComments(body)
	iBranch := strings.Index(code, "if !opts.IncludeCode {")
	iPrep := strings.Index(code, "s.explainTraceClassicPrepared(r)")
	iErr := strings.Index(code, "writeExplainPrepareErr(w, err)")
	iDeliver := strings.Index(code, "s.deliverExplain(w, r, xid, p.extra, p.run, p.service, p.cacheKey)")
	if iBranch < 0 || iPrep < iBranch || iErr < iPrep || iDeliver < iErr {
		t.Errorf("kodsuz dal: klasik hazırlık → writeExplainPrepareErr → deliverExplain sırası yok (%d %d %d %d)", iBranch, iPrep, iErr, iDeliver)
	}
	in := flatWS(stripGoComments(readSourceFile(t, "explain_trace_input.go")))
	if !strings.Contains(in, "if len(spans) == 0 { return traceExplainInput{}, errExplainTraceNotFound }") {
		t.Error("buildTraceExplainInput sıfır span'de errExplainTraceNotFound dönmüyor")
	}
	for _, err := range []error{errExplainTraceNotFound, fmt.Errorf("sarılı: %w", errExplainTraceNotFound)} {
		w := httptest.NewRecorder()
		writeExplainPrepareErr(w, err)
		if w.Code != http.StatusNotFound || strings.TrimSpace(w.Body.String()) != "trace not found" {
			t.Errorf("%v → %d %q; düz metin 404", err, w.Code, w.Body.String())
		}
	}
	other := httptest.NewRecorder()
	writeExplainPrepareErr(other, errors.New("kaynak okunamadı"))
	if other.Code == http.StatusNotFound {
		t.Error("başka hata 404'e çevrildi")
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stripGoComments(string(b)), "s.explainTraceInvestigation(") {
			t.Errorf("%s explainTraceInvestigation'ı çağırıyor — v0.10.1036'dan beri uçtan erişilemez olmalı", f)
		}
	}
}
