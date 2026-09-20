package chstore

import (
	"context"
	"fmt"
	"sort"
	"strconv"
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
	// Engine — v0.10.818: system.tables motoru (ReplicatedReplacingMergeTree…);
	// FE runbook eksik host'ta AYNI aileyi kurar (ReplacingMergeTree(version) /
	// AggregatingMergeTree eş yola düz ReplicatedMergeTree ile katılamaz).
	Engine string `json:"engine,omitempty"`
}

// ReplicaMissingHost — v0.10.818: shard'ın erişilebilir bir host'u bu tablo
// için system.replicas satırı vermedi. Engine boş = tablo o host'ta YOK;
// doluysa tablo var ama Replicated değil (düz MergeTree: yalnız kendine
// yazılanı tutar).
type ReplicaMissingHost struct {
	Host   string `json:"host"`
	Engine string `json:"engine,omitempty"`
}

// ReplicaShard — bir tablonun bir shard'ı: replikalar + karar.
type ReplicaShard struct {
	Shard              int                  `json:"shard"`
	Replicas           []ReplicaState       `json:"replicas"`
	Verdict            string               `json:"verdict"`
	Hint               string               `json:"hint"`
	DivergentPartition string               `json:"divergentPartition,omitempty"`
	DivergencePct      float64              `json:"divergencePct,omitempty"`
	Missing            []ReplicaMissingHost `json:"missing,omitempty"` // v0.10.818
}

// ReplicaTable — bir Replicated tablo, shard'ları ve en kötü karar.
type ReplicaTable struct {
	Table   string         `json:"table"`
	Shards  []ReplicaShard `json:"shards"`
	Verdict string         `json:"verdict"`
	// View/Inner — v0.10.824: `.inner_id.<uuid>` satırı bir combined
	// MaterializedView'ın GİZLİ hedefidir; tablo düzeyi onarım (ATTACH
	// PARTITION + EXCHANGE TABLES) burada YANLIŞTIR — EXCHANGE uuid'yi
	// taşımaz, MV `TO INNER UUID '<uuid>'` ile eski tabloya yazmaya devam
	// eder. View çözülemezse (MV satırı gelmedi) View boş kalır.
	View  string `json:"view,omitempty"`
	Inner bool   `json:"inner,omitempty"`
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
	// Warnings — v0.10.818: küme düzeyi kırmızı uyarılar (DDL'i işlemeyen host,
	// erişilemeyen host). Notes bilgi, Warnings eylem ister.
	Warnings []string `json:"warnings,omitempty"`
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
	// v0.10.818 — test ortamı bulgusu: shard 2'nin state tabloları yalnız bir
	// host'ta Replicated (1/1), öteki host'ta hiç kayıt yok; kart bunu "tek
	// replika · tutarlı" sayıyordu. Beklenen host (makro rosteri) ile kayıtlı
	// host karşılaştırılır:
	ReplicaMissing       = "missing_replica" // erişilebilir host'ta tablo bu yolda kayıtlı değil (tablo yok)
	ReplicaNotReplicated = "not_replicated"  // host'ta tablo var ama Replicated değil (düz MergeTree)
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
	ReplicaDivergent: 4, ReplicaReadOnly: 5, ReplicaSessionExpired: 6,
	ReplicaMissing: 7, ReplicaNotReplicated: 8, ReplicaNoReplication: 9, // v0.10.818 — yapısal üçlü en üstte
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

// shardCoverage — SAF (v0.10.818): shard'ın erişilebilir host'ları (expected,
// makro rosteri) içinde bu tablo için system.replicas satırı vermeyenler.
// engines: host → engine (system.tables; yoksa ""). Bir eksik host'ta tablo
// düz MergeTree ise not_replicated (veri var ama eşler replike etmez),
// tablo hiç yoksa missing_replica. Eksik yoksa ("", "").
// unseen: host'ta tablo Replicated ama system.replicas satırı yok (iki okuma
// arasında yaratıldı / o host replicas okumasında atlandı) — karar DEĞİL, not.
func shardCoverage(table string, expected []string, known []ReplicaState, engines map[string]string) (missing []ReplicaMissingHost, verdict, hint string, unseen []string) {
	have := map[string]bool{}
	for _, r := range known {
		have[r.Host] = true
	}
	plain := 0
	for _, h := range expected {
		if have[h] {
			continue
		}
		eng := engines[h]
		if strings.HasPrefix(eng, "Replicated") {
			unseen = append(unseen, h)
			continue
		}
		if eng != "" {
			plain++
		}
		missing = append(missing, ReplicaMissingHost{Host: h, Engine: eng})
	}
	sort.Strings(unseen)
	if len(missing) == 0 {
		return nil, "", "", unseen
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Host < missing[j].Host })
	names := make([]string, 0, len(missing))
	for _, m := range missing {
		if m.Engine != "" {
			names = append(names, fmt.Sprintf("%s (engine=%s)", m.Host, m.Engine))
		} else {
			names = append(names, m.Host+" (tablo yok)")
		}
	}
	if plain > 0 {
		return missing, ReplicaNotReplicated,
			fmt.Sprintf("%s: tablo %s host'unda Replicated DEĞİL — o host yalnız kendine yazılanı tutar, eşler replike etmez; rastgele replika seçimi her sorguda başka veri gösterir. ON CLUSTER DDL bu host'ta uygulanmamış olabilir (is_local uyarısına bak). Yerel veriyi eş ZK yolunda Replicated tabloya ATTACH edip değiştir — runbook.",
				table, strings.Join(names, ", ")), unseen
	}
	return missing, ReplicaMissing,
		fmt.Sprintf("%s: tablo %s host'unda YOK — ON CLUSTER DDL bu host'ta uygulanmamış (küme tanımında host kendini is_local görmüyorsa DDL atlanır, boot sessiz geçer) ya da tablo sonradan silinmiş. Eş replikanın SHOW CREATE TABLE'ıyla aynı ZK yolunda yeniden kur — runbook.",
			table, strings.Join(names, ", ")), unseen
}

