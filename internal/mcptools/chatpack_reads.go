package mcptools

// chatpack_reads.go — v0.10.2361 (investigation-design S4): sohbet kanıt
// paketinin (internal/sre/chatpack.go) dalga 2 okumaları. Aracın aynı
// gövdesini ve aynı memo anahtarını kullanır: model aynı okumayı yeniden
// çağırırsa sonuç memodan gelir.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// ExplainTraceRead — explain_trace aracının gövdesi (kimlikle tek trace).
func ExplainTraceRead(ctx context.Context, d Deps, traceID string) (string, error) {
	args, _ := json.Marshal(explainTraceArgs{TraceID: traceID})
	out, err := explainTraceTool(d).Handler(ctx, args)
	if err != nil {
		return "", err
	}
	return bodyString(out)
}

// SearchTraceLogsOnce — search_logs trace_id gövdesi, Elasticsearch'e TEK
// sorgu: histogram ve alan eşlemesi sorulmaz (karar 16). Gövde aracın memo
// anahtarına yazılır.
func SearchTraceLogsOnce(ctx context.Context, d Deps, traceID string, rangeS int) (string, error) {
	p, err := planSearchLogs(cosreSearchLogsArgs{TraceID: traceID, RangeS: rangeS})
	if err != nil {
		return "", err
	}
	st := d.LogStore
	if st == nil {
		return "", errors.New("log backend not configured")
	}
	backend := logsBackendSource(st)
	from, to := rangeWindow(ctx, p.WindowS)
	out, err := v2Memo(ctx, d, searchLogsCacheKey(backend, p, to), slCacheTTL, func(c context.Context) (map[string]any, error) {
		page, err := logstore.SearchWithTimeout(c, st, p.filter(from, to), searchLogsTimeout)
		if err != nil {
			return nil, err
		}
		if page == nil {
			page = &logstore.Page{}
		}
		return searchLogsPayload(backend, p, page, nil, nil, from, to), nil
	})
	if err != nil {
		return "", err
	}
	return bodyString(out)
}

func bodyString(v any) (string, error) {
	if raw, ok := v.(json.RawMessage); ok {
		return string(raw), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}
	return string(b), nil
}
