package evaluator

// v0.10.1562 — db-health ve svc-slowdown sorguları kendi log_comment
// etiketini taşır; prod system.query_log maliyet ölçümü bu iki dedektörü
// worker:evaluator'ın geri kalanından ancak böyle ayırır. Etiket çağrı
// yerinde verildiği için kaynak metni pinlenir.

import (
	"os"
	"strings"
	"testing"
)

func TestDetectorQueryTagsPinned(t *testing.T) {
	src, err := os.ReadFile("evaluator.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`e.evaluateDBHealth(chstore.WithQueryTag(ctx, "worker:evaluator:db-health"))`,
		`e.evaluateServiceSlowdown(chstore.WithQueryTag(ctx, "worker:evaluator:svc-slowdown"))`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("etiketli çağrı yok: %s", want)
		}
	}
}
