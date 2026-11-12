package ml

// v0.10.2063 — Davis S2 (docs/anomaly/davis-ml-gap.md satır 5, 7):
// dependency_error_rate. Sözleşme: kaynak VM client histogramı
// (_count{error_type!=""} / _count) — db_summary_5m / messaging_summary_5m
// DEĞİL (başarılı span %5 örneklenir, oran ~20 kat şişer). Etiket yoksa
// "ölçülemedi" (sağlıklı da anomali de değil); hatasız çift = oran 0; olay
// için oran ≥ %1 ve kovada ≥ 5 hatalı çağrı.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestDepErrQueryAndFamilies(t *testing.T) {
	var db, http depFamily
	for _, f := range depFamilies {
		switch f.ID {
		case "db":
			db = f
		case "http":
			http = f
		}
	}
	if !db.ErrorRate || http.ErrorRate {
		t.Fatalf("hata oranı yalnız db + messaging: db=%v http=%v", db.ErrorRate, http.ErrorRate)
	}
	q := depErrQuery(db, "service_name", "db_namespace", 300)
	want := `sum by (service_name, db_namespace) (rate(db_client_operation_duration_seconds_count{error_type!=""}[300s]))`
	if q != want {
		t.Fatalf("sorgu:\n%s\nbeklenen\n%s", q, want)
	}
	if strings.Contains(q, "summary") {
		t.Fatal("örneklenmiş CH özet tablosu okunmaz")
	}
}

func TestDepErrRatios(t *testing.T) {
	count := map[string]float64{"orders-api\x00orders_db": 10, "orders-api\x00cache": 4, "billing\x00ledger": 0}
	errs := map[string]float64{"orders-api\x00orders_db": 0.5, "other\x00x": 3}
	got := depErrRatios(count, errs)
	if got["orders-api\x00orders_db"] != 0.05 {
		t.Fatalf("oran: %v", got)
	}
	if v, ok := got["orders-api\x00cache"]; !ok || v != 0 {
		t.Fatalf("hatasız seçili çift oran 0 olmalı: %v", got)
	}
	if _, ok := got["billing\x00ledger"]; ok {
		t.Fatal("trafiksiz çift değerlendirilmez")
	}
	if _, ok := got["other\x00x"]; ok {
		t.Fatal("seçim dışı (topk) çift yazılmaz")
	}
}

func TestDepErrFire(t *testing.T) {
	up := func(obs float64) Judgement { return Judgement{Fire: true, Direction: "up", Observed: obs} }
	for _, c := range []struct {
		name string
		j    Judgement
		eps  float64 // hatalı çağrı/sn
		want bool
	}{
		{"%4,2 ve 60 hata", up(0.042), 0.2, true},
		{"oran %1 altı", up(0.009), 1, false},
		{"tam %1, tam 5 hata", up(0.01), 5.0 / 300, true},
		{"4 hata", up(0.5), 4.0 / 300, false},
		{"karar yok", Judgement{}, 1, false},
		{"aşağı yön", Judgement{Fire: true, Direction: "down", Observed: 0.2}, 1, false},
	} {
		if got := depErrFire(c.j, c.eps, 300); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestDepUnmeasuredNote(t *testing.T) {
	if n := depUnmeasuredNote(nil, true); n != "" {
		t.Fatalf("hepsi ölçüldü: %q", n)
	}
	if n := depUnmeasuredNote([]string{"db", "messaging"}, false); !strings.HasPrefix(n, "ölçülemedi: db, messaging") {
		t.Fatalf("hiç ölçülmedi: %q", n)
	}
	if n := depUnmeasuredNote([]string{"messaging"}, true); !strings.HasPrefix(n, "kısmen ölçülüyor: messaging") {
		t.Fatalf("kısmi: %q", n)
	}
	d := NewDependency()
	d.setUnmeasured(true, "ölçülemedi: x")
	if d.Unmeasured()[detDependencyErrorRate] != "ölçülemedi: x" {
		t.Fatal("not dedektöre yazılmalı")
	}
	d.setUnmeasured(false, "ölçülemedi: x")
	if d.Unmeasured() != nil {
		t.Fatal("dedektör kapalıyken not yok")
	}
}

// Detay grafiği dedektörle aynı oranı çizer (yüzde); http ailesinde yok.
func TestDepErrSourceQueryAndSentence(t *testing.T) {
	q, ok := BuildSourceQuery(chstore.DefaultMLConfig(), detDependencyErrorRate, "db|orders_db", "orders-api", "service_name", PodScope{})
	if !ok || q.Unit != "%" || !strings.Contains(q.Expr, `error_type=~".+"`) || !strings.Contains(q.Expr, `db_namespace="orders_db"`) ||
		!strings.Contains(q.Expr, `service_name="orders-api"`) || !strings.Contains(q.Expr, "or vector(0)") {
		t.Fatalf("ifade: %+v", q)
	}
	if _, ok := BuildSourceQuery(chstore.DefaultMLConfig(), detDependencyErrorRate, "http|api.acme.com", "orders-api", "service_name", PodScope{}); ok {
		t.Fatal("http ailesinde hata oranı yok")
	}
	in := CardInput{Detector: detDependencyErrorRate, Signal: chstore.MLSignalTrace, Service: "orders-api", Code: "db|orders_db",
		Observed: 0.042, Baseline: 0.001, Direction: "up", BucketSec: 300}
	s := howMuchOf(in, specFor(in), in.StartedAt)
	if !strings.HasPrefix(s, "db|orders_db çağrılarının %4,2") || !strings.Contains(s, "normalde") {
		t.Fatalf("cümle: %q", s)
	}
}

// Varsayılan açık (operatör 2026-11-12); eventPart oran serisini çizer.
func TestDepErrDefaultsAndPart(t *testing.T) {
	if !chstore.DefaultMLConfig().Detectors[detDependencyErrorRate] || !chstore.NormalizeMLConfig(chstore.MLConfig{}).Detectors[detDependencyErrorRate] {
		t.Fatal("dependency_error_rate varsayılan açık (eski blobda anahtar yok = açık)")
	}
	if p := eventPart(chstore.MLEvent{Signal: chstore.MLSignalTrace, Detector: detDependencyErrorRate, Code: "db|orders_db"}); p != "dep|db|orders_db|err_ratio" {
		t.Fatalf("part: %s", p)
	}
	in := CardInput{Detector: detDependencyErrorRate, Signal: chstore.MLSignalTrace, Service: "orders-api", Code: "db|orders_db", Observed: 0.042, Baseline: 0.001, Direction: "up"}
	sp := specFor(in)
	if sp.unit != unitRatio || !strings.Contains(whatOf(in, sp), "db|orders_db") {
		t.Fatalf("kart: %+v / %q", sp, whatOf(in, sp))
	}
}
