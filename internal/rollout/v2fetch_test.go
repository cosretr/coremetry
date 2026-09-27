package rollout

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v2fetch_test.go — v0.10.982 — Rollouts v2 P2.2 KSM okuma katmanı sözleşmesi
// (docs/rollouts/v2-audit.md §4.8–§4.11; kararlar 2, 8).
//
//   - Her tik sorgusu `max by`/`count` toplamalı (HA kopyası seri tavanını
//     yemesin, §4.8) ve HER seçici küme namespace kalkanını taşır (§4.9).
//     Tür kapalıysa o türün sorgusu hiç gitmez.
//   - SampleAt = timestamp() HAM seçiciye, toplamanın İÇİNDE (V5).
//   - Snapshot yalnız bütün sorgular gelmiş ve HİÇBİRİ kesik/kısmi
//     değilken Complete'tir (§4.10); aksi hâlde iş yükü taşımaz.
//   - İmajlar yalnız hedef (açık olaylı / bekleyen) iş yükleri için,
//     sınırlı sorgu ve ad sayısıyla okunur (§4.11); okunamayan iş yükünün
//     RevisionImages'i nil kalır ("okunmadı"), okunup boş gelen {} olur.

func fs(v float64, kv ...string) V2Sample {
	l := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		l[kv[i]] = kv[i+1]
	}
	return V2Sample{Labels: l, Value: v}
}

func fr(samples ...V2Sample) V2QueryResult {
	return V2QueryResult{Samples: samples, Series: len(samples), TotalSeries: len(samples)}
}

func TestParseV2Vector(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		raw     string
		want    []V2Sample
		wantErr bool
	}{
		{name: "boş vektör", typ: "vector", raw: `[]`, want: []V2Sample{}},
		{name: "iki örnek", typ: "vector",
			raw:  `[{"metric":{"namespace":"pay","deployment":"api"},"value":[1784271068.5,"3"]},{"metric":{},"value":[1,"0.25"]}]`,
			want: []V2Sample{{Labels: map[string]string{"namespace": "pay", "deployment": "api"}, Value: 3}, {Labels: map[string]string{}, Value: 0.25}}},
		{name: "NaN ve Inf atlanır", typ: "vector",
			raw:  `[{"metric":{"a":"1"},"value":[1,"NaN"]},{"metric":{"a":"2"},"value":[1,"+Inf"]},{"metric":{"a":"3"},"value":[1,"7"]}]`,
			want: []V2Sample{{Labels: map[string]string{"a": "3"}, Value: 7}}},
		{name: "matrix reddedilir", typ: "matrix", raw: `[]`, wantErr: true},
		{name: "skaler reddedilir", typ: "scalar", raw: `[1,"2"]`, wantErr: true},
		{name: "bozuk değer", typ: "vector", raw: `[{"metric":{},"value":[1,"x"]}]`, wantErr: true},
		{name: "eksik değer çifti", typ: "vector", raw: `[{"metric":{},"value":[1]}]`, wantErr: true},
		{name: "bozuk JSON", typ: "vector", raw: `[{`, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseV2Vector(c.typ, []byte(c.raw))
			if c.wantErr {
				if err == nil {
					t.Fatalf("hata bekleniyordu, gelen %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("beklenmeyen hata: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v\nwant %#v", got, c.want)
			}
		})
	}
}

func v2QueryNames(qs []V2Query) []string {
	out := make([]string, 0, len(qs))
	for _, q := range qs {
		out = append(out, q.Name)
	}
	return out
}

