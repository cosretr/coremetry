package templater

// masker_ids_test.go — v0.10.1496: Desenler imzasıyla ortak kimlik
// yüklemi (logstore.MaskIDTokens) Drain maskesine de uygulanır. Sentetik
// kimlikler.

import (
	"fmt"
	"testing"
)

func TestMask_MixedAlnumIDs(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"request_id":"QXC1015060201WKue000000000000202610240340413867","msg":"ok"}`,
			`{"request_id":"<*>","msg":"ok"}`},
		{"job 01HF3ZK8Q4N6V2W9X7Y5R3T1PB done", "job <*> done"},
		{"cache key 1a2b3c4d miss", "cache key <*> miss"},
		{"Parsed iso8601 timestamp via log4j2 on x86_64", "Parsed iso8601 timestamp via log4j2 on x86_64"},
		{"java.lang.NullPointerException at OrderServiceImpl.handle", "java.lang.NullPointerException at OrderServiceImpl.handle"},
	}
	for _, c := range cases {
		if got := Mask(c.in); got != c.want {
			t.Errorf("Mask(%q)\n  got:  %q\n  want: %q", c.in, got, c.want)
		}
	}
}

// Operatörün şekli Drain'de: her satırın kendi request_id'si ilk belirteçte
// → önek ağacı her satırı ayrı yaprağa yolluyordu (N şablon). Artık TEK küme.
func TestDrain_RequestIDBodiesOneTemplate(t *testing.T) {
	d := NewDrain()
	for i := 0; i < 50; i++ {
		line := fmt.Sprintf(`{"request_id":"QXC%013dWKue%024d","channel":"MOB","msg":"inquiry completed in %dms"}`,
			1015060201+i*7919, 202610240340413867+int64(i)*104729, 10+i)
		d.Add(line, "orders-api", int64(i))
	}
	snap := d.Snapshot()
	if len(snap) != 1 || snap[0].Count != 50 {
		for _, c := range snap {
			t.Logf("%d × %s", c.Count, c.TemplateString())
		}
		t.Fatalf("want 1 template × 50, got %d", len(snap))
	}
}
