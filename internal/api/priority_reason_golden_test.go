package api

// priority_reason_golden_test.go — v0.10.2300 (sunucu metinleri S3): inbox
// öncelik gerekçesinin DONDURULMUŞ matrisi — exception merdiveni, Oracle
// merdiveni, anomali olayı, kaynak önceliği istisnası / P3 zorlaması ve
// incident katlaması (her dal). Altın metin anahtarları eklenmeden ÖNCE
// bugünkü koddan üretildi; gerekçe bildirim ("Öncelik" satırı), MCP
// InboxReason ve CoSRE inbox okuyucusuyla AYNI bayt. Yalnız bilinçli bir
// metin değişikliğinde -update-prio-golden ile yazılır.

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var updatePrioGolden = flag.Bool("update-prio-golden", false, "testdata/priority_reason_*.golden.json'ı yeniden yaz")

type prioGoldenRow struct {
	Name   string `json:"name"`
	Prio   string `json:"prio"`
	Reason string `json:"reason"`
}

var prioGoldenNow = time.Unix(1_790_000_000, 0)

// prioGoldenItems — matrisin satırları (InboxItem: öncelik + gerekçe; anahtar
// testi aynı satırları okur).
func prioGoldenItems() []struct {
	name string
	it   InboxItem
} {
	now := prioGoldenNow
	ns := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	cfg := chstore.DefaultExceptionTriage()
	type row = struct {
		name string
		it   InboxItem
	}
	var out []row
	add := func(name, prio, reason string, m *chstore.StdMsg) {
		out = append(out, row{name, InboxItem{ID: "x:" + name, Priority: prio, PriorityReason: reason, PriorityReasonMsg: m}})
	}
	g := func(occ uint64, first, last time.Duration, state string, atResolve uint64) chstore.ExceptionGroup {
		return chstore.ExceptionGroup{Fingerprint: "fp-1", Service: "svc-a", Type: "TypeA", Occurrences: occ,
			FirstSeen: ns(first), LastSeen: ns(last), State: state, OccurrencesAtResolve: atResolve}
	}
	exc := []struct {
		name string
		g    chstore.ExceptionGroup
	}{
		{"ex-burst-fresh", g(11260, 12*time.Minute, time.Minute, "new", 0)},
		{"ex-burst-secs", g(1200, 40*time.Second, 10*time.Second, "new", 0)},
		{"ex-burst-ended", g(11260, 5*time.Hour+12*time.Minute, 5*time.Hour, "new", 0)},
		{"ex-burst-ended-hours", g(2500, 26*time.Hour+10*time.Minute, 26*time.Hour, "new", 0)},
		{"ex-regressed-p1", g(1900, 30*24*time.Hour, 2*time.Hour, chstore.ExStateRegressed, 1000)},
		{"ex-regressed-p2", g(900, 30*24*time.Hour, 2*time.Hour, chstore.ExStateRegressed, 0)},
		{"ex-active", g(18217, 35*24*time.Hour, time.Minute, "new", 0)},
		{"ex-stopped", g(600, 30*24*time.Hour, 2*time.Hour+5*time.Minute, "new", 0)},
		{"ex-seen", g(150, 10*24*time.Hour, 30*time.Minute, "new", 0)},
		{"ex-near-burst", g(450, 8*time.Hour+6*time.Minute, 8*time.Hour, "new", 0)},
		{"ex-steady", g(20, 10*24*time.Hour, 8*time.Hour, "new", 0)},
	}
	for _, c := range exc {
		p, r, m := exceptionPriorityAtMsg(c.g, cfg, now)
		add(c.name, p, r, m)
	}
	og := func(first, last time.Duration, state string) chstore.ExceptionGroup {
		gg := g(100, first, last, state, 0)
		gg.Fingerprint = chstore.OracleGroupPrefix + "fp-2"
		return gg
	}
	hi := cfg
	hi.OracleP1MinOccurrences = 200
	ora := []struct {
		name       string
		g          chstore.ExceptionGroup
		cfg        chstore.ExceptionTriageConfig
		last, prev uint64
	}{
		{"ora-burst", og(10*24*time.Hour, time.Minute, "new"), cfg, 1200, 10},
		{"ora-new", og(time.Hour, time.Minute, "new"), cfg, 120, 100},
		{"ora-p1", og(10*24*time.Hour, time.Minute, "new"), cfg, 120, 100},
		{"ora-regressed", og(10*24*time.Hour, 10*time.Hour, chstore.ExStateRegressed), hi, 150, 90},
		{"ora-p2", og(10*24*time.Hour, time.Minute, "new"), hi, 150, 90},
		{"ora-steady", og(10*24*time.Hour, time.Minute, "new"), cfg, 5, 4},
	}
	for _, c := range ora {
		p, r, m := oraclePriorityAtMsg(c.g, c.cfg, now, c.last, c.prev, "lbl-1")
		add(c.name, p, r, m)
	}
	an := []struct {
		name string
		e    chstore.AnomalyEvent
	}{
		{"an-cleared", chstore.AnomalyEvent{Status: "cleared", PeakRatio: 4}},
		{"an-no-baseline", chstore.AnomalyEvent{Status: "open", BaselineKind: chstore.AnomalyBaselineNone}},
		{"an-no-signal", chstore.AnomalyEvent{Status: "open", BaselineKind: "ewma", PeakRatio: 0}},
		{"an-peak", chstore.AnomalyEvent{Status: "open", BaselineKind: "ewma", PeakRatio: 3.25}},
		{"an-mild", chstore.AnomalyEvent{Status: "open", BaselineKind: "ewma", PeakRatio: 1.4}},
	}
	for _, c := range an {
		p, r, m := anomalyPriorityMsg(c.e)
		add(c.name, p, r, m)
	}
	// Kaynak önceliği istisnası / P3 zorlaması: varsayılan liste + operatör kalıbı.
	keep := append(chstore.DefaultInboxKeepSourcePriority(), "custom-*")
	var ks []InboxItem
	breach := chstore.NewStdMsg("prio.breach.critical", "r", "2.5")
	for _, pat := range keep {
		id := strings.ReplaceAll(pat, "*", "x")
		if strings.HasPrefix(pat, "incident:") {
			ks = append(ks, InboxItem{ID: "keep:" + pat, Kind: "incident", Severity: "critical", Priority: "P1", PriorityReason: "Declared incident, critical",
				PriorityReasonMsg: chstore.NewStdMsg("prio.inc.critical")})
			continue
		}
		for _, sev := range []string{"critical", "warning"} {
			ks = append(ks, InboxItem{ID: "keep:" + pat + ":" + sev, Kind: "problem", Severity: sev, Priority: "P1",
				PriorityReason: "critical + 2.5x threshold", PriorityReasonMsg: breach, Problem: &InboxProblemRef{ID: "p-1", RuleID: id}})
		}
	}
	ks = append(ks,
		InboxItem{ID: "keep:no-reason", Kind: "problem", Severity: "critical", Priority: "P2", Problem: &InboxProblemRef{ID: "p-1", RuleID: "builtin-x"}},
		InboxItem{ID: "force:problem", Kind: "problem", Severity: "critical", Priority: "P1", PriorityReason: "critical + 2.5x threshold", PriorityReasonMsg: breach, Problem: &InboxProblemRef{ID: "p-1", RuleID: "rule-x"}},
		InboxItem{ID: "force:anomaly", Kind: "anomaly", Priority: "P2", PriorityReason: "3.2x baseline", PriorityReasonMsg: chstore.NewStdMsg("prio.an.peak", "r", "3.2")},
		InboxItem{ID: "skip:p3", Kind: "problem", Priority: "P3", PriorityReason: "warning steady", PriorityReasonMsg: chstore.NewStdMsg("prio.warnSteady"), Problem: &InboxProblemRef{ID: "p-1", RuleID: "builtin-x"}},
	)
	forceNonExceptionP3With(ks, keep)
	for _, it := range ks {
		out = append(out, row{it.ID, it})
	}
	// Incident katlaması: bağlı problem incident'ten yüksekse gerekçe ondan.
	fold := []InboxItem{
		{ID: "incident:i-1", Kind: "incident", Priority: "P2", PriorityReason: "Declared incident, warning", Incident: &InboxIncidentRef{ID: "i-1", Status: "open"}},
		{ID: "problem:p-1", Kind: "problem", Priority: "P1", PriorityReason: "critical + 2.5x threshold", PriorityReasonMsg: chstore.NewStdMsg("prio.breach.critical", "r", "2.5"), Problem: &InboxProblemRef{ID: "p-1"}},
	}
	for _, it := range foldIncidentProblems(fold, map[string][]string{"i-1": {"p-1"}}) {
		out = append(out, row{"fold:" + it.ID, it})
	}
	return out
}

