package v2probe

// assess_test.go — v0.10.979 — V1–V14 yargıları: her V için confirmed /
// refuted / unknown vakası (spec §10); jetonlanmış sonuçlar üzerinde.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

func mkScalar(id, unit, variant, v string) Result {
	return Result{ID: id, Unit: unit, Variant: variant, State: sourcestate.OK, Scalar: &v}
}

func mkEmpty(id, unit string) Result {
	return Result{ID: id, Unit: unit, State: sourcestate.Empty, Rows: []Row{}}
}

func mkRows(id, unit string, rows ...Row) Result {
	return Result{ID: id, Unit: unit, State: sourcestate.OK, Rows: rows}
}

func mkNames(id, unit string, names ...string) Result {
	return Result{ID: id, Unit: unit, State: sourcestate.OK, Names: names}
}

func mkErr(id, unit string, st sourcestate.State) Result {
	return Result{ID: id, Unit: unit, State: st, Detail: "x"}
}

func nameRows(names ...string) []Row {
	out := make([]Row, 0, len(names))
	for _, n := range names {
		out = append(out, Row{Labels: map[string]string{"__name__": n}, Value: "3"})
	}
	return out
}

func find(as []Assumption, id string) Assumption {
	for _, a := range as {
		if a.ID == id {
			return a
		}
	}
	return Assumption{}
}

var tgt = []Unit{{Token: "cluster-a", Role: "target"}}

func TestAssessV1(t *testing.T) {
	ok := []Result{
		mkRows("K1.D", "cluster-a", nameRows("kube_deployment_metadata_generation", "kube_deployment_status_observed_generation")...),
		mkRows("K1.S", "cluster-a", nameRows("kube_statefulset_metadata_generation", "kube_statefulset_status_observed_generation")...),
		mkRows("K1.DS", "cluster-a", nameRows("kube_daemonset_metadata_generation", "kube_daemonset_status_observed_generation")...),
	}
	if a := find(Assess(tgt, ok), "V1"); a.Verdict != Confirmed || len(a.Evidence) != 3 {
		t.Fatalf("V1 confirmed bekleniyordu: %+v", a)
	}
	bad := append([]Result{}, ok...)
	bad[1] = mkRows("K1.S", "cluster-a", nameRows("kube_statefulset_metadata_generation")...)
	if a := find(Assess(tgt, bad), "V1"); a.Verdict != Refuted || !strings.Contains(strings.Join(a.Evidence, " "), "missing kube_statefulset_status_observed_generation") {
		t.Fatalf("V1 refuted bekleniyordu: %+v", a)
	}
	unk := append([]Result{}, ok...)
	unk[2] = mkErr("K1.DS", "cluster-a", sourcestate.Unauthorized)
	if a := find(Assess(tgt, unk), "V1"); a.Verdict != Unknown {
		t.Fatalf("V1 unknown bekleniyordu: %+v", a)
	}
	if a := find(Assess(nil, nil), "V1"); a.Verdict != Unknown {
		t.Fatalf("hedefsiz V1 unknown: %+v", a)
	}
}

func TestAssessV2AndV2b(t *testing.T) {
	ok := []Result{
		mkNames("K2.1b", "cluster-a", "namespace", "replicaset", "owner_kind", "owner_name", "owner_is_controller"),
		mkNames("K2.1d", "cluster-a", "namespace", "statefulset", "revision"),
		mkRows("K2.3a", "cluster-a", Row{Labels: map[string]string{"owner_kind": "Deployment", "owner_is_controller": "true"}, Value: "9"}),
		mkRows("K1.S", "cluster-a", nameRows("kube_statefulset_status_current_revision", "kube_statefulset_status_update_revision")...),
	}
	as := Assess(tgt, ok)
	if find(as, "V2").Verdict != Confirmed || find(as, "V2b").Verdict != Confirmed {
		t.Fatalf("V2/V2b confirmed: %+v %+v", find(as, "V2"), find(as, "V2b"))
	}
	bad := []Result{
		mkNames("K2.1b", "cluster-a", "namespace", "replicaset"),
		mkNames("K2.1d", "cluster-a", "namespace", "statefulset", "revision"),
		mkRows("K2.3a", "cluster-a", Row{Labels: map[string]string{"owner_kind": "Deployment"}, Value: "9"}),
		mkRows("K1.S", "cluster-a", nameRows("kube_statefulset_status_current_revision")...),
	}
	as = Assess(tgt, bad)
	if find(as, "V2").Verdict != Refuted || find(as, "V2b").Verdict != Refuted || !strings.Contains(find(as, "V2b").Evidence[0], "rollback invisible") {
		t.Fatalf("V2/V2b refuted: %+v %+v", find(as, "V2"), find(as, "V2b"))
	}
	as = Assess(tgt, nil)
	if find(as, "V2").Verdict != Unknown || find(as, "V2b").Verdict != Unknown {
		t.Fatal("V2/V2b unknown")
	}
}

