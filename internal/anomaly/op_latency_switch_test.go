package anomaly

// op_latency_switch_test.go — v0.10.1056: trace_op_latency dedektörü
// varsayılan KAPALI (anomaly_sensitivity.opLatency, nil = kapalı).
//
// Operatör (prod, "Operasyon gecikmesi" anomali detayı, iki örnek): "Trace op
// latency false pozitif geliyor, gerek yok gelmelerine bence." Normalde
// milisaniyenin altında / ~20 ms p99'lu operasyonlarda tek kovalık 200 ms ve
// 350 ms sıçramalar "gecikme normalin 3.6 / 4 katına çıktı" diye yineleniyordu.
//
// NE ÇİVİLİYOR:
//   - recorder adımı kapalıyken dedektörü HİÇ çağırmaz ve upsert yazmaz;
//     açıkken olay alanları v0.10.1056 öncesiyle birebir;
//   - dedektörün KENDİSİ kapalıyken her G/Ç'den önce boş liste döner (MV
//     sorgusu yok, v0.10.1046 aktif-olay okuması yok) — her çağıran uyar;
//     açıkken G/Ç'ye ulaşır (kapı her şeyi susturmuyor);
//   - recorder tiki anahtarı atomic ayardan okuyup adıma verir.

import (
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestRecordOpLatencySwitch(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	hits := []OpLatencyAnomaly{
		{Service: "svc-a", Operation: "GET /x", CurP99Ms: 900, BaseP99Ms: 100, Ratio: 9, CurCalls: 500, SampleTraceID: "t1", LastSeenNs: 42},
		{Service: "svc-b", Operation: "POST /y", CurP99Ms: 600, BaseP99Ms: 150, Ratio: 4, CurCalls: 80, SampleTraceID: "t2", LastSeenNs: 43},
	}
	run := func(on bool, detErr error) (int, []chstore.AnomalyEvent) {
		calls := 0
		var got []chstore.AnomalyEvent
		recordOpLatency(context.Background(), on, now,
			func(context.Context) ([]OpLatencyAnomaly, error) {
				calls++
				return hits, detErr
			},
			func(_ context.Context, ev chstore.AnomalyEvent) error {
				got = append(got, ev)
				return nil
			})
		return calls, got
	}

	t.Run("kapalı → dedektör çağrılmaz, upsert yok", func(t *testing.T) {
		calls, got := run(false, nil)
		if calls != 0 || len(got) != 0 {
			t.Fatalf("kapalıyken dedektör %d kez çağrıldı, %d upsert", calls, len(got))
		}
	})

	t.Run("açık → bir çağrı, olay alanları birebir", func(t *testing.T) {
		calls, got := run(true, nil)
		if calls != 1 {
			t.Fatalf("dedektör %d kez çağrıldı, 1 bekleniyordu", calls)
		}
		want := []chstore.AnomalyEvent{
			{ID: chstore.FingerprintAnomaly("trace_op_latency", "GET /x", "svc-a"), Kind: "trace_op_latency",
				Pattern: "GET /x", Service: "svc-a", StartedAt: now.UnixNano(), LastSeen: 42,
				CurrentRatio: 9, CurrentCount: 500, Sample: "t1"},
			{ID: chstore.FingerprintAnomaly("trace_op_latency", "POST /y", "svc-b"), Kind: "trace_op_latency",
				Pattern: "POST /y", Service: "svc-b", StartedAt: now.UnixNano(), LastSeen: 43,
				CurrentRatio: 4, CurrentCount: 80, Sample: "t2"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("olaylar:\n got %+v\nwant %+v", got, want)
		}
	})

	t.Run("açık + dedektör hatası → dönen isabetler yine yazılır (bugünkü davranış)", func(t *testing.T) {
		_, got := run(true, errors.New("ch down"))
		if len(got) != len(hits) {
			t.Fatalf("%d upsert, %d bekleniyordu", len(got), len(hits))
		}
	})
}

// TestDetectOpLatencyOffDoesNoIO — sıfır değerli Store'un bağlantısı nil:
// herhangi bir sorgu (MV, aktif olay, örnek) panikler. Kapalıyken dedektör
// paniklemeden BOŞ liste (nil değil) ve nil hata döner; açıkken G/Ç'ye ulaşır.
func TestDetectOpLatencyOffDoesNoIO(t *testing.T) {
	ctx := context.Background()
	on, off := true, false

	cases := []struct {
		name    string
		publish *chstore.AnomalySensitivityConfig // nil = hiç yayınlanmamış (varsayılan)
	}{
		{"hiç yayınlanmamış ayar (varsayılan)", nil},
		{"eski blob, alan yok (nil)", &chstore.AnomalySensitivityConfig{}},
		{"açıkça false — batch listesi dolu, doğrulanmış", &chstore.AnomalySensitivityConfig{OpLatency: &off}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &chstore.Store{}
			if tc.publish != nil {
				s.SetAnomalySensitivity(*tc.publish)
			}
			var got []OpLatencyAnomaly
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("kapalıyken dedektör G/Ç yaptı (panik: %v)", r)
					}
				}()
				got, err = DetectOpLatencyAnomalies(ctx, s, 5*time.Minute)
			}()
			if err != nil || got == nil || len(got) != 0 {
				t.Fatalf("kapalı: got=%v (nil=%v) err=%v — boş liste + nil hata bekleniyordu", got, got == nil, err)
			}
		})
	}

	t.Run("açık → G/Ç'ye ulaşır", func(t *testing.T) {
		s := &chstore.Store{}
		s.SetAnomalySensitivity(chstore.AnomalySensitivityConfig{OpLatency: &on})
		reached := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					reached = true // nil bağlantıda sorgu = G/Ç denendi
				}
			}()
			if _, err := DetectOpLatencyAnomalies(ctx, s, 5*time.Minute); err != nil {
				reached = true
			}
		}()
		if !reached {
			t.Fatal("açıkken dedektör hiçbir sorgu denemedi — kapı her şeyi susturuyor")
		}
	})
}

