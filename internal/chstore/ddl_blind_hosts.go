package chstore

// ddl_blind_hosts.go — v0.10.823 boot uyarısı: küme tanımında KENDİNİ
// bulamayan host'lar (operatör test kümesi, 2026-09-19).
//
// ON CLUSTER DDL bir host'ta yalnız o host kendi system.clusters
// görünümünde is_local=1 bir satır görürse çalışır. remote_servers IP ile
// (ya da başka bir adla) yazılmışsa host kendini tanımaz: DDL o düğümde
// SESSİZCE atlanır, istisna yoktur, boot yeşil geçer. Sonuç aylar sonra
// başka bir kılıkta görünür — tablo bir host'ta hiç yok ya da düz
// MergeTree kalmış, aynı shard'ın replikaları birbirini replike etmiyor,
// Distributed okuması her yenilemede başka bir sayı veriyor (v0.10.818
// "Replika tutarlılığı" kartının teşhis ettiği durum).
//
// Kart operatörün oraya bakmasını bekler; bu dosya boot günlüğüne YÜKSEK
// SESLE yazar — v0.10.762'nin sarkan-MV logu ile aynı yaşam döngüsü
// (store.go New(), DDL koşmaz, bloklamaz, hata yalnız log).
//
// Sınıflandırma saf ve PAYLAŞIMLI: blindHosts (replica_consistency.go,
// v0.10.818). İki durum ayrıdır ve ikisi de DDL'i öldürür:
//   - zero:   host cevap verdi, küme adını tanıyor ama is_local sayısı 0
//   - absent: host cevap verdi ama o küme adına hiç satırı yok
//
// İkincisi GROUP BY'dan DOĞAL olarak çıkmaz — sıfır satırlı grup hiç satır
// üretmez — bu yüzden roster ayrıca system.one'dan okunur.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// ddlBlindRosterSQL — SAF: cevap veren her düğüm tam bir satır verir.
func ddlBlindRosterSQL(cluster string) string {
	return fmt.Sprintf(`
		SELECT hostName()
		FROM clusterAllReplicas('%s', system.one)
		SETTINGS max_execution_time = 10, skip_unavailable_shards = 1`, cluster)
}

// ddlBlindIsLocalSQL — SAF: her host'un KENDİ küme görünümünde bu küme adı
// için is_local sayısı. Küme adı WHERE'de BAĞLANIR (literal değil);
// currentDatabase() burada yok — sorgu veritabanı kapsamlı değil.
func ddlBlindIsLocalSQL(cluster string) string {
	return fmt.Sprintf(`
		SELECT hostName(), toUInt32(countIf(is_local))
		FROM clusterAllReplicas('%s', system.clusters)
		WHERE cluster = ?
		GROUP BY 1
		ORDER BY 1
		SETTINGS max_execution_time = 10, skip_unavailable_shards = 1`, cluster)
}

// LogDDLBlindHosts — boot sonrası: DDL-kör host varsa yüksek sesle log.
// Küme kipi dışında hiç koşmaz. 10 sn tavan; her hata TEK satır log,
// asla fatal — bu bir teşhis, boot kapısı değil.
func (s *Store) LogDDLBlindHosts(ctx context.Context) {
	if !s.clusterMode() {
		return
	}
	cluster := strings.TrimSpace(s.cfg.ClusterName)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	roster := map[string]bool{}
	rrows, err := s.conn.Query(pctx, ddlBlindRosterSQL(cluster))
	if err != nil {
		log.Printf("[chstore] DDL-kör host taraması düştü (system.one): %v", err)
		return
	}
	for rrows.Next() {
		var h string
		if err := rrows.Scan(&h); err != nil {
			rrows.Close()
			log.Printf("[chstore] DDL-kör host taraması düştü (system.one satırı): %v", err)
			return
		}
		roster[h] = true
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		log.Printf("[chstore] DDL-kör host taraması yarıda kesildi (system.one): %v", err)
		return // yarım roster = uydurma "absent" uyarısı
	}

	isLocal := map[string]uint32{}
	lrows, err := s.conn.Query(pctx, ddlBlindIsLocalSQL(cluster), cluster)
	if err != nil {
		log.Printf("[chstore] DDL-kör host taraması düştü (system.clusters): %v", err)
		return
	}
	for lrows.Next() {
		var h string
		var n uint32
		if err := lrows.Scan(&h, &n); err != nil {
			lrows.Close()
			log.Printf("[chstore] DDL-kör host taraması düştü (system.clusters satırı): %v", err)
			return
		}
		isLocal[h] = n
		roster[h] = true
	}
	lrows.Close()
	if err := lrows.Err(); err != nil {
		log.Printf("[chstore] DDL-kör host taraması yarıda kesildi (system.clusters): %v", err)
		return
	}

	hosts := make([]string, 0, len(roster))
	for h := range roster {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	zero, absent := blindHosts(hosts, isLocal)
	for _, h := range zero {
		log.Printf("[chstore] DDL-KÖR HOST %s: küme tanımında kendini is_local görmüyor — ON CLUSTER DDL bu host'ta UYGULANMAZ (küme tanımını host adıyla düzelt; Admin → ClickHouse → Replika tutarlılığı / onarımı)", h)
	}
	for _, h := range absent {
		log.Printf("[chstore] DDL-KÖR HOST %s: küme tanımı (remote_servers) bu host'ta '%s' kümesini içermiyor — ON CLUSTER DDL bu host'ta UYGULANMAZ (küme tanımını her host'a aynı adla yay; Admin → ClickHouse → Replika tutarlılığı / onarımı)", h, cluster)
	}
}
