package chstore

// priority_reason_golden_test.go — v0.10.2300 (sunucu metinleri S3):
// öncelik gerekçesinin DONDURULMUŞ matrisi (computePriority + IncidentPriority,
// her dal). Altın metin anahtarları eklenmeden ÖNCE bugünkü koddan üretildi;
// gerekçe metni bildirim başlığı / gövdesi ("Öncelik" satırı), MCP
// InboxReason, rehberli copilot blokları ve insight istemiyle AYNI bayt.
// Yalnız bilinçli bir metin değişikliğinde -update-prio-golden ile yazılır.

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var updatePrioGolden = flag.Bool("update-prio-golden", false, "testdata/priority_reason_*.golden.json'ı yeniden yaz")

type prioGoldenRow struct {
	Name   string `json:"name"`
	Prio   string `json:"prio"`
	Reason string `json:"reason"`
}

// prioGoldenCase — matrisin bir girdisi (problem ya da incident).
type prioGoldenCase struct {
	name string
	p    *Problem
	inc  *Incident
}

func prioGoldenCases() ([]prioGoldenCase, int64, ProblemPriorityConfig) {
	now := time.Unix(1_790_000_000, 0).UnixNano()
	hour := int64(time.Hour)
	cfg := ProblemPriorityConfig{}
	pr := func(id, sev string, v, th float64, cmp string, age int64) *Problem {
		return &Problem{ID: "p-1", RuleID: id, RuleName: "rule-1", Service: "svc-a", Severity: sev,
			Value: v, Threshold: th, Comparator: cmp, Status: "open", StartedAt: now - age}
	}
	exP := pr(ExceptionGroupRulePrefix+"new", "critical", 0, 0, "", 0)
	exP.Priority, exP.PriorityReason = "P1", "önceden hesaplanmış gerekçe"
	cs := []prioGoldenCase{
		{name: "info", p: pr("rule-x", "info", 50, 10, ">", 0)},
		{name: "storm", p: pr("exception-storm", "critical", 9, 5, "", 0)},
		{name: "incident-critical", p: pr("incident:i-1", "critical", 0, 0, "", 0)},
		{name: "incident-warning", p: pr("incident:i-1", "warning", 0, 0, "", 0)},
		{name: "exception-passthrough", p: exP},
		{name: "critical-breach", p: pr("rule-x", "critical", 25, 10, ">", 0)},
		{name: "critical-breach-below", p: pr("rule-x", "critical", 40, 99, "<", 0)},
		{name: "critical-total-loss", p: pr("monitor:m-1", "critical", 0, 1, "", 0)},
		{name: "critical-total-loss-frac", p: pr("rule-x", "critical", 0, 99.5, "<", 0)},
		{name: "critical-stale", p: pr("rule-x", "critical", 12, 10, ">", 5*hour+hour/4)},
		{name: "critical-plain", p: pr("rule-x", "critical", 12, 10, ">", 0)},
		{name: "warning-breach", p: pr("rule-x", "warning", 31, 10, ">", 0)},
		{name: "warning-total-loss", p: pr("rule-x", "warning", 0, 3, "<", 0)},
		{name: "warning-steady", p: pr("rule-x", "warning", 12, 10, ">", 0)},
		{name: "warning-zero-threshold", p: pr("rule-x", "warning", 12, 0, ">", 0)},
	}
	for _, c := range []struct{ name, sev, status string }{
		{"inc-critical", "critical", "open"}, {"inc-warning", "warning", "open"}, {"inc-info", "info", "open"},
		{"inc-critical-ack", "critical", "acknowledged"}, {"inc-warning-ack", "warning", "acknowledged"},
		{"inc-info-ack", "info", "acknowledged"},
	} {
		cs = append(cs, prioGoldenCase{name: c.name, inc: &Incident{ID: "i-1", Severity: c.sev, Status: c.status}})
	}
	return cs, now, cfg
}

