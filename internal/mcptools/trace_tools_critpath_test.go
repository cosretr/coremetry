package mcptools

// trace_tools_critpath_test.go — v0.10.1627 — CoSRE kritik yol planı R2:
// get_trace critical_path adımları kind / duration_ms / self_ms / share_pct /
// target / target_source / http_status / top alanlarını EK olarak taşır;
// analysis.critical_path_top_external_wait en büyük paylı adımın izlenmeyen
// bir CLIENT çağrısı olduğunu söyler. Eski alanlar (span_id, service, name,
// self_on_path_ms) aynen ve ilk sırada. Sentetik fikstür (12.21 s dış gateway).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTraceToolCriticalPathFields(t *testing.T) {
	r := ttSpan("r1", "", "orders-api", "POST /orders/score", 0, 12460, "")
	r.Kind = "server"
	r.Attributes = map[string]string{"http.response.status_code": "200"}
	i := ttSpan("i1", "r1", "orders-api", "com.acme.orders.ScoreService.evaluate", 100, 12400, "")
	i.Kind = "internal"
	c := ttSpan("c1", "i1", "orders-api", "POST gateway.ext.example", 150, 12360, "")
	c.Kind = "client"
	c.Attributes = map[string]string{"url.full": "https://gateway.ext.example/v1/score", "server.address": "gateway.ext.example", "server.port": "443", "http.response.status_code": "200"}
	an := buildTraceToolAnalysis([]chstore.SpanRow{r, i, c}, false)
	if !an.CriticalPathTopExternalWait {
		t.Error("critical_path_top_external_wait false")
	}
	b, err := json.Marshal(an.CriticalPath)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"span_id":"r1","service":"orders-api","name":"POST /orders/score","self_on_path_ms":160,"kind":"server","duration_ms":12460,"self_ms":160,"share_pct":1.3,"http_status":200},` +
		`{"span_id":"i1","service":"orders-api","name":"com.acme.orders.ScoreService.evaluate","self_on_path_ms":90,"kind":"internal","duration_ms":12300,"self_ms":90,"share_pct":0.7},` +
		`{"span_id":"c1","service":"orders-api","name":"POST gateway.ext.example","self_on_path_ms":12210,"kind":"client","duration_ms":12210,"self_ms":12210,"share_pct":98,"target":"https://gateway.ext.example/v1/score","target_source":"url.full","http_status":200,"top":true}]`
	if string(b) != want {
		t.Errorf("critical_path:\n%s\n--- beklenen ---\n%s", b, want)
	}
	full, _ := json.Marshal(an)
	if !strings.Contains(string(full), `"critical_path_top_external_wait":true`) {
		t.Error("analysis alanı JSON'da yok")
	}
	// İzlenen aşağı akış SERVER'ı → dış bağımlılık değil.
	s2 := ttSpan("s2", "c1", "scoring-svc", "POST /v1/score", 200, 12300, "")
	s2.Kind = "server"
	if buildTraceToolAnalysis([]chstore.SpanRow{r, i, c, s2}, false).CriticalPathTopExternalWait {
		t.Error("aşağı akış SERVER'ı olan CLIENT dış bağımlılık sayıldı")
	}
}
