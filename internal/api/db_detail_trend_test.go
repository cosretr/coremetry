package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cache"
)

// v0.10.1095 — /database detay trend ucu (db_summary_1m ≤ 3 sa, aksi 5m).

func TestDBDetailTrendKeyDiffersByGranularity(t *testing.T) {
	base := dbDetailTrendKey("oracle", "core", "APP", 60, "b1")
	for name, other := range map[string]string{
		"grenlik":  dbDetailTrendKey("oracle", "core", "APP", 300, "b1"),
		"system":   dbDetailTrendKey("postgresql", "core", "APP", 60, "b1"),
		"instance": dbDetailTrendKey("oracle", "core2", "APP", 60, "b1"),
		"dbName":   dbDetailTrendKey("oracle", "core", "APP2", 60, "b1"),
		"boş db":   dbDetailTrendKey("oracle", "core", "", 60, "b1"),
		"pencere":  dbDetailTrendKey("oracle", "core", "APP", 60, "b2"),
		// Alan sınırı: "a"+"bc" ile "ab"+"c" aynı anahtara düşmemeli (v0.5.187).
		"sınır": dbDetailTrendKey("oracle", "coreA", "PP", 60, "b1"),
	} {
		if other == base {
			t.Errorf("%s anahtarı değiştirmiyor: %s", name, base)
		}
	}
	if !strings.HasPrefix(base, "db-detail-trend:v1:") || !strings.Contains(base, ":g=60:") {
		t.Errorf("önek / grenlik: %s", base)
	}
	// Çekmece ucunun anahtar alanıyla çakışmamalı (ayrı yük şekli).
	if strings.HasPrefix(base, "db-detail:") {
		t.Errorf("detay ucunun önekiyle çakışıyor: %s", base)
	}
}

func TestDatabaseDetailTrendRoute(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	mux := http.NewServeMux()
	s.registerDatabaseDetailTrendRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/databases/detail/trend?instance=x", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("system yokken %d, 400 beklenir", w.Code)
	}
	src, err := os.ReadFile("db_detail_trend.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`registerRoutesExtra("database-detail-trend"`,
		`s.serveCached(w, r, key, 30*time.Second`,
		// Grenlik anahtara serveCached'ten ÖNCE girer.
		`dbDetailTrendKey(system, instance, dbName, g.BucketSec, cacheBucket(from, to))`,
		`s.store.GetDBDetailTrend(ctx, g, `,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("db_detail_trend.go %q taşımalı", want)
		}
	}
}
