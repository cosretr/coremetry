package api

// trace_investigate_code.go — v0.10.1034 (operatör-bildirimli, prod: "Kod
// inceleme çalışma mantığı ile direkt Ask CoSRE farklı."). Ask CoSRE paneli
// trace'te iki AYRI kanıt hattıyla cevap veriyordu: varsayılan cevap trace
// incelemesinden (trace_investigate.go: get_trace + loglar + dönem kıyası + pod
// + deploy + Oracle, seçili span'e odaklı), "Kodu da incele" ise eski klasik
// toplayıcıdan (buildTraceExplainInput: ≤100 span + ≤15 log; kıyas/pod/deploy
// yok, seçili span'i yok sayar). Operatör ikisini farklı davranırken görüyordu.
//
// Şimdi TEK hat: "Kodu da incele" = aynı inceleme (aynı okumalar, aynı adımlar,
// aynı odak, inv.User bayt bayt aynı) + üstüne kod. Kod çekicisinin girdisi:
//   - stack + onu basan servis: incelemenin get_logs_for_trace okumasının HAM
//     kayıtları (dikiş: mcptools.WithTraceLogsSink — araç çıktısı stack'i
//     keser); klasik seçim kuralı + 15 satır tavanı (traceLogStack).
//   - Seçili span'in logunda stack YOKSA (ör. log basmayan CLIENT yaprağı, ya
//     da exception'ı aşağı akıştaki servis basmış): TEK ek trace geneli okuma
//     (invTraceStackRead; adım olarak görünür, audit'li). Çıktısı inv.User'a
//     GİRMEZ; yalnız stack + servis alınır ve kökeni hem prompt'un kod
//     bölümünde hem kod künyesinde (stackOrigin) tek satırla SÖYLENİR.
//   - hata metni + SQL: get_trace'in span listesi (invSpanErrorEvidence; stack
//     belli olduktan SONRA).
// Kod çekimi kendi adımıyla yayınlanır (invToolCode; önizlemede yalnız künye —
// "kod tarayıcıya gitmez", copilot_code.go). Şema kanıtı korunur (bütçe sırası
// kod > şema > SQL > log, operatör direktifi 2026-08-28).
//
// Model çağrısı BUFFERED ve mevcut taşma zincirinden (copilotExplainEvidence:
// tam kod → yarım kod; yarıya inemezse kodsuz + "kod sığmadı" notu) DEĞİŞMEDEN
// geçer; incelemenin sunucu kuyruğu (sayı uyarısı + Kaynak durumu) cevabı yine
// kapatır. Sayı denetiminde cevaptaki çitli kod iddia sayılmaz, kanıta yalnız
// GERÇEKTEN gönderilen kodun satır referansları + şema bloğu eklenir. Kodsuz yol
// bugünkü gibi akar — iki taşma stratejisi aynı istekte hiç koşmaz.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

// invToolCode — v0.10.1034: kod çekiminin adım etiketi (invToolOracle gibi
// registry aracı DEĞİL; sunucunun kendi DevOps okuması).
const invToolCode = "source_code"

// traceLogStack — v0.10.1034, SAF: kod çekicisinin stack'i ve onu BASAN servis.
// Klasik toplayıcının seçim kuralı (buildTraceExplainInput): severity yüksekten
// (kararlı sıra), yalnız ilk traceExplainLogRows kayıt aday, stackparse.FromLog
// ile stack TAŞIYAN ilki — trace'in en ciddi hatası; stack HAM (kesilmemiş:
// frame'ler dosya+satır taşır). Servis kökün değil stack'i basanın
// (traceExplainInput.StackService gerekçesi). Girdi dilimi DEĞİŞMEZ — aracın
// kendi kayıtları (kopya sıralanır).
func traceLogStack(logs []*logstore.LogRecord) (stack, service string) {
	sorted := make([]*logstore.LogRecord, 0, len(logs))
	for _, lg := range logs {
		if lg != nil {
			sorted = append(sorted, lg)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Severity > sorted[j].Severity })
	if len(sorted) > traceExplainLogRows {
		sorted = sorted[:traceExplainLogRows] // klasik yol yalnız prompt'a giren 15 satıra bakar
	}
	for _, lg := range sorted {
		if st, _ := stackparse.FromLog(lg.Attributes, lg.Body); strings.TrimSpace(st) != "" {
			return st, lg.ServiceName
		}
	}
	return "", ""
}

