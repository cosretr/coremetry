package api

// chat_explain_registry.go — v0.10.1405 (Tek CoSRE adım 2). Operatör:
//   "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
//    sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//
// v0.10.1400 trace / exception ✨ açıklamasını sohbetin SIRADAN bir turu
// yaptı; span, problem, incident, anomaly, servis sağlığı, runbook ve log
// satırı hâlâ CoSRE çekmecesinin "özne kipinde" (ikinci thread, ikinci
// composer) çiziliyordu. Bu dosya açıklama türlerinin TEK kaydıdır:
//
//   tür → {yüzey (explain-…:chat, aisurface kayıtlı), adım çipi, kurucu}
//
// Kurucular (build*Explain) ✨ HTTP düğmesinin KENDİ kurucularıdır
// (explain_span.go, explain_problem.go, explain_incident.go,
// explain_anomaly.go, explain_service_health.go, explain_runbook.go,
// copilot_explain_log.go): düğme ve sohbet AYNI explainBuilt'i kullanır,
// yani istem çifti ve önbellek anahtarı (explainCacheKey(system, user, ""))
// BAYT BAYT aynı → tek önbellek satırı; ikinci çağrı LLM'siz isabet
// (chat_explain_registry_test.go her tür için pinler). trace / exception
// v0.10.1400'ün kendi turlarıyla (chat_explain_turn.go) kayıtta durur.
//
// v0.10.1407 (Tek CoSRE adım 4) — charts (Service → Details grafik ✨'ları)
// ve log-spike (Logs histogramı ✨) da kayıtta: kurucuları
// buildChartsExplain / buildLogSpikeExplain (HTTP düğmesiyle ortak), cevabı
// ölçülmüş kanıtın tipli bloğuyla (charts_signals / log_spike) birlikte
// döner; künyenin renderer'ı "charts" / "log-spike" → istemci kartı çizer.
// Bu iki tür KANIT-ÖNCE: model düşse de blok + dürüst satır cevaptır.
//
// Endpoint kayıtta YOK: ayrı bir explain yüzeyi/istemi yok — istemci görünür
// soruyu (endpointAskQuestion) pencereyle gönderir, operasyon ön-getirmesi
// deterministik çözer (v0.10.1367 davranışı, çekmecesiz).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

const (
	explainAskSpan          = "span"
	explainAskProblem       = "problem"
	explainAskIncident      = "incident"
	explainAskAnomaly       = "anomaly"
	explainAskServiceHealth = "service-health"
	explainAskRunbook       = "runbook"
	explainAskLog           = "log"
	explainAskCharts        = "charts"    // v0.10.1407
	explainAskLogSpike      = "log-spike" // v0.10.1407
	// explainAskMetricPanel — v0.10.1435: explain_metric_panel.go.

	// explainRendererBody — künyenin çizim kipi: ExplainBody (Karar şeridi,
	// Kanıt satırı).
	explainRendererBody = "body"
	// v0.10.1407 — kanıt kartları: ChartsSignalsCard / LogSpikeCard (blok + metin).
	explainRendererCharts   = "charts"
	explainRendererLogSpike = "log-spike"
	// chatExplainLabelMax — künye etiketinin tavanı (rune).
	chatExplainLabelMax = 80
	// chatExplainNoteMax — künye notunun tavanı (rune; arşiv sanitize'ı da aynı).
	chatExplainNoteMax = 200
)

// explainRenderers — künye `renderer` beyaz listesi (arşiv sanitize'ı).
var explainRenderers = map[string]bool{explainRendererBody: true, explainRendererCharts: true, explainRendererLogSpike: true,
	explainRendererMetricPanel: true} // v0.10.1435

