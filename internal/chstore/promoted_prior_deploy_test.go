// promoted_prior_deploy_test.go — v0.10.1054: "hiçbir şey kaybolmaz".
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun;
// bugün deploy'a hâlâ eski kurala göre bağlanıyor." İnceleme: kural deploy'u
// "olası neden" saymadığında deploy satırdan ve detaydan TAMAMEN düşmemeli;
// kök-neden işçisi ölçülen gerilemeyle adayı geri aldıysa detay deploy'u
// yeniden "olası neden" göstermeli.
//
// İncelemenin senaryosu: gece işi 7 gece ateşledi, deploy oldu, 8. bölüm
// deploy'dan 10 dk sonra başladı (sayaç 8, ortalama aralık 24 sa).
//
//	(a) critical, ölçülen gerileme: hipotezin RecentDeploy'u dolu → satır /
//	    detay deploy'u yeniden "olası neden" (RecentDeploy), nötr alan boşalır;
//	(b) warning / ölçüm yok (hipotez yok ya da indirgenmiş): RecentDeploy yok,
//	    deploy nötr PriorDeploy'da — atılmaz;
//	(c) sayaç 2 (vaka A): çip bugünkü gibi, PriorDeploy yok.
//
// Anomali satırı (EnrichAnomaliesWithDeploys) aynı seçimden: iki satır aynı.
//
// Mutasyon kontrolleri: PriorDeploy atanmazsa (b) kırılır; attachProblemRootCause
// RestoreMeasuredDeploy'u çağırmazsa (a) kırılır.
package chstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// nightlyEvent — MergeAnomalyCarry'nin gerçek yolu: n gece, her gece 20 dk,
// dakikada bir yazım; son gecenin başlangıcı lastNight.
func nightlyEvent(t *testing.T, id string, nights int64, lastNight int64) AnomalyEvent {
	t.Helper()
	const day = 24 * int64(time.Hour)
	var stored AnomalyEvent
	exists := false
	for n := int64(0); n < nights; n++ {
		night := lastNight - (nights-1-n)*day
		for m := int64(0); m < 20; m++ {
			at := night + m*int64(time.Minute)
			in := AnomalyEvent{ID: id, Kind: "trace_op", Pattern: "nightly-job", Service: "batch-svc", StartedAt: at, LastSeen: at, CurrentRatio: 4}
			stored, exists = MergeAnomalyCarry(in, stored, exists), true
		}
	}
	if stored.EpisodeCount != uint32(nights) || stored.StartedAt != lastNight {
		t.Fatalf("kurulum: sayaç %d başlangıç %d, want %d / %d", stored.EpisodeCount, stored.StartedAt, nights, lastNight)
	}
	return stored
}