func TestAssessV3(t *testing.T) {
	ok := []Result{
		mkRows("K1.R", "cluster-a", nameRows("kube_replicaset_owner", "kube_replicaset_spec_replicas")...),
		mkScalar("K3.1", "cluster-a", "", "1200"), mkScalar("K3.2", "cluster-a", "", "900"),
	}
	a := find(Assess(tgt, ok), "V3")
	if a.Verdict != Confirmed || !strings.Contains(a.Note, "K3.1 > 1000") {
		t.Fatalf("V3: %+v", a)
	}
	ok[1] = mkScalar("K3.1", "cluster-a", "", "60000")
	if a = find(Assess(tgt, ok), "V3"); a.Verdict != Refuted || !strings.Contains(strings.Join(a.Evidence, " "), "shard needed") {
		t.Fatalf("V3 refuted: %+v", a)
	}
	ok[1] = mkErr("K3.1", "cluster-a", sourcestate.Timeout)
	if a = find(Assess(tgt, ok), "V3"); a.Verdict != Unknown {
		t.Fatalf("V3 unknown: %+v", a)
	}
}

func TestAssessV4(t *testing.T) {
	ok := []Result{
		mkRows("K1.D", "cluster-a", nameRows("kube_deployment_metadata_generation")...),
		mkRows("K1.R", "cluster-a", nameRows("kube_replicaset_owner")...),
		mkRows("K1.S", "cluster-a", nameRows("kube_statefulset_replicas")...),
		mkRows("K1.DS", "cluster-a", nameRows("kube_daemonset_metadata_generation")...),
		mkEmpty("K6.4", "cluster-a"),
	}
	if a := find(Assess(tgt, ok), "V4"); a.Verdict != Confirmed {
		t.Fatalf("V4 confirmed: %+v", a)
	}
	ok[1] = mkRows("K1.R", "cluster-a", nameRows("kube_replicaset_owner", "kube_replicaset_created")...)
	ok[4] = mkScalar("K6.4", "cluster-a", "", "40")
	if a := find(Assess(tgt, ok), "V4"); a.Verdict != Refuted || !strings.Contains(a.Note, "dec 9") {
		t.Fatalf("V4 refuted (favourable): %+v", a)
	}
	ok[4] = mkErr("K6.4", "cluster-a", sourcestate.Unreachable)
	ok[1] = mkRows("K1.R", "cluster-a", nameRows("kube_replicaset_owner")...)
	if a := find(Assess(tgt, ok), "V4"); a.Verdict != Unknown {
		t.Fatalf("V4 unknown: %+v", a)
	}
}

func TestAssessV5(t *testing.T) {
	ok := []Result{mkScalar("K0.5", "cluster-a", "", "10"), mkScalar("K0.6b", "cluster-a", "", "1784271060"), mkScalar("K0.6b", "cluster-a", "second_sample", "1784271060")}
	if a := find(Assess(tgt, ok), "V5"); a.Verdict != Confirmed {
		t.Fatalf("V5: %+v", a)
	}
	ok[0] = mkScalar("K0.5", "cluster-a", "", "20")
	if a := find(Assess(tgt, ok), "V5"); a.Verdict != Refuted || !strings.Contains(a.Evidence[0], "30 s scrape") {
		t.Fatalf("V5 30 s: %+v", a)
	}
	ok[0] = mkScalar("K0.5", "cluster-a", "", "10")
	ok[2] = mkScalar("K0.6b", "cluster-a", "second_sample", "1784271120")
	if a := find(Assess(tgt, ok), "V5"); a.Verdict != Refuted || !strings.Contains(a.Evidence[0], "samples differ") {
		t.Fatalf("V5 differ: %+v", a)
	}
	ok[0] = mkScalar("K0.5", "cluster-a", "", "15")
	ok[2] = mkScalar("K0.6b", "cluster-a", "second_sample", "1784271060")
	if a := find(Assess(tgt, ok), "V5"); a.Verdict != Unknown {
		t.Fatalf("V5 band dışı unknown: %+v", a)
	}
	if a := find(Assess(tgt, ok[:1]), "V5"); a.Verdict != Unknown {
		t.Fatalf("V5 örneksiz unknown: %+v", a)
	}
}

