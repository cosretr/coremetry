package api

// promql_console_routes.go — v0.10.953 (Explore → PromQL konsolu, Thanos;
// spec C5/C6 — docs/promql-console/audit.md §4, §5, §6, §9; operatör onaylı
// kararlar 1, 2, 8, 9, 10, 11, 12 — 2026-09-26).
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): yüzeyin bütün rotaları burada,
// kayıt init()'te route_registry.go defterine ("promql" adıyla; defter
// sayım testi çağrı dizgesini yorumda bile sayar, burada yazılmıyor).
// Handler'lar
// promql_console_handlers.go (query / query_range / metadata) ve
// promql_console_history.go (geçmiş) dosyalarında; ayar handler'ları C3'ün
// promql_console_settings.go dosyasında — rota satırları YALNIZ burada
// (make audit CHECK 7 aynı "METHOD /yol" dizgesini iki dosyada görürse
// yorum bile olsa "duplicate route" basar).
//
//   GET|POST /api/promql/query              query, time, cluster
//   GET|POST /api/promql/query_range        query, start, end, step, cluster, points
//   GET      /api/promql/labels             match[], start, end, limit, cluster
//   GET      /api/promql/label/{name}/values
//   GET      /api/promql/series
//   GET|DEL  /api/promql/history            kişisel, sunucu tarafı (C6)
//   GET      /api/settings/promql-console   her rol (sınırları GÖSTERİR)
//   PUT      /api/settings/promql-console   admin + audit
//
// ── NEDEN editor+ (karar 1) ──────────────────────────────────────────────
//
// Konsol paylaşımlı bir üretim metrik deposuna SINIRSIZ PromQL gönderir ve
// küratörlü /clusters görünümlerinin dışındaki her metriği açar. Custom
// role'ler yalnız tarayıcıda uygulanır, sunucuda bir API'yi daraltamaz —
// viewer'a açık bir rota fiilen herkese açık olurdu. Kapı KAYIT satırında
// (api-route SKILL §5): güvenlik incelemesi tek dosyaya iner. Geçmiş ve
// metadata uçları da aynı kapının arkasında: yalnız konsolun kendisi
// kullanır, viewer'a "boş sayfa" riski yok (sayfa kilitli <Empty> çizer).
// Ayar GET'i BİLEREK kapısız: viewer kilidin yanında sınırları görür, blob
// gizli bilgi taşımaz (token'lar thanos_clusters blobunda).
//
// ── NEDEN query_range ALT ÇİZGİLİ ────────────────────────────────────────
//
// Repo'nun ilk alt çizgili statik segmenti (api-route SKILL: kebab-case).
// Bilinçli istisna: parametre adları ve zarf Prometheus HTTP API'siyle
// birebir (karar 9-11) ve yol da öyle — /api/v1/query_range'i bilen bir
// operatör ya da araç yolu tahmin edebilsin, CodeMirror PromQL istemcisinin
// eşlemesi tek satır kalsın. "query-range" yazmak, Prometheus'un en bilinen
// iki ucundan birini yanlış hecelemek olurdu.
//
// ── NEDEN serveCached YOK (query / query_range, karar 12) ────────────────
//
// Önbellek kullanıcılar arası paylaşımlı: bir isabet kullanıcı başına
// limiti ve audit satırını atlardı; bayat isabet sorguyu kullanıcı
// bağlamı OLMADAN arka planda yeniden koşardı (cache.go refreshKey) ve her
// sonuç (32 MiB'a kadar) Redis'e yazılırdı. Metadata uçları (etiket, değer,
// seri) ise 60 s önbellekli: otomatik tamamlama patlamalı gelir, sonuç
// kullanıcıdan bağımsızdır ve anahtar TÜM girdileri (enjekte etiket adı ve
// değeri dahil) taşır — promqlMetaCacheKey.
//
// ── NEDEN GET VE POST ────────────────────────────────────────────────────
//
// Prometheus uyumu: sorgu 8 KiB'a kadar (maxPromQLQueryLen) ve URL'de
// taşınması proxy sınırlarına takılır; konsol POST form
// (application/x-www-form-urlencoded) gönderir, GET paylaşılabilir
// bağlantılar ve curl içindir. İkisi AYNI handler'a gider.

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("promql", (*Server).registerPromQLRoutes) }

func (s *Server) registerPromQLRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/promql/query", auth.RequireAnyRole(editorRoles, s.promqlQuery))
	mux.HandleFunc("POST /api/promql/query", auth.RequireAnyRole(editorRoles, s.promqlQuery))
	mux.HandleFunc("GET /api/promql/query_range", auth.RequireAnyRole(editorRoles, s.promqlQueryRange))
	mux.HandleFunc("POST /api/promql/query_range", auth.RequireAnyRole(editorRoles, s.promqlQueryRange))
	mux.HandleFunc("GET /api/promql/labels", auth.RequireAnyRole(editorRoles, s.promqlLabels))
	mux.HandleFunc("GET /api/promql/label/{name}/values", auth.RequireAnyRole(editorRoles, s.promqlLabelValues))
	mux.HandleFunc("GET /api/promql/series", auth.RequireAnyRole(editorRoles, s.promqlSeries))
	mux.HandleFunc("GET /api/promql/history", auth.RequireAnyRole(editorRoles, s.getPromQLHistory))
	mux.HandleFunc("DELETE /api/promql/history", auth.RequireAnyRole(editorRoles, s.deletePromQLHistory))

	mux.HandleFunc("GET /api/settings/promql-console", s.getPromQLConsoleSettings)
	mux.Handle("PUT /api/settings/promql-console",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putPromQLConsoleSettings)))
}
