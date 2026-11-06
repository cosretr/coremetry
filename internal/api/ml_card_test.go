package api

// ml_card_test.go — v0.10.1843 (öğrenme döngüsü Faz 2 dilim 2.3): ML liste
// satırı `summary` taşır (sunucu, ml.ExplainSummary) ve önbellek anahtarları
// TÜM girdileri (özetin saat dilimi dahil) hash'ler — v0.5.187 sınıfı.

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMLEventsKeyHashesAllInputs(t *testing.T) {
	base := mlEventsKey(86400, "trace", "op_latency", "orders-api", "shadow", "Europe/Istanbul")
	variants := []string{
		mlEventsKey(3600, "trace", "op_latency", "orders-api", "shadow", "Europe/Istanbul"),
		mlEventsKey(86400, "log", "op_latency", "orders-api", "shadow", "Europe/Istanbul"),
		mlEventsKey(86400, "trace", "op_error_rate", "orders-api", "shadow", "Europe/Istanbul"),
		mlEventsKey(86400, "trace", "op_latency", "payments-mobile", "shadow", "Europe/Istanbul"),
		mlEventsKey(86400, "trace", "op_latency", "orders-api", "closed", "Europe/Istanbul"),
		mlEventsKey(86400, "trace", "op_latency", "orders-api", "shadow", "UTC"),
	}
	for i, v := range variants {
		if v == base {
			t.Errorf("girdi %d anahtarı değiştirmedi: %s", i, v)
		}
	}
	if mlReplayKey("a", "UTC") == mlReplayKey("b", "UTC") || mlReplayKey("a", "UTC") == mlReplayKey("a", "Europe/Istanbul") {
		t.Error("replay anahtarı girdilerden birini kaçırıyor")
	}
}

// Bağlanma pini (tested-but-unreachable dersi): iki uç saf çekirdeği
// GERÇEKTEN döndürür — liste "items": rows, detay "card": BuildExplainCard.
func TestMLHandlersReturnCardAndSummary(t *testing.T) {
	src, err := os.ReadFile("ml.go")
	if err != nil {
		t.Fatal(err)
	}
	body := func(fn string) string {
		s := string(src)
		i := strings.Index(s, "func (s *Server) "+fn+"(")
		if i < 0 {
			t.Fatalf("%s yok", fn)
		}
		j := strings.Index(s[i+1:], "\nfunc ")
		if j < 0 {
			return s[i:]
		}
		return s[i : i+1+j]
	}
	ev := body("getMLEvents")
	if !regexp.MustCompile(`rows := mlEventRows\(items,`).MatchString(ev) || !strings.Contains(ev, `"items": rows`) {
		t.Error("getMLEvents özetli satırları döndürmüyor")
	}
	if !regexp.MustCompile(`"card":\s*ml\.BuildExplainCard\(in\)`).MatchString(body("getMLReplay")) {
		t.Error("getMLReplay kartı döndürmüyor")
	}
}

func TestMLEventRowsCarrySummary(t *testing.T) {
	loc := time.FixedZone("TR", 3*3600)
	start := time.Date(2026, 11, 3, 14, 10, 0, 0, loc)
	ev := chstore.MLEvent{ID: "e1", Detector: "error_rate", Signal: chstore.MLSignalLog, Stage: chstore.MLStageShadow, Service: "branch-teller",
		Level: "error", StartedAt: start.UnixNano(), LastSeen: start.Add(10 * time.Minute).UnixNano(), WindowSec: 300, Observed: 207, Baseline: 23, Direction: "up"}
	rows := mlEventRows([]chstore.MLEvent{ev}, chstore.MLConfig{}, start.Add(15*time.Minute), loc)
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	_ = json.Unmarshal(b, &got)
	if got[0]["id"] != "e1" || got[0]["service"] != "branch-teller" {
		t.Errorf("olay alanları düzleşmedi: %s", b)
	}
	if s, _ := got[0]["summary"].(string); s != "branch-teller: hata logları normalin yaklaşık 9 katı, 15 dakikadır (14:10'dan beri)." {
		t.Errorf("summary %q", s)
	}
	if rows := mlEventRows(nil, chstore.MLConfig{}, start, loc); rows == nil {
		t.Error("boş liste nil (JSON null) döndü")
	}
	if p := mlEventPath("a b"); !strings.HasPrefix(p, "/anomalies/ml/") || strings.Contains(p, " ") {
		t.Errorf("path %q", p)
	}
}
