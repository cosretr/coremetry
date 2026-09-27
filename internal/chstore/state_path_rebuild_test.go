package chstore

// state_path_rebuild_test.go — v0.10.965 — State tablolarını birleşik ZK
// yolunda yeniden kurma sihirbazı. Operatör kararları 2026-09-27: on tablo
// (ingest_ledger, ai_eval_runs + sekiz Rollouts v2) DROP + CREATE, veri kaybı
// kabul, taşıma yok. v0.10.971 — boot'un kural 3'ü KALDIRILDI (operatör
// kararı 2026-09-27, öneri 2): "kilit" yok, kısmi seçim onay (partialOK)
// istemez; "eski yolda kalanlar" (stillLegacyAfter / stillLegacy) yalnız
// bilgidir. Veri güvenliği kapılarının hepsi aynen durur.
//
// Canlı ClickHouse YOK: saf tablolar + sprConn (driver.Conn gömülü sahte;
// mv_inner_conn_test.go scriptRow tip anahtarı ve rv2PreConn alt dize
// yönlendirmesi emsali). Sahte host → tablo → {motor, yol, replika, satır}
// tutar; Exec DROP/CREATE'i bu duruma uygular. Host adları sentetik
// (host-1..host-4, küme uptrace_all).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/cilcenk/coremetry/internal/config"
)

// ───────────────────────── saf tablolar ─────────────────────────

func TestStatePathAllowlist(t *testing.T) {
	want := []string{"ingest_ledger", "ai_eval_runs", "rollout_events", "rollout_workload_state", "argocd_app_status",
		"argocd_sync_events", "argocd_app_mapping", "rollout_classification", "ado_commit_enrichment", "rollout_worker_runs"}
	var got []string
	for _, e := range statePathAllowlist {
		got = append(got, e.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("izin listesi = %v", got)
	}
	if !slices.Equal(got[2:], rolloutV2TableNames()) {
		t.Errorf("sekiz = %v, rolloutV2TableNames = %v", got[2:], rolloutV2TableNames())
	}
	cat := canonicalTables(30, 30, 7)
	preserve := map[string]bool{}
	for _, n := range configPreserveTables {
		preserve[n] = true
	}
	var inter []string
	for _, e := range statePathAllowlist {
		if tableDDLByName(cat, e.Name) == "" {
			t.Errorf("%s kanonik katalogda yok", e.Name)
		}
		if !stateProbeTable(e.Name) || highVolumeTables[e.Name] {
			t.Errorf("%s state tablosu değil", e.Name)
		}
		if !StatePathRebuildAllowed(e.Name) {
			t.Errorf("StatePathRebuildAllowed(%s) false", e.Name)
		}
		if preserve[e.Name] {
			inter = append(inter, e.Name)
		}
	}
	if !slices.Contains(telemetryPurgeTables, "ingest_ledger") {
		t.Error("ingest_ledger telemetryPurgeTables'ta değil — derived sınıfının gerekçesi çöker")
	}
	sort.Strings(inter)
	if !slices.Equal(inter, []string{"ai_eval_runs", "argocd_sync_events"}) {
		t.Errorf("izin listesi ∩ configPreserveTables = %v — yalnız operatör kararıyla (2026-09-27) ai_eval_runs ve boşken argocd_sync_events", inter)
	}
	ev, _ := statePathEntryFor("ai_eval_runs")
	if ev.Class != statePathClassOperatorException || !strings.Contains(ev.Note, "2026-09-27") {
		t.Errorf("ai_eval_runs = %+v", ev)
	}
	se, _ := statePathEntryFor("argocd_sync_events")
	if se.Class != statePathClassEmptyOnly || !strings.Contains(se.Note, "satır varsa asla") {
		t.Errorf("argocd_sync_events = %+v", se)
	}
	il, _ := statePathEntryFor("ingest_ledger")
	if il.Class != statePathClassDerived || !strings.Contains(il.Note, "öneri a") {
		t.Errorf("ingest_ledger = %+v", il)
	}
	for _, n := range []string{"problems", "", "ingest_ledger_old", "INGEST_LEDGER"} {
		if StatePathRebuildAllowed(n) {
			t.Errorf("StatePathRebuildAllowed(%q) true", n)
		}
	}
	if StatePathEvalFresh != 15*time.Minute {
		t.Errorf("StatePathEvalFresh = %v", StatePathEvalFresh)
	}
}

// spRep — sınıflandırma girdisi.
type spRep struct {
	eng    string
	path   string
	name   string
	total  int
	active int
	ro     bool
}

func spClassify(name string, per map[string]spRep) (string, string, string) {
	engines := map[string]string{}
	reps := map[string]statePathReplica{}
	for h, r := range per {
		if r.eng != "" {
			engines[h] = r.eng
		}
		if r.path != "" {
			reps[h] = statePathReplica{Path: r.path, ReplicaName: r.name, Total: r.total, Active: r.active, ReadOnly: r.ro}
		}
	}
	return classifyStatePathTable(name, spHosts, engines, reps, "/clickhouse/tables")
}

func TestClassifyStatePathTable(t *testing.T) {
	const eng = "ReplicatedReplacingMergeTree"
	uni := "/clickhouse/tables/state/x"
	leg := func(h string) string { return "/clickhouse/tables/" + spShard(h) + "/x" }
	all := func(f func(h string) spRep) map[string]spRep {
		out := map[string]spRep{}
		for _, h := range spHosts {
			out[h] = f(h)
		}
		return out
	}
	some := func(hs []string, f func(h string) spRep) map[string]spRep {
		out := map[string]spRep{}
		for _, h := range hs {
			out[h] = f(h)
		}
		return out
	}
	u := func(total, active int) func(string) spRep {
		return func(h string) spRep {
			return spRep{eng: eng, path: uni, name: spShard(h) + "-" + h, total: total, active: active}
		}
	}
	l := func(h string) spRep { return spRep{eng: eng, path: leg(h), name: "r", total: 2, active: 2} }
	cases := []struct {
		label, state, action, detail string
		per                          map[string]spRep
	}{
		{"birleşik N/N", "unified", "skip", "", all(u(4, 4))},
		{"hiçbir host'ta yok", "absent", "create", "", map[string]spRep{}},
		{"birleşik, bir host'ta yok", "unified_partial", "create", "", some(spHosts[:3], u(3, 3))},
		{"eski N/N", "legacy", "rebuild", "", all(l)},
		{"eski, yarım DROP", "legacy_partial", "rebuild", "önceki DROP yarım", some(spHosts[2:], l)},
		{"karışık", "mixed", "rebuild", "", func() map[string]spRep {
			m := some(spHosts[:2], u(2, 2))
			for k, v := range some(spHosts[2:], l) {
				m[k] = v
			}
			return m
		}()},
		{"düz motor", "unknown", "blocked", "Replicated değil", func() map[string]spRep {
			m := all(l)
			m["host-2"] = spRep{eng: "ReplacingMergeTree"}
			return m
		}()},
		{"readonly", "unknown", "blocked", "readonly", func() map[string]spRep {
			m := all(l)
			r := m["host-3"]
			r.ro = true
			m["host-3"] = r
			return m
		}()},
		{"tanınmayan yol", "unknown", "blocked", "tanınmayan ZK yolu", func() map[string]spRep {
			m := all(l)
			m["host-4"] = spRep{eng: eng, path: "/elsewhere/01/x", name: "r"}
			return m
		}()},
		{"iç içe yol", "unknown", "blocked", "tanınmayan ZK yolu", func() map[string]spRep {
			m := all(l)
			m["host-4"] = spRep{eng: eng, path: "/clickhouse/tables/02/x/extra", name: "r"}
			return m
		}()},
		{"birleşik N/N aktif eksik", "unknown", "blocked", "birleşik ama aktif replika eksik — Replika tutarlılığı satırına bak", all(u(4, 3))},
		{"birleşik N/N fazla kayıt", "unknown", "blocked", "yol paylaşılıyor", all(u(5, 4))},
		{"Replicated ama replicas satırı yok", "unknown", "blocked", "system.replicas satırı yok", func() map[string]spRep {
			m := all(l)
			m["host-1"] = spRep{eng: eng}
			return m
		}()},
	}
	for _, c := range cases {
		st, act, detail := spClassify("x", c.per)
		if st != c.state || act != c.action || !strings.Contains(detail, c.detail) || (c.detail == "" && detail != "") {
			t.Errorf("[%s] = (%s, %s, %q), beklenen (%s, %s, …%q…)", c.label, st, act, detail, c.state, c.action, c.detail)
		}
	}
}

func TestStatePathVerified(t *testing.T) {
	const pre = "/clickhouse/tables"
	good := func() (map[string]string, map[string]statePathReplica) {
		e, r := map[string]string{}, map[string]statePathReplica{}
		for _, h := range spHosts {
			e[h] = "ReplicatedReplacingMergeTree"
			r[h] = statePathReplica{Path: pre + "/state/x", ReplicaName: spShard(h) + "-" + h, Total: 4, Active: 4}
		}
		return e, r
	}
	e, r := good()
	if !statePathVerified("x", spHosts, e, r, pre) {
		t.Fatal("sağlam tablo doğrulanmadı")
	}
	muts := map[string]func(e map[string]string, r map[string]statePathReplica){
		"bir host'ta yok": func(e map[string]string, r map[string]statePathReplica) { delete(e, "host-2"); delete(r, "host-2") },
		"düz motor":       func(e map[string]string, _ map[string]statePathReplica) { e["host-2"] = "ReplacingMergeTree" },
		"eski yol": func(_ map[string]string, r map[string]statePathReplica) {
			x := r["host-3"]
			x.Path = pre + "/02/x"
			r["host-3"] = x
		},
		"aynı replika adı": func(_ map[string]string, r map[string]statePathReplica) {
			x := r["host-2"]
			x.ReplicaName = r["host-1"].ReplicaName
			r["host-2"] = x
		},
		"aktif eksik": func(_ map[string]string, r map[string]statePathReplica) {
			x := r["host-4"]
			x.Active = 3
			r["host-4"] = x
		},
		"readonly": func(_ map[string]string, r map[string]statePathReplica) {
			x := r["host-4"]
			x.ReadOnly = true
			r["host-4"] = x
		},
		// v0.10.965 — yalnız total bozuk (active N): total == N koşulu tek başına pinli.
		"total fazla": func(_ map[string]string, r map[string]statePathReplica) {
			x := r["host-4"]
			x.Total = 5
			r["host-4"] = x
		},
	}
	for label, mut := range muts {
		e, r := good()
		mut(e, r)
		if statePathVerified("x", spHosts, e, r, pre) {
			t.Errorf("[%s] doğrulandı sayıldı", label)
		}
	}
	if statePathVerified("x", nil, e, r, pre) {
		t.Error("host'suz doğrulama true")
	}
}

func TestStatePathAckGate(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	if msg := statePathAckAge(now.Add(-29*time.Minute).UnixMilli(), now); msg != "" {
		t.Errorf("29 dk'lık onay bayat sayıldı: %s", msg)
	}
	// v0.10.965 — yaş tam dakika ("31m5s" değil); gelecek tarih eksi süre değil.
	if msg := statePathAckAge(now.Add(-31*time.Minute-5*time.Second).UnixMilli(), now); msg != "onay bayat (31 dk önce ölçüldü; en çok 30 dk) — yeniden planla" {
		t.Errorf("31 dk'lık onay = %q", msg)
	}
	if msg := statePathAckAge(now.Add(time.Minute).UnixMilli(), now); msg != "onay bayat (ölçüm zamanı gelecekte; en çok 30 dk) — yeniden planla" {
		t.Errorf("gelecekteki onay = %q", msg)
	}
	il, _ := statePathEntryFor("ingest_ledger")
	ev, _ := statePathEntryFor("ai_eval_runs")
	re, _ := statePathEntryFor("rollout_events")
	ack := func(state string, rows uint64) *StatePathAckTable {
		return &StatePathAckTable{State: state, Rows: rows}
	}
	cases := []struct {
		label string
		e     statePathEntry
		state string
		rows  uint64
		ack   *StatePathAckTable
		want  string // "" = geçer
	}{
		{"durum değişti", il, "unified", 0, ack("legacy", 0), "durum değişti (onay: eski yol, şimdi: birleşik)"},
		{"durum değişti, tanınmayan onay durumu aynen", il, "legacy", 0, ack("bogus", 0), "(onay: bogus, şimdi: eski yol)"},
		{"empty_only 1 satır", re, "legacy", 1, ack("legacy", 0), "yalnız BOŞKEN"},
		{"empty_only 1 satır, onaysız", re, "legacy", 1, nil, "yalnız BOŞKEN"},
		{"0 satır onaysız", re, "legacy", 0, nil, ""},
		{"0 satır ingest_ledger onaysız", il, "absent", 0, nil, ""},
		{"satır var onay yok", il, "legacy", 5, nil, "5 satır var ama onay yok"},
		{"defter pay içinde (+200)", il, "legacy", 100200, ack("legacy", 100000), ""},
		{"defter pay içinde (küçük onay +10000)", il, "legacy", 10100, ack("legacy", 100), ""},
		{"defter pay dışında (+50000)", il, "legacy", 150000, ack("legacy", 100000), "onaylanan 100.000, şimdi 150.000"},
		{"satır var onay yok (binlik)", il, "legacy", 12345, nil, "12.345 satır var ama onay yok"},
		{"defter azaldı (TTL)", il, "legacy", 900, ack("legacy", 1000), ""},
		{"eval aynı", ev, "legacy", 5, ack("legacy", 5), ""},
		{"eval +1", ev, "legacy", 6, ack("legacy", 5), "onaylanan 5, şimdi 6"},
		{"eval azaldı", ev, "legacy", 4, ack("legacy", 5), ""},
	}
	for _, c := range cases {
		got := statePathAckGate(c.e, c.state, c.rows, c.ack)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("[%s] = %q, beklenen …%q…", c.label, got, c.want)
		}
	}
	// Onayda olmayan ATLA satırı sınanmaz; onay yoksa (nil) tek engel.
	tables := []StatePathRebuildTable{{Table: "users_x", Action: "skip"}, {Table: "ai_eval_runs", State: "unified", Action: "skip", Rows: 9}}
	if msgs := statePathAckAll(tables, &StatePathRebuildAck{MeasuredAt: time.Now().UnixMilli()}, time.Now()); len(msgs) != 0 {
		t.Errorf("atla satırı sınandı: %v", msgs)
	}
	if msgs := statePathAckAll(tables, nil, time.Now()); len(msgs) != 1 || !strings.Contains(msgs[0], "ack zorunlu") {
		t.Errorf("nil onay = %v", msgs)
	}
	// Planda ATLA'ydı (onaya girmedi), şimdi düşecek: 0 satırda bile ret.
	drift := []StatePathRebuildTable{{Table: "rollout_events", State: "legacy", Action: "rebuild"}}
	if msgs := statePathAckAll(drift, &StatePathRebuildAck{MeasuredAt: time.Now().UnixMilli()}, time.Now()); len(msgs) != 1 || msgs[0] != "rollout_events: plan onayında yok (şimdi: eski yol) — yeniden planla" {
		t.Errorf("onaysız eylem = %v", msgs)
	}
}

