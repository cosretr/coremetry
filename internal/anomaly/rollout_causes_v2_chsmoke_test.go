//go:build chsmoke

// v0.10.1583 — kök-neden rollout adayı yalnız v1 workload_rollouts'u
// okuyordu.
//
// Kod denetimi bulgusu: rolloutCauses → RolloutsForWorkloads yalnız v1
// tablosunu okuyor; rollouts.source="v2" (varsayılan) iken satırları
// rollout_events'e v2 dedektörü yazıyor ve v1 tablosu boş kalıyor — rollout
// adayı kök nedene HİÇ ulaşmıyordu. Bu test gerçek CH'ye karşı aynı iş
// yükünü iki tabloya ayrı ayrı yazar ve kaynağa göre adayı bekler.
//
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/anomaly/ -run TestRolloutCausesReadsConfiguredSource
package anomaly

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
	"github.com/cilcenk/coremetry/internal/correlator"
	"github.com/cilcenk/coremetry/internal/rollout"
)

type noClusters struct{}

func (noClusters) Clusters() []rollout.ClusterRef { return nil }

func TestRolloutCausesReadsConfiguredSource(t *testing.T) {
	addr := os.Getenv("CH_SMOKE_ADDR")
	if addr == "" {
		t.Skip("CH_SMOKE_ADDR verilmedi — gerçek CH testi atlanıyor")
	}
	st, err := chstore.New(config.CHConfig{Addr: addr, Database: "default", DialTimeout: "10s"}, config.RetentionConfig{})
	if err != nil {
		t.Fatalf("şema kurulamadı: %v", err)
	}
	ctx := context.Background()
	run := time.Now().UnixNano()
	raw, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}, Auth: clickhouse.Auth{Database: "default"}})
	if err != nil {
		t.Fatalf("ham bağlantı: %v", err)
	}
	defer raw.Close()
	onset := time.Now().Add(-5 * time.Minute).UTC()
	rolloutAt := onset.Add(-10 * time.Minute)
	from, to := rolloutWindow(onset.UnixNano())

	cases := []struct {
		name    string
		source  func() string // nil = SetRolloutSource çağrılmadı (bugünkü kablolama)
		seedV1  bool          // workload_rollouts satırı
		seedV2  bool          // rollout_events satırı
		wantHit bool
	}{
		{name: "source=v2, yalnız v2 olayı", source: func() string { return rollout.SourceV2 }, seedV2: true, wantHit: true},
		{name: "source=v1, yalnız v1 satırı", source: func() string { return rollout.SourceV1 }, seedV1: true, wantHit: true},
		{name: "kaynak kurulmamış = v1", seedV1: true, wantHit: true},
		{name: "source=v1, yalnız v2 olayı", source: func() string { return rollout.SourceV1 }, seedV2: true, wantHit: false},
		{name: "source=v2, yalnız v1 satırı", source: func() string { return rollout.SourceV2 }, seedV1: true, wantHit: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := fmt.Sprintf("rc-src-svc-%d-%d", run, i)
			wl := fmt.Sprintf("rc-src-wl-%d-%d", run, i)
			key := rollout.Key{ClusterID: "c1", Namespace: "ns1", Workload: wl}
			// Servisin span ürettiği iş yükü (workload_revision_activity_1m).
			ts := onset.Format("2006-01-02 15:04:05")
			if err := raw.Exec(ctx, `INSERT INTO workload_revision_activity_1m
				(bucket, cluster, k8s_namespace, workload, workload_kind, revision, service_name, image, image_tag,
				 span_count_state, first_seen_state, last_seen_state)
				SELECT toStartOfMinute(toDateTime64(?, 9, 'UTC')), 'c1', 'ns1', ?, 'Deployment', 'rs-2', ?, 'reg/app', 'v2',
				       countState(), minState(toDateTime64(?, 9, 'UTC')), maxState(toDateTime64(?, 9, 'UTC'))`,
				ts, wl, svc, ts, ts); err != nil {
				t.Fatalf("aktivite tohumu: %v", err)
			}
			if tc.seedV1 {
				if err := st.RolloutUpsert(ctx, []rollout.Rollout{{
					ClusterID: "c1", Namespace: "ns1", Workload: wl, Kind: "Deployment", Revision: "rs-2",
					StartedAt: rolloutAt, Status: rollout.StatusCompleted, PrevRevision: "rs-1",
					Image: "reg/app", ImageTag: "v2", PrevImage: "reg/app", PrevImageTag: "v1", DetectedBy: "spans",
				}}); err != nil {
					t.Fatalf("v1 tohumu: %v", err)
				}
			}
			if tc.seedV2 {
				now := time.Now().UTC()
				if err := st.RolloutV2WriteEvents(ctx, []rollout.V2Event{{
					ClusterID: "c1", Namespace: "ns1", WorkloadKind: "Deployment", Workload: wl,
					IncarnationAt: rolloutAt.Add(-24 * time.Hour), Generation: 7, StartedAt: rolloutAt,
					Status: rollout.V2StatusSucceeded, ChangeType: rollout.V2ChangeRollout,
					NewRevision: "rs-2", OldRevision: "rs-1",
					Images: []string{"reg/app:v2"}, PrevImages: []string{"reg/app:v1"},
					SucceededAt: rolloutAt.Add(2 * time.Minute), UpdatedAt: now, Version: uint64(now.UnixNano()),
				}}); err != nil {
					t.Fatalf("v2 tohumu: %v", err)
				}
				// async insert: görünene kadar bekle.
				deadline := time.Now().Add(20 * time.Second)
				for {
					rows, _ := st.RolloutV2ForWorkloads(ctx, []rollout.Key{key}, from, to)
					if len(rows) > 0 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("v2 olayı görünmedi")
					}
					time.Sleep(500 * time.Millisecond)
				}
			}

			s := NewRootCauseSynthesizer(st, nil)
			s.SetRollouts(noClusters{}, func() bool { return true })
			if tc.source != nil {
				s.SetRolloutSource(tc.source)
			}
			var in correlator.SynthesisInput
			ev := s.rolloutCauses(ctx, chstore.Problem{
				ID: "rc-src-p-" + wl, Service: svc, Severity: "critical", Status: "open", StartedAt: onset.UnixNano(),
			}, &in)
			if hit := len(in.Rollouts) > 0 && len(ev) > 0; hit != tc.wantHit {
				t.Fatalf("aday var=%v, istenen %v (in.Rollouts=%d, kanıt=%d)", hit, tc.wantHit, len(in.Rollouts), len(ev))
			}
			if tc.wantHit && ev[0].Workload != wl {
				t.Fatalf("aday yanlış iş yükü: %s", ev[0].Workload)
			}
		})
	}
}
