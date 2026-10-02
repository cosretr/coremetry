package anomaly

// log_template_new_switch_test.go — v0.10.1061: log_template_new dedektörü
// varsayılan KAPALI (anomaly_sensitivity.logTemplateNew, nil = kapalı).
//
// Operatör onaylı (prod): "Bu log anomalileri de false pozitif geliyor."
// Drain'in "ilk kez gördüğü" biçimler yanlış alarm üretiyordu. Öbür log
// anomalisi `log_pattern` (seçilmiş desen sıçraması) operatörce doğru
// yakalama sayıldı ve anahtara BAĞLI DEĞİL. v0.10.1056 op_latency_switch_test
// emsalinin birebir şekli.
//
// NE ÇİVİLİYOR:
//   - recorder adımı kapalıyken dedektörü HİÇ çağırmaz ve upsert yazmaz;
//     açıkken olay alanları v0.10.1061 öncesiyle birebir;
//   - dedektörün KENDİSİ kapalıyken her G/Ç'den önce boş liste döner (aday ve
//     bilinen-şablon okuması yok) — her çağıran uyar; açıkken G/Ç'ye ulaşır;
//   - recorder tiki anahtarı atomic ayardan okuyup adıma verir; log_pattern
//     adımı ve templater anahtara dokunmaz.

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

func TestRecordNewLogTemplatesSwitch(t *testing.T) {
	hits := []LogTemplateAnomaly{
		{TemplateID: "tpl-1", Template: "user <*> logged in", Service: "svc-a", FirstSeenNs: 10, LastSeenNs: 42, TotalCount: 7, Sample: "user 7 logged in"},
		{TemplateID: "tpl-2", Template: "queue <*> drained", Service: "svc-b", FirstSeenNs: 11, LastSeenNs: 43, TotalCount: 4, Sample: "queue q1 drained"},
	}
	run := func(on bool, detErr error) (int, []chstore.AnomalyEvent) {
		calls := 0
		var got []chstore.AnomalyEvent
		recordNewLogTemplates(context.Background(), on,
			func(context.Context) ([]LogTemplateAnomaly, error) {
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
			{ID: chstore.FingerprintAnomaly("log_template_new", "tpl-1", "svc-a"), Kind: "log_template_new",
				Pattern: "user <*> logged in", Service: "svc-a", StartedAt: 10, LastSeen: 42,
				CurrentRatio: 0, CurrentCount: 7, Sample: "user 7 logged in"},
			{ID: chstore.FingerprintAnomaly("log_template_new", "tpl-2", "svc-b"), Kind: "log_template_new",
				Pattern: "queue <*> drained", Service: "svc-b", StartedAt: 11, LastSeen: 43,
				CurrentRatio: 0, CurrentCount: 4, Sample: "queue q1 drained"},
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

// TestDetectNewLogTemplatesOffDoesNoIO — sıfır değerli Store'un bağlantısı
// nil: log_templates okuması panikler. Kapalıyken dedektör paniklemeden BOŞ
// liste (nil değil) ve nil hata döner; açıkken G/Ç'ye ulaşır.
func TestDetectNewLogTemplatesOffDoesNoIO(t *testing.T) {
	ctx := context.Background()
	on, off := true, false

	cases := []struct {
		name    string
		publish *chstore.AnomalySensitivityConfig // nil = hiç yayınlanmamış (varsayılan)
	}{
		{"hiç yayınlanmamış ayar (varsayılan)", nil},
		{"eski blob, alan yok (nil)", &chstore.AnomalySensitivityConfig{}},
		{"açıkça false", &chstore.AnomalySensitivityConfig{LogTemplateNew: &off}},
		{"opLatency açık, logTemplateNew yok — komşu anahtar sürüklemez", &chstore.AnomalySensitivityConfig{OpLatency: &on}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &chstore.Store{}
			if tc.publish != nil {
				s.SetAnomalySensitivity(*tc.publish)
			}
			var got []LogTemplateAnomaly
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("kapalıyken dedektör G/Ç yaptı (panik: %v)", r)
					}
				}()
				got, err = DetectNewLogTemplates(ctx, s, 10*time.Minute)
			}()
			if err != nil || got == nil || len(got) != 0 {
				t.Fatalf("kapalı: got=%v (nil=%v) err=%v — boş liste + nil hata bekleniyordu", got, got == nil, err)
			}
		})
	}

	t.Run("açık → G/Ç'ye ulaşır", func(t *testing.T) {
		s := &chstore.Store{}
		s.SetAnomalySensitivity(chstore.AnomalySensitivityConfig{LogTemplateNew: &on})
		reached := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					reached = true // nil bağlantıda sorgu = G/Ç denendi
				}
			}()
			if _, err := DetectNewLogTemplates(ctx, s, 10*time.Minute); err != nil {
				reached = true
			}
		}()
		if !reached {
			t.Fatal("açıkken dedektör hiçbir sorgu denemedi — kapı her şeyi susturuyor")
		}
	})
}

