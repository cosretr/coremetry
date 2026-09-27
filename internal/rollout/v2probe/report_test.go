package v2probe

// report_test.go — v0.10.979 — golden rapor (testdata/golden_report.md;
// `go test ./internal/rollout/v2probe -run TestRenderGolden -update` yeniler;
// cluster_matcher_test.go v0.10.950 emsali) + sızıntı testi: sentetik
// fixture'daki nöbetçi ham değerler (gerçek küme adı, API host'u, takım adı,
// bilinmeyen etiket) ne markdown'da ne Result alanlarında görünür.
//
// Fixture: iki hedef (cluster-a namespace filtreli; cluster-b K0.2'den sonra
// 401 ile erken durmuş), iki hub (hub-1 Thanos etiketli → matcher'sız geçiş;
// hub-2 URL başına, bir timeout + bir truncated + H5.1a bad_data → H5.2a
// fallback), T paketi T0 fallback notuyla, bütçe notu.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

var update = flag.Bool("update", false, "golden dosyayı yeniden yaz")

// Nöbetçiler — hiçbir çıktıda görünmemeli.
var sentinels = []string{
	"realcluster-prod-01", "realcluster-prod-02", "ocp-prod-01", "hubraw", "hub-real-01", "hub-real-02",
	"api.real.example.invalid", "api.real2.example.invalid", "api.unknown.example.invalid",
	"team-payments", "team-billing", "openshift-gitops", "checkout-real-api", "db.internal.example.invalid",
	"c-1a2b3c4d", "c-2b3c4d5e", "c-9f8e7d6c", "c-8e7d6c5b", "thanos.realcluster",
}

func fixtureSeeds() Seeds {
	return Seeds{
		Clusters: []Alias{
			{Token: "cluster-a", Raw: []string{"c-1a2b3c4d", "realcluster-prod-01", "ocp-prod-01"}},
			{Token: "cluster-b", Raw: []string{"c-2b3c4d5e", "realcluster-prod-02"}},
			{Token: "hub-1", Raw: []string{"c-9f8e7d6c", "hubraw", "hub-real-01"}},
			{Token: "hub-2", Raw: []string{"c-8e7d6c5b", "hub-real-02"}},
		},
		APIHosts: map[string]string{
			"api.real.example.invalid:6443": "cluster-a", "api.real.example.invalid": "cluster-a",
			"api.real2.example.invalid:6443": "cluster-b", "api.real2.example.invalid": "cluster-b",
		},
		EnvList: []string{"dev", "test", "prod"}, SuffixList: []string{"ocpa", "ocpb"},
	}
}

func row(v string, kv ...string) Row {
	l := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		l[kv[i]] = kv[i+1]
	}
	return Row{Labels: l, Value: v}
}

var fixtureScalars = map[string]string{
	"K0.1": "180", "K0.4": "1", "K0.5": "10", "K0.6": "42", "K0.6b": "1784271060",
	"K2.2a": "900", "K2.2b": "900", "K2.2c": "900", "K2.2d": "0", "K2.2e": "850",
	"K2.4a": "870", "K2.4b": "30", "K2.4c": "0", "K2.4d": "12", "K2.4e": "500", "K2.4f": "480", "K2.4g": "0",
	"K3.1": "1200", "K3.2": "900", "K3.3": "300", "K3.4": "160", "K3.5": "20", "K3.6": "30", "K3.7": "3000",
	"K4.1": "2", "K4.2": "1", "K4.3": "0", "K4.4": "1",
	"K5.1": "40", "K5.2": "6", "K5.3": "10", "K5.4": "1", "K5.5": "2", "K5.6": "0",
	"K6.1": "1200", "K6.2": "900", "K6.3": "900",
	"D1": "12", "D2": "10", "D3": "4", "D4": "30", "D7": "1", "D8": "150",
	"H0.1": "40000", "H0.4": "40000", "H1.6a": "9000", "H1.6b": "3000", "H1.6c": "0",
	"H2.2": "5", "H2.3": "5", "H2.4": "100", "H2.6": "1200", "H2.8": "3",
	"H3.1": "4", "H3.3": "2", "H3.5": "1",
	"H5.3a": "40000", "H5.3b": "40000", "H5.3c": "0", "H5.3d": "40000",
	"H5.4a": "38000", "H5.4b": "40", "H5.4c": "120", "H5.4d": "5", "H5.4e": "1100", "H5.4f": "3", "H5.4g": "3",
	"H6.1": "39000", "H6.4": "3", "H6.5": "12", "H6.6": "1", "H6.7": "140",
	"N1": "0.93", "N3a": "120", "N3b": "40", "N3c": "300", "L1": "2800",
}

