package chstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// replica_consistency.go — Admin "Replika tutarlılığı" kartının veri yarısı
// (v0.10.791; spec onayı 2026-09-19).
//
// Operatör, test ortamında aynı sorgunun her yenilemede farklı sayı verdiğini
// ve dünkü bir trace'in MV'de var ham'da yok çıktığını gördü. İkisi de aynı
// parmak izi: load_balancing=random her Distributed sorguda shard başına
// başka bir replika seçer; aynı shard'ın iki replikası aynı veriyi
// taşımıyorsa sonuç okumadan okumaya değişir. system.replicas'ta gecikme 0
// görmek bunu DIŞLAMAZ — iki replika birbirini hiç replike etmiyor olabilir
// (`{shard}` makrosu her host'ta farklı → her host kendi ZooKeeper yolunda
// tek başına) ya da bir replika parça kaybetmiş olabilir.
//
// Bu dosya küme genelinde (clusterAllReplicas) system.replicas + system.parts
// + system.macros okur, host'ları system.clusters ile shard'a eşler ve her
// (tablo × shard) için saf bir karar verir (replicaVerdict). Yalnız okur;
// eylemler (SYNC / RESTORE) ayrı dilim. system.* okumaları NODE-LOKAL
// tablolardır: dangling_mv_admin ile aynı gerekçeyle ana bağlantıda (s.conn),
// RoundRobin havuzunda değil.

// ReplicaHost — system.clusters'taki host ve makroları.
type ReplicaHost struct {
	Host    string            `json:"host"`
	Shard   int               `json:"shard"`
	Replica int               `json:"replica"`
	Macros  map[string]string `json:"macros,omitempty"`
}

// ReplicaState — bir Replicated tablonun bir host'taki durumu.
type ReplicaState struct {
	Host           string            `json:"host"`
	Shard          int               `json:"shard"`
	ReplicaNum     int               `json:"replica"`
	ZKPath         string            `json:"zkPath"`
	ReplicaName    string            `json:"replicaName"`
	TotalReplicas  int               `json:"totalReplicas"`
	ActiveReplicas int               `json:"activeReplicas"`
	ReadOnly       bool              `json:"readonly"`
	SessionExpired bool              `json:"sessionExpired"`
	DelayS         uint64            `json:"delayS"`
	Queue          uint32            `json:"queue"`
	LastException  string            `json:"lastException,omitempty"`
	Rows           map[string]uint64 `json:"rows"` // partition → aktif parçalardaki satır
	TotalRows      uint64            `json:"totalRows"`
}

// ReplicaShard — bir tablonun bir shard'ı: replikalar + karar.
type ReplicaShard struct {
	Shard              int            `json:"shard"`
	Replicas           []ReplicaState `json:"replicas"`
	Verdict            string         `json:"verdict"`
	Hint               string         `json:"hint"`
	DivergentPartition string         `json:"divergentPartition,omitempty"`
	DivergencePct      float64        `json:"divergencePct,omitempty"`
}

// ReplicaTable — bir Replicated tablo, shard'ları ve en kötü karar.
type ReplicaTable struct {
	Table   string         `json:"table"`
	Shards  []ReplicaShard `json:"shards"`
	Verdict string         `json:"verdict"`
}

// ReplicaConsistencyReport — kartın tamamı.
type ReplicaConsistencyReport struct {
	Cluster       string         `json:"cluster"`
	Database      string         `json:"database"`
	LoadBalancing string         `json:"loadBalancing"`
	Hosts         []ReplicaHost  `json:"hosts"`
	Tables        []ReplicaTable `json:"tables"`
	GeneratedAt   int64          `json:"generatedAt"`
	Notes         []string       `json:"notes,omitempty"`
}

// Kararlar — FE rozet/metin bunlara göre (adminch/replicaConsistency.ts).
const (
	ReplicaOK             = "ok"
	ReplicaSingle         = "single"          // shard'da tek replika: yedeklilik yok
	ReplicaLagging        = "lagging"         // gecikme/kuyruk eşiği aşıldı
	ReplicaDivergent      = "divergent"       // aynı partition'da farklı satır
	ReplicaReadOnly       = "readonly"        // readonly replika
	ReplicaSessionExpired = "session_expired" // Keeper oturumu düşmüş
	ReplicaNoReplication  = "no_replication"  // farklı ZK yolu / eksik kayıt
	ReplicaUnmapped       = "unmapped"        // host system.clusters'ta yok
)