// TestLogTemplateNewSwitchWiring — kaynak pini: tik anahtarı atomic ayardan
// okuyup adıma verir (sabit true DEĞİL); dedektörün kapısı her G/Ç'den önce;
// log_pattern adımı ve templater anahtara bağlı değil.
func TestLogTemplateNewSwitchWiring(t *testing.T) {
	strip := regexp.MustCompile(`(?m)^\s*//.*$`)
	read := func(f string) string {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		return strip.ReplaceAllString(string(b), "")
	}

	rec := read("recorder.go")
	if !strings.Contains(rec, "recordNewLogTemplates(ctx, r.store.AnomalySensitivityForDetectors().LogTemplateNewOn(),") {
		t.Fatal("recorder tiki yeni-şablon adımını anahtarla çağırmıyor")
	}
	if strings.Count(rec, "DetectNewLogTemplates(") != 1 {
		t.Fatal("recorder dedektörü adımın dışında da çağırıyor")
	}
	i := strings.Index(rec, "func recordNewLogTemplates(")
	if i < 0 {
		t.Fatal("recordNewLogTemplates yok")
	}
	step := rec[i:]
	step = step[:strings.Index(step, "\n}\n")]
	if g, d := strings.Index(step, "if !on {"), strings.Index(step, "detect(ctx)"); g < 0 || d < 0 || g > d {
		t.Fatal("adımın kapısı dedektör çağrısından önce değil")
	}

	// log_pattern adımı tikte koşulsuz kalır (operatör: doğru yakalama).
	tick := rec[strings.Index(rec, "func (r *Recorder) tick("):]
	tick = tick[:strings.Index(tick, "\n}\n")]
	lp := strings.Index(tick, "DetectLogPatterns(logCtx, r.logs, r.window)")
	if lp < 0 {
		t.Fatal("log_pattern adımı tikten çıkmış")
	}
	if strings.Contains(tick[:lp], "LogTemplateNewOn()") {
		t.Fatal("log_pattern adımı yeni-şablon anahtarının arkasına girmiş")
	}
	if strings.Contains(read("log_patterns.go"), "LogTemplateNew") {
		t.Fatal("log_pattern dedektörü yeni-şablon anahtarını okuyor")
	}
	for _, f := range []string{"../templater/puller.go", "../templater/drain.go"} {
		if strings.Contains(read(f), "LogTemplateNew") {
			t.Fatalf("templater (%s) anahtara bağlanmış — log_templates defteri yazılmaya devam etmeli", f)
		}
	}

	src := read("log_templates.go")
	j := strings.Index(src, "func DetectNewLogTemplates(")
	if j < 0 {
		t.Fatal("DetectNewLogTemplates bulunamadı")
	}
	body := src[j:]
	body = body[:strings.Index(body, "\n}\n")]
	gate := strings.Index(body, "if !store.AnomalySensitivityForDetectors().LogTemplateNewOn() {")
	core := strings.Index(body, "detectNewLogTemplates(ctx, store, window, time.Now())")
	if gate < 0 || core < 0 || gate > core {
		t.Fatalf("dedektör kapısı log_templates okumasından önce değil (kapı=%d, çekirdek=%d)", gate, core)
	}
}
