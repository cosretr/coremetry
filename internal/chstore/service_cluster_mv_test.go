package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.883 (paritesi #8 dilim 3) — cluster kırılımı MV'den: tdigest üç yüzdelik,
// HAVING cluster != ”, 50 tavanı, zaman+servis pinli; seri sorgusu kova adımını
// bağlar, IN listesi n yer tutucu; adım/kafes saf.
func TestServiceClusterMVSQLShape(t *testing.T) {
	q := serviceClusterBreakdownMVSQL()
	for _, w := range []string{"FROM service_env_summary_5m", "quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state)", "service_name = ?", "HAVING cluster != ''", "LIMIT 50", "max_execution_time = 10"} {
		if !strings.Contains(q, w) {
			t.Errorf("kırılım %q taşımıyor", w)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("ham tablo")
	}
	sq := serviceClusterSeriesMVSQL(3)
	if !strings.Contains(sq, "cluster IN (?,?,?)") || !strings.Contains(sq, "INTERVAL ? SECOND") || !strings.Contains(sq, "LIMIT 20000") {
		t.Errorf("seri: %s", sq)
	}
	t.Logf("CBSQL:%s", strings.ReplaceAll(q, "\n", " "))
	t.Logf("CSSQL:%s", strings.ReplaceAll(serviceClusterSeriesMVSQL(1), "\n", " "))
}

func TestClusterSeriesStepAndFill(t *testing.T) {
	if clusterSeriesStep(time.Hour) != 300 || clusterSeriesStep(24*time.Hour) != 300 {
		t.Fatal("≤24 sa 5 dk adım")
	}
	if s := clusterSeriesStep(7 * 24 * time.Hour); s != 2100 || s%300 != 0 {
		t.Fatalf("7 g adımı %d — 288 noktaya sığan 5 dk katı beklenir (2100)", s)
	}
	from := time.Date(2026, 9, 23, 12, 2, 0, 0, time.UTC)
	got := fillBuckets(map[int64]uint64{from.Truncate(5 * time.Minute).Unix(): 7}, from, from.Add(15*time.Minute), 300)
	if len(got) != 4 || got[0] != 7 || got[1] != 0 {
		t.Fatalf("kafes: %v", got)
	}
	if fillBuckets(nil, from, from, 300) != nil {
		t.Fatal("boş pencere nil")
	}
}
