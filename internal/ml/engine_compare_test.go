package ml

// v0.10.2309 — Davis özneleri ↔ ML karşılaştırması (engine_compare.go):
// aile tablosu, yanlı sınıf, ±15 dk sınırı, belirsiz kuralı, episodesExact,
// medyan / kısa, referans Problem öneki, satır tavanı.

import (
	"fmt"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var ecBase = time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)

func ecT(min int) int64 { return ecBase.Add(time.Duration(min) * time.Minute).UnixNano() }

const ecDays = 7

var (
	ecFrom = ecBase.Add(-ecDays * 24 * time.Hour).UnixNano()
	ecTo   = ecBase.Add(24 * time.Hour).UnixNano()
)

func ecSubj(id, svc, kind, pattern string, s, e int) EngineSubjectIn {
	return EngineSubjectIn{ID: id, Service: svc, SubjectKind: kind, Pattern: pattern, Direction: "up",
		Start: ecT(s), End: ecT(e), EpisodeCount: 1, FirstStart: ecT(s)}
}

func ecML(id, det, svc, code string, s, e int) EngineMLIn {
	return EngineMLIn{ID: id, Detector: det, Service: svc, Code: code, Direction: "up", Start: ecT(s), End: ecT(e)}
}

func ecRow(t *testing.T, r EngineCompareResult, svc, fam, src string) EngineCompareRow {
	t.Helper()
	for _, row := range r.Rows {
		if row.Service == svc && row.Family == fam && row.Source == src {
			return row
		}
	}
	t.Fatalf("satır yok: %s/%s/%s (%+v)", svc, fam, src, r.Rows)
	return EngineCompareRow{}
}

func TestEngineCompareFamilyMapping(t *testing.T) {
	ep, q := chstore.AnomalySubjectEndpoint, chstore.AnomalySubjectQueue
	subj := []struct {
		s          EngineSubjectIn
		k          engKey
		biased, cp bool
		ok         bool
	}{
		{ecSubj("a", "checkout", ep, "p99_ms @ /pay", 0, 10), engKey{"checkout", EngFamLatency, EngSrcMetric}, false, true, true},
		{ecSubj("a", "checkout", ep, "p95_ms @ /pay", 0, 10), engKey{"checkout", EngFamLatency, EngSrcMetric}, false, true, true},
		{ecSubj("a", "checkout", ep, "error_rate @ /pay", 0, 10), engKey{"checkout", EngFamErrors, EngSrcMetric}, false, true, true},
		{ecSubj("a", "checkout", ep, "request_rate @ /pay", 0, 10), engKey{"checkout", EngFamThroughput, EngSrcMetric}, false, false, true}, // up → karşılık yok
		{ecSubj("a", "orders", q, "consumer_lag @ queue:kafka@orders.v1", 0, 10), engKey{"orders", EngFamConsumerLag, EngSrcMetric}, false, true, true},
		{ecSubj("a", "queue:kafka@orders.v1", q, "error_rate", 0, 10), engKey{"orders.v1", EngFamErrors, EngSrcSampledTrace}, true, true, true},
		{ecSubj("a", "queue:kafka@orders.v1", q, "p99_ms", 0, 10), engKey{"orders.v1", EngFamLatency, EngSrcSampledTrace}, true, true, true},
		{ecSubj("a", "queue:kafka@orders.v1", q, "request_rate", 0, 10), engKey{"orders.v1", EngFamThroughput, EngSrcSampledTrace}, true, false, true},
		{ecSubj("a", "db:pg@orders", chstore.AnomalySubjectDB, "error_rate", 0, 10), engKey{"db:pg@orders", EngFamErrors, EngSrcSampledTrace}, true, false, true},
		{ecSubj("a", "checkout", ep, "cpu @ /pay", 0, 10), engKey{}, false, false, false},
	}
	for i, c := range subj {
		k, b, cp, ok := subjectSide(c.s)
		if k != c.k || b != c.biased || cp != c.cp || ok != c.ok {
			t.Errorf("özne #%d (%s %s): %v %v %v %v", i, c.s.SubjectKind, c.s.Pattern, k, b, cp, ok)
		}
	}
	down := ecSubj("a", "checkout", ep, "request_rate @ /pay", 0, 10)
	down.Direction = chstore.AnomalyDirectionDown
	if _, _, cp, _ := subjectSide(down); !cp {
		t.Error("request_rate down karşılıksız")
	}
	mls := []struct {
		m      EngineMLIn
		k      engKey
		biased bool
		ok     bool
	}{
		{ecML("m", "op_latency", "checkout", "/pay", 0, 10), engKey{"checkout", EngFamLatency, EngSrcMetric}, false, true},
		{ecML("m", "svc_breadth", "checkout", "", 0, 10), engKey{"checkout", EngFamLatency, EngSrcMetric}, false, true},
		{ecML("m", "op_error_rate", "checkout", "/pay", 0, 10), engKey{"checkout", EngFamErrors, EngSrcMetric}, false, true},
		{ecML("m", "svc_error_rate", "checkout", "", 0, 10), engKey{"checkout", EngFamErrors, EngSrcMetric}, false, true},
		{ecML("m", "trace_error_count", "checkout", "/pay", 0, 10), engKey{"checkout", EngFamErrors, EngSrcMetric}, false, true},
		{ecML("m", "throughput_drop", "checkout", "/pay", 0, 10), engKey{"checkout", EngFamThroughput, EngSrcMetric}, false, true},
		{ecML("m", "metric_kafka", "orders", "metric_kafka|consumer_lag#orders.v1", 0, 10), engKey{"orders", EngFamConsumerLag, EngSrcMetric}, false, true},
		{ecML("m", "metric_kafka", "orders", "metric_kafka|rebalance", 0, 10), engKey{}, false, false},
		{ecML("m", "dependency_latency", "orders", "messaging|orders.v1", 0, 10), engKey{"orders.v1", EngFamLatency, EngSrcSampledTrace}, true, true},
		{ecML("m", "dependency_error_rate", "orders", "messaging|orders.v1", 0, 10), engKey{"orders.v1", EngFamErrors, EngSrcSampledTrace}, true, true},
		{ecML("m", "dependency_latency", "orders", "db|orders_db", 0, 10), engKey{}, false, false},
		{ecML("m", "log_volume", "orders", "", 0, 10), engKey{}, false, false},
	}
	for i, c := range mls {
		k, b, ok := mlSide(c.m)
		if k != c.k || b != c.biased || ok != c.ok {
			t.Errorf("ML #%d (%s %s): %v %v %v", i, c.m.Detector, c.m.Code, k, b, ok)
		}
	}
	// Eşlenen ML dedektörlerinin her biri okuma listesinde.
	read := map[string]bool{}
	for _, d := range chstore.EngineCompareMLDetectors {
		read[d] = true
	}
	for _, c := range mls {
		if c.ok && !read[c.m.Detector] {
			t.Errorf("%s okuma listesinde yok", c.m.Detector)
		}
	}
}

