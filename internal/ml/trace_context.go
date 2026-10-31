package ml

// trace_context.go — ML trace T1b (v0.10.1754): trace olayının kanıtına
// bağlam REFERANSI. Sahiplik (operatör 2026-10-31): exception sinyalinin
// sahibi exception akışı; burada veri KOPYALANMAZ, yalnız kimlik bağlanır:
//   - op_error_rate: aynı servis + endpoint ±5 dk en sık 3 exception tipi
//     (fingerprint = exception grubu kimliği) + örnek trace'lerin Oracle
//     hata kaydı referansı (kaynak, kod, operasyon; gövde yok);
//   - op_error_rate / op_latency: BubbleUp özeti (mevcut ServiceBubbleUp +
//     rca.TopBubbleUp, rootcause_bubbleup ile aynı kesim ve tavan).
// Koşu başına BİR KEZ hesaplanır (tracker'da tutulur, sonraki tiklerin
// kanıtına eklenir); tik başına en çok 2 BubbleUp (sentezleyiciyle aynı
// bütçe). Not: BubbleUp örneklenmiş span'lerin dağılımına bakar; oran
// hesaplamaz (tail sampling kuralı ihlal edilmez).

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rca"
)

const (
	traceCtxWindow        = 5 * time.Minute
	traceCtxBubblePerTick = 2
)

// needsTraceContext — SAF.
func needsTraceContext(det string) (exceptions, bubble bool) {
	switch det {
	case detOpErrorRate:
		return true, true
	case detOpLatency:
		return false, true
	}
	return false, false
}

// oracleRef — Oracle hata kaydı referansı (gövde kopyalanmaz).
type oracleRef struct {
	TraceID   string `json:"traceId"`
	SourceID  string `json:"sourceId"`
	Code      string `json:"code"`
	Operation string `json:"operation,omitempty"`
	T         int64  `json:"t"` // unix ns
}

// bubbleSummary — SAF: BubbleUp sonucu → kanıt özeti.
func bubbleSummary(bu *chstore.BubbleUpResult) []map[string]any {
	out := []map[string]any{}
	for _, t := range rca.TopBubbleUp(bu, rca.BubbleUpRenderCap, rca.BubbleUpRenderMinScore) {
		out = append(out, map[string]any{"key": t.Key, "value": t.Value, "selPct": t.SelPct, "basePct": t.BasePct})
	}
	return out
}

// traceContext — koşu başı bağlamı. bubbleLeft: tik BubbleUp bütçesi.
func traceContext(ctx context.Context, deps Deps, ev chstore.MLEvent, samples []chstore.MLSample, bubbleLeft *int) map[string]any {
	wantEx, wantBubble := needsTraceContext(ev.Detector)
	out := map[string]any{}
	det := time.Unix(0, ev.StartedAt)
	from, to := det.Add(-traceCtxWindow), det.Add(traceCtxWindow)
	if wantEx {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		exs, err := deps.Store.EndpointTopExceptions(cctx, chstore.EndpointDetailQuery{Service: ev.Service, Path: ev.Code, From: from, To: to}, 3)
		cancel()
		refs := []map[string]any{}
		if err == nil {
			for _, x := range exs {
				refs = append(refs, map[string]any{"fingerprint": x.Fingerprint, "type": x.Type, "count": x.Count})
			}
		}
		out["exceptions"] = refs
		var ora []oracleRef
		for _, sm := range samples {
			if sm.TraceID == "" || len(ora) >= 3 {
				continue
			}
			octx, cancel := context.WithTimeout(ctx, 2*time.Second)
			rows, err := deps.Store.OracleErrorsByTrace(octx, sm.TraceID, from, to, 1)
			cancel()
			if err == nil && len(rows) > 0 {
				r := rows[0]
				ora = append(ora, oracleRef{TraceID: r.TraceID, SourceID: r.SourceID, Code: r.ErrorCode, Operation: r.OperationCode, T: r.Time.UnixNano()})
			}
		}
		if ora == nil {
			ora = []oracleRef{}
		}
		out["oracle"] = ora
	}
	if wantBubble && *bubbleLeft > 0 {
		*bubbleLeft--
		bctx, cancel := context.WithTimeout(ctx, rca.BubbleUpTimeout)
		bu, err := deps.Store.ServiceBubbleUp(bctx, ev.Service, ev.Detector == detOpErrorRate, det.Add(-traceCtxWindow), det.Add(rca.ExtrasWindow))
		cancel()
		if err == nil && bu != nil {
			out["bubbleUp"] = bubbleSummary(bu)
		} else {
			out["bubbleUp"] = "okunamadı"
		}
	}
	return out
}