// stackSink — v0.10.1034: L okumasının ham-kayıt kancası. İnceleme içinde yalnız
// L dalının goroutine'i yazar (okuma wg.Wait'ten sonra); ek okumada sıralı.
func (inv *traceInvestigation) stackSink() mcptools.TraceLogsSink {
	return func(logs []*logstore.LogRecord) {
		inv.Stack, inv.StackService = traceLogStack(logs)
	}
}

// invSpanErrorEvidence — v0.10.1034, SAF: şema kanıtının girdisi, klasik
// toplayıcının formülüyle (buildTraceExplainInput): hata span'lerinin durum
// mesajları (≤5) ve SQL ifadeleri (≤3, tekil, 600 rune). Liste get_trace'in
// span listesi (≤200, hata span'leri garantili, zaman sıralı).
func invSpanErrorEvidence(spans []invTraceSpan) (errTexts, dbStmts []string) {
	seen := map[string]bool{}
	for _, sp := range spans {
		if sp.StatusCode != "error" {
			continue
		}
		if sp.DBStatement != "" {
			st := truncRunesN(sp.DBStatement, 600)
			if len(dbStmts) < 3 && !seen[st] {
				seen[st] = true
				dbStmts = append(dbStmts, st)
			}
		}
		if len(errTexts) < 5 && sp.StatusMessage != "" {
			errTexts = append(errTexts, sp.StatusMessage)
		}
	}
	return errTexts, dbStmts
}

// codeSchemaInputs — v0.10.1034: şema kanıtının girdisi (hata metni + SQL),
// klasik formülle (traceExplainInput.ErrorText: durum mesajları + stack,
// ≤2000 rune). Stack belli OLDUKTAN sonra çağrılır (ek okuma onu değiştirebilir);
// yalnız kod istendiğinde — kodsuz incelemede hiç hesaplanmaz.
func (inv *traceInvestigation) codeSchemaInputs() (errorText string, dbStmts []string) {
	errTexts, dbStmts := invSpanErrorEvidence(inv.spans)
	return truncRunesN(strings.Join(errTexts, "\n")+"\n"+inv.Stack, 2000), dbStmts
}

// codeOutcomeTransient — v0.10.1034, SAF: kod çekiminin GEÇİCİ çıkmazı mı?
// Önbellek anahtarı okumalardan ÖNCE kurulduğu için (kanıt anahtara girmez)
// git sunucusunun anlık arızası bir saat boyunca "Kod okunamadı" diye
// donmasın: incelemenin cacheable kuralının kod kaynağına uzantısı (geçici
// kaynak arızası → saklanmaz). Kalıcı çıkmazlar (stack yok, depo çözülemedi,
// ağaçta yok, yapılandırılmamış) saklanabilir — yeniden okumak sonucu değiştirmez.
func codeOutcomeTransient(o devops.CodeOutcome) bool {
	switch o {
	case devops.CodeDeadline, devops.CodeCancelled, devops.CodeBackendError, devops.CodeCatalogError:
		return true
	}
	return false
}

// codeStepStatus — v0.10.1034, SAF: kod çekiminin adım rozeti (sourcestate
// sözlüğüyle): pencere geldi → ok (bütçe kesti → limitli), yapılandırılmamış,
// süre tavanı → zaman aşımı, DevOps hatası → erişilemedi, iptal/katalog/sınıfsız
// → hata; gerisi (stack yok, depo/proje çözülemedi, ağaçta yok) → boş.
func codeStepStatus(cc devops.CodeContext) sourcestate.Status {
	st := sourcestate.Status{Source: "code", Backend: "devops", Returned: len(cc.Windows), Detail: invCapRunes(cc.Reason, 200)}
	switch {
	case len(cc.Windows) > 0 && cc.Outcome == devops.CodePartial:
		st.State = sourcestate.Truncated
	case len(cc.Windows) > 0:
		st.State = sourcestate.OK
	case cc.Outcome == devops.CodeUnconfigured:
		st.State = sourcestate.NotConfigured
	case cc.Outcome == devops.CodeDeadline:
		st.State = sourcestate.Timeout
	case cc.Outcome == devops.CodeBackendError:
		st.State = sourcestate.Unreachable
	case cc.Outcome == devops.CodeCancelled, cc.Outcome == devops.CodeCatalogError, cc.Outcome == devops.CodeOther:
		st.State = sourcestate.Error
	default:
		st.State = sourcestate.Empty
	}
	return st
}

