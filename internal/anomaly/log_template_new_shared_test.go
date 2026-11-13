package anomaly

// log_template_new_shared_test.go — v0.10.2069 (ML new_template taşıması):
// ML'e açılan giriş eski dedektörün AYNI iç adımlarını aynı sırayla çağırır
// (çatal yok), ve ML olayının taşıdığı eski satır kimliği eski kayıtçının
// yazdığı kimlikle birebir.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2085 — eski kayıtçı silindi; geçmiş log_template_new satırları (30 gün
// TTL) bu kimlikle yazılmıştı. ML olayı aynı kimliği taşımaya devam etmeli ki
// listede eski satır bir kez görünsün: biçim burada sabit.
func TestLegacyEventIDMatchesRecorder(t *testing.T) {
	a := LogTemplateAnomaly{TemplateID: "tpl-7", Template: "payment gateway rejected card <*>", Service: "orders-api"}
	if got, want := a.LegacyEventID(), chstore.FingerprintAnomaly("log_template_new", "tpl-7", "orders-api"); got != want || got == "" {
		t.Fatalf("LegacyEventID %q ≠ eski kayıtçı kimliği %q", got, want)
	}
}

func TestNewLogTemplateFamiliesReusesDecision(t *testing.T) {
	src, err := os.ReadFile("log_template_new_shared.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func NewLogTemplateFamilies(")
	if i < 0 {
		t.Fatal("NewLogTemplateFamilies yok")
	}
	body = body[i:]
	body = body[:strings.Index(body, "\n}\n")]
	gate := strings.Index(body, "maskerQuietGate(store.LogMaskerStateCached(ctx))")
	core := strings.Index(body, "detectNewLogTemplates(ctx, store, window, time.Now())")
	noise := strings.Index(body, "filterNewTemplateNoise(found, store.AnomalySensitivityForDetectors().IsBatchService, templater.TemplateLevel)")
	if gate < 0 || core < 0 || noise < 0 || !(gate < core && core < noise) {
		t.Fatalf("ortak karar sırası bozuk: masker=%d çekirdek=%d gürültü=%d", gate, core, noise)
	}
	// v0.10.2085 — eski kayıtçı ve anahtarı silindi: tek çağıran ML.
	rec, err := os.ReadFile("recorder.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rec), "filterNewTemplateNoise(") || strings.Contains(string(rec), "NewLogTemplateFamilies(") {
		t.Fatal("eski kayıtçı yeni log kalıbı olayı yazmamalı (ML new_template tek yazıcı)")
	}
}
