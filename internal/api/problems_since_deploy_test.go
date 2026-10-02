package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// problems_since_deploy_test.go — v0.10.1054: anomaliden terfi eden Problem de
// "yinelenen" kuralına uyar (deploy raporu + rollout çekmecesi).
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun;
// bugün deploy'a hâlâ eski kurala göre bağlanıyor."
//
// "Deploy sonrası problemler" listesi TEK işlevden (problemsSinceDeploy) gelir
// ve anomali listesiyle (anomaliesSinceDeploy, v0.10.1049) AYNI tavrı taşır:
// HİÇBİR satır gizlenmez — seçim bugünkü `StartedAt >= since` kapısı; kaynak
// olayı bu deploy'dan önce de DÜZENLİ görülen terfi Problem'i listede KALIR,
// servisini nitelemeye devam eder ve PredatesDeploy=true taşır. Vaka A / vaka B
// / seyrek / terfi olmayan Problem işaretsiz.
//
// Mutasyon kontrolleri: (1) işaretlenen satır listeden düşürülürse (ilk
// v0.10.1049 taslağının hatası) gece işi satırı kaybolur ve kırılır; (2) kural
// "herhangi bir eski bölüm"e genişletilirse vaka A/B işaretlenir ve kırılır;
// (3) keep zenginleştirmeden SONRA uygulanırsa enrich gereksiz satır görür ve
// kırılır. Bölüm alanlarının YALNIZ terfi Problem'ine iliştirildiği chstore
// promoted_anomaly_source_test.go'da pinli.
func TestProblemsSinceDeploy(t *testing.T) {
	const (
		m   = int64(time.Minute)
		day = 24 * 60 * m
	)
	since := int64(1_700_000_000) * int64(time.Second)
	mk := func(id, rule, svc string, started int64) *chstore.Problem {
		return &chstore.Problem{ID: id, RuleID: rule, Service: svc, StartedAt: started, Status: "open"}
	}
	all := []*chstore.Problem{
		mk("nightly", chstore.PromotedAnomalyRulePrefix+"0123456789abcdef", "batch-svc", since+30*m),
		mk("caseA", chstore.PromotedAnomalyRulePrefix+"1123456789abcdef", "batch-svc", since+5*m),
		mk("caseB", chstore.PromotedAnomalyRulePrefix+"2123456789abcdef", "batch-svc", since+6*m),
		mk("bornAfter", chstore.PromotedAnomalyRulePrefix+"3123456789abcdef", "batch-svc", since+2*day),
		mk("rule", "builtin:error_rate", "batch-svc", since+10*m),
		mk("before", chstore.PromotedAnomalyRulePrefix+"4123456789abcdef", "batch-svc", since-10*m),
		mk("otherSvc", chstore.PromotedAnomalyRulePrefix+"5123456789abcdef", "other-svc", since+40*m),
	}
	// Zenginleştirmenin yaptığı: terfi Problem'ine kaynak olayın bölüm alanları
	// (chstore.attachPromotedEpisodes — burada sahte).
	episodes := map[string][2]int64{ // id → {sayaç, ilk}
		"nightly":   {3, since - 2*day + 30*m},
		"caseA":     {2, since - 60*m},
		"caseB":     {2, since - 20*day},
		"bornAfter": {3, since + 5*m},
		"otherSvc":  {3, since - 2*day},
	}
	var enriched []string
	enrich := func(ps []chstore.Problem) []chstore.Problem {
		for i := range ps {
			enriched = append(enriched, ps[i].ID)
			if e, ok := episodes[ps[i].ID]; ok {
				ps[i].EpisodeCount = uint32(e[0])
				ps[i].FirstStartedAt = e[1]
			}
		}
		return ps
	}

	got := problemsSinceDeploy(all, since, func(svc string) bool { return svc == "batch-svc" }, enrich)
	want := []struct {
		id     string
		marked bool
	}{ // StartedAt azalan (bugünkü sıra)
		{"bornAfter", false}, // yinelenen ama ilk görülme deploy'dan sonra
		{"nightly", true},    // gece işi 3. gece — listede KALIR, işaretli
		{"rule", false},
		{"caseB", false},
		{"caseA", false},
	}
	if len(got) != len(want) {
		t.Fatalf("liste %d satır, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].PredatesDeploy != w.marked {
			t.Errorf("satır %d: (%s, işaret=%v), want (%s, %v)", i, got[i].ID, got[i].PredatesDeploy, w.id, w.marked)
		}
	}
	// keep zenginleştirmeden ÖNCE daraltır; deploy'dan önceki satır hiç
	// zenginleştirilmez (dahil etme kapısı bugünkü gibi).
	if strings.Join(enriched, ",") != "bornAfter,nightly,rule,caseB,caseA" {
		t.Errorf("zenginleştirilen satırlar = %v", enriched)
	}

	// keep nil (deploy raporu) → her servis.
	enriched = nil
	got = problemsSinceDeploy(all, since, nil, enrich)
	if len(got) != 6 || got[1].ID != "otherSvc" || !got[1].PredatesDeploy {
		t.Errorf("keep nil: diğer servisin yinelenen terfi satırı listede ve işaretli olmalı: %+v", got)
	}

	// Tel: işaret yalnız işaretli satırda; diğer satırın JSON'u bugünkü gibi.
	b, _ := json.Marshal(got)
	if n := strings.Count(string(b), `"predatesDeploy":true`); n != 2 {
		t.Errorf("JSON'da %d işaretli satır, want 2: %s", n, b)
	}
}

