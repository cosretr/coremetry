package chstore

import (
	"encoding/json"
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
	// v0.10.818 — yapısal üçlü: no_replication > not_replicated > missing_replica > session_expired.
	if got := worstVerdict([]string{ReplicaSessionExpired, ReplicaMissing, ReplicaNotReplicated}); got != ReplicaNotReplicated {
		t.Errorf("got %s", got)
	}
	if got := worstVerdict(nil); got != ReplicaOK {
		t.Errorf("got %s", got)
	}
	// Her karar sıralamada — yeni bir sabit eklenip sıraya konmazsa 0 = ok sayılır.
	for _, v := range []string{ReplicaOK, ReplicaSingle, ReplicaUnmapped, ReplicaLagging, ReplicaDivergent, ReplicaReadOnly, ReplicaSessionExpired, ReplicaMissing, ReplicaNotReplicated, ReplicaNoReplication} {
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
		"clusterAllReplicas('%s', system.tables)", "clusterAllReplicas('%s', system.clusters)", // v0.10.818
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
	// v0.10.818 — her clusterAllReplicas okuması TEK TEK: zaman tavanı + erişilemeyen
	// host'u atlama (sayım karşılaştırması bir yorumla kandırılabilirdi; sorgu
	// gövdesi backtick'e kadar okunur).
	segs := strings.Split(s, "clusterAllReplicas('%s'")
	if len(segs) < 6 { // macros, one, clusters, tables, replicas, parts
		t.Fatalf("clusterAllReplicas okuması %d (en az 6)", len(segs)-1)
	}
	for i, seg := range segs[1:] {
		end := strings.Index(seg, "`")
		if end < 0 {
			end = len(seg)
		}
		body := seg[:end]
		if !strings.Contains(body, "skip_unavailable_shards = 1") || !strings.Contains(body, "max_execution_time") {
			t.Errorf("clusterAllReplicas okuması #%d ayarsız: %q", i+1, body)
		}
	}
	for _, want := range []string{"system.one)", "trows.Err()", "prows.Err()", "lrows.Err()", "orows.Err()", "mrows.Err()"} {
		if !strings.Contains(s, want) {
			t.Errorf("eksik: %s", want)
		}
	}
}

// v0.10.818 — kapsama kararı: beklenen (erişilebilir) host'lar vs kayıtlı.
func TestShardCoverage(t *testing.T) {
	known := []ReplicaState{rep("ch-04", "/t/02/x", 1, nil)}
	// Tablo ch-03'te hiç yok → missing_replica.
	miss, v, hint, unseen := shardCoverage("x", []string{"ch-03", "ch-04"}, known, map[string]string{"ch-04": "ReplicatedMergeTree"})
	if v != ReplicaMissing || len(miss) != 1 || miss[0].Host != "ch-03" || miss[0].Engine != "" || !strings.Contains(hint, "ch-03 (tablo yok)") || !strings.Contains(hint, "is_local") || unseen != nil {
		t.Errorf("tablo yok: v=%s miss=%+v hint=%s unseen=%v", v, miss, hint, unseen)
	}
	// Tablo ch-03'te düz MergeTree → not_replicated.
	miss, v, hint, _ = shardCoverage("x", []string{"ch-03", "ch-04"}, known, map[string]string{"ch-03": "MergeTree", "ch-04": "ReplicatedMergeTree"})
	if v != ReplicaNotReplicated || len(miss) != 1 || miss[0].Engine != "MergeTree" || !strings.Contains(hint, "engine=MergeTree") {
		t.Errorf("düz tablo: v=%s miss=%+v hint=%s", v, miss, hint)
	}
	// ch-03'te Replicated ama system.replicas satırı yok → karar DEĞİL, unseen notu.
	miss, v, _, unseen = shardCoverage("x", []string{"ch-03", "ch-04"}, known, map[string]string{"ch-03": "ReplicatedReplacingMergeTree", "ch-04": "ReplicatedReplacingMergeTree"})
	if v != "" || miss != nil || len(unseen) != 1 || unseen[0] != "ch-03" {
		t.Errorf("Replicated ama kayıtsız: v=%q miss=%v unseen=%v", v, miss, unseen)
	}
	// Herkes kayıtlı → karar yok.
	if miss, v, _, _ := shardCoverage("x", []string{"ch-04"}, known, nil); v != "" || miss != nil {
		t.Errorf("tam kapsama: v=%q miss=%v", v, miss)
	}
	// Beklenen host yok (roster boş) → karar yok.
	if _, v, _, _ := shardCoverage("x", nil, known, nil); v != "" {
		t.Errorf("roster boş: %q", v)
	}
	// Kapsama kararı sıralamada replicaVerdict'in üstündedir ama no_replication'ın altında.
	if replicaVerdictRank[ReplicaMissing] <= replicaVerdictRank[ReplicaSessionExpired] || replicaVerdictRank[ReplicaNotReplicated] >= replicaVerdictRank[ReplicaNoReplication] {
		t.Error("sıra: session_expired < missing_replica < not_replicated < no_replication")
	}
}

// v0.10.818 — müşteri vakası uçtan uca: tek host 1/1 → replicaVerdict "single";
// kapsama "missing_replica" → birleşim missing_replica (kartın eski "tek
// replika · tutarlı" yalanı biter). no_replication kapsamaya yenilmez.
func TestMergeCoverageCustomerCase(t *testing.T) {
	known := []ReplicaState{rep("ch-04", "/t/02/alert_rules", 1, nil)}
	base, baseHint, _, _ := replicaVerdict(known)
	if base != ReplicaSingle {
		t.Fatalf("taban %s", base)
	}
	miss, cv, chint, _ := shardCoverage("alert_rules", []string{"ch-03", "ch-04"}, known, map[string]string{"ch-04": "ReplicatedReplacingMergeTree"})
	v, hint := mergeCoverage(base, baseHint, cv, chint)
	if v != ReplicaMissing || hint != chint || len(miss) != 1 {
		t.Fatalf("birleşim: %s / %s / %+v", v, hint, miss)
	}
	if v, h := mergeCoverage(ReplicaNoReplication, "yol", ReplicaMissing, "eksik"); v != ReplicaNoReplication || h != "yol" {
		t.Errorf("no_replication kapsamaya yenilmemeli: %s %s", v, h)
	}
	if v, h := mergeCoverage(ReplicaOK, "", "", ""); v != ReplicaOK || h != "" {
		t.Errorf("kapsama yokken taban: %s %s", v, h)
	}
}

// v0.10.818 — is_local kör host'lar: sayım 0 (kendini görmüyor) ve satır yok (küme adı tanımsız).
func TestBlindHosts(t *testing.T) {
	zero, absent := blindHosts([]string{"ch-01", "ch-02", "ch-03", "ch-04"}, map[string]uint32{"ch-01": 1, "ch-02": 0, "ch-04": 1})
	if len(zero) != 1 || zero[0] != "ch-02" || len(absent) != 1 || absent[0] != "ch-03" {
		t.Errorf("zero=%v absent=%v", zero, absent)
	}
	if z, a := blindHosts(nil, nil); z != nil || a != nil {
		t.Error("boş roster → boş")
	}
}

// v0.10.818 — roster uyarıları: cevap vermeyen (küme tanımı > cevap) ve cevap verip eşlenemeyen ayrı.
func TestRosterWarnings(t *testing.T) {
	w := rosterWarnings(4, []string{"ch-01", "ch-02", "ch-04"}, []string{"ch-04"})
	if len(w) != 2 || !strings.Contains(w[0], "4 host var, 3 host cevap verdi") || !strings.Contains(w[1], "ch-04: cevap verdi ama shard'a eşlenemedi") {
		t.Errorf("%v", w)
	}
	if w := rosterWarnings(2, []string{"a", "b"}, nil); len(w) != 0 {
		t.Errorf("tam roster → uyarı yok: %v", w)
	}
}

// v0.10.818 — düz-MergeTree-her-yerde tablo: replicas JSON `[]` (null FE'yi düşürüyordu).
func TestReplicaShardReplicasNeverNull(t *testing.T) {
	var rs []ReplicaState
	if rs == nil {
		rs = []ReplicaState{}
	}
	b, err := json.Marshal(ReplicaShard{Shard: 2, Replicas: rs, Verdict: ReplicaNotReplicated})
	if err != nil || !strings.Contains(string(b), `"replicas":[]`) {
		t.Errorf("%s %v", b, err)
	}
	src, _ := os.ReadFile("replica_consistency.go")
	if !strings.Contains(string(src), "rs = []ReplicaState{} // JSON `[]`") {
		t.Error("gruplama nil dilimi boş dilime çevirmeli")
	}
}

// v0.10.792 — hostName() → shard eşlemesi: system.clusters adı YA DA adresi;
// ikisi de tutmazsa {shard} makrosu (sayısal → sayı, "01" → 1; değilse sıralı
// ayrık değer sırası). Test ortamı: küme tanımı IP'li, hostName() OS adı —
// 791 her satırı "eşlenemedi" gösterdi, makrolar doğruyken.
func TestShardRefFor(t *testing.T) {
	hosts := []clusterHostRow{
		{Shard: 1, Replica: 1, Host: "10.0.0.1", Addr: "10.0.0.1"},
		{Shard: 1, Replica: 2, Host: "ch-02", Addr: "10.0.0.2"},
		{Shard: 2, Replica: 1, Host: "10.0.0.3", Addr: "10.0.0.3"},
	}
	cases := []struct {
		name    string
		host    string
		macros  map[string]string
		shards  []string
		wantSh  int
		wantRep int
		wantVia string
	}{
		{"ad eşleşir", "ch-02", nil, nil, 1, 2, "clusters"},
		{"adres eşleşir", "10.0.0.3", nil, nil, 2, 1, "clusters"},
		{"makro sayısal, baştaki sıfır", "ch-01", map[string]string{"shard": "01", "replica": "node1"}, []string{"01", "02"}, 1, 0, "macro"},
		{"makro sayısal 02", "ch-04", map[string]string{"shard": "02"}, []string{"01", "02"}, 2, 0, "macro"},
		{"makro sayısal değil → sıralı", "ch-09", map[string]string{"shard": "shb"}, []string{"sha", "shb"}, 2, 0, "macro"},
		{"makro sıfır", "ch-00", map[string]string{"shard": "00"}, []string{"00"}, 0, 0, "macro"},
		{"hiçbiri", "ch-99", map[string]string{"replica": "x"}, nil, -1, 0, ""},
		{"makro yok", "ch-98", nil, nil, -1, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sh, rep, via := shardRefFor(c.host, hosts, c.macros, c.shards)
			if sh != c.wantSh || rep != c.wantRep || via != c.wantVia {
				t.Fatalf("got (%d,%d,%q) want (%d,%d,%q)", sh, rep, via, c.wantSh, c.wantRep, c.wantVia)
			}
		})
	}
}

