package api

// anomaly_verdicts_test.go — v0.10.181/184: parmak izi kanonik ya da boş; rune
// kesimi; rota kaydı api.go dışında (tek satır çağrı); GET ucu YOK.

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVerdictFingerprintAndTrunc(t *testing.T) {
	if verdictFingerprint("trace_op", "", "svc") != "" || verdictFingerprint("trace_op", "p", "") != "" {
		t.Fatal("eksik desen/servis → ham id yerine BOŞ olmalı")
	}
	if fp := verdictFingerprint("trace_op", "p", "svc"); len(fp) != 16 {
		t.Fatalf("kanonik parmak izi 16 hex olmalı: %q", fp)
	}
	s := strings.Repeat("ğ", 600)
	if out := truncRunes(s, 500); !utf8.ValidString(out) || utf8.RuneCountInString(out) != 500 {
		t.Fatalf("rune kesimi bozuk: %d geçerli=%v", utf8.RuneCountInString(out), utf8.ValidString(out))
	}
}

func TestAnomalyVerdictRoutesOutsideAPIGo(t *testing.T) {
	apiGo, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apiGo), "s.registerAnomalyVerdictRoutes(mux)") {
		t.Fatal("registerAnomalyVerdictRoutes api.go'dan çağrılmıyor — rotalar 200 + boş ekran olur")
	}
	if strings.Contains(string(apiGo), "/api/anomalies/{id}/verdict") {
		t.Fatal("verdict rotası api.go'ya yazılmış (api.go büyümeyecek kuralı)")
	}
	// v0.10.1032 — pin BİLİNÇLİ taşındı, korunan değişmez aynı: events ucu
	// kararı ekler. Zenginleştirme zinciri artık tek yerde
	// (anomaly_event_get.go enrichAnomalyEvents) ve events ucu + tekil uç
	// ikisi de onu çağırır (anomaly_event_get_test.go).
	if !strings.Contains(funcBody(string(apiGo), "getAnomalyEvents"), "enrichAnomalyEvents(ctx, s.store, ") {
		t.Fatal("events ucu ortak zenginleştirme zincirini çağırmıyor — karar eklenmez")
	}
	chain, _ := os.ReadFile("anomaly_event_get.go")
	if !strings.Contains(funcBody(string(chain), "enrichAnomalyEvents"), "EnrichAnomaliesWithVerdicts") {
		t.Fatal("events ucu kararı eklemiyor (zincirde EnrichAnomaliesWithVerdicts yok)")
	}
	own, _ := os.ReadFile("anomaly_verdicts.go")
	if strings.Contains(string(own), "GET /api/anomalies/verdicts") {
		t.Fatal("çağıransız GET ucu geri gelmiş (v0.10.184 #2)")
	}
}
