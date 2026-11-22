package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

type memFailedTurnStore struct {
	rows []chstore.CosreInteraction
	sigs []chstore.CosreSignal
}

func (m *memFailedTurnStore) ListCosreInteractionsForScoring(context.Context, time.Time, time.Time) ([]chstore.CosreInteraction, error) {
	return m.rows, nil
}
func (m *memFailedTurnStore) ListCosreSignals(context.Context, time.Time, time.Time) ([]chstore.CosreSignal, error) {
	return m.sigs, nil
}

func TestFailedTurnsViewOf(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	rows := []chstore.CosreInteraction{
		{ExchangeID: "a", UserEmail: "u@example.com", CreatedAt: t0, Question: "checkout sağlığı nasıl", Status: "ok", Tier: "clarify"},
		{ExchangeID: "b", UserEmail: "u@example.com", CreatedAt: t0.Add(20 * time.Second), Question: "checkout sağlığı nasıl?", Status: "ok"},
		{ExchangeID: "c", UserEmail: "v@example.com", CreatedAt: t0, Question: "kafka lag", Status: "ok"},
	}
	v := failedTurnsViewOf(7, rows, nil, t0)
	if v.Total != 1 || v.Scanned != 3 || v.Turns[0].ExchangeID != "a" ||
		!reflect.DeepEqual(v.Turns[0].Reasons, []string{cosrelearn.FailReask, cosrelearn.FailClarifyOnly}) {
		t.Fatalf("görünüm: %+v", v)
	}
	if len(v.ByReason) != 2 || len(v.Clusters) != 1 || v.Clusters[0].Count != 1 {
		t.Fatalf("sayaç / küme: %+v %+v", v.ByReason, v.Clusters)
	}
}

func TestFailedTurnsRouteAdminOnly(t *testing.T) {
	prev := failedTurnStoreFor
	failedTurnStoreFor = func(*Server) failedTurnStore { return &memFailedTurnStore{} }
	t.Cleanup(func() { failedTurnStoreFor = prev })
	s := &Server{}
	mux := http.NewServeMux()
	s.registerFailedTurnRoutes(mux)
	for role, want := range map[string]int{auth.RoleViewer: http.StatusForbidden, auth.RoleEditor: http.StatusForbidden} {
		r := httptest.NewRequest(http.MethodGet, "/api/ai/failed-turns?days=30", nil)
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u", Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d", role, w.Code)
		}
	}
}