func TestV2TickQueriesKindsAndShape(t *testing.T) {
	ns := `,namespace=~"pay|shop"`
	all := V2TickQueries([]string{V2KindDeployment, V2KindStatefulSet, V2KindDaemonSet}, ns)
	wantAll := []string{V2QDeployGauges, V2QDeploySample, V2QDeployProgressing, V2QRSOwner, V2QRSActive, V2QRSSpecCount,
		V2QStsGauges, V2QStsSample, V2QStsRevisions, V2QDsGauges, V2QDsSample, V2QDsHashes}
	if got := v2QueryNames(all); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("sorgu adları %v, istenen %v", got, wantAll)
	}
	for _, q := range all {
		// §4.8: her ifade toplamalı (ham seri okunmaz).
		if !strings.HasPrefix(q.Expr, "max by (") && !strings.HasPrefix(q.Expr, "count(") && !strings.HasPrefix(q.Expr, "count by (") {
			t.Errorf("%s toplamasız: %s", q.Name, q.Expr)
		}
		// §4.9: seçici sayısı kadar namespace kalkanı.
		if sel, nsn := strings.Count(q.Expr, "{"), strings.Count(q.Expr, ns); sel != nsn {
			t.Errorf("%s: %d seçici, %d namespace kalkanı: %s", q.Name, sel, nsn, q.Expr)
		}
		if !strings.Contains(q.Expr, `namespace!=""`) {
			t.Errorf("%s: namespace!=\"\" taban eşleştiricisi yok (kalkan baştaki virgülle eklenir): %s", q.Name, q.Expr)
		}
	}
	byName := map[string]string{}
	for _, q := range all {
		byName[q.Name] = q.Expr
	}
	// V5: timestamp toplamanın İÇİNDE.
	for _, n := range []string{V2QDeploySample, V2QStsSample, V2QDsSample} {
		if !strings.Contains(byName[n], "(timestamp(kube_") || strings.HasPrefix(byName[n], "timestamp(") {
			t.Errorf("%s: timestamp() ham seçiciye, max by'ın içinde olmalı: %s", n, byName[n])
		}
	}
	for n, want := range map[string]string{
		V2QDeployGauges:      `kube_deployment_(metadata_generation|status_observed_generation|spec_replicas|status_replicas|status_replicas_updated|status_replicas_available|spec_paused|created)`,
		V2QDeployProgressing: `condition="Progressing",status="false"`,
		V2QRSOwner:           `owner_kind="Deployment",owner_is_controller!="false"`,
		V2QRSActive:          `) > 0`,
		V2QStsRevisions:      `kube_statefulset_status_(current|update)_revision`,
		V2QDsHashes:          `group_left (owner_name)`,
	} {
		if !strings.Contains(byName[n], want) {
			t.Errorf("%s ifadesinde %q yok: %s", n, want, byName[n])
		}
	}
	// Tür kapısı: yalnız Deployment → STS/DS sorgusu GİTMEZ.
	dep := V2TickQueries([]string{"deployment"}, "")
	if got := v2QueryNames(dep); !reflect.DeepEqual(got, wantAll[:6]) {
		t.Fatalf("yalnız Deployment: %v", got)
	}
	for _, q := range dep {
		if strings.Contains(q.Expr, "namespace=~") {
			t.Errorf("boş filtrede kalkan olmamalı: %s", q.Expr)
		}
	}
	if len(V2TickQueries(nil, "")) != 0 || len(V2TickQueries([]string{"Rollout"}, "")) != 0 {
		t.Error("tür yoksa sorgu da yok")
	}
}

// v2FullDeployResults — iki Deployment'lık tam okuma (api: rs a etkin, b
// boş; web: yalnız w1).
func v2FullDeployResults() map[string]V2QueryResult {
	return map[string]V2QueryResult{
		V2QDeployGauges: fr(
			fs(4, "__name__", "kube_deployment_metadata_generation", "namespace", "pay", "deployment", "api"),
			fs(4, "__name__", "kube_deployment_status_observed_generation", "namespace", "pay", "deployment", "api"),
			fs(3, "__name__", "kube_deployment_spec_replicas", "namespace", "pay", "deployment", "api"),
			fs(3, "__name__", "kube_deployment_status_replicas", "namespace", "pay", "deployment", "api"),
			fs(3, "__name__", "kube_deployment_status_replicas_updated", "namespace", "pay", "deployment", "api"),
			fs(2, "__name__", "kube_deployment_status_replicas_available", "namespace", "pay", "deployment", "api"),
			fs(1, "__name__", "kube_deployment_spec_paused", "namespace", "pay", "deployment", "api"),
			fs(1784271000, "__name__", "kube_deployment_created", "namespace", "pay", "deployment", "api"),
			fs(2, "__name__", "kube_deployment_metadata_generation", "namespace", "pay", "deployment", "web"),
			fs(1, "__name__", "kube_deployment_status_observed_generation", "namespace", "pay", "deployment", "web"),
		),
		V2QDeploySample: fr(
			fs(1784271068.5, "namespace", "pay", "deployment", "api"),
			fs(1784271060, "namespace", "pay", "deployment", "web"),
		),
		V2QDeployProgressing: fr(
			fs(1, "namespace", "pay", "deployment", "api"),
			fs(0, "namespace", "pay", "deployment", "web"),
		),
		V2QRSOwner: fr(
			fs(1, "namespace", "pay", "replicaset", "api-b", "owner_name", "api"),
			fs(1, "namespace", "pay", "replicaset", "api-a", "owner_name", "api"),
			fs(1, "namespace", "pay", "replicaset", "web-w1", "owner_name", "web"),
			fs(1, "namespace", "pay", "replicaset", "", "owner_name", "web"), // bozuk etiket: atlanır
		),
		V2QRSActive: fr(
			fs(3, "namespace", "pay", "replicaset", "api-a"),
			fs(2, "namespace", "pay", "replicaset", "orphan-x"), // sahipsiz: iş yüküne bağlanmaz
		),
		V2QRSSpecCount: fr(fs(4)),
	}
}

