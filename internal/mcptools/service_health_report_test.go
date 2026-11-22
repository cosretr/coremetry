package mcptools

// v0.10.2454 — get_service_health raporu: montaj (eksik / okunamayan kaynak),
// bölüm sırası, örneklenmiş beyanı, lag "ölçülmüyor", önbellek anahtarı,
// paralel okuma (sahte kaynaklar; CH / VM yok). Adlar sentetik.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

var (
	hrTestTo   = time.Date(2026, 11, 22, 10, 30, 0, 0, time.UTC)
	hrTestFrom = hrTestTo.Add(-30 * time.Minute)
)

func hrSeries(vals ...float64) []chstore.SpanMetricSeries {
	s := chstore.SpanMetricSeries{}
	for i, v := range vals {
		s.Points = append(s.Points, chstore.SpanMetricPoint{Time: int64(i), Value: v})
	}
	return []chstore.SpanMetricSeries{s}
}

func hrVM(rps, errp, p95, p99 float64) MetricREDResult {
	return MetricREDResult{Source: "vm", Found: true, Metric: "http_server_request_duration_seconds", LatencyUnitKnown: true, InfraExcluded: true,
		Series: map[string][]chstore.SpanMetricSeries{"rate": hrSeries(rps, rps), "error_rate": hrSeries(errp), "p95": hrSeries(p95), "p99": hrSeries(p99)}}
}

// hrFullSources — her kaynağı dolu sahte set.
func hrFullSources() hrSources {
	return hrSources{
		metricRED: func(_ context.Context, from, _ time.Time) (MetricREDResult, error) {
			if from.Before(hrTestFrom) {
				return hrVM(50, 1, 100, 200), nil // önceki pencere
			}
			return hrVM(100, 2.5, 120, 300), nil
		},
		spanRED: func(_ context.Context, from, _ time.Time) (chstore.ServiceSummary, error) {
			if from.Before(hrTestFrom) {
				return chstore.ServiceSummary{SpanCount: 1000, ErrorCount: 50}, nil
			}
			return chstore.ServiceSummary{SpanCount: 1000, ErrorCount: 200}, nil
		},
		errProfile: func(_ context.Context, from, to time.Time) (chstore.ErrorProfile, error) {
			return chstore.ErrorProfile{From: from, To: to, TotalErrors: 200, ErrorTraces: 140, Rows: []chstore.ErrorProfileRow{
				{Operation: "POST /orders", ErrorType: "TimeoutException", HTTPStatus: 504, Errors: 90, Sample: "aaa1"},
				{Operation: "POST /orders", HTTPStatus: 500, Errors: 30, Sample: "aaa2"},
				{Operation: "GET /stock", GRPCCode: "14", Errors: 50, Sample: "bbb1"},
				{Operation: "db select", DBSystem: "oracle", Errors: 30, Sample: "ccc1"},
			}}, nil
		},
		opTotals: func(_ context.Context, names []string, _, _ time.Time) (map[string]chstore.OpTotal, error) {
			out := map[string]chstore.OpTotal{}
			for _, n := range names {
				out[n] = chstore.OpTotal{Calls: 400, Errors: 100}
			}
			return out, nil
		},
		exceptions: func(_ context.Context, _, _ time.Time) ([]chstore.ExceptionGroup, error) {
			return []chstore.ExceptionGroup{
				{Type: "java.net.SocketTimeoutException", Message: "Read timed out", Occurrences: 75, FirstSeen: hrTestFrom.Add(5 * time.Minute).UnixNano(), LastSeen: hrTestTo.UnixNano(), State: "new"},
				{Type: "IllegalStateException", Message: "pool exhausted", Occurrences: 9000, FirstSeen: hrTestFrom.Add(-48 * time.Hour).UnixNano(), LastSeen: hrTestTo.UnixNano(), State: "open"},
			}, nil
		},
		downstream: func(_ context.Context, _, _ time.Time) (runtimesig.DownstreamReport, error) {
			return runtimesig.DownstreamReport{Targets: []runtimesig.TargetRED{{Address: "partner-gw:443", RPS: 4, ErrorPct: 12.5, DominantError: "504"}, {Address: "ok-svc:80", RPS: 9}}}, nil
		},
		edges: func(_ context.Context, _, _ time.Time) ([]chstore.DownstreamEdge, error) {
			return []chstore.DownstreamEdge{{Target: "oracle-main", Kind: "db", Calls: 300, Errors: 30}}, nil
		},
		msgFlow: func(_ context.Context, _, _ time.Time) ([]chstore.MessagingFlowRow, error) {
			return []chstore.MessagingFlowRow{
				{System: "kafka", Destination: "orders-events", Service: "orders-api", Role: "producer", Spans: 500, Errors: 5, P95Ms: 12},
				{System: "kafka", Destination: "orders-events", Service: "billing-worker", Role: "consumer", Spans: 480},
			}, nil
		},
		kafkaFamily: func(_ context.Context, _ time.Time) (KafkaFamilyInfo, error) {
			return KafkaFamilyInfo{State: "metric", Family: "otel_java", Produce: true}, nil
		},
		lag:      func(_ context.Context, _, _ time.Time) ([]chstore.SpanMetricSeries, error) { return nil, nil },
		problems: func(_ context.Context) (uint64, error) { return 2, nil },
		anomalies: func(_ context.Context, _ time.Time) ([]chstore.AnomalyEvent, error) {
			return []chstore.AnomalyEvent{{ID: "a1"}}, nil
		},
		deploys: func(_ context.Context, _, _ time.Time) ([]chstore.Deploy, bool, error) {
			return []chstore.Deploy{{Version: "1.4.0", TimeUnixNs: hrTestFrom.UnixNano()}, {Version: "1.4.1", TimeUnixNs: hrTestFrom.Add(10 * time.Minute).UnixNano()}}, true, nil
		},
	}
}

