package anomaly

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// stack_version_test.go — v0.10.1044 (operatör: "Kod, dalın ucundan değil
// çalışan sürümden okunsun"). Sürüm YALNIZ stack'i basan servisin
// span'lerinden seçilir: trace'teki başka bir servisin (çok daha fazla span
// basan bir ağ geçidi) sürümü, o servisin deposunda yanlış bir tag'e
// bağlanırdı.
func TestStackServiceVersion(t *testing.T) {
	sp := func(svc, ver string, at int64) chstore.SpanRow {
		return chstore.SpanRow{ServiceName: svc, StartTime: at,
			ResourceAttributes: map[string]string{"service.version": ver}}
	}
	spans := []chstore.SpanRow{
		sp("edge-gateway", "9.0.0", 1), sp("edge-gateway", "9.0.0", 2), sp("edge-gateway", "9.0.0", 3),
		sp("card-service", "1.4.1", 10), sp("card-service", "1.4.2", 20), sp("card-service", "1.4.2", 5),
		sp("card-service", "latest", 30),
	}
	cases := []struct{ svc, want string }{
		{"card-service", "1.4.2"}, // en sık; gateway'in 3 span'i sayılmaz
		{"edge-gateway", "9.0.0"}, // kendi span'leri
		{"missing-service", ""},   // span yok → bilinmiyor
		{"", ""},                  // servis yok → bilinmiyor
	}
	for _, c := range cases {
		if got := StackServiceVersion(spans, c.svc); got != c.want {
			t.Errorf("%q: %q, beklenen %q", c.svc, got, c.want)
		}
	}
	// Eşitlik: en yeni span'in sürümü (rolling deploy'da yeni rollout).
	tie := []chstore.SpanRow{sp("card-service", "1.4.1", 100), sp("card-service", "1.4.2", 200)}
	if got := StackServiceVersion(tie, "card-service"); got != "1.4.2" {
		t.Errorf("eşitlikte en yeni span kazanmalı: %q", got)
	}
}

// Canary: yeni sürümdeki pod düşer (stack'i o basar), eski sürümdeki iki
// deneme başarılı. Çoğunluk 1.4.2 der; doğru cevap stack'i basan span'in
// sürümü 1.5.0. Sıra: logun span'i → logun resource'u → çoğunluk.
func TestStackVersionPrefersTheStackRecord(t *testing.T) {
	span := func(id, ver string, at int64) chstore.SpanRow {
		return chstore.SpanRow{SpanID: id, ServiceName: "card-service", StartTime: at,
			ResourceAttributes: map[string]string{"service.version": ver}}
	}
	spans := []chstore.SpanRow{span("s-fail", "1.5.0", 100), span("s-retry1", "1.4.2", 200), span("s-retry2", "1.4.2", 300)}
	if got := StackVersion(spans, "card-service", "s-fail", nil); got != "1.5.0" {
		t.Fatalf("logun span'i kazanmalı (canary): %q", got)
	}
	logRes := map[string]string{"container.image.tag": "1.5.0"}
	if got := StackVersion(spans, "card-service", "s-absent", logRes); got != "1.5.0" {
		t.Fatalf("span trace'te yoksa logun resource'u: %q", got)
	}
	if got := StackVersion(spans, "card-service", "", nil); got != "1.4.2" {
		t.Fatalf("son çare çoğunluk: %q", got)
	}
	// Logun span'i sürüm taşımıyorsa (yer tutucu) bir sonraki halka.
	noVer := []chstore.SpanRow{span("s-fail", "latest", 100), span("s-retry1", "1.4.2", 200)}
	if got := StackVersion(noVer, "card-service", "s-fail", map[string]string{"service.version": "1.5.0"}); got != "1.5.0" {
		t.Fatalf("span sürümsüzse logun resource'u: %q", got)
	}
}

// Exception: sürüm YALNIZ stack'i veren olaydan. Örneğin trace'i eldeki
// trace değilse sürüm yok (bugünkü davranış) — başka bir olayın sürümü
// asla bu stack'e yamanmaz.
func TestExceptionStackVersionSameOccurrence(t *testing.T) {
	spans := []chstore.SpanRow{
		{SpanID: "a1", ServiceName: "card-service", ResourceAttributes: map[string]string{"service.version": "1.5.0"}},
		{SpanID: "a2", ServiceName: "card-service", ResourceAttributes: map[string]string{"service.version": "1.4.2"}},
		{SpanID: "a3", ServiceName: "card-service", ResourceAttributes: map[string]string{"service.version": "1.4.2"}},
	}
	samples := []chstore.ExceptionSample{
		{TraceID: "trace-new", SpanID: "a1"},
		{TraceID: "trace-old", SpanID: "b9"},
	}
	cases := []struct {
		name      string
		sample    int
		logSpan   string
		haveStack bool
		want      string
	}{
		{"örnek eldeki trace'ten → kendi span'i", 0, "", true, "1.5.0"},
		{"örnek başka trace'ten → sürüm yok", 1, "", true, ""},
		{"log-fallback → logun span'i", -1, "a1", true, "1.5.0"},
		{"log-fallback, span yok → çoğunluk (aynı trace)", -1, "", true, "1.4.2"},
		{"stack yok → sürüm yok", -1, "", false, ""},
	}
	for _, c := range cases {
		got := exceptionStackVersion(samples, c.sample, "trace-new", spans, "card-service", c.logSpan, nil, c.haveStack)
		if got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
}

// Ulaşılabilirlik: exception girdisi sürümü stack'i veren olaydan, ZATEN
// yüklenen örnek trace'in span'leri ve logundan doldurur — ek okuma yok
// ([[feedback-tested-but-unreachable]]).
func TestExceptionInputCarriesStackVersion(t *testing.T) {
	// v0.10.1103 — örnek trace + log gövdesi ortak yardımcılara taşındı
	// (sampleTraceEvidence → traceEvidence / traceLogsEvidence); pinler aynı
	// bağları yeni yerlerinde arar.
	for file, wants := range map[string][]string{
		"exception_context.go": {
			"o.StackForPrompt, o.StackRaw, o.StackService, o.StackSample = pickExceptionStack(",
			"stackVersion := exceptionStackVersion(samples, st.StackSample, traceID, st.Trace.Spans,",
			"verSvc, st.Logs.StackSpan, st.Logs.StackRes,",
			"StackVersion: stackVersion,",
		},
		"trace_evidence.go": {
			"r := traceEvidenceResult{Spans: spans,",
			"r.StackSpan, r.StackRes = lg.SpanID, lg.ResourceAttributes",
		},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := strings.Join(strings.Fields(string(b)), " ")
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("%s: %q yok — sürüm stack'in olayından gelmiyor", file, want)
			}
		}
	}
}
