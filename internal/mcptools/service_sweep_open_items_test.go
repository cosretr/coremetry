package mcptools

// v0.10.1694 (operator-reported) — service_health_sweep "Açık konular".
// Belirti: servis sayfası şeridi 13 açık konu (exception + httperror grupları)
// gösterirken tarama yalnız problems tablosunu okudu ve "veri yok" dedi. Bölüm
// artık Deps.InboxOpen'ı şeridin sorgusuyla (ServiceAttentionQuery) okur;
// 0 problem satırı + exception/httperror kalemleri raporlanmalı.

import (
	"context"
	"strings"
	"testing"
)

func TestSweepOpenItemsReportsInboxExceptions(t *testing.T) {
	r := baseSweepReaders()
	var got InboxOpenQuery
	r.openItems = func(_ context.Context, q InboxOpenQuery) (InboxOpenResult, error) {
		got = q
		return InboxOpenResult{Total: 3, Rows: []InboxRow{
			{Kind: "exception", Priority: "P1", Title: "java.net.SocketTimeoutException", Service: q.Service},
			{Kind: "httperror", Priority: "P2", Title: "HTTP 503 GET /orders", Service: q.Service},
			{Kind: "exception", Priority: "P3", Title: "java.lang.IllegalStateException", Service: q.Service},
		}}, nil
	}
	req := sweepReq()
	req.Service, req.Env = "orders-api", "prod"
	res, err := runServiceSweep(context.Background(), r, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	sec := sectionByName(res, sweepHopProblems)
	if sec.State != hopOK {
		t.Fatalf("state = %q, want ok (%s)", sec.State, sec.Summary)
	}
	for _, want := range []string{"3 açık konu", "P1 exception", "SocketTimeoutException", "/inbox?service=orders-api&prio=P1,P2,P3&env="} {
		if !strings.Contains(sec.Summary, want) {
			t.Errorf("summary %q içermeli: %q", sec.Summary, want)
		}
	}
	// Şerit ortamsız okur (serviceInboxHref env=): tarama da öyle, yoksa env
	// üyeliği şeridin gösterdiği satırı düşürebilir.
	if got != (InboxOpenQuery{Service: "orders-api", Limit: ServiceAttentionLimit, Attention: true}) {
		t.Errorf("sorgu %+v, want şeridin sorgusu", got)
	}
	if sec.EnvApplied == nil || *sec.EnvApplied {
		t.Errorf("EnvApplied = %v, want false", sec.EnvApplied)
	}
	if len(res.Data.OpenItems) != 3 || res.Data.OpenItems[1].Kind != "httperror" {
		t.Errorf("data.openItems = %+v", res.Data.OpenItems)
	}
	if !strings.Contains(strings.Join(res.Verdict.Reasons, ";"), "3 açık") {
		t.Errorf("verdict açık konu sayısını taşımalı: %v", res.Verdict.Reasons)
	}
}

func TestSweepOpenItemsEmptyAndCapped(t *testing.T) {
	r := baseSweepReaders()
	r.openItems = func(context.Context, InboxOpenQuery) (InboxOpenResult, error) {
		return InboxOpenResult{SLOCount: 1}, nil
	}
	res, _ := runServiceSweep(context.Background(), r, sweepReq(), nil)
	if sec := sectionByName(res, sweepHopProblems); sec.State != hopEmpty || !strings.Contains(sec.Summary, "açık konu yok (SLO burn-rate 1 uyarı)") {
		t.Errorf("boş: %+v", sec)
	}
	r.openItems = func(context.Context, InboxOpenQuery) (InboxOpenResult, error) {
		return InboxOpenResult{Total: 20, Truncated: true, Rows: []InboxRow{{Kind: "problem", Priority: "P2", Title: "x"}}}, nil
	}
	res, _ = runServiceSweep(context.Background(), r, sweepReq(), nil)
	if sec := sectionByName(res, sweepHopProblems); !strings.Contains(sec.Summary, "20+ açık konu") {
		t.Errorf("tavan: %q", sec.Summary)
	}
	r.openItems = nil
	res, _ = runServiceSweep(context.Background(), r, sweepReq(), nil)
	if sec := sectionByName(res, sweepHopProblems); sec.State != hopSkipped {
		t.Errorf("okuyucusuz: %+v", sec)
	}
}