// v0.10.824 — `.inner_id.<uuid>` → view eşlemesi (operatör, test kümesi
// 2026-09-20: kart iç tabloya `_fix` + ATTACH PARTITION + EXCHANGE
// merdivenini bastı). Sözleşme: Atomic `TO INNER UUID` biçimi DE eski
// biçim (iç uuid = view uuid) DE çözülür; `TO <tablo>` MV'sinin gizli iç
// tablosu YOKTUR; MV olmayan satırlar eşlemeye girmez; MV satırı hiç
// gelmemişse iç tablo çözülmez (View boş kalır, uydurulmaz).
func TestInnerTableViews(t *testing.T) {
	const (
		viewUUID  = "11111111-1111-1111-1111-111111111111"
		innerUUID = "22222222-2222-2222-2222-222222222222"
		legacy    = "33333333-3333-3333-3333-333333333333"
		toTable   = "44444444-4444-4444-4444-444444444444"
	)
	rows := []mvTableRow{
		// Atomic combined MV: iç tablo AYRI uuid taşır (v0.10.780 dersi).
		{Host: "ch-01", Name: "service_summary_5m", Engine: "MaterializedView", UUID: viewUUID,
			CreateQuery: "CREATE MATERIALIZED VIEW db.service_summary_5m TO INNER UUID '" + innerUUID + "' (`time_bucket` DateTime) AS SELECT 1"},
		// Eski biçim: DDL'de TO yok → iç uuid = view uuid.
		{Host: "ch-01", Name: "operation_summary_5m", Engine: "MaterializedView", UUID: legacy,
			CreateQuery: "CREATE MATERIALIZED VIEW db.operation_summary_5m (`time_bucket` DateTime) ENGINE = AggregatingMergeTree AS SELECT 1"},
		// TO <tablo> MV: hedefi gerçek tablo, gizli iç tablo yok.
		{Host: "ch-01", Name: "span_links_reverse_mv", Engine: "MaterializedView", UUID: toTable,
			CreateQuery: "CREATE MATERIALIZED VIEW db.span_links_reverse_mv TO db.span_links_reverse AS SELECT 1"},
		// MV olmayan satırlar (iç tablonun kendisi dahil) eşlemeye girmez.
		{Host: "ch-01", Name: ".inner_id." + innerUUID, Engine: "AggregatingMergeTree", UUID: innerUUID},
		{Host: "ch-02", Name: "spans_local", Engine: "ReplicatedMergeTree", UUID: "55555555-5555-5555-5555-555555555555"},
		// uuid'siz MV (Ordinary DB) çözülemez.
		{Host: "ch-02", Name: "legacy_mv", Engine: "MaterializedView", UUID: zeroUUID, CreateQuery: "CREATE MATERIALIZED VIEW db.legacy_mv AS SELECT 1"},
	}
	got := innerTableViews(rows)
	want := map[string]string{
		".inner_id." + innerUUID: "service_summary_5m",
		".inner_id." + legacy:    "operation_summary_5m",
	}
	if len(got) != len(want) {
		t.Fatalf("eşleme %d girdi: %v", len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s → %q, istenen %q", k, got[k], v)
		}
	}
	if _, ok := got[".inner_id."+toTable]; ok {
		t.Error("TO <tablo> MV'si iç tablo eşlemesine girmemeli")
	}
	// Çözülemeyen iç tablo: MV satırı hiç gelmediyse (o host MV'yi kaybetmiş)
	// eşlemede yok — çağıran View'ı boş bırakır.
	if v := got[".inner_id.99999999-9999-9999-9999-999999999999"]; v != "" {
		t.Errorf("çözülemeyen iç tablo uydurulmamalı: %q", v)
	}
	if len(innerTableViews(nil)) != 0 {
		t.Error("boş girdi → boş eşleme")
	}
	if !isInnerTable(".inner_id.abc") || isInnerTable("spans_local") || isInnerTable("inner_id.abc") {
		t.Error("isInnerTable öneki")
	}
}

