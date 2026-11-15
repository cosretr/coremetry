package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2197 — router boşlukları D3-F (chat_router_d3f.go).

func TestBusinessCodeToken(t *testing.T) {
	cases := map[string]string{
		"kod DQP1080 için tekrar kontrol":    "DQP1080",
		"DQP1080 hatası nerede":              "DQP1080",
		"AB123 ne":                           "AB123",
		"HTTP500 hataları":                   "",
		"OP_NAME_REST neden hata veriyor":    "",
		"dqp1080 küçük harf":                 "",
		"P1 problemler":                      "",
		"ABCDEF1234 altı harf":               "",
		"orders-api son 1 saatte hata oranı": "",
	}
	for q, want := range cases {
		if got := businessCodeToken(q); got != want {
			t.Errorf("%q → %q, want %q", q, got, want)
		}
	}
}

func TestPromoteD3F(t *testing.T) {
	q := "OP_NAME_REST operasyonu neden hata veriyor?"
	if r := promoteD3F(guidedRoute{Intent: guidedProblems}, q, true); r.OpNotFound != "OP_NAME_REST" || d3fRouteLabel(r) != guidedOpNotFoundRoute {
		t.Errorf("v2 katalog dışı op: %+v", r)
	}
	if r := promoteD3F(guidedRoute{Intent: guidedProblems}, q, false); r.OpNotFound != "" || r.Intent != guidedProblems {
		t.Errorf("v1 değişmemeli: %+v", r)
	}
	if r := promoteD3F(guidedRoute{Intent: guidedNone, Env: "prod"}, "kod DQP1080 için tekrar kontrol", true); r.BusinessCode != "DQP1080" || r.Env != "prod" || d3fRouteLabel(r) != guidedBusinessCodeRoute {
		t.Errorf("v2 iş kodu: %+v", r)
	}
	// Servisli soru ya da özel rota ezilmez.
	if r := promoteD3F(guidedRoute{Intent: guidedProblems, Service: "svc-a"}, "svc-a DQP1080", true); r.BusinessCode != "" {
		t.Errorf("servisli soru: %+v", r)
	}
	if r := promoteD3F(guidedRoute{Intent: guidedLogErrors}, "DQP1080 logları", true); r.BusinessCode != "" {
		t.Errorf("log rotası ezilmemeli: %+v", r)
	}
}

func TestOpNotFoundAnswer(t *testing.T) {
	cands := []chstore.CatalogOperation{
		{Operation: "OP_NAME_RESTV2", Service: "svc-a", CallCount: 10},
		{Operation: "OP_NAME_RESTV2", Service: "svc-b", CallCount: 5},
		{Operation: "OTHER_REST_CALL", Service: "svc-c", CallCount: 99},
		{Operation: "OP_NAME_SOAP", Service: "svc-a", CallCount: 3},
	}
	near := opNearNames("OP_NAME_REST", cands, opNearMax)
	if len(near) == 0 || near[0] != "OP_NAME_RESTV2" || len(near) > opNearMax {
		t.Fatalf("en yakın: %v", near)
	}
	q := "OP_NAME_REST operasyonu neden hata veriyor?"
	text, sugg := opNotFoundAnswerTR("OP_NAME_REST", q, near)
	if !strings.HasPrefix(text, "Operasyon bulunamadı: OP_NAME_REST") || !strings.Contains(text, "OP_NAME_RESTV2") {
		t.Errorf("metin: %q", text)
	}
	if len(sugg) != len(near) || sugg[0] != "OP_NAME_RESTV2 operasyonu neden hata veriyor?" {
		t.Errorf("öneri sorunun kendisi, ad değişmiş olmalı: %v", sugg)
	}
	if text, sugg := opNotFoundAnswerTR("OP_NAME_REST", q, nil); !strings.Contains(text, "yakın bir ad da bulunamadı") || sugg != nil {
		t.Errorf("aday yok: %q %v", text, sugg)
	}
	if got := opNearQueryTokens("OP_NAME_REST"); strings.Join(got, ",") != "OP_NAME_REST,NAME,REST" {
		t.Errorf("parçalar: %v", got)
	}
}

