package chstore

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

// attr_kvh_alias_admin.go — v0.10.2410: 0017 SİHİRBAZI (operatör: "0017'yi
// çalıştırmak için bir sihirbaz yap"; "ihtiyaç varsa çıksınlar, önkontrolleri
// kendileri yapsınlar"). 0014 sihirbazının yerini alır (0014 nesnelerinin
// hepsi burada: v2 kolon + indeks, anahtar indeksleri, attr_kvh ALIAS).
//
// Dış Distributed kurulumda boot 0017'yi KOŞMAZ (attr_index.go
// repairAttrIndexCols). Sihirbaz:
//   - durum: adım başına nesne × host (spans_local + Distributed sarmalayıcı),
//     görünen her pod'un derleyici kipi (none/legacy/v2), host sürümleri,
//     erişilebilirlik, disk boşluğu, partition'lar + bekleyen mutasyonlar;
//   - TEK koşu: bekleyen adımlar sırayla, her adım öncesi yeniden probe, ilk
//     hatada durur; tekrar koşmak güvenli (biten adım atlanır, ifadeler IF
//     [NOT] EXISTS). Pod restart YOK: pod'lar attrIndexReprobeLoop ile
//     kolonları kendileri görür; koşu "switch" adımında görünen her pod v2
//     diyene kadar bekler, demezse eski kolona dokunmadan durur.
//   - MATERIALIZE INDEX varsayılan KAPALI; açıkken partition partition
//     (her partition'ın mutasyonu bitmeden sonrakine geçilmez).
//
// ASLA `MODIFY COLUMN … ALIAS` (eski part'larda veri kalır, sonraki
// mutasyonlar Code 47) — eski kolon tek ALTER'da DROP + ADD … ALIAS.

const attrKVHMigrationFile = "0017_attr_kvh_alias.sql"

const (
	AttrKVHStepColumns     = "columns"
	AttrKVHStepIndexes     = "indexes"
	AttrKVHStepSwitch      = "switch"
	AttrKVHStepLegacy      = "legacy"
	AttrKVHStepMaterialize = "materialize"
)

// attrKVHRequiredSteps — sihirbazın "iş bitti" hükmü bu dördüne bakar;
// MATERIALIZE opsiyonel (yalnız eski part'larda budama).
var attrKVHRequiredSteps = []string{AttrKVHStepColumns, AttrKVHStepIndexes, AttrKVHStepSwitch, AttrKVHStepLegacy}

var attrKVHStepTitles = map[string]string{
	AttrKVHStepColumns:     "1 · v2 ALIAS kolonları (attr_kvh2, res_kvh2)",
	AttrKVHStepIndexes:     "2 · bloom indeksleri (kvh2 + anahtar)",
	AttrKVHStepSwitch:      "3 · pod'lar v2 derleyici kipine geçti",
	AttrKVHStepLegacy:      "4 · eski attr_kvh/res_kvh → ALIAS (indeks düşer, DROP+ADD)",
	AttrKVHStepMaterialize: "5 · (opsiyonel) eski part'lara indeks — MATERIALIZE INDEX",
}

var attrKVHPartitionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// AttrIndexModeName — bu pod'un derleyici kipi (pod heartbeat'i + durum).
func AttrIndexModeName() string {
	switch attrIndexMode.Load() {
	case attrIndexV2:
		return "v2"
	case attrIndexLegacy:
		return "legacy"
	}
	return "none"
}

// attrKVHTables — küme kipinde yerel + sarmalayıcı; tek düğümde yalnız spans.
func attrKVHTables(cluster string) (local string, all []string) {
	if strings.TrimSpace(cluster) == "" {
		return "spans", []string{"spans"}
	}
	return "spans_local", []string{"spans_local", "spans"}
}

func attrKVHOnCluster(cluster string) string {
	if c := strings.TrimSpace(cluster); c != "" {
		return " ON CLUSTER " + c
	}
	return ""
}

