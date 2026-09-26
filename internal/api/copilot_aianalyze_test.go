package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.944 — TestAggRED eskiden kova yüzdeliklerinin span ağırlıklı
// ORTALAMASINI pinliyordu (p99 = (100*400 + 300*600)/400 = 550). Bu hiçbir
// popülasyonun p99'u değildir: 5 dakikalık bir patlamanın p99'u sakin
// kovalarla sulandırılıp AI prompt'una "p99" diye giriyordu. Artık pencere
// tek satırdır (chstore.ServiceWindowRED — tdigest durumlarının birleşimi) ve
// windowRED yüzdeliği OLDUĞU GİBİ taşır.
func TestWindowREDKeepsWholeWindowPercentiles(t *testing.T) {
	w := chstore.ServiceWindowRED{Spans: 400, Errors: 40, AvgMs: 95, P50Ms: 65, P95Ms: 290, P99Ms: 900}
	got := windowRED(w, 60) // 400 span / 60 s
	if got.Spans != 400 || got.ErrorCount != 40 || got.ErrorRate != 10 {
		t.Fatalf("sayımlar: %+v", got)
	}
	// Tüm-pencere p99 (900) korunur; eski ağırlıklı ortalama 550 verirdi.
	if got.P99Ms != 900 || got.P95Ms != 290 || got.P50Ms != 65 || got.AvgMs != 95 {
		t.Errorf("yüzdelikler dönüştürülmeden geçmeli: %+v", got)
	}
	if got.Rate < 6.66 || got.Rate > 6.67 {
		t.Errorf("rate=%.3f want ~6.667 (span/s)", got.Rate)
	}
}

func TestWindowREDEmpty(t *testing.T) {
	// Boş pencere: sayılar 0 ve "0 ms" bir ölçüm gibi taşınmaz.
	got := windowRED(chstore.ServiceWindowRED{P99Ms: 12}, 60)
	if got.Spans != 0 || got.ErrorRate != 0 || got.Rate != 0 || got.P99Ms != 0 {
		t.Errorf("empty window should be zero, got %+v", got)
	}
}

