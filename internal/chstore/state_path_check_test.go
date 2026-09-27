package chstore

// state_path_check_test.go — v0.10.965 — State tablolarının ZK yolu denetimi
// (Replika tutarlılığı kartı). Prod bulgusu (operatör, 2026-09-27, v0.10.960,
// 4 host / 2 shard): on state tablosu eski '/clickhouse/tables/<shard>/<ad>'
// yolunda, her biri iki replikasyon grubuna bölünmüş; kural 3 yüzünden boot
// sonraki her yeni state tablosunu da eski yola kuruyor. Fikstür sentetik:
// host-1..host-4, shard 01/02.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestMergeObservedStatePathLegacyWins — boot'un birleştirme anlamı pinli:
// eski yol kazanır; iki eski yol çakışırsa ilk görülen kalır.
func TestMergeObservedStatePathLegacyWins(t *testing.T) {
	const pre = "/clickhouse/tables"
	uni, l1, l2 := pre+"/state/problems", pre+"/01/problems", pre+"/02/problems"
	cases := []struct {
		name      string
		seq       []string
		want      string
		wantSplit []bool
	}{
		{"tek yol", []string{l1}, l1, []bool{false}},
		{"aynı yol iki kez", []string{uni, uni}, uni, []bool{false, false}},
		{"birleşik sonra eski → eski kazanır", []string{uni, l1}, l1, []bool{false, true}},
		{"eski sonra birleşik → eski kalır", []string{l1, uni}, l1, []bool{false, true}},
		{"iki eski → ilk görülen kalır", []string{l1, l2}, l1, []bool{false, true}},
		{"iki eski ters sıra", []string{l2, l1}, l2, []bool{false, true}},
		{"birleşik, eski, eski", []string{uni, l2, l1}, l2, []bool{false, true, true}},
	}
	for _, c := range cases {
		paths := map[string]string{}
		for i, p := range c.seq {
			if got := mergeObservedStatePath(paths, "problems", p, pre); got != c.wantSplit[i] {
				t.Errorf("[%s] adım %d split=%v, beklenen %v", c.name, i, got, c.wantSplit[i])
			}
		}
		if paths["problems"] != c.want {
			t.Errorf("[%s] = %q, beklenen %q", c.name, paths["problems"], c.want)
		}
	}
}

