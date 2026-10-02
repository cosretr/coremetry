package anomaly

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/correlator"
)

// rootcause_recurring_deploy_test.go — v0.10.1049, yinelenen anomali ayrımı.
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor."
//
// v0.10.1045'ten beri her yeniden tetiklenme yeni bir bölüm: started_at yeni,
// ve bu bölümün 30 dk öncesindeki deploy synthInputForAnomaly'de 0.80 tabanlı
// EN GÜÇLÜ şüpheli oluyordu — gece işi her gece akşamki deploy'a yazılıyordu.
//
// İnceleme düzeltmesi: deploy adayı DÜŞMEZ, İNER. Yalnız DÜZENLİ yinelenen
// (≥ 3 bölüm, ortalama aralık ≤ 48 sa) olayda deploy kanıt olarak kalır ama
// diğer katmanların altına iner ve düz bir gerekçe taşır; ölçülen etki adımı
// (enrichDeployImpact, in.Deploy dolu olduğu için) yine koşar ve gerileme
// gösterirse normal puan geri gelir. Vaka A (deploy kırdı → rollback → bozuk
// yeniden deploy: 2. bölüm) ve vaka B (20 gün önce tek kıpırtı, bugün gerçek
// gerileme) bugünkü atfı korur.
//
// Mutasyon kontrolleri: (1) kural "herhangi bir eski bölüm"e genişletilirse
// vaka A/B deploy'u tepe şüpheli olmaktan çıkar ve kırılır; (2) indirgeme
// yerine düşürme (in.Deploy = nil) geri gelirse gece işi alt testi deploy'u
// bulamaz (ve ölçülen etki adımı koşmaz) ve kırılır.
func TestSynthInputForAnomalyRecurringDemotesDeploy(t *testing.T) {
	const m = int64(time.Minute)
	const day = 24 * 60 * m
	start := int64(1_700_000_000) * int64(time.Second)
	deploy := chstore.RecentDeployEntry{Service: "batch-svc", Version: "v2.0.0", FirstSeenNs: start - 10*m}
	inputs := evidenceInputs{
		deploys: []chstore.RecentDeployEntry{deploy},
		events: []chstore.AnomalyEvent{
			// Aynı servisin başka bir aktif sinyali (0.30–0.60 bandı) —
			// indirgenen deploy'un (0.10) üstünde kalmalı.
			{ID: "other", Service: "batch-svc", Status: "active", Kind: "log_pattern", Pattern: "timeout", CurrentRatio: 4, PeakRatio: 5},
		},
	}
	regressed := &chstore.DeployImpact{P99DeltaPct: 140, ErrorRateDeltaPct: 2.5}
	flat := &chstore.DeployImpact{P99DeltaPct: 3, ErrorRateDeltaPct: 0}

	cases := []struct {
		name        string
		ev          chstore.AnomalyEvent
		impact      *chstore.DeployImpact
		wantDemoted bool // deploy adayı indi mi (aksi hâlde tepe şüpheli)
	}{
		{"yeni olay (first bilinmiyor) — deploy tepe şüpheli, bugünkü gibi",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start}, nil, false},
		{"tek bölüm (first == started) — deploy tepe şüpheli",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start, FirstStartedAt: start, EpisodeCount: 1}, nil, false},
		{"vaka A: deploy kırdı → rollback → bozuk yeniden deploy, 2. bölüm — atıf KORUNUR",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start, FirstStartedAt: start - 60*m, EpisodeCount: 2}, nil, false},
		{"vaka B: 20 gün önce tek kıpırtı, bugün 2. bölüm — atıf KORUNUR",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start, FirstStartedAt: start - 20*day, EpisodeCount: 2}, nil, false},
		{"sayaç 5, ortalama 20 gün — atıf KORUNUR",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start, FirstStartedAt: start - 80*day, EpisodeCount: 5}, nil, false},
		{"gece işi 3. gece — deploy İNER (gerileme ölçülmedi)",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start, FirstStartedAt: start - 2*day, EpisodeCount: 3}, nil, true},
		{"gece işi 3. gece, ölçülen etki düz — deploy İNER",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start, FirstStartedAt: start - 2*day, EpisodeCount: 3}, flat, true},
		{"gece işi 3. gece AMA ölçülen etki gerileme — normal puan geri gelir",
			chstore.AnomalyEvent{ID: "a", Service: "batch-svc", StartedAt: start, FirstStartedAt: start - 2*day, EpisodeCount: 3}, regressed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := synthInputForAnomaly(c.ev, inputs)
			// Deploy HER durumda girdide: ölçülen etki adımı (enrichDeployImpact
			// `in.Deploy == nil` ise erken döner) yinelenen olayda da koşar.
			if in.Deploy == nil {
				t.Fatal("deploy adayı girdiden düştü — indirgeme yerine düşürme; ölçülen etki adımı koşmaz")
			}
			if (in.DeployRecurring != nil) != (c.wantDemoted || c.impact == regressed) {
				t.Errorf("DeployRecurring = %+v, beklenen yinelenme işareti var=%v", in.DeployRecurring, c.wantDemoted || c.impact == regressed)
			}
			in.DeployImpact = c.impact // worker'da enrichDeployImpact doldurur
			// Diğer katmanlar dokunulmadan taşınır.
			if len(in.Signals) != 1 || in.Signals[0].Pattern != "timeout" {
				t.Errorf("aynı-servis sinyali taşınmadı: %+v", in.Signals)
			}
			h := correlator.Synthesize("anomaly", c.ev.ID, c.ev.Service, start, in)
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
			if c.wantDemoted {
				if dep != &h.Candidates[len(h.Candidates)-1] {
					t.Errorf("indirgenen deploy en altta değil: %+v", h.Candidates)
				}
				if !strings.HasPrefix(dep.Reason, "yinelenen anomali: 3. kez, ilk ") ||
					!strings.Contains(dep.Reason, "deploy'dan önce de görülüyordu") {
					t.Errorf("indirgenen deploy gerekçesi düz değil: %q", dep.Reason)
				}
				if h.RecentDeploy != nil {
					t.Errorf("indirgenen deploy hipotezin RecentDeploy'unda — ribbon/istem onu 'olası neden' gösterir: %+v", h.RecentDeploy)
				}
			} else if h.RecentDeploy == nil {
				t.Error("deploy tepe şüpheli ama RecentDeploy boş")
			}
		})
	}
}
