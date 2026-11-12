package api

// v0.10.2026 — Operator-reported: log kalıbı anomalisinde "Logs" servisin
// tüm loglarını açıyordu, o kalıbı değil. log_template_new olayı okuma
// zincirinde arama metnini taşır (Şablonlar sekmesiyle aynı türetici);
// türetilemezse boş kalır ve bağlantı yalnız servisle açılır. Fikstür nötr.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestWithLogQueries(t *testing.T) {
	cases := []struct {
		name, kind, pattern, want string
	}{
		{"drain şablonu: literal parçalar tırnaklı AND", "log_template_new",
			`Batch job <*> failed: connection refused to <*>`, `"Batch job" AND "failed: connection refused to"`},
		{"yer tutucusuz şablon tek ifade", "log_template_new", `Starting com.acme.orders.Main`, `"Starting com.acme.orders.Main"`},
		{"literal parça yok → boş (yalnız servis)", "log_template_new", `<*> <*> ok`, ``},
		{"log_pattern dokunulmaz (FE pattern= kullanır)", "log_pattern", `connection_refused`, ``},
		{"trace türü dokunulmaz", "trace_op", `POST /orders`, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withLogQueries([]chstore.AnomalyEvent{{Kind: c.kind, Pattern: c.pattern, Service: "orders-batch"}})
			if got[0].LogQuery != c.want {
				t.Fatalf("LogQuery = %q, istenen %q", got[0].LogQuery, c.want)
			}
		})
	}
}
