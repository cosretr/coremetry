package api

// preferences_display_test.go — v0.10.1832 (Log Search Faz 1 dilim 1.9):
// görünüm tercihi ucu — şekil doğrulaması, yalnız kendi satırı (authz),
// gidiş-dönüş, gövde tavanı, claims yoksa 401.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type memSavedViews struct {
	mu   sync.Mutex
	rows map[string]chstore.SavedView
}

func (m *memSavedViews) GetSavedView(_ context.Context, id string) (*chstore.SavedView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.rows[id]; ok {
		return &v, nil
	}
	return nil, nil
}
func (m *memSavedViews) UpsertSavedView(_ context.Context, v chstore.SavedView) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[v.ID] = v
	return nil
}

func TestValidateDisplayPref(t *testing.T) {
	for _, c := range []struct {
		b  displayPrefBody
		ok bool
	}{
		{displayPrefBody{V: 1}, true},
		{displayPrefBody{V: 1, RH: "2", HH: "auto", Den: "compact", PP: 250, Smp: 1000}, true},
		{displayPrefBody{V: 2}, false},
		{displayPrefBody{V: 1, RH: "4"}, false},
		{displayPrefBody{V: 1, HH: "x"}, false},
		{displayPrefBody{V: 1, Den: "tight"}, false},
		{displayPrefBody{V: 1, PP: 99}, false},
		{displayPrefBody{V: 1, Smp: 9}, false},
		{displayPrefBody{V: 1, Smp: 5001}, false},
	} {
		if err := validateDisplayPref(c.b); (err == nil) != c.ok {
			t.Errorf("%+v: err=%v ok=%v", c.b, err, c.ok)
		}
	}
	if canonicalDisplayPrefJSON(displayPrefBody{V: 1, PP: 100, RH: "2"}) != `{"v":1,"rh":"2","pp":100}` {
		t.Errorf("kanonik biçim: %s", canonicalDisplayPrefJSON(displayPrefBody{V: 1, PP: 100, RH: "2"}))
	}
}

func displayReq(method, uid, body string) *http.Request {
	r := httptest.NewRequest(method, "/api/preferences/logs/display", strings.NewReader(body))
	r.SetPathValue("key", "logs")
	if uid != "" {
		r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: uid, Role: auth.RoleViewer}))
	}
	return r
}

func TestDisplayPrefRoundTripAndOwnerIsolation(t *testing.T) {
	st := &memSavedViews{rows: map[string]chstore.SavedView{}}
	s := &Server{}
	w := httptest.NewRecorder()
	serveDisplayPrefPutWith(w, displayReq("PUT", "u-alice", `{"v":1,"rh":"3","den":"expanded","pp":50,"smp":800}`), s, st)
	if w.Code != 200 {
		t.Fatalf("PUT %d %s", w.Code, w.Body.String())
	}
	row := st.rows["pref:u-alice:logs:display"]
	if row.Page != "table:logs" || row.Name != "display" || row.OwnerID != "u-alice" {
		t.Fatalf("satır: %+v", row)
	}
	if _, clash := st.rows["pref:u-alice:logs"]; clash {
		t.Fatal("kolon modeli satırına yazılmamalı")
	}
	get := func(uid string) map[string]json.RawMessage {
		w := httptest.NewRecorder()
		serveDisplayPrefGetWith(w, displayReq("GET", uid, ""), s, st)
		var out map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if string(get("u-alice")["display"]) != `{"v":1,"rh":"3","den":"expanded","pp":50,"smp":800}` {
		t.Errorf("gidiş-dönüş: %s", get("u-alice")["display"])
	}
	if string(get("u-bob")["display"]) != "null" {
		t.Errorf("başka kullanıcının tercihi görünmemeli: %s", get("u-bob")["display"])
	}
	// Elle bozulmuş sahip alanı (aynı id, farklı owner) yine görünmez.
	r := st.rows["pref:u-alice:logs:display"]
	r.OwnerID = "u-mallory"
	st.rows["pref:u-alice:logs:display"] = r
	if string(get("u-alice")["display"]) != "null" {
		t.Error("owner eşleşmezse yok sayılmalı")
	}
}

func TestDisplayPrefRejects(t *testing.T) {
	st := &memSavedViews{rows: map[string]chstore.SavedView{}}
	s := &Server{}
	for _, c := range []struct {
		body string
		code int
	}{
		{`{"v":1,"pp":7}`, http.StatusBadRequest},
		{`{bozuk`, http.StatusBadRequest},
		{`{"v":1,"rh":"` + strings.Repeat("x", 5000) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		w := httptest.NewRecorder()
		serveDisplayPrefPutWith(w, displayReq("PUT", "u1", c.body), s, st)
		if w.Code != c.code {
			t.Errorf("%.30s: %d; want %d", c.body, w.Code, c.code)
		}
	}
	if len(st.rows) != 0 {
		t.Error("reddedilen istek yazmamalı")
	}
	// Claims yok → 401 (mux üzerinden, gerçek kayıt).
	mux := (&Server{}).buildMux()
	for _, m := range []string{"GET", "PUT"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(m, "/api/preferences/logs/display", strings.NewReader(`{"v":1}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s claims yokken %d", m, rec.Code)
		}
	}
	// Geçersiz anahtar 400.
	w := httptest.NewRecorder()
	r := displayReq("GET", "u1", "")
	r.SetPathValue("key", "../etc")
	serveDisplayPrefGetWith(w, r, s, st)
	if w.Code != http.StatusBadRequest {
		t.Errorf("geçersiz key %d", w.Code)
	}
}
