package chstore

import (
	"strings"
	"testing"
)

// v0.10.2402 — ham-agregat MV ikizleri: service_meta_combo_5m
// (DeriveServiceMetadataAll) ve db_receiver_instance_5m
// (discoverReceiverInstances). Sözleşme: ham yedek sorgu ile MV AYNI
// ifade gövdesini taşır (iki kopya yok), MV'ler katalogda + üç küme
// sicilinde, okuyucu kapsama kapısından geçer. Gerçek CH eşitliği:
// mv_raw_agg_chsmoke_test.go.

func TestServiceMetaComboMVSharesRawBody(t *testing.T) {
	ddl := canonicalMVDDL(serviceMetaComboMV)
	if ddl == "" {
		t.Fatal("service_meta_combo_5m katalogda yok")
	}
	for name, frag := range map[string]string{"projeksiyon": svcMetaProjection, "guard": svcMetaGuard} {
		if !strings.Contains(ddl, frag) || !strings.Contains(deriveMetadataAllSQL, frag) {
			t.Errorf("%s MV ile ham sorguda ortak olmalı", name)
		}
	}
	for _, want := range []string{"countState() AS c_state", "FROM spans",
		"GROUP BY service_name, ug_val, sy_val, ns_val, dep_val, time_bucket",
		"ORDER BY (service_name, ug_val, sy_val, ns_val, dep_val, time_bucket)"} {
		if !strings.Contains(ddl, want) {
			t.Errorf("MV DDL %q taşımalı", want)
		}
	}
	for _, want := range []string{"countMerge(c_state) AS c", "FROM service_meta_combo_5m",
		"time_bucket >= toStartOfFiveMinute(?) AND time_bucket < ?",
		"GROUP BY service_name, ug_val, sy_val, ns_val, dep_val", "max_execution_time"} {
		if !strings.Contains(deriveMetadataMVSQL, want) {
			t.Errorf("MV okuması %q taşımalı", want)
		}
	}
}

// Ham yedek sorgu v0.10.2399'daki metnin birebir aynısı (boşluk normalize).
func TestDBReceiverRawSQLUnchanged(t *testing.T) {
	const golden = `SELECT coalesce( nullIf(attr_values[indexOf(attr_keys, ?)], ''), ` +
		`nullIf(attr_values[indexOf(attr_keys, 'instance')], ''), ` +
		`nullIf(attr_values[indexOf(attr_keys, 'server.address')], ''), ` +
		`nullIf(res_values[indexOf(res_keys, 'service.name')], ''), '' ) AS inst, ` +
		`count() AS n FROM metric_points WHERE time >= ? AND time <= ? AND startsWith(metric, ?) ` +
		`GROUP BY inst HAVING inst != '' ORDER BY n DESC, inst ASC LIMIT 2000 SETTINGS max_execution_time = 8`
	if got := strings.Join(strings.Fields(dbReceiverInstRawSQL), " "); got != golden {
		t.Fatalf("ham sorgu değişti:\n got: %s\nwant: %s", got, golden)
	}
}

func TestDBReceiverInstMVShape(t *testing.T) {
	ddl := canonicalMVDDL(dbReceiverInstMV)
	if ddl == "" {
		t.Fatal("db_receiver_instance_5m katalogda yok")
	}
	if !strings.Contains(ddl, dbReceiverInstExpr("concat(metric_prefix, 'instance.name')")) {
		t.Error("MV instance zinciri ham sorguyla aynı üreticiden gelmeli")
	}
	for _, p := range dbReceiverPrefixes {
		if !strings.Contains(ddl, "startsWith(metric, '"+p.metric+"'), '"+p.metric+"'") ||
			!strings.Contains(dbReceiverMVWhere(), "startsWith(metric, '"+p.metric+"')") {
			t.Errorf("önek %s MV'nin önek ifadesinde ve WHERE'inde olmalı", p.metric)
		}
	}
	for _, want := range []string{"FROM metric_points", "countState() AS n_state",
		"ORDER BY (metric_prefix, inst, time_bucket)", "GROUP BY metric_prefix, inst, time_bucket"} {
		if !strings.Contains(ddl, want) {
			t.Errorf("MV DDL %q taşımalı", want)
		}
	}
	for _, want := range []string{"countMerge(n_state) AS n", "metric_prefix = ?",
		"time_bucket >= toStartOfFiveMinute(?) AND time_bucket < ?", "HAVING inst != ''",
		"ORDER BY n DESC, inst ASC", "LIMIT 2000"} {
		if !strings.Contains(dbReceiverInstMVSQL, want) {
			t.Errorf("MV okuması %q taşımalı", want)
		}
	}
}

// Gün-bir üç küme kaydı + purge listesi (v0.5.426 dersi: bir sicile girip
// ötekine girmeyen MV küme kipinde göçü kırar ya da tek shard'ı okur).
func TestRawAggMVsRegistered(t *testing.T) {
	purge := map[string]bool{}
	for _, n := range telemetryPurgeTables {
		purge[n] = true
	}
	for _, n := range []string{serviceMetaComboMV, dbReceiverInstMV} {
		if !highVolumeTables[n] || defaultShardPolicy[n] == "" || !tablesWithoutTraceID[n] || !purge[n] {
			t.Errorf("%s: highVolumeTables / defaultShardPolicy / tablesWithoutTraceID / purge kaydı eksik", n)
		}
		if s := priorSourceProbeSQL[map[string]priorSource{serviceMetaComboMV: priorSrcSvcMetaCombo,
			dbReceiverInstMV: priorSrcDBReceiverInst}[n]]; !strings.Contains(s, "FROM "+n+" ") {
			t.Errorf("%s kapsama probu yanlış tabloyu okuyor: %s", n, s)
		}
	}
	shard := map[string]string{serviceMetaComboMV: "service_name", dbReceiverInstMV: "metric_prefix"}
	for n, col := range shard {
		if !strings.Contains(defaultShardPolicy[n], col) || !strings.Contains(canonicalMVDDL(n), "ORDER BY ("+col) {
			t.Errorf("%s: shard anahtarı ORDER BY öneki olmalı (O5)", n)
		}
	}
}
