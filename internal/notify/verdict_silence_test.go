package notify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1016 — "problem değil" imzalarının bildirimi: yalnız politika AÇIKSA
// susar (varsayılan kapalı). İmza sunucuda Problem'den yeniden üretilir ve
// FE'nin ürettiğiyle birebir aynı olmalıdır.

func TestVerdictSignature(t *testing.T) {
	cases := []struct {
		name string
		p    chstore.Problem
		want string
	}{
		{"alarm kuralı", chstore.Problem{ID: "abc", RuleID: "rule-1", Service: "payments"}, "p:rule-1|payments"},
		{"servissiz kural", chstore.Problem{ID: "abc", RuleID: "rule-1"}, "p:rule-1|"},
		{"exception grubu (new)", chstore.Problem{ID: exceptionGroupID("fp1", chstore.ExStateNew),
			RuleID: chstore.ExceptionGroupRulePrefix + chstore.ExStateNew, Service: "payments"}, "e:fp1"},
		{"exception grubu (regressed)", chstore.Problem{ID: exceptionGroupID("fp1", chstore.ExStateRegressed),
			RuleID: chstore.ExceptionGroupRulePrefix + chstore.ExStateRegressed, Service: "payments"}, "e:fp1"},
		{"olay öğretilmez", chstore.Problem{ID: "inc-1", RuleID: "incident:inc-1", Kind: chstore.NotifyKindIncident, Service: "payments"}, ""},
		{"kural kimliği yok", chstore.Problem{ID: "x", Service: "payments"}, ""},
	}
	for _, c := range cases {
		if got := verdictSignature(c.p); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
	if exceptionVerdictSignature("") != "" {
		t.Error("boş parmak izi imza üretmemeli")
	}
}

// Sunucu imzası FE'ninkiyle aynı kalıptan çıkmalı — biri değişirse öğretilen
// kararlar sessizce bildirimle eşleşmez olur.
func TestVerdictSignatureMatchesFrontend(t *testing.T) {
	fe, err := os.ReadFile("../../frontend/src/lib/problemVerdict.ts")
	if err != nil {
		t.Fatal(err)
	}
	// v0.10.1032 — kalıplar problemSignature / exceptionSignature'a taşındı
	// (tam sayfa detaylar da aynı imzayı kursun diye); satır imzası onlara
	// satırın KENDİ servisi ve kural kimliğiyle devreder. Korunan sözleşme aynı.
	for _, w := range []string{
		"`p:${p.ruleId}|${p.service ?? ''}`",
		"`e:${g.fingerprint}`",
		"problemSignature({ ruleId: it.problem.ruleId, service: it.service })",
		"exceptionSignature(it.exception)",
	} {
		if !strings.Contains(string(fe), w) {
			t.Errorf("problemVerdict.ts %s kalıbını taşımalı (sunucu: verdict_silence.go verdictSignature)", w)
		}
	}
}

func TestNoiseSignatureSet(t *testing.T) {
	set := noiseSignatureSet([]chstore.ProblemVerdict{
		{Signature: "p:r|s", Verdict: chstore.ProblemVerdictNoise},
		{Signature: "e:fp", Verdict: chstore.ProblemVerdictReal},
		{Signature: "e:fp2", Verdict: chstore.ProblemVerdictNoise},
	})
	if _, ok := set["p:r|s"]; !ok || len(set) != 2 {
		t.Fatalf("yalnız noise imzaları: %v", set)
	}
	if _, ok := set["e:fp"]; ok {
		t.Error("\"gerçek\" karar susturma kümesine girmemeli")
	}
}

// Önbellek sıcakken depo okunmaz; karar yalnız (politika açık ∧ imza noise).
func TestVerdictSilencedDecision(t *testing.T) {
	ctx := context.Background()
	warm := func(mute bool) *Notifier {
		return &Notifier{store: &chstore.Store{}, pvAt: time.Now(), pvMute: mute,
			pvNoise: map[string]struct{}{"p:r|s": {}}}
	}
	if warm(false).verdictSilenced(ctx, "p:r|s") {
		t.Error("politika KAPALI: noise imzası bile susmamalı (varsayılan davranış değişmez)")
	}
	on := warm(true)
	if !on.verdictSilenced(ctx, "p:r|s") {
		t.Error("politika açık + noise imzası → susmalı")
	}
	if on.verdictSilenced(ctx, "p:other|s") {
		t.Error("işaretlenmemiş imza susmamalı")
	}
	if on.verdictSilenced(ctx, "") {
		t.Error("imzasız kayıt (olay) susmamalı")
	}
	var nilN *Notifier
	if nilN.verdictSilenced(ctx, "p:r|s") || (&Notifier{}).verdictSilenced(ctx, "p:r|s") {
		t.Error("notifier / depo yokken susturma yok")
	}
}

// Kablolama: kapı SSE yayınından SONRA (canlı akış güncellenir), üç yolda da
// var; okuma hatası susturmaz.
func TestVerdictSilenceWiring(t *testing.T) {
	n, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(n)
	gate := strings.Index(src, "n.verdictSilenced(ctx, sig)")
	pub := strings.Index(src, `n.Publish("problem.open", p)`)
	if gate < 0 || pub < 0 || gate < pub {
		t.Errorf("SendProblemAlert kapısı SSE yayınından sonra olmalı (gate=%d publish=%d)", gate, pub)
	}
	e, err := os.ReadFile("exception_notifier.go")
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(e), "e.n.verdictSilenced(ctx, exceptionVerdictSignature(g.Fingerprint))"); c != 2 {
		t.Errorf("exception yolları (kanal + P1 anonsu) kapıyı çağırmalı: %d, 2 beklenir", c)
	}
	v, err := os.ReadFile("verdict_silence.go")
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(v), "return false, nil"); c != 3 {
		t.Errorf("politika kapalı + iki okuma hatası susturmasız dönmeli: %d, 3 beklenir", c)
	}
}
