package api

// v0.10.2392 — etki kuralı geri oynatma ucu: yalnız admin, kelepçeler,
// anahtar tüm girdileri taşır, sahte okuyucuyla uçtan uca gövde.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

type irFakeReader struct {
	candCalls int
	cand      chstore.ImpactReplayCandidateReq
}

func (f *irFakeReader) ImpactReplayCandidates(_ context.Context, req chstore.ImpactReplayCandidateReq) ([]chstore.ImpactReplayCandidate, error) {
	f.candCalls++
	f.cand = req
	return []chstore.ImpactReplayCandidate{{Service: "svc-a"}}, nil
}

func (f *irFakeReader) ImpactReplaySeries(context.Context, chstore.ImpactReplaySeriesReq) ([]chstore.ImpactReplaySeries, error) {
	return nil, nil // geçmiş yok → atlanır; gövde şekli test edilir
}

func TestImpactReplayClampsAndKey(t *testing.T) {
	for in, want := range map[string]int{"": 7, "x": 7, "0": 7, "15": 7, "1": 1, "3": 3, "14": 14} {
		if got := impactReplayDays(in); got != want {
			t.Errorf("days(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]string{"": "failure_rate", "bogus": "failure_rate", "response_time": "response_time", "consumer_lag": "consumer_lag"} {
		if got := impactReplayDetector(in); got != want {
			t.Errorf("detector(%q) = %q", in, got)
		}
	}
	base := impactReplayInputs{Cfg: chstore.DefaultMLConfig(), Sens: chstore.AnomalySensitivityConfig{SeasonalTimezone: "UTC"}, Prio: chstore.DefaultProblemPriority()}
	k := impactReplayKey(7, "failure_rate", base)
	if k != impactReplayKey(7, "failure_rate", base) {
		t.Error("anahtar kararsız")
	}
	vary := map[string]string{
		"days":     impactReplayKey(3, "failure_rate", base),
		"detector": impactReplayKey(7, "response_time", base),
	}
	c := base
	c.Cfg.BaselineDays = 14
	vary["cfg"] = impactReplayKey(7, "failure_rate", c)
	c = base
	c.Sens.SeasonalTimezone = "Europe/Istanbul"
	vary["tz"] = impactReplayKey(7, "failure_rate", c)
	c = base
	c.Sens.Calendar.MonthEndAuto = true
	vary["calendar"] = impactReplayKey(7, "failure_rate", c)
	c = base
	c.Prio.BigBreachRatio = 3
	vary["prio"] = impactReplayKey(7, "failure_rate", c)
	for name, v := range vary {
		if v == k {
			t.Errorf("%s anahtarı değiştirmedi", name)
		}
	}
}

func TestImpactReplayRouteAdminOnly(t *testing.T) {
	if _, pat := (&Server{}).buildMux().Handler(httptest.NewRequest("GET", "/api/ml/impact-replay?days=7", nil)); pat != "GET /api/ml/impact-replay" {
		t.Errorf("kalıp = %q", pat)
	}
	fake := &irFakeReader{}
	prev := impactReplaySource
	impactReplaySource = func(*Server, context.Context) (ml.ImpactReplayReader, impactReplayInputs) {
		return fake, impactReplayInputs{Cfg: chstore.DefaultMLConfig(), Sens: chstore.AnomalySensitivityConfig{SeasonalTimezone: "UTC"}, Prio: chstore.DefaultProblemPriority()}
	}
	t.Cleanup(func() { impactReplaySource = prev })
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	mux := http.NewServeMux()
	s.registerMLImpactReplayRoutes(mux)
	for role, want := range map[string]int{auth.RoleViewer: http.StatusForbidden, auth.RoleEditor: http.StatusForbidden, auth.RoleAdmin: http.StatusOK} {
		r := httptest.NewRequest(http.MethodGet, "/api/ml/impact-replay?days=99&detector=nope", nil)
		r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-" + role, Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: kod %d, beklenen %d (%s)", role, w.Code, want, w.Body.String())
		}
		if role != auth.RoleAdmin {
			continue
		}
		var body ml.ImpactReplayResult
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		// kelepçe: days 99 → 7, bilinmeyen dedektör → failure_rate
		if body.Days != 7 || body.Detector != "failure_rate" || body.To-body.From != 7*86400 {
			t.Errorf("gövde: %+v", body)
		}
		if body.Episodes == nil || body.Skipped == nil || len(body.Notes) == 0 {
			t.Errorf("boş dilimler: %+v", body)
		}
		if fake.cand.Limit != ml.ImpactReplaySubjectCap+1 || fake.cand.To.Sub(fake.cand.From) != 7*24*time.Hour {
			t.Errorf("aday sınırı: %+v", fake.cand)
		}
	}
	if fake.candCalls != 1 {
		t.Errorf("viewer/editor isteği okuma yaptı: %d", fake.candCalls)
	}
}
