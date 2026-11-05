package api

// metric_name_drop.go — v0.10.1809: Pipeline metrik DROP kurallarının
// okuma tarafı (operatör sorusu: kuraldan önce VM'e yazılmış healthcheck
// metrikleri listelerde görünmeye devam ediyordu).
//
// Kurallar yazma anında zaten uygulanıyor (CH ingest + VM forward,
// otlp/forward_filter.go). Bu dosya YALNIZ keşif yüzeylerini süzer:
//
//   - GET /api/metrics/names (iki zarf da, iki kaynak da) — ad listesi;
//   - GET /api/metrics/labels + attr-keys — gizli ad için backend'e
//     inmeden boş liste.
//
// Açık sorgular (/api/metrics/query, PromQL, dashboard, compare)
// SÜZÜLMEZ: operatör eski veriyi adıyla incelemeye devam eder
// (DECISIONS 2026-11-05).
//
// Neden api.go'da değil: TestApiGoDoesNotGrow — handler'lar yalnız bu
// yardımcıları çağırır.
//
// Bilinen boşluklar:
//   - Sayfalı + aramalı zarf: her iki kaynak da sayfayı kaynakta keser
//     (CH SQL LIMIT/OFFSET, VM pageNames). Süzme sayfaya uygulanır, yani
//     gizli ad içeren bir sayfa KISA gelebilir; hasMore ham sayfadan
//     hesaplanır ki sonsuz kaydırma durmasın, total sayfada düşürülenler
//     kadar azaltılır (yaklaşık). Ad kardinalitesi küçük; operatör
//     etkisi bir sayfada birkaç eksik satır.
//   - VM adları Prometheus biçiminde (http_server_duration_seconds_bucket);
//     OTLP yazımlı `=` kuralı VM adını gizlemez. `=~` deseni ikisini de
//     yakalar (yazma tarafı forward_filter OTLP adıyla karar verir).

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// metricNameDrop — derlenmiş süzgeç + cache anahtarı eki. Kural yokken
// (nil, ""): anahtar bayt-bayt eski, gereksiz soğuk okuma yok. Ek, kural
// kümesinin TAMAMININ FNV özetini taşır (v0.5.187 — uzunluk değil).
func (s *Server) metricNameDrop() (func(string) bool, string) {
	if s == nil || s.pipeline == nil {
		return nil, ""
	}
	fn, digest := s.pipeline.MetricNameDropper()
	if fn == nil {
		return nil, ""
	}
	return fn, ":nd=" + digest
}

// metricNameHidden — etiket/anahtar keşfi kapısı. Gizli adın cevabı
// sabit boş liste olduğu için bu iki uç cache'e HİÇ inmez; gizli
// olmayan adın backend cevabı kurallardan bağımsız, yani anahtarlarına
// özet eklemek yalnız her kural PUT'unda DISTINCT taramalarını
// soğuturdu (v0.8.456'nın önlediği maliyet).
func (s *Server) metricNameHidden(metric string) bool {
	drop, _ := s.metricNameDrop()
	return drop != nil && drop(metric)
}

// serveHiddenMetricEmpty — gizli ad için `[]` yazar ve true döner (çağıran
// return eder); gizli değilse hiçbir şey yazmaz. Tek satırlık çağrı
// api.go'yu büyütmesin diye yazım burada.
func (s *Server) serveHiddenMetricEmpty(w http.ResponseWriter, metric string) bool {
	if !s.metricNameHidden(metric) {
		return false
	}
	writeJSON(w, []string{})
	return true
}

// filterDroppedMetricNames — SAF. drop nil = dilim aynen döner.
func filterDroppedMetricNames(names []chstore.MetricInfo, drop func(string) bool) []chstore.MetricInfo {
	if drop == nil {
		return names
	}
	out := make([]chstore.MetricInfo, 0, len(names))
	for _, n := range names {
		if !drop(n.Name) {
			out = append(out, n)
		}
	}
	return out
}

// metricNamesPage — SAF: sayfalı zarf. hasMore HAM sayfadan (süzme
// öncesi) hesaplanır: gizli satırlar yüzünden kısalan sayfa "son sayfa"
// sanılıp sonsuz kaydırmayı durdurmasın. total sayfadaki düşenler kadar
// azalır, dönen satır sayısının altına inmez.
func metricNamesPage(names []chstore.MetricInfo, total, offset int, source string, drop func(string) bool) map[string]any {
	hasMore := offset+len(names) < total
	kept := filterDroppedMetricNames(names, drop)
	total -= len(names) - len(kept)
	if total < offset+len(kept) {
		total = offset + len(kept)
	}
	if kept == nil {
		kept = []chstore.MetricInfo{}
	}
	return map[string]any{
		"names":   kept,
		"total":   total,
		"hasMore": hasMore,
		// source: hangi store cevapladı. Gövdeyle BİRLİKTE gidiyor,
		// yani rozet her zaman ekrandaki satırların kaynağını gösterir.
		"source": source,
	}
}