// v0.10.824 — iç tablo ipucu YALNIZ yapısal üç kararda eklenir; ıraksama /
// readonly / oturum kararlarında SYSTEM SYNC/RESTORE doğru kalır (uuid
// değişmez), yanlış olan tablo takasıdır.
// Taban ipucu DEĞİŞTİRİLİR, eklenmez: 818'in "ATTACH edip değiştir" reçetesi
// MV uyarısının yanında dursaydı tek cümle kendiyle çelişirdi.
func TestInnerShardHint(t *testing.T) {
	// Gerçek taban: shardCoverage'ın not_replicated ipucu (tablo merdivenini anlatır).
	_, _, base, _ := shardCoverage(".inner_id.abc", []string{"ch-03", "ch-04"},
		[]ReplicaState{rep("ch-04", "/t/01/x", 1, nil)}, map[string]string{"ch-03": "AggregatingMergeTree", "ch-04": "ReplicatedAggregatingMergeTree"})
	if !strings.Contains(base, "ATTACH edip") {
		t.Fatalf("taban artık tablo merdivenini anlatmıyor, test anlamsızlaştı: %q", base)
	}
	for _, v := range []string{ReplicaMissing, ReplicaNotReplicated, ReplicaNoReplication} {
		got := innerShardHint(base, true, v)
		if got != innerTableHint {
			t.Errorf("%s: %q", v, got)
		}
		if strings.Contains(got, "ATTACH edip") {
			t.Errorf("%s: çelişen tablo reçetesi kaldı: %q", v, got)
		}
		if got := innerShardHint("", true, v); got != innerTableHint {
			t.Errorf("%s boş taban: %q", v, got)
		}
	}
	for _, v := range []string{ReplicaOK, ReplicaSingle, ReplicaUnmapped, ReplicaLagging, ReplicaDivergent, ReplicaReadOnly, ReplicaSessionExpired} {
		if got := innerShardHint(base, true, v); got != base {
			t.Errorf("%s ipucuna dokunmamalı: %q", v, got)
		}
	}
	if got := innerShardHint(base, false, ReplicaNotReplicated); got != base {
		t.Errorf("iç tablo değil: %q", got)
	}
}

