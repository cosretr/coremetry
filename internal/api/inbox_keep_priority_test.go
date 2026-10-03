package api

// v0.10.1072 — inbox görünüm kuralının (v0.9.487) dar istisnası. Operatör
// (prod, 2026-10-02): "Dün akşam CRM database'inde sorun oldu ama
// problemlerde P1 gelmedi" — kritik hata oranı anomalisi kaynağında P1
// (critical + 14.9× ≥ 2×), inbox'ta P3'tü.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// TestForceNonExceptionP3WithAllowlist — saf çekirdek tablosu.
func TestForceNonExceptionP3WithAllowlist(t *testing.T) {
	def := chstore.DefaultInboxKeepSourcePriority()
	prob := func(rule, sev, prio string) InboxItem {
		return InboxItem{Kind: "problem", Severity: sev, Priority: prio, PriorityReason: "kaynak gerekçe",
			Problem: &InboxProblemRef{RuleID: rule}}
	}
	cases := []struct {
		name       string
		item       InboxItem
		keep       []string
		wantPrio   string
		wantReason string
	}{
		{"kritik hata oranı anomalisi P1 kalır", prob("anomaly:crm-core:error_rate", "critical", "P1"), def,
			"P1", "kaynak önceliği korundu (kritik hata oranı) · kaynak gerekçe"},
		{"warning hata oranı anomalisi P2 kalır", prob("anomaly:crm-core:error_rate", "warning", "P2"), def,
			"P2", "kaynak önceliği korundu (hata oranı anomalisi)"},
		{"gecikme anomalisi P3'e", prob("anomaly:crm-core:p99_latency", "critical", "P1"), def,
			"P3", "tür kuralı"},
		{"terfi anomalisi (trace_op) P3'e", prob("anomaly-auto:7f3a", "critical", "P1"), def,
			"P3", "kaynak önceliği P1"},
		{"yerleşik critical kalır", prob("builtin-error-rate-15pct", "critical", "P1"), def,
			"P1", "(yerleşik kural)"},
		{"db-health kalır", prob("db-health:replication-lag", "critical", "P1"), def,
			"P1", "(DB sağlık kuralı)"},
		{"SLO burn P3'e", prob("slo:checkout:critical", "critical", "P1"), def, "P3", "tür kuralı"},
		{"boş liste = saf v0.9.487", prob("builtin-error-rate-15pct", "critical", "P1"), []string{}, "P3", "tür kuralı"},
		{"operatör kalıbı gerekçede görünür", prob("custom:payments", "critical", "P1"), []string{"custom:*"},
			"P1", "(istisna listesi: custom:*)"},
		{"kritik incident kalır", InboxItem{Kind: "incident", Severity: "critical", Priority: "P1"}, def,
			"P1", "(kritik incident)"},
		{"warning incident P3'e", InboxItem{Kind: "incident", Severity: "warning", Priority: "P2"}, def, "P3", "tür kuralı"},
		{"anomali olay satırının kimliği yok → P3", InboxItem{Kind: "anomaly", Priority: "P1",
			Anomaly: &InboxAnomalyRef{Kind: "trace_op"}}, []string{"anomaly*"}, "P3", "tür kuralı"},
		{"exception dokunulmaz", InboxItem{Kind: "exception", Priority: "P1", PriorityReason: "x"}, nil, "P1", "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := []InboxItem{c.item}
			forceNonExceptionP3With(items, c.keep)
			if items[0].Priority != c.wantPrio {
				t.Fatalf("Priority = %q, want %q (%q)", items[0].Priority, c.wantPrio, items[0].PriorityReason)
			}
			if !strings.Contains(items[0].PriorityReason, c.wantReason) {
				t.Fatalf("PriorityReason = %q, %q içermeli", items[0].PriorityReason, c.wantReason)
			}
		})
	}
}

