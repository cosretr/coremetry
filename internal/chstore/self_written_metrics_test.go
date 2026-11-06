package chstore

// self_written_metrics_test.go — v0.10.1858 regresyon kapısı.
//
// Belirti: self-ingest-stall (P1 "Coremetry veri almıyor") hiç açılmıyordu.
// Kök neden: evaluator'ın her tikte yazdığı disk serisi (v0.10.911) ve
// Oracle sayacı metric_catalog.last_seen'i hep taze tutuyordu; kural
// "span VE metrik sustu" istediği için span'ler dursa da alarm kördü.
//
// MUTASYON KANITI: ingestMetricLastSeenSQL'den WHERE satırını sil →
// TestIngestMetricLastSeenExcludesSelfWritten kızarır. SelfWrittenMetrics'ten
// bir adı sil → aynı test + oracle paketindeki eşitlik testi kızarır.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestIngestMetricLastSeenExcludesSelfWritten(t *testing.T) {
	q, args := ingestMetricLastSeenSQL()
	if !strings.Contains(q, "FROM metric_catalog") {
		t.Fatalf("katalogdan okunmuyor:\n%s", q)
	}
	if !strings.Contains(q, "WHERE metric NOT IN (") {
		t.Fatalf("iç yazar serileri dışlanmıyor (alarm yine kör olur):\n%s", q)
	}
	if !strings.Contains(q, "max_execution_time") {
		t.Fatalf("süre sınırı yok:\n%s", q)
	}
	want := SelfWrittenMetrics()
	if len(args) != len(want) || strings.Count(q, "?") != len(want) {
		t.Fatalf("yer tutucu/bind sayısı uyuşmuyor: ? = %d, bind = %d, ad = %d",
			strings.Count(q, "?"), len(args), len(want))
	}
	for i, n := range want {
		if args[i] != n {
			t.Fatalf("bind[%d] = %v, beklenen %q", i, args[i], n)
		}
	}
}

func TestSelfWrittenMetricsRegistry(t *testing.T) {
	got := SelfWrittenMetrics()
	for _, must := range []string{SelfDiskUsedMetric, OracleErrorCounterMetric} {
		found := false
		for _, g := range got {
			if g == must {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q kayıtta yok", must)
		}
	}
	// Disk serisi gerçekten bu adla yazılıyor mu (yazan fonksiyonla aynı ad).
	pts := (&Store{}).SelfDiskPoints([]DiskFree{{Host: "node-1", Disk: "default", Total: 100, Free: 40}}, time.Now())
	if len(pts) != 1 || pts[0].Metric != SelfDiskUsedMetric {
		t.Fatalf("disk serisi beklenen adla yazılmıyor: %+v", pts)
	}
}

// TestInsertMetricsCallersAreRegistered — metric_points'e yazan her çağıran
// bilinmeli. Yeni bir iç yazar eklenirse: serinin adını SelfWrittenMetrics'e
// ekle (yoksa ingest-stall alarmı yeniden körleşir), SONRA dosyayı buraya.
func TestInsertMetricsCallersAreRegistered(t *testing.T) {
	root := filepath.Join("..", "..")
	want := []string{
		"internal/evaluator/selfhealth.go", // SelfDiskUsedMetric
		"internal/oracle/counter.go",       // OracleErrorCounterMetric
		"main.go",                          // OTLP alıcısı: dış telemetri, dışlanmaz
	}
	var got []string
	scan := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s okunamadı: %v", path, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "func (s *Store) InsertMetrics") {
				continue
			}
			// çağrı (`x.InsertMetrics(`) ya da metot değeri (`store.InsertMetrics)`)
			if strings.Contains(trim, ".InsertMetrics(ctx") || strings.Contains(trim, "store.InsertMetrics)") {
				rel, _ := filepath.Rel(root, path)
				got = append(got, filepath.ToSlash(rel))
				return
			}
		}
	}
	scan(filepath.Join(root, "main.go"))
	for _, dir := range []string{"internal", "cmd"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scan(path)
			return nil
		})
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("metric_points yazarları değişti.\n got: %v\nwant: %v\n"+
			"Yeni iç yazarın seri adını SelfWrittenMetrics'e ekle, sonra bu listeyi güncelle.", got, want)
	}
}
