package correlator

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// hypothesis_recurring_deploy_test.go — v0.10.1049, yinelenen anomali.
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor."
//
// DeployRecurring doluyken deploy adayı DÜŞMEZ, İNER: recurringDeployScore
// (0.10) — eş-ateşleme (0.20) ve sinyal bandının (≥ 0.30) altında, düz Türkçe
// gerekçeyle, breadth'e sayılmadan, RecentDeploy boş. Ölçülen etki gerileme
// gösterirse (p99 ≥ +%20 ya da hata ≥ +1 puan — Insight kartıyla aynı eşik)
// normal puan geri gelir.
func TestSynthesizeRecurringDeployDemoted(t *testing.T) {
	first := time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC).UnixNano()
	dep := &chstore.RecentDeploy{Version: "v2.0.0", TimeUnixNs: 1, AgeSeconds: 600}
	rec := &DeployRecurrence{Count: 3, FirstSeenNs: first}
	base := SynthesisInput{
		Deploy:           dep,
		FreshnessFrac:    0.7,
		CoFiringServices: []string{"svc"},
		Signals:          []SignalEvidence{{Kind: "trace_op", Pattern: "GET /x", Ratio: 2}},
	}

	cases := []struct {
		name      string
		recurring *DeployRecurrence
		impact    *chstore.DeployImpact
		wantScore float64 // deploy adayının puanı; < 0 = normal (≥ 0.80)
	}{
		{"yinelenme yok — bugünkü deploy katmanı", nil, nil, -1},
		{"yinelenen, ölçüm yok — iner", rec, nil, recurringDeployScore},
		{"yinelenen, etki düz — iner", rec, &chstore.DeployImpact{P99DeltaPct: 19.9, ErrorRateDeltaPct: 0.99}, recurringDeployScore},
		{"yinelenen, p99 +%20 — normal puan", rec, &chstore.DeployImpact{P99DeltaPct: 20}, -1},
		{"yinelenen, hata +1 puan — normal puan", rec, &chstore.DeployImpact{ErrorRateDeltaPct: 1}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			in.DeployRecurring = c.recurring
			in.DeployImpact = c.impact
			h := Synthesize("anomaly", "a", "svc", 1, in)
			var d *chstore.ScoredCause
			for i := range h.Candidates {
				if strings.Contains(h.Candidates[i].Reason, "v2.0.0") {
					d = &h.Candidates[i]
				}
			}
			if d == nil {
				t.Fatalf("deploy adayı yok — düşürüldü: %+v", h.Candidates)
			}
			if c.wantScore >= 0 {
				if d.Score != c.wantScore {
					t.Errorf("indirgenen puan = %v, want %v", d.Score, c.wantScore)
				}
				if d != &h.Candidates[len(h.Candidates)-1] {
					t.Errorf("indirgenen deploy en altta değil (eş-ateşleme 0.20, sinyal ≥ 0.30): %+v", h.Candidates)
				}
				const wantReason = "yinelenen anomali: 3. kez, ilk 2026-09-30 02:00 UTC — deploy'dan önce de görülüyordu (deploy v2.0.0 10m önce)"
				if !strings.HasPrefix(d.Reason, wantReason) {
					t.Errorf("gerekçe = %q, want önek %q", d.Reason, wantReason)
				}
				if h.RecentDeploy != nil {
					t.Errorf("indirgenen deploy RecentDeploy'da: %+v", h.RecentDeploy)
				}
				// Breadth'e sayılmaz: aynı girdi deploy'suz ile AYNI güveni verir.
				noDep := base
				noDep.Deploy = nil
				if want := Synthesize("anomaly", "a", "svc", 1, noDep).Confidence; h.Confidence != want {
					t.Errorf("indirgenen deploy güveni değiştirdi: %v, deploy'suz %v", h.Confidence, want)
				}
				return
			}
			if d.Score < deployBaseScore || h.Candidates[0].Reason != d.Reason {
				t.Errorf("normal deploy katmanı bekleniyordu (≥ %.2f, tepe): %+v", deployBaseScore, h.Candidates)
			}
			if h.RecentDeploy == nil {
				t.Error("normal deploy adayında RecentDeploy boş")
			}
			if c.recurring != nil && !strings.Contains(d.Reason, "yinelenen anomali: 3. kez") {
				t.Errorf("gerileme gösteren yinelenen deploy yinelenme notunu taşımıyor: %q", d.Reason)
			}
		})
	}
}

// TestSynthesizeRecurringDeployNoRolloutBump — indirgenen deploy, aynı imajın
// rollout kaydıyla YÜKSELMEZ (kayıt deploy'un olduğunu doğrular, anomaliyi
// açıkladığını değil) ve ayrı bir rollout adayı da doğmaz.
func TestSynthesizeRecurringDeployNoRolloutBump(t *testing.T) {
	in := SynthesisInput{
		Deploy:          &chstore.RecentDeploy{Version: "v2.0.0", AgeSeconds: 600},
		DeployRecurring: &DeployRecurrence{Count: 4, FirstSeenNs: 1},
		Rollouts:        []RolloutCandidate{{Subject: "rollout:c/ns/w@3", ImageTag: "v2.0.0", Score: 0.9, Reason: "r"}},
	}
	h := Synthesize("anomaly", "a", "svc", 1, in)
	for _, c := range h.Candidates {
		if c.Score > recurringDeployScore {
			t.Errorf("indirgenen deploy rollout kaydıyla yükseldi / ayrı aday doğdu: %+v", h.Candidates)
		}
	}
}