func TestBuildV2SnapshotDeployment(t *testing.T) {
	snap, diag := BuildV2Snapshot([]string{V2KindDeployment}, v2FullDeployResults())
	if !snap.Complete {
		t.Fatalf("tam okuma Complete olmalı (diag %v)", diag)
	}
	want := V2Presence{RSOwner: true, RSSpec: true, ProgressingCondition: true, Created: true}
	if snap.Presence != want {
		t.Fatalf("presence %+v, istenen %+v", snap.Presence, want)
	}
	if len(snap.Workloads) != 2 {
		t.Fatalf("iş yükü sayısı %d: %+v", len(snap.Workloads), snap.Workloads)
	}
	api, web := snap.Workloads[0], snap.Workloads[1]
	if api.Name != "api" || web.Name != "web" {
		t.Fatalf("sıra ad sırası olmalı: %s, %s", api.Name, web.Name)
	}
	wantAPI := V2Observation{Kind: V2KindDeployment, Namespace: "pay", Name: "api",
		SampleAt: time.UnixMilli(1784271068500).UTC(), CreatedAt: time.Unix(1784271000, 0).UTC(),
		Generation: 4, ObservedGeneration: 4, SpecReplicas: 3, StatusReplicas: 3, UpdatedReplicas: 3, AvailableReplicas: 2,
		Paused: true, ProgressDeadlineExceeded: true,
		ReplicaSets: []V2ReplicaSet{{Name: "api-a", SpecReplicas: 3}, {Name: "api-b", SpecReplicas: 0}}}
	if !reflect.DeepEqual(api, wantAPI) {
		t.Fatalf("api\n got %+v\nwant %+v", api, wantAPI)
	}
	if web.ProgressDeadlineExceeded || web.Paused || web.Generation != 2 || web.ObservedGeneration != 1 || len(web.ReplicaSets) != 1 {
		t.Fatalf("web yanlış: %+v", web)
	}
	if diag["fetch_bad_sample"] != 1 {
		t.Errorf("bozuk etiketli owner sayılmalı: %v", diag)
	}
}

func TestBuildV2SnapshotIncomplete(t *testing.T) {
	kinds := []string{V2KindDeployment}
	cases := map[string]func(m map[string]V2QueryResult){
		"eksik sorgu": func(m map[string]V2QueryResult) { delete(m, V2QRSOwner) },
		"kesik": func(m map[string]V2QueryResult) {
			r := m[V2QDeployGauges]
			r.Truncated, r.TotalSeries = true, r.Series+1
			m[V2QDeployGauges] = r
		},
		"kısmi (uyarı)": func(m map[string]V2QueryResult) {
			r := m[V2QRSActive]
			r.Partial = true
			m[V2QRSActive] = r
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			m := v2FullDeployResults()
			mut(m)
			snap, diag := BuildV2Snapshot(kinds, m)
			if snap.Complete || len(snap.Workloads) != 0 {
				t.Fatalf("kısmi okuma Complete olmamalı ve iş yükü taşımamalı: %+v", snap)
			}
			if len(diag) == 0 {
				t.Fatal("teşhis sayacı yok")
			}
		})
	}
}