func TestStatePathWritersGate(t *testing.T) {
	cases := []struct {
		label      string
		ro, ar     string
		err        error
		wantBlocks string
	}{
		{"v1 + kapalı", `{"enabled":true,"source":"v1"}`, `{"enabled":false}`, nil, ""},
		{"boş kaynak", `{"enabled":true}`, ``, nil, ""},
		{"blob yok", ``, ``, nil, ""},
		{"v2", `{"source":"v2"}`, `{"enabled":false}`, nil, `rollouts.source="v2"`},
		{"argocd açık", `{"source":"v1"}`, `{"enabled":true}`, nil, "argocd.enabled=true"},
		{"okuma hatası", ``, ``, errors.New("timeout"), "okunamadı (timeout)"},
		{"bozuk blob", `{`, ``, nil, "çözülemedi"},
	}
	for _, c := range cases {
		got := statePathWritersGate([]byte(c.ro), []byte(c.ar), c.err)
		if (c.wantBlocks == "") != (got == "") || !strings.Contains(got, c.wantBlocks) {
			t.Errorf("[%s] = %q, beklenen …%q…", c.label, got, c.wantBlocks)
		}
	}
}

func TestUnifiedZKGate(t *testing.T) {
	const p = "/clickhouse/tables/state/x"
	exp := []string{"01-r1", "01-r2", "02-r1", "02-r2"}
	cases := []struct {
		label  string
		owners []string
		err    error
		live   []string
		want   string
	}{
		{"ZNONODE", nil, errors.New("Code: 999. Coordination::Exception: No node, path: " + p + "/replicas"), nil, ""},
		{"çocuksuz", []string{}, nil, nil, ""},
		{"kısmi: sahipler ⊂ beklenen", []string{"01-r1", "01-r2"}, nil, []string{"01-r1", "01-r2"}, ""},
		{"kısmi: yabancı sahip", []string{"01-r1", "other-r9"}, nil, []string{"01-r1"}, "başka replikalarca tutuluyor (01-r1, other-r9)"},
		// v0.10.965 — beklenen ad ama canlı değil: BAYAT kayıt (CREATE 253 / drop_wait boşalmaz) → DROP'tan önce ret.
		{"kısmi: bayat sahip (beklenen ad, canlı değil)", []string{"01-r1", "01-r2", "02-r1"}, nil, []string{"01-r1", "01-r2"}, "replika kaydı kalmış (02-r1)"},
		{"karışık: eski host'un bayat birleşik kaydı", []string{"01-r1", "02-r1"}, nil, []string{"01-r1"}, "SYSTEM DROP REPLICA '<ad>' FROM ZKPATH '" + p + "'"},
		{"bayat + yabancı: yabancı metni", []string{"01-r1", "02-r1", "other-r9"}, nil, []string{"01-r1"}, "başka replikalarca tutuluyor"},
		{"canlı birleşik yok ama sahip var", []string{"01-r1"}, nil, nil, "başka replikalarca tutuluyor"},
		{"okuma hatası", nil, errors.New("ACCESS_DENIED"), nil, "birleşik ZK yolu okunamadı (ACCESS_DENIED)"},
	}
	for _, c := range cases {
		got := unifiedZKGate("x", p, c.owners, c.err, c.live, exp)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("[%s] = %q, beklenen …%q…", c.label, got, c.want)
		}
	}
}

func spMacros() map[string]map[string]string {
	return map[string]map[string]string{
		"host-1": {"shard": "01", "replica": "r1"}, "host-2": {"shard": "01", "replica": "r2"},
		"host-3": {"shard": "02", "replica": "r1"}, "host-4": {"shard": "02", "replica": "r2"},
	}
}

func TestStatePathReplicaCollisions(t *testing.T) {
	names, blocked := statePathReplicaNames(spHosts, spMacros())
	if len(blocked) != 0 || names["host-1"] != "01-r1" || names["host-4"] != "02-r2" {
		t.Fatalf("benzersiz makrolar: %v %v", names, blocked)
	}
	m := spMacros()
	m["host-3"] = map[string]string{"shard": "01", "replica": "r1"}
	_, blocked = statePathReplicaNames(spHosts, m)
	if len(blocked) != 1 || blocked[0] != `replika adı çakışması: {shard}-{replica} host-1, host-3 host'larında "01-r1"` {
		t.Errorf("çakışma = %v", blocked)
	}
	m = spMacros()
	delete(m["host-4"], "replica")
	_, blocked = statePathReplicaNames(spHosts, m)
	if len(blocked) != 1 || !strings.HasPrefix(blocked[0], "host-4: makro çözülemedi (") {
		t.Errorf("çözülemeyen makro = %v", blocked)
	}
}

// TestStatePathStillLegacyAfter — v0.10.971 (eski kilit testinin yerine):
// çalıştırmadan sonra eski yolda kalacak state tabloları. YALNIZ bilgi —
// kural 3 kalktı, kalan eski tablo yeni tabloları eski yola çekmez.
func TestStatePathStillLegacyAfter(t *testing.T) {
	const pre = "/clickhouse/tables"
	cur := map[string]string{"users": pre + "/state/users"}
	var ten []string
	for _, e := range statePathAllowlist {
		cur[e.Name] = pre + "/01/" + e.Name
		ten = append(ten, e.Name)
	}
	if still := statePathStillLegacyAfter(cur, ten, pre); still == nil || len(still) != 0 {
		t.Errorf("onu seçili: %v", still)
	}
	if still := statePathStillLegacyAfter(cur, ten[2:], pre); !slices.Equal(still, []string{"ai_eval_runs", "ingest_ledger"}) {
		t.Errorf("8/10: %v", still)
	}
	cur["alert_rules"] = pre + "/02/alert_rules"
	if still := statePathStillLegacyAfter(cur, ten, pre); !slices.Equal(still, []string{"alert_rules"}) {
		t.Errorf("izin dışı eski tablo: %v", still)
	}
	// Girdi haritası DEĞİŞMEZ (plan ve apply aynı gözlemi okur).
	if cur["ingest_ledger"] != pre+"/01/ingest_ledger" {
		t.Error("statePathStillLegacyAfter girdiyi değiştirdi")
	}
}

// TestStatePathNoLockConcept — v0.10.971 kaynak pini: sihirbaz ve denetim boot
// kuralını "kilit" için ÇAĞIRMAZ, kısmi seçim onay alanı (partialOK) yok.
// Kural 3 geri gelirse ya da kilit kopyası yeniden yazılırsa kızarır.
func TestStatePathNoLockConcept(t *testing.T) {
	for _, f := range []string{"state_path_check.go", "state_path_rebuild.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, bad := range []string{"useUnifiedStatePath(", "LockOpen", "LockReason", "PartialOK", `json:"partialOK"`, "statePathLockAfter"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s %q içeriyor — kural 3 kalktı, kilit kavramı yok", f, bad)
			}
		}
	}
}

