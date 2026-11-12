package anomaly

// log_template_new_shared_test.go — v0.10.2069 (ML new_template taşıması):
// ML'e açılan giriş eski dedektörün AYNI iç adımlarını aynı sırayla çağırır
// (çatal yok), ve ML olayının taşıdığı eski satır kimliği eski kayıtçının
// yazdığı kimlikle birebir.

import (
	"os"
	"strings"
	"testing"
)

func TestLegacyEventIDMatchesRecorder(t *testing.T) {
	a := LogTemplateAnomaly{TemplateID: "tpl-7", Template: "payment gateway rejected card <*>", Service: "orders-api"}
	if got, want := a.LegacyEventID(), newTemplateEvent(a).ID; got != want || got == "" {
		t.Fatalf("LegacyEventID %q ≠ kayıtçı kimliği %q", got, want)
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
	// Eski kayıtçı aynı gürültü süzgecini aynı girdilerle çağırıyor (çatal yok).
	rec, err := os.ReadFile("recorder.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec), "filterNewTemplateNoise(found, r.store.AnomalySensitivityForDetectors().IsBatchService, templater.TemplateLevel)") {
		t.Fatal("eski kayıtçının gürültü süzgeci değişti — ML ortak girişini de güncelle")
	}
	if strings.Contains(body, "LogTemplateNewOn()") {
		t.Fatal("ortak giriş eski anahtara bağlanmamalı (ML kendi anahtarına bakar)")
	}
}