var nomatchScalars = map[string]string{"H0.1": "41000", "H0.4": "41000", "H1.6a": "9000", "H1.6b": "3000", "H1.6c": "0"}

func fixtureRows(id, variant string) ([]Row, bool) {
	switch id {
	case "K0.2":
		return []Row{row("1", "job", "kube-state-metrics"), row("1", "job", "openshift-state-metrics")}, true
	case "K0.3":
		return []Row{row("1", "version", "v2.13.0")}, true
	case "K1.D":
		return nameRows("kube_deployment_metadata_generation", "kube_deployment_status_observed_generation", "kube_deployment_spec_replicas", "kube_deployment_status_replicas",
			"kube_deployment_status_replicas_updated", "kube_deployment_status_replicas_available", "kube_deployment_status_replicas_ready", "kube_deployment_status_replicas_unavailable",
			"kube_deployment_status_condition", "kube_deployment_spec_paused", "kube_deployment_labels"), true
	case "K1.R":
		return nameRows("kube_replicaset_owner", "kube_replicaset_spec_replicas", "kube_replicaset_status_replicas", "kube_replicaset_status_ready_replicas", "kube_replicaset_labels"), true
	case "K1.P":
		return nameRows("kube_pod_container_info", "kube_pod_owner", "kube_pod_labels", "kube_pod_info", "kube_pod_start_time"), true
	case "K1.S":
		return nameRows("kube_statefulset_metadata_generation", "kube_statefulset_status_observed_generation", "kube_statefulset_replicas", "kube_statefulset_status_replicas",
			"kube_statefulset_status_replicas_updated", "kube_statefulset_status_replicas_ready", "kube_statefulset_status_replicas_available", "kube_statefulset_status_replicas_current",
			"kube_statefulset_status_current_revision", "kube_statefulset_status_update_revision"), true
	case "K1.DS":
		return nameRows("kube_daemonset_metadata_generation", "kube_daemonset_status_observed_generation", "kube_daemonset_status_desired_number_scheduled",
			"kube_daemonset_status_updated_number_scheduled", "kube_daemonset_status_number_available", "kube_daemonset_status_number_unavailable",
			"kube_daemonset_status_number_ready", "kube_daemonset_status_current_number_scheduled"), true
	case "K1.H":
		return []Row{row("8", "scaletargetref_kind", "Deployment"), row("1", "scaletargetref_kind", "StatefulSet")}, true
	case "K2.3a":
		return []Row{row("1150", "owner_kind", "Deployment", "owner_is_controller", "true"), row("40", "owner_kind", "<none>", "owner_is_controller", "")}, true
	case "K2.3b":
		return []Row{row("800", "owner_kind", "ReplicaSet"), row("30", "owner_kind", "DaemonSet"), row("20", "owner_kind", "StatefulSet"), row("30", "owner_kind", "ReplicationController")}, true
	case "K2.3c":
		return []Row{row("158", "condition", "Available", "status", "true", "reason", "MinimumReplicasAvailable"), row("156", "condition", "Progressing", "status", "true", "reason", "NewReplicaSetAvailable"),
			row("2", "condition", "Progressing", "status", "false", "reason", "ProgressDeadlineExceeded")}, true
	case "D5":
		return []Row{}, true
	case "D6":
		return nameRows("openshift_deploymentconfig_spec_replicas", "openshift_deploymentconfig_metadata_generation", "kube_replicationcontroller_owner"), true
	case "R1", "R3", "H1.3a", "H1.3b", "N6", "N7":
		return []Row{}, true
	case "H0.2":
		if variant == "dedup_off" {
			return []Row{row("40000", "prometheus", "openshift-monitoring/k8s", "prometheus_replica", "prometheus-k8s-0"), row("40000", "prometheus", "openshift-monitoring/k8s", "prometheus_replica", "prometheus-k8s-1")}, true
		}
		return []Row{row("40000", "prometheus", "openshift-monitoring/k8s", "prometheus_replica", "")}, true
	case "H0.3":
		// v0.10.979 — k8s_cluster / cluster_name nöbetçili: küme kimliği sınıfı.
		rows := []Row{row("40000", "cluster", "hubraw", "prometheus", "openshift-monitoring/k8s", "k8s_cluster", "realcluster-prod-01", "cluster_name", "hub-real-01")}
		if variant == "nomatch" {
			rows = append(rows, row("1000", "cluster", "", "prometheus", "openshift-monitoring/k8s", "secret_host", "db.internal.example.invalid"))
		}
		return rows, true
	case "H0.5":
		return []Row{row("12", "cluster", "hubraw", "prometheus", "openshift-monitoring/k8s", "tenant", "hub-real-01",
			"k8s_cluster", "hub-real-02", "openshift_cluster", "ocp-prod-01", "tenant_id", "realcluster-prod-02")}, true
	case "H1.1", "H1.5":
		return []Row{row("12000", "namespace", "team-payments-prod", "job", "team-payments-prod-metrics"), row("9000", "namespace", "team-billing-dev", "job", "team-billing-dev-metrics"),
			row("400", "namespace", "openshift-gitops", "job", "openshift-gitops-metrics")}, true
	case "H1.2":
		return []Row{row("12000", "namespace", "team-payments-prod", "exported_namespace", "team-payments-prod", "job", "team-payments-prod-metrics"),
			row("9000", "namespace", "team-billing-dev", "exported_namespace", "", "job", "team-billing-dev-metrics"),
			row("400", "namespace", "openshift-gitops", "exported_namespace", "argocd-apps", "job", "openshift-gitops-metrics")}, true
	case "H1.4":
		return []Row{row("2", "namespace", "team-payments-prod"), row("1", "namespace", "team-billing-dev"), row("1", "namespace", "openshift-gitops")}, true
	case "H2.1":
		return []Row{row("20000", "dest_server", "https://api.real.example.invalid:6443"), row("18000", "dest_server", "https://api.real2.example.invalid:6443"),
			row("100", "dest_server", "https://kubernetes.default.svc"), row("50", "dest_server", "https://api.unknown.example.invalid:6443"), row("5", "dest_server", "")}, true
	case "H2.5":
		return []Row{row("4", "port", "6443")}, true
	case "H2.7":
		return []Row{row("3000", "apps_per_target_ns", "1"), row("500", "apps_per_target_ns", "2")}, true
	case "H3.2":
		return []Row{row("38000", "same_name_same_server", "1"), row("4", "same_name_same_server", "2")}, true
	case "H3.4":
		return []Row{row("17000", "clusters_per_base", "2"), row("2000", "clusters_per_base", "1")}, true
	case "H4.1":
		return []Row{row("37000", "sync_status", "Synced", "health_status", "Healthy"), row("2000", "sync_status", "OutOfSync", "health_status", "Healthy"), row("140", "sync_status", "Synced", "health_status", "Degraded")}, true
	case "H4.2":
		return []Row{row("39900", "operation", ""), row("100", "operation", "sync")}, true
	case "H4.3":
		return []Row{row("30000", "autosync_enabled", "true"), row("10000", "autosync_enabled", "false")}, true
	case "H4.4":
		return []Row{row("12000", "namespace", "team-payments-prod", "autosync_enabled", "true"), row("9000", "namespace", "team-billing-dev", "autosync_enabled", "false")}, true
	case "H5.3e":
		return []Row{row("1", "namespace", "team-payments-prod", "version", "v2.13.3"), row("1", "namespace", "team-billing-dev", "version", "v2.11.0")}, true
	case "H6.2a":
		return []Row{row("120", "phase", "Succeeded"), row("3", "phase", "Failed")}, true
	case "H6.2b":
		return []Row{row("2900", "phase", "Succeeded"), row("40", "phase", "Failed")}, true
	case "H6.3":
		return []Row{row("2500", "autosync_enabled", "true"), row("440", "autosync_enabled", "false")}, true
	case "N2":
		return []Row{row("0.98", "namespace", "team-payments-prod"), row("0.9", "namespace", "team-billing-dev"), row("0", "namespace", "openshift-gitops")}, true
	case "N4":
		return []Row{row("300", "sfx", "zzz"), row("120", "sfx", "ocpa"), row("40", "sfx", "legacy")}, true
	case "N5a":
		return []Row{row("19000", "dest_server", "https://api.real.example.invalid:6443", "sfx", "ocpa"), row("17500", "dest_server", "https://api.real2.example.invalid:6443", "sfx", "ocpb")}, true
	case "N5b":
		return []Row{row("0.95", "dest_server", "https://api.real.example.invalid:6443"), row("0.97", "dest_server", "https://api.real2.example.invalid:6443")}, true
	case "L2":
		return []Row{row("2000", "namespace", "team-payments-prod", "project", "payments"), row("800", "namespace", "openshift-gitops", "project", "default")}, true
	case "T1":
		return []Row{row("120000", "cluster", "realcluster-prod-01", "sampled", "120000", "svc_version", "5000", "img_tag", "118000", "container_id", "119000", "depl", "119500", "rs", "119500", "sts", "300", "ds", "200", "env_name", "0", "k8s_cluster", "120000", "ocp_cluster", "0"),
			row("80000", "cluster", "realcluster-prod-02", "sampled", "80000", "svc_version", "4000", "img_tag", "79000", "container_id", "79500", "depl", "79800", "rs", "79800", "sts", "100", "ds", "100", "env_name", "0", "k8s_cluster", "80000", "ocp_cluster", "0"),
			row("300", "cluster", "", "sampled", "300", "svc_version", "0", "img_tag", "0", "container_id", "0", "depl", "0", "rs", "0", "sts", "0", "ds", "0", "env_name", "0", "k8s_cluster", "0", "ocp_cluster", "0")}, true
	case "T2":
		return []Row{row("120000", "deploy_env", "prod-realcluster-prod-01", "cluster", "realcluster-prod-01"), row("80000", "deploy_env", "prod-realcluster-prod-02", "cluster", "realcluster-prod-02"), row("300", "deploy_env", "prod", "cluster", "")}, true
	case "T3":
		return []Row{row("300", "span_cluster", "legacy-cluster-name", "owner", "")}, true
	}
	return nil, false
}