func TestStatePathGateOrder(t *testing.T) {
	if got := statePathGate(statePathGateInput{}); len(got) != 1 || got[0] != "küme kipi değil" {
		t.Errorf("küme kipi = %v", got)
	}
	base := statePathGateInput{ClusterMode: true, Cluster: "uptrace_all", CfgCluster: "uptrace_all", RosterSize: 4,
		Hosts: spHosts, Queue: DDLQueueHealth{Verdict: "healthy"}, ZKPrefix: "/clickhouse/tables",
		DBEngines: map[string]string{"host-1": "Atomic", "host-2": "Atomic", "host-3": "Atomic", "host-4": "Atomic"}}
	if got := statePathGate(base); len(got) != 0 {
		t.Errorf("temiz girdi engelli: %v", got)
	}
	in := base
	in.Cluster = "other"
	if got := statePathGate(in); len(got) != 1 || !strings.Contains(got[0], `küme "other" uygulamanın kümesi değil ("uptrace_all")`) {
		t.Errorf("başka küme = %v", got)
	}
	in = base
	in.Hosts = spHosts[:3]
	if got := statePathGate(in); len(got) != 1 || !strings.Contains(got[0], "erişilemeyen host: küme tanımında 4 host, 3 cevap verdi") || strings.Contains(got[0], "okuma hatası") {
		t.Errorf("erişim = %v", got)
	}
	// v0.10.965 — R2 hatası mesajda: yetki/eşzamanlılık hatası ölü host gibi görünmesin.
	in = base
	in.Hosts, in.ReachErr = nil, errors.New("code: 497, ACCESS_DENIED")
	if got := statePathGate(in); len(got) != 1 || !strings.Contains(got[0], "küme tanımında 4 host, 0 cevap verdi (system.one okuma hatası: code: 497, ACCESS_DENIED)") {
		t.Errorf("erişim hatası = %v", got)
	}
	in = base
	in.Queue = DDLQueueHealth{Verdict: "worker_stuck", Detail: "kuyruk başı 20 dk"}
	in.DBEngines = map[string]string{"host-1": "Atomic", "host-2": "Replicated", "host-3": "Atomic", "host-4": "Atomic"}
	in.Tables = []StatePathRebuildTable{{Table: "ingest_ledger", State: "unknown", Detail: "host-2'te motor MergeTree (Replicated değil)"}}
	in.ReplicaBlocked = []string{"R"}
	in.ZKBlocked = []string{"Z"}
	in.RenderBlocked = []string{"D"}
	in.ZKPrefix, in.EightTouched = "/ch/tbl", true
	in.WritersChecked, in.WritersBlocked = true, "W"
	in.EvalChecked, in.EvalRunning = true, 2
	got := statePathGate(in)
	want := []string{
		"dağıtık DDL kuyruğu sağlıklı değil (worker_stuck): kuyruk başı 20 dk",
		"host-2: veritabanı motoru Replicated (Atomic değil)",
		"ingest_ledger: host-2'te motor MergeTree (Replicated değil)",
		"R", "Z", "D",
		"özel ZK öneki (/ch/tbl)",
		"W",
		"ai_eval_runs: 2 evalset koşusu şu an çalışıyor (son 15 dk) — bitmesini bekle",
	}
	if len(got) != len(want) {
		t.Fatalf("sıra = %v", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("#%d = %q, beklenen önek %q", i, got[i], want[i])
		}
	}
	// v0.10.965 — eval okuma hatası her durumda GERÇEK engel ("geçici" dalı yok:
	// tablo bir yerde eksikse sayım parça yoklamasından gelir).
	in = base
	in.EvalChecked, in.EvalErr = true, errors.New("UNKNOWN_TABLE")
	if got := statePathGate(in); len(got) != 1 || !strings.Contains(got[0], "koşu olmadığı doğrulanmadan düşürülmez") || strings.Contains(got[0], "geçici") {
		t.Errorf("eval okuma hatası = %v", got)
	}
	in.EvalByParts = true
	if got := statePathGate(in); len(got) != 1 || !strings.Contains(got[0], "koşu olmadığı doğrulanmadan düşürülmez") {
		t.Errorf("parça yoklaması hatası = %v", got)
	}
	in.EvalErr, in.EvalRunning = nil, 3
	if got := statePathGate(in); len(got) != 1 || !strings.Contains(got[0], "ai_eval_runs: son 15 dk içinde 3 taze parça") || !strings.Contains(got[0], "çalışıyor") {
		t.Errorf("parça yoklaması taze = %v", got)
	}
}

// TestStatePathEvalAckGate — v0.10.965 — ai_eval_runs'ın MANTIKSAL onay
// kapısı: fiziksel satır birleşmeyle küçülüp yeni bir koşuyu gizleyebilir;
// son yazım onayın ölçüm anında ya da sonrasındaysa ret.
func TestStatePathEvalAckGate(t *testing.T) {
	const at = int64(1_800_000_000_000)
	ack := &StatePathRebuildAck{MeasuredAt: at}
	cases := []struct {
		label   string
		checked bool
		last    int64
		ack     *StatePathRebuildAck
		want    string
	}{
		{"ölçümden önce yazım", true, at - 1, ack, ""},
		{"boş tablo (0)", true, 0, ack, ""},
		{"ölçüm anında yazım", true, at, ack, "plandan sonra bir evalset koşusu yazıldı"},
		{"ölçümden sonra yazım", true, at + 60_000, ack, "son yazım 2027-01-15 08:01:00 UTC"},
		{"R8 okunmadı", false, at + 60_000, ack, ""},
		{"onay yok", true, at + 60_000, nil, ""},
	}
	for _, c := range cases {
		got := statePathEvalAckGate(c.checked, c.last, c.ack)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("[%s] = %q, beklenen …%q…", c.label, got, c.want)
		}
	}
}

// TestStatePathRepairReject — v0.10.965 — izin listesindeki state tablosu
// birleşik yolda değilken Onar / İlk replika eski yolu çoğaltır: ret. İzin
// listesi dışı eski tablo ve birleşik (kind "") tablo geçer.
func TestStatePathRepairReject(t *testing.T) {
	cases := []struct {
		table, kind, want string
	}{
		{"ingest_ledger", "legacy", "ingest_ledger state tablosu eski (shard'lı) ZK yolunda — onarım eski yolu yeniden kurar"},
		{"ai_eval_runs", "mixed", "karışık ZK yolunda"},
		{"rollout_events", "absent", "birleşik yolda eksik"},
		{"ingest_ledger", "", ""},
		{"alert_rules", "legacy", ""},
		{"spans_local", "legacy", ""},
	}
	for _, c := range cases {
		got := statePathRepairReject(c.table, c.kind)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("(%s, %s) = %q, beklenen …%q…", c.table, c.kind, got, c.want)
		}
	}
	chk := &StatePathCheck{Legacy: []StatePathTable{{Table: "rollout_events", Kind: "absent"}}}
	if statePathListedKind(chk, "rollout_events") != "absent" || statePathListedKind(chk, "users") != "" || statePathListedKind(nil, "x") != "" {
		t.Error("statePathListedKind")
	}
	// Kaynak pini: PlanReplicaRepair kapıyı raporu okuduktan sonra, karar kapısından ÖNCE koşar.
	b, err := os.ReadFile("replica_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	gate := strings.Index(src, "statePathRepairReject(req.Table, firstNonEmpty(tbl.StatePath, statePathListedKind(rep.StatePaths, req.Table)))")
	verdict := strings.Index(src, "if shard.Verdict != ReplicaMissing && shard.Verdict != ReplicaNotReplicated {")
	if gate < 0 || verdict < 0 || gate > verdict {
		t.Errorf("PlanReplicaRepair state yolu kapısı yok ya da karar kapısından sonra (gate=%d verdict=%d)", gate, verdict)
	}
}

// TestStatePathObservedIgnoresNonStateTables — v0.10.965 — statePathObserved
// boot'un stateProbeTable süzgecini uygular (plan ve son kilit buna bağlı).
func TestStatePathObservedIgnoresNonStateTables(t *testing.T) {
	const pre = "/clickhouse/tables"
	reps := map[string]map[string]statePathReplica{}
	for _, n := range []string{"spans_local", ".inner_id.45f12d2e-36ac-4cb4-8bef-5025847af024", "problems_old", ""} {
		reps[n] = map[string]statePathReplica{"host-1": {Path: pre + "/01/" + n}, "host-3": {Path: pre + "/02/" + n}}
	}
	reps["ingest_ledger"] = map[string]statePathReplica{"host-1": {Path: pre + "/state/ingest_ledger"}}
	got := statePathObserved(reps, pre)
	if len(got) != 1 || got["ingest_ledger"] != pre+"/state/ingest_ledger" {
		t.Fatalf("statePathObserved = %v", got)
	}
}

// TestStatePathExecBudget — v0.10.965 — bütçe dolmuşsa ifade GÖNDERİLMEZ ve
// koşu durur (yerel ctx süresi sunucu kuyruğu sayılmaz).
func TestStatePathExecBudget(t *testing.T) {
	c := newSprConn()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, stop := statePathExec(ctx, c, "DROP TABLE IF EXISTS ingest_ledger ON CLUSTER `uptrace_all` SYNC")
	if !stop || r.OK || !strings.Contains(r.Err, "gönderilmedi") || len(c.execs) != 0 {
		t.Errorf("ölü ctx: stop=%v r=%+v execs=%v", stop, r, c.execs)
	}
	// Canlı ctx + sunucu tarafı 159: bekliyor, koşu SÜRER.
	c.createErr = errors.New("code: 159, message: Watching task /clickhouse/task_queue/ddl/query-0000000123 is executing longer than distributed_ddl_task_timeout (=180) seconds. There are 2 unfinished hosts (0 of them are currently executing the task), they are going to execute the query in background")
	r, stop = statePathExec(context.Background(), c, "CREATE TABLE IF NOT EXISTS x ON CLUSTER c (a UInt8) ENGINE = ReplicatedMergeTree('/clickhouse/tables/state/x', '{shard}-{replica}') ORDER BY a")
	if stop || r.Err != statePathPendingErr {
		t.Errorf("159: stop=%v r=%+v", stop, r)
	}
}

func TestStatePathDropDDL(t *testing.T) {
	s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}}
	got := statePathDropDDL("ingest_ledger", s.onCluster())
	want := "DROP TABLE IF EXISTS ingest_ledger ON CLUSTER `uptrace_all` SYNC SETTINGS max_table_size_to_drop = 0, max_partition_size_to_drop = 0"
	if got != want {
		t.Errorf("DROP =\n%s\nbeklenen\n%s", got, want)
	}
	for _, e := range statePathAllowlist {
		if bt := bareDestructiveTarget(statePathDropDDL(e.Name, s.onCluster())); bt != "" {
			t.Errorf("%s: çıplak yüksek hacimli düşürme hedefi %q", e.Name, bt)
		}
	}
}