func hrBuild(src hrSources, env string) ServiceHealthReport {
	return assembleHealthReport(collectHealthRaw(context.Background(), src, "orders-api", env, hrTestFrom, hrTestTo))
}

func TestHealthReportFullAssembly(t *testing.T) {
	r := hrBuild(hrFullSources(), "")
	m := r.Metrics
	if m.Status != HRStatusOK || m.Current == nil || *m.Current.RPS != 100 || *m.Current.ErrorPct != 2.5 || *m.Current.P99Ms != 300 || !m.InfraExcluded {
		t.Fatalf("metrics: %+v cur=%+v", m, m.Current)
	}
	if m.Delta == nil || *m.Delta.RPSPct != 100 || *m.Delta.ErrorPP != 1.5 || *m.Delta.P95Pct != 20 {
		t.Fatalf("delta: %+v", m.Delta)
	}
	te := r.TraceErrors
	if te.ErrorSharePct != 20 || *te.PriorErrorSharePct != 5 || te.Trend != "artıyor" || *te.ErrorTraces != 140 || !strings.Contains(te.Note, "örneklenmiş") {
		t.Fatalf("trace errors: %+v", te)
	}
	ops := r.ErrorOps.Rows
	if len(ops) != 3 || ops[0].Operation != "POST /orders" || ops[0].Errors != 120 || ops[0].SampleTrace != "aaa1" ||
		ops[0].Dominant != "error.type=TimeoutException · http=504" || ops[0].DominantPct != 75 || *ops[0].ErrorPct != 25 {
		t.Fatalf("ops: %+v", ops)
	}
	tech := r.Technical
	if len(tech.ErrorType) != 1 || tech.ErrorType[0].SharePct != 45 || len(tech.HTTPStatus) != 2 || tech.GRPCStatus[0].Value != "14" || tech.DB[0].Value != "oracle" {
		t.Fatalf("tech: %+v", tech)
	}
	if len(tech.Downstream) != 2 || tech.Downstream[0].Target != "partner-gw:443" || tech.Downstream[0].Source != "vm" || tech.Downstream[1].Source != "topology" {
		t.Fatalf("downstream: %+v", tech.Downstream)
	}
	ex := r.Exceptions.Groups
	if len(ex) != 2 || ex[0].Kind != "new" || ex[1].Kind != "recurring" {
		t.Fatalf("exceptions: %+v", ex)
	}
	ms := r.Messaging
	if ms.Status != HRStatusOK || len(ms.Topics) != 1 || ms.Topics[0].Role != "producer" || ms.Topics[0].ErrorPct != 1 {
		t.Fatalf("messaging: %+v", ms)
	}
	if !strings.HasPrefix(ms.LagStatus, "ölçülmüyor") || len(ms.Lag) != 0 {
		t.Fatalf("lag yalnız seriden; yoksa ölçülmüyor: %q %+v", ms.LagStatus, ms.Lag)
	}
	c := r.Context
	if *c.OpenProblems != 2 || *c.Anomalies != 1 || c.LastDeploy == nil || c.LastDeploy.Version != "1.4.1" {
		t.Fatalf("context: %+v", c)
	}
	if r.HasUnread() {
		t.Fatal("tam kaynakta unread olmamalı")
	}
}