// mergeCoverage — SAF (v0.10.818): kapsama kararı yapısaldır; sıralamada
// daha kötüyse replicaVerdict'in kararını ve ipucunu değiştirir (farklı ZK
// yolu = no_replication yine en üstte). Müşteri vakası: tek host 1/1 →
// replicaVerdict "single", kapsama "missing_replica" → missing_replica.
func mergeCoverage(base, baseHint, cv, chint string) (string, string) {
	if cv != "" && replicaVerdictRank[cv] > replicaVerdictRank[base] {
		return cv, chint
	}
	return base, baseHint
}

// innerTablePrefix / innerTableHint — v0.10.824 (operatör, test kümesi
// ekran görüntüsü 2026-09-20): shard 1'de bir `.inner_id.<uuid>` satırı bir
// host'ta "Replicated değil (AggregatingMergeTree)", ötekinde "tablo yok"
// çıktı ve kart 818'in `_fix` + ATTACH PARTITION + EXCHANGE TABLES
// merdivenini bastı. Bu merdiven MV iç tablosunda YANLIŞTIR: EXCHANGE
// tabloların uuid'sini TAŞIMAZ, MV DDL'i `TO INNER UUID '<uuid>'` ile eski
// (düz) tabloyu adresler ve oraya yazmaya devam eder — operatör "onardım"
// sanır, kaskad eski tabloya akar. Onarım MV düzeyindedir (820 onarım
// sihirbazı `.inner*` tablolarını zaten reddediyordu; eksik olan metindi).
const (
	innerTablePrefix = ".inner_id."
	innerTableHint   = "MV iç tablosu: ATTACH/EXCHANGE uygulanmaz — MV'yi kanonik DDL ile o host'ta (yeniden) kur; Admin → ClickHouse → MV onarımı kartı bunu tek tıkla yapar"
)

