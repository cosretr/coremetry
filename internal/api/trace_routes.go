package api

// trace_routes.go — v0.10.275 (trace view Dilim 1b): GET /api/traces/{id}
// api.go'dan buraya TAŞINDI (route_registry.go defteri, init kaydı; api.go
// kısaldı). Yanıta `analysis` (chstore.TraceAnalysis) eklendi —
// docs/audit/trace-view.md §4: ağaç iki kez istemcide kuruluyor ve iki
// farklı öz-süre tanımı yaşıyordu; tek kaynak burada. Alan EKLEME, sürüm
// DEĞİL: eski istemci alanı yok sayar; cache anahtarı v3.
//
// v0.10.671 — gövde traceDetailPayload'a ayrıldı: /api/traces/{id}/bundle
// (trace_bundle.go, kiosk modu) aynı span çözümlemesini + analysis'i
// kullanır. İki uç tek gövdeyi paylaşır; bu ucun tel şekli ve cache
// anahtarı AYNEN.

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("trace", (*Server).registerTraceRoutes) }

func (s *Server) registerTraceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/traces/{id}", s.getTrace)
}

func (s *Server) getTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "trace id required", http.StatusBadRequest)
		return
	}
	// 30s cache. A trace is immutable once stored, but a
	// just-flipped Tempo backend setting should rescue stale
	// "not found" entries within a short window — hence the
	// short TTL. `source` distinguishes CH-resident vs Tempo-
	// fallback so the frontend can banner-tag the result.
	// v0.10.275 — analysis alanı eklendi; eski gövde yeni FE'ye servis edilmesin.
	// v0.10.1525 — v4: DB span'leri Databases kimliğiyle damgalı (dbInstance /
	// dbName / dbStmtHash); v3 gövdesi linksiz span'ler servis ederdi.
	key := "trace:v4:" + id
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		out, _, err := s.traceDetailPayload(ctx, id, time.Time{})
		return out, err
	})
}

// traceDetailResolution — v0.10.2201: trace DETAY çözümünün sonucu. Sayfa
// (GET /api/traces/{id}, bundle) ve CoSRE açıklama turu (buildTraceExplainInput)
// AYNI çözümü kullanır.
//
// Operator-reported (prod, 7 gün): "Bu trace'i açıkla" turlarının 43'ü
// "bulunamadı" ile bitti — trace ekranda AÇIKKEN. Sayfa Distributed okuma 0
// span döndüğünde MV stub'ından pencereyi alıp TÜM replikalardan okuyor
// (v0.10.810 replika ıraksaması yedeği); açıklama yalnız resolveTraceSpans
// çağırıyordu → aynı trace sayfada var, açıklamada yok.
type traceDetailResolution struct {
	Spans  []chstore.SpanRow
	Source string
	// Stub — span okunamadı ama MV trace'i biliyor (StubReason dolu).
	Stub       *chstore.TraceAggregateStub
	StubReason string // "" | traceStubReplicaMiss | traceStubAgedOut
}

const (
	traceStubReplicaMiss = "replica_miss"
	traceStubAgedOut     = "aged_out"
)

// traceDetailDeps — çözümün dört okuması. Prod'da Server'ın kendisi;
// testte traceDetailDepsSeam ("sayfa bulur, açıklama bulamaz" yeniden üretimi).
type traceDetailDeps struct {
	Primary     func(ctx context.Context, id string, hint time.Time) ([]chstore.SpanRow, string, error)
	Stub        func(ctx context.Context, id string) (chstore.TraceAggregateStub, bool)
	AgedOut     func(ctx context.Context, startNs int64) bool
	AllReplicas func(ctx context.Context, id string, startNs, endNs int64) ([]chstore.SpanRow, error)
}

// traceDetailDepsSeam — TEST DİKİŞİ (prod'da nil).
var traceDetailDepsSeam func() traceDetailDeps

func (s *Server) traceDetailDeps() traceDetailDeps {
	if traceDetailDepsSeam != nil {
		return traceDetailDepsSeam()
	}
	return traceDetailDeps{
		Primary: func(ctx context.Context, id string, hint time.Time) ([]chstore.SpanRow, string, error) {
			return s.resolveTraceSpansNear(ctx, id, hint)
		},
		Stub:        s.store.GetTraceAggregateStub,
		AgedOut:     s.traceAgedOut,
		AllReplicas: s.store.GetTraceAllReplicas,
	}
}

// resolveTraceDetail — sayfanın ve açıklamanın ORTAK trace çözümü:
// Tempo → CH (resolveTraceSpansNear, ipucu ilk pencere) → 0 span ise MV
// stub → TTL içindeyse tüm replikalar → yoksa dürüst stub nedeni.
func (s *Server) resolveTraceDetail(ctx context.Context, id string, hint time.Time) (traceDetailResolution, error) {
	return resolveTraceDetailWith(ctx, s.traceDetailDeps(), id, hint)
}

