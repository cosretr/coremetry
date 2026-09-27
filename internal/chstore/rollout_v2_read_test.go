package chstore

// rollout_v2_read_test.go — v0.10.984 — Rollouts v2 P2.3 okuma yolu
// sözleşmesi (rollout_v2_read.go başlığı; docs/rollouts/v2-audit.md §10.6):
//
//   - Her okuma rollout_events / rollout_worker_runs FINAL + zaman sınırlı
//     WHERE + LIMIT + max_execution_time; akış started_at DESC + tam anahtar
//     (toplam sıra), tail keyset (updated_at, anahtar…) + watermark.
//   - ?status= v1 değeri v2'ye çevrilir (rolloutV2Where).
//   - RolloutRowFromV2: v1 alanlarına eşleme (durum v1 sözlüğünde, imaj
//     çifti tam referanstan, DEFAULT 0 zamanları epoch → sıfır).
//   - v1 RolloutRow JSON'u değişmez (anahtar kümesi pinli); v2 satırı
//     yalnız EKLEMELİ anahtarlar taşır.

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
)

func mustContainAll(t *testing.T, what, sql string, wants ...string) {
	t.Helper()
	sql = normCols(sql) // tek satıra (girinti / satır sonu farkı pinlenmez)
	for _, w := range wants {
		if !strings.Contains(sql, w) {
			t.Errorf("%s sorgusunda %q yok:\n%s", what, w, sql)
		}
	}
}

func TestRolloutV2ReadPathSQLShape(t *testing.T) {
	where, args := rolloutV2Where(RolloutFilter{ClusterID: "c", Namespace: "n", Workload: "w", Status: "completed", Kind: "Deployment"},
		time.Unix(100, 0), time.Unix(200, 0))
	if !strings.HasPrefix(where, "started_at >= toDateTime64(?, 3, 'UTC') AND started_at <= toDateTime64(?, 3, 'UTC')") {
		t.Fatalf("zaman sınırı başta olmalı: %s", where)
	}
	mustContainAll(t, "where", where, "cluster_id = ?", "namespace = ?", "workload = ?", "status = ?", "workload_kind = ?")
	if len(args) != 7 || args[5] != rollout.V2StatusSucceeded {
		t.Fatalf("durum v2'ye çevrilmeli (completed → succeeded): %v", args)
	}
	if w2, a2 := rolloutV2Where(RolloutFilter{}, time.Unix(1, 0), time.Unix(2, 0)); strings.Contains(w2, "status") || len(a2) != 2 {
		t.Fatalf("boş süzgeç yalnız pencere: %s %v", w2, a2)
	}

	list := rolloutV2ListSQL(where)
	mustContainAll(t, "akış", list, "FROM rollout_events FINAL WHERE started_at >=",
		"ORDER BY started_at DESC, cluster_id, namespace, workload_kind, workload, incarnation_at, generation",
		"LIMIT ?", "max_execution_time = 10")

	one := rolloutV2ByIDSQL()
	mustContainAll(t, "tekil", one, "FROM rollout_events FINAL",
		"cluster_id = ? AND namespace = ? AND workload_kind = ? AND workload = ?",
		"incarnation_at = toDateTime64(?, 3, 'UTC') AND generation = ?", "LIMIT 1", "max_execution_time = 5")
	if strings.Count(one, "?") != 6 {
		t.Fatalf("tekil 6 bağ: %d", strings.Count(one, "?"))
	}

	tail := rolloutV2TailSQL()
	mustContainAll(t, "tail", tail, "FROM rollout_events FINAL", "WHERE updated_at >= toDateTime64(?, 3, 'UTC')",
		"(updated_at, cluster_id, namespace, workload_kind, workload, incarnation_at, generation) > (toDateTime64(?, 3, 'UTC'), ?, ?, ?, ?, toDateTime64(?, 3, 'UTC'), ?)",
		"updated_at <= toDateTime64(?, 3, 'UTC')", "started_at >= now() - INTERVAL 30 DAY",
		"ORDER BY updated_at, cluster_id, namespace, workload_kind, workload, incarnation_at, generation",
		"LIMIT ?", "max_execution_time = 5")
	if strings.Count(tail, "?") != 10 {
		t.Fatalf("tail 10 bağ: %d", strings.Count(tail, "?"))
	}

	fw := rolloutsV2ForWorkloadsSQL(3)
	mustContainAll(t, "iş yükleri", fw, "FROM rollout_events FINAL",
		"started_at >= toDateTime64(?, 3, 'UTC') AND started_at <= toDateTime64(?, 3, 'UTC')",
		"(cluster_id, namespace, workload) IN ((?, ?, ?), (?, ?, ?), (?, ?, ?))", "LIMIT 200", "max_execution_time = 10")

	for name, q := range map[string]string{
		"toplam": rolloutV2StatsTotalsSQL(where), "top": rolloutV2StatsTopSQL(where, ""), "gün": rolloutV2StatsByDaySQL(where),
	} {
		mustContainAll(t, name, q, "FROM rollout_events FINAL WHERE started_at >=", "max_execution_time = 10")
		if !strings.Contains(q, "LIMIT") && name != "toplam" {
			t.Errorf("%s LIMIT'siz", name)
		}
	}
	tot := rolloutV2StatsTotalsSQL(where)
	mustContainAll(t, "toplam", tot, "countIf(status='succeeded')", "countIf(status='progressing')", "countIf(status='stuck')",
		"dateDiff('second', started_at, succeeded_at)")
	if strings.Contains(tot, "'completed'") || strings.Contains(tot, "in_progress") {
		t.Fatalf("v2 tablosunda v1 durum adı sayılmaz: %s", tot)
	}

	runs := rolloutWorkerRunsSQL(7)
	mustContainAll(t, "koşular", runs, "FROM rollout_worker_runs FINAL", "WHERE worker = ?",
		"started_at >= now() - INTERVAL 7 DAY", "ORDER BY started_at DESC", "LIMIT ?", "max_execution_time = 5")
	if n := len(strings.Split(rolloutWorkerRunReadCols, ",")); n != 16 {
		t.Fatalf("koşu okuması 16 kolon (version hariç): %d", n)
	}

	svc := rolloutWorkloadServicesBatchSQL(2)
	mustContainAll(t, "servis", svc, "FROM workload_revision_activity_1m",
		"(cluster, k8s_namespace, workload) IN ((?, ?, ?), (?, ?, ?))", "bucket >= toDateTime(?, 'UTC')", "LIMIT 400",
		"max_execution_time = 10")
	if strings.Contains(svc, "revision =") || strings.Contains(svc, "workload, revision") {
		t.Fatalf("iş yükü düzeyi servis çözümü revizyonla süzmez: %s", svc)
	}
}

