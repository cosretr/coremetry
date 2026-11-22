package runtimesig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2443 — runtimeAlerts yalnız HTTP yanıtında dolu; nil iken alan hiç
// yazılmaz (CoSRE gövdesi bayt-aynı), doluyken UI'nin okuduğu adlarla.
func TestReportRuntimeAlertsJSON(t *testing.T) {
	b, err := json.Marshal(Report{Service: "svc"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "runtimeAlerts") {
		t.Fatalf("nil runtimeAlerts yazıldı: %s", b)
	}
	ra := chstore.DefaultRuntimeAlerts()
	ra.GCShareWarnPct = 7
	b, err = json.Marshal(Report{Service: "svc", RuntimeAlerts: &ra})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"runtimeAlerts":{`, `"gcShareWarnPct":7`, `"gcPauseWarnMs":2000`, `"heapRawWarnPct":92`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s yok: %s", want, b)
		}
	}
}