// v0.10.824 — kaynak pini: motor envanteri MV kimliğini AYNI okumada taşır
// (ikinci küme geneli sorgu açılmadı) ve yapısal kararda iç tablo ipucu
// uygulanır. Metin kaybolursa kart yine EXCHANGE merdivenini basar.
func TestReplicaInventoryCarriesMVIdentity(t *testing.T) {
	src, err := os.ReadFile("replica_consistency.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		"create_table_query",                        // MV DDL'i envanterde
		"engine = 'MaterializedView'",               // MV satırları da çekilir
		"innerTableViews(mvRows)",                   // eşleme kuruluyor
		"tbl.Inner, tbl.View = true, innerViews[t]", // satıra bağlanıyor
		"rsh.Hint = innerShardHint(",                // ipucu uygulanıyor
	} {
		if !strings.Contains(s, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	// İpucu metni TEK sabitte; ikinci bir gövde sapardı.
	if n := strings.Count(s, "MV iç tablosu: ATTACH/EXCHANGE uygulanmaz"); n != 1 {
		t.Errorf("ipucu metni %d yerde (tek sabit olmalı)", n)
	}
	if !strings.Contains(s, "innerUUIDFor(") {
		t.Error("iç uuid çözümü dangling_mv_admin ile aynı gövdeden gelmeli (innerUUIDFor)")
	}
}
