package api

// trace_explain_prefix_signal_test.go — v0.10.1637 — CoSRE kritik yol
// takibi: traceAnswerWithLatencyOwner modelin cevabını değiştirdiğinde
// (öneki EKLEDİ ya da yanlış değerli satırı DÜZELTTİ) olay sayılır; kural
// uygulandığında (TopExternalWait) payda satırı yazılır. Haftalık oran
// /ai → CoSRE kalite panelinde tek satır. Yazım fire-and-forget: CH hatası
// cevabı ASLA etkilemez (bayt bayt aynı).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func TestComposeLatencyOwnerClassifies(t *testing.T) {
	ext := cpGatewayBlock + "\n\nTrace " + defTestTrace + " with 5 spans:\n```json\n[]\n```"
	const line = "dış bağımlılık bekleme: https://gateway.ext.example/v1/score 12.21 s"
	const wrong = "dış bağımlılık bekleme: https://gateway.ext.example/v1/score 12.46 s"
	const body = "**Özet**\n- orders-api"
	cases := []struct {
		name, evidence, answer string
		applied                bool
		action, model          string
	}{
		{"model unuttu → added", ext, body, true, prefixActionAdded, ""},
		{"yanlış değer → corrected", ext, wrong + "\n\n" + body, true, prefixActionCorrected, wrong},
		{"model doğru yazdı → olay yok, payda var", ext, line + "\n\n" + body, true, "", ""},
		{"kalın doğru satır → olay yok", ext, "**" + line + "**\n\n" + body, true, "", ""},
		{"blok yok → hiçbir şey", "Trace x with 1 spans:\n```json\n[]\n```", body, false, "", ""},
		{"boş cevap → hiçbir şey", ext, "", false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ev := composeLatencyOwner(c.evidence, c.answer)
			if out != traceAnswerWithLatencyOwner(c.evidence, c.answer) {
				t.Fatalf("compose ≠ traceAnswerWithLatencyOwner")
			}
			if ev.Applied != c.applied || ev.Action != c.action || ev.ModelLine != c.model {
				t.Errorf("olay %+v; want applied=%v action=%q model=%q", ev, c.applied, c.action, c.model)
			}
			if c.applied {
				if ev.Computed != line || ev.Target != "https://gateway.ext.example/v1/score" || ev.TraceID != defTestTrace {
					t.Errorf("computed/target/trace: %+v", ev)
				}
			}
		})
	}
}

func TestLatencyOwnerSignals(t *testing.T) {
	base := latencyOwnerEvent{Applied: true, TraceID: "t1", Target: "db.ext.example", Computed: "x"}
	if got := latencyOwnerSignals("x1", latencyOwnerEvent{}); len(got) != 0 {
		t.Fatalf("uygulanmadı → sinyal yok: %+v", got)
	}
	got := latencyOwnerSignals("x1", base)
	if len(got) != 1 || got[0].Kind != cosrelearn.SigPrefixApplied || got[0].ExchangeID != "x1" || got[0].UserEmail != cosrelearn.SystemSignalActor {
		t.Fatalf("yalnız payda: %+v", got)
	}
	for _, act := range []string{prefixActionAdded, prefixActionCorrected} {
		ev := base
		ev.Action = act
		got := latencyOwnerSignals("x1", ev)
		if len(got) != 2 || got[1].Kind != cosrelearn.SigPrefixCorrected || !strings.HasPrefix(got[1].Detail, "action="+act+" ") {
			t.Errorf("%s: %+v", act, got)
		}
		for _, g := range got {
			if _, err := validateSystemSignal(g); err != nil {
				t.Errorf("%s: geçersiz sistem sinyali %+v: %v", act, g, err)
			}
		}
	}
}

