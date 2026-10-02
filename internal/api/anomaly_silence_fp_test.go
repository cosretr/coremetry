package api

// anomaly_silence_fp_test.go — v0.10.162: susturma parmak izi kanonik sha1
// (chstore.FingerprintAnomaly) olmalı; düz `kind|pattern|service` metni
// tüketicilerle (api.go getTraceOpAnomalies/getLogPatternAnomalies,
// evaluator muted[ev.ID]) asla eşleşmiyordu.
//
// v0.10.1042 (operatör: "Anomalide 'Mute' sonrası satır listeden düşsün") —
// bir OLAY için yazılan susturma O OLAYLA eşleşmeli. log_template_new (kimlik
// şablon kimliğinden) ve behavior_change (kimlik ham metrik adından) türlerinde
// desen görüntü metni, yeniden hesaplanan sha1 olayın kimliği değil: Mute bu iki
// türde hiçbir okuyucuyla eşleşmiyordu. İstemcinin gönderdiği iyi biçimli olay
// kimliği artık olduğu gibi saklanır.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSilenceFingerprintCanonical(t *testing.T) {
	raw := "trace_op|POST /v1/charges|payments-orchestrator"
	got := silenceFingerprint(raw, "trace_op", "POST /v1/charges", "payments-orchestrator")
	want := chstore.FingerprintAnomaly("trace_op", "POST /v1/charges", "payments-orchestrator")
	if got != want || got == raw {
		t.Fatalf("fingerprint = %q, want canonical sha1 %q", got, want)
	}
	// desensiz (Cmd-K gibi) çağıran gönderdiğini korur
	if got := silenceFingerprint("abc123", "trace_op", "", "svc"); got != "abc123" {
		t.Fatalf("patternless silence must keep raw fingerprint, got %q", got)
	}
}

// TestSilenceFingerprintDecision — hangi parmak izinin saklandığına TEK saf
// işlev karar verir. İyi biçimli olay kimliği → olduğu gibi; değilse desen +
// servis → kanonik sha1; o da yoksa raw (v0.10.162 köprüsü). Biçimi bozuk
// değer kimlik sayılmaz.
func TestSilenceFingerprintDecision(t *testing.T) {
	const svc = "svc-a"
	tplID := chstore.FingerprintAnomaly("log_template_new", "tpl-7f3a", svc)
	recomputed := func(kind, pattern string) string { return chstore.FingerprintAnomaly(kind, pattern, svc) }
	cases := []struct {
		name                        string
		raw, kind, pattern, service string
		want                        string
	}{
		{"olay kimliği, desenden türeyen tür (sha1 ile aynı)",
			recomputed("trace_op", "GET /x"), "trace_op", "GET /x", svc, recomputed("trace_op", "GET /x")},
		{"olay kimliği, desen görüntü metni (yeniden hesaplamadan FARKLI) → kimlik kalır",
			tplID, "log_template_new", "Connection to <*> refused", svc, tplID},
		{"düz anahtar → kanonik sha1",
			"log_pattern|ORA-00001|" + svc, "log_pattern", "ORA-00001", svc, recomputed("log_pattern", "ORA-00001")},
		{"büyük harf hex → kimlik DEĞİL, yeniden hesap",
			strings.ToUpper(tplID), "log_template_new", "Connection to <*> refused", svc, recomputed("log_template_new", "Connection to <*> refused")},
		{"15 karakter → yeniden hesap",
			tplID[:15], "trace_op", "GET /x", svc, recomputed("trace_op", "GET /x")},
		{"17 karakter → yeniden hesap",
			tplID + "a", "trace_op", "GET /x", svc, recomputed("trace_op", "GET /x")},
		{"tam sha1 (40) → yeniden hesap",
			strings.Repeat("a", 40), "trace_op", "GET /x", svc, recomputed("trace_op", "GET /x")},
		{"hex olmayan 16 → yeniden hesap",
			"zzzzzzzzzzzzzzzz", "trace_op", "GET /x", svc, recomputed("trace_op", "GET /x")},
		{"boş raw, desen var → yeniden hesap",
			"", "trace_op", "GET /x", svc, recomputed("trace_op", "GET /x")},
		{"olay kimliği, desensiz/servissiz → kimlik kalır",
			tplID, "log_template_new", "", "", tplID},
		{"bozuk raw, desensiz → raw (v0.10.162 köprüsü)",
			"abc123", "trace_op", "", svc, "abc123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := silenceFingerprint(c.raw, c.kind, c.pattern, c.service); got != c.want {
				t.Fatalf("silenceFingerprint(%q, %q, %q, %q) = %q, want %q", c.raw, c.kind, c.pattern, c.service, got, c.want)
			}
		})
	}
}