// Eşikler — replicaVerdict'in ölçüleri; testte pinli.
const (
	replicaLagWarnS       = 60   // sn; okuma eşiği (v0.10.790) ile aynı
	replicaQueueWarn      = 1000 // replikasyon kuyruğu
	replicaDivergePct     = 2.0  // partition satır farkı yüzdesi
	replicaDivergeMinRows = 5000 // yüzdenin anlamlı olduğu taban
)

var replicaVerdictRank = map[string]int{
	ReplicaOK: 0, ReplicaSingle: 1, ReplicaUnmapped: 2, ReplicaLagging: 3,
	ReplicaDivergent: 4, ReplicaReadOnly: 5, ReplicaSessionExpired: 6, ReplicaNoReplication: 7,
}

// worstVerdict — sıralı en kötü.
func worstVerdict(vs []string) string {
	worst := ReplicaOK
	for _, v := range vs {
		if replicaVerdictRank[v] > replicaVerdictRank[worst] {
			worst = v
		}
	}
	return worst
}

// replicaVerdict — SAF: bir shard'ın replikalarından karar + ipucu.
// Sıra: yapısal (yol/kayıt) > oturum > readonly > gecikme > ıraksama > tek.
// Gecikme ıraksamadan ÖNCE: geciken replikanın satırı doğal olarak eksiktir,
// bu geçicidir; "ıraksama" yalnız kuyruk boşken anlamlıdır.
func replicaVerdict(rs []ReplicaState) (verdict, hint string, divPart string, divPct float64) {
	if len(rs) == 0 {
		return ReplicaOK, "", "", 0
	}
	if len(rs) > 1 {
		paths := map[string][]string{}
		for _, r := range rs {
			paths[r.ZKPath] = append(paths[r.ZKPath], r.Host)
		}
		if len(paths) > 1 {
			parts := make([]string, 0, len(paths))
			for p, hs := range paths {
				parts = append(parts, fmt.Sprintf("%s → %s", strings.Join(hs, ","), p))
			}
			sort.Strings(parts)
			return ReplicaNoReplication,
				fmt.Sprintf("Aynı shard'ın replikaları FARKLI ZooKeeper yolunda (%d yol): birbirini replike etmiyorlar; her host yalnız kendine düşen ekleri tutar, rastgele replika seçimi her sorguda başka veri gösterir. Makro ({shard}/{replica}) ve remote_servers yapılandırması — runbook. %s",
					len(paths), strings.Join(parts, " · ")), "", 0
		}
		for _, r := range rs {
			if r.TotalReplicas > 0 && r.TotalReplicas < len(rs) {
				return ReplicaNoReplication,
					fmt.Sprintf("ZK yolu aynı ama %s'nin gördüğü kayıtlı replika sayısı (%d) shard'daki host sayısından (%d) az: bir host yola kayıtlı değil (SYSTEM RESTORE REPLICA / tabloyu o host'ta yeniden ATTACH).",
						r.Host, r.TotalReplicas, len(rs)), "", 0
			}
		}
	}
	for _, r := range rs {
		if r.SessionExpired {
			return ReplicaSessionExpired,
				fmt.Sprintf("%s ZooKeeper/Keeper oturumu düşmüş: replika ne yazıyor ne alıyor. Keeper bağlantısı düzelince oturum kendiliğinden yenilenir; düzelmiyorsa Keeper'a bak.", r.Host), "", 0
		}
	}
	for _, r := range rs {
		if r.ReadOnly {
			return ReplicaReadOnly,
				fmt.Sprintf("%s readonly: Keeper meta verisi kayıp ya da oturum sorunlu (%s). Oturum sağlıklıysa SYSTEM RESTORE REPLICA (eylem dilimi).", r.Host, firstNonEmpty(r.LastException, "istisna yok")), "", 0
		}
	}
	for _, r := range rs {
		if r.DelayS > replicaLagWarnS || r.Queue > replicaQueueWarn {
			return ReplicaLagging,
				fmt.Sprintf("%s geride: gecikme %d sn, kuyruk %d. Yetişene kadar bu replikadan okuyan sorgular eksik görür; okuma eşiği (%d sn) aşıldıysa Distributed sorgular onu atlar. Kuyruk erimiyorsa last_queue_update_exception'a bak.", r.Host, r.DelayS, r.Queue, replicaLagWarnS), "", 0
		}
	}
	if len(rs) == 1 {
		return ReplicaSingle, "Shard'da tek replika: yedeklilik yok, ıraksama ölçülemez.", "", 0
	}
	// Iraksama: her partition için replika satırları (yoksa 0).
	parts := map[string]bool{}
	for _, r := range rs {
		for p := range r.Rows {
			parts[p] = true
		}
	}
	worstP, worstPct := "", 0.0
	var worstMax, worstMin uint64
	var worstMaxHost, worstMinHost string
	for p := range parts {
		var mx, mn uint64
		var mxH, mnH string
		first := true
		for _, r := range rs {
			n := r.Rows[p]
			if first || n > mx {
				mx, mxH = n, r.Host
			}
			if first || n < mn {
				mn, mnH = n, r.Host
			}
			first = false
		}
		if mx < replicaDivergeMinRows {
			continue
		}
		pct := float64(mx-mn) / float64(mx) * 100
		if pct > worstPct {
			worstP, worstPct, worstMax, worstMin, worstMaxHost, worstMinHost = p, pct, mx, mn, mxH, mnH
		}
	}
	if worstP != "" && worstPct > replicaDivergePct {
		return ReplicaDivergent,
			fmt.Sprintf("Replikalar aynı partition'da farklı satır tutuyor (%s: %%%.1f — %s %d / %s %d), kuyruk boş. Eksik parçalar: o host'ta SYSTEM SYNC REPLICA; yetmezse detached parçaları ATTACH (eylem dilimi).",
				worstP, worstPct, worstMaxHost, worstMax, worstMinHost, worstMin), worstP, worstPct
	}
	return ReplicaOK, "", "", 0
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// ReplicaConsistency — küme geneli rapor. Tek düğümde Cluster boş döner.
func (s *Store) ReplicaConsistency(ctx context.Context) (*ReplicaConsistencyReport, error) {
	out := &ReplicaConsistencyReport{GeneratedAt: time.Now().UnixNano()}
	if !s.clusterMode() {
		return out, nil
	}
	cluster := strings.TrimSpace(s.cfg.ClusterName)
	out.Cluster = cluster
	if err := s.conn.QueryRow(ctx, "SELECT currentDatabase()").Scan(&out.Database); err != nil {
		return nil, fmt.Errorf("currentDatabase: %w", err)
	}
	_ = s.conn.QueryRow(ctx, "SELECT value FROM system.settings WHERE name = 'load_balancing'").Scan(&out.LoadBalancing)

	hostRows, err := s.clusterHostRowsFor(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("system.clusters: %w", err)
	}
	shardOf := map[string]clusterHostRow{}
	for _, h := range hostRows {
		shardOf[h.Host] = h
		out.Hosts = append(out.Hosts, ReplicaHost{Host: h.Host, Shard: h.Shard, Replica: h.Replica, Macros: map[string]string{}})
	}
	hostIdx := map[string]int{}
	for i, h := range out.Hosts {
		hostIdx[h.Host] = i
	}

	// Makrolar — {shard}/{replica} yapılandırması; yol uyuşmazlığının kökü.
	mrows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT hostName(), macro, substitution
		FROM clusterAllReplicas('%s', system.macros)
		SETTINGS max_execution_time = 10`, cluster))
	if err != nil {
		return nil, fmt.Errorf("system.macros: %w", err)
	}
	for mrows.Next() {
		var host, macro, sub string
		if err := mrows.Scan(&host, &macro, &sub); err != nil {
			mrows.Close()
			return nil, err
		}
		if i, ok := hostIdx[host]; ok {
			out.Hosts[i].Macros[macro] = sub
		} else {
			out.Notes = append(out.Notes, fmt.Sprintf("%s system.clusters eşlemesinde yok (makro %s=%s)", host, macro, sub))
		}
	}
	mrows.Close()

	// Replikalar — bu veritabanının TÜM Replicated tabloları.
	rrows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT hostName(), table, zookeeper_path, replica_name,
		       toUInt32(total_replicas), toUInt32(active_replicas),
		       toUInt8(is_readonly), toUInt8(is_session_expired),
		       toUInt64(absolute_delay), toUInt32(queue_size),
		       substring(last_queue_update_exception, 1, 300)
		FROM clusterAllReplicas('%s', system.replicas)
		WHERE database = ?
		ORDER BY table, hostName()
		SETTINGS max_execution_time = 15`, cluster), out.Database)
	if err != nil {
		return nil, fmt.Errorf("system.replicas: %w", err)
	}
	byTable := map[string][]ReplicaState{}
	for rrows.Next() {
		var r ReplicaState
		var table string
		var total, active uint32
		var ro, sess uint8
		if err := rrows.Scan(&r.Host, &table, &r.ZKPath, &r.ReplicaName, &total, &active, &ro, &sess, &r.DelayS, &r.Queue, &r.LastException); err != nil {
			rrows.Close()
			return nil, err
		}
		r.TotalReplicas, r.ActiveReplicas = int(total), int(active)
		r.ReadOnly, r.SessionExpired = ro == 1, sess == 1
		r.Rows = map[string]uint64{}
		if h, ok := shardOf[r.Host]; ok {
			r.Shard, r.ReplicaNum = h.Shard, h.Replica
		} else {
			r.Shard = -1
		}
		byTable[table] = append(byTable[table], r)
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return nil, err
	}

	// Parçalar — aktif parçaların partition başına satırı, host başına.
	prows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT hostName(), table, partition, sum(rows)
		FROM clusterAllReplicas('%s', system.parts)
		WHERE database = ? AND active
		GROUP BY hostName(), table, partition
		SETTINGS max_execution_time = 15`, cluster), out.Database)
	if err != nil {
		return nil, fmt.Errorf("system.parts: %w", err)
	}
	for prows.Next() {
		var host, table, partition string
		var rows uint64
		if err := prows.Scan(&host, &table, &partition, &rows); err != nil {
			prows.Close()
			return nil, err
		}
		rs := byTable[table]
		for i := range rs {
			if rs[i].Host == host {
				rs[i].Rows[partition] += rows
				rs[i].TotalRows += rows
			}
		}
	}
	prows.Close()

	// Grupla + karar.
	tables := make([]string, 0, len(byTable))
	for t := range byTable {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		byShard := map[int][]ReplicaState{}
		for _, r := range byTable[t] {
			byShard[r.Shard] = append(byShard[r.Shard], r)
		}
		shards := make([]int, 0, len(byShard))
		for sh := range byShard {
			shards = append(shards, sh)
		}
		sort.Ints(shards)
		tbl := ReplicaTable{Table: t}
		var verdicts []string
		for _, sh := range shards {
			rs := byShard[sh]
			sort.Slice(rs, func(i, j int) bool { return rs[i].Host < rs[j].Host })
			rsh := ReplicaShard{Shard: sh, Replicas: rs}
			if sh < 0 {
				rsh.Verdict, rsh.Hint = ReplicaUnmapped, "Host system.clusters'taki küme tanımında yok: shard'a eşlenemedi (remote_servers ile hostName() uyuşmuyor)."
			} else {
				rsh.Verdict, rsh.Hint, rsh.DivergentPartition, rsh.DivergencePct = replicaVerdict(rs)
			}
			verdicts = append(verdicts, rsh.Verdict)
			tbl.Shards = append(tbl.Shards, rsh)
		}
		tbl.Verdict = worstVerdict(verdicts)
		out.Tables = append(out.Tables, tbl)
	}
	if out.LoadBalancing == "random" || out.LoadBalancing == "" {
		out.Notes = append(out.Notes, "load_balancing=random: her Distributed sorgu shard başına rastgele replika seçer; replikalar ıraksamışsa sonuç okumadan okumaya değişir.")
	}
	return out, nil
}
