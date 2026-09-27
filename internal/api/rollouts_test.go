package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// rollouts_test.go — v0.10.200 sözleşmesi (rollouts.go + rollout_keys.go).
//
// (1) Kayıt pini: registerRolloutRoutes api.go'da ÇAĞRILIR, rota dizeleri
//     api.go'da DEĞİLDİR (kayıtsız rota 404 değil boş sayfa — SPA catch-all;
//     otomatik kapısı yok). anomaly_verdicts_test.go emsali.
// (2) Anahtar invaryantları: her girdi anahtarı ayırır (cache_key_test.go),
//     ayraç saldırısı aynı ön-görüntüyü üretemez, aynı 30 s kova tek girdi.

func TestRolloutRoutesRegisteredOnceInApiGo(t *testing.T) {
	b, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Count(src, "s.registerRolloutRoutes(mux)") != 1 {
		t.Fatal("registerRolloutRoutes(mux) api.go'da tam bir kez çağrılmalı")
	}
	for _, route := range []string{`"GET /api/rollouts"`, `"GET /api/rollout"`, `"GET /api/rollout/detail"`, `"GET /api/rollouts/stats"`, `"GET /api/rollouts/runs"`, `"GET /api/settings/rollouts"`, `"PUT /api/settings/rollouts"`} {
		if strings.Contains(src, route) {
			t.Fatalf("rota api.go'ya sızmış (kendi dosyasında kalmalı): %s", route)
		}
	}
}

// TestRolloutRoutesResolveOnMux — kayıt pini metin değil GERÇEK mux'la:
// HandleFunc satırı silinirse SPA catch-all 200+boş sayfa döner ve metin
// pini bunu göremezdi (inceleme #13).
func TestRolloutRoutesResolveOnMux(t *testing.T) {
	mux := http.NewServeMux()
	(&Server{}).registerRolloutRoutes(mux)
	for _, p := range []struct{ m, path string }{
		{"GET", "/api/rollouts"}, {"GET", "/api/rollout"}, {"GET", "/api/rollout/detail"}, {"GET", "/api/rollouts/stats"},
		{"GET", "/api/rollouts/runs"}, {"GET", "/api/settings/rollouts"}, {"PUT", "/api/settings/rollouts"},
	} {
		if _, pat := mux.Handler(httptest.NewRequest(p.m, p.path, nil)); pat == "" {
			t.Fatalf("%s %s mux'ta çözülmüyor", p.m, p.path)
		}
	}
}

func TestRolloutKeysCarryEveryInput(t *testing.T) {
	from := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	base := chstore.RolloutFilter{ClusterID: "c-1", Namespace: "pay", Workload: "api", Status: "completed", Kind: "Deployment"}
	k0 := rolloutsListKey("v1", base, 100, from, to)
	variants := []chstore.RolloutFilter{
		{ClusterID: "c-2", Namespace: "pay", Workload: "api", Status: "completed", Kind: "Deployment"},
		{ClusterID: "c-1", Namespace: "core", Workload: "api", Status: "completed", Kind: "Deployment"},
		{ClusterID: "c-1", Namespace: "pay", Workload: "web", Status: "completed", Kind: "Deployment"},
		{ClusterID: "c-1", Namespace: "pay", Workload: "api", Status: "rolled_back", Kind: "Deployment"},
		{ClusterID: "c-1", Namespace: "pay", Workload: "api", Status: "completed", Kind: "StatefulSet"},
	}
	for _, v := range variants {
		if rolloutsListKey("v1", v, 100, from, to) == k0 {
			t.Fatalf("girdi anahtarı ayırmadı: %+v", v)
		}
	}
	if rolloutsListKey("v1", base, 50, from, to) == k0 {
		t.Fatal("limit anahtarda olmalı")
	}
	if rolloutsListKey("v1", base, 100, from.Add(time.Hour), to.Add(time.Hour)) == k0 {
		t.Fatal("pencere anahtarda olmalı")
	}
	// aynı 30 s kova → tek girdi (FE her tick'te yeniden hesaplar)
	if rolloutsListKey("v1", base, 100, from.Add(7*time.Second), to.Add(7*time.Second)) != k0 {
		t.Fatal("aynı 30 s kovası tek girdi olmalı")
	}
	// ayraç saldırısı: parçalar ayrı ayrı özetlenir
	a := chstore.RolloutFilter{ClusterID: "c\x00pay", Namespace: ""}
	bf := chstore.RolloutFilter{ClusterID: "c", Namespace: "\x00pay"}
	if rolloutsListKey("v1", a, 100, from, to) == rolloutsListKey("v1", bf, 100, from, to) {
		t.Fatal("NUL kaydırması anahtar çakıştırmamalı")
	}
	// kararlılık
	if rolloutsListKey("v1", base, 100, from, to) != k0 {
		t.Fatal("anahtar kararlı olmalı")
	}
	id := chstore.RolloutID{ClusterID: "c-1", Namespace: "pay", Workload: "api", Revision: "api-abc", StartedAt: from}
	if rolloutKey("v1", id) == rolloutKey("v1", chstore.RolloutID{ClusterID: "c-1", Namespace: "pay", Workload: "api", Revision: "api-abc", StartedAt: from.Add(time.Minute)}) {
		t.Fatal("startedAt anahtarda olmalı")
	}
	if rolloutStatsKey("v1", "c-1", "pay", 10, from, to) == rolloutStatsKey("v1", "c-1", "pay", 20, from, to) {
		t.Fatal("topN anahtarda olmalı")
	}
	// v0.10.984 — okuma kaynağı her anahtarda (iki tablo aynı girdiye farklı cevap verir).
	if rolloutsListKey("v2", base, 100, from, to) == k0 {
		t.Fatal("kaynak liste anahtarında olmalı")
	}
	if rolloutKey("v2", id) == rolloutKey("v1", id) {
		t.Fatal("kaynak tekil anahtarda olmalı")
	}
	if rolloutStatsKey("v2", "c-1", "pay", 10, from, to) == rolloutStatsKey("v1", "c-1", "pay", 10, from, to) {
		t.Fatal("kaynak istatistik anahtarında olmalı")
	}
	if rolloutDetailKey("v2", id, from) == rolloutDetailKey("v1", id, from) || rolloutRunsKey("v2") == rolloutRunsKey("v1") {
		t.Fatal("kaynak çekmece / koşu anahtarında olmalı")
	}
	v2id := chstore.RolloutV2ID{ClusterID: "c-1", Namespace: "pay", Kind: "Deployment", Workload: "api", IncarnationAt: from, Generation: 7}
	for _, v := range []chstore.RolloutV2ID{
		{ClusterID: "c-2", Namespace: "pay", Kind: "Deployment", Workload: "api", IncarnationAt: from, Generation: 7},
		{ClusterID: "c-1", Namespace: "pay", Kind: "StatefulSet", Workload: "api", IncarnationAt: from, Generation: 7},
		{ClusterID: "c-1", Namespace: "pay", Kind: "Deployment", Workload: "api", IncarnationAt: from.Add(time.Minute), Generation: 7},
		{ClusterID: "c-1", Namespace: "pay", Kind: "Deployment", Workload: "api", IncarnationAt: from, Generation: 8},
	} {
		if rolloutV2Key(v) == rolloutV2Key(v2id) || rolloutV2DetailKey(v, from) == rolloutV2DetailKey(v2id, from) {
			t.Fatalf("v2 anahtar parçası ayırmadı: %+v", v)
		}
	}
	if rolloutV2Key(v2id) == rolloutKey("v2", id) {
		t.Fatal("6 parçalı anahtar 5 parçalıyla çakışmamalı")
	}
}