func TestEngineCompareBiasedExcludedFromTotals(t *testing.T) {
	subj := []EngineSubjectIn{
		ecSubj("s1", "checkout", chstore.AnomalySubjectEndpoint, "error_rate @ /pay", 0, 30),
		ecSubj("s2", "queue:kafka@orders.v1", chstore.AnomalySubjectQueue, "error_rate", 0, 30),
	}
	mls := []EngineMLIn{
		ecML("m1", "op_error_rate", "checkout", "/pay", 5, 25),
		ecML("m2", "dependency_error_rate", "orders", "messaging|orders.v1", 5, 25),
	}
	r := CompareEngines(subj, mls, nil, ecDays, ecFrom, ecTo)
	b := ecRow(t, r, "queue:kafka@orders.v1", EngFamErrors, EngSrcSampledTrace)
	if !b.Biased || b.Subject.Matched != 1 || b.ML.Matched != 1 {
		t.Errorf("yanlı satır: %+v", b)
	}
	if r.Totals.Subject.Series != 1 || r.Totals.ML.Events != 1 || r.Totals.Subject.Matched != 1 {
		t.Errorf("toplamlar yanlı satırı saydı: %+v", r.Totals)
	}
}

func TestEngineCompareToleranceBoundary(t *testing.T) {
	// Özne [0, 10] dk; ML tam 15 dk sonra başlar → eşleşir; 15 dk + 1 ns → eşleşmez.
	s := ecSubj("s1", "checkout", chstore.AnomalySubjectEndpoint, "p99_ms @ /pay", 0, 10)
	at := ecML("m1", "op_latency", "checkout", "/pay", 25, 40)
	r := CompareEngines([]EngineSubjectIn{s}, []EngineMLIn{at}, nil, ecDays, ecFrom, ecTo)
	if row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric); row.Subject.Matched != 1 || row.ML.Matched != 1 {
		t.Errorf("tam 15 dk eşleşmedi: %+v", row)
	}
	past := at
	past.Start++
	r = CompareEngines([]EngineSubjectIn{s}, []EngineMLIn{past}, nil, ecDays, ecFrom, ecTo)
	row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric)
	if row.Subject.Only != 1 || row.ML.Only != 1 || row.Examples.OnlySubject[0] != "s1" || row.Examples.OnlyML[0] != "m1" {
		t.Errorf("15 dk + 1 ns eşleşti: %+v", row)
	}
	if engineCompareTolerance != 15*time.Minute {
		t.Errorf("tolerans %v", engineCompareTolerance)
	}
}

