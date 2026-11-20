package api

// Problem "Kök neden" etiketi: doğrulama (kapalı kategori,
// http(s) bağlantı, tek satır suçlu), rol kapıları (viewer okur, yazamaz),
// vaka yoksa Problem'den kurulur ve onaylı yazılır, düzenleme mevcut vakayı
// taşır, her yazım audit satırı düşer, taslak etiket sayılmaz.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestValidateRootCauseLabel(t *testing.T) {
	ok := rootCauseLabelInput{Cause: "  orders-db bağlantı havuzu doldu ", Category: "capacity", Culprit: " orders-db ", Link: "https://wiki.example.test/pm/12"}
	got, err := validateRootCauseLabel(ok)
	if err != nil || got.Cause != "orders-db bağlantı havuzu doldu" || got.Culprit != "orders-db" {
		t.Fatalf("geçerli girdi: %+v %v", got, err)
	}
	for name, in := range map[string]rootCauseLabelInput{
		"boş neden":          {Cause: "  ", Category: "deploy"},
		"bilinmeyen kat.":    {Cause: "x", Category: "dns"},
		"çok satırlı suçlu":  {Cause: "x", Category: "deploy", Culprit: "a\nb"},
		"uzun suçlu":         {Cause: "x", Category: "deploy", Culprit: strings.Repeat("ş", rootCauseCulpritMax+1)},
		"javascript bağlant": {Cause: "x", Category: "deploy", Link: "javascript:alert(1)"},
		"hostsuz bağlantı":   {Cause: "x", Category: "deploy", Link: "https://"},
		"uzun neden":         {Cause: strings.Repeat("a", caseFieldMax+1), Category: "deploy"},
	} {
		if _, err := validateRootCauseLabel(in); err == nil {
			t.Errorf("%s kabul edildi", name)
		}
	}
}

func TestLabelOfIgnoresDraft(t *testing.T) {
	if l := labelOf("p1", &chstore.IncidentCase{Status: chstore.CaseDraft, FinalCause: "x"}, nil); l != nil {
		t.Fatalf("taslak etiket sayıldı: %+v", l)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	l := labelOf("p1", &chstore.IncidentCase{CaseID: "p:p1", Status: chstore.CaseConfirmed, FinalCause: "x", Category: "deploy", ConfirmedBy: "a", ConfirmedAt: at},
		&chstore.ProblemRootCauseExtra{Culprit: "orders-api", Link: "https://x.test"})
	if l == nil || l.At != at.UnixMilli() || l.Culprit != "orders-api" || l.Link != "https://x.test" || l.By != "a" {
		t.Fatalf("etiket: %+v", l)
	}
}

// memRootCauseStore — rootCauseLabelStore'un bellek-içi sahtesi (vaka kısmı memCaseStore).
type memRootCauseStore struct {
	*memCaseStore
	mu       sync.Mutex
	problems map[string]chstore.Problem
	extras   map[string]chstore.ProblemRootCauseExtra
}

func (m *memRootCauseStore) GetProblem(_ context.Context, id string) (*chstore.Problem, error) {
	p, ok := m.problems[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (m *memRootCauseStore) GetProblemRootCauseExtra(_ context.Context, id string) (*chstore.ProblemRootCauseExtra, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.extras[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (m *memRootCauseStore) PutProblemRootCauseExtra(_ context.Context, e chstore.ProblemRootCauseExtra) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extras[e.ProblemID] = e
	return nil
}

func newRootCauseHarness(t *testing.T) (*caseHarness, *memRootCauseStore) {
	t.Helper()
	h := newCaseHarness(t) // p:p1 taslak (öneri "deploy 2.3"), p:p2 taslak
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC).UnixNano()
	st := &memRootCauseStore{memCaseStore: h.store, extras: map[string]chstore.ProblemRootCauseExtra{},
		problems: map[string]chstore.Problem{
			"p1": {ID: "p1", Service: "orders-api", RuleName: "error rate", StartedAt: start},
			"p9": {ID: "p9", Service: "billing", RuleName: "p99", StartedAt: start, Severity: "critical"},
		}}
	prev := rootCauseLabelStoreFor
	rootCauseLabelStoreFor = func(*Server) rootCauseLabelStore { return st }
	t.Cleanup(func() { rootCauseLabelStoreFor = prev })
	h.s.registerRootCauseLabelRoutes(h.mux)
	return h, st
}

func decodeRootCause(t *testing.T, body []byte) rootCauseLabelResponse {
	t.Helper()
	var out rootCauseLabelResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("json: %v %s", err, body)
	}
	return out
}

func TestRootCauseLabelRoles(t *testing.T) {
	h, st := newRootCauseHarness(t)
	body := `{"cause":"orders-db havuzu doldu","category":"capacity","culprit":"orders-db"}`
	if w := h.do(t, http.MethodPut, "/api/problems/p9/root-cause-label", body, "viewer"); w.Code != http.StatusForbidden {
		t.Fatalf("viewer yazdı: %d", w.Code)
	}
	if len(st.extras) != 0 || h.store.upserts != 0 || len(h.s.auditQ) != 0 {
		t.Fatal("reddedilen yazım iz bıraktı")
	}
	// Viewer OKUR: etiket yok → null + kategori kümesi (boş sayfa değil).
	w := h.do(t, http.MethodGet, "/api/problems/p9/root-cause-label", "", "viewer")
	if w.Code != http.StatusOK {
		t.Fatalf("viewer GET %d", w.Code)
	}
	if out := decodeRootCause(t, w.Body.Bytes()); out.Label != nil || len(out.Categories) == 0 {
		t.Fatalf("boş etiket: %+v", out)
	}
}

func TestRootCauseLabelCreatesCaseAndAudits(t *testing.T) {
	h, st := newRootCauseHarness(t)
	body := `{"cause":"billing-db indeks kilidi","category":"data","culprit":"db:billing","link":"https://pm.example.test/7"}`
	w := h.do(t, http.MethodPut, "/api/problems/p9/root-cause-label", body, "editor")
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body.String())
	}
	c := st.cases["p:p9"]
	if c.Status != chstore.CaseConfirmed || c.Source != "problem" || c.SourceID != "p9" || c.Service != "billing" ||
		c.FinalCause != "billing-db indeks kilidi" || c.Category != "data" || c.ConfirmedBy != "editor@example.test" || c.OpenedAt.IsZero() {
		t.Fatalf("kurulan vaka: %+v", c)
	}
	if e := st.extras["p9"]; e.Culprit != "db:billing" || e.Link != "https://pm.example.test/7" || e.By != "editor@example.test" {
		t.Fatalf("ek: %+v", e)
	}
	select {
	case e := <-h.s.auditQ:
		if e.Action != "problem.rootcause.set" || e.TargetID != "p9" || !strings.Contains(e.Details, `"createdCase":true`) {
			t.Errorf("audit: %+v", e)
		}
	default:
		t.Fatal("audit satırı yok")
	}
	out := decodeRootCause(t, h.do(t, http.MethodGet, "/api/problems/p9/root-cause-label", "", "viewer").Body.Bytes())
	if out.Label == nil || out.Label.Culprit != "db:billing" || out.Label.Cause != "billing-db indeks kilidi" {
		t.Fatalf("okunan etiket: %+v", out.Label)
	}
}