func fixtureNames(id string) ([]string, bool) {
	switch id {
	case "K1.X":
		return []string{"kube_daemonset_metadata_generation", "kube_deployment_metadata_generation", "kube_replicaset_owner", "kube_statefulset_metadata_generation"}, true
	case "K2.1a":
		return []string{"container", "container_id", "image", "image_id", "image_spec", "namespace", "pod"}, true
	case "K2.1b":
		return []string{"namespace", "owner_is_controller", "owner_kind", "owner_name", "replicaset"}, true
	case "K2.1c":
		return []string{"condition", "deployment", "namespace", "reason", "status"}, true
	case "K2.1d":
		return []string{"namespace", "revision", "statefulset"}, true
	case "K2.1e":
		return []string{"deployment", "label_app", "namespace"}, true
	case "R0":
		return []string{}, true
	case "H5.1a", "H5.2a":
		return []string{"autosync_enabled", "dest_namespace", "dest_server", "exported_namespace", "health_status", "job", "name", "namespace", "operation", "project", "repo", "sync_status"}, true
	case "H5.1b", "H5.2b":
		return []string{"dest_server", "exported_namespace", "job", "name", "namespace", "phase", "project"}, true
	case "H5.1c":
		return []string{"job", "k8s_version", "name", "namespace", "server"}, true
	case "H5.1d":
		return []string{"job", "name", "namespace"}, true
	}
	return nil, false
}