func TestEngineCompareIndeterminate(t *testing.T) {
	// Özne satırı 3 bölüm: ilk bölüm ML'den önce, son bölüm ML bitişinden
	// +15 dk sonra başlamış → ML olayının karşılığı ezilmiş olabilir.
	s := ecSubj("s1", "checkout", chstore.AnomalySubjectEndpoint, "p99_ms @ /pay", 300, 320)
	s.EpisodeCount, s.FirstStart = 3, ecT(0)
	m := ecML("m1", "op_latency", "checkout", "/pay", 100, 120)
	r := CompareEngines([]EngineSubjectIn{s}, []EngineMLIn{m}, nil, ecDays, ecFrom, ecTo)
	row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric)
	if row.ML.Indeterminate != 1 || row.ML.Only != 0 || len(row.Examples.OnlyML) != 0 {
		t.Errorf("belirsiz sayılmadı: %+v", row.ML)
	}
	// Tek bölüm → yalnız ML.
	s.EpisodeCount, s.FirstStart = 1, s.Start
	r = CompareEngines([]EngineSubjectIn{s}, []EngineMLIn{m}, nil, ecDays, ecFrom, ecTo)
	if row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric); row.ML.Only != 1 || row.ML.Indeterminate != 0 {
		t.Errorf("tek bölüm: %+v", row.ML)
	}
	// İlk bölüm ML bitişinden de sonra → belirsiz değil.
	s.EpisodeCount, s.FirstStart = 3, ecT(200)
	r = CompareEngines([]EngineSubjectIn{s}, []EngineMLIn{m}, nil, ecDays, ecFrom, ecTo)
	if row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric); row.ML.Only != 1 {
		t.Errorf("ilk bölüm sonra: %+v", row.ML)
	}
}

func TestEngineCompareEpisodesExact(t *testing.T) {
	in := ecSubj("s1", "checkout", chstore.AnomalySubjectEndpoint, "p99_ms @ /pay", 0, 10)
	in.EpisodeCount, in.FirstStart = 4, ecFrom
	r := CompareEngines([]EngineSubjectIn{in}, nil, nil, ecDays, ecFrom, ecTo)
	row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric)
	if !row.Subject.EpisodesExact || row.Subject.Episodes != 4 || row.Subject.Series != 1 || !r.Totals.Subject.EpisodesExact {
		t.Errorf("pencerede başlamış: %+v", row.Subject)
	}
	in.FirstStart = ecFrom - 1
	r = CompareEngines([]EngineSubjectIn{in}, nil, nil, ecDays, ecFrom, ecTo)
	if row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric); row.Subject.EpisodesExact || r.Totals.Subject.EpisodesExact {
		t.Errorf("pencereden önce başlamış exact: %+v", row.Subject)
	}
}

func TestEngineCompareMedianShortPerDay(t *testing.T) {
	ep := chstore.AnomalySubjectEndpoint
	subj := []EngineSubjectIn{
		ecSubj("s1", "checkout", ep, "p99_ms @ /a", 0, 5),     // 300 sn, kısa
		ecSubj("s2", "checkout", ep, "p99_ms @ /b", 100, 109), // 540 sn, kısa
		ecSubj("s3", "checkout", ep, "p99_ms @ /c", 200, 210), // 600 sn, kısa DEĞİL (sınır)
		ecSubj("s4", "checkout", ep, "p99_ms @ /d", 300, 360), // 3600 sn
	}
	r := CompareEngines(subj, nil, nil, ecDays, ecFrom, ecTo)
	row := ecRow(t, r, "checkout", EngFamLatency, EngSrcMetric)
	if row.Subject.MedianSec != 570 || row.Subject.Short != 2 {
		t.Errorf("medyan / kısa: %+v", row.Subject)
	}
	if row.Subject.PerDay != 4.0/ecDays || r.Totals.Subject.MedianSec != 570 {
		t.Errorf("olay/gün: %+v totals %+v", row.Subject, r.Totals.Subject)
	}
	if EngineCompareShortSec != 600 {
		t.Errorf("kısa eşiği %d, want 600 (singleMinSustained)", EngineCompareShortSec)
	}
	if medianSec([]int64{7}) != 7 || medianSec(nil) != 0 {
		t.Error("medianSec tek / boş")
	}
}