// Kaynak pini: kova yüzdeliği ortalaması AI yüzeylerine geri dönmesin.
// aggRED silindi; analyze-service ve window_compare ServiceWindowRED okur.
func TestNoBucketPercentileAveragingInAIPrompts(t *testing.T) {
	for file, needles := range map[string][]string{
		"copilot_aianalyze.go": {"func aggRED(", "wP99", "GetServiceSummary5m("},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range needles {
			if strings.Contains(string(b), n) {
				t.Errorf("%s: %q geri döndü — kova yüzdelikleri ortalanıyor ya da yaklaşık sayı 'tam' diye sunuluyor", file, n)
			}
		}
	}
	b, err := os.ReadFile("copilot_aianalyze.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "s.store.ServiceWindowRED(") != 2 {
		t.Error("buildServiceContext güncel + baseline pencereyi ServiceWindowRED ile okumalı")
	}
	// Davranış pini (kaynak metni değil, ÜRETİLEN kanıt): window_compare
	// kanıtı yaklaşık sayıyı "tam sayım" diye sunmaz, örneklemeyi söyler.
	ist := time.FixedZone("TRT", 3*3600)
	w1 := absWindow{From: time.Date(2026, 9, 25, 10, 0, 0, 0, ist), To: time.Date(2026, 9, 25, 11, 0, 0, 0, ist)}
	w2 := absWindow{From: time.Date(2026, 9, 26, 10, 0, 0, 0, ist), To: time.Date(2026, 9, 26, 11, 0, 0, 0, ist)}
	ev := renderWindowCompareTR("checkout", []absWindow{w1, w2}, []aiRED{{Spans: 100, P99Ms: 900}, {Spans: 120, P99Ms: 1200}}, ist)
	if strings.Contains(ev, "tam sayım") || !strings.Contains(ev, "YAKLAŞIK") || !strings.Contains(ev, "örnekleme") {
		t.Errorf("kanıt yaklaşık değeri tam diye sunmamalı:\n%s", ev)
	}
}

// TestParseServiceAnalysis covers the tolerant JSON extraction: bare JSON,
// code-fenced JSON, and surrounding prose; plus a non-JSON miss → nil.
func TestParseServiceAnalysis(t *testing.T) {
	good := `{"ozet":"x","olasi_neden":"y","kanit":["a","b"],"oneriler":["c"],"guven":"orta"}`
	cases := []struct {
		name, in string
		nilWant  bool
	}{
		{"bare", good, false},
		{"fenced", "```json\n" + good + "\n```", false},
		{"prose-wrapped", "İşte analiz:\n" + good + "\nUmarım yardımcı olur.", false},
		{"no-json", "model refused to answer", true},
		{"empty", "", true},
	}
	for _, c := range cases {
		got := parseServiceAnalysis(c.in)
		if c.nilWant && got != nil {
			t.Errorf("%s: want nil, got %+v", c.name, got)
		}
		if !c.nilWant {
			if got == nil {
				t.Errorf("%s: want parsed, got nil", c.name)
			} else if got.Guven != "orta" || len(got.Kanit) != 2 {
				t.Errorf("%s: fields wrong: %+v", c.name, got)
			}
		}
	}
}

// TestPostCheckServiceAnalysis verifies hallucination detection: a service name
// not present in the gathered context is flagged; known services + technical
// hyphenated terms are not.
func TestPostCheckServiceAnalysis(t *testing.T) {
	cx := &aiServiceContext{
		Service:    "payment-service",
		Downstream: []string{"ledger-service"},
		Upstream:   []string{"mobile-bff"},
	}
	// Mentions a known downstream + a technical term → verified.
	clean := &serviceAnalysis{
		Ozet:       "payment-service bozuldu",
		OlasiNeden: "ledger-service çağrılarında timeout",
		Kanit:      []string{"error-rate %0.4 → %8.3", "p99 artışı"},
		Oneriler:   []string{"ledger-service DB havuzunu incele"},
	}
	if pc := postCheckServiceAnalysis(clean, cx); !pc.Verified || len(pc.UnknownServices) != 0 {
		t.Errorf("clean analysis should verify, got %+v", pc)
	}
	// Invents "fraud-detector" which is not in the context → flagged.
	halluc := &serviceAnalysis{
		Ozet:       "sorun fraud-detector kaynaklı",
		OlasiNeden: "auth-gateway yavaş",
		Kanit:      []string{"error-rate yüksek"},
		Oneriler:   []string{"x"},
	}
	pc := postCheckServiceAnalysis(halluc, cx)
	if pc.Verified {
		t.Error("hallucinated services should fail verification")
	}
	found := map[string]bool{}
	for _, u := range pc.UnknownServices {
		found[u] = true
	}
	if !found["fraud-detector"] || !found["auth-gateway"] {
		t.Errorf("expected fraud-detector + auth-gateway flagged, got %v", pc.UnknownServices)
	}
	// error-rate is a technical term, must NOT be flagged.
	if found["error-rate"] {
		t.Error("error-rate is a technical term, must not be flagged as a service")
	}
}

// v0.10.944 (inceleme) — ServiceWindowRED hatası atılıyordu: zaman aşımı
// Current/Baseline'ı sıfır bırakıyor, prompt "önceki pencerede veri yok"
// diyordu. Hata artık sınıfıyla söylenir, "veri yok" diye sunulmaz.
func TestRenderServiceSnapshotReadErrorsAreNotNoData(t *testing.T) {
	cx := &aiServiceContext{Service: "checkout", RangeS: 1800,
		Current: aiRED{Spans: 900, Rate: 0.5, P99Ms: 800},
		baseErr: fmt.Errorf("service window red: %w", context.DeadlineExceeded)}
	got := renderServiceSnapshot(cx)
	if !strings.Contains(got, "Baseline: önceki pencere OKUNAMADI (timeout)") || strings.Contains(got, "önceki pencerede veri yok") {
		t.Errorf("baseline hatası 'veri yok' diye sunulmamalı:\n%s", got)
	}
	if !strings.Contains(got, "RED: rate=0.5") {
		t.Errorf("güncel pencere okunduysa RED satırı kalır:\n%s", got)
	}
	cx = &aiServiceContext{Service: "checkout", RangeS: 1800,
		curErr:  fmt.Errorf("service window red: dial tcp 10.0.0.9:9000: connect: connection refused"),
		baseErr: fmt.Errorf("service window red: %w", context.DeadlineExceeded)}
	got = renderServiceSnapshot(cx)
	if !strings.Contains(got, "RED: mevcut pencere OKUNAMADI (unreachable) — veri yok DEĞİL") || strings.Contains(got, "rate=") {
		t.Errorf("güncel pencere hatası sayı yerine sınıfını söylemeli:\n%s", got)
	}
	// Hata yokken eski davranış: gerçekten boş baseline "veri yok".
	got = renderServiceSnapshot(&aiServiceContext{Service: "checkout", RangeS: 1800, Current: aiRED{Spans: 10}})
	if !strings.Contains(got, "Baseline: önceki pencerede veri yok.") {
		t.Errorf("boş baseline dürüst 'veri yok':\n%s", got)
	}
	// Hata alanları JSON sözleşmesine sızmaz (lib/types.ts değişmez).
	b, _ := json.Marshal(cx)
	if strings.Contains(string(b), "curErr") || strings.Contains(string(b), "baseErr") || strings.Contains(string(b), "refused") {
		t.Errorf("hata alanları JSON'a sızdı: %s", b)
	}
}

// Guided adımları: "span verisi yok" yalnız okuma BAŞARILIYSA; hata çipe
// taşınır (kaynaktan pin — iki bundle da aynı kuralı taşımalı).
func TestGuidedServiceContextStepCarriesReadError(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if n := strings.Count(src, `emitGuidedStepResult(emit, nCtx, "service_context", guidedStepSegment(&b, `); n != 2 {
		t.Fatalf("iki service_context adımı bekleniyordu, %d", n)
	}
	if strings.Count(src, `"service_context", guidedStepSegment(&b, atCtx), cx.curErr)`) != 1 ||
		strings.Count(src, `"service_context", guidedStepSegment(&b, atSnap), cx.curErr)`) != 1 {
		t.Error("service_context adımı okuma hatasını (cx.curErr) çipe taşımalı")
	}
	if strings.Count(src, "cx.curErr == nil && cx.Current.Spans == 0") != 2 {
		t.Error("'span verisi yok' yalnız okuma başarılıyken yazılmalı")
	}
}

// TestServiceSnapshotRateUnitIsSpanPerSecond — v0.10.944: snapshot'taki oran
// service_summary_5m'in TÜM span türleri üzerinden hızıdır; "req/s" istek
// hızını abartıyordu ve guided window_compare aynı sayıya "span/s" diyordu.
// Few-shot örneği gerçek girdiyle aynı birimi taşımalı.
func TestServiceSnapshotRateUnitIsSpanPerSecond(t *testing.T) {
	got := renderServiceSnapshot(&aiServiceContext{Service: "checkout", RangeS: 1800, Current: aiRED{Spans: 900, Rate: 0.5}})
	if !strings.Contains(got, "RED: rate=0.5 span/s (tüm span türleri)") || strings.Contains(got, "req/s") {
		t.Errorf("birim span/s olmalı:\n%s", got)
	}
	b, err := os.ReadFile("../copilot/prompts.go")
	if err != nil {
		t.Fatal(err)
	}
	if src := string(b); !strings.Contains(src, "RED: rate=42.0 span/s (tüm span türleri)") || strings.Contains(src, "RED: rate=42.0 req/s") {
		t.Error("prompts.go few-shot örneği gerçek girdinin birimiyle (span/s) aynı olmalı")
	}
}
