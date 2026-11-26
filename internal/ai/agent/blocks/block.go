package blocks

import "fmt"

// block.go — v0.10.541 (CoSRE v2 Faz 3.3a): BLOK PROTOKOLÜ. Cevap düz metin
// yerine tipli bloklar taşır; SSE `event: block` çerçevesi:
//
//	{ id, type, seq, final, payload }
//
// Model YALNIZ metin üretir; yapısal bloklar (chart/link/trace_list/table/
// action/evidence) deterministik — tool sonucundan ya da sunucu link
// kurucusundan (v0.10.47 chart-origin kararının genellemesi). Eski
// `delta/answer` çerçeveleri bir sürüm boyunca PARALEL yayımlanır: eski
// istemci ve arşiv (chatPersist yalnız metin saklar) fence'i görmeye devam
// eder, yeni istemci tipli bloğu tercih eder.
const (
	TypeText      = "text"
	TypeTable     = "table"
	TypeChart     = "chart"
	TypeTraceList = "trace_list"
	TypeLink      = "link"
	TypeAction    = "action"
	TypeEvidence  = "evidence"
	// TypeOperation — operasyon adı ön-getirmesinin kartı (api/chat_operation_prefetch.go).
	TypeOperation = "operation"
	// TypeServiceReport — v0.10.1337: guided servis raporunun kartı
	// (api/copilot_service_report.go): hüküm, operasyonlar, teknik hatalar,
	// trace kırılımları + o trace'lerin logları, link çipleri.
	TypeServiceReport = "service_report"
	// TypeTopology — v0.10.1388: CoSRE servis topolojisi / örnek trace
	// topolojisi grafiği (api/copilot_service_topology.go; FE ChatTopologyCard).
	TypeTopology = "topology"
	// TypeChartsSignals — v0.10.1407 (Tek CoSRE adım 4): Service → Details
	// grafik açıklamasının ölçülmüş sinyalleri (deploy/problem/anomali/op-delta)
	// + pencere/kapsam (api/copilot_service_charts.go; FE ChartsSignalsCard).
	TypeChartsSignals = "charts_signals"
	// TypeLogSpike — v0.10.1407: log artışı açıklamasının deterministik kanıtı
	// (mcptools.LogSpikeResult + kapsam; api/copilot_explain_log_spike.go;
	// FE LogSpikeCard).
	TypeLogSpike = "log_spike"
	// TypeMetricPanel — v0.10.1435: panelden "Bu grafiği açıkla" turunun
	// kanıtı (grafik + brush bölgesi + ölçüm; api/explain_metric_panel.go;
	// FE MetricPanelCard).
	TypeMetricPanel = "metric_panel"
	// TypeTraceEvidence — v0.10.2480: trace açıklamasının (anlatım biçimi)
	// sunucu-hesaplı kanıtı (hata zinciri, kritik yol, log, Oracle; api/
	// trace_explain_evidence.go; FE TraceNarrativeBody "Kanıt" bölümü). Modelden
	// ÖNCE yayılır: kanıt akış beklemeden çizilir.
	TypeTraceEvidence = "trace_evidence"
	// v0.10.2360 — kanıt blokları (investigation-design S3, karar 18): araç
	// sonucundan sunucu kurar (mcptools/evidence_parts.go), yalnız
	// cosre_conversation.evidence = on iken yayınlanır; çizici S5.
	TypeException = "exception"
	TypeTrace     = "trace"
	TypeLogs      = "logs"
	TypeAnomaly   = "anomaly"
)

// Sequencer — bir turn içinde blok kimliği/sırası. SAF: emit'e verilecek
// gövdeyi döner; çağıran `emit("block", …)` ile yayımlar (withStepIDs'in
// adım sayacından bağımsız — çip kimlikleri `i`, bloklar `seq`).
type Sequencer struct{ n int }

func (s *Sequencer) Next(typ string, payload any) map[string]any {
	s.n++
	return map[string]any{
		"id": fmt.Sprintf("b%d", s.n), "type": typ, "seq": s.n, "final": true, "payload": payload,
	}
}

// Count — yayımlanan blok sayısı (testler + cevap künyesi).
func (s *Sequencer) Count() int { return s.n }
