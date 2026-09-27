package chstore

// state_path_check.go — v0.10.965 — State tablolarının ZK yolu denetimi
// (Admin → ClickHouse → "Replika tutarlılığı" kartı; operatör kararı
// 2026-09-27: "sihirbaza ekle … düzeltmesi ve kontrolü").
//
// PROD BULGUSU (v0.10.960, 4 host / 2 shard): on state tablosu ESKİ (shard'lı)
// ZK yolunda — '/clickhouse/tables/01/<ad>' shard-1 host'larında,
// '/clickhouse/tables/02/<ad>' shard-2 host'larında. Her biri İKİ ayrı
// replikasyon grubu: uygulama bağlandığı host'un yarısını görür. Kök neden
// state_replication.go useUnifiedStatePath KURAL 3: kümede HERHANGİ bir state
// tablosu eski yoldayken boot, hiç var olmayan YENİ state tablolarını da eski
// yola kurar. ingest_ledger (v0.10.767) eski yolda doğdu ve sonraki her
// tabloyu kilitledi. Onu tutan tablolar birleşik yola geçince kural 4 devreye
// girer ve kilit kendiliğinden açılır.
//
// Bu dosya YENİ OKUMA YAPMAZ: raporun zaten okuduğu system.replicas +
// system.parts + system.tables satırlarından SAF bir tablo düzeyi hüküm
// çıkarır. Shard başına kararlar DEĞİŞMEZ — her yarı kendi shard'ında
// gerçekten tutarlıdır; kusur shard'lar ARASINDADIR, o yüzden tablo düzeyi
// bayrak. Kilit hesabı boot'un kendi kuralıdır (useUnifiedStatePath) ve
// gözlem boot'un kendi birleştirmesiyle kurulur (mergeObservedStatePath):
// kart ile boot ayrışamaz.

import (
	"sort"
	"strings"
)

// StatePathGroup — v0.10.965 — bir tablonun bir ZK yolundaki host'ları.
// Hosts UI'da sayı olarak görünür, adlar yalnız title'da.
type StatePathGroup struct {
	Path    string   `json:"path"`
	Hosts   []string `json:"hosts"`
	Rows    uint64   `json:"rows"` // gruptaki en dolu host'un aktif satırı
	Unified bool     `json:"unified"`
}

// StatePathTable — v0.10.965 — birleşik yolda OLMAYAN bir state tablosu.
type StatePathTable struct {
	Table       string           `json:"table"`
	Kind        string           `json:"kind"` // "legacy" | "mixed" | "absent"
	Groups      []StatePathGroup `json:"groups"`
	Rows        uint64           `json:"rows"`        // gruplar toplamı
	Rebuildable bool             `json:"rebuildable"` // sihirbazın izin listesinde
	Class       string           `json:"class,omitempty"`
	ClassNote   string           `json:"classNote,omitempty"`
}

// StatePathCheck — v0.10.965 — kartın "State tablolarının ZK yolu" bloğu.
type StatePathCheck struct {
	ZKPrefix    string           `json:"zkPrefix"`
	LockOpen    bool             `json:"lockOpen"`
	LockReason  string           `json:"lockReason"`  // useUnifiedStatePath'in metni
	Complete    bool             `json:"complete"`    // küme tanımındaki her host cevap verdi
	Unreachable int              `json:"unreachable"` // tanımdaki host − cevap veren host
	Unified     int              `json:"unified"`     // her yerde birleşik yoldaki state tabloları
	Legacy      []StatePathTable `json:"legacy"`
}

// Tablo düzeyi ZK yolu türleri (StatePathTable.Kind / ReplicaTable.StatePath).
const (
	statePathLegacy = "legacy"
	statePathMixed  = "mixed"
	statePathAbsent = "absent"
)

// legacyStatePath — v0.10.965 — SAF: yol eski (shard'lı) biçimde mi:
// `<önek>/<segment>/<ad>`, TEK segment ve segment "state" DEĞİL. İç içe yol,
// başka önek ya da birleşik yol false.
func legacyStatePath(zkPrefix, name, path string) bool {
	pre := zkPrefix + "/"
	if !strings.HasPrefix(path, pre) {
		return false
	}
	rest := path[len(pre):]
	i := strings.Index(rest, "/")
	if i <= 0 {
		return false
	}
	seg, tail := rest[:i], rest[i+1:]
	return seg != stateReplicaDir && tail == name
}

// statePathKind — v0.10.965 — SAF: tablonun host satırlarından tablo düzeyi
// tür. "" = satır yok ya da hepsi birleşik yolda.
func statePathKind(rs []ReplicaState, zkPrefix, table string) string {
	want := unifiedStatePath(zkPrefix, table)
	uni, other := false, false
	for _, r := range rs {
		if r.ZKPath == want {
			uni = true
		} else {
			other = true
		}
	}
	switch {
	case !other:
		return ""
	case uni:
		return statePathMixed
	default:
		return statePathLegacy
	}
}

