package api

// ml_card_test.go — v0.10.1843 (öğrenme döngüsü Faz 2 dilim 2.3): ML liste
// satırı `summary` taşır (sunucu, ml.ExplainSummary) ve önbellek anahtarları
// TÜM girdileri (özetin saat dilimi dahil) hash'ler — v0.5.187 sınıfı.

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
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
	// v0.10.1870 — "Olayın seyri" ek katmanı yanıtta (ml_course.go); şekil
	// değişti → anahtar v3.
	if !regexp.MustCompile(`"course":\s*s\.mlCourse\(ctx, in, src\.Config\)`).MatchString(body("getMLReplay")) {
		t.Error("getMLReplay course katmanını döndürmüyor")
	}
	// v0.10.1876 — detay kartının profili de batch adını çözer (liste ile aynı yardımcı).
	if !strings.Contains(body("getMLReplay"), "ml.ResolveProfile(src.Profile, service,") {
		t.Error("getMLReplay batch adlı revizyonsuz servisi çözmüyor")
	}
	if !strings.Contains(mlReplayKey("a", "UTC"), "ml:replay:v3:") {
		t.Error("replay yanıt şekli değişti, anahtar sürümü v3 olmalı")
	}
}

func TestMLEventRowsCarrySummary(t *testing.T) {
	loc := time.FixedZone("TR", 3*3600)
	start := time.Date(2026, 11, 3, 14, 10, 0, 0, loc)
	ev := chstore.MLEvent{ID: "e1", Detector: "error_rate", Signal: chstore.MLSignalLog, Stage: chstore.MLStageShadow, Service: "branch-teller",
		Level: "error", StartedAt: start.UnixNano(), LastSeen: start.Add(10 * time.Minute).UnixNano(), WindowSec: 300, Observed: 207, Baseline: 23, Direction: "up"}
	rows := mlEventRows([]chstore.MLEvent{ev}, chstore.MLConfig{}, start.Add(15*time.Minute), loc, mlListExtras{})
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
	if rows := mlEventRows(nil, chstore.MLConfig{}, start, loc, mlListExtras{}); rows == nil {
		t.Error("boş liste nil (JSON null) döndü")
	}
	if p := mlEventPath("a b"); !strings.HasPrefix(p, "/anomalies/ml/") || strings.Contains(p, " ") {
		t.Errorf("path %q", p)
	}
}

// v0.10.1874 — liste satırı "Seyir" (kayıttan, ≤ 24 nokta) + "Servis tipi"
// (ml_learned); ikisi toplu okumadan, okunamazsa boş (satır yine döner).
func TestMLEventRowsCarrySparkAndProfile(t *testing.T) {
	loc := time.FixedZone("TR", 3*3600)
	start := time.Date(2026, 11, 3, 14, 10, 0, 0, loc)
	ev := chstore.MLEvent{ID: "e1", Detector: "error_rate", Signal: chstore.MLSignalLog, Service: "branch-teller", Level: "error",
		StartedAt: start.UnixNano(), LastSeen: start.Add(10 * time.Minute).UnixNano(), WindowSec: 300, Observed: 207, Baseline: 23, Direction: "up"}
	series := `[{"t":` + strconv.FormatInt(start.Add(-5*time.Minute).Unix(), 10) + `,"v":20},{"t":` + strconv.FormatInt(start.Unix(), 10) + `,"v":200}]`
	ex := mlListExtras{
		Series:   map[string]string{"e1": series},
		Profiles: map[string]chstore.MLLearnedRow{"branch-teller": {Key: "branch-teller", Params: `{"profile":"low_traffic"}`}},
	}
	rows := mlEventRows([]chstore.MLEvent{ev}, chstore.MLConfig{}, start.Add(15*time.Minute), loc, ex)
	if rows[0].Spark == nil || len(rows[0].Spark.V) != 2 || rows[0].Spark.A != 1 {
		t.Errorf("seyir: %+v", rows[0].Spark)
	}
	if rows[0].Profile != "low_traffic" || rows[0].ProfileLabel != "az çağrılan" {
		t.Errorf("servis tipi: %q %q", rows[0].Profile, rows[0].ProfileLabel)
	}
	bare := mlEventRows([]chstore.MLEvent{ev}, chstore.MLConfig{}, start, loc, mlListExtras{})
	if bare[0].Spark != nil || bare[0].Profile != "unknown" || bare[0].ProfileLabel != "" {
		t.Errorf("toplu okuma yoksa boş: %+v", bare[0])
	}
	// v0.10.1876 — regresyon: revizyonu olmayan batch adlı servis "batch".
	bev := ev
	bev.Service = "inventory-core-batch"
	isBatch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	if b := mlEventRows([]chstore.MLEvent{bev}, chstore.MLConfig{}, start, loc, mlListExtras{IsBatch: isBatch}); b[0].Profile != "batch" || b[0].ProfileLabel != "batch" {
		t.Errorf("batch adı: %q %q", b[0].Profile, b[0].ProfileLabel)
	}
	if !strings.Contains(mlEventsKey(1, "", "", "", "", "UTC"), "ml:events:v7:") {
		t.Error("liste şekli değişti, anahtar v7 olmalı")
	}
}