// fixtureResult — bir instance'ın ham sonucu.
func fixtureResult(in Instance, unit string, at time.Time) Result {
	res := Result{ID: in.ID, Unit: unit, Variant: in.Variant, VariantRaw: in.VariantRaw, State: sourcestate.OK, SampleAt: at, DurationMs: 100 + int64(len(in.ID))*7}
	if names, ok := fixtureNames(in.ID); ok {
		res.Names = names
		if len(names) == 0 {
			res.State = sourcestate.Empty
		}
		return res
	}
	if rows, ok := fixtureRows(in.ID, in.Variant); ok {
		res.Rows = rows
		res.Total = len(rows)
		if len(rows) == 0 {
			res.State = sourcestate.Empty
		}
		return res
	}
	v, ok := fixtureScalars[in.ID]
	if in.Variant == "nomatch" {
		if nv, ok2 := nomatchScalars[in.ID]; ok2 {
			v = nv
		}
	}
	if in.ID == "K0.4" && in.Variant == "dedup_off" {
		v = "2"
	}
	if !ok {
		res.State = sourcestate.Empty
		res.Rows = []Row{}
		return res
	}
	res.Scalar = &v
	return res
}

func fixtureRun() RawRun {
	start := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	targets := []RawUnit{
		{ID: "c-1a2b3c4d", Token: "cluster-a", Role: "target", NSFilter: true, Calls: 63, DurationMs: 41211, State: "ok"},
		{ID: "c-2b3c4d5e", Token: "cluster-b", Role: "target", Calls: 2, DurationMs: 900, State: "unauthorized", EarlyStop: "unauthorized streak after K0.2 (thanos rejected the cluster credentials for realcluster-prod-02, HTTP 403)"},
	}
	hubs := []RawUnit{
		{ID: "c-9f8e7d6c", Token: "hub-1", Role: "hub", WithoutMatcher: true, Calls: 104, DurationMs: 92300, State: "ok"},
		{ID: "c-8e7d6c5b", Token: "hub-2", Role: "hub", Calls: 88, DurationMs: 70000, State: "partial"},
	}
	run := RawRun{RunID: "a1b2c3d4e5f6", Pod: "coremetry-api-0", Status: "done", StartedAt: start, FinishedAt: start.Add(171204 * time.Millisecond),
		BudgetS: 300, Calls: 318, Planned: 331, Packs: AllPacks, EnvListN: 3, SuffixListN: 2,
		Units:    append([]RawUnit{{ID: ClickHouseUnit, Token: ClickHouseUnit, Role: ClickHouseUnit, Calls: 4, DurationMs: 2400, State: "ok"}}, append(targets, hubs...)...),
		Warnings: []string{"PromQL warning: store realcluster-prod-01 partial at https://thanos.realcluster-prod-01.example.invalid:9090"},
		Skipped:  []string{"cluster-b (realcluster-prod-02): K0.3–R3 skipped: unit unauthorized (streak)", "budget 300 s: not exhausted"},
	}
	at := start
	tick := func() time.Time { at = at.Add(500 * time.Millisecond); return at }
	// T
	for _, q := range ByPack(PackT) {
		r := fixtureResult(Instance{Query: q, Expr: q.Expr}, ClickHouseUnit, tick())
		if q.ID == "T3" {
			r.Detail = "unmapped=1 total=3"
		}
		run.Results = append(run.Results, r)
	}
	run.Results = append(run.Results, Result{ID: "T1", Unit: ClickHouseUnit, Variant: "fallback", State: sourcestate.OK, Detail: "T0 fallback: `cluster` column missing — 0011 not applied", SampleAt: tick(), DurationMs: 800,
		Rows: []Row{row("120000", "cluster", "realcluster-prod-01", "sampled", "120000", "svc_version", "5000", "img_tag", "118000", "container_id", "119000", "depl", "119500", "rs", "119500", "sts", "300", "ds", "200", "env_name", "0", "k8s_cluster", "120000", "ocp_cluster", "0")}, Total: 1})
	// Hedefler
	for i, t := range targets {
		p := Params{K5Window: "6h"}
		if t.NSFilter {
			p.NSMatcher = `,namespace=~"team-.*"`
		}
		stop := false
		var second *Result
		for _, pack := range []Pack{PackK, PackD, PackR} {
			for _, q := range ByPack(pack) {
				insts, _ := Expand(q, p)
				for _, in := range insts {
					if in.Variant == "second_sample" {
						r := fixtureResult(in, t.ID, tick().Add(31*time.Second))
						second = &r
						continue
					}
					switch {
					case i == 1 && (q.ID == "K0.1" || q.ID == "K0.2"):
						run.Results = append(run.Results, Result{ID: q.ID, Unit: t.ID, Variant: in.Variant, State: sourcestate.Unauthorized, Detail: "thanos rejected the cluster credentials for realcluster-prod-02 (HTTP 403)", SampleAt: tick(), DurationMs: 40})
						if q.ID == "K0.2" {
							stop = true
						}
					case stop:
						run.Results = append(run.Results, Result{ID: q.ID, Unit: t.ID, Variant: in.Variant, State: sourcestate.Error, Skipped: true, Detail: "skipped: unit unauthorized (streak)", SampleAt: tick()})
					default:
						r := fixtureResult(in, t.ID, tick())
						if q.ID == "K6.4" {
							r.State, r.Scalar, r.Rows = sourcestate.Empty, nil, []Row{}
						}
						if q.ID == "K1.D" && i == 0 {
							r.Warnings = []string{"store realcluster-prod-01 responded partially"}
						}
						run.Results = append(run.Results, r)
					}
				}
			}
		}
		if second != nil && !stop {
			run.Results = append(run.Results, *second)
		}
	}
	// Hub'lar
	for i, h := range hubs {
		p := Params{EnvList: []string{"dev", "test", "prod"}, SuffixList: []string{"ocpa", "ocpb"}, PairGroups: map[string][]string{"pair-1": {"ocpa", "ocpb"}},
			InstanceNS: []string{"team-payments-prod", "team-billing-dev"}, WithoutMatcher: h.WithoutMatcher}
		if i == 1 {
			p.InstanceNS = nil
		}
		var passes [][]Instance
		var withPass, nomatchPass []Instance
		for _, pack := range []Pack{PackH, PackN, PackR} {
			for _, q := range ByPack(pack) {
				insts, _ := Expand(q, p)
				for _, in := range insts {
					if in.Variant == "nomatch" {
						nomatchPass = append(nomatchPass, in)
					} else {
						withPass = append(withPass, in)
					}
				}
			}
		}
		passes = append(passes, nomatchPass, withPass)
		for _, pass := range passes {
			for _, in := range pass {
				if in.FallbackFor != "" {
					if !(i == 1 && in.ID == "H5.2a") {
						continue
					}
				}
				switch {
				case i == 1 && in.ID == "H5.1a":
					run.Results = append(run.Results, Result{ID: in.ID, Unit: h.ID, State: sourcestate.Error, Detail: "bad_data: match[] is not supported by this querier", SampleAt: tick(), DurationMs: 30})
				case i == 1 && in.ID == "H6.2b":
					run.Results = append(run.Results, Result{ID: in.ID, Unit: h.ID, State: sourcestate.Timeout, Detail: "query exceeded the 30s worker timeout", SampleAt: tick(), DurationMs: 30000})
				case i == 1 && in.ID == "H2.1":
					r := fixtureResult(in, h.ID, tick())
					r.Truncated, r.Total = true, 2300
					run.Results = append(run.Results, r)
				default:
					run.Results = append(run.Results, fixtureResult(in, h.ID, tick()))
				}
			}
		}
	}
	return run
}