// isInnerTable — SAF: ad gizli MV iç tablosu mu.
func isInnerTable(name string) bool { return strings.HasPrefix(name, innerTablePrefix) }

// innerTableViews — SAF (v0.10.824): system.tables envanterindeki MV
// satırlarından `.inner_id.<uuid>` → view adı eşlemesi. İç tablonun uuid'si
// DDL'deki `TO INNER UUID` (Atomic) ya da view'ın kendi uuid'si (eski
// biçim) — innerUUIDFor ile, dangling_mv_admin ile TEK gövde. `TO <tablo>`
// biçimli MV'nin gizli iç tablosu yoktur, eşlemeye girmez.
func innerTableViews(rows []mvTableRow) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		if r.Engine != "MaterializedView" || r.UUID == "" || r.UUID == zeroUUID {
			continue
		}
		if !reInnerUUID.MatchString(r.CreateQuery) && reMVWithTO.MatchString(r.CreateQuery) {
			continue // TO <tablo>: hedefi gerçek tablo, gizli iç tablo yok
		}
		iu := innerUUIDFor(r.CreateQuery, r.UUID)
		if iu == "" || iu == zeroUUID {
			continue
		}
		out[innerTablePrefix+strings.ToLower(iu)] = r.Name
	}
	return out
}

// innerShardHint — SAF (v0.10.824): iç tablo satırında YAPISAL kararların
// ipucunu MV uyarısıyla DEĞİŞTİRİR (eklemez). Ekleme, taban ipucunun
// reçetesiyle ("… ATTACH edip değiştir — runbook.") tek cümlede çelişirdi;
// operatör ilk reçeteyi okur. Etkilenen host listesi kaybolmaz: kart onu
// Replikalar kolonunda sh.missing satırlarında zaten gösterir.
// Iraksama/readonly/oturum kararlarına dokunmaz: SYSTEM SYNC / RESTORE
// REPLICA iç tabloda da doğrudur (uuid değişmez), yanlış olan tablo
// TAKASIDIR.
func innerShardHint(base string, inner bool, verdict string) string {
	if !inner {
		return base
	}
	switch verdict {
	case ReplicaMissing, ReplicaNotReplicated, ReplicaNoReplication:
		return innerTableHint
	}
	return base
}

// blindHosts — SAF (v0.10.818): is_local sayımı 0 olan host'lar (zero) ve
// küme tanımında hiç satır vermeyen host'lar (absent: remote_servers bu
// küme adını içermiyor) — ikisi de ON CLUSTER DDL'i işlemez.
func blindHosts(roster []string, isLocalCount map[string]uint32) (zero, absent []string) {
	for _, h := range roster {
		n, ok := isLocalCount[h]
		switch {
		case !ok:
			absent = append(absent, h)
		case n == 0:
			zero = append(zero, h)
		}
	}
	sort.Strings(zero)
	sort.Strings(absent)
	return zero, absent
}

// rosterWarnings — SAF (v0.10.818): küme tanımındaki host sayısı vs cevap
// veren (system.one) host sayısı; cevap verip shard'a eşlenemeyenler ayrı
// (kapsama ölçülemedi, "erişilemez" DEĞİL).
func rosterWarnings(defined int, reachable, unmapped []string) []string {
	var out []string
	if len(reachable) < defined {
		out = append(out, fmt.Sprintf("küme tanımında %d host var, %d host cevap verdi — cevap vermeyen host'lar ölçüme girmedi (skip_unavailable_shards); onların tabloları bu kartta görünmez.", defined, len(reachable)))
	}
	if len(unmapped) > 0 {
		out = append(out, fmt.Sprintf("%s: cevap verdi ama shard'a eşlenemedi ({shard} makrosu yok ya da küme tanımıyla uyuşmuyor) — bu host'ların kapsaması ölçülemedi.", strings.Join(unmapped, ", ")))
	}
	return out
}