func TestStatePathCreateDDL(t *testing.T) {
	cat := canonicalTables(30, 30, 7)
	// Pod'un BAYAT boot gözlemi: onu da eski yolda. Çıktı yine birleşik olmalı.
	stale := stateObservation{ok: true, paths: map[string]string{}}
	for _, e := range statePathAllowlist {
		stale.paths[e.Name] = "/clickhouse/tables/01/" + e.Name
	}
	live := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, stateObs: stale, canonicalTableDDL: cat}
	eight, err := rolloutV2LayerStatements("uptrace_all")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range statePathAllowlist {
		stmt, err := live.statePathCreateDDL(e.Name, "uptrace_all")
		if err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		if !strings.Contains(stmt, "'/clickhouse/tables/state/"+e.Name+"'") || !strings.Contains(stmt, "'{shard}-{replica}'") || !strings.Contains(stmt, " ON CLUSTER ") {
			t.Errorf("%s: birleşik yol / replika / ON CLUSTER yok:\n%s", e.Name, stmt)
		}
		if strings.Contains(stmt, "{shard}/") || strings.Contains(stmt, "'{replica}'") {
			t.Errorf("%s: pod'un bayat gözlemi sızdı", e.Name)
		}
		// Adaptörü bayat gözleme bakan canlı Store, eski yolu üretir (kanıt: gözlem gerçekten bayat).
		if e.Class != statePathClassEmptyOnly {
			if frags := live.adaptDDL(tableDDLByName(cat, e.Name)); !strings.Contains(frags[0], "'/clickhouse/tables/{shard}/"+e.Name+"', '{replica}'") {
				t.Errorf("%s: fikstür bayat değil: %s", e.Name, frags[0])
			}
		}
	}
	// Sekiz: 0015 ifadesi == kanonik katalogun gölge render'ı (tek gerçek).
	shadow := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, stateObs: stateObservation{ok: true, paths: map[string]string{}}}
	for _, st := range eight {
		n, _ := ddlCreatesObject(st)
		got, err := live.statePathCreateDDL(n, "uptrace_all")
		if err != nil || got != st {
			t.Errorf("%s: sekizin CREATE'i 0015 ifadesiyle bayt bayt aynı değil (err %v)", n, err)
		}
		frags := shadow.adaptDDL(tableDDLByName(cat, n))
		if len(frags) != 1 || normalizeRolloutV2DDL(frags[0]) != normalizeRolloutV2DDL(st) {
			t.Errorf("%s: 0015 ile katalog render'ı ayrışıyor", n)
		}
	}
	// Özel önek: iki katalog tablosu önekle, sekiz ENGELLİ.
	custom := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all", ReplicaPath: "/ch/tbl"}, stateObs: stale, canonicalTableDDL: cat}
	for _, e := range statePathAllowlist {
		stmt, err := custom.statePathCreateDDL(e.Name, "uptrace_all")
		if e.Class == statePathClassEmptyOnly {
			if err == nil || !strings.Contains(err.Error(), "birleşik yola render edilemedi") {
				t.Errorf("%s: özel önekte sekiz engellenmeli (%v)", e.Name, err)
			}
			continue
		}
		if err != nil || !strings.Contains(stmt, "'/ch/tbl/state/"+e.Name+"'") {
			t.Errorf("%s: özel önek = %v\n%s", e.Name, err, stmt)
		}
	}
	// Katalog yüklenmemiş → engel.
	empty := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}}
	if _, err := empty.statePathCreateDDL("ingest_ledger", "uptrace_all"); err == nil || !strings.Contains(err.Error(), "kanonik katalog yüklenmemiş") {
		t.Errorf("boş katalog = %v", err)
	}
	if _, err := empty.statePathCreateDDL("problems", "uptrace_all"); err == nil {
		t.Error("izin dışı ad render edildi")
	}
	// Render kontrolü: yanlış replika adı / yanlış ad / ON CLUSTER yok.
	for label, stmt := range map[string]string{
		"replika": "CREATE TABLE IF NOT EXISTS x ON CLUSTER c (a UInt8) ENGINE = ReplicatedMergeTree('/clickhouse/tables/state/x', '{replica}') ORDER BY a",
		"ad":      "CREATE TABLE IF NOT EXISTS y ON CLUSTER c (a UInt8) ENGINE = ReplicatedMergeTree('/clickhouse/tables/state/x', '{shard}-{replica}') ORDER BY a",
		"küme":    "CREATE TABLE IF NOT EXISTS x (a UInt8) ENGINE = ReplicatedMergeTree('/clickhouse/tables/state/x', '{shard}-{replica}') ORDER BY a",
		"yol":     "CREATE TABLE IF NOT EXISTS x ON CLUSTER c (a UInt8) ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/x', '{shard}-{replica}') ORDER BY a",
		"motor":   "CREATE TABLE IF NOT EXISTS x ON CLUSTER c (a UInt8) ENGINE = MergeTree ORDER BY a",
	} {
		if err := statePathRenderCheck(stmt, "x", "/clickhouse/tables"); err == nil {
			t.Errorf("[%s] render kontrolü geçti", label)
		}
	}
}

func TestStatePathResumeHint(t *testing.T) {
	tables := []StatePathRebuildTable{
		{Table: "ingest_ledger", Action: "rebuild", After: "legacy", pending: []string{"host-3"}, created: true},
		{Table: "ai_eval_runs", Action: "rebuild", After: "unified", Verified: true, created: true},
		{Table: "rollout_events", Action: "skip"},
	}
	for phase, want := range map[string]string{
		"drop":      "DROP yarıda kaldı. Yeniden ölç → planla → çalıştır",
		"drop_wait": "DROP şu host'larda henüz uygulanmadı: ingest_ledger (host-3). CREATE koşmadı.",
		"create":    "Kurma yarıda kaldı: 2 tablo kuruldu.",
		"verify":    "ingest_ledger doğrulanamadı (şimdi: eski yol). Eski yola yeniden kurulduysa",
	} {
		if got := statePathResumeHint(phase, tables); !strings.Contains(got, want) {
			t.Errorf("[%s] = %q, beklenen …%q…", phase, got, want)
		}
	}
	if got := statePathResumeHint("done", tables); got != "" {
		t.Errorf("done = %q", got)
	}
}