func TestRenderGolden(t *testing.T) {
	rep := Finalize(fixtureRun(), fixtureSeeds())
	got := rep.Markdown
	path := filepath.Join("testdata", "golden_report.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden okunamadı (-update ile üret): %v", err)
	}
	if string(want) != got {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("golden farkı satır %d:\n alınan:  %s\n beklenen: %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("golden uzunluk farkı: %d vs %d satır", len(gl), len(wl))
	}
	// Determinizm: ikinci Finalize aynı çıktı.
	if again := Finalize(fixtureRun(), fixtureSeeds()); again.Markdown != got || again.Tokens != rep.Tokens {
		t.Fatal("Finalize deterministik değil")
	}
	// Yapısal beklentiler.
	if rep.Tokens == 0 || rep.Calls != 318 || rep.Planned != 331 || len(rep.Units) != 5 {
		t.Fatalf("rapor üstbilgisi: tokens=%d calls=%d planned=%d units=%d", rep.Tokens, rep.Calls, rep.Planned, len(rep.Units))
	}
	if !strings.Contains(got, "| ID | cluster-a | cluster-b | hub-1 | hub-2 |") {
		t.Error("§11.9 tablosu sütunları")
	}
	if !strings.Contains(got, "skipped (metrics-only)") || !strings.Contains(got, "T0 fallback") || !strings.Contains(got, "without matcher") || !strings.Contains(got, "equal: yes") {
		t.Error("rapor bölümleri eksik")
	}
	if len(rep.DeniedLabels) != 1 || rep.DeniedLabels[0] != "secret_host" {
		t.Errorf("varsayılan red: %v", rep.DeniedLabels)
	}
	if !strings.Contains(got, "default-denied label names (values never shown): secret_host") {
		t.Error("altbilgi reddedilen etiket")
	}
	// Şablon basılır, etkin ifade değil.
	if strings.Contains(got, `namespace=~"team-.*"`) || strings.Contains(got, "ocpa|ocpb") {
		t.Error("rapor etkin ifadeyi / ham suffix'i basmamalı")
	}
	for _, id := range []string{"V1", "V5", "V6", "V13"} {
		if a := find(rep.Assumptions, id); a.Verdict == "" {
			t.Errorf("%s yargısı yok", id)
		}
	}
}