// fencedCodeRe — çitli kod bloğu (kapanmamışsa metnin sonuna kadar).
var fencedCodeRe = regexp.MustCompile("(?s)```.*?(?:```|$)")

// maskFencedCode — v0.10.1034, SAF: cevaptaki çitli kod blokları sayı iddiası
// DEĞİL (alıntılanan kodun satır numaraları, sabitleri; ExpandQuotes'un
// eklediği bağlam satırları). Denetimden önce boşlukla değiştirilir.
func maskFencedCode(s string) string { return fencedCodeRe.ReplaceAllString(s, " ") }

// codeRefEvidence — v0.10.1034, SAF: sayı denetiminin kod kanıtı. Kod
// GÖVDESİ değil (pencerenin her satır numarası uydurma bir "250 ms"e dayanak
// olurdu), yalnız modelin meşru olarak andığı referanslar: pencere başına hata
// satırı, ilk/son satır ve (varsa) pencere dışı imza satırı. Yalnız GERÇEKTEN
// gönderilen kod (tam / yarım; düştüyse boş) — copilotExplainEvidenceSent.
func codeRefEvidence(cc devops.CodeContext) string {
	var b strings.Builder
	for _, w := range cc.Windows {
		fmt.Fprintf(&b, "\nkod satır referansı: %d %d %d", w.Line, w.FromLine, w.ToLine)
		if w.SignatureLine > 0 {
			fmt.Fprintf(&b, " %d", w.SignatureLine)
		}
	}
	return b.String()
}

// codeStackOriginNotes — v0.10.1034: stack seçili span'in DEĞİL (trace geneli ek
// okumadan) — prompt'un kod bölümüne ve kod künyesine giden TEK satır.
func codeStackOriginNotes(inv *traceInvestigation) (prompt, frame string) {
	svc := invInline(inv.StackService, 80)
	prompt = fmt.Sprintf("\n\nKOD KAYNAĞI (sunucu notu): seçili span %s'in loglarında stacktrace YOK; aşağıdaki kod bağlamı trace genelindeki en ciddi stacktrace'ten çekildi (basan servis: %s). Bu stack'i seçili span'in bastığını varsayma.", inv.SpanID, svc)
	frame = fmt.Sprintf("Kod seçili span'in değil, trace'in en ciddi stacktrace'inden — basan servis: %s (seçili span'in loglarında stacktrace yok).", svc)
	return prompt, frame
}

// invTraceStackRead — v0.10.1034: seçili span'in logunda stack yoksa TEK ek
// okuma: trace geneli get_logs_for_trace (span süzgeci yok, klasik limit),
// incelemenin koşucusu (rol süzgeci + audit) ve aynı ham-kayıt kancasıyla;
// step + step-result yayınlanır. Çıktısı inceleme kanıtına (inv.User) GİRMEZ.
func invTraceStackRead(ctx context.Context, run invToolRunner, emit func(string, any), inv *traceInvestigation) *invSection {
	sec := newInvSection("L", "Loglar (trace geneli — kod için)", invToolLogs,
		map[string]any{"trace_id": inv.TraceID, "limit": traceExplainLogLimit}, invBudgetLogs, invRunesL)
	sec.step = emitStepChip(emit, sec.Tool, string(sec.Args))
	sec.Called = true
	cctx := mcptools.WithTraceLogsSink(mcptools.WithAnchor(ctx, inv.CmpTo), inv.stackSink())
	sec.Outcome = run(cctx, sec.Tool, sec.Args, sec.Budget)
	invParseSection(sec)
	invEmitStepResult(emit, sec)
	return sec
}