// TestStatePathQueriesBoundedStrict — kaynak pini: her clusterAllReplicas
// okuması zaman tavanlı, LIMIT'li ve skip_unavailable_shards TAŞIMAZ;
// veritabanı bağlı parametre; pod'un gözlemiyle DDL üreten yollar çağrılmaz.
func TestStatePathQueriesBoundedStrict(t *testing.T) {
	b, err := os.ReadFile("state_path_rebuild.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	segs := strings.Split(src, "clusterAllReplicas(")
	if len(segs)-1 < 9 { // one, replicas, tables, parts, databases, macros, clusters(is_local), ai_eval_runs, parts(eval yedeği)
		t.Fatalf("clusterAllReplicas okuması %d (en az 9)", len(segs)-1)
	}
	for i, seg := range segs[1:] {
		end := strings.Index(seg, "\n}\n")
		if end < 0 {
			end = len(seg)
		}
		body := seg[:end]
		if !strings.Contains(body, "max_execution_time") || !strings.Contains(body, "LIMIT") {
			t.Errorf("clusterAllReplicas okuması #%d tavansız/LIMIT'siz: %q", i+1, body)
		}
	}
	for _, bad := range []string{"skip_unavailable_shards =", "skip_unavailable_shards=", "currentDatabase()", "s.execDDL(", "s.adaptDDL(", "s.execWithReadonlyRetry(", "clusterMacrosByHost("} {
		if strings.Contains(src, bad) {
			t.Errorf("state_path_rebuild.go %q içeriyor", bad)
		}
	}
	for _, want := range []string{
		"clusterAllReplicas('%s', system.replicas) WHERE database = ?",
		"clusterAllReplicas('%s', system.tables) \"+\n\t\t\"WHERE database = ? AND name IN (%s)",
		"clusterAllReplicas('%s', system.parts) \"+\n\t\t\"WHERE database = ? AND active AND table IN (%s)",
		"clusterAllReplicas('%s', system.databases) WHERE name = ?",
		"clusterAllReplicas('%s', `%s`.`ai_eval_runs`)",
		"shadow.adaptDDL(ddl)",
		"s.conn.Exec", // düz Exec (dropRemovedTable emsali) — statePathExec(ctx, s.conn, …)
	} {
		if want == "s.conn.Exec" {
			if !strings.Contains(src, "statePathExec(ctx, s.conn, stmt)") || !strings.Contains(src, "conn.Exec(ctx, stmt)") {
				t.Error("DDL düz conn.Exec ile koşmalı")
			}
			continue
		}
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	// Okuma SQL'leri — hepsi LIMIT + tavan (R2 dahil).
	for _, q := range []string{statePathReachSQL("c"), statePathReplicasSQL("c"), statePathTablesSQL("c"), statePathPartsSQL("c"),
		statePathDatabasesSQL("c"), statePathMacrosSQL("c"), statePathIsLocalSQL("c"), statePathEvalSQL("c", "coremetry"), statePathEvalPartsSQL("c")} {
		if !strings.Contains(q, "LIMIT ") || !strings.Contains(q, "SETTINGS max_execution_time = ") || strings.Contains(q, "skip_unavailable") {
			t.Errorf("sınırsız okuma: %s", q)
		}
	}
	if !strings.Contains(statePathEvalSQL("c", "coremetry"), "INTERVAL 15 MINUTE") {
		t.Error("R8 penceresi StatePathEvalFresh'ten türemeli (15 dk)")
	}
	// v0.10.965 — R8 yalnız ÇALIŞAN koşuları sayar; ikinci kolon en son yazım (ms).
	if q := statePathEvalSQL("c", "coremetry"); !strings.Contains(q, "countIf(st = 'running' AND upd >= now64(9) - INTERVAL 15 MINUTE)") || !strings.Contains(q, "toUnixTimestamp64Milli(max(upd))") {
		t.Errorf("R8 çalışan süzgeci / son yazım yok: %s", q)
	}
	// v0.10.965 — eval yedeği: tablonun kendisini DEĞİL system.parts'ı okur, veritabanı bağlı.
	if q := statePathEvalPartsSQL("c"); !strings.Contains(q, "clusterAllReplicas('c', system.parts) WHERE database = ? AND table = 'ai_eval_runs' AND active") ||
		!strings.Contains(q, "modification_time >= now() - INTERVAL 15 MINUTE") || strings.Contains(q, "ai_eval_runs`") || strings.Contains(q, "level") {
		t.Errorf("eval parça yoklaması: %s", q)
	}
}

// ───────────────────────── sprConn — sahte küme ─────────────────────────

type sprTable struct {
	engine   string
	path     string
	replica  string
	rows     uint64
	readonly bool
}

type sprConn struct {
	driver.Conn
	hosts       []string
	macros      map[string]map[string]string
	state       map[string]map[string]*sprTable // host → tablo → hâl
	zkForeign   map[string][]string             // znode yolu → yabancı replikalar
	unreachable bool
	dbEngine    map[string]string
	settings    map[string]string
	evalRunning uint64
	evalErr     error
	// v0.10.965 — R8'in ikinci kolonu (en son yazım, unix ms) ve parça
	// yoklaması (tablo bir host'ta yoksa): taze parça sayısı + en yeni parça.
	evalLastWriteMs int64
	evalFreshParts  uint64
	evalPartsLastMs int64
	dropLag         map[string]bool // bu host'larda DROP uygulanmaz
	execErrAt       int             // 1 tabanlı; bu Exec hata döner (uygulanmadan)
	execErr         error
	createErr       error // CREATE uygulanır, sonra bu hata döner (kod 159)
	// deferAfter > 0: CREATE hemen UYGULANMAZ (dağıtık DDL kuyrukta); ilk
	// ertelemeden sonraki deferAfter'ıncı system.replicas okumasında uygulanır.
	deferAfter int
	deferred   []string
	deferReads int
	beforeExec func(c *sprConn, q string)
	// v0.10.971 — dolu ise system.replicas okuması bu hatayı döner (son
	// ölçümün okunamadığı dal).
	replicasErr error
	queries     []string
	execs       []string
}

func newSprConn() *sprConn {
	c := &sprConn{hosts: spHosts, macros: spMacros(), state: map[string]map[string]*sprTable{},
		zkForeign: map[string][]string{}, dbEngine: map[string]string{}, settings: map[string]string{}, dropLag: map[string]bool{}}
	for _, h := range spHosts {
		c.state[h] = map[string]*sprTable{}
		c.dbEngine[h] = "Atomic"
	}
	return c
}

// legacy — tablo dört host'ta shard'lı yolda ({replica} adıyla); rows host başına.
func (c *sprConn) legacy(table string, rows uint64) {
	for _, h := range c.hosts {
		c.state[h][table] = &sprTable{engine: "ReplicatedReplacingMergeTree", path: "/clickhouse/tables/" + spShard(h) + "/" + table, replica: c.macros[h]["replica"], rows: rows}
	}
}

// unified — tablo verilen host'larda birleşik yolda.
func (c *sprConn) unified(table string, hosts ...string) {
	for _, h := range hosts {
		c.state[h][table] = &sprTable{engine: "ReplicatedReplacingMergeTree", path: "/clickhouse/tables/state/" + table, replica: c.macros[h]["shard"] + "-" + c.macros[h]["replica"]}
	}
}

func (c *sprConn) allTen() {
	for _, e := range statePathAllowlist {
		c.legacy(e.Name, 0)
	}
}

type sprRows struct {
	driver.Rows
	vals [][]any
	i    int
}

func (r *sprRows) Next() bool { r.i++; return r.i <= len(r.vals) }
func (r *sprRows) Scan(dest ...any) error {
	return sprScan(r.vals[r.i-1], dest)
}
func (r *sprRows) Close() error { return nil }
func (r *sprRows) Err() error   { return nil }

func sprScan(row []any, dest []any) error {
	if len(dest) != len(row) {
		return fmt.Errorf("Scan %d hedef, satır %d kolon", len(dest), len(row))
	}
	for k, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = row[k].(string)
		case *uint64:
			*p = row[k].(uint64)
		case *int64:
			*p = row[k].(int64)
		case *uint32:
			*p = row[k].(uint32)
		case *uint16:
			*p = row[k].(uint16)
		case *uint8:
			*p = row[k].(uint8)
		default:
			return fmt.Errorf("Scan hedefi %d: %T", k, d)
		}
	}
	return nil
}

type sprRow struct {
	vals []any
	err  error
}

func (r sprRow) Err() error { return r.err }
func (r sprRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return sprScan(r.vals, dest)
}
func (r sprRow) ScanStruct(any) error { return r.err }

func (c *sprConn) sortedHosts() []string {
	hs := append([]string(nil), c.hosts...)
	sort.Strings(hs)
	return hs
}

func (c *sprConn) tableNames(h string) []string {
	var out []string
	for t := range c.state[h] {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// peers — aynı tablo + aynı yoldaki host sayısı (total = active).
func (c *sprConn) peers(table, path string) uint32 {
	n := uint32(0)
	for _, h := range c.hosts {
		if t := c.state[h][table]; t != nil && t.path == path {
			n++
		}
	}
	return n
}

// v0.10.965 — sahte, sürücünün acquire'ı gibi ölü ctx'te HİÇBİR ŞEY
// göndermeden context.Cause(ctx) döner (kayıt da düşmez).
func (c *sprConn) Query(ctx context.Context, q string, args ...any) (driver.Rows, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	c.queries = append(c.queries, q)
	var vals [][]any
	switch {
	case strings.Contains(q, "FROM system.clusters WHERE cluster = ?") && !strings.Contains(q, "clusterAllReplicas"):
		for i, h := range c.sortedHosts() {
			vals = append(vals, []any{uint32(i/2 + 1), uint32(i%2 + 1), h, "10.0.0." + fmt.Sprint(i+1), uint16(9000), uint8(0)})
		}
	case strings.Contains(q, "system.one)"):
		if c.unreachable {
			return nil, errors.New("code: 279, message: All connection tries failed")
		}
		for _, h := range c.sortedHosts() {
			vals = append(vals, []any{h})
		}
	case strings.Contains(q, "system.replicas)"):
		if c.replicasErr != nil {
			return nil, c.replicasErr
		}
		if len(c.deferred) > 0 {
			if c.deferReads++; c.deferReads >= c.deferAfter {
				for _, d := range c.deferred {
					if err := c.applyCreate(d); err != nil {
						return nil, err
					}
				}
				c.deferred = nil
			}
		}
		for _, h := range c.sortedHosts() {
			for _, t := range c.tableNames(h) {
				st := c.state[h][t]
				if st.path == "" {
					continue
				}
				n := c.peers(t, st.path)
				ro := uint8(0)
				if st.readonly {
					ro = 1
				}
				vals = append(vals, []any{h, t, st.path, st.replica, n, n, ro})
			}
		}
	case strings.Contains(q, "system.tables)"):
		for _, h := range c.sortedHosts() {
			for _, t := range c.tableNames(h) {
				if StatePathRebuildAllowed(t) {
					vals = append(vals, []any{h, t, c.state[h][t].engine})
				}
			}
		}
	case strings.Contains(q, "system.parts)"):
		for _, h := range c.sortedHosts() {
			for _, t := range c.tableNames(h) {
				if StatePathRebuildAllowed(t) && c.state[h][t].rows > 0 {
					vals = append(vals, []any{h, t, c.state[h][t].rows})
				}
			}
		}
	case strings.Contains(q, "system.databases)"):
		for _, h := range c.sortedHosts() {
			vals = append(vals, []any{h, c.dbEngine[h]})
		}
	case strings.Contains(q, "system.macros)"):
		for _, h := range c.sortedHosts() {
			for _, k := range []string{"replica", "shard"} {
				if v, ok := c.macros[h][k]; ok {
					vals = append(vals, []any{h, k, v})
				}
			}
		}
	case strings.Contains(q, "system.clusters)"):
		for _, h := range c.sortedHosts() {
			vals = append(vals, []any{h, uint32(1)})
		}
	case strings.Contains(q, "FROM system.zookeeper"):
		path := args[0].(string)
		table := strings.TrimSuffix(strings.TrimPrefix(path, "/clickhouse/tables/state/"), "/replicas")
		for _, h := range c.sortedHosts() {
			if st := c.state[h][table]; st != nil && st.path == "/clickhouse/tables/state/"+table {
				vals = append(vals, []any{st.replica})
			}
		}
		for _, f := range c.zkForeign[path] {
			vals = append(vals, []any{f})
		}
		if len(vals) == 0 {
			return nil, errors.New("code: 999, message: Coordination::Exception: No node, path: " + path)
		}
	default:
		return nil, errors.New("beklenmeyen Query: " + q)
	}
	return &sprRows{vals: vals}, nil
}

func (c *sprConn) QueryRow(ctx context.Context, q string, args ...any) driver.Row {
	if ctx.Err() != nil {
		return sprRow{err: context.Cause(ctx)}
	}
	c.queries = append(c.queries, q)
	switch {
	case q == "SELECT currentDatabase()":
		return sprRow{vals: []any{"coremetry"}}
	case strings.Contains(q, "system_settings FINAL"):
		if v, ok := c.settings[args[0].(string)]; ok {
			return sprRow{vals: []any{v}}
		}
		return sprRow{err: errors.New("sql: no rows in result set")}
	case strings.Contains(q, "system.parts") && strings.Contains(q, "modification_time"):
		return sprRow{vals: []any{c.evalFreshParts, c.evalPartsLastMs}}
	case strings.Contains(q, "`coremetry`.`ai_eval_runs`"):
		// v0.10.965 — gerçek ClickHouse gibi: tablo bir host'ta yoksa
		// clusterAllReplicas okuması (skip'siz) UNKNOWN_TABLE ile düşer.
		for _, h := range c.hosts {
			if c.state[h]["ai_eval_runs"] == nil {
				return sprRow{err: errors.New("code: 60, message: Table coremetry.ai_eval_runs does not exist. (UNKNOWN_TABLE)")}
			}
		}
		if c.evalErr != nil {
			return sprRow{err: c.evalErr}
		}
		return sprRow{vals: []any{c.evalRunning, c.evalLastWriteMs}}
	}
	return sprRow{err: errors.New("beklenmeyen QueryRow: " + q)}
}

var sprDropRe = regexp.MustCompile(`^DROP TABLE IF EXISTS ([A-Za-z0-9_]+) ON CLUSTER `)

func (c *sprConn) Exec(ctx context.Context, q string, _ ...any) error {
	if ctx.Err() != nil { // v0.10.965 — ölü ctx: gönderilmez, kaydedilmez
		return context.Cause(ctx)
	}
	c.execs = append(c.execs, q)
	if c.beforeExec != nil {
		c.beforeExec(c, q)
	}
	if ctx.Err() != nil { // bütçe ifade sırasında doldu (beforeExec iptal etti)
		return context.Cause(ctx)
	}
	if c.execErrAt == len(c.execs) {
		return c.execErr
	}
	if m := sprDropRe.FindStringSubmatch(q); m != nil {
		for _, h := range c.hosts {
			if !c.dropLag[h] {
				delete(c.state[h], m[1])
			}
		}
		return nil
	}
	if _, ok := ddlCreatesObject(q); ok {
		if c.deferAfter > 0 {
			c.deferred = append(c.deferred, q)
			return c.createErr
		}
		if err := c.applyCreate(q); err != nil {
			return err
		}
		return c.createErr
	}
	return errors.New("beklenmeyen Exec: " + q)
}

// applyCreate — CREATE IF NOT EXISTS: yol ifadeden, replika adı makrolardan.
func (c *sprConn) applyCreate(q string) error {
	name, _ := ddlCreatesObject(q)
	fam, path, rep, ok := replicatedEngineArgs(q)
	if !ok {
		return errors.New("sahte: Replicated olmayan CREATE")
	}
	for _, h := range c.hosts {
		if c.state[h][name] != nil {
			continue // IF NOT EXISTS
		}
		rn, err := expandMacros(rep, c.macros[h])
		if err != nil {
			return err
		}
		c.state[h][name] = &sprTable{engine: fam, path: path, replica: rn}
	}
	return nil
}

func (c *sprConn) count(kind string) int {
	n := 0
	for _, e := range c.execs {
		if strings.HasPrefix(e, kind) {
			n++
		}
	}
	return n
}

func (c *sprConn) queried(sub string) bool {
	for _, q := range c.queries {
		if strings.Contains(q, sub) {
			return true
		}
	}
	return false
}

// sprSeams — test dikişleri: DDL kuyruğu, yoklama (4 deneme, ns aralık), meşgul bayrağı.
func sprSeams(t *testing.T, queue DDLQueueHealth) {
	t.Helper()
	oldQ, oldP := statePathQueueHealth, statePathPoll
	statePathQueueHealth = func(context.Context, *Store) (DDLQueueHealth, error) { return queue, nil }
	statePathPoll.Every, statePathPoll.Drop, statePathPoll.Verify = time.Nanosecond, 3*time.Nanosecond, 3*time.Nanosecond
	statePathRebuildBusy.Store(false)
	t.Cleanup(func() { statePathQueueHealth, statePathPoll = oldQ, oldP; statePathRebuildBusy.Store(false) })
}

var sprHealthy = DDLQueueHealth{Verdict: "healthy"}

func sprStore(c *sprConn) *Store {
	return &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: c, canonicalTableDDL: canonicalTables(30, 30, 7)}
}

// sprAck — planın onayı (FE ackFromPlan'ın aynası: ATLA olmayan her tablo).
func sprAck(p *StatePathRebuildPlan) *StatePathRebuildAck {
	a := &StatePathRebuildAck{MeasuredAt: p.MeasuredAt, Tables: []StatePathAckTable{}}
	for _, t := range p.Tables {
		if t.Action != "skip" {
			a.Tables = append(a.Tables, StatePathAckTable{Table: t.Table, State: t.State, Rows: t.Rows})
		}
	}
	return a
}

func sprTen() []string {
	var out []string
	for _, e := range statePathAllowlist {
		out = append(out, e.Name)
	}
	return out
}

// sprPlanAck — aynı sahte kümede planla, onayı üret; plan hiçbir şey koşmamalı.
func sprPlanAck(t *testing.T, s *Store, c *sprConn, tables []string) (*StatePathRebuildPlan, *StatePathRebuildAck) {
	t.Helper()
	p, err := s.PlanStatePathRebuild(context.Background(), tables)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(c.execs) != 0 {
		t.Fatalf("plan DDL koştu: %v", c.execs)
	}
	return p, sprAck(p)
}

func sprRefused(t *testing.T, label string, res *StatePathRebuildResult, err error, c *sprConn, want string) {
	t.Helper()
	var ge *StatePathGateError
	if !errors.As(err, &ge) {
		t.Fatalf("[%s] kapı reddi bekleniyordu, (%+v, %v)", label, res, err)
	}
	if !strings.Contains(ge.Error(), want) {
		t.Errorf("[%s] ret = %q, beklenen …%q…", label, ge.Error(), want)
	}
	if len(c.execs) != 0 {
		t.Errorf("[%s] ret sonrası %d ifade koştu: %v", label, len(c.execs), c.execs)
	}
}

// ───────────────────────── kablolama hücreleri ─────────────────────────

func TestStatePathRebuildHappy(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	c.legacy("ingest_ledger", 1234)
	c.legacy("ai_eval_runs", 5)
	c.unified("users", spHosts...)
	// v0.10.965 — boot süzgeci: state dışı replikalı tablolar (shard'lı yolda,
	// prod'un gerçek şekli) "eski yolda kalanlar"a girmez ve hiç ifade almaz.
	for _, n := range []string{"spans_local", ".inner_id.45f12d2e-36ac-4cb4-8bef-5025847af024", "problems_old"} {
		c.legacy(n, 0)
	}
	s := sprStore(c)
	p, ack := sprPlanAck(t, s, c, sprTen())
	if len(p.Blocked) != 0 {
		t.Fatalf("plan engelli: %v", p.Blocked)
	}
	// v0.10.965 — rollout uyarısı arayüzün kendi satırında: plan uyarısı olarak YİNELENMEZ.
	if len(p.Warnings) != 0 {
		t.Errorf("plan uyarıları boş olmalı (is_local temiz): %v", p.Warnings)
	}
	if len(p.Drops) != 10 || len(p.Creates) != 10 || p.StillLegacyAfter == nil || len(p.StillLegacyAfter) != 0 {
		t.Fatalf("plan: drops %d creates %d kalan %v", len(p.Drops), len(p.Creates), p.StillLegacyAfter)
	}
	// v0.10.971 — plan JSON'unda kilit alanı yok.
	if pj, _ := json.Marshal(p); strings.Contains(string(pj), `"lockOpensAfter"`) {
		t.Errorf("plan JSON lockOpensAfter taşıyor: %s", pj)
	}
	if p.Hosts != 4 || p.Database != "coremetry" || p.Cluster != "uptrace_all" {
		t.Errorf("plan başlığı: %+v", p)
	}
	for _, want := range []string{"4/4 host erişilebilir", "DDL kuyruğu sağlıklı", "replika adları benzersiz (01-r1, 01-r2, 02-r1, 02-r2)", "birleşik znode", "çalışan evalset koşusu yok", "Rollouts v2 yazıcıları kapalı"} {
		if !slices.ContainsFunc(p.Checks, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("kontrol %q yok: %v", want, p.Checks)
		}
	}
	// Plan satırları: ingest_ledger 2 grup, satır = grup max toplamı.
	if p.Tables[0].Table != "ingest_ledger" || p.Tables[0].Rows != 2468 || len(p.Tables[0].Groups) != 2 || p.Tables[1].Rows != 10 {
		t.Errorf("plan satırları: %+v / %+v", p.Tables[0], p.Tables[1])
	}
	c.queries = nil
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(c.execs) != 20 || c.count("DROP") != 10 || c.count("CREATE") != 10 {
		t.Fatalf("ifadeler: %d (DROP %d, CREATE %d)", len(c.execs), c.count("DROP"), c.count("CREATE"))
	}
	lastDrop, firstCreate := -1, len(c.execs)
	for i, e := range c.execs {
		if strings.HasPrefix(e, "DROP") {
			lastDrop = i
		} else if i < firstCreate {
			firstCreate = i
		}
	}
	if lastDrop >= firstCreate {
		t.Errorf("DROP'lar CREATE'lerden önce bitmeli (son DROP %d, ilk CREATE %d)", lastDrop, firstCreate)
	}
	eight, _ := rolloutV2LayerStatements("uptrace_all")
	for _, st := range eight {
		if !slices.Contains(c.execs, st) {
			n, _ := ddlCreatesObject(st)
			t.Errorf("%s: CREATE 0015 ifadesiyle bayt bayt aynı değil", n)
		}
	}
	if !res.OK || res.Phase != "done" || res.StillLegacy == nil || len(res.StillLegacy) != 0 || res.Resume != "" {
		t.Errorf("sonuç: ok=%v phase=%s kalan=%v resume=%q", res.OK, res.Phase, res.StillLegacy, res.Resume)
	}
	// v0.10.971 — not kilit anlatmaz; boot log doğrulaması "(N birleşik, 0 eski)" biçimiyle.
	if !strings.Contains(res.Note, "(N birleşik, 0 eski)") || strings.Contains(strings.ToLower(res.Note), "kilit") ||
		strings.Contains(res.Note, "yola kurulacak") {
		t.Errorf("not: %q", res.Note)
	}
	if rj, _ := json.Marshal(res); strings.Contains(string(rj), `"lockOpen"`) || strings.Contains(string(rj), `"lockReason"`) {
		t.Errorf("sonuç JSON kilit alanı taşıyor: %s", rj)
	}
	for _, tb := range res.Tables {
		if !tb.Verified || tb.After != "unified" {
			t.Errorf("%s: verified=%v after=%s", tb.Table, tb.Verified, tb.After)
		}
	}
	if len(res.Statements) != 20 {
		t.Errorf("sonuç ifadeleri %d", len(res.Statements))
	}
	for _, h := range spHosts {
		if st := c.state[h]["ingest_ledger"]; st == nil || st.path != "/clickhouse/tables/state/ingest_ledger" || st.replica != spShard(h)+"-"+c.macros[h]["replica"] {
			t.Errorf("%s: ingest_ledger = %+v", h, st)
		}
	}
	if c.queried("skip_unavailable_shards") {
		t.Error("katı ölçüm skip_unavailable_shards taşıdı")
	}
}

func TestStatePathRebuildRefusals(t *testing.T) {
	type cell struct {
		label  string
		setup  func(c *sprConn)
		mutate func(c *sprConn, ack *StatePathRebuildAck) // plan ile apply arasında
		queue  DDLQueueHealth
		want   string
	}
	cells := []cell{
		{label: "erişilemeyen host", mutate: func(c *sprConn, _ *StatePathRebuildAck) { c.unreachable = true },
			want: "erişilemeyen host: küme tanımında 4 host, 0 cevap verdi (system.one okuma hatası: code: 279, message: All connection tries failed)"},
		{label: "artık eski değil", mutate: func(c *sprConn, _ *StatePathRebuildAck) { c.unified("ingest_ledger", spHosts...) },
			want: "ingest_ledger: durum değişti (onay: eski yol, şimdi: birleşik)"},
		{label: "eval satırı arttı (5 → 7)", setup: func(c *sprConn) { c.state["host-1"]["ai_eval_runs"].rows = 5 },
			mutate: func(c *sprConn, _ *StatePathRebuildAck) { c.state["host-1"]["ai_eval_runs"].rows = 7 },
			want:   "ai_eval_runs: onaylanan 5, şimdi 7 satır"},
		{label: "defter +50000 (onay 100000)", setup: func(c *sprConn) { c.legacy("ingest_ledger", 50000) },
			mutate: func(c *sprConn, _ *StatePathRebuildAck) { c.state["host-1"]["ingest_ledger"].rows = 100000 },
			want:   "ingest_ledger: onaylanan 100.000, şimdi 150.000 satır"},
		{label: "satır var onay yok", setup: func(c *sprConn) { c.legacy("ingest_ledger", 3) },
			mutate: func(_ *sprConn, a *StatePathRebuildAck) { a.Tables = nil },
			want:   "ingest_ledger: 6 satır var ama onay yok"},
		{label: "bayat onay", mutate: func(_ *sprConn, a *StatePathRebuildAck) { a.MeasuredAt -= int64(31 * time.Minute / time.Millisecond) },
			want: "onay bayat"},
		{label: "v2 yazıcısı (source v2)", setup: func(c *sprConn) { c.settings["rollouts"] = `{"enabled":true,"source":"v2"}` },
			want: `rollouts.source="v2"`},
		{label: "argocd açık", setup: func(c *sprConn) { c.settings["argocd"] = `{"enabled":true}` },
			want: "argocd.enabled=true"},
		{label: "evalset koşuyor", setup: func(c *sprConn) { c.evalRunning = 1 },
			want: "ai_eval_runs: 1 evalset koşusu şu an çalışıyor"},
		{label: "DDL kuyruğu sağlıksız", queue: DDLQueueHealth{Verdict: "worker_stuck", Detail: "baş 20 dk"},
			want: "dağıtık DDL kuyruğu sağlıklı değil (worker_stuck): baş 20 dk"},
		{label: "yabancı znode sahibi", setup: func(c *sprConn) {
			c.zkForeign["/clickhouse/tables/state/ingest_ledger/replicas"] = []string{"otherinstall-r1"}
		},
			want: "ingest_ledger: birleşik ZK yolu /clickhouse/tables/state/ingest_ledger başka replikalarca tutuluyor (otherinstall-r1)"},
		// v0.10.965 — R11 kapısı sekiz v2 tablosunda da koşar.
		{label: "v2 yabancı znode sahibi", setup: func(c *sprConn) {
			c.zkForeign["/clickhouse/tables/state/argocd_app_status/replicas"] = []string{"otherinstall-r1"}
		},
			want: "argocd_app_status: birleşik ZK yolu /clickhouse/tables/state/argocd_app_status başka replikalarca tutuluyor (otherinstall-r1)"},
		// v0.10.965 — birleşik (host-3'te yok) + host-3'ün BAYAT birleşik kaydı: CREATE 253 verirdi, DROP'lardan ÖNCE ret.
		{label: "bayat znode sahibi (unified_partial)", setup: func(c *sprConn) {
			delete(c.state["host-3"], "rollout_events")
			c.unified("rollout_events", "host-1", "host-2", "host-4")
			c.zkForeign["/clickhouse/tables/state/rollout_events/replicas"] = []string{"02-r1"}
		},
			want: "rollout_events: birleşik ZK yolu /clickhouse/tables/state/rollout_events'de tablosu olmayan host'un replika kaydı kalmış (02-r1)"},
		{label: "bayat znode sahibi (mixed)", setup: func(c *sprConn) {
			c.unified("argocd_app_status", "host-1", "host-2")
			c.zkForeign["/clickhouse/tables/state/argocd_app_status/replicas"] = []string{"02-r1"}
		},
			want: "argocd_app_status: birleşik ZK yolu /clickhouse/tables/state/argocd_app_status'de tablosu olmayan host'un replika kaydı kalmış (02-r1)"},
		// v0.10.965 — fiziksel satır aynı (birleşme yeni koşuyu gizledi) ama son yazım onaydan sonra: ret.
		{label: "eval: birleşme gizledi, son yazım onaydan sonra", setup: func(c *sprConn) { c.state["host-1"]["ai_eval_runs"].rows = 7 },
			mutate: func(c *sprConn, a *StatePathRebuildAck) { c.evalLastWriteMs = a.MeasuredAt + 60_000 },
			want:   "ai_eval_runs: plandan sonra bir evalset koşusu yazıldı"},
		// v0.10.965 — ai_eval_runs yalnız host-3'te (yarım DROP): tablo okunamaz, parça yoklaması taze parça görürse ret.
		{label: "eval legacy_partial: taze parça", setup: func(c *sprConn) {
			for _, h := range []string{"host-1", "host-2", "host-4"} {
				delete(c.state[h], "ai_eval_runs")
			}
			c.evalFreshParts = 1
		},
			want: "ai_eval_runs: son 15 dk içinde 1 taze parça"},
		{label: "replika adı çakışması", setup: func(c *sprConn) { c.macros["host-2"]["replica"] = "r1" },
			want: `replika adı çakışması: {shard}-{replica} host-1, host-2 host'larında "01-r1"`},
		{label: "veritabanı Atomic değil", setup: func(c *sprConn) { c.dbEngine["host-4"] = "Ordinary" },
			want: "host-4: veritabanı motoru Ordinary (Atomic değil)"},
		{label: "düz motor", setup: func(c *sprConn) { c.state["host-3"]["rollout_events"] = &sprTable{engine: "ReplacingMergeTree"} },
			want: "rollout_events: host-3'te motor ReplacingMergeTree (Replicated değil)"},
	}
	for _, cl := range cells {
		t.Run(cl.label, func(t *testing.T) {
			q := cl.queue
			if q.Verdict == "" {
				q = sprHealthy
			}
			sprSeams(t, q)
			c := newSprConn()
			c.allTen()
			if cl.setup != nil {
				cl.setup(c)
			}
			s := sprStore(c)
			tables := sprTen()
			_, ack := sprPlanAck(t, s, c, tables)
			if cl.mutate != nil {
				cl.mutate(c, ack)
			}
			res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: tables, Ack: ack})
			sprRefused(t, cl.label, res, err, c, cl.want)
		})
	}
}