// Kaynak yok → her bölüm missing, hiçbir değer üretilmez.
func TestHealthReportAllSourcesMissing(t *testing.T) {
	r := hrBuild(hrSources{}, "")
	for name, st := range map[string]string{"metrics": r.Metrics.Status, "trace_errors": r.TraceErrors.Status, "ops": r.ErrorOps.Status,
		"technical": r.Technical.Status, "exceptions": r.Exceptions.Status, "messaging": r.Messaging.Status} {
		if st != HRStatusMissing {
			t.Errorf("%s: %q (missing bekleniyor)", name, st)
		}
	}
	if r.Metrics.Current != nil || r.ErrorOps.Rows != nil || r.Messaging.Lag != nil {
		t.Fatal("eksik kaynakta değer yazılmamalı")
	}
	if !strings.HasPrefix(r.Messaging.LagStatus, "ölçülmüyor") {
		t.Fatalf("lag: %q", r.Messaging.LagStatus)
	}
	if r.Context.OpenProblems != nil || len(r.Context.Missing) != 3 || r.HasUnread() {
		t.Fatalf("context: %+v", r.Context)
	}
}

// Okuma hatası → unread (okunamadı ≠ yok); kalan bölümler etkilenmez.
func TestHealthReportUnreadIsNotNone(t *testing.T) {
	src := hrFullSources()
	boom := errors.New("timeout")
	src.metricRED = func(context.Context, time.Time, time.Time) (MetricREDResult, error) { return MetricREDResult{}, boom }
	src.errProfile = func(context.Context, time.Time, time.Time) (chstore.ErrorProfile, error) {
		return chstore.ErrorProfile{}, boom
	}
	src.problems = func(context.Context) (uint64, error) { return 0, boom }
	r := hrBuild(src, "")
	if r.Metrics.Status != HRStatusUnread || r.ErrorOps.Status != HRStatusUnread || r.Technical.Status != HRStatusUnread {
		t.Fatalf("unread bekleniyor: %s %s %s", r.Metrics.Status, r.ErrorOps.Status, r.Technical.Status)
	}
	if r.TraceErrors.Status != HRStatusOK || r.TraceErrors.ErrorTraces != nil {
		t.Fatalf("span özeti okunur; hatalı trace sayısı profil okunamayınca YOK: %+v", r.TraceErrors)
	}
	if r.Exceptions.Status != HRStatusOK || r.Context.OpenProblems != nil || len(r.Context.Unread) != 1 || !r.HasUnread() {
		t.Fatalf("diğerleri: %+v", r.Context)
	}
	// Aşağı hedefler profil okunamasa da gelir.
	if len(r.Technical.Downstream) == 0 {
		t.Fatal("downstream profil hatasından bağımsız")
	}
}

// VM metriği yok + kafka span'i yok → missing / none, açık not.
func TestHealthReportNoMetricNoMessaging(t *testing.T) {
	src := hrFullSources()
	src.metricRED = func(context.Context, time.Time, time.Time) (MetricREDResult, error) {
		return MetricREDResult{Source: "vm", Tried: []string{"http_server_request_duration_seconds"}}, nil
	}
	src.msgFlow = func(context.Context, time.Time, time.Time) ([]chstore.MessagingFlowRow, error) { return nil, nil }
	src.kafkaFamily = func(context.Context, time.Time) (KafkaFamilyInfo, error) { return KafkaFamilyInfo{State: "spans"}, nil }
	r := hrBuild(src, "")
	if r.Metrics.Status != HRStatusMissing || !strings.Contains(r.Metrics.Note, "denenen") || r.Metrics.Current != nil {
		t.Fatalf("metrics: %+v", r.Metrics)
	}
	if r.Messaging.Status != HRStatusNone || r.Messaging.LagStatus != "ölçülmüyor (tüketici rolü görülmedi)" {
		t.Fatalf("messaging: %+v", r.Messaging)
	}
}

// Ortamlı soru: MV'lerde ortam yok → operasyon hata % verilmez, beyan edilir.
func TestHealthReportEnvDropsMVPercent(t *testing.T) {
	r := hrBuild(hrFullSources(), "prep")
	for _, o := range r.ErrorOps.Rows {
		if o.ErrorPct != nil {
			t.Fatalf("env'li soruda MV hata %% verilmemeli: %+v", o)
		}
	}
	if !strings.Contains(r.ErrorOps.Note, "ortam") || !strings.Contains(r.Exceptions.Note, "ortam") || r.Env != "prep" {
		t.Fatalf("ortam notu: %q / %q", r.ErrorOps.Note, r.Exceptions.Note)
	}
}

