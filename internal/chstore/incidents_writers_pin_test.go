package chstore

// incidents_writers_pin_test.go — v0.10.1826 (Incidents emekliliği, audit
// §16.1 E1). Ana anahtar yalnız İKİ kapıda (AttachProblemToIncidentWith,
// JoinOpenIncidentWith) ve yaşam döngüsü kancasında duruyor; bu pin hiçbir
// otomatik yazıcının kapıları atlayıp incident açamayacağını çiviler
// (test edilmiş ama ulaşılamaz sınıfı):
//   - `INSERT INTO incidents` / `incident_problems` yalnız chstore'da;
//   - chstore dışında `UpsertIncident(` yalnız bilinen arşiv-hijyeni
//     yollarında (elle API, bildirimden ack, evaluator kademeli kapanış) —
//     hiçbiri yeni incident açmaz;
//   - otomatik açıcılar Store kapı sarmalayıcılarını çağırır; çağıran
//     sayısı değişirse (yeni dedektör) bu pin bilinçli güncellenir.

import (
	"os"
	"path/filepath"
	"sort"
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
	attach := map[string]int{}
	join := map[string]int{}
	for path, s := range srcs {
		for _, raw := range []string{"INSERT INTO incidents", "INSERT INTO incident_problems", "INSERT INTO incident_events"} {
			if strings.Contains(s, raw) {
				t.Errorf("%s: %q chstore dışında — kapı atlanır", path, raw)
			}
		}
		if strings.Contains(s, ".UpsertIncident(") && !upsertAllowed[path] {
			t.Errorf("%s: UpsertIncident beklenmeyen yerde — incident yalnız kapılardan açılır", path)
		}
		if n := strings.Count(s, ".AttachProblemToIncident(ctx, p)"); n > 0 {
			attach[path] = n
		}
		if n := strings.Count(s, ".JoinOpenIncident(ctx, p, services)"); n > 0 {
			join[path] = n
		}
	}
	total := 0
	for _, n := range attach {
		total += n
	}
	if total != 18 {
		keys := make([]string, 0, len(attach))
		for k := range attach {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Errorf("otomatik AttachProblemToIncident çağıranı %d (istenen 18): %v", total, keys)
	}
	if len(join) != 1 || join["../evaluator/fatal_exception.go"] != 1 {
		t.Errorf("JoinOpenIncident yalnız evaluator.joinOpenIncident'ten: %v", join)
	}
}