// invCodeStep — v0.10.1034: kod çekimi bir inceleme ADIMI (en çok 25 sn sürer;
// akış sessiz kalmasın). Önizleme yalnız künye (depo, dosya:satır, gerekçe) —
// kod içeriği tarayıcıya gitmez.
func (s *Server) invCodeStep(ctx context.Context, emit func(string, any), inv *traceInvestigation) devops.CodeContext {
	sec := &invSection{Tool: invToolCode}
	sec.step = emitStepChip(emit, invToolCode, string(invArgs(map[string]any{"service": inv.StackService})))
	t0 := time.Now()
	cc := s.buildCodeContext(ctx, inv.StackService, inv.Stack)
	st := codeStepStatus(cc)
	sec.Statuses = []sourcestate.Status{st}
	content, err := json.Marshal(map[string]any{"source": st, "code": codePayload(cc, true)})
	if err != nil {
		content = []byte("{}")
	}
	sec.Outcome = agenttools.Outcome{Executed: true, Duration: time.Since(t0), Kind: agenttools.KindOK, Content: string(content)}
	invEmitStepResult(emit, sec)
	return cc
}

// traceInvestigationCodePrepared — v0.10.1034: "Kodu da incele"nin üretim
// yarısı. İnceleme bitti (adımlar aktı); gerekirse trace geneli stack okuması,
// sonra kod adımı + şema kanıtı; model buffered + mevcut taşma zinciriyle.
// Önbellek: anahtar okumalardan ÖNCE (traceInvestigationCacheKey, kodlu sistem
// istemiyle — kodlu/kodsuz aynı satırı paylaşmaz); yan kayıt kod künyesini de
// taşır (isabette git sunucusuna gidilmez). Saklanmaz: incelemenin cacheable
// kuralı tutmuyorsa, ek stack okuması kalıcı değilse (zaman aşımı, erişilemedi…)
// ya da kod çekimi geçici çıkmazdaysa.
func (s *Server) traceInvestigationCodePrepared(r *http.Request, run invToolRunner, emit func(string, any), inv *traceInvestigation, key string) explainPrepared {
	if emit == nil {
		emit = func(string, any) {}
	}
	ctx := r.Context()
	settled := true
	user, origin := inv.User, ""
	if inv.SpanID != "" && inv.Stack == "" && run != nil {
		sec := invTraceStackRead(ctx, run, emit, inv)
		settled = invStatusesSettled(sec.Statuses)
		if inv.Stack != "" {
			var note string
			note, origin = codeStackOriginNotes(inv)
			user += note // inv.User DEĞİŞMEZ; not yalnız kod bölümünün başında
		}
	}
	errorText, dbStmts := inv.codeSchemaInputs()
	cc := s.invCodeStep(ctx, emit, inv)
	se := s.buildSchemaEvidence(errorText, dbStmts, mapperBlocks(cc))
	meta := inv.cacheMeta()
	meta.Code = codePayload(cc, true)
	meta.Code.StackOrigin = origin
	p := explainPrepared{extra: meta.frameExtra(), run: s.invCodeRun(r, inv, user, cc, se), service: inv.RootService}
	if settled && inv.cacheable(time.Now()) && !codeOutcomeTransient(cc.Outcome) {
		p.cacheKey = key
		p.onStore = func(ctx context.Context) { s.traceInvestigationMetaSet(ctx, key, meta) }
	}
	return p
}

// invCodeRun — v0.10.1034: kod varyantının üretimi. Gerçek prompt = user
// (inceleme user bloğu [+ kök notu]) + kod bloğu + şema bloğu
// (copilotExplainEvidence'ın sırası); sistem kodlu inceleme istemi, taşmada
// kodsuz düşüş düz inceleme istemi. Akmaz (explainPromptBuffered — yarısı akmış
// cevabın üstüne yeniden deneme yazılmaz); kuyruk yalnız answer.text'te
// (sıfır-delta sözleşmesi). Sayı denetiminin kod kanıtı, cevabı ÜRETEN denemede
// gönderilen koddan (tam / yarım / yok) kurulur.
func (s *Server) invCodeRun(r *http.Request, inv *traceInvestigation, user string, cc devops.CodeContext, se schemaEvidence) explainRun {
	inv.wantCode = true // kod varyantı: cevaptaki çitli kod iddia sayılmaz (answerTail)
	return invAnswerWithTail(explainPromptBuffered(func() (string, error) {
		out, sent, err := s.copilotExplainEvidenceSent(r,
			copilot.SystemPromptTraceInvestigation(), copilot.SystemPromptTraceInvestigationWithCode(), user, cc, se)
		inv.codeEvidence = codeRefEvidence(sent) + se.Block
		return out, err
	}), inv)
}
