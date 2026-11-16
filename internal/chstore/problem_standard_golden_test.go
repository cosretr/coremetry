package chstore

// problem_standard_golden_test.go — v0.10.2299 (sunucu metinleri S2):
// Problem standardının Türkçe çıktısının DONDURULMUŞ matrisi. Altın dosya
// metin anahtarları eklenmeden ÖNCE bugünkü koddan üretildi; anahtar / arg
// eklemesi Türkçe baytları değiştirirse bu test kırılır. Türkçe metin
// bildirim konusu, thread Topic'i, MCP satırı ve özet öğeleriyle AYNI bayt
// (docs/i18n/server-text-design.md §1). Altın yalnız bilinçli bir Türkçe
// metin değişikliğinde -update-std-golden ile yeniden yazılır.

import (
	"bytes"
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var updateStdGolden = flag.Bool("update-std-golden", false, "testdata/problem_standard_tr.golden.json'ı yeniden yaz")

// stdGoldenRow — matrisin bir satırı: Türkçe alanlar + kapı notları.
type stdGoldenRow struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Headline string   `json:"headline"`
	Impact   string   `json:"impact"`
	Evidence string   `json:"evidence"`
	Next     string   `json:"next"`
	Context  []string `json:"context,omitempty"`
	Gates    []string `json:"gates"`
}

func stdGoldenOf(name string, s ProblemStandard) stdGoldenRow {
	r := stdGoldenRow{Name: name, Kind: s.Kind, Headline: s.Headline, Impact: s.Impact.Primary.Text,
		Evidence: s.Evidence.Sentence, Next: s.NextStep.Text, Context: s.Context}
	for _, g := range s.Confidence.Gates {
		r.Gates = append(r.Gates, g.ID+" "+g.Name+" "+g.Status+" "+g.Note)
	}
	return r
}

// stdGoldenStds — matrisin standartları (anahtar testleri okur; stdGoldenMatrix doldurur).
var stdGoldenStds []ProblemStandard