func TestAssessV6(t *testing.T) {
	units := []Unit{{Token: "cluster-a", Role: "target"}, {Token: "hub-1", Role: "hub"}}
	ok := []Result{
		mkScalar("K0.4", "cluster-a", "", "1"), mkScalar("K0.4", "cluster-a", "dedup_off", "2"),
		mkRows("H0.2", "hub-1", Row{Labels: map[string]string{"prometheus": "<prometheus-1>", "prometheus_replica": ""}, Value: "40000"}),
	}
	a := find(Assess(units, ok), "V6")
	if a.Verdict != Confirmed || !strings.Contains(strings.Join(a.Evidence, " "), "raw HA ratio 2") {
		t.Fatalf("V6: %+v", a)
	}
	ok[2] = mkRows("H0.2", "hub-1", Row{Labels: map[string]string{"prometheus": "<prometheus-1>", "prometheus_replica": "<replica-1>"}, Value: "20000"})
	if a = find(Assess(units, ok), "V6"); a.Verdict != Refuted {
		t.Fatalf("V6 replica: %+v", a)
	}
	ok[2] = mkRows("H0.2", "hub-1", Row{Labels: map[string]string{"prometheus": "<prometheus-1>"}, Value: "40000"})
	ok[0] = mkScalar("K0.4", "cluster-a", "", "2")
	if a = find(Assess(units, ok), "V6"); a.Verdict != Refuted {
		t.Fatalf("V6 dup: %+v", a)
	}
	if a = find(Assess(units, ok[1:2]), "V6"); a.Verdict != Unknown {
		t.Fatalf("V6 unknown: %+v", a)
	}
}

func TestAssessV7V8V9(t *testing.T) {
	ok := []Result{
		mkRows("K2.3c", "cluster-a", Row{Labels: map[string]string{"condition": "Available", "status": "true", "reason": "MinimumReplicasAvailable"}, Value: "12"}),
		mkRows("K0.3", "cluster-a", Row{Labels: map[string]string{"version": "v2.13.0"}, Value: "1"}),
		mkScalar("K2.4b", "cluster-a", "", "30"),
		mkRows("K2.3b", "cluster-a", Row{Labels: map[string]string{"owner_kind": "DaemonSet"}, Value: "30"}, Row{Labels: map[string]string{"owner_kind": "ReplicaSet"}, Value: "300"}),
		mkRows("K0.2", "cluster-a", Row{Labels: map[string]string{"job": "kube-state-metrics"}, Value: "1"}, Row{Labels: map[string]string{"job": "openshift-state-metrics"}, Value: "1"}),
		mkScalar("K0.6", "cluster-a", "", "42"),
	}
	as := Assess(tgt, ok)
	if find(as, "V7").Verdict != Confirmed || !strings.Contains(find(as, "V7").Evidence[0], "reason=true, K0.3=v2.13.0") {
		t.Fatalf("V7: %+v", find(as, "V7"))
	}
	if find(as, "V8").Verdict != Confirmed || find(as, "V9").Verdict != Confirmed {
		t.Fatalf("V8/V9: %+v %+v", find(as, "V8"), find(as, "V9"))
	}
	ok[0] = mkRows("K2.3c", "cluster-a", Row{Labels: map[string]string{"condition": "Available", "status": "true"}, Value: "12"})
	ok[2] = mkScalar("K2.4b", "cluster-a", "", "0")
	ok[4] = mkRows("K0.2", "cluster-a", Row{Labels: map[string]string{"job": "kube-state-metrics"}, Value: "1"}, Row{Labels: map[string]string{"job": "ksm-second"}, Value: "1"})
	as = Assess(tgt, ok)
	if a := find(as, "V7"); a.Verdict != Confirmed || !strings.Contains(a.Note, "KSM < v2.17") {
		t.Fatalf("V7 reason yok: %+v", a)
	}
	if find(as, "V8").Verdict != Refuted || find(as, "V9").Verdict != Refuted {
		t.Fatalf("V8/V9 refuted: %+v %+v", find(as, "V8"), find(as, "V9"))
	}
	ok[4] = mkRows("K0.2", "cluster-a", Row{Labels: map[string]string{"job": "kube-state-metrics"}, Value: "1"})
	ok[5] = mkScalar("K0.6", "cluster-a", "", "120")
	if a := find(Assess(tgt, ok), "V9"); a.Verdict != Refuted || !strings.Contains(a.Evidence[0], "≥ 90") {
		t.Fatalf("V9 stale: %+v", a)
	}
	ok[2] = mkScalar("K2.4b", "cluster-a", "", "5")
	ok[3] = mkRows("K2.3b", "cluster-a", Row{Labels: map[string]string{"owner_kind": "ReplicaSet"}, Value: "300"})
	if a := find(Assess(tgt, ok), "V8"); a.Verdict != Unknown {
		t.Fatalf("V8 DS satırı yok → unknown: %+v", a)
	}
	as = Assess(tgt, nil)
	for _, id := range []string{"V7", "V8", "V9"} {
		if find(as, id).Verdict != Unknown {
			t.Errorf("%s unknown olmalı", id)
		}
	}
}