// TestProblemsSinceDeployCallSites — iki "deploy sonrası" yüzeyi de seçimi bu
// işlevden alıyor; elle filterOpenProblemsSince + enrich kopyası geri gelmez.
func TestProblemsSinceDeployCallSites(t *testing.T) {
	for _, f := range []string{"deployment_report.go", "rollout_detail.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "problemsSinceDeploy(snapshot.All(), sinceNs, ") {
			t.Errorf("%s: problemsSinceDeploy çağrısı yok", f)
		}
		if strings.Contains(src, "filterOpenProblemsSince(snapshot.All()") {
			t.Errorf("%s: elle seçim geri gelmiş — işaret iki kopyada ayrışır", f)
		}
	}
}

// TestProblemToInboxCarriesEpisode — Problems kuyruğunun terfi Problem'i
// satırı anomali satırıyla aynı "yinelenen" işaretini çizebilsin diye bölüm
// alanları ref'e geçer (ek okuma yok). İliştirmesiz satırda alanlar boş.
func TestProblemToInboxCarriesEpisode(t *testing.T) {
	prior := &chstore.RecentDeploy{Version: "v2.0.0", AgeSeconds: 600}
	it := problemToInbox(chstore.Problem{ID: "p", RuleID: chstore.PromotedAnomalyRulePrefix + "0123456789abcdef", EpisodeCount: 3, FirstStartedAt: 42, PriorDeploy: prior})
	if it.Problem == nil || it.Problem.EpisodeCount != 3 || it.Problem.FirstStartedAt != 42 {
		t.Fatalf("terfi Problem satırı bölüm alanlarını taşımıyor: %+v", it.Problem)
	}
	// "Hiçbir şey kaybolmaz": bastırılan deploy satırın ipucuna gider.
	if it.PriorDeploy != prior || it.RecentDeploy != nil {
		t.Fatalf("nötr önceki deploy satıra geçmedi: prior=%+v recent=%+v", it.PriorDeploy, it.RecentDeploy)
	}
	old := problemToInbox(chstore.Problem{ID: "r", RuleID: "builtin:error_rate"})
	b, _ := json.Marshal(old)
	if strings.Contains(string(b), "episodeCount") || strings.Contains(string(b), "firstStartedAt") || strings.Contains(string(b), "priorDeploy") {
		t.Fatalf("kural satırının teli değişti: %s", b)
	}
}