func TestRolloutRowFromV2(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	epoch := time.Unix(0, 0).UTC()
	e := rollout.V2Event{ClusterID: "c-1", Namespace: "pay", WorkloadKind: "StatefulSet", Workload: "db",
		IncarnationAt: t0.Add(-time.Hour), Generation: 5, StartedAt: t0, Status: rollout.V2StatusStuck,
		ChangeType: rollout.V2ChangeConfig, NewRevision: "db-5c", OldRevision: "db-4b",
		Images: []string{"reg:5000/db:16.2", "reg/side:1"}, PrevImages: nil, StuckReason: rollout.V2StuckTimeout,
		SucceededAt: epoch, StuckAt: t0.Add(11 * time.Minute), FinishedAt: epoch, Note: "n", UpdatedAt: t0.Add(12 * time.Minute),
		SpecReplicas: 3, UpdatedReplicas: 1, AvailableReplicas: 2, Version: 9}
	r := RolloutRowFromV2(e)
	if r.Status != rollout.StatusStalled || r.Kind != "StatefulSet" || r.Revision != "db-5c" || r.PrevRevision != "db-4b" || r.DetectedBy != "ksm" {
		t.Fatalf("v1 alanları: %+v", r.Rollout)
	}
	if r.Image != "reg:5000/db" || r.ImageTag != "16.2" || r.PrevImage != "" || r.PrevImageTag != "" {
		t.Fatalf("imaj çifti: %q %q %q %q", r.Image, r.ImageTag, r.PrevImage, r.PrevImageTag)
	}
	if !r.CompletedAt.IsZero() || !r.PodsReadyAt.IsZero() || !r.KSMNotReadySince.Equal(t0.Add(11*time.Minute)) || !r.KSMStartedAt.Equal(t0) {
		t.Fatalf("zamanlar (epoch sentinel → sıfır): %+v", r.Rollout)
	}
	if r.V2 == nil || r.V2.Generation != 5 || r.V2.Status != "stuck" || r.V2.StuckReason != "timeout" || r.V2.PrevImages == nil || !r.V2.SucceededAt.IsZero() {
		t.Fatalf("v2 alanları: %+v", r.V2)
	}
	// Tamamlanan: completedAt = succeeded_at; yalnız biten (devralınan) → finished_at.
	e.Status, e.SucceededAt, e.FinishedAt = rollout.V2StatusSucceeded, t0.Add(time.Minute), t0.Add(time.Minute)
	if got := RolloutRowFromV2(e); !got.CompletedAt.Equal(t0.Add(time.Minute)) || got.Status != rollout.StatusCompleted {
		t.Fatalf("succeeded: %+v", got.Rollout)
	}
	e.Status, e.SucceededAt, e.FinishedAt = rollout.V2StatusSuperseded, epoch, t0.Add(3*time.Minute)
	if got := RolloutRowFromV2(e); !got.CompletedAt.Equal(t0.Add(3*time.Minute)) || got.Status != rollout.StatusSuperseded {
		t.Fatalf("superseded: %+v", got.Rollout)
	}
	// İnceleme: istio sidecar'ı sıralı dizide önde; imaj çifti değişen
	// uygulama imajından, önceki taraf aynı repodan (eskiden 1.20.1 → 1.20.1).
	e.Workload = "api"
	e.Images = []string{"docker.io/istio/proxyv2:1.20.1", "harbor.corp/pay/api:2.5.0"}
	e.PrevImages = []string{"docker.io/istio/proxyv2:1.20.1", "harbor.corp/pay/api:2.4.0"}
	if got := RolloutRowFromV2(e); got.Image != "harbor.corp/pay/api" || got.ImageTag != "2.5.0" ||
		got.PrevImage != "harbor.corp/pay/api" || got.PrevImageTag != "2.4.0" {
		t.Fatalf("sidecar önde: %q %q %q %q", got.Image, got.ImageTag, got.PrevImage, got.PrevImageTag)
	}
}

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// v1 satırının anahtar kümesi v0.10.244'ten beri bu — P2.3 v1 cevabını
// değiştirmez (lib/types.ts WorkloadRollout zorunlu alanları).
var rolloutRowV1Keys = []string{"clusterId", "completedAt", "detectedBy", "firstSpanAt", "image", "imageTag", "kind",
	"ksmNotReadySince", "ksmStartedAt", "namespace", "note", "podsReadyAt", "prevImage", "prevImageTag", "prevRevision",
	"problemsCaused", "revision", "spanCount", "startedAt", "status", "trafficConfirmedAt", "updatedAt", "workload"}

