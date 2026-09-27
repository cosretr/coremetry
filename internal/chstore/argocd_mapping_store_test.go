package chstore

// argocd_mapping_store_test.go — v0.10.985 — Rollouts v2 P3.2 CH kapısı
// sözleşmesi (docs/rollouts/v2-audit.md §10.3.5, §10.6):
//   - eşleyicinin canlı kenar okuması FINAL + instance IN + removed_at = 0 +
//     tam anahtar keyset + sayfa LIMIT + max_execution_time;
//   - servis kartı: iş yükü kenarları (cluster_id, namespace, workload) ve
//     zayıf kenarlar (cluster_id, namespace) ayrı bütçeyle + tazelik sınırı +
//     tavan+1; son durum nokta öneki + LIMIT 1 BY (sayfa LIMIT'inden önce);
//     senkron sayımı change_kind='sync' + pencere + ardışık katlama; filo iş yükü okuması MV'den, zaman
//     sınırlı, LIMIT tavan+1;
//   - INSERT kolonları MappingRow sırasıyla (DDL'in 15 kolonu); canlı kenarın
//     removed_at'i 1970 (DEFAULT 0) bağlanır; tek geçersiz satır batch'i reddeder;
//   - okumalar ana bağlantıda (stateTables).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
)

func mustContain(t *testing.T, what, q string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(q, w) {
			t.Errorf("%s sorgusunda %q yok:\n%s", what, w, q)
		}
	}
}

func TestArgoCDLiveMappingsSQLShape(t *testing.T) {
	q := argocdLiveMappingsPageSQL(2)
	mustContain(t, "canlı kenar", q,
		"FROM argocd_app_mapping FINAL",
		"WHERE instance_id IN (?, ?)",
		"AND removed_at = toDateTime64(0, 3)",
		"AND (cluster_id, namespace, workload_kind, workload, instance_id, app_namespace, app_name) > (?, ?, ?, ?, ?, ?, ?)",
		"ORDER BY cluster_id, namespace, workload_kind, workload, instance_id, app_namespace, app_name",
		"LIMIT 20000",
		"max_execution_time = 15",
	)
	if n := strings.Count(q, "?"); n != 2+7 {
		t.Errorf("bağ sayısı %d, beklenen 9", n)
	}
}

func TestArgoCDServiceReadsSQLShape(t *testing.T) {
	// v0.10.985 inceleme: iş yükü ve zayıf kenarlar AYRI bütçe — zayıf
	// kenarlar (workload_kind '') ORDER BY'da önce gelir, ortak LIMIT'i
	// doldurup sonraki kümelerin iş yükü kenarlarını kesiyordu.
	q := argocdServiceWorkloadEdgesSQL(2)
	mustContain(t, "servis iş yükü kenarı", q,
		"FROM argocd_app_mapping FINAL",
		"WHERE (cluster_id, namespace, workload) IN ((?, ?, ?), (?, ?, ?))",
		"AND workload != ''",
		"AND removed_at = toDateTime64(0, 3)",
		"AND last_verified_at >= toDateTime64(?, 3, 'UTC')",
		"LIMIT 5001",
		"max_execution_time = 10",
	)
	if strings.Count(q, "?") != 7 {
		t.Errorf("servis iş yükü kenarı bağları: %d", strings.Count(q, "?"))
	}
	q = argocdServiceWeakEdgesSQL(2)
	mustContain(t, "servis zayıf kenar", q,
		"FROM argocd_app_mapping FINAL",
		"WHERE (cluster_id, namespace) IN ((?, ?), (?, ?))",
		"AND workload_kind = '' AND workload = ''",
		"AND removed_at = toDateTime64(0, 3)",
		"AND last_verified_at >= toDateTime64(?, 3, 'UTC')",
		"LIMIT 5001",
		"max_execution_time = 10",
	)
	if strings.Count(q, "?") != 5 {
		t.Errorf("servis zayıf kenar bağları: %d", strings.Count(q, "?"))
	}

	q = argocdStatusForAppsSQL(3)
	mustContain(t, "son durum", q,
		"FROM argocd_app_status FINAL",
		"WHERE (instance_id, app_namespace, app_name) IN ((?, ?, ?), (?, ?, ?), (?, ?, ?))",
		"ORDER BY instance_id, app_namespace, app_name, changed_at DESC",
		"LIMIT 1 BY instance_id, app_namespace, app_name",
		"LIMIT 3\n",
		"max_execution_time = 10",
	)
	if i, j := strings.Index(q, "LIMIT 1 BY"), strings.Index(q, "LIMIT 3\n"); i < 0 || j < i {
		t.Error("LIMIT 1 BY sayfa LIMIT'inden ÖNCE olmalı")
	}

	q = argocdSyncCountsSQL(2)
	mustContain(t, "senkron sayısı", q,
		"FROM argocd_app_status FINAL",
		"WHERE (instance_id, app_namespace, app_name) IN ((?, ?, ?), (?, ?, ?))",
		"AND change_kind = 'sync'",
		"AND changed_at >= toDateTime64(?, 3, 'UTC')",
		// v0.10.985 inceleme: lockDegraded'da iki pod'un bir tik arayla
		// yazdığı aynı senkron tek sayılır (DDL: okuyucu katlar).
		"lagInFrame(sync_phase) OVER w AS prev_phase",
		"lagInFrame(changed_at) OVER w AS prev_at",
		"PARTITION BY instance_id, app_namespace, app_name ORDER BY changed_at ASC",
		"WHERE NOT (sync_phase = prev_phase AND dateDiff('millisecond', prev_at, changed_at) <= ?)",
		"GROUP BY instance_id, app_namespace, app_name, sync_phase",
		"LIMIT 16",
		"max_execution_time = 10",
	)
	if strings.Count(q, "?") != 6+1+1 {
		t.Errorf("senkron sayısı bağları: %d (anahtarlar + pencere + katlama)", strings.Count(q, "?"))
	}

	q = argocdMapperWorkloadsSQL()
	mustContain(t, "filo iş yükü", q,
		"FROM workload_revision_activity_1m",
		"anyLast(workload_kind) AS kind",
		"WHERE bucket >= toDateTime64(?, 3, 'UTC') AND bucket <= toDateTime64(?, 3, 'UTC')",
		"AND workload != ''",
		"GROUP BY cluster, k8s_namespace, workload",
		"LIMIT 200001",
		"max_execution_time = 25",
	)
	if strings.Contains(q, "FROM spans") {
		t.Error("filo iş yükü MV'den okunur, ham spans'ten değil (invariant 3)")
	}
}

