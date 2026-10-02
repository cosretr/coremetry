package anomaly

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/correlator"
)

// rootcause_promoted_recurring_test.go — v0.10.1054: anomaliden terfi eden
// Problem de "yinelenen" kuralına uyar (kök-neden işçisi, PROBLEM çıpası).
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun;
// bugün deploy'a hâlâ eski kurala göre bağlanıyor."
//
// v0.10.1049 anomali çıpasında deploy adayını İNDİRİYORDU (DeployRecurring →
// recurringDeployScore, düz gerekçe, ölçülen gerilemede geri). Aynı olaydan
// terfi eden `anomaly-auto:` Problem'inin çıpası bunu bilmiyordu: deploy 0.80
// tabanlı tepe şüpheli kalıyordu. Artık AYNI mekanizma (deployRecurrence) —
// kaynak olayın sayacı ve ilk görülmesiyle, yalnız olay hâlâ Problem'in
// bölümündeyken (PromotedProblemSource).
//
// Mutasyon kontrolleri: (1) promotedDeployRecurrence hep nil dönerse (kural
// terfi Problem'ine uygulanmaz) gece işi alt testi deploy'u tepede bulur ve
// kırılır; (2) önek kontrolü atlanırsa (kural terfi OLMAYAN Problem'e
// uygulanır) çıplak parmak izi / alarm kuralı alt testleri deploy'u tepede
// bulamaz ve kırılır; (3) indirgeme yerine düşürme gelirse deploy adayı
// hipotezden kaybolur ve kırılır.
func TestPromotedProblemAnchorRecurringDemotesDeploy(t *testing.T) {
	const m = int64(time.Minute)
	const day = 24 * 60 * m
	start := int64(1_700_000_000) * int64(time.Second)
	const svc = "batch-svc"
	fp := chstore.FingerprintAnomaly("trace_op", "nightly-job", svc)
	promotedRule := chstore.PromotedAnomalyRulePrefix + fp
	deploy := chstore.RecentDeployEntry{Service: svc, Version: "v2.0.0", FirstSeenNs: start - 10*m}
	inputs := evidenceInputs{
		deploys: []chstore.RecentDeployEntry{deploy},
		events: []chstore.AnomalyEvent{
			// Aynı servisin başka bir aktif sinyali (0.30–0.60 bandı) —
			// indirgenen deploy'un (0.10) üstünde kalmalı.
			{ID: "other", Service: svc, Status: "active", Kind: "log_pattern", Pattern: "timeout", CurrentRatio: 4, PeakRatio: 5},
		},
	}
	regressed := &chstore.DeployImpact{P99DeltaPct: 140, ErrorRateDeltaPct: 2.5}
	flat := &chstore.DeployImpact{P99DeltaPct: 3, ErrorRateDeltaPct: 0}
	src := func(started, first int64, count uint32) map[string]chstore.AnomalyEvent {
		return map[string]chstore.AnomalyEvent{fp: {ID: fp, Service: svc, StartedAt: started, FirstStartedAt: first, EpisodeCount: count}}
	}
	nightly := src(start, start-2*day, 3)

	cases := []struct {
		name        string
		ruleID      string
		srcs        map[string]chstore.AnomalyEvent
		impact      *chstore.DeployImpact
		wantDemoted bool
		wantMark    bool // DeployRecurring dolu mu (gerilemede de dolu: gerekçeye not)
	}{
		{"terfi, gece işi 3. gece — deploy İNER", promotedRule, nightly, nil, true, true},
		{"terfi, gece işi, ölçülen etki düz — deploy İNER", promotedRule, nightly, flat, true, true},
		{"terfi, gece işi AMA ölçülen etki gerileme — normal puan geri", promotedRule, nightly, regressed, false, true},
		{"terfi, vaka A: 2. bölüm, ilk 1 sa önce — atıf KORUNUR", promotedRule, src(start, start-60*m, 2), nil, false, false},
		{"terfi, vaka B: 20 gün önce tek kıpırtı — atıf KORUNUR", promotedRule, src(start, start-20*day, 2), nil, false, false},
		{"terfi, sayaç 5 ortalama 20 gün — atıf KORUNUR", promotedRule, src(start, start-80*day, 5), nil, false, false},
		{"terfi, olay bulunamadı (TTL) — atıf KORUNUR", promotedRule, map[string]chstore.AnomalyEvent{}, nil, false, false},
		{"terfi, kaynak okunamadı (nil) — atıf KORUNUR", promotedRule, nil, nil, false, false},
		{"terfi, olay yeni bölüme geçmiş — atıf KORUNUR", promotedRule, src(start+day, start-2*day, 4), nil, false, false},
		{"alarm kuralı (aynı servis, aynı an) — atıf KORUNUR", "builtin:error_rate", nightly, nil, false, false},
		{"terfi DEĞİL: kural id'si çıplak parmak izi — atıf KORUNUR", fp, nightly, nil, false, false},
		{"terfi DEĞİL: metrik dedektörü — atıf KORUNUR", "anomaly:" + svc + ":p99_ms", nightly, nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := chstore.Problem{ID: c.ruleID + ":" + svc, RuleID: c.ruleID, Service: svc, StartedAt: start, Severity: "critical", Status: "open"}
			bundle := buildEvidenceBundle(p, inputs)
			in := synthInputForProblem(p, bundle)
			in.DeployRecurring = promotedDeployRecurrence(p, bundle.Deploy, c.srcs)
			if in.Deploy == nil {
				t.Fatal("deploy adayı girdiden düştü — indirgeme yerine düşürme; ölçülen etki adımı koşmaz")
			}
			if (in.DeployRecurring != nil) != c.wantMark {
				t.Fatalf("DeployRecurring = %+v, beklenen işaret var=%v", in.DeployRecurring, c.wantMark)
			}
			in.DeployImpact = c.impact // worker'da enrichDeployImpact doldurur
			h := correlator.Synthesize("problem", p.ID, svc, start, in)
			if len(h.Candidates) == 0 {
				t.Fatal("aday yok")
			}
			deployTop := strings.HasPrefix(h.Candidates[0].Reason, "deployed ")
			if deployTop == c.wantDemoted {
				t.Errorf("tepe şüpheli deploy mu = %v, want %v (adaylar: %+v)", deployTop, !c.wantDemoted, h.Candidates)
			}
			var dep *chstore.ScoredCause
			for i := range h.Candidates {
				if strings.Contains(h.Candidates[i].Reason, "v2.0.0") {
					dep = &h.Candidates[i]
				}
			}
			if dep == nil {
				t.Fatalf("deploy adayı hipotezden kayboldu: %+v", h.Candidates)
			}
			switch {
			case c.wantDemoted:
				if dep != &h.Candidates[len(h.Candidates)-1] {
					t.Errorf("indirgenen deploy en altta değil: %+v", h.Candidates)
				}
				if !strings.HasPrefix(dep.Reason, "yinelenen anomali: 3. kez, ilk 2023-11-12 ") ||
					!strings.Contains(dep.Reason, "deploy'dan önce de görülüyordu (deploy v2.0.0 10m önce)") {
					t.Errorf("indirgenen deploy gerekçesi düz değil: %q", dep.Reason)
				}
				if h.RecentDeploy != nil {
					t.Errorf("indirgenen deploy hipotezin RecentDeploy'unda — ribbon/istem onu 'olası neden' gösterir: %+v", h.RecentDeploy)
				}
			case c.wantMark: // gerileme: normal puan + yinelenme notu
				if !strings.Contains(dep.Reason, "yinelenen anomali: 3. kez") || h.RecentDeploy == nil {
					t.Errorf("gerilemede geri gelen deploy notu / RecentDeploy eksik: %q %+v", dep.Reason, h.RecentDeploy)
				}
			default:
				if h.RecentDeploy == nil || strings.Contains(dep.Reason, "yinelenen") {
					t.Errorf("bugünkü atıf değişti: RecentDeploy=%+v gerekçe=%q", h.RecentDeploy, dep.Reason)
				}
			}
		})
	}
}

