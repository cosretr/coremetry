//go:build chsmoke

// v0.10.1579 — deploy kanıtı kalıcı hipotezden yaşla düşüyordu.
//
// Kod denetimi bulgusu: tik başına paylaşılan deploy okuması
// [now−30m, now] penceresini tarıyordu; seçim ise deploy'un problemin
// BAŞLANGICINDAN önceki 30 dk içinde doğmasını istiyor. Hipotez satırı
// her 30 s'de tam-satır yeniden yazıldığından, deploy'dan >~30 dk sonra
// hâlâ açık critical bir problemin deploy adayı, RecentDeploy'u ve derin
// kanıt kapısı (isP1 || hasDeploy) sessizce kayboluyordu.
//
// Bu test GERÇEK ClickHouse'a karşı synthesizer'ın tikini koşar ve
// yazılan hipotezi okur — saf seçici yeşilken bağlantı kopuk kalmasın.
//
//	docker run -d -p 19000:9000 -e CLICKHOUSE_SKIP_USER_SETUP=1 clickhouse/clickhouse-server
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/anomaly/ -run TestRootCauseDeployEvidenceSurvivesAge
package anomaly

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

func TestRootCauseDeployEvidenceSurvivesAge(t *testing.T) {
	addr := os.Getenv("CH_SMOKE_ADDR")
	if addr == "" {
		t.Skip("CH_SMOKE_ADDR verilmedi — gerçek CH testi atlanıyor")
	}
	st, err := chstore.New(config.CHConfig{
		Addr: addr, Database: "default", DialTimeout: "10s",
	}, config.RetentionConfig{})
	if err != nil {
		t.Fatalf("şema kurulamadı: %v", err)
	}
	ctx := context.Background()

	cases := []struct {
		name string
		// problem onset'i şimdiden ne kadar önce; deploy onset'ten 5 dk önce.
		onsetAgo   time.Duration
		wantDeploy bool
	}{
		{name: "taze problem (deploy 15 dk önce)", onsetAgo: 10 * time.Minute, wantDeploy: true},
		{name: "deploy 30 dk sınırını geçti (45 dk önce)", onsetAgo: 40 * time.Minute, wantDeploy: true},
		{name: "deploy 3 saat önce, problem hâlâ açık", onsetAgo: 3 * time.Hour, wantDeploy: true},
		{name: "deploy 26 saat önce — üst sınırın dışında", onsetAgo: 26 * time.Hour, wantDeploy: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			svc := "age-svc-" + string(rune('a'+i))
			onset := now.Add(-tc.onsetAgo)
			if _, err := st.UpsertEvent(ctx, chstore.Event{
				Kind: "deploy", Label: "v2", Service: svc,
				Time: onset.Add(-5 * time.Minute).UnixNano(),
			}); err != nil {
				t.Fatalf("deploy olayı yazılamadı: %v", err)
			}
			pid := "age-problem-" + svc
			if err := st.UpsertProblem(ctx, chstore.Problem{
				ID: pid, RuleID: "rule-age", RuleName: "error rate",
				Severity: "critical", Service: svc, Metric: "error_rate",
				Value: 0.4, Threshold: 0.05, Status: "open",
				StartedAt: onset.UnixNano(),
			}); err != nil {
				t.Fatalf("problem yazılamadı: %v", err)
			}
			time.Sleep(6 * time.Second) // OpenProblemsSnapshot 5 s memo

			s := NewRootCauseSynthesizer(st, nil)
			s.run(ctx)

			hyps, err := st.GetHypotheses(ctx, "problem", []string{pid})
			if err != nil {
				t.Fatalf("hipotez okunamadı: %v", err)
			}
			h, ok := hyps[pid]
			if !ok {
				t.Fatalf("hipotez yazılmadı (problem %s)", pid)
			}
			if got := h.RecentDeploy != nil; got != tc.wantDeploy {
				t.Fatalf("RecentDeploy var=%v, istenen %v (onset %s önce; top=%q)",
					got, tc.wantDeploy, tc.onsetAgo, h.TopSuspect)
			}
		})
	}
}
