package api

// database_errors.go — v0.10.1020 — bir veritabanına giden başarısız çağrıların
// hata türüne göre kırılımı (Databases × Dynatrace, dilim 2). Kavram ve sorgular:
// internal/chstore/db_errors.go.
//
//	GET /api/databases/errors?system=&instance=&dbName=&from=&to=
//
// /api/databases/detail ile AYNI kimlik üçlüsü ve pencere; ayrı uç çünkü ham
// spans okuması detay yükünü geciktirmemeli (sayfa ikisini paralel çeker).
// Rol kapısı yok (detay ucuyla aynı); 30 sn önbellek, anahtar kimlik + pencere
// kovasını FNV ile taşır (dbDetailKey ile aynı alan ayırıcı disiplini).

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"time"
)

func init() { registerRoutesExtra("database-errors", (*Server).registerDatabaseErrorRoutes) }

func (s *Server) registerDatabaseErrorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/databases/errors", s.getDatabaseErrors)
}

// dbErrorsKey — SAF: önbellek anahtarı (tüm girdiler; alan sınırları NUL ile).
func dbErrorsKey(system, instance, dbName, bucket string) string {
	h := fnv.New64a()
	h.Write([]byte(system))
	h.Write([]byte{0})
	h.Write([]byte(instance))
	h.Write([]byte{0})
	h.Write([]byte(dbName))
	return fmt.Sprintf("db-errors:v1:%x:%s", h.Sum64(), bucket)
}

func (s *Server) getDatabaseErrors(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	system, instance, dbName := q.Get("system"), q.Get("instance"), q.Get("dbName")
	if system == "" {
		writeJSONError(w, http.StatusBadRequest, "system required")
		return
	}
	from, to := parseFromTo(r, time.Hour)
	key := dbErrorsKey(system, instance, dbName, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetDatabaseErrors(ctx, system, instance, dbName, from, to)
	})
}
