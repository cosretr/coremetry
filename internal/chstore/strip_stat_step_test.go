package chstore

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/migrations"
)

// v0.10.2480 (operator-reported, prod: Traces şeridi "requests / 1m bucket"
// yazıyor, çubuklar ~10 sn, p99 çizgisi çubuklardan KABA görünüyor).
//
// Hipotez (a)/(b)/(c) bu tabloyla sınanır: şeridin (/traces, servissiz +
// kümesiz, giriş kapsamı `kind IN (server, consumer)`) her istatistiği
// (avg / p50 / p95 / p99), her pencerede, her yolda (dar rollup ya da ham
// spans) ÇİZGİ ADIMINDA okunur — yüzdelik dilim 1 dk'ya sessizce
// kaba(laş)tırılmaz, ileri doldurma yok. Kaynak gerçekten destekliyor:
// dar rollup'ın DÖRT katmanının da tDigest state'i (q_state) var.

// stripEntryFilters — Traces.tsx'in servissiz şerit süzgeci (giriş kapsamı).
func stripEntryFilters() []FilterExpr {
	return []FilterExpr{{Key: "kind", Op: "IN", Values: []string{"server", "consumer"}}}
}

func TestStripStatStepPerWindowStatPath(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	windows := []struct {
		name string
		win  time.Duration
		want int // çizgi adımı (sn), 240 nokta bütçesi
		tier string
	}{
		{"15m", 15 * time.Minute, 10, "rollup_spans_narrow_10s"},
		{"1h", time.Hour, 20, "rollup_spans_narrow_10s"},
		{"3h", 3 * time.Hour, 60, "rollup_spans_narrow_1m"},
		{"24h", 24 * time.Hour, 600, "rollup_spans_narrow_5m"},
	}
	stats := []string{"avg", "p50", "p95", "p99"}
	for _, w := range windows {
		from, to := now.Add(-w.win), now
		for _, stat := range stats {
			for _, path := range []string{"rollup", "raw"} {
				name := fmt.Sprintf("%s/%s/%s", w.name, stat, path)
				f := SpanMetricBatchFilter{
					Filters: stripEntryFilters(),
					From:    from, To: to,
					StepSeconds: StripLineStep(from, to, now, StripLineMaxPoints),
					Aggs: []SpanMetricAggSpec{
						{Name: "count", Aggregation: "count"},
						{Name: "errors", Aggregation: "errors"},
						{Name: "rt", Aggregation: stat, Field: "duration_ms"},
					},
				}
				// QuerySpanMetricMulti'nin adım zinciri — istatistik adımı DEĞİŞTİRMEZ.
				step := raiseStepForWindow(clampSpanMetricStep(f.StepSeconds, from, to, 0), 0)
				if step != w.want {
					t.Errorf("%s: adım %d, beklenen %d", name, step, w.want)
					continue
				}
				// (c) op-MV (operation_summary_5m) şerit şeklini ALMAZ: kind çipi kapısı.
				if _, ok := operationMVGate(nil, f.Filters); ok {
					t.Errorf("%s: şerit op-MV'ye düşmemeli", name)
				}
				if path == "raw" {
					// Ham yol adımı aynen kullanır (toStartOfInterval step); ek kapı yok.
					continue
				}
				q, ok := narrowRollupEligible(f)
				if !ok {
					t.Errorf("%s: dar rollup'a uygun değil", name)
					continue
				}
				tier, ok := pickNarrowRollupTier(step, from, now)
				if !ok || tier.table != w.tier {
					t.Errorf("%s: katman %q (ok=%v), beklenen %q", name, tier.table, ok, w.tier)
					continue
				}
				// (a)/(b) yüzdelik dilim katmanın KENDİ state'inden, ÇİZGİ adımında
				// birleşir; 60 sn'ye kenetleme ya da WITH FILL (ileri doldurma) yok.
				sql, _ := narrowRollupSQL(tier.table, step, q, needsQuantiles(f.Aggs), from, to, 0)
				if !strings.Contains(sql, fmt.Sprintf("INTERVAL %d SECOND", step)) {
					t.Errorf("%s: SQL adım %d ile kovalamıyor:\n%s", name, step, sql)
				}
				if stat != "avg" && !strings.Contains(sql, "quantilesTDigestMerge(0.5, 0.95, 0.99)(q_state)") {
					t.Errorf("%s: yüzdelik dilim katman state'inden okunmuyor", name)
				}
				if strings.Contains(strings.ToUpper(sql), "WITH FILL") {
					t.Errorf("%s: ileri doldurma (WITH FILL) yasak", name)
				}
			}
		}
	}
}

// Çizgi istatistikleri boş kovada SIFIR/önceki değerle doldurulmaz (yalnız
// pencereli modda sayım-sınıfı seriler sıfırlanır; şerit pencere göndermez).
func TestStripStatNoForwardFill(t *testing.T) {
	for _, stat := range []string{"avg", "p50", "p95", "p99"} {
		if spanAggZeroFills(stat) {
			t.Errorf("%s: gecikme serisi doldurulmamalı (boşluk = boşluk)", stat)
		}
	}
	if _, k := spanMetricWindow(0, 10); k != 0 {
		t.Errorf("şerit pencere göndermez: winK=%d, beklenen 0", k)
	}
}

// Hipotez (a) — "10 sn katmanı yalnız sayım taşır, yüzdelik dilim 1 dk'da":
// YANLIŞ. Dar zincirin dört katmanının hepsi q_state (tDigest) taşır.
func TestNarrowRollupTiersCarryQuantileState(t *testing.T) {
	b, err := migrations.FS.ReadFile("0001_rollup_narrow.sql")
	if err != nil {
		t.Fatal(err)
	}
	ddl := string(b)
	for _, tier := range narrowTiers {
		re := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + tier.table + `_local\b.*?\)\s*ENGINE`)
		body := re.FindString(ddl)
		if body == "" {
			t.Errorf("%s_local DDL bulunamadı", tier.table)
			continue
		}
		if !strings.Contains(body, "q_state") || !strings.Contains(body, "quantilesTDigest(0.5, 0.95, 0.99)") {
			t.Errorf("%s: q_state (tDigest) yok — yüzdelik dilim bu katmanda okunamazdı", tier.table)
		}
	}
}
