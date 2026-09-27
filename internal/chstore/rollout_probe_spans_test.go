package chstore

// rollout_probe_spans_test.go — v0.10.979 — Rollouts v2 §11.8 T paketi SQL
// çivileri: örneklemeli ve sınırlı (zaman-sınırlı WHERE, servis × zaman
// dilimi kotası, dış LIMIT, GROUP BY, ORDER BY, LIMIT 50, max_execution_time
// = 20), T0 fallback ifade takası, fallback tetikleyicisi, Scan sırası ↔
// SELECT sırası, iç seçimde yalnız gereken kolon (T2 res_keys okumaz;
// res_values yalnız fallback'te).

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRolloutProbeCoverageSQLBounded(t *testing.T) {
	q := flatWSCH(rolloutProbeCoverageSQL(900, rolloutProbeClusterCol))
	for _, must := range []string{
		"SELECT cluster AS cluster, res_keys, service_name FROM spans",
		"WHERE time >= ? AND time <= ?",
		// 900 s → 12 dilim × 75 s, dilim başına ⌈200/12⌉ = 17 (sample_slices.go).
		"LIMIT 17 BY service_name, toStartOfInterval(time, INTERVAL 75 SECOND)",
		"LIMIT 200000 )",
		"GROUP BY cluster",
		"ORDER BY sampled DESC",
		"LIMIT 50",
		"SETTINGS max_execution_time = 20",
		"countIf(has(res_keys, 'service.version')) AS svc_version",
		"countIf(has(res_keys, 'container.image.tag') OR has(res_keys, 'k8s.container.image.tag')) AS img_tag",
		"countIf(has(res_keys, 'container.id')) AS container_id",
		"countIf(has(res_keys, 'k8s.deployment.name')) AS depl",
		"countIf(has(res_keys, 'k8s.replicaset.name')) AS rs",
		"countIf(has(res_keys, 'k8s.statefulset.name')) AS sts",
		"countIf(has(res_keys, 'k8s.daemonset.name')) AS ds",
		"countIf(has(res_keys, 'deployment.environment.name')) AS env_name",
		"countIf(has(res_keys, 'k8s.cluster.name')) AS k8s_cluster",
		"countIf(has(res_keys, 'openshift.cluster.name')) AS ocp_cluster",
	} {
		if !strings.Contains(q, must) {
			t.Errorf("T1 sorgusunda %q yok:\n%s", must, q)
		}
	}
	if strings.Contains(q, "res_values") {
		t.Errorf("T1 birincil yolu res_values okumamalı:\n%s", q)
	}
	if strings.Contains(q, "coremetry.spans") {
		t.Error("telemetri tablosu niteliksiz anılmalı")
	}
}

func TestRolloutProbeEnvSQLBounded(t *testing.T) {
	q := flatWSCH(rolloutProbeEnvSQL(900, rolloutProbeClusterCol))
	for _, must := range []string{
		"SELECT deploy_env, cluster, count() AS n",
		"SELECT cluster AS cluster, deploy_env, service_name FROM spans",
		"WHERE time >= ? AND time <= ?",
		"LIMIT 17 BY service_name, toStartOfInterval(time, INTERVAL 75 SECOND)",
		"LIMIT 200000 )",
		"GROUP BY deploy_env, cluster",
		"ORDER BY n DESC",
		"LIMIT 50",
		"SETTINGS max_execution_time = 20",
	} {
		if !strings.Contains(q, must) {
			t.Errorf("T2 sorgusunda %q yok:\n%s", must, q)
		}
	}
	if strings.Contains(q, "res_keys") {
		t.Errorf("T2 iç seçimi res_keys okumamalı (kalın dizi kolonu):\n%s", q)
	}
}

func TestRolloutProbeFallbackSwap(t *testing.T) {
	q := flatWSCH(rolloutProbeCoverageSQL(900, rolloutProbeClusterFallback))
	if !strings.Contains(q, "SELECT res_values[indexOf(res_keys, 'k8s.cluster.name')] AS cluster, res_keys, service_name FROM spans") {
		t.Errorf("fallback ifadesi iç seçime girmeli:\n%s", q)
	}
	if strings.Contains(q, "SELECT cluster AS cluster") {
		t.Error("fallback'te düz cluster kolonu kalmamalı")
	}
	q2 := flatWSCH(rolloutProbeEnvSQL(900, rolloutProbeClusterFallback))
	if !strings.Contains(q2, "SELECT res_values[indexOf(res_keys, 'k8s.cluster.name')] AS cluster, deploy_env, service_name FROM spans") {
		t.Errorf("T2 fallback:\n%s", q2)
	}
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("code: 47, message: Missing columns: 'cluster' while processing query"), true},
		{errors.New("code: 47, message: Unknown identifier: cluster"), true},
		{errors.New("code: 47, message: Missing columns: 'deploy_env'"), false},
		{errors.New("code: 60, message: Table spans does not exist"), false},
		{errors.New("code: 159, message: Timeout exceeded: elapsed 20.1 seconds"), false},
	}
	for _, c := range cases {
		if got := rolloutProbeNeedsFallback(c.err); got != c.want {
			t.Errorf("needsFallback(%v) = %v, beklenen %v", c.err, got, c.want)
		}
	}
}

func TestRolloutProbeScanOrderMatchesSelect(t *testing.T) {
	var r RolloutProbeClusterRow
	targets := rolloutProbeCoverageScanTargets(&r)
	wantFields := []string{"Cluster", "Sampled", "SvcVersion", "ImgTag", "ContainerID", "Depl", "RS", "STS", "DS", "EnvName", "K8sCluster", "OcpCluster"}
	if len(targets) != len(wantFields) {
		t.Fatalf("%d hedef, %d alan", len(targets), len(wantFields))
	}
	rv := reflect.ValueOf(&r).Elem()
	for i, f := range wantFields {
		if targets[i] != rv.FieldByName(f).Addr().Interface() {
			t.Errorf("hedef %d %s alanına işaret etmiyor", i, f)
		}
	}
	// SELECT takma ad sırası aynı.
	q := flatWSCH(rolloutProbeCoverageSQL(900, rolloutProbeClusterCol))
	aliases := []string{"AS sampled", "AS svc_version", "AS img_tag", "AS container_id", "AS depl", "AS rs", "AS sts", "AS ds", "AS env_name", "AS k8s_cluster", "AS ocp_cluster"}
	last := -1
	for _, a := range aliases {
		i := strings.Index(q, a)
		if i < 0 || i < last {
			t.Errorf("takma ad sırası bozuk: %s", a)
		}
		last = i
	}
}