// Consumer lag yalnız VM serisinden.
func TestHealthReportLagFromSeriesOnly(t *testing.T) {
	src := hrFullSources()
	src.lag = func(context.Context, time.Time, time.Time) ([]chstore.SpanMetricSeries, error) {
		s := hrSeries(10, 2500, 40)
		s[0].GroupKey = []string{"orders-events"}
		return s, nil
	}
	r := hrBuild(src, "")
	if len(r.Messaging.Lag) != 1 || r.Messaging.Lag[0].Max != 2500 || r.Messaging.Lag[0].Last != 40 || !strings.HasPrefix(r.Messaging.LagStatus, "ölçülüyor") {
		t.Fatalf("lag: %+v %q", r.Messaging.Lag, r.Messaging.LagStatus)
	}
}

// Bölüm sırası: JSON alanları ve Order operatör sırasında.
func TestHealthReportSectionOrder(t *testing.T) {
	want := []string{"metrics", "trace_errors", "error_operations", "technical_errors", "exceptions", "messaging", "context"}
	if strings.Join(HRSectionOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("sıra: %v", HRSectionOrder)
	}
	b, err := json.Marshal(hrBuild(hrFullSources(), ""))
	if err != nil {
		t.Fatal(err)
	}
	js, last := string(b), -1
	for _, k := range want {
		i := strings.Index(js, `"`+k+`":{`)
		if i <= last {
			t.Fatalf("%s sırası bozuk (%d ≤ %d)", k, i, last)
		}
		last = i
	}
	txt := RenderServiceHealthReportTR(hrBuild(hrFullSources(), ""))
	last = -1
	for _, h := range []string{HealthReportMarker, "1) Metrikler", "2) Trace hataları — ÖRNEKLENMİŞ", "3) En çok hata veren", "4) Teknik hatalar", "5) Exception", "6) Messaging", "Bağlam:"} {
		i := strings.Index(txt, h)
		if i <= last {
			t.Fatalf("metin sırası: %q (%d ≤ %d)\n%s", h, i, last, txt)
		}
		last = i
	}
	for _, v := range []string{"istek/s 100", "hata %2.5", "p99 300 ms", "istek %+100", "örnek trace aaa1", "consumer lag: ölçülmüyor", "son deploy 1.4.1"} {
		if !strings.Contains(txt, v) {
			t.Errorf("metinde %q yok:\n%s", v, txt)
		}
	}
}

// Eksik bölüm metinde adıyla söylenir, değer yok.
func TestHealthReportRenderMissing(t *testing.T) {
	txt := RenderServiceHealthReportTR(hrBuild(hrSources{}, ""))
	if strings.Count(txt, "ölçülmüyor / kaynak yok") < 5 || strings.Contains(txt, "istek/s") {
		t.Fatalf("eksik metin:\n%s", txt)
	}
}

// Önbellek anahtarı TÜM girdileri taşır.
func TestHealthReportCacheKey(t *testing.T) {
	base := HealthReportCacheKey("orders-api", "", hrTestFrom, hrTestTo)
	for name, k := range map[string]string{
		"service": HealthReportCacheKey("orders-api2", "", hrTestFrom, hrTestTo),
		"env":     HealthReportCacheKey("orders-api", "prep", hrTestFrom, hrTestTo),
		"from":    HealthReportCacheKey("orders-api", "", hrTestFrom.Add(-time.Minute), hrTestTo),
		"to":      HealthReportCacheKey("orders-api", "", hrTestFrom, hrTestTo.Add(time.Minute)),
	} {
		if k == base {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
	f, to := HealthReportWindow(hrTestFrom.Add(17*time.Second), hrTestTo.Add(17*time.Second))
	if !to.Equal(hrTestTo) || to.Sub(f) != 30*time.Minute {
		t.Fatalf("pencere: %v %v", f, to)
	}
}

func TestHealthTrend(t *testing.T) {
	for _, c := range []struct {
		cur, prior float64
		n          uint64
		want       string
	}{{20, 5, 10, "artıyor"}, {2, 6, 10, "azalıyor"}, {5.2, 5, 10, "yatay"}, {5, 0, 0, "önceki pencerede veri yok"}} {
		if got := hrTrend(c.cur, c.prior, c.n); got != c.want {
			t.Errorf("%v→%v: %q", c.prior, c.cur, got)
		}
	}
}
