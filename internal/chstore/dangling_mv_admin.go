package chstore

// dangling_mv_admin.go — v0.10.762 SARKAN MV onarım sihirbazı (operatör:
// "sihirbaz yap"; prod olayı 2026-09-17: spans spool'u 509K dosya / 398 GiB,
// son hata "Target table `.inner_id.<uuid>` of view … doesn't exist").
//
// Combined bir MaterializedView'ın (TO'suz) verisi gizli `.inner_id.<view
// uuid>` tablosundadır. dropCombinedMV önce iç tabloyu (boyut kalkanı
// için, v0.8.190), sonra view nesnesini ON CLUSTER düşürür; ikinci adım
// bir node'da yarım kalırsa o node'da SARKAN view kalır: INSERT kaskadı
// hedef tabloyu bulamaz, INSERT reddedilir, Distributed spool'u o shard
// için büyür — trace'lerin yarısı aranamaz hâle gelir.
//
// Üç parça: (1) tespit — clusterAllReplicas(system.tables) üzerinden
// host başına "view var, .inner_id yok" (TO'lu MV'ler hariç); (2) onarım
// — YALNIZ o node'a bağlanıp (shardConn) view'ı düşür + kanonik DDL'i
// ON CLUSTER'sız yeniden kur (Replicated iç tablo ZK yoluna eş replika
// olarak katılır ve veriyi diğer replikadan çeker); (3) boot logu +
// dropCombinedMV sonrası artık temizliği (aynı sınıf bir daha sessiz
// kalmasın). Tek-node kipinde aynı mantık s.conn üzerinden.

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DanglingMV — bir node'daki sarkan view.
type DanglingMV struct {
	Host    string `json:"host"`
	Addr    string `json:"addr,omitempty"` // host:port (native); boş = çözülemedi
	Shard   int    `json:"shard,omitempty"`
	Replica int    `json:"replica,omitempty"`
	View    string `json:"view"`
	// UUID — EKSİK iç tablonun uuid'si (`.inner_id.<uuid>`; hata metnindeki).
	// v0.10.780: view'ın kendi uuid'si DEĞİL — Atomic DB'de iç tablo
	// `TO INNER UUID '…'` ile ayrı bir uuid taşır; 762 view uuid'sine bakıp
	// prod'daki sarkan view'ı GÖREMEDİ ("yok" dedi, spool 515K dosyaya çıktı).
	UUID      string `json:"uuid"`
	ViewUUID  string `json:"viewUuid,omitempty"`
	Canonical bool   `json:"canonical"` // kanonik DDL var → yeniden kurulabilir
	// PeerHost/PeerAddr — aynı shard'da iç tablosu SAĞLAM bir eş replika;
	// varsa onarım iç tabloyu oradan AYNI UUID ile kurar (view düşmez,
	// tarihçe replikasyondan gelir). Yoksa view yeniden kurulur (tarihçe yok).
	PeerHost string `json:"peerHost,omitempty"`
	PeerAddr string `json:"peerAddr,omitempty"`
}

// mvTableRow — system.tables'tan okunan satır (saf tespit girdisi).
type mvTableRow struct {
	Host, Name, UUID, Engine, CreateQuery string
}

const zeroUUID = "00000000-0000-0000-0000-000000000000"

// reMVWithTO — "CREATE MATERIALIZED VIEW db.x TO db.t …": iç tablosu yok,
// sarkan sayılmaz.
var reMVWithTO = regexp.MustCompile(`(?is)^\s*CREATE\s+MATERIALIZED\s+VIEW\s+\S+\s+TO\s+`)

// chObjRe — SYSTEM/DDL'e girecek nesne adı (spool_ops.go ile aynı disiplin).
var chObjRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reInnerUUID — Atomic DB'de MV DDL'i iç tablonun uuid'sini ayrıca taşır.
var reInnerUUID = regexp.MustCompile(`(?i)TO\s+INNER\s+UUID\s+'([0-9a-fA-F-]{36})'`)