func prioGoldenMatrix() []prioGoldenRow {
	var out []prioGoldenRow
	for _, r := range prioGoldenItems() {
		out = append(out, prioGoldenRow{Name: r.name, Prio: r.it.Priority, Reason: r.it.PriorityReason})
	}
	return out
}

func checkPrioGolden(t *testing.T, path string, v any) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if *updatePrioGolden {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("altın %s değişti:\n- %s\n+ %s", path, want, buf.Bytes())
	}
}

func TestInboxPriorityReasonGolden(t *testing.T) {
	checkPrioGolden(t, filepath.Join("testdata", "priority_reason_tr.golden.json"), prioGoldenMatrix())
}

// prioMsgFixture — FE fikstür satırı (frontend/src/lib/prioMsg.lang.test.tsx).
type prioMsgFixture struct {
	Name string          `json:"name"`
	TR   string          `json:"tr"`
	Msg  *chstore.StdMsg `json:"msg"`
}

// TestInboxPriorityReasonMessages — v0.10.2300 (S3): matrisin her satırında
// gerekçenin anahtarı var; sarmalayıcı (kaynak önceliği / bağlı problem) iç
// gerekçeyi "rM" ile taşır. Ladder sarmalayıcıları (…At / anomalyPriority)
// Msg çekirdeğiyle aynı metni döner. Fikstür FE testine gider.
func TestInboxPriorityReasonMessages(t *testing.T) {
	var fx []prioMsgFixture
	for _, r := range prioGoldenItems() {
		m := r.it.PriorityReasonMsg
		if m == nil || m.K == "" {
			t.Errorf("%s: anahtar yok (%q)", r.name, r.it.PriorityReason)
			continue
		}
		fx = append(fx, prioMsgFixture{Name: r.name, TR: r.it.PriorityReason, Msg: m})
	}
	checkPrioGolden(t, filepath.Join("testdata", "priority_reason_msgs.golden.json"), fx)
	g := chstore.ExceptionGroup{Fingerprint: "fp-1", Occurrences: 600, FirstSeen: prioGoldenNow.Add(-48 * time.Hour).UnixNano(), LastSeen: prioGoldenNow.Add(-time.Hour).UnixNano()}
	p1, r1 := exceptionPriorityAt(g, chstore.DefaultExceptionTriage(), prioGoldenNow)
	p2, r2, _ := exceptionPriorityAtMsg(g, chstore.DefaultExceptionTriage(), prioGoldenNow)
	if p1 != p2 || r1 != r2 {
		t.Fatalf("sarmalayıcı ayrıştı: %s %q / %s %q", p1, r1, p2, r2)
	}
}

// TestInboxRowsCarryNoReasonMsg — MCP inbox aracı / CoSRE okuyucusu satırı
// alan alan kopyalar (inboxRowsFrom): anahtar araca GİTMEZ, gerekçe metni aynen.
func TestInboxRowsCarryNoReasonMsg(t *testing.T) {
	items := []InboxItem{}
	for _, r := range prioGoldenItems() {
		items = append(items, r.it)
	}
	withMsg, _ := json.Marshal(inboxRowsFrom(items))
	for i := range items {
		items[i].PriorityReasonMsg = nil
	}
	without, _ := json.Marshal(inboxRowsFrom(items))
	if !bytes.Equal(withMsg, without) || bytes.Contains(withMsg, []byte("prio.")) {
		t.Fatalf("araç satırına anahtar sızdı:\n%s", withMsg)
	}
}