// explainBuilt — bir ✨ açıklamasının üretim girdisi + künye malzemesi. HTTP
// düğmesi ve sohbet turu AYNI değeri kullanır (anahtar tek yerde hesaplanır).
type explainBuilt struct {
	System   string
	User     string
	CacheKey string // explainCacheKey(System, User, "") — düğmenin formülü
	Label    string // künye etiketi (kural adı, olay başlığı, span adı, "RUNBOOK"…)
	Note     string // künye notu (runbook: "Based on N past resolved…")
	Service  string // kimlik köprüsünün ortamı (answerRequestIDLinks)
	Links    []guidedAnswerLink
	// Extra — HTTP gövdesinin ek alanları (similarCount, matched); sohbette yok.
	Extra map[string]any
	// Renderer — v0.10.1407: künyenin çizim kipi (boş = body).
	Renderer string
	// Blocks — v0.10.1407: cevapla birlikte yayılan tipli kanıt blokları
	// (charts_signals / log_spike). Doluysa tür KANIT-ÖNCE: model düşse de
	// bloklar + dürüst satır cevaptır (buildChartsResult sözleşmesi).
	Blocks []explainBlock
	// Unexplained — v0.10.1407: anlatılacak ölçüm yok (log-spike kanıtı
	// okunamadı); LLM çağrılmaz, bu metin cevaptır (bloklar yine yayılır).
	Unexplained string
	// Followups — v0.10.1435: cevabın takip çipleri (metric_panel: genişlet /
	// kıyasla / kırılım / aralığın trace-log linkleri); boşsa yok.
	Followups []chatFollowup
	// Appendix — v0.10.2014: model cevabından SONRA eklenen deterministik bölüm
	// (log satırının runbook eşleşmeleri). Önbelleğe girmez: her turda kurucu
	// yeniden hesaplar; boşsa hiçbir şey eklenmez.
	Appendix string
}

// explainBlock — v0.10.1407: kurucunun tipli bloğu (blocks.Type* + gövde).
type explainBlock struct {
	Type    string
	Payload any
}

// newExplainBuilt — istem çifti + düğmenin anahtar formülü.
func newExplainBuilt(system, user string) explainBuilt {
	return explainBuilt{System: system, User: user, CacheKey: explainCacheKey(system, user, "")}
}

// explainNotFoundError — açıklanacak özne yok. msg HTTP gövdesinin bugünkü
// metni ("problem not found", …); jsonBody → writeJSONError (log satırı).
type explainNotFoundError struct {
	msg      string
	jsonBody bool
}

func (e *explainNotFoundError) Error() string { return e.msg }

func explainNotFound(msg string) error { return &explainNotFoundError{msg: msg} }

// explainBadArgsError — v0.10.1407: öznenin argümanları geçersiz (log-spike
// penceresi / kipi). HTTP 400 JSON (v0.10.1284 gövdesiyle birebir); sohbette
// LLM'siz dürüst cevap.
type explainBadArgsError struct{ msg string }

func (e *explainBadArgsError) Error() string { return e.msg }

func explainBadArgs(msg string) error { return &explainBadArgsError{msg: msg} }

// writeExplainBuildErr — kurucu hatası, ilk bayttan önce: bulunamadı → 404
// (gövde v0.10.1404'teki handler'larla birebir), gerisi writeErr.
func writeExplainBuildErr(w http.ResponseWriter, err error) {
	var nf *explainNotFoundError
	if errors.As(err, &nf) {
		if nf.jsonBody {
			writeJSONError(w, http.StatusNotFound, nf.msg)
			return
		}
		http.Error(w, nf.msg, http.StatusNotFound)
		return
	}
	var ba *explainBadArgsError
	if errors.As(err, &ba) {
		writeJSONError(w, http.StatusBadRequest, ba.msg)
		return
	}
	writeErr(w, err)
}

// explainStore — kurucuların okuduğu ClickHouse yüzeyi (*chstore.Store bunu
// karşılar). Ayrı arayüz yalnız test için: düğme ↔ sohbet önbellek paylaşımı
// canlı CH olmadan uçtan uca sınanır (explainStoreSeam).
type explainStore interface {
	GetProblem(ctx context.Context, id string) (*chstore.Problem, error)
	EnrichProblemsWithDeploys(ctx context.Context, problems []chstore.Problem, lookback time.Duration) []chstore.Problem
	GetHypothesis(ctx context.Context, anchorKind, anchorID string) (*chstore.RootCauseHypothesis, error)
	// v0.10.1410 — komşular topoloji özetinden (serviceNeighborsRollup);
	// eski ServiceNeighbors (ham spans, DB/dış çağrıya kör) silindi.
	ReadServiceTopologyAggForFocus(ctx context.Context, from, to time.Time, focus string, hops, limit int) ([]chstore.ServiceTopologyEdge, error)
	GetTraces(ctx context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error)
	GetIncident(ctx context.Context, id string) (*chstore.Incident, error)
	IncidentProblems(ctx context.Context, incidentID string) ([]string, error)
	ListProblems(ctx context.Context, f chstore.ProblemFilter) ([]chstore.Problem, error)
	GetAnomalyEvent(ctx context.Context, id string, activeAge time.Duration) (*chstore.AnomalyEvent, error)
	GetServiceDeploys(ctx context.Context, service string, from, to time.Time) ([]chstore.Deploy, error)
	QuerySpanMetricMulti(ctx context.Context, f chstore.SpanMetricBatchFilter) (map[string][]chstore.SpanMetricSeries, int, error)
	FindSimilarResolvedProblems(ctx context.Context, service, ruleID string, limit int) ([]chstore.Problem, error)
	// v0.10.1407 — grafik kanıtı (buildServiceChartsInput).
	// v0.10.2081 — eski olaylar + ML olayları (chstore.ListAnomalyFeed).
	ListAnomalyFeed(ctx context.Context, f chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error)
	GetOperationSummaryCompared(ctx context.Context, service string, since time.Duration, from, to time.Time, normalized bool, env string) ([]chstore.OperationSummary, error)
}