// attrKVHStepSQL — SAF: adımın ifadeleri (0017 ile birebir; test pinler).
// cluster "" = tek düğüm (ON CLUSTER yok, tablo spans). partitionID yalnız
// materialize için ("" = tüm tablo).
func attrKVHStepSQL(step, cluster, partitionID string) []string {
	local, tables := attrKVHTables(cluster)
	oc := attrKVHOnCluster(cluster)
	var out []string
	switch step {
	case AttrKVHStepColumns:
		for _, t := range tables {
			for _, c := range attrIndexV2Cols {
				out = append(out, fmt.Sprintf("ALTER TABLE %s%s ADD COLUMN IF NOT EXISTS %s Array(UInt64) ALIAS %s", t, oc, c.col, attrIndexV2Expr(c)))
			}
		}
	case AttrKVHStepIndexes:
		for _, c := range attrIndexV2Cols {
			out = append(out, fmt.Sprintf("ALTER TABLE %s%s ADD INDEX IF NOT EXISTS %s %s TYPE bloom_filter(0.01) GRANULARITY 4", local, oc, c.idx, c.col))
		}
		// Anahtar indeksleri 0014'ten (EXISTS yüklemi); 0014 hiç koşmamış
		// kurulumda da v2 yolu tam olsun diye burada (IF NOT EXISTS).
		for _, c := range attrIndexV2Cols {
			out = append(out, fmt.Sprintf("ALTER TABLE %s%s ADD INDEX IF NOT EXISTS %s %s TYPE bloom_filter(0.01) GRANULARITY 4", local, oc, c.keysIdx, c.keysCol))
		}
	case AttrKVHStepLegacy:
		for _, c := range attrIndexCols {
			out = append(out, fmt.Sprintf("ALTER TABLE %s%s DROP INDEX IF EXISTS %s", local, oc, c.idx))
		}
		for _, t := range tables {
			for _, c := range attrIndexCols {
				out = append(out, fmt.Sprintf("ALTER TABLE %s%s DROP COLUMN IF EXISTS %s, ADD COLUMN %s Array(UInt64) ALIAS %s", t, oc, c.col, c.col, attrIndexExpr(c)))
			}
		}
	case AttrKVHStepMaterialize:
		in := ""
		if partitionID != "" {
			in = " IN PARTITION ID '" + partitionID + "'"
		}
		for _, c := range attrIndexV2Cols {
			out = append(out, fmt.Sprintf("ALTER TABLE %s%s MATERIALIZE INDEX %s%s", local, oc, c.idx, in))
		}
	}
	return out
}

// AttrKVHObject — durum probe'unun taradığı nesne ve İSTENEN hâli.
type AttrKVHObject struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // column | index
	Table string `json:"table"`
	Want  string `json:"want"` // present | absent | alias
}

// attrKVHStepObjects — SAF: adımın "bitti" sayılması için nesneler.
func attrKVHStepObjects(step, cluster string) []AttrKVHObject {
	local, tables := attrKVHTables(cluster)
	var out []AttrKVHObject
	switch step {
	case AttrKVHStepColumns:
		for _, t := range tables {
			for _, c := range attrIndexV2Cols {
				out = append(out, AttrKVHObject{Name: c.col, Kind: "column", Table: t, Want: "present"})
			}
		}
	case AttrKVHStepIndexes:
		for _, c := range attrIndexV2Cols {
			out = append(out, AttrKVHObject{Name: c.idx, Kind: "index", Table: local, Want: "present"})
		}
		for _, c := range attrIndexV2Cols {
			out = append(out, AttrKVHObject{Name: c.keysIdx, Kind: "index", Table: local, Want: "present"})
		}
	case AttrKVHStepLegacy:
		for _, c := range attrIndexCols {
			out = append(out, AttrKVHObject{Name: c.idx, Kind: "index", Table: local, Want: "absent"})
		}
		for _, t := range tables {
			for _, c := range attrIndexCols {
				out = append(out, AttrKVHObject{Name: c.col, Kind: "column", Table: t, Want: "alias"})
			}
		}
	}
	return out
}

// AttrKVHObjectStatus — nesnenin host başına hâli (istenen hâldeki host sayısı).
type AttrKVHObjectStatus struct {
	AttrKVHObject
	Hosts     int    `json:"hosts"`
	HaveHosts int    `json:"haveHosts"`
	State     string `json:"state"` // ok | partial | missing | unknown
	Err       string `json:"err,omitempty"`
}

// attrKVHHave — SAF: probe sayımı → istenen hâldeki host sayısı.
// "absent" için sayım nesnenin BULUNDUĞU host'lardır.
func attrKVHHave(want string, count, hosts int) int {
	if want == "absent" {
		if count >= hosts {
			return 0
		}
		return hosts - count
	}
	return count
}

// attrKVHStepState — SAF: nesne hâllerinden adım hükmü.
func attrKVHStepState(objs []AttrKVHObjectStatus) string {
	if len(objs) == 0 {
		return "unknown"
	}
	ok, missing := 0, 0
	for _, o := range objs {
		switch o.State {
		case "unknown":
			return "unknown"
		case "ok":
			ok++
		case "missing":
			missing++
		}
	}
	switch {
	case ok == len(objs):
		return "done"
	case missing == len(objs):
		return "pending"
	}
	return "partial"
}

