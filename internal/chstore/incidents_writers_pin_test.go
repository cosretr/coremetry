package chstore

// incidents_writers_pin_test.go — v0.10.1826 (Incidents emekliliği, audit
// §16.1 E1). Bu pin hiçbir otomatik yazıcının incident açamayacağını
// çiviler:
//   - `INSERT INTO incidents` / `incident_problems` yalnız chstore'da;
//   - chstore dışında `UpsertIncident(` yalnız bilinen arşiv-hijyeni
//     yollarında (elle API, bildirimden ack, evaluator kademeli kapanış) —
//     hiçbiri yeni incident açmaz;
//   - Incidents ölü kod dilimi 1: otomatik açıcılar (AttachProblemToIncident
//     18 çağıran, JoinOpenIncident 1) silindi; çağrıları geri gelemez.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goSourcesOutsideChstore(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	roots := []string{"..", filepath.Join("..", "..", "main.go")}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if filepath.Base(path) == "chstore" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(path)] = string(b)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestIncidentWritersGoThroughDoors(t *testing.T) {
	srcs := goSourcesOutsideChstore(t)
	upsertAllowed := map[string]bool{
		"../evaluator/evaluator.go": true, // kademeli kapanış (E4, sessiz)
		"../api/api_incidents.go":   true, // elle güncelle / ack / çözüm (+ 410'lu ilan)
		"../api/notify_actions.go":  true, // bildirim bağlantısından ack
	}
	for path, s := range srcs {
		for _, raw := range []string{"INSERT INTO incidents", "INSERT INTO incident_problems", "INSERT INTO incident_events"} {
			if strings.Contains(s, raw) {
				t.Errorf("%s: %q chstore dışında — kapı atlanır", path, raw)
			}
		}
		if strings.Contains(s, ".UpsertIncident(") && !upsertAllowed[path] {
			t.Errorf("%s: UpsertIncident beklenmeyen yerde — incident yalnız kapılardan açılır", path)
		}
		// v0.10.2052 18 → 19, v0.10.2068 19 → 18, Incidents ölü kod dilimi 1
		// 18 → 0: otomatik kapılar silindi; çağrı biçimi geri gelemez.
		for _, gone := range []string{".AttachProblemToIncident(", ".AttachProblemToIncidentWith(", ".JoinOpenIncident(", ".JoinOpenIncidentWith(", ".SetNeighborProvider("} {
			if strings.Contains(s, gone) {
				t.Errorf("%s: %s — otomatik incident kapısı silindi, geri gelmesin", path, gone)
			}
		}
	}
}