// stdGoldenMatrix — her tür, yön ve kaynak dalı; nötr adlar.
func stdGoldenMatrix() []stdGoldenRow {
	stdGoldenStds = nil
	now := time.Unix(1_790_000_000, 0)
	t0 := now.Add(-95 * time.Minute).UnixNano()
	yes, no := true, false
	tr := &StdTraffic{Calls5m: 12, Errors5m: 4, P99Ms: 900, BaselineCalls5m: 60, BaselineP99Ms: 300}
	trOK := &StdTraffic{Calls5m: 400, Errors5m: 40, P99Ms: 900, BaselineCalls5m: 500, BaselineP99Ms: 300}
	ctx := StdFacts{Now: now, RecentDeploy: "svc-a v2", CalendarKind: CalendarHoliday, Batch: true}
	p := func(id, name, metric string, v, th float64, desc string) Problem {
		return Problem{ID: "p-" + id, RuleID: id, RuleName: name, Service: "svc-a", Metric: metric, Value: v,
			Threshold: th, Comparator: ">", Description: desc, Status: "open", StartedAt: t0}
	}
	down := func(pr Problem) Problem { pr.Comparator = "<"; return pr }
	var out []stdGoldenRow
	add := func(name string, s ProblemStandard) {
		out = append(out, stdGoldenOf(name, s))
		stdGoldenStds = append(stdGoldenStds, s)
	}
	ap := func(name string, pr Problem, f StdFacts) { add(name, StandardizeProblem(pr, f)) }

	ap("err-up-vm", p("anomaly:svc-a:error_rate", "anomaly", "error_rate", 3.25, 0.4, REDEvidenceVM), StdFacts{Now: now, Traffic: trOK})
	ap("err-up-hold", p("anomaly:svc-a:error_rate", "anomaly", "error_rate", 12.5, 1, REDEvidenceVMHold+"x"), StdFacts{Now: now, Traffic: tr})
	ap("err-down-span", down(p("anomaly:svc-a:error_rate", "anomaly", "error_rate", 0.05, 2, REDEvidenceSpan)), StdFacts{Now: now})
	ap("err-ramp", p("anomaly:svc-a:error_rate", "anomaly", "error_rate", 7.75, 0.5, "Teknik hata (5xx) sürekli artıyor — 2.5 hata/sn"), StdFacts{Now: now, Traffic: trOK})
	ap("p99-up", p("anomaly:svc-a:p99_ms", "anomaly", "p99_ms", 1450, 320, REDEvidenceVM), StdFacts{Now: now, Traffic: tr})
	ap("p99-down", down(p("anomaly:svc-a:p99_ms", "anomaly", "p99_ms", 80, 320, "")), StdFacts{Now: now})
	ap("rate-up", p("anomaly:svc-a:request_rate", "anomaly", "request_rate", 250.5, 40, ""), StdFacts{Now: now, Traffic: trOK})
	ap("rate-drop-pct", down(p("anomaly:svc-a:request_rate", "anomaly", "request_rate", 3.5, 40, "")), StdFacts{Now: now, Traffic: tr})
	rd := p("anomaly:svc-a:request_rate", "anomaly", "request_rate", 3.5, 0, "")
	rd.Comparator = "<"
	ap("rate-drop-plain", rd, StdFacts{Now: now})
	ap("silent", p("anomaly:svc-a:service_silent", "anomaly", "service_silent", 0, 0, ""), StdFacts{Now: now})
	ap("other-ratio", p("anomaly:svc-a:queue_depth", "anomaly", "svc:queue_depth", 50, 10, ""), StdFacts{Now: now})
	ap("other-noratio", p("anomaly:svc-a:x_error_count", "anomaly", "svc:ext:error_count", 0, 0, ""), StdFacts{Now: now})
	ap("auto-fail", p("anomaly-auto:svc-a:y", "auto", "jobs_fail_count", 9, 3, ""), StdFacts{Now: now})
	ap("auto-ml", p("anomaly-auto:svc-a:ml", MLPromotedRuleNamePrefix+"grup", "x", 9, 3, "svc-a: özet cümlesi"), StdFacts{Now: now})
	ap("auto-inf", p("anomaly-auto:svc-a:z", "auto", "request_rate", 0, 30, ""), StdFacts{Now: now})
	ap("ext", p(RuleExtSeriesPrefix+"src-1", "ext", "src-1:ext:error_count", 14, 2, ""), StdFacts{Now: now})
	ap("ext-down", p(RuleExtDownPrefix+"src-1", "ext", "", 0, 0, ""), StdFacts{Now: now})
	ap("ext-cap", p(RuleExtCapPrefix+"src-1", "ext", "", 0, 0, ""), StdFacts{Now: now})
	ap("ext-cluster", p(RuleExtClusterPrefix+"src-1", "ext", "", 6, 0, ""), StdFacts{Now: now, RelationClaimed: true, RelationVerified: &yes})
	ap("cluster", p("anomaly-cluster:c1", "cluster", "", 4, 0, ""), StdFacts{Now: now, RelationVerified: &no})
	ap("slowdown", p(RuleSvcSlowdownPrefix+"svc-a", "Yaygın yavaşlama", "", 2300, 700, ""), StdFacts{Now: now, Traffic: tr})
	ap("collapse", p(RuleSvcSlowdownPrefix+"svc-a", "trafik çöküşü ve yavaşlama", "", 2300, 700, ""), StdFacts{Now: now, Traffic: tr})
	ap("db-err", p(RuleDBHealthPrefix+"postgresql@db-host-1/db-1", "db", DBHealthMetricErrorPct, 4.5, 2, ""), StdFacts{Now: now})
	ap("db-p99", p(RuleDBHealthPrefix+"postgresql@db-host-1/db-1", "db", DBHealthMetricP99Ms, 1800, 500, ""), StdFacts{Now: now})
	ap("db-count", p(RuleDBHealthPrefix+"postgresql@db-host-1/db-1", "db", "db.errors_5m", 37, 10, ""), StdFacts{Now: now})
	ap("db-cap", p(RuleDBCapacityPrefix+"db-1", "cap", "cap_pct", 91.5, 85, ""), StdFacts{Now: now})
	ap("db-slow-stmt", p("db-slow-stmt", "stmt", "p95_ms", 4200, 1000, ""), StdFacts{Now: now})
	ap("gc-ms", p("runtime:jvm-gc:svc-a", "gc", "runtime.jvm_gc", 640, 200, ""), StdFacts{Now: now})
	ap("gc-pct-open", p("runtime:jvm-gc:svc-a", "gc", "runtime.jvm_gc_pct", 23.4, 10, ""), StdFacts{Now: now, SameServiceErrOrP99Open: true})
	ap("monitor", p("monitor:m1", "Monitor: check-1", "", 0, 0, ""), StdFacts{Now: now})
	mon := p("monitor:m2", "", "", 0, 0, "")
	ap("monitor-noname", mon, StdFacts{Now: now})
	for _, id := range []string{"self-ingest-stall", "self-spool-depth", "self-disk-eta", "self-channel-broken:c", "self-volume-spike", "self-other"} {
		ap(id, p(id, "self", "", 1, 0, ""), StdFacts{Now: now})
	}
	ap("slo", p("slo:s1", "slo", "", 14.4, 1, ""), StdFacts{Now: now})
	ap("slo-inf", p("slo:s1", "slo", "", 0, 1, ""), StdFacts{Now: now})
	ap("storm", p("exception-storm", "storm", "", 7, 0, ""), StdFacts{Now: now})
	ap("shared", p("exception:shared-dependency:x", "shared", "", 3, 0, ""), StdFacts{Now: now, RelationClaimed: true})
	ap("fatal", p("exception:fatal-infrastructure:x", "fatal", "", 12, 0, ""), StdFacts{Now: now})
	exg := p(ExceptionGroupRulePrefix+"fp-1", "Exception · ErrorType-1", "", 0, 0, "ErrorType-1 — 42 occurrence · x")
	exg.ID = ExceptionGroupRulePrefix + "fp-1"
	ap("exgroup", exg, StdFacts{Now: now})
	exh := p(ExceptionGroupRulePrefix+"fp-2", "HTTP hatası · 503", "", 0, 0, "")
	exh.ID = ExceptionGroupRulePrefix + "fp-2"
	ap("exgroup-http5", exh, StdFacts{Now: now})
	exh4 := p(ExceptionGroupRulePrefix+"fp-3", "HTTP hatası · 404", "", 0, 0, "")
	exh4.ID = ExceptionGroupRulePrefix + "fp-3"
	ap("exgroup-http4", exh4, StdFacts{Now: now})
	exo := p(ExceptionGroupRulePrefix+OracleGroupPrefix+"x", "Oracle · ERR-1", "", 0, 0, "")
	exo.ID = ExceptionGroupRulePrefix + OracleGroupPrefix + "x"
	exo.Priority = "P1"
	ap("exgroup-oracle", exo, StdFacts{Now: now})
	inc := p("incident:i1", "Incident · başlık-1", "", 0, 0, "")
	ap("incident-problem", inc, StdFacts{Now: now, IncidentProblems: 3})
	ap("rule-builtin-err", p("builtin-x", "Kural-1", "http.error_rate", 6, 5, ""), StdFacts{Now: now, Traffic: trOK})
	ap("rule-user-ms", p("r-1", "Kural-2", "checkout_latency", 1300, 1000, ""), StdFacts{Now: now})
	ap("rule-user-rate", p("r-2", "", "queue_rate", 0, 0, ""), StdFacts{Now: now})
	ap("rule-user-sn", p("r-3", "Kural-3", "x", 2.5, 1, ""), ctx)
	ep := p("r-4", "Kural-4", "x_pct", 81.25, 80, "")
	ep.EpisodeCount = 3
	ep.Status = "resolved"
	ap("rule-episode-ctx", ep, StdFacts{Now: now, CalendarKind: CalendarMonthEnd})
	ap("rule-halfday", p("r-5", "Kural-5", "x", 1, 1, ""), StdFacts{Now: now, CalendarKind: CalendarHalfDay, RecentDeploy: "svc-a v3"})
	long := p("r-6", "Çok uzun kural adı "+string(bytes.Repeat([]byte("ğ"), 120)), "x", 1, 1, "")
	ap("rule-long-clip", long, StdFacts{Now: now})

	// Exception grubu.
	g := ExceptionGroup{Fingerprint: "fp-9", Type: "ErrorType-9", Message: "m", Service: "svc-a", State: "open",
		FirstSeen: t0, LastSeen: now.UnixNano(), Occurrences: 1234}
	add("ex", StandardizeException(g, ctx))
	g2 := g
	g2.Type, g2.State, g2.Occurrences = "503", ExStateRegressed, 3
	add("ex-http5-regressed", StandardizeException(g2, StdFacts{Now: now}))
	g3 := g
	g3.Type = "429"
	add("ex-http4", StandardizeException(g3, StdFacts{Now: now}))
	g4 := g
	g4.Fingerprint, g4.Message, g4.Priority = OracleGroupPrefix+"q", " SELECT op ", "P1"
	add("ex-oracle", StandardizeException(g4, StdFacts{Now: now}))

	// Anomali olayı.
	ev := func(kind, pattern string, peak float64) AnomalyEvent {
		return AnomalyEvent{ID: "e-1", Kind: kind, Pattern: pattern, Service: "svc-a", PeakRatio: peak, CurrentRatio: peak,
			StartedAt: t0, LastSeen: now.UnixNano(), Status: "active", EpisodeCount: 2}
	}
	for _, e := range []struct {
		n string
		e AnomalyEvent
	}{
		{"ev-op-latency", ev("trace_op_latency", "GET /a", 4.2)},
		{"ev-op", ev("trace_op", "GET /a", 2.5)},
		{"ev-log", ev("log_pattern", "pat-1", 9)},
		{"ev-behavior", ev("behavior_change", "", 3)},
		{"ev-newtpl", ev("log_template_new", "tpl-1", CountOnlyRatio)},
		{"ev-subject", ev(SubjectAnomalyKind, "p99_ms @ GET /a", 6)},
		{"ev-raw", ev("custom_kind", "x", 1.5)},
		{"ev-ml", ev(MLAnomalyKindPrefix+"new_template", "", 2)},
		{"ev-ml-metric", ev(MLAnomalyKindPrefix+"metric_cpu", "cpu", 3.3)},
		{"ev-nopat-noratio", ev("trace_op", "", math.NaN())},
	} {
		add(e.n, StandardizeAnomalyEvent(e.e, ctx))
	}

	// Incident.
	ri := now.Add(-5 * time.Minute).UnixNano()
	add("inc", StandardizeIncident(Incident{ID: "i-1", Title: " başlık-2 ", Service: "svc-a", Status: "open", Severity: "critical", StartedAt: t0}, StdFacts{Now: now, IncidentProblems: 2}))
	add("inc-resolved", StandardizeIncident(Incident{ID: "i-2", Title: "başlık-3", Status: "resolved", Severity: "warning", StartedAt: t0, ResolvedAt: &ri}, ctx))
	return out
}

