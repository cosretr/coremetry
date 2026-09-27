package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// rollout_probe_spans.go — v0.10.979 — ROLLOUTS v2 §11.8 T PAKETİ (admin
// probe; docs/rollouts/v2-audit.md §11.8 T1/T2/T0, spec §13).
//
// ── NE ───────────────────────────────────────────────────────────────────
//
// T1: span cluster değeri başına öznitelik VARLIĞI (res_keys has()) — 15 dk
// örneklem, ≤50 satır. T2: deploy_env × cluster biçimi (türetilen cluster
// formu görünür mü) — aynı örneklem. T0: prod `spans`'ta `cluster` kolonu yoksa
// (0011 uygulanmamış) tek seferlik yeniden koşum: `cluster` yerine
// `res_values[indexOf(res_keys, 'k8s.cluster.name')]`, FallbackUsed=true.
//
// ── NEDEN MV DEĞİL, HAM spans (MV-first invariant #3 ile uyum) ─────────
//
// Bu bir AGREGAT değil, sınırlı bir örneklem üzerinde öznitelik-VARLIĞI
// probe'udur — Admin › K8s Coverage kartıyla (k8s_coverage.go) aynı sınıf ve
// aynı örnekleme kalıbı: zaman-sınırlı WHERE, `LIMIT n BY service_name,
// toStartOfInterval(...)` (sample_slices.go: kota zaman dilimine bölünür ki
// birincil anahtar öneki = pencerenin ilk saniyeleri örneklenmesin), dış
// LIMIT 200 000, SETTINGS max_execution_time = 20. Hiçbir MV res_keys taşımaz;
// soru "anahtar GELİYOR MU"dur, değeri ne değil. Maliyet O(örneklem).
//
// ── NEDEN İKİ AYRI İÇ SEÇİM ──────────────────────────────────────────────
//
// T1'in içi yalnız cluster + res_keys, T2'ninki yalnız cluster + deploy_env:
// res_keys kalın bir dizi kolonu; T2 için okumak baytı katlar. res_values
// yalnız T0 fallback'inde okunur (audit'in kendi yedek ifadesi).
//
// Zaman argümanları GetK8sCoverage gibi time.Time olarak bağlanır (tz-less
// sözleşme: `time >= ? AND time <= ?`, DateTime64 dönüşümü sürücüde).

const (
	rolloutProbeSampleRows = 200_000
	rolloutProbePerService = 200 // §11.8 T1: LIMIT 200 BY service_name (dilimlere bölünür)
	rolloutProbeMaxRows    = 50
	rolloutProbeWindowSec  = int64(900)

	rolloutProbeClusterCol      = "cluster"
	rolloutProbeClusterFallback = "res_values[indexOf(res_keys, 'k8s.cluster.name')]"
)

// RolloutProbeClusterRow — T1 satırı: cluster değeri + sayaçlar.
type RolloutProbeClusterRow struct {
	Cluster     string `json:"cluster"`
	Sampled     uint64 `json:"sampled"`
	SvcVersion  uint64 `json:"svcVersion"`
	ImgTag      uint64 `json:"imgTag"`
	ContainerID uint64 `json:"containerId"`
	Depl        uint64 `json:"depl"`
	RS          uint64 `json:"rs"`
	STS         uint64 `json:"sts"`
	DS          uint64 `json:"ds"`
	EnvName     uint64 `json:"envName"`
	K8sCluster  uint64 `json:"k8sCluster"`
	OcpCluster  uint64 `json:"ocpCluster"`
}

// RolloutProbeEnvRow — T2 satırı.
type RolloutProbeEnvRow struct {
	DeployEnv string `json:"deployEnv"`
	Cluster   string `json:"cluster"`
	N         uint64 `json:"n"`
}

// RolloutProbeSpans — T1 + T2 (+ T0 bayrağı).
type RolloutProbeSpans struct {
	Coverage     []RolloutProbeClusterRow `json:"coverage"`
	Env          []RolloutProbeEnvRow     `json:"env"`
	WindowSec    int64                    `json:"windowSec"`
	SampleRows   int                      `json:"sampleRows"`
	FallbackUsed bool                     `json:"fallbackUsed,omitempty"`
}

// rolloutProbeInner — ortak iç örneklem (SAF): seçilen kolonlar çağırana göre.
func rolloutProbeInner(windowSec int64, clusterExpr, extraCols string) string {
	bucketSec, perBucket := sampleSlices(windowSec, rolloutProbePerService)
	return fmt.Sprintf(`
			SELECT %s AS cluster, %s, service_name FROM spans
			WHERE time >= ? AND time <= ?
			LIMIT %d BY service_name, toStartOfInterval(time, INTERVAL %d SECOND)
			LIMIT %d`, clusterExpr, extraCols, perBucket, bucketSec, rolloutProbeSampleRows)
}