// AttrKVHPod — görünen bir Coremetry pod'u ve derleyici kipi ("" = eski sürüm, bildirmiyor).
type AttrKVHPod struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	Version   string `json:"version"`
	Mode      string `json:"mode"`
	IsThisPod bool   `json:"isThisPod"`
}

// attrKVHSwitchState — SAF: görünen her pod v2 ise done.
func attrKVHSwitchState(pods []AttrKVHPod) (state, detail string) {
	if len(pods) == 0 {
		return "unknown", "pod listesi okunamadı"
	}
	var lag []string
	for _, p := range pods {
		if p.Mode != "v2" {
			m := p.Mode
			if m == "" {
				m = "bildirmiyor (eski sürüm)"
			}
			lag = append(lag, p.Hostname+"="+m)
		}
	}
	if len(lag) == 0 {
		return "done", fmt.Sprintf("%d pod v2", len(pods))
	}
	if len(lag) == len(pods) {
		return "pending", strings.Join(lag, ", ")
	}
	return "partial", strings.Join(lag, ", ")
}

// AttrKVHStep — sihirbazdaki bir adım.
type AttrKVHStep struct {
	ID      string                `json:"id"`
	Title   string                `json:"title"`
	State   string                `json:"state"` // done | pending | partial | unknown | optional
	Detail  string                `json:"detail,omitempty"`
	Objects []AttrKVHObjectStatus `json:"objects"`
	SQL     []string              `json:"sql"`
}

// AttrKVHCheck — otomatik ön kontrol satırı.
type AttrKVHCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Blocking bool   `json:"blocking"`
	Detail   string `json:"detail"`
}

// AttrKVHHost — host sürümü + disk.
type AttrKVHHost struct {
	Host      string `json:"host"`
	Version   string `json:"version"`
	FreeBytes uint64 `json:"freeBytes"`
	TotalByte uint64 `json:"totalBytes"`
}

// AttrKVHPartition — spans_local partition'ı (MATERIALIZE birimi).
type AttrKVHPartition struct {
	ID           string `json:"id"`
	Partition    string `json:"partition"`
	Parts        uint64 `json:"parts"`
	Rows         uint64 `json:"rows"`
	Bytes        uint64 `json:"bytes"`
	Materialized bool   `json:"materialized"`
}

// AttrKVHJob — bu pod'da koşan / son koşu.
type AttrKVHJob struct {
	Running     bool               `json:"running"`
	Cluster     string             `json:"cluster,omitempty"`
	StartedBy   string             `json:"startedBy,omitempty"`
	StartedAt   int64              `json:"startedAt,omitempty"`
	DoneAt      int64              `json:"doneAt,omitempty"`
	Materialize bool               `json:"materialize"`
	Step        string             `json:"step,omitempty"`
	FailedStep  string             `json:"failedStep,omitempty"`
	Err         string             `json:"err,omitempty"`
	Log         []RollupStmtResult `json:"log"`
}

// AttrKVHAliasStatus — sihirbaz kartının tamamı.
type AttrKVHAliasStatus struct {
	Cluster          string             `json:"cluster"`
	Hosts            int                `json:"hosts"`
	BootManaged      bool               `json:"bootManaged"`
	SpansLocal       bool               `json:"spansLocal"`
	ThisPodMode      string             `json:"thisPodMode"`
	BloomUsed        uint64             `json:"bloomUsed"`
	Checks           []AttrKVHCheck     `json:"checks"`
	HostInfo         []AttrKVHHost      `json:"hostInfo"`
	Steps            []AttrKVHStep      `json:"steps"`
	Pods             []AttrKVHPod       `json:"pods"`
	Partitions       []AttrKVHPartition `json:"partitions"`
	PendingMutations int                `json:"pendingMutations"`
	// Pending — zorunlu dört adımdan biri bitmemiş (UI kartı yalnız o zaman gösterir).
	Pending bool `json:"pending"`
	// Blocker — koşuyu engelleyen ilk ön kontrol ("" = koşulabilir).
	Blocker   string     `json:"blocker,omitempty"`
	Job       AttrKVHJob `json:"job"`
	Generated int64      `json:"generated"`
}

