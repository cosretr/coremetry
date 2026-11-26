package ml

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2475 (operatör bildirimi "Hiçbir şey anlaşılmıyor"): consumer lag
// grup Problem'i okunur cümleyle açılır ve ölçüsü birim karıştırmaz.

// Pin: grup Problem satırının Value ve Threshold'u HER ZAMAN aynı birimdedir
// — ya ikisi de sinyal sayısı (ml_group_signals, Value = Threshold) ya da
// ikisi de lider lag olayının mesaj sayısı (ml_group_lag: gözlenen, taban).
// Tazeleme ve servis düzeyi ölçü ölçüyü (Metric) değerle BİRLİKTE taşır.
func TestGroupProblemNeverMixesUnits(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	imp := lagImpactEvidence(true, 1000, 4)
	decide := func(base, obs float64) decidedGroup {
		e := lagGateEv("a", now, 4*time.Minute, base, obs, imp)
		gs := GroupMLEvents([]chstore.MLEvent{e}, 10*time.Minute)
		return decidedGroup{G: gs[0], D: DecideGroupPromotion(gs[0], []chstore.MLEvent{e}, GroupGateInput{}, now), Members: []chstore.MLEvent{e}}
	}
	desc := func(decidedGroup) string { return "x" }
	check := func(name string, p chstore.Problem, lagObs, lagBase float64) {
		t.Helper()
		switch p.Metric {
		case groupLagMetric:
			if p.Value != lagObs || p.Threshold != lagBase {
				t.Fatalf("%s: lag satırı lag olayının mesaj sayılarını taşımalı: %+v", name, p)
			}
		case groupProblemMetric:
			if p.Value != p.Threshold || p.Value < 1 || p.Value > 3 {
				t.Fatalf("%s: sinyal sayısı satırı Value = Threshold = sinyal sayısı olmalı: %+v", name, p)
			}
		default:
			if p.Metric == "" {
				t.Fatalf("%s: ölçüsüz satır: %+v", name, p)
			}
		}
	}
	// Ekrandaki vaka (sentetik): taban 5 mesaj, şimdi 17.936 → ölçü mesaj/mesaj.
	open := planGroupProblems([]decidedGroup{decide(5, 17936)}, nil, true, desc).Open[0]
	check("açılış", open, 17936, 5)
	if r := open.Value / open.Threshold; r < 3587 || r > 3588 {
		t.Fatalf("oran lag/taban olmalı: %v", r)
	}
	// Sonraki tik daha düşük lag: tepe ölçü korunur, birim aynı.
	re := planGroupProblems([]decidedGroup{decide(5, 26)}, []chstore.Problem{open}, true, desc).Refresh[0]
	check("düşük tik", re, 17936, 5)
	// 2 katın altı (P2 kolu) ilk açılış: sinyal sayısı satırı.
	check("P2 açılış", planGroupProblems([]decidedGroup{decide(600, 900)}, nil, true, desc).Open[0], 0, 0)
	// Servis düzeyi ölçü daha yüksek oranla devralırsa metrik de onunla gelir.
	svc := svcProblemIn{Service: "orders", Value: 0.5, Threshold: 0.00001, Metric: "svc_error_rate", Desc: "y"}
	mix := planMLProblems([]decidedGroup{decide(5, 17936)}, []svcProblemIn{svc}, []chstore.Problem{open}, true, desc).Refresh
	for _, p := range mix {
		if p.ID == open.ID && (p.Metric != "svc_error_rate" || p.Value != 0.5 || p.Threshold != 0.00001) {
			t.Fatalf("servis ölçüsü değer + eşik + metrikle birlikte gelmeli: %+v", p)
		}
	}
}

// Lag lider olayı: Problem cümlesi standart cümle (iç kapı dili yok); topic
// adıyla; diğer açık lag topic'leri "+N topic daha".
func TestSingleProblemDescriptionLag(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	imp := lagImpactEvidence(true, 1000, 4)
	lead := lagGateEv("a", now, 4*time.Minute, 5, 17936, imp)
	other := lagGateEv("b", now, 3*time.Minute, 8, 9000, imp)
	other.Code = kafkaTopicPart("payments.events")
	ended := lagGateEv("c", now, 90*time.Minute, 8, 9000, imp)
	ended.Code = kafkaTopicPart("audit.events")
	ended.LastSeen = now.Add(-60 * time.Minute).UnixNano()
	mem := []chstore.MLEvent{lead, other, ended}
	gs := GroupMLEvents(mem, 120*time.Minute)
	d := DecideGroupPromotion(gs[0], mem, GroupGateInput{}, now)
	if !d.SingleWouldOpen || d.SingleEvent != "a" {
		t.Fatalf("tek sinyal kolu lider a: %+v", d)
	}
	got := singleProblemDescription(decidedGroup{G: gs[0], D: d, Members: mem}, nil)
	h := chstore.StripEvidenceSource(got)
	want := "orders · Kafka consumer lag: normalde ~5 mesaj, şimdi 17.936 (topic orders.events, +1 topic daha)."
	if h != want {
		t.Fatalf("cümle\n got %q\nwant %q", h, want)
	}
	for _, bad := range []string{"tek güçlü sinyal", "teyit etmedi", "ml_group_lag", "sürdürme"} {
		if strings.Contains(got, bad) {
			t.Fatalf("iç dil sızdı (%q): %q", bad, got)
		}
	}
	if !strings.Contains(got, chstore.REDEvidenceVM) {
		t.Fatalf("kaynak işareti (VM) kalmalı: %q", got)
	}
}
