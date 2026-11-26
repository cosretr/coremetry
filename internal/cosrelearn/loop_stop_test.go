package cosrelearn

// loop_stop_test.go — CoSRE ölçüm: döngü künyesi sinyali + iki yeni başarısız
// tur gerekçesi (round_cap, context_overflow). Sözleşme: ayrıntı sabit
// biçimli ve kardinalitesi sınırlı (bilinmeyen durma nedeni "other");
// dedektör loop_stop sinyalinden gerekçe okur; taşma hata metni
// sağlayıcı hatası değil context_overflow sayılır.

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestLoopStopDetailRoundTrip(t *testing.T) {
	l := LoopShape{Stop: "round_cap", Rounds: 5, Calls: 6, Tools: 33, SchemaBytes: 41940, Overflow: OverflowRetried}
	d := LoopStopDetail(l)
	if d != "stop=round_cap rounds=5 calls=6 tools=33 schema_bytes=41940 overflow=retried" {
		t.Fatalf("ayrıntı: %q", d)
	}
	if got := ParseLoopStopDetail(d); !reflect.DeepEqual(got, l) {
		t.Fatalf("geri okuma: %+v", got)
	}
	if d := LoopStopDetail(LoopShape{Stop: "weird-new-reason", Rounds: -1, Overflow: "maybe"}); d != "stop=other rounds=0 calls=0 tools=0 schema_bytes=0" {
		t.Fatalf("kardinalite sınırı: %q", d)
	}
	if d := LoopStopDetail(LoopShape{}); d != "stop=none rounds=0 calls=0 tools=0 schema_bytes=0" {
		t.Fatalf("boş: %q", d)
	}
}

func TestLoopStopReasons(t *testing.T) {
	cases := map[string][]string{
		"stop=stop rounds=2 calls=2 tools=30 schema_bytes=100":      nil,
		"stop=round_cap rounds=5 calls=5 tools=30 schema_bytes=100": {FailRoundCap},
		"stop=call_cap rounds=3 calls=6 tools=30 schema_bytes=100":  {FailRoundCap},
		"stop=token_budget rounds=3":                                {FailRoundCap},
		"stop=stop rounds=2 overflow=retried":                       {FailCtxOverflow},
		"stop=error rounds=1 overflow=failed":                       {FailCtxOverflow},
		"stop=round_cap overflow=retried":                           {FailRoundCap, FailCtxOverflow},
		"garbage":                                                   nil,
	}
	for d, want := range cases {
		if got := LoopStopReasons(d); !reflect.DeepEqual(got, want) {
			t.Errorf("%q → %v, want %v", d, got, want)
		}
	}
}

func TestTurnFailReasonsContextOverflow(t *testing.T) {
	r := ftTurn("x", "a", 0, "q")
	r.Status, r.ErrorMsg = "error", "openai-compat 400: This model's maximum context length is 8192 tokens"
	if got := TurnFailReasons(r); !reflect.DeepEqual(got, []string{FailCtxOverflow}) {
		t.Fatalf("ham taşma: %v", got)
	}
	r.ErrorMsg = "Bu konuşma modelin bağlam penceresine sığmadı. En olası sebep …"
	if got := TurnFailReasons(r); !reflect.DeepEqual(got, []string{FailCtxOverflow}) {
		t.Fatalf("TR taşma mesajı: %v", got)
	}
}

func TestDetectFailedTurnsReadsLoopStop(t *testing.T) {
	rows := []chstore.CosreInteraction{ftTurn("x1", "a", 0, "checkout neden yavaş"), ftTurn("x2", "b", 0, "orders lag")}
	sigs := []chstore.CosreSignal{
		{ExchangeID: "x1", Kind: SigLoopStop, UserEmail: SystemSignalActor, Detail: LoopStopDetail(LoopShape{Stop: "round_cap", Rounds: 5, Tools: 30})},
		{ExchangeID: "x2", Kind: SigLoopStop, UserEmail: SystemSignalActor, Detail: LoopStopDetail(LoopShape{Stop: "stop", Rounds: 2, Tools: 30})},
		{ExchangeID: "zz", Kind: SigLoopStop, UserEmail: SystemSignalActor, Detail: "stop=round_cap"}, // pencere dışı tur
	}
	got := ftReasons(DetectFailedTurns(rows, sigs))
	if !reflect.DeepEqual(got, map[string][]string{"x1": {FailRoundCap}}) {
		t.Fatalf("got %v", got)
	}
	if !SystemSignalKinds[SigLoopStop] {
		t.Fatal("loop_stop sistem sinyali kümesinde olmalı")
	}
	if _, ok := Weight[SigLoopStop]; ok {
		t.Fatal("loop_stop ağırlık taşımaz (etiket değil)")
	}
}
