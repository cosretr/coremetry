package api

// CoSRE replay notlaması uçları: okuma viewer'a açık, başlatma ve
// zamanlama yalnız admin + audit; sınırlar 400; sürerken 409; başlatma canlı
// araştırma kaydını yazmaz (yalnız "sre-replay-<runKey>-<problemId>" işleri).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sre"
)

type sreReplayFake struct {
	*sreFakeStore
	reports map[string]chstore.SavedView
	cases   []chstore.IncidentCase
	extras  map[string]chstore.ProblemRootCauseExtra
}

func (f *sreReplayFake) ListIncidentCases(_ context.Context, q chstore.IncidentCaseQuery) ([]chstore.IncidentCase, error) {
	if q.Status != chstore.CaseConfirmed || q.Source != "problem" {
		return nil, nil
	}
	return f.cases, nil
}
func (f *sreReplayFake) ListProblemRootCauseExtras(_ context.Context, ids []string) (map[string]chstore.ProblemRootCauseExtra, error) {
	out := map[string]chstore.ProblemRootCauseExtra{}
	for _, id := range ids {
		if e, ok := f.extras[id]; ok {
			out[id] = e
		}
	}
	return out, nil
}
func (f *sreReplayFake) ListSREReplayRows(_ context.Context, runID string, _ int) ([]chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []chstore.SavedView{}
	for id, v := range f.rows {
		if strings.HasPrefix(id, "sre-replay-"+runID+"-") {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *sreReplayFake) ListSREReplayReports(context.Context, int) ([]chstore.SavedView, error) {
	out := []chstore.SavedView{}
	for _, v := range f.reports {
		out = append(out, v)
	}
	return out, nil
}
func (f *sreReplayFake) GetSREReplayReport(_ context.Context, id string) (*chstore.SavedView, error) {
	v, ok := f.reports[chstore.SREReplayReportID(id)]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (f *sreReplayFake) PutSREReplayReport(_ context.Context, id, state, body string, createdAt int64) error {
	f.reports[chstore.SREReplayReportID(id)] = chstore.SavedView{ID: chstore.SREReplayReportID(id), Page: chstore.SREReplayReportPage,
		Name: state, QueryString: body, CreatedAt: createdAt}
	return nil
}

func newReplayTestEnv(t *testing.T) (*Server, *http.ServeMux, *sreReplayFake) {
	t.Helper()
	s, _, st := newSRETestEnv(t)
	start := time.Now().Add(-48 * time.Hour)
	st.problems["p9"] = chstore.Problem{ID: "p9", Service: "pay", Status: "resolved", RuleID: "r", StartedAt: start.UnixNano()}
	rf := &sreReplayFake{sreFakeStore: st, reports: map[string]chstore.SavedView{},
		extras: map[string]chstore.ProblemRootCauseExtra{"p9": {ProblemID: "p9", Culprit: "db:pay"}},
		cases: []chstore.IncidentCase{{CaseID: "p:p9", Source: "problem", SourceID: "p9", Service: "pay", Category: "dependency",
			FinalCause: "pay-db kilitlendi", Status: chstore.CaseConfirmed, OpenedAt: start}}}
	prevS, prevC := sreReplayStoreOf, sreReplaySettingsOf
	sreReplayStoreOf = func(*Server) sre.ReplayStore { return rf }
	sreReplaySettingsOf = func(*Server) sre.SettingsStore { return st }
	t.Cleanup(func() { sreReplayStoreOf, sreReplaySettingsOf = prevS, prevC })
	mux := http.NewServeMux()
	s.registerSREReplayRoutes(mux)
	return s, mux, rf
}

func TestSREReplayRoles(t *testing.T) {
	s, mux, rf := newReplayTestEnv(t)
	if w := sreDo(mux, "GET", "/api/sre/replay/reports", "", auth.RoleViewer); w.Code != 200 || !strings.Contains(w.Body.String(), `"reports":[]`) ||
		!strings.Contains(w.Body.String(), `"weekly":false`) {
		t.Fatalf("viewer GET = %d %s", w.Code, w.Body)
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := sreDo(mux, "POST", "/api/sre/replay/reports", `{}`, role); w.Code != http.StatusForbidden {
			t.Fatalf("%s POST = %d", role, w.Code)
		}
		if w := sreDo(mux, "PUT", "/api/sre/replay/schedule", `{"weekly":true}`, role); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT zamanlama = %d", role, w.Code)
		}
	}
	if len(rf.reports) != 0 || len(rf.rows) != 0 || len(rf.settings) != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen yazım iz bıraktı")
	}
	for _, bad := range []string{`{"repeats":4}`, `{"limit":31}`, `{"days":-1}`, `nope`} {
		if w := sreDo(mux, "POST", "/api/sre/replay/reports", bad, auth.RoleAdmin); w.Code != 400 {
			t.Errorf("%s = %d, 400 beklenir", bad, w.Code)
		}
	}
	w := sreDo(mux, "POST", "/api/sre/replay/reports", `{"days":30,"repeats":2,"variant":{"contextPack":true}}`, auth.RoleAdmin)
	if w.Code != 200 {
		t.Fatalf("admin POST = %d %s", w.Code, w.Body)
	}
	var d sreReplayDetail
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d.ReportID == "" || len(d.Items) != 1 || len(d.Items[0].Runs) != 2 || d.Items[0].Culprit != "db:pay" || !d.Variant.ContextPack {
		t.Fatalf("rapor: %s", w.Body)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "sre.replay.run" || a.TargetID != d.ReportID || !strings.Contains(a.Details, `"queued":2`) {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("POST audit yazmalı")
	}
	if _, ok := rf.rows[chstore.SREInvestigationID("p9")]; ok {
		t.Fatal("canlı araştırma kaydı yazıldı")
	}
	for id := range rf.rows {
		if !strings.HasPrefix(id, "sre-replay-"+d.ReportID+"n") {
			t.Errorf("rapor dışı satır: %s", id)
		}
	}
	if w := sreDo(mux, "POST", "/api/sre/replay/reports", `{}`, auth.RoleAdmin); w.Code != http.StatusConflict {
		t.Errorf("sürerken ikinci rapor = %d", w.Code)
	}
	if w := sreDo(mux, "GET", "/api/sre/replay/reports/"+d.ReportID, "", auth.RoleViewer); w.Code != 200 || !strings.Contains(w.Body.String(), `"summary"`) {
		t.Errorf("viewer ayrıntı = %d %s", w.Code, w.Body)
	}
	if w := sreDo(mux, "GET", "/api/sre/replay/reports/bad-id", "", auth.RoleViewer); w.Code != 400 {
		t.Errorf("geçersiz kimlik = %d", w.Code)
	}
	if w := sreDo(mux, "GET", "/api/sre/replay/reports/rnope", "", auth.RoleViewer); w.Code != 404 {
		t.Errorf("olmayan rapor = %d", w.Code)
	}
	if w := sreDo(mux, "PUT", "/api/sre/replay/schedule", `{"weekly":true,"days":90,"repeats":3}`, auth.RoleAdmin); w.Code != 200 {
		t.Fatalf("zamanlama PUT = %d %s", w.Code, w.Body)
	}
	sc, _ := sre.GetReplaySchedule(context.Background(), rf.sreFakeStore)
	if !sc.Weekly || sc.Days != 90 || sc.Repeats != 3 || sc.UpdatedBy != "admin@example.test" {
		t.Errorf("zamanlama: %+v", sc)
	}
	if a := <-s.auditQ; a.Action != "sre.replay.schedule" {
		t.Errorf("zamanlama audit = %+v", a)
	}
}
