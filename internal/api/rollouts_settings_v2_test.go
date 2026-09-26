package api

// v0.10.957 — Rollouts v2 P1.5: /api/settings/rollouts GET/PUT gidiş-dönüşü
// (sahte depo; canlı CH YOK). v2 vidaları EKLEMELİ: v1 alanları ve `resolved`
// anahtarları aynen, v2 uygulanan değerleri `resolved`'a eklenir; anlaşılmaz
// girdi 400 (depo dokunulmaz), eski blob v2 varsayılanlarıyla okunur.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

type rolloutFakeStore struct {
	raw  []byte
	puts int
}

func (f *rolloutFakeStore) GetRolloutSettingsRaw(context.Context) ([]byte, error) { return f.raw, nil }
func (f *rolloutFakeStore) PutRolloutSettingsRaw(_ context.Context, raw []byte) error {
	f.puts++
	f.raw = raw
	return nil
}

func newRolloutSettingsEnv(t *testing.T, st rollout.SettingsStore) (*Server, *http.ServeMux) {
	t.Helper()
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	s.SetRollout(rollout.NewSettingsService())
	prev := rolloutSettingsStoreOf
	rolloutSettingsStoreOf = func(*Server) rollout.SettingsStore { return st }
	t.Cleanup(func() { rolloutSettingsStoreOf = prev })
	mux := http.NewServeMux()
	s.registerRolloutRoutes(mux)
	return s, mux
}

func rolloutAdminDo(mux *http.ServeMux, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/settings/rollouts", strings.NewReader(body))
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Email: "a@example.test", Role: auth.RoleAdmin}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestRolloutSettingsV2GetResolved(t *testing.T) {
	_, mux := newRolloutSettingsEnv(t, &rolloutFakeStore{})
	w := rolloutAdminDo(mux, "GET", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %d %s", w.Code, w.Body)
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	res := m["resolved"]
	// v1 anahtarları aynen duruyor.
	for _, k := range []string{"enabled", "intervalSec", "bucketSec", "threshold", "hysteresis", "exitHysteresis", "overlapMaxSec", "lookbackSec", "weakSignal", "stalledMinSec"} {
		if _, ok := res[k]; !ok {
			t.Errorf("v1 resolved anahtarı kayboldu: %s", k)
		}
	}
	want := map[string]any{"source": "v1", "detectorIntervalSec": float64(30), "stuckAfterSec": float64(600), "ignoreScale": true,
		"initialEvents": true, "observedGenWaitTicks": float64(10), "incarnationAbsentTicks": float64(3), "knownRevisionsMax": float64(32)}
	for k, v := range want {
		if res[k] != v {
			t.Errorf("resolved.%s = %v, beklenen %v", k, res[k], v)
		}
	}
	if kinds, _ := json.Marshal(res["kinds"]); string(kinds) != `["Deployment","StatefulSet","DaemonSet"]` {
		t.Errorf("resolved.kinds = %s", kinds)
	}
	if m["defaults"]["source"] != "v1" || m["defaults"]["detectorIntervalS"] != float64(30) || m["defaults"]["stuckAfter"] != "10m" {
		t.Errorf("defaults v2 varsayılanlarını taşımalı: %v", m["defaults"])
	}
}

func TestRolloutSettingsV2PutRoundTrip(t *testing.T) {
	st := &rolloutFakeStore{}
	s, mux := newRolloutSettingsEnv(t, st)
	body := `{"enabled":false,"interval":"60s","source":"v2","detectorIntervalS":20,"stuckAfter":"15m","ignoreScale":false,
	  "kinds":["deployment","DaemonSet"],"initialEvents":false,"observedGenWaitTicks":8,"incarnationAbsentTicks":5,"knownRevisionsMax":40}`
	w := rolloutAdminDo(mux, "PUT", body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	if st.puts != 1 || !strings.Contains(string(st.raw), `"source":"v2"`) || !strings.Contains(string(st.raw), `"knownRevisionsMax":40`) {
		t.Fatalf("blob yazılmalı: %d %s", st.puts, st.raw)
	}
	var m map[string]map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	res := m["resolved"]
	if res["source"] != "v2" || res["detectorIntervalSec"] != float64(20) || res["stuckAfterSec"] != float64(900) || res["ignoreScale"] != false ||
		res["initialEvents"] != false || res["observedGenWaitTicks"] != float64(8) || res["incarnationAbsentTicks"] != float64(5) || res["knownRevisionsMax"] != float64(40) {
		t.Fatalf("PUT cevabı uygulananı göstermeli: %v", res)
	}
	if kinds, _ := json.Marshal(res["kinds"]); string(kinds) != `["Deployment","DaemonSet"]` {
		t.Errorf("kinds kanonik: %s", kinds)
	}
	// Öteki pod aynı blobu okur.
	other := rollout.NewSettingsService()
	if err := other.LoadPersisted(context.Background(), st); err != nil || other.ResolvedV2().Source != rollout.SourceV2 {
		t.Fatalf("öteki pod: %v %+v", err, other.ResolvedV2())
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "rollouts.settings.update" || !strings.Contains(a.Details, `"source":"v2"`) {
			t.Fatalf("audit: %+v", a)
		}
	default:
		t.Fatal("PUT audit yazmalı")
	}
}

func TestRolloutSettingsV2PutValidation(t *testing.T) {
	st := &rolloutFakeStore{}
	s, mux := newRolloutSettingsEnv(t, st)
	for name, body := range map[string]string{
		"source":            `{"source":"v3"}`,
		"kinds":             `{"kinds":["Deployment","CronJob"]}`,
		"stuckAfter":        `{"stuckAfter":"on dakika"}`,
		"detectorIntervalS": `{"detectorIntervalS":-5}`,
		"knownRevisionsMax": `{"knownRevisionsMax":-1}`,
	} {
		w := rolloutAdminDo(mux, "PUT", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), name) {
			t.Errorf("%s: %d %s — 400 + alan adı beklenirdi", name, w.Code, w.Body)
		}
	}
	if st.puts != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen PUT yazmamalı / audit etmemeli")
	}
	// Geçerli ama depo yok → 503 (nil *chstore.Store'u arayüze sarıp panik değil).
	rolloutSettingsStoreOf = func(*Server) rollout.SettingsStore { return nil }
	if w := rolloutAdminDo(mux, "PUT", `{"source":"v2"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("depo yok → %d, beklenen 503", w.Code)
	}
}
