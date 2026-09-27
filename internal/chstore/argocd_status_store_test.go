package chstore

// argocd_status_store_test.go — v0.10.983 — Rollouts v2 P3.1 CH kapısı
// sözleşmesi (docs/rollouts/v2-audit.md §10.3.3, §10.6):
//   - son durum okuması FINAL + instance nokta öneki + keyset + uygulama
//     başına LIMIT 1 BY (changed_at DESC) + sayfa LIMIT + max_execution_time;
//   - INSERT kolon listesi StatusRow sırasıyla, DDL'in 16 kolonu; bağlar tam
//     satır ve açık version; yazıcı sözleşmesini ihlal eden satır bütün
//     batch'i reddeder;
//   - okuma ana bağlantıda: stateTables "FROM argocd_app_status"u taşır.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
)

func TestArgoCDLatestStatusSQLShape(t *testing.T) {
	q := argocdLatestStatusPageSQL()
	for _, want := range []string{
		"FROM argocd_app_status FINAL",
		"WHERE instance_id = ?",
		"AND (app_namespace, app_name) > (?, ?)",
		"ORDER BY app_namespace, app_name, changed_at DESC",
		"LIMIT 1 BY app_namespace, app_name",
		"LIMIT 20000",
		"max_execution_time = 15",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("son durum sorgusunda %q yok:\n%s", want, q)
		}
	}
	if strings.Count(q, "?") != 3 {
		t.Errorf("3 bağ beklenir: %d", strings.Count(q, "?"))
	}
	if i, j := strings.Index(q, "LIMIT 1 BY"), strings.Index(q, "LIMIT 20000"); i < 0 || j < i {
		t.Error("LIMIT 1 BY sayfa LIMIT'inden ÖNCE olmalı")
	}
}

func TestArgoCDStatusColsAndArgs(t *testing.T) {
	cols := strings.Split(argocdStatusCols, ", ")
	if len(cols) != 16 || cols[0] != "instance_id" || cols[3] != "changed_at" || cols[15] != "version" {
		t.Fatalf("kolonlar (DDL 16): %v", cols)
	}
	at := time.Date(2026, 9, 27, 10, 1, 0, 0, time.FixedZone("x", 3*3600))
	r := argocd.StatusRow{InstanceID: "i", AppNamespace: "ns", AppName: "a", ChangedAt: at, ChangeKind: argocd.ChangeSync,
		SyncStatus: "Synced", HealthStatus: "Healthy", SyncPhase: "Succeeded", AutoSync: "true", ClusterID: "c-a", Version: 7}
	args := argocdStatusArgs(r)
	if len(args) != len(cols) {
		t.Fatalf("bağ sayısı %d ≠ kolon %d", len(args), len(cols))
	}
	if ts := args[3].(time.Time); ts.Location() != time.UTC || !ts.Equal(at) {
		t.Fatalf("changed_at UTC bağlanmalı: %v", ts)
	}
	if args[8] != "Succeeded" || args[14] != "c-a" || args[15] != uint64(7) {
		t.Fatalf("bağ sırası: %v", args)
	}
}

func TestValidateArgoCDStatusesRejectsBatch(t *testing.T) {
	ok := argocd.StatusRow{InstanceID: "i", AppNamespace: "ns", AppName: "a", ChangedAt: time.Now(), ChangeKind: argocd.ChangeState, Version: 1}
	bad := ok
	bad.ChangedAt = time.Time{}
	if err := validateArgoCDStatuses([]argocd.StatusRow{ok}); err != nil {
		t.Fatal(err)
	}
	if err := validateArgoCDStatuses([]argocd.StatusRow{ok, bad}); err == nil || !strings.Contains(err.Error(), "i/ns/a") {
		t.Fatalf("tek geçersiz satır batch'i reddetmeli: %v", err)
	}
}

func TestArgoCDStatusReadIsStateTable(t *testing.T) {
	for _, s := range stateTables {
		if s == "FROM argocd_app_status" {
			return
		}
	}
	t.Fatal("stateTables \"FROM argocd_app_status\" taşımalı (okuma ana bağlantıda)")
}

// v0.10.983 inceleme — silinmiş uygulamanın son satırı yeniden kuruluma
// alınmaz (churn'lü instance 200k tavanına silinmişlerle dayanmasın).
func TestArgoCDRebuildSkipsDeleted(t *testing.T) {
	for kind, want := range map[string]bool{
		argocd.ChangeBaseline: true, argocd.ChangeAppeared: true, argocd.ChangeState: true,
		argocd.ChangeSync: true, argocd.ChangeDeleted: false,
	} {
		if got := argocdRebuildKeep(argocd.StatusRow{ChangeKind: kind}); got != want {
			t.Errorf("%s: keep=%v, beklenen %v", kind, got, want)
		}
	}
	if argocdStatusScanCap <= argocdStatusHardCap {
		t.Fatal("tarama tavanı bellek tavanından büyük olmalı")
	}
}
