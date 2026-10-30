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
		if _, ok := Weight[k]; ok {
			t.Errorf("%s puan ağırlığı taşıyor", k)
		}
	}
	in := Inputs{
		Interactions: []chstore.CosreInteraction{{ExchangeID: "x"}},
		Signals: []chstore.CosreSignal{
			{ExchangeID: "x", Kind: SigPrefixApplied, UserEmail: SystemSignalActor},
			{ExchangeID: "x", Kind: SigPrefixCorrected, UserEmail: SystemSignalActor, Detail: "action=added"},
		},
	}
	for _, k := range DeriveSignals(in)["x"] {
		if SystemSignalKinds[k] {
			t.Errorf("DeriveSignals sistem sinyalini cevaba bağladı: %s", k)
		}
	}
}