func TestBuildV2SnapshotPresenceAbsentFamilies(t *testing.T) {
	m := v2FullDeployResults()
	m[V2QRSOwner], m[V2QRSActive], m[V2QRSSpecCount], m[V2QDeployProgressing] = fr(), fr(), fr(), fr()
	snap, _ := BuildV2Snapshot([]string{V2KindDeployment}, m)
	if !snap.Complete {
		t.Fatal("boş aile tam okumadır (seri yok ≠ kısmi)")
	}
	if snap.Presence.RSOwner || snap.Presence.RSSpec || snap.Presence.ProgressingCondition {
		t.Fatalf("aileler yokken presence false olmalı: %+v", snap.Presence)
	}
	m[V2QRSSpecCount] = fr(fs(0))
	if snap, _ = BuildV2Snapshot([]string{V2KindDeployment}, m); snap.Presence.RSSpec {
		t.Fatal("count 0 → RSSpec false")
	}
}

func TestBuildV2SnapshotStatefulSetDaemonSet(t *testing.T) {
	m := map[string]V2QueryResult{
		V2QStsGauges: fr(
			fs(7, "__name__", "kube_statefulset_metadata_generation", "namespace", "db", "statefulset", "pg"),
			fs(7, "__name__", "kube_statefulset_status_observed_generation", "namespace", "db", "statefulset", "pg"),
			fs(3, "__name__", "kube_statefulset_replicas", "namespace", "db", "statefulset", "pg"),
			fs(3, "__name__", "kube_statefulset_status_replicas", "namespace", "db", "statefulset", "pg"),
			fs(1, "__name__", "kube_statefulset_status_replicas_updated", "namespace", "db", "statefulset", "pg"),
			fs(2, "__name__", "kube_statefulset_status_replicas_ready", "namespace", "db", "statefulset", "pg"),
			fs(2, "__name__", "kube_statefulset_status_replicas_available", "namespace", "db", "statefulset", "pg"),
			fs(2, "__name__", "kube_statefulset_metadata_generation", "namespace", "db", "statefulset", "redis"),
		),
		V2QStsSample: fr(fs(1784271068, "namespace", "db", "statefulset", "pg"), fs(1784271068, "namespace", "db", "statefulset", "redis")),
		V2QStsRevisions: fr(
			fs(1, "__name__", "kube_statefulset_status_current_revision", "namespace", "db", "statefulset", "pg", "revision", "pg-1"),
			fs(1, "__name__", "kube_statefulset_status_update_revision", "namespace", "db", "statefulset", "pg", "revision", "pg-2"),
			// redis: iki update_revision → belirsiz, boş bırakılır
			fs(1, "__name__", "kube_statefulset_status_update_revision", "namespace", "db", "statefulset", "redis", "revision", "r-1"),
			fs(1, "__name__", "kube_statefulset_status_update_revision", "namespace", "db", "statefulset", "redis", "revision", "r-2"),
		),
		V2QDsGauges: fr(
			fs(5, "__name__", "kube_daemonset_metadata_generation", "namespace", "ops", "daemonset", "agent"),
			fs(5, "__name__", "kube_daemonset_status_observed_generation", "namespace", "ops", "daemonset", "agent"),
			fs(4, "__name__", "kube_daemonset_status_desired_number_scheduled", "namespace", "ops", "daemonset", "agent"),
			fs(3, "__name__", "kube_daemonset_status_updated_number_scheduled", "namespace", "ops", "daemonset", "agent"),
			fs(4, "__name__", "kube_daemonset_status_number_available", "namespace", "ops", "daemonset", "agent"),
		),
		V2QDsSample: fr(fs(1784271068, "namespace", "ops", "daemonset", "agent")),
		V2QDsHashes: fr(
			fs(3, "namespace", "ops", "owner_name", "agent", "label_controller_revision_hash", "h2"),
			fs(1, "namespace", "ops", "owner_name", "agent", "label_controller_revision_hash", "h1"),
		),
	}
	snap, diag := BuildV2Snapshot([]string{V2KindStatefulSet, V2KindDaemonSet}, m)
	if !snap.Complete || len(snap.Workloads) != 3 {
		t.Fatalf("snapshot %+v diag %v", snap, diag)
	}
	if !snap.Presence.PodRevisionHash || snap.Presence.RSOwner {
		t.Fatalf("presence %+v", snap.Presence)
	}
	byName := map[string]V2Observation{}
	for _, o := range snap.Workloads {
		byName[o.Name] = o
	}
	pg := byName["pg"]
	if pg.Kind != V2KindStatefulSet || pg.CurrentRevision != "pg-1" || pg.UpdateRevision != "pg-2" ||
		pg.SpecReplicas != 3 || pg.StatusReplicas != 3 || pg.UpdatedReplicas != 1 || pg.ReadyReplicas != 2 || pg.AvailableReplicas != 2 {
		t.Fatalf("pg %+v", pg)
	}
	if r := byName["redis"]; r.UpdateRevision != "" || diag["sts_revision_ambiguous"] != 1 {
		t.Fatalf("belirsiz revizyon boş kalmalı ve sayılmalı: %+v %v", r, diag)
	}
	ag := byName["agent"]
	if ag.Kind != V2KindDaemonSet || ag.SpecReplicas != 4 || ag.UpdatedReplicas != 3 || ag.AvailableReplicas != 4 ||
		!reflect.DeepEqual(ag.RevisionHashes, []string{"h1", "h2"}) {
		t.Fatalf("agent %+v", ag)
	}
}