func (st *AttrKVHAliasStatus) step(id string) *AttrKVHStep {
	for i := range st.Steps {
		if st.Steps[i].ID == id {
			return &st.Steps[i]
		}
	}
	return nil
}

// attrKVHPending — SAF.
func attrKVHPending(st AttrKVHAliasStatus) bool {
	if st.BootManaged {
		return false
	}
	for _, id := range attrKVHRequiredSteps {
		if p := st.step(id); p == nil || p.State != "done" {
			return true
		}
	}
	return false
}

// attrKVHBlocker — SAF: ilk engelleyici ön kontrol.
func attrKVHBlocker(st AttrKVHAliasStatus) string {
	switch {
	case st.BootManaged:
		return "spans uygulama yönetimli — boot 0017'yi kendisi uygular (attr_index.go)"
	case st.Cluster == "":
		return "küme adı bulunamadı (config cluster_name boş, spans Distributed engine'inden okunamadı)"
	case !st.SpansLocal:
		return "spans_local yok — dış Distributed kurulum bekleniyordu"
	}
	for _, c := range st.Checks {
		if c.Blocking && !c.OK {
			return c.Name + ": " + c.Detail
		}
	}
	return ""
}

// attrKVHMaterializedPartitions — SAF: bitmiş MATERIALIZE INDEX mutasyon
// komutlarından (system.mutations.command) iki v2 indeksin de yazıldığı
// partition'lar; all = tüm tablo için koşmuş.
func attrKVHMaterializedPartitions(doneCommands []string) (all bool, ids map[string]bool) {
	per := map[string]map[string]bool{}
	whole := map[string]bool{}
	for _, cmd := range doneCommands {
		c := strings.Join(strings.Fields(cmd), " ")
		for _, ic := range attrIndexV2Cols {
			head := "MATERIALIZE INDEX " + ic.idx
			i := strings.Index(c, head)
			if i < 0 {
				continue
			}
			rest := strings.TrimSpace(c[i+len(head):])
			if rest == "" || !strings.HasPrefix(rest, "IN PARTITION") {
				whole[ic.idx] = true
				continue
			}
			if j := strings.Index(rest, "ID '"); j >= 0 {
				id := rest[j+4:]
				if k := strings.Index(id, "'"); k >= 0 {
					id = id[:k]
					if per[id] == nil {
						per[id] = map[string]bool{}
					}
					per[id][ic.idx] = true
				}
			}
		}
	}
	all = true
	for _, ic := range attrIndexV2Cols {
		if !whole[ic.idx] {
			all = false
		}
	}
	ids = map[string]bool{}
	for id, set := range per {
		ok := true
		for _, ic := range attrIndexV2Cols {
			if !set[ic.idx] && !whole[ic.idx] {
				ok = false
			}
		}
		if ok {
			ids[id] = true
		}
	}
	return all, ids
}

// attrKVHDiskMinFreeRatio — MATERIALIZE için en dolu host'ta asgari boş oran.
const attrKVHDiskMinFreeRatio = 0.10