// statePathGroups — v0.10.965 — SAF: host satırlarını ZK yoluna göre
// gruplar (yol sırasıyla); grup satırı en dolu host'unki.
func statePathGroups(rs []ReplicaState, zkPrefix, table string) ([]StatePathGroup, uint64) {
	byPath := map[string]*StatePathGroup{}
	for _, r := range rs {
		g := byPath[r.ZKPath]
		if g == nil {
			g = &StatePathGroup{Path: r.ZKPath, Hosts: []string{}, Unified: r.ZKPath == unifiedStatePath(zkPrefix, table)}
			byPath[r.ZKPath] = g
		}
		g.Hosts = append(g.Hosts, r.Host)
		if r.TotalRows > g.Rows {
			g.Rows = r.TotalRows
		}
	}
	out := make([]StatePathGroup, 0, len(byPath))
	var sum uint64
	for _, g := range byPath {
		sort.Strings(g.Hosts)
		sum += g.Rows
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, sum
}

// statePathCheckFor — v0.10.965 — SAF: raporun okumalarından ZK yolu hükmü.
//
//   - byTable: tablo → host satırları (system.replicas + system.parts toplamı)
//   - engineOf: tablo → host → motor (system.tables; yoksa tablo o host'ta yok)
//   - reachable: cevap veren host'lar; rosterSize: küme tanımındaki host sayısı
//
// Yalnız stateProbeTable(t) tablolar sayılır — boot'un süzgeci: `.inner*`,
// `_local`, `_old`, `_fix` … elenir. Kilit boot'un kuralıyla hesaplanır:
// gözlem mergeObservedStatePath ile katlanır, sonra useUnifiedStatePath(obs,
// önek, ""). "" asla anahtar olamaz (stateProbeTable("") false) → kural 3/4.
func statePathCheckFor(byTable map[string][]ReplicaState, engineOf map[string]map[string]string,
	reachable []string, rosterSize int, zkPrefix string) *StatePathCheck {
	out := &StatePathCheck{
		ZKPrefix:    zkPrefix,
		Complete:    len(reachable) >= rosterSize,
		Unreachable: max(0, rosterSize-len(reachable)),
		Legacy:      []StatePathTable{},
	}
	paths := map[string]string{}
	names := make([]string, 0, len(byTable))
	for t := range byTable {
		names = append(names, t)
	}
	sort.Strings(names)
	listed := map[string]bool{}
	for _, t := range names {
		if !stateProbeTable(t) {
			continue
		}
		rs := byTable[t]
		for _, r := range rs {
			mergeObservedStatePath(paths, t, r.ZKPath, zkPrefix)
		}
		kind := statePathKind(rs, zkPrefix, t)
		if kind == "" {
			if len(rs) > 0 && !statePathMissingSomewhere(t, engineOf, reachable) {
				out.Unified++
			}
			continue
		}
		out.Legacy = append(out.Legacy, statePathTableFor(t, kind, rs, zkPrefix))
		listed[t] = true
	}
	// v0.10.965 — izin listesindeki bir ad erişilebilir bir host'ta YOKSA ve
	// eski grubu da yoksa "absent": yarım kalmış bir çalıştırmanın düşürdüğü
	// tablolar system.replicas'ta artık görünmez, kilit hesabına da girmez —
	// ama sihirbaz onları kurmalıdır.
	for _, e := range statePathAllowlist {
		if listed[e.Name] || !statePathMissingSomewhere(e.Name, engineOf, reachable) {
			continue
		}
		out.Legacy = append(out.Legacy, statePathTableFor(e.Name, statePathAbsent, byTable[e.Name], zkPrefix))
		listed[e.Name] = true
	}
	sort.SliceStable(out.Legacy, func(i, j int) bool {
		ai, bi := statePathAllowIndex(out.Legacy[i].Table), statePathAllowIndex(out.Legacy[j].Table)
		if ai != bi {
			return ai < bi
		}
		return out.Legacy[i].Table < out.Legacy[j].Table
	})
	out.LockOpen, out.LockReason = useUnifiedStatePath(stateObservation{ok: true, paths: paths}, zkPrefix, "")
	return out
}

// statePathMissingSomewhere — v0.10.965 — SAF: tablo erişilebilir host'lardan
// en az birinde system.tables satırı vermedi mi? Erişilebilir host yoksa false.
func statePathMissingSomewhere(table string, engineOf map[string]map[string]string, reachable []string) bool {
	for _, h := range reachable {
		if engineOf[table][h] == "" {
			return true
		}
	}
	return false
}

// statePathTableFor — v0.10.965 — SAF: listelenecek satır + izin listesi sınıfı.
func statePathTableFor(t, kind string, rs []ReplicaState, zkPrefix string) StatePathTable {
	groups, sum := statePathGroups(rs, zkPrefix, t)
	row := StatePathTable{Table: t, Kind: kind, Groups: groups, Rows: sum}
	if e, ok := statePathEntryFor(t); ok {
		row.Rebuildable, row.Class, row.ClassNote = true, e.Class, e.Note
	}
	return row
}
