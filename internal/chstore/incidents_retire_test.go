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

func TestIncidentDoorsOpenPure(t *testing.T) {
	if incidentDoorsOpen(false) || !incidentDoorsOpen(true) {
		t.Fatal("kapı yalnız açık anahtarda açık olmalı")
	}
}

func retireFakeStore() (*Store, *[]string, *[][]any) {
	var queries []string
	var appended [][]any
	return &Store{conn: exSnapFakeConn{queries: &queries, appended: &appended}}, &queries, &appended
}

func TestAttachDoorRespectsSwitch(t *testing.T) {
	p := Problem{ID: "p-1", RuleName: "err", Service: "svc-a", Severity: "critical"}

	t.Run("kapalı", func(t *testing.T) {
		withIncidentsEnabled(t, false)
		st, q, a := retireFakeStore()
		var hooked int
		st.SetIncidentLifecycleHook(func(context.Context, Incident, IncidentEvent) { hooked++ })
		inc, err := st.AttachProblemToIncidentWith(context.Background(), p, nil)
		if inc != nil || err != nil {
			t.Fatalf("kapalı: (nil, nil) bekleniyordu, (%v, %v)", inc, err)
		}
		if len(*q) != 0 || len(*a) != 0 || hooked != 0 {
			t.Fatalf("kapalı: CH'ye dokunulmamalı (sorgu %d, yazım %d, kanca %d)", len(*q), len(*a), hooked)
		}
	})
	t.Run("açık", func(t *testing.T) {
		withIncidentsEnabled(t, true)
		st, q, _ := retireFakeStore()
		var kinds []string
		st.SetIncidentLifecycleHook(func(_ context.Context, _ Incident, ev IncidentEvent) { kinds = append(kinds, ev.Kind) })
		inc, err := st.AttachProblemToIncidentWith(context.Background(), p, nil)
		if err != nil || inc == nil {
			t.Fatalf("açık: yeni incident bekleniyordu, (%v, %v)", inc, err)
		}
		joined := strings.Join(*q, "\n")
		for _, want := range []string{"FROM incident_problems FINAL", "FROM incidents FINAL", "INSERT INTO incidents", "INSERT INTO incident_problems"} {
			if !strings.Contains(joined, want) {
				t.Errorf("açık: %q sorgusu bekleniyordu", want)
			}
		}
		if len(kinds) != 1 || kinds[0] != "created" {
			t.Fatalf("açık: created kancası bir kez bekleniyordu, %v", kinds)
		}
	})
}

func TestJoinDoorRespectsSwitch(t *testing.T) {
	p := Problem{ID: "p-2", RuleName: "fatal", Service: "svc-b", Severity: "critical"}

	t.Run("kapalı", func(t *testing.T) {
		withIncidentsEnabled(t, false)
		st, q, _ := retireFakeStore()
		inc, joined, err := st.JoinOpenIncidentWith(context.Background(), p, []string{"svc-b"}, nil)
		if inc != nil || joined || err != nil || len(*q) != 0 {
			t.Fatalf("kapalı: (nil,false,nil) ve sorgusuz bekleniyordu; (%v,%v,%v) sorgu %d", inc, joined, err, len(*q))
		}
	})
	t.Run("açık", func(t *testing.T) {
		withIncidentsEnabled(t, true)
		st, q, _ := retireFakeStore()
		inc, joined, err := st.JoinOpenIncidentWith(context.Background(), p, []string{"svc-b"}, nil)
		// Eşleşen açık incident yok → bugünkü davranış: açmaz, katılmaz,
		// ama okumaları YAPAR.
		if inc != nil || joined || err != nil {
			t.Fatalf("açık: eşleşme yok → (nil,false,nil), (%v,%v,%v)", inc, joined, err)
		}
		if len(*q) == 0 {
			t.Fatal("açık: kapı okumaları atmalı")
		}
	})
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

// TestIncidentDoorsGateFirst — wiring pini: kapı iki kapının İLK deyimi;
// AttachProblemToIncident / JoinOpenIncident yalnız With hâline delege eder
// (otomatik yazıcıların hepsi bu iki sarmalayıcıdan geçer — api tarafı
// pini evaluator/anomaly/monitor çağıranlarını sayar).
func TestIncidentDoorsGateFirst(t *testing.T) {
	src := readSrc(t, "incident.go")
	for _, sig := range []string{
		"func (s *Store) AttachProblemToIncidentWith(ctx context.Context, p Problem, np NeighborProvider) (*Incident, error) {",
		"func (s *Store) JoinOpenIncidentWith(ctx context.Context, p Problem, services []string, np NeighborProvider) (*Incident, bool, error) {",
	} {
		i := strings.Index(src, sig)
		if i < 0 {
			t.Fatalf("imza yok: %s", sig)
		}
		body := src[i+len(sig):]
		gate := strings.Index(body, "if !incidentDoorsOpen(IncidentsEnabled()) {")
		firstCH := strings.Index(body, "s.conn.")
		if gate < 0 || firstCH < 0 || gate > firstCH {
			t.Errorf("%s: kapı ilk CH okumasından önce olmalı (gate=%d ch=%d)", sig, gate, firstCH)
		}
	}
	for _, want := range []string{
		"return s.AttachProblemToIncidentWith(ctx, p, s.neighborProvider)",
		"return s.JoinOpenIncidentWith(ctx, p, services, s.neighborProvider)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("sarmalayıcı delegesi yok: %s", want)
		}
	}
	lc := readSrc(t, "incident_lifecycle.go")
	if !strings.Contains(lc, "incidentLifecycleNotifies(ev.Kind, IncidentsEnabled())") {
		t.Error("yaşam döngüsü kancası ana anahtarla kapılı olmalı")
	}
}
