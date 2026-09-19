package chstore

import (
	"os"
	"strings"
	"testing"
)

// replica_consistency_test.go — v0.10.791. Karar saf; yanlış dal yanlış
// rozet basar ve operatör yanlış onarıma gider, o yüzden tablo-testli.

func rep(host, zk string, total int, over func(*ReplicaState)) ReplicaState {
	r := ReplicaState{Host: host, ZKPath: zk, ReplicaName: host, TotalReplicas: total, ActiveReplicas: total, Rows: map[string]uint64{}}
	if over != nil {
		over(&r)
	}
	return r
}

func TestReplicaVerdict(t *testing.T) {
	ok := func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-18": 100000, "2026-09-19": 50000} }
	cases := []struct {
		name string
		rs   []ReplicaState
		want string
		hint string // alt dize
	}{
		{"boş", nil, ReplicaOK, ""},
		{"tek replika", []ReplicaState{rep("a", "/t/1/spans", 1, ok)}, ReplicaSingle, "tek replika"},
		{"iki replika aynı yol aynı satır", []ReplicaState{rep("a", "/t/1/spans", 2, ok), rep("b", "/t/1/spans", 2, ok)}, ReplicaOK, ""},
		{"farklı ZK yolu → replikasyon yok", []ReplicaState{rep("a", "/t/a/spans", 1, ok), rep("b", "/t/b/spans", 1, ok)}, ReplicaNoReplication, "FARKLI ZooKeeper yolunda (2 yol)"},
		{"aynı yol ama kayıtlı replika az", []ReplicaState{rep("a", "/t/1/spans", 1, ok), rep("b", "/t/1/spans", 2, ok)}, ReplicaNoReplication, "kayıtlı replika sayısı (1)"},
		{"oturum düşmüş yapısal sorundan sonra gelir", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { ok(r); r.SessionExpired = true }), rep("b", "/t/1/spans", 2, ok)}, ReplicaSessionExpired, "oturumu düşmüş"},
		{"readonly", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { ok(r); r.ReadOnly = true; r.LastException = "Cannot allocate block number" }), rep("b", "/t/1/spans", 2, ok)}, ReplicaReadOnly, "Cannot allocate block number"},
		{"gecikme ıraksamayı bastırır", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { r.DelayS = 120; r.Rows = map[string]uint64{"2026-09-19": 10000} }), rep("b", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-19": 50000} })}, ReplicaLagging, "gecikme 120 sn"},
		{"kuyruk eşiği", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { ok(r); r.Queue = 5000 }), rep("b", "/t/1/spans", 2, ok)}, ReplicaLagging, "kuyruk 5000"},
		{"ıraksama: aynı partition farklı satır", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-18": 100000} }), rep("b", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-18": 60000} })}, ReplicaDivergent, "2026-09-18: %40.0"},
		{"ıraksama: bir replikada partition hiç yok", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-18": 100000} }), rep("b", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{} })}, ReplicaDivergent, "b 0"},
		{"küçük fark tolere (yüzde eşiği)", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-19": 100000} }), rep("b", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-19": 99000} })}, ReplicaOK, ""},
		{"küçük partition tolere (satır tabanı)", []ReplicaState{rep("a", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-19": 400} }), rep("b", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"2026-09-19": 100} })}, ReplicaOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, hint, _, _ := replicaVerdict(c.rs)
			if v != c.want {
				t.Fatalf("karar %q, istenen %q (ipucu: %s)", v, c.want, hint)
			}
			if c.hint != "" && !strings.Contains(hint, c.hint) {
				t.Errorf("ipucu %q içermeli: %q", c.hint, hint)
			}
			if c.want == ReplicaOK && hint != "" {
				t.Errorf("ok kararında ipucu boş olmalı: %q", hint)
			}
		})
	}
}

func TestReplicaVerdictDivergenceReportsWorstPartition(t *testing.T) {
	a := rep("a", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"p1": 100000, "p2": 100000} })
	b := rep("b", "/t/1/spans", 2, func(r *ReplicaState) { r.Rows = map[string]uint64{"p1": 97000, "p2": 50000} })
	v, _, part, pct := replicaVerdict([]ReplicaState{a, b})
	if v != ReplicaDivergent || part != "p2" || pct < 49.9 || pct > 50.1 {
		t.Fatalf("v=%s part=%s pct=%.1f", v, part, pct)
	}
}

func TestWorstVerdictOrder(t *testing.T) {
	if got := worstVerdict([]string{ReplicaOK, ReplicaLagging, ReplicaSingle}); got != ReplicaLagging {
		t.Errorf("got %s", got)
	}
	if got := worstVerdict([]string{ReplicaDivergent, ReplicaNoReplication, ReplicaReadOnly}); got != ReplicaNoReplication {
		t.Errorf("got %s", got)
	}
	if got := worstVerdict(nil); got != ReplicaOK {
		t.Errorf("got %s", got)
	}
	// Her karar sıralamada — yeni bir sabit eklenip sıraya konmazsa 0 = ok sayılır.
	for _, v := range []string{ReplicaOK, ReplicaSingle, ReplicaUnmapped, ReplicaLagging, ReplicaDivergent, ReplicaReadOnly, ReplicaSessionExpired, ReplicaNoReplication} {
		if _, ok := replicaVerdictRank[v]; !ok {
			t.Errorf("%s sıralamada yok", v)
		}
	}
}

// Kaynak pinleri: system.* okumaları ana bağlantıda (node-lokal tablolar,
// RoundRobin'e verilmez — conn_strategy_test gerekçesi), veritabanı
// currentDatabase() ile değil BAĞLI parametreyle süzülür (clusterAllReplicas
// uzak düğümde başka varsayılan DB görebilir), her sorgu zaman tavanlı.
func TestReplicaConsistencyQueriesAreBoundedAndOnMainConn(t *testing.T) {
	src, err := os.ReadFile("replica_consistency.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "telemetryReadConn") {
		t.Error("system.* okumaları ana bağlantıda kalmalı")
	}
	for _, want := range []string{
		"clusterAllReplicas('%s', system.replicas)", "clusterAllReplicas('%s', system.parts)", "clusterAllReplicas('%s', system.macros)",
		"WHERE database = ?", "WHERE database = ? AND active",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	if strings.Contains(s, "database = currentDatabase()") {
		t.Error("clusterAllReplicas sorgusunda currentDatabase() süzgeci: uzak düğümde başka DB'ye çözülebilir; bağlı parametre kullan")
	}
	if n := strings.Count(s, "max_execution_time"); n < 3 {
		t.Errorf("küme geneli sorguların hepsi zaman tavanlı olmalı (bulunan %d)", n)
	}
}
