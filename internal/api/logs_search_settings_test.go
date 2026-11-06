package api

// logs_search_settings_test.go — v0.10.1831 (Log Search Faz 1 dilim 1.8):
// örnek boyu üst sınırı (logs_search.maxSample), istek limiti kelepçesi, PUT
// doğrulaması + admin kapısı + audit/yayın pini, dar _source parametresi ve
// önbellek anahtarı (kolon KÜMESİ sıralı özet — v0.5.187), doküman ucu.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogsSampleLimit(t *testing.T) {
	for _, c := range []struct{ want, max, got int }{
		{0, 2000, 100}, {-3, 2000, 100}, {1, 2000, 1}, {50, 2000, 50}, {500, 2000, 500},
		{2000, 2000, 2000}, {2001, 2000, 2000}, {9000, 5000, 5000}, {600, 500, 500},
	} {
		if g := logsSampleLimit(c.want, c.max); g != c.got {
			t.Errorf("logsSampleLimit(%d, %d) = %d; want %d", c.want, c.max, g, c.got)
		}
	}
}

func TestLogsSearchSettingsNormalizeValidate(t *testing.T) {
	for _, c := range []struct {
		in      int
		norm    int
		wantErr bool
	}{
		{0, 2000, false}, {10, 10, false}, {500, 500, false}, {5000, 5000, false},
		{9, 2000, true}, {5001, 2000, true}, {-1, 2000, true},
	} {
		if n := normalizeLogsSearchSettings(logsSearchSettings{MaxSample: c.in}).MaxSample; n != c.norm {
			t.Errorf("normalize(%d) = %d; want %d", c.in, n, c.norm)
		}
		_, err := validateLogsSearchSettings(logsSearchSettings{MaxSample: c.in})
		if (err != nil) != c.wantErr {
			t.Errorf("validate(%d) err=%v; wantErr %v", c.in, err, c.wantErr)
		}
	}
	if decodeLogsSearchSettings([]byte(`{bozuk`)).MaxSample != logsSampleDefaultMax {
		t.Error("bozuk blob varsayılana düşmeli")
	}
	if decodeLogsSearchSettings([]byte(`{"maxSample":750}`)).MaxSample != 750 {
		t.Error("kayıtlı değer okunmalı")
	}
	if logsSampleHardCap != logstore.SearchLimitCap {
		t.Errorf("api tavanı (%d) logstore tavanıyla (%d) aynı olmalı", logsSampleHardCap, logstore.SearchLimitCap)
	}
}

func TestLogsSearchSettingsRoutesGateAndPins(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerLogsSearchSettingsRoutes(mux)
	do := func(method, role string) int {
		r := httptest.NewRequest(method, "/api/settings/logs-search", strings.NewReader(`{"maxSample":800}`))
		r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u1", Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if c := do("GET", auth.RoleViewer); c != 200 {
		t.Errorf("viewer GET %d; 200 beklendi (salt-okunur görür)", c)
	}
	if c := do("PUT", auth.RoleViewer); c != http.StatusForbidden {
		t.Errorf("viewer PUT %d; 403 beklendi", c)
	}
	if c := do("PUT", auth.RoleEditor); c != http.StatusForbidden {
		t.Errorf("editor PUT %d; 403 beklendi", c)
	}
	if c := do("PUT", auth.RoleAdmin); c != http.StatusServiceUnavailable {
		t.Errorf("admin PUT (store yok) %d; kapıdan geçip 503 beklendi", c)
	}
	src, err := os.ReadFile("logs_search_settings.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`s.audit(r, "logs_search.settings", "settings", logsSearchSettingKey, string(raw))`,
		`s.publishConfigReload(r.Context(), "logs_search")`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("PUT yolu %q içermeli", want)
		}
	}
}

func TestParseLogsSourceFieldsAndKey(t *testing.T) {
	if parseLogsSourceFields("  ") != nil {
		t.Error("boş → nil (tam _source)")
	}
	got := parseLogsSourceFields("CHANNEL_CODE, http.status ,CHANNEL_CODE,,")
	if strings.Join(got, "|") != "CHANNEL_CODE|http.status" {
		t.Errorf("parse: %v", got)
	}
	var many []string
	for i := 0; i < 80; i++ {
		many = append(many, "f"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if n := len(parseLogsSourceFields(strings.Join(many, ","))); n != logstore.MaxSourceFields {
		t.Errorf("tavan %d; %d beklendi", n, logstore.MaxSourceFields)
	}
	base := logstore.Filter{Service: "orders-api"}
	if logsSourceKey(base) != "" {
		t.Error("parametresiz istek eski anahtarı korumalı")
	}
	a := base
	a.SourceFields = []string{"b", "a"}
	b := base
	b.SourceFields = []string{"a", "b"}
	c := base
	c.SourceFields = []string{"a", "c"}
	if logsSearchKey(a, "1", "2") != logsSearchKey(b, "1", "2") {
		t.Error("kolon KÜMESİ sırası anahtarı değiştirmemeli")
	}
	if logsSearchKey(a, "1", "2") == logsSearchKey(c, "1", "2") {
		t.Error("farklı kolon kümesi farklı anahtar (v0.5.187 çapraz zehirlenme)")
	}
	d := a
	d.SkipTotal = true
	if logsSearchKey(a, "1", "2") == logsSearchKey(d, "1", "2") {
		t.Error("skipTotal anahtara girmeli")
	}
}

// docFetchStore — DocFetcher'lı sahte ES.
type docFetchStore struct {
	logstore.Store
	refs []string
}

func (d *docFetchStore) FetchDoc(_ context.Context, ref string, _ time.Time) (*logstore.LogRecord, error) {
	d.refs = append(d.refs, ref)
	if ref == "app-x/missing" {
		return nil, logstore.ErrDocNotFound
	}
	return &logstore.LogRecord{ID: 7, Body: "full", Attributes: map[string]string{"CHANNEL_CODE": "MOBILE"}, DocRef: ref}, nil
}
func (*docFetchStore) Backend() string { return "elasticsearch" }

func TestGetLogsDoc(t *testing.T) {
	st := &docFetchStore{}
	s := &Server{logs: st, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	call := func(ref string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.getLogsDoc(w, httptest.NewRequest("GET", "/api/logs/doc?ref="+ref, nil))
		return w
	}
	if w := call("app-orders-2026.11.05/abc"); w.Code != 200 || !strings.Contains(w.Body.String(), `"CHANNEL_CODE":"MOBILE"`) {
		t.Fatalf("doc: %d %s", w.Code, w.Body.String())
	}
	call("app-orders-2026.11.05/abc")
	if len(st.refs) != 1 {
		t.Errorf("ikinci açılış önbellekten (%d çağrı)", len(st.refs))
	}
	if w := call("app-x/missing"); w.Code != http.StatusNotFound {
		t.Errorf("yok → 404, %d", w.Code)
	}
	if w := call("nofslash"); w.Code != http.StatusBadRequest {
		t.Errorf("geçersiz ref → 400, %d", w.Code)
	}
	ch := &Server{logs: chBackendStub{}, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	w := httptest.NewRecorder()
	ch.getLogsDoc(w, httptest.NewRequest("GET", "/api/logs/doc?ref=a/b", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("CH arka ucu → 400, %d", w.Code)
	}
}