// TestInboxKeepPriorityHandlerPipeline — handler'ın sırası, CH'siz:
// kaynak satırları gerçek eşleyicilerden (EnrichProblemsWithPriority →
// problemToInbox, incidentToInbox) → forceNonExceptionP3 (global ayar) →
// inboxFacetCounts → applyInboxFacets(P1). Karışık satırlar: korunan anomali
// P1 kalır, trace_op P1 P3'e iner, yerleşik critical kalır, SLO P3'te kalır;
// P1 çipi ile "yalnız P1" süzgeci AYNI kümeyi sayar.
func TestInboxKeepPriorityHandlerPipeline(t *testing.T) {
	prev := chstore.CurrentProblemPriority()
	t.Cleanup(func() { chstore.SetProblemPriority(prev) })
	chstore.SetProblemPriority(chstore.DefaultProblemPriority())

	now := time.Now().UnixNano()
	open := func(id, rule string, v, th float64) chstore.Problem {
		return chstore.Problem{ID: id, RuleID: rule, Severity: "critical", Status: "open",
			Value: v, Threshold: th, StartedAt: now - int64(10*time.Minute)}
	}
	probs := chstore.EnrichProblemsWithPriority([]chstore.Problem{
		open("a", "anomaly:crm-core:error_rate", 14.9, 1), // olay satırı: 14.9×
		open("t", "anomaly-auto:7f3a", 10, 1),             // trace_op terfisi
		open("b", "builtin-error-rate-15pct", 40, 15),     // 2.7×
		open("s", "slo:checkout:critical", 20, 2),         // SLO burn
	})
	var items []InboxItem
	for _, p := range probs {
		if p.Priority != "P1" {
			t.Fatalf("kurulum: %s kaynakta %s, P1 bekleniyordu", p.RuleID, p.Priority)
		}
		items = append(items, problemToInbox(p))
	}
	items = append(items,
		incidentToInbox(chstore.Incident{ID: "i1", Severity: "critical", Status: "open", StartedAt: now}),
		InboxItem{ID: "exception:e", Kind: "exception", Priority: "P1"},
	)

	forceNonExceptionP3(items)
	got := map[string]string{}
	for _, it := range items {
		got[it.ID] = it.Priority
	}
	want := map[string]string{
		"problem:a": "P1", "problem:t": "P3", "problem:b": "P1", "problem:s": "P3",
		"incident:i1": "P1", "exception:e": "P1",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %s, want %s", id, got[id], w)
		}
	}
	counts := inboxFacetCounts(items)
	onlyP1 := applyInboxFacets(append([]InboxItem(nil), items...), inboxKindsAll, []string{"P1"})
	if counts["P1"] != 4 || len(onlyP1) != 4 {
		t.Errorf("P1 çipi %d, yalnız-P1 listesi %d — ikisi de 4 olmalı (anomali, yerleşik, incident, exception)",
			counts["P1"], len(onlyP1))
	}
	if counts["P3"] != 2 {
		t.Errorf("P3 çipi %d, want 2 (trace_op + SLO)", counts["P3"])
	}
}

// TestInboxKeepRuleRunsBeforeFacetCounts — sıra pini: kural facet
// sayaçlarından ÖNCE (yoksa P1 çipi korunan satırları yanlış sayar).
func TestInboxKeepRuleRunsBeforeFacetCounts(t *testing.T) {
	src := readSrc(t, "inbox.go")
	force := strings.Index(src, "\t\tforceNonExceptionP3(items)")
	counts := strings.Index(src, "counts := inboxFacetCounts(items)")
	if force < 0 || counts < 0 || force > counts {
		t.Fatalf("forceNonExceptionP3 (%d) inboxFacetCounts'tan (%d) önce çağrılmalı", force, counts)
	}
}

