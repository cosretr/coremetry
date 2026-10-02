package chstore

// anomaly_sensitivity_batch_test.go — v0.10.1039: batch servis yüklemi.
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// Üç sözleşme pinlenir:
//  1. IsBatchService (TEK yüklem): büyük-küçük, alt dizgi konumu, kısa
//     kalıp, boş liste = kapalı, alan yok = varsayılan, tavan, tekrar.
//  2. "Yok" ile "boş" JSON gidiş-dönüşünde AYRI kalır: eski blob (alan yok)
//     varsayılanı alır; açıkça boş liste Normalize + kayıt + okuma
//     turlarından sonra da KAPALI kalır (null tuzağı).
//  3. Go ↔ SQL: BatchServiceSQL aynı fikstür tablosunda IsBatchService ile
//     aynı cevabı verir (`clickhouse` ikilisi PATH'teyse canlı CH ile).

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func batchCfg(p ...string) AnomalySensitivityConfig {
	if p == nil {
		p = []string{}
	}
	return AnomalySensitivityConfig{BatchServicePatterns: &p}
}

func TestIsBatchServiceTable(t *testing.T) {
	absent := AnomalySensitivityConfig{} // alan yazılmamış (eski satır)
	tests := []struct {
		name string
		cfg  AnomalySensitivityConfig
		svc  string
		want bool
	}{
		// ── alan YOK → varsayılan ["-batch"] ──
		{"alan yok + -batch sonda", absent, "orders-batch", true},
		{"alan yok + -batch ortada", absent, "orders-batch-worker", true},
		{"alan yok + büyük harf", absent, "ORDERS-BATCH", true},
		{"alan yok + karışık harf", absent, "Orders-Batch", true},
		{"alan yok + batch ama tire yok", absent, "ordersbatch", false},
		{"alan yok + alt çizgi", absent, "orders_batch", false},
		{"alan yok + batch başta (tire önde değil)", absent, "batch-orders", false},
		{"alan yok + batch'i içeren daha uzun sözcük", absent, "nightly-batchjob", true},
		{"alan yok + ilgisiz servis", absent, "payments-api", false},
		{"alan yok + boş ad", absent, "", false},

		// ── açık liste ──
		{"operatör kalıbı başta", batchCfg("etl-"), "etl-loader", true},
		{"operatör kalıbı büyük yazılmış", batchCfg("  ETL-  "), "etl-loader", true},
		{"operatör listesi varsayılanı DEĞİŞTİRİR", batchCfg("etl-"), "orders-batch", false},
		{"iki kalıp, ikincisi eşleşir", batchCfg("etl-", "-cron"), "report-cron", true},

		// ── boş liste = KURAL KAPALI ──
		{"boş liste → batch adı da sıradan", batchCfg(), "orders-batch", false},
		// Kısa kalıplar düşer; geriye bir şey kalmazsa kural kapalı. "ab"
		// gibi bir kalıp filonun yarısını yutardı.
		{"yalnız kısa kalıplar → kapalı", batchCfg("", " ", "-", "ab"), "ab-batch", false},
		{"kısa kalıp düşer, uzun kalır", batchCfg("ab", "-batch"), "ab-service", false},
		{"kısa kalıp düşer, uzun kalır (eşleşme)", batchCfg("ab", "-batch"), "x-batch", true},
		{"tam 3 karakter geçerli", batchCfg("etl"), "etl", true},

		// ── ASCII dışı: yalnız ASCII harfler katlanır (SQL ikiziyle aynı) ──
		{"ASCII dışı ad, ASCII kalıp", absent, "ÖDEME-BATCH", true},
		{"ASCII dışı kalıp birebir", batchCfg("ödeme"), "ödeme-api", true},
		{"ASCII dışı harf KATLANMAZ", batchCfg("ödeme"), "ÖDEME-api", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsBatchService(tt.svc); got != tt.want {
				t.Fatalf("IsBatchService(%q) = %v, beklenen %v (liste %v)",
					tt.svc, got, tt.want, tt.cfg.BatchServicePatternList())
			}
		})
	}
}

func TestNormalizeBatchServicePatterns(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil → boş (nil DEĞİL)", nil, []string{}},
		{"kırp + küçült", []string{"  -BATCH "}, []string{"-batch"}},
		{"tekrar düşer, ilk kalır, sıra korunur", []string{"-cron", "-batch", "-CRON", " -batch"}, []string{"-cron", "-batch"}},
		{"kısa kalıplar düşer", []string{"", "a", "ab", "abc"}, []string{"abc"}},
		{"3 rune: ASCII dışı da sayılır", []string{"öçş"}, []string{"öçş"}},
		{"tavan 10", []string{"p01", "p02", "p03", "p04", "p05", "p06", "p07", "p08", "p09", "p10", "p11", "p12"},
			[]string{"p01", "p02", "p03", "p04", "p05", "p06", "p07", "p08", "p09", "p10"}},
		{"tavan tekrardan SONRA sayılır", []string{"p01", "p01", "p01", "p02"}, []string{"p01", "p02"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeBatchServicePatterns(tt.in)
			if got == nil {
				t.Fatal("nil döndü — boş liste nil'e dönerse JSON'da null yazılır ve kural geri açılır")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q, beklenen %q", got, tt.want)
			}
		})
	}
}

