package api

// oracle_console.go — v0.10.742 (operatör 2026-09-16: "Oracle için de SQL
// console"). Admin SQL konsolunun Oracle ayağı:
//
//   POST /api/admin/sql/oracle   {sourceId, query}  → SQLResult şekli
//
// Üç katman oracle/console.go başlığında; buradaki iş rol kapısı, gövde,
// audit ve ClickHouse playground ile AYNI cevap sözleşmesi: sorgu hatası
// 200 + {"error"} (operatörün sorusuna verilmiş cevaptır, HTTP hatası
// değil). Şifre/DSN cevaba girmez (oracle.Query redaksiyonlu döner; audit
// details'a yalnız sourceId, satır sayısı, süre, sorgu metni girer).
// api.go BÜYÜMEZ: route_registry defteri.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func init() { registerRoutesExtra("oracle-console", (*Server).registerOracleConsoleRoutes) }

func (s *Server) registerOracleConsoleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/sql/oracle", auth.RequireRole(auth.RoleAdmin, s.execOracleSQL))
}

type oracleConsoleInput struct {
	SourceID string `json:"sourceId"`
	Query    string `json:"query"`
}

func (s *Server) execOracleSQL(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "oracle servisi bu podda yok")
		return
	}
	var in oracleConsoleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	in.SourceID = strings.TrimSpace(in.SourceID)
	in.Query = strings.TrimSpace(in.Query)
	if in.SourceID == "" {
		writeJSONError(w, http.StatusBadRequest, "sourceId zorunlu")
		return
	}
	// Allow-list İSTEKTE 400: bir DML denemesi "sorgu hatası" değil, reddedilmiş
	// bir istektir (CH playground sözleşmesi).
	if !oracle.IsSafeConsoleSQL(in.Query) {
		writeJSONError(w, http.StatusBadRequest, oracle.ErrConsoleUnsafeSQL.Error())
		return
	}
	res, err := s.oracle.Query(r.Context(), in.SourceID, in.Query, oracle.ConsoleMaxRows)
	if err != nil {
		if errors.Is(err, oracle.ErrConsoleNoSource) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		// Bağlantı/sorgu hatası: 200 + error (redaksiyonlu) — playground sözleşmesi.
		s.audit(r, "sql.query", "oracle_sql_console", in.SourceID,
			fmt.Sprintf(`{"error":true,"query":%q}`, in.Query))
		writeJSON(w, map[string]any{"columns": []string{}, "rows": [][]any{}, "rowCount": 0, "tookMs": 0, "error": err.Error()})
		return
	}
	s.audit(r, "sql.query", "oracle_sql_console", in.SourceID,
		fmt.Sprintf(`{"rows":%d,"tookMs":%d,"capped":%v,"query":%q}`, res.RowCount, res.TookMs, res.Capped, in.Query))
	writeJSON(w, res)
}
