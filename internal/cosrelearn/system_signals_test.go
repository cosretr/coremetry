package cosrelearn

// system_signals_test.go — v0.10.1637 — sunucu kaynaklı sinyaller
// (prefix_applied / prefix_corrected) istemci kümesinde DEĞİL ve gece puanına
// girmez: sunucunun cevabı düzeltmesi modelin kusuru ya da başarısı olarak
// cevaba puan yazmaz, yalnız haftalık orana sayılır.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSystemSignalsStayOutOfScoring(t *testing.T) {
	for k := range SystemSignalKinds {
		if ClientSignalKinds[k] {
			t.Errorf("%s istemci kümesinde", k)
		}
		if w, ok := Weight[k]; ok && w != 0 { // v0.10.2353 — açık 0 (aksiyon sinyalleri) serbest
			t.Errorf("%s puan ağırlığı taşıyor", k)
		}
	}
	in := Inputs{
		Interactions: []chstore.CosreInteraction{{ExchangeID: "x"}},
		Signals: []chstore.CosreSignal{
			{ExchangeID: "x", Kind: SigPrefixApplied, UserEmail: SystemSignalActor},
			{ExchangeID: "x", Kind: SigPrefixCorrected, UserEmail: SystemSignalActor, Detail: "action=added"},
			{ExchangeID: "x", Kind: SigActionConfirm, UserEmail: SystemSignalActor, Detail: `{"kind":"share_link","result":"done"}`},
			{ExchangeID: "x", Kind: SigActionCancel, UserEmail: SystemSignalActor, Detail: `{"kind":"problem_note","result":"cancelled"}`},
		},
	}
	for _, k := range DeriveSignals(in)["x"] {
		if SystemSignalKinds[k] {
			t.Errorf("DeriveSignals sistem sinyalini cevaba bağladı: %s", k)
		}
	}
}

// v0.10.2353 (interaction-audit §5, I4) — aksiyon kartı sinyalleri: sistem
// türü, ağırlık AÇIKÇA 0, puanı değiştirmez ve aday seçiminin okuduğu
// gerekçelere (DeriveSignals → Reasons) girmez (note emsali).
func TestActionSignalsAreNotLabels(t *testing.T) {
	for _, k := range []string{SigActionConfirm, SigActionCancel} {
		if w, ok := Weight[k]; !ok || w != 0 {
			t.Errorf("%s ağırlığı açık 0 olmalı (%v, %v)", k, w, ok)
		}
		if !SystemSignalKinds[k] || ClientSignalKinds[k] {
			t.Errorf("%s yalnız sistem kümesinde olmalı", k)
		}
	}
	in := Inputs{
		Interactions: []chstore.CosreInteraction{{ExchangeID: "x"}},
		Signals:      []chstore.CosreSignal{{ExchangeID: "x", Kind: SigActionConfirm}, {ExchangeID: "x", Kind: SigActionCancel}},
	}
	if got := DeriveSignals(in)["x"]; len(got) != 0 {
		t.Fatalf("aksiyon sinyali gerekçeye girdi: %v", got)
	}
	if sc, pos, neg := AnswerScore(nil, nil, DefaultSignalWeights()); sc != baseScore || pos+neg != 0 {
		t.Fatalf("taban puan değişti: %v", sc)
	}
}