func TestV2ValueConversions(t *testing.T) {
	if v2U64(-1) != 0 || v2U64(math.NaN()) != 0 || v2U64(3.9) != 3 {
		t.Error("v2U64 negatif/NaN 0, kesir aşağı")
	}
	if v2U32(1e12) != math.MaxUint32 || v2U32(-5) != 0 {
		t.Error("v2U32 kelepçe")
	}
	if !v2SecTime(0).IsZero() || !v2SecTime(-1).IsZero() {
		t.Error("0/negatif saniye = zaman yok")
	}
	if got := v2SecTime(1784271068.123); got != time.UnixMilli(1784271068123).UTC() {
		t.Errorf("ms yuvarlama: %v", got)
	}
}

// v0.10.982 — P2.2 incelemesi: (1) türünün durumu varken DURUMU OLMAYAN iş
// yükü (mint → 'initial') primary hedeftir — ilk okumada hazır iş yükünün
// initial olayı aynı tikte succeeded kapanır, sonraki tikte hedef olmaz;
// (2) imajı boş, hedefi belli, replikası > 0 durumlar (bootstrap baseline'ı)
// fill hedefidir (ayrı, küçük bütçe); spec 0, hedefsiz ya da imajı dolu olan
// değil.
func TestV2ImageTargetsNewAndFill(t *testing.T) {
	const cid = "c-1"
	dep := func(name string, spec uint32) V2Observation {
		return V2Observation{Kind: V2KindDeployment, Namespace: "pay", Name: name, Generation: 1, SpecReplicas: spec,
			ReplicaSets: []V2ReplicaSet{{Name: name + "-a", SpecReplicas: spec}}}
	}
	snap := V2Snapshot{Complete: true, Workloads: []V2Observation{
		dep("bare", 2), dep("bare2", 1), dep("fresh", 1), dep("full", 2), dep("norev", 2), dep("zero", 0),
		{Kind: V2KindStatefulSet, Namespace: "db", Name: "pg", Generation: 1, SpecReplicas: 1}, // STS türünün durumu yok → bootstrap
	}}
	st := func(name, rev string, images ...string) V2WorkloadState {
		return V2WorkloadState{ClusterID: cid, Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: name,
			Generation: 1, IncarnationAt: detT0, CurrentRevision: rev, Images: images}
	}
	states := []V2WorkloadState{st("bare", "bare-a"), st("bare2", "bare2-a"), st("full", "full-a", "reg/full:1"),
		st("norev", ""), st("zero", "zero-a")}
	primary, fill, capped := V2ImageTargets(snap, cid, states, nil, 100, 20)
	key := func(name string) V2Key { return V2Key{cid, "pay", V2KindDeployment, name} }
	if !reflect.DeepEqual(primary, []V2Key{key("fresh")}) || capped != 0 {
		t.Fatalf("primary %v (capped %d), istenen yalnız fresh", primary, capped)
	}
	if !reflect.DeepEqual(fill, []V2Key{key("bare"), key("bare2")}) {
		t.Fatalf("fill %v, istenen bare, bare2", fill)
	}
	if _, f, _ := V2ImageTargets(snap, cid, states, nil, 100, 1); !reflect.DeepEqual(f, []V2Key{key("bare")}) {
		t.Fatalf("fill tavanı: %v", f)
	}
}

