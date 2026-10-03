package api

// anomaly_sensitivity_oplatency_test.go — v0.10.1085 kaynak pini: tek seferlik
// operasyon gecikmesi göçü (1056 dönemi kayıtlı false → nil = açık) ayar
// yükleyicisinde, OKUMADAN ÖNCE koşar — boot ve 30 sn'lik tazeleme aynı
// gövdeden geçer, aynı tur göç edilmiş değeri yayınlar (problem_priority
// inbox göçü emsali).

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestLoadAnomalySensitivityRunsOpLatencyMigrationFirst(t *testing.T) {
	b, err := os.ReadFile("anomaly_sensitivity.go")
	if err != nil {
		t.Fatal(err)
	}
	src := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(string(b), "")
	i := strings.Index(src, "func (s *Server) LoadAnomalySensitivity(")
	if i < 0 {
		t.Fatal("LoadAnomalySensitivity bulunamadı")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	mig := strings.Index(body, "chstore.MigrateOpLatencyDefaultOnce(ctx, s.store)")
	load := strings.Index(body, "s.store.LoadAnomalySensitivity(ctx)")
	if mig < 0 || load < 0 || mig > load {
		t.Fatalf("göç okumadan önce değil (göç %d, okuma %d)", mig, load)
	}
	// Tazeleyici aynı gövdeyi çağırır (göç her pod'da, başarana dek turda bir).
	if !strings.Contains(src, "s.LoadAnomalySensitivity(ctx)") {
		t.Fatal("tazeleyici LoadAnomalySensitivity'yi çağırmıyor")
	}
}
