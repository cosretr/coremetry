package chstore

// service_namespace_membership.go — v0.10.1140 (operatör-raporlu, test
// ortamı): namespace → servis ÜYELİĞİ telemetriden.
//
// Kök neden: service_metadata servis başına TEK namespace tutar; deriver
// (marginalModes) en sık görülen değeri yazar. Aynı service.name hem
// payments-prep (yoğun) hem payments-uat (seyrek) namespace'inde koşunca
// katalog "payments-prep" der; Services sayfasının namespace süzgeci bu tek
// değere karşılaştırdığından servis azınlık namespace'inde HİÇ görünmez ve
// yalnız azınlıkta yaşayan bir namespace açılır listeye bile girmez.
//
// Kaynak: entity_seen_5m (entity_schema.go) — (service_name, cluster,
// k8s_namespace, k8s_pod) × 5 dk kova, `WHERE k8s_pod != ''` kapılı
// (k8s_pod host_name'e düşer, yani pratikte host'u olan her span).
//
//   ENGINE   AggregatingMergeTree, PARTITION BY toDate(time_bucket), TTL 30g
//   ORDER BY (service_name, cluster, k8s_namespace, k8s_pod, time_bucket)
//   Küme kipinde Distributed, shard anahtarı cityHash64(service_name).
//
// Maliyet (rakamlar entity_schema.go'nun kendi tahmininden: ~2.5k pod ×
// 288 kova/gün ≈ 720k satır/gün; servis × pod çarpanı küçük):
//   - ServicesSeenInNamespace: k8s_namespace ORDER BY'da 3. sırada → önek
//     DEĞİL; birincil anahtar yalnız generic-exclusion ile kısmen budar.
//     Budamanın asıl kaynağı gün partition'ı (toDate(time_bucket)). Okunan
//     yalnız 3-4 dar kolon (service_name, cluster, k8s_namespace,
//     time_bucket — agregat state'ler OKUNMAZ). 24 sa pencere ≈ ≤1M satır,
//     onlarca MB'nin altında; 30 s önbellekli. Ham spans'in milyarlarca
//     satırıyla kıyaslanamaz. Bilinçli seçim: ayrı (k8s_namespace önde) bir
//     MV, tek bir süzgeç için her span INSERT'ine ikinci yazım yükü getirirdi.
//   - NamespacesSeen: aynı tarama, tek kolon GROUP BY; 5 dk önbellekli.
//   - ServiceNamespaceCounts: service_name IN (sayfa ≤500 ad) → ORDER BY
//     öneki, birincil anahtar doğrudan budar; en ucuzu.
// Hepsi zaman sınırlı WHERE + LIMIT + max_execution_time (CLAUDE.md).
//
// Sınır (bilinçli): k8s_namespace yalnız k8s.namespace.name /
// kubernetes.namespace.name'den gelir; service.namespace kaynaklı
// namespace'ler burada YOKTUR. Bu yüzden çağıran katalog değerini
// (service_metadata.Namespace — elle sabitlenmiş ya da türetilmiş) her zaman
// BİRLEŞİMLE ekler; bu okuma eski davranışın üstüne yalnız ekler.
//
// entity_seen_5m yoksa (k8s_pod terfi kolonu yok / 0011 uygulanmamış) sorgu
// hata döner; çağıran fail-open katalog-yalnız davranışa düşer.

import (
	"context"
	"fmt"
	"time"
)

const (
	// nsMembershipServiceLimit — bir namespace'te görülen servis adı tavanı.
	// LIMIT'in birimi: SATIR = farklı service_name (GROUP BY sonrası).
	nsMembershipServiceLimit = 5000
	// nsSeenNamespaceLimit — açılır listedeki namespace tavanı; birim farklı
	// k8s_namespace. Alfabetik sıralı (büyüklüğe göre DEĞİL) — azınlık
	// namespace büyüklük yüzünden kesilmez.
	nsSeenNamespaceLimit = 1000
	// nsCountNameLimit — sayım için sorulan ad tavanı (sayfa ≤500).
	nsCountNameLimit = 500
)