func TestV2ImageTargetsAndQueries(t *testing.T) {
	const cid = "c-1"
	obs := func(kind, ns, name string, gen uint64, rs ...string) V2Observation {
		o := V2Observation{Kind: kind, Namespace: ns, Name: name, Generation: gen}
		for _, r := range rs {
			o.ReplicaSets = append(o.ReplicaSets, V2ReplicaSet{Name: r})
		}
		return o
	}
	snap := V2Snapshot{Complete: true, Workloads: []V2Observation{
		obs(V2KindDeployment, "pay", "api", 5, "api-a", "api-b"),    // nesil artmış → hedef
		obs(V2KindDeployment, "pay", "quiet", 2, "quiet-q"),         // değişmemiş → hedef DEĞİL
		obs(V2KindDeployment, "pay", "open", 3, "open-o", "open.x"), // açık olay → hedef
		obs(V2KindStatefulSet, "db", "pg", 7),                       // bekleyen → hedef
		obs(V2KindDaemonSet, "ops", "agent", 1),                     // durum yok, türün durumu da yok (bootstrap) → hedef değil
		obs(V2KindDeployment, "pay", "evopen", 1, "evopen-1"),       // yalnız bellekteki olay açık → hedef
	}}
	st := func(kind, ns, name string, gen, pending, open uint64) V2WorkloadState {
		return V2WorkloadState{ClusterID: cid, Namespace: ns, WorkloadKind: kind, Workload: name, Generation: gen,
			PendingGeneration: pending, OpenGeneration: open, IncarnationAt: detT0}
	}
	states := []V2WorkloadState{
		st(V2KindDeployment, "pay", "api", 4, 0, 0),
		st(V2KindDeployment, "pay", "quiet", 2, 0, 0),
		st(V2KindDeployment, "pay", "open", 3, 0, 3),
		st(V2KindStatefulSet, "db", "pg", 6, 7, 0),
		st(V2KindDeployment, "pay", "evopen", 1, 0, 0),
	}
	events := []V2Event{{ClusterID: cid, Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "evopen",
		IncarnationAt: detT0, Generation: 1, Status: V2StatusStuck}}
	targets, fill, capped := V2ImageTargets(snap, cid, states, events, 100, 20)
	want := []V2Key{{cid, "db", V2KindStatefulSet, "pg"}, {cid, "pay", V2KindDeployment, "api"},
		{cid, "pay", V2KindDeployment, "evopen"}, {cid, "pay", V2KindDeployment, "open"}}
	if !reflect.DeepEqual(targets, want) || capped != 0 || len(fill) != 0 {
		t.Fatalf("hedefler %v (capped %d, fill %v), istenen %v", targets, capped, fill, want)
	}
	if tg, _, c := V2ImageTargets(snap, cid, states, events, 2, 20); len(tg) != 2 || c != 2 {
		t.Fatalf("tavan: %v %d", tg, c)
	}

	ns := `,namespace=~"pay|db"`
	qs, dropped := V2ImageQueries(snap, cid, targets, ns)
	if dropped != 0 || len(qs) != 2 {
		t.Fatalf("iki sorgu (pay Deployment, db STS/DS) beklenir: %d dropped=%d %+v", len(qs), dropped, qs)
	}
	var depQ, stsQ V2ImageQuery
	for _, q := range qs {
		if q.Deployment {
			depQ = q
		} else {
			stsQ = q
		}
	}
	for _, s := range []string{`kube_pod_container_info{namespace="pay",image!=""` + ns, `owner_kind="ReplicaSet"`,
		`owner_name=~"api-a|api-b|evopen-1|open-o|open\\.x"`, "count by (namespace, owner_name, image)", "group_left (owner_name)"} {
		if !strings.Contains(depQ.Expr, s) {
			t.Errorf("Deployment imaj sorgusunda %q yok:\n%s", s, depQ.Expr)
		}
	}
	for _, s := range []string{`namespace="db"`, `owner_kind=~"StatefulSet|DaemonSet"`, `owner_name=~"pg"`,
		"group_left (label_controller_revision_hash)", "count by (namespace, owner_kind, owner_name, label_controller_revision_hash, image)"} {
		if !strings.Contains(stsQ.Expr, s) {
			t.Errorf("STS imaj sorgusunda %q yok:\n%s", s, stsQ.Expr)
		}
	}
	if strings.Count(depQ.Expr, "{") != strings.Count(depQ.Expr, ns) {
		t.Errorf("imaj sorgusunda her seçici kalkanı taşımalı: %s", depQ.Expr)
	}

	// Uygula: Deployment sonucu RS adına göre, STS revizyon hash'ine göre.
	ApplyV2Images(&snap, cid, depQ, fr(
		fs(2, "namespace", "pay", "owner_name", "api-a", "image", "reg/api:1"),
		fs(1, "namespace", "pay", "owner_name", "api-b", "image", "reg/api:2"),
		fs(1, "namespace", "pay", "owner_name", "api-b", "image", "reg/sidecar:9"),
	))
	ApplyV2Images(&snap, cid, stsQ, fr(
		fs(3, "namespace", "db", "owner_kind", "StatefulSet", "owner_name", "pg", "label_controller_revision_hash", "pg-2", "image", "pg:16"),
	))
	got := map[string]map[string][]string{}
	for _, o := range snap.Workloads {
		got[o.Name] = o.RevisionImages
	}
	if !reflect.DeepEqual(got["api"], map[string][]string{"api-a": {"reg/api:1"}, "api-b": {"reg/api:2", "reg/sidecar:9"}}) {
		t.Errorf("api imajları %v", got["api"])
	}
	if got["open"] == nil || len(got["open"]) != 0 {
		t.Errorf("okunup boş gelen iş yükü {} olmalı (nil = okunmadı): %#v", got["open"])
	}
	if got["quiet"] != nil || got["agent"] != nil {
		t.Errorf("hedef olmayan iş yükü nil kalmalı: %v %v", got["quiet"], got["agent"])
	}
	if !reflect.DeepEqual(got["pg"], map[string][]string{"pg-2": {"pg:16"}}) {
		t.Errorf("pg imajları %v", got["pg"])
	}

	// Kısmi imaj sonucu uygulanmaz (RevisionImages nil kalır).
	snap2 := V2Snapshot{Complete: true, Workloads: []V2Observation{obs(V2KindDeployment, "pay", "api", 5, "api-a")}}
	bad := fr(fs(1, "namespace", "pay", "owner_name", "api-a", "image", "x"))
	bad.Partial = true
	q2, _ := V2ImageQueries(snap2, cid, []V2Key{{cid, "pay", V2KindDeployment, "api"}}, "")
	ApplyV2Images(&snap2, cid, q2[0], bad)
	if snap2.Workloads[0].RevisionImages != nil {
		t.Fatal("kısmi imaj okuması uygulanmamalı")
	}
}

