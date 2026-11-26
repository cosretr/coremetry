package ml

// v0.10.2482 — Sinyaller "neden Problem değil (henüz)" durumu kapının KENDİ
// kararından türer (WhyNotProblemOf). Sözleşme: metin kapının yapmayacağını
// vaat edemez — kalan süre verildiyse kapı (DecideGroupPromotion) o anda
// açar ve bir saniye önce açmaz; süreyle düzelmeyen şart (kat, yön) bir saat
// sonra da açmaz. Servis adları SENTETİK.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// whyRow — üyeleri gruplar, kararı `at` anında verir ve kanıta damgalar
// (kaydedicinin yaptığı gibi), sonra durumu `now` anında okur.
func whyRow(t *testing.T, evs []chstore.MLEvent, in GroupGateInput, at time.Time, wi WhyNotInput) (WhyNotProblem, bool, MLGroup) {
	t.Helper()
	gs := GroupMLEvents(evs, 10*time.Minute)
	if len(gs) != 1 {
		t.Fatalf("tek grup bekleniyordu: %d", len(gs))
	}
	d := DecideGroupPromotion(gs[0], evs, in, at)
	stamped := make([]chstore.MLEvent, len(evs))
	for i, e := range evs {
		e.Evidence = stampGroupShadow(e.Evidence, d)
		stamped[i] = e
	}
	wi.Gate = in
	w, ok := WhyNotProblemOf(stamped, wi)
	return w, ok, gs[0]
}

func TestWhyNotProblemOf(t *testing.T) {
	isBatch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	in := GroupGateInput{IsBatch: isBatch}
	now := gBase.Add(30 * time.Minute)
	down := func(e chstore.MLEvent) chstore.MLEvent { e.Direction = "down"; return e }
	cases := []struct {
		name     string
		evs      []chstore.MLEvent
		running  bool
		live     bool
		want     string
		wantWait time.Duration // > 0: EligibleAt = now + wantWait
	}{
		{"tek sinyal 5 kat, 26 dk önce başladı → 6 dk daha", []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 26, 30, 100, 500)}, true, true, WhySingleShort, 6 * time.Minute},
		{"aynı, canlı kip kapalı → süre yok", []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 26, 30, 100, 500)}, true, false, WhySingleShort, 0},
		{"tek sinyal 1,5 kat, kısa → kat şartı (süre yok)", []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 26, 30, 100, 150)}, true, true, WhySingleBelowRatio, 0},
		{"tek sinyal uzun ama 1,5 kat → eşiğin altında", []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 150)}, true, true, WhySingleBelowRatio, 0},
		{"taban 0, 19 hata → sayım şartı", []chstore.MLEvent{gateEv("a", detTraceErrorCount, "orders-api", 0, 30, 0, 19)}, true, true, WhySingleBelowCount, 0},
		{"gecikme düştü → kötüleşme değil", []chstore.MLEvent{down(gateEv("a", detOpLatency, "orders-api", 0, 30, 500, 100))}, true, true, WhySingleNotWorsening, 0},
		{"batch servis, yalnız yük (log CUSUM toplam)", []chstore.MLEvent{gateEv("a", detChangePoint, "billing-batch", 0, 30, 100, 900)}, true, true, WhyBatch, 0},
		{"kapı açardı (uzun + güçlü)", []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500)}, true, true, WhyGatePassed, 0},
		{"iki sinyal → kapı açardı", []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500), gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 14)}, true, true, WhyGatePassed, 0},
		{"bitti, 4 dk sürdü → 10 dk sürmedi (süre yok)", []chstore.MLEvent{closed(gateEv("a", detOpLatency, "orders-api", 20, 24, 100, 500))}, false, true, WhySingleShort, 0},
		{"bitti, uzun ama 1,5 kat → eşiğin altında kaldı", []chstore.MLEvent{closed(gateEv("a", detOpLatency, "orders-api", 0, 24, 100, 150))}, false, true, WhySingleBelowRatio, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, ok, g := whyRow(t, c.evs, in, now, WhyNotInput{Live: c.live, Running: c.running, Now: now})
			if !ok || w.Code != c.want {
				t.Fatalf("durum %+v ok=%v, istenen %s", w, ok, c.want)
			}
			if c.wantWait > 0 {
				if w.EligibleAt != now.Add(c.wantWait).Unix() || w.Minutes != 10 {
					t.Fatalf("EligibleAt=%d (istenen %d) Minutes=%d", w.EligibleAt, now.Add(c.wantWait).Unix(), w.Minutes)
				}
			} else if w.EligibleAt != 0 {
				t.Fatalf("süre verilmemeliydi: %+v", w)
			}
			// PIN: kalan süre kapının kendi kararıyla doğrulanır.
			if w.EligibleAt > 0 {
				at := time.Unix(w.EligibleAt, 0)
				if d := DecideGroupPromotion(g, c.evs, in, at); !d.WouldOpen {
					t.Fatalf("vaat edilen anda kapı açmadı: %s", d.Reason)
				}
				if d := DecideGroupPromotion(g, c.evs, in, at.Add(-time.Second)); d.WouldOpen {
					t.Fatalf("vaatten önce kapı açtı: %s", d.Reason)
				}
			}
			// PIN: süreyle düzelmeyen şart bir saat sonra da açmaz.
			switch w.Code {
			case WhySingleBelowRatio, WhySingleBelowCount, WhySingleNotWorsening, WhyBatch:
				if c.running {
					if d := DecideGroupPromotion(g, c.evs, in, now.Add(time.Hour)); d.WouldOpen {
						t.Fatalf("%s dedik ama kapı bir saat sonra açıyor: %s", w.Code, d.Reason)
					}
				}
			}
		})
	}
}