func TestBuildPromotedCandidate(t *testing.T) {
	tool := v2Tool(t)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	inter := chstore.CosreInteraction{Question: "Payments-API sağlığı nasıl? payments-api", Answer: "payments-api sağlıklı", Tier: "guided"}
	req := promoteTurnReq{
		ExchangeID: "ex9", ExpectTier: "guided", ExpectRoute: "service_health", AnyOfTools: []string{tool, " "},
		MustContain: []string{"payments-api"}, MustNotContain: []string{"kubectl"},
		Anonymize: []promoteAnon{{From: "payments-api", To: "checkout"}}, Reasons: []string{cosrelearn.FailReask, "bogus"},
	}
	c, err := buildPromotedCandidate(req, inter, map[string]bool{tool: true}, "admin@example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Question != "checkout sağlığı nasıl? checkout" || c.Answer != "" || !c.Anonymized || c.BindService != "checkout" ||
		!reflect.DeepEqual(c.MustContain, []string{"checkout"}) || !reflect.DeepEqual(c.AnyOfTools, []string{tool}) ||
		!reflect.DeepEqual(c.Signals, []string{cosrelearn.FailReask}) || !c.Accepted || c.Source != evalCandidateSourcePromoted || c.ID != "evp-ex9" {
		t.Fatalf("aday: %+v", c)
	}
	// Anonimleştirme seçilmezse metin ve cevap aynen (otomatik maskeleme yok).
	req.Anonymize = nil
	c, _ = buildPromotedCandidate(req, inter, map[string]bool{tool: true}, "a", now)
	if c.Question != inter.Question || c.Answer != inter.Answer || c.Anonymized || c.BindService != "" {
		t.Fatalf("anonimsiz: %+v", c)
	}
	ec := evalCandidateCase(c)
	if !ec.real || ec.ID != "real-ex9" || ec.Expect.ExpectRoute != "service_health" || ec.Expect.RequireEvidence || evalAgentSkipIntent(ec) {
		t.Fatalf("vaka: %+v", ec)
	}
	for _, bad := range []promoteTurnReq{
		{ExchangeID: "x", AnyOfTools: []string{"no_such_tool"}},
		{ExchangeID: "x", ExpectRoute: "Service Health"},
		{ExchangeID: "x", Anonymize: []promoteAnon{{From: "a"}}},
	} {
		if _, err := buildPromotedCandidate(bad, inter, map[string]bool{tool: true}, "a", now); err == nil {
			t.Fatalf("geçersiz istek kabul edildi: %+v", bad)
		}
	}
	if _, err := buildPromotedCandidate(promoteTurnReq{ExchangeID: "x"}, chstore.CosreInteraction{}, nil, "a", now); err == nil {
		t.Fatal("sorusuz tur terfi edildi")
	}
}

func TestPromoteFlowAndDailyExclusion(t *testing.T) {
	st := newMemCandidateStore()
	st.inter["ex1"] = chstore.CosreInteraction{ExchangeID: "ex1", Question: "checkout kafka lag var mı", Tier: "guided"}
	useCandidateStore(t, st)
	s := &Server{copilot: nil, auditQ: make(chan chstore.AuditEntry, 4)}
	mux := http.NewServeMux()
	s.registerFailedTurnRoutes(mux)
	post := func(body string, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/ai/failed-turns/promote", bytes.NewBufferString(body))
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u1", Email: "admin@example.com", Role: role}))
		w := httptest.NewRecorder()
		// requireCopilot kapısı copilot'suz sunucuda 503 verir; akışı doğrudan sına.
		if role == auth.RoleAdmin {
			s.promoteFailedTurn(w, r)
		} else {
			mux.ServeHTTP(w, r)
		}
		return w
	}
	if w := post(`{"exchangeId":"ex1"}`, auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Fatalf("editör terfi: %d", w.Code)
	}
	if w := post(`{"exchangeId":"missing"}`, auth.RoleAdmin); w.Code != http.StatusNotFound {
		t.Fatalf("olmayan tur: %d", w.Code)
	}
	w := post(`{"exchangeId":"ex1","expectTier":"guided","expectRoute":"messaging_health","reasons":["reask"]}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("terfi %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		CaseID string `json:"caseId"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.CaseID != "real-ex1" {
		t.Fatalf("caseId %q", out.CaseID)
	}
	select {
	case e := <-s.auditQ:
		if e.Action != "evalset.turn.promote" {
			t.Fatalf("audit %+v", e)
		}
	default:
		t.Fatal("audit satırı yok")
	}
	// Yeniden terfi = düzenleme: satır sayısı aynı, oluşturma zamanı korunur.
	created := st.rows["evp-ex1"].CreatedAt
	if w := post(`{"exchangeId":"ex1","expectRoute":"service_health"}`, auth.RoleAdmin); w.Code != http.StatusOK || len(st.rows) != 1 || st.rows["evp-ex1"].CreatedAt != created {
		t.Fatalf("yeniden terfi: %d rows=%d", w.Code, len(st.rows))
	}
	// Gerçek sorular kümesinde var, günlük koşuda YOK (yalnız açık tıklama).
	cases, seed, promoted, err := s.evalRealCases(context.Background())
	if err != nil || promoted != 1 || len(cases) != seed+1 || cases[len(cases)-1].Expect.ExpectRoute != "service_health" {
		t.Fatalf("gerçek küme: n=%d seed=%d promoted=%d err=%v", len(cases), seed, promoted, err)
	}
	for _, c := range s.evalDailyCandidateCases(context.Background()) {
		if strings.HasPrefix(c.ID, evalPromotedCasePrefix) {
			t.Fatalf("terfi vakası günlük koşuya girdi: %s", c.ID)
		}
	}
}