func TestStatePathRebuildRowTolerancePasses(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	c.legacy("ingest_ledger", 50000)
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	c.state["host-1"]["ingest_ledger"].rows += 200 // ~dakikalık defter büyümesi
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil || !res.OK {
		t.Fatalf("+200 satır geçmeliydi: %v / %+v", err, res)
	}
}

func TestStatePathRebuildRejectsBeforeAnyQuery(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	for _, tables := range [][]string{{"problems"}, {"ingest_ledger", "ingest_ledger"}} {
		_, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: tables, Ack: &StatePathRebuildAck{MeasuredAt: time.Now().UnixMilli()}})
		var ge *StatePathGateError
		if !errors.As(err, &ge) {
			t.Fatalf("%v: ret yok (%v)", tables, err)
		}
		if len(c.queries) != 0 || len(c.execs) != 0 {
			t.Errorf("%v: ret öncesi %d sorgu / %d ifade", tables, len(c.queries), len(c.execs))
		}
	}
	p, err := s.PlanStatePathRebuild(context.Background(), []string{"problems"})
	if err != nil || len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "geçersiz tablo: problems — izin listesinde değil") || len(c.queries) != 0 {
		t.Errorf("plan: %v / %+v / %d sorgu", err, p, len(c.queries))
	}
}