func TestAssessV10V11V12(t *testing.T) {
	ok := []Result{mkRows("K1.H", "cluster-a", Row{Labels: map[string]string{"scaletargetref_kind": "Deployment"}, Value: "8"})}
	for id, v := range map[string]string{"K5.1": "40", "K5.2": "6", "K5.3": "10", "K5.4": "1", "K5.5": "2", "K5.6": "0"} {
		ok = append(ok, mkScalar(id, "cluster-a", "", v))
	}
	for id, v := range map[string]string{"D1": "12", "D2": "10", "D3": "4", "D4": "30", "D7": "1", "D8": "150"} {
		ok = append(ok, mkScalar(id, "cluster-a", "", v))
	}
	ok = append(ok, mkEmpty("D5", "cluster-a"), mkRows("D6", "cluster-a", nameRows("openshift_deploymentconfig_spec_replicas")...),
		mkRows("K1.D", "cluster-a", nameRows("kube_deployment_status_replicas")...))
	as := Assess(tgt, ok)
	if a := find(as, "V10"); a.Verdict != Confirmed || !strings.Contains(a.Note, "K5.1/K5.3 = 4") {
		t.Fatalf("V10: %+v", a)
	}
	if a := find(as, "V11"); a.Verdict != Unknown || len(a.Evidence) != 2 {
		t.Fatalf("V11 daima unknown + kanıt: %+v", a)
	}
	if a := find(as, "V12"); a.Verdict != Confirmed || !strings.Contains(a.Evidence[0], "DC share 6.2%") || !strings.Contains(a.Note, "material") {
		t.Fatalf("V12: %+v", a)
	}
	as = Assess(tgt, ok[:3])
	if find(as, "V10").Verdict != Unknown || find(as, "V12").Verdict != Unknown {
		t.Fatal("V10/V12 unknown")
	}
}