func TestRolloutRowJSONV1UnchangedV2Additive(t *testing.T) {
	v1 := RolloutRow{Rollout: rollout.Rollout{ClusterID: "c", Workload: "w", Revision: "r", StartedAt: time.Unix(10, 0)}}
	if got := jsonKeys(t, v1); !reflect.DeepEqual(got, rolloutRowV1Keys) {
		t.Fatalf("v1 JSON anahtarları değişmiş:\n got %v\nwant %v", got, rolloutRowV1Keys)
	}
	v2 := RolloutRowFromV2(rollout.V2Event{ClusterID: "c", Namespace: "n", WorkloadKind: "Deployment", Workload: "w",
		IncarnationAt: time.Unix(60, 0), Generation: 2, StartedAt: time.Unix(120, 0), Status: "progressing", ChangeType: "rollout",
		UpdatedAt: time.Unix(130, 0), Version: 1})
	got := jsonKeys(t, v2)
	have := map[string]bool{}
	for _, k := range got {
		have[k] = true
	}
	for _, k := range rolloutRowV1Keys {
		if !have[k] {
			t.Errorf("v2 satırı v1 anahtarını düşürmüş: %s", k)
		}
	}
	for _, k := range []string{"incarnationAt", "generation", "v2Status", "changeType", "images", "prevImages", "versionTag",
		"stuckReason", "specReplicas", "updatedReplicas", "availableReplicas", "observedGeneration", "succeededAt", "stuckAt", "finishedAt"} {
		if !have[k] {
			t.Errorf("v2 anahtarı eksik: %s", k)
		}
	}
	b, _ := json.Marshal(v2)
	if !strings.Contains(string(b), `"incarnationAt":60000`) || !strings.Contains(string(b), `"images":[]`) || !strings.Contains(string(b), `"succeededAt":0`) {
		t.Fatalf("v2 değerleri (ms, [] , sıfır zaman 0): %s", b)
	}
}