func TestRootCauseLabelEditCarriesDraft(t *testing.T) {
	h, st := newRootCauseHarness(t)
	// Taslak: GET öneriyi taşır, etiket yok.
	if out := decodeRootCause(t, h.do(t, http.MethodGet, "/api/problems/p1/root-cause-label", "", "viewer").Body.Bytes()); out.Label != nil || out.Suggested != "deploy 2.3" {
		t.Fatalf("taslak: %+v", out)
	}
	if w := h.do(t, http.MethodPut, "/api/problems/p1/root-cause-label", `{"cause":"deploy 2.3","category":"deploy"}`, "editor"); w.Code != http.StatusOK {
		t.Fatalf("PUT %d", w.Code)
	}
	<-h.s.auditQ
	c := st.cases["p:p1"]
	if c.Title != "error rate" || c.SuggestedCause != "deploy 2.3" || c.CosreCorrect != 1 {
		t.Fatalf("taslak alanları taşınmadı: %+v", c)
	}
	if w := h.do(t, http.MethodPut, "/api/problems/p1/root-cause-label", `{"cause":"orders-api 2.3 bağlantı sızıntısı","category":"code_bug","culprit":"orders-api"}`, "admin"); w.Code != http.StatusOK {
		t.Fatalf("düzenleme %d", w.Code)
	}
	e := <-h.s.auditQ
	if !strings.Contains(e.Details, `"edit":true`) {
		t.Errorf("düzenleme audit'i: %+v", e)
	}
	c = st.cases["p:p1"]
	if c.Category != "code_bug" || c.ConfirmedBy != "admin@example.test" || c.SuggestedCause != "deploy 2.3" || strings.Count(c.Provenance, "confirmed by") != 1 {
		t.Fatalf("düzenlenen vaka: %+v", c)
	}
}

func TestRootCauseLabelBounds(t *testing.T) {
	h, _ := newRootCauseHarness(t)
	for _, tc := range []struct {
		target, body string
		code         int
	}{
		{"/api/problems/nope/root-cause-label", `{"cause":"x","category":"deploy"}`, http.StatusNotFound},
		{"/api/problems/p9/root-cause-label", `{"cause":"x","category":"dns"}`, http.StatusBadRequest},
		{"/api/problems/p9/root-cause-label", `{"cause":"x","category":"deploy","link":"ftp://x"}`, http.StatusBadRequest},
		{"/api/problems/p9/root-cause-label", `not json`, http.StatusBadRequest},
	} {
		if w := h.do(t, http.MethodPut, tc.target, tc.body, "editor"); w.Code != tc.code {
			t.Errorf("%s %s → %d, beklenen %d", tc.target, tc.body, w.Code, tc.code)
		}
	}
	if len(h.s.auditQ) != 0 {
		t.Error("reddedilen yazım audit düştü")
	}
}
