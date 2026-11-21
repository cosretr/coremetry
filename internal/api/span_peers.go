package api

// span_peers.go — 2026-11-21 — GET /api/spans/peers: span detay panelinin
// "akranlar" dağılımı (aynı servis + operasyon, trace penceresi ±15 dk).
//
// v0.10.2411 bunu /api/spans/heatmap'ten okuyordu: her span tıklamasında
// (servis, ±15 dk) ham spans GROUP BY'ı. Panelin ihtiyacı yalnız 1-B süre
// dağılımı → spanmetrics_1m'in TDigest state'i (chstore.SpanPeerDistribution);
// MV satırı yoksa ham heatmap yedek yol. Neden /heatmap'e dal DEĞİL: o uç
// zaman × süre ızgarası sözleşmesi taşıyor ve süzgeç DSL'i serbest — MV
// yalnız (servis, operasyon) eşitliğini ifade edebilir; ayrı uç bu dar
// sözleşmeyi isimle söyler. Yanıt LatencyHeatmap şekli + `source`.
// serveCached 60 sn; anahtar tüm girdileri taşır. Rol kapısı yok (salt okuma).

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// spanPeersMaxWindow — panel ±15 dk + trace süresi ister; 6 sa üstü reddedilir
// (ham yedek yolun heatmap bütçesi, CLAUDE.md ≤6h).
const spanPeersMaxWindow = 6 * time.Hour

func init() {
	registerRoutesExtra("spans-peers", (*Server).registerSpanPeersRoutes)
}

func (s *Server) registerSpanPeersRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/spans/peers", s.spanPeers)
}

func (s *Server) spanPeers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	service, operation := q.Get("service"), q.Get("operation")
	from, to := parseTime(q.Get("from")), parseTime(q.Get("to"))
	if service == "" || operation == "" || from.IsZero() || !to.After(from) {
		http.Error(w, "service, operation, from < to are required", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > spanPeersMaxWindow {
		http.Error(w, fmt.Sprintf("window longer than %s", spanPeersMaxWindow), http.StatusBadRequest)
		return
	}
	key := fmt.Sprintf("span-peers:v1:w=%s:svc=%q:op=%q", cacheBucket(from, to), service, operation)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		return s.store.SpanPeerDistribution(ctx, service, operation, from, to)
	})
}