func prioGoldenMatrix() []prioGoldenRow {
	cs, now, cfg := prioGoldenCases()
	out := make([]prioGoldenRow, 0, len(cs))
	for _, c := range cs {
		var p, r string
		if c.inc != nil {
			p, r = IncidentPriority(*c.inc)
		} else {
			p, r = computePriority(*c.p, now, cfg)
		}
		out = append(out, prioGoldenRow{Name: c.name, Prio: p, Reason: r})
	}
	return out
}

func checkGolden(t *testing.T, path string, v any, update bool) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("altın %s değişti:\n%s", path, firstDiff(string(want), buf.String()))
	}
}

func TestPriorityReasonGolden(t *testing.T) {
	checkGolden(t, filepath.Join("testdata", "priority_reason_tr.golden.json"), prioGoldenMatrix(), *updatePrioGolden)
}

// prioMsgFixture — FE'nin okuduğu fikstür satırı (frontend/src/lib/
// prioMsg.lang.test.tsx): gerekçe metni + anahtar.
type prioMsgFixture struct {
	Name string  `json:"name"`
	TR   string  `json:"tr"`
	Msg  *StdMsg `json:"msg"`
}

// TestPriorityReasonMessages — v0.10.2300 (S3): her dalın gerekçesinin
// yanında anahtar var (exception grubu geçişi hariç: anahtarı problemle
// gelir); fikstür testdata/priority_reason_msgs.golden.json (FE okur).
func TestPriorityReasonMessages(t *testing.T) {
	cs, now, cfg := prioGoldenCases()
	var fx []prioMsgFixture
	for _, c := range cs {
		var r string
		var m *StdMsg
		if c.inc != nil {
			_, r, _ = incidentPriorityMsg(*c.inc)
			m = IncidentPriorityMsg(*c.inc)
		} else {
			_, r, m = computePriorityMsg(*c.p, now, cfg)
		}
		if c.name == "exception-passthrough" {
			if m != nil {
				t.Errorf("geçiş: anahtar problemden gelmeli, %v", m)
			}
			continue
		}
		if m == nil || m.K == "" {
			t.Errorf("%s: anahtar yok (%q)", c.name, r)
			continue
		}
		fx = append(fx, prioMsgFixture{Name: c.name, TR: r, Msg: m})
	}
	checkGolden(t, filepath.Join("testdata", "priority_reason_msgs.golden.json"), fx, *updatePrioGolden)
}

// TestPriorityReasonMsgNotOnProblemJSON — Problem MCP kaynağına / aracına
// (marshalJSON(rows)) ve webhook gövdesine ("problem": p) BÜTÜN olarak
// serileşiyor: zenginleştirmenin koyduğu anahtar JSON'a çıkmamalı, bayt aynı.
func TestPriorityReasonMsgNotOnProblemJSON(t *testing.T) {
	p := Problem{ID: "p-1", RuleID: "rule-x", RuleName: "rule-1", Service: "svc-a", Severity: "critical",
		Value: 25, Threshold: 10, Comparator: ">", Status: "open", StartedAt: time.Now().UnixNano()}
	before, _ := json.Marshal(p)
	rows := EnrichProblemsWithPriority([]Problem{p})
	if rows[0].PriorityReasonMsg == nil {
		t.Fatal("zenginleştirme anahtarı koymadı")
	}
	got, _ := json.Marshal(rows[0])
	rows[0].PriorityReasonMsg = nil
	want, _ := json.Marshal(rows[0])
	if !bytes.Equal(got, want) || bytes.Contains(got, []byte("priorityReasonMsg")) || bytes.Contains(got, []byte("prio.")) {
		t.Fatalf("Problem JSON'unda anahtar sızdı:\n%s", got)
	}
	if bytes.Equal(before, got) {
		t.Fatal("öncelik zenginleşmedi (test girdisi)")
	}
}