// innerUUIDFor — SAF: iç tablonun uuid'si — DDL'de `TO INNER UUID` varsa o,
// yoksa view'ın uuid'si (eski sürüm davranışı).
func innerUUIDFor(createQuery, viewUUID string) string {
	if m := reInnerUUID.FindStringSubmatch(createQuery); m != nil {
		return strings.ToLower(m[1])
	}
	return viewUUID
}

// injectTableUUID — SAF: SHOW CREATE çıktısına (uuid'siz gelir;
// show_table_uuid_in_table_create_query_if_not_nil=0) tablo adından hemen
// sonra `UUID '<uuid>'` ekler ki eş replikada AYNI uuid ile kurulsun ve
// {uuid}'li Replicated yoluna katılsın. Zaten varsa dokunmaz.
func injectTableUUID(ddl, uuid string) string {
	if strings.Contains(ddl, "UUID '") {
		return ddl
	}
	name := "`.inner_id." + uuid + "`"
	i := strings.Index(ddl, name)
	if i < 0 {
		return ddl
	}
	i += len(name)
	return ddl[:i] + " UUID '" + uuid + "'" + ddl[i:]
}

// danglingFromRows — SAF: host başına view uuid'si için `.inner_id.<uuid>`
// var mı. Çıktı host, view sıralı.
func danglingFromRows(rows []mvTableRow) []DanglingMV {
	inner := map[string]map[string]bool{}
	var views []mvTableRow
	for _, r := range rows {
		if strings.HasPrefix(r.Name, ".inner_id.") {
			if inner[r.Host] == nil {
				inner[r.Host] = map[string]bool{}
			}
			inner[r.Host][r.Name] = true
			continue
		}
		if r.Engine == "MaterializedView" {
			views = append(views, r)
		}
	}
	out := []DanglingMV{}
	for _, v := range views {
		if v.UUID == "" || v.UUID == zeroUUID || reMVWithTO.MatchString(v.CreateQuery) {
			continue
		}
		iu := innerUUIDFor(v.CreateQuery, v.UUID)
		if inner[v.Host][".inner_id."+iu] {
			continue
		}
		_, canon := canonicalMVForObject(v.Name)
		d := DanglingMV{Host: v.Host, View: v.Name, UUID: iu, ViewUUID: v.UUID, Canonical: canon}
		// Eş replika adayı: iç tablo başka bir host'ta duruyor mu (shard eşleşmesi
		// DanglingMVs'te adreslerle yapılır; burada yalnız aday listesi).
		for h, set := range inner {
			if h != v.Host && set[".inner_id."+iu] {
				d.PeerHost = h
				break
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].View < out[j].View
	})
	return out
}

// canonicalMVForObject — nesne adı → kanonik MV adı. Terfi edilmiş MV'ler
// kümede `<ad>_local` olarak yaşar (mvStorageName).
func canonicalMVForObject(obj string) (string, bool) {
	if canonicalMVDDL(obj) != "" {
		return obj, true
	}
	if base := strings.TrimSuffix(obj, "_local"); base != obj && canonicalMVDDL(base) != "" {
		return base, true
	}
	return "", false
}

// reOnCluster — adaptDDL'in eklediği ON CLUSTER parçası; node'a özel
// çalıştırmada sökülür (aksi hâlde DDL yine kümeye dağılır).
var reOnCluster = regexp.MustCompile("(?i)\\s+ON\\s+CLUSTER\\s+`?[A-Za-z0-9_.-]+`?")

// stripOnCluster — SAF.
func stripOnCluster(sql string) string { return reOnCluster.ReplaceAllString(sql, "") }

