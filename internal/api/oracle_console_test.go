package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

// v0.10.742 — konsol ucu: rol kapısı, servis yok 503, güvensiz SQL 400 (ağa
// çıkmadan), bilinmeyen kaynak 404, şifre/DSN cevapta yok.
func oracleConsoleServer(t *testing.T, withService bool) (*Server, *http.ServeMux) {
	t.Helper()
	c, _ := cache.NewNoop()
	s := &Server{cache: c, auditQ: make(chan chstore.AuditEntry, 8)}
	if withService {
		s.oracle = oracle.New()
		s.oracle.Configure(oracle.Settings{Sources: []oracle.SourceConfig{{
			ID: "o-1", Name: "core", Host: "db.example.local", ServiceName: "ORCLPDB",
			User: "coremetry", Password: "sup3rsecret", Schema: "S", Table: "T",
		}}})
	}
	mux := http.NewServeMux()
	s.registerOracleConsoleRoutes(mux)
	return s, mux
}

func postOracleSQL(mux *http.ServeMux, role, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/sql/oracle", strings.NewReader(body))
	if role != "" {
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Email: "a@example.test", Role: role}))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestOracleConsole_RoleGate(t *testing.T) {
	_, mux := oracleConsoleServer(t, true)
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if rec := postOracleSQL(mux, role, `{"sourceId":"o-1","query":"SELECT 1 FROM dual"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, 403 bekleniyor", role, rec.Code)
		}
	}
}

func TestOracleConsole_NoService503(t *testing.T) {
	_, mux := oracleConsoleServer(t, false)
	if rec := postOracleSQL(mux, auth.RoleAdmin, `{"sourceId":"o-1","query":"SELECT 1 FROM dual"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("servis yok: %d", rec.Code)
	}
}

func TestOracleConsole_UnsafeSQL400AndUnknownSource404(t *testing.T) {
	_, mux := oracleConsoleServer(t, true)
	if rec := postOracleSQL(mux, auth.RoleAdmin, `{"sourceId":"o-1","query":"DELETE FROM t"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("DML: %d, 400 bekleniyor", rec.Code)
	}
	if rec := postOracleSQL(mux, auth.RoleAdmin, `{"sourceId":"o-1","query":"EXPLAIN PLAN FOR SELECT 1 FROM dual"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("EXPLAIN PLAN (plan_table'a yazar): %d, 400 bekleniyor", rec.Code)
	}
	if rec := postOracleSQL(mux, auth.RoleAdmin, `{"sourceId":"o-yok","query":"SELECT 1 FROM dual"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("bilinmeyen kaynak: %d, 404 bekleniyor", rec.Code)
	}
	if rec := postOracleSQL(mux, auth.RoleAdmin, `{"query":"SELECT 1 FROM dual"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("sourceId yok: %d, 400 bekleniyor", rec.Code)
	}
}

// Bağlantı kurulamaz (test ortamında Oracle yok): cevap 200 + error, ve
// error metninde şifre/DSN YOK.
func TestOracleConsole_ConnectFailureIs200AndRedacted(t *testing.T) {
	_, mux := oracleConsoleServer(t, true)
	rec := postOracleSQL(mux, auth.RoleAdmin, `{"sourceId":"o-1","query":"SELECT 1 FROM dual"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("bağlantı hatası 200 olmalı: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("error alanı bekleniyor: %s", body)
	}
	if strings.Contains(body, "sup3rsecret") || strings.Contains(body, "oracle://") {
		t.Fatalf("şifre/DSN cevaba sızdı: %s", body)
	}
}