// rolloutProbeCoverageSQL — T1 (SAF; rollout_probe_spans_test.go pinler).
func rolloutProbeCoverageSQL(windowSec int64, clusterExpr string) string {
	return fmt.Sprintf(`
		SELECT cluster,
		       count()                                               AS sampled,
		       countIf(has(res_keys, 'service.version'))              AS svc_version,
		       countIf(%s) AS img_tag,
		       countIf(has(res_keys, 'container.id'))                 AS container_id,
		       countIf(has(res_keys, 'k8s.deployment.name'))          AS depl,
		       countIf(has(res_keys, 'k8s.replicaset.name'))          AS rs,
		       countIf(has(res_keys, 'k8s.statefulset.name'))         AS sts,
		       countIf(has(res_keys, 'k8s.daemonset.name'))           AS ds,
		       countIf(has(res_keys, 'deployment.environment.name'))  AS env_name,
		       countIf(has(res_keys, 'k8s.cluster.name'))             AS k8s_cluster,
		       countIf(has(res_keys, 'openshift.cluster.name'))       AS ocp_cluster
		FROM (%s
		)
		GROUP BY cluster
		ORDER BY sampled DESC
		LIMIT %d
		SETTINGS max_execution_time = 20`,
		k8sCoverageImageTagExpr, rolloutProbeInner(windowSec, clusterExpr, "res_keys"), rolloutProbeMaxRows)
}

// rolloutProbeEnvSQL — T2 (SAF).
func rolloutProbeEnvSQL(windowSec int64, clusterExpr string) string {
	return fmt.Sprintf(`
		SELECT deploy_env, cluster, count() AS n
		FROM (%s
		)
		GROUP BY deploy_env, cluster
		ORDER BY n DESC
		LIMIT %d
		SETTINGS max_execution_time = 20`,
		rolloutProbeInner(windowSec, clusterExpr, "deploy_env"), rolloutProbeMaxRows)
}

// rolloutProbeCoverageScanTargets — Scan hedefleri, SELECT sırasıyla (SAF,
// testle çivili: bir kayma sayacı yanlış alana yazar ve sayılar makul
// göründüğü için sessiz kalır — k8sCoverageScanTargets dersi).
func rolloutProbeCoverageScanTargets(r *RolloutProbeClusterRow) []any {
	return []any{&r.Cluster, &r.Sampled, &r.SvcVersion, &r.ImgTag, &r.ContainerID,
		&r.Depl, &r.RS, &r.STS, &r.DS, &r.EnvName, &r.K8sCluster, &r.OcpCluster}
}

// rolloutProbeNeedsFallback — `cluster` kolonu yok (0011 uygulanmamış): CH
// "Missing columns: 'cluster'" / "Unknown identifier: cluster" (code 47).
// SAF.
func rolloutProbeNeedsFallback(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	if !strings.Contains(m, "cluster") {
		return false
	}
	return strings.Contains(m, "Missing columns") || strings.Contains(m, "Unknown identifier") || strings.Contains(m, "code: 47")
}

// RolloutProbeSpanCoverage — T1 + T2; `cluster` kolonu yoksa T0 ile bir kez
// daha (FallbackUsed). Pencere çağıranın (probe 15 dk verir); sınırlar
// SQL'de sabit.
func (s *Store) RolloutProbeSpanCoverage(ctx context.Context, from, to time.Time) (*RolloutProbeSpans, error) {
	windowSec := int64(to.Sub(from).Seconds())
	if windowSec <= 0 {
		windowSec = rolloutProbeWindowSec
	}
	out := &RolloutProbeSpans{Coverage: []RolloutProbeClusterRow{}, Env: []RolloutProbeEnvRow{},
		WindowSec: windowSec, SampleRows: rolloutProbeSampleRows}
	expr := rolloutProbeClusterCol
	cov, err := s.rolloutProbeCoverage(ctx, windowSec, expr, from, to)
	if err != nil && rolloutProbeNeedsFallback(err) {
		expr = rolloutProbeClusterFallback
		out.FallbackUsed = true
		cov, err = s.rolloutProbeCoverage(ctx, windowSec, expr, from, to)
	}
	if err != nil {
		return nil, fmt.Errorf("rollout probe T1: %w", err)
	}
	out.Coverage = cov
	env, err := s.rolloutProbeEnv(ctx, windowSec, expr, from, to)
	if err != nil {
		return nil, fmt.Errorf("rollout probe T2: %w", err)
	}
	out.Env = env
	return out, nil
}

func (s *Store) rolloutProbeCoverage(ctx context.Context, windowSec int64, expr string, from, to time.Time) ([]RolloutProbeClusterRow, error) {
	rows, err := s.telemetryReadConn().Query(ctx, rolloutProbeCoverageSQL(windowSec, expr), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RolloutProbeClusterRow{}
	for rows.Next() {
		var r RolloutProbeClusterRow
		if err := rows.Scan(rolloutProbeCoverageScanTargets(&r)...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) rolloutProbeEnv(ctx context.Context, windowSec int64, expr string, from, to time.Time) ([]RolloutProbeEnvRow, error) {
	rows, err := s.telemetryReadConn().Query(ctx, rolloutProbeEnvSQL(windowSec, expr), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RolloutProbeEnvRow{}
	for rows.Next() {
		var r RolloutProbeEnvRow
		if err := rows.Scan(&r.DeployEnv, &r.Cluster, &r.N); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
