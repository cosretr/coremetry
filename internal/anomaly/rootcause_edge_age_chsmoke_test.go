//go:build chsmoke

// v0.10.1628 — kök-neden kenar grafiği problem yaşlanınca komşuyu düşürüyordu
// (v0.10.1579 deploy kanıtının ikizi; o sürümün gövdesi "kenar grafiği
// penceresi (son 60 dk) aynı biçimde; bu sürümde değişmedi" diye not etti).
//
// Belirti: sentezleyici tik başına paylaşılan komşuluk okumasını
// [now−60m, now] üzerinden yapıyordu. Çağrı kenarı yalnız problemin
// başlangıcı civarında akmışsa (devre kesici açıldı, çağıran bağımlılığı
// çağırmayı bıraktı, trafik yönlendirildi) problem 60 dk'dan yaşlı olunca
// komşu problem Candidates'tan çıkıyor; hipotez her 30 s tam-satır yeniden
// yazıldığı için kök neden adayı sessizce kayboluyordu.
//
// Bu test GERÇEK ClickHouse'a karşı sentezleyicinin tikini koşar.
//
//	docker run -d -p 19000:9000 -e CLICKHOUSE_SKIP_USER_SETUP=1 clickhouse/clickhouse-server
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/anomaly/ -run TestRootCauseEdgeEvidenceSurvivesAge
package anomaly

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

func TestRootCauseEdgeEvidenceSurvivesAge(t *testing.T) {
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
		name          string
		onsetAgo      time.Duration // problem onset'i şimdiden ne kadar önce
		wantNeighbour bool
	}{
		{name: "taze problem, kenar pencerede", onsetAgo: 10 * time.Minute, wantNeighbour: true},
		{name: "problem 90 dk önce başladı, kenar yalnız başlangıçta", onsetAgo: 90 * time.Minute, wantNeighbour: true},
		{name: "problem 3 sa önce başladı, kenar yalnız başlangıçta", onsetAgo: 3 * time.Hour, wantNeighbour: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			caller := "edge-age-caller-" + string(rune('a'+i))
			callee := "edge-age-callee-" + string(rune('a'+i))
			onset := now.Add(-tc.onsetAgo)
			// Kenar yalnız [onset−5m, onset+10m] kovalarında: hata taşıyan
			// çağrılar, sonra trafik kesiliyor.
			for b := onset.Add(-5 * time.Minute).Truncate(5 * time.Minute); !b.After(onset.Add(10 * time.Minute)); b = b.Add(5 * time.Minute) {
				if err := st.Conn().Exec(ctx, `INSERT INTO topology_edges_5m
					(time_bucket, parent_service, child_node, node_kind, protocol, calls, errors, sum_duration_ns)
					VALUES (?, ?, ?, 'service', 'http', 200, 120, 400000000)`,
					b.UTC(), caller, callee); err != nil {
					t.Fatalf("kenar yazılamadı: %v", err)
				}
			}
			for _, p := range []chstore.Problem{
				{ID: "edge-age-" + caller, RuleID: "rule-edge-age", RuleName: "error rate",
					Severity: "critical", Service: caller, Metric: "error_rate",
					Value: 0.4, Threshold: 0.05, Status: "open", StartedAt: onset.UnixNano()},
				{ID: "edge-age-" + callee, RuleID: "rule-edge-age-callee", RuleName: "error rate",
					Severity: "warning", Service: callee, Metric: "error_rate",
					Value: 0.6, Threshold: 0.05, Status: "open", StartedAt: onset.Add(-time.Minute).UnixNano()},
			} {
				if err := st.UpsertProblem(ctx, p); err != nil {
					t.Fatalf("problem yazılamadı: %v", err)
				}
			}
			time.Sleep(6 * time.Second) // OpenProblemsSnapshot 5 s memo

			s := NewRootCauseSynthesizer(st, nil)
			s.run(ctx)

			pid := "edge-age-" + caller
			hyps, err := st.GetHypotheses(ctx, "problem", []string{pid})
			if err != nil {
				t.Fatalf("hipotez okunamadı: %v", err)
			}
			h, ok := hyps[pid]
			if !ok {
				t.Fatalf("hipotez yazılmadı (problem %s)", pid)
			}
			got := false
			for _, c := range h.Candidates {
				if c.Service == callee {
					got = true
				}
			}
			if got != tc.wantNeighbour {
				t.Fatalf("komşu %s aday mı=%v, istenen %v (onset %s önce; top=%q, adaylar=%+v)",
					callee, got, tc.wantNeighbour, tc.onsetAgo, h.TopSuspect, h.Candidates)
			}
		})
	}
}