func TestPromotedNightlyScenarioNothingDisappears(t *testing.T) {
	const svc = "batch-svc"
	const m = int64(time.Minute)
	ep8 := time.Now().Add(-2 * time.Hour).UnixNano() // 8. bölümün başlangıcı
	deploys := map[string][]spanDeploy{svc: {{version: "v2.0.0", ns: ep8 - 10*m}}}
	fp := FingerprintAnomaly("trace_op", "nightly-job", svc)
	fp2 := FingerprintAnomaly("trace_op", "broken-by-deploy", svc)

	ev8 := nightlyEvent(t, fp, 8, ep8)  // nightly ×7 + deploy + 8. bölüm
	ev2 := nightlyEvent(t, fp2, 2, ep8) // vaka A: 2. bölüm
	problem := func(id string, sev string) Problem {
		return Problem{ID: PromotedAnomalyRulePrefix + id + ":" + svc, RuleID: PromotedAnomalyRulePrefix + id,
			Service: svc, StartedAt: ep8, Severity: sev, Status: "open"}
	}
	enrich := func(t *testing.T, probs []Problem) []Problem {
		t.Helper()
		var qs []string
		var args [][]any
		st := enrichFixture(t, probs, deploys, promotedFakeConn{events: []AnomalyEvent{ev8, ev2}, queries: &qs, args: &args}, 30*time.Minute)
		return st.EnrichProblemsWithDeploys(context.Background(), probs, 30*time.Minute)
	}
	measured := &RecentDeploy{Version: "v2.0.0", TimeUnixNs: ep8 - 10*m, AgeSeconds: 600,
		Impact: &DeployImpact{P99DeltaPct: 140, ErrorRateDeltaPct: 2.5}}

	t.Run("(a) critical, ölçülen gerileme → deploy yeniden olası neden", func(t *testing.T) {
		got := enrich(t, []Problem{problem(fp, "critical")})
		if got[0].RecentDeploy != nil || got[0].PriorDeploy == nil {
			t.Fatalf("ön koşul: kural deploy'u bastırmalı, nötr alanda tutmalı: recent=%+v prior=%+v", got[0].RecentDeploy, got[0].PriorDeploy)
		}
		// Kök-neden işçisi gerilemeyi ölçtü ve adayı geri aldı (hipotezin
		// RecentDeploy'u dolu) — liste / detay okuması aynı toplu okumadan.
		attachProblemRootCause(got, map[string]RootCauseHypothesis{got[0].ID: {TopSuspect: svc, Confidence: 0.6, RecentDeploy: measured}})
		if got[0].RecentDeploy == nil || got[0].RecentDeploy.Version != "v2.0.0" || got[0].RecentDeploy.Impact == nil {
			t.Fatalf("ölçülen gerilemede deploy olası neden olarak geri gelmedi: %+v", got[0].RecentDeploy)
		}
		if got[0].PriorDeploy != nil {
			t.Errorf("geri alınan deploy nötr alanda da kaldı (iki kez basılır): %+v", got[0].PriorDeploy)
		}
		// İndirgenmiş hipotez (ölçüm düz / yok) → geri alma YOK, nötr kalır.
		flat := enrich(t, []Problem{problem(fp, "critical")})
		attachProblemRootCause(flat, map[string]RootCauseHypothesis{flat[0].ID: {TopSuspect: "other", Confidence: 0.4}})
		if flat[0].RecentDeploy != nil || flat[0].PriorDeploy == nil {
			t.Errorf("indirgenmiş hipotezde deploy olası neden oldu ya da kayboldu: recent=%+v prior=%+v", flat[0].RecentDeploy, flat[0].PriorDeploy)
		}
	})

	t.Run("(b) warning, hipotez yok → nötr önceki deploy, atılmaz", func(t *testing.T) {
		got := enrich(t, []Problem{problem(fp, "warning")})
		attachProblemRootCause(got, nil)
		p := got[0]
		if p.RecentDeploy != nil {
			t.Errorf("kural tutarken deploy olası neden: %+v", p.RecentDeploy)
		}
		if p.PriorDeploy == nil || p.PriorDeploy.Version != "v2.0.0" || p.PriorDeploy.AgeSeconds != 600 {
			t.Fatalf("deploy satırdan düştü (nötr alan boş): %+v", p.PriorDeploy)
		}
		if p.EpisodeCount != 8 || p.FirstStartedAt != ep8-7*24*int64(time.Hour) {
			t.Errorf("bölüm alanları (%d, %d), want 8 / ilk gece", p.EpisodeCount, p.FirstStartedAt)
		}
		b, _ := json.Marshal(p)
		if !strings.Contains(string(b), `"priorDeploy":{"version":"v2.0.0"`) || strings.Contains(string(b), `"recentDeploy"`) {
			t.Errorf("tel: %s", b)
		}
		// Anomali satırı AYNI seçimden: aynı nötr deploy, çip yok.
		evs := []AnomalyEvent{ev8}
		var qs []string
		var args [][]any
		st := enrichFixture(t, []Problem{p}, deploys, promotedFakeConn{queries: &qs, args: &args}, 30*time.Minute)
		evs = st.EnrichAnomaliesWithDeploys(context.Background(), evs, 30*time.Minute)
		if evs[0].RecentDeploy != nil || evs[0].PriorDeploy == nil || *evs[0].PriorDeploy != *p.PriorDeploy {
			t.Errorf("anomali satırı terfi satırından farklı: recent=%+v prior=%+v", evs[0].RecentDeploy, evs[0].PriorDeploy)
		}
	})

	t.Run("(c) sayaç 2 → çip bugünkü gibi, nötr alan yok", func(t *testing.T) {
		got := enrich(t, []Problem{problem(fp2, "warning")})
		p := got[0]
		if p.RecentDeploy == nil || p.RecentDeploy.Version != "v2.0.0" || p.PriorDeploy != nil || p.EpisodeCount != 2 {
			t.Errorf("vaka A atfı değişti: recent=%+v prior=%+v sayaç=%d", p.RecentDeploy, p.PriorDeploy, p.EpisodeCount)
		}
	})
}

// TestRestoreMeasuredDeploy — geri alma yalnız kural bir deploy'u bastırdıysa
// (prior dolu) ve hipotez deploy taşıyorsa; diğer her girdide ikili aynen.
func TestRestoreMeasuredDeploy(t *testing.T) {
	d := &RecentDeploy{Version: "v1"}
	h := &RootCauseHypothesis{RecentDeploy: &RecentDeploy{Version: "v2"}}
	cases := []struct {
		name          string
		recent, prior *RecentDeploy
		hyp           *RootCauseHypothesis
		wantRecent    string
		wantPrior     bool
	}{
		{"bastırma yok, hipotez deploy'lu — dokunulmaz (kural/exception satırı)", nil, nil, h, "", false},
		{"recent dolu — dokunulmaz", d, nil, h, "v1", false},
		{"bastırıldı, hipotez yok", nil, d, nil, "", true},
		{"bastırıldı, hipotez indirgenmiş (deploy'suz)", nil, d, &RootCauseHypothesis{}, "", true},
		{"bastırıldı, ölçülen gerileme — geri alınır", nil, d, h, "v2", false},
	}
	for _, c := range cases {
		r, p := RestoreMeasuredDeploy(c.recent, c.prior, c.hyp)
		gotR := ""
		if r != nil {
			gotR = r.Version
		}
		if gotR != c.wantRecent || (p != nil) != c.wantPrior {
			t.Errorf("%s: (%q, prior=%v), want (%q, %v)", c.name, gotR, p != nil, c.wantRecent, c.wantPrior)
		}
	}
	src := mustReadSource(t, "rootcause_hypothesis.go")
	for _, pin := range []string{
		"problems[i].RecentDeploy, problems[i].PriorDeploy = RestoreMeasuredDeploy(",
		"events[i].RecentDeploy, events[i].PriorDeploy = RestoreMeasuredDeploy(",
	} {
		if !strings.Contains(src, pin) {
			t.Errorf("kök-neden özeti geri almayı uygulamıyor: %q", pin)
		}
	}
}
