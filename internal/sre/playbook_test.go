package sre

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// TestPlaybookTable — tablo pini: tür başına kontrol sırası + kaynaklar
// gerçek adım adı + hipotezler gerçek tür.
func TestPlaybookTable(t *testing.T) {
	want := map[string][]string{
		PlaybookFailureRate: {"error_breakdown", "downstream_calls", "recent_deploys", "dependency_errors", "logs_exceptions"},
		PlaybookLatency:     {"critical_path", "slow_dependency", "saturation", "recent_deploys"},
		PlaybookConsumerLag: {"throughput_vs_rate", "consumer_errors", "downstream", "recent_deploys"},
		PlaybookGeneric:     {"service_sweep", "recent_deploys", "dependencies", "logs"},
	}
	steps := map[string]bool{stepSweep: true, stepDeploys: true, stepDeps: true, stepPivot: true, stepLogSpike: true,
		stepEvents: true, stepNode: true, stepErrors: true}
	if len(playbookTable) != len(want) {
		t.Fatalf("tablo %d tür, beklenen %d", len(playbookTable), len(want))
	}
	for _, pb := range playbookTable {
		var ids []string
		for _, c := range pb.Checks {
			ids = append(ids, c.ID)
			if c.Label == "" || len(c.Sources) == 0 || len(c.Hypotheses) == 0 {
				t.Errorf("%s/%s: etiket/kaynak/hipotez boş", pb.Type, c.ID)
			}
			for _, s := range c.Sources {
				if !steps[s] {
					t.Errorf("%s/%s: bilinmeyen kaynak %q", pb.Type, c.ID, s)
				}
			}
			for _, h := range c.Hypotheses {
				if !IsHypothesisKind(h) {
					t.Errorf("%s/%s: bilinmeyen hipotez %q", pb.Type, c.ID, h)
				}
			}
		}
		if !reflect.DeepEqual(ids, want[pb.Type]) {
			t.Errorf("%s: kontroller %v, beklenen %v", pb.Type, ids, want[pb.Type])
		}
	}
	if PlaybookFor("nope").Type != PlaybookGeneric {
		t.Fatal("bilinmeyen tür → genel")
	}
}

func TestProblemTypeOf(t *testing.T) {
	cases := []struct {
		p    chstore.Problem
		sym  string
		want string
	}{
		{chstore.Problem{Metric: "error_rate"}, symptomError, PlaybookFailureRate},
		{chstore.Problem{Metric: "p99"}, symptomLatency, PlaybookLatency},
		{chstore.Problem{Metric: "consumer_lag"}, "", PlaybookConsumerLag},
		{chstore.Problem{RuleName: "Kafka lag p99"}, symptomLatency, PlaybookConsumerLag},
		{chstore.Problem{Metric: "kafka_lag_max"}, "", PlaybookConsumerLag},
		{chstore.Problem{Metric: "feature_flag_errors"}, symptomError, PlaybookFailureRate}, // "flag" lag değil
		{chstore.Problem{Metric: "down"}, symptomDown, PlaybookGeneric},
		{chstore.Problem{Metric: "request_rate"}, "", PlaybookGeneric},
	}
	for _, c := range cases {
		if got := ProblemTypeOf(c.p, c.sym); got != c.want {
			t.Errorf("%+v/%s: %s, beklenen %s", c.p.Metric+c.p.RuleName, c.sym, got, c.want)
		}
	}
}

func specNames(ss []StepSpec) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}

// TestApplyPlaybook — eksik kaynak aynı okuyucuyla eklenir; var olan adım
// değişmez; node dalga 3 bayrağı; errors (paket okuyucusu) eklenmez.
func TestApplyPlaybook(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	start := now.Add(-20 * time.Minute)
	in := planInput{Anchor: anchorFor(start.UnixNano(), now), StartNs: start.UnixNano(), Now: now}

	// Gecikme problemi: plan pivot'u zaten içerir (latency belirtisi) → yalnız node.
	lat := chstore.Problem{ID: "p", Service: "checkout", Metric: "p99"}
	base := planSteps(lat, nil, in)
	got, node := applyPlaybook(base, PlaybookFor(PlaybookLatency), lat, nil, in)
	if !node || !reflect.DeepEqual(specNames(got), specNames(base)) {
		t.Fatalf("latency: node=%v adımlar %v", node, specNames(got))
	}

	// Lag problemi (belirtisiz): log_spike eklenir (error seviyesi), pivot / node yok.
	lag := chstore.Problem{ID: "p", Service: "consumer", Metric: "consumer_lag"}
	base = planSteps(lag, nil, in)
	got, node = applyPlaybook(base, PlaybookFor(PlaybookConsumerLag), lag, nil, in)
	if node || !reflect.DeepEqual(specNames(got), append(specNames(base), stepLogSpike)) {
		t.Fatalf("lag: node=%v adımlar %v (taban %v)", node, specNames(got), specNames(base))
	}
	if got[len(got)-1].Spike == nil || got[len(got)-1].Spike.Level != "error" {
		t.Fatal("lag: log_spike error seviyesinde olmalı")
	}
	for _, s := range got {
		if s.Name == stepErrors {
			t.Fatal("errors paket okuyucusu — oyun kitabı eklememeli")
		}
	}

	// Servissiz problem → ekleme yok.
	none := chstore.Problem{ID: "p", Metric: "p99"}
	base = planSteps(none, nil, in)
	if got, node = applyPlaybook(base, PlaybookFor(PlaybookLatency), none, nil, in); node || len(got) != len(base) {
		t.Fatal("servissiz problemde oyun kitabı ekleme yapmamalı")
	}
}

// TestRunInvestigationStructured — B1–B4 kayıtta (varsayılan açık, kapı
// shadow); Plain varyant → hiçbiri (B öncesi kayıt), skor aynı.
func TestRunInvestigationStructured(t *testing.T) {
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
	inv := newInv()
	runInvestigation(context.Background(), okReaders(&calls{}, n), inv, func(*Investigation) {}, func() time.Time { return t0 })
	if inv.Playbook == nil || inv.Playbook.Type != PlaybookFailureRate {
		t.Fatalf("oyun kitabı: %+v", inv.Playbook)
	}
	for _, h := range inv.Hypotheses {
		if h.State == "" {
			t.Fatalf("durumsuz hipotez %s", h.Kind)
		}
	}
	if len(inv.Reach) == 0 || inv.Reach[0].Source != stepErrors || inv.Reach[0].Status != ReachNotRun {
		t.Fatalf("erişim (paket kapalı → errors not_run, ilk sırada): %+v", inv.Reach)
	}
	if inv.Coverage == nil || inv.Coverage.Mode != GateShadow {
		t.Fatalf("kapı shadow kaydedilmeli: %+v", inv.Coverage)
	}

	plain := newInv()
	plain.Plain = true
	runInvestigation(context.Background(), okReaders(&calls{}, &fakeNarrator{active: true, auto: true, answer: "x"}), plain, func(*Investigation) {}, func() time.Time { return t0 })
	if plain.Playbook != nil || plain.Reach != nil || plain.Coverage != nil || plain.Hypotheses[0].State != "" {
		t.Fatalf("plain varyant B alanlarını yazmamalı: %+v %+v", plain.Playbook, plain.Coverage)
	}
	if plain.Hypotheses[0].Score != inv.Hypotheses[0].Score || plain.Hypotheses[0].Kind != inv.Hypotheses[0].Kind {
		t.Fatal("durum skoru / sırayı değiştirmemeli")
	}
}