// AttrKVHAliasStatus — tam durum + otomatik ön kontroller. pods: API'nin
// heartbeat'lerden topladığı pod listesi.
func (s *Store) AttrKVHAliasStatus(ctx context.Context, pods []AttrKVHPod) AttrKVHAliasStatus {
	out := AttrKVHAliasStatus{
		Generated: time.Now().Unix(), Checks: []AttrKVHCheck{}, HostInfo: []AttrKVHHost{},
		Steps: []AttrKVHStep{}, Pods: pods, Partitions: []AttrKVHPartition{},
		ThisPodMode: AttrIndexModeName(), Job: attrKVHJobSnapshot(),
	}
	if out.Pods == nil {
		out.Pods = []AttrKVHPod{}
	}
	out.BloomUsed = AttrIndexUsed()
	out.BootManaged = !s.spansIsExternalDistributed(ctx)
	if out.BootManaged {
		// Boot yönetiyor: probe'a gerek yok, kart gizli.
		out.Blocker = attrKVHBlocker(out)
		return out
	}
	cluster := s.entityLayerCluster(ctx)
	out.Cluster = cluster
	if ok, err := s.tableExists(ctx, "spans_local"); err == nil {
		out.SpansLocal = ok
	}
	hosts := 1
	src := func(t string) string { return t }
	if cluster != "" {
		var n uint64
		if err := s.conn.QueryRow(ctx, `SELECT count() FROM system.clusters WHERE cluster = ?`, cluster).Scan(&n); err == nil && n > 0 {
			hosts = int(n)
		}
		src = func(t string) string { return fmt.Sprintf("clusterAllReplicas('%s', %s)", cluster, t) }
	}
	out.Hosts = hosts
	const qs = " SETTINGS max_execution_time = 10, skip_unavailable_shards = 1"

	// ── ön kontroller: erişilebilirlik + sürüm + disk ──
	rows, err := s.conn.Query(ctx, `SELECT hostName(), version() FROM `+src("system.one")+qs)
	versions := map[string]bool{}
	if err != nil {
		out.Checks = append(out.Checks, AttrKVHCheck{Name: "host erişimi", Blocking: true, Detail: err.Error()})
	} else {
		for rows.Next() {
			var h AttrKVHHost
			if rows.Scan(&h.Host, &h.Version) == nil {
				out.HostInfo = append(out.HostInfo, h)
				versions[h.Version] = true
			}
		}
		rows.Close()
		out.Checks = append(out.Checks, AttrKVHCheck{Name: "host erişimi", Blocking: true, OK: len(out.HostInfo) >= hosts,
			Detail: fmt.Sprintf("%d/%d host yanıt verdi (ON CLUSTER DDL erişilemeyen host'ta asılı kalır)", len(out.HostInfo), hosts)})
		vs := make([]string, 0, len(versions))
		for v := range versions {
			vs = append(vs, v)
		}
		out.Checks = append(out.Checks, AttrKVHCheck{Name: "ClickHouse sürümü", OK: len(vs) == 1,
			Detail: strings.Join(vs, ", ") + " (tek sürüm beklenir; ALIAS + bloom davranışı 26.2'de ölçüldü)"})
	}
	if drows, err := s.conn.Query(ctx, `SELECT hostName(), sum(free_space), sum(total_space) FROM `+src("system.disks")+` GROUP BY hostName()`+qs); err == nil {
		minRatio, minHost := 1.0, ""
		for drows.Next() {
			var h string
			var free, total uint64
			if drows.Scan(&h, &free, &total) == nil {
				for i := range out.HostInfo {
					if out.HostInfo[i].Host == h {
						out.HostInfo[i].FreeBytes, out.HostInfo[i].TotalByte = free, total
					}
				}
				if total > 0 && float64(free)/float64(total) < minRatio {
					minRatio, minHost = float64(free)/float64(total), h
				}
			}
		}
		drows.Close()
		out.Checks = append(out.Checks, AttrKVHCheck{Name: "disk boşluğu", OK: minRatio >= attrKVHDiskMinFreeRatio,
			Detail: fmt.Sprintf("en dolu host %s: %%%.0f boş (MATERIALIZE için ≥ %%%.0f; 1-4 adımları metadata, disk boşaltır)", minHost, minRatio*100, attrKVHDiskMinFreeRatio*100)})
	}
	var spansEngine string
	_ = s.conn.QueryRow(ctx, `SELECT engine FROM system.tables WHERE database = currentDatabase() AND name = 'spans' LIMIT 1`).Scan(&spansEngine)
	out.Checks = append(out.Checks, AttrKVHCheck{Name: "tablolar", Blocking: true, OK: out.SpansLocal && spansEngine == "Distributed",
		Detail: fmt.Sprintf("spans engine=%s, spans_local=%v", spansEngine, out.SpansLocal)})

	// ── adımlar ──
	count := func(sql string, args ...any) (int, error) {
		var n uint64
		if err := s.conn.QueryRow(ctx, sql+qs, args...).Scan(&n); err != nil {
			return 0, err
		}
		return int(n), nil
	}
	probe := func(o AttrKVHObject) AttrKVHObjectStatus {
		st := AttrKVHObjectStatus{AttrKVHObject: o, Hosts: hosts}
		var n int
		var err error
		switch {
		case o.Kind == "index":
			n, err = count(`SELECT count() FROM `+src("system.data_skipping_indices")+` WHERE database = currentDatabase() AND table = ? AND name = ?`, o.Table, o.Name)
		case o.Want == "alias":
			n, err = count(`SELECT count() FROM `+src("system.columns")+` WHERE database = currentDatabase() AND table = ? AND name = ? AND default_kind = 'ALIAS'`, o.Table, o.Name)
		default:
			n, err = count(`SELECT count() FROM `+src("system.columns")+` WHERE database = currentDatabase() AND table = ? AND name = ?`, o.Table, o.Name)
		}
		if err != nil {
			st.State, st.Err = "unknown", err.Error()
			return st
		}
		st.HaveHosts = attrKVHHave(o.Want, n, hosts)
		st.State = entityLayerObjectState(st.HaveHosts, hosts)
		return st
	}
	for _, id := range []string{AttrKVHStepColumns, AttrKVHStepIndexes, AttrKVHStepSwitch, AttrKVHStepLegacy, AttrKVHStepMaterialize} {
		step := AttrKVHStep{ID: id, Title: attrKVHStepTitles[id], Objects: []AttrKVHObjectStatus{}, SQL: attrKVHStepSQL(id, cluster, "")}
		if step.SQL == nil {
			step.SQL = []string{}
		}
		switch id {
		case AttrKVHStepSwitch:
			step.State, step.Detail = attrKVHSwitchState(out.Pods)
		case AttrKVHStepMaterialize:
			step.State = "optional"
		default:
			for _, o := range attrKVHStepObjects(id, cluster) {
				step.Objects = append(step.Objects, probe(o))
			}
			step.State = attrKVHStepState(step.Objects)
		}
		out.Steps = append(out.Steps, step)
	}

	// ── partition'lar + mutasyonlar ──
	local, _ := attrKVHTables(cluster)
	var doneCmds []string
	if mrows, err := s.conn.Query(ctx, `SELECT command, min(is_done) FROM `+src("system.mutations")+`
		WHERE database = currentDatabase() AND table = ? AND (command LIKE '%kvh%')
		GROUP BY command LIMIT 500`+qs, local); err == nil {
		for mrows.Next() {
			var cmd string
			var done uint8
			if mrows.Scan(&cmd, &done) == nil {
				if done == 1 {
					doneCmds = append(doneCmds, cmd)
				} else {
					out.PendingMutations++
				}
			}
		}
		mrows.Close()
	}
	all, matIDs := attrKVHMaterializedPartitions(doneCmds)
	if prows, err := s.conn.Query(ctx, `SELECT partition_id, any(partition), count(), sum(rows), sum(bytes_on_disk) FROM `+src("system.parts")+`
		WHERE database = currentDatabase() AND table = ? AND active
		GROUP BY partition_id ORDER BY partition_id LIMIT 400`+qs, local); err == nil {
		for prows.Next() {
			var p AttrKVHPartition
			if prows.Scan(&p.ID, &p.Partition, &p.Parts, &p.Rows, &p.Bytes) == nil {
				p.Materialized = all || matIDs[p.ID]
				out.Partitions = append(out.Partitions, p)
			}
		}
		prows.Close()
	}
	if m := out.step(AttrKVHStepMaterialize); m != nil {
		left := 0
		for _, p := range out.Partitions {
			if !p.Materialized {
				left++
			}
		}
		m.Detail = fmt.Sprintf("%d/%d partition bekliyor · %d bekleyen kvh mutasyonu", left, len(out.Partitions), out.PendingMutations)
	}
	out.Pending = attrKVHPending(out)
	out.Blocker = attrKVHBlocker(out)
	return out
}