// explainStoreSeam — yalnız testler; üretimde nil (s.store okunur).
var explainStoreSeam explainStore

func (s *Server) explainReads() explainStore {
	if explainStoreSeam != nil {
		return explainStoreSeam
	}
	return s.store
}

// explainKind — kayıttaki bir açıklama türü.
type explainKind struct {
	Kind    string
	Surface string // ai_calls yüzeyi (aisurface kayıtlı; Eval ailesi HTTP yüzeyiyle aynı)
	Step    string // adım çipinin araç adı
	// Turn — kendi turu olan türler (trace / exception, v0.10.1400 kodu aynen).
	Turn func(s *Server, ctx context.Context, emit func(string, any), ask chatExplainAsk, loc *time.Location) (bool, bool)
	// Build — genel tur: düğmenin kurucusu.
	Build func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error)
}

// explainKindFor — türün kaydı. switch (paket düzeyi tablo değil): kurucular
// sohbet koduna geri dönse bile ilklendirme döngüsü doğmaz.
func explainKindFor(kind string) (explainKind, bool) {
	switch kind {
	case explainAskTrace:
		return explainKind{Kind: kind, Surface: aisurface.ExplainTraceChat, Step: "trace",
			Turn: func(s *Server, ctx context.Context, emit func(string, any), ask chatExplainAsk, _ *time.Location) (bool, bool) {
				return s.chatExplainTraceTurn(ctx, emit, ask)
			}}, true
	case explainAskException:
		return explainKind{Kind: kind, Surface: aisurface.ExplainExceptionChat, Step: opExceptionExplainTool,
			Turn: func(s *Server, ctx context.Context, emit func(string, any), ask chatExplainAsk, loc *time.Location) (bool, bool) {
				return s.chatExplainExceptionTurn(ctx, emit, ask, loc)
			}}, true
	case explainAskSpan:
		return explainKind{Kind: kind, Surface: aisurface.ExplainSpanChat, Step: "span",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				return s.buildSpanExplain(ctx, subj.ID, subj.SpanID)
			}}, true
	case explainAskProblem:
		return explainKind{Kind: kind, Surface: aisurface.ExplainProblemChat, Step: "problem",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				return s.buildProblemExplain(ctx, subj.ID)
			}}, true
	case explainAskIncident:
		return explainKind{Kind: kind, Surface: aisurface.ExplainIncidentChat, Step: "incident",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				return s.buildIncidentExplain(ctx, subj.ID)
			}}, true
	case explainAskAnomaly:
		return explainKind{Kind: kind, Surface: aisurface.ExplainAnomalyChat, Step: "anomaly",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				return s.buildAnomalyExplain(ctx, subj.ID)
			}}, true
	case explainAskServiceHealth:
		return explainKind{Kind: kind, Surface: aisurface.ExplainServiceHealthChat, Step: "service_health",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				return s.buildServiceHealthExplain(ctx, subj.ID, subj.From, subj.To)
			}}, true
	case explainAskRunbook:
		return explainKind{Kind: kind, Surface: aisurface.ExplainRunbookChat, Step: "runbook",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				return s.buildRunbookExplain(ctx, subj.ID)
			}}, true
	case explainAskLog:
		return explainKind{Kind: kind, Surface: aisurface.ExplainLogChat, Step: "log_line",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				// copilotExplainLog'un gövdesiyle aynı alanlar (istemci env göndermez).
				body, err := validateLogLineBody(logLineExplainBody{TsNs: subj.TsNs, Service: subj.ID, Pod: subj.Pod, TraceID: subj.TraceID, Hash: subj.Hash})
				if err != nil {
					return explainBuilt{}, explainNotFound(err.Error())
				}
				return s.buildLogLineExplain(ctx, body)
			}}, true
	case explainAskCharts: // v0.10.1407
		return explainKind{Kind: kind, Surface: aisurface.ExplainChartsChat, Step: "service_charts",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				b, _ := s.buildChartsExplain(ctx, subj.ID, subj.Scope, subj.From, subj.To)
				return b, nil
			}}, true
	case explainAskLogSpike: // v0.10.1407
		return explainKind{Kind: kind, Surface: aisurface.ExplainLogSpikeChat, Step: "log_spike",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				// copilotExplainLogSpike'ın gövdesiyle aynı alanlar (kodek → gövde).
				b, _, err := s.buildLogSpikeExplain(ctx, logSpikeBodyFromSubject(subj))
				return b, err
			}}, true
	case explainAskMetricPanel: // v0.10.1435 — panelden "Bu grafiği açıkla"
		return explainKind{Kind: kind, Surface: aisurface.ExplainMetricPanelChat, Step: "metric_panel",
			Build: func(ctx context.Context, s *Server, subj aiSubject) (explainBuilt, error) {
				return s.buildMetricPanelExplain(ctx, subj)
			}}, true
	}
	return explainKind{}, false
}

