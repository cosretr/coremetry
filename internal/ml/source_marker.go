package ml

// source_marker.go — v0.10.2383 (triage mockup dilim 2; dilim 1 denetimi):
// ML canlı yolunun açtığı Problem'de standart "span · örneklenmiş, güven ORTA"
// diyordu — oysa servis hata oranı ve diğer trace-RED dedektörleri VM'in
// örneklenmemiş sunucu histogramını okur. Standardın bunun için ZATEN bir
// mekanizması var: dedektör açıklamaya kaynak işaretini yazar, standart geri
// okur (chstore.ParseEvidenceSource; işaretlerin tek tanımı chstore'da). ML
// canlı yolu o işareti yazmıyordu; bu dosya yazar.
//
// İşaret LİDER olayın kaynağıdır: servis satırında Problem'in ölçüsünü veren
// olay, grup satırında grubun kural kimliğindeki ilk üye, tek sinyal kolunda o
// tek olay.
//
// Dedektör → kaynak (her biri dedektörün kendi dosya başlığındaki KAYNAK notu):
//
//	VM, örneklenmemiş   op_latency / op_error_rate / throughput_drop /
//	                    svc_error_rate (sunucu RED histogramı), svc_breadth
//	                    (op_latency olaylarından türer), trace_pod_outlier,
//	                    trace_change_point (aynı VM p95 serisi), dependency_*
//	                    (client histogramı), metric_* YALNIZ kanıt "vm" diyorsa
//	log kayıtları       log_volume / error_rate / service_silent /
//	                    template_spike / rare_template / rare_error_code /
//	                    pod_outlier / change_point (Elasticsearch sayımı)
//	işaret YOK          trace_error_count (CH hata span sayımı: hatalar tam,
//	                    ama mevcut "span" kaynağı örneklenmiş demek),
//	                    new_template (Drain defteri: örneklem), Thanos'tan
//	                    okunan metric_* (işaret VictoriaMetrics adını taşır),
//	                    svc_telemetry_volume, bilinmeyen dedektör
//
// İşaretsiz olayda açıklama aynen kalır → standart bugünkü sınıflamayı söyler.
// Uydurma kaynak adı yok: yeni tek işaret metni (chstore.EvidenceLogs) mevcut
// "logs" kaynağına okunur.

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// problemDescMax — Problem açıklamasının rune tavanı (işaret dahil).
const problemDescMax = 600

// eventSourceMarker — SAF: olayın gerçek ölçü kaynağının işareti ("" = yok).
func eventSourceMarker(e chstore.MLEvent) string {
	switch e.Detector {
	case detOpLatency, detOpErrorRate, detThroughputDrop, detSvcErrorRate, detSvcBreadth,
		detTracePodOutlier, detTraceChangePoint, detDependencyLatency, detDependencyErrorRate:
		return chstore.REDEvidenceVM
	case detLogVolume, detErrorRate, detServiceSilent, detTemplateSpike, detRareTemplate,
		detRareErrorCode, detPodOutlier, detChangePoint:
		return chstore.EvidenceLogs
	}
	if strings.HasPrefix(e.Detector, "metric_") && e.Evidence != "" {
		var m struct {
			Source string `json:"source"`
		}
		if json.Unmarshal([]byte(e.Evidence), &m) == nil && m.Source == "vm" {
			return chstore.REDEvidenceVM
		}
	}
	return ""
}

// withSourceMarker — SAF: Problem cümlesi + lider olayın kaynak işareti.
// Gövde tavanda kırpılır, işaret KIRPILMAZ (standart onu geri okur).
func withSourceMarker(desc string, lead chstore.MLEvent) string {
	m := eventSourceMarker(lead)
	if m == "" {
		return truncRunes(desc, problemDescMax)
	}
	tail := " " + m + "."
	return truncRunes(desc, problemDescMax-utf8.RuneCountInString(tail)) + tail
}

// groupLead — SAF: grubun lider olayı = kural kimliğindeki ilk üye
// (GroupRuleID). Üyeler arasında yoksa sıfır değer (işaret yazılmaz).
func groupLead(g decidedGroup) chstore.MLEvent {
	if len(g.G.Members) == 0 {
		return chstore.MLEvent{}
	}
	for _, m := range g.Members {
		if m.ID == g.G.Members[0] {
			return m
		}
	}
	return chstore.MLEvent{}
}