// ── koşu (arka plan; pod başına tek) ─────────────────────────────────────

var attrKVHJobState struct {
	mu  sync.Mutex
	job AttrKVHJob
}

func attrKVHJobSnapshot() AttrKVHJob {
	attrKVHJobState.mu.Lock()
	defer attrKVHJobState.mu.Unlock()
	j := attrKVHJobState.job
	j.Log = append([]RollupStmtResult{}, j.Log...)
	return j
}

func attrKVHJobUpdate(f func(j *AttrKVHJob)) {
	attrKVHJobState.mu.Lock()
	f(&attrKVHJobState.job)
	attrKVHJobState.mu.Unlock()
}

// AttrKVHRunOptions — koşu girdileri.
type AttrKVHRunOptions struct {
	StartedBy   string
	Materialize bool
	// Pods — her yeniden probe'da görünen pod'lar (API heartbeat'lerden).
	Pods func(ctx context.Context) []AttrKVHPod
	// SwitchWait / Poll — switch adımında pod'ların v2'ye geçişini bekleme.
	SwitchWait, Poll time.Duration
	// Done — koşu bitince (audit için).
	Done func(job AttrKVHJob)
}

// AttrKVHAliasStart — ön kontrol geçerse koşuyu arka planda başlatır.
func (s *Store) AttrKVHAliasStart(ctx context.Context, opt AttrKVHRunOptions) (AttrKVHAliasStatus, error) {
	st := s.AttrKVHAliasStatus(ctx, opt.Pods(ctx))
	if st.Blocker != "" {
		return st, fmt.Errorf("ön kontrol geçmedi — %s", st.Blocker)
	}
	if !st.Pending && !opt.Materialize {
		return st, fmt.Errorf("yapılacak iş yok — 0017 her host'ta tamam")
	}
	attrKVHJobState.mu.Lock()
	if attrKVHJobState.job.Running {
		attrKVHJobState.mu.Unlock()
		return st, fmt.Errorf("bu pod'da bir 0017 koşusu zaten sürüyor")
	}
	attrKVHJobState.job = AttrKVHJob{Running: true, Cluster: st.Cluster, StartedBy: opt.StartedBy, StartedAt: time.Now().UnixMilli(), Materialize: opt.Materialize, Log: []RollupStmtResult{}}
	attrKVHJobState.mu.Unlock()
	if opt.SwitchWait <= 0 {
		opt.SwitchWait = 3 * time.Minute
	}
	if opt.Poll <= 0 {
		opt.Poll = 5 * time.Second
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				attrKVHJobUpdate(func(j *AttrKVHJob) { j.Err = fmt.Sprintf("panik: %v", r) })
			}
			attrKVHJobUpdate(func(j *AttrKVHJob) { j.Running = false; j.Step = ""; j.DoneAt = time.Now().UnixMilli() })
			if opt.Done != nil {
				opt.Done(attrKVHJobSnapshot())
			}
		}()
		s.attrKVHRun(context.Background(), opt)
	}()
	st.Job = attrKVHJobSnapshot()
	return st, nil
}

