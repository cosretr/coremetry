package chstore

// ml_recommend.go — v0.10.1886 (operatör 2026-11-06: "Settings'te anomali
// ayarları çok karmaşık, sen default kur"; VM'de HTTP, gRPC ve Kafka
// metrikleri var). SAF: ölçülmüş kaynak durumundan ÖNERİLEN ML kurulumu.
//
// Kural tek cümle: verisi VictoriaMetrics'te OLAN dedektör açılır, verisi
// OLMAYAN kapanır (zaten sessiz kalırdı), ölçülemeyen ve Elasticsearch'e sorgu
// atan dedektörlere DOKUNULMAZ. Terfi anahtarları, eşikler, MetricsQL
// ifadeleri ve kalite çerçevesi değişmez — öneri yalnız aç/kapa anahtarlarıdır.
// Hiçbir şey kendiliğinden uygulanmaz: uç öneriyi döner, admin "Uygula"ya
// basınca bildik PUT /api/admin/ml (audit'li) yazar.

// MLSourceCheck — bir veri kaynağının VictoriaMetrics'teki ölçülmüş durumu.
type MLSourceCheck struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Checked=false: ölçülemedi (VM bağlı değil / sorgu hatası) — "yok" DEĞİL.
	Checked bool   `json:"checked"`
	Present bool   `json:"present"`
	Note    string `json:"note,omitempty"`
}

// Kaynak kimlikleri (metrik aileleri kendi dedektör adını kullanır).
const (
	MLSourceHTTPServer      = "http_server"
	MLSourceGRPCServer      = "grpc_server"
	MLSourceHTTPClient      = "http_client"
	MLSourceDBClient        = "db_client"
	MLSourceMessagingClient = "messaging_client"
)

// mlDetectorSources — dedektör → onu besleyen kaynaklar (biri yeter).
// Log dedektörleri burada YOK: kaynakları Elasticsearch, öneri dokunmaz.
var mlDetectorSources = map[string][]string{
	"op_latency":         {MLSourceHTTPServer, MLSourceGRPCServer},
	"op_error_rate":      {MLSourceHTTPServer, MLSourceGRPCServer},
	"throughput_drop":    {MLSourceHTTPServer, MLSourceGRPCServer},
	"trace_pod_outlier":  {MLSourceHTTPServer, MLSourceGRPCServer},
	"trace_change_point": {MLSourceHTTPServer, MLSourceGRPCServer},
	"dependency_latency": {MLSourceHTTPClient, MLSourceDBClient, MLSourceMessagingClient},
	"metric_jvm":         {"metric_jvm"},
	"metric_pool":        {"metric_pool"},
	"metric_kafka":       {"metric_kafka"},
	"metric_http_client": {"metric_http_client"},
	"metric_k8s":         {"metric_k8s"},
}

// mlRecommendExempt — v0.10.2018: kaynağı VictoriaMetrics olmayan log-dışı
// dedektörler; öneri (VM kaynak ölçümü) bunlara dokunmaz, log dedektörleri gibi.
var mlRecommendExempt = map[string]string{
	"trace_error_count": "kaynak ClickHouse spanmetrics_1m (hata span'leri örneklenmez); VM ölçümü ilgisiz",
}

// MLRecommendChange — önerinin bugünkü ayardan bir farkı. Key "enabled" (ana
// anahtar) ya da dedektör adı.
type MLRecommendChange struct {
	Key    string `json:"key"`
	From   bool   `json:"from"`
	To     bool   `json:"to"`
	Reason string `json:"reason"`
}

// RecommendMLConfig — SAF: bugünkü ayar + ölçülmüş kaynaklar → önerilen ayar
// ve farklar (MLDetectorNames sırasıyla; ana anahtar başta). Girdi değişmez.
func RecommendMLConfig(cur MLConfig, checks []MLSourceCheck) (MLConfig, []MLRecommendChange) {
	cur = NormalizeMLConfig(cur)
	out := cur
	out.Detectors = make(map[string]bool, len(cur.Detectors))
	for k, v := range cur.Detectors {
		out.Detectors[k] = v
	}
	byID := make(map[string]MLSourceCheck, len(checks))
	for _, c := range checks {
		byID[c.ID] = c
	}
	var changes []MLRecommendChange
	for _, det := range MLDetectorNames {
		if _, exempt := mlRecommendExempt[det]; exempt {
			continue // VM dışı kaynak: dokunulmaz
		}
		srcs, ok := mlDetectorSources[det]
		if !ok {
			continue // log dedektörü: dokunulmaz
		}
		checked, present, label := 0, false, ""
		for _, id := range srcs {
			c, ok := byID[id]
			if !ok || !c.Checked {
				continue
			}
			checked++
			if c.Present {
				present = true
				if label == "" {
					label = c.Label
				}
			}
		}
		// Kapatma kararı yalnız TÜM kaynaklar ölçüldüyse: ölçülemeyen kaynak
		// "yok" sayılıp çalışan bir dedektör kapatılmasın.
		var to bool
		var reason string
		switch {
		case present:
			to, reason = true, label+" verisi VictoriaMetrics'te var"
		case checked == len(srcs):
			to, reason = false, "verisi VictoriaMetrics'te bulunamadı"
		default:
			continue
		}
		if cur.Detectors[det] != to {
			out.Detectors[det] = to
			changes = append(changes, MLRecommendChange{Key: det, From: cur.Detectors[det], To: to, Reason: reason})
		}
	}
	anyOn := false
	for _, v := range out.Detectors {
		anyOn = anyOn || v
	}
	if !cur.Enabled && anyOn {
		out.Enabled = true
		changes = append([]MLRecommendChange{{Key: "enabled", From: false, To: true, Reason: "açık dedektörlerin çalışması için ana anahtar"}}, changes...)
	}
	return out, changes
}