func TestBizCodeAnswer(t *testing.T) {
	t0 := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	l := bizCodeLookup{
		Seen: []chstore.BusinessCodeSeenRow{
			{Kind: "function_code", Service: "svc-a", Calls: 1200, Errors: 30, FirstSeen: t0, LastSeen: t0.Add(48 * time.Hour)},
			{Kind: "function_code", Service: "svc-b", Calls: 40, Errors: 0, FirstSeen: t0.Add(-time.Hour), LastSeen: t0},
		},
		Ops: []chstore.CatalogOperation{{Operation: "DEPOSIT_CREATE", Service: "svc-a", CallCount: 900, ErrorCount: 20}},
	}
	text, sugg := bizCodeAnswerTR("DQP1080", l, time.UTC)
	for _, want := range []string{"DQP1080 (FUNCTION_CODE)", "1,240", "ilk görülme 10.11 07:00", "son görülme 12.11 08:00", "svc-a", "DEPOSIT_CREATE · svc-a"} {
		if !strings.Contains(text, want) {
			t.Errorf("metinde %q yok:\n%s", want, text)
		}
	}
	if len(sugg) == 0 || sugg[0] != "DEPOSIT_CREATE operasyonuna bak" {
		t.Errorf("öneriler: %v", sugg)
	}
	if text, sugg := bizCodeAnswerTR("DQP1080", bizCodeLookup{}, nil); !strings.HasPrefix(text, "DQP1080 kodu son 7 günde bulunamadı") || sugg != nil {
		t.Errorf("bulunamadı: %q %v", text, sugg)
	}
}

func TestGuidedD3FAnswerSeams(t *testing.T) {
	prevN, prevB := opNearSeam, bizCodeSeam
	t.Cleanup(func() { opNearSeam, bizCodeSeam = prevN, prevB })
	var gotToks []string
	var gotLimit int
	opNearSeam = func(_ context.Context, toks []string, limit int) ([]chstore.CatalogOperation, error) {
		gotToks, gotLimit = toks, limit
		return []chstore.CatalogOperation{{Operation: "OP_NAME_RESTV2"}}, nil
	}
	bizCodeSeam = func(_ context.Context, code string, from, to time.Time) (bizCodeLookup, error) {
		if code != "DQP1080" || to.Sub(from) != bizCodeLookbackS*time.Second {
			t.Errorf("okuma: %s %v", code, to.Sub(from))
		}
		return bizCodeLookup{}, nil
	}
	var answers []map[string]any
	emit := func(ev string, v any) {
		if ev == "answer" {
			answers = append(answers, v.(map[string]any))
		}
	}
	s := &Server{}
	h, ok := s.guidedD3FAnswer(context.Background(), emit, guidedRoute{Intent: guidedAskService, OpNotFound: "OP_NAME_REST"}, "OP_NAME_REST neden hata veriyor", time.Now(), time.UTC)
	if !h || !ok || gotLimit != opCandidateLimit || len(gotToks) == 0 {
		t.Fatalf("op kolu: h=%v ok=%v limit=%d toks=%v", h, ok, gotLimit, gotToks)
	}
	h, ok = s.guidedD3FAnswer(context.Background(), emit, guidedRoute{Intent: guidedAskService, BusinessCode: "DQP1080"}, "DQP1080", time.Now(), time.UTC)
	if !h || !ok || len(answers) != 2 || !strings.Contains(answers[1]["text"].(string), "bulunamadı") {
		t.Fatalf("iş kodu kolu: %v", answers)
	}
	if h, _ := s.guidedD3FAnswer(context.Background(), emit, guidedRoute{Intent: guidedProblems}, "x", time.Now(), nil); h {
		t.Error("başka rota sahiplenilmemeli")
	}
}
