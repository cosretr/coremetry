package logstore

// variable_corpus_test.go — v0.10.1829 regresyon testi (imza kimliği).
//
// Operator-reported: "Sadece bazen loglarda GUID, UUID, id değerleri
// oluyor; bunları yeni log pattern'i algılamasın." Aynı mesaj farklı bir
// GUID / UUID / id / sayı ile gelince Logs › Desenler'de AYRI satır
// açılıyordu (yapışık UUID `id_<uuid>`, kısa karışık kimlik `TX9F3K2`,
// pod son eki, binlik ayraçlı sayı, ondalık, tek hane, `{0}` biçim yer
// tutucusu, e-posta, IPv6, Java nesne hash'i). Korpus maskcorpus'ta, Drain
// kimliği aynı satırlarla templater/masker_variable_test.go'da.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore/maskcorpus"
)

func TestNormalizeSignature_SameMessageDifferentIDs(t *testing.T) {
	for _, g := range maskcorpus.Same {
		t.Run(g.Name, func(t *testing.T) {
			want := NormalizeSignature(g.Lines[0])
			for _, l := range g.Lines[1:] {
				if got := NormalizeSignature(l); got != want {
					t.Errorf("identity split:\n  %q → %q\n  %q → %q", g.Lines[0], want, l, got)
				}
			}
		})
	}
}

func TestNormalizeSignature_MustStayDifferent(t *testing.T) {
	for _, c := range maskcorpus.Different {
		t.Run(c.Name, func(t *testing.T) {
			if a, b := NormalizeSignature(c.A), NormalizeSignature(c.B); a == b {
				t.Errorf("over-merged: %q and %q → %q", c.A, c.B, a)
			}
		})
	}
}