// shardRefFor — SAF (v0.10.792): hostName() → shard/replika. Öncelik
// system.clusters (host_name YA DA host_address hostName() ile eşleşir);
// eşleşmezse makrolar: {shard} sayısal ise o ("01" → 1), değilse sıralı
// ayrık değerler arasındaki sırası; {replica} yalnız etiket (replika no 0).
// Test ortamı 2026-09-19: küme tanımı IP ile yazılmıştı, hostName() OS adı;
// 791 her satırı "eşlenemedi" gösterdi, makrolar doğruyken.
func shardRefFor(host string, hosts []clusterHostRow, macros map[string]string, macroShards []string) (shard, replica int, via string) {
	for _, h := range hosts {
		if h.Host == host || (h.Addr != "" && h.Addr == host) {
			return h.Shard, h.Replica, "clusters"
		}
	}
	if m, ok := macros["shard"]; ok && strings.TrimSpace(m) != "" {
		m = strings.TrimSpace(m)
		if n, err := strconv.Atoi(strings.TrimLeft(m, "0")); err == nil && n > 0 {
			return n, 0, "macro"
		}
		if m == "0" || strings.Trim(m, "0") == "" {
			return 0, 0, "macro" // "00" gibi: sıfır shard'ı da eşle
		}
		for i, v := range macroShards {
			if v == m {
				return i + 1, 0, "macro"
			}
		}
	}
	return -1, 0, ""
}

