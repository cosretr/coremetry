package api

// health.go — GET /api/health (v0.10.754'te api.go'dan taşındı: api.go
// büyümez kuralı; tavan .claude/baselines/api_go_lines). Gövde aynen;
// tek ek: `otlp_ingest_rejects` (ingest_counters.go).

import (
	"encoding/json"
	"net/http"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/otlp"
)

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	// v0.5.280 — accepted counters added so the Topbar live
	// activity ticker can derive per-second rates client-side
	// (delta of cumulative ÷ elapsed).
	// v0.5.342 — overload signalling. If any ingest queue is
	// past the overload threshold (≥90% full) or the
	// drop-counter is climbing, return 503 so an upstream LB
	// (envoy "outlier_detection", k8s readinessProbe) can take
	// this pod out of rotation until it drains. The body still
	// renders for operators inspecting curl output.
	// v0.8.339 (HA audit H3) — readiness honesty: ALL FIVE ingest queues
	// (exemplars + span_links were invisible here) and a cached CH ping.
	// The audited failure: CH down with fast connection-refused → flushers
	// drop instantly → queues stay EMPTY → health reported "ok" while 100%
	// of telemetry was discarded. The ping (3s timeout, 5s cache) makes
	// readiness track the actual write path, so the LB stops routing and
	// the collector's own queue holds data instead of our discard path.
	spansLen, spansCap := s.ing.Spans.QueueLen(), s.ing.Spans.Capacity()
	logsLen, logsCap := s.ing.Logs.QueueLen(), s.ing.Logs.Capacity()
	metricsLen, metricsCap := s.ing.Metrics.QueueLen(), s.ing.Metrics.Capacity()
	exLen, exCap := s.ing.Exemplars.QueueLen(), s.ing.Exemplars.Capacity()
	slLen, slCap := s.ing.SpanLinks.QueueLen(), s.ing.SpanLinks.Capacity()
	overloaded := isOverloaded(spansLen, spansCap) ||
		isOverloaded(logsLen, logsCap) ||
		isOverloaded(metricsLen, metricsCap) ||
		isOverloaded(exLen, exCap) ||
		isOverloaded(slLen, slCap)
	degraded := isDegraded(spansLen, spansCap) ||
		isDegraded(logsLen, logsCap) ||
		isDegraded(metricsLen, metricsCap) ||
		isDegraded(exLen, exCap) ||
		isDegraded(slLen, slCap)
	chOK := s.chReachable(r.Context())
	// v0.9.985 — dağıtık kipte bir INSERT'in "OK" dönmesi verinin İNDİĞİ
	// anlamına gelmez: Distributed motoru diske spool'layıp hemen OK der.
	// 2026-08-12'de lokal küme 3s39d boyunca hiç span yazamazken bu
	// endpoint yemyeşildi (aşağıdaki sayaçların HEPSİ doğruydu — hepsi
	// yanlış katmanı ölçüyordu). Tek-düğümde dq nil'dir ve hiçbir sorgu
	// çalışmaz; gövde de o kurulumda bayt-bayt eskisi gibi kalır.
	dq, spoolDegraded, spoolDetail := s.distributionBacklog()
	load, code := healthVerdict(overloaded, degraded, spoolDegraded, chOK)
	body := map[string]interface{}{
		"status":                  load,
		"spans_queued":            spansLen,
		"logs_queued":             logsLen,
		"metrics_queued":          metricsLen,
		"spans_capacity":          spansCap,
		"logs_capacity":           logsCap,
		"metrics_capacity":        metricsCap,
		"spans_dropped":           s.ing.Spans.Dropped(),
		"logs_dropped":            s.ing.Logs.Dropped(),
		"metrics_dropped":         s.ing.Metrics.Dropped(),
		"sse_dropped":             s.sseDropped(), // v0.10.200 — dolu abone kanalına düşen olaylar
		"spans_write_failed":      s.ing.Spans.WriteFailed(),
		"logs_write_failed":       s.ing.Logs.WriteFailed(),
		"metrics_write_failed":    s.ing.Metrics.WriteFailed(),
		"spans_accepted":          s.ing.Spans.Accepted(),
		"logs_accepted":           s.ing.Logs.Accepted(),
		"metrics_accepted":        s.ing.Metrics.Accepted(),
		"exemplars_queued":        exLen,
		"exemplars_capacity":      exCap,
		"exemplars_dropped":       s.ing.Exemplars.Dropped(),
		"exemplars_write_failed":  s.ing.Exemplars.WriteFailed(),
		"span_links_queued":       slLen,
		"span_links_capacity":     slCap,
		"span_links_dropped":      s.ing.SpanLinks.Dropped(),
		"span_links_write_failed": s.ing.SpanLinks.WriteFailed(),
		// v0.10.293 — VM çift yazım ileti kuyruğu (self-observability kuralı:
		// yeni ingest yolu = sayaç).
		"vm_metrics_forward_enqueued": s.ing.MetricForwardEnqueued(),
		"vm_metrics_forward_dropped":  s.ing.MetricForwardDropped(),
		"vm_metrics_forward_filtered": s.ing.MetricForwardFiltered(), // v0.10.373 — drop kuralları
		"otlp_convert_degrades":       otlp.ConvertDegradeCounts(),   // v0.10.388 — sessiz degrade sayaçları
		"otlp_ingest_rejects":         otlp.IngestRejectCounts(),     // v0.10.754 — sayaçsız kayıp noktaları (decode/oversize/gRPC oversize/boş id/geçersiz damga)
		// v0.10.300 — attribute hash indeksi: hazır mı, kaç yüklem bloom yolunda.
		"attr_index_available":  chstore.AttrIndexAvailable(),
		"attr_index_used":       chstore.AttrIndexUsed(),
		"attr_slice_used":       chstore.AttrSliceUsed(),    // v0.10.301 — indeks-güdümlü aday dilimi
		"traces_empty_mismatch": tracesEmptyMismatch.Load(), // v0.10.813 — şerit sayıyor, liste boş (aday tavanı sınıfı)
		"clickhouse":            chStatusLabel(chOK, spoolDegraded),
		// v0.9.238 — which roles THIS pod actually runs. In distributed mode
		// the api and ingest Deployments answer the same hostname through
		// different Services, and until now nothing in the response said
		// which one you reached: a 501 from /v1/traces was the only clue,
		// and only if you happened to POST OTLP at it. Two consequences this
		// surfaces directly — the browser RUM exporter pointing at an
		// api-role pod, and "why is this endpoint slow" questions where the
		// first thing to establish is which pod answered.
		"roles": map[string]bool{
			"ingest": !s.roleIngestOff,
			"api":    !s.roleAPIOff,
		},
	}
	// Spool alanları YALNIZ dağıtık kipte eklenir. Tek-düğümde dq nil'dir
	// (sorgu hiç koşmadı) ve gövde v0.9.984 ile bayt-bayt aynı kalır —
	// orada spool diye bir kavram yok, sıfır basmak yalan olurdu.
	if dq != nil {
		body["distributed_spool_files"] = dq.Files
		body["distributed_spool_bytes"] = dq.Bytes
		body["distributed_spool_errors"] = dq.ErrorCount
		body["distributed_spool_broken_files"] = dq.BrokenFiles
		// measured=false → "ölçemedim", "temiz" DEĞİL (v0.9.984 dersi).
		body["distributed_spool_measured"] = dq.Measured
		if dq.Partial {
			// Küme geneli okuma düştü, sayılar yalnız bu düğümün —
			// yaklaşıklık itiraf edilir, "hepsi bu" diye okunmasın.
			body["distributed_spool_partial"] = true
		}
		if spoolDetail != "" {
			body["distributed_spool_detail"] = spoolDetail
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	_ = json.NewEncoder(w).Encode(body)
}
