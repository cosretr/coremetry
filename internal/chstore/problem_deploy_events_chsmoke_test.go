//go:build chsmoke

// v0.10.1582 — Problem RecentDeploy pipeline deploy event'lerini görmüyordu.
//
// Kod denetimi bulgusu: EnrichProblemsWithDeploys → fetchDeploysByService
// yalnız service_version_5m / ham spans okuyordu; POST /api/operator-events
// ile yazılan events(kind='deploy') satırları (v0.9.1204'ten beri banner,
// marker, pencere ve hipotez hunilerinde birleşen kaynak) problem
// zenginleştirmesine hiç girmiyordu. Sonuç: hipotezin gördüğü pipeline
// deploy'u Problem'in deploy çipinde / DeployBox'ında yok.
//
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/chstore/ -run TestEnrichProblemsSeesPipelineDeployEvents
package chstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestEnrichProblemsSeesPipelineDeployEvents(t *testing.T) {
	st := smokeStore(t)
	ctx := context.Background()
	run := time.Now().UnixNano()

	cases := []struct {
		name       string
		deployAgo  time.Duration // deploy event'i problem başlangıcından ne kadar önce
		eventSvc   string        // "" = problem servisi
		wantDeploy bool
	}{
		{name: "pencere içinde pipeline deploy", deployAgo: 10 * time.Minute, wantDeploy: true},
		{name: "30 dk lookback dışında", deployAgo: 45 * time.Minute, wantDeploy: false},
		{name: "başka servisin deploy'u", deployAgo: 10 * time.Minute, eventSvc: "other", wantDeploy: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := fmt.Sprintf("pde-svc-%d-%d", run, i)
			evSvc := svc
			if tc.eventSvc != "" {
				evSvc = svc + "-" + tc.eventSvc
			}
			onset := time.Now().Add(-5 * time.Minute)
			if _, err := st.UpsertEvent(ctx, Event{
				Kind: "deploy", Label: "v7", Service: evSvc,
				Time: onset.Add(-tc.deployAgo).UnixNano(),
			}); err != nil {
				t.Fatalf("deploy olayı: %v", err)
			}
			ps := st.EnrichProblemsWithDeploys(ctx, []Problem{{
				ID: fmt.Sprintf("pde-p-%d-%d", run, i), RuleID: "rule-pde", Severity: "critical",
				Service: svc, Status: "open", StartedAt: onset.UnixNano(),
			}}, 30*time.Minute)
			got := ps[0].RecentDeploy
			if (got != nil) != tc.wantDeploy {
				t.Fatalf("RecentDeploy=%+v, istenen var=%v", got, tc.wantDeploy)
			}
			if got != nil && got.Version != "v7" {
				t.Fatalf("sürüm %q, istenen v7", got.Version)
			}
		})
	}
}