func TestArgoCDMappingColsAndArgs(t *testing.T) {
	cols := strings.Split(argocdMappingCols, ", ")
	if len(cols) != 15 || cols[0] != "cluster_id" || cols[6] != "app_name" || cols[13] != "removed_at" || cols[14] != "version" {
		t.Fatalf("kolonlar (DDL 15): %v", cols)
	}
	at := time.Date(2026, 9, 27, 10, 1, 0, 0, time.FixedZone("x", 3*3600))
	r := argocd.MappingRow{ClusterID: "c-a", Namespace: "pay", WorkloadKind: "Deployment", Workload: "api", InstanceID: "i1",
		AppNamespace: "apps", AppName: "pay-api", MatchMethod: argocd.MatchName, MatchClass: argocd.ClassEstimated,
		Confidence: 70, Candidates: 1, FirstMatchedAt: at, LastVerifiedAt: at, Version: 9}
	args := argocdMappingArgs(r)
	if len(args) != len(cols) {
		t.Fatalf("bağ sayısı %d ≠ kolon %d", len(args), len(cols))
	}
	if ts := args[11].(time.Time); ts.Location() != time.UTC || !ts.Equal(at) {
		t.Fatalf("first_matched_at UTC: %v", ts)
	}
	if rm := args[13].(time.Time); rm.Unix() != 0 {
		t.Fatalf("canlı kenarın removed_at'i 1970 (DEFAULT 0) bağlanmalı: %v", rm)
	}
	if args[9] != uint8(70) || args[10] != uint16(1) || args[14] != uint64(9) {
		t.Fatalf("bağ tipleri/sırası: %v", args)
	}
	r.RemovedAt = at
	if rm := argocdMappingArgs(r)[13].(time.Time); !rm.Equal(at) {
		t.Fatalf("kaldırılmış kenarın removed_at'i: %v", rm)
	}
}

func TestValidateArgoCDMappingsRejectsBatch(t *testing.T) {
	ok := argocd.MappingRow{ClusterID: "c", Namespace: "n", WorkloadKind: "Deployment", Workload: "w", InstanceID: "i",
		AppNamespace: "a", AppName: "x", MatchMethod: argocd.MatchManual, MatchClass: argocd.ClassExact, Confidence: 100,
		Candidates: 1, FirstMatchedAt: time.Now(), LastVerifiedAt: time.Now(), Version: 1}
	bad := ok
	bad.LastVerifiedAt = time.Time{}
	if err := validateArgoCDMappings([]argocd.MappingRow{ok}); err != nil {
		t.Fatal(err)
	}
	if err := validateArgoCDMappings([]argocd.MappingRow{ok, bad}); err == nil || !strings.Contains(err.Error(), "c/n/w ← i/a/x") {
		t.Fatalf("tek geçersiz satır batch'i reddetmeli: %v", err)
	}
}

func TestArgoCDMappingReadIsStateTable(t *testing.T) {
	need := map[string]bool{"FROM argocd_app_mapping": false, "FROM argocd_app_status": false}
	for _, s := range stateTables {
		if _, ok := need[s]; ok {
			need[s] = true
		}
	}
	for k, v := range need {
		if !v {
			t.Errorf("stateTables %q taşımalı (okuma ana bağlantıda)", k)
		}
	}
}