func TestAssessV13V14(t *testing.T) {
	noFilter := []Unit{{Token: "cluster-a", Role: "target"}}
	if a := find(Assess(noFilter, nil), "V13"); a.Verdict != Confirmed || !strings.Contains(a.Evidence[0], "n/a") {
		t.Fatalf("V13 filtresiz: %+v", a)
	}
	withFilter := []Unit{{Token: "cluster-a", Role: "target", NSFilter: true}}
	var ns []Result
	for _, id := range []string{"K3.1", "K3.2", "K3.3", "K3.4", "K3.5", "K3.6", "K3.7"} {
		ns = append(ns, mkScalar(id, "cluster-a", "ns", "5"))
	}
	if a := find(Assess(withFilter, ns), "V13"); a.Verdict != Confirmed {
		t.Fatalf("V13 filtreli: %+v", a)
	}
	if a := find(Assess(withFilter, ns[:3]), "V13"); a.Verdict != Unknown {
		t.Fatalf("V13 eksik: %+v", a)
	}
	ok := []Result{
		mkRows("K1.P", "cluster-a", nameRows("kube_pod_container_info", "kube_pod_owner")...),
		mkNames("K2.1a", "cluster-a", "image", "image_spec", "image_id"),
		mkScalar("K2.2a", "cluster-a", "", "900"), mkScalar("K2.2b", "cluster-a", "", "900"), mkScalar("K2.2d", "cluster-a", "", "0"),
	}
	if a := find(Assess(tgt, ok), "V14"); a.Verdict != Confirmed || !strings.Contains(a.Note, "K2.2b=900") || strings.Contains(a.Note, "K2.2d") {
		t.Fatalf("V14: %+v", a)
	}
	ok[2] = mkScalar("K2.2a", "cluster-a", "", "0")
	if a := find(Assess(tgt, ok), "V14"); a.Verdict != Refuted {
		t.Fatalf("V14 0: %+v", a)
	}
	ok[2] = mkScalar("K2.2a", "cluster-a", "", "9")
	ok[1] = mkNames("K2.1a", "cluster-a", "image")
	if a := find(Assess(tgt, ok), "V14"); a.Verdict != Refuted || !strings.Contains(a.Evidence[0], "missing image_spec") {
		t.Fatalf("V14 etiket: %+v", a)
	}
	if a := find(Assess(tgt, ok[:1]), "V14"); a.Verdict != Unknown {
		t.Fatalf("V14 unknown: %+v", a)
	}
}

func TestDecisionsShape(t *testing.T) {
	units := []Unit{{Token: "cluster-a", Role: "target"}, {Token: "hub-1", Role: "hub"}}
	res := []Result{
		mkScalar("H0.1", "hub-1", "", "40000"), mkScalar("H0.1", "hub-1", "nomatch", "41000"),
		mkRows("H1.2", "hub-1", Row{Labels: map[string]string{"namespace": "<team-1>-prod", "exported_namespace": "<team-1>-prod", "job": "<team-1>-prod-metrics"}, Value: "10"},
			Row{Labels: map[string]string{"namespace": "<team-2>-prod", "exported_namespace": "", "job": "<team-2>-prod-metrics"}, Value: "10"},
			Row{Labels: map[string]string{"namespace": "<team-3>-prod", "exported_namespace": "<ns-1>", "job": "<team-3>-prod-metrics"}, Value: "10"}),
		mkScalar("K3.1", "cluster-a", "", "1500"), mkScalar("K6.1", "cluster-a", "", "1500"),
		mkNames("R0", "cluster-a"), mkEmpty("R1", "cluster-a"), mkEmpty("R2", "cluster-a"), mkEmpty("R3", "cluster-a"),
	}
	ds := Decisions(units, res)
	got := map[string]string{}
	for _, d := range ds {
		got[d.ID] = d.Verdict
	}
	if len(ds) != 15 {
		t.Fatalf("%d karar satırı, beklenen 15", len(ds))
	}
	if !strings.Contains(got["dec 5 / hubs[].injectClusterLabel"], "40000 ≠ without 41000 — injection hides Argo series") {
		t.Errorf("dec 5: %s", got["dec 5 / hubs[].injectClusterLabel"])
	}
	if !strings.Contains(got["dec 28"], "H1.2 case A=1 B=1 C=1") {
		t.Errorf("dec 28: %s", got["dec 28"])
	}
	if !strings.Contains(got["dec 3"], "v1 leg truncated today") {
		t.Errorf("dec 3: %s", got["dec 3"])
	}
	if !strings.Contains(got["dec 11"], "do not build") {
		t.Errorf("dec 11: %s", got["dec 11"])
	}
	if got["dec 30"] != "skipped (metrics-only)" {
		t.Errorf("dec 30: %s", got["dec 30"])
	}
}