// DanglingMVs — küme geneli (ya da tek node) sarkan view listesi.
func (s *Store) DanglingMVs(ctx context.Context) ([]DanglingMV, string, error) {
	cluster := ""
	src := "system.tables"
	if s.clusterMode() {
		cluster = strings.TrimSpace(s.cfg.ClusterName)
		src = fmt.Sprintf("clusterAllReplicas('%s', system.tables)", cluster)
	}
	rows, err := s.conn.Query(ctx, `
		SELECT hostName(), name, toString(uuid), engine, substring(create_table_query, 1, 400)
		FROM `+src+`
		WHERE database = currentDatabase()
		  AND (engine = 'MaterializedView' OR name LIKE '.inner_id.%')
		SETTINGS max_execution_time = 10`)
	if err != nil {
		return nil, cluster, err
	}
	defer rows.Close()
	var in []mvTableRow
	for rows.Next() {
		var r mvTableRow
		if err := rows.Scan(&r.Host, &r.Name, &r.UUID, &r.Engine, &r.CreateQuery); err != nil {
			return nil, cluster, err
		}
		in = append(in, r)
	}
	if err := rows.Err(); err != nil {
		return nil, cluster, err
	}
	out := danglingFromRows(in)
	if cluster != "" && len(out) > 0 {
		hosts, _, herr := s.clusterHostRows(ctx)
		if herr == nil {
			byHost := map[string]clusterHostRow{}
			for _, h := range hosts {
				byHost[h.Host] = h
			}
			for i := range out {
				if h, ok := byHost[out[i].Host]; ok {
					out[i].Addr, out[i].Shard, out[i].Replica = fmt.Sprintf("%s:%d", h.Host, h.Port), h.Shard, h.Replica
				}
				// Eş replika: AYNI shard'da iç tablosu sağlam bir host (ilk aday
				// başka shard'daysa yeniden ara).
				out[i].PeerHost, out[i].PeerAddr = "", ""
				for _, r := range in {
					if r.Host == out[i].Host || r.Name != ".inner_id."+out[i].UUID {
						continue
					}
					if ph, ok := byHost[r.Host]; ok && ph.Shard == out[i].Shard && out[i].Shard != 0 {
						out[i].PeerHost, out[i].PeerAddr = ph.Host, fmt.Sprintf("%s:%d", ph.Host, ph.Port)
						break
					}
				}
			}
		}
	}
	return out, cluster, nil
}

