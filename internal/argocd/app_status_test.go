package argocd

// app_status_test.go — v0.10.983 — Rollouts v2 P3.1 saf çekirdeği
// (app_status.go; docs/rollouts/v2-audit.md §5.1–5.4, §10.3.3).
// Sözleşme:
//   - StatusRow, migrations/0015'teki argocd_app_status kolonlarını SIRASIYLA
//     adıyla (`ch`) ve tipiyle aynalar; yazıcı sözleşmesi (TTL çapası, sözlük,
//     sync satırının fazı) INSERT'ten önce reddeder.
//   - Sorgular: parça seçicisi instance namespace'i (+ job), kopyalar `max by`
//     ile katlanır (pod/instance yok), sabit olmayan küme `unless` ile,
//     senkron penceresi değişen + yeni doğan sayaçları verir.
//   - Çözücü: app_ns = exported_namespace, yoksa namespace (§5.2); aynı
//     anahtarda iki farklı durum = BELİRSİZ (o tik atlanır, yanlış satır yok).
//   - Sayaç farkı: artış, sıfırlanma (reset), taban varken yeni doğan seri;
//     taban yokken yeni seri SAYILMAZ.
//   - Diff: ilk koşu taban; değişimde tek satır; aynı tikte senkron + değişim
//     TEK 'sync' satırı; yokluk iki tam envanter sonra 'deleted'; toplu yokluk
//     dondurulur; belirsiz uygulama önceki durumunda kalır.

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func stDDLColumns(t *testing.T) [][2]string {
	t.Helper()
	b, err := os.ReadFile("../../migrations/0015_rollouts_v2.sql")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "CREATE TABLE IF NOT EXISTS argocd_app_status ")
	if i < 0 {
		t.Fatal("0015'te argocd_app_status yok")
	}
	body := src[i:]
	body = body[strings.Index(body, "(\n")+2:]
	body = body[:strings.Index(body, ") ENGINE")]
	var cols [][2]string
	for _, ln := range strings.Split(body, "\n") {
		if c := strings.Index(ln, "--"); c >= 0 {
			ln = ln[:c]
		}
		ln = strings.TrimSuffix(strings.TrimSpace(ln), ",")
		if ln == "" {
			continue
		}
		f := strings.Fields(ln)
		cols = append(cols, [2]string{f[0], f[1]})
	}
	return cols
}

func TestStatusRowMirrorsDDL(t *testing.T) {
	ddl := stDDLColumns(t)
	rt := reflect.TypeOf(StatusRow{})
	if rt.NumField() != len(ddl) {
		t.Fatalf("DDL %d kolon, Go %d alan: %v", len(ddl), rt.NumField(), ddl)
	}
	for i, c := range ddl {
		f := rt.Field(i)
		var typ string
		switch f.Type {
		case reflect.TypeOf(""):
			typ = "string"
		case reflect.TypeOf(uint64(0)):
			typ = "UInt64"
		case reflect.TypeOf(time.Time{}):
			typ = "DateTime64(3)"
		default:
			typ = f.Type.String()
		}
		want := c[1]
		if want == "String" || want == "LowCardinality(String)" {
			want = "string"
		}
		if f.Tag.Get("ch") != c[0] || typ != want {
			t.Fatalf("kolon %d: DDL %v, Go %s %s", i, c, f.Tag.Get("ch"), typ)
		}
	}
	if got := strings.Join(StatusColumns(), ", "); !strings.HasPrefix(got, "instance_id, app_namespace, app_name, changed_at") || !strings.HasSuffix(got, "cluster_id, version") {
		t.Fatalf("StatusColumns sırası: %s", got)
	}
}

var stT0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func stValidRow() StatusRow {
	return StatusRow{InstanceID: "team-a-prod", AppNamespace: "team-a-prod", AppName: "team-a-api-prod-ca", ChangedAt: stT0,
		ChangeKind: ChangeState, SyncStatus: "OutOfSync", HealthStatus: "Healthy", Version: uint64(stT0.UnixNano())}
}