// clusterMacrosByHost — küme geneli {shard}/{replica} makroları ve sıralı
// ayrık {shard} değerleri (v0.10.823'te ReplicaConsistency'nin içinden
// çıkarıldı). shardRefFor'un ikinci ve üçüncü girdisi buradan gelir:
// küme tanımı IP ile yazılmışsa hostName() → shard eşlemesinin TEK kaynağı
// makrolardır, ve iki yüzeyin aynı numarayı vermesi şarttır.
func (s *Store) clusterMacrosByHost(ctx context.Context, cluster string) (map[string]map[string]string, []string, error) {
	macrosByHost := map[string]map[string]string{}
	mrows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT hostName(), macro, substitution
		FROM clusterAllReplicas('%s', system.macros)
		SETTINGS max_execution_time = 10, skip_unavailable_shards = 1`, cluster))
	if err != nil {
		return nil, nil, fmt.Errorf("system.macros: %w", err)
	}
	for mrows.Next() {
		var host, macro, sub string
		if err := mrows.Scan(&host, &macro, &sub); err != nil {
			mrows.Close()
			return nil, nil, err
		}
		if macrosByHost[host] == nil {
			macrosByHost[host] = map[string]string{}
		}
		macrosByHost[host][macro] = sub
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return nil, nil, fmt.Errorf("system.macros: %w", err)
	}
	set := map[string]bool{}
	for _, m := range macrosByHost {
		if v := strings.TrimSpace(m["shard"]); v != "" {
			set[v] = true
		}
	}
	macroShards := make([]string, 0, len(set))
	for v := range set {
		macroShards = append(macroShards, v)
	}
	sort.Strings(macroShards)
	return macrosByHost, macroShards, nil
}

// hostShardMap — hostName() → shard numarası (eşlenemeyen host YOK sayılmaz,
// haritaya hiç girmez; çağıran onu -1 sayar). system.clusters + makrolar,
// karar shardRefFor'da (v0.10.792 IP-tanımlı küme dersi).
func (s *Store) hostShardMap(ctx context.Context, cluster string, hosts []string) (map[string]int, error) {
	hostRows, err := s.clusterHostRowsFor(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("system.clusters: %w", err)
	}
	macrosByHost, macroShards, err := s.clusterMacrosByHost(ctx, cluster)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(hosts))
	for _, h := range hosts {
		if sh, _, via := shardRefFor(h, hostRows, macrosByHost[h], macroShards); via != "" && sh >= 0 {
			out[h] = sh
		}
	}
	return out, nil
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

	// Makrolar — {shard}/{replica} yapılandırması; yol uyuşmazlığının kökü ve
	// system.clusters IP ile yazılmışsa hostName() → shard eşlemesinin kaynağı.
	// v0.10.823: okuma + sıralı ayrık {shard} listesi clusterMacrosByHost'a
	// ÇIKARILDI — ham sayım kartı da aynı eşlemeyi kullanır, iki kopya iki
	// farklı shard numarası üretirdi.
	macrosByHost, macroShards, err := s.clusterMacrosByHost(ctx, cluster)
	if err != nil {
		return nil, err
	}

	// v0.10.818 — erişilebilir roster: system.one her cevap veren host'tan
	// tam bir satır verir (makrosuz host da sayılır); makro rosteriyle birleşir.
	reachableSet := map[string]bool{}
	orows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT hostName()
		FROM clusterAllReplicas('%s', system.one)
		SETTINGS max_execution_time = 10, skip_unavailable_shards = 1`, cluster))
	if err != nil {
		return nil, fmt.Errorf("system.one: %w", err)
	}
	for orows.Next() {
		var h string
		if err := orows.Scan(&h); err != nil {
			orows.Close()
			return nil, err
		}
		reachableSet[h] = true
	}
	orows.Close()
	if err := orows.Err(); err != nil {
		return nil, fmt.Errorf("system.one: %w", err)
	}
	for h := range macrosByHost {
		reachableSet[h] = true
	}
	// hostName() değerleri: cevap veren her düğüm (makrolu ya da makrosuz).
	refOf := map[string][2]int{}
	viaMacro := 0
	hostNames := make([]string, 0, len(reachableSet))
	for h := range reachableSet {
		hostNames = append(hostNames, h)
	}
	sort.Strings(hostNames)
	for _, h := range hostNames {
		sh, rep, via := shardRefFor(h, hostRows, macrosByHost[h], macroShards)
		refOf[h] = [2]int{sh, rep}
		if via == "macro" {
			viaMacro++
		}
		out.Hosts = append(out.Hosts, ReplicaHost{Host: h, Shard: sh, Replica: rep, Macros: macrosByHost[h]})
	}
	if viaMacro > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("system.clusters host adları hostName() ile uyuşmuyor (küme tanımı IP/başka ad); %d host shard'a {shard} makrosuyla eşlendi, replika numarası bilinmiyor.", viaMacro))
	}
	for _, h := range hostRows {
		if _, ok := refOf[h.Host]; ok {
			continue
		}
		if h.Addr != "" {
			if _, ok := refOf[h.Addr]; ok {
				continue
			}
		}
		// Küme tanımındaki host hiçbir hostName() ile eşleşmedi — makro
		// eşlemesi kapsıyorsa sessiz, kapsamıyorsa görünür kalsın.
		if viaMacro == 0 {
			out.Hosts = append(out.Hosts, ReplicaHost{Host: h.Host, Shard: h.Shard, Replica: h.Replica})
		}
	}

	// v0.10.818 — beklenen host'lar: cevap veren VE shard'a eşlenen host'lar;
	// eşlenemeyenler ayrı uyarı (kapsama ölçülemedi), cevap vermeyenler
	// (küme tanımı sayısı > cevap) ayrı uyarı — ikisi "erişilemez" diye
	// karıştırılmaz.
	expectedByShard := map[int][]string{}
	var unmappedHosts []string
	for _, h := range hostNames {
		if sh := refOf[h][0]; sh >= 0 {
			expectedByShard[sh] = append(expectedByShard[sh], h)
		} else {
			unmappedHosts = append(unmappedHosts, h)
		}
	}
	out.Warnings = append(out.Warnings, rosterWarnings(len(hostRows), hostNames, unmappedHosts)...)
	// v0.10.818 — is_local: her host'un KENDİ küme görünümünde kendini bulması
	// gerekir; bulamayan (0) ya da bu küme adını hiç tanımayan (satır yok) host
	// ON CLUSTER DDL'i işlemez (tablolar eksik / Replicated değil kalır) ve boot
	// bunu null_status_on_timeout ile sessiz geçer.
	lrows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT hostName(), toUInt32(countIf(is_local))
		FROM clusterAllReplicas('%s', system.clusters)
		WHERE cluster = ?
		GROUP BY hostName()
		SETTINGS max_execution_time = 10, skip_unavailable_shards = 1`, cluster), cluster)
	if err != nil {
		out.Notes = append(out.Notes, "is_local denetimi okunamadı: "+err.Error())
	} else {
		isLocal := map[string]uint32{}
		for lrows.Next() {
			var host string
			var n uint32
			if err := lrows.Scan(&host, &n); err != nil {
				lrows.Close()
				return nil, err
			}
			isLocal[host] = n
		}
		lrows.Close()
		if err := lrows.Err(); err != nil {
			out.Notes = append(out.Notes, "is_local denetimi yarıda kesildi: "+err.Error())
		} else {
			zero, absent := blindHosts(hostNames, isLocal)
			if len(zero) > 0 {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s: küme tanımında kendini is_local görmüyor (remote_servers IP/başka adla yazılmış) — ON CLUSTER DDL bu host'ta UYGULANMAZ; Coremetry'nin boot DDL'i burada tablo yaratmaz/değiştirmez, boot sessiz geçer. Küme tanımını host'un kendi adıyla düzelt.", strings.Join(zero, ", ")))
			}
			if len(absent) > 0 {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s: küme tanımı (remote_servers) bu host'ta '%s' kümesini içermiyor — ON CLUSTER DDL burada UYGULANMAZ. Küme tanımını her host'a aynı adla yay.", strings.Join(absent, ", "), cluster))
			}
		}
	}

	// v0.10.818 — motor envanteri: hangi host'ta tablo var, Replicated mı.
	// v0.10.824 — AYNI okuma MV kimliğini de taşır (uuid + DDL öneki): iç
	// tablo → view eşlemesi buradan çıkar, ikinci bir küme geneli okuma
	// açılmaz. create_table_query yalnız MV satırlarında taşınır (ham tablo
	// DDL'i kilobaytlarca ve işe yaramaz).
	engineOf := map[string]map[string]string{} // table → host → engine
	var mvRows []mvTableRow                    // v0.10.824 — yalnız engine = 'MaterializedView'
	trows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT hostName(), name, engine, toString(uuid),
		       if(engine = 'MaterializedView', substring(create_table_query, 1, 400), '')
		FROM clusterAllReplicas('%s', system.tables)
		WHERE database = ? AND (engine LIKE '%%MergeTree%%' OR engine = 'MaterializedView')
		SETTINGS max_execution_time = 15, skip_unavailable_shards = 1`, cluster), out.Database)
	if err != nil {
		return nil, fmt.Errorf("system.tables: %w", err)
	}
	for trows.Next() {
		var r mvTableRow
		if err := trows.Scan(&r.Host, &r.Name, &r.Engine, &r.UUID, &r.CreateQuery); err != nil {
			trows.Close()
			return nil, err
		}
		if r.Engine == "MaterializedView" {
			mvRows = append(mvRows, r) // MV NESNESİ tablo satırı değil: kartta listelenmez
			continue
		}
		if engineOf[r.Name] == nil {
			engineOf[r.Name] = map[string]string{}
		}
		engineOf[r.Name][r.Host] = r.Engine
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return nil, fmt.Errorf("system.tables: %w", err) // yarım envanter = yanlış "tablo yok"
	}
	innerViews := innerTableViews(mvRows)

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
		SETTINGS max_execution_time = 15, skip_unavailable_shards = 1`, cluster), out.Database)
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
		if ref, ok := refOf[r.Host]; ok {
			r.Shard, r.ReplicaNum = ref[0], ref[1]
		} else {
			sh, rep, _ := shardRefFor(r.Host, hostRows, nil, nil)
			r.Shard, r.ReplicaNum = sh, rep
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
		SETTINGS max_execution_time = 15, skip_unavailable_shards = 1`, cluster), out.Database)
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
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("system.parts: %w", err) // yarım sayım = sahte ıraksama
	}

	// Grupla + karar. v0.10.818: hiçbir host'ta Replicated olmayan MergeTree
	// tabloları da listeye girer (engineOf'tan) — eskiden kart onları hiç
	// görmüyordu (spans_local tamamen düz MergeTree olsa sessizdi).
	tableSet := map[string]bool{}
	for t := range byTable {
		tableSet[t] = true
	}
	for t := range engineOf {
		tableSet[t] = true
	}
	tables := make([]string, 0, len(tableSet))
	for t := range tableSet {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		byShard := map[int][]ReplicaState{}
		for _, r := range byTable[t] {
			byShard[r.Shard] = append(byShard[r.Shard], r)
		}
		// Kapsama: tablonun bulunduğu (Replicated ya da düz) her host'un shard'ı
		// ve kayıtlı replikaların shard'ları birlikte.
		shardSet := map[int]bool{}
		for sh := range byShard {
			shardSet[sh] = true
		}
		for h := range engineOf[t] {
			if ref, ok := refOf[h]; ok && ref[0] >= 0 {
				shardSet[ref[0]] = true
			}
		}
		// v0.10.818 — bütün bir shard'da tablo yoksa (DDL o shard'ın iki host'unda
		// da atlanmış) hiçbir okuma satır vermez; beklenen shard'lar da gezilir.
		for sh := range expectedByShard {
			shardSet[sh] = true
		}
		shards := make([]int, 0, len(shardSet))
		for sh := range shardSet {
			shards = append(shards, sh)
		}
		sort.Ints(shards)
		tbl := ReplicaTable{Table: t}
		// v0.10.824 — iç tablo satırı: FE runbook'u tablo merdivenine değil MV
		// onarımına yollasın diye view adı (çözülebildiyse) satırda taşınır.
		if isInnerTable(t) {
			tbl.Inner, tbl.View = true, innerViews[t]
		}
		var verdicts []string
		for _, sh := range shards {
			rs := byShard[sh]
			if rs == nil {
				rs = []ReplicaState{} // JSON `[]` — FE sh.replicas[0]/map null'da patlamasın
			}
			sort.Slice(rs, func(i, j int) bool { return rs[i].Host < rs[j].Host })
			for i := range rs {
				rs[i].Engine = engineOf[t][rs[i].Host] // v0.10.818 — runbook aynı aileyi kurar
			}
			rsh := ReplicaShard{Shard: sh, Replicas: rs}
			if sh < 0 {
				rsh.Verdict, rsh.Hint = ReplicaUnmapped, "Host system.clusters'taki küme tanımında yok: shard'a eşlenemedi (remote_servers ile hostName() uyuşmuyor)."
			} else {
				rsh.Verdict, rsh.Hint, rsh.DivergentPartition, rsh.DivergencePct = replicaVerdict(rs)
				// v0.10.818 — kapsama kararı yapısaldır; sıralamada daha kötüyse kazanır
				// (mergeCoverage; farklı ZK yolu = no_replication yine en üstte kalır).
				missing, cv, chint, unseen := shardCoverage(t, expectedByShard[sh], rs, engineOf[t])
				rsh.Verdict, rsh.Hint = mergeCoverage(rsh.Verdict, rsh.Hint, cv, chint)
				rsh.Missing = missing
				if len(unseen) > 0 {
					out.Notes = append(out.Notes, fmt.Sprintf("%s · shard %d: %s Replicated ama system.replicas satırı yok (iki okuma arasında yaratıldı ya da o host replicas okumasında atlandı) — yeniden ölç; sürüyorsa SYSTEM RESTART REPLICA.", t, sh, strings.Join(unseen, ", ")))
				}
				// v0.10.824 — yapısal kararda iç tabloya tablo merdiveni yazılmaz.
				rsh.Hint = innerShardHint(rsh.Hint, tbl.Inner, rsh.Verdict)
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