// Izgara: süren tek sinyalli satırda süre verildiyse kapı TAM o anda açar;
// verilmediyse ve kod "kapı açardı" değilse kapı şimdi açmıyor.
func TestWhyNotProblemNeverPromisesGrid(t *testing.T) {
	in := GroupGateInput{}
	now := gBase.Add(30 * time.Minute)
	for from := 15; from <= 30; from++ {
		for _, obs := range []float64{100, 150, 199, 200, 300, 900} {
			evs := []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", from, 30, 100, obs)}
			w, ok, g := whyRow(t, evs, in, now, WhyNotInput{Live: true, Running: true, Now: now})
			if !ok {
				t.Fatalf("from=%d obs=%v: durum yok", from, obs)
			}
			opens := DecideGroupPromotion(g, evs, in, now).WouldOpen
			if opens != (w.Code == WhyGatePassed) {
				t.Fatalf("from=%d obs=%v: kapı açar=%v ama kod %s", from, obs, opens, w.Code)
			}
			if w.EligibleAt > 0 {
				at := time.Unix(w.EligibleAt, 0)
				if !DecideGroupPromotion(g, evs, in, at).WouldOpen || DecideGroupPromotion(g, evs, in, at.Add(-time.Second)).WouldOpen {
					t.Fatalf("from=%d obs=%v: EligibleAt %v kapıyla uyuşmuyor", from, obs, at)
				}
			}
		}
	}
}

// Kararı olmayan satır (eski kayıt) durum taşımaz; servis düzeyi kapının
// "açar" kararı "kapıdan geçti" der.
func TestWhyNotProblemNoDecisionAndSvc(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	e := gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500)
	if _, ok := WhyNotProblemOf([]chstore.MLEvent{e}, WhyNotInput{Now: now, Running: true}); ok {
		t.Fatal("kararsız satır durum taşımamalı")
	}
	s := gateEv("s", detSvcErrorRate, "orders-api", 0, 30, 0.01, 0.2)
	s.Evidence = mergeEvidence("", map[string]any{svcShadowKey: SvcShadow{WouldOpen: true, RuleID: MLSvcRulePrefix + "orders-api"}})
	if w, ok := WhyNotProblemOf([]chstore.MLEvent{s}, WhyNotInput{Now: now, Running: true}); !ok || w.Code != WhyGatePassed {
		t.Fatalf("svc kararı: %+v ok=%v", w, ok)
	}
}