func TestValidateSystemSignal(t *testing.T) {
	ok := chstore.CosreSignal{ExchangeID: "x", Kind: cosrelearn.SigPrefixApplied, UserEmail: cosrelearn.SystemSignalActor}
	if _, err := validateSystemSignal(ok); err != nil {
		t.Fatal(err)
	}
	bad := []chstore.CosreSignal{
		{Kind: cosrelearn.SigCopy, UserEmail: cosrelearn.SystemSignalActor}, // istemci türü
		{Kind: "made_up", UserEmail: cosrelearn.SystemSignalActor},          // bilinmeyen
		{Kind: cosrelearn.SigPrefixApplied, UserEmail: "a@b"},               // sistem dışı aktör
		{ExchangeID: strings.Repeat("x", 65), Kind: cosrelearn.SigPrefixApplied, UserEmail: cosrelearn.SystemSignalActor},
	}
	for _, b := range bad {
		if _, err := validateSystemSignal(b); err == nil {
			t.Errorf("kabul edilmemeliydi: %+v", b)
		}
	}
	long := ok
	long.Detail = strings.Repeat("é", 600)
	if g, _ := validateSystemSignal(long); len(g.Detail) > cosreSignalDetailMax {
		t.Errorf("ayrıntı tavanı: %d", len(g.Detail))
	}
	// İstemci bu türleri POST edemez (sahte payda/olay).
	for _, k := range []string{cosrelearn.SigPrefixApplied, cosrelearn.SigPrefixCorrected} {
		if _, err := validateCosreSignal(cosreSignalRequest{ExchangeID: "x", Kind: k}); err == nil {
			t.Errorf("istemci %s gönderebiliyor", k)
		}
	}
}

// TestExplainTraceSignalWriteFailureLeavesAnswer — ✨ düğmesi: sinyal yazımı
// hata dönse de cevap, yazıcısız koşuyla bayt bayt aynı; yazıcı çağrılır.
func TestExplainTraceSignalWriteFailureLeavesAnswer(t *testing.T) {
	explanation := func(writer func(context.Context, chstore.CosreSignal) error) string {
		t.Helper()
		setSystemSignalWriter(writer)
		defer setSystemSignalWriter(nil)
		p := newDefProvider(t, defAnswerA, defAnswerB)
		s := defServer(t, p)
		s.tempo = cpServer(t, cpTempoGatewayTrace(t)).tempo
		w := defCall(s, "", "")
		if w.Code != 200 {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Explanation string `json:"explanation"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v: %.300s", err, w.Body.String())
		}
		return body.Explanation
	}
	calls := make(chan chstore.CosreSignal, 8)
	failing := func(_ context.Context, g chstore.CosreSignal) error {
		calls <- g
		return errors.New("ch down")
	}
	got := explanation(failing)
	want := explanation(nil)
	if got != want || !strings.HasPrefix(got, "dış bağımlılık bekleme: ") {
		t.Fatalf("yazım hatası cevabı değiştirdi:\n%q\n%q", got, want)
	}
	kinds := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(kinds) < 2 {
		select {
		case g := <-calls:
			kinds[g.Kind] = true
		case <-deadline:
			t.Fatalf("sinyal yazıcısı çağrılmadı: %v", kinds)
		}
	}
	if !kinds[cosrelearn.SigPrefixApplied] || !kinds[cosrelearn.SigPrefixCorrected] {
		t.Errorf("türler: %v", kinds)
	}
}

func TestPrefixStatsRates(t *testing.T) {
	cases := []struct {
		in             chstore.CosrePrefixStats
		added, correct float64
	}{
		{chstore.CosrePrefixStats{}, 0, 0},
		{chstore.CosrePrefixStats{Applied: 10, Added: 3, Corrected: 1}, 0.3, 0.1},
		{chstore.CosrePrefixStats{Applied: 2, Added: 5}, 1, 0}, // pencere kenarı: oran ≤1
	}
	for _, c := range cases {
		got := c.in.WithRates()
		if got.AddedRate != c.added || got.CorrectedRate != c.correct {
			t.Errorf("%+v → %+v", c.in, got)
		}
	}
	b, _ := json.Marshal(chstore.CosrePrefixStats{Applied: 4, Added: 1}.WithRates())
	if string(b) != `{"applied":4,"added":1,"corrected":0,"addedRate":0.25,"correctedRate":0}` {
		t.Errorf("json: %s", b)
	}
}