func TestStatePathRebuildOnlyTwoSkipsSettings(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	c.settings["rollouts"] = `{"source":"v2"}` // okunsaydı reddederdi
	s := sprStore(c)
	two := []string{"ingest_ledger", "ai_eval_runs"}
	for _, e := range statePathAllowlist[2:] {
		c.unified(e.Name, spHosts...) // sekiz zaten birleşik: eski yolda tablo kalmasın
	}
	_, ack := sprPlanAck(t, s, c, two)
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: two, Ack: ack})
	if err != nil || !res.OK || len(res.StillLegacy) != 0 {
		t.Fatalf("iki tablo: %v / %+v", err, res)
	}
	if c.queried("system_settings") {
		t.Error("sekizden hiçbiri kurulmazken Rollouts/Argo ayarı okundu")
	}
	if c.count("DROP") != 2 || c.count("CREATE") != 2 {
		t.Errorf("ifadeler: %v", c.execs)
	}
}

func TestStatePathRebuildDropErrorStops(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	c.execErrAt, c.execErr = 3, errors.New("code: 999, message: Coordination error")
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != "drop" || res.OK || len(c.execs) != 3 || c.count("CREATE") != 0 {
		t.Errorf("DROP hatası: phase=%s ok=%v execs=%d", res.Phase, res.OK, len(c.execs))
	}
	if !strings.HasPrefix(res.Resume, "DROP yarıda kaldı.") || res.Statements[2].OK || !strings.Contains(res.Statements[2].Err, "Coordination error") {
		t.Errorf("devam / ifade: %q / %+v", res.Resume, res.Statements)
	}
	// Son ölçüm: ilk iki tablo gitti, gerisi eski (eski yolda kalan sekiz).
	if res.Tables[0].After != "absent" || res.Tables[2].After != "legacy" || len(res.StillLegacy) != 8 || slices.Contains(res.StillLegacy, "ingest_ledger") {
		t.Errorf("after: %s / %s kalan %v", res.Tables[0].After, res.Tables[2].After, res.StillLegacy)
	}
}

func TestStatePathRebuildDropLag(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	c.dropLag["host-3"] = true
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != "drop_wait" || res.OK || c.count("CREATE") != 0 || c.count("DROP") != 10 {
		t.Errorf("drop lag: phase=%s ok=%v execs=%v", res.Phase, res.OK, c.execs)
	}
	if !strings.Contains(res.Resume, "ingest_ledger (host-3)") || !strings.Contains(res.Resume, "CREATE koşmadı") {
		t.Errorf("devam metni host-3'ü anmıyor: %q", res.Resume)
	}
	if res.Tables[0].After != "legacy_partial" {
		t.Errorf("after = %s", res.Tables[0].After)
	}
}

// v0.10.965 — DROP tabloyu host'lardan kaldırdı ama birleşik znode'da replika
// kaydı kaldı: drop_wait CREATE'i koşturmamalı (R11), devam metni kaydı anar.
func TestStatePathRebuildDropWaitZnodeLeftover(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	c.beforeExec = func(c *sprConn, q string) {
		if strings.HasPrefix(q, "DROP TABLE IF EXISTS ingest_ledger ") {
			c.zkForeign["/clickhouse/tables/state/ingest_ledger/replicas"] = []string{"02-r1"}
		}
	}
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != "drop_wait" || res.OK || c.count("DROP") != 10 || c.count("CREATE") != 0 {
		t.Errorf("znode artığı: phase=%s ok=%v execs=%v", res.Phase, res.OK, c.execs)
	}
	if !strings.Contains(res.Resume, "ingest_ledger (birleşik znode'da replika: 02-r1)") || !strings.Contains(res.Resume, "CREATE koşmadı") {
		t.Errorf("devam metni znode artığını anmıyor: %q", res.Resume)
	}
}

// v0.10.965 — zaman aşımı OLMAYAN bir CREATE hatası "create" fazında durur;
// sonraki CREATE'ler koşmaz, devam metni kurulanları sayar.
func TestStatePathRebuildCreateErrorStops(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	c.execErrAt, c.execErr = 12, errors.New("code: 999, message: Coordination error") // 10 DROP, sonra 2. CREATE
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != "create" || res.OK || len(c.execs) != 12 || c.count("CREATE") != 2 {
		t.Errorf("CREATE hatası: phase=%s ok=%v execs=%d CREATE=%d", res.Phase, res.OK, len(c.execs), c.count("CREATE"))
	}
	if !strings.HasPrefix(res.Resume, "Kurma yarıda kaldı: 1 tablo kuruldu.") {
		t.Errorf("devam = %q", res.Resume)
	}
}

// v0.10.965 — ai_eval_runs yalnız host-3'te kaldı (hata ile bitmiş ON CLUSTER
// DROP yeniden denenmez): tablonun kendisi okunamaz (UNKNOWN_TABLE), ama
// parça yoklaması taze yazım görmezse sihirbaz onu düşürüp kurabilmeli.
func TestStatePathRebuildEvalLegacyPartialByParts(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	for _, h := range []string{"host-1", "host-2", "host-4"} {
		delete(c.state[h], "ai_eval_runs")
	}
	s := sprStore(c)
	p, ack := sprPlanAck(t, s, c, sprTen())
	if len(p.Blocked) != 0 || p.Tables[1].State != "legacy_partial" || p.Tables[1].Action != "rebuild" {
		t.Fatalf("plan: engel %v, ai_eval_runs %+v", p.Blocked, p.Tables[1])
	}
	if !slices.ContainsFunc(p.Checks, func(s string) bool { return strings.Contains(s, "parça yoklaması") }) {
		t.Errorf("parça yoklaması kontrolü yok: %v", p.Checks)
	}
	if c.queried("`coremetry`.`ai_eval_runs`") {
		t.Error("tablo bir host'ta yokken ai_eval_runs'ın kendisi okundu (UNKNOWN_TABLE)")
	}
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !slices.ContainsFunc(c.execs, func(e string) bool { return strings.HasPrefix(e, "DROP TABLE IF EXISTS ai_eval_runs ") }) ||
		!slices.ContainsFunc(c.execs, func(e string) bool { return strings.HasPrefix(e, "CREATE TABLE IF NOT EXISTS ai_eval_runs ") }) {
		t.Errorf("ai_eval_runs DROP + CREATE koşmadı: %v", c.execs)
	}
	if !res.OK || res.Phase != "done" || !res.Tables[1].Verified {
		t.Errorf("sonuç: ok=%v phase=%s ai_eval_runs=%+v", res.OK, res.Phase, res.Tables[1])
	}
}