func resolveTraceDetailWith(ctx context.Context, d traceDetailDeps, id string, hint time.Time) (traceDetailResolution, error) {
	// v0.9.632 — çözümleme TEK yerde (trace_resolve.go) ve sırası
	// operatör kararıyla ÖNCE TEMPO; Tempo yapılandırılmamışsa / bütçeyi
	// aşarsa / bulamazsa ClickHouse'a düşülür.
	spans, src, err := d.Primary(ctx, id, hint)
	if err != nil {
		return traceDetailResolution{}, err
	}
	if len(spans) > 0 {
		return traceDetailResolution{Spans: spans, Source: src}, nil
	}
	// v0.6.34 — trace_summary_5m'in TTL'i ham spans'tan uzun: MV trace'i
	// biliyorsa dürüst "aged out" künyesi. v0.10.810 — TTL içindeyse
	// replika ıraksaması: tüm replikalardan yedek okuma.
	stub, ok := d.Stub(ctx, id)
	if !ok {
		return traceDetailResolution{Source: traceSourceCH}, nil
	}
	if d.AgedOut(ctx, stub.StartTimeNs) {
		return traceDetailResolution{Stub: &stub, StubReason: traceStubAgedOut}, nil
	}
	rows, rerr := d.AllReplicas(ctx, id, stub.StartTimeNs, stub.EndTimeNs)
	if rerr == nil && len(rows) > 0 {
		log.Printf("[trace] %s: Distributed okuma 0 span, tüm replikalardan %d span — replika ıraksaması (Admin → Replika tutarlılığı)", id, len(rows))
		return traceDetailResolution{Spans: rows, Source: traceSourceAllReplicas}, nil
	}
	if rerr != nil {
		log.Printf("[trace] %s: tüm-replika yedek okuması başarısız: %v", id, rerr)
	}
	return traceDetailResolution{Stub: &stub, StubReason: traceStubReplicaMiss}, nil
}

// traceDetailPayload — GET /api/traces/{id} gövdesi (v0.10.671'de ayrıldı;
// trace_bundle.go da çağırır). Dönen span dilimi bundle'ın log penceresini
// kurmak için; map istemciye giden gövdenin kendisi. v0.10.1889 — `hint`
// (sıfır = yok): bundle'ın log zamanı ipucu, yalnız CH pencere aramasını
// hızlandırır. v0.10.2201 — çözüm resolveTraceDetail'de (açıklamayla ortak).
func (s *Server) traceDetailPayload(ctx context.Context, id string, hint time.Time) (map[string]any, []chstore.SpanRow, error) {
	res, err := s.resolveTraceDetail(ctx, id, hint)
	if err != nil {
		return nil, nil, err
	}
	if res.Source == traceSourceAllReplicas {
		rows := res.Spans
		chstore.StampDBSpanIdentity(rows)
		out := map[string]any{"traceId": id, "spans": rows, "source": traceSourceAllReplicas, "replicaMiss": true}
		out["analysis"] = chstore.BuildTraceAnalysis(rows, false)
		return out, rows, nil
	}
	if spans, src := res.Spans, res.Source; len(spans) > 0 {
		// v0.10.1525 — DB span → Databases / ifade detayı linkleri için kimlik
		// sunucuda, MV kurallarıyla (chstore/db_span_identity.go).
		chstore.StampDBSpanIdentity(spans)
		out := map[string]any{"traceId": id, "spans": spans, "source": src}
		// v0.9.457 (dürüstlük A2) — 50k tavanına çarpan trace "tam"
		// diye render ediliyordu; tavan dolunca söyle, gerçek span sayısı
		// MV stub'ından (best-effort). Tavan yalnız ClickHouse yolunda.
		capped := src == traceSourceCH && len(spans) >= 50000
		if capped {
			out["spanCapped"] = true
			if stub, ok := s.store.GetTraceAggregateStub(ctx, id); ok {
				out["spanTotal"] = stub.SpanCount
			}
		}
		// v0.10.275 (Dilim 1b) — ağaç + kritik yol + öz süre + servis özeti Go'da.
		out["analysis"] = chstore.BuildTraceAnalysis(spans, capped)
		return out, spans, nil
	}
	if res.Stub != nil {
		return map[string]any{"traceId": id, "spans": []any{}, "source": "mv_only", "stub": *res.Stub, "stubReason": res.StubReason}, nil, nil
	}
	return map[string]any{"traceId": id, "spans": []chstore.SpanRow{}, "source": "clickhouse"}, nil, nil
}