// v0.10.2013 — anomali tekilleştirme D2: satır grup alanlarını taşır; grup
// okunamazsa boş. Liste okuması tavanda kesilse de liste satırı gruba girer.
func TestMLEventRowsCarryGroup(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, loc)
	a := chstore.MLEvent{ID: "a", Detector: "op_latency", Service: "checkout", StartedAt: start.UnixNano(), LastSeen: start.Add(30 * time.Minute).UnixNano()}
	b := chstore.MLEvent{ID: "b", Detector: "dependency_latency", Service: "checkout", StartedAt: start.Add(5 * time.Minute).UnixNano(), LastSeen: start.Add(20 * time.Minute).UnixNano()}
	// okuma yalnız b'yi döndü (tavan); liste satırı a yine gruba katılır
	idx := ml.GroupIndex(ml.GroupMLEvents(mergeGroupInput([]chstore.MLEvent{b}, []chstore.MLEvent{a, b}), 10*time.Minute))
	rows := mlEventRows([]chstore.MLEvent{a}, chstore.MLConfig{}, start, loc, mlListExtras{Groups: idx})
	if rows[0].GroupID == "" || rows[0].GroupSize != 2 || len(rows[0].GroupDetectors) != 2 {
		t.Errorf("grup alanları: %+v", rows[0])
	}
	// v0.10.2021 — liste satırı = grup: aralık ve sinyaller grubun tüm üyelerinden.
	if rows[0].GroupStart != a.StartedAt || rows[0].GroupEnd != a.LastSeen || len(rows[0].GroupSignals) != 1 || rows[0].GroupSignals[0] != "trace" {
		t.Errorf("grup aralığı/sinyal: %+v", rows[0])
	}
	if bare := mlEventRows([]chstore.MLEvent{a}, chstore.MLConfig{}, start, loc, mlListExtras{}); bare[0].GroupID != "" || bare[0].GroupSize != 0 {
		t.Errorf("grup yoksa boş: %+v", bare[0])
	}
}

// v0.10.2015 — anomali tekilleştirme D3: grubun en son gölge kararı tüm
// üye satırlarına taşınır (kanıttan; ek okuma yok); kararsız grup alanı boş.
func TestMLEventRowsCarryGroupShadow(t *testing.T) {
	start := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	a := chstore.MLEvent{ID: "a", Detector: "op_latency", Service: "checkout", StartedAt: start.UnixNano(), LastSeen: start.Add(30 * time.Minute).UnixNano(),
		Evidence: `{"groupShadow":{"group":"mlg-old","wouldOpen":false,"rule":"anomaly-auto:group:mlg-old","reason":"açmazdı: tek dedektör","at":100}}`}
	b := chstore.MLEvent{ID: "b", Detector: "dependency_latency", Service: "checkout", StartedAt: start.Add(5 * time.Minute).UnixNano(), LastSeen: start.Add(20 * time.Minute).UnixNano(),
		Evidence: `{"groupShadow":{"group":"mlg-x","wouldOpen":true,"rule":"anomaly-auto:group:mlg-x","priority":"P2","reason":"açardı","at":200}}`}
	c := chstore.MLEvent{ID: "c", Detector: "op_latency", Service: "payments", StartedAt: start.UnixNano(), LastSeen: start.Add(5 * time.Minute).UnixNano()}
	items := []chstore.MLEvent{a, b, c}
	idx := ml.GroupIndex(ml.GroupMLEvents(items, 10*time.Minute))
	rows := mlEventRows(items, chstore.MLConfig{}, start, time.UTC, mlListExtras{Groups: idx})
	for _, r := range rows[:2] {
		if r.GroupShadow == nil || !r.GroupShadow.WouldOpen || r.GroupShadow.Priority != "P2" {
			t.Errorf("%s: en son karar taşınmalı: %+v", r.ID, r.GroupShadow)
		}
	}
	if rows[2].GroupShadow != nil {
		t.Errorf("kararsız grup boş olmalı: %+v", rows[2].GroupShadow)
	}
}

// v0.10.1845 — detay eylem kutusu olay satırının etiketini viewer'a da gösterir.
func TestMLReplayVerdict(t *testing.T) {
	if mlReplayVerdict(nil) != nil || mlReplayVerdict(&chstore.MLEvent{}) != nil {
		t.Error("etiketsiz olay nil olmalı")
	}
	v := mlReplayVerdict(&chstore.MLEvent{Verdict: "anomaly", VerdictBy: "op@x", VerdictAt: 5, Note: "n"})
	if v["verdict"] != "anomaly" || v["by"] != "op@x" || v["note"] != "n" {
		t.Errorf("%v", v)
	}
}