func TestEngineCompareWouldOpenAndProblems(t *testing.T) {
	s := ecSubj("s1", "checkout", chstore.AnomalySubjectEndpoint, "error_rate @ /pay", 0, 30)
	s.Promotion = chstore.AnomalyPromotionShadow
	m := ecML("m1", "svc_error_rate", "checkout", "", 0, 30)
	m.WouldOpen, m.LiveOpened = true, true
	refs := []EngineRefProblem{{Service: "checkout", Start: ecT(44), End: ecT(60)}, {Service: "orders", Start: ecT(0)}}
	r := CompareEngines([]EngineSubjectIn{s}, []EngineMLIn{m}, refs, ecDays, ecFrom, ecTo)
	row := ecRow(t, r, "checkout", EngFamErrors, EngSrcMetric)
	if row.Subject.WouldOpen != 1 || row.Subject.WithProblem != 1 || row.ML.WouldOpen != 1 || row.ML.LiveOpened != 1 || row.ML.WithProblem != 1 {
		t.Errorf("açardı / Problem: %+v", row)
	}
	refs[0].Start = ecT(46) // 30 dk bitiş + 16 dk → tolerans dışı
	r = CompareEngines([]EngineSubjectIn{s}, []EngineMLIn{m}, refs, ecDays, ecFrom, ecTo)
	if row := ecRow(t, r, "checkout", EngFamErrors, EngSrcMetric); row.Subject.WithProblem != 0 {
		t.Errorf("tolerans dışı Problem eşleşti: %+v", row.Subject)
	}
}

func TestEngineRefRule(t *testing.T) {
	for rule, want := range map[string]bool{
		"anomaly-auto:group:abc":      false,
		"anomaly-auto:svc:checkout":   false,
		"anomaly-auto:abc":            false,
		"anomaly:checkout:error_rate": false,
		"anomaly:checkout:p99_ms":     false,
		"svc-slowdown:checkout":       false,
		"rule:latency-slo":            true,
		"health:pod-restarts":         true,
	} {
		if got := EngineRefRule(rule); got != want {
			t.Errorf("%s = %v, want %v", rule, got, want)
		}
	}
}

func TestEngineMLInOfShadowHelpers(t *testing.T) {
	in := EngineMLInOf(chstore.EngineCompareMLRow{ID: "m1", Detector: "op_latency", Service: "checkout",
		Evidence: `{"groupShadow":{"group":"g1","wouldOpen":true,"rule":"anomaly-auto:group:m1","live":true}}`})
	if !in.WouldOpen || !in.LiveOpened {
		t.Errorf("grup kararı: %+v", in)
	}
	in = EngineMLInOf(chstore.EngineCompareMLRow{ID: "m2", Detector: "svc_error_rate", Service: "checkout",
		Evidence: `{"svcShadow":{"wouldOpen":true,"rule":"anomaly-auto:svc:checkout"}}`})
	if !in.WouldOpen || in.LiveOpened {
		t.Errorf("servis kararı: %+v", in)
	}
	if in := EngineMLInOf(chstore.EngineCompareMLRow{Evidence: ""}); in.WouldOpen || in.LiveOpened {
		t.Errorf("kanıtsız: %+v", in)
	}
}

func TestEngineCompareRowCap(t *testing.T) {
	var mls []EngineMLIn
	for i := 0; i < EngineCompareRowCap+5; i++ {
		mls = append(mls, ecML(fmt.Sprint(i), "op_latency", fmt.Sprintf("svc-%03d", i), "/x", 0, 10))
	}
	mls = append(mls, ecML("extra", "op_latency", "svc-000", "/y", 0, 10))
	r := CompareEngines(nil, mls, nil, ecDays, ecFrom, ecTo)
	if len(r.Rows) != EngineCompareRowCap || !r.RowsTruncated || r.Rows[0].Service != "svc-000" || r.Rows[0].ML.Events != 2 {
		t.Errorf("tavan: %d rows, truncated=%v first=%+v", len(r.Rows), r.RowsTruncated, r.Rows[0])
	}
}
