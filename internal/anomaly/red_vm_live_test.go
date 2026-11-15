package anomaly

// red_vm_live_test.go — v0.10.1478: CANLI VictoriaMetrics'e karşı uçtan uca
// duman testi (CI'da atlanır). Çalıştırma:
//
//	COREMETRY_TEST_VM_URL=http://localhost:8428 COREMETRY_TEST_VM_SERVICE=<svc> \
//	  go test ./internal/anomaly -run TestREDVMLive -v
//
// VM'de son 16 günün http_server_request_duration_seconds_* serisi olmalı.
// Ölçtüğü: ısınma tik sayısı + tik başına sorgu/süre (DECISIONS'daki sayılar)
// ve servisin seçilen kaynakla serisi.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestREDVMLive(t *testing.T) {
	url := os.Getenv("COREMETRY_TEST_VM_URL")
	svc := os.Getenv("COREMETRY_TEST_VM_SERVICE")
	if url == "" || svc == "" {
		t.Skip("COREMETRY_TEST_VM_URL / COREMETRY_TEST_VM_SERVICE yok")
	}
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: url})
	src := newVMREDSource()
	src.wire.Store(&vmREDBox{vm: vm})
	sens := chstore.DefaultAnomalySensitivity()
	sens.REDSourceEnv = os.Getenv("COREMETRY_TEST_VM_ENV")
	now := time.Now()
	var v *vmREDView
	for tick := 1; tick <= 12; tick++ {
		start := time.Now()
		v = src.prepare(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets)
		t.Logf("tik %d: %d sorgu (%d hata) %v, %d kova okundu, bekleyen %d, kova belleği %d, servis %d, son hata %q",
			tick, v.queries, v.queryErrs, time.Since(start).Round(time.Millisecond), v.bucketsFetched, v.pending, len(src.store.frames), len(src.store.names), v.lastErr)
		if v.pending == 0 {
			break
		}
	}
	for _, m := range []string{"request_rate", "error_rate", "p99_ms"} {
		s, u := v.seriesFor(svc, m, sens.DwellBuckets)
		t.Logf("%s: %s · ardışık %d nokta (son %v) · mevsimsel %d", m, redSourceEvidenceTR(u), len(s.Values), tail(s.Values, 3), len(s.Seasonal))
	}
	// Sabit durum: bir sonraki 5 dk kovası.
	start := time.Now()
	v = src.prepare(context.Background(), now.Add(5*time.Minute), sens, seasonalDays, seasonalNeighborBuckets)
	t.Logf("sabit durum tiki: %d sorgu %v, %d kova", v.queries, time.Since(start).Round(time.Millisecond), v.bucketsFetched)
}

func tail(xs []float64, n int) []float64 {
	if len(xs) <= n {
		return xs
	}
	return xs[len(xs)-n:]
}