func TestValidateStatusRow(t *testing.T) {
	if err := ValidateStatusRow(stValidRow()); err != nil {
		t.Fatalf("geçerli satır reddedildi: %v", err)
	}
	for name, mut := range map[string]func(*StatusRow){
		"sıfır çapa":           func(r *StatusRow) { r.ChangedAt = time.Time{} },
		"epoch çapa":           func(r *StatusRow) { r.ChangedAt = time.Unix(0, 0) },
		"boş instance":         func(r *StatusRow) { r.InstanceID = "" },
		"boş ad":               func(r *StatusRow) { r.AppName = " " },
		"boş app ns":           func(r *StatusRow) { r.AppNamespace = "" },
		"sözlük dışı tür":      func(r *StatusRow) { r.ChangeKind = "moved" },
		"sync fazsız":          func(r *StatusRow) { r.ChangeKind = ChangeSync },
		"fazlı ama sync değil": func(r *StatusRow) { r.SyncPhase = "Succeeded" },
		"version sıfır":        func(r *StatusRow) { r.Version = 0 },
		"autosync bozuk":       func(r *StatusRow) { r.AutoSync = "yes" },
	} {
		r := stValidRow()
		mut(&r)
		if ValidateStatusRow(r) == nil {
			t.Errorf("%s: reddedilmeliydi", name)
		}
	}
	ok := stValidRow()
	ok.ChangeKind, ok.SyncPhase = ChangeSync, "Succeeded"
	if err := ValidateStatusRow(ok); err != nil {
		t.Fatalf("fazlı sync satırı geçerli: %v", err)
	}
}