// TestValidateInboxKeepPatterns — PUT doğrulaması (chstore'daki tek kural).
// Geniş kalıp ("*:*", "*-*") yalnız-yıldız kontrolünü atlatıyordu; artık
// literal önek + en az 3 literal karakter şart. Sayı tavanı normalize
// (tekilleştirilmiş, boşsuz) liste üzerinde.
func TestValidateInboxKeepPatterns(t *testing.T) {
	dups := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		dups = append(dups, "builtin-*", "")
	}
	ok := [][]string{nil, {}, chstore.DefaultInboxKeepSourcePriority(), {"", "  "}, {"slo:checkout:*"}, dups}
	for _, in := range ok {
		if err := chstore.ValidateInboxKeepSourcePriority(in); err != nil {
			t.Errorf("%q reddedildi: %v", in, err)
		}
	}
	many := make([]string, 0, 21)
	for i := 0; i < 21; i++ {
		many = append(many, "rule-"+strings.Repeat("x", i+1))
	}
	bad := [][]string{{"*"}, {" ** "}, {"*:*"}, {"*-*"}, {"*error_rate"}, {"a*b"}, {"ab*"},
		{strings.Repeat("a", 129)}, many}
	for _, in := range bad {
		if err := chstore.ValidateInboxKeepSourcePriority(in); err == nil {
			t.Errorf("%q kabul edildi", in)
		}
	}
}

// TestPutProblemPriorityKeepsStoredList — PUT gövdesi KAYITLI değerin
// üstüne çözülür: alanı göndermeyen istemci kayıtlı listeyi (operatörün
// `[]` ile kapattığı dahil) varsayılana döndürmez.
func TestPutProblemPriorityKeepsStoredList(t *testing.T) {
	custom := []string{"db-health:*"}
	empty := []string{}
	cases := []struct {
		name   string
		stored *[]string
		body   string
		want   []string
	}{
		{"alan yok → kayıtlı özel liste kalır", &custom, `{"bigBreachRatio":3,"staleCriticalHours":4}`, custom},
		{"alan yok → kayıtlı [] kalır (istisna kapalı)", &empty, `{"bigBreachRatio":3,"staleCriticalHours":4}`, []string{}},
		{"alan var → gövde kazanır", &custom, `{"bigBreachRatio":3,"staleCriticalHours":4,"inboxKeepSourcePriority":["builtin-*"]}`, []string{"builtin-*"}},
		{"açık null → varsayılan", &custom, `{"bigBreachRatio":3,"staleCriticalHours":4,"inboxKeepSourcePriority":null}`, chstore.DefaultInboxKeepSourcePriority()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stored := chstore.ProblemPriorityConfig{BigBreachRatio: 2, StaleCriticalHours: 4, InboxKeepSourcePriority: c.stored}
			got, err := decodeProblemPriorityPut(stored, strings.NewReader(c.body))
			if err != nil {
				t.Fatal(err)
			}
			list := chstore.NormalizeProblemPriority(got).InboxKeepSourcePriorityList()
			if strings.Join(list, ",") != strings.Join(c.want, ",") || len(list) != len(c.want) {
				t.Fatalf("liste = %q, want %q", list, c.want)
			}
			if got.BigBreachRatio != 3 {
				t.Errorf("gövde alanı uygulanmadı: %+v", got)
			}
		})
	}
	// Decode kayıtlı değerin dizisini yerinde ezmez.
	if custom[0] != "db-health:*" {
		t.Errorf("kayıtlı dilim yerinde değişti: %q", custom)
	}
	// Alanı hiç göndermeyen gövde staleCriticalHours'ı da KAYITLI değerde tutar.
	got, _ := decodeProblemPriorityPut(chstore.ProblemPriorityConfig{BigBreachRatio: 2, StaleCriticalHours: 0}, strings.NewReader(`{"bigBreachRatio":3}`))
	if got.StaleCriticalHours != 0 {
		t.Errorf("kayıtlı staleCriticalHours 0 (kapalı) ezildi: %v", got.StaleCriticalHours)
	}
}
