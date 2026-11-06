package mcptools

// v0.10.1760 — ML dilim 5: explain_ml_event ham seriyi döndürmez, özet +
// referanslar + Replay page_url; get_log_anomalies satırı kısa ve gövde ≤2 KB.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func TestExplainMLPayload(t *testing.T) {
	s := chstore.MLSnapshot{EventID: "e1", Detector: "op_error_rate", Signal: "trace", Service: "pay", Part: "/pay|err_ratio",
		Series: `[{"t":0,"v":0.01},{"t":300,"v":0.4}]`, Dwell: `[300]`, Thresholds: `{"criticalZ":4,"effectiveZ":4.5}`,
		Evidence: `{"exceptions":[{"fingerprint":"f","type":"NPE","count":3}],"bubbleUp":"okunamadı"}`,
		Samples:  `[{"t":1,"traceId":"abc","kind":"span"}]`, ConfirmIDs: []string{"x"}}
	got := explainMLPayload(s, []chstore.MLSnapshot{{EventID: "e1"}, {EventID: "e2", Signal: "log", Detector: "error_rate", Part: "_error"}}, nil)
	raw, _ := json.Marshal(got)
	body := string(raw)
	for _, want := range []string{`"series_peak":0.4`, `"triggered_buckets":1`, `"effectiveZ":4.5`, `"sample_trace_ids":["abc"]`, `"other_signals":["log/error_rate _error"]`, `"page_url":"/anomalies/ml/e1"`, `"exceptions"`} {
		if !strings.Contains(body, want) {
			t.Errorf("%s yok: %s", want, body)
		}
	}
	if strings.Contains(body, `"series":`) || len(raw) > mlToolMaxBytes {
		t.Fatalf("ham seri / tavan: %d bayt", len(raw))
	}
}

// v0.10.1875 — explain_ml_event kartın özet + dört alanını taşır (tek üretici
// ml.BuildExplainCard); boş etki yazılmaz; gövde tavanı korunur.
func TestExplainMLPayloadCard(t *testing.T) {
	s := chstore.MLSnapshot{EventID: "e9", Detector: "error_rate", Signal: "log", Service: "orders-api", Part: "_error", Series: `[]`}
	card := &ml.ExplainCard{Summary: "orders-api: hata logları normalin yaklaşık 9 katı, 13 dakikadır (14:31'den beri).",
		What: "orders-api servisinde hata logları arttı.", HowMuch: "Son 13 dakikada 640 hata satırı.", Since: "Bugün 14:31'den beri, 13 dakikadır sürüyor."}
	got := explainMLPayload(s, nil, card)
	if got["summary"] != card.Summary {
		t.Errorf("summary %v", got["summary"])
	}
	c, _ := got["card"].(map[string]any)
	if c["what"] != card.What || c["how_much"] != card.HowMuch || c["since"] != card.Since {
		t.Errorf("card %v", c)
	}
	if _, ok := c["impact"]; ok {
		t.Error("boş etki yazılmaz")
	}
	if raw, _ := json.Marshal(got); len(raw) > mlToolMaxBytes {
		t.Errorf("tavan: %d", len(raw))
	}
}

func TestMLEventRowAndFit(t *testing.T) {
	r := mlEventRow(chstore.MLEvent{ID: "a", Signal: "log", Detector: "rare_error_code", Service: "pay", Level: "error", Code: "ORA-1", Evidence: `{"confirmScore":2}`}, "")
	if r["part"] != "ORA-1" || r["confirmed_by"] != 2 {
		t.Fatalf("row=%v", r)
	}
	if _, ok := r["summary"]; ok {
		t.Error("boş özet alanı yazılmaz")
	}
	if r := mlEventRow(chstore.MLEvent{ID: "b"}, "orders-api: hata logları normalin yaklaşık 9 katı, 13 dakikadır (14:31'den beri)."); r["summary"] == nil {
		t.Error("v0.10.1875: satır sunucu özetini taşır")
	}
	rows := make([]map[string]any, 50)
	for i := range rows {
		rows[i] = map[string]any{"id": strings.Repeat("x", 80)}
	}
	b := fitRows(map[string]any{}, "events", rows, mlToolMaxBytes)
	raw, _ := json.Marshal(b)
	if len(raw) > mlToolMaxBytes || b["returned"].(int) == 0 {
		t.Fatalf("fit: %d bayt returned=%v", len(raw), b["returned"])
	}
}