// silenceRoundTripEvents — dedektörlerin yazdığı şekilde olaylar (kimlik ↔
// desen ilişkisi üreticiyle aynı): recorder.go (log_pattern, trace_op,
// log_template_new), behavior_scan.go behaviorEvent (behaviorEventID +
// behaviorPattern = displayMetric + " · davranış değişimi").
func silenceRoundTripEvents() map[string]chstore.AnomalyEvent {
	const svc = "payments-orchestrator"
	return map[string]chstore.AnomalyEvent{
		"trace_op": {
			ID: chstore.FingerprintAnomaly("trace_op", "POST /v1/charges", svc), Kind: "trace_op",
			Pattern: "POST /v1/charges", Service: svc, Status: "active", PeakRatio: 4,
		},
		"log_pattern": {
			ID: chstore.FingerprintAnomaly("log_pattern", "ORA-00001", svc), Kind: "log_pattern",
			Pattern: "ORA-00001", Service: svc, Status: "active", PeakRatio: 3,
		},
		"log_template_new": {
			ID: chstore.FingerprintAnomaly("log_template_new", "tpl-7f3a", svc), Kind: "log_template_new",
			Pattern: "Connection to <*> refused after <*> ms", Service: svc, Status: "active",
		},
		"behavior_change": {
			ID: chstore.FingerprintAnomaly("behavior_change", "request_rate", svc), Kind: "behavior_change",
			Pattern: "Request rate · davranış değişimi", Service: svc, Status: "active", PeakRatio: 6,
		},
	}
}

// TestSilenceRoundTripPerKind — olay → ön yüzün kurduğu susturma gövdesi
// (lib/inboxDrawer.ts anomalyEventSilenceBody: fingerprint = olay kimliği;
// detay sayfası, inbox çekmecesi, servis sayfası, Cmd-K aynı alanı taşır) →
// saklanan parmak izi == olay kimliği → inbox open elemesi satırı düşürür,
// all görünümü "muted" damgalar. Aynı anahtar evaluator'ın muted[ev.ID]
// kapısı ve `anomaly-auto:<ev.ID>` kapatması.
func TestSilenceRoundTripPerKind(t *testing.T) {
	for kind, ev := range silenceRoundTripEvents() {
		t.Run(kind, func(t *testing.T) {
			body := struct{ Fingerprint, Kind, Pattern, Service string }{ev.ID, ev.Kind, ev.Pattern, ev.Service}
			stored := silenceFingerprint(body.Fingerprint, body.Kind, body.Pattern, body.Service)
			if stored != ev.ID {
				t.Fatalf("saklanan parmak izi %q, olay kimliği %q — susturma bu olayla eşleşmez", stored, ev.ID)
			}
			muted := map[string]bool{stored: true}
			if excl := inboxAnomalyExcludeIDs(muted, "open"); !reflect.DeepEqual(excl, []string{ev.ID}) {
				t.Fatalf("SQL elemesi %v, [%s] bekleniyordu", excl, ev.ID)
			}
			if got := applyInboxAnomalySilences([]InboxItem{anomalyToInbox(ev)}, muted, "open"); len(got) != 0 {
				t.Fatalf("open: susturulmuş satır listede kaldı: %+v", got)
			}
			got := applyInboxAnomalySilences([]InboxItem{anomalyToInbox(ev)}, muted, "all")
			if len(got) != 1 || got[0].Status != inboxMutedStatus {
				t.Fatalf("all: %+v, muted damgalı tek satır bekleniyordu", got)
			}
		})
	}
}

// Eski (yeniden hesaplanmış) susturmalar: desenden türeyen türlerde olay
// kimliğiyle AYNI — çalışmaya devam eder; log_template_new / behavior_change'te
// hiçbir zaman eşleşmediler ve eşleşmemeye devam ederler (kayıt düzeltilmez,
// süreleri dolar). /anomalies canlı akışı (olay değil, düz anahtar) yalnız
// log_pattern / trace_op satırı gösterir; o türlerde yeniden hesap = kimlik.
func TestLegacyRecomputedSilencesPerKind(t *testing.T) {
	for kind, ev := range silenceRoundTripEvents() {
		legacy := chstore.FingerprintAnomaly(ev.Kind, ev.Pattern, ev.Service)
		plain := silenceFingerprint(ev.Kind+"|"+ev.Pattern+"|"+ev.Service, ev.Kind, ev.Pattern, ev.Service)
		if plain != legacy {
			t.Errorf("%s: düz anahtar yolu %q, kanonik %q", kind, plain, legacy)
		}
		switch kind {
		case "trace_op", "log_pattern":
			if legacy != ev.ID {
				t.Errorf("%s: eski susturma olay kimliğiyle eşleşmeli", kind)
			}
		case "log_template_new", "behavior_change":
			if legacy == ev.ID {
				t.Errorf("%s: öncül değişti — desen artık kimliği veriyor, bu test ve DECISIONS kaydı güncellenmeli", kind)
			}
		}
	}
}

// Yazım ucu kararı saf işleve bırakır (tek karar noktası).
func TestCreateSilenceUsesDecision(t *testing.T) {
	body := funcBody(readSrc(t, "anomaly_extra.go"), "createAnomalySilence")
	if !strings.Contains(body, "Fingerprint: silenceFingerprint(body.Fingerprint, body.Kind, body.Pattern, body.Service),") {
		t.Error("createAnomalySilence saklanacak parmak izini silenceFingerprint'e sormuyor")
	}
}