func TestV2ImageQueriesChunkAndCap(t *testing.T) {
	const cid = "c-1"
	var snap V2Snapshot
	var targets []V2Key
	for i := 0; i < 30; i++ {
		name := "w" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
		o := V2Observation{Kind: V2KindDeployment, Namespace: "pay", Name: name}
		for j := 0; j < 11; j++ {
			o.ReplicaSets = append(o.ReplicaSets, V2ReplicaSet{Name: name + "-" + string(rune('a'+j))})
		}
		snap.Workloads = append(snap.Workloads, o)
		targets = append(targets, V2Key{cid, "pay", V2KindDeployment, name})
	}
	qs, dropped := V2ImageQueries(snap, cid, targets, "")
	if dropped != 0 {
		t.Fatalf("30 iş yükü tavan içinde: dropped %d", dropped)
	}
	total := 0
	for _, q := range qs {
		if n := strings.Count(q.Expr, "|") + 1; n > v2ImageNamesPerQuery {
			t.Errorf("sorgu başına ad tavanı aşıldı: %d", n)
		}
		total += len(q.Workloads)
	}
	if total != 30 {
		t.Fatalf("her hedef tam bir sorguda: %d", total)
	}
	// Sorgu tavanı: fazlası düşer ve sayılır.
	for i := 0; i < 400; i++ {
		name := "z" + strings.Repeat("y", i)
		snap.Workloads = append(snap.Workloads, V2Observation{Kind: V2KindDeployment, Namespace: "ns" + string(rune('a'+i%26)), Name: name,
			ReplicaSets: []V2ReplicaSet{{Name: name + "-1"}}})
		targets = append(targets, V2Key{cid, "ns" + string(rune('a'+i%26)), V2KindDeployment, name})
	}
	qs, dropped = V2ImageQueries(snap, cid, targets, "")
	if len(qs) > v2ImageMaxQueries || dropped == 0 {
		t.Fatalf("sorgu tavanı %d (dropped %d)", len(qs), dropped)
	}
}
