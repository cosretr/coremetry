package api

// cosre_open_problems_parity_test.go — v0.10.1695 (operatör kararı 2026-10-31):
// open_problems şeritle AYNI sayıyı verir. Tek servis kapsamında aracın planı
// şeridin SORGUSUNU (ServiceAttentionQuery → aynı önbellek girdisi) ve
// süzgecini (Attention) kullanır → total_open == şeridin "N açık konu"su.
// Belirti: v0.10.1691 Attention'sız okuyordu; "problem değil" kararlı
// imzaları, SLO burn-rate ve anomalileri de sayıyordu.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestOpenProblemsParityWithStrip(t *testing.T) {
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	body, err := json.Marshal(map[string]any{"items": openItemsFixture(), "total": 6, "limit": 20, "truncated": false})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := s.inboxView(inboxOpenValues(mcptools.ServiceAttentionQuery("orders-api")))
	s.l1.set(key, body, time.Minute)

	rec := httptest.NewRecorder()
	s.inbox(rec, httptest.NewRequest(http.MethodGet, "/api/inbox?service=orders-api&status=open&limit=20", nil))
	var view serviceOpenItemsView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("handler gövdesi: %v", err)
	}
	stripRows, _ := serviceAttentionItems(view.Items, nil)

	var tool mcp.Tool
	for _, tl := range mcptools.V2OnlyTools(mcptools.Deps{InboxOpen: s.inboxOpen}) {
		if tl.Name == mcptools.OpenProblemsName {
			tool = tl
		}
	}
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"services":["orders-api"]}`))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := out.(map[string]any)
	if len(stripRows) != 4 || m["total_open"] != len(stripRows) || m["slo_burn_problems"] != 1 {
		t.Fatalf("parite: open_problems total_open=%v slo=%v, şerit %d (want 4)", m["total_open"], m["slo_burn_problems"], len(stripRows))
	}
}