// TestResolveStateReplicaPathsUsesMergeHelper — kaynak pini: boot'un döngüsü
// ortak gövdeyi çağırır ve log satırını korur (kart ile boot ayrışamaz).
func TestResolveStateReplicaPathsUsesMergeHelper(t *testing.T) {
	b, err := os.ReadFile("state_replication.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	at := strings.Index(src, "func (s *Store) resolveStateReplicaPaths(")
	end := strings.Index(src[at:], "\n}\n")
	body := src[at : at+end]
	for _, want := range []string{
		"mergeObservedStatePath(obs.paths, t, p, zkPrefix)",
		"İKİ farklı ZK yolunda görüldü",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("resolveStateReplicaPaths %q içermiyor", want)
		}
	}
	if strings.Contains(body, "eski olan kazanır") {
		t.Error("birleştirme kuralı boot gövdesinde kopya kalmış — tek gövde mergeObservedStatePath")
	}
}

func TestLegacyStatePath(t *testing.T) {
	const pre = "/clickhouse/tables"
	cases := []struct {
		path string
		want bool
	}{
		{"/clickhouse/tables/01/x", true},
		{"/clickhouse/tables/02/x", true},
		{"/clickhouse/tables/state/x", false},       // birleşik
		{"/clickhouse/tables/01/nested/x", false},   // iç içe
		{"/other/tables/01/x", false},               // başka önek
		{"/clickhouse/tables/01/y", false},          // başka ad
		{"/clickhouse/tables//x", false},            // boş segment
		{"/clickhouse/tables/x", false},             // segmentsiz
		{"/clickhouse/tablesX/01/x", false},         // önek sınırı
		{"/clickhouse/tables/01/x/replicas", false}, // alt yol
	}
	for _, c := range cases {
		if got := legacyStatePath(pre, "x", c.path); got != c.want {
			t.Errorf("legacyStatePath(%q) = %v, beklenen %v", c.path, got, c.want)
		}
	}
}

var spHosts = []string{"host-1", "host-2", "host-3", "host-4"}

func spShard(h string) string {
	if h == "host-1" || h == "host-2" {
		return "01"
	}
	return "02"
}

// spLegacyRows — tablo dört host'ta shard'lı yolda; satır host başına.
func spLegacyRows(table string, rows map[string]uint64) []ReplicaState {
	var out []ReplicaState
	for _, h := range spHosts {
		out = append(out, ReplicaState{Host: h, ZKPath: "/clickhouse/tables/" + spShard(h) + "/" + table, TotalRows: rows[h]})
	}
	return out
}

func spUnifiedRows(table string, hosts ...string) []ReplicaState {
	var out []ReplicaState
	for _, h := range hosts {
		out = append(out, ReplicaState{Host: h, ZKPath: "/clickhouse/tables/state/" + table})
	}
	return out
}

func spEngines(byTable map[string][]ReplicaState) map[string]map[string]string {
	out := map[string]map[string]string{}
	for t, rs := range byTable {
		for _, r := range rs {
			if out[t] == nil {
				out[t] = map[string]string{}
			}
			out[t][r.Host] = "ReplicatedReplacingMergeTree"
		}
	}
	return out
}

func TestStatePathCheckFor(t *testing.T) {
	const pre = "/clickhouse/tables"
	byTable := map[string][]ReplicaState{
		"ingest_ledger": spLegacyRows("ingest_ledger", map[string]uint64{"host-1": 1234, "host-2": 1200, "host-3": 1100, "host-4": 1099}),
		"users":         spUnifiedRows("users", spHosts...),
		// Boot'un süzgeci: bunlar sayılmaz.
		".inner_id.45f12d2e-36ac-4cb4-8bef-5025847af024": spLegacyRows(".inner_id.45f12d2e-36ac-4cb4-8bef-5025847af024", nil),
		"spans_local":  spLegacyRows("spans_local", nil),
		"problems_old": spLegacyRows("problems_old", nil),
		// Karışık: iki host birleşik, iki host eski.
		"ai_eval_runs": append(spUnifiedRows("ai_eval_runs", "host-1", "host-2"),
			ReplicaState{Host: "host-3", ZKPath: pre + "/02/ai_eval_runs"}, ReplicaState{Host: "host-4", ZKPath: pre + "/02/ai_eval_runs"}),
		// İzinli ad, host-3'te yok, eski grubu yok → absent.
		"rollout_events": spUnifiedRows("rollout_events", "host-1", "host-2", "host-4"),
		// İzin listesi dışı eski tablo → listelenir, rebuildable=false.
		"alert_rules": spLegacyRows("alert_rules", nil),
	}
	engineOf := spEngines(byTable)
	// Düşürülmüş (system.replicas'ta görünmeyen) izinli ad: absent, grupsuz.
	chk := statePathCheckFor(byTable, engineOf, spHosts, 4, pre)

	byName := map[string]StatePathTable{}
	var order []string
	for _, l := range chk.Legacy {
		byName[l.Table] = l
		order = append(order, l.Table)
	}
	il, ok := byName["ingest_ledger"]
	if !ok {
		t.Fatalf("ingest_ledger listelenmedi: %v", order)
	}
	if il.Kind != "legacy" || !il.Rebuildable || il.Class != "derived" || !strings.Contains(il.ClassNote, "2026-09-27") {
		t.Errorf("ingest_ledger = %+v", il)
	}
	if len(il.Groups) != 2 || len(il.Groups[0].Hosts) != 2 || len(il.Groups[1].Hosts) != 2 {
		t.Fatalf("ingest_ledger grupları 2×2 değil: %+v", il.Groups)
	}
	if il.Groups[0].Path != pre+"/01/ingest_ledger" || il.Groups[0].Rows != 1234 || il.Groups[1].Rows != 1100 || il.Rows != 2334 {
		t.Errorf("grup satırı = en dolu host, tablo = toplam: %+v (rows %d)", il.Groups, il.Rows)
	}
	if il.Groups[0].Unified || il.Groups[1].Unified {
		t.Error("eski gruplar unified=false olmalı")
	}
	if byName["ai_eval_runs"].Kind != "mixed" {
		t.Errorf("ai_eval_runs kind = %q, beklenen mixed", byName["ai_eval_runs"].Kind)
	}
	if byName["rollout_events"].Kind != "absent" {
		t.Errorf("rollout_events (host-3'te yok) kind = %q, beklenen absent", byName["rollout_events"].Kind)
	}
	for _, n := range []string{"rollout_workload_state", "rollout_worker_runs"} {
		if l, ok := byName[n]; !ok || l.Kind != "absent" || l.Groups == nil || len(l.Groups) != 0 {
			t.Errorf("%s (hiçbir yerde yok) = %+v, beklenen absent + [] grup", n, l)
		}
	}
	ar, ok := byName["alert_rules"]
	if !ok || ar.Rebuildable || ar.Class != "" || ar.Kind != "legacy" {
		t.Errorf("izin listesi dışı eski tablo = %+v, beklenen legacy + rebuildable=false", ar)
	}
	for _, n := range []string{"users", ".inner_id.45f12d2e-36ac-4cb4-8bef-5025847af024", "spans_local", "problems_old"} {
		if _, ok := byName[n]; ok {
			t.Errorf("%s listelenmemeli", n)
		}
	}
	if chk.Unified != 1 {
		t.Errorf("Unified = %d, beklenen 1 (users)", chk.Unified)
	}
	// Sıra: izin listesi sırası, sonra ad.
	if order[0] != "ingest_ledger" || order[1] != "ai_eval_runs" || order[len(order)-1] != "alert_rules" {
		t.Errorf("sıra = %v", order)
	}
	// Kilit metni boot'un kendi kuralından.
	obs := stateObservation{ok: true, paths: map[string]string{}}
	for _, n := range []string{"ingest_ledger", "users", "ai_eval_runs", "rollout_events", "alert_rules"} {
		for _, r := range byTable[n] {
			mergeObservedStatePath(obs.paths, n, r.ZKPath, pre)
		}
	}
	wantOpen, wantReason := useUnifiedStatePath(obs, pre, "")
	if chk.LockOpen != wantOpen || chk.LockReason != wantReason || chk.LockOpen {
		t.Errorf("kilit = (%v, %q), beklenen (%v, %q)", chk.LockOpen, chk.LockReason, wantOpen, wantReason)
	}
	if !strings.Contains(chk.LockReason, "3 state tablosu eski yolda") {
		t.Errorf("LockReason = %q (ingest_ledger + ai_eval_runs + alert_rules)", chk.LockReason)
	}
	if !chk.Complete || chk.Unreachable != 0 || chk.ZKPrefix != pre {
		t.Errorf("tam roster: %+v", chk)
	}

	// Hepsi birleşik → kilit açık, liste boş (null değil).
	all := map[string][]ReplicaState{}
	for _, e := range statePathAllowlist {
		all[e.Name] = spUnifiedRows(e.Name, spHosts...)
	}
	all["users"] = spUnifiedRows("users", spHosts...)
	open := statePathCheckFor(all, spEngines(all), spHosts, 4, pre)
	if !open.LockOpen || len(open.Legacy) != 0 || open.Unified != 11 {
		t.Errorf("hepsi birleşik: %+v", open)
	}
	if open.LockReason != "taze veya göç SONRASI kurulum" {
		t.Errorf("LockReason = %q", open.LockReason)
	}

	// Eksik roster: 3 cevap / 4 tanım.
	part := statePathCheckFor(all, spEngines(all), spHosts[:3], 4, pre)
	if part.Complete || part.Unreachable != 1 {
		t.Errorf("eksik roster Complete=%v Unreachable=%d", part.Complete, part.Unreachable)
	}

	// JSON: null dilim yok.
	b, err := json.Marshal(open)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("JSON null taşıyor: %s", b)
	}
	b, _ = json.Marshal(chk)
	if strings.Contains(string(b), "null") {
		t.Errorf("JSON null taşıyor: %s", b)
	}
}