func TestRenderNoLeak(t *testing.T) {
	rep := Finalize(fixtureRun(), fixtureSeeds())
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sentinels {
		if strings.Contains(rep.Markdown, s) {
			t.Errorf("markdown nöbetçi sızdırdı: %q", s)
		}
		if strings.Contains(string(raw), s) {
			t.Errorf("JSON nöbetçi sızdırdı: %q", s)
		}
	}
	// v0.10.979 — pod adı yalnız JSON'da (çok-pod teşhisi); yapıştırılan
	// markdown hostname taşımaz. Ortak nöbetçi listesine GİRMEZ: JSON'da bilerek var.
	if strings.Contains(rep.Markdown, fixtureRun().Pod) {
		t.Errorf("markdown pod adını sızdırdı")
	}
	if rep.Pod != fixtureRun().Pod {
		t.Errorf("JSON pod alanı korunmalı: %q", rep.Pod)
	}
	// H0.3/H0.5 küme kimliği etiketleri jetonlu (k8s_cluster olayı).
	for _, r := range rep.Results {
		if r.ID != "H0.3" && r.ID != "H0.5" {
			continue
		}
		for _, row := range r.Rows {
			for _, l := range []string{"k8s_cluster", "cluster_name", "openshift_cluster", "tenant_id"} {
				if v, ok := row.Labels[l]; ok && v != "" && !strings.HasPrefix(v, "cluster-") && !strings.HasPrefix(v, "hub-") {
					t.Errorf("%s %s=%q jetonlanmamış", r.Key(), l, v)
				}
			}
		}
	}
	for _, r := range rep.Results {
		if r.VariantRaw != "" {
			t.Errorf("%s VariantRaw temizlenmemiş", r.Key())
		}
		if strings.HasPrefix(r.Variant, "inst:") && !strings.HasPrefix(r.Variant, "inst:<team-") {
			t.Errorf("inst varyantı jetonlanmamış: %s", r.Variant)
		}
	}
	if !strings.Contains(rep.Markdown, "cluster-b (cluster-b): K0.3") {
		t.Errorf("Skipped scrub: %v", rep.Skipped)
	}
	if !strings.Contains(strings.Join(rep.Warnings, " "), "store cluster-a partial at https://<host>") {
		t.Errorf("Warnings scrub: %v", rep.Warnings)
	}
	var h21 *Result
	for i := range rep.Results {
		if rep.Results[i].ID == "H2.1" && rep.Results[i].Unit == "hub-1" {
			h21 = &rep.Results[i]
		}
	}
	if h21 == nil || h21.Rows[0].Labels["dest_server"] != "https://api.cluster-a.<domain>:6443" || h21.Rows[2].Labels["dest_server"] != "https://kubernetes.default.svc" {
		t.Fatalf("H2.1 jetonları: %+v", h21)
	}
}