// TestProblemExplainerPromotedDeployLine — v0.10.1054: arka plan AI özetinin
// kanıt paketi, sayfadaki hipotez bloğuyla çelişmesin. Terfi Problem'inin
// kaynak olayı deploy'dan önce de düzenli görülüyorsa ve işçi deploy'u ölçülen
// gerilemeyle geri ALMADIYSA DEPLOY satırı nötr ("ana şüpheli değil"); diğer
// her durumda bugünkü "prime suspect" satırı BAYT BAYT.
func TestProblemExplainerPromotedDeployLine(t *testing.T) {
	const m = int64(time.Minute)
	const day = 24 * 60 * m
	start := int64(1_700_000_000) * int64(time.Second)
	const svc = "batch-svc"
	fp := chstore.FingerprintAnomaly("trace_op", "nightly-job", svc)
	deploy := &chstore.RecentDeployEntry{Service: svc, Version: "v2.0.0", FirstSeenNs: start - 10*m}
	nightly := map[string]chstore.AnomalyEvent{fp: {ID: fp, StartedAt: start, FirstStartedAt: start - 7*day, EpisodeCount: 8}}
	caseA := map[string]chstore.AnomalyEvent{fp: {ID: fp, StartedAt: start, FirstStartedAt: start - 60*m, EpisodeCount: 2}}
	promoted := chstore.Problem{ID: "pp", RuleID: chstore.PromotedAnomalyRulePrefix + fp, Service: svc, StartedAt: start}
	rule := chstore.Problem{ID: "r", RuleID: "builtin:error_rate", Service: svc, StartedAt: start}
	restored := &chstore.RootCauseHypothesis{RecentDeploy: &chstore.RecentDeploy{Version: "v2.0.0"}}
	demoted := &chstore.RootCauseHypothesis{TopSuspect: "other"}
	cases := []struct {
		name string
		p    chstore.Problem
		srcs map[string]chstore.AnomalyEvent
		hyp  *chstore.RootCauseHypothesis
		want bool
	}{
		{"terfi, gece işi, hipotez yok → nötr", promoted, nightly, nil, true},
		{"terfi, gece işi, hipotez indirgedi → nötr", promoted, nightly, demoted, true},
		{"terfi, gece işi, ölçülen gerileme geri aldı → bugünkü", promoted, nightly, restored, false},
		{"terfi, vaka A (2. bölüm) → bugünkü", promoted, caseA, nil, false},
		{"terfi, kaynak okunamadı → bugünkü", promoted, nil, nil, false},
		{"alarm kuralı → bugünkü", rule, nightly, nil, false},
	}
	for _, c := range cases {
		if got := problemExplainerDeployPredates(c.p, deploy, c.srcs, c.hyp); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	b := EvidenceBundle{Problem: promoted, Deploy: deploy, Confidence: 2}
	var plain strings.Builder
	renderEvidence(&plain, b)
	if !strings.Contains(plain.String(), "- DEPLOY (prime 'what changed' suspect): batch-svc v2.0.0 deployed 10m before onset\n") {
		t.Errorf("bugünkü satır değişti:\n%s", plain.String())
	}
	b.DeployPredates = true
	var neutral strings.Builder
	renderEvidence(&neutral, b)
	if !strings.Contains(neutral.String(), "- DEPLOY batch-svc v2.0.0 deployed 10m before onset — yinelenen anomali, deploy'dan önce de görülüyordu; ana şüpheli değil\n") ||
		strings.Contains(neutral.String(), "prime") {
		t.Errorf("nötr satır yok / ana şüpheli diyor:\n%s", neutral.String())
	}

	// Tik başına TEK okuma, aday döngüsünden önce.
	src, err := os.ReadFile("problem_explainer.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	iRead := strings.Index(s, "e.store.PromotedAnomalySources(ctx, candidates)")
	iLoop := strings.Index(s, "for _, p := range candidates {")
	if strings.Count(s, "PromotedAnomalySources(") != 1 || iRead < 0 || iLoop < 0 || iRead > iLoop {
		t.Error("ProblemExplainer terfi kaynaklarını tik başına tek okumayla, döngüden önce almıyor")
	}
	if !strings.Contains(s, "bundle.DeployPredates = problemExplainerDeployPredates(p, bundle.Deploy, promoted, hyp)") {
		t.Error("ProblemExplainer nötr deploy satırını bağlamıyor")
	}
}

// TestDeployRecurrenceSingleMechanism — iki çıpa TEK yardımcıdan okur; worker
// kaynakları tik başına TEK toplu okumayla alır ve problem çıpasına bağlar.
func TestDeployRecurrenceSingleMechanism(t *testing.T) {
	b, err := os.ReadFile("rootcause_worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, pin := range []struct {
		s string
		n int
	}{
		{"chstore.AnomalyPredatesDeploy(", 1}, // yalnız deployRecurrence içinde
		{"out.DeployRecurring = deployRecurrence(ev.FirstStartedAt, ev.StartedAt, ev.EpisodeCount, deploy)", 1},
		{"s.store.PromotedAnomalySources(ctx, problems)", 1},
		{"synthIn.DeployRecurring = promotedDeployRecurrence(p, bundle.Deploy, promoted)", 1},
	} {
		if got := strings.Count(src, pin.s); got != pin.n {
			t.Errorf("rootcause_worker.go'da %q %d kez, want %d", pin.s, got, pin.n)
		}
	}
	// Okuma döngünün DIŞINDA (çıpa başına değil, tik başına).
	iRead := strings.Index(src, "s.store.PromotedAnomalySources(ctx, problems)")
	iLoop := strings.Index(src, "for _, p := range problems {")
	if iRead < 0 || iLoop < 0 || iRead > iLoop {
		t.Error("terfi kaynak okuması problem döngüsünden önce değil — çıpa başına okuma olur")
	}
	// Tek-bölümlü / kaynaksız çıpada yardımcı nil döner (bugünkü atıf).
	d := &chstore.RecentDeployEntry{FirstSeenNs: 100}
	if deployRecurrence(0, 200, 1, d) != nil || deployRecurrence(0, 200, 0, d) != nil || deployRecurrence(1, 200, 9, nil) != nil {
		t.Error("deployRecurrence bugünkü atfı değiştirdi")
	}
}