func TestProblemStandardTurkishGolden(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(stdGoldenMatrix()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "problem_standard_tr.golden.json")
	if *updateStdGolden {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("Türkçe problem standardı metni değişti (altın %s); fark:\n%s", path, firstDiff(string(want), buf.String()))
	}
}

func firstDiff(a, b string) string {
	al, bl := bytes.Split([]byte(a), []byte("\n")), bytes.Split([]byte(b), []byte("\n"))
	for i := 0; i < len(al) && i < len(bl); i++ {
		if !bytes.Equal(al[i], bl[i]) {
			return "satır " + itoa(int64(i+1)) + "\n- " + string(al[i]) + "\n+ " + string(bl[i])
		}
	}
	return "uzunluk farkı"
}

// stdMsgFixture — FE'nin okuduğu fikstür satırı: anahtar + argümanlar +
// aynı yerdeki Türkçe metin (vitest EN çizimini ve anahtarın katalogda
// olduğunu doğrular: frontend/src/lib/stdMsg.lang.test.ts).
type stdMsgFixture struct {
	Field string  `json:"field"`
	TR    string  `json:"tr"`
	Msg   *StdMsg `json:"msg"`
}

// TestProblemStandardMessages — v0.10.2299 (S2): her sunucu cümlesinin
// yanında anahtar var (ML terfi ve olay manşeti hariç: serbest metin);
// ContextMsgs Context ile aynı uzunlukta; kapı notu ↔ NoteMsg. Fikstür
// testdata/problem_standard_msgs.golden.json (FE okur) matristen üretilir.
func TestProblemStandardMessages(t *testing.T) {
	stdGoldenMatrix()
	var fx []stdMsgFixture
	seen := map[string]bool{}
	push := func(field, tr string, m *StdMsg) {
		if m == nil {
			return
		}
		if m.K == "" {
			t.Fatalf("%s: boş anahtar (%q)", field, tr)
		}
		b, _ := json.Marshal(m)
		id := field + string(b)
		if seen[id] {
			return
		}
		seen[id] = true
		fx = append(fx, stdMsgFixture{Field: field, TR: tr, Msg: m})
	}
	for _, s := range stdGoldenStds {
		free := s.Kind == "incident" || (s.Kind == "anomaly_auto" && s.HeadlineMsg == nil)
		if s.HeadlineMsg == nil && !free {
			t.Errorf("%s: manşet anahtarı yok (%q)", s.Kind, s.Headline)
		}
		for name, m := range map[string]*StdMsg{"impact": s.Impact.Primary.Msg, "evidence": s.Evidence.Msg, "next": s.NextStep.Msg} {
			if m == nil {
				t.Errorf("%s: %s anahtarı yok", s.Kind, name)
			}
		}
		if len(s.ContextMsgs) != len(s.Context) {
			t.Errorf("%s: bağlam %d satır, anahtar %d", s.Kind, len(s.Context), len(s.ContextMsgs))
		}
		push("headline", s.Headline, s.HeadlineMsg)
		push("impact", s.Impact.Primary.Text, s.Impact.Primary.Msg)
		push("evidence", s.Evidence.Sentence, s.Evidence.Msg)
		push("next", s.NextStep.Text, s.NextStep.Msg)
		for i := range s.ContextMsgs {
			push("context", s.Context[i], &s.ContextMsgs[i])
		}
		for _, g := range s.Confidence.Gates {
			if (g.Note != "") != (g.NoteMsg != nil) {
				t.Errorf("%s %s: not %q ↔ anahtar %v", s.Kind, g.ID, g.Note, g.NoteMsg)
			}
			push("gate", g.Note, g.NoteMsg)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(fx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "problem_standard_msgs.golden.json")
	if *updateStdGolden {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("anahtar fikstürü eski (%s; -update-std-golden ile yeniden yaz, FE testi okur):\n%s", path, firstDiff(string(want), buf.String()))
	}
}

// TestStdMsgNonFiniteArgs — +Inf / NaN argüman JSON'u bozmaz.
func TestStdMsgNonFiniteArgs(t *testing.T) {
	m := stdM("k", "rX", math.Inf(1), "vNum", math.NaN(), "s", "x")
	if _, err := json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if m.A["rX"] != "inf" || m.A["vNum"] != "—" || m.A["s"] != "x" {
		t.Fatalf("%v", m.A)
	}
}