// TestFinalizeClipsDetail — v0.10.979 — Detail / EarlyStop DetailMax'a
// kırpılır; kırpma ScrubText'ten SONRA (takma ad ortadan bölünüp sızmaz);
// önek korunur (IsBadData / firstWord tüketicileri).
func TestFinalizeClipsDetail(t *testing.T) {
	long := "execution: " + strings.Repeat("x", 20<<10)
	run := RawRun{RunID: "r", Status: "done", StartedAt: time.Now(), FinishedAt: time.Now(), Packs: []Pack{PackH},
		Units: []RawUnit{{ID: "c-9f8e7d6c", Token: "hub-1", Role: "hub", State: "unauthorized", EarlyStop: "unauthorized streak after H0.1 (" + strings.Repeat("e", 20<<10) + ")"}},
		Results: []Result{
			{ID: "H0.1", Unit: "c-9f8e7d6c", State: sourcestate.Error, Detail: long, SampleAt: time.Now()},
			{ID: "H0.4", Unit: "c-9f8e7d6c", State: sourcestate.Error, Detail: strings.Repeat("x", DetailMax-3) + "realcluster-prod-01 tail", SampleAt: time.Now()},
		}}
	rep := Finalize(run, fixtureSeeds())
	if d := rep.Results[0].Detail; len(d) > DetailMax+len("…") || !strings.HasPrefix(d, "execution:") || !strings.HasSuffix(d, "…") {
		t.Errorf("Detail kırpılmamış / önek bozuk: len=%d %q", len(d), d[:40])
	}
	if es := rep.Units[0].EarlyStop; len(es) > DetailMax+len("…") || !strings.HasPrefix(es, "unauthorized streak after H0.1") {
		t.Errorf("EarlyStop kırpılmamış: len=%d", len(es))
	}
	d2 := rep.Results[1].Detail
	if strings.Contains(d2, "realcluster") {
		t.Errorf("kırpma scrub'dan önce koşmuş, takma ad parçası sızdı: %q", d2)
	}
	// Kesik jetonun İÇİNE düşer ("clu…"): önce kırpılsaydı "rea…" parçası kalırdı.
	if !strings.HasSuffix(d2, "clu…") {
		t.Errorf("kırpma jetonlanmış metin üzerinde olmalı: %q", d2[len(d2)-40:])
	}
	if strings.Contains(rep.Markdown, "realcluster") || len(rep.Markdown) > 64<<10 {
		t.Errorf("markdown: len=%d", len(rep.Markdown))
	}
	if clipText("abc", 3) != "abc" || clipText("abcd", 3) != "abc…" || clipText("aé", 2) != "a…" {
		t.Error("clipText")
	}
}

func TestSummaryAndPairs(t *testing.T) {
	v := "7"
	if s := summary(&Result{State: sourcestate.OK, Scalar: &v}); s != "7" {
		t.Errorf("scalar: %s", s)
	}
	if s := summary(&Result{State: sourcestate.Empty}); s != "0 (empty)" {
		t.Errorf("empty: %s", s)
	}
	if s := summary(&Result{State: sourcestate.Error, Skipped: true}); s != "skipped" {
		t.Errorf("skipped: %s", s)
	}
	if s := summary(&Result{State: sourcestate.Unauthorized}); s != "unauthorized" {
		t.Errorf("state: %s", s)
	}
	r := &Result{State: sourcestate.OK, Rows: []Row{row("1", "a", "x"), row("2", "a", "y", "b", "z"), row("3", "a", "w"), row("4", "a", "v")}}
	if s := summary(r); s != "x=1, {y/z}=2, w=3, … +1" {
		t.Errorf("rows: %s", s)
	}
	if s := summary(&Result{State: sourcestate.OK, Names: []string{"a", "b"}}); s != "2 names" {
		t.Errorf("names: %s", s)
	}
	if summary(nil) != "—" {
		t.Error("nil")
	}
}