// v0.10.965 — 12 dk bütçe CREATE fazında doldu: kalan CREATE'ler GÖNDERİLMEZ
// ("kuyrukta" sayılmaz), faz "create", son ölçüm taze bağlamda yapılır.
func TestStatePathRebuildBudgetExpiresInCreate(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	c.beforeExec = func(_ *sprConn, q string) {
		if strings.HasPrefix(q, "CREATE") {
			cancel(context.DeadlineExceeded)
		}
	}
	res, err := s.ApplyStatePathRebuild(ctx, StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if c.count("CREATE") != 1 || res.Phase != "create" || res.OK {
		t.Fatalf("bütçe: CREATE=%d phase=%s ok=%v", c.count("CREATE"), res.Phase, res.OK)
	}
	last := res.Statements[len(res.Statements)-1]
	if len(res.Statements) != 11 || last.OK || !strings.Contains(last.Err, "süre bütçesi doldu") {
		t.Errorf("ifadeler: %d, son %+v", len(res.Statements), last)
	}
	for _, st := range res.Statements[:10] {
		if st.Err == statePathPendingErr {
			t.Errorf("gönderilmemiş ifade kuyrukta sayıldı: %+v", st)
		}
	}
	if strings.Contains(res.Resume, "son ölçüm okunamadı") || res.Tables[2].After != "absent" {
		t.Errorf("son ölçüm taze bağlamda yapılmadı: devam %q, after %q", res.Resume, res.Tables[2].After)
	}
	if !strings.Contains(res.Resume, "12 dk süre bütçesi doldu — Kurma yarıda kaldı: 0 tablo kuruldu.") {
		t.Errorf("devam = %q", res.Resume)
	}
}

// v0.10.965 — bütçe 3. DROP sırasında doldu: DUR, CREATE yok, faz "drop".
func TestStatePathRebuildBudgetExpiresInDrop(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	n := 0
	c.beforeExec = func(_ *sprConn, q string) {
		if n++; n == 3 {
			cancel(context.DeadlineExceeded)
		}
	}
	res, err := s.ApplyStatePathRebuild(ctx, StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.execs) != 3 || c.count("CREATE") != 0 || res.Phase != "drop" || len(res.Statements) != 3 {
		t.Fatalf("bütçe DROP: execs=%d phase=%s ifadeler=%d", len(c.execs), res.Phase, len(res.Statements))
	}
	if !strings.Contains(res.Statements[2].Err, "gönderildiği kesin değil") || !strings.HasPrefix(res.Resume, "12 dk süre bütçesi doldu — DROP yarıda kaldı.") {
		t.Errorf("ifade / devam: %+v / %q", res.Statements[2], res.Resume)
	}
}

func TestStatePathRebuildBootLegacyRecreateCaughtByVerify(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	// DROP yoklaması bittikten sonra ingest_ledger ESKİ yola kurulur;
	// sihirbazın CREATE IF NOT EXISTS'i no-op kalır. v0.10.971 — kural 3
	// kalktı; bu yarış artık yalnız (a) ON CLUSTER DROP bir host'ta henüz
	// işlenmemişken açılan pod'un kural 2 ile komşusuna katılması ya da (b)
	// v0.10.970 veya eski imajlı bir pod'un açılmasıyla doğar. Doğrulama yine
	// yakalar.
	c.beforeExec = func(c *sprConn, q string) {
		if strings.HasPrefix(q, "CREATE TABLE IF NOT EXISTS ingest_ledger ") {
			c.legacy("ingest_ledger", 0)
		}
	}
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if res.Phase != "verify" || res.OK || res.Tables[0].After != "legacy" || res.Tables[0].Verified {
		t.Errorf("verify yakalamadı: phase=%s ok=%v after=%s", res.Phase, res.OK, res.Tables[0].After)
	}
	if !res.Tables[1].Verified || !slices.Equal(res.StillLegacy, []string{"ingest_ledger"}) {
		t.Errorf("kalanlar: %v", res.StillLegacy)
	}
	if !strings.Contains(res.Resume, "ingest_ledger doğrulanamadı (şimdi: eski yol)") || res.Note != "" {
		t.Errorf("devam / not: %q / %q", res.Resume, res.Note)
	}
}

func TestStatePathRebuildResumeMix(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	// 6 birleşik, 2 yok (önceki koşu düşürmüş), 2 eski.
	for _, n := range []string{"argocd_app_status", "argocd_sync_events", "argocd_app_mapping", "rollout_classification", "ado_commit_enrichment", "rollout_worker_runs"} {
		c.unified(n, spHosts...)
	}
	for _, h := range spHosts {
		delete(c.state[h], "rollout_events")
		delete(c.state[h], "rollout_workload_state")
	}
	s := sprStore(c)
	p, ack := sprPlanAck(t, s, c, sprTen())
	if len(ack.Tables) != 4 {
		t.Errorf("onay yalnız ATLA olmayanları taşımalı: %+v", ack.Tables)
	}
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatalf("%v (plan engeli %v)", err, p.Blocked)
	}
	if c.count("DROP") != 2 || c.count("CREATE") != 4 || len(c.execs) != 6 {
		t.Errorf("ifadeler: %v", c.execs)
	}
	for _, e := range c.execs {
		for _, n := range []string{"argocd_app_status", "rollout_worker_runs"} {
			if strings.Contains(e, " "+n+" ") {
				t.Errorf("birleşik %s ifade aldı: %s", n, stmtHead(e))
			}
		}
	}
	if !res.OK || len(res.StillLegacy) != 0 {
		t.Errorf("sonuç: %+v", res)
	}
}

// TestStatePathRebuildPartialRunsWithoutPartialOK — v0.10.971: kural 3 kalktı,
// kısmi seçim partialOK istemez. Seçim dışı eski tablolar planda ve sonuçta
// BİLGİ olarak görünür (bölünmüş kalırlar); veri onayı (ack) aynen şart.
func TestStatePathRebuildPartialRunsWithoutPartialOK(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	eight := sprTen()[2:]
	p, ack := sprPlanAck(t, s, c, eight)
	if !slices.Equal(p.StillLegacyAfter, []string{"ai_eval_runs", "ingest_ledger"}) || len(p.Blocked) != 0 {
		t.Errorf("plan kısmi seçimi engellememeli, kalanları listelemeli: %+v", p)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "partialOK") || strings.Contains(w, "kilid") {
			t.Errorf("plan uyarısı kaldırılan kilit metnini taşıyor: %q", w)
		}
	}
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: eight, Ack: ack})
	if err != nil {
		t.Fatalf("kısmi seçim (geçerli veri onayıyla) reddedildi: %v", err)
	}
	if !res.OK || !slices.Equal(res.StillLegacy, []string{"ai_eval_runs", "ingest_ledger"}) || c.count("DROP") != 8 {
		t.Errorf("kısmi: ok=%v kalan=%v DROP=%d", res.OK, res.StillLegacy, c.count("DROP"))
	}
	// Onay kapısı aynen: kısmi seçimde de bayat onay reddedilir.
	c2 := newSprConn()
	c2.allTen()
	s2 := sprStore(c2)
	_, ack2 := sprPlanAck(t, s2, c2, eight)
	ack2.MeasuredAt -= int64(31 * time.Minute / time.Millisecond)
	res2, err2 := s2.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: eight, Ack: ack2})
	sprRefused(t, "kısmi, bayat onay", res2, err2, c2, "onay bayat")
}

// TestStatePathRebuildFinalReadErrorInResume — v0.10.971: son ölçüm okunamazsa
// hata eskiden LockReason'da taşınırdı; artık devam metninin önünde.
func TestStatePathRebuildFinalReadErrorInResume(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	c.execErrAt, c.execErr = 3, errors.New("code: 999, message: Coordination error")
	c.beforeExec = func(c *sprConn, _ string) {
		if len(c.execs) == 3 {
			c.replicasErr = errors.New("code: 159, message: Timeout exceeded")
		}
	}
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Phase != "drop" || !strings.HasPrefix(res.Resume, "son ölçüm okunamadı: system.replicas: code: 159") ||
		!strings.Contains(res.Resume, "DROP yarıda kaldı.") {
		t.Errorf("devam = %q (phase %s ok %v)", res.Resume, res.Phase, res.OK)
	}
}

func TestStatePathRebuildCreateQueued159(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, ack := sprPlanAck(t, s, c, sprTen())
	c.createErr = errors.New("code: 159, message: Watching task /clickhouse/task_queue/ddl/query-0000000123 is executing longer than distributed_ddl_task_timeout (=180) seconds. There are 2 unfinished hosts (0 of them are currently executing the task), they are going to execute the query in background")
	// Kuyruk CREATE'leri ancak ÜÇÜNCÜ doğrulama okumasında uygular: yoklama
	// gerçekten beklemeli (ilk okumada "doğrulandı" diyen bir döngü burada
	// son ölçümde yakalanır ve OK=false döner).
	c.deferAfter = 3
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Phase != "done" || c.count("CREATE") != 10 {
		t.Fatalf("159: ok=%v phase=%s", res.OK, res.Phase)
	}
	for _, st := range res.Statements[10:] {
		if st.OK || st.Err != "kuyrukta — durum yoklamasıyla doğrulanıyor" {
			t.Errorf("CREATE 159 bekliyor olarak kaydedilmeli: %+v", st)
		}
	}
}

func TestStatePathRebuildBusy(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	statePathRebuildBusy.Store(true)
	res, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: &StatePathRebuildAck{}})
	sprRefused(t, "meşgul", res, err, c, "başka bir yeniden kurulum bu pod'da sürüyor")
	if len(c.queries) != 0 {
		t.Error("meşgulken ölçüm yapıldı")
	}
	if !statePathRebuildBusy.Load() {
		t.Error("meşgul bayrağı başkasınınken sıfırlandı")
	}
	statePathRebuildBusy.Store(false)
	// Bayrak apply sonunda bırakılır.
	_, ack := sprPlanAck(t, s, c, sprTen())
	if _, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "uptrace_all", Tables: sprTen(), Ack: ack}); err != nil {
		t.Fatal(err)
	}
	if statePathRebuildBusy.Load() {
		t.Error("apply sonrası meşgul bayrağı kaldı")
	}
}

func TestStatePathPlanOtherClusterAndSingleNode(t *testing.T) {
	sprSeams(t, sprHealthy)
	c := newSprConn()
	c.allTen()
	s := sprStore(c)
	_, err := s.ApplyStatePathRebuild(context.Background(), StatePathRebuildRequest{Cluster: "other_all", Tables: sprTen(), Ack: &StatePathRebuildAck{}})
	sprRefused(t, "başka küme", nil, err, c, `küme "other_all" uygulamanın kümesi değil ("uptrace_all")`)
	if len(c.queries) != 0 {
		t.Error("küme reddi okumadan ÖNCE olmalı (ad SQL'e girmeden)")
	}
	single := &Store{conn: c}
	p, err := single.PlanStatePathRebuild(context.Background(), nil)
	if err != nil || len(p.Blocked) != 1 || p.Blocked[0] != "küme kipi değil" {
		t.Errorf("tek düğüm: %v / %+v", err, p)
	}
}
