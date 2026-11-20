package chstore

import (
	"strconv"
	"strings"
)

// db_receiver_mv.go — v0.10.2402: /databases receiver keşfinin MV ikizi.
//
// discoverReceiverInstances her /databases çağrısında metric_points'i
// tarayıp instance adını attr/res dizilerinden coalesce ediyordu
// (query_log: ~26.8 MiB/çağrı, ~2.4k çağrı; MV-first ihlali). Artık
// db_receiver_instance_5m insert anında (önek, instance) başına 5 dk
// kovalarda sayar; okuma kova sayılarını toplar. Ham sorgu YEDEK: MV ilk
// kovası pencere başından önce değilse (geriye dolmaz; taze kurulum,
// küme kipinde ertelenmiş DDL) o koşar.
//
// Tek gövde: instance zinciri (dbReceiverInstExpr) ve önek listesi
// (dbReceiverPrefixes) hem ham sorguyu hem MV'yi üretir. Önek listesi
// değişirse MV'nin SELECT'i değişir → DROP + RECREATE (§5).

// dbReceiverPrefixes — receiver aileleri: metrik öneki → motor.
var dbReceiverPrefixes = []struct{ metric, system string }{
	{"oracledb.", "oracle"},
	{"postgresql.", "postgresql"},
	{"mysql.", "mysql"},
	{"redis.", "redis"},
}

const dbReceiverInstMV = "db_receiver_instance_5m"

// dbReceiverInstTTLDays — metrik saklamasının varsayılanı (7 g). Daha uzun
// pencerede ilk kova pencereden sonra kalır → sourceCovers ham yola düşer.
const dbReceiverInstTTLDays = 7

// dbReceiverInstExpr — instance zinciri; specific = öneke özgü
// "<önek>instance.name" anahtarının ifadesi (ham: bağ parametresi "?",
// MV: concat(metric_prefix, 'instance.name')).
func dbReceiverInstExpr(specific string) string {
	return `coalesce(
			nullIf(attr_values[indexOf(attr_keys, ` + specific + `)], ''),
			nullIf(attr_values[indexOf(attr_keys, 'instance')], ''),
			nullIf(attr_values[indexOf(attr_keys, 'server.address')], ''),
			nullIf(res_values[indexOf(res_keys, 'service.name')], ''),
			''
		)`
}

// dbReceiverPrefixExpr — metrik adından aile öneki (” = aile dışı).
func dbReceiverPrefixExpr() string {
	parts := make([]string, 0, len(dbReceiverPrefixes)*2+1)
	for _, p := range dbReceiverPrefixes {
		parts = append(parts, "startsWith(metric, '"+p.metric+"'), '"+p.metric+"'")
	}
	parts = append(parts, "''")
	return "multiIf(" + strings.Join(parts, ", ") + ")"
}

// dbReceiverMVWhere — MV ön-elemesi: yalnız dört ailenin satırları.
func dbReceiverMVWhere() string {
	parts := make([]string, 0, len(dbReceiverPrefixes))
	for _, p := range dbReceiverPrefixes {
		parts = append(parts, "startsWith(metric, '"+p.metric+"')")
	}
	return strings.Join(parts, " OR ")
}

// dbReceiverInstRawSQL — ham yedek (eski sorgunun birebir aynısı).
var dbReceiverInstRawSQL = `
		SELECT ` + dbReceiverInstExpr("?") + ` AS inst,
		count() AS n
		FROM metric_points
		WHERE time >= ? AND time <= ?
		  AND startsWith(metric, ?)
		GROUP BY inst
		HAVING inst != ''
		ORDER BY n DESC, inst ASC
		LIMIT ` + strconv.Itoa(dbReceiverLimit) + `
		SETTINGS max_execution_time = 8`

// dbReceiverInstMVSQL — MV okuması; çıktı şekli ham sorguyla aynı.
var dbReceiverInstMVSQL = `
		SELECT inst, countMerge(n_state) AS n
		FROM ` + dbReceiverInstMV + `
		WHERE metric_prefix = ?
		  AND time_bucket >= toStartOfFiveMinute(?) AND time_bucket < ?
		GROUP BY inst
		HAVING inst != ''
		ORDER BY n DESC, inst ASC
		LIMIT ` + strconv.Itoa(dbReceiverLimit) + `
		SETTINGS max_execution_time = 8`

// dbReceiverInstMVSelect — MV'nin AS SELECT gövdesi (store.go kataloğu).
func dbReceiverInstMVSelect() string {
	return `
		   ` + dbReceiverPrefixExpr() + ` AS metric_prefix,
		   ` + dbReceiverInstExpr("concat(metric_prefix, 'instance.name')") + ` AS inst,
		   toStartOfFiveMinute(time) AS time_bucket,
		   countState() AS n_state
		 FROM metric_points
		 WHERE ` + dbReceiverMVWhere() + `
		 GROUP BY metric_prefix, inst, time_bucket`
}

// serviceMetaComboMVSelect — service_meta_combo_5m'in AS SELECT gövdesi
// (store.go kataloğu); projeksiyon ve guard deriveMetadataAllSQL'le ortak.
func serviceMetaComboMVSelect() string {
	return `
		   service_name,` + svcMetaProjection + `,
		   toStartOfFiveMinute(time) AS time_bucket,
		   countState() AS c_state
		 FROM spans
		 WHERE ` + svcMetaGuard + `
		 GROUP BY service_name, ug_val, sy_val, ns_val, dep_val, time_bucket`
}