func TestShardSelectorAndQueries(t *testing.T) {
	sel := ShardSelector(Instance{HubNamespace: "team-a-prod", MetricsJob: `team-a-prod-"metrics"`})
	if sel != `namespace="team-a-prod",job="team-a-prod-\"metrics\""` {
		t.Fatalf("seçici: %s", sel)
	}
	if s := ShardSelector(Instance{HubNamespace: "gitops"}); s != `namespace="gitops"` {
		t.Fatalf("job'suz seçici: %s", s)
	}
	inv := InventoryQuery(sel)
	if !strings.HasPrefix(inv, appInfoBy+" (argocd_app_info{"+sel+"})") || strings.Contains(inv, "pod") {
		t.Fatalf("envanter: %s", inv)
	}
	ns := NonSteadyQuery(`namespace="g"`)
	want := appInfoBy + ` (argocd_app_info{namespace="g"} unless argocd_app_info{namespace="g",sync_status="Synced",health_status="Healthy",operation=""})`
	if ns != want {
		t.Fatalf("sabit olmayan:\n got %s\nwant %s", ns, want)
	}
	// Hedefli okuma: ≤ ServiceSelectorCap ad/sorgu, en çok maxQ sorgu; kalanı döner.
	var names []string
	for i := 0; i < ServiceSelectorCap*3+5; i++ {
		names = append(names, "app-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+strings.Repeat("y", i/26))
	}
	qs, rest := TargetedQueries(`namespace="g"`, append(names, names[0], " "), 2)
	if len(qs) != 2 || len(rest) != len(names)-2*ServiceSelectorCap {
		t.Fatalf("hedefli: %d sorgu, %d kalan (ad %d)", len(qs), len(rest), len(names))
	}
	if !strings.Contains(qs[0], `argocd_app_info{namespace="g",name=~"`) || strings.Count(qs[0], "|") != ServiceSelectorCap-1 {
		t.Fatalf("hedefli sorgu biçimi: %s", qs[0])
	}
	if q, r := TargetedQueries(`namespace="g"`, nil, 5); len(q)+len(r) != 0 {
		t.Fatal("boş ad listesi sorgu üretmemeli")
	}
	sw := SyncWindowQuery(`namespace="g"`, 3*time.Minute)
	for _, part := range []string{
		`sum by (namespace, exported_namespace, name, phase) (argocd_app_sync_total{namespace="g",dry_run!="true"}) and on (namespace, exported_namespace, name, phase) (`,
		`(sum by (namespace, exported_namespace, name, phase) (changes(argocd_app_sync_total{namespace="g",dry_run!="true"}[180s])) > 0)`,
		`unless on (namespace, exported_namespace, name, phase) sum by (namespace, exported_namespace, name, phase) (argocd_app_sync_total{namespace="g",dry_run!="true"} offset 180s)`,
	} {
		if !strings.Contains(sw, part) {
			t.Errorf("senkron penceresi %q içermeli:\n%s", part, sw)
		}
	}
	// v0.10.983 inceleme: dry-run senkronu (Argo v3.1+ `dry_run`, §5.1)
	// sayılmaz — dört sayaç seçicisinin (güncel, changes, doğan, offset) hepsi.
	if n := strings.Count(sw, `,dry_run!="true"}`); n != 4 {
		t.Errorf("senkron penceresinde 4 dry-run dışı seçici beklenir, %d:\n%s", n, sw)
	}
	if SyncFullQuery(`namespace="g"`) != `sum by (namespace, exported_namespace, name, phase) (argocd_app_sync_total{namespace="g",dry_run!="true"})` {
		t.Fatalf("tam sayaç: %s", SyncFullQuery(`namespace="g"`))
	}
	// v0.10.983 ikinci inceleme: yalnız argocd_app_info üreten job'ların hedefleri.
	if lq := LivenessQuery(`namespace="g"`); lq != `count_values("up", up{namespace="g"} and on (job) group by (job) (argocd_app_info{namespace="g"}))` {
		t.Fatalf("hedef sağlığı: %s", lq)
	}
}

func TestParseShardApps(t *testing.T) {
	raw := json.RawMessage(`[
	 {"metric":{"namespace":"inst","exported_namespace":"inst","name":"a1","sync_status":"Synced","health_status":"Healthy","autosync_enabled":"true","project":"p","repo":"r","dest_server":"https://x:6443","dest_namespace":"d"},"value":[1,"1"]},
	 {"metric":{"namespace":"inst","exported_namespace":"inst","name":"a1","sync_status":"Synced","health_status":"Healthy","autosync_enabled":"true","project":"p","repo":"r","dest_server":"https://x:6443","dest_namespace":"d","job":"j2"},"value":[1,"1"]},
	 {"metric":{"namespace":"inst","exported_namespace":"apps","name":"a1","sync_status":"OutOfSync","health_status":"Healthy","autosync_enabled":"FALSE"},"value":[1,"1"]},
	 {"metric":{"namespace":"inst","exported_namespace":"inst","name":"flap","sync_status":"Synced","health_status":"Healthy"},"value":[1,"1"]},
	 {"metric":{"namespace":"inst","exported_namespace":"inst","name":"flap","sync_status":"OutOfSync","health_status":"Healthy"},"value":[1,"1"]},
	 {"metric":{"namespace":"other","exported_namespace":"inst","name":"foreign"},"value":[1,"1"]},
	 {"metric":{"namespace":"inst","name":"b1","autosync_enabled":"maybe"},"value":[1,"1"]},
	 {"metric":{"namespace":"inst","exported_namespace":"inst","name":""},"value":[1,"1"]}
	]`)
	obs, err := ParseShardApps("i1", "inst", "vector", raw)
	if err != nil {
		t.Fatal(err)
	}
	a1 := obs.Apps[AppKey{"i1", "inst", "a1"}]
	if a1 != (AppTuple{SyncStatus: "Synced", HealthStatus: "Healthy", AutoSync: "true", Project: "p", Repo: "r", DestServer: "https://x:6443", DestNamespace: "d"}) {
		t.Fatalf("a1 (durum A, kopya katlanır): %+v", a1)
	}
	if c := obs.Apps[AppKey{"i1", "apps", "a1"}]; c.SyncStatus != "OutOfSync" || c.AutoSync != "false" {
		t.Fatalf("durum C: app_ns = exported_namespace, ayrı anahtar: %+v", c)
	}
	if b := obs.Apps[AppKey{"i1", "inst", "b1"}]; b.AutoSync != "" {
		t.Fatalf("durum B: app_ns = namespace; tanınmayan autosync boş: %+v", b)
	}
	if !obs.Ambiguous[AppKey{"i1", "inst", "flap"}] {
		t.Fatal("aynı anahtarda iki farklı durum belirsiz olmalı")
	}
	if _, ok := obs.Apps[AppKey{"i1", "inst", "flap"}]; ok {
		t.Fatal("belirsiz uygulama gözlem sayılmamalı")
	}
	if len(obs.Apps) != 3 || obs.Skipped != 2 {
		t.Fatalf("3 uygulama + 2 atlanan (yabancı ns, boş ad) beklenir: %d / %d", len(obs.Apps), obs.Skipped)
	}
	if _, err := ParseShardApps("i1", "inst", "matrix", json.RawMessage(`[]`)); err == nil {
		t.Fatal("vector olmayan sonuç hata")
	}
	var o ShardObs
	o.Merge(obs)
	o.Merge(ShardObs{Apps: map[AppKey]AppTuple{{"i1", "inst", "a1"}: {SyncStatus: "OutOfSync"}}})
	if !o.Ambiguous[AppKey{"i1", "inst", "a1"}] || len(o.Apps) != 2 {
		t.Fatalf("birleşimde çelişen durum belirsiz olmalı: %+v", o)
	}
}

func TestParseSyncCounters(t *testing.T) {
	raw := json.RawMessage(`[
	 {"metric":{"namespace":"inst","exported_namespace":"apps","name":"a1","phase":"Succeeded"},"value":[1,"4"]},
	 {"metric":{"namespace":"inst","name":"a2","phase":"Failed"},"value":[1,"2"]},
	 {"metric":{"namespace":"inst","name":"a3","phase":"Succeeded"},"value":[1,"NaN"]},
	 {"metric":{"namespace":"inst","name":"a4","phase":"Succeeded"},"value":[1,"-1"]},
	 {"metric":{"namespace":"inst","name":"","phase":"Succeeded"},"value":[1,"1"]}
	]`)
	m, err := ParseSyncCounters("inst", "vector", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[SyncKey{"apps", "a1", "Succeeded"}] != 4 || m[SyncKey{"inst", "a2", "Failed"}] != 2 {
		t.Fatalf("sayaçlar: %v", m)
	}
}

func TestSyncIncreases(t *testing.T) {
	k := func(n, p string) SyncKey { return SyncKey{"ns", n, p} }
	prev := map[SyncKey]float64{k("up", "Succeeded"): 3, k("same", "Succeeded"): 5, k("reset", "Succeeded"): 9, k("noise", "Succeeded"): 2}
	cur := map[SyncKey]float64{k("up", "Succeeded"): 5, k("same", "Succeeded"): 5, k("reset", "Succeeded"): 1, k("new", "Failed"): 1, k("noise", "Succeeded"): 2.2}
	inc, diag := SyncIncreases(prev, cur, true)
	if len(inc) != 3 || inc[k("up", "Succeeded")] != 2 || inc[k("reset", "Succeeded")] != 1 || inc[k("new", "Failed")] != 1 {
		t.Fatalf("artışlar: %v", inc)
	}
	if diag["sync_counter_reset"] != 1 || diag["sync_newborn"] != 1 {
		t.Fatalf("teşhis: %v", diag)
	}
	inc, diag = SyncIncreases(prev, cur, false)
	if _, ok := inc[k("new", "Failed")]; ok || diag["sync_unbaselined"] != 1 {
		t.Fatalf("taban yokken yeni seri sayılmaz: %v %v", inc, diag)
	}
}

func TestSyncPhases(t *testing.T) {
	inc := map[SyncKey]float64{
		{"ns", "a", "Failed"}: 1, {"ns", "a", "Succeeded"}: 1,
		{"ns", "b", "Error"}: 1, {"ns", "b", "Failed"}: 2,
		{"ns", "c", "Zeta"}: 1, {"ns", "c", "Alpha"}: 1,
	}
	got := SyncPhases("i", inc)
	want := map[AppKey]string{{"i", "ns", "a"}: "Succeeded", {"i", "ns", "b"}: "Failed", {"i", "ns", "c"}: "Alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("faz önceliği: %v", got)
	}
}

func TestDestClusterID(t *testing.T) {
	by := map[string]string{"https://api.a.example:6443": "c-a"}
	norm := func(u string) string { return strings.TrimSuffix(strings.ToLower(u), "/") }
	for in, want := range map[string]string{
		"https://API.a.example:6443/":        "c-a",
		"https://kubernetes.default.svc":     "hub-1",
		"https://kubernetes.default.svc:443": "hub-1",
		"":                                   "",
		"https://unknown.example:6443":       "",
	} {
		if got := DestClusterID(in, "hub-1", by, norm); got != want {
			t.Errorf("%q → %q, beklenen %q", in, got, want)
		}
	}
}

// ── DiffStatus ─────────────────────────────────────────────────────────────

func stKey(n string) AppKey { return AppKey{"i1", "inst", n} }

func stDiff(in DiffInput) DiffOutput {
	in.InstanceID, in.HubClusterID, in.ChangedAt = "i1", "hub-1", stT0
	if in.ClusterOf == nil {
		in.ClusterOf = func(ds string) string {
			if ds == "https://a" {
				return "c-a"
			}
			return ""
		}
	}
	var v uint64 = 100
	in.NextVersion = func() uint64 { v++; return v }
	return DiffStatus(in)
}

func stMem(n string, tup AppTuple) AppMem {
	r := StatusRow{InstanceID: "i1", AppNamespace: "inst", AppName: n, ChangedAt: stT0.Add(-time.Hour), ChangeKind: ChangeBaseline, Version: 1}
	r = r.WithTuple(tup)
	return AppMem{Row: r}
}

var (
	stSynced = AppTuple{SyncStatus: "Synced", HealthStatus: "Healthy", DestServer: "https://a", Project: "p"}
	stOOS    = AppTuple{SyncStatus: "OutOfSync", HealthStatus: "Healthy", DestServer: "https://a", Project: "p"}
)

func TestDiffStatusFirstRunBaseline(t *testing.T) {
	obs := ShardObs{Apps: map[AppKey]AppTuple{stKey("a"): stSynced, stKey("b"): {SyncStatus: "Synced", HealthStatus: "Healthy", DestServer: "https://z"}}}
	out := stDiff(DiffInput{Inventory: true, Baseline: true, Obs: obs, Prev: map[AppKey]AppMem{}})
	if len(out.Rows) != 2 {
		t.Fatalf("iki taban satırı: %+v", out.Rows)
	}
	for _, r := range out.Rows {
		if r.ChangeKind != ChangeBaseline || !r.ChangedAt.Equal(stT0) || r.Version <= 100 {
			t.Fatalf("taban satırı: %+v", r)
		}
		if err := ValidateStatusRow(r); err != nil {
			t.Fatalf("üretilen satır yazıcı sözleşmesini geçmeli: %v", err)
		}
	}
	if out.Rows[0].AppName != "a" || out.Rows[0].ClusterID != "c-a" || out.Rows[1].ClusterID != "" {
		t.Fatalf("sıra ve cluster_id: %+v", out.Rows)
	}
	if len(out.Next) != 2 {
		t.Fatalf("bellek: %v", out.Next)
	}
	// Envanter DIŞI okumada taban yazılmaz (bilinmeyen uygulama bekler).
	out = stDiff(DiffInput{Inventory: false, Baseline: true, Obs: obs, Prev: map[AppKey]AppMem{}})
	if len(out.Rows) != 0 || out.Diag["unknown_before_inventory"] != 2 {
		t.Fatalf("envanter öncesi bilinmeyen: %+v %v", out.Rows, out.Diag)
	}
}

func TestDiffStatusChangeSyncAppeared(t *testing.T) {
	prev := map[AppKey]AppMem{
		stKey("steady"): stMem("steady", stSynced),
		stKey("flip"):   stMem("flip", stSynced),
		stKey("synced"): stMem("synced", stSynced),
		stKey("both"):   stMem("both", stSynced),
	}
	gone := stMem("back", stSynced)
	gone.Deleted = true
	prev[stKey("back")] = gone
	obs := ShardObs{Apps: map[AppKey]AppTuple{
		stKey("steady"): stSynced, stKey("flip"): stOOS, stKey("synced"): stSynced, stKey("both"): stOOS,
		stKey("back"): stSynced, stKey("new"): stOOS,
	}}
	out := stDiff(DiffInput{Obs: obs, Prev: prev, Syncs: map[AppKey]string{stKey("synced"): "Succeeded", stKey("both"): "Failed"}})
	got := map[string]StatusRow{}
	for _, r := range out.Rows {
		if _, dup := got[r.AppName]; dup {
			t.Fatalf("uygulama başına tik başına EN FAZLA bir satır: %s", r.AppName)
		}
		got[r.AppName] = r
	}
	if _, ok := got["steady"]; ok || len(got) != 5 {
		t.Fatalf("değişmeyene satır yok; 5 satır: %v", got)
	}
	if r := got["flip"]; r.ChangeKind != ChangeState || r.SyncStatus != "OutOfSync" || r.Project != "p" || r.ClusterID != "c-a" {
		t.Fatalf("state: %+v", r)
	}
	if r := got["synced"]; r.ChangeKind != ChangeSync || r.SyncPhase != "Succeeded" {
		t.Fatalf("sync: %+v", r)
	}
	if r := got["both"]; r.ChangeKind != ChangeSync || r.SyncPhase != "Failed" || r.SyncStatus != "OutOfSync" {
		t.Fatalf("senkron + değişim TEK sync satırı, yeni tuple ile: %+v", r)
	}
	if got["back"].ChangeKind != ChangeAppeared || got["new"].ChangeKind != ChangeAppeared {
		t.Fatalf("silinmişin dönüşü ve yeni uygulama appeared: %+v %+v", got["back"], got["new"])
	}
	if out.Next[stKey("back")].Deleted || out.Next[stKey("flip")].Row.SyncStatus != "OutOfSync" {
		t.Fatalf("bellek güncellenmeli: %+v", out.Next)
	}
}

func TestDiffStatusAbsenceAndMass(t *testing.T) {
	prev := map[AppKey]AppMem{stKey("a"): stMem("a", stOOS), stKey("b"): stMem("b", stSynced)}
	obs := ShardObs{Apps: map[AppKey]AppTuple{stKey("b"): stSynced}}
	// Envanter dışı okuma yokluk çıkarmaz.
	out := stDiff(DiffInput{Obs: obs, Prev: prev})
	if len(out.Rows) != 0 || out.Next[stKey("a")].Absent != 0 {
		t.Fatalf("sabit olmayan okumada yokluk yok: %+v", out)
	}
	// 1. tam envanter: sayılır, satır yok.
	out = stDiff(DiffInput{Inventory: true, Obs: obs, Prev: prev})
	if len(out.Rows) != 0 || out.Next[stKey("a")].Absent != 1 {
		t.Fatalf("ilk yokluk yalnız sayılır: %+v", out)
	}
	// 2. tam envanter: deleted, son tuple taşınır.
	out = stDiff(DiffInput{Inventory: true, Obs: obs, Prev: out.Next})
	if len(out.Rows) != 1 || out.Rows[0].ChangeKind != ChangeDeleted || out.Rows[0].SyncStatus != "OutOfSync" || !out.Next[stKey("a")].Deleted {
		t.Fatalf("ikinci yoklukta deleted: %+v", out.Rows)
	}
	// Silinmiş uygulama yine yoksa yeni satır yok.
	out = stDiff(DiffInput{Inventory: true, Obs: obs, Prev: out.Next})
	if len(out.Rows) != 0 {
		t.Fatalf("silinmişe tekrar satır yok: %+v", out.Rows)
	}

	// Toplu yokluk: 30 bilinen, 20'si yok → dondurulur.
	big := map[AppKey]AppMem{}
	few := ShardObs{Apps: map[AppKey]AppTuple{}}
	for i := 0; i < 30; i++ {
		n := "app-" + string(rune('A'+i))
		m := stMem(n, stSynced)
		m.Absent = 1
		big[stKey(n)] = m
		if i < 10 {
			few.Apps[stKey(n)] = stSynced
		}
	}
	out = stDiff(DiffInput{Inventory: true, Obs: few, Prev: big})
	if !out.Mass || len(out.Rows) != 0 || out.Next[stKey("app-Z")].Absent != 1 {
		t.Fatalf("toplu yokluk dondurulmalı: mass=%v rows=%d", out.Mass, len(out.Rows))
	}
	out = stDiff(DiffInput{Inventory: true, Obs: few, Prev: big, MassAccept: true})
	if len(out.Rows) != 20 {
		t.Fatalf("süre dolunca kabul: %d satır", len(out.Rows))
	}
}

func TestDiffStatusAmbiguousKeepsPrev(t *testing.T) {
	prev := map[AppKey]AppMem{stKey("a"): stMem("a", stSynced)}
	obs := ShardObs{Apps: map[AppKey]AppTuple{}, Ambiguous: map[AppKey]bool{stKey("a"): true}}
	out := stDiff(DiffInput{Inventory: true, Obs: obs, Prev: prev, Syncs: map[AppKey]string{stKey("a"): "Succeeded"}})
	if len(out.Rows) != 0 || out.Next[stKey("a")].Absent != 0 || out.Next[stKey("a")].Row.SyncStatus != "Synced" {
		t.Fatalf("belirsiz uygulama önceki durumda kalır, yok sayılmaz: %+v", out)
	}
	if out.Diag["app_ambiguous"] != 1 {
		t.Fatalf("teşhis: %v", out.Diag)
	}
}

// Aynı tik aralığında ikinci satır önceki satırı (anahtar aynı) ezmesin:
// önceki satırın changed_at'inin 1 ms sonrasına kayar.
func TestDiffStatusSameIntervalBump(t *testing.T) {
	m := stMem("a", stSynced)
	m.Row.ChangedAt, m.Row.ChangeKind, m.Row.SyncPhase = stT0, ChangeSync, "Succeeded"
	out := stDiff(DiffInput{Obs: ShardObs{Apps: map[AppKey]AppTuple{stKey("a"): stOOS}}, Prev: map[AppKey]AppMem{stKey("a"): m}})
	if len(out.Rows) != 1 || !out.Rows[0].ChangedAt.Equal(stT0.Add(time.Millisecond)) || out.Diag["changed_at_bumped"] != 1 {
		t.Fatalf("aynı aralıkta kaydırma: %+v %v", out.Rows, out.Diag)
	}
	m.Row.ChangedAt = stT0.Add(-time.Minute)
	out = stDiff(DiffInput{Obs: ShardObs{Apps: map[AppKey]AppTuple{stKey("a"): stOOS}}, Prev: map[AppKey]AppMem{stKey("a"): m}})
	if !out.Rows[0].ChangedAt.Equal(stT0) {
		t.Fatalf("önceki aralıktaki satır kaydırma gerektirmez: %v", out.Rows[0].ChangedAt)
	}
}

// ── v0.10.983 inceleme düzeltmeleri ───────────────────────────────────────

// v0.10.983 ikinci inceleme: önceki tikte silinmiş uygulama bellekten düşer
// (uzun yaşayan liderde ApplicationSet önizlemeleri birikmesin); geri dönen
// yine 'appeared'. Bu tikte silinen Next'te kalır (changed_at kaydırması).
func TestDiffStatusPrunesDeleted(t *testing.T) {
	prev := map[AppKey]AppMem{stKey("a"): stMem("a", stOOS), stKey("b"): stMem("b", stSynced)}
	obs := ShardObs{Apps: map[AppKey]AppTuple{stKey("b"): stSynced}}
	out := stDiff(DiffInput{Inventory: true, Obs: obs, Prev: prev})
	out = stDiff(DiffInput{Inventory: true, Obs: obs, Prev: out.Next})
	if len(out.Rows) != 1 || !out.Next[stKey("a")].Deleted {
		t.Fatalf("bu tikte silinen Next'te kalır: %+v", out.Rows)
	}
	gone := out.Next
	m := gone[stKey("a")]
	m.Row.ChangedAt = stT0.Add(-time.Minute) // önceki tik
	gone[stKey("a")] = m
	out = stDiff(DiffInput{Obs: obs, Prev: gone})
	if _, ok := out.Next[stKey("a")]; ok || len(out.Next) != 1 {
		t.Fatalf("önceki tikte silinen bellekten düşmeli: %v", out.Next)
	}
	back := ShardObs{Apps: map[AppKey]AppTuple{stKey("a"): stOOS, stKey("b"): stSynced}}
	if out = stDiff(DiffInput{Obs: back, Prev: out.Next}); len(out.Rows) != 1 || out.Rows[0].ChangeKind != ChangeAppeared {
		t.Fatalf("geri dönen uygulama appeared: %+v", out.Rows)
	}
}

func TestParseLiveness(t *testing.T) {
	raw := json.RawMessage(`[{"metric":{"up":"1"},"value":[1,"3"]},{"metric":{"up":"0"},"value":[1,"1"]},{"metric":{"up":"x"},"value":[1,"9"]},{"metric":{"up":"1"},"value":[1,"NaN"]}]`)
	up, down, err := ParseLiveness("vector", raw)
	if err != nil || up != 3 || down != 1 {
		t.Fatalf("up=%d down=%d err=%v", up, down, err)
	}
	if up, down, err := ParseLiveness("vector", json.RawMessage(`[]`)); err != nil || up != 0 || down != 0 {
		t.Fatalf("boş: %d %d %v", up, down, err)
	}
	if _, _, err := ParseLiveness("matrix", nil); err == nil {
		t.Fatal("vector dışı sonuç hata")
	}
}

// Bilinen canlı uygulamaların HEPSİ yoksa (az uygulamalı instance dahil) yokluk
// işlenmez ve MassAccept de bunu kabul etmez; HoldAbsence yokluğu tümden atlar.
func TestDiffStatusTotalAbsenceAndHold(t *testing.T) {
	prev := map[AppKey]AppMem{stKey("a"): stMem("a", stSynced), stKey("b"): stMem("b", stSynced)}
	other := ShardObs{Apps: map[AppKey]AppTuple{stKey("z"): stSynced}}
	for _, accept := range []bool{false, true} {
		cur := prev
		for i := 0; i < 3; i++ {
			out := stDiff(DiffInput{Inventory: true, Obs: other, Prev: cur, MassAccept: accept})
			for _, r := range out.Rows {
				if r.ChangeKind == ChangeDeleted {
					t.Fatalf("toplam yoklukta deleted yazılmaz (accept=%v): %+v", accept, r)
				}
			}
			if !out.TotalAbsence || !out.Mass || out.Next[stKey("a")].Absent != 0 {
				t.Fatalf("toplam yokluk işaretlenmeli, Absent ilerlememeli: %+v", out)
			}
			cur = out.Next
			delete(cur, stKey("z"))
		}
	}
	// HoldAbsence: kısmi yokluk bile sayılmaz.
	obs := ShardObs{Apps: map[AppKey]AppTuple{stKey("b"): stSynced}}
	out := stDiff(DiffInput{Inventory: true, Obs: obs, Prev: prev, HoldAbsence: true})
	if len(out.Rows) != 0 || out.Next[stKey("a")].Absent != 0 || out.Diag["absence_held"] != 1 {
		t.Fatalf("HoldAbsence yokluğu saymaz: %+v", out)
	}
}

// Taban envanterinde belirsiz kalan anahtar, taban bittikten sonra ilk
// gözlendiğinde (envanter dışı okuma dahil) 'baseline' yazılır, 'appeared' değil.
func TestDiffStatusBaselineKeys(t *testing.T) {
	obs := ShardObs{Apps: map[AppKey]AppTuple{stKey("amb"): stOOS, stKey("new"): stOOS}}
	out := stDiff(DiffInput{Obs: obs, Prev: map[AppKey]AppMem{}, BaselineKeys: map[AppKey]bool{stKey("amb"): true}})
	kinds := map[string]string{}
	for _, r := range out.Rows {
		kinds[r.AppName] = r.ChangeKind
	}
	if kinds["amb"] != ChangeBaseline || kinds["new"] != ChangeAppeared {
		t.Fatalf("taban anahtarı baseline, gerçekten yeni appeared: %v", kinds)
	}
}

// Okuma kapsamı değişti (Rebaseline) ve taban bekleniyor: bilinen uygulamanın
// farklı tuple'ı 'state' değil 'baseline'; kapsam değişmeden (yalnız
// yeniden kurulum) 'state' kalır.
func TestDiffStatusRebaseline(t *testing.T) {
	prev := map[AppKey]AppMem{stKey("a"): stMem("a", stSynced)}
	obs := ShardObs{Apps: map[AppKey]AppTuple{stKey("a"): stOOS}}
	out := stDiff(DiffInput{Inventory: true, Baseline: true, Rebaseline: true, Obs: obs, Prev: prev})
	if len(out.Rows) != 1 || out.Rows[0].ChangeKind != ChangeBaseline || out.Rows[0].SyncStatus != "OutOfSync" {
		t.Fatalf("kapsam değişiminde baseline: %+v", out.Rows)
	}
	out = stDiff(DiffInput{Inventory: true, Baseline: true, Obs: obs, Prev: prev})
	if len(out.Rows) != 1 || out.Rows[0].ChangeKind != ChangeState {
		t.Fatalf("yalnız yeniden kurulumda state: %+v", out.Rows)
	}
}

// §10.3.3 "TTL'i dolan uygulama sonraki tam envanterde yeniden taban": son
// satırı RefreshBefore'dan eski, değişmemiş uygulama tam envanterde aynı
// tuple'la 'baseline' ile tazelenir; envanter dışı okumada ya da taze satırda
// yazılmaz.
func TestDiffStatusTTLRefresh(t *testing.T) {
	old := stMem("old", stSynced)
	old.Row.ChangedAt = stT0.Add(-StatusRefreshAge - time.Hour)
	fresh := stMem("fresh", stSynced)
	prev := map[AppKey]AppMem{stKey("old"): old, stKey("fresh"): fresh}
	obs := ShardObs{Apps: map[AppKey]AppTuple{stKey("old"): stSynced, stKey("fresh"): stSynced}}
	out := stDiff(DiffInput{Inventory: true, Obs: obs, Prev: prev, RefreshBefore: stT0.Add(-StatusRefreshAge)})
	if len(out.Rows) != 1 || out.Rows[0].AppName != "old" || out.Rows[0].ChangeKind != ChangeBaseline ||
		out.Rows[0].Tuple() != stSynced || !out.Rows[0].ChangedAt.Equal(stT0) || out.Diag["ttl_refresh"] != 1 {
		t.Fatalf("eski satır tazelenmeli: %+v %v", out.Rows, out.Diag)
	}
	if !out.Next[stKey("old")].Row.ChangedAt.Equal(stT0) {
		t.Fatal("bellek tazelenen satırı taşımalı")
	}
	if out := stDiff(DiffInput{Obs: obs, Prev: prev, RefreshBefore: stT0.Add(-StatusRefreshAge)}); len(out.Rows) != 0 {
		t.Fatalf("envanter dışı okumada tazeleme yok: %+v", out.Rows)
	}
	if StatusRefreshAge >= 180*24*time.Hour {
		t.Fatal("tazeleme yaşı TTL'den (180 gün) kısa olmalı")
	}
}