// TestBatchPatternsJSONRoundTrip — "yok" ile "boş" kayıt-okuma turlarında
// ayrı kalmalı. PUT = Normalize + Marshal (SaveAnomalySensitivity) ; GET =
// Unmarshal + Normalize (GetAnomalySensitivity).
func TestBatchPatternsJSONRoundTrip(t *testing.T) {
	putGet := func(t *testing.T, in AnomalySensitivityConfig) (AnomalySensitivityConfig, string) {
		t.Helper()
		raw, err := json.Marshal(NormalizeAnomalySensitivity(in))
		if err != nil {
			t.Fatal(err)
		}
		var back AnomalySensitivityConfig
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		return NormalizeAnomalySensitivity(back), string(raw)
	}

	t.Run("eski blob (alan yok) → varsayılan", func(t *testing.T) {
		var old AnomalySensitivityConfig
		if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6}`), &old); err != nil {
			t.Fatal(err)
		}
		if old.BatchServicePatterns != nil {
			t.Fatal("alan olmayan blob nil olmayan işaretçi üretti")
		}
		n := NormalizeAnomalySensitivity(old)
		if n.BatchServicePatterns == nil || !reflect.DeepEqual(*n.BatchServicePatterns, []string{"-batch"}) {
			t.Fatalf("Normalize varsayılanı somutlaştırmadı: %v", n.BatchServicePatterns)
		}
		if !n.IsBatchService("orders-batch") {
			t.Fatal("eski blob'da -batch servisi kurala girmedi")
		}
	})

	t.Run("açık null → alan yok gibi (varsayılan)", func(t *testing.T) {
		var c AnomalySensitivityConfig
		if err := json.Unmarshal([]byte(`{"batchServicePatterns":null}`), &c); err != nil {
			t.Fatal(err)
		}
		if !c.IsBatchService("orders-batch") {
			t.Fatal("null varsayılan olarak okunmadı")
		}
	})

	t.Run("açık boş liste → KAPALI ve kapalı KALIR", func(t *testing.T) {
		var in AnomalySensitivityConfig
		if err := json.Unmarshal([]byte(`{"batchServicePatterns":[]}`), &in); err != nil {
			t.Fatal(err)
		}
		if in.IsBatchService("orders-batch") {
			t.Fatal("açık boş liste kapalı okunmadı")
		}
		got, raw := putGet(t, in)
		if !strings.Contains(raw, `"batchServicePatterns":[]`) {
			t.Fatalf("kaydedilen blob boş listeyi [] olarak taşımıyor: %s", raw)
		}
		if got.IsBatchService("orders-batch") {
			t.Fatal("PUT+GET turundan sonra kural geri AÇILDI (null tuzağı)")
		}
		// İkinci tur: bir kez daha kaydet-oku.
		got2, _ := putGet(t, got)
		if got2.IsBatchService("orders-batch") || got2.BatchServicePatterns == nil || len(*got2.BatchServicePatterns) != 0 {
			t.Fatalf("ikinci turda kapalı kalmadı: %v", got2.BatchServicePatterns)
		}
	})

	t.Run("yalnız kısa kalıplar → normalize sonrası boş = kapalı, null değil", func(t *testing.T) {
		got, raw := putGet(t, batchCfg("ab", " "))
		if !strings.Contains(raw, `"batchServicePatterns":[]`) || got.IsBatchService("ab-batch") {
			t.Fatalf("kısa kalıplar sonrası boş liste kapalı kalmadı: %s", raw)
		}
	})

	t.Run("operatör listesi korunur", func(t *testing.T) {
		got, _ := putGet(t, batchCfg(" ETL- ", "-batch", "-batch"))
		if want := []string{"etl-", "-batch"}; !reflect.DeepEqual(got.BatchServicePatternList(), want) {
			t.Fatalf("got %q, beklenen %q", got.BatchServicePatternList(), want)
		}
	})

	t.Run("işaretçi nil dilim → yine [] yazılır", func(t *testing.T) {
		var nilSlice []string
		got, raw := putGet(t, AnomalySensitivityConfig{BatchServicePatterns: &nilSlice})
		if strings.Contains(raw, `"batchServicePatterns":null`) || got.IsBatchService("orders-batch") {
			t.Fatalf("nil dilime işaretçi null yazıldı ya da varsayılana döndü: %s", raw)
		}
	})
}

// batchFixtureNames — Go ↔ SQL fikstürü. Sentetik adlar.
var batchFixtureNames = []string{
	"orders-batch", "ORDERS-BATCH", "Orders-Batch-Worker", "nightly-batchjob",
	"batch-orders", "ordersbatch", "orders_batch", "payments-api",
	"etl-loader", "ETL-LOADER", "report-cron", "ÖDEME-BATCH", "ödeme-api",
	"ÖDEME-api", "İşlem-BATCH", "ışlem-batch", "x", "",
}

// TestBatchServiceSQLShape — her ortamda koşar: koşul AYNI etkin listeden
// üretilir, kalıp başına tam bir terim, argümanlar normalize kalıplar.
func TestBatchServiceSQLShape(t *testing.T) {
	c := batchCfg("-batch", " ETL- ", "-batch", "ab")
	cond, args := c.BatchServiceSQL("service_name")
	want := "(positionCaseInsensitive(service_name, ?) > 0 OR positionCaseInsensitive(service_name, ?) > 0)"
	if cond != want {
		t.Fatalf("koşul\n%s\nbeklenen\n%s", cond, want)
	}
	if !reflect.DeepEqual(args, []any{"-batch", "etl-"}) {
		t.Fatalf("argümanlar %v — normalize liste olmalı", args)
	}
	if strings.Count(cond, "?") != len(args) {
		t.Fatal("yer tutucu sayısı argüman sayısına eşit değil")
	}
	// Kalıp SQL metnine GÖMÜLMEZ (bind).
	if strings.Contains(cond, "batch") || strings.Contains(cond, "etl") {
		t.Fatal("kalıp SQL metnine gömülmüş")
	}
	// Varsayılan (alan yok) tek terim.
	if cond, args := (AnomalySensitivityConfig{}).BatchServiceSQL("service_name"); strings.Count(cond, "?") != 1 || args[0] != "-batch" {
		t.Fatalf("varsayılan koşul %q %v", cond, args)
	}
	// Boş liste → koşul YOK.
	if cond, args := batchCfg().BatchServiceSQL("service_name"); cond != "" || args != nil {
		t.Fatalf("boş liste koşul üretti: %q %v", cond, args)
	}
}

// TestBatchServiceSQLAgreesWithGo — fikstür tablosunda SQL ve Go AYNI
// servisleri batch der. Canlı motor gerektirir: `clickhouse` ikilisi
// (clickhouse local) PATH'te değilse atlanır.
func TestBatchServiceSQLAgreesWithGo(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — Go↔SQL fikstür karşılaştırması atlandı (şekil testi yine koşar)")
	}
	for _, c := range []AnomalySensitivityConfig{
		{}, // varsayılan
		batchCfg("-batch", "ETL-", "ödeme", "-cron"),
		batchCfg("işlem"),
	} {
		cond, args := c.BatchServiceSQL("service_name")
		if cond == "" {
			t.Fatal("bu fikstürlerde koşul boş olmamalı")
		}
		vals := make([]string, len(batchFixtureNames))
		for i, n := range batchFixtureNames {
			vals[i] = "(" + chQuote(n) + ")"
		}
		q := "SELECT service_name, toUInt8(" + inlineArgs(t, cond, args) + ") FROM values('service_name String', " +
			strings.Join(vals, ", ") + ") FORMAT TSVRaw"
		out, err := exec.Command(bin, "local", "--query", q).CombinedOutput()
		if err != nil {
			t.Fatalf("clickhouse local: %v\n%s\nsorgu: %s", err, out, q)
		}
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(lines) != len(batchFixtureNames) {
			t.Fatalf("%d satır, beklenen %d:\n%s", len(lines), len(batchFixtureNames), out)
		}
		for i, ln := range lines {
			parts := strings.Split(ln, "\t")
			if len(parts) != 2 || parts[0] != batchFixtureNames[i] {
				t.Fatalf("satır %d beklenmedik: %q", i, ln)
			}
			sqlSays := parts[1] == "1"
			if goSays := c.IsBatchService(parts[0]); goSays != sqlSays {
				t.Errorf("liste %v, servis %q: Go=%v SQL=%v — iki taraf farklı küme diyor",
					c.BatchServicePatternList(), parts[0], goSays, sqlSays)
			}
		}
	}
}

// chQuote — CH dizgi değişmezi (yalnız test: sürücünün istemci tarafı
// bind'inin karşılığı).
func chQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// inlineArgs — `?` yer tutucularını sırayla değişmezlerle doldurur.
func inlineArgs(t *testing.T, q string, args []any) string {
	t.Helper()
	var b strings.Builder
	i := 0
	for _, r := range q {
		if r != '?' {
			b.WriteRune(r)
			continue
		}
		if i >= len(args) {
			t.Fatalf("argüman eksik: %q", q)
		}
		switch v := args[i].(type) {
		case string:
			b.WriteString(chQuote(v))
		default:
			b.WriteString(fmt.Sprint(v))
		}
		i++
	}
	if i != len(args) {
		t.Fatalf("fazla argüman: %d/%d", i, len(args))
	}
	return b.String()
}
