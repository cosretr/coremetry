package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// chat_turn_rows_ops_test.go — v0.10.2473: top-N operasyon ve pod listeleri
// yapısal satır bırakır; "ikincisini aç" operasyonu açar / pod'a kapsar.
// Kimlik varlık modelinden (servis varlığı + ad ya da entity.PodID).

func TestOperationAndPodRows(t *testing.T) {
	ops := operationRows("checkout", []string{"GET /api/cart", "POST /api/pay", ""})
	if len(ops) != 2 || ops[1].Kind != rowKindOperation || ops[1].ID != "svc:checkout|POST /api/pay" {
		t.Fatalf("operasyon satırları: %+v", ops)
	}
	if operationRows("", []string{"x"}) != nil {
		t.Fatal("servissiz operasyon satırı olmamalı")
	}
	many := make([]string, 30)
	for i := range many {
		many[i] = "op-" + strings.Repeat("x", i+1)
	}
	if n := len(operationRows("checkout", many)); n != chatRowsMax {
		t.Fatalf("tavan: %d", n)
	}
	pods := podRows([]podRowIn{
		{Service: "payments", Pod: "payments-7d9-abc", ClusterID: "c1", Namespace: "shop"},
		{Service: "payments", Pod: "payments-7d9-def"},
		{Pod: "orphan"}, // kimliksiz → atlanır
	})
	if len(pods) != 2 || pods[0].ID != "pod:c1/shop/payments-7d9-abc" || pods[1].ID != "svc:payments|payments-7d9-def" {
		t.Fatalf("pod satırları: %+v", pods)
	}

	// operasyon listesinden sonra "ikincisini aç"
	prev := ChatContext{Service: "checkout", Rows: ops}
	req := chatRequest{Messages: []copilot.ChatMessage{{Role: "user", Text: "checkout en hatalı operasyonları"}, {Role: "assistant", Text: "liste"}, {Role: "user", Text: "ikincisini aç"}}}
	res := stateResolveOut{Next: prev}
	if l := (&chatStateTurn{}).applyRowPick(&res, prev, &req, true); l == "" || req.Messages[2].Text != "checkout POST /api/pay operasyonu nasıl?" ||
		req.Context.Service != "checkout" || req.Context.Operation != "POST /api/pay" || res.Next.Operation != "POST /api/pay" {
		t.Fatalf("operasyon seçimi: label=%q text=%q ctx=%+v next=%+v", l, req.Messages[2].Text, req.Context, res.Next)
	}

	// pod listesinden sonra sıra sözü → pod kapsamı
	for i, want := range []struct{ ord, text, ns string }{
		{"ilkini aç", "payments payments-7d9-abc pod'u nasıl?", "shop"},
		{"ikincisini aç", "payments payments-7d9-def pod'u nasıl?", ""},
	} {
		prevP := ChatContext{Service: "payments", Rows: pods}
		reqP := chatRequest{Messages: []copilot.ChatMessage{{Role: "user", Text: "payments podları"}, {Role: "assistant", Text: "liste"}, {Role: "user", Text: want.ord}}}
		resP := stateResolveOut{Next: prevP}
		if l := (&chatStateTurn{}).applyRowPick(&resP, prevP, &reqP, true); l == "" || reqP.Messages[2].Text != want.text ||
			reqP.Context.Service != "payments" || resP.Next.Pod != pods[i].Name || resP.Next.Namespace != want.ns {
			t.Fatalf("pod seçimi %d: label=%q text=%q next=%+v", i, l, reqP.Messages[2].Text, resP.Next)
		}
	}
	// bozuk kimlik → seçim yok
	if _, ok := rowPickRequest(chatRow{Kind: rowKindOperation, Name: "x", ID: "x"}, ""); ok {
		t.Fatal("bozuk operasyon kimliği çözülmemeli")
	}
	if _, ok := rowPickRequest(chatRow{Kind: rowKindPod, Name: "x", ID: "pod:c1"}, ""); ok {
		t.Fatal("bozuk pod kimliği çözülmemeli")
	}
	// önsöz bloğu servisi yazar
	if blk := chatRowsLineTR(ops, nil); !strings.Contains(blk, "2. operation checkout POST /api/pay") {
		t.Fatalf("önsöz: %q", blk)
	}
	// ilk liste kazanır
	stt := &chatStateTurn{}
	stt.noteRows(ops)
	stt.noteRows(pods)
	if len(stt.rows) != 2 || stt.rows[0].Kind != rowKindOperation {
		t.Fatalf("ilk liste kazanmalı: %+v", stt.rows)
	}
}

// Pod sağlığı demetinin satırları kanıtta İLK listelenen sırayla.
func TestPodHealthRows(t *testing.T) {
	svc := mcptools.PodHealthData{Service: "payments", Instances: []mcptools.PodInstanceRow{{ID: "payments-a"}, {ID: "payments-b"}}}
	if r := podHealthRows(svc, thanosPodsResult{}); len(r) != 2 || r[1].ID != "svc:payments|payments-b" {
		t.Fatalf("OTel envanteri: %+v", r)
	}
	th := thanosPodsResult{Rows: []thanos.PodRow{
		{Cluster: "prod-eu", Namespace: "shop", Pod: "payments-x", MemBytes: 1},
		{Cluster: "prod-eu", Namespace: "shop", Pod: "payments-y", Restarts: 3},
	}, CIDs: map[string]string{"prod-eu": "c-eu"}}
	if r := podHealthRows(mcptools.PodHealthData{Service: "payments"}, th); len(r) != 2 || r[0].ID != "pod:c-eu/shop/payments-y" {
		t.Fatalf("Thanos sırası (restart önce): %+v", r)
	}
	fleet := mcptools.PodHealthData{Heap: []mcptools.PodHeapRow{{Service: "checkout", Pod: "checkout-1"}}}
	if r := podHealthRows(fleet, thanosPodsResult{}); len(r) != 1 || r[0].Service != "checkout" || r[0].ID != "svc:checkout|checkout-1" {
		t.Fatalf("filo heap: %+v", r)
	}
}