func (s *Store) attrKVHFail(step, msg string) {
	attrKVHJobUpdate(func(j *AttrKVHJob) { j.FailedStep, j.Err = step, msg })
	log.Printf("[chstore] 0017 sihirbazı %s adımında durdu: %s", step, msg)
}

func (s *Store) attrKVHExec(ctx context.Context, step string, stmts []string) bool {
	for _, q := range stmts {
		r := RollupStmtResult{Head: stmtHead(q)}
		qctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := s.conn.Exec(qctx, q)
		cancel()
		if err != nil {
			r.Err = err.Error()
		} else {
			r.OK = true
		}
		attrKVHJobUpdate(func(j *AttrKVHJob) { j.Log = append(j.Log, r) })
		if err != nil {
			s.attrKVHFail(step, r.Head+": "+r.Err)
			return false
		}
	}
	return true
}

func (s *Store) attrKVHReprobe(ctx context.Context, opt AttrKVHRunOptions) AttrKVHAliasStatus {
	pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return s.AttrKVHAliasStatus(pctx, opt.Pods(pctx))
}

func (s *Store) attrKVHRun(ctx context.Context, opt AttrKVHRunOptions) {
	note := func(head string) {
		attrKVHJobUpdate(func(j *AttrKVHJob) { j.Log = append(j.Log, RollupStmtResult{Head: head, OK: true}) })
	}
	for _, id := range attrKVHRequiredSteps {
		attrKVHJobUpdate(func(j *AttrKVHJob) { j.Step = id })
		st := s.attrKVHReprobe(ctx, opt)
		if st.Blocker != "" {
			s.attrKVHFail(id, "ön kontrol: "+st.Blocker)
			return
		}
		step := st.step(id)
		if step != nil && step.State == "done" {
			note(id + ": zaten tamam — atlandı")
			continue
		}
		if step != nil && step.State == "unknown" && id != AttrKVHStepSwitch {
			s.attrKVHFail(id, "durum okunamadı (host'lardan biri yanıt vermiyor?) — körlemesine DDL gönderilmedi")
			return
		}
		if id == AttrKVHStepSwitch {
			// Bu pod hemen; diğerleri attrIndexReprobeLoop ile (≤ 30 s) +
			// heartbeat (10 s). Görünen her pod v2 diyene kadar bekle.
			s.ReprobeAttrIndexNow(ctx)
			deadline := time.Now().Add(opt.SwitchWait)
			for {
				pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				state, detail := attrKVHSwitchState(opt.Pods(pctx))
				cancel()
				if state == "done" {
					note("switch: " + detail)
					break
				}
				if time.Now().After(deadline) {
					s.attrKVHFail(id, "pod'lar v2'ye geçmedi ("+detail+") — eski kolonlara dokunulmadı; pod'lar yeni sürümde mi? Tekrar koşmak güvenli")
					return
				}
				time.Sleep(opt.Poll)
			}
			continue
		}
		if !s.attrKVHExec(ctx, id, attrKVHStepSQL(id, st.Cluster, "")) {
			return
		}
		// Yeniden probe: ifadeler tuttu ama ON CLUSTER kuyruğu her host'ta inmiş mi?
		if again := s.attrKVHReprobe(ctx, opt); again.step(id) == nil || again.step(id).State != "done" {
			state := "unknown"
			if again.step(id) != nil {
				state = again.step(id).State
			}
			s.attrKVHFail(id, "ifadeler gönderildi ama adım her host'ta görünmüyor (durum="+state+"; DDL kuyruğu?) — tekrar koşmak güvenli")
			return
		}
	}
	if !opt.Materialize {
		return
	}
	attrKVHJobUpdate(func(j *AttrKVHJob) { j.Step = AttrKVHStepMaterialize })
	st := s.attrKVHReprobe(ctx, opt)
	for _, c := range st.Checks {
		if c.Name == "disk boşluğu" && !c.OK {
			s.attrKVHFail(AttrKVHStepMaterialize, "disk boşluğu yetersiz: "+c.Detail)
			return
		}
	}
	local, _ := attrKVHTables(st.Cluster)
	for _, p := range st.Partitions {
		if p.Materialized {
			continue
		}
		if !attrKVHPartitionIDRe.MatchString(p.ID) {
			s.attrKVHFail(AttrKVHStepMaterialize, "beklenmeyen partition_id "+p.ID)
			return
		}
		if !s.attrKVHExec(ctx, AttrKVHStepMaterialize, attrKVHStepSQL(AttrKVHStepMaterialize, st.Cluster, p.ID)) {
			return
		}
		// Partition partition: bu partition'ın mutasyonları bitmeden sonrakine geçme.
		if err := s.attrKVHWaitMutations(ctx, st.Cluster, local, p.ID, opt.Poll); err != nil {
			s.attrKVHFail(AttrKVHStepMaterialize, "partition "+p.ID+": "+err.Error())
			return
		}
		note("materialize: partition " + p.ID + " bitti")
	}
}