// logSpikeBodyFromSubject — SAF (v0.10.1407): `log-spike` kodeği → HTTP
// gövdesi (LogsHistogram'ın gönderdiği alanlar birebir).
func logSpikeBodyFromSubject(subj aiSubject) logSpikeExplainBody {
	return logSpikeExplainBody{Service: subj.ID, Env: subj.Env, Query: subj.Q, Level: subj.Level,
		Mode: subj.Mode, FromNs: subj.From.UnixNano(), ToNs: subj.To.UnixNano()}
}

// explainTurnKinds — kayıttaki türler (testler + sanitize belgesi).
var explainTurnKinds = []string{explainAskTrace, explainAskException, explainAskSpan, explainAskProblem,
	explainAskIncident, explainAskAnomaly, explainAskServiceHealth, explainAskRunbook, explainAskLog,
	explainAskCharts, explainAskLogSpike, explainAskMetricPanel}

// explainKindTR — bulunamadı cevabındaki tür adı.
var explainKindTR = map[string]string{
	explainAskSpan: "Span", explainAskProblem: "Problem", explainAskIncident: "Olay",
	explainAskAnomaly: "Anomali", explainAskServiceHealth: "Servis", explainAskRunbook: "Problem",
	explainAskMetricPanel: "Panel", // v0.10.1435
}

