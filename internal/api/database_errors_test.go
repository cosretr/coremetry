package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cache"
)

// v0.10.1020 — veritabanı hata kırılımı ucu.

func TestDBErrorsKey(t *testing.T) {
	base := dbErrorsKey("oracle", "core", "APP", "b1")
	for name, other := range map[string]string{
		"system":   dbErrorsKey("postgresql", "core", "APP", "b1"),
		"instance": dbErrorsKey("oracle", "core2", "APP", "b1"),
		"dbName":   dbErrorsKey("oracle", "core", "APP2", "b1"),
		"pencere":  dbErrorsKey("oracle", "core", "APP", "b2"),
		// Alan sınırı: "a"+"bc" ile "ab"+"c" aynı anahtara düşmemeli (v0.5.187 sınıfı).
		"sınır": dbErrorsKey("oracle", "coreA", "PP", "b1"),
	} {
		if other == base {
			t.Errorf("%s anahtarı değiştirmiyor: %s", name, base)
		}
	}
	if !strings.HasPrefix(base, "db-errors:v1:") {
		t.Errorf("önek: %s", base)
	}
}

func TestDatabaseErrorsRoute(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	mux := http.NewServeMux()
	s.registerDatabaseErrorRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/databases/errors?instance=x", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("system yokken %d, 400 beklenir", w.Code)
	}
	src, err := os.ReadFile("database_errors.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`registerRoutesExtra("database-errors"`,
		`s.serveCached(w, r, key, 30*time.Second`,
		`dbErrorsKey(system, instance, dbName, cacheBucket(from, to))`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("database_errors.go %q taşımalı", want)
		}
	}
}
