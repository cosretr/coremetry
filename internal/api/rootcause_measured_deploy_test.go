package api

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/correlator"
)

// rootcause_measured_deploy_test.go — v0.10.1054: /rootcause ucu ölçülen
// gerilemeyi gösterir (problem ve anomali).
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun;
// bugün deploy'a hâlâ eski kurala göre bağlanıyor." İnceleme: kural deploy'u
// bastırdığında /rootcause yanıtının RecentDeploy'u boş kalıyordu; kök-neden
// işçisi gerilemeyi ÖLÇÜP adayı geri aldığında bile (hipotezin RecentDeploy'u
// dolu) detay sayfası "deploy ile çakışıyor" başlığını göstermiyordu. Aynı
// boşluk v0.10.1049'dan beri anomali ucunda da vardı.
//
// Senaryo (a): gece işi 7 gece, deploy, 8. bölüm; critical; ölçülen gerileme.
// Hipotez GERÇEK sentezleyiciden (correlator.Synthesize, DeployRecurring +
// gerileme) — yanıt deploy'u ölçülmüş etkisiyle yeniden "olası neden" taşır.
//
// Mutasyon kontrolü: iki uçtan birinde wg.Wait sonrası RestoreMeasuredDeploy
// çağrısı silinirse çağrı-yeri pini kırılır; geri alma kuralı bozulursa
// senaryo alt testleri kırılır.
func TestRootCauseRestoresMeasuredRecurringDeploy(t *testing.T) {
	const (
		m   = int64(time.Minute)
		day = 24 * 60 * m
	)
	ep8 := int64(1_700_000_000) * int64(time.Second)
	deploy := &chstore.RecentDeploy{Version: "v2.0.0", TimeUnixNs: ep8 - 10*m, AgeSeconds: 600}
	rec := &correlator.DeployRecurrence{Count: 8, FirstSeenNs: ep8 - 7*day}
	synth := func(imp *chstore.DeployImpact) *chstore.RootCauseHypothesis {
		h := correlator.Synthesize("problem", "p", "batch-svc", ep8, correlator.SynthesisInput{
			Deploy: deploy, FreshnessFrac: 0.6, DeployRecurring: rec, DeployImpact: imp,
			Signals: []correlator.SignalEvidence{{Kind: "log_pattern", Pattern: "timeout", Ratio: 4}},
		})
		return &h
	}
	prior := &chstore.RecentDeploy{Version: "v2.0.0", TimeUnixNs: ep8 - 10*m, AgeSeconds: 600}

	t.Run("(a) ölçülen gerileme → deploy olası neden, etkisiyle", func(t *testing.T) {
		h := synth(&chstore.DeployImpact{P99DeltaPct: 140, ErrorRateDeltaPct: 2.5})
		got, _ := chstore.RestoreMeasuredDeploy(nil, prior, h)
		if got == nil || got.Version != "v2.0.0" || got.Impact == nil || got.Impact.P99DeltaPct != 140 {
			t.Fatalf("ölçülen gerilemede /rootcause deploy'u göstermiyor: %+v", got)
		}
	})
	t.Run("ölçüm düz → indirgenmiş, olası neden DEĞİL", func(t *testing.T) {
		h := synth(&chstore.DeployImpact{P99DeltaPct: 3})
		if got, _ := chstore.RestoreMeasuredDeploy(nil, prior, h); got != nil {
			t.Errorf("indirgenmiş adayda deploy olası neden oldu: %+v", got)
		}
	})
	t.Run("bastırma yoksa (kural / exception problemi) yanıt bayt bayt aynı", func(t *testing.T) {
		h := synth(&chstore.DeployImpact{P99DeltaPct: 140})
		if got, p := chstore.RestoreMeasuredDeploy(nil, nil, h); got != nil || p != nil {
			t.Errorf("bastırma olmadan deploy eklendi: %+v", got)
		}
		cur := &chstore.RecentDeploy{Version: "v9"}
		if got, _ := chstore.RestoreMeasuredDeploy(cur, nil, h); got != cur {
			t.Errorf("mevcut deploy değişti: %+v", got)
		}
	})

	// İki uç da bastırılan deploy'u enrich'ten alıp paralel okumalardan SONRA
	// hipotezle değerlendiriyor.
	b, err := os.ReadFile("rootcause.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// v0.10.1119 — fan-out handler'dan demet kurucularına taşındı (çekirdek uç da kullanır).
	for _, fn := range []string{"func (s *Server) problemRootCauseBundle(", "func (s *Server) anomalyRootCauseBundle("} {
		i := strings.Index(src, fn)
		if i < 0 {
			t.Fatalf("%s bulunamadı", fn)
		}
		body := src[i:]
		if end := strings.Index(body[1:], "\nfunc "); end > 0 {
			body = body[:end+1]
		}
		iWait := strings.Index(body, "wg.Wait()")
		iRestore := strings.Index(body, "out.RecentDeploy, _ = chstore.RestoreMeasuredDeploy(out.RecentDeploy, prior, out.Hypothesis)")
		if !strings.Contains(body, "prior = enr[0].PriorDeploy") {
			t.Errorf("%s: bastırılan deploy enrich'ten alınmıyor", fn)
		}
		if iWait < 0 || iRestore < iWait {
			t.Errorf("%s: wg.Wait sonrası ölçülen-gerileme geri alımı yok", fn)
		}
	}
}
