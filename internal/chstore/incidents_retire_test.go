package chstore

// incidents_retire_test.go — v0.10.1826 (operatör kararı 2026-11-05:
// "Incident sekmesi yerine Anomalies olsun; eski incident'lar devre dışı
// kalsın"). Tek ana anahtar `incidents.enabled` (varsayılan KAPALI):
//   - iki otomatik kapı kapalıyken CH'ye DOKUNMADAN "incident yok" döner,
//     açıkken bugünkü yola girer (sorgu atar, created kancasını çağırır);
//   - yaşam döngüsü bildirimi kapalıyken hiçbir olayda gitmez;
//   - ayar round-trip'i (yaz → oku) atomic'i iki yönde günceller.
// Testler paket düzeyindeki atomic'i kurar ve t.Cleanup ile geri alır.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSrc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func withIncidentsEnabled(t *testing.T, on bool) {
	t.Helper()
	prev := IncidentsEnabled()
	SetIncidentsEnabled(on)
	t.Cleanup(func() { SetIncidentsEnabled(prev) })
}

// TestIncidentsSwitchNoProductionWriter — v0.10.2024 (operatör 2026-11-12:
// prod'da system_settings incidents={"enabled":true} kayıtlı kaldığından
// boot yüklemesi akışı açıyor, menü Incidents gösterip Anomalies'i
// gizliyordu; "sen default yap onu"). Anahtar kalktı: repo'da hiçbir
// üretim (_test olmayan) Go dosyası SetIncidentsEnabled çağırmaz ve
// `incidents` blobunu okuyan yükleyici yoktur — kayıtlı değer ne olursa
// olsun IncidentsEnabled() false kalır.
func TestIncidentsSwitchNoProductionWriter(t *testing.T) {
	if IncidentsEnabled() {
		t.Fatal("süreç açılışında akış kapalı olmalı")
	}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "frontend", ".git", ".claude", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := string(b)
		n := strings.Count(src, "SetIncidentsEnabled(")
		if filepath.Base(p) == "incidents_settings.go" && strings.Contains(p, "chstore") {
			n -= strings.Count(src, "func SetIncidentsEnabled(")
		}
		if n > 0 {
			t.Errorf("%s: üretim kodu SetIncidentsEnabled çağırmamalı", p)
		}
		for _, bad := range []string{"LoadIncidentsSettings", "SaveIncidentsSettings", "IncidentsSettingsKey"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s: %q geri gelmemeli (kayıtlı anahtar okunmaz)", p, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIncidentLifecycleNotifiesSwitch(t *testing.T) {
	for _, kind := range []string{"created", "resolved", "severity_raised", "ack", "note", "problem_attached", ""} {
		if incidentLifecycleNotifies(kind, false) {
			t.Errorf("kapalı anahtar: kind=%q bildirmemeli", kind)
		}
	}
	for kind, want := range map[string]bool{"created": true, "resolved": true, "severity_raised": true, "ack": false, "note": false} {
		if got := incidentLifecycleNotifies(kind, true); got != want {
			t.Errorf("açık anahtar: kind=%q → %v, istenen %v", kind, got, want)
		}
	}
}

func retireFakeStore() (*Store, *[]string, *[][]any) {
	var queries []string
	var appended [][]any
	return &Store{conn: exSnapFakeConn{queries: &queries, appended: &appended}}, &queries, &appended
}

func TestLifecycleHookSilentWhenRetired(t *testing.T) {
	for _, on := range []bool{false, true} {
		withIncidentsEnabled(t, on)
		st, _, _ := retireFakeStore()
		var hooked int
		st.SetIncidentLifecycleHook(func(context.Context, Incident, IncidentEvent) { hooked++ })
		// Elle çözüm / kademeli kapanış yolu: olay yazılır, kanca yalnız açıkken.
		_ = st.AppendIncidentLifecycle(context.Background(), Incident{ID: "i-1"}, IncidentEvent{Kind: "resolved"})
		want := 0
		if on {
			want = 1
		}
		if hooked != want {
			t.Errorf("enabled=%v: kanca %d kez, istenen %d", on, hooked, want)
		}
	}
}

// TestNoAutoIncidentDoors — v0.10.2070 (Incidents ölü kod dilimi 1): otomatik
// incident kapıları (AttachProblemToIncident / JoinOpenIncident) SİLİNDİ;
// geri gelmesin. Yaşam döngüsü kancası hâlâ ana anahtarla kapılı.
func TestNoAutoIncidentDoors(t *testing.T) {
	src := readSrc(t, "incident.go")
	for _, gone := range []string{"AttachProblemToIncident", "JoinOpenIncident", "NeighborProvider"} {
		if strings.Contains(src, gone) {
			t.Errorf("incident.go: %s geri geldi", gone)
		}
	}
	lc := readSrc(t, "incident_lifecycle.go")
	if !strings.Contains(lc, "incidentLifecycleNotifies(ev.Kind, IncidentsEnabled())") {
		t.Error("yaşam döngüsü kancası ana anahtarla kapılı olmalı")
	}
}