// TestStatePathCheckForLegacyPartialListedOnce — v0.10.965 — yarım DROP:
// ingest_ledger host-3'te yok ama öteki host'larda ESKİ yolda. Tek satır
// ("legacy"), ikinci bir "absent" satırı yok (React anahtarı da yinelenirdi).
func TestStatePathCheckForLegacyPartialListedOnce(t *testing.T) {
	const pre = "/clickhouse/tables"
	byTable := map[string][]ReplicaState{}
	for _, e := range statePathAllowlist {
		byTable[e.Name] = spUnifiedRows(e.Name, spHosts...)
	}
	var il []ReplicaState
	for _, r := range spLegacyRows("ingest_ledger", nil) {
		if r.Host != "host-3" {
			il = append(il, r)
		}
	}
	byTable["ingest_ledger"] = il
	chk := statePathCheckFor(byTable, spEngines(byTable), spHosts, 4, pre)
	n := 0
	for _, l := range chk.Legacy {
		if l.Table == "ingest_ledger" {
			n++
			if l.Kind != "legacy" {
				t.Errorf("ingest_ledger kind = %q, beklenen legacy", l.Kind)
			}
		}
	}
	if n != 1 {
		t.Errorf("ingest_ledger %d kez listelendi: %+v", n, chk.Legacy)
	}
}

