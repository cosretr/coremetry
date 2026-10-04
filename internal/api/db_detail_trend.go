package api

// db_detail_trend.go — v0.10.1095 — /database detayının üç grafiği
// (Calls/s · Error % · P99) için tek veritabanı trend ucu. Kavram, seçici
// ve SQL: internal/chstore/db_detail_trend.go.
//
//	GET /api/databases/detail/trend?system=&instance=&dbName=&from=&to=
//
// Operatör kararı ("Önerin A yapalım", docs/DECISIONS.md 2026-10-04):
// ≤ 3 sa pencere db_summary_1m'den 1 dk kova, üstü db_summary_5m'den 5 dk.
// NEDEN /api/databases/trends'e parametre DEĞİL: o uç filo geneli ve
// /databases tablosunun 5 dk rozet kuralını (son TAM kova) taşıyor;
// grenliği orada değiştirmek tablonun sağlık rozetini de değiştirirdi.
//
// Rol kapısı yok (detay ucuyla aynı, viewer görür). 30 sn önbellek;
// anahtar kimlik + pencere kovası + SEÇİLEN GRENLİK (v0.5.187 "tüm
// girdiler"): grenlik kapsama probundan gelir ve aynı pencere için 5m'den
// 1m'e dönebilir (1m tablo yeni doldu) — anahtarda olmasa 5m yükü 30 sn
// boyunca 1m slotunda servis edilirdi.

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"time"
)

func init() {
	registerRoutesExtra("database-detail-trend", (*Server).registerDatabaseDetailTrendRoutes)
}

func (s *Server) registerDatabaseDetailTrendRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/databases/detail/trend", s.getDatabaseDetailTrend)
}

// dbDetailTrendKey — SAF: önbellek anahtarı (kimlik alanları NUL ile
// ayrılı FNV, dbDetailKey disiplini; grenlik ve pencere kovası düz).
func dbDetailTrendKey(system, instance, dbName string, bucketSec int, bucket string) string {
	h := fnv.New64a()
	h.Write([]byte(system))
	h.Write([]byte{0})
	h.Write([]byte(instance))
	h.Write([]byte{0})
	h.Write([]byte(dbName))
	return fmt.Sprintf("db-detail-trend:v1:%x:g=%d:%s", h.Sum64(), bucketSec, bucket)
}

func (s *Server) getDatabaseDetailTrend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	system, instance, dbName := q.Get("system"), q.Get("instance"), q.Get("dbName")
	if system == "" {
		writeJSONError(w, http.StatusBadRequest, "system required")
		return
	}
	from, to := parseFromTo(r, time.Hour)
	// Grenlik anahtardan ÖNCE seçilir (prob 60 sn önbellekli, hata → 5m).
	g := s.store.DBDetailTrendGrain(r.Context(), from, to)
	key := dbDetailTrendKey(system, instance, dbName, g.BucketSec, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetDBDetailTrend(ctx, g, system, instance, dbName, from, to)
	})
}