// attrKVHWaitMutations — partition'ın MATERIALIZE mutasyonları her host'ta
// bitene kadar bekler (en çok 6 saat); hatalı mutasyon = hata.
func (s *Store) attrKVHWaitMutations(ctx context.Context, cluster, table, partitionID string, poll time.Duration) error {
	src := "system.mutations"
	if cluster != "" {
		src = fmt.Sprintf("clusterAllReplicas('%s', system.mutations)", cluster)
	}
	deadline := time.Now().Add(6 * time.Hour)
	like := "%MATERIALIZE INDEX%" + partitionID + "%"
	for {
		var pending uint64
		var failReason string
		qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.conn.QueryRow(qctx, `SELECT countIf(NOT is_done), anyIf(latest_fail_reason, latest_fail_reason != '' AND NOT is_done)
			FROM `+src+` WHERE database = currentDatabase() AND table = ? AND command LIKE ?
			SETTINGS max_execution_time = 10, skip_unavailable_shards = 1`, table, like).Scan(&pending, &failReason)
		cancel()
		if err == nil && pending == 0 {
			return nil
		}
		if failReason != "" {
			return fmt.Errorf("mutasyon hatası: %s", failReason)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("6 saatte bitmedi (system.mutations izleyin)")
		}
		time.Sleep(poll)
	}
}

// ── derleyici kipinin kendiliğinden yeniden probe'u (pod restart yok) ─────

// ReprobeAttrIndexNow — bu pod'un kipini hemen tazeler.
func (s *Store) ReprobeAttrIndexNow(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if m := s.probeAttrIndex(pctx); m != attrIndexMode.Load() && m >= attrIndexMode.Load() {
		registerAttrIndex(m)
	}
}

// attrIndexReprobeInterval — v2 değilken kip yoklama aralığı (system.columns
// tek satır; ucuz). Sihirbaz switch adımı bunun üstüne heartbeat'i bekler.
const attrIndexReprobeInterval = 30 * time.Second

// attrIndexReprobeLoop — boot sonrası, kip v2 olana dek periyodik probe.
// Yalnız YUKARI geçer (none → legacy → v2): kolonlar elle düşürülürse eski
// davranış (restart) geçerli — yanlış alarmla yola düşmek yerine.
func (s *Store) attrIndexReprobeLoop() {
	t := time.NewTicker(attrIndexReprobeInterval)
	defer t.Stop()
	for range t.C {
		if attrIndexMode.Load() == attrIndexV2 {
			return
		}
		s.ReprobeAttrIndexNow(context.Background())
	}
}
