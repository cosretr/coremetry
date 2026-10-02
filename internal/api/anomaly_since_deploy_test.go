package api

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// anomaly_since_deploy_test.go — v0.10.1049, yinelenen anomali ayrımı.
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor."
//
// Deploy raporu ve rollout çekmecesinin "deploy sonrası anomaliler" listesi
// TEK işlevden (anomaliesSinceDeploy) gelir. İnceleme düzeltmesi: liste HİÇBİR
// satırı gizlemez (seçim bugünkü `StartedAt >= since` ile birebir); o deploy'dan
// önce de DÜZENLİ görülen olay (≥ 3 bölüm, ortalama aralık ≤ 48 sa) yalnız
// PredatesDeploy=true taşır ve ekran onu "yinelenen" diye işaretler. Vaka A
// (deploy kırdı → rollback → bozuk yeniden deploy) ve vaka B (20 gün önceki tek
// kıpırtı, bugün gerçek gerileme) İŞARETSİZ — yeni gibi okunur.
//
// Mutasyon kontrolleri: (1) kural "herhangi bir eski bölüm"e genişletilirse vaka
// A/B işaretlenir ve kırılır; (2) işaretlenen satır listeden düşürülürse (ilk
// taslak) gece işi satırı kaybolur ve kırılır.
func TestAnomaliesSinceDeploy(t *testing.T) {
	const (
		m   = int64(time.Minute)
		day = 24 * 60 * m
	)
	since := int64(1_700_000_000) * int64(time.Second)
	keepAll := func(string) bool { return true }
	cases := []struct {
		name       string
		ev         chstore.AnomalyEvent
		keep       func(string) bool
		wantListed bool
		wantMarked bool
	}{
		{"yeni, deploy'dan sonra başladı (first bilinmiyor) — listede, işaretsiz",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since + 10*m}, keepAll, true, false},
		{"yeni, tam deploy anında — listede (kapsayıcı sınır, bugünkü gibi)",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since}, keepAll, true, false},
		{"bu bölüm deploy'dan önce başladı — listede değil (bugünkü gibi)",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since - 10*m}, keepAll, false, false},
		{"vaka A: 2. bölüm, ilk bölüm 1 sa önce — listede, işaretsiz",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since + 5*m, FirstStartedAt: since - 60*m, EpisodeCount: 2}, keepAll, true, false},
		{"vaka B: 2. bölüm, ilk görülme 20 gün önce — listede, işaretsiz",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since + 5*m, FirstStartedAt: since - 20*day, EpisodeCount: 2}, keepAll, true, false},
		{"gece işi 3. gece — listede KALIR, 'yinelenen' işaretli",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since + 30*m, FirstStartedAt: since - 2*day + 30*m, EpisodeCount: 3}, keepAll, true, true},
		{"yinelenen ama ilk görülme de deploy'dan sonra — işaretsiz",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since + 2*day, FirstStartedAt: since + 5*m, EpisodeCount: 3}, keepAll, true, false},
		{"cleared — listede değil",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "cleared", StartedAt: since + 10*m}, keepAll, false, false},
		{"kapsam dışı servis — listede değil",
			chstore.AnomalyEvent{ID: "a", Service: "s", Status: "active", StartedAt: since + 10*m}, func(string) bool { return false }, false, false},
	}
	for _, c := range cases {
		got := anomaliesSinceDeploy([]chstore.AnomalyEvent{c.ev}, since, c.keep)
		listed := len(got["s"]) == 1
		if listed != c.wantListed {
			t.Errorf("%s: listede=%v, want %v", c.name, listed, c.wantListed)
			continue
		}
		if listed && got["s"][0].PredatesDeploy != c.wantMarked {
			t.Errorf("%s: PredatesDeploy=%v, want %v", c.name, got["s"][0].PredatesDeploy, c.wantMarked)
		}
	}
}

// TestAnomalyToInboxCarriesEpisode — Problems kuyruğunun anomali satırı
// "yinelenen" işaretini çizebilsin diye bölüm sayacı ve ilk görülme olay
// satırından aynen geçer (ek okuma yok). Tek bölümlü / eski satırda alanlar
// boş kalır (omitempty) — satır bugünkü gibi.
func TestAnomalyToInboxCarriesEpisode(t *testing.T) {
	it := anomalyToInbox(chstore.AnomalyEvent{ID: "fp", Status: "active", PeakRatio: 3, EpisodeCount: 3, FirstStartedAt: 42})
	if it.Anomaly == nil || it.Anomaly.EpisodeCount != 3 || it.Anomaly.FirstStartedAt != 42 {
		t.Fatalf("anomali satırı bölüm alanlarını taşımıyor: %+v", it.Anomaly)
	}
	old := anomalyToInbox(chstore.AnomalyEvent{ID: "fp", Status: "active", PeakRatio: 3})
	if old.Anomaly.EpisodeCount != 0 || old.Anomaly.FirstStartedAt != 0 {
		t.Fatalf("eski satırda bölüm alanları uyduruldu: %+v", old.Anomaly)
	}
}

// TestAnomaliesSinceDeployCallSites — iki "deploy sonrası" yüzeyi de seçimi
// bu işlevden alıyor; elle yazılmış bir kopya geri gelmez (iki kopya ayrışırsa
// rapor ile çekmece farklı listeler / işaretler gösterir).
func TestAnomaliesSinceDeployCallSites(t *testing.T) {
	for _, f := range []string{"deployment_report.go", "rollout_detail.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "anomaliesSinceDeploy(allAnomalies, sinceNs, ") {
			t.Errorf("%s: anomaliesSinceDeploy çağrısı yok", f)
		}
		if strings.Contains(src, "a.StartedAt >= sinceNs") {
			t.Errorf("%s: elle started_at kıyası geri gelmiş — işaret ve seçim iki kopyada ayrışır", f)
		}
	}
}