// TestStatePathLockNeverKeyedByEmptyName — kilit useUnifiedStatePath(obs,
// önek, "") ile sorulur: "" bir tablo adı olamaz, yani kural 2 hiç
// tetiklenmez ve cevap kural 3/4'tür.
func TestStatePathLockNeverKeyedByEmptyName(t *testing.T) {
	if stateProbeTable("") {
		t.Fatal("stateProbeTable(\"\") true — kilit sorgusu kural 2'ye düşebilir")
	}
	chk := statePathCheckFor(map[string][]ReplicaState{"": spLegacyRows("", nil)}, nil, nil, 4, "/clickhouse/tables")
	if !chk.LockOpen || len(chk.Legacy) != 0 || chk.LockReason != "taze veya göç SONRASI kurulum" {
		t.Errorf("boş ad gözleme girdi: %+v", chk)
	}
}

func TestStatePathKind(t *testing.T) {
	const pre = "/clickhouse/tables"
	if k := statePathKind(nil, pre, "x"); k != "" {
		t.Errorf("satırsız = %q", k)
	}
	if k := statePathKind(spUnifiedRows("x", spHosts...), pre, "x"); k != "" {
		t.Errorf("hepsi birleşik = %q", k)
	}
	if k := statePathKind(spLegacyRows("x", nil), pre, "x"); k != "legacy" {
		t.Errorf("eski = %q", k)
	}
	mixed := append(spUnifiedRows("x", "host-1"), spLegacyRows("x", nil)[1:]...)
	if k := statePathKind(mixed, pre, "x"); k != "mixed" {
		t.Errorf("karışık = %q", k)
	}
}

// TestReplicaConsistencyWiresStatePathCheck — kaynak pini: rapor denetimi
// parçalar okunduktan SONRA, gruplamadan ÖNCE kurar; satır alanı yalnız
// state adaylarında dolar. Yeni okuma yok (skip bayrağı pini ayrı testte).
func TestReplicaConsistencyWiresStatePathCheck(t *testing.T) {
	b, err := os.ReadFile("replica_consistency.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	assign := "out.StatePaths = statePathCheckFor(byTable, engineOf, hostNames, len(hostRows), s.zkPrefix())"
	i := strings.Index(src, assign)
	parts := strings.Index(src, "clusterAllReplicas('%s', system.parts)")
	group := strings.Index(src, "// Grupla + karar.")
	if i < 0 || parts < 0 || group < 0 || !(parts < i && i < group) {
		t.Errorf("statePathCheckFor atanması parçalar okunduktan sonra, gruplamadan önce olmalı (parts=%d assign=%d group=%d)", parts, i, group)
	}
	for _, want := range []string{
		"StatePaths *StatePathCheck `json:\"statePaths,omitempty\"`",
		"StatePath string `json:\"statePath,omitempty\"`",
		"if stateProbeTable(t) {\n\t\t\ttbl.StatePath = statePathKind(byTable[t], s.zkPrefix(), t)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if n := strings.Count(src, "clusterAllReplicas('%s'"); n != 6 {
		t.Errorf("clusterAllReplicas okuması %d — denetim yeni okuma açmamalı (6)", n)
	}
}