// servicesSeenInNamespaceSQL — namespace (ve opsiyonel cluster) → pencerede
// telemetri gönderen service.name'ler. Saf; tablo-testli.
func servicesSeenInNamespaceSQL(namespace, cluster string, from, to time.Time) (string, []any) {
	args := []any{namespace, from, to}
	clusterWhere := ""
	if cluster != "" {
		clusterWhere = "\n\t\t  AND cluster = ?"
		args = append(args, cluster)
	}
	return fmt.Sprintf(`SELECT service_name
		FROM entity_seen_5m
		WHERE k8s_namespace = ?
		  AND time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= ?%s
		GROUP BY service_name
		ORDER BY service_name
		LIMIT %d
		SETTINGS max_execution_time = 10`, clusterWhere, nsMembershipServiceLimit), args
}

// ServicesSeenInNamespace — namespace'te pencerede span gönderen servisler.
// cluster boşsa tüm cluster'lar. Boş namespace → boş sonuç (sorgu yok).
func (s *Store) ServicesSeenInNamespace(ctx context.Context, namespace, cluster string, from, to time.Time) ([]string, error) {
	if namespace == "" {
		return []string{}, nil
	}
	sql, args := servicesSeenInNamespaceSQL(namespace, cluster, from, to)
	return s.scanStrings(ctx, "services seen in namespace", sql, args)
}

// namespacesSeenSQL — pencerede görülen farklı k8s_namespace'ler. Saf.
func namespacesSeenSQL(from, to time.Time) (string, []any) {
	return fmt.Sprintf(`SELECT k8s_namespace
		FROM entity_seen_5m
		WHERE k8s_namespace != ''
		  AND time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= ?
		GROUP BY k8s_namespace
		ORDER BY k8s_namespace
		LIMIT %d
		SETTINGS max_execution_time = 10`, nsSeenNamespaceLimit), []any{from, to}
}

// NamespacesSeen — pencerede telemetri gönderen namespace'ler (alfabetik).
func (s *Store) NamespacesSeen(ctx context.Context, from, to time.Time) ([]string, error) {
	sql, args := namespacesSeenSQL(from, to)
	return s.scanStrings(ctx, "namespaces seen", sql, args)
}

// serviceNamespaceCountsSQL — verilen servislerin pencerede koştuğu farklı
// namespace sayısı. service_name ORDER BY öneki → birincil anahtar budar. Saf.
func serviceNamespaceCountsSQL(services []string, from, to time.Time) (string, []any) {
	return fmt.Sprintf(`SELECT service_name, toUInt32(uniqExact(k8s_namespace)) AS n
		FROM entity_seen_5m
		WHERE service_name IN (?) AND k8s_namespace != ''
		  AND time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= ?
		GROUP BY service_name
		LIMIT %d
		SETTINGS max_execution_time = 10`, nsCountNameLimit), []any{services, from, to}
}

// ServiceNamespaceCounts — {servis: farklı namespace sayısı}; boş girdi →
// boş harita (sorgu yok). Girdi nsCountNameLimit'e kırpılır.
func (s *Store) ServiceNamespaceCounts(ctx context.Context, services []string, from, to time.Time) (map[string]int, error) {
	out := map[string]int{}
	if len(services) == 0 {
		return out, nil
	}
	if len(services) > nsCountNameLimit {
		services = services[:nsCountNameLimit]
	}
	sql, args := serviceNamespaceCountsSQL(services, from, to)
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("service namespace counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var svc string
		var n uint32
		if err := rows.Scan(&svc, &n); err != nil {
			return nil, err
		}
		out[svc] = int(n)
	}
	return out, rows.Err()
}

// scanStrings — tek String kolonlu okumanın ortak gövdesi; nil yerine [].
func (s *Store) scanStrings(ctx context.Context, what, sql string, args []any) ([]string, error) {
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
