//go:build chsmoke

package anomaly

// v0.10.1586 — Davis paritesi FAZ 0.5 / C: trace_op sürdürme sorgusu GERÇEK
// ClickHouse'ta (operation_summary_5m MV'si, multiIf kova kolonu, prev_*
// tarama tipleri). İki ardışık kovada hata veren çift olay; yalnız son
// kovada hata veren çift olay DEĞİL.
//
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/anomaly/ -run TestTraceOpDwellQueryOnCH -v

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

func TestTraceOpDwellQueryOnCH(t *testing.T) {
	addr := os.Getenv("CH_SMOKE_ADDR")
	if addr == "" {
		t.Skip("CH_SMOKE_ADDR verilmedi — gerçek CH testi atlanıyor")
	}
	st, err := chstore.New(config.CHConfig{Addr: addr, Database: "default", DialTimeout: "10s"}, config.RetentionConfig{})
	if err != nil {
		t.Fatalf("şema kurulamadı: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	aligned := now.Truncate(5 * time.Minute)
	svc := fmt.Sprintf("orders-api-smoke-%d", now.UnixNano())
	var spans []*chstore.Span
	add := func(op string, at time.Time, calls, errs int) {
		for i := 0; i < calls; i++ {
			status := "ok"
			if i < errs {
				status = "error"
			}
			spans = append(spans, &chstore.Span{
				TraceID: fmt.Sprintf("%032x", len(spans)+1), SpanID: fmt.Sprintf("%016x", len(spans)+1),
				Name: op, Kind: "server", ServiceName: svc, StatusCode: status,
				Time: at.Add(time.Duration(i) * time.Millisecond), Duration: int64(time.Millisecond),
			})
		}
	}
	add("GET /orders", aligned.Add(-60*time.Minute), 100, 1) // taban
	add("GET /orders", aligned.Add(-9*time.Minute), 100, 30) // önceki kova
	add("GET /orders", aligned.Add(-4*time.Minute), 100, 40) // en yeni kova
	add("POST /pay", aligned.Add(-60*time.Minute), 100, 1)
	add("POST /pay", aligned.Add(-9*time.Minute), 100, 0)
	add("POST /pay", aligned.Add(-4*time.Minute), 100, 40)
	if err := st.InsertSpans(ctx, spans); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var out []TraceOpAnomaly
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, err = detectTraceOps(ctx, st.TelemetryReadConn(), chstore.DefaultAnomalySensitivity(), now, 5*time.Minute, 2, nil, t.Logf)
		if err != nil {
			t.Fatalf("detectTraceOps (dwell 2): %v", err)
		}
		mine := 0
		for _, a := range out {
			if a.Service == svc {
				mine++
			}
		}
		if mine > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond) // async_insert görünürlüğü
	}
	got := map[string]string{}
	for _, a := range out {
		if a.Service == svc {
			got[a.Operation] = a.Kind
		}
	}
	if got["GET /orders"] != "error_spike" || got["POST /pay"] != "" {
		t.Fatalf("dwell 2: %v — sürmüş çift olay, tek kovalık çift değil", got)
	}
	// Tek pencere (canlı şerit yolu) ikisini de görür.
	one, err := detectTraceOps(ctx, st.TelemetryReadConn(), chstore.DefaultAnomalySensitivity(), now, 5*time.Minute, 1, nil, t.Logf)
	if err != nil {
		t.Fatalf("detectTraceOps (dwell 1): %v", err)
	}
	seen := map[string]bool{}
	for _, a := range one {
		if a.Service == svc {
			seen[a.Operation] = true
		}
	}
	if !seen["GET /orders"] || !seen["POST /pay"] {
		t.Fatalf("dwell 1 ikisini de görmeli: %v", seen)
	}
	// Aktif muafiyet: tek kovalık çift aktifse tazelenir.
	act, err := detectTraceOps(ctx, st.TelemetryReadConn(), chstore.DefaultAnomalySensitivity(), now, 5*time.Minute, 2,
		[]traceOpPair{{Service: svc, Operation: "POST /pay"}}, t.Logf)
	if err != nil {
		t.Fatalf("detectTraceOps (aktif): %v", err)
	}
	seen = map[string]bool{}
	for _, a := range act {
		if a.Service == svc {
			seen[a.Operation] = true
		}
	}
	if !seen["POST /pay"] || !seen["GET /orders"] {
		t.Fatalf("aktif çift sürdürmeden muaf olmalı: %v", seen)
	}
}