// TestOpLatencySwitchWiring — kaynak pini: tik anahtarı atomic ayardan okuyup
// adıma verir (sabit true DEĞİL); dedektörün kapısı her G/Ç'den önce.
func TestOpLatencySwitchWiring(t *testing.T) {
	strip := regexp.MustCompile(`(?m)^\s*//.*$`)
	read := func(f string) string {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		return strip.ReplaceAllString(string(b), "")
	}

	rec := read("recorder.go")
	if !strings.Contains(rec, "recordOpLatency(ctx, r.store.AnomalySensitivityForDetectors().OpLatencyOn(), now,") {
		t.Fatal("recorder tiki op-latency adımını anahtarla çağırmıyor")
	}
	if strings.Count(rec, "DetectOpLatencyAnomalies(") != 1 {
		t.Fatal("recorder dedektörü adımın dışında da çağırıyor")
	}
	i := strings.Index(rec, "func recordOpLatency(")
	if i < 0 {
		t.Fatal("recordOpLatency yok")
	}
	step := rec[i:]
	if g, d := strings.Index(step, "if !on {"), strings.Index(step, "detect(ctx)"); g < 0 || d < 0 || g > d {
		t.Fatal("adımın kapısı dedektör çağrısından önce değil")
	}

	src := read("op_latency.go")
	j := strings.Index(src, "func DetectOpLatencyAnomalies(")
	if j < 0 {
		t.Fatal("DetectOpLatencyAnomalies bulunamadı")
	}
	body := src[j:]
	gate := strings.Index(body, "if !sens.OpLatencyOn() {")
	for _, io := range []string{"store.TelemetryReadConn()", "ListActiveAnomalyKeys(", "conn.Query("} {
		k := strings.Index(body, io)
		if gate < 0 || k < 0 || gate > k {
			t.Fatalf("dedektör kapısı %q'den önce değil (kapı=%d, g/ç=%d)", io, gate, k)
		}
	}
}
