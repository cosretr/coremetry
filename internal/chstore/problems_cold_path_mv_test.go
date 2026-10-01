package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.1018 — operatör: "Problems sayfası biraz yavaş, acaba indeks yok mu
// tablolarda." State tabloları (problems, exception_groups, …) küçük ve
// anahtarlı; yavaşlık sayfanın soğuk yolundaki İKİ ham `spans` toplamasındaydı
// (deploy eşlemesi: açık problemlerin en eskisine dek, ≤32 gün; servis→cluster
// haritası: tüm filo, son 1 saat). İkisi de artık ön-toplamdan okur, MV
// pencereyi kapsamıyorsa ham yola düşer. Bu testler o sözleşmeyi pinler.

func TestDeploysByServiceSQLShapes(t *testing.T) {
	mv := deploysByServiceMVSQL(3)
	for _, w := range []string{
		"FROM service_version_5m",
		"service_name IN (?,?,?)",
		"time_bucket >= ? AND time_bucket <= ?", // zaman sınırlı
		"minMerge(first_seen_state)",            // kova etiketi değil, gerçek ilk görülme
		"HAVING version != ''",
		"LIMIT 50000",
		"max_execution_time = 10",
	} {
		if !strings.Contains(mv, w) {
			t.Errorf("MV sorgusu %q taşımalı:\n%s", w, mv)
		}
	}
	if strings.Contains(mv, "FROM spans") {
		t.Error("MV sorgusu ham spans okumamalı")
	}
	raw := deploysByServiceRawSQL(2)
	for _, w := range []string{
		"FROM spans",
		"service_name IN (?,?)",
		"time >= ? AND time <= ?",
		effectiveVersionExpr, // v0.9.66 — merkez sürüm zinciri
		"LIMIT 50000",
		"max_execution_time = 10",
	} {
		if !strings.Contains(raw, w) {
			t.Errorf("ham sorgu %q taşımalı", w)
		}
	}
	// İki yol AYNI kolon sırasını döndürmeli (ortak yürütücü konumsal okur).
	for name, q := range map[string]string{"mv": mv, "raw": raw} {
		a, b, c := strings.Index(q, "service_name,"), strings.Index(q, "version"), strings.Index(q, "first_seen_ns")
		if a < 0 || b < a || c < b {
			t.Errorf("%s: kolon sırası service_name, version, first_seen_ns olmalı", name)
		}
	}
}

func TestServiceClusterMapMVSQLShape(t *testing.T) {
	for _, w := range []string{
		"FROM service_env_summary_5m",
		"time_bucket >= ?",
		"GROUP BY service_name, cluster",
		"HAVING cluster != ''",
		"LIMIT 50000",
		"max_execution_time = 8",
	} {
		if !strings.Contains(serviceClusterMapMVSQL, w) {
			t.Errorf("servis→cluster MV sorgusu %q taşımalı", w)
		}
	}
}

// Kablolama: iki okuyucu da MV'yi KAPSAMA ölçümünden sonra dener ve ham yolu
// yedek olarak korur; anomali ikizi kendi ham kopyasını taşımaz.
func TestProblemsColdPathIsMVFirst(t *testing.T) {
	pt, err := os.ReadFile("problem_telemetry.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pt)
	for _, w := range []string{
		"if s.deployMVCovers(ctx, from) {",
		"deploysByServiceMVSQL(len(services))+s.shardSkipSetting()",
		"alignBucketStart(from), to)",
		"deploysByServiceRawSQL(len(services)), services, from, to)",         // yedek yol
		"byService, err := s.fetchDeploysByService(ctx, services, from, to)", // anomali ikizi ortak okuyucuda
	} {
		if !strings.Contains(src, w) {
			t.Errorf("problem_telemetry.go %q taşımalı", w)
		}
	}
	// Deploy eşlemesinin ham spans sorgusu TEK kopya (deploysByServiceRawSQL).
	if c := strings.Count(src, "has(res_keys, 'helm.chart.version')"); c != 1 {
		t.Errorf("ham deploy sorgusu tek kopya olmalı, %d bulundu", c)
	}
	rp, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	repo := string(rp)
	gate := strings.Index(repo, "if s.EnvSummaryCovers(ctx, from) {\n\t\tgot, err := s.queryServiceClusterMap(ctx, serviceClusterMapMVSQL, alignBucketStart(from))")
	rawAt := strings.Index(repo, "SELECT service_name, `+s.clusterExpr()+` AS cluster")
	if gate < 0 || rawAt < 0 || rawAt < gate {
		t.Errorf("GetServiceClusterMap önce MV'yi denemeli, ham yol yedek kalmalı (gate=%d raw=%d)", gate, rawAt)
	}
}