// chatExplainRegistryTurn — kurucu tabanlı genel açıklama turu: adım çipi →
// düğmenin kurucusu → streamExplainCore (önbellek → akan üretim → sakla) →
// answer çerçevesi + künye. Bulunamayan özne → LLM'siz dürüst cevap.
func (s *Server) chatExplainRegistryTurn(ctx context.Context, emit func(string, any), k explainKind, ask chatExplainAsk, loc *time.Location) (bool, bool) {
	subj, ok := explainAskSubject(ask)
	if !ok { // normalizeExplainAsk geçtiyse olamaz; savunma
		return false, false
	}
	// v0.10.1435 — kodek dışı alanlar: yapısal yük (metric_panel) + tz.
	subj.Panel, subj.Loc = ask.Panel, loc
	args, _ := json.Marshal(map[string]any{"kind": ask.Kind, "id": ask.ID})
	n := emitGuidedStep(emit, k.Step, string(args))
	b, err := k.Build(ctx, s, subj)
	if err != nil {
		var nf *explainNotFoundError
		var ba *explainBadArgsError
		switch {
		case errors.As(err, &nf):
			emitGuidedStepResult(emit, n, k.Step, "", errExplainTurnNotFound)
			emit("answer", map[string]any{"text": explainTurnNotFoundTR(ask), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
		case errors.As(err, &ba): // v0.10.1407 — geçersiz argüman: LLM'siz dürüst cevap
			emitGuidedStepResult(emit, n, k.Step, "", err)
			emit("answer", map[string]any{"text": "Açıklama kurulamadı — " + ba.msg + ".", "exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
		default:
			emitGuidedStepResult(emit, n, k.Step, "", err)
			emit("error", map[string]string{"error": err.Error()})
		}
		return true, false
	}
	if b.Unexplained != "" { // v0.10.1407 — ölçüm okunamadı: adım çipi hatalı
		emitGuidedStepResult(emit, n, k.Step, "", errors.New(b.Unexplained))
	} else {
		emitGuidedStepResult(emit, n, k.Step, b.User, nil)
	}
	renderer := explainRendererBody
	if b.Renderer != "" {
		renderer = b.Renderer
	}
	meta := chatExplainMeta{Kind: ask.Kind, ID: ask.ID, Subject: ask.Subject, Surface: k.Surface,
		Renderer: renderer, Label: clipRunes(strings.TrimSpace(b.Label), chatExplainLabelMax),
		Note: clipRunes(strings.TrimSpace(b.Note), chatExplainNoteMax)}
	// v0.10.1407 — kanıt blokları cevaptan ÖNCE (kart ölçülmüş veriyle hemen
	// dolar; withBlockSeq sohbetin sıralayıcısına bağlar).
	for _, bl := range b.Blocks {
		emit(bl.Type, bl.Payload)
	}
	if b.Unexplained != "" { // ölçüm yok → LLM yok, dürüst cevap (blok yine çizilir)
		emit("answer", withAnswerFollowups(map[string]any{"text": b.Unexplained, "exchangeId": copilot.MetaFromContext(ctx).ExchangeID, "explain": meta}, b.Followups))
		return true, false
	}
	// Düğmenin kurucusundan gelen anahtar (explainCacheKey(system, user, "")).
	// v0.10.1407 — kanıt-önce türde model hatası turu düşürmez: `error` yerine
	// bloklar + dürüst satır (HTTP buildChartsResult sözleşmesinin sohbet ikizi).
	coreEmit, narrErr := emit, ""
	if len(b.Blocks) > 0 {
		coreEmit = func(kind string, p any) {
			if kind == "error" {
				if m, ok := p.(map[string]string); ok {
					narrErr = m["error"]
				}
				return
			}
			emit(kind, p)
		}
	}
	res, ok := s.streamExplainCore(ctx, coreEmit, k.Surface, b.System, b.User, b.CacheKey, ask.Fresh) // v0.10.1406 — fresh: Yeniden üret
	if !ok && len(b.Blocks) == 0 {
		return true, false
	}
	if !ok {
		emit("answer", withAnswerFollowups(map[string]any{"text": explainNarrationFailedTR(narrErr), "exchangeId": res.Xid, "explain": meta}, b.Followups))
		return true, false
	}
	text := trimExplainText(res)
	if b.Appendix != "" { // v0.10.2014 — runbook eki: akışa son delta, cevaba aynı metin
		emit("delta", map[string]string{"text": explainAppendixSep + b.Appendix})
		text = joinExplainAppendix(text, b.Appendix)
	}
	links := append(append([]guidedAnswerLink(nil), b.Links...), s.answerRequestIDLinks(ctx, text, b.Service)...)
	meta.Cached, meta.CachedAtMs = res.Cached, res.CachedAtMs
	ans := map[string]any{
		"text": text, "exchangeId": res.Xid,
		"links":   dedupLinksByHref(links),
		"explain": meta,
	}
	emit("answer", withAnswerFollowups(withExplainCached(ans, res), b.Followups))
	return true, true
}

// explainNarrationFailedTR — SAF (v0.10.1407): kanıt-önce türde anlatım
// düşünce cevabın dürüst satırı (ServiceChartsExplainBody'nin eski boş-anlatım
// metni; kart bloğu ölçülmüş veriyi yine çizer).
func explainNarrationFailedTR(err string) string {
	if err = strings.TrimSpace(err); err != "" {
		return "Anlatım üretilemedi (" + err + "). Aşağıdaki kanıt yine de ölçülmüş veridir."
	}
	return "Model boş yanıt döndürdü. Aşağıdaki kanıt yine de ölçülmüş veridir."
}