// RepairDanglingMV — o node'da: view'ı düşür (iç tablo zaten yok), kanonik
// DDL'i ON CLUSTER'sız kur, iç tablonun doğduğunu doğrula. Koşulan
// ifadeler döner (audit + ekran).
func (s *Store) RepairDanglingMV(ctx context.Context, host, view string) ([]string, error) {
	if !chObjRe.MatchString(view) {
		return nil, fmt.Errorf("geçersiz nesne adı %q", view)
	}
	list, _, err := s.DanglingMVs(ctx)
	if err != nil {
		return nil, fmt.Errorf("tespit: %w", err)
	}
	var row *DanglingMV
	for i := range list {
		if list[i].Host == host && list[i].View == view {
			row = &list[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("%s/%s şu an sarkan değil ya da bulunamadı — yeniden Ölç", host, view)
	}
	conn := s.conn
	if s.clusterMode() {
		if row.Addr == "" {
			return nil, fmt.Errorf("%s adresi system.clusters'tan çözülemedi", host)
		}
		c, err := s.shardConn(ctx, row.Addr)
		if err != nil {
			return nil, fmt.Errorf("node bağlantısı: %w", err)
		}
		conn = c
	}
	// v0.10.780 — TERCİH: iç tabloyu eş replikadan AYNI UUID ile kur. View
	// düşmez, iç tablo {uuid}'li Replicated yoluna katılır ve tarihçeyi eşten
	// çeker; kaskad anında düzelir. Eş yoksa eski yol (view'ı yeniden kur).
	if row.PeerAddr != "" && s.clusterMode() {
		peer, err := s.shardConn(ctx, row.PeerAddr)
		if err != nil {
			return nil, fmt.Errorf("eş replika bağlantısı (%s): %w", row.PeerHost, err)
		}
		var ddl string
		if err := peer.QueryRow(ctx, "SHOW CREATE TABLE `.inner_id."+row.UUID+"`").Scan(&ddl); err != nil {
			return nil, fmt.Errorf("eş replikadan DDL (%s): %w", row.PeerHost, err)
		}
		ddl = injectTableUUID(ddl, row.UUID)
		steps := []string{"-- eş replika " + row.PeerHost + "'ten, aynı UUID ile:", ddl}
		ectx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err = conn.Exec(ectx, ddl)
		cancel()
		if err != nil {
			return steps, fmt.Errorf("iç tablo kurulamadı: %w", err)
		}
		var n uint64
		if err := conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = currentDatabase() AND name = ?", ".inner_id."+row.UUID).Scan(&n); err != nil || n == 0 {
			return steps, fmt.Errorf("doğrulama: iç tablo doğmadı (%v)", err)
		}
		return steps, nil
	}
	name, ok := canonicalMVForObject(view)
	if !ok {
		return nil, fmt.Errorf("%s için kanonik DDL yok (migrations/*.sql MV'si) ve eş replika bulunamadı — elle onar", view)
	}
	steps := []string{"DROP TABLE IF EXISTS `" + view + "` SYNC"}
	for _, st := range s.adaptDDL(canonicalMVDDL(name)) {
		steps = append(steps, stripOnCluster(st))
	}
	for _, st := range steps {
		ectx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := conn.Exec(ectx, st)
		cancel()
		if err != nil {
			return steps, fmt.Errorf("%s: %w", firstWords(st, 4), err)
		}
	}
	// Doğrulama: view var ve iç tablosu doğdu.
	var uuid, cq string
	if err := conn.QueryRow(ctx, "SELECT toString(uuid), substring(create_table_query, 1, 400) FROM system.tables WHERE database = currentDatabase() AND name = ?", view).Scan(&uuid, &cq); err != nil {
		return steps, fmt.Errorf("doğrulama (view): %w", err)
	}
	var n uint64
	if err := conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = currentDatabase() AND name = ?", ".inner_id."+innerUUIDFor(cq, uuid)).Scan(&n); err != nil || n == 0 {
		return steps, fmt.Errorf("doğrulama: iç tablo doğmadı (%v)", err)
	}
	return steps, nil
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// dropLeftoverViewObjects — dropCombinedMV'nin ON CLUSTER view düşürmesi
// bir node'da yarım kaldıysa artık view'ı o node'da düşürür (best-effort,
// yüksek sesle log). Sonraki CREATE … IF NOT EXISTS ON CLUSTER o node'da
// artık sarkan adı görmez ve MV'yi taze kurar.
func (s *Store) dropLeftoverViewObjects(ctx context.Context, mv string) {
	if !s.clusterMode() {
		return
	}
	obj := s.mvStorageName(mv)
	list, _, err := s.DanglingMVs(ctx)
	if err != nil {
		log.Printf("[chstore] sarkan MV taraması düştü (%v) — %s için artık view kontrolü atlandı", err, obj)
		return
	}
	for _, d := range list {
		if d.View != obj {
			continue
		}
		if d.Addr == "" {
			log.Printf("[chstore] SARKAN VIEW %s@%s — adres çözülemedi, elle: DROP TABLE %s SYNC (o node'da)", d.View, d.Host, d.View)
			continue
		}
		c, cerr := s.shardConn(ctx, d.Addr)
		if cerr != nil {
			log.Printf("[chstore] SARKAN VIEW %s@%s — node bağlantısı: %v", d.View, d.Host, cerr)
			continue
		}
		if e := c.Exec(ctx, "DROP TABLE IF EXISTS `"+d.View+"` SYNC"); e != nil {
			log.Printf("[chstore] SARKAN VIEW %s@%s düşürülemedi: %v", d.View, d.Host, e)
			continue
		}
		log.Printf("[chstore] sarkan view %s@%s düşürüldü (ON CLUSTER drop o node'da yarım kalmıştı)", d.View, d.Host)
	}
}

// LogDanglingMVs — boot sonrası: sarkan view varsa yüksek sesle log.
// Onarım sihirbazdan (Admin → ClickHouse), boot'ta DDL koşmaz.
func (s *Store) LogDanglingMVs(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, _, err := s.DanglingMVs(pctx)
	if err != nil {
		log.Printf("[chstore] sarkan MV taraması düştü: %v", err)
		return
	}
	for _, d := range list {
		log.Printf("[chstore] SARKAN MV: %s@%s (uuid %s) — INSERT kaskadı bu node'da DÜŞER, Distributed spool büyür; Admin → ClickHouse → Sarkan MV onarımı", d.View, d.Host, d.UUID)
	}
}
